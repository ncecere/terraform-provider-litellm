package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func protocolTokenCredentials() map[string]tftypes.Value {
	return map[string]tftypes.Value{
		"client_id":     tftypes.NewValue(tftypes.String, "client"),
		"client_secret": tftypes.NewValue(tftypes.String, "secret"),
	}
}

func TestMCPTokenExchangeCanonicalUpdateEqualTakeoverClearAndHandoffProtocol(t *testing.T) {
	credentialsState := map[string]tftypes.Value{
		"client_id":     tftypes.NewValue(tftypes.String, "client"),
		"client_secret": tftypes.NewValue(tftypes.String, "secret"),
	}
	baseState := map[string]interface{}{
		"id": "canonical-update", "server_id": "canonical-update", "server_name": "canonical_update",
		"transport": "http", "url": "https://mcp.invalid", "auth_type": "oauth2_token_exchange", "spec_version": "2024-11-05",
		"credentials": credentialsState, "oauth_scopes": protocolMCPStringList("scope.one"),
		"issuer": "https://issuer.old", "token_exchange_endpoint": "https://idp.old/token", "audience": "api://old",
		"subject_token_type": "urn:old", "token_exchange_profile": "rfc8693",
	}
	baseConfig := map[string]interface{}{
		"server_name": "canonical_update", "transport": "http", "url": "https://mcp.invalid", "auth_type": "oauth2_token_exchange",
		"credentials": credentialsState, "oauth_scopes": protocolMCPStringList("scope.one"),
	}
	before := map[string]interface{}{
		"server_id": "canonical-update", "server_name": "canonical_update", "transport": "http", "url": "https://mcp.invalid", "auth_type": "oauth2_token_exchange", "mcp_info": map[string]interface{}{},
		"issuer": "https://issuer.old", "token_exchange_endpoint": "https://idp.old/token", "audience": "api://old",
		"subject_token_type": "urn:old", "token_exchange_profile": "rfc8693",
	}
	owned := mcpFieldOwnership{
		Owned: map[string]bool{
			mcpFieldCredentialsPath: true, mcpFieldOAuthScopesPath: true,
			mcpFieldIssuerPath: true, mcpFieldTokenExchangeEndpointPath: true, mcpFieldAudiencePath: true,
			mcpFieldSubjectTokenTypePath: true, mcpFieldTokenExchangeProfilePath: true,
		},
		Removals: map[string]bool{}, CredentialClass: "oauth2_token_exchange",
		CredentialKeys: []string{"client_id", "client_secret"}, Generation: 1, Versioned: true,
	}

	t.Run("canonical update", func(t *testing.T) {
		config := cloneMCPEnvVarsInterfaceMap(baseConfig)
		changes := map[string]interface{}{
			"token_exchange_endpoint": "https://idp.new/token", "audience": "api://new", "subject_token_type": "urn:new",
		}
		for name, value := range changes {
			config[name] = value
		}
		config["issuer"] = "https://issuer.old"
		config["token_exchange_profile"] = "rfc8693"
		after := cloneMCPEnvVarsInterfaceMap(before)
		for name, value := range changes {
			after[name] = value
		}
		result := runMCPUpdateCompletionProtocol(t, baseState, config, changes, before, after, protocolMCPFieldPrivate(t, owned))
		if result.puts != 1 || accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) {
			t.Fatalf("canonical update failed: puts=%d diagnostics=%s", result.puts, agentProtocolDiagnosticsText(result.applied.Diagnostics))
		}
		for name, value := range changes {
			if result.body[name] != value {
				t.Fatalf("canonical update omitted %s: %#v", name, result.body)
			}
		}
		if _, present := result.body["credentials"]; present {
			t.Fatalf("unchanged credentials were resent: %#v", result.body)
		}
	})

	t.Run("equal takeover", func(t *testing.T) {
		state := cloneMCPEnvVarsInterfaceMap(baseState)
		state["field_ownership_generation"] = int64(1)
		config := cloneMCPEnvVarsInterfaceMap(baseConfig)
		config["audience"] = "api://old"
		prior := mcpFieldOwnership{
			Owned: map[string]bool{mcpFieldCredentialsPath: true, mcpFieldOAuthScopesPath: true}, Removals: map[string]bool{},
			CredentialClass: "oauth2_token_exchange", CredentialKeys: []string{"client_id", "client_secret"}, Generation: 1, Versioned: true,
		}
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"audience": "api://old"}, before, before, protocolMCPFieldPrivate(t, prior))
		if result.puts != 0 || accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) {
			t.Fatalf("equal takeover failed: puts=%d diagnostics=%s", result.puts, agentProtocolDiagnosticsText(result.applied.Diagnostics))
		}
		committed := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
		if !committed.Owned[mcpFieldAudiencePath] || committed.Generation != 2 {
			t.Fatalf("equal takeover did not commit ownership: %#v", committed)
		}
	})

	t.Run("canonical clear including issuer", func(t *testing.T) {
		after := cloneMCPEnvVarsInterfaceMap(before)
		changes := map[string]interface{}{}
		for _, name := range mcpTokenExchangeCanonicalFields {
			after[name] = nil
			changes[name] = nil
		}
		result := runMCPUpdateCompletionProtocol(t, baseState, baseConfig, changes, before, after, protocolMCPFieldPrivate(t, owned))
		if result.puts != 1 || accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) {
			t.Fatalf("canonical clear failed: puts=%d diagnostics=%s body=%#v", result.puts, agentProtocolDiagnosticsText(result.applied.Diagnostics), result.body)
		}
		for _, name := range mcpTokenExchangeCanonicalFields {
			value, present := result.body[name]
			if !present || value != nil {
				t.Fatalf("canonical clear omitted explicit null for %s: %#v", name, result.body)
			}
		}
		committed := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
		for _, fieldPath := range []string{mcpFieldIssuerPath, mcpFieldTokenExchangeEndpointPath, mcpFieldAudiencePath, mcpFieldSubjectTokenTypePath, mcpFieldTokenExchangeProfilePath} {
			if !committed.Removals[fieldPath] || committed.Owned[fieldPath] {
				t.Fatalf("clear ownership not committed for %s: %#v", fieldPath, committed)
			}
		}
	})

	t.Run("atomic legacy audience handoff", func(t *testing.T) {
		legacyCredentials := map[string]tftypes.Value{
			"client_id": tftypes.NewValue(tftypes.String, "client"), "client_secret": tftypes.NewValue(tftypes.String, "secret"),
			"audience": tftypes.NewValue(tftypes.String, "api://old"),
		}
		state := cloneMCPEnvVarsInterfaceMap(baseState)
		state["credentials"] = legacyCredentials
		state["audience"] = nil
		config := cloneMCPEnvVarsInterfaceMap(baseConfig)
		config["audience"] = "api://old"
		legacyOwned := mcpFieldOwnership{
			Owned: map[string]bool{mcpFieldCredentialsPath: true, mcpFieldOAuthScopesPath: true}, Removals: map[string]bool{},
			CredentialClass: "oauth2_token_exchange", CredentialKeys: []string{"audience", "client_id", "client_secret"}, Generation: 1, Versioned: true,
		}
		legacyBefore := cloneMCPEnvVarsInterfaceMap(before)
		legacyBefore["issuer"], legacyBefore["token_exchange_endpoint"] = nil, nil
		legacyBefore["subject_token_type"], legacyBefore["token_exchange_profile"] = nil, nil
		legacyAfter := cloneMCPEnvVarsInterfaceMap(legacyBefore)
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"credentials": credentialsState, "audience": "api://old"}, legacyBefore, legacyAfter, protocolMCPFieldPrivate(t, legacyOwned))
		if result.puts != 1 || accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) {
			t.Fatalf("atomic handoff failed: puts=%d diagnostics=%s body=%#v", result.puts, agentProtocolDiagnosticsText(result.applied.Diagnostics), result.body)
		}
		if result.body["audience"] != "api://old" {
			t.Fatalf("handoff did not send canonical sibling: %#v", result.body)
		}
		wireCredentials, ok := result.body["credentials"].(map[string]interface{})
		if !ok {
			t.Fatalf("handoff credentials=%T", result.body["credentials"])
		}
		if _, present := wireCredentials["audience"]; present {
			t.Fatalf("handoff retained legacy alias: %#v", wireCredentials)
		}
	})
}

