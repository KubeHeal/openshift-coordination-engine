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

// MLClient is a client for the Python ML/AI service
type MLClient struct {
	baseURL    string
	httpClient *http.Client
	log        *logrus.Logger
}

// NewMLClient creates a new ML service client with connection pooling
func NewMLClient(baseURL string, timeout time.Duration, log *logrus.Logger) *MLClient {
	// Create HTTP client with connection pooling
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 10,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
	}

	return &MLClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   timeout,
		},
		log: log,
	}
}

// AnomalyDetectionRequest represents a request to detect anomalies
type AnomalyDetectionRequest struct {
	Metrics   []MetricData `json:"metrics"`
	Threshold float64      `json:"threshold,omitempty"` // Optional, service has default
}

// MetricData represents a single metric data point
type MetricData struct {
	Name      string            `json:"name"`
	Value     float64           `json:"value"`
	Timestamp time.Time         `json:"timestamp"`
	Labels    map[string]string `json:"labels,omitempty"`
}

// AnomalyDetectionResponse represents the response from anomaly detection
type AnomalyDetectionResponse struct {
	Anomalies []Anomaly `json:"anomalies"`
	Summary   struct {
		Total          int     `json:"total"`
		AnomaliesFound int     `json:"anomalies_found"`
		HighSeverity   int     `json:"high_severity"`
		Confidence     float64 `json:"confidence"`
	} `json:"summary"`
}

// Anomaly represents a detected anomaly
type Anomaly struct {
	MetricName string    `json:"metric_name"`
	Value      float64   `json:"value"`
	Expected   float64   `json:"expected"`
	Deviation  float64   `json:"deviation"`
	Severity   string    `json:"severity"` // low, medium, high, critical
	Confidence float64   `json:"confidence"`
	Timestamp  time.Time `json:"timestamp"`
	Reason     string    `json:"reason,omitempty"`
}

// PredictionRequest represents a request to predict future issues
type PredictionRequest struct {
	HistoricalMetrics []MetricData `json:"historical_metrics"`
	PredictAhead      string       `json:"predict_ahead"` // Duration: "5m", "1h", "24h"
	IncludeConfidence bool         `json:"include_confidence,omitempty"`
}

// PredictionResponse represents the response from prediction
type PredictionResponse struct {
	Predictions []Prediction `json:"predictions"`
	Summary     struct {
		TimeRange         string  `json:"time_range"`
		MetricsCount      int     `json:"metrics_count"`
		OverallConfidence float64 `json:"overall_confidence"`
	} `json:"summary"`
}

// Prediction represents a predicted metric value
type Prediction struct {
	MetricName     string    `json:"metric_name"`
	PredictedValue float64   `json:"predicted_value"`
	Confidence     float64   `json:"confidence"`
	Timestamp      time.Time `json:"timestamp"`
	Trend          string    `json:"trend,omitempty"` // increasing, decreasing, stable
	Risk           string    `json:"risk,omitempty"`  // low, medium, high
}

// PatternAnalysisRequest represents a request to analyze historical patterns
type PatternAnalysisRequest struct {
	Metrics   []MetricData `json:"metrics"`
	TimeRange struct {
		Start time.Time `json:"start"`
		End   time.Time `json:"end"`
	} `json:"time_range"`
	AnalysisType string `json:"analysis_type,omitempty"` // trend, seasonal, correlation
}

// PatternAnalysisResponse represents the response from pattern analysis
type PatternAnalysisResponse struct {
	Patterns []Pattern `json:"patterns"`
	Insights []string  `json:"insights"`
	Summary  struct {
		PatternsFound int     `json:"patterns_found"`
		Confidence    float64 `json:"confidence"`
	} `json:"summary"`
}

