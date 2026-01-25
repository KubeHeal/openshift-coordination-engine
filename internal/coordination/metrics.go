package coordination

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

var (
	// LayerDetectionTotal counts total layer detection attempts
	LayerDetectionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_layer_detection_total",
			Help: "Total number of layer detection attempts",
		},
		[]string{"detected_layer", "multi_layer"},
	)

	// PlanGenerationTotal counts total plan generation attempts
	PlanGenerationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_plan_generation_total",
			Help: "Total number of remediation plan generations",
		},
		[]string{"layers_count", "status"},
	)

	// PlanGenerationDuration tracks time taken to generate a plan
	PlanGenerationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_plan_generation_duration_seconds",
			Help:    "Time taken to generate remediation plan",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10}, // 100ms to 10s
		},
		[]string{"layers_count"},
	)

	// PlanExecutionTotal counts total plan execution attempts
	PlanExecutionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_plan_execution_total",
			Help: "Total number of plan execution attempts",
		},
		[]string{"status", "layers_count"},
	)

	// PlanExecutionDuration tracks time taken to execute a plan
	PlanExecutionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_plan_execution_duration_seconds",
			Help:    "Time taken to execute remediation plan",
			Buckets: []float64{10, 30, 60, 120, 300, 600, 1200}, // 10s to 20min
		},
		[]string{"status", "layers_count"},
	)

	// PlanStepsTotal tracks total steps in plans
	PlanStepsTotal = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_plan_steps_total",
			Help:    "Total number of steps in remediation plans",
			Buckets: []float64{1, 3, 5, 10, 15, 20, 30},
		},
		[]string{"layers_count"},
	)

	// HealthCheckpointTotal counts health checkpoint executions
	HealthCheckpointTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_health_checkpoint_total",
			Help: "Total number of health checkpoint executions",
		},
		[]string{"layer", "status"},
	)

	// HealthCheckpointDuration tracks time taken for health checks
	HealthCheckpointDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_health_checkpoint_duration_seconds",
			Help:    "Time taken to execute health checkpoint",
			Buckets: []float64{1, 5, 10, 30, 60, 120}, // 1s to 2min
		},
		[]string{"layer", "status"},
	)

	// RollbackTotal counts total rollback executions
	RollbackTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_rollback_total",
			Help: "Total number of rollback executions",
		},
		[]string{"trigger_reason", "steps_rolled_back"},
	)

	// RollbackDuration tracks time taken for rollback
	RollbackDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_rollback_duration_seconds",
			Help:    "Time taken to execute rollback",
			Buckets: []float64{5, 10, 30, 60, 120, 300}, // 5s to 5min
		},
	)

	// MultiLayerIssuesTotal counts issues by affected layers
	MultiLayerIssuesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_multi_layer_issues_total",
			Help: "Total number of multi-layer issues detected",
		},
		[]string{"layers_count", "root_cause_layer"},
	)

	// PlansActiveGauge tracks currently active plans
	PlansActiveGauge = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "coordination_engine_plans_active",
			Help: "Number of currently executing remediation plans",
		},
	)

	// LayerDetectionAccuracy tracks detection accuracy by layer
	LayerDetectionAccuracy = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "coordination_engine_layer_detection_accuracy",
			Help: "Accuracy of layer detection (0-1)",
		},
		[]string{"layer"},
	)

	// MLLayerDetectionTotal tracks ML-enhanced layer detection attempts (Phase 6)
	MLLayerDetectionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_ml_layer_detection_total",
			Help: "Total ML-enhanced layer detections",
		},
		[]string{"success", "ml_available"},
	)

	// MLLayerConfidenceHist records ML prediction confidence for layer detection
	MLLayerConfidenceHist = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_ml_layer_confidence",
			Help:    "ML prediction confidence for layer detection",
			Buckets: []float64{0.5, 0.6, 0.7, 0.75, 0.8, 0.85, 0.9, 0.95, 0.99},
		},
		[]string{"layer"},
	)

	// MLDetectionDurationHist records duration of ML prediction calls
	MLDetectionDurationHist = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_ml_detection_duration_seconds",
			Help:    "Duration of ML prediction calls",
			Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0},
		},
	)
)

