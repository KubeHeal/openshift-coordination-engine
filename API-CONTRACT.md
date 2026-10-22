# API Contract: Go Coordination Engine

This document defines the **two critical integration boundaries** for the Go Coordination Engine.

## 1. Upstream API (Consumed by MCP Server)

The sections below describe what this server returns today. `docs/API.md` lists every registered route. Fields that are not returned yet are marked with an issue link.

Against KubeHeal/openshift-cluster-health-mcp commit `90564fa` (`pkg/clients/coordination_engine.go`), disk exhaustion, right-sizing, and RCA match the client. Anomaly JSON names, the create-incident response shape, predict `trend`, and `GET /api/v1/capacity/trends` do not. Incident list `severity=all`, `status`, and `limit` match. `offset` is still ignored, and `status=completed` does not match workflow rows whose status is `remediated`.

### Base URL
- `http://coordination-engine:8080/api/v1`

### Endpoints

#### `GET /health`
Lightweight liveness check on the API port. It does not check dependencies.

**Response** (200 OK):
```json
{
  "status": "ok",
  "version": "1.2.0"
}
```

#### `GET /api/v1/health`
Dependency health. `dependencies.kubernetes` and `dependencies.ml_service` are objects (`name`, `status`, `message`, `latency_ms`, `checked_at`), not strings. There is no `argocd` entry.

`rbac` includes `status`, `critical_ok`, and `message`. `permissions_total`, `permissions_ok`, and `permissions_failed` are returned as `0` because this handler does not count permissions.

`details.namespace` is an empty object (the RBAC verifier has no exported fields). `details.service_account` is `"self-healing-operator"`.

**Response** (200 when `healthy` or `degraded`, 503 when `unhealthy`):
```json
{
  "status": "healthy",
  "timestamp": "2025-12-18T10:00:00Z",
  "version": "1.2.0",
  "uptime_seconds": 3600,
  "dependencies": {
    "kubernetes": {
      "name": "kubernetes",
      "status": "ok",
      "message": "Connected",
      "latency_ms": 15,
      "checked_at": "2025-12-18T10:00:00Z"
    },
    "ml_service": {
      "name": "ml_service",
      "status": "ok",
      "message": "Connected",
      "latency_ms": 45,
      "checked_at": "2025-12-18T10:00:00Z"
    }
  },
  "rbac": {
    "status": "ok",
    "permissions_total": 0,
    "permissions_ok": 0,
    "permissions_failed": 0,
    "critical_ok": true,
    "message": "Critical permissions verified"
  },
  "details": {
    "namespace": {},
    "service_account": "self-healing-operator"
  }
}
```

#### `POST /remediation/trigger`
Trigger a remediation workflow.

**Request Body**:
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
  "workflow_id": "wf-67890abc",
  "status": "pending",
  "deployment_method": "argocd",
  "estimated_duration": "1m"
}
```

`status` at accept time is `pending`. `estimated_duration` is the checkpoint timeout in whole minutes. The default is `"1m"`. `dry_run` is not a handler field. If the MCP tool sets it, the workflow still starts. When `DATA_DIR` is set, the workflow is written to `{DATA_DIR}/workflows/<id>.json` before this response returns. An empty `DATA_DIR` keeps it in memory. If `incident_id` exists in the incident store, this call sets `workflow_id` on that incident. An unknown id still returns `202`. Validation errors are plain text, not JSON. MCP does not call `GET /api/v1/workflows/{id}`.

#### `GET /incidents`
Lists stored incidents and workflow-derived rows.

**Query parameters**:
- `namespace` (optional): rows whose `namespace` or `target` equals this value
- `severity` (optional): exact match. Empty or `all` matches every severity
- `status` (optional): exact match on the row status. Empty or `all` matches every status
- `limit` (optional): maximum rows returned. Defaults to 50

`offset` is ignored. Filters apply to the combined list. `total` is the match count before `limit`. The MCP incident list, `cluster://incidents`, and remediation-history all send `severity=all`, `status`, `limit`, and `offset`. `severity=all` returns every severity. `status=completed` does not match workflow rows, whose status is `remediated`. MCP reads `summary.*`, `priority`, `action_type`, `confidence`, `parameters`, `started_at`, `completed_at`, `duration_seconds`, and `tags`, which are absent.

