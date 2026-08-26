package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestJWTKeyMappingWriteOnlyClientCapabilityOnOffProtocol(t *testing.T) {
	ctx := context.Background()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, "http://127.0.0.1:1")
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	withKey := jwtMappingProtocolValue(t, schema, map[string]interface{}{"jwt_claim_name": "", "jwt_claim_value": "", "key_wo": "sk-secret", "key_wo_version": "1"})
	withoutKey := jwtMappingProtocolValue(t, schema, map[string]interface{}{})
	for _, test := range []struct {
		name       string
		config     *tfprotov6.DynamicValue
		capability bool
		wantError  bool
	}{
		{"create-capability-on", withKey, true, false},
		{"create-capability-off", withKey, false, true},
		{"null-import-read-capability-off", withoutKey, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: "litellm_jwt_key_mapping", Config: test.config, ClientCapabilities: &tfprotov6.ValidateResourceConfigClientCapabilities{WriteOnlyAttributesAllowed: test.capability}})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) != test.wantError {
				t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
			}
			if text := agentProtocolDiagnosticsText(response.Diagnostics); strings.Contains(text, "sk-secret") {
				t.Fatalf("diagnostic leaked write-only key: %s", text)
			}
		})
	}
}

func TestJWTKeyMappingRotationRejectedDuringPlanWithoutMutationProtocol(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	prior := jwtMappingProtocolValue(t, schema, map[string]interface{}{"id": jwtMappingID1, "jwt_claim_name": "", "jwt_claim_value": "claim-secret", "key_wo_version": "1", "description": nil, "is_active": true, "created_at": "2026-08-26T00:00:00Z", "updated_at": "2026-08-26T00:01:00Z"})
	configValues := map[string]interface{}{"jwt_claim_name": "", "jwt_claim_value": "claim-secret", "key_wo": "sk-rotate-secret", "key_wo_version": "2", "description": nil, "is_active": true}
	config := jwtMappingProtocolValue(t, schema, configValues)
	proposed := organizationProjectProtocolReplace(t, schema, prior, map[string]interface{}{"key_wo_version": "2"})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: prior, ProposedNewState: proposed})
	text := agentProtocolDiagnosticsText(planned.Diagnostics)
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || !strings.Contains(text, "Unsupported JWT Key Rotation") {
		t.Fatalf("plan err=%v diagnostics=%s", err, text)
	}
	if containsAny(text, "claim-secret", "sk-rotate-secret", "2") {
		t.Fatalf("rotation diagnostic leaked content: %s", text)
	}
	if requests.Load() != 0 {
		t.Fatalf("rotation planning made %d API requests", requests.Load())
	}
}

