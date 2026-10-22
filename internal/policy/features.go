package policy

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// FeatureScope is the workload a feature query is narrowed to.
// Namespace is the policy namespace. Pod and Deployment come from alert labels
// when those labels are valid Kubernetes names.
type FeatureScope struct {
	Namespace  string
	Pod        string
	Deployment string
}

func sanitizeScope(namespace, pod, deployment string) (FeatureScope, error) {
	if errs := validation.IsDNS1123Label(namespace); len(errs) > 0 {
		return FeatureScope{}, fmt.Errorf("namespace %q is invalid: %s", namespace, strings.Join(errs, ", "))
	}
	scope := FeatureScope{Namespace: namespace}
	if pod != "" {
		if errs := validation.IsDNS1123Subdomain(pod); len(errs) > 0 {
			return FeatureScope{}, fmt.Errorf("pod label %q is not a kubernetes name", pod)
		}
		scope.Pod = pod
	}
	if deployment != "" {
		if errs := validation.IsDNS1123Subdomain(deployment); len(errs) > 0 {
			return FeatureScope{}, fmt.Errorf("deployment label %q is not a kubernetes name", deployment)
		}
		scope.Deployment = deployment
	}
	return scope, nil
}

// resolveFeatureQuery returns PromQL for one training column.
// A policy query wins over the built-in catalog. Unknown columns are not guessed.
func resolveFeatureQuery(model *ModelSpec, column string, scope FeatureScope) (string, error) {
	if model != nil {
		if query, ok := model.FeatureQueries[column]; ok && strings.TrimSpace(query) != "" {
			return applyFeatureTemplate(query, scope), nil
		}
	}
	query, ok := catalogQuery(column, scope)
	if !ok {
		return "", fmt.Errorf("no prometheus query for feature column %q", column)
	}
	return query, nil
}

func applyFeatureTemplate(query string, scope FeatureScope) string {
	return strings.NewReplacer(
		"$namespace", scope.Namespace,
		"$pod", scope.Pod,
		"$deployment", scope.Deployment,
	).Replace(query)
}

func catalogQuery(column string, scope FeatureScope) (string, bool) {
	selector := workloadSelector(scope)
	switch column {
	case "cpu_usage":
		return fmt.Sprintf(`sum(rate(container_cpu_usage_seconds_total{%s,container!=""}[5m]))`, selector), true
	case "memory_usage":
		return fmt.Sprintf(`sum(container_memory_working_set_bytes{%s,container!=""})`, selector), true
	case "restart_count":
		return fmt.Sprintf(`sum(kube_pod_container_status_restarts_total{%s})`, selector), true
	case "response_time":
		return fmt.Sprintf(`histogram_quantile(0.95, sum by (le) (rate(http_request_duration_seconds_bucket{%s}[5m])))`, selector), true
	case "error_rate":
		return fmt.Sprintf(
			`sum(rate(http_requests_total{%s,status=~"5.."}[5m])) / sum(rate(http_requests_total{%s}[5m]))`,
			selector, selector,
		), true
	default:
		return "", false
	}
}

func workloadSelector(scope FeatureScope) string {
	parts := []string{"namespace=" + strconv.Quote(scope.Namespace)}
	switch {
	case scope.Pod != "":
		parts = append(parts, "pod="+strconv.Quote(scope.Pod))
	case scope.Deployment != "":
		matcher := regexp.QuoteMeta(scope.Deployment) + "-.*"
		parts = append(parts, "pod=~"+strconv.Quote(matcher))
	}
	return strings.Join(parts, ",")
}
