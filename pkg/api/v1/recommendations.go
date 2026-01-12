// Package v1 provides API handlers for the coordination engine.
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/internal/remediation"
	"github.com/tosin2013/openshift-coordination-engine/internal/storage"
	"github.com/tosin2013/openshift-coordination-engine/pkg/kserve"
	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// RecommendationsHandler handles ML-powered remediation recommendations API requests
type RecommendationsHandler struct {
	orchestrator  *remediation.Orchestrator
	incidentStore *storage.IncidentStore
	kserveClient  *kserve.ProxyClient
	log           *logrus.Logger
}

// NewRecommendationsHandler creates a new recommendations handler
func NewRecommendationsHandler(
	orchestrator *remediation.Orchestrator,
	incidentStore *storage.IncidentStore,
	kserveClient *kserve.ProxyClient,
	log *logrus.Logger,
) *RecommendationsHandler {
	return &RecommendationsHandler{
		orchestrator:  orchestrator,
		incidentStore: incidentStore,
		kserveClient:  kserveClient,
		log:           log,
	}
}

// GetRecommendationsRequest represents the request body for getting recommendations
type GetRecommendationsRequest struct {
	Timeframe           string  `json:"timeframe"`            // "1h", "6h", "24h" (default: "6h")
	IncludePredictions  *bool   `json:"include_predictions"`  // Include ML predictions (default: true)
	ConfidenceThreshold float64 `json:"confidence_threshold"` // Minimum confidence 0.0-1.0 (default: 0.7)
	Namespace           string  `json:"namespace"`            // Optional: filter by namespace
}

// Recommendation represents a single remediation recommendation
type Recommendation struct {
	ID                 string   `json:"id"`
	Type               string   `json:"type"`                           // "proactive", "reactive"
	IssueType          string   `json:"issue_type"`                     // e.g., "memory_pressure", "pod_crash_loop"
	Target             string   `json:"target"`                         // e.g., "Deployment/payment-service"
	Namespace          string   `json:"namespace"`                      // Target namespace
	Severity           string   `json:"severity"`                       // "low", "medium", "high", "critical"
	Confidence         float64  `json:"confidence"`                     // 0.0-1.0
	PredictedTime      string   `json:"predicted_time,omitempty"`       // ISO8601 timestamp when issue is expected
	RecommendedActions []string `json:"recommended_actions"`            // List of suggested remediation actions
	Evidence           []string `json:"evidence"`                       // Supporting data points
	Source             string   `json:"source,omitempty"`               // "ml_prediction", "historical_analysis", "pattern_detection"
	RelatedIncidentID  string   `json:"related_incident_id,omitempty"`  // If related to an existing incident
}

// GetRecommendationsResponse represents the response for getting recommendations
type GetRecommendationsResponse struct {
	Status               string           `json:"status"`
	Timestamp            string           `json:"timestamp"`
	Timeframe            string           `json:"timeframe"`
	Recommendations      []Recommendation `json:"recommendations"`
	TotalRecommendations int              `json:"total_recommendations"`
	MLEnabled            bool             `json:"ml_enabled"`
	Message              string           `json:"message,omitempty"`
}

