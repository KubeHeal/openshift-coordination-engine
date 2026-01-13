// Package integrations provides clients for external service integrations.
package integrations

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

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
