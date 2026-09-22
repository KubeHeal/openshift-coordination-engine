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

// SlackSink posts alerts to a Slack incoming webhook URL.
type SlackSink struct {
	webhookURL string
	client     *http.Client
	log        *logrus.Logger
}

// NewSlackSink creates a new Slack webhook sink.
func NewSlackSink(webhookURL string, client *http.Client, log *logrus.Logger) *SlackSink {
	return &SlackSink{webhookURL: webhookURL, client: client, log: log}
}

// Name returns the sink identifier.
func (s *SlackSink) Name() string { return "slack" }

// Send posts the alert to the Slack webhook.
func (s *SlackSink) Send(ctx context.Context, alert *Alert) error {
	payload := s.buildPayload(alert)

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal slack payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create slack request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send slack webhook: %w", err)
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			s.log.WithError(closeErr).Debug("Failed to close Slack response body")
		}
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("slack webhook returned %d (body unreadable)", resp.StatusCode)
		}
		return fmt.Errorf("slack webhook returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

type slackPayload struct {
	Text        string            `json:"text"`
	Attachments []slackAttachment `json:"attachments,omitempty"`
}

type slackAttachment struct {
	Color  string       `json:"color"`
	Title  string       `json:"title"`
	Text   string       `json:"text"`
	Fields []slackField `json:"fields,omitempty"`
}

type slackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

func (s *SlackSink) buildPayload(alert *Alert) slackPayload {
	color := severityColor(alert.Severity)
	title := fmt.Sprintf("%s Anomaly — %s/%s", capitalize(alert.Severity), alert.Namespace, alert.Service)

	return slackPayload{
		Text: fmt.Sprintf("KubeHeal: %s anomaly detected in %s/%s", alert.Severity, alert.Namespace, alert.Service),
		Attachments: []slackAttachment{
			{
				Color: color,
				Title: title,
				Text:  fmt.Sprintf("%s\n*Recommendation:* %s", alert.Summary, alert.Recommendation),
				Fields: []slackField{
					{Title: "Score", Value: fmt.Sprintf("%.2f", alert.AnomalyScore), Short: true},
					{Title: "Severity", Value: alert.Severity, Short: true},
					{Title: "Namespace", Value: alert.Namespace, Short: true},
					{Title: "Service", Value: alert.Service, Short: true},
				},
			},
		},
	}
}

func severityColor(severity string) string {
	switch severity {
	case "critical":
		return "#FF0000"
	case "warning":
		return "#FFA500"
	default:
		return "#36A64F"
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return string(s[0]-32) + s[1:]
}
