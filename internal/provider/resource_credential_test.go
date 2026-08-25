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

type credentialAlternatingWorkerAPI struct {
	mu                  sync.Mutex
	nextWorker          int
	connectionWorkers   map[string]int
	workers             [2]*credentialRemote
	requests            []string
	freshGETs           int
	nonFreshGETs        int
	freshPATCHes        int
	freshDELETEs        int
	deletes             int
	posts               int
	conflictPatchWorker *int
}

func newCredentialAlternatingWorkerAPI(t *testing.T, workers [2]*credentialRemote) (*httptest.Server, *credentialAlternatingWorkerAPI) {
	t.Helper()
	cluster := &credentialAlternatingWorkerAPI{
		connectionWorkers: make(map[string]int),
		workers:           workers,
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		cluster.serveHTTP(writer, request)
	}))
	t.Cleanup(server.Close)
	return server, cluster
}

func (c *credentialAlternatingWorkerAPI) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()

	worker, ok := c.connectionWorkers[request.RemoteAddr]
	if !ok {
		worker = c.nextWorker % len(c.workers)
		c.nextWorker++
		c.connectionWorkers[request.RemoteAddr] = worker
	}
	writer.Header().Set("Content-Type", "application/json")
	c.requests = append(c.requests, request.Method+" "+request.RequestURI)

	switch request.Method {
	case http.MethodGet:
		if request.Close {
			c.freshGETs++
		} else {
			c.nonFreshGETs++
		}
		remote := c.workers[worker]
		if remote == nil {
			http.NotFound(writer, request)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"credential_name":   remote.name,
			"credential_info":   remote.info,
			"credential_values": remote.values,
		})
	case http.MethodPost:
		c.posts++
		var body map[string]interface{}
		_ = json.NewDecoder(request.Body).Decode(&body)
		name, _ := body["credential_name"].(string)
		info, _ := body["credential_info"].(map[string]interface{})
		values, _ := body["credential_values"].(map[string]interface{})
		c.workers[worker] = &credentialRemote{name: name, info: info, values: maskCredentialWorkerValues(values)}
		_, _ = writer.Write([]byte(`{"success":true,"message":"created"}`))
	case http.MethodPatch:
		if request.Close {
			c.freshPATCHes++
		}
		var body map[string]interface{}
		_ = json.NewDecoder(request.Body).Decode(&body)
		name, _ := body["credential_name"].(string)
		info, _ := body["credential_info"].(map[string]interface{})
		values, _ := body["credential_values"].(map[string]interface{})
		// LiteLLM v1.98 updates the durable row for every PATCH, but only
		// synchronizes credential_list when this worker already has the name.
		if c.workers[worker] != nil {
			if c.conflictPatchWorker != nil && worker == *c.conflictPatchWorker {
				info = map[string]interface{}{"env": "third-version"}
			}
			c.workers[worker] = &credentialRemote{name: name, info: info, values: maskCredentialWorkerValues(values)}
		}
		_, _ = writer.Write([]byte(`{"success":true,"message":"updated"}`))
	case http.MethodDelete:
		c.deletes++
		if request.Close {
			c.freshDELETEs++
		}
		c.workers[worker] = nil
		_, _ = writer.Write([]byte(`{"success":true,"message":"deleted"}`))
	default:
		http.NotFound(writer, request)
	}
}

func maskCredentialWorkerValues(values map[string]interface{}) map[string]interface{} {
	masked := make(map[string]interface{}, len(values))
	for key, value := range values {
		switch typed := value.(type) {
		case string:
			masked[key] = maskLiteLLMCredentialString(typed)
		case map[string]interface{}:
			masked[key] = maskCredentialWorkerValues(typed)
		default:
			masked[key] = typed
		}
	}
	return masked
}

func credentialWorkerRemote(name string, info map[string]interface{}) *credentialRemote {
	return &credentialRemote{
		name:   name,
		info:   info,
		values: map[string]interface{}{"api_key": maskLiteLLMCredentialString("secret")},
	}
}

func credentialDiagnosticsContain(diagnostics diag.Diagnostics, text string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Summary(), text) || strings.Contains(diagnostic.Detail(), text) {
			return true
		}
	}
	return false
}

func credentialProtocolDiagnosticsContain(diagnostics []*tfprotov6.Diagnostic, text string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Summary, text) || strings.Contains(diagnostic.Detail, text) {
			return true
		}
	}
	return false
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

