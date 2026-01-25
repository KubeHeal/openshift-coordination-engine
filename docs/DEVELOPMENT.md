# Development Guide: Go Coordination Engine

## Prerequisites

- **Go**: 1.21+ (matches ADR-036 MCP server standard)
- **Docker/Podman**: For building container images
- **kubectl/oc**: For Kubernetes/OpenShift access
- **make**: Build automation
- **golangci-lint**: Code linting (optional but recommended)

## Quick Start

```bash
# 1. Initialize Go module (if not already done)
go mod tidy

# 2. Build the binary
make build

# 3. Run unit tests
make test

# 4. Run locally (requires kubeconfig and Python ML service)
export KUBECONFIG=~/.kube/config
export ML_SERVICE_URL=http://localhost:8080
make run

# 5. Build container image
make docker-build
```

## Project Structure

```
openshift-coordination-engine/
├── cmd/
│   └── coordination-engine/
│       └── main.go                    # Entry point
├── internal/                          # Private application code
│   ├── detector/                      # ADR-041, ADR-040
│   ├── coordination/                  # ADR-040
│   ├── remediation/                   # ADR-039, ADR-038
│   └── integrations/                  # External service clients
├── pkg/                               # Public API and models
│   ├── api/v1/                        # REST API handlers
│   └── models/                        # Data structures
├── charts/                            # Helm chart for deployment
├── docs/adrs/                         # Architecture decisions
├── test/                              # Integration and e2e tests
│   ├── integration/
│   └── e2e/
├── go.mod                             # Go module definition
├── Makefile                           # Build automation
├── Dockerfile                         # Container image
└── README.md                          # Project overview
```

## Development Workflow

### 1. Create a Feature Branch

```bash
git checkout -b feature/deployment-detector
```

### 2. Implement Feature with Tests

**Example: Deployment Detector (ADR-041)**

```go
// internal/detector/deployment_detector.go
package detector

import (
    "context"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes"
)

type DeploymentMethod string

const (
    DeploymentMethodArgoCD   DeploymentMethod = "argocd"
    DeploymentMethodHelm     DeploymentMethod = "helm"
    DeploymentMethodOperator DeploymentMethod = "operator"
    DeploymentMethodManual   DeploymentMethod = "manual"
)

type DeploymentInfo struct {
    Method     DeploymentMethod `json:"method"`
    Managed    bool             `json:"managed"`
    ManagedBy  string           `json:"managed_by,omitempty"`
    Source     string           `json:"source,omitempty"`
    Confidence float64          `json:"confidence"`
}

type Detector struct {
    clientset *kubernetes.Clientset
}

func NewDetector(clientset *kubernetes.Clientset) *Detector {
    return &Detector{clientset: clientset}
}

func (d *Detector) DetectFromMetadata(
    labels, annotations map[string]string,
) *DeploymentInfo {
    // Priority 1: ArgoCD tracking annotation (highest confidence)
    if trackingID, ok := annotations["argocd.argoproj.io/tracking-id"]; ok {
        return &DeploymentInfo{
            Method:     DeploymentMethodArgoCD,
            Managed:    true,
            ManagedBy:  annotations["argocd.argoproj.io/application"],
            Confidence: 0.95,
        }
    }
    
    // Priority 2: Helm release annotation
    if helmRelease, ok := annotations["meta.helm.sh/release-name"]; ok {
        return &DeploymentInfo{
            Method:     DeploymentMethodHelm,
            Managed:    false,
            ManagedBy:  helmRelease,
            Confidence: 0.90,
        }
    }
    
    // Priority 3: Operator managed-by label
    if managedBy, ok := labels["app.kubernetes.io/managed-by"]; ok && managedBy != "Helm" {
        return &DeploymentInfo{
            Method:     DeploymentMethodOperator,
            Managed:    true,
            ManagedBy:  managedBy,
            Confidence: 0.80,
        }
    }
    
    // Default: Manual
    return &DeploymentInfo{
        Method:     DeploymentMethodManual,
        Managed:    false,
        Confidence: 0.60,
    }
}
```

**Unit Test**:

```go
// internal/detector/deployment_detector_test.go
package detector

import (
    "testing"
    "github.com/stretchr/testify/assert"
)

func TestDetectFromMetadata_ArgoCD(t *testing.T) {
    detector := NewDetector(nil)
    
    annotations := map[string]string{
        "argocd.argoproj.io/tracking-id":  "app:Deployment:ns/name",
        "argocd.argoproj.io/application": "payment-service",
    }
    
    result := detector.DetectFromMetadata(map[string]string{}, annotations)
    
    assert.Equal(t, DeploymentMethodArgoCD, result.Method)
    assert.True(t, result.Managed)
    assert.Equal(t, "payment-service", result.ManagedBy)
    assert.Greater(t, result.Confidence, 0.9)
}

func TestDetectFromMetadata_Helm(t *testing.T) {
    detector := NewDetector(nil)
    
    annotations := map[string]string{
        "meta.helm.sh/release-name": "my-release",
    }
    
    result := detector.DetectFromMetadata(map[string]string{}, annotations)
    
    assert.Equal(t, DeploymentMethodHelm, result.Method)
    assert.False(t, result.Managed)
    assert.Equal(t, "my-release", result.ManagedBy)
}
```

### 3. Run Tests and Linting

```bash
# Unit tests
make test

# Integration tests (requires running cluster)
make test-integration

# Linting
make lint

# Coverage report
make coverage
```

### 4. Build and Run Locally

```bash
# Build binary
make build

# Run with local Python ML service
export ML_SERVICE_URL=http://localhost:8080
export KUBECONFIG=~/.kube/config
./bin/coordination-engine
```

### 5. Build Container Image

```bash
# Build image
make docker-build

# Push to registry (set REGISTRY env var)
export REGISTRY=quay.io/myorg
make docker-push
```

## Configuration

The coordination engine is configured via environment variables:

```bash
# Kubernetes configuration
export KUBECONFIG=/path/to/kubeconfig

# Python ML service endpoint
export ML_SERVICE_URL=http://aiops-ml-service:8080

# ArgoCD API endpoint (optional, detected from cluster if not set)
export ARGOCD_API_URL=https://argocd-server:443

# Log level (debug, info, warn, error)
export LOG_LEVEL=info

# HTTP server port
export PORT=8080

# Metrics port
export METRICS_PORT=9090
```

## Integration with Python ML Service

The Go engine calls Python ML endpoints for anomaly detection and predictions.

**Example HTTP Client**:

```go
// internal/integrations/ml_service_client.go
package integrations

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
)

type MLServiceClient struct {
    baseURL    string
    httpClient *http.Client
}

func NewMLServiceClient(baseURL string) *MLServiceClient {
    return &MLServiceClient{
        baseURL:    baseURL,
        httpClient: &http.Client{Timeout: 30 * time.Second},
    }
}

type AnomalyDetectionRequest struct {
    Metrics []Metric `json:"metrics"`
}

type AnomalyDetectionResponse struct {
    Anomalies []Anomaly `json:"anomalies"`
    Score     float64   `json:"score"`
}

func (c *MLServiceClient) DetectAnomaly(
    ctx context.Context,
    req *AnomalyDetectionRequest,
) (*AnomalyDetectionResponse, error) {
    url := fmt.Sprintf("%s/api/v1/anomaly/detect", c.baseURL)
    
    body, err := json.Marshal(req)
    if err != nil {
        return nil, fmt.Errorf("marshal request: %w", err)
    }
    
    httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
    if err != nil {
        return nil, fmt.Errorf("create request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")
    
    resp, err := c.httpClient.Do(httpReq)
    if err != nil {
        return nil, fmt.Errorf("send request: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
    }
    
    var result AnomalyDetectionResponse
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("decode response: %w", err)
    }
    
    return &result, nil
}
```

## Testing Strategy

### Unit Tests
- Test each detector, planner, remediator in isolation
- Mock Kubernetes client and external services
- Aim for >80% code coverage

### Integration Tests
- Test with real Kubernetes cluster (kind/k3s for local dev)
- Deploy Python ML service locally or use mock server
- Test end-to-end workflows

### E2E Tests
- Deploy to OpenShift cluster
- Test with real ArgoCD, MCO, Python ML service
- Validate multi-layer coordination scenarios

## Debugging

### Local Development

```bash
# Enable debug logging
export LOG_LEVEL=debug

# Run with delve debugger
dlv debug ./cmd/coordination-engine
```

