package notifier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/sirupsen/logrus"
)

// AlertmanagerSink posts alerts to Prometheus Alertmanager via its HTTP API.
type AlertmanagerSink struct {
	baseURL string
	client  *http.Client
	log     *logrus.Logger
}

// NewAlertmanagerSink creates a new Alertmanager API sink.
func NewAlertmanagerSink(baseURL string, client *http.Client, log *logrus.Logger) *AlertmanagerSink {
	return &AlertmanagerSink{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  client,
		log:     log,
	}
}

// Name returns the sink identifier.
func (a *AlertmanagerSink) Name() string { return "alertmanager" }

// Send posts the alert to the Alertmanager API.
func (a *AlertmanagerSink) Send(ctx context.Context, alert *Alert) error {
	amAlerts := a.buildPayload(alert)

	body, err := json.Marshal(amAlerts)
	if err != nil {
		return fmt.Errorf("marshal alertmanager payload: %w", err)
	}

	url := a.baseURL + "/api/v2/alerts"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create alertmanager request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("send alertmanager alert: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			a.log.WithError(closeErr).Debug("Failed to close Alertmanager response body")
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("alertmanager API returned %d (body unreadable)", resp.StatusCode)
		}
		return fmt.Errorf("alertmanager API returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

type amAlert struct {
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt,omitempty"`
}

func (a *AlertmanagerSink) buildPayload(alert *Alert) []amAlert {
	labels := map[string]string{
		"alertname": "KubeHealAnomaly",
		"severity":  alert.Severity,
		"namespace": alert.Namespace,
		"service":   alert.Service,
		"source":    "kubeheal-coordination-engine",
	}
	for k, v := range alert.Labels {
		labels[k] = v
	}

	annotations := map[string]string{
		"summary":        fmt.Sprintf("Critical anomaly detected (score %.2f) in %s/%s", alert.AnomalyScore, alert.Namespace, alert.Service),
		"description":    alert.Summary,
		"recommendation": alert.Recommendation,
	}

	return []amAlert{
		{
			Labels:      labels,
			Annotations: annotations,
			StartsAt:    alert.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z"),
		},
	}
}
