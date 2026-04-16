package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

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

func TestReadTeamWithNestedTeamInfoResponse(t *testing.T) {
	t.Parallel()

	// Test with nested "team_info" response matching actual LiteLLM API format
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_info": map[string]interface{}{
					"team_id":            "team-abc-123",
					"team_alias":         "production-team",
					"organization_id":    "org-1",
					"max_budget":         500.0,
					"tpm_limit":          10000.0,
					"rpm_limit":          1000.0,
					"budget_duration":    "monthly",
					"blocked":            false,
					"tpm_limit_type":     "team",
					"rpm_limit_type":     "team",
					"models":             []interface{}{"gpt-4", "claude-3"},
					"tags":               []interface{}{"prod", "high-priority"},
					"guardrails":         []interface{}{"content-filter"},
					"prompts":            []interface{}{},
					"metadata":           map[string]interface{}{"env": "production"},
					"model_aliases":      map[string]interface{}{"fast": "gpt-3.5-turbo"},
					"model_rpm_limit":    map[string]interface{}{"gpt-4": 100.0},
					"model_tpm_limit":    map[string]interface{}{"gpt-4": 5000.0},
					"team_member_budget": 50.0,
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

	req := r.buildTeamRequest(ctx, data, "team-123")

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

	req := r.buildTeamRequest(ctx, data, "team-123")

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

func TestBuildTeamRequest_SoftBudgetAndTeamMemberBudgetDuration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	r := &TeamResource{}
	data := &TeamResourceModel{
		TeamAlias:                types.StringValue("test-team"),
		SoftBudget:               types.Float64Value(100.0),
		TeamMemberBudgetDuration: types.StringValue("30d"),
		RouterSettings:           types.ObjectNull(routerSettingsAttrTypes),
	}

	req := r.buildTeamRequest(ctx, data, "team-123")

	if sb, ok := req["soft_budget"].(float64); !ok || sb != 100.0 {
		t.Errorf("expected soft_budget 100.0, got %v", req["soft_budget"])
	}
	if tmbd, ok := req["team_member_budget_duration"].(string); !ok || tmbd != "30d" {
		t.Errorf("expected team_member_budget_duration '30d', got %v", req["team_member_budget_duration"])
	}
}

func TestBuildTeamRequest_SoftBudgetAlertingEmailsInjectedIntoMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	emails, _ := types.ListValue(types.StringType, []attr.Value{
		types.StringValue("admin@example.com"),
		types.StringValue("ops@example.com"),
	})

	r := &TeamResource{}
	data := &TeamResourceModel{
		TeamAlias:                types.StringValue("test-team"),
		SoftBudgetAlertingEmails: emails,
		RouterSettings:           types.ObjectNull(routerSettingsAttrTypes),
	}

	req := r.buildTeamRequest(ctx, data, "team-123")

	md, ok := req["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected metadata map, got %T", req["metadata"])
	}

	alertEmails, ok := md["soft_budget_alerting_emails"].([]string)
	if !ok {
		t.Fatalf("expected soft_budget_alerting_emails []string in metadata, got %T", md["soft_budget_alerting_emails"])
	}
	if len(alertEmails) != 2 || alertEmails[0] != "admin@example.com" || alertEmails[1] != "ops@example.com" {
		t.Errorf("soft_budget_alerting_emails = %v, want [admin@example.com ops@example.com]", alertEmails)
	}
}

func TestBuildTeamRequest_SoftBudgetAlertingEmailsWithExistingMetadata(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	emails, _ := types.ListValue(types.StringType, []attr.Value{
		types.StringValue("admin@example.com"),
	})

	meta, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"environment": types.StringValue("production"),
	})

	r := &TeamResource{}
	data := &TeamResourceModel{
		TeamAlias:                types.StringValue("test-team"),
		Metadata:                 meta,
		SoftBudgetAlertingEmails: emails,
		RouterSettings:           types.ObjectNull(routerSettingsAttrTypes),
	}

	req := r.buildTeamRequest(ctx, data, "team-123")

	md, ok := req["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected metadata map, got %T", req["metadata"])
	}

	// Verify user metadata is preserved
	if env, ok := md["environment"].(string); !ok || env != "production" {
		t.Errorf("expected environment 'production' in metadata, got %v", md["environment"])
	}

	// Verify emails were injected
	alertEmails, ok := md["soft_budget_alerting_emails"].([]string)
	if !ok {
		t.Fatalf("expected soft_budget_alerting_emails in metadata, got %T", md["soft_budget_alerting_emails"])
	}
	if len(alertEmails) != 1 || alertEmails[0] != "admin@example.com" {
		t.Errorf("soft_budget_alerting_emails = %v, want [admin@example.com]", alertEmails)
	}
}

