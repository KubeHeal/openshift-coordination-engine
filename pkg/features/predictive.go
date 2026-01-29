// Package features provides feature engineering for ML models.
// This package implements feature engineering for the predictive-analytics model,
// following the pattern established in pkg/api/v1/anomaly.go for the anomaly-detector model.
package features

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/sirupsen/logrus"
)

// PredictiveFeatureConfig holds configuration for predictive feature engineering
type PredictiveFeatureConfig struct {
	// LookbackHours is the number of hours to look back for historical data
	LookbackHours int

	// Enabled enables feature engineering (if false, raw metrics are used)
	Enabled bool

	// ExpectedFeatureCount is the number of features the model expects.
	// If set (> 0), the builder will log a warning if the generated feature count doesn't match.
	// This helps detect feature engineering mismatches early.
	// Set this to match the model's StandardScaler expectation.
	ExpectedFeatureCount int
}

// DefaultPredictiveConfig returns default configuration for predictive feature engineering
func DefaultPredictiveConfig() PredictiveFeatureConfig {
	return PredictiveFeatureConfig{
		LookbackHours: 24,
		Enabled:       true,
	}
}

// MetricDataProvider is an interface for querying historical metric data
type MetricDataProvider interface {
	// QueryRange queries a PromQL expression over a time range and returns data points
	QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]DataPoint, error)

	// Query performs an instant query
	Query(ctx context.Context, query string) (float64, error)

	// IsAvailable returns true if the provider is configured and available
	IsAvailable() bool
}

// DataPoint represents a single metric observation at a point in time
type DataPoint struct {
	Timestamp time.Time
	Value     float64
}

// PredictiveFeatureBuilder builds feature vectors for the predictive-analytics model.
// It generates 3264 features matching the Python model's training expectations.
//
// Feature breakdown:
// - 5 base metrics × 25 features each × 24 lookback hours = 3000 features
// - Time-based features: 8 features × 24 hours = 192 features
// - Static time features: 8 features
// - Total: ~3200 features (exact count depends on configuration)
type PredictiveFeatureBuilder struct {
	provider MetricDataProvider
	config   PredictiveFeatureConfig
	log      *logrus.Logger
}

// NewPredictiveFeatureBuilder creates a new feature builder
func NewPredictiveFeatureBuilder(provider MetricDataProvider, config PredictiveFeatureConfig, log *logrus.Logger) *PredictiveFeatureBuilder {
	builder := &PredictiveFeatureBuilder{
		provider: provider,
		config:   config,
		log:      log,
	}

	// Validate expected feature count if specified
	if config.ExpectedFeatureCount > 0 {
		actualCount := builder.calculateTotalFeatures()
		if actualCount != config.ExpectedFeatureCount {
			log.WithFields(logrus.Fields{
				"expected_features":   config.ExpectedFeatureCount,
				"actual_features":     actualCount,
				"base_metrics":        len(predictiveBaseMetrics),
				"features_per_metric": FeaturesPerMetric,
				"lookback_hours":      config.LookbackHours,
				"time_features":       TimeFeatureCount,
			}).Warn("Feature count mismatch detected! The model may reject predictions. " +
				"Update the Go feature engineering to match the model's training or set ExpectedFeatureCount=0 to disable this warning.")
		}
	}

	return builder
}

// Base metrics used for predictive analytics
// Must match the training notebook's metric selection
var predictiveBaseMetrics = []string{
	"cpu_usage",
	"memory_usage",
	"disk_usage",
	"network_in",
	"network_out",
}

// Lag periods in hours - matches training notebook
var lagPeriods = []int{1, 2, 3, 6, 12, 24}

// Rolling window sizes in hours - matches training notebook
var rollingWindows = []int{3, 6, 12, 24}

