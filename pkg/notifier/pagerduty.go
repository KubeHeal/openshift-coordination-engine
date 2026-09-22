package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/sirupsen/logrus"
)

const pagerDutyEventsURL = "https://events.pagerduty.com/v2/enqueue"

// PagerDutySink creates PagerDuty incidents via Events API v2.
type PagerDutySink struct {
	routingKey string
	eventsURL  string // overridable for testing
	client     *http.Client
	log        *logrus.Logger
}

// NewPagerDutySink creates a new PagerDuty Events API v2 sink.
func NewPagerDutySink(routingKey string, client *http.Client, log *logrus.Logger) *PagerDutySink {
	return &PagerDutySink{
		routingKey: routingKey,
		eventsURL:  pagerDutyEventsURL,
		client:     client,
		log:        log,
	}
}

// Name returns the sink identifier.
func (p *PagerDutySink) Name() string { return "pagerduty" }

// SetEventsURL overrides the PagerDuty events endpoint (for testing).
func (p *PagerDutySink) SetEventsURL(url string) { p.eventsURL = url }

// Send creates a PagerDuty event for the alert.
func (p *PagerDutySink) Send(ctx context.Context, alert *Alert) error {
	payload := p.buildPayload(alert)

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal pagerduty payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.eventsURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create pagerduty request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("send pagerduty event: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			p.log.WithError(closeErr).Debug("Failed to close PagerDuty response body")
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("pagerduty events API returned %d (body unreadable)", resp.StatusCode)
		}
		return fmt.Errorf("pagerduty events API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

type pdEvent struct {
	RoutingKey  string    `json:"routing_key"`
	EventAction string    `json:"event_action"`
	Payload     pdPayload `json:"payload"`
}

type pdPayload struct {
	Summary       string                 `json:"summary"`
	Severity      string                 `json:"severity"`
	Source        string                 `json:"source"`
	Component     string                 `json:"component"`
	Timestamp     string                 `json:"timestamp,omitempty"`
	CustomDetails map[string]interface{} `json:"custom_details,omitempty"`
}

func (p *PagerDutySink) buildPayload(alert *Alert) pdEvent {
	return pdEvent{
		RoutingKey:  p.routingKey,
		EventAction: "trigger",
		Payload: pdPayload{
			Summary:   fmt.Sprintf("KubeHeal: %s anomaly (score %.2f) in %s/%s", alert.Severity, alert.AnomalyScore, alert.Namespace, alert.Service),
			Severity:  pdSeverity(alert.Severity),
			Source:    "kubeheal-coordination-engine",
			Component: "anomaly-detector",
			Timestamp: alert.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"),
			CustomDetails: map[string]interface{}{
				"anomaly_score":  alert.AnomalyScore,
				"namespace":      alert.Namespace,
				"service":        alert.Service,
				"recommendation": alert.Recommendation,
				"summary":        alert.Summary,
			},
		},
	}
}

// pdSeverity maps our severity to PagerDuty severity values.
func pdSeverity(severity string) string {
	switch severity {
	case "critical":
		return "critical"
	case "warning":
		return "warning"
	default:
		return "info"
	}
}
