# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a **Go-based Coordination Engine** that replaces a Python coordination engine while maintaining integration with the existing Python ML/AI service for anomaly detection and predictions. The Go engine provides Kubernetes-native orchestration for multi-layer coordination (infrastructure, platform, application) in OpenShift clusters.

**Key Integration Points:**
- **Upstream**: MCP server (`openshift-cluster-health-mcp`) consumes this engine via REST API
- **Downstream**: Python ML service (`aiops-ml-service`) provides anomaly detection via REST API
- **Cluster**: Integrates with Kubernetes/OpenShift APIs, ArgoCD, and Machine Config Operator (MCO)

## Common Commands

### Building and Testing
```bash
# Build the binary
make build

# Run unit tests
make test

# Run integration tests (requires running cluster)
make test-integration

# Run end-to-end tests (requires OpenShift cluster)
make test-e2e

# Generate coverage report
make coverage

# Run linting
make lint

# Format code
make fmt
```

### Running Locally
```bash
# Set environment variables
export KUBECONFIG=~/.kube/config
export LOG_LEVEL=debug

# KServe integration (recommended - ADR-039)
export ENABLE_KSERVE_INTEGRATION=true
export KSERVE_NAMESPACE=self-healing-platform
export KSERVE_ANOMALY_DETECTOR_SERVICE=anomaly-detector-predictor

# Run the engine
make run

# Or run the binary directly
./bin/coordination-engine
```

### Docker/Container Operations
```bash
# Build container image
make docker-build

# Push to registry (set REGISTRY env var first)
export REGISTRY=quay.io/myorg
make docker-push

# Run container locally
make docker-run
```

### Development Tools
```bash
# Install development tools (golangci-lint, goimports, ginkgo)
make install-tools

# Run all CI checks (lint + test + build)
make ci
```

## Architecture

### Component Responsibilities
- **Go Engine**: Orchestration, remediation planning, multi-layer coordination
- **KServe InferenceServices**: User-deployed ML models for anomaly detection and predictions (ADR-039)
- **MCP Server**: Natural language interface that calls this Go engine

### Project Structure
```
cmd/coordination-engine/main.go     # Entry point, HTTP server setup
internal/                           # Private application code
  ├── detector/                     # Deployment method and layer detection (ADR-041, ADR-040)
  ├── coordination/                 # Multi-layer coordination logic (ADR-040)
  ├── remediation/                  # Remediation planning and execution (ADR-039, ADR-038)
  └── integrations/                 # External service clients (KServe, ArgoCD, legacy ML)
pkg/                                # Public API and models
  ├── api/v1/                       # REST API handlers
  └── models/                       # Data structures
```

### API Contracts

**Upstream API (Consumed by MCP Server):**
- Base URL: `http://coordination-engine:8080/api/v1`
- Key endpoints:
  - `GET /health` - Health check
  - `POST /remediation/trigger` - Trigger remediation workflow
  - `GET /incidents` - List incidents and remediation status
  - `GET /workflows/{id}` - Get workflow execution details
- **IMPORTANT**: Must maintain API compatibility with existing Python coordination engine

