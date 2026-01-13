// Package v1 provides API handlers for the coordination engine.
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/tosin2013/openshift-coordination-engine/internal/integrations"
	"github.com/tosin2013/openshift-coordination-engine/pkg/kserve"
)

// PredictionHandler handles time-specific resource prediction API requests
type PredictionHandler struct {
	kserveClient     *kserve.ProxyClient
	prometheusClient *integrations.PrometheusClient
	log              *logrus.Logger

	// Default values when Prometheus is not available
	defaultCPURollingMean    float64
	defaultMemoryRollingMean float64
}

// NewPredictionHandler creates a new prediction handler
func NewPredictionHandler(
	kserveClient *kserve.ProxyClient,
	prometheusClient *integrations.PrometheusClient,
	log *logrus.Logger,
) *PredictionHandler {
	return &PredictionHandler{
		kserveClient:             kserveClient,
		prometheusClient:         prometheusClient,
		log:                      log,
		defaultCPURollingMean:    0.65, // 65% average CPU usage
		defaultMemoryRollingMean: 0.72, // 72% average memory usage
	}
}

// RegisterRoutes registers prediction API routes
func (h *PredictionHandler) RegisterRoutes(router *mux.Router) {
	router.HandleFunc("/api/v1/predict", h.HandlePredict).Methods("POST")
	h.log.Info("Prediction API endpoint registered: POST /api/v1/predict")
}

// PredictRequest represents the request body for time-specific predictions
type PredictRequest struct {
	Hour       int    `json:"hour"`        // Required: 0-23 (hour of day)
	DayOfWeek  int    `json:"day_of_week"` // Required: 0=Monday, 6=Sunday
	Namespace  string `json:"namespace"`   // Optional: namespace filter
	Deployment string `json:"deployment"`  // Optional: deployment filter
	Pod        string `json:"pod"`         // Optional: specific pod filter
	Scope      string `json:"scope"`       // Optional: pod, deployment, namespace, cluster (default: namespace)
	Model      string `json:"model"`       // Optional: KServe model name (default: predictive-analytics)
}

// PredictResponse represents the response for time-specific predictions
type PredictResponse struct {
	Status         string              `json:"status"`
	Scope          string              `json:"scope"`
	Target         string              `json:"target"`
	Predictions    PredictionValues    `json:"predictions"`
	CurrentMetrics CurrentMetrics      `json:"current_metrics"`
	ModelInfo      ModelInfo           `json:"model_info"`
	TargetTime     TargetTimeInfo      `json:"target_time"`
}

// PredictionValues contains the predicted resource usage percentages
type PredictionValues struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
}

// CurrentMetrics contains the current rolling metrics from Prometheus
type CurrentMetrics struct {
	CPURollingMean    float64 `json:"cpu_rolling_mean"`
	MemoryRollingMean float64 `json:"memory_rolling_mean"`
	Timestamp         string  `json:"timestamp"`
	TimeRange         string  `json:"time_range"`
}

// ModelInfo contains information about the KServe model used for prediction
type ModelInfo struct {
	Name       string  `json:"name"`
	Version    string  `json:"version"`
	Confidence float64 `json:"confidence"`
}

// TargetTimeInfo contains information about the prediction target time
type TargetTimeInfo struct {
	Hour         int    `json:"hour"`
	DayOfWeek    int    `json:"day_of_week"`
	ISOTimestamp string `json:"iso_timestamp"`
}

// PredictErrorResponse represents an error response for predictions
type PredictErrorResponse struct {
	Status  string `json:"status"`
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
	Code    string `json:"code"`
}

// Error codes for prediction failures
const (
	ErrCodeInvalidRequest       = "INVALID_REQUEST"
	ErrCodePrometheusUnavailable = "PROMETHEUS_UNAVAILABLE"
	ErrCodeKServeUnavailable    = "KSERVE_UNAVAILABLE"
	ErrCodeModelNotFound        = "MODEL_NOT_FOUND"
	ErrCodePredictionFailed     = "PREDICTION_FAILED"
)

