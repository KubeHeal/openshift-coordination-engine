# ADR-012: ML-Enhanced Layer Detection

## Status
ACCEPTED - 2025-12-19

## Context

Phase 3 implemented a keyword-based heuristic layer detector that identifies which layers (infrastructure, platform, application) are affected by an issue. While this works well for common patterns, it has limitations:

1. **Limited Pattern Recognition**: Keyword matching can't detect novel multi-layer patterns
2. **Static Confidence**: All detections have implicit 100% confidence without ML validation
3. **No Historical Learning**: Doesn't learn from past incidents and their actual root causes
4. **Manual Root Cause Logic**: Uses simple heuristic (infrastructure > platform > application) without data-driven insights

The existing Python ML service (`aiops-ml-service`) already provides anomaly detection, predictions, and pattern recognition. We should leverage these capabilities to enhance layer detection accuracy.

## Decision

Enhance the existing `LayerDetector` with ML predictions while maintaining backward compatibility with the keyword-based approach:

1. **Hybrid Detection Strategy**: Combine keyword-based detection (fast, works offline) with ML predictions (accurate, learns from history)
2. **ML Confidence Scores**: Add ML-derived confidence scores for each detected layer
3. **ML Root Cause Analysis**: Use ML service to suggest root cause layer based on historical patterns
4. **Pattern-Based Multi-Layer Detection**: Leverage ML pattern recognition to identify recurring multi-layer issues
5. **Graceful Degradation**: Fall back to keyword-based detection when ML service is unavailable

## Architecture

### Enhanced Layer Detection Flow

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Keyword-Based Detection (Fast Path)                      │
│    - Extract keywords from issue description                │
│    - Map resources to layers                                │
│    - Initial affected layers: [infrastructure, application] │
│    - Keyword confidence: 0.70                                │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│ 2. ML Pattern Analysis (Enhanced Path)                      │
│    POST /api/v1/pattern/analyze                             │
│    {                                                         │
│      "historical_data": {                                    │
│        "description": "node disk pressure...",               │
│        "resources": [{kind: "Node"}, {kind: "Pod"}],         │
│        "recent_events": ["DiskPressure", "Evicted"]          │
│      }                                                       │
│    }                                                         │
│    Response:                                                 │
│    {                                                         │
│      "patterns": [                                           │
│        {"pattern": "infrastructure_cascading_failure",       │
│         "confidence": 0.92,                                  │
│         "suggested_root_cause": "infrastructure"}            │
│      ],                                                      │
│      "layer_predictions": {                                  │
│        "infrastructure": {"probability": 0.95},              │
│        "application": {"probability": 0.88}                  │
│      }                                                       │
│    }                                                         │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│ 3. Combined Confidence Calculation                          │
│    - Infrastructure: max(0.70 keyword, 0.95 ML) = 0.95      │
│    - Application: max(0.70 keyword, 0.88 ML) = 0.88         │
│    - Root cause: infrastructure (0.95 > 0.88)               │
│    - Detection method: "ml_enhanced"                         │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│ 4. Graceful Degradation (Fallback)                          │
│    - If ML service unavailable: use keyword confidence      │
│    - If ML service timeout: use cached predictions          │
│    - Always emit metrics for both detection paths           │
└─────────────────────────────────────────────────────────────┘
```

**Note (ADR-014 Enhancement)**: Historical data and recent events are now enriched with **real Prometheus/Thanos metrics** instead of default values:
- **Actual CPU/memory**: From `container_cpu_usage_seconds_total` and `container_memory_working_set_bytes`
- **Real restart counts**: From `kube_pod_container_status_restarts_total`
- **Historical trends**: From Thanos long-term storage (months of data vs. 2 days)
- **45-feature vectors**: CPU trends, memory patterns, resource utilization, platform health

This improves ML pattern matching confidence from **~0.75 (with defaults) to ~0.90 (with real metrics)** for infrastructure-layer issues. The ML service can now detect gradual degradation patterns (e.g., "memory growing 5% per day") that were invisible with generic metrics.

**Example Impact**:
- **Before (defaults)**: "Node memory pressure" detected with 0.75 confidence (keyword-based)
- **After (real metrics)**: "Node memory pressure" detected with 0.92 confidence (ML sees actual memory trend: 65% → 78% → 89% over 24 hours)

### Enhanced Data Models

**LayeredIssue Extension**:
```go
type LayeredIssue struct {
    ID                string
    Description       string
    AffectedLayers    []Layer
    RootCauseLayer    Layer
    ImpactedResources map[Layer][]Resource
    DetectedAt        time.Time
    Severity          string

    // NEW: ML-enhanced fields
    MLPredictions     *MLLayerPredictions `json:"ml_predictions,omitempty"`
    LayerConfidence   map[Layer]float64   `json:"layer_confidence,omitempty"`
    DetectionMethod   string              `json:"detection_method"` // "keyword", "ml_enhanced", "ml_only"
    HistoricalPattern string              `json:"historical_pattern,omitempty"` // "infrastructure_cascading_failure"
}

