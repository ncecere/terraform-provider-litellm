package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestJWTKeyMappingMalformedCreateRetainsConfirmedUUIDOnlyProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + jwtMappingID1 + `","jwt_claim_name":"sub"}`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	configValues := map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo": "sk-secret", "key_wo_version": "1", "description": "planned", "is_active": true}
	config := jwtMappingProtocolValue(t, schema, configValues)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, ProposedNewState: jwtMappingCreateProposed(t, schema, configValues)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("apply err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	var id string
	if err := attributes["id"].As(&id); err != nil || id != jwtMappingID1 {
		t.Fatalf("id=%q err=%v", id, err)
	}
	for _, field := range []string{"jwt_claim_name", "jwt_claim_value", "key_wo_version", "description", "is_active", "created_at", "updated_at", "created_by", "updated_by"} {
		if !attributes[field].IsNull() {
			t.Fatalf("unconfirmed %s persisted: %s", field, attributes[field])
		}
	}
	if text := agentProtocolDiagnosticsText(applied.Diagnostics); containsAny(text, "claim-secret", "sk-secret", "planned") {
		t.Fatalf("diagnostic leaked configured value: %s", text)
	}
}

func containsAny(value string, secrets ...string) bool {
	for _, secret := range secrets {
		if len(secret) > 0 && strings.Contains(value, secret) {
			return true
		}
	}
	return false
}
