package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const jwtTestKeyHash = "sha256:ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

func TestJWTKeyMappingKeyHashCreateAndReplacementProtocol(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var createBody map[string]interface{}
	mapping := jwtMappingJSON(jwtMappingID1, "claim-secret", nil, true)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == jwtKeyMappingCreatePath:
			_ = json.NewDecoder(r.Body).Decode(&createBody)
			_ = json.NewEncoder(w).Encode(mapping)
		case r.Method == http.MethodGet && r.URL.Path == jwtKeyMappingInfoPath:
			_ = json.NewEncoder(w).Encode(mapping)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))

	// key_hash and key_wo are mutually exclusive.
	both := jwtMappingProtocolValue(t, schema, map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_hash": jwtTestKeyHash, "key_wo": "sk-raw-secret", "key_wo_version": "1"})
	validated, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: "litellm_jwt_key_mapping", Config: both, ClientCapabilities: &tfprotov6.ValidateResourceConfigClientCapabilities{WriteOnlyAttributesAllowed: true}})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(validated.Diagnostics) {
		t.Fatalf("key_hash with key_wo accepted: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(validated.Diagnostics))
	}
	if strings.Contains(agentProtocolDiagnosticsText(validated.Diagnostics), "sk-raw-secret") {
		t.Fatal("diagnostic leaked the raw key")
	}

	configValues := map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_hash": jwtTestKeyHash, "is_active": true}
	config := jwtMappingProtocolValue(t, schema, configValues)
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, ProposedNewState: jwtMappingCreateProposed(t, schema, configValues)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("create err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	if createBody["token"] != strings.ToLower(strings.TrimPrefix(jwtTestKeyHash, "sha256:")) {
		t.Fatalf("create token=%#v, want bare lowercase hash", createBody["token"])
	}
	if _, sent := createBody["key"]; sent {
		t.Fatalf("create sent a raw key alongside the hash: %#v", createBody)
	}

	plan := func(values map[string]interface{}, proposed map[string]interface{}) *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		response, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: jwtMappingProtocolValue(t, schema, values), PriorState: applied.NewState, ProposedNewState: organizationProjectProtocolReplace(t, schema, applied.NewState, proposed), PriorPrivate: applied.Private})
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	if steady := plan(configValues, nil); accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, applied.NewState, steady) != organizationProjectProtocolActionNoOp {
		t.Fatalf("steady plan diagnostics=%s action=%s", agentProtocolDiagnosticsText(steady.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, applied.NewState, steady))
	}
	// A claim change with only key_hash is a safe replacement.
	claimChange := map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "other-claim", "key_hash": jwtTestKeyHash, "is_active": true}
	replaced := plan(claimChange, map[string]interface{}{"jwt_claim_value": "other-claim"})
	if accessGroupProtocolDiagnosticsHaveError(replaced.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, applied.NewState, replaced) != organizationProjectProtocolActionReplace {
		t.Fatalf("claim replacement with key_hash diagnostics=%s action=%s", agentProtocolDiagnosticsText(replaced.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, applied.NewState, replaced))
	}
	// Pointing the mapping at another key replaces it.
	otherHash := "sha256:" + strings.Repeat("1", 64)
	rekeyed := plan(map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_hash": otherHash, "is_active": true}, map[string]interface{}{"key_hash": otherHash})
	if accessGroupProtocolDiagnosticsHaveError(rekeyed.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, applied.NewState, rekeyed) != organizationProjectProtocolActionReplace {
		t.Fatalf("key_hash change diagnostics=%s action=%s", agentProtocolDiagnosticsText(rekeyed.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, applied.NewState, rekeyed))
	}
}
