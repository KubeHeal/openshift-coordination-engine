# Architectural Decision Records (ADRs)

This directory contains Architectural Decision Records (ADRs) for the Go-based Coordination Engine.

## Overview

The Coordination Engine is a Go-based service that orchestrates multi-layer remediation for OpenShift/Kubernetes environments. It integrates with ArgoCD for GitOps-managed applications, monitors the Machine Config Operator (MCO) for infrastructure-layer changes, and consumes a Python ML/AI service for anomaly detection.

## ADR Index

### Implementation ADRs (Local)

These ADRs define the **Go implementation** of the coordination engine:

| ADR | Title | Status | Description |
|-----|-------|--------|-------------|
| [001](001-go-project-architecture.md) | Go Project Architecture and Standards | ACCEPTED | Go version, project layout, coding conventions, testing standards |
| [002](002-deployment-detection-implementation.md) | Deployment Detection Implementation | ACCEPTED | Go implementation of deployment method detection (ArgoCD, Helm, Operator, Manual) |
| [003](003-multi-layer-coordination-implementation.md) | Multi-Layer Coordination Implementation | ACCEPTED | Layer detection, multi-layer planner, orchestrator, health checker |
| [004](004-argocd-mco-integration.md) | ArgoCD/MCO Integration | ACCEPTED | ArgoCD client, MCO client, integration boundaries |
| [005](005-remediation-strategies-implementation.md) | Remediation Strategies Implementation | ACCEPTED | Strategy selector, remediators (ArgoCD, Helm, Operator, Manual) |
| [006](006-rbac-kubernetes-client-configuration.md) | RBAC and Kubernetes Client Configuration | ACCEPTED | Kubernetes client initialization, RBAC permissions, ServiceAccount setup |
| 007-010 | *(Reserved - see note below)* | - | Reserved for future use |
| [009](009-python-ml-integration.md) | Python ML Service Integration | ACCEPTED | HTTP client for Python ML/AI service (anomaly detection, predictions) |
| [011](011-mcp-server-integration.md) | MCP Server Integration | ACCEPTED | REST API contract for MCP server integration |
| [012](012-ml-enhanced-layer-detection.md) | ML-Enhanced Layer Detection | ACCEPTED | ML-enhanced layer detection with confidence scores (Phase 6) |
| [013](013-github-branch-protection-collaboration.md) | GitHub Branch Protection and Collaboration Workflow | ACCEPTED | Branch protection rules, code ownership, and contribution guidelines |
| [014](014-prometheus-thanos-observability-incident-management.md) | Prometheus/Thanos Observability Integration and Incident Management | ACCEPTED | Prometheus/Thanos metrics integration, incident storage with persistence, and API enhancements for manual incident creation |

**Note on numbering**: ADR-007, ADR-008, and ADR-010 are reserved numbers. These were initially planned for additional decisions but were either integrated into existing ADRs or deemed unnecessary. The numbers are kept reserved to maintain sequential reference integrity.

### Platform ADRs (Reference)

These ADRs from `/home/lab-user/openshift-aiops-platform/docs/adrs/` define the **overall strategy** and are referenced by local ADRs:

| ADR | Title | Referenced By |
|-----|-------|---------------|
| [Platform ADR-033](../../openshift-aiops-platform/docs/adrs/033-coordination-engine-rbac-permissions.md) | Coordination Engine RBAC Permissions | ADR-006 |
| [Platform ADR-038](../../openshift-aiops-platform/docs/adrs/038-argocd-mco-integration-boundaries.md) | ArgoCD/MCO Integration Boundaries | ADR-004 |
| [Platform ADR-039](../../openshift-aiops-platform/docs/adrs/039-non-argocd-application-remediation.md) | Non-ArgoCD Application Remediation | ADR-005 |
| [Platform ADR-040](../../openshift-aiops-platform/docs/adrs/040-multi-layer-coordination-strategy.md) | Multi-Layer Coordination Strategy | ADR-003 |
| [Platform ADR-041](../../openshift-aiops-platform/docs/adrs/041-deployment-method-detection-strategy.md) | Deployment Method Detection Strategy | ADR-002 |
| [Platform ADR-042](../../openshift-aiops-platform/docs/adrs/042-go-based-coordination-engine.md) | Go-Based Coordination Engine | All ADRs |

## ADR Relationships