// GetRecommendations handles POST /api/v1/recommendations
// @Summary Get ML-powered remediation recommendations
// @Description Returns proactive and reactive remediation recommendations using ML predictions
// @Tags recommendations
// @Accept json
// @Produce json
// @Param request body GetRecommendationsRequest true "Recommendation request parameters"
// @Success 200 {object} GetRecommendationsResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v1/recommendations [post]
func (h *RecommendationsHandler) GetRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	h.log.Info("Received get recommendations request")

	// Parse request body (optional - defaults used if empty)
	var req GetRecommendationsRequest
	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.log.WithError(err).Debug("Failed to decode request body")
			h.respondError(w, http.StatusBadRequest, "Invalid request body: "+err.Error())
			return
		}
	}

	// Set defaults
	if req.Timeframe == "" {
		req.Timeframe = "6h"
	}
	if req.IncludePredictions == nil {
		defaultTrue := true
		req.IncludePredictions = &defaultTrue
	}
	if req.ConfidenceThreshold == 0 {
		req.ConfidenceThreshold = 0.7
	}

	// Validate timeframe
	validTimeframes := map[string]bool{"1h": true, "6h": true, "24h": true}
	if !validTimeframes[req.Timeframe] {
		h.respondError(w, http.StatusBadRequest, "Invalid timeframe: must be '1h', '6h', or '24h'")
		return
	}

	// Validate confidence threshold
	if req.ConfidenceThreshold < 0 || req.ConfidenceThreshold > 1 {
		h.respondError(w, http.StatusBadRequest, "Invalid confidence_threshold: must be between 0.0 and 1.0")
		return
	}

	h.log.WithFields(logrus.Fields{
		"timeframe":            req.Timeframe,
		"include_predictions":  *req.IncludePredictions,
		"confidence_threshold": req.ConfidenceThreshold,
		"namespace":            req.Namespace,
	}).Info("Processing recommendations request")

	// Collect recommendations from various sources
	recommendations := make([]Recommendation, 0)

	// 1. Get historical incident-based recommendations
	historicalRecs := h.getHistoricalRecommendations(ctx, req)
	recommendations = append(recommendations, historicalRecs...)

	// 2. Get ML predictions if enabled and KServe is available
	mlEnabled := false
	if *req.IncludePredictions && h.kserveClient != nil {
		mlEnabled = true
		mlRecs, err := h.getMLPredictions(ctx, req)
		if err != nil {
			h.log.WithError(err).Warn("ML predictions failed, continuing with historical analysis")
			mlEnabled = false
		} else {
			recommendations = append(recommendations, mlRecs...)
		}
	}

	// 3. Get pattern-based recommendations
	patternRecs := h.getPatternRecommendations(ctx, req)
	recommendations = append(recommendations, patternRecs...)

	// Filter by confidence threshold
	filteredRecs := make([]Recommendation, 0, len(recommendations))
	for _, rec := range recommendations {
		if rec.Confidence >= req.ConfidenceThreshold {
			filteredRecs = append(filteredRecs, rec)
		}
	}

	// Filter by namespace if specified
	if req.Namespace != "" {
		namespacedRecs := make([]Recommendation, 0)
		for _, rec := range filteredRecs {
			if rec.Namespace == req.Namespace {
				namespacedRecs = append(namespacedRecs, rec)
			}
		}
		filteredRecs = namespacedRecs
	}

	// Build response
	response := GetRecommendationsResponse{
		Status:               "success",
		Timestamp:            time.Now().UTC().Format(time.RFC3339),
		Timeframe:            req.Timeframe,
		Recommendations:      filteredRecs,
		TotalRecommendations: len(filteredRecs),
		MLEnabled:            mlEnabled,
	}

	if len(filteredRecs) == 0 {
		response.Message = "No recommendations above the confidence threshold"
	}

	h.log.WithFields(logrus.Fields{
		"total_recommendations": len(filteredRecs),
		"ml_enabled":            mlEnabled,
		"timeframe":             req.Timeframe,
	}).Info("Recommendations generated successfully")

	h.respondJSON(w, http.StatusOK, response)
}

