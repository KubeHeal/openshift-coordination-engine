# GitHub Repository Setup Guide

This guide helps you configure your GitHub repository for automated CI/CD and container image publishing to Quay.io.

## Required GitHub Secrets

Before pushing to GitHub, configure these secrets in your repository:

### Navigation
1. Go to your GitHub repository: `https://github.com/tosin2013/openshift-coordination-engine`
2. Click **Settings** → **Secrets and variables** → **Actions**
3. Click **New repository secret**

### Required Secrets

#### 1. QUAY_USERNAME
- **Description**: Your Quay.io username or robot account name
- **Value**: `takinosh` or `takinosh+github_actions`
- **How to get**: 
  - Go to https://quay.io/user/takinosh?tab=settings
  - Or create a Robot Account for better security

#### 2. QUAY_TOKEN
- **Description**: Your Quay.io password or robot account token
- **How to get**:
  - **Option A - User Password**: Use your Quay.io account password (less secure)
  - **Option B - Robot Account** (RECOMMENDED):
    1. Go to https://quay.io/user/takinosh?tab=robots
    2. Click **Create Robot Account**
    3. Name it `github_actions`
    4. Grant **Write** permission to `takinosh/openshift-coordination-engine`
    5. Copy the generated token

#### 3. CODECOV_TOKEN (Optional)
- **Description**: Codecov token for code coverage reports
- **How to get**:
  1. Go to https://codecov.io/
  2. Sign in with GitHub
  3. Add repository `tosin2013/openshift-coordination-engine`
  4. Copy the upload token

## Setting Up Secrets

```bash
# Using GitHub CLI (recommended)
gh secret set QUAY_USERNAME -b "takinosh+github_actions"
gh secret set QUAY_TOKEN -b "YOUR_ROBOT_TOKEN_HERE"
gh secret set CODECOV_TOKEN -b "YOUR_CODECOV_TOKEN_HERE"

# Or set manually via GitHub UI
# Settings → Secrets and variables → Actions → New repository secret
```

## Verifying Setup

### 1. Check Secrets Are Set
```bash
gh secret list
# Should show: QUAY_USERNAME, QUAY_TOKEN, CODECOV_TOKEN
```

### 2. Test CI Workflow
```bash
# Push to main branch or create a PR
git push origin main

# Check workflow status
gh run list --workflow=ci.yaml

# View workflow logs
gh run view --log
```

### 3. Test Quay.io Push
```bash
# Tag and push (triggers release workflow)
git tag v0.1.0
git push origin v0.1.0

# Check workflow status
gh run list --workflow=release-quay.yaml

# Verify image on Quay.io
podman pull quay.io/takinosh/openshift-coordination-engine:latest
```

## GitHub Actions Workflows

### CI Workflow (`.github/workflows/ci.yaml`)
**Triggers**: Push to main/develop, Pull Requests
**Jobs**:
- **Lint**: Runs golangci-lint
- **Test**: Executes unit tests with coverage
- **Build**: Compiles binary and uploads artifact
- **Security Scan**: Runs Gosec and Trivy scanners

### Quay.io Release Workflow (`.github/workflows/release-quay.yaml`)
**Triggers**: Push to main, Tags (v*), Manual dispatch
**Jobs**:
- **Build and Push**: Builds multi-arch image (amd64, arm64) and pushes to Quay.io
- **Security Scan**: Scans container image with Trivy
- **GitHub Release**: Creates release notes for version tags

## Making Your First Push

```bash
# Initialize git (if not already done)
git init
git add .
git commit -m "Initial commit: OpenShift Coordination Engine"
git branch -M main

# Add remote (replace with your repository)
git remote add origin https://github.com/tosin2013/openshift-coordination-engine.git

# Push to GitHub
git push -u origin main

# Watch the CI workflow run
gh run watch

# After CI passes, create a release
git tag v0.1.0
git push origin v0.1.0

# Watch the release workflow
gh run watch
```

## Troubleshooting

### "Quay.io login failed"
```bash
# Verify secrets are set correctly
gh secret list

# Test Quay.io credentials locally
echo "YOUR_TOKEN" | podman login quay.io -u takinosh+github_actions --password-stdin

# Check workflow logs
gh run view --log
```

### "Permission denied to push image"
- Ensure robot account has **Write** permission
- Verify repository exists: https://quay.io/repository/takinosh/openshift-coordination-engine
- Make repository **Public** or grant robot account access

