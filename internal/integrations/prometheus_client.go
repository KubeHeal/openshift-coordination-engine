// Package integrations provides clients for external service integrations.
package integrations

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ScopeType defines the scope of metric queries
type ScopeType string

const (
	// ScopePod queries metrics for a specific pod
	ScopePod ScopeType = "pod"
	// ScopeDeployment queries metrics for pods belonging to a deployment
	ScopeDeployment ScopeType = "deployment"
	// ScopeNamespace queries metrics for all pods in a namespace
	ScopeNamespace ScopeType = "namespace"
	// ScopeCluster queries metrics across the entire cluster
	ScopeCluster ScopeType = "cluster"
)

// QueryOptions specifies filtering options for Prometheus queries
type QueryOptions struct {
	Namespace  string        // Filter by namespace
	Deployment string        // Filter by deployment name (matches pod prefix)
	Pod        string        // Filter by exact pod name
	Scope      ScopeType     // Query scope level
	TimeRange  time.Duration // Time range for historical queries
}

// TrendPoint represents a single data point for trend analysis
type TrendPoint struct {
	Timestamp time.Time `json:"timestamp"`
	Value     float64   `json:"value"`
}

// TrendData contains historical metric data with summary statistics
type TrendData struct {
	Points  []TrendPoint `json:"points"`
	Current float64      `json:"current"`
	Average float64      `json:"average"`
	Min     float64      `json:"min"`
	Max     float64      `json:"max"`
}

// TrendAnalysis contains the results of trend analysis calculations
type TrendAnalysis struct {
	DailyChangePercent  float64   `json:"daily_change_percent"`
	WeeklyChangePercent float64   `json:"weekly_change_percent"`
	Direction           string    `json:"direction"` // "increasing", "decreasing", "stable"
	DaysUntilThreshold  int       `json:"days_until_threshold"` // -1 if not applicable
	ProjectedDate       time.Time `json:"projected_date,omitempty"`
	Confidence          float64   `json:"confidence"` // 0.0-1.0
}

// PrometheusClient queries Prometheus for cluster metrics
type PrometheusClient struct {
	baseURL    string
	httpClient *http.Client
	log        *logrus.Logger

	// Cache for rolling mean values with TTL
	cache    map[string]cachedMetric
	cacheMu  sync.RWMutex
	cacheTTL time.Duration
}

// cachedMetric holds a cached metric value with expiration
type cachedMetric struct {
	value     float64
	expiresAt time.Time
}

// PrometheusQueryResponse represents the response from Prometheus query API
type PrometheusQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"` // [timestamp, "value"]
		} `json:"result"`
	} `json:"data"`
	Error     string `json:"error,omitempty"`
	ErrorType string `json:"errorType,omitempty"`
}

// NewPrometheusClient creates a new Prometheus query client
func NewPrometheusClient(baseURL string, timeout time.Duration, log *logrus.Logger) *PrometheusClient {
	if baseURL == "" {
		return nil
	}

	// Create HTTP client with TLS configuration for OpenShift's Prometheus
	transport := &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //#nosec G402 -- Required for self-signed certs in OpenShift clusters
		},
	}

	return &PrometheusClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		log:      log,
		cache:    make(map[string]cachedMetric),
		cacheTTL: 5 * time.Minute, // Cache metrics for 5 minutes
	}
}

// Close releases resources held by the client
func (c *PrometheusClient) Close() {
	if c != nil && c.httpClient != nil {
		c.httpClient.CloseIdleConnections()
	}
}

// IsAvailable returns true if the Prometheus client is configured
func (c *PrometheusClient) IsAvailable() bool {
	return c != nil && c.baseURL != ""
}

// GetCPURollingMean returns the 24-hour rolling mean of CPU usage across the cluster
// Query: avg(rate(container_cpu_usage_seconds_total{container!="",pod!=""}[24h]))
func (c *PrometheusClient) GetCPURollingMean(ctx context.Context) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := "cpu_rolling_mean"
	if value, ok := c.getCached(cacheKey); ok {
		return value, nil
	}

	// Query for average CPU usage rate over 24h window
	// This gives us a value between 0 and N (where N is the number of CPU cores used)
	// We normalize it to 0-1 range by dividing by total available CPU
	query := `avg(rate(container_cpu_usage_seconds_total{container!="",pod!=""}[24h]))`

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		c.log.WithError(err).Debug("Failed to query CPU rolling mean from Prometheus")
		return 0, err
	}

	// Normalize to 0-1 range (assuming typical cluster has ~100 cores)
	// In production, you'd query node_cpu_seconds_total to get actual capacity
	normalizedValue := clampToUnitRange(value)

	c.setCached(cacheKey, normalizedValue)
	c.log.WithFields(logrus.Fields{
		"raw_value":        value,
		"normalized_value": normalizedValue,
		"query":            query,
	}).Debug("Retrieved CPU rolling mean from Prometheus")

	return normalizedValue, nil
}

// GetMemoryRollingMean returns the 24-hour rolling mean of memory usage across the cluster
// Query: avg(avg_over_time(container_memory_usage_bytes{container!="",pod!=""}[24h]) / container_spec_memory_limit_bytes{container!="",pod!=""})
func (c *PrometheusClient) GetMemoryRollingMean(ctx context.Context) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := "memory_rolling_mean"
	if value, ok := c.getCached(cacheKey); ok {
		return value, nil
	}

	// Query for average memory usage as a ratio of limits over 24h
	// This gives us a value between 0-1 representing memory utilization
	query := `avg(container_memory_usage_bytes{container!="",pod!=""} / container_spec_memory_limit_bytes{container!="",pod!=""} > 0)`

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		// Try alternative query without limits (node-level)
		c.log.WithError(err).Debug("Container memory ratio query failed, trying node-level query")
		query = `1 - avg(node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes)`
		value, err = c.queryInstant(ctx, query)
		if err != nil {
			c.log.WithError(err).Debug("Failed to query memory rolling mean from Prometheus")
			return 0, err
		}
	}

	// Ensure value is in 0-1 range
	normalizedValue := clampToUnitRange(value)

	c.setCached(cacheKey, normalizedValue)
	c.log.WithFields(logrus.Fields{
		"raw_value":        value,
		"normalized_value": normalizedValue,
		"query":            query,
	}).Debug("Retrieved memory rolling mean from Prometheus")

	return normalizedValue, nil
}

