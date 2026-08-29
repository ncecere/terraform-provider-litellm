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

func TestMCPAcceptedCreateCredentialRecoveryReappliesOpaqueValuesProtocol(t *testing.T) {
	ctx := context.Background()
	var requestedID atomic.Value
	var readsEnabled atomic.Bool
	var puts atomic.Int64
	var putBody atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodPost:
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			requestedID.Store(body["server_id"].(string))
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"server_id": body["server_id"]})
		case http.MethodPut:
			puts.Add(1)
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			putBody.Store(body)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{})
		case http.MethodGet:
			if !readsEnabled.Load() {
				http.Error(writer, `{"error":"unavailable"}`, http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"server_id": requestedID.Load().(string), "server_name": "credential_recovery", "transport": "http",
				"url": "https://mcp.example.test", "auth_type": "api_key", "credentials": nil, "mcp_info": map[string]interface{}{},
			})
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	configValues := map[string]interface{}{
		"server_name": "credential_recovery", "url": "https://mcp.example.test", "transport": "http", "auth_type": "api_key",
		"credentials": map[string]tftypes.Value{"auth_value": tftypes.NewValue(tftypes.String, "old-secret")},
	}
	config, nullState, createPlan := mcpServerProtocolCreatePlan(t, protocolServer, schema, configValues)
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: nullState,
		PlannedState: createPlan.PlannedState, PlannedPrivate: createPlan.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("unconfirmed create: err=%v diagnostics=%v", err, created.Diagnostics)
	}
	readsEnabled.Store(true)
	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: "litellm_mcp_server", CurrentState: created.NewState, Private: created.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("recovery refresh: err=%v diagnostics=%v", err, refreshed.Diagnostics)
	}
	recoveryConfigValues := map[string]interface{}{
		"server_name": "credential_recovery", "url": "https://mcp.example.test", "transport": "http", "auth_type": "api_key",
		"credentials": map[string]tftypes.Value{"auth_value": tftypes.NewValue(tftypes.String, "new-secret")},
	}
	recoveryConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, recoveryConfigValues))
	proposed := organizationProjectProtocolReplace(t, schema, refreshed.NewState, recoveryConfigValues)
	recoveryPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: recoveryConfig, PriorState: refreshed.NewState,
		ProposedNewState: proposed, PriorPrivate: refreshed.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(recoveryPlan.Diagnostics) {
		t.Fatalf("recovery plan: err=%v diagnostics=%v", err, recoveryPlan.Diagnostics)
	}
	recovered, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: recoveryConfig, PriorState: refreshed.NewState,
		PlannedState: recoveryPlan.PlannedState, PlannedPrivate: recoveryPlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(recovered.Diagnostics) || puts.Load() != 1 {
		t.Fatalf("recovery apply: err=%v diagnostics=%s puts=%d body=%#v", err, agentProtocolDiagnosticsText(recovered.Diagnostics), puts.Load(), putBody.Load())
	}
	if body, _ := putBody.Load().(map[string]interface{}); !mcpWireValuesEqual(body["credentials"], map[string]interface{}{"auth_value": "new-secret"}) {
		t.Fatalf("recovery PUT did not contain the complete current opaque intent: %#v", body)
	}
	attributes := protocolAttributeMap(t, schema, recovered.NewState)
	var credentials map[string]tftypes.Value
	if err := attributes["credentials"].As(&credentials); err != nil {
		t.Fatalf("recovered credentials=%#v err=%v", credentials, err)
	}
	var authValue string
	if value, present := credentials["auth_value"]; !present {
		t.Fatalf("recovered credentials omitted auth_value: %#v", credentials)
	} else if err := value.As(&authValue); err != nil || authValue != "new-secret" {
		t.Fatalf("recovered auth_value=%q err=%v", authValue, err)
	}
	committed := protocolCommittedMCPFieldOwnership(t, recovered.Private)
	if !committed.Owned[mcpFieldCredentialsPath] || committed.CredentialClass != "api_key" {
		t.Fatalf("credential ownership was not committed: %#v", committed)
	}
}

func TestMCPInfoCommittedUnconfirmedCreateRetainsPendingOwnershipProtocol(t *testing.T) {
	ctx := context.Background()
	var requestedID atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			requestedID.Store(body["server_id"].(string))
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"server_id": body["server_id"]})
			return
		}
		http.Error(writer, `{"error":"unavailable"}`, http.StatusInternalServerError)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, map[string]interface{}{
		"server_name": "pending", "url": "https://mcp.example.test", "transport": "http", "mcp_info_json": `{"access":false}`,
	})
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("unconfirmed create: %v %s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	assertMCPServerIdentityOnlyState(t, schema, applied.NewState, requestedID.Load().(string))
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	if !attributes["mcp_info_json"].IsNull() || protocolMCPInt64(t, attributes["mcp_info_ownership_generation"]) != 0 {
		t.Fatal("unconfirmed planned MCP info was published")
	}
	private := protocolPrivateMapFromBytes(t, applied.Private)
	committed, pending, validationErr := validateMCPInfoPrivateBundle(map[string][]byte(private))
	if validationErr != nil || pending == nil || pending.Mode != mcpInfoModeWhole || pending.Generation != 1 || committed.Mode != mcpInfoModeNone {
		t.Fatalf("recovery provenance: committed=%#v pending=%#v err=%v", committed, pending, validationErr)
	}
}
