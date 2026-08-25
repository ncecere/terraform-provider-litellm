package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func credentialTestSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&CredentialResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func credentialTestModel(name string, info, values map[string]string) CredentialResourceModel {
	model := CredentialResourceModel{
		ID:                     types.StringValue(name),
		CredentialName:         types.StringValue(name),
		ModelID:                types.StringNull(),
		CredentialInfo:         types.MapNull(types.StringType),
		CredentialValues:       types.MapNull(types.StringType),
		CredentialInfoJSON:     types.StringNull(),
		CredentialValuesJSON:   types.StringNull(),
		CredentialValuesActive: types.BoolValue(true),
		CredentialSource:       types.StringValue("credential_values"),
	}
	if info != nil {
		model.CredentialInfo = stringMapValue(info)
	}
	if values != nil {
		model.CredentialValues = stringMapValue(values)
	}
	return model
}

func credentialTestState(t *testing.T, schema resourceschema.Schema, model CredentialResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := state.Set(context.Background(), &model); diagnostics.HasError() {
		t.Fatalf("set state: %v", diagnostics)
	}
	return state
}

func credentialTestPlan(t *testing.T, schema resourceschema.Schema, model CredentialResourceModel) tfsdk.Plan {
	t.Helper()
	state := credentialTestState(t, schema, model)
	return tfsdk.Plan{Raw: state.Raw, Schema: schema}
}

func credentialTestConfig(t *testing.T, schema resourceschema.Schema, model CredentialResourceModel) tfsdk.Config {
	t.Helper()
	state := credentialTestState(t, schema, model)
	return tfsdk.Config{Raw: state.Raw, Schema: schema}
}

func credentialProtocolDynamicValue(t *testing.T, schemaType tftypes.Type, value tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	dynamic, err := tfprotov6.NewDynamicValue(schemaType, value)
	if err != nil {
		t.Fatalf("build credential protocol dynamic value: %v", err)
	}
	return &dynamic
}

func credentialProtocolMap(value interface{}) tftypes.Value {
	return tftypes.NewValue(tftypes.Map{ElementType: tftypes.String}, value)
}

func credentialProtocolStringMap(values map[string]string) tftypes.Value {
	elements := make(map[string]tftypes.Value, len(values))
	for key, value := range values {
		elements[key] = tftypes.NewValue(tftypes.String, value)
	}
	return credentialProtocolMap(elements)
}

func credentialProtocolValue(resourceType tftypes.Type, name interface{}, id interface{}, modelID interface{}, info, values tftypes.Value, infoJSON, valuesJSON, active, source interface{}) tftypes.Value {
	return tftypes.NewValue(resourceType, map[string]tftypes.Value{
		"id":                       tftypes.NewValue(tftypes.String, id),
		"credential_name":          tftypes.NewValue(tftypes.String, name),
		"model_id":                 tftypes.NewValue(tftypes.String, modelID),
		"credential_info":          info,
		"credential_values":        values,
		"credential_info_json":     tftypes.NewValue(tftypes.String, infoJSON),
		"credential_values_json":   tftypes.NewValue(tftypes.String, valuesJSON),
		"credential_values_active": tftypes.NewValue(tftypes.Bool, active),
		"credential_source":        tftypes.NewValue(tftypes.String, source),
	})
}

func credentialProtocolReplace(t *testing.T, schemaType tftypes.Type, dynamic *tfprotov6.DynamicValue, replacements map[string]tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	value, err := dynamic.Unmarshal(schemaType)
	if err != nil {
		t.Fatalf("unmarshal credential protocol value: %v", err)
	}
	var attributes map[string]tftypes.Value
	if err := value.As(&attributes); err != nil {
		t.Fatalf("decode credential protocol attributes: %v", err)
	}
	for name, replacement := range replacements {
		attributes[name] = replacement
	}
	return credentialProtocolDynamicValue(t, schemaType, tftypes.NewValue(schemaType, attributes))
}

func credentialProtocolServer(t *testing.T, apiBase string) (tfprotov6.ProviderServer, tftypes.Type) {
	t.Helper()
	ctx := context.Background()
	server := providerserver.NewProtocol6(New("test")())()
	schemaResponse, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemaResponse.Diagnostics) {
		t.Fatalf("get credential protocol schema: err=%v diagnostics=%v", err, schemaResponse.Diagnostics)
	}
	providerType := schemaResponse.Provider.ValueType()
	providerConfig := tftypes.NewValue(providerType, map[string]tftypes.Value{
		"api_base":             tftypes.NewValue(tftypes.String, apiBase),
		"api_key":              tftypes.NewValue(tftypes.String, "admin"),
		"insecure_skip_verify": tftypes.NewValue(tftypes.Bool, false),
		"litellm_changed_by":   tftypes.NewValue(tftypes.String, nil),
	})
	configureResponse, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		TerraformVersion: "test",
		Config:           credentialProtocolDynamicValue(t, providerType, providerConfig),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configureResponse.Diagnostics) {
		t.Fatalf("configure credential protocol server: err=%v diagnostics=%v", err, configureResponse.Diagnostics)
	}
	resourceSchema := schemaResponse.ResourceSchemas["litellm_credential"]
	if resourceSchema == nil {
		t.Fatal("credential protocol schema is missing")
	}
	return server, resourceSchema.ValueType()
}

func TestCredentialSchemaPreservesLegacyTypesAndAddsJSON(t *testing.T) {
	t.Parallel()
	schema := credentialTestSchema(t)
	if schema.Version != 0 {
		t.Fatalf("schema version = %d, want unchanged version 0", schema.Version)
	}
	info, ok := schema.Attributes["credential_info"].(resourceschema.MapAttribute)
	if !ok || !info.Optional || !info.Computed || info.ElementType != types.StringType {
		t.Fatalf("credential_info changed public contract: %#v", schema.Attributes["credential_info"])
	}
	values, ok := schema.Attributes["credential_values"].(resourceschema.MapAttribute)
	if !ok || values.Required || !values.Optional || values.Computed || !values.Sensitive || values.ElementType != types.StringType {
		t.Fatalf("credential_values did not preserve map(string)/sensitive while becoming optional: %#v", schema.Attributes["credential_values"])
	}
	for _, name := range []string{"credential_info_json", "credential_values_json"} {
		attribute, ok := schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || !attribute.Optional || !attribute.Computed {
			t.Fatalf("%s is not additive optional/computed JSON: %#v", name, schema.Attributes[name])
		}
	}
	modelID := schema.Attributes["model_id"].(resourceschema.StringAttribute)
	if len(modelID.PlanModifiers) == 0 {
		t.Fatal("model_id lost its unconditional create-only replacement modifier")
	}
}

func TestCredentialJSONValidationCanonicalizationAndExactNumbers(t *testing.T) {
	t.Parallel()
	input := `{ "nested": {"large":9007199254740993123456789}, "enabled": true, "items": [1.25, null] }`
	object, err := decodeCredentialJSONObjectString(input)
	if err != nil {
		t.Fatalf("decode object: %v", err)
	}
	large := object["nested"].(map[string]interface{})["large"]
	if number, ok := large.(json.Number); !ok || number.String() != "9007199254740993123456789" {
		t.Fatalf("large number rounded or changed: %#v", large)
	}
	canonical, err := canonicalCredentialJSON(object)
	if err != nil {
		t.Fatalf("canonical JSON: %v", err)
	}
	if canonical != `{"enabled":true,"items":[1.25,null],"nested":{"large":9007199254740993123456789}}` {
		t.Fatalf("canonical JSON = %s", canonical)
	}
	for _, invalid := range []string{`[]`, `null`, `{"a":1} trailing`, ``} {
		if _, err := decodeCredentialJSONObjectString(invalid); err == nil {
			t.Errorf("invalid JSON object accepted: %q", invalid)
		}
	}
}

func TestCredentialLegacyAndJSONMergeRequiresEqualOverlap(t *testing.T) {
	t.Parallel()
	legacy := stringMapValue(map[string]string{"same": "value", "legacy": "only"})
	merged, err := buildCredentialConfiguredObject(context.Background(), legacy, types.StringValue(`{"same":"value","nested":{"enabled":true}}`))
	if err != nil {
		t.Fatalf("merge equal overlap: %v", err)
	}
	if len(merged.Object) != 3 || merged.Object["same"] != "value" {
		t.Fatalf("merged object = %#v", merged.Object)
	}
	if _, err := buildCredentialConfiguredObject(context.Background(), legacy, types.StringValue(`{"same":false}`)); err == nil {
		t.Fatal("different overlapping values were accepted")
	}
}