// GetNamespaceCPURollingMean returns CPU rolling mean for a specific namespace
func (c *PrometheusClient) GetNamespaceCPURollingMean(ctx context.Context, namespace string) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := fmt.Sprintf("cpu_rolling_mean_%s", namespace)
	if value, ok := c.getCached(cacheKey); ok {
		return value, nil
	}

	// Build PromQL query with namespace filter
	query := fmt.Sprintf(`avg(rate(container_cpu_usage_seconds_total{container!="",pod!="",namespace=%q}[24h]))`, namespace)

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		return 0, err
	}

	normalizedValue := clampToUnitRange(value)
	c.setCached(cacheKey, normalizedValue)

	return normalizedValue, nil
}

// GetNamespaceMemoryRollingMean returns memory rolling mean for a specific namespace
func (c *PrometheusClient) GetNamespaceMemoryRollingMean(ctx context.Context, namespace string) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := fmt.Sprintf("memory_rolling_mean_%s", namespace)
	if value, ok := c.getCached(cacheKey); ok {
		return value, nil
	}

	// Build PromQL query with namespace filter
	query := fmt.Sprintf(`avg(container_memory_usage_bytes{container!="",pod!="",namespace=%q} / container_spec_memory_limit_bytes{container!="",pod!="",namespace=%q} > 0)`, namespace, namespace)

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		return 0, err
	}

	normalizedValue := clampToUnitRange(value)
	c.setCached(cacheKey, normalizedValue)

	return normalizedValue, nil
}

// GetScopedCPURollingMean returns CPU rolling mean with flexible scoping
// Supports namespace, deployment, and pod filtering
func (c *PrometheusClient) GetScopedCPURollingMean(ctx context.Context, namespace, deployment, pod string) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := fmt.Sprintf("cpu_rolling_mean_scoped_%s_%s_%s", namespace, deployment, pod)
	if value, ok := c.getCached(cacheKey); ok {
		return value, nil
	}

	// Build PromQL query based on scope
	query := c.buildScopedCPUQuery(namespace, deployment, pod)

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		c.log.WithError(err).WithFields(logrus.Fields{
			"namespace":  namespace,
			"deployment": deployment,
			"pod":        pod,
			"query":      query,
		}).Debug("Failed to query scoped CPU rolling mean from Prometheus")
		return 0, err
	}

	normalizedValue := clampToUnitRange(value)
	c.setCached(cacheKey, normalizedValue)

	c.log.WithFields(logrus.Fields{
		"raw_value":        value,
		"normalized_value": normalizedValue,
		"namespace":        namespace,
		"deployment":       deployment,
		"pod":              pod,
	}).Debug("Retrieved scoped CPU rolling mean from Prometheus")

	return normalizedValue, nil
}

// GetScopedMemoryRollingMean returns memory rolling mean with flexible scoping
// Supports namespace, deployment, and pod filtering
func (c *PrometheusClient) GetScopedMemoryRollingMean(ctx context.Context, namespace, deployment, pod string) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := fmt.Sprintf("memory_rolling_mean_scoped_%s_%s_%s", namespace, deployment, pod)
	if value, ok := c.getCached(cacheKey); ok {
		return value, nil
	}

	// Build PromQL query based on scope
	query := c.buildScopedMemoryQuery(namespace, deployment, pod)

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		// Try fallback query without limits
		c.log.WithError(err).Debug("Scoped memory ratio query failed, trying alternative")
		fallbackQuery := c.buildScopedMemoryQueryFallback(namespace, deployment, pod)
		value, err = c.queryInstant(ctx, fallbackQuery)
		if err != nil {
			c.log.WithError(err).WithFields(logrus.Fields{
				"namespace":  namespace,
				"deployment": deployment,
				"pod":        pod,
			}).Debug("Failed to query scoped memory rolling mean from Prometheus")
			return 0, err
		}
	}

	normalizedValue := clampToUnitRange(value)
	c.setCached(cacheKey, normalizedValue)

	c.log.WithFields(logrus.Fields{
		"raw_value":        value,
		"normalized_value": normalizedValue,
		"namespace":        namespace,
		"deployment":       deployment,
		"pod":              pod,
	}).Debug("Retrieved scoped memory rolling mean from Prometheus")

	return normalizedValue, nil
}

// buildScopedCPUQuery constructs a PromQL query for CPU metrics based on scope
func (c *PrometheusClient) buildScopedCPUQuery(namespace, deployment, pod string) string {
	var labelSelectors []string

	// Always exclude empty containers and pods
	labelSelectors = append(labelSelectors, `container!=""`, `pod!=""`)

	// Add namespace filter
	if namespace != "" {
		labelSelectors = append(labelSelectors, fmt.Sprintf(`namespace=%q`, namespace))
	}

	// Add deployment filter (matches pods with deployment prefix)
	if deployment != "" {
		labelSelectors = append(labelSelectors, fmt.Sprintf(`pod=~"%s-.*"`, deployment))
	}

	// Add pod filter (exact match)
	if pod != "" {
		labelSelectors = append(labelSelectors, fmt.Sprintf(`pod=%q`, pod))
	}

	selector := "{" + joinSelectors(labelSelectors) + "}"
	return fmt.Sprintf(`avg(rate(container_cpu_usage_seconds_total%s[24h]))`, selector)
}

