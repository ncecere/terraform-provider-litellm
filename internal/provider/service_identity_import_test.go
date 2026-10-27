package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const (
	keyServiceIdentityRawToken = "sk-synthetic-service-identity-test"
	keyServiceIdentityID       = "synthetic-service-account-a"
)

func keyServiceIdentityAPI(t *testing.T, lookup string, metadata interface{}, metadataPresent bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	info := map[string]interface{}{
		"key_alias":          "synthetic-refreshed-alias",
		"blocked":            true,
		"models":             []interface{}{"synthetic-refreshed-model"},
		"service_account_id": "synthetic-top-level-identity-not-authoritative",
	}
	if metadataPresent {
		info["metadata"] = metadata
	}
	body, err := json.Marshal(map[string]interface{}{"key": lookup, "info": info})
	if err != nil {
		t.Fatal(err)
	}
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet || request.URL.Path != "/key/info" || request.URL.Query().Get("key") != lookup {
			t.Errorf("unexpected synthetic key request: %s %s", request.Method, request.URL.Path)
			http.Error(writer, `{"detail":"unexpected-synthetic-request"}`, http.StatusBadRequest)
			return
		}
		_, _ = writer.Write(body)
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func keyServiceIdentityImported(t *testing.T, ctx context.Context, server tfprotov6.ProviderServer, schema *tfprotov6.Schema, id string) *tfprotov6.ImportedResource {
	t.Helper()
	response, err := server.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_key", ID: id})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || len(response.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, response.Diagnostics)
	}
	imported := response.ImportedResources[0]
	attributes := protocolAttributeMap(t, schema, imported.State)
	if !attributes["service_account_id"].IsNull() || !protocolPrivateHasKey(t, imported.Private, numericImportedPrivateKey) {
		t.Fatal("import must defer service identity hydration to its exact read")
	}
	return imported
}

func assertKeyServiceIdentity(t *testing.T, attributes map[string]tftypes.Value, want interface{}) {
	t.Helper()
	expected := tftypes.NewValue(tftypes.String, want)
	if !attributes["service_account_id"].Equal(expected) {
		t.Fatalf("service_account_id=%s, want %s", attributes["service_account_id"], expected)
	}
}

func TestKeyServiceIdentityImportReadProtocol(t *testing.T) {
	ctx := context.Background()
	for _, hashMode := range []bool{false, true} {
		mode := "stateful"
		id, lookup := keyServiceIdentityRawToken, keyServiceIdentityRawToken
		if hashMode {
			mode = "hash-write-only"
			id = hashKeyForID(keyServiceIdentityRawToken)
			lookup = strings.TrimPrefix(id, "sha256:")
		}
		for _, test := range []struct {
			name            string
			metadata        interface{}
			metadataPresent bool
			want            interface{}
		}{
			{"valid", map[string]interface{}{"owner": "synthetic-owner", "service_account_id": keyServiceIdentityID}, true, keyServiceIdentityID},
			{"missing-metadata", nil, false, nil},
			{"null-metadata", nil, true, nil},
			{"missing-member", map[string]interface{}{"owner": "synthetic-owner"}, true, nil},
			{"null-member", map[string]interface{}{"service_account_id": nil}, true, nil},
		} {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				api, requests := keyServiceIdentityAPI(t, lookup, test.metadata, test.metadataPresent)
				server, schemas := configuredImportProtocolServer(t, ctx, api.URL)
				schema := schemas.ResourceSchemas["litellm_key"]
				imported := keyServiceIdentityImported(t, ctx, server, schema, id)
				if requests.Load() != 0 {
					t.Fatal("import made an API request before Read")
				}
				read, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_key", CurrentState: imported.State, Private: imported.Private})
				if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
					t.Fatalf("read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
				}
				attributes := protocolAttributeMap(t, schema, read.NewState)
				assertKeyServiceIdentity(t, attributes, test.want)
				if !attributes["id"].Equal(tftypes.NewValue(tftypes.String, hashKeyForID(keyServiceIdentityRawToken))) || !attributes["key_wo"].IsNull() {
					t.Fatal("read changed the management identity or persisted the write-only key")
				}
				if hashMode {
					if !attributes["key"].IsNull() || !attributes["key_wo_version"].Equal(tftypes.NewValue(tftypes.String, "1")) || bytes.Contains(read.NewState.MsgPack, []byte(keyServiceIdentityRawToken)) || bytes.Contains(read.NewState.JSON, []byte(keyServiceIdentityRawToken)) {
						t.Fatal("hash import/read must retain version 1 without persisting the raw token")
					}
				} else if !attributes["key"].Equal(tftypes.NewValue(tftypes.String, keyServiceIdentityRawToken)) {
					t.Fatal("stateful import lost its sensitive key")
				}
				if protocolPrivateHasKey(t, read.Private, numericImportedPrivateKey) || requests.Load() != 1 {
					t.Fatal("successful exact import read did not consume its marker exactly once")
				}
				refreshed, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_key", CurrentState: read.NewState, Private: read.Private})
				if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
					t.Fatalf("ordinary read after import: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(refreshed.Diagnostics))
				}
				assertKeyServiceIdentity(t, protocolAttributeMap(t, schema, refreshed.NewState), test.want)
				if requests.Load() != 2 {
					t.Fatal("ordinary read did not use one exact key lookup")
				}
			})
		}
	}
}

