package remediation

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// RemediationTotal counts total remediation attempts by remediator and deployment method
	RemediationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_remediation_total",
			Help: "Total number of remediation attempts",
		},
		[]string{"remediator", "deployment_method", "issue_type"},
	)

	// RemediationSuccess counts successful remediation completions
	RemediationSuccess = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_remediation_success_total",
			Help: "Total number of successful remediations",
		},
		[]string{"remediator", "deployment_method", "issue_type"},
	)

	// RemediationFailures counts failed remediation attempts
	RemediationFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_remediation_failures_total",
			Help: "Total number of failed remediations",
		},
		[]string{"remediator", "deployment_method", "issue_type", "error_type"},
	)

	// RemediationDuration tracks the time taken for remediation
	RemediationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_remediation_duration_seconds",
			Help:    "Time taken to complete remediation",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 300, 600}, // 1s to 10m
		},
		[]string{"remediator", "deployment_method", "status"},
	)

	// StrategySelectionTotal counts remediator selection by strategy
	StrategySelectionTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_strategy_selection_total",
			Help: "Total number of remediation strategy selections",
		},
		[]string{"strategy", "deployment_method", "selected"},
	)

	// RemediationSuccessRate tracks current success rate as a gauge
	RemediationSuccessRate = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "coordination_engine_remediation_success_rate",
			Help: "Current remediation success rate (0-1)",
		},
		[]string{"remediator", "deployment_method"},
	)

	// WorkflowsActive tracks currently active workflows
	WorkflowsActive = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "coordination_engine_workflows_active",
			Help: "Number of currently active remediation workflows",
		},
	)

	// WorkflowsTotal counts total workflow executions by status
	WorkflowsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "coordination_engine_workflows_total",
			Help: "Total number of remediation workflows",
		},
		[]string{"status"},
	)

	// WorkflowStepDuration tracks time for individual workflow steps
	WorkflowStepDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "coordination_engine_workflow_step_duration_seconds",
			Help:    "Time taken for individual workflow steps",
			Buckets: prometheus.DefBuckets, // 0.005s to 10s
		},
		[]string{"step_type", "status"},
	)

	// RemediatorHealthScore tracks health/availability of each remediator
	RemediatorHealthScore = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "coordination_engine_remediator_health_score",
			Help: "Health score of remediator (0-1, where 1 is healthy)",
		},
		[]string{"remediator"},
	)
)

// RecordRemediation records metrics for a remediation attempt
func RecordRemediation(remediator, deploymentMethod, issueType string, duration float64, success bool) {
	RemediationTotal.WithLabelValues(remediator, deploymentMethod, issueType).Inc()

	status := "success"
	if success {
		RemediationSuccess.WithLabelValues(remediator, deploymentMethod, issueType).Inc()
	} else {
		status = "failure"
	}

	RemediationDuration.WithLabelValues(remediator, deploymentMethod, status).Observe(duration)
}

// RecordRemediationFailure records a remediation failure with error type
func RecordRemediationFailure(remediator, deploymentMethod, issueType, errorType string) {
	RemediationFailures.WithLabelValues(remediator, deploymentMethod, issueType, errorType).Inc()
}

// RecordStrategySelection records a remediator selection
func RecordStrategySelection(strategy, deploymentMethod string, selected bool) {
	selectedStr := "false"
	if selected {
		selectedStr = "true"
	}
	StrategySelectionTotal.WithLabelValues(strategy, deploymentMethod, selectedStr).Inc()
}

// UpdateSuccessRate updates the success rate gauge
func UpdateSuccessRate(remediator, deploymentMethod string, rate float64) {
	RemediationSuccessRate.WithLabelValues(remediator, deploymentMethod).Set(rate)
}

// RecordWorkflowStart records the start of a workflow
func RecordWorkflowStart() {
	WorkflowsActive.Inc()
}

// RecordWorkflowEnd records the end of a workflow
func RecordWorkflowEnd(status string) {
	WorkflowsActive.Dec()
	WorkflowsTotal.WithLabelValues(status).Inc()
}

// RecordWorkflowStep records metrics for a workflow step
func RecordWorkflowStep(stepType, status string, duration float64) {
	WorkflowStepDuration.WithLabelValues(stepType, status).Observe(duration)
}

// UpdateRemediatorHealth updates the health score for a remediator
func UpdateRemediatorHealth(remediator string, healthScore float64) {
	RemediatorHealthScore.WithLabelValues(remediator).Set(healthScore)
}
