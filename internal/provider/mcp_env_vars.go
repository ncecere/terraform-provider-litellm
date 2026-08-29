package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	invalidMCPEnvVarsSummary = "Invalid MCP Environment Variables"
	invalidMCPEnvVarsDetail  = "The collection must be an ordered list of complete environment-variable objects with unique valid names, string values, a supported scope, and nullable string descriptions. Collection contents were omitted from this diagnostic."
)

var mcpEnvVarObjectType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"name":        types.StringType,
	"value":       types.StringType,
	"scope":       types.StringType,
	"description": types.StringType,
}}

type mcpEnvVar struct {
	Name        string
	Value       string
	Scope       string
	Description *string
}

func mcpEnvVarNameValid(name string) bool {
	if name == "" {
		return false
	}
	first := name[0]
	if !((first >= 'A' && first <= 'Z') || (first >= 'a' && first <= 'z') || first == '_') {
		return false
	}
	for index := 1; index < len(name); index++ {
		character := name[index]
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || character == '_' {
			continue
		}
		return false
	}
	return true
}

func mcpEnvVarScopeValid(scope string) bool {
	return scope == "global" || scope == "user"
}

type mcpEnvVarNameValidator struct{}

var _ validator.String = mcpEnvVarNameValidator{}

func (mcpEnvVarNameValidator) Description(context.Context) string {
	return "The name must match [A-Za-z_][A-Za-z0-9_]*."
}

func (v mcpEnvVarNameValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (mcpEnvVarNameValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if !mcpEnvVarNameValid(req.ConfigValue.ValueString()) {
		resp.Diagnostics.AddAttributeError(req.Path, invalidMCPEnvVarsSummary, "The environment-variable name must match the required identifier syntax. The configured name was omitted from this diagnostic.")
	}
}

type mcpEnvVarsValidator struct{}

var _ validator.List = mcpEnvVarsValidator{}

func (mcpEnvVarsValidator) Description(context.Context) string {
	return "Environment-variable names must be unique."
}

func (v mcpEnvVarsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (mcpEnvVarsValidator) ValidateList(ctx context.Context, req validator.ListRequest, resp *validator.ListResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if diagnostics := canceledCollectionDiagnostics(ctx, req.Path); diagnostics.HasError() {
		resp.Diagnostics.Append(diagnostics...)
		return
	}
	if !req.ConfigValue.ElementType(ctx).Equal(mcpEnvVarObjectType) {
		resp.Diagnostics.AddAttributeError(req.Path, invalidMCPEnvVarsSummary, invalidMCPEnvVarsDetail)
		return
	}
	seen := make(map[string]bool, len(req.ConfigValue.Elements()))
	for _, raw := range req.ConfigValue.Elements() {
		if diagnostics := canceledCollectionDiagnostics(ctx, req.Path); diagnostics.HasError() {
			resp.Diagnostics.Append(diagnostics...)
			return
		}
		object, ok := raw.(types.Object)
		if !ok || object.IsNull() {
			resp.Diagnostics.AddAttributeError(req.Path, invalidMCPEnvVarsSummary, invalidMCPEnvVarsDetail)
			return
		}
		if object.IsUnknown() {
			continue
		}
		name, ok := object.Attributes()["name"].(types.String)
		if !ok || name.IsNull() {
			resp.Diagnostics.AddAttributeError(req.Path, invalidMCPEnvVarsSummary, invalidMCPEnvVarsDetail)
			return
		}
		if name.IsUnknown() {
			continue
		}
		if seen[name.ValueString()] {
			resp.Diagnostics.AddAttributeError(req.Path, invalidMCPEnvVarsSummary, "Environment-variable names must be unique. Collection contents were omitted from this diagnostic.")
			return
		}
		seen[name.ValueString()] = true
	}
}

// strictTerraformMCPEnvVars keeps null, unknown, empty, malformed, and canceled
// values distinct and validates the complete nested list before conversion.
func strictTerraformMCPEnvVars(ctx context.Context, value types.List, valuePath path.Path) ([]mcpEnvVar, collectionValueState, diag.Diagnostics) {
	state := listCollectionState(value)
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return nil, state, diagnostics
	}
	if value.IsNull() || value.IsUnknown() {
		return nil, state, nil
	}
	var diagnostics diag.Diagnostics
	if !value.ElementType(ctx).Equal(mcpEnvVarObjectType) {
		diagnostics.AddAttributeError(valuePath, invalidMCPEnvVarsSummary, invalidMCPEnvVarsDetail)
		return nil, state, diagnostics
	}
	result := make([]mcpEnvVar, len(value.Elements()))
	seen := make(map[string]bool, len(result))
	for index, raw := range value.Elements() {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			return nil, state, canceled
		}
		object, ok := raw.(types.Object)
		if !ok || object.IsNull() || object.IsUnknown() || !object.Type(ctx).Equal(mcpEnvVarObjectType) {
			diagnostics.AddAttributeError(valuePath, invalidMCPEnvVarsSummary, invalidMCPEnvVarsDetail)
			continue
		}
		attributes := object.Attributes()
		name, nameOK := attributes["name"].(types.String)
		itemValue, valueOK := attributes["value"].(types.String)
		scope, scopeOK := attributes["scope"].(types.String)
		description, descriptionOK := attributes["description"].(types.String)
		if !nameOK || name.IsNull() || name.IsUnknown() || !mcpEnvVarNameValid(name.ValueString()) ||
			!valueOK || itemValue.IsNull() || itemValue.IsUnknown() ||
			!scopeOK || scope.IsNull() || scope.IsUnknown() || !mcpEnvVarScopeValid(scope.ValueString()) ||
			!descriptionOK || description.IsUnknown() {
			diagnostics.AddAttributeError(valuePath, invalidMCPEnvVarsSummary, invalidMCPEnvVarsDetail)
			continue
		}
		if seen[name.ValueString()] {
			diagnostics.AddAttributeError(valuePath, invalidMCPEnvVarsSummary, "Environment-variable names must be unique. Collection contents were omitted from this diagnostic.")
			continue
		}
		seen[name.ValueString()] = true
		result[index] = mcpEnvVar{Name: name.ValueString(), Value: itemValue.ValueString(), Scope: scope.ValueString()}
		if !description.IsNull() {
			text := description.ValueString()
			result[index].Description = &text
		}
	}
	if diagnostics.HasError() {
		return nil, state, diagnostics
	}
	if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
		return nil, state, canceled
	}
	return result, state, nil
}

