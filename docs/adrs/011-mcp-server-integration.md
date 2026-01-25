# ADR-011: MCP Server Integration

## Status
ACCEPTED - 2025-12-18

## Context

The Go Coordination Engine will be called by the **OpenShift Cluster Health MCP Server** to provide natural language access to remediation and coordination workflows.

The MCP server already integrates with the current Python coordination engine via REST API. This Go implementation must maintain API compatibility to enable a seamless migration without requiring changes to the MCP server.

## Decision

- Maintain **100% API compatibility** with the existing coordination engine REST API.
- Expose the same REST endpoints under `/api/v1`.
- Implement HTTP handlers using `gorilla/mux` for routing.
- Ensure MCP server does **not** need code changes when switching to Go engine.
- Use structured JSON logging for request/response tracking.

## API Contract

### Base URL
- Development: `http://localhost:8080/api/v1`
- Production: `http://coordination-engine:8080/api/v1`

### Endpoints (Consumed by MCP Server)

#### 1. Health Check
```http
GET /api/v1/health

Response 200 OK:
{
  "status": "healthy",
  "version": "1.0.0",
  "dependencies": {
    "kubernetes": "connected",
    "ml_service": "healthy",
    "argocd": "connected"
  },
  "timestamp": "2025-12-18T10:00:00Z"
}
```

#### 2. Trigger Remediation
```http
POST /api/v1/remediation/trigger
Content-Type: application/json

{
  "namespace": "production",
  "resource_type": "pod",
  "resource_name": "payment-service-abc123",
  "issue_type": "CrashLoopBackOff",
  "severity": "high",
  "metadata": {
    "user": "mcp-server",
    "trigger_source": "natural_language_query"
  }
}

Response 202 Accepted:
{
  "status": "accepted",
  "workflow_id": "wf-20251218-001",
  "message": "Remediation workflow initiated",
  "estimated_duration": "5m"
}
```

#### 3. List Incidents
```http
GET /api/v1/incidents?namespace=production&status=active&severity=high&limit=50

Query Parameters:
- status: Filter by status ("active", "resolved", "cancelled", "all")
- severity: Filter by severity ("low", "medium", "high", "critical", "all")
- namespace: Filter by target namespace
- limit: Maximum incidents to return (default: 50, max: 500)

Response 200 OK:
{
  "incidents": [
    {
      "id": "inc-001",
      "namespace": "production",
      "resource": "payment-service",
      "issue_type": "HighMemoryUsage",
      "severity": "medium",
      "status": "investigating",
      "created_at": "2025-12-18T09:45:00Z",
      "workflow_id": "wf-20251218-001"
    }
  ],
  "total": 1,
  "filters": {
    "status": "active",
    "severity": "high",
    "namespace": "production"
  },
  "page": 1,
  "limit": 50
}
```

#### 4. Get Workflow Status
```http
GET /api/v1/workflows/{id}

Response 200 OK:
{
  "workflow_id": "wf-20251218-001",
  "status": "in_progress",
  "current_step": "infrastructure_remediation",
  "steps": [
    {
      "layer": "infrastructure",
      "status": "completed",
      "duration": "2m30s"
    },
    {
      "layer": "application",
      "status": "in_progress",
      "started_at": "2025-12-18T09:50:00Z"
    }
  ],
  "created_at": "2025-12-18T09:47:30Z",
  "updated_at": "2025-12-18T09:50:15Z"
}
```

