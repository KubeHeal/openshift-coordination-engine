// Package integrations provides clients for external service integrations.
package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPrometheusResponse creates a mock Prometheus response
func mockPrometheusResponse(value float64) string {
	resp := PrometheusQueryResponse{
		Status: "success",
	}
	resp.Data.ResultType = "vector"
	resp.Data.Result = []struct {
		Metric map[string]string `json:"metric"`
		Value  []interface{}     `json:"value"`
	}{
		{
			Metric: map[string]string{},
			Value:  []interface{}{float64(time.Now().Unix()), "0.75"},
		},
	}
	// Override value in the response
	resp.Data.Result[0].Value[1] = formatFloat(value)
	data, _ := json.Marshal(resp)
	return string(data)
}

func formatFloat(v float64) string {
	return fmt.Sprintf("%v", v)
}

// mockPrometheusRangeResponse creates a mock Prometheus range query response
func mockPrometheusRangeResponse(values []float64) string {
	resp := PrometheusRangeQueryResponse{
		Status: "success",
	}
	resp.Data.ResultType = "matrix"

	now := time.Now()
	promValues := make([][]interface{}, len(values))
	for i, v := range values {
		ts := float64(now.Add(-time.Duration(len(values)-i) * time.Hour).Unix())
		promValues[i] = []interface{}{ts, formatFloat(v)}
	}

	resp.Data.Result = []struct {
		Metric map[string]string `json:"metric"`
		Values [][]interface{}   `json:"values"`
	}{
		{
			Metric: map[string]string{},
			Values: promValues,
		},
	}

	data, _ := json.Marshal(resp)
	return string(data)
}

// newTestPrometheusClient creates a test client with a mock server
func newTestPrometheusClient(t *testing.T, handler http.HandlerFunc) (*PrometheusClient, *httptest.Server) {
	server := httptest.NewServer(handler)
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	client := NewPrometheusClient(server.URL, 30*time.Second, log)
	return client, server
}

// TestPrometheusClient_BuildQueryWithScope_Pod tests pod-scoped query building
func TestPrometheusClient_BuildQueryWithScope_Pod(t *testing.T) {
	log := logrus.New()
	client := &PrometheusClient{log: log}

	opts := QueryOptions{
		Namespace: "default",
		Pod:       "my-pod-12345",
		Scope:     ScopePod,
	}

	baseQuery := `sum(rate(container_cpu_usage_seconds_total{%s}[5m]))`
	result := client.buildQueryWithScope(baseQuery, opts)

	assert.Contains(t, result, `container!=""`)
	assert.Contains(t, result, `pod="my-pod-12345"`)
	assert.Contains(t, result, `namespace="default"`)
}

// TestPrometheusClient_BuildQueryWithScope_Deployment tests deployment-scoped query building
func TestPrometheusClient_BuildQueryWithScope_Deployment(t *testing.T) {
	log := logrus.New()
	client := &PrometheusClient{log: log}

	opts := QueryOptions{
		Namespace:  "production",
		Deployment: "web-app",
		Scope:      ScopeDeployment,
	}

	baseQuery := `sum(rate(container_cpu_usage_seconds_total{%s}[5m]))`
	result := client.buildQueryWithScope(baseQuery, opts)

	assert.Contains(t, result, `container!=""`)
	assert.Contains(t, result, `pod=~"web-app-.*"`)
	assert.Contains(t, result, `namespace="production"`)
}

// TestPrometheusClient_BuildQueryWithScope_Namespace tests namespace-scoped query building
func TestPrometheusClient_BuildQueryWithScope_Namespace(t *testing.T) {
	log := logrus.New()
	client := &PrometheusClient{log: log}

	opts := QueryOptions{
		Namespace: "kube-system",
		Scope:     ScopeNamespace,
	}

	baseQuery := `sum(container_memory_usage_bytes{%s})`
	result := client.buildQueryWithScope(baseQuery, opts)

	assert.Contains(t, result, `container!=""`)
	assert.Contains(t, result, `namespace="kube-system"`)
	assert.NotContains(t, result, `pod=`)
}

// TestPrometheusClient_BuildQueryWithScope_Cluster tests cluster-scoped query building
func TestPrometheusClient_BuildQueryWithScope_Cluster(t *testing.T) {
	log := logrus.New()
	client := &PrometheusClient{log: log}

	opts := QueryOptions{
		Scope: ScopeCluster,
	}

	baseQuery := `sum(rate(container_cpu_usage_seconds_total{%s}[5m]))`
	result := client.buildQueryWithScope(baseQuery, opts)

	assert.Contains(t, result, `container!=""`)
	assert.NotContains(t, result, `namespace=`)
	assert.NotContains(t, result, `pod=`)
}

