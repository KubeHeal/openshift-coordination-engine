# ADR-013: GitHub Branch Protection and Collaboration Workflow

## Status
ACCEPTED - 2026-01-20

## Context

The Go Coordination Engine has matured with 12 ADRs, comprehensive CI/CD (4 GitHub Actions workflows), and multi-version support (main + release-4.18, 4.19, 4.20). As the project prepares for multi-collaborator development, we need formal governance to ensure code quality, prevent accidental breakage, and maintain a clean Git history.

### Current State

**Strengths:**
- Comprehensive CI: lint (golangci-lint), test (coverage), build, security-scan (Gosec + Trivy)
- DCO sign-off requirement documented
- Conventional commits in use
- Multi-version support with release branches

**Gaps:**
- No CODEOWNERS file for automatic review assignment
- No CONTRIBUTING.md guide for new contributors
- No formal branch protection rules enforced
- No documented PR review requirements
- No defined merge strategy (squash vs rebase vs merge)

### Collaboration Requirements

1. **Code Review**: All changes must be reviewed by maintainers before merge
2. **CI Validation**: All CI checks must pass (lint, test, build, security)
3. **History Cleanliness**: Maintain linear history, avoid merge commits
4. **Commit Standards**: Enforce DCO sign-off and conventional commits
5. **Code Ownership**: Automatically assign reviewers based on changed files
6. **Protection**: Prevent force pushes and accidental deletions of critical branches

## Decision

Implement comprehensive GitHub branch protection rules, code ownership, and contribution guidelines to enable safe multi-collaborator workflow.

### Branch Protection Rules

#### Main Branch (`main`)
- **Required approvals**: 1 reviewer
- **Required status checks**:
  - Lint (from ci.yaml)
  - Test (from ci.yaml)
  - Build (from ci.yaml)
  - Security Scan (from ci.yaml)
- **Additional requirements**:
  - Code owner review required
  - All conversations must be resolved
  - Signed commits (DCO) required
  - Linear history (no merge commits)
- **Restrictions**:
  - No force pushes
  - No branch deletions
  - Push restricted to maintainers only

#### Release Branches (`release-4.18`, `release-4.19`, `release-4.20`)
- **Required approvals**: 1 reviewer
- **Required status checks**:
  - Lint (from ci.yaml)
  - Test (from ci.yaml)
  - Build (from ci.yaml)
  - Security Scan (from ci.yaml)
  - Integration tests (from integration.yaml, if applicable)
- **Additional requirements**:
  - Code owner review required
  - All conversations must be resolved
  - Signed commits (DCO) required
  - Linear history (no merge commits)
- **Restrictions**:
  - No force pushes
  - No branch deletions
  - Push restricted to maintainers only

### PR Review Requirements

1. **Automatic Review Assignment**: CODEOWNERS file assigns reviewers based on changed files
2. **Approval Dismissal**: New commits dismiss previous approvals
3. **Conversation Resolution**: All review comments must be resolved before merge
4. **Review SLA**: Reviewers should respond within 24-48 hours
5. **PR Size Guidelines**: Prefer PRs < 400 lines (excluding generated code)

### Merge Strategy

**Squash and Merge (Default)**:
- Combines all commits into one clean commit on target branch
- Preserves PR description and commit messages in squash commit body
- Keeps main/release branch history linear and easy to read
- **Use for**: Most PRs with multiple commits

**Rebase and Merge (Allowed)**:
- Maintains linear history without squash
- Preserves individual commits from PR
- **Use for**: Single-commit PRs or when commit history is meaningful

**Merge Commits (Disabled)**:
- Creates merge commit, adds noise to history
- **Disabled** to maintain clean linear history

### Commit Convention

**Format**: `<type>(<scope>): <description>`

**Types**:
- `feat`: New feature
- `fix`: Bug fix
- `docs`: Documentation changes
- `style`: Code formatting (no logic change)
- `refactor`: Code restructuring (no behavior change)
- `test`: Test additions or modifications
- `chore`: Maintenance tasks (dependencies, configs)
- `ci`: CI/CD changes
- `perf`: Performance improvements

**Scopes** (examples):
- `detector`, `coordination`, `remediation`, `integrations`
- `api`, `helm`, `ci`, `docs`

**Examples**:
```
feat(detector): add KServe deployment method detection
fix(remediation): handle ArgoCD sync timeout gracefully
docs(adr): add ADR-013 for branch protection
ci(workflow): add integration test job
```

**DCO Sign-off**: All commits must include `Signed-off-by` line
```bash
git commit -s -m "feat(api): add health endpoint"
```

### Code Ownership Areas

Based on project structure from ADR-001:

