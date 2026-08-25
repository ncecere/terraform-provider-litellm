package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

func TestTeamIDSchemaSupportsConfiguredOrGeneratedIdentity(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&TeamResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	attribute, ok := response.Schema.Attributes["team_id"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("team_id schema type = %T, want schema.StringAttribute", response.Schema.Attributes["team_id"])
	}
	if !attribute.Optional || !attribute.Computed || attribute.Required {
		t.Fatalf("team_id must be Optional+Computed: %#v", attribute)
	}
	if len(attribute.Validators) != 1 {
		t.Fatalf("team_id validators = %d, want 1", len(attribute.Validators))
	}
	if len(attribute.PlanModifiers) != 2 {
		t.Fatalf("team_id plan modifiers = %d, want state preservation and replacement", len(attribute.PlanModifiers))
	}
}

func TestTeamImportMirrorsIDAndTeamID(t *testing.T) {
	t.Parallel()

	teamResource := &TeamResource{}
	var schemaResponse resource.SchemaResponse
	teamResource.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state, err := nullStateFor(schemaResponse.Schema)
	if err != nil {
		t.Fatalf("build import state: %v", err)
	}
	response := &resource.ImportStateResponse{State: state}
	teamResource.ImportState(
		context.Background(),
		resource.ImportStateRequest{ID: "external-team"},
		response,
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var data TeamResourceModel
	response.Diagnostics.Append(response.State.Get(context.Background(), &data)...)
	if response.Diagnostics.HasError() {
		t.Fatalf("decode imported state: %v", response.Diagnostics)
	}
	if data.ID.ValueString() != "external-team" || data.TeamID.ValueString() != "external-team" {
		t.Fatalf("imported identity: id=%q team_id=%q", data.ID.ValueString(), data.TeamID.ValueString())
	}
	if !data.TPMLimitType.IsNull() || !data.RPMLimitType.IsNull() {
		t.Fatalf("import must leave create-only limit types unconfigured: tpm=%v rpm=%v", data.TPMLimitType, data.RPMLimitType)
	}
}

func TestTeamAccessGroupIDsSchemaIsOptionalUnordered(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&TeamResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	attribute, ok := response.Schema.Attributes["access_group_ids"].(resourceschema.SetAttribute)
	if !ok {
		t.Fatalf("access_group_ids schema type = %T, want schema.SetAttribute", response.Schema.Attributes["access_group_ids"])
	}
	if !attribute.Optional || !attribute.Computed || attribute.Required {
		t.Fatalf("access_group_ids must be Optional+Computed: %#v", attribute)
	}
	if len(attribute.Validators) != 1 {
		t.Fatalf("access_group_ids validators = %d, want 1", len(attribute.Validators))
	}
}

func TestBuildTeamRequestIncludesManagedAccessGroupIDs(t *testing.T) {
	t.Parallel()

	data := &TeamResourceModel{
		TeamAlias: types.StringValue("access-group-team"),
		AccessGroupIDs: types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("group-b"),
			types.StringValue("group-a"),
		}),
	}
	request, err := (&TeamResource{}).buildTeamRequest(context.Background(), data, "team-123")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := request["access_group_ids"].([]string)
	if !ok {
		t.Fatalf("access_group_ids request type = %T, want []string", request["access_group_ids"])
	}
	if len(got) != 2 || !slices.Contains(got, "group-a") || !slices.Contains(got, "group-b") {
		t.Fatalf("access_group_ids request = %v", got)
	}

	data.AccessGroupIDs = types.SetValueMust(types.StringType, []attr.Value{})
	request, err = (&TeamResource{}).buildTeamRequest(context.Background(), data, "team-123")
	if err != nil {
		t.Fatal(err)
	}
	got, ok = request["access_group_ids"].([]string)
	if !ok || len(got) != 0 {
		t.Fatalf("empty access_group_ids request = %#v, want []string{}", request["access_group_ids"])
	}
}

