# ADR-015: KServe InferenceService Integration

## Status
IMPLEMENTED - 2026-01-28

## Context

The Go Coordination Engine originally integrated with a Python ML service via REST APIs (ADR-009). However, for production deployments on OpenShift with OpenShift AI (RHOAI), KServe InferenceServices provide a standardized, Kubernetes-native way to deploy and serve ML models.

KServe offers:
- Standardized inference protocols (v1/v2)
- Auto-scaling based on inference load
- Canary/blue-green deployments for models
- GPU scheduling and resource management
- Integration with OpenShift AI (RHOAI)

This ADR documents the KServe proxy client integration that allows the coordination engine to consume ML models deployed as KServe InferenceServices.

### Problem Statement (Issue #53)

The initial implementation had a bug where the KServe model name was hardcoded as `"model"` in API endpoints:

```
/v1/models/model:predict  ← WRONG
/v1/models/model          ← WRONG
```

This assumption was based on KServe's default behavior when `spec.predictor.model.name` is not set. However, in production deployments with OpenShift AI, models have explicit names configured:

```
/v1/models/anomaly-detector:predict  ← CORRECT
/v1/models/anomaly-detector          ← CORRECT
```

## Decision

### 1. Dynamic Model Name Discovery

The proxy client reads model names from environment variables following the pattern:

```bash
# Service name (required)
KSERVE_ANOMALY_DETECTOR_SERVICE=anomaly-detector-predictor

# Model name for KServe API paths (optional, defaults to logical name)
KSERVE_ANOMALY_DETECTOR_MODEL=anomaly-detector
```

The model name is used in KServe API endpoints:
- Prediction: `POST /v1/models/<model_name>:predict`
- Health: `GET /v1/models/<model_name>`

### 2. ModelInfo Struct

Each registered model includes a `KServeModelName` field:

```go
type ModelInfo struct {
    Name            string `json:"name"`              // Logical name (e.g., "anomaly-detector")
    ServiceName     string `json:"service_name"`      // K8s service DNS name
    KServeModelName string `json:"kserve_model_name"` // Name used in KServe API paths
    Namespace       string `json:"namespace"`
    URL             string `json:"url"`
}
```

### 3. Fallback Behavior

If `KSERVE_*_MODEL` environment variable is not set, the code falls back to using the logical model name (derived from the service name):

```go
// Example: KSERVE_ANOMALY_DETECTOR_SERVICE → anomaly-detector
kserveModelName := os.Getenv(modelEnvKey)
if kserveModelName == "" {
    kserveModelName = modelName  // Fallback to logical name
}
```

### 4. KServe vs Legacy ML Service

The coordination engine supports two ML backends:

| Feature | Legacy ML Service (ADR-009) | KServe (This ADR) |
|---------|----------------------------|-------------------|
| Protocol | Custom REST API | KServe v1/v2 Protocol |
| Configuration | `ML_SERVICE_URL` | `KSERVE_*_SERVICE`, `KSERVE_*_MODEL` |
| Deployment | Python Flask service | KServe InferenceService CRDs |
| Scaling | Manual/HPA | KPA (Knative autoscaler) |
| Multi-model | Single service | Multiple InferenceServices |

KServe is preferred for OpenShift AI deployments; legacy ML service remains available for backward compatibility.

## Implementation

### Environment Variables

```yaml
# Helm values.yaml
env:
  - name: ENABLE_KSERVE_INTEGRATION
    value: "true"
  - name: KSERVE_NAMESPACE
    value: "self-healing-platform"
  - name: KSERVE_PREDICTOR_PORT
    value: "8080"  # RawDeployment mode
  # Anomaly Detector
  - name: KSERVE_ANOMALY_DETECTOR_SERVICE
    value: "anomaly-detector-predictor"
  - name: KSERVE_ANOMALY_DETECTOR_MODEL
    value: "anomaly-detector"
  # Predictive Analytics
  - name: KSERVE_PREDICTIVE_ANALYTICS_SERVICE
    value: "predictive-analytics-predictor"
  - name: KSERVE_PREDICTIVE_ANALYTICS_MODEL
    value: "predictive-analytics"
  - name: KSERVE_TIMEOUT
    value: "10s"
```

### Proxy Client Usage

```go
// Create client
client, err := kserve.NewProxyClient(kserve.ProxyConfig{
    Namespace:     "self-healing-platform",
    PredictorPort: 8080,
    Timeout:       10 * time.Second,
}, logger)

// Make prediction - uses correct endpoint:
// POST http://anomaly-detector-predictor.self-healing-platform.svc:8080/v1/models/anomaly-detector:predict
resp, err := client.Predict(ctx, "anomaly-detector", instances)

// Check health - uses correct endpoint:
// GET http://anomaly-detector-predictor.self-healing-platform.svc:8080/v1/models/anomaly-detector
health, err := client.CheckModelHealth(ctx, "anomaly-detector")
```

