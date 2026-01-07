// Package integrations provides clients for external service integration.
package integrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// KServeClient is a client for KServe InferenceServices (ADR-039)
// It implements the KServe v1 prediction protocol:
// https://kserve.github.io/website/latest/modelserving/data_plane/v1_protocol/
type KServeClient struct {
	anomalyDetectorURL     string
	predictiveAnalyticsURL string
	httpClient             *http.Client
	log                    *logrus.Logger
}

// KServeConfig holds configuration for the KServe client
type KServeClientConfig struct {
	AnomalyDetectorURL     string
	PredictiveAnalyticsURL string
	Timeout                time.Duration
}

// NewKServeClient creates a new KServe client with connection pooling
func NewKServeClient(cfg KServeClientConfig, log *logrus.Logger) *KServeClient {
	// Create HTTP client with connection pooling
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	return &KServeClient{
		anomalyDetectorURL:     cfg.AnomalyDetectorURL,
		predictiveAnalyticsURL: cfg.PredictiveAnalyticsURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		log: log,
	}
}

// KServeV1Request represents a KServe v1 prediction request
// See: https://kserve.github.io/website/latest/modelserving/data_plane/v1_protocol/
type KServeV1Request struct {
	// Instances is a list of input instances for prediction
	// Each instance is a list of feature values
	Instances [][]float64 `json:"instances"`
}

// KServeV1Response represents a KServe v1 prediction response
type KServeV1Response struct {
	// Predictions contains the model predictions
	// For anomaly detection: -1 = anomaly, 1 = normal
	Predictions []int `json:"predictions"`

	// ModelName is the name of the model that made the prediction
	ModelName string `json:"model_name,omitempty"`

	// ModelVersion is the version of the model
	ModelVersion string `json:"model_version,omitempty"`
}

// KServeModelMetadata represents model metadata from GET /v1/models/<model>
type KServeModelMetadata struct {
	Name     string   `json:"name"`
	Versions []string `json:"versions,omitempty"`
	Platform string   `json:"platform,omitempty"`
	Inputs   []struct {
		Name     string `json:"name"`
		Datatype string `json:"datatype"`
		Shape    []int  `json:"shape"`
	} `json:"inputs,omitempty"`
	Outputs []struct {
		Name     string `json:"name"`
		Datatype string `json:"datatype"`
		Shape    []int  `json:"shape"`
	} `json:"outputs,omitempty"`
}

// KServeModelList represents the response from GET /v1/models
type KServeModelList struct {
	Models []string `json:"models"`
}

// AnomalyPrediction represents the result of anomaly detection
type AnomalyPrediction struct {
	// IsAnomaly indicates if the instance is an anomaly
	IsAnomaly bool `json:"is_anomaly"`

	// Prediction is the raw prediction value (-1 for anomaly, 1 for normal)
	Prediction int `json:"prediction"`

	// ModelName is the name of the model that made the prediction
	ModelName string `json:"model_name,omitempty"`

	// ModelVersion is the version of the model
	ModelVersion string `json:"model_version,omitempty"`
}

// AnomalyDetectionResult represents the result of batch anomaly detection
type AnomalyDetectionResult struct {
	// Predictions contains individual predictions for each instance
	Predictions []AnomalyPrediction `json:"predictions"`

	// Summary contains aggregated statistics
	Summary struct {
		Total          int     `json:"total"`
		AnomaliesFound int     `json:"anomalies_found"`
		AnomalyRate    float64 `json:"anomaly_rate"`
	} `json:"summary"`

	// ModelInfo contains information about the model used
	ModelInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"model_info"`
}

// DetectAnomalies calls the anomaly detector KServe service
func (c *KServeClient) DetectAnomalies(ctx context.Context, instances [][]float64) (*AnomalyDetectionResult, error) {
	if c.anomalyDetectorURL == "" {
		return nil, fmt.Errorf("anomaly detector service not configured")
	}

	// Build KServe v1 request
	req := &KServeV1Request{
		Instances: instances,
	}

	// Call KServe predict endpoint
	endpoint := fmt.Sprintf("%s/v1/models/anomaly-detector:predict", c.anomalyDetectorURL)
	resp, err := c.predict(ctx, endpoint, req)
	if err != nil {
		return nil, fmt.Errorf("anomaly detection failed: %w", err)
	}

	// Convert KServe response to AnomalyDetectionResult
	result := &AnomalyDetectionResult{
		Predictions: make([]AnomalyPrediction, len(resp.Predictions)),
	}
	result.ModelInfo.Name = resp.ModelName
	result.ModelInfo.Version = resp.ModelVersion

	anomalyCount := 0
	for i, pred := range resp.Predictions {
		isAnomaly := pred == -1 // KServe/sklearn convention: -1 = anomaly
		if isAnomaly {
			anomalyCount++
		}
		result.Predictions[i] = AnomalyPrediction{
			IsAnomaly:    isAnomaly,
			Prediction:   pred,
			ModelName:    resp.ModelName,
			ModelVersion: resp.ModelVersion,
		}
	}

	result.Summary.Total = len(resp.Predictions)
	result.Summary.AnomaliesFound = anomalyCount
	if len(resp.Predictions) > 0 {
		result.Summary.AnomalyRate = float64(anomalyCount) / float64(len(resp.Predictions))
	}

	c.log.WithFields(logrus.Fields{
		"total":           result.Summary.Total,
		"anomalies_found": result.Summary.AnomaliesFound,
		"anomaly_rate":    result.Summary.AnomalyRate,
		"model":           resp.ModelName,
		"version":         resp.ModelVersion,
	}).Debug("KServe anomaly detection completed")

	return result, nil
}

// PredictiveAnalyticsResult represents the result of predictive analytics
type PredictiveAnalyticsResult struct {
	// Predictions contains the raw predictions
	Predictions []int `json:"predictions"`

	// RiskLevel indicates the overall risk level
	RiskLevel string `json:"risk_level"` // low, medium, high

	// ModelInfo contains information about the model used
	ModelInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"model_info"`
}

