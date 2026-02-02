// Package v1 provides API handlers for the coordination engine.
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"

	"github.com/KubeHeal/openshift-coordination-engine/internal/integrations"
	"github.com/KubeHeal/openshift-coordination-engine/pkg/features"
	"github.com/KubeHeal/openshift-coordination-engine/pkg/kserve"
)

// PredictionHandler handles time-specific resource prediction API requests
type PredictionHandler struct {
	kserveClient     *kserve.ProxyClient
	prometheusClient *integrations.PrometheusClient
	featureBuilder   *features.PredictiveFeatureBuilder
	log              *logrus.Logger

	// Default values when Prometheus is not available (Issue #58)
	// These match the 5 features expected by the predictive-analytics model:
	// cpu_usage, memory_usage, disk_usage, network_in, network_out
	defaultCPURollingMean    float64
	defaultMemoryRollingMean float64
	defaultDiskUsage         float64
	defaultNetworkIn         float64
	defaultNetworkOut        float64

	// Feature engineering configuration
	enableFeatureEngineering bool
}

// PredictionHandlerConfig holds configuration for the prediction handler
type PredictionHandlerConfig struct {
	// EnableFeatureEngineering enables the 3200+-feature vector for predictive-analytics model
	// When enabled, the handler queries Prometheus for historical data and builds
	// engineered features (lags, rolling stats, trends) matching the model's training.
	// When disabled, 5 raw features are sent matching the model's base metrics (Issue #58):
	// cpu_usage, memory_usage, disk_usage, network_in, network_out
	EnableFeatureEngineering bool

	// LookbackHours is the number of hours to look back for historical data
	LookbackHours int

	// ExpectedFeatureCount is the number of features the model expects.
	// If set (> 0), the builder will log a warning if the generated count doesn't match.
	ExpectedFeatureCount int
}

// DefaultPredictionHandlerConfig returns the default configuration.
//
// IMPORTANT: This function provides defaults for backward compatibility only.
// Production deployments should use NewPredictionHandlerWithConfig() with explicit
// configuration from environment variables (Issue #57):
//
//	predictionConfig := v1.PredictionHandlerConfig{
//	    EnableFeatureEngineering: cfg.FeatureEngineering.Enabled,
//	    LookbackHours:            cfg.FeatureEngineering.LookbackHours,
//	    ExpectedFeatureCount:     cfg.FeatureEngineering.ExpectedFeatureCount,
//	}
//	handler := v1.NewPredictionHandlerWithConfig(client, prom, log, predictionConfig)
//
// Environment variables:
//   - ENABLE_FEATURE_ENGINEERING: "true" or "false" (default: true)
//   - FEATURE_ENGINEERING_LOOKBACK_HOURS: hours of historical data (default: 24)
//   - FEATURE_ENGINEERING_EXPECTED_COUNT: expected feature count, 0 to disable validation
func DefaultPredictionHandlerConfig() PredictionHandlerConfig {
	defaultConfig := features.DefaultPredictiveConfig()
	return PredictionHandlerConfig{
		EnableFeatureEngineering: true,
		LookbackHours:            defaultConfig.LookbackHours,
		ExpectedFeatureCount:     0, // Disabled by default
	}
}

// NewPredictionHandler creates a new prediction handler with default configuration.
//
// Deprecated: Use NewPredictionHandlerWithConfig with explicit configuration from
// environment variables. This function uses hardcoded defaults and ignores
// ENABLE_FEATURE_ENGINEERING environment variable. See Issue #57.
func NewPredictionHandler(
	kserveClient *kserve.ProxyClient,
	prometheusClient *integrations.PrometheusClient,
	log *logrus.Logger,
) *PredictionHandler {
	return NewPredictionHandlerWithConfig(kserveClient, prometheusClient, log, DefaultPredictionHandlerConfig())
}

