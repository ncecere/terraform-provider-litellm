package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTeamMemberBudgetDurationSchema(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&TeamMemberResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	attribute, ok := response.Schema.Attributes["budget_duration"].(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("budget_duration schema type = %T", response.Schema.Attributes["budget_duration"])
	}
	if !attribute.Optional || attribute.Computed || attribute.Required {
		t.Fatalf("budget_duration must be Optional-only: %#v", attribute)
	}
	if len(attribute.Validators) != 1 {
		t.Fatalf("budget_duration validators = %d, want duration format validator", len(attribute.Validators))
	}
}

func TestTeamMemberMutationRequestsUseNativeBudgetDuration(t *testing.T) {
	t.Parallel()

	data := &TeamMemberResourceModel{
		TeamID:          types.StringValue("team-1"),
		UserID:          types.StringValue("user-1"),
		UserEmail:       types.StringValue("user@example.com"),
		Role:            types.StringValue("user"),
		MaxBudgetInTeam: types.Float64Value(50),
		BudgetDuration:  types.StringValue("30d"),
	}
	for name, request := range map[string]map[string]interface{}{
		"member_add":    buildTeamMemberAddRequest(data),
		"member_update": buildTeamMemberUpdateRequest(data),
	} {
		if got := request["budget_duration"]; got != "30d" {
			t.Errorf("%s budget_duration = %#v, want 30d", name, got)
		}
		if got := request["max_budget_in_team"]; got != float64(50) {
			t.Errorf("%s max_budget_in_team = %#v, want 50", name, got)
		}
	}
	if got := buildTeamMemberUpdateRequest(data)["role"]; got != "user" {
		t.Errorf("member_update role = %#v, want user", got)
	}
}

// TestApplyTeamMemberNullableClears_TransitionToNull verifies that clearing
// managed nullable fields results in explicit JSON null on the wire — required
// because the LiteLLM API ignores omitted fields under Pydantic
// exclude_unset=True.
func TestReadTeamMemberUsesMembershipBudgetAndEscapedTeamID(t *testing.T) {
	t.Parallel()

	const teamID = "team/group #1&ops"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/team/info" || request.URL.Query().Get("team_id") != teamID {
			http.Error(writer, "unexpected team query", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"team_info": map[string]interface{}{
				"members_with_roles": []interface{}{
					map[string]interface{}{
						"user_id":    "user-1",
						"user_email": "observed@example.com",
						"role":       "admin",
					},
				},
			},
			"team_memberships": []interface{}{
				map[string]interface{}{
					"user_id": "user-1",
					"litellm_budget_table": map[string]interface{}{
						"max_budget":      75.0,
						"budget_duration": "7d",
					},
				},
			},
		})
	}))
	defer server.Close()

	teamMemberResource := &TeamMemberResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := TeamMemberResourceModel{
		TeamID:          types.StringValue(teamID),
		UserID:          types.StringValue("user-1"),
		UserEmail:       types.StringValue("configured@example.com"),
		Role:            types.StringValue("user"),
		MaxBudgetInTeam: types.Float64Value(50),
		BudgetDuration:  types.StringValue("30d"),
	}
	exists, err := teamMemberResource.readTeamMember(context.Background(), &data)
	if err != nil || !exists {
		t.Fatalf("readTeamMember() exists=%v err=%v", exists, err)
	}
	if data.UserEmail.ValueString() != "observed@example.com" || data.Role.ValueString() != "admin" {
		t.Fatalf("member identity read-back email=%q role=%q", data.UserEmail.ValueString(), data.Role.ValueString())
	}
	if data.MaxBudgetInTeam.ValueFloat64() != 75 || data.BudgetDuration.ValueString() != "7d" {
		t.Fatalf("member budget read-back max=%v duration=%q", data.MaxBudgetInTeam.ValueFloat64(), data.BudgetDuration.ValueString())
	}
}

func TestReadTeamMemberPreservesUnmanagedBudgetFields(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"team_info": map[string]interface{}{
				"members_with_roles": []interface{}{
					map[string]interface{}{"user_id": "user-1", "role": "user"},
				},
			},
			"team_memberships": []interface{}{
				map[string]interface{}{
					"user_id": "user-1",
					"litellm_budget_table": map[string]interface{}{
						"max_budget":      75.0,
						"budget_duration": "7d",
					},
				},
			},
		})
	}))
	defer server.Close()

	teamMemberResource := &TeamMemberResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := TeamMemberResourceModel{
		TeamID:          types.StringValue("team-1"),
		UserID:          types.StringValue("user-1"),
		MaxBudgetInTeam: types.Float64Null(),
		BudgetDuration:  types.StringNull(),
	}
	exists, err := teamMemberResource.readTeamMember(context.Background(), &data)
	if err != nil || !exists {
		t.Fatalf("readTeamMember() exists=%v err=%v", exists, err)
	}
	if !data.MaxBudgetInTeam.IsNull() || !data.BudgetDuration.IsNull() {
		t.Fatalf("unmanaged member budget was adopted: max=%v duration=%v", data.MaxBudgetInTeam, data.BudgetDuration)
	}
}