| Path | Owner(s) | Rationale |
|------|----------|-----------|
| `*` | @tosin2013 | Default owner |
| `/internal/detector/` | @tosin2013 | Deployment and layer detection logic |
| `/internal/coordination/` | @tosin2013 | Multi-layer coordination orchestration |
| `/internal/remediation/` | @tosin2013 | Remediation strategies |
| `/internal/integrations/` | @tosin2013 | External service clients (ArgoCD, MCO, ML) |
| `/pkg/api/` | @tosin2013 | REST API handlers |
| `/charts/` | @tosin2013 | Helm chart configuration |
| `/.github/workflows/` | @tosin2013 | CI/CD workflows |
| `/docs/adrs/` | @tosin2013 | Architectural decisions |
| `/API-CONTRACT.md` | @tosin2013 | API specification |

As collaborators join, they will be added to relevant sections based on their areas of expertise.

## Implementation

### 1. Create CODEOWNERS File

**File**: `.github/CODEOWNERS`

Defines automatic reviewer assignment based on file paths.

### 2. Create CONTRIBUTING.md

**File**: `CONTRIBUTING.md` (root directory)

Comprehensive guide covering:
- Development workflow (fork, clone, branch, commit, PR)
- Commit message guidelines (conventional commits, DCO)
- PR process (requirements, review expectations, merge strategy)
- Testing requirements (>80% coverage)
- Code review guidelines (for contributors and reviewers)

### 3. Update GITHUB-SETUP.md

**Addition**: Detailed "Branch Protection Configuration" section

Includes:
- Step-by-step GitHub UI configuration
- GitHub CLI/API commands for programmatic setup
- Required status checks mapping
- Verification steps
- Troubleshooting guide

### 4. Update docs/DEVELOPMENT.md

**Addition**: "Pull Request Review Process" section

Documents:
- How to create a PR
- PR requirements checklist
- Review process flow
- Merge strategy explanation
- Code owner expectations

### 5. Apply Branch Protection via GitHub

**Options**:

**A) GitHub UI**: Settings → Branches → Add branch protection rule

**B) GitHub CLI** (recommended for consistency):
```bash
# Apply protection to main branch
gh api repos/{owner}/{repo}/branches/main/protection \
  --method PUT \
  --field required_status_checks='{"strict":true,"contexts":["Lint","Test","Build","Security Scan"]}' \
  --field enforce_admins=true \
  --field required_pull_request_reviews='{"dismissal_restrictions":{},"dismiss_stale_reviews":true,"require_code_owner_reviews":true,"required_approving_review_count":1}' \
  --field restrictions=null \
  --field required_linear_history=true \
  --field allow_force_pushes=false \
  --field allow_deletions=false \
  --field required_conversation_resolution=true

# Apply protection to release branches (repeat for each)
gh api repos/{owner}/{repo}/branches/release-4.18/protection --method PUT [...]
```

### 6. Verification

**Test PR Process**:
1. Create test branch: `test/branch-protection-verification`
2. Make trivial change (e.g., add comment to README)
3. Create PR to main
4. Verify:
   - CI checks run automatically
   - Merge blocked until checks pass
   - Merge blocked until 1 approval received
   - Code owner automatically assigned as reviewer
   - Direct push to main fails
5. Approve and merge using squash
6. Verify linear history maintained

## Configuration

### Environment Variables

No new environment variables required. Branch protection is configured via GitHub settings.

### CI Workflow Mapping

Required status checks map to existing GitHub Actions workflows:

| Status Check | Workflow | Job Name |
|--------------|----------|----------|
| Lint | `ci.yaml` | `lint` |
| Test | `ci.yaml` | `test` |
| Build | `ci.yaml` | `build` |
| Security Scan | `ci.yaml` | `security-scan` |

Integration tests (from `integration.yaml`) are optional for main but recommended for release branches.

## Testing Strategy

### Verification Tests

1. **Branch Protection Enforcement**:
   - Attempt direct push to main (should fail)
   - Attempt force push to main (should fail)
   - Attempt to delete main branch (should fail)

2. **PR Review Process**:
   - Create PR without DCO sign-off (CI should fail)
   - Create PR without code owner approval (merge blocked)
   - Create PR with failing tests (merge blocked)
   - Create PR with unresolved conversations (merge blocked)

3. **Merge Strategy**:
   - Verify squash and merge creates single commit
   - Verify linear history maintained
   - Verify PR description included in squash commit

### Rollback Plan

If branch protection causes issues:
1. Disable protection rules via GitHub settings
2. Fix underlying issue (CI configuration, status check names)
3. Re-enable protection rules
4. Verify with test PR

## Consequences

### Positive