// Feature names per metric (25 features each)
var predictiveFeatureNames = []string{
	"value",            // Current value
	"lag_1h",           // 1-hour lag
	"lag_2h",           // 2-hour lag
	"lag_3h",           // 3-hour lag
	"lag_6h",           // 6-hour lag
	"lag_12h",          // 12-hour lag
	"lag_24h",          // 24-hour lag
	"rolling_mean_3h",  // 3-hour rolling mean
	"rolling_mean_6h",  // 6-hour rolling mean
	"rolling_mean_12h", // 12-hour rolling mean
	"rolling_mean_24h", // 24-hour rolling mean
	"rolling_std_3h",   // 3-hour rolling std
	"rolling_std_6h",   // 6-hour rolling std
	"rolling_std_12h",  // 12-hour rolling std
	"rolling_std_24h",  // 24-hour rolling std
	"rolling_max_3h",   // 3-hour rolling max
	"rolling_max_6h",   // 6-hour rolling max
	"rolling_max_12h",  // 12-hour rolling max
	"rolling_max_24h",  // 24-hour rolling max
	"rolling_min_3h",   // 3-hour rolling min
	"rolling_min_6h",   // 6-hour rolling min
	"rolling_min_12h",  // 12-hour rolling min
	"rolling_min_24h",  // 24-hour rolling min
	"diff",             // value - lag_1h
	"pct_change",       // (value - lag_1h) / lag_1h
}

// Time-based feature names
var timeFeatureNames = []string{
	"hour_of_day",       // 0-23
	"day_of_week",       // 0-6 (Monday=0)
	"is_weekend",        // 0 or 1
	"month",             // 1-12
	"quarter",           // 1-4
	"day_of_month",      // 1-31
	"week_of_year",      // 1-53
	"is_business_hours", // 0 or 1 (9-17 weekdays)
}

// FeaturesPerMetric is the number of features generated per metric
const FeaturesPerMetric = 25

// TimeFeatureCount is the number of time-based features
const TimeFeatureCount = 8

// FeatureVector contains the engineered features for prediction
type FeatureVector struct {
	// Features is the flattened feature vector ready for ML model input
	Features []float64

	// FeatureCount is the total number of features
	FeatureCount int

	// MetricsData contains the raw current metric values (for debugging/logging)
	MetricsData map[string]float64

	// Timestamp when the features were generated
	Timestamp time.Time
}

// FeatureInfo contains metadata about the feature engineering
type FeatureInfo struct {
	TotalFeatures     int      `json:"total_features"`
	BaseMetrics       []string `json:"base_metrics"`
	FeaturesPerMetric int      `json:"features_per_metric"`
	LookbackHours     int      `json:"lookback_hours"`
	TimeFeatures      int      `json:"time_features"`
}

// GetFeatureInfo returns metadata about the feature engineering configuration
func (b *PredictiveFeatureBuilder) GetFeatureInfo() FeatureInfo {
	totalFeatures := len(predictiveBaseMetrics)*FeaturesPerMetric*b.config.LookbackHours + TimeFeatureCount*b.config.LookbackHours + TimeFeatureCount
	return FeatureInfo{
		TotalFeatures:     totalFeatures,
		BaseMetrics:       predictiveBaseMetrics,
		FeaturesPerMetric: FeaturesPerMetric,
		LookbackHours:     b.config.LookbackHours,
		TimeFeatures:      TimeFeatureCount,
	}
}