func TestCredentialCreateRequestAlternativesAndModelDominance(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		values     types.Map
		valuesJSON types.String
		modelID    types.String
		wantValues bool
		wantModel  bool
		wantError  bool
	}{
		{"omitted values", types.MapNull(types.StringType), types.StringNull(), types.StringNull(), false, false, true},
		{"empty legacy values", stringMapValue(map[string]string{}), types.StringNull(), types.StringNull(), false, false, true},
		{"empty JSON values", types.MapNull(types.StringType), types.StringValue(`{}`), types.StringNull(), false, false, true},
		{"nonempty JSON values", types.MapNull(types.StringType), types.StringValue(`{"oauth":{"enabled":true}}`), types.StringNull(), true, false, false},
		{"model only", types.MapNull(types.StringType), types.StringNull(), types.StringValue("provider/model/with/slash"), false, true, false},
		{"model with empty values", stringMapValue(map[string]string{}), types.StringNull(), types.StringValue("model-1"), true, true, false},
		{"model with nonempty values", stringMapValue(map[string]string{"api_key": "inactive"}), types.StringNull(), types.StringValue("model-1"), true, true, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			model := credentialTestModel("create", nil, nil)
			model.CredentialValues = test.values
			model.CredentialValuesJSON = test.valuesJSON
			model.ModelID = test.modelID
			request, _, _, err := buildCredentialCreateRequest(context.Background(), model)
			if test.wantError {
				if err == nil {
					t.Fatalf("build create accepted an empty values-only source: %#v", request)
				}
				return
			}
			if err != nil {
				t.Fatalf("build create: %v", err)
			}
			_, gotValues := request["credential_values"]
			_, gotModel := request["model_id"]
			if gotValues != test.wantValues || gotModel != test.wantModel {
				t.Fatalf("request = %#v", request)
			}
		})
	}
}

func TestCredentialCreateRejectsUnknownApplySourceBeforeAnyRequest(t *testing.T) {
	t.Parallel()
	requests := 0
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer api.Close()

	schema := credentialTestSchema(t)
	planned := credentialTestModel("unknown-apply-source", nil, map[string]string{"api_key": "secret"})
	planned.ID = types.StringUnknown()
	plan := credentialTestPlan(t, schema, planned)
	applyConfig := planned
	applyConfig.ModelID = types.StringUnknown()
	config := credentialTestConfig(t, schema, applyConfig)
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&CredentialResource{client: &Client{APIBase: api.URL, APIKey: "admin", HTTPClient: api.Client()}}).Create(
		context.Background(), resource.CreateRequest{Config: config, Plan: plan}, response,
	)
	if !response.Diagnostics.HasError() {
		t.Fatal("unknown apply-time model_id was treated as omitted")
	}
	if requests != 0 {
		t.Fatalf("unknown apply-time source reached preflight or POST: requests=%d", requests)
	}
}

func TestCredentialProtocolCreateSourcePlanningMatrix(t *testing.T) {
	t.Parallel()
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		t.Errorf("planning unexpectedly called LiteLLM: %s %s", request.Method, request.RequestURI)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer api.Close()
	server, resourceType := credentialProtocolServer(t, api.URL)
	nullMap := credentialProtocolMap(nil)
	unknownMap := credentialProtocolMap(tftypes.UnknownValue)
	nullPrior := credentialProtocolDynamicValue(t, resourceType, tftypes.NewValue(resourceType, nil))

	cases := []struct {
		name       string
		modelID    interface{}
		values     tftypes.Value
		valuesJSON interface{}
		wantError  bool
	}{
		{name: "all omitted", modelID: nil, values: nullMap, valuesJSON: nil, wantError: true},
		{name: "empty legacy only", modelID: nil, values: credentialProtocolStringMap(map[string]string{}), valuesJSON: nil, wantError: true},
		{name: "empty JSON only", modelID: nil, values: nullMap, valuesJSON: `{}`, wantError: true},
		{name: "empty merged legacy and JSON", modelID: nil, values: credentialProtocolStringMap(map[string]string{}), valuesJSON: `{}`, wantError: true},
		{name: "nonempty legacy", modelID: nil, values: credentialProtocolStringMap(map[string]string{"api_key": "secret"}), valuesJSON: nil},
		{name: "nonempty JSON", modelID: nil, values: nullMap, valuesJSON: `{"oauth":{"enabled":true}}`},
		{name: "model only", modelID: "model/source", values: nullMap, valuesJSON: nil},
		{name: "model plus empty", modelID: "model/source", values: credentialProtocolStringMap(map[string]string{}), valuesJSON: `{}`},
		{name: "model plus nonempty", modelID: "model/source", values: credentialProtocolStringMap(map[string]string{"api_key": "inactive"}), valuesJSON: nil},
		{name: "unknown model defers", modelID: tftypes.UnknownValue, values: nullMap, valuesJSON: nil},
		{name: "unknown legacy defers", modelID: nil, values: unknownMap, valuesJSON: nil},
		{name: "unknown JSON defers", modelID: nil, values: nullMap, valuesJSON: tftypes.UnknownValue},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			configValue := credentialProtocolValue(resourceType, "source-matrix", nil, test.modelID, nullMap, test.values, nil, test.valuesJSON, nil, nil)
			proposedValue := credentialProtocolValue(resourceType, "source-matrix", tftypes.UnknownValue, test.modelID, unknownMap, test.values, tftypes.UnknownValue, test.valuesJSON, tftypes.UnknownValue, tftypes.UnknownValue)
			response, err := server.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{
				TypeName:         "litellm_credential",
				Config:           credentialProtocolDynamicValue(t, resourceType, configValue),
				PriorState:       nullPrior,
				ProposedNewState: credentialProtocolDynamicValue(t, resourceType, proposedValue),
			})
			if err != nil {
				t.Fatalf("plan create source: %v", err)
			}
			if gotError := accessGroupProtocolDiagnosticsHaveError(response.Diagnostics); gotError != test.wantError {
				t.Fatalf("plan diagnostics error=%t want=%t diagnostics=%v", gotError, test.wantError, response.Diagnostics)
			}
		})
	}
}