**Response** (200 OK). A workflow-derived row looks like this (`source` is `"workflow"`, and `severity` is the workflow severity):
```json
{
  "incidents": [
    {
      "id": "inc-12345",
      "namespace": "production",
      "target": "production",
      "resource": "Deployment/payment-service",
      "issue_type": "pod_crash_loop",
      "severity": "high",
      "created_at": "2025-12-18T09:45:00Z",
      "status": "remediated",
      "workflow_id": "wf-67890abc",
      "source": "workflow"
    }
  ],
  "total": 1
}
```

Stored rows use `source` `"manual"` and include `title`, `description`, `target`, `severity`, `status` (`active`, `resolved`, or `cancelled`), `affected_resources`, and `labels`. They do not include `namespace`. Workflow rows use the workflow severity, falling back to the stored incident severity, and status `remediated`, `failed`, or `in_progress`. When `DATA_DIR` is set, workflow rows are loaded from `{DATA_DIR}/workflows`.

`POST /api/v1/incidents` creates stored rows (`201`). The handler reads `affected_resources` (snake_case) and requires `target`. MCP sends `affectedResources`, `source`, `createdBy`, `correlationId`, `externalId`, `confidence`, and `parameters`, which are ignored. MCP does not require `target`, so a create without `target` is `400`. The body is `status`, `incident_id`, `created_at`, `incident`, and `message`. Top-level `status` is `"success"`. `title`, `description`, `severity`, and status `active` are inside `incident`. There is no `priority`. MCP reads those fields at the top level. Create errors are `{"status":"error","error":"..."}`.

#### `GET /workflows/{id}`
Get workflow execution details.

**Response** (200 OK):
```json
{
  "id": "wf-67890abc",
  "incident_id": "inc-12345",
  "status": "completed",
  "deployment_method": "argocd",
  "namespace": "production",
  "resource_name": "payment-service",
  "resource_kind": "Deployment",
  "issue_type": "pod_crash_loop",
  "remediator": "manual",
  "created_at": "2025-12-18T09:50:00Z",
  "started_at": "2025-12-18T09:50:00Z",
  "completed_at": "2025-12-18T09:52:00Z",
  "duration": "2m0s",
  "steps": [
    {
      "order": 0,
      "description": "Detect deployment method for production/payment-service",
      "status": "pending"
    }
  ],
  "layers": ["application"],
  "checkpoints": [
    {
      "layer": "application",
      "after_step": 1,
      "status": "passed",
      "checks": ["pods_running", "endpoints_healthy"]
    }
  ]
}
```

`layers` has one entry from the resource kind (`application` for `Deployment`, `StatefulSet`, `DaemonSet`, and `Pod`; `platform` otherwise). `checkpoints` has one entry after the remediation step. Its `status` is `pending` until execution finishes, then `passed` or `failed`. `404` is plain text `Workflow not found`. When `DATA_DIR` is set, the workflow is read from `{DATA_DIR}/workflows/<id>.json`. MCP does not call this route. `GET /api/v1/coordination/workflows/{id}` is a different object whose `remediation_plan` also has `layers` and `checkpoints`. MCP does not call that route either.

#### `POST /recommendations`
Get ML-powered remediation recommendations with predictive analytics. The MCP tool `get-remediation-recommendations` does not call this route. It calls `POST /api/v1/anomalies/analyze`.

**Request Body** (all fields optional):
```json
{
  "timeframe": "6h",
  "include_predictions": true,
  "confidence_threshold": 0.7,
  "namespace": "production"
}
```

**Request Parameters**:
- `timeframe` (optional, default: "6h"): Prediction window - `"1h"`, `"6h"`, or `"24h"`
- `include_predictions` (optional, default: true): Include ML-powered predictions from KServe
- `confidence_threshold` (optional, default: 0.7): Minimum confidence score (0.0-1.0)
- `namespace` (optional): Filter recommendations by namespace

**Response** (200 OK):
```json
{
  "status": "success",
  "timestamp": "2026-01-11T17:00:00Z",
  "timeframe": "6h",
  "recommendations": [
    {
      "id": "rec-001",
      "type": "proactive",
      "issue_type": "memory_pressure",
      "target": "Deployment/payment-service",
      "namespace": "production",
      "severity": "high",
      "confidence": 0.85,
      "predicted_time": "2026-01-11T19:30:00Z",
      "recommended_actions": [
        "increase_memory_limit",
        "add_horizontal_scaling"
      ],
      "evidence": [
        "Memory usage trend: 65% → 85% over 2h",
        "Historical pattern: similar increase before OOM"
      ],
      "source": "ml_prediction",
      "related_incident_id": "inc-12345"
    }
  ],
  "total_recommendations": 1,
  "ml_enabled": true
}
```