// TestPrometheusClient_GetCPUTrend tests CPU trend retrieval
func TestPrometheusClient_GetCPUTrend(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return mock range data
		values := []float64{0.5, 0.55, 0.6, 0.62, 0.65, 0.68, 0.7}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockPrometheusRangeResponse(values)))
	})

	client, server := newTestPrometheusClient(t, handler)
	defer server.Close()

	opts := QueryOptions{
		Namespace: "default",
		Scope:     ScopeNamespace,
	}

	trendData, err := client.GetCPUTrend(context.Background(), opts, 7*24*time.Hour)
	require.NoError(t, err)
	assert.NotNil(t, trendData)
	assert.Greater(t, len(trendData.Points), 0)
	assert.Greater(t, trendData.Current, 0.0)
	assert.Greater(t, trendData.Average, 0.0)
}

// TestPrometheusClient_GetMemoryTrend tests memory trend retrieval
func TestPrometheusClient_GetMemoryTrend(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return mock range data (in bytes)
		values := []float64{1e9, 1.1e9, 1.2e9, 1.15e9, 1.3e9, 1.25e9, 1.4e9}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockPrometheusRangeResponse(values)))
	})

	client, server := newTestPrometheusClient(t, handler)
	defer server.Close()

	opts := QueryOptions{
		Namespace: "production",
		Scope:     ScopeNamespace,
	}

	trendData, err := client.GetMemoryTrend(context.Background(), opts, 7*24*time.Hour)
	require.NoError(t, err)
	assert.NotNil(t, trendData)
	assert.Greater(t, len(trendData.Points), 0)
}

// TestPrometheusClient_CalculateTrend tests trend analysis calculation
func TestPrometheusClient_CalculateTrend(t *testing.T) {
	log := logrus.New()
	client := &PrometheusClient{log: log}

	tests := []struct {
		name              string
		data              *TrendData
		threshold         float64
		expectedDirection string
	}{
		{
			name: "increasing trend",
			data: &TrendData{
				Points: []TrendPoint{
					{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 0.4},
					{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 0.45},
					{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 0.5},
					{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 0.55},
					{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 0.6},
					{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 0.65},
					{Timestamp: time.Now(), Value: 0.7},
				},
				Current: 0.7,
				Average: 0.55,
				Min:     0.4,
				Max:     0.7,
			},
			threshold:         0.85,
			expectedDirection: "increasing",
		},
		{
			name: "decreasing trend",
			data: &TrendData{
				Points: []TrendPoint{
					{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 0.8},
					{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 0.75},
					{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 0.7},
					{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 0.65},
					{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 0.6},
					{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 0.55},
					{Timestamp: time.Now(), Value: 0.5},
				},
				Current: 0.5,
				Average: 0.65,
				Min:     0.5,
				Max:     0.8,
			},
			threshold:         0.85,
			expectedDirection: "decreasing",
		},
		{
			name: "stable trend",
			data: &TrendData{
				Points: []TrendPoint{
					{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 0.5},
					{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 0.51},
					{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 0.49},
					{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 0.5},
					{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 0.52},
					{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 0.48},
					{Timestamp: time.Now(), Value: 0.5},
				},
				Current: 0.5,
				Average: 0.5,
				Min:     0.48,
				Max:     0.52,
			},
			threshold:         0.85,
			expectedDirection: "stable",
		},
		{
			name:              "insufficient data",
			data:              &TrendData{Points: []TrendPoint{{Timestamp: time.Now(), Value: 0.5}}},
			threshold:         0.85,
			expectedDirection: "insufficient_data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			analysis := client.CalculateTrend(tt.data, tt.threshold)
			assert.Equal(t, tt.expectedDirection, analysis.Direction)

			if tt.expectedDirection == "increasing" {
				assert.Greater(t, analysis.DailyChangePercent, 0.0)
				if tt.threshold > 0 && tt.data.Current < tt.threshold {
					assert.GreaterOrEqual(t, analysis.DaysUntilThreshold, 0)
				}
			} else if tt.expectedDirection == "decreasing" {
				assert.Less(t, analysis.DailyChangePercent, 0.0)
				assert.Equal(t, -1, analysis.DaysUntilThreshold)
			}
		})
	}
}

