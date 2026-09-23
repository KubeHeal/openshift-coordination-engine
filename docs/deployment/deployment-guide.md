# Coordination Engine Deployment Guide

> **Version:** 1.2.0 | **Last updated:** 2026-09-23

This guide describes how to deploy and operate the Coordination Engine on OpenShift. It covers Helm installation, RBAC, KServe setup, Prometheus integration, persistent storage, alert sinks, upgrades, and troubleshooting.

## Deployment Methods

| Method | Use Case |
|---|---|
| **Helm chart** (recommended) | Production OpenShift clusters. |
| **kubeheal-operator** | Managed lifecycle with OLM. |
| **Binary** | Local development and testing. |

This guide focuses on the Helm chart method. For local development, see the [Deployment Checklist](../DEPLOYMENT-CHECKLIST.md).

## Prerequisites

| Requirement | Minimum Version |
|---|---|
| OpenShift | 4.20 |
| Kubernetes | 1.33 |
| Helm | 3.0 |
| KServe (CRDs and controller) | 0.11+ |
| Prometheus (in-cluster) | Operator-managed or standalone |

The engine supports OpenShift 4.20, 4.21, and 4.22. Each version has a dedicated values file.

## Step 1: Create the Namespace

```bash
kubectl create namespace self-healing-platform
```

The engine runs in the `self-healing-platform` namespace by default. You can change this at install time.

## Step 2: Select the Values File

The chart ships with version-specific values files that set the correct image tag, OpenShift version metadata, and Kubernetes version:

| OpenShift Version | Values File | Image Tag |
|---|---|---|
| 4.20 | `values-ocp-4.20.yaml` | `ocp-4.20-latest` |
| 4.21 | `values-ocp-4.21.yaml` | `ocp-4.21-latest` |
| 4.22 | `values-ocp-4.22.yaml` | `ocp-4.22-latest` |

Identify your cluster version:

```bash
oc version
```

## Step 3: Install the Helm Chart

Install using the values file that matches your cluster version. This example uses OpenShift 4.22:

```bash
helm install coordination-engine ./charts/coordination-engine \
  -n self-healing-platform \
  -f ./charts/coordination-engine/values-ocp-4.22.yaml \
  --set kserve.enabled=true \
  --set kserve.namespace=self-healing-platform
```

Helm creates the following resources:

- Deployment (1 replica by default)
- Service (ClusterIP on port 8080 and 9090)
- ServiceAccount
- Role and RoleBinding
- ClusterRoleBinding (for cross-namespace read access)
- ServiceMonitor (when `monitoring.enabled=true`)
- PersistentVolumeClaim (when `persistence.enabled=true`)
- KServe health check Job (when `kserve.enabled=true`)

## Step 4: Verify the Deployment

```bash
# Check pod status
kubectl get pods -n self-healing-platform -l app.kubernetes.io/name=coordination-engine

# Check the health endpoint
kubectl exec -n self-healing-platform deploy/coordination-engine -- \
  curl -s http://localhost:8080/api/v1/health | jq .

# Check the logs
kubectl logs -n self-healing-platform deploy/coordination-engine --tail=20
```

**Expected startup log messages:**

```
Starting OpenShift Coordination Engine
Kubernetes clients initialized
RBAC permissions verified successfully
Deployment detector initialized
Starting API server port=8080
Starting metrics server port=9090
```

## RBAC Resources

The chart creates a Role with permissions organized by API group. The ServiceAccount name defaults to `self-healing-operator`.

### Core Permissions

| API Group | Resources | Verbs |
|---|---|---|
| `""` (core) | pods, services, configmaps, secrets, events | get, list, watch, create, update, patch, delete |
| `""` (core) | namespaces, nodes, endpoints | get, list, watch |
| `""` (core) | persistentvolumes, persistentvolumeclaims | get, list, watch |
| `apps` | deployments, replicasets, daemonsets, statefulsets | get, list, watch, create, update, patch, delete |
| `batch` | jobs, cronjobs | get, list, watch, create, update, patch, delete |

### Monitoring and Networking