// getHistoricalRecommendations analyzes historical incidents to generate recommendations
func (h *RecommendationsHandler) getHistoricalRecommendations(ctx context.Context, req GetRecommendationsRequest) []Recommendation {
	recommendations := make([]Recommendation, 0)

	// Get historical incidents from store
	filter := storage.ListFilter{
		Namespace: req.Namespace,
		Limit:     100,
	}
	incidents := h.incidentStore.List(filter)

	// Get workflow-based incidents (if orchestrator is available)
	var workflows []*models.Workflow
	if h.orchestrator != nil {
		workflows = h.orchestrator.ListWorkflows()
	}

	// Analyze incident patterns
	issueFrequency := make(map[string]int)
	namespaceIssues := make(map[string][]string)

	// Count incident types from stored incidents
	for _, inc := range incidents {
		key := string(inc.Severity) + ":" + inc.Target
		issueFrequency[key]++
		namespaceIssues[inc.Target] = append(namespaceIssues[inc.Target], inc.Title)
	}

	// Count issue types from workflows
	for _, wf := range workflows {
		key := wf.IssueType + ":" + wf.Namespace
		issueFrequency[key]++
		namespaceIssues[wf.Namespace] = append(namespaceIssues[wf.Namespace], wf.IssueType)
	}

	// Generate recommendations for recurring issues
	recID := 0
	for key, count := range issueFrequency {
		if count < 2 {
			continue // Only recommend for recurring issues
		}

		// Parse the key to extract issue type and namespace
		var issueType, namespace string
		if len(key) > 0 {
			// Find the colon separator
			for i, c := range key {
				if c == ':' {
					issueType = key[:i]
					namespace = key[i+1:]
					break
				}
			}
		}

		if issueType == "" || namespace == "" {
			continue
		}

		recID++
		confidence := calculateHistoricalConfidence(count)

		rec := Recommendation{
			ID:        fmt.Sprintf("rec-hist-%03d", recID),
			Type:      "proactive",
			IssueType: issueType,
			Target:    namespace,
			Namespace: namespace,
			Severity:  mapCountToSeverity(count),
			Confidence: confidence,
			RecommendedActions: getRecommendedActions(issueType),
			Evidence: []string{
				fmt.Sprintf("Issue occurred %d times in recent history", count),
				fmt.Sprintf("Pattern detected in namespace: %s", namespace),
			},
			Source: "historical_analysis",
		}

		recommendations = append(recommendations, rec)
	}

	return recommendations
}

// getMLPredictions calls KServe predictive-analytics model for ML-based predictions
func (h *RecommendationsHandler) getMLPredictions(ctx context.Context, req GetRecommendationsRequest) ([]Recommendation, error) {
	recommendations := make([]Recommendation, 0)

	// Check if predictive-analytics model is available
	if _, exists := h.kserveClient.GetModel("predictive-analytics"); !exists {
		h.log.Debug("predictive-analytics model not available")
		return recommendations, nil
	}

	// Prepare sample input data based on current cluster state
	// In a real implementation, this would gather actual metrics from Prometheus
	instances := [][]float64{
		{0.75, 0.80, 0.02}, // CPU usage, memory usage, error rate
		{0.85, 0.90, 0.05}, // High resource utilization scenario
	}

	// Call KServe model
	resp, err := h.kserveClient.Predict(ctx, "predictive-analytics", instances)
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	// Interpret predictions (-1 = issue predicted, 1 = normal)
	for i, prediction := range resp.Predictions {
		if prediction == -1 { // Issue predicted
			predictedTime := time.Now().Add(getPredictionHorizon(req.Timeframe))

			rec := Recommendation{
				ID:            fmt.Sprintf("rec-ml-%03d", i+1),
				Type:          "proactive",
				IssueType:     interpretPrediction(i),
				Target:        "cluster-resources",
				Namespace:     req.Namespace,
				Severity:      "high",
				Confidence:    0.85, // Would come from model in real implementation
				PredictedTime: predictedTime.UTC().Format(time.RFC3339),
				RecommendedActions: []string{
					"increase_resource_limits",
					"add_horizontal_scaling",
					"review_resource_quotas",
				},
				Evidence: []string{
					fmt.Sprintf("ML model predicts issue within %s", req.Timeframe),
					fmt.Sprintf("Input features indicate resource pressure (instance %d)", i+1),
				},
				Source: "ml_prediction",
			}

			recommendations = append(recommendations, rec)
		}
	}

	return recommendations, nil
}

