// Package models defines data structures for deployment detection and remediation.
package models

import (
	"encoding/json"
	"fmt"
	"time"
)

// DeploymentMethod represents the deployment method used for an application
type DeploymentMethod string

const (
	// DeploymentMethodArgoCD indicates the application is managed by ArgoCD
	DeploymentMethodArgoCD DeploymentMethod = "argocd"

	// DeploymentMethodHelm indicates the application is deployed via Helm
	DeploymentMethodHelm DeploymentMethod = "helm"

	// DeploymentMethodOperator indicates the application is managed by a Kubernetes Operator
	DeploymentMethodOperator DeploymentMethod = "operator"

	// DeploymentMethodManual indicates the application is manually deployed (kubectl apply, etc.)
	DeploymentMethodManual DeploymentMethod = "manual"

	// DeploymentMethodUnknown indicates the deployment method could not be determined
	DeploymentMethodUnknown DeploymentMethod = "unknown"
)

// DeploymentInfo contains information about how an application was deployed
type DeploymentInfo struct {
	// Method is the detected deployment method
	Method DeploymentMethod `json:"method"`

	// Confidence is a score between 0.0 and 1.0 indicating detection confidence
	// Higher values indicate more certainty in the detection
	Confidence float64 `json:"confidence"`

	// Source is the metadata source used for detection (e.g., "annotation", "label")
	Source string `json:"source"`

	// Details contains method-specific information
	Details map[string]string `json:"details,omitempty"`

	// DetectedAt is the timestamp when detection occurred
	DetectedAt time.Time `json:"detected_at"`

	// Namespace is the Kubernetes namespace
	Namespace string `json:"namespace"`

	// ResourceName is the name of the resource (pod, deployment, etc.)
	ResourceName string `json:"resource_name"`

	// ResourceKind is the kind of resource (Pod, Deployment, etc.)
	ResourceKind string `json:"resource_kind"`
}

// IsHighConfidence returns true if the confidence score is >= 0.80
func (d *DeploymentInfo) IsHighConfidence() bool {
	return d.Confidence >= 0.80
}

// IsMediumConfidence returns true if the confidence score is >= 0.60 and < 0.80
func (d *DeploymentInfo) IsMediumConfidence() bool {
	return d.Confidence >= 0.60 && d.Confidence < 0.80
}

// IsLowConfidence returns true if the confidence score is < 0.60
func (d *DeploymentInfo) IsLowConfidence() bool {
	return d.Confidence < 0.60
}

// IsGitOpsManaged returns true if the application is managed via GitOps (ArgoCD)
func (d *DeploymentInfo) IsGitOpsManaged() bool {
	return d.Method == DeploymentMethodArgoCD
}

// IsOperatorManaged returns true if the application is managed by a Kubernetes Operator
func (d *DeploymentInfo) IsOperatorManaged() bool {
	return d.Method == DeploymentMethodOperator
}

// IsHelmManaged returns true if the application is deployed via Helm
func (d *DeploymentInfo) IsHelmManaged() bool {
	return d.Method == DeploymentMethodHelm
}

// IsManuallyDeployed returns true if the application is manually deployed
func (d *DeploymentInfo) IsManuallyDeployed() bool {
	return d.Method == DeploymentMethodManual
}

// GetDetail returns a specific detail value by key, or empty string if not found
func (d *DeploymentInfo) GetDetail(key string) string {
	if d.Details == nil {
		return ""
	}
	return d.Details[key]
}

// SetDetail sets a detail value
func (d *DeploymentInfo) SetDetail(key, value string) {
	if d.Details == nil {
		d.Details = make(map[string]string)
	}
	d.Details[key] = value
}

// Validate checks if the DeploymentInfo is valid
func (d *DeploymentInfo) Validate() error {
	// Check method is valid
	switch d.Method {
	case DeploymentMethodArgoCD, DeploymentMethodHelm, DeploymentMethodOperator,
		DeploymentMethodManual, DeploymentMethodUnknown:
		// Valid method
	default:
		return fmt.Errorf("invalid deployment method: %s", d.Method)
	}

	// Check confidence is in valid range
	if d.Confidence < 0.0 || d.Confidence > 1.0 {
		return fmt.Errorf("confidence must be between 0.0 and 1.0, got: %f", d.Confidence)
	}

	// Check required fields
	if d.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}

	if d.ResourceName == "" {
		return fmt.Errorf("resource_name is required")
	}

	if d.ResourceKind == "" {
		return fmt.Errorf("resource_kind is required")
	}

	// Check detected_at is not zero
	if d.DetectedAt.IsZero() {
		return fmt.Errorf("detected_at timestamp is required")
	}

	return nil
}

// String returns a human-readable string representation
func (d *DeploymentInfo) String() string {
	return fmt.Sprintf("%s/%s (%s): %s (confidence: %.2f)",
		d.Namespace, d.ResourceName, d.ResourceKind, d.Method, d.Confidence)
}

// ToJSON serializes the DeploymentInfo to JSON
func (d *DeploymentInfo) ToJSON() ([]byte, error) {
	data, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal deployment info: %w", err)
	}
	return data, nil
}

// FromJSON deserializes DeploymentInfo from JSON
func FromJSON(data []byte) (*DeploymentInfo, error) {
	var info DeploymentInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("failed to unmarshal deployment info: %w", err)
	}

	// Validate after unmarshaling
	if err := info.Validate(); err != nil {
		return nil, fmt.Errorf("invalid deployment info: %w", err)
	}

	return &info, nil
}

// NewDeploymentInfo creates a new DeploymentInfo with required fields
func NewDeploymentInfo(namespace, resourceName, resourceKind string, method DeploymentMethod, confidence float64) *DeploymentInfo {
	return &DeploymentInfo{
		Method:       method,
		Confidence:   confidence,
		Namespace:    namespace,
		ResourceName: resourceName,
		ResourceKind: resourceKind,
		DetectedAt:   time.Now(),
		Details:      make(map[string]string),
	}
}