func TestCredentialProtocolCreateFinalStateUsesResolvedApplyConfig(t *testing.T) {
	// Each case exercises a stateful exact-name route and an immediate retry.
	for _, test := range []struct {
		name      string
		ambiguous bool
	}{
		{name: "successful postflight"},
		{name: "accepted create with lost response", ambiguous: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			credentialName := "resolved-apply-success"
			if test.ambiguous {
				credentialName = "resolved-apply-ambiguous"
			}
			created := false
			gets := 0
			posts := 0
			collisions := 0
			var requests []string
			api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				requests = append(requests, request.Method+" "+request.RequestURI)
				switch request.Method {
				case http.MethodGet:
					gets++
					if !created {
						http.NotFound(writer, request)
						return
					}
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{
						"credential_name": credentialName,
						"credential_info": map[string]interface{}{
							"owner":  "caller",
							"remote": "authoritative",
						},
						"credential_values": map[string]interface{}{"api_key": "in****et"},
					})
				case http.MethodPost:
					posts++
					if created {
						collisions++
						http.Error(writer, `{"detail":"duplicate"}`, http.StatusConflict)
						return
					}
					var body map[string]interface{}
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Errorf("decode POST: %v", err)
					}
					info, _ := body["credential_info"].(map[string]interface{})
					values, _ := body["credential_values"].(map[string]interface{})
					if body["credential_name"] != credentialName || body["model_id"] != "resolved/model" ||
						info["owner"] != "caller" || values["api_key"] != "inactive-secret" {
						t.Errorf("POST did not use resolved apply config: %#v", body)
					}
					created = true
					if test.ambiguous {
						// LiteLLM accepted and committed the request, but the caller
						// cannot consume the response. This is a dispatched/lost-
						// response outcome even though the status is 2xx.
						_, _ = writer.Write([]byte(`{"success":`))
						return
					}
					_, _ = writer.Write([]byte(`{"success":true,"message":"created"}`))
				default:
					http.NotFound(writer, request)
				}
			}))
			defer api.Close()

			server, resourceType := credentialProtocolServer(t, api.URL)
			ctx := context.Background()
			legacyInfo := credentialProtocolStringMap(map[string]string{"owner": "caller"})
			legacyValues := credentialProtocolStringMap(map[string]string{"api_key": "inactive-secret"})
			nullState := credentialProtocolDynamicValue(t, resourceType, tftypes.NewValue(resourceType, nil))
			planningConfig := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(
				resourceType,
				credentialName,
				nil,
				tftypes.UnknownValue,
				legacyInfo,
				legacyValues,
				nil,
				nil,
				nil,
				nil,
			))
			proposed := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(
				resourceType,
				credentialName,
				tftypes.UnknownValue,
				tftypes.UnknownValue,
				legacyInfo,
				legacyValues,
				tftypes.UnknownValue,
				tftypes.UnknownValue,
				tftypes.UnknownValue,
				tftypes.UnknownValue,
			))
			plan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName:         "litellm_credential",
				Config:           planningConfig,
				PriorState:       nullState,
				ProposedNewState: proposed,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(plan.Diagnostics) {
				t.Fatalf("plan resolved create: err=%v diagnostics=%v", err, plan.Diagnostics)
			}
			plannedValue, err := plan.PlannedState.Unmarshal(resourceType)
			if err != nil {
				t.Fatal(err)
			}
			if plannedValue.IsFullyKnown() {
				t.Fatal("test precondition failed: planned state unexpectedly fully known")
			}
			var plannedAttributes map[string]tftypes.Value
			if err := plannedValue.As(&plannedAttributes); err != nil {
				t.Fatal(err)
			}
			if plannedAttributes["model_id"].IsKnown() || plannedAttributes["credential_info_json"].IsKnown() || plannedAttributes["credential_values_json"].IsKnown() {
				t.Fatalf("test precondition failed: planned apply-time values are known: %#v", plannedAttributes)
			}

			applyConfig := credentialProtocolReplace(t, resourceType, planningConfig, map[string]tftypes.Value{
				"model_id": tftypes.NewValue(tftypes.String, "resolved/model"),
			})
			apply, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName:       "litellm_credential",
				Config:         applyConfig,
				PriorState:     nullState,
				PlannedState:   plan.PlannedState,
				PlannedPrivate: plan.PlannedPrivate,
			})
			if err != nil {
				t.Fatalf("apply resolved create: %v", err)
			}
			if gotError := accessGroupProtocolDiagnosticsHaveError(apply.Diagnostics); gotError != test.ambiguous {
				t.Fatalf("apply diagnostics error=%t want=%t diagnostics=%v", gotError, test.ambiguous, apply.Diagnostics)
			}
			if apply.NewState == nil {
				t.Fatal("apply returned no caller-known state")
			}
			stateValue, err := apply.NewState.Unmarshal(resourceType)
			if err != nil {
				t.Fatal(err)
			}
			if !stateValue.IsFullyKnown() {
				t.Fatalf("apply returned protocol-invalid unknown state: %#v", stateValue)
			}
			var attributes map[string]tftypes.Value
			if err := stateValue.As(&attributes); err != nil {
				t.Fatal(err)
			}
			for attribute, want := range map[string]string{
				"id":                credentialName,
				"credential_name":   credentialName,
				"model_id":          "resolved/model",
				"credential_source": "model_id",
			} {
				var got string
				if err := attributes[attribute].As(&got); err != nil || got != want {
					t.Fatalf("%s=%q want=%q err=%v", attribute, got, want, err)
				}
			}
			var active bool
			if err := attributes["credential_values_active"].As(&active); err != nil || active {
				t.Fatalf("credential_values_active=%t err=%v", active, err)
			}
			var stateInfo map[string]tftypes.Value
			if err := attributes["credential_info"].As(&stateInfo); err != nil {
				t.Fatalf("decode configured info: %v", err)
			}
			var owner string
			if err := stateInfo["owner"].As(&owner); err != nil || owner != "caller" {
				t.Fatalf("configured info owner=%q err=%v", owner, err)
			}
			var stateValues map[string]tftypes.Value
			if err := attributes["credential_values"].As(&stateValues); err != nil {
				t.Fatalf("decode configured values: %v", err)
			}
			var secret string
			if err := stateValues["api_key"].As(&secret); err != nil || secret != "inactive-secret" {
				t.Fatalf("write-only config was not preserved: secret=%q err=%v", secret, err)
			}
			if !attributes["credential_values_json"].IsNull() {
				t.Fatalf("omitted write-only JSON was not retained as known null: %#v", attributes)
			}
			if test.ambiguous {
				if !attributes["credential_info_json"].IsNull() {
					t.Fatalf("ambiguous state claimed unverified remote metadata: %#v", attributes)
				}
				var privateEnvelope map[string]string
				if err := json.Unmarshal(apply.Private, &privateEnvelope); err != nil {
					t.Fatalf("decode uncertain private envelope: %v", err)
				}
				encodedMarker, err := base64.StdEncoding.DecodeString(privateEnvelope[credentialPrivateMetadataKey])
				if err != nil {
					t.Fatalf("decode uncertain marker: %v", err)
				}
				metadata, ok := decodeCredentialPrivateMetadata(encodedMarker)
				if !ok || !metadata.UncertainOwnership || metadata.AllRemoteOwned {
					t.Fatalf("ambiguous state did not retain the uncertain marker: %#v", metadata)
				}
			} else {
				var infoJSON string
				if err := attributes["credential_info_json"].As(&infoJSON); err != nil || infoJSON != `{"owner":"caller","remote":"authoritative"}` {
					t.Fatalf("authoritative info JSON=%q err=%v", infoJSON, err)
				}
			}

			// Re-applying the stale create cannot dispatch a second POST. Exact-name
			// preflight observes the one committed object and fails as a collision.
			retry, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName:       "litellm_credential",
				Config:         applyConfig,
				PriorState:     nullState,
				PlannedState:   plan.PlannedState,
				PlannedPrivate: plan.PlannedPrivate,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(retry.Diagnostics) {
				t.Fatalf("retry did not fail exact-name preflight: err=%v diagnostics=%v", err, retry.Diagnostics)
			}
			if !created || posts != 1 || collisions != 0 || gets != 3 {
				t.Fatalf("create retry orphan/collision safety: created=%t posts=%d collisions=%d gets=%d requests=%v", created, posts, collisions, gets, requests)
			}
			wantRequests := []string{
				"GET /credentials/by_name/" + credentialName,
				"POST /credentials",
				"GET /credentials/by_name/" + credentialName,
				"GET /credentials/by_name/" + credentialName,
			}
			if !reflect.DeepEqual(requests, wantRequests) {
				t.Fatalf("request sequence=%v want=%v", requests, wantRequests)
			}
		})
	}
}