func TestJWTKeyMappingClaimReplacementCredentialSafetyProtocol(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	prior := jwtMappingProtocolValue(t, schema, map[string]interface{}{"id": jwtMappingID1, "jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo_version": "1", "description": nil, "is_active": true, "created_at": "2026-08-26T00:00:00Z", "updated_at": "2026-08-26T00:01:00Z"})
	plan := func(configValues, proposedValues map[string]interface{}, priorState *tfprotov6.DynamicValue) *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		config := jwtMappingProtocolValue(t, schema, configValues)
		proposed := organizationProjectProtocolReplace(t, schema, priorState, proposedValues)
		response, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: priorState, ProposedNewState: proposed})
		if err != nil {
			t.Fatalf("plan err=%v", err)
		}
		return response
	}

	for _, test := range []struct {
		name        string
		config      map[string]interface{}
		proposed    map[string]interface{}
		wantError   bool
		wantReplace bool
	}{
		{
			name:        "changed version with known key",
			config:      map[string]interface{}{"jwt_claim_name": "aud", "jwt_claim_value": "claim-secret", "key_wo": "sk-replacement-secret", "key_wo_version": "2", "is_active": true},
			proposed:    map[string]interface{}{"jwt_claim_name": "aud", "key_wo_version": "2"},
			wantReplace: true,
		},
		{
			name:        "claim value change with changed version",
			config:      map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "replacement-claim-secret", "key_wo": "sk-replacement-secret", "key_wo_version": "2", "is_active": true},
			proposed:    map[string]interface{}{"jwt_claim_value": "replacement-claim-secret", "key_wo_version": "2"},
			wantReplace: true,
		},
		{
			name:        "unchanged version with known key",
			config:      map[string]interface{}{"jwt_claim_name": "aud", "jwt_claim_value": "claim-secret", "key_wo": "sk-replacement-secret", "key_wo_version": "1", "is_active": true},
			proposed:    map[string]interface{}{"jwt_claim_name": "aud"},
			wantReplace: true,
		},
		{
			name:      "missing key and version",
			config:    map[string]interface{}{"jwt_claim_name": "aud", "jwt_claim_value": "claim-secret", "is_active": true},
			proposed:  map[string]interface{}{"jwt_claim_name": "aud", "key_wo_version": nil},
			wantError: true,
		},
		{
			name:      "null key",
			config:    map[string]interface{}{"jwt_claim_name": "aud", "jwt_claim_value": "claim-secret", "key_wo": nil, "key_wo_version": "1", "is_active": true},
			proposed:  map[string]interface{}{"jwt_claim_name": "aud"},
			wantError: true,
		},
		{
			name:      "empty key",
			config:    map[string]interface{}{"jwt_claim_name": "aud", "jwt_claim_value": "claim-secret", "key_wo": "", "key_wo_version": "1", "is_active": true},
			proposed:  map[string]interface{}{"jwt_claim_name": "aud"},
			wantError: true,
		},
		{
			name:      "unknown key",
			config:    map[string]interface{}{"jwt_claim_name": "aud", "jwt_claim_value": "claim-secret", "key_wo": tftypes.UnknownValue, "key_wo_version": "1", "is_active": true},
			proposed:  map[string]interface{}{"jwt_claim_name": "aud"},
			wantError: true,
		},
		{
			name:      "empty version",
			config:    map[string]interface{}{"jwt_claim_name": "aud", "jwt_claim_value": "claim-secret", "key_wo": "sk-replacement-secret", "key_wo_version": "", "is_active": true},
			proposed:  map[string]interface{}{"jwt_claim_name": "aud", "key_wo_version": ""},
			wantError: true,
		},
		{
			name:      "unknown version",
			config:    map[string]interface{}{"jwt_claim_name": "aud", "jwt_claim_value": "claim-secret", "key_wo": "sk-replacement-secret", "key_wo_version": tftypes.UnknownValue, "is_active": true},
			proposed:  map[string]interface{}{"jwt_claim_name": "aud", "key_wo_version": tftypes.UnknownValue},
			wantError: true,
		},
		{
			name:      "incomplete unknown claim pair",
			config:    map[string]interface{}{"jwt_claim_name": "aud", "jwt_claim_value": tftypes.UnknownValue, "key_wo": "sk-replacement-secret", "key_wo_version": "1", "is_active": true},
			proposed:  map[string]interface{}{"jwt_claim_name": "aud", "jwt_claim_value": tftypes.UnknownValue},
			wantError: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			planned := plan(test.config, test.proposed, prior)
			text := agentProtocolDiagnosticsText(planned.Diagnostics)
			if accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) != test.wantError {
				t.Fatalf("diagnostics=%s replace=%v", text, planned.RequiresReplace)
			}
			if got := len(planned.RequiresReplace) != 0; got != test.wantReplace && !test.wantError {
				t.Fatalf("requires replace=%v, want %t", planned.RequiresReplace, test.wantReplace)
			}
			if !test.wantError && organizationProjectProtocolPlannedAction(t, schema, prior, planned) != organizationProjectProtocolActionReplace {
				t.Fatalf("action=%s replace=%v", organizationProjectProtocolPlannedAction(t, schema, prior, planned), planned.RequiresReplace)
			}
			if containsAny(text, "claim-secret", "replacement-claim-secret", "sk-replacement-secret") {
				t.Fatalf("replacement diagnostic leaked content: %s", text)
			}
		})
	}

	importedKeyless := jwtMappingProtocolValue(t, schema, map[string]interface{}{"id": jwtMappingID1, "jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo_version": nil, "description": nil, "is_active": true, "created_at": "2026-08-26T00:00:00Z", "updated_at": "2026-08-26T00:01:00Z"})
	keylessReplacement := plan(
		map[string]interface{}{"jwt_claim_name": "aud", "jwt_claim_value": "claim-secret", "is_active": true},
		map[string]interface{}{"jwt_claim_name": "aud"},
		importedKeyless,
	)
	if !accessGroupProtocolDiagnosticsHaveError(keylessReplacement.Diagnostics) {
		t.Fatalf("imported keyless replacement was accepted: replace=%v", keylessReplacement.RequiresReplace)
	}

	unknownConfig := map[string]interface{}{"jwt_claim_name": tftypes.UnknownValue, "jwt_claim_value": "claim-secret", "key_wo": "sk-replacement-secret", "key_wo_version": "1", "is_active": true}
	unknownClaim := plan(unknownConfig, map[string]interface{}{"jwt_claim_name": tftypes.UnknownValue}, prior)
	if accessGroupProtocolDiagnosticsHaveError(unknownClaim.Diagnostics) || len(unknownClaim.RequiresReplace) != 0 {
		t.Fatalf("unknown claim scheduled replacement: diagnostics=%s replace=%v", agentProtocolDiagnosticsText(unknownClaim.Diagnostics), unknownClaim.RequiresReplace)
	}
	if action := organizationProjectProtocolPlannedAction(t, schema, prior, unknownClaim); action == organizationProjectProtocolActionDelete || action == organizationProjectProtocolActionReplace {
		t.Fatalf("unknown claim action=%s", action)
	}

	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	destroyed, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: jwtMappingProtocolValue(t, schema, nil), PriorState: prior, ProposedNewState: nullState})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyed.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, prior, destroyed) != organizationProjectProtocolActionDelete {
		t.Fatalf("destroy plan err=%v diagnostics=%s replace=%v", err, agentProtocolDiagnosticsText(destroyed.Diagnostics), destroyed.RequiresReplace)
	}
	if requests.Load() != 0 {
		t.Fatalf("planning made %d API requests", requests.Load())
	}
}