| API Group | Resources | Verbs |
|---|---|---|
| `monitoring.coreos.com` | servicemonitors, prometheusrules | get, list, watch, create, update, patch, delete |
| `networking.k8s.io` | networkpolicies | get, list, watch |
| `networking.istio.io` | virtualservices, destinationrules | get, list, watch |

### OpenShift-Specific

| API Group | Resources | Verbs |
|---|---|---|
| `argoproj.io` | applications | get, list, watch |
| `machineconfiguration.openshift.io` | machineconfigs, machineconfigpools | get, list, watch |
| `operator.openshift.io`, `config.openshift.io` | clusteroperators | get, list, watch |

To verify RBAC permissions after installation:

```bash
kubectl auth can-i list pods \
  --as=system:serviceaccount:self-healing-platform:self-healing-operator \
  -n self-healing-platform
```

## KServe InferenceService Setup

The engine requires two KServe InferenceServices: `anomaly-detector` and `predictive-analytics`.

### Deploy the InferenceServices

1. Verify KServe CRDs are installed:
   ```bash
   kubectl get crd inferenceservices.serving.kserve.io
   ```

2. Deploy the anomaly detector:
   ```yaml
   apiVersion: serving.kserve.io/v1beta1
   kind: InferenceService
   metadata:
     name: anomaly-detector
     namespace: self-healing-platform
   spec:
     predictor:
       model:
         modelFormat:
           name: sklearn
         runtime: kserve-sklearnserver
   ```

3. Deploy predictive analytics:
   ```yaml
   apiVersion: serving.kserve.io/v1beta1
   kind: InferenceService
   metadata:
     name: predictive-analytics
     namespace: self-healing-platform
   spec:
     predictor:
       model:
         modelFormat:
           name: sklearn
         runtime: kserve-sklearnserver
   ```

4. Wait for both services to become ready:
   ```bash
   kubectl get inferenceservices -n self-healing-platform
   ```
   Both services must show `READY: True` before the engine can use them.

### KServe Environment Variables

The chart sets these by default:

| Variable | Default Value | Purpose |
|---|---|---|
| `ENABLE_KSERVE_INTEGRATION` | `true` | Enable KServe integration. |
| `KSERVE_NAMESPACE` | `self-healing-platform` | InferenceService namespace. |
| `KSERVE_PREDICTOR_PORT` | `8080` | Predictor port (RawDeployment mode). |
| `KSERVE_ANOMALY_DETECTOR_SERVICE` | `anomaly-detector-predictor` | Anomaly detector service name. |
| `KSERVE_PREDICTIVE_ANALYTICS_SERVICE` | `predictive-analytics-predictor` | Predictive analytics service name. |
| `KSERVE_TIMEOUT` | `10s` | Timeout for KServe calls. |

### Add Custom Models

Register additional KServe models by adding environment variables with the pattern `KSERVE_<MODEL_NAME>_SERVICE`:

```yaml
env:
  - name: KSERVE_DISK_FAILURE_PREDICTOR_SERVICE
    value: "disk-failure-predictor-predictor"
```

The engine discovers these variables at startup and registers each model automatically.

## Prometheus Integration

The engine queries Prometheus for live metrics to feed into anomaly detection and predictions.

### Configure the Prometheus URL

Set the `PROMETHEUS_URL` environment variable. In OpenShift, the Thanos Querier is the standard endpoint:

```yaml
env:
  - name: PROMETHEUS_URL
    value: "https://thanos-querier.openshift-monitoring.svc:9091"
```

### ServiceMonitor

When `monitoring.enabled=true` (the default), the chart creates a ServiceMonitor that configures Prometheus to scrape the engine metrics port (9090).

**Key metrics exported:**

| Metric | Type | Description |
|---|---|---|
| `coordination_engine_remediation_total` | Counter | Total remediation workflows triggered. |
| `coordination_engine_remediation_duration_seconds` | Histogram | Remediation workflow duration. |
| `coordination_engine_strategy_selection_total` | Counter | Strategy selections by deployment method. |
| `go_goroutines` | Gauge | Current number of goroutines. |
| `go_memstats_alloc_bytes` | Gauge | Allocated memory in bytes. |

### Verify Prometheus Scraping

