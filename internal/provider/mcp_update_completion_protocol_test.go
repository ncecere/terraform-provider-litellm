package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type mcpUpdateCompletionProtocolResult struct {
	planned *tfprotov6.PlanResourceChangeResponse
	applied *tfprotov6.ApplyResourceChangeResponse
	body    map[string]interface{}
	puts    int64
	schema  *tfprotov6.Schema
	state   *tfprotov6.DynamicValue
}

func runMCPUpdateCompletionProtocol(t *testing.T, stateValues, configValues, proposedChanges, before, after map[string]interface{}, private []byte) mcpUpdateCompletionProtocolResult {
	t.Helper()
	ctx := context.Background()
	var puts atomic.Int64
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			response := before
			if puts.Load() != 0 {
				response = after
			}
			_ = json.NewEncoder(writer).Encode(response)
		case http.MethodPut:
			_ = json.NewDecoder(request.Body).Decode(&body)
			puts.Add(1)
			_ = json.NewEncoder(writer).Encode(after)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	ownership := protocolCommittedMCPFieldOwnership(t, private)
	if ownership.Versioned && ownership.Generation > 0 {
		stateCopy := make(map[string]interface{}, len(stateValues)+1)
		for key, value := range stateValues {
			stateCopy[key] = value
		}
		stateCopy["field_ownership_generation"] = ownership.Generation
		stateValues = stateCopy
	}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := organizationProjectProtocolReplace(t, schema, state, proposedChanges)
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: state,
		PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil {
		t.Fatalf("apply transport error: %v", err)
	}
	return mcpUpdateCompletionProtocolResult{planned: planned, applied: applied, body: body, puts: puts.Load(), schema: schema, state: state}
}

func TestMCPServerNameUpdatePreservesUnownedAliasProtocol(t *testing.T) {
	private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
	state := map[string]interface{}{
		"id": "alias-preserve", "server_id": "alias-preserve", "server_name": "old_name", "alias": "remote_alias",
		"transport": "http", "url": "https://alias.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}
	config := map[string]interface{}{
		"server_name": "new_name", "transport": "http", "url": "https://alias.invalid/mcp",
	}
	before := map[string]interface{}{
		"server_id": "alias-preserve", "server_name": "old_name", "alias": "remote_alias", "transport": "http",
		"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
	}

	t.Run("exact readback", func(t *testing.T) {
		after := map[string]interface{}{
			"server_id": "alias-preserve", "server_name": "new_name", "alias": "remote_alias", "transport": "http",
			"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		}
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"server_name": "new_name", "alias": nil}, before, after, private)
		if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) {
			t.Fatalf("apply: diagnostics=%v", result.applied.Diagnostics)
		}
		if result.puts != 1 || result.body["alias"] != "remote_alias" {
			t.Fatalf("PUT alias was not preserved exactly: puts=%d body=%#v", result.puts, result.body)
		}
		ownership := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
		if ownership.Owned[mcpFieldAliasPath] {
			t.Fatalf("injected alias changed private ownership: %#v", ownership)
		}
	})

	t.Run("mismatched readback", func(t *testing.T) {
		after := map[string]interface{}{
			"server_id": "alias-preserve", "server_name": "new_name", "alias": "regenerated", "transport": "http",
			"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		}
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"server_name": "new_name", "alias": nil}, before, after, private)
		if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 {
			t.Fatalf("alias mismatch was not surfaced: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
		}
		assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
		ownership := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
		if ownership.Generation != 0 || ownership.Owned[mcpFieldAliasPath] {
			t.Fatalf("alias mismatch changed private ownership: %#v", ownership)
		}
	})
}

