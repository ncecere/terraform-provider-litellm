package provider

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	frameworkdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	frameworkvalidator "github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var mcp208FieldNames = []string{
	"delegate_auth_to_upstream", "oauth_passthrough", "dcr_bridge", "is_byok",
	"byok_description", "byok_api_key_help_url", "source_url", "timeout", "max_concurrent_requests",
}

func TestMCP208SchemaV6AndDataSourceParity(t *testing.T) {
	ctx := context.Background()
	var resourceResponse frameworkresource.SchemaResponse
	(&MCPServerResource{}).Schema(ctx, frameworkresource.SchemaRequest{}, &resourceResponse)
	if resourceResponse.Schema.Version != 8 {
		t.Fatalf("schema version = %d", resourceResponse.Schema.Version)
	}
	for _, name := range mcp208FieldNames {
		attribute, present := resourceResponse.Schema.Attributes[name]
		if !present || !attribute.IsOptional() || !attribute.IsComputed() || attribute.IsSensitive() {
			t.Fatalf("resource field flags are invalid for %s", name)
		}
	}
	listAttribute, ok := resourceResponse.Schema.Attributes["byok_description"].(resourceschema.ListAttribute)
	if !ok || listAttribute.ElementType != types.StringType || len(listAttribute.Validators) == 0 {
		t.Fatal("byok_description does not enforce string-list members")
	}
	if attribute, ok := resourceResponse.Schema.Attributes["timeout"].(resourceschema.Float64Attribute); !ok || len(attribute.Validators) != 1 {
		t.Fatal("timeout validator is missing")
	}
	if attribute, ok := resourceResponse.Schema.Attributes["max_concurrent_requests"].(resourceschema.Int64Attribute); !ok || len(attribute.Validators) != 1 {
		t.Fatal("maximum concurrency validator is missing")
	}

	var singular frameworkdatasource.SchemaResponse
	(&MCPServerDataSource{}).Schema(ctx, frameworkdatasource.SchemaRequest{}, &singular)
	var list frameworkdatasource.SchemaResponse
	(&MCPServersListDataSource{}).Schema(ctx, frameworkdatasource.SchemaRequest{}, &list)
	items := list.Schema.Attributes["mcp_servers"].(datasourceschema.ListNestedAttribute)
	for _, name := range mcp208FieldNames {
		if !singular.Schema.Attributes[name].IsComputed() || !items.NestedObject.Attributes[name].IsComputed() {
			t.Fatalf("data source parity is missing for %s", name)
		}
	}
}

