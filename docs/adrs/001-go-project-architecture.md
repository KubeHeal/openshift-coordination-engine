# ADR-001: Go Project Architecture and Standards

## Status
ACCEPTED - 2025-12-18

## Context

This repository implements the Go-based Coordination Engine as defined in platform ADR-042. We need to establish clear architectural standards, project layout, coding conventions, and development practices to ensure consistency, maintainability, and alignment with the Kubernetes ecosystem.

Following the success of the Go-based MCP server (platform ADR-036), we adopt similar patterns and best practices for this coordination engine implementation.

## Decision

### 1. Go Version and Dependencies

**Go Version**: 1.21+
- Minimum version: 1.21 (required for standard library features)
- Recommended: 1.22+ for improved performance

**Core Dependencies**:
```go
// go.mod
module openshift-coordination-engine

go 1.21

require (
    // Kubernetes client
    k8s.io/client-go v0.29.0
    k8s.io/api v0.29.0
    k8s.io/apimachinery v0.29.0

    // HTTP routing and web
    github.com/gorilla/mux v1.8.1

    // Logging
    github.com/sirupsen/logrus v1.9.3

    // Metrics
    github.com/prometheus/client_golang v1.18.0

    // Testing
    github.com/stretchr/testify v1.8.4
    github.com/onsi/ginkgo/v2 v2.13.0
    github.com/onsi/gomega v1.30.0

    // Utilities
    github.com/google/uuid v1.5.0
)
```

### 2. Project Layout

Follow the **Standard Go Project Layout** with Kubernetes-specific conventions:

```
openshift-coordination-engine/
├── cmd/
│   └── coordination-engine/
│       └── main.go                    # Application entry point
│
├── internal/                          # Private application code
│   ├── detector/
│   │   ├── deployment_detector.go     # ADR-002: Deployment detection
│   │   ├── deployment_detector_test.go
│   │   ├── layer_detector.go          # ADR-003: Layer detection
│   │   └── layer_detector_test.go
│   │
│   ├── coordination/
│   │   ├── planner.go                 # ADR-003: Multi-layer planner
│   │   ├── planner_test.go
│   │   ├── orchestrator.go            # ADR-003: Workflow orchestrator
│   │   ├── orchestrator_test.go
│   │   ├── health_checker.go          # ADR-003: Health checking
│   │   └── health_checker_test.go
│   │
│   ├── remediation/
│   │   ├── strategy_selector.go       # ADR-005: Strategy selection
│   │   ├── strategy_selector_test.go
│   │   ├── argocd_remediator.go       # ADR-004: ArgoCD remediation
│   │   ├── helm_remediator.go         # ADR-005: Helm remediation
│   │   ├── operator_remediator.go     # ADR-005: Operator remediation
│   │   ├── manual_remediator.go       # ADR-005: Manual remediation
│   │   └── interfaces.go              # Remediator interface definitions
│   │
│   └── integrations/
│       ├── argocd_client.go           # ADR-004: ArgoCD API client
│       ├── argocd_client_test.go
│       ├── mco_client.go              # ADR-004: MCO client
│       ├── mco_client_test.go
│       ├── ml_service_client.go       # ADR-009: Python ML client
│       └── ml_service_client_test.go
│
├── pkg/                               # Public API and models
│   ├── api/
│   │   └── v1/
│   │       ├── remediation.go         # ADR-011: Remediation handlers
│   │       ├── detection.go           # Detection API handlers
│   │       ├── health.go              # Health check handlers
│   │       ├── middleware.go          # HTTP middleware
│   │       └── handlers_test.go
│   │
│   └── models/
│       ├── deployment_info.go         # Deployment detection models
│       ├── layered_issue.go           # Multi-layer coordination models
│       ├── remediation_plan.go        # Remediation plan models
│       └── workflow.go                # Workflow execution models
│
├── test/
│   ├── integration/                   # Integration tests
│   │   ├── argocd_integration_test.go
│   │   ├── mco_integration_test.go
│   │   └── ml_service_integration_test.go
│   │
│   ├── e2e/                           # End-to-end tests
│   │   └── remediation_e2e_test.go
│   │
│   └── fixtures/                      # Test fixtures and mocks
│       ├── mock_kubernetes.go
│       ├── mock_argocd.go
│       └── mock_ml_service.go
│
├── charts/
│   └── coordination-engine/           # Helm chart
│       ├── Chart.yaml
│       ├── values.yaml
│       └── templates/
│
├── docs/
│   └── adrs/                          # Architectural Decision Records
│
├── .github/
│   └── workflows/
│       ├── ci.yaml                    # CI pipeline
│       └── release.yaml               # Release pipeline
│
├── Dockerfile                         # Container image build
├── Makefile                           # Build automation
├── go.mod                             # Go module definition
├── go.sum                             # Dependency checksums
├── .golangci.yml                      # Linter configuration
├── README.md
├── CLAUDE.md                          # Claude Code instructions
├── API-CONTRACT.md                    # API specification
└── DEVELOPMENT.md                     # Development guide
```

