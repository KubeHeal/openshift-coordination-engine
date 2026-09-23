# Coordination Engine User Guide

> **Version:** 1.2.0 | **Last updated:** 2026-09-23

## What Is the Coordination Engine?

The Coordination Engine is the orchestration core of the KubeHeal self-healing platform. It receives health signals from Prometheus and KServe ML models, detects anomalies, plans remediation workflows, and executes multi-layer recovery actions across OpenShift clusters.

**Where it fits in the KubeHeal stack:**

```
MCP Server  --->  Coordination Engine  --->  KServe ML Models
(NL interface)    (this component)           (anomaly detection)
                        |
                        +--->  Kubernetes / ArgoCD / MCO
                               (remediation targets)
```

API consumers call the engine through its REST API on port 8080. The MCP server is the primary consumer, but SREs and platform engineers can call the API directly.

## Prerequisites

Before you call the Coordination Engine API, verify the following:

- The engine pod is running in the `self-healing-platform` namespace.
- KServe InferenceServices are deployed (`anomaly-detector`, `predictive-analytics`).
- Prometheus is accessible at the configured URL (required for predictions and feature engineering).
- Your client can reach the engine service at `http://coordination-engine:8080` (in-cluster) or through an exposed route.

## Quick Start

### 1. Check Health

Verify the engine and its dependencies are operational:

```bash
curl http://coordination-engine:8080/api/v1/health
```

**Expected response:**

```json
{
  "status": "healthy",
  "timestamp": "2026-09-23T10:00:00Z",
  "version": "1.2.0",
  "dependencies": {
    "kubernetes": "ok",
    "ml_service": "ok",
    "argocd": "ok"
  }
}
```

A `"status": "healthy"` response means the engine can reach Kubernetes, the ML service, and ArgoCD.

### 2. Analyze Anomalies

Send metrics to the anomaly analysis endpoint:

```bash
curl -X POST http://coordination-engine:8080/api/v1/anomalies/analyze \
  -H "Content-Type: application/json" \
  -d '{"namespace": "production"}'
```

The engine queries Prometheus for current metrics, sends them to the KServe anomaly-detector model, and returns detected anomalies. If anomalies exceed the configured severity threshold, alerts are dispatched to configured sinks (Slack, PagerDuty, or Alertmanager).

### 3. List Incidents

View recent incidents and their remediation status:

```bash
curl "http://coordination-engine:8080/api/v1/incidents?namespace=production&limit=10"
```

## API Feature Walkthroughs

The base URL for all endpoints is `http://coordination-engine:8080/api/v1`.

### Health Check

**Endpoint:** `GET /health`

Returns the engine status and the health of each dependency.

| Dependency | Meaning when `"ok"` |
|---|---|
| `kubernetes` | The engine can reach the Kubernetes API server. |
| `ml_service` | KServe InferenceServices are responding. |
| `argocd` | ArgoCD API is reachable (or ArgoCD is not configured). |

### Anomaly Analysis

**Endpoint:** `POST /api/v1/anomalies/analyze`

Queries Prometheus for live metrics and sends them to the KServe anomaly-detector model. Returns a list of detected anomalies with severity scores.

When configured, the engine dispatches alerts for anomalies that meet or exceed the severity threshold. See the "Alert Notification Sinks" section for details.

### Predictions

**Endpoint:** `POST /api/v1/predict`

Predicts future resource usage for a target workload at a specified time.

**Request body:**