// BuildFeatures builds the complete feature vector for the predictive-analytics model.
// The feature vector is structured to match the training notebook's feature engineering.
//
// Parameters:
//   - ctx: Context for cancellation and timeouts
//   - namespace: Optional namespace filter for scoped predictions
//   - deployment: Optional deployment filter for scoped predictions
//   - pod: Optional pod filter for scoped predictions
//
// Returns the feature vector or an error if feature generation fails.
func (b *PredictiveFeatureBuilder) BuildFeatures(ctx context.Context, namespace, deployment, pod string) (*FeatureVector, error) {
	if b.provider == nil || !b.provider.IsAvailable() {
		return nil, fmt.Errorf("metric data provider not available")
	}

	now := time.Now()
	lookbackDuration := time.Duration(b.config.LookbackHours) * time.Hour
	startTime := now.Add(-lookbackDuration)

	b.log.WithFields(logrus.Fields{
		"lookback_hours": b.config.LookbackHours,
		"start_time":     startTime.Format(time.RFC3339),
		"end_time":       now.Format(time.RFC3339),
		"namespace":      namespace,
		"deployment":     deployment,
		"pod":            pod,
	}).Debug("Building predictive features")

	// Collect features for all metrics and time steps
	allFeatures := make([]float64, 0, b.calculateTotalFeatures())
	metricsData := make(map[string]float64)

	// For each hour in the lookback window
	for hourOffset := 0; hourOffset < b.config.LookbackHours; hourOffset++ {
		timestamp := now.Add(-time.Duration(hourOffset) * time.Hour)

		// Build metric features for this time step
		for _, metric := range predictiveBaseMetrics {
			metricFeatures, currentValue, err := b.buildMetricFeatures(ctx, metric, timestamp, namespace, deployment, pod)
			if err != nil {
				b.log.WithError(err).WithFields(logrus.Fields{
					"metric":      metric,
					"hour_offset": hourOffset,
				}).Debug("Failed to build metric features, using defaults")
				metricFeatures = b.getDefaultMetricFeatures()
				currentValue = 0.5
			}
			allFeatures = append(allFeatures, metricFeatures...)

			// Store current value for the most recent time step
			if hourOffset == 0 {
				metricsData[metric] = currentValue
			}
		}

		// Add time-based features for this time step
		timeFeatures := b.buildTimeFeatures(timestamp)
		allFeatures = append(allFeatures, timeFeatures...)
	}

	// Add static time features for the current time
	staticTimeFeatures := b.buildTimeFeatures(now)
	allFeatures = append(allFeatures, staticTimeFeatures...)

	b.log.WithFields(logrus.Fields{
		"feature_count":  len(allFeatures),
		"metrics_count":  len(predictiveBaseMetrics),
		"lookback_hours": b.config.LookbackHours,
	}).Debug("Predictive features built successfully")

	return &FeatureVector{
		Features:     allFeatures,
		FeatureCount: len(allFeatures),
		MetricsData:  metricsData,
		Timestamp:    now,
	}, nil
}

// calculateTotalFeatures calculates the expected total number of features
func (b *PredictiveFeatureBuilder) calculateTotalFeatures() int {
	// Metric features: 5 metrics × 25 features × 24 hours = 3000
	metricFeatures := len(predictiveBaseMetrics) * FeaturesPerMetric * b.config.LookbackHours
	// Time features per hour: 8 features × 24 hours = 192
	timeFeatures := TimeFeatureCount * b.config.LookbackHours
	// Static time features: 8
	staticFeatures := TimeFeatureCount

	return metricFeatures + timeFeatures + staticFeatures
}

// buildMetricFeatures builds the 25 features for a single metric at a specific time
func (b *PredictiveFeatureBuilder) buildMetricFeatures(
	ctx context.Context,
	metric string,
	timestamp time.Time,
	namespace, deployment, pod string,
) ([]float64, float64, error) {
	baseQuery := b.getMetricQuery(metric, namespace, deployment, pod)

	// Query current value
	currentValue, err := b.queryAtTime(ctx, baseQuery, timestamp)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query current value for %s: %w", metric, err)
	}

	features := make([]float64, 0, FeaturesPerMetric)

	// 1. Current value
	features = append(features, currentValue)

	// 2. Lag features (6 features)
	lagValues := make([]float64, len(lagPeriods))
	for i, lag := range lagPeriods {
		lagTime := timestamp.Add(-time.Duration(lag) * time.Hour)
		lagValue, err := b.queryAtTime(ctx, baseQuery, lagTime)
		if err != nil {
			lagValue = currentValue // Default to current value on error
		}
		lagValues[i] = lagValue
		features = append(features, lagValue)
	}

	// 3-6. Rolling statistics (16 features: 4 windows × 4 stats)
	for _, window := range rollingWindows {
		windowDuration := time.Duration(window) * time.Hour
		windowStart := timestamp.Add(-windowDuration)

		// Query range for this window
		dataPoints, err := b.queryRangeForStats(ctx, baseQuery, windowStart, timestamp)
		if err != nil || len(dataPoints) == 0 {
			// Default values when data is unavailable
			features = append(features, currentValue, 0.1, currentValue, currentValue)
			continue
		}

		// Calculate statistics
		mean, std, maxVal, minVal := calculateStats(dataPoints)
		features = append(features, mean, std, maxVal, minVal)
	}

	// 7. Diff feature (value - lag_1h)
	lag1h := lagValues[0] // First lag is 1 hour
	diff := currentValue - lag1h
	features = append(features, diff)

	// 8. Percent change feature
	pctChange := 0.0
	if lag1h != 0 {
		pctChange = (currentValue - lag1h) / lag1h
	}
	// Clamp extreme values
	pctChange = math.Max(-10.0, math.Min(10.0, pctChange))
	features = append(features, pctChange)

	return features, currentValue, nil
}