type MLLayerPredictions struct {
    Infrastructure *LayerPrediction `json:"infrastructure,omitempty"`
    Platform       *LayerPrediction `json:"platform,omitempty"`
    Application    *LayerPrediction `json:"application,omitempty"`
    RootCauseSuggestion Layer       `json:"root_cause_suggestion"`
    Confidence     float64          `json:"confidence"`
    PredictedAt    time.Time        `json:"predicted_at"`
}

type LayerPrediction struct {
    Affected     bool     `json:"affected"`
    Probability  float64  `json:"probability"`
    Evidence     []string `json:"evidence,omitempty"` // ["high_disk_usage", "node_pressure"]
    IsRootCause  bool     `json:"is_root_cause"`
}
```

### ML Service API Extension

**New Pattern Analysis Request for Layer Detection**:
```json
POST /api/v1/pattern/analyze
{
  "historical_data": {
    "description": "node disk pressure causing pod evictions",
    "resources": [
      {"kind": "Node", "name": "worker-1", "issue": "DiskPressure"},
      {"kind": "Pod", "name": "my-app", "namespace": "default", "issue": "Evicted"}
    ],
    "recent_events": ["DiskPressure", "Evicted", "PodEviction"],
    "time_range": "1h"
  },
  "analysis_type": "layer_detection"
}

Response 200 OK:
{
  "patterns": [
    {
      "pattern": "infrastructure_cascading_failure",
      "confidence": 0.92,
      "description": "Infrastructure issue causing downstream application failures"
    }
  ],
  "layer_predictions": {
    "infrastructure": {
      "affected": true,
      "probability": 0.95,
      "evidence": ["node_disk_pressure", "mco_related", "kernel_event"],
      "is_root_cause": true
    },
    "platform": {
      "affected": false,
      "probability": 0.15
    },
    "application": {
      "affected": true,
      "probability": 0.88,
      "evidence": ["pod_eviction", "container_killed"],
      "is_root_cause": false
    }
  },
  "suggested_root_cause": "infrastructure",
  "overall_confidence": 0.92
}
```

## Implementation

### 1. Enhanced LayerDetector

**File**: `internal/coordination/ml_layer_detector.go` (NEW)

```go
package coordination

import (
    "context"
    "time"

    "github.com/sirupsen/logrus"
    "openshift-coordination-engine/internal/integrations"
    "openshift-coordination-engine/pkg/models"
)

// MLLayerDetector enhances layer detection with ML predictions
type MLLayerDetector struct {
    baseDetector *LayerDetector  // Keyword-based detector (fallback)
    mlClient     *integrations.MLClient
    enableML     bool
    timeout      time.Duration
    log          *logrus.Logger
}

func NewMLLayerDetector(mlClient *integrations.MLClient, log *logrus.Logger) *MLLayerDetector {
    return &MLLayerDetector{
        baseDetector: NewLayerDetector(log),
        mlClient:     mlClient,
        enableML:     mlClient != nil,
        timeout:      5 * time.Second,
        log:          log,
    }
}

