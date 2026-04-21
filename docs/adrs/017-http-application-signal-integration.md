# ADR-017: HTTP Application Signal Integration for Anomaly Detection

**Status**: Accepted  
**Date**: 2026-04-21  
**Deciders**: KubeHeal Engineering

## Context

The original anomaly detection feature vector (ADR-014) covers five infrastructure metrics:
`node_cpu_utilization`, `node_memory_utilization`, `pod_cpu_usage`, `pod_memory_usage`,
and `container_restart_count`. These identify resource pressure but miss application-level
degradation signals — notably HTTP error rates and latency — that are visible to end users
before infrastructure metrics spike.

## Decision

Enrich the anomaly analysis response with optional application-level signals collected
**separately** from the KServe model's 45-feature vector. This preserves full backward
compatibility with the existing anomaly-detector model while surfacing HTTP signals as
metadata in `EnrichedSignals`.

### Signals added

| Signal | Prometheus source | Availability |
|--------|-------------------|--------------|
| `cpu_throttle_rate` | `container_cpu_cfs_throttled_seconds_total / container_cpu_cfs_periods_total` | cAdvisor (always) |
| `http_error_rate` | `rate(container_http_requests_total{status=~"5.."}[5m])` | Istio / instrumented apps |
| `http_response_time_p99_ms` | `histogram_quantile(0.99, istio_request_duration_milliseconds_bucket)` | Istio only |

All signals gracefully return `null` when the underlying metrics are absent.
`ThrottlingDetected` is set when throttle rate > 25%.
`HTTPDegraded` is set when error rate > 5% or P99 > 1000 ms.

## Consequences

- Anomaly response size increases slightly when enriched signals are available.
- No changes to the KServe model or its 45-feature input format.
- Clusters without Istio/OSSM will see `http_error_rate` and `http_response_time_p99_ms` as `null`.
- ADR-020 (throttle detection) is the definitive source for the `cpu_throttle_rate` metric.
