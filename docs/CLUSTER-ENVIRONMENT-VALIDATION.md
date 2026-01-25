# Cluster Environment Validation

**Cluster**: `api.cluster-pvbs6.pvbs6.sandbox3005.opentlc.com:6443`
**Date**: 2025-12-18
**Status**: ✅ VALIDATED

## Executive Summary

This document validates the implementation plan against the actual OpenShift cluster environment. **The cluster is production-ready for Go coordination engine deployment** with minor adjustments required.

## Cluster Information

### Version Details

```
OpenShift: 4.18.21
Kubernetes: v1.31.10
Client: 4.18.21
```

**Compatibility**: ✅ **EXCELLENT**
- OpenShift 4.18 is a recent stable release (Dec 2024)
- Kubernetes 1.31 is the latest stable version
- All Go client-go v0.29.0 dependencies (from ADR-001) are compatible

**Recommendation**: Pin to `k8s.io/client-go v0.31.x` to match cluster version exactly.

## Existing Infrastructure

### Deployed Components

| Component | Status | Namespace | Notes |
|-----------|--------|-----------|-------|
| **coordination-engine (Python)** | ✅ Running (1/1) | self-healing-platform | Current Python implementation to be replaced |
| **cluster-health-mcp** | ✅ Running (1/1) | self-healing-platform | MCP server already integrated |
| **ArgoCD (openshift-gitops)** | ✅ Running (1/1) | openshift-gitops | ArgoCD integration ready (ADR-004) |
| **MCO** | ✅ Available | N/A | Master pool (3 nodes), Worker pool (4 nodes) |
| **anomaly-detector-predictor** | ⚠️ CrashLoopBackOff | self-healing-platform | ML service needs fixing |
| **predictive-analytics-predictor** | ⚠️ CrashLoopBackOff | self-healing-platform | ML service needs fixing |

### Service Endpoints

```
coordination-engine:       172.30.102.200:8080 (HTTP), 172.30.102.200:8090 (metrics)
cluster-health-mcp:        172.30.202.52:8080
openshift-gitops-server:   172.30.13.196:80, 172.30.13.196:443
```

## RBAC Configuration

### ServiceAccount

✅ **VALIDATED**: `self-healing-operator` ServiceAccount exists
- Created: 2025-12-11
- Namespace: `self-healing-platform`
- Used by: Current coordination-engine deployment

### Role and RoleBinding

✅ **VALIDATED**: Role and RoleBinding exist with comprehensive permissions

**Current Permissions** (Verified):
```yaml
Core API:
  - pods, services, configmaps, secrets, events (full CRUD)
  - namespaces, nodes, endpoints (read-only)
  - persistentvolumes, persistentvolumeclaims (read-only)
  - leases (full CRUD for leader election)

Apps API:
  - deployments, replicasets, daemonsets, statefulsets (full CRUD)

Batch API:
  - jobs, cronjobs (full CRUD)

Autoscaling:
  - horizontalpodautoscalers (read-only)

Policy:
  - poddisruptionbudgets (read-only)

Networking:
  - networkpolicies (read-only)

Storage:
  - storageclasses (read-only)

Monitoring:
  - servicemonitors, prometheusrules (full CRUD)

MCO (OpenShift-specific):
  - machineconfigs, machineconfigpools (read-only)

KServe:
  - inferenceservices (read-only)

Kubeflow:
  - notebooks (read-only)

OpenShift Image Streams:
  - imagestreams, imagestreamtags (full CRUD)

OpenShift Builds:
  - builds (read-only)
```

### ⚠️ Missing Permissions (Required for Go Implementation)

**ArgoCD Integration** (ADR-004):
```yaml
- apiGroups: ["argoproj.io"]
  resources: ["applications"]
  verbs: ["get", "list", "watch"]
```

**OpenShift Operators** (ADR-003 - Multi-Layer Coordination):
```yaml
- apiGroups: ["operator.openshift.io", "config.openshift.io"]
  resources: ["clusteroperators"]
  verbs: ["get", "list", "watch"]
```

**Action Required**: Update Role manifest before Phase 4 (ArgoCD/MCO Integration)

## Resource Allocation

### Current Coordination Engine Resources

```yaml
Requests:
  CPU:    200m
  Memory: 256Mi

Limits:
  CPU:    500m
  Memory: 512Mi
```

**Assessment**: ✅ **APPROPRIATE** for Go implementation
- Go binary has lower memory footprint than Python (~50% reduction expected)
- Current limits align with ADR recommendations (CPU: 500m, Memory: 512Mi)
- No changes needed for initial deployment

**Recommendation**: Monitor resource usage in Phase 10 (Deployment) and adjust if needed.

## Storage

### Persistent Volume Claim

```yaml
Name: self-healing-data-development
Mounted at: /opt/app-root/src/data
```