func TestMCPServerNameUpdateAliasFallbackAndAmbiguityProtocol(t *testing.T) {
	for name, alias := range map[string]interface{}{"null alias": nil, "empty alias": ""} {
		t.Run(name, func(t *testing.T) {
			private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
			state := map[string]interface{}{
				"id": "alias-ambiguous", "server_id": "alias-ambiguous", "server_name": "old_name", "alias": alias,
				"transport": "http", "url": "https://alias.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
			}
			config := map[string]interface{}{"server_name": "new_name", "transport": "http", "url": "https://alias.invalid/mcp"}
			before := map[string]interface{}{
				"server_id": "alias-ambiguous", "server_name": "old_name", "alias": alias, "transport": "http",
				"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
			}
			after := map[string]interface{}{
				"server_id": "alias-ambiguous", "server_name": "new_name", "alias": "new_name", "transport": "http",
				"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
			}
			result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"server_name": "new_name", "alias": alias}, before, after, private)
			if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 || result.body["alias"] != "new_name" {
				t.Fatalf("alias fallback did not converge: puts=%d body=%#v diagnostics=%v", result.puts, result.body, result.applied.Diagnostics)
			}
			if got := protocolString(t, protocolAttributeMap(t, result.schema, result.applied.NewState)["alias"]); got != "new_name" {
				t.Fatalf("alias fallback state = %q", got)
			}
		})
	}

	t.Run("invalid historical alias", func(t *testing.T) {
		private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
		state := map[string]interface{}{
			"id": "alias-ambiguous", "server_id": "alias-ambiguous", "server_name": "old_name", "alias": "remote alias",
			"transport": "http", "url": "https://alias.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
		}
		config := map[string]interface{}{"server_name": "new_name", "transport": "http", "url": "https://alias.invalid/mcp"}
		before := map[string]interface{}{
			"server_id": "alias-ambiguous", "server_name": "old_name", "alias": "remote alias", "transport": "http",
			"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		}
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"server_name": "new_name", "alias": "remote alias"}, before, before, private)
		if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
			t.Fatalf("invalid historical alias was not rejected before PUT: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
		}
		assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
		if strings.Contains(fmtDiagnostics(result.applied.Diagnostics), "new_name") {
			t.Fatal("alias preflight diagnostic exposed configured content")
		}
	})
}

func TestMCPServerNameAndAliasRemovalHasZeroPUTProtocol(t *testing.T) {
	private := protocolMCPFieldPrivate(t, mcpFieldOwnership{
		Owned: map[string]bool{mcpFieldAliasPath: true}, Removals: map[string]bool{}, Generation: 2, Versioned: true,
	})
	state := map[string]interface{}{
		"id": "alias-remove", "server_id": "alias-remove", "server_name": "old_name", "alias": "managed",
		"transport": "http", "url": "https://alias.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}
	config := map[string]interface{}{"server_name": "new_name", "transport": "http", "url": "https://alias.invalid/mcp"}
	before := map[string]interface{}{
		"server_id": "alias-remove", "server_name": "old_name", "alias": "managed", "transport": "http",
		"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
	}
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"server_name": "new_name", "alias": nil}, before, before, private)
	if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
		t.Fatalf("simultaneous alias removal was not rejected before PUT: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
	}
	assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
	ownership := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
	if ownership.Generation != 2 || !ownership.Owned[mcpFieldAliasPath] {
		t.Fatalf("failed alias removal changed private ownership: %#v", ownership)
	}
}