// buildScopedMemoryQuery constructs a PromQL query for memory metrics based on scope
func (c *PrometheusClient) buildScopedMemoryQuery(namespace, deployment, pod string) string {
	var labelSelectors []string

	// Always exclude empty containers and pods
	labelSelectors = append(labelSelectors, `container!=""`, `pod!=""`)

	// Add namespace filter
	if namespace != "" {
		labelSelectors = append(labelSelectors, fmt.Sprintf(`namespace=%q`, namespace))
	}

	// Add deployment filter (matches pods with deployment prefix)
	if deployment != "" {
		labelSelectors = append(labelSelectors, fmt.Sprintf(`pod=~"%s-.*"`, deployment))
	}

	// Add pod filter (exact match)
	if pod != "" {
		labelSelectors = append(labelSelectors, fmt.Sprintf(`pod=%q`, pod))
	}

	selector := "{" + joinSelectors(labelSelectors) + "}"
	return fmt.Sprintf(`avg(container_memory_usage_bytes%s / container_spec_memory_limit_bytes%s > 0)`, selector, selector)
}

// buildScopedMemoryQueryFallback constructs a fallback PromQL query for memory metrics
// Used when container limits are not set
func (c *PrometheusClient) buildScopedMemoryQueryFallback(namespace, deployment, pod string) string {
	var labelSelectors []string

	// Always exclude empty containers and pods
	labelSelectors = append(labelSelectors, `container!=""`, `pod!=""`)

	// Add namespace filter
	if namespace != "" {
		labelSelectors = append(labelSelectors, fmt.Sprintf(`namespace=%q`, namespace))
	}

	// Add deployment filter (matches pods with deployment prefix)
	if deployment != "" {
		labelSelectors = append(labelSelectors, fmt.Sprintf(`pod=~"%s-.*"`, deployment))
	}

	// Add pod filter (exact match)
	if pod != "" {
		labelSelectors = append(labelSelectors, fmt.Sprintf(`pod=%q`, pod))
	}

	selector := "{" + joinSelectors(labelSelectors) + "}"
	// Use working set bytes as a percentage of a reasonable max (2GB per container as baseline)
	return fmt.Sprintf(`avg(container_memory_working_set_bytes%s / 2147483648)`, selector)
}

// joinSelectors joins label selectors with commas
func joinSelectors(selectors []string) string {
	result := ""
	for i, s := range selectors {
		if i > 0 {
			result += ","
		}
		result += s
	}
	return result
}

// queryInstant executes an instant query against Prometheus
func (c *PrometheusClient) queryInstant(ctx context.Context, query string) (float64, error) {
	endpoint := fmt.Sprintf("%s/api/v1/query", c.baseURL)

	// Build request URL with query parameter
	reqURL, err := url.Parse(endpoint)
	if err != nil {
		return 0, fmt.Errorf("failed to parse URL: %w", err)
	}

	params := url.Values{}
	params.Set("query", query)
	reqURL.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	// Add bearer token if available (for OpenShift authentication)
	if token := c.getServiceAccountToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to execute query: %w", err)
	}
	defer closeBody(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("prometheus returned status %d: %s", resp.StatusCode, string(body))
	}

	var promResp PrometheusQueryResponse
	if err := json.Unmarshal(body, &promResp); err != nil {
		return 0, fmt.Errorf("failed to parse response: %w", err)
	}

	if promResp.Status != "success" {
		return 0, fmt.Errorf("prometheus query failed: %s - %s", promResp.ErrorType, promResp.Error)
	}

	if len(promResp.Data.Result) == 0 {
		return 0, fmt.Errorf("no data returned for query: %s", query)
	}

	// Extract value from result
	// Value is [timestamp, "string_value"]
	if len(promResp.Data.Result[0].Value) < 2 {
		return 0, fmt.Errorf("unexpected result format")
	}

	valueStr, ok := promResp.Data.Result[0].Value[1].(string)
	if !ok {
		return 0, fmt.Errorf("unexpected value type in result")
	}

	var value float64
	if _, err := fmt.Sscanf(valueStr, "%f", &value); err != nil {
		return 0, fmt.Errorf("failed to parse value '%s': %w", valueStr, err)
	}

	return value, nil
}

// getServiceAccountToken reads the service account token for in-cluster authentication
func (c *PrometheusClient) getServiceAccountToken() string {
	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		// Not running in-cluster or token not available
		return ""
	}
	return string(token)
}

// getCached returns a cached value if it exists and hasn't expired
func (c *PrometheusClient) getCached(key string) (float64, bool) {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()

	cached, exists := c.cache[key]
	if !exists || time.Now().After(cached.expiresAt) {
		return 0, false
	}
	return cached.value, true
}

// setCached stores a value in the cache with TTL
func (c *PrometheusClient) setCached(key string, value float64) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	c.cache[key] = cachedMetric{
		value:     value,
		expiresAt: time.Now().Add(c.cacheTTL),
	}
}

// ClearCache clears all cached metrics
func (c *PrometheusClient) ClearCache() {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	c.cache = make(map[string]cachedMetric)
}

// closeBody closes the response body and logs any error
func closeBody(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		if err := resp.Body.Close(); err != nil {
			// Log is not available here, so we silently ignore
			// In practice, close errors on read bodies are rare
			_ = err
		}
	}
}

// clampToUnitRange ensures a value is within the 0.0 to 1.0 range
func clampToUnitRange(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
}

// PrometheusRangeQueryResponse represents the response from Prometheus range query API
type PrometheusRangeQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Values [][]interface{}   `json:"values"` // [[timestamp, "value"], ...]
		} `json:"result"`
	} `json:"data"`
	Error     string `json:"error,omitempty"`
	ErrorType string `json:"errorType,omitempty"`
}

