package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// LiteLLM v1.104.0 scopes JWT key mappings by (jwt_issuer, claim name, claim
// value) and always returns jwt_issuer, using null for the global scope.
// v1.98.0 omitted the field. Both shapes must decode.

func jwtMappingJSONWithIssuer(id, claimValue string, issuer interface{}) map[string]interface{} {
	mapping := jwtMappingJSON(id, claimValue, nil, true)
	mapping["jwt_issuer"] = issuer
	return mapping
}

func TestJWTKeyMappingIssuerDecodeShapes(t *testing.T) {
	for _, test := range []struct {
		name    string
		issuer  interface{}
		omit    bool
		want    *string
		wantErr bool
	}{
		{name: "v1.98 omitted field is global", omit: true},
		{name: "v1.104 null is global", issuer: nil},
		{name: "stored empty string is global", issuer: ""},
		{name: "issuer scope", issuer: "https://issuer.example", want: stringPointer("https://issuer.example")},
		{name: "non-string issuer", issuer: 7, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			object := jwtMappingJSON(jwtMappingID1, "claim-secret", nil, true)
			if !test.omit {
				object["jwt_issuer"] = test.issuer
			}
			raw, _ := json.Marshal(object)
			mapping, err := decodeJWTKeyMappingObject(raw)
			if (err != nil) != test.wantErr {
				t.Fatalf("err=%v wantErr=%t", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if !equalNullableString(mapping.Issuer, test.want) {
				t.Fatalf("issuer=%v want=%v", mapping.Issuer, test.want)
			}
			listItem, err := decodeJWTKeyMappingListObject(raw)
			if err != nil || !equalNullableString(listItem.Issuer, test.want) {
				t.Fatalf("list item issuer=%v err=%v", listItem.Issuer, err)
			}
		})
	}

	first := []jwtKeyMappingObject{{ID: jwtMappingID1, Issuer: stringPointer("a")}}
	second := []jwtKeyMappingObject{{ID: jwtMappingID1, Issuer: nil}}
	if jwtKeyMappingScansEqual(first, second) {
		t.Fatal("scan equality ignored an issuer change")
	}
}

func TestJWTKeyMappingIssuerCreateReplaceAndPreserveProtocol(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var createBodies []map[string]interface{}
	var mapping map[string]interface{}
	responseIssuer := func(body map[string]interface{}) interface{} {
		if value, ok := body["jwt_issuer"]; ok {
			return value
		}
		return nil
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == jwtKeyMappingCreatePath:
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			createBodies = append(createBodies, body)
			mapping = jwtMappingJSONWithIssuer(jwtMappingID1, "claim-secret", responseIssuer(body))
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
	create := func(configValues map[string]interface{}) *tfprotov6.ApplyResourceChangeResponse {
		t.Helper()
		config := jwtMappingProtocolValue(t, schema, configValues)
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, ProposedNewState: jwtMappingCreateProposed(t, schema, configValues)})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("create plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
		}
		if _, configured := configValues["jwt_issuer"]; !configured {
			if issuer := protocolAttributeMap(t, schema, planned.PlannedState)["jwt_issuer"]; !issuer.IsKnown() || !issuer.IsNull() {
				t.Fatalf("issuer-less create planned jwt_issuer=%s, want known null", issuer)
			}
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
			t.Fatalf("create apply err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
		}
		return applied
	}

	// Global scope: the request omits jwt_issuer so pre-1.104 servers keep working.
	globalConfig := map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo": "sk-create-secret", "key_wo_version": "1", "is_active": true}
	global := create(globalConfig)
	if _, sent := createBodies[0]["jwt_issuer"]; sent {
		t.Fatalf("global create sent jwt_issuer: %#v", createBodies[0])
	}
	if issuer := protocolAttributeMap(t, schema, global.NewState)["jwt_issuer"]; !issuer.IsNull() {
		t.Fatalf("global state jwt_issuer=%s", issuer)
	}

	// Issuer scope is sent, confirmed on read-back, and recorded in state.
	scopedConfig := map[string]interface{}{"jwt_issuer": "https://issuer.example", "jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo": "sk-create-secret", "key_wo_version": "1", "is_active": true}
	scoped := create(scopedConfig)
	if createBodies[1]["jwt_issuer"] != "https://issuer.example" {
		t.Fatalf("scoped create body=%#v", createBodies[1])
	}
	var issuer string
	if err := protocolAttributeMap(t, schema, scoped.NewState)["jwt_issuer"].As(&issuer); err != nil || issuer != "https://issuer.example" {
		t.Fatalf("scoped state jwt_issuer=%q err=%v", issuer, err)
	}

	plan := func(configValues, proposedValues map[string]interface{}, prior *tfprotov6.DynamicValue) *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		response, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: jwtMappingProtocolValue(t, schema, configValues), PriorState: prior, ProposedNewState: organizationProjectProtocolReplace(t, schema, prior, proposedValues), PriorPrivate: scoped.Private})
		if err != nil {
			t.Fatalf("plan err=%v", err)
		}
		return response
	}

	// Omitting jwt_issuer preserves the existing scope without a replacement.
	// Terraform core proposes the prior value for an omitted Optional+Computed
	// attribute, so the proposed state carries no change.
	omitted := map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo_version": "1", "is_active": true}
	preserved := plan(omitted, nil, scoped.NewState)
	if accessGroupProtocolDiagnosticsHaveError(preserved.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, scoped.NewState, preserved) != organizationProjectProtocolActionNoOp {
		t.Fatalf("omitted issuer plan diagnostics=%s action=%s", agentProtocolDiagnosticsText(preserved.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, scoped.NewState, preserved))
	}

	// Changing a known issuer is an identity change: replacement requires key_wo.
	changed := map[string]interface{}{"jwt_issuer": "https://other.example", "jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo_version": "1", "is_active": true}
	unsafe := plan(changed, map[string]interface{}{"jwt_issuer": "https://other.example"}, scoped.NewState)
	if !accessGroupProtocolDiagnosticsHaveError(unsafe.Diagnostics) {
		t.Fatalf("issuer change without key_wo was accepted: replace=%v", unsafe.RequiresReplace)
	}
	if text := agentProtocolDiagnosticsText(unsafe.Diagnostics); containsAny(text, "claim-secret", "issuer.example", "other.example") {
		t.Fatalf("replacement diagnostic leaked content: %s", text)
	}
	withKey := map[string]interface{}{"jwt_issuer": "https://other.example", "jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo": "sk-replacement-secret", "key_wo_version": "2", "is_active": true}
	replacement := plan(withKey, map[string]interface{}{"jwt_issuer": "https://other.example", "key_wo_version": "2"}, scoped.NewState)
	if accessGroupProtocolDiagnosticsHaveError(replacement.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, scoped.NewState, replacement) != organizationProjectProtocolActionReplace {
		t.Fatalf("issuer replacement diagnostics=%s action=%s", agentProtocolDiagnosticsText(replacement.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, scoped.NewState, replacement))
	}

	// Moving from the global (null) scope to an issuer is also a replacement.
	fromGlobal := plan(withKey, map[string]interface{}{"jwt_issuer": "https://other.example", "key_wo_version": "2"}, global.NewState)
	if accessGroupProtocolDiagnosticsHaveError(fromGlobal.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, global.NewState, fromGlobal) != organizationProjectProtocolActionReplace {
		t.Fatalf("global-to-issuer diagnostics=%s action=%s", agentProtocolDiagnosticsText(fromGlobal.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, global.NewState, fromGlobal))
	}
}

func TestJWTKeyMappingIssuerCreateMismatchRetainsOnlyIdentity(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == jwtKeyMappingCreatePath {
			// A server that ignores the requested issuer (for example a pre-1.104
			// proxy) creates a global mapping instead.
			_ = json.NewEncoder(w).Encode(jwtMappingJSON(jwtMappingID1, "claim-secret", nil, true))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	configValues := map[string]interface{}{"jwt_issuer": "https://issuer.example", "jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo": "sk-create-secret", "key_wo_version": "1", "is_active": true}
	config := jwtMappingProtocolValue(t, schema, configValues)
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, ProposedNewState: jwtMappingCreateProposed(t, schema, configValues)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("issuer mismatch was accepted: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	attrs := protocolAttributeMap(t, schema, applied.NewState)
	var id string
	if err := attrs["id"].As(&id); err != nil || id != jwtMappingID1 || !attrs["jwt_issuer"].IsNull() || !attrs["jwt_claim_value"].IsNull() {
		t.Fatalf("mismatch state was not identity-only: id=%q issuer=%s", id, attrs["jwt_issuer"])
	}
}
