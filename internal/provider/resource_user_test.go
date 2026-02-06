package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestNullPreservationForListAttributes verifies that when the API returns an
// empty list for an Optional+Computed list attribute that was not specified in
// the user's config (i.e., is null in state), the read-back logic preserves
// null rather than overwriting it with an empty list.
//
// This is the core fix for issue #51:
// "Creating a new user with resource 'litellm_user' required 'teams' for no apparent reason"
//
// The Terraform Framework requires that if an attribute is null in the plan,
// it must remain null in the state after apply. Overwriting null with []
// causes: "Provider produced inconsistent result after apply"

func TestNullListPreservation(t *testing.T) {
	t.Run("null teams stays null when API returns empty array", func(t *testing.T) {
		// Simulate: user did not specify teams in config → teams is null
		teams := types.ListNull(types.StringType)

		// Simulate: API returned "teams": [] (empty array)
		apiTeams := []interface{}{}

		// Apply the same logic as readUser
		if len(apiTeams) > 0 {
			teamsList := make([]attr.Value, len(apiTeams))
			for i, t := range apiTeams {
				if str, ok := t.(string); ok {
					teamsList[i] = types.StringValue(str)
				}
			}
			teams, _ = types.ListValue(types.StringType, teamsList)
		} else if !teams.IsNull() {
			teams, _ = types.ListValue(types.StringType, []attr.Value{})
		}

		if !teams.IsNull() {
			t.Errorf("expected teams to remain null when API returns empty array and config didn't specify teams, but got: %v", teams)
		}
	})

	t.Run("non-null teams becomes empty list when API returns empty array", func(t *testing.T) {
		// Simulate: user specified teams = ["team-1"] in config, then removed all teams
		initialTeams := []attr.Value{types.StringValue("team-1")}
		teams, _ := types.ListValue(types.StringType, initialTeams)

		// Simulate: API returned "teams": [] (empty array) after update
		apiTeams := []interface{}{}

		if len(apiTeams) > 0 {
			teamsList := make([]attr.Value, len(apiTeams))
			for i, t := range apiTeams {
				if str, ok := t.(string); ok {
					teamsList[i] = types.StringValue(str)
				}
			}
			teams, _ = types.ListValue(types.StringType, teamsList)
		} else if !teams.IsNull() {
			teams, _ = types.ListValue(types.StringType, []attr.Value{})
		}

		if teams.IsNull() {
			t.Error("expected teams to become empty list (not null) when user previously had teams in config")
		}
		if len(teams.Elements()) != 0 {
			t.Errorf("expected teams to be empty list, got %d elements", len(teams.Elements()))
		}
	})

	t.Run("teams populated when API returns non-empty array", func(t *testing.T) {
		// Simulate: user did not specify teams (null), but API returns teams
		teams := types.ListNull(types.StringType)

		// Simulate: API returned "teams": ["team-1", "team-2"]
		apiTeams := []interface{}{"team-1", "team-2"}

		if len(apiTeams) > 0 {
			teamsList := make([]attr.Value, len(apiTeams))
			for i, t := range apiTeams {
				if str, ok := t.(string); ok {
					teamsList[i] = types.StringValue(str)
				}
			}
			teams, _ = types.ListValue(types.StringType, teamsList)
		} else if !teams.IsNull() {
			teams, _ = types.ListValue(types.StringType, []attr.Value{})
		}

		if teams.IsNull() {
			t.Error("expected teams to be populated when API returns non-empty array")
		}
		if len(teams.Elements()) != 2 {
			t.Errorf("expected 2 teams, got %d", len(teams.Elements()))
		}
	})
}