func TestMCPServerCredentialsClearIncludesOwnedLiftedColumnsProtocol(t *testing.T) {
	credentials := map[string]tftypes.Value{
		"client_secret":           tftypes.NewValue(tftypes.String, "secret"),
		"audience":                tftypes.NewValue(tftypes.String, "audience"),
		"subject_token_type":      tftypes.NewValue(tftypes.String, "subject"),
		"token_exchange_endpoint": tftypes.NewValue(tftypes.String, "https://token.invalid/exchange"),
	}
	state := map[string]interface{}{
		"id": "clear-lifted", "server_id": "clear-lifted", "server_name": "clear-lifted", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "oauth2_token_exchange", "spec_version": "2024-11-05",
		"credentials": credentials, "field_ownership_generation": int64(1),
	}
	config := map[string]interface{}{
		"server_name": "clear-lifted", "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "oauth2_token_exchange",
	}
	before := map[string]interface{}{
		"server_id": "clear-lifted", "server_name": "clear-lifted", "transport": "http", "url": "https://known.invalid/mcp",
		"auth_type": "oauth2_token_exchange", "credentials": nil, "audience": "audience", "subject_token_type": "subject",
		"token_exchange_endpoint": "https://token.invalid/exchange", "token_exchange_profile": "unowned-profile", "mcp_info": map[string]interface{}{},
	}
	owned := mcpFieldOwnership{Owned: map[string]bool{mcpFieldCredentialsPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
	for _, test := range []struct {
		name           string
		retainAudience bool
		wantFailure    bool
	}{
		{name: "exact clear"},
		{name: "failed lifted clear", retainAudience: true, wantFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			after := map[string]interface{}{
				"server_id": "clear-lifted", "server_name": "clear-lifted", "transport": "http", "url": "https://known.invalid/mcp",
				"auth_type": "oauth2_token_exchange", "credentials": nil, "audience": nil, "subject_token_type": nil,
				"token_exchange_endpoint": nil, "token_exchange_profile": "unowned-profile", "mcp_info": map[string]interface{}{},
			}
			if test.retainAudience {
				after["audience"] = "audience"
			}
			result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"credentials": nil}, before, after, protocolMCPFieldPrivate(t, owned))
			if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) != test.wantFailure || result.puts != 1 {
				t.Fatalf("credential clear result: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
			}
			if value, present := result.body["credentials"]; !present || value != nil {
				t.Fatalf("credential clear sentinel missing: %#v", result.body)
			}
			for _, name := range []string{"audience", "subject_token_type", "token_exchange_endpoint"} {
				if value, present := result.body[name]; !present || value != nil {
					t.Fatalf("owned lifted clear %s missing: %#v", name, result.body)
				}
			}
			if _, sent := result.body["token_exchange_profile"]; sent {
				t.Fatalf("unowned lifted column was cleared: %#v", result.body)
			}
			if test.wantFailure {
				assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
			} else if ownership := protocolCommittedMCPFieldOwnership(t, result.applied.Private); ownership.Owned[mcpFieldCredentialsPath] {
				t.Fatalf("credential ownership survived confirmed clear: %#v", ownership)
			}
		})
	}
}

func TestMCPServerObservableCredentialDriftIsReassertedProtocol(t *testing.T) {
	credentials := map[string]tftypes.Value{
		"client_secret":     tftypes.NewValue(tftypes.String, "secret"),
		"upstream_resource": tftypes.NewValue(tftypes.String, "configured-resource"),
	}
	remoteCredentials := map[string]tftypes.Value{
		"client_secret":     tftypes.NewValue(tftypes.String, "secret"),
		"upstream_resource": tftypes.NewValue(tftypes.String, "remote-drift"),
	}
	state := map[string]interface{}{
		"id": "observable-credential", "server_id": "observable-credential", "server_name": "observable-credential", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "oauth2", "spec_version": "2024-11-05",
		"credentials": remoteCredentials, "field_ownership_generation": int64(1),
	}
	config := map[string]interface{}{
		"server_name": "observable-credential", "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "oauth2", "credentials": credentials,
	}
	before := map[string]interface{}{
		"server_id": "observable-credential", "server_name": "observable-credential", "transport": "http", "url": "https://known.invalid/mcp",
		"auth_type": "oauth2", "credentials": map[string]string{"upstream_resource": "remote-drift"}, "mcp_info": map[string]interface{}{},
	}
	owned := mcpFieldOwnership{Owned: map[string]bool{mcpFieldCredentialsPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
	for _, test := range []struct {
		name        string
		readback    string
		wantFailure bool
	}{
		{name: "confirmed repair", readback: "configured-resource"},
		{name: "unconfirmed repair", readback: "remote-drift", wantFailure: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			after := map[string]interface{}{
				"server_id": "observable-credential", "server_name": "observable-credential", "transport": "http", "url": "https://known.invalid/mcp",
				"auth_type": "oauth2", "credentials": map[string]string{"upstream_resource": test.readback}, "mcp_info": map[string]interface{}{},
			}
			result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"credentials": credentials}, before, after, protocolMCPFieldPrivate(t, owned))
			if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) != test.wantFailure || result.puts != 1 {
				t.Fatalf("observable credential drift result: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
			}
			wireCredentials, _ := result.body["credentials"].(map[string]interface{})
			if wireCredentials["upstream_resource"] != "configured-resource" {
				t.Fatalf("observable credential intent missing from PUT: %#v", result.body)
			}
			if test.wantFailure {
				assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
			}
		})
	}
}