func credentialProtocolPrivateMetadata(t *testing.T, private []byte) credentialPrivateMetadata {
	t.Helper()
	var envelope map[string]string
	if err := json.Unmarshal(private, &envelope); err != nil {
		t.Fatalf("decode credential private envelope: %v", err)
	}
	encoded, err := base64.StdEncoding.DecodeString(envelope[credentialPrivateMetadataKey])
	if err != nil {
		t.Fatalf("decode credential private metadata: %v", err)
	}
	metadata, ok := decodeCredentialPrivateMetadata(encoded)
	if !ok {
		t.Fatalf("invalid credential private metadata: %q", private)
	}
	return metadata
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
			if !created || posts != 1 || collisions != 0 || gets != 3*credentialProbeSampleSize {
				t.Fatalf("create retry orphan/collision safety: created=%t posts=%d collisions=%d gets=%d requests=%v", created, posts, collisions, gets, requests)
			}
			wantRequests := make([]string, 0, 3*credentialProbeSampleSize+1)
			for range credentialProbeSampleSize {
				wantRequests = append(wantRequests, "GET /credentials/by_name/"+credentialName)
			}
			wantRequests = append(wantRequests, "POST /credentials")
			for range 2 * credentialProbeSampleSize {
				wantRequests = append(wantRequests, "GET /credentials/by_name/"+credentialName)
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
	wantRequests := make([]string, 0, 2*credentialProbeSampleSize+1)
	for range credentialProbeSampleSize {
		wantRequests = append(wantRequests, "GET /credentials/by_name/both")
	}
	wantRequests = append(wantRequests, "POST /credentials")
	for range credentialProbeSampleSize {
		wantRequests = append(wantRequests, "GET /credentials/by_name/both")
	}
	if !reflect.DeepEqual(requests, wantRequests) {
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
	patched := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		calls = append(calls, request.Method+" "+request.RequestURI)
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPatch {
			_ = json.NewDecoder(request.Body).Decode(&patchBody)
			patched = true
			_, _ = writer.Write([]byte(`{"success":true,"message":"Credential updated successfully"}`))
			return
		}
		if patched {
			_, _ = writer.Write([]byte(`{"credential_name":"update","credential_info":{"env":"new","nested":{"owned":"new","external":"keep"}},"credential_values":{"api_key":"se****et"}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"credential_name":"update","credential_info":{"env":"old","nested":{"owned":"old","external":"keep"}},"credential_values":{"api_key":"se****et"}}`))
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
	if len(calls) != 2*credentialProbeSampleSize+credentialPatchFanoutSize {
		t.Fatalf("PATCH did not perform bounded fan-out with preflight and postflight probes: %#v", calls)
	}
}

func TestCredentialUpdatePostflightUsesCompleteShallowMerge(t *testing.T) {
	for _, test := range []struct {
		name      string
		third     bool
		wantError bool
	}{
		{name: "desired prior and 404 fan-out"},
		{name: "arbitrary third version", third: true, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			const name = "complete-shallow-merge"
			remoteBefore := credentialRemote{
				name: name,
				info: map[string]interface{}{
					"managed":   "old",
					"unmanaged": "keep",
				},
				values: map[string]interface{}{
					"api_key":        maskLiteLLMCredentialString("secret"),
					"model_value":    "server-selected",
					"external_token": maskLiteLLMCredentialString("external-secret"),
				},
			}
			desired := credentialRemote{
				name: name,
				info: map[string]interface{}{
					"managed":   "new",
					"unmanaged": "keep",
				},
				values: map[string]interface{}{
					"api_key":        maskLiteLLMCredentialString("secret"),
					"model_value":    "server-selected",
					"external_token": maskLiteLLMCredentialString("external-secret"),
				},
			}
			third := desired
			third.values = shallowMergeCredentialObject(desired.values, map[string]interface{}{
				"external_token": maskLiteLLMCredentialString("third-secret"),
			})

			getCalls := 0
			var patchBodies []map[string]interface{}
			api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodPatch {
					var body map[string]interface{}
					_ = json.NewDecoder(request.Body).Decode(&body)
					patchBodies = append(patchBodies, body)
					_, _ = writer.Write([]byte(`{"success":true,"message":"updated"}`))
					return
				}
				getCalls++
				remote := remoteBefore
				if getCalls > credentialProbeSampleSize {
					switch getCalls - credentialProbeSampleSize {
					case 1, 4:
						remote = desired
					case 2:
						remote = remoteBefore
					case 3:
						if test.third {
							remote = third
						} else {
							http.NotFound(writer, request)
							return
						}
					}
				}
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{
					"credential_name":   remote.name,
					"credential_info":   remote.info,
					"credential_values": remote.values,
				})
			}))
			defer api.Close()

			schema := credentialTestSchema(t)
			stateModel := credentialTestModel(name, map[string]string{"managed": "old"}, map[string]string{"api_key": "secret"})
			planModel := credentialTestModel(name, map[string]string{"managed": "new"}, map[string]string{"api_key": "secret"})
			state := credentialTestState(t, schema, stateModel)
			response := &resource.UpdateResponse{State: state}
			(&CredentialResource{client: &Client{APIBase: api.URL, APIKey: "admin", HTTPClient: api.Client()}}).Update(
				context.Background(),
				resource.UpdateRequest{State: state, Plan: credentialTestPlan(t, schema, planModel), Config: credentialTestConfig(t, schema, planModel)},
				response,
			)
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatalf("update diagnostics=%v want_error=%t", response.Diagnostics, test.wantError)
			}
			if !test.wantError && !credentialDiagnosticsContain(response.Diagnostics, "Worker Convergence") {
				t.Fatalf("desired/prior/404 fan-out lacked convergence warning: %v", response.Diagnostics)
			}
			if test.wantError && !credentialDiagnosticsContain(response.Diagnostics, "Postflight Failed") {
				t.Fatalf("third version was not a postflight conflict: %v", response.Diagnostics)
			}
			if len(patchBodies) != credentialPatchFanoutSize {
				t.Fatalf("PATCH fan-out=%d want=%d", len(patchBodies), credentialPatchFanoutSize)
			}
			for _, body := range patchBodies {
				info := body["credential_info"].(map[string]interface{})
				values := body["credential_values"].(map[string]interface{})
				if info["managed"] != "new" || info["unmanaged"] != "keep" || !reflect.DeepEqual(values, map[string]interface{}{"api_key": "secret"}) {
					t.Fatalf("hydrated shallow PATCH=%#v", body)
				}
			}
		})
	}
}

func TestCredentialModelDominantMetadataUpdatePreservesCompleteRemoteValues(t *testing.T) {
	const name = "model-metadata-update"
	remoteValues := map[string]interface{}{
		"api_key":        maskLiteLLMCredentialString("model-secret"),
		"model_value":    "server-selected",
		"external_token": maskLiteLLMCredentialString("external-secret"),
	}
	patched := false
	var valuesPatch map[string]interface{}
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPatch {
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			valuesPatch, _ = body["credential_values"].(map[string]interface{})
			patched = true
			_, _ = writer.Write([]byte(`{"success":true,"message":"updated"}`))
			return
		}
		env := "old"
		if patched {
			env = "new"
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"credential_name":   name,
			"credential_info":   map[string]interface{}{"env": env, "unmanaged": "keep"},
			"credential_values": remoteValues,
		})
	}))
	defer api.Close()

	schema := credentialTestSchema(t)
	stateModel := credentialTestModel(name, map[string]string{"env": "old"}, nil)
	stateModel.ModelID = types.StringValue("deployment/model")
	stateModel.CredentialValuesActive = types.BoolValue(false)
	stateModel.CredentialSource = types.StringValue("model_id")
	planModel := stateModel
	planModel.CredentialInfo = stringMapValue(map[string]string{"env": "new"})
	state := credentialTestState(t, schema, stateModel)
	response := &resource.UpdateResponse{State: state}
	(&CredentialResource{client: &Client{APIBase: api.URL, APIKey: "admin", HTTPClient: api.Client()}}).Update(
		context.Background(),
		resource.UpdateRequest{State: state, Plan: credentialTestPlan(t, schema, planModel), Config: credentialTestConfig(t, schema, planModel)},
		response,
	)
	if response.Diagnostics.HasError() {
		t.Fatalf("model-dominant metadata update: %v", response.Diagnostics)
	}
	if !reflect.DeepEqual(valuesPatch, map[string]interface{}{}) {
		t.Fatalf("model-dominant values PATCH=%#v want empty delta", valuesPatch)
	}
}

func TestCredentialUpdateRetriesAfterAcceptedMutation(t *testing.T) {
	const name = "accepted-mutation-retry"
	remote := credentialRemote{
		name:   name,
		info:   map[string]interface{}{"managed": "new", "unmanaged": "keep"},
		values: map[string]interface{}{"api_key": maskLiteLLMCredentialString("secret"), "model_value": "retain"},
	}
	patches := 0
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPatch {
			patches++
			_, _ = writer.Write([]byte(`{"success":true,"message":"updated"}`))
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"credential_name":   remote.name,
			"credential_info":   remote.info,
			"credential_values": remote.values,
		})
	}))
	defer api.Close()

	schema := credentialTestSchema(t)
	stateModel := credentialTestModel(name, map[string]string{"managed": "old"}, map[string]string{"api_key": "secret"})
	planModel := credentialTestModel(name, map[string]string{"managed": "new"}, map[string]string{"api_key": "secret"})
	state := credentialTestState(t, schema, stateModel)
	response := &resource.UpdateResponse{State: state}
	(&CredentialResource{client: &Client{APIBase: api.URL, APIKey: "admin", HTTPClient: api.Client()}}).Update(
		context.Background(),
		resource.UpdateRequest{State: state, Plan: credentialTestPlan(t, schema, planModel), Config: credentialTestConfig(t, schema, planModel)},
		response,
	)
	if response.Diagnostics.HasError() || patches != credentialPatchFanoutSize {
		t.Fatalf("accepted mutation retry diagnostics=%v patches=%d", response.Diagnostics, patches)
	}
	var updated CredentialResourceModel
	if diagnostics := response.State.Get(context.Background(), &updated); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	var info map[string]string
	if diagnostics := updated.CredentialInfo.ElementsAs(context.Background(), &info, false); diagnostics.HasError() || info["managed"] != "new" {
		t.Fatalf("retry did not record desired state: info=%v diagnostics=%v", info, diagnostics)
	}
}

