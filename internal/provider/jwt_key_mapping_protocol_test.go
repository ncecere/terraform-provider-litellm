package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func jwtMappingProtocolValue(t *testing.T, schema *tfprotov6.Schema, values map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	return accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
}

func jwtMappingCreateProposed(t *testing.T, schema *tfprotov6.Schema, config map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	values := map[string]interface{}{}
	for key, value := range config {
		values[key] = value
	}
	values["key_wo"] = nil
	for _, field := range []string{"id", "created_at", "updated_at", "created_by", "updated_by"} {
		values[field] = tftypes.UnknownValue
	}
	return jwtMappingProtocolValue(t, schema, values)
}

func TestJWTKeyMappingCreateUpdateClearDeleteProtocol(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	mapping := jwtMappingJSON(jwtMappingID1, "claim-secret", "owned-description", true)
	deleted := false
	var createBody, updateBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == jwtKeyMappingCreatePath:
			_ = json.NewDecoder(r.Body).Decode(&createBody)
			_ = json.NewEncoder(w).Encode(mapping)
		case r.Method == http.MethodPost && r.URL.Path == jwtKeyMappingUpdatePath:
			_ = json.NewDecoder(r.Body).Decode(&updateBody)
			if value, ok := updateBody["description"]; ok && value == nil {
				mapping["description"] = nil
			}
			if value, ok := updateBody["is_active"].(bool); ok {
				mapping["is_active"] = value
			}
			mapping["updated_at"] = "2026-08-26T00:02:00Z"
			_ = json.NewEncoder(w).Encode(mapping)
		case r.Method == http.MethodPost && r.URL.Path == jwtKeyMappingDeletePath:
			deleted = true
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
		case r.Method == http.MethodGet && r.URL.Path == jwtKeyMappingInfoPath:
			if r.URL.Query().Get("id") != jwtMappingID1 {
				t.Errorf("id query=%q", r.URL.Query().Get("id"))
			}
			if deleted {
				http.Error(w, `{"detail":"Mapping not found"}`, http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(mapping)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	configValues := map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo": "sk-create-secret", "key_wo_version": "1", "description": "owned-description", "is_active": true}
	config := jwtMappingProtocolValue(t, schema, configValues)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, ProposedNewState: jwtMappingCreateProposed(t, schema, configValues)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	for name, raw := range map[string][]byte{"plan JSON": planned.PlannedState.JSON, "plan msgpack": planned.PlannedState.MsgPack, "plan private": planned.PlannedPrivate, "plan diagnostics": []byte(agentProtocolDiagnosticsText(planned.Diagnostics))} {
		if bytes.Contains(raw, []byte("sk-create-secret")) {
			t.Fatalf("%s retained raw write-only key", name)
		}
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("create apply err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	for name, raw := range map[string][]byte{"state JSON": applied.NewState.JSON, "state msgpack": applied.NewState.MsgPack, "apply private": applied.Private, "apply diagnostics": []byte(agentProtocolDiagnosticsText(applied.Diagnostics))} {
		if bytes.Contains(raw, []byte("sk-create-secret")) {
			t.Fatalf("%s retained raw write-only key", name)
		}
	}
	if createBody["key"] != "sk-create-secret" || createBody["jwt_claim_value"] != "claim-secret" {
		t.Fatalf("create body=%#v", createBody)
	}
	attrs := protocolAttributeMap(t, schema, applied.NewState)
	if !attrs["key_wo"].IsNull() {
		t.Fatal("write-only key persisted")
	}

	updateConfigValues := map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo": "sk-create-secret", "key_wo_version": "1", "description": nil, "is_active": false}
	updateConfig := jwtMappingProtocolValue(t, schema, updateConfigValues)
	proposed := organizationProjectProtocolReplace(t, schema, applied.NewState, map[string]interface{}{"description": nil, "is_active": false})
	updatedPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: updateConfig, PriorState: applied.NewState, ProposedNewState: proposed, PriorPrivate: applied.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updatedPlan.Diagnostics) {
		t.Fatalf("update plan err=%v diagnostics=%v", err, updatedPlan.Diagnostics)
	}
	plannedAttributes := protocolAttributeMap(t, schema, updatedPlan.PlannedState)
	if plannedAttributes["updated_at"].IsKnown() || plannedAttributes["updated_by"].IsKnown() {
		t.Fatalf("mutable update pinned stale computed metadata: updated_at=%s updated_by=%s", plannedAttributes["updated_at"], plannedAttributes["updated_by"])
	}
	updated, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: updateConfig, PriorState: applied.NewState, PlannedState: updatedPlan.PlannedState, PlannedPrivate: updatedPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updated.Diagnostics) {
		t.Fatalf("update apply err=%v diagnostics=%v", err, updated.Diagnostics)
	}
	if value, ok := updateBody["description"]; !ok || value != nil {
		t.Fatalf("description clear body=%#v", updateBody)
	}
	if updateBody["is_active"] != false {
		t.Fatalf("update body=%#v", updateBody)
	}
	if _, present := updateBody["key"]; present {
		t.Fatalf("update sent unverifiable key rotation: %#v", updateBody)
	}

	steadyPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: updateConfig, PriorState: updated.NewState, ProposedNewState: updated.NewState, PriorPrivate: updated.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(steadyPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, updated.NewState, steadyPlan) != organizationProjectProtocolActionNoOp {
		t.Fatalf("steady plan err=%v diagnostics=%v action=%s", err, steadyPlan.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, updated.NewState, steadyPlan))
	}

	destroyPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: jwtMappingProtocolValue(t, schema, nil), PriorState: updated.NewState, ProposedNewState: nullState, PriorPrivate: updated.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyPlan.Diagnostics) {
		t.Fatalf("destroy plan err=%v diagnostics=%v", err, destroyPlan.Diagnostics)
	}
	destroyed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: jwtMappingProtocolValue(t, schema, nil), PriorState: updated.NewState, PlannedState: destroyPlan.PlannedState, PlannedPrivate: destroyPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyed.Diagnostics) {
		t.Fatalf("destroy err=%v diagnostics=%v", err, destroyed.Diagnostics)
	}
	if !deleted {
		t.Fatal("delete endpoint not called")
	}
}

func TestJWTKeyMappingMalformedUpdateRetainsPriorStateProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"` + jwtMappingID1 + `"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(jwtMappingJSON(jwtMappingID1, "claim-secret", "old", true))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	prior := jwtMappingProtocolValue(t, schema, map[string]interface{}{"id": jwtMappingID1, "jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo_version": "1", "description": "old", "is_active": true, "created_at": "2026-08-26T00:00:00Z", "updated_at": "2026-08-26T00:01:00Z"})
	configValues := map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo": "sk-secret", "key_wo_version": "1", "description": "new", "is_active": true}
	config := jwtMappingProtocolValue(t, schema, configValues)
	proposed := organizationProjectProtocolReplace(t, schema, prior, map[string]interface{}{"description": "new"})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: prior, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: prior, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("apply err=%v diagnostics=%v", err, applied.Diagnostics)
	}
	assertMCPServerFailedUpdateRetainsPriorState(t, schema, prior, applied.NewState)
}