func TestCredentialCreateBothKeepsValuesInactiveAndPostflights(t *testing.T) {
	t.Parallel()
	var requests []string
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		requests = append(requests, request.Method+" "+request.RequestURI)
		switch request.Method {
		case http.MethodPost:
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			if body["model_id"] != "provider/model" || body["credential_values"].(map[string]interface{})["api_key"] != "inactive-secret" {
				t.Errorf("POST body = %#v", body)
			}
			created = true
			_, _ = writer.Write([]byte(`{"success":true,"message":"Credential created successfully"}`))
		case http.MethodGet:
			if !created {
				http.NotFound(writer, request)
				return
			}
			_, _ = writer.Write([]byte(`{"credential_name":"both","credential_info":{},"credential_values":{"api_key":"mo****et"}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := credentialTestSchema(t)
	model := credentialTestModel("both", map[string]string{}, map[string]string{"api_key": "inactive-secret"})
	model.ID = types.StringUnknown()
	model.ModelID = types.StringValue("provider/model")
	model.CredentialValuesActive = types.BoolUnknown()
	model.CredentialSource = types.StringUnknown()
	plan := credentialTestPlan(t, schema, model)
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&CredentialResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}).Create(
		context.Background(), resource.CreateRequest{Plan: plan}, response,
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", response.Diagnostics)
	}
	if !reflect.DeepEqual(requests, []string{
		"GET /credentials/by_name/both",
		"POST /credentials",
		"GET /credentials/by_name/both",
	}) {
		t.Fatalf("requests = %#v", requests)
	}
	var state CredentialResourceModel
	if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
		t.Fatalf("decode state: %v", diagnostics)
	}
	if state.CredentialValuesActive.ValueBool() || state.CredentialSource.ValueString() != "model_id" {
		t.Fatalf("model dominance not represented honestly: %#v", state)
	}
	var values map[string]string
	state.CredentialValues.ElementsAs(context.Background(), &values, false)
	if values["api_key"] != "inactive-secret" {
		t.Fatal("legacy HCL compatibility value was not retained")
	}
}

func TestCredentialMalformedCreateResponseRetainsRecoveryIdentity(t *testing.T) {
	t.Parallel()
	created := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			created = true
			_, _ = writer.Write([]byte(`{"status_code":500,"detail":"serialized exception"}`))
			return
		}
		if !created {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(`{"credential_name":"recover","credential_info":{},"credential_values":{"api_key":"re****et"}}`))
	}))
	defer server.Close()
	schema := credentialTestSchema(t)
	model := credentialTestModel("recover", map[string]string{}, map[string]string{"api_key": "recovery-secret"})
	model.ID = types.StringUnknown()
	plan := credentialTestPlan(t, schema, model)
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&CredentialResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}).Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("serialized exception body was accepted")
	}
	var state CredentialResourceModel
	if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
		t.Fatalf("decode recovery state: %v", diagnostics)
	}
	if state.ID.ValueString() != "recover" {
		t.Fatalf("recovery identity lost: %#v", state)
	}
}

func TestCredentialPatchHydratesNewNestedObjectsAndProtectsTransitions(t *testing.T) {
	t.Parallel()
	remote := map[string]interface{}{
		"settings":     map[string]interface{}{"external": "keep", "nested": map[string]interface{}{"remote": true}},
		"external_top": "preserve for v1.98 credential_info rebuild",
	}
	desired := map[string]interface{}{
		"settings": map[string]interface{}{"managed": "new", "nested": map[string]interface{}{"owned": true}},
	}
	desiredOwnership := credentialOwnershipForObject(desired)
	patch, err := hydrateCredentialPatch(remote, map[string]interface{}{}, desired, emptyCredentialOwnership(), desiredOwnership, false)
	if err == nil {
		patch, err = hydrateCredentialInfoTopLevel(remote, patch, emptyCredentialOwnership(), desiredOwnership)
	}
	if err != nil {
		t.Fatalf("hydrate new nested object: %v", err)
	}
	if patch["external_top"] != "preserve for v1.98 credential_info rebuild" {
		t.Fatalf("unmanaged top-level info was not carried safely: %#v", patch)
	}
	settings := patch["settings"].(map[string]interface{})
	if settings["external"] != "keep" || settings["managed"] != "new" {
		t.Fatalf("hydrated settings = %#v", settings)
	}
	nested := settings["nested"].(map[string]interface{})
	if nested["remote"] != true || nested["owned"] != true {
		t.Fatalf("hydrated nested settings = %#v", nested)
	}

	prior := map[string]interface{}{"settings": map[string]interface{}{"managed": "old"}}
	if _, err := hydrateCredentialPatch(remote, prior, map[string]interface{}{"settings": "scalar"}, credentialOwnershipForObject(prior), credentialOwnershipForObject(map[string]interface{}{"settings": "scalar"}), false); err == nil {
		t.Fatal("object-to-scalar transition discarded an unmanaged sibling")
	}
	maskedRemote := map[string]interface{}{
		"credentials": map[string]interface{}{"region": "us-east-1", "api_key": "sk****et"},
	}
	maskedDesired := map[string]interface{}{"credentials": map[string]interface{}{"region": "us-west-2"}}
	if _, err := hydrateCredentialPatch(maskedRemote, map[string]interface{}{}, maskedDesired, emptyCredentialOwnership(), credentialOwnershipForObject(maskedDesired), true); err == nil {
		t.Fatal("unmanaged nested mask was sent back as reconstructable data")
	}
}

func TestCredentialReplacementOwnershipRequiresCompleteReconstructability(t *testing.T) {
	t.Parallel()
	prior := map[string]interface{}{
		"api_key": "secret",
		"nested":  map[string]interface{}{"owned": "value"},
	}
	ownership := credentialOwnershipForObject(prior)
	remote := map[string]interface{}{
		"api_key": "se****et",
		"nested":  map[string]interface{}{"owned": "value"},
	}
	if !credentialRemoteFullyOwned(remote, prior, ownership, true) {
		t.Fatal("fully owned reconstructable credential was not eligible for guarded replacement")
	}
	remote["nested"].(map[string]interface{})["external"] = "unmanaged"
	if credentialRemoteFullyOwned(remote, prior, ownership, true) {
		t.Fatal("credential with unmanaged nested data was considered fully owned")
	}
	if credentialRemoteFullyOwned(map[string]interface{}{"api_key": "ot****er"}, prior, ownership, true) {
		t.Fatal("credential with a non-corresponding mask was considered reconstructable")
	}

	atomicPrior := map[string]interface{}{"shape": "scalar"}
	atomicOwnership := credentialOwnershipForObject(atomicPrior)
	atomicToObject := map[string]interface{}{"shape": map[string]interface{}{"masked_secret": "sk****et", "external": true}}
	if credentialRemoteFullyOwned(atomicToObject, atomicPrior, atomicOwnership, true) {
		t.Fatal("atomic-to-object remote shape drift was considered fully owned")
	}
	if _, err := projectCredentialObject(atomicToObject, atomicPrior, atomicOwnership, true); err == nil {
		t.Fatal("atomic-to-object remote shape drift was projected into owned state")
	}

	objectPrior := map[string]interface{}{"shape": map[string]interface{}{"owned": "value"}}
	objectOwnership := credentialOwnershipForObject(objectPrior)
	objectToAtomic := map[string]interface{}{"shape": "remote-scalar"}
	if credentialRemoteFullyOwned(objectToAtomic, objectPrior, objectOwnership, false) {
		t.Fatal("object-to-atomic remote shape drift was considered fully owned")
	}
	if _, err := projectCredentialObject(objectToAtomic, objectPrior, objectOwnership, false); err == nil {
		t.Fatal("object-to-atomic remote shape drift was projected into owned state")
	}
	if _, err := hydrateCredentialPatch(objectToAtomic, objectPrior, objectPrior, objectOwnership, objectOwnership, false); err == nil {
		t.Fatal("object-to-atomic remote shape drift was patched over")
	}
}

func TestCredentialProtocolReplacementDeleteRefusesAtomicObjectShapeDrift(t *testing.T) {
	t.Parallel()
	const name = "shape-replacement"
	var mu sync.Mutex
	created := false
	remoteInfo := map[string]interface{}{"shape": "scalar"}
	deleteCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodPost:
			created = true
			_, _ = writer.Write([]byte(`{"success":true,"message":"Credential created successfully"}`))
		case http.MethodGet:
			if !created || request.RequestURI != credentialByNamePath(name) {
				http.NotFound(writer, request)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"credential_name":   name,
				"credential_info":   remoteInfo,
				"credential_values": map[string]interface{}{"api_key": "se****et"},
			})
		case http.MethodDelete:
			deleteCalls++
			_, _ = writer.Write([]byte(`{"success":true,"message":"Credential deleted successfully"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer api.Close()
	server, resourceType := credentialProtocolServer(t, api.URL)
	ctx := context.Background()
	nullMap := credentialProtocolMap(nil)
	values := credentialProtocolStringMap(map[string]string{"api_key": "secret"})
	config := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, name, nil, nil, nullMap, values, `{"shape":"scalar"}`, nil, nil, nil))
	proposed := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, name, tftypes.UnknownValue, nil, credentialProtocolMap(tftypes.UnknownValue), values, `{"shape":"scalar"}`, tftypes.UnknownValue, tftypes.UnknownValue, tftypes.UnknownValue))
	nullState := credentialProtocolDynamicValue(t, resourceType, tftypes.NewValue(resourceType, nil))
	createPlan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "litellm_credential",
		Config:           config,
		PriorState:       nullState,
		ProposedNewState: proposed,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(createPlan.Diagnostics) {
		t.Fatalf("plan shape credential: err=%v diagnostics=%v", err, createPlan.Diagnostics)
	}
	createApply, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:       "litellm_credential",
		Config:         config,
		PriorState:     nullState,
		PlannedState:   createPlan.PlannedState,
		PlannedPrivate: createPlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(createApply.Diagnostics) {
		t.Fatalf("apply shape credential: err=%v diagnostics=%v", err, createApply.Diagnostics)
	}

	replacementConfig := credentialProtocolReplace(t, resourceType, config, map[string]tftypes.Value{
		"model_id": tftypes.NewValue(tftypes.String, "replacement/model"),
	})
	replacementProposed := credentialProtocolReplace(t, resourceType, createApply.NewState, map[string]tftypes.Value{
		"model_id": tftypes.NewValue(tftypes.String, "replacement/model"),
	})
	replacementPlan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "litellm_credential",
		Config:           replacementConfig,
		PriorState:       createApply.NewState,
		ProposedNewState: replacementProposed,
		PriorPrivate:     createApply.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(replacementPlan.Diagnostics) || len(replacementPlan.RequiresReplace) == 0 {
		t.Fatalf("plan guarded replacement: err=%v diagnostics=%v requires_replace=%v", err, replacementPlan.Diagnostics, replacementPlan.RequiresReplace)
	}

	mu.Lock()
	remoteInfo = map[string]interface{}{
		"shape": map[string]interface{}{
			"masked_secret": "sk****et",
			"external":      true,
		},
	}
	mu.Unlock()
	readDrift, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_credential",
		CurrentState: createApply.NewState,
		Private:      createApply.Private,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(readDrift.Diagnostics) {
		t.Fatalf("atomic-to-object drift read: err=%v diagnostics=%v", err, readDrift.Diagnostics)
	}

	deleteApply, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:       "litellm_credential",
		Config:         replacementConfig,
		PriorState:     createApply.NewState,
		PlannedState:   nullState,
		PlannedPrivate: replacementPlan.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(deleteApply.Diagnostics) {
		t.Fatalf("guarded replacement deletion: err=%v diagnostics=%v", err, deleteApply.Diagnostics)
	}
	mu.Lock()
	defer mu.Unlock()
	if deleteCalls != 0 {
		t.Fatalf("shape-mismatched credential was destructively deleted %d time(s)", deleteCalls)
	}
}

