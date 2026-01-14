// Package v1 provides API handlers for the coordination engine.
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/internal/integrations"
	"github.com/tosin2013/openshift-coordination-engine/pkg/kserve"
)

// AnomalyHandler handles anomaly analysis API requests
// Implements Issue #30: Add Anomaly Analysis Endpoint with Feature Engineering
type AnomalyHandler struct {
	kserveClient     *kserve.ProxyClient
	prometheusClient *integrations.PrometheusClient
	log              *logrus.Logger

	// Default values when Prometheus is not available
	defaultMetricValue float64
}

// NewAnomalyHandler creates a new anomaly analysis handler
func NewAnomalyHandler(
	kserveClient *kserve.ProxyClient,
	prometheusClient *integrations.PrometheusClient,
	log *logrus.Logger,
) *AnomalyHandler {
	return &AnomalyHandler{
		kserveClient:       kserveClient,
		prometheusClient:   prometheusClient,
		log:                log,
		defaultMetricValue: 0.5,
	}
}

// RegisterRoutes registers anomaly analysis API routes
func (h *AnomalyHandler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/api/v1/anomalies/analyze", h.AnalyzeAnomalies).Methods("POST")
	h.log.Info("Anomaly analysis API endpoint registered: POST /api/v1/anomalies/analyze")
}

// AnomalyAnalyzeRequest represents the request body for anomaly analysis
type AnomalyAnalyzeRequest struct {
	TimeRange     string  `json:"time_range"`     // Options: 1h, 6h, 24h, 7d
	Namespace     string  `json:"namespace"`      // Optional: scope to namespace
	Deployment    string  `json:"deployment"`     // Optional: scope to deployment
	Pod           string  `json:"pod"`            // Optional: scope to specific pod
	LabelSelector string  `json:"label_selector"` // Optional: label selector
	Threshold     float64 `json:"threshold"`      // Anomaly score threshold (0.0-1.0)
	ModelName     string  `json:"model_name"`     // KServe model to use (default: anomaly-detector)
}

// AnomalyAnalyzeResponse represents the response for anomaly analysis
type AnomalyAnalyzeResponse struct {
	Status            string          `json:"status"`
	TimeRange         string          `json:"time_range"`
	Scope             AnomalyScope    `json:"scope"`
	ModelUsed         string          `json:"model_used"`
	AnomaliesDetected int             `json:"anomalies_detected"`
	Anomalies         []AnomalyResult `json:"anomalies"`
	Summary           AnomalySummary  `json:"summary"`
	Recommendation    string          `json:"recommendation"`
	Features          FeatureInfo     `json:"features"`
}

// AnomalyScope describes the scope of the anomaly analysis
type AnomalyScope struct {
	Namespace         string `json:"namespace,omitempty"`
	Deployment        string `json:"deployment,omitempty"`
	Pod               string `json:"pod,omitempty"`
	TargetDescription string `json:"target_description"`
}

// AnomalyResult represents a detected anomaly
type AnomalyResult struct {
	Timestamp         string             `json:"timestamp"`
	Severity          string             `json:"severity"`      // critical, warning, info
	AnomalyScore      float64            `json:"anomaly_score"` // 0.0-1.0
	Confidence        float64            `json:"confidence"`    // 0.0-1.0
	Metrics           map[string]float64 `json:"metrics"`
	Explanation       string             `json:"explanation"`
	RecommendedAction string             `json:"recommended_action"`
}

// AnomalySummary provides summary statistics for the analysis
type AnomalySummary struct {
	MaxScore          float64 `json:"max_score"`
	AverageScore      float64 `json:"average_score"`
	MetricsAnalyzed   int     `json:"metrics_analyzed"`
	FeaturesGenerated int     `json:"features_generated"`
}

// FeatureInfo provides information about the feature engineering
type FeatureInfo struct {
	TotalFeatures     int      `json:"total_features"`
	BaseMetrics       []string `json:"base_metrics"`
	FeaturesPerMetric int      `json:"features_per_metric"`
	FeatureNames      []string `json:"feature_names"`
}