// buildTimeFeatures builds time-based features for a given timestamp
func (b *PredictiveFeatureBuilder) buildTimeFeatures(t time.Time) []float64 {
	hour := float64(t.Hour())
	dayOfWeek := float64((int(t.Weekday()) + 6) % 7) // Convert Sunday=0 to Monday=0
	isWeekend := 0.0
	if t.Weekday() == time.Saturday || t.Weekday() == time.Sunday {
		isWeekend = 1.0
	}
	month := float64(t.Month())
	quarter := float64((t.Month()-1)/3 + 1)
	dayOfMonth := float64(t.Day())
	_, weekOfYear := t.ISOWeek()
	isBusinessHours := 0.0
	if hour >= 9 && hour < 17 && isWeekend == 0 {
		isBusinessHours = 1.0
	}

	return []float64{
		hour,
		dayOfWeek,
		isWeekend,
		month,
		quarter,
		dayOfMonth,
		float64(weekOfYear),
		isBusinessHours,
	}
}

// getMetricQuery returns the Prometheus query for a metric with optional scope filters
func (b *PredictiveFeatureBuilder) getMetricQuery(metric, namespace, deployment, pod string) string {
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
		selectorStr = "," + joinSelectors(selectors)
	}

	// Define queries for each metric type
	queries := map[string]string{
		"cpu_usage": fmt.Sprintf(
			`avg(rate(container_cpu_usage_seconds_total{container!="",pod!=""%s}[5m]))`,
			selectorStr,
		),
		"memory_usage": fmt.Sprintf(
			`avg(container_memory_working_set_bytes{container!="",pod!=""%s}) / avg(kube_node_status_allocatable{resource="memory"})`,
			selectorStr,
		),
		"disk_usage": fmt.Sprintf(
			`1 - avg(node_filesystem_avail_bytes{mountpoint="/"%s}) / avg(node_filesystem_size_bytes{mountpoint="/"%s})`,
			selectorStr, selectorStr,
		),
		"network_in": fmt.Sprintf(
			`avg(rate(container_network_receive_bytes_total{interface!="lo"%s}[5m]))`,
			selectorStr,
		),
		"network_out": fmt.Sprintf(
			`avg(rate(container_network_transmit_bytes_total{interface!="lo"%s}[5m]))`,
			selectorStr,
		),
	}

	query, ok := queries[metric]
	if !ok {
		return metric // Return metric name as-is if not found
	}
	return query
}

// queryAtTime queries the metric value at a specific timestamp
func (b *PredictiveFeatureBuilder) queryAtTime(ctx context.Context, query string, timestamp time.Time) (float64, error) {
	// For historical queries, use query_range with a small window and take the last value
	start := timestamp.Add(-1 * time.Minute)
	end := timestamp

	dataPoints, err := b.provider.QueryRange(ctx, query, start, end, time.Minute)
	if err != nil {
		// Fall back to instant query if range query fails
		value, queryErr := b.provider.Query(ctx, query)
		if queryErr != nil {
			return 0, fmt.Errorf("failed to query metric at time %s: %w", timestamp.Format(time.RFC3339), queryErr)
		}
		return value, nil
	}

	if len(dataPoints) == 0 {
		value, queryErr := b.provider.Query(ctx, query)
		if queryErr != nil {
			return 0, fmt.Errorf("no data and instant query failed: %w", queryErr)
		}
		return value, nil
	}

	// Return the last data point
	return dataPoints[len(dataPoints)-1].Value, nil
}

// queryRangeForStats queries a range of data points for statistical calculations
func (b *PredictiveFeatureBuilder) queryRangeForStats(
	ctx context.Context,
	query string,
	start, end time.Time,
) ([]DataPoint, error) {
	// Use 5-minute steps for efficiency
	step := 5 * time.Minute
	dataPoints, err := b.provider.QueryRange(ctx, query, start, end, step)
	if err != nil {
		return nil, fmt.Errorf("failed to query range for stats: %w", err)
	}
	return dataPoints, nil
}

