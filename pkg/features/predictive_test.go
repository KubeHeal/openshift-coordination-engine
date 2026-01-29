// Package features provides feature engineering for ML models.
package features

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockMetricDataProvider implements MetricDataProvider for testing
type MockMetricDataProvider struct {
	// QueryRangeFunc allows customizing QueryRange behavior in tests
	QueryRangeFunc func(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]DataPoint, error)

	// QueryFunc allows customizing Query behavior in tests
	QueryFunc func(ctx context.Context, query string) (float64, error)

	// IsAvailableResult controls the return value of IsAvailable
	IsAvailableResult bool
}

func (m *MockMetricDataProvider) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]DataPoint, error) {
	if m.QueryRangeFunc != nil {
		return m.QueryRangeFunc(ctx, query, start, end, step)
	}
	// Default: return some sample data points
	now := time.Now()
	points := make([]DataPoint, 0)
	for i := 0; i < 12; i++ {
		points = append(points, DataPoint{
			Timestamp: now.Add(-time.Duration(i) * 5 * time.Minute),
			Value:     0.5 + float64(i)*0.01,
		})
	}
	return points, nil
}

func (m *MockMetricDataProvider) Query(ctx context.Context, query string) (float64, error) {
	if m.QueryFunc != nil {
		return m.QueryFunc(ctx, query)
	}
	// Default: return a sample value
	return 0.65, nil
}

func (m *MockMetricDataProvider) IsAvailable() bool {
	return m.IsAvailableResult
}

func TestNewPredictiveFeatureBuilder(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	provider := &MockMetricDataProvider{IsAvailableResult: true}
	config := DefaultPredictiveConfig()

	builder := NewPredictiveFeatureBuilder(provider, config, log)

	assert.NotNil(t, builder)
}

func TestDefaultPredictiveConfig(t *testing.T) {
	config := DefaultPredictiveConfig()

	assert.Equal(t, 24, config.LookbackHours)
	assert.True(t, config.Enabled)
}

func TestGetFeatureInfo(t *testing.T) {
	log := logrus.New()
	provider := &MockMetricDataProvider{IsAvailableResult: true}
	config := DefaultPredictiveConfig()
	builder := NewPredictiveFeatureBuilder(provider, config, log)

	info := builder.GetFeatureInfo()

	assert.Equal(t, 5, len(info.BaseMetrics))
	assert.Equal(t, FeaturesPerMetric, info.FeaturesPerMetric)
	assert.Equal(t, 24, info.LookbackHours)
	assert.Equal(t, TimeFeatureCount, info.TimeFeatures)
	assert.Equal(t, 6, info.TimeFeatures) // Verify TimeFeatureCount is 6
	// Total features should be exactly 3264 (matching Python model)
	assert.Equal(t, 3264, info.TotalFeatures)
}

func TestBuildTimeFeatures(t *testing.T) {
	log := logrus.New()
	provider := &MockMetricDataProvider{IsAvailableResult: true}
	config := DefaultPredictiveConfig()
	builder := NewPredictiveFeatureBuilder(provider, config, log)

	// Test a specific timestamp: Wednesday, 2:30 PM
	testTime := time.Date(2026, 1, 28, 14, 30, 0, 0, time.UTC)
	features := builder.buildTimeFeatures(testTime)

	assert.Len(t, features, TimeFeatureCount) // Should be 6 features

	// Verify specific features in Python notebook order:
	// hour, day_of_week, day_of_month, month, is_weekend, is_business_hours
	assert.Equal(t, 14.0, features[0]) // hour (0-23)
	assert.Equal(t, 2.0, features[1])  // day_of_week (Wednesday = 2, Monday = 0)
	assert.Equal(t, 28.0, features[2]) // day_of_month (1-31)
	assert.Equal(t, 1.0, features[3])  // month (January = 1)
	assert.Equal(t, 0.0, features[4])  // is_weekend (Wednesday is not weekend)
	assert.Equal(t, 1.0, features[5])  // is_business_hours (14:00 is business hours)
}

func TestBuildTimeFeaturesWeekend(t *testing.T) {
	log := logrus.New()
	provider := &MockMetricDataProvider{IsAvailableResult: true}
	config := DefaultPredictiveConfig()
	builder := NewPredictiveFeatureBuilder(provider, config, log)

	// Test a Saturday at 10 AM
	testTime := time.Date(2026, 2, 7, 10, 0, 0, 0, time.UTC)
	features := builder.buildTimeFeatures(testTime)

	// Verify features in Python notebook order:
	// hour, day_of_week, day_of_month, month, is_weekend, is_business_hours
	assert.Equal(t, 10.0, features[0]) // hour (0-23)
	assert.Equal(t, 5.0, features[1])  // day_of_week (Saturday = 5)
	assert.Equal(t, 7.0, features[2])  // day_of_month (7th)
	assert.Equal(t, 2.0, features[3])  // month (February = 2)
	assert.Equal(t, 1.0, features[4])  // is_weekend (Saturday is weekend)
	assert.Equal(t, 0.0, features[5])  // is_business_hours (weekend, so not business hours)
}

