package provider

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func authoritativeTeamResponse(teamID string, metadata map[string]interface{}, aliases interface{}) map[string]interface{} {
	return map[string]interface{}{
		"team_id":          teamID,
		"keys":             []interface{}{},
		"team_memberships": []interface{}{},
		"team_info": map[string]interface{}{
			"team_id":          teamID,
			"team_alias":       "platform",
			"organization_id":  "org-1",
			"access_group_ids": []interface{}{},
			"models":           []interface{}{},
			"blocked":          false,
			"metadata":         metadata,
			"litellm_model_table": map[string]interface{}{
				"model_aliases": aliases,
			},
			"team_member_budget_table": map[string]interface{}{},
		},
	}
}

func TestProjectTeamInfoResponseUsesAuthoritativeV198Relations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	prior := TeamResourceModel{
		ID:                   types.StringValue("team-1"),
		TeamAlias:            types.StringValue("platform"),
		OrganizationID:       types.StringValue("org-old"),
		TPMLimitType:         types.StringValue("best_effort_throughput"),
		RPMLimitType:         types.StringValue("best_effort_throughput"),
		TeamMemberBudget:     types.Float64Value(1),
		MemberBudgetDuration: types.StringValue("1d"),
		TeamMemberRPMLimit:   types.Int64Value(1),
		TeamMemberTPMLimit:   types.Int64Value(1),
		Metadata: types.MapValueMust(types.StringType, map[string]attr.Value{
			"owner":  types.StringValue("terraform"),
			"secret": types.StringValue("configured-secret"),
		}),
		ModelRPMLimit: types.MapValueMust(types.Int64Type, map[string]attr.Value{"gpt": types.Int64Value(1)}),
		ModelTPMLimit: types.MapValueMust(types.Int64Type, map[string]attr.Value{"gpt": types.Int64Value(1)}),
	}
	response := authoritativeTeamResponse("team-1", map[string]interface{}{
		"owner":           "changed",
		"secret":          "litellm_enc::opaque",
		"private":         "must-not-be-adopted",
		"tags":            []interface{}{"prod"},
		"guardrails":      []interface{}{"safe"},
		"prompts":         []interface{}{},
		"tpm_limit_type":  "guaranteed_throughput",
		"rpm_limit_type":  "guaranteed_throughput",
		"model_rpm_limit": map[string]interface{}{"gpt": int64(9)},
		"model_tpm_limit": map[string]interface{}{},
	}, `{"fast":"gpt-4o"}`)
	teamInfo := response["team_info"].(map[string]interface{})
	teamInfo["team_member_budget_table"] = map[string]interface{}{
		"max_budget": 12.5, "budget_duration": "30d", "rpm_limit": int64(10), "tpm_limit": int64(1000),
	}

	projected, err := projectTeamInfoResponse(ctx, prior, response, false)
	if err != nil {
		t.Fatal(err)
	}
	if projected.OrganizationID.ValueString() != "org-1" || projected.TPMLimitType.ValueString() != "guaranteed_throughput" || projected.RPMLimitType.ValueString() != "guaranteed_throughput" {
		t.Fatalf("top/nested strings were not projected: %#v", projected)
	}
	if projected.TeamMemberBudget.ValueFloat64() != 12.5 || projected.TeamMemberRPMLimit.ValueInt64() != 10 || projected.TeamMemberTPMLimit.ValueInt64() != 1000 || projected.MemberBudgetDuration.ValueString() != "30d" {
		t.Fatalf("member budget relation was not projected: %#v", projected)
	}
	var metadata map[string]string
	if diagnostics := projected.Metadata.ElementsAs(ctx, &metadata, false); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if metadata["owner"] != "changed" || metadata["secret"] != "configured-secret" || len(metadata) != 2 {
		t.Fatalf("selective/masked metadata = %#v", metadata)
	}
	var aliases map[string]string
	if diagnostics := projected.ModelAliases.ElementsAs(ctx, &aliases, false); diagnostics.HasError() || aliases["fast"] != "gpt-4o" {
		t.Fatalf("model aliases = %#v diagnostics=%v", aliases, diagnostics)
	}
	var rpm, tpm map[string]int64
	projected.ModelRPMLimit.ElementsAs(ctx, &rpm, false)
	projected.ModelTPMLimit.ElementsAs(ctx, &tpm, false)
	if rpm["gpt"] != 9 || len(tpm) != 0 || projected.Tags.IsNull() || projected.Guardrails.IsNull() || projected.Prompts.IsNull() {
		t.Fatalf("nested list/map projection failed: rpm=%#v tpm=%#v tags=%v guardrails=%v prompts=%v", rpm, tpm, projected.Tags, projected.Guardrails, projected.Prompts)
	}
}