func TestTeamIDForCreatePreservesCustomOrGeneratesUUID(t *testing.T) {
	t.Parallel()

	if got := teamIDForCreate(types.StringValue("engineering-platform")); got != "engineering-platform" {
		t.Fatalf("configured team ID = %q, want engineering-platform", got)
	}
	for name, value := range map[string]types.String{
		"null":    types.StringNull(),
		"unknown": types.StringUnknown(),
	} {
		t.Run(name, func(t *testing.T) {
			got := teamIDForCreate(value)
			if _, err := uuid.Parse(got); err != nil {
				t.Fatalf("generated team ID %q is not a UUID: %v", got, err)
			}
		})
	}
}

func TestReadTeamCustomIDIsEscapedAndMirrored(t *testing.T) {
	t.Parallel()

	const teamID = "engineering/group #1&ops"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.URL.Query().Get("team_id"); got != teamID {
			http.Error(writer, "unexpected team ID: "+got, http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"team_info": map[string]interface{}{
					"team_id":          teamID,
					"team_alias":       "Engineering Platform",
					"access_group_ids": []interface{}{"group-b", "group-a"},
				},
			})
		case "/team/permissions_list":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"team_member_permissions": []interface{}{},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	teamResource := &TeamResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := TeamResourceModel{
		ID:        types.StringValue(teamID),
		TeamID:    types.StringNull(),
		TeamAlias: types.StringValue("Engineering Platform"),
		AccessGroupIDs: types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("group-a"),
			types.StringValue("group-b"),
		}),
		TeamMemberPermissions: types.ListUnknown(types.StringType),
	}
	if err := teamResource.readTeam(context.Background(), &data); err != nil {
		t.Fatalf("readTeam returned error: %v", err)
	}
	if data.TeamID.ValueString() != teamID || data.ID.ValueString() != teamID {
		t.Fatalf("team identity not mirrored: id=%q team_id=%q", data.ID.ValueString(), data.TeamID.ValueString())
	}
	expectedGroups := types.SetValueMust(types.StringType, []attr.Value{
		types.StringValue("group-a"),
		types.StringValue("group-b"),
	})
	if !data.AccessGroupIDs.Equal(expectedGroups) {
		t.Fatalf("access_group_ids = %v, want %v", data.AccessGroupIDs, expectedGroups)
	}
}

func TestReadTeamRejectsMismatchedRemoteIdentity(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"team_info": map[string]interface{}{
				"team_id":    "different-team",
				"team_alias": "Different",
			},
		})
	}))
	defer server.Close()

	teamResource := &TeamResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := TeamResourceModel{ID: types.StringValue("expected-team")}
	if err := teamResource.readTeam(context.Background(), &data); err == nil || !strings.Contains(err.Error(), "different-team") {
		t.Fatalf("readTeam mismatch error = %v, want remote identity diagnostic", err)
	}
}

func TestReadTeamResolvesUnknownOptionalComputedCollections(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_alias": "agent-team",
				"blocked":    false,
			})
		case "/team/permissions_list":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_member_permissions": []string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := &TeamResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := TeamResourceModel{
		ID:                    types.StringValue("team-123"),
		TeamAlias:             types.StringValue("agent-team"),
		Models:                types.ListUnknown(types.StringType),
		Tags:                  types.ListUnknown(types.StringType),
		Guardrails:            types.ListUnknown(types.StringType),
		Prompts:               types.ListUnknown(types.StringType),
		Metadata:              types.MapUnknown(types.StringType),
		ModelAliases:          types.MapUnknown(types.StringType),
		ModelRPMLimit:         types.MapUnknown(types.Int64Type),
		ModelTPMLimit:         types.MapUnknown(types.Int64Type),
		TeamMemberPermissions: types.ListUnknown(types.StringType),
	}

	if err := r.readTeam(context.Background(), &data); err != nil {
		t.Fatalf("readTeam returned error: %v", err)
	}

	if data.Models.IsUnknown() {
		t.Fatal("models should be known after read")
	}
	if data.Tags.IsUnknown() {
		t.Fatal("tags should be known after read")
	}
	if data.Guardrails.IsUnknown() {
		t.Fatal("guardrails should be known after read")
	}
	if data.Prompts.IsUnknown() {
		t.Fatal("prompts should be known after read")
	}
	if data.Metadata.IsUnknown() {
		t.Fatal("metadata should be known after read")
	}
	if data.ModelAliases.IsUnknown() {
		t.Fatal("model_aliases should be known after read")
	}
	if data.ModelRPMLimit.IsUnknown() {
		t.Fatal("model_rpm_limit should be known after read")
	}
	if data.ModelTPMLimit.IsUnknown() {
		t.Fatal("model_tpm_limit should be known after read")
	}
	if data.TeamMemberPermissions.IsUnknown() {
		t.Fatal("team_member_permissions should be known after read")
	}
}