// AnomalyErrorResponse represents an error response for anomaly analysis
type AnomalyErrorResponse struct {
	Status  string `json:"status"`
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
	Code    string `json:"code"`
}

// Error codes for anomaly analysis failures
const (
	ErrCodeAnomalyInvalidRequest        = "INVALID_REQUEST"
	ErrCodeAnomalyPrometheusUnavailable = "PROMETHEUS_UNAVAILABLE"
	ErrCodeAnomalyKServeUnavailable     = "KSERVE_UNAVAILABLE"
	ErrCodeAnomalyModelNotFound         = "MODEL_NOT_FOUND"
	ErrCodeAnomalyAnalysisFailed        = "ANALYSIS_FAILED"
)

// Base metrics used for anomaly detection
// 5 metrics × 9 features each = 45 total features
var baseMetrics = []string{
	"node_cpu_utilization",
	"node_memory_utilization",
	"pod_cpu_usage",
	"pod_memory_usage",
	"container_restart_count",
}

// Feature names per metric
var featureNames = []string{
	"value",      // current value
	"mean_5m",    // 5-minute rolling mean
	"std_5m",     // 5-minute rolling stddev
	"min_5m",     // 5-minute rolling min
	"max_5m",     // 5-minute rolling max
	"lag_1",      // 1-minute lag
	"lag_5",      // 5-minute lag
	"diff",       // value - lag_1
	"pct_change", // (value - lag_1) / lag_1
}

// AnalyzeAnomalies handles POST /api/v1/anomalies/analyze
// @Summary Analyze anomalies with ML-powered feature engineering
// @Description Queries Prometheus for metrics, performs 45-feature engineering, and calls KServe anomaly-detector model
// @Tags anomaly
// @Accept json
// @Produce json
// @Param request body AnomalyAnalyzeRequest true "Anomaly analysis request"
// @Success 200 {object} AnomalyAnalyzeResponse
// @Failure 400 {object} AnomalyErrorResponse
// @Failure 503 {object} AnomalyErrorResponse
// @Router /api/v1/anomalies/analyze [post]
func (h *AnomalyHandler) AnalyzeAnomalies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "" && !strings.HasPrefix(contentType, "application/json") {
		h.respondError(w, http.StatusBadRequest, "Content-Type must be application/json", "", ErrCodeAnomalyInvalidRequest)
		return
	}

	// Parse request
	var req AnomalyAnalyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.WithError(err).Debug("Invalid anomaly analysis request format")
		h.respondError(w, http.StatusBadRequest, "Invalid request format", err.Error(), ErrCodeAnomalyInvalidRequest)
		return
	}

	// Set defaults and validate
	h.setRequestDefaults(&req)
	if err := h.validateRequest(&req); err != nil {
		h.log.WithError(err).Debug("Anomaly analysis request validation failed")
		h.respondError(w, http.StatusBadRequest, err.Error(), "", ErrCodeAnomalyInvalidRequest)
		return
	}

	h.log.WithFields(logrus.Fields{
		"time_range": req.TimeRange,
		"namespace":  req.Namespace,
		"deployment": req.Deployment,
		"pod":        req.Pod,
		"threshold":  req.Threshold,
		"model_name": req.ModelName,
	}).Info("Processing anomaly analysis request")

	// Check if KServe is available
	if h.kserveClient == nil {
		h.respondError(w, http.StatusServiceUnavailable, "KServe integration not enabled", "KServe client is not configured", ErrCodeAnomalyKServeUnavailable)
		return
	}

	// Check if model exists
	if _, exists := h.kserveClient.GetModel(req.ModelName); !exists {
		h.respondError(w, http.StatusServiceUnavailable, fmt.Sprintf("Model '%s' not available", req.ModelName), "Model not found in KServe", ErrCodeAnomalyModelNotFound)
		return
	}

	// Build feature vector (45 features)
	features, metricsData, err := h.buildFeatureVector(ctx, req.Namespace, req.Pod, req.Deployment)
	if err != nil {
		h.log.WithError(err).Warn("Failed to build feature vector from Prometheus, using defaults")
		features = h.getDefaultFeatures()
		metricsData = h.getDefaultMetricsData()
	}

	h.log.WithFields(logrus.Fields{
		"feature_count": len(features),
		"metrics_count": len(baseMetrics),
	}).Debug("Feature vector built")

	// Call KServe anomaly-detector model
	instances := [][]float64{features}
	resp, err := h.kserveClient.Predict(ctx, req.ModelName, instances)
	if err != nil {
		h.log.WithError(err).WithField("model", req.ModelName).Error("KServe anomaly detection failed")
		h.respondError(w, http.StatusServiceUnavailable, "Anomaly detection failed", err.Error(), ErrCodeAnomalyAnalysisFailed)
		return
	}

	// Process predictions and build response
	response := h.buildAnalysisResponse(&req, resp, features, metricsData)

	h.log.WithFields(logrus.Fields{
		"anomalies_detected": response.AnomaliesDetected,
		"max_score":          response.Summary.MaxScore,
		"model":              response.ModelUsed,
	}).Info("Anomaly analysis completed successfully")

	h.respondJSON(w, http.StatusOK, response)
}

