package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// LiteLLM v1.98.0 accepts these fields in UpdateRouterConfig. Complex values
// remain JSON strings in Terraform so heterogeneous objects and ordered arrays
// can round-trip without lossy schema coercion.
var keyRetryPolicyAttrTypes = map[string]attr.Type{
	"bad_request_error_retries":              types.Int64Type,
	"authentication_error_retries":           types.Int64Type,
	"timeout_error_retries":                  types.Int64Type,
	"rate_limit_error_retries":               types.Int64Type,
	"content_policy_violation_error_retries": types.Int64Type,
	"internal_server_error_retries":          types.Int64Type,
}

var keyRouterSettingsAttrTypes = map[string]attr.Type{
	"routing_strategy_args":       types.StringType,
	"routing_strategy":            types.StringType,
	"routing_groups":              types.StringType,
	"retry_policy":                types.ObjectType{AttrTypes: keyRetryPolicyAttrTypes},
	"model_group_retry_policy":    types.StringType,
	"model_group_affinity_config": types.StringType,
	"allowed_fails":               types.Int64Type,
	"cooldown_time":               types.Float64Type,
	"num_retries":                 types.Int64Type,
	"timeout":                     types.Float64Type,
	"max_retries":                 types.Int64Type,
	"retry_after":                 types.Float64Type,
	"fallbacks":                   types.StringType,
	"context_window_fallbacks":    types.StringType,
	"model_group_alias":           types.StringType,
	"enable_tag_filtering":        types.BoolType,
	"tag_routing_prefix":          types.StringType,
}

func keyRouterSettingsDataSourceAttribute() datasourceschema.SingleNestedAttribute {
	return datasourceschema.SingleNestedAttribute{
		Description: "The complete key-specific LiteLLM v1.98.0 router-settings document. Complex heterogeneous fields are canonical JSON strings.",
		Computed:    true,
		Attributes: map[string]datasourceschema.Attribute{
			"routing_strategy_args": datasourceschema.StringAttribute{Computed: true},
			"routing_strategy":      datasourceschema.StringAttribute{Computed: true},
			"routing_groups":        datasourceschema.StringAttribute{Computed: true},
			"retry_policy": datasourceschema.SingleNestedAttribute{
				Computed: true,
				Attributes: map[string]datasourceschema.Attribute{
					"bad_request_error_retries":              datasourceschema.Int64Attribute{Computed: true},
					"authentication_error_retries":           datasourceschema.Int64Attribute{Computed: true},
					"timeout_error_retries":                  datasourceschema.Int64Attribute{Computed: true},
					"rate_limit_error_retries":               datasourceschema.Int64Attribute{Computed: true},
					"content_policy_violation_error_retries": datasourceschema.Int64Attribute{Computed: true},
					"internal_server_error_retries":          datasourceschema.Int64Attribute{Computed: true},
				},
			},
			"model_group_retry_policy":    datasourceschema.StringAttribute{Computed: true},
			"model_group_affinity_config": datasourceschema.StringAttribute{Computed: true},
			"allowed_fails":               datasourceschema.Int64Attribute{Computed: true},
			"cooldown_time":               datasourceschema.Float64Attribute{Computed: true},
			"num_retries":                 datasourceschema.Int64Attribute{Computed: true},
			"timeout":                     datasourceschema.Float64Attribute{Computed: true},
			"max_retries":                 datasourceschema.Int64Attribute{Computed: true},
			"retry_after":                 datasourceschema.Float64Attribute{Computed: true},
			"fallbacks":                   datasourceschema.StringAttribute{Computed: true},
			"context_window_fallbacks":    datasourceschema.StringAttribute{Computed: true},
			"model_group_alias":           datasourceschema.StringAttribute{Computed: true},
			"enable_tag_filtering":        datasourceschema.BoolAttribute{Computed: true},
			"tag_routing_prefix":          datasourceschema.StringAttribute{Computed: true},
		},
	}
}