func TestTeamDefaultMemberBudgetDurationSchema(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&TeamResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	attribute, ok := response.Schema.Attributes["team_member_budget_duration"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("team_member_budget_duration schema type = %T", response.Schema.Attributes["team_member_budget_duration"])
	}
	if !attribute.Optional || attribute.Computed || attribute.Required {
		t.Fatalf("team_member_budget_duration must be Optional-only: %#v", attribute)
	}
	if len(attribute.Validators) != 1 {
		t.Fatalf("team_member_budget_duration validators = %d, want duration format validator", len(attribute.Validators))
	}
}

func TestBuildTeamRequestIncludesMemberBudgetDuration(t *testing.T) {
	t.Parallel()

	request, err := (&TeamResource{}).buildTeamRequest(context.Background(), &TeamResourceModel{
		TeamAlias:            types.StringValue("budget-team"),
		TeamMemberBudget:     types.Float64Value(50),
		MemberBudgetDuration: types.StringValue("30d"),
	}, "team-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := request["team_member_budget_duration"]; got != "30d" {
		t.Fatalf("team_member_budget_duration = %#v, want 30d", got)
	}
}

func TestTeamBudgetInputsRemainOptionalOnly(t *testing.T) {
	t.Parallel()

	var resp resource.SchemaResponse
	(&TeamResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("team schema returned diagnostics: %v", resp.Diagnostics)
	}

	for _, name := range []string{"max_budget", "budget_duration"} {
		attribute, ok := resp.Schema.Attributes[name]
		if !ok {
			t.Fatalf("team schema missing %q", name)
		}
		if !attribute.IsOptional() {
			t.Errorf("%s must remain Optional", name)
		}
		if attribute.IsComputed() {
			t.Errorf("%s must not be Computed; Optional+Computed would prevent removal from clearing the API value", name)
		}
	}
}

func TestReadTeamIgnoresUnconfiguredServerBudgetDefaults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_info": map[string]interface{}{
					"team_id":          "team-defaults",
					"team_alias":       "defaults-team",
					"max_budget":       500.0,
					"budget_duration":  "30d",
					"access_group_ids": []interface{}{"externally-managed"},
					"team_member_budget_table": map[string]interface{}{
						"max_budget":      25.0,
						"budget_duration": "30d",
						"rpm_limit":       10.0,
						"tpm_limit":       1000.0,
					},
				},
			})
		case "/team/permissions_list":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_member_permissions": []string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := &TeamResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := TeamResourceModel{
		ID:                   types.StringValue("team-defaults"),
		TeamAlias:            types.StringValue("defaults-team"),
		MaxBudget:            types.Float64Null(),
		BudgetDuration:       types.StringNull(),
		AccessGroupIDs:       types.SetNull(types.StringType),
		TeamMemberBudget:     types.Float64Null(),
		MemberBudgetDuration: types.StringNull(),
		TeamMemberRPMLimit:   types.Int64Null(),
		TeamMemberTPMLimit:   types.Int64Null(),
	}

	if err := r.readTeam(context.Background(), &data); err != nil {
		t.Fatalf("readTeam returned error: %v", err)
	}
	if !data.MaxBudget.IsNull() {
		t.Errorf("unconfigured max_budget should remain null, got %v", data.MaxBudget)
	}
	if !data.BudgetDuration.IsNull() {
		t.Errorf("unconfigured budget_duration should remain null, got %v", data.BudgetDuration)
	}
	if !data.TeamMemberBudget.IsNull() || !data.MemberBudgetDuration.IsNull() || !data.TeamMemberRPMLimit.IsNull() || !data.TeamMemberTPMLimit.IsNull() {
		t.Errorf("unconfigured team member defaults should remain null: budget=%v duration=%v rpm=%v tpm=%v", data.TeamMemberBudget, data.MemberBudgetDuration, data.TeamMemberRPMLimit, data.TeamMemberTPMLimit)
	}
	expectedAccessGroups := types.SetValueMust(types.StringType, []attr.Value{types.StringValue("externally-managed")})
	if !data.AccessGroupIDs.Equal(expectedAccessGroups) {
		t.Errorf("access_group_ids = %v, want API value %v", data.AccessGroupIDs, expectedAccessGroups)
	}
}