// TestPrometheusClient_LinearRegression tests linear regression calculation
func TestPrometheusClient_LinearRegression(t *testing.T) {
	log := logrus.New()
	client := &PrometheusClient{log: log}

	tests := []struct {
		name           string
		points         []TrendPoint
		expectedSlope  float64
		slopeTolerance float64
	}{
		{
			name: "positive slope",
			points: []TrendPoint{
				{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 10},
				{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 20},
				{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 30},
				{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 40},
				{Timestamp: time.Now(), Value: 50},
			},
			expectedSlope:  10.0, // 10 units per day
			slopeTolerance: 0.1,
		},
		{
			name: "negative slope",
			points: []TrendPoint{
				{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 50},
				{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 40},
				{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 30},
				{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 20},
				{Timestamp: time.Now(), Value: 10},
			},
			expectedSlope:  -10.0,
			slopeTolerance: 0.1,
		},
		{
			name: "zero slope (flat)",
			points: []TrendPoint{
				{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 50},
				{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 50},
				{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 50},
				{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 50},
				{Timestamp: time.Now(), Value: 50},
			},
			expectedSlope:  0.0,
			slopeTolerance: 0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slope, _, rSquared := client.linearRegression(tt.points)
			assert.InDelta(t, tt.expectedSlope, slope, tt.slopeTolerance)
			assert.GreaterOrEqual(t, rSquared, 0.0)
			assert.LessOrEqual(t, rSquared, 1.0)
		})
	}
}

// TestPrometheusClient_InfrastructureMetrics tests infrastructure metric queries
func TestPrometheusClient_InfrastructureMetrics(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("query")
		var value float64

		switch {
		case contains(query, "etcd_object_counts") || contains(query, "apiserver_storage_objects"):
			value = 15000
		case contains(query, "apiserver_request_total"):
			value = 250.5
		case contains(query, "scheduler_pending_pods"):
			value = 5
		case contains(query, "etcd_server_has_leader"):
			value = 1
		default:
			value = 100
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockPrometheusResponse(value)))
	})

	client, server := newTestPrometheusClient(t, handler)
	defer server.Close()

	t.Run("GetETCDObjectCount", func(t *testing.T) {
		count, err := client.GetETCDObjectCount(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 15000, count)
	})

	t.Run("GetAPIServerQPS", func(t *testing.T) {
		qps, err := client.GetAPIServerQPS(context.Background())
		require.NoError(t, err)
		assert.InDelta(t, 250.5, qps, 0.1)
	})

	t.Run("GetSchedulerQueueLength", func(t *testing.T) {
		length, err := client.GetSchedulerQueueLength(context.Background())
		require.NoError(t, err)
		assert.Equal(t, 5, length)
	})

	t.Run("GetControlPlaneHealth", func(t *testing.T) {
		health, err := client.GetControlPlaneHealth(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "healthy", health)
	})
}

// TestPrometheusClient_ScopedCPUUsage tests scoped CPU usage queries
func TestPrometheusClient_ScopedCPUUsage(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockPrometheusResponse(0.75)))
	})

	client, server := newTestPrometheusClient(t, handler)
	defer server.Close()

	tests := []struct {
		name string
		opts QueryOptions
	}{
		{
			name: "pod scope",
			opts: QueryOptions{
				Namespace: "default",
				Pod:       "my-pod-12345",
				Scope:     ScopePod,
			},
		},
		{
			name: "deployment scope",
			opts: QueryOptions{
				Namespace:  "production",
				Deployment: "web-app",
				Scope:      ScopeDeployment,
			},
		},
		{
			name: "namespace scope",
			opts: QueryOptions{
				Namespace: "kube-system",
				Scope:     ScopeNamespace,
			},
		},
		{
			name: "cluster scope",
			opts: QueryOptions{
				Scope: ScopeCluster,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client.ClearCache()
			value, err := client.GetCPUUsage(context.Background(), tt.opts)
			require.NoError(t, err)
			assert.InDelta(t, 0.75, value, 0.01)
		})
	}
}

// TestPrometheusClient_ScopedMemoryUsage tests scoped memory usage queries
func TestPrometheusClient_ScopedMemoryUsage(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockPrometheusResponse(1073741824))) // 1 GB
	})

	client, server := newTestPrometheusClient(t, handler)
	defer server.Close()

	opts := QueryOptions{
		Namespace: "default",
		Scope:     ScopeNamespace,
	}

	value, err := client.GetMemoryUsage(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, int64(1073741824), value)
}

