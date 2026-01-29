package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tosin2013/openshift-coordination-engine/pkg/kserve"
)

func TestPredictionHandler_HandlePredict_Validation(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewPredictionHandler(nil, nil, log)

	t.Run("invalid hour - too high", func(t *testing.T) {
		reqBody := `{"hour": 25, "day_of_week": 3}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "error", resp.Status)
		assert.Contains(t, resp.Error, "hour must be between 0-23")
		assert.Equal(t, ErrCodeInvalidRequest, resp.Code)
	})

	t.Run("invalid hour - negative", func(t *testing.T) {
		reqBody := `{"hour": -1, "day_of_week": 3}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "hour must be between 0-23")
	})

	t.Run("invalid day_of_week - too high", func(t *testing.T) {
		reqBody := `{"hour": 15, "day_of_week": 7}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "day_of_week must be between 0-6")
	})

	t.Run("invalid day_of_week - negative", func(t *testing.T) {
		reqBody := `{"hour": 15, "day_of_week": -1}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "day_of_week must be between 0-6")
	})

	t.Run("invalid scope", func(t *testing.T) {
		reqBody := `{"hour": 15, "day_of_week": 3, "scope": "invalid"}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "scope must be one of")
	})

	t.Run("pod scope requires pod name", func(t *testing.T) {
		reqBody := `{"hour": 15, "day_of_week": 3, "scope": "pod", "namespace": "test-ns"}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "pod name is required")
	})

	t.Run("deployment scope requires deployment name", func(t *testing.T) {
		reqBody := `{"hour": 15, "day_of_week": 3, "scope": "deployment", "namespace": "test-ns"}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "deployment name is required")
	})

	t.Run("pod scope requires namespace", func(t *testing.T) {
		reqBody := `{"hour": 15, "day_of_week": 3, "scope": "pod", "pod": "my-pod"}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "namespace is required")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		reqBody := `{"hour": invalid}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "Invalid request format")
	})

	t.Run("invalid content type", func(t *testing.T) {
		reqBody := `hour=15&day_of_week=3`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "Content-Type must be application/json")
	})
}

func TestPredictionHandler_HandlePredict_NoKServe(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Handler without KServe client
	handler := NewPredictionHandler(nil, nil, log)

	t.Run("returns error when KServe unavailable", func(t *testing.T) {
		reqBody := `{"hour": 15, "day_of_week": 3, "namespace": "test-ns"}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "error", resp.Status)
		assert.Contains(t, resp.Error, "KServe integration not enabled")
		assert.Equal(t, ErrCodeKServeUnavailable, resp.Code)
	})
}

func TestPredictionHandler_HandlePredict_ModelNotFound(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Set up KServe client with a different model
	os.Setenv("KSERVE_OTHER_MODEL_SERVICE", "other-model-predictor")
	defer os.Unsetenv("KSERVE_OTHER_MODEL_SERVICE")

	cfg := kserve.ProxyConfig{
		Namespace: "test-ns",
		Timeout:   30 * time.Second,
	}

	kserveClient, err := kserve.NewProxyClient(cfg, log)
	require.NoError(t, err)

	handler := NewPredictionHandler(kserveClient, nil, log)

	t.Run("returns error when model not found", func(t *testing.T) {
		// Request default model "predictive-analytics" which doesn't exist
		reqBody := `{"hour": 15, "day_of_week": 3}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "error", resp.Status)
		assert.Contains(t, resp.Error, "Model 'predictive-analytics' not available")
		assert.Equal(t, ErrCodeModelNotFound, resp.Code)
	})

	t.Run("returns error when specified model not found", func(t *testing.T) {
		reqBody := `{"hour": 15, "day_of_week": 3, "model": "non-existent-model"}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "Model 'non-existent-model' not available")
	})
}

func TestPredictionHandler_HandlePredict_WithKServe(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Set up KServe client with predictive-analytics model
	os.Setenv("KSERVE_PREDICTIVE_ANALYTICS_SERVICE", "predictive-analytics-predictor")
	defer os.Unsetenv("KSERVE_PREDICTIVE_ANALYTICS_SERVICE")

	cfg := kserve.ProxyConfig{
		Namespace: "test-ns",
		Timeout:   30 * time.Second,
	}

	kserveClient, err := kserve.NewProxyClient(cfg, log)
	require.NoError(t, err)

	handler := NewPredictionHandler(kserveClient, nil, log)

	t.Run("prediction fails due to service unavailable", func(t *testing.T) {
		// In unit tests, the KServe service is not actually reachable
		reqBody := `{"hour": 15, "day_of_week": 3, "namespace": "test-ns"}`
		req := httptest.NewRequest("POST", "/api/v1/predict", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.HandlePredict(w, req)

		// Should fail because KServe service is not actually running
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)

		var resp PredictErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "error", resp.Status)
		assert.Equal(t, ErrCodePredictionFailed, resp.Code)
	})
}