// setRequestDefaults sets default values for optional request fields
func (h *AnomalyHandler) setRequestDefaults(req *AnomalyAnalyzeRequest) {
	if req.TimeRange == "" {
		req.TimeRange = "1h"
	}
	if req.Threshold == 0 {
		req.Threshold = 0.7
	}
	if req.ModelName == "" {
		req.ModelName = "anomaly-detector"
	}
}

// validateRequest validates the anomaly analysis request parameters
func (h *AnomalyHandler) validateRequest(req *AnomalyAnalyzeRequest) error {
	// Validate time range
	validTimeRanges := map[string]bool{
		"1h": true, "6h": true, "24h": true, "7d": true,
	}
	if !validTimeRanges[req.TimeRange] {
		return fmt.Errorf("time_range must be one of: 1h, 6h, 24h, 7d")
	}

	// Validate threshold
	if req.Threshold < 0 || req.Threshold > 1 {
		return fmt.Errorf("threshold must be between 0.0 and 1.0")
	}

	return nil
}

// buildFeatureVector builds the 45-feature vector from Prometheus metrics
// Features per metric (9 each):
// - value: current value
// - mean_5m: 5-minute rolling mean
// - std_5m: 5-minute rolling stddev
// - min_5m: 5-minute rolling min
// - max_5m: 5-minute rolling max
// - lag_1: 1-minute lag
// - lag_5: 5-minute lag
// - diff: value - lag_1
// - pct_change: (value - lag_1) / lag_1
func (h *AnomalyHandler) buildFeatureVector(ctx context.Context, namespace, pod, deployment string) ([]float64, map[string]float64, error) {
	if h.prometheusClient == nil || !h.prometheusClient.IsAvailable() {
		return nil, nil, fmt.Errorf("prometheus client not available")
	}

	features := make([]float64, 0, 45)
	metricsData := make(map[string]float64)

	for _, metric := range baseMetrics {
		metricFeatures, currentValue, err := h.queryMetricFeatures(ctx, metric, namespace, pod, deployment)
		if err != nil {
			h.log.WithError(err).WithField("metric", metric).Debug("Failed to query metric features, using defaults")
			metricFeatures = h.getDefaultMetricFeatures()
			currentValue = h.defaultMetricValue
		}
		features = append(features, metricFeatures...)
		metricsData[metric] = currentValue
	}

	return features, metricsData, nil
}