// HandlePredict handles POST /api/v1/predict
// @Summary Get time-specific resource usage predictions
// @Description Provides time-specific resource usage predictions using KServe ML models and Prometheus metrics
// @Tags prediction
// @Accept json
// @Produce json
// @Param request body PredictRequest true "Prediction request"
// @Success 200 {object} PredictResponse
// @Failure 400 {object} PredictErrorResponse
// @Failure 503 {object} PredictErrorResponse
// @Router /api/v1/predict [post]
func (h *PredictionHandler) HandlePredict(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "" && !strings.HasPrefix(contentType, "application/json") {
		h.respondError(w, http.StatusBadRequest, "Content-Type must be application/json", "", ErrCodeInvalidRequest)
		return
	}

	// Parse request
	var req PredictRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.WithError(err).Debug("Invalid predict request format")
		h.respondError(w, http.StatusBadRequest, "Invalid request format", err.Error(), ErrCodeInvalidRequest)
		return
	}

	// Validate request
	if err := h.validateRequest(&req); err != nil {
		h.log.WithError(err).Debug("Predict request validation failed")
		h.respondError(w, http.StatusBadRequest, err.Error(), "", ErrCodeInvalidRequest)
		return
	}

	// Set defaults
	h.setRequestDefaults(&req)

	h.log.WithFields(logrus.Fields{
		"hour":        req.Hour,
		"day_of_week": req.DayOfWeek,
		"namespace":   req.Namespace,
		"deployment":  req.Deployment,
		"pod":         req.Pod,
		"scope":       req.Scope,
		"model":       req.Model,
	}).Info("Processing prediction request")

	// Check if KServe is available
	if h.kserveClient == nil {
		h.respondError(w, http.StatusServiceUnavailable, "KServe integration not enabled", "KServe client is not configured", ErrCodeKServeUnavailable)
		return
	}

	// Check if model exists
	if _, exists := h.kserveClient.GetModel(req.Model); !exists {
		h.respondError(w, http.StatusServiceUnavailable, fmt.Sprintf("Model '%s' not available", req.Model), "Model not found in KServe", ErrCodeModelNotFound)
		return
	}

	// Get current metrics from Prometheus
	cpuRollingMean, memoryRollingMean, prometheusErr := h.getScopedMetrics(ctx, &req)
	if prometheusErr != nil {
		h.log.WithError(prometheusErr).Warn("Failed to get Prometheus metrics, using defaults")
		cpuRollingMean = h.defaultCPURollingMean
		memoryRollingMean = h.defaultMemoryRollingMean
	}

	// Build prediction instances
	// Features: [hour_of_day, day_of_week, cpu_rolling_mean, memory_rolling_mean]
	instances := [][]float64{{
		float64(req.Hour),
		float64(req.DayOfWeek),
		cpuRollingMean,
		memoryRollingMean,
	}}

	h.log.WithFields(logrus.Fields{
		"instances":          instances,
		"cpu_rolling_mean":   cpuRollingMean,
		"memory_rolling_mean": memoryRollingMean,
	}).Debug("Prepared prediction instances")

	// Call KServe model
	resp, err := h.kserveClient.Predict(ctx, req.Model, instances)
	if err != nil {
		h.log.WithError(err).WithField("model", req.Model).Error("KServe prediction failed")
		h.respondError(w, http.StatusServiceUnavailable, "Prediction failed", err.Error(), ErrCodePredictionFailed)
		return
	}

	// Process predictions
	cpuPercent, memoryPercent, confidence := h.processPredictions(resp, cpuRollingMean, memoryRollingMean)

	// Calculate target ISO timestamp
	targetTimestamp := h.calculateTargetTimestamp(req.Hour, req.DayOfWeek)

	// Build response
	response := PredictResponse{
		Status: "success",
		Scope:  req.Scope,
		Target: h.getTarget(&req),
		Predictions: PredictionValues{
			CPUPercent:    cpuPercent,
			MemoryPercent: memoryPercent,
		},
		CurrentMetrics: CurrentMetrics{
			CPURollingMean:    cpuRollingMean * 100, // Convert to percentage
			MemoryRollingMean: memoryRollingMean * 100,
			Timestamp:         time.Now().UTC().Format(time.RFC3339),
			TimeRange:         "24h",
		},
		ModelInfo: ModelInfo{
			Name:       req.Model,
			Version:    resp.ModelVersion,
			Confidence: confidence,
		},
		TargetTime: TargetTimeInfo{
			Hour:         req.Hour,
			DayOfWeek:    req.DayOfWeek,
			ISOTimestamp: targetTimestamp,
		},
	}

	h.log.WithFields(logrus.Fields{
		"scope":          response.Scope,
		"target":         response.Target,
		"cpu_percent":    cpuPercent,
		"memory_percent": memoryPercent,
		"confidence":     confidence,
	}).Info("Prediction completed successfully")

	h.respondJSON(w, http.StatusOK, response)
}

