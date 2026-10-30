package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type externalRotationProtocolProvider struct {
	*LiteLLMProvider
	resolver externalSecretResolver
}

func (provider *externalRotationProtocolProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{func() resource.Resource { return &KeyResource{externalSecrets: provider.resolver} }}
}

type externalKeyProtocolFixture struct {
	server      tfprotov6.ProviderServer
	schema      *tfprotov6.Schema
	aws         *fakeExternalSecrets
	mu          sync.Mutex
	info        map[string]interface{}
	mode        string
	reads       int
	updates     int
	ignoreClear string
	afterUpdate func()
	lastUpdate  map[string]interface{}
}

func newExternalKeyProtocolFixture(t *testing.T) *externalKeyProtocolFixture {
	t.Helper()
	fixture := &externalKeyProtocolFixture{aws: &fakeExternalSecrets{token: testExternalRawKey, version: "example-version"}, info: map[string]interface{}{
		"status": "active", "blocked": false, "team_id": "example-team", "project_id": "example-project", "key_alias": "example-alias",
		"metadata":   map[string]interface{}{"service_account_id": "example-service-account", "api_owned": map[string]interface{}{"native": true}},
		"max_budget": 20, "models": []string{"example-model"},
	}}
	gateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if request.Header.Get("Authorization") != "Bearer example-admin-credential" {
			t.Error("existing provider auth not used")
		}
		hash, _ := keyHashFromID(hashKeyForID(fixture.aws.token))
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			fixture.reads++
			if request.URL.Query().Get("key") != hash {
				t.Error("lookup used old/raw key instead of AWSCURRENT hash")
			}
			if fixture.mode == "missing" {
				http.Error(writer, "response-secret "+testExternalRawKey, http.StatusNotFound)
				return
			}
			if fixture.mode == "inaccessible" {
				http.Error(writer, "response-secret "+testExternalRawKey, http.StatusForbidden)
				return
			}
			// Real LiteLLM 1.106.4 removes info.token. Happy path deliberately omits it.
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": hash, "info": fixture.info})
		case request.Method == http.MethodPost && request.URL.Path == "/key/update":
			fixture.updates++
			var payload map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Error("invalid update body")
			}
			if payload["key"] != hash {
				t.Error("update did not use verified hash")
			}
			if _, present := payload["project_id"]; present {
				t.Error("external update attempted project reassignment")
			}
			if fixture.mode == "update-failure" {
				http.Error(writer, "mutation-secret "+testExternalRawKey, http.StatusInternalServerError)
				return
			}
			fixture.lastUpdate = payload
			for name, value := range payload {
				if name != "key" && name != fixture.ignoreClear {
					fixture.info[name] = value
				}
			}
			if fixture.afterUpdate != nil {
				fixture.afterUpdate()
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": hash})
		default:
			t.Error("external mode called generate/delete/unexpected endpoint")
			http.Error(writer, "unexpected route", http.StatusBadRequest)
		}
	}))
	t.Cleanup(gateway.Close)
	fixture.server = providerserver.NewProtocol6(&externalRotationProtocolProvider{LiteLLMProvider: &LiteLLMProvider{version: "test"}, resolver: fixture.aws})()
	schemas, err := fixture.server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemas.Diagnostics) {
		t.Fatalf("schema: %v", err)
	}
	fixture.schema = schemas.ResourceSchemas["litellm_key"]
	configuration, err := tftypes.ValueFromJSON([]byte(`{"api_base":"`+gateway.URL+`","api_key":"example-admin-credential","insecure_skip_verify":null,"litellm_changed_by":null}`), schemas.Provider.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	configured, err := fixture.server.ConfigureProvider(context.Background(), &tfprotov6.ConfigureProviderRequest{Config: accessGroupProtocolDynamicValue(t, schemas.Provider, configuration)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configured.Diagnostics) {
		t.Fatal("provider configuration failed")
	}
	return fixture
}

func (fixture *externalKeyProtocolFixture) config(t *testing.T, overrides map[string]tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	values := map[string]tftypes.Value{
		"external_rotation":  tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"secret_arn": tftypes.String, "version_hash": tftypes.String}}, map[string]tftypes.Value{"secret_arn": tftypes.NewValue(tftypes.String, testExternalSecretARN), "version_hash": tftypes.NewValue(tftypes.String, nil)}),
		"service_account_id": tftypes.NewValue(tftypes.String, "example-service-account"),
		"team_id":            tftypes.NewValue(tftypes.String, "example-team"), "project_id": tftypes.NewValue(tftypes.String, "example-project"), "key_alias": tftypes.NewValue(tftypes.String, "example-alias"),
		"max_budget": tftypes.NewValue(tftypes.Number, 20),
		"models":     tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{tftypes.NewValue(tftypes.String, "example-model")}),
	}
	for name, value := range overrides {
		values[name] = value
	}
	return keyNumericProtocolDynamic(t, fixture.schema, keyNumericProtocolValue(t, fixture.schema, false, values))
}

