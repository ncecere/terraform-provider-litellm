package provider

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestProjectTeamDataSourceInfoAcceptsAuthoritativeV198Envelope(t *testing.T) {
	t.Parallel()
	result := decodeTeamDataSourceObject(t, `{
		"team_id":"team-presence",
		"team_info":{
			"team_id":"team-presence","team_alias":"","organization_id":null,
			"access_group_ids":["group-b","group-a"],"models":[],
			"max_budget":0,"spend":1.25,"tpm_limit":9007199254740993,"rpm_limit":9223372036854775807,
			"budget_duration":"","metadata":{},"blocked":false
		},
		"keys":[{}],"team_memberships":[{}]
	}`)

	got, err := projectTeamDataSourceInfo(result, "team-presence")
	if err != nil {
		t.Fatalf("project authoritative team response: %v", err)
	}
	if got.ID.ValueString() != "team-presence" || got.TeamID.ValueString() != "team-presence" {
		t.Fatalf("identity was not projected: %#v", got)
	}
	if got.TeamAlias.IsNull() || got.TeamAlias.ValueString() != "" || !got.OrganizationID.IsNull() {
		t.Fatalf("string presence was not preserved: alias=%v organization=%v", got.TeamAlias, got.OrganizationID)
	}
	if got.Models.IsNull() || len(got.Models.Elements()) != 0 || got.Metadata.IsNull() || len(got.Metadata.Elements()) != 0 {
		t.Fatalf("empty collections were not preserved: models=%v metadata=%v", got.Models, got.Metadata)
	}
	if got.AccessGroupIDs.IsNull() || len(got.AccessGroupIDs.Elements()) != 2 {
		t.Fatalf("access_group_ids set was not preserved: %v", got.AccessGroupIDs)
	}
	if got.MaxBudget.IsNull() || got.MaxBudget.ValueFloat64() != 0 || got.Blocked.IsNull() || got.Blocked.ValueBool() {
		t.Fatalf("explicit zero/false were not preserved: max_budget=%v blocked=%v", got.MaxBudget, got.Blocked)
	}
	if got.TPMLimit.ValueInt64() != 9007199254740993 || got.RPMLimit.ValueInt64() != math.MaxInt64 {
		t.Fatalf("exact integer limits were not preserved: tpm=%d rpm=%d", got.TPMLimit.ValueInt64(), got.RPMLimit.ValueInt64())
	}
}

func TestProjectTeamDataSourceInfoRejectsNonAuthoritativeRelations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body string
	}{
		{"flat", `{"team_id":"team-presence","team_alias":"flat"}`},
		{"wrong root identity", `{"team_id":"other","team_info":{"team_id":"team-presence"},"keys":[],"team_memberships":[]}`},
		{"wrong nested identity", `{"team_id":"team-presence","team_info":{"team_id":"other"},"keys":[],"team_memberships":[]}`},
		{"missing keys", `{"team_id":"team-presence","team_info":{"team_id":"team-presence"},"team_memberships":[]}`},
		{"null memberships", `{"team_id":"team-presence","team_info":{"team_id":"team-presence"},"keys":[],"team_memberships":null}`},
		{"late malformed key", `{"team_id":"team-presence","team_info":{"team_id":"team-presence"},"keys":[{},false],"team_memberships":[]}`},
		{"unknown root relation", `{"team_id":"team-presence","team_info":{"team_id":"team-presence"},"keys":[],"team_memberships":[],"alternate":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := projectTeamDataSourceInfo(decodeTeamDataSourceObject(t, test.body), "team-presence")
			if err == nil {
				t.Fatalf("non-authoritative response accepted: %#v", got)
			}
			if strings.Contains(err.Error(), "other") || strings.Contains(err.Error(), "alternate") {
				t.Fatalf("diagnostic disclosed response content: %v", err)
			}
		})
	}
}

func TestProjectTeamDataSourcePermissionsPresenceAndAuthority(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		body       string
		wantNull   bool
		wantLength int
		wantError  bool
	}{
		{"null", `{"team_id":"team-presence","all_available_permissions":[],"team_member_permissions":null}`, true, 0, false},
		{"empty", `{"team_id":"team-presence","all_available_permissions":["team_member_add"],"team_member_permissions":[]}`, false, 0, false},
		{"values", `{"team_id":"team-presence","all_available_permissions":["team_member_add"],"team_member_permissions":["team_member_add"]}`, false, 1, false},
		{"wrong identity", `{"team_id":"other","all_available_permissions":[],"team_member_permissions":[]}`, false, 0, true},
		{"missing available relation", `{"team_id":"team-presence","team_member_permissions":[]}`, false, 0, true},
		{"missing team relation", `{"team_id":"team-presence","all_available_permissions":[]}`, false, 0, true},
		{"malformed available relation", `{"team_id":"team-presence","all_available_permissions":[false],"team_member_permissions":[]}`, false, 0, true},
		{"malformed team relation", `{"team_id":"team-presence","all_available_permissions":[],"team_member_permissions":["ok",false]}`, false, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := projectTeamDataSourcePermissions(decodeTeamDataSourceObject(t, test.body), "team-presence")
			if test.wantError {
				if err == nil {
					t.Fatalf("invalid permissions response accepted: %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("permissions projection: %v", err)
			}
			if got.IsNull() != test.wantNull || (!got.IsNull() && len(got.Elements()) != test.wantLength) {
				t.Fatalf("permissions=%v want null=%t length=%d", got, test.wantNull, test.wantLength)
			}
		})
	}
}

func TestProjectTeamsListDataSourceIsAtomicAndPresenceAware(t *testing.T) {
	t.Parallel()
	valid := decodeTeamDataSourceObject(t, `{"team_id":"team-b","team_alias":null,"organization_id":"","max_budget":0,"spend":0,"tpm_limit":0,"rpm_limit":0,"blocked":false}`)
	empty := decodeTeamDataSourceObject(t, `{"team_id":"team-a","team_alias":"","organization_id":null,"max_budget":null,"spend":null,"tpm_limit":null,"rpm_limit":null,"blocked":null}`)
	got, err := projectTeamsListDataSource([]map[string]interface{}{valid, empty})
	if err != nil {
		t.Fatalf("project team list: %v", err)
	}
	if len(got) != 2 || got[0].TeamID.ValueString() != "team-a" || got[1].TeamID.ValueString() != "team-b" {
		t.Fatalf("stable canonical ordering not preserved: %#v", got)
	}
	if !got[0].MaxBudget.IsNull() || got[1].MaxBudget.IsNull() || got[1].MaxBudget.ValueFloat64() != 0 {
		t.Fatalf("null/zero presence not preserved: %#v", got)
	}
	if got[1].Blocked.IsNull() || got[1].Blocked.ValueBool() {
		t.Fatalf("explicit false not preserved: %v", got[1].Blocked)
	}

	lateMalformed := decodeTeamDataSourceObject(t, `{"team_id":"team-c","team_alias":false}`)
	if partial, err := projectTeamsListDataSource([]map[string]interface{}{valid, lateMalformed}); err == nil || partial != nil {
		t.Fatalf("late malformed item published partial list: partial=%#v err=%v", partial, err)
	}
	if partial, err := projectTeamsListDataSource([]map[string]interface{}{valid, valid}); err == nil || partial != nil {
		t.Fatalf("duplicate canonical identity published partial list: partial=%#v err=%v", partial, err)
	}
}

func decodeTeamDataSourceObject(t *testing.T, body string) map[string]interface{} {
	t.Helper()
	var result map[string]interface{}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf("decode test response: %v", err)
	}
	return result
}
