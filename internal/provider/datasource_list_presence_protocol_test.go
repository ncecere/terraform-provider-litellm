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

type listPresenceCase struct {
	name, typeName, path, listField, nullField string
	empty, item, malformed                     string
}

func listPresenceCases() []listPresenceCase {
	hash := strings.Repeat("a", 64)
	mappingID := "11111111-1111-4111-8111-111111111111"
	return []listPresenceCase{
		{name: "access_groups", typeName: "litellm_access_groups", path: "/access_group/list", listField: "access_groups", nullField: "model_names", empty: `{"access_groups":[]}`, item: `{"access_group":"presence-access","model_names":null}`, malformed: `{"access_group":"presence-access-bad","model_names":["ok",1]}`},
		{name: "agents", typeName: "litellm_agents", path: "/v1/agents", listField: "agents", nullField: "created_at", empty: `[]`, item: `{"agent_id":"presence-agent","agent_name":"agent"}`, malformed: `{"agent_id":"presence-agent-bad","agent_name":"agent","created_at":false}`},
		{name: "budgets", typeName: "litellm_budgets", path: "/budget/list", listField: "budgets", nullField: "max_budget", empty: `[]`, item: `{"budget_id":"presence-budget","max_budget":null}`, malformed: `{"budget_id":"presence-budget-bad","max_budget":"0"}`},
		{name: "guardrails", typeName: "litellm_guardrails", path: "/v2/guardrails/list", listField: "guardrails", nullField: "default_on", empty: `{"guardrails":[]}`, item: `{"guardrail_id":"presence-guardrail","guardrail_name":"guardrail","litellm_params":{"guardrail":"integration","mode":"pre_call","default_on":null}}`, malformed: `{"guardrail_id":"presence-guardrail-bad","guardrail_name":"guardrail","litellm_params":{"guardrail":"integration","mode":"pre_call","default_on":"false"}}`},
		{name: "jwt_key_mappings", typeName: "litellm_jwt_key_mappings", path: jwtKeyMappingListPath, listField: "mappings", nullField: "description", empty: `{"mappings":[],"total_count":0,"current_page":1,"total_pages":0}`, item: fmt.Sprintf(`{"id":%q,"jwt_claim_name":"sub","jwt_claim_value":"value","description":null,"is_active":false,"created_at":"2026-08-26T00:00:00Z","updated_at":"2026-08-26T00:01:00Z"}`, mappingID), malformed: fmt.Sprintf(`{"id":%q,"jwt_claim_name":"sub","jwt_claim_value":"value","description":false,"is_active":false,"created_at":"2026-08-26T00:00:00Z","updated_at":"2026-08-26T00:01:00Z"}`, "22222222-2222-4222-8222-222222222222")},
		{name: "keys", typeName: "litellm_keys", path: "/key/list", listField: "keys", nullField: "key_alias", empty: `{"keys":[],"total_count":0,"current_page":1,"total_pages":0}`, item: fmt.Sprintf(`{"token":%q,"key_alias":null}`, hash), malformed: fmt.Sprintf(`{"token":%q,"key_alias":false}`, strings.Repeat("b", 64))},
		{name: "models", typeName: "litellm_models", path: "/model/info", listField: "models", nullField: "base_model", empty: `{"data":[]}`, item: `{"model_name":null,"litellm_params":null,"model_info":{"id":"presence-model","base_model":null}}`, malformed: `{"model_info":{"id":"presence-model-bad","base_model":false}}`},
		{name: "organizations", typeName: "litellm_organizations", path: "/organization/list", listField: "organizations", nullField: "organization_alias", empty: `[]`, item: `{"organization_id":"presence-org","organization_alias":null}`, malformed: `{"organization_id":"presence-org-bad","organization_alias":false}`},
		{name: "projects", typeName: "litellm_projects", path: "/project/list", listField: "projects", nullField: "project_alias", empty: `[]`, item: `{"project_id":"presence-project","project_alias":null}`, malformed: `{"project_id":"presence-project-bad","project_alias":false}`},
		{name: "prompts", typeName: "litellm_prompts", path: "/prompts/list", listField: "prompts", nullField: "api_base", empty: `{"prompts":[]}`, item: `{"prompt_id":"presence-prompt","litellm_params":{"prompt_integration":"provider","api_base":null},"prompt_info":null}`, malformed: `{"prompt_id":"presence-prompt-bad","litellm_params":{"prompt_integration":"provider","api_base":false}}`},
		{name: "search_tools", typeName: "litellm_search_tools", path: "/search_tools/list", listField: "search_tools", nullField: "api_base", empty: `{"search_tools":[]}`, item: `{"search_tool_id":"presence-search","search_tool_name":"search","litellm_params":{"api_base":null}}`, malformed: `{"search_tool_id":"presence-search-bad","search_tool_name":"search","litellm_params":{"api_base":false}}`},
		{name: "tags", typeName: "litellm_tags", path: "/tag/list", listField: "tags", nullField: "models", empty: `[]`, item: `{"name":"presence-tag","models":null}`, malformed: `{"name":"presence-tag-bad","models":["ok",1]}`},
		{name: "unified_access_groups", typeName: "litellm_unified_access_groups", path: "/v1/access_group", listField: "access_groups", nullField: "access_group_name", empty: `[]`, item: `{"access_group_id":"presence-unified","access_group_name":null}`, malformed: `{"access_group_id":"presence-unified-bad","access_group_name":false}`},
		{name: "users", typeName: "litellm_users", path: "/user/list", listField: "users", nullField: "user_alias", empty: `{"users":[],"total":0,"page":1,"page_size":100,"total_pages":0}`, item: `{"user_id":"presence-user","user_alias":null}`, malformed: `{"user_id":"presence-user-bad","user_alias":false}`},
	}
}