func TestProjectTeamInfoResponsePreservesAbsentNullAndEmpty(t *testing.T) {
	t.Parallel()
	prior := TeamResourceModel{
		ID:           types.StringValue("team-1"),
		TeamAlias:    types.StringValue("platform"),
		Tags:         types.ListValueMust(types.StringType, []attr.Value{types.StringValue("old")}),
		Guardrails:   types.ListValueMust(types.StringType, []attr.Value{types.StringValue("old")}),
		Prompts:      types.ListValueMust(types.StringType, []attr.Value{types.StringValue("old")}),
		ModelAliases: types.MapValueMust(types.StringType, map[string]attr.Value{"old": types.StringValue("model")}),
	}
	response := authoritativeTeamResponse("team-1", map[string]interface{}{
		"tags": []interface{}{}, "guardrails": nil,
	}, nil)
	projected, err := projectTeamInfoResponse(context.Background(), prior, response, false)
	if err != nil {
		t.Fatal(err)
	}
	if projected.Tags.IsNull() || len(projected.Tags.Elements()) != 0 {
		t.Fatalf("present empty tags = %v, want known empty", projected.Tags)
	}
	if !projected.Guardrails.IsNull() || !projected.Prompts.IsNull() || !projected.ModelAliases.IsNull() {
		t.Fatalf("null/omitted values not cleared distinctly: guardrails=%v prompts=%v aliases=%v", projected.Guardrails, projected.Prompts, projected.ModelAliases)
	}
}

func TestProjectTeamInfoResponseMalformedOrAmbiguousIsAtomic(t *testing.T) {
	t.Parallel()
	prior := TeamResourceModel{
		ID:        types.StringValue("team-1"),
		TeamID:    types.StringValue("team-1"),
		TeamAlias: types.StringValue("prior"),
		Tags:      types.ListValueMust(types.StringType, []attr.Value{types.StringValue("prior")}),
	}
	valid := func() map[string]interface{} {
		return authoritativeTeamResponse("team-1", map[string]interface{}{}, map[string]interface{}{})
	}
	tests := map[string]func(map[string]interface{}){
		"null envelope":                func(response map[string]interface{}) { response["team_info"] = nil },
		"missing keys relation":        func(response map[string]interface{}) { delete(response, "keys") },
		"missing memberships relation": func(response map[string]interface{}) { delete(response, "team_memberships") },
		"null required alias": func(response map[string]interface{}) {
			response["team_info"].(map[string]interface{})["team_alias"] = nil
		},
		"malformed metadata relation": func(response map[string]interface{}) {
			response["team_info"].(map[string]interface{})["metadata"] = []interface{}{}
		},
		"malformed nested list": func(response map[string]interface{}) {
			response["team_info"].(map[string]interface{})["metadata"].(map[string]interface{})["tags"] = "not-a-list"
		},
		"malformed model table": func(response map[string]interface{}) {
			response["team_info"].(map[string]interface{})["litellm_model_table"] = "not-an-object"
		},
		"malformed encoded aliases": func(response map[string]interface{}) {
			response["team_info"].(map[string]interface{})["litellm_model_table"].(map[string]interface{})["model_aliases"] = `[]`
		},
		"malformed member budget": func(response map[string]interface{}) {
			response["team_info"].(map[string]interface{})["team_member_budget_table"] = []interface{}{}
		},
		"ambiguous flat managed field": func(response map[string]interface{}) {
			response["team_info"].(map[string]interface{})["tags"] = []interface{}{"wrong-location"}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			response := valid()
			mutate(response)
			projected, err := projectTeamInfoResponse(context.Background(), prior, response, false)
			if err == nil {
				t.Fatal("malformed response was accepted")
			}
			if !reflect.DeepEqual(projected, prior) {
				t.Fatalf("failed projection mutated prior state: %#v", projected)
			}
			if strings.Contains(err.Error(), "configured-secret") {
				t.Fatalf("diagnostic leaked response content: %v", err)
			}
		})
	}
}

