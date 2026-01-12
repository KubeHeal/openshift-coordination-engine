// Package v1 provides API handlers for the coordination engine.
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
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
	Type               string   `json:"type"`
	IssueType          string   `json:"issue_type"`
	Target             string   `json:"target"`
	Namespace          string   `json:"namespace"`
	Severity           string   `json:"severity"`
	Confidence         float64  `json:"confidence"`
	PredictedTime      string   `json:"predicted_time,omitempty"`
	RecommendedActions []string `json:"recommended_actions"`
	Evidence           []string `json:"evidence"`
	Source             string   `json:"source,omitempty"`
	RelatedIncidentID  string   `json:"related_incident_id,omitempty"`
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
func (h *RecommendationsHandler) GetRecommendations(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	h.log.Info("Received get recommendations request")

	// Parse and validate request
	req, err := h.parseAndValidateRequest(r)
	if err != nil {
		h.respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	h.log.WithFields(logrus.Fields{
		"timeframe":            req.Timeframe,
		"include_predictions":  *req.IncludePredictions,
		"confidence_threshold": req.ConfidenceThreshold,
		"namespace":            req.Namespace,
	}).Info("Processing recommendations request")

	// Collect and filter recommendations
	recommendations, mlEnabled := h.collectRecommendations(ctx, req)
	filteredRecs := h.filterRecommendations(recommendations, req)

	// Build and send response
	h.sendRecommendationsResponse(w, req, filteredRecs, mlEnabled)
}

// parseAndValidateRequest parses the request body and validates parameters
func (h *RecommendationsHandler) parseAndValidateRequest(r *http.Request) (*GetRecommendationsRequest, error) {
	var req GetRecommendationsRequest

	if r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.log.WithError(err).Debug("Failed to decode request body")
			return nil, fmt.Errorf("invalid request body: %w", err)
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
		return nil, fmt.Errorf("invalid timeframe: must be '1h', '6h', or '24h'")
	}

	// Validate confidence threshold
	if req.ConfidenceThreshold < 0 || req.ConfidenceThreshold > 1 {
		return nil, fmt.Errorf("invalid confidence_threshold: must be between 0.0 and 1.0")
	}

	return &req, nil
}

// collectRecommendations gathers recommendations from all sources
func (h *RecommendationsHandler) collectRecommendations(ctx context.Context, req *GetRecommendationsRequest) ([]Recommendation, bool) {
	recommendations := make([]Recommendation, 0)

	// Get historical incident-based recommendations
	historicalRecs := h.getHistoricalRecommendations(req)
	recommendations = append(recommendations, historicalRecs...)

	// Get ML predictions if enabled and KServe is available
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

	// Get pattern-based recommendations
	patternRecs := h.getPatternRecommendations()
	recommendations = append(recommendations, patternRecs...)

	return recommendations, mlEnabled
}

// filterRecommendations filters recommendations by confidence and namespace
func (h *RecommendationsHandler) filterRecommendations(recommendations []Recommendation, req *GetRecommendationsRequest) []Recommendation {
	filteredRecs := make([]Recommendation, 0, len(recommendations))

	for i := range recommendations {
		rec := &recommendations[i]
		if rec.Confidence >= req.ConfidenceThreshold {
			if req.Namespace == "" || rec.Namespace == req.Namespace {
				filteredRecs = append(filteredRecs, *rec)
			}
		}
	}

	return filteredRecs
}

