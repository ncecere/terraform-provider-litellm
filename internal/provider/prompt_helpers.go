package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	defaultPromptEnvironment = "development"
	promptImportVersion      = "v1"
	promptLegacyImportPrefix = "legacy."
)

type promptAPIObject struct {
	PromptID    string
	Environment string
	Version     int64
	HasVersion  bool
	CreatedAt   *string
	UpdatedAt   *string
	Params      map[string]interface{}
	Info        map[string]interface{}
}

func promptImportID(promptID, environment string) string {
	return promptImportVersion + "." +
		base64.RawURLEncoding.EncodeToString([]byte(promptID)) + "." +
		base64.RawURLEncoding.EncodeToString([]byte(environment))
}

func legacyPromptImportID(promptID string) string {
	return promptLegacyImportPrefix + base64.RawURLEncoding.EncodeToString([]byte(promptID))
}

func parsePromptImportID(value string) (promptID, environment string, err error) {
	if strings.HasPrefix(value, promptLegacyImportPrefix) {
		encoded := strings.TrimPrefix(value, promptLegacyImportPrefix)
		decoded, decodeErr := base64.RawURLEncoding.DecodeString(encoded)
		if decodeErr != nil || len(decoded) == 0 || !utf8.Valid(decoded) || legacyPromptImportID(string(decoded)) != value {
			return "", "", fmt.Errorf("invalid escaped legacy prompt import ID")
		}
		return string(decoded), defaultPromptEnvironment, nil
	}
	parts := strings.Split(value, ".")
	if len(parts) == 3 && parts[0] == promptImportVersion {
		promptBytes, promptErr := base64.RawURLEncoding.DecodeString(parts[1])
		environmentBytes, environmentErr := base64.RawURLEncoding.DecodeString(parts[2])
		if promptErr != nil || environmentErr != nil || len(promptBytes) == 0 || len(environmentBytes) == 0 || !utf8.Valid(promptBytes) || !utf8.Valid(environmentBytes) {
			return "", "", fmt.Errorf("invalid prompt import ID")
		}
		promptID, environment = string(promptBytes), string(environmentBytes)
		if promptImportID(promptID, environment) != value {
			return "", "", fmt.Errorf("invalid non-canonical prompt import ID")
		}
		return promptID, environment, nil
	}
	if value == "" {
		return "", "", fmt.Errorf("prompt import ID must not be empty")
	}
	return value, defaultPromptEnvironment, nil
}

func isPromptAbsentError(err error) bool {
	return IsNotFoundError(err) || IsAPIErrorStatus(err, http.StatusBadRequest)
}

func promptScopedExists(ctx context.Context, client *Client, promptID, environment string) (bool, error) {
	var info map[string]interface{}
	err := client.DoRequestWithResponse(ctx, http.MethodGet, promptEndpoint(promptID, environment, nil), nil, &info)
	if err == nil {
		return true, nil
	}
	if !IsAPIErrorStatus(err, http.StatusBadRequest) && !IsAPIErrorStatus(err, http.StatusNotFound) {
		return false, err
	}
	// v1.98 uses 400 both for ordinary absence and for authorization/visibility
	// failures. The scoped versions route is the bounded authoritative DB check:
	// only a successful empty envelope proves that Create may use this identity.
	versions, versionsErr := fetchEnvelopeListObjects(ctx, client, promptVersionsEndpoint(promptID, environment), "prompts", "prompt version item")
	if versionsErr != nil {
		// Unlike info, v1.98's versions route uses 404 for an absent scoped
		// history. Other 4xx responses remain ambiguous and fail closed.
		if IsAPIErrorStatus(versionsErr, http.StatusNotFound) {
			return false, nil
		}
		return false, versionsErr
	}
	return len(versions) > 0, nil
}

func promptEnvironment(value string) string {
	if value == "" {
		return defaultPromptEnvironment
	}
	return value
}

func promptPath(promptID string, version *int64) string {
	lookupID := promptID
	if version != nil {
		lookupID = fmt.Sprintf("%s.v%d", promptID, *version)
	}
	return fmt.Sprintf("/prompts/%s", url.PathEscape(lookupID))
}

