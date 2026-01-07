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

func TestNewKServeClient(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := KServeClientConfig{
		AnomalyDetectorURL:     "http://anomaly-detector.test.svc.cluster.local",
		PredictiveAnalyticsURL: "http://predictive-analytics.test.svc.cluster.local",
		Timeout:                10 * time.Second,
	}

	client := NewKServeClient(cfg, log)

	assert.NotNil(t, client)
	assert.Equal(t, "http://anomaly-detector.test.svc.cluster.local", client.anomalyDetectorURL)
	assert.Equal(t, "http://predictive-analytics.test.svc.cluster.local", client.predictiveAnalyticsURL)
	assert.NotNil(t, client.httpClient)
	assert.NotNil(t, client.log)
	assert.True(t, client.HasAnomalyDetector())
	assert.True(t, client.HasPredictiveAnalytics())
}

func TestNewKServeClient_DefaultTimeout(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := KServeClientConfig{
		AnomalyDetectorURL: "http://test:8080",
		// No timeout specified
	}

	client := NewKServeClient(cfg, log)

	assert.Equal(t, 10*time.Second, client.httpClient.Timeout)
}

func TestKServeClient_DetectAnomalies(t *testing.T) {
	// Create mock KServe server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models/anomaly-detector:predict", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Decode request
		var req KServeV1Request
		err := json.NewDecoder(r.Body).Decode(&req)
		assert.NoError(t, err)
		assert.Len(t, req.Instances, 3)

		// Send KServe v1 response
		resp := KServeV1Response{
			Predictions:  []int{-1, 1, -1}, // 2 anomalies, 1 normal
			ModelName:    "anomaly-detector",
			ModelVersion: "v2",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Create client
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL: server.URL,
		Timeout:            30 * time.Second,
	}
	client := NewKServeClient(cfg, log)

	// Make request
	instances := [][]float64{
		{0.5, 1.2, 0.8}, // Anomaly
		{0.3, 0.9, 1.1}, // Normal
		{2.5, 3.0, 4.0}, // Anomaly
	}

	result, err := client.DetectAnomalies(context.Background(), instances)

	// Assert
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Predictions, 3)
	assert.Equal(t, 3, result.Summary.Total)
	assert.Equal(t, 2, result.Summary.AnomaliesFound)
	assert.InDelta(t, 0.666, result.Summary.AnomalyRate, 0.01)
	assert.Equal(t, "anomaly-detector", result.ModelInfo.Name)
	assert.Equal(t, "v2", result.ModelInfo.Version)

	// Check individual predictions
	assert.True(t, result.Predictions[0].IsAnomaly)
	assert.False(t, result.Predictions[1].IsAnomaly)
	assert.True(t, result.Predictions[2].IsAnomaly)
}

func TestKServeClient_DetectAnomalies_NoAnomalies(t *testing.T) {
	// Create mock server that returns no anomalies
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := KServeV1Response{
			Predictions:  []int{1, 1, 1}, // All normal
			ModelName:    "anomaly-detector",
			ModelVersion: "v1",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL: server.URL,
		Timeout:            30 * time.Second,
	}
	client := NewKServeClient(cfg, log)

	result, err := client.DetectAnomalies(context.Background(), [][]float64{{0.1, 0.2, 0.3}, {0.2, 0.3, 0.4}, {0.1, 0.1, 0.1}})

	require.NoError(t, err)
	assert.Equal(t, 0, result.Summary.AnomaliesFound)
	assert.Equal(t, 0.0, result.Summary.AnomalyRate)
}

func TestKServeClient_DetectAnomalies_NotConfigured(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create client without anomaly detector URL
	cfg := KServeClientConfig{
		PredictiveAnalyticsURL: "http://predictive:8080",
	}
	client := NewKServeClient(cfg, log)

	_, err := client.DetectAnomalies(context.Background(), [][]float64{{0.1, 0.2, 0.3}})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "anomaly detector service not configured")
}

