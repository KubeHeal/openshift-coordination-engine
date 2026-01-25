package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMLClient(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	client := NewMLClient("http://test-ml:8080", 30*time.Second, log)

	assert.NotNil(t, client)
	assert.Equal(t, "http://test-ml:8080", client.baseURL)
	assert.NotNil(t, client.httpClient)
	assert.NotNil(t, client.log)
	assert.Equal(t, 30*time.Second, client.httpClient.Timeout)
}

func TestMLClient_DetectAnomalies(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/anomaly/detect", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Decode request
		var req AnomalyDetectionRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		assert.NoError(t, err)
		assert.Len(t, req.Metrics, 2)

		// Send response
		resp := AnomalyDetectionResponse{
			Anomalies: []Anomaly{
				{
					MetricName: "cpu_usage",
					Value:      95.0,
					Expected:   60.0,
					Deviation:  35.0,
					Severity:   "high",
					Confidence: 0.92,
					Timestamp:  time.Now(),
					Reason:     "CPU usage spike detected",
				},
			},
			Summary: struct {
				Total          int     `json:"total"`
				AnomaliesFound int     `json:"anomalies_found"`
				HighSeverity   int     `json:"high_severity"`
				Confidence     float64 `json:"confidence"`
			}{
				Total:          2,
				AnomaliesFound: 1,
				HighSeverity:   1,
				Confidence:     0.92,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	client := NewMLClient(server.URL, 30*time.Second, log)

	// Make request
	req := &AnomalyDetectionRequest{
		Metrics: []MetricData{
			{Name: "cpu_usage", Value: 95.0, Timestamp: time.Now()},
			{Name: "memory_usage", Value: 45.0, Timestamp: time.Now()},
		},
		Threshold: 0.8,
	}

	resp, err := client.DetectAnomalies(context.Background(), req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Anomalies, 1)
	assert.Equal(t, "cpu_usage", resp.Anomalies[0].MetricName)
	assert.Equal(t, "high", resp.Anomalies[0].Severity)
	assert.Equal(t, 1, resp.Summary.AnomaliesFound)
	assert.Equal(t, 1, resp.Summary.HighSeverity)
}

func TestMLClient_Predict(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/prediction/predict", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		// Decode request
		var req PredictionRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		assert.NoError(t, err)
		assert.Equal(t, "1h", req.PredictAhead)

		// Send response
		resp := PredictionResponse{
			Predictions: []Prediction{
				{
					MetricName:     "cpu_usage",
					PredictedValue: 75.0,
					Confidence:     0.85,
					Timestamp:      time.Now().Add(1 * time.Hour),
					Trend:          "increasing",
					Risk:           "medium",
				},
			},
			Summary: struct {
				TimeRange         string  `json:"time_range"`
				MetricsCount      int     `json:"metrics_count"`
				OverallConfidence float64 `json:"overall_confidence"`
			}{
				TimeRange:         "1h",
				MetricsCount:      1,
				OverallConfidence: 0.85,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	client := NewMLClient(server.URL, 30*time.Second, log)

	// Make request
	req := &PredictionRequest{
		HistoricalMetrics: []MetricData{
			{Name: "cpu_usage", Value: 60.0, Timestamp: time.Now().Add(-2 * time.Hour)},
			{Name: "cpu_usage", Value: 65.0, Timestamp: time.Now().Add(-1 * time.Hour)},
		},
		PredictAhead:      "1h",
		IncludeConfidence: true,
	}

	resp, err := client.Predict(context.Background(), req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Predictions, 1)
	assert.Equal(t, "cpu_usage", resp.Predictions[0].MetricName)
	assert.Equal(t, 75.0, resp.Predictions[0].PredictedValue)
	assert.Equal(t, "increasing", resp.Predictions[0].Trend)
	assert.Equal(t, "medium", resp.Predictions[0].Risk)
}

func TestMLClient_AnalyzePatterns(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/pattern/analyze", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		// Decode request
		var req PatternAnalysisRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		assert.NoError(t, err)

		// Send response
		resp := PatternAnalysisResponse{
			Patterns: []Pattern{
				{
					Type:        "seasonal",
					Description: "Daily spike in CPU usage at 9 AM",
					Metrics:     []string{"cpu_usage"},
					Confidence:  0.90,
					StartTime:   time.Now().Add(-24 * time.Hour),
					EndTime:     time.Now(),
				},
			},
			Insights: []string{
				"CPU usage shows daily pattern",
				"Peak usage correlates with business hours",
			},
			Summary: struct {
				PatternsFound int     `json:"patterns_found"`
				Confidence    float64 `json:"confidence"`
			}{
				PatternsFound: 1,
				Confidence:    0.90,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	client := NewMLClient(server.URL, 30*time.Second, log)

	// Make request
	now := time.Now()
	req := &PatternAnalysisRequest{
		Metrics: []MetricData{
			{Name: "cpu_usage", Value: 60.0, Timestamp: now.Add(-24 * time.Hour)},
			{Name: "cpu_usage", Value: 65.0, Timestamp: now.Add(-12 * time.Hour)},
		},
		AnalysisType: "seasonal",
	}
	req.TimeRange.Start = now.Add(-24 * time.Hour)
	req.TimeRange.End = now

	resp, err := client.AnalyzePatterns(context.Background(), req)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.Patterns, 1)
	assert.Equal(t, "seasonal", resp.Patterns[0].Type)
	assert.Len(t, resp.Insights, 2)
	assert.Equal(t, 1, resp.Summary.PatternsFound)
}

func TestMLClient_HealthCheck(t *testing.T) {
	// Create mock server with healthy response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/health", r.URL.Path)
		assert.Equal(t, "GET", r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	client := NewMLClient(server.URL, 30*time.Second, log)

	// Test health check
	err := client.HealthCheck(context.Background())
	assert.NoError(t, err)
}

func TestMLClient_HealthCheck_Unhealthy(t *testing.T) {
	// Create mock server with unhealthy response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	// Create client
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	client := NewMLClient(server.URL, 30*time.Second, log)

	// Test health check
	err := client.HealthCheck(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "ML service unhealthy")
}

func TestMLClient_ErrorHandling_Timeout(t *testing.T) {
	// Create mock server that delays response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second) // Longer than client timeout
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client with short timeout
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	client := NewMLClient(server.URL, 100*time.Millisecond, log)

	// Make request
	req := &AnomalyDetectionRequest{
		Metrics: []MetricData{
			{Name: "test", Value: 1.0, Timestamp: time.Now()},
		},
	}

	_, err := client.DetectAnomalies(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
}

func TestMLClient_ErrorHandling_InvalidResponse(t *testing.T) {
	// Create mock server with invalid JSON
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	// Create client
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	client := NewMLClient(server.URL, 30*time.Second, log)

	// Make request
	req := &AnomalyDetectionRequest{
		Metrics: []MetricData{
			{Name: "test", Value: 1.0, Timestamp: time.Now()},
		},
	}

	_, err := client.DetectAnomalies(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode response")
}

func TestMLClient_ErrorHandling_ServerError(t *testing.T) {
	// Create mock server with 500 error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	// Create client
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	client := NewMLClient(server.URL, 30*time.Second, log)

	// Make request
	req := &AnomalyDetectionRequest{
		Metrics: []MetricData{
			{Name: "test", Value: 1.0, Timestamp: time.Now()},
		},
	}

	_, err := client.DetectAnomalies(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 500")
}

func TestMLClient_ContextCancellation(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create client
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	client := NewMLClient(server.URL, 30*time.Second, log)

	// Create context that will be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Make request
	req := &AnomalyDetectionRequest{
		Metrics: []MetricData{
			{Name: "test", Value: 1.0, Timestamp: time.Now()},
		},
	}

	_, err := client.DetectAnomalies(ctx, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

func TestMLClient_Close(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	client := NewMLClient("http://test-ml:8080", 30*time.Second, log)

	// Close should not panic
	assert.NotPanics(t, func() {
		client.Close()
	})
}