// NewPredictionHandlerWithConfig creates a new prediction handler with custom configuration
func NewPredictionHandlerWithConfig(
	kserveClient *kserve.ProxyClient,
	prometheusClient *integrations.PrometheusClient,
	log *logrus.Logger,
	config PredictionHandlerConfig,
) *PredictionHandler {
	var featureBuilder *features.PredictiveFeatureBuilder

	// Create feature builder based on configuration and Prometheus availability
	switch {
	case config.EnableFeatureEngineering && prometheusClient != nil:
		adapter := features.NewPrometheusAdapter(prometheusClient)

		// Build feature config from handler config
		featureConfig := features.PredictiveFeatureConfig{
			LookbackHours:        config.LookbackHours,
			Enabled:              true,
			ExpectedFeatureCount: config.ExpectedFeatureCount,
		}
		if featureConfig.LookbackHours == 0 {
			featureConfig.LookbackHours = 24 // Default
		}

		featureBuilder = features.NewPredictiveFeatureBuilder(adapter, featureConfig, log)
		log.WithFields(logrus.Fields{
			"lookback_hours":         featureConfig.LookbackHours,
			"feature_count":          featureBuilder.GetFeatureInfo().TotalFeatures,
			"base_metrics":           len(features.GetPredictiveBaseMetrics()),
			"expected_feature_count": config.ExpectedFeatureCount,
		}).Info("Predictive feature engineering enabled")

	case config.EnableFeatureEngineering:
		log.Warn("Feature engineering enabled but Prometheus not available, falling back to raw metrics")

	default:
		// Feature engineering explicitly disabled via ENABLE_FEATURE_ENGINEERING=false (Issue #57)
		log.WithFields(logrus.Fields{
			"expected_feature_count": config.ExpectedFeatureCount,
		}).Info("Predictive feature engineering disabled, using raw metrics only")
	}

	return &PredictionHandler{
		kserveClient:             kserveClient,
		prometheusClient:         prometheusClient,
		featureBuilder:           featureBuilder,
		log:                      log,
		defaultCPURollingMean:    0.65, // 65% average CPU usage
		defaultMemoryRollingMean: 0.72, // 72% average memory usage
		defaultDiskUsage:         0.45, // 45% average disk usage (Issue #58)
		defaultNetworkIn:         0.10, // 10% normalized network in (Issue #58)
		defaultNetworkOut:        0.08, // 8% normalized network out (Issue #58)
		enableFeatureEngineering: config.EnableFeatureEngineering,
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
	Status         string           `json:"status"`
	Scope          string           `json:"scope"`
	Target         string           `json:"target"`
	Predictions    PredictionValues `json:"predictions"`
	CurrentMetrics CurrentMetrics   `json:"current_metrics"`
	ModelInfo      ModelInfo        `json:"model_info"`
	TargetTime     TargetTimeInfo   `json:"target_time"`
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
	ErrCodeInvalidRequest        = "INVALID_REQUEST"
	ErrCodePrometheusUnavailable = "PROMETHEUS_UNAVAILABLE"
	ErrCodeKServeUnavailable     = "KSERVE_UNAVAILABLE"
	ErrCodeModelNotFound         = "MODEL_NOT_FOUND"
	ErrCodePredictionFailed      = "PREDICTION_FAILED"
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

	// Parse and validate request
	req, err := h.parseAndValidateRequest(r)
	if err != nil {
		h.handleRequestError(w, err)
		return
	}

	h.logPredictionRequest(req)

	// Validate KServe availability
	if err := h.validateKServeAvailability(req.Model); err != nil {
		h.handleServiceError(w, err)
		return
	}

	// Get metrics for response (used for logging and response building)
	cpuRollingMean, memoryRollingMean := h.getMetricsWithDefaults(ctx, req)

	// Build prediction instances (Issue #58: uses 5 raw metrics when feature engineering is disabled)
	instances, featureCount := h.buildPredictionInstances(ctx, req)

	h.logPredictionInstances(featureCount, cpuRollingMean, memoryRollingMean)

	// Execute prediction
	cpuPercent, memoryPercent, confidence, modelVersion, err := h.executePrediction(ctx, req.Model, instances, cpuRollingMean, memoryRollingMean)
	if err != nil {
		h.handleServiceError(w, err)
		return
	}

	// Build and send response
	response := h.buildPredictResponse(req, cpuPercent, memoryPercent, confidence, modelVersion, cpuRollingMean, memoryRollingMean)
	h.logPredictionSuccess(&response, cpuPercent, memoryPercent, confidence)
	h.respondJSON(w, http.StatusOK, response)
}

// parseAndValidateRequest parses the request body and validates it
func (h *PredictionHandler) parseAndValidateRequest(r *http.Request) (*PredictRequest, error) {
	// Check content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "" && !strings.HasPrefix(contentType, "application/json") {
		return nil, &requestError{message: "Content-Type must be application/json", code: ErrCodeInvalidRequest}
	}

	// Parse request
	var req PredictRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.log.WithError(err).Debug("Invalid predict request format")
		return nil, &requestError{message: "Invalid request format", details: err.Error(), code: ErrCodeInvalidRequest}
	}

	// Validate request
	if err := h.validateRequest(&req); err != nil {
		h.log.WithError(err).Debug("Predict request validation failed")
		return nil, &requestError{message: err.Error(), code: ErrCodeInvalidRequest}
	}

	// Set defaults
	h.setRequestDefaults(&req)
	return &req, nil
}

