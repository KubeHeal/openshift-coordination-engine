// Package capacity provides capacity analysis and trending utilities for OpenShift resources.
package capacity

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/sirupsen/logrus"
)

// Analyzer provides capacity analysis for namespaces and clusters
type Analyzer struct {
	k8sClient kubernetes.Interface
	log       *logrus.Logger
}

// NewAnalyzer creates a new capacity analyzer
func NewAnalyzer(k8sClient kubernetes.Interface, log *logrus.Logger) *Analyzer {
	return &Analyzer{
		k8sClient: k8sClient,
		log:       log,
	}
}

// QuotaInfo contains resource quota information
type QuotaInfo struct {
	CPULimit       string  `json:"limit"`
	CPULimitNumeric float64 `json:"limit_numeric"`
}

// MemoryQuotaInfo contains memory quota information
type MemoryQuotaInfo struct {
	Limit      string `json:"limit"`
	LimitBytes int64  `json:"limit_bytes"`
}

// NamespaceQuota contains namespace quota details
type NamespaceQuota struct {
	CPU           *CPUQuota    `json:"cpu,omitempty"`
	Memory        *MemoryQuota `json:"memory,omitempty"`
	PodCountLimit int64        `json:"pod_count_limit"`
	HasQuota      bool         `json:"has_quota"`
}

// CPUQuota contains CPU quota information
type CPUQuota struct {
	Limit        string  `json:"limit"`
	LimitNumeric float64 `json:"limit_numeric"`
}

// MemoryQuota contains memory quota information
type MemoryQuota struct {
	Limit      string `json:"limit"`
	LimitBytes int64  `json:"limit_bytes"`
}

// ResourceUsage contains current resource usage information
type ResourceUsage struct {
	CPU      *CPUUsage    `json:"cpu"`
	Memory   *MemoryUsage `json:"memory"`
	PodCount int          `json:"pod_count"`
}

// CPUUsage contains CPU usage details
type CPUUsage struct {
	Used        string  `json:"used"`
	UsedNumeric float64 `json:"used_numeric"`
	Percent     float64 `json:"percent"`
}

// MemoryUsage contains memory usage details
type MemoryUsage struct {
	Used      string  `json:"used"`
	UsedBytes int64   `json:"used_bytes"`
	Percent   float64 `json:"percent"`
}

// AvailableCapacity contains available resource capacity
type AvailableCapacity struct {
	CPU      *CPUAvailable    `json:"cpu"`
	Memory   *MemoryAvailable `json:"memory"`
	PodSlots int64            `json:"pod_slots"`
}

// CPUAvailable contains available CPU capacity
type CPUAvailable struct {
	Available        string  `json:"available"`
	AvailableNumeric float64 `json:"available_numeric"`
	Percent          float64 `json:"percent"`
}

// MemoryAvailable contains available memory capacity
type MemoryAvailable struct {
	Available      string `json:"available"`
	AvailableBytes int64  `json:"available_bytes"`
	Percent        float64 `json:"percent"`
}

// InfrastructureImpact contains infrastructure impact metrics
type InfrastructureImpact struct {
	EtcdObjectCount      int64   `json:"etcd_object_count"`
	EtcdCapacityPercent  float64 `json:"etcd_capacity_percent"`
	APIServerQPS         float64 `json:"api_server_qps"`
	SchedulerQueueLength int     `json:"scheduler_queue_length"`
	ControlPlaneHealth   string  `json:"control_plane_health"`
}

// ClusterCapacity contains cluster-wide capacity information
type ClusterCapacity struct {
	TotalCPU         string `json:"total_cpu"`
	TotalMemory      string `json:"total_memory"`
	AllocatableCPU   string `json:"allocatable_cpu"`
	AllocatableMemory string `json:"allocatable_memory"`
}

// ClusterUsage contains cluster-wide usage information
type ClusterUsage struct {
	CPU      *CPUUsage    `json:"cpu"`
	Memory   *MemoryUsage `json:"memory"`
	PodCount int          `json:"pod_count"`
}

