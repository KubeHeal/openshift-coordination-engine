package models

import (
	"fmt"
	"time"
)

// Issue represents a problem requiring remediation
type Issue struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`     // "CrashLoopBackOff", "ImagePullBackOff", etc.
	Severity     string    `json:"severity"` // "low", "medium", "high", "critical"
	Namespace    string    `json:"namespace"`
	ResourceType string    `json:"resource_type"` // "pod", "deployment", "statefulset"
	ResourceName string    `json:"resource_name"`
	Description  string    `json:"description"`
	DetectedAt   time.Time `json:"detected_at"`
}

// Validate checks if the issue is valid
func (i *Issue) Validate() error {
	if i.ID == "" {
		return fmt.Errorf("issue ID is required")
	}
	if i.Type == "" {
		return fmt.Errorf("issue type is required")
	}
	if i.Namespace == "" {
		return fmt.Errorf("namespace is required")
	}
	if i.ResourceName == "" {
		return fmt.Errorf("resource name is required")
	}
	if i.ResourceType == "" {
		return fmt.Errorf("resource type is required")
	}
	return nil
}

// String returns a human-readable representation
func (i *Issue) String() string {
	return fmt.Sprintf("%s/%s (%s): %s [%s]",
		i.Namespace,
		i.ResourceName,
		i.ResourceType,
		i.Type,
		i.Severity,
	)
}