// DetectLayersWithML performs ML-enhanced layer detection
func (mld *MLLayerDetector) DetectLayersWithML(ctx context.Context, issueID, issueDescription string, resources []models.Resource) *models.LayeredIssue {
    // 1. Start with keyword-based detection (fast path)
    layeredIssue := mld.baseDetector.DetectLayers(ctx, issueID, issueDescription, resources)
    layeredIssue.DetectionMethod = "keyword"

    // Set initial keyword-based confidence (0.70)
    layeredIssue.LayerConfidence = make(map[models.Layer]float64)
    for _, layer := range layeredIssue.AffectedLayers {
        layeredIssue.LayerConfidence[layer] = 0.70
    }

    // 2. If ML is disabled or unavailable, return keyword results
    if !mld.enableML {
        mld.log.Debug("ML detection disabled, using keyword-based results")
        return layeredIssue
    }

    // 3. Call ML service for pattern analysis
    mlCtx, cancel := context.WithTimeout(ctx, mld.timeout)
    defer cancel()

    mlPredictions, err := mld.getMLPredictions(mlCtx, issueDescription, resources)
    if err != nil {
        mld.log.WithError(err).Warn("ML prediction failed, using keyword-based results")
        return layeredIssue
    }

    // 4. Enhance with ML predictions
    mld.enhanceWithMLPredictions(layeredIssue, mlPredictions)
    layeredIssue.DetectionMethod = "ml_enhanced"

    mld.log.WithFields(logrus.Fields{
        "issue_id":        issueID,
        "detection":       "ml_enhanced",
        "ml_confidence":   mlPredictions.Confidence,
        "affected_layers": layeredIssue.AffectedLayers,
        "root_cause":      layeredIssue.RootCauseLayer,
    }).Info("ML-enhanced layer detection complete")

    return layeredIssue
}

// getMLPredictions calls ML service for layer predictions
func (mld *MLLayerDetector) getMLPredictions(ctx context.Context, description string, resources []models.Resource) (*models.MLLayerPredictions, error) {
    // Convert resources to ML format
    mlResources := make([]map[string]interface{}, len(resources))
    for i, r := range resources {
        mlResources[i] = map[string]interface{}{
            "kind":      r.Kind,
            "name":      r.Name,
            "namespace": r.Namespace,
            "issue":     r.Issue,
        }
    }

    // Call pattern analysis API
    req := &integrations.PatternAnalysisRequest{
        Metrics: []integrations.MetricData{}, // Empty for now, could add resource metrics
        TimeRange: struct {
            Start time.Time `json:"start"`
            End   time.Time `json:"end"`
        }{
            Start: time.Now().Add(-1 * time.Hour),
            End:   time.Now(),
        },
        AnalysisType: "layer_detection",
    }

    resp, err := mld.mlClient.AnalyzePatterns(ctx, req)
    if err != nil {
        return nil, err
    }

    // Parse response into ML predictions
    return mld.parseMLResponse(resp), nil
}

