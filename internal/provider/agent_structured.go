package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Wire evidence (read-only): LiteLLM v1.98 commit
// d8f71d7bdbd7c9873d98293f83d64c6db72847e6, litellm/types/agents.py,
// declares AgentConfig/PatchAgentRequest.litellm_params as dict[str, Any],
// AgentSkill.security as list[dict[str, list[str]]], and AgentCard.signatures as
// list[AgentCardSignature] with an arbitrary dict[str, Any] header. The same
// commit's proxy/agent_endpoints/agent_registry.py serializes those dictionaries
// directly and PATCHes a supplied complete agent_card_params object.
//
// agentJSONObjectValidator is deliberately attached only to the additive JSON
// bridge. The legacy map(string) surface remains completely literal.
type agentJSONObjectValidator struct{}

var _ validator.String = agentJSONObjectValidator{}

func (agentJSONObjectValidator) Description(context.Context) string {
	return "Value must encode exactly one non-null JSON object."
}

func (v agentJSONObjectValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (agentJSONObjectValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := decodeAgentJSONObject(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Agent JSON Object", "The value must encode exactly one non-null JSON object.")
	}
}

func decodeAgentJSONObject(value string) (map[string]interface{}, error) {
	var decoded interface{}
	if err := decodeJSONUseNumber([]byte(value), &decoded); err != nil {
		return nil, errors.New("invalid JSON")
	}
	object, ok := decoded.(map[string]interface{})
	if !ok || object == nil {
		return nil, errors.New("expected JSON object")
	}
	return object, nil
}

type agentJSONNullOrObjectValidator struct{}

var _ validator.String = agentJSONNullOrObjectValidator{}

func (agentJSONNullOrObjectValidator) Description(context.Context) string {
	return "Value must encode JSON null or exactly one JSON object."
}
func (v agentJSONNullOrObjectValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (agentJSONNullOrObjectValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var decoded interface{}
	if decodeJSONUseNumber([]byte(req.ConfigValue.ValueString()), &decoded) != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Agent JSON Value", "The value must encode JSON null or exactly one JSON object.")
		return
	}
	if decoded == nil {
		return
	}
	if object, ok := decoded.(map[string]interface{}); !ok || object == nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Agent JSON Value", "The value must encode JSON null or exactly one JSON object.")
	}
}

type agentJSONNullOrSecurityValidator struct{}

var _ validator.String = agentJSONNullOrSecurityValidator{}

func (agentJSONNullOrSecurityValidator) Description(context.Context) string {
	return "Value must encode JSON null or an ordered A2A security-requirement list."
}
func (v agentJSONNullOrSecurityValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (agentJSONNullOrSecurityValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value, err := decodeAgentSecurityJSON(req.ConfigValue.ValueString())
	if err != nil || (value != nil && reflect.ValueOf(value).Kind() != reflect.Slice) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Agent Security JSON", "The value must encode JSON null or an ordered list of objects whose values are string lists.")
	}
}

func decodeAgentNullOrObject(value string) (interface{}, error) {
	var decoded interface{}
	if err := decodeJSONUseNumber([]byte(value), &decoded); err != nil {
		return nil, errors.New("invalid JSON")
	}
	if decoded == nil {
		return nil, nil
	}
	object, ok := decoded.(map[string]interface{})
	if !ok || object == nil {
		return nil, errors.New("expected JSON null or object")
	}
	return object, nil
}

func decodeAgentSecurityJSON(value string) (interface{}, error) {
	var decoded interface{}
	if err := decodeJSONUseNumber([]byte(value), &decoded); err != nil {
		return nil, errors.New("invalid JSON")
	}
	if decoded == nil {
		return nil, nil
	}
	items, ok := decoded.([]interface{})
	if !ok || items == nil {
		return nil, errors.New("expected JSON null or list")
	}
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]interface{})
		if !ok || item == nil {
			return nil, errors.New("security must contain objects")
		}
		for _, rawScopes := range item {
			scopes, ok := rawScopes.([]interface{})
			if !ok {
				return nil, errors.New("security scopes must be string lists")
			}
			for _, rawScope := range scopes {
				if _, ok := rawScope.(string); !ok {
					return nil, errors.New("security scopes must be string lists")
				}
			}
		}
	}
	return items, nil
}

func canonicalAgentJSON(value interface{}) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", errors.New("value cannot be represented as JSON")
	}
	return string(encoded), nil
}

// configuredAgentParams merges the two additive surfaces without interpreting
// legacy strings. A legacy value "false" is the JSON string "false", never a
// boolean; JSON-looking legacy text is likewise sent as text. Overlap is
// accepted only when the JSON value is the identical string.
func configuredAgentParams(legacy types.Map, structured types.String) (map[string]interface{}, bool, error) {
	result := map[string]interface{}{}
	configured := false
	if !legacy.IsNull() && !legacy.IsUnknown() {
		configured = true
		for key, raw := range legacy.Elements() {
			value, ok := raw.(types.String)
			if !ok || value.IsNull() || value.IsUnknown() {
				return nil, false, errors.New("litellm_params contains a non-string value")
			}
			result[key] = value.ValueString()
		}
	}
	if !structured.IsNull() && !structured.IsUnknown() {
		configured = true
		object, err := decodeAgentJSONObject(structured.ValueString())
		if err != nil {
			return nil, false, err
		}
		for key, value := range object {
			if prior, exists := result[key]; exists && !reflect.DeepEqual(prior, value) {
				return nil, false, fmt.Errorf("litellm_params and litellm_params_json configure different values for key %q", key)
			}
			result[key] = value
		}
	}
	return result, configured, nil
}

