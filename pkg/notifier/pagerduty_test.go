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

func TestPagerDutySink_Name(t *testing.T) {
	sink := NewPagerDutySink("test-key", http.DefaultClient, logrus.New())
	assert.Equal(t, "pagerduty", sink.Name())
}

func TestPagerDutySink_Send_Success(t *testing.T) {
	var receivedEvent pdEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, http.MethodPost, r.Method)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedEvent))

		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"status":"success","dedup_key":"test123"}`))
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	sink := NewPagerDutySink("test-routing-key", server.Client(), log)
	sink.SetEventsURL(server.URL)

	err := sink.Send(context.Background(), newTestAlert())
	require.NoError(t, err)

	assert.Equal(t, "test-routing-key", receivedEvent.RoutingKey)
	assert.Equal(t, "trigger", receivedEvent.EventAction)
	assert.Equal(t, "critical", receivedEvent.Payload.Severity)
	assert.Equal(t, "kubeheal-coordination-engine", receivedEvent.Payload.Source)
	assert.Equal(t, "anomaly-detector", receivedEvent.Payload.Component)
	assert.Contains(t, receivedEvent.Payload.Summary, "critical")
	assert.Contains(t, receivedEvent.Payload.Summary, "production/my-app")
	assert.Equal(t, 0.95, receivedEvent.Payload.CustomDetails["anomaly_score"])
}

func TestPagerDutySink_Send_RateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	sink := NewPagerDutySink("test-key", server.Client(), log)
	sink.SetEventsURL(server.URL)

	err := sink.Send(context.Background(), newTestAlert())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "429")
}

func TestPagerDutySink_Send_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	sink := NewPagerDutySink("test-key", server.Client(), log)
	sink.SetEventsURL(server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := sink.Send(ctx, newTestAlert())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send pagerduty event")
}

func TestPagerDutySink_SeverityMapping(t *testing.T) {
	assert.Equal(t, "critical", pdSeverity("critical"))
	assert.Equal(t, "warning", pdSeverity("warning"))
	assert.Equal(t, "info", pdSeverity("info"))
	assert.Equal(t, "info", pdSeverity("unknown"))
}