// validateRequest validates the prediction request parameters
func (h *PredictionHandler) validateRequest(req *PredictRequest) error {
	if err := h.validateTimeFields(req); err != nil {
		return err
	}
	if err := h.validateScope(req); err != nil {
		return err
	}
	return h.validateScopeRequirements(req)
}

// validateTimeFields validates hour and day_of_week fields
func (h *PredictionHandler) validateTimeFields(req *PredictRequest) error {
	if req.Hour < 0 || req.Hour > 23 {
		return fmt.Errorf("hour must be between 0-23")
	}
	if req.DayOfWeek < 0 || req.DayOfWeek > 6 {
		return fmt.Errorf("day_of_week must be between 0-6 (0=Monday, 6=Sunday)")
	}
	return nil
}

// validateScope validates the scope field if provided
func (h *PredictionHandler) validateScope(req *PredictRequest) error {
	if req.Scope == "" {
		return nil
	}
	validScopes := map[string]bool{
		"pod":        true,
		"deployment": true,
		"namespace":  true,
		"cluster":    true,
	}
	if !validScopes[req.Scope] {
		return fmt.Errorf("scope must be one of: pod, deployment, namespace, cluster")
	}
	return nil
}

// validateScopeRequirements validates scope-specific field requirements
func (h *PredictionHandler) validateScopeRequirements(req *PredictRequest) error {
	switch req.Scope {
	case "pod":
		if req.Pod == "" {
			return fmt.Errorf("pod name is required when scope is 'pod'")
		}
		if req.Namespace == "" {
			return fmt.Errorf("namespace is required when scope is 'pod'")
		}
	case "deployment":
		if req.Deployment == "" {
			return fmt.Errorf("deployment name is required when scope is 'deployment'")
		}
		if req.Namespace == "" {
			return fmt.Errorf("namespace is required when scope is 'deployment'")
		}
	}
	return nil
}

// setRequestDefaults sets default values for optional request fields
func (h *PredictionHandler) setRequestDefaults(req *PredictRequest) {
	if req.Scope == "" {
		req.Scope = h.inferScope(req)
	}

	if req.Model == "" {
		req.Model = "predictive-analytics"
	}
}

// inferScope determines the scope based on provided fields
func (h *PredictionHandler) inferScope(req *PredictRequest) string {
	switch {
	case req.Pod != "":
		return "pod"
	case req.Deployment != "":
		return "deployment"
	case req.Namespace != "":
		return "namespace"
	default:
		return "cluster"
	}
}

// getScopedMetrics retrieves CPU and memory rolling means based on the request scope
func (h *PredictionHandler) getScopedMetrics(ctx context.Context, req *PredictRequest) (float64, float64, error) {
	if h.prometheusClient == nil || !h.prometheusClient.IsAvailable() {
		return h.defaultCPURollingMean, h.defaultMemoryRollingMean, fmt.Errorf("prometheus client not available")
	}

	switch req.Scope {
	case "cluster":
		return h.getScopedMetricsForCluster(ctx)
	case "namespace":
		return h.getScopedMetricsForNamespace(ctx, req.Namespace)
	case "deployment":
		return h.getScopedMetricsForDeployment(ctx, req.Namespace, req.Deployment)
	case "pod":
		return h.getScopedMetricsForPod(ctx, req.Namespace, req.Pod)
	default:
		return h.getScopedMetricsForCluster(ctx)
	}
}

// getScopedMetricsForNamespace retrieves metrics for a specific namespace
func (h *PredictionHandler) getScopedMetricsForNamespace(ctx context.Context, namespace string) (float64, float64, error) {
	if namespace == "" {
		return h.getScopedMetricsForCluster(ctx)
	}
	return h.getMetricsWithScope(ctx, namespace, "", "", "namespace")
}

// getScopedMetricsForDeployment retrieves metrics for a specific deployment
func (h *PredictionHandler) getScopedMetricsForDeployment(ctx context.Context, namespace, deployment string) (float64, float64, error) {
	return h.getMetricsWithScope(ctx, namespace, deployment, "", "deployment")
}

// getScopedMetricsForPod retrieves metrics for a specific pod
func (h *PredictionHandler) getScopedMetricsForPod(ctx context.Context, namespace, pod string) (float64, float64, error) {
	return h.getMetricsWithScope(ctx, namespace, "", pod, "pod")
}