#### 5. Create Incident (Manual Tracking)
```http
POST /api/v1/incidents
Content-Type: application/json

{
  "title": "High CPU usage in production",
  "description": "payment-service CPU sustained >80% for 30 minutes. Possible memory leak.",
  "severity": "high",
  "target": "production",
  "affected_resources": [
    "deployment/payment-service",
    "pod/payment-service-abc123"
  ],
  "labels": {
    "team": "platform",
    "escalation": "true",
    "source": "mcp-server"
  }
}

Response 201 Created:
{
  "status": "success",
  "incident_id": "inc-a1b2c3d4",
  "created_at": "2026-01-25T10:00:00Z",
  "incident": {
    "id": "inc-a1b2c3d4",
    "title": "High CPU usage in production",
    "description": "payment-service CPU sustained >80% for 30 minutes. Possible memory leak.",
    "severity": "high",
    "target": "production",
    "status": "active",
    "affected_resources": [
      "deployment/payment-service",
      "pod/payment-service-abc123"
    ],
    "labels": {
      "team": "platform",
      "escalation": "true",
      "source": "mcp-server"
    },
    "created_at": "2026-01-25T10:00:00Z",
    "updated_at": "2026-01-25T10:00:00Z"
  },
  "message": "Incident created successfully"
}

Error Responses:
- 400 Bad Request: Invalid request body or missing required fields
- 500 Internal Server Error: Failed to create incident
```

**Use Case**: MCP server creates incident from natural language query:
- User: "High CPU in production for 30 minutes"
- MCP: Extracts structured data → POST /api/v1/incidents
- Engine: Creates incident, returns incident_id
- MCP: Returns to user: "Incident inc-a1b2c3d4 created and tracking"

See: [ADR-014](014-prometheus-thanos-observability-incident-management.md) for incident storage implementation

## Go Implementation

### HTTP Server Setup

Package: `cmd/coordination-engine/main.go`

```go
package main

import (
    "context"
    "net/http"
    "os"
    "os/signal"
    "syscall"
    "time"

    "github.com/gorilla/mux"
    "github.com/sirupsen/logrus"

    "openshift-coordination-engine/pkg/api/v1"
)

func main() {
    log := logrus.New()
    log.SetFormatter(&logrus.JSONFormatter{})

    router := mux.NewRouter()

    // API v1 routes
    v1Router := router.PathPrefix("/api/v1").Subrouter()

    // Health endpoint
    v1Router.HandleFunc("/health", api.HealthHandler).Methods("GET")

    // Remediation endpoints
    v1Router.HandleFunc("/remediation/trigger", api.TriggerRemediationHandler).Methods("POST")

    // Incident endpoints
    v1Router.HandleFunc("/incidents", api.ListIncidentsHandler).Methods("GET")

    // Workflow endpoints
    v1Router.HandleFunc("/workflows/{id}", api.GetWorkflowHandler).Methods("GET")

    // Middleware
    router.Use(loggingMiddleware)
    router.Use(corsMiddleware)

    // Server configuration
    port := os.Getenv("PORT")
    if port == "" {
        port = "8080"
    }

    srv := &http.Server{
        Addr:         ":" + port,
        Handler:      router,
        ReadTimeout:  15 * time.Second,
        WriteTimeout: 15 * time.Second,
        IdleTimeout:  60 * time.Second,
    }

    // Graceful shutdown
    go func() {
        log.Infof("Starting server on port %s", port)
        if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Server failed: %v", err)
        }
    }()

    // Wait for interrupt signal
    quit := make(chan os.Signal, 1)
    signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
    <-quit

    log.Info("Shutting down server...")
    ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()

    if err := srv.Shutdown(ctx); err != nil {
        log.Fatalf("Server forced shutdown: %v", err)
    }

    log.Info("Server exited")
}
```

### API Handlers

Package: `pkg/api/v1/remediation.go`

```go
package v1

import (
    "encoding/json"
    "net/http"

    "github.com/google/uuid"
)

type TriggerRemediationRequest struct {
    Namespace    string            `json:"namespace"`
    ResourceType string            `json:"resource_type"`
    ResourceName string            `json:"resource_name"`
    IssueType    string            `json:"issue_type"`
    Severity     string            `json:"severity"`
    Metadata     map[string]string `json:"metadata"`
}

type TriggerRemediationResponse struct {
    Status            string `json:"status"`
    WorkflowID        string `json:"workflow_id"`
    Message           string `json:"message"`
    EstimatedDuration string `json:"estimated_duration"`
}

func TriggerRemediationHandler(w http.ResponseWriter, r *http.Request) {
    var req TriggerRemediationRequest

    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        http.Error(w, "Invalid request body", http.StatusBadRequest)
        return
    }

    // Validate request
    if req.Namespace == "" || req.ResourceName == "" {
        http.Error(w, "Missing required fields", http.StatusBadRequest)
        return
    }

    // Generate workflow ID
    workflowID := "wf-" + uuid.New().String()[:8]

    // Initiate remediation workflow (implementation in ADR-003, ADR-005)
    // Workflow orchestrates multi-layer coordination and remediation

    resp := TriggerRemediationResponse{
        Status:            "accepted",
        WorkflowID:        workflowID,
        Message:           "Remediation workflow initiated",
        EstimatedDuration: "5m",
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusAccepted)
    json.NewEncoder(w).Encode(resp)
}
```

