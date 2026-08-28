package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// validateSetAttribute runs every declared set validator against value and
// returns the combined diagnostics.
func validateSetAttribute(attribute resourceschema.SetAttribute, value types.Set) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	request := validator.SetRequest{
		Path:        path.Root("mcp_toolset_ids"),
		ConfigValue: value,
	}
	for _, candidate := range attribute.SetValidators() {
		var response validator.SetResponse
		candidate.ValidateSet(context.Background(), request, &response)
		diagnostics.Append(response.Diagnostics...)
	}
	return diagnostics
}

func TestMCPToolsetAssignmentSchemasAreOptionalSets(t *testing.T) {
	t.Parallel()

	resources := map[string]resource.Resource{
		"key":  &KeyResource{},
		"team": &TeamResource{},
	}
	for name, candidate := range resources {
		name, candidate := name, candidate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var response resource.SchemaResponse
			candidate.Schema(context.Background(), resource.SchemaRequest{}, &response)
			attribute, ok := response.Schema.Attributes["mcp_toolset_ids"].(resourceschema.SetAttribute)
			if !ok {
				t.Fatalf("mcp_toolset_ids schema = %T, want schema.SetAttribute", response.Schema.Attributes["mcp_toolset_ids"])
			}
			if !attribute.Optional || attribute.Computed || attribute.ElementType != types.StringType {
				t.Fatalf("mcp_toolset_ids schema = %#v, want optional-only set(string)", attribute)
			}
			if got := validateSetAttribute(attribute, types.SetValueMust(types.StringType, []attr.Value{types.StringValue("")})); !got.HasError() {
				t.Fatalf("empty-string toolset ID passed validation, want rejection")
			}
			if got := validateSetAttribute(attribute, types.SetValueMust(types.StringType, []attr.Value{types.StringValue("toolset-a")})); got.HasError() {
				t.Fatalf("valid toolset ID failed validation: %v", got.Errors())
			}
		})
	}
}

func TestMCPToolsetAssignmentsUseObjectPermissionField(t *testing.T) {
	t.Parallel()

	configured := types.SetValueMust(types.StringType, []attr.Value{
		types.StringValue("toolset-b"),
		types.StringValue("toolset-a"),
	})
	empty := types.SetValueMust(types.StringType, []attr.Value{})
	null := types.SetNull(types.StringType)

	tests := map[string]struct {
		build func(types.Set) (map[string]interface{}, error)
	}{
		"key": {
			build: func(value types.Set) (map[string]interface{}, error) {
				request, diagnostics := (&KeyResource{}).buildKeyRequest(context.Background(), &KeyResourceModel{MCPToolsetIDs: value})
				if diagnostics.HasError() {
					return nil, fmt.Errorf("build key request: %v", diagnostics.Errors())
				}
				return request, nil
			},
		},
		"team": {
			build: func(value types.Set) (map[string]interface{}, error) {
				return (&TeamResource{}).buildTeamRequest(context.Background(), &TeamResourceModel{
					TeamAlias: types.StringValue("team"), MCPToolsetIDs: value,
				}, "team-id")
			},
		},
	}

	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request, err := test.build(null)
			if err != nil {
				t.Fatalf("build null request: %v", err)
			}
			if _, present := request["object_permission"]; present {
				t.Fatalf("unmanaged request contains object_permission: %#v", request)
			}

			request, err = test.build(configured)
			if err != nil {
				t.Fatalf("build configured request: %v", err)
			}
			permission, ok := request["object_permission"].(map[string]interface{})
			if !ok {
				t.Fatalf("object_permission = %T, want map", request["object_permission"])
			}
			if got := permission["mcp_toolsets"]; !reflect.DeepEqual(got, []string{"toolset-a", "toolset-b"}) {
				t.Fatalf("mcp_toolsets = %#v, want sorted complete set", got)
			}

			request, err = test.build(empty)
			if err != nil {
				t.Fatalf("build empty request: %v", err)
			}
			permission = request["object_permission"].(map[string]interface{})
			if got := permission["mcp_toolsets"]; !reflect.DeepEqual(got, []string{}) {
				t.Fatalf("cleared mcp_toolsets = %#v, want explicit empty list", got)
			}
		})
	}
}