func TestPredictionHandler_Scoping(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewPredictionHandler(nil, nil, log)

	t.Run("namespace scope from namespace field", func(t *testing.T) {
		req := &PredictRequest{
			Hour:      15,
			DayOfWeek: 3,
			Namespace: "my-namespace",
		}
		handler.setRequestDefaults(req)

		assert.Equal(t, "namespace", req.Scope)
		assert.Equal(t, "my-namespace", handler.getTarget(req))
	})

	t.Run("deployment scope from deployment field", func(t *testing.T) {
		req := &PredictRequest{
			Hour:       15,
			DayOfWeek:  3,
			Namespace:  "my-namespace",
			Deployment: "my-deployment",
		}
		handler.setRequestDefaults(req)

		assert.Equal(t, "deployment", req.Scope)
		assert.Equal(t, "my-namespace/my-deployment", handler.getTarget(req))
	})

	t.Run("pod scope from pod field", func(t *testing.T) {
		req := &PredictRequest{
			Hour:      15,
			DayOfWeek: 3,
			Namespace: "my-namespace",
			Pod:       "my-pod-xyz",
		}
		handler.setRequestDefaults(req)

		assert.Equal(t, "pod", req.Scope)
		assert.Equal(t, "my-namespace/my-pod-xyz", handler.getTarget(req))
	})

	t.Run("cluster scope when no filters", func(t *testing.T) {
		req := &PredictRequest{
			Hour:      15,
			DayOfWeek: 3,
		}
		handler.setRequestDefaults(req)

		assert.Equal(t, "cluster", req.Scope)
		assert.Equal(t, "cluster", handler.getTarget(req))
	})

	t.Run("explicit cluster scope", func(t *testing.T) {
		req := &PredictRequest{
			Hour:      15,
			DayOfWeek: 3,
			Scope:     "cluster",
		}
		handler.setRequestDefaults(req)

		assert.Equal(t, "cluster", req.Scope)
		assert.Equal(t, "cluster", handler.getTarget(req))
	})

	t.Run("default model is predictive-analytics", func(t *testing.T) {
		req := &PredictRequest{
			Hour:      15,
			DayOfWeek: 3,
		}
		handler.setRequestDefaults(req)

		assert.Equal(t, "predictive-analytics", req.Model)
	})

	t.Run("custom model preserved", func(t *testing.T) {
		req := &PredictRequest{
			Hour:      15,
			DayOfWeek: 3,
			Model:     "custom-model",
		}
		handler.setRequestDefaults(req)

		assert.Equal(t, "custom-model", req.Model)
	})
}

func TestPredictionHandler_RegisterRoutes(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewPredictionHandler(nil, nil, log)
	router := mux.NewRouter()

	handler.RegisterRoutes(router)

	// Test that route is registered
	req := httptest.NewRequest("POST", "/api/v1/predict", http.NoBody)
	match := &mux.RouteMatch{}
	assert.True(t, router.Match(req, match))
}

func TestPredictRequest_Structure(t *testing.T) {
	reqJSON := `{
		"hour": 15,
		"day_of_week": 3,
		"namespace": "production",
		"deployment": "my-app",
		"pod": "my-app-xyz",
		"scope": "deployment",
		"model": "predictive-analytics"
	}`

	var req PredictRequest
	err := json.Unmarshal([]byte(reqJSON), &req)
	require.NoError(t, err)

	assert.Equal(t, 15, req.Hour)
	assert.Equal(t, 3, req.DayOfWeek)
	assert.Equal(t, "production", req.Namespace)
	assert.Equal(t, "my-app", req.Deployment)
	assert.Equal(t, "my-app-xyz", req.Pod)
	assert.Equal(t, "deployment", req.Scope)
	assert.Equal(t, "predictive-analytics", req.Model)
}

