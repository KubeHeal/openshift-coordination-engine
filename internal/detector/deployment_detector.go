// Package detector provides deployment method and layer detection capabilities.
package detector

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// Detection constants (as defined in ADR-041)
const (
	// ArgoCD annotations and labels
	ArgoCDTrackingAnnotation = "argocd.argoproj.io/tracking-id"
	ArgoCDInstanceLabel      = "argocd.argoproj.io/instance"

	// Helm annotations
	HelmReleaseNameAnnotation      = "meta.helm.sh/release-name"
	HelmReleaseNamespaceAnnotation = "meta.helm.sh/release-namespace"
	HelmChartLabel                 = "helm.sh/chart"

	// Operator labels
	ManagedByLabel = "app.kubernetes.io/managed-by"

	// Confidence scores (as defined in ADR-041)
	ConfidenceArgoCD   = 0.95
	ConfidenceHelm     = 0.90
	ConfidenceOperator = 0.80
	ConfidenceManual   = 0.60
)

// DeploymentDetector detects the deployment method of Kubernetes resources
type DeploymentDetector struct {
	clientset kubernetes.Interface
	log       *logrus.Logger
	cache     *deploymentCache
}

// deploymentCache caches deployment detection results to reduce API calls
type deploymentCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry
	ttl     time.Duration
}

type cacheEntry struct {
	info      *models.DeploymentInfo
	expiresAt time.Time
}

// NewDeploymentDetector creates a new deployment detector with caching
func NewDeploymentDetector(clientset kubernetes.Interface, log *logrus.Logger) *DeploymentDetector {
	return &DeploymentDetector{
		clientset: clientset,
		log:       log,
		cache: &deploymentCache{
			entries: make(map[string]*cacheEntry),
			ttl:     5 * time.Minute, // Cache entries for 5 minutes
		},
	}
}

// DetectDeploymentMethod detects how a deployment was deployed
// Priority:
// 1. ArgoCD (tracking annotation) - confidence 0.95
// 2. Helm (release annotation) - confidence 0.90
// 3. Operator (managed-by label) - confidence 0.80
// 4. Manual (default) - confidence 0.60
func (d *DeploymentDetector) DetectDeploymentMethod(ctx context.Context, namespace, deploymentName string) (*models.DeploymentInfo, error) {
	// Check cache first
	cacheKey := fmt.Sprintf("deployment/%s/%s", namespace, deploymentName)
	if info := d.cache.get(cacheKey); info != nil {
		d.log.WithFields(logrus.Fields{
			"namespace":  namespace,
			"deployment": deploymentName,
			"method":     info.Method,
			"source":     "cache",
		}).Debug("Deployment method retrieved from cache")

		// Record cache hit metrics
		RecordDetection(string(info.Method), info.Source, "Deployment", info.Confidence, true)

		return info, nil
	}

	// Fetch deployment from Kubernetes
	deployment, err := d.clientset.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		// Record detection error
		if strings.Contains(err.Error(), "not found") {
			RecordDetectionError("not_found", "Deployment")
		} else {
			RecordDetectionError("api_error", "Deployment")
		}
		return nil, fmt.Errorf("failed to get deployment %s/%s: %w", namespace, deploymentName, err)
	}

	// Detect deployment method
	info := d.detectFromMetadata(deployment.Annotations, deployment.Labels, namespace, deploymentName, "Deployment")

	// Cache the result
	d.cache.set(cacheKey, info)

	// Record detection metrics
	RecordDetection(string(info.Method), info.Source, "Deployment", info.Confidence, false)

	d.log.WithFields(logrus.Fields{
		"namespace":  namespace,
		"deployment": deploymentName,
		"method":     info.Method,
		"confidence": info.Confidence,
	}).Info("Detected deployment method")

	return info, nil
}