func TestCredentialTopLevelRemovalPlansSafeErrorNotReplacement(t *testing.T) {
	t.Parallel()
	stateModel := credentialTestModel("remove", map[string]string{"keep": "yes", "remove": "owned"}, map[string]string{"api_key": "secret"})
	prior, err := inferCredentialPrivateMetadata(context.Background(), stateModel)
	if err != nil {
		t.Fatal(err)
	}
	configModel := credentialTestModel("remove", map[string]string{"keep": "yes"}, map[string]string{"api_key": "secret"})
	desired, err := buildCredentialConfiguredObject(context.Background(), configModel.CredentialInfo, configModel.CredentialInfoJSON)
	if err != nil {
		t.Fatal(err)
	}
	if !credentialTopLevelKeyRemoved(credentialMetadataOwnership(prior, false), desired.UnionOwnership) {
		t.Fatal("top-level removal was not classified as unsafe")
	}
	if len(credentialReplacementPaths(stateModel, configModel)) != 0 {
		t.Fatal("top-level content removal was incorrectly classified as replacement")
	}
}

func TestCredentialUpdateNeverSendsModelAndRequiresValidBodyAndPostflight(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var calls []string
	var patchBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		calls = append(calls, request.Method+" "+request.RequestURI)
		call := len(calls)
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch call {
		case 1:
			_, _ = writer.Write([]byte(`{"credential_name":"update","credential_info":{"env":"old","nested":{"owned":"old","external":"keep"}},"credential_values":{"api_key":"se****et"}}`))
		case 2:
			_ = json.NewDecoder(request.Body).Decode(&patchBody)
			_, _ = writer.Write([]byte(`{"success":true,"message":"Credential updated successfully"}`))
		default:
			_, _ = writer.Write([]byte(`{"credential_name":"update","credential_info":{"env":"new","nested":{"owned":"new","external":"keep"}},"credential_values":{"api_key":"se****et"}}`))
		}
	}))
	defer server.Close()
	schema := credentialTestSchema(t)
	stateModel := credentialTestModel("update", map[string]string{"env": "old"}, map[string]string{"api_key": "secret"})
	stateModel.CredentialInfoJSON = types.StringValue(`{"nested":{"owned":"old"}}`)
	planModel := credentialTestModel("update", map[string]string{"env": "new"}, map[string]string{"api_key": "secret"})
	planModel.CredentialInfoJSON = types.StringValue(`{"nested":{"owned":"new"}}`)
	state := credentialTestState(t, schema, stateModel)
	plan := credentialTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}
	(&CredentialResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}).Update(
		context.Background(), resource.UpdateRequest{State: state, Plan: plan, Config: credentialTestConfig(t, schema, planModel)}, response,
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	if _, exists := patchBody["model_id"]; exists {
		t.Fatalf("PATCH sent create-only model_id: %#v", patchBody)
	}
	nested := patchBody["credential_info"].(map[string]interface{})["nested"].(map[string]interface{})
	if nested["external"] != "keep" || nested["owned"] != "new" {
		t.Fatalf("PATCH did not hydrate nested siblings: %#v", patchBody)
	}
	if len(calls) != 3 {
		t.Fatalf("PATCH did not perform preflight and postflight GET: %#v", calls)
	}
}

func TestCredentialUpdateRejectsSerializedExceptionEvenWhenPostflightMatches(t *testing.T) {
	t.Parallel()
	call := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		call++
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPatch {
			_, _ = writer.Write([]byte(`{"status_code":404,"detail":"exception"}`))
			return
		}
		env := "old"
		if call > 2 {
			env = "new"
		}
		_, _ = writer.Write([]byte(fmt.Sprintf(`{"credential_name":"exception","credential_info":{"env":%q},"credential_values":{}}`, env)))
	}))
	defer server.Close()
	schema := credentialTestSchema(t)
	stateModel := credentialTestModel("exception", map[string]string{"env": "old"}, map[string]string{})
	planModel := credentialTestModel("exception", map[string]string{"env": "new"}, map[string]string{})
	state := credentialTestState(t, schema, stateModel)
	plan := credentialTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}
	(&CredentialResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}).Update(
		context.Background(), resource.UpdateRequest{State: state, Plan: plan, Config: credentialTestConfig(t, schema, planModel)}, response,
	)
	if !response.Diagnostics.HasError() {
		t.Fatal("serialized PATCH exception was accepted")
	}
}

func TestCredentialDeleteValidatesBodyAndExactAbsence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		body      string
		getStatus int
		wantError bool
	}{
		{"success", `{"success":true,"message":"Credential deleted successfully"}`, http.StatusNotFound, false},
		{"serialized exception", `{"status_code":500,"detail":"exception"}`, http.StatusNotFound, true},
		{"still exists", `{"success":true,"message":"Credential deleted successfully"}`, http.StatusOK, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodDelete {
					_, _ = writer.Write([]byte(test.body))
					return
				}
				writer.WriteHeader(test.getStatus)
				if test.getStatus == http.StatusOK {
					_, _ = writer.Write([]byte(`{"credential_name":"delete","credential_info":{},"credential_values":{}}`))
				} else {
					_, _ = writer.Write([]byte(`{"detail":"missing"}`))
				}
			}))
			defer server.Close()
			schema := credentialTestSchema(t)
			state := credentialTestState(t, schema, credentialTestModel("delete", map[string]string{}, map[string]string{}))
			response := &resource.DeleteResponse{}
			(&CredentialResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}).Delete(context.Background(), resource.DeleteRequest{State: state}, response)
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatalf("diagnostics=%v want error=%t", response.Diagnostics, test.wantError)
			}
		})
	}
}