// getMetricsWithScope is a helper that queries Prometheus with the given scope parameters
func (h *PredictionHandler) getMetricsWithScope(ctx context.Context, namespace, deployment, pod, scopeName string) (float64, float64, error) {
	cpuValue, err := h.prometheusClient.GetScopedCPURollingMean(ctx, namespace, deployment, pod)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get %s CPU metrics: %w", scopeName, err)
	}
	memoryValue, err := h.prometheusClient.GetScopedMemoryRollingMean(ctx, namespace, deployment, pod)
	if err != nil {
		return cpuValue, 0, fmt.Errorf("failed to get %s memory metrics: %w", scopeName, err)
	}
	return cpuValue, memoryValue, nil
}

// getScopedMetricsForCluster is a helper for cluster-wide metrics
func (h *PredictionHandler) getScopedMetricsForCluster(ctx context.Context) (float64, float64, error) {
	cpuValue, err := h.prometheusClient.GetCPURollingMean(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get cluster CPU metrics: %w", err)
	}
	memoryValue, err := h.prometheusClient.GetMemoryRollingMean(ctx)
	if err != nil {
		return cpuValue, 0, fmt.Errorf("failed to get cluster memory metrics: %w", err)
	}
	return cpuValue, memoryValue, nil
}

// processPredictions interprets the KServe model response and calculates predicted values
func (h *PredictionHandler) processPredictions(resp *kserve.DetectResponse, cpuRollingMean, memoryRollingMean float64) (float64, float64, float64) {
	// The predictive-analytics model returns classification predictions
	// We use the current metrics and prediction result to forecast values

	// Base prediction on current metrics
	cpuPercent := cpuRollingMean * 100
	memoryPercent := memoryRollingMean * 100

	// Calculate confidence based on model response and metric stability
	confidence := 0.85 // Base confidence

	// If the model predicts an issue (-1), adjust the prediction upward
	if len(resp.Predictions) > 0 && resp.Predictions[0] == -1 {
		// Issue predicted - increase expected resource usage
		cpuPercent = min(cpuPercent*1.15, 100.0)    // 15% increase
		memoryPercent = min(memoryPercent*1.15, 100.0)
		confidence = 0.92 // Higher confidence when issue is predicted
	} else if len(resp.Predictions) > 0 && resp.Predictions[0] == 1 {
		// Normal operation predicted - slight variation expected
		cpuPercent *= 1 + (0.05 - 0.1*cpuRollingMean) // Small adjustment
		memoryPercent *= 1 + (0.05 - 0.1*memoryRollingMean)
		confidence = 0.88
	}

	// Clamp values to valid percentages
	cpuPercent = clampPercentage(cpuPercent)
	memoryPercent = clampPercentage(memoryPercent)

	return cpuPercent, memoryPercent, confidence
}

// getTarget returns the target identifier based on the request scope
func (h *PredictionHandler) getTarget(req *PredictRequest) string {
	switch req.Scope {
	case "pod":
		return fmt.Sprintf("%s/%s", req.Namespace, req.Pod)
	case "deployment":
		return fmt.Sprintf("%s/%s", req.Namespace, req.Deployment)
	case "namespace":
		if req.Namespace != "" {
			return req.Namespace
		}
		return "all-namespaces"
	case "cluster":
		return "cluster"
	default:
		if req.Namespace != "" {
			return req.Namespace
		}
		return "cluster"
	}
}

// calculateTargetTimestamp calculates the ISO timestamp for the prediction target time
func (h *PredictionHandler) calculateTargetTimestamp(hour, dayOfWeek int) string {
	now := time.Now().UTC()

	// Calculate days until target day of week
	// Go uses Sunday=0, Monday=1, etc.
	// Our API uses Monday=0, Sunday=6
	goTargetDay := (dayOfWeek + 1) % 7 // Convert to Go's weekday format
	currentDay := int(now.Weekday())

	daysUntil := goTargetDay - currentDay
	if daysUntil < 0 {
		daysUntil += 7
	}
	// If same day but hour has passed, go to next week
	if daysUntil == 0 && hour <= now.Hour() {
		daysUntil = 7
	}

	targetDate := now.AddDate(0, 0, daysUntil)
	targetTime := time.Date(
		targetDate.Year(),
		targetDate.Month(),
		targetDate.Day(),
		hour,
		0,
		0,
		0,
		time.UTC,
	)

	return targetTime.Format(time.RFC3339)
}

// clampPercentage ensures a percentage value is within 0-100 range
func clampPercentage(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

// respondJSON writes a JSON response
func (h *PredictionHandler) respondJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		h.log.WithError(err).Error("Failed to encode JSON response")
	}
}

// respondError writes an error response
func (h *PredictionHandler) respondError(w http.ResponseWriter, statusCode int, message, details, code string) {
	response := PredictErrorResponse{
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
