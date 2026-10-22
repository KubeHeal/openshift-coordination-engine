package policy

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func decodeDocument(data []byte) (*Document, error) {
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("decode policy document: %w", err)
	}
	return &doc, nil
}

func TestResolveFeatureQuery_CatalogOrderAndScope(t *testing.T) {
	model := ModelSpec{FeatureColumns: []string{"cpu_usage", "memory_usage", "restart_count", "response_time", "error_rate"}}
	scope := FeatureScope{Namespace: "checkout", Deployment: "checkout-api"}

	queries := make([]string, 0, len(model.FeatureColumns))
	for _, column := range model.FeatureColumns {
		query, err := resolveFeatureQuery(&model, column, scope)
		require.NoError(t, err)
		queries = append(queries, query)
	}

	require.Contains(t, queries[0], "container_cpu_usage_seconds_total")
	require.Contains(t, queries[1], "container_memory_working_set_bytes")
	require.Contains(t, queries[2], "kube_pod_container_status_restarts_total")
	require.Contains(t, queries[3], "http_request_duration_seconds_bucket")
	require.Contains(t, queries[4], `status=~"5.."`)
	for _, query := range queries {
		require.Contains(t, query, `namespace="checkout"`)
		require.Contains(t, query, `pod=~"checkout-api-.*"`)
	}
}

func TestResolveFeatureQuery_PodBeatsDeployment(t *testing.T) {
	query, err := resolveFeatureQuery(&ModelSpec{}, "cpu_usage", FeatureScope{
		Namespace:  "checkout",
		Pod:        "checkout-api-0",
		Deployment: "checkout-api",
	})
	require.NoError(t, err)
	require.Contains(t, query, `pod="checkout-api-0"`)
	require.NotContains(t, query, "pod=~")
}

func TestResolveFeatureQuery_PolicyOverrideWins(t *testing.T) {
	model := ModelSpec{
		FeatureQueries: map[string]string{
			"cpu_usage": `sum(custom_cpu{namespace="$namespace",pod="$pod"})`,
		},
	}
	query, err := resolveFeatureQuery(&model, "cpu_usage", FeatureScope{Namespace: "checkout", Pod: "checkout-api-0"})
	require.NoError(t, err)
	require.Equal(t, `sum(custom_cpu{namespace="checkout",pod="checkout-api-0"})`, query)
}

func TestResolveFeatureQuery_UnknownColumn(t *testing.T) {
	_, err := resolveFeatureQuery(&ModelSpec{}, "queue_depth", FeatureScope{Namespace: "checkout"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "queue_depth")
}

func TestSanitizeScope_RejectsUnsafePodLabel(t *testing.T) {
	_, err := sanitizeScope("checkout", `bad"pod`, "")
	require.Error(t, err)
}