func TestMCPServerCredentialsUpdateProjectsLiftedColumnIntentProtocol(t *testing.T) {
	priorCredentials := map[string]tftypes.Value{
		"client_secret": tftypes.NewValue(tftypes.String, "secret"),
		"audience":      tftypes.NewValue(tftypes.String, "old-audience"),
	}
	desiredCredentials := map[string]tftypes.Value{
		"client_secret": tftypes.NewValue(tftypes.String, "secret"),
		"audience":      tftypes.NewValue(tftypes.String, "new-audience"),
	}
	state := map[string]interface{}{
		"id": "update-lifted", "server_id": "update-lifted", "server_name": "update-lifted", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "oauth2_token_exchange", "spec_version": "2024-11-05",
		"credentials": priorCredentials, "field_ownership_generation": int64(1),
	}
	config := map[string]interface{}{
		"server_name": "update-lifted", "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "oauth2_token_exchange",
		"credentials": desiredCredentials,
	}
	before := map[string]interface{}{
		"server_id": "update-lifted", "server_name": "update-lifted", "transport": "http", "url": "https://known.invalid/mcp",
		"auth_type": "oauth2_token_exchange", "credentials": nil, "audience": "old-audience", "mcp_info": map[string]interface{}{},
	}
	after := map[string]interface{}{
		"server_id": "update-lifted", "server_name": "update-lifted", "transport": "http", "url": "https://known.invalid/mcp",
		"auth_type": "oauth2_token_exchange", "credentials": nil, "audience": "new-audience", "mcp_info": map[string]interface{}{},
	}
	owned := mcpFieldOwnership{Owned: map[string]bool{mcpFieldCredentialsPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"credentials": desiredCredentials}, before, after, protocolMCPFieldPrivate(t, owned))
	if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 || result.body["audience"] != "new-audience" {
		t.Fatalf("lifted credential intent did not converge: puts=%d body=%#v diagnostics=%v", result.puts, result.body, result.applied.Diagnostics)
	}
}

func TestMCPServerInitialEmptyCredentialsTakeoverHasZeroPUTProtocol(t *testing.T) {
	state := map[string]interface{}{
		"id": "empty-credentials", "server_id": "empty-credentials", "server_name": "empty-credentials",
		"transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}
	emptyCredentials := map[string]tftypes.Value{}
	config := map[string]interface{}{
		"server_name": "empty-credentials", "transport": "http", "url": "https://known.invalid/mcp", "credentials": emptyCredentials,
	}
	before := map[string]interface{}{
		"server_id": "empty-credentials", "server_name": "empty-credentials", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "none", "credentials": nil, "mcp_info": map[string]interface{}{},
	}
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"credentials": emptyCredentials}, before, before, protocolMCPFieldPrivate(t, emptyMCPFieldOwnership()))
	if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
		t.Fatalf("empty merge-only credential takeover was not rejected: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
	}
	assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
}

func TestMCPServerInitialNonEmptyCredentialsSameClassHasZeroPUTProtocol(t *testing.T) {
	state := map[string]interface{}{
		"id": "nonempty-credentials", "server_id": "nonempty-credentials", "server_name": "nonempty-credentials",
		"transport": "http", "url": "https://known.invalid/mcp", "auth_type": "api_key", "spec_version": "2024-11-05",
	}
	credentials := map[string]tftypes.Value{"auth_value": tftypes.NewValue(tftypes.String, "configured")}
	config := map[string]interface{}{
		"server_name": "nonempty-credentials", "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "api_key", "credentials": credentials,
	}
	before := map[string]interface{}{
		"server_id": "nonempty-credentials", "server_name": "nonempty-credentials", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "api_key", "credentials": nil, "mcp_info": map[string]interface{}{},
	}
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"credentials": credentials}, before, before, protocolMCPFieldPrivate(t, emptyMCPFieldOwnership()))
	if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
		t.Fatalf("non-empty merge-only credential takeover was not rejected: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
	}
	assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
}

