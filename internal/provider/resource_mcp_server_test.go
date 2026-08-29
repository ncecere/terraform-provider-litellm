package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestBuildMCPServerRequestOmitsUnsupportedPhantomFields(t *testing.T) {
	t.Parallel()

	r := &MCPServerResource{}
	data := &MCPServerResourceModel{
		ServerName:        types.StringValue("test-mcp"),
		URL:               types.StringValue("http://mcp.internal.svc.cluster.local:8000/mcp"),
		Transport:         types.StringValue("http"),
		SpecVersion:       types.StringValue("2024-11-05"),
		SkipURLValidation: types.BoolValue(false),
	}

	req, err := r.buildMCPServerRequest(context.Background(), data, nil, false)
	if err != nil {
		t.Fatal(err)
	}

	for _, field := range []string{"spec_version", "skip_url_validation"} {
		if _, ok := req[field]; ok {
			t.Fatalf("unsupported field %s must not be sent", field)
		}
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
		URL:             types.StringValue("http://mcp.internal/mcp"),
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

	req, err := r.buildMCPServerRequest(context.Background(), data, nil, false)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := req["skip_url_validation"]; ok {
		t.Fatalf("skip_url_validation should be omitted when unconfigured, got %v", req["skip_url_validation"])
	}
}

func mcpServerTestSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	response := &resource.SchemaResponse{}
	(&MCPServerResource{}).Schema(context.Background(), resource.SchemaRequest{}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func mcpServerTestConfig(t *testing.T, schema resourceschema.Schema, data MCPServerResourceModel) tfsdk.Config {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := state.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set config: %v", diagnostics)
	}
	return tfsdk.Config{Raw: state.Raw, Schema: schema}
}

func validateMCPServerTestConfig(t *testing.T, schema resourceschema.Schema, data MCPServerResourceModel) *resource.ValidateConfigResponse {
	t.Helper()
	response := &resource.ValidateConfigResponse{}
	(&MCPServerResource{}).ValidateConfig(context.Background(), resource.ValidateConfigRequest{
		Config: mcpServerTestConfig(t, schema, data),
	}, response)
	return response
}

func TestMCPServerTransportSchemaAndDeprecations(t *testing.T) {
	t.Parallel()
	schema := mcpServerTestSchema(t)
	if attribute := schema.Attributes["url"]; !attribute.IsOptional() || attribute.IsRequired() {
		t.Fatalf("url must remain string-typed but become optional: %#v", attribute)
	} else if _, ok := attribute.(resourceschema.StringAttribute); !ok {
		t.Fatalf("url type changed: %#v", attribute)
	}
	if attribute := schema.Attributes["spec_path"]; !attribute.IsOptional() || attribute.IsRequired() {
		t.Fatalf("spec_path must be optional: %#v", attribute)
	}
	specVersion := schema.Attributes["spec_version"].(resourceschema.StringAttribute)
	if specVersion.DeprecationMessage == "" || !specVersion.IsOptional() || !specVersion.IsComputed() {
		t.Fatalf("spec_version compatibility contract missing: %#v", specVersion)
	}
	skipValidation := schema.Attributes["skip_url_validation"].(resourceschema.BoolAttribute)
	if skipValidation.DeprecationMessage == "" || !skipValidation.IsOptional() {
		t.Fatalf("skip_url_validation compatibility contract missing: %#v", skipValidation)
	}
}

func TestMCPServerTransportConfigValidation(t *testing.T) {
	t.Parallel()
	schema := mcpServerTestSchema(t)
	base := func() MCPServerResourceModel {
		return MCPServerResourceModel{
			ServerName:                 types.StringValue("server"),
			AuthType:                   types.StringValue("none"),
			SpecVersion:                types.StringValue("2024-11-05"),
			SkipURLValidation:          types.BoolNull(),
			MCPAccessGroups:            types.ListNull(types.StringType),
			Args:                       types.ListNull(types.StringType),
			Env:                        types.MapNull(types.StringType),
			EnvVars:                    types.ListNull(mcpEnvVarObjectType),
			Credentials:                types.MapNull(types.StringType),
			AllowedTools:               types.ListNull(types.StringType),
			ExtraHeaders:               types.ListNull(types.StringType),
			StaticHeaders:              types.MapNull(types.StringType),
			OAuthScopes:                types.ListNull(types.StringType),
			AvailableOnPublicInternet:  types.BoolNull(),
			OAuth2Flow:                 types.StringNull(),
			Instructions:               types.StringNull(),
			ToolNameToDisplayName:      types.MapNull(types.StringType),
			ToolNameToDescription:      types.MapNull(types.StringType),
			BYOKDescription:            types.ListNull(types.StringType),
			MCPInfoJSON:                types.StringNull(),
			MCPInfoOverridesJSON:       types.StringNull(),
			MCPInfoClearPaths:          types.ListNull(types.StringType),
			MCPInfoOwnershipGeneration: types.Int64Value(0),
		}
	}

	valid := map[string]MCPServerResourceModel{}
	httpURL := base()
	httpURL.Transport = types.StringValue("http")
	httpURL.URL = types.StringValue("https://example.invalid/mcp")
	valid["http url"] = httpURL
	httpSpec := base()
	httpSpec.Transport = types.StringValue("http")
	httpSpec.SpecPath = types.StringValue("/srv/specs/service:admin/openapi.json")
	valid["http spec path"] = httpSpec
	sseBoth := base()
	sseBoth.Transport = types.StringValue("sse")
	sseBoth.URL = types.StringValue("https://example.invalid/sse")
	sseBoth.SpecPath = types.StringValue("https://example.invalid/openapi.json")
	valid["sse both alternatives"] = sseBoth
	legacyPhantoms := httpURL
	legacyPhantoms.SpecVersion = types.StringValue("2025-06-18")
	legacyPhantoms.SkipURLValidation = types.BoolValue(true)
	valid["state-aware phantom values"] = legacyPhantoms
	stdio := base()
	stdio.Transport = types.StringValue("stdio")
	stdio.Command = types.StringValue("/usr/local/bin/python3")
	stdio.Args = stringListValue("-m", "server")
	valid["url-less stdio"] = stdio

	for name, model := range valid {
		model := model
		t.Run(name, func(t *testing.T) {
			if response := validateMCPServerTestConfig(t, schema, model); response.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
			}
		})
	}

	invalid := map[string]MCPServerResourceModel{}
	missingEndpoint := base()
	missingEndpoint.Transport = types.StringValue("http")
	invalid["http missing alternatives"] = missingEndpoint
	missingCommand := base()
	missingCommand.Transport = types.StringValue("stdio")
	missingCommand.Args = stringListValue("server.py")
	invalid["stdio missing command"] = missingCommand
	missingArgs := base()
	missingArgs.Transport = types.StringValue("stdio")
	missingArgs.Command = types.StringValue("python")
	missingArgs.Args = stringListValue()
	invalid["stdio missing args"] = missingArgs
	disallowed := base()
	disallowed.Transport = types.StringValue("stdio")
	disallowed.Command = types.StringValue("/private/configured/path/secret-runner")
	disallowed.Args = stringListValue("serve")
	invalid["stdio disallowed command"] = disallowed
	trailingSlash := base()
	trailingSlash.Transport = types.StringValue("stdio")
	trailingSlash.Command = types.StringValue("/usr/bin/python3/")
	trailingSlash.Args = stringListValue("server.py")
	invalid["stdio Python basename semantics"] = trailingSlash
	emptyURL := httpSpec
	emptyURL.URL = types.StringValue("")
	invalid["configured empty URL"] = emptyURL
	emptySpec := httpURL
	emptySpec.SpecPath = types.StringValue("")
	invalid["configured empty spec path"] = emptySpec

	for name, model := range invalid {
		model := model
		t.Run(name, func(t *testing.T) {
			response := validateMCPServerTestConfig(t, schema, model)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected validation error")
			}
			for _, diagnostic := range response.Diagnostics {
				if strings.Contains(diagnostic.Detail(), "secret-runner") || strings.Contains(diagnostic.Detail(), "/private/configured") {
					t.Fatalf("diagnostic exposed configured content: %s", diagnostic.Detail())
				}
			}
		})
	}
}