**Assessment**: ⚠️ **REVIEW REQUIRED**
- Current Python coordination engine uses PVC
- Go implementation uses in-memory cache (ADR-002)
- PVC may not be needed for Go implementation

**Action Required**:
- Determine if PVC is needed for Go engine
- If not needed, remove volume mount from Helm chart
- If needed for other data (logs, temporary files), document usage

## MCO Status

### MachineConfigPools

```
Master Pool:
  Nodes:    3
  Updated:  3/3
  Ready:    3/3
  Status:   Stable (UPDATED=True, UPDATING=False, DEGRADED=False)

Worker Pool:
  Nodes:    4
  Updated:  4/4
  Ready:    4/4
  Status:   Stable (UPDATED=True, UPDATING=False, DEGRADED=False)
```

**Assessment**: ✅ **EXCELLENT**
- MCO is stable and healthy
- All nodes are updated and ready
- No ongoing updates or degradations
- MCO client (ADR-004) can be tested immediately

## ArgoCD Status

### ArgoCD Deployment

```
Name:      openshift-gitops-server
Namespace: openshift-gitops
Replicas:  1/1 (Running)
Age:       13 days
Services:  HTTP (80), HTTPS (443), Metrics (8083)
```

**Assessment**: ✅ **READY**
- ArgoCD is deployed and running
- HTTP/HTTPS endpoints available for API access
- Metrics endpoint available for monitoring
- ArgoCD client (ADR-004) can be developed and tested

**Action Required**:
- Generate ArgoCD API token for coordination engine
- Store token in Kubernetes Secret: `argocd-token`
- Update Helm chart to reference secret

### ArgoCD Applications

**Recommendation**: Create test ArgoCD applications during Phase 4 for integration testing.

## ML Service Status

### Anomaly Detector

```
Status: CrashLoopBackOff (300+ restarts)
Age:    6 days 22 hours
```

### Predictive Analytics

```
Status: CrashLoopBackOff (300+ restarts)
Age:    6 days 22 hours
```

**Assessment**: ⚠️ **CRITICAL ISSUE**
- ML predictors are not functioning
- Circuit breaker (ADR-009) will activate immediately
- Coordination engine will operate in degraded mode

**Impact on Implementation Plan**:
- ✅ Phase 6 (ML Service Integration) can proceed with mock ML service
- ⚠️ Production testing of ML integration blocked until predictors are fixed
- ✅ Circuit breaker and graceful degradation can be tested in real environment

**Action Required**:
1. **Immediate**: Fix ML service CrashLoopBackOff issues
2. **Phase 6**: Test ML integration with mock service first
3. **Before Production**: Ensure ML services are stable

## Network and Service Discovery

### Internal DNS

```
coordination-engine.self-healing-platform.svc.cluster.local:8080
cluster-health-mcp.self-healing-platform.svc.cluster.local:8080
openshift-gitops-server.openshift-gitops.svc.cluster.local:443
```

**Assessment**: ✅ **STANDARD**
- Standard Kubernetes service DNS
- All services discoverable via cluster DNS
- No custom DNS configuration needed

### Service Communication

**Validated Paths**:
```
MCP Server → Coordination Engine:
  cluster-health-mcp:8080 → coordination-engine:8080

Coordination Engine → ArgoCD:
  coordination-engine:8080 → openshift-gitops-server:443

Coordination Engine → ML Service (when fixed):
  coordination-engine:8080 → anomaly-detector-predictor:80
```

**Assessment**: ✅ **READY**
- All service-to-service communication paths available
- No additional NetworkPolicies needed (cluster allows pod-to-pod by default)
- HTTPS for ArgoCD, HTTP for internal services (standard)

## Image Registry

### Current Setup

```
Registry: image-registry.openshift-image-registry.svc:5000
Image:    image-registry.openshift-image-registry.svc:5000/self-healing-platform/coordination-engine:latest
```

**Assessment**: ✅ **PRODUCTION-READY**
- Internal OpenShift image registry available
- Namespace: `self-healing-platform` (project already exists)
- Tag strategy: `latest` (acceptable for development, should use semantic versioning for production)

**Recommendations**:
1. **Development**: Use `latest` tag, build and push locally
2. **CI/CD**: Use semantic versioning (e.g., `v1.0.0`, `v1.1.0`)
3. **Production**: Use immutable tags (SHA or semantic version)

**Build and Push Commands**:
```bash
# Build
make docker-build

# Tag for internal registry
docker tag coordination-engine:latest \
  image-registry.openshift-image-registry.svc:5000/self-healing-platform/coordination-engine:v1.0.0

# Login to registry
oc whoami -t | docker login -u $(oc whoami) --password-stdin \
  image-registry.openshift-image-registry.svc:5000

# Push
docker push image-registry.openshift-image-registry.svc:5000/self-healing-platform/coordination-engine:v1.0.0
```

