// Package capacity provides capacity analysis and trending utilities for OpenShift resources.
package capacity

import (
	"math"
	"time"
)

// TrendDirection indicates the direction of a trend
type TrendDirection string

const (
	// TrendDirectionIncreasing indicates increasing trend
	TrendDirectionIncreasing TrendDirection = "increasing"
	// TrendDirectionDecreasing indicates decreasing trend
	TrendDirectionDecreasing TrendDirection = "decreasing"
	// TrendDirectionStable indicates stable/no significant change
	TrendDirectionStable TrendDirection = "stable"
)

// ResourceTrend contains trending information for a resource
type ResourceTrend struct {
	DailyChangePercent  float64        `json:"daily_change_percent"`
	WeeklyChangePercent float64        `json:"weekly_change_percent"`
	Direction           TrendDirection `json:"direction"`
}

// TrendingInfo contains overall trending information
type TrendingInfo struct {
	CPU                     *ResourceTrend `json:"cpu,omitempty"`
	Memory                  *ResourceTrend `json:"memory,omitempty"`
	DaysUntil85Percent      int            `json:"days_until_85_percent"`
	ProjectedExhaustionDate string         `json:"projected_exhaustion_date,omitempty"`
	Confidence              float64        `json:"confidence"`
}

// DataPoint represents a single metric data point
type DataPoint struct {
	Timestamp time.Time
	Value     float64
}

// LinearRegression calculates the slope and intercept of a linear regression
// Returns: slope, intercept, r-squared (coefficient of determination)
func LinearRegression(dataPoints []DataPoint) (slope, intercept, rSquared float64) {
	n := float64(len(dataPoints))
	if n < 2 {
		return 0, 0, 0
	}

	// Convert timestamps to days from start for x values
	startTime := dataPoints[0].Timestamp
	x := make([]float64, len(dataPoints))
	y := make([]float64, len(dataPoints))

	for i, dp := range dataPoints {
		x[i] = dp.Timestamp.Sub(startTime).Hours() / 24.0 // days
		y[i] = dp.Value
	}

	// Calculate means
	var sumX, sumY, sumXY, sumX2, sumY2 float64
	for i := 0; i < len(x); i++ {
		sumX += x[i]
		sumY += y[i]
		sumXY += x[i] * y[i]
		sumX2 += x[i] * x[i]
		sumY2 += y[i] * y[i]
	}

	meanX := sumX / n
	meanY := sumY / n

	// Calculate slope and intercept
	numerator := sumXY - n*meanX*meanY
	denominator := sumX2 - n*meanX*meanX

	if denominator == 0 {
		return 0, meanY, 0
	}

	slope = numerator / denominator
	intercept = meanY - slope*meanX

	// Calculate R-squared
	ssRes := 0.0
	ssTot := 0.0
	for i := 0; i < len(x); i++ {
		predicted := slope*x[i] + intercept
		ssRes += (y[i] - predicted) * (y[i] - predicted)
		ssTot += (y[i] - meanY) * (y[i] - meanY)
	}

	if ssTot == 0 {
		rSquared = 1.0 // All values are the same
	} else {
		rSquared = 1.0 - (ssRes / ssTot)
	}

	return slope, intercept, rSquared
}

// CalculateDailyChangePercent calculates the daily change percentage from data points
func CalculateDailyChangePercent(dataPoints []DataPoint) float64 {
	if len(dataPoints) < 2 {
		return 0
	}

	slope, _, _ := LinearRegression(dataPoints)

	// Get the average value as baseline
	var sum float64
	for _, dp := range dataPoints {
		sum += dp.Value
	}
	avgValue := sum / float64(len(dataPoints))

	if avgValue == 0 {
		return 0
	}

	// Convert slope to daily change percentage
	// slope is change per day, convert to percentage of average
	dailyChangePercent := (slope / avgValue) * 100

	return dailyChangePercent
}

// CalculateWeeklyChangePercent calculates the weekly change percentage
func CalculateWeeklyChangePercent(dataPoints []DataPoint) float64 {
	return CalculateDailyChangePercent(dataPoints) * 7
}

// DaysUntilThreshold calculates days until a threshold is reached
// Returns -1 if usage is stable or decreasing
func DaysUntilThreshold(current, limit, dailyChangePercent, threshold float64) int {
	if dailyChangePercent <= 0 {
		return -1 // Usage decreasing or stable
	}

	if limit <= 0 {
		return -1 // No limit set
	}

	targetUsage := limit * threshold // e.g., 85% of limit
	delta := targetUsage - current

	if delta <= 0 {
		return 0 // Already at or above threshold
	}

	// dailyChangePercent is percentage of current usage per day
	dailyAbsoluteChange := current * (dailyChangePercent / 100)
	if dailyAbsoluteChange <= 0 {
		return -1
	}

	days := delta / dailyAbsoluteChange

	return int(math.Ceil(days))
}