func TestMCPTokenExchangeCanonicalReadbackFailureRetainsPublicAndPrivateProtocol(t *testing.T) {
	state := map[string]interface{}{
		"id": "readback-failure", "server_id": "readback-failure", "server_name": "readback_failure",
		"transport": "http", "url": "https://mcp.invalid", "auth_type": "oauth2_token_exchange", "spec_version": "2024-11-05",
		"credentials": protocolTokenCredentials(), "oauth_scopes": protocolMCPStringList("scope"),
		"token_exchange_endpoint": "https://old.invalid/token", "field_ownership_generation": int64(1),
	}
	config := map[string]interface{}{
		"server_name": "readback_failure", "transport": "http", "url": "https://mcp.invalid", "auth_type": "oauth2_token_exchange",
		"credentials": protocolTokenCredentials(), "oauth_scopes": protocolMCPStringList("scope"), "token_exchange_endpoint": "https://new.invalid/token",
	}
	before := map[string]interface{}{
		"server_id": "readback-failure", "server_name": "readback_failure", "transport": "http", "url": "https://mcp.invalid", "auth_type": "oauth2_token_exchange", "mcp_info": map[string]interface{}{},
		"token_exchange_endpoint": "https://old.invalid/token",
	}
	after := cloneMCPEnvVarsInterfaceMap(before)
	after["token_exchange_endpoint"] = "https://wrong.invalid/token"
	owned := mcpFieldOwnership{
		Owned: map[string]bool{mcpFieldCredentialsPath: true, mcpFieldOAuthScopesPath: true, mcpFieldTokenExchangeEndpointPath: true}, Removals: map[string]bool{},
		CredentialClass: "oauth2_token_exchange", CredentialKeys: []string{"client_id", "client_secret"}, Generation: 1, Versioned: true,
	}
	private := protocolMCPFieldPrivate(t, owned)
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"token_exchange_endpoint": "https://new.invalid/token"}, before, after, private)
	if result.puts != 1 || !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) {
		t.Fatalf("mismatched canonical readback was accepted: puts=%d diagnostics=%s", result.puts, agentProtocolDiagnosticsText(result.applied.Diagnostics))
	}
	assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
	if committed := protocolCommittedMCPFieldOwnership(t, result.applied.Private); !mcpFieldOwnershipEqual(committed, owned) {
		t.Fatalf("failed readback changed committed private ownership: %#v", committed)
	}
}