func TestMCP208OwnershipCompatibilityAndExactPayloads(t *testing.T) {
	ctx := context.Background()
	legacy := mcpFieldOwnership{Owned: map[string]bool{mcpFieldAliasPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
	raw := encodeMCPFieldOwnership(legacy)
	decoded, err := decodeMCPFieldOwnership(raw)
	if err != nil || !mcpFieldOwnershipEqual(legacy, decoded) {
		t.Fatalf("legacy ownership no longer decodes: %v", err)
	}

	config := MCPServerResourceModel{
		ServerName:             types.StringValue("safe"),
		Transport:              types.StringValue("http"),
		AuthType:               types.StringValue("true_passthrough"),
		URL:                    types.StringValue("https://example.invalid"),
		DelegateAuthToUpstream: types.BoolValue(false),
		OAuthPassthrough:       types.BoolValue(false),
		DCRBridge:              types.BoolValue(true),
		IsBYOK:                 types.BoolValue(false),
		BYOKDescription:        stringListValue(),
		BYOKAPIKeyHelpURL:      types.StringValue(""),
		SourceURL:              types.StringValue(""),
		Timeout:                types.Float64Value(1.25),
		MaxConcurrentRequests:  types.Int64Value(7),
	}
	request, err := (&MCPServerResource{}).buildMCPServerCreateRequest(ctx, &config, &config, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]interface{}{
		"transport": "http", "auth_type": "true_passthrough", "server_name": "safe", "url": "https://example.invalid",
		"delegate_auth_to_upstream": false, "oauth_passthrough": false, "dcr_bridge": true, "is_byok": false,
		"byok_description": []string{}, "byok_api_key_help_url": "", "source_url": "", "timeout": 1.25, "max_concurrent_requests": int64(7),
	}
	if !mcpWireValuesEqual(request, want) {
		t.Fatalf("create payload mismatch: %#v", request)
	}

	for fieldPath, sentinel := range map[string]interface{}{
		mcpFieldDelegateAuthToUpstreamPath: false,
		mcpFieldOAuthPassthroughPath:       false,
		mcpFieldDCRBridgePath:              nil,
		mcpFieldIsBYOKPath:                 false,
		mcpFieldBYOKDescriptionPath:        []string{},
		mcpFieldBYOKAPIKeyHelpURLPath:      nil,
		mcpFieldSourceURLPath:              nil,
		mcpFieldTimeoutPath:                nil,
		mcpFieldMaxConcurrentRequestsPath:  nil,
	} {
		if !mcpWireValuesEqual(mcpFieldRemovalSentinel(fieldPath), sentinel) {
			t.Fatalf("wrong removal sentinel for %s", fieldPath)
		}
	}
}

func TestMCP208CrossValidationUnknownDeferralAndValueFreeNumbers(t *testing.T) {
	validate := func(data MCPServerResourceModel) diag.Diagnostics {
		var diagnostics diag.Diagnostics
		validateMCP208CrossConfiguration(data, &diagnostics)
		return diagnostics
	}
	if diagnostics := validate(MCPServerResourceModel{DCRBridge: types.BoolValue(true), AuthType: types.StringValue("oauth2")}); !diagnostics.HasError() {
		t.Fatal("incompatible DCR authentication was accepted")
	}
	if diagnostics := validate(MCPServerResourceModel{DCRBridge: types.BoolValue(true), AuthType: types.StringUnknown()}); diagnostics.HasError() {
		t.Fatal("unknown DCR dependency did not defer")
	}
	if diagnostics := validate(MCPServerResourceModel{DelegateAuthToUpstream: types.BoolValue(true), AuthType: types.StringValue("none")}); !diagnostics.HasError() {
		t.Fatal("incompatible delegated authentication was accepted")
	}
	if diagnostics := validate(MCPServerResourceModel{DelegateAuthToUpstream: types.BoolValue(true), AuthType: types.StringValue("oauth2"), OAuth2Flow: types.StringValue("client_credentials")}); !diagnostics.HasError() {
		t.Fatal("machine-to-machine delegated authentication was accepted")
	}
	if diagnostics := validate(MCPServerResourceModel{DelegateAuthToUpstream: types.BoolValue(true), AuthType: types.StringValue("oauth2"), OAuth2Flow: types.StringNull()}); !diagnostics.HasError() {
		t.Fatal("omitted delegated authentication flow was accepted")
	}
	unknownDelegation := MCPServerResourceModel{DelegateAuthToUpstream: types.BoolValue(true), AuthType: types.StringValue("oauth2"), OAuth2Flow: types.StringUnknown()}
	if diagnostics := validate(unknownDelegation); diagnostics.HasError() {
		t.Fatalf("unknown delegated authentication flow did not defer: %v", diagnostics)
	}
	if _, err := mcpFieldDesiredValue(context.Background(), unknownDelegation, mcpFieldDelegateAuthToUpstreamPath); err == nil {
		t.Fatal("unknown delegated authentication flow reached mutation construction")
	}
	if diagnostics := validate(MCPServerResourceModel{DelegateAuthToUpstream: types.BoolValue(true), AuthType: types.StringValue("oauth2"), OAuth2Flow: types.StringValue("authorization_code")}); diagnostics.HasError() {
		t.Fatalf("interactive delegated authentication was rejected: %v", diagnostics)
	}
	validPassthrough := MCPServerResourceModel{OAuthPassthrough: types.BoolValue(true), AuthType: types.StringValue("none"), ExtraHeaders: stringListValue("x-trace", "aUtHoRiZaTiOn")}
	if diagnostics := validate(validPassthrough); diagnostics.HasError() {
		t.Fatalf("case-insensitive Authorization member was rejected: %v", diagnostics)
	}
	validPassthrough.ExtraHeaders = stringListValue("x-trace")
	if diagnostics := validate(validPassthrough); !diagnostics.HasError() {
		t.Fatal("OAuth passthrough without Authorization was accepted")
	}
	for _, data := range []MCPServerResourceModel{
		{DCRBridge: types.BoolValue(false), AuthType: types.StringValue("none")},
		{DelegateAuthToUpstream: types.BoolValue(false), AuthType: types.StringValue("none")},
		{OAuthPassthrough: types.BoolValue(false), AuthType: types.StringValue("oauth2")},
	} {
		if diagnostics := validate(data); diagnostics.HasError() {
			t.Fatal("explicit false was rejected")
		}
	}

	for _, value := range []float64{0, -1} {
		var response frameworkvalidator.Float64Response
		mcpPositiveFloat64Validator{}.ValidateFloat64(context.Background(), frameworkvalidator.Float64Request{ConfigValue: types.Float64Value(value)}, &response)
		if !response.Diagnostics.HasError() {
			t.Fatal("invalid positive number was accepted")
		}
		text := response.Diagnostics.Errors()[0].Summary() + response.Diagnostics.Errors()[0].Detail()
		if strings.Contains(text, "-1") || strings.Contains(text, "Inf") || strings.Contains(text, "NaN") {
			t.Fatal("numeric diagnostic exposed a value")
		}
	}
	for _, value := range []int64{0, -1, math.MaxInt32 + 1} {
		var response frameworkvalidator.Int64Response
		mcpPositiveInt64Validator{}.ValidateInt64(context.Background(), frameworkvalidator.Int64Request{ConfigValue: types.Int64Value(value)}, &response)
		if !response.Diagnostics.HasError() {
			t.Fatal("integer outside LiteLLM's durable range was accepted")
		}
	}
}

func TestMCP208ProjectionNumberStrictnessTransactionalityAndParity(t *testing.T) {
	ctx := context.Background()
	response := map[string]interface{}{
		"server_id": "safe", "transport": "http", "auth_type": "none",
		"delegate_auth_to_upstream": true, "oauth_passthrough": false, "dcr_bridge": nil, "is_byok": true,
		"byok_description": []interface{}{"first"}, "byok_api_key_help_url": nil, "source_url": "https://source.invalid",
		"timeout": json.Number("1.25"), "max_concurrent_requests": json.Number("9223372036854775807"),
	}
	singular, err := projectMCPServerDataSource(response, "safe")
	if err != nil {
		t.Fatal(err)
	}
	listed, err := projectMCPServerManagerListDataSource(response, "safe")
	if err != nil {
		t.Fatal(err)
	}
	if !singular.DelegateAuthToUpstream.Equal(listed.DelegateAuthToUpstream) || !singular.BYOKDescription.Equal(listed.BYOKDescription) || !singular.Timeout.Equal(listed.Timeout) || !singular.MaxConcurrentRequests.Equal(listed.MaxConcurrentRequests) {
		t.Fatal("singular and list projections differ")
	}

	resourceData := MCPServerResourceModel{ID: types.StringValue("safe"), ServerID: types.StringValue("safe"), Description: types.StringValue("before")}
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &resourceData, response, emptyMCPInfoProvenance(), emptyMCPFieldOwnership(), true, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatal(err)
	}
	before := resourceData
	malformed := make(map[string]interface{}, len(response))
	for key, value := range response {
		malformed[key] = value
	}
	malformed["description"] = "after"
	malformed["max_concurrent_requests"] = json.Number("1.5")
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &resourceData, malformed, emptyMCPInfoProvenance(), emptyMCPFieldOwnership(), true, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err == nil {
		t.Fatal("fractional maximum concurrency was accepted")
	}
	if !reflect.DeepEqual(before, resourceData) {
		t.Fatal("late malformed field partially changed resource projection")
	}
	for unlimited, want := range map[json.Number]int64{"0": 0, "-1": -1} {
		malformed["max_concurrent_requests"] = unlimited
		projected, err := projectMCPServerDataSource(malformed, "safe")
		if err != nil || projected.MaxConcurrentRequests.ValueInt64() != want {
			t.Fatalf("valid upstream unlimited concurrency sentinel was rejected: %v", err)
		}
	}
	for _, bad := range []interface{}{json.Number("1.5"), json.Number("9223372036854775808")} {
		malformed["max_concurrent_requests"] = bad
		if _, err := projectMCPServerDataSource(malformed, "safe"); err == nil {
			t.Fatal("malformed maximum concurrency was accepted")
		}
	}
	malformed["max_concurrent_requests"] = json.Number("1")
	for _, bad := range []interface{}{json.Number("0"), json.Number("-1"), math.Inf(1), "1"} {
		malformed["timeout"] = bad
		if _, err := projectMCPServerDataSource(malformed, "safe"); err == nil {
			t.Fatal("invalid timeout was accepted")
		}
	}
}

func TestMCP208DirectV0ThroughV5TypedNullUpgrade(t *testing.T) {
	ctx := context.Background()
	upgraders := (&MCPServerResource{}).UpgradeState(ctx)
	for version := int64(0); version <= 5; version++ {
		prior := map[string]interface{}{"id": "safe", "server_id": "safe", "transport": "http"}
		if version == 0 {
			prior["extra_headers"] = map[string]string{}
		}
		raw, _ := json.Marshal(prior)
		response := frameworkresource.UpgradeStateResponse{}
		upgraders[version].StateUpgrader(ctx, frameworkresource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: raw}}, &response)
		if response.Diagnostics.HasError() || response.DynamicValue == nil {
			t.Fatalf("v%d upgrade failed: %v", version, response.Diagnostics)
		}
		var upgraded map[string]json.RawMessage
		if err := json.Unmarshal(response.DynamicValue.JSON, &upgraded); err != nil {
			t.Fatal(err)
		}
		for _, name := range mcp208FieldNames {
			if string(upgraded[name]) != "null" {
				t.Fatalf("v%d %s was not a typed null", version, name)
			}
		}
	}
}