// enhanceWithMLPredictions merges ML predictions with keyword-based results
func (mld *MLLayerDetector) enhanceWithMLPredictions(issue *models.LayeredIssue, mlPred *models.MLLayerPredictions) {
    issue.MLPredictions = mlPred

    // Update affected layers based on ML probabilities
    if mlPred.Infrastructure != nil && mlPred.Infrastructure.Affected && mlPred.Infrastructure.Probability > 0.75 {
        issue.AddAffectedLayer(models.LayerInfrastructure)
        issue.LayerConfidence[models.LayerInfrastructure] = mlPred.Infrastructure.Probability
    }

    if mlPred.Platform != nil && mlPred.Platform.Affected && mlPred.Platform.Probability > 0.75 {
        issue.AddAffectedLayer(models.LayerPlatform)
        issue.LayerConfidence[models.LayerPlatform] = mlPred.Platform.Probability
    }

    if mlPred.Application != nil && mlPred.Application.Affected && mlPred.Application.Probability > 0.75 {
        issue.AddAffectedLayer(models.LayerApplication)
        issue.LayerConfidence[models.LayerApplication] = mlPred.Application.Probability
    }

    // Use ML-suggested root cause if confidence is high
    if mlPred.Confidence > 0.85 {
        issue.RootCauseLayer = mlPred.RootCauseSuggestion
        mld.log.WithFields(logrus.Fields{
            "ml_suggestion": mlPred.RootCauseSuggestion,
            "confidence":    mlPred.Confidence,
        }).Info("Using ML-suggested root cause")
    }
}
```

### 2. Metrics Extension

**File**: `internal/coordination/metrics.go` (ADD)

```go
var (
    // ML-enhanced detection metrics
    MLLayerDetectionTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "coordination_engine_ml_layer_detection_total",
            Help: "Total ML-enhanced layer detections",
        },
        []string{"success", "ml_available"},
    )

    MLLayerConfidence = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "coordination_engine_ml_layer_confidence",
            Help:    "ML prediction confidence for layer detection",
            Buckets: []float64{0.5, 0.6, 0.7, 0.8, 0.9, 0.95, 0.99},
        },
        []string{"layer"},
    )

    MLDetectionDuration = promauto.NewHistogram(
        prometheus.HistogramOpts{
            Name:    "coordination_engine_ml_detection_duration_seconds",
            Help:    "Duration of ML prediction calls",
            Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0},
        },
    )
)
```

### 3. Main Integration

**File**: `cmd/coordination-engine/main.go` (UPDATE)

```go
// Initialize ML-enhanced layer detector
var layerDetector coordination.LayerDetectorInterface
if cfg.MLServiceURL != "" && cfg.EnableMLLayerDetection {
    mlLayerDetector := coordination.NewMLLayerDetector(mlClient, log)
    layerDetector = mlLayerDetector
    log.Info("ML-enhanced layer detector initialized")
} else {
    layerDetector = coordination.NewLayerDetector(log)
    log.Info("Keyword-based layer detector initialized")
}
```

## Configuration

Environment variables:
```bash
ENABLE_ML_LAYER_DETECTION=true        # Enable ML-enhanced detection (default: true if ML_SERVICE_URL set)
ML_LAYER_DETECTION_TIMEOUT=5s         # Timeout for ML predictions (default: 5s)
ML_LAYER_CONFIDENCE_THRESHOLD=0.75    # Minimum probability to mark layer as affected
ML_ROOT_CAUSE_CONFIDENCE=0.85         # Minimum confidence to use ML-suggested root cause
```

## Testing Strategy

1. **Unit Tests**: Mock ML client, test confidence calculation, test graceful degradation
2. **Integration Tests**: Real ML service, validate prediction accuracy
3. **A/B Testing**: Compare keyword vs ML-enhanced detection accuracy
4. **Performance Tests**: Measure latency impact (<100ms target)

## Consequences

### Positive
- ✅ Higher accuracy for layer detection (ML learns from historical patterns)
- ✅ Confidence scores enable data-driven decision making
- ✅ Identifies novel multi-layer patterns that keywords miss
- ✅ Backward compatible (graceful degradation to keyword-based)
- ✅ Metrics provide visibility into ML vs keyword accuracy

### Negative
- ⚠️ Adds latency (5s timeout, ~100-500ms typical)
- ⚠️ Dependency on ML service availability
- ⚠️ Requires ML service to support layer detection analysis
- ⚠️ More complex testing (ML predictions are probabilistic)

### Mitigation Strategies
- **Latency**: Short timeout (5s), async pattern analysis where possible
- **Availability**: Graceful degradation to keyword-based, circuit breaker
- **ML Service Updates**: Versioned API contract, feature flags for rollout
- **Testing**: Confidence thresholds, comparison with keyword baseline

## Rollout Plan

1. **Phase 6.1**: Implement ML-enhanced detector with feature flag OFF
2. **Phase 6.2**: Enable for 10% of traffic, A/B test vs keyword
3. **Phase 6.3**: Analyze metrics, tune confidence thresholds
4. **Phase 6.4**: Enable for 100% if accuracy improvement > 10%
5. **Phase 6.5**: Deprecate keyword-only path (keep as fallback)

## Success Metrics

- **Accuracy**: ML-enhanced detection matches actual root cause >90% (vs 75% keyword baseline)
- **Latency**: P95 < 500ms for ML prediction calls
- **Availability**: Graceful degradation works 100% of time when ML unavailable
- **Confidence**: Average ML confidence > 0.85 for multi-layer issues

## References

- ADR-009: Python ML Service Integration (base ML client)
- ADR-003: Multi-Layer Coordination Implementation (Phase 3 keyword detector)
- Platform ADR-040: Multi-Layer Coordination (original design)
- ML Service API: `/home/lab-user/openshift-aiops-platform/ml-service/api/v1/`

## Related ADRs

- ADR-003: Multi-Layer Coordination Implementation (keyword detector)
- ADR-009: Python ML Service Integration (ML client foundation)
- ADR-014: Prometheus/Thanos Observability Integration (real metrics improve ML confidence 0.75 → 0.90)
