package v1

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/KubeHeal/openshift-coordination-engine/internal/rca"
)

func newTestRCAHandler() *RCAHandler {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	clientset := fake.NewSimpleClientset()
	return NewRCAHandler(clientset, nil, clientset.Discovery(), log)
}

func TestRCAHandler_RegisterRoutes(t *testing.T) {
	handler := newTestRCAHandler()
	router := mux.NewRouter()
	handler.RegisterRoutes(router)

	req := httptest.NewRequest("POST", "/api/v1/investigate/rca", http.NoBody)
	match := &mux.RouteMatch{}
	assert.True(t, router.Match(req, match), "route should be registered")
}

func TestRCAHandler_Validation_MissingService(t *testing.T) {
	handler := newTestRCAHandler()
	body := `{"namespace":"default","start_time":"2026-09-22T10:00:00Z","end_time":"2026-09-22T11:00:00Z"}`
	req := httptest.NewRequest("POST", "/api/v1/investigate/rca", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.InvestigateRCA(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp RCAErrorResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp.Error, "service is required")
	assert.Equal(t, rcaErrInvalidRequest, resp.Code)
}

func TestRCAHandler_Validation_MissingNamespace(t *testing.T) {
	handler := newTestRCAHandler()
	body := `{"service":"myapp","start_time":"2026-09-22T10:00:00Z","end_time":"2026-09-22T11:00:00Z"}`
	req := httptest.NewRequest("POST", "/api/v1/investigate/rca", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.InvestigateRCA(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var resp RCAErrorResponse
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Contains(t, resp.Error, "namespace is required")
}

func TestRCAHandler_Validation_BadTimeRange(t *testing.T) {
	handler := newTestRCAHandler()

	t.Run("end before start", func(t *testing.T) {
		body := `{"service":"myapp","namespace":"default","start_time":"2026-09-22T11:00:00Z","end_time":"2026-09-22T10:00:00Z"}`
		req := httptest.NewRequest("POST", "/api/v1/investigate/rca", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.InvestigateRCA(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp RCAErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Contains(t, resp.Error, "end_time must be after start_time")
	})

	t.Run("exceeds 7 days", func(t *testing.T) {
		body := `{"service":"myapp","namespace":"default","start_time":"2026-09-01T00:00:00Z","end_time":"2026-09-22T10:00:00Z"}`
		req := httptest.NewRequest("POST", "/api/v1/investigate/rca", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.InvestigateRCA(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp RCAErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Contains(t, resp.Error, "time range must not exceed 7 days")
	})

	t.Run("invalid RFC3339", func(t *testing.T) {
		body := `{"service":"myapp","namespace":"default","start_time":"not-a-date","end_time":"2026-09-22T10:00:00Z"}`
		req := httptest.NewRequest("POST", "/api/v1/investigate/rca", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		handler.InvestigateRCA(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		var resp RCAErrorResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		assert.Contains(t, resp.Error, "start_time must be RFC3339 format")
	})
}

func TestRCAHandler_Validation_InvalidJSON(t *testing.T) {
	handler := newTestRCAHandler()
	body := `{invalid}`
	req := httptest.NewRequest("POST", "/api/v1/investigate/rca", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.InvestigateRCA(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRCAHandler_Validation_WrongContentType(t *testing.T) {
	handler := newTestRCAHandler()
	body := `<xml/>`
	req := httptest.NewRequest("POST", "/api/v1/investigate/rca", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()

	handler.InvestigateRCA(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRCAHandler_SuccessEmpty(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)
	clientset := fake.NewSimpleClientset()
	handler := NewRCAHandler(clientset, nil, clientset.Discovery(), log)

	body := `{"service":"myapp","namespace":"default","start_time":"2026-09-22T10:00:00Z","end_time":"2026-09-22T11:00:00Z"}`
	req := httptest.NewRequest("POST", "/api/v1/investigate/rca", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.InvestigateRCA(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp rca.Result
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "success", resp.Status)
	assert.Equal(t, "myapp", resp.Service)
	assert.Equal(t, "default", resp.Namespace)
	assert.False(t, resp.IstioAvailable)
	assert.Empty(t, resp.RootCauses)
	assert.Equal(t, float64(0), resp.ConfidenceScore)
}

func TestRCAHandler_WithEvents(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-abc123.warning",
			Namespace: "default",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      "Pod",
			Name:      "myapp-abc123",
			Namespace: "default",
		},
		Reason:  "OOMKilled",
		Message: "Container exceeded memory limit",
		Type:    "Warning",
		Count:   3,
		LastTimestamp: metav1.Time{
			Time: mustParseTime("2026-09-22T10:30:00Z"),
		},
	}

	clientset := fake.NewSimpleClientset(event)
	handler := NewRCAHandler(clientset, nil, clientset.Discovery(), log)

	body := `{"service":"myapp","namespace":"default","start_time":"2026-09-22T10:00:00Z","end_time":"2026-09-22T11:00:00Z"}`
	req := httptest.NewRequest("POST", "/api/v1/investigate/rca", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.InvestigateRCA(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp rca.Result
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "success", resp.Status)
	require.Len(t, resp.RootCauses, 1)
	assert.Equal(t, rca.SignalPodEvent, resp.RootCauses[0].SignalType)
	assert.Contains(t, resp.RootCauses[0].Description, "OOMKilled")
	assert.Greater(t, resp.ConfidenceScore, float64(0))
}

func TestRCAHandler_WithNetworkPolicy(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myapp-abc123",
			Namespace: "default",
			Labels:    map[string]string{"app": "myapp"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	netpol := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deny-all",
			Namespace: "default",
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "myapp"},
			},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			Ingress:     []networkingv1.NetworkPolicyIngressRule{},
		},
	}

	clientset := fake.NewSimpleClientset(pod, netpol)
	handler := NewRCAHandler(clientset, nil, clientset.Discovery(), log)

	body := `{"service":"myapp","namespace":"default","start_time":"2026-09-22T10:00:00Z","end_time":"2026-09-22T11:00:00Z"}`
	req := httptest.NewRequest("POST", "/api/v1/investigate/rca", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	handler.InvestigateRCA(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp rca.Result
	err := json.NewDecoder(w.Body).Decode(&resp)
	require.NoError(t, err)
	assert.Equal(t, "success", resp.Status)

	hasNetpol := false
	for _, rc := range resp.RootCauses {
		if rc.SignalType == rca.SignalNetworkPolicy {
			hasNetpol = true
			assert.Contains(t, rc.Description, "deny-all")
			assert.Contains(t, rc.Description, "denies all ingress")
		}
	}
	assert.True(t, hasNetpol, "expected at least one NetworkPolicy finding")
}

func mustParseTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
