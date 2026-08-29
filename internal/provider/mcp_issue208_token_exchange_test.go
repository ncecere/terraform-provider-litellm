package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	frameworkdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

var mcpTokenExchangeCanonicalFields = []string{
	"issuer", "token_exchange_endpoint", "audience", "subject_token_type", "token_exchange_profile",
}

func tokenExchangeCredentials(values map[string]attr.Value) types.Map {
	return types.MapValueMust(types.StringType, values)
}

func validateTokenExchangeConfig(data MCPServerResourceModel) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	validateMCPTokenExchangeConfiguration(data, &diagnostics)
	return diagnostics
}

func TestMCPTokenExchangeSchemaV8SensitivityAndResourceOnlyExposure(t *testing.T) {
	ctx := context.Background()
	var resourceResponse frameworkresource.SchemaResponse
	(&MCPServerResource{}).Schema(ctx, frameworkresource.SchemaRequest{}, &resourceResponse)
	if resourceResponse.Schema.Version != 8 {
		t.Fatalf("schema version=%d", resourceResponse.Schema.Version)
	}
	for _, name := range mcpTokenExchangeCanonicalFields {
		attribute, present := resourceResponse.Schema.Attributes[name]
		if !present || !attribute.IsOptional() || !attribute.IsComputed() {
			t.Fatalf("canonical field flags are invalid for %s", name)
		}
		wantSensitive := name == "issuer" || name == "token_exchange_endpoint"
		if attribute.IsSensitive() != wantSensitive {
			t.Fatalf("canonical field sensitivity is invalid for %s", name)
		}
	}
	profile, ok := resourceResponse.Schema.Attributes["token_exchange_profile"].(resourceschema.StringAttribute)
	if !ok || profile.Default != nil || len(profile.Validators) != 1 {
		t.Fatal("token_exchange_profile gained a synthetic default or lost its enum validator")
	}

	var singular frameworkdatasource.SchemaResponse
	(&MCPServerDataSource{}).Schema(ctx, frameworkdatasource.SchemaRequest{}, &singular)
	var listed frameworkdatasource.SchemaResponse
	(&MCPServersListDataSource{}).Schema(ctx, frameworkdatasource.SchemaRequest{}, &listed)
	items := listed.Schema.Attributes["mcp_servers"].(datasourceschema.ListNestedAttribute)
	for _, name := range mcpTokenExchangeCanonicalFields {
		if _, present := singular.Schema.Attributes[name]; present {
			t.Fatalf("role-masked field %s was exposed by the singular data source", name)
		}
		if _, present := items.NestedObject.Attributes[name]; present {
			t.Fatalf("role-masked field %s was exposed by the list data source", name)
		}
	}
}

func TestMCPTokenExchangeDirectV0ThroughV7UpgradePreservesCredentials(t *testing.T) {
	ctx := context.Background()
	upgraders := (&MCPServerResource{}).UpgradeState(ctx)
	for version := int64(0); version <= 7; version++ {
		prior := map[string]interface{}{
			"id": "upgrade", "server_id": "upgrade", "transport": "http",
			"credentials": map[string]interface{}{
				"client_id": " cid ", "client_secret": "sec",
				"token_exchange_endpoint": "https://legacy.invalid/token?x=1&y=2",
				"audience":                "api://legacy", "subject_token_type": "urn:legacy:exact",
				"token_exchange_profile": "entra_obo", "upstream_resource": "auto",
			},
			"field_ownership_generation": float64(17),
		}
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
		for _, name := range mcpTokenExchangeCanonicalFields {
			if string(upgraded[name]) != "null" {
				t.Fatalf("v%d %s=%s", version, name, upgraded[name])
			}
		}
		var beforeCredentials, afterCredentials map[string]interface{}
		_ = json.Unmarshal(json.RawMessage(mustJSON(t, prior["credentials"])), &beforeCredentials)
		_ = json.Unmarshal(upgraded["credentials"], &afterCredentials)
		if !reflect.DeepEqual(beforeCredentials, afterCredentials) {
			t.Fatalf("v%d rewrote credential aliases: before=%#v after=%#v", version, beforeCredentials, afterCredentials)
		}
		if string(upgraded["field_ownership_generation"]) != "17" {
			t.Fatalf("v%d changed the existing ownership generation", version)
		}
	}
}

