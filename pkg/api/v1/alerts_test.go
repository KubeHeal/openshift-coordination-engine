package v1

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/KubeHeal/openshift-coordination-engine/internal/policy"
)

func TestPostAlerts_UnmatchedReturnsZero(t *testing.T) {
	handler := testAlertsHandler(t)
	body := bytes.NewBufferString(`{
      "version": "4",
      "status": "firing",
      "alerts": [{
        "status": "firing",
        "labels": {"alertname": "OtherAlert", "namespace": "checkout"}
      }]
    }`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.PostAlerts(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{
      "status": "processed",
      "matched_policies": 0,
      "actions_taken": []
    }`, rec.Body.String())
}

func TestPostAlerts_InvalidJSON(t *testing.T) {
	handler := testAlertsHandler(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", bytes.NewBufferString("{"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.PostAlerts(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "Invalid Alertmanager webhook format")
}

func TestPostAlerts_FiringAlertUsesPolicyModel(t *testing.T) {
	handler := testAlertsHandler(t)
	body := bytes.NewBufferString(`{
      "version": "4",
      "status": "firing",
      "receiver": "coordination-engine",
      "alerts": [{
        "status": "firing",
        "labels": {"alertname": "CheckoutQueueLag", "namespace": "checkout", "pod": "checkout-api-0"},
        "annotations": {"summary": "queue lag"},
        "startsAt": "2026-10-21T20:00:00Z",
        "fingerprint": "fp"
      }]
    }`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/alerts", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router := mux.NewRouter()
	handler.RegisterRoutes(router)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"matched_policies":1`)
	require.Contains(t, rec.Body.String(), `"policy":"checkout"`)
	require.Contains(t, rec.Body.String(), `"verdict":1`)
	require.Contains(t, rec.Body.String(), `"result":"no_action"`)
}

type fixedScorer struct{}

func (fixedScorer) Score(context.Context, string, []float64) (int, error) { return 1, nil }

type fixedQuerier struct{}

func (fixedQuerier) Query(context.Context, string) (float64, error) { return 1, nil }

func testAlertsHandler(t *testing.T) *AlertsHandler {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	raw := []byte(`{
      "actionTypes": ["scale_deployment"],
      "policies": [{
        "name": "checkout",
        "namespace": "checkout",
        "alerts": ["CheckoutQueueLag"],
        "model": {"name": "checkout-detector", "featureColumns": ["cpu_usage"]},
        "repairs": [{"alert": "CheckoutQueueLag", "action": "scale_deployment", "params": {"deployment": "checkout-api", "replicas": 2}}]
      }]
    }`)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	log := logrus.New()
	log.SetOutput(io.Discard)
	loader, err := policy.NewLoader(path, log)
	require.NoError(t, err)
	executor, err := policy.NewActionExecutor(
		k8sfake.NewSimpleClientset(),
		dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()),
		meta.NewDefaultRESTMapper([]schema.GroupVersion{{Version: "v1"}}),
		nil,
		log,
	)
	require.NoError(t, err)
	engine, err := policy.NewEngine(policy.EngineConfig{
		Loader:         loader,
		Querier:        fixedQuerier{},
		Scorer:         fixedScorer{},
		Actions:        executor,
		ModelNamespace: "self-healing-platform",
		ModelPort:      8080,
		Log:            log,
	})
	require.NoError(t, err)
	return NewAlertsHandler(engine, log)
}
