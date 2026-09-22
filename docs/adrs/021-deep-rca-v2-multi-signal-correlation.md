# ADR-021: Deep RCA v2 — Multi-Signal Correlation Endpoint

## Status
IMPLEMENTED — 2026-09-22

## Context

Deep Root-Cause Analysis was deferred from v1.1.0 due to external Istio/Kiali
dependencies.  The coordination engine already detects anomalies (ADR-017) and
predicts resource exhaustion (ADR-018), but neither endpoint answers the
question **"why did this service break?"** by correlating signals across
multiple Kubernetes subsystems.

Operators investigating an incident typically switch between `kubectl get events`,
`kubectl get networkpolicy`, and `istioctl analyze` — a slow, manual process.
A single `POST /api/v1/investigate/rca` endpoint that correlates all three
signal types within a time window dramatically reduces mean-time-to-diagnosis.

### Design Constraints

| Constraint | Detail |
|---|---|
| Istio is optional | Many clusters do not run Istio; the endpoint must degrade gracefully. |
| Read-only access | The engine must never modify NetworkPolicies or VirtualServices. |
| Bounded latency | The endpoint must respond within 30 s even on large clusters. |
| Existing RBAC | NetworkPolicy read access is already granted (ADR-006); Istio CRD access must be added. |

## Decision

Implement a **three-correlator architecture** behind a single REST endpoint:

```
POST /api/v1/investigate/rca
```

### Signal Correlators

| # | Signal Type | Source | Availability |
|---|---|---|---|
| 1 | **Pod Events** | `core/v1` Events API | Always (Kubernetes native) |
| 2 | **NetworkPolicy** | `networking.k8s.io/v1` | Always (Kubernetes native) |
| 3 | **Istio VirtualService** | `networking.istio.io/v1beta1` via dynamic client | Optional (gracefully skipped) |

### Architecture

```
┌──────────────────────────────────────────────────┐
│  RCAHandler  (pkg/api/v1/rca.go)                 │
│  - validates request                             │
│  - delegates to Aggregator                       │
└──────────────────┬───────────────────────────────┘
                   │
┌──────────────────▼───────────────────────────────┐
│  Aggregator  (internal/rca/aggregator.go)        │
│  - runs correlators in parallel (errgroup)       │
│  - merges + deduplicates findings                │
│  - computes weighted confidence_score            │
├──────────────┬───────────────┬───────────────────┤
│ EventCorr.   │ NetpolCorr.   │ IstioCorr.        │
│ events.go    │ networkpol.go │ virtualservice.go  │
│ (clientset)  │ (clientset)   │ (dynamic client)   │
└──────────────┴───────────────┴───────────────────┘
```

### Request / Response Contract

**Request:**
```json
{
  "service":    "my-app",
  "namespace":  "production",
  "start_time": "2026-09-22T10:00:00Z",
  "end_time":   "2026-09-22T11:00:00Z"
}
```

**Response:**
```json
{
  "status": "success",
  "service": "my-app",
  "namespace": "production",
  "time_range": { "start": "...", "end": "..." },
  "root_causes": [
    {
      "signal_type":       "pod_event",
      "description":       "Container OOMKilled in pod my-app-abc123",
      "evidence":          { ... },
      "confidence":        0.92,
      "remediation_steps": ["Increase memory limits", "Check for memory leaks"]
    }
  ],
  "confidence_score":     0.89,
  "affected_components":  ["pod/my-app-abc123", "networkpolicy/deny-all"],
  "istio_available":      false
}
```

### Confidence Scoring

Each correlator assigns per-finding confidence (0.0–1.0) based on evidence
strength.  The aggregator computes a composite score using signal-type weights:

| Signal Type | Weight | Rationale |
|---|---|---|
| Pod Events | 0.40 | Highest direct evidence (OOMKill, CrashLoop, etc.) |
| NetworkPolicy | 0.30 | Strong indirect evidence (connectivity failures) |
| Istio VirtualService | 0.30 | Routing misconfigurations (when available) |

When Istio is not installed the weights are redistributed:
- Pod Events: 0.55
- NetworkPolicy: 0.45

### Graceful Istio Degradation

1. On first request the Istio correlator calls
   `clientset.Discovery().ServerResourcesForGroupVersion("networking.istio.io/v1beta1")`.
2. The result is cached for the lifetime of the process.
3. If the CRD group is absent, the correlator returns an empty finding list and
   sets `istio_available: false` in the response.
4. No error is raised — the other two correlators continue normally.

### Event Correlation Strategy

The Event correlator:
1. Lists pods matching the service name pattern (`<service>-*`) in the namespace.
2. Queries `core/v1` Events for those pods within `[start_time, end_time]`.
3. Classifies events by reason: `OOMKilled`, `CrashLoopBackOff`,
   `FailedScheduling`, `Unhealthy`, `FailedMount`, `ImagePullBackOff`, etc.
4. Groups recurring events and assigns confidence based on count and recency.

### NetworkPolicy Correlation Strategy

The NetworkPolicy correlator:
1. Lists all NetworkPolicies in the target namespace.
2. Resolves pods belonging to the service.
3. For each policy, checks whether pod labels match the policy's `podSelector`.
4. Detects problematic patterns:
   - **Deny-all** policies (empty ingress/egress rules).
   - **Missing ingress** rules for pods that should receive traffic.
   - **Label selector mismatches** between the policy and service pods.

### Istio VirtualService Correlation Strategy

The Istio correlator:
1. Lists VirtualServices in the namespace via the dynamic client.
2. Inspects each VirtualService for:
   - Routes referencing the target service with invalid hosts or ports.
   - Traffic weight distributions that do not sum to 100.
   - Missing destination rules for referenced hosts.
3. Assigns per-finding confidence based on misconfiguration severity.

## Consequences

### Positive
- Single endpoint replaces three manual investigation steps.
- Graceful degradation keeps the endpoint useful on non-Istio clusters.
- Parallel correlator execution keeps latency bounded.
- Weighted confidence scoring surfaces the most likely root cause first.

### Negative
- Event API does not support server-side time-range filtering; client-side
  filtering is needed after listing.
- Istio CRD detection adds a one-time discovery call on first request.
- NetworkPolicy analysis is heuristic — it cannot prove that traffic was
  actually blocked, only that a policy *could* block it.

### Risks
- Very large clusters may have thousands of events in the time window;
  pagination or field selectors limit the blast radius.
- Istio API versions may change; the dynamic client approach isolates the
  engine from compile-time CRD dependencies.

## Related

- Issue: [#72](https://github.com/KubeHeal/openshift-coordination-engine/issues/72)
- ADR-006: RBAC Permissions (NetworkPolicy read access)
- ADR-014: Prometheus/Thanos Observability (incident management)
- ADR-017: HTTP Application Signal Integration (enriched signals)
- ADR-018: Disk Exhaustion / Memory Leak Detection
