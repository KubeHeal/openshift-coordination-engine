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

func TestAnomalyHandler_AnalyzeAnomalies_Validation(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	t.Run("invalid time_range", func(t *testing.T) {
		reqBody := `{"time_range": "2h"}`
		req := httptest.NewRequest("POST", "/api/v1/anomalies/analyze", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.AnalyzeAnomalies(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp AnomalyErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "error", resp.Status)
		assert.Contains(t, resp.Error, "time_range must be one of")
		assert.Equal(t, ErrCodeAnomalyInvalidRequest, resp.Code)
	})

	t.Run("invalid threshold - too high", func(t *testing.T) {
		reqBody := `{"time_range": "1h", "threshold": 1.5}`
		req := httptest.NewRequest("POST", "/api/v1/anomalies/analyze", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.AnalyzeAnomalies(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp AnomalyErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "threshold must be between 0.0 and 1.0")
	})

	t.Run("invalid threshold - negative", func(t *testing.T) {
		reqBody := `{"time_range": "1h", "threshold": -0.5}`
		req := httptest.NewRequest("POST", "/api/v1/anomalies/analyze", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.AnalyzeAnomalies(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp AnomalyErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "threshold must be between 0.0 and 1.0")
	})

	t.Run("invalid JSON", func(t *testing.T) {
		reqBody := `{"time_range": invalid}`
		req := httptest.NewRequest("POST", "/api/v1/anomalies/analyze", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.AnalyzeAnomalies(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp AnomalyErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "Invalid request format")
	})

	t.Run("invalid content type", func(t *testing.T) {
		reqBody := `time_range=1h&namespace=test`
		req := httptest.NewRequest("POST", "/api/v1/anomalies/analyze", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		handler.AnalyzeAnomalies(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp AnomalyErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "Content-Type must be application/json")
	})
}

func TestAnomalyHandler_AnalyzeAnomalies_NoKServe(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Handler without KServe client
	handler := NewAnomalyHandler(nil, nil, log)

	t.Run("returns error when KServe unavailable", func(t *testing.T) {
		reqBody := `{"time_range": "1h", "namespace": "test-ns"}`
		req := httptest.NewRequest("POST", "/api/v1/anomalies/analyze", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.AnalyzeAnomalies(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)

		var resp AnomalyErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "error", resp.Status)
		assert.Contains(t, resp.Error, "KServe integration not enabled")
		assert.Equal(t, ErrCodeAnomalyKServeUnavailable, resp.Code)
	})
}

func TestAnomalyHandler_AnalyzeAnomalies_ModelNotFound(t *testing.T) {
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

	handler := NewAnomalyHandler(kserveClient, nil, log)

	t.Run("returns error when default model not found", func(t *testing.T) {
		// Request default model "anomaly-detector" which doesn't exist
		reqBody := `{"time_range": "1h"}`
		req := httptest.NewRequest("POST", "/api/v1/anomalies/analyze", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.AnalyzeAnomalies(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)

		var resp AnomalyErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "error", resp.Status)
		assert.Contains(t, resp.Error, "Model 'anomaly-detector' not available")
		assert.Equal(t, ErrCodeAnomalyModelNotFound, resp.Code)
	})

	t.Run("returns error when specified model not found", func(t *testing.T) {
		reqBody := `{"time_range": "1h", "model_name": "non-existent-model"}`
		req := httptest.NewRequest("POST", "/api/v1/anomalies/analyze", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.AnalyzeAnomalies(w, req)

		assert.Equal(t, http.StatusServiceUnavailable, w.Code)

		var resp AnomalyErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Contains(t, resp.Error, "Model 'non-existent-model' not available")
	})
}

func TestAnomalyHandler_AnalyzeAnomalies_WithKServe(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Set up KServe client with anomaly-detector model
	os.Setenv("KSERVE_ANOMALY_DETECTOR_SERVICE", "anomaly-detector-predictor")
	defer os.Unsetenv("KSERVE_ANOMALY_DETECTOR_SERVICE")

	cfg := kserve.ProxyConfig{
		Namespace: "test-ns",
		Timeout:   30 * time.Second,
	}

	kserveClient, err := kserve.NewProxyClient(cfg, log)
	require.NoError(t, err)

	handler := NewAnomalyHandler(kserveClient, nil, log)

	t.Run("analysis fails due to service unavailable", func(t *testing.T) {
		// In unit tests, the KServe service is not actually reachable
		reqBody := `{"time_range": "1h", "namespace": "self-healing-platform"}`
		req := httptest.NewRequest("POST", "/api/v1/anomalies/analyze", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.AnalyzeAnomalies(w, req)

		// Should fail because KServe service is not actually running
		assert.Equal(t, http.StatusServiceUnavailable, w.Code)

		var resp AnomalyErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "error", resp.Status)
		assert.Equal(t, ErrCodeAnomalyAnalysisFailed, resp.Code)
	})
}

func TestAnomalyHandler_RequestDefaults(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	t.Run("default time_range is 1h", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{}
		handler.setRequestDefaults(req)

		assert.Equal(t, "1h", req.TimeRange)
	})

	t.Run("default threshold is 0.7", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{}
		handler.setRequestDefaults(req)

		assert.Equal(t, 0.7, req.Threshold)
	})

	t.Run("default model_name is anomaly-detector", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{}
		handler.setRequestDefaults(req)

		assert.Equal(t, "anomaly-detector", req.ModelName)
	})

	t.Run("custom values preserved", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{
			TimeRange: "24h",
			Threshold: 0.8,
			ModelName: "custom-model",
		}
		handler.setRequestDefaults(req)

		assert.Equal(t, "24h", req.TimeRange)
		assert.Equal(t, 0.8, req.Threshold)
		assert.Equal(t, "custom-model", req.ModelName)
	})
}

func TestAnomalyHandler_ValidateRequest(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	t.Run("valid request with 1h time range", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{TimeRange: "1h", Threshold: 0.7}
		err := handler.validateRequest(req)
		assert.NoError(t, err)
	})

	t.Run("valid request with 6h time range", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{TimeRange: "6h", Threshold: 0.7}
		err := handler.validateRequest(req)
		assert.NoError(t, err)
	})

	t.Run("valid request with 24h time range", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{TimeRange: "24h", Threshold: 0.7}
		err := handler.validateRequest(req)
		assert.NoError(t, err)
	})

	t.Run("valid request with 7d time range", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{TimeRange: "7d", Threshold: 0.7}
		err := handler.validateRequest(req)
		assert.NoError(t, err)
	})

	t.Run("valid threshold at boundaries", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{TimeRange: "1h", Threshold: 0.0}
		err := handler.validateRequest(req)
		assert.NoError(t, err)

		req.Threshold = 1.0
		err = handler.validateRequest(req)
		assert.NoError(t, err)
	})

	t.Run("invalid time range", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{TimeRange: "12h", Threshold: 0.7}
		err := handler.validateRequest(req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "time_range must be one of")
	})

	t.Run("invalid threshold", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{TimeRange: "1h", Threshold: 1.5}
		err := handler.validateRequest(req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "threshold must be between")
	})
}