### In-Cluster Debugging

```bash
# Port-forward to coordination engine pod
kubectl port-forward -n self-healing-platform \
  deployment/coordination-engine 8080:8080 9090:9090

# View logs
kubectl logs -f -n self-healing-platform \
  deployment/coordination-engine

# Check metrics
curl http://localhost:9090/metrics
```

## Performance Considerations

- **Caching**: Implement caching for deployment detection (ADR-041)
- **Rate Limiting**: Limit API calls to ArgoCD, K8s API
- **Circuit Breakers**: Graceful degradation when Python ML service is down
- **Connection Pooling**: Reuse HTTP connections to Python ML service
- **Concurrency**: Use goroutines for parallel health checks

## Security

- **RBAC**: ServiceAccount with minimal required permissions (ADR-033)
- **TLS**: Verify TLS certificates for external services
- **Secrets**: Use Kubernetes Secrets for sensitive config
- **Input Validation**: Validate all API inputs
- **Context Timeouts**: Set timeouts on all external calls

## Common Issues

### Issue: Cannot connect to Kubernetes API
```bash
# Verify kubeconfig
kubectl cluster-info

# Check ServiceAccount permissions
kubectl auth can-i get pods --as=system:serviceaccount:self-healing-platform:coordination-engine
```

### Issue: Python ML service not responding
```bash
# Check ML service status
kubectl get pods -n self-healing-platform -l app=aiops-ml-service

# Test ML service endpoint
kubectl port-forward -n self-healing-platform svc/aiops-ml-service 8080:8080
curl http://localhost:8080/health
```

### Issue: ArgoCD integration not working
```bash
# Verify ArgoCD Application exists
kubectl get applications -n argocd

# Check ArgoCD API access
curl -k https://argocd-server/api/v1/applications
```

## Resources

- **Platform ADRs**: `/home/lab-user/openshift-aiops-platform/docs/adrs/`
  - ADR-033: RBAC Permissions
  - ADR-038: ArgoCD/MCO Integration
  - ADR-039: Non-ArgoCD Remediation
  - ADR-040: Multi-Layer Coordination
  - ADR-041: Deployment Detection
  - ADR-042: Go Coordination Engine
- **MCP Server Reference**: `/home/lab-user/openshift-cluster-health-mcp/` (Go implementation example)
- **Go client-go**: https://github.com/kubernetes/client-go
- **ArgoCD API**: https://argo-cd.readthedocs.io/en/stable/developer-guide/api-docs/

## Contributing

1. Read platform `AGENTS.md` and relevant ADRs
2. Create feature branch from `main`
3. Write tests first (TDD encouraged)
4. Implement feature with documentation
5. Run `make lint test` before committing
6. Commit with sign-off: `git commit -s -m "feat: add deployment detector"`
7. Open PR with description and ADR references

For a comprehensive contributing guide, see [CONTRIBUTING.md](../CONTRIBUTING.md).

## Pull Request Review Process

All changes to protected branches (`main`, `release-*`) must go through the pull request review process. See [ADR-013](adrs/013-github-branch-protection-collaboration.md) for complete branch protection strategy.

### Creating a Pull Request

1. **Sync with upstream**:
   ```bash
   git checkout main
   git pull upstream main
   ```

2. **Create feature branch**:
   ```bash
   git checkout -b feat/your-feature-name
   # Or: fix/bug-name, docs/doc-update, refactor/code-cleanup
   ```

3. **Make changes and commit** (with DCO sign-off):
   ```bash
   git add .
   git commit -s -m "feat(detector): add KServe deployment detection"
   ```

4. **Push to your fork**:
   ```bash
   git push origin feat/your-feature-name
   ```

5. **Open PR on GitHub**:
   ```bash
   gh pr create --title "feat(detector): add KServe deployment detection" \
     --body "Adds detection for KServe-deployed models. Fixes #123"
   ```

### PR Requirements Checklist

Before your PR can be merged, ensure:

- ✅ **DCO sign-off**: All commits signed with `git commit -s`
- ✅ **Conventional commits**: Title follows `type(scope): description` format
- ✅ **CI checks pass**: Lint, Test, Build, Security Scan all green
- ✅ **Code owner approval**: At least 1 approval from code owners
- ✅ **Conversations resolved**: All review comments addressed
- ✅ **Tests included**: Unit tests for new code (>80% coverage)
- ✅ **Documentation updated**: README, ADRs, or this guide if applicable
- ✅ **No merge conflicts**: Branch rebased on latest main if needed

