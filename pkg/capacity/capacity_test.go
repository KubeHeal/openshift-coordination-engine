package capacity

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinearRegression(t *testing.T) {
	tests := []struct {
		name              string
		dataPoints        []DataPoint
		expectedSlope     float64
		expectedIntercept float64
		minRSquared       float64
	}{
		{
			name: "perfect linear data - increasing",
			dataPoints: []DataPoint{
				{Timestamp: time.Now().Add(-7 * 24 * time.Hour), Value: 10},
				{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 12},
				{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 14},
				{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 16},
				{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 18},
				{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 20},
				{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 22},
			},
			expectedSlope:     2.0,
			expectedIntercept: 10.0,
			minRSquared:       0.99,
		},
		{
			name: "perfect linear data - decreasing",
			dataPoints: []DataPoint{
				{Timestamp: time.Now().Add(-7 * 24 * time.Hour), Value: 22},
				{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 20},
				{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 18},
				{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 16},
				{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 14},
				{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 12},
				{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 10},
			},
			expectedSlope:     -2.0,
			expectedIntercept: 22.0,
			minRSquared:       0.99,
		},
		{
			name: "constant data",
			dataPoints: []DataPoint{
				{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 10},
				{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 10},
				{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 10},
			},
			expectedSlope:     0,
			expectedIntercept: 10.0,
			minRSquared:       1.0,
		},
		{
			name:              "single point",
			dataPoints:        []DataPoint{{Timestamp: time.Now(), Value: 10}},
			expectedSlope:     0,
			expectedIntercept: 0,
			minRSquared:       0,
		},
		{
			name:              "empty data",
			dataPoints:        []DataPoint{},
			expectedSlope:     0,
			expectedIntercept: 0,
			minRSquared:       0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slope, intercept, rSquared := LinearRegression(tt.dataPoints)

			assert.InDelta(t, tt.expectedSlope, slope, 0.1)
			assert.InDelta(t, tt.expectedIntercept, intercept, 0.1)
			assert.GreaterOrEqual(t, rSquared, tt.minRSquared)
		})
	}
}

func TestCalculateDailyChangePercent(t *testing.T) {
	tests := []struct {
		name       string
		dataPoints []DataPoint
		expected   float64
	}{
		{
			name: "10% daily increase",
			dataPoints: []DataPoint{
				{Timestamp: time.Now().Add(-7 * 24 * time.Hour), Value: 100},
				{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 110},
				{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 120},
				{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 130},
				{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 140},
				{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 150},
				{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 160},
			},
			expected: 7.69, // slope(10) / avg(130) * 100
		},
		{
			name:       "insufficient data",
			dataPoints: []DataPoint{{Timestamp: time.Now(), Value: 100}},
			expected:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateDailyChangePercent(tt.dataPoints)
			assert.InDelta(t, tt.expected, result, 1.0)
		})
	}
}

func TestCalculateWeeklyChangePercent(t *testing.T) {
	dataPoints := []DataPoint{
		{Timestamp: time.Now().Add(-7 * 24 * time.Hour), Value: 100},
		{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 110},
		{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 120},
		{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 130},
		{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 140},
		{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 150},
		{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 160},
	}

	dailyChange := CalculateDailyChangePercent(dataPoints)
	weeklyChange := CalculateWeeklyChangePercent(dataPoints)

	// Weekly should be 7x daily
	assert.InDelta(t, dailyChange*7, weeklyChange, 0.01)
}

func TestDaysUntilThreshold(t *testing.T) {
	tests := []struct {
		name               string
		current            float64
		limit              float64
		dailyChangePercent float64
		threshold          float64
		expectedDays       int
	}{
		{
			name:               "increasing usage - 5% daily",
			current:            60,
			limit:              100,
			dailyChangePercent: 5,
			threshold:          0.85,
			expectedDays:       9, // Need 25 more, 3 per day (5% of 60)
		},
		{
			name:               "already at threshold",
			current:            90,
			limit:              100,
			dailyChangePercent: 5,
			threshold:          0.85,
			expectedDays:       0,
		},
		{
			name:               "decreasing usage",
			current:            60,
			limit:              100,
			dailyChangePercent: -5,
			threshold:          0.85,
			expectedDays:       -1,
		},
		{
			name:               "stable usage",
			current:            60,
			limit:              100,
			dailyChangePercent: 0,
			threshold:          0.85,
			expectedDays:       -1,
		},
		{
			name:               "no limit set",
			current:            60,
			limit:              0,
			dailyChangePercent: 5,
			threshold:          0.85,
			expectedDays:       -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			days := DaysUntilThreshold(tt.current, tt.limit, tt.dailyChangePercent, tt.threshold)
			assert.Equal(t, tt.expectedDays, days)
		})
	}
}

