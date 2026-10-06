package provider

import (
	"context"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// liteLLMRedactedMarker is LiteLLM's fixed replacement for credential-bearing
// values (litellm.constants.REDACTED_BY_LITELM_STRING). From 1.104.0 agent
// responses use it for every caller, proxy admins included.
const liteLLMRedactedMarker = "REDACTED_BY_LITELM"

// liteLLMSensitiveKeySegments mirrors SensitiveDataMasker's default patterns
// at the pinned release; a "cost" segment overrides them (pricing fields).
var liteLLMSensitiveKeySegments = map[string]struct{}{
	"password": {}, "secret": {}, "key": {}, "token": {}, "auth": {}, "authorization": {},
	"cookie": {}, "credential": {}, "credentials": {}, "access": {}, "private": {},
	"certificate": {}, "fingerprint": {}, "tenancy": {},
}

// isLiteLLMSensitiveParamKey reports whether LiteLLM treats a parameter name as
// a secret. LiteLLM 1.104.0 masks these agent litellm_params values on every
// read and keeps the stored value when a PATCH omits the key.
func isLiteLLMSensitiveParamKey(key string) bool {
	segments := strings.Split(strings.ReplaceAll(strings.ToLower(key), "-", "_"), "_")
	for _, segment := range segments {
		if segment == "cost" {
			return false
		}
	}
	for _, segment := range segments {
		if _, sensitive := liteLLMSensitiveKeySegments[segment]; sensitive {
			return true
		}
	}
	return false
}

// removedAgentSecretParamKeys lists Terraform-owned top-level litellm_params
// keys that configuration removes although LiteLLM treats them as secrets.
// LiteLLM 1.104.0 keeps such a key when a PATCH omits it, so the removal could
// never converge. Imported (API-owned) keys are not Terraform's to remove.
func removedAgentSecretParamKeys(state, config AgentResourceModel, imported agentFieldSet) ([]string, error) {
	desired, _, err := configuredAgentParams(config.LiteLLMParams, config.LiteLLMParamsJSON)
	if err != nil {
		return nil, err
	}
	prior := map[string]struct{}{}
	if !state.LiteLLMParams.IsNull() && !state.LiteLLMParams.IsUnknown() {
		for key := range state.LiteLLMParams.Elements() {
			prior[key] = struct{}{}
		}
	}
	if !state.LiteLLMParamsJSON.IsNull() && !state.LiteLLMParamsJSON.IsUnknown() {
		object, decodeErr := decodeAgentJSONObject(state.LiteLLMParamsJSON.ValueString())
		if decodeErr != nil {
			return nil, decodeErr
		}
		for key := range object {
			prior[key] = struct{}{}
		}
	}
	var removed []string
	for key := range prior {
		if _, retained := desired[key]; retained || imported[agentLeaf(agentFieldParams, key)] {
			continue
		}
		if isLiteLLMSensitiveParamKey(key) {
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	return removed, nil
}

func agentParamsConfigKnown(config AgentResourceModel) bool {
	return !config.LiteLLMParams.IsUnknown() && !config.LiteLLMParamsJSON.IsUnknown() && !agentMapHasUnknownElement(config.LiteLLMParams)
}

func agentMapHasUnknownElement(value types.Map) bool {
	if value.IsNull() || value.IsUnknown() {
		return false
	}
	for _, element := range value.Elements() {
		if element.IsUnknown() {
			return true
		}
	}
	return false
}

// seedAgentSecretParams gives a confirmation read the secret parameters the
// provider just wrote. LiteLLM 1.104.0 returns REDACTED_BY_LITELM for them to
// every caller, so the only recoverable value is the one Terraform sent. Only
// secret keys are seeded: a secret LiteLLM did not store is absent from the
// response and still fails confirmation, and every other value comes from the
// response.
func seedAgentSecretParams(ctx context.Context, observed *AgentResourceModel, planned AgentResourceModel) error {
	desired, _, err := configuredAgentParams(planned.LiteLLMParams, planned.LiteLLMParamsJSON)
	if err != nil {
		return err
	}
	secrets := map[string]interface{}{}
	for key, value := range desired {
		if isLiteLLMSensitiveParamKey(key) {
			secrets[key] = value
		}
	}
	if len(secrets) == 0 {
		return nil
	}
	values := make(map[string]attr.Value, len(secrets))
	for key, value := range secrets {
		values[key] = types.StringValue(metadataValueToString(value))
	}
	seeded, diagnostics := checkedStringMapValue(ctx, values, path.Root(agentFieldParams), true)
	if err := collectionProjectionError(ctx, diagnostics); err != nil {
		return err
	}
	canonical, err := canonicalAgentJSON(secrets)
	if err != nil {
		return err
	}
	observed.LiteLLMParams = seeded
	observed.LiteLLMParamsJSON = types.StringValue(canonical)
	return nil
}
