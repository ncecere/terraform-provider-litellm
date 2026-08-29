package provider

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func protocolNestedAttribute(t *testing.T, value tftypes.Value, name string) tftypes.Value {
	t.Helper()
	attributes := map[string]tftypes.Value{}
	if err := value.As(&attributes); err != nil {
		t.Fatalf("decode nested protocol object: %v", err)
	}
	child, ok := attributes[name]
	if !ok {
		t.Fatalf("nested protocol object omitted %q", name)
	}
	return child
}

func TestAdditionalNumericImportProtocolsAdoptOnceAndTransitionMarker(t *testing.T) {
	ctx := context.Background()
	var callMutex sync.Mutex
	readCalls := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		callKey := request.URL.Path
		if request.URL.Path == "/team/info" {
			callKey += "?team_id=" + request.URL.Query().Get("team_id")
		}
		if request.URL.Path != "/team/permissions_list" {
			callMutex.Lock()
			readCalls[callKey]++
			firstRead := readCalls[callKey] == 1
			callMutex.Unlock()
			if firstRead {
				switch request.URL.Path {
				case "/v1/agents/agent-import":
					_, _ = writer.Write([]byte(`{"agent_id":"wrong-agent","agent_name":"wrong"}`))
				case "/budget/info":
					_, _ = writer.Write([]byte(`[{"budget_id":"wrong-budget"}]`))
				case "/search_tools/search-import":
					_, _ = writer.Write([]byte(`{"search_tool_id":"wrong-search","search_tool_name":"wrong","litellm_params":{"search_provider":"tavily"}}`))
				case "/v1/mcp/server/mcp-import":
					_, _ = writer.Write([]byte(`{"server_id":"wrong-mcp","server_name":"wrong","url":"https://wrong.invalid","transport":"http"}`))
				case "/organization/info":
					_, _ = writer.Write([]byte(`{"organization_id":"wrong-organization","members":[]}`))
				case "/project/info":
					_, _ = writer.Write([]byte(`{"project_id":"wrong-project","team_id":"team-owner"}`))
				case "/tag/info":
					_, _ = writer.Write([]byte(`{"tag-import":{"name":"wrong-tag"}}`))
				case "/team/info":
					_, _ = writer.Write([]byte(`{"team_id":"wrong-team","team_info":{"team_id":"wrong-team","team_alias":"wrong","members_with_roles":[]},"team_memberships":[]}`))
				case "/user/info":
					_, _ = writer.Write([]byte(`{"user_id":"wrong-user","user_info":{"user_id":"wrong-user"}}`))
				case "/key/info":
					_, _ = writer.Write([]byte(`{"key":"wrong-key","info":{}}`))
				default:
					http.NotFound(writer, request)
				}
				return
			}
		}
		switch request.URL.Path {
		case "/v1/agents/agent-import":
			_, _ = writer.Write([]byte(`{"agent_id":"agent-import","agent_name":"imported agent","tpm_limit":9007199254740993,"rpm_limit":10,"session_tpm_limit":11,"session_rpm_limit":12}`))
		case "/budget/info":
			_, _ = writer.Write([]byte(`[{"budget_id":"budget-import","max_budget":12.5,"soft_budget":10,"max_parallel_requests":2,"tpm_limit":9007199254740993,"rpm_limit":20,"model_max_budget":{"gpt":9007199254740993}}]`))
		case "/search_tools/search-import":
			_, _ = writer.Write([]byte(`{"search_tool_id":"search-import","search_tool_name":"imported search","litellm_params":{"search_provider":"tavily","timeout":3.5,"max_retries":9007199254740993}}`))
		case "/v1/mcp/server/mcp-import":
			_, _ = writer.Write([]byte(`{"server_id":"mcp-import","server_name":"imported mcp","url":"https://mcp.example.test","transport":"http","mcp_info":{"mcp_server_cost_info":{"default_cost_per_query":0.125,"tool_name_to_cost_per_query":{"search":0.25}}}}`))
		case "/organization/info":
			_, _ = writer.Write([]byte(`{"organization_id":"org-import","members":[{"user_id":"user-import","organization_id":"org-import","user_role":"internal_user","budget_id":"member-budget","litellm_budget_table":{"budget_id":"member-budget","max_budget":42.5}}]}`))
		case "/project/info":
			_, _ = writer.Write([]byte(`{"project_id":"project-import","project_alias":"imported project","team_id":"team-owner","litellm_budget_table":{"tpm_limit":9007199254740993,"model_max_budget":{}},"metadata":{"model_rpm_limit":{},"model_tpm_limit":{}}}`))
		case "/tag/info":
			_, _ = writer.Write([]byte(`{"tag-import":{"name":"tag-import","litellm_budget_table":{"budget_id":"tag-budget","tpm_limit":9007199254740993,"model_max_budget":{}}}}`))
		case "/team/info":
			if request.URL.Query().Get("team_id") == "team-member-import" {
				_, _ = writer.Write([]byte(`{"team_id":"team-member-import","team_info":{"team_id":"team-member-import","team_alias":"member team","members_with_roles":[{"user_id":"member-import","role":"user"}]},"team_memberships":[{"user_id":"member-import","team_id":"team-member-import","budget_id":"member-budget","litellm_budget_table":{"budget_id":"member-budget","max_budget":42.5,"budget_duration":null}}]}`))
			} else {
				_, _ = writer.Write([]byte(`{"team_id":"team-import","team_info":{"team_id":"team-import","team_alias":"imported team","tpm_limit":9007199254740993,"metadata":{"model_rpm_limit":{},"model_tpm_limit":{}}},"keys":[],"team_memberships":[]}`))
			}
		case "/team/permissions_list":
			_, _ = writer.Write([]byte(`{"team_id":"team-import","team_member_permissions":[]}`))
		case "/user/info":
			_, _ = writer.Write([]byte(`{"user_id":"user-resource-import","user_info":{"user_id":"user-resource-import","user_email":"import@example.test","tpm_limit":9007199254740993}}`))
		case "/key/info":
			_, _ = writer.Write([]byte(`{"key":"sk-import","info":{"tpm_limit":9007199254740993,"litellm_budget_table":{"max_budget":50.5,"soft_budget":40,"model_max_budget":{}},"metadata":{"model_rpm_limit":{},"model_tpm_limit":{}}}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemaResponse, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemaResponse.Diagnostics) {
		t.Fatalf("get provider schema: %v, %v", err, schemaResponse.Diagnostics)
	}
	providerValue, err := tftypes.ValueFromJSON([]byte(`{"api_base":"`+server.URL+`","api_key":"test-key","insecure_skip_verify":null,"litellm_changed_by":null}`), schemaResponse.Provider.ValueType())
	if err != nil {
		t.Fatalf("build provider config: %v", err)
	}
	configureResponse, err := protocolServer.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: accessGroupProtocolDynamicValue(t, schemaResponse.Provider, providerValue)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configureResponse.Diagnostics) {
		t.Fatalf("configure provider: %v, %v", err, configureResponse.Diagnostics)
	}

	tests := []struct {
		name, typeName, importID string
		assertRead               func(*testing.T, map[string]tftypes.Value)
	}{
		{"agent", "litellm_agent", "agent-import", func(t *testing.T, a map[string]tftypes.Value) {
			if got := protocolInt64(t, a["tpm_limit"]); got != 9007199254740993 {
				t.Fatalf("agent TPM = %d", got)
			}
		}},
		{"budget", "litellm_budget", "budget-import", func(t *testing.T, a map[string]tftypes.Value) {
			if got := protocolInt64(t, a["tpm_limit"]); got != 9007199254740993 {
				t.Fatalf("budget TPM = %d", got)
			}
			var modelBudget string
			if err := a["model_max_budget"].As(&modelBudget); err != nil || modelBudget != `{"gpt":9007199254740993}` {
				t.Fatalf("budget model_max_budget = %q (%v)", modelBudget, err)
			}
		}},
		{"search tool", "litellm_search_tool", "search-import", func(t *testing.T, a map[string]tftypes.Value) {
			if got := protocolInt64(t, a["max_retries"]); got != 9007199254740993 {
				t.Fatalf("search retries = %d", got)
			}
		}},
		{"mcp server", "litellm_mcp_server", "mcp-import", func(t *testing.T, a map[string]tftypes.Value) {
			costInfo := protocolNestedAttribute(t, a["mcp_info"], "mcp_server_cost_info")
			costs := map[string]tftypes.Value{}
			if err := costInfo.As(&costs); err != nil {
				t.Fatal(err)
			}
			toolCosts := map[string]tftypes.Value{}
			if err := costs["tool_name_to_cost_per_query"].As(&toolCosts); err != nil {
				t.Fatal(err)
			}
			var cost big.Float
			if err := toolCosts["search"].As(&cost); err != nil {
				t.Fatal(err)
			}
			value, _ := cost.Float64()
			if value != .25 {
				t.Fatalf("MCP cost = %v", value)
			}
		}},
		{"organization member", "litellm_organization_member", "org-import:user-import", func(t *testing.T, a map[string]tftypes.Value) {
			var b big.Float
			if err := a["max_budget_in_organization"].As(&b); err != nil {
				t.Fatal(err)
			}
			v, _ := b.Float64()
			if v != 42.5 {
				t.Fatalf("member budget = %v", v)
			}
		}},
		{"project", "litellm_project", "project-import", func(t *testing.T, a map[string]tftypes.Value) {
			if got := protocolInt64(t, a["tpm_limit"]); got != 9007199254740993 {
				t.Fatalf("project TPM = %d", got)
			}
		}},
		{"tag", "litellm_tag", "tag-import", func(t *testing.T, a map[string]tftypes.Value) {
			if got := protocolInt64(t, a["tpm_limit"]); got != 9007199254740993 {
				t.Fatalf("tag TPM = %d", got)
			}
		}},
		{"team", "litellm_team", "team-import", func(t *testing.T, a map[string]tftypes.Value) {
			if got := protocolInt64(t, a["tpm_limit"]); got != 9007199254740993 {
				t.Fatalf("team TPM = %d", got)
			}
		}},
		{"team member", "litellm_team_member", "team-member-import:member-import", func(t *testing.T, a map[string]tftypes.Value) {
			var b big.Float
			if err := a["max_budget_in_team"].As(&b); err != nil {
				t.Fatal(err)
			}
			value, _ := b.Float64()
			if value != 42.5 {
				t.Fatalf("team member budget = %v", value)
			}
		}},
		{"user", "litellm_user", "user-resource-import", func(t *testing.T, a map[string]tftypes.Value) {
			if got := protocolInt64(t, a["tpm_limit"]); got != 9007199254740993 {
				t.Fatalf("user TPM = %d", got)
			}
		}},
		{"key", "litellm_key", "sk-import", func(t *testing.T, a map[string]tftypes.Value) {
			if got := protocolInt64(t, a["tpm_limit"]); got != 9007199254740993 {
				t.Fatalf("key TPM = %d", got)
			}
			for field, want := range map[string]float64{"max_budget": 50.5, "soft_budget": 40} {
				var number big.Float
				if err := a[field].As(&number); err != nil {
					t.Fatalf("decode key %s: %v", field, err)
				}
				got, _ := number.Float64()
				if got != want {
					t.Fatalf("key %s = %v, want %v", field, got, want)
				}
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schema := schemaResponse.ResourceSchemas[test.typeName]
			importResponse, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: test.typeName, ID: test.importID})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(importResponse.Diagnostics) || len(importResponse.ImportedResources) != 1 {
				t.Fatalf("import: %v, %v", err, importResponse.Diagnostics)
			}
			imported := importResponse.ImportedResources[0]
			if len(imported.Private) == 0 {
				t.Fatal("numeric import marker was not stored")
			}
			rejectedRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: test.typeName, CurrentState: imported.State, Private: imported.Private})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(rejectedRead.Diagnostics) {
				t.Fatalf("non-authoritative first read was accepted: %v, %v", err, rejectedRead.Diagnostics)
			}
			if !protocolPrivateHasKey(t, rejectedRead.Private, numericImportedPrivateKey) {
				t.Fatal("import marker was cleared after rejected identity")
			}
			importedValue, _ := imported.State.Unmarshal(schema.ValueType())
			rejectedValue, _ := rejectedRead.NewState.Unmarshal(schema.ValueType())
			if !importedValue.Equal(rejectedValue) {
				t.Fatal("rejected import read changed prior state")
			}

			firstRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: test.typeName, CurrentState: rejectedRead.NewState, Private: rejectedRead.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(firstRead.Diagnostics) {
				t.Fatalf("valid read after rejected identity: %v, %v", err, firstRead.Diagnostics)
			}
			test.assertRead(t, protocolAttributeMap(t, schema, firstRead.NewState))
			if protocolPrivateHasKey(t, firstRead.Private, numericImportedPrivateKey) {
				t.Fatalf("marker not cleared after authoritative read: %x", firstRead.Private)
			}
			secondRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: test.typeName, CurrentState: firstRead.NewState, Private: firstRead.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(secondRead.Diagnostics) {
				t.Fatalf("second read: %v, %v", err, secondRead.Diagnostics)
			}
			firstValue, _ := firstRead.NewState.Unmarshal(schema.ValueType())
			secondValue, _ := secondRead.NewState.Unmarshal(schema.ValueType())
			if !firstValue.Equal(secondValue) {
				t.Fatal("second read drifted after marker transition")
			}
		})
	}
}