// getDefaultMetricFeatures returns default features for a single metric when data is unavailable
func (b *PredictiveFeatureBuilder) getDefaultMetricFeatures() []float64 {
	features := make([]float64, FeaturesPerMetric)

	// Default current value and lags (7 features)
	for i := 0; i < 7; i++ {
		features[i] = 0.5
	}

	// Default rolling statistics (16 features: 4 windows × 4 stats)
	idx := 7
	for range rollingWindows {
		features[idx] = 0.5   // mean
		features[idx+1] = 0.1 // std
		features[idx+2] = 0.6 // max
		features[idx+3] = 0.4 // min
		idx += 4
	}

	// Default diff and pct_change
	features[FeaturesPerMetric-2] = 0.0 // diff
	features[FeaturesPerMetric-1] = 0.0 // pct_change

	return features
}

// GetDefaultFeatures returns a complete default feature vector
func (b *PredictiveFeatureBuilder) GetDefaultFeatures() *FeatureVector {
	totalFeatures := b.calculateTotalFeatures()
	features := make([]float64, totalFeatures)

	idx := 0
	for hourOffset := 0; hourOffset < b.config.LookbackHours; hourOffset++ {
		timestamp := time.Now().Add(-time.Duration(hourOffset) * time.Hour)

		// Default metric features
		for range predictiveBaseMetrics {
			defaultMetricFeatures := b.getDefaultMetricFeatures()
			copy(features[idx:], defaultMetricFeatures)
			idx += len(defaultMetricFeatures)
		}

		// Time features
		timeFeatures := b.buildTimeFeatures(timestamp)
		copy(features[idx:], timeFeatures)
		idx += len(timeFeatures)
	}

	// Static time features
	staticTimeFeatures := b.buildTimeFeatures(time.Now())
	copy(features[idx:], staticTimeFeatures)

	return &FeatureVector{
		Features:     features,
		FeatureCount: len(features),
		MetricsData:  b.getDefaultMetricsData(),
		Timestamp:    time.Now(),
	}
}

// getDefaultMetricsData returns default raw metric values
func (b *PredictiveFeatureBuilder) getDefaultMetricsData() map[string]float64 {
	return map[string]float64{
		"cpu_usage":    0.5,
		"memory_usage": 0.5,
		"disk_usage":   0.5,
		"network_in":   0.5,
		"network_out":  0.5,
	}
}

// Helper functions

// calculateStats calculates mean, std, maxVal, minVal from data points
func calculateStats(points []DataPoint) (mean, std, maxVal, minVal float64) {
	if len(points) == 0 {
		return 0, 0, 0, 0
	}

	// Calculate mean
	sum := 0.0
	maxVal = points[0].Value
	minVal = points[0].Value

	for _, p := range points {
		sum += p.Value
		if p.Value > maxVal {
			maxVal = p.Value
		}
		if p.Value < minVal {
			minVal = p.Value
		}
	}
	mean = sum / float64(len(points))

	// Calculate standard deviation
	if len(points) > 1 {
		sumSquares := 0.0
		for _, p := range points {
			diff := p.Value - mean
			sumSquares += diff * diff
		}
		std = math.Sqrt(sumSquares / float64(len(points)))
	}

	return mean, std, maxVal, minVal
}

// joinSelectors joins label selectors with commas
func joinSelectors(selectors []string) string {
	if len(selectors) == 0 {
		return ""
	}
	result := selectors[0]
	for i := 1; i < len(selectors); i++ {
		result += "," + selectors[i]
	}
	return result
}

// GetPredictiveBaseMetrics returns the list of base metrics used for feature engineering
func GetPredictiveBaseMetrics() []string {
	result := make([]string, len(predictiveBaseMetrics))
	copy(result, predictiveBaseMetrics)
	return result
}

// GetPredictiveFeatureNames returns the list of feature names per metric
func GetPredictiveFeatureNames() []string {
	result := make([]string, len(predictiveFeatureNames))
	copy(result, predictiveFeatureNames)
	return result
}

// GetTimeFeatureNames returns the list of time-based feature names
func GetTimeFeatureNames() []string {
	result := make([]string, len(timeFeatureNames))
	copy(result, timeFeatureNames)
	return result
}