func TestJWTKeyMappingHistoricalVersionCanBeOmittedWithoutDiffProtocol(t *testing.T) {
	ctx := context.Background()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, "http://127.0.0.1:1")
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	prior := jwtMappingProtocolValue(t, schema, map[string]interface{}{"id": jwtMappingID1, "jwt_claim_name": "sub", "jwt_claim_value": "value", "key_wo_version": "historical", "description": "api-owned", "is_active": true, "created_at": "2026-08-26T00:00:00Z", "updated_at": "2026-08-26T00:01:00Z"})
	config := jwtMappingProtocolValue(t, schema, map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "value"})
	proposed := organizationProjectProtocolReplace(t, schema, prior, map[string]interface{}{"key_wo_version": nil})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: prior, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	attributes := protocolAttributeMap(t, schema, planned.PlannedState)
	var version string
	if err := attributes["key_wo_version"].As(&version); err != nil || version != "historical" {
		t.Fatalf("version=%q err=%v", version, err)
	}
}

func TestJWTKeyMappingUpdateResponseLossRetainsPriorStateProtocol(t *testing.T) {
	ctx := context.Background()
	var updates atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != jwtKeyMappingUpdatePath {
			http.NotFound(w, r)
			return
		}
		updates.Add(1)
		connection, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Fatal(err)
		}
		defer connection.Close()
		_, _ = buffer.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 500\r\n\r\n{\"id\":\"")
		_ = buffer.Flush()
	}))
	server.Start()
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	prior := jwtMappingProtocolValue(t, schema, map[string]interface{}{"id": jwtMappingID1, "jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo_version": "1", "description": "old", "is_active": true, "created_at": "2026-08-26T00:00:00Z", "updated_at": "2026-08-26T00:01:00Z"})
	configValues := map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo": "sk-secret", "key_wo_version": "1", "description": "new", "is_active": true}
	config := jwtMappingProtocolValue(t, schema, configValues)
	proposed := organizationProjectProtocolReplace(t, schema, prior, map[string]interface{}{"description": "new"})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: prior, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: prior, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	text := agentProtocolDiagnosticsText(applied.Diagnostics)
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || updates.Load() != 1 || containsAny(text, "claim-secret", "sk-secret", "new") {
		t.Fatalf("apply err=%v diagnostics=%s updates=%d", err, text, updates.Load())
	}
	before, _ := prior.Unmarshal(schema.ValueType())
	after, _ := applied.NewState.Unmarshal(schema.ValueType())
	if !before.Equal(after) {
		t.Fatal("response-loss update changed prior state")
	}
}

