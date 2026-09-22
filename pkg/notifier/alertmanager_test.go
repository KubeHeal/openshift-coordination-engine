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

func TestAlertmanagerSink_Name(t *testing.T) {
	sink := NewAlertmanagerSink("http://alertmanager:9093", http.DefaultClient, logrus.New())
	assert.Equal(t, "alertmanager", sink.Name())
}

func TestAlertmanagerSink_Send_Success(t *testing.T) {
	var receivedAlerts []amAlert
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v2/alerts", r.URL.Path)

		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedAlerts))

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	sink := NewAlertmanagerSink(server.URL, server.Client(), log)

	err := sink.Send(context.Background(), newTestAlert())
	require.NoError(t, err)

	require.Len(t, receivedAlerts, 1)
	alert := receivedAlerts[0]

	assert.Equal(t, "KubeHealAnomaly", alert.Labels["alertname"])
	assert.Equal(t, "critical", alert.Labels["severity"])
	assert.Equal(t, "production", alert.Labels["namespace"])
	assert.Equal(t, "my-app", alert.Labels["service"])
	assert.Equal(t, "kubeheal-coordination-engine", alert.Labels["source"])
	assert.Contains(t, alert.Annotations["summary"], "0.95")
	assert.Equal(t, "CPU and memory elevated", alert.Annotations["description"])
	assert.Equal(t, "immediate_investigation", alert.Annotations["recommendation"])
	assert.NotEmpty(t, alert.StartsAt)
}

func TestAlertmanagerSink_Send_WithCustomLabels(t *testing.T) {
	var receivedAlerts []amAlert
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &receivedAlerts))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	sink := NewAlertmanagerSink(server.URL, server.Client(), log)

	alert := newTestAlert()
	alert.Labels["team"] = "sre"
	alert.Labels["env"] = "prod"

	err := sink.Send(context.Background(), alert)
	require.NoError(t, err)

	require.Len(t, receivedAlerts, 1)
	assert.Equal(t, "sre", receivedAlerts[0].Labels["team"])
	assert.Equal(t, "prod", receivedAlerts[0].Labels["env"])
}

func TestAlertmanagerSink_Send_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("bad gateway"))
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	sink := NewAlertmanagerSink(server.URL, server.Client(), log)

	err := sink.Send(context.Background(), newTestAlert())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
}

func TestAlertmanagerSink_Send_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	sink := NewAlertmanagerSink(server.URL, server.Client(), log)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := sink.Send(ctx, newTestAlert())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send alertmanager alert")
}

func TestAlertmanagerSink_TrailingSlashTrimmed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v2/alerts", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	sink := NewAlertmanagerSink(server.URL+"/", server.Client(), log)

	err := sink.Send(context.Background(), newTestAlert())
	require.NoError(t, err)
}