```
Platform ADR-042 (Go Coordination Engine)
    │
    ├──> ADR-001 (Go Project Architecture)
    │       └──> Foundation for all Go code
    │
    ├──> Platform ADR-041 ──> ADR-002 (Deployment Detection)
    │                           └──> Used by ADR-005 (Remediation Strategies)
    │
    ├──> Platform ADR-040 ──> ADR-003 (Multi-Layer Coordination)
    │                           ├──> Uses ADR-002 (Layer Detection)
    │                           ├──> Uses ADR-004 (Infrastructure Layer/MCO)
    │                           ├──> Uses ADR-005 (Application Layer Remediation)
    │                           └──> Enhanced by ADR-012 (ML Layer Detection)
    │
    ├──> Platform ADR-038 ──> ADR-004 (ArgoCD/MCO Integration)
    │                           └──> Used by ADR-005 (ArgoCD Remediator)
    │
    ├──> Platform ADR-039 ──> ADR-005 (Remediation Strategies)
    │                           ├──> Uses ADR-002 (Detection)
    │                           └──> Uses ADR-004 (ArgoCD Integration)
    │
    ├──> Platform ADR-033 ──> ADR-006 (RBAC Configuration)
    │                           └──> Required by all components
    │
    ├──> ADR-009 (Python ML Integration)
    │       ├──> Consumed by coordination engine
    │       └──> Enhanced in ADR-012 (ML Layer Detection)
    │
    ├──> ADR-011 (MCP Server Integration)
    │       └──> REST API consumed by MCP server
    │
    ├──> ADR-012 (ML-Enhanced Layer Detection)
    │       ├──> Enhances ADR-003 (Layer Detector)
    │       └──> Uses ADR-009 (Python ML Client)
    │
    └──> ADR-014 (Prometheus/Thanos Observability & Incidents)
            ├──> Enhances ADR-009 (ML metrics source)
            ├──> Extends ADR-011 (Incident creation API)
            ├──> Improves ADR-012 (ML confidence with real metrics)
            └──> Extends ADR-001 (Storage package)
```

## Reading Order

### For New Developers

If you're new to the coordination engine, read ADRs in this order:

1. **[Platform ADR-042](../../openshift-aiops-platform/docs/adrs/042-go-based-coordination-engine.md)** - Overall architecture and context
2. **[ADR-001](001-go-project-architecture.md)** - Go standards and project layout
3. **[ADR-013](013-github-branch-protection-collaboration.md)** - Branch protection and collaboration workflow
4. **[ADR-011](011-mcp-server-integration.md)** - REST API contract (how MCP server calls us)
5. **[ADR-002](002-deployment-detection-implementation.md)** - Deployment method detection
6. **[ADR-003](003-multi-layer-coordination-implementation.md)** - Multi-layer coordination logic
7. **[ADR-005](005-remediation-strategies-implementation.md)** - Remediation strategies
8. **[ADR-004](004-argocd-mco-integration.md)** - ArgoCD and MCO integration
9. **[ADR-006](006-rbac-kubernetes-client-configuration.md)** - Kubernetes client and RBAC
10. **[ADR-009](009-python-ml-integration.md)** - Python ML service integration
11. **[ADR-012](012-ml-enhanced-layer-detection.md)** *(Optional)* - ML-enhanced layer detection
12. **[ADR-014](014-prometheus-thanos-observability-incident-management.md)** - Prometheus/Thanos observability and incident management (builds on ADR-003 and ADR-009)

### For Platform Understanding

If you want to understand the overall platform strategy first:

1. **[Platform ADR-042](../../openshift-aiops-platform/docs/adrs/042-go-based-coordination-engine.md)** - Go coordination engine decision
2. **[Platform ADR-040](../../openshift-aiops-platform/docs/adrs/040-multi-layer-coordination-strategy.md)** - Multi-layer strategy
3. **[Platform ADR-041](../../openshift-aiops-platform/docs/adrs/041-deployment-method-detection-strategy.md)** - Detection strategy
4. **[Platform ADR-038](../../openshift-aiops-platform/docs/adrs/038-argocd-mco-integration-boundaries.md)** - Integration boundaries
5. **[Platform ADR-039](../../openshift-aiops-platform/docs/adrs/039-non-argocd-application-remediation.md)** - Remediation strategies
6. **[Platform ADR-033](../../openshift-aiops-platform/docs/adrs/033-coordination-engine-rbac-permissions.md)** - RBAC requirements

Then read the local ADRs (001-006, 009, 011) for implementation details.

## Key Concepts

### Deployment Methods

The engine detects four deployment methods:
- **ArgoCD**: GitOps-managed via ArgoCD (confidence: 0.95)
- **Helm**: Helm-managed releases (confidence: 0.90)
- **Operator**: Operator-managed custom resources (confidence: 0.80)
- **Manual**: Direct `kubectl apply` or manual (confidence: 0.60)

See: [ADR-002](002-deployment-detection-implementation.md)

### Multi-Layer Coordination

The engine orchestrates remediation across three layers:
1. **Infrastructure**: Nodes, MCO, OS configuration
2. **Platform**: OpenShift operators, SDN, storage
3. **Application**: User workloads (pods, deployments)

Remediation always proceeds: Infrastructure → Platform → Application

See: [ADR-003](003-multi-layer-coordination-implementation.md)

### Integration Boundaries