func (fixture *externalKeyProtocolFixture) plan(t *testing.T, prior, config *tfprotov6.DynamicValue, private []byte) *tfprotov6.PlanResourceChangeResponse {
	t.Helper()
	if prior == nil {
		prior = keyNumericProtocolDynamic(t, fixture.schema, tftypes.NewValue(fixture.schema.ValueType(), nil))
	}
	proposed, err := config.Unmarshal(fixture.schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	var attributes map[string]tftypes.Value
	if err := proposed.As(&attributes); err != nil {
		t.Fatal(err)
	}
	for _, attribute := range fixture.schema.Block.Attributes {
		if attribute.Computed && attributes[attribute.Name].IsNull() {
			attributes[attribute.Name] = tftypes.NewValue(attribute.ValueType(), tftypes.UnknownValue)
		}
	}
	planned, err := fixture.server.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_key", Config: config, PriorState: prior, ProposedNewState: keyNumericProtocolDynamic(t, fixture.schema, tftypes.NewValue(fixture.schema.ValueType(), attributes)), PriorPrivate: private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		for _, diagnostic := range planned.Diagnostics {
			t.Logf("%s: %s", diagnostic.Summary, diagnostic.Detail)
		}
		t.Fatalf("plan failed: %v", err)
	}
	if len(planned.RequiresReplace) != 0 {
		t.Fatal("external enrollment requested replacement")
	}
	return planned
}

func (fixture *externalKeyProtocolFixture) apply(t *testing.T, prior, config *tfprotov6.DynamicValue, plan *tfprotov6.PlanResourceChangeResponse, wantError bool) *tfprotov6.ApplyResourceChangeResponse {
	t.Helper()
	if prior == nil {
		prior = keyNumericProtocolDynamic(t, fixture.schema, tftypes.NewValue(fixture.schema.ValueType(), nil))
	}
	applied, err := fixture.server.ApplyResourceChange(context.Background(), &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_key", Config: config, PriorState: prior, PlannedState: plan.PlannedState, PlannedPrivate: plan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) != wantError {
		for _, diagnostic := range applied.Diagnostics {
			t.Logf("%s: %s", diagnostic.Summary, diagnostic.Detail)
		}
		t.Fatalf("apply failed unexpectedly: %v", err)
	}
	return applied
}