func mustJSON(t *testing.T, value interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestMCPTokenExchangeSourceAndRuntimeValidation(t *testing.T) {
	for _, name := range mcpCredentialLiftedColumnNames {
		canonical := MCPServerResourceModel{AuthType: types.StringValue("oauth2_token_exchange")}
		switch name {
		case "token_exchange_endpoint":
			canonical.TokenExchangeEndpoint = types.StringValue("same")
		case "audience":
			canonical.Audience = types.StringValue("same")
		case "subject_token_type":
			canonical.SubjectTokenType = types.StringValue("same")
		case "token_exchange_profile":
			canonical.TokenExchangeProfile = types.StringValue("rfc8693")
		}
		legacyValue := "same"
		if name == "token_exchange_profile" {
			legacyValue = "rfc8693"
		}
		canonical.Credentials = tokenExchangeCredentials(map[string]attr.Value{
			"client_id": types.StringValue("client"), "client_secret": types.StringValue("secret"), name: types.StringValue(legacyValue),
		})
		if diagnostics := validateTokenExchangeConfig(canonical); !diagnostics.HasError() {
			t.Fatalf("equal dual source was accepted for %s", name)
		}
		canonical.Credentials = types.MapUnknown(types.StringType)
		if diagnostics := validateTokenExchangeConfig(canonical); !diagnostics.HasError() {
			t.Fatalf("unknown credentials did not block canonical %s", name)
		}
	}

	completeCredentials := tokenExchangeCredentials(map[string]attr.Value{
		"client_id": types.StringValue("client"), "client_secret": types.StringValue("secret"),
	})
	base := MCPServerResourceModel{
		AuthType: types.StringValue("oauth2_token_exchange"), Credentials: completeCredentials,
		TokenExchangeEndpoint: types.StringValue("https://idp.invalid/token"),
	}
	if diagnostics := validateTokenExchangeConfig(base); diagnostics.HasError() {
		t.Fatalf("complete RFC 8693 config was rejected: %v", diagnostics)
	}

	for _, test := range []struct {
		name string
		data MCPServerResourceModel
	}{
		{
			name: "equal dual source",
			data: MCPServerResourceModel{AuthType: types.StringValue("oauth2_token_exchange"), TokenExchangeEndpoint: types.StringValue("same-secret-marker"), Credentials: tokenExchangeCredentials(map[string]attr.Value{
				"client_id": types.StringValue("client"), "client_secret": types.StringValue("secret"), "token_exchange_endpoint": types.StringValue("same-secret-marker"),
			})},
		},
		{name: "canonical with unknown credentials", data: MCPServerResourceModel{AuthType: types.StringValue("oauth2_token_exchange"), TokenExchangeEndpoint: types.StringValue("unknown-secret-marker"), Credentials: types.MapUnknown(types.StringType)}},
		{name: "missing explicit client secret", data: MCPServerResourceModel{AuthType: types.StringValue("oauth2_token_exchange"), TokenExchangeEndpoint: types.StringValue("endpoint-secret-marker"), Credentials: tokenExchangeCredentials(map[string]attr.Value{"client_id": types.StringValue("client")})}},
		{name: "issuer token exchange missing explicit clients", data: MCPServerResourceModel{AuthType: types.StringValue("oauth2_token_exchange"), Issuer: types.StringValue("issuer-secret-marker")}},
		{name: "endpoint wrong auth", data: MCPServerResourceModel{AuthType: types.StringValue("oauth2"), OAuth2Flow: types.StringValue("client_credentials"), TokenExchangeEndpoint: types.StringValue("endpoint-secret-marker"), Credentials: completeCredentials}},
		{name: "profile wrong auth", data: MCPServerResourceModel{AuthType: types.StringValue("oauth2_id_jag"), TokenExchangeProfile: types.StringValue("rfc8693"), Credentials: completeCredentials}},
		{name: "issuer wrong auth", data: MCPServerResourceModel{AuthType: types.StringValue("oauth2_id_jag"), Issuer: types.StringValue("issuer-secret-marker")}},
		{name: "oauth audience wrong flow", data: MCPServerResourceModel{AuthType: types.StringValue("oauth2"), OAuth2Flow: types.StringValue("authorization_code"), Audience: types.StringValue("audience-secret-marker")}},
		{name: "entra missing scopes", data: MCPServerResourceModel{AuthType: types.StringValue("oauth2_token_exchange"), Credentials: completeCredentials, TokenExchangeProfile: types.StringValue("entra_obo")}},
		{name: "entra unknown scopes", data: MCPServerResourceModel{AuthType: types.StringValue("oauth2_token_exchange"), Credentials: completeCredentials, TokenExchangeProfile: types.StringValue("entra_obo"), OAuthScopes: types.ListUnknown(types.StringType)}},
		{name: "entra missing explicit endpoint", data: MCPServerResourceModel{AuthType: types.StringValue("oauth2_token_exchange"), Credentials: completeCredentials, TokenExchangeProfile: types.StringValue("entra_obo"), OAuthScopes: stringListValue("api://target/.default")}},
		{name: "entra unused audience", data: MCPServerResourceModel{AuthType: types.StringValue("oauth2_token_exchange"), Credentials: completeCredentials, TokenExchangeEndpoint: types.StringValue("endpoint-secret-marker"), TokenExchangeProfile: types.StringValue("entra_obo"), OAuthScopes: stringListValue("api://target/.default"), Audience: types.StringValue("audience-secret-marker")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagnostics := validateTokenExchangeConfig(test.data)
			if !diagnostics.HasError() {
				t.Fatal("invalid token-exchange configuration was accepted")
			}
			text := strings.ToLower(diagnostics.Errors()[0].Summary() + diagnostics.Errors()[0].Detail())
			for _, protected := range []string{"same-secret-marker", "unknown-secret-marker", "endpoint-secret-marker", "issuer-secret-marker", "audience-secret-marker"} {
				if strings.Contains(text, protected) {
					t.Fatalf("diagnostic exposed a configured value: %s", text)
				}
			}
		})
	}

	entra := MCPServerResourceModel{
		AuthType: types.StringValue("oauth2_token_exchange"), Credentials: completeCredentials,
		TokenExchangeEndpoint: types.StringValue("https://login.invalid/token"), TokenExchangeProfile: types.StringValue("entra_obo"),
		OAuthScopes: stringListValue("api://target/.default"),
	}
	if diagnostics := validateTokenExchangeConfig(entra); diagnostics.HasError() {
		t.Fatalf("complete Entra OBO config was rejected: %v", diagnostics)
	}
	legacyEntra := MCPServerResourceModel{
		AuthType: types.StringValue("oauth2_token_exchange"), OAuthScopes: stringListValue("api://target/.default"),
		Credentials: tokenExchangeCredentials(map[string]attr.Value{
			"client_id": types.StringValue("client"), "client_secret": types.StringValue("secret"),
			"token_exchange_endpoint": types.StringValue("https://login.invalid/token"),
			"token_exchange_profile":  types.StringValue("entra_obo"), "audience": types.StringValue("legacy-unused"),
		}),
	}
	if diagnostics := validateTokenExchangeConfig(legacyEntra); diagnostics.HasError() || len(diagnostics.Warnings()) == 0 {
		t.Fatalf("legacy Entra compatibility shape was not retained with a warning: %v", diagnostics)
	}
	legacyFutureProfile := MCPServerResourceModel{
		AuthType: types.StringValue("oauth2_token_exchange"),
		Credentials: tokenExchangeCredentials(map[string]attr.Value{
			"client_id": types.StringValue("client"), "client_secret": types.StringValue("secret"),
			"token_exchange_endpoint": types.StringValue("https://identity.invalid/token"),
			"token_exchange_profile":  types.StringValue("future_profile"),
		}),
	}
	if diagnostics := validateTokenExchangeConfig(legacyFutureProfile); diagnostics.HasError() {
		t.Fatalf("historically accepted legacy profile was reinterpreted or rejected: %v", diagnostics)
	}
	unknownAuth := base
	unknownAuth.AuthType = types.StringUnknown()
	if diagnostics := validateTokenExchangeConfig(unknownAuth); diagnostics.HasError() {
		t.Fatalf("unknown authentication dependency did not defer: %v", diagnostics)
	}
	conditionalConfig := MCPServerResourceModel{
		AuthType: types.StringValue("oauth2_token_exchange"), Credentials: types.MapUnknown(types.StringType),
		TokenExchangeEndpoint: types.StringUnknown(), Audience: types.StringUnknown(),
	}
	conditionalPlan := conditionalConfig
	conditionalPlan.Credentials = completeCredentials
	conditionalPlan.TokenExchangeEndpoint = types.StringValue("https://identity.invalid/token")
	conditionalPlan.Audience = types.StringValue("api://resolved")
	resolved := resolveMCPTokenExchangeValidationConfig(conditionalConfig, conditionalPlan)
	if diagnostics := validateTokenExchangeConfig(resolved); diagnostics.HasError() {
		t.Fatalf("jointly resolved conditional token-exchange values were rejected: %v", diagnostics)
	}

	variableConfig := conditionalPlan
	variableConfig.Credentials = tokenExchangeCredentials(map[string]attr.Value{
		"client_id":                  types.StringUnknown(),
		"client_secret":              types.StringUnknown(),
		"token_endpoint_auth_method": types.StringValue("client_secret_basic"),
	})
	var configDiagnostics diag.Diagnostics
	validateMCPTokenExchangeConfigurationForConfig(variableConfig, &configDiagnostics)
	if configDiagnostics.HasError() {
		t.Fatalf("configuration-time unknown credential values did not defer to the proposed plan: %v", configDiagnostics)
	}
	resolved = resolveMCPTokenExchangeValidationConfig(variableConfig, conditionalPlan)
	if diagnostics := validateTokenExchangeConfig(resolved); diagnostics.HasError() {
		t.Fatalf("jointly resolved credential map values were rejected: %v", diagnostics)
	}
}

func TestMCPTokenExchangeLegacyWireLiftingAndCanonicalWireShape(t *testing.T) {
	ctx := context.Background()
	legacy := MCPServerResourceModel{
		ServerName: types.StringValue("legacy"), Transport: types.StringValue("http"), URL: types.StringValue("https://mcp.invalid"),
		AuthType: types.StringValue("oauth2_token_exchange"),
		Credentials: stringMapValue(map[string]string{
			"client_id": "client", "client_secret": "secret", "token_endpoint_auth_method": "client_secret_basic",
			"upstream_resource": "auto", "token_exchange_endpoint": "https://idp.invalid/token",
			"audience": "api://target", "subject_token_type": "urn:example:subject", "token_exchange_profile": "rfc8693",
		}),
		OAuthScopes: stringListValue("scope.one"),
	}
	request, err := (&MCPServerResource{}).buildMCPServerCreateRequest(ctx, &legacy, &legacy, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range mcpCredentialLiftedColumnNames {
		if _, present := request[name]; !present {
			t.Fatalf("legacy alias %s was not lifted", name)
		}
	}
	credentials, ok := request["credentials"].(map[string]interface{})
	if !ok {
		t.Fatalf("credentials wire shape=%T", request["credentials"])
	}
	for _, name := range mcpCredentialLiftedColumnNames {
		if _, present := credentials[name]; present {
			t.Fatalf("legacy alias %s remained in credentials: %#v", name, credentials)
		}
	}
	for _, name := range []string{"client_id", "client_secret", "token_endpoint_auth_method", "upstream_resource", "scopes"} {
		if _, present := credentials[name]; !present {
			t.Fatalf("credentials-native field %s was stripped: %#v", name, credentials)
		}
	}

	canonical := legacy
	canonical.Credentials = stringMapValue(map[string]string{"client_id": "client", "client_secret": "secret"})
	canonical.TokenExchangeEndpoint = types.StringValue("https://canonical.invalid/token")
	canonical.Audience = types.StringValue("api://canonical")
	canonical.SubjectTokenType = types.StringValue("urn:canonical:subject")
	canonical.TokenExchangeProfile = types.StringValue("rfc8693")
	canonicalRequest, err := (&MCPServerResource{}).buildMCPServerCreateRequest(ctx, &canonical, &canonical, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalRequest["token_exchange_endpoint"] != "https://canonical.invalid/token" {
		t.Fatalf("canonical endpoint was not sent: %#v", canonicalRequest)
	}
	if _, present := canonicalRequest["issuer"]; present {
		t.Fatal("an issuer default was synthesized")
	}
}

func TestMCPTokenExchangeLegacyReadDriftAndImportMasking(t *testing.T) {
	ctx := context.Background()
	response := map[string]interface{}{
		"server_id": "projection", "server_name": "projection", "transport": "http", "auth_type": "oauth2_token_exchange",
		"token_exchange_endpoint": "https://remote.invalid/token", "audience": "api://remote",
		"subject_token_type": "urn:remote:subject", "token_exchange_profile": "entra_obo",
	}
	legacy := MCPServerResourceModel{
		ID: types.StringValue("projection"), ServerID: types.StringValue("projection"),
		Credentials: stringMapValue(map[string]string{
			"client_id": "client", "token_exchange_endpoint": "https://old.invalid/token", "audience": "api://old",
			"subject_token_type": "urn:old:subject", "token_exchange_profile": "rfc8693",
		}),
		Issuer: types.StringNull(), TokenExchangeEndpoint: types.StringNull(), Audience: types.StringNull(),
		SubjectTokenType: types.StringNull(), TokenExchangeProfile: types.StringNull(),
	}
	ownership := mcpFieldOwnership{Owned: map[string]bool{mcpFieldCredentialsPath: true}, Removals: map[string]bool{}, Versioned: true}
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &legacy, response, emptyMCPInfoProvenance(), ownership, false, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatal(err)
	}
	if !legacy.TokenExchangeEndpoint.IsNull() || !legacy.Audience.IsNull() || !legacy.SubjectTokenType.IsNull() || !legacy.TokenExchangeProfile.IsNull() {
		t.Fatal("legacy credential ownership projected canonical siblings")
	}
	credentials, err := mcpFieldStringMap(ctx, legacy.Credentials)
	if err != nil || credentials["audience"] != "api://remote" || credentials["token_exchange_profile"] != "entra_obo" {
		t.Fatalf("observable legacy drift was not projected: %#v err=%v", credentials, err)
	}
	response["token_exchange_profile"] = "future_profile"
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &legacy, response, emptyMCPInfoProvenance(), ownership, false, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatalf("historically accepted legacy profile drift was rejected: %v", err)
	}
	credentials, err = mcpFieldStringMap(ctx, legacy.Credentials)
	if err != nil || credentials["token_exchange_profile"] != "future_profile" {
		t.Fatalf("legacy profile compatibility was lost: %#v err=%v", credentials, err)
	}
	response["token_exchange_profile"] = "entra_obo"

	imported := MCPServerResourceModel{ID: types.StringValue("projection"), ServerID: types.StringValue("projection")}
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &imported, response, emptyMCPInfoProvenance(), emptyMCPFieldOwnership(), true, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatal(err)
	}
	if imported.TokenExchangeEndpoint.ValueString() != "https://remote.invalid/token" || imported.TokenExchangeProfile.ValueString() != "entra_obo" {
		t.Fatal("full-admin import did not project visible canonical siblings")
	}
	masked := map[string]interface{}{"server_id": "projection", "server_name": "projection", "transport": "http", "auth_type": "oauth2_token_exchange", "token_exchange_endpoint": nil, "audience": nil, "subject_token_type": nil, "token_exchange_profile": nil, "issuer": nil}
	before := imported
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &imported, masked, emptyMCPInfoProvenance(), emptyMCPFieldOwnership(), false, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatal(err)
	}
	if !before.TokenExchangeEndpoint.Equal(imported.TokenExchangeEndpoint) || !before.TokenExchangeProfile.Equal(imported.TokenExchangeProfile) {
		t.Fatal("role-masked null erased known imported state")
	}
}