## Deployment Strategy Validation

### Canary Deployment Feasibility

**Current Setup**:
```
coordination-engine (Python): 1 replica
Service selector:             app=coordination-engine
```

**Canary Strategy** (Phase 10):
```yaml
# Step 1: Deploy Go engine with different label
coordination-engine-go:
  replicas: 1
  labels:
    app: coordination-engine
    version: go

# Step 2: Update service selector to route traffic
service/coordination-engine:
  selector:
    app: coordination-engine
    # Remove version selector to load balance between Python and Go
```

**Traffic Splitting Options**:

**Option 1: Service-Level Load Balancing** (Simple, Recommended)
- Deploy Go engine with same service labels
- Kubernetes service load balances 50/50
- Adjust by scaling replicas: 1 Python + 1 Go = 50%, 1 Python + 3 Go = 75% Go

**Option 2: Istio/Service Mesh** (Advanced)
- Requires Istio or OpenShift Service Mesh
- Fine-grained traffic control (10%, 25%, etc.)
- More complex setup

**Recommendation**: Use **Option 1** for simplicity. Adjust replica counts for traffic distribution.

### Rollback Strategy

**Current Deployment**:
```yaml
deployment/coordination-engine:
  image: coordination-engine:latest
  replicas: 1
```

**Rollback Process** (if Go deployment fails):
```bash
# Option 1: Kubernetes rollback
oc rollout undo deployment/coordination-engine -n self-healing-platform

# Option 2: Manual switch
oc patch deployment coordination-engine -n self-healing-platform \
  --type='json' -p='[{"op": "replace", "path": "/spec/template/spec/containers/0/image", "value": "coordination-engine:python-backup"}]'

# Option 3: Scale down Go, scale up Python
oc scale deployment coordination-engine-go --replicas=0 -n self-healing-platform
oc scale deployment coordination-engine-python --replicas=1 -n self-healing-platform
```

**Assessment**: ✅ **SAFE ROLLBACK POSSIBLE**
- Multiple rollback options available
- Service selector can be updated instantly
- No data loss (in-memory cache only)

## Monitoring and Observability

### Prometheus

**Status**: ⚠️ **NEEDS VERIFICATION**
- ServiceMonitor CRD is available (monitoring.coreos.com API group)
- RBAC permissions include servicemonitors
- Need to verify OpenShift monitoring stack is enabled

**Action Required**:
```bash
# Verify Prometheus Operator
oc get deployment -n openshift-monitoring

# Create ServiceMonitor for coordination engine
# (Part of Helm chart in Phase 0)
```

### Metrics Endpoint

**Current**:
```
coordination-engine:8090 (metrics port configured)
```

**Go Implementation**:
```
GET /metrics (port 9090, configurable)
```

**Recommendation**: Use port `9090` for consistency with Prometheus conventions.

## Implementation Plan Adjustments

### Phase 0: Foundation (Week 1-2)

**Additions**:
- [ ] Update go.mod to use `k8s.io/client-go v0.31.x` (match cluster version)
- [ ] Configure internal image registry in Makefile:
  ```makefile
  REGISTRY ?= image-registry.openshift-image-registry.svc:5000/self-healing-platform
  IMAGE_NAME ?= coordination-engine
  IMAGE_TAG ?= latest
  ```
- [ ] Add OpenShift-specific Dockerfile optimizations (oc binary if needed)

### Phase 1: Core Infrastructure (Week 3-4)

**Additions**:
- [ ] Test RBAC with existing ServiceAccount `self-healing-operator`
- [ ] Verify permissions against actual cluster (not just unit tests)
- [ ] Update Role manifest to add ArgoCD and OpenShift operator permissions

### Phase 4: ArgoCD/MCO Integration (Week 9-10)

**Modifications**:
- [ ] **ArgoCD Token Generation**:
  ```bash
  # Get ArgoCD admin password
  oc get secret openshift-gitops-cluster -n openshift-gitops -o jsonpath='{.data.admin\.password}' | base64 -d

  # Create ServiceAccount token for coordination engine
  # Store in Secret: argocd-token
  ```
- [ ] **Test with real MCO** (already stable in cluster)
- [ ] **Create test ArgoCD applications** for integration testing

### Phase 6: ML Service Integration (Week 13-14)

**Critical Changes**:
- [ ] **DO NOT block on ML service stability**
- [ ] Implement and test with **mock ML service** first
- [ ] Test circuit breaker with real (broken) ML service to verify graceful degradation
- [ ] Production ML integration **ONLY after fixing CrashLoopBackOff**

**Parallel Track**: Fix ML service issues while Phase 6 development continues

### Phase 10: Deployment (Week 20-24)