func TestCredentialImportIsMinimalMetadataOnly(t *testing.T) {
	// Private import state is exercised through provider protocol elsewhere;
	// this path assertion ensures public values and model provenance are never invented.
	t.Parallel()
	schema := credentialTestSchema(t)
	blank := credentialTestModel("", nil, nil)
	blank.ID = types.StringNull()
	blank.CredentialName = types.StringNull()
	state := credentialTestState(t, schema, blank)
	response := &resource.ImportStateResponse{State: state}
	(&CredentialResource{}).ImportState(context.Background(), resource.ImportStateRequest{ID: "team/name%?#雪"}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var imported CredentialResourceModel
	if diagnostics := response.State.Get(context.Background(), &imported); diagnostics.HasError() {
		t.Fatalf("decode import: %v", diagnostics)
	}
	if imported.ID.ValueString() != "team/name%?#雪" || !imported.ModelID.IsNull() || !imported.CredentialValues.IsNull() || !imported.CredentialValuesJSON.IsNull() || imported.CredentialSource.ValueString() != "imported" {
		t.Fatalf("imported state = %#v", imported)
	}
	if _, err := credentialOwnedObjectFromSurfaces(context.Background(), imported.CredentialInfo, imported.CredentialInfoJSON, emptyCredentialOwnership(), emptyCredentialOwnership()); err != nil {
		t.Fatalf("metadata-only import could not refresh unknown computed surfaces: %v", err)
	}
}

func TestCredentialProtocolImportCarriesPrivateMetadata(t *testing.T) {
	t.Parallel()
	server := providerserver.NewProtocol6(New("test")())()
	response, err := server.ImportResourceState(context.Background(), &tfprotov6.ImportResourceStateRequest{
		TypeName: "litellm_credential",
		ID:       "import/name%?#雪",
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
		t.Fatalf("protocol import: err=%v diagnostics=%v", err, response.Diagnostics)
	}
	if len(response.ImportedResources) != 1 {
		t.Fatalf("imported resources = %d", len(response.ImportedResources))
	}
	imported := response.ImportedResources[0]
	if len(imported.Private) == 0 || !strings.Contains(string(imported.Private), credentialPrivateMetadataKey) {
		t.Fatalf("metadata-only ownership marker missing from private state: %q", imported.Private)
	}
}

func TestCredentialProtocolImportIsSourceFreeNoOpAndRejectsSourceAdoption(t *testing.T) {
	t.Parallel()
	const identity = "team/credential%雪"
	var mu sync.Mutex
	var requests []string
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		requests = append(requests, request.Method+" "+request.RequestURI)
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet || request.RequestURI != credentialByNamePath(identity) {
			http.Error(writer, "unexpected mutation or route", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"credential_name": identity,
			"credential_info": map[string]interface{}{
				"owner":  "outside",
				"nested": map[string]interface{}{"enabled": true},
			},
			"credential_values": map[string]interface{}{
				"api_key":     "sk****et",
				"credentials": map[string]interface{}{"token": "to****en"},
			},
		})
	}))
	defer api.Close()
	server, resourceType := credentialProtocolServer(t, api.URL)
	ctx := context.Background()

	importResponse, err := server.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_credential", ID: identity})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importResponse.Diagnostics) || len(importResponse.ImportedResources) != 1 {
		t.Fatalf("protocol import: err=%v diagnostics=%v resources=%d", err, importResponse.Diagnostics, len(importResponse.ImportedResources))
	}
	imported := importResponse.ImportedResources[0]
	readResponse, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_credential",
		CurrentState: imported.State,
		Private:      imported.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(readResponse.Diagnostics) {
		t.Fatalf("protocol import refresh: err=%v diagnostics=%v", err, readResponse.Diagnostics)
	}
	readValue, err := readResponse.NewState.Unmarshal(resourceType)
	if err != nil {
		t.Fatalf("decode imported credential state: %v", err)
	}
	var readAttributes map[string]tftypes.Value
	if err := readValue.As(&readAttributes); err != nil {
		t.Fatalf("decode imported credential attributes: %v", err)
	}
	for _, name := range []string{"credential_values", "credential_values_json", "model_id"} {
		if !readAttributes[name].IsKnown() || !readAttributes[name].IsNull() {
			t.Fatalf("imported %s = %s, want known null", name, readAttributes[name])
		}
	}
	var source string
	if err := readAttributes["credential_source"].As(&source); err != nil || source != "imported" {
		t.Fatalf("imported source = %q err=%v", source, err)
	}

	nullMap := credentialProtocolMap(nil)
	config := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, identity, nil, nil, nullMap, nullMap, nil, nil, nil, nil))
	noOpPlan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "litellm_credential",
		Config:           config,
		PriorState:       readResponse.NewState,
		ProposedNewState: readResponse.NewState,
		PriorPrivate:     readResponse.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(noOpPlan.Diagnostics) {
		t.Fatalf("source-free imported plan: err=%v diagnostics=%v", err, noOpPlan.Diagnostics)
	}
	plannedValue, err := noOpPlan.PlannedState.Unmarshal(resourceType)
	if err != nil || !plannedValue.Equal(readValue) {
		t.Fatalf("source-free imported plan changed state: err=%v planned=%s prior=%s", err, plannedValue, readValue)
	}

	assertOnlyImportRead := func(stage string) {
		t.Helper()
		mu.Lock()
		defer mu.Unlock()
		if !reflect.DeepEqual(requests, []string{http.MethodGet + " " + credentialByNamePath(identity)}) {
			t.Fatalf("%s sent unexpected requests: %#v", stage, requests)
		}
	}
	assertOnlyImportRead("source-free plan")

	for _, adoption := range []struct {
		name         string
		configChange map[string]tftypes.Value
		planChange   map[string]tftypes.Value
	}{
		{
			name: "empty legacy values",
			configChange: map[string]tftypes.Value{
				"credential_values": credentialProtocolStringMap(map[string]string{}),
			},
			planChange: map[string]tftypes.Value{
				"credential_values": credentialProtocolStringMap(map[string]string{}),
			},
		},
		{
			name: "nonempty legacy values",
			configChange: map[string]tftypes.Value{
				"credential_values": credentialProtocolStringMap(map[string]string{"api_key": "new-source"}),
			},
			planChange: map[string]tftypes.Value{
				"credential_values": credentialProtocolStringMap(map[string]string{"api_key": "new-source"}),
			},
		},
		{
			name: "unknown legacy values",
			configChange: map[string]tftypes.Value{
				"credential_values": credentialProtocolMap(tftypes.UnknownValue),
			},
			planChange: map[string]tftypes.Value{
				"credential_values": credentialProtocolMap(tftypes.UnknownValue),
			},
		},
		{
			name: "model",
			configChange: map[string]tftypes.Value{
				"model_id": tftypes.NewValue(tftypes.String, "deployment/source"),
			},
			planChange: map[string]tftypes.Value{
				"model_id": tftypes.NewValue(tftypes.String, "deployment/source"),
			},
		},
	} {
		t.Run(adoption.name, func(t *testing.T) {
			adoptionConfig := credentialProtocolReplace(t, resourceType, config, adoption.configChange)
			adoptionProposed := credentialProtocolReplace(t, resourceType, readResponse.NewState, adoption.planChange)
			planResponse, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName:         "litellm_credential",
				Config:           adoptionConfig,
				PriorState:       readResponse.NewState,
				ProposedNewState: adoptionProposed,
				PriorPrivate:     readResponse.Private,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(planResponse.Diagnostics) {
				t.Fatalf("unsafe imported adoption plan: err=%v diagnostics=%v", err, planResponse.Diagnostics)
			}
			assertOnlyImportRead("unsafe adoption plan")

			// Exercise the protocol apply boundary as a defense in depth. Even a
			// caller bypassing the failed plan cannot reach preflight, PATCH, or
			// replacement deletion while import reconstructability is unknown.
			applyResponse, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName:       "litellm_credential",
				Config:         adoptionConfig,
				PriorState:     readResponse.NewState,
				PlannedState:   adoptionProposed,
				PlannedPrivate: readResponse.Private,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applyResponse.Diagnostics) {
				t.Fatalf("unsafe imported adoption apply: err=%v diagnostics=%v", err, applyResponse.Diagnostics)
			}
			assertOnlyImportRead("unsafe adoption apply")
		})
	}
}

func TestCredentialImportedSourceAdoptionFailsBeforeReplacement(t *testing.T) {
	t.Parallel()
	metadata := credentialPrivateMetadata{Version: 1, Imported: true, LegacyInfo: emptyCredentialOwnership(), JSONInfo: emptyCredentialOwnership(), LegacyValues: emptyCredentialOwnership(), JSONValues: emptyCredentialOwnership()}
	if !metadata.Imported {
		t.Fatal("invalid fixture")
	}
	config := credentialTestModel("imported", nil, map[string]string{"api_key": "known-now"})
	if !credentialConfigHasSource(config) {
		t.Fatal("new values source was not recognized")
	}
	// The ModifyPlan branch is also covered by protocol-level private-state
	// tests; assert the metadata-only state remains explicitly unowned here.
	if len(credentialMetadataOwnership(metadata, true).Children) != 0 || metadata.AllRemoteOwned {
		t.Fatalf("import accidentally adopted secret ownership: %#v", metadata)
	}
}

func TestCredentialReadSelectiveOwnershipAndMasks(t *testing.T) {
	t.Parallel()
	ownership := credentialOwnershipForObject(map[string]interface{}{
		"api_key":     "secret",
		"credentials": map[string]interface{}{"client_secret": "nested", "region": "old"},
	})
	remote := map[string]interface{}{
		"api_key":     "se****et",
		"credentials": map[string]interface{}{"client_secret": "ne****ed", "region": "new", "external": "ignore"},
		"outside":     "ignore",
	}
	prior := map[string]interface{}{"api_key": "secret", "credentials": map[string]interface{}{"client_secret": "nested", "region": "old"}}
	projected, err := projectCredentialObject(remote, prior, ownership, true)
	if err != nil {
		t.Fatalf("project masked values: %v", err)
	}
	if projected["api_key"] != "secret" {
		t.Fatalf("mask entered state: %#v", projected)
	}
	nested := projected["credentials"].(map[string]interface{})
	if nested["client_secret"] != "nested" || nested["region"] != "new" {
		t.Fatalf("nested reconciliation = %#v", nested)
	}
	if _, ok := nested["external"]; ok {
		t.Fatalf("unmanaged nested key entered ownership: %#v", nested)
	}
	if _, err := projectCredentialObject(map[string]interface{}{"api_key": "ot****er"}, map[string]interface{}{"api_key": "secret"}, credentialOwnershipForObject(map[string]interface{}{"api_key": "secret"}), true); err == nil {
		t.Fatal("non-corresponding mask was accepted")
	}
}

func TestCredentialPathsAndDataSourceRouteContract(t *testing.T) {
	t.Parallel()
	name := "../team/name %?#雪"
	for _, route := range []string{credentialByNamePath(name), credentialMutationPath(name)} {
		if strings.Contains(route, "?") || strings.Contains(route, "#") || strings.Contains(route, "/../") {
			t.Fatalf("unsafe route: %q", route)
		}
	}

	var schemaResponse datasource.SchemaResponse
	(&CredentialDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
	modelAttribute := schemaResponse.Schema.Attributes["model_id"].(datasourceschema.StringAttribute)
	var validation validator.StringResponse
	for _, valueValidator := range modelAttribute.Validators {
		valueValidator.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("model_id"), ConfigValue: types.StringValue("provider/model")}, &validation)
	}
	if !validation.Diagnostics.HasError() {
		t.Fatal("by-model slash was accepted even though LiteLLM route is not path-capable")
	}
}

