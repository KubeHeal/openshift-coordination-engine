# Feature Engineering Developer Guide

This guide explains how to maintain and update the predictive analytics feature engineering in the coordination engine when the ML model's feature requirements change.

## Overview

The `predictive-analytics` KServe model requires a specific feature vector structure. The Go-based feature engineering in `pkg/features/predictive.go` must exactly match the Python training notebook's feature engineering.

**Critical**: Changes to the model's feature engineering require coordinated updates to both:
1. The Python training notebook
2. The Go feature engineering code

## Current Feature Structure

### Feature Count Formula

The feature vector structure matches the Python training notebook exactly:

```
Total Features = LookbackHours × (BaseMetrics + TimeFeatures + FeaturesPerMetric × BaseMetrics)

With defaults (24-hour lookback):
Total = 24 × (5 + 6 + 25×5) = 24 × (5 + 6 + 125) = 24 × 136 = 3264 features
```

**Per timestep breakdown (136 features):**
1. Raw metric values: 5 features
2. Time features: 6 features
3. Engineered metric features: 25 × 5 = 125 features

### Base Metrics (5)

Defined in `pkg/features/predictive.go`:

```go
var predictiveBaseMetrics = []string{
    "cpu_usage",
    "memory_usage",
    "disk_usage",
    "network_in",
    "network_out",
}
```

### Features Per Metric (25)

| Feature | Index | Description |
|---------|-------|-------------|
| value | 0 | Current metric value |
| lag_1h | 1 | 1-hour lag |
| lag_2h | 2 | 2-hour lag |
| lag_3h | 3 | 3-hour lag |
| lag_6h | 4 | 6-hour lag |
| lag_12h | 5 | 12-hour lag |
| lag_24h | 6 | 24-hour lag |
| rolling_mean_3h | 7 | 3-hour rolling mean |
| rolling_mean_6h | 8 | 6-hour rolling mean |
| rolling_mean_12h | 9 | 12-hour rolling mean |
| rolling_mean_24h | 10 | 24-hour rolling mean |
| rolling_std_3h | 11 | 3-hour rolling std |
| rolling_std_6h | 12 | 6-hour rolling std |
| rolling_std_12h | 13 | 12-hour rolling std |
| rolling_std_24h | 14 | 24-hour rolling std |
| rolling_max_3h | 15 | 3-hour rolling max |
| rolling_max_6h | 16 | 6-hour rolling max |
| rolling_max_12h | 17 | 12-hour rolling max |
| rolling_max_24h | 18 | 24-hour rolling max |
| rolling_min_3h | 19 | 3-hour rolling min |
| rolling_min_6h | 20 | 6-hour rolling min |
| rolling_min_12h | 21 | 12-hour rolling min |
| rolling_min_24h | 22 | 24-hour rolling min |
| diff | 23 | value - lag_1h |
| pct_change | 24 | (value - lag_1h) / lag_1h |

### Time Features (6)

Time features match the Python training notebook exactly:

| Feature | Index | Description | Range |
|---------|-------|-------------|-------|
| hour | 0 | Hour of day | 0-23 |
| day_of_week | 1 | Day of week (Monday=0) | 0-6 |
| day_of_month | 2 | Day of month | 1-31 |
| month | 3 | Month of year | 1-12 |
| is_weekend | 4 | Weekend indicator | 0 or 1 |
| is_business_hours | 5 | Business hours (9-17 weekdays) | 0 or 1 |

## Updating Feature Engineering

### Step 1: Understand the Model Changes

Before making changes, get the following from the ML team:

1. **New feature count**: Exact number of features expected
2. **Feature order**: Precise order of features in the vector
3. **New features**: Any added features and their computation
4. **Removed features**: Any features no longer needed
5. **Changed features**: Any features with modified computation

### Step 2: Update Go Constants

Edit `pkg/features/predictive.go`:

```go
// Update if adding/removing base metrics
var predictiveBaseMetrics = []string{
    "cpu_usage",
    "memory_usage",
    // Add or remove metrics here
}

// Update if adding/removing lag periods
var lagPeriods = []int{1, 2, 3, 6, 12, 24}

// Update if adding/removing rolling windows
var rollingWindows = []int{3, 6, 12, 24}

// Update the constant
const FeaturesPerMetric = 25  // Update this number

// Update time features if changed
const TimeFeatureCount = 6    // Update this number (must match Python notebook)
```

### Step 3: Update Feature Names

Update the feature name arrays to match:

```go
var predictiveFeatureNames = []string{
    "value",
    "lag_1h",
    // ... update to match new features
}

// Time features - must match Python notebook order exactly
var timeFeatureNames = []string{
    "hour",             // 0-23
    "day_of_week",      // 0-6 (Monday=0)
    "day_of_month",     // 1-31
    "month",            // 1-12
    "is_weekend",       // 0 or 1
    "is_business_hours", // 0 or 1
}
```

### Step 4: Update Feature Building Logic

If the feature computation changes, update `buildMetricFeatures()`:

```go
func (b *PredictiveFeatureBuilder) buildMetricFeatures(...) ([]float64, float64, error) {
    // Update feature computation logic here
}
```

And update `buildTimeFeatures()` if time features change:

```go
func (b *PredictiveFeatureBuilder) buildTimeFeatures(t time.Time) []float64 {
    // Update time feature computation here
}
```

### Step 5: Update Default Features

Update `getDefaultMetricFeatures()` to return the correct number of features:

```go
func (b *PredictiveFeatureBuilder) getDefaultMetricFeatures() []float64 {
    features := make([]float64, FeaturesPerMetric)
    // Set appropriate default values
    return features
}
```

### Step 6: Update Tests

Update `pkg/features/predictive_test.go`:

1. Update `TestGetFeatureInfo` to expect new feature count
2. Update `TestCalculateTotalFeatures` with new calculation
3. Update `TestGetDefaultMetricFeatures` for new feature count
4. Add tests for any new features

### Step 7: Update Documentation

1. Update this guide with new feature structure
2. Update `docs/adrs/016-predictive-analytics-feature-engineering.md`
3. Update API documentation if response structure changes

## Validation Checklist

Before deploying changes:

- [ ] Feature count matches model's `StandardScaler` expectation
- [ ] Feature order matches training notebook exactly
- [ ] All unit tests pass: `go test ./pkg/features/...`
- [ ] Integration test passes with actual model
- [ ] Default features return correct count
- [ ] Documentation updated

## Debugging Feature Mismatches

### Error: "X has N features, but StandardScaler is expecting M features"

This error means the Go code is generating a different feature count than the model expects.

**Debug Steps:**

1. Check expected feature count from model:
   ```bash
   # Query the model metadata
   curl http://predictive-analytics-predictor:8080/v2/models/predictive-analytics
   ```

2. Check actual feature count from Go:
   ```go
   info := featureBuilder.GetFeatureInfo()
   log.Printf("Total features: %d", info.TotalFeatures)
   ```

3. Compare with calculation (Python formula):
   ```
   Expected = LookbackHours × (BaseMetrics + TimeFeatures + FeaturesPerMetric × BaseMetrics)
            = 24 × (5 + 6 + 25×5) = 24 × 136 = 3264
   ```

### Error: "ValueError: Input contains NaN"

This means some features contain invalid values.

**Debug Steps:**

1. Check Prometheus queries are returning data
2. Verify default values are set for missing data
3. Check for division by zero in pct_change calculation

### Performance Issues

If feature engineering is slow:

1. Reduce `FEATURE_ENGINEERING_LOOKBACK_HOURS` (default: 24)
2. Check Prometheus query performance
3. Consider caching historical data

## Model Versioning Strategy

To support multiple model versions:

### Option 1: Environment Variable (Current)

```yaml
env:
  - name: FEATURE_ENGINEERING_VERSION
    value: "v1"  # or "v2", etc.
```

Then in code:
```go
switch os.Getenv("FEATURE_ENGINEERING_VERSION") {
case "v2":
    return buildFeaturesV2(ctx, ...)
default:
    return buildFeaturesV1(ctx, ...)
}
```

### Option 2: Model Metadata Query

Query the model for its expected feature schema:
```go
metadata, _ := kserveClient.GetModelMetadata(ctx, "predictive-analytics")
if len(metadata.Inputs) > 0 {
    expectedFeatures := metadata.Inputs[0].Shape[1]
    // Validate or adapt
}
```

### Option 3: Feature Configuration File

Create a JSON/YAML configuration that can be updated without code changes:

```yaml
# features-config.yaml
version: "v1"
base_metrics:
  - cpu_usage
  - memory_usage
  - disk_usage
  - network_in
  - network_out
lag_periods: [1, 2, 3, 6, 12, 24]
rolling_windows: [3, 6, 12, 24]
time_features:  # Must match Python notebook (6 features)
  - hour
  - day_of_week
  - day_of_month
  - month
  - is_weekend
  - is_business_hours
```

## Coordination Workflow

When the ML team updates the model:

```
┌─────────────────┐    ┌──────────────────┐    ┌─────────────────┐
│   ML Team       │    │   Platform Team  │    │   Deploy        │
│                 │    │                  │    │                 │
│ 1. Update       │───▶│ 2. Update Go     │───▶│ 4. Deploy both  │
│    training     │    │    feature eng.  │    │    together     │
│    notebook     │    │                  │    │                 │
│                 │    │ 3. Update tests  │    │                 │
└─────────────────┘    └──────────────────┘    └─────────────────┘
```

**Important**: Model and coordination engine updates should be deployed together to avoid feature mismatches.

## Quick Reference

### Key Files

| File | Purpose |
|------|---------|
| `pkg/features/predictive.go` | Feature engineering logic |
| `pkg/features/predictive_test.go` | Unit tests |
| `pkg/features/prometheus_adapter.go` | Prometheus integration |
| `pkg/config/config.go` | Configuration |
| `pkg/api/v1/prediction.go` | API handler integration |

### Key Constants

| Constant | Location | Default |
|----------|----------|---------|
| `FeaturesPerMetric` | `pkg/features/predictive.go` | 25 |
| `TimeFeatureCount` | `pkg/features/predictive.go` | 6 |
| `DefaultFeatureEngineeringLookbackHours` | `pkg/config/config.go` | 24 |

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `ENABLE_FEATURE_ENGINEERING` | Enable/disable feature engineering | `true` |
| `FEATURE_ENGINEERING_LOOKBACK_HOURS` | Historical data lookback | `24` |
| `FEATURE_ENGINEERING_EXPECTED_COUNT` | Expected feature count for validation (0=disabled) | `0` |

### Feature Count Validation

To enable early detection of feature mismatches, set the expected feature count:

```yaml
env:
  - name: FEATURE_ENGINEERING_EXPECTED_COUNT
    value: "3264"  # Set to your model's expected feature count (must match Python model)
```

When enabled, the system logs a warning at startup if the calculated feature count doesn't match:

```
level=warning msg="Feature count mismatch detected! The model may reject predictions..."
  expected_features=3264
  actual_features=<actual>
```

This helps catch issues before they cause runtime errors.

## Contact

For questions about feature engineering:
- **Go implementation**: Platform team
- **Model training**: ML/Data Science team
- **Issue tracking**: [GitHub Issues](https://github.com/KubeHeal/openshift-coordination-engine/issues)

---

*Last Updated: 2026-01-29*