func externalProtocolString(t *testing.T, fixture *externalKeyProtocolFixture, state *tfprotov6.DynamicValue, name string) string {
	t.Helper()
	value, err := state.Unmarshal(fixture.schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]tftypes.Value
	if err := value.As(&fields); err != nil {
		t.Fatal(err)
	}
	var result string
	if err := fields[name].As(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestExternalKeyProtocolAdoptionRotationUpdateForget(t *testing.T) {
	fixture := newExternalKeyProtocolFixture(t)
	config := fixture.config(t, nil)
	plan := fixture.plan(t, nil, config, nil)
	plannedValue, err := plan.PlannedState.Unmarshal(fixture.schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	var plannedFields map[string]tftypes.Value
	if err := plannedValue.As(&plannedFields); err != nil || plannedFields["key"].IsKnown() || plannedFields["id"].IsKnown() {
		t.Fatal("missing-state plan must leave credential identity computed until adoption")
	}
	created := fixture.apply(t, nil, config, plan, false)
	if externalProtocolString(t, fixture, created.NewState, "key") != testExternalRawKey || externalProtocolString(t, fixture, created.NewState, "id") != hashKeyForID(testExternalRawKey) {
		t.Fatal("adoption did not resolve coherent credential identity")
	}
	if fixture.updates != 0 || fixture.reads != 2 {
		t.Fatal("matching adoption was not read-only")
	}
	fixture.aws.token = "sk-rotatedabcdefghijklmnopqrstuvwxyz"
	fixture.aws.version = "rotated-version"
	read, err := fixture.server.ReadResource(context.Background(), &tfprotov6.ReadResourceRequest{TypeName: "litellm_key", CurrentState: created.NewState, Private: created.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("rotation refresh failed: %v %v", err, read.Diagnostics)
	}
	if externalProtocolString(t, fixture, read.NewState, "key") != fixture.aws.token || externalProtocolString(t, fixture, read.NewState, "id") != hashKeyForID(fixture.aws.token) {
		t.Fatal("rotation did not change coherent token and hash")
	}
	config = fixture.config(t, map[string]tftypes.Value{"max_budget": tftypes.NewValue(tftypes.Number, 30), "metadata": tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, map[string]tftypes.Value{"owner": tftypes.NewValue(tftypes.String, "example-owner")})})
	plan = fixture.plan(t, read.NewState, config, read.Private)
	updated := fixture.apply(t, read.NewState, config, plan, false)
	if fixture.updates != 1 {
		t.Fatal("explicit drift not updated")
	}
	metadata := fixture.info["metadata"].(map[string]interface{})
	if metadata["service_account_id"] != "example-service-account" || metadata["api_owned"] == nil {
		t.Fatal("metadata identity/native siblings lost")
	}
	assertProtocolFloat64(t, updated.NewState, fixture.schema, "max_budget", 30)
	fixture.aws.err = errorsForExternalTest("AWS unavailable on destroy")
	null := keyNumericProtocolDynamic(t, fixture.schema, tftypes.NewValue(fixture.schema.ValueType(), nil))
	deleted, err := fixture.server.ApplyResourceChange(context.Background(), &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_key", Config: null, PriorState: updated.NewState, PlannedState: null, PlannedPrivate: updated.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(deleted.Diagnostics) {
		t.Fatalf("forget failed: %v %v", err, deleted.Diagnostics)
	}
	value, _ := deleted.NewState.Unmarshal(fixture.schema.ValueType())
	if !value.IsNull() || fixture.updates != 1 {
		t.Fatal("destroy did not forget only")
	}
}

func TestExternalKeyProtocolUncertainAdoptionRetainsExternalOwnership(t *testing.T) {
	for _, enrollment := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing state", true: "enrollment"}[enrollment], func(t *testing.T) {
			fixture := newExternalKeyProtocolFixture(t)
			config := fixture.config(t, map[string]tftypes.Value{"max_budget": tftypes.NewValue(tftypes.Number, 25)})
			var prior *tfprotov6.DynamicValue
			if enrollment {
				prior = fixture.config(t, map[string]tftypes.Value{"external_rotation": tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"secret_arn": tftypes.String, "version_hash": tftypes.String}}, nil), "id": tftypes.NewValue(tftypes.String, hashKeyForID(testExternalRawKey)), "key": tftypes.NewValue(tftypes.String, testExternalRawKey), "service_account_id": tftypes.NewValue(tftypes.String, nil)})
			}
			plan := fixture.plan(t, prior, config, nil)
			fixture.mode = "update-failure"
			applied := fixture.apply(t, prior, config, plan, true)
			value, err := applied.NewState.Unmarshal(fixture.schema.ValueType())
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]tftypes.Value
			if err := value.As(&fields); err != nil {
				t.Fatal(err)
			}
			if fields["external_rotation"].IsNull() {
				t.Fatal("uncertain adoption restored destructive ownership")
			}
			if externalProtocolString(t, fixture, applied.NewState, "id") != hashKeyForID(testExternalRawKey) {
				t.Fatal("verified binding lost on uncertain adoption")
			}
			fixture.aws.err = errorsForExternalTest("AWS unavailable on forget")
			null := keyNumericProtocolDynamic(t, fixture.schema, tftypes.NewValue(fixture.schema.ValueType(), nil))
			forgotten, err := fixture.server.ApplyResourceChange(context.Background(), &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_key", Config: null, PriorState: applied.NewState, PlannedState: null, PlannedPrivate: applied.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(forgotten.Diagnostics) {
				t.Fatal("uncertain adopted key could not be forgotten safely")
			}
		})
	}
}