// MetricDataPoint represents a single metric data point with timestamp
type MetricDataPoint struct {
	Timestamp time.Time
	Value     float64
}

// GetNamespaceCPUTrend queries historical CPU usage for trending analysis
func (c *PrometheusClient) GetNamespaceCPUTrend(ctx context.Context, namespace, window string) ([]MetricDataPoint, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("prometheus client not available")
	}

	// Query for CPU usage rate over time with 1 hour resolution
	query := fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace=%q,container!=""}[5m]))`, namespace)

	return c.queryRange(ctx, query, window, "1h")
}

// GetNamespaceMemoryTrend queries historical memory usage for trending analysis
func (c *PrometheusClient) GetNamespaceMemoryTrend(ctx context.Context, namespace, window string) ([]MetricDataPoint, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("prometheus client not available")
	}

	// Query for memory usage over time with 1 hour resolution
	query := fmt.Sprintf(`sum(container_memory_usage_bytes{namespace=%q,container!=""})`, namespace)

	return c.queryRange(ctx, query, window, "1h")
}

// GetNamespaceCPUUsage queries current CPU usage for a namespace (in cores)
func (c *PrometheusClient) GetNamespaceCPUUsage(ctx context.Context, namespace string) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := fmt.Sprintf("cpu_usage_%s", namespace)
	if value, ok := c.getCached(cacheKey); ok {
		return value, nil
	}

	query := fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{namespace=%q,container!=""}[5m]))`, namespace)

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		return 0, err
	}

	c.setCached(cacheKey, value)
	return value, nil
}

// GetNamespaceMemoryUsage queries current memory usage for a namespace (in bytes)
func (c *PrometheusClient) GetNamespaceMemoryUsage(ctx context.Context, namespace string) (int64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := fmt.Sprintf("memory_usage_%s", namespace)
	if value, ok := c.getCached(cacheKey); ok {
		return int64(value), nil
	}

	query := fmt.Sprintf(`sum(container_memory_usage_bytes{namespace=%q,container!=""})`, namespace)

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		return 0, err
	}

	c.setCached(cacheKey, value)
	return int64(value), nil
}

// GetClusterCPUUsage queries current total cluster CPU usage (in cores)
func (c *PrometheusClient) GetClusterCPUUsage(ctx context.Context) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := "cluster_cpu_usage"
	if value, ok := c.getCached(cacheKey); ok {
		return value, nil
	}

	query := `sum(rate(container_cpu_usage_seconds_total{container!=""}[5m]))`

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		return 0, err
	}

	c.setCached(cacheKey, value)
	return value, nil
}

// GetClusterMemoryUsage queries current total cluster memory usage (in bytes)
func (c *PrometheusClient) GetClusterMemoryUsage(ctx context.Context) (int64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := "cluster_memory_usage"
	if value, ok := c.getCached(cacheKey); ok {
		return int64(value), nil
	}

	query := `sum(container_memory_usage_bytes{container!=""})`

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		return 0, err
	}

	c.setCached(cacheKey, value)
	return int64(value), nil
}

// GetEtcdObjectCount queries the total number of objects in etcd
func (c *PrometheusClient) GetEtcdObjectCount(ctx context.Context) (int64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	query := `sum(etcd_object_counts)`

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		// Try alternative query
		query = `sum(apiserver_storage_objects)`
		value, err = c.queryInstant(ctx, query)
		if err != nil {
			return 0, err
		}
	}

	return int64(value), nil
}

// GetAPIServerQPS queries the current API server requests per second
func (c *PrometheusClient) GetAPIServerQPS(ctx context.Context) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	query := `sum(rate(apiserver_request_total[5m]))`

	return c.queryInstant(ctx, query)
}

// GetSchedulerQueueLength queries the current scheduler queue length
func (c *PrometheusClient) GetSchedulerQueueLength(ctx context.Context) (int, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	query := `sum(scheduler_pending_pods)`

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		return 0, err
	}

	return int(value), nil
}

// GetControlPlaneHealth queries control plane component health
func (c *PrometheusClient) GetControlPlaneHealth(ctx context.Context) (string, error) {
	if !c.IsAvailable() {
		return "unknown", fmt.Errorf("prometheus client not available")
	}

	// Check if etcd is healthy
	query := `sum(etcd_server_has_leader)`
	value, err := c.queryInstant(ctx, query)
	if err != nil {
		return "unknown", nil
	}

	if value > 0 {
		return "healthy", nil
	}
	return "unhealthy", nil
}

// queryRange executes a range query against Prometheus
func (c *PrometheusClient) queryRange(ctx context.Context, query, window, step string) ([]MetricDataPoint, error) {
	start, end := c.calculateTimeRange(window)

	reqURL, err := c.buildRangeQueryURL(query, start, end, step)
	if err != nil {
		return nil, err
	}

	body, err := c.executeRangeQuery(ctx, reqURL)
	if err != nil {
		return nil, err
	}

	return c.parseRangeResponse(body, query)
}

// calculateTimeRange returns start and end times based on window
func (c *PrometheusClient) calculateTimeRange(window string) (start, end time.Time) {
	end = time.Now()
	switch window {
	case "30d":
		start = end.AddDate(0, 0, -30)
	case "14d":
		start = end.AddDate(0, 0, -14)
	default: // "7d"
		start = end.AddDate(0, 0, -7)
	}
	return start, end
}

// buildRangeQueryURL builds the URL for a range query
func (c *PrometheusClient) buildRangeQueryURL(query string, start, end time.Time, step string) (string, error) {
	endpoint := fmt.Sprintf("%s/api/v1/query_range", c.baseURL)
	reqURL, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("failed to parse URL: %w", err)
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", fmt.Sprintf("%d", start.Unix()))
	params.Set("end", fmt.Sprintf("%d", end.Unix()))
	params.Set("step", step)
	reqURL.RawQuery = params.Encode()

	return reqURL.String(), nil
}

