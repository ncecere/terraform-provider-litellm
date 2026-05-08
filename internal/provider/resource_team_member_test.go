package provider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestApplyTeamMemberNullableClears_TransitionToNull verifies that clearing
// max_budget_in_team in plan (was set in state) results in explicit JSON null
// on the wire — required because the LiteLLM API ignores omitted fields under
// Pydantic exclude_unset=True.
func TestApplyTeamMemberNullableClears_TransitionToNull(t *testing.T) {
	t.Parallel()

	state := &TeamMemberResourceModel{
		MaxBudgetInTeam: types.Float64Value(50),
	}
	plan := &TeamMemberResourceModel{
		MaxBudgetInTeam: types.Float64Null(),
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

	body, err := json.Marshal(updateReq)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if !strings.Contains(string(body), `"max_budget_in_team":null`) {
		t.Errorf("request body missing \"max_budget_in_team\":null; got %s", string(body))
	}
}

// TestApplyTeamMemberNullableClears_NoTransition_NoOp verifies the helper does
// not inject the key when state and plan agree.
func TestApplyTeamMemberNullableClears_NoTransition_NoOp(t *testing.T) {
	t.Parallel()

	// Both null: no key injected.
	state := &TeamMemberResourceModel{MaxBudgetInTeam: types.Float64Null()}
	plan := &TeamMemberResourceModel{MaxBudgetInTeam: types.Float64Null()}

	updateReq := map[string]interface{}{}
	applyTeamMemberNullableClears(updateReq, state, plan)

	if _, ok := updateReq["max_budget_in_team"]; ok {
		t.Errorf("helper added max_budget_in_team when no transition; got %v", updateReq)
	}

	// Both set (stable value): existing value preserved.
	state = &TeamMemberResourceModel{MaxBudgetInTeam: types.Float64Value(50)}
	plan = &TeamMemberResourceModel{MaxBudgetInTeam: types.Float64Value(75)}

	updateReq = map[string]interface{}{"max_budget_in_team": float64(75)}
	applyTeamMemberNullableClears(updateReq, state, plan)

	if v := updateReq["max_budget_in_team"]; v != float64(75) {
		t.Errorf("helper overwrote stable max_budget_in_team; got %v, want 75", v)
	}
}