func TestMCPServerInitialEmptyCredentialsClassReplacementProtocol(t *testing.T) {
	state := map[string]interface{}{
		"id": "replace-empty-credentials", "server_id": "replace-empty-credentials", "server_name": "replace-empty-credentials",
		"transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}
	emptyCredentials := map[string]tftypes.Value{}
	emptyScopes := protocolMCPStringList()
	config := map[string]interface{}{
		"server_name": "replace-empty-credentials", "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "api_key", "credentials": emptyCredentials,
		"oauth_scopes": emptyScopes,
	}
	before := map[string]interface{}{
		"server_id": "replace-empty-credentials", "server_name": "replace-empty-credentials", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "none", "credentials": nil, "mcp_info": map[string]interface{}{},
	}
	after := map[string]interface{}{
		"server_id": "replace-empty-credentials", "server_name": "replace-empty-credentials", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "api_key", "credentials": nil, "mcp_info": map[string]interface{}{},
	}
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"auth_type": "api_key", "credentials": emptyCredentials, "oauth_scopes": emptyScopes}, before, after, protocolMCPFieldPrivate(t, emptyMCPFieldOwnership()))
	if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 {
		t.Fatalf("credential-class replacement was rejected: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
	}
	if value, present := result.body["credentials"]; !present || !mcpWireValuesEqual(value, map[string]interface{}{"scopes": []string{}}) {
		t.Fatalf("empty replacement credentials and scopes missing from PUT: %#v", result.body)
	}
	if ownership := protocolCommittedMCPFieldOwnership(t, result.applied.Private); !ownership.Owned[mcpFieldCredentialsPath] || !ownership.Owned[mcpFieldOAuthScopesPath] {
		t.Fatalf("replacement credential ownership was not committed: %#v", ownership)
	}
}

func TestMCPServerOwnedCredentialsClassReplacementProtocol(t *testing.T) {
	for _, test := range []struct {
		name          string
		prior         map[string]tftypes.Value
		desired       map[string]tftypes.Value
		expected      map[string]string
		clearedLifted string
	}{
		{
			name: "replacement may remove old keys",
			prior: map[string]tftypes.Value{
				"client_id": tftypes.NewValue(tftypes.String, "id"), "client_secret": tftypes.NewValue(tftypes.String, "secret"),
			},
			desired:  map[string]tftypes.Value{"auth_value": tftypes.NewValue(tftypes.String, "replacement")},
			expected: map[string]string{"auth_value": "replacement"},
		},
		{
			name: "replacement clears omitted owned lifted key",
			prior: map[string]tftypes.Value{
				"client_secret": tftypes.NewValue(tftypes.String, "secret"), "audience": tftypes.NewValue(tftypes.String, "old-audience"),
			},
			desired:       map[string]tftypes.Value{"auth_value": tftypes.NewValue(tftypes.String, "replacement")},
			expected:      map[string]string{"auth_value": "replacement"},
			clearedLifted: "audience",
		},
		{
			name:     "unchanged map is still supplied",
			prior:    map[string]tftypes.Value{"auth_value": tftypes.NewValue(tftypes.String, "same")},
			desired:  map[string]tftypes.Value{"auth_value": tftypes.NewValue(tftypes.String, "same")},
			expected: map[string]string{"auth_value": "same"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			emptyScopes := protocolMCPStringList()
			state := map[string]interface{}{
				"id": "replace-owned-credentials", "server_id": "replace-owned-credentials", "server_name": "replace-owned-credentials",
				"transport": "http", "url": "https://known.invalid/mcp", "auth_type": "oauth2", "spec_version": "2024-11-05",
				"credentials": test.prior, "oauth_scopes": emptyScopes, "field_ownership_generation": int64(1),
			}
			config := map[string]interface{}{
				"server_name": "replace-owned-credentials", "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "api_key", "credentials": test.desired,
				"oauth_scopes": emptyScopes,
			}
			before := map[string]interface{}{
				"server_id": "replace-owned-credentials", "server_name": "replace-owned-credentials", "transport": "http",
				"url": "https://known.invalid/mcp", "auth_type": "oauth2", "credentials": nil, "mcp_info": map[string]interface{}{},
			}
			after := map[string]interface{}{
				"server_id": "replace-owned-credentials", "server_name": "replace-owned-credentials", "transport": "http",
				"url": "https://known.invalid/mcp", "auth_type": "api_key", "credentials": nil, "mcp_info": map[string]interface{}{},
			}
			if test.clearedLifted != "" {
				before[test.clearedLifted] = "old-audience"
				after[test.clearedLifted] = nil
			}
			owned := mcpFieldOwnership{Owned: map[string]bool{mcpFieldCredentialsPath: true, mcpFieldOAuthScopesPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
			result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"auth_type": "api_key", "credentials": test.desired, "oauth_scopes": emptyScopes}, before, after, protocolMCPFieldPrivate(t, owned))
			if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 {
				t.Fatalf("owned credential-class replacement failed: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
			}
			expectedCredentials := make(map[string]interface{}, len(test.expected)+1)
			for name, value := range test.expected {
				expectedCredentials[name] = value
			}
			expectedCredentials["scopes"] = []string{}
			if value, present := result.body["credentials"]; !present || !mcpWireValuesEqual(value, expectedCredentials) {
				t.Fatalf("complete replacement credentials and scopes missing from PUT: %#v", result.body)
			}
			if test.clearedLifted != "" {
				if value, present := result.body[test.clearedLifted]; !present || value != nil {
					t.Fatalf("owned lifted replacement clear missing from PUT: %#v", result.body)
				}
			}
		})
	}
}

func TestMCPServerNullToValueURLRunsImplicitClearPreflightProtocol(t *testing.T) {
	private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
	state := map[string]interface{}{
		"id": "url-null-change", "server_id": "url-null-change", "server_name": "url-null-change",
		"transport": "http", "url": nil, "spec_path": "/known/spec.json", "auth_type": "none", "spec_version": "2024-11-05",
	}
	config := map[string]interface{}{
		"server_name": "url-null-change", "transport": "http", "url": "https://new.invalid/mcp", "spec_path": "/known/spec.json",
	}
	before := map[string]interface{}{
		"server_id": "url-null-change", "server_name": "url-null-change", "transport": "http", "url": nil,
		"spec_path": "/known/spec.json", "auth_type": "none", "issuer": "https://issuer.invalid", "mcp_info": map[string]interface{}{},
	}
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"url": "https://new.invalid/mcp"}, before, before, private)
	if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
		t.Fatalf("null-to-value URL bypassed implicit-clear preflight: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
	}
	assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
}