### "Tests failing in CI"
```bash
# Run tests locally first
make test

# Check for missing dependencies
go mod tidy

# Review test logs in GitHub Actions
gh run view --log --job=test
```

### "Lint errors in CI"
```bash
# Run linter locally
make lint

# Auto-fix formatting issues
make fmt

# Check specific errors
golangci-lint run
```

## Monitoring Workflows

### GitHub CLI Commands
```bash
# List recent workflow runs
gh run list

# Watch current run
gh run watch

# View specific run
gh run view RUN_ID

# View logs for failed run
gh run view RUN_ID --log-failed

# Re-run failed jobs
gh run rerun RUN_ID --failed
```

### GitHub UI
- View all workflows: `https://github.com/tosin2013/openshift-coordination-engine/actions`
- View CI runs: `https://github.com/tosin2013/openshift-coordination-engine/actions/workflows/ci.yaml`
- View releases: `https://github.com/tosin2013/openshift-coordination-engine/actions/workflows/release-quay.yaml`

## Security Best Practices

1. **Use Robot Accounts**: Create dedicated robot accounts for GitHub Actions instead of using personal credentials
2. **Limit Permissions**: Grant only necessary permissions (Write to specific repository)
3. **Rotate Tokens**: Periodically rotate Quay.io tokens
4. **Review Security Scans**: Check Trivy scan results in GitHub Security tab
5. **Enable Branch Protection**: Require CI to pass before merging PRs (see configuration below)

## Branch Protection Configuration

Branch protection ensures code quality by requiring CI checks and code reviews before merging. See [ADR-013](docs/adrs/013-github-branch-protection-collaboration.md) for the complete branch protection strategy.

### Required Configuration

Protect the following branches:
- **main**: Primary development branch
- **release-4.18, release-4.19, release-4.20**: Release branches

### Option 1: GitHub UI Configuration (Recommended for First-Time Setup)

#### Step-by-Step Instructions

1. **Navigate to Branch Protection Settings**:
   - Go to repository: `https://github.com/tosin2013/openshift-coordination-engine`
   - Click **Settings** → **Branches** (left sidebar)
   - Click **Add branch protection rule**

2. **Configure for Main Branch**:

   **Branch name pattern**: `main`

   **Protect matching branches** - Enable these settings:

   ✅ **Require a pull request before merging**:
   - Required approvals: `1`
   - ✅ Dismiss stale pull request approvals when new commits are pushed
   - ✅ Require review from Code Owners
   - ✅ Require approval of the most recent reviewable push
   - ✅ Require conversation resolution before merging

   ✅ **Require status checks to pass before merging**:
   - ✅ Require branches to be up to date before merging
   - **Required status checks** (search and add these):
     - `Lint` (from ci.yaml workflow)
     - `Test` (from ci.yaml workflow)
     - `Build` (from ci.yaml workflow)
     - `Security Scan` (from ci.yaml workflow)

   ✅ **Require signed commits**

   ✅ **Require linear history**

   ✅ **Do not allow bypassing the above settings**

   ❌ **Allow force pushes** (leave unchecked)

   ❌ **Allow deletions** (leave unchecked)

   **Restrict who can push to matching branches** (Optional):
   - Add: `tosin2013` (and other maintainers as they join)

3. **Click "Create"** to save the rule

4. **Repeat for Release Branches**:
   - Click **Add branch protection rule** again
   - **Branch name pattern**: `release-*` (matches all release branches)
   - Apply the same settings as main branch
   - **Additional status check** (if available): `Integration Tests` (from integration.yaml)
   - Click **Create**

### Option 2: GitHub CLI Configuration (Recommended for Automation)

Use the GitHub CLI to programmatically apply branch protection rules with exact settings.

#### Prerequisites
```bash
# Install GitHub CLI if not already installed
# See: https://cli.github.com/

# Authenticate
gh auth login
```

#### Apply Protection to Main Branch

```bash
gh api repos/tosin2013/openshift-coordination-engine/branches/main/protection \
  --method PUT \
  --field required_status_checks='{"strict":true,"contexts":["Lint","Test","Build","Security Scan"]}' \
  --field enforce_admins=true \
  --field required_pull_request_reviews='{"dismissal_restrictions":{},"dismiss_stale_reviews":true,"require_code_owner_reviews":true,"required_approving_review_count":1,"require_last_push_approval":true}' \
  --field restrictions=null \
  --field required_linear_history=true \
  --field allow_force_pushes=false \
  --field allow_deletions=false \
  --field required_conversation_resolution=true \
  --field required_signatures=true
```