func TestMCP208DCRImplicitClearSafety(t *testing.T) {
	state := MCPServerResourceModel{DCRBridge: types.BoolValue(true)}
	hydration := map[string]interface{}{"dcr_bridge": true}
	config := MCPServerResourceModel{}
	candidate := mcpFieldOwnership{Owned: map[string]bool{}, Removals: map[string]bool{}, Versioned: true}
	if err := validateMCPImplicitClearSafety(config, state, candidate, hydration, map[string]interface{}{}, true, false); err == nil {
		t.Fatal("omitted observed DCR did not block URL change")
	}

	config.DCRBridge = types.BoolValue(false)
	candidate.Owned[mcpFieldDCRBridgePath] = true
	if err := validateMCPImplicitClearSafety(config, state, candidate, hydration, map[string]interface{}{"dcr_bridge": false}, true, false); err != nil {
		t.Fatalf("genuinely changed DCR intent was rejected: %v", err)
	}
	config.DCRBridge = types.BoolValue(true)
	if err := validateMCPImplicitClearSafety(config, state, candidate, hydration, map[string]interface{}{}, true, false); err == nil {
		t.Fatal("unchanged DCR takeover did not block URL change")
	}
	if err := validateMCPImplicitClearSafety(config, state, candidate, map[string]interface{}{"dcr_bridge": false}, map[string]interface{}{"dcr_bridge": true}, true, false); err != nil {
		t.Fatalf("owned DCR reconciliation against remote drift was rejected: %v", err)
	}

	config.DCRBridge = types.BoolNull()
	candidate = mcpFieldOwnership{Owned: map[string]bool{}, Removals: map[string]bool{mcpFieldDCRBridgePath: true}, Versioned: true}
	if err := validateMCPImplicitClearSafety(config, state, candidate, hydration, map[string]interface{}{"dcr_bridge": nil}, true, false); err != nil {
		t.Fatalf("explicit owned DCR removal was rejected: %v", err)
	}

	authConfig := MCPServerResourceModel{
		Credentials: stringMapValue(map[string]string{"client_id": "client"}),
		OAuthScopes: stringListValue(),
	}
	authCandidate := mcpFieldOwnership{
		Owned:    map[string]bool{mcpFieldCredentialsPath: true, mcpFieldOAuthScopesPath: true},
		Removals: map[string]bool{}, Versioned: true,
	}
	authDelta := map[string]interface{}{"credentials": map[string]interface{}{"client_id": "client", "scopes": []string{}}}
	if err := validateMCPImplicitClearSafety(authConfig, MCPServerResourceModel{}, authCandidate, map[string]interface{}{}, authDelta, false, true); err != nil {
		t.Fatalf("auth transition without DCR regressed: %v", err)
	}
}