// executeRangeQuery executes the HTTP request for a range query
func (c *PrometheusClient) executeRangeQuery(ctx context.Context, reqURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if token := c.getServiceAccountToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute query: %w", err)
	}
	defer closeBody(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("prometheus returned status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// parseRangeResponse parses the Prometheus range query response
func (c *PrometheusClient) parseRangeResponse(body []byte, query string) ([]MetricDataPoint, error) {
	var promResp PrometheusRangeQueryResponse
	if err := json.Unmarshal(body, &promResp); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if promResp.Status != "success" {
		return nil, fmt.Errorf("prometheus query failed: %s - %s", promResp.ErrorType, promResp.Error)
	}

	if len(promResp.Data.Result) == 0 {
		return nil, fmt.Errorf("no data returned for query: %s", query)
	}

	return c.extractDataPoints(promResp.Data.Result[0].Values), nil
}

// extractDataPoints extracts MetricDataPoints from raw Prometheus values
func (c *PrometheusClient) extractDataPoints(values [][]interface{}) []MetricDataPoint {
	dataPoints := make([]MetricDataPoint, 0, len(values))
	for _, v := range values {
		if dp, ok := c.parseDataPoint(v); ok {
			dataPoints = append(dataPoints, dp)
		}
	}
	return dataPoints
}

// parseDataPoint parses a single data point from Prometheus response
func (c *PrometheusClient) parseDataPoint(values []interface{}) (MetricDataPoint, bool) {
	if len(values) < 2 {
		return MetricDataPoint{}, false
	}

	ts, ok := values[0].(float64)
	if !ok {
		return MetricDataPoint{}, false
	}

	valueStr, ok := values[1].(string)
	if !ok {
		return MetricDataPoint{}, false
	}

	var value float64
	if _, err := fmt.Sscanf(valueStr, "%f", &value); err != nil {
		return MetricDataPoint{}, false
	}

	return MetricDataPoint{
		Timestamp: time.Unix(int64(ts), 0),
		Value:     value,
	}, true
}

// =============================================================================
// Scoped Query Methods (Issue #28 Enhancements)
// =============================================================================

// buildQueryWithScope constructs a PromQL query with scope-based label selectors
func (c *PrometheusClient) buildQueryWithScope(baseQuery string, opts QueryOptions) string {
	filters := []string{`container!=""`}

	switch opts.Scope {
	case ScopePod:
		if opts.Pod != "" {
			filters = append(filters, fmt.Sprintf(`pod=%q`, opts.Pod))
		}
		if opts.Namespace != "" {
			filters = append(filters, fmt.Sprintf(`namespace=%q`, opts.Namespace))
		}
	case ScopeDeployment:
		if opts.Deployment != "" {
			filters = append(filters, fmt.Sprintf(`pod=~"%s-.*"`, opts.Deployment))
		}
		if opts.Namespace != "" {
			filters = append(filters, fmt.Sprintf(`namespace=%q`, opts.Namespace))
		}
	case ScopeNamespace:
		if opts.Namespace != "" {
			filters = append(filters, fmt.Sprintf(`namespace=%q`, opts.Namespace))
		}
	case ScopeCluster:
		// No namespace filter for cluster scope
	default:
		// Default to cluster scope
	}

	filterStr := strings.Join(filters, ",")
	return fmt.Sprintf(baseQuery, filterStr)
}

// GetCPUUsage returns the current CPU usage with scoped query options
func (c *PrometheusClient) GetCPUUsage(ctx context.Context, opts QueryOptions) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := fmt.Sprintf("cpu_usage_scoped_%s_%s_%s_%s", opts.Scope, opts.Namespace, opts.Deployment, opts.Pod)
	if value, ok := c.getCached(cacheKey); ok {
		return value, nil
	}

	query := c.buildQueryWithScope(`sum(rate(container_cpu_usage_seconds_total{%s}[5m]))`, opts)

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		c.log.WithError(err).WithFields(logrus.Fields{
			"scope":      opts.Scope,
			"namespace":  opts.Namespace,
			"deployment": opts.Deployment,
			"pod":        opts.Pod,
			"query":      query,
		}).Debug("Failed to query scoped CPU usage from Prometheus")
		return 0, err
	}

	c.setCached(cacheKey, value)
	return value, nil
}

// GetCPURollingMeanScoped returns the rolling mean CPU usage with scoped query options
func (c *PrometheusClient) GetCPURollingMeanScoped(ctx context.Context, opts QueryOptions) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	window := opts.TimeRange
	if window == 0 {
		window = 24 * time.Hour
	}

	cacheKey := fmt.Sprintf("cpu_rolling_mean_scoped_%s_%s_%s_%s_%v", opts.Scope, opts.Namespace, opts.Deployment, opts.Pod, window)
	if value, ok := c.getCached(cacheKey); ok {
		return value, nil
	}

	windowStr := formatDurationForPromQL(window)
	query := c.buildQueryWithScope(fmt.Sprintf(`avg(rate(container_cpu_usage_seconds_total{%%s}[%s]))`, windowStr), opts)

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		c.log.WithError(err).Debug("Failed to query scoped CPU rolling mean from Prometheus")
		return 0, err
	}

	normalizedValue := clampToUnitRange(value)
	c.setCached(cacheKey, normalizedValue)
	return normalizedValue, nil
}

