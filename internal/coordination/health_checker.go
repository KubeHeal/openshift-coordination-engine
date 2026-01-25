// Package coordination implements multi-layer orchestration and health checking.
package coordination

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// HealthChecker verifies layer-specific health conditions
type HealthChecker struct {
	clientset     kubernetes.Interface
	dynamicClient dynamic.Interface
	log           *logrus.Logger
}

// NewHealthChecker creates a new health checker
func NewHealthChecker(clientset kubernetes.Interface, dynamicClient dynamic.Interface, log *logrus.Logger) *HealthChecker {
	return &HealthChecker{
		clientset:     clientset,
		dynamicClient: dynamicClient,
		log:           log,
	}
}

// CheckInfrastructureHealth verifies infrastructure layer health
func (hc *HealthChecker) CheckInfrastructureHealth(ctx context.Context) error {
	hc.log.Info("Checking infrastructure layer health")

	checks := []func(context.Context) error{
		hc.checkNodesReady,
		hc.checkMCOStable,
		hc.checkStorageAvailable,
	}

	for _, check := range checks {
		if err := check(ctx); err != nil {
			return err
		}
	}

	hc.log.Info("Infrastructure layer health check passed")
	return nil
}

// CheckPlatformHealth verifies platform layer health
func (hc *HealthChecker) CheckPlatformHealth(ctx context.Context) error {
	hc.log.Info("Checking platform layer health")

	checks := []func(context.Context) error{
		hc.checkOperatorsReady,
		hc.checkNetworkingFunctional,
		hc.checkIngressAvailable,
	}

	for _, check := range checks {
		if err := check(ctx); err != nil {
			return err
		}
	}

	hc.log.Info("Platform layer health check passed")
	return nil
}

// CheckApplicationHealth verifies application layer health
func (hc *HealthChecker) CheckApplicationHealth(ctx context.Context) error {
	hc.log.Info("Checking application layer health")

	checks := []func(context.Context) error{
		hc.checkPodsRunning,
		hc.checkEndpointsHealthy,
		hc.checkServicesResponding,
	}

	for _, check := range checks {
		if err := check(ctx); err != nil {
			return err
		}
	}

	hc.log.Info("Application layer health check passed")
	return nil
}

// Infrastructure checks

func (hc *HealthChecker) checkNodesReady(ctx context.Context) error {
	nodes, err := hc.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	notReadyNodes := 0
	for i := range nodes.Items {
		ready := false
		for _, condition := range nodes.Items[i].Status.Conditions {
			if condition.Type == corev1.NodeReady {
				if condition.Status == corev1.ConditionTrue {
					ready = true
					break
				}
			}
		}

		if !ready {
			notReadyNodes++
			hc.log.WithField("node", nodes.Items[i].Name).Warn("Node is not ready")
		}
	}

	if notReadyNodes > 0 {
		return fmt.Errorf("%d node(s) are not ready", notReadyNodes)
	}

	hc.log.WithField("nodes", len(nodes.Items)).Debug("All nodes are ready")
	return nil
}

func (hc *HealthChecker) checkMCOStable(ctx context.Context) error {
	hc.log.Debug("Checking MachineConfigPool status")

	// If dynamic client is not available, skip this check
	if hc.dynamicClient == nil {
		hc.log.Debug("Dynamic client not available, skipping MCO check")
		return nil
	}

	// Define MachineConfigPool GVR
	mcpGVR := schema.GroupVersionResource{
		Group:    "machineconfiguration.openshift.io",
		Version:  "v1",
		Resource: "machineconfigpools",
	}

	// List MachineConfigPools
	mcpList, err := hc.dynamicClient.Resource(mcpGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		// MachineConfigPools might not exist in non-OpenShift clusters
		hc.log.WithError(err).Debug("Failed to list MachineConfigPools (may not be OpenShift)")
		return nil
	}

	degradedPools := 0
	for _, item := range mcpList.Items {
		// Extract status conditions
		conditions, found, err := unstructured.NestedSlice(item.Object, "status", "conditions")
		if err != nil || !found {
			continue
		}

		// Check for degraded or updating conditions
		for _, cond := range conditions {
			condition, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}

			condType, found, err := unstructured.NestedString(condition, "type")
			if err != nil || !found {
				continue
			}
			condStatus, found, err := unstructured.NestedString(condition, "status")
			if err != nil || !found {
				continue
			}

			// Check if pool is degraded
			if condType == "Degraded" && condStatus == "True" {
				degradedPools++
				hc.log.WithField("pool", item.GetName()).Warn("MachineConfigPool is degraded")
			}
		}
	}

	if degradedPools > 0 {
		return fmt.Errorf("%d MachineConfigPool(s) are degraded", degradedPools)
	}

	hc.log.Debug("All MachineConfigPools are stable")
	return nil
}