func TestGetDefaultFeatures(t *testing.T) {
	log := logrus.New()
	provider := &MockMetricDataProvider{IsAvailableResult: true}
	config := DefaultPredictiveConfig()
	builder := NewPredictiveFeatureBuilder(provider, config, log)

	featureVector := builder.GetDefaultFeatures()

	assert.NotNil(t, featureVector)
	assert.NotEmpty(t, featureVector.Features)
	assert.Equal(t, featureVector.FeatureCount, len(featureVector.Features))
	assert.NotNil(t, featureVector.MetricsData)
	assert.NotZero(t, featureVector.Timestamp)
}

func TestGetDefaultMetricFeatures(t *testing.T) {
	log := logrus.New()
	provider := &MockMetricDataProvider{IsAvailableResult: true}
	config := DefaultPredictiveConfig()
	builder := NewPredictiveFeatureBuilder(provider, config, log)

	features := builder.getDefaultMetricFeatures()

	assert.Len(t, features, FeaturesPerMetric)
	// Verify structure: value, 6 lags, 16 rolling stats, diff, pct_change
	assert.Equal(t, 0.5, features[0])                   // value
	assert.Equal(t, 0.0, features[FeaturesPerMetric-2]) // diff
	assert.Equal(t, 0.0, features[FeaturesPerMetric-1]) // pct_change
}

func TestBuildFeaturesProviderUnavailable(t *testing.T) {
	log := logrus.New()
	provider := &MockMetricDataProvider{IsAvailableResult: false}
	config := DefaultPredictiveConfig()
	builder := NewPredictiveFeatureBuilder(provider, config, log)

	_, err := builder.BuildFeatures(context.Background(), "", "", "")

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not available")
}

func TestBuildFeaturesNilProvider(t *testing.T) {
	log := logrus.New()
	config := DefaultPredictiveConfig()
	builder := NewPredictiveFeatureBuilder(nil, config, log)

	_, err := builder.BuildFeatures(context.Background(), "", "", "")

	assert.Error(t, err)
}

func TestBuildFeaturesSuccess(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	provider := &MockMetricDataProvider{
		IsAvailableResult: true,
		QueryFunc: func(ctx context.Context, query string) (float64, error) {
			return 0.65, nil
		},
		QueryRangeFunc: func(ctx context.Context, query string, start, end time.Time, step time.Duration) ([]DataPoint, error) {
			// Return sample data points
			points := make([]DataPoint, 0)
			current := start
			for current.Before(end) {
				points = append(points, DataPoint{
					Timestamp: current,
					Value:     0.5 + float64(len(points))*0.01,
				})
				current = current.Add(step)
			}
			return points, nil
		},
	}

	config := PredictiveFeatureConfig{
		LookbackHours: 2, // Use shorter lookback for faster tests
		Enabled:       true,
	}
	builder := NewPredictiveFeatureBuilder(provider, config, log)

	featureVector, err := builder.BuildFeatures(context.Background(), "test-namespace", "", "")

	require.NoError(t, err)
	assert.NotNil(t, featureVector)
	assert.NotEmpty(t, featureVector.Features)
	assert.Equal(t, featureVector.FeatureCount, len(featureVector.Features))
	assert.NotNil(t, featureVector.MetricsData)

	// Verify metrics data contains expected metrics
	for _, metric := range predictiveBaseMetrics {
		_, exists := featureVector.MetricsData[metric]
		assert.True(t, exists, "Expected metric %s in MetricsData", metric)
	}
}

func TestCalculateStats(t *testing.T) {
	tests := []struct {
		name         string
		points       []DataPoint
		expectedMean float64
		expectedStd  float64
		expectedMax  float64
		expectedMin  float64
	}{
		{
			name:         "empty points",
			points:       []DataPoint{},
			expectedMean: 0,
			expectedStd:  0,
			expectedMax:  0,
			expectedMin:  0,
		},
		{
			name: "single point",
			points: []DataPoint{
				{Value: 0.5},
			},
			expectedMean: 0.5,
			expectedStd:  0, // No std dev for single point
			expectedMax:  0.5,
			expectedMin:  0.5,
		},
		{
			name: "multiple points",
			points: []DataPoint{
				{Value: 0.2},
				{Value: 0.4},
				{Value: 0.6},
				{Value: 0.8},
			},
			expectedMean: 0.5,
			expectedMax:  0.8,
			expectedMin:  0.2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mean, std, maxVal, minVal := calculateStats(tt.points)

			assert.InDelta(t, tt.expectedMean, mean, 0.001)
			if tt.expectedStd > 0 {
				assert.Greater(t, std, 0.0)
			}
			assert.Equal(t, tt.expectedMax, maxVal)
			assert.Equal(t, tt.expectedMin, minVal)
		})
	}
}