func TestMCPToolsetAssignmentsReadOnlyWhenManagedOrImported(t *testing.T) {
	t.Parallel()

	remote := map[string]interface{}{
		"object_permission_id": "permission-id",
		"mcp_servers":          []interface{}{"server-that-must-not-be-adopted"},
		"mcp_toolsets":         []interface{}{"toolset-b", "toolset-a"},
	}

	tests := map[string]struct {
		read func(*httptest.Server, types.Set, bool) (types.Set, error)
	}{
		"key": {
			read: func(server *httptest.Server, value types.Set, imported bool) (types.Set, error) {
				data := KeyResourceModel{Key: types.StringValue("sk-test"), MCPToolsetIDs: value}
				r := &KeyResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}
				err := r.readKeyWithNumericOwnership(context.Background(), &data, imported)
				return data.MCPToolsetIDs, err
			},
		},
		"team": {
			read: func(server *httptest.Server, value types.Set, imported bool) (types.Set, error) {
				data := TeamResourceModel{ID: types.StringValue("team-id"), MCPToolsetIDs: value}
				r := &TeamResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}
				err := r.readTeamWithNumericOwnership(context.Background(), &data, imported)
				return data.MCPToolsetIDs, err
			},
		},
	}

	for name, test := range tests {
		name, test := name, test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/team/permissions_list" {
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{
						"team_id":                 request.URL.Query().Get("team_id"),
						"team_member_permissions": []interface{}{},
					})
					return
				}
				switch name {
				case "key":
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": "sk-test", "info": map[string]interface{}{"token": "sk-test", "object_permission": remote}})
				case "team":
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{
						"team_id":          "team-id",
						"keys":             []interface{}{},
						"team_memberships": []interface{}{},
						"team_info":        map[string]interface{}{"team_id": "team-id", "team_alias": "team", "access_group_ids": []interface{}{}, "object_permission": remote},
					})
				}
			}))
			defer server.Close()

			unmanaged, err := test.read(server, types.SetNull(types.StringType), false)
			if err != nil {
				t.Fatalf("read unmanaged assignment: %v", err)
			}
			if !unmanaged.IsNull() {
				t.Fatalf("unmanaged assignment was adopted: %#v", unmanaged)
			}

			managed, err := test.read(server, types.SetValueMust(types.StringType, []attr.Value{types.StringValue("old")}), false)
			if err != nil {
				t.Fatalf("read managed assignment: %v", err)
			}
			if !managed.Equal(types.SetValueMust(types.StringType, []attr.Value{types.StringValue("toolset-a"), types.StringValue("toolset-b")})) {
				t.Fatalf("managed assignment = %#v, want remote membership", managed)
			}

			imported, err := test.read(server, types.SetNull(types.StringType), true)
			if err != nil {
				t.Fatalf("read imported assignment: %v", err)
			}
			if !imported.Equal(managed) {
				t.Fatalf("imported assignment = %#v, want %#v", imported, managed)
			}
		})
	}
}

// The key planner must treat assignment-only differences as real configuration
// changes so ModifyPlan does not replace an assignment update, clear, or
// omission-based ownership release with prior state.
func TestKeyPlannerTreatsMCPToolsetIDsAsConfigurationChange(t *testing.T) {
	t.Parallel()

	assigned := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("toolset-a")})
	empty := types.SetValueMust(types.StringType, []attr.Value{})

	var config, state KeyResourceModel
	config.MCPToolsetIDs = types.SetNull(types.StringType)
	state.MCPToolsetIDs = types.SetNull(types.StringType)
	if keyHasNonSemanticConfigurationChange(config, state) {
		t.Fatal("identical unmanaged models must not force a plan")
	}

	config.MCPToolsetIDs = assigned
	if !keyHasNonSemanticConfigurationChange(config, state) {
		t.Fatal("new assignment must produce a plan")
	}

	state.MCPToolsetIDs = assigned
	if keyHasNonSemanticConfigurationChange(config, state) {
		t.Fatal("equal assignment must not force a plan")
	}

	config.MCPToolsetIDs = empty
	if !keyHasNonSemanticConfigurationChange(config, state) {
		t.Fatal("explicit [] clear must produce a plan")
	}

	config.MCPToolsetIDs = types.SetNull(types.StringType)
	if !keyHasNonSemanticConfigurationChange(config, state) {
		t.Fatal("omission-based ownership release must produce a plan")
	}

	config.MCPToolsetIDs = types.SetUnknown(types.StringType)
	if !keyHasNonSemanticConfigurationChange(config, state) {
		t.Fatal("unknown assignment must produce a plan")
	}
}

// Team update convergence must compare mcp_toolset_ids so an accepted update
// whose readback does not reflect the planned assignments is not published.
func TestTeamChangedFieldMismatchComparesMCPToolsetIDs(t *testing.T) {
	t.Parallel()

	assigned := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("toolset-a")})
	desired := partialTeamState("team-id")
	prior := partialTeamState("team-id")
	actual := partialTeamState("team-id")
	desired.MCPToolsetIDs = assigned

	field, mismatch := teamChangedFieldMismatch(desired, prior, actual)
	if !mismatch || field != "mcp_toolset_ids" {
		t.Fatalf("mismatch = (%q, %t), want (\"mcp_toolset_ids\", true)", field, mismatch)
	}

	actual.MCPToolsetIDs = assigned
	if field, mismatch := teamChangedFieldMismatch(desired, prior, actual); mismatch {
		t.Fatalf("converged assignments reported mismatch on %q", field)
	}
}
