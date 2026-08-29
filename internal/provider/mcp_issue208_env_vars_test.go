package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	frameworkdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	frameworkvalidator "github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func testMCPEnvVarsValue(t *testing.T, values ...mcpEnvVar) types.List {
	t.Helper()
	result, diagnostics := mcpEnvVarsTerraformValue(context.Background(), values, pathRootEnvVars)
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	return result
}

var pathRootEnvVars = path.Root("env_vars")

func cloneMCPEnvVarsInterfaceMap(source map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(source))
	for name, value := range source {
		result[name] = value
	}
	return result
}

func protocolMCPEnvVars(t *testing.T, schema *tfprotov6.Schema, values ...map[string]interface{}) []tftypes.Value {
	t.Helper()
	root := schema.ValueType().(tftypes.Object)
	listType := root.AttributeTypes["env_vars"].(tftypes.List)
	objectType := listType.ElementType.(tftypes.Object)
	result := make([]tftypes.Value, len(values))
	for index, configured := range values {
		attributes := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
		for name, valueType := range objectType.AttributeTypes {
			value := interface{}(nil)
			if configuredValue, present := configured[name]; present {
				value = configuredValue
			}
			attributes[name] = tftypes.NewValue(valueType, value)
		}
		result[index] = tftypes.NewValue(objectType, attributes)
	}
	return result
}

func TestMCPEnvVarsSchemaV8AndDataSourcesUnchanged(t *testing.T) {
	ctx := context.Background()
	var response frameworkresource.SchemaResponse
	(&MCPServerResource{}).Schema(ctx, frameworkresource.SchemaRequest{}, &response)
	if response.Schema.Version != 8 {
		t.Fatalf("schema version=%d", response.Schema.Version)
	}
	attribute, ok := response.Schema.Attributes["env_vars"].(resourceschema.ListNestedAttribute)
	if !ok || !attribute.IsOptional() || !attribute.IsComputed() || !attribute.IsSensitive() || len(attribute.Validators) == 0 {
		t.Fatalf("env_vars schema=%#v", response.Schema.Attributes["env_vars"])
	}
	name := attribute.NestedObject.Attributes["name"]
	value := attribute.NestedObject.Attributes["value"].(resourceschema.StringAttribute)
	scope := attribute.NestedObject.Attributes["scope"].(resourceschema.StringAttribute)
	description := attribute.NestedObject.Attributes["description"]
	if !name.IsRequired() || !value.IsOptional() || !value.IsComputed() || value.Default == nil ||
		!scope.IsOptional() || !scope.IsComputed() || scope.Default == nil || len(scope.Validators) != 1 ||
		!description.IsOptional() || description.IsComputed() {
		t.Fatal("env_vars nested flags/defaults changed")
	}
	var singular frameworkdatasource.SchemaResponse
	(&MCPServerDataSource{}).Schema(ctx, frameworkdatasource.SchemaRequest{}, &singular)
	var listed frameworkdatasource.SchemaResponse
	(&MCPServersListDataSource{}).Schema(ctx, frameworkdatasource.SchemaRequest{}, &listed)
	if _, present := singular.Schema.Attributes["env_vars"]; present {
		t.Fatal("singular data source unexpectedly exposes env_vars")
	}
	items := listed.Schema.Attributes["mcp_servers"].(datasourceschema.ListNestedAttribute)
	if _, present := items.NestedObject.Attributes["env_vars"]; present {
		t.Fatal("list data source unexpectedly exposes env_vars")
	}
}

