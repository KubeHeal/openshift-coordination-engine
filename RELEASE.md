# Release Guide — openshift-coordination-engine

This document is the canonical reference for cutting a release of the Coordination Engine.
It covers versioning policy, branch strategy, the step-by-step release checklist, and
community standards that must be followed before tagging.

---

## Versioning Policy

The Coordination Engine uses **Semantic Versioning** (`MAJOR.MINOR.PATCH`):

| Increment | When |
|-----------|------|
| **MAJOR** | Breaking REST API changes (endpoint removed/renamed, response schema incompatible) |
| **MINOR** | New endpoints, new response fields (backward-compatible), new ADRs implemented |
| **PATCH** | Bug fixes, dependency bumps, documentation corrections |

### Rolling 3-Version Support Matrix

The engine is tested and supported against the three most recent OpenShift release trains.
OCP 4.21 is now GA (April 2026) — the active window has shifted to 4.19 / 4.20 / 4.21.

| OCP Version | Kubernetes | Status |
|-------------|------------|--------|
| 4.21        | 1.34       | Active (current) |
| 4.20        | 1.33       | Active |
| 4.19        | 1.32       | Active |
| 4.18        | 1.31       | Maintenance — dropping when 4.22 releases |

Older versions may work but receive no backport PRs.

---

## Branch Strategy

```
main          ← integration branch (CI must be green before merge)
develop       ← feature work (optional; used for long-running feature sets)
release-4.19  ← patch backports for OCP 4.19 train
release-4.20  ← patch backports for OCP 4.20 train
release-4.21  ← patch backports for OCP 4.21 train (current)
release-4.18  ← maintenance only (no new features)
```

`release-4.x` branches are synced from `main` automatically by `.github/workflows/sync-release-branch.yaml`
when a push lands on `main`.

---

## Required Checks Before Merge (Branch Protection)

All PRs targeting `main` must pass:

| Check | Workflow | Required |
|-------|----------|----------|
| `Lint` | `ci.yaml` | ✅ |
| `Test` | `ci.yaml` | ✅ |
| `Build` | `ci.yaml` | ✅ |
| `codecov/patch` | `ci.yaml` | ✅ |
| `Integration Tests` | `integration.yaml` | ✅ |

At least **1 approving review** is required. Direct pushes to `main` are not permitted.

---

## Developer Certificate of Origin (DCO)

All commits **must** include a DCO sign-off line matching the author email:

```bash
git commit -s -m "feat: your commit message"
# Adds: Signed-off-by: Your Name <your@email.com>
```

**Author standards:**
```bash
git config user.name  "Tosin Akinosho"
git config user.email "takinosh@redhat.com"
```

---

## Release Checklist

### 1. Pre-Release

- [ ] All issues targeted for this milestone are closed or moved to the next milestone
- [ ] `CHANGELOG.md` `[Unreleased]` section is complete and accurate
- [ ] All ADRs for new features are in `Accepted` status (`docs/adrs/`)
- [ ] `todo.md` reviewed; no high-priority items outstanding
- [ ] CI is green on `main` (all required checks pass)
- [ ] Integration tests pass: `make integration-test`
- [ ] Go version in `go.mod` matches the version in `ci.yaml` (`go-version:`)

### 2. Version Bump

```bash
# Update go.mod toolchain line if needed
# Update any version constant in source (grep for VERSION or AppVersion)
grep -r "AppVersion\|Version = " cmd/ pkg/
```

### 3. Update CHANGELOG

```bash
# Move [Unreleased] content to the new version section
# Example for v1.2.0:
# ## [1.2.0] - $(date +%Y-%m-%d)
```

Commit with a DCO sign-off:
```bash
git add CHANGELOG.md
git commit -s -m "release: prepare v1.2.0 — update CHANGELOG"
```

### 4. Tag

```bash
VERSION=v1.2.0
git tag -a "$VERSION" -m "Release $VERSION"
git push origin main --tags
```

### 5. Sync Release Branches

The `sync-release-branch.yaml` workflow runs automatically on push to `main`.
Verify it completed successfully in the GitHub Actions tab.

For **manual** backport to a specific release branch:
```bash
git checkout release-4.21
git cherry-pick <commit-sha>   # see docs/playbooks/cherry-pick-fixes.md
git push origin release-4.21
```

### 6. Quay Image Publish

The `release-quay.yaml` workflow publishes to Quay automatically when a push lands on
`release-4.18`, `release-4.19`, `release-4.20`, or `release-4.21`, or when triggered manually:

```bash
# Manual trigger via GitHub CLI
gh workflow run release-quay.yaml --repo KubeHeal/openshift-coordination-engine \
  -f ref=release-4.21
```

Verify the image at: `quay.io/kubeheal/openshift-coordination-engine:<tag>`

### 7. GitHub Release Draft

```bash
gh release create "$VERSION" \
  --repo KubeHeal/openshift-coordination-engine \
  --title "openshift-coordination-engine $VERSION" \
  --notes-file <(sed -n "/^## \[$VERSION\]/,/^## \[/p" CHANGELOG.md | head -n -1) \
  --draft
```

Review the draft and publish when ready.

### 8. Update Milestone

```bash
# Close the completed milestone via GitHub UI or:
gh api repos/KubeHeal/openshift-coordination-engine/milestones \
  --jq '.[] | select(.title=="'$VERSION'") | .number'
# Then: gh api repos/.../milestones/{number} --method PATCH --field state=closed
```

---

## Helm Chart Release

The Helm chart lives under `charts/openshift-coordination-engine/`. When the CE version
changes, bump both `version` (chart) and `appVersion` in `Chart.yaml`, then re-render:

```bash
helm lint charts/openshift-coordination-engine
helm template charts/openshift-coordination-engine | grep "image:"
```

---

## Related Documentation

- [CHANGELOG.md](./CHANGELOG.md) — full version history
- [CONTRIBUTING.md](./CONTRIBUTING.md) — development workflow and PR standards
- [docs/adrs/](./docs/adrs/) — Architectural Decision Records
- [docs/playbooks/cherry-pick-fixes.md](./docs/playbooks/cherry-pick-fixes.md) — backport guide
- [GitHub Issues](https://github.com/KubeHeal/openshift-coordination-engine/issues) — issue tracker
