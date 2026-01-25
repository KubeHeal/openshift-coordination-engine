# Integration Tests

Integration tests verify the coordination engine's behavior against a real Kubernetes cluster.

## Prerequisites

- Running Kubernetes or OpenShift cluster
- `KUBECONFIG` environment variable pointing to cluster configuration
- Sufficient permissions to create/read namespaces, deployments, pods, etc.

## Running Integration Tests

```bash
# Set KUBECONFIG if not already set
export KUBECONFIG=~/.kube/config

# Run integration tests
make test-integration
```

## Test Coverage

Current integration tests verify:

- [ ] Kubernetes cluster connectivity
- [ ] Deployment detection (ArgoCD, Helm, Operator, Manual)
- [ ] Multi-layer coordination (Infrastructure, Platform, Application)
- [ ] ML service integration
- [ ] Remediation workflows

## Adding New Tests

Integration tests should:

1. Use the `// +build integration` build tag
2. Extend `IntegrationTestSuite` for common setup
3. Clean up any resources created during testing
4. Be idempotent and safe to run multiple times

Example:

```go
// +build integration

package integration

import "testing"

func (s *IntegrationTestSuite) TestMyFeature() {
    // Your test code here
}
```