// TestPrometheusClient_Cache tests caching behavior
func TestPrometheusClient_Cache(t *testing.T) {
	callCount := 0
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(mockPrometheusResponse(0.5)))
	})

	client, server := newTestPrometheusClient(t, handler)
	defer server.Close()

	opts := QueryOptions{
		Namespace: "test",
		Scope:     ScopeNamespace,
	}

	// First call should hit the server
	_, err := client.GetCPUUsage(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount)

	// Second call should use cache
	_, err = client.GetCPUUsage(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 1, callCount) // Still 1, cache was used

	// Clear cache and call again
	client.ClearCache()
	_, err = client.GetCPUUsage(context.Background(), opts)
	require.NoError(t, err)
	assert.Equal(t, 2, callCount) // Now 2, cache was cleared
}

// TestPrometheusClient_IsAvailable tests client availability check
func TestPrometheusClient_IsAvailable(t *testing.T) {
	t.Run("available client", func(t *testing.T) {
		log := logrus.New()
		client := NewPrometheusClient("http://localhost:9090", 30*time.Second, log)
		assert.True(t, client.IsAvailable())
	})

	t.Run("nil client", func(t *testing.T) {
		var client *PrometheusClient
		assert.False(t, client.IsAvailable())
	})

	t.Run("empty URL", func(t *testing.T) {
		client := NewPrometheusClient("", 30*time.Second, logrus.New())
		assert.Nil(t, client)
	})
}

// TestFormatDurationForPromQL tests duration formatting for PromQL
func TestFormatDurationForPromQL(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{24 * time.Hour, "1d"},
		{48 * time.Hour, "2d"},
		{168 * time.Hour, "7d"}, // 7 days
		{12 * time.Hour, "12h"},
		{1 * time.Hour, "1h"},
		{30 * time.Minute, "30m"},
		{5 * time.Minute, "5m"},
		{30 * time.Second, "30s"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatDurationForPromQL(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestScopeType tests ScopeType constants
func TestScopeType(t *testing.T) {
	assert.Equal(t, ScopeType("pod"), ScopePod)
	assert.Equal(t, ScopeType("deployment"), ScopeDeployment)
	assert.Equal(t, ScopeType("namespace"), ScopeNamespace)
	assert.Equal(t, ScopeType("cluster"), ScopeCluster)
}

// TestQueryOptions tests QueryOptions struct
func TestQueryOptions(t *testing.T) {
	opts := QueryOptions{
		Namespace:  "production",
		Deployment: "web-app",
		Pod:        "web-app-12345",
		Scope:      ScopeDeployment,
		TimeRange:  24 * time.Hour,
	}

	assert.Equal(t, "production", opts.Namespace)
	assert.Equal(t, "web-app", opts.Deployment)
	assert.Equal(t, "web-app-12345", opts.Pod)
	assert.Equal(t, ScopeDeployment, opts.Scope)
	assert.Equal(t, 24*time.Hour, opts.TimeRange)
}

// TestTrendData tests TrendData struct
func TestTrendData(t *testing.T) {
	data := TrendData{
		Points: []TrendPoint{
			{Timestamp: time.Now().Add(-1 * time.Hour), Value: 0.5},
			{Timestamp: time.Now(), Value: 0.6},
		},
		Current: 0.6,
		Average: 0.55,
		Min:     0.5,
		Max:     0.6,
	}

	assert.Len(t, data.Points, 2)
	assert.Equal(t, 0.6, data.Current)
	assert.Equal(t, 0.55, data.Average)
	assert.Equal(t, 0.5, data.Min)
	assert.Equal(t, 0.6, data.Max)
}

// TestTrendAnalysis tests TrendAnalysis struct
func TestTrendAnalysis(t *testing.T) {
	analysis := TrendAnalysis{
		DailyChangePercent:  2.5,
		WeeklyChangePercent: 17.5,
		Direction:           "increasing",
		DaysUntilThreshold:  30,
		ProjectedDate:       time.Now().AddDate(0, 0, 30),
		Confidence:          0.85,
	}

	assert.Equal(t, 2.5, analysis.DailyChangePercent)
	assert.Equal(t, 17.5, analysis.WeeklyChangePercent)
	assert.Equal(t, "increasing", analysis.Direction)
	assert.Equal(t, 30, analysis.DaysUntilThreshold)
	assert.Equal(t, 0.85, analysis.Confidence)
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