func TestPredictResponse_Structure(t *testing.T) {
	resp := PredictResponse{
		Status: "success",
		Scope:  "namespace",
		Target: "my-namespace",
		Predictions: PredictionValues{
			CPUPercent:    74.5,
			MemoryPercent: 81.2,
		},
		CurrentMetrics: CurrentMetrics{
			CPURollingMean:    68.2,
			MemoryRollingMean: 74.5,
			Timestamp:         "2026-01-12T14:30:00Z",
			TimeRange:         "24h",
		},
		ModelInfo: ModelInfo{
			Name:       "predictive-analytics",
			Version:    "v1",
			Confidence: 0.92,
		},
		TargetTime: TargetTimeInfo{
			Hour:         15,
			DayOfWeek:    3,
			ISOTimestamp: "2026-01-12T15:00:00Z",
		},
	}

	jsonData, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded PredictResponse
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	assert.Equal(t, resp.Status, decoded.Status)
	assert.Equal(t, resp.Scope, decoded.Scope)
	assert.Equal(t, resp.Target, decoded.Target)
	assert.Equal(t, resp.Predictions.CPUPercent, decoded.Predictions.CPUPercent)
	assert.Equal(t, resp.Predictions.MemoryPercent, decoded.Predictions.MemoryPercent)
	assert.Equal(t, resp.CurrentMetrics.CPURollingMean, decoded.CurrentMetrics.CPURollingMean)
	assert.Equal(t, resp.CurrentMetrics.MemoryRollingMean, decoded.CurrentMetrics.MemoryRollingMean)
	assert.Equal(t, resp.ModelInfo.Name, decoded.ModelInfo.Name)
	assert.Equal(t, resp.ModelInfo.Confidence, decoded.ModelInfo.Confidence)
	assert.Equal(t, resp.TargetTime.Hour, decoded.TargetTime.Hour)
	assert.Equal(t, resp.TargetTime.DayOfWeek, decoded.TargetTime.DayOfWeek)
}

func TestPredictErrorResponse_Structure(t *testing.T) {
	resp := PredictErrorResponse{
		Status:  "error",
		Error:   "Failed to query Prometheus metrics",
		Details: "Connection timeout after 30s",
		Code:    ErrCodePrometheusUnavailable,
	}

	jsonData, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded PredictErrorResponse
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	assert.Equal(t, resp.Status, decoded.Status)
	assert.Equal(t, resp.Error, decoded.Error)
	assert.Equal(t, resp.Details, decoded.Details)
	assert.Equal(t, resp.Code, decoded.Code)
}

func TestCalculateTargetTimestamp(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewPredictionHandler(nil, nil, log)

	t.Run("calculates timestamp for future time", func(t *testing.T) {
		// Test that we get a valid RFC3339 timestamp
		timestamp := handler.calculateTargetTimestamp(15, 3)
		assert.NotEmpty(t, timestamp)

		// Verify it parses correctly
		parsed, err := time.Parse(time.RFC3339, timestamp)
		require.NoError(t, err)

		// Verify hour is correct
		assert.Equal(t, 15, parsed.Hour())
	})

	t.Run("handles boundary hours", func(t *testing.T) {
		// Hour 0 (midnight)
		timestamp := handler.calculateTargetTimestamp(0, 0)
		parsed, err := time.Parse(time.RFC3339, timestamp)
		require.NoError(t, err)
		assert.Equal(t, 0, parsed.Hour())

		// Hour 23
		timestamp = handler.calculateTargetTimestamp(23, 6)
		parsed, err = time.Parse(time.RFC3339, timestamp)
		require.NoError(t, err)
		assert.Equal(t, 23, parsed.Hour())
	})
}

func TestClampPercentage(t *testing.T) {
	assert.Equal(t, 0.0, clampPercentage(-5.0))
	assert.Equal(t, 0.0, clampPercentage(0.0))
	assert.Equal(t, 50.0, clampPercentage(50.0))
	assert.Equal(t, 100.0, clampPercentage(100.0))
	assert.Equal(t, 100.0, clampPercentage(150.0))
}