func TestReadTeam_SoftBudgetAlertingEmailsExtractedFromMetadata(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_alias":  "budget-team",
				"soft_budget": 100.0,
				"team_member_budget_duration": "30d",
				"blocked":     false,
				"metadata": map[string]interface{}{
					"soft_budget_alerting_emails": []interface{}{"user@example.com", "admin@example.com"},
					"custom_key":                  "custom_value",
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

	// Simulate user configured metadata with "custom_key" and soft_budget_alerting_emails
	meta, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"custom_key": types.StringValue("custom_value"),
	})

	emails, _ := types.ListValue(types.StringType, []attr.Value{
		types.StringValue("user@example.com"),
		types.StringValue("admin@example.com"),
	})

	data := TeamResourceModel{
		ID:                       types.StringValue("team-budget-123"),
		TeamAlias:                types.StringValue("budget-team"),
		Metadata:                 meta,
		SoftBudgetAlertingEmails: emails,
	}

	if err := r.readTeam(context.Background(), &data); err != nil {
		t.Fatalf("readTeam returned error: %v", err)
	}

	// Verify soft_budget was read
	if data.SoftBudget.ValueFloat64() != 100.0 {
		t.Errorf("expected soft_budget 100.0, got %f", data.SoftBudget.ValueFloat64())
	}

	// Verify team_member_budget_duration was read
	if data.TeamMemberBudgetDuration.ValueString() != "30d" {
		t.Errorf("expected team_member_budget_duration '30d', got '%s'", data.TeamMemberBudgetDuration.ValueString())
	}

	// Verify soft_budget_alerting_emails was extracted from metadata
	if data.SoftBudgetAlertingEmails.IsNull() || data.SoftBudgetAlertingEmails.IsUnknown() {
		t.Fatal("soft_budget_alerting_emails should be known and non-null")
	}
	var readEmails []string
	data.SoftBudgetAlertingEmails.ElementsAs(context.Background(), &readEmails, false)
	if len(readEmails) != 2 || readEmails[0] != "user@example.com" || readEmails[1] != "admin@example.com" {
		t.Errorf("soft_budget_alerting_emails = %v, want [user@example.com admin@example.com]", readEmails)
	}

	// Verify soft_budget_alerting_emails was removed from metadata
	if data.Metadata.IsNull() || data.Metadata.IsUnknown() {
		t.Fatal("metadata should be known and non-null")
	}
	var readMeta map[string]string
	data.Metadata.ElementsAs(context.Background(), &readMeta, false)
	if _, exists := readMeta["soft_budget_alerting_emails"]; exists {
		t.Error("soft_budget_alerting_emails should NOT appear in metadata")
	}
	if readMeta["custom_key"] != "custom_value" {
		t.Errorf("expected custom_key 'custom_value' in metadata, got '%s'", readMeta["custom_key"])
	}
}

func TestReadTeam_SoftBudgetAlertingEmailsPreservesNullWhenNotConfigured(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_alias": "simple-team",
				"blocked":    false,
				"metadata":   map[string]interface{}{},
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
		ID:                       types.StringValue("team-simple"),
		TeamAlias:                types.StringValue("simple-team"),
		SoftBudgetAlertingEmails: types.ListNull(types.StringType),
	}

	if err := r.readTeam(context.Background(), &data); err != nil {
		t.Fatalf("readTeam returned error: %v", err)
	}

	// When user didn't configure soft_budget_alerting_emails and API doesn't return it,
	// it should stay null (no spurious drift)
	if !data.SoftBudgetAlertingEmails.IsNull() {
		t.Fatal("soft_budget_alerting_emails should remain null when not configured and not returned by API")
	}
}

func TestReadTeam_UnknownSoftBudgetAlertingEmailsResolvesAfterRead(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_alias": "unknown-team",
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
		ID:                       types.StringValue("team-unknown"),
		TeamAlias:                types.StringValue("unknown-team"),
		SoftBudgetAlertingEmails: types.ListUnknown(types.StringType),
	}

	if err := r.readTeam(context.Background(), &data); err != nil {
		t.Fatalf("readTeam returned error: %v", err)
	}

	if data.SoftBudgetAlertingEmails.IsUnknown() {
		t.Fatal("soft_budget_alerting_emails should be known after read (not 'known after apply')")
	}
}
