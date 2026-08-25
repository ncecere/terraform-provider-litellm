package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
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

	req, err := r.buildMCPServerRequest(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}

	if got, ok := req["skip_url_validation"].(bool); !ok || !got {
		t.Fatalf("expected skip_url_validation=true, got %T: %v", req["skip_url_validation"], req["skip_url_validation"])
	}
}

func TestReadMCPServerFallsBackToCollection(t *testing.T) {
	t.Parallel()

	var collectionReads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v1/mcp/server/server-1" {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = writer.Write([]byte(`{"error":"server not found after internal 404 lookup"}`))
			return
		}
		if request.URL.Path == "/v1/mcp/server" {
			if collectionReads.Add(1) < 3 {
				_ = json.NewEncoder(writer).Encode([]map[string]interface{}{})
				return
			}
			_ = json.NewEncoder(writer).Encode([]map[string]interface{}{
				{
					"server_id":         "server-1",
					"server_name":       "test-mcp",
					"url":               "http://mcp.internal/mcp",
					"transport":         "http",
					"auth_type":         "none",
					"allow_all_keys":    true,
					"mcp_access_groups": []interface{}{},
					"args":              []interface{}{},
					"env":               map[string]interface{}{},
					"credentials":       map[string]interface{}{},
					"allowed_tools":     []interface{}{},
					"extra_headers":     []interface{}{},
					"static_headers":    map[string]interface{}{},
				},
			})
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()

	resource := &MCPServerResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := MCPServerResourceModel{
		ID:              types.StringValue("server-1"),
		ServerID:        types.StringValue("server-1"),
		MCPAccessGroups: types.ListUnknown(types.StringType),
		Args:            types.ListUnknown(types.StringType),
		Env:             types.MapUnknown(types.StringType),
		Credentials:     types.MapUnknown(types.StringType),
		AllowedTools:    types.ListUnknown(types.StringType),
		ExtraHeaders:    types.ListUnknown(types.StringType),
		StaticHeaders:   types.MapUnknown(types.StringType),
	}

	if err := resource.readMCPServer(context.Background(), &data); err != nil {
		t.Fatalf("readMCPServer returned error: %v", err)
	}
	if data.ServerName.ValueString() != "test-mcp" || data.URL.ValueString() != "http://mcp.internal/mcp" {
		t.Fatalf("collection fallback did not populate server: %#v", data)
	}
	assertMCPServerCollectionsKnown(t, data)
	if got := collectionReads.Load(); got != 3 {
		t.Fatalf("collection reads = %d, want 3", got)
	}
}

func TestResolveUnknownMCPServerStateAfterFailedCreate(t *testing.T) {
	t.Parallel()

	data := MCPServerResourceModel{
		ID:              types.StringValue("server-1"),
		ServerID:        types.StringValue("server-1"),
		MCPAccessGroups: types.ListUnknown(types.StringType),
		Args:            types.ListUnknown(types.StringType),
		Env:             types.MapUnknown(types.StringType),
		Credentials:     types.MapUnknown(types.StringType),
		AllowedTools:    types.ListUnknown(types.StringType),
		ExtraHeaders:    types.ListUnknown(types.StringType),
		StaticHeaders:   types.MapUnknown(types.StringType),
		CreatedAt:       types.StringUnknown(),
		CreatedBy:       types.StringUnknown(),
		MCPInfo: &MCPInfoModel{MCPServerCostInfo: &MCPServerCostInfoModel{
			ToolNameToCostPerQuery: types.MapUnknown(types.Float64Type),
		}},
	}

	resolveUnknownMCPServerState(&data, nil)
	assertMCPServerCollectionsKnown(t, data)
	if data.CreatedAt.IsUnknown() || data.CreatedBy.IsUnknown() || data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsUnknown() {
		t.Fatalf("failed create retained unknown computed state: %#v", data)
	}
}

func TestResolveUnknownMCPServerStatePreservesPriorValues(t *testing.T) {
	t.Parallel()

	data := MCPServerResourceModel{
		AllowedTools: types.ListUnknown(types.StringType),
		CreatedAt:    types.StringUnknown(),
	}
	prior := MCPServerResourceModel{
		AllowedTools: stringListValue("search"),
		CreatedAt:    types.StringValue("2026-01-01T00:00:00Z"),
	}
	resolveUnknownMCPServerState(&data, &prior)

	if !data.AllowedTools.Equal(prior.AllowedTools) || !data.CreatedAt.Equal(prior.CreatedAt) {
		t.Fatalf("prior values not preserved: %#v", data)
	}
}

func assertMCPServerCollectionsKnown(t *testing.T, data MCPServerResourceModel) {
	t.Helper()
	if data.MCPAccessGroups.IsUnknown() || data.Args.IsUnknown() || data.Env.IsUnknown() || data.Credentials.IsUnknown() || data.AllowedTools.IsUnknown() || data.ExtraHeaders.IsUnknown() || data.StaticHeaders.IsUnknown() {
		t.Fatalf("MCP server retained unknown collection state: %#v", data)
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

	req, err := r.buildMCPServerRequest(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}

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

	req, err := r.buildMCPServerRequest(context.Background(), data)
	if err != nil {
		t.Fatal(err)
	}

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

	if err := r.readMCPServerWithNumericOwnership(context.Background(), &data, true); err != nil {
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