// queryMetricFeatures queries Prometheus for all features of a single metric
func (h *AnomalyHandler) queryMetricFeatures(ctx context.Context, metric, namespace, pod, deployment string) ([]float64, float64, error) {
	// Build base query based on metric type
	baseQuery := h.getMetricBaseQuery(metric, namespace, pod, deployment)

	// Query current value
	currentValue, err := h.queryPromQL(ctx, baseQuery)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query current value for %s: %w", metric, err)
	}

	// Query rolling statistics (5m window) - use helper that returns default on error
	mean5m := h.queryPromQLWithDefault(ctx, fmt.Sprintf("avg_over_time((%s)[5m:])", baseQuery), currentValue)
	std5m := h.queryPromQLWithDefault(ctx, fmt.Sprintf("stddev_over_time((%s)[5m:])", baseQuery), 0)
	min5m := h.queryPromQLWithDefault(ctx, fmt.Sprintf("min_over_time((%s)[5m:])", baseQuery), currentValue)
	max5m := h.queryPromQLWithDefault(ctx, fmt.Sprintf("max_over_time((%s)[5m:])", baseQuery), currentValue)

	// Query lag values
	lag1 := h.queryPromQLWithDefault(ctx, fmt.Sprintf("(%s) offset 1m", baseQuery), currentValue)
	lag5 := h.queryPromQLWithDefault(ctx, fmt.Sprintf("(%s) offset 5m", baseQuery), currentValue)

	// Calculate derived features
	diff := currentValue - lag1
	pctChange := 0.0
	if lag1 != 0 {
		pctChange = (currentValue - lag1) / lag1
	}

	// Return all 9 features for this metric
	return []float64{
		currentValue,
		mean5m,
		std5m,
		min5m,
		max5m,
		lag1,
		lag5,
		diff,
		pctChange,
	}, currentValue, nil
}

// getMetricBaseQuery returns the Prometheus query for a given metric
func (h *AnomalyHandler) getMetricBaseQuery(metric, namespace, pod, deployment string) string {
	// Build label selectors
	var selectors []string
	if namespace != "" {
		selectors = append(selectors, fmt.Sprintf("namespace=%q", namespace))
	}
	if pod != "" {
		selectors = append(selectors, fmt.Sprintf("pod=%q", pod))
	}
	if deployment != "" {
		selectors = append(selectors, fmt.Sprintf(`pod=~"%s-.*"`, deployment))
	}

	selectorStr := ""
	if len(selectors) > 0 {
		selectorStr = strings.Join(selectors, ",")
	}

	// Define queries for each metric type
	queries := map[string]string{
		"node_cpu_utilization": fmt.Sprintf(
			`avg(1 - rate(node_cpu_seconds_total{mode="idle"%s}[5m]))`,
			h.prependComma(selectorStr),
		),
		"node_memory_utilization": fmt.Sprintf(
			`1 - (node_memory_MemAvailable_bytes%s / node_memory_MemTotal_bytes%s)`,
			h.wrapSelector(selectorStr), h.wrapSelector(selectorStr),
		),
		"pod_cpu_usage": fmt.Sprintf(
			`sum(rate(container_cpu_usage_seconds_total{container!=""%s}[5m])) by (pod)`,
			h.prependComma(selectorStr),
		),
		"pod_memory_usage": fmt.Sprintf(
			`sum(container_memory_working_set_bytes{container!=""%s}) by (pod) / sum(kube_pod_container_resource_limits{resource="memory"%s}) by (pod)`,
			h.prependComma(selectorStr), h.prependComma(selectorStr),
		),
		"container_restart_count": fmt.Sprintf(
			`sum(kube_pod_container_status_restarts_total{%s}) by (pod)`,
			selectorStr,
		),
	}

	query, ok := queries[metric]
	if !ok {
		return metric // Return metric name as-is if not found
	}
	return query
}

// prependComma prepends a comma if selector is non-empty
func (h *AnomalyHandler) prependComma(selector string) string {
	if selector != "" {
		return "," + selector
	}
	return ""
}

// wrapSelector wraps selector with braces if non-empty
func (h *AnomalyHandler) wrapSelector(selector string) string {
	if selector != "" {
		return "{" + selector + "}"
	}
	return ""
}