func TestPredictionHandler_ValidateRequest(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewPredictionHandler(nil, nil, log)

	t.Run("valid request passes", func(t *testing.T) {
		req := &PredictRequest{
			Hour:      15,
			DayOfWeek: 3,
		}
		err := handler.validateRequest(req)
		assert.NoError(t, err)
	})

	t.Run("valid request with all fields", func(t *testing.T) {
		req := &PredictRequest{
			Hour:       15,
			DayOfWeek:  3,
			Namespace:  "production",
			Deployment: "my-app",
			Scope:      "deployment",
			Model:      "predictive-analytics",
		}
		err := handler.validateRequest(req)
		assert.NoError(t, err)
	})

	t.Run("valid namespace scope without namespace", func(t *testing.T) {
		req := &PredictRequest{
			Hour:      15,
			DayOfWeek: 3,
			Scope:     "namespace",
		}
		err := handler.validateRequest(req)
		// Namespace scope without namespace is allowed (falls back to cluster)
		assert.NoError(t, err)
	})

	t.Run("valid cluster scope", func(t *testing.T) {
		req := &PredictRequest{
			Hour:      0,
			DayOfWeek: 0,
			Scope:     "cluster",
		}
		err := handler.validateRequest(req)
		assert.NoError(t, err)
	})
}

func TestPredictionHandler_ProcessPredictions(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewPredictionHandler(nil, nil, log)

	t.Run("issue predicted increases values", func(t *testing.T) {
		resp := &kserve.DetectResponse{
			Predictions: []int{-1}, // Issue predicted
		}
		cpuMean := 0.65
		memMean := 0.72

		cpuPercent, memPercent, confidence := handler.processPredictions(resp, cpuMean, memMean)

		// Should increase the predictions
		assert.Greater(t, cpuPercent, cpuMean*100)
		assert.Greater(t, memPercent, memMean*100)
		assert.Equal(t, 0.92, confidence)
	})

	t.Run("normal operation", func(t *testing.T) {
		resp := &kserve.DetectResponse{
			Predictions: []int{1}, // Normal
		}
		cpuMean := 0.65
		memMean := 0.72

		cpuPercent, memPercent, confidence := handler.processPredictions(resp, cpuMean, memMean)

		// Values should be close to original
		assert.InDelta(t, cpuMean*100, cpuPercent, 10.0)
		assert.InDelta(t, memMean*100, memPercent, 10.0)
		assert.Equal(t, 0.88, confidence)
	})

	t.Run("empty predictions", func(t *testing.T) {
		resp := &kserve.DetectResponse{
			Predictions: []int{},
		}
		cpuMean := 0.65
		memMean := 0.72

		cpuPercent, memPercent, confidence := handler.processPredictions(resp, cpuMean, memMean)

		// Should return base values
		assert.Equal(t, cpuMean*100, cpuPercent)
		assert.Equal(t, memMean*100, memPercent)
		assert.Equal(t, 0.85, confidence)
	})

	t.Run("values clamped to 100", func(t *testing.T) {
		resp := &kserve.DetectResponse{
			Predictions: []int{-1}, // Issue predicted
		}
		cpuMean := 0.95 // Already high
		memMean := 0.98

		cpuPercent, memPercent, _ := handler.processPredictions(resp, cpuMean, memMean)

		// Should be clamped to 100
		assert.LessOrEqual(t, cpuPercent, 100.0)
		assert.LessOrEqual(t, memPercent, 100.0)
	})
}

func TestErrorCodes(t *testing.T) {
	assert.Equal(t, "INVALID_REQUEST", ErrCodeInvalidRequest)
	assert.Equal(t, "PROMETHEUS_UNAVAILABLE", ErrCodePrometheusUnavailable)
	assert.Equal(t, "KSERVE_UNAVAILABLE", ErrCodeKServeUnavailable)
	assert.Equal(t, "MODEL_NOT_FOUND", ErrCodeModelNotFound)
	assert.Equal(t, "PREDICTION_FAILED", ErrCodePredictionFailed)
}