func TestMCPServerURLClearPreservesOAuthFieldsProtocol(t *testing.T) {
	state := map[string]interface{}{
		"id": "url-clear", "server_id": "url-clear", "server_name": "url-clear", "transport": "http",
		"url": "https://old.invalid/mcp", "spec_path": "/known/spec.json", "auth_type": "oauth2", "spec_version": "2024-11-05",
		"authorization_url": "https://auth.invalid/authorize", "token_url": "https://auth.invalid/token", "registration_url": "https://auth.invalid/register",
	}
	config := map[string]interface{}{
		"server_name": "url-clear", "transport": "http", "spec_path": "/known/spec.json", "auth_type": "oauth2",
		"authorization_url": "https://auth.invalid/authorize", "token_url": "https://auth.invalid/token", "registration_url": "https://auth.invalid/register",
	}
	before := map[string]interface{}{
		"server_id": "url-clear", "server_name": "url-clear", "transport": "http", "url": "https://old.invalid/mcp",
		"spec_path": "/known/spec.json", "auth_type": "oauth2", "authorization_url": "https://auth.invalid/authorize",
		"token_url": "https://auth.invalid/token", "registration_url": "https://auth.invalid/register", "mcp_info": map[string]interface{}{},
	}
	after := map[string]interface{}{
		"server_id": "url-clear", "server_name": "url-clear", "transport": "http", "url": nil,
		"spec_path": "/known/spec.json", "auth_type": "oauth2", "authorization_url": "https://auth.invalid/authorize",
		"token_url": "https://auth.invalid/token", "registration_url": "https://auth.invalid/register", "mcp_info": map[string]interface{}{},
	}
	owned := mcpFieldOwnership{Owned: map[string]bool{
		mcpFieldAuthorizationURLPath: true, mcpFieldTokenURLPath: true, mcpFieldRegistrationURLPath: true,
	}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"url": nil}, before, after, protocolMCPFieldPrivate(t, owned))
	if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 {
		t.Fatalf("URL clear was incorrectly blocked: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
	}
	if value, present := result.body["url"]; !present || value != nil {
		t.Fatalf("URL clear sentinel missing from PUT: %#v", result.body)
	}
	for _, name := range []string{"authorization_url", "token_url", "registration_url"} {
		if _, sent := result.body[name]; sent {
			t.Fatalf("unchanged OAuth field %s was needlessly sent: %#v", name, result.body)
		}
	}
}

