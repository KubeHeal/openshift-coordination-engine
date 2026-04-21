# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.1.0] - 2026-04-21

### Added — AIOps Use Case Gap Closure

- **Anomaly detection enrichment** (ADR-017): `EnrichedSignals` object added to anomaly response with optional `cpu_throttle_rate`, `http_error_rate`, and `http_response_time_p99_ms` signals. All signals gracefully return `null` when Prometheus metrics are unavailable (no Istio required for throttle detection).
- **Disk exhaustion ETA** (ADR-018): New `GET /api/v1/predict/disk-exhaustion` endpoint computes days-until-full per filesystem using 7-day `deriv()` on `node_filesystem_avail_bytes`. Returns urgency classification (critical/warning/info/stable) and projected exhaustion date.
- **Memory leak detection** (ADR-018): New `GET /api/v1/predict/memory-leak` endpoint applies 24-hour slope analysis on `container_memory_working_set_bytes` to classify containers as leaking or normal.
- **Right-sizing recommendations** (ADR-019): New `GET /api/v1/recommendations/rightsizing` endpoint compares P95 CPU/memory usage (30-day window) against current `requests` and `limits`. Returns per-container `recommendedRequest` and `recommendedLimit` with 20%/50% headroom, plus sizing classification (over-provisioned / under-provisioned / right-sized).
- **CPU throttle detection** (ADR-020): Real CFS-based throttle rate from `container_cpu_cfs_throttled_seconds_total / container_cpu_cfs_periods_total` replaces the previous heuristic `cpu_throttling` label. `ThrottlingDetected = true` when rate > 25%.
- **Capacity forecasting output** (use case 5): `TrendingInfo` now includes `ForecastedExhaustionDays` (days to 100% limit) and `RecommendedReplicaIncrease` (suggested replica count increase when exhaustion < 30 days) in all `/api/v1/capacity/*` responses.

### ADRs

- [ADR-017](docs/adrs/017-http-application-signal-integration.md): HTTP Application Signal Integration
- [ADR-018](docs/adrs/018-disk-exhaustion-memory-leak-detection.md): Disk Exhaustion ETA and Memory Leak Slope Detection
- [ADR-019](docs/adrs/019-rightsizing-recommendation-engine.md): VPA-style Right-Sizing Recommendation Engine
- [ADR-020](docs/adrs/020-cpu-throttle-detection-cfs-metrics.md): CPU Throttle Detection via cgroup CFS Metrics

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

[Unreleased]: https://github.com/KubeHeal/openshift-coordination-engine/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/KubeHeal/openshift-coordination-engine/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/KubeHeal/openshift-coordination-engine/releases/tag/v1.0.0
