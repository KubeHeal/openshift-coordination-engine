// Package rca implements multi-signal root-cause analysis correlation (ADR-021).
package rca

import "time"

// SignalType identifies the source of a root-cause finding.
type SignalType string

// Signal type constants for the three correlators.
const (
	SignalPodEvent      SignalType = "pod_event"
	SignalNetworkPolicy SignalType = "network_policy"
	SignalIstioVS       SignalType = "istio_virtual_service"
)

// Request is the input accepted by the aggregator.
type Request struct {
	Service   string    `json:"service"`
	Namespace string    `json:"namespace"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

// Result is the aggregated output of all correlators.
type Result struct {
	Status             string      `json:"status"`
	Service            string      `json:"service"`
	Namespace          string      `json:"namespace"`
	TimeRange          TimeRange   `json:"time_range"`
	RootCauses         []RootCause `json:"root_causes"`
	ConfidenceScore    float64     `json:"confidence_score"`
	AffectedComponents []string    `json:"affected_components"`
	IstioAvailable     bool        `json:"istio_available"`
	CorrelatorStats    []CorrStat  `json:"correlator_stats,omitempty"`
}

// TimeRange is a start/end pair serialised into the response.
type TimeRange struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// RootCause is a single finding from any correlator.
type RootCause struct {
	SignalType       SignalType             `json:"signal_type"`
	Description      string                 `json:"description"`
	Evidence         map[string]interface{} `json:"evidence"`
	Confidence       float64                `json:"confidence"`
	RemediationSteps []string               `json:"remediation_steps"`
}

// CorrStat records per-correlator execution metadata.
type CorrStat struct {
	Name     string        `json:"name"`
	Findings int           `json:"findings"`
	Duration time.Duration `json:"duration_ms"`
	Error    string        `json:"error,omitempty"`
}

// Correlator is the interface each signal source must implement.
type Correlator interface {
	// Name returns a human-readable correlator identifier.
	Name() string
	// Correlate runs the analysis and returns zero or more root-cause findings.
	Correlate(req *Request) ([]RootCause, error)
}
