# ADR-018: Disk Exhaustion ETA and Memory Leak Slope Detection

**Status**: Accepted  
**Date**: 2026-04-21  
**Deciders**: KubeHeal Engineering

## Context

The existing predictive analytics model (ADR-016, ADR-051 in platform) forecasts
CPU and memory usage but provides no mechanism for:

1. Predicting **when a filesystem will be full** (disk-full ETA).
2. Classifying whether a container's memory growth represents a **leak** or normal usage.

Both conditions are common root causes of production incidents (OOMKilled pods, disk-full
PVC failures) and are deterministic enough to be computed without an ML model.

## Decision

Add two new endpoints using deterministic analytics against Prometheus:

### GET /api/v1/predict/disk-exhaustion

- Queries `node_filesystem_avail_bytes` and `node_filesystem_size_bytes` with a 7-day
  `deriv()` to compute the daily fill rate.
- Returns `DaysUntilFull` and urgency classification:
  - **critical**: < 3 days
  - **warning**: < 7 days
  - **info**: ≥ 7 days
  - **stable**: usage not increasing
- Supports `?node=` and `?mountpoint=` query parameters.
- Gracefully degrades when < 7 days of Prometheus data is available.

### GET /api/v1/predict/memory-leak

- Queries `container_memory_working_set_bytes` over a 24-hour window.
- Uses `deriv()` for slope and compares current value vs 24-hour mean as an R² proxy.
- `LeakDetected = true` when slope > 0 **and** current > 1.1 × mean.
- Returns `Confidence` scaled by daily growth rate relative to current usage.

## Consequences

- Both endpoints require Prometheus retention ≥ 7 days (disk) and ≥ 24 hours (memory).
- No KServe dependency — fully deterministic using Prometheus only.
- When Prometheus is unavailable, both endpoints return HTTP 503.
- Memory leak detection is aggregated; a future iteration should iterate per pod/container label set.