// Pattern represents a detected pattern
type Pattern struct {
	Type        string    `json:"type"` // trend, seasonal, spike, correlation
	Description string    `json:"description"`
	Metrics     []string  `json:"metrics"`
	Confidence  float64   `json:"confidence"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
}

// DetectAnomalies calls the ML service to detect anomalies in metrics
func (c *MLClient) DetectAnomalies(ctx context.Context, req *AnomalyDetectionRequest) (*AnomalyDetectionResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/anomaly/detect", c.baseURL)

	var resp AnomalyDetectionResponse
	if err := c.doRequest(ctx, "POST", endpoint, req, &resp); err != nil {
		return nil, fmt.Errorf("anomaly detection failed: %w", err)
	}

	c.log.WithFields(logrus.Fields{
		"total_metrics":   req.Metrics,
		"anomalies_found": resp.Summary.AnomaliesFound,
		"high_severity":   resp.Summary.HighSeverity,
		"confidence":      resp.Summary.Confidence,
	}).Debug("Anomaly detection completed")

	return &resp, nil
}

// Predict calls the ML service to predict future metric values
func (c *MLClient) Predict(ctx context.Context, req *PredictionRequest) (*PredictionResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/prediction/predict", c.baseURL)

	var resp PredictionResponse
	if err := c.doRequest(ctx, "POST", endpoint, req, &resp); err != nil {
		return nil, fmt.Errorf("prediction failed: %w", err)
	}

	c.log.WithFields(logrus.Fields{
		"historical_count": len(req.HistoricalMetrics),
		"predict_ahead":    req.PredictAhead,
		"predictions":      len(resp.Predictions),
		"confidence":       resp.Summary.OverallConfidence,
	}).Debug("Prediction completed")

	return &resp, nil
}

// AnalyzePatterns calls the ML service to analyze historical patterns
func (c *MLClient) AnalyzePatterns(ctx context.Context, req *PatternAnalysisRequest) (*PatternAnalysisResponse, error) {
	endpoint := fmt.Sprintf("%s/api/v1/pattern/analyze", c.baseURL)

	var resp PatternAnalysisResponse
	if err := c.doRequest(ctx, "POST", endpoint, req, &resp); err != nil {
		return nil, fmt.Errorf("pattern analysis failed: %w", err)
	}

	c.log.WithFields(logrus.Fields{
		"metrics_count":  len(req.Metrics),
		"patterns_found": resp.Summary.PatternsFound,
		"insights_count": len(resp.Insights),
		"confidence":     resp.Summary.Confidence,
	}).Debug("Pattern analysis completed")

	return &resp, nil
}

// HealthCheck checks if the ML service is healthy
func (c *MLClient) HealthCheck(ctx context.Context) error {
	endpoint := fmt.Sprintf("%s/health", c.baseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ML service unhealthy: status %d", resp.StatusCode)
	}

	return nil
}

// doRequest performs an HTTP request with JSON encoding/decoding
func (c *MLClient) doRequest(ctx context.Context, method, url string, reqBody, respBody interface{}) error {
	// Encode request body
	var body io.Reader
	if reqBody != nil {
		jsonData, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("failed to encode request: %w", err)
		}
		body = bytes.NewBuffer(jsonData)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Execute request
	startTime := time.Now()
	resp, err := c.httpClient.Do(req)
	duration := time.Since(startTime)

	if err != nil {
		c.log.WithFields(logrus.Fields{
			"method":   method,
			"url":      url,
			"duration": duration.Milliseconds(),
		}).WithError(err).Error("ML service request failed")
		return fmt.Errorf("request failed: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			c.log.WithError(closeErr).Warn("Failed to close response body")
		}
	}()

	// Log request
	c.log.WithFields(logrus.Fields{
		"method":   method,
		"url":      url,
		"status":   resp.StatusCode,
		"duration": duration.Milliseconds(),
	}).Debug("ML service request completed")

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("unexpected status %d, failed to read body: %w", resp.StatusCode, readErr)
		}
		return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// Decode response
	if respBody != nil {
		if err := json.NewDecoder(resp.Body).Decode(respBody); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// Close closes the HTTP client connections
func (c *MLClient) Close() {
	c.httpClient.CloseIdleConnections()
}
