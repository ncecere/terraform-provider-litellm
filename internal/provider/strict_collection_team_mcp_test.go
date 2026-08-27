package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestStrictTeamAndMCPRequestCollectionsFailAtomically(t *testing.T) {
	t.Parallel()
	lateInvalidList := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("configured-first-value"),
		types.StringNull(),
	})

	t.Run("team list", func(t *testing.T) {
		request, err := (&TeamResource{}).buildTeamRequest(context.Background(), &TeamResourceModel{
			TeamAlias: types.StringValue("team"),
			Models:    lateInvalidList,
		}, "team-id")
		if err == nil || request != nil {
			t.Fatalf("invalid team list produced request=%#v error=%v", request, err)
		}
		if strings.Contains(err.Error(), "configured-first-value") {
			t.Fatal("team collection diagnostic exposed configured content")
		}
	})

	t.Run("MCP list", func(t *testing.T) {
		request, err := (&MCPServerResource{}).buildMCPServerRequest(context.Background(), &MCPServerResourceModel{
			Transport:    types.StringValue("http"),
			AuthType:     types.StringValue("none"),
			AllowedTools: lateInvalidList,
		}, nil, false)
		if err == nil || request != nil {
			t.Fatalf("invalid MCP list produced request=%#v error=%v", request, err)
		}
		if strings.Contains(err.Error(), "configured-first-value") {
			t.Fatal("MCP collection diagnostic exposed configured content")
		}
	})

	t.Run("MCP sensitive map", func(t *testing.T) {
		credentials := types.MapValueMust(types.StringType, map[string]attr.Value{
			"secret-key-name": types.StringUnknown(),
		})
		request, err := (&MCPServerResource{}).buildMCPServerRequest(context.Background(), &MCPServerResourceModel{
			Transport:   types.StringValue("http"),
			AuthType:    types.StringValue("api_key"),
			Credentials: credentials,
		}, nil, false)
		if err == nil || request != nil {
			t.Fatalf("invalid MCP map produced request=%#v error=%v", request, err)
		}
		if strings.Contains(err.Error(), "secret-key-name") {
			t.Fatal("MCP collection diagnostic exposed a sensitive map key")
		}
	})
}

func TestStrictTeamRouterSettingsFailAtomically(t *testing.T) {
	t.Parallel()
	fallbacks := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("fallback-ok"),
		types.StringNull(),
	})
	entry := types.ObjectValueMust(fallbackEntryAttrTypes, map[string]attr.Value{
		"model":           types.StringValue("primary"),
		"fallback_models": fallbacks,
	})
	router := types.ObjectValueMust(routerSettingsAttrTypes, map[string]attr.Value{
		"fallbacks":                types.ListValueMust(types.ObjectType{AttrTypes: fallbackEntryAttrTypes}, []attr.Value{entry}),
		"context_window_fallbacks": types.ListNull(types.ObjectType{AttrTypes: fallbackEntryAttrTypes}),
	})
	request, err := (&TeamResource{}).buildTeamRequest(context.Background(), &TeamResourceModel{
		TeamAlias:      types.StringValue("team"),
		RouterSettings: router,
	}, "team-id")
	if err == nil || request != nil {
		t.Fatalf("invalid nested router collection produced request=%#v error=%v", request, err)
	}
	if strings.Contains(err.Error(), "fallback-ok") {
		t.Fatal("router collection diagnostic exposed configured content")
	}
}

func TestStrictTeamAndMCPRequestCollectionsHonorCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	values := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("value")})

	if request, err := (&TeamResource{}).buildTeamRequest(ctx, &TeamResourceModel{TeamAlias: types.StringValue("team"), Models: values}, "team-id"); err == nil || request != nil {
		t.Fatalf("canceled team conversion produced request=%#v error=%v", request, err)
	}
	if request, err := (&MCPServerResource{}).buildMCPServerRequest(ctx, &MCPServerResourceModel{Transport: types.StringValue("http"), AuthType: types.StringValue("none"), AllowedTools: values}, nil, false); err == nil || request != nil {
		t.Fatalf("canceled MCP conversion produced request=%#v error=%v", request, err)
	}
}