// requestError represents a request validation error
type requestError struct {
	message string
	details string
	code    string
}

func (e *requestError) Error() string { return e.message }

// serviceError represents a service availability error
type serviceError struct {
	message string
	details string
	code    string
}

func (e *serviceError) Error() string { return e.message }

// handleRequestError handles request validation errors
func (h *PredictionHandler) handleRequestError(w http.ResponseWriter, err error) {
	var reqErr *requestError
	if errors.As(err, &reqErr) {
		h.respondError(w, http.StatusBadRequest, reqErr.message, reqErr.details, reqErr.code)
	}
}

// handleServiceError handles service availability errors
func (h *PredictionHandler) handleServiceError(w http.ResponseWriter, err error) {
	var svcErr *serviceError
	if errors.As(err, &svcErr) {
		h.respondError(w, http.StatusServiceUnavailable, svcErr.message, svcErr.details, svcErr.code)
	}
}

// validateKServeAvailability checks if KServe and the requested model are available
func (h *PredictionHandler) validateKServeAvailability(model string) error {
	if h.kserveClient == nil {
		return &serviceError{message: "KServe integration not enabled", details: "KServe client is not configured", code: ErrCodeKServeUnavailable}
	}
	if _, exists := h.kserveClient.GetModel(model); !exists {
		return &serviceError{message: fmt.Sprintf("Model '%s' not available", model), details: "Model not found in KServe", code: ErrCodeModelNotFound}
	}
	return nil
}

// getMetricsWithDefaults retrieves metrics from Prometheus or returns defaults
func (h *PredictionHandler) getMetricsWithDefaults(ctx context.Context, req *PredictRequest) (cpuRollingMean, memoryRollingMean float64) {
	cpuRollingMean, memoryRollingMean, prometheusErr := h.getScopedMetrics(ctx, req)
	if prometheusErr != nil {
		h.log.WithError(prometheusErr).Warn("Failed to get Prometheus metrics, using defaults")
		return h.defaultCPURollingMean, h.defaultMemoryRollingMean
	}
	return cpuRollingMean, memoryRollingMean
}