// GetMemoryUsage returns the current memory usage with scoped query options (in bytes)
func (c *PrometheusClient) GetMemoryUsage(ctx context.Context, opts QueryOptions) (int64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	cacheKey := fmt.Sprintf("memory_usage_scoped_%s_%s_%s_%s", opts.Scope, opts.Namespace, opts.Deployment, opts.Pod)
	if value, ok := c.getCached(cacheKey); ok {
		return int64(value), nil
	}

	query := c.buildQueryWithScope(`sum(container_memory_usage_bytes{%s})`, opts)

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		c.log.WithError(err).WithFields(logrus.Fields{
			"scope":      opts.Scope,
			"namespace":  opts.Namespace,
			"deployment": opts.Deployment,
			"pod":        opts.Pod,
			"query":      query,
		}).Debug("Failed to query scoped memory usage from Prometheus")
		return 0, err
	}

	c.setCached(cacheKey, value)
	return int64(value), nil
}

// GetMemoryRollingMeanScoped returns the rolling mean memory usage with scoped query options (normalized 0-1)
func (c *PrometheusClient) GetMemoryRollingMeanScoped(ctx context.Context, opts QueryOptions) (float64, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	window := opts.TimeRange
	if window == 0 {
		window = 24 * time.Hour
	}

	cacheKey := fmt.Sprintf("memory_rolling_mean_scoped_%s_%s_%s_%s_%v", opts.Scope, opts.Namespace, opts.Deployment, opts.Pod, window)
	if value, ok := c.getCached(cacheKey); ok {
		return value, nil
	}

	windowStr := formatDurationForPromQL(window)
	query := c.buildQueryWithScope(
		fmt.Sprintf(`avg(avg_over_time(container_memory_usage_bytes{%%s}[%s]) / container_spec_memory_limit_bytes{%%s} > 0)`, windowStr),
		opts,
	)
	// Need to apply scope twice for the ratio
	query = c.buildMemoryRatioQuery(opts, windowStr)

	value, err := c.queryInstant(ctx, query)
	if err != nil {
		// Try fallback query without limits
		c.log.WithError(err).Debug("Memory ratio query failed, trying fallback")
		fallbackQuery := c.buildQueryWithScope(
			fmt.Sprintf(`avg(avg_over_time(container_memory_usage_bytes{%%s}[%s]) / 2147483648)`, windowStr),
			opts,
		)
		value, err = c.queryInstant(ctx, fallbackQuery)
		if err != nil {
			return 0, err
		}
	}

	normalizedValue := clampToUnitRange(value)
	c.setCached(cacheKey, normalizedValue)
	return normalizedValue, nil
}

// buildMemoryRatioQuery constructs a memory ratio query with proper scoping
func (c *PrometheusClient) buildMemoryRatioQuery(opts QueryOptions, windowStr string) string {
	filters := []string{`container!=""`}

	switch opts.Scope {
	case ScopePod:
		if opts.Pod != "" {
			filters = append(filters, fmt.Sprintf(`pod=%q`, opts.Pod))
		}
		if opts.Namespace != "" {
			filters = append(filters, fmt.Sprintf(`namespace=%q`, opts.Namespace))
		}
	case ScopeDeployment:
		if opts.Deployment != "" {
			filters = append(filters, fmt.Sprintf(`pod=~"%s-.*"`, opts.Deployment))
		}
		if opts.Namespace != "" {
			filters = append(filters, fmt.Sprintf(`namespace=%q`, opts.Namespace))
		}
	case ScopeNamespace:
		if opts.Namespace != "" {
			filters = append(filters, fmt.Sprintf(`namespace=%q`, opts.Namespace))
		}
	}

	filterStr := strings.Join(filters, ",")
	return fmt.Sprintf(`avg(avg_over_time(container_memory_usage_bytes{%s}[%s]) / container_spec_memory_limit_bytes{%s} > 0)`,
		filterStr, windowStr, filterStr)
}

// =============================================================================
// Trending Analysis Methods (Issue #28 Enhancements)
// =============================================================================

// GetCPUTrend returns CPU trend data for the specified scope and time window
func (c *PrometheusClient) GetCPUTrend(ctx context.Context, opts QueryOptions, window time.Duration) (*TrendData, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("prometheus client not available")
	}

	windowStr := formatDurationForPromQL(window)
	query := c.buildQueryWithScope(
		fmt.Sprintf(`avg_over_time(sum(rate(container_cpu_usage_seconds_total{%%s}[5m]))[%s:1h])`, windowStr),
		opts,
	)

	dataPoints, err := c.queryRangeWithDuration(ctx, query, window, time.Hour)
	if err != nil {
		return nil, err
	}

	return c.buildTrendData(dataPoints), nil
}

// GetMemoryTrend returns memory trend data for the specified scope and time window
func (c *PrometheusClient) GetMemoryTrend(ctx context.Context, opts QueryOptions, window time.Duration) (*TrendData, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("prometheus client not available")
	}

	windowStr := formatDurationForPromQL(window)
	query := c.buildQueryWithScope(
		fmt.Sprintf(`avg_over_time(sum(container_memory_usage_bytes{%%s})[%s:1h])`, windowStr),
		opts,
	)

	dataPoints, err := c.queryRangeWithDuration(ctx, query, window, time.Hour)
	if err != nil {
		return nil, err
	}

	return c.buildTrendData(dataPoints), nil
}

// buildTrendData constructs TrendData from data points
func (c *PrometheusClient) buildTrendData(dataPoints []MetricDataPoint) *TrendData {
	if len(dataPoints) == 0 {
		return &TrendData{}
	}

	trendPoints := make([]TrendPoint, len(dataPoints))
	var sum, min, max float64
	min = math.MaxFloat64

	for i, dp := range dataPoints {
		trendPoints[i] = TrendPoint{
			Timestamp: dp.Timestamp,
			Value:     dp.Value,
		}
		sum += dp.Value
		if dp.Value < min {
			min = dp.Value
		}
		if dp.Value > max {
			max = dp.Value
		}
	}

	current := dataPoints[len(dataPoints)-1].Value
	average := sum / float64(len(dataPoints))

	return &TrendData{
		Points:  trendPoints,
		Current: current,
		Average: average,
		Min:     min,
		Max:     max,
	}
}

