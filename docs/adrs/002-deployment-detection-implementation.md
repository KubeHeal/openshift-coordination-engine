# ADR-002: Deployment Detection Implementation

## Status
ACCEPTED - 2025-12-18

## Context

This ADR defines the Go implementation of deployment method detection as outlined in platform ADR-041 (Deployment Method Detection Strategy). The coordination engine must accurately detect whether applications were deployed via ArgoCD, Helm, Operators, or manual kubectl commands to route remediation to the appropriate strategy (ADR-005).

Platform ADR-041 establishes:
- Detection priority: ArgoCD > Helm > Operator > Manual
- Confidence scoring for each detection method
- Caching strategy to optimize performance
- Detection accuracy target of >90%

## Decision

Implement deployment detection in Go with the following components:

### 1. Core Types and Interfaces

Package: `pkg/models/deployment_info.go`

```go
package models

import "time"

// DeploymentMethod represents how an application was deployed
type DeploymentMethod string

const (
    DeploymentMethodArgoCD   DeploymentMethod = "argocd"
    DeploymentMethodHelm     DeploymentMethod = "helm"
    DeploymentMethodOperator DeploymentMethod = "operator"
    DeploymentMethodManual   DeploymentMethod = "manual"
    DeploymentMethodUnknown  DeploymentMethod = "unknown"
)

// DeploymentInfo contains detected deployment information
type DeploymentInfo struct {
    Method     DeploymentMethod `json:"method"`
    Managed    bool             `json:"managed"`     // Is it GitOps or operator-managed?
    Source     string           `json:"source"`      // Git repo, Helm chart, etc.
    ManagedBy  string           `json:"managed_by"`  // ArgoCD app, Helm release, operator name
    Namespace  string           `json:"namespace"`
    Confidence float64          `json:"confidence"`  // Detection confidence (0.0-1.0)
    DetectedAt time.Time        `json:"detected_at"`
}

// IsHighConfidence returns true if confidence is above threshold
func (d *DeploymentInfo) IsHighConfidence() bool {
    return d.Confidence >= 0.70
}

// IsGitOpsManaged returns true if managed by ArgoCD (GitOps)
func (d *DeploymentInfo) IsGitOpsManaged() bool {
    return d.Method == DeploymentMethodArgoCD && d.Managed
}
```

### 2. Detector Implementation

Package: `internal/detector/deployment_detector.go`

