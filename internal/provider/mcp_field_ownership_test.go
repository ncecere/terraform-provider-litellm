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
	ownership := mcpFieldOwnership{Owned: owned, Removals: map[string]bool{}, Generation: 7, Versioned: true}
	raw := encodeMCPFieldOwnership(ownership)
	decoded, err := decodeMCPFieldOwnership(raw)
	if err != nil || !mcpFieldOwnershipEqual(decoded, ownership) {
		t.Fatalf("canonical ownership did not decode: decoded=%#v err=%v", decoded, err)
	}
	var wire map[string]interface{}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 5 {
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
		mcpFieldEnvPath: map[string]string{}, mcpFieldStaticHeadersPath: map[string]string{}, mcpFieldCredentialsPath: nil,
		mcpFieldAllowAllKeysPath: false,
	}
	if len(want) != 14 || len(mcpFieldPaths) != 14 {
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
	if len(steady.Removals) != 0 || steady.Generation != 2 {
		t.Fatalf("post-clear no-op = %#v", steady)
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
		Credentials: emptyMap, AllowedTools: emptyList, ExtraHeaders: emptyList, StaticHeaders: emptyMap,
		AuthorizationURL: types.StringValue(""), TokenURL: types.StringValue(""), RegistrationURL: types.StringValue(""), AllowAllKeys: types.BoolValue(false),
	}
	request, err := (&MCPServerResource{}).buildMCPServerCreateRequest(ctx, &config, &config, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, fieldPath := range mcpFieldPaths {
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
	delta, err := buildMCPFieldDelta(ctx, MCPServerResourceModel{}, MCPServerResourceModel{}, MCPServerResourceModel{}, committed, candidate, map[string]interface{}{})
	if err != nil {
		t.Fatal(err)
	}
	if len(delta) != 14 {
		t.Fatalf("delta contains unrelated or missing sentinel: %#v", delta)
	}
	for _, fieldPath := range mcpFieldPaths {
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

func TestMCPConfiguredCredentialKeyDeletionRejected(t *testing.T) {
	ctx := context.Background()
	state := MCPServerResourceModel{Credentials: types.MapValueMust(types.StringType, map[string]attr.Value{
		"client_id": types.StringValue("id"), "client_secret": types.StringValue("secret"),
	})}
	config := MCPServerResourceModel{Credentials: types.MapValueMust(types.StringType, map[string]attr.Value{
		"client_id": types.StringValue("id"),
	})}
	ownership := mcpFieldOwnership{Owned: map[string]bool{mcpFieldCredentialsPath: true}, Removals: map[string]bool{}, Versioned: true}
	if err := validateMCPFieldCredentialMerge(ctx, state, config, ownership); err == nil {
		t.Fatal("configured credential key deletion was accepted despite v1.98 merge semantics")
	}
	config.Credentials = types.MapNull(types.StringType)
	if err := validateMCPFieldCredentialMerge(ctx, state, config, ownership); err != nil {
		t.Fatalf("whole credential clear was rejected: %v", err)
	}
}
