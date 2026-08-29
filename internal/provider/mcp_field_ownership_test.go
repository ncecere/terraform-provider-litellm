package provider

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMCPFieldOwnershipGrammarCanonicalAndValueFree(t *testing.T) {
	owned := map[string]bool{}
	for _, fieldPath := range mcpFieldPaths {
		owned[fieldPath] = true
	}
	ownership := mcpFieldOwnership{Owned: owned, Removals: map[string]bool{}, CredentialClass: "api_key", CredentialKeys: []string{"auth_value"}, Generation: 7, Versioned: true}
	raw := encodeMCPFieldOwnership(ownership)
	decoded, err := decodeMCPFieldOwnership(raw)
	if err != nil || !mcpFieldOwnershipEqual(decoded, ownership) {
		t.Fatalf("canonical ownership did not decode: decoded=%#v err=%v", decoded, err)
	}
	var wire map[string]interface{}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 7 {
		t.Fatalf("private grammar keys = %#v", wire)
	}
	paths := append([]string(nil), mcpFieldPaths...)
	slices.Sort(paths)
	got := make([]string, 0, len(wire["owned_paths"].([]interface{})))
	for _, value := range wire["owned_paths"].([]interface{}) {
		got = append(got, value.(string))
	}
	if !slices.Equal(got, paths) {
		t.Fatalf("owned paths are not canonical: %v", got)
	}
	for _, forbidden := range []string{"secret", "value", "hash"} {
		if _, present := wire[forbidden]; present {
			t.Fatalf("private ownership contains forbidden %q member", forbidden)
		}
	}
}