**Recommendation Types**:
- `proactive`: Predicted issues that haven't occurred yet
- `reactive`: Recommendations based on current/recent issues

**Recommendation Sources**:
- `ml_prediction`: Generated by KServe predictive-analytics model
- `historical_analysis`: Based on historical incident patterns
- `pattern_detection`: Based on detected failure patterns

**Error Response** (400 Bad Request):
```json
{
  "status": "error",
  "error": "Invalid timeframe: must be '1h', '6h', or '24h'"
}
```

#### `POST /api/v1/anomalies/analyze`
Anomaly analysis on this server. `POST /api/v1/anomaly/detect` is not registered here.

**Request** (all fields optional): `time_range` (`1h`, `6h`, `24h`, `7d`; default `1h`), `namespace`, `deployment`, `pod`, `label_selector`, `threshold` (0–1, default `0.7`), `model_name` (default `anomaly-detector`).

MCP sends `timeRange`, `modelName`, `labelSelector`, `metrics`, and `includePredictions`. The handler binds `time_range`, `model_name`, and `label_selector`, so the camelCase names are dropped. The window stays `1h` and the model stays `anomaly-detector`.

**Response** (200 OK): `status` (`success`), `time_range`, `scope`, `model_used`, `anomalies_detected`, `anomalies`, `summary`, `recommendation`, `features`, and optional `enriched_signals`.

The recommendation field is singular `recommendation`. MCP reads `recommendations`, `alerts`, `analyzed_at`, and anomaly `metric`, `value`, `type`, `expected_min`, and `expected_max`, which are absent. `enriched_signals` matches and is what `get-throttled-pods` uses.

**Errors** (400 or 503): `{"status":"error","error":"...","details":"...","code":"..."}`. Codes include `INVALID_REQUEST`, `KSERVE_UNAVAILABLE`, `MODEL_NOT_FOUND`, and `ANALYSIS_FAILED`. A missing Prometheus client does not fail the call; the handler uses default features and still calls KServe.

#### `POST /api/v1/alerts`

Alertmanager webhook for the SelfHealingPolicy engine. The route is registered only when `ENABLE_POLICY_ENGINE=true`. Any application is one object in the policy file mounted at `POLICY_CONFIG_PATH`. The engine does not hard-code alert names, namespaces, or models.

**Request** (Alertmanager v4 webhook):
```json
{
  "version": "4",
  "status": "firing",
  "alerts": [
    {
      "status": "firing",
      "labels": {
        "alertname": "CheckoutQueueLag",
        "namespace": "checkout"
      },
      "startsAt": "2026-10-21T20:00:00.000Z",
      "fingerprint": "abc123"
    }
  ]
}
```

`labels.namespace` is matched to a policy. If it is absent, `labels.kubernetes_namespace` is used. Only `status=firing` can select a repair. Resolved alerts are ignored.

For each match the engine queries Prometheus for that policy's `model.featureColumns`, in order, and posts one instance to:

`http://{service}.{namespace}.svc.cluster.local:{port}/v1/models/{model.name}:predict`

`service` defaults to `{model.name}-predictor` and `namespace` defaults to `KSERVE_NAMESPACE`. A policy may set `model.service` and `model.namespace`. Feature columns use a built-in PromQL catalog (`cpu_usage`, `memory_usage`, `restart_count`, `response_time`, `error_rate`) unless the policy sets `model.featureQueries`. Query templates may use `$namespace`, `$pod`, and `$deployment`.

Verdict `-1` runs the `repairs[]` entry for that alert. Verdict `1` logs `model says normal, no action` and does not repair. Any other verdict, a query error, or a model error does not repair. Actions always run in the policy namespace.

Standard action types: `restart_pod`, `rollback_deployment`, `scale_deployment`, `delete_resource`, `apply_resource`, `exec_command`, `patch_resource`.

**Response** (200 OK, repair executed):
```json
{
  "status": "processed",
  "matched_policies": 1,
  "actions_taken": [
    {
      "policy": "checkout",
      "alert": "CheckoutQueueLag",
      "model": "checkout-detector",
      "verdict": -1,
      "action": "scale_deployment",
      "result": "success"
    }
  ]
}
```