func TestMCPEnvVarsStrictConversionValidationAndPrivacy(t *testing.T) {
	ctx := context.Background()
	valid := testMCPEnvVarsValue(t,
		mcpEnvVar{Name: "GLOBAL_ONE", Value: "secret-one", Scope: "global"},
		mcpEnvVar{Name: "_USER2", Value: "placeholder", Scope: "user"},
	)
	converted, state, diagnostics := strictTerraformMCPEnvVars(ctx, valid, pathRootEnvVars)
	if diagnostics.HasError() || state != collectionValuePopulated || len(converted) != 2 || converted[1].Value != "placeholder" {
		t.Fatalf("valid conversion failed: state=%d diagnostics=%v", state, diagnostics)
	}
	for _, test := range []struct {
		value types.List
		state collectionValueState
	}{
		{types.ListNull(mcpEnvVarObjectType), collectionValueNull},
		{types.ListUnknown(mcpEnvVarObjectType), collectionValueUnknown},
		{types.ListValueMust(mcpEnvVarObjectType, nil), collectionValueEmpty},
	} {
		_, gotState, gotDiagnostics := strictTerraformMCPEnvVars(ctx, test.value, pathRootEnvVars)
		if gotDiagnostics.HasError() || gotState != test.state {
			t.Fatalf("outer state=%d want=%d diagnostics=%v", gotState, test.state, gotDiagnostics)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, canceledDiagnostics := strictTerraformMCPEnvVars(canceled, valid, pathRootEnvVars); !canceledDiagnostics.HasError() {
		t.Fatal("cancellation was not distinguished")
	}

	duplicate := testMCPEnvVarsValue(t, mcpEnvVar{Name: "DUP", Scope: "global"}, mcpEnvVar{Name: "DUP", Scope: "user"})
	var duplicateResponse frameworkvalidator.ListResponse
	mcpEnvVarsValidator{}.ValidateList(ctx, frameworkvalidator.ListRequest{ConfigValue: duplicate, Path: pathRootEnvVars}, &duplicateResponse)
	if !duplicateResponse.Diagnostics.HasError() {
		t.Fatal("duplicate names were accepted")
	}
	for _, invalid := range []string{"", "1BAD", "BAD-NAME", "BAD.NAME"} {
		var response frameworkvalidator.StringResponse
		mcpEnvVarNameValidator{}.ValidateString(ctx, frameworkvalidator.StringRequest{ConfigValue: types.StringValue(invalid), Path: pathRootEnvVars}, &response)
		if !response.Diagnostics.HasError() {
			t.Fatal("invalid name was accepted")
		}
		if strings.Contains(response.Diagnostics.Errors()[0].Detail(), invalid) && invalid != "" {
			t.Fatal("name leaked through validation diagnostics")
		}
	}

	secretFragments := []string{"SECRET_NAME", "secret-value", "secret-description", "server-id", "/private", "https://"}
	for _, raw := range []interface{}{
		"wrong-root",
		[]interface{}{nil},
		[]interface{}{map[string]interface{}{"name": ""}},
		[]interface{}{map[string]interface{}{"name": "BAD-NAME"}},
		[]interface{}{map[string]interface{}{"name": "SECRET_NAME", "value": false}},
		[]interface{}{map[string]interface{}{"name": "SECRET_NAME", "scope": "other"}},
		[]interface{}{map[string]interface{}{"name": "SECRET_NAME", "description": false}},
		[]interface{}{map[string]interface{}{"name": "SECRET_NAME"}, map[string]interface{}{"name": "SECRET_NAME"}},
	} {
		result := map[string]interface{}{"env_vars": raw}
		_, _, _, apiDiagnostics := strictAPIMCPEnvVars(ctx, result, "env_vars", pathRootEnvVars)
		if !apiDiagnostics.HasError() {
			t.Fatalf("malformed API value accepted: %T", raw)
		}
		text := fmt.Sprint(apiDiagnostics)
		for _, fragment := range secretFragments {
			if strings.Contains(text, fragment) {
				t.Fatalf("diagnostic leaked protected content: %s", text)
			}
		}
	}
}

func TestMCPEnvVarsCreateDefaultsExplicitEmptyAndUserPlaceholderProtocol(t *testing.T) {
	ctx := context.Background()
	var postBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodPost:
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			postBody.Store(body)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"server_id": body["server_id"]})
		case http.MethodGet:
			body := postBody.Load().(map[string]interface{})
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"server_id": body["server_id"], "server_name": "env_create", "transport": "http", "auth_type": "none",
				"url": "https://known.invalid/mcp", "env_vars": body["env_vars"],
			})
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	envVars := protocolMCPEnvVars(t, schema,
		map[string]interface{}{"name": "DEFAULTED"},
		map[string]interface{}{"name": "USER_VALUE", "value": "admin-placeholder", "scope": "user", "description": "hint"},
	)
	config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, map[string]interface{}{
		"server_name": "env_create", "transport": "http", "url": "https://known.invalid/mcp", "env_vars": envVars,
	})
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("create: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(created.Diagnostics))
	}
	want := []map[string]interface{}{
		{"name": "DEFAULTED", "value": "", "scope": "global", "description": nil},
		{"name": "USER_VALUE", "value": "admin-placeholder", "scope": "user", "description": "hint"},
	}
	body := postBody.Load().(map[string]interface{})
	if !mcpWireValuesEqual(body["env_vars"], want) {
		t.Fatalf("create env_vars mismatch: %#v", body["env_vars"])
	}
	committed := protocolCommittedMCPFieldOwnership(t, created.Private)
	if !committed.Owned[mcpFieldEnvVarsPath] {
		t.Fatal("create did not commit env_vars ownership")
	}
}