// detectFromMetadata detects deployment method from annotations and labels
// This is the core detection logic that implements the priority-based strategy from ADR-041
//
//nolint:gocyclo // complexity acceptable for priority-based detection logic
func (d *DeploymentDetector) detectFromMetadata(
	annotations, labels map[string]string,
	namespace, resourceName, resourceKind string,
) *models.DeploymentInfo {
	// Priority 1: ArgoCD (tracking annotation or instance label) - confidence 0.95
	if trackingID, ok := annotations[ArgoCDTrackingAnnotation]; ok && trackingID != "" {
		info := models.NewDeploymentInfo(namespace, resourceName, resourceKind, models.DeploymentMethodArgoCD, ConfidenceArgoCD)
		info.Source = "annotation:" + ArgoCDTrackingAnnotation
		info.SetDetail("tracking_id", trackingID)

		// Add additional ArgoCD details if available
		if appName, ok := labels[ArgoCDInstanceLabel]; ok {
			info.SetDetail("app_name", appName)
		}
		if appName, ok := labels["app.kubernetes.io/instance"]; ok {
			info.SetDetail("argocd_app", appName)
		}

		return info
	}

	// Fallback: Check for ArgoCD instance label if tracking-id not found
	// This handles cases where resources are created by ArgoCD but tracking-id is not set
	if appName, ok := labels[ArgoCDInstanceLabel]; ok && appName != "" {
		info := models.NewDeploymentInfo(namespace, resourceName, resourceKind, models.DeploymentMethodArgoCD, ConfidenceArgoCD)
		info.Source = "label:" + ArgoCDInstanceLabel
		info.SetDetail("argocd_app", appName)
		return info
	}

	// Priority 2: Helm (release annotation) - confidence 0.90
	if releaseName, ok := annotations[HelmReleaseNameAnnotation]; ok && releaseName != "" {
		info := models.NewDeploymentInfo(namespace, resourceName, resourceKind, models.DeploymentMethodHelm, ConfidenceHelm)
		info.Source = "annotation:" + HelmReleaseNameAnnotation
		info.SetDetail("release_name", releaseName)

		// Add additional Helm details if available
		if releaseNamespace, ok := annotations[HelmReleaseNamespaceAnnotation]; ok {
			info.SetDetail("release_namespace", releaseNamespace)
		}
		if chart, ok := labels[HelmChartLabel]; ok {
			info.SetDetail("chart", chart)
		}

		return info
	}

	// Priority 3: Operator (managed-by label) - confidence 0.80
	if managedBy, ok := labels[ManagedByLabel]; ok && managedBy != "" {
		// Exclude Helm from operator detection (Helm also sets managed-by)
		if managedBy != "Helm" && !strings.Contains(strings.ToLower(managedBy), "helm") {
			// Check if it's actually an operator
			if isOperatorManager(managedBy) {
				info := models.NewDeploymentInfo(namespace, resourceName, resourceKind, models.DeploymentMethodOperator, ConfidenceOperator)
				info.Source = "label:" + ManagedByLabel
				info.SetDetail("operator", managedBy)
				info.SetDetail("managed_by", managedBy)

				// Add operator name if available
				if operatorName, ok := labels["app.kubernetes.io/name"]; ok {
					info.SetDetail("operator_name", operatorName)
				}

				return info
			}
		}
	}

	// Priority 4: Manual (default) - confidence 0.60
	info := models.NewDeploymentInfo(namespace, resourceName, resourceKind, models.DeploymentMethodManual, ConfidenceManual)
	info.Source = "default"
	info.SetDetail("reason", "no deployment method indicators found")

	// Add common labels if available
	if appName, ok := labels["app"]; ok {
		info.SetDetail("app", appName)
	}
	if version, ok := labels["version"]; ok {
		info.SetDetail("version", version)
	}

	return info
}

