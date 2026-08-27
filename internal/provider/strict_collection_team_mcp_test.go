package provider

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