func TestPredictionHandler_ProcessForecastPredictions(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewPredictionHandler(nil, nil, log)

	t.Run("processes forecast with both CPU and memory", func(t *testing.T) {
		resp := &kserve.ForecastResponse{
			Predictions: map[string]kserve.ForecastResult{
				"cpu_usage": {
					Forecast:        []float64{0.65, 0.70, 0.72},
					ForecastHorizon: 3,
					Confidence:      []float64{0.90, 0.85, 0.80},
				},
				"memory_usage": {
					Forecast:        []float64{0.75, 0.78, 0.80},
					ForecastHorizon: 3,
					Confidence:      []float64{0.88, 0.84, 0.80},
				},
			},
			ModelName:      "predictive-analytics",
			ModelVersion:   "1.0.0",
			Timestamp:      "2026-01-14T15:00:00Z",
			LookbackWindow: 24,
		}

		cpuPercent, memPercent, confidence := handler.processForecastPredictions(resp, 0.60, 0.70)

		// Should use first forecast value * 100
		assert.Equal(t, 65.0, cpuPercent)
		assert.Equal(t, 75.0, memPercent)
		// Confidence should be average of both metrics' first confidence
		assert.Equal(t, 0.89, confidence) // (0.90 + 0.88) / 2
	})

	t.Run("processes forecast with only CPU", func(t *testing.T) {
		resp := &kserve.ForecastResponse{
			Predictions: map[string]kserve.ForecastResult{
				"cpu_usage": {
					Forecast:        []float64{0.55},
					ForecastHorizon: 1,
					Confidence:      []float64{0.92},
				},
			},
			ModelName: "predictive-analytics",
		}

		cpuPercent, memPercent, confidence := handler.processForecastPredictions(resp, 0.60, 0.70)

		// CPU should use forecast, memory should fall back to rolling mean
		assert.InDelta(t, 55.0, cpuPercent, 0.001)
		assert.InDelta(t, 70.0, memPercent, 0.001) // Rolling mean * 100
		assert.Equal(t, 0.92, confidence)
	})

	t.Run("processes forecast with only memory", func(t *testing.T) {
		resp := &kserve.ForecastResponse{
			Predictions: map[string]kserve.ForecastResult{
				"memory_usage": {
					Forecast:        []float64{0.82},
					ForecastHorizon: 1,
					Confidence:      []float64{0.87},
				},
			},
			ModelName: "predictive-analytics",
		}

		cpuPercent, memPercent, confidence := handler.processForecastPredictions(resp, 0.60, 0.70)

		// CPU should fall back to rolling mean, memory should use forecast
		assert.Equal(t, 60.0, cpuPercent) // Rolling mean * 100
		assert.Equal(t, 82.0, memPercent)
		assert.Equal(t, 0.87, confidence)
	})

	t.Run("handles empty predictions", func(t *testing.T) {
		resp := &kserve.ForecastResponse{
			Predictions: map[string]kserve.ForecastResult{},
			ModelName:   "predictive-analytics",
		}

		cpuPercent, memPercent, confidence := handler.processForecastPredictions(resp, 0.60, 0.70)

		// Should fall back to rolling means
		assert.Equal(t, 60.0, cpuPercent)
		assert.Equal(t, 70.0, memPercent)
		assert.Equal(t, 0.85, confidence) // Base confidence
	})

	t.Run("handles empty forecast arrays", func(t *testing.T) {
		resp := &kserve.ForecastResponse{
			Predictions: map[string]kserve.ForecastResult{
				"cpu_usage": {
					Forecast:   []float64{},
					Confidence: []float64{},
				},
			},
			ModelName: "predictive-analytics",
		}

		cpuPercent, memPercent, confidence := handler.processForecastPredictions(resp, 0.60, 0.70)

		// Should fall back to rolling means
		assert.Equal(t, 60.0, cpuPercent)
		assert.Equal(t, 70.0, memPercent)
		assert.Equal(t, 0.85, confidence)
	})

	t.Run("clamps values over 100", func(t *testing.T) {
		resp := &kserve.ForecastResponse{
			Predictions: map[string]kserve.ForecastResult{
				"cpu_usage": {
					Forecast:   []float64{1.2}, // 120%
					Confidence: []float64{0.9},
				},
				"memory_usage": {
					Forecast:   []float64{1.5}, // 150%
					Confidence: []float64{0.85},
				},
			},
			ModelName: "predictive-analytics",
		}

		cpuPercent, memPercent, _ := handler.processForecastPredictions(resp, 0.60, 0.70)

		// Should be clamped to 100
		assert.Equal(t, 100.0, cpuPercent)
		assert.Equal(t, 100.0, memPercent)
	})

	t.Run("clamps negative values", func(t *testing.T) {
		resp := &kserve.ForecastResponse{
			Predictions: map[string]kserve.ForecastResult{
				"cpu_usage": {
					Forecast:   []float64{-0.1},
					Confidence: []float64{0.5},
				},
			},
			ModelName: "predictive-analytics",
		}

		cpuPercent, _, _ := handler.processForecastPredictions(resp, 0.60, 0.70)

		// Should be clamped to 0
		assert.Equal(t, 0.0, cpuPercent)
	})
}

