# RBAC Configuration and Requirements

This document describes the RBAC (Role-Based Access Control) requirements for the OpenShift Coordination Engine.

## Overview

The Coordination Engine requires specific Kubernetes RBAC permissions to function properly. These permissions enable the engine to:

- Monitor workloads across infrastructure, platform, and application layers
- Detect deployment methods (ArgoCD, Helm, Operator, Manual)
- Coordinate remediation actions across multiple layers
- Integrate with OpenShift-specific resources (MCO, operators)

## ServiceAccount

**Name**: `self-healing-operator`
**Namespace**: `self-healing-platform` (default, configurable)

The coordination engine runs under this ServiceAccount, which must have the permissions defined below.

## Required Permissions

### Core API Resources

#### Full Access (get, list, watch, create, update, patch, delete)
- **pods**: Monitor and remediate pod issues
- **services**: Manage service configurations
- **configmaps**: Read and update configuration
- **secrets**: Access sensitive configuration (use with caution)
- **events**: Create event records for audit trail

#### Read-Only Access (get, list, watch)
- **namespaces**: Discover available namespaces
- **nodes**: Monitor node health status
- **endpoints**: Track service endpoints
- **persistentvolumes**: Monitor storage resources
- **persistentvolumeclaims**: Track storage claims

#### Leader Election
- **leases**: Required for high-availability deployments with leader election

### Apps API Resources

Full access to manage application workloads:
- **deployments**: Primary workload management
- **replicasets**: Deployment version tracking
- **daemonsets**: Node-level workload management
- **statefulsets**: Stateful application management

### Batch API Resources

Full access to batch workloads:
- **jobs**: One-time task execution
- **cronjobs**: Scheduled task management

### Monitoring Resources

Full access for observability:
- **servicemonitors** (monitoring.coreos.com): Prometheus metrics collection
- **prometheusrules** (monitoring.coreos.com): Alert rule management

### ArgoCD Resources

Read-only access for deployment detection:
- **applications** (argoproj.io): Detect ArgoCD-managed deployments

**Rationale**: The coordination engine needs to detect if a workload is managed by ArgoCD to determine the appropriate remediation strategy (trigger ArgoCD sync vs. direct Kubernetes changes).

### OpenShift Machine Configuration

Read-only access for infrastructure monitoring:
- **machineconfigs** (machineconfiguration.openshift.io): Monitor node configuration
- **machineconfigpools** (machineconfiguration.openshift.io): Track MCO rollout status

**Rationale**: Required for multi-layer coordination to detect infrastructure-level issues and coordinate with MCO updates.

### OpenShift Operator Resources

Read-only access:
- **clusteroperators** (operator.openshift.io, config.openshift.io): Monitor platform operator health

**Rationale**: Enables platform-layer coordination and health checks.

## RBAC Manifests

The RBAC resources are defined in the Helm chart:

- **Role**: `charts/coordination-engine/templates/role.yaml`
- **RoleBinding**: `charts/coordination-engine/templates/rolebinding.yaml`
- **ServiceAccount**: `charts/coordination-engine/templates/serviceaccount.yaml`

These are automatically created when deploying via Helm.

## Verification

### Automated Verification

Use the provided script to verify all RBAC permissions:

```bash
./scripts/verify-rbac.sh [namespace]
```

**Example Output**:
```
======================================
RBAC Verification Script
======================================
Namespace: self-healing-platform
ServiceAccount: self-healing-operator

Checking RBAC permissions...

Core API Resources:
-------------------
✓ get pods
✓ list pods
...

======================================
Summary
======================================
Total Checks: 37
Allowed: 37
Denied: 0

✅ All RBAC permissions verified successfully!
```

### Manual Verification

Check individual permissions using `kubectl auth can-i`:

```bash
# Check pod read permission
kubectl auth can-i get pods \
  --as=system:serviceaccount:self-healing-platform:self-healing-operator \
  -n self-healing-platform

# Check ArgoCD application read permission
kubectl auth can-i get applications.argoproj.io \
  --as=system:serviceaccount:self-healing-platform:self-healing-operator \
  -n self-healing-platform
```