func TestReadTeamWithNestedTeamInfoResponse(t *testing.T) {
	t.Parallel()

	// Test with nested "team_info" response matching actual LiteLLM API format
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_info": map[string]interface{}{
					"team_id":         "team-abc-123",
					"team_alias":      "production-team",
					"organization_id": "org-1",
					"max_budget":      500.0,
					"tpm_limit":       10000.0,
					"rpm_limit":       1000.0,
					"budget_duration": "monthly",
					"blocked":         false,
					"tpm_limit_type":  "team",
					"rpm_limit_type":  "team",
					"models":          []interface{}{"gpt-4", "claude-3"},
					"tags":            []interface{}{"prod", "high-priority"},
					"guardrails":      []interface{}{"content-filter"},
					"prompts":         []interface{}{},
					"metadata": map[string]interface{}{
						"env":             "production",
						"model_rpm_limit": map[string]interface{}{"gpt-4": 100.0},
						"model_tpm_limit": map[string]interface{}{"gpt-4": 5000.0},
					},
					"model_aliases": map[string]interface{}{"fast": "gpt-3.5-turbo"},
					"team_member_budget_table": map[string]interface{}{
						"max_budget":      50.0,
						"budget_duration": "30d",
					},
				},
			})
		case "/team/permissions_list":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_member_permissions": []interface{}{"team_member_add", "team_member_delete"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := &TeamResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := TeamResourceModel{
		ID:                    types.StringValue("team-abc-123"),
		TeamAlias:             types.StringValue("production-team"),
		MaxBudget:             types.Float64Value(500),
		BudgetDuration:        types.StringValue("monthly"),
		TeamMemberBudget:      types.Float64Value(50),
		MemberBudgetDuration:  types.StringValue("30d"),
		Models:                types.ListUnknown(types.StringType),
		Tags:                  types.ListUnknown(types.StringType),
		Guardrails:            types.ListUnknown(types.StringType),
		Prompts:               types.ListUnknown(types.StringType),
		Metadata:              types.MapUnknown(types.StringType),
		ModelAliases:          types.MapUnknown(types.StringType),
		ModelRPMLimit:         types.MapUnknown(types.Int64Type),
		ModelTPMLimit:         types.MapUnknown(types.Int64Type),
		TeamMemberPermissions: types.ListUnknown(types.StringType),
	}

	if err := r.readTeamWithNumericOwnership(context.Background(), &data, true); err != nil {
		t.Fatalf("readTeam returned error: %v", err)
	}

	// Verify scalar fields were extracted from nested "team_info" block
	if data.TeamAlias.ValueString() != "production-team" {
		t.Fatalf("expected team_alias 'production-team', got '%s'", data.TeamAlias.ValueString())
	}
	if data.OrganizationID.ValueString() != "org-1" {
		t.Fatalf("expected organization_id 'org-1', got '%s'", data.OrganizationID.ValueString())
	}
	if data.MaxBudget.ValueFloat64() != 500.0 {
		t.Fatalf("expected max_budget 500.0, got %f", data.MaxBudget.ValueFloat64())
	}
	if data.BudgetDuration.ValueString() != "monthly" {
		t.Fatalf("expected budget_duration 'monthly', got '%s'", data.BudgetDuration.ValueString())
	}
	if data.TPMLimitType.ValueString() != "team" {
		t.Fatalf("expected tpm_limit_type 'team', got '%s'", data.TPMLimitType.ValueString())
	}
	if data.RPMLimitType.ValueString() != "team" {
		t.Fatalf("expected rpm_limit_type 'team', got '%s'", data.RPMLimitType.ValueString())
	}
	if data.TeamMemberBudget.ValueFloat64() != 50.0 {
		t.Fatalf("expected team_member_budget 50.0, got %f", data.TeamMemberBudget.ValueFloat64())
	}
	if data.MemberBudgetDuration.ValueString() != "30d" {
		t.Fatalf("expected team_member_budget_duration 30d, got %q", data.MemberBudgetDuration.ValueString())
	}

	// Verify lists were populated from nested response
	if data.Models.IsUnknown() || data.Models.IsNull() {
		t.Fatal("models should be known and non-null after read with nested response")
	}
	if data.Tags.IsUnknown() || data.Tags.IsNull() {
		t.Fatal("tags should be known and non-null after read with nested response")
	}
	if data.Guardrails.IsUnknown() || data.Guardrails.IsNull() {
		t.Fatal("guardrails should be known and non-null after read with nested response")
	}

	// Verify maps were populated from nested response
	if data.Metadata.IsUnknown() || data.Metadata.IsNull() {
		t.Fatal("metadata should be known and non-null after read with nested response")
	}
	if data.ModelAliases.IsUnknown() || data.ModelAliases.IsNull() {
		t.Fatal("model_aliases should be known and non-null after read with nested response")
	}
	if data.ModelRPMLimit.IsUnknown() || data.ModelRPMLimit.IsNull() {
		t.Fatal("model_rpm_limit should be known and non-null after read with nested response")
	}
	if data.ModelTPMLimit.IsUnknown() || data.ModelTPMLimit.IsNull() {
		t.Fatal("model_tpm_limit should be known and non-null after read with nested response")
	}

	// Verify permissions were fetched and populated
	if data.TeamMemberPermissions.IsUnknown() || data.TeamMemberPermissions.IsNull() {
		t.Fatal("team_member_permissions should be known and non-null after read with nested response")
	}

	// Verify all Unknown fields are resolved (no more "known after apply")
	if data.Prompts.IsUnknown() {
		t.Fatal("prompts should be known after read")
	}
}