func TestCredentialAcceptedMutationNestedRemovalClassification(t *testing.T) {
	const name = "accepted-nested-removal"
	priorInfo := map[string]interface{}{
		"nested": map[string]interface{}{
			"managed":  "keep",
			"removed":  "old",
			"external": "preserve",
		},
	}
	desiredInfo := map[string]interface{}{
		"nested": map[string]interface{}{
			"managed":  "keep",
			"external": "preserve",
		},
	}
	changedReappearance := map[string]interface{}{
		"nested": map[string]interface{}{
			"managed":  "keep",
			"removed":  "third",
			"external": "preserve",
		},
	}

	for _, test := range []struct {
		name                string
		preflightInfo       map[string]interface{}
		postflightInfo      []map[string]interface{}
		wantError           bool
		wantPatches         int
		wantRetainedRemoval bool
		wantWarning         bool
	}{
		{
			name:          "desired worker with removal already accepted converges",
			preflightInfo: desiredInfo,
			postflightInfo: []map[string]interface{}{
				desiredInfo,
			},
			wantPatches: credentialPatchFanoutSize,
		},
		{
			name:          "exact prior worker remains an allowed stale version",
			preflightInfo: priorInfo,
			postflightInfo: []map[string]interface{}{
				desiredInfo,
				priorInfo,
				desiredInfo,
				priorInfo,
			},
			wantPatches: credentialPatchFanoutSize,
			wantWarning: true,
		},
		{
			name:                "changed reappearance is third during retry preflight",
			preflightInfo:       changedReappearance,
			wantError:           true,
			wantRetainedRemoval: true,
		},
		{
			name:          "changed reappearance is third during postflight",
			preflightInfo: priorInfo,
			postflightInfo: []map[string]interface{}{
				desiredInfo,
				changedReappearance,
				desiredInfo,
				priorInfo,
			},
			wantError:           true,
			wantPatches:         credentialPatchFanoutSize,
			wantRetainedRemoval: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			getCalls := 0
			patches := 0
			var patchBodies []map[string]interface{}
			api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodPatch {
					patches++
					var body map[string]interface{}
					_ = json.NewDecoder(request.Body).Decode(&body)
					patchBodies = append(patchBodies, body)
					_, _ = writer.Write([]byte(`{"success":true,"message":"updated"}`))
					return
				}
				getCalls++
				info := test.preflightInfo
				if getCalls > credentialProbeSampleSize && len(test.postflightInfo) != 0 {
					info = test.postflightInfo[(getCalls-credentialProbeSampleSize-1)%len(test.postflightInfo)]
				}
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{
					"credential_name":   name,
					"credential_info":   info,
					"credential_values": map[string]interface{}{"api_key": maskLiteLLMCredentialString("secret"), "external_value": "preserve"},
				})
			}))
			defer api.Close()

			server, resourceType := credentialProtocolServer(t, api.URL)
			priorJSON := `{"nested":{"managed":"keep","removed":"old"}}`
			desiredJSON := `{"nested":{"managed":"keep"}}`
			values := credentialProtocolStringMap(map[string]string{"api_key": "secret"})
			nullMap := credentialProtocolMap(nil)
			prior := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, name, name, nil, nullMap, values, priorJSON, nil, true, "credential_values"))
			config := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, name, nil, nil, nullMap, values, desiredJSON, nil, nil, nil))
			planned := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, name, name, nil, nullMap, values, desiredJSON, nil, true, "credential_values"))

			priorModel := credentialTestModel(name, nil, map[string]string{"api_key": "secret"})
			priorModel.CredentialInfoJSON = types.StringValue(priorJSON)
			metadata, err := inferCredentialPrivateMetadata(context.Background(), priorModel)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := encodeCredentialPrivateMetadata(metadata)
			if err != nil {
				t.Fatal(err)
			}
			private, err := json.Marshal(map[string][]byte{credentialPrivateMetadataKey: encoded})
			if err != nil {
				t.Fatal(err)
			}

			apply, err := server.ApplyResourceChange(context.Background(), &tfprotov6.ApplyResourceChangeRequest{
				TypeName:       "litellm_credential",
				Config:         config,
				PriorState:     prior,
				PlannedState:   planned,
				PlannedPrivate: private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(apply.Diagnostics) != test.wantError {
				t.Fatalf("accepted removal retry: err=%v diagnostics=%v", err, apply.Diagnostics)
			}
			if patches != test.wantPatches {
				t.Fatalf("PATCH count=%d want=%d bodies=%#v diagnostics=%v", patches, test.wantPatches, patchBodies, apply.Diagnostics)
			}
			if test.wantWarning && !credentialProtocolDiagnosticsContain(apply.Diagnostics, "Worker Convergence") {
				t.Fatalf("stale prior worker lacked convergence warning: %v", apply.Diagnostics)
			}
			for _, body := range patchBodies {
				nested := body["credential_info"].(map[string]interface{})["nested"].(map[string]interface{})
				if _, exists := nested["removed"]; exists || nested["external"] != "preserve" {
					t.Fatalf("PATCH abandoned removal ownership or unmanaged sibling: %#v", body)
				}
			}
			resultMetadata := credentialProtocolPrivateMetadata(t, apply.Private)
			nestedOwnership := resultMetadata.JSONInfo.Children["nested"]
			_, ownsRemoved := nestedOwnership.Children["removed"]
			if ownsRemoved != test.wantRetainedRemoval {
				t.Fatalf("removed path ownership retained=%t want=%t metadata=%#v diagnostics=%v", ownsRemoved, test.wantRetainedRemoval, resultMetadata, apply.Diagnostics)
			}
		})
	}
}