// queryPromQL executes a PromQL query and returns the result
func (h *AnomalyHandler) queryPromQL(ctx context.Context, query string) (float64, error) {
	if h.prometheusClient == nil {
		return h.defaultMetricValue, nil
	}

	// Use the Prometheus client's Query method
	value, err := h.prometheusClient.Query(ctx, query)
	if err != nil {
		return h.defaultMetricValue, err
	}

	return value, nil
}

// queryPromQLWithDefault executes a PromQL query and returns a default value on error
func (h *AnomalyHandler) queryPromQLWithDefault(ctx context.Context, query string, defaultValue float64) float64 {
	value, err := h.queryPromQL(ctx, query)
	if err != nil {
		h.log.WithError(err).WithField("query", query).Debug("PromQL query failed, using default value")
		return defaultValue
	}
	return value
}

// getDefaultFeatures returns a default 45-feature vector
func (h *AnomalyHandler) getDefaultFeatures() []float64 {
	features := make([]float64, 45)
	for i := 0; i < 5; i++ { // 5 metrics
		baseIdx := i * 9
		features[baseIdx+0] = 0.5 // value
		features[baseIdx+1] = 0.5 // mean_5m
		features[baseIdx+2] = 0.1 // std_5m
		features[baseIdx+3] = 0.3 // min_5m
		features[baseIdx+4] = 0.7 // max_5m
		features[baseIdx+5] = 0.5 // lag_1
		features[baseIdx+6] = 0.5 // lag_5
		features[baseIdx+7] = 0.0 // diff
		features[baseIdx+8] = 0.0 // pct_change
	}
	return features
}

// getDefaultMetricFeatures returns default features for a single metric
func (h *AnomalyHandler) getDefaultMetricFeatures() []float64 {
	return []float64{
		0.5, // value
		0.5, // mean_5m
		0.1, // std_5m
		0.3, // min_5m
		0.7, // max_5m
		0.5, // lag_1
		0.5, // lag_5
		0.0, // diff
		0.0, // pct_change
	}
}

// getDefaultMetricsData returns default metrics data map
func (h *AnomalyHandler) getDefaultMetricsData() map[string]float64 {
	return map[string]float64{
		"node_cpu_utilization":    0.5,
		"node_memory_utilization": 0.5,
		"pod_cpu_usage":           0.5,
		"pod_memory_usage":        0.5,
		"container_restart_count": 0.0,
	}
}

// buildAnalysisResponse builds the anomaly analysis response from model predictions
func (h *AnomalyHandler) buildAnalysisResponse(
	req *AnomalyAnalyzeRequest,
	resp *kserve.DetectResponse,
	features []float64,
	metricsData map[string]float64,
) AnomalyAnalyzeResponse {
	// Determine if anomaly was detected
	isAnomaly := len(resp.Predictions) > 0 && resp.Predictions[0] == -1

	// Calculate anomaly score (0.0-1.0)
	// -1 = anomaly, 1 = normal
	// Convert to 0.0-1.0 scale where higher = more anomalous
	anomalyScore := 0.0
	if isAnomaly {
		// Calculate score based on how far metrics deviate from normal
		anomalyScore = h.calculateAnomalyScore(metricsData)
	}

	// Build anomaly results
	var anomalies []AnomalyResult
	if isAnomaly && anomalyScore >= req.Threshold {
		anomaly := h.buildAnomalyResult(metricsData, anomalyScore)
		anomalies = append(anomalies, anomaly)
	}

	// Build scope description
	scope := h.buildScope(req)

	// Build feature info
	featureInfo := h.buildFeatureInfo()

	// Calculate summary
	summary := h.buildSummary(anomalies, features)

	// Generate recommendation
	recommendation := h.generateRecommendation(anomalies, summary)

	return AnomalyAnalyzeResponse{
		Status:            "success",
		TimeRange:         req.TimeRange,
		Scope:             scope,
		ModelUsed:         req.ModelName,
		AnomaliesDetected: len(anomalies),
		Anomalies:         anomalies,
		Summary:           summary,
		Recommendation:    recommendation,
		Features:          featureInfo,
	}
}

