# Multi-Version OpenShift Release Strategy

## Overview

This document describes the multi-version release strategy for the OpenShift Coordination Engine, supporting a rolling 3-version window across OpenShift releases.

## Version Support Matrix

| Component | OCP 4.18 | OCP 4.19 | OCP 4.20 |
|-----------|----------|----------|----------|
| **Kubernetes** | 1.31 | 1.32 | 1.33 |
| **client-go** | v0.31.3 | v0.32.0 | v0.33.0 |
| **Release Branch** | `release-4.18` | `release-4.19` | `release-4.20` |
| **Container Tag** | `ocp-4.18-*` | `ocp-4.19-*` | `ocp-4.20-*` |
| **Status** | Supported | Supported | Current |

## Branch Strategy

### Branch Roles

1. **main**: Primary development branch
   - All new features developed here
   - Auto-syncs to `release-4.20` (current version)
   - No direct container builds

2. **release-4.20** (Current Release)
   - Matches latest stable OpenShift (4.20)
   - Auto-synced from `main` via GitHub Actions
   - Triggers container builds: `ocp-4.20-latest`, `ocp-4.20-<sha>`

3. **release-4.19** (Stable)
   - Cherry-pick bug fixes from `main`
   - Manual updates for critical security patches
   - Triggers container builds: `ocp-4.19-latest`, `ocp-4.19-<sha>`

4. **release-4.18** (Legacy Support)
   - Critical bug fixes only
   - Security patches only
   - Triggers container builds: `ocp-4.18-latest`, `ocp-4.18-<sha>`

### Automated Sync Workflow

**Trigger**: Every push to `main`

**Action**: Automatically merge `main` → `release-4.20`

**Conflict Resolution**:
- If merge succeeds: Changes pushed automatically
- If conflicts occur: PR created for manual resolution

## Container Image Strategy

### Image Naming Convention

```
quay.io/takinosh/openshift-coordination-engine:<tag>
```

**Tags**:
- `ocp-4.18-latest`: Latest build for OCP 4.18 (from `release-4.18`)
- `ocp-4.18-a1b2c3d`: Specific commit SHA on `release-4.18`
- `ocp-4.19-latest`: Latest build for OCP 4.19 (from `release-4.19`)
- `ocp-4.19-d4e5f6g`: Specific commit SHA on `release-4.19`
- `ocp-4.20-latest`: Latest build for OCP 4.20 (from `release-4.20`)
- `ocp-4.20-g7h8i9j`: Specific commit SHA on `release-4.20`

### Build Triggers

**Workflow**: `.github/workflows/release-quay.yaml`

**Trigger Branches**: `release-4.18`, `release-4.19`, `release-4.20` (NOT `main`)

**Build Process**:
1. Extract OpenShift version from branch name
2. Run tests (version-specific)
3. Build multi-arch image (amd64, arm64)
4. Tag with both `-latest` and `-<sha>`
5. Push to Quay.io registry
6. Scan with Trivy
7. Generate deployment summary

## Maintenance Procedures

### Adding Support for New OpenShift Version (e.g., 4.21)

When OpenShift 4.21 is released:

1. **Update Support Matrix**
   ```bash
   # New matrix will be: 4.19, 4.20, 4.21
   # Drop support for 4.18
   ```

2. **Create New Release Branch**
   ```bash
   git checkout main
   git pull origin main
   git checkout -b release-4.21

   # Update go.mod
   go get k8s.io/client-go@v0.34.0
   go get k8s.io/api@v0.34.0
   go get k8s.io/apimachinery@v0.34.0
   go mod tidy

   # Commit and push
   git add go.mod go.sum
   git commit -m "chore: initialize release-4.21 for OpenShift 4.21 (k8s 1.34)"
   git push -u origin release-4.21
   ```

3. **Update Auto-Sync Workflow**
   - Edit `.github/workflows/sync-release-branch.yaml`
   - Change default sync target from `release-4.20` to `release-4.21`

4. **Update Release Workflow**
   - Edit `.github/workflows/release-quay.yaml`
   - Add `release-4.21` to trigger branches