func TestCredentialUpdateRejectsSerializedExceptionEvenWhenPostflightMatches(t *testing.T) {
	t.Parallel()
	patched := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPatch {
			patched = true
			_, _ = writer.Write([]byte(`{"status_code":404,"detail":"exception"}`))
			return
		}
		env := "old"
		if patched {
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

func TestCredentialTerminalDeleteValidatesBodyAndWarnsOnStalePresence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		body        string
		getStatus   int
		wantError   bool
		wantWarning bool
	}{
		{"success", `{"success":true,"message":"Credential deleted successfully"}`, http.StatusNotFound, false, true},
		{"serialized exception", `{"status_code":500,"detail":"exception"}`, http.StatusNotFound, true, false},
		{"stale worker remains", `{"success":true,"message":"Credential deleted successfully"}`, http.StatusOK, false, true},
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
			if response.Diagnostics.HasError() != test.wantError || credentialDiagnosticsContain(response.Diagnostics, "stale worker") != test.wantWarning {
				t.Fatalf("diagnostics=%v want error=%t warning=%t", response.Diagnostics, test.wantError, test.wantWarning)
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
		want := make([]string, credentialProbeSampleSize)
		for index := range want {
			want[index] = http.MethodGet + " " + credentialByNamePath(identity)
		}
		if !reflect.DeepEqual(requests, want) {
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

func TestCredentialProtocolImportedMetadataOwnershipTransition(t *testing.T) {
	for _, test := range []struct {
		name    string
		persist bool
	}{
		{name: "authoritative success", persist: true},
		{name: "postflight failure retains import", persist: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			const identity = "imported-metadata-transition"
			var mu sync.Mutex
			remoteInfo := map[string]interface{}{
				"owner":  "outside",
				"nested": map[string]interface{}{"enabled": true},
			}
			remoteValues := map[string]interface{}{
				"api_key":     maskLiteLLMCredentialString("remote-secret"),
				"credentials": map[string]interface{}{"token": maskLiteLLMCredentialString("nested-secret")},
			}
			patches := 0
			var valuesPatches []map[string]interface{}
			api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				writer.Header().Set("Content-Type", "application/json")
				switch request.Method {
				case http.MethodGet:
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{
						"credential_name":   identity,
						"credential_info":   remoteInfo,
						"credential_values": remoteValues,
					})
				case http.MethodPatch:
					patches++
					var body map[string]interface{}
					_ = json.NewDecoder(request.Body).Decode(&body)
					infoPatch, _ := body["credential_info"].(map[string]interface{})
					valuesPatch, _ := body["credential_values"].(map[string]interface{})
					valuesPatches = append(valuesPatches, valuesPatch)
					if test.persist {
						remoteInfo = shallowMergeCredentialObject(remoteInfo, infoPatch)
						remoteValues = shallowMergeCredentialObject(remoteValues, valuesPatch)
					}
					_, _ = writer.Write([]byte(`{"success":true,"message":"updated"}`))
				default:
					http.NotFound(writer, request)
				}
			}))
			defer api.Close()

			server, resourceType := credentialProtocolServer(t, api.URL)
			ctx := context.Background()
			importResponse, err := server.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{
				TypeName: "litellm_credential",
				ID:       identity,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(importResponse.Diagnostics) || len(importResponse.ImportedResources) != 1 {
				t.Fatalf("import: err=%v diagnostics=%v", err, importResponse.Diagnostics)
			}
			imported := importResponse.ImportedResources[0]
			read, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
				TypeName:     "litellm_credential",
				CurrentState: imported.State,
				Private:      imported.Private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("import read: err=%v diagnostics=%v", err, read.Diagnostics)
			}
			beforeMetadata := credentialProtocolPrivateMetadata(t, read.Private)
			if !beforeMetadata.Imported || beforeMetadata.ValuesUnowned || len(credentialMetadataOwnership(beforeMetadata, false).Children) != 0 || len(credentialMetadataOwnership(beforeMetadata, true).Children) != 0 {
				t.Fatalf("pre-transition import marker=%#v", beforeMetadata)
			}

			nullMap := credentialProtocolMap(nil)
			configuredInfo := credentialProtocolStringMap(map[string]string{"owner": "terraform"})
			config := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(
				resourceType, identity, nil, nil, configuredInfo, nullMap, nil, nil, nil, nil,
			))
			proposed := credentialProtocolReplace(t, resourceType, read.NewState, map[string]tftypes.Value{
				"credential_info": configuredInfo,
			})
			plan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName:         "litellm_credential",
				Config:           config,
				PriorState:       read.NewState,
				ProposedNewState: proposed,
				PriorPrivate:     read.Private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(plan.Diagnostics) {
				t.Fatalf("metadata ownership plan: err=%v diagnostics=%v", err, plan.Diagnostics)
			}
			apply, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName:       "litellm_credential",
				Config:         config,
				PriorState:     read.NewState,
				PlannedState:   plan.PlannedState,
				PlannedPrivate: plan.PlannedPrivate,
			})
			if err != nil {
				t.Fatalf("metadata ownership apply: %v", err)
			}
			if patches != credentialPatchFanoutSize {
				t.Fatalf("metadata ownership PATCH fan-out=%d", patches)
			}
			for _, valuesPatch := range valuesPatches {
				if !reflect.DeepEqual(valuesPatch, map[string]interface{}{}) {
					t.Fatalf("imported transition sent credential values: %#v", valuesPatch)
				}
			}

			if !test.persist {
				if !accessGroupProtocolDiagnosticsHaveError(apply.Diagnostics) || !credentialProtocolDiagnosticsContain(apply.Diagnostics, "Postflight Failed") {
					t.Fatalf("unpersisted PATCH was accepted: %v", apply.Diagnostics)
				}
				retained := credentialProtocolPrivateMetadata(t, apply.Private)
				if !retained.Imported || retained.ValuesUnowned || len(credentialMetadataOwnership(retained, false).Children) != 0 || len(credentialMetadataOwnership(retained, true).Children) != 0 {
					t.Fatalf("failed transition changed import ownership: %#v", retained)
				}
				return
			}

			if accessGroupProtocolDiagnosticsHaveError(apply.Diagnostics) {
				t.Fatalf("authoritative metadata transition: %v", apply.Diagnostics)
			}
			transitioned := credentialProtocolPrivateMetadata(t, apply.Private)
			if transitioned.Imported || !transitioned.ValuesUnowned || transitioned.AllRemoteOwned ||
				len(credentialMetadataOwnership(transitioned, false).Children) != 1 ||
				len(credentialMetadataOwnership(transitioned, true).Children) != 0 ||
				transitioned.LegacyValuesConfigured || transitioned.JSONValuesConfigured {
				t.Fatalf("invalid transitioned metadata: %#v", transitioned)
			}
			appliedValue, err := apply.NewState.Unmarshal(resourceType)
			if err != nil {
				t.Fatal(err)
			}
			var appliedAttributes map[string]tftypes.Value
			if err := appliedValue.As(&appliedAttributes); err != nil {
				t.Fatal(err)
			}
			for _, attribute := range []string{"credential_values", "credential_values_json", "model_id"} {
				if !appliedAttributes[attribute].IsKnown() || !appliedAttributes[attribute].IsNull() {
					t.Fatalf("transition adopted %s: %s", attribute, appliedAttributes[attribute])
				}
			}
			var source string
			if err := appliedAttributes["credential_source"].As(&source); err != nil || source != "imported" {
				t.Fatalf("transitioned source=%q err=%v", source, err)
			}

			refreshed, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
				TypeName:     "litellm_credential",
				CurrentState: apply.NewState,
				Private:      apply.Private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
				t.Fatalf("transition refresh: err=%v diagnostics=%v", err, refreshed.Diagnostics)
			}
			noDrift, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName:         "litellm_credential",
				Config:           config,
				PriorState:       refreshed.NewState,
				ProposedNewState: refreshed.NewState,
				PriorPrivate:     refreshed.Private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(noDrift.Diagnostics) {
				t.Fatalf("transition no-drift plan: err=%v diagnostics=%v", err, noDrift.Diagnostics)
			}

			updatedInfo := credentialProtocolStringMap(map[string]string{"owner": "terraform-next"})
			updatedConfig := credentialProtocolReplace(t, resourceType, config, map[string]tftypes.Value{"credential_info": updatedInfo})
			updatedProposed := credentialProtocolReplace(t, resourceType, refreshed.NewState, map[string]tftypes.Value{"credential_info": updatedInfo})
			updatedPlan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName:         "litellm_credential",
				Config:           updatedConfig,
				PriorState:       refreshed.NewState,
				ProposedNewState: updatedProposed,
				PriorPrivate:     refreshed.Private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(updatedPlan.Diagnostics) {
				t.Fatalf("transitioned metadata update plan: err=%v diagnostics=%v", err, updatedPlan.Diagnostics)
			}
			updatedApply, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName:       "litellm_credential",
				Config:         updatedConfig,
				PriorState:     refreshed.NewState,
				PlannedState:   updatedPlan.PlannedState,
				PlannedPrivate: updatedPlan.PlannedPrivate,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(updatedApply.Diagnostics) {
				t.Fatalf("transitioned metadata update: err=%v diagnostics=%v", err, updatedApply.Diagnostics)
			}
			updatedMetadata := credentialProtocolPrivateMetadata(t, updatedApply.Private)
			if updatedMetadata.Imported || !updatedMetadata.ValuesUnowned || len(credentialMetadataOwnership(updatedMetadata, true).Children) != 0 {
				t.Fatalf("subsequent update adopted secrets: %#v", updatedMetadata)
			}

			adoptionConfig := credentialProtocolReplace(t, resourceType, updatedConfig, map[string]tftypes.Value{
				"credential_values": credentialProtocolStringMap(map[string]string{"api_key": "new-secret"}),
			})
			adoptionProposed := credentialProtocolReplace(t, resourceType, updatedApply.NewState, map[string]tftypes.Value{
				"credential_values": credentialProtocolStringMap(map[string]string{"api_key": "new-secret"}),
			})
			adoptionPlan, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName:         "litellm_credential",
				Config:           adoptionConfig,
				PriorState:       updatedApply.NewState,
				ProposedNewState: adoptionProposed,
				PriorPrivate:     updatedApply.Private,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(adoptionPlan.Diagnostics) {
				t.Fatalf("transitioned secret adoption was not rejected: err=%v diagnostics=%v", err, adoptionPlan.Diagnostics)
			}
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