func keyRouterSettingsResourceAttribute() resourceschema.SingleNestedAttribute {
	jsonObject := []validator.String{jsonShapeStringValidator{shape: '{'}}
	jsonArray := []validator.String{jsonShapeStringValidator{shape: '['}}
	return resourceschema.SingleNestedAttribute{
		Description: "Key-specific LiteLLM v1.98.0 router settings. When configured, Terraform owns and replaces the complete router-settings document. Complex heterogeneous fields are JSON strings.",
		Optional:    true,
		Attributes: map[string]resourceschema.Attribute{
			"routing_strategy_args": resourceschema.StringAttribute{Description: "JSON object passed to the routing strategy.", Optional: true, Validators: jsonObject},
			"routing_strategy":      resourceschema.StringAttribute{Description: "Routing strategy name.", Optional: true},
			"routing_groups":        resourceschema.StringAttribute{Description: "JSON array of routing groups.", Optional: true, Validators: jsonArray},
			"retry_policy": resourceschema.SingleNestedAttribute{
				Description: "Default retry counts by LiteLLM exception type.",
				Optional:    true,
				Attributes: map[string]resourceschema.Attribute{
					"bad_request_error_retries":              resourceschema.Int64Attribute{Optional: true},
					"authentication_error_retries":           resourceschema.Int64Attribute{Optional: true},
					"timeout_error_retries":                  resourceschema.Int64Attribute{Optional: true},
					"rate_limit_error_retries":               resourceschema.Int64Attribute{Optional: true},
					"content_policy_violation_error_retries": resourceschema.Int64Attribute{Optional: true},
					"internal_server_error_retries":          resourceschema.Int64Attribute{Optional: true},
				},
			},
			"model_group_retry_policy":    resourceschema.StringAttribute{Description: "JSON object mapping model groups to retry-policy objects. Retry-policy keys use LiteLLM's PascalCase wire names.", Optional: true, Validators: jsonObject},
			"model_group_affinity_config": resourceschema.StringAttribute{Description: "JSON object mapping affinity groups to arrays of model groups.", Optional: true, Validators: jsonObject},
			"allowed_fails":               resourceschema.Int64Attribute{Description: "Failures allowed before a deployment enters cooldown.", Optional: true},
			"cooldown_time":               resourceschema.Float64Attribute{Description: "Cooldown duration in seconds.", Optional: true},
			"num_retries":                 resourceschema.Int64Attribute{Description: "Number of request retries.", Optional: true},
			"timeout":                     resourceschema.Float64Attribute{Description: "Request timeout in seconds.", Optional: true},
			"max_retries":                 resourceschema.Int64Attribute{Description: "Maximum retries.", Optional: true},
			"retry_after":                 resourceschema.Float64Attribute{Description: "Retry delay in seconds; decimal values are supported.", Optional: true},
			"fallbacks":                   resourceschema.StringAttribute{Description: "Ordered JSON array of model fallback mappings.", Optional: true, Validators: jsonArray},
			"context_window_fallbacks":    resourceschema.StringAttribute{Description: "Ordered JSON array of context-window fallback mappings.", Optional: true, Validators: jsonArray},
			"model_group_alias":           resourceschema.StringAttribute{Description: "JSON object mapping aliases to model groups or alias configuration objects.", Optional: true, Validators: jsonObject},
			"enable_tag_filtering":        resourceschema.BoolAttribute{Description: "Enable routing by request tags.", Optional: true},
			"tag_routing_prefix":          resourceschema.StringAttribute{Description: "Prefix used for tag-based routing.", Optional: true},
		},
	}
}

var keyRetryPolicyWireNames = map[string]string{
	"bad_request_error_retries":              "BadRequestErrorRetries",
	"authentication_error_retries":           "AuthenticationErrorRetries",
	"timeout_error_retries":                  "TimeoutErrorRetries",
	"rate_limit_error_retries":               "RateLimitErrorRetries",
	"content_policy_violation_error_retries": "ContentPolicyViolationErrorRetries",
	"internal_server_error_retries":          "InternalServerErrorRetries",
}

type jsonShapeStringValidator struct {
	shape byte
}

var _ validator.String = jsonShapeStringValidator{}

func (v jsonShapeStringValidator) Description(context.Context) string {
	if v.shape == '{' {
		return "Value must be a JSON object."
	}
	return "Value must be a JSON array."
}