func TestMCPServerTransportValidationRunsAtProtocolConfigTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]

	valid := []map[string]interface{}{
		{"server_name": "url", "transport": "http", "url": "https://example.invalid/mcp"},
		{"server_name": "spec", "transport": "sse", "spec_path": "/srv/a:b/openapi.json"},
		{"server_name": "stdio", "transport": "stdio", "command": "/usr/bin/python3", "args": []tftypes.Value{tftypes.NewValue(tftypes.String, "server.py")}},
		{"server_name": "legacy", "transport": "http", "url": "https://example.invalid", "spec_version": "2025-06-18", "skip_url_validation": true},
	}
	for i, values := range valid {
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
		validated, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: "litellm_mcp_server", Config: config})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(validated.Diagnostics) {
			t.Fatalf("valid case %d: err=%v diagnostics=%v", i, err, validated.Diagnostics)
		}
	}

	const secretCommand = "/private/configured/secret-runner"
	const secretAuth = "configured-secret-auth"
	const secretTransport = "configured-secret-transport"
	invalid := []map[string]interface{}{
		{"server_name": "missing", "transport": "http"},
		{"server_name": "args", "transport": "stdio", "command": "python3"},
		{"server_name": "command", "transport": "stdio", "command": secretCommand, "args": []tftypes.Value{tftypes.NewValue(tftypes.String, "serve")}},
		{"server_name": "auth", "transport": "http", "url": "https://example.invalid", "auth_type": secretAuth},
		{"server_name": "empty-url", "transport": "http", "url": "", "spec_path": "/valid/spec.json"},
		{"server_name": "empty-spec", "transport": "http", "url": "https://example.invalid", "spec_path": ""},
		{"server_name": "transport", "transport": secretTransport},
	}
	for i, values := range invalid {
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
		validated, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: "litellm_mcp_server", Config: config})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(validated.Diagnostics) {
			t.Fatalf("invalid case %d: err=%v diagnostics=%v", i, err, validated.Diagnostics)
		}
		diagnostics := fmt.Sprint(validated.Diagnostics)
		if strings.Contains(diagnostics, secretCommand) || strings.Contains(diagnostics, secretAuth) || strings.Contains(diagnostics, secretTransport) {
			t.Fatalf("invalid case %d exposed configured content", i)
		}
	}
}