```json
{
  "hour": 15,
  "day_of_week": 3,
  "namespace": "production",
  "deployment": "payment-service",
  "scope": "deployment",
  "model": "predictive-analytics"
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `hour` | int | Yes | Hour of day (0 to 23). |
| `day_of_week` | int | Yes | Day of week (0 = Monday, 6 = Sunday). |
| `namespace` | string | No | Namespace filter. |
| `deployment` | string | No | Deployment filter. |
| `pod` | string | No | Pod filter. |
| `scope` | string | No | One of `pod`, `deployment`, `namespace`, `cluster`. Default: inferred. |
| `model` | string | No | KServe model name. Default: `predictive-analytics`. |

**Response fields of note:**

- `predictions.cpu_percent` and `predictions.memory_percent` are the predicted values.
- `current_metrics` shows rolling averages from Prometheus.
- `model_info.confidence` is a score from 0.0 to 1.0.

When feature engineering is enabled (the default), the engine builds a 3200+ engineered feature vector from Prometheus range queries. When disabled, 5 raw features are sent: `cpu_usage`, `memory_usage`, `disk_usage`, `network_in`, `network_out`.

### Recommendations

**Endpoint:** `POST /recommendations`

Returns ML-powered remediation recommendations with predictive analytics.

**Request body:**

```json
{
  "timeframe": "6h",
  "include_predictions": true,
  "confidence_threshold": 0.7,
  "namespace": "production"
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `timeframe` | string | `"6h"` | Prediction window: `"1h"`, `"6h"`, or `"24h"`. |
| `include_predictions` | bool | `true` | Include ML-powered predictions. |
| `confidence_threshold` | float | `0.7` | Minimum confidence score (0.0 to 1.0). |
| `namespace` | string | (none) | Filter by namespace. |

**Recommendation types:**

- `proactive`: Predicted issues that have not occurred yet.
- `reactive`: Based on current or recent issues.

**Recommendation sources:**

- `ml_prediction`: Generated by the KServe predictive-analytics model.
- `historical_analysis`: Based on historical incident patterns.
- `pattern_detection`: Based on detected failure patterns.

### Root Cause Analysis

**Endpoint:** `POST /api/v1/investigate/rca`

Correlates pod events, NetworkPolicy violations, and Istio VirtualService misconfigurations to identify root causes.

**Request body:**

```json
{
  "service": "my-app",
  "namespace": "production",
  "start_time": "2026-09-22T10:00:00Z",
  "end_time": "2026-09-22T11:00:00Z"
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `service` | string | Yes | Service name to investigate. |
| `namespace` | string | Yes | Kubernetes namespace. |
| `start_time` | string | Yes | RFC 3339 start of investigation window. |
| `end_time` | string | Yes | RFC 3339 end (max 7 days from start). |

**Signal types returned:**

- `pod_event`: OOMKilled, CrashLoopBackOff, and similar pod events.
- `network_policy`: Blocked traffic from NetworkPolicy rules.
- `istio_virtual_service`: Istio routing misconfigurations.

All three correlators run in parallel with a 25-second timeout. Istio correlation is skipped when Istio CRDs are not installed.

### Incidents

**Endpoint:** `GET /incidents`

Returns a list of incidents with their remediation status.

**Query parameters:**

| Parameter | Type | Default | Description |
|---|---|---|---|
| `namespace` | string | (none) | Filter by namespace. |
| `severity` | string | (none) | Filter: `low`, `medium`, `high`, `critical`. |
| `limit` | int | `50` | Maximum results to return. |

### Remediation

**Endpoint:** `POST /remediation/trigger`

Triggers a remediation workflow for a specific incident.

**Request body:**

```json
{
  "incident_id": "inc-12345",
  "namespace": "production",
  "resource": {
    "kind": "Deployment",
    "name": "payment-service"
  },
  "issue": {
    "type": "pod_crash_loop",
    "description": "Pods in CrashLoopBackOff",
    "severity": "high"
  }
}
```

**Response** (202 Accepted):

```json
{
  "workflow_id": "wf-67890",
  "status": "in_progress",
  "deployment_method": "argocd",
  "estimated_duration": "5m"
}
```

The engine detects the deployment method (ArgoCD, Helm, Operator, or Manual) and routes remediation accordingly:

- **ArgoCD-managed**: Triggers an ArgoCD sync.
- **Helm-managed**: Applies changes through Helm.
- **Operator-managed**: Delegates to the managing operator.
- **Manual**: Applies changes directly through the Kubernetes API.

### Workflow Details

**Endpoint:** `GET /workflows/{id}`

Returns execution details for a remediation workflow, including steps, layer progression, and health checkpoints.

### Pattern Analysis

**Endpoint:** `POST /api/v1/pattern/analyze`

Analyzes historical data for recurring failure patterns.

**Request body:**

```json
{
  "namespace": "production",
  "time_range": {
    "start": "2026-09-22T00:00:00Z",
    "end": "2026-09-23T00:00:00Z"
  },
  "resource_types": ["Deployment", "StatefulSet"]
}
```

Returns patterns with types (for example, `recurring_crash`), frequencies, affected resources, confidence scores, and root cause hints.

## Alert Notification Sinks

When anomaly analysis detects issues at or above the configured severity threshold, alerts are dispatched asynchronously to all configured sinks. Alert dispatch never delays the HTTP response.

### Supported Sinks

| Sink | Env Var | Protocol |
|---|---|---|
| Slack | `KUBEHEAL_SLACK_WEBHOOK_URL` | Incoming webhook POST. |
| PagerDuty | `KUBEHEAL_PAGERDUTY_ROUTING_KEY` | Events API v2 POST to `/v2/enqueue`. |
| Alertmanager | `KUBEHEAL_ALERTMANAGER_URL` | POST to `{url}/api/v2/alerts`. |

### Behavior

- Each sink runs in its own goroutine with a 10-second timeout.
- Sink errors are logged at WARN level but never propagated to the API response.
- If no sinks are configured, the dispatcher is a no-op.
- Severity comparison: `info < warning < critical`. Setting the threshold to `warning` dispatches alerts for both `warning` and `critical`.

## Environment Variable Reference

### Core Configuration

| Variable | Default | Description |
|---|---|---|
| `PORT` | `8080` | HTTP server port. |
| `METRICS_PORT` | `9090` | Prometheus metrics port. |
| `LOG_LEVEL` | `info` | Log level: `debug`, `info`, `warn`, `error`, `fatal`, `panic`. |
| `KUBECONFIG` | (empty) | Path to kubeconfig file. Empty uses in-cluster config. |
| `NAMESPACE` | `self-healing-platform` | Namespace the engine operates in. |
| `HTTP_TIMEOUT` | `30s` | HTTP client timeout for outbound calls. |
| `ENABLE_CORS` | `false` | Enable CORS headers on API responses. |
| `CORS_ALLOW_ORIGIN` | `*` | Comma-separated list of allowed CORS origins. |

### KServe Integration

| Variable | Default | Description |
|---|---|---|
| `ENABLE_KSERVE_INTEGRATION` | `true` | Enable KServe model integration. |
| `KSERVE_NAMESPACE` | `self-healing-platform` | Namespace where KServe InferenceServices are deployed. |
| `KSERVE_PREDICTOR_PORT` | `8080` | Port for KServe predictors (RawDeployment mode). |
| `KSERVE_ANOMALY_DETECTOR_SERVICE` | (empty) | KServe service name for anomaly detection. |
| `KSERVE_PREDICTIVE_ANALYTICS_SERVICE` | (empty) | KServe service name for predictive analytics. |
| `KSERVE_TIMEOUT` | `10s` | Timeout for KServe API calls (min 1s, max 2m). |

**Dynamic service discovery:** Set `KSERVE_<MODEL_NAME>_SERVICE=<service-name>` to register additional KServe models without code changes. For example, `KSERVE_DISK_FAILURE_PREDICTOR_SERVICE=disk-failure-predictor-predictor` registers a model named `disk-failure-predictor`.

### Prometheus and ML

| Variable | Default | Description |
|---|---|---|
| `PROMETHEUS_URL` | (empty) | Prometheus API URL. Empty disables Prometheus queries. |
| `ENABLE_FEATURE_ENGINEERING` | `true` | Build 3200+ engineered features from Prometheus. |
| `FEATURE_ENGINEERING_LOOKBACK_HOURS` | `24` | Hours of historical data for feature engineering. |
| `FEATURE_ENGINEERING_EXPECTED_COUNT` | `0` | Expected feature count (0 disables validation). |

### Incident Storage

| Variable | Default | Description |
|---|---|---|
| `DATA_DIR` | (empty) | Directory for persistent incident storage. Empty uses in-memory only. |
| `INCIDENT_RETENTION_DAYS` | `90` | Days to retain resolved incidents. |
| `KUBEHEAL_MAX_STORED_INCIDENTS` | `10000` | Maximum incidents stored on disk. |

### OOM Remediation

| Variable | Default | Description |
|---|---|---|
| `OOM_MEMORY_MULTIPLIER` | `2.5` | Memory limit multiplier on OOMKill events. |
| `OOM_MEMORY_MAX_LIMIT` | `2Gi` | Maximum memory limit ceiling. |

### Alert Sinks

| Variable | Default | Description |
|---|---|---|
| `KUBEHEAL_SLACK_WEBHOOK_URL` | (empty) | Slack incoming webhook URL. Empty disables Slack alerts. |
| `KUBEHEAL_PAGERDUTY_ROUTING_KEY` | (empty) | PagerDuty Events API v2 routing key. Empty disables PagerDuty. |
| `KUBEHEAL_ALERTMANAGER_URL` | (empty) | Alertmanager base URL. Empty disables Alertmanager alerts. |
| `KUBEHEAL_ALERT_SEVERITY_THRESHOLD` | `critical` | Minimum severity to trigger alerts: `info`, `warning`, `critical`. |

### Legacy (Deprecated)

| Variable | Default | Description |
|---|---|---|
| `ML_SERVICE_URL` | (empty) | Legacy Python ML service URL. Use KServe instead. |
| `ARGOCD_API_URL` | (empty) | ArgoCD API URL. Auto-detected from the cluster when empty. |

### Kubernetes Client Tuning

| Variable | Default | Description |
|---|---|---|
| `KUBERNETES_QPS` | `50` | Kubernetes API client queries per second. |
| `KUBERNETES_BURST` | `100` | Kubernetes API client burst limit. |

## Troubleshooting

### Common HTTP Error Codes

| Code | Meaning | Action |
|---|---|---|
| `400` | Invalid request body or parameters. | Check your JSON payload and parameter values. |
| `404` | Endpoint not found. | Verify the URL path matches the API contract. |
| `500` | Internal server error. | Check engine logs with `kubectl logs -n self-healing-platform deploy/coordination-engine`. |
| `502` | Upstream service unavailable. | Verify Prometheus and KServe services are running. |
| `503` | Service unavailable. | The engine is starting up or a dependency is down. |

### KServe Not Ready

**Symptom:** Predictions return `KSERVE_UNAVAILABLE` or `MODEL_NOT_FOUND`.

**Steps to resolve:**

1. Verify KServe InferenceServices are deployed:
   ```bash
   kubectl get inferenceservices -n self-healing-platform
   ```
2. Check that the service names match the environment variables:
   ```bash
   kubectl get env deploy/coordination-engine -n self-healing-platform | grep KSERVE
   ```
3. Verify KServe predictor pods are running:
   ```bash
   kubectl get pods -n self-healing-platform -l component=predictor
   ```

### Prometheus Unreachable

**Symptom:** Predictions return empty results or the feature engineering step fails.

**Steps to resolve:**

1. Verify the `PROMETHEUS_URL` environment variable is set correctly.
2. Test connectivity from the engine pod:
   ```bash
   kubectl exec -n self-healing-platform deploy/coordination-engine -- \
     curl -s "$PROMETHEUS_URL/api/v1/query?query=up"
   ```
3. In OpenShift, the typical Prometheus URL is `https://prometheus-k8s.openshift-monitoring.svc:9091`.

### Health Endpoint Reports Unhealthy Dependency

**Symptom:** `GET /health` returns `"status": "degraded"` with one or more dependencies not `"ok"`.

**Steps to resolve:**

1. Identify the failing dependency in the response body.
2. Check the engine logs for the specific error:
   ```bash
   kubectl logs -n self-healing-platform deploy/coordination-engine --tail=50
   ```
3. Verify network policies allow traffic from the engine pod to the dependency.

### Remediation Workflow Stuck

**Symptom:** `GET /workflows/{id}` shows `"status": "in_progress"` for longer than the estimated duration.

**Steps to resolve:**

1. Check the workflow steps for a failed checkpoint.
2. For ArgoCD-managed workloads, verify the ArgoCD Application sync status:
   ```bash
   kubectl get applications -n argocd
   ```
3. Check RBAC permissions. The engine ServiceAccount needs access to the target namespace.

## Glossary

| Term | Definition |
|---|---|
| **Anomaly** | A metric value that the ML model classifies as outside normal behavior. |
| **Checkpoint** | A health validation step that runs between remediation actions. |
| **Coordination Engine** | The Go service that orchestrates anomaly detection, RCA, and remediation. |
| **Feature Engineering** | The process of building a high-dimensional feature vector from raw Prometheus metrics for ML model input. |
| **Incident** | A record of a detected issue, including its severity, affected resource, and remediation status. |
| **InferenceService** | A KServe custom resource that serves an ML model behind a standard prediction API. |
| **KServe** | A Kubernetes-native platform for serving ML models. |
| **Layer** | One of three remediation scopes: infrastructure (nodes, MCO), platform (operators, core services), application (user workloads). |
| **MCP Server** | The natural language interface that consumes this engine. |
| **Remediation** | An automated corrective action (for example, ArgoCD sync, memory limit increase, pod restart). |
| **RCA** | Root Cause Analysis. The process of correlating signals to identify the underlying cause of an issue. |
| **Sink** | An external alert destination (Slack, PagerDuty, Alertmanager). |
| **Workflow** | A sequence of remediation steps with health checkpoints between layers. |
