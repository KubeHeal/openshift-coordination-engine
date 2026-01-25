# Coordination Engine Helm Chart

Helm chart for deploying the OpenShift Coordination Engine.

## Prerequisites

- Kubernetes 1.28+ / OpenShift 4.14+
- Helm 3.0+
- ServiceAccount with appropriate RBAC permissions

## Installing the Chart

```bash
# Install in self-healing-platform namespace
helm install coordination-engine ./charts/coordination-engine \
  --namespace self-healing-platform \
  --create-namespace

# Install with custom values
helm install coordination-engine ./charts/coordination-engine \
  --namespace self-healing-platform \
  --values custom-values.yaml
```

## Uninstalling the Chart

```bash
helm uninstall coordination-engine -n self-healing-platform
```

## Configuration

The following table lists the configurable parameters of the Coordination Engine chart and their default values.

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `1` |
| `image.repository` | Image repository | `image-registry.openshift-image-registry.svc:5000/self-healing-platform/coordination-engine` |
| `image.tag` | Image tag | `latest` |
| `image.pullPolicy` | Image pull policy | `IfNotPresent` |
| `serviceAccount.create` | Create service account | `true` |
| `serviceAccount.name` | Service account name | `self-healing-operator` |
| `service.type` | Kubernetes service type | `ClusterIP` |
| `service.port` | HTTP service port | `8080` |
| `service.metricsPort` | Metrics service port | `9090` |
| `resources.requests.memory` | Memory request | `256Mi` |
| `resources.requests.cpu` | CPU request | `200m` |
| `resources.limits.memory` | Memory limit | `512Mi` |
| `resources.limits.cpu` | CPU limit | `500m` |
| `monitoring.enabled` | Enable Prometheus ServiceMonitor | `true` |
| `rbac.create` | Create RBAC resources | `true` |

## Example: Custom Values

```yaml
# custom-values.yaml
replicaCount: 2

image:
  repository: quay.io/myorg/coordination-engine
  tag: "v1.0.0"

resources:
  requests:
    memory: "512Mi"
    cpu: "500m"
  limits:
    memory: "1Gi"
    cpu: "1000m"

env:
  - name: LOG_LEVEL
    value: "debug"
  - name: ML_SERVICE_URL
    value: "http://my-ml-service:8080"

autoscaling:
  enabled: true
  minReplicas: 2
  maxReplicas: 5
  targetCPUUtilizationPercentage: 70
```

## Upgrading the Chart

```bash
helm upgrade coordination-engine ./charts/coordination-engine \
  --namespace self-healing-platform \
  --values custom-values.yaml
```

## Testing the Chart

```bash
# Lint the chart
helm lint ./charts/coordination-engine

# Render templates (dry-run)
helm template coordination-engine ./charts/coordination-engine

# Test installation (dry-run)
helm install coordination-engine ./charts/coordination-engine \
  --namespace self-healing-platform \
  --dry-run --debug
```

## Health Checks

The chart includes health checks for the coordination engine:

```bash
# Check deployment status
kubectl get deployment coordination-engine -n self-healing-platform

# Check pod status
kubectl get pods -l app.kubernetes.io/name=coordination-engine -n self-healing-platform

# Check health endpoint
kubectl port-forward svc/coordination-engine 8080:8080 -n self-healing-platform
curl http://localhost:8080/api/v1/health
```

## Troubleshooting

### Pod not starting

Check RBAC permissions:
```bash
kubectl auth can-i get pods \
  --as=system:serviceaccount:self-healing-platform:self-healing-operator \
  -n self-healing-platform
```

Check logs:
```bash
kubectl logs -f deployment/coordination-engine -n self-healing-platform
```

### Image pull errors

Ensure the image registry and credentials are correct:
```bash
kubectl get pods -n self-healing-platform
kubectl describe pod <pod-name> -n self-healing-platform
```