func TestAnomalyHandler_RegisterRoutes(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)
	router := mux.NewRouter()

	handler.RegisterRoutes(router)

	// Test that route is registered
	req := httptest.NewRequest("POST", "/api/v1/anomalies/analyze", http.NoBody)
	match := &mux.RouteMatch{}
	assert.True(t, router.Match(req, match))
}

func TestAnomalyHandler_BuildScope(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	t.Run("pod scope", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{
			Namespace: "self-healing-platform",
			Pod:       "broken-app-xyz",
		}
		scope := handler.buildScope(req)

		assert.Equal(t, "self-healing-platform", scope.Namespace)
		assert.Equal(t, "broken-app-xyz", scope.Pod)
		assert.Contains(t, scope.TargetDescription, "pod 'broken-app-xyz'")
		assert.Contains(t, scope.TargetDescription, "namespace 'self-healing-platform'")
	})

	t.Run("deployment scope", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{
			Namespace:  "self-healing-platform",
			Deployment: "broken-app",
		}
		scope := handler.buildScope(req)

		assert.Equal(t, "self-healing-platform", scope.Namespace)
		assert.Equal(t, "broken-app", scope.Deployment)
		assert.Contains(t, scope.TargetDescription, "deployment 'broken-app'")
	})

	t.Run("namespace scope", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{
			Namespace: "self-healing-platform",
		}
		scope := handler.buildScope(req)

		assert.Equal(t, "self-healing-platform", scope.Namespace)
		assert.Contains(t, scope.TargetDescription, "namespace 'self-healing-platform'")
	})

	t.Run("cluster-wide scope", func(t *testing.T) {
		req := &AnomalyAnalyzeRequest{}
		scope := handler.buildScope(req)

		assert.Equal(t, "cluster-wide", scope.TargetDescription)
	})
}

