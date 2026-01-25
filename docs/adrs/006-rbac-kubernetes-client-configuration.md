# ADR-006: RBAC and Kubernetes Client Configuration

## Status
ACCEPTED - 2025-12-18

## Context

This ADR defines the Go implementation of Kubernetes client initialization and RBAC permissions as outlined in platform ADR-033 (Coordination Engine RBAC Permissions). The coordination engine requires proper authentication, authorization, and Kubernetes API access to perform detection, coordination, and remediation operations.

Platform ADR-033 establishes:
- ServiceAccount: `self-healing-operator`
- Required permissions for multi-layer coordination (infrastructure, platform, application)
- ArgoCD and MCO integration permissions
- Namespace-scoped roles for security

Without proper RBAC configuration, the engine receives HTTP 403 Forbidden errors when accessing Kubernetes APIs.

## Decision

Implement Kubernetes client initialization and RBAC configuration in Go with the following components:

### 1. Kubernetes Client Initialization

Package: `cmd/coordination-engine/main.go`

```go
package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"openshift-coordination-engine/internal/coordination"
	"openshift-coordination-engine/internal/detector"
	"openshift-coordination-engine/internal/integrations"
	"openshift-coordination-engine/internal/remediation"
	"openshift-coordination-engine/pkg/api/v1"
)

func main() {
	log := logrus.New()
	log.SetFormatter(&logrus.JSONFormatter{})
	log.SetLevel(logrus.InfoLevel)

	// Initialize Kubernetes client
	clientset, dynamicClient, err := initializeKubernetesClient(log)
	if err != nil {
		log.Fatalf("Failed to initialize Kubernetes client: %v", err)
	}

	log.Info("Kubernetes client initialized successfully")

	// Initialize components
	// ... (rest of initialization)
}

// initializeKubernetesClient creates Kubernetes clientset and dynamic client
func initializeKubernetesClient(log *logrus.Logger) (*kubernetes.Clientset, dynamic.Interface, error) {
	var config *rest.Config
	var err error

	// Try in-cluster config first (when running in Kubernetes)
	config, err = rest.InClusterConfig()
	if err != nil {
		log.Info("In-cluster config not available, trying kubeconfig")

		// Fallback to kubeconfig (for local development)
		config, err = buildConfigFromKubeconfig(log)
		if err != nil {
			return nil, nil, err
		}
	} else {
		log.Info("Using in-cluster configuration")
	}

	// Configure client settings
	config.QPS = 50    // Queries per second
	config.Burst = 100 // Burst capacity

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	// Create dynamic client for CRDs
	dynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return nil, nil, err
	}

	return clientset, dynamicClient, nil
}

// buildConfigFromKubeconfig builds config from KUBECONFIG environment or default location
func buildConfigFromKubeconfig(log *logrus.Logger) (*rest.Config, error) {
	kubeconfigPath := os.Getenv("KUBECONFIG")

	if kubeconfigPath == "" {
		// Default kubeconfig location
		if home := homedir.HomeDir(); home != "" {
			kubeconfigPath = filepath.Join(home, ".kube", "config")
		}
	}

	log.WithField("kubeconfig", kubeconfigPath).Info("Loading kubeconfig")

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}

	return config, nil
}
```

### 2. RBAC Configuration

The following RBAC manifests should be deployed with the coordination engine:

#### ServiceAccount

File: `charts/coordination-engine/templates/serviceaccount.yaml`

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: self-healing-operator
  namespace: {{ .Values.namespace | default "self-healing-platform" }}
  labels:
    app: coordination-engine
    component: rbac
