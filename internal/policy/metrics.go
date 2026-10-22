package policy

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	alertsReceived = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_alerts_received_total",
			Help: "Alertmanager alerts observed by the self-healing policy engine",
		},
		[]string{"policy", "result"},
	)
	modelScores = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_model_scores_total",
			Help: "Model verdicts scored for a matched self-healing policy",
		},
		[]string{"policy", "verdict"},
	)
	repairsExecuted = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "policy_engine_repairs_executed_total",
			Help: "Repairs attempted after an anomaly verdict",
		},
		[]string{"policy", "action", "result"},
	)
)

func recordAlertOutcome(outcome *alertOutcome) {
	alertsReceived.WithLabelValues(outcome.policyLabel(), outcome.result).Inc()
	if outcome.modelCalled {
		modelScores.WithLabelValues(outcome.policyLabel(), outcome.verdictLabel()).Inc()
	}
	if !outcome.repairAttempted {
		return
	}
	result := "error"
	if outcome.repairOK {
		result = "success"
	}
	repairsExecuted.WithLabelValues(outcome.policyLabel(), outcome.action, result).Inc()
}
