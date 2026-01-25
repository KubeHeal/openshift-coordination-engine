package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeploymentMethod_Constants(t *testing.T) {
	tests := []struct {
		name     string
		method   DeploymentMethod
		expected string
	}{
		{"ArgoCD", DeploymentMethodArgoCD, "argocd"},
		{"Helm", DeploymentMethodHelm, "helm"},
		{"Operator", DeploymentMethodOperator, "operator"},
		{"Manual", DeploymentMethodManual, "manual"},
		{"Unknown", DeploymentMethodUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.method))
		})
	}
}

func TestNewDeploymentInfo(t *testing.T) {
	info := NewDeploymentInfo("default", "my-app", "Pod", DeploymentMethodArgoCD, 0.95)

	assert.Equal(t, "default", info.Namespace)
	assert.Equal(t, "my-app", info.ResourceName)
	assert.Equal(t, "Pod", info.ResourceKind)
	assert.Equal(t, DeploymentMethodArgoCD, info.Method)
	assert.Equal(t, 0.95, info.Confidence)
	assert.NotZero(t, info.DetectedAt)
	assert.NotNil(t, info.Details)
}

func TestDeploymentInfo_IsHighConfidence(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		expected   bool
	}{
		{"High - 0.95", 0.95, true},
		{"High - 0.80", 0.80, true},
		{"Medium - 0.75", 0.75, false},
		{"Low - 0.50", 0.50, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := NewDeploymentInfo("default", "app", "Pod", DeploymentMethodArgoCD, tt.confidence)
			assert.Equal(t, tt.expected, info.IsHighConfidence())
		})
	}
}

func TestDeploymentInfo_IsMediumConfidence(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		expected   bool
	}{
		{"High - 0.95", 0.95, false},
		{"Medium - 0.75", 0.75, true},
		{"Medium - 0.60", 0.60, true},
		{"Low - 0.59", 0.59, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := NewDeploymentInfo("default", "app", "Pod", DeploymentMethodArgoCD, tt.confidence)
			assert.Equal(t, tt.expected, info.IsMediumConfidence())
		})
	}
}

func TestDeploymentInfo_IsLowConfidence(t *testing.T) {
	tests := []struct {
		name       string
		confidence float64
		expected   bool
	}{
		{"High - 0.80", 0.80, false},
		{"Medium - 0.60", 0.60, false},
		{"Low - 0.59", 0.59, true},
		{"Low - 0.30", 0.30, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := NewDeploymentInfo("default", "app", "Pod", DeploymentMethodArgoCD, tt.confidence)
			assert.Equal(t, tt.expected, info.IsLowConfidence())
		})
	}
}

func TestDeploymentInfo_IsGitOpsManaged(t *testing.T) {
	tests := []struct {
		name     string
		method   DeploymentMethod
		expected bool
	}{
		{"ArgoCD is GitOps", DeploymentMethodArgoCD, true},
		{"Helm is not GitOps", DeploymentMethodHelm, false},
		{"Operator is not GitOps", DeploymentMethodOperator, false},
		{"Manual is not GitOps", DeploymentMethodManual, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := NewDeploymentInfo("default", "app", "Pod", tt.method, 0.90)
			assert.Equal(t, tt.expected, info.IsGitOpsManaged())
		})
	}
}

func TestDeploymentInfo_IsOperatorManaged(t *testing.T) {
	tests := []struct {
		name     string
		method   DeploymentMethod
		expected bool
	}{
		{"Operator is operator-managed", DeploymentMethodOperator, true},
		{"ArgoCD is not operator-managed", DeploymentMethodArgoCD, false},
		{"Helm is not operator-managed", DeploymentMethodHelm, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := NewDeploymentInfo("default", "app", "Pod", tt.method, 0.90)
			assert.Equal(t, tt.expected, info.IsOperatorManaged())
		})
	}
}

func TestDeploymentInfo_IsHelmManaged(t *testing.T) {
	tests := []struct {
		name     string
		method   DeploymentMethod
		expected bool
	}{
		{"Helm is helm-managed", DeploymentMethodHelm, true},
		{"ArgoCD is not helm-managed", DeploymentMethodArgoCD, false},
		{"Operator is not helm-managed", DeploymentMethodOperator, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := NewDeploymentInfo("default", "app", "Pod", tt.method, 0.90)
			assert.Equal(t, tt.expected, info.IsHelmManaged())
		})
	}
}