func TestAnomalyHandler_BuildFeatureInfo(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	featureInfo := handler.buildFeatureInfo()

	assert.Equal(t, 45, featureInfo.TotalFeatures)
	assert.Equal(t, 9, featureInfo.FeaturesPerMetric)
	assert.Equal(t, 5, len(featureInfo.BaseMetrics))
	assert.Equal(t, 45, len(featureInfo.FeatureNames))

	// Verify base metrics
	assert.Contains(t, featureInfo.BaseMetrics, "node_cpu_utilization")
	assert.Contains(t, featureInfo.BaseMetrics, "node_memory_utilization")
	assert.Contains(t, featureInfo.BaseMetrics, "pod_cpu_usage")
	assert.Contains(t, featureInfo.BaseMetrics, "pod_memory_usage")
	assert.Contains(t, featureInfo.BaseMetrics, "container_restart_count")

	// Verify feature names include expected patterns
	assert.Contains(t, featureInfo.FeatureNames, "node_cpu_utilization_value")
	assert.Contains(t, featureInfo.FeatureNames, "node_cpu_utilization_mean_5m")
	assert.Contains(t, featureInfo.FeatureNames, "pod_memory_usage_pct_change")
}

func TestAnomalyHandler_GetDefaultFeatures(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	features := handler.getDefaultFeatures()

	assert.Equal(t, 45, len(features))

	// Check structure - 5 metrics × 9 features each
	for i := 0; i < 5; i++ {
		baseIdx := i * 9
		// value
		assert.Equal(t, 0.5, features[baseIdx+0])
		// mean_5m
		assert.Equal(t, 0.5, features[baseIdx+1])
		// std_5m
		assert.Equal(t, 0.1, features[baseIdx+2])
		// min_5m
		assert.Equal(t, 0.3, features[baseIdx+3])
		// max_5m
		assert.Equal(t, 0.7, features[baseIdx+4])
		// lag_1
		assert.Equal(t, 0.5, features[baseIdx+5])
		// lag_5
		assert.Equal(t, 0.5, features[baseIdx+6])
		// diff
		assert.Equal(t, 0.0, features[baseIdx+7])
		// pct_change
		assert.Equal(t, 0.0, features[baseIdx+8])
	}
}

func TestAnomalyHandler_GetDefaultMetricsData(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	metricsData := handler.getDefaultMetricsData()

	assert.Equal(t, 5, len(metricsData))
	assert.Equal(t, 0.5, metricsData["node_cpu_utilization"])
	assert.Equal(t, 0.5, metricsData["node_memory_utilization"])
	assert.Equal(t, 0.5, metricsData["pod_cpu_usage"])
	assert.Equal(t, 0.5, metricsData["pod_memory_usage"])
	assert.Equal(t, 0.0, metricsData["container_restart_count"])
}

func TestAnomalyHandler_CalculateAnomalyScore(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	t.Run("normal metrics produce moderate score", func(t *testing.T) {
		metrics := map[string]float64{
			"node_cpu_utilization":    0.5,
			"node_memory_utilization": 0.5,
			"pod_cpu_usage":           0.5,
			"pod_memory_usage":        0.5,
			"container_restart_count": 0.0,
		}
		score := handler.calculateAnomalyScore(metrics)

		assert.Greater(t, score, 0.0)
		assert.LessOrEqual(t, score, 1.0)
	})

	t.Run("high metrics produce high score", func(t *testing.T) {
		metrics := map[string]float64{
			"node_cpu_utilization":    0.95,
			"node_memory_utilization": 0.95,
			"pod_cpu_usage":           0.95,
			"pod_memory_usage":        0.95,
			"container_restart_count": 5.0,
		}
		score := handler.calculateAnomalyScore(metrics)

		assert.Greater(t, score, 0.8)
	})

	t.Run("low metrics produce low score", func(t *testing.T) {
		metrics := map[string]float64{
			"node_cpu_utilization":    0.1,
			"node_memory_utilization": 0.1,
			"pod_cpu_usage":           0.1,
			"pod_memory_usage":        0.1,
			"container_restart_count": 0.0,
		}
		score := handler.calculateAnomalyScore(metrics)

		assert.Less(t, score, 0.3)
	})

	t.Run("score is clamped to 1.0", func(t *testing.T) {
		metrics := map[string]float64{
			"node_cpu_utilization":    5.0, // Unrealistic high value
			"node_memory_utilization": 5.0,
			"pod_cpu_usage":           5.0,
			"pod_memory_usage":        5.0,
			"container_restart_count": 100.0,
		}
		score := handler.calculateAnomalyScore(metrics)

		assert.Equal(t, 1.0, score)
	})
}