func TestMCPEnvVarsUpdateAllMembersAndPreserveUnmanagedProtocol(t *testing.T) {
	_, schema, closeHarness := protocolMCPStage2Harness(t)
	defer closeHarness()
	prior := []map[string]interface{}{{"name": "ONE", "value": "first", "scope": "global", "description": nil}}
	desired := []map[string]interface{}{
		{"name": "SECOND", "value": "placeholder", "scope": "user", "description": "per-user"},
		{"name": "ONE", "value": "changed", "scope": "global", "description": "shared"},
	}
	state := map[string]interface{}{
		"id": "env-update", "server_id": "env-update", "server_name": "env-update", "transport": "http", "url": "https://known.invalid/mcp",
		"auth_type": "none", "spec_version": "2024-11-05", "env_vars": protocolMCPEnvVars(t, schema, prior[0]),
	}
	config := map[string]interface{}{
		"server_name": "env-update", "transport": "http", "url": "https://known.invalid/mcp",
		"env_vars": protocolMCPEnvVars(t, schema, desired...),
	}
	before := map[string]interface{}{
		"server_id": "env-update", "server_name": "env-update", "transport": "http", "url": "https://known.invalid/mcp",
		"auth_type": "none", "mcp_info": map[string]interface{}{}, "env_vars": prior,
	}
	after := cloneMCPEnvVarsInterfaceMap(before)
	after["env_vars"] = desired
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"env_vars": protocolMCPEnvVars(t, schema, desired...)}, before, after, protocolMCPFieldPrivate(t, mcpFieldOwnership{
		Owned: map[string]bool{mcpFieldEnvVarsPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true,
	}))
	if result.puts != 1 || accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || !mcpWireValuesEqual(result.body["env_vars"], desired) {
		t.Fatalf("complete update failed: puts=%d diagnostics=%s body=%#v", result.puts, agentProtocolDiagnosticsText(result.applied.Diagnostics), result.body)
	}

	unmanagedState := map[string]interface{}{
		"id": "env-unmanaged", "server_id": "env-unmanaged", "server_name": "env-unmanaged", "description": "old", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05", "env_vars": protocolMCPEnvVars(t, schema, prior[0]),
	}
	unmanagedConfig := map[string]interface{}{"server_name": "env-unmanaged", "description": "new", "transport": "http", "url": "https://known.invalid/mcp"}
	unmanagedBefore := map[string]interface{}{
		"server_id": "env-unmanaged", "server_name": "env-unmanaged", "description": "old", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{}, "env_vars": prior,
	}
	unmanagedAfter := cloneMCPEnvVarsInterfaceMap(unmanagedBefore)
	unmanagedAfter["description"] = "new"
	unmanagedAfter["env_vars"] = desired
	unmanaged := runMCPUpdateCompletionProtocol(t, unmanagedState, unmanagedConfig, map[string]interface{}{"description": "new"}, unmanagedBefore, unmanagedAfter, protocolMCPFieldPrivate(t, emptyMCPFieldOwnership()))
	if unmanaged.puts != 1 || !accessGroupProtocolDiagnosticsHaveError(unmanaged.applied.Diagnostics) {
		t.Fatalf("unmanaged environment-variable change was accepted: puts=%d diagnostics=%s", unmanaged.puts, agentProtocolDiagnosticsText(unmanaged.applied.Diagnostics))
	}
	assertMCPServerFailedUpdateRetainsPriorState(t, unmanaged.schema, unmanaged.state, unmanaged.applied.NewState)
}

