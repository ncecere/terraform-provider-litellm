package provider

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	frameworkdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestMCPParitySchemasAndSensitivity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var resourceResponse frameworkresource.SchemaResponse
	(&MCPServerResource{}).Schema(ctx, frameworkresource.SchemaRequest{}, &resourceResponse)
	if resourceResponse.Schema.Version != 8 {
		t.Fatalf("resource schema version = %d, want 8", resourceResponse.Schema.Version)
	}
	for _, name := range []string{"url", "spec_path", "command", "args", "env", "static_headers", "authorization_url", "token_url", "registration_url"} {
		if !resourceResponse.Schema.Attributes[name].IsSensitive() {
			t.Errorf("resource %s must be sensitive", name)
		}
	}
	for _, name := range []string{"extra_headers", "mcp_access_groups", "allowed_tools", "created_at", "created_by", "updated_at", "updated_by", "transport"} {
		if resourceResponse.Schema.Attributes[name].IsSensitive() {
			t.Errorf("resource %s must remain non-sensitive", name)
		}
	}
	for _, name := range []string{"updated_at", "updated_by"} {
		attribute, ok := resourceResponse.Schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || !attribute.Computed || attribute.Optional || len(attribute.PlanModifiers) != 0 {
			t.Errorf("resource %s lifecycle schema = %#v", name, resourceResponse.Schema.Attributes[name])
		}
	}

	var singularResponse frameworkdatasource.SchemaResponse
	(&MCPServerDataSource{}).Schema(ctx, frameworkdatasource.SchemaRequest{}, &singularResponse)
	for _, name := range []string{"url", "spec_path", "command", "args", "env", "static_headers", "authorization_url", "token_url", "registration_url"} {
		if !singularResponse.Schema.Attributes[name].IsSensitive() {
			t.Errorf("singular data source %s must be sensitive", name)
		}
	}
	upstream, ok := singularResponse.Schema.Attributes["upstream_resource"].(datasourceschema.StringAttribute)
	if !ok || !upstream.Computed || upstream.Sensitive {
		t.Fatalf("singular upstream_resource schema = %#v", singularResponse.Schema.Attributes["upstream_resource"])
	}

	var listResponse frameworkdatasource.SchemaResponse
	(&MCPServersListDataSource{}).Schema(ctx, frameworkdatasource.SchemaRequest{}, &listResponse)
	servers := listResponse.Schema.Attributes["mcp_servers"].(datasourceschema.ListNestedAttribute)
	for _, name := range []string{"mcp_access_groups", "allowed_tools", "command", "args", "env", "extra_headers", "static_headers", "authorization_url", "token_url", "registration_url"} {
		if _, present := servers.NestedObject.Attributes[name]; !present {
			t.Errorf("manager list is missing %s", name)
		}
	}
	for _, name := range []string{"url", "spec_path", "command", "args", "env", "static_headers", "authorization_url", "token_url", "registration_url"} {
		if !servers.NestedObject.Attributes[name].IsSensitive() {
			t.Errorf("manager list %s must be sensitive", name)
		}
	}
	for _, name := range []string{"credentials", "upstream_resource", "created_by", "updated_by", "last_health_check", "health_check_error"} {
		if _, present := servers.NestedObject.Attributes[name]; present {
			t.Errorf("manager list must not expose %s", name)
		}
	}
}

func TestProjectMCPServerManagerListRole(t *testing.T) {
	t.Parallel()
	response := map[string]interface{}{
		"server_id":          "manager-list",
		"server_name":        "manager",
		"transport":          "stdio",
		"auth_type":          "none",
		"mcp_access_groups":  []interface{}{"group"},
		"allowed_tools":      []interface{}{"search"},
		"command":            "python3",
		"args":               []interface{}{"server.py"},
		"env":                map[string]interface{}{"MODE": "safe"},
		"extra_headers":      []interface{}{"X-Trace"},
		"static_headers":     map[string]interface{}{},
		"authorization_url":  "https://auth.invalid/authorize",
		"token_url":          "https://auth.invalid/token",
		"registration_url":   "https://auth.invalid/register",
		"allow_all_keys":     false,
		"mcp_info":           map[string]interface{}{},
		"created_by":         false, // unavailable list identity is deliberately ignored
		"updated_by":         false,
		"health_check_error": false,
	}
	projected, err := projectMCPServerManagerListDataSource(response, "manager-list")
	if err != nil {
		t.Fatal(err)
	}
	if projected.MCPAccessGroups.IsNull() || projected.AllowedTools.IsNull() || projected.Args.IsNull() || projected.Env.IsNull() || projected.ExtraHeaders.IsNull() {
		t.Fatal("manager list dropped nonempty common fields")
	}
	if projected.StaticHeaders.IsNull() || projected.StaticHeaders.IsUnknown() {
		t.Fatal("authoritative empty static_headers was not retained")
	}
	if projected.AllowAllKeys.IsNull() || projected.AllowAllKeys.ValueBool() {
		t.Fatal("known false allow_all_keys was not retained")
	}
	if projected.MCPInfoJSON.IsNull() || projected.MCPInfoJSON.ValueString() != "{}" {
		t.Fatalf("empty mcp_info projection = %q", projected.MCPInfoJSON.ValueString())
	}

	const secret = "credential-secret-response"
	response["credentials"] = map[string]interface{}{"upstream_resource": secret}
	if _, err := projectMCPServerManagerListDataSource(response, "manager-list"); err == nil {
		t.Fatal("manager list accepted non-null credentials")
	} else if strings.Contains(err.Error(), secret) {
		t.Fatal("manager-list credential error exposed response content")
	}
}

