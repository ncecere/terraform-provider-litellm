package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestBuildMCPServerRequestIncludesSkipURLValidation(t *testing.T) {
	t.Parallel()

	r := &MCPServerResource{}
	data := &MCPServerResourceModel{
		ServerName:        types.StringValue("test-mcp"),
		URL:               types.StringValue("http://mcp.internal.svc.cluster.local:8000/mcp"),
		Transport:         types.StringValue("http"),
		SkipURLValidation: types.BoolValue(true),
	}

	req := r.buildMCPServerRequest(context.Background(), data)

	if got, ok := req["skip_url_validation"].(bool); !ok || !got {
		t.Fatalf("expected skip_url_validation=true, got %T: %v", req["skip_url_validation"], req["skip_url_validation"])
	}
}

func TestBuildMCPServerRequestOmitsSkipURLValidationWhenUnconfigured(t *testing.T) {
	t.Parallel()

	r := &MCPServerResource{}
	data := &MCPServerResourceModel{
		ServerName:        types.StringValue("test-mcp"),
		URL:               types.StringValue("https://example.com/mcp"),
		Transport:         types.StringValue("http"),
		SkipURLValidation: types.BoolNull(),
	}

	req := r.buildMCPServerRequest(context.Background(), data)

	if _, ok := req["skip_url_validation"]; ok {
		t.Fatalf("skip_url_validation should be omitted when unconfigured, got %v", req["skip_url_validation"])
	}
}

func TestBuildMCPServerRequestExtraHeadersList(t *testing.T) {
	t.Parallel()

	r := &MCPServerResource{}
	data := &MCPServerResourceModel{
		ServerName:   types.StringValue("test-mcp"),
		URL:          types.StringValue("https://example.com/mcp"),
		Transport:    types.StringValue("http"),
		ExtraHeaders: stringListValue("header-one", "header-two"),
	}

	req := r.buildMCPServerRequest(context.Background(), data)

	extraHeaders, ok := req["extra_headers"].([]string)
	if !ok {
		t.Fatalf("expected extra_headers to be []string, got %T: %v", req["extra_headers"], req["extra_headers"])
	}
	if len(extraHeaders) != 2 {
		t.Fatalf("expected 2 extra headers, got %d", len(extraHeaders))
	}
	if extraHeaders[0] != "header-one" || extraHeaders[1] != "header-two" {
		t.Fatalf("unexpected extra headers: %v", extraHeaders)
	}
}

func TestReadMCPServerExtraHeadersList(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"server_id":     "srv-extra-headers",
			"server_name":   "server-extra-headers",
			"url":           "https://example.com/mcp",
			"transport":     "http",
			"extra_headers": []interface{}{"header-one", "header-two"},
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
		ID:           types.StringValue("srv-extra-headers"),
		ServerID:     types.StringValue("srv-extra-headers"),
		ExtraHeaders: types.ListUnknown(types.StringType),
	}

	if err := r.readMCPServer(context.Background(), &data); err != nil {
		t.Fatalf("readMCPServer returned error: %v", err)
	}

	if data.ExtraHeaders.IsUnknown() || data.ExtraHeaders.IsNull() {
		t.Fatal("extra_headers should be known and non-null after read")
	}

	var headers []string
	if diags := data.ExtraHeaders.ElementsAs(context.Background(), &headers, false); diags.HasError() {
		t.Fatalf("failed to decode extra_headers: %v", diags)
	}
	if len(headers) != 2 || headers[0] != "header-one" || headers[1] != "header-two" {
		t.Fatalf("unexpected extra_headers: %v", headers)
	}
}

func TestMCPServerUpgradeStateV0ToV1ExtraHeadersMapToList(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	r := &MCPServerResource{}
	upgraders := r.UpgradeState(ctx)

	upgrader, ok := upgraders[0]
	if !ok {
		t.Fatal("expected state upgrader for version 0")
	}

	v0State := map[string]interface{}{
		"id":          "srv-1",
		"server_id":   "srv-1",
		"server_name": "server-one",
		"url":         "https://example.com/mcp",
		"transport":   "http",
		"extra_headers": map[string]string{
			"header-two": "value-two",
			"header-one": "value-one",
		},
	}
	v0JSON, err := json.Marshal(v0State)
	if err != nil {
		t.Fatalf("failed to marshal v0 state: %v", err)
	}

	req := resource.UpgradeStateRequest{
		RawState: &tfprotov6.RawState{JSON: v0JSON},
	}
	resp := resource.UpgradeStateResponse{}

	upgrader.StateUpgrader(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics.Errors())
	}
	if resp.DynamicValue == nil {
		t.Fatal("expected DynamicValue to be set")
	}

	var upgraded map[string]interface{}
	if err := json.Unmarshal(resp.DynamicValue.JSON, &upgraded); err != nil {
		t.Fatalf("failed to unmarshal upgraded state: %v", err)
	}

	extraHeaders, ok := upgraded["extra_headers"].([]interface{})
	if !ok {
		t.Fatalf("expected extra_headers to be list after upgrade, got %T", upgraded["extra_headers"])
	}
	if len(extraHeaders) != 2 {
		t.Fatalf("expected 2 headers, got %d", len(extraHeaders))
	}
	// Sorted for deterministic migration.
	if extraHeaders[0] != "header-one" || extraHeaders[1] != "header-two" {
		t.Fatalf("unexpected upgraded extra_headers: %v", extraHeaders)
	}
}

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