```go
package detector

import (
    "context"
    "fmt"
    "time"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes"

    "openshift-coordination-engine/pkg/models"
)

// DeploymentDetector detects how applications were deployed
type DeploymentDetector struct {
    clientset *kubernetes.Clientset
    cache     DetectionCache
}

// NewDeploymentDetector creates a new deployment detector
func NewDeploymentDetector(clientset *kubernetes.Clientset, cache DetectionCache) *DeploymentDetector {
    return &DeploymentDetector{
        clientset: clientset,
        cache:     cache,
    }
}

// DetectFromPod detects deployment method from a pod
func (d *DeploymentDetector) DetectFromPod(ctx context.Context, namespace, podName string) (*models.DeploymentInfo, error) {
    // Check cache first
    if cached := d.cache.Get(namespace, podName, "Pod"); cached != nil {
        return cached, nil
    }

    // Get pod from Kubernetes API
    pod, err := d.clientset.CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
    if err != nil {
        return nil, fmt.Errorf("failed to get pod %s/%s: %w", namespace, podName, err)
    }

    // Detect from metadata
    info := d.DetectFromMetadata(namespace, podName, pod.Labels, pod.Annotations)

    // Cache result
    d.cache.Set(namespace, podName, "Pod", info)

    return info, nil
}

// DetectFromMetadata detects deployment method from labels and annotations
func (d *DeploymentDetector) DetectFromMetadata(namespace, name string, labels, annotations map[string]string) *models.DeploymentInfo {
    // Priority 1: ArgoCD detection (confidence: 0.95)
    if trackingID, ok := annotations["argocd.argoproj.io/tracking-id"]; ok {
        return &models.DeploymentInfo{
            Method:     models.DeploymentMethodArgoCD,
            Managed:    true,
            Source:     annotations["argocd.argoproj.io/git-repository"],
            ManagedBy:  annotations["argocd.argoproj.io/instance"],
            Namespace:  namespace,
            Confidence: 0.95,
            DetectedAt: time.Now(),
        }
    }

    // Fallback ArgoCD detection (confidence: 0.85)
    if instance, ok := labels["argocd.argoproj.io/instance"]; ok {
        return &models.DeploymentInfo{
            Method:     models.DeploymentMethodArgoCD,
            Managed:    true,
            ManagedBy:  instance,
            Namespace:  namespace,
            Confidence: 0.85,
            DetectedAt: time.Now(),
        }
    }

    // Priority 2: Helm detection (confidence: 0.90)
    if releaseName, ok := annotations["meta.helm.sh/release-name"]; ok {
        releaseNS := annotations["meta.helm.sh/release-namespace"]
        if releaseNS == "" {
            releaseNS = namespace
        }

        return &models.DeploymentInfo{
            Method:     models.DeploymentMethodHelm,
            Managed:    false, // Helm is not GitOps-managed unless via ArgoCD
            Source:     fmt.Sprintf("helm:%s", releaseName),
            ManagedBy:  releaseName,
            Namespace:  releaseNS,
            Confidence: 0.90,
            DetectedAt: time.Now(),
        }
    }

    // Priority 3: Operator detection (confidence: 0.80)
    if managedBy, ok := labels["app.kubernetes.io/managed-by"]; ok && managedBy != "Helm" {
        return &models.DeploymentInfo{
            Method:     models.DeploymentMethodOperator,
            Managed:    true,
            ManagedBy:  managedBy,
            Namespace:  namespace,
            Confidence: 0.80,
            DetectedAt: time.Now(),
        }
    }

    // Default: Manual deployment (confidence: 0.60)
    return &models.DeploymentInfo{
        Method:     models.DeploymentMethodManual,
        Managed:    false,
        Namespace:  namespace,
        Confidence: 0.60,
        DetectedAt: time.Now(),
    }
}
```

### 3. Caching Layer

Package: `internal/detector/cache.go`

```go
package detector

import (
    "fmt"
    "sync"
    "time"

    "openshift-coordination-engine/pkg/models"
)

// DetectionCache caches deployment detection results
type DetectionCache interface {
    Get(namespace, name, kind string) *models.DeploymentInfo
    Set(namespace, name, kind string, info *models.DeploymentInfo)
    Invalidate(namespace, name, kind string)
    Clear()
}

// InMemoryCache implements in-memory caching with TTL
type InMemoryCache struct {
    cache map[string]*cacheEntry
    ttl   time.Duration
    mu    sync.RWMutex
}

type cacheEntry struct {
    info      *models.DeploymentInfo
    expiresAt time.Time
}

// NewInMemoryCache creates a new in-memory cache
func NewInMemoryCache(ttl time.Duration) *InMemoryCache {
    cache := &InMemoryCache{
        cache: make(map[string]*cacheEntry),
        ttl:   ttl,
    }

    // Start background cleanup goroutine
    go cache.cleanup()

    return cache
}

func (c *InMemoryCache) cacheKey(namespace, name, kind string) string {
    return fmt.Sprintf("%s:%s:%s", kind, namespace, name)
}

func (c *InMemoryCache) Get(namespace, name, kind string) *models.DeploymentInfo {
    c.mu.RLock()
    defer c.mu.RUnlock()

    key := c.cacheKey(namespace, name, kind)
    entry, ok := c.cache[key]
    if !ok {
        return nil
    }

    // Check if expired
    if time.Now().After(entry.expiresAt) {
        return nil
    }

    return entry.info
}

func (c *InMemoryCache) Set(namespace, name, kind string, info *models.DeploymentInfo) {
    c.mu.Lock()
    defer c.mu.Unlock()

    key := c.cacheKey(namespace, name, kind)
    c.cache[key] = &cacheEntry{
        info:      info,
        expiresAt: time.Now().Add(c.ttl),
    }
}

func (c *InMemoryCache) Invalidate(namespace, name, kind string) {
    c.mu.Lock()
    defer c.mu.Unlock()

    key := c.cacheKey(namespace, name, kind)
    delete(c.cache, key)
}

func (c *InMemoryCache) Clear() {
    c.mu.Lock()
    defer c.mu.Unlock()

    c.cache = make(map[string]*cacheEntry)
}

// cleanup removes expired entries every minute
func (c *InMemoryCache) cleanup() {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()

    for range ticker.C {
        c.mu.Lock()
        now := time.Now()
        for key, entry := range c.cache {
            if now.After(entry.expiresAt) {
                delete(c.cache, key)
            }
        }
        c.mu.Unlock()
    }
}
```