// NamespaceSummary contains summary information for a namespace
type NamespaceSummary struct {
	Name          string  `json:"name"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	PodCount      int     `json:"pod_count"`
}

// ClusterInfrastructure contains cluster infrastructure health information
type ClusterInfrastructure struct {
	ControlPlaneCPUPercent    float64 `json:"control_plane_cpu_percent"`
	ControlPlaneMemoryPercent float64 `json:"control_plane_memory_percent"`
	EtcdHealth                string  `json:"etcd_health"`
}

// GetNamespaceQuota retrieves resource quota for a namespace
func (a *Analyzer) GetNamespaceQuota(ctx context.Context, namespace string) (*NamespaceQuota, error) {
	quotaList, err := a.k8sClient.CoreV1().ResourceQuotas(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list resource quotas: %w", err)
	}

	result := &NamespaceQuota{
		HasQuota: false,
	}

	if len(quotaList.Items) == 0 {
		return result, nil
	}

	// Aggregate all quotas in the namespace
	var totalCPU, totalMemory resource.Quantity
	var podCount int64

	for _, quota := range quotaList.Items {
		result.HasQuota = true

		if cpu, ok := quota.Status.Hard[corev1.ResourceLimitsCPU]; ok {
			totalCPU.Add(cpu)
		} else if cpu, ok := quota.Status.Hard[corev1.ResourceCPU]; ok {
			totalCPU.Add(cpu)
		}

		if mem, ok := quota.Status.Hard[corev1.ResourceLimitsMemory]; ok {
			totalMemory.Add(mem)
		} else if mem, ok := quota.Status.Hard[corev1.ResourceMemory]; ok {
			totalMemory.Add(mem)
		}

		if pods, ok := quota.Status.Hard[corev1.ResourcePods]; ok {
			podCount += pods.Value()
		}
	}

	if !totalCPU.IsZero() {
		result.CPU = &CPUQuota{
			Limit:        totalCPU.String(),
			LimitNumeric: float64(totalCPU.MilliValue()) / 1000.0,
		}
	}

	if !totalMemory.IsZero() {
		result.Memory = &MemoryQuota{
			Limit:      totalMemory.String(),
			LimitBytes: totalMemory.Value(),
		}
	}

	result.PodCountLimit = podCount

	return result, nil
}

// GetNamespacePodCount returns the number of pods in a namespace
func (a *Analyzer) GetNamespacePodCount(ctx context.Context, namespace string) (int, error) {
	pods, err := a.k8sClient.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to list pods: %w", err)
	}

	// Count only running pods
	count := 0
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodPending {
			count++
		}
	}

	return count, nil
}

// GetClusterCapacity calculates total cluster capacity from nodes
func (a *Analyzer) GetClusterCapacity(ctx context.Context) (*ClusterCapacity, error) {
	nodes, err := a.k8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	var totalCPU, totalMemory resource.Quantity
	var allocatableCPU, allocatableMemory resource.Quantity

	for _, node := range nodes.Items {
		// Skip master nodes if labeled
		if _, isMaster := node.Labels["node-role.kubernetes.io/master"]; isMaster {
			continue
		}
		if _, isControlPlane := node.Labels["node-role.kubernetes.io/control-plane"]; isControlPlane {
			continue
		}

		if cpu, ok := node.Status.Capacity[corev1.ResourceCPU]; ok {
			totalCPU.Add(cpu)
		}
		if mem, ok := node.Status.Capacity[corev1.ResourceMemory]; ok {
			totalMemory.Add(mem)
		}
		if cpu, ok := node.Status.Allocatable[corev1.ResourceCPU]; ok {
			allocatableCPU.Add(cpu)
		}
		if mem, ok := node.Status.Allocatable[corev1.ResourceMemory]; ok {
			allocatableMemory.Add(mem)
		}
	}

	return &ClusterCapacity{
		TotalCPU:          totalCPU.String(),
		TotalMemory:       totalMemory.String(),
		AllocatableCPU:    allocatableCPU.String(),
		AllocatableMemory: allocatableMemory.String(),
	}, nil
}

// GetClusterPodCount returns the total number of pods in the cluster
func (a *Analyzer) GetClusterPodCount(ctx context.Context) (int, error) {
	pods, err := a.k8sClient.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, fmt.Errorf("failed to list all pods: %w", err)
	}

	count := 0
	for _, pod := range pods.Items {
		if pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodPending {
			count++
		}
	}

	return count, nil
}

// ListNamespaces returns all namespaces in the cluster
func (a *Analyzer) ListNamespaces(ctx context.Context) ([]string, error) {
	namespaces, err := a.k8sClient.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces: %w", err)
	}

	result := make([]string, 0, len(namespaces.Items))
	for _, ns := range namespaces.Items {
		result = append(result, ns.Name)
	}

	return result, nil
}

// CalculateAvailableCapacity calculates available capacity from quota and usage
func CalculateAvailableCapacity(quota *NamespaceQuota, usage *ResourceUsage) *AvailableCapacity {
	result := &AvailableCapacity{}

	if quota.CPU != nil && usage.CPU != nil {
		availableCPU := quota.CPU.LimitNumeric - usage.CPU.UsedNumeric
		if availableCPU < 0 {
			availableCPU = 0
		}
		percent := 0.0
		if quota.CPU.LimitNumeric > 0 {
			percent = (availableCPU / quota.CPU.LimitNumeric) * 100
		}
		result.CPU = &CPUAvailable{
			Available:        formatCPU(availableCPU),
			AvailableNumeric: availableCPU,
			Percent:          percent,
		}
	}

	if quota.Memory != nil && usage.Memory != nil {
		availableBytes := quota.Memory.LimitBytes - usage.Memory.UsedBytes
		if availableBytes < 0 {
			availableBytes = 0
		}
		percent := 0.0
		if quota.Memory.LimitBytes > 0 {
			percent = (float64(availableBytes) / float64(quota.Memory.LimitBytes)) * 100
		}
		result.Memory = &MemoryAvailable{
			Available:      formatBytes(availableBytes),
			AvailableBytes: availableBytes,
			Percent:        percent,
		}
	}

	if quota.PodCountLimit > 0 {
		result.PodSlots = quota.PodCountLimit - int64(usage.PodCount)
		if result.PodSlots < 0 {
			result.PodSlots = 0
		}
	}

	return result
}

// formatCPU formats CPU in millicores
func formatCPU(cores float64) string {
	millicores := int64(cores * 1000)
	return fmt.Sprintf("%dm", millicores)
}

// formatBytes formats bytes in human-readable format
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1fGi", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1fMi", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1fKi", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d", bytes)
	}
}

// FormatDuration formats a duration in human-readable format
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "N/A"
	}

	days := int(d.Hours() / 24)
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}

	hours := int(d.Hours())
	if hours > 0 {
		return fmt.Sprintf("%dh", hours)
	}

	minutes := int(d.Minutes())
	if minutes > 0 {
		return fmt.Sprintf("%dm", minutes)
	}

	return fmt.Sprintf("%.0fs", d.Seconds())
}