func TestPredictionHandler_ProcessAnomalyPredictions(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewPredictionHandler(nil, nil, log)

	t.Run("issue predicted increases values", func(t *testing.T) {
		resp := &kserve.DetectResponse{
			Predictions: []int{-1}, // Issue predicted
		}
		cpuMean := 0.65
		memMean := 0.72

		cpuPercent, memPercent, confidence := handler.processAnomalyPredictions(resp, cpuMean, memMean)

		// Should increase the predictions
		assert.Greater(t, cpuPercent, cpuMean*100)
		assert.Greater(t, memPercent, memMean*100)
		assert.Equal(t, 0.92, confidence)
	})

	t.Run("normal operation", func(t *testing.T) {
		resp := &kserve.DetectResponse{
			Predictions: []int{1}, // Normal
		}
		cpuMean := 0.65
		memMean := 0.72

		cpuPercent, memPercent, confidence := handler.processAnomalyPredictions(resp, cpuMean, memMean)

		// Values should be close to original
		assert.InDelta(t, cpuMean*100, cpuPercent, 10.0)
		assert.InDelta(t, memMean*100, memPercent, 10.0)
		assert.Equal(t, 0.88, confidence)
	})
}

// =============================================================================
// Issue #57 Tests: ENABLE_FEATURE_ENGINEERING Configuration
// =============================================================================

// TestPredictionHandlerConfig_Struct tests the PredictionHandlerConfig struct
func TestPredictionHandlerConfig_Struct(t *testing.T) {
	t.Run("config with feature engineering enabled", func(t *testing.T) {
		config := PredictionHandlerConfig{
			EnableFeatureEngineering: true,
			LookbackHours:            24,
			ExpectedFeatureCount:     3264,
		}

		assert.True(t, config.EnableFeatureEngineering)
		assert.Equal(t, 24, config.LookbackHours)
		assert.Equal(t, 3264, config.ExpectedFeatureCount)
	})

	t.Run("config with feature engineering disabled", func(t *testing.T) {
		config := PredictionHandlerConfig{
			EnableFeatureEngineering: false,
			LookbackHours:            0,
			ExpectedFeatureCount:     5,
		}

		assert.False(t, config.EnableFeatureEngineering)
		assert.Equal(t, 0, config.LookbackHours)
		assert.Equal(t, 5, config.ExpectedFeatureCount)
	})
}

// TestDefaultPredictionHandlerConfig tests the default configuration values
func TestDefaultPredictionHandlerConfig(t *testing.T) {
	config := DefaultPredictionHandlerConfig()

	// Default should have feature engineering enabled (for backward compatibility)
	assert.True(t, config.EnableFeatureEngineering, "Default config should have feature engineering enabled")
	assert.Equal(t, 24, config.LookbackHours, "Default lookback should be 24 hours")
	assert.Equal(t, 0, config.ExpectedFeatureCount, "Default expected count should be 0 (validation disabled)")
}

// TestNewPredictionHandlerWithConfig_FeatureEngineeringDisabled tests that config is respected (Issue #57)
func TestNewPredictionHandlerWithConfig_FeatureEngineeringDisabled(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	// Create config with feature engineering DISABLED
	config := PredictionHandlerConfig{
		EnableFeatureEngineering: false,
		LookbackHours:            0,
		ExpectedFeatureCount:     5,
	}

	// Create handler with explicit config
	handler := NewPredictionHandlerWithConfig(nil, nil, log, config)

	// Verify the handler respects the config
	assert.False(t, handler.IsFeatureEngineeringEnabled(),
		"Feature engineering should be disabled when config.EnableFeatureEngineering=false")
	assert.Nil(t, handler.GetFeatureInfo(),
		"Feature info should be nil when feature engineering is disabled")
}

// TestNewPredictionHandlerWithConfig_FeatureEngineeringEnabled tests enabled config path
func TestNewPredictionHandlerWithConfig_FeatureEngineeringEnabled(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	// Create config with feature engineering ENABLED
	config := PredictionHandlerConfig{
		EnableFeatureEngineering: true,
		LookbackHours:            24,
		ExpectedFeatureCount:     3264,
	}

	// Create handler with explicit config (no Prometheus client)
	handler := NewPredictionHandlerWithConfig(nil, nil, log, config)

	// Feature engineering should be enabled in config but feature builder nil due to no Prometheus
	assert.False(t, handler.IsFeatureEngineeringEnabled(),
		"Feature engineering should be disabled without Prometheus client")
	assert.Nil(t, handler.GetFeatureInfo(),
		"Feature info should be nil without Prometheus client")
}