// CalculateTrend performs trend analysis on trend data
func (c *PrometheusClient) CalculateTrend(data *TrendData, threshold float64) *TrendAnalysis {
	if data == nil || len(data.Points) < 2 {
		return &TrendAnalysis{
			Direction:          "insufficient_data",
			DaysUntilThreshold: -1,
		}
	}

	// Perform linear regression
	slope, _, rSquared := c.linearRegression(data.Points)

	// Calculate daily change percentage
	dailyChange := 0.0
	if data.Average != 0 {
		dailyChange = (slope / data.Average) * 100
	}

	// Determine direction
	direction := "stable"
	if dailyChange > 0.5 {
		direction = "increasing"
	} else if dailyChange < -0.5 {
		direction = "decreasing"
	}

	// Calculate days until threshold
	daysUntil := -1
	var projectedDate time.Time
	if threshold > 0 && dailyChange > 0 && data.Current < threshold {
		delta := threshold - data.Current
		dailyAbsoluteChange := data.Current * (dailyChange / 100)
		if dailyAbsoluteChange > 0 {
			days := delta / dailyAbsoluteChange
			daysUntil = int(math.Ceil(days))
			projectedDate = time.Now().AddDate(0, 0, daysUntil)
		}
	}

	// Calculate confidence
	confidence := c.calculateTrendConfidence(data.Points, rSquared)

	return &TrendAnalysis{
		DailyChangePercent:  math.Round(dailyChange*100) / 100,
		WeeklyChangePercent: math.Round(dailyChange*7*100) / 100,
		Direction:           direction,
		DaysUntilThreshold:  daysUntil,
		ProjectedDate:       projectedDate,
		Confidence:          confidence,
	}
}

// linearRegression calculates slope, intercept, and R-squared for trend points
func (c *PrometheusClient) linearRegression(points []TrendPoint) (slope, intercept, rSquared float64) {
	n := float64(len(points))
	if n < 2 {
		return 0, 0, 0
	}

	// Convert timestamps to days from start
	startTime := points[0].Timestamp
	x := make([]float64, len(points))
	y := make([]float64, len(points))

	for i, p := range points {
		x[i] = p.Timestamp.Sub(startTime).Hours() / 24.0 // days
		y[i] = p.Value
	}

	// Calculate sums
	var sumX, sumY, sumXY, sumX2, sumY2 float64
	for i := 0; i < len(x); i++ {
		sumX += x[i]
		sumY += y[i]
		sumXY += x[i] * y[i]
		sumX2 += x[i] * x[i]
		sumY2 += y[i] * y[i]
	}

	meanX := sumX / n
	meanY := sumY / n

	// Calculate slope and intercept
	numerator := sumXY - n*meanX*meanY
	denominator := sumX2 - n*meanX*meanX

	if denominator == 0 {
		return 0, meanY, 0
	}

	slope = numerator / denominator
	intercept = meanY - slope*meanX

	// Calculate R-squared
	ssRes := 0.0
	ssTot := 0.0
	for i := 0; i < len(x); i++ {
		predicted := slope*x[i] + intercept
		ssRes += (y[i] - predicted) * (y[i] - predicted)
		ssTot += (y[i] - meanY) * (y[i] - meanY)
	}

	if ssTot == 0 {
		rSquared = 1.0
	} else {
		rSquared = 1.0 - (ssRes / ssTot)
	}

	return slope, intercept, rSquared
}

// calculateTrendConfidence calculates confidence score for trend analysis
func (c *PrometheusClient) calculateTrendConfidence(points []TrendPoint, rSquared float64) float64 {
	if len(points) < 2 {
		return 0
	}

	// Data point factor (0-0.4)
	maxPoints := 168.0 // 7 days * 24 hours
	pointsFactor := math.Min(float64(len(points))/maxPoints, 1.0) * 0.4

	// R-squared factor (0-0.4)
	rSquaredFactor := math.Max(0, rSquared) * 0.4

	// Time span factor (0-0.2)
	timeSpan := points[len(points)-1].Timestamp.Sub(points[0].Timestamp)
	maxSpan := 7 * 24 * time.Hour
	spanFactor := math.Min(timeSpan.Hours()/maxSpan.Hours(), 1.0) * 0.2

	confidence := pointsFactor + rSquaredFactor + spanFactor
	return math.Round(confidence*100) / 100
}

// queryRangeWithDuration executes a range query using time.Duration instead of string
func (c *PrometheusClient) queryRangeWithDuration(ctx context.Context, query string, window, step time.Duration) ([]MetricDataPoint, error) {
	end := time.Now()
	start := end.Add(-window)

	endpoint := fmt.Sprintf("%s/api/v1/query_range", c.baseURL)
	reqURL, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	params := url.Values{}
	params.Set("query", query)
	params.Set("start", fmt.Sprintf("%d", start.Unix()))
	params.Set("end", fmt.Sprintf("%d", end.Unix()))
	params.Set("step", formatDurationForPromQL(step))
	reqURL.RawQuery = params.Encode()

	body, err := c.executeRangeQuery(ctx, reqURL.String())
	if err != nil {
		return nil, err
	}

	return c.parseRangeResponse(body, query)
}

