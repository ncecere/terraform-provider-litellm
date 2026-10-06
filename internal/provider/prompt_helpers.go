package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
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
	return IsAPIErrorStatus(err, http.StatusNotFound) || IsAPIErrorStatus(err, http.StatusBadRequest)
}

func promptScopedExists(ctx context.Context, client *Client, promptID, environment string) (bool, error) {
	var info map[string]interface{}
	err := client.DoRequestWithResponse(ctx, http.MethodGet, promptEndpoint(promptID, environment, nil), nil, &info)
	if err == nil {
		// On a multi-worker proxy another worker's in-memory registry can still
		// serve a prompt that was just deleted from the database. For a
		// database-backed prompt, confirm with the database-only version history;
		// a config prompt exists only in the registry and always counts.
		observed, decodeErr := promptObject(info, true, promptID, promptEnvironment(environment))
		if decodeErr == nil && observed.Info != nil && observed.Info["prompt_type"] == "db" {
			if absent, historyErr := promptScopedHistoryAbsent(ctx, client, promptID, environment); historyErr == nil && absent {
				return false, nil
			}
		}
		return true, nil
	}
	if !IsAPIErrorStatus(err, http.StatusBadRequest) && !IsAPIErrorStatus(err, http.StatusNotFound) {
		return false, err
	}
	// The info route uses 400 both for ordinary absence and for
	// authorization/visibility failures. The scoped versions route is the
	// bounded authoritative database check. LiteLLM's POST /prompts has no
	// duplicate check, so only LiteLLM's own "No versions found" 404 proves that
	// Create may use this identity; a generic 404, an empty list (which LiteLLM
	// never sends), or any other outcome fails closed.
	versions, versionsErr := fetchEnvelopeListObjects(ctx, client, promptVersionsEndpoint(promptID, environment), "prompts", "prompt version item")
	if versionsErr != nil {
		if isPromptVersionsNotFoundError(versionsErr) {
			return false, nil
		}
		return false, versionsErr
	}
	if len(versions) == 0 {
		return false, fmt.Errorf("prompt version history response was empty instead of LiteLLM's absence response")
	}
	return true, nil
}

// liteLLMPromptVersionsNotFoundMarker is the detail LiteLLM's scoped versions
// route returns with HTTP 404 when no database version exists (1.98.0 and
// 1.104.0: "No versions found for prompt ID <id>").
var liteLLMPromptVersionsNotFoundMarker = []byte("No versions found for prompt ID")

// classifyPromptVersionsNotFoundBody inspects a 404 body once, at the client
// boundary, so callers never need the raw body.
func classifyPromptVersionsNotFoundBody(body []byte) bool {
	return bytes.Contains(body, liteLLMPromptVersionsNotFoundMarker)
}

func isPromptVersionsNotFoundError(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound && apiErr.promptVersionsNotFound
}

// promptScopedHistoryAbsent reports authoritative database absence of a prompt
// environment through the versions route. Only LiteLLM's own "No versions
// found" 404 proves absence: LiteLLM never answers 200 with an empty list, so
// an empty list, a generic 404, or any other outcome is not proof.
func promptScopedHistoryAbsent(ctx context.Context, client *Client, promptID, environment string) (bool, error) {
	_, err := fetchEnvelopeListObjects(ctx, client, promptVersionsEndpoint(promptID, environment), "prompts", "prompt version item")
	if err != nil {
		if isPromptVersionsNotFoundError(err) {
			return true, nil
		}
		return false, err
	}
	return false, nil
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
	return endpointWithPathSegment("/prompts/", lookupID, "")
}

func promptEndpoint(promptID, environment string, version *int64) string {
	query := url.Values{}
	query.Set("environment", promptEnvironment(environment))
	return endpointWithQuery(promptPath(promptID, version), query)
}

func promptVersionsEndpoint(promptID, environment string) string {
	query := url.Values{}
	query.Set("environment", promptEnvironment(environment))
	return endpointWithQuery(endpointWithPathSegment("/prompts/", promptID, "/versions"), query)
}

func promptListEndpoint(environment string, configured bool) string {
	if !configured {
		return "/prompts/list"
	}
	query := url.Values{}
	query.Set("environment", promptEnvironment(environment))
	return endpointWithQuery("/prompts/list", query)
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