func TestMCPServerStdioCommandAllowlistMatchesV198(t *testing.T) {
	t.Parallel()
	expected := []string{"deno", "docker", "node", "npx", "python", "python3", "uvx"}
	for _, command := range expected {
		if _, ok := mcpStdioAllowedCommandsV198[command]; !ok {
			t.Fatalf("missing v1.98 stdio command %q", command)
		}
	}
	if len(mcpStdioAllowedCommandsV198) != len(expected) {
		t.Fatalf("stdio allowlist = %#v, want exact built-in v1.98 values", mcpStdioAllowedCommandsV198)
	}
}

func TestBuildMCPServerRequestTransportAlternatives(t *testing.T) {
	t.Parallel()
	r := &MCPServerResource{}

	stdioRequest, err := r.buildMCPServerRequest(context.Background(), &MCPServerResourceModel{
		ServerName:  types.StringValue("stdio"),
		Transport:   types.StringValue("stdio"),
		AuthType:    types.StringValue("none"),
		Command:     types.StringValue("python3"),
		Args:        stringListValue("/srv/a:b/server.py"),
		SpecVersion: types.StringValue("2024-11-05"),
	}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stdioRequest["url"]; ok {
		t.Fatal("URL-less stdio payload unexpectedly contains url")
	}
	if _, ok := stdioRequest["spec_version"]; ok {
		t.Fatal("stdio payload unexpectedly contains phantom spec_version")
	}

	specialSpecPath := "/srv/openapi/tenant:a/service/b.json"
	specRequest, err := r.buildMCPServerRequest(context.Background(), &MCPServerResourceModel{
		ServerName: types.StringValue("openapi"),
		Transport:  types.StringValue("http"),
		AuthType:   types.StringValue("none"),
		SpecPath:   types.StringValue(specialSpecPath),
	}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := specRequest["spec_path"]; got != specialSpecPath {
		t.Fatalf("spec_path payload changed: %#v", got)
	}
	if _, ok := specRequest["url"]; ok {
		t.Fatal("spec-path-only payload unexpectedly contains url")
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

	req, err := r.buildMCPServerRequest(context.Background(), data, nil, false)
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

func TestReadMCPServerPreservesUnknownNestedToolCostMapOnAPIOmission(t *testing.T) {
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

	ownership := emptyMCPInfoProvenance()
	ownership.API[mcpInfoToolCostsLeaf] = true
	if _, _, err := r.readMCPServerWithProvenance(context.Background(), &data, ownership, false); err != nil {
		t.Fatalf("readMCPServer returned error: %v", err)
	}

	if data.MCPInfo == nil || data.MCPInfo.MCPServerCostInfo == nil {
		t.Fatal("mcp_info.mcp_server_cost_info should be present after read")
	}
	if !data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsUnknown() {
		t.Fatal("API omission must preserve the prior tool_name_to_cost_per_query value")
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

func TestReadMCPServerPreservesPhantomMigrationAndOptionalOwnership(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"server_id": "migration-1", "server_name": "migration", "transport": "http",
			"url": "https://example.invalid/mcp", "spec_path": "/remote/unowned/spec.json",
			"spec_version": "2025-06-18", "skip_url_validation": true,
		})
	}))
	defer server.Close()

	resource := &MCPServerResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := MCPServerResourceModel{
		ID: types.StringValue("migration-1"), ServerID: types.StringValue("migration-1"),
		URL: types.StringValue("https://example.invalid/mcp"), SpecPath: types.StringNull(),
		SpecVersion: types.StringValue("2024-11-05"), SkipURLValidation: types.BoolValue(false),
	}
	if err := resource.readMCPServer(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	if data.SpecVersion.ValueString() != "2024-11-05" || data.SkipURLValidation.ValueBool() {
		t.Fatalf("phantom compatibility state changed during read: %#v", data)
	}
	if !data.SpecPath.IsNull() {
		t.Fatalf("ordinary read adopted unconfigured optional spec_path: %#v", data.SpecPath)
	}
}

func TestReadMCPServerKnownEndpointsSurviveRestrictedNullOrOmission(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		stateURL     types.String
		stateSpec    types.String
		response     map[string]interface{}
		wantURL      types.String
		wantSpecPath types.String
	}{
		{
			name: "owned URL-only", stateURL: types.StringValue("https://configured.invalid/mcp"), stateSpec: types.StringNull(),
			response: map[string]interface{}{"server_id": "owned", "transport": "http", "url": "https://remote.invalid/mcp"},
			wantURL:  types.StringValue("https://remote.invalid/mcp"), wantSpecPath: types.StringNull(),
		},
		{
			name: "owned spec-only", stateURL: types.StringNull(), stateSpec: types.StringValue("/configured/spec.json"),
			response: map[string]interface{}{"server_id": "owned", "transport": "http", "spec_path": "/remote/spec.json"},
			wantURL:  types.StringNull(), wantSpecPath: types.StringValue("/remote/spec.json"),
		},
		{
			name: "owned both", stateURL: types.StringValue("https://configured.invalid/mcp"), stateSpec: types.StringValue("/configured/spec.json"),
			response: map[string]interface{}{"server_id": "owned", "transport": "http", "url": "https://remote.invalid/mcp", "spec_path": "/remote/spec.json"},
			wantURL:  types.StringValue("https://remote.invalid/mcp"), wantSpecPath: types.StringValue("/remote/spec.json"),
		},
		{
			name: "owned URL omitted", stateURL: types.StringValue("https://configured.invalid/mcp"), stateSpec: types.StringNull(),
			response: map[string]interface{}{"server_id": "owned", "transport": "http", "spec_path": "/unowned/remote.json"},
			wantURL:  types.StringValue("https://configured.invalid/mcp"), wantSpecPath: types.StringNull(),
		},
		{
			name: "owned spec explicit null", stateURL: types.StringNull(), stateSpec: types.StringValue("/configured/spec.json"),
			response: map[string]interface{}{"server_id": "owned", "transport": "http", "url": "https://unowned.invalid/mcp", "spec_path": nil},
			wantURL:  types.StringNull(), wantSpecPath: types.StringValue("/configured/spec.json"),
		},
		{
			name: "owned both with spec omitted", stateURL: types.StringValue("https://configured.invalid/mcp"), stateSpec: types.StringValue("/configured/spec.json"),
			response: map[string]interface{}{"server_id": "owned", "transport": "http", "url": "https://remote.invalid/mcp"},
			wantURL:  types.StringValue("https://remote.invalid/mcp"), wantSpecPath: types.StringValue("/configured/spec.json"),
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(writer).Encode(test.response)
			}))
			defer server.Close()
			resource := &MCPServerResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
			data := MCPServerResourceModel{ID: types.StringValue("owned"), ServerID: types.StringValue("owned"), URL: test.stateURL, SpecPath: test.stateSpec}
			if err := resource.readMCPServer(context.Background(), &data); err != nil {
				t.Fatal(err)
			}
			if !data.URL.Equal(test.wantURL) || !data.SpecPath.Equal(test.wantSpecPath) {
				t.Fatalf("endpoints: url=%v spec_path=%v, want url=%v spec_path=%v", data.URL, data.SpecPath, test.wantURL, test.wantSpecPath)
			}
		})
	}
}