✅ **Code Quality**: All changes reviewed by maintainers before merge
✅ **CI Validation**: No broken code in main/release branches
✅ **Clean History**: Linear history, easy to bisect and revert
✅ **Clear Ownership**: Automatic reviewer assignment reduces coordination overhead
✅ **Documentation**: CONTRIBUTING.md provides clear onboarding path
✅ **Protection**: Prevents accidental force pushes and deletions
✅ **Standards Enforcement**: DCO and conventional commits required via CI

### Negative

⚠️ **Approval Bottleneck**: Single maintainer (@tosin2013) initially - may slow velocity
⚠️ **Learning Curve**: New contributors must learn DCO, conventional commits, squash workflow
⚠️ **CI Dependency**: Flaky CI can block all merges
⚠️ **Overhead**: Additional process for small changes (even typo fixes require PR + approval)

### Mitigation Strategies

**Approval Bottleneck**:
- Add collaborators to CODEOWNERS as they gain expertise
- Document 24-48 hour review SLA to set expectations
- Use draft PRs for early feedback without blocking review queue

**Learning Curve**:
- Comprehensive CONTRIBUTING.md with examples
- PR template (future) with checklist
- Document common mistakes in troubleshooting section

**CI Dependency**:
- Keep CI fast (<5 minutes total)
- Monitor CI reliability, fix flaky tests immediately
- Allow administrators to bypass on CI infrastructure failures only

**Process Overhead**:
- Keep PR size guidelines (<400 lines) to speed reviews
- Use squash and merge to avoid fixup commit noise
- Automate what can be automated (linting, formatting)

## Rollout Plan

### Phase 1: Documentation (Immediate)
1. Create ADR-013 (this document)
2. Create `.github/CODEOWNERS`
3. Create `CONTRIBUTING.md`
4. Update `GITHUB-SETUP.md`
5. Update `docs/DEVELOPMENT.md`
6. Update `docs/adrs/README.md`

### Phase 2: Branch Protection (After Documentation Review)
1. Apply protection to `main` branch
2. Apply protection to `release-4.18`, `release-4.19`, `release-4.20`
3. Verify with test PR

### Phase 3: Validation (1-2 weeks)
1. Monitor PR workflow for friction points
2. Gather feedback from contributors
3. Adjust guidelines if needed (PR size, review SLA)

### Phase 4: Onboarding (Ongoing)
1. Share CONTRIBUTING.md with new collaborators
2. Add collaborators to CODEOWNERS as they demonstrate expertise
3. Update branch protection push restrictions if needed

## Success Metrics

**Code Quality**:
- Zero incidents of broken main/release branches
- >80% test coverage maintained
- Zero critical security findings from Gosec/Trivy in merged PRs

**Process Health**:
- 90% of PRs reviewed within 24-48 hours
- <5% of PRs require re-review after new commits
- CI passes on first try for >70% of PRs

**Collaboration**:
- Clear code ownership reduces review latency
- Contribution process documented and followed
- New contributors successfully onboard within 1 week

## Future Enhancements

### Issue and PR Templates
- Bug report template (steps to reproduce, expected vs actual)
- Feature request template (use case, proposed solution)
- PR template (checklist: DCO, tests, docs, ADR if needed)

### Automation
- PR size labeler (small, medium, large based on lines changed)
- Stale PR closer (auto-close after 30 days inactive)
- Changelog generator (from conventional commits)
- Release automation (version bumping, tagging)

### Advanced Protection
- Require deployments to succeed before merge (future E2E environment)
- Require security review for changes to `/internal/integrations/`
- Branch protection for `docs/*` with documentation reviewers

## References

### Internal
- [ADR-001: Go Project Architecture](001-go-project-architecture.md) - Project layout and standards
- [CLAUDE.md](../../CLAUDE.md) - Development workflow and commands
- [DEVELOPMENT.md](../../DEVELOPMENT.md) - Development guide
- [GITHUB-SETUP.md](../../GITHUB-SETUP.md) - GitHub configuration

### External
- [GitHub Branch Protection Rules](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-protected-branches)
- [CODEOWNERS Syntax](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/about-code-owners)
- [Conventional Commits](https://www.conventionalcommits.org/)
- [Developer Certificate of Origin (DCO)](https://developercertificate.org/)

## Related ADRs

- **ADR-001**: Go Project Architecture - Defines project structure referenced by CODEOWNERS
- **Platform ADR-042**: Go-Based Coordination Engine - Overall project rationale
- **All ADRs**: Benefit from enforced review process and documentation standards

---

*Implementation Date: 2026-01-20*
*Authors: @tosin2013*
*Reviewers: (To be added as collaborators join)*