// formatDurationForPromQL formats a duration for use in PromQL queries
func formatDurationForPromQL(d time.Duration) string {
	if d >= 24*time.Hour {
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd", days)
	}
	if d >= time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	if d >= time.Minute {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%ds", int(d.Seconds()))
}

// =============================================================================
// Infrastructure Metrics Methods (Issue #28 Enhancements)
// =============================================================================

// GetETCDObjectCount queries the total number of objects stored in etcd
// Useful for capacity planning and understanding cluster scale
func (c *PrometheusClient) GetETCDObjectCount(ctx context.Context) (int, error) {
	if !c.IsAvailable() {
		return 0, fmt.Errorf("prometheus client not available")
	}

	// Try primary query first
	query := `sum(etcd_object_counts)`
	value, err := c.queryInstant(ctx, query)
	if err != nil {
		// Try alternative query for newer metrics
		query = `sum(apiserver_storage_objects)`
		value, err = c.queryInstant(ctx, query)
		if err != nil {
			// Try another alternative
			query = `sum(etcd_mvcc_db_total_size_in_bytes) / 1024 / 1024` // MB as rough object estimate
			value, err = c.queryInstant(ctx, query)
			if err != nil {
				return 0, fmt.Errorf("failed to query etcd object count: %w", err)
			}
		}
	}

	return int(value), nil
}

// GetAPIServerQPSDetailed returns detailed API server QPS with breakdown by verb
func (c *PrometheusClient) GetAPIServerQPSDetailed(ctx context.Context) (map[string]float64, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("prometheus client not available")
	}

	result := make(map[string]float64)

	// Total QPS
	totalQuery := `sum(rate(apiserver_request_total[5m]))`
	totalValue, err := c.queryInstant(ctx, totalQuery)
	if err == nil {
		result["total"] = totalValue
	}

	// QPS by verb (common operations)
	verbs := []string{"GET", "LIST", "WATCH", "CREATE", "UPDATE", "DELETE", "PATCH"}
	for _, verb := range verbs {
		verbQuery := fmt.Sprintf(`sum(rate(apiserver_request_total{verb=%q}[5m]))`, verb)
		verbValue, err := c.queryInstant(ctx, verbQuery)
		if err == nil {
			result[strings.ToLower(verb)] = verbValue
		}
	}

	return result, nil
}

// GetSchedulerMetrics returns scheduler-related metrics
func (c *PrometheusClient) GetSchedulerMetrics(ctx context.Context) (map[string]interface{}, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("prometheus client not available")
	}

	result := make(map[string]interface{})

	// Pending pods (queue length)
	queueQuery := `sum(scheduler_pending_pods)`
	queueValue, err := c.queryInstant(ctx, queueQuery)
	if err == nil {
		result["queue_length"] = int(queueValue)
	}

	// Scheduling attempts
	attemptsQuery := `sum(rate(scheduler_schedule_attempts_total[5m]))`
	attemptsValue, err := c.queryInstant(ctx, attemptsQuery)
	if err == nil {
		result["scheduling_rate_per_second"] = attemptsValue
	}

	// Scheduling latency (p99)
	latencyQuery := `histogram_quantile(0.99, sum(rate(scheduler_scheduling_attempt_duration_seconds_bucket[5m])) by (le))`
	latencyValue, err := c.queryInstant(ctx, latencyQuery)
	if err == nil {
		result["p99_latency_seconds"] = latencyValue
	}

	// Unschedulable pods
	unschedulableQuery := `sum(scheduler_pending_pods{queue="unschedulable"})`
	unschedulableValue, err := c.queryInstant(ctx, unschedulableQuery)
	if err == nil {
		result["unschedulable_pods"] = int(unschedulableValue)
	}

	return result, nil
}

// GetControllerManagerMetrics returns controller manager metrics
func (c *PrometheusClient) GetControllerManagerMetrics(ctx context.Context) (map[string]interface{}, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("prometheus client not available")
	}

	result := make(map[string]interface{})

	// Work queue depth
	queueDepthQuery := `sum(workqueue_depth)`
	queueDepthValue, err := c.queryInstant(ctx, queueDepthQuery)
	if err == nil {
		result["total_queue_depth"] = int(queueDepthValue)
	}

	// Work queue adds rate
	queueAddsQuery := `sum(rate(workqueue_adds_total[5m]))`
	queueAddsValue, err := c.queryInstant(ctx, queueAddsQuery)
	if err == nil {
		result["queue_adds_per_second"] = queueAddsValue
	}

	// Work queue retries rate
	retriesQuery := `sum(rate(workqueue_retries_total[5m]))`
	retriesValue, err := c.queryInstant(ctx, retriesQuery)
	if err == nil {
		result["retries_per_second"] = retriesValue
	}

	return result, nil
}

// GetInfrastructureHealthSummary returns a comprehensive infrastructure health summary
func (c *PrometheusClient) GetInfrastructureHealthSummary(ctx context.Context) (map[string]interface{}, error) {
	if !c.IsAvailable() {
		return nil, fmt.Errorf("prometheus client not available")
	}

	result := make(map[string]interface{})

	// Control plane health
	controlPlaneHealth, _ := c.GetControlPlaneHealth(ctx)
	result["control_plane_status"] = controlPlaneHealth

	// etcd object count
	etcdCount, err := c.GetETCDObjectCount(ctx)
	if err == nil {
		result["etcd_object_count"] = etcdCount
	}

	// API server QPS
	apiQPS, err := c.GetAPIServerQPS(ctx)
	if err == nil {
		result["api_server_qps"] = apiQPS
	}

	// Scheduler queue
	schedulerQueue, err := c.GetSchedulerQueueLength(ctx)
	if err == nil {
		result["scheduler_queue_length"] = schedulerQueue
	}

	// Cluster CPU/Memory
	clusterCPU, err := c.GetClusterCPUUsage(ctx)
	if err == nil {
		result["cluster_cpu_usage_cores"] = clusterCPU
	}

	clusterMemory, err := c.GetClusterMemoryUsage(ctx)
	if err == nil {
		result["cluster_memory_usage_bytes"] = clusterMemory
	}

	return result, nil
}
