package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	frameworkdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	frameworkvalidator "github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestMCP178SchemaContracts(t *testing.T) {
	ctx := context.Background()
	var resourceResponse frameworkresource.SchemaResponse
	(&MCPServerResource{}).Schema(ctx, frameworkresource.SchemaRequest{}, &resourceResponse)
	if resourceResponse.Schema.Version != 7 {
		t.Fatalf("resource schema version = %d", resourceResponse.Schema.Version)
	}
	scopes, ok := resourceResponse.Schema.Attributes["oauth_scopes"].(resourceschema.ListAttribute)
	if !ok || !scopes.Optional || scopes.Computed || !scopes.Sensitive || scopes.ElementType != types.StringType {
		t.Fatalf("oauth_scopes schema = %#v", resourceResponse.Schema.Attributes["oauth_scopes"])
	}
	for _, name := range []string{"available_on_public_internet", "oauth2_flow", "instructions", "tool_name_to_display_name", "tool_name_to_description"} {
		attribute := resourceResponse.Schema.Attributes[name]
		if !attribute.IsOptional() || !attribute.IsComputed() || attribute.IsSensitive() {
			t.Fatalf("resource %s flags are invalid: %#v", name, attribute)
		}
	}
	for _, name := range []string{"tool_name_to_display_name", "tool_name_to_description"} {
		attribute, ok := resourceResponse.Schema.Attributes[name].(resourceschema.MapAttribute)
		if !ok || attribute.ElementType != types.StringType {
			t.Fatalf("resource %s type is invalid: %#v", name, attribute)
		}
	}

	var singularResponse frameworkdatasource.SchemaResponse
	(&MCPServerDataSource{}).Schema(ctx, frameworkdatasource.SchemaRequest{}, &singularResponse)
	for _, name := range []string{"available_on_public_internet", "oauth2_flow", "instructions", "tool_name_to_display_name", "tool_name_to_description"} {
		if !singularResponse.Schema.Attributes[name].IsComputed() || singularResponse.Schema.Attributes[name].IsOptional() {
			t.Fatalf("singular data source %s flags are invalid", name)
		}
	}
	var listResponse frameworkdatasource.SchemaResponse
	(&MCPServersListDataSource{}).Schema(ctx, frameworkdatasource.SchemaRequest{}, &listResponse)
	servers := listResponse.Schema.Attributes["mcp_servers"].(datasourceschema.ListNestedAttribute)
	for _, name := range []string{"available_on_public_internet", "oauth2_flow", "instructions", "tool_name_to_display_name", "tool_name_to_description"} {
		if !servers.NestedObject.Attributes[name].IsComputed() {
			t.Fatalf("list data source %s is not computed", name)
		}
	}
	if _, present := singularResponse.Schema.Attributes["oauth_scopes"]; present {
		t.Fatal("write-only oauth_scopes leaked into the singular data source")
	}
	if _, present := servers.NestedObject.Attributes["oauth_scopes"]; present {
		t.Fatal("write-only oauth_scopes leaked into the list data source")
	}
}

func TestMCP178DisplayMapValidatorIsExactAndValueFree(t *testing.T) {
	ctx := context.Background()
	validator := mcpToolNameMapValidator{validateDisplayNames: true}
	for _, value := range []string{"", "Name_1", "name-with-hyphen"} {
		var response frameworkvalidator.MapResponse
		validator.ValidateMap(ctx, frameworkvalidator.MapRequest{ConfigValue: stringMapValue(map[string]string{"tool": value})}, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("valid display name rejected: %v", response.Diagnostics)
		}
	}
	const secretKey = "secret-map-key"
	const secretValue = "secret value with spaces"
	var response frameworkvalidator.MapResponse
	validator.ValidateMap(ctx, frameworkvalidator.MapRequest{ConfigValue: stringMapValue(map[string]string{secretKey: secretValue})}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("invalid display name was accepted")
	}
	text := response.Diagnostics.Errors()[0].Summary() + response.Diagnostics.Errors()[0].Detail()
	if strings.Contains(text, secretKey) || strings.Contains(text, secretValue) {
		t.Fatalf("validator diagnostic exposed a map key or value: %q", text)
	}
}

