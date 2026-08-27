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

const mcpDataSourceV198Item = `{
	"server_id":"presence-mcp",
	"server_name":"",
	"alias":null,
	"description":"server",
	"url":"",
	"spec_path":null,
	"transport":"http",
	"auth_type":"none",
	"mcp_access_groups":[],
	"mcp_info":{"access":false,"nested":[9007199254740993,null]},
	"command":null,
	"args":[],
	"env":{},
	"allowed_tools":["search"],
	"extra_headers":[],
	"static_headers":{},
	"authorization_url":"",
	"token_url":null,
	"registration_url":"",
	"allow_all_keys":false,
	"created_at":null,
	"created_by":"",
	"updated_at":"2026-08-26T00:00:00Z",
	"updated_by":null,
	"status":"unknown",
	"last_health_check":null,
	"health_check_error":""
}`

func TestMCPDataSourcesCompleteEveryComputedPathProtocol(t *testing.T) {
	ctx := context.Background()
	var mode atomic.Value
	mode.Store("valid")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		item := mcpDataSourceV198Item
		switch mode.Load().(string) {
		case "minimal":
			item = `{"server_id":"presence-mcp","transport":"stdio"}`
		case "masked":
			item = `{"server_id":"presence-mcp","transport":"http","mcp_info":{"is_public":true}}`
		case "null":
			item = `{"server_id":"presence-mcp","transport":"http","server_name":null,"mcp_access_groups":null,"args":null,"env":null,"allowed_tools":null,"extra_headers":null,"static_headers":null,"allow_all_keys":null,"mcp_info":null}`
		case "empty":
			if request.URL.Path == "/v1/mcp/server" {
				_, _ = writer.Write([]byte(`[]`))
				return
			}
		}
		if request.URL.Path == "/v1/mcp/server" {
			_, _ = fmt.Fprintf(writer, "[%s]", item)
			return
		}
		if request.URL.Path == "/v1/mcp/server/presence-mcp" {
			_, _ = writer.Write([]byte(item))
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	singleSchema := schemas.DataSourceSchemas["litellm_mcp_server"]
	listSchema := schemas.DataSourceSchemas["litellm_mcp_servers"]
	singleConfig := singularPresenceConfig(t, singleSchema, map[string]interface{}{"server_id": "presence-mcp"})
	listConfig := singularPresenceConfig(t, listSchema, map[string]interface{}{})

	for _, readMode := range []string{"valid", "minimal", "null", "masked"} {
		t.Run(readMode, func(t *testing.T) {
			mode.Store(readMode)
			single, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_server", Config: singleConfig})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(single.Diagnostics) {
				t.Fatalf("singular read: err=%v diagnostics=%v", err, single.Diagnostics)
			}
			assertDataSourceReadComputedKnown(t, singleSchema, single)

			list, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_servers", Config: listConfig})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(list.Diagnostics) {
				t.Fatalf("list read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(list.Diagnostics))
			}
			assertDataSourceReadComputedKnown(t, listSchema, list)
			assertProtocolListLength(t, listSchema, list.State, "mcp_servers", 1)

			singleAttributes := protocolAttributeMap(t, singleSchema, single.State)
			listItem := mcpProtocolListItem(t, listSchema, list.State)
			switch readMode {
			case "valid":
				assertMCPProtocolFalse(t, singleAttributes["allow_all_keys"])
				assertMCPProtocolFalse(t, listItem["allow_all_keys"])
				for _, field := range []string{"mcp_access_groups", "args", "env", "extra_headers", "static_headers"} {
					assertMCPProtocolEmptyCollection(t, singleAttributes[field])
				}
				for name, value := range map[string]tftypes.Value{
					"singular": singleAttributes["mcp_info_json"],
					"list":     listItem["mcp_info_json"],
				} {
					var document string
					if err := value.As(&document); err != nil || document != `{"access":false,"nested":[9007199254740993,null]}` {
						t.Fatalf("%s mcp_info_json=%q err=%v", name, document, err)
					}
				}
			case "minimal", "null":
				for _, field := range []string{"server_name", "mcp_access_groups", "args", "env", "allow_all_keys", "mcp_info_json"} {
					if value := singleAttributes[field]; !value.IsKnown() || !value.IsNull() {
						t.Fatalf("%s was not a known typed null: %v", field, value)
					}
				}
			case "masked":
				if !singleAttributes["mcp_info_json"].IsNull() || !listItem["mcp_info_json"].IsNull() {
					t.Fatal("restricted mcp_info singleton became incomplete authority")
				}
			}
		})
	}

	mode.Store("empty")
	empty, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_servers", Config: listConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(empty.Diagnostics) {
		t.Fatalf("empty list read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(empty.Diagnostics))
	}
	assertDataSourceReadComputedKnown(t, listSchema, empty)
	assertProtocolListLength(t, listSchema, empty.State, "mcp_servers", 0)
}

