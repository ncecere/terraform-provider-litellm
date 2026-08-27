package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestStage3KeyRequestCollectionsFailAtomicallyAndDoNotActivateTeamSentinel(t *testing.T) {
	t.Parallel()

	models := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("valid-before-error"),
		types.StringUnknown(),
	})
	metadata := types.MapValueMust(types.StringType, map[string]attr.Value{
		"sensitive-name": types.StringNull(),
	})
	request, diagnostics := (&KeyResource{}).buildKeyRequest(context.Background(), &KeyResourceModel{
		TeamID:   types.StringValue("team-1"),
		Models:   models,
		Metadata: metadata,
	})
	if request != nil {
		t.Fatalf("invalid key collections produced a request, including a possible all-team sentinel: %#v", request)
	}
	if len(diagnostics.Errors()) != 2 {
		t.Fatalf("diagnostics = %#v, want both collection failures", diagnostics)
	}
	assertCollectionDiagnostics(t, diagnostics[:1], 1, `models[1]`)
	assertCollectionDiagnostics(t, diagnostics[1:], 1, "metadata")
}

func TestStage3ModelRequestCollectionsRejectWrongNullAndUnknownElements(t *testing.T) {
	t.Parallel()

	data := ModelResourceModel{
		AccessGroups: types.ListValueMust(types.StringType, []attr.Value{
			types.StringValue("first"), types.StringNull(),
		}),
		AdditionalLiteLLMParams: types.MapValueMust(types.DynamicType, map[string]attr.Value{
			"wrong": types.DynamicValue(types.Int64Value(42)),
		}),
		AdditionalModelInfo: types.MapValueMust(types.StringType, map[string]attr.Value{
			"unknown": types.StringUnknown(),
		}),
	}
	collections, diagnostics := convertModelRequestCollections(context.Background(), data)
	if collections.accessGroups != nil || collections.additionalLiteLLMParams != nil || collections.additionalModelInfo != nil {
		t.Fatalf("invalid model collections produced partial converted values: %#v", collections)
	}
	if len(diagnostics.Errors()) != 3 {
		t.Fatalf("diagnostics = %#v, want three collection failures", diagnostics)
	}
	assertCollectionDiagnostics(t, diagnostics[:1], 1, `access_groups[1]`)
	assertCollectionDiagnostics(t, diagnostics[1:2], 1, "additional_litellm_params")
	assertCollectionDiagnostics(t, diagnostics[2:], 1, `additional_model_info["unknown"]`)
}

func TestStage3AgentNestedRequestCollectionsRejectAtomically(t *testing.T) {
	t.Parallel()

	scopes := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("read"), types.StringUnknown()})
	securityItem := types.MapValueMust(types.ListType{ElemType: types.StringType}, map[string]attr.Value{"oauth-secret-name": scopes})
	security := types.ListValueMust(types.MapType{ElemType: types.ListType{ElemType: types.StringType}}, []attr.Value{securityItem})
	data := AgentResourceModel{
		AgentName: types.StringValue("agent"),
		StaticHeaders: types.MapValueMust(types.StringType, map[string]attr.Value{
			"Authorization": types.StringUnknown(),
		}),
		AgentCard: &AgentCardModel{
			Name: types.StringValue("card"), URL: types.StringValue("https://example.test"),
			Skills: []AgentSkillModel{{ID: types.StringValue("skill"), Name: types.StringValue("Skill"), Security: security}},
		},
		ObjectPermission: &AgentObjectPermissionModel{
			Models: types.ListValueMust(types.StringType, []attr.Value{types.StringNull()}),
			MCPToolPermissions: types.MapValueMust(types.StringType, map[string]attr.Value{
				"sensitive-server": types.StringUnknown(),
			}),
		},
	}
	request, err := (&AgentResource{}).buildAgentRequest(context.Background(), &data)
	if err == nil || request != nil {
		t.Fatalf("invalid nested agent collections were not rejected atomically: request=%#v err=%v", request, err)
	}
	diagnostics := validateAgentRequestCollections(context.Background(), data)
	if len(diagnostics.Errors()) != 4 {
		t.Fatalf("diagnostics = %#v, want four collection failures", diagnostics)
	}
	wantPaths := []string{
		"static_headers",
		`agent_card.skills[0].security[0]`,
		`object_permission.models[0]`,
		"object_permission.mcp_tool_permissions",
	}
	for index, wantPath := range wantPaths {
		assertCollectionDiagnostics(t, diagnostics[index:index+1], 1, wantPath)
	}
}

func TestStage3AgentImportedJSONProjectionDoesNotBecomeAConfiguredConflict(t *testing.T) {
	t.Parallel()

	importedProjection := AgentResourceModel{
		LiteLLMParams: types.MapValueMust(types.StringType, map[string]attr.Value{
			"numeric": types.StringValue("42"),
		}),
		LiteLLMParamsJSON: types.StringValue(`{"numeric":42}`),
	}
	if diagnostics := validateAgentRequestCollections(context.Background(), importedProjection); diagnostics.HasError() {
		t.Fatalf("valid imported compatibility projection was rejected: %#v", diagnostics)
	}
	if diagnostics := validateAgentConfiguredParams(AgentResourceModel{}); diagnostics.HasError() {
		t.Fatalf("unconfigured update parameters were rejected: %#v", diagnostics)
	}
}

func TestStage3RequestCollectionPreflightsHonorCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("value")})
	if request, diagnostics := (&KeyResource{}).buildKeyRequest(ctx, &KeyResourceModel{Models: value}); request != nil || !diagnostics.HasError() {
		t.Fatalf("canceled key conversion: request=%#v diagnostics=%#v", request, diagnostics)
	}
	if diagnostics := validateModelRequestCollections(ctx, ModelResourceModel{AccessGroups: value}); !diagnostics.HasError() {
		t.Fatal("canceled model conversion did not return an error diagnostic")
	}
	agent := AgentResourceModel{AgentName: types.StringValue("agent"), ExtraHeaders: value}
	if diagnostics := validateAgentRequestCollections(ctx, agent); !diagnostics.HasError() {
		t.Fatal("canceled agent conversion did not return an error diagnostic")
	}
	if request, err := (&AgentResource{}).buildAgentRequest(ctx, &agent); err == nil || request != nil {
		t.Fatalf("canceled agent create request: request=%#v err=%v", request, err)
	}
	if request, err := (&AgentResource{}).buildAgentUpdateRequest(ctx, &agent, &AgentResourceModel{}, &agent, agentFieldSet{}); err == nil || request != nil {
		t.Fatalf("canceled agent update request: request=%#v err=%v", request, err)
	}
}

func TestStage3ValidAgentCollectionPayloadPreservesOrderAndDuplicates(t *testing.T) {
	t.Parallel()

	values := types.ListValueMust(types.StringType, []attr.Value{
		types.StringValue("second"), types.StringValue("first"), types.StringValue("second"),
	})
	request, err := (&AgentResource{}).buildAgentRequest(context.Background(), &AgentResourceModel{
		AgentName:    types.StringValue("agent"),
		ExtraHeaders: values,
		ObjectPermission: &AgentObjectPermissionModel{
			Models: values,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertOrdered := func(raw interface{}) {
		t.Helper()
		got, ok := raw.([]string)
		if !ok || len(got) != 3 || got[0] != "second" || got[1] != "first" || got[2] != "second" {
			t.Fatalf("ordered duplicate-preserving payload = %#v", raw)
		}
	}
	assertOrdered(request["extra_headers"])
	permission := request["object_permission"].(map[string]interface{})
	assertOrdered(permission["models"])
}