**Downstream ML Integration (ADR-039):**
- **KServe (recommended)**: Direct calls to KServe InferenceServices
  - `POST /v1/models/anomaly-detector:predict` - Anomaly detection
  - `POST /v1/models/predictive-analytics:predict` - Predictive analytics
  - See [KServe v1 Protocol](https://kserve.github.io/website/latest/modelserving/data_plane/v1_protocol/)
- **Legacy ML Service (deprecated)**: `http://aiops-ml-service:8080`

### Configuration via Environment Variables
```bash
# Core configuration
KUBECONFIG=/path/to/kubeconfig      # Kubernetes configuration
ARGOCD_API_URL=https://...          # ArgoCD API (optional, detected from cluster)
LOG_LEVEL=info                      # Log level (debug, info, warn, error)
PORT=8080                           # HTTP server port
METRICS_PORT=9090                   # Prometheus metrics port

# KServe integration (ADR-039 - recommended)
ENABLE_KSERVE_INTEGRATION=true
KSERVE_NAMESPACE=self-healing-platform
KSERVE_ANOMALY_DETECTOR_SERVICE=anomaly-detector-predictor
KSERVE_PREDICTIVE_ANALYTICS_SERVICE=predictive-analytics-predictor

# Legacy ML (deprecated - use KServe instead)
# ML_SERVICE_URL=http://...
```

## Development Workflow

### Kubernetes Client Initialization
The engine uses `client-go` to interact with Kubernetes. The client initialization (in `main.go:151-175`) tries in-cluster config first, then falls back to `KUBECONFIG`.

### Integration with Python ML Service
Create HTTP clients in `internal/integrations/ml_service_client.go` to call Python ML endpoints. Always:
- Use context with timeout (30s default)
- Handle network failures gracefully with circuit breakers
- Implement connection pooling (reuse `http.Client`)
- Validate responses and check status codes

### Deployment Detection (ADR-041)
The engine must detect deployment methods (ArgoCD, Helm, Operator, Manual) from metadata:
1. **Priority 1**: ArgoCD tracking annotation (`argocd.argoproj.io/tracking-id`) - confidence 0.95
2. **Priority 2**: Helm release annotation (`meta.helm.sh/release-name`) - confidence 0.90
3. **Priority 3**: Operator managed-by label (`app.kubernetes.io/managed-by`) - confidence 0.80
4. **Default**: Manual deployment - confidence 0.60

Implement in `internal/detector/deployment_detector.go`.

### Multi-Layer Coordination (ADR-040)
Remediation workflows operate across three layers:
1. **Infrastructure**: Node issues, MCO configuration
2. **Platform**: OpenShift operators, core services
3. **Application**: User workloads (ArgoCD-managed or manual)

Each layer has health checkpoints between steps.

### ArgoCD vs Non-ArgoCD Remediation (ADR-038, ADR-039)
- **ArgoCD-managed**: Trigger sync via ArgoCD API, wait for sync completion
- **Non-ArgoCD**: Apply changes directly via Kubernetes API (Helm, Operators, Manual)

### Testing Strategy
- **Unit tests**: Mock Kubernetes client and external services, aim for >80% coverage
- **Integration tests**: Use real cluster (kind/k3s for local dev), mock or real ML service
- **E2E tests**: Deploy to OpenShift cluster with real ArgoCD, MCO, ML service

### Performance and Reliability Patterns
- **Caching**: Cache deployment detection results to reduce API calls
- **Rate Limiting**: Limit API calls to ArgoCD and Kubernetes API
- **Circuit Breakers**: Graceful degradation when Python ML service is unavailable
- **Concurrency**: Use goroutines for parallel health checks across layers

## Related Documentation

### Platform ADRs (in `/home/lab-user/openshift-aiops-platform/docs/adrs/`)
- ADR-033: RBAC Permissions for coordination engine ServiceAccount
- ADR-038: ArgoCD/MCO Integration patterns
- ADR-039: Non-ArgoCD Remediation strategies
- ADR-040: Multi-Layer Coordination design
- ADR-041: Deployment Detection logic
- ADR-042: Go Coordination Engine (overall design)

### Local ADRs (in `docs/adrs/`)
- ADR-009: Python ML Service Integration
- ADR-011: MCP Server Integration

### Related Repositories
- Platform: `/home/lab-user/openshift-aiops-platform`
- MCP Server: `/home/lab-user/openshift-cluster-health-mcp` (Go implementation reference)

### External References
- Go client-go: https://github.com/kubernetes/client-go
- ArgoCD API: https://argo-cd.readthedocs.io/en/stable/developer-guide/api-docs/

## Migration Context

This engine is part of a migration from Python to Go for coordination logic:
- **Removed**: Python coordination engine
- **Kept**: Python ML/AI service (as separate deployment)
- **Added**: This Go coordination engine

See `MIGRATION-GUIDE.md` for migration steps and `API-CONTRACT.md` for detailed API specifications.

## Code Style Notes

- Use `client-go` for all Kubernetes operations
- Use `gorilla/mux` for HTTP routing
- Use `logrus` or `zap` for structured logging (JSON format)
- Use `prometheus/client_golang` for metrics
- Use `testify` for assertions, `ginkgo/gomega` for BDD-style tests
- Go version: 1.21+
- Follow standard Go project layout (cmd, internal, pkg)