// buildPredictionInstances builds the feature vector for prediction
func (h *PredictionHandler) buildPredictionInstances(ctx context.Context, req *PredictRequest) ([][]float64, int) {
	// Use feature engineering for predictive-analytics model if enabled
	if req.Model == "predictive-analytics" && h.featureBuilder != nil && h.enableFeatureEngineering {
		featureVector, err := h.featureBuilder.BuildFeatures(ctx, req.Namespace, req.Deployment, req.Pod)
		if err != nil {
			h.log.WithError(err).Warn("Feature engineering failed, falling back to raw metrics")
			// Issue #58: Use 5 raw metrics that match the model's training features
			return h.buildRawMetricInstances(ctx, req)
		}
		h.log.WithFields(logrus.Fields{
			"feature_count": featureVector.FeatureCount,
			"metrics":       featureVector.MetricsData,
		}).Debug("Built engineered features for prediction")
		return [][]float64{featureVector.Features}, featureVector.FeatureCount
	}
	// Issue #58: Use 5 raw features matching the model's expected input:
	// [cpu_usage, memory_usage, disk_usage, network_in, network_out]
	return h.buildRawMetricInstances(ctx, req)
}

// executePrediction calls the KServe model and processes the response
func (h *PredictionHandler) executePrediction(ctx context.Context, model string, instances [][]float64, cpuRollingMean, memoryRollingMean float64) (cpuPercent, memoryPercent, confidence float64, modelVersion string, err error) {
	resp, err := h.kserveClient.PredictFlexible(ctx, model, instances)
	if err != nil {
		h.log.WithError(err).WithField("model", model).Error("KServe prediction failed")
		return 0, 0, 0, "", &serviceError{message: "Prediction failed", details: err.Error(), code: ErrCodePredictionFailed}
	}

	return h.processKServeResponse(resp, cpuRollingMean, memoryRollingMean)
}

// processKServeResponse processes the KServe response based on its type
func (h *PredictionHandler) processKServeResponse(resp *kserve.ModelResponse, cpuRollingMean, memoryRollingMean float64) (cpuPercent, memoryPercent, confidence float64, modelVersion string, err error) {
	switch resp.Type {
	case "forecast":
		if resp.ForecastResponse == nil {
			return 0, 0, 0, "", &serviceError{message: "Prediction failed", details: "Empty forecast response from model", code: ErrCodePredictionFailed}
		}
		cpuPercent, memoryPercent, confidence = h.processForecastPredictions(resp.ForecastResponse, cpuRollingMean, memoryRollingMean)
		return cpuPercent, memoryPercent, confidence, resp.ForecastResponse.ModelVersion, nil
	case "anomaly":
		if resp.AnomalyResponse == nil {
			return 0, 0, 0, "", &serviceError{message: "Prediction failed", details: "Empty anomaly response from model", code: ErrCodePredictionFailed}
		}
		cpuPercent, memoryPercent, confidence = h.processAnomalyPredictions(resp.AnomalyResponse, cpuRollingMean, memoryRollingMean)
		return cpuPercent, memoryPercent, confidence, resp.AnomalyResponse.ModelVersion, nil
	default:
		return 0, 0, 0, "", &serviceError{message: "Prediction failed", details: "Unknown response format from model", code: ErrCodePredictionFailed}
	}
}

// buildPredictResponse constructs the prediction response
func (h *PredictionHandler) buildPredictResponse(req *PredictRequest, cpuPercent, memoryPercent, confidence float64, modelVersion string, cpuRollingMean, memoryRollingMean float64) PredictResponse {
	return PredictResponse{
		Status: "success",
		Scope:  req.Scope,
		Target: h.getTarget(req),
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
			Version:    modelVersion,
			Confidence: confidence,
		},
		TargetTime: TargetTimeInfo{
			Hour:         req.Hour,
			DayOfWeek:    req.DayOfWeek,
			ISOTimestamp: h.calculateTargetTimestamp(req.Hour, req.DayOfWeek),
		},
	}
}

// logPredictionRequest logs the incoming prediction request
func (h *PredictionHandler) logPredictionRequest(req *PredictRequest) {
	h.log.WithFields(logrus.Fields{
		"hour":        req.Hour,
		"day_of_week": req.DayOfWeek,
		"namespace":   req.Namespace,
		"deployment":  req.Deployment,
		"pod":         req.Pod,
		"scope":       req.Scope,
		"model":       req.Model,
	}).Info("Processing prediction request")
}

