# Playbook: Cherry-Picking Critical Fixes

## Overview
This playbook guides you through cherry-picking critical bug fixes or security patches from `main` to older release branches.

## When to Cherry-Pick

✅ **DO cherry-pick**:
- Critical security vulnerabilities
- Data corruption bugs
- Production outages
- Critical performance issues
- Essential bug fixes affecting multiple versions

❌ **DO NOT cherry-pick**:
- New features
- Refactoring
- Non-critical improvements
- Documentation-only changes
- Breaking API changes

## Prerequisites
- [ ] Fix has been merged to `main` and tested
- [ ] Issue affects the target release branches
- [ ] Fix is compatible with older Kubernetes API versions

## Steps

### 1. Identify the Commit

```bash
# Find the commit SHA from main
git checkout main
git pull origin main
git log --oneline | grep "fix:"

# Example output:
# a1b2c3d fix: resolve memory leak in health checker
# b2c3d4e fix: correct ArgoCD sync timeout
```

Note the commit SHA (e.g., `a1b2c3d`).

### 2. Determine Target Branches

Check which branches need the fix:

```bash
# Check if the bug exists in release-4.19
git checkout release-4.19
git log --oneline | grep "fix: resolve memory leak"  # Should not exist

# Check if the bug exists in release-4.18
git checkout release-4.18
git log --oneline | grep "fix: resolve memory leak"  # Should not exist
```

### 3. Cherry-Pick to release-4.19

```bash
# Checkout the target branch
git checkout release-4.19
git pull origin release-4.19

# Cherry-pick the commit
git cherry-pick a1b2c3d

# If successful (no conflicts):
git push origin release-4.19

# If conflicts occur:
# 1. Resolve conflicts manually
git status  # See conflicted files
# Edit files to resolve conflicts
git add <resolved-files>
git cherry-pick --continue

# 2. Test the changes
make test
make build

# 3. Push
git push origin release-4.19
```

### 4. Cherry-Pick to release-4.18

```bash
# Checkout the target branch
git checkout release-4.18
git pull origin release-4.18

# Cherry-pick the commit
git cherry-pick a1b2c3d

# Resolve conflicts if needed (same process as above)

# Test the changes
make test
make build

# Push
git push origin release-4.18
```

### 5. Verify Container Builds

After pushing to each branch:

- [ ] Check GitHub Actions for successful builds
- [ ] Verify new container images pushed to Quay.io:
  - `ocp-4.19-latest` (new SHA)
  - `ocp-4.18-latest` (new SHA)

```bash
# Check latest images
oc image info quay.io/takinosh/openshift-coordination-engine:ocp-4.19-latest
oc image info quay.io/takinosh/openshift-coordination-engine:ocp-4.18-latest
```

### 6. Update Tracking

Document the cherry-pick in the PR or issue:

```markdown
## Cherry-Pick Status

- [x] `main` (original fix)
- [x] `release-4.19` (cherry-picked: <commit-sha>)
- [x] `release-4.18` (cherry-picked: <commit-sha>)

**Container Images**:
- `ocp-4.19-<new-sha>`
- `ocp-4.18-<new-sha>`
```

### 7. Notify Users (if security patch)

For security fixes:

- [ ] Update GitHub Security Advisory
- [ ] Post announcement in GitHub Discussions
- [ ] Notify via Slack/email
- [ ] Recommend users update deployments

Example message:

```markdown
## Security Update: CVE-2026-XXXXX

A critical security vulnerability has been patched in all supported versions.

**Affected Versions**: All versions prior to:
- 4.19.x-<new-sha>
- 4.18.x-<new-sha>

**Action Required**: Update your deployments immediately.

```bash
# For OpenShift 4.19
helm upgrade coordination-engine ./charts/coordination-engine \
  --set image.tag=ocp-4.19-<new-sha> \
  --namespace self-healing-platform

# For OpenShift 4.18
helm upgrade coordination-engine ./charts/coordination-engine \
  --set image.tag=ocp-4.18-<new-sha> \
  --namespace self-healing-platform
```
```

## Handling Conflicts

### Common Conflict Scenarios

**Scenario 1: API Version Differences**

```bash
# Conflict in Kubernetes API usage
# File: internal/client/pods.go

# Resolution: Use version-appropriate API
# For release-4.18 (k8s 1.31), may need different API calls than main (k8s 1.33)
```

**Scenario 2: Dependency Version Differences**

```bash
# Conflict in go.mod or imports

# Resolution: Adjust import paths to match the branch's dependencies
# Check release branch's go.mod for correct versions
```

### Aborting Cherry-Pick

If the fix is not compatible:

```bash
# Abort the cherry-pick
git cherry-pick --abort

# Document why cherry-pick was skipped
echo "Cherry-pick skipped for release-4.18: incompatible with k8s 1.31 API" >> CHANGELOG.md
```

## Testing Checklist

Before pushing cherry-picked changes:

- [ ] Unit tests pass: `make test`
- [ ] Build succeeds: `make build`
- [ ] Integration tests pass (if applicable): `make test-integration`
- [ ] Manual testing in target OpenShift version
- [ ] No new deprecation warnings

## Rollback

If cherry-pick causes issues:

```bash
# Find the cherry-pick commit
git log --oneline | head -5

# Revert the cherry-pick
git revert <cherry-pick-commit-sha>
git push origin release-4.19
```

## Checklist

- [ ] Commit identified from main
- [ ] Target branches determined
- [ ] Cherry-pick applied to release-4.19
- [ ] Cherry-pick applied to release-4.18
- [ ] Conflicts resolved (if any)
- [ ] Tests passed on all branches
- [ ] Container images built successfully
- [ ] Tracking updated in PR/issue
- [ ] Users notified (if critical)

## Tips

1. **Test locally first**: Cherry-pick to a local branch and test before pushing
2. **One commit at a time**: Don't cherry-pick multiple commits at once
3. **Maintain commit message**: Keep original commit message for traceability
4. **Document exceptions**: If a branch is skipped, document why