func errorsForExternalTest(message string) error { return &externalFixtureError{message} }

type externalFixtureError struct{ message string }

func (err *externalFixtureError) Error() string { return err.message }

func TestExternalKeyProtocolInitialDriftAndEnrollment(t *testing.T) {
	for _, enrollment := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing state drift", true: "enrollment"}[enrollment], func(t *testing.T) {
			fixture := newExternalKeyProtocolFixture(t)
			config := fixture.config(t, map[string]tftypes.Value{"max_budget": tftypes.NewValue(tftypes.Number, 25), "duration": tftypes.NewValue(tftypes.String, "30d")})
			var prior *tfprotov6.DynamicValue
			if enrollment {
				prior = fixture.config(t, map[string]tftypes.Value{"external_rotation": tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"secret_arn": tftypes.String, "version_hash": tftypes.String}}, nil), "id": tftypes.NewValue(tftypes.String, hashKeyForID(testExternalRawKey)), "key": tftypes.NewValue(tftypes.String, testExternalRawKey), "service_account_id": tftypes.NewValue(tftypes.String, nil), "project_id": tftypes.NewValue(tftypes.String, nil)})
			}
			plan := fixture.plan(t, prior, config, nil)
			applied := fixture.apply(t, prior, config, plan, false)
			if fixture.updates != 1 || externalProtocolString(t, fixture, applied.NewState, "id") != hashKeyForID(testExternalRawKey) {
				t.Fatal("adoption drift/enrollment not safely reconciled")
			}
		})
	}
}

func TestExternalKeyProtocolReadFailuresRetainStateAndSanitize(t *testing.T) {
	for _, mode := range []string{"missing", "inaccessible", "wrong team", "wrong project", "wrong metadata", "missing metadata", "missing blocked", "blocked", "deleted", "expired", "revoked", "wrong hash", "AWS missing", "AWS missing version"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newExternalKeyProtocolFixture(t)
			config := fixture.config(t, nil)
			created := fixture.apply(t, nil, config, fixture.plan(t, nil, config, nil), false)
			switch mode {
			case "missing", "inaccessible":
				fixture.mode = mode
			case "wrong team":
				fixture.info["team_id"] = "response-secret"
			case "wrong project":
				fixture.info["project_id"] = "response-secret"
			case "wrong metadata":
				fixture.info["metadata"] = map[string]interface{}{"service_account_id": "response-secret"}
			case "missing metadata":
				delete(fixture.info, "metadata")
			case "missing blocked":
				delete(fixture.info, "blocked")
			case "blocked":
				fixture.info["blocked"] = true
			case "deleted", "expired", "revoked":
				fixture.info["status"] = mode
			case "wrong hash":
				fixture.info["token"] = "response-secret"
			case "AWS missing":
				fixture.aws.err = errorsForExternalTest("credential-secret " + testExternalRawKey)
			case "AWS missing version":
				fixture.aws.version = ""
			}
			read, err := fixture.server.ReadResource(context.Background(), &tfprotov6.ReadResourceRequest{TypeName: "litellm_key", CurrentState: created.NewState, Private: created.Private})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatal("invalid external read did not fail closed")
			}
			assertKeyRawStateUnchanged(t, created.NewState, read.NewState)
			for _, diagnostic := range read.Diagnostics {
				if strings.Contains(diagnostic.Detail, testExternalRawKey) || strings.Contains(diagnostic.Detail, "response-secret") || strings.Contains(diagnostic.Detail, "credential-secret") {
					t.Fatal("credential leaked in diagnostic")
				}
			}
		})
	}
}

