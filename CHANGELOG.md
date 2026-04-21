# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.0.0] - 2026-04-21

### Added
- Go-based coordination engine for multi-layer OpenShift remediation ([ADR-001](docs/adrs/001-go-project-architecture.md))
- Deployment detection for ArgoCD, Helm, Operator, and manual deployment methods ([ADR-002](docs/adrs/002-deployment-detection-implementation.md))
- Multi-layer coordination across infrastructure, platform, and application tiers ([ADR-003](docs/adrs/003-multi-layer-coordination-implementation.md))
- ArgoCD and MCO integration with clear boundary definitions ([ADR-004](docs/adrs/004-argocd-mco-integration.md))
- Remediation strategies: ArgoCD sync, Helm rollback, operator-managed, and manual paths ([ADR-005](docs/adrs/005-remediation-strategies-implementation.md))
- RBAC and Kubernetes client configuration for cluster-scoped operations ([ADR-006](docs/adrs/006-rbac-kubernetes-client-configuration.md))
- KServe InferenceService integration for ML-assisted anomaly detection and prediction ([ADR-015](docs/adrs/015-kserve-inference-service-integration.md))
- MCP server REST API integration contract at `/api/v1` ([ADR-011](docs/adrs/011-mcp-server-integration.md))
- ML-enhanced layer detection via KServe probability scoring ([ADR-012](docs/adrs/012-ml-enhanced-layer-detection.md))
- Prometheus and Thanos observability integration with incident management ([ADR-014](docs/adrs/014-prometheus-thanos-observability-incident-management.md))
- Predictive analytics feature engineering for CPU, memory, disk, and network forecasting ([ADR-016](docs/adrs/016-predictive-analytics-feature-engineering.md))
- GitHub branch protection and collaboration workflow ([ADR-013](docs/adrs/013-github-branch-protection-collaboration.md))
- REST API endpoints: `/api/v1/health`, `/api/v1/remediation`, `/api/v1/incidents`, `/api/v1/capacity`, `/api/v1/anomalies/analyze`, `/api/v1/predict`, `/api/v1/recommendations`
- Helm chart supporting OpenShift 4.18, 4.19, and 4.20 via `values-ocp-4.x.yaml` overlays
- Rolling 3-version support strategy documented in `docs/VERSION-STRATEGY.md`
- Release branches `release-4.18`, `release-4.19`, `release-4.20` with `main` auto-sync

### Fixed
- CI pipeline now uses Go 1.24 to match `go.mod` toolchain requirement (previously pinned to 1.21)

[Unreleased]: https://github.com/KubeHeal/openshift-coordination-engine/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/KubeHeal/openshift-coordination-engine/releases/tag/v1.0.0