// logPredictionInstances logs the prepared prediction instances
func (h *PredictionHandler) logPredictionInstances(featureCount int, cpuRollingMean, memoryRollingMean float64) {
	h.log.WithFields(logrus.Fields{
		"feature_count":       featureCount,
		"cpu_rolling_mean":    cpuRollingMean,
		"memory_rolling_mean": memoryRollingMean,
		"feature_engineering": h.enableFeatureEngineering && h.featureBuilder != nil,
	}).Debug("Prepared prediction instances")
}

// logPredictionSuccess logs successful prediction completion
func (h *PredictionHandler) logPredictionSuccess(response *PredictResponse, cpuPercent, memoryPercent, confidence float64) {
	h.log.WithFields(logrus.Fields{
		"scope":          response.Scope,
		"target":         response.Target,
		"cpu_percent":    cpuPercent,
		"memory_percent": memoryPercent,
		"confidence":     confidence,
	}).Info("Prediction completed successfully")
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

// processForecastPredictions interprets the predictive-analytics model response with forecast data
func (h *PredictionHandler) processForecastPredictions(resp *kserve.ForecastResponse, cpuRollingMean, memoryRollingMean float64) (float64, float64, float64) {
	// Default values based on rolling means
	cpuPercent := cpuRollingMean * 100
	memoryPercent := memoryRollingMean * 100
	confidence := 0.85 // Base confidence

	// Extract CPU forecast if available
	if cpuForecast, ok := resp.Predictions["cpu_usage"]; ok && len(cpuForecast.Forecast) > 0 {
		// Use the first forecast value (closest prediction)
		cpuPercent = cpuForecast.Forecast[0] * 100

		// Use confidence from the model if available
		if len(cpuForecast.Confidence) > 0 {
			confidence = cpuForecast.Confidence[0]
		}
	}

	// Extract memory forecast if available
	if memForecast, ok := resp.Predictions["memory_usage"]; ok && len(memForecast.Forecast) > 0 {
		// Use the first forecast value (closest prediction)
		memoryPercent = memForecast.Forecast[0] * 100

		// Average confidence if both metrics have confidence values
		if len(memForecast.Confidence) > 0 {
			if cpuForecast, ok := resp.Predictions["cpu_usage"]; ok && len(cpuForecast.Confidence) > 0 {
				confidence = (cpuForecast.Confidence[0] + memForecast.Confidence[0]) / 2
			} else {
				confidence = memForecast.Confidence[0]
			}
		}
	}

	// Clamp values to valid percentages
	cpuPercent = clampPercentage(cpuPercent)
	memoryPercent = clampPercentage(memoryPercent)

	h.log.WithFields(logrus.Fields{
		"cpu_percent":    cpuPercent,
		"memory_percent": memoryPercent,
		"confidence":     confidence,
		"model_type":     "forecast",
	}).Debug("Processed forecast predictions")

	return cpuPercent, memoryPercent, confidence
}

// processAnomalyPredictions interprets the anomaly-detector model response (legacy behavior)
func (h *PredictionHandler) processAnomalyPredictions(resp *kserve.DetectResponse, cpuRollingMean, memoryRollingMean float64) (float64, float64, float64) {
	// The anomaly-detector model returns classification predictions (-1 or 1)
	// We use the current metrics and prediction result to forecast values

	// Base prediction on current metrics
	cpuPercent := cpuRollingMean * 100
	memoryPercent := memoryRollingMean * 100

	// Calculate confidence based on model response and metric stability
	confidence := 0.85 // Base confidence

	// If the model predicts an issue (-1), adjust the prediction upward
	if len(resp.Predictions) > 0 && resp.Predictions[0] == -1 {
		// Issue predicted - increase expected resource usage
		cpuPercent = min(cpuPercent*1.15, 100.0) // 15% increase
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

// processPredictions is kept for backwards compatibility with tests
// Deprecated: Use processAnomalyPredictions or processForecastPredictions instead
func (h *PredictionHandler) processPredictions(resp *kserve.DetectResponse, cpuRollingMean, memoryRollingMean float64) (float64, float64, float64) {
	return h.processAnomalyPredictions(resp, cpuRollingMean, memoryRollingMean)
}

// buildRawMetricInstances builds the 5-feature instance for predictions (Issue #58)
// Features: [cpu_usage, memory_usage, disk_usage, network_in, network_out]
// This matches the predictive-analytics model's training data features.
func (h *PredictionHandler) buildRawMetricInstances(ctx context.Context, req *PredictRequest) ([][]float64, int) {
	cpuUsage := h.defaultCPURollingMean
	memoryUsage := h.defaultMemoryRollingMean
	diskUsage := h.defaultDiskUsage
	networkIn := h.defaultNetworkIn
	networkOut := h.defaultNetworkOut

	// Try to fetch real metrics from Prometheus if available
	if h.prometheusClient != nil && h.prometheusClient.IsAvailable() {
		var err error

		// Fetch CPU usage
		cpuUsage, err = h.prometheusClient.GetScopedCPURollingMean(ctx, req.Namespace, req.Deployment, req.Pod)
		if err != nil {
			h.log.WithError(err).Debug("Failed to get CPU usage, using default")
			cpuUsage = h.defaultCPURollingMean
		}

		// Fetch Memory usage
		memoryUsage, err = h.prometheusClient.GetScopedMemoryRollingMean(ctx, req.Namespace, req.Deployment, req.Pod)
		if err != nil {
			h.log.WithError(err).Debug("Failed to get memory usage, using default")
			memoryUsage = h.defaultMemoryRollingMean
		}

		// Fetch Disk usage
		diskUsage, err = h.prometheusClient.GetScopedDiskUsage(ctx, req.Namespace, req.Deployment, req.Pod)
		if err != nil {
			h.log.WithError(err).Debug("Failed to get disk usage, using default")
			diskUsage = h.defaultDiskUsage
		}

		// Fetch Network In
		networkIn, err = h.prometheusClient.GetScopedNetworkIn(ctx, req.Namespace, req.Deployment, req.Pod)
		if err != nil {
			h.log.WithError(err).Debug("Failed to get network in, using default")
			networkIn = h.defaultNetworkIn
		}

		// Fetch Network Out
		networkOut, err = h.prometheusClient.GetScopedNetworkOut(ctx, req.Namespace, req.Deployment, req.Pod)
		if err != nil {
			h.log.WithError(err).Debug("Failed to get network out, using default")
			networkOut = h.defaultNetworkOut
		}
	}

	h.log.WithFields(logrus.Fields{
		"cpu_usage":    cpuUsage,
		"memory_usage": memoryUsage,
		"disk_usage":   diskUsage,
		"network_in":   networkIn,
		"network_out":  networkOut,
		"namespace":    req.Namespace,
		"deployment":   req.Deployment,
		"pod":          req.Pod,
	}).Debug("Built raw metric instances for prediction")

	return [][]float64{{
		cpuUsage,
		memoryUsage,
		diskUsage,
		networkIn,
		networkOut,
	}}, 5
}

// IsFeatureEngineeringEnabled returns true if feature engineering is enabled
func (h *PredictionHandler) IsFeatureEngineeringEnabled() bool {
	return h.enableFeatureEngineering && h.featureBuilder != nil
}

// GetFeatureInfo returns information about the feature engineering configuration
// Returns nil if feature engineering is not enabled
func (h *PredictionHandler) GetFeatureInfo() *features.FeatureInfo {
	if h.featureBuilder == nil {
		return nil
	}
	info := h.featureBuilder.GetFeatureInfo()
	return &info
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
