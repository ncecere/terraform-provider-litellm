package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestMCPInfoStage3ImportTwoRefreshesAndNoConfigNoDriftProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"server_id":"import-json","server_name":"top","url":"https://mcp.example.test","transport":"http","auth_type":"none","mcp_info":{"access":true,"server_name":42,"mcp_server_cost_info":[],"owner":{"id":999999999999999999999999999999},"items":[false,null]}}`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "import-json"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) {
		t.Fatalf("import: %v %s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	first, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(first.Diagnostics) {
		t.Fatalf("first refresh: %v %s", err, agentProtocolDiagnosticsText(first.Diagnostics))
	}
	if protocolPrivateHasKey(t, first.Private, numericImportedPrivateKey) {
		t.Fatal("import marker survived authoritative complete-object read")
	}
	attributes := protocolAttributeMap(t, schema, first.NewState)
	if attributes["mcp_info"].IsKnown() && !attributes["mcp_info"].IsNull() {
		t.Fatal("arbitrarily typed unowned known members were projected into the fixed block")
	}
	var firstJSON string
	if err := attributes["mcp_info_json"].As(&firstJSON); err != nil {
		t.Fatal(err)
	}
	second, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: first.NewState, Private: first.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(second.Diagnostics) {
		t.Fatalf("second refresh: %v %s", err, agentProtocolDiagnosticsText(second.Diagnostics))
	}
	var secondJSON string
	if err := protocolAttributeMap(t, schema, second.NewState)["mcp_info_json"].As(&secondJSON); err != nil || secondJSON != firstJSON {
		t.Fatalf("refresh JSON drift: %q -> %q, %v", firstJSON, secondJSON, err)
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"server_name": "top", "url": "https://mcp.example.test", "transport": "http",
	}))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: second.NewState, ProposedNewState: second.NewState, PriorPrivate: second.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("no-config plan: %v %s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	priorAttributes := protocolAttributeMap(t, schema, second.NewState)
	plannedAttributes := protocolAttributeMap(t, schema, planned.PlannedState)
	for _, name := range []string{"mcp_info_json", "mcp_info", "mcp_info_ownership_generation"} {
		if !priorAttributes[name].Equal(plannedAttributes[name]) {
			t.Fatalf("no-config MCP drift in %s: %s -> %s", name, priorAttributes[name], plannedAttributes[name])
		}
	}
}

func TestMCPInfoStage3MaskedDataSourcesPublishTypedNullProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v1/mcp/server/masked" {
			_, _ = writer.Write([]byte(`{"server_id":"masked","transport":"http","mcp_info":{"is_public":true}}`))
			return
		}
		_, _ = writer.Write([]byte(`[{"server_id":"masked","transport":"http","mcp_info":{"is_public":true}}]`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)

	singleSchema := schemas.DataSourceSchemas["litellm_mcp_server"]
	singleConfig := accessGroupProtocolDynamicValue(t, singleSchema, organizationProjectProtocolValue(t, singleSchema, map[string]interface{}{"server_id": "masked"}))
	single, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_server", Config: singleConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(single.Diagnostics) {
		t.Fatalf("single masked read: %v %s", err, agentProtocolDiagnosticsText(single.Diagnostics))
	}
	if !protocolAttributeMap(t, singleSchema, single.State)["mcp_info_json"].IsNull() {
		t.Fatal("singular data source published a masked MCP info subset")
	}

	listSchema := schemas.DataSourceSchemas["litellm_mcp_servers"]
	listConfig := accessGroupProtocolDynamicValue(t, listSchema, organizationProjectProtocolValue(t, listSchema, map[string]interface{}{}))
	list, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_servers", Config: listConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(list.Diagnostics) {
		t.Fatalf("list masked read: %v %s", err, agentProtocolDiagnosticsText(list.Diagnostics))
	}
	var items []tftypes.Value
	if err := protocolAttributeMap(t, listSchema, list.State)["mcp_servers"].As(&items); err != nil || len(items) != 1 {
		t.Fatalf("masked list items: %v len=%d", err, len(items))
	}
	item := map[string]tftypes.Value{}
	if err := items[0].As(&item); err != nil || !item["mcp_info_json"].IsNull() {
		t.Fatalf("list data source published a masked MCP info subset: %v", err)
	}
}

func TestMCPInfoStage3ListDataSourceLosslessProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[{"server_id":"list-json","transport":"http","mcp_info":{"access":false,"owner":{"name":"ops"},"items":[1,null],"huge":999999999999999999999999}}]`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.DataSourceSchemas["litellm_mcp_servers"]
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{}))
	read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_servers", Config: config})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("list read: %v %s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	var items []tftypes.Value
	if err := protocolAttributeMap(t, schema, read.State)["mcp_servers"].As(&items); err != nil || len(items) != 1 {
		t.Fatalf("items: %v len=%d", err, len(items))
	}
	item := map[string]tftypes.Value{}
	if err := items[0].As(&item); err != nil {
		t.Fatal(err)
	}
	var document string
	if err := item["mcp_info_json"].As(&document); err != nil || document != `{"access":false,"huge":999999999999999999999999,"items":[1,null],"owner":{"name":"ops"}}` {
		t.Fatalf("list complete JSON = %q, %v", document, err)
	}
}