func TestMCPFieldOwnershipGrammarRejectsMalformedIntent(t *testing.T) {
	valid := mcpFieldOwnership{Owned: map[string]bool{mcpFieldAliasPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
	var wire map[string]interface{}
	if err := json.Unmarshal(encodeMCPFieldOwnership(valid), &wire); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(map[string]interface{}){
		"unknown member": func(value map[string]interface{}) { value["future"] = true },
		"unknown path":   func(value map[string]interface{}) { value["owned_paths"] = []string{"/future"} },
		"duplicate path": func(value map[string]interface{}) {
			value["owned_paths"] = []string{mcpFieldAliasPath, mcpFieldAliasPath}
		},
		"unsorted paths": func(value map[string]interface{}) {
			value["owned_paths"] = []string{mcpFieldTokenURLPath, mcpFieldAliasPath}
		},
		"digest mismatch": func(value map[string]interface{}) { value["intent_digest"] = string(make([]byte, 64)) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			copyWire := map[string]interface{}{}
			for key, value := range wire {
				copyWire[key] = value
			}
			mutate(copyWire)
			raw, _ := json.Marshal(copyWire)
			if _, err := decodeMCPFieldOwnership(raw); err == nil {
				t.Fatalf("malformed ownership accepted: %s", raw)
			}
		})
	}
}

func TestMCPFieldRemovalSentinelsV198(t *testing.T) {
	want := map[string]interface{}{
		mcpFieldAliasPath: nil, mcpFieldDescriptionPath: nil, mcpFieldCommandPath: nil,
		mcpFieldAuthorizationURLPath: nil, mcpFieldTokenURLPath: nil, mcpFieldRegistrationURLPath: nil,
		mcpFieldAccessGroupsPath: []string{}, mcpFieldArgsPath: []string{}, mcpFieldAllowedToolsPath: []string{}, mcpFieldExtraHeadersPath: []string{},
		mcpFieldEnvPath: map[string]string{}, mcpFieldEnvVarsPath: []map[string]interface{}{}, mcpFieldStaticHeadersPath: map[string]string{}, mcpFieldCredentialsPath: nil,
		mcpFieldAllowAllKeysPath: false, mcpFieldOAuthScopesPath: []string{}, mcpFieldAvailablePublicInternetPath: true,
		mcpFieldOAuth2FlowPath: nil, mcpFieldInstructionsPath: nil,
		mcpFieldToolNameToDisplayNamePath: map[string]string{}, mcpFieldToolNameToDescriptionPath: map[string]string{},
		mcpFieldDelegateAuthToUpstreamPath: false, mcpFieldOAuthPassthroughPath: false, mcpFieldDCRBridgePath: nil,
		mcpFieldIsBYOKPath: false, mcpFieldBYOKDescriptionPath: []string{}, mcpFieldBYOKAPIKeyHelpURLPath: nil,
		mcpFieldSourceURLPath: nil, mcpFieldTimeoutPath: nil, mcpFieldMaxConcurrentRequestsPath: nil,
	}
	if len(want) != 30 || len(mcpFieldPaths) != 30 {
		t.Fatalf("sentinel inventory changed: want=%d paths=%d", len(want), len(mcpFieldPaths))
	}
	for _, fieldPath := range mcpFieldPaths {
		if !mcpWireValuesEqual(want[fieldPath], mcpFieldRemovalSentinel(fieldPath)) {
			t.Fatalf("wrong v1.98 sentinel for %s: %#v", fieldPath, mcpFieldRemovalSentinel(fieldPath))
		}
	}
	if mcpFieldRemovalSentinel(mcpFieldCredentialsPath) != nil {
		t.Fatal("credentials clear must be top-level null, never an empty object")
	}
}

func TestMCPFieldOwnershipTakeoverRemovalUnknownAndReacquire(t *testing.T) {
	config := MCPServerResourceModel{Alias: types.StringValue("same")}
	first := deriveMCPFieldPlanOwnership(emptyMCPFieldOwnership(), config)
	if !first.Owned[mcpFieldAliasPath] || len(first.Removals) != 0 || first.Generation != 1 {
		t.Fatalf("takeover = %#v", first)
	}
	unknown := config
	unknown.Alias = types.StringUnknown()
	retained := deriveMCPFieldPlanOwnership(committedMCPFieldOwnership(first), unknown)
	if !retained.Owned[mcpFieldAliasPath] || retained.Generation != first.Generation {
		t.Fatalf("unknown did not retain ownership: %#v", retained)
	}
	removed := deriveMCPFieldPlanOwnership(committedMCPFieldOwnership(first), MCPServerResourceModel{})
	if removed.Owned[mcpFieldAliasPath] || !removed.Removals[mcpFieldAliasPath] || removed.Generation != 2 {
		t.Fatalf("removal = %#v", removed)
	}
	steady := deriveMCPFieldPlanOwnership(committedMCPFieldOwnership(removed), MCPServerResourceModel{})
	if !steady.Removals[mcpFieldAliasPath] || steady.Generation != 2 {
		t.Fatalf("post-clear authority was not retained = %#v", steady)
	}
	reacquired := deriveMCPFieldPlanOwnership(committedMCPFieldOwnership(removed), config)
	if !reacquired.Owned[mcpFieldAliasPath] || reacquired.Generation != 3 {
		t.Fatalf("reacquire = %#v", reacquired)
	}
}

func TestMCPFieldLegacyAmbiguousRemovalRequiresTwoSteps(t *testing.T) {
	// Public state is deliberately not an input. An absent legacy grammar plus
	// absent config cannot infer that a value used to be configured and cannot
	// safely clear it on the first upgrade.
	first := deriveMCPFieldPlanOwnership(emptyMCPFieldOwnership(), MCPServerResourceModel{})
	if len(first.Removals) != 0 || len(first.Owned) != 0 {
		t.Fatalf("legacy omission inferred ownership/removal: %#v", first)
	}
	acquired := deriveMCPFieldPlanOwnership(emptyMCPFieldOwnership(), MCPServerResourceModel{Alias: types.StringValue("value")})
	removed := deriveMCPFieldPlanOwnership(committedMCPFieldOwnership(acquired), MCPServerResourceModel{})
	if !removed.Removals[mcpFieldAliasPath] {
		t.Fatalf("second-step removal was not explicit: %#v", removed)
	}
}

func TestMCPServerCreateSendsConfiguredEmptyAndFalseFields(t *testing.T) {
	ctx := context.Background()
	emptyList := types.ListValueMust(types.StringType, nil)
	emptyMap := types.MapValueMust(types.StringType, nil)
	config := MCPServerResourceModel{
		ServerName: types.StringValue("server"), Transport: types.StringValue("http"), AuthType: types.StringValue("none"), URL: types.StringValue("https://example.invalid"),
		Alias: types.StringValue("alias"), Description: types.StringValue(""), MCPAccessGroups: emptyList, Command: types.StringValue(""), Args: emptyList, Env: emptyMap,
		EnvVars: types.ListValueMust(mcpEnvVarObjectType, nil), Credentials: emptyMap, AllowedTools: emptyList, ExtraHeaders: emptyList, StaticHeaders: emptyMap,
		AuthorizationURL: types.StringValue(""), TokenURL: types.StringValue(""), RegistrationURL: types.StringValue(""), AllowAllKeys: types.BoolValue(false),
		OAuthScopes: emptyList, AvailableOnPublicInternet: types.BoolValue(false), OAuth2Flow: types.StringValue("authorization_code"), Instructions: types.StringValue(""),
		ToolNameToDisplayName: emptyMap, ToolNameToDescription: emptyMap,
		DelegateAuthToUpstream: types.BoolValue(false), OAuthPassthrough: types.BoolValue(false), DCRBridge: types.BoolValue(false), IsBYOK: types.BoolValue(false),
		BYOKDescription: emptyList, BYOKAPIKeyHelpURL: types.StringValue(""), SourceURL: types.StringValue(""), Timeout: types.Float64Value(1), MaxConcurrentRequests: types.Int64Value(1),
	}
	request, err := (&MCPServerResource{}).buildMCPServerCreateRequest(ctx, &config, &config, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, fieldPath := range mcpFieldPaths {
		if fieldPath == mcpFieldOAuthScopesPath {
			credentials, ok := request["credentials"].(map[string]interface{})
			if !ok {
				t.Fatalf("native credential object missing from create: %#v", request)
			}
			if scopes, present := credentials["scopes"]; !present || !mcpWireValuesEqual(scopes, []string{}) {
				t.Fatalf("explicit empty scopes omitted from create: %#v", request)
			}
			continue
		}
		if _, present := request[mcpFieldWireName(fieldPath)]; !present {
			t.Fatalf("explicit empty/false field omitted from create: %s (%#v)", fieldPath, request)
		}
	}
}

func TestMCPFieldDeltaEmitsAllAndOnlyV198RemovalSentinels(t *testing.T) {
	ctx := context.Background()
	owned := map[string]bool{}
	for _, fieldPath := range mcpFieldPaths {
		owned[fieldPath] = true
	}
	committed := mcpFieldOwnership{Owned: owned, Removals: map[string]bool{}, Generation: 4, Versioned: true}
	candidate := deriveMCPFieldPlanOwnership(committed, MCPServerResourceModel{})
	state := MCPServerResourceModel{Credentials: types.MapValueMust(types.StringType, map[string]attr.Value{})}
	delta, err := buildMCPFieldDelta(ctx, MCPServerResourceModel{}, MCPServerResourceModel{}, state, committed, candidate, map[string]interface{}{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta) != 29 {
		t.Fatalf("delta contains unrelated or missing sentinel: %#v", delta)
	}
	for _, fieldPath := range mcpFieldPaths {
		if fieldPath == mcpFieldOAuthScopesPath {
			// A simultaneous full credentials clear also clears native scopes; a
			// second top-level alias would be invalid and unsafe.
			continue
		}
		name := mcpFieldWireName(fieldPath)
		got, present := delta[name]
		if !present || !mcpWireValuesEqual(got, mcpFieldRemovalSentinel(fieldPath)) {
			t.Fatalf("sentinel %s = %#v present=%t", name, got, present)
		}
	}
	if _, present := delta["mcp_info"]; present {
		t.Fatal("general removal synthesized #213 mcp_info")
	}
}

func TestMCPObservableCredentialCreateAndReadProjection(t *testing.T) {
	ctx := context.Background()
	credentials := types.MapValueMust(types.StringType, map[string]attr.Value{
		"client_secret":     types.StringValue("secret"),
		"audience":          types.StringValue("audience"),
		"upstream_resource": types.StringValue("resource-old"),
	})
	config := MCPServerResourceModel{Credentials: credentials}
	ownership := mcpFieldOwnership{Owned: map[string]bool{mcpFieldCredentialsPath: true}, Removals: map[string]bool{}, Versioned: true}
	observed := map[string]interface{}{
		"audience":    "audience",
		"credentials": map[string]interface{}{"upstream_resource": "resource-old"},
	}
	if err := verifyMCPFieldCreateReadback(ctx, config, observed, ownership); err != nil {
		t.Fatalf("observable create credentials rejected: %v", err)
	}
	missingLifted := map[string]interface{}{"credentials": map[string]interface{}{"upstream_resource": "resource-old"}}
	if err := verifyMCPFieldCreateReadback(ctx, config, missingLifted, ownership); err == nil {
		t.Fatal("create accepted missing lifted credential persistence")
	}
	missingAdminConfig := map[string]interface{}{"audience": "audience", "credentials": nil}
	if err := verifyMCPFieldCreateReadback(ctx, config, missingAdminConfig, ownership); err == nil {
		t.Fatal("create accepted missing observable credential persistence")
	}

	data := MCPServerResourceModel{
		ID: types.StringValue("credential-read"), ServerID: types.StringValue("credential-read"),
		Credentials: credentials,
	}
	response := map[string]interface{}{
		"server_id": "credential-read", "transport": "http", "audience": "audience-new",
		"credentials": map[string]interface{}{"upstream_resource": "resource-new"},
	}
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &data, response, emptyMCPInfoProvenance(), ownership, false, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatalf("observable credential read projection: %v", err)
	}
	projected, err := mcpFieldStringMap(ctx, data.Credentials)
	if err != nil || projected["client_secret"] != "secret" || projected["audience"] != "audience-new" || projected["upstream_resource"] != "resource-new" {
		t.Fatalf("credential projection did not preserve secrets and expose visible drift: %#v err=%v", projected, err)
	}
	response["credentials"] = nil
	response["audience"] = nil
	if err := (&MCPServerResource{}).readMCPServerResultProjection(ctx, &data, response, emptyMCPInfoProvenance(), ownership, false, mcpInfoLeafSet{}, mcpInfoLeafSet{}); err != nil {
		t.Fatalf("masked credential read projection: %v", err)
	}
	masked, _ := mcpFieldStringMap(ctx, data.Credentials)
	if masked["audience"] != "audience-new" || masked["upstream_resource"] != "resource-new" {
		t.Fatalf("masked read erased prior observable credential state: %#v", masked)
	}
}

func TestMCPImplicitClearPreflightRejectsUnchangedAndAllowsCompleteClear(t *testing.T) {
	hydration := map[string]interface{}{
		"authorization_url": "https://auth.example/authorize", "token_url": "https://auth.example/token", "registration_url": "https://auth.example/register",
	}
	owned := map[string]bool{mcpFieldAuthorizationURLPath: true, mcpFieldTokenURLPath: true, mcpFieldRegistrationURLPath: true}
	planned := mcpFieldOwnership{Owned: cloneMCPFieldSet(owned), Removals: map[string]bool{}, Versioned: true}
	config := MCPServerResourceModel{AuthorizationURL: types.StringValue("https://auth.example/authorize"), TokenURL: types.StringValue("https://auth.example/token"), RegistrationURL: types.StringValue("https://auth.example/register")}
	if err := validateMCPImplicitClearSafety(config, MCPServerResourceModel{}, planned, hydration, map[string]interface{}{}, true, false); err == nil {
		t.Fatal("URL change accepted unchanged OAuth endpoints that v1.98 would clear")
	}
	planned.Owned = map[string]bool{}
	planned.Removals = cloneMCPFieldSet(owned)
	delta := map[string]interface{}{"authorization_url": nil, "token_url": nil, "registration_url": nil}
	if err := validateMCPImplicitClearSafety(MCPServerResourceModel{}, MCPServerResourceModel{}, planned, hydration, delta, true, false); err != nil {
		t.Fatalf("complete explicit OAuth clear rejected: %v", err)
	}
}

func TestMCPConfirmedCredentialClearAllowsSameClassReAdd(t *testing.T) {
	ctx := context.Background()
	credentials := types.MapValueMust(types.StringType, map[string]attr.Value{"auth_value": types.StringValue("replacement")})
	state := MCPServerResourceModel{AuthType: types.StringValue("api_key"), Credentials: types.MapNull(types.StringType)}
	config := MCPServerResourceModel{AuthType: types.StringValue("api_key"), Credentials: credentials}
	plan := config
	committed := mcpFieldOwnership{
		Owned: map[string]bool{}, Removals: map[string]bool{mcpFieldCredentialsPath: true}, Generation: 2, Versioned: true,
	}
	candidate := deriveMCPFieldPlanOwnership(committed, config)
	delta, err := buildMCPFieldDelta(ctx, plan, config, state, committed, candidate, map[string]interface{}{"auth_type": "api_key", "credentials": nil}, false)
	if err != nil {
		t.Fatalf("confirmed-clear re-add rejected: %v", err)
	}
	if !mcpWireValuesEqual(delta["credentials"], map[string]string{"auth_value": "replacement"}) || candidate.Removals[mcpFieldCredentialsPath] {
		t.Fatalf("confirmed-clear re-add was not exact: candidate=%#v delta=%#v", candidate, delta)
	}
}

func TestMCPPendingCredentialClassReplacementRetriesAfterRemoteAuthAdvanced(t *testing.T) {
	ctx := context.Background()
	priorCredentials := types.MapValueMust(types.StringType, map[string]attr.Value{
		"client_secret": types.StringValue("old"), "audience": types.StringValue("old-audience"),
	})
	desiredCredentials := types.MapValueMust(types.StringType, map[string]attr.Value{"auth_value": types.StringValue("replacement")})
	// A refresh after an accepted PUT can observe the new auth class while
	// role-masked credentials still retain the prior Terraform values.
	state := MCPServerResourceModel{AuthType: types.StringValue("api_key"), Credentials: priorCredentials}
	config := MCPServerResourceModel{AuthType: types.StringValue("api_key"), Credentials: desiredCredentials}
	committed := mcpFieldOwnership{
		Owned: map[string]bool{mcpFieldCredentialsPath: true}, Removals: map[string]bool{},
		CredentialClass: "oauth2_token_exchange", CredentialKeys: []string{"audience", "client_secret"}, Generation: 1, Versioned: true,
	}
	candidate := deriveMCPFieldPlanOwnership(committed, config)
	delta, err := buildMCPFieldDelta(ctx, config, config, state, committed, candidate, map[string]interface{}{
		"auth_type": "api_key", "credentials": nil, "audience": nil,
	}, false)
	if err != nil {
		t.Fatalf("pending class replacement retry rejected: %v", err)
	}
	if candidate.CredentialClass != "api_key" || !slices.Equal(candidate.CredentialKeys, []string{"auth_value"}) || !mcpWireValuesEqual(delta["credentials"], map[string]string{"auth_value": "replacement"}) {
		t.Fatalf("pending class replacement retry was not exact: candidate=%#v delta=%#v", candidate, delta)
	}
	if value, present := delta["audience"]; !present || value != nil {
		t.Fatalf("retry omitted prior lifted-key clear: %#v", delta)
	}
}

func TestMCPFieldOwnershipGenerationBinding(t *testing.T) {
	committed := mcpFieldOwnership{Owned: map[string]bool{}, Removals: map[string]bool{mcpFieldCredentialsPath: true}, Generation: 3, Versioned: true}
	if err := validateMCPFieldOwnershipGeneration(types.Int64Value(3), committed); err != nil {
		t.Fatalf("matching generation rejected: %v", err)
	}
	for _, generation := range []types.Int64{types.Int64Value(2), types.Int64Value(4), types.Int64Null(), types.Int64Unknown()} {
		if err := validateMCPFieldOwnershipGeneration(generation, committed); err == nil {
			t.Fatalf("mismatched generation %s accepted", generation)
		}
	}
	if err := validateMCPFieldOwnershipGeneration(types.Int64Value(1), emptyMCPFieldOwnership()); err == nil {
		t.Fatal("missing private ownership accepted for a nonzero public generation")
	}
}

func TestMCPEmptyObservableUpstreamResourceRejected(t *testing.T) {
	if err := validateMCPCredentialStringMapV198(map[string]string{"upstream_resource": ""}); err == nil {
		t.Fatal("empty upstream_resource was accepted even though pinned v1.98 omits it from readback")
	}
	if err := validateMCPCredentialStringMapV198(map[string]string{"upstream_resource": "resource"}); err != nil {
		t.Fatalf("non-empty upstream_resource rejected: %v", err)
	}
}

func TestMCPConfiguredCredentialKeyDeletionRejected(t *testing.T) {
	ctx := context.Background()
	state := MCPServerResourceModel{AuthType: types.StringValue("oauth2"), Credentials: types.MapValueMust(types.StringType, map[string]attr.Value{
		"client_id": types.StringValue("id"), "client_secret": types.StringValue("secret"),
	})}
	config := MCPServerResourceModel{Credentials: types.MapValueMust(types.StringType, map[string]attr.Value{
		"client_id": types.StringValue("id"),
	})}
	ownership := mcpFieldOwnership{Owned: map[string]bool{mcpFieldCredentialsPath: true}, Removals: map[string]bool{}, Versioned: true}
	if err := validateMCPFieldCredentialMerge(ctx, state, state, config, nil, ownership, ownership); err == nil {
		t.Fatal("configured credential key deletion was accepted despite v1.98 merge semantics")
	}
	config.Credentials = types.MapNull(types.StringType)
	if err := validateMCPFieldCredentialMerge(ctx, state, state, config, nil, ownership, ownership); err != nil {
		t.Fatalf("whole credential clear was rejected: %v", err)
	}
}
