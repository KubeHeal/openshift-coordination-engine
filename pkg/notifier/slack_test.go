package notifier

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestAlert() *Alert {
	return &Alert{
		Severity:       "critical",
		Service:        "my-app",
		Namespace:      "production",
		AnomalyScore:   0.95,
		Summary:        "CPU and memory elevated",
		Recommendation: "immediate_investigation",
		Timestamp:      time.Date(2026, 9, 22, 10, 30, 0, 0, time.UTC),
		Labels:         map[string]string{"scope": "cluster-wide"},
	}
}

func TestSlackSink_Name(t *testing.T) {
	sink := NewSlackSink("https://hooks.slack.com/test", http.DefaultClient, logrus.New())
	assert.Equal(t, "slack", sink.Name())
}

func TestSlackSink_Send_Success(t *testing.T) {
	var receivedBody slackPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, http.MethodPost, r.Method)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedBody))

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	sink := NewSlackSink(server.URL, server.Client(), log)

	err := sink.Send(context.Background(), newTestAlert())
	require.NoError(t, err)

	assert.Contains(t, receivedBody.Text, "critical")
	assert.Contains(t, receivedBody.Text, "production/my-app")
	require.Len(t, receivedBody.Attachments, 1)
	assert.Equal(t, "#FF0000", receivedBody.Attachments[0].Color)
	assert.Contains(t, receivedBody.Attachments[0].Title, "Critical")
}

func TestSlackSink_Send_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	sink := NewSlackSink(server.URL, server.Client(), log)

	err := sink.Send(context.Background(), newTestAlert())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestSlackSink_Send_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	sink := NewSlackSink(server.URL, server.Client(), log)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := sink.Send(ctx, newTestAlert())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send slack webhook")
}

func TestSlackSink_SeverityColors(t *testing.T) {
	assert.Equal(t, "#FF0000", severityColor("critical"))
	assert.Equal(t, "#FFA500", severityColor("warning"))
	assert.Equal(t, "#36A64F", severityColor("info"))
	assert.Equal(t, "#36A64F", severityColor("unknown"))
}
