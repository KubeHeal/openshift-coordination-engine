// Package kserve provides a proxy client for KServe InferenceServices.
// It implements dynamic model discovery from environment variables per ADR-039 and ADR-040.
package kserve

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ProxyClient is a client for proxying requests to KServe InferenceServices.
// It supports dynamic model discovery from environment variables.
type ProxyClient struct {
	namespace     string
	predictorPort int
	models        map[string]*ModelInfo
	httpClient    *http.Client
	log           *logrus.Logger
	modelsMutex   sync.RWMutex
}

// ModelInfo contains information about a registered KServe model
type ModelInfo struct {
	// Name is the user-friendly model name (e.g., "anomaly-detector")
	Name string `json:"name"`

	// ServiceName is the KServe InferenceService name (e.g., "anomaly-detector-predictor")
	ServiceName string `json:"service_name"`

	// Namespace is the Kubernetes namespace where the model is deployed
	Namespace string `json:"namespace"`

	// URL is the full service URL for the KServe InferenceService
	URL string `json:"url"`
}

// ProxyConfig holds configuration for the KServe proxy client
type ProxyConfig struct {
	// Namespace is the default namespace for KServe InferenceServices
	Namespace string

	// PredictorPort is the port where KServe InferenceService predictors listen
	// In RawDeployment mode, predictors listen on 8080, not the default HTTP port 80
	PredictorPort int

	// Timeout for HTTP requests to KServe services
	Timeout time.Duration
}

// DefaultPredictorPort is the default port for KServe predictors in RawDeployment mode
const DefaultPredictorPort = 8080

// DetectRequest represents a request to call a KServe model for predictions
type DetectRequest struct {
	// Model is the name of the model to call (e.g., "anomaly-detector")
	Model string `json:"model"`

	// Instances is a list of input instances for prediction
	// Each instance is a list of feature values
	Instances [][]float64 `json:"instances"`
}

// DetectResponse represents the response from a KServe model prediction (anomaly-detector)
type DetectResponse struct {
	// Predictions contains the model predictions (for anomaly-detector: []int)
	Predictions []int `json:"predictions"`

	// ModelName is the name of the model that made the prediction
	ModelName string `json:"model_name"`

	// ModelVersion is the version of the model
	ModelVersion string `json:"model_version,omitempty"`
}

// ForecastResult contains the forecast data for a single metric
type ForecastResult struct {
	// Forecast contains the predicted values
	Forecast []float64 `json:"forecast"`

	// ForecastHorizon is the number of time periods forecasted
	ForecastHorizon int `json:"forecast_horizon"`

	// Confidence contains the confidence scores for each forecast value
	Confidence []float64 `json:"confidence"`
}

// ForecastResponse represents the response from the predictive-analytics KServe model
type ForecastResponse struct {
	// Predictions contains forecasts per metric (cpu_usage, memory_usage)
	Predictions map[string]ForecastResult `json:"predictions"`

	// ModelName is the name of the model that made the prediction
	ModelName string `json:"model_name"`

	// ModelVersion is the version of the model
	ModelVersion string `json:"model_version,omitempty"`

	// Timestamp when the prediction was generated
	Timestamp string `json:"timestamp,omitempty"`

	// LookbackWindow is the number of hours of historical data used
	LookbackWindow int `json:"lookback_window,omitempty"`
}

// ModelResponse is a flexible response type that can hold either DetectResponse or ForecastResponse
type ModelResponse struct {
	// Type indicates the response type: "anomaly" or "forecast"
	Type string

	// AnomalyResponse holds anomaly detection results (for anomaly-detector model)
	AnomalyResponse *DetectResponse

	// ForecastResponse holds forecast results (for predictive-analytics model)
	ForecastResponse *ForecastResponse
}

// ModelHealthResponse represents the health status of a KServe model
type ModelHealthResponse struct {
	// Model is the name of the model
	Model string `json:"model"`

	// Status is the health status (ready, unavailable, unknown)
	Status string `json:"status"`

	// Service is the KServe InferenceService name
	Service string `json:"service"`

	// Namespace is where the model is deployed
	Namespace string `json:"namespace"`

	// Message contains additional information
	Message string `json:"message,omitempty"`
}

