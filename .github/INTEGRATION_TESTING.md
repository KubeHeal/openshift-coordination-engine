# Integration Testing with OpenShift

This document describes how to set up integration testing against a real OpenShift cluster using GitHub Actions.

## GitHub Secrets Setup

To enable OpenShift cluster integration tests, add the following secrets to your GitHub repository:

### Required Secrets

1. **`OPENSHIFT_TOKEN`** - OpenShift authentication token
   - Example: `sha256~RCuV8aknRBgtzXH4dMoqSNe8twRHRvoirBEwZDcmUfM`
   - Get from: `oc whoami -t` after logging in

2. **`OPENSHIFT_SERVER`** - OpenShift API server URL
   - Example: `https://api.cluster-k6x6k.k6x6k.sandbox3465.opentlc.com:6443`
   - Get from: `oc whoami --show-server`

### Optional Secrets

3. **`ML_SERVICE_URL`** - ML service endpoint URL (optional)
   - Example: `http://aiops-ml-service.aiops.svc.cluster.local:8080`
   - Defaults to: `http://localhost:8080` if not set

## How to Add Secrets to GitHub

1. Go to your repository on GitHub
2. Click **Settings** → **Secrets and variables** → **Actions**
3. Click **New repository secret**
4. Add each secret:
   - Name: `OPENSHIFT_TOKEN`
   - Value: Your token from `oc whoami -t`
   - Click **Add secret**
5. Repeat for `OPENSHIFT_SERVER` and optionally `ML_SERVICE_URL`

## Getting OpenShift Credentials

### From Command Line

```bash
# Login to your OpenShift cluster
oc login --token=<your-token> --server=<your-server>

# Get your token
oc whoami -t

# Get your server URL
oc whoami --show-server
```

### From Web Console

1. Log in to OpenShift web console
2. Click your username in top right → **Copy login command**
3. Click **Display Token**
4. Copy the token and server URL

## Workflow Behavior

### When Secrets Are Available

The `openshift-integration` job will:
1. ✅ Install OpenShift CLI (`oc`)
2. ✅ Login to your cluster using the token
3. ✅ Run integration tests (`make test-integration`)
4. ✅ Run e2e tests (`make test-e2e`)
5. ✅ Clean up test namespaces
6. ✅ Upload test results as artifacts

### When Secrets Are NOT Available

The workflow will:
- Skip OpenShift integration tests gracefully
- Continue running other tests (kind cluster tests)
- Not fail the build

### Security Notes

- ⚠️ Secrets are **NOT** available on pull requests from forks (GitHub security feature)
- ✅ OpenShift integration only runs on:
  - Push to `main` or `develop` branches
  - Scheduled runs (daily at 2 AM UTC)
- ✅ Uses `--insecure-skip-tls-verify=true` for sandbox clusters with self-signed certs
- ⚠️ **Never** commit tokens or credentials to the repository

## Local Testing

To test integration tests locally against your OpenShift cluster:

```bash
# Login to cluster
oc login --token=<your-token> --server=<your-server>

# Run integration tests
export KUBECONFIG=~/.kube/config
make test-integration

# Run e2e tests
make test-e2e
```

## Troubleshooting

### "oc: command not found"

Install OpenShift CLI:
```bash
curl -LO https://mirror.openshift.com/pub/openshift-v4/clients/ocp/stable/openshift-client-linux.tar.gz
tar -xzf openshift-client-linux.tar.gz
sudo mv oc /usr/local/bin/
```

### "error: You must be logged in to the server"

Check your token hasn't expired:
```bash
oc login --token=<your-token> --server=<your-server>
```

### Tests skip with "KUBECONFIG not set"

Export KUBECONFIG:
```bash
export KUBECONFIG=$HOME/.kube/config
```

### Certificate errors

For sandbox clusters with self-signed certificates:
```bash
oc login --token=<token> --server=<server> --insecure-skip-tls-verify=true
```

## Cluster Permissions Required

The service account running tests needs:

- ✅ Create/read/delete namespaces
- ✅ Create/read/delete deployments, pods, services
- ✅ Read cluster operators (for OpenShift-specific tests)
- ✅ Create/read ArgoCD applications (for ArgoCD tests)
- ✅ Execute Helm operations (for Helm tests)

See `docs/adrs/ADR-033-rbac-permissions.md` for full RBAC requirements.