// TestNewPredictionHandlerWithConfig_RespectsConfig verifies handler stores config correctly
func TestNewPredictionHandlerWithConfig_RespectsConfig(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	tests := []struct {
		name                     string
		enableFeatureEngineering bool
		expectedEnabled          bool
	}{
		{
			name:                     "feature engineering explicitly disabled",
			enableFeatureEngineering: false,
			expectedEnabled:          false,
		},
		{
			name:                     "feature engineering explicitly enabled (no prometheus)",
			enableFeatureEngineering: true,
			expectedEnabled:          false, // No Prometheus, so still disabled
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := PredictionHandlerConfig{
				EnableFeatureEngineering: tt.enableFeatureEngineering,
				LookbackHours:            24,
				ExpectedFeatureCount:     0,
			}

			handler := NewPredictionHandlerWithConfig(nil, nil, log, config)

			assert.Equal(t, tt.expectedEnabled, handler.IsFeatureEngineeringEnabled(),
				"IsFeatureEngineeringEnabled() should return %v", tt.expectedEnabled)
		})
	}
}

// TestPredictionHandler_BuildRawMetricInstances tests raw metric feature building
func TestPredictionHandler_BuildRawMetricInstances(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create handler with feature engineering disabled
	config := PredictionHandlerConfig{
		EnableFeatureEngineering: false,
		ExpectedFeatureCount:     4, // Raw metrics use 4 features
	}
	handler := NewPredictionHandlerWithConfig(nil, nil, log, config)

	t.Run("builds 4 raw features", func(t *testing.T) {
		instances := handler.buildRawMetricInstances(15, 3, 0.65, 0.72)

		require.Len(t, instances, 1, "Should return single instance")
		require.Len(t, instances[0], 4, "Raw metrics should have exactly 4 features")

		// Verify feature order: [hour, day_of_week, cpu_rolling_mean, memory_rolling_mean]
		assert.Equal(t, 15.0, instances[0][0], "Feature 0 should be hour")
		assert.Equal(t, 3.0, instances[0][1], "Feature 1 should be day_of_week")
		assert.Equal(t, 0.65, instances[0][2], "Feature 2 should be cpu_rolling_mean")
		assert.Equal(t, 0.72, instances[0][3], "Feature 3 should be memory_rolling_mean")
	})

	t.Run("handles boundary values", func(t *testing.T) {
		// Test hour 0, day 0
		instances := handler.buildRawMetricInstances(0, 0, 0.0, 0.0)
		assert.Equal(t, 0.0, instances[0][0])
		assert.Equal(t, 0.0, instances[0][1])

		// Test hour 23, day 6
		instances = handler.buildRawMetricInstances(23, 6, 1.0, 1.0)
		assert.Equal(t, 23.0, instances[0][0])
		assert.Equal(t, 6.0, instances[0][1])
	})
}

// TestPredictionHandler_IsFeatureEngineeringEnabled tests the helper method
func TestPredictionHandler_IsFeatureEngineeringEnabled(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	t.Run("returns false when config disabled", func(t *testing.T) {
		config := PredictionHandlerConfig{
			EnableFeatureEngineering: false,
		}
		handler := NewPredictionHandlerWithConfig(nil, nil, log, config)
		assert.False(t, handler.IsFeatureEngineeringEnabled())
	})

	t.Run("returns false when enabled but no prometheus", func(t *testing.T) {
		config := PredictionHandlerConfig{
			EnableFeatureEngineering: true,
		}
		handler := NewPredictionHandlerWithConfig(nil, nil, log, config)
		assert.False(t, handler.IsFeatureEngineeringEnabled())
	})
}

// TestPredictionHandler_GetFeatureInfo tests the feature info method
func TestPredictionHandler_GetFeatureInfo(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	t.Run("returns nil when feature engineering disabled", func(t *testing.T) {
		config := PredictionHandlerConfig{
			EnableFeatureEngineering: false,
		}
		handler := NewPredictionHandlerWithConfig(nil, nil, log, config)
		assert.Nil(t, handler.GetFeatureInfo())
	})

	t.Run("returns nil when no prometheus client", func(t *testing.T) {
		config := PredictionHandlerConfig{
			EnableFeatureEngineering: true,
			LookbackHours:            24,
		}
		handler := NewPredictionHandlerWithConfig(nil, nil, log, config)
		assert.Nil(t, handler.GetFeatureInfo())
	})
}