func TestMCPEnvVarsUpdateClearAndMismatchRetainTransactionProtocol(t *testing.T) {
	values := []map[string]interface{}{{"name": "ONE", "value": "first", "scope": "global", "description": nil}}
	owned := mcpFieldOwnership{Owned: map[string]bool{mcpFieldEnvVarsPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
	stateBase := map[string]interface{}{
		"id": "env-clear", "server_id": "env-clear", "server_name": "env-clear", "transport": "http", "url": "https://known.invalid/mcp",
		"auth_type": "none", "spec_version": "2024-11-05", "field_ownership_generation": int64(1),
	}
	config := map[string]interface{}{"server_name": "env-clear", "transport": "http", "url": "https://known.invalid/mcp"}
	before := map[string]interface{}{
		"server_id": "env-clear", "server_name": "env-clear", "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none",
		"mcp_info": map[string]interface{}{}, "env_vars": values,
	}
	for _, test := range []struct {
		name        string
		after       interface{}
		wantFailure bool
	}{
		{name: "exact clear", after: []interface{}{}},
		{name: "mismatched readback", after: values, wantFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, schema, closeHarness := protocolMCPStage2Harness(t)
			defer closeHarness()
			state := cloneMCPEnvVarsInterfaceMap(stateBase)
			state["env_vars"] = protocolMCPEnvVars(t, schema, map[string]interface{}{"name": "ONE", "value": "first", "scope": "global"})
			after := cloneMCPEnvVarsInterfaceMap(before)
			after["env_vars"] = test.after
			result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"env_vars": nil}, before, after, protocolMCPFieldPrivate(t, owned))
			if result.puts != 1 || accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) != test.wantFailure {
				t.Fatalf("clear result puts=%d diagnostics=%s", result.puts, agentProtocolDiagnosticsText(result.applied.Diagnostics))
			}
			if sent, present := result.body["env_vars"]; !present || !mcpWireValuesEqual(sent, []interface{}{}) {
				t.Fatalf("exact clear sentinel missing: %#v", result.body)
			}
			if test.wantFailure {
				assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
				if committed := protocolCommittedMCPFieldOwnership(t, result.applied.Private); !mcpFieldOwnershipEqual(committed, owned) {
					t.Fatalf("failed readback changed private ownership: %#v", committed)
				}
			}
		})
	}
}