func TestBuildTeamRequest_RouterSettingsWithFallbacks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	fbModels, _ := types.ListValue(types.StringType, []attr.Value{
		types.StringValue("gpt-4"),
		types.StringValue("claude-3-haiku"),
	})

	entry, _ := types.ObjectValue(fallbackEntryAttrTypes, map[string]attr.Value{
		"model":           types.StringValue("gpt-3.5-turbo"),
		"fallback_models": fbModels,
	})

	fallbacksList, _ := types.ListValue(types.ObjectType{AttrTypes: fallbackEntryAttrTypes}, []attr.Value{entry})

	rs, _ := types.ObjectValue(routerSettingsAttrTypes, map[string]attr.Value{
		"fallbacks":                fallbacksList,
		"context_window_fallbacks": types.ListNull(types.ObjectType{AttrTypes: fallbackEntryAttrTypes}),
	})

	r := &TeamResource{}
	data := &TeamResourceModel{
		TeamAlias:      types.StringValue("test-team"),
		RouterSettings: rs,
	}

	req, err := r.buildTeamRequest(ctx, data, "team-123")
	if err != nil {
		t.Fatal(err)
	}

	rsPayload, ok := req["router_settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("router_settings missing or wrong type: %T", req["router_settings"])
	}

	fbs, ok := rsPayload["fallbacks"].([]map[string][]string)
	if !ok {
		t.Fatalf("fallbacks wrong type: %T", rsPayload["fallbacks"])
	}
	if len(fbs) != 1 {
		t.Fatalf("expected 1 fallback entry, got %d", len(fbs))
	}

	models, ok := fbs[0]["gpt-3.5-turbo"]
	if !ok {
		t.Fatal("expected fallback entry for gpt-3.5-turbo")
	}
	if len(models) != 2 || models[0] != "gpt-4" || models[1] != "claude-3-haiku" {
		t.Errorf("fallback_models = %v, want [gpt-4, claude-3-haiku]", models)
	}

	if _, exists := rsPayload["context_window_fallbacks"]; exists {
		t.Error("context_window_fallbacks should not be present when null")
	}
}