func externalProtocolClearConfig(t *testing.T, fixture *externalKeyProtocolFixture, field string) *tfprotov6.DynamicValue {
	t.Helper()
	overrides := map[string]tftypes.Value{}
	if field == "tpd_limit" || field == "both" {
		overrides["tpd_limit"] = tftypes.NewValue(tftypes.Number, 500)
	}
	if field == "router_settings" || field == "both" {
		for _, attribute := range fixture.schema.Block.Attributes {
			if attribute.Name == "router_settings" {
				objectType := attribute.ValueType().(tftypes.Object)
				values := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
				for name, attributeType := range objectType.AttributeTypes {
					values[name] = tftypes.NewValue(attributeType, nil)
				}
				values["num_retries"] = tftypes.NewValue(tftypes.Number, 3)
				overrides["router_settings"] = tftypes.NewValue(objectType, values)
			}
		}
	}
	return fixture.config(t, overrides)
}

func TestExternalKeyProtocolClearReadback(t *testing.T) {
	for _, field := range []string{"tpd_limit", "router_settings", "both"} {
		for _, mode := range []string{"success", "ignored", "token race after update", "version race after update", "identity changed after update"} {
			t.Run(field+"/"+mode, func(t *testing.T) {
				fixture := newExternalKeyProtocolFixture(t)
				ownedConfig := externalProtocolClearConfig(t, fixture, field)
				created := fixture.apply(t, nil, ownedConfig, fixture.plan(t, nil, ownedConfig, nil), false)
				config := fixture.config(t, nil)
				plan := fixture.plan(t, created.NewState, config, created.Private)
				fixture.mu.Lock()
				switch mode {
				case "ignored":
					fixture.ignoreClear = field
					if field == "both" {
						fixture.ignoreClear = "router_settings"
					}
				case "token race after update":
					fixture.afterUpdate = func() { fixture.aws.token = "sk-rotatedabcdefghijklmnopqrstuvwxyz" }
				case "version race after update":
					fixture.afterUpdate = func() { fixture.aws.version = "changed-version" }
				case "identity changed after update":
					fixture.afterUpdate = func() { fixture.info["team_id"] = "response-secret" }
				}
				reads, updates, secrets := fixture.reads, fixture.updates, fixture.aws.calls
				fixture.mu.Unlock()
				applied := fixture.apply(t, created.NewState, config, plan, mode != "success")
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
				if fixture.updates-updates != 1 || fixture.reads-reads != 2 || fixture.aws.calls-secrets != 2 {
					t.Fatal("clear did not use one update and one bounded fresh confirmation")
				}
				hash, _ := keyHashFromID(hashKeyForID(testExternalRawKey))
				wanted := map[string]interface{}{"key": hash, "models": []interface{}{"example-model"}, "max_budget": float64(20)}
				if field == "tpd_limit" || field == "both" {
					wanted["tpd_limit"] = nil
				}
				if field == "router_settings" || field == "both" {
					wanted["router_settings"] = map[string]interface{}{}
				}
				if !reflect.DeepEqual(fixture.lastUpdate, wanted) {
					t.Fatalf("clear changed update payload: %#v", fixture.lastUpdate)
				}
				if mode == "success" {
					assertKeyRawStateUnchanged(t, plan.PlannedState, applied.NewState)
				} else {
					assertKeyRawStateUnchanged(t, created.NewState, applied.NewState)
				}
				for _, diagnostic := range applied.Diagnostics {
					if strings.Contains(diagnostic.Detail, testExternalRawKey) || strings.Contains(diagnostic.Detail, "response-secret") {
						t.Fatal("clear response leaked")
					}
				}
			})
		}
	}
}

func TestExternalKeyProtocolClearPlanApplyRaces(t *testing.T) {
	for _, field := range []string{"tpd_limit", "router_settings", "both"} {
		for _, race := range []string{"token", "version"} {
			t.Run(field+"/"+race, func(t *testing.T) {
				fixture := newExternalKeyProtocolFixture(t)
				ownedConfig := externalProtocolClearConfig(t, fixture, field)
				created := fixture.apply(t, nil, ownedConfig, fixture.plan(t, nil, ownedConfig, nil), false)
				config := fixture.config(t, nil)
				plan := fixture.plan(t, created.NewState, config, created.Private)
				fixture.mu.Lock()
				if race == "token" {
					fixture.aws.token = "sk-rotatedabcdefghijklmnopqrstuvwxyz"
				} else {
					fixture.aws.version = "changed-version"
				}
				updates := fixture.updates
				fixture.mu.Unlock()
				applied := fixture.apply(t, created.NewState, config, plan, true)
				assertKeyRawStateUnchanged(t, created.NewState, applied.NewState)
				fixture.mu.Lock()
				defer fixture.mu.Unlock()
				if fixture.updates != updates {
					t.Fatal("plan/apply race sent a clear mutation")
				}
			})
		}
	}
}