func TestJWTKeyMappingFalseCreateUsesOneControlledUpdateAndFreshReadProtocol(t *testing.T) {
	ctx := context.Background()
	mapping := jwtMappingJSON(jwtMappingID1, "", "description", true)
	mapping["jwt_claim_name"] = ""
	var mu sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == jwtKeyMappingCreatePath:
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if _, present := body["is_active"]; present {
				t.Error("v1.98 create body unexpectedly sent is_active")
			}
			_ = json.NewEncoder(w).Encode(mapping)
		case r.Method == http.MethodPost && r.URL.Path == jwtKeyMappingUpdatePath:
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 2 || body["id"] != jwtMappingID1 || body["is_active"] != false {
				t.Errorf("deactivation body=%#v", body)
			}
			mapping["is_active"] = false
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
	configValues := map[string]interface{}{"jwt_claim_name": "", "jwt_claim_value": "", "key_wo": "sk-create-secret", "key_wo_version": "1", "description": "description", "is_active": false}
	config := jwtMappingProtocolValue(t, schema, configValues)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, ProposedNewState: jwtMappingCreateProposed(t, schema, configValues)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("apply err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	var active bool
	if err := attributes["is_active"].As(&active); err != nil || active {
		t.Fatalf("active=%v err=%v", active, err)
	}
	want := []string{"POST " + jwtKeyMappingCreatePath, "POST " + jwtKeyMappingUpdatePath, "GET " + jwtKeyMappingInfoPath + "?id=" + jwtMappingID1}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
}

func TestJWTKeyMappingFalseCreateFailureRetainsUUIDOnlyAndNeverRecreatesProtocol(t *testing.T) {
	for _, failure := range []string{"update response", "fresh read"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			mapping := jwtMappingJSON(jwtMappingID1, "value", nil, true)
			var creates, updates, reads atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost && r.URL.Path == jwtKeyMappingCreatePath:
					creates.Add(1)
					_ = json.NewEncoder(w).Encode(mapping)
				case r.Method == http.MethodPost && r.URL.Path == jwtKeyMappingUpdatePath:
					updates.Add(1)
					mapping["is_active"] = false
					if failure == "update response" {
						_, _ = w.Write([]byte(`{"id":"` + jwtMappingID1 + `"}`))
						return
					}
					_ = json.NewEncoder(w).Encode(mapping)
				case r.Method == http.MethodGet && r.URL.Path == jwtKeyMappingInfoPath:
					if reads.Add(1) == 1 && failure == "fresh read" {
						http.Error(w, `{"detail":"read-secret"}`, http.StatusInternalServerError)
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
			configValues := map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "value", "key_wo": "sk-secret", "key_wo_version": "1", "description": nil, "is_active": false}
			config := jwtMappingProtocolValue(t, schema, configValues)
			nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
			planned, _ := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, ProposedNewState: jwtMappingCreateProposed(t, schema, configValues)})
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
			refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_jwt_key_mapping", CurrentState: applied.NewState, Private: applied.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
				t.Fatalf("recovery read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(refreshed.Diagnostics))
			}
			if creates.Load() != 1 || updates.Load() != 1 {
				t.Fatalf("creates=%d updates=%d", creates.Load(), updates.Load())
			}
		})
	}
}

func TestJWTKeyMappingDelete404StillRequiresInfo404Protocol(t *testing.T) {
	ctx := context.Background()
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	prior := jwtMappingProtocolValue(t, schema, map[string]interface{}{"id": jwtMappingID1, "jwt_claim_name": "", "jwt_claim_value": "", "key_wo_version": nil, "description": nil, "is_active": false, "created_at": "2026-08-26T00:00:00Z", "updated_at": "2026-08-26T00:01:00Z"})
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	destroyed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: jwtMappingProtocolValue(t, schema, nil), PriorState: prior, PlannedState: nullState})
	terminal, unmarshalErr := destroyed.NewState.Unmarshal(schema.ValueType())
	if err != nil || unmarshalErr != nil || accessGroupProtocolDiagnosticsHaveError(destroyed.Diagnostics) || !terminal.IsNull() {
		t.Fatalf("destroy err=%v unmarshal=%v diagnostics=%s state=%s", err, unmarshalErr, agentProtocolDiagnosticsText(destroyed.Diagnostics), destroyed.NewState)
	}
	want := []string{"POST " + jwtKeyMappingDeletePath, "GET " + jwtKeyMappingInfoPath + "?id=" + jwtMappingID1}
	if fmt.Sprint(calls) != fmt.Sprint(want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
}

func TestJWTKeyMappingCreateResponseLossThen409RequiresManualImportProtocol(t *testing.T) {
	ctx := context.Background()
	var creates atomic.Int64
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != jwtKeyMappingCreatePath {
			http.NotFound(w, r)
			return
		}
		if creates.Add(1) == 1 {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("server does not support hijacking")
			}
			connection, buffer, err := hijacker.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			_, _ = buffer.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 500\r\n\r\n{\"id\":\"")
			_ = buffer.Flush()
			return
		}
		http.Error(w, `{"detail":"duplicate claim-secret sk-secret"}`, http.StatusConflict)
	}))
	server.Start()
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	configValues := map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "claim-secret", "key_wo": "sk-secret", "key_wo_version": "1", "description": "description", "is_active": true}
	config := jwtMappingProtocolValue(t, schema, configValues)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, ProposedNewState: jwtMappingCreateProposed(t, schema, configValues)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	for attempt := 1; attempt <= 2; attempt++ {
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_jwt_key_mapping", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		text := agentProtocolDiagnosticsText(applied.Diagnostics)
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || !strings.Contains(text, "import") || containsAny(text, "claim-secret", "sk-secret", "description") {
			t.Fatalf("attempt=%d err=%v diagnostics=%s", attempt, err, text)
		}
		value, unmarshalErr := applied.NewState.Unmarshal(schema.ValueType())
		if unmarshalErr != nil || !value.IsNull() {
			t.Fatalf("attempt=%d guessed state=%s unmarshal=%v", attempt, applied.NewState, unmarshalErr)
		}
	}
	if creates.Load() != 2 {
		t.Fatalf("create calls=%d", creates.Load())
	}
}