func TestKeyServiceIdentityNormalReadOwnershipProtocol(t *testing.T) {
	ctx := context.Background()
	for _, priorIdentity := range []interface{}{nil, keyServiceIdentityID} {
		mode := "unconfigured-null"
		if priorIdentity != nil {
			mode = "known"
		}
		for _, test := range []struct {
			name            string
			metadata        interface{}
			metadataPresent bool
		}{
			{"matching-visible", map[string]interface{}{"owner": "synthetic-refreshed-owner", "service_account_id": keyServiceIdentityID}, true},
			{"missing-metadata", nil, false},
			{"null-metadata", nil, true},
			{"missing-member", map[string]interface{}{"owner": "synthetic-refreshed-owner"}, true},
			{"null-member", map[string]interface{}{"owner": "synthetic-refreshed-owner", "service_account_id": nil}, true},
		} {
			t.Run(mode+"/"+test.name, func(t *testing.T) {
				api, requests := keyServiceIdentityAPI(t, keyServiceIdentityRawToken, test.metadata, test.metadataPresent)
				server, schemas := configuredImportProtocolServer(t, ctx, api.URL)
				schema := schemas.ResourceSchemas["litellm_key"]
				attributes := protocolAttributeMap(t, schema, keySafeReadProtocolPriorState(t, schema, keyServiceIdentityRawToken))
				attributes["service_account_id"] = tftypes.NewValue(tftypes.String, priorIdentity)
				attributes["metadata"] = tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{"owner": tftypes.NewValue(tftypes.String, "synthetic-prior-owner")})
				prior := keyNumericProtocolDynamic(t, schema, tftypes.NewValue(schema.ValueType(), attributes))
				read, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_key", CurrentState: prior})
				if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
					t.Fatalf("read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
				}
				refreshed := protocolAttributeMap(t, schema, read.NewState)
				assertKeyServiceIdentity(t, refreshed, priorIdentity)
				if !refreshed["key_alias"].Equal(tftypes.NewValue(tftypes.String, "synthetic-refreshed-alias")) || requests.Load() != 1 {
					t.Fatal("normal read did not refresh the exact key")
				}
				var metadata map[string]tftypes.Value
				if err := refreshed["metadata"].As(&metadata); err != nil {
					t.Fatal(err)
				}
				if _, exists := metadata["service_account_id"]; exists {
					t.Fatal("identity hydration must not depend on or broaden configured metadata projection")
				}
			})
		}
	}
}