func TestMCPTokenExchangeProjectionRejectsMalformedTypesAndProfileTransactionally(t *testing.T) {
	ctx := context.Background()
	data := MCPServerResourceModel{ID: types.StringValue("malformed"), ServerID: types.StringValue("malformed"), Description: types.StringValue("before"), TokenExchangeProfile: types.StringValue("rfc8693")}
	malformedFields := map[string]interface{}{
		"issuer": false, "token_exchange_endpoint": false, "audience": false, "subject_token_type": false,
		"token_exchange_profile": false,
	}
	for name, malformed := range malformedFields {
		response := map[string]interface{}{
			"server_id": "malformed", "server_name": "changed", "transport": "http", "auth_type": "oauth2_token_exchange", name: malformed,
		}
		before := data
		if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &data, response, emptyMCPInfoProvenance(), emptyMCPFieldOwnership(), true, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err == nil {
			t.Fatalf("malformed %s was accepted", name)
		}
		if !reflect.DeepEqual(before, data) {
			t.Fatal("malformed late response partially changed resource state")
		}
	}
	invalidProfile := map[string]interface{}{
		"server_id": "malformed", "server_name": "changed", "transport": "http", "auth_type": "oauth2_token_exchange", "token_exchange_profile": "unsupported",
	}
	before := data
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &data, invalidProfile, emptyMCPInfoProvenance(), emptyMCPFieldOwnership(), true, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err == nil {
		t.Fatal("unsupported profile was accepted")
	}
	if !reflect.DeepEqual(before, data) {
		t.Fatal("invalid profile partially changed resource state")
	}
	freshImport := MCPServerResourceModel{ID: types.StringValue("malformed"), ServerID: types.StringValue("malformed"), TokenExchangeProfile: types.StringUnknown()}
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &freshImport, invalidProfile, emptyMCPInfoProvenance(), emptyMCPFieldOwnership(), true, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatalf("unowned historical profile prevented import compatibility: %v", err)
	}
	if !freshImport.TokenExchangeProfile.IsNull() {
		t.Fatal("unrepresentable imported profile was projected into the canonical sibling")
	}
}

