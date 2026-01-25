# Go Coordination Engine Architecture

## High-Level Architecture

```mermaid
graph TB
    MCP[MCP Server]
    GoEngine[Go Coordination Engine]
    PythonML[Python ML Service]
    K8s[Kubernetes API]
    ArgoCD[ArgoCD]
    MCO[MCO]

    MCP -->|REST| GoEngine
    GoEngine -->|REST| PythonML
    GoEngine -->|client-go| K8s
    GoEngine -->|HTTP| ArgoCD
    GoEngine -->|Monitor| MCO
```

## Responsibilities

- **Go Engine**: orchestration, remediation, multi-layer coordination
- **Python ML**: anomaly detection, predictions, pattern recognition
- **MCP Server**: natural language interface on top of Go engine

## Repositories

- Platform: `/home/lab-user/openshift-aiops-platform`
- MCP Server: `/home/lab-user/openshift-cluster-health-mcp`
- Go Engine Stub (here): `openshift-coordination-engine/`

See `API-CONTRACT.md` for integration details.


