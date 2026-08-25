package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type invalidAgentMCPToolPermissionsResponseError struct{}

func (invalidAgentMCPToolPermissionsResponseError) Error() string {
	return "invalid agent MCP tool permissions response: expected an object of string arrays"
}

func isInvalidAgentMCPToolPermissionsResponse(err error) bool {
	_, ok := err.(invalidAgentMCPToolPermissionsResponseError)
	return ok
}

type agentMCPToolPermissionsValidator struct{}

var _ validator.Map = agentMCPToolPermissionsValidator{}

func (agentMCPToolPermissionsValidator) Description(context.Context) string {
	return "Each value must be a JSON array containing only strings."
}

func (v agentMCPToolPermissionsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v agentMCPToolPermissionsValidator) ValidateMap(ctx context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for _, element := range req.ConfigValue.Elements() {
		value, ok := element.(types.String)
		if !ok || value.IsNull() {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid MCP Tool Permissions", v.Description(ctx))
			return
		}
		if value.IsUnknown() {
			continue
		}
		if _, err := decodeAgentMCPToolArray(value.ValueString()); err != nil {
			// Keep this diagnostic deliberately generic: permission values, map
			// keys, and tool names may disclose authorization details.
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid MCP Tool Permissions", v.Description(ctx))
			return
		}
	}
}

func decodeAgentMCPToolArray(encoded string) ([]string, error) {
	var decoded interface{}
	if err := decodeJSONUseNumber([]byte(encoded), &decoded); err != nil {
		return nil, fmt.Errorf("invalid MCP tool permission array")
	}
	items, ok := decoded.([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid MCP tool permission array")
	}
	tools := make([]string, len(items))
	for index, item := range items {
		tool, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("invalid MCP tool permission array")
		}
		tools[index] = tool
	}
	return tools, nil
}

func decodeConfiguredAgentMCPToolPermissions(value types.Map) (map[string][]string, error) {
	permissions := make(map[string][]string, len(value.Elements()))
	for key, element := range value.Elements() {
		encoded, ok := element.(types.String)
		if !ok || encoded.IsNull() || encoded.IsUnknown() {
			return nil, fmt.Errorf("invalid MCP tool permissions configuration")
		}
		tools, err := decodeAgentMCPToolArray(encoded.ValueString())
		if err != nil {
			return nil, fmt.Errorf("invalid MCP tool permissions configuration")
		}
		permissions[key] = tools
	}
	return permissions, nil
}

func decodeObservedAgentMCPToolPermissions(raw interface{}) (map[string][]string, error) {
	object, ok := raw.(map[string]interface{})
	if !ok {
		return nil, invalidAgentMCPToolPermissionsResponseError{}
	}
	permissions := make(map[string][]string, len(object))
	for key, rawTools := range object {
		items, ok := rawTools.([]interface{})
		if !ok {
			return nil, invalidAgentMCPToolPermissionsResponseError{}
		}
		tools := make([]string, len(items))
		for index, rawTool := range items {
			tool, ok := rawTool.(string)
			if !ok {
				return nil, invalidAgentMCPToolPermissionsResponseError{}
			}
			tools[index] = tool
		}
		permissions[key] = tools
	}
	return permissions, nil
}

func agentMCPToolPermissionsOwned(data AgentResourceModel) bool {
	return data.ObjectPermission != nil &&
		!data.ObjectPermission.MCPToolPermissions.IsNull() &&
		!data.ObjectPermission.MCPToolPermissions.IsUnknown()
}

func agentMCPToolPermissionsConfirmed(planned, observed AgentResourceModel) bool {
	if !agentMCPToolPermissionsOwned(planned) {
		return true
	}
	if !agentMCPToolPermissionsOwned(observed) {
		return false
	}
	plannedPermissions, err := decodeConfiguredAgentMCPToolPermissions(planned.ObjectPermission.MCPToolPermissions)
	if err != nil {
		return false
	}
	observedPermissions, err := decodeConfiguredAgentMCPToolPermissions(observed.ObjectPermission.MCPToolPermissions)
	if err != nil || len(plannedPermissions) != len(observedPermissions) {
		return false
	}
	for serverID, plannedTools := range plannedPermissions {
		observedTools, exists := observedPermissions[serverID]
		if !exists || !slices.Equal(plannedTools, observedTools) {
			return false
		}
	}
	return true
}

func reconcileAgentMCPToolPermissions(current types.Map, observed map[string][]string) (types.Map, error) {
	currentElements := current.Elements()
	reconciled := make(map[string]attr.Value, len(observed))
	for key, tools := range observed {
		if element, exists := currentElements[key]; exists {
			if configured, ok := element.(types.String); ok && !configured.IsNull() && !configured.IsUnknown() {
				configuredTools, err := decodeAgentMCPToolArray(configured.ValueString())
				if err == nil && slices.Equal(configuredTools, tools) {
					reconciled[key] = configured
					continue
				}
			}
		}
		encoded, err := json.Marshal(tools)
		if err != nil {
			return current, fmt.Errorf("invalid agent MCP tool permissions response: could not encode string array")
		}
		reconciled[key] = types.StringValue(string(encoded))
	}
	value, diagnostics := types.MapValue(types.StringType, reconciled)
	if diagnostics.HasError() {
		return current, fmt.Errorf("invalid agent MCP tool permissions response: could not build map state")
	}
	if current.Equal(value) {
		return current, nil
	}
	return value, nil
}
