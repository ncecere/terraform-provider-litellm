package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestTeamDataSourceAuthoritativePresenceProtocol(t *testing.T) {
	ctx := context.Background()
	var mode atomic.Value
	mode.Store("null")
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Query().Get("team_id") != "team-protocol" {
			http.Error(writer, "bad lookup", http.StatusBadRequest)
			return
		}
		current := mode.Load().(string)
		switch request.URL.Path {
		case "/team/info":
			_, _ = fmt.Fprint(writer, teamDataSourceProtocolInfo(current))
		case "/team/permissions_list":
			_, _ = fmt.Fprint(writer, teamDataSourceProtocolPermissions(current))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer httpServer.Close()

	server, schemas := configuredImportProtocolServer(t, ctx, httpServer.URL)
	schema := schemas.DataSourceSchemas["litellm_team"]
	config := singularPresenceConfig(t, schema, map[string]interface{}{"team_id": "team-protocol"})

	for _, successMode := range []string{"absent", "null", "empty"} {
		t.Run(successMode, func(t *testing.T) {
			mode.Store(successMode)
			read, err := server.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_team", Config: config})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("team read: err=%v diagnostics=%v", err, read.Diagnostics)
			}
			assertDataSourceReadComputedKnown(t, schema, read)
			attributes := protocolAttributeMap(t, schema, read.State)
			if successMode != "empty" {
				for _, field := range []string{"team_alias", "organization_id", "access_group_ids", "models", "max_budget", "spend", "tpm_limit", "rpm_limit", "budget_duration", "metadata", "team_member_permissions", "blocked"} {
					if !attributes[field].IsKnown() || !attributes[field].IsNull() {
						t.Fatalf("%s was not a known typed null: %v", field, attributes[field])
					}
				}
				return
			}
			for _, field := range []string{"access_group_ids", "models", "metadata", "team_member_permissions"} {
				if attributes[field].IsNull() || !attributes[field].IsKnown() {
					t.Fatalf("%s did not preserve explicit empty: %v", field, attributes[field])
				}
			}
			for _, field := range []string{"max_budget", "spend", "tpm_limit", "rpm_limit"} {
				assertSingularPresenceZero(t, attributes[field])
			}
			var blocked bool
			if err := attributes["blocked"].As(&blocked); err != nil || blocked {
				t.Fatalf("blocked did not preserve false: value=%t err=%v", blocked, err)
			}
		})
	}

	for _, failureMode := range []string{
		"wrong_root", "wrong_nested", "missing_keys", "malformed_memberships",
		"wrong_permissions", "missing_permissions_relation", "malformed_permissions",
	} {
		t.Run(failureMode, func(t *testing.T) {
			mode.Store(failureMode)
			read, err := server.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_team", Config: config})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("%s response accepted: err=%v diagnostics=%v", failureMode, err, read.Diagnostics)
			}
			assertSingularPresenceStateUnchanged(t, schema, config, read.State)
			for _, diagnostic := range read.Diagnostics {
				if diagnostic != nil && (strings.Contains(diagnostic.Summary, "other-team") || strings.Contains(diagnostic.Detail, "other-team")) {
					t.Fatalf("diagnostic disclosed response identity: %#v", diagnostic)
				}
			}
		})
	}
}

func TestTeamsListDataSourcePresenceAndAtomicityProtocol(t *testing.T) {
	ctx := context.Background()
	var mode atomic.Value
	mode.Store("empty")
	var filter atomic.Value
	filter.Store("")
	httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/team/list" || request.Method != http.MethodGet {
			http.NotFound(writer, request)
			return
		}
		filter.Store(request.URL.Query().Get("organization_id"))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, teamListDataSourceProtocolResponse(mode.Load().(string)))
	}))
	defer httpServer.Close()

	server, schemas := configuredImportProtocolServer(t, ctx, httpServer.URL)
	schema := schemas.DataSourceSchemas["litellm_teams"]
	config := singularPresenceConfig(t, schema, map[string]interface{}{"organization_id": "org-protocol"})

	mode.Store("empty")
	empty, err := server.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_teams", Config: config})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(empty.Diagnostics) {
		t.Fatalf("empty team list: err=%v diagnostics=%s", err, teamDataSourceProtocolDiagnostics(empty.Diagnostics))
	}
	assertDataSourceReadComputedKnown(t, schema, empty)
	assertProtocolListLength(t, schema, empty.State, "teams", 0)
	if got := filter.Load().(string); got != "org-protocol" {
		t.Fatalf("organization_id filter=%q want org-protocol", got)
	}

	mode.Store("valid")
	valid, err := server.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_teams", Config: config})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(valid.Diagnostics) {
		t.Fatalf("valid team list: err=%v diagnostics=%v", err, valid.Diagnostics)
	}
	assertDataSourceReadComputedKnown(t, schema, valid)
	assertProtocolListLength(t, schema, valid.State, "teams", 2)
	attributes := protocolAttributeMap(t, schema, valid.State)
	var teams []tftypes.Value
	if err := attributes["teams"].As(&teams); err != nil {
		t.Fatalf("decode teams: %v", err)
	}
	var first map[string]tftypes.Value
	if err := teams[0].As(&first); err != nil {
		t.Fatalf("decode first team: %v", err)
	}
	for _, field := range []string{"team_alias", "organization_id", "max_budget", "spend", "tpm_limit", "rpm_limit", "blocked"} {
		if !first[field].IsKnown() || !first[field].IsNull() {
			t.Fatalf("teams[0].%s was not a known typed null: %v", field, first[field])
		}
	}

	for _, failureMode := range []string{"wrong_envelope", "non_object", "late_malformed", "duplicate"} {
		t.Run(failureMode, func(t *testing.T) {
			mode.Store(failureMode)
			read, err := server.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_teams", Config: config})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("%s response accepted: err=%v diagnostics=%v", failureMode, err, read.Diagnostics)
			}
			assertSingularPresenceStateUnchanged(t, schema, config, read.State)
		})
	}
}