```

#### Role

File: `charts/coordination-engine/templates/role.yaml`

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: self-healing-operator
  namespace: {{ .Values.namespace | default "self-healing-platform" }}
rules:
# Core API resources
- apiGroups: [""]
  resources: ["pods", "services", "configmaps", "secrets", "events"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

- apiGroups: [""]
  resources: ["namespaces", "nodes", "endpoints"]
  verbs: ["get", "list", "watch"]

- apiGroups: [""]
  resources: ["persistentvolumes", "persistentvolumeclaims"]
  verbs: ["get", "list", "watch"]

- apiGroups: [""]
  resources: ["leases"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

# Apps API resources
- apiGroups: ["apps"]
  resources: ["deployments", "replicasets", "daemonsets", "statefulsets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

# Batch API resources
- apiGroups: ["batch"]
  resources: ["jobs", "cronjobs"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

# Autoscaling resources
- apiGroups: ["autoscaling"]
  resources: ["horizontalpodautoscalers"]
  verbs: ["get", "list", "watch"]

# Policy resources
- apiGroups: ["policy"]
  resources: ["poddisruptionbudgets"]
  verbs: ["get", "list", "watch"]

# Networking resources
- apiGroups: ["networking.k8s.io"]
  resources: ["networkpolicies"]
  verbs: ["get", "list", "watch"]

# Storage resources
- apiGroups: ["storage.k8s.io"]
  resources: ["storageclasses"]
  verbs: ["get", "list", "watch"]

# Monitoring resources
- apiGroups: ["monitoring.coreos.com"]
  resources: ["servicemonitors", "prometheusrules"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]

# ArgoCD resources (for deployment detection and integration)
- apiGroups: ["argoproj.io"]
  resources: ["applications"]
  verbs: ["get", "list", "watch"]

# Machine configuration resources (read-only for MCO monitoring)
- apiGroups: ["machineconfiguration.openshift.io"]
  resources: ["machineconfigs", "machineconfigpools"]
  verbs: ["get", "list", "watch"]

# OpenShift operators (for multi-layer coordination)
- apiGroups: ["operator.openshift.io", "config.openshift.io"]
  resources: ["clusteroperators"]
  verbs: ["get", "list", "watch"]
```

#### RoleBinding