// TestPredictionHandlerWithConfig_LogMessages tests that appropriate log messages are generated
func TestPredictionHandlerWithConfig_LogMessages(t *testing.T) {
	t.Run("logs disabled message when feature engineering disabled", func(t *testing.T) {
		// Create a logger with a hook to capture log entries
		log := logrus.New()
		log.SetLevel(logrus.DebugLevel)

		var logEntries []logrus.Entry
		log.AddHook(&testLogHook{entries: &logEntries})

		config := PredictionHandlerConfig{
			EnableFeatureEngineering: false,
			ExpectedFeatureCount:     5,
		}

		_ = NewPredictionHandlerWithConfig(nil, nil, log, config)

		// Check that "disabled" message was logged
		found := false
		for _, entry := range logEntries {
			if entry.Level == logrus.InfoLevel &&
				(entry.Message == "Predictive feature engineering disabled, using raw metrics only") {
				found = true
				// Verify expected_feature_count is in the log fields
				if val, ok := entry.Data["expected_feature_count"]; ok {
					assert.Equal(t, 5, val)
				}
				break
			}
		}
		assert.True(t, found, "Should log 'feature engineering disabled' message")
	})

	t.Run("logs warning when enabled but no prometheus", func(t *testing.T) {
		log := logrus.New()
		log.SetLevel(logrus.DebugLevel)

		var logEntries []logrus.Entry
		log.AddHook(&testLogHook{entries: &logEntries})

		config := PredictionHandlerConfig{
			EnableFeatureEngineering: true,
			LookbackHours:            24,
		}

		_ = NewPredictionHandlerWithConfig(nil, nil, log, config)

		// Check that warning message was logged
		found := false
		for _, entry := range logEntries {
			if entry.Level == logrus.WarnLevel &&
				entry.Message == "Feature engineering enabled but Prometheus not available, falling back to raw metrics" {
				found = true
				break
			}
		}
		assert.True(t, found, "Should log warning when prometheus not available")
	})
}

// testLogHook is a test hook for capturing log entries
type testLogHook struct {
	entries *[]logrus.Entry
}

func (h *testLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *testLogHook) Fire(entry *logrus.Entry) error {
	*h.entries = append(*h.entries, *entry)
	return nil
}

// TestNewPredictionHandler_UsesDefaultConfig verifies deprecated function uses defaults
func TestNewPredictionHandler_UsesDefaultConfig(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// The deprecated NewPredictionHandler should use default config
	handler := NewPredictionHandler(nil, nil, log)

	// Since no Prometheus, feature engineering won't be fully enabled
	// but internally the config should have EnableFeatureEngineering=true (default)
	assert.NotNil(t, handler)
}

// TestIssue57_ConfigPropagation is an integration test for Issue #57
func TestIssue57_ConfigPropagation(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	t.Run("ENABLE_FEATURE_ENGINEERING=false should disable feature engineering", func(t *testing.T) {
		// Simulate what main.go does after the fix
		config := PredictionHandlerConfig{
			EnableFeatureEngineering: false, // Simulates cfg.FeatureEngineering.Enabled = false
			LookbackHours:            24,
			ExpectedFeatureCount:     5, // Simple model with 5 features
		}

		handler := NewPredictionHandlerWithConfig(nil, nil, log, config)

		// Key assertion: feature engineering should be disabled
		assert.False(t, handler.IsFeatureEngineeringEnabled(),
			"Issue #57: Feature engineering should be disabled when ENABLE_FEATURE_ENGINEERING=false")
	})

	t.Run("ENABLE_FEATURE_ENGINEERING=true should enable feature engineering (with prometheus)", func(t *testing.T) {
		// When enabled AND prometheus available, feature engineering should work
		config := PredictionHandlerConfig{
			EnableFeatureEngineering: true,
			LookbackHours:            24,
			ExpectedFeatureCount:     3264,
		}

		// Note: Without actual Prometheus, it will log a warning but still create handler
		handler := NewPredictionHandlerWithConfig(nil, nil, log, config)

		// Without Prometheus, feature builder is nil, so still disabled
		assert.False(t, handler.IsFeatureEngineeringEnabled(),
			"Without Prometheus, feature engineering should be disabled even if config enabled")
	})
}