// NewProxyClient creates a new KServe proxy client with dynamic model discovery
func NewProxyClient(cfg ProxyConfig, log *logrus.Logger) (*ProxyClient, error) {
	if cfg.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	predictorPort := cfg.PredictorPort
	if predictorPort == 0 {
		predictorPort = DefaultPredictorPort
	}

	// Create HTTP client with connection pooling
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
	}

	client := &ProxyClient{
		namespace:     cfg.Namespace,
		predictorPort: predictorPort,
		models:        make(map[string]*ModelInfo),
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		log: log,
	}

	// Load models from environment variables
	client.loadModelsFromEnv()

	if len(client.models) == 0 {
		log.Warn("No KServe models discovered from environment variables")
	} else {
		log.WithField("models", client.ListModels()).Info("KServe models loaded from environment")
	}

	return client, nil
}

// loadModelsFromEnv discovers models from environment variables.
// Pattern: KSERVE_<MODEL_NAME>_SERVICE = service-name
// Example: KSERVE_ANOMALY_DETECTOR_SERVICE = anomaly-detector-predictor
func (c *ProxyClient) loadModelsFromEnv() {
	c.modelsMutex.Lock()
	defer c.modelsMutex.Unlock()

	for _, env := range os.Environ() {
		// Skip non-KServe environment variables
		if !strings.HasPrefix(env, "KSERVE_") {
			continue
		}

		// Skip KSERVE_NAMESPACE, KSERVE_TIMEOUT, and KSERVE_PREDICTOR_PORT (configuration variables)
		if strings.HasPrefix(env, "KSERVE_NAMESPACE") ||
			strings.HasPrefix(env, "KSERVE_TIMEOUT") ||
			strings.HasPrefix(env, "KSERVE_PREDICTOR_PORT") {
			continue
		}

		// Check for _SERVICE suffix
		if !strings.Contains(env, "_SERVICE=") {
			continue
		}

		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}

		envKey := parts[0]
		serviceName := parts[1]

		// Convert KSERVE_ANOMALY_DETECTOR_SERVICE → anomaly-detector
		modelName := strings.TrimPrefix(envKey, "KSERVE_")
		modelName = strings.TrimSuffix(modelName, "_SERVICE")
		modelName = strings.ToLower(strings.ReplaceAll(modelName, "_", "-"))

		// Build service URL with the predictor port
		url := fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", serviceName, c.namespace, c.predictorPort)

		c.models[modelName] = &ModelInfo{
			Name:        modelName,
			ServiceName: serviceName,
			Namespace:   c.namespace,
			URL:         url,
		}

		c.log.WithFields(logrus.Fields{
			"model":   modelName,
			"service": serviceName,
			"url":     url,
			"port":    c.predictorPort,
		}).Debug("Registered KServe model from environment")
	}
}

// ListModels returns a list of registered model names
func (c *ProxyClient) ListModels() []string {
	c.modelsMutex.RLock()
	defer c.modelsMutex.RUnlock()

	models := make([]string, 0, len(c.models))
	for name := range c.models {
		models = append(models, name)
	}
	return models
}

// GetModel returns information about a specific model
func (c *ProxyClient) GetModel(name string) (*ModelInfo, bool) {
	c.modelsMutex.RLock()
	defer c.modelsMutex.RUnlock()

	model, exists := c.models[name]
	return model, exists
}

// GetAllModels returns information about all registered models
func (c *ProxyClient) GetAllModels() []*ModelInfo {
	c.modelsMutex.RLock()
	defer c.modelsMutex.RUnlock()

	models := make([]*ModelInfo, 0, len(c.models))
	for _, model := range c.models {
		models = append(models, model)
	}
	return models
}

// ModelCount returns the number of registered models
func (c *ProxyClient) ModelCount() int {
	c.modelsMutex.RLock()
	defer c.modelsMutex.RUnlock()
	return len(c.models)
}

