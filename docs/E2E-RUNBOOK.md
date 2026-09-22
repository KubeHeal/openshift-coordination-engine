# E2E Testing Runbook — Coordination Engine

This runbook documents how to run end-to-end tests for the coordination-engine,
both automated (CI) and manual (live OpenShift cluster).

## Testing Tiers

| Tier | Environment | Trigger | What it validates |
|------|------------|---------|-------------------|
| **Tier 1** | Kind cluster | Every push/PR (CI gate) | Helm deploy, health endpoint, RBAC, metrics |
| **Tier 2** | OpenShift cluster | Manual, `release-*` pushes, `e2e-test` label | Full OCP deployment, version-specific values |

---

## Tier 1: Kind Cluster (Automated)

Tier 1 runs automatically via `.github/workflows/e2e-kind.yaml` on every push and PR.

### Run locally

```bash
# Build the E2E test image
docker build -t coordination-engine:e2e-test .

# Create a Kind cluster
kind create cluster --name ce-e2e

# Load the image into Kind
kind load docker-image coordination-engine:e2e-test --name ce-e2e

# Run E2E tests
E2E_IMAGE=coordination-engine:e2e-test make test-e2e

# Or use the convenience target
make test-e2e-ci    # builds image + runs tests (assumes Kind cluster exists)

# Cleanup
kind delete cluster --name ce-e2e
```

### What Tier 1 tests verify

1. **TestHelmDeployAndHealthCheck**: Helm install -> Deployment ready -> `GET /health` returns 200
2. **TestHelmDeployRBACResources**: ServiceAccount, Role, RoleBinding exist
3. **TestHelmDeployMetricsPort**: Prometheus metrics on port 9090

---

## Tier 2: OpenShift Cluster (Manual / Release)

Tier 2 runs on a live OpenShift cluster via `.github/workflows/e2e-openshift.yaml`.

### Quick Start

```bash
# Deploy to an existing cluster with one command
./scripts/setup-e2e.sh --deploy
```

### Prerequisites

- `oc` CLI installed and logged into an OpenShift cluster
- `helm` CLI installed (v3+)
- Container image available on Quay.io (or build locally)

### Step-by-step Manual Deployment

#### 1. Verify cluster access

```bash
oc whoami
oc version
oc cluster-info
```

#### 2. Deploy via Helm

```bash
# For OCP 4.22
helm install coordination-engine ./charts/coordination-engine \
  --namespace self-healing-platform \
  --create-namespace \
  --values ./charts/coordination-engine/values-ocp-4.22.yaml \
  --set image.pullPolicy=Always \
  --wait --timeout 5m

# For OCP 4.21
helm install coordination-engine ./charts/coordination-engine \
  --namespace self-healing-platform \
  --create-namespace \
  --values ./charts/coordination-engine/values-ocp-4.21.yaml \
  --wait --timeout 5m
```

#### 3. Verify deployment

```bash
# Check pod status
oc get pods -n self-healing-platform

# Wait for rollout
oc rollout status deployment/coordination-engine-coordination-engine \
  -n self-healing-platform --timeout=3m
```

#### 4. Verify health endpoint

```bash
# Port-forward and test
oc port-forward svc/coordination-engine-coordination-engine 8080:8080 \
  -n self-healing-platform &
sleep 2

curl -s http://localhost:8080/health | jq .
# Expected: {"status":"ok"}

kill %1  # stop port-forward
```

#### 5. Verify RBAC

```bash
oc get serviceaccount -n self-healing-platform | grep coordination-engine
oc get role -n self-healing-platform | grep coordination-engine
oc get rolebinding -n self-healing-platform | grep coordination-engine
oc get clusterrolebinding | grep coordination-engine
```

#### 6. Verify metrics

```bash
oc port-forward svc/coordination-engine-coordination-engine 9090:9090 \
  -n self-healing-platform &
sleep 2

curl -s http://localhost:9090/metrics | head -20
# Expected: Prometheus metrics starting with go_, process_, etc.

kill %1
```