```bash
# Check the metrics endpoint directly
kubectl exec -n self-healing-platform deploy/coordination-engine -- \
  curl -s http://localhost:9090/metrics | head -20

# Verify the ServiceMonitor exists
kubectl get servicemonitor -n self-healing-platform
```

## Persistent Storage for Incidents

By default, incident data is stored in memory and lost on pod restart. Enable persistent storage to survive restarts.

### Enable the PVC

In your values file or override:

```yaml
persistence:
  enabled: true
  storageClass: "gp3"
  size: "1Gi"
  mountPath: "/var/lib/kubeheal/incidents"
```

The chart creates a PVC and mounts it at the specified path. Set the `DATA_DIR` environment variable to the same path (the default values file already does this).

### Storage Parameters

| Variable | Default | Description |
|---|---|---|
| `DATA_DIR` | `/var/lib/kubeheal/incidents` | Directory for incident JSON files. |
| `INCIDENT_RETENTION_DAYS` | `90` | Days to retain resolved incidents (0 disables cleanup). |
| `KUBEHEAL_MAX_STORED_INCIDENTS` | `10000` | Maximum incidents on disk. Oldest resolved incidents are evicted first. |

## Alert Sink Configuration

The engine dispatches alerts when anomaly analysis detects issues above the severity threshold.

### Slack

Set `KUBEHEAL_SLACK_WEBHOOK_URL` to a Slack incoming webhook URL:

```yaml
env:
  - name: KUBEHEAL_SLACK_WEBHOOK_URL
    value: "https://hooks.slack.com/services/T00/B00/xxxx"
```

### PagerDuty

Set `KUBEHEAL_PAGERDUTY_ROUTING_KEY` to a PagerDuty Events API v2 routing key:

```yaml
env:
  - name: KUBEHEAL_PAGERDUTY_ROUTING_KEY
    value: "your-routing-key"
```

### Alertmanager

Set `KUBEHEAL_ALERTMANAGER_URL` to your Alertmanager base URL:

```yaml
env:
  - name: KUBEHEAL_ALERTMANAGER_URL
    value: "http://alertmanager.openshift-monitoring.svc:9093"
```

### Severity Threshold

Control which anomalies trigger alerts:

```yaml
env:
  - name: KUBEHEAL_ALERT_SEVERITY_THRESHOLD
    value: "warning"   # triggers for both warning and critical
```

Valid values: `info`, `warning`, `critical`. The default is `critical`.

## Health Checks and Monitoring

### Liveness and Readiness Probes

The chart configures both probes against the `/health` endpoint on port 8080:

| Probe | Initial Delay | Period | Timeout | Failure Threshold |
|---|---|---|---|---|
| Liveness | 10s | 10s | 5s | 3 |
| Readiness | 5s | 5s | 3s | 3 |

### Pod Security Context

The chart runs with OpenShift restricted-v2 SCC compatibility:

- `runAsNonRoot: true`
- `readOnlyRootFilesystem: true`
- `allowPrivilegeEscalation: false`
- `seccompProfile.type: RuntimeDefault`
- All capabilities are dropped.

The `runAsUser` and `fsGroup` fields are null by default so that OpenShift assigns UIDs from the namespace range (required for ROSA).

## Upgrade Procedure

### Helm Upgrade

1. Pull the latest chart or update the local chart directory.

2. Run `helm upgrade`:
   ```bash
   helm upgrade coordination-engine ./charts/coordination-engine \
     -n self-healing-platform \
     -f ./charts/coordination-engine/values-ocp-4.22.yaml
   ```

3. Verify the new pod is running:
   ```bash
   kubectl rollout status deployment/coordination-engine -n self-healing-platform
   ```

4. Confirm the health endpoint returns the expected version:
   ```bash
   kubectl exec -n self-healing-platform deploy/coordination-engine -- \
     curl -s http://localhost:8080/api/v1/health | jq .version
   ```

### Rolling Restart

To restart without changing configuration:

```bash
kubectl rollout restart deployment/coordination-engine -n self-healing-platform
```

### Rollback

To revert to the previous Helm release:

```bash
helm rollback coordination-engine -n self-healing-platform
```

## Troubleshooting

### CrashLoopBackOff