func TestCredentialDataSourcePreservesMapAndAddsFullJSON(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"credential_name":"data","credential_info":{"provider":"openai","enabled":true,"large":9007199254740993123456789},"credential_values":{"api_key":"sk****et"}}`))
	}))
	defer server.Close()
	var schemaResponse datasource.SchemaResponse
	(&CredentialDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
	configModel := CredentialDataSourceModel{CredentialName: types.StringValue("data"), ModelID: types.StringNull(), ID: types.StringNull(), CredentialInfo: types.MapUnknown(types.StringType), CredentialInfoJSON: types.StringUnknown()}
	configState := tfsdk.State{Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(context.Background()), nil), Schema: schemaResponse.Schema}
	if diagnostics := configState.Set(context.Background(), &configModel); diagnostics.HasError() {
		t.Fatalf("set config: %v", diagnostics)
	}
	response := &datasource.ReadResponse{State: tfsdk.State{Raw: configState.Raw, Schema: schemaResponse.Schema}}
	(&CredentialDataSource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}).Read(context.Background(), datasource.ReadRequest{Config: tfsdk.Config{Raw: configState.Raw, Schema: schemaResponse.Schema}}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("data source diagnostics: %v", response.Diagnostics)
	}
	var state CredentialDataSourceModel
	if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
		t.Fatalf("decode state: %v", diagnostics)
	}
	var compatibility map[string]string
	state.CredentialInfo.ElementsAs(context.Background(), &compatibility, false)
	if !reflect.DeepEqual(compatibility, map[string]string{"provider": "openai"}) {
		t.Fatalf("compatibility map = %#v", compatibility)
	}
	if !strings.Contains(state.CredentialInfoJSON.ValueString(), "9007199254740993123456789") || !strings.Contains(state.CredentialInfoJSON.ValueString(), `"enabled":true`) {
		t.Fatalf("full JSON lost heterogeneous values: %s", state.CredentialInfoJSON.ValueString())
	}
}

type credentialPrivateReaderStub struct {
	encoded []byte
	diags   diag.Diagnostics
}

func (s credentialPrivateReaderStub) GetKey(context.Context, string) ([]byte, diag.Diagnostics) {
	return s.encoded, s.diags
}

func TestCredentialCreatePreflightRefusesCollisionAndFailure(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{"exact collision", http.StatusOK, `{"credential_name":"preflight","credential_info":{},"credential_values":{}}`},
		{"unavailable preflight", http.StatusInternalServerError, `{"detail":"unavailable"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodPost {
					posts++
				}
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			schema := credentialTestSchema(t)
			model := credentialTestModel("preflight", nil, map[string]string{"api_key": "secret"})
			model.ID = types.StringUnknown()
			plan := credentialTestPlan(t, schema, model)
			response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
			(&CredentialResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}).Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
			if !response.Diagnostics.HasError() || posts != 0 {
				t.Fatalf("preflight diagnostics=%v posts=%d", response.Diagnostics, posts)
			}
		})
	}
}

func TestCredentialAmbiguousCreateRecoveryIsBoundedAndRetainsPartialIdentity(t *testing.T) {
	// The retry backoff is intentionally exercised, so do not run this in parallel.
	gets := 0
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			posts++
			_, _ = writer.Write([]byte(`{"accepted":true}`))
			return
		}
		gets++
		http.NotFound(writer, request)
	}))
	defer server.Close()
	schema := credentialTestSchema(t)
	model := credentialTestModel("bounded", nil, map[string]string{"api_key": "secret"})
	model.ID = types.StringUnknown()
	plan := credentialTestPlan(t, schema, model)
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&CredentialResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}).Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if !response.Diagnostics.HasError() || posts != 1 || gets != 1+credentialPostflightAttempts {
		t.Fatalf("bounded recovery diagnostics=%v posts=%d gets=%d", response.Diagnostics, posts, gets)
	}
	var partial CredentialResourceModel
	if diagnostics := response.State.Get(context.Background(), &partial); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if partial.ID.ValueString() != "bounded" || partial.CredentialName.ValueString() != "bounded" {
		t.Fatalf("partial identity = %#v", partial)
	}
}

func TestCredentialAmbiguousCreateClassification(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		accepted bool
		err      error
		want     bool
	}{
		"unusable success":        {accepted: true, want: true},
		"request timeout":         {err: &APIError{StatusCode: http.StatusRequestTimeout}, want: true},
		"server error":            {err: &APIError{StatusCode: http.StatusBadGateway}, want: true},
		"definite bad request":    {err: &APIError{StatusCode: http.StatusBadRequest}},
		"local transport failure": {err: &safeTransportError{kind: "LiteLLM HTTP transport request failed"}},
		"dispatched transport":    {err: &safeTransportError{kind: "LiteLLM HTTP transport request failed", dispatched: true}, want: true},
		"terminal TLS":            {err: &safeTransportError{kind: "LiteLLM TLS verification failed", dispatched: true}},
	} {
		t.Run(name, func(t *testing.T) {
			if got := shouldRecoverCredentialCreate(test.accepted, test.err); got != test.want {
				t.Fatalf("classification=%t want=%t", got, test.want)
			}
		})
	}
}

func TestCredentialProtocolAmbiguousCreateMarksUncertainAndBlocksMutations(t *testing.T) {
	// Stateful handler; do not run in parallel.
	created := false
	patches := 0
	deletes := 0
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			if !created {
				http.NotFound(writer, request)
				return
			}
			_, _ = writer.Write([]byte(`{"credential_name":"uncertain","credential_info":{"owner":"terraform"},"credential_values":{"api_key":"se****et"}}`))
		case http.MethodPost:
			created = true
			_, _ = writer.Write([]byte(`{"success":`))
		case http.MethodPatch:
			patches++
		case http.MethodDelete:
			deletes++
		}
	}))
	defer api.Close()
	server, resourceType := credentialProtocolServer(t, api.URL)
	ctx := context.Background()
	values := credentialProtocolStringMap(map[string]string{"api_key": "secret"})
	info := credentialProtocolStringMap(map[string]string{"owner": "terraform"})
	config := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, "uncertain", nil, nil, info, values, nil, nil, nil, nil))
	proposed := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, "uncertain", tftypes.UnknownValue, nil, info, values, tftypes.UnknownValue, tftypes.UnknownValue, tftypes.UnknownValue, tftypes.UnknownValue))
	nullState := credentialProtocolDynamicValue(t, resourceType, tftypes.NewValue(resourceType, nil))
	plan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_credential", Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(plan.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, plan.Diagnostics)
	}
	apply, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_credential", Config: config, PriorState: nullState, PlannedState: plan.PlannedState, PlannedPrivate: plan.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(apply.Diagnostics) || apply.NewState == nil {
		t.Fatalf("ambiguous apply: err=%v diagnostics=%v state=%v", err, apply.Diagnostics, apply.NewState)
	}
	var privateEnvelope map[string]string
	if err := json.Unmarshal(apply.Private, &privateEnvelope); err != nil {
		t.Fatalf("decode private envelope: %v", err)
	}
	encodedMarker, err := base64.StdEncoding.DecodeString(privateEnvelope[credentialPrivateMetadataKey])
	if err != nil {
		t.Fatalf("decode private marker: %v", err)
	}
	privateMetadata, ok := decodeCredentialPrivateMetadata(encodedMarker)
	if !ok || !privateMetadata.UncertainOwnership || privateMetadata.AllRemoteOwned || len(credentialMetadataOwnership(privateMetadata, false).Children) != 0 || len(credentialMetadataOwnership(privateMetadata, true).Children) != 0 {
		t.Fatalf("uncertain marker invalid: %#v raw=%q", privateMetadata, apply.Private)
	}
	value, err := apply.NewState.Unmarshal(resourceType)
	if err != nil {
		t.Fatal(err)
	}
	var attributes map[string]tftypes.Value
	if err := value.As(&attributes); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := attributes["id"].As(&id); err != nil || id != "uncertain" {
		t.Fatalf("partial id=%q err=%v", id, err)
	}

	updatedConfig := credentialProtocolReplace(t, resourceType, config, map[string]tftypes.Value{"credential_info": credentialProtocolStringMap(map[string]string{"owner": "changed"})})
	updatedProposed := credentialProtocolReplace(t, resourceType, apply.NewState, map[string]tftypes.Value{"credential_info": credentialProtocolStringMap(map[string]string{"owner": "changed"})})
	blockedPlan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_credential", Config: updatedConfig, PriorState: apply.NewState, ProposedNewState: updatedProposed, PriorPrivate: apply.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(blockedPlan.Diagnostics) || patches != 0 {
		t.Fatalf("blocked update plan: err=%v diagnostics=%v patches=%d", err, blockedPlan.Diagnostics, patches)
	}
	destroy, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_credential", Config: nullState, PriorState: apply.NewState, PlannedState: nullState, PlannedPrivate: apply.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(destroy.Diagnostics) || deletes != 0 {
		t.Fatalf("blocked destroy: err=%v diagnostics=%v deletes=%d", err, destroy.Diagnostics, deletes)
	}
}

