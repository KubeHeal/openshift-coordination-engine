# Documentation Audit Report

**Repository:** KubeHeal/openshift-coordination-engine
**Audit date:** 2026-09-21
**Auditor:** AI-assisted (documentation-specialist skill, STE100 voice)
**Scope:** All 44 markdown files in the repository

## Executive summary

- **Quality:** Fair
- **Completeness:** 70%
- **Recommendation:** Major revisions

The repository has strong coverage for architectural decisions (20 ADRs), API contracts,
and contributor workflows. Several critical gaps exist: a 30-line architecture stub, stale
Go version references in four files, broken links to two missing documents, and no formal
software design document. The audit found 5 critical issues, 6 high-priority issues, and
7 medium-priority issues.

---

## Critical issues

| ID | File | Issue | Impact |
|----|------|-------|--------|
| C-1 | `ARCHITECTURE.md` | 30-line stub. References local paths (`/home/lab-user/...`) that do not exist. States "Go Engine Stub (here)," which is outdated. | New developers get no useful architecture overview. |
| C-2 | `README.md` line 22 | States "Go 1.21+" as a prerequisite. The `go.mod` requires Go 1.26.0. | Contributors install the wrong Go version and cannot build the project. |
| C-3 | `docs/DEVELOPMENT.md` line 5 | States "Go: 1.21+". Same root cause as C-2. | Same impact as C-2. |
| C-4 | `CONTRIBUTING.md` line 33 | States "Go 1.21+". Same root cause as C-2. | Same impact as C-2. |
| C-5 | `CLAUDE.md` line 219 | States "Go version: 1.21+". Same root cause as C-2. | AI agents use the wrong Go version when building. |

**Recommendation:** Fix C-2 through C-5 by updating to "Go 1.26+". Replace C-1
(`ARCHITECTURE.md`) with a pointer to the new `DESIGN_DOC.md`.

---

## High-priority issues

| ID | File | Issue | Impact |
|----|------|-------|--------|
| H-1 | `README.md` line 400 | Links to `docs/MONITORING.md`, which does not exist. | Readers reach a dead link when looking for metrics guidance. |
| H-2 | `README.md` line 451 | Links to `docs/IMPLEMENTATION-PLAN.md`, which does not exist. | Readers reach a dead link for implementation status. |
| H-3 | `CLAUDE.md` line 210 | References `MIGRATION-GUIDE.md`, which does not exist. | AI agents cannot find the migration steps. |
| H-4 | `docs/adrs/README.md` | 17 links to `../../openshift-aiops-platform/docs/adrs/...` paths that do not exist in this repository. | All platform ADR cross-references are broken when viewing this repo in isolation. |
| H-5 | (missing) | No `DESIGN_DOC.md` or formal software design document exists. | No single document describes the full system architecture with diagrams, constraints, and quality requirements. |
| H-6 | `CHANGELOG.md` | `[Unreleased]` section does not mention issues #68, #90, or #94 (CI fixes, golangci-lint v2, security remediations). | Release notes will be incomplete when v1.2.0 is cut. |

---

## Medium-priority issues

| ID | File | Issue | Impact |
|----|------|-------|--------|
| M-1 | `todo.md` | Working scratchpad, not formal documentation. No header or date. | Readers may confuse it with a project roadmap. |
| M-2 | `GITHUB-SETUP.md` | Overlaps significantly with `CONTRIBUTING.md` (fork, clone, CI setup). | Duplicate guidance creates maintenance burden. |
| M-3 | `ARCHITECTURE.md` + `CLAUDE.md` | Reference local filesystem paths (`/home/lab-user/openshift-aiops-platform`, `/home/lab-user/openshift-cluster-health-mcp`). | Paths are meaningless outside the original development machine. |
| M-4 | `API-CONTRACT.md` line 462 | References `../docs/adrs/039-user-deployed-kserve-models.md`, which does not exist in this repo. | Broken cross-reference. |
| M-5 | `docs/adrs/README.md` line 250 | States "next: ADR-016" but ADR-016 through ADR-020 already exist. | Outdated guidance for contributors creating new ADRs. |
| M-6 | All ADRs | No ADR includes a table of contents. Several exceed 200 lines. | Long ADRs are hard to navigate. |
| M-7 | (missing) | No OpenAPI/Swagger specification file exists. Issue #71 tracks this. | API consumers must read prose docs instead of machine-readable specs. |

---

## Low-priority issues

| ID | File | Issue |
|----|------|-------|
| L-1 | `README.md` | Deployment example on line 370 uses `--set mlServiceUrl=...` (deprecated legacy ML). |
| L-2 | `docs/adrs/README.md` | ADR status legend uses emoji (non-STE100) but does not affect correctness. |
| L-3 | `charts/coordination-engine/README.md` | No version or last-updated date. |
| L-4 | `test/integration/README.md` | No link back to the main TESTING-GUIDE.md. |
| L-5 | `.github/INTEGRATION_TESTING.md` | Duplicates content from `test/integration/README.md`. |

---

## Quick wins (less than 1 hour each)

1. **Update Go version** in `README.md`, `DEVELOPMENT.md`, `CONTRIBUTING.md`, and `CLAUDE.md` from "1.21+" to "1.26+".
2. **Remove dead links** in `README.md` to `docs/MONITORING.md` and `docs/IMPLEMENTATION-PLAN.md`.
3. **Update ADR README** next-ADR number from "ADR-016" to "ADR-021".
4. **Replace deprecated ML example** in README.md deployment section with KServe configuration.

---

## Next steps

1. Fix all critical issues (C-1 through C-5).
2. Create `DESIGN_DOC.md` to resolve H-5 and replace C-1.
3. Address high-priority broken links (H-1, H-2, H-3).
4. Update `CHANGELOG.md` with recent work before v1.2.0 release (H-6).
5. Consolidate `GITHUB-SETUP.md` into `CONTRIBUTING.md` (M-2).
6. Replace local filesystem paths with GitHub URLs or remove them (M-3).
7. Generate OpenAPI spec when issue #71 is implemented (M-7).
