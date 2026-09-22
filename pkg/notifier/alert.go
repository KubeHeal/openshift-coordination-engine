// Package notifier provides pluggable alert sinks for critical anomaly
// notifications (ADR-022, Issue #75).
package notifier

import (
	"context"
	"time"
)

// Alert carries the data dispatched to all configured sinks.
type Alert struct {
	Severity       string            `json:"severity"`
	Service        string            `json:"service"`
	Namespace      string            `json:"namespace"`
	AnomalyScore   float64           `json:"anomaly_score"`
	Summary        string            `json:"summary"`
	Recommendation string            `json:"recommendation"`
	Timestamp      time.Time         `json:"timestamp"`
	Labels         map[string]string `json:"labels,omitempty"`
}

// AlertSink is the interface every notification backend must implement.
type AlertSink interface {
	// Name returns a human-readable identifier for logging.
	Name() string
	// Send delivers the alert to the external system.
	Send(ctx context.Context, alert *Alert) error
}
