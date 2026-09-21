# Cross-Repository Release Checklist

This checklist documents the end-to-end release sequence across the KubeHeal
ecosystem. Follow every step in order. Each checkbox is a gate — do not proceed
to the next step until the current one is verified.

> **Scope**: This checklist covers the coordination between
> [openshift-coordination-engine](https://github.com/KubeHeal/openshift-coordination-engine)
> and [kubeheal-operator](https://github.com/KubeHeal/kubeheal-operator).
> For single-repo release details (tagging, changelog, DCO), see [RELEASE.md](./RELEASE.md).

---

## Prerequisites

Before starting, confirm:

- [ ] You have push access to both `KubeHeal/openshift-coordination-engine` and `KubeHeal/kubeheal-operator`
- [ ] You have the `gh` CLI authenticated (`gh auth status`)
- [ ] You have `helm`, `make`, and `go` installed locally
- [ ] Quay.io push credentials are configured in the coordination-engine repo secrets (`QUAY_USERNAME`, `QUAY_PASSWORD`)

---

## Phase 1 — Coordination Engine Release

**Milestone**: [v1.2.0](https://github.com/KubeHeal/openshift-coordination-engine/milestone/6)

### 1.1 Pre-flight checks

- [ ] All issues in the target milestone are closed or deferred to the next milestone
- [ ] CI is green on `main` — Lint, Test, Build, codecov/patch all pass
- [ ] Security scan is clean — no open High/Critical Trivy alerts on `main`
- [ ] `go.mod` Go version matches `ci.yaml` `GO_VERSION`
- [ ] Integration tests pass locally: `make test-integration`
- [ ] CHANGELOG.md `[Unreleased]` section is complete

### 1.2 Prepare the release commit

```bash
# Update CHANGELOG.md: move [Unreleased] to [1.2.0] - YYYY-MM-DD
# Bump any version constants
grep -r "AppVersion\|Version = " cmd/ pkg/

git add CHANGELOG.md
git commit -s -m "release: prepare v1.2.0 — update CHANGELOG"
git push origin main
```

### 1.3 Create release branch (if new OCP version)

```bash
# Example: creating release-4.22 for OCP 4.22 support
git checkout main && git pull
git checkout -b release-4.22

# Bump k8s.io dependencies to match the OCP version
go get k8s.io/client-go@v0.35.0
go get k8s.io/api@v0.35.0
go get k8s.io/apimachinery@v0.35.0
go mod tidy

git add go.mod go.sum
git commit -s -m "chore: initialize release-4.22 for OCP 4.22 (k8s 1.35)"
git push -u origin release-4.22
```

- [ ] `release-4.22` branch exists on GitHub
- [ ] `release-quay.yaml` workflow triggered automatically on push

### 1.4 Verify image publish

Wait for the `release-quay.yaml` workflow to complete, then verify:

```bash
# Check workflow status
gh run list --repo KubeHeal/openshift-coordination-engine \
  --workflow release-quay.yaml --limit 3

# Verify images exist on Quay
podman pull quay.io/takinosh/openshift-coordination-engine:ocp-4.22-latest
podman inspect quay.io/takinosh/openshift-coordination-engine:ocp-4.22-latest \
  | jq '.[0].Created'
```

- [ ] Image `quay.io/takinosh/openshift-coordination-engine:ocp-4.22-latest` exists
- [ ] Image `quay.io/takinosh/openshift-coordination-engine:ocp-4.22-<sha>` exists
- [ ] Trivy scan step passed (no HIGH/CRITICAL in SARIF upload)

### 1.5 Tag and create GitHub Release

```bash
VERSION=v1.2.0
git checkout main
git tag -a "$VERSION" -m "Release $VERSION"
git push origin "$VERSION"

gh release create "$VERSION" \
  --repo KubeHeal/openshift-coordination-engine \
  --title "openshift-coordination-engine $VERSION" \
  --notes-file <(sed -n "/^## \[$VERSION\]/,/^## \[/p" CHANGELOG.md | head -n -1) \
  --draft
```

- [ ] Tag `v1.2.0` pushed
- [ ] GitHub Release draft created — review and publish
- [ ] Milestone `v1.2.0` closed

---

## Phase 2 — Kubeheal Operator Update

**Milestone**: [v0.1.0](https://github.com/KubeHeal/kubeheal-operator/milestone/1)
**Blocked by**: Phase 1 complete (published image on Quay)

### 2.1 Update operator Helm chart

In the `kubeheal-operator` repo, update the coordination-engine image reference:

```bash
cd kubeheal-operator

# Update the Helm chart values
# Set image.repository and image.tag to the newly published image
vim charts/kubeheal-operator/values.yaml
# image:
#   repository: quay.io/takinosh/openshift-coordination-engine
#   tag: "ocp-4.22-latest"    # or pin to SHA tag for reproducibility

# Update Chart.yaml appVersion
vim charts/kubeheal-operator/Chart.yaml
# appVersion: "1.2.0"

git add -A
git commit -s -m "chore: update coordination-engine image to v1.2.0 (ocp-4.22)"
git push origin main
```

- [ ] Helm chart references the new coordination-engine image tag
- [ ] `Chart.yaml` `appVersion` updated to `1.2.0`
- [ ] CI passes on `kubeheal-operator` main branch

### 2.2 Build and push operator images

```bash
# Build operator image
make docker-build docker-push IMG=quay.io/takinosh/kubeheal-operator:v0.1.0

# Build OLM bundle image
make bundle-build bundle-push \
  BUNDLE_IMG=quay.io/takinosh/kubeheal-operator-bundle:v0.1.0
```

- [ ] Operator image pushed: `quay.io/takinosh/kubeheal-operator:v0.1.0`
- [ ] Bundle image pushed: `quay.io/takinosh/kubeheal-operator-bundle:v0.1.0`

### 2.3 Smoke test the operator

```bash
# Deploy on a Kind cluster (Tier 1)
kind create cluster --name kubeheal-test
make deploy IMG=quay.io/takinosh/kubeheal-operator:v0.1.0

# Verify coordination-engine pod is running
kubectl get pods -n kubeheal-system
kubectl wait --for=condition=ready pod -l app=coordination-engine \
  -n kubeheal-system --timeout=120s

# Check health endpoint
kubectl port-forward svc/coordination-engine 8080:8080 -n kubeheal-system &
curl -sf http://localhost:8080/health && echo "PASS" || echo "FAIL"

# Clean up
kind delete cluster --name kubeheal-test
```

- [ ] Operator deploys coordination-engine successfully
- [ ] Health endpoint returns 200
- [ ] No crash loops or RBAC errors in pod logs

### 2.4 Tag operator release

```bash
cd kubeheal-operator
git tag -a v0.1.0 -m "Release v0.1.0"
git push origin v0.1.0

gh release create v0.1.0 \
  --repo KubeHeal/kubeheal-operator \
  --title "kubeheal-operator v0.1.0" \
  --generate-notes \
  --draft
```

- [ ] Tag `v0.1.0` pushed
- [ ] GitHub Release draft created — review and publish

---

## Phase 3 — Distribution (OperatorHub)

**Blocked by**: Phase 2 complete (operator + bundle images published)

### 3.1 Submit to community-operators-prod

```bash
# Fork k8s-operatorhub/community-operators-prod (if not already)
git clone https://github.com/<your-fork>/community-operators-prod
cd community-operators-prod

# Create the operator directory
mkdir -p operators/kubeheal-operator/0.1.0

# Copy bundle manifests
cp -r /path/to/kubeheal-operator/bundle/manifests \
  operators/kubeheal-operator/0.1.0/
cp -r /path/to/kubeheal-operator/bundle/metadata \
  operators/kubeheal-operator/0.1.0/

# Commit and push
git checkout -b kubeheal-operator-v0.1.0
git add operators/kubeheal-operator/
git commit -s -m "operator kubeheal-operator (0.1.0)"
git push origin kubeheal-operator-v0.1.0
```

Open a PR against `k8s-operatorhub/community-operators-prod`.

- [ ] PR opened against `community-operators-prod`
- [ ] OLM CI pipeline passes on the PR
- [ ] PR merged by maintainers

### 3.2 Verify OperatorHub listing

After the community-operators-prod PR merges (may take 1–3 days):

- [ ] `kubeheal-operator` appears on [OperatorHub.io](https://operatorhub.io)
- [ ] Operator installs cleanly from OperatorHub on a test cluster

---

## Post-Release

- [ ] Announce the release (GitHub Discussions, team channels)
- [ ] Update any downstream consumers (Validated Patterns, RHDP catalog items)
- [ ] Open the next milestone (e.g., `v1.3.0`) and triage deferred issues into it
- [ ] Archive old release branches that are past the support window

---

## Quick Reference: Issue and Milestone Map

| Step | Repo | Issue | Milestone |
|------|------|-------|-----------|
| Engine v1.2.0 release | coordination-engine | [#91](https://github.com/KubeHeal/openshift-coordination-engine/issues/91) | distribution |
| OCP 4.22 support | coordination-engine | [#81](https://github.com/KubeHeal/openshift-coordination-engine/issues/81) | v1.2.0 |
| E2E operator validation | coordination-engine | [#92](https://github.com/KubeHeal/openshift-coordination-engine/issues/92) | distribution |
| This checklist | coordination-engine | [#93](https://github.com/KubeHeal/openshift-coordination-engine/issues/93) | distribution |
| Versioning policy | coordination-engine | [#69](https://github.com/KubeHeal/openshift-coordination-engine/issues/69) | distribution |
| Operator v0.1.0 release | kubeheal-operator | [#2](https://github.com/KubeHeal/kubeheal-operator/issues/2) | v0.1.0 |
| OperatorHub submission | kubeheal-operator | [#3](https://github.com/KubeHeal/kubeheal-operator/issues/3) | v0.1.0 |

---

## Related Documentation

- [RELEASE.md](./RELEASE.md) — single-repo release process (versioning, tagging, changelog)
- [VERSION-STRATEGY.md](./docs/VERSION-STRATEGY.md) — multi-version OCP support strategy
- [CONTRIBUTING.md](./CONTRIBUTING.md) — development workflow and PR standards