// CalculateProjectedExhaustionDate calculates when resources will be exhausted
func CalculateProjectedExhaustionDate(days int) string {
	if days < 0 {
		return ""
	}

	exhaustionDate := time.Now().AddDate(0, 0, days)
	return exhaustionDate.Format("2006-01-02")
}

// DetermineTrendDirection determines the trend direction from daily change percentage
func DetermineTrendDirection(dailyChangePercent float64) TrendDirection {
	// Use a threshold of 0.5% daily change to determine significance
	const threshold = 0.5

	if dailyChangePercent > threshold {
		return TrendDirectionIncreasing
	} else if dailyChangePercent < -threshold {
		return TrendDirectionDecreasing
	}
	return TrendDirectionStable
}

// CalculateConfidence calculates confidence score based on data quality
func CalculateConfidence(dataPoints []DataPoint, rSquared float64) float64 {
	if len(dataPoints) < 2 {
		return 0
	}

	// Factors affecting confidence:
	// 1. Number of data points (more is better, up to 168 for hourly data over 7 days)
	// 2. R-squared value (higher is better)
	// 3. Time span of data (longer is better, up to 7 days)

	// Data point factor (0-0.4)
	maxPoints := 168.0 // 7 days * 24 hours
	pointsFactor := math.Min(float64(len(dataPoints))/maxPoints, 1.0) * 0.4

	// R-squared factor (0-0.4)
	rSquaredFactor := math.Max(0, rSquared) * 0.4

	// Time span factor (0-0.2)
	if len(dataPoints) < 2 {
		return pointsFactor + rSquaredFactor
	}

	timeSpan := dataPoints[len(dataPoints)-1].Timestamp.Sub(dataPoints[0].Timestamp)
	maxSpan := 7 * 24 * time.Hour
	spanFactor := math.Min(timeSpan.Hours()/maxSpan.Hours(), 1.0) * 0.2

	confidence := pointsFactor + rSquaredFactor + spanFactor

	// Round to 2 decimal places
	return math.Round(confidence*100) / 100
}

// AnalyzeTrend performs trend analysis on data points
func AnalyzeTrend(cpuDataPoints, memoryDataPoints []DataPoint, currentCPU, cpuLimit, currentMemory, memoryLimit float64) *TrendingInfo {
	result := &TrendingInfo{
		DaysUntil85Percent: -1,
		Confidence:         0,
	}

	// CPU trending
	if len(cpuDataPoints) >= 2 {
		dailyCPUChange := CalculateDailyChangePercent(cpuDataPoints)
		weeklyCPUChange := CalculateWeeklyChangePercent(cpuDataPoints)
		_, _, cpuRSquared := LinearRegression(cpuDataPoints)

		result.CPU = &ResourceTrend{
			DailyChangePercent:  math.Round(dailyCPUChange*100) / 100,
			WeeklyChangePercent: math.Round(weeklyCPUChange*100) / 100,
			Direction:           DetermineTrendDirection(dailyCPUChange),
		}

		// Calculate days until 85% for CPU
		cpuDays := DaysUntilThreshold(currentCPU, cpuLimit, dailyCPUChange, 0.85)
		if cpuDays >= 0 && (result.DaysUntil85Percent < 0 || cpuDays < result.DaysUntil85Percent) {
			result.DaysUntil85Percent = cpuDays
		}

		result.Confidence = CalculateConfidence(cpuDataPoints, cpuRSquared)
	}

	// Memory trending
	if len(memoryDataPoints) >= 2 {
		dailyMemoryChange := CalculateDailyChangePercent(memoryDataPoints)
		weeklyMemoryChange := CalculateWeeklyChangePercent(memoryDataPoints)
		_, _, memRSquared := LinearRegression(memoryDataPoints)

		result.Memory = &ResourceTrend{
			DailyChangePercent:  math.Round(dailyMemoryChange*100) / 100,
			WeeklyChangePercent: math.Round(weeklyMemoryChange*100) / 100,
			Direction:           DetermineTrendDirection(dailyMemoryChange),
		}

		// Calculate days until 85% for memory
		memDays := DaysUntilThreshold(currentMemory, memoryLimit, dailyMemoryChange, 0.85)
		if memDays >= 0 && (result.DaysUntil85Percent < 0 || memDays < result.DaysUntil85Percent) {
			result.DaysUntil85Percent = memDays
		}

		// Use higher confidence between CPU and memory
		memConfidence := CalculateConfidence(memoryDataPoints, memRSquared)
		if memConfidence > result.Confidence {
			result.Confidence = memConfidence
		}
	}

	// Set projected exhaustion date
	if result.DaysUntil85Percent >= 0 {
		result.ProjectedExhaustionDate = CalculateProjectedExhaustionDate(result.DaysUntil85Percent)
	}

	return result
}