// getPatternRecommendations detects common patterns and generates recommendations
func (h *RecommendationsHandler) getPatternRecommendations(ctx context.Context, req GetRecommendationsRequest) []Recommendation {
	recommendations := make([]Recommendation, 0)

	// Pattern detection based on workflow analysis (if orchestrator is available)
	if h.orchestrator == nil {
		return recommendations
	}
	workflows := h.orchestrator.ListWorkflows()

	// Track failure patterns
	failurePatterns := make(map[string]int)
	for _, wf := range workflows {
		if wf.Status == "failed" {
			key := wf.IssueType + ":" + wf.Namespace
			failurePatterns[key]++
		}
	}

	// Generate recommendations for repeated failures
	recID := 0
	for key, count := range failurePatterns {
		if count < 2 {
			continue
		}

		var issueType, namespace string
		for i, c := range key {
			if c == ':' {
				issueType = key[:i]
				namespace = key[i+1:]
				break
			}
		}

		if issueType == "" {
			continue
		}

		recID++
		rec := Recommendation{
			ID:         fmt.Sprintf("rec-pattern-%03d", recID),
			Type:       "reactive",
			IssueType:  issueType,
			Target:     fmt.Sprintf("%s-workloads", namespace),
			Namespace:  namespace,
			Severity:   "high",
			Confidence: 0.80,
			RecommendedActions: []string{
				"investigate_root_cause",
				"review_remediation_strategy",
				"consider_manual_intervention",
			},
			Evidence: []string{
				fmt.Sprintf("Remediation failed %d times for similar issues", count),
				"Pattern suggests underlying infrastructure problem",
			},
			Source: "pattern_detection",
		}

		recommendations = append(recommendations, rec)
	}

	return recommendations
}

// Helper functions

func calculateHistoricalConfidence(count int) float64 {
	// Higher occurrence count = higher confidence
	switch {
	case count >= 10:
		return 0.95
	case count >= 5:
		return 0.85
	case count >= 3:
		return 0.75
	default:
		return 0.65
	}
}

func mapCountToSeverity(count int) string {
	switch {
	case count >= 10:
		return "critical"
	case count >= 5:
		return "high"
	case count >= 3:
		return "medium"
	default:
		return "low"
	}
}

func getRecommendedActions(issueType string) []string {
	actionMap := map[string][]string{
		"pod_crash_loop": {
			"check_container_logs",
			"verify_resource_limits",
			"review_health_probes",
		},
		"memory_pressure": {
			"increase_memory_limit",
			"add_horizontal_scaling",
			"optimize_memory_usage",
		},
		"cpu_throttling": {
			"increase_cpu_limit",
			"optimize_cpu_usage",
			"consider_vertical_scaling",
		},
		"high": {
			"investigate_root_cause",
			"increase_resources",
			"review_deployment_config",
		},
		"critical": {
			"immediate_investigation",
			"scale_resources",
			"contact_on_call",
		},
	}

	if actions, ok := actionMap[issueType]; ok {
		return actions
	}

	return []string{
		"investigate_issue",
		"review_logs",
		"check_metrics",
	}
}

func getPredictionHorizon(timeframe string) time.Duration {
	switch timeframe {
	case "1h":
		return 30 * time.Minute
	case "6h":
		return 3 * time.Hour
	case "24h":
		return 12 * time.Hour
	default:
		return 3 * time.Hour
	}
}

func interpretPrediction(instanceIndex int) string {
	// Map prediction instances to issue types
	issueTypes := []string{
		"memory_pressure",
		"cpu_throttling",
		"resource_exhaustion",
	}

	if instanceIndex < len(issueTypes) {
		return issueTypes[instanceIndex]
	}
	return "resource_issue"
}

// respondJSON writes a JSON response
func (h *RecommendationsHandler) respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.WithError(err).Error("Failed to encode JSON response")
	}
}

// respondError writes an error response
func (h *RecommendationsHandler) respondError(w http.ResponseWriter, statusCode int, message string) {
	response := map[string]interface{}{
		"status": "error",
		"error":  message,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode error response")
	}
}
