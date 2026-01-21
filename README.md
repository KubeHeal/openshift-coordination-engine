# OpenShift Coordination Engine

[![CI](https://github.com/tosin2013/openshift-coordination-engine/workflows/CI/badge.svg)](https://github.com/tosin2013/openshift-coordination-engine/actions)
[![Container](https://quay.io/repository/takinosh/openshift-coordination-engine/status)](https://quay.io/repository/takinosh/openshift-coordination-engine)
[![Go Report Card](https://goreportcard.com/badge/github.com/tosin2013/openshift-coordination-engine)](https://goreportcard.com/report/github.com/tosin2013/openshift-coordination-engine)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

A Go-based coordination engine for multi-layer remediation in OpenShift/Kubernetes environments. Orchestrates automated incident response across infrastructure, platform, and application layers with intelligent deployment-aware strategies.

## Features

- **Multi-Layer Coordination**: Orchestrates remediation across infrastructure (nodes, MCO), platform (operators, SDN), and application layers
- **Deployment-Aware**: Detects deployment methods (ArgoCD, Helm, Operator, Manual) and applies appropriate remediation strategies
- **GitOps Integration**: Respects ArgoCD workflows and maintains Git as the source of truth
- **ML-Enhanced**: Integrates with Python ML service for anomaly detection and predictive analysis
- **Production-Ready**: Built-in health checks, metrics, RBAC, and graceful degradation

## Quick Start

### Prerequisites

- Go 1.21+
- Kubernetes 1.28+ or OpenShift 4.14+
- kubectl/oc CLI configured

### Installation

#### Using Container Image

```bash
# Pull the latest image
podman pull quay.io/takinosh/openshift-coordination-engine:latest

# Run with KServe integration (recommended - ADR-039)
podman run -d \
  -p 8080:8080 \
  -p 9090:9090 \
  -e ENABLE_KSERVE_INTEGRATION=true \
  -e KSERVE_NAMESPACE=self-healing-platform \
  -e KSERVE_ANOMALY_DETECTOR_SERVICE=anomaly-detector-predictor \
  quay.io/takinosh/openshift-coordination-engine:latest
```

#### Using Helm

```bash
# Add Helm repository (if available)
helm repo add coordination-engine https://github.com/tosin2013/openshift-coordination-engine

# Install with KServe integration (recommended)
helm install coordination-engine ./charts/coordination-engine \
  --namespace self-healing-platform \
  --create-namespace
```

#### From Source

```bash
# Clone repository
git clone https://github.com/tosin2013/openshift-coordination-engine.git
cd openshift-coordination-engine

# Build
make build

# Run
./bin/coordination-engine
```

## Multi-Version Support

This project supports multiple OpenShift versions through version-specific release branches and container images.

### Supported Versions

| OpenShift | Kubernetes | Image Tag | Branch | Status |
|-----------|-----------|-----------|--------|--------|
| 4.18 | 1.31 | `ocp-4.18-latest` | `release-4.18` | ✅ Supported |
| 4.19 | 1.32 | `ocp-4.19-latest` | `release-4.19` | ✅ Supported |
| 4.20 | 1.33 | `ocp-4.20-latest` | `release-4.20` | ✅ Supported (Current) |

**Support Policy**: Rolling 3-version window. When OpenShift 4.21 is released, support for 4.18 will be dropped.

### Version Selection

#### Check Your Cluster Version

```bash
oc version
# Server Version: 4.19.5
```

#### Pull Version-Specific Image

```bash
# For OpenShift 4.18
podman pull quay.io/takinosh/openshift-coordination-engine:ocp-4.18-latest

# For OpenShift 4.19
podman pull quay.io/takinosh/openshift-coordination-engine:ocp-4.19-latest

# For OpenShift 4.20
podman pull quay.io/takinosh/openshift-coordination-engine:ocp-4.20-latest
```

#### Specific Git SHA Tags

For reproducible deployments, use SHA-tagged images:

```bash
# Example: OpenShift 4.20 with specific commit
podman pull quay.io/takinosh/openshift-coordination-engine:ocp-4.20-a1b2c3d
```

#### Deploy with Helm

```bash
# OpenShift 4.18
helm install coordination-engine ./charts/coordination-engine \
  --values ./charts/coordination-engine/values-ocp-4.18.yaml \
  --namespace self-healing-platform

# OpenShift 4.19
helm install coordination-engine ./charts/coordination-engine \
  --values ./charts/coordination-engine/values-ocp-4.19.yaml \
  --namespace self-healing-platform

# OpenShift 4.20 (or use default values.yaml)
helm install coordination-engine ./charts/coordination-engine \
  --values ./charts/coordination-engine/values-ocp-4.20.yaml \
  --namespace self-healing-platform
```

Or override directly:

```bash
helm install coordination-engine ./charts/coordination-engine \
  --set image.tag=ocp-4.19-latest \
  --namespace self-healing-platform
```

**⚠️ Important**: Always match container image version to your OpenShift cluster version to avoid Kubernetes API compatibility issues.

### Development Branches

- **main**: Development branch, auto-syncs to `release-4.20`
- **release-4.18**: Supports OpenShift 4.18 (client-go v0.31.x)
- **release-4.19**: Supports OpenShift 4.19 (client-go v0.32.x)
- **release-4.20**: Supports OpenShift 4.20 (client-go v0.33.x)

**Note**: Direct development happens on `main`. Changes are automatically propagated to `release-4.20` and cherry-picked to older versions as needed.

For detailed version strategy documentation, see [VERSION-STRATEGY.md](docs/VERSION-STRATEGY.md).

## Configuration

### Environment Variables

#### Core Configuration

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `PORT` | HTTP server port | 8080 | No |
| `METRICS_PORT` | Prometheus metrics port | 9090 | No |
| `LOG_LEVEL` | Logging level | info | No |
| `NAMESPACE` | Kubernetes namespace | self-healing-platform | No |
| `ARGOCD_API_URL` | ArgoCD API endpoint | Auto-detect | No |
| `KUBECONFIG` | Kubernetes config file | In-cluster | No |

#### KServe Integration (ADR-039 - Recommended)

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `ENABLE_KSERVE_INTEGRATION` | Enable KServe integration | true | No |
| `KSERVE_NAMESPACE` | KServe InferenceService namespace | self-healing-platform | Yes* |
| `KSERVE_ANOMALY_DETECTOR_SERVICE` | Anomaly detector service name | - | Yes* |
| `KSERVE_PREDICTIVE_ANALYTICS_SERVICE` | Predictive analytics service name | - | No |
| `KSERVE_TIMEOUT` | KServe API call timeout | 10s | No |

*Required when `ENABLE_KSERVE_INTEGRATION=true`

**Example KServe configuration:**
```bash
export ENABLE_KSERVE_INTEGRATION=true
export KSERVE_NAMESPACE=self-healing-platform
export KSERVE_ANOMALY_DETECTOR_SERVICE=anomaly-detector-predictor
export KSERVE_PREDICTIVE_ANALYTICS_SERVICE=predictive-analytics-predictor
```

#### Legacy ML Service (Deprecated)

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `ML_SERVICE_URL` | Python ML service endpoint (deprecated) | - | No* |

*Required only when `ENABLE_KSERVE_INTEGRATION=false`

**⚠️ Note**: `ML_SERVICE_URL` is deprecated. Use KServe integration instead (ADR-039).

## Deployment Prerequisites

### KServe Model Dependencies

The coordination engine requires KServe InferenceServices to be deployed and healthy before starting.

**1. Verify KServe InferenceServices exist:**
```bash
kubectl get inferenceservice -n self-healing-platform

# Expected output:
# NAME                    URL   READY   PREV   LATEST   AGE
# anomaly-detector        ...   True    100           5m
# predictive-analytics    ...   True    100           5m
```

**2. Verify InferenceService pods are running:**
```bash
kubectl get pods -n self-healing-platform -l serving.kserve.io/inferenceservice

# Expected: All pods in Running state with 2/2 containers ready
```

**3. Verify model files are present:**
- **PVC-based**: Ensure `model-storage-pvc` contains model subdirectories
  ```bash
  kubectl exec -n self-healing-platform deployment/model-uploader -- ls -la /models
  ```
- **S3-based**: Verify S3 bucket contains model artifacts

**4. Test model endpoints manually:**
```bash
kubectl run -it --rm curl --image=curlimages/curl -n self-healing-platform -- \
  curl http://predictive-analytics-predictor.self-healing-platform.svc:8080/v1/models/model

# Expected: JSON response with model metadata
```

**Troubleshooting 404 Errors:**

If you see "KServe model 'model' does not exist" errors:
1. Check InferenceService status: `kubectl describe inferenceservice <name> -n self-healing-platform`
2. Check pod logs: `kubectl logs -n self-healing-platform -l serving.kserve.io/inferenceservice=<name>`
3. Verify model files exist in PVC or S3
4. Ensure coordination engine deployed AFTER KServe models (ArgoCD sync wave 3)

### RBAC Setup

The coordination engine requires specific Kubernetes permissions. Apply the RBAC manifests:

```bash
kubectl apply -f charts/coordination-engine/templates/serviceaccount.yaml
kubectl apply -f charts/coordination-engine/templates/role.yaml
kubectl apply -f charts/coordination-engine/templates/rolebinding.yaml
```

See [RBAC Documentation](docs/RBAC.md) for detailed permissions.

## API Endpoints

### Health Check

The coordination engine provides two health endpoints following Kubernetes best practices:

```bash
# Lightweight health check (Kubernetes standard)
curl http://localhost:8080/health

# Detailed health check with dependency monitoring
curl http://localhost:8080/api/v1/health
```

See [API Documentation](docs/API.md) for complete endpoint details.

### Trigger Remediation

```bash
curl -X POST http://localhost:8080/api/v1/remediation/trigger \
  -H "Content-Type: application/json" \
  -d '{
    "namespace": "production",
    "resource_type": "pod",
    "resource_name": "my-app-abc123",
    "issue_type": "CrashLoopBackOff",
    "severity": "high"
  }'
```

### List Incidents

```bash
curl http://localhost:8080/api/v1/incidents?namespace=production&status=active
```

### Get Workflow Status

```bash
curl http://localhost:8080/api/v1/workflows/wf-12345678
```

See [API Documentation](docs/API.md) for complete API reference.

## Architecture

The coordination engine consists of several components:

- **Layer Detector**: Identifies which layers (infrastructure, platform, application) are affected
- **Deployment Detector**: Determines how applications were deployed (ArgoCD, Helm, Operator, Manual)
- **Multi-Layer Planner**: Creates ordered remediation plans across layers
- **Strategy Selector**: Routes to appropriate remediator based on deployment method
- **Remediators**: Execute deployment-specific remediation (ArgoCD sync, Helm rollback, etc.)
- **Health Checker**: Validates system state at each layer after remediation

See [Architecture Documentation](docs/adrs/README.md) for detailed design decisions.

## Development

### Build

```bash
make build
```

### Test

```bash
# Unit tests
make test

# Integration tests
make test-integration

# E2E tests
make test-e2e

# Coverage
make coverage
```

### Linting

```bash
make lint
make fmt
```

## Deployment

### OpenShift

```bash
# Apply RBAC
oc apply -f charts/coordination-engine/templates/serviceaccount.yaml
oc apply -f charts/coordination-engine/templates/role.yaml
oc apply -f charts/coordination-engine/templates/rolebinding.yaml

# Deploy via Helm
helm install coordination-engine ./charts/coordination-engine \
  --set image.repository=quay.io/takinosh/openshift-coordination-engine \
  --set image.tag=latest \
  --set mlServiceUrl=http://aiops-ml-service:8080 \
  --namespace self-healing-platform
```

### Verification

```bash
# Check pod status
kubectl get pods -n self-healing-platform

# Check health endpoint
kubectl port-forward svc/coordination-engine 8080:8080 -n self-healing-platform
curl http://localhost:8080/health  # Lightweight check

# View logs
kubectl logs -f deployment/coordination-engine -n self-healing-platform

# Check metrics
curl http://localhost:9090/metrics
```

## Monitoring

The coordination engine exposes Prometheus metrics on port 9090:

- `coordination_engine_remediation_total` - Total remediation attempts
- `coordination_engine_remediation_duration_seconds` - Remediation duration
- `coordination_engine_argocd_sync_total` - ArgoCD sync operations
- `coordination_engine_ml_layer_detection_total` - ML-enhanced detections

See [Monitoring Guide](docs/MONITORING.md) for complete metrics reference.

## Troubleshooting

### Common Issues

**RBAC Permission Denied**
```bash
# Verify permissions
kubectl auth can-i get pods --as=system:serviceaccount:self-healing-platform:self-healing-operator
kubectl auth can-i patch deployments --as=system:serviceaccount:self-healing-platform:self-healing-operator
```

**ML Service Connection Failed**
```bash
# Check ML service health
curl http://aiops-ml-service:8080/health

# Verify network connectivity
kubectl exec -it deployment/coordination-engine -- curl http://aiops-ml-service:8080/health
```

**ArgoCD Integration Not Working**
```bash
# Check ArgoCD API access
oc get applications -n openshift-gitops

# Verify ArgoCD URL configuration
kubectl get deployment coordination-engine -n self-healing-platform -o yaml | grep ARGOCD_API_URL
```

## Contributing

Contributions are welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](LICENSE) file for details.

## Documentation

- [Architecture Decision Records](docs/adrs/README.md)
- [API Reference](docs/API.md)
- [RBAC Configuration](docs/RBAC.md)
- [Development Guide](CLAUDE.md)
- [Implementation Status](docs/IMPLEMENTATION-PLAN.md)

## Support

- **Issues**: [GitHub Issues](https://github.com/tosin2013/openshift-coordination-engine/issues)
- **Discussions**: [GitHub Discussions](https://github.com/tosin2013/openshift-coordination-engine/discussions)

## Acknowledgments

Built with:
- [client-go](https://github.com/kubernetes/client-go) - Kubernetes Go client
- [Gorilla Mux](https://github.com/gorilla/mux) - HTTP routing
- [Logrus](https://github.com/sirupsen/logrus) - Structured logging
- [Prometheus](https://prometheus.io/) - Metrics and monitoring