func TestMCPTokenExchangeAtomicAliasHandoffAndMergeRejection(t *testing.T) {
	ctx := context.Background()
	state := MCPServerResourceModel{
		AuthType:    types.StringValue("oauth2_token_exchange"),
		Credentials: stringMapValue(map[string]string{"client_id": "client", "client_secret": "secret", "audience": "api://same", "upstream_resource": "auto"}),
		Audience:    types.StringNull(), OAuthScopes: stringListValue("scope"),
	}
	config := state
	config.Credentials = stringMapValue(map[string]string{"client_id": "client", "client_secret": "secret", "upstream_resource": "auto"})
	config.Audience = types.StringValue("api://same")
	plan := config
	committed := mcpFieldOwnership{
		Owned: map[string]bool{mcpFieldCredentialsPath: true, mcpFieldOAuthScopesPath: true}, Removals: map[string]bool{},
		CredentialClass: "oauth2_token_exchange", CredentialKeys: []string{"audience", "client_id", "client_secret", "upstream_resource"}, Generation: 1, Versioned: true,
	}
	candidate := deriveMCPFieldPlanOwnership(committed, config)
	delta, err := buildMCPFieldDelta(ctx, plan, config, state, committed, candidate, map[string]interface{}{"auth_type": "oauth2_token_exchange", "audience": "api://same", "credentials": map[string]interface{}{"upstream_resource": "auto"}}, false)
	if err != nil {
		t.Fatal(err)
	}
	if delta["audience"] != "api://same" {
		t.Fatalf("equal-value handoff did not force the canonical sibling: %#v", delta)
	}
	credentials, ok := delta["credentials"].(map[string]string)
	if !ok {
		t.Fatalf("handoff credential shape=%T", delta["credentials"])
	}
	if _, present := credentials["audience"]; present {
		t.Fatalf("handoff retained the lifted alias: %#v", credentials)
	}

	badConfig := state
	badConfig.Credentials = stringMapValue(map[string]string{"client_id": "client", "client_secret": "secret", "audience": "api://same"})
	badCandidate := deriveMCPFieldPlanOwnership(committed, badConfig)
	if _, err := buildMCPFieldDelta(ctx, badConfig, badConfig, state, committed, badCandidate, map[string]interface{}{"auth_type": "oauth2_token_exchange", "audience": "api://same"}, false); err == nil {
		t.Fatal("non-lifted credential-key deletion bypassed merge-only rejection")
	}
}

