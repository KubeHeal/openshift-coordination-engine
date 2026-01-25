# ArgoCD Integration Guide

## Overview

The Go Coordination Engine now supports **ArgoCD-managed applications**, automatically detecting GitOps deployments and triggering ArgoCD sync operations instead of bypassing GitOps workflows.

## How It Works

### Detection Flow
1. **Request arrives** via POST /api/v1/remediation/trigger
2. **Detector inspects** deployment metadata for ArgoCD annotations
3. **Strategy selector** routes to appropriate remediator:
   - `argocd.argoproj.io/tracking-id` present → **ArgoCDRemediator**
   - Helm annotations → **HelmRemediator** (future)
   - Manual deployment → **ManualRemediator** (fallback)
4. **ArgoCDRemediator** triggers sync via ArgoCD API
5. **Waits for completion** and health check

### Architecture

```
┌─────────────────┐
│  MCP Server     │
└────────┬────────┘
         │ POST /api/v1/remediation/trigger
         ▼
┌─────────────────┐
│  Orchestrator   │
│                 │
│  ┌───────────┐  │
│  │ Detector  │  │
│  └─────┬─────┘  │
│        │        │
│  ┌─────▼──────────────┐
│  │ Strategy Selector  │
│  │                    │
│  │ ┌──────────────┐   │
│  │ │  ArgoCD      │   │
│  │ │  Remediator  │   │
│  │ └──────┬───────┘   │
│  │        │           │
│  │ ┌──────▼───────┐   │
│  │ │  Manual      │   │
│  │ │  Remediator  │   │
│  │ └──────────────┘   │
│  └────────────────────┘
└─────────────────┘
         │
         ▼
┌─────────────────┐
│  ArgoCD API     │
└─────────────────┘
```

## Configuration

### Environment Variables

```bash
# ArgoCD API URL (required for ArgoCD integration)
export ARGOCD_API_URL=https://argocd-server.openshift-gitops.svc.cluster.local

# ArgoCD authentication token (required)
export ARGOCD_TOKEN=<your-argocd-token>

# Kubernetes configuration
export KUBECONFIG=~/.kube/config

# ML Service URL
export ML_SERVICE_URL=http://aiops-ml-service:8080
```

### Getting ArgoCD Token

**Method 1: Service Account Token (Recommended)**
```bash
# Create ServiceAccount for coordination engine
kubectl create sa coordination-engine -n openshift-gitops

# Create RBAC for ArgoCD access
cat <<EOF | kubectl apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind:ClusterRole
metadata:
  name: argocd-sync-trigger
rules:
- apiGroups: ["argoproj.io"]
  resources: ["applications"]
  verbs: ["get", "list", "patch", "update"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: coordination-engine-argocd
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: argocd-sync-trigger
subjects:
- kind: ServiceAccount
  name: coordination-engine
  namespace: openshift-gitops
EOF

# Get token
export ARGOCD_TOKEN=$(kubectl create token coordination-engine -n openshift-gitops --duration=8760h)
```

**Method 2: ArgoCD CLI (Development)**
```bash
# Login to ArgoCD
argocd login argocd-server.openshift-gitops.svc.cluster.local

# Generate token
export ARGOCD_TOKEN=$(argocd account generate-token)
```

## Testing

### 1. Deploy ArgoCD-Managed Application

```bash
# Create test application via ArgoCD
cat <<EOF | kubectl apply -f -
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: test-app
  namespace: openshift-gitops
spec:
  project: default
  source:
    repoURL: https://github.com/your-org/your-repo
    targetRevision: HEAD
    path: k8s/overlays/dev
  destination:
    server: https://kubernetes.default.svc
    namespace: default
  syncPolicy:
    automated:
      prune: false
      selfHeal: true
EOF

# Wait for sync
kubectl wait --for=condition=Synced app/test-app -n openshift-gitops --timeout=300s
```

### 2. Verify Detection

```bash
# Check deployment has ArgoCD annotation
kubectl get deployment -n default -o jsonpath='{.items[0].metadata.annotations}'

# Should see:
# {"argocd.argoproj.io/tracking-id":"test-app:Deployment:default/test-app"}
```

### 3. Trigger Remediation

```bash
# Start coordination engine
export ARGOCD_API_URL=https://argocd-server.openshift-gitops.svc.cluster.local
export ARGOCD_TOKEN=<token>
./bin/coordination-engine

# Trigger remediation
curl -X POST http://localhost:8080/api/v1/remediation/trigger \
  -H "Content-Type: application/json" \
  -d '{
    "incident_id": "inc-argocd-001",
    "namespace": "default",
    "resource": {
      "kind": "Deployment",
      "name": "test-app"
    },
    "issue": {
      "type": "pod_crash_loop",
      "description": "Pods crashing in test-app",
      "severity": "high"
    }
  }' | jq

# Response should show:
# {
#   "workflow_id": "wf-xxxxx",
#   "status": "in_progress",
#   "deployment_method": "argocd",  <-- ArgoCD detected!
#   "estimated_duration": "5m"
# }
```

