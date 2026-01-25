package detector

import (
	"context"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"github.com/tosin2013/openshift-coordination-engine/pkg/models"
)

// Detector is an alias for DeploymentDetector for easier imports
type Detector = DeploymentDetector

// NewDetector creates a new deployment detector
func NewDetector(clientset kubernetes.Interface, log *logrus.Logger) *Detector {
	return NewDeploymentDetector(clientset, log)
}

// DetectByKind is a convenience wrapper that detects deployment method based on resource kind
func (d *Detector) DetectByKind(ctx context.Context, namespace, name, kind string) (*models.DeploymentInfo, error) {
	// Route to appropriate detection method based on kind
	switch kind {
	case "StatefulSet":
		return d.DetectStatefulSetMethod(ctx, namespace, name)
	case "DaemonSet":
		return d.DetectDaemonSetMethod(ctx, namespace, name)
	case "Deployment", "Pod", "":
		// Default to deployment detection
		return d.DetectDeploymentMethod(ctx, namespace, name)
	default:
		// Unknown kind, try deployment detection
		return d.DetectDeploymentMethod(ctx, namespace, name)
	}
}