func promptEndpoint(promptID, environment string, version *int64) string {
	query := url.Values{}
	query.Set("environment", promptEnvironment(environment))
	return promptPath(promptID, version) + "?" + query.Encode()
}

func promptVersionsEndpoint(promptID, environment string) string {
	query := url.Values{}
	query.Set("environment", promptEnvironment(environment))
	return promptPath(promptID, nil) + "/versions?" + query.Encode()
}

func promptListEndpoint(environment string, configured bool) string {
	if !configured {
		return "/prompts/list"
	}
	query := url.Values{}
	query.Set("environment", promptEnvironment(environment))
	return "/prompts/list?" + query.Encode()
}

func optionalPromptAPIString(object map[string]interface{}, field string) (*string, error) {
	value, exists := object[field]
	if !exists || value == nil {
		return nil, nil
	}
	text, ok := value.(string)
	if !ok {
		return nil, fmt.Errorf("prompt response field %q must be a string or null", field)
	}
	return &text, nil
}

func promptObject(raw map[string]interface{}, wrapped bool, expectedPromptID, expectedEnvironment string) (promptAPIObject, error) {
	var result promptAPIObject
	object := raw
	if wrapped {
		value, exists := raw["prompt_spec"]
		if !exists || value == nil {
			return result, fmt.Errorf("prompt response omitted required prompt_spec")
		}
		var ok bool
		object, ok = value.(map[string]interface{})
		if !ok {
			return result, fmt.Errorf("prompt response field %q must be an object", "prompt_spec")
		}
	}

	promptID, ok := object["prompt_id"].(string)
	if !ok || promptID == "" {
		return result, fmt.Errorf("prompt response omitted a non-empty prompt_id")
	}
	if expectedPromptID != "" && promptID != expectedPromptID {
		return result, fmt.Errorf("prompt response identity did not match the requested prompt")
	}
	result.PromptID = promptID

	params, ok := object["litellm_params"].(map[string]interface{})
	if !ok || params == nil {
		return result, fmt.Errorf("prompt response field %q must be an object", "litellm_params")
	}
	result.Params = params
	if rawInfo, exists := object["prompt_info"]; exists && rawInfo != nil {
		info, valid := rawInfo.(map[string]interface{})
		if !valid {
			return result, fmt.Errorf("prompt response field %q must be an object or null", "prompt_info")
		}
		result.Info = info
	}

	environment := ""
	topEnvironmentPresent := false
	if value, exists := object["environment"]; exists && value != nil {
		var valid bool
		environment, valid = value.(string)
		if !valid || environment == "" {
			return result, fmt.Errorf("prompt response field %q must be a non-empty string or null", "environment")
		}
		topEnvironmentPresent = true
	}
	infoEnvironment := ""
	if result.Info != nil {
		if value, exists := result.Info["environment"]; exists && value != nil {
			var valid bool
			infoEnvironment, valid = value.(string)
			if !valid || infoEnvironment == "" {
				return result, fmt.Errorf("prompt response field %q must be a non-empty string or null", "prompt_info.environment")
			}
		}
	}
	if topEnvironmentPresent && infoEnvironment != "" && environment != infoEnvironment {
		return result, fmt.Errorf("prompt response returned conflicting environment identities")
	}
	if expectedEnvironment != "" && !topEnvironmentPresent {
		return result, fmt.Errorf("prompt response omitted required top-level environment identity")
	}
	if environment == "" {
		environment = infoEnvironment
	}
	environment = promptEnvironment(environment)
	if expectedEnvironment != "" && environment != promptEnvironment(expectedEnvironment) {
		return result, fmt.Errorf("prompt response environment did not match the requested environment")
	}
	result.Environment = environment

	if value, exists := object["version"]; exists && value != nil {
		version, versionErr := exactInt64FromAPI(value)
		if versionErr != nil || version <= 0 {
			return result, fmt.Errorf("prompt response field %q must be a positive integer", "version")
		}
		result.Version = version
		result.HasVersion = true
	}
	var timestampErr error
	result.CreatedAt, timestampErr = optionalPromptAPIString(object, "created_at")
	if timestampErr != nil {
		return result, timestampErr
	}
	result.UpdatedAt, timestampErr = optionalPromptAPIString(object, "updated_at")
	if timestampErr != nil {
		return result, timestampErr
	}
	return result, nil
}