func TestMCP178DirectV0ThroughV4StateUpgradeAddsOnlyNullFields(t *testing.T) {
	ctx := context.Background()
	upgraders := (&MCPServerResource{}).UpgradeState(ctx)
	for _, version := range []int64{0, 1, 2, 3, 4} {
		prior := map[string]interface{}{
			"id": "stable", "server_id": "stable", "transport": "http", "description": "preserved",
			"field_ownership_generation": float64(9),
		}
		if version == 0 {
			prior["extra_headers"] = map[string]string{"X-B": "ignored", "X-A": "ignored"}
		} else {
			prior["extra_headers"] = []string{"X-B", "X-A"}
		}
		raw, _ := json.Marshal(prior)
		response := frameworkresource.UpgradeStateResponse{}
		upgraders[version].StateUpgrader(ctx, frameworkresource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: raw}}, &response)
		if response.Diagnostics.HasError() || response.DynamicValue == nil {
			t.Fatalf("v%d upgrade failed: %v", version, response.Diagnostics)
		}
		var upgraded map[string]interface{}
		if err := json.Unmarshal(response.DynamicValue.JSON, &upgraded); err != nil {
			t.Fatal(err)
		}
		if upgraded["id"] != "stable" || upgraded["server_id"] != "stable" || upgraded["description"] != "preserved" {
			t.Fatalf("v%d changed existing values: %#v", version, upgraded)
		}
		if upgraded["field_ownership_generation"] != float64(9) {
			t.Fatalf("v%d changed ownership generation: %#v", version, upgraded)
		}
		for _, name := range []string{"oauth_scopes", "available_on_public_internet", "oauth2_flow", "instructions", "tool_name_to_display_name", "tool_name_to_description"} {
			if value, present := upgraded[name]; !present || value != nil {
				t.Fatalf("v%d %s = %#v present=%t", version, name, value, present)
			}
		}
	}
}

