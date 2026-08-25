package provider

import (
	"encoding/json"
	"fmt"
)

var guardrailReservedParams = map[string]struct{}{
	"guardrail":  {},
	"mode":       {},
	"default_on": {},
}

type guardrailAPIObject struct {
	ID        string
	Name      string
	Params    map[string]interface{}
	Info      map[string]interface{}
	CreatedAt *string
	UpdatedAt *string
}

func optionalAPIString(object map[string]interface{}, field string) (*string, error) {
	value, exists := object[field]
	if !exists || value == nil {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("guardrail response field %q must be a string or null", field)
	}
	return &text, nil
}

func decodeGuardrailAPIObject(object map[string]interface{}, expectedID string, requireID bool) (guardrailAPIObject, error) {
	var result guardrailAPIObject
	id, err := optionalAPIString(object, "guardrail_id")
	if err != nil {
		return result, err
	}
	if id != nil {
		result.ID = *id
	}
	if requireID && result.ID == "" {
		return result, fmt.Errorf("guardrail response omitted a non-empty guardrail_id")
	}
	if expectedID != "" && result.ID != expectedID {
		return result, fmt.Errorf("guardrail response identity did not match the requested guardrail")
	}

	name, err := optionalAPIString(object, "guardrail_name")
	if err != nil {
		return result, err
	}
	if name == nil || *name == "" {
		return result, fmt.Errorf("guardrail response omitted a non-empty guardrail_name")
	}
	result.Name = *name

	if raw, exists := object["litellm_params"]; exists && raw != nil {
		params, ok := raw.(map[string]interface{})
		if !ok {
			return result, fmt.Errorf("guardrail response field %q must be an object or null", "litellm_params")
		}
		result.Params = params
	}
	if raw, exists := object["guardrail_info"]; exists && raw != nil {
		info, ok := raw.(map[string]interface{})
		if !ok {
			return result, fmt.Errorf("guardrail response field %q must be an object or null", "guardrail_info")
		}
		result.Info = info
	}
	result.CreatedAt, err = optionalAPIString(object, "created_at")
	if err != nil {
		return result, err
	}
	result.UpdatedAt, err = optionalAPIString(object, "updated_at")
	if err != nil {
		return result, err
	}
	return result, nil
}

func guardrailModeFromAPI(params map[string]interface{}) (string, bool, error) {
	value, exists := params["mode"]
	if !exists || value == nil {
		return "", false, nil
	}
	if err := validateGuardrailModeValue(value); err != nil {
		return "", false, fmt.Errorf("guardrail response field %q must be a mode string, string array, or valid Mode object", "litellm_params.mode")
	}
	if text, ok := value.(string); ok {
		return text, true, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", false, fmt.Errorf("guardrail response field %q could not be encoded", "litellm_params.mode")
	}
	return string(encoded), true, nil
}

func guardrailAdditionalParams(params map[string]interface{}) map[string]interface{} {
	additional := make(map[string]interface{})
	for key, value := range params {
		if _, reserved := guardrailReservedParams[key]; !reserved {
			additional[key] = value
		}
	}
	return additional
}

func canonicalGuardrailJSONObject(object map[string]interface{}, field string) (string, error) {
	encoded, err := json.Marshal(object)
	if err != nil {
		return "", fmt.Errorf("guardrail response field %q could not be encoded", field)
	}
	return string(encoded), nil
}

func isGuardrailMaskedAPIString(value string) bool {
	return value == "*****" || liteLLMCredentialMask.MatchString(value)
}

func containsGuardrailMaskedValue(value interface{}) bool {
	switch typed := value.(type) {
	case string:
		return isGuardrailMaskedAPIString(typed)
	case map[string]interface{}:
		for _, child := range typed {
			if containsGuardrailMaskedValue(child) {
				return true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if containsGuardrailMaskedValue(child) {
				return true
			}
		}
	}
	return false
}

func restoreGuardrailMaskedLeaves(remote, prior interface{}, field string) (interface{}, error) {
	if !containsGuardrailMaskedValue(remote) {
		return remote, nil
	}
	if text, ok := remote.(string); ok && isGuardrailMaskedAPIString(text) {
		priorText, ok := prior.(string)
		if !ok {
			return nil, fmt.Errorf("guardrail response field %q contains a masked value without corresponding prior plaintext state", field)
		}
		if isGuardrailMaskedAPIString(priorText) {
			return nil, fmt.Errorf("guardrail response field %q cannot recover plaintext from a previously stored masked value", field)
		}
		return priorText, nil
	}
	switch typed := remote.(type) {
	case map[string]interface{}:
		priorObject, ok := prior.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("guardrail response field %q changed shape while masked", field)
		}
		result := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			restored, err := restoreGuardrailMaskedLeaves(child, priorObject[key], field+"."+key)
			if err != nil {
				return nil, err
			}
			result[key] = restored
		}
		return result, nil
	case []interface{}:
		priorArray, ok := prior.([]interface{})
		if !ok || len(priorArray) != len(typed) {
			return nil, fmt.Errorf("guardrail response field %q changed shape while masked", field)
		}
		result := make([]interface{}, len(typed))
		for index, child := range typed {
			restored, err := restoreGuardrailMaskedLeaves(child, priorArray[index], fmt.Sprintf("%s[%d]", field, index))
			if err != nil {
				return nil, err
			}
			result[index] = restored
		}
		return result, nil
	default:
		return remote, nil
	}
}

func reconcileOwnedGuardrailParams(apiParams map[string]interface{}, priorJSON string) (string, error) {
	configured, err := decodeRequestJSONObject(priorJSON, "litellm_params")
	if err != nil {
		return "", err
	}
	reconciled := make(map[string]interface{})
	for key, configuredValue := range configured {
		if _, reserved := guardrailReservedParams[key]; reserved {
			continue
		}
		apiValue, exists := apiParams[key]
		if !exists {
			continue
		}
		apiValue = removeUnconfiguredGuardrailNulls(apiValue, configuredValue)
		apiValue, err = restoreGuardrailMaskedLeaves(apiValue, configuredValue, "litellm_params."+key)
		if err != nil {
			return "", err
		}
		reconciled[key] = apiValue
	}
	observed, err := canonicalGuardrailJSONObject(reconciled, "litellm_params")
	if err != nil {
		return "", err
	}
	if jsonSemanticallyEqual(priorJSON, observed) {
		return priorJSON, nil
	}
	return observed, nil
}