func (hc *HealthChecker) checkStorageAvailable(ctx context.Context) error {
	hc.log.Debug("Checking storage availability")

	// Check StorageClasses
	storageClasses, err := hc.clientset.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err != nil {
		hc.log.WithError(err).Debug("Failed to list StorageClasses")
		// Storage might not be configured, which is acceptable
		return nil
	}

	if len(storageClasses.Items) == 0 {
		hc.log.Debug("No StorageClasses found (may be acceptable)")
		return nil
	}

	// Check PersistentVolumes
	pvs, err := hc.clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		hc.log.WithError(err).Debug("Failed to list PersistentVolumes")
		return nil
	}

	failedPVs := 0
	for i := range pvs.Items {
		if pvs.Items[i].Status.Phase == corev1.VolumeFailed {
			failedPVs++
			hc.log.WithFields(logrus.Fields{
				"pv":     pvs.Items[i].Name,
				"phase":  pvs.Items[i].Status.Phase,
				"reason": pvs.Items[i].Status.Reason,
			}).Warn("PersistentVolume is in failed state")
		}
	}

	if failedPVs > 0 {
		return fmt.Errorf("%d PersistentVolume(s) are in failed state", failedPVs)
	}

	hc.log.WithFields(logrus.Fields{
		"storage_classes":    len(storageClasses.Items),
		"persistent_volumes": len(pvs.Items),
	}).Debug("Storage is available")
	return nil
}

// Platform checks

//nolint:gocyclo // complexity acceptable for comprehensive operator health checks
func (hc *HealthChecker) checkOperatorsReady(ctx context.Context) error {
	hc.log.Debug("Checking ClusterOperator status")

	// If dynamic client is not available, skip this check
	if hc.dynamicClient == nil {
		hc.log.Debug("Dynamic client not available, skipping ClusterOperator check")
		return nil
	}

	// Define ClusterOperator GVR
	coGVR := schema.GroupVersionResource{
		Group:    "config.openshift.io",
		Version:  "v1",
		Resource: "clusteroperators",
	}

	// List ClusterOperators
	coList, err := hc.dynamicClient.Resource(coGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		// ClusterOperators might not exist in non-OpenShift clusters
		hc.log.WithError(err).Debug("Failed to list ClusterOperators (may not be OpenShift)")
		return nil
	}

	degradedOperators := 0
	unavailableOperators := 0
	for _, item := range coList.Items {
		// Extract status conditions
		conditions, found, err := unstructured.NestedSlice(item.Object, "status", "conditions")
		if err != nil || !found {
			continue
		}

		isDegraded := false
		isAvailable := false

		for _, cond := range conditions {
			condition, ok := cond.(map[string]interface{})
			if !ok {
				continue
			}

			condType, found, err := unstructured.NestedString(condition, "type")
			if err != nil || !found {
				continue
			}
			condStatus, found, err := unstructured.NestedString(condition, "status")
			if err != nil || !found {
				continue
			}

			if condType == "Degraded" && condStatus == "True" {
				isDegraded = true
			}

			if condType == "Available" && condStatus == "True" {
				isAvailable = true
			}
		}

		if isDegraded {
			degradedOperators++
			hc.log.WithField("operator", item.GetName()).Warn("ClusterOperator is degraded")
		}

		if !isAvailable {
			unavailableOperators++
			hc.log.WithField("operator", item.GetName()).Warn("ClusterOperator is not available")
		}
	}

	if degradedOperators > 0 || unavailableOperators > 0 {
		return fmt.Errorf("%d ClusterOperator(s) degraded, %d unavailable", degradedOperators, unavailableOperators)
	}

	hc.log.WithField("operators", len(coList.Items)).Debug("All ClusterOperators are ready")
	return nil
}