// sendRecommendationsResponse builds and sends the response
func (h *RecommendationsHandler) sendRecommendationsResponse(w http.ResponseWriter, req *GetRecommendationsRequest, filteredRecs []Recommendation, mlEnabled bool) {
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
func (h *RecommendationsHandler) getHistoricalRecommendations(req *GetRecommendationsRequest) []Recommendation {
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

	// Count incident types from stored incidents
	for _, inc := range incidents {
		key := string(inc.Severity) + ":" + inc.Target
		issueFrequency[key]++
	}

	// Count issue types from workflows
	for _, wf := range workflows {
		key := wf.IssueType + ":" + wf.Namespace
		issueFrequency[key]++
	}

	// Generate recommendations for recurring issues
	recID := 0
	for key, count := range issueFrequency {
		if count < 2 {
			continue // Only recommend for recurring issues
		}

		issueType, namespace := parseKeyParts(key)
		if issueType == "" || namespace == "" {
			continue
		}

		recID++
		recommendations = append(recommendations, Recommendation{
			ID:                 fmt.Sprintf("rec-hist-%03d", recID),
			Type:               "proactive",
			IssueType:          issueType,
			Target:             namespace,
			Namespace:          namespace,
			Severity:           mapCountToSeverity(count),
			Confidence:         calculateHistoricalConfidence(count),
			RecommendedActions: getRecommendedActions(issueType),
			Evidence: []string{
				fmt.Sprintf("Issue occurred %d times in recent history", count),
				fmt.Sprintf("Pattern detected in namespace: %s", namespace),
			},
			Source: "historical_analysis",
		})
	}

	return recommendations
}

// getMLPredictions calls KServe predictive-analytics model for ML-based predictions
func (h *RecommendationsHandler) getMLPredictions(ctx context.Context, req *GetRecommendationsRequest) ([]Recommendation, error) {
	recommendations := make([]Recommendation, 0)

	// Check if predictive-analytics model is available
	if _, exists := h.kserveClient.GetModel("predictive-analytics"); !exists {
		h.log.Debug("predictive-analytics model not available")
		return recommendations, nil
	}

	// Get current time for temporal features
	currentTime := time.Now()

	// Prepare input features matching model training order:
	// [hour_of_day, day_of_week, cpu_rolling_mean, memory_rolling_mean]
	// The model expects exactly 4 features in this specific order
	instances := h.buildPredictionInstances(currentTime)

	h.log.WithFields(logrus.Fields{
		"hour_of_day": currentTime.Hour(),
		"day_of_week": int(currentTime.Weekday()),
		"instances":   len(instances),
	}).Debug("Prepared ML prediction features")

	// Call KServe model
	resp, err := h.kserveClient.Predict(ctx, "predictive-analytics", instances)
	if err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	h.log.WithField("predictions", len(resp.Predictions)).Info("ML predictions successful")

	// Interpret predictions
	// The model may return classification (-1 = issue predicted, 1 = normal)
	// or scaled values that indicate resource pressure
	recommendations = h.interpretMLPredictions(resp.Predictions, req, currentTime, instances)

	return recommendations, nil
}

// buildPredictionInstances creates feature instances for ML prediction
// Features must match training order: [hour_of_day, day_of_week, cpu_rolling_mean, memory_rolling_mean]
func (h *RecommendationsHandler) buildPredictionInstances(currentTime time.Time) [][]float64 {
	hourOfDay := float64(currentTime.Hour())
	dayOfWeek := float64(currentTime.Weekday())

	// Calculate rolling means from recent metrics
	// In production, these would come from Prometheus queries over 24h window
	cpuRollingMean := h.getCPURollingMean()
	memoryRollingMean := h.getMemoryRollingMean()

	// Build instances with 4 features each (matching model training)
	instances := [][]float64{
		{hourOfDay, dayOfWeek, cpuRollingMean, memoryRollingMean},
	}

	// Add scenario with slightly elevated metrics for comparison
	instances = append(instances, []float64{
		hourOfDay,
		dayOfWeek,
		min(cpuRollingMean*1.15, 1.0),    // 15% higher CPU scenario
		min(memoryRollingMean*1.15, 1.0), // 15% higher memory scenario
	})

	return instances
}

// getCPURollingMean returns the 24-hour rolling mean of CPU usage
// In production, this would query Prometheus: avg_over_time(container_cpu_usage_seconds_total[24h])
func (h *RecommendationsHandler) getCPURollingMean() float64 {
	// TODO: Integrate with Prometheus for real metrics
	// For now, return a reasonable default that represents typical cluster state
	// This should be replaced with actual Prometheus query:
	// query: avg(rate(container_cpu_usage_seconds_total{namespace!=""}[24h]))
	return 0.65 // 65% average CPU usage
}

// getMemoryRollingMean returns the 24-hour rolling mean of memory usage
// In production, this would query Prometheus: avg_over_time(container_memory_usage_bytes[24h])
func (h *RecommendationsHandler) getMemoryRollingMean() float64 {
	// TODO: Integrate with Prometheus for real metrics
	// For now, return a reasonable default that represents typical cluster state
	// This should be replaced with actual Prometheus query:
	// query: avg(container_memory_usage_bytes{namespace!=""} / container_spec_memory_limit_bytes{namespace!=""})
	return 0.72 // 72% average memory usage
}

// interpretMLPredictions converts model output to recommendations
// The model returns classification predictions (-1 = issue predicted, 1 = normal)
// for each input instance based on the 4 features
func (h *RecommendationsHandler) interpretMLPredictions(predictions []int, req *GetRecommendationsRequest, currentTime time.Time, instances [][]float64) []Recommendation {
	recommendations := make([]Recommendation, 0)

	cpuRollingMean := h.getCPURollingMean()
	memoryRollingMean := h.getMemoryRollingMean()

	// Process each prediction corresponding to each instance
	for i, prediction := range predictions {
		// -1 indicates the model predicts an issue
		if prediction == -1 {
			predictedTime := currentTime.Add(getPredictionHorizon(req.Timeframe))

			// Determine issue type based on which metrics are elevated
			var issueType string
			var severity string
			var actions []string
			var evidence []string

			// Get the instance features if available
			var instanceCPU, instanceMem float64
			if i < len(instances) {
				instanceCPU = instances[i][2]  // cpu_rolling_mean
				instanceMem = instances[i][3]  // memory_rolling_mean
			} else {
				instanceCPU = cpuRollingMean
				instanceMem = memoryRollingMean
			}

			// Determine primary concern based on metric values
			if instanceMem > instanceCPU {
				issueType = "memory_pressure"
				severity = mapMetricToSeverity(instanceMem)
				actions = []string{
					"increase_memory_limit",
					"add_horizontal_scaling",
					"optimize_memory_usage",
				}
				evidence = []string{
					fmt.Sprintf("ML model predicts memory pressure within %s", req.Timeframe),
					fmt.Sprintf("Current memory rolling mean: %.1f%%", instanceMem*100),
				}
			} else {
				issueType = "cpu_throttling"
				severity = mapMetricToSeverity(instanceCPU)
				actions = []string{
					"increase_cpu_limit",
					"add_horizontal_scaling",
					"review_resource_quotas",
				}
				evidence = []string{
					fmt.Sprintf("ML model predicts CPU pressure within %s", req.Timeframe),
					fmt.Sprintf("Current CPU rolling mean: %.1f%%", instanceCPU*100),
				}
			}

			// Add feature context to evidence
			evidence = append(evidence,
				fmt.Sprintf("Features: hour=%d, day=%d, cpu_rolling=%.2f, memory_rolling=%.2f",
					currentTime.Hour(), int(currentTime.Weekday()), cpuRollingMean, memoryRollingMean))

			// Calculate confidence based on how elevated the metrics are
			confidence := calculatePredictionConfidence(instanceCPU, instanceMem)

			recommendations = append(recommendations, Recommendation{
				ID:                 fmt.Sprintf("rec-ml-%03d", i+1),
				Type:               "proactive",
				IssueType:          issueType,
				Target:             "cluster-resources",
				Namespace:          req.Namespace,
				Severity:           severity,
				Confidence:         confidence,
				PredictedTime:      predictedTime.UTC().Format(time.RFC3339),
				RecommendedActions: actions,
				Evidence:           evidence,
				Source:             "ml_prediction",
			})
		}
	}

	return recommendations
}

// mapMetricToSeverity converts a metric value (0.0-1.0) to severity level
func mapMetricToSeverity(metricValue float64) string {
	switch {
	case metricValue >= 0.9:
		return "critical"
	case metricValue >= 0.8:
		return "high"
	case metricValue >= 0.7:
		return "medium"
	default:
		return "low"
	}
}

// calculatePredictionConfidence calculates confidence based on metric values
func calculatePredictionConfidence(cpuMean, memoryMean float64) float64 {
	// Higher metrics = higher confidence in the prediction
	maxMetric := cpuMean
	if memoryMean > cpuMean {
		maxMetric = memoryMean
	}

	// Scale confidence between 0.7 and 0.95 based on metric severity
	confidence := 0.7 + (maxMetric * 0.25)
	if confidence > 0.95 {
		confidence = 0.95
	}
	return confidence
}


// getPatternRecommendations detects common patterns and generates recommendations
func (h *RecommendationsHandler) getPatternRecommendations() []Recommendation {
	recommendations := make([]Recommendation, 0)

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

		issueType, namespace := parseKeyParts(key)
		if issueType == "" {
			continue
		}

		recID++
		recommendations = append(recommendations, Recommendation{
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
		})
	}

	return recommendations
}

// parseKeyParts splits a "type:namespace" key into its components
func parseKeyParts(key string) (issueType, namespace string) {
	if key == "" {
		return "", ""
	}
	idx := strings.Index(key, ":")
	if idx == -1 {
		return "", ""
	}
	return key[:idx], key[idx+1:]
}

// Helper functions

func calculateHistoricalConfidence(count int) float64 {
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