**Symptom:** The pod repeatedly restarts.

**Steps to resolve:**

1. Check the logs for the exit reason:
   ```bash
   kubectl logs -n self-healing-platform deploy/coordination-engine --previous
   ```
2. Common causes:
   - Missing or invalid `KUBECONFIG` (when running outside the cluster).
   - KServe is enabled but no `KSERVE_*_SERVICE` variables are set. Set at least one service name.
   - `PORT` and `METRICS_PORT` are set to the same value.
3. Validate the configuration locally before deploying:
   ```bash
   LOG_LEVEL=debug ./bin/coordination-engine 2>&1 | head -50
   ```

### RBAC Errors

**Symptom:** Log messages contain `"forbidden"` or `"cannot list resource"`.

**Steps to resolve:**

1. Verify the Role and RoleBinding exist:
   ```bash
   kubectl get role,rolebinding -n self-healing-platform | grep coordination
   ```
2. Check that `rbac.create` is `true` in your values file.
3. Test a specific permission:
   ```bash
   kubectl auth can-i list pods \
     --as=system:serviceaccount:self-healing-platform:self-healing-operator \
     -n self-healing-platform
   ```
4. If the ClusterRoleBinding is missing, verify it was created:
   ```bash
   kubectl get clusterrolebinding | grep coordination
   ```

### Image Pull Errors

**Symptom:** Pod status shows `ImagePullBackOff` or `ErrImagePull`.

**Steps to resolve:**

1. Verify the image exists:
   ```bash
   podman pull quay.io/takinosh/openshift-coordination-engine:ocp-4.22-latest
   ```
2. If the registry requires authentication, create an image pull secret:
   ```bash
   kubectl create secret docker-registry regcred \
     --docker-server=quay.io \
     --docker-username=<user> \
     --docker-password=<token> \
     -n self-healing-platform
   ```
   Then set `imagePullSecrets` in your values file.

### SCC Issues on OpenShift

**Symptom:** Pod fails to start with `"unable to validate against any security context constraint"`.

**Steps to resolve:**

1. Check the pod events:
   ```bash
   kubectl describe pod -n self-healing-platform -l app.kubernetes.io/name=coordination-engine
   ```
2. The chart defaults are compatible with `restricted-v2`. If you override `podSecurityContext.runAsUser`, verify the UID is within the namespace range:
   ```bash
   kubectl get namespace self-healing-platform -o jsonpath='{.metadata.annotations.openshift\.io/sa\.scc\.uid-range}'
   ```
3. Do not set `runAsUser` on ROSA clusters. Let OpenShift assign the UID.

### KServe Health Check Job Fails

**Symptom:** `helm install` reports the KServe health check Job failed.

**Steps to resolve:**

1. Check the Job logs:
   ```bash
   kubectl logs -n self-healing-platform job/coordination-engine-kserve-check
   ```
2. Verify InferenceServices are ready:
   ```bash
   kubectl get inferenceservices -n self-healing-platform
   ```
3. If InferenceServices are not deployed yet, install with `--set kserve.enabled=false` and re-enable after deploying the models.

### Prometheus Connection Refused

**Symptom:** Predictions return empty results. Logs show `"connection refused"` for the Prometheus URL.

**Steps to resolve:**

1. Verify the Prometheus URL is correct for your cluster:
   ```bash
   kubectl get svc -n openshift-monitoring | grep thanos
   ```
2. Test connectivity from the engine pod:
   ```bash
   kubectl exec -n self-healing-platform deploy/coordination-engine -- \
     curl -sk "https://thanos-querier.openshift-monitoring.svc:9091/api/v1/query?query=up"
   ```
3. Check that NetworkPolicies allow egress from the engine pod to the monitoring namespace.

## Related Documentation

- [User Guide](../user/coordination-engine-guide.md) for API usage and feature walkthroughs.
- [API Contract](../../API-CONTRACT.md) for complete endpoint specifications.
- [Helm Chart README](../../charts/coordination-engine/README.md) for chart-specific configuration.
- [Release Process](../../RELEASE.md) for versioning and release procedures.
- [Design Document](../../DESIGN_DOC.md) for architecture details.
