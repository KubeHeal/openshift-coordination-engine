# Security Policy

## Supported Versions

The Coordination Engine follows a rolling 3-version support window aligned with OpenShift releases.

### Release Branches

| Branch | OCP Version | Kubernetes | client-go | Go | Status |
|--------|-------------|------------|-----------|-----|--------|
| `release-4.22` | 4.22 | 1.35 | v0.35.0 | 1.26 | :white_check_mark: Current |
| `release-4.21` | 4.21 | 1.34 | v0.34.0 | 1.26 | :white_check_mark: Supported |
| `release-4.20` | 4.20 | 1.33 | v0.33.0 | 1.26 | :white_check_mark: Supported |
| `release-4.19` | 4.19 | 1.32 | v0.32.0 | 1.23 | :x: End of life |
| `release-4.18` | 4.18 | 1.31 | v0.31.3 | 1.23 | :x: End of life |

### Container Images

| Image Tag | Source Branch | Security Updates |
|-----------|-------------|------------------|
| `ocp-4.22-latest` | `release-4.22` | :white_check_mark: Active |
| `ocp-4.21-latest` | `release-4.21` | :white_check_mark: Active |
| `ocp-4.20-latest` | `release-4.20` | :white_check_mark: Critical only |

### Engine Versions

| Engine Version | Status | Notes |
|----------------|--------|-------|
| 1.2.x | :white_check_mark: Supported | Current release |
| 1.1.x | :x: End of life | Upgrade to 1.2.x |
| < 1.1 | :x: End of life | Upgrade to 1.2.x |

## Dependency Versions

### Go Runtime

The engine requires **Go 1.26+**. Each release branch is tested against the Go version specified in `go.mod`.

Security vulnerabilities in the Go standard library are addressed by updating the Go toolchain version. The CI pipeline runs `govulncheck` to detect reachable vulnerabilities.

### Kubernetes Client Libraries

Each release branch pins `k8s.io/client-go` to the version that matches the target OpenShift/Kubernetes release. These dependencies are **not** updated by Dependabot to prevent version drift.

| Release Branch | client-go | Kubernetes API |
|---------------|-----------|----------------|
| `release-4.22` | v0.35.0 | 1.35 |
| `release-4.21` | v0.34.0 | 1.34 |
| `release-4.20` | v0.33.0 | 1.33 |

### Container Base Image

The runtime image uses `registry.access.redhat.com/ubi9/ubi-minimal:latest`. Security patches are applied at build time via `microdnf update -y`. Dependabot monitors the base image for updates.

## Security Scanning

The CI pipeline runs three security tools on every push and pull request:

| Tool | What It Checks | SARIF Upload |
|------|---------------|--------------|
| **govulncheck** | Reachable Go vulnerabilities (call graph analysis) | No (fails build) |
| **gosec** | Go security anti-patterns (injection, hardcoded secrets, weak crypto) | No (fails build) |
| **Trivy** | All known CVEs in Go dependencies and filesystem | Yes (GitHub Security tab) |

A scheduled Trivy image scan runs weekly (Mondays) against the latest published container image to catch newly disclosed CVEs in base image layers.

Results are visible on the [Security tab](https://github.com/KubeHeal/openshift-coordination-engine/security/code-scanning).

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

### Private Reporting

1. Go to the [Security Advisories](https://github.com/KubeHeal/openshift-coordination-engine/security/advisories) page.
2. Click **"Report a vulnerability"**.
3. Provide the following information:
   - Description of the vulnerability
   - Steps to reproduce
   - Affected versions and branches
   - Impact assessment (if known)

### What to Expect

- **Acknowledgment**: Within 3 business days.
- **Initial assessment**: Within 7 business days.
- **Fix timeline**: Depends on severity.
  - **Critical**: Patch within 7 days, published to all supported branches.
  - **High**: Patch within 14 days.
  - **Medium/Low**: Included in the next scheduled release.
- **Disclosure**: Coordinated disclosure after the fix is available on all supported branches.

### Scope

The following are in scope for security reports:

- Vulnerabilities in the Coordination Engine code (`cmd/`, `internal/`, `pkg/`)
- Authentication or authorization bypass in the REST API
- Information disclosure through API responses or logs
- Denial of service through API endpoints
- Container image vulnerabilities in the published images
- Insecure default configuration

The following are out of scope:

- Vulnerabilities in upstream dependencies that are not reachable (use `govulncheck` to verify)
- Vulnerabilities in the KServe, ArgoCD, or Prometheus services the engine integrates with
- Issues in the MCP server or ML service (report those to their respective repositories)

## Security Best Practices for Deployers

1. **Run with least privilege.** The Helm chart creates a Role (not ClusterRole) with the minimum permissions needed. Do not grant additional RBAC without reviewing the impact.
2. **Enable network policies.** Restrict ingress to the engine pod to only the MCP server and monitoring services.
3. **Use persistent storage encryption.** If `persistence.enabled=true`, use an encrypted StorageClass for the incident data PVC.
4. **Rotate alert sink credentials.** Slack webhook URLs, PagerDuty routing keys, and Alertmanager URLs should be stored in Kubernetes Secrets, not in values files.
5. **Pin image tags.** In production, use SHA-tagged images (for example, `ocp-4.22-a1b2c3d`) instead of `-latest` tags.
6. **Review RBAC regularly.** Run `kubectl auth can-i --list --as=system:serviceaccount:self-healing-platform:self-healing-operator` to audit effective permissions.