func TestCredentialMixedReadRequiresEveryPresentVersionToMatchPrior(t *testing.T) {
	t.Parallel()
	state := credentialTestModel("mixed-read", map[string]string{"env": "old"}, map[string]string{"api_key": "secret"})
	metadata, err := inferCredentialPrivateMetadata(context.Background(), state)
	if err != nil {
		t.Fatal(err)
	}
	priorInfo, err := credentialOwnedObjectFromSurfaces(context.Background(), state.CredentialInfo, state.CredentialInfoJSON, metadata.LegacyInfo, metadata.JSONInfo)
	if err != nil {
		t.Fatal(err)
	}
	priorValues, err := credentialOwnedObjectFromSurfaces(context.Background(), state.CredentialValues, state.CredentialValuesJSON, metadata.LegacyValues, metadata.JSONValues)
	if err != nil {
		t.Fatal(err)
	}
	exact := credentialWorkerRemote("mixed-read", map[string]interface{}{"env": "old"})
	third := credentialWorkerRemote("mixed-read", map[string]interface{}{"env": "third"})
	matches := credentialMatchingRemotes([]credentialRemote{*exact, *third}, func(remote credentialRemote) bool {
		return credentialRemoteMatchesOwnedState(remote, priorInfo, priorValues, metadata)
	})
	if len(matches) != 1 {
		t.Fatalf("mixed read prior matching classified %d versions, want exactly one; a warning requires every present version to match", len(matches))
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

func TestCredentialDataSourceRejectsConflictingPresentWorkerVersions(t *testing.T) {
	const name = "data-conflict"
	api, _ := newCredentialAlternatingWorkerAPI(t, [2]*credentialRemote{
		credentialWorkerRemote(name, map[string]interface{}{"env": "one"}),
		credentialWorkerRemote(name, map[string]interface{}{"env": "two"}),
	})
	ctx := context.Background()
	var schemaResponse datasource.SchemaResponse
	(&CredentialDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	config := CredentialDataSourceModel{
		ID:                 types.StringNull(),
		CredentialName:     types.StringValue(name),
		ModelID:            types.StringNull(),
		CredentialInfo:     types.MapUnknown(types.StringType),
		CredentialInfoJSON: types.StringUnknown(),
	}
	configState := tfsdk.State{Raw: tftypes.NewValue(schemaResponse.Schema.Type().TerraformType(ctx), nil), Schema: schemaResponse.Schema}
	if diagnostics := configState.Set(ctx, &config); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	response := &datasource.ReadResponse{State: tfsdk.State{Raw: configState.Raw, Schema: schemaResponse.Schema}}
	(&CredentialDataSource{client: &Client{APIBase: api.URL, APIKey: "admin", HTTPClient: api.Client()}}).Read(
		ctx,
		datasource.ReadRequest{Config: tfsdk.Config{Raw: configState.Raw, Schema: schemaResponse.Schema}},
		response,
	)
	if !response.Diagnostics.HasError() || !credentialDiagnosticsContain(response.Diagnostics, "Worker Convergence") {
		t.Fatalf("data source selected an arbitrary conflicting version: %v", response.Diagnostics)
	}
}

func TestCredentialAlternatingWorkersCreateDataSourceAndPlanReadRetainsIdentity(t *testing.T) {
	// The connection-bound worker assignment models a load balancer whose
	// backend selection changes only when the client opens a new connection.
	api, cluster := newCredentialAlternatingWorkerAPI(t, [2]*credentialRemote{})
	client := &Client{APIBase: api.URL, APIKey: "admin", HTTPClient: api.Client()}
	ctx := context.Background()

	resourceSchema := credentialTestSchema(t)
	model := credentialTestModel("alternating", map[string]string{"owner": "terraform"}, map[string]string{"api_key": "secret"})
	model.ID = types.StringUnknown()
	createPlan := credentialTestPlan(t, resourceSchema, model)
	createResponse := &resource.CreateResponse{State: tfsdk.State{Raw: createPlan.Raw, Schema: resourceSchema}}
	(&CredentialResource{client: client}).Create(ctx, resource.CreateRequest{Plan: createPlan}, createResponse)
	if createResponse.Diagnostics.HasError() || !credentialDiagnosticsContain(createResponse.Diagnostics, "Worker Convergence") {
		t.Fatalf("create did not retain sampled matching state with a convergence warning: %v", createResponse.Diagnostics)
	}

	var dataSchemaResponse datasource.SchemaResponse
	(&CredentialDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &dataSchemaResponse)
	dataConfig := CredentialDataSourceModel{
		ID:                 types.StringNull(),
		CredentialName:     types.StringValue("alternating"),
		ModelID:            types.StringNull(),
		CredentialInfo:     types.MapUnknown(types.StringType),
		CredentialInfoJSON: types.StringUnknown(),
	}
	dataConfigState := tfsdk.State{Raw: tftypes.NewValue(dataSchemaResponse.Schema.Type().TerraformType(ctx), nil), Schema: dataSchemaResponse.Schema}
	if diagnostics := dataConfigState.Set(ctx, &dataConfig); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	dataResponse := &datasource.ReadResponse{State: tfsdk.State{Raw: dataConfigState.Raw, Schema: dataSchemaResponse.Schema}}
	(&CredentialDataSource{client: client}).Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Raw: dataConfigState.Raw, Schema: dataSchemaResponse.Schema}}, dataResponse)
	if dataResponse.Diagnostics.HasError() || !credentialDiagnosticsContain(dataResponse.Diagnostics, "Worker Convergence") {
		t.Fatalf("data source did not use sampled matching state with a convergence warning: %v", dataResponse.Diagnostics)
	}

	readResponse := &resource.ReadResponse{State: createResponse.State}
	(&CredentialResource{client: client}).Read(ctx, resource.ReadRequest{State: createResponse.State}, readResponse)
	if readResponse.Diagnostics.HasError() || !credentialDiagnosticsContain(readResponse.Diagnostics, "Worker Convergence") {
		t.Fatalf("plan refresh did not retain prior state with a mixed-cache warning: %v", readResponse.Diagnostics)
	}
	var retained CredentialResourceModel
	if diagnostics := readResponse.State.Get(ctx, &retained); diagnostics.HasError() || retained.ID.ValueString() != "alternating" {
		t.Fatalf("one worker 404 removed identity: state=%#v diagnostics=%v", retained, diagnostics)
	}

	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if cluster.freshGETs != 4*credentialProbeSampleSize || cluster.nonFreshGETs != 0 || len(cluster.connectionWorkers) < cluster.freshGETs {
		t.Fatalf("credential probes were keepalive-pinned: fresh=%d nonfresh=%d connections=%d", cluster.freshGETs, cluster.nonFreshGETs, len(cluster.connectionWorkers))
	}
}