### Health Check Job

The Helm chart includes a pre-deployment health check job that verifies KServe models are ready:

```yaml
# templates/kserve-health-check.yaml
{{- range .Values.kserve.models }}
SERVICE_NAME="{{ .name }}"
MODEL_NAME="{{ .modelName }}"
HEALTH_PATH="/v1/models/$MODEL_NAME"
curl -f http://$SERVICE_NAME.$KSERVE_NS.svc:$KSERVE_PORT$HEALTH_PATH
{{- end }}
```

## Consequences

### Positive
- ✅ Dynamic model name discovery from environment variables
- ✅ Backward compatible with existing deployments
- ✅ Fallback to logical name when `_MODEL` env var not set
- ✅ Integrates with OpenShift AI (RHOAI) InferenceServices
- ✅ Health check job ensures models are ready before coordination engine starts
- ✅ Supports multiple models with different names

### Negative
- ⚠️ Requires additional environment variables for model names
- ⚠️ Must keep Helm values in sync with InferenceService configurations
- ⚠️ RawDeployment mode uses port 8080 (not standard HTTP/80)

## Migration Guide

### From Hardcoded "model" Name

If upgrading from a version with hardcoded `"model"` name:

1. Add `KSERVE_*_MODEL` environment variables to your deployment:
   ```yaml
   - name: KSERVE_ANOMALY_DETECTOR_MODEL
     value: "anomaly-detector"
   ```

2. Verify your InferenceService has the correct model name:
   ```bash
   oc exec -n self-healing-platform anomaly-detector-predictor-xxx -- \
     curl -s http://localhost:8080/v2/models
   # Should show: {"models":["anomaly-detector"]}
   ```

3. Update Helm values if using the health check job:
   ```yaml
   kserve:
     models:
       - name: "anomaly-detector-predictor"
         modelName: "anomaly-detector"
   ```

### From Legacy ML Service (ADR-009)

If migrating from the legacy Python ML service:

1. Deploy KServe InferenceServices with your models
2. Set `ENABLE_KSERVE_INTEGRATION=true`
3. Remove or comment out `ML_SERVICE_URL`
4. Add KServe environment variables as shown above

## Testing

### Unit Tests

```go
func TestProxyClient_LoadModelsFromEnv_WithModelEnvVar(t *testing.T) {
    os.Setenv("KSERVE_ANOMALY_DETECTOR_SERVICE", "anomaly-detector-predictor")
    os.Setenv("KSERVE_ANOMALY_DETECTOR_MODEL", "custom-model-name")
    defer os.Unsetenv("KSERVE_ANOMALY_DETECTOR_SERVICE")
    defer os.Unsetenv("KSERVE_ANOMALY_DETECTOR_MODEL")

    client, _ := NewProxyClient(cfg, log)
    model, _ := client.GetModel("anomaly-detector")
    
    assert.Equal(t, "custom-model-name", model.KServeModelName)
}
```

### Integration Tests

```bash
# Verify model responds with correct name
oc exec -n self-healing-platform coordination-engine-xxx -- \
  curl -s http://anomaly-detector-predictor:8080/v1/models/anomaly-detector
# Should return: {"name":"anomaly-detector","versions":null,...}
```

## Configuration Reference

| Environment Variable | Description | Default |
|---------------------|-------------|---------|
| `ENABLE_KSERVE_INTEGRATION` | Enable KServe integration | `true` |
| `KSERVE_NAMESPACE` | Namespace for InferenceServices | `self-healing-platform` |
| `KSERVE_PREDICTOR_PORT` | Predictor port (RawDeployment) | `8080` |
| `KSERVE_TIMEOUT` | Request timeout | `10s` |
| `KSERVE_*_SERVICE` | InferenceService name | Required |
| `KSERVE_*_MODEL` | KServe model name for API paths | Defaults to logical name |

## References

- [KServe v1 Protocol](https://kserve.github.io/website/latest/modelserving/data_plane/v1_protocol/)
- [GitHub Issue #53](https://github.com/tosin2013/openshift-coordination-engine/issues/53)
- [OpenShift AI Documentation](https://docs.redhat.com/en/documentation/red_hat_openshift_ai_self-managed)

## Related ADRs

- [ADR-009](009-python-ml-integration.md) - Legacy Python ML Service Integration
- [ADR-014](014-prometheus-thanos-observability-incident-management.md) - Prometheus metrics for ML predictions
- Platform ADR-039: Non-ArgoCD Application Remediation
- Platform ADR-040: Multi-Layer Coordination Strategy

---

*Created: 2026-01-28*
*Last Updated: 2026-01-28*
