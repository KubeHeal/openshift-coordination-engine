package models

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestIssue_Validate(t *testing.T) {
	tests := []struct {
		name      string
		issue     *Issue
		expectErr bool
	}{
		{
			name: "Valid issue",
			issue: &Issue{
				ID:           "issue-1",
				Type:         "CrashLoopBackOff",
				Severity:     "high",
				Namespace:    "default",
				ResourceType: "pod",
				ResourceName: "test-pod",
				Description:  "Pod is crash looping",
				DetectedAt:   time.Now(),
			},
			expectErr: false,
		},
		{
			name: "Missing ID",
			issue: &Issue{
				Type:         "CrashLoopBackOff",
				Severity:     "high",
				Namespace:    "default",
				ResourceType: "pod",
				ResourceName: "test-pod",
				DetectedAt:   time.Now(),
			},
			expectErr: true,
		},
		{
			name: "Missing Type",
			issue: &Issue{
				ID:           "issue-1",
				Severity:     "high",
				Namespace:    "default",
				ResourceType: "pod",
				ResourceName: "test-pod",
				DetectedAt:   time.Now(),
			},
			expectErr: true,
		},
		{
			name: "Missing Namespace",
			issue: &Issue{
				ID:           "issue-1",
				Type:         "CrashLoopBackOff",
				Severity:     "high",
				ResourceType: "pod",
				ResourceName: "test-pod",
				DetectedAt:   time.Now(),
			},
			expectErr: true,
		},
		{
			name: "Missing ResourceName",
			issue: &Issue{
				ID:           "issue-1",
				Type:         "CrashLoopBackOff",
				Severity:     "high",
				Namespace:    "default",
				ResourceType: "pod",
				DetectedAt:   time.Now(),
			},
			expectErr: true,
		},
		{
			name: "Missing ResourceType",
			issue: &Issue{
				ID:           "issue-1",
				Type:         "CrashLoopBackOff",
				Severity:     "high",
				Namespace:    "default",
				ResourceName: "test-pod",
				DetectedAt:   time.Now(),
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.issue.Validate()
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIssue_String(t *testing.T) {
	issue := &Issue{
		ID:           "issue-1",
		Type:         "CrashLoopBackOff",
		Severity:     "high",
		Namespace:    "production",
		ResourceType: "deployment",
		ResourceName: "payment-service",
		Description:  "Service is failing",
		DetectedAt:   time.Now(),
	}

	result := issue.String()
	assert.Contains(t, result, "production")
	assert.Contains(t, result, "payment-service")
	assert.Contains(t, result, "deployment")
	assert.Contains(t, result, "CrashLoopBackOff")
	assert.Contains(t, result, "high")
}