func TestProjectTeamInfoRejectsWrappedEnvelopeWithoutExactRootIdentity(t *testing.T) {
	prior := TeamResourceModel{ID: types.StringValue("team-projection"), TeamAlias: types.StringValue("team")}
	for name, response := range map[string]map[string]interface{}{
		"missing root identity": {"team_info": map[string]interface{}{"team_id": "team-projection", "team_alias": "team"}},
		"ambiguous root field":  {"team_id": "team-projection", "team_info": map[string]interface{}{"team_id": "team-projection", "team_alias": "team"}, "team_alias": "wrong-location"},
	} {
		t.Run(name, func(t *testing.T) {
			projected, err := projectTeamInfoResponse(context.Background(), prior, response, false)
			if err == nil || !reflect.DeepEqual(projected, prior) {
				t.Fatalf("wrapped malformed response was accepted: projected=%#v err=%v", projected, err)
			}
		})
	}
}

func TestProjectTeamInfoRejectsMaskedMetadataOnImport(t *testing.T) {
	prior := TeamResourceModel{
		ID:       types.StringValue("team-import"),
		TeamID:   types.StringValue("team-import"),
		Metadata: types.MapNull(types.StringType),
	}
	response := authoritativeTeamResponse("team-import", map[string]interface{}{
		"external_secret": "litellm_enc::masked",
	}, map[string]interface{}{})
	projected, err := projectTeamInfoResponse(context.Background(), prior, response, true)
	if err == nil || !reflect.DeepEqual(projected, prior) {
		t.Fatalf("masked import metadata was accepted: projected=%#v err=%v", projected, err)
	}
}

func TestProjectTeamPermissionsRequiresMatchingIdentity(t *testing.T) {
	prior := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("prior")})
	for name, response := range map[string]map[string]interface{}{
		"missing identity":    {"team_member_permissions": []interface{}{}},
		"mismatched identity": {"team_id": "other", "team_member_permissions": []interface{}{}},
		"missing permissions": {"team_id": "team-projection"},
		"null permissions":    {"team_id": "team-projection", "team_member_permissions": nil},
	} {
		t.Run(name, func(t *testing.T) {
			projected, err := projectTeamPermissions(context.Background(), prior, response, "team-projection")
			if err == nil || !projected.Equal(prior) {
				t.Fatalf("permissions identity was accepted: projected=%v err=%v", projected, err)
			}
		})
	}
}

func TestProjectTeamInfoResponseKeepsFlatCompatibility(t *testing.T) {
	t.Parallel()
	prior := TeamResourceModel{ID: types.StringValue("legacy"), TeamAlias: types.StringValue("old")}
	projected, err := projectTeamInfoResponse(context.Background(), prior, map[string]interface{}{
		"team_id": "legacy", "team_alias": "flat", "models": []interface{}{}, "metadata": nil,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if projected.TeamAlias.ValueString() != "flat" || projected.Models.IsNull() {
		t.Fatalf("flat compatibility projection = %#v", projected)
	}
}