func agentStringProjection(object map[string]interface{}, excludeSynthetic bool) (types.Map, error) {
	values := map[string]attr.Value{}
	for key, raw := range object {
		if excludeSynthetic && key == "is_public" {
			continue
		}
		// Preserve the historical computed map(string) projection for state and
		// imports. The JSON bridge, not this compatibility projection, is the
		// authority for the original wire type.
		values[key] = types.StringValue(metadataValueToString(raw))
	}
	result, diagnostics := types.MapValue(types.StringType, values)
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), errors.New("could not project string values")
	}
	return result, nil
}

func stripAgentSyntheticParams(object map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(object))
	for key, value := range object {
		if key != "is_public" {
			result[key] = value
		}
	}
	return result
}

func restoreMaskedAgentLeaves(remote, prior interface{}, key string) (interface{}, error) {
	if isMaskedAgentAPIValue(key, remote) {
		priorString, ok := prior.(string)
		if !ok {
			return nil, errors.New("masked structured agent value changed shape")
		}
		return priorString, nil
	}
	switch value := remote.(type) {
	case map[string]interface{}:
		priorObject, _ := prior.(map[string]interface{})
		out := make(map[string]interface{}, len(value))
		for childKey, child := range value {
			restored, err := restoreMaskedAgentLeaves(child, priorObject[childKey], childKey)
			if err != nil {
				return nil, err
			}
			out[childKey] = restored
		}
		return out, nil
	case []interface{}:
		priorList, _ := prior.([]interface{})
		out := make([]interface{}, len(value))
		for index, child := range value {
			var priorChild interface{}
			if index < len(priorList) {
				priorChild = priorList[index]
			}
			restored, err := restoreMaskedAgentLeaves(child, priorChild, key)
			if err != nil {
				return nil, err
			}
			out[index] = restored
		}
		return out, nil
	default:
		return remote, nil
	}
}

func reconcileAgentJSONObject(current types.String, remote map[string]interface{}) (types.String, error) {
	remote = stripAgentSyntheticParams(remote)
	var prior map[string]interface{}
	if !current.IsNull() && !current.IsUnknown() {
		var err error
		prior, err = decodeAgentJSONObject(current.ValueString())
		if err != nil {
			return current, err
		}
	}
	value, err := restoreMaskedAgentLeaves(remote, prior, "litellm_params")
	if err != nil {
		return current, err
	}
	canonical, err := canonicalAgentJSON(value)
	if err != nil {
		return current, err
	}
	if !current.IsNull() && !current.IsUnknown() && jsonSemanticallyEqual(current.ValueString(), canonical) {
		return current, nil
	}
	return types.StringValue(canonical), nil
}

func decodeAgentSecurity(value types.List) ([]map[string][]string, error) {
	if value.IsNull() || value.IsUnknown() {
		return nil, nil
	}
	result := make([]map[string][]string, 0, len(value.Elements()))
	for _, raw := range value.Elements() {
		mapping, ok := raw.(types.Map)
		if !ok || mapping.IsNull() || mapping.IsUnknown() {
			return nil, errors.New("security must contain known maps")
		}
		item := make(map[string][]string, len(mapping.Elements()))
		for name, scopesRaw := range mapping.Elements() {
			scopes, ok := scopesRaw.(types.List)
			if !ok || scopes.IsNull() || scopes.IsUnknown() {
				return nil, errors.New("security scopes must be known string lists")
			}
			item[name] = listToStringSlice(scopes)
		}
		result = append(result, item)
	}
	return result, nil
}

func readAgentSecurity(raw interface{}) (types.List, error) {
	items, ok := raw.([]interface{})
	if !ok {
		return types.ListNull(types.MapType{ElemType: types.ListType{ElemType: types.StringType}}), errors.New("security must be a list")
	}
	values := make([]attr.Value, 0, len(items))
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]interface{})
		if !ok {
			return types.ListNull(types.MapType{ElemType: types.ListType{ElemType: types.StringType}}), errors.New("security must contain objects")
		}
		mapped := map[string]attr.Value{}
		for name, rawScopes := range item {
			scopes, ok := rawScopes.([]interface{})
			if !ok {
				return types.ListNull(types.MapType{ElemType: types.ListType{ElemType: types.StringType}}), errors.New("security scopes must be string lists")
			}
			list := make([]attr.Value, 0, len(scopes))
			for _, rawScope := range scopes {
				scope, ok := rawScope.(string)
				if !ok {
					return types.ListNull(types.MapType{ElemType: types.ListType{ElemType: types.StringType}}), errors.New("security scopes must be string lists")
				}
				list = append(list, types.StringValue(scope))
			}
			mapped[name] = types.ListValueMust(types.StringType, list)
		}
		values = append(values, types.MapValueMust(types.ListType{ElemType: types.StringType}, mapped))
	}
	result, diagnostics := types.ListValue(types.MapType{ElemType: types.ListType{ElemType: types.StringType}}, values)
	if diagnostics.HasError() {
		return types.ListNull(types.MapType{ElemType: types.ListType{ElemType: types.StringType}}), errors.New("security could not be represented")
	}
	return result, nil
}

// A2AProviderConfigManager at the pinned source commit selects AgentCore only
// when custom_llm_provider is "bedrock" and model contains "agentcore". That
// predicate is selection logic, not request validation: other providers remain
// valid even when their model name happens to contain the same substring.
func validateAgentCorePair(map[string]interface{}) error { return nil }