func TestCredentialLiveAlternatingApplyImmediatePlanDestroySequence(t *testing.T) {
	const name = "live-sequence"
	api, cluster := newCredentialAlternatingWorkerAPI(t, [2]*credentialRemote{})
	client := &Client{APIBase: api.URL, APIKey: "admin", HTTPClient: api.Client()}
	ctx := context.Background()
	schema := credentialTestSchema(t)
	planned := credentialTestModel(name, map[string]string{"owner": "terraform"}, map[string]string{"api_key": "secret"})
	planned.ID = types.StringUnknown()
	plan := credentialTestPlan(t, schema, planned)

	create := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	(&CredentialResource{client: client}).Create(ctx, resource.CreateRequest{Plan: plan}, create)
	if create.Diagnostics.HasError() || !credentialDiagnosticsContain(create.Diagnostics, "Worker Convergence") {
		t.Fatalf("live create did not accept matching presence plus 404: %v", create.Diagnostics)
	}

	read := &resource.ReadResponse{State: create.State}
	(&CredentialResource{client: client}).Read(ctx, resource.ReadRequest{State: create.State}, read)
	if read.Diagnostics.HasError() || !credentialDiagnosticsContain(read.Diagnostics, "Worker Convergence") {
		t.Fatalf("immediate no-drift refresh failed on matching presence plus 404: %v", read.Diagnostics)
	}
	var refreshed CredentialResourceModel
	if diagnostics := read.State.Get(ctx, &refreshed); diagnostics.HasError() || refreshed.ID.ValueString() != name {
		t.Fatalf("immediate refresh lost the created identity: state=%#v diagnostics=%v", refreshed, diagnostics)
	}

	// A later Terraform invocation starts with a new connection and may route
	// DELETE to the worker whose credential_list never observed create.
	client.HTTPClient.CloseIdleConnections()
	deleted := &resource.DeleteResponse{}
	(&CredentialResource{client: client}).Delete(ctx, resource.DeleteRequest{State: read.State}, deleted)
	if deleted.Diagnostics.HasError() || !credentialDiagnosticsContain(deleted.Diagnostics, "stale worker") {
		t.Fatalf("terminal destroy did not report durable deletion plus stale-cache risk: %v", deleted.Diagnostics)
	}

	wantRequests := make([]string, 0, 4*credentialProbeSampleSize+2)
	for range credentialProbeSampleSize {
		wantRequests = append(wantRequests, http.MethodGet+" "+credentialByNamePath(name))
	}
	wantRequests = append(wantRequests, http.MethodPost+" /credentials")
	for range 2 * credentialProbeSampleSize {
		wantRequests = append(wantRequests, http.MethodGet+" "+credentialByNamePath(name))
	}
	wantRequests = append(wantRequests, http.MethodDelete+" "+credentialMutationPath(name))
	for range credentialProbeSampleSize {
		wantRequests = append(wantRequests, http.MethodGet+" "+credentialByNamePath(name))
	}

	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if !reflect.DeepEqual(cluster.requests, wantRequests) {
		t.Fatalf("live lifecycle request sequence=%v want=%v", cluster.requests, wantRequests)
	}
	if cluster.posts != 1 || cluster.deletes != 1 || cluster.freshDELETEs != 0 || cluster.nonFreshGETs != 0 || cluster.workers[0] == nil || cluster.workers[1] != nil {
		t.Fatalf("live lifecycle did not preserve the durable-delete/stale-cache contract: posts=%d deletes=%d fresh_deletes=%d nonfresh_gets=%d workers=%#v", cluster.posts, cluster.deletes, cluster.freshDELETEs, cluster.nonFreshGETs, cluster.workers)
	}
}