func TestMCPTokenExchangeAcceptedCreateRecoveryProtocol(t *testing.T) {
	ctx := context.Background()
	var accepted atomic.Value
	var readsEnabled atomic.Bool
	var posts, gets, puts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodPost:
			posts.Add(1)
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			accepted.Store(body)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"server_id": body["server_id"]})
		case http.MethodGet:
			gets.Add(1)
			if !readsEnabled.Load() {
				http.Error(writer, `{"error":"unavailable"}`, http.StatusInternalServerError)
				return
			}
			body := accepted.Load().(map[string]interface{})
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"server_id": body["server_id"], "server_name": "accepted_recovery", "transport": "http", "url": "https://mcp.invalid", "auth_type": "oauth2_token_exchange", "mcp_info": map[string]interface{}{},
				"issuer": "https://issuer.invalid", "token_exchange_endpoint": "https://idp.invalid/token", "audience": "api://target", "subject_token_type": "urn:subject", "token_exchange_profile": "rfc8693",
			})
		case http.MethodPut:
			puts.Add(1)
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			prior := accepted.Load().(map[string]interface{})
			for name, value := range body {
				prior[name] = value
			}
			accepted.Store(prior)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"server_id": prior["server_id"]})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	configValues := map[string]interface{}{
		"server_name": "accepted_recovery", "transport": "http", "url": "https://mcp.invalid", "auth_type": "oauth2_token_exchange",
		"issuer": "https://issuer.invalid", "token_exchange_endpoint": "https://idp.invalid/token", "audience": "api://target", "subject_token_type": "urn:subject", "token_exchange_profile": "rfc8693",
		"credentials": protocolTokenCredentials(), "oauth_scopes": protocolMCPStringList("scope"),
	}
	config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, configValues)
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) || posts.Load() != 1 || gets.Load() != 1 {
		t.Fatalf("unconfirmed create: err=%v diagnostics=%s posts=%d gets=%d", err, agentProtocolDiagnosticsText(created.Diagnostics), posts.Load(), gets.Load())
	}
	if !protocolPrivateHasKey(t, created.Private, mcpFieldAcceptedCreatePrivateKey) || !protocolPrivateHasKey(t, created.Private, mcpFieldPendingOwnershipPrivateKey) {
		t.Fatal("accepted create did not retain the existing recovery grammar")
	}

	readsEnabled.Store(true)
	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: created.NewState, Private: created.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) || gets.Load() != 2 {
		t.Fatalf("recovery refresh: err=%v diagnostics=%s gets=%d", err, agentProtocolDiagnosticsText(refreshed.Diagnostics), gets.Load())
	}
	proposed := organizationProjectProtocolReplace(t, schema, refreshed.NewState, configValues)
	recoveryPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: refreshed.NewState, ProposedNewState: proposed, PriorPrivate: refreshed.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(recoveryPlan.Diagnostics) {
		t.Fatalf("recovery plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(recoveryPlan.Diagnostics))
	}
	recovered, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: refreshed.NewState, PlannedState: recoveryPlan.PlannedState, PlannedPrivate: recoveryPlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(recovered.Diagnostics) || puts.Load() != 1 || gets.Load() != 4 {
		t.Fatalf("recovery apply: err=%v diagnostics=%s puts=%d gets=%d", err, agentProtocolDiagnosticsText(recovered.Diagnostics), puts.Load(), gets.Load())
	}
	ownership := protocolCommittedMCPFieldOwnership(t, recovered.Private)
	for _, fieldPath := range []string{mcpFieldIssuerPath, mcpFieldTokenExchangeEndpointPath, mcpFieldAudiencePath, mcpFieldSubjectTokenTypePath, mcpFieldTokenExchangeProfilePath} {
		if !ownership.Owned[fieldPath] {
			t.Fatalf("accepted-create recovery omitted %s ownership: %#v", fieldPath, ownership)
		}
	}
	if protocolPrivateHasKey(t, recovered.Private, mcpFieldAcceptedCreatePrivateKey) || protocolPrivateHasKey(t, recovered.Private, mcpFieldPendingOwnershipPrivateKey) {
		t.Fatal("accepted-create recovery markers were not cleared")
	}
}

