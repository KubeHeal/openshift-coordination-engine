# Coordination Engine API Documentation

## Overview

The Coordination Engine provides a REST API for orchestrating multi-layer remediation across infrastructure, platform, and application layers in OpenShift clusters.

**Base URL**: `http://coordination-engine:8080/api/v1`

**Content Type**: `application/json`

## Authentication

Currently, the API does not require authentication when accessed from within the cluster. External access should be secured through OpenShift Routes with appropriate authentication.

## Common Headers

### Request Headers

| Header | Description | Required |
|--------|-------------|----------|
| `Content-Type` | Must be `application/json` for requests with body | For POST/PUT/PATCH |
| `X-Request-ID` | Custom request ID for tracing | No |

### Response Headers

| Header | Description |
|--------|-------------|
| `Content-Type` | Always `application/json` |
| `X-Request-ID` | Request ID for tracing (auto-generated if not provided) |

## Health Check Endpoints

The coordination engine provides two health check endpoints to support different use cases:

### GET /health

**Lightweight health check for Kubernetes probes** (Recommended for liveness/readiness probes)

Returns a simple health status following Kubernetes conventions. This endpoint is optimized for frequent health checks with minimal overhead.

**Request**:
```http
GET /health HTTP/1.1
Host: coordination-engine:8080
```

**Response** (200 OK):
```json
{
  "status": "ok",
  "version": "ocp-4.20-abc123"
}
```

**Use Cases**:
- Kubernetes liveness probes
- Kubernetes readiness probes
- Load balancer health checks
- Quick availability checks

---

### GET /api/v1/health

**Detailed health check with dependency monitoring**

Returns comprehensive health status of the coordination engine and all its dependencies.

**Request**:
```http
GET /api/v1/health HTTP/1.1
Host: coordination-engine:8080
```

**Response** (200 OK - Healthy):
```json
{
  "status": "healthy",
  "timestamp": "2025-12-18T19:00:00Z",
  "version": "1.0.0",
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
    "permissions_total": 37,
    "permissions_ok": 37,
    "permissions_failed": 0,
    "critical_ok": true,
    "message": "Critical permissions verified"
  },
  "details": {
    "namespace": "self-healing-platform",
    "service_account": "self-healing-operator"
  }
}
```

**Response** (200 OK - Degraded):
```json
{
  "status": "degraded",
  "timestamp": "2025-12-18T19:00:00Z",
  "version": "1.0.0",
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
      "status": "degraded",
      "message": "Unreachable: connection timeout",
      "latency_ms": 5000,
      "checked_at": "2025-12-18T19:00:00Z"
    }
  },
  "rbac": {
    "status": "ok",
    "permissions_total": 37,
    "permissions_ok": 37,
    "permissions_failed": 0,
    "critical_ok": true,
    "message": "Critical permissions verified"
  }
}
```

**Response** (503 Service Unavailable - Unhealthy):
```json
{
  "status": "unhealthy",
  "timestamp": "2025-12-18T19:00:00Z",
  "version": "1.0.0",
  "uptime_seconds": 3600,
  "dependencies": {
    "kubernetes": {
      "name": "kubernetes",
      "status": "down",
      "message": "Failed to connect: connection refused",
      "latency_ms": 100,
      "checked_at": "2025-12-18T19:00:00Z"
    }
  },
  "rbac": {
    "status": "down",
    "permissions_total": 37,
    "permissions_ok": 34,
    "permissions_failed": 3,
    "critical_ok": false,
    "message": "Critical permissions missing: [/pods:get, /pods:list, /events:create]"
  }
}
```

**Status Fields**:

- `status`: Overall health status
  - `healthy`: All systems operational
  - `degraded`: Some non-critical issues
  - `unhealthy`: Critical issues present

- `dependencies[].status`: Dependency status
  - `ok`: Dependency is healthy
  - `degraded`: Dependency has non-critical issues
  - `down`: Dependency is unavailable

**Use Cases**:

1. **Kubernetes Liveness Probe**: Use this endpoint to verify the application is running
2. **Readiness Probe**: Check `status == "healthy"` before routing traffic
3. **Monitoring**: Track `dependencies` and `rbac` status for alerts
4. **Debugging**: Check `latency_ms` to identify slow dependencies

## Metrics Endpoint

### GET /metrics

Prometheus metrics endpoint (served on port 9090 by default).

**Request**:
```http
GET /metrics HTTP/1.1
Host: coordination-engine:9090
```

**Response**:
```
# HELP go_goroutines Number of goroutines that currently exist.
# TYPE go_goroutines gauge
go_goroutines 15
...
```