func TestCredentialAlternatingWorkersTerminalDeleteWarnsAndRemovesState(t *testing.T) {
	name := "stale-delete"
	api, cluster := newCredentialAlternatingWorkerAPI(t, [2]*credentialRemote{
		credentialWorkerRemote(name, map[string]interface{}{"env": "old"}),
		credentialWorkerRemote(name, map[string]interface{}{"env": "old"}),
	})
	server, resourceType := credentialProtocolServer(t, api.URL)
	ctx := context.Background()
	info := credentialProtocolStringMap(map[string]string{"env": "old"})
	values := credentialProtocolStringMap(map[string]string{"api_key": "secret"})
	prior := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, name, name, nil, info, values, nil, nil, true, "credential_values"))
	nullState := credentialProtocolDynamicValue(t, resourceType, tftypes.NewValue(resourceType, nil))

	destroy, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     "litellm_credential",
		Config:       nullState,
		PriorState:   prior,
		PlannedState: nullState,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroy.Diagnostics) || !credentialProtocolDiagnosticsContain(destroy.Diagnostics, "stale worker") || destroy.NewState == nil {
		t.Fatalf("terminal durable delete did not warn and remove state: err=%v diagnostics=%v state=%v", err, destroy.Diagnostics, destroy.NewState)
	}
	removed, unmarshalErr := destroy.NewState.Unmarshal(resourceType)
	if unmarshalErr != nil || !removed.IsNull() {
		t.Fatalf("confirmed delete retained Terraform state: value=%v err=%v", removed, unmarshalErr)
	}
	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if cluster.workers[0] != nil || cluster.workers[1] == nil || cluster.deletes != 1 || cluster.freshDELETEs != 0 || cluster.freshGETs != credentialProbeSampleSize || cluster.nonFreshGETs != 0 {
		t.Fatalf("terminal delete did not preserve the explicit stale-cache contract: workers=%#v deletes=%d fresh_deletes=%d fresh_gets=%d nonfresh_gets=%d", cluster.workers, cluster.deletes, cluster.freshDELETEs, cluster.freshGETs, cluster.nonFreshGETs)
	}
}

