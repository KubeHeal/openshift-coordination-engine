# Coordination Engine API Documentation

## Overview

The coordination engine exposes a REST API for remediation, detection, prediction, and health. This page lists every route the process registers and the behavior of the handlers that serve them.

Against KubeHeal/openshift-cluster-health-mcp commit `90564fa`, disk exhaustion, right-sizing, and RCA match the client. Incident list filters, anomaly JSON names, the create-incident response shape, predict `trend`, and `GET /api/v1/capacity/trends` do not. The route notes record what that client sends.

**API server**: `http://coordination-engine:8080` (`PORT`, default `8080`)

**Metrics server**: `http://coordination-engine:9090` (`METRICS_PORT`, default `9090`)

**Content type**: JSON for successful API responses. Some error paths write plain text. See [Error responses](#error-responses).

Routes under `/api/v1` are registered in `cmd/coordination-engine/main.go` and the `RegisterRoutes` methods in `pkg/api/v1`. The generated list is `docs/swagger.yaml`.

## Route index

| Method | Path | Registered |
|--------|------|------------|
| GET | `/health` | Always, on the API port |
| GET | `/api/v1/health` | Always |
| GET | `/metrics` | Always, on the metrics port |
| POST | `/api/v1/remediation/trigger` | Always |
| GET | `/api/v1/workflows/{id}` | Always |
| GET, POST | `/api/v1/incidents` | Always |
| POST | `/api/v1/recommendations` | Always |
| POST | `/api/v1/predict` | Always |
| GET | `/api/v1/predict/disk-exhaustion` | Always |
| GET | `/api/v1/predict/memory-leak` | Always |
| GET | `/api/v1/recommendations/rightsizing` | Always |
| GET | `/api/v1/detect/deployment/{namespace}/{name}` | Always |
| GET | `/api/v1/detect/statefulset/{namespace}/{name}` | Always |
| GET | `/api/v1/detect/daemonset/{namespace}/{name}` | Always |
| POST | `/api/v1/detect/cache/clear` | Always |
| GET | `/api/v1/detect/cache/stats` | Always |
| POST | `/api/v1/coordination/trigger` | Always |
| GET | `/api/v1/coordination/workflows` | Always |
| GET | `/api/v1/coordination/workflows/{id}` | Always |
| GET | `/api/v1/capacity/namespace/{namespace}` | Always |
| GET | `/api/v1/capacity/cluster` | Always |
| POST | `/api/v1/anomalies/analyze` | Always |
| POST | `/api/v1/investigate/rca` | Always |
| POST | `/api/v1/alerts` | Only when `ENABLE_POLICY_ENGINE=true` |
| POST | `/api/v1/detect` | Only when KServe integration is enabled |
| GET | `/api/v1/models` | Only when KServe integration is enabled |
| GET | `/api/v1/models/{model}/health` | Only when KServe integration is enabled |
| GET | `/api/docs/` | Only when `ENABLE_SWAGGER_UI=true` |

`POST /api/v1/anomaly/detect` and `POST /api/v1/pattern/analyze` are not MCP calls and are not routes on this server. The legacy client in `internal/integrations/ml_client.go` calls those paths on `ML_SERVICE_URL`. Anomaly analysis on this server is `POST /api/v1/anomalies/analyze`.

`GET /api/v1/cluster/status` is not registered. MCP `GetClusterStatus` calls it, and no tool calls that method.

## Authentication

The API does not require authentication when reached from inside the cluster. Expose it through an OpenShift Route when clients are outside the cluster.

## Common headers

### Request headers

| Header | Description | Required |
|--------|-------------|----------|
| `Content-Type` | `application/json` for bodies that the handler parses as JSON | For those POST bodies |
| `X-Request-ID` | Client trace id | No |

### Response headers

| Header | Description |
|--------|-------------|
| `Content-Type` | `application/json` for handler JSON. `text/plain; charset=utf-8` when the handler uses `http.Error` |
| `X-Request-ID` | The request id, generated when the client did not send one |

## Health check endpoints

### GET /health

Lightweight liveness response. It does not check Kubernetes, the ML service, or RBAC. Use it for probes.

**Response** (200 OK):

```json
{
  "status": "ok",
  "version": "1.2.0"
}
```

`status` is always `"ok"` when the process can answer. `version` is the binary version.

### GET /api/v1/health

Dependency health. This is the rich health check.

**Response fields**:

| Field | Meaning |
|-------|---------|
| `status` | `healthy`, `degraded`, or `unhealthy` |
| `timestamp` | Time of this check |
| `version` | Binary version |
| `uptime_seconds` | Process uptime |
| `dependencies.kubernetes` | API-server list check. `down` makes the overall status `unhealthy` |
| `dependencies.ml_service` | `GET {ML_SERVICE_URL}/health`. Unreachable is `degraded` and does not by itself make the process unhealthy |
| `rbac.status` | `ok` or `down` |
| `rbac.critical_ok` | Result of the critical-permission check |
| `rbac.message` | `Critical permissions verified`, or the missing-permission error |
| `rbac.permissions_total`, `permissions_ok`, `permissions_failed` | Present on the struct. This handler does not fill them, so they are `0` |
| `details.service_account` | The string `self-healing-operator` |
| `details.namespace` | The RBAC verifier object. That type has no exported fields, so the JSON value is `{}`, not a namespace name |

Each dependency object has `name`, `status` (`ok`, `degraded`, or `down`), `message`, `latency_ms`, and `checked_at`.

**HTTP status**:

- `200` when `status` is `healthy` or `degraded`
- `503` when `status` is `unhealthy` (Kubernetes `down`, or `rbac.critical_ok` is false)

```json
{
  "status": "healthy",
  "timestamp": "2025-12-18T19:00:00Z",
  "version": "1.2.0",
  "uptime_seconds": 3600,
  "dependencies": {
    "kubernetes": {
      "name": "kubernetes",
      "status": "ok",
      "message": "Connected",
      "latency_ms": 15,
      "checked_at": "2025-12-18T19:00:00Z"
    },
    "ml_service": {
      "name": "ml_service",
      "status": "ok",
      "message": "Connected",
      "latency_ms": 45,
      "checked_at": "2025-12-18T19:00:00Z"
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

There is no `argocd` dependency on this response.

## Metrics endpoint

### GET /metrics

Prometheus text on the metrics port (`METRICS_PORT`, default `9090`), not on `8080`.

```http
GET /metrics HTTP/1.1
Host: coordination-engine:9090
```

## Remediation

The sections below describe the handlers as they run today.

### POST /api/v1/remediation/trigger

Starts a remediation workflow.

**Request fields**:

| Field | Required |
|-------|----------|
| `incident_id` | Yes |
| `namespace` | Yes |
| `resource.kind` | Yes |
| `resource.name` | Yes |
| `issue.type` | Yes |
| `issue.description` | No |
| `issue.severity` | No |

**Success**: `202 Accepted`

```json
{
  "workflow_id": "wf-67890abc",
  "status": "pending",
  "deployment_method": "argocd",
  "estimated_duration": "1m"
}
```

`status` at accept time is `pending`. The workflow then runs in the background and can move to `in_progress`, `completed`, or `failed`. `deployment_method` is the detected method (`argocd`, `helm`, `operator`, `manual`) or `unknown` when detection fails. `estimated_duration` is the checkpoint timeout formatted in whole minutes. The default timeout is one minute, so the value is `"1m"`. `dry_run` is not a handler field. If the MCP tool sets it, the workflow still starts.

When `DATA_DIR` is set, the workflow is written to `{DATA_DIR}/workflows/<id>.json` before the response returns and is reloaded on startup. An empty `DATA_DIR` keeps the workflow in memory. If `incident_id` already exists in the incident store, the handler sets `workflow_id` on that incident. An unknown `incident_id` still returns `202`.

**Errors**: plain text from `http.Error` (`400` for a bad body or a missing required field, `500` when triggering fails).

### GET /api/v1/workflows/{id}

Returns one remediation workflow.

**Success**: `200` with `WorkflowResponse`:

| Field | Notes |
|-------|--------|
| `id`, `incident_id`, `status` | `status` is `pending`, `in_progress`, `completed`, or `failed` |
| `deployment_method` | Detected method |
| `namespace`, `resource_name`, `resource_kind`, `issue_type` | Copied from the workflow |
| `remediator` | Set once execution starts |
| `error_message` | Set when execution fails |
| `created_at` | RFC3339 |
| `started_at`, `completed_at` | RFC3339, omitted until set |
| `duration` | Go duration string, set only after completion |
| `steps` | Step objects: `order`, `description`, `status`, optional timestamps and `error_message` |
| `layers` | One layer from the resource kind. `Deployment`, `StatefulSet`, `DaemonSet`, and `Pod` are `application`. Other kinds are `platform`. |
| `checkpoints` | One checkpoint after the remediation step. `layer`, `after_step`, `status` (`pending`, `passed`, or `failed`), and `checks` (`pods_running`, `endpoints_healthy`). Status is `pending` until execution finishes, then `passed` or `failed`. |

MCP does not call this route. `GET /api/v1/coordination/workflows/{id}` is a different object and also includes `layers` and `checkpoints` on `remediation_plan`. MCP does not call that route either.

**Missing id**: `404` with plain text `Workflow not found`.

When `DATA_DIR` is set, workflows are loaded from `{DATA_DIR}/workflows`. An empty `DATA_DIR` keeps them in memory.

### GET /api/v1/incidents

Lists incidents from two sources.

**Query parameters**:

| Parameter | Effect |
|-----------|--------|
| `namespace` | Rows whose `namespace` or `target` equals this value. Empty matches every namespace. |
| `severity` | Exact match on the row severity (`low`, `medium`, `high`, `critical`). Empty or `all` matches every severity. |
| `status` | Exact match on the row status. Empty or `all` matches every status. |
| `limit` | Maximum rows returned. Defaults to 50 when omitted, invalid, or not positive. |

`offset` is not read. Filters apply to the combined stored and workflow list. `total` is the number of rows that matched before `limit`. The returned array is the page. The MCP incident list, `cluster://incidents`, and remediation-history send `severity=all`, `status`, `limit`, and `offset`. `severity=all` and `status=all` return every severity and status. `status=completed` does not match workflow rows, whose status is `remediated`. The body field is `total`. MCP reads `summary.*`, `priority`, `action_type`, `confidence`, `parameters`, `started_at`, `completed_at`, `duration_seconds`, and `tags`, which are absent.

**Success**: `200`

```json
{
  "incidents": [],
  "total": 0
}
```

Stored rows (`source` `manual`) include `id`, `title`, `description`, `target`, `severity`, `status` (`active`, `resolved`, or `cancelled`), `created_at`, `affected_resources`, `labels`, and `workflow_id` when one is already on the record. They do not include `namespace`.

Workflow rows (`source` `workflow`) include `id` (the workflow's incident id), `namespace`, `target` (same as the namespace), `resource` (`kind/name`), `issue_type`, `severity` (the workflow severity, or the stored incident severity when the workflow has none), `created_at`, `workflow_id`, and `status` (`remediated`, `failed`, or `in_progress`).

Stored incidents persist when `DATA_DIR` is set. Workflow rows come from the same store: `{DATA_DIR}/workflows` when `DATA_DIR` is set, and memory when it is empty.

### POST /api/v1/incidents

Creates a stored incident.

**Request fields**: `title` (required, max 200), `description` (required, max 2000), `severity` (required: `low`, `medium`, `high`, `critical`), `target` (required, max 100), optional `affected_resources` and `labels`.

MCP sends `affectedResources`, `source`, `createdBy`, `correlationId`, `externalId`, `confidence`, and `parameters`. The handler ignores those names. It reads `affected_resources` and requires `target`. MCP does not require `target`, so a create without `target` is `400`.

**Success**: `201`

```json
{
  "status": "success",
  "incident_id": "inc-67890abc",
  "created_at": "2025-12-18T19:00:00Z",
  "incident": {},
  "message": "Incident created successfully"
}
```

`incident` is the stored record. New incidents get status `active`. `title`, `description`, `severity`, and status `active` are inside `incident`. Top-level `status` is `"success"`. There is no `priority`. MCP reads those fields at the top level. `target` is the value `GET /api/v1/incidents?namespace=` matches.

**Errors**: `400` JSON `{"status":"error","error":"..."}`.

## Recommendations

### POST /api/v1/recommendations

The MCP tool `get-remediation-recommendations` does not call this route. It calls `POST /api/v1/anomalies/analyze`.

**Request** (body may be empty): `timeframe` (`1h`, `6h`, `24h`; default `6h`), `include_predictions` (default true), `confidence_threshold` (default `0.7`, range 0–1), `namespace`.

**Success**: `200` with `status`, `timestamp`, `timeframe`, `recommendations`, `total_recommendations`, `ml_enabled`, and optional `message`.

Each recommendation has `id`, `type` (`proactive` or `reactive`), `issue_type`, `target`, `namespace`, `severity`, `confidence`, optional `predicted_time`, `recommended_actions`, `evidence`, `source` (`ml_prediction`, `historical_analysis`, or `pattern_detection`), and optional `related_incident_id`.

**Errors**: `400` `{"status":"error","error":"..."}`.

ML recommendations are included only when KServe is configured and `include_predictions` is true.

### GET /api/v1/recommendations/rightsizing

Compares usage with requests and limits. Requires a reachable Prometheus client.

**Query**: `namespace`, `pod` (prefix match), `window` (default `30d`; the value is passed through to Prometheus).

**Success**: `200` with `status` `success`, `timestamp`, `namespace`, `analysis_window`, `recommendations`, `over_provisioned_count`, `under_provisioned_count`, `right_sized_count`.

Each recommendation includes namespace, pod, container, current and recommended CPU and memory, `cpu_sizing`, `memory_sizing` (`over-provisioned`, `under-provisioned`, or `right-sized`), and optional `throttle_rate_pct`.

**Errors**: `503` when Prometheus is unavailable, `500` when the query fails. Body: `{"status":"error","error":"..."}`.

## Prediction

### POST /api/v1/predict

Time-specific CPU and memory prediction. The route is always registered. The call needs a KServe client and a known model.

**Request**: `hour` (0–23) and `day_of_week` (0 = Monday through 6 = Sunday) are required. Optional `namespace`, `deployment`, `pod`, `scope` (`pod`, `deployment`, `namespace`, `cluster`; inferred when omitted), `model` (default `predictive-analytics`). `scope=pod` requires `pod` and `namespace`. `scope=deployment` requires `deployment` and `namespace`.

**Success**: `200` with `status` `success`, `scope`, `target`, `predictions.cpu_percent`, `predictions.memory_percent`, `current_metrics`, `model_info` (`name`, `version`, `confidence`), and `target_time` (`hour`, `day_of_week`, `iso_timestamp`).

`current_metrics.cpu_rolling_mean` and `memory_rolling_mean` are percentages. `disk_usage`, `network_in`, and `network_out` are ratios from 0 to 1. If Prometheus or feature engineering fails, the handler continues with default metrics.

There is no top-level `trend` or `recommendation`. MCP reads both. MCP also calls `GET /api/v1/capacity/trends?namespace=`, which is not registered. That call is best-effort, so this prediction still returns and MCP `capacity_forecast` stays empty. Forecast fields are `trending.forecasted_exhaustion_days` and `trending.recommended_replica_increase` on `GET /api/v1/capacity/namespace/{namespace}`.

**Errors**: `400` or `503` JSON `{"status":"error","error":"...","details":"...","code":"..."}`.

| `code` | HTTP | When |
|--------|------|------|
| `INVALID_REQUEST` | 400 | Bad JSON, hour, day, or scope |
| `KSERVE_UNAVAILABLE` | 503 | KServe client is not configured |
| `MODEL_NOT_FOUND` | 503 | Model name is not registered |
| `PREDICTION_FAILED` | 503 | Model call failed or returned an empty body |

### GET /api/v1/predict/disk-exhaustion

**Query**: `node`, `mountpoint`.

**Success**: `200` with `status` `success`, `timestamp`, optional `scope` (the node filter), `results`, `critical_count`, `warning_count`.

Each result has `node`, `mountpoint`, `available_bytes`, `total_bytes`, `used_percent`, `daily_fill_rate_bytes`, `days_until_full` (`-1` when stable or shrinking), `urgency` (`critical`, `warning`, `info`, or `stable`), and optional `projected_full_date`.

**Errors**: `503` or `500`, `{"status":"error","error":"..."}`. `503` when Prometheus is not available.

### GET /api/v1/predict/memory-leak

**Query**: `namespace`.

**Success**: `200` with `status` `success`, `timestamp`, optional `namespace`, `results`, `leak_count`.

Each result has `namespace`, `pod`, `container`, `current_memory_bytes`, `daily_growth_bytes`, `growth_r_squared`, `leak_detected`, and `confidence`.

**Errors**: same shape as disk exhaustion. `503` when Prometheus is not available.

## Detection

These routes classify how a workload was deployed. They are separate from the KServe `POST /api/v1/detect` proxy.

### GET /api/v1/detect/deployment/{namespace}/{name}

### GET /api/v1/detect/statefulset/{namespace}/{name}

### GET /api/v1/detect/daemonset/{namespace}/{name}

**Success**: `200`

```json
{
  "success": true,
  "data": {
    "method": "argocd",
    "confidence": 0.95,
    "source": "annotation",
    "namespace": "production",
    "resource_name": "payment-service",
    "resource_kind": "Deployment"
  },
  "message": "Deployment method detected successfully"
}
```

`data` also includes `details` and `detected_at` when set. `method` is `argocd`, `helm`, `operator`, `manual`, or `unknown`. `message` is `Deployment method detected successfully`, `StatefulSet method detected successfully`, or `DaemonSet method detected successfully`, matching the route.

**Errors**: `400`, `404`, or `500` with `{"success":false,"error":"..."}`.

### POST /api/v1/detect/cache/clear

**Success**: `200` `{"success":true,"message":"Cache cleared successfully"}`.

### GET /api/v1/detect/cache/stats

**Success**: `200` `{"success":true,"data":{"total_entries":0,"valid_entries":0,"expired_entries":0,"ttl_seconds":0}}`.

## Coordination

Multi-layer workflows are a second, in-memory store. They are not the remediation workflows from `GET /api/v1/workflows/{id}`.

### POST /api/v1/coordination/trigger

**Request**: `incident_id`, `description`, and at least one `resources` entry. Each resource has `kind`, `name`, `namespace`, and `issue`.

**Success**: `202`

```json
{
  "workflow_id": "cwf-123456789",
  "status": "pending",
  "affected_layers": ["application"],
  "root_cause_layer": "application",
  "estimated_steps": 1
}
```

**Errors**: plain text, `400` or `500`.

### GET /api/v1/coordination/workflows/{id}

**Success**: `200` with the stored object: `id`, `incident_id`, `status` (`pending`, `executing`, `completed`, `failed`), `layered_issue`, `remediation_plan` (includes `layers` and `checkpoints`), `execution_result`, `created_at`, `started_at`, `completed_at`, `error_message`. MCP does not call this route.

**Missing id**: `404` plain text `workflow not found`.

### GET /api/v1/coordination/workflows

**Success**: `200` `{"workflows":[],"total":0}`. No query filters. Lost on restart.

## Capacity

### GET /api/v1/capacity/namespace/{namespace}

**Query**: `include_trending` (default true), `include_infrastructure` (default false), `window` (default `7d`; the value is passed through).

**Success**: `200` with `status` `success`, `namespace`, `timestamp`, `quota`, `current_usage`, `available`, and `trending` or `infrastructure_impact` when requested and Prometheus can answer. `trending` includes `forecasted_exhaustion_days` and `recommended_replica_increase`. `GET /api/v1/capacity/trends` is not registered.

**Errors**: `400` or `500` `{"status":"error","error":"<HTTP status text>","message":"..."}`.

### GET /api/v1/capacity/cluster

**Success**: `200` with `status` `success`, `scope` `cluster`, `timestamp`, `cluster_capacity`, `cluster_usage`, `namespaces`, and optional `infrastructure`.

**Errors**: `500`, same error object as the namespace route.

## Anomaly analysis

### POST /api/v1/anomalies/analyze

This is the anomaly route on this server.

**Request**: optional `time_range` (`1h`, `6h`, `24h`, `7d`; default `1h`), `namespace`, `deployment`, `pod`, `label_selector`, `threshold` (0–1; default `0.7`), `model_name` (default `anomaly-detector`).

MCP sends `timeRange`, `modelName`, `labelSelector`, `metrics`, and `includePredictions`. The handler binds `time_range`, `model_name`, and `label_selector`, so the camelCase names are dropped. The window stays `1h` and the model stays `anomaly-detector`.

**Success**: `200` with `status` `success`, `time_range`, `scope`, `model_used`, `anomalies_detected`, `anomalies`, `summary`, `recommendation`, `features`, and optional `enriched_signals`.

The recommendation field is singular `recommendation`. MCP reads `recommendations`, `alerts`, `analyzed_at`, and anomaly `metric`, `value`, `type`, `expected_min`, and `expected_max`, which are absent. `enriched_signals` matches and is what `get-throttled-pods` uses.

If Prometheus cannot build the feature vector, the handler uses default features and still calls the model. Alerts are dispatched after the response is written when a sink is configured and the severity meets `KUBEHEAL_ALERT_SEVERITY_THRESHOLD`.

**Errors**: `400` or `503`

```json
{
  "status": "error",
  "error": "KServe integration not enabled",
  "details": "KServe client is not configured",
  "code": "KSERVE_UNAVAILABLE"
}
```

Other `code` values: `INVALID_REQUEST`, `MODEL_NOT_FOUND`, `ANALYSIS_FAILED`. `PROMETHEUS_UNAVAILABLE` is defined and is not what a missing Prometheus client returns today; that path falls back to defaults.

## Root cause analysis

### POST /api/v1/investigate/rca

**Request**: `service`, `namespace`, `start_time`, `end_time`. Times are RFC3339. `end_time` must be after `start_time`, and the window must be at most 7 days.

**Success**: `200` with `status`, `service`, `namespace`, `time_range` (`start`, `end`), `root_causes`, `confidence_score`, `affected_components`, `istio_available`, and optional `correlator_stats`.

Each root cause has `signal_type` (`pod_event`, `network_policy`, or `istio_virtual_service`), `description`, `evidence`, `confidence`, and `remediation_steps`. `correlator_stats[].duration_ms` is the millisecond count stored on the result.

**Errors**: `400` `{"status":"error","error":"...","details":"...","code":"INVALID_REQUEST"}`. Processing failures use `code` `ANALYSIS_FAILED`.

## Alerts

### POST /api/v1/alerts

Registered only when `ENABLE_POLICY_ENGINE=true`. The process also requires `PROMETHEUS_URL` and `POLICY_CONFIG_PATH`. With the flag false (the default), this path is not registered.

Accepts an Alertmanager webhook. The handler reads `status` and `alerts[]` (`status`, `labels`, `startsAt`, `fingerprint`). Other webhook fields are ignored. Body limit is 2 MiB.

**Success**: `200`

```json
{
  "status": "processed",
  "matched_policies": 0,
  "actions_taken": []
}
```

`actions_taken` entries can include `policy`, `alert`, `model`, `verdict`, `action`, `result`, and `error`. Unmatched alerts still return `200` with `matched_policies` `0`.

**Errors**: `400` `{"status":"error","error":"Invalid Alertmanager webhook format"}`.

Policy matching and repair behavior are described in [API-CONTRACT.md](../API-CONTRACT.md).

## KServe proxy

Registered only when KServe integration is enabled and the proxy client starts. That is `ENABLE_KSERVE_INTEGRATION=true` (the default) and a successful client init. If init fails, these three routes are absent. `POST /api/v1/predict` and `POST /api/v1/anomalies/analyze` stay registered and return `503` when the client is missing.

### POST /api/v1/detect

Proxies one prediction to a named model.

**Request**: `model` (required) and `instances` (required, array of float arrays).

**Success**: `200` with `predictions` (integers), `model_name`, and optional `model_version`.

**Errors**: `{"error":"...","success":false}` with `400` (bad body, missing field, or non-JSON content type), `404` (unknown model), `503` (model unavailable), or `500`.

### GET /api/v1/models

**Success**: `200` `{"models":["anomaly-detector"],"count":1}`.

### GET /api/v1/models/{model}/health

**Success**: `200` with `model`, `status`, `service`, `namespace`, and optional `message`.

**Unknown model**: `404` `{"error":"...","success":false}`.

## Swagger UI

### GET /api/docs/

Served only when `ENABLE_SWAGGER_UI=true` (default false). The UI loads `/api/docs/doc.json`. With the flag false, the prefix is not registered.

## Error responses

Handlers do not share one error envelope.

| Shape | Who uses it |
|-------|-------------|
| Plain text (`http.Error`) | `POST /api/v1/remediation/trigger`, `GET /api/v1/workflows/{id}`, coordination trigger, coordination workflow GET |
| `{"status":"error","error":"..."}` | `POST /api/v1/incidents`, recommendations, alerts, disk exhaustion, memory leak, right-sizing |
| `{"status":"error","error":"...","details":"...","code":"..."}` | `POST /api/v1/predict`, `POST /api/v1/anomalies/analyze`, `POST /api/v1/investigate/rca` |
| `{"success":false,"error":"..."}` | Deployment detection |
| `{"error":"...","success":false}` | KServe proxy |
| `{"status":"error","error":"<status text>","message":"..."}` | Capacity |
| `{"error":"Internal server error","message":"An unexpected error occurred"}` | Panic recovery middleware |

`503` from `GET /api/v1/health` is still the health JSON body, with `status` `unhealthy`.

## Request tracing

`RequestLogger` assigns `X-Request-ID`:

1. The client header is kept when present.
2. Otherwise a UUID is generated.
3. The same value is written on the response.
4. Completion logs include `request_id`, `method`, `path`, `status`, `duration_ms`, `bytes_written`, and `remote_addr`. The message is `Request completed` below 400, `Request completed with client error` for 4xx, and `Request completed with server error` for 5xx.

```bash
curl -H "X-Request-ID: my-trace-id" http://coordination-engine:8080/api/v1/health
```

## Rate limiting

The HTTP API does not rate-limit callers. The Kubernetes client defaults are `KUBERNETES_QPS=50` and `KUBERNETES_BURST=100`.

## Versioning

The versioned surface is `/api/v1`. `GET /health` and `GET /metrics` are unversioned.

## CORS

`ENABLE_CORS` and `CORS_ALLOW_ORIGIN` are loaded into configuration. `main.go` does not install the CORS middleware, so setting `ENABLE_CORS=true` does not add CORS headers. The API does not send CORS headers today.

## Contract gaps

MCP still sends `offset` on the incident list, and it still treats workflow history status `completed` as the list filter. This list uses `remediated` for a completed workflow. `dry_run` on the trigger body is still ignored, so the workflow starts. Anomaly request fields stay camelCase on the MCP client (`timeRange`, `modelName`, `labelSelector`). `GET /api/v1/capacity/trends` is not a route on this server.

## Examples

```bash
curl http://coordination-engine:8080/health

curl http://coordination-engine:8080/api/v1/health

curl -H "X-Request-ID: healthcheck-$(date +%s)" \
  http://coordination-engine:8080/api/v1/health

curl -X POST http://coordination-engine:8080/api/v1/remediation/trigger \
  -H "Content-Type: application/json" \
  -d '{
    "incident_id": "inc-12345",
    "namespace": "production",
    "resource": {"kind": "Deployment", "name": "payment-service"},
    "issue": {"type": "pod_crash_loop", "severity": "high"}
  }'

curl "http://coordination-engine:8080/api/v1/incidents?namespace=production&severity=high"
```

### Kubernetes probes

Use `/health` for liveness and readiness. `/api/v1/health` calls the API server, the ML service URL, and RBAC, so it is the dependency check rather than the probe.

```yaml
livenessProbe:
  httpGet:
    path: /health
    port: http
  initialDelaySeconds: 10
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 3
readinessProbe:
  httpGet:
    path: /health
    port: http
  initialDelaySeconds: 5
  periodSeconds: 5
  timeoutSeconds: 3
  successThreshold: 1
  failureThreshold: 3
```

## Monitoring integration

Scrape the metrics port (container port name `metrics` in the Helm chart). The chart creates a ServiceMonitor when `monitoring.enabled=true`.

## References

- [API Contract](../API-CONTRACT.md)
- [Health model](../pkg/models/health.go)
- [Health handler](../pkg/api/v1/health.go)
- [Request logging](../pkg/middleware/logging.go)
