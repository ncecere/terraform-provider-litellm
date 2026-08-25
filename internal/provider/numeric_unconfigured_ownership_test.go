package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestUnconfiguredNumericResourceReadsDoNotAdoptServerDefaults(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/agents/agent-1":
			_, _ = w.Write([]byte(`{"agent_id":"agent-1","agent_name":"a","tpm_limit":9007199254740993,"rpm_limit":2,"session_tpm_limit":3,"session_rpm_limit":4}`))
		case "/budget/info":
			_, _ = w.Write([]byte(`[{"budget_id":"budget-1","max_budget":1,"soft_budget":2,"max_parallel_requests":3,"tpm_limit":9007199254740993,"rpm_limit":4,"model_max_budget":{"gpt":9007199254740993}}]`))
		case "/search_tools/search-1":
			_, _ = w.Write([]byte(`{"search_tool_id":"search-1","search_tool_name":"s","litellm_params":{"search_provider":"p","timeout":2.5,"max_retries":9007199254740993}}`))
		case "/v1/mcp/server/mcp-1":
			_, _ = w.Write([]byte(`{"server_id":"mcp-1","server_name":"m","url":"https://m.example","transport":"http","mcp_info":{"mcp_server_cost_info":{"default_cost_per_query":1.5,"tool_name_to_cost_per_query":{"x":2.5}}}}`))
		case "/organization/info":
			_, _ = w.Write([]byte(`{"organization_id":"org-1","members":[{"user_id":"user-1","organization_id":"org-1","user_role":"internal_user","budget_id":"b","litellm_budget_table":{"budget_id":"b","max_budget":42}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}
	ctx := context.Background()
	agent := &AgentResourceModel{ID: types.StringValue("agent-1")}
	if err := (&AgentResource{client: client}).readAgentWithNumericOwnership(ctx, agent, false); err != nil {
		t.Fatal(err)
	}
	if !agent.TPMLimit.IsNull() || !agent.RPMLimit.IsNull() || !agent.SessionTPMLimit.IsNull() || !agent.SessionRPMLimit.IsNull() {
		t.Fatalf("agent adopted defaults: %#v", agent)
	}
	budget := &BudgetResourceModel{BudgetID: types.StringValue("budget-1")}
	if err := (&BudgetResource{client: client}).readBudgetWithNumericOwnership(ctx, budget, false); err != nil {
		t.Fatal(err)
	}
	if !budget.MaxBudget.IsNull() || !budget.TPMLimit.IsNull() || !budget.ModelMaxBudget.IsNull() {
		t.Fatalf("budget adopted defaults: %#v", budget)
	}
	search := &SearchToolResourceModel{SearchToolID: types.StringValue("search-1")}
	if err := (&SearchToolResource{client: client}).readSearchToolWithNumericOwnership(ctx, search, false); err != nil {
		t.Fatal(err)
	}
	if !search.Timeout.IsNull() || !search.MaxRetries.IsNull() {
		t.Fatalf("search adopted defaults: %#v", search)
	}
	mcp := &MCPServerResourceModel{ID: types.StringValue("mcp-1")}
	if err := (&MCPServerResource{client: client}).readMCPServerWithNumericOwnership(ctx, mcp, false); err != nil {
		t.Fatal(err)
	}
	if mcp.MCPInfo != nil {
		t.Fatalf("MCP reconstructed unconfigured mcp_info: %#v", mcp.MCPInfo)
	}
	member := &OrganizationMemberResourceModel{OrganizationID: types.StringValue("org-1"), UserID: types.StringValue("user-1"), Role: types.StringValue("internal_user")}
	exists, _, err := (&OrganizationMemberResource{client: client}).readOrganizationMemberWithNumericOwnership(ctx, member, false)
	if err != nil || !exists {
		t.Fatalf("member read: %v exists=%t", err, exists)
	}
	if !member.MaxBudgetInOrganization.IsNull() {
		t.Fatalf("member adopted default: %#v", member.MaxBudgetInOrganization)
	}
}
