package provider

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAgentMCPToolPermissionsSchemaCompatibility(t *testing.T) {
	t.Parallel()

	var response frameworkresource.SchemaResponse
	(&AgentResource{}).Schema(context.Background(), frameworkresource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	permissionBlock, ok := response.Schema.Blocks["object_permission"].(resourceschema.SingleNestedBlock)
	if !ok {
		t.Fatalf("object_permission schema = %T", response.Schema.Blocks["object_permission"])
	}
	attribute, ok := permissionBlock.Attributes["mcp_tool_permissions"].(resourceschema.MapAttribute)
	if !ok {
		t.Fatalf("mcp_tool_permissions schema = %T", permissionBlock.Attributes["mcp_tool_permissions"])
	}
	if !attribute.Optional || attribute.Computed || !attribute.ElementType.Equal(types.StringType) {
		t.Fatalf("public schema changed: optional=%t computed=%t element=%T", attribute.Optional, attribute.Computed, attribute.ElementType)
	}
}

func TestAgentMCPToolPermissionsValidator(t *testing.T) {
	t.Parallel()

	valid := []string{`[]`, `["tool"]`, ` [ "first", "second" ] `, `["", "escaped\\name"]`}
	for _, configured := range valid {
		configured := configured
		t.Run("valid", func(t *testing.T) {
			var response validator.MapResponse
			agentMCPToolPermissionsValidator{}.ValidateMap(context.Background(), validator.MapRequest{
				Path:        path.Root("mcp_tool_permissions"),
				ConfigValue: stringMapValue(map[string]string{"server": configured}),
			}, &response)
			if response.Diagnostics.HasError() {
				t.Fatalf("valid JSON array rejected: %v", response.Diagnostics)
			}
		})
	}

	const secret = "sentinel-private-tool"
	invalid := []string{"", `null`, `{}`, `"value"`, `[1]`, `[true]`, `[{"name":"` + secret + `"}]`, `["ok", null]`, `["unterminated`}
	for _, configured := range invalid {
		configured := configured
		t.Run("invalid", func(t *testing.T) {
			var response validator.MapResponse
			agentMCPToolPermissionsValidator{}.ValidateMap(context.Background(), validator.MapRequest{
				Path:        path.Root("mcp_tool_permissions"),
				ConfigValue: stringMapValue(map[string]string{"sentinel-private-server": configured}),
			}, &response)
			if !response.Diagnostics.HasError() {
				t.Fatalf("invalid permission value was accepted")
			}
			diagnostics := fmt.Sprint(response.Diagnostics)
			for _, forbidden := range []string{secret, "sentinel-private-server", configured} {
				if forbidden != "" && strings.Contains(diagnostics, forbidden) {
					t.Fatalf("diagnostic leaked permission content")
				}
			}
		})
	}

	var nullResponse validator.MapResponse
	nullValue := types.MapValueMust(types.StringType, map[string]attr.Value{"server": types.StringNull()})
	agentMCPToolPermissionsValidator{}.ValidateMap(context.Background(), validator.MapRequest{Path: path.Root("mcp_tool_permissions"), ConfigValue: nullValue}, &nullResponse)
	if !nullResponse.Diagnostics.HasError() {
		t.Fatal("null map element was accepted")
	}
}

func TestBuildAgentRequestConvertsMCPToolPermissionsToNativeArrays(t *testing.T) {
	t.Parallel()

	resource := &AgentResource{}
	data := &AgentResourceModel{
		AgentName: types.StringValue("agent"),
		ObjectPermission: &AgentObjectPermissionModel{
			MCPToolPermissions: stringMapValue(map[string]string{
				"server-a": ` [ "first", "second" ] `,
				"server-b": `[]`,
			}),
		},
	}
	request, err := resource.buildAgentRequest(data)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	permissions := request["object_permission"].(map[string]interface{})["mcp_tool_permissions"]
	want := map[string][]string{"server-a": {"first", "second"}, "server-b": {}}
	if !reflect.DeepEqual(permissions, want) {
		t.Fatalf("wire permissions = %#v, want native string arrays", permissions)
	}

	data.ObjectPermission.MCPToolPermissions = types.MapValueMust(types.StringType, map[string]attr.Value{})
	request, err = resource.buildAgentRequest(data)
	if err != nil {
		t.Fatalf("build empty-map clear: %v", err)
	}
	permissionObject := request["object_permission"].(map[string]interface{})
	empty, present := permissionObject["mcp_tool_permissions"].(map[string][]string)
	if !present || len(empty) != 0 {
		t.Fatalf("explicit empty map clear omitted: %#v", permissionObject)
	}
}

func TestBuildAgentRequestRejectsMalformedLegacyMCPToolPermissionState(t *testing.T) {
	t.Parallel()

	data := &AgentResourceModel{
		AgentName: types.StringValue("agent"),
		ObjectPermission: &AgentObjectPermissionModel{
			MCPToolPermissions: stringMapValue(map[string]string{"private-server": "[old invalid rendering]"}),
		},
	}
	if _, err := (&AgentResource{}).buildAgentRequest(data); err == nil {
		t.Fatal("malformed old state reached request builder")
	} else if strings.Contains(err.Error(), "private-server") || strings.Contains(err.Error(), "old invalid rendering") {
		t.Fatal("request error leaked permission content")
	}
}

func TestReadAgentMCPToolPermissionsCanonicalizesAndPreservesEquivalentSpelling(t *testing.T) {
	t.Parallel()

	resource := &AgentResource{}
	configured := ` [ "first", "second" ] `
	data := AgentResourceModel{ObjectPermission: &AgentObjectPermissionModel{
		MCPToolPermissions: stringMapValue(map[string]string{"server": configured}),
	}}
	if err := resource.readObjectPermission(map[string]interface{}{
		"mcp_tool_permissions": map[string]interface{}{"server": []interface{}{"first", "second"}},
	}, &data); err != nil {
		t.Fatalf("configured read: %v", err)
	}
	if got := data.ObjectPermission.MCPToolPermissions.Elements()["server"].(types.String).ValueString(); got != configured {
		t.Fatalf("equivalent configured spelling changed to %q", got)
	}

	imported := AgentResourceModel{}
	if err := resource.readObjectPermission(map[string]interface{}{
		"mcp_tool_permissions": map[string]interface{}{
			"server": []interface{}{"first", "second"},
			"empty":  []interface{}{},
		},
	}, &imported); err != nil {
		t.Fatalf("import read: %v", err)
	}
	if got := imported.ObjectPermission.MCPToolPermissions.Elements()["server"].(types.String).ValueString(); got != `["first","second"]` {
		t.Fatalf("imported value = %q", got)
	}
	if got := imported.ObjectPermission.MCPToolPermissions.Elements()["empty"].(types.String).ValueString(); got != `[]` {
		t.Fatalf("imported empty array = %q", got)
	}

	legacy := AgentResourceModel{ObjectPermission: &AgentObjectPermissionModel{
		MCPToolPermissions: stringMapValue(map[string]string{"server": `[first second]`}),
	}}
	if err := resource.readObjectPermission(map[string]interface{}{
		"mcp_tool_permissions": map[string]interface{}{"server": []interface{}{"first", "second"}},
	}, &legacy); err != nil {
		t.Fatalf("legacy state repair: %v", err)
	}
	if got := legacy.ObjectPermission.MCPToolPermissions.Elements()["server"].(types.String).ValueString(); got != `["first","second"]` {
		t.Fatalf("legacy state was not repaired: %q", got)
	}
}

func TestReadAgentMCPToolPermissionsHonorsOmissionOwnership(t *testing.T) {
	t.Parallel()

	resource := &AgentResource{}
	configuredBlockWithOmittedMap := AgentResourceModel{ObjectPermission: &AgentObjectPermissionModel{
		MCPToolPermissions: types.MapNull(types.StringType),
	}}
	if err := resource.readObjectPermission(map[string]interface{}{
		"mcp_tool_permissions": map[string]interface{}{"server": []interface{}{"tool"}},
	}, &configuredBlockWithOmittedMap); err != nil {
		t.Fatalf("unowned read: %v", err)
	}
	if !configuredBlockWithOmittedMap.ObjectPermission.MCPToolPermissions.IsNull() {
		t.Fatal("an omitted map adopted API-owned permissions inside a configured block")
	}

	explicitEmpty := types.MapValueMust(types.StringType, map[string]attr.Value{})
	for name, response := range map[string]map[string]interface{}{
		"field omitted": {},
		"field null":    {"mcp_tool_permissions": nil},
	} {
		t.Run("empty map "+name, func(t *testing.T) {
			managed := AgentResourceModel{ObjectPermission: &AgentObjectPermissionModel{MCPToolPermissions: explicitEmpty}}
			if err := resource.readObjectPermission(response, &managed); err != nil {
				t.Fatalf("read: %v", err)
			}
			if !managed.ObjectPermission.MCPToolPermissions.Equal(explicitEmpty) {
				t.Fatal("authoritative absence erased an explicitly owned empty map")
			}
		})
	}

	nonempty := stringMapValue(map[string]string{"server": `["tool"]`})
	for name, response := range map[string]map[string]interface{}{
		"field omitted": {},
		"field null":    {"mcp_tool_permissions": nil},
	} {
		t.Run("nonempty map "+name, func(t *testing.T) {
			managed := AgentResourceModel{ObjectPermission: &AgentObjectPermissionModel{MCPToolPermissions: nonempty}}
			if err := resource.readObjectPermission(response, &managed); err != nil {
				t.Fatalf("read: %v", err)
			}
			if !managed.ObjectPermission.MCPToolPermissions.IsNull() {
				t.Fatal("authoritative absence silently retained a nonempty map")
			}
		})
	}

	siblingModels := stringListValue("model-owned")
	wholeObjectEmpty := AgentResourceModel{ObjectPermission: &AgentObjectPermissionModel{
		MCPToolPermissions: explicitEmpty,
		Models:             siblingModels,
	}}
	reconcileAbsentAgentMCPToolPermissions(&wholeObjectEmpty)
	if !wholeObjectEmpty.ObjectPermission.MCPToolPermissions.Equal(explicitEmpty) || !wholeObjectEmpty.ObjectPermission.Models.Equal(siblingModels) {
		t.Fatal("whole-object omission disturbed an empty clear or sibling field")
	}
	wholeObjectNonempty := AgentResourceModel{ObjectPermission: &AgentObjectPermissionModel{
		MCPToolPermissions: nonempty,
		Models:             siblingModels,
	}}
	reconcileAbsentAgentMCPToolPermissions(&wholeObjectNonempty)
	if !wholeObjectNonempty.ObjectPermission.MCPToolPermissions.IsNull() || !wholeObjectNonempty.ObjectPermission.Models.Equal(siblingModels) {
		t.Fatal("whole-object omission retained permissions or disturbed a sibling field")
	}
	unowned := AgentResourceModel{ObjectPermission: &AgentObjectPermissionModel{MCPToolPermissions: types.MapNull(types.StringType)}}
	reconcileAbsentAgentMCPToolPermissions(&unowned)
	if !unowned.ObjectPermission.MCPToolPermissions.IsNull() {
		t.Fatal("whole-object omission changed an unowned null map")
	}
	withoutBlock := AgentResourceModel{}
	reconcileAbsentAgentMCPToolPermissions(&withoutBlock)
	if withoutBlock.ObjectPermission != nil {
		t.Fatal("whole-object omission created an unowned block")
	}
}

func TestReadAgentMCPToolPermissionsRejectsMalformedResponses(t *testing.T) {
	t.Parallel()

	const private = "sentinel-private-value"
	malformed := []interface{}{
		private,
		[]interface{}{},
		map[string]interface{}{"private-server": private},
		map[string]interface{}{"private-server": []interface{}{private, 1}},
		map[string]interface{}{"private-server": []interface{}{nil}},
	}
	for _, raw := range malformed {
		data := AgentResourceModel{}
		err := (&AgentResource{}).readObjectPermission(map[string]interface{}{"mcp_tool_permissions": raw}, &data)
		if err == nil {
			t.Fatalf("malformed response accepted: %T", raw)
		}
		for _, forbidden := range []string{private, "private-server"} {
			if strings.Contains(err.Error(), forbidden) {
				t.Fatal("response error leaked permission content")
			}
		}
	}
}
