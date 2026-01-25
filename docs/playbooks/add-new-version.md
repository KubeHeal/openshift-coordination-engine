# Playbook: Adding New OpenShift Version

## Overview
This playbook guides you through adding support for a new OpenShift version (e.g., 4.21) when it's released.

## Prerequisites
- [ ] New OpenShift version released (e.g., 4.21)
- [ ] Kubernetes version mapping confirmed (e.g., 4.21 = k8s 1.34)
- [ ] client-go version available for new k8s version (e.g., v0.34.0)
- [ ] Write access to the repository

## Steps

### 1. Create New Release Branch

```bash
# Checkout and update main
git checkout main
git pull origin main

# Create new release branch
git checkout -b release-4.21

# Update Go dependencies
go get k8s.io/client-go@v0.34.0
go get k8s.io/api@v0.34.0
go get k8s.io/apimachinery@v0.34.0
go mod tidy

# Commit and push
git add go.mod go.sum
git commit -m "chore: initialize release-4.21 for OpenShift 4.21 (k8s 1.34)"
git push -u origin release-4.21
```

### 2. Update GitHub Actions Workflows

#### 2.1 Update Release Workflow

Edit `.github/workflows/release-quay.yaml`:

```yaml
on:
  push:
    branches:
      - 'release-4.19'  # Keep (drop 4.18)
      - 'release-4.20'  # Keep
      - 'release-4.21'  # Add new
  workflow_dispatch:
    inputs:
      release_branch:
        options:
          - release-4.19  # Keep
          - release-4.20  # Keep
          - release-4.21  # Add new
```

#### 2.2 Update CI Workflow

Edit `.github/workflows/ci.yaml`:

```yaml
on:
  push:
    branches: [ main, develop, 'release-4.19', 'release-4.20', 'release-4.21' ]
  pull_request:
    branches: [ main, develop, 'release-4.19', 'release-4.20', 'release-4.21' ]
```

#### 2.3 Update Integration Tests

Edit `.github/workflows/integration.yaml`:

```yaml
# Update triggers
on:
  push:
    branches: [ main, develop, 'release-4.19', 'release-4.20', 'release-4.21' ]
  pull_request:
    branches: [ main, develop, 'release-4.19', 'release-4.20', 'release-4.21' ]

# Update version matrix
strategy:
  matrix:
    ocp_version: ['4.19', '4.20', '4.21']
    include:
      - ocp_version: '4.19'
        k8s_version: 'v1.32.0'
        client_go_version: 'v0.32.0'
      - ocp_version: '4.20'
        k8s_version: 'v1.33.0'
        client_go_version: 'v0.33.0'
      - ocp_version: '4.21'
        k8s_version: 'v1.34.0'
        client_go_version: 'v0.34.0'
```

#### 2.4 Update Auto-Sync Workflow

Edit `.github/workflows/sync-release-branch.yaml`:

```yaml
- name: Determine target branch
  id: target
  run: |
    if [ "${{ github.event_name }}" == "workflow_dispatch" ]; then
      TARGET_BRANCH="${{ inputs.target_branch }}"
    else
      # Update to sync to release-4.21 (new current version)
      TARGET_BRANCH="release-4.21"
    fi
```

### 3. Create Helm Values Override

```bash
# Create new values file
cp charts/coordination-engine/values-ocp-4.20.yaml \
   charts/coordination-engine/values-ocp-4.21.yaml

# Edit the new file
# - Change image.tag to "ocp-4.21-latest"
# - Update openshiftVersion.target to "4.21"
# - Update openshiftVersion.kubernetes to "1.34"
```

### 4. Update Helm Chart Metadata

Edit `charts/coordination-engine/Chart.yaml`:

```yaml
annotations:
  openshift.io/supported-versions: "4.19,4.20,4.21"  # Update
  kubernetes.io/supported-versions: "1.32,1.33,1.34"  # Update
```

Edit `charts/coordination-engine/values.yaml`:

```yaml
image:
  tag: "ocp-4.21-latest"  # Update default

openshiftVersion:
  target: "4.21"  # Update
  kubernetes: "1.34"  # Update
```

### 5. Update Documentation

#### 5.1 Update README.md

Update the version support table:

```markdown
| OpenShift | Kubernetes | Image Tag | Branch | Status |
|-----------|-----------|-----------|--------|--------|
| 4.19 | 1.32 | `ocp-4.19-latest` | `release-4.19` | ✅ Supported |
| 4.20 | 1.33 | `ocp-4.20-latest` | `release-4.20` | ✅ Supported |
| 4.21 | 1.34 | `ocp-4.21-latest` | `release-4.21` | ✅ Supported (Current) |
```

Add deprecation notice for 4.18:

```markdown
### Deprecated Versions

| OpenShift | End of Support | Notes |
|-----------|---------------|-------|
| 4.18 | 2026-XX-XX | Dropped with 4.21 release |
```

#### 5.2 Update VERSION-STRATEGY.md

Update the version support matrix table with 4.21 and mark 4.18 as deprecated.

### 6. Mark Old Version as End of Support

Remove `release-4.18` from all workflow files (optional: keep branch as archive).

### 7. Testing

```bash
# Verify new branch builds
git checkout release-4.21
make test
make build

# Verify workflows will trigger
git log --oneline
```

### 8. Commit Workflow Changes

```bash
git checkout main
git add .github/workflows/*.yaml charts/ README.md docs/
git commit -m "chore: add support for OpenShift 4.21, deprecate 4.18"
git push origin main
```

### 9. Verify Container Builds

After pushing to `release-4.21`:
- [ ] Check GitHub Actions for successful build
- [ ] Verify images pushed to Quay.io:
  - `quay.io/takinosh/openshift-coordination-engine:ocp-4.21-latest`
  - `quay.io/takinosh/openshift-coordination-engine:ocp-4.21-<sha>`

### 10. Announce

- [ ] Update GitHub releases
- [ ] Notify users via discussions/Slack
- [ ] Update deployment documentation

## Rollback

If issues arise:

```bash
# Delete the new branch
git push origin --delete release-4.21

# Revert workflow changes
git checkout main
git revert <commit-sha>
git push origin main
```

## Checklist

- [ ] New release branch created with correct client-go version
- [ ] All workflow files updated
- [ ] Helm values override file created
- [ ] Chart.yaml updated
- [ ] README.md updated
- [ ] VERSION-STRATEGY.md updated
- [ ] Old version (4.18) deprecated in workflows
- [ ] Container images build successfully
- [ ] Documentation published
- [ ] Users notified