func (test listPresenceCase) response(mode string) string {
	if mode == "empty" {
		return test.empty
	}
	if mode == "wrong_envelope" {
		switch test.name {
		case "agents", "budgets", "organizations", "projects", "tags", "unified_access_groups":
			return `{"items":[]}`
		default:
			return `[]`
		}
	}
	items := []string{test.item}
	if mode == "duplicate" {
		items = append(items, test.item)
	}
	if mode == "malformed" {
		items = append(items, test.malformed)
	}
	joined := strings.Join(items, ",")
	switch test.name {
	case "access_groups":
		return `{"access_groups":[` + joined + `]}`
	case "guardrails":
		return `{"guardrails":[` + joined + `]}`
	case "jwt_key_mappings":
		return fmt.Sprintf(`{"mappings":[%s],"total_count":%d,"current_page":1,"total_pages":1}`, joined, len(items))
	case "keys":
		return fmt.Sprintf(`{"keys":[%s],"total_count":%d,"current_page":1,"total_pages":1}`, joined, len(items))
	case "models":
		return `{"data":[` + joined + `]}`
	case "prompts":
		return `{"prompts":[` + joined + `]}`
	case "search_tools":
		return `{"search_tools":[` + joined + `]}`
	case "users":
		return fmt.Sprintf(`{"users":[%s],"total":%d,"page":1,"page_size":100,"total_pages":1}`, joined, len(items))
	default:
		return `[` + joined + `]`
	}
}

func TestRegisteredListDataSourcesPresenceProtocol(t *testing.T) {
	// Team and MCP list/source files are intentionally reserved for the #217
	// and #215 rebases. Credential and vector-store have no registered list
	// data source on this baseline. Every other registered list is covered.
	reserved := map[string]string{
		"litellm_teams":       "#217",
		"litellm_mcp_servers": "#215",
	}
	cases := listPresenceCases()
	covered := make(map[string]struct{}, len(cases))
	for _, test := range cases {
		covered[test.typeName] = struct{}{}
	}
	for _, typeName := range []string{
		"litellm_models", "litellm_keys", "litellm_teams", "litellm_organizations", "litellm_users", "litellm_budgets", "litellm_tags",
		"litellm_access_groups", "litellm_unified_access_groups", "litellm_prompts", "litellm_guardrails", "litellm_mcp_servers",
		"litellm_search_tools", "litellm_agents", "litellm_projects", "litellm_jwt_key_mappings",
	} {
		if _, ok := covered[typeName]; !ok {
			if _, reservedForRebase := reserved[typeName]; !reservedForRebase {
				t.Fatalf("registered list data source %s lacks presence protocol coverage", typeName)
			}
		}
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var mode atomic.Value
			mode.Store("empty")
			httpServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path {
					http.NotFound(writer, request)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(writer, test.response(mode.Load().(string)))
			}))
			defer httpServer.Close()
			server, schemas := configuredImportProtocolServer(t, ctx, httpServer.URL)
			schema := schemas.DataSourceSchemas[test.typeName]
			config := singularPresenceConfig(t, schema, map[string]interface{}{})

			mode.Store("empty")
			empty, err := server.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: test.typeName, Config: config})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(empty.Diagnostics) {
				t.Fatalf("empty list read: err=%v diagnostics=%v", err, empty.Diagnostics)
			}
			assertDataSourceReadComputedKnown(t, schema, empty)
			assertProtocolListLength(t, schema, empty.State, test.listField, 0)

			mode.Store("null")
			nullRead, err := server.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: test.typeName, Config: config})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(nullRead.Diagnostics) {
				t.Fatalf("nullable list item read: err=%v diagnostics=%v", err, nullRead.Diagnostics)
			}
			assertDataSourceReadComputedKnown(t, schema, nullRead)
			assertProtocolListItemNull(t, schema, nullRead.State, test.listField, test.nullField)

			for _, failureMode := range []string{"duplicate", "malformed", "wrong_envelope"} {
				mode.Store(failureMode)
				failed, err := server.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: test.typeName, Config: config})
				if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) {
					t.Fatalf("%s response accepted: err=%v diagnostics=%v", failureMode, err, failed.Diagnostics)
				}
				assertSingularPresenceStateUnchanged(t, schema, config, failed.State)
			}
		})
	}
}

func assertProtocolListLength(t *testing.T, schema *tfprotov6.Schema, state *tfprotov6.DynamicValue, field string, want int) {
	t.Helper()
	attributes := protocolAttributeMap(t, schema, state)
	var values []tftypes.Value
	if err := attributes[field].As(&values); err != nil || len(values) != want {
		t.Fatalf("%s length=%d want=%d err=%v", field, len(values), want, err)
	}
}

func assertProtocolListItemNull(t *testing.T, schema *tfprotov6.Schema, state *tfprotov6.DynamicValue, listField, nullField string) {
	t.Helper()
	attributes := protocolAttributeMap(t, schema, state)
	var values []tftypes.Value
	if err := attributes[listField].As(&values); err != nil || len(values) != 1 {
		t.Fatalf("decode %s item: len=%d err=%v", listField, len(values), err)
	}
	var item map[string]tftypes.Value
	if err := values[0].As(&item); err != nil {
		t.Fatalf("decode %s element: %v", listField, err)
	}
	if value, ok := item[nullField]; !ok || !value.IsKnown() || !value.IsNull() {
		t.Fatalf("%s[*].%s was not a known typed null: %v", listField, nullField, value)
	}
}