#### Apply Protection to Release Branches

```bash
# Apply to each release branch individually
for branch in release-4.18 release-4.19 release-4.20; do
  gh api repos/tosin2013/openshift-coordination-engine/branches/$branch/protection \
    --method PUT \
    --field required_status_checks='{"strict":true,"contexts":["Lint","Test","Build","Security Scan"]}' \
    --field enforce_admins=true \
    --field required_pull_request_reviews='{"dismissal_restrictions":{},"dismiss_stale_reviews":true,"require_code_owner_reviews":true,"required_approving_review_count":1,"require_last_push_approval":true}' \
    --field restrictions=null \
    --field required_linear_history=true \
    --field allow_force_pushes=false \
    --field allow_deletions=false \
    --field required_conversation_resolution=true \
    --field required_signatures=true
done
```

#### Alternative: Using Shell Script

Create `.github/scripts/apply-branch-protection.sh`:

```bash
#!/bin/bash
set -e

REPO="tosin2013/openshift-coordination-engine"
BRANCHES=("main" "release-4.18" "release-4.19" "release-4.20")

PROTECTION_CONFIG='{
  "required_status_checks": {
    "strict": true,
    "contexts": ["Lint", "Test", "Build", "Security Scan"]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": {
    "dismissal_restrictions": {},
    "dismiss_stale_reviews": true,
    "require_code_owner_reviews": true,
    "required_approving_review_count": 1,
    "require_last_push_approval": true
  },
  "restrictions": null,
  "required_linear_history": true,
  "allow_force_pushes": false,
  "allow_deletions": false,
  "required_conversation_resolution": true,
  "required_signatures": true
}'

for branch in "${BRANCHES[@]}"; do
  echo "Applying protection to $branch..."
  gh api "repos/$REPO/branches/$branch/protection" \
    --method PUT \
    --input - <<< "$PROTECTION_CONFIG"
  echo "✅ Protection applied to $branch"
done

echo "✅ Branch protection configured for all branches"
```

Make executable and run:
```bash
chmod +x .github/scripts/apply-branch-protection.sh
./.github/scripts/apply-branch-protection.sh
```

### Required Status Checks Mapping

These status checks map to jobs in GitHub Actions workflows:

| Status Check Name | Workflow File | Job Name | Description |
|-------------------|---------------|----------|-------------|
| Lint | `.github/workflows/ci.yaml` | `lint` | Go linting with golangci-lint |
| Test | `.github/workflows/ci.yaml` | `test` | Unit tests with coverage >80% |
| Build | `.github/workflows/ci.yaml` | `build` | Build binary for linux/amd64 |
| Security Scan | `.github/workflows/ci.yaml` | `security-scan` | Gosec + Trivy security scans |
| Integration Tests | `.github/workflows/integration.yaml` | `integration-test` | Integration tests (optional for main) |

**Note**: Status check names must match the job names exactly as defined in workflow files.

### Verifying Branch Protection

After applying branch protection:

1. **Check Protection Status**:
   ```bash
   # View protection for main
   gh api repos/tosin2013/openshift-coordination-engine/branches/main/protection | jq

   # View protection for release branch
   gh api repos/tosin2013/openshift-coordination-engine/branches/release-4.18/protection | jq
   ```

2. **Test with Sample PR**:
   ```bash
   # Create test branch
   git checkout -b test/branch-protection-verification

   # Make trivial change
   echo "# Testing branch protection" >> README.md
   git add README.md
   git commit -s -m "test: verify branch protection rules"
   git push origin test/branch-protection-verification

   # Create PR
   gh pr create --title "test: verify branch protection" --body "Testing branch protection rules"

   # Verify:
   # ✅ CI checks run automatically
   # ✅ Merge blocked until checks pass
   # ✅ Merge blocked until 1 approval received
   # ✅ Code owner (@tosin2013) auto-assigned as reviewer
   ```