File: `charts/coordination-engine/templates/rolebinding.yaml`

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: self-healing-operator
  namespace: {{ .Values.namespace | default "self-healing-platform" }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: self-healing-operator
subjects:
- kind: ServiceAccount
  name: self-healing-operator
  namespace: {{ .Values.namespace | default "self-healing-platform" }}
```

### 3. Deployment Configuration

File: `charts/coordination-engine/templates/deployment.yaml`

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: coordination-engine
  namespace: {{ .Values.namespace | default "self-healing-platform" }}
  labels:
    app: coordination-engine
spec:
  replicas: {{ .Values.replicas | default 1 }}
  selector:
    matchLabels:
      app: coordination-engine
  template:
    metadata:
      labels:
        app: coordination-engine
    spec:
      serviceAccountName: self-healing-operator
      containers:
      - name: coordination-engine
        image: {{ .Values.image.repository }}:{{ .Values.image.tag }}
        imagePullPolicy: {{ .Values.image.pullPolicy | default "IfNotPresent" }}
        ports:
        - containerPort: 8080
          name: http
        - containerPort: 9090
          name: metrics
        env:
        - name: LOG_LEVEL
          value: {{ .Values.logLevel | default "info" }}
        - name: ML_SERVICE_URL
          value: {{ .Values.mlServiceUrl }}
        - name: ARGOCD_API_URL
          value: {{ .Values.argocdApiUrl }}
        - name: ARGOCD_TOKEN
          valueFrom:
            secretKeyRef:
              name: argocd-token
              key: token
              optional: true
        resources:
          requests:
            memory: "256Mi"
            cpu: "100m"
          limits:
            memory: "512Mi"
            cpu: "500m"
        livenessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 10
          periodSeconds: 10
        readinessProbe:
          httpGet:
            path: /health
            port: 8080
          initialDelaySeconds: 5
          periodSeconds: 5
```

### 4. RBAC Verification Utilities

Package: `internal/rbac/verifier.go`

```go
package rbac

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	authv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// RBACVerifier verifies RBAC permissions
type RBACVerifier struct {
	clientset *kubernetes.Clientset
	log       *logrus.Logger
}

// NewRBACVerifier creates a new RBAC verifier
func NewRBACVerifier(clientset *kubernetes.Clientset, log *logrus.Logger) *RBACVerifier {
	return &RBACVerifier{
		clientset: clientset,
		log:       log,
	}
}

// VerifyPermission checks if the current service account has a specific permission
func (rv *RBACVerifier) VerifyPermission(ctx context.Context, namespace, verb, group, resource string) (bool, error) {
	rv.log.WithFields(logrus.Fields{
		"namespace": namespace,
		"verb":      verb,
		"group":     group,
		"resource":  resource,
	}).Debug("Verifying RBAC permission")

	sar := &authv1.SelfSubjectAccessReview{
		Spec: authv1.SelfSubjectAccessReviewSpec{
			ResourceAttributes: &authv1.ResourceAttributes{
				Namespace: namespace,
				Verb:      verb,
				Group:     group,
				Resource:  resource,
			},
		},
	}

	response, err := rv.clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(ctx, sar, metav1.CreateOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to verify permission: %w", err)
	}

	allowed := response.Status.Allowed

	if !allowed {
		rv.log.WithFields(logrus.Fields{
			"namespace": namespace,
			"verb":      verb,
			"resource":  fmt.Sprintf("%s.%s", resource, group),
			"reason":    response.Status.Reason,
		}).Warn("Permission denied")
	}

	return allowed, nil
}

// VerifyRequiredPermissions checks all required permissions at startup
func (rv *RBACVerifier) VerifyRequiredPermissions(ctx context.Context, namespace string) error {
	rv.log.Info("Verifying required RBAC permissions")

	requiredPermissions := []struct {
		verb     string
		group    string
		resource string
	}{
		// Core resources
		{"get", "", "pods"},
		{"list", "", "pods"},
		{"delete", "", "pods"},
		{"get", "apps", "deployments"},
		{"patch", "apps", "deployments"},

		// ArgoCD resources
		{"get", "argoproj.io", "applications"},
		{"list", "argoproj.io", "applications"},

		// MCO resources
		{"get", "machineconfiguration.openshift.io", "machineconfigpools"},
		{"list", "machineconfiguration.openshift.io", "machineconfigpools"},
	}

	var missingPermissions []string

	for _, perm := range requiredPermissions {
		allowed, err := rv.VerifyPermission(ctx, namespace, perm.verb, perm.group, perm.resource)
		if err != nil {
			return fmt.Errorf("permission verification failed: %w", err)
		}

		if !allowed {
			missing := fmt.Sprintf("%s %s.%s", perm.verb, perm.resource, perm.group)
			missingPermissions = append(missingPermissions, missing)
		}
	}

	if len(missingPermissions) > 0 {
		return fmt.Errorf("missing required permissions: %v", missingPermissions)
	}

	rv.log.Info("All required RBAC permissions verified")
	return nil
}
```

### 5. Health Check with RBAC Status

Package: `pkg/api/v1/health.go`

```go
package v1

import (
	"encoding/json"
	"net/http"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"openshift-coordination-engine/internal/rbac"
)

type HealthHandler struct {
	clientset     *kubernetes.Clientset
	rbacVerifier  *rbac.RBACVerifier
	log           *logrus.Logger
}

func NewHealthHandler(clientset *kubernetes.Clientset, log *logrus.Logger) *HealthHandler {
	return &HealthHandler{
		clientset:    clientset,
		rbacVerifier: rbac.NewRBACVerifier(clientset, log),
		log:          log,
	}
}

// HealthCheck handles GET /health
func (h *HealthHandler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Check Kubernetes connectivity
	_, err := h.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{Limit: 1})
	k8sStatus := "connected"
	if err != nil {
		k8sStatus = "disconnected"
		h.log.WithError(err).Error("Kubernetes connectivity check failed")
	}

	// Verify critical RBAC permissions
	rbacStatus := "ok"
	canGetPods, _ := h.rbacVerifier.VerifyPermission(ctx, "default", "get", "", "pods")
	if !canGetPods {
		rbacStatus = "missing_permissions"
	}

	response := map[string]interface{}{
		"status": "healthy",
		"version": "1.0.0",
		"dependencies": map[string]interface{}{
			"kubernetes": k8sStatus,
			"rbac":       rbacStatus,
		},
		"timestamp": time.Now().Format(time.RFC3339),
	}

	if k8sStatus != "connected" || rbacStatus != "ok" {
		response["status"] = "degraded"
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
```

## Configuration

Environment variables:
```bash
KUBECONFIG=/path/to/kubeconfig    # Kubeconfig file path (for local dev)
NAMESPACE=self-healing-platform    # Coordination engine namespace
SERVICE_ACCOUNT=self-healing-operator  # ServiceAccount name
K8S_QPS=50                         # Kubernetes API QPS limit
K8S_BURST=100                      # Kubernetes API burst limit
```

## Testing Strategy

### Unit Tests

```go
func TestKubernetesClient_InClusterConfig(t *testing.T) {
	// Test in-cluster configuration loading
	// Mock InClusterConfig to succeed
}

func TestKubernetesClient_KubeconfigFallback(t *testing.T) {
	// Test kubeconfig fallback when in-cluster fails
	// Set KUBECONFIG environment variable
}

func TestRBACVerifier_VerifyPermission(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	verifier := rbac.NewRBACVerifier(clientset, logrus.New())

	allowed, err := verifier.VerifyPermission(context.Background(), "default", "get", "", "pods")

	// In fake clientset, all permissions allowed by default
	assert.NoError(t, err)
	assert.True(t, allowed)
}
```

### Integration Tests

```bash
# Test RBAC permissions on real cluster
kubectl auth can-i get pods --as=system:serviceaccount:self-healing-platform:self-healing-operator -n self-healing-platform
kubectl auth can-i patch deployments --as=system:serviceaccount:self-healing-platform:self-healing-operator -n self-healing-platform
kubectl auth can-i get applications --as=system:serviceaccount:self-healing-platform:self-healing-operator -n self-healing-platform

# Test coordination engine health endpoint
kubectl port-forward svc/coordination-engine 8080:8080 -n self-healing-platform
curl http://localhost:8080/health
```

## Deployment Checklist

- [ ] Create namespace: `kubectl create namespace self-healing-platform`
- [ ] Apply ServiceAccount: `kubectl apply -f serviceaccount.yaml`
- [ ] Apply Role: `kubectl apply -f role.yaml`
- [ ] Apply RoleBinding: `kubectl apply -f rolebinding.yaml`
- [ ] Create ArgoCD token secret (if using ArgoCD)
- [ ] Deploy coordination engine: `helm install coordination-engine ./charts/coordination-engine`
- [ ] Verify RBAC permissions: `kubectl auth can-i ...`
- [ ] Check health endpoint: `curl http://coordination-engine:8080/health`
- [ ] Monitor logs for RBAC errors: `kubectl logs -f deployment/coordination-engine`

## Performance Characteristics

- **Client Initialization**: <1s (in-cluster config)
- **RBAC Verification**: <100ms per permission check
- **API Call QPS**: 50 queries per second (configurable)
- **API Call Burst**: 100 (for spike handling)

## Consequences

### Positive
- ✅ **Secure Access**: Namespace-scoped permissions follow least-privilege principle
- ✅ **Multi-Layer Support**: Permissions cover infrastructure, platform, and application layers
- ✅ **ArgoCD Integration**: Read access to ArgoCD applications
- ✅ **MCO Monitoring**: Read-only access to MachineConfigs and pools
- ✅ **Verification**: Startup checks ensure required permissions are granted
- ✅ **Health Monitoring**: RBAC status included in health endpoint

### Negative
- ⚠️ **Broad Permissions**: Multiple API groups increase security surface area
- ⚠️ **Manual Setup**: RBAC manifests must be applied before deployment
- ⚠️ **Audit Required**: Regular review of permission usage needed

### Mitigation
- **Broad Permissions**: Use namespace-scoped Role (not ClusterRole), implement audit logging
- **Manual Setup**: Automate with Helm charts, add deployment validation
- **Audit**: Enable Kubernetes audit logging, monitor permission usage via metrics

## References

- Platform ADR-033: Coordination Engine RBAC Permissions (permission requirements)
- ADR-001: Go Project Architecture (client initialization patterns)
- Kubernetes RBAC: https://kubernetes.io/docs/reference/access-authn-authz/rbac/
- client-go: https://github.com/kubernetes/client-go
- ServiceAccount Tokens: https://kubernetes.io/docs/reference/access-authn-authz/service-accounts-admin/

## Related ADRs

- Platform ADR-033: Coordination Engine RBAC Permissions
- ADR-001: Go Project Architecture
- ADR-002: Deployment Detection Implementation (ArgoCD API access)
- ADR-003: Multi-Layer Coordination Implementation (multi-layer permissions)
- ADR-004: ArgoCD/MCO Integration (ArgoCD and MCO permissions)