func TestBuildTeamRequest_NullRouterSettings_SendsEmptyToAPI(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := &TeamResource{}
	data := &TeamResourceModel{
		TeamAlias:      types.StringValue("test-team"),
		RouterSettings: types.ObjectNull(routerSettingsAttrTypes),
	}

	req, err := r.buildTeamRequest(ctx, data, "team-123")
	if err != nil {
		t.Fatal(err)
	}

	rs, exists := req["router_settings"]
	if !exists {
		t.Fatal("router_settings should be present (as empty object) to clear server-side fallbacks")
	}
	rsMap, ok := rs.(map[string]interface{})
	if !ok {
		t.Fatalf("router_settings should be map[string]interface{}, got %T", rs)
	}
	if len(rsMap) != 0 {
		t.Errorf("router_settings should be empty to clear fallbacks, got %v", rsMap)
	}
}

func TestReadTeam_RouterSettingsFromAPI(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_alias": "fallback-team",
				"blocked":    false,
				"router_settings": map[string]interface{}{
					"fallbacks": []interface{}{
						map[string]interface{}{
							"gpt-3.5-turbo": []interface{}{"gpt-4", "claude-3-haiku"},
						},
					},
					"context_window_fallbacks": []interface{}{
						map[string]interface{}{
							"gpt-3.5-turbo": []interface{}{"gpt-4-32k"},
						},
					},
				},
			})
		case "/team/permissions_list":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_member_permissions": []string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := &TeamResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	// RouterSettings must be non-null so readTeam populates it
	emptyRS, _ := types.ObjectValue(routerSettingsAttrTypes, map[string]attr.Value{
		"fallbacks":                types.ListNull(types.ObjectType{AttrTypes: fallbackEntryAttrTypes}),
		"context_window_fallbacks": types.ListNull(types.ObjectType{AttrTypes: fallbackEntryAttrTypes}),
	})

	data := TeamResourceModel{
		ID:             types.StringValue("team-456"),
		TeamAlias:      types.StringValue("fallback-team"),
		RouterSettings: emptyRS,
	}

	if err := r.readTeam(context.Background(), &data); err != nil {
		t.Fatalf("readTeam returned error: %v", err)
	}

	if data.RouterSettings.IsNull() {
		t.Fatal("router_settings should not be null after read")
	}

	var rs RouterSettingsModel
	data.RouterSettings.As(context.Background(), &rs, basetypes.ObjectAsOptions{})

	if rs.Fallbacks.IsNull() {
		t.Fatal("fallbacks should not be null")
	}

	var entries []FallbackEntryModel
	rs.Fallbacks.ElementsAs(context.Background(), &entries, false)

	if len(entries) != 1 {
		t.Fatalf("expected 1 fallback entry, got %d", len(entries))
	}
	if entries[0].Model.ValueString() != "gpt-3.5-turbo" {
		t.Errorf("model = %s, want gpt-3.5-turbo", entries[0].Model.ValueString())
	}

	var fbModels []string
	entries[0].FallbackModels.ElementsAs(context.Background(), &fbModels, false)
	if len(fbModels) != 2 || fbModels[0] != "gpt-4" || fbModels[1] != "claude-3-haiku" {
		t.Errorf("fallback_models = %v, want [gpt-4 claude-3-haiku]", fbModels)
	}

	// Verify context_window_fallbacks
	if rs.ContextWindowFallbacks.IsNull() {
		t.Fatal("context_window_fallbacks should not be null")
	}

	var cwEntries []FallbackEntryModel
	rs.ContextWindowFallbacks.ElementsAs(context.Background(), &cwEntries, false)

	if len(cwEntries) != 1 {
		t.Fatalf("expected 1 context_window_fallback entry, got %d", len(cwEntries))
	}
	if cwEntries[0].Model.ValueString() != "gpt-3.5-turbo" {
		t.Errorf("model = %s, want gpt-3.5-turbo", cwEntries[0].Model.ValueString())
	}

	var cwModels []string
	cwEntries[0].FallbackModels.ElementsAs(context.Background(), &cwModels, false)
	if len(cwModels) != 1 || cwModels[0] != "gpt-4-32k" {
		t.Errorf("context_window fallback_models = %v, want [gpt-4-32k]", cwModels)
	}
}