// PredictFutureIssues calls the predictive analytics KServe service
func (c *KServeClient) PredictFutureIssues(ctx context.Context, instances [][]float64) (*PredictiveAnalyticsResult, error) {
	if c.predictiveAnalyticsURL == "" {
		return nil, fmt.Errorf("predictive analytics service not configured")
	}

	// Build KServe v1 request
	req := &KServeV1Request{
		Instances: instances,
	}

	// Call KServe predict endpoint
	endpoint := fmt.Sprintf("%s/v1/models/predictive-analytics:predict", c.predictiveAnalyticsURL)
	resp, err := c.predict(ctx, endpoint, req)
	if err != nil {
		return nil, fmt.Errorf("predictive analytics failed: %w", err)
	}

	// Convert response
	result := &PredictiveAnalyticsResult{
		Predictions: resp.Predictions,
	}
	result.ModelInfo.Name = resp.ModelName
	result.ModelInfo.Version = resp.ModelVersion

	// Calculate risk level based on predictions
	result.RiskLevel = c.calculateRiskLevel(resp.Predictions)

	c.log.WithFields(logrus.Fields{
		"predictions": len(resp.Predictions),
		"risk_level":  result.RiskLevel,
		"model":       resp.ModelName,
		"version":     resp.ModelVersion,
	}).Debug("KServe predictive analytics completed")

	return result, nil
}

// calculateRiskLevel determines risk level from predictions
func (c *KServeClient) calculateRiskLevel(predictions []int) string {
	if len(predictions) == 0 {
		return "low"
	}

	// Count anomalies (-1 values)
	anomalyCount := 0
	for _, pred := range predictions {
		if pred == -1 {
			anomalyCount++
		}
	}

	anomalyRate := float64(anomalyCount) / float64(len(predictions))

	switch {
	case anomalyRate >= 0.5:
		return "high"
	case anomalyRate >= 0.2:
		return "medium"
	default:
		return "low"
	}
}

// GetModelMetadata retrieves metadata for a model
func (c *KServeClient) GetModelMetadata(ctx context.Context, baseURL, modelName string) (*KServeModelMetadata, error) {
	endpoint := fmt.Sprintf("%s/v1/models/%s", baseURL, modelName)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var metadata KServeModelMetadata
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &metadata, nil
}

// ListModels lists available models from a KServe service
func (c *KServeClient) ListModels(ctx context.Context, baseURL string) (*KServeModelList, error) {
	endpoint := fmt.Sprintf("%s/v1/models", baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var modelList KServeModelList
	if err := json.NewDecoder(resp.Body).Decode(&modelList); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &modelList, nil
}

// HealthCheck checks if the KServe services are healthy
func (c *KServeClient) HealthCheck(ctx context.Context) error {
	var errors []string

	// Check anomaly detector
	if c.anomalyDetectorURL != "" {
		if err := c.checkServiceHealth(ctx, c.anomalyDetectorURL, "anomaly-detector"); err != nil {
			errors = append(errors, fmt.Sprintf("anomaly-detector: %v", err))
		}
	}

	// Check predictive analytics
	if c.predictiveAnalyticsURL != "" {
		if err := c.checkServiceHealth(ctx, c.predictiveAnalyticsURL, "predictive-analytics"); err != nil {
			errors = append(errors, fmt.Sprintf("predictive-analytics: %v", err))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("KServe health check failed: %v", errors)
	}

	return nil
}

// checkServiceHealth checks if a specific KServe service is healthy
func (c *KServeClient) checkServiceHealth(ctx context.Context, baseURL, modelName string) error {
	endpoint := fmt.Sprintf("%s/v1/models/%s", baseURL, modelName)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: status %d", resp.StatusCode)
	}

	return nil
}

// predict performs a KServe v1 predict request
func (c *KServeClient) predict(ctx context.Context, endpoint string, req *KServeV1Request) (*KServeV1Response, error) {
	// Encode request body
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to encode request: %w", err)
	}

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
			"endpoint": endpoint,
			"duration": duration.Milliseconds(),
		}).WithError(err).Error("KServe request failed")
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	// Log request
	c.log.WithFields(logrus.Fields{
		"endpoint": endpoint,
		"status":   resp.StatusCode,
		"duration": duration.Milliseconds(),
	}).Debug("KServe request completed")

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, fmt.Errorf("unexpected status %d, failed to read body: %w", resp.StatusCode, readErr)
		}
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Decode response
	var kserveResp KServeV1Response
	if err := json.NewDecoder(resp.Body).Decode(&kserveResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &kserveResp, nil
}

// Close closes the HTTP client connections
func (c *KServeClient) Close() {
	c.httpClient.CloseIdleConnections()
}

// HasAnomalyDetector returns true if anomaly detector is configured
func (c *KServeClient) HasAnomalyDetector() bool {
	return c.anomalyDetectorURL != ""
}

// HasPredictiveAnalytics returns true if predictive analytics is configured
func (c *KServeClient) HasPredictiveAnalytics() bool {
	return c.predictiveAnalyticsURL != ""
}