func TestKServeClient_PredictFutureIssues(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models/predictive-analytics:predict", r.URL.Path)

		resp := KServeV1Response{
			Predictions:  []int{-1, 1, 1}, // 1 issue predicted (33% anomaly rate)
			ModelName:    "predictive-analytics",
			ModelVersion: "v1",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		PredictiveAnalyticsURL: server.URL,
		Timeout:                30 * time.Second,
	}
	client := NewKServeClient(cfg, log)

	result, err := client.PredictFutureIssues(context.Background(), [][]float64{{0.5, 0.6, 0.7}, {0.3, 0.4, 0.5}, {0.2, 0.2, 0.2}})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Predictions, 3)
	assert.Equal(t, "medium", result.RiskLevel) // 1/3 = 33% anomaly rate → medium risk (20-50%)
	assert.Equal(t, "predictive-analytics", result.ModelInfo.Name)
}

func TestKServeClient_PredictFutureIssues_HighRisk(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := KServeV1Response{
			Predictions:  []int{-1, -1, -1}, // All issues
			ModelName:    "predictive-analytics",
			ModelVersion: "v1",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		PredictiveAnalyticsURL: server.URL,
	}
	client := NewKServeClient(cfg, log)

	result, err := client.PredictFutureIssues(context.Background(), [][]float64{{1.0, 1.0, 1.0}, {1.0, 1.0, 1.0}, {1.0, 1.0, 1.0}})

	require.NoError(t, err)
	assert.Equal(t, "high", result.RiskLevel)
}

func TestKServeClient_PredictFutureIssues_NotConfigured(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	cfg := KServeClientConfig{
		AnomalyDetectorURL: "http://anomaly:8080",
	}
	client := NewKServeClient(cfg, log)

	_, err := client.PredictFutureIssues(context.Background(), [][]float64{{0.1, 0.2, 0.3}})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "predictive analytics service not configured")
}

func TestKServeClient_GetModelMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models/anomaly-detector", r.URL.Path)
		assert.Equal(t, "GET", r.Method)

		metadata := KServeModelMetadata{
			Name:     "anomaly-detector",
			Versions: []string{"v1", "v2"},
			Platform: "sklearn",
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(metadata)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL: server.URL,
	}
	client := NewKServeClient(cfg, log)

	metadata, err := client.GetModelMetadata(context.Background(), server.URL, "anomaly-detector")

	require.NoError(t, err)
	assert.Equal(t, "anomaly-detector", metadata.Name)
	assert.Equal(t, []string{"v1", "v2"}, metadata.Versions)
	assert.Equal(t, "sklearn", metadata.Platform)
}

func TestKServeClient_ListModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models", r.URL.Path)
		assert.Equal(t, "GET", r.Method)

		modelList := KServeModelList{
			Models: []string{"anomaly-detector", "predictive-analytics"},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(modelList)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL: server.URL,
	}
	client := NewKServeClient(cfg, log)

	modelList, err := client.ListModels(context.Background(), server.URL)

	require.NoError(t, err)
	assert.Len(t, modelList.Models, 2)
	assert.Contains(t, modelList.Models, "anomaly-detector")
	assert.Contains(t, modelList.Models, "predictive-analytics")
}

func TestKServeClient_HealthCheck(t *testing.T) {
	// Create healthy anomaly detector server
	anomalyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models/anomaly-detector", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(KServeModelMetadata{Name: "anomaly-detector"})
	}))
	defer anomalyServer.Close()

	// Create healthy predictive analytics server
	predictiveServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/models/predictive-analytics", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(KServeModelMetadata{Name: "predictive-analytics"})
	}))
	defer predictiveServer.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL:     anomalyServer.URL,
		PredictiveAnalyticsURL: predictiveServer.URL,
	}
	client := NewKServeClient(cfg, log)

	err := client.HealthCheck(context.Background())

	assert.NoError(t, err)
}

func TestKServeClient_HealthCheck_Unhealthy(t *testing.T) {
	// Create unhealthy server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL: server.URL,
	}
	client := NewKServeClient(cfg, log)

	err := client.HealthCheck(context.Background())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "KServe health check failed")
}

func TestKServeClient_ErrorHandling_Timeout(t *testing.T) {
	// Create slow server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL: server.URL,
		Timeout:            100 * time.Millisecond,
	}
	client := NewKServeClient(cfg, log)

	_, err := client.DetectAnomalies(context.Background(), [][]float64{{0.1, 0.2, 0.3}})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "request failed")
}