## Future Endpoints (Planned)

### POST /api/v1/remediation/trigger

Trigger a remediation workflow for a detected issue.

**Status**: Not yet implemented

### GET /api/v1/incidents

List detected incidents and their remediation status.

**Status**: Not yet implemented

### GET /api/v1/workflows/{id}

Get details of a specific remediation workflow execution.

**Status**: Not yet implemented

## Error Responses

All error responses follow a consistent format:

```json
{
  "error": "error_code",
  "message": "Human-readable error message",
  "details": {
    "field": "Additional context"
  }
}
```

**Common Error Codes**:

| Status Code | Error Code | Description |
|-------------|------------|-------------|
| 400 | `bad_request` | Invalid request format |
| 404 | `not_found` | Resource not found |
| 500 | `internal_error` | Internal server error |
| 503 | `service_unavailable` | Service temporarily unavailable |

## Request Tracing

All requests are assigned a unique request ID for tracing:

1. **Client-provided**: Send `X-Request-ID` header
2. **Auto-generated**: UUID generated if header not provided
3. **Response**: Request ID returned in `X-Request-ID` response header
4. **Logs**: All log entries include the request ID

**Example**:
```bash
curl -H "X-Request-ID: my-trace-id" http://coordination-engine:8080/api/v1/health
```

**Log Entry**:
```json
{
  "level": "info",
  "msg": "Request completed",
  "request_id": "my-trace-id",
  "method": "GET",
  "path": "/api/v1/health",
  "status": 200,
  "duration_ms": 15,
  "timestamp": "2025-12-18T19:00:00Z"
}
```

## Rate Limiting

Currently, no rate limiting is implemented. The Kubernetes client is configured with:
- **QPS**: 50 queries per second
- **Burst**: 100 concurrent requests

## Versioning

The API is versioned through the URL path (`/api/v1`). Future versions will be introduced as `/api/v2`, etc., with backwards compatibility maintained for at least one previous version.

## CORS

CORS is not enabled by default. To enable CORS for frontend applications:

1. Set environment variable `ENABLE_CORS=true`
2. Configure allowed origins if needed (default: `*`)

## Examples

### Health Check with curl

```bash
# Lightweight health check (recommended)
curl http://coordination-engine:8080/health

# Detailed health check with dependency status
curl http://coordination-engine:8080/api/v1/health

# Pretty-printed with jq
curl -s http://coordination-engine:8080/api/v1/health | jq .

# With custom request ID
curl -H "X-Request-ID: healthcheck-$(date +%s)" \
  http://coordination-engine:8080/api/v1/health
```

### Health Check with kubectl

```bash
# Port-forward to access from local machine
kubectl port-forward svc/coordination-engine 8080:8080 -n self-healing-platform

# Lightweight health check
curl http://localhost:8080/health

# Detailed health check
curl http://localhost:8080/api/v1/health
```

### Kubernetes Probes

**Recommended: Use `/health` endpoint for probes**

The `/health` endpoint follows Kubernetes conventions and provides lightweight health checks optimized for frequent probe requests.

**Deployment manifest example**:
```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: coordination-engine
spec:
  template:
    spec:
      containers:
      - name: coordination-engine
        image: coordination-engine:latest
        ports:
        - containerPort: 8080
          name: http
        - containerPort: 9090
          name: metrics
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

**Alternative: Use `/api/v1/health` for detailed monitoring**

If you need dependency status visibility in your probes, you can use the detailed endpoint:

```yaml
livenessProbe:
  httpGet:
    path: /api/v1/health
    port: http
  initialDelaySeconds: 30
  periodSeconds: 10
  timeoutSeconds: 5
  failureThreshold: 3
```

> **Note**: The detailed endpoint performs additional checks (Kubernetes API, ML service, RBAC) which may add latency. Use `/health` for most cases.

## Monitoring Integration

### Prometheus Scrape Configuration

```yaml
scrape_configs:
  - job_name: 'coordination-engine'
    kubernetes_sd_configs:
      - role: pod
        namespaces:
          names:
            - self-healing-platform
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name]
        action: keep
        regex: coordination-engine
      - source_labels: [__meta_kubernetes_pod_container_port_name]
        action: keep
        regex: metrics
```

### ServiceMonitor (Operator)

ServiceMonitor is automatically created by the Helm chart when `monitoring.enabled=true`.

## References

- [API Contract Documentation](../API-CONTRACT.md)
- [Health Models](../pkg/models/health.go)
- [Health Handler](../pkg/api/v1/health.go)
- [Middleware Documentation](../pkg/middleware/)