func (hc *HealthChecker) checkNetworkingFunctional(ctx context.Context) error {
	hc.log.Debug("Checking networking functionality")

	// Check SDN pods (OpenShift 3.x / 4.x SDN)
	sdnNamespace := "openshift-sdn"
	sdnPods, err := hc.clientset.CoreV1().Pods(sdnNamespace).List(ctx, metav1.ListOptions{})
	if err == nil && len(sdnPods.Items) > 0 {
		// SDN is present, check pod health
		problematicPods := 0
		for i := range sdnPods.Items {
			if sdnPods.Items[i].Status.Phase != corev1.PodRunning && sdnPods.Items[i].Status.Phase != corev1.PodSucceeded {
				problematicPods++
				hc.log.WithFields(logrus.Fields{
					"namespace": sdnNamespace,
					"pod":       sdnPods.Items[i].Name,
					"phase":     sdnPods.Items[i].Status.Phase,
				}).Warn("SDN pod is not healthy")
			}
		}

		if problematicPods > 0 {
			return fmt.Errorf("%d SDN pod(s) are not healthy", problematicPods)
		}

		hc.log.WithField("sdn_pods", len(sdnPods.Items)).Debug("SDN networking is functional")
		return nil
	}

	// Check OVN-Kubernetes pods (OpenShift 4.x OVN)
	ovnNamespace := "openshift-ovn-kubernetes"
	ovnPods, err := hc.clientset.CoreV1().Pods(ovnNamespace).List(ctx, metav1.ListOptions{})
	if err == nil && len(ovnPods.Items) > 0 {
		// OVN is present, check pod health
		problematicPods := 0
		for i := range ovnPods.Items {
			if ovnPods.Items[i].Status.Phase != corev1.PodRunning && ovnPods.Items[i].Status.Phase != corev1.PodSucceeded {
				problematicPods++
				hc.log.WithFields(logrus.Fields{
					"namespace": ovnNamespace,
					"pod":       ovnPods.Items[i].Name,
					"phase":     ovnPods.Items[i].Status.Phase,
				}).Warn("OVN pod is not healthy")
			}
		}

		if problematicPods > 0 {
			return fmt.Errorf("%d OVN pod(s) are not healthy", problematicPods)
		}

		hc.log.WithField("ovn_pods", len(ovnPods.Items)).Debug("OVN networking is functional")
		return nil
	}

	// If neither SDN nor OVN are present, assume basic networking
	hc.log.Debug("No OpenShift networking components found (may be using different CNI)")
	return nil
}

func (hc *HealthChecker) checkIngressAvailable(ctx context.Context) error {
	hc.log.Debug("Checking ingress availability")

	// Check ingress controller namespace (OpenShift)
	ingressNamespace := "openshift-ingress"

	// List deployments in ingress namespace
	deployments, err := hc.clientset.AppsV1().Deployments(ingressNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		// Ingress namespace might not exist in non-OpenShift clusters
		hc.log.WithError(err).Debug("Failed to list ingress deployments (may not be OpenShift)")
		return nil
	}

	if len(deployments.Items) == 0 {
		hc.log.Debug("No ingress deployments found")
		return nil
	}

	unavailableDeployments := 0
	for i := range deployments.Items {
		// Check if deployment is available
		if deployments.Items[i].Status.AvailableReplicas < deployments.Items[i].Status.Replicas {
			unavailableDeployments++
			hc.log.WithFields(logrus.Fields{
				"deployment":         deployments.Items[i].Name,
				"desired_replicas":   deployments.Items[i].Status.Replicas,
				"available_replicas": deployments.Items[i].Status.AvailableReplicas,
			}).Warn("Ingress deployment is not fully available")
		}
	}

	if unavailableDeployments > 0 {
		return fmt.Errorf("%d ingress deployment(s) are not fully available", unavailableDeployments)
	}

	hc.log.WithField("deployments", len(deployments.Items)).Debug("Ingress is available")
	return nil
}