func TestMCPTokenExchangeImplicitClearPreflightIncludesIssuerAndAllScopedFields(t *testing.T) {
	state := MCPServerResourceModel{
		Issuer: types.StringValue("https://old.invalid"), AuthorizationURL: types.StringValue("https://old.invalid/authorize"),
		TokenURL: types.StringValue("https://old.invalid/token"), RegistrationURL: types.StringValue("https://old.invalid/register"),
		OAuth2Flow: types.StringValue("authorization_code"), DCRBridge: types.BoolValue(true),
		TokenExchangeEndpoint: types.StringValue("https://old.invalid/exchange"), Audience: types.StringValue("api://old"),
		SubjectTokenType: types.StringValue("urn:old"), TokenExchangeProfile: types.StringValue("rfc8693"),
	}
	hydration := map[string]interface{}{
		"issuer": state.Issuer.ValueString(), "authorization_url": state.AuthorizationURL.ValueString(),
		"token_url": state.TokenURL.ValueString(), "registration_url": state.RegistrationURL.ValueString(),
		"oauth2_flow": state.OAuth2Flow.ValueString(), "dcr_bridge": true,
		"token_exchange_endpoint": state.TokenExchangeEndpoint.ValueString(), "audience": state.Audience.ValueString(),
		"subject_token_type": state.SubjectTokenType.ValueString(), "token_exchange_profile": state.TokenExchangeProfile.ValueString(),
	}
	if err := validateMCPImplicitClearSafety(MCPServerResourceModel{}, state, emptyMCPFieldOwnership(), hydration, map[string]interface{}{"issuer": "https://new.invalid"}, false, false, true); err == nil {
		t.Fatal("issuer change did not protect an unchanged affected value")
	}
	// Establishing the first nonblank issuer is not destructive.
	firstState := state
	firstState.Issuer = types.StringNull()
	if err := validateMCPImplicitClearSafety(MCPServerResourceModel{}, firstState, emptyMCPFieldOwnership(), map[string]interface{}{}, map[string]interface{}{"issuer": "https://first.invalid"}, false, false, false); err != nil {
		t.Fatalf("first issuer establishment was rejected: %v", err)
	}

	config := state
	planned := mcpFieldOwnership{Owned: map[string]bool{}, Removals: map[string]bool{}, Versioned: true}
	delta := map[string]interface{}{}
	for _, item := range []struct {
		path string
		name string
	}{
		{mcpFieldIssuerPath, "issuer"}, {mcpFieldAuthorizationURLPath, "authorization_url"},
		{mcpFieldTokenURLPath, "token_url"}, {mcpFieldRegistrationURLPath, "registration_url"},
		{mcpFieldOAuth2FlowPath, "oauth2_flow"}, {mcpFieldTokenExchangeEndpointPath, "token_exchange_endpoint"},
		{mcpFieldAudiencePath, "audience"}, {mcpFieldSubjectTokenTypePath, "subject_token_type"},
		{mcpFieldTokenExchangeProfilePath, "token_exchange_profile"},
	} {
		planned.Owned[item.path] = true
		delta[item.name] = hydration[item.name].(string) + "/changed"
	}
	planned.Owned[mcpFieldDCRBridgePath] = true
	config.Issuer = types.StringValue(delta["issuer"].(string))
	config.AuthorizationURL = types.StringValue(delta["authorization_url"].(string))
	config.TokenURL = types.StringValue(delta["token_url"].(string))
	config.RegistrationURL = types.StringValue(delta["registration_url"].(string))
	config.OAuth2Flow = types.StringValue(delta["oauth2_flow"].(string))
	config.TokenExchangeEndpoint = types.StringValue(delta["token_exchange_endpoint"].(string))
	config.Audience = types.StringValue(delta["audience"].(string))
	config.SubjectTokenType = types.StringValue(delta["subject_token_type"].(string))
	config.TokenExchangeProfile = types.StringValue(delta["token_exchange_profile"].(string))
	config.DCRBridge = types.BoolValue(false)
	delta["dcr_bridge"] = false
	if err := validateMCPImplicitClearSafety(config, state, planned, hydration, delta, false, false, true); err != nil {
		t.Fatalf("complete genuinely changed scoped intent was rejected: %v", err)
	}
}

