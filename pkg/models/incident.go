package models

import (
	"fmt"
	"time"
)

// IncidentStatus represents the current state of an incident
type IncidentStatus string

// Incident status constants
const (
	IncidentStatusActive    IncidentStatus = "active"
	IncidentStatusResolved  IncidentStatus = "resolved"
	IncidentStatusCancelled IncidentStatus = "cancelled"
)

// IncidentSeverity represents the severity level of an incident
type IncidentSeverity string

// Incident severity constants
const (
	IncidentSeverityLow      IncidentSeverity = "low"
	IncidentSeverityMedium   IncidentSeverity = "medium"
	IncidentSeverityHigh     IncidentSeverity = "high"
	IncidentSeverityCritical IncidentSeverity = "critical"
)

// Incident represents a manually or automatically created incident for tracking
type Incident struct {
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	Description       string            `json:"description"`
	Severity          IncidentSeverity  `json:"severity"`
	Target            string            `json:"target"`
	Status            IncidentStatus    `json:"status"`
	AffectedResources []string          `json:"affected_resources,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
	ResolvedAt        *time.Time        `json:"resolved_at,omitempty"`
	WorkflowID        string            `json:"workflow_id,omitempty"`
}

// ValidSeverities returns all valid severity values
func ValidSeverities() []IncidentSeverity {
	return []IncidentSeverity{
		IncidentSeverityLow,
		IncidentSeverityMedium,
		IncidentSeverityHigh,
		IncidentSeverityCritical,
	}
}

// IsValidSeverity checks if a severity string is valid
func IsValidSeverity(severity string) bool {
	for _, s := range ValidSeverities() {
		if string(s) == severity {
			return true
		}
	}
	return false
}

// Validate checks if the incident is valid
func (i *Incident) Validate() error {
	if i.Title == "" {
		return fmt.Errorf("title is required")
	}
	if len(i.Title) > 200 {
		return fmt.Errorf("title must not exceed 200 characters")
	}
	if i.Description == "" {
		return fmt.Errorf("description is required")
	}
	if len(i.Description) > 2000 {
		return fmt.Errorf("description must not exceed 2000 characters")
	}
	if i.Severity == "" {
		return fmt.Errorf("severity is required")
	}
	if !IsValidSeverity(string(i.Severity)) {
		return fmt.Errorf("severity must be one of: low, medium, high, critical")
	}
	if i.Target == "" {
		return fmt.Errorf("target is required")
	}
	if len(i.Target) > 100 {
		return fmt.Errorf("target must not exceed 100 characters")
	}
	return nil
}

// String returns a human-readable representation
func (i *Incident) String() string {
	return fmt.Sprintf("[%s] %s: %s (%s)",
		i.Severity,
		i.Target,
		i.Title,
		i.Status,
	)
}

// IsActive returns true if the incident is currently active
func (i *Incident) IsActive() bool {
	return i.Status == IncidentStatusActive
}

// Resolve marks the incident as resolved
func (i *Incident) Resolve() {
	now := time.Now()
	i.Status = IncidentStatusResolved
	i.ResolvedAt = &now
	i.UpdatedAt = now
}

// Cancel marks the incident as cancelled
func (i *Incident) Cancel() {
	i.Status = IncidentStatusCancelled
	i.UpdatedAt = time.Now()
}