5. **Update CI Workflow**
   - Edit `.github/workflows/ci.yaml`
   - Add `release-4.21` to trigger branches

6. **Create Helm Values Override**
   - Create `charts/coordination-engine/values-ocp-4.21.yaml`
   - Set `image.tag: "ocp-4.21-latest"`

7. **Update Documentation**
   - Update README.md support matrix
   - Update VERSION-STRATEGY.md (this file)
   - Add migration notes if needed

8. **Archive Old Version (4.18)**
   - Update README.md to mark 4.18 as "End of Support"
   - Remove 4.18 from CI/release workflows

### Cherry-Picking Bug Fixes to Older Versions

**Scenario**: Bug fix merged to `main` needs to go to `release-4.19` and `release-4.18`

```bash
# Identify commit SHA from main
git log main --oneline | grep "fix: resolve memory leak"
# Example: a1b2c3d fix: resolve memory leak in health checker

# Cherry-pick to release-4.19
git checkout release-4.19
git pull origin release-4.19
git cherry-pick a1b2c3d
# Resolve conflicts if any
git push origin release-4.19

# Cherry-pick to release-4.18
git checkout release-4.18
git pull origin release-4.18
git cherry-pick a1b2c3d
# Resolve conflicts if any
git push origin release-4.18
```

**Result**: Both branches will trigger new container builds automatically.

## Deployment Guidelines

### Production Deployment

**Rule**: Always match container image version to OpenShift cluster version

```bash
# Check cluster version first
oc version
# Output: Server Version: 4.19.5

# Deploy matching image
helm install coordination-engine ./charts/coordination-engine \
  --set image.tag=ocp-4.19-latest \
  --namespace self-healing-platform
```

### Upgrading Deployments

**Scenario**: Upgrading from OCP 4.19 → 4.20

```bash
# Step 1: Upgrade OpenShift cluster (separate process)
# ...

# Step 2: Upgrade coordination-engine deployment
helm upgrade coordination-engine ./charts/coordination-engine \
  --set image.tag=ocp-4.20-latest \
  --reuse-values \
  --namespace self-healing-platform

# Step 3: Verify health
kubectl rollout status deployment/coordination-engine -n self-healing-platform
curl http://coordination-engine:8080/api/v1/health
```

### Rollback Procedure

**Scenario**: New image version causes issues

```bash
# Rollback Helm deployment
helm rollback coordination-engine -n self-healing-platform

# Or explicitly set previous version
helm upgrade coordination-engine ./charts/coordination-engine \
  --set image.tag=ocp-4.20-a1b2c3d \
  --namespace self-healing-platform
```

## Troubleshooting

### Version Mismatch Errors

**Symptom**: API errors like "resource not found" or "unsupported API version"

**Diagnosis**:
```bash
# Check deployed image
kubectl get deployment coordination-engine -n self-healing-platform -o jsonpath='{.spec.template.spec.containers[0].image}'

# Check cluster version
oc version
```

**Solution**: Deploy correct image version

### Sync Workflow Failures

**Symptom**: Auto-sync PR created for conflicts

**Action**:
1. Review the auto-generated PR
2. Checkout the sync branch locally
3. Resolve conflicts manually
4. Test changes
5. Merge PR

### Build Failures on Release Branch

**Symptom**: Container build fails on `release-4.X` branch

**Common issues**:
- go.mod dependency conflicts
- Test failures with version-specific APIs

**Solution**:
1. Fix on `main` first (if applicable)
2. Cherry-pick fix to release branch
3. Or apply version-specific fix directly to release branch

## References

- [Kubernetes API Deprecation Guide](https://kubernetes.io/docs/reference/using-api/deprecation-guide/)
- [client-go Compatibility Matrix](https://github.com/kubernetes/client-go#compatibility-matrix)
- [OpenShift Release Schedule](https://access.redhat.com/support/policy/updates/openshift)
- [OpenShift Version Mapping](https://gist.github.com/jeyaramashok/ebbd25f36338de4422fd584fea841c08)
