package policy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
)

func TestEngine_SecondApplicationIsConfiguredNotCompiled(t *testing.T) {
	client := k8sfake.NewSimpleClientset(
		deployment("parasol-insurance", "ic-app", "1", "ic-app:v1"),
		pod("parasol-insurance", "ic-app-aaa", "ic-app", corev1.PodRunning),
		deployment("checkout", "checkout-api", "1", "checkout:v1"),
	)
	querier := &scriptQuerier{}
	scorer := &recordingScorer{verdictFor: func(url string) int {
		if strings.Contains(url, "checkout-detector") {
			return -1
		}
		return 1
	}}
	var logs bytes.Buffer
	log := discardLogger()
	log.SetOutput(&logs)
	log.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true})
	engine := testEngine(t, twoApplicationPolicyJSON, client, querier, scorer, log)

	beforeRepair := testutil.ToFloat64(repairsExecuted.WithLabelValues("checkout", actionScaleDeployment, "success"))
	beforeParasol := testutil.ToFloat64(repairsExecuted.WithLabelValues("parasol-insurance", actionRestartPod, "success"))

	response := engine.HandleWebhook(context.Background(), Webhook{
		Status: "firing",
		Alerts: []IncomingAlert{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "ParasolOOMRisk",
					"namespace": "parasol-insurance",
				},
			},
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "CheckoutQueueLag",
					"namespace": "checkout",
					"pod":       "checkout-api-0",
				},
				StartsAt:    time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC),
				Fingerprint: "checkout-1",
			},
		},
	})

	require.Equal(t, 2, response.MatchedPolicies)
	require.Len(t, response.ActionsTaken, 2)
	require.Equal(t, resultNoAction, actionResult(response, "parasol-insurance"))
	require.Equal(t, resultSuccess, actionResult(response, "checkout"))
	require.Contains(t, logs.String(), "model says normal, no action")

	_, err := client.CoreV1().Pods("parasol-insurance").Get(context.Background(), "ic-app-aaa", metav1.GetOptions{})
	require.NoError(t, err)
	scaled, err := client.AppsV1().Deployments("checkout").Get(context.Background(), "checkout-api", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, int32(3), *scaled.Spec.Replicas)

	require.Len(t, scorer.calls, 2)
	parasolCall := callFor(t, scorer, "anomaly-detector")
	require.Equal(t, []float64{0.5, 0.6, 4, 0.2, 0.01}, parasolCall.features)
	require.Contains(t, parasolCall.url, "anomaly-detector-predictor.self-healing-platform.svc.cluster.local:8080")
	require.NotContains(t, parasolCall.url, "KSERVE_")

	checkoutCall := callFor(t, scorer, "checkout-detector")
	require.Equal(t, []float64{7}, checkoutCall.features)
	require.Contains(t, checkoutCall.url, "checkout-detector-predictor.checkout-ml.svc.cluster.local:8080")

	require.Contains(t, strings.Join(querier.queries, "\n"), `sum(checkout_queue_depth{namespace="checkout",pod="checkout-api-0"})`)
	require.Equal(t, beforeRepair+1, testutil.ToFloat64(repairsExecuted.WithLabelValues("checkout", actionScaleDeployment, "success")))
	require.Equal(t, beforeParasol, testutil.ToFloat64(repairsExecuted.WithLabelValues("parasol-insurance", actionRestartPod, "success")))
}

func TestEngine_AnomalyVerdictRepairsOnlyMatchingAlert(t *testing.T) {
	client := k8sfake.NewSimpleClientset(
		deployment("parasol-insurance", "ic-app", "1", "ic-app:v1"),
		pod("parasol-insurance", "ic-app-aaa", "ic-app", corev1.PodRunning),
		deployment("checkout", "checkout-api", "1", "checkout:v1"),
	)
	scorer := &recordingScorer{verdictFor: func(url string) int {
		if strings.Contains(url, "anomaly-detector") {
			return -1
		}
		return 1
	}}
	engine := testEngine(t, twoApplicationPolicyJSON, client, &scriptQuerier{}, scorer, discardLogger())

	response := engine.HandleWebhook(context.Background(), Webhook{Status: "firing", Alerts: []IncomingAlert{
		{Status: "firing", Labels: map[string]string{"alertname": "ParasolOOMRisk", "namespace": "parasol-insurance"}, Fingerprint: "p", StartsAt: time.Now()},
		{Status: "firing", Labels: map[string]string{"alertname": "CheckoutQueueLag", "namespace": "checkout", "pod": "checkout-api-0"}, Fingerprint: "c", StartsAt: time.Now()},
	}})

	require.Equal(t, resultSuccess, actionResult(response, "parasol-insurance"))
	require.Equal(t, resultNoAction, actionResult(response, "checkout"))
	_, err := client.CoreV1().Pods("parasol-insurance").Get(context.Background(), "ic-app-aaa", metav1.GetOptions{})
	require.Error(t, err)
	untouched, err := client.AppsV1().Deployments("checkout").Get(context.Background(), "checkout-api", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, int32(1), *untouched.Spec.Replicas)
}