func TestKServeClient_ErrorHandling_InvalidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL: server.URL,
	}
	client := NewKServeClient(cfg, log)

	_, err := client.DetectAnomalies(context.Background(), [][]float64{{0.1, 0.2, 0.3}})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decode response")
}

func TestKServeClient_ErrorHandling_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Model not found"))
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL: server.URL,
	}
	client := NewKServeClient(cfg, log)

	_, err := client.DetectAnomalies(context.Background(), [][]float64{{0.1, 0.2, 0.3}})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 500")
}

func TestKServeClient_ContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL: server.URL,
		Timeout:            30 * time.Second,
	}
	client := NewKServeClient(cfg, log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.DetectAnomalies(ctx, [][]float64{{0.1, 0.2, 0.3}})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "context canceled")
}

func TestKServeClient_Close(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL: "http://test:8080",
	}
	client := NewKServeClient(cfg, log)

	// Close should not panic
	assert.NotPanics(t, func() {
		client.Close()
	})
}

func TestKServeClient_HasAnomalyDetector(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "configured",
			url:      "http://anomaly-detector:8080",
			expected: true,
		},
		{
			name:     "not configured",
			url:      "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := KServeClientConfig{
				AnomalyDetectorURL: tt.url,
			}
			client := NewKServeClient(cfg, log)
			assert.Equal(t, tt.expected, client.HasAnomalyDetector())
		})
	}
}

func TestKServeClient_HasPredictiveAnalytics(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "configured",
			url:      "http://predictive-analytics:8080",
			expected: true,
		},
		{
			name:     "not configured",
			url:      "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := KServeClientConfig{
				PredictiveAnalyticsURL: tt.url,
			}
			client := NewKServeClient(cfg, log)
			assert.Equal(t, tt.expected, client.HasPredictiveAnalytics())
		})
	}
}

func TestKServeClient_CalculateRiskLevel(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	cfg := KServeClientConfig{
		AnomalyDetectorURL: "http://test:8080",
	}
	client := NewKServeClient(cfg, log)

	tests := []struct {
		name        string
		predictions []int
		expected    string
	}{
		{
			name:        "empty predictions",
			predictions: []int{},
			expected:    "low",
		},
		{
			name:        "all normal",
			predictions: []int{1, 1, 1, 1, 1},
			expected:    "low",
		},
		{
			name:        "low risk (< 20%)",
			predictions: []int{-1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, // 10%
			expected:    "low",
		},
		{
			name:        "medium risk (20-50%)",
			predictions: []int{-1, -1, 1, 1, 1, 1, 1, 1, 1, 1}, // 20%
			expected:    "medium",
		},
		{
			name:        "high risk (>= 50%)",
			predictions: []int{-1, -1, -1, -1, -1, 1, 1, 1, 1, 1}, // 50%
			expected:    "high",
		},
		{
			name:        "all anomalies",
			predictions: []int{-1, -1, -1},
			expected:    "high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.calculateRiskLevel(tt.predictions)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestKServeV1Request_JSON(t *testing.T) {
	req := &KServeV1Request{
		Instances: [][]float64{
			{0.5, 1.2, 0.8},
			{0.3, 0.9, 1.1},
		},
	}

	jsonData, err := json.Marshal(req)
	require.NoError(t, err)

	expected := `{"instances":[[0.5,1.2,0.8],[0.3,0.9,1.1]]}`
	assert.JSONEq(t, expected, string(jsonData))
}

func TestKServeV1Response_JSON(t *testing.T) {
	jsonData := `{
		"predictions": [-1, 1],
		"model_name": "anomaly-detector",
		"model_version": "v2"
	}`

	var resp KServeV1Response
	err := json.Unmarshal([]byte(jsonData), &resp)
	require.NoError(t, err)

	assert.Equal(t, []int{-1, 1}, resp.Predictions)
	assert.Equal(t, "anomaly-detector", resp.ModelName)
	assert.Equal(t, "v2", resp.ModelVersion)
}