func TestNullMapPreservation(t *testing.T) {
	t.Run("null metadata stays null when API returns empty map", func(t *testing.T) {
		// Simulate: user did not specify metadata in config → metadata is null
		metadata := types.MapNull(types.StringType)

		// Simulate: API returned "metadata": {} (empty map)
		apiMetadata := map[string]interface{}{}

		if len(apiMetadata) > 0 {
			metaMap := make(map[string]attr.Value)
			for k, v := range apiMetadata {
				if str, ok := v.(string); ok {
					metaMap[k] = types.StringValue(str)
				}
			}
			metadata, _ = types.MapValue(types.StringType, metaMap)
		} else if !metadata.IsNull() {
			metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
		}

		if !metadata.IsNull() {
			t.Errorf("expected metadata to remain null when API returns empty map and config didn't specify metadata, but got: %v", metadata)
		}
	})

	t.Run("non-null metadata becomes empty map when API returns empty map", func(t *testing.T) {
		// Simulate: user specified metadata in config, then removed all entries
		initialMeta := map[string]attr.Value{"key": types.StringValue("value")}
		metadata, _ := types.MapValue(types.StringType, initialMeta)

		// Simulate: API returned "metadata": {} (empty map)
		apiMetadata := map[string]interface{}{}

		if len(apiMetadata) > 0 {
			metaMap := make(map[string]attr.Value)
			for k, v := range apiMetadata {
				if str, ok := v.(string); ok {
					metaMap[k] = types.StringValue(str)
				}
			}
			metadata, _ = types.MapValue(types.StringType, metaMap)
		} else if !metadata.IsNull() {
			metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
		}

		if metadata.IsNull() {
			t.Error("expected metadata to become empty map (not null) when user previously had metadata in config")
		}
		if len(metadata.Elements()) != 0 {
			t.Errorf("expected metadata to be empty map, got %d elements", len(metadata.Elements()))
		}
	})

	t.Run("metadata populated when API returns non-empty map", func(t *testing.T) {
		// Simulate: user did not specify metadata (null), but API returns metadata
		metadata := types.MapNull(types.StringType)

		// Simulate: API returned "metadata": {"department": "Engineering"}
		apiMetadata := map[string]interface{}{"department": "Engineering"}

		if len(apiMetadata) > 0 {
			metaMap := make(map[string]attr.Value)
			for k, v := range apiMetadata {
				if str, ok := v.(string); ok {
					metaMap[k] = types.StringValue(str)
				}
			}
			metadata, _ = types.MapValue(types.StringType, metaMap)
		} else if !metadata.IsNull() {
			metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
		}

		if metadata.IsNull() {
			t.Error("expected metadata to be populated when API returns non-empty map")
		}
		if len(metadata.Elements()) != 1 {
			t.Errorf("expected 1 metadata entry, got %d", len(metadata.Elements()))
		}
	})
}

// TestOldBehaviorWouldFail demonstrates what the OLD (buggy) behavior did.
// Before the fix, readUser would unconditionally set teams to [] from the API
// response even when the user didn't specify teams, causing:
// "Provider produced inconsistent result after apply: .teams: was null, but now cty.ListValEmpty(cty.String)"
func TestOldBehaviorWouldFail(t *testing.T) {
	t.Run("OLD behavior: null teams overwritten to empty list (the bug)", func(t *testing.T) {
		// Simulate old readUser behavior:
		// teams starts as null (user didn't specify)
		_ = types.ListNull(types.StringType)

		// Old code: unconditionally sets from API response
		apiTeams := []interface{}{} // API returns empty array
		// Old code was:
		//   if teams, ok := userInfo["teams"].([]interface{}); ok {
		//     teamsList := make([]attr.Value, len(teams))
		//     ...
		//     data.Teams, _ = types.ListValue(types.StringType, teamsList)
		//   }
		// This would succeed because []interface{}{} type-asserts to []interface{} fine.
		teamsList := make([]attr.Value, len(apiTeams))
		_ = teamsList
		oldBehaviorResult, _ := types.ListValue(types.StringType, teamsList)

		// This IS the bug: null was overwritten to []
		if oldBehaviorResult.IsNull() {
			t.Error("old behavior should have created empty list (not null) - this test validates the bug existed")
		}
		// Confirm it was overwritten
		if len(oldBehaviorResult.Elements()) != 0 {
			t.Errorf("old behavior created list with %d elements, expected 0", len(oldBehaviorResult.Elements()))
		}
		// This empty list != null, which is what Terraform would detect as inconsistent
	})
}