### 3. Package Organization Principles

**internal/** - Private Implementation
- Never imported by external projects
- Contains business logic and internal implementations
- Organized by functional domain (detector, coordination, remediation, integrations, storage)
  - `storage/`: In-memory and persistent data stores for incidents and workflows (ADR-014)

**pkg/** - Public API
- Can be imported by other Go projects
- Minimal, stable interfaces
- API handlers and models only

**cmd/** - Application Entry Points
- Minimal code, mostly wiring
- Parse flags, initialize clients, start server

### 4. Code Style and Conventions

#### Naming Conventions

```go
// Package names: lowercase, single word
package detector

// Interfaces: noun or adjective
type Remediator interface {}
type Detectable interface {}

// Implementations: noun describing what it does
type ArgoCDRemediator struct {}
type DeploymentDetector struct {}

// Functions: verb describing action
func DetectDeploymentMethod() {}
func TriggerRemediation() {}

// Constants: CamelCase or ALL_CAPS for exported
const DefaultTimeout = 30 * time.Second
const MAX_RETRIES = 3
```

#### Error Handling

```go
// Always wrap errors with context
import "fmt"

func (d *DeploymentDetector) Detect(namespace, name string) (*DeploymentInfo, error) {
    pod, err := d.client.GetPod(namespace, name)
    if err != nil {
        return nil, fmt.Errorf("failed to get pod %s/%s: %w", namespace, name, err)
    }

    // ... rest of implementation
}

// Check errors immediately
if err != nil {
    return nil, err
}
```

#### Logging Standards

```go
// Use structured logging with logrus
import "github.com/sirupsen/logrus"

log := logrus.WithFields(logrus.Fields{
    "namespace": namespace,
    "pod":       podName,
    "method":    "DetectDeploymentMethod",
})

log.Info("Detecting deployment method")
log.WithField("result", result).Debug("Detection complete")
log.WithError(err).Error("Detection failed")
```

#### Context Usage

```go
// Always accept context as first parameter
func (c *ArgoCDClient) SyncApplication(ctx context.Context, appName string) error {
    // Use context for cancellation and timeouts
    req, err := http.NewRequestWithContext(ctx, "POST", url, body)
    if err != nil {
        return err
    }

    // ... rest of implementation
}

// Create context with timeout for external calls
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

result, err := client.SyncApplication(ctx, "my-app")
```

### 5. Testing Standards

#### Unit Tests

```go
// Test file naming: *_test.go
// Test function naming: TestFunctionName_Scenario

func TestDeploymentDetector_DetectFromAnnotations_ArgoCD(t *testing.T) {
    // Arrange
    detector := NewDeploymentDetector(mockK8sClient)
    annotations := map[string]string{
        "argocd.argoproj.io/tracking-id": "app:Deployment:ns/name",
    }

    // Act
    result, err := detector.DetectFromMetadata("ns", "name", nil, annotations)

    // Assert
    assert.NoError(t, err)
    assert.Equal(t, DeploymentMethodArgoCD, result.Method)
    assert.True(t, result.Managed)
    assert.Greater(t, result.Confidence, 0.9)
}
```

#### Table-Driven Tests

```go
func TestDeploymentDetector_DetectFromMetadata(t *testing.T) {
    tests := []struct {
        name        string
        annotations map[string]string
        labels      map[string]string
        want        DeploymentMethod
        wantManaged bool
    }{
        {
            name:        "ArgoCD tracking annotation",
            annotations: map[string]string{"argocd.argoproj.io/tracking-id": "app:Deployment:ns/name"},
            want:        DeploymentMethodArgoCD,
            wantManaged: true,
        },
        {
            name:        "Helm release annotation",
            annotations: map[string]string{"meta.helm.sh/release-name": "my-release"},
            want:        DeploymentMethodHelm,
            wantManaged: false,
        },
        // ... more test cases
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := detector.DetectFromMetadata("ns", "name", tt.labels, tt.annotations)
            assert.NoError(t, err)
            assert.Equal(t, tt.want, result.Method)
            assert.Equal(t, tt.wantManaged, result.Managed)
        })
    }
}
```

#### BDD-Style Tests (Ginkgo/Gomega)

```go
import (
    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"
)

var _ = Describe("DeploymentDetector", func() {
    var detector *DeploymentDetector

    BeforeEach(func() {
        detector = NewDeploymentDetector(mockK8sClient)
    })

    Context("when detecting ArgoCD deployments", func() {
        It("should detect from tracking annotation", func() {
            annotations := map[string]string{
                "argocd.argoproj.io/tracking-id": "app:Deployment:ns/name",
            }

            result, err := detector.DetectFromMetadata("ns", "name", nil, annotations)

            Expect(err).ToNot(HaveOccurred())
            Expect(result.Method).To(Equal(DeploymentMethodArgoCD))
            Expect(result.Confidence).To(BeNumerically(">", 0.9))
        })
    })
})
```

### 6. Build and Development Tools

#### Makefile Targets

```makefile
.PHONY: build test lint fmt vet

# Build binary
build:
    go build -o bin/coordination-engine ./cmd/coordination-engine

# Run all tests
test:
    go test -v -race -coverprofile=coverage.out ./...

# Run linter
lint:
    golangci-lint run ./...

# Format code
fmt:
    gofmt -s -w .
    goimports -w .

# Vet code
vet:
    go vet ./...

# CI pipeline (run all checks)
ci: fmt vet lint test build

# Run locally
run:
    go run ./cmd/coordination-engine

# Generate coverage report
coverage:
    go test -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
```

#### GolangCI-Lint Configuration

```yaml
# .golangci.yml
linters:
  enable:
    - gofmt
    - govet
    - errcheck
    - staticcheck
    - gosimple
    - ineffassign
    - unused
    - misspell
    - goconst
    - gocyclo

linters-settings:
  gocyclo:
    min-complexity: 15
  goconst:
    min-len: 3
    min-occurrences: 3
```

### 7. Dependency Management

```bash
# Add new dependency
go get github.com/example/package

# Update dependencies
go get -u ./...

# Tidy dependencies
go mod tidy

# Vendor dependencies (optional)
go mod vendor
```

## Consequences

### Positive
- ✅ **Consistency**: Clear standards for all Go code
- ✅ **Maintainability**: Standard project layout easy to navigate
- ✅ **Quality**: Automated linting and testing enforce code quality
- ✅ **Kubernetes Native**: Follows Go Kubernetes ecosystem conventions
- ✅ **Testability**: Clear separation of concerns enables comprehensive testing

### Negative
- ⚠️ **Learning Curve**: Team must learn Go idioms and conventions
- ⚠️ **Tooling Setup**: Requires golangci-lint, goimports, ginkgo installation
- ⚠️ **Migration Effort**: Python developers need Go training

### Mitigation
- Provide Go training and pair programming sessions
- Document common patterns and examples in DEVELOPMENT.md
- Use pre-commit hooks to enforce standards automatically

## References

- Go Standard Project Layout: https://github.com/golang-standards/project-layout
- Kubernetes client-go examples: https://github.com/kubernetes/client-go/tree/master/examples
- Go Code Review Comments: https://github.com/golang/go/wiki/CodeReviewComments
- Effective Go: https://go.dev/doc/effective_go
- Platform ADR-036: Go-Based Standalone MCP Server (reference implementation)
- Platform ADR-042: Go-Based Coordination Engine (overall architecture)

## Related ADRs

- ADR-009: Python ML Service Integration (Go HTTP client implementation)
- ADR-011: MCP Server Integration (HTTP server and API implementation)
- ADR-014: Prometheus/Thanos Observability and Incident Management (storage package for incident persistence)
- Platform ADR-042: Go-Based Coordination Engine (architecture context)
