package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
)

func validateRepairParams(action string, params map[string]any) error {
	switch action {
	case actionRestartPod:
		_, err := stringParam(params, "deployment")
		return err
	case actionRollbackDeployment:
		return validateRollbackParams(params)
	case actionScaleDeployment:
		return validateScaleParams(params)
	case actionDeleteResource:
		return validateObjectRef(params)
	case actionApplyResource:
		return validateApplyParams(params)
	case actionExecCommand:
		return validateExecParams(params)
	case actionPatchResource:
		return validatePatchParams(params)
	default:
		return fmt.Errorf("action %q is not implemented", action)
	}
}

func validateRollbackParams(params map[string]any) error {
	if _, err := stringParam(params, "deployment"); err != nil {
		return err
	}
	if _, ok := params["revision"]; !ok {
		return nil
	}
	revision, err := numberParam(params, "revision")
	if err != nil {
		return err
	}
	if revision < 1 {
		return errors.New("param \"revision\" must be >= 1")
	}
	return nil
}

func validateScaleParams(params map[string]any) error {
	if _, err := stringParam(params, "deployment"); err != nil {
		return err
	}
	replicas, err := numberParam(params, "replicas")
	if err != nil {
		return err
	}
	if replicas < 0 {
		return errors.New("param \"replicas\" must be >= 0")
	}
	return nil
}

func validateObjectRef(params map[string]any) error {
	apiVersion, err := stringParam(params, "apiVersion")
	if err != nil {
		return err
	}
	if _, _, err := splitAPIVersion(apiVersion); err != nil {
		return err
	}
	if _, err := stringParam(params, "kind"); err != nil {
		return err
	}
	_, err = stringParam(params, "name")
	return err
}

func validateApplyParams(params map[string]any) error {
	raw, err := manifestParam(params)
	if err != nil {
		return err
	}
	obj := &unstructured.Unstructured{Object: raw}
	if obj.GetAPIVersion() == "" || obj.GetKind() == "" || obj.GetName() == "" {
		return errors.New("param \"manifest\" requires apiVersion, kind, and metadata.name")
	}
	_, _, err = splitAPIVersion(obj.GetAPIVersion())
	return err
}

func validateExecParams(params map[string]any) error {
	selector, err := stringParam(params, "podSelector")
	if err != nil {
		return err
	}
	if _, err := labels.Parse(selector); err != nil {
		return fmt.Errorf("param \"podSelector\" is invalid: %w", err)
	}
	if _, err := stringSliceParam(params, "command"); err != nil {
		return err
	}
	if _, ok := params["container"]; !ok {
		return nil
	}
	_, err = stringParam(params, "container")
	return err
}

func validatePatchParams(params map[string]any) error {
	if err := validateObjectRef(params); err != nil {
		return err
	}
	patch, err := objectParam(params, "patch")
	if err != nil {
		return err
	}
	if len(patch) == 0 {
		return errors.New("param \"patch\" must not be empty")
	}
	return nil
}

func stringParam(params map[string]any, key string) (string, error) {
	if params == nil {
		return "", fmt.Errorf("missing param %q", key)
	}
	value, ok := params[key]
	if !ok {
		return "", fmt.Errorf("missing param %q", key)
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("param %q must be a non-empty string", key)
	}
	return text, nil
}

func numberParam(params map[string]any, key string) (int, error) {
	if params == nil {
		return 0, fmt.Errorf("missing param %q", key)
	}
	value, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("missing param %q", key)
	}
	return asInt(key, value)
}

func asInt(key string, value any) (int, error) {
	switch n := value.(type) {
	case int:
		return n, nil
	case int32:
		return int(n), nil
	case int64:
		return boundedInt(key, n)
	case float64:
		return floatInt(key, n)
	case json.Number:
		parsed, err := n.Int64()
		if err != nil {
			return 0, fmt.Errorf("param %q must be an integer: %w", key, err)
		}
		return boundedInt(key, parsed)
	default:
		return 0, fmt.Errorf("param %q must be an integer", key)
	}
}

func floatInt(key string, n float64) (int, error) {
	if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
		return 0, fmt.Errorf("param %q must be an integer", key)
	}
	if n > math.MaxInt32 || n < math.MinInt32 {
		return 0, fmt.Errorf("param %q is out of range", key)
	}
	return boundedInt(key, int64(n))
}

func boundedInt(key string, n int64) (int, error) {
	if n > math.MaxInt32 || n < math.MinInt32 {
		return 0, fmt.Errorf("param %q is out of range", key)
	}
	return int(n), nil
}

func stringSliceParam(params map[string]any, key string) ([]string, error) {
	if params == nil {
		return nil, fmt.Errorf("missing param %q", key)
	}
	value, ok := params[key]
	if !ok {
		return nil, fmt.Errorf("missing param %q", key)
	}
	switch items := value.(type) {
	case []string:
		return copyNonEmptyStrings(key, items)
	case []any:
		out := make([]string, len(items))
		for i, item := range items {
			text, ok := item.(string)
			if !ok || text == "" {
				return nil, fmt.Errorf("param %q must be an array of non-empty strings", key)
			}
			out[i] = text
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("param %q must be an array of non-empty strings", key)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("param %q must be an array of non-empty strings", key)
	}
}

func copyNonEmptyStrings(key string, items []string) ([]string, error) {
	if len(items) == 0 {
		return nil, fmt.Errorf("param %q must be an array of non-empty strings", key)
	}
	out := make([]string, len(items))
	for i, item := range items {
		if item == "" {
			return nil, fmt.Errorf("param %q must be an array of non-empty strings", key)
		}
		out[i] = item
	}
	return out, nil
}

func objectParam(params map[string]any, key string) (map[string]any, error) {
	if params == nil {
		return nil, fmt.Errorf("missing param %q", key)
	}
	value, ok := params[key]
	if !ok {
		return nil, fmt.Errorf("missing param %q", key)
	}
	obj, ok := value.(map[string]any)
	if !ok || obj == nil {
		return nil, fmt.Errorf("param %q must be an object", key)
	}
	return obj, nil
}

func manifestParam(params map[string]any) (map[string]any, error) {
	if params == nil {
		return nil, errors.New("missing param \"manifest\"")
	}
	value, ok := params["manifest"]
	if !ok {
		return nil, errors.New("missing param \"manifest\"")
	}
	switch manifest := value.(type) {
	case map[string]any:
		if manifest == nil {
			return nil, errors.New("param \"manifest\" must be an object")
		}
		return manifest, nil
	case string:
		var decoded map[string]any
		if err := json.Unmarshal([]byte(manifest), &decoded); err != nil {
			return nil, fmt.Errorf("param \"manifest\" must be an object: %w", err)
		}
		return decoded, nil
	default:
		return nil, errors.New("param \"manifest\" must be an object")
	}
}

func splitAPIVersion(apiVersion string) (string, string, error) {
	group, version, ok := strings.Cut(apiVersion, "/")
	if !ok {
		if apiVersion == "" {
			return "", "", fmt.Errorf("invalid apiVersion %q", apiVersion)
		}
		return "", apiVersion, nil
	}
	if group == "" || version == "" || strings.Contains(version, "/") {
		return "", "", fmt.Errorf("invalid apiVersion %q", apiVersion)
	}
	return group, version, nil
}