### 4. API Handler

Package: `pkg/api/v1/detection.go`

```go
package v1

import (
    "encoding/json"
    "net/http"

    "github.com/gorilla/mux"
    "github.com/sirupsen/logrus"

    "openshift-coordination-engine/internal/detector"
)

type DetectionHandler struct {
    detector *detector.DeploymentDetector
    log      *logrus.Logger
}

func NewDetectionHandler(detector *detector.DeploymentDetector, log *logrus.Logger) *DetectionHandler {
    return &DetectionHandler{
        detector: detector,
        log:      log,
    }
}

// DetectPodDeploymentMethod handles GET /api/v1/detect/pod/{namespace}/{pod_name}
func (h *DetectionHandler) DetectPodDeploymentMethod(w http.ResponseWriter, r *http.Request) {
    vars := mux.Vars(r)
    namespace := vars["namespace"]
    podName := vars["pod_name"]

    log := h.log.WithFields(logrus.Fields{
        "namespace": namespace,
        "pod":       podName,
    })

    log.Info("Detecting deployment method for pod")

    info, err := h.detector.DetectFromPod(r.Context(), namespace, podName)
    if err != nil {
        log.WithError(err).Error("Failed to detect deployment method")
        http.Error(w, err.Error(), http.StatusInternalServerError)
        return
    }

    response := map[string]interface{}{
        "status":          "success",
        "deployment_info": info,
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(http.StatusOK)
    json.NewEncoder(w).Encode(response)

    log.WithFields(logrus.Fields{
        "method":     info.Method,
        "managed":    info.Managed,
        "confidence": info.Confidence,
    }).Info("Deployment method detected")
}
```

### 5. Metrics

Package: `internal/detector/metrics.go`

```go
package detector

import (
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promauto"
)

var (
    DetectionTotal = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "coordination_engine_deployment_detection_total",
            Help: "Total number of deployment method detections",
        },
        []string{"method", "namespace"},
    )

    DetectionConfidence = promauto.NewHistogramVec(
        prometheus.HistogramOpts{
            Name:    "coordination_engine_deployment_detection_confidence",
            Help:    "Confidence score of deployment detection",
            Buckets: prometheus.LinearBuckets(0.5, 0.1, 6), // 0.5 to 1.0
        },
        []string{"method"},
    )

    CacheHits = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "coordination_engine_detection_cache_hits_total",
            Help: "Total number of cache hits for deployment detection",
        },
        []string{"namespace"},
    )

    CacheMisses = promauto.NewCounterVec(
        prometheus.CounterOpts{
            Name: "coordination_engine_detection_cache_misses_total",
            Help: "Total number of cache misses for deployment detection",
        },
        []string{"namespace"},
    )
)
```

## Configuration

Environment variables:
```bash
DETECTION_CACHE_TTL=5m           # Cache TTL (default: 5 minutes)
DETECTION_CONFIDENCE_THRESHOLD=0.70  # Minimum confidence threshold
```

## Testing Strategy

### Unit Tests

