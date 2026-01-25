package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tosin2013/openshift-coordination-engine/internal/storage"
	"github.com/tosin2013/openshift-coordination-engine/pkg/kserve"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

func TestRecommendationsHandler_GetRecommendations(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create mock dependencies
	incidentStore := storage.NewIncidentStore()

	// Add some test incidents
	incident1 := &models.Incident{
		Title:       "Test incident 1",
		Description: "Memory pressure in production",
		Severity:    models.IncidentSeverityHigh,
		Target:      "production",
	}
	incident2 := &models.Incident{
		Title:       "Test incident 2",
		Description: "Memory pressure in production again",
		Severity:    models.IncidentSeverityHigh,
		Target:      "production",
	}
	incidentStore.Create(incident1)
	incidentStore.Create(incident2)

	handler := NewRecommendationsHandler(nil, incidentStore, nil, log)

	t.Run("successful request with defaults", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/api/v1/recommendations", http.NoBody)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp GetRecommendationsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "success", resp.Status)
		assert.Equal(t, "6h", resp.Timeframe)
		assert.NotEmpty(t, resp.Timestamp)
		assert.False(t, resp.MLEnabled) // No KServe client provided
	})

	t.Run("successful request with custom parameters", func(t *testing.T) {
		reqBody := `{
			"timeframe": "1h",
			"include_predictions": false,
			"confidence_threshold": 0.5,
			"namespace": "production"
		}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp GetRecommendationsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "success", resp.Status)
		assert.Equal(t, "1h", resp.Timeframe)
		assert.False(t, resp.MLEnabled)
	})

	t.Run("invalid timeframe", func(t *testing.T) {
		reqBody := `{"timeframe": "2h"}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp map[string]interface{}
		json.NewDecoder(w.Body).Decode(&resp)
		assert.Equal(t, "error", resp["status"])
		assert.Contains(t, resp["error"], "invalid timeframe")
	})

	t.Run("invalid confidence threshold - too high", func(t *testing.T) {
		reqBody := `{"confidence_threshold": 1.5}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)

		var resp map[string]interface{}
		json.NewDecoder(w.Body).Decode(&resp)
		assert.Contains(t, resp["error"], "confidence_threshold")
	})

	t.Run("invalid confidence threshold - negative", func(t *testing.T) {
		reqBody := `{"confidence_threshold": -0.5}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		reqBody := `{"timeframe": invalid}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("high confidence threshold filters all", func(t *testing.T) {
		reqBody := `{"confidence_threshold": 0.99}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp GetRecommendationsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// With very high threshold, most recommendations should be filtered
		assert.Equal(t, "success", resp.Status)
	})
}

func TestRecommendationsHandler_WithKServe(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Set up environment for KServe model
	os.Setenv("KSERVE_PREDICTIVE_ANALYTICS_SERVICE", "predictive-analytics-predictor")
	defer os.Unsetenv("KSERVE_PREDICTIVE_ANALYTICS_SERVICE")

	cfg := kserve.ProxyConfig{
		Namespace: "test-ns",
		Timeout:   30 * time.Second,
	}

	kserveClient, err := kserve.NewProxyClient(cfg, log)
	require.NoError(t, err)

	incidentStore := storage.NewIncidentStore()
	handler := NewRecommendationsHandler(nil, incidentStore, kserveClient, log)

	t.Run("with ML predictions enabled but service unavailable", func(t *testing.T) {
		// In unit tests, the KServe service is not available, so ML will be disabled
		// after the failed prediction attempt
		reqBody := `{"include_predictions": true}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp GetRecommendationsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "success", resp.Status)
		// ML should be disabled because the KServe service is not actually reachable
		// The handler gracefully falls back when ML predictions fail
		assert.False(t, resp.MLEnabled)
	})

	t.Run("with ML predictions disabled", func(t *testing.T) {
		reqBody := `{"include_predictions": false}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp GetRecommendationsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "success", resp.Status)
		assert.False(t, resp.MLEnabled) // Explicitly disabled
	})

	t.Run("without KServe client", func(t *testing.T) {
		handlerNoKServe := NewRecommendationsHandler(nil, incidentStore, nil, log)
		reqBody := `{"include_predictions": true}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handlerNoKServe.GetRecommendations(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp GetRecommendationsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "success", resp.Status)
		assert.False(t, resp.MLEnabled) // No KServe client provided
	})
}

func TestRecommendationsHandler_HistoricalRecommendations(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	incidentStore := storage.NewIncidentStore()

	// Create multiple incidents with same pattern to trigger recommendations
	for i := 0; i < 5; i++ {
		incident := &models.Incident{
			Title:       "Memory pressure incident",
			Description: "Memory pressure detected",
			Severity:    models.IncidentSeverityHigh,
			Target:      "production",
		}
		incidentStore.Create(incident)
	}

	handler := NewRecommendationsHandler(nil, incidentStore, nil, log)

	t.Run("generates recommendations for recurring issues", func(t *testing.T) {
		reqBody := `{"confidence_threshold": 0.5}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp GetRecommendationsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, "success", resp.Status)
		// Should have at least one recommendation based on recurring incidents
		assert.GreaterOrEqual(t, resp.TotalRecommendations, 1)

		if len(resp.Recommendations) > 0 {
			rec := resp.Recommendations[0]
			assert.NotEmpty(t, rec.ID)
			assert.NotEmpty(t, rec.Type)
			assert.NotEmpty(t, rec.IssueType)
			assert.NotEmpty(t, rec.Target)
			assert.NotEmpty(t, rec.RecommendedActions)
			assert.NotEmpty(t, rec.Evidence)
			assert.GreaterOrEqual(t, rec.Confidence, 0.0)
			assert.LessOrEqual(t, rec.Confidence, 1.0)
		}
	})
}