// RecordLayerDetection records metrics for layer detection
func RecordLayerDetection(detectedLayer models.Layer, isMultiLayer bool) {
	multiLayerStr := "false"
	if isMultiLayer {
		multiLayerStr = "true"
	}
	LayerDetectionTotal.WithLabelValues(string(detectedLayer), multiLayerStr).Inc()
}

// RecordMultiLayerIssue records detection of multi-layer issue
func RecordMultiLayerIssue(layersCount int, rootCauseLayer models.Layer) {
	layersCountStr := formatLayersCount(layersCount)
	MultiLayerIssuesTotal.WithLabelValues(layersCountStr, string(rootCauseLayer)).Inc()
}

// RecordPlanGeneration records metrics for plan generation
func RecordPlanGeneration(layersCount int, duration float64, success bool) {
	layersCountStr := formatLayersCount(layersCount)
	status := "success"
	if !success {
		status = "failed"
	}

	PlanGenerationTotal.WithLabelValues(layersCountStr, status).Inc()
	PlanGenerationDuration.WithLabelValues(layersCountStr).Observe(duration)
}

// RecordPlanSteps records the number of steps in a plan
func RecordPlanSteps(layersCount, stepsCount int) {
	layersCountStr := formatLayersCount(layersCount)
	PlanStepsTotal.WithLabelValues(layersCountStr).Observe(float64(stepsCount))
}

// RecordPlanExecutionStart records the start of plan execution
func RecordPlanExecutionStart() {
	PlansActiveGauge.Inc()
}

// RecordPlanExecutionEnd records the end of plan execution
func RecordPlanExecutionEnd(status string, layersCount int, duration float64) {
	PlansActiveGauge.Dec()

	layersCountStr := formatLayersCount(layersCount)
	PlanExecutionTotal.WithLabelValues(status, layersCountStr).Inc()
	PlanExecutionDuration.WithLabelValues(status, layersCountStr).Observe(duration)
}

// RecordHealthCheckpoint records metrics for health checkpoint execution
func RecordHealthCheckpoint(layer models.Layer, duration float64, success bool) {
	status := "success"
	if !success {
		status = "failed"
	}

	HealthCheckpointTotal.WithLabelValues(string(layer), status).Inc()
	HealthCheckpointDuration.WithLabelValues(string(layer), status).Observe(duration)
}

// RecordRollback records metrics for rollback execution
func RecordRollback(triggerReason string, stepsRolledBack int, duration float64) {
	stepsStr := formatStepsCount(stepsRolledBack)
	RollbackTotal.WithLabelValues(triggerReason, stepsStr).Inc()
	RollbackDuration.Observe(duration)
}

// UpdateLayerDetectionAccuracy updates detection accuracy metric
func UpdateLayerDetectionAccuracy(layer models.Layer, accuracy float64) {
	LayerDetectionAccuracy.WithLabelValues(string(layer)).Set(accuracy)
}

// formatLayersCount formats layers count as string for metrics labels
func formatLayersCount(count int) string {
	switch count {
	case 1:
		return "1"
	case 2:
		return "2"
	case 3:
		return "3"
	default:
		return "3+"
	}
}

// formatStepsCount formats steps count as string for metrics labels
func formatStepsCount(count int) string {
	switch {
	case count <= 5:
		return "1-5"
	case count <= 10:
		return "6-10"
	case count <= 20:
		return "11-20"
	default:
		return "20+"
	}
}

// RecordMLLayerDetection records metrics for ML-enhanced layer detection (Phase 6)
func RecordMLLayerDetection(success, mlAvailable bool) {
	successStr := "false"
	if success {
		successStr = "true"
	}
	mlAvailableStr := "false"
	if mlAvailable {
		mlAvailableStr = "true"
	}
	MLLayerDetectionTotal.WithLabelValues(successStr, mlAvailableStr).Inc()
}

// RecordMLLayerConfidence records ML confidence for a specific layer (Phase 6)
func RecordMLLayerConfidence(layer models.Layer, confidence float64) {
	MLLayerConfidenceHist.WithLabelValues(string(layer)).Observe(confidence)
}

// RecordMLDetectionDuration records duration of ML prediction call (Phase 6)
func RecordMLDetectionDuration(duration float64) {
	MLDetectionDurationHist.Observe(duration)
}