func TestReadMCPServerSupportsURLlessStdioAndSpecPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		response map[string]interface{}
		check    func(*testing.T, MCPServerResourceModel)
	}{
		{
			name: "url-less stdio",
			response: map[string]interface{}{
				"server_id": "stdio-1", "server_name": "stdio", "transport": "stdio",
				"command": "/usr/bin/python3", "args": []interface{}{"server.py"},
			},
			check: func(t *testing.T, data MCPServerResourceModel) {
				if !data.URL.IsNull() || data.Command.ValueString() != "/usr/bin/python3" {
					t.Fatalf("unexpected stdio state: %#v", data)
				}
			},
		},
		{
			name: "spec-path only",
			response: map[string]interface{}{
				"server_id": "spec-1", "server_name": "spec", "transport": "http",
				"spec_path": "/srv/specs/tenant:a/service/openapi.json",
			},
			check: func(t *testing.T, data MCPServerResourceModel) {
				if !data.URL.IsNull() || data.SpecPath.ValueString() != "/srv/specs/tenant:a/service/openapi.json" {
					t.Fatalf("unexpected spec-path state: %#v", data)
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			serverID := test.response["server_id"].(string)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(test.response)
			}))
			defer server.Close()
			resource := &MCPServerResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
			data := MCPServerResourceModel{ID: types.StringValue(serverID), ServerID: types.StringValue(serverID), URL: types.StringNull(), SpecPath: types.StringNull(), Command: types.StringNull()}
			if err := resource.readMCPServerWithNumericOwnership(context.Background(), &data, true); err != nil {
				t.Fatalf("read: %v", err)
			}
			test.check(t, data)
		})
	}
}

