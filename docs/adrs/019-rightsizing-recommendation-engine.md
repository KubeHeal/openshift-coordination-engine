# ADR-019: VPA-style Right-Sizing Recommendation Engine

**Status**: Accepted  
**Date**: 2026-04-21  
**Deciders**: KubeHeal Engineering

## Context

Application teams routinely over-provision CPU and memory requests/limits during initial
deployment and never revisit them. This leads to:

- **Wasted cluster capacity** from over-provisioned pods.
- **OOMKilled / throttling incidents** from under-provisioned pods.
- Kubernetes scheduler making suboptimal placement decisions.

The Vertical Pod Autoscaler (VPA) addresses this but requires CRD installation and
admission webhook integration, which not all clusters have. Teams need a lightweight
advisory API that surfaces right-sizing guidance without requiring VPA.

## Decision

Add `GET /api/v1/recommendations/rightsizing` to the coordination engine.

### Algorithm

1. Query `quantile_over_time(0.95, ...)` over a configurable window (default: 30 days)
   for `container_cpu_usage_seconds_total` (rate) and `container_memory_working_set_bytes`.
2. Query current `kube_pod_container_resource_requests` and `kube_pod_container_resource_limits`.
3. Compute recommendations:
   - `recommendedRequest` = P95 × 1.20 (20% headroom)
   - `recommendedLimit`   = P95 × 1.50 (50% headroom)
4. Classify sizing:
   - **over-provisioned**: current request > 2× P95 usage
   - **under-provisioned**: current request < 0.8× P95 usage
   - **right-sized**: within the 0.8–2.0× band
5. Optionally surface `ThrottleRatePct` from `container_cpu_cfs_throttled_seconds_total`
   to help teams distinguish CPU limit issues from CPU request over-provisioning.

### Prerequisites

- Prometheus retention ≥ 30 days (configurable via `?window=7d|14d|30d`).
- `kube-state-metrics` must expose `kube_pod_container_resource_requests/limits`.

## Consequences

- Recommendations are **advisory only** — no automatic changes are made.
- The initial implementation aggregates across all containers in scope; a follow-up
  iteration will iterate per pod/container using Prometheus label sets.
- When Prometheus is unavailable, the endpoint returns HTTP 503.
- CPU throttle rate is surfaced from ADR-020 logic and may be `null` on older OCP configs.