// isOperatorManager returns true if the managed-by value indicates an operator
func isOperatorManager(managedBy string) bool {
	managedByLower := strings.ToLower(managedBy)

	// Common operator patterns
	operatorPatterns := []string{
		"operator",
		".operator",
		"-operator",
	}

	for _, pattern := range operatorPatterns {
		if strings.Contains(managedByLower, pattern) {
			return true
		}
	}

	// Known operators
	knownOperators := []string{
		"prometheus-operator",
		"etcd-operator",
		"mysql-operator",
		"postgres-operator",
		"redis-operator",
		"kafka-operator",
		"elastic-operator",
		"mongodb-operator",
	}

	for _, operator := range knownOperators {
		if managedByLower == operator {
			return true
		}
	}

	return false
}

// DetectStatefulSetMethod detects how a StatefulSet was deployed
func (d *DeploymentDetector) DetectStatefulSetMethod(ctx context.Context, namespace, name string) (*models.DeploymentInfo, error) {
	cacheKey := fmt.Sprintf("statefulset/%s/%s", namespace, name)
	if info := d.cache.get(cacheKey); info != nil {
		d.log.WithFields(logrus.Fields{
			"namespace":   namespace,
			"statefulset": name,
			"source":      "cache",
		}).Debug("StatefulSet method retrieved from cache")
		return info, nil
	}

	sts, err := d.clientset.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get statefulset %s/%s: %w", namespace, name, err)
	}

	info := d.detectFromMetadata(sts.Annotations, sts.Labels, namespace, name, "StatefulSet")
	d.cache.set(cacheKey, info)

	d.log.WithFields(logrus.Fields{
		"namespace":   namespace,
		"statefulset": name,
		"method":      info.Method,
		"confidence":  info.Confidence,
	}).Debug("Detected deployment method for StatefulSet")

	return info, nil
}

// DetectDaemonSetMethod detects how a DaemonSet was deployed
func (d *DeploymentDetector) DetectDaemonSetMethod(ctx context.Context, namespace, name string) (*models.DeploymentInfo, error) {
	cacheKey := fmt.Sprintf("daemonset/%s/%s", namespace, name)
	if info := d.cache.get(cacheKey); info != nil {
		d.log.WithFields(logrus.Fields{
			"namespace": namespace,
			"daemonset": name,
			"source":    "cache",
		}).Debug("DaemonSet method retrieved from cache")
		return info, nil
	}

	ds, err := d.clientset.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get daemonset %s/%s: %w", namespace, name, err)
	}

	info := d.detectFromMetadata(ds.Annotations, ds.Labels, namespace, name, "DaemonSet")
	d.cache.set(cacheKey, info)

	d.log.WithFields(logrus.Fields{
		"namespace":  namespace,
		"daemonset":  name,
		"method":     info.Method,
		"confidence": info.Confidence,
	}).Debug("Detected deployment method for DaemonSet")

	return info, nil
}

// ClearCache clears all cached deployment detection results
func (d *DeploymentDetector) ClearCache() {
	d.cache.clear()
	d.log.Info("Deployment detection cache cleared")
}

// GetCacheStats returns statistics about the cache
func (d *DeploymentDetector) GetCacheStats() map[string]interface{} {
	d.cache.mu.RLock()
	defer d.cache.mu.RUnlock()

	validEntries := 0
	expiredEntries := 0
	now := time.Now()

	for _, entry := range d.cache.entries {
		if now.Before(entry.expiresAt) {
			validEntries++
		} else {
			expiredEntries++
		}
	}

	return map[string]interface{}{
		"total_entries":   len(d.cache.entries),
		"valid_entries":   validEntries,
		"expired_entries": expiredEntries,
		"ttl_seconds":     d.cache.ttl.Seconds(),
	}
}

// Cache methods

func (c *deploymentCache) get(key string) *models.DeploymentInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil
	}

	// Check if expired
	if time.Now().After(entry.expiresAt) {
		return nil
	}

	return entry.info
}

func (c *deploymentCache) set(key string, info *models.DeploymentInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = &cacheEntry{
		info:      info,
		expiresAt: time.Now().Add(c.ttl),
	}
}

func (c *deploymentCache) clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]*cacheEntry)
}