func TestReadTeam_NullRouterSettingsWhenAPIHasNone(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_alias": "no-fallback-team",
				"blocked":    false,
			})
		case "/team/permissions_list":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_member_permissions": []string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := &TeamResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := TeamResourceModel{
		ID:             types.StringValue("team-789"),
		TeamAlias:      types.StringValue("no-fallback-team"),
		RouterSettings: types.ObjectNull(routerSettingsAttrTypes),
	}

	if err := r.readTeam(context.Background(), &data); err != nil {
		t.Fatalf("readTeam returned error: %v", err)
	}

	if !data.RouterSettings.IsNull() {
		t.Fatal("router_settings should be null when API has no router_settings")
	}
}

func TestReadTeam_DetectsDriftWhenAPIStillHasFallbacks(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_alias": "stale-fallback-team",
				"blocked":    false,
				"router_settings": map[string]interface{}{
					"fallbacks": []interface{}{
						map[string]interface{}{
							"gpt-3.5-turbo": []interface{}{"gpt-4"},
						},
					},
				},
			})
		case "/team/permissions_list":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_member_permissions": []string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := &TeamResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	// Simulate: user removed router_settings from config (state is null),
	// but the API still has fallbacks from a previous apply.
	data := TeamResourceModel{
		ID:             types.StringValue("team-drift"),
		TeamAlias:      types.StringValue("stale-fallback-team"),
		RouterSettings: types.ObjectNull(routerSettingsAttrTypes),
	}

	if err := r.readTeam(context.Background(), &data); err != nil {
		t.Fatalf("readTeam returned error: %v", err)
	}

	// readTeam should now report the API's actual state (non-null),
	// so Terraform detects the drift and plans to clear it.
	if data.RouterSettings.IsNull() {
		t.Fatal("router_settings should NOT be null -- API still has fallbacks, Terraform must detect drift")
	}
}