#### 7. Verify ServiceMonitor (if monitoring is enabled)

```bash
oc get servicemonitor -n self-healing-platform
```

### Full-Stack Test (with kubeheal-operator)

To test the full deployment path through the kubeheal-operator:

```bash
# Clone the operator repo
git clone https://github.com/KubeHeal/kubeheal-operator.git
cd kubeheal-operator

# Deploy via the operator's Helm chart
helm install kubeheal ./helm-charts/self-healing-platform \
  --namespace self-healing-platform \
  --create-namespace \
  --set global.git.repoURL=https://github.com/KubeHeal/kubeheal-operator.git \
  --wait --timeout 10m

# Verify the coordination-engine pod comes up
oc get pods -n self-healing-platform | grep coordination-engine
```

---

## Audit Cluster Readiness

The setup script includes an audit mode that checks all prerequisites:

```bash
./scripts/setup-e2e.sh --audit
```

This checks:
1. Cluster connection
2. Helm chart availability
3. Target namespace
4. Deployment status
5. Health endpoint
6. Container image version

---

## CI Setup: GitHub Secrets

The `e2e-openshift.yaml` workflow requires these repository secrets:

| Secret | Required | Description |
|--------|----------|-------------|
| `OPENSHIFT_SERVER` | Yes | Cluster API URL (e.g., `https://api.cluster.example.com:6443`) |
| `OPENSHIFT_TOKEN` | Yes | Login token (from `oc whoami -t`) |
| `QUAY_USERNAME` | No | Quay.io username (for image push) |
| `QUAY_PASSWORD` | No | Quay.io password/token (for image push) |

### Set secrets automatically

```bash
# Login to the cluster first, then:
./scripts/setup-e2e.sh --set-secrets
```

### Set secrets manually

```bash
gh secret set OPENSHIFT_SERVER --repo KubeHeal/openshift-coordination-engine \
  --body "$(oc whoami --show-server)"

gh secret set OPENSHIFT_TOKEN --repo KubeHeal/openshift-coordination-engine \
  --body "$(oc whoami -t)"
```

### Trigger the workflow

```bash
# Manual trigger
gh workflow run e2e-openshift.yaml \
  --repo KubeHeal/openshift-coordination-engine

# With specific OCP version
gh workflow run e2e-openshift.yaml \
  --repo KubeHeal/openshift-coordination-engine \
  -f ocp_values_file=values-ocp-4.21.yaml
```

---

## Cleanup

```bash
# Using the setup script
./scripts/setup-e2e.sh --destroy

# Or manually
helm uninstall coordination-engine -n self-healing-platform
oc delete namespace self-healing-platform
```

---

## Troubleshooting

### Pod stuck in ImagePullBackOff

```bash
oc describe pod -n self-healing-platform -l app.kubernetes.io/name=coordination-engine
# Check: Is the image tag correct? Is Quay.io accessible?
```

### Health endpoint returns error

```bash
oc logs -n self-healing-platform -l app.kubernetes.io/name=coordination-engine --tail=50
# Check: Are required env vars set? Is KServe integration disabled for standalone testing?
```

### RBAC errors in logs

```bash
oc get role,rolebinding,clusterrolebinding -n self-healing-platform | grep coordination
# Verify the ServiceAccount has the correct permissions
```

### Kind cluster: image not found

```bash
# Ensure the image was loaded into Kind
kind load docker-image coordination-engine:e2e-test --name ce-e2e
ap list --name ce-e2e
```

---

## Related

- [RELEASE-CHECKLIST.md](../RELEASE-CHECKLIST.md) — Cross-repo release process (Phase 2 covers operator E2E)
- [VERSION-STRATEGY.md](VERSION-STRATEGY.md) — Multi-version OCP support strategy
- [jupyter-notebook-validator-operator E2E](https://github.com/tosin2013/jupyter-notebook-validator-operator/blob/main/.github/workflows/e2e-openshift.yaml) — Reference patterns for OpenShift E2E