func TestMCP208OwnedFieldsClearAndMismatchProtocol(t *testing.T) {
	state := map[string]interface{}{
		"id": "advanced-clear", "server_id": "advanced-clear", "server_name": "advanced-clear",
		"transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
		"delegate_auth_to_upstream": false, "oauth_passthrough": false, "dcr_bridge": false, "is_byok": true,
		"byok_description": protocolMCPStringList("line"), "byok_api_key_help_url": "https://known.invalid/help",
		"source_url": "https://known.invalid/source", "timeout": 2.5, "max_concurrent_requests": int64(9),
		"field_ownership_generation": int64(1),
	}
	config := map[string]interface{}{
		"server_name": "advanced-clear", "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none",
	}
	changes := map[string]interface{}{
		"delegate_auth_to_upstream": nil, "oauth_passthrough": nil, "dcr_bridge": nil, "is_byok": nil,
		"byok_description": nil, "byok_api_key_help_url": nil, "source_url": nil,
		"timeout": nil, "max_concurrent_requests": nil,
	}
	before := map[string]interface{}{
		"server_id": "advanced-clear", "server_name": "advanced-clear", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		"delegate_auth_to_upstream": false, "oauth_passthrough": false, "dcr_bridge": false, "is_byok": true,
		"byok_description": []string{"line"}, "byok_api_key_help_url": "https://known.invalid/help",
		"source_url": "https://known.invalid/source", "timeout": json.Number("2.5"), "max_concurrent_requests": json.Number("9"),
	}
	after := map[string]interface{}{
		"server_id": "advanced-clear", "server_name": "advanced-clear", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		"delegate_auth_to_upstream": false, "oauth_passthrough": false, "dcr_bridge": nil, "is_byok": false,
		"byok_description": []string{}, "byok_api_key_help_url": nil, "source_url": nil,
		"timeout": nil, "max_concurrent_requests": nil,
	}
	owned := mcpFieldOwnership{Owned: map[string]bool{}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
	for _, fieldPath := range []string{
		mcpFieldDelegateAuthToUpstreamPath, mcpFieldOAuthPassthroughPath, mcpFieldDCRBridgePath,
		mcpFieldIsBYOKPath, mcpFieldBYOKDescriptionPath, mcpFieldBYOKAPIKeyHelpURLPath,
		mcpFieldSourceURLPath, mcpFieldTimeoutPath, mcpFieldMaxConcurrentRequestsPath,
	} {
		owned.Owned[fieldPath] = true
	}

	t.Run("exact readback", func(t *testing.T) {
		result := runMCPUpdateCompletionProtocol(t, state, config, changes, before, after, protocolMCPFieldPrivate(t, owned))
		if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 {
			t.Fatalf("advanced clear failed: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
		}
		for name, want := range map[string]interface{}{
			"delegate_auth_to_upstream": false, "oauth_passthrough": false, "dcr_bridge": nil, "is_byok": false,
			"byok_description": []string{}, "byok_api_key_help_url": nil, "source_url": nil,
			"timeout": nil, "max_concurrent_requests": nil,
		} {
			got, present := result.body[name]
			if !present || !mcpWireValuesEqual(got, want) {
				t.Fatalf("clear sentinel for %s is missing or incorrect: %#v", name, result.body)
			}
		}
		committed := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
		for fieldPath := range owned.Owned {
			if committed.Owned[fieldPath] || !committed.Removals[fieldPath] {
				t.Fatalf("clear ownership was not committed for %s: %#v", fieldPath, committed)
			}
		}
	})

	t.Run("late mismatch retains state and private", func(t *testing.T) {
		mismatched := make(map[string]interface{}, len(after))
		for name, value := range after {
			mismatched[name] = value
		}
		mismatched["max_concurrent_requests"] = json.Number("1")
		private := protocolMCPFieldPrivate(t, owned)
		result := runMCPUpdateCompletionProtocol(t, state, config, changes, before, mismatched, private)
		if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 {
			t.Fatalf("advanced mismatch was not rejected: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
		}
		assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
		if committed := protocolCommittedMCPFieldOwnership(t, result.applied.Private); !mcpFieldOwnershipEqual(committed, owned) {
			t.Fatalf("advanced mismatch changed committed ownership: %#v", committed)
		}
	})
}

func TestMCP208DelegatedAuthOmittedFlowRejectedBeforeMutationProtocol(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "delegate-m2m", "server_id": "delegate-m2m", "server_name": "delegate-m2m",
		"transport": "http", "url": "https://known.invalid/mcp", "auth_type": "oauth2",
		"oauth2_flow": "client_credentials", "delegate_auth_to_upstream": false, "spec_version": "2024-11-05",
	}))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"server_name": "delegate-m2m", "transport": "http", "url": "https://known.invalid/mcp",
		"auth_type": "oauth2", "delegate_auth_to_upstream": true,
	}))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"delegate_auth_to_upstream": true})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed,
		PriorPrivate: protocolMCPFieldPrivate(t, emptyMCPFieldOwnership()),
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("omitted-flow M2M takeover was not rejected: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	if requests.Load() != 0 {
		t.Fatalf("plan-time delegated-auth rejection made %d remote requests", requests.Load())
	}
}

func TestMCP208DCRImplicitClearBlocksPUTProtocol(t *testing.T) {
	state := map[string]interface{}{
		"id": "dcr-block", "server_id": "dcr-block", "server_name": "dcr-block",
		"transport": "http", "url": "https://old.invalid/mcp", "auth_type": "true_passthrough",
		"spec_version": "2024-11-05", "dcr_bridge": true,
	}
	config := map[string]interface{}{
		"server_name": "dcr-block", "transport": "http", "url": "https://new.invalid/mcp", "auth_type": "true_passthrough",
	}
	before := map[string]interface{}{
		"server_id": "dcr-block", "server_name": "dcr-block", "transport": "http",
		"url": "https://old.invalid/mcp", "auth_type": "true_passthrough", "dcr_bridge": true, "mcp_info": map[string]interface{}{},
	}
	private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"url": "https://new.invalid/mcp"}, before, before, private)
	if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
		t.Fatalf("implicit DCR clear was not blocked before PUT: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
	}
	assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
	if committed := protocolCommittedMCPFieldOwnership(t, result.applied.Private); committed.Generation != 0 || len(committed.Owned) != 0 || len(committed.Removals) != 0 {
		t.Fatalf("blocked DCR clear changed committed ownership: %#v", committed)
	}
}
