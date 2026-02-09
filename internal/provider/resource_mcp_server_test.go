package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestReadMCPServerResolvesUnknownNestedToolCostMap(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"server_id":   "srv-1",
			"server_name": "server-one",
			"url":         "https://example.com/mcp",
			"transport":   "http",
			"mcp_info": map[string]interface{}{
				"mcp_server_cost_info": map[string]interface{}{},
			},
		})
	}))
	defer server.Close()

	r := &MCPServerResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := MCPServerResourceModel{
		ID:       types.StringValue("srv-1"),
		ServerID: types.StringValue("srv-1"),
		MCPInfo: &MCPInfoModel{
			MCPServerCostInfo: &MCPServerCostInfoModel{
				ToolNameToCostPerQuery: types.MapUnknown(types.Float64Type),
			},
		},
	}

	if err := r.readMCPServer(context.Background(), &data); err != nil {
		t.Fatalf("readMCPServer returned error: %v", err)
	}

	if data.MCPInfo == nil || data.MCPInfo.MCPServerCostInfo == nil {
		t.Fatal("mcp_info.mcp_server_cost_info should be present after read")
	}
	if data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsUnknown() {
		t.Fatal("tool_name_to_cost_per_query should be known after read")
	}
}

func TestReadMCPServerReadsNestedToolCostMap(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"server_id":   "srv-2",
			"server_name": "server-two",
			"url":         "https://example.com/mcp",
			"transport":   "http",
			"mcp_info": map[string]interface{}{
				"mcp_server_cost_info": map[string]interface{}{
					"tool_name_to_cost_per_query": map[string]interface{}{
						"search": 0.25,
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &MCPServerResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := MCPServerResourceModel{
		ID:       types.StringValue("srv-2"),
		ServerID: types.StringValue("srv-2"),
		MCPInfo: &MCPInfoModel{
			MCPServerCostInfo: &MCPServerCostInfoModel{
				ToolNameToCostPerQuery: types.MapUnknown(types.Float64Type),
			},
		},
	}

	if err := r.readMCPServer(context.Background(), &data); err != nil {
		t.Fatalf("readMCPServer returned error: %v", err)
	}

	toolCosts := map[string]float64{}
	if diags := data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.ElementsAs(context.Background(), &toolCosts, false); diags.HasError() {
		t.Fatalf("failed to decode tool_name_to_cost_per_query: %v", diags)
	}
	if got := toolCosts["search"]; got != 0.25 {
		t.Fatalf("expected search cost 0.25, got %v", got)
	}
}