func TestReadTeamMemberRejectsOrphanBudgetMembership(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"team_info": map[string]interface{}{
				"members_with_roles": []interface{}{},
			},
			"team_memberships": []interface{}{
				map[string]interface{}{
					"user_id": "user-1",
					"litellm_budget_table": map[string]interface{}{
						"max_budget":      75.0,
						"budget_duration": "7d",
					},
				},
			},
		})
	}))
	defer server.Close()

	teamMemberResource := &TeamMemberResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := TeamMemberResourceModel{TeamID: types.StringValue("team-1"), UserID: types.StringValue("user-1")}
	exists, err := teamMemberResource.readTeamMember(context.Background(), &data)
	if err != nil {
		t.Fatalf("readTeamMember() error = %v", err)
	}
	if exists {
		t.Fatal("budget membership without members_with_roles roster entry must not count as a team member")
	}
}

func TestApplyTeamMemberNullableClears_TransitionToNull(t *testing.T) {
	t.Parallel()

	state := &TeamMemberResourceModel{
		MaxBudgetInTeam: types.Float64Value(50),
		BudgetDuration:  types.StringValue("30d"),
	}
	plan := &TeamMemberResourceModel{
		MaxBudgetInTeam: types.Float64Null(),
		BudgetDuration:  types.StringNull(),
	}

	updateReq := map[string]interface{}{"team_id": "team-1", "user_id": "user-1"}
	applyTeamMemberNullableClears(updateReq, state, plan)

	v, ok := updateReq["max_budget_in_team"]
	if !ok {
		t.Fatal("updateReq missing max_budget_in_team after clear; expected explicit nil")
	}
	if v != nil {
		t.Errorf("updateReq[max_budget_in_team] = %v, want nil", v)
	}
	if v, ok := updateReq["budget_duration"]; !ok || v != nil {
		t.Errorf("updateReq[budget_duration] = %#v, want explicit nil", v)
	}

	body, err := json.Marshal(updateReq)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if !strings.Contains(string(body), `"max_budget_in_team":null`) {
		t.Errorf("request body missing \"max_budget_in_team\":null; got %s", string(body))
	}
	if !strings.Contains(string(body), `"budget_duration":null`) {
		t.Errorf("request body missing \"budget_duration\":null; got %s", string(body))
	}
}

// TestApplyTeamMemberNullableClears_NoTransition_NoOp verifies the helper does
// not inject the key when state and plan agree.
func TestApplyTeamMemberNullableClears_NoTransition_NoOp(t *testing.T) {
	t.Parallel()

	// Both null: no key injected.
	state := &TeamMemberResourceModel{
		MaxBudgetInTeam: types.Float64Null(),
		BudgetDuration:  types.StringNull(),
	}
	plan := &TeamMemberResourceModel{
		MaxBudgetInTeam: types.Float64Null(),
		BudgetDuration:  types.StringNull(),
	}

	updateReq := map[string]interface{}{}
	applyTeamMemberNullableClears(updateReq, state, plan)

	if _, ok := updateReq["max_budget_in_team"]; ok {
		t.Errorf("helper added max_budget_in_team when no transition; got %v", updateReq)
	}
	if _, ok := updateReq["budget_duration"]; ok {
		t.Errorf("helper added budget_duration when no transition; got %v", updateReq)
	}

	// Both set (stable value): existing values are preserved.
	state = &TeamMemberResourceModel{
		MaxBudgetInTeam: types.Float64Value(50),
		BudgetDuration:  types.StringValue("30d"),
	}
	plan = &TeamMemberResourceModel{
		MaxBudgetInTeam: types.Float64Value(75),
		BudgetDuration:  types.StringValue("7d"),
	}

	updateReq = map[string]interface{}{
		"max_budget_in_team": float64(75),
		"budget_duration":    "7d",
	}
	applyTeamMemberNullableClears(updateReq, state, plan)

	if v := updateReq["max_budget_in_team"]; v != float64(75) {
		t.Errorf("helper overwrote stable max_budget_in_team; got %v, want 75", v)
	}
	if v := updateReq["budget_duration"]; v != "7d" {
		t.Errorf("helper overwrote stable budget_duration; got %v, want 7d", v)
	}
}