func TestMCPEnvVarsImportMaskingAndMalformedLateMemberTransactionalProtocol(t *testing.T) {
	ctx := context.Background()
	var payload atomic.Value
	payload.Store(map[string]interface{}{
		"server_id": "env-import", "server_name": "env-import", "transport": "http", "auth_type": "none",
		"env_vars": []interface{}{
			map[string]interface{}{"name": "FIRST", "value": "one", "scope": "global", "description": nil},
			map[string]interface{}{"name": "SECOND", "value": "placeholder", "scope": "user", "description": "hint"},
		},
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(payload.Load())
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_mcp_server", ID: "env-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) {
		t.Fatalf("import: %v %s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	first, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(first.Diagnostics) {
		t.Fatalf("first read: %v %s", err, agentProtocolDiagnosticsText(first.Diagnostics))
	}
	firstAttributes := protocolAttributeMap(t, schema, first.NewState)
	if firstAttributes["env_vars"].IsNull() {
		t.Fatal("full-admin import did not adopt env_vars")
	}
	if ownership := protocolCommittedMCPFieldOwnership(t, first.Private); ownership.Owned[mcpFieldEnvVarsPath] {
		t.Fatal("import inferred Terraform ownership")
	}

	payload.Store(map[string]interface{}{"server_id": "env-import", "server_name": "env-import", "transport": "http", "auth_type": "none", "env_vars": nil})
	masked, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: first.NewState, Private: first.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(masked.Diagnostics) {
		t.Fatalf("masked read: %v %s", err, agentProtocolDiagnosticsText(masked.Diagnostics))
	}
	if !protocolAttributeMap(t, schema, masked.NewState)["env_vars"].Equal(firstAttributes["env_vars"]) {
		t.Fatal("masked null erased known env_vars")
	}

	payload.Store(map[string]interface{}{"server_id": "env-import", "server_name": "env-import", "transport": "http", "auth_type": "none"})
	omitted, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: masked.NewState, Private: masked.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(omitted.Diagnostics) {
		t.Fatalf("omitted read: %v %s", err, agentProtocolDiagnosticsText(omitted.Diagnostics))
	}
	if !protocolAttributeMap(t, schema, omitted.NewState)["env_vars"].Equal(firstAttributes["env_vars"]) {
		t.Fatal("masked omission erased known env_vars")
	}

	payload.Store(map[string]interface{}{"server_id": "env-import", "server_name": "env-import", "transport": "http", "auth_type": "none", "env_vars": []interface{}{}})
	cleared, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: omitted.NewState, Private: omitted.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(cleared.Diagnostics) {
		t.Fatalf("authoritative empty read: %v %s", err, agentProtocolDiagnosticsText(cleared.Diagnostics))
	}
	envVarsType := schema.ValueType().(tftypes.Object).AttributeTypes["env_vars"]
	if value := protocolAttributeMap(t, schema, cleared.NewState)["env_vars"]; !value.Equal(tftypes.NewValue(envVarsType, []tftypes.Value{})) {
		t.Fatal("authoritative empty list was not projected")
	}

	payload.Store(map[string]interface{}{
		"server_id": "env-import", "server_name": "changed-late", "transport": "http", "auth_type": "none",
		"env_vars": []interface{}{
			map[string]interface{}{"name": "FIRST", "value": "one", "scope": "global"},
			map[string]interface{}{"name": "SECOND", "value": false, "scope": "user"},
		},
	})
	malformed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: cleared.NewState, Private: cleared.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(malformed.Diagnostics) {
		t.Fatalf("malformed late member accepted: %v %s", err, agentProtocolDiagnosticsText(malformed.Diagnostics))
	}
	before, _ := cleared.NewState.Unmarshal(schema.ValueType())
	after, _ := malformed.NewState.Unmarshal(schema.ValueType())
	if !before.Equal(after) || !reflect.DeepEqual(cleared.Private, malformed.Private) {
		t.Fatal("malformed late member published partial public/private state")
	}
}

func TestMCPEnvVarsAcceptedCreateRecoveryProtocol(t *testing.T) {
	ctx := context.Background()
	var accepted atomic.Value
	var readsEnabled atomic.Bool
	var puts atomic.Int64
	var putBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodPost:
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			accepted.Store(body)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"server_id": body["server_id"]})
		case http.MethodGet:
			if !readsEnabled.Load() {
				http.Error(writer, `{"error":"unavailable"}`, http.StatusInternalServerError)
				return
			}
			body := accepted.Load().(map[string]interface{})
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"server_id": body["server_id"], "server_name": "env_recovery", "transport": "http", "auth_type": "none",
				"url": "https://known.invalid/mcp", "env_vars": body["env_vars"], "mcp_info": map[string]interface{}{},
			})
		case http.MethodPut:
			puts.Add(1)
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			putBody.Store(body)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	envVars := protocolMCPEnvVars(t, schema, map[string]interface{}{"name": "RECOVER", "value": "value", "scope": "global"})
	configValues := map[string]interface{}{
		"server_name": "env_recovery", "transport": "http", "url": "https://known.invalid/mcp", "env_vars": envVars,
	}
	config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, configValues)
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("unconfirmed create: %v %s", err, agentProtocolDiagnosticsText(created.Diagnostics))
	}
	readsEnabled.Store(true)
	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: "litellm_mcp_server", CurrentState: created.NewState, Private: created.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("recovery refresh: %v %s", err, agentProtocolDiagnosticsText(refreshed.Diagnostics))
	}
	proposed := organizationProjectProtocolReplace(t, schema, refreshed.NewState, configValues)
	recoveryPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: refreshed.NewState, ProposedNewState: proposed, PriorPrivate: refreshed.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(recoveryPlan.Diagnostics) {
		t.Fatalf("recovery plan: %v %s", err, agentProtocolDiagnosticsText(recoveryPlan.Diagnostics))
	}
	recovered, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: refreshed.NewState,
		PlannedState: recoveryPlan.PlannedState, PlannedPrivate: recoveryPlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(recovered.Diagnostics) || puts.Load() != 1 {
		t.Fatalf("recovery apply: %v diagnostics=%s puts=%d body=%#v", err, agentProtocolDiagnosticsText(recovered.Diagnostics), puts.Load(), putBody.Load())
	}
	if body, _ := putBody.Load().(map[string]interface{}); body == nil {
		t.Fatal("accepted-create recovery did not send its required base-field update")
	} else if _, resent := body["env_vars"]; resent {
		t.Fatalf("authoritatively matched environment variables were resent: %#v", body)
	}
	if ownership := protocolCommittedMCPFieldOwnership(t, recovered.Private); !ownership.Owned[mcpFieldEnvVarsPath] {
		t.Fatalf("accepted create recovery did not commit ownership: %#v", ownership)
	}
	if protocolPrivateHasKey(t, recovered.Private, mcpFieldAcceptedCreatePrivateKey) {
		t.Fatal("accepted create recovery marker was not cleared")
	}
}