func TestMCPCollectionProjectionFailureIsTransactional(t *testing.T) {
	t.Parallel()
	prior := MCPServerResourceModel{
		ID:         types.StringValue("mcp-id"),
		ServerID:   types.StringValue("mcp-id"),
		ServerName: types.StringValue("prior_name"),
		Transport:  types.StringValue("http"),
		MCPInfo: &MCPInfoModel{
			ServerName:  types.StringValue("prior info"),
			Description: types.StringNull(),
			LogoURL:     types.StringNull(),
			MCPServerCostInfo: &MCPServerCostInfoModel{
				DefaultCostPerQuery:    types.Float64Null(),
				ToolNameToCostPerQuery: types.MapNull(types.Float64Type),
			},
		},
	}
	data := prior
	confirmed := mcpInfoLeafSet{"prior-confirmed": true}
	adopted := mcpInfoLeafSet{"prior-adopted": true}
	response := map[string]interface{}{
		"server_id":   "mcp-id",
		"transport":   "http",
		"server_name": "changed_name",
		"mcp_info": map[string]interface{}{
			"server_name": "changed info",
			"mcp_server_cost_info": map[string]interface{}{
				"tool_name_to_cost_per_query": map[string]interface{}{
					"valid":        1.0,
					"late-invalid": "not-a-number",
				},
			},
		},
	}
	ownership := emptyMCPInfoProvenance()
	ownership.Terraform[mcpInfoServerNameLeaf] = true
	ownership.Terraform[mcpInfoToolCostsLeaf] = true

	err := (&MCPServerResource{}).readMCPServerResultProjection(context.Background(), &data, response, ownership, emptyMCPFieldOwnership(), false, confirmed, adopted)
	if err == nil {
		t.Fatal("late malformed MCP collection was accepted")
	}
	if !reflect.DeepEqual(data, prior) {
		t.Fatalf("failed MCP projection changed public model: got=%#v want=%#v", data, prior)
	}
	if !reflect.DeepEqual(confirmed, mcpInfoLeafSet{"prior-confirmed": true}) || !reflect.DeepEqual(adopted, mcpInfoLeafSet{"prior-adopted": true}) {
		t.Fatalf("failed MCP projection changed provenance: confirmed=%#v adopted=%#v", confirmed, adopted)
	}
}

func TestUnifiedAccessGroupCollectionProjectionIsTransactional(t *testing.T) {
	t.Parallel()
	prior := UnifiedAccessGroupResourceModel{
		ID:                 types.StringValue("group-id"),
		AccessGroupID:      types.StringValue("group-id"),
		AccessGroupName:    types.StringValue("prior"),
		AccessModelNames:   types.ListValueMust(types.StringType, []attr.Value{types.StringValue("prior-model")}),
		AccessMCPServerIDs: types.ListNull(types.StringType),
		AccessAgentIDs:     types.ListNull(types.StringType),
		AssignedTeamIDs:    types.ListNull(types.StringType),
	}
	data := prior
	err := readUnifiedAccessGroupResponse(context.Background(), map[string]interface{}{
		"access_group_id":       "group-id",
		"access_group_name":     "changed",
		"access_model_names":    []interface{}{"valid-prefix"},
		"access_mcp_server_ids": []interface{}{"valid-prefix", false},
		"access_agent_ids":      []interface{}{},
		"assigned_team_ids":     []interface{}{},
	}, &data)
	if err == nil {
		t.Fatal("late malformed unified access-group collection was accepted")
	}
	if !reflect.DeepEqual(data, prior) {
		t.Fatalf("failed unified access-group projection changed state: got=%#v want=%#v", data, prior)
	}
}

func TestUnifiedAccessGroupDataSourceMalformedCollectionsPublishNoStateProtocol(t *testing.T) {
	t.Parallel()
	validHash := strings.Repeat("a", 64)
	for name, malformed := range map[string]map[string]interface{}{
		"late list element": {"access_mcp_server_ids": []interface{}{"valid-prefix", false}},
		"late assigned key": {"assigned_key_ids": []interface{}{validHash, false}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			response := map[string]interface{}{
				"access_group_id": "group-id", "access_group_name": "group",
				"access_model_names": []interface{}{}, "access_mcp_server_ids": []interface{}{},
				"access_agent_ids": []interface{}{}, "assigned_team_ids": []interface{}{},
				"assigned_key_ids": []interface{}{},
			}
			for field, value := range malformed {
				response[field] = value
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(writer).Encode(response)
			}))
			defer server.Close()

			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			const typeName = "litellm_unified_access_group"
			schema := schemas.DataSourceSchemas[typeName]
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"access_group_id": "group-id"}))
			read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: typeName, Config: config})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("malformed data source response was accepted: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
			}
			if read.State != nil {
				attributes := protocolAttributeMap(t, schema, read.State)
				for field, value := range attributes {
					if field == "access_group_id" {
						continue
					}
					if !value.IsNull() {
						t.Fatalf("malformed data source response published response-derived %s state", field)
					}
				}
			}
			if strings.Contains(agentProtocolDiagnosticsText(read.Diagnostics), validHash) {
				t.Fatal("malformed data source diagnostic exposed a response value")
			}
		})
	}
}

func TestTeamCollectionProjectionCancellationIsTransactional(t *testing.T) {
	t.Parallel()
	prior := TeamResourceModel{
		ID:        types.StringValue("team-id"),
		TeamID:    types.StringValue("team-id"),
		TeamAlias: types.StringValue("prior"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	projected, err := projectTeamInfoResponse(ctx, prior, map[string]interface{}{
		"team_id":          "team-id",
		"team_alias":       "changed",
		"access_group_ids": []interface{}{},
	}, false)
	if err == nil {
		t.Fatal("canceled Team collection projection was accepted")
	}
	if !reflect.DeepEqual(projected, prior) {
		t.Fatalf("canceled Team projection changed public model: got=%#v want=%#v", projected, prior)
	}
}