func TestAnomalyHandler_GenerateExplanation(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	t.Run("high CPU generates explanation", func(t *testing.T) {
		metrics := map[string]float64{
			"pod_cpu_usage":    0.9,
			"pod_memory_usage": 0.5,
		}
		explanation := handler.generateExplanation(metrics)

		assert.Contains(t, explanation, "CPU usage elevated")
	})

	t.Run("high memory generates explanation", func(t *testing.T) {
		metrics := map[string]float64{
			"pod_cpu_usage":    0.5,
			"pod_memory_usage": 0.9,
		}
		explanation := handler.generateExplanation(metrics)

		assert.Contains(t, explanation, "Memory usage high")
	})

	t.Run("restarts generate explanation", func(t *testing.T) {
		metrics := map[string]float64{
			"container_restart_count": 3.0,
		}
		explanation := handler.generateExplanation(metrics)

		assert.Contains(t, explanation, "Container restarts detected")
	})

	t.Run("node pressure generates explanation", func(t *testing.T) {
		metrics := map[string]float64{
			"node_cpu_utilization":    0.9,
			"node_memory_utilization": 0.9,
		}
		explanation := handler.generateExplanation(metrics)

		assert.Contains(t, explanation, "Node CPU pressure")
		assert.Contains(t, explanation, "Node memory pressure")
	})

	t.Run("normal metrics produce generic explanation", func(t *testing.T) {
		metrics := map[string]float64{
			"pod_cpu_usage":           0.5,
			"pod_memory_usage":        0.5,
			"container_restart_count": 0.0,
		}
		explanation := handler.generateExplanation(metrics)

		assert.Contains(t, explanation, "Anomalous behavior detected")
	})
}

func TestAnomalyHandler_RecommendAction(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	t.Run("high restarts recommend restart_pod", func(t *testing.T) {
		metrics := map[string]float64{
			"container_restart_count": 5.0,
		}
		action := handler.recommendAction(metrics, "critical")

		assert.Equal(t, "restart_pod", action)
	})

	t.Run("high memory recommends scale_resources", func(t *testing.T) {
		metrics := map[string]float64{
			"pod_memory_usage":        0.98,
			"container_restart_count": 0.0,
		}
		action := handler.recommendAction(metrics, "critical")

		assert.Equal(t, "scale_resources", action)
	})

	t.Run("high CPU recommends scale_resources", func(t *testing.T) {
		metrics := map[string]float64{
			"pod_cpu_usage":           0.98,
			"container_restart_count": 0.0,
		}
		action := handler.recommendAction(metrics, "critical")

		assert.Equal(t, "scale_resources", action)
	})

	t.Run("critical severity recommends immediate_investigation", func(t *testing.T) {
		metrics := map[string]float64{
			"pod_cpu_usage":           0.8,
			"container_restart_count": 1.0,
		}
		action := handler.recommendAction(metrics, "critical")

		assert.Equal(t, "immediate_investigation", action)
	})

	t.Run("warning severity recommends schedule_review", func(t *testing.T) {
		metrics := map[string]float64{
			"pod_cpu_usage": 0.7,
		}
		action := handler.recommendAction(metrics, "warning")

		assert.Equal(t, "schedule_review", action)
	})

	t.Run("info severity recommends monitor", func(t *testing.T) {
		metrics := map[string]float64{
			"pod_cpu_usage": 0.5,
		}
		action := handler.recommendAction(metrics, "info")

		assert.Equal(t, "monitor", action)
	})
}