func TestKeyServiceIdentityErrorsRetainRawStateProtocol(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name      string
		metadata  interface{}
		knownOnly bool
	}{
		{"contradictory", map[string]interface{}{"service_account_id": "synthetic-conflicting-service-identity"}, true},
		{"empty", map[string]interface{}{"service_account_id": ""}, false},
		{"number", map[string]interface{}{"service_account_id": 123}, false},
		{"boolean", map[string]interface{}{"service_account_id": true}, false},
		{"object-member", map[string]interface{}{"service_account_id": map[string]interface{}{"synthetic-secret-field": "synthetic-payload-secret"}}, false},
		{"array-member", map[string]interface{}{"service_account_id": []interface{}{"synthetic-payload-secret"}}, false},
		{"json-string-metadata", `{"service_account_id":"synthetic-payload-secret"}`, false},
		{"array-metadata", []interface{}{"synthetic-payload-secret"}, false},
	} {
		for _, mode := range []string{"import", "known", "unconfigured-null"} {
			if test.knownOnly && mode != "known" {
				continue
			}
			t.Run(test.name+"/"+mode, func(t *testing.T) {
				api, requests := keyServiceIdentityAPI(t, keyServiceIdentityRawToken, test.metadata, true)
				server, schemas := configuredImportProtocolServer(t, ctx, api.URL)
				schema := schemas.ResourceSchemas["litellm_key"]
				prior := keySafeReadProtocolPriorState(t, schema, keyServiceIdentityRawToken)
				var private []byte
				if mode == "import" {
					imported := keyServiceIdentityImported(t, ctx, server, schema, keyServiceIdentityRawToken)
					prior, private = imported.State, imported.Private
				} else if mode == "known" {
					attributes := protocolAttributeMap(t, schema, prior)
					attributes["service_account_id"] = tftypes.NewValue(tftypes.String, keyServiceIdentityID)
					prior = keyNumericProtocolDynamic(t, schema, tftypes.NewValue(schema.ValueType(), attributes))
				}
				read, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_key", CurrentState: prior, Private: private})
				if mode == "unconfigured-null" && !strings.HasSuffix(test.name, "-metadata") {
					// An unmanaged identity is not validated on ordinary refresh, so a
					// malformed metadata member never makes an unchanged key unreadable.
					if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
						t.Fatalf("unmanaged identity failed refresh: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
					}
					if !protocolAttributeMap(t, schema, read.NewState)["service_account_id"].IsNull() {
						t.Fatal("ordinary refresh backfilled an unmanaged identity")
					}
					return
				}
				if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
					t.Fatalf("read must reject unsafe identity: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
				}
				assertKeyRawStateUnchanged(t, prior, read.NewState)
				if mode == "import" {
					var before, after map[string]json.RawMessage
					if err := json.Unmarshal(private, &before); err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(read.Private, &after); err != nil {
						t.Fatal(err)
					}
					// The framework consumes its own first-read marker, including on
					// diagnostics. Provider ownership and import markers must survive.
					delete(before, ".import_before_read")
					if !reflect.DeepEqual(before, after) {
						t.Fatal("identity error changed provider private state or consumed its import marker")
					}
				} else if !bytes.Equal(private, read.Private) {
					t.Fatal("identity error changed private state")
				}
				text := agentProtocolDiagnosticsText(read.Diagnostics)
				for _, secret := range []string{keyServiceIdentityRawToken, keyServiceIdentityID, "synthetic-conflicting-service-identity", "synthetic-payload-secret", "synthetic-secret-field", "synthetic-refreshed-alias"} {
					if strings.Contains(text, secret) {
						t.Fatalf("identity diagnostic exposed synthetic response/state contents: %s", text)
					}
				}
				if requests.Load() != 1 {
					t.Fatal("invalid exact response was retried or used another endpoint")
				}
			})
		}
	}
}