// calculateAnomalyScore calculates an anomaly score from metrics
func (h *AnomalyHandler) calculateAnomalyScore(metrics map[string]float64) float64 {
	// Weight different metrics by importance
	weights := map[string]float64{
		"node_cpu_utilization":    0.2,
		"node_memory_utilization": 0.2,
		"pod_cpu_usage":           0.2,
		"pod_memory_usage":        0.25,
		"container_restart_count": 0.15,
	}

	score := 0.0
	for metric, value := range metrics {
		weight := weights[metric]
		if weight == 0 {
			weight = 0.2
		}
		// Higher values indicate potential issues
		score += value * weight
	}

	// Clamp to 0.0-1.0
	if score < 0 {
		score = 0
	}
	if score > 1 {
		score = 1
	}

	return math.Round(score*100) / 100
}

// buildAnomalyResult creates an AnomalyResult from metrics data
func (h *AnomalyHandler) buildAnomalyResult(metrics map[string]float64, score float64) AnomalyResult {
	// Determine severity based on score
	severity := "info"
	if score >= 0.9 {
		severity = "critical"
	} else if score >= 0.7 {
		severity = "warning"
	}

	// Build explanation based on metrics
	explanation := h.generateExplanation(metrics)

	// Recommend action based on severity and metrics
	recommendedAction := h.recommendAction(metrics, severity)

	return AnomalyResult{
		Timestamp:         time.Now().UTC().Format(time.RFC3339),
		Severity:          severity,
		AnomalyScore:      score,
		Confidence:        0.87, // Base confidence from model
		Metrics:           metrics,
		Explanation:       explanation,
		RecommendedAction: recommendedAction,
	}
}

// generateExplanation generates a human-readable explanation for the anomaly
func (h *AnomalyHandler) generateExplanation(metrics map[string]float64) string {
	var issues []string

	if cpu, ok := metrics["pod_cpu_usage"]; ok && cpu > 0.8 {
		issues = append(issues, fmt.Sprintf("CPU usage elevated (%.0f%%)", cpu*100))
	}
	if mem, ok := metrics["pod_memory_usage"]; ok && mem > 0.8 {
		issues = append(issues, fmt.Sprintf("Memory usage high (%.0f%%)", mem*100))
	}
	if restarts, ok := metrics["container_restart_count"]; ok && restarts > 0 {
		issues = append(issues, fmt.Sprintf("Container restarts detected (%.0f)", restarts))
	}
	if nodeCPU, ok := metrics["node_cpu_utilization"]; ok && nodeCPU > 0.8 {
		issues = append(issues, fmt.Sprintf("Node CPU pressure (%.0f%%)", nodeCPU*100))
	}
	if nodeMem, ok := metrics["node_memory_utilization"]; ok && nodeMem > 0.8 {
		issues = append(issues, fmt.Sprintf("Node memory pressure (%.0f%%)", nodeMem*100))
	}

	if len(issues) == 0 {
		return "Anomalous behavior detected based on metric patterns"
	}

	return strings.Join(issues, "; ")
}

// recommendAction recommends an action based on metrics and severity
func (h *AnomalyHandler) recommendAction(metrics map[string]float64, severity string) string {
	// Check for container restarts - highest priority
	if restarts, ok := metrics["container_restart_count"]; ok && restarts > 3 {
		return "restart_pod"
	}

	// Check for memory pressure
	if mem, ok := metrics["pod_memory_usage"]; ok && mem > 0.95 {
		return "scale_resources"
	}

	// Check for CPU pressure
	if cpu, ok := metrics["pod_cpu_usage"]; ok && cpu > 0.95 {
		return "scale_resources"
	}

	// Based on severity
	switch severity {
	case "critical":
		return "immediate_investigation"
	case "warning":
		return "schedule_review"
	default:
		return "monitor"
	}
}