func mcpEnvVarsWire(values []mcpEnvVar) []map[string]interface{} {
	result := make([]map[string]interface{}, len(values))
	for index, value := range values {
		result[index] = map[string]interface{}{
			"name": value.Name, "value": value.Value, "scope": value.Scope, "description": nil,
		}
		if value.Description != nil {
			result[index]["description"] = *value.Description
		}
	}
	return result
}

func mcpEnvVarsTerraformValue(ctx context.Context, values []mcpEnvVar, valuePath path.Path) (types.List, diag.Diagnostics) {
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return types.ListNull(mcpEnvVarObjectType), diagnostics
	}
	elements := make([]attr.Value, len(values))
	for index, value := range values {
		attributes := map[string]attr.Value{
			"name": types.StringValue(value.Name), "value": types.StringValue(value.Value),
			"scope": types.StringValue(value.Scope), "description": types.StringNull(),
		}
		if value.Description != nil {
			attributes["description"] = types.StringValue(*value.Description)
		}
		object, objectDiagnostics := types.ObjectValue(mcpEnvVarObjectType.AttrTypes, attributes)
		if objectDiagnostics.HasError() {
			var diagnostics diag.Diagnostics
			diagnostics.AddAttributeError(valuePath, invalidMCPEnvVarsSummary, invalidMCPEnvVarsDetail)
			return types.ListNull(mcpEnvVarObjectType), diagnostics
		}
		elements[index] = object
	}
	result, constructorDiagnostics := types.ListValue(mcpEnvVarObjectType, elements)
	if constructorDiagnostics.HasError() {
		var diagnostics diag.Diagnostics
		diagnostics.AddAttributeError(valuePath, invalidMCPEnvVarsSummary, invalidMCPEnvVarsDetail)
		return types.ListNull(mcpEnvVarObjectType), diagnostics
	}
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return types.ListNull(mcpEnvVarObjectType), diagnostics
	}
	return result, nil
}

