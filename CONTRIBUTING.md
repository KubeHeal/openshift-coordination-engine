# Contributing to OpenShift Coordination Engine

Thank you for your interest in contributing to the OpenShift Coordination Engine! This guide will help you get started with the development workflow, coding standards, and contribution process.

## Table of Contents

1. [Code of Conduct](#code-of-conduct)
2. [Getting Started](#getting-started)
3. [Development Workflow](#development-workflow)
4. [Commit Message Guidelines](#commit-message-guidelines)
5. [Pull Request Process](#pull-request-process)
6. [Code Review Guidelines](#code-review-guidelines)
7. [Branch Protection and Merge Strategy](#branch-protection-and-merge-strategy)
8. [Testing Requirements](#testing-requirements)
9. [Documentation](#documentation)
10. [Getting Help](#getting-help)

## Code of Conduct

We are committed to providing a welcoming and professional environment for all contributors. Expected behavior:

- **Respectful**: Treat all contributors with respect, regardless of experience level
- **Constructive**: Provide constructive feedback in code reviews
- **Collaborative**: Work together to solve problems and improve the codebase
- **Professional**: Maintain professional communication in issues, PRs, and discussions

## Getting Started

### Prerequisites

Before contributing, ensure you have the following installed:

- **Go 1.21+**: [Install Go](https://go.dev/doc/install)
- **Docker**: [Install Docker](https://docs.docker.com/get-docker/)
- **Kubernetes cluster**: Local (kind, minikube, k3s) or OpenShift cluster
- **kubectl**: [Install kubectl](https://kubernetes.io/docs/tasks/tools/)
- **Git**: [Install Git](https://git-scm.com/downloads)

### Fork and Clone

1. **Fork the repository** on GitHub (click "Fork" button)

2. **Clone your fork** locally:
   ```bash
   git clone https://github.com/YOUR_USERNAME/openshift-coordination-engine.git
   cd openshift-coordination-engine
   ```

3. **Add upstream remote**:
   ```bash
   git remote add upstream https://github.com/tosin2013/openshift-coordination-engine.git
   ```

4. **Verify remotes**:
   ```bash
   git remote -v
   # origin    https://github.com/YOUR_USERNAME/openshift-coordination-engine.git (fetch)
   # origin    https://github.com/YOUR_USERNAME/openshift-coordination-engine.git (push)
   # upstream  https://github.com/tosin2013/openshift-coordination-engine.git (fetch)
   # upstream  https://github.com/tosin2013/openshift-coordination-engine.git (push)
   ```

### Local Setup

1. **Install development tools**:
   ```bash
   make install-tools
   ```

   This installs:
   - `golangci-lint`: Linter
   - `goimports`: Import formatter
   - `ginkgo`: BDD testing framework

2. **Verify setup**:
   ```bash
   make ci
   ```

   This runs lint, test, and build to ensure everything works.

3. **Configure Git for DCO** (required):
   ```bash
   git config --global user.name "Your Name"
   git config --global user.email "your.email@example.com"
   ```

## Development Workflow

### 1. Create a Feature Branch

Always create a new branch for your work:

```bash
# Sync with upstream main
git checkout main
git pull upstream main

# Create feature branch
git checkout -b feat/your-feature-name

# Or for bug fixes
git checkout -b fix/your-bug-fix-name
```

**Branch naming conventions**:
- `feat/description` - New features
- `fix/description` - Bug fixes
- `docs/description` - Documentation changes
- `refactor/description` - Code refactoring
- `test/description` - Test additions
- `chore/description` - Maintenance tasks

### 2. Make Your Changes

Follow these guidelines:

- **Read ADRs**: Check [docs/adrs/README.md](docs/adrs/README.md) for relevant architectural decisions
- **Follow Go standards**: See [ADR-001](docs/adrs/001-go-project-architecture.md) for coding conventions
- **Write tests**: Add unit tests for all new code (>80% coverage required)
- **Update documentation**: Update README, ADRs, or DEVELOPMENT.md if needed

### 3. Test Your Changes

Run all checks locally before pushing:

```bash
# Run linting
make lint

# Run unit tests
make test

# Run tests with coverage
make coverage

# Run all CI checks (lint + test + build)
make ci
```

**Fix common issues**:
```bash
# Auto-format code
make fmt

# Fix import order
goimports -w .
```

### 4. Commit Your Changes

**IMPORTANT**: All commits must be signed off with DCO (Developer Certificate of Origin).

#### DCO Sign-off

By signing off, you certify that you have the right to submit the code and agree to the [Developer Certificate of Origin](https://developercertificate.org/).

**How to sign off**:
```bash
git commit -s -m "feat(api): add health endpoint"
```

The `-s` flag adds `Signed-off-by: Your Name <your.email@example.com>` to your commit message.

**Verify sign-off**:
```bash
git log --show-signature -1
```

You should see the `Signed-off-by` line in your commit message.

### 5. Push Your Branch

```bash
git push origin feat/your-feature-name
```

### 6. Create a Pull Request

1. Go to your fork on GitHub
2. Click "Compare & pull request"
3. Fill in the PR template:
   - **Title**: Use conventional commit format (e.g., `feat(api): add health endpoint`)
   - **Description**: Explain what and why (not how - code shows how)
   - **Testing**: Describe how you tested the changes
   - **Related Issues**: Link to related issues (e.g., `Fixes #123`)
4. Ensure all CI checks pass
5. Request review (code owners will be auto-assigned)

## Commit Message Guidelines

We use **Conventional Commits** for clear, semantic commit messages.

### Format

```
<type>(<scope>): <description>

[optional body]

[optional footer]
```

### Types

| Type | Description | Example |
|------|-------------|---------|
| `feat` | New feature | `feat(detector): add KServe deployment detection` |
| `fix` | Bug fix | `fix(remediation): handle ArgoCD timeout gracefully` |
| `docs` | Documentation | `docs(adr): add ADR-013 for branch protection` |
| `style` | Code formatting (no logic change) | `style(detector): fix gofmt formatting` |
| `refactor` | Code restructuring (no behavior change) | `refactor(api): extract handler logic` |
| `test` | Add or modify tests | `test(detector): add unit tests for layer detection` |
| `chore` | Maintenance tasks | `chore(deps): update client-go to v0.28.0` |
| `ci` | CI/CD changes | `ci(workflow): add integration test job` |
| `perf` | Performance improvements | `perf(detector): cache deployment method results` |

### Scopes

Common scopes based on project structure:

- `detector` - Deployment/layer detection
- `coordination` - Multi-layer coordination
- `remediation` - Remediation strategies
- `integrations` - External service clients (ArgoCD, MCO, ML)
- `api` - REST API handlers
- `helm` - Helm chart
- `ci` - CI/CD workflows
- `docs` - Documentation
- `adr` - Architectural decisions

### Examples

**Good commit messages**:
```
feat(api): add GET /api/v1/workflows/:id endpoint

Adds endpoint to retrieve workflow execution details by ID.
Required for MCP server integration (ADR-011).

Signed-off-by: Your Name <your.email@example.com>
```

```
fix(detector): handle missing ArgoCD tracking annotation

When ArgoCD annotation is missing, fall back to Helm detection
instead of returning error. Improves detection reliability.

Fixes #42

Signed-off-by: Your Name <your.email@example.com>
```

**Bad commit messages** (avoid these):
```
update code          ❌ Too vague
WIP                  ❌ Work-in-progress should not be committed to main
Fixed stuff          ❌ Not descriptive
asdf                 ❌ Not meaningful
```

## Pull Request Process

### PR Requirements

Before your PR can be merged, it must meet these requirements:

- ✅ **DCO sign-off**: All commits signed with `git commit -s`
- ✅ **CI checks pass**: Lint, test, build, security scan all green
- ✅ **Code owner approval**: At least 1 approval from code owners
- ✅ **Conversations resolved**: All review comments addressed
- ✅ **Tests included**: Unit tests for new code (>80% coverage)
- ✅ **Documentation updated**: README, ADRs, DEVELOPMENT.md if applicable
- ✅ **No merge conflicts**: Rebase on latest main if needed

### PR Size Guidelines

**Keep PRs small and focused**:
- ✅ **< 200 lines**: Ideal - quick to review
- ⚠️ **200-400 lines**: Acceptable - may take longer to review
- ❌ **> 400 lines**: Too large - consider splitting into multiple PRs

**Exceptions**:
- Generated code (e.g., from code generation tools)
- Large refactoring (discuss with maintainers first)
- Test additions (test files don't count toward limit)

### Review Process

1. **Code owners assigned**: Automatically based on CODEOWNERS file
2. **Review SLA**: Reviewers respond within 24-48 hours
3. **Feedback addressed**: Author addresses review comments
4. **Re-review if needed**: New commits dismiss previous approvals
5. **Approval granted**: Once all requirements met
6. **Merge**: Maintainer merges using squash and merge

### Addressing Review Feedback

**Respond to all comments**:
```
# If you made the requested change:
"Done in <commit-hash>"

# If you disagree:
"I kept X because Y. What do you think?"

# If you need clarification:
"Can you elaborate on what you mean by Z?"
```

**Push new commits**:
```bash
# Make changes based on feedback
git add .
git commit -s -m "fix(detector): address review feedback"
git push origin feat/your-feature-name
```

**Note**: New commits will dismiss previous approvals, requiring re-review.

## Code Review Guidelines

### For Contributors

When your PR is under review:

- **Be responsive**: Reply to comments within 24-48 hours
- **Ask questions**: If feedback is unclear, ask for clarification
- **Be open**: Consider feedback objectively, even if you disagree initially
- **Explain decisions**: If you choose not to implement a suggestion, explain why
- **Keep discussions focused**: Stick to the code, not personal preferences

### For Reviewers

When reviewing PRs:

- **Be timely**: Review within 24-48 hours of assignment
- **Be constructive**: Explain why, not just what
- **Be specific**: Point to exact lines, suggest alternatives
- **Approve minor issues**: Don't block on nitpicks (formatting, naming)
- **Test locally**: For complex changes, pull and test locally

**Review checklist**:
- ✅ Code follows Go conventions (ADR-001)
- ✅ Tests included and passing (>80% coverage)
- ✅ No security vulnerabilities (SQL injection, XSS, etc.)
- ✅ Error handling is appropriate
- ✅ Documentation updated if needed
- ✅ Commit messages follow conventional commits
- ✅ DCO sign-off present on all commits

## Branch Protection and Merge Strategy

### Protected Branches

The following branches are protected:

- **main**: Primary development branch
- **release-4.18, release-4.19, release-4.20**: Release branches

**Protection rules**:
- ❌ No direct pushes (must go through PR)
- ❌ No force pushes
- ❌ No branch deletions
- ✅ 1 required approval
- ✅ All CI checks must pass
- ✅ Code owner review required
- ✅ Conversations must be resolved
- ✅ Linear history enforced

### Merge Strategy

**Squash and Merge (Default)**:
- All commits in PR squashed into single commit on main
- Keeps history clean and linear
- PR description included in squash commit body
- **Use for**: Most PRs

**How it works**:
```
Your branch:  feat(api): add endpoint A
              feat(api): add endpoint B
              fix(api): fix typo

Main after merge: feat(api): add endpoints A and B (#123)
```

**Rebase and Merge (Allowed)**:
- Preserves individual commits from PR
- Maintains linear history without squash
- **Use for**: Single-commit PRs or when commit history is meaningful

**Merge Commits (Disabled)**:
- Creates merge commits, adds noise to history
- **Not allowed** in this project

### Syncing Your Fork

Keep your fork up to date with upstream:

```bash
# Fetch upstream changes
git fetch upstream

# Update your local main
git checkout main
git merge upstream/main

# Push to your fork
git push origin main

# Rebase your feature branch (if needed)
git checkout feat/your-feature
git rebase main
```

## Testing Requirements

### Unit Tests

**Required for all new code**:
- **Coverage**: Minimum 80% line coverage
- **Framework**: Use `testify` for assertions, `ginkgo/gomega` for BDD-style tests
- **Mocking**: Mock external dependencies (Kubernetes client, ML service, ArgoCD)

**Run unit tests**:
```bash
make test
```

**Check coverage**:
```bash
make coverage
open coverage.html  # View coverage report in browser
```

**Example test**:
```go
package detector_test

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "openshift-coordination-engine/internal/detector"
)

func TestDetectDeploymentMethod(t *testing.T) {
    d := detector.NewDeploymentDetector()

    // Test ArgoCD detection
    result := d.Detect(mockArgoCDResource())
    assert.Equal(t, "ArgoCD", result.Method)
    assert.GreaterOrEqual(t, result.Confidence, 0.95)
}
```

### Integration Tests

**When to add**:
- Changes to Kubernetes client interactions
- Changes to external service integrations (ArgoCD, ML)
- Multi-component workflows

**Run integration tests** (requires cluster):
```bash
export KUBECONFIG=~/.kube/config
make test-integration
```

### End-to-End Tests

**When to add**:
- New REST API endpoints
- Complete remediation workflows
- Multi-layer coordination scenarios

**Run E2E tests** (requires OpenShift cluster):
```bash
export KUBECONFIG=~/.kube/config
make test-e2e
```

## Documentation

### When to Update Documentation

Update documentation when you:

- **Add new features**: Update README, DEVELOPMENT.md
- **Change APIs**: Update API-CONTRACT.md
- **Make architectural decisions**: Create new ADR in docs/adrs/
- **Change build/deploy**: Update Makefile, CLAUDE.md, GITHUB-SETUP.md
- **Add dependencies**: Update DEVELOPMENT.md with setup instructions

### Creating ADRs

If your change involves an architectural decision:

1. **Check for existing ADRs**: Read [docs/adrs/README.md](docs/adrs/README.md)
2. **Create new ADR**: Follow template from existing ADRs
3. **Number sequentially**: Next available number (currently ADR-014)
4. **Include sections**: Status, Context, Decision, Implementation, Consequences, References
5. **Update index**: Add to docs/adrs/README.md
6. **Cross-reference**: Link related ADRs

**Example ADR creation**:
```bash
# Create ADR file
cat > docs/adrs/014-my-decision.md << 'EOF'
# ADR-014: My Architectural Decision

## Status
PROPOSED - 2026-01-20

## Context
...
EOF

# Update index
# Edit docs/adrs/README.md to add ADR-014 to index table
```

## Getting Help

### Resources

- **Documentation**: Start with [README.md](README.md), [DEVELOPMENT.md](docs/DEVELOPMENT.md)
- **ADRs**: Read [docs/adrs/README.md](docs/adrs/README.md) for architectural decisions
- **Issues**: Check [existing issues](https://github.com/tosin2013/openshift-coordination-engine/issues)

### Asking Questions

**Before asking**:
1. Check documentation (README, DEVELOPMENT, ADRs)
2. Search existing issues and PRs
3. Try to debug locally with logs

**How to ask**:
1. **Open an issue**: Use "Question" label
2. **Provide context**: What you're trying to do, what you've tried
3. **Include details**: Go version, cluster version, error logs
4. **Be specific**: "How do I X?" is better than "Help with Y?"

### Reporting Bugs

**Use the bug report template** (when available):

1. **Title**: Clear, descriptive (e.g., "Detector fails on missing ArgoCD annotation")
2. **Description**: What happened vs what you expected
3. **Steps to reproduce**: Numbered steps to reproduce the issue
4. **Environment**: Go version, cluster type, OS
5. **Logs**: Relevant error logs or stack traces
6. **Workaround**: If you found one, share it

## Thank You!

Your contributions make this project better. We appreciate your time and effort!

If you have questions or suggestions about this guide, please open an issue or submit a PR.

---

**Related Documentation**:
- [Development Guide](docs/DEVELOPMENT.md)
- [Architectural Decision Records](docs/adrs/README.md)
- [API Contract](API-CONTRACT.md)
- [ADR-013: Branch Protection](docs/adrs/013-github-branch-protection-collaboration.md)