The engine respects clear boundaries:
- **ArgoCD**: Trigger sync via ArgoCD API, don't bypass GitOps
- **MCO**: Monitor status read-only, don't create MachineConfigs
- **Helm**: Use `helm upgrade/rollback`, don't modify resources directly
- **Operators**: Update CR to trigger reconciliation, don't modify managed resources

See: [ADR-004](004-argocd-mco-integration.md), [ADR-005](005-remediation-strategies-implementation.md)

### Remediation Strategies

The engine uses a strategy pattern to route remediation:
- `StrategySelector` chooses the appropriate `Remediator` based on deployment method
- Each `Remediator` implements deployment-specific remediation logic
- Fallback to `ManualRemediator` for unknown deployment methods

See: [ADR-005](005-remediation-strategies-implementation.md)

### Observability and Incident Management

The engine integrates with Prometheus/Thanos for real-time cluster metrics and persistent incident tracking:
- **Thanos Querier**: `https://thanos-querier.openshift-monitoring.svc:9091`
- **Long-term storage**: Months of historical metrics for ML training (vs. 2-7 days in Prometheus)
- **45-feature vectors**: Real cluster data (CPU, memory, restarts, trends) for anomaly detection
- **ML accuracy improvement**: 60-70% → 85-95% with real metrics (+20-30%)
- **Incident tracking**: Persistent JSON storage with CRUD operations for compliance and multi-day correlation
- **API enhancements**: Manual incident creation via `POST /api/v1/incidents`, enhanced filtering with `status=all`

See: [ADR-014](014-prometheus-thanos-observability-incident-management.md)

## Package Organization

Based on [ADR-001](001-go-project-architecture.md):

```
openshift-coordination-engine/
├── cmd/coordination-engine/        # Application entry point
├── internal/                       # Private implementation
│   ├── detector/                   # Deployment and layer detection (ADR-002, ADR-003)
│   ├── coordination/               # Planner, orchestrator, health checker (ADR-003)
│   ├── remediation/                # Strategy selector and remediators (ADR-005)
│   └── integrations/               # ArgoCD, MCO, ML service clients (ADR-004, ADR-009)
├── pkg/                            # Public API
│   ├── api/v1/                     # HTTP handlers (ADR-011)
│   └── models/                     # Data models
└── charts/coordination-engine/     # Helm chart (ADR-006)
```

## API Contracts

### Upstream API (MCP Server Integration)

The MCP server calls the coordination engine via REST API:

- `GET /api/v1/health` - Health check
- `POST /api/v1/remediation/trigger` - Trigger remediation workflow
- `GET /api/v1/incidents?status=all&severity=high` - List incidents with enhanced filtering (ADR-014)
- `POST /api/v1/incidents` - Create incident for manual tracking (ADR-014)
- `GET /api/v1/workflows/{id}` - Get workflow status

See: [ADR-011](011-mcp-server-integration.md), [ADR-014](014-prometheus-thanos-observability-incident-management.md), [API-CONTRACT.md](../../API-CONTRACT.md)

### Downstream API (Python ML Service)

The coordination engine calls the Python ML service:

- `POST /api/v1/anomaly/detect` - Detect anomalies
- `POST /api/v1/prediction/predict` - Predict future issues
- `POST /api/v1/pattern/analyze` - Analyze patterns

See: [ADR-009](009-python-ml-integration.md)

## Development Workflow

1. **Setup**: Follow [DEVELOPMENT.md](../../DEVELOPMENT.md) for environment setup
2. **Standards**: Follow Go conventions from [ADR-001](001-go-project-architecture.md)
3. **Testing**: Write unit tests (>80% coverage), integration tests, E2E tests
4. **RBAC**: Ensure proper permissions from [ADR-006](006-rbac-kubernetes-client-configuration.md)
5. **Deployment**: Use Helm chart, verify health endpoint

## References

- [Platform ADRs](../../openshift-aiops-platform/docs/adrs/) - Overall strategy
- [CLAUDE.md](../../CLAUDE.md) - Claude Code instructions
- [API-CONTRACT.md](../../API-CONTRACT.md) - API specification
- [DEVELOPMENT.md](../../DEVELOPMENT.md) - Development guide
- [Makefile](../../Makefile) - Build commands

## Contributing

When creating new ADRs:
1. Follow the [ADR template](https://github.com/joelparkerhenderson/architecture-decision-record)
2. Number ADRs sequentially (next: ADR-014; skip reserved numbers 007, 008, 010)
3. Reference platform ADRs where applicable
4. Update this README with the new ADR in index table, relationship diagram, and reading order
5. Add cross-references in "Related ADRs" section
6. Status: PROPOSED → ACCEPTED → DEPRECATED/SUPERSEDED

## Status Legend

- ✅ **ACCEPTED**: ADR is approved and implemented
- 🔄 **PROPOSED**: ADR is under review
- ⚠️ **DEPRECATED**: ADR is no longer valid
- 🔀 **SUPERSEDED**: Replaced by a newer ADR

---

*Last Updated: 2026-01-25*
