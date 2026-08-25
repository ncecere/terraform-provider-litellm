package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestMCPServerDataSourcesSupportV198TransportAlternativesProtocol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/mcp/server/spec-id":
			_, _ = writer.Write([]byte(`{"server_id":"spec-id","server_name":"spec","transport":"http","spec_path":"/srv/specs/tenant:a/openapi.json"}`))
		case "/v1/mcp/server":
			_, _ = writer.Write([]byte(`[{"server_id":"stdio-id","server_name":null,"transport":"stdio","command":"python3","args":["server.py"]}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	singleSchema := schemas.DataSourceSchemas["litellm_mcp_server"]
	singleConfig := accessGroupProtocolDynamicValue(t, singleSchema, organizationProjectProtocolValue(t, singleSchema, map[string]interface{}{"server_id": "spec-id"}))
	single, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_server", Config: singleConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(single.Diagnostics) {
		t.Fatalf("single read: err=%v diagnostics=%v", err, single.Diagnostics)
	}
	singleAttributes := protocolAttributeMap(t, singleSchema, single.State)
	var specPath string
	if err := singleAttributes["spec_path"].As(&specPath); err != nil || specPath != "/srv/specs/tenant:a/openapi.json" {
		t.Fatalf("single spec_path=%q err=%v", specPath, err)
	}
	if !singleAttributes["url"].IsNull() {
		t.Fatalf("spec-path-only single data source synthesized url: %v", singleAttributes["url"])
	}

	listSchema := schemas.DataSourceSchemas["litellm_mcp_servers"]
	listConfig := accessGroupProtocolDynamicValue(t, listSchema, organizationProjectProtocolValue(t, listSchema, map[string]interface{}{}))
	list, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_servers", Config: listConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(list.Diagnostics) {
		t.Fatalf("list read: err=%v diagnostics=%v", err, list.Diagnostics)
	}
	var items []tftypes.Value
	if err := protocolAttributeMap(t, listSchema, list.State)["mcp_servers"].As(&items); err != nil || len(items) != 1 {
		t.Fatalf("list items=%d err=%v", len(items), err)
	}
	item := map[string]tftypes.Value{}
	if err := items[0].As(&item); err != nil {
		t.Fatal(err)
	}
	var transport string
	if err := item["transport"].As(&transport); err != nil || transport != "stdio" {
		t.Fatalf("list transport=%q err=%v", transport, err)
	}
	if !item["url"].IsNull() || !item["spec_path"].IsNull() {
		t.Fatalf("URL-less stdio list item synthesized an endpoint: %#v", item)
	}
}

func TestMCPServerDataSourcesRejectMalformedOptionalFieldsProtocol(t *testing.T) {
	ctx := context.Background()
	var response atomic.Value
	response.Store(`{"server_id":"malformed","transport":"http","url":1}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(response.Load().(string)))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	singleSchema := schemas.DataSourceSchemas["litellm_mcp_server"]
	singleConfig := accessGroupProtocolDynamicValue(t, singleSchema, organizationProjectProtocolValue(t, singleSchema, map[string]interface{}{"server_id": "malformed"}))
	for name, payload := range map[string]string{
		"wrong endpoint type": `{"server_id":"malformed","transport":"http","url":1}`,
		"wrong list element":  `{"server_id":"malformed","transport":"http","url":"https://example.invalid","args":["ok",1]}`,
		"wrong map element":   `{"server_id":"malformed","transport":"http","url":"https://example.invalid","env":{"safe":1}}`,
		"wrong boolean":       `{"server_id":"malformed","transport":"http","url":"https://example.invalid","allow_all_keys":"false"}`,
	} {
		t.Run("single "+name, func(t *testing.T) {
			response.Store(payload)
			read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_server", Config: singleConfig})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("malformed value accepted: err=%v diagnostics=%v", err, read.Diagnostics)
			}
		})
	}
	listSchema := schemas.DataSourceSchemas["litellm_mcp_servers"]
	listConfig := accessGroupProtocolDynamicValue(t, listSchema, organizationProjectProtocolValue(t, listSchema, map[string]interface{}{}))
	for name, payload := range map[string]string{
		"wrong endpoint type": `[{"server_id":"malformed","transport":"http","url":1}]`,
		"wrong boolean":       `[{"server_id":"malformed","transport":"http","url":"https://example.invalid","allow_all_keys":"false"}]`,
	} {
		t.Run("list "+name, func(t *testing.T) {
			response.Store(payload)
			read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_servers", Config: listConfig})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("malformed value accepted: err=%v diagnostics=%v", err, read.Diagnostics)
			}
		})
	}
}

func TestMCPServerDataSourcesRejectMalformedRequiredShapesProtocol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v1/mcp/server/malformed" {
			_, _ = writer.Write([]byte(`{"server_id":"malformed"}`))
			return
		}
		_, _ = writer.Write([]byte(`[{"server_id":"malformed-list","transport":"websocket"}]`))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	singleSchema := schemas.DataSourceSchemas["litellm_mcp_server"]
	singleConfig := accessGroupProtocolDynamicValue(t, singleSchema, organizationProjectProtocolValue(t, singleSchema, map[string]interface{}{"server_id": "malformed"}))
	listSchema := schemas.DataSourceSchemas["litellm_mcp_servers"]
	listConfig := accessGroupProtocolDynamicValue(t, listSchema, organizationProjectProtocolValue(t, listSchema, map[string]interface{}{}))
	for name, request := range map[string]*tfprotov6.ReadDataSourceRequest{
		"single": {TypeName: "litellm_mcp_server", Config: singleConfig},
		"list":   {TypeName: "litellm_mcp_servers", Config: listConfig},
	} {
		t.Run(name, func(t *testing.T) {
			response, err := protocolServer.ReadDataSource(ctx, request)
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
				t.Fatalf("malformed response: err=%v diagnostics=%v", err, response.Diagnostics)
			}
		})
	}
}