func decodeMCPEnvVarsAPI(raw interface{}) ([]mcpEnvVar, error) {
	var members []interface{}
	switch value := raw.(type) {
	case []interface{}:
		members = value
	case []map[string]interface{}:
		members = make([]interface{}, len(value))
		for index := range value {
			members[index] = value[index]
		}
	default:
		return nil, fmt.Errorf("malformed MCP environment-variable collection")
	}
	result := make([]mcpEnvVar, len(members))
	seen := make(map[string]bool, len(members))
	for index, rawMember := range members {
		member, ok := rawMember.(map[string]interface{})
		if !ok || member == nil {
			return nil, fmt.Errorf("malformed MCP environment-variable member")
		}
		for key := range member {
			if key != "name" && key != "value" && key != "scope" && key != "description" {
				return nil, fmt.Errorf("malformed MCP environment-variable member")
			}
		}
		name, ok := member["name"].(string)
		if !ok || !mcpEnvVarNameValid(name) || seen[name] {
			return nil, fmt.Errorf("malformed MCP environment-variable member")
		}
		seen[name] = true
		value := ""
		if rawValue, present := member["value"]; present {
			var valueOK bool
			value, valueOK = rawValue.(string)
			if !valueOK {
				return nil, fmt.Errorf("malformed MCP environment-variable member")
			}
		}
		scope := "global"
		if rawScope, present := member["scope"]; present {
			var scopeOK bool
			scope, scopeOK = rawScope.(string)
			if !scopeOK || !mcpEnvVarScopeValid(scope) {
				return nil, fmt.Errorf("malformed MCP environment-variable member")
			}
		}
		var description *string
		if rawDescription, present := member["description"]; present && rawDescription != nil {
			text, descriptionOK := rawDescription.(string)
			if !descriptionOK {
				return nil, fmt.Errorf("malformed MCP environment-variable member")
			}
			description = &text
		}
		result[index] = mcpEnvVar{Name: name, Value: value, Scope: scope, Description: description}
	}
	return result, nil
}

func strictAPIMCPEnvVars(ctx context.Context, object map[string]interface{}, field string, valuePath path.Path) (types.List, apiValuePresence, []mcpEnvVar, diag.Diagnostics) {
	raw, presence, err := apiValueAt(object, field)
	if err != nil || presence != apiValuePresent {
		return types.ListNull(mcpEnvVarObjectType), presence, nil, nil
	}
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return types.ListNull(mcpEnvVarObjectType), presence, nil, diagnostics
	}
	values, err := decodeMCPEnvVarsAPI(raw)
	if err != nil {
		var diagnostics diag.Diagnostics
		diagnostics.AddAttributeError(valuePath, invalidMCPEnvVarsSummary, "LiteLLM returned an environment-variable collection that cannot be represented safely. Response contents were omitted from this diagnostic.")
		return types.ListNull(mcpEnvVarObjectType), presence, nil, diagnostics
	}
	result, diagnostics := mcpEnvVarsTerraformValue(ctx, values, valuePath)
	return result, presence, values, diagnostics
}

func canonicalMCPEnvVarsAPIWire(result map[string]interface{}) ([]map[string]interface{}, apiValuePresence, error) {
	raw, presence, err := apiValueAt(result, "env_vars")
	if err != nil || presence != apiValuePresent {
		return nil, presence, err
	}
	values, err := decodeMCPEnvVarsAPI(raw)
	if err != nil {
		return nil, presence, err
	}
	return mcpEnvVarsWire(values), presence, nil
}

func validateMCPEnvVarsAPI(result map[string]interface{}) error {
	_, _, err := canonicalMCPEnvVarsAPIWire(result)
	return err
}