func TestMCPTokenExchangeCanonicalCreateProtocol(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int64
	var postBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
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
				"server_id": body["server_id"], "server_name": "canonical_create", "transport": "http", "auth_type": "oauth2_token_exchange",
				"url": "https://mcp.invalid", "issuer": "https://issuer.invalid", "token_exchange_endpoint": "https://idp.invalid/token",
				"audience": "api://target", "subject_token_type": "urn:subject", "token_exchange_profile": "rfc8693", "mcp_info": map[string]interface{}{},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	configValues := map[string]interface{}{
		"server_name": "canonical_create", "transport": "http", "url": "https://mcp.invalid", "auth_type": "oauth2_token_exchange",
		"issuer": "https://issuer.invalid", "token_exchange_endpoint": "https://idp.invalid/token", "audience": "api://target",
		"subject_token_type": "urn:subject", "token_exchange_profile": "rfc8693",
		"credentials": map[string]tftypes.Value{
			"client_id": tftypes.NewValue(tftypes.String, "client"), "client_secret": tftypes.NewValue(tftypes.String, "secret"),
		},
		"oauth_scopes": protocolMCPStringList("scope.one"),
	}
	config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, configValues)
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) || requests.Load() != 2 {
		t.Fatalf("canonical create: err=%v diagnostics=%s requests=%d", err, agentProtocolDiagnosticsText(created.Diagnostics), requests.Load())
	}
	body := postBody.Load().(map[string]interface{})
	credentials, ok := body["credentials"].(map[string]interface{})
	if !ok {
		t.Fatalf("create credentials=%T", body["credentials"])
	}
	for _, alias := range mcpCredentialLiftedColumnNames {
		if _, present := credentials[alias]; present {
			t.Fatalf("canonical create put alias %s in credentials", alias)
		}
	}
	ownership := protocolCommittedMCPFieldOwnership(t, created.Private)
	for _, fieldPath := range []string{mcpFieldIssuerPath, mcpFieldTokenExchangeEndpointPath, mcpFieldAudiencePath, mcpFieldSubjectTokenTypePath, mcpFieldTokenExchangeProfilePath} {
		if !ownership.Owned[fieldPath] {
			t.Fatalf("canonical create did not commit %s ownership: %#v", fieldPath, ownership)
		}
	}
}

func TestMCPTokenExchangeDualSourceUnknownPlanProtocolNoRequests(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "unknown-source", "server_id": "unknown-source", "server_name": "unknown_source", "transport": "http", "url": "https://mcp.invalid", "auth_type": "oauth2_token_exchange", "spec_version": "2024-11-05",
	}))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"server_name": "unknown_source", "transport": "http", "url": "https://mcp.invalid", "auth_type": "oauth2_token_exchange",
		"token_exchange_endpoint": "https://idp.invalid/token", "credentials": tftypes.UnknownValue,
	}))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"token_exchange_endpoint": "https://idp.invalid/token", "credentials": tftypes.UnknownValue})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: protocolMCPFieldPrivate(t, emptyMCPFieldOwnership()),
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || requests.Load() != 0 {
		t.Fatalf("unknown dual-source absence was not rejected locally: err=%v diagnostics=%s requests=%d", err, agentProtocolDiagnosticsText(planned.Diagnostics), requests.Load())
	}
}