func TestCalculateTotalFeatures(t *testing.T) {
	log := logrus.New()
	provider := &MockMetricDataProvider{IsAvailableResult: true}
	config := DefaultPredictiveConfig()
	builder := NewPredictiveFeatureBuilder(provider, config, log)

	total := builder.calculateTotalFeatures()

	// With default config (24 hours), using Python formula:
	// lookback × (metrics + time_features + features_per_metric × metrics)
	// = 24 × (5 + 6 + 25×5) = 24 × 136 = 3264
	baseMetrics := 5
	columnsPerTimestep := baseMetrics + TimeFeatureCount + (FeaturesPerMetric * baseMetrics)
	expected := 24 * columnsPerTimestep
	assert.Equal(t, 3264, expected, "Formula verification: should equal 3264")
	assert.Equal(t, expected, total)
}

func TestGetPredictiveBaseMetrics(t *testing.T) {
	metrics := GetPredictiveBaseMetrics()

	assert.Len(t, metrics, 5)
	assert.Contains(t, metrics, "cpu_usage")
	assert.Contains(t, metrics, "memory_usage")
	assert.Contains(t, metrics, "disk_usage")
	assert.Contains(t, metrics, "network_in")
	assert.Contains(t, metrics, "network_out")
}

func TestGetPredictiveFeatureNames(t *testing.T) {
	names := GetPredictiveFeatureNames()

	assert.Len(t, names, FeaturesPerMetric)
	assert.Contains(t, names, "value")
	assert.Contains(t, names, "lag_1h")
	assert.Contains(t, names, "rolling_mean_24h")
	assert.Contains(t, names, "diff")
	assert.Contains(t, names, "pct_change")
}

func TestGetTimeFeatureNames(t *testing.T) {
	names := GetTimeFeatureNames()

	assert.Len(t, names, TimeFeatureCount) // Should be 6 features
	// Verify all 6 time feature names (matching Python notebook)
	assert.Contains(t, names, "hour")
	assert.Contains(t, names, "day_of_week")
	assert.Contains(t, names, "day_of_month")
	assert.Contains(t, names, "month")
	assert.Contains(t, names, "is_weekend")
	assert.Contains(t, names, "is_business_hours")
}

func TestJoinSelectors(t *testing.T) {
	tests := []struct {
		name      string
		selectors []string
		expected  string
	}{
		{
			name:      "empty",
			selectors: []string{},
			expected:  "",
		},
		{
			name:      "single",
			selectors: []string{`namespace="test"`},
			expected:  `namespace="test"`,
		},
		{
			name:      "multiple",
			selectors: []string{`namespace="test"`, `pod="my-pod"`},
			expected:  `namespace="test",pod="my-pod"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := joinSelectors(tt.selectors)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetMetricQuery(t *testing.T) {
	log := logrus.New()
	provider := &MockMetricDataProvider{IsAvailableResult: true}
	config := DefaultPredictiveConfig()
	builder := NewPredictiveFeatureBuilder(provider, config, log)

	tests := []struct {
		name       string
		metric     string
		namespace  string
		deployment string
		pod        string
		contains   string
	}{
		{
			name:     "cpu_usage no scope",
			metric:   "cpu_usage",
			contains: "container_cpu_usage_seconds_total",
		},
		{
			name:      "memory_usage with namespace",
			metric:    "memory_usage",
			namespace: "test-ns",
			contains:  `namespace="test-ns"`,
		},
		{
			name:       "disk_usage with deployment",
			metric:     "disk_usage",
			namespace:  "test-ns",
			deployment: "my-app",
			contains:   `pod=~"my-app-.*"`,
		},
		{
			name:      "network_in with pod",
			metric:    "network_in",
			namespace: "test-ns",
			pod:       "my-pod-abc123",
			contains:  `pod="my-pod-abc123"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := builder.getMetricQuery(tt.metric, tt.namespace, tt.deployment, tt.pod)
			assert.Contains(t, query, tt.contains)
		})
	}
}

func BenchmarkBuildTimeFeatures(b *testing.B) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	provider := &MockMetricDataProvider{IsAvailableResult: true}
	config := DefaultPredictiveConfig()
	builder := NewPredictiveFeatureBuilder(provider, config, log)

	testTime := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		builder.buildTimeFeatures(testTime)
	}
}

func BenchmarkGetDefaultFeatures(b *testing.B) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	provider := &MockMetricDataProvider{IsAvailableResult: true}
	config := DefaultPredictiveConfig()
	builder := NewPredictiveFeatureBuilder(provider, config, log)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		builder.GetDefaultFeatures()
	}
}