func TestKeyServiceIdentityImportReadPlanRequiresReplaceProtocol(t *testing.T) {
	ctx := context.Background()
	for _, hashMode := range []bool{false, true} {
		mode := "stateful"
		id, lookup := keyServiceIdentityRawToken, keyServiceIdentityRawToken
		if hashMode {
			mode = "hash-write-only"
			id = hashKeyForID(keyServiceIdentityRawToken)
			lookup = strings.TrimPrefix(id, "sha256:")
		}
		t.Run(mode, func(t *testing.T) {
			api, requests := keyServiceIdentityAPI(t, lookup, map[string]interface{}{"service_account_id": keyServiceIdentityID}, true)
			server, schemas := configuredImportProtocolServer(t, ctx, api.URL)
			schema := schemas.ResourceSchemas["litellm_key"]
			for _, attribute := range schema.Block.Attributes {
				if attribute.Name == "service_account_id" && (!attribute.Optional || attribute.Computed) {
					t.Fatal("service_account_id must remain Optional and non-Computed")
				}
			}
			imported := keyServiceIdentityImported(t, ctx, server, schema, id)
			read, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_key", CurrentState: imported.State, Private: imported.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
			}
			assertKeyServiceIdentity(t, protocolAttributeMap(t, schema, read.NewState), keyServiceIdentityID)
			for _, test := range []struct {
				name        string
				configured  interface{}
				wantReplace bool
			}{
				{"matching-config", keyServiceIdentityID, false},
				{"omitted-config", nil, true},
				{"different-config", "synthetic-service-account-b", true},
			} {
				t.Run(test.name, func(t *testing.T) {
					overrides := map[string]tftypes.Value{"service_account_id": tftypes.NewValue(tftypes.String, test.configured)}
					if hashMode {
						overrides["key_wo"] = tftypes.NewValue(tftypes.String, keyServiceIdentityRawToken)
						overrides["key_wo_version"] = tftypes.NewValue(tftypes.String, "1")
					}
					config := keyNumericProtocolDynamic(t, schema, keyNumericProtocolValue(t, schema, false, overrides))
					proposedAttributes := protocolAttributeMap(t, schema, read.NewState)
					proposedAttributes["service_account_id"] = overrides["service_account_id"]
					proposed := keyNumericProtocolDynamic(t, schema, tftypes.NewValue(schema.ValueType(), proposedAttributes))
					planned, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
						TypeName: "litellm_key", Config: config, PriorState: read.NewState,
						PriorPrivate: read.Private, ProposedNewState: proposed,
					})
					if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
						t.Fatalf("plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
					}
					if test.wantReplace {
						if len(planned.RequiresReplace) != 1 || !planned.RequiresReplace[0].Equal(tftypes.NewAttributePath().WithAttributeName("service_account_id")) {
							t.Fatalf("RequiresReplace=%v, want only service_account_id", planned.RequiresReplace)
						}
					} else {
						if len(planned.RequiresReplace) != 0 {
							t.Fatalf("matching service identity scheduled replacement: %v", planned.RequiresReplace)
						}
						if action := organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned); action != organizationProjectProtocolActionNoOp {
							t.Fatalf("matching imported configuration action=%s, want NoOp", action)
						}
					}
					attributes := protocolAttributeMap(t, schema, planned.PlannedState)
					assertKeyServiceIdentity(t, attributes, test.configured)
					if !attributes["key_wo"].IsNull() || (hashMode && !attributes["key"].IsNull()) {
						t.Fatal("service identity planning persisted the write-only token")
					}
				})
			}
			if requests.Load() != 1 {
				t.Fatal("planning performed an API request")
			}
		})
	}
}

func TestKeyServiceIdentityMetadataObjectContract(t *testing.T) {
	for _, test := range []struct {
		name    string
		raw     interface{}
		present bool
		valid   bool
	}{
		{"null", nil, false, true},
		{"object", map[string]interface{}{"service_account_id": keyServiceIdentityID}, true, true},
		{"string-map", map[string]string{"service_account_id": keyServiceIdentityID}, true, true},
		{"json-string", `{"service_account_id":"synthetic-service-account-a"}`, true, false},
		{"array", []interface{}{keyServiceIdentityID}, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			object, present, valid := keyResponseObject(test.raw)
			if present != test.present || valid != test.valid {
				t.Fatalf("metadata presence/validity=(%t,%t), want (%t,%t)", present, valid, test.present, test.valid)
			}
			if present && valid && object["service_account_id"] != keyServiceIdentityID {
				t.Fatal("metadata object lost its string service identity")
			}
		})
	}
}
