package provider

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestApplyTeamMemberNullableClears_TransitionToNull verifies that clearing
// max_budget_in_team in plan (was set in state) results in explicit JSON null
// on the wire — required because the LiteLLM API ignores omitted fields under
// Pydantic exclude_unset=True.
func TestIsTeamMemberAlreadyInTeamError(t *testing.T) {
	t.Parallel()

	alreadyErr := errors.New(`API request failed with status 400: {"type":"team_member_already_in_team","message":"User is already in team"}`)
	if !isTeamMemberAlreadyInTeamError(alreadyErr) {
		t.Fatal("expected team_member_already_in_team status 400 error to be idempotent")
	}

	wrongStatus := errors.New(`API request failed with status 500: {"type":"team_member_already_in_team"}`)
	if isTeamMemberAlreadyInTeamError(wrongStatus) {
		t.Fatal("status 500 should not be treated as idempotent already-in-team")
	}

	wrongType := errors.New(`API request failed with status 400: {"type":"other_error"}`)
	if isTeamMemberAlreadyInTeamError(wrongType) {
		t.Fatal("other status 400 errors should not be treated as idempotent already-in-team")
	}
}

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

// TestFindMembershipBudgetID covers resolving a member's budget object id from a
// /team/info response, used to apply budget_duration via /budget/update.
func TestFindMembershipBudgetID(t *testing.T) {
	t.Parallel()

	// Nested litellm_budget_table.budget_id is preferred.
	teamInfo := map[string]interface{}{
		"team_memberships": []interface{}{
			map[string]interface{}{
				"user_id": "alice",
				"litellm_budget_table": map[string]interface{}{
					"budget_id":       "budget-alice",
					"budget_duration": nil,
				},
			},
			map[string]interface{}{
				"user_id":   "bob",
				"budget_id": "budget-bob",
			},
		},
	}

	if id, ok := findMembershipBudgetID(teamInfo, "alice"); !ok || id != "budget-alice" {
		t.Errorf("alice: got (%q, %v), want (\"budget-alice\", true)", id, ok)
	}
	// Flat budget_id fallback when no nested table.
	if id, ok := findMembershipBudgetID(teamInfo, "bob"); !ok || id != "budget-bob" {
		t.Errorf("bob: got (%q, %v), want (\"budget-bob\", true)", id, ok)
	}
	// Unknown user → not found.
	if id, ok := findMembershipBudgetID(teamInfo, "carol"); ok || id != "" {
		t.Errorf("carol: got (%q, %v), want (\"\", false)", id, ok)
	}
	// Missing team_memberships → not found, no panic.
	if id, ok := findMembershipBudgetID(map[string]interface{}{}, "alice"); ok || id != "" {
		t.Errorf("empty teamInfo: got (%q, %v), want (\"\", false)", id, ok)
	}
}