func TestDeploymentInfo_IsManuallyDeployed(t *testing.T) {
	tests := []struct {
		name     string
		method   DeploymentMethod
		expected bool
	}{
		{"Manual is manually-deployed", DeploymentMethodManual, true},
		{"ArgoCD is not manually-deployed", DeploymentMethodArgoCD, false},
		{"Helm is not manually-deployed", DeploymentMethodHelm, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := NewDeploymentInfo("default", "app", "Pod", tt.method, 0.90)
			assert.Equal(t, tt.expected, info.IsManuallyDeployed())
		})
	}
}

func TestDeploymentInfo_Details(t *testing.T) {
	info := NewDeploymentInfo("default", "app", "Pod", DeploymentMethodArgoCD, 0.95)

	// Test setting and getting details
	info.SetDetail("app_name", "my-argocd-app")
	info.SetDetail("tracking_id", "abc123")

	assert.Equal(t, "my-argocd-app", info.GetDetail("app_name"))
	assert.Equal(t, "abc123", info.GetDetail("tracking_id"))
	assert.Equal(t, "", info.GetDetail("nonexistent"))

	// Test multiple sets
	info.SetDetail("app_name", "updated-app")
	assert.Equal(t, "updated-app", info.GetDetail("app_name"))
}

func TestDeploymentInfo_Validate(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func() *DeploymentInfo
		wantErr   bool
		errMsg    string
	}{
		{
			name: "Valid deployment info",
			setupFunc: func() *DeploymentInfo {
				return NewDeploymentInfo("default", "app", "Pod", DeploymentMethodArgoCD, 0.95)
			},
			wantErr: false,
		},
		{
			name: "Invalid deployment method",
			setupFunc: func() *DeploymentInfo {
				info := NewDeploymentInfo("default", "app", "Pod", DeploymentMethodArgoCD, 0.95)
				info.Method = DeploymentMethod("invalid")
				return info
			},
			wantErr: true,
			errMsg:  "invalid deployment method",
		},
		{
			name: "Confidence too low",
			setupFunc: func() *DeploymentInfo {
				return NewDeploymentInfo("default", "app", "Pod", DeploymentMethodArgoCD, -0.1)
			},
			wantErr: true,
			errMsg:  "confidence must be between 0.0 and 1.0",
		},
		{
			name: "Confidence too high",
			setupFunc: func() *DeploymentInfo {
				return NewDeploymentInfo("default", "app", "Pod", DeploymentMethodArgoCD, 1.5)
			},
			wantErr: true,
			errMsg:  "confidence must be between 0.0 and 1.0",
		},
		{
			name: "Missing namespace",
			setupFunc: func() *DeploymentInfo {
				info := NewDeploymentInfo("", "app", "Pod", DeploymentMethodArgoCD, 0.95)
				return info
			},
			wantErr: true,
			errMsg:  "namespace is required",
		},
		{
			name: "Missing resource name",
			setupFunc: func() *DeploymentInfo {
				info := NewDeploymentInfo("default", "", "Pod", DeploymentMethodArgoCD, 0.95)
				return info
			},
			wantErr: true,
			errMsg:  "resource_name is required",
		},
		{
			name: "Missing resource kind",
			setupFunc: func() *DeploymentInfo {
				info := NewDeploymentInfo("default", "app", "", DeploymentMethodArgoCD, 0.95)
				return info
			},
			wantErr: true,
			errMsg:  "resource_kind is required",
		},
		{
			name: "Missing detected_at timestamp",
			setupFunc: func() *DeploymentInfo {
				info := NewDeploymentInfo("default", "app", "Pod", DeploymentMethodArgoCD, 0.95)
				info.DetectedAt = time.Time{}
				return info
			},
			wantErr: true,
			errMsg:  "detected_at timestamp is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := tt.setupFunc()
			err := info.Validate()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestDeploymentInfo_String(t *testing.T) {
	info := NewDeploymentInfo("default", "my-app", "Pod", DeploymentMethodArgoCD, 0.95)

	str := info.String()
	assert.Contains(t, str, "default/my-app")
	assert.Contains(t, str, "Pod")
	assert.Contains(t, str, "argocd")
	assert.Contains(t, str, "0.95")
}

func TestDeploymentInfo_JSON(t *testing.T) {
	original := NewDeploymentInfo("default", "my-app", "Pod", DeploymentMethodArgoCD, 0.95)
	original.Source = "annotation"
	original.SetDetail("app_name", "test-app")

	// Test ToJSON
	jsonData, err := original.ToJSON()
	require.NoError(t, err)
	assert.NotNil(t, jsonData)

	// Verify JSON contains expected fields
	var jsonMap map[string]interface{}
	err = json.Unmarshal(jsonData, &jsonMap)
	require.NoError(t, err)
	assert.Equal(t, "argocd", jsonMap["method"])
	assert.Equal(t, 0.95, jsonMap["confidence"])
	assert.Equal(t, "annotation", jsonMap["source"])
	assert.Equal(t, "default", jsonMap["namespace"])
	assert.Equal(t, "my-app", jsonMap["resource_name"])
	assert.Equal(t, "Pod", jsonMap["resource_kind"])

	// Test FromJSON
	restored, err := FromJSON(jsonData)
	require.NoError(t, err)
	assert.Equal(t, original.Method, restored.Method)
	assert.Equal(t, original.Confidence, restored.Confidence)
	assert.Equal(t, original.Source, restored.Source)
	assert.Equal(t, original.Namespace, restored.Namespace)
	assert.Equal(t, original.ResourceName, restored.ResourceName)
	assert.Equal(t, original.ResourceKind, restored.ResourceKind)
	assert.Equal(t, "test-app", restored.GetDetail("app_name"))
}

func TestFromJSON_InvalidJSON(t *testing.T) {
	invalidJSON := []byte(`{"method": "invalid"}`)

	info, err := FromJSON(invalidJSON)
	require.Error(t, err)
	assert.Nil(t, info)
	assert.Contains(t, err.Error(), "invalid deployment info")
}

func TestDeploymentInfo_JSONMarshaling(t *testing.T) {
	tests := []struct {
		name   string
		info   *DeploymentInfo
		verify func(*testing.T, []byte)
	}{
		{
			name: "ArgoCD deployment",
			info: func() *DeploymentInfo {
				info := NewDeploymentInfo("default", "app", "Pod", DeploymentMethodArgoCD, 0.95)
				info.Source = "annotation"
				info.SetDetail("tracking_id", "abc123")
				return info
			}(),
			verify: func(t *testing.T, data []byte) {
				var result map[string]interface{}
				require.NoError(t, json.Unmarshal(data, &result))
				assert.Equal(t, "argocd", result["method"])
				assert.Equal(t, "annotation", result["source"])
			},
		},
		{
			name: "Helm deployment",
			info: func() *DeploymentInfo {
				info := NewDeploymentInfo("kube-system", "nginx", "Deployment", DeploymentMethodHelm, 0.90)
				info.Source = "annotation"
				info.SetDetail("release_name", "nginx-release")
				return info
			}(),
			verify: func(t *testing.T, data []byte) {
				var result map[string]interface{}
				require.NoError(t, json.Unmarshal(data, &result))
				assert.Equal(t, "helm", result["method"])
				assert.Equal(t, "kube-system", result["namespace"])
			},
		},
		{
			name: "Operator deployment",
			info: func() *DeploymentInfo {
				info := NewDeploymentInfo("monitoring", "prometheus", "StatefulSet", DeploymentMethodOperator, 0.80)
				info.Source = "label"
				info.SetDetail("operator", "prometheus-operator")
				return info
			}(),
			verify: func(t *testing.T, data []byte) {
				var result map[string]interface{}
				require.NoError(t, json.Unmarshal(data, &result))
				assert.Equal(t, "operator", result["method"])
				assert.Equal(t, "label", result["source"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.info)
			require.NoError(t, err)
			tt.verify(t, data)

			// Verify round-trip
			var restored DeploymentInfo
			require.NoError(t, json.Unmarshal(data, &restored))
			assert.Equal(t, tt.info.Method, restored.Method)
			assert.Equal(t, tt.info.Confidence, restored.Confidence)
		})
	}
}