func TestAnomalyHandler_BuildSummary(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	t.Run("empty anomalies", func(t *testing.T) {
		anomalies := []AnomalyResult{}
		features := make([]float64, 45)

		summary := handler.buildSummary(anomalies, features)

		assert.Equal(t, 0.0, summary.MaxScore)
		assert.Equal(t, 0.0, summary.AverageScore)
		assert.Equal(t, 5, summary.MetricsAnalyzed)
		assert.Equal(t, 45, summary.FeaturesGenerated)
	})

	t.Run("single anomaly", func(t *testing.T) {
		anomalies := []AnomalyResult{
			{AnomalyScore: 0.85},
		}
		features := make([]float64, 45)

		summary := handler.buildSummary(anomalies, features)

		assert.Equal(t, 0.85, summary.MaxScore)
		assert.Equal(t, 0.85, summary.AverageScore)
	})

	t.Run("multiple anomalies", func(t *testing.T) {
		anomalies := []AnomalyResult{
			{AnomalyScore: 0.7},
			{AnomalyScore: 0.9},
			{AnomalyScore: 0.8},
		}
		features := make([]float64, 45)

		summary := handler.buildSummary(anomalies, features)

		assert.Equal(t, 0.9, summary.MaxScore)
		assert.Equal(t, 0.8, summary.AverageScore)
	})
}

func TestAnomalyHandler_GenerateRecommendation(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	t.Run("no anomalies", func(t *testing.T) {
		anomalies := []AnomalyResult{}
		summary := AnomalySummary{MaxScore: 0.0}

		recommendation := handler.generateRecommendation(anomalies, summary)

		assert.Contains(t, recommendation, "No anomalies detected")
	})

	t.Run("critical anomaly", func(t *testing.T) {
		anomalies := []AnomalyResult{
			{Severity: "critical", AnomalyScore: 0.92},
		}
		summary := AnomalySummary{MaxScore: 0.92}

		recommendation := handler.generateRecommendation(anomalies, summary)

		assert.Contains(t, recommendation, "CRITICAL")
		assert.Contains(t, recommendation, "Immediate investigation")
	})

	t.Run("warning anomaly", func(t *testing.T) {
		anomalies := []AnomalyResult{
			{Severity: "warning", AnomalyScore: 0.85},
		}
		summary := AnomalySummary{MaxScore: 0.85}

		recommendation := handler.generateRecommendation(anomalies, summary)

		assert.Contains(t, recommendation, "WARNING")
	})

	t.Run("info anomaly", func(t *testing.T) {
		anomalies := []AnomalyResult{
			{Severity: "info", AnomalyScore: 0.5},
		}
		summary := AnomalySummary{MaxScore: 0.5}

		recommendation := handler.generateRecommendation(anomalies, summary)

		assert.Contains(t, recommendation, "INFO")
	})
}

func TestAnomalyAnalyzeRequest_Structure(t *testing.T) {
	reqJSON := `{
		"time_range": "1h",
		"namespace": "self-healing-platform",
		"deployment": "broken-app",
		"pod": "",
		"label_selector": "",
		"threshold": 0.7,
		"model_name": "anomaly-detector"
	}`

	var req AnomalyAnalyzeRequest
	err := json.Unmarshal([]byte(reqJSON), &req)
	require.NoError(t, err)

	assert.Equal(t, "1h", req.TimeRange)
	assert.Equal(t, "self-healing-platform", req.Namespace)
	assert.Equal(t, "broken-app", req.Deployment)
	assert.Equal(t, "", req.Pod)
	assert.Equal(t, "", req.LabelSelector)
	assert.Equal(t, 0.7, req.Threshold)
	assert.Equal(t, "anomaly-detector", req.ModelName)
}