func (v jsonShapeStringValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v jsonShapeStringValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var decoded interface{}
	if err := json.Unmarshal([]byte(req.ConfigValue.ValueString()), &decoded); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Router Settings JSON", fmt.Sprintf("Value must be valid JSON: %s", err))
		return
	}
	valid := false
	if v.shape == '{' {
		_, valid = decoded.(map[string]interface{})
	} else {
		_, valid = decoded.([]interface{})
	}
	if !valid {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Router Settings JSON Shape", v.Description(ctx))
	}
}

func decodeRouterSettingsJSON(value types.String) (interface{}, error) {
	var decoded interface{}
	if err := json.Unmarshal([]byte(value.ValueString()), &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func applyKeyRouterSettingsUpdateSemantics(request map[string]interface{}, planned, prior types.Object) {
	// LiteLLM distinguishes an omitted router_settings field (leave unchanged)
	// from an explicit empty object (clear the complete stored document). The
	// v1.98.0 request/auth validation path rejects JSON null for this field.
	if planned.IsNull() && !prior.IsNull() {
		request["router_settings"] = map[string]interface{}{}
	}
}

func keyRouterSettingsPayload(obj types.Object) (map[string]interface{}, error) {
	if obj.IsNull() || obj.IsUnknown() {
		return nil, nil
	}
	attrs := obj.Attributes()
	payload := make(map[string]interface{})

	for _, name := range []string{"routing_strategy", "tag_routing_prefix"} {
		value := attrs[name].(types.String)
		if !value.IsNull() && !value.IsUnknown() {
			payload[name] = value.ValueString()
		}
	}
	for _, name := range []string{"allowed_fails", "num_retries", "max_retries"} {
		value := attrs[name].(types.Int64)
		if !value.IsNull() && !value.IsUnknown() {
			payload[name] = value.ValueInt64()
		}
	}
	for _, name := range []string{"cooldown_time", "timeout", "retry_after"} {
		value := attrs[name].(types.Float64)
		if !value.IsNull() && !value.IsUnknown() {
			payload[name] = value.ValueFloat64()
		}
	}
	if value := attrs["enable_tag_filtering"].(types.Bool); !value.IsNull() && !value.IsUnknown() {
		payload["enable_tag_filtering"] = value.ValueBool()
	}

	for _, name := range []string{
		"routing_strategy_args",
		"routing_groups",
		"model_group_retry_policy",
		"model_group_affinity_config",
		"fallbacks",
		"context_window_fallbacks",
		"model_group_alias",
	} {
		value := attrs[name].(types.String)
		if value.IsNull() || value.IsUnknown() {
			continue
		}
		decoded, err := decodeRouterSettingsJSON(value)
		if err != nil {
			return nil, fmt.Errorf("decode router_settings.%s: %w", name, err)
		}
		payload[name] = decoded
	}

	if retryPolicy := attrs["retry_policy"].(types.Object); !retryPolicy.IsNull() && !retryPolicy.IsUnknown() {
		policy := make(map[string]interface{})
		for terraformName, wireName := range keyRetryPolicyWireNames {
			value := retryPolicy.Attributes()[terraformName].(types.Int64)
			if !value.IsNull() && !value.IsUnknown() {
				policy[wireName] = value.ValueInt64()
			}
		}
		payload["retry_policy"] = policy
	}

	return payload, nil
}

func normalizedRouterSettingsJSON(value interface{}) (interface{}, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var normalized interface{}
	if err := json.Unmarshal(encoded, &normalized); err != nil {
		return nil, err
	}
	return normalized, nil
}

// keyRouterSettingsMatchAPI compares the complete owned document. A nil or
// empty API object both mean there is no key-level override and inheritance is
// restored. JSON normalization removes Go int64/float64 representation noise.
func keyRouterSettingsMatchAPI(wanted map[string]interface{}, raw interface{}) (bool, error) {
	observed, present, err := routerSettingsMapFromAPI(raw)
	if err != nil {
		return false, err
	}
	if wanted == nil {
		return !present || len(observed) == 0, nil
	}
	if !present {
		return false, nil
	}
	normalizedWanted, err := normalizedRouterSettingsJSON(wanted)
	if err != nil {
		return false, err
	}
	normalizedObserved, err := normalizedRouterSettingsJSON(observed)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(normalizedWanted, normalizedObserved), nil
}

func routerSettingsMapFromAPI(raw interface{}) (map[string]interface{}, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	if value, ok := raw.(string); ok {
		if value == "" || value == "null" {
			return nil, false, nil
		}
		var decoded interface{}
		if err := json.Unmarshal([]byte(value), &decoded); err != nil {
			return nil, false, fmt.Errorf("decode router_settings response: %w", err)
		}
		raw = decoded
	}
	settings, ok := raw.(map[string]interface{})
	if !ok {
		return nil, false, fmt.Errorf("expected router_settings to be an object, got %T", raw)
	}
	return settings, true, nil
}

func jsonStringFromRouterSettingsAPI(raw interface{}, current attr.Value) (types.String, error) {
	canonical, err := json.Marshal(raw)
	if err != nil {
		return types.StringNull(), err
	}
	if existing, ok := current.(types.String); ok && !existing.IsNull() && !existing.IsUnknown() {
		var configured interface{}
		if json.Unmarshal([]byte(existing.ValueString()), &configured) == nil && reflect.DeepEqual(configured, raw) {
			return existing, nil
		}
	}
	return types.StringValue(string(canonical)), nil
}

func int64FromRouterSettingsAPI(raw interface{}) (types.Int64, error) {
	switch value := raw.(type) {
	case float64:
		if value != float64(int64(value)) {
			return types.Int64Null(), fmt.Errorf("expected an integer, got %v", value)
		}
		return types.Int64Value(int64(value)), nil
	case int:
		return types.Int64Value(int64(value)), nil
	case int64:
		return types.Int64Value(value), nil
	default:
		return types.Int64Null(), fmt.Errorf("expected an integer, got %T", raw)
	}
}

func float64FromRouterSettingsAPI(raw interface{}) (types.Float64, error) {
	switch value := raw.(type) {
	case float64:
		return types.Float64Value(value), nil
	case int:
		return types.Float64Value(float64(value)), nil
	case int64:
		return types.Float64Value(float64(value)), nil
	default:
		return types.Float64Null(), fmt.Errorf("expected a number, got %T", raw)
	}
}

func retryPolicyFromAPI(raw interface{}) (types.Object, error) {
	policy, ok := raw.(map[string]interface{})
	if !ok {
		return types.ObjectNull(keyRetryPolicyAttrTypes), fmt.Errorf("expected retry_policy to be an object, got %T", raw)
	}
	knownWireNames := make(map[string]struct{}, len(keyRetryPolicyWireNames))
	for _, wireName := range keyRetryPolicyWireNames {
		knownWireNames[wireName] = struct{}{}
	}
	for wireName := range policy {
		if _, known := knownWireNames[wireName]; !known {
			return types.ObjectNull(keyRetryPolicyAttrTypes), fmt.Errorf("retry_policy contains unsupported LiteLLM v1.98.0 field %q", wireName)
		}
	}
	values := make(map[string]attr.Value, len(keyRetryPolicyAttrTypes))
	for terraformName, wireName := range keyRetryPolicyWireNames {
		values[terraformName] = types.Int64Null()
		if rawValue, exists := policy[wireName]; exists && rawValue != nil {
			value, err := int64FromRouterSettingsAPI(rawValue)
			if err != nil {
				return types.ObjectNull(keyRetryPolicyAttrTypes), fmt.Errorf("retry_policy.%s: %w", wireName, err)
			}
			values[terraformName] = value
		}
	}
	value, diagnostics := types.ObjectValue(keyRetryPolicyAttrTypes, values)
	if diagnostics.HasError() {
		return types.ObjectNull(keyRetryPolicyAttrTypes), fmt.Errorf("build retry_policy state: %v", diagnostics.Errors())
	}
	return value, nil
}

func keyRouterSettingsFromAPI(raw interface{}, current types.Object) (types.Object, bool, error) {
	settings, present, err := routerSettingsMapFromAPI(raw)
	if err != nil || !present {
		return types.ObjectNull(keyRouterSettingsAttrTypes), present, err
	}
	for name := range settings {
		if _, known := keyRouterSettingsAttrTypes[name]; !known {
			return types.ObjectNull(keyRouterSettingsAttrTypes), true, fmt.Errorf("router_settings contains unsupported LiteLLM v1.98.0 field %q", name)
		}
	}

	values := make(map[string]attr.Value, len(keyRouterSettingsAttrTypes))
	// Initialize the finite v1.98.0 schema explicitly with typed nulls.
	for _, name := range []string{"routing_strategy_args", "routing_strategy", "routing_groups", "model_group_retry_policy", "model_group_affinity_config", "fallbacks", "context_window_fallbacks", "model_group_alias", "tag_routing_prefix"} {
		values[name] = types.StringNull()
	}
	values["retry_policy"] = types.ObjectNull(keyRetryPolicyAttrTypes)
	for _, name := range []string{"allowed_fails", "num_retries", "max_retries"} {
		values[name] = types.Int64Null()
	}
	for _, name := range []string{"cooldown_time", "timeout", "retry_after"} {
		values[name] = types.Float64Null()
	}
	values["enable_tag_filtering"] = types.BoolNull()

	currentAttrs := map[string]attr.Value{}
	if !current.IsNull() && !current.IsUnknown() {
		currentAttrs = current.Attributes()
	}
	for _, name := range []string{"routing_strategy_args", "routing_groups", "model_group_retry_policy", "model_group_affinity_config", "fallbacks", "context_window_fallbacks", "model_group_alias"} {
		if rawValue, exists := settings[name]; exists && rawValue != nil {
			value, err := jsonStringFromRouterSettingsAPI(rawValue, currentAttrs[name])
			if err != nil {
				return types.ObjectNull(keyRouterSettingsAttrTypes), true, fmt.Errorf("router_settings.%s: %w", name, err)
			}
			values[name] = value
		}
	}
	for _, name := range []string{"routing_strategy", "tag_routing_prefix"} {
		if rawValue, exists := settings[name]; exists && rawValue != nil {
			value, ok := rawValue.(string)
			if !ok {
				return types.ObjectNull(keyRouterSettingsAttrTypes), true, fmt.Errorf("router_settings.%s: expected string, got %T", name, rawValue)
			}
			values[name] = types.StringValue(value)
		}
	}
	for _, name := range []string{"allowed_fails", "num_retries", "max_retries"} {
		if rawValue, exists := settings[name]; exists && rawValue != nil {
			value, err := int64FromRouterSettingsAPI(rawValue)
			if err != nil {
				return types.ObjectNull(keyRouterSettingsAttrTypes), true, fmt.Errorf("router_settings.%s: %w", name, err)
			}
			values[name] = value
		}
	}
	for _, name := range []string{"cooldown_time", "timeout", "retry_after"} {
		if rawValue, exists := settings[name]; exists && rawValue != nil {
			value, err := float64FromRouterSettingsAPI(rawValue)
			if err != nil {
				return types.ObjectNull(keyRouterSettingsAttrTypes), true, fmt.Errorf("router_settings.%s: %w", name, err)
			}
			values[name] = value
		}
	}
	if rawValue, exists := settings["enable_tag_filtering"]; exists && rawValue != nil {
		value, ok := rawValue.(bool)
		if !ok {
			return types.ObjectNull(keyRouterSettingsAttrTypes), true, fmt.Errorf("router_settings.enable_tag_filtering: expected boolean, got %T", rawValue)
		}
		values["enable_tag_filtering"] = types.BoolValue(value)
	}
	if rawValue, exists := settings["retry_policy"]; exists && rawValue != nil {
		value, err := retryPolicyFromAPI(rawValue)
		if err != nil {
			return types.ObjectNull(keyRouterSettingsAttrTypes), true, err
		}
		values["retry_policy"] = value
	}

	value, diagnostics := types.ObjectValue(keyRouterSettingsAttrTypes, values)
	if diagnostics.HasError() {
		return types.ObjectNull(keyRouterSettingsAttrTypes), true, fmt.Errorf("build router_settings state: %v", diagnostics.Errors())
	}
	return value, true, nil
}