### Middleware

Package: `pkg/api/v1/middleware.go`

```go
package v1

import (
    "net/http"
    "time"

    "github.com/sirupsen/logrus"
)

func loggingMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        start := time.Now()

        logrus.WithFields(logrus.Fields{
            "method": r.Method,
            "path":   r.URL.Path,
            "remote": r.RemoteAddr,
        }).Info("Request received")

        next.ServeHTTP(w, r)

        logrus.WithFields(logrus.Fields{
            "method":   r.Method,
            "path":     r.URL.Path,
            "duration": time.Since(start).String(),
        }).Info("Request completed")
    })
}

func corsMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

        if r.Method == "OPTIONS" {
            w.WriteHeader(http.StatusOK)
            return
        }

        next.ServeHTTP(w, r)
    })
}
```

## Migration Strategy

### Phase 1: Parallel Deployment
- Deploy Go engine alongside Python engine
- Route a small percentage of MCP traffic to Go engine
- Monitor for API compatibility issues

### Phase 2: Gradual Rollout
- Increase traffic to Go engine (25% → 50% → 75%)
- Validate response compatibility
- Monitor performance and error rates

### Phase 3: Complete Cutover
- Route 100% of traffic to Go engine
- Keep Python engine as fallback for 2 weeks
- Decommission Python engine after validation period

## Consequences

### Positive
- ✅ **Zero MCP Changes**: MCP server works without modifications
- ✅ **Independent Deployment**: Go engine can be rolled out incrementally
- ✅ **Performance**: Lower latency, lower memory usage
- ✅ **Structured Logging**: JSON logs for better observability
- ✅ **Graceful Shutdown**: Proper signal handling for zero-downtime deployments

### Negative
- ⚠️ **API Contract Rigidity**: Must maintain exact response format
- ⚠️ **Testing Overhead**: Must validate all MCP integration scenarios
- ⚠️ **Version Coordination**: API version must match MCP expectations

### Mitigation Strategies
- **Contract Testing**: Automated tests comparing Go and Python responses
- **Integration Tests**: Full MCP → Go engine integration test suite
- **Monitoring**: Track API compatibility metrics in production

## Testing Strategy

1. **Contract Tests**: Validate response format matches Python implementation
2. **Integration Tests**: Test full MCP → Coordination Engine flow
3. **Load Tests**: Ensure Go implementation handles production traffic
4. **Compatibility Tests**: Side-by-side comparison of Go vs Python responses

## Configuration

Environment variables:
```bash
PORT=8080                    # HTTP server port
LOG_LEVEL=info               # Logging level (debug, info, warn, error)
METRICS_PORT=9090            # Prometheus metrics port
ENABLE_CORS=true             # Enable CORS headers
```

## References

- MCP Server Repository: `/home/lab-user/openshift-cluster-health-mcp`
- Platform ADR-042: Go-Based Coordination Engine
- API Contract Specification: `API-CONTRACT.md`
- MCP ADR-006: Integration Architecture
- Gorilla Mux: https://github.com/gorilla/mux

## Related ADRs

- ADR-001: Go Project Architecture (HTTP routing and middleware patterns)
- ADR-009: Python ML Service Integration (downstream dependency)
- ADR-014: Prometheus/Thanos Observability and Incident Management (incident creation API, enhanced filtering)
- Platform ADR-042: Go-Based Coordination Engine (overall architecture)