**Response** (200 OK, no matching policy):
```json
{
  "status": "processed",
  "matched_policies": 0,
  "actions_taken": []
}
```

**Error Response** (400 Bad Request):
```json
{
  "status": "error",
  "error": "Invalid Alertmanager webhook format"
}
```

Metrics: `policy_engine_alerts_received_total`, `policy_engine_model_scores_total`, `policy_engine_repairs_executed_total`.

For details, see:
- MCP ADR-006: Integration Architecture
- MCP ADR-014: Go Coordination Engine Integration (to be created)

## 2. Downstream ML Integration

The Go coordination engine supports two ML integration modes:

### 2.1 KServe Integration (Recommended - ADR-039)

The coordination engine calls KServe InferenceServices directly for ML predictions.
This is the recommended approach for both vanilla Kubernetes and OpenShift deployments.

**Configuration**:
```yaml
env:
  - name: ENABLE_KSERVE_INTEGRATION
    value: "true"
  - name: KSERVE_NAMESPACE
    value: "self-healing-platform"
  - name: KSERVE_ANOMALY_DETECTOR_SERVICE
    value: "anomaly-detector-predictor"
  - name: KSERVE_PREDICTIVE_ANALYTICS_SERVICE
    value: "predictive-analytics-predictor"
```

**KServe Service DNS**:
```
http://<service-name>.<namespace>.svc.cluster.local/v1/models/<model>:predict
```

#### KServe v1 Prediction API

**Required Endpoints**:
- `GET /v1/models/<model>` - Model metadata
- `POST /v1/models/<model>:predict` - Inference
- `GET /v1/models` - List available models

#### `POST /v1/models/anomaly-detector:predict`
Detect anomalies using KServe InferenceService.

**Request Body** (KServe v1 format):
```json
{
  "instances": [
    [0.5, 1.2, 0.8],
    [0.3, 0.9, 1.1]
  ]
}
```

**Response** (200 OK):
```json
{
  "predictions": [-1, 1],
  "model_name": "anomaly-detector",
  "model_version": "v2"
}
```

**Prediction Values**:
- `-1` = Anomaly detected
- `1` = Normal (no anomaly)

#### `POST /v1/models/predictive-analytics:predict`
Predict future issues using KServe InferenceService.

**Request Body**:
```json
{
  "instances": [
    [0.75, 0.80, 0.02],
    [0.85, 0.90, 0.05]
  ]
}
```

**Response** (200 OK):
```json
{
  "predictions": [-1, -1],
  "model_name": "predictive-analytics",
  "model_version": "v1"
}
```

#### `GET /v1/models/anomaly-detector`
Get model metadata.

**Response** (200 OK):
```json
{
  "name": "anomaly-detector",
  "versions": ["v1", "v2"],
  "platform": "sklearn"
}
```

### Go KServe Client Implementation

```go
// internal/integrations/kserve_client.go
package integrations

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"
)

type KServeClient struct {
    anomalyDetectorURL     string
    predictiveAnalyticsURL string
    httpClient             *http.Client
}

type KServeV1Request struct {
    Instances [][]float64 `json:"instances"`
}

type KServeV1Response struct {
    Predictions  []int  `json:"predictions"`
    ModelName    string `json:"model_name,omitempty"`
    ModelVersion string `json:"model_version,omitempty"`
}

// DetectAnomalies calls KServe anomaly detector
func (c *KServeClient) DetectAnomalies(
    ctx context.Context,
    instances [][]float64,
) (*AnomalyDetectionResult, error) {
    endpoint := fmt.Sprintf("%s/v1/models/anomaly-detector:predict", 
        c.anomalyDetectorURL)
    
    req := &KServeV1Request{Instances: instances}
    // ... implementation
}
```

### 2.2 Legacy ML Service (outbound client)

These paths are **not** routes on the coordination engine, and MCP does not call them. `internal/integrations/ml_client.go` calls them on `ML_SERVICE_URL` when that client is used. Set `ML_SERVICE_URL` (for example `http://aiops-ml-service:8080`). Anomaly analysis **on this server** is `POST /api/v1/anomalies/analyze`.