func TestMCPServerTransportUpdateCompletesEndpointPayloadProtocol(t *testing.T) {
	for _, test := range []struct {
		name          string
		oldTransport  string
		newTransport  string
		url           interface{}
		specPath      interface{}
		mismatchURL   bool
		wantApplyFail bool
	}{
		{name: "http to sse preserves both endpoints", oldTransport: "http", newTransport: "sse", url: "https://transport.invalid/mcp", specPath: "/srv/openapi.json"},
		{name: "sse to http preserves URL", oldTransport: "sse", newTransport: "http", url: "https://transport.invalid/mcp", specPath: nil},
		{name: "injected endpoint readback mismatch", oldTransport: "http", newTransport: "sse", url: "https://transport.invalid/mcp", specPath: nil, mismatchURL: true, wantApplyFail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
			state := map[string]interface{}{
				"id": "transport-endpoint", "server_id": "transport-endpoint", "server_name": "transport-endpoint",
				"transport": test.oldTransport, "url": test.url, "spec_path": test.specPath, "auth_type": "none", "spec_version": "2024-11-05",
			}
			config := map[string]interface{}{
				"server_name": "transport-endpoint", "transport": test.newTransport, "url": test.url, "spec_path": test.specPath,
			}
			before := map[string]interface{}{
				"server_id": "transport-endpoint", "server_name": "transport-endpoint", "transport": test.oldTransport,
				"url": test.url, "spec_path": test.specPath, "auth_type": "none", "mcp_info": map[string]interface{}{},
			}
			after := map[string]interface{}{
				"server_id": "transport-endpoint", "server_name": "transport-endpoint", "transport": test.newTransport,
				"url": test.url, "spec_path": test.specPath, "auth_type": "none", "mcp_info": map[string]interface{}{},
			}
			if test.mismatchURL {
				after["url"] = "https://different.invalid/mcp"
			}
			result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"transport": test.newTransport}, before, after, private)
			if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) != test.wantApplyFail || result.puts != 1 {
				t.Fatalf("apply result: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
			}
			if result.body["url"] != test.url {
				t.Fatalf("unchanged URL missing from transport PUT: %#v", result.body)
			}
			if test.specPath != nil && result.body["spec_path"] != test.specPath {
				t.Fatalf("unchanged spec_path missing from transport PUT: %#v", result.body)
			}
			if test.wantApplyFail {
				assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
			}
		})
	}
}

func TestMCPServerTransportUpdateCompletesStdioPayloadProtocol(t *testing.T) {
	private := protocolMCPFieldPrivate(t, mcpFieldOwnership{
		Owned: map[string]bool{mcpFieldCommandPath: true, mcpFieldArgsPath: true}, Removals: map[string]bool{}, Generation: 2, Versioned: true,
	})
	args := protocolMCPStringList("server.py")
	wireArgs := []string{"server.py"}
	state := map[string]interface{}{
		"id": "transport-stdio", "server_id": "transport-stdio", "server_name": "transport-stdio", "transport": "http",
		"url": "https://transport.invalid/mcp", "command": "python3", "args": args, "auth_type": "none", "spec_version": "2024-11-05",
	}
	config := map[string]interface{}{
		"server_name": "transport-stdio", "transport": "stdio", "command": "python3", "args": args,
	}
	before := map[string]interface{}{
		"server_id": "transport-stdio", "server_name": "transport-stdio", "transport": "http", "url": "https://transport.invalid/mcp",
		"command": "python3", "args": wireArgs, "auth_type": "none", "mcp_info": map[string]interface{}{},
	}
	after := map[string]interface{}{
		"server_id": "transport-stdio", "server_name": "transport-stdio", "transport": "stdio",
		"command": "python3", "args": wireArgs, "auth_type": "none", "mcp_info": map[string]interface{}{},
	}
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"transport": "stdio", "url": nil}, before, after, private)
	if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 {
		t.Fatalf("stdio apply: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
	}
	if result.body["command"] != "python3" || !mcpWireValuesEqual(result.body["args"], wireArgs) {
		t.Fatalf("unchanged stdio dependencies missing from PUT: %#v", result.body)
	}
	ownership := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
	if !ownership.Owned[mcpFieldCommandPath] || !ownership.Owned[mcpFieldArgsPath] {
		t.Fatalf("stdio update lost private ownership: %#v", ownership)
	}
}