func TestMCPTokenExchangeImportVisibleThenMaskedProtocol(t *testing.T) {
	ctx := context.Background()
	var masked atomic.Bool
	var gets atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		gets.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		response := map[string]interface{}{"server_id": "token-import", "server_name": "token_import", "transport": "http", "auth_type": "oauth2_token_exchange", "mcp_info": map[string]interface{}{}}
		for name, value := range map[string]interface{}{
			"issuer": "https://issuer.invalid", "token_exchange_endpoint": "https://idp.invalid/token", "audience": "api://target", "subject_token_type": "urn:subject", "token_exchange_profile": "rfc8693",
		} {
			if masked.Load() {
				response[name] = nil
			} else {
				response[name] = value
			}
		}
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_mcp_server", ID: "token-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) {
		t.Fatalf("import: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	visible, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(visible.Diagnostics) || gets.Load() != 1 {
		t.Fatalf("visible import read: err=%v diagnostics=%s gets=%d", err, agentProtocolDiagnosticsText(visible.Diagnostics), gets.Load())
	}
	visibleAttributes := protocolAttributeMap(t, schema, visible.NewState)
	for _, name := range mcpTokenExchangeCanonicalFields {
		if visibleAttributes[name].IsNull() || !visibleAttributes[name].IsKnown() {
			t.Fatalf("visible import did not project %s", name)
		}
	}
	if ownership := protocolCommittedMCPFieldOwnership(t, visible.Private); len(ownership.Owned) != 0 {
		t.Fatalf("import inferred Terraform ownership: %#v", ownership)
	}

	masked.Store(true)
	maskedRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: visible.NewState, Private: visible.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(maskedRead.Diagnostics) || gets.Load() != 2 {
		t.Fatalf("masked import read: err=%v diagnostics=%s gets=%d", err, agentProtocolDiagnosticsText(maskedRead.Diagnostics), gets.Load())
	}
	maskedAttributes := protocolAttributeMap(t, schema, maskedRead.NewState)
	for _, name := range mcpTokenExchangeCanonicalFields {
		if !maskedAttributes[name].Equal(visibleAttributes[name]) {
			t.Fatalf("masked role response erased %s", name)
		}
	}
}
