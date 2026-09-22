# ADR-022: Alerting Sink Interface for Critical Anomaly Notifications

## Status
IMPLEMENTED — 2026-09-22

## Context

The coordination engine detects critical anomalies via the
`POST /api/v1/anomalies/analyze` endpoint (ADR-017) but has no mechanism to
proactively notify on-call teams.  Operators must poll the API or watch logs
to discover that a critical anomaly was detected.  This gap increases
mean-time-to-response (MTTR) and breaks the AIOps detect-to-action loop.

Three notification targets are common in OpenShift operations:

| Target | Use Case |
|---|---|
| **Slack** | ChatOps — team channels receive rich anomaly summaries |
| **PagerDuty** | On-call escalation via Events API v2 |
| **Alertmanager** | Integration with existing Prometheus alerting pipelines |

## Decision

Implement a pluggable **AlertSink** interface in `pkg/notifier/` with three
concrete implementations and a **Dispatcher** that fans out to all configured
sinks asynchronously.

### Interface

```go
type AlertSink interface {
    Name() string
    Send(ctx context.Context, alert Alert) error
}
```

### Architecture

```
AnomalyHandler.AnalyzeAnomalies()
        │
        │  severity == "critical"
        ▼
   Dispatcher.Dispatch(alert)
        │
   ┌────┼────────────┐
   ▼    ▼            ▼
 Slack  PagerDuty  Alertmanager
(goroutine each — fire-and-forget)
```

### Dispatch Model

- **Asynchronous**: Each sink runs in its own goroutine so the HTTP response
  is never delayed by webhook latency or failures.
- **Fire-and-forget**: Sink errors are logged at WARN level but never
  propagated to the caller.  The anomaly response is returned regardless.
- **Configurable threshold**: The `KUBEHEAL_ALERT_SEVERITY_THRESHOLD` env var
  controls the minimum severity that triggers alerts (default: `critical`).

### Configuration

| Env Var | Default | Description |
|---|---|---|
| `KUBEHEAL_SLACK_WEBHOOK_URL` | (empty) | Slack incoming webhook URL; empty disables |
| `KUBEHEAL_PAGERDUTY_ROUTING_KEY` | (empty) | PagerDuty Events API v2 routing key; empty disables |
| `KUBEHEAL_ALERTMANAGER_URL` | (empty) | Alertmanager base URL; empty disables |
| `KUBEHEAL_ALERT_SEVERITY_THRESHOLD` | `critical` | Minimum severity to dispatch |

### Slack Payload

POST to the webhook URL:
```json
{
  "text": "KubeHeal: Critical anomaly detected",
  "attachments": [{
    "color": "#FF0000",
    "title": "Critical Anomaly — namespace/deployment",
    "fields": [
      { "title": "Score", "value": "0.95", "short": true },
      { "title": "Severity", "value": "critical", "short": true }
    ],
    "text": "Explanation and recommendation"
  }]
}
```

### PagerDuty Payload

POST to `https://events.pagerduty.com/v2/enqueue`:
```json
{
  "routing_key": "...",
  "event_action": "trigger",
  "payload": {
    "summary": "KubeHeal: Critical anomaly (score 0.95) in namespace/deployment",
    "severity": "critical",
    "source": "kubeheal-coordination-engine",
    "component": "anomaly-detector",
    "custom_details": { "anomaly_score": 0.95, "recommendation": "..." }
  }
}
```

### Alertmanager Payload

POST to `{base_url}/api/v2/alerts`:
```json
[{
  "labels": {
    "alertname": "KubeHealAnomaly",
    "severity": "critical",
    "namespace": "production",
    "service": "my-app"
  },
  "annotations": {
    "summary": "Critical anomaly detected (score 0.95)",
    "recommendation": "immediate_investigation"
  },
  "startsAt": "2026-09-22T10:30:00Z"
}]
```

## Consequences

### Positive
- Closes the detect-to-notify gap for critical anomalies.
- Pluggable interface makes it easy to add new sinks (e.g., Teams, email).
- Async dispatch means zero impact on anomaly endpoint latency.
- Alertmanager integration reuses existing OpenShift monitoring pipelines.

### Negative
- Fire-and-forget means alert delivery is best-effort, not guaranteed.
- No deduplication — repeated anomaly requests may fire duplicate alerts.
  (Future: add a cooldown period per service.)

### Risks
- Webhook URLs and routing keys are secrets; Helm chart must store them in
  Kubernetes Secrets, not plain values.
- Alertmanager may be unreachable if monitoring namespace has NetworkPolicies.

## Related

- Issue: [#75](https://github.com/KubeHeal/openshift-coordination-engine/issues/75)
- ADR-017: HTTP Application Signal Integration (anomaly detection)
- ADR-014: Prometheus/Thanos Observability (incident management)