| Client method | Outbound path | Request fields | Response fields |
|---------------|---------------|----------------|-----------------|
| `DetectAnomalies` | `POST {ML_SERVICE_URL}/api/v1/anomaly/detect` | `metrics[]` (`name`, `value`, `timestamp`, `labels`), optional `threshold` | `anomalies[]` (`metric_name`, `value`, `expected`, `deviation`, `severity`, `confidence`, `timestamp`, `reason`), `summary` (`total`, `anomalies_found`, `high_severity`, `confidence`) |
| `Predict` | `POST {ML_SERVICE_URL}/api/v1/prediction/predict` | `historical_metrics[]`, `predict_ahead`, optional `include_confidence` | `predictions[]` (`metric_name`, `predicted_value`, `confidence`, `timestamp`, `trend`, `risk`), `summary` (`time_range`, `metrics_count`, `overall_confidence`) |
| `AnalyzePatterns` | `POST {ML_SERVICE_URL}/api/v1/pattern/analyze` | `metrics[]`, `time_range.start`, `time_range.end`, optional `analysis_type` | `patterns[]` (`type`, `description`, `metrics`, `confidence`, `start_time`, `end_time`), `insights`, `summary` (`patterns_found`, `confidence`) |
| `HealthCheck` | `GET {ML_SERVICE_URL}/health` | none | HTTP status from the ML service |

### 2.3 Routes on this coordination engine

#### `POST /api/v1/predict`
Get time-specific resource usage predictions using KServe ML models and Prometheus metrics.

Sends 5 base metrics (`cpu_usage`, `memory_usage`, `disk_usage`, `network_in`, `network_out`) to the `predictive-analytics` model. When feature engineering is enabled (`ENABLE_FEATURE_ENGINEERING=true`, the default), a 3200+ engineered feature vector is built from Prometheus range queries instead.