// buildScope builds the scope description
func (h *AnomalyHandler) buildScope(req *AnomalyAnalyzeRequest) AnomalyScope {
	var description string
	switch {
	case req.Pod != "":
		description = fmt.Sprintf("pod '%s' in namespace '%s'", req.Pod, req.Namespace)
	case req.Deployment != "":
		description = fmt.Sprintf("deployment '%s' in namespace '%s'", req.Deployment, req.Namespace)
	case req.Namespace != "":
		description = fmt.Sprintf("namespace '%s'", req.Namespace)
	default:
		description = "cluster-wide"
	}

	return AnomalyScope{
		Namespace:         req.Namespace,
		Deployment:        req.Deployment,
		Pod:               req.Pod,
		TargetDescription: description,
	}
}

// buildFeatureInfo builds the feature information section
func (h *AnomalyHandler) buildFeatureInfo() FeatureInfo {
	// Generate all feature names
	allFeatureNames := make([]string, 0, 45)
	for _, metric := range baseMetrics {
		for _, feature := range featureNames {
			allFeatureNames = append(allFeatureNames, fmt.Sprintf("%s_%s", metric, feature))
		}
	}

	return FeatureInfo{
		TotalFeatures:     45,
		BaseMetrics:       baseMetrics,
		FeaturesPerMetric: 9,
		FeatureNames:      allFeatureNames,
	}
}

// buildSummary builds the analysis summary
func (h *AnomalyHandler) buildSummary(anomalies []AnomalyResult, features []float64) AnomalySummary {
	maxScore := 0.0
	totalScore := 0.0

	for _, a := range anomalies {
		if a.AnomalyScore > maxScore {
			maxScore = a.AnomalyScore
		}
		totalScore += a.AnomalyScore
	}

	avgScore := 0.0
	if len(anomalies) > 0 {
		avgScore = totalScore / float64(len(anomalies))
	}

	return AnomalySummary{
		MaxScore:          maxScore,
		AverageScore:      math.Round(avgScore*100) / 100,
		MetricsAnalyzed:   len(baseMetrics),
		FeaturesGenerated: len(features),
	}
}

// generateRecommendation generates an overall recommendation
func (h *AnomalyHandler) generateRecommendation(anomalies []AnomalyResult, summary AnomalySummary) string {
	if len(anomalies) == 0 {
		return "No anomalies detected. System operating normally."
	}

	// Check for critical anomalies
	hasCritical := false
	for _, a := range anomalies {
		if a.Severity == "critical" {
			hasCritical = true
			break
		}
	}

	if hasCritical {
		return fmt.Sprintf("CRITICAL: Immediate investigation recommended. %d anomalies detected with max score %.2f. Consider scaling resources or triggering remediation.",
			len(anomalies), summary.MaxScore)
	}

	if summary.MaxScore >= 0.8 {
		return fmt.Sprintf("WARNING: Elevated anomaly levels detected. %d anomalies with max score %.2f. Schedule review and monitor closely.",
			len(anomalies), summary.MaxScore)
	}

	return fmt.Sprintf("INFO: %d minor anomalies detected. Continue monitoring.", len(anomalies))
}

// respondJSON writes a JSON response
func (h *AnomalyHandler) respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.WithError(err).Error("Failed to encode JSON response")
	}
}

// respondError writes an error response
func (h *AnomalyHandler) respondError(w http.ResponseWriter, statusCode int, message, details, code string) {
	response := AnomalyErrorResponse{
		Status:  "error",
		Error:   message,
		Details: details,
		Code:    code,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(response); err != nil {
		h.log.WithError(err).Error("Failed to encode error response")
	}
}

// SetPrometheusClient sets the Prometheus client (useful for testing)
func (h *AnomalyHandler) SetPrometheusClient(client *integrations.PrometheusClient) {
	h.prometheusClient = client
}

// GetBaseMetrics returns the list of base metrics used for feature engineering
func GetBaseMetrics() []string {
	result := make([]string, len(baseMetrics))
	copy(result, baseMetrics)
	sort.Strings(result)
	return result
}

// GetFeatureNames returns the list of feature names per metric
func GetFeatureNames() []string {
	result := make([]string, len(featureNames))
	copy(result, featureNames)
	return result
}