3. **Test Direct Push Protection** (should fail):
   ```bash
   # Attempt direct push to main (should be rejected)
   git checkout main
   echo "test" >> README.md
   git commit -s -m "test: direct push"
   git push origin main
   # Expected: remote: error: GH006: Protected branch update failed
   ```

4. **Clean Up Test PR**:
   ```bash
   # Close and delete test PR
   gh pr close test/branch-protection-verification --delete-branch
   ```

### Troubleshooting Branch Protection

#### Issue: Status Checks Not Showing

**Problem**: Required status checks like "Lint" or "Test" don't appear in the dropdown.

**Solution**:
1. Status checks must run at least once before they appear in the UI
2. Create a PR to trigger workflows, then add the checks to protection
3. Verify workflow job names match exactly:
   ```bash
   # Check workflow job names
   gh run list --workflow=ci.yaml --limit 1
   gh run view RUN_ID --log
   ```

#### Issue: DCO Check Failing

**Problem**: PR blocked because commits lack DCO sign-off.

**Solution**:
```bash
# Amend last commit with sign-off
git commit --amend --signoff

# Force push (only to your feature branch, not main!)
git push --force origin your-branch-name

# For multiple commits, use interactive rebase
git rebase -i HEAD~3
# Mark commits as 'edit', then for each:
git commit --amend --signoff
git rebase --continue
```

#### Issue: Merge Blocked - "Conversations Must Be Resolved"

**Problem**: PR has unresolved review comments.

**Solution**:
1. Go to "Files changed" tab in PR
2. Find all review comments with 🔵 (unresolved)
3. Reply to each comment or click "Resolve conversation"
4. Once all conversations have ✅, merge will be unblocked

#### Issue: "Branch Is Out of Date"

**Problem**: PR branch is behind main, "Update branch" button appears.

**Solution**:
```bash
# Update your feature branch with latest main
git checkout your-feature-branch
git fetch upstream
git rebase upstream/main

# Or merge (if rebase is too complex)
git merge upstream/main

# Push updated branch
git push origin your-feature-branch --force-with-lease
```

#### Issue: CI Checks Failing

**Problem**: PR blocked because Lint, Test, Build, or Security Scan failed.

**Solution**:
```bash
# Run checks locally to debug
make ci  # Runs lint + test + build

# Fix issues
make fmt      # Auto-format code
make lint     # Check linting
make test     # Run tests
make coverage # Check coverage >80%

# Commit fixes with DCO
git add .
git commit -s -m "fix(ci): resolve lint/test failures"
git push origin your-feature-branch
```

### Adding New Maintainers

As collaborators join and gain maintainer status:

1. **Add to Repository Collaborators**:
   ```bash
   # Grant write access
   gh api repos/tosin2013/openshift-coordination-engine/collaborators/NEW_USERNAME \
     --method PUT \
     --field permission=push
   ```

2. **Update CODEOWNERS**:
   ```bash
   # Edit .github/CODEOWNERS
   # Add new maintainer to relevant sections:
   /internal/detector/  @tosin2013 @new-maintainer
   ```

3. **Update Branch Protection Push Restrictions** (optional):
   - Settings → Branches → Edit main protection rule
   - **Restrict who can push to matching branches**: Add new maintainer

4. **Update Documentation**:
   - Add to CONTRIBUTING.md acknowledgments
   - Update ADR-013 with new owner information

### Merge Strategy Configuration

The repository allows two merge strategies:

**Squash and Merge (Default)**:
- Enabled by default for all PRs
- Combines all commits into one clean commit
- Keeps main branch history linear

**Rebase and Merge (Allowed)**:
- Enabled for single-commit PRs or meaningful commit history
- Maintains linear history without squash

**Merge Commits (Disabled)**:
- Disabled to prevent merge commit noise
- Settings → General → Pull Requests:
  - ✅ Allow squash merging
  - ✅ Allow rebase merging
  - ❌ Allow merge commits (unchecked)

## Next Steps

After setup:
1. ✅ Set up GitHub secrets
2. ✅ Push code to GitHub
3. ✅ Verify CI passes
4. ✅ Create first release (v0.1.0)
5. ✅ Verify image pushed to Quay.io
6. ✅ Test deployment with Helm

---

**Need Help?**
- GitHub Actions Documentation: https://docs.github.com/en/actions
- Quay.io Robot Accounts: https://docs.quay.io/glossary/robot-accounts.html
- GitHub CLI: https://cli.github.com/