func TestValidateMCPServerResponseRejectsMalformedRequiredShapes(t *testing.T) {
	t.Parallel()
	tests := []map[string]interface{}{
		{},
		{"server_id": "wrong", "transport": "http", "url": "https://example.invalid"},
		{"server_id": "expected"},
		{"server_id": "expected", "transport": "websocket", "url": "https://example.invalid"},
	}
	for i, response := range tests {
		if err := validateMCPServerResponse(response, "expected"); err == nil {
			t.Fatalf("case %d unexpectedly accepted", i)
		} else if strings.Contains(err.Error(), "https://") || strings.Contains(err.Error(), "wrong") {
			t.Fatalf("case %d error exposed response content: %v", i, err)
		}
	}
	for _, response := range []map[string]interface{}{
		{"server_id": "expected", "transport": "http"},
		{"server_id": "expected", "transport": "stdio"},
	} {
		if err := validateMCPServerResponse(response, "expected"); err != nil {
			t.Fatalf("valid nullable response shape rejected: %v", err)
		}
	}
}

func TestMCPServerSlashIDFailsBeforeDispatch(t *testing.T) {
	t.Parallel()
	serverID := "tenant:admin/private?revision=1"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()

	resource := &MCPServerResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	_, err := resource.getMCPServer(context.Background(), serverID)
	if err == nil || requests != 0 {
		t.Fatalf("slash ID result: err=%v requests=%d", err, requests)
	}
	if strings.Contains(err.Error(), serverID) || strings.Contains(err.Error(), url.PathEscape(serverID)) {
		t.Fatalf("slash ID diagnostic exposed identity: %q", err)
	}
}