func TestRecommendationsHandler_NamespaceFiltering(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	incidentStore := storage.NewIncidentStore()

	// Create incidents in different namespaces
	for i := 0; i < 3; i++ {
		incident := &models.Incident{
			Title:       "Production incident",
			Description: "Issue in production",
			Severity:    models.IncidentSeverityHigh,
			Target:      "production",
		}
		incidentStore.Create(incident)
	}
	for i := 0; i < 3; i++ {
		incident := &models.Incident{
			Title:       "Staging incident",
			Description: "Issue in staging",
			Severity:    models.IncidentSeverityMedium,
			Target:      "staging",
		}
		incidentStore.Create(incident)
	}

	handler := NewRecommendationsHandler(nil, incidentStore, nil, log)

	t.Run("filter by production namespace", func(t *testing.T) {
		reqBody := `{"namespace": "production", "confidence_threshold": 0.5}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp GetRecommendationsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		// All recommendations should be for production namespace
		for _, rec := range resp.Recommendations {
			assert.Equal(t, "production", rec.Namespace)
		}
	})

	t.Run("filter by non-existent namespace", func(t *testing.T) {
		reqBody := `{"namespace": "non-existent"}`
		req := httptest.NewRequest("POST", "/api/v1/recommendations", bytes.NewBufferString(reqBody))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.GetRecommendations(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp GetRecommendationsResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.Equal(t, 0, resp.TotalRecommendations)
		assert.Contains(t, resp.Message, "No recommendations")
	})
}

func TestRecommendation_Structure(t *testing.T) {
	rec := Recommendation{
		ID:            "rec-001",
		Type:          "proactive",
		IssueType:     "memory_pressure",
		Target:        "Deployment/payment-service",
		Namespace:     "production",
		Severity:      "high",
		Confidence:    0.85,
		PredictedTime: "2026-01-11T19:30:00Z",
		RecommendedActions: []string{
			"increase_memory_limit",
			"add_horizontal_scaling",
		},
		Evidence: []string{
			"Memory usage trend: 65% → 85% over 2h",
		},
		Source:            "ml_prediction",
		RelatedIncidentID: "inc-12345",
	}

	jsonData, err := json.Marshal(rec)
	require.NoError(t, err)

	var decoded Recommendation
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	assert.Equal(t, rec.ID, decoded.ID)
	assert.Equal(t, rec.Type, decoded.Type)
	assert.Equal(t, rec.IssueType, decoded.IssueType)
	assert.Equal(t, rec.Target, decoded.Target)
	assert.Equal(t, rec.Namespace, decoded.Namespace)
	assert.Equal(t, rec.Severity, decoded.Severity)
	assert.Equal(t, rec.Confidence, decoded.Confidence)
	assert.Equal(t, rec.PredictedTime, decoded.PredictedTime)
	assert.Equal(t, rec.RecommendedActions, decoded.RecommendedActions)
	assert.Equal(t, rec.Evidence, decoded.Evidence)
	assert.Equal(t, rec.Source, decoded.Source)
	assert.Equal(t, rec.RelatedIncidentID, decoded.RelatedIncidentID)
}

func TestGetRecommendationsResponse_Structure(t *testing.T) {
	resp := GetRecommendationsResponse{
		Status:    "success",
		Timestamp: "2026-01-11T17:00:00Z",
		Timeframe: "6h",
		Recommendations: []Recommendation{
			{
				ID:        "rec-001",
				Type:      "proactive",
				IssueType: "memory_pressure",
			},
		},
		TotalRecommendations: 1,
		MLEnabled:            true,
		Message:              "",
	}

	jsonData, err := json.Marshal(resp)
	require.NoError(t, err)

	var decoded GetRecommendationsResponse
	err = json.Unmarshal(jsonData, &decoded)
	require.NoError(t, err)

	assert.Equal(t, resp.Status, decoded.Status)
	assert.Equal(t, resp.Timestamp, decoded.Timestamp)
	assert.Equal(t, resp.Timeframe, decoded.Timeframe)
	assert.Equal(t, resp.TotalRecommendations, decoded.TotalRecommendations)
	assert.Equal(t, resp.MLEnabled, decoded.MLEnabled)
	assert.Len(t, decoded.Recommendations, 1)
}

func TestHelperFunctions(t *testing.T) {
	t.Run("calculateHistoricalConfidence", func(t *testing.T) {
		assert.Equal(t, 0.95, calculateHistoricalConfidence(10))
		assert.Equal(t, 0.95, calculateHistoricalConfidence(15))
		assert.Equal(t, 0.85, calculateHistoricalConfidence(5))
		assert.Equal(t, 0.85, calculateHistoricalConfidence(7))
		assert.Equal(t, 0.75, calculateHistoricalConfidence(3))
		assert.Equal(t, 0.65, calculateHistoricalConfidence(2))
		assert.Equal(t, 0.65, calculateHistoricalConfidence(1))
	})

	t.Run("mapCountToSeverity", func(t *testing.T) {
		assert.Equal(t, "critical", mapCountToSeverity(10))
		assert.Equal(t, "critical", mapCountToSeverity(15))
		assert.Equal(t, "high", mapCountToSeverity(5))
		assert.Equal(t, "high", mapCountToSeverity(7))
		assert.Equal(t, "medium", mapCountToSeverity(3))
		assert.Equal(t, "low", mapCountToSeverity(2))
		assert.Equal(t, "low", mapCountToSeverity(1))
	})

	t.Run("getRecommendedActions", func(t *testing.T) {
		// Known issue types
		actions := getRecommendedActions("pod_crash_loop")
		assert.Contains(t, actions, "check_container_logs")

		actions = getRecommendedActions("memory_pressure")
		assert.Contains(t, actions, "increase_memory_limit")

		actions = getRecommendedActions("cpu_throttling")
		assert.Contains(t, actions, "increase_cpu_limit")

		// Unknown issue type should return generic actions
		actions = getRecommendedActions("unknown_issue")
		assert.Contains(t, actions, "investigate_issue")
	})

	t.Run("getPredictionHorizon", func(t *testing.T) {
		assert.Equal(t, 30*time.Minute, getPredictionHorizon("1h"))
		assert.Equal(t, 3*time.Hour, getPredictionHorizon("6h"))
		assert.Equal(t, 12*time.Hour, getPredictionHorizon("24h"))
		assert.Equal(t, 3*time.Hour, getPredictionHorizon("unknown"))
	})

	t.Run("interpretPrediction", func(t *testing.T) {
		assert.Equal(t, "memory_pressure", interpretPrediction(0))
		assert.Equal(t, "cpu_throttling", interpretPrediction(1))
		assert.Equal(t, "resource_exhaustion", interpretPrediction(2))
		assert.Equal(t, "resource_issue", interpretPrediction(10)) // Out of range
	})
}

func TestGetRecommendationsRequest_Defaults(t *testing.T) {
	req := GetRecommendationsRequest{}

	// Verify zero values
	assert.Empty(t, req.Timeframe)
	assert.Nil(t, req.IncludePredictions)
	assert.Zero(t, req.ConfidenceThreshold)
	assert.Empty(t, req.Namespace)
}