// Application checks

func (hc *HealthChecker) checkPodsRunning(ctx context.Context) error {
	// Check all pods in self-healing-platform namespace
	namespace := "self-healing-platform"

	pods, err := hc.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods in namespace %s: %w", namespace, err)
	}

	problematicPods := 0
	for i := range pods.Items {
		// Allow Running and Succeeded states
		if pods.Items[i].Status.Phase != corev1.PodRunning && pods.Items[i].Status.Phase != corev1.PodSucceeded {
			problematicPods++
			hc.log.WithFields(logrus.Fields{
				"namespace": namespace,
				"pod":       pods.Items[i].Name,
				"phase":     pods.Items[i].Status.Phase,
			}).Warn("Pod is not in Running or Succeeded state")
		}
	}

	if problematicPods > 0 {
		return fmt.Errorf("%d pod(s) in namespace %s are not healthy", problematicPods, namespace)
	}

	hc.log.WithFields(logrus.Fields{
		"namespace": namespace,
		"pods":      len(pods.Items),
	}).Debug("All pods are healthy")
	return nil
}

func (hc *HealthChecker) checkEndpointsHealthy(ctx context.Context) error {
	hc.log.Debug("Checking endpoints health")

	// Check endpoints in self-healing-platform namespace
	namespace := "self-healing-platform"

	endpoints, err := hc.clientset.CoreV1().Endpoints(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		hc.log.WithError(err).Debug("Failed to list endpoints")
		return nil
	}

	emptyEndpoints := 0
	for i := range endpoints.Items {
		// Check if endpoint has ready addresses
		hasReadyAddresses := false
		for _, subset := range endpoints.Items[i].Subsets {
			if len(subset.Addresses) > 0 {
				hasReadyAddresses = true
				break
			}
		}

		if !hasReadyAddresses {
			emptyEndpoints++
			hc.log.WithFields(logrus.Fields{
				"namespace": namespace,
				"endpoint":  endpoints.Items[i].Name,
			}).Warn("Endpoint has no ready addresses")
		}
	}

	// Allow some empty endpoints (services without selectors, etc.)
	if emptyEndpoints > 0 {
		hc.log.WithField("empty_endpoints", emptyEndpoints).Debug("Some endpoints have no ready addresses")
	}

	hc.log.WithField("endpoints", len(endpoints.Items)).Debug("Endpoints checked")
	return nil
}

func (hc *HealthChecker) checkServicesResponding(ctx context.Context) error {
	hc.log.Debug("Checking services responding")

	// Check services in self-healing-platform namespace
	namespace := "self-healing-platform"

	services, err := hc.clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		hc.log.WithError(err).Debug("Failed to list services")
		return nil
	}

	// Just verify services exist and have valid specs
	invalidServices := 0
	for i := range services.Items {
		// Check if service has ports defined
		if len(services.Items[i].Spec.Ports) == 0 {
			invalidServices++
			hc.log.WithFields(logrus.Fields{
				"namespace": namespace,
				"service":   services.Items[i].Name,
			}).Warn("Service has no ports defined")
		}
	}

	if invalidServices > 0 {
		return fmt.Errorf("%d service(s) have invalid configuration", invalidServices)
	}

	hc.log.WithField("services", len(services.Items)).Debug("Services are responding")
	return nil
}