func TestMCPServerTransportUpdateUnsafeDependenciesHaveZeroPUTProtocol(t *testing.T) {
	for _, test := range []struct {
		name      string
		transport string
		state     map[string]interface{}
		config    map[string]interface{}
		changes   map[string]interface{}
		before    map[string]interface{}
	}{
		{
			name: "unknown HTTP endpoint", transport: "sse",
			state: map[string]interface{}{
				"id": "unsafe-http", "server_id": "unsafe-http", "server_name": "unsafe-http", "transport": "stdio",
				"command": "python3", "args": protocolMCPStringList("server.py"), "auth_type": "none", "spec_version": "2024-11-05",
			},
			config:  map[string]interface{}{"server_name": "unsafe-http", "transport": "sse", "url": tftypes.UnknownValue},
			changes: map[string]interface{}{"transport": "sse", "url": tftypes.UnknownValue, "command": nil, "args": nil},
			before: map[string]interface{}{
				"server_id": "unsafe-http", "server_name": "unsafe-http", "transport": "stdio", "command": "python3",
				"args": []string{"server.py"}, "auth_type": "none", "mcp_info": map[string]interface{}{},
			},
		},
		{
			name: "unknown stdio command", transport: "stdio",
			state: map[string]interface{}{
				"id": "unsafe-stdio", "server_id": "unsafe-stdio", "server_name": "unsafe-stdio", "transport": "http",
				"url": "https://transport.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
			},
			config:  map[string]interface{}{"server_name": "unsafe-stdio", "transport": "stdio", "command": tftypes.UnknownValue, "args": protocolMCPStringList("server.py")},
			changes: map[string]interface{}{"transport": "stdio", "url": nil, "command": tftypes.UnknownValue, "args": protocolMCPStringList("server.py")},
			before: map[string]interface{}{
				"server_id": "unsafe-stdio", "server_name": "unsafe-stdio", "transport": "http", "url": "https://transport.invalid/mcp",
				"auth_type": "none", "mcp_info": map[string]interface{}{},
			},
		},
		{
			name: "unknown stdio args", transport: "stdio",
			state: map[string]interface{}{
				"id": "unsafe-args", "server_id": "unsafe-args", "server_name": "unsafe-args", "transport": "http",
				"url": "https://transport.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
			},
			config:  map[string]interface{}{"server_name": "unsafe-args", "transport": "stdio", "command": "python3", "args": tftypes.UnknownValue},
			changes: map[string]interface{}{"transport": "stdio", "url": nil, "command": "python3", "args": tftypes.UnknownValue},
			before: map[string]interface{}{
				"server_id": "unsafe-args", "server_name": "unsafe-args", "transport": "http", "url": "https://transport.invalid/mcp",
				"auth_type": "none", "mcp_info": map[string]interface{}{},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
			result := runMCPUpdateCompletionProtocol(t, test.state, test.config, test.changes, test.before, test.before, private)
			if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
				t.Fatalf("unsafe %s dependencies were not rejected before PUT: puts=%d diagnostics=%v", test.transport, result.puts, result.applied.Diagnostics)
			}
			assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
		})
	}
}

func TestMCPServerTransportUpdateAbsentDependenciesHasZeroPUTProtocol(t *testing.T) {
	ctx := context.Background()
	var puts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			puts.Add(1)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"server_id": "absent-dependencies", "server_name": "absent-dependencies", "transport": "http",
			"url": "https://transport.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		})
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "absent-dependencies", "server_id": "absent-dependencies", "server_name": "absent-dependencies", "transport": "http",
		"url": "https://transport.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}))
	for _, transport := range []string{"sse", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
				"server_name": "absent-dependencies", "transport": transport,
			}))
			proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"transport": transport, "url": nil})
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("absent dependency plan: err=%v diagnostics=%v", err, planned.Diagnostics)
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: state,
				PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || puts.Load() != 0 {
				t.Fatalf("absent dependency apply: err=%v diagnostics=%v puts=%d", err, applied.Diagnostics, puts.Load())
			}
			assertMCPServerFailedUpdateRetainsPriorState(t, schema, state, applied.NewState)
		})
	}
}

func fmtDiagnostics(diagnostics []*tfprotov6.Diagnostic) string {
	var builder strings.Builder
	for _, diagnostic := range diagnostics {
		builder.WriteString(diagnostic.Summary)
		builder.WriteString(diagnostic.Detail)
	}
	return builder.String()
}