// Predict calls a KServe model for predictions
func (c *ProxyClient) Predict(ctx context.Context, modelName string, instances [][]float64) (*DetectResponse, error) {
	model, exists := c.GetModel(modelName)
	if !exists {
		return nil, &ModelNotFoundError{ModelName: modelName}
	}

	// Build KServe v1 request
	kserveReq := map[string]interface{}{
		"instances": instances,
	}

	jsonData, err := json.Marshal(kserveReq)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	// Build endpoint URL - KServe v1 protocol: /v1/models/<model>:predict
	// Note: KServe defaults to model name "model" when spec.predictor.model.name is not set
	// We use the hardcoded "model" name for KServe API paths, while keeping the logical
	// model name (e.g., "anomaly-detector") for user-facing APIs and service resolution
	endpoint := fmt.Sprintf("%s/v1/models/model:predict", model.URL)

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Execute request
	startTime := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	duration := time.Since(startTime)

	if err != nil {
		c.log.WithFields(logrus.Fields{
			"model":    modelName,
			"endpoint": endpoint,
			"duration": duration.Milliseconds(),
		}).WithError(err).Error("KServe predict request failed")
		return nil, &ModelUnavailableError{ModelName: modelName, Cause: err}
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	// Log request
	c.log.WithFields(logrus.Fields{
		"model":    modelName,
		"endpoint": endpoint,
		"status":   resp.StatusCode,
		"duration": duration.Milliseconds(),
	}).Debug("KServe predict request completed")

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("model %s returned status %d, failed to read body: %w", modelName, resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("model %s returned status %d: %s", modelName, resp.StatusCode, string(bodyBytes))
	}

	// Decode response - KServe v1 response format
	var kserveResp struct {
		Predictions  []int  `json:"predictions"`
		ModelName    string `json:"model_name,omitempty"`
		ModelVersion string `json:"model_version,omitempty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&kserveResp); err != nil {
		return nil, fmt.Errorf("failed to decode response from model %s: %w", modelName, err)
	}

	return &DetectResponse{
		Predictions:  kserveResp.Predictions,
		ModelName:    modelName,
		ModelVersion: kserveResp.ModelVersion,
	}, nil
}

// PredictFlexible calls a KServe model and returns a flexible response that handles
// different model response formats (anomaly-detector vs predictive-analytics).
// This method uses a type switch based on the model name to properly parse the response.
func (c *ProxyClient) PredictFlexible(ctx context.Context, modelName string, instances [][]float64) (*ModelResponse, error) {
	model, exists := c.GetModel(modelName)
	if !exists {
		return nil, &ModelNotFoundError{ModelName: modelName}
	}

	// Build KServe v1 request
	kserveReq := map[string]interface{}{
		"instances": instances,
	}

	jsonData, err := json.Marshal(kserveReq)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

	// Build endpoint URL - KServe v1 protocol: /v1/models/<model>:predict
	endpoint := fmt.Sprintf("%s/v1/models/model:predict", model.URL)

	// Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Execute request
	startTime := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	duration := time.Since(startTime)

	if err != nil {
		c.log.WithFields(logrus.Fields{
			"model":    modelName,
			"endpoint": endpoint,
			"duration": duration.Milliseconds(),
		}).WithError(err).Error("KServe predict request failed")
		return nil, &ModelUnavailableError{ModelName: modelName, Cause: err}
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	// Log request
	c.log.WithFields(logrus.Fields{
		"model":    modelName,
		"endpoint": endpoint,
		"status":   resp.StatusCode,
		"duration": duration.Milliseconds(),
	}).Debug("KServe predict request completed")

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("model %s returned status %d, failed to read body: %w", modelName, resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("model %s returned status %d: %s", modelName, resp.StatusCode, string(bodyBytes))
	}

	// Read the response body for flexible parsing
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body from model %s: %w", modelName, err)
	}

	// Parse response based on model type
	return c.parseModelResponse(modelName, bodyBytes)
}

// parseModelResponse parses the response body based on the model type
func (c *ProxyClient) parseModelResponse(modelName string, body []byte) (*ModelResponse, error) {
	switch modelName {
	case "predictive-analytics":
		return c.parseForecastResponse(modelName, body)
	case "anomaly-detector":
		return c.parseAnomalyResponse(modelName, body)
	default:
		// Try to detect the response type by attempting to parse both formats
		return c.parseAutoDetectResponse(modelName, body)
	}
}

// parseForecastResponse parses predictive-analytics model responses.
// Supports two formats for flexibility with different model architectures:
//
// Format 1 - Nested (custom wrapper output):
//
//	{"predictions": {"cpu_usage": {"forecast": [...], ...}, "memory_usage": {...}}}
//
// Format 2 - Array (standard sklearn multi-output):
//
//	{"predictions": [[cpu_value, memory_value], ...]}
func (c *ProxyClient) parseForecastResponse(modelName string, body []byte) (*ModelResponse, error) {
	// Try Format 1: Nested structure (custom wrapper or rich model output)
	var nestedResp struct {
		Predictions    map[string]ForecastResult `json:"predictions"`
		ModelName      string                    `json:"model_name,omitempty"`
		ModelVersion   string                    `json:"model_version,omitempty"`
		Timestamp      string                    `json:"timestamp,omitempty"`
		LookbackWindow int                       `json:"lookback_window,omitempty"`
	}

	if err := json.Unmarshal(body, &nestedResp); err == nil &&
		nestedResp.Predictions != nil && len(nestedResp.Predictions) > 0 {
		c.log.WithFields(logrus.Fields{
			"model":  modelName,
			"format": "nested",
		}).Debug("Parsed forecast response in nested format")
		return &ModelResponse{
			Type: "forecast",
			ForecastResponse: &ForecastResponse{
				Predictions:    nestedResp.Predictions,
				ModelName:      modelName,
				ModelVersion:   nestedResp.ModelVersion,
				Timestamp:      nestedResp.Timestamp,
				LookbackWindow: nestedResp.LookbackWindow,
			},
		}, nil
	}

	// Fallback to Format 2: Array structure (sklearn multi-output)
	var arrayResp struct {
		Predictions  [][]float64 `json:"predictions"`
		ModelName    string      `json:"model_name,omitempty"`
		ModelVersion string      `json:"model_version,omitempty"`
	}

	if err := json.Unmarshal(body, &arrayResp); err != nil {
		return nil, fmt.Errorf("failed to parse forecast response from model %s: %w", modelName, err)
	}

	// Convert array format to nested format
	// Convention: [0] = CPU, [1] = Memory (per model metadata)
	predictions := make(map[string]ForecastResult)

	if len(arrayResp.Predictions) > 0 && len(arrayResp.Predictions[0]) >= 2 {
		cpuForecasts := make([]float64, len(arrayResp.Predictions))
		memForecasts := make([]float64, len(arrayResp.Predictions))

		for i, pred := range arrayResp.Predictions {
			cpuForecasts[i] = pred[0]
			memForecasts[i] = pred[1]
		}

		predictions["cpu_usage"] = ForecastResult{
			Forecast:        cpuForecasts,
			ForecastHorizon: len(cpuForecasts),
			Confidence:      []float64{0.85}, // Default confidence for sklearn models
		}
		predictions["memory_usage"] = ForecastResult{
			Forecast:        memForecasts,
			ForecastHorizon: len(memForecasts),
			Confidence:      []float64{0.85},
		}

		c.log.WithFields(logrus.Fields{
			"model":       modelName,
			"format":      "array_converted",
			"num_samples": len(arrayResp.Predictions),
		}).Debug("Converted array forecast to nested format")
	} else if len(arrayResp.Predictions) > 0 && len(arrayResp.Predictions[0]) == 1 {
		// Handle single-output models (just CPU or a single metric)
		forecasts := make([]float64, len(arrayResp.Predictions))
		for i, pred := range arrayResp.Predictions {
			forecasts[i] = pred[0]
		}
		predictions["forecast"] = ForecastResult{
			Forecast:        forecasts,
			ForecastHorizon: len(forecasts),
			Confidence:      []float64{0.85},
		}

		c.log.WithFields(logrus.Fields{
			"model":       modelName,
			"format":      "array_single_converted",
			"num_samples": len(arrayResp.Predictions),
		}).Debug("Converted single-output array forecast to nested format")
	}

	return &ModelResponse{
		Type: "forecast",
		ForecastResponse: &ForecastResponse{
			Predictions:  predictions,
			ModelName:    modelName,
			ModelVersion: arrayResp.ModelVersion,
		},
	}, nil
}

// parseAnomalyResponse parses an anomaly-detector model response
func (c *ProxyClient) parseAnomalyResponse(modelName string, body []byte) (*ModelResponse, error) {
	var anomalyResp struct {
		Predictions  []int  `json:"predictions"`
		ModelName    string `json:"model_name,omitempty"`
		ModelVersion string `json:"model_version,omitempty"`
	}

	if err := json.Unmarshal(body, &anomalyResp); err != nil {
		return nil, fmt.Errorf("failed to decode anomaly response from model %s: %w", modelName, err)
	}

	return &ModelResponse{
		Type: "anomaly",
		AnomalyResponse: &DetectResponse{
			Predictions:  anomalyResp.Predictions,
			ModelName:    modelName,
			ModelVersion: anomalyResp.ModelVersion,
		},
	}, nil
}

// parseAutoDetectResponse tries to detect and parse the response format automatically
func (c *ProxyClient) parseAutoDetectResponse(modelName string, body []byte) (*ModelResponse, error) {
	// First, try to unmarshal into a generic map to inspect the structure
	var rawResp map[string]interface{}
	if err := json.Unmarshal(body, &rawResp); err != nil {
		return nil, fmt.Errorf("failed to decode response from model %s: %w", modelName, err)
	}

	predictions, exists := rawResp["predictions"]
	if !exists {
		return nil, fmt.Errorf("response from model %s missing 'predictions' field", modelName)
	}

	// Check if predictions is an array or object
	switch pred := predictions.(type) {
	case []interface{}:
		// Could be anomaly-detector (array of ints) or sklearn forecast (array of arrays)
		if len(pred) > 0 {
			// Check if it's an array of arrays (sklearn multi-output forecast)
			if _, isArray := pred[0].([]interface{}); isArray {
				// Array of arrays format: [[cpu, mem], ...] -> forecast
				return c.parseForecastResponse(modelName, body)
			}
		}
		// Simple array format: [0, 1, 0, ...] -> anomaly-detector
		return c.parseAnomalyResponse(modelName, body)
	case map[string]interface{}:
		// Predictive-analytics format: predictions is a nested object
		return c.parseForecastResponse(modelName, body)
	default:
		return nil, fmt.Errorf("unsupported predictions format from model %s", modelName)
	}
}

// PredictForecast is a convenience method that calls PredictFlexible and returns only forecast responses.
// Returns an error if the model does not return a forecast response.
func (c *ProxyClient) PredictForecast(ctx context.Context, modelName string, instances [][]float64) (*ForecastResponse, error) {
	resp, err := c.PredictFlexible(ctx, modelName, instances)
	if err != nil {
		return nil, err
	}

	if resp.Type != "forecast" || resp.ForecastResponse == nil {
		return nil, fmt.Errorf("model %s did not return a forecast response (got type: %s)", modelName, resp.Type)
	}

	return resp.ForecastResponse, nil
}

// CheckModelHealth checks if a specific KServe model is healthy
func (c *ProxyClient) CheckModelHealth(ctx context.Context, modelName string) (*ModelHealthResponse, error) {
	model, exists := c.GetModel(modelName)
	if !exists {
		return &ModelHealthResponse{
			Model:     modelName,
			Status:    "unknown",
			Message:   "Model not registered",
			Namespace: c.namespace,
		}, &ModelNotFoundError{ModelName: modelName}
	}

	// KServe v1 health endpoint: GET /v1/models/<model>
	// Note: KServe defaults to model name "model" when spec.predictor.model.name is not set
	endpoint := fmt.Sprintf("%s/v1/models/model", model.URL)

	httpReq, err := http.NewRequestWithContext(ctx, "GET", endpoint, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return &ModelHealthResponse{
			Model:     modelName,
			Status:    "unavailable",
			Service:   model.ServiceName,
			Namespace: model.Namespace,
			Message:   fmt.Sprintf("Connection failed: %v", err),
		}, nil
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close health check response body")
		}
	}()

	if resp.StatusCode == http.StatusOK {
		return &ModelHealthResponse{
			Model:     modelName,
			Status:    "ready",
			Service:   model.ServiceName,
			Namespace: model.Namespace,
		}, nil
	}

	return &ModelHealthResponse{
		Model:     modelName,
		Status:    "unavailable",
		Service:   model.ServiceName,
		Namespace: model.Namespace,
		Message:   fmt.Sprintf("Health check returned status %d", resp.StatusCode),
	}, nil
}

// HealthCheck checks all registered models and returns overall health
func (c *ProxyClient) HealthCheck(ctx context.Context) error {
	models := c.ListModels()
	if len(models) == 0 {
		return fmt.Errorf("no models registered")
	}

	var unhealthyModels []string
	for _, modelName := range models {
		health, err := c.CheckModelHealth(ctx, modelName)
		if err != nil || health.Status != "ready" {
			unhealthyModels = append(unhealthyModels, modelName)
		}
	}

	if len(unhealthyModels) > 0 {
		return fmt.Errorf("unhealthy models: %v", unhealthyModels)
	}

	return nil
}

// Close closes the HTTP client connections
func (c *ProxyClient) Close() {
	c.httpClient.CloseIdleConnections()
}

// RefreshModels reloads models from environment variables
func (c *ProxyClient) RefreshModels() {
	c.modelsMutex.Lock()
	// Clear existing models
	c.models = make(map[string]*ModelInfo)
	c.modelsMutex.Unlock()

	// Reload from environment
	c.loadModelsFromEnv()

	c.log.WithField("models", c.ListModels()).Info("KServe models refreshed from environment")
}

// ModelNotFoundError is returned when a model is not registered
type ModelNotFoundError struct {
	ModelName string
}

func (e *ModelNotFoundError) Error() string {
	return fmt.Sprintf("model not found: %s", e.ModelName)
}

// ModelUnavailableError is returned when a model is unavailable
type ModelUnavailableError struct {
	ModelName string
	Cause     error
}

func (e *ModelUnavailableError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("model unavailable: %s: %v", e.ModelName, e.Cause)
	}
	return fmt.Sprintf("model unavailable: %s", e.ModelName)
}

func (e *ModelUnavailableError) Unwrap() error {
	return e.Cause
}