func TestMCPEnvVarsDirectV0ThroughV6Upgrade(t *testing.T) {
	ctx := context.Background()
	upgraders := (&MCPServerResource{}).UpgradeState(ctx)
	for version := int64(0); version <= 6; version++ {
		prior := map[string]interface{}{"id": "upgrade", "server_id": "upgrade", "transport": "http", "private_marker": "preserved"}
		if version == 0 {
			prior["extra_headers"] = map[string]string{}
		}
		raw, _ := json.Marshal(prior)
		var response frameworkresource.UpgradeStateResponse
		upgrader, present := upgraders[version]
		if !present {
			t.Fatalf("missing direct v%d upgrader", version)
		}
		upgrader.StateUpgrader(ctx, frameworkresource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: raw}}, &response)
		if response.Diagnostics.HasError() || response.DynamicValue == nil {
			t.Fatalf("v%d upgrade diagnostics=%v", version, response.Diagnostics)
		}
		var upgraded map[string]json.RawMessage
		if err := json.Unmarshal(response.DynamicValue.JSON, &upgraded); err != nil {
			t.Fatal(err)
		}
		if string(upgraded["env_vars"]) != "null" || string(upgraded["private_marker"]) != `"preserved"` {
			t.Fatalf("v%d upgraded state=%s", version, response.DynamicValue.JSON)
		}
	}
}