**Request Body**:
```json
{
  "hour": 15,
  "day_of_week": 3,
  "namespace": "production",
  "deployment": "payment-service",
  "pod": "",
  "scope": "deployment",
  "model": "predictive-analytics"
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `hour` | int | Yes | Hour of day, 0-23 |
| `day_of_week` | int | Yes | Day of week, 0=Monday, 6=Sunday |
| `namespace` | string | No | Namespace filter |
| `deployment` | string | No | Deployment filter |
| `pod` | string | No | Pod filter |
| `scope` | string | No | One of: `pod`, `deployment`, `namespace`, `cluster` (default: inferred) |
| `model` | string | No | KServe model name (default: `predictive-analytics`) |

**Response** (200 OK):
```json
{
  "status": "success",
  "scope": "deployment",
  "target": "production/payment-service",
  "predictions": {
    "cpu_percent": 74.5,
    "memory_percent": 81.2
  },
  "current_metrics": {
    "cpu_rolling_mean": 68.2,
    "memory_rolling_mean": 74.5,
    "disk_usage": 0.45,
    "network_in": 0.10,
    "network_out": 0.08,
    "timestamp": "2026-01-12T14:30:00Z",
    "time_range": "24h"
  },
  "model_info": {
    "name": "predictive-analytics",
    "version": "1.0.0",
    "confidence": 0.92
  },
  "target_time": {
    "hour": 15,
    "day_of_week": 3,
    "iso_timestamp": "2026-01-15T15:00:00Z"
  }
}
```

There is no top-level `trend` or `recommendation`. MCP reads both. MCP also calls `GET /api/v1/capacity/trends?namespace=`, which is not registered. Forecast fields are `trending.forecasted_exhaustion_days` and `trending.recommended_replica_increase` on `GET /api/v1/capacity/namespace/{namespace}`. The trends call is best-effort, so the prediction still returns and MCP `capacity_forecast` stays empty.

**Error Responses**:

| Code | HTTP Status | Condition |
|------|-------------|-----------|
| `INVALID_REQUEST` | 400 | Malformed JSON, invalid hour/day_of_week, bad scope |
| `KSERVE_UNAVAILABLE` | 503 | KServe integration not configured |
| `MODEL_NOT_FOUND` | 503 | Requested model not registered in KServe |
| `PREDICTION_FAILED` | 503 | Model returned an error or empty response |

#### `POST /api/v1/investigate/rca`
Registered on this coordination engine. Deep root-cause analysis correlating Istio VirtualService misconfigs, NetworkPolicy violations, and pod events (ADR-021, Issue #72).

**Request Body**:
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
| `service` | string | Yes | Name of the service to investigate |
| `namespace` | string | Yes | Kubernetes namespace |
| `start_time` | string (RFC 3339) | Yes | Start of investigation window |
| `end_time` | string (RFC 3339) | Yes | End of investigation window (max 7 days) |

**Response** (200 OK):
```json
{
  "status": "success",
  "service": "my-app",
  "namespace": "production",
  "time_range": { "start": "...", "end": "..." },
  "root_causes": [
    {
      "signal_type": "pod_event",
      "description": "OOMKilled: Container exceeded memory limit on pod/my-app-abc123",
      "evidence": {
        "event_type": "Warning",
        "reason": "OOMKilled",
        "count": 3,
        "message": "Container exceeded memory limit"
      },
      "confidence": 0.92,
      "remediation_steps": [
        "Increase container memory limits",
        "Profile application for memory leaks"
      ]
    }
  ],
  "confidence_score": 0.89,
  "affected_components": ["pod/my-app-abc123", "networkpolicy/deny-all"],
  "istio_available": false,
  "correlator_stats": [
    { "name": "pod_events", "findings": 1, "duration_ms": 45 },
    { "name": "network_policy", "findings": 0, "duration_ms": 32 },
    { "name": "istio_virtual_service", "findings": 0, "duration_ms": 1 }
  ]
}
```

**Signal Types**: `pod_event`, `network_policy`, `istio_virtual_service`

**Error Codes**: `INVALID_REQUEST`, `ANALYSIS_FAILED`

**Notes**:
- Istio correlation is optional — gracefully skipped when Istio CRDs are not installed.
- All three correlators run in parallel with a 25-second timeout each.
- Confidence scores range from 0.0 to 1.0 and are weighted by signal type.

## Alert Notification Sinks (ADR-022, Issue #75)

When the anomaly analysis endpoint (`POST /api/v1/anomalies/analyze`) detects
anomalies at or above the configured severity threshold, alerts are dispatched
asynchronously to all configured sinks.  Dispatching never delays the HTTP
response.

### Supported Sinks

| Sink | Env Var | Protocol |
|---|---|---|
| Slack | `KUBEHEAL_SLACK_WEBHOOK_URL` | POST to incoming webhook URL |
| PagerDuty | `KUBEHEAL_PAGERDUTY_ROUTING_KEY` | POST to Events API v2 (`/v2/enqueue`) |
| Alertmanager | `KUBEHEAL_ALERTMANAGER_URL` | POST to `{url}/api/v2/alerts` |

### Configuration

| Env Var | Default | Description |
|---|---|---|
| `KUBEHEAL_SLACK_WEBHOOK_URL` | (empty) | Slack incoming webhook URL; empty = disabled |
| `KUBEHEAL_PAGERDUTY_ROUTING_KEY` | (empty) | PagerDuty Events API v2 routing key; empty = disabled |
| `KUBEHEAL_ALERTMANAGER_URL` | (empty) | Alertmanager base URL; empty = disabled |
| `KUBEHEAL_ALERT_SEVERITY_THRESHOLD` | `critical` | Minimum severity to trigger alerts (`info`, `warning`, `critical`) |

### Behavior

- Alerts are **fire-and-forget**: sink errors are logged at WARN level but
  never propagated to the API response.
- Each sink runs in its own goroutine with a 10-second timeout.
- If no sinks are configured, the dispatcher is a no-op.
- Severity threshold comparison: `info < warning < critical`.  Setting the
  threshold to `warning` dispatches alerts for both `warning` and `critical`.

## MCP client compatibility

Checked against KubeHeal/openshift-cluster-health-mcp commit `90564fa`.

- Disk exhaustion (`GET /api/v1/predict/disk-exhaustion`), right-sizing (`GET /api/v1/recommendations/rightsizing`), and RCA (`POST /api/v1/investigate/rca`) match the MCP client.
- Incident list filters, anomaly JSON names, the create-incident response shape, predict `trend`, and `GET /api/v1/capacity/trends` do not.
- KServe integration is the recommended approach (ADR-039).
- Legacy `ML_SERVICE_URL` is deprecated but still supported.

## References

- [KServe v1 Protocol](https://kserve.github.io/website/latest/modelserving/data_plane/v1_protocol/)
- ADR-039: User-Deployed KServe Models (platform repository)