func TestExternalKeyProtocolSavedPlanReExpansion(t *testing.T) {
	for _, race := range []string{"unchanged", "token", "version"} {
		t.Run(race, func(t *testing.T) {
			fixture := newExternalKeyProtocolFixture(t)
			config := fixture.config(t, nil)
			created := fixture.apply(t, nil, config, fixture.plan(t, nil, config, nil), false)
			config = fixture.config(t, map[string]tftypes.Value{"max_budget": tftypes.NewValue(tftypes.Number, 30)})
			planned := fixture.plan(t, created.NewState, config, created.Private)
			fixture.mu.Lock()
			switch race {
			case "token":
				fixture.aws.token = "sk-rotatedabcdefghijklmnopqrstuvwxyz"
			case "version":
				fixture.aws.version = "changed-version"
			}
			updates := fixture.updates
			fixture.mu.Unlock()
			// Saved-plan re-expansion uses the original state private data, not
			// planned private data. Preserve known planned identity and let Apply
			// refuse a race instead of returning an inconsistent final plan.
			response, err := fixture.server.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{
				TypeName: "litellm_key", Config: config, PriorState: created.NewState,
				ProposedNewState: planned.PlannedState, PriorPrivate: created.Private,
			})
			if err != nil {
				t.Fatal("saved plan re-expansion protocol failed")
			}
			if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
				t.Fatal("saved plan re-expansion failed before apply identity validation")
			}
			assertKeyRawStateUnchanged(t, planned.PlannedState, response.PlannedState)
			if race != "unchanged" {
				applied := fixture.apply(t, created.NewState, config, response, true)
				assertKeyRawStateUnchanged(t, created.NewState, applied.NewState)
				found := false
				for _, diagnostic := range applied.Diagnostics {
					found = found || strings.Contains(diagnostic.Detail, "changed after planning")
					if strings.Contains(diagnostic.Detail, testExternalRawKey) || strings.Contains(diagnostic.Detail, fixture.aws.token) {
						t.Fatal("apply diagnostic exposed a token")
					}
				}
				if !found {
					t.Fatal("missing actionable replan diagnostic")
				}
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.updates != updates {
				t.Fatal("plan re-expansion mutated the gateway")
			}
		})
	}
}

func TestExternalKeyProtocolRaceAndUncertainUpdate(t *testing.T) {
	for _, mode := range []string{"token race", "version race", "uncertain update", "mode removal"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newExternalKeyProtocolFixture(t)
			config := fixture.config(t, nil)
			created := fixture.apply(t, nil, config, fixture.plan(t, nil, config, nil), false)
			config = fixture.config(t, map[string]tftypes.Value{"max_budget": tftypes.NewValue(tftypes.Number, 30)})
			plan := fixture.plan(t, created.NewState, config, created.Private)
			switch mode {
			case "token race":
				fixture.aws.token = "sk-rotatedabcdefghijklmnopqrstuvwxyz"
			case "version race":
				fixture.aws.version = "changed-version"
			case "uncertain update":
				fixture.mode = "update-failure"
			case "mode removal":
				config = fixture.config(t, map[string]tftypes.Value{"external_rotation": tftypes.NewValue(tftypes.Object{AttributeTypes: map[string]tftypes.Type{"secret_arn": tftypes.String, "version_hash": tftypes.String}}, nil)})
				response, err := fixture.server.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_key", Config: config, PriorState: created.NewState, ProposedNewState: config, PriorPrivate: created.Private})
				if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || fixture.updates != 0 {
					t.Fatal("mode removal accepted")
				}
				return
			}
			applied := fixture.apply(t, created.NewState, config, plan, true)
			assertKeyRawStateUnchanged(t, created.NewState, applied.NewState)
			if mode != "uncertain update" && fixture.updates != 0 {
				t.Fatal("race sent mutation")
			}
		})
	}
}