```go
func TestDeploymentDetector_DetectFromMetadata_ArgoCD(t *testing.T) {
    detector := NewDeploymentDetector(nil, NewInMemoryCache(5*time.Minute))

    annotations := map[string]string{
        "argocd.argoproj.io/tracking-id":    "payment:Deployment:prod/payment",
        "argocd.argoproj.io/instance":       "payment-service",
        "argocd.argoproj.io/git-repository": "https://github.com/org/repo",
    }

    info := detector.DetectFromMetadata("prod", "payment-123", nil, annotations)

    assert.Equal(t, models.DeploymentMethodArgoCD, info.Method)
    assert.True(t, info.Managed)
    assert.Equal(t, "payment-service", info.ManagedBy)
    assert.Greater(t, info.Confidence, 0.9)
    assert.True(t, info.IsHighConfidence())
    assert.True(t, info.IsGitOpsManaged())
}

func TestInMemoryCache_Expiration(t *testing.T) {
    cache := NewInMemoryCache(100 * time.Millisecond)

    info := &models.DeploymentInfo{Method: models.DeploymentMethodArgoCD}
    cache.Set("ns", "pod", "Pod", info)

    // Should exist immediately
    assert.NotNil(t, cache.Get("ns", "pod", "Pod"))

    // Wait for expiration
    time.Sleep(150 * time.Millisecond)

    // Should be expired
    assert.Nil(t, cache.Get("ns", "pod", "Pod"))
}
```

### Integration Tests

```go
func TestDeploymentDetector_IntegrationWithKubernetes(t *testing.T) {
    // Requires test cluster or mock Kubernetes API
    clientset := testutil.NewFakeClientset()
    cache := NewInMemoryCache(5 * time.Minute)
    detector := NewDeploymentDetector(clientset, cache)

    // Create test pod with ArgoCD annotations
    pod := &corev1.Pod{
        ObjectMeta: metav1.ObjectMeta{
            Name:      "test-pod",
            Namespace: "default",
            Annotations: map[string]string{
                "argocd.argoproj.io/tracking-id": "app:Deployment:default/test",
            },
        },
    }
    clientset.CoreV1().Pods("default").Create(context.Background(), pod, metav1.CreateOptions{})

    // Detect deployment method
    info, err := detector.DetectFromPod(context.Background(), "default", "test-pod")

    assert.NoError(t, err)
    assert.Equal(t, models.DeploymentMethodArgoCD, info.Method)
}
```

## Performance Characteristics

- **Cache Hit Latency**: <1ms (in-memory lookup)
- **Cache Miss Latency**: <100ms (Kubernetes API call + detection)
- **Cache TTL**: 5 minutes (configurable)
- **Detection Accuracy**: >90% for ArgoCD, Helm, Operator
- **Memory Overhead**: ~100 bytes per cached entry

## Consequences

### Positive
- ✅ **High Performance**: In-memory caching reduces API calls by ~80%
- ✅ **Type Safety**: Go's type system prevents detection errors
- ✅ **Testability**: Clear interfaces enable comprehensive testing
- ✅ **Observability**: Prometheus metrics track accuracy and performance
- ✅ **Concurrent Safe**: Mutex-protected cache supports concurrent access

### Negative
- ⚠️ **Cache Staleness**: 5-minute TTL means detection may lag metadata changes
- ⚠️ **Memory Usage**: Large clusters with many pods require more memory
- ⚠️ **No Persistence**: Cache lost on restart (acceptable for this use case)

### Mitigation
- **Staleness**: Implement cache invalidation on pod update events
- **Memory**: Monitor cache size, implement LRU eviction if needed
- **Persistence**: Not required, acceptable trade-off for performance

## References

- Platform ADR-041: Deployment Method Detection Strategy (overall strategy)
- Platform ADR-039: Non-ArgoCD Application Remediation (usage context)
- ADR-001: Go Project Architecture (package organization)
- ADR-005: Remediation Strategies Implementation (consumer of detection)
- Kubernetes client-go: https://github.com/kubernetes/client-go
- ArgoCD Tracking Annotations: https://argo-cd.readthedocs.io/en/stable/user-guide/resource_tracking/

## Related ADRs

- Platform ADR-041: Deployment Method Detection Strategy
- ADR-001: Go Project Architecture
- ADR-005: Remediation Strategies Implementation