func teamDataSourceProtocolDiagnostics(diagnostics []*tfprotov6.Diagnostic) string {
	var result strings.Builder
	for _, diagnostic := range diagnostics {
		if diagnostic != nil {
			fmt.Fprintf(&result, "%s: %s; ", diagnostic.Summary, diagnostic.Detail)
		}
	}
	return result.String()
}

func teamDataSourceProtocolInfo(mode string) string {
	rootID, nestedID := "team-protocol", "team-protocol"
	keys := `[]`
	memberships := `[]`
	switch mode {
	case "wrong_root":
		rootID = "other-team"
	case "wrong_nested":
		nestedID = "other-team"
	case "missing_keys":
		return fmt.Sprintf(`{"team_id":%q,"team_info":{"team_id":%q},"team_memberships":[]}`, rootID, nestedID)
	case "malformed_memberships":
		memberships = `[{},false]`
	}
	fields := `,"team_alias":null,"organization_id":null,"access_group_ids":null,"models":null,"max_budget":null,"spend":null,"tpm_limit":null,"rpm_limit":null,"budget_duration":null,"metadata":null,"blocked":null`
	if mode == "absent" {
		fields = ""
	} else if mode == "empty" {
		fields = `,"team_alias":"","organization_id":"","access_group_ids":[],"models":[],"max_budget":0,"spend":0,"tpm_limit":0,"rpm_limit":0,"budget_duration":"","metadata":{},"blocked":false`
	}
	return fmt.Sprintf(`{"team_id":%q,"team_info":{"team_id":%q%s},"keys":%s,"team_memberships":%s}`, rootID, nestedID, fields, keys, memberships)
}

func teamDataSourceProtocolPermissions(mode string) string {
	switch mode {
	case "wrong_permissions":
		return `{"team_id":"other-team","all_available_permissions":[],"team_member_permissions":[]}`
	case "missing_permissions_relation":
		return `{"team_id":"team-protocol","all_available_permissions":[]}`
	case "malformed_permissions":
		return `{"team_id":"team-protocol","all_available_permissions":[],"team_member_permissions":["ok",false]}`
	case "absent", "null":
		return `{"team_id":"team-protocol","all_available_permissions":[],"team_member_permissions":null}`
	default:
		return `{"team_id":"team-protocol","all_available_permissions":["team_member_add"],"team_member_permissions":[]}`
	}
}

func teamListDataSourceProtocolResponse(mode string) string {
	nullItem := `{"team_id":"team-a","team_alias":null,"organization_id":null,"max_budget":null,"spend":null,"tpm_limit":null,"rpm_limit":null,"blocked":null}`
	explicitItem := `{"team_id":"team-b","team_alias":"","organization_id":"","max_budget":0,"spend":0,"tpm_limit":9007199254740993,"rpm_limit":0,"blocked":false}`
	switch mode {
	case "empty":
		return `[]`
	case "valid":
		return `[` + nullItem + `,` + explicitItem + `]`
	case "wrong_envelope":
		return `{"teams":[]}`
	case "non_object":
		return `[false]`
	case "late_malformed":
		return `[` + nullItem + `,{"team_id":"team-c","blocked":"false"}]`
	case "duplicate":
		return `[` + nullItem + `,` + nullItem + `]`
	default:
		return `null`
	}
}