// TestApplyTeamNullableClears_AllTransitionsEmitNull verifies that when each
// nullable field transitions from set in state → null in plan, the resulting
// map carries explicit nil and json.Marshal serializes it as JSON null.
// This guards against regressions where omitting the field instead of sending
// null would let the LiteLLM API (Pydantic exclude_unset=True) keep stale values.
func TestApplyTeamNullableClears_AllTransitionsEmitNull(t *testing.T) {
	t.Parallel()

	state := &TeamResourceModel{
		MaxBudget:            types.Float64Value(100),
		BudgetDuration:       types.StringValue("30d"),
		TPMLimit:             types.Int64Value(1000),
		RPMLimit:             types.Int64Value(60),
		TeamMemberBudget:     types.Float64Value(50),
		MemberBudgetDuration: types.StringValue("30d"),
		TeamMemberRPMLimit:   types.Int64Value(10),
		TeamMemberTPMLimit:   types.Int64Value(500),
	}
	plan := &TeamResourceModel{
		MaxBudget:            types.Float64Null(),
		BudgetDuration:       types.StringNull(),
		TPMLimit:             types.Int64Null(),
		RPMLimit:             types.Int64Null(),
		TeamMemberBudget:     types.Float64Null(),
		MemberBudgetDuration: types.StringNull(),
		TeamMemberRPMLimit:   types.Int64Null(),
		TeamMemberTPMLimit:   types.Int64Null(),
	}

	teamReq := map[string]interface{}{"team_id": "team-123"}
	applyTeamNullableClears(teamReq, state, plan)

	expectedNullKeys := []string{
		"max_budget", "budget_duration", "tpm_limit", "rpm_limit",
		"team_member_budget", "team_member_budget_duration", "team_member_rpm_limit", "team_member_tpm_limit",
	}
	for _, k := range expectedNullKeys {
		v, ok := teamReq[k]
		if !ok {
			t.Errorf("teamReq missing key %q after clear; expected explicit nil", k)
			continue
		}
		if v != nil {
			t.Errorf("teamReq[%q] = %v, want nil", k, v)
		}
	}

	body, err := json.Marshal(teamReq)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	bodyStr := string(body)
	for _, k := range expectedNullKeys {
		needle := `"` + k + `":null`
		if !strings.Contains(bodyStr, needle) {
			t.Errorf("request body missing %s; got %s", needle, bodyStr)
		}
	}

	clearReq := extractTeamMemberBudgetClears(teamReq, "team-123")
	if clearReq == nil || clearReq["team_id"] != "team-123" {
		t.Fatalf("team member budget clear request = %#v", clearReq)
	}
	for _, key := range []string{
		"team_member_budget",
		"team_member_budget_duration",
		"team_member_rpm_limit",
		"team_member_tpm_limit",
	} {
		if value, exists := clearReq[key]; !exists || value != nil {
			t.Errorf("clearReq[%q] = %#v, want explicit nil", key, value)
		}
		if _, exists := teamReq[key]; exists {
			t.Errorf("main team request retained extracted clear %q", key)
		}
	}
}

// TestApplyTeamNullableClears_NoTransition_NoOp verifies the helper does not
// inject keys when state and plan agree (both null, or both set) — only the
// non-null → null transition triggers explicit clears.
func TestApplyTeamNullableClears_NoTransition_NoOp(t *testing.T) {
	t.Parallel()

	// Both null (field never set): helper must not introduce keys.
	state := &TeamResourceModel{
		MaxBudget:      types.Float64Null(),
		BudgetDuration: types.StringNull(),
		TPMLimit:       types.Int64Null(),
		RPMLimit:       types.Int64Null(),
	}
	plan := &TeamResourceModel{
		MaxBudget:      types.Float64Null(),
		BudgetDuration: types.StringNull(),
		TPMLimit:       types.Int64Null(),
		RPMLimit:       types.Int64Null(),
	}

	teamReq := map[string]interface{}{}
	applyTeamNullableClears(teamReq, state, plan)

	if len(teamReq) != 0 {
		t.Errorf("teamReq should be empty when no transitions; got %v", teamReq)
	}

	// Both set (stable value): helper must not overwrite to nil.
	state = &TeamResourceModel{MaxBudget: types.Float64Value(100)}
	plan = &TeamResourceModel{MaxBudget: types.Float64Value(200)}

	teamReq = map[string]interface{}{"max_budget": float64(200)}
	applyTeamNullableClears(teamReq, state, plan)

	if v := teamReq["max_budget"]; v != float64(200) {
		t.Errorf("helper overwrote stable max_budget; got %v, want 200", v)
	}
	if clearReq := extractTeamMemberBudgetClears(teamReq, "team-123"); clearReq != nil {
		t.Errorf("unexpected team member budget clear request: %#v", clearReq)
	}
}
