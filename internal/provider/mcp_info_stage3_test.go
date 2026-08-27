package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestMCPInfoStage3SelectiveResolutionIsLossless(t *testing.T) {
	base, err := parseMCPInfoJSONObject(`{"access":false,"owner":{"team":"security","contact":"old"},"items":[true,{"n":999999999999999999999999999999999999}],"nullable":null,"obsolete":"remove"}`)
	if err != nil {
		t.Fatal(err)
	}
	config := MCPServerResourceModel{
		MCPInfo:              &MCPInfoModel{Description: types.StringValue("managed")},
		MCPInfoOverridesJSON: types.StringValue(`{"owner":{"contact":null},"atomic":[]}`),
		MCPInfoClearPaths:    stringListValue("/obsolete"),
	}
	resolved, err := resolveMCPInfoUpdateDocument(context.Background(), base, config)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalMCPInfoJSONObject(resolved.Document)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"access":false,"atomic":[],"description":"managed","items":[true,{"n":999999999999999999999999999999999999}],"nullable":null,"owner":{"contact":null,"team":"security"}}`
	if canonical != want {
		t.Fatalf("resolved document = %s", canonical)
	}
	ownership := emptyMCPInfoProvenance()
	ownership.Mode = mcpInfoModeSelective
	ownership.Fixed[mcpInfoDescriptionPointer] = true
	ownership.Overrides["/atomic"] = true
	ownership.Overrides["/owner/contact"] = true
	ownership.Clears["/obsolete"] = true
	if err := verifyMCPInfoReadback(base, resolved.Document, resolved.Document, ownership); err != nil {
		t.Fatal(err)
	}
	changed := cloneMCPInfoJSONObject(resolved.Document)
	changed["items"] = []interface{}{false}
	if err := verifyMCPInfoReadback(base, resolved.Document, changed, ownership); err == nil {
		t.Fatal("unowned array mutation was accepted")
	}
}

func TestBuildMCPServerRequestUsesResolvedCompleteMCPInfo(t *testing.T) {
	document, _ := parseMCPInfoJSONObject(`{"unknown":{"owner":true},"array":[1,2],"null":null}`)
	request, err := (&MCPServerResource{}).buildMCPServerRequest(context.Background(), &MCPServerResourceModel{
		ServerName: types.StringValue("server"), Transport: types.StringValue("http"), AuthType: types.StringValue("none"),
	}, document, true)
	if err != nil {
		t.Fatal(err)
	}
	if !mcpInfoJSONValuesEqual(request["mcp_info"], document) {
		t.Fatalf("complete document was reconstructed or lost: %#v", request["mcp_info"])
	}
	empty, err := (&MCPServerResource{}).buildMCPServerRequest(context.Background(), &MCPServerResourceModel{
		ServerName: types.StringValue("server"), Transport: types.StringValue("http"), AuthType: types.StringValue("none"),
	}, map[string]interface{}{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if value, present := empty["mcp_info"]; !present || len(value.(map[string]interface{})) != 0 {
		t.Fatalf("explicit root empty object was omitted: %#v", empty)
	}
}

func TestMCPInfoStage3EqualWholeTakeoverCommitsWithoutPUTProtocol(t *testing.T) {
	ctx := context.Background()
	var puts int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPut {
			puts++
		}
		_, _ = writer.Write([]byte(`{"server_id":"takeover","server_name":"top","url":"https://mcp.example.test","transport":"http","auth_type":"none","mcp_info":{"owner":{"team":"security"}}}`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "takeover", "server_id": "takeover", "server_name": "top", "url": "https://mcp.example.test", "transport": "http", "auth_type": "none",
		"mcp_info_json": `{"owner":{"team":"security"}}`, "mcp_info_ownership_generation": int64(0),
	}))
	config := protocolMCPStage2Config(t, schema, map[string]interface{}{"mcp_info_json": `{ "owner" : { "team" : "security" } }`})
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"mcp_info_json": `{ "owner" : { "team" : "security" } }`, "mcp_info_ownership_generation": tftypes.UnknownValue})
	private := protocolMCPV2Private(t, emptyMCPInfoProvenance())
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: %v %s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || puts != 0 {
		t.Fatalf("apply: %v %s puts=%d", err, agentProtocolDiagnosticsText(applied.Diagnostics), puts)
	}
	committed, diagnostics := readMCPInfoProvenance(ctx, protocolPrivateMapFromBytes(t, applied.Private))
	if diagnostics.HasError() || committed.Mode != mcpInfoModeWhole || committed.Generation != 1 {
		t.Fatalf("committed provenance = %#v, %v", committed, diagnostics)
	}
}

func TestMCPInfoStage3MaskedHydrationFailsBeforePUTProtocol(t *testing.T) {
	ctx := context.Background()
	puts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPut {
			puts++
		}
		_, _ = writer.Write([]byte(`{"server_id":"masked","server_name":"top","url":"https://mcp.example.test","transport":"http","auth_type":"none","mcp_info":null}`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "masked", "server_id": "masked", "server_name": "top", "description": "old", "url": "https://mcp.example.test", "transport": "http", "auth_type": "none",
	}))
	config := protocolMCPStage2Config(t, schema, map[string]interface{}{"description": "new"})
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"description": "new"})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: %v %s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || puts != 0 {
		t.Fatalf("masked hydration: %v %s puts=%d", err, agentProtocolDiagnosticsText(applied.Diagnostics), puts)
	}
}

func TestMCPInfoDataSourceSchemaIsComputedSensitive(t *testing.T) {
	var resourceResponse resource.SchemaResponse
	(&MCPServerResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resourceResponse)
	if _, ok := resourceResponse.Schema.Attributes["mcp_info_json"].(resourceschema.StringAttribute); !ok {
		t.Fatal("resource schema sanity check failed")
	}
	// Protocol schema sensitivity is checked because it covers both singular and
	// nested list-item propagation as Terraform sees it.
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/v1/mcp/server/ds" {
			_, _ = writer.Write([]byte(`{"server_id":"ds","transport":"http","mcp_info":{"access":false,"owner":{"id":1},"items":[1,null],"huge":999999999999999999999999}}`))
			return
		}
		_, _ = writer.Write([]byte(`[{"server_id":"ds","transport":"http","mcp_info":{"access":false,"owner":{"id":1},"items":[1,null],"huge":999999999999999999999999}}]`))
	}))
	defer server.Close()
	var singleResponse datasource.SchemaResponse
	(&MCPServerDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &singleResponse)
	if attribute := singleResponse.Schema.Attributes["mcp_info_json"].(datasourceschema.StringAttribute); !attribute.Computed || !attribute.Sensitive {
		t.Fatal("singular mcp_info_json is not computed sensitive")
	}
	var listResponse datasource.SchemaResponse
	(&MCPServersListDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &listResponse)
	listAttribute := listResponse.Schema.Attributes["mcp_servers"].(datasourceschema.ListNestedAttribute)
	if attribute := listAttribute.NestedObject.Attributes["mcp_info_json"].(datasourceschema.StringAttribute); !attribute.Computed || !attribute.Sensitive {
		t.Fatal("list-item mcp_info_json is not computed sensitive")
	}
	protocolServer, schemas := configuredImportProtocolServer(t, context.Background(), server.URL)
	singleSchema := schemas.DataSourceSchemas["litellm_mcp_server"]
	listSchema := schemas.DataSourceSchemas["litellm_mcp_servers"]
	_ = listSchema
	singleConfig := accessGroupProtocolDynamicValue(t, singleSchema, organizationProjectProtocolValue(t, singleSchema, map[string]interface{}{"server_id": "ds"}))
	read, err := protocolServer.ReadDataSource(context.Background(), &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_server", Config: singleConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("single read: %v %s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	var document string
	if err := protocolAttributeMap(t, singleSchema, read.State)["mcp_info_json"].As(&document); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := decodeJSONUseNumber([]byte(document), &decoded); err != nil || decoded["access"] != false {
		t.Fatalf("lossless document = %s, %v", document, err)
	}
	if number := decoded["huge"].(json.Number).String(); number != "999999999999999999999999" {
		t.Fatalf("huge number = %s", number)
	}
}