func TestMCPServerUpdatedAuditProjectionAndRetention(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	resource := &MCPServerResource{}
	data := MCPServerResourceModel{
		ID:        types.StringValue("audit"),
		ServerID:  types.StringValue("audit"),
		UpdatedAt: types.StringUnknown(),
		UpdatedBy: types.StringUnknown(),
	}
	result := map[string]interface{}{
		"server_id": "audit", "transport": "http",
		"updated_at": "2026-09-01T00:00:00Z", "updated_by": "admin-id",
	}
	if err := resource.readMCPServerResultProjection(ctx, &data, result, emptyMCPInfoProvenance(), emptyMCPFieldOwnership(), false, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatal(err)
	}
	if data.UpdatedAt.ValueString() != "2026-09-01T00:00:00Z" || data.UpdatedBy.ValueString() != "admin-id" {
		t.Fatalf("updated audit fields were not projected: %#v", data)
	}

	result["updated_at"], result["updated_by"] = nil, nil
	if err := resource.readMCPServerResultProjection(ctx, &data, result, emptyMCPInfoProvenance(), emptyMCPFieldOwnership(), false, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatal(err)
	}
	if data.UpdatedAt.ValueString() != "2026-09-01T00:00:00Z" || data.UpdatedBy.ValueString() != "admin-id" {
		t.Fatal("role-redacted audit fields erased known prior values")
	}

	restricted := MCPServerResourceModel{
		ID:        types.StringValue("audit"),
		ServerID:  types.StringValue("audit"),
		UpdatedAt: types.StringUnknown(),
		UpdatedBy: types.StringUnknown(),
	}
	if err := resource.readMCPServerResultProjection(ctx, &restricted, result, emptyMCPInfoProvenance(), emptyMCPFieldOwnership(), true, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatal(err)
	}
	if !restricted.UpdatedAt.IsNull() || !restricted.UpdatedBy.IsNull() {
		t.Fatal("first restricted import did not resolve audit unknowns to typed null")
	}

	before := data
	result["updated_at"] = false
	if err := resource.readMCPServerResultProjection(ctx, &data, result, emptyMCPInfoProvenance(), emptyMCPFieldOwnership(), false, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err == nil {
		t.Fatal("malformed updated_at was accepted")
	}
	if !reflect.DeepEqual(before, data) {
		t.Fatal("malformed response partially changed staged resource state")
	}
}

func TestMCPServerV0ThroughV3UpgradeToV4(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	upgraders := (&MCPServerResource{}).UpgradeState(ctx)
	for _, version := range []int64{0, 1, 2, 3} {
		version := version
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			prior := map[string]interface{}{
				"id": "upgrade", "server_id": "upgrade", "transport": "http", "description": "preserved",
			}
			if version == 0 {
				prior["extra_headers"] = map[string]string{"Z-Header": "ignored", "A-Header": "ignored"}
			} else {
				prior["extra_headers"] = []string{"Z-Header", "A-Header"}
			}
			if version >= 2 {
				prior["mcp_info_json"] = `{"preserved":true}`
				prior["mcp_info_overrides_json"] = nil
				prior["mcp_info_clear_paths"] = nil
				prior["mcp_info_ownership_generation"] = float64(7)
			}
			if version == 3 {
				prior["field_ownership_generation"] = float64(9)
			}
			raw, err := json.Marshal(prior)
			if err != nil {
				t.Fatal(err)
			}
			response := frameworkresource.UpgradeStateResponse{}
			upgraders[version].StateUpgrader(ctx, frameworkresource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: raw}}, &response)
			if response.Diagnostics.HasError() || response.DynamicValue == nil {
				t.Fatalf("upgrade diagnostics: %v", response.Diagnostics)
			}
			var upgraded map[string]json.RawMessage
			if err := json.Unmarshal(response.DynamicValue.JSON, &upgraded); err != nil {
				t.Fatal(err)
			}
			if string(upgraded["updated_at"]) != "null" || string(upgraded["updated_by"]) != "null" {
				t.Fatalf("v%d updated fields were not injected as null", version)
			}
			if string(upgraded["description"]) != `"preserved"` {
				t.Fatalf("v%d changed unrelated public state", version)
			}
			if version == 3 && string(upgraded["field_ownership_generation"]) != "9" {
				t.Fatalf("v3 field ownership generation was overwritten: %s", upgraded["field_ownership_generation"])
			}
			if version < 3 && string(upgraded["field_ownership_generation"]) != "0" {
				t.Fatalf("v%d field ownership generation was not initialized", version)
			}
			if version == 0 && string(upgraded["extra_headers"]) != `["A-Header","Z-Header"]` {
				t.Fatalf("v0 extra_headers conversion = %s", upgraded["extra_headers"])
			}
		})
	}
}
