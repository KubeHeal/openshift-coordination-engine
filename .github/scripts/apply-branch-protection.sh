#!/bin/bash
#
# Apply Branch Protection Rules to GitHub Repository
#
# This script applies comprehensive branch protection rules to main and release branches
# as defined in ADR-013: GitHub Branch Protection and Collaboration Workflow.
#
# Prerequisites:
#   - GitHub CLI (gh) installed and authenticated
#   - Repository: tosin2013/openshift-coordination-engine
#   - Maintainer permissions on the repository
#
# Usage:
#   ./apply-branch-protection.sh
#
# See also:
#   - docs/adrs/013-github-branch-protection-collaboration.md
#   - GITHUB-SETUP.md (Branch Protection Configuration section)

set -e  # Exit on error
set -u  # Exit on undefined variable

# Configuration
REPO="tosin2013/openshift-coordination-engine"
BRANCHES=("main" "release-4.18" "release-4.19" "release-4.20")

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Branch protection configuration (JSON)
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

# Functions
log_info() {
  echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
  echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
  echo -e "${RED}[ERROR]${NC} $1"
}

check_prerequisites() {
  log_info "Checking prerequisites..."

  # Check if gh CLI is installed
  if ! command -v gh &> /dev/null; then
    log_error "GitHub CLI (gh) is not installed."
    log_error "Install from: https://cli.github.com/"
    exit 1
  fi

  # Check if gh is authenticated
  if ! gh auth status &> /dev/null; then
    log_error "GitHub CLI is not authenticated."
    log_error "Run: gh auth login"
    exit 1
  fi

  # Check if jq is installed (for JSON parsing)
  if ! command -v jq &> /dev/null; then
    log_warn "jq is not installed. Output will not be formatted."
    log_warn "Install from: https://stedolan.github.io/jq/"
  fi

  log_info "Prerequisites check passed."
}

apply_branch_protection() {
  local branch="$1"

  log_info "Applying protection to branch: $branch"

  # Apply protection using GitHub API
  if gh api "repos/$REPO/branches/$branch/protection" \
    --method PUT \
    --input - <<< "$PROTECTION_CONFIG" > /dev/null 2>&1; then
    log_info "✅ Protection applied to $branch"
  else
    log_error "❌ Failed to apply protection to $branch"
    log_error "   This may happen if:"
    log_error "   - Branch does not exist"
    log_error "   - You don't have admin permissions"
    log_error "   - Status checks (Lint, Test, Build, Security Scan) haven't run yet"
    return 1
  fi
}

verify_branch_protection() {
  local branch="$1"

  log_info "Verifying protection for branch: $branch"

  if gh api "repos/$REPO/branches/$branch/protection" > /dev/null 2>&1; then
    if command -v jq &> /dev/null; then
      log_info "Protection status for $branch:"
      gh api "repos/$REPO/branches/$branch/protection" | jq -r '
        "  Required approvals: \(.required_pull_request_reviews.required_approving_review_count // 0)",
        "  Required status checks: \(.required_status_checks.contexts | join(", "))",
        "  Code owner review required: \(.required_pull_request_reviews.require_code_owner_reviews)",
        "  Linear history required: \(.required_linear_history)",
        "  Force pushes allowed: \(.allow_force_pushes.enabled)",
        "  Deletions allowed: \(.allow_deletions.enabled)"
      '
    else
      log_info "✅ Protection is configured for $branch"
    fi
  else
    log_warn "⚠️  Could not verify protection for $branch"
  fi
}

main() {
  echo "=========================================="
  echo "Branch Protection Configuration Script"
  echo "Repository: $REPO"
  echo "=========================================="
  echo ""

  # Check prerequisites
  check_prerequisites
  echo ""

  # Confirm action
  log_warn "This script will apply branch protection rules to:"
  for branch in "${BRANCHES[@]}"; do
    echo "  - $branch"
  done
  echo ""
  read -p "Continue? (yes/no): " -r
  echo ""
  if [[ ! $REPLY =~ ^[Yy][Ee][Ss]$ ]]; then
    log_info "Aborted by user."
    exit 0
  fi

  # Apply protection to each branch
  local success_count=0
  local fail_count=0

  for branch in "${BRANCHES[@]}"; do
    if apply_branch_protection "$branch"; then
      ((success_count++))
    else
      ((fail_count++))
    fi
    echo ""
  done

  # Summary
  echo "=========================================="
  log_info "Summary:"
  log_info "  ✅ Successful: $success_count"
  if [ $fail_count -gt 0 ]; then
    log_error "  ❌ Failed: $fail_count"
  fi
  echo "=========================================="
  echo ""

  # Verify if all succeeded
  if [ $fail_count -eq 0 ]; then
    log_info "Verifying branch protection..."
    echo ""
    for branch in "${BRANCHES[@]}"; do
      verify_branch_protection "$branch"
      echo ""
    done

    log_info "✅ Branch protection configured successfully for all branches!"
    echo ""
    log_info "Next steps:"
    echo "  1. Verify CODEOWNERS file exists: .github/CODEOWNERS"
    echo "  2. Ensure CI workflows have run at least once on each branch"
    echo "  3. Test with a sample PR: see GITHUB-SETUP.md"
    echo "  4. Read ADR-013 for complete documentation"
  else
    log_error "Some branches failed. Common issues:"
    echo "  - Branch does not exist: Create the branch first"
    echo "  - Status checks not available: Run CI workflows at least once"
    echo "  - Insufficient permissions: Ensure you have admin access"
    echo ""
    log_info "See GITHUB-SETUP.md for troubleshooting guidance"
    exit 1
  fi
}

# Run main function
main