func TestMCPDataSourcesRejectMalformedResponsesAtomicallyProtocol(t *testing.T) {
	ctx := context.Background()
	const secret = "response-secret-mcp"
	var mode atomic.Value
	mode.Store("wrong_identity")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		valid := mcpDataSourceV198Item
		var payload string
		switch mode.Load().(string) {
		case "wrong_identity":
			payload = `{"server_id":"` + secret + `","transport":"http"}`
		case "single_wrong_root":
			payload = `[{"server_id":"presence-mcp","transport":"http"}]`
		case "single_late_nested":
			payload = `{"server_id":"presence-mcp","transport":"http","allowed_tools":["valid",false]}`
		case "list_wrong_root":
			payload = `{}`
		case "list_wrong_element":
			payload = `[` + valid + `,false]`
		case "list_duplicate":
			payload = `[` + valid + `,` + valid + `]`
		case "list_late_nested":
			payload = `[` + valid + `,{"server_id":"late-mcp","transport":"http","static_headers":{"valid":"value","invalid":false}}]`
		default:
			t.Fatalf("unsupported test mode %q", mode.Load().(string))
		}
		_, _ = writer.Write([]byte(payload))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	singleSchema := schemas.DataSourceSchemas["litellm_mcp_server"]
	listSchema := schemas.DataSourceSchemas["litellm_mcp_servers"]
	singleConfig := singularPresenceConfig(t, singleSchema, map[string]interface{}{"server_id": "presence-mcp"})
	listConfig := singularPresenceConfig(t, listSchema, map[string]interface{}{})

	for _, failureMode := range []string{"wrong_identity", "single_wrong_root", "single_late_nested"} {
		mode.Store(failureMode)
		failed, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_server", Config: singleConfig})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) {
			t.Fatalf("%s accepted: err=%v diagnostics=%v", failureMode, err, failed.Diagnostics)
		}
		assertSingularPresenceStateUnchanged(t, singleSchema, singleConfig, failed.State)
		assertMCPDiagnosticsContentSafe(t, failed.Diagnostics, secret)
	}
	for _, failureMode := range []string{"list_wrong_root", "list_wrong_element", "list_duplicate", "list_late_nested"} {
		mode.Store(failureMode)
		failed, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_servers", Config: listConfig})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) {
			t.Fatalf("%s accepted: err=%v diagnostics=%v", failureMode, err, failed.Diagnostics)
		}
		assertSingularPresenceStateUnchanged(t, listSchema, listConfig, failed.State)
		assertMCPDiagnosticsContentSafe(t, failed.Diagnostics, secret)
	}
}

func mcpProtocolListItem(t *testing.T, schema *tfprotov6.Schema, state *tfprotov6.DynamicValue) map[string]tftypes.Value {
	t.Helper()
	var items []tftypes.Value
	if err := protocolAttributeMap(t, schema, state)["mcp_servers"].As(&items); err != nil || len(items) != 1 {
		t.Fatalf("decode MCP server list: len=%d err=%v", len(items), err)
	}
	item := map[string]tftypes.Value{}
	if err := items[0].As(&item); err != nil {
		t.Fatal(err)
	}
	return item
}

func assertMCPProtocolFalse(t *testing.T, value tftypes.Value) {
	t.Helper()
	var actual bool
	if err := value.As(&actual); err != nil || actual {
		t.Fatalf("value was not known false: value=%v err=%v", value, err)
	}
}

func assertMCPProtocolEmptyCollection(t *testing.T, value tftypes.Value) {
	t.Helper()
	if !value.IsKnown() || value.IsNull() {
		t.Fatalf("empty collection was not known: %v", value)
	}
	switch value.Type().(type) {
	case tftypes.List, tftypes.Set, tftypes.Tuple:
		var values []tftypes.Value
		if err := value.As(&values); err != nil || len(values) != 0 {
			t.Fatalf("collection was not empty: len=%d err=%v", len(values), err)
		}
	case tftypes.Map, tftypes.Object:
		var values map[string]tftypes.Value
		if err := value.As(&values); err != nil || len(values) != 0 {
			t.Fatalf("collection was not empty: len=%d err=%v", len(values), err)
		}
	default:
		t.Fatalf("unexpected collection type %T", value.Type())
	}
}

func assertMCPDiagnosticsContentSafe(t *testing.T, diagnostics []*tfprotov6.Diagnostic, forbidden string) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic != nil && (strings.Contains(diagnostic.Summary, forbidden) || strings.Contains(diagnostic.Detail, forbidden)) {
			t.Fatalf("diagnostic disclosed response content: %#v", diagnostic)
		}
	}
}