func TestCredentialOwnedAtomicPreflightIsRecursiveForMaskedAndReadableLeaves(t *testing.T) {
	t.Parallel()
	prior := map[string]interface{}{
		"oauth": map[string]interface{}{
			"nested": map[string]interface{}{"client_secret": "nested-secret", "region": "us-east-1"},
		},
		"endpoint": "https://example.invalid",
	}
	ownership := credentialOwnershipForObject(prior)
	remote := map[string]interface{}{
		"oauth": map[string]interface{}{
			"nested": map[string]interface{}{"client_secret": "ne****et", "region": "us-east-1"},
		},
		"endpoint": "https://example.invalid",
	}
	if err := validateCredentialOwnedAtomicPreconditions(remote, prior, ownership, true); err != nil {
		t.Fatalf("valid recursive preflight: %v", err)
	}
	remote["oauth"].(map[string]interface{})["nested"].(map[string]interface{})["region"] = "out-of-band"
	if err := validateCredentialOwnedAtomicPreconditions(remote, prior, ownership, true); err == nil {
		t.Fatal("readable nested drift was accepted")
	}
	remote["oauth"].(map[string]interface{})["nested"].(map[string]interface{})["region"] = "us-east-1"
	remote["oauth"].(map[string]interface{})["nested"].(map[string]interface{})["client_secret"] = "ot****er"
	if err := validateCredentialOwnedAtomicPreconditions(remote, prior, ownership, true); err == nil {
		t.Fatal("non-corresponding nested mask was accepted")
	}
	remote["oauth"].(map[string]interface{})["nested"].(map[string]interface{})["client_secret"] = "ne****et"
	remote["endpoint"] = "https://changed.invalid"
	if _, err := hydrateCredentialPatch(remote, prior, map[string]interface{}{"oauth": prior["oauth"], "endpoint": "https://planned.invalid"}, ownership, ownership, true); err == nil {
		t.Fatal("planned overwrite bypassed prior-value compare-and-set")
	}
}

func TestCredentialSchemaZeroOwnershipUsesConfigOnlyAndInvalidPrivateFailsClosed(t *testing.T) {
	t.Parallel()
	config := credentialTestModel("schema-zero", map[string]string{"owned": "configured"}, map[string]string{"api_key": "secret"})
	fromConfig, diagnostics := readCredentialPrivateMetadata(context.Background(), credentialPrivateReaderStub{}, &config)
	if diagnostics.HasError() || len(credentialMetadataOwnership(fromConfig, false).Children) != 1 || len(credentialMetadataOwnership(fromConfig, true).Children) != 1 {
		t.Fatalf("config inference metadata=%#v diagnostics=%v", fromConfig, diagnostics)
	}
	withoutConfig, diagnostics := readCredentialPrivateMetadata(context.Background(), credentialPrivateReaderStub{}, nil)
	if diagnostics.HasError() || !withoutConfig.noPrivateFallback || len(credentialMetadataOwnership(withoutConfig, false).Children) != 0 || len(credentialMetadataOwnership(withoutConfig, true).Children) != 0 {
		t.Fatalf("state-only fallback metadata=%#v diagnostics=%v", withoutConfig, diagnostics)
	}
	invalid := []byte(`{"version":1,"legacy_info_configured":true,"legacy_info":{"object":true,"children":{"owned":{"object":true,"atomic":true}}}}`)
	blocked, diagnostics := readCredentialPrivateMetadata(context.Background(), credentialPrivateReaderStub{encoded: invalid}, &config)
	if !diagnostics.HasError() || len(credentialMetadataOwnership(blocked, false).Children) != 0 {
		t.Fatalf("invalid private was inferred: metadata=%#v diagnostics=%v", blocked, diagnostics)
	}
}

func TestCredentialSchemaZeroReadPreservesValuesWithoutPersistingOwnership(t *testing.T) {
	t.Parallel()
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"credential_name":"schema-zero","credential_info":{"remote":"current","nested":{"enabled":true}},"credential_values":{"api_key":"se****et"}}`))
	}))
	defer api.Close()
	server, resourceType := credentialProtocolServer(t, api.URL)
	current := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(
		resourceType,
		"schema-zero",
		"schema-zero",
		nil,
		credentialProtocolStringMap(map[string]string{"remote": "old"}),
		credentialProtocolStringMap(map[string]string{"api_key": "secret"}),
		nil,
		nil,
		true,
		"credential_values",
	))
	read, err := server.ReadResource(context.Background(), &tfprotov6.ReadResourceRequest{TypeName: "litellm_credential", CurrentState: current})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("schema-zero read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	value, err := read.NewState.Unmarshal(resourceType)
	if err != nil {
		t.Fatal(err)
	}
	var attributes map[string]tftypes.Value
	if err := value.As(&attributes); err != nil {
		t.Fatal(err)
	}
	var values map[string]tftypes.Value
	if err := attributes["credential_values"].As(&values); err != nil {
		t.Fatal(err)
	}
	var secret string
	if err := values["api_key"].As(&secret); err != nil || secret != "secret" {
		t.Fatalf("schema-zero secret=%q err=%v", secret, err)
	}
	if strings.Contains(string(read.Private), credentialPrivateMetadataKey) {
		t.Fatalf("read inferred private ownership from state: %q", read.Private)
	}
}

func TestCredentialProtocolReplacementMarkerPrecedesUnknownReturn(t *testing.T) {
	// Stateful handler; do not run in parallel.
	created := false
	remoteInfo := map[string]interface{}{"owned": "value"}
	deleteCalls := 0
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			if !created {
				http.NotFound(writer, request)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"credential_name": "unknown-replace", "credential_info": remoteInfo, "credential_values": map[string]interface{}{"api_key": "se****et"}})
		case http.MethodPost:
			created = true
			_, _ = writer.Write([]byte(`{"success":true,"message":"created"}`))
		case http.MethodDelete:
			deleteCalls++
			_, _ = writer.Write([]byte(`{"success":true,"message":"deleted"}`))
		}
	}))
	defer api.Close()
	server, resourceType := credentialProtocolServer(t, api.URL)
	ctx := context.Background()
	config := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, "unknown-replace", nil, nil, credentialProtocolStringMap(map[string]string{"owned": "value"}), credentialProtocolStringMap(map[string]string{"api_key": "secret"}), nil, nil, nil, nil))
	proposed := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, "unknown-replace", tftypes.UnknownValue, nil, credentialProtocolStringMap(map[string]string{"owned": "value"}), credentialProtocolStringMap(map[string]string{"api_key": "secret"}), tftypes.UnknownValue, tftypes.UnknownValue, tftypes.UnknownValue, tftypes.UnknownValue))
	nullState := credentialProtocolDynamicValue(t, resourceType, tftypes.NewValue(resourceType, nil))
	createPlan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_credential", Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(createPlan.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%v", err, createPlan.Diagnostics)
	}
	create, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_credential", Config: config, PriorState: nullState, PlannedState: createPlan.PlannedState, PlannedPrivate: createPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(create.Diagnostics) {
		t.Fatalf("create: err=%v diagnostics=%v", err, create.Diagnostics)
	}
	unknownConfig := credentialProtocolReplace(t, resourceType, config, map[string]tftypes.Value{
		"model_id":             tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"credential_info_json": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	})
	unknownProposed := credentialProtocolReplace(t, resourceType, create.NewState, map[string]tftypes.Value{
		"model_id":             tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
		"credential_info_json": tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	})
	replacementPlan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_credential", Config: unknownConfig, PriorState: create.NewState, ProposedNewState: unknownProposed, PriorPrivate: create.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(replacementPlan.Diagnostics) || len(replacementPlan.RequiresReplace) == 0 {
		t.Fatalf("unknown replacement plan: err=%v diagnostics=%v replace=%v", err, replacementPlan.Diagnostics, replacementPlan.RequiresReplace)
	}
	remoteInfo = map[string]interface{}{"owned": "value", "external": "must-preserve"}
	destroy, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_credential", Config: unknownConfig, PriorState: create.NewState, PlannedState: nullState, PlannedPrivate: replacementPlan.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(destroy.Diagnostics) || deleteCalls != 0 {
		t.Fatalf("guarded delete: err=%v diagnostics=%v deletes=%d", err, destroy.Diagnostics, deleteCalls)
	}
}

func TestCredentialCanonicalJSONPreservesProviderSideFractionLexeme(t *testing.T) {
	t.Parallel()
	const input = `{"fraction":0.123456789012345678901234567890123456789}`
	object, err := decodeCredentialJSONObjectString(input)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalCredentialJSON(object)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != input {
		t.Fatalf("provider-side fraction lexeme changed: %s", canonical)
	}
}