func TestMCP178CreatePayloadCombinesNativeScopesAndExactZeroValues(t *testing.T) {
	emptyMap := types.MapValueMust(types.StringType, nil)
	config := MCPServerResourceModel{
		ServerName: types.StringValue("server"), Transport: types.StringValue("http"), AuthType: types.StringValue("oauth2"), URL: types.StringValue("https://example.invalid/mcp"),
		Credentials: stringMapValue(map[string]string{"client_id": "client"}),
	}
	config.OAuthScopes = stringListValue("scope-a", "scope-b")
	config.AvailableOnPublicInternet = types.BoolValue(false)
	config.OAuth2Flow = types.StringValue("client_credentials")
	config.Instructions = types.StringValue("")
	config.ToolNameToDisplayName = emptyMap
	config.ToolNameToDescription = emptyMap
	request, err := (&MCPServerResource{}).buildMCPServerCreateRequest(context.Background(), &config, &config, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	credentials, ok := request["credentials"].(map[string]interface{})
	if !ok || credentials["client_id"] != "client" || !mcpWireValuesEqual(credentials["scopes"], []string{"scope-a", "scope-b"}) {
		t.Fatalf("native credential payload = %#v", request["credentials"])
	}
	if request["available_on_public_internet"] != false || request["instructions"] != "" || !mcpWireValuesEqual(request["tool_name_to_display_name"], map[string]string{}) || !mcpWireValuesEqual(request["tool_name_to_description"], map[string]string{}) {
		t.Fatalf("exact zero-value payload = %#v", request)
	}
	config.Credentials = stringMapValue(map[string]string{"scopes": "collision"})
	if _, err := (&MCPServerResource{}).buildMCPServerCreateRequest(context.Background(), &config, &config, nil, false); err == nil {
		t.Fatal("generic credentials scopes alias was accepted")
	}
}

func TestMCP178ScopeDeltaPreservationClearAndCredentialSafety(t *testing.T) {
	ctx := context.Background()
	state := MCPServerResourceModel{OAuthScopes: stringListValue("old"), Credentials: types.MapNull(types.StringType)}
	committed := mcpFieldOwnership{Owned: map[string]bool{mcpFieldOAuthScopesPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
	config := MCPServerResourceModel{OAuthScopes: stringListValue("new")}
	candidate := deriveMCPFieldPlanOwnership(committed, config)
	delta, err := buildMCPFieldDelta(ctx, config, config, state, committed, candidate, map[string]interface{}{}, false)
	if err != nil {
		t.Fatal(err)
	}
	credentials := delta["credentials"].(map[string]interface{})
	if !mcpWireValuesEqual(credentials["scopes"], []string{"new"}) {
		t.Fatalf("scope change delta = %#v", delta)
	}
	if err := verifyMCPFieldUpdateReadback(ctx, config, config, committed, candidate, map[string]interface{}{}, map[string]interface{}{}, delta); err != nil {
		t.Fatalf("unobservable scope confirmation boundary failed: %v", err)
	}

	removed := deriveMCPFieldPlanOwnership(committed, MCPServerResourceModel{})
	clearDelta, err := buildMCPFieldDelta(ctx, MCPServerResourceModel{}, MCPServerResourceModel{}, state, committed, removed, map[string]interface{}{}, false)
	if err != nil || !mcpWireValuesEqual(clearDelta["credentials"], map[string]interface{}{"scopes": []string{}}) {
		t.Fatalf("scope clear delta = %#v err=%v", clearDelta, err)
	}

	credentialsOwned := mcpFieldOwnership{Owned: map[string]bool{mcpFieldCredentialsPath: true, mcpFieldOAuthScopesPath: true}, Removals: map[string]bool{}, Generation: 2, Versioned: true}
	unsafeCandidate := cloneMCPFieldOwnership(credentialsOwned)
	delete(unsafeCandidate.Owned, mcpFieldCredentialsPath)
	unsafeCandidate.Removals[mcpFieldCredentialsPath] = true
	unsafeState := state
	unsafeState.Credentials = stringMapValue(map[string]string{})
	if _, err := buildMCPFieldDelta(ctx, config, config, unsafeState, credentialsOwned, unsafeCandidate, map[string]interface{}{}, false); err == nil {
		t.Fatal("generic credentials clear was allowed to discard retained scopes")
	}
	emptyScopeConfig := MCPServerResourceModel{OAuthScopes: stringListValue()}
	allowed, err := buildMCPFieldDelta(ctx, emptyScopeConfig, emptyScopeConfig, unsafeState, credentialsOwned, unsafeCandidate, map[string]interface{}{}, false)
	if err != nil || allowed["credentials"] != nil {
		t.Fatalf("combined generic/scopes clear = %#v err=%v", allowed, err)
	}
}

func TestMCP178ResourceAndDataSourceProjectionParityAndTransactions(t *testing.T) {
	ctx := context.Background()
	response := map[string]interface{}{
		"server_id": "server", "transport": "http", "available_on_public_internet": false,
		"oauth2_flow": "authorization_code", "instructions": "",
		"tool_name_to_display_name": map[string]interface{}{"tool": ""},
		"tool_name_to_description":  map[string]interface{}{"tool": "arbitrary description"},
	}
	ownership := mcpFieldOwnership{Owned: map[string]bool{
		mcpFieldOAuthScopesPath: true, mcpFieldAvailablePublicInternetPath: true, mcpFieldOAuth2FlowPath: true,
		mcpFieldInstructionsPath: true, mcpFieldToolNameToDisplayNamePath: true, mcpFieldToolNameToDescriptionPath: true,
	}, Removals: map[string]bool{}, Versioned: true}
	data := MCPServerResourceModel{
		ID: types.StringValue("server"), ServerID: types.StringValue("server"), OAuthScopes: stringListValue("secret-scope"),
		AvailableOnPublicInternet: types.BoolUnknown(), OAuth2Flow: types.StringUnknown(), Instructions: types.StringUnknown(),
		ToolNameToDisplayName: types.MapUnknown(types.StringType), ToolNameToDescription: types.MapUnknown(types.StringType),
	}
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &data, response, emptyMCPInfoProvenance(), ownership, false, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatal(err)
	}
	if data.OAuthScopes.Elements()[0].(types.String).ValueString() != "secret-scope" || data.AvailableOnPublicInternet.ValueBool() || data.OAuth2Flow.ValueString() != "authorization_code" || data.Instructions.ValueString() != "" {
		t.Fatalf("resource projection = %#v", data)
	}
	singular, err := projectMCPServerDataSource(response, "server")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := projectMCPServerManagerListDataSource(response, "server")
	if err != nil {
		t.Fatal(err)
	}
	if !singular.AvailableOnPublicInternet.Equal(listed.AvailableOnPublicInternet) || !singular.OAuth2Flow.Equal(listed.OAuth2Flow) || !singular.Instructions.Equal(listed.Instructions) || !singular.ToolNameToDisplayName.Equal(listed.ToolNameToDisplayName) || !singular.ToolNameToDescription.Equal(listed.ToolNameToDescription) {
		t.Fatalf("singular/list projection mismatch: %#v %#v", singular, listed)
	}

	prior := data
	malformed := map[string]interface{}{}
	for name, value := range response {
		malformed[name] = value
	}
	malformed["tool_name_to_description"] = map[string]interface{}{"private-key": false}
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &data, malformed, emptyMCPInfoProvenance(), ownership, false, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err == nil {
		t.Fatal("malformed map member was accepted")
	}
	if !reflect.DeepEqual(prior, data) {
		t.Fatal("failed projection partially changed resource state")
	}
}

func TestMCP178ImportAdoptsObservableFieldsButNeverScopesProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"server_id":"import-fields","server_name":"import_fields","transport":"http","url":"https://example.invalid/mcp","auth_type":"oauth2","available_on_public_internet":false,"oauth2_flow":"authorization_code","instructions":"","tool_name_to_display_name":{"tool":"Display"},"tool_name_to_description":{"tool":"description"},"credentials":null,"mcp_info":{}}`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_mcp_server", ID: "import-fields"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schema, read.NewState)
	if !attributes["oauth_scopes"].IsNull() {
		t.Fatal("import invented observable OAuth scopes")
	}
	var public bool
	if err := attributes["available_on_public_internet"].As(&public); err != nil || public {
		t.Fatalf("imported public flag = %v err=%v", public, err)
	}
	if protocolString(t, attributes["oauth2_flow"]) != "authorization_code" || protocolString(t, attributes["instructions"]) != "" {
		t.Fatal("import did not adopt exact observable scalar fields")
	}
	for _, name := range []string{"tool_name_to_display_name", "tool_name_to_description"} {
		values := map[string]tftypes.Value{}
		if err := attributes[name].As(&values); err != nil || len(values) != 1 {
			t.Fatalf("imported %s = %#v err=%v", name, values, err)
		}
	}
}

func TestMCP178SetAndClearLifecycleProtocol(t *testing.T) {
	baseState := map[string]interface{}{
		"id": "fields", "server_id": "fields", "server_name": "fields", "transport": "http", "url": "https://example.invalid/mcp", "auth_type": "oauth2", "spec_version": "2024-11-05",
	}
	before := map[string]interface{}{
		"server_id": "fields", "server_name": "fields", "transport": "http", "url": "https://example.invalid/mcp", "auth_type": "oauth2",
		"available_on_public_internet": true, "oauth2_flow": "authorization_code", "instructions": "old",
		"tool_name_to_display_name": map[string]interface{}{"tool": "Old"}, "tool_name_to_description": map[string]interface{}{"tool": "old"}, "credentials": nil, "mcp_info": map[string]interface{}{},
	}
	config := map[string]interface{}{
		"server_name": "fields", "transport": "http", "url": "https://example.invalid/mcp", "auth_type": "oauth2",
		"oauth_scopes": protocolMCPStringList("scope"), "available_on_public_internet": false, "oauth2_flow": "client_credentials", "instructions": "",
		"tool_name_to_display_name": map[string]tftypes.Value{}, "tool_name_to_description": map[string]tftypes.Value{},
	}
	changes := map[string]interface{}{
		"oauth_scopes": protocolMCPStringList("scope"), "available_on_public_internet": false, "oauth2_flow": "client_credentials", "instructions": "",
		"tool_name_to_display_name": map[string]tftypes.Value{}, "tool_name_to_description": map[string]tftypes.Value{},
	}
	after := map[string]interface{}{
		"server_id": "fields", "server_name": "fields", "transport": "http", "url": "https://example.invalid/mcp", "auth_type": "oauth2",
		"available_on_public_internet": false, "oauth2_flow": "client_credentials", "instructions": "",
		"tool_name_to_display_name": map[string]interface{}{}, "tool_name_to_description": map[string]interface{}{}, "credentials": nil, "mcp_info": map[string]interface{}{},
	}
	set := runMCPUpdateCompletionProtocol(t, baseState, config, changes, before, after, protocolMCPFieldPrivate(t, emptyMCPFieldOwnership()))
	if accessGroupProtocolDiagnosticsHaveError(set.applied.Diagnostics) || set.puts != 1 {
		t.Fatalf("set apply: puts=%d diagnostics=%v", set.puts, set.applied.Diagnostics)
	}
	credentials, ok := set.body["credentials"].(map[string]interface{})
	if !ok || !mcpWireValuesEqual(credentials["scopes"], []string{"scope"}) || set.body["available_on_public_internet"] != false || set.body["instructions"] != "" {
		t.Fatalf("set payload = %#v", set.body)
	}
	setOwnership := protocolCommittedMCPFieldOwnership(t, set.applied.Private)
	for _, fieldPath := range []string{mcpFieldOAuthScopesPath, mcpFieldAvailablePublicInternetPath, mcpFieldOAuth2FlowPath, mcpFieldInstructionsPath, mcpFieldToolNameToDisplayNamePath, mcpFieldToolNameToDescriptionPath} {
		if !setOwnership.Owned[fieldPath] {
			t.Fatalf("set ownership missing %s: %#v", fieldPath, setOwnership)
		}
	}

	clearState := map[string]interface{}{
		"id": "fields", "server_id": "fields", "server_name": "fields", "transport": "http", "url": "https://example.invalid/mcp", "auth_type": "oauth2", "spec_version": "2024-11-05",
		"oauth_scopes": protocolMCPStringList("scope"), "available_on_public_internet": false, "oauth2_flow": "client_credentials", "instructions": "",
		"tool_name_to_display_name": map[string]tftypes.Value{}, "tool_name_to_description": map[string]tftypes.Value{},
	}
	clearConfig := map[string]interface{}{"server_name": "fields", "transport": "http", "url": "https://example.invalid/mcp", "auth_type": "oauth2"}
	clearChanges := map[string]interface{}{
		"oauth_scopes": nil, "available_on_public_internet": nil, "oauth2_flow": nil, "instructions": nil, "tool_name_to_display_name": nil, "tool_name_to_description": nil,
	}
	clearedRemote := map[string]interface{}{
		"server_id": "fields", "server_name": "fields", "transport": "http", "url": "https://example.invalid/mcp", "auth_type": "oauth2",
		"available_on_public_internet": true, "oauth2_flow": nil, "instructions": nil,
		"tool_name_to_display_name": map[string]interface{}{}, "tool_name_to_description": map[string]interface{}{}, "credentials": nil, "mcp_info": map[string]interface{}{},
	}
	cleared := runMCPUpdateCompletionProtocol(t, clearState, clearConfig, clearChanges, after, clearedRemote, set.applied.Private)
	if accessGroupProtocolDiagnosticsHaveError(cleared.applied.Diagnostics) || cleared.puts != 1 {
		t.Fatalf("clear apply: puts=%d diagnostics=%v", cleared.puts, cleared.applied.Diagnostics)
	}
	clearCredentials, ok := cleared.body["credentials"].(map[string]interface{})
	if !ok || !mcpWireValuesEqual(clearCredentials["scopes"], []string{}) || cleared.body["available_on_public_internet"] != true || cleared.body["oauth2_flow"] != nil || cleared.body["instructions"] != nil {
		t.Fatalf("clear payload = %#v", cleared.body)
	}
}

func TestMCP178OAuthFlowImplicitClearZeroPUTProtocol(t *testing.T) {
	state := map[string]interface{}{
		"id": "implicit-flow", "server_id": "implicit-flow", "server_name": "implicit_flow", "transport": "http", "url": "https://old.invalid/mcp", "auth_type": "oauth2", "oauth2_flow": "authorization_code", "spec_version": "2024-11-05",
	}
	config := map[string]interface{}{"server_name": "implicit_flow", "transport": "http", "url": "https://new.invalid/mcp", "auth_type": "oauth2"}
	remote := map[string]interface{}{
		"server_id": "implicit-flow", "server_name": "implicit_flow", "transport": "http", "url": "https://old.invalid/mcp", "auth_type": "oauth2", "oauth2_flow": "authorization_code", "mcp_info": map[string]interface{}{},
	}
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"url": "https://new.invalid/mcp"}, remote, remote, protocolMCPFieldPrivate(t, emptyMCPFieldOwnership()))
	if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
		t.Fatalf("implicit flow clear: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
	}
	assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
}

func TestMCP178ScopesOnlyIntentCannotSatisfyAuthClassCredentialSafety(t *testing.T) {
	config := MCPServerResourceModel{
		OAuthScopes: stringListValue("scope"),
		Credentials: types.MapNull(types.StringType),
	}
	planned := mcpFieldOwnership{
		Owned:     map[string]bool{mcpFieldOAuthScopesPath: true},
		Removals:  map[string]bool{},
		Versioned: true,
	}
	delta := map[string]interface{}{
		"credentials": map[string]interface{}{"scopes": []string{"scope"}},
	}
	if err := validateMCPImplicitClearSafety(config, MCPServerResourceModel{}, planned, map[string]interface{}{}, delta, false, true); err == nil {
		t.Fatal("scopes-only credentials object was accepted as complete auth-class replacement intent")
	}

	config.Credentials = stringMapValue(map[string]string{"client_id": "client", "client_secret": "secret"})
	planned.Owned[mcpFieldCredentialsPath] = true
	delta["credentials"] = map[string]interface{}{"client_id": "client", "client_secret": "secret", "scopes": []string{"scope"}}
	if err := validateMCPImplicitClearSafety(config, MCPServerResourceModel{}, planned, map[string]interface{}{}, delta, false, true); err != nil {
		t.Fatalf("complete generic credential and scope intent was rejected: %v", err)
	}

	withoutScopes := config
	withoutScopes.OAuthScopes = types.ListNull(types.StringType)
	withoutScopeOwnership := cloneMCPFieldOwnership(planned)
	delete(withoutScopeOwnership.Owned, mcpFieldOAuthScopesPath)
	if err := validateMCPImplicitClearSafety(withoutScopes, MCPServerResourceModel{}, withoutScopeOwnership, map[string]interface{}{}, map[string]interface{}{
		"credentials": map[string]interface{}{"client_id": "client", "client_secret": "secret"},
	}, false, true); err == nil {
		t.Fatal("auth-class replacement without explicit native scope intent was accepted")
	}

	unknownScopes := config
	unknownScopes.OAuthScopes = types.ListUnknown(types.StringType)
	if err := validateMCPImplicitClearSafety(unknownScopes, MCPServerResourceModel{}, planned, map[string]interface{}{}, map[string]interface{}{
		"credentials": map[string]interface{}{"client_id": "client", "client_secret": "secret"},
	}, false, true); err == nil {
		t.Fatal("auth-class replacement with unknown owned scopes was accepted")
	}
}

func TestMCP178OAuthFlowImplicitClearProtection(t *testing.T) {
	hydration := map[string]interface{}{"oauth2_flow": "authorization_code"}
	state := MCPServerResourceModel{OAuth2Flow: types.StringValue("authorization_code")}
	if err := validateMCPImplicitClearSafety(MCPServerResourceModel{}, state, emptyMCPFieldOwnership(), hydration, map[string]interface{}{}, true, false); err == nil {
		t.Fatal("URL change was allowed to clear an adopted OAuth2 flow")
	}
	planned := mcpFieldOwnership{Owned: map[string]bool{}, Removals: map[string]bool{mcpFieldOAuth2FlowPath: true}, Versioned: true}
	if err := validateMCPImplicitClearSafety(MCPServerResourceModel{}, state, planned, hydration, map[string]interface{}{"oauth2_flow": nil}, true, false); err != nil {
		t.Fatalf("explicit OAuth2 flow clear rejected: %v", err)
	}
	if sentinel := mcpFieldRemovalSentinel(mcpFieldAvailablePublicInternetPath); sentinel != true {
		t.Fatalf("public-internet removal sentinel = %#v", sentinel)
	}
}
