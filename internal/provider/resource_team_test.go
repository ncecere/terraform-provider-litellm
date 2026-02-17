package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
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
		TeamID:                types.StringUnknown(),
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
		TeamID:                types.StringUnknown(),
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

func TestReadTeamPopulatesTeamID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_info": map[string]interface{}{
					"team_id":    "team-abc-123",
					"team_alias": "my-team",
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
		ID:                    types.StringValue("team-abc-123"),
		TeamID:                types.StringUnknown(),
		TeamAlias:             types.StringValue("my-team"),
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

	if data.TeamID.IsUnknown() {
		t.Fatal("team_id should be known after read")
	}
	if data.TeamID.IsNull() {
		t.Fatal("team_id should not be null after read")
	}
	if data.TeamID.ValueString() != "team-abc-123" {
		t.Fatalf("expected team_id 'team-abc-123', got '%s'", data.TeamID.ValueString())
	}
	if data.ID.ValueString() != data.TeamID.ValueString() {
		t.Fatalf("id and team_id should be equal: id=%s, team_id=%s", data.ID.ValueString(), data.TeamID.ValueString())
	}
}

func TestReadTeamTeamIDNotUpdatedWhenAbsentInResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			// No team_id in response — team_id in data should remain unchanged
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_info": map[string]interface{}{
					"team_alias": "my-team",
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
		ID:                    types.StringValue("team-existing-999"),
		TeamID:                types.StringValue("team-existing-999"),
		TeamAlias:             types.StringValue("my-team"),
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

	// When API omits team_id, data.TeamID should remain at its prior value (not cleared)
	if data.TeamID.IsNull() {
		t.Fatal("team_id should not be null when API omits it — prior value should be preserved")
	}
	if data.TeamID.ValueString() != "team-existing-999" {
		t.Fatalf("expected team_id to remain 'team-existing-999', got '%s'", data.TeamID.ValueString())
	}
}

func TestReadTeamResolvesTeamIDUnknown(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case "/team/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"team_alias": "agent-team",
				"team_id":    "team-resolved-456",
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
		ID:                    types.StringValue("team-resolved-456"),
		TeamID:                types.StringUnknown(),
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

	if data.TeamID.IsUnknown() {
		t.Fatal("team_id should be known after read")
	}
	if data.TeamID.ValueString() != "team-resolved-456" {
		t.Fatalf("expected team_id 'team-resolved-456', got '%s'", data.TeamID.ValueString())
	}
}