func TestCredentialReplacementDeleteBlocksWhileAnyWorkerStillServesData(t *testing.T) {
	const name = "replacement-stale-delete"
	deletes := 0
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			_, _ = writer.Write([]byte(`{"credential_name":"replacement-stale-delete","credential_info":{"env":"old"},"credential_values":{"api_key":"se****et"}}`))
		case http.MethodDelete:
			deletes++
			// The durable row is deleted, but this worker intentionally retains
			// the old process-local credential_list entry.
			_, _ = writer.Write([]byte(`{"success":true,"message":"Credential deleted successfully"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer api.Close()
	server, resourceType := credentialProtocolServer(t, api.URL)
	model := credentialTestModel(name, map[string]string{"env": "old"}, map[string]string{"api_key": "secret"})
	metadata, err := inferCredentialPrivateMetadata(context.Background(), model)
	if err != nil {
		t.Fatal(err)
	}
	metadata.AllRemoteOwned = true
	metadata.ReplacementPending = true
	encoded, err := encodeCredentialPrivateMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	private, err := json.Marshal(map[string][]byte{credentialPrivateMetadataKey: encoded})
	if err != nil {
		t.Fatal(err)
	}
	prior := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(
		resourceType,
		name,
		name,
		nil,
		credentialProtocolStringMap(map[string]string{"env": "old"}),
		credentialProtocolStringMap(map[string]string{"api_key": "secret"}),
		nil,
		nil,
		true,
		"credential_values",
	))
	nullState := credentialProtocolDynamicValue(t, resourceType, tftypes.NewValue(resourceType, nil))
	destroy, err := server.ApplyResourceChange(context.Background(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName:       "litellm_credential",
		Config:         nullState,
		PriorState:     prior,
		PlannedState:   nullState,
		PlannedPrivate: private,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(destroy.Diagnostics) || !credentialProtocolDiagnosticsContain(destroy.Diagnostics, "blocked recreation/adoption") || deletes != 1 {
		t.Fatalf("replacement stale-cache delete was not blocked: err=%v diagnostics=%v deletes=%d", err, destroy.Diagnostics, deletes)
	}
	retained, decodeErr := destroy.NewState.Unmarshal(resourceType)
	if decodeErr != nil || retained.IsNull() {
		t.Fatalf("replacement failure did not retain state: value=%v err=%v", retained, decodeErr)
	}
}

func TestCredentialAlternatingWorkersMixedUpdateWarnsAndUsesMatchingVersion(t *testing.T) {
	name := "mixed-update"
	api, cluster := newCredentialAlternatingWorkerAPI(t, [2]*credentialRemote{
		credentialWorkerRemote(name, map[string]interface{}{"env": "old"}),
		credentialWorkerRemote(name, map[string]interface{}{"env": "old"}),
	})
	client := &Client{APIBase: api.URL, APIKey: "admin", HTTPClient: api.Client()}
	schema := credentialTestSchema(t)
	stateModel := credentialTestModel(name, map[string]string{"env": "old"}, map[string]string{"api_key": "secret"})
	planModel := credentialTestModel(name, map[string]string{"env": "new"}, map[string]string{"api_key": "secret"})
	state := credentialTestState(t, schema, stateModel)
	plan := credentialTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}

	(&CredentialResource{client: client}).Update(context.Background(), resource.UpdateRequest{
		State:  state,
		Plan:   plan,
		Config: credentialTestConfig(t, schema, planModel),
	}, response)
	if response.Diagnostics.HasError() || credentialDiagnosticsContain(response.Diagnostics, "Worker Convergence") {
		t.Fatalf("PATCH fan-out did not converge alternating present workers: %v", response.Diagnostics)
	}
	var updated CredentialResourceModel
	if diagnostics := response.State.Get(context.Background(), &updated); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	var infoValues map[string]string
	if diagnostics := updated.CredentialInfo.ElementsAs(context.Background(), &infoValues, false); diagnostics.HasError() || infoValues["env"] != "new" {
		t.Fatalf("matching updated worker state was not recorded: info=%v diagnostics=%v", infoValues, diagnostics)
	}

	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if cluster.workers[0].info["env"] != "new" || cluster.workers[1].info["env"] != "new" || cluster.freshPATCHes != credentialPatchFanoutSize || cluster.freshGETs != 2*credentialProbeSampleSize {
		t.Fatalf("fan-out did not converge alternating worker versions: workers=%#v patches=%d fresh=%d", cluster.workers, cluster.freshPATCHes, cluster.freshGETs)
	}
}

func TestCredentialAlternatingWorkersUpdateAcceptsPriorExactPlus404(t *testing.T) {
	const name = "mixed-update-missing"
	api, cluster := newCredentialAlternatingWorkerAPI(t, [2]*credentialRemote{
		credentialWorkerRemote(name, map[string]interface{}{"env": "old"}),
		nil,
	})
	client := &Client{APIBase: api.URL, APIKey: "admin", HTTPClient: api.Client()}
	schema := credentialTestSchema(t)
	stateModel := credentialTestModel(name, map[string]string{"env": "old"}, map[string]string{"api_key": "secret"})
	planModel := credentialTestModel(name, map[string]string{"env": "new"}, map[string]string{"api_key": "secret"})
	state := credentialTestState(t, schema, stateModel)
	response := &resource.UpdateResponse{State: state}

	(&CredentialResource{client: client}).Update(context.Background(), resource.UpdateRequest{
		State:  state,
		Plan:   credentialTestPlan(t, schema, planModel),
		Config: credentialTestConfig(t, schema, planModel),
	}, response)
	if response.Diagnostics.HasError() || !credentialDiagnosticsContain(response.Diagnostics, "Worker Convergence") {
		t.Fatalf("prior-exact plus 404 update was not verified with warning: %v", response.Diagnostics)
	}
	var updated CredentialResourceModel
	if diagnostics := response.State.Get(context.Background(), &updated); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	var info map[string]string
	if diagnostics := updated.CredentialInfo.ElementsAs(context.Background(), &info, false); diagnostics.HasError() || info["env"] != "new" {
		t.Fatalf("verified desired state was not recorded: info=%v diagnostics=%v", info, diagnostics)
	}

	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if cluster.workers[0] == nil || cluster.workers[0].info["env"] != "new" || cluster.workers[1] != nil || cluster.freshPATCHes != credentialPatchFanoutSize {
		t.Fatalf("PATCH fan-out did not preserve missing-cache semantics: workers=%#v patches=%d", cluster.workers, cluster.freshPATCHes)
	}
}

func TestCredentialAlternatingWorkersUpdateRejectsThirdVersion(t *testing.T) {
	const name = "third-update-version"
	api, cluster := newCredentialAlternatingWorkerAPI(t, [2]*credentialRemote{
		credentialWorkerRemote(name, map[string]interface{}{"env": "old"}),
		credentialWorkerRemote(name, map[string]interface{}{"env": "old"}),
	})
	conflictWorker := 1
	cluster.conflictPatchWorker = &conflictWorker
	client := &Client{APIBase: api.URL, APIKey: "admin", HTTPClient: api.Client()}
	schema := credentialTestSchema(t)
	stateModel := credentialTestModel(name, map[string]string{"env": "old"}, map[string]string{"api_key": "secret"})
	planModel := credentialTestModel(name, map[string]string{"env": "new"}, map[string]string{"api_key": "secret"})
	state := credentialTestState(t, schema, stateModel)
	response := &resource.UpdateResponse{State: state}

	(&CredentialResource{client: client}).Update(context.Background(), resource.UpdateRequest{
		State:  state,
		Plan:   credentialTestPlan(t, schema, planModel),
		Config: credentialTestConfig(t, schema, planModel),
	}, response)
	if !response.Diagnostics.HasError() || !credentialDiagnosticsContain(response.Diagnostics, "Postflight Failed") {
		t.Fatalf("desired plus third worker version was accepted: %v", response.Diagnostics)
	}
	var retained CredentialResourceModel
	if diagnostics := response.State.Get(context.Background(), &retained); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	var info map[string]string
	if diagnostics := retained.CredentialInfo.ElementsAs(context.Background(), &info, false); diagnostics.HasError() || info["env"] != "old" {
		t.Fatalf("conflicting update did not retain prior state: info=%v diagnostics=%v", info, diagnostics)
	}
}

func TestCredentialAll404RemoteDeletionRequiresCompleteFreshSample(t *testing.T) {
	api, cluster := newCredentialAlternatingWorkerAPI(t, [2]*credentialRemote{})
	server, resourceType := credentialProtocolServer(t, api.URL)
	ctx := context.Background()
	name := "remote-delete"
	info := credentialProtocolStringMap(map[string]string{"env": "old"})
	values := credentialProtocolStringMap(map[string]string{"api_key": "secret"})
	prior := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, name, name, nil, info, values, nil, nil, true, "credential_values"))

	readResponse, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_credential", CurrentState: prior})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(readResponse.Diagnostics) || readResponse.NewState == nil {
		t.Fatalf("all-404 refresh failed: err=%v diagnostics=%v state=%v", err, readResponse.Diagnostics, readResponse.NewState)
	}
	removed, unmarshalErr := readResponse.NewState.Unmarshal(resourceType)
	if unmarshalErr != nil || !removed.IsNull() {
		t.Fatalf("four consecutive all-404 probes did not remove state: value=%v err=%v", removed, unmarshalErr)
	}
	cluster.mu.Lock()
	defer cluster.mu.Unlock()
	if cluster.freshGETs != credentialProbeSampleSize || cluster.nonFreshGETs != 0 {
		t.Fatalf("remote deletion sample was not bounded/fresh: fresh=%d nonfresh=%d", cluster.freshGETs, cluster.nonFreshGETs)
	}
}

func TestCredentialSamplingTerminalErrorsRetainStateWithoutRetry(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "terminal HTTP response", status: http.StatusBadRequest, body: `{"detail":"invalid request"}`},
		{name: "identity mismatch", status: http.StatusOK, body: `{"credential_name":"other","credential_info":{},"credential_values":{}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			requests := 0
			api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests++
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer api.Close()
			server, resourceType := credentialProtocolServer(t, api.URL)
			ctx := context.Background()
			name := "terminal"
			info := credentialProtocolStringMap(map[string]string{"env": "old"})
			values := credentialProtocolStringMap(map[string]string{"api_key": "secret"})
			prior := credentialProtocolDynamicValue(t, resourceType, credentialProtocolValue(resourceType, name, name, nil, info, values, nil, nil, true, "credential_values"))

			readResponse, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_credential", CurrentState: prior})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(readResponse.Diagnostics) || readResponse.NewState == nil || requests != 1 {
				t.Fatalf("terminal sampling did not stop and retain state: err=%v diagnostics=%v requests=%d state=%v", err, readResponse.Diagnostics, requests, readResponse.NewState)
			}
			retained, unmarshalErr := readResponse.NewState.Unmarshal(resourceType)
			if unmarshalErr != nil || retained.IsNull() {
				t.Fatalf("terminal probe removed state: value=%v err=%v", retained, unmarshalErr)
			}
		})
	}
}

func TestCredentialSamplingRetriesTransientWithoutCountingItAsAbsence(t *testing.T) {
	requests := 0
	fresh := 0
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.Close {
			fresh++
		}
		writer.Header().Set("Content-Type", "application/json")
		if requests == 2 {
			http.Error(writer, `{"detail":"busy"}`, http.StatusServiceUnavailable)
			return
		}
		http.NotFound(writer, request)
	}))
	defer api.Close()

	sample, err := probeCredentialEndpoint(context.Background(), &Client{APIBase: api.URL, APIKey: "admin", HTTPClient: api.Client()}, credentialByNamePath("transient"), "transient")
	if err != nil || !sample.authoritativeAbsence() || sample.transient != 1 || requests != 6 || fresh != requests {
		t.Fatalf("transient counted as absence or was not retried: sample=%#v err=%v requests=%d fresh=%d", sample, err, requests, fresh)
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
	if !response.Diagnostics.HasError() || posts != 1 || gets != 2*credentialProbeSampleSize {
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