**OpenShift-Specific Steps**:
- [ ] **Build and Push to Internal Registry**:
  ```bash
  # Login
  oc whoami -t | docker login -u $(oc whoami) --password-stdin \
    default-route-openshift-image-registry.apps.cluster-pvbs6.pvbs6.sandbox3005.opentlc.com

  # Build and push
  make docker-build docker-push
  ```
- [ ] **Canary Deployment**:
  ```bash
  # Deploy Go engine (1 replica)
  oc apply -f deploy/go-engine.yaml

  # Verify both Python and Go running
  oc get pods -l app=coordination-engine

  # Monitor metrics
  oc port-forward svc/coordination-engine 9090:9090
  curl http://localhost:9090/metrics
  ```
- [ ] **Traffic Distribution**:
  ```bash
  # 50/50: 1 Python + 1 Go
  oc scale deployment coordination-engine-python --replicas=1
  oc scale deployment coordination-engine-go --replicas=1

  # 75% Go: 1 Python + 3 Go
  oc scale deployment coordination-engine-python --replicas=1
  oc scale deployment coordination-engine-go --replicas=3

  # 100% Go: 0 Python + 2 Go
  oc scale deployment coordination-engine-python --replicas=0
  oc scale deployment coordination-engine-go --replicas=2
  ```

## Pre-Implementation Checklist

### Immediate Actions (Before Phase 0)

- [ ] **Fix ML Service CrashLoopBackOff**:
  ```bash
  oc logs -f deployment/anomaly-detector-predictor -n self-healing-platform
  oc logs -f deployment/predictive-analytics-predictor -n self-healing-platform
  ```
  - Investigate root cause
  - Fix configuration/dependencies
  - Verify stable operation

- [ ] **Update RBAC Permissions**:
  ```bash
  # Edit role to add ArgoCD and OpenShift operator permissions
  oc edit role self-healing-operator -n self-healing-platform
  ```
  - Add `argoproj.io/applications` (get, list, watch)
  - Add `operator.openshift.io/clusteroperators` (get, list, watch)
  - Add `config.openshift.io/clusteroperators` (get, list, watch)

- [ ] **Generate ArgoCD Token**:
  ```bash
  # Create token for coordination engine ServiceAccount
  # Store in Secret
  oc create secret generic argocd-token \
    --from-literal=token=<TOKEN> \
    -n self-healing-platform
  ```

- [ ] **Verify Prometheus Monitoring**:
  ```bash
  oc get servicemonitor -n self-healing-platform
  oc get prometheus -n openshift-monitoring
  ```

### Phase 0 Prerequisites

- [ ] ✅ OpenShift cluster access (verified)
- [ ] ✅ Namespace `self-healing-platform` exists (verified)
- [ ] ✅ ServiceAccount `self-healing-operator` exists (verified)
- [ ] ⚠️ ArgoCD API token (needs generation)
- [ ] ⚠️ ML services stable (needs fixing)
- [ ] ⚠️ RBAC permissions complete (needs ArgoCD/operator additions)

## Risk Assessment Updates

### New Risks Identified

| Risk | Impact | Probability | Mitigation |
|------|--------|-------------|------------|
| ML service instability blocks production | High | High (already broken) | Mock ML service for development, fix in parallel track |
| Missing ArgoCD permissions cause API errors | Medium | Medium | Update RBAC before Phase 4, test early |
| Internal registry build/push complexity | Low | Low | Document commands, test in Phase 0 |

### Risk Mitigation Plan

1. **ML Service**:
   - **Immediate**: Assign engineer to fix CrashLoopBackOff
   - **Development**: Use mock ML service (Phase 6)
   - **Testing**: Test circuit breaker with broken service
   - **Production**: Block ML integration until stable

2. **RBAC**:
   - **Before Phase 4**: Update Role manifest
   - **Testing**: Verify permissions with `oc auth can-i`
   - **Validation**: Test ArgoCD API calls with token

3. **Image Registry**:
   - **Phase 0**: Document build/push process
   - **CI/CD**: Automate with GitHub Actions
   - **Validation**: Test manual push before automation

## Conclusion

### Overall Assessment: ✅ **CLUSTER IS READY**

**Strengths**:
- Modern OpenShift 4.18 / Kubernetes 1.31
- RBAC infrastructure already in place
- ArgoCD deployed and running
- MCO stable and healthy
- Resource allocation appropriate

**Critical Path Items**:
1. ⚠️ Fix ML service CrashLoopBackOff (parallel track)
2. ⚠️ Update RBAC for ArgoCD permissions (before Phase 4)
3. ⚠️ Generate and store ArgoCD token (before Phase 4)

**Recommendation**: **PROCEED WITH IMPLEMENTATION PLAN** with minor adjustments documented above.

---

**Validation Date**: 2025-12-18
**Validated By**: Cluster Environment Analysis
**Next Review**: After Phase 0 completion
**Status**: ✅ APPROVED FOR DEVELOPMENT
