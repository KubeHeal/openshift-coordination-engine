// Package policy implements the per-application SelfHealingPolicy engine.
// Application names, alerts, models, and repairs come from the policy file.
// Adding an application that uses the standard action types does not require
// a coordination-engine code change.
package policy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

const (
	actionRestartPod         = "restart_pod"
	actionRollbackDeployment = "rollback_deployment"
	actionScaleDeployment    = "scale_deployment"
	actionDeleteResource     = "delete_resource"
	actionApplyResource      = "apply_resource"
	actionExecCommand        = "exec_command"
	actionPatchResource      = "patch_resource"
)

// StandardActionTypes is the repair registry implemented by this engine.
// A new application can use these types without a code change. A new action
// type is the only reason this package needs a code change.
var StandardActionTypes = []string{
	actionRestartPod,
	actionRollbackDeployment,
	actionScaleDeployment,
	actionDeleteResource,
	actionApplyResource,
	actionExecCommand,
	actionPatchResource,
}

var standardActions = map[string]struct{}{
	actionRestartPod:         {},
	actionRollbackDeployment: {},
	actionScaleDeployment:    {},
	actionDeleteResource:     {},
	actionApplyResource:      {},
	actionExecCommand:        {},
	actionPatchResource:      {},
}

var (
	featureColumnPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	alertNamePattern     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// Document is the JSON file mounted from the self-healing-policies ConfigMap.
type Document struct {
	ActionTypes []string `json:"actionTypes"`
	Policies    []Policy `json:"policies"`
}

// Policy is one application's model-gated repair contract.
type Policy struct {
	Name      string    `json:"name"`
	Namespace string    `json:"namespace"`
	Alerts    []string  `json:"alerts"`
	Model     ModelSpec `json:"model"`
	Repairs   []Repair  `json:"repairs"`
}

// ModelSpec selects the KServe model and the feature vector it was trained on.
// Namespace, Service, and FeatureQueries are optional. When they are omitted,
// the engine derives the service URL and uses the built-in PromQL catalog.
type ModelSpec struct {
	Name           string            `json:"name"`
	Namespace      string            `json:"namespace,omitempty"`
	Service        string            `json:"service,omitempty"`
	FeatureColumns []string          `json:"featureColumns"`
	FeatureQueries map[string]string `json:"featureQueries,omitempty"`
}

// Repair maps one alert name to a standard action and its parameters.
type Repair struct {
	Alert  string         `json:"alert"`
	Action string         `json:"action"`
	Params map[string]any `json:"params"`
}

// Validate checks that every policy can be executed without application-specific code.
func (d *Document) Validate() error {
	if d == nil {
		return errors.New("policy document is empty")
	}
	allowed, err := validateActionTypes(d.ActionTypes)
	if err != nil {
		return err
	}
	return validatePolicies(d.Policies, allowed)
}

func validateActionTypes(actionTypes []string) (map[string]struct{}, error) {
	if len(actionTypes) == 0 {
		return nil, errors.New("actionTypes must list at least one implemented action type")
	}
	allowed := make(map[string]struct{}, len(actionTypes))
	for _, action := range actionTypes {
		if _, ok := standardActions[action]; !ok {
			return nil, fmt.Errorf("action type %q is not implemented", action)
		}
		if _, dup := allowed[action]; dup {
			return nil, fmt.Errorf("duplicate action type %q", action)
		}
		allowed[action] = struct{}{}
	}
	return allowed, nil
}

func validatePolicies(policies []Policy, allowed map[string]struct{}) error {
	names := make(map[string]struct{}, len(policies))
	claimed := make(map[string]string)
	for i := range policies {
		policy := &policies[i]
		if err := validatePolicy(policy, allowed); err != nil {
			return fmt.Errorf("policies[%d] %q: %w", i, policy.Name, err)
		}
		if _, dup := names[policy.Name]; dup {
			return fmt.Errorf("duplicate policy name %q", policy.Name)
		}
		names[policy.Name] = struct{}{}
		for _, alertName := range policy.Alerts {
			key := policy.Namespace + "\x00" + alertName
			if other, ok := claimed[key]; ok {
				return fmt.Errorf("alert %q in namespace %q is claimed by both %q and %q", alertName, policy.Namespace, other, policy.Name)
			}
			claimed[key] = policy.Name
		}
	}
	return nil
}

func validatePolicy(policy *Policy, allowed map[string]struct{}) error {
	if err := validatePolicyIdentity(policy); err != nil {
		return err
	}
	if err := validateAlerts(policy.Alerts); err != nil {
		return err
	}
	if err := validateModel(&policy.Model); err != nil {
		return err
	}
	return validateRepairs(policy, allowed)
}

func validatePolicyIdentity(policy *Policy) error {
	if errs := validation.IsDNS1123Subdomain(policy.Name); len(errs) > 0 {
		return fmt.Errorf("name is invalid: %s", strings.Join(errs, ", "))
	}
	if errs := validation.IsDNS1123Label(policy.Namespace); len(errs) > 0 {
		return fmt.Errorf("namespace is invalid: %s", strings.Join(errs, ", "))
	}
	return nil
}

func validateAlerts(alerts []string) error {
	if len(alerts) == 0 {
		return errors.New("alerts must contain at least one alert name")
	}
	seen := make(map[string]struct{}, len(alerts))
	for _, alertName := range alerts {
		if !alertNamePattern.MatchString(alertName) {
			return fmt.Errorf("alert %q is invalid", alertName)
		}
		if _, dup := seen[alertName]; dup {
			return fmt.Errorf("duplicate alert %q", alertName)
		}
		seen[alertName] = struct{}{}
	}
	return nil
}

func validateModel(model *ModelSpec) error {
	if err := validateModelLocation(model); err != nil {
		return err
	}
	return validateFeatureColumns(model)
}

func validateModelLocation(model *ModelSpec) error {
	if errs := validation.IsDNS1123Subdomain(model.Name); len(errs) > 0 {
		return fmt.Errorf("model.name is invalid: %s", strings.Join(errs, ", "))
	}
	if model.Namespace != "" {
		if errs := validation.IsDNS1123Label(model.Namespace); len(errs) > 0 {
			return fmt.Errorf("model.namespace is invalid: %s", strings.Join(errs, ", "))
		}
	}
	if _, err := resolvedService(model); err != nil {
		return err
	}
	return nil
}

func validateFeatureColumns(model *ModelSpec) error {
	if len(model.FeatureColumns) == 0 {
		return errors.New("model.featureColumns must contain at least one column")
	}
	seen := make(map[string]struct{}, len(model.FeatureColumns))
	for _, column := range model.FeatureColumns {
		if !featureColumnPattern.MatchString(column) {
			return fmt.Errorf("feature column %q is invalid", column)
		}
		if _, dup := seen[column]; dup {
			return fmt.Errorf("duplicate feature column %q", column)
		}
		seen[column] = struct{}{}
	}
	for column, query := range model.FeatureQueries {
		if !featureColumnPattern.MatchString(column) {
			return fmt.Errorf("feature query column %q is invalid", column)
		}
		if strings.TrimSpace(query) == "" {
			return fmt.Errorf("feature query %q is empty", column)
		}
	}
	return nil
}

func validateRepairs(policy *Policy, allowed map[string]struct{}) error {
	if len(policy.Repairs) == 0 {
		return errors.New("repairs must contain at least one repair")
	}
	watched := make(map[string]struct{}, len(policy.Alerts))
	for _, alertName := range policy.Alerts {
		watched[alertName] = struct{}{}
	}
	seen := make(map[string]struct{}, len(policy.Repairs))
	for i := range policy.Repairs {
		repair := policy.Repairs[i]
		if err := validateRepair(repair, watched, allowed); err != nil {
			return fmt.Errorf("repairs[%d]: %w", i, err)
		}
		if _, dup := seen[repair.Alert]; dup {
			return fmt.Errorf("duplicate repair for alert %q", repair.Alert)
		}
		seen[repair.Alert] = struct{}{}
	}
	return nil
}

func validateRepair(repair Repair, watched, allowed map[string]struct{}) error {
	if _, ok := watched[repair.Alert]; !ok {
		return fmt.Errorf("alert %q is not in alerts", repair.Alert)
	}
	if _, ok := allowed[repair.Action]; !ok {
		return fmt.Errorf("action %q is not in actionTypes", repair.Action)
	}
	if _, ok := standardActions[repair.Action]; !ok {
		return fmt.Errorf("action %q is not implemented", repair.Action)
	}
	if repair.Params == nil {
		return errors.New("params is required")
	}
	return validateRepairParams(repair.Action, repair.Params)
}

// Match returns the policy that owns this namespace and alert name.
func (d *Document) Match(namespace, alertName string) *Policy {
	if d == nil || namespace == "" || alertName == "" {
		return nil
	}
	for i := range d.Policies {
		policy := &d.Policies[i]
		if policy.Namespace != namespace {
			continue
		}
		if containsString(policy.Alerts, alertName) {
			return policy
		}
	}
	return nil
}

func (p *Policy) repairFor(alertName string) (Repair, bool) {
	if p == nil {
		return Repair{}, false
	}
	for _, repair := range p.Repairs {
		if repair.Alert == alertName {
			return repair, true
		}
	}
	return Repair{}, false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
