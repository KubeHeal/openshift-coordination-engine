# ADR-020: CPU Throttle Detection via cgroup CFS Metrics

**Status**: Accepted  
**Date**: 2026-04-21  
**Deciders**: KubeHeal Engineering

## Context

The existing anomaly detection (ADR-014) labels `cpu_throttling` as an issue type using
a heuristic based on CPU usage percentage. This is a poor proxy for actual throttling:
a pod can be throttled at 30% CPU utilisation if its `cpu.limits` is set too low.

The accurate signal is the Linux CFS (Completely Fair Scheduler) kernel counter:

```
container_cpu_cfs_throttled_seconds_total  — cumulative seconds throttled
container_cpu_cfs_periods_total            — cumulative CFS scheduling periods
```

The throttle rate is:

```
throttle_rate = rate(throttled_seconds[5m]) / rate(periods[5m])
```

A rate above 25% indicates the container is being meaningfully CPU-throttled.

## Decision

Replace the heuristic `cpu_throttling` label in `recommendations.go` with a real
CFS-based throttle rate computed from the metrics above.

Surface the throttle rate in two places:

1. **`EnrichedSignals.CPUThrottleRate`** in the anomaly analysis response (ADR-017).
2. **`ThrottleRatePct`** in right-sizing recommendations (ADR-019).

### Alert threshold

Set `ThrottlingDetected = true` when `throttle_rate > 0.25` (25%). This threshold
is consistent with Google SRE practices for identifying meaningful CPU contention.

### Availability

`container_cpu_cfs_throttled_seconds_total` is exposed by cAdvisor ≥ 0.39 and is
enabled by default in OCP 4.18+. On older configurations where the metric is absent,
the throttle rate is returned as `null` — the feature gracefully degrades.

## Consequences

- The `cpu_throttling` label in `recommendations.go` is superseded by this CFS metric.
- Throttle detection now works at low CPU utilisation, which was the key blind spot.
- No additional Prometheus scrape configuration is required for OCP 4.18+.
- `container_cpu_cfs_periods_total` must have a non-zero rate; when it is zero (idle
  container), division returns 0 and throttle rate is reported as 0.