### Application-Level Verification

The coordination engine performs automatic RBAC checks at startup:

```go
rbacVerifier := rbac.NewVerifier(k8sClient, namespace, log)
if err := rbacVerifier.CheckCriticalPermissions(ctx); err != nil {
    log.Fatal("Critical RBAC permissions missing")
}
```

**Critical permissions checked at startup**:
- pods: get, list
- deployments: get, list
- events: create

If critical permissions are missing, the application will fail to start with a clear error message.

## Troubleshooting

### Problem: Application fails to start with RBAC errors

**Symptom**:
```
FATAL: Critical RBAC permissions missing - cannot start
```

**Solution**:
1. Run the verification script: `./scripts/verify-rbac.sh`
2. Check if the Role and RoleBinding exist:
   ```bash
   oc get role self-healing-operator -n self-healing-platform
   oc get rolebinding self-healing-operator -n self-healing-platform
   ```
3. Re-deploy the Helm chart to create RBAC resources:
   ```bash
   helm upgrade coordination-engine ./charts/coordination-engine -n self-healing-platform
   ```

### Problem: ArgoCD permissions denied

**Symptom**:
```
✗ get applications (argoproj.io)
✗ list applications (argoproj.io)
```

**Solution**:
The ArgoCD CRDs must be installed in the cluster. Verify:
```bash
kubectl get crd applications.argoproj.io
```

If ArgoCD is not installed, the permission checks will fail. The coordination engine will still function but won't be able to detect ArgoCD-managed deployments.

### Problem: MachineConfig permissions denied

**Symptom**:
```
✗ get machineconfigpools (machineconfiguration.openshift.io)
```

**Solution**:
MachineConfigPool resources are OpenShift-specific. If running on vanilla Kubernetes, these permissions are not required and can be safely ignored. Update the Role to remove these permissions if not using OpenShift.

### Problem: Permission checks pass but application still fails

**Symptom**:
Verification script shows all permissions allowed, but the application logs permission errors.

**Solution**:
1. Verify the ServiceAccount is correctly specified in the Deployment:
   ```bash
   oc get deployment coordination-engine -n self-healing-platform -o jsonpath='{.spec.template.spec.serviceAccountName}'
   ```
   Should output: `self-healing-operator`

2. Check pod logs for specific permission errors:
   ```bash
   oc logs -f deployment/coordination-engine -n self-healing-platform
   ```

3. Verify the RoleBinding subject matches the ServiceAccount:
   ```bash
   oc get rolebinding self-healing-operator -n self-healing-platform -o yaml
   ```

## Security Considerations

### Principle of Least Privilege

The RBAC configuration follows the principle of least privilege:

1. **Namespace-scoped**: Role (not ClusterRole) limits access to the deployment namespace
2. **Resource-specific**: Only required resources are granted access
3. **Verb-specific**: Read-only access where possible (ArgoCD, MCO, operators)
4. **Explicit permissions**: No wildcard resources or verbs

### Sensitive Resources

**Secrets Access**: The coordination engine requires `get` access to secrets to read configuration. Ensure:
- Secrets contain only non-sensitive configuration
- Use external secret management (Vault, Sealed Secrets) for sensitive data
- Audit secret access through Kubernetes audit logs

### Audit and Compliance

Enable Kubernetes audit logging to track coordination engine actions:
```yaml
apiVersion: audit.k8s.io/v1
kind: Policy
rules:
  - level: RequestResponse
    users: ["system:serviceaccount:self-healing-platform:self-healing-operator"]
```

## References

- [ADR-006: RBAC and Kubernetes Client Configuration](../docs/adrs/ADR-006-rbac-kubernetes-client.md)
- [ADR-033: Platform RBAC Permissions](/home/lab-user/openshift-aiops-platform/docs/adrs/ADR-033-*.md)
- [Kubernetes RBAC Documentation](https://kubernetes.io/docs/reference/access-authn-authz/rbac/)
- [OpenShift RBAC](https://docs.openshift.com/container-platform/latest/authentication/using-rbac.html)