func TestCalculateProjectedExhaustionDate(t *testing.T) {
	tests := []struct {
		name     string
		days     int
		expected string
	}{
		{
			name:     "negative days returns empty",
			days:     -1,
			expected: "",
		},
		{
			name:     "zero days returns today",
			days:     0,
			expected: time.Now().Format("2006-01-02"),
		},
		{
			name:     "7 days from now",
			days:     7,
			expected: time.Now().AddDate(0, 0, 7).Format("2006-01-02"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateProjectedExhaustionDate(tt.days)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDetermineTrendDirection(t *testing.T) {
	tests := []struct {
		dailyChangePercent float64
		expected           TrendDirection
	}{
		{5.0, TrendDirectionIncreasing},
		{0.6, TrendDirectionIncreasing},
		{0.5, TrendDirectionStable}, // At threshold
		{0.3, TrendDirectionStable},
		{0.0, TrendDirectionStable},
		{-0.3, TrendDirectionStable},
		{-0.5, TrendDirectionStable}, // At threshold
		{-0.6, TrendDirectionDecreasing},
		{-5.0, TrendDirectionDecreasing},
	}

	for _, tt := range tests {
		result := DetermineTrendDirection(tt.dailyChangePercent)
		assert.Equal(t, tt.expected, result, "for dailyChange=%f", tt.dailyChangePercent)
	}
}

func TestCalculateConfidence(t *testing.T) {
	tests := []struct {
		name       string
		dataPoints []DataPoint
		rSquared   float64
		minConf    float64
		maxConf    float64
	}{
		{
			name:       "empty data",
			dataPoints: []DataPoint{},
			rSquared:   0,
			minConf:    0,
			maxConf:    0,
		},
		{
			name: "small dataset, low r-squared",
			dataPoints: []DataPoint{
				{Timestamp: time.Now().Add(-1 * time.Hour), Value: 10},
				{Timestamp: time.Now(), Value: 12},
			},
			rSquared: 0.5,
			minConf:  0,
			maxConf:  0.3,
		},
		{
			name: "large dataset, high r-squared",
			dataPoints: func() []DataPoint {
				dp := make([]DataPoint, 168) // 7 days * 24 hours
				for i := 0; i < 168; i++ {
					dp[i] = DataPoint{
						Timestamp: time.Now().Add(-time.Duration(168-i) * time.Hour),
						Value:     float64(10 + i),
					}
				}
				return dp
			}(),
			rSquared: 0.99,
			minConf:  0.8,
			maxConf:  1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			confidence := CalculateConfidence(tt.dataPoints, tt.rSquared)
			assert.GreaterOrEqual(t, confidence, tt.minConf)
			assert.LessOrEqual(t, confidence, tt.maxConf)
		})
	}
}

func TestAnalyzeTrend(t *testing.T) {
	// Create test data showing increasing CPU and memory usage
	cpuDataPoints := []DataPoint{
		{Timestamp: time.Now().Add(-7 * 24 * time.Hour), Value: 2.0},
		{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 2.2},
		{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 2.4},
		{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 2.6},
		{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 2.8},
		{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 3.0},
		{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 3.2},
	}

	memDataPoints := []DataPoint{
		{Timestamp: time.Now().Add(-7 * 24 * time.Hour), Value: 4000000000},
		{Timestamp: time.Now().Add(-6 * 24 * time.Hour), Value: 4200000000},
		{Timestamp: time.Now().Add(-5 * 24 * time.Hour), Value: 4400000000},
		{Timestamp: time.Now().Add(-4 * 24 * time.Hour), Value: 4600000000},
		{Timestamp: time.Now().Add(-3 * 24 * time.Hour), Value: 4800000000},
		{Timestamp: time.Now().Add(-2 * 24 * time.Hour), Value: 5000000000},
		{Timestamp: time.Now().Add(-1 * 24 * time.Hour), Value: 5200000000},
	}

	t.Run("increasing trend", func(t *testing.T) {
		result := AnalyzeTrend(cpuDataPoints, memDataPoints, 3.2, 10.0, 5200000000, 10000000000)

		require.NotNil(t, result)
		require.NotNil(t, result.CPU)
		require.NotNil(t, result.Memory)

		assert.Equal(t, TrendDirectionIncreasing, result.CPU.Direction)
		assert.Equal(t, TrendDirectionIncreasing, result.Memory.Direction)
		assert.Greater(t, result.CPU.DailyChangePercent, 0.0)
		assert.Greater(t, result.Memory.DailyChangePercent, 0.0)
		assert.Greater(t, result.Confidence, 0.0)
	})

	t.Run("empty cpu data", func(t *testing.T) {
		result := AnalyzeTrend([]DataPoint{}, memDataPoints, 3.2, 10.0, 5200000000, 10000000000)

		require.NotNil(t, result)
		assert.Nil(t, result.CPU)
		require.NotNil(t, result.Memory)
	})

	t.Run("empty memory data", func(t *testing.T) {
		result := AnalyzeTrend(cpuDataPoints, []DataPoint{}, 3.2, 10.0, 5200000000, 10000000000)

		require.NotNil(t, result)
		require.NotNil(t, result.CPU)
		assert.Nil(t, result.Memory)
	})
}

func TestCalculateAvailableCapacity(t *testing.T) {
	tests := []struct {
		name               string
		quota              *NamespaceQuota
		usage              *ResourceUsage
		expectedCPU        float64
		expectedMem        int64
		expectedPodSlots   int64
	}{
		{
			name: "normal usage",
			quota: &NamespaceQuota{
				CPU:           &CPUQuota{Limit: "10", LimitNumeric: 10.0},
				Memory:        &MemoryQuota{Limit: "10Gi", LimitBytes: 10737418240},
				PodCountLimit: 50,
				HasQuota:      true,
			},
			usage: &ResourceUsage{
				CPU:      &CPUUsage{Used: "6000m", UsedNumeric: 6.0, Percent: 60.0},
				Memory:   &MemoryUsage{Used: "6Gi", UsedBytes: 6442450944, Percent: 60.0},
				PodCount: 20,
			},
			expectedCPU:      4.0,
			expectedMem:      4294967296,
			expectedPodSlots: 30,
		},
		{
			name: "over quota usage",
			quota: &NamespaceQuota{
				CPU:           &CPUQuota{Limit: "10", LimitNumeric: 10.0},
				Memory:        &MemoryQuota{Limit: "10Gi", LimitBytes: 10737418240},
				PodCountLimit: 50,
				HasQuota:      true,
			},
			usage: &ResourceUsage{
				CPU:      &CPUUsage{Used: "12000m", UsedNumeric: 12.0, Percent: 120.0},
				Memory:   &MemoryUsage{Used: "12Gi", UsedBytes: 12884901888, Percent: 120.0},
				PodCount: 60,
			},
			expectedCPU:      0,
			expectedMem:      0,
			expectedPodSlots: 0,
		},
		{
			name: "no quota",
			quota: &NamespaceQuota{
				HasQuota: false,
			},
			usage: &ResourceUsage{
				PodCount: 10,
			},
			expectedCPU:      0,
			expectedMem:      0,
			expectedPodSlots: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateAvailableCapacity(tt.quota, tt.usage)

			if tt.quota.CPU != nil && tt.usage.CPU != nil {
				require.NotNil(t, result.CPU)
				assert.InDelta(t, tt.expectedCPU, result.CPU.AvailableNumeric, 0.1)
			}

			if tt.quota.Memory != nil && tt.usage.Memory != nil {
				require.NotNil(t, result.Memory)
				assert.InDelta(t, tt.expectedMem, result.Memory.AvailableBytes, 1000)
			}

			if tt.quota.PodCountLimit > 0 {
				assert.Equal(t, tt.expectedPodSlots, result.PodSlots)
			}
		})
	}
}

func TestFormatCPU(t *testing.T) {
	tests := []struct {
		cores    float64
		expected string
	}{
		{1.0, "1000m"},
		{0.5, "500m"},
		{2.5, "2500m"},
		{0.001, "1m"},
		{0.0, "0m"},
		{10.0, "10000m"},
	}

	for _, tt := range tests {
		result := formatCPU(tt.cores)
		assert.Equal(t, tt.expected, result)
	}
}

func TestFormatBytes(t *testing.T) {
	const (
		KB = int64(1024)
		MB = KB * 1024
		GB = MB * 1024
	)

	tests := []struct {
		bytes    int64
		expected string
	}{
		{GB, "1.0Gi"},
		{2 * GB, "2.0Gi"},
		{512 * MB, "512.0Mi"},
		{100 * MB, "100.0Mi"},
		{512 * KB, "512.0Ki"},
		{512, "512"},
		{0, "0"},
	}

	for _, tt := range tests {
		result := formatBytes(tt.bytes)
		assert.Equal(t, tt.expected, result)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{-1 * time.Hour, "N/A"},
		{48 * time.Hour, "2d"},
		{24 * time.Hour, "1d"},
		{12 * time.Hour, "12h"},
		{90 * time.Minute, "1h"},
		{30 * time.Minute, "30m"},
		{30 * time.Second, "30s"},
	}

	for _, tt := range tests {
		result := FormatDuration(tt.duration)
		assert.Equal(t, tt.expected, result)
	}
}

func TestTrendDirectionConstants(t *testing.T) {
	// Ensure constants are defined correctly
	assert.Equal(t, TrendDirection("increasing"), TrendDirectionIncreasing)
	assert.Equal(t, TrendDirection("decreasing"), TrendDirectionDecreasing)
	assert.Equal(t, TrendDirection("stable"), TrendDirectionStable)
}

func BenchmarkLinearRegression(b *testing.B) {
	dataPoints := make([]DataPoint, 168)
	for i := 0; i < 168; i++ {
		dataPoints[i] = DataPoint{
			Timestamp: time.Now().Add(-time.Duration(168-i) * time.Hour),
			Value:     float64(10 + i),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		LinearRegression(dataPoints)
	}
}

func BenchmarkCalculateDailyChangePercent(b *testing.B) {
	dataPoints := make([]DataPoint, 168)
	for i := 0; i < 168; i++ {
		dataPoints[i] = DataPoint{
			Timestamp: time.Now().Add(-time.Duration(168-i) * time.Hour),
			Value:     float64(10 + i),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		CalculateDailyChangePercent(dataPoints)
	}
}

func BenchmarkDaysUntilThreshold(b *testing.B) {
	for i := 0; i < b.N; i++ {
		DaysUntilThreshold(60, 100, 5, 0.85)
	}
}

func BenchmarkAnalyzeTrend(b *testing.B) {
	cpuDataPoints := make([]DataPoint, 168)
	memDataPoints := make([]DataPoint, 168)
	for i := 0; i < 168; i++ {
		cpuDataPoints[i] = DataPoint{
			Timestamp: time.Now().Add(-time.Duration(168-i) * time.Hour),
			Value:     float64(2 + i/100),
		}
		memDataPoints[i] = DataPoint{
			Timestamp: time.Now().Add(-time.Duration(168-i) * time.Hour),
			Value:     float64(4000000000 + i*10000000),
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AnalyzeTrend(cpuDataPoints, memDataPoints, 3.2, 10.0, 5200000000, 10000000000)
	}
}

// TestRSquaredBounds ensures R-squared values are properly bounded
func TestRSquaredBounds(t *testing.T) {
	// Test with various datasets
	datasets := [][]DataPoint{
		// Perfect fit
		{
			{Timestamp: time.Now().Add(-2 * time.Hour), Value: 10},
			{Timestamp: time.Now().Add(-1 * time.Hour), Value: 20},
			{Timestamp: time.Now(), Value: 30},
		},
		// Constant values
		{
			{Timestamp: time.Now().Add(-2 * time.Hour), Value: 10},
			{Timestamp: time.Now().Add(-1 * time.Hour), Value: 10},
			{Timestamp: time.Now(), Value: 10},
		},
	}

	for _, data := range datasets {
		_, _, rSquared := LinearRegression(data)
		assert.GreaterOrEqual(t, rSquared, 0.0)
		assert.LessOrEqual(t, rSquared, 1.0)
		assert.False(t, math.IsNaN(rSquared))
		assert.False(t, math.IsInf(rSquared, 0))
	}
}
