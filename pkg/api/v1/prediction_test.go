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