### Review Process Flow

1. **PR created** → Code owners auto-assigned via CODEOWNERS
2. **CI checks run** → Lint, Test, Build, Security Scan (4-5 minutes)
3. **Review SLA** → Reviewers respond within 24-48 hours
4. **Feedback addressed** → Author makes changes, pushes new commits
5. **Re-review** → New commits dismiss previous approvals
6. **Approval granted** → Once all requirements met
7. **Merge** → Maintainer merges using squash and merge

### Addressing Review Feedback

**Respond to all review comments**:
- If you made the requested change: `"Done in abc123"`
- If you disagree: `"I kept X because Y. What do you think?"`
- If you need clarification: `"Can you elaborate on Z?"`

**Push new commits** (don't force push during review):
```bash
git add .
git commit -s -m "fix(detector): address review feedback"
git push origin feat/your-feature-name
```

**Resolve conversations** after addressing:
- Go to "Files changed" tab in PR
- Click "Resolve conversation" on addressed comments

### Code Owner Expectations

Code owners are automatically assigned based on `.github/CODEOWNERS`:

| Path | Owner(s) |
|------|----------|
| `/internal/detector/` | @tosin2013 |
| `/internal/coordination/` | @tosin2013 |
| `/internal/remediation/` | @tosin2013 |
| `/internal/integrations/` | @tosin2013 |
| `/pkg/api/` | @tosin2013 |
| `/docs/adrs/` | @tosin2013 |
| `/.github/workflows/` | @tosin2013 |

**Code owners will review for**:
- Adherence to Go conventions (ADR-001)
- Test coverage (>80% required)
- Security considerations (no SQL injection, XSS, etc.)
- Error handling and logging
- Documentation completeness

### Merge Strategy

The repository uses **squash and merge** by default:

**Squash and Merge (Default)**:
```
Your branch:  feat(api): add endpoint A
              feat(api): add endpoint B
              fix(api): fix typo

Main after merge: feat(api): add endpoints A and B (#123)
```

Benefits:
- Clean, linear history on main
- PR description preserved in squash commit body
- Easy to revert entire feature

**Rebase and Merge (Allowed)**:
- For single-commit PRs or meaningful commit history
- Maintains linear history without squash

**Merge Commits (Disabled)**:
- Not allowed to prevent merge commit noise

### Syncing Your Branch

If your branch falls behind main:

```bash
# Fetch latest from upstream
git fetch upstream

# Rebase your branch on main
git checkout feat/your-feature-name
git rebase upstream/main

# Resolve any conflicts, then push
git push origin feat/your-feature-name --force-with-lease
```

### Common PR Issues

**DCO check failing**:
```bash
# Amend last commit with sign-off
git commit --amend --signoff
git push origin feat/your-feature-name --force
```

**CI checks failing**:
```bash
# Run CI checks locally
make ci  # Runs lint + test + build

# Fix formatting
make fmt

# Re-run specific checks
make lint
make test
make coverage
```

**Merge blocked - conversations not resolved**:
- Go to "Files changed" tab
- Find comments with 🔵 (unresolved)
- Reply and click "Resolve conversation"

**Branch out of date**:
```bash
# Update branch with latest main
git checkout feat/your-feature-name
git fetch upstream
git rebase upstream/main
git push origin feat/your-feature-name --force-with-lease
```

### PR Size Guidelines

Keep PRs focused and reviewable:

- ✅ **< 200 lines**: Ideal - quick review
- ⚠️ **200-400 lines**: Acceptable - may take longer
- ❌ **> 400 lines**: Too large - split into multiple PRs

**Exceptions**: Generated code, large refactoring (discuss first), test additions

### Escalation

If a PR is blocked or needs urgent attention:

1. **Comment on PR**: Tag reviewers with `@username`
2. **Check review SLA**: Reviewers should respond within 24-48 hours
3. **Reach out**: Contact maintainers if SLA exceeded

---

**Need help?** Check the platform repo's troubleshooting guide, see [CONTRIBUTING.md](../CONTRIBUTING.md), or reach out to the platform team.