### 4. Verify ArgoCD Sync Triggered

```bash
# Check workflow status
curl http://localhost:8080/api/v1/workflows/wf-xxxxx | jq

# Check ArgoCD application status
kubectl get application test-app -n openshift-gitops -o jsonpath='{.status.sync.status}'
# Should show: Synced

# Check server logs
journalctl -u coordination-engine -f | grep argocd
# Should see:
# "Starting ArgoCD remediation"
# "Found ArgoCD application"
# "Triggering ArgoCD sync"
# "Waiting for ArgoCD sync completion"
# "ArgoCD remediation completed successfully"
```

## Expected Behavior

### For ArgoCD-Managed Apps

1. ✅ **Detection**: ArgoCD tracking annotation detected (confidence: 0.95)
2. ✅ **Routing**: Routed to ArgoCDRemediator
3. ✅ **Sync**: ArgoCD sync triggered via API
4. ✅ **Wait**: Waits for sync completion (up to 5 minutes)
5. ✅ **Verify**: Checks app is Synced + Healthy
6. ✅ **Complete**: Workflow marked as completed

### For Manual Apps

1. ✅ **Detection**: No ArgoCD annotation (confidence: 0.60)
2. ✅ **Routing**: Falls back to ManualRemediator
3. ✅ **Direct**: Pod restart via Kubernetes API
4. ✅ **Complete**: Workflow marked as completed

## API Endpoints Used

### ArgoCD API Calls

**GET /api/v1/applications/{name}**
- Purpose: Get application status
- Used: Before and after sync

**POST /api/v1/applications/{name}/sync**
- Purpose: Trigger application sync
- Payload:
  ```json
  {
    "prune": false,
    "dryRun": false
  }
  ```

**GET /api/v1/applications**
- Purpose: Find application by resource
- Used: When ArgoCD app name unknown

## Troubleshooting

### ArgoCD Remediator Not Selected

**Check detection:**
```bash
kubectl get deployment <name> -n <namespace> -o yaml | grep argocd
```

**Fix:** Add ArgoCD annotation manually:
```bash
kubectl annotate deployment <name> -n <namespace> \
  argocd.argoproj.io/tracking-id="<app-name>:Deployment:<namespace>/<name>"
```

### Sync Fails with 401 Unauthorized

**Check token:**
```bash
# Verify token works
curl -H "Authorization: Bearer $ARGOCD_TOKEN" \
  $ARGOCD_API_URL/api/version
```

**Fix:** Regenerate token with correct permissions

### Sync Timeout

**Check ArgoCD application:**
```bash
kubectl get application <app-name> -n openshift-gitops -o yaml
```

**Increase timeout:**
Set `ARGOCD_SYNC_TIMEOUT=10m` environment variable

### Application Not Found

**Check namespace:**
ArgoCD applications are typically in `openshift-gitops` or `argocd` namespace

**List all applications:**
```bash
kubectl get applications -A
```

## Production Deployment

### Helm Chart Values

```yaml
# values.yaml
env:
  - name: ARGOCD_API_URL
    value: "https://argocd-server.openshift-gitops.svc.cluster.local"
  - name: ARGOCD_TOKEN
    valueFrom:
      secretKeyRef:
        name: argocd-token
        key: token

serviceAccount:
  create: true
  annotations:
    argocd.argoproj.io/sync-wave: "0"

rbac:
  create: true
  rules:
    - apiGroups: ["argoproj.io"]
      resources: ["applications"]
      verbs: ["get", "list", "patch", "update"]
```

### Secret Management

```bash
# Create secret for ArgoCD token
kubectl create secret generic argocd-token \
  --from-literal=token=$ARGOCD_TOKEN \
  -n self-healing-platform
```

## Metrics

ArgoCD remediation exposes these metrics:

```
# Remediation attempts by method
coordination_engine_remediation_total{method="argocd",issue_type="pod_crash_loop",status="success"}

# Remediation duration
coordination_engine_remediation_duration_seconds{method="argocd",issue_type="pod_crash_loop"}

# Strategy selection
coordination_engine_strategy_selection_total{deployment_method="argocd",remediator="argocd"}
```

## Next Steps

1. ✅ **Deploy to test cluster** with real ArgoCD
2. ⚠️ **Test multiple scenarios** (sync, refresh, rollback)
3. ⚠️ **Add Helm remediator** for Helm-managed apps
4. ⚠️ **Add Operator remediator** for operator-managed apps
5. ⚠️ **Multi-layer coordination** (infra + platform + app)

## References

- [ArgoCD API Documentation](https://argo-cd.readthedocs.io/en/stable/developer-guide/api-docs/)
- [ADR-004: ArgoCD/MCO Integration](docs/adrs/004-argocd-mco-integration.md)
- [ADR-005: Remediation Strategies](docs/adrs/005-remediation-strategies-implementation.md)
- [API Contract](API-CONTRACT.md)