func TestEngine_UnmatchedAndResolvedDoNotCallKubernetes(t *testing.T) {
	client := k8sfake.NewSimpleClientset(pod("checkout", "keep", "checkout-api", corev1.PodRunning))
	scorer := &recordingScorer{verdictFor: func(string) int { return -1 }}
	engine := testEngine(t, twoApplicationPolicyJSON, client, &scriptQuerier{}, scorer, discardLogger())

	unmatched := engine.HandleWebhook(context.Background(), Webhook{Status: "firing", Alerts: []IncomingAlert{
		{Status: "firing", Labels: map[string]string{"alertname": "OtherAlert", "namespace": "checkout"}},
	}})
	require.Equal(t, 0, unmatched.MatchedPolicies)
	require.Empty(t, unmatched.ActionsTaken)

	resolved := engine.HandleWebhook(context.Background(), Webhook{Status: "resolved", Alerts: []IncomingAlert{
		{Status: "resolved", Labels: map[string]string{"alertname": "CheckoutQueueLag", "namespace": "checkout"}},
	}})
	require.Equal(t, 0, resolved.MatchedPolicies)
	require.Empty(t, scorer.calls)

	_, err := client.CoreV1().Pods("checkout").Get(context.Background(), "keep", metav1.GetOptions{})
	require.NoError(t, err)
}

func TestEngine_KubernetesNamespaceLabelFallback(t *testing.T) {
	client := k8sfake.NewSimpleClientset(deployment("checkout", "checkout-api", "1", "checkout:v1"))
	scorer := &recordingScorer{verdictFor: func(string) int { return -1 }}
	engine := testEngine(t, twoApplicationPolicyJSON, client, &scriptQuerier{}, scorer, discardLogger())

	response := engine.HandleWebhook(context.Background(), Webhook{Status: "firing", Alerts: []IncomingAlert{
		{Status: "firing", Labels: map[string]string{"alertname": "CheckoutQueueLag", "kubernetes_namespace": "checkout", "pod": "checkout-api-0"}},
	}})
	require.Equal(t, 1, response.MatchedPolicies)
	require.Equal(t, resultSuccess, response.ActionsTaken[0].Result)
}

func TestEngine_UnknownFeatureDoesNotRepair(t *testing.T) {
	raw := `{
      "actionTypes": ["restart_pod"],
      "policies": [{
        "name": "checkout",
        "namespace": "checkout",
        "alerts": ["CheckoutQueueLag"],
        "model": {"name": "checkout-detector", "featureColumns": ["not_a_metric"]},
        "repairs": [{"alert": "CheckoutQueueLag", "action": "restart_pod", "params": {"deployment": "checkout-api"}}]
      }]
    }`
	client := k8sfake.NewSimpleClientset(
		deployment("checkout", "checkout-api", "1", "checkout:v1"),
		pod("checkout", "keep", "checkout-api", corev1.PodRunning),
	)
	scorer := &recordingScorer{verdictFor: func(string) int { return -1 }}
	engine := testEngine(t, raw, client, &scriptQuerier{}, scorer, discardLogger())

	response := engine.HandleWebhook(context.Background(), Webhook{Status: "firing", Alerts: []IncomingAlert{
		{Status: "firing", Labels: map[string]string{"alertname": "CheckoutQueueLag", "namespace": "checkout"}},
	}})
	require.Equal(t, resultFeatureError, response.ActionsTaken[0].Result)
	require.Empty(t, scorer.calls)
	_, err := client.CoreV1().Pods("checkout").Get(context.Background(), "keep", metav1.GetOptions{})
	require.NoError(t, err)
}