func TestAnomalyAnalyzeResponse_Structure(t *testing.T) {
	resp := AnomalyAnalyzeResponse{
		Status:    "success",
		TimeRange: "1h",
		Scope: AnomalyScope{
			Namespace:         "self-healing-platform",
			Deployment:        "broken-app",
			TargetDescription: "deployment 'broken-app' in namespace 'self-healing-platform'",
		},
		ModelUsed:         "anomaly-detector",
		AnomaliesDetected: 1,
		Anomalies: []AnomalyResult{
			{
				Timestamp:         "2026-01-14T16:30:00Z",
				Severity:          "critical",
				AnomalyScore:      0.92,
				Confidence:        0.87,
				Metrics:           map[string]float64{"pod_memory_usage": 0.95},
				Explanation:       "Memory usage critically high",
				RecommendedAction: "scale_resources",
			},
		},
		Summary: AnomalySummary{
			MaxScore:          0.92,
			AverageScore:      0.92,
			MetricsAnalyzed:   5,
			FeaturesGenerated: 45,
		},
		Recommendation: "CRITICAL: Immediate investigation recommended.",
		Features: FeatureInfo{
			TotalFeatures:     45,
			BaseMetrics:       []string{"node_cpu_utilization"},
			FeaturesPerMetric: 9,
			FeatureNames:      []string{"node_cpu_utilization_value"},
		},
	}

	jsonData, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded AnomalyAnalyzeResponse
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	assert.Equal(t, resp.Status, decoded.Status)
	assert.Equal(t, resp.TimeRange, decoded.TimeRange)
	assert.Equal(t, resp.Scope.Namespace, decoded.Scope.Namespace)
	assert.Equal(t, resp.ModelUsed, decoded.ModelUsed)
	assert.Equal(t, resp.AnomaliesDetected, decoded.AnomaliesDetected)
	assert.Equal(t, len(resp.Anomalies), len(decoded.Anomalies))
	assert.Equal(t, resp.Summary.MaxScore, decoded.Summary.MaxScore)
	assert.Equal(t, resp.Summary.FeaturesGenerated, decoded.Summary.FeaturesGenerated)
}

func TestAnomalyErrorCodes(t *testing.T) {
	assert.Equal(t, "INVALID_REQUEST", ErrCodeAnomalyInvalidRequest)
	assert.Equal(t, "PROMETHEUS_UNAVAILABLE", ErrCodeAnomalyPrometheusUnavailable)
	assert.Equal(t, "KSERVE_UNAVAILABLE", ErrCodeAnomalyKServeUnavailable)
	assert.Equal(t, "MODEL_NOT_FOUND", ErrCodeAnomalyModelNotFound)
	assert.Equal(t, "ANALYSIS_FAILED", ErrCodeAnomalyAnalysisFailed)
}

func TestGetBaseMetrics(t *testing.T) {
	metrics := GetBaseMetrics()

	assert.Equal(t, 5, len(metrics))
	assert.Contains(t, metrics, "container_restart_count")
	assert.Contains(t, metrics, "node_cpu_utilization")
	assert.Contains(t, metrics, "node_memory_utilization")
	assert.Contains(t, metrics, "pod_cpu_usage")
	assert.Contains(t, metrics, "pod_memory_usage")
}

func TestGetFeatureNames(t *testing.T) {
	features := GetFeatureNames()

	assert.Equal(t, 9, len(features))
	assert.Contains(t, features, "value")
	assert.Contains(t, features, "mean_5m")
	assert.Contains(t, features, "std_5m")
	assert.Contains(t, features, "min_5m")
	assert.Contains(t, features, "max_5m")
	assert.Contains(t, features, "lag_1")
	assert.Contains(t, features, "lag_5")
	assert.Contains(t, features, "diff")
	assert.Contains(t, features, "pct_change")
}

func TestAnomalyHandler_BuildAnomalyResult(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	handler := NewAnomalyHandler(nil, nil, log)

	t.Run("critical severity for high score", func(t *testing.T) {
		metrics := map[string]float64{
			"pod_cpu_usage":    0.95,
			"pod_memory_usage": 0.98,
		}
		result := handler.buildAnomalyResult(metrics, 0.95)

		assert.Equal(t, "critical", result.Severity)
		assert.Equal(t, 0.95, result.AnomalyScore)
		assert.Equal(t, 0.87, result.Confidence)
		assert.NotEmpty(t, result.Timestamp)
		assert.NotEmpty(t, result.Explanation)
		assert.NotEmpty(t, result.RecommendedAction)
	})

	t.Run("warning severity for moderate score", func(t *testing.T) {
		metrics := map[string]float64{
			"pod_cpu_usage": 0.75,
		}
		result := handler.buildAnomalyResult(metrics, 0.75)

		assert.Equal(t, "warning", result.Severity)
	})

	t.Run("info severity for low score", func(t *testing.T) {
		metrics := map[string]float64{
			"pod_cpu_usage": 0.5,
		}
		result := handler.buildAnomalyResult(metrics, 0.5)

		assert.Equal(t, "info", result.Severity)
	})
}
