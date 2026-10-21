package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const parasolPolicyJSON = `{
  "actionTypes": ["restart_pod", "rollback_deployment", "scale_deployment", "delete_resource", "apply_resource", "exec_command", "patch_resource"],
  "policies": [
    {
      "name": "parasol-insurance",
      "namespace": "parasol-insurance",
      "alerts": ["ParasolOOMRisk", "ParasolCPUThrottling", "ParasolCrashLooping", "ParasolDBConnectionFailure", "ParasolDiskPressure"],
      "model": {
        "name": "anomaly-detector",
        "featureColumns": ["cpu_usage", "memory_usage", "restart_count", "response_time", "error_rate"]
      },
      "repairs": [
        { "alert": "ParasolOOMRisk", "action": "restart_pod", "params": { "deployment": "ic-app" } },
        { "alert": "ParasolCPUThrottling", "action": "restart_pod", "params": { "deployment": "ic-app" } },
        { "alert": "ParasolCrashLooping", "action": "rollback_deployment", "params": { "deployment": "ic-app" } },
        { "alert": "ParasolDBConnectionFailure", "action": "delete_resource", "params": { "apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "name": "chaos-block-db-access" } },
        { "alert": "ParasolDiskPressure", "action": "exec_command", "params": { "podSelector": "app=postgresql", "command": ["rm", "/var/lib/pgsql/data/chaos-fill"] } }
      ]
    }
  ]
}`

const twoApplicationPolicyJSON = `{
  "actionTypes": ["restart_pod", "rollback_deployment", "scale_deployment", "delete_resource", "apply_resource", "exec_command", "patch_resource"],
  "policies": [
    {
      "name": "parasol-insurance",
      "namespace": "parasol-insurance",
      "alerts": ["ParasolOOMRisk"],
      "model": {
        "name": "anomaly-detector",
        "featureColumns": ["cpu_usage", "memory_usage", "restart_count", "response_time", "error_rate"]
      },
      "repairs": [
        { "alert": "ParasolOOMRisk", "action": "restart_pod", "params": { "deployment": "ic-app" } }
      ]
    },
    {
      "name": "checkout",
      "namespace": "checkout",
      "alerts": ["CheckoutQueueLag"],
      "model": {
        "name": "checkout-detector",
        "namespace": "checkout-ml",
        "service": "checkout-detector-predictor",
        "featureColumns": ["queue_depth"],
        "featureQueries": {
          "queue_depth": "sum(checkout_queue_depth{namespace=\"$namespace\",pod=\"$pod\"})"
        }
      },
      "repairs": [
        { "alert": "CheckoutQueueLag", "action": "scale_deployment", "params": { "deployment": "checkout-api", "replicas": 3 } }
      ]
    }
  ]
}`

func TestDocument_PublishedParasolSample(t *testing.T) {
	doc := mustDocument(t, parasolPolicyJSON)
	require.Len(t, doc.Policies, 1)
	assert.Equal(t, "parasol-insurance", doc.Policies[0].Name)
	assert.Equal(t, []string{"cpu_usage", "memory_usage", "restart_count", "response_time", "error_rate"}, doc.Policies[0].Model.FeatureColumns)
	assert.Empty(t, doc.Policies[0].Model.FeatureQueries)
	assert.Empty(t, doc.Policies[0].Model.Service)
}

func TestDocument_TwoApplications(t *testing.T) {
	doc := mustDocument(t, twoApplicationPolicyJSON)
	require.Len(t, doc.Policies, 2)

	parasol := doc.Match("parasol-insurance", "ParasolOOMRisk")
	checkout := doc.Match("checkout", "CheckoutQueueLag")
	require.NotNil(t, parasol)
	require.NotNil(t, checkout)
	assert.Equal(t, "parasol-insurance", parasol.Name)
	assert.Equal(t, "checkout", checkout.Name)
	assert.Nil(t, doc.Match("checkout", "ParasolOOMRisk"))
	assert.Nil(t, doc.Match("parasol-insurance", "CheckoutQueueLag"))
}

func TestDocument_RejectsUnknownAction(t *testing.T) {
	raw := `{
      "actionTypes": ["restart_pod", "drain_node"],
      "policies": []
    }`
	_, err := parseDocument(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "drain_node")
}

func TestDocument_RejectsRepairOutsideActionTypes(t *testing.T) {
	raw := `{
      "actionTypes": ["restart_pod"],
      "policies": [{
        "name": "checkout",
        "namespace": "checkout",
        "alerts": ["CheckoutQueueLag"],
        "model": {"name": "checkout-detector", "featureColumns": ["cpu_usage"]},
        "repairs": [{"alert": "CheckoutQueueLag", "action": "scale_deployment", "params": {"deployment": "checkout-api", "replicas": 2}}]
      }]
    }`
	_, err := parseDocument(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scale_deployment")
}

func TestDocument_RejectsDuplicateNamespaceAlert(t *testing.T) {
	raw := `{
      "actionTypes": ["restart_pod"],
      "policies": [
        {
          "name": "checkout",
          "namespace": "checkout",
          "alerts": ["CheckoutQueueLag"],
          "model": {"name": "checkout-detector", "featureColumns": ["cpu_usage"]},
          "repairs": [{"alert": "CheckoutQueueLag", "action": "restart_pod", "params": {"deployment": "checkout-api"}}]
        },
        {
          "name": "checkout-canary",
          "namespace": "checkout",
          "alerts": ["CheckoutQueueLag"],
          "model": {"name": "checkout-detector", "featureColumns": ["cpu_usage"]},
          "repairs": [{"alert": "CheckoutQueueLag", "action": "restart_pod", "params": {"deployment": "checkout-api"}}]
        }
      ]
    }`
	_, err := parseDocument(raw)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "claimed by both")
}

func TestDocument_UnknownFeatureColumnStillLoads(t *testing.T) {
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
	doc, err := parseDocument(raw)
	require.NoError(t, err)
	assert.Equal(t, "not_a_metric", doc.Policies[0].Model.FeatureColumns[0])
}

func mustDocument(t *testing.T, raw string) *Document {
	t.Helper()
	doc, err := parseDocument(raw)
	require.NoError(t, err)
	return doc
}

func parseDocument(raw string) (*Document, error) {
	doc, err := decodeDocument([]byte(raw))
	if err != nil {
		return nil, err
	}
	if err := doc.Validate(); err != nil {
		return nil, err
	}
	return doc, nil
}