func TestEngine_DedupeBlocksRetryUntilTTL(t *testing.T) {
	client := k8sfake.NewSimpleClientset(deployment("checkout", "checkout-api", "1", "checkout:v1"))
	scorer := &recordingScorer{verdictFor: func(string) int { return -1 }}
	engine := testEngine(t, twoApplicationPolicyJSON, client, &scriptQuerier{}, scorer, discardLogger())
	current := time.Date(2026, 10, 21, 20, 0, 0, 0, time.UTC)
	engine.now = func() time.Time { return current }
	alert := IncomingAlert{
		Status:      "firing",
		Labels:      map[string]string{"alertname": "CheckoutQueueLag", "namespace": "checkout", "pod": "checkout-api-0"},
		StartsAt:    current,
		Fingerprint: "same",
	}

	first := engine.HandleWebhook(context.Background(), Webhook{Status: "firing", Alerts: []IncomingAlert{alert}})
	second := engine.HandleWebhook(context.Background(), Webhook{Status: "firing", Alerts: []IncomingAlert{alert}})
	require.Equal(t, resultSuccess, first.ActionsTaken[0].Result)
	require.Equal(t, resultDeduped, second.ActionsTaken[0].Result)
	require.Len(t, scorer.calls, 1)

	current = current.Add(repairDedupeTTL + time.Second)
	third := engine.HandleWebhook(context.Background(), Webhook{Status: "firing", Alerts: []IncomingAlert{alert}})
	require.Equal(t, resultSuccess, third.ActionsTaken[0].Result)
	require.Len(t, scorer.calls, 2)
}

func testEngine(t *testing.T, raw string, client *k8sfake.Clientset, querier Querier, scorer Scorer, log *logrus.Logger) *Engine {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "policies.json")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
	loader, err := NewLoader(path, log)
	require.NoError(t, err)
	executor := testExecutorWithDynamic(t, client, dynamicfake.NewSimpleDynamicClient(runtime.NewScheme()), nil)
	engine, err := NewEngine(EngineConfig{
		Loader:         loader,
		Querier:        querier,
		Scorer:         scorer,
		Actions:        executor,
		ModelNamespace: "self-healing-platform",
		ModelPort:      8080,
		Log:            log,
	})
	require.NoError(t, err)
	return engine
}

func actionResult(response AlertsResponse, policyName string) string {
	for _, action := range response.ActionsTaken {
		if action.Policy == policyName {
			return action.Result
		}
	}
	return ""
}

func callFor(t *testing.T, scorer *recordingScorer, model string) scoreCall {
	t.Helper()
	for _, call := range scorer.calls {
		if strings.Contains(call.url, model) {
			return call
		}
	}
	t.Fatalf("no score call for %s", model)
	return scoreCall{}
}

type scoreCall struct {
	url      string
	features []float64
}

type recordingScorer struct {
	mu         sync.Mutex
	calls      []scoreCall
	verdictFor func(url string) int
}

func (s *recordingScorer) Score(_ context.Context, predictURL string, features []float64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, scoreCall{url: predictURL, features: append([]float64(nil), features...)})
	return s.verdictFor(predictURL), nil
}

type scriptQuerier struct {
	mu      sync.Mutex
	queries []string
}

func (q *scriptQuerier) Query(_ context.Context, query string) (float64, error) {
	q.mu.Lock()
	q.queries = append(q.queries, query)
	q.mu.Unlock()
	switch {
	case strings.Contains(query, "checkout_queue_depth"):
		return 7, nil
	case strings.Contains(query, "container_cpu_usage_seconds_total"):
		return 0.5, nil
	case strings.Contains(query, "container_memory_working_set_bytes"):
		return 0.6, nil
	case strings.Contains(query, "kube_pod_container_status_restarts_total"):
		return 4, nil
	case strings.Contains(query, "http_request_duration_seconds_bucket"):
		return 0.2, nil
	case strings.Contains(query, "http_requests_total"):
		return 0.01, nil
	default:
		return 0, fmt.Errorf("unexpected query %s", query)
	}
}
