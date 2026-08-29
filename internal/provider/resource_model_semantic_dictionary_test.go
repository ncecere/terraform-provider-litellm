package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func modelStringMap(values map[string]string) types.Map {
	elements := make(map[string]attr.Value, len(values))
	for key, value := range values {
		elements[key] = types.StringValue(value)
	}
	return types.MapValueMust(types.StringType, elements)
}

func TestModelAdditionalModelInfoJSONSchemaIsAdditiveAndSensitive(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&ModelResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	attribute, ok := response.Schema.Attributes["additional_model_info_json"].(resourceschema.StringAttribute)
	if !ok || !attribute.Optional || !attribute.Computed || !attribute.Sensitive {
		t.Fatalf("additional_model_info_json schema = %#v", response.Schema.Attributes["additional_model_info_json"])
	}
	legacy, ok := response.Schema.Attributes["additional_model_info"].(resourceschema.MapAttribute)
	if !ok || legacy.ElementType != types.StringType {
		t.Fatalf("legacy additional_model_info schema changed: %#v", response.Schema.Attributes["additional_model_info"])
	}
	for _, key := range reservedAdditionalModelInfoKeys {
		if key == "input_cost_per_token" || key == "output_cost_per_token" {
			t.Fatalf("legacy map validation was broadened in place: %#v", reservedAdditionalModelInfoKeys)
		}
	}
	if _, present := response.Schema.Attributes["model_max_budget_json"]; present {
		t.Fatal("model-budget behavior leaked into issue 218 model-info slice")
	}

	var singularResponse datasource.SchemaResponse
	(&ModelDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &singularResponse)
	if _, present := singularResponse.Schema.Attributes["additional_model_info_json"]; present {
		t.Fatal("resource-owned JSON leaked into singular data source")
	}
	var listResponse datasource.SchemaResponse
	(&ModelsListDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &listResponse)
	if _, present := listResponse.Schema.Attributes["additional_model_info_json"]; present {
		t.Fatal("resource-owned JSON leaked into list data source")
	}
}

func TestModelAdditionalModelInfoJSONPreservesNativeIdentity(t *testing.T) {
	t.Parallel()

	raw := `{ "string_false":"false", "native_false":false, "leading":"001", "large":9007199254740993, "decimal":0.1, "nested":{"nullable":null,"items":[1,true,"1"]} }`
	object, provenance, err := modelAdditionalModelInfoJSONConfiguration(context.Background(), types.StringValue(raw), types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	if !provenance.Configured || len(provenance.TerraformOwned) != 7 {
		t.Fatalf("provenance = %#v", provenance)
	}
	nested := object["nested"].(map[string]interface{})
	if object["string_false"] != "false" || object["native_false"] != false || object["leading"] != "001" || nested["nullable"] != nil {
		t.Fatalf("scalar identity changed: %#v", object)
	}
	large, ok := object["large"].(json.Number)
	if !ok || large.String() != "9007199254740993" {
		t.Fatalf("large number = %#v", object["large"])
	}
	decimal, ok := object["decimal"].(json.Number)
	if !ok || decimal.String() != "0.1" {
		t.Fatalf("decimal = %#v", object["decimal"])
	}
	canonical, err := canonicalSemanticDictionary(context.Background(), object)
	if err != nil || !strings.Contains(canonical, `"large":9007199254740993`) || !strings.Contains(canonical, `"decimal":0.1`) {
		t.Fatalf("canonical = %q, %v", canonical, err)
	}
	for _, rejectedRaw := range []string{`{"close":1.0000000000000001}`, `{"top_level_null":null}`} {
		if rejected, rejectedProvenance, err := modelAdditionalModelInfoJSONConfiguration(context.Background(), types.StringValue(rejectedRaw), types.MapNull(types.StringType)); err == nil || rejected != nil || rejectedProvenance.Initialized {
			t.Fatalf("non-persistent value returned %#v %#v %v", rejected, rejectedProvenance, err)
		}
	}
}

func TestModelAdditionalModelInfoJSONOverlapErrorsAreContentFree(t *testing.T) {
	t.Parallel()

	secretKey := "private-sensitive-key"
	for name, test := range map[string]struct {
		legacy types.Map
		raw    string
	}{
		"legacy":        {legacy: modelStringMap(map[string]string{secretKey: "private-value"}), raw: `{"private-sensitive-key":true}`},
		"reserved":      {legacy: types.MapNull(types.StringType), raw: `{"base_model":"private-value"}`},
		"mirrored cost": {legacy: types.MapNull(types.StringType), raw: `{"input_cost_per_token":1}`},
	} {
		t.Run(name, func(t *testing.T) {
			object, provenance, err := modelAdditionalModelInfoJSONConfiguration(context.Background(), types.StringValue(test.raw), test.legacy)
			if err == nil || object != nil || provenance.Initialized {
				t.Fatalf("unsafe overlap returned %#v %#v %v", object, provenance, err)
			}
			if strings.Contains(err.Error(), secretKey) || strings.Contains(err.Error(), "base_model") || strings.Contains(err.Error(), "input_cost_per_token") || strings.Contains(err.Error(), "private-value") {
				t.Fatalf("overlap error exposed content: %v", err)
			}
		})
	}
}

func TestModelAdditionalModelInfoJSONReplacementContract(t *testing.T) {
	t.Parallel()

	state := types.StringValue(`{"nested":{"a":1,"b":2}}`)
	_, provenance, err := modelAdditionalModelInfoJSONConfiguration(context.Background(), state, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		config types.String
		want   bool
	}{
		{name: "same semantics", config: types.StringValue(`{ "nested" : { "b":2, "a":1 } }`)},
		{name: "value change", config: types.StringValue(`{"nested":{"a":2,"b":2}}`), want: true},
		{name: "nested removal", config: types.StringValue(`{"nested":{"a":1}}`), want: true},
		{name: "explicit empty", config: types.StringValue(`{}`), want: true},
		{name: "null removal", config: types.StringNull(), want: true},
		{name: "unknown", config: types.StringUnknown(), want: true},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := modelAdditionalModelInfoJSONNeedsReplacement(context.Background(), test.config, state, provenance)
			if err != nil || got != test.want {
				t.Fatalf("replacement = %v, %v; want %v", got, err, test.want)
			}
		})
	}
	unconfigured := modelUnconfiguredSemanticDictionaryProvenance()
	if replace, err := modelAdditionalModelInfoJSONNeedsReplacement(context.Background(), types.StringValue(`{}`), types.StringNull(), unconfigured); err != nil || !replace {
		t.Fatalf("initial takeover replacement = %v, %v", replace, err)
	}
}

func TestModelAdditionalModelInfoJSONProjectionIsExactAndAtomic(t *testing.T) {
	t.Parallel()

	configured := types.StringValue(`{ "nested":{"owned":1}, "empty":{}, "list":[1,true], "literal":"****" }`)
	_, provenance, err := modelAdditionalModelInfoJSONConfiguration(context.Background(), configured, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	observed := mustParseSemanticDictionary(t, `{"nested":{"owned":1,"api":"ignored"},"empty":{},"list":[1,true],"literal":"****","derived":{"ignored":true}}`)
	reconciled, err := reconcileModelAdditionalModelInfoJSON(context.Background(), configured, observed, provenance)
	if err != nil || !reconciled.Equal(configured) {
		t.Fatalf("reconciled = %#v, %v; want configured spelling", reconciled, err)
	}

	drifted := mustParseSemanticDictionary(t, `{"nested":{"owned":2,"api":"ignored"},"empty":{},"list":[1,true],"literal":"****"}`)
	reconciled, err = reconcileModelAdditionalModelInfoJSON(context.Background(), configured, drifted, provenance)
	if err != nil || !strings.Contains(reconciled.ValueString(), `"owned":2`) || strings.Contains(reconciled.ValueString(), "api") {
		t.Fatalf("drifted projection = %q, %v", reconciled.ValueString(), err)
	}

	missing := mustParseSemanticDictionary(t, `{"nested":{},"empty":{},"list":[1,true],"literal":"****"}`)
	if partial, err := projectModelAdditionalModelInfoJSON(context.Background(), missing, provenance); err == nil || partial != nil {
		t.Fatalf("missing owned leaf returned %#v, %v", partial, err)
	}
	typeChanged := mustParseSemanticDictionary(t, `{"nested":{"owned":{"new":"shape"}},"empty":{},"list":[1,true],"literal":"****"}`)
	if partial, err := projectModelAdditionalModelInfoJSON(context.Background(), typeChanged, provenance); err == nil || partial != nil {
		t.Fatalf("owned type change returned %#v, %v", partial, err)
	}
}

func TestModelAdditionalModelInfoJSONPrivateProvenanceFailsClosed(t *testing.T) {
	t.Parallel()

	state := types.StringValue(`{"native":true}`)
	_, provenance, err := modelAdditionalModelInfoJSONConfiguration(context.Background(), state, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeModelAdditionalModelInfoJSONProvenance(context.Background(), provenance)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeModelAdditionalModelInfoJSONProvenance(context.Background(), raw, state)
	if err != nil || !decoded.Configured || !decoded.TerraformOwned["/native"] {
		t.Fatalf("decoded provenance = %#v, %v", decoded, err)
	}
	mismatched, err := cloneSemanticDictionaryProvenance(context.Background(), provenance)
	if err != nil {
		t.Fatal(err)
	}
	mismatched.TerraformOwned = semanticDictionaryPathSet{"/other": true}
	mismatchedRaw, err := encodeModelAdditionalModelInfoJSONProvenance(context.Background(), mismatched)
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		raw   []byte
		state types.String
	}{
		"missing for state":   {state: state},
		"configured mismatch": {raw: raw, state: types.StringNull()},
		"path mismatch":       {raw: mismatchedRaw, state: state},
		"malformed":           {raw: []byte(`{"version":1}`), state: state},
	} {
		t.Run(name, func(t *testing.T) {
			if value, err := decodeModelAdditionalModelInfoJSONProvenance(context.Background(), test.raw, test.state); err == nil || value.Initialized {
				t.Fatalf("corrupt private decoded as %#v, %v", value, err)
			}
		})
	}
}

func TestModelAdditionalModelInfoJSONRequestBodiesPreserveNativeValues(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"POST", "PATCH"} {
		t.Run(method, func(t *testing.T) {
			var captured map[string]interface{}
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if method == "PATCH" && request.Method == http.MethodGet {
					response.Header().Set("Content-Type", "application/json")
					_, _ = response.Write([]byte(`{"data":[{"model_info":{"id":"model-json","base_model":"gpt-4o-mini","nested":{"api_only":true}}}]}`))
					return
				}
				if request.Method != method {
					response.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				decoder := json.NewDecoder(request.Body)
				decoder.UseNumber()
				if err := decoder.Decode(&captured); err != nil {
					t.Errorf("decode request: %v", err)
				}
				response.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(response).Encode(map[string]interface{}{"model_info": captured["model_info"], "litellm_params": captured["litellm_params"]})
			}))
			defer server.Close()

			resource := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
			data := &ModelResourceModel{
				ID:                      types.StringValue("model-json"),
				ModelName:               types.StringValue("model-json"),
				CustomLLMProvider:       types.StringValue("openai"),
				BaseModel:               types.StringValue("gpt-4o-mini"),
				AccessGroups:            types.ListNull(types.StringType),
				AdditionalLiteLLMParams: types.MapNull(types.StringType),
				AdditionalModelInfo: modelStringMap(map[string]string{
					"legacy_disjoint": "legacy",
				}),
				AdditionalModelInfoJSON: types.StringValue(`{"native":false,"large":9007199254740993,"nested":{"nullable":null,"items":[1,true,"1"]}}`),
			}
			var err error
			if method == "POST" {
				err = resource.createOrUpdateModel(context.Background(), data, "model-json", false)
			} else {
				_, err = resource.patchModel(context.Background(), data, &ModelResourceModel{}, false, false)
			}
			if err != nil {
				t.Fatal(err)
			}
			modelInfo, ok := captured["model_info"].(map[string]interface{})
			if !ok || modelInfo["native"] != false || modelInfo["legacy_disjoint"] != "legacy" {
				t.Fatalf("model_info = %#v", captured["model_info"])
			}
			nested, ok := modelInfo["nested"].(map[string]interface{})
			if !ok || nested["nullable"] != nil {
				t.Fatalf("nested model_info = %#v", modelInfo["nested"])
			}
			large, ok := modelInfo["large"].(json.Number)
			if !ok || large.String() != "9007199254740993" {
				t.Fatalf("large = %#v", modelInfo["large"])
			}
			if method == "PATCH" {
				nested, ok := modelInfo["nested"].(map[string]interface{})
				if !ok || nested["api_only"] != true || nested["items"] == nil {
					t.Fatalf("hydrated nested metadata = %#v", modelInfo["nested"])
				}
			}
		})
	}
}

func TestModelAdditionalModelInfoJSONHydrationIdentityFailureSendsNoPatch(t *testing.T) {
	t.Parallel()

	for name, responseBody := range map[string]string{
		"missing": `{"data":[{"model_info":{"nested":{"api_only":true}}}]}`,
		"wrong":   `{"data":[{"model_info":{"id":"different-model","nested":{"api_only":true}}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var reads, patches atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodGet {
					reads.Add(1)
					_, _ = response.Write([]byte(responseBody))
					return
				}
				patches.Add(1)
				response.WriteHeader(http.StatusInternalServerError)
			}))
			defer server.Close()
			resource := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
			data := &ModelResourceModel{
				ID:                      types.StringValue("model-json"),
				ModelName:               types.StringValue("model-json"),
				CustomLLMProvider:       types.StringValue("openai"),
				BaseModel:               types.StringValue("gpt-4o-mini"),
				AccessGroups:            types.ListNull(types.StringType),
				AdditionalLiteLLMParams: types.MapNull(types.StringType),
				AdditionalModelInfo:     types.MapNull(types.StringType),
				AdditionalModelInfoJSON: types.StringValue(`{"nested":{"owned":true}}`),
			}
			if _, err := resource.patchModel(context.Background(), data, &ModelResourceModel{}, false, false); err == nil {
				t.Fatal("identity failure unexpectedly reached PATCH")
			}
			if reads.Load() != 1 || patches.Load() != 0 {
				t.Fatalf("requests: reads=%d patches=%d", reads.Load(), patches.Load())
			}
		})
	}
}

func TestModelAdditionalModelInfoJSONOverlapSendsNoRequest(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		response.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	resource := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := &ModelResourceModel{
		ModelName:               types.StringValue("model-json"),
		CustomLLMProvider:       types.StringValue("openai"),
		BaseModel:               types.StringValue("gpt-4o-mini"),
		AccessGroups:            types.ListNull(types.StringType),
		AdditionalLiteLLMParams: types.MapNull(types.StringType),
		AdditionalModelInfo:     modelStringMap(map[string]string{"collision": "private-value"}),
		AdditionalModelInfoJSON: types.StringValue(`{"collision":true}`),
	}
	if err := resource.createOrUpdateModel(context.Background(), data, "model-json", false); err == nil {
		t.Fatal("overlapping request unexpectedly succeeded")
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("overlap sent %d requests", got)
	}
}

func TestCreateModelAdditionalModelInfoJSONShapeFailureIsAnError(t *testing.T) {
	t.Parallel()

	var posts, reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/model/new":
			posts.Add(1)
			_, _ = response.Write([]byte(`{}`))
		case request.Method == http.MethodGet && request.URL.Path == "/model/info":
			reads.Add(1)
			_, _ = response.Write([]byte(`{"data":[{"model_name":"model-json","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini"},"model_info":{"base_model":"gpt-4o-mini"}}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	resourceUnderTest := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	schema := schemaResponse.Schema
	empty := tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil)
	configured := types.StringValue(`{"owned":{"native":true}}`)
	planned := ModelResourceModel{
		ModelName:               types.StringValue("model-json"),
		CustomLLMProvider:       types.StringValue("openai"),
		BaseModel:               types.StringValue("gpt-4o-mini"),
		AccessGroups:            types.ListNull(types.StringType),
		AdditionalLiteLLMParams: types.MapNull(types.StringType),
		AdditionalModelInfo:     types.MapNull(types.StringType),
		AdditionalModelInfoJSON: configured,
	}
	plan := tfsdk.Plan{Raw: empty, Schema: schema}
	if diagnostics := plan.Set(context.Background(), &planned); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	config := tfsdk.Config{Raw: plan.Raw, Schema: schema}
	response := &resource.CreateResponse{State: tfsdk.State{Raw: empty, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan, Config: config}, response)
	if !response.Diagnostics.HasError() || posts.Load() != 1 || reads.Load() != 1 {
		t.Fatalf("create shape failure: diagnostics=%v posts=%d reads=%d", response.Diagnostics, posts.Load(), reads.Load())
	}
	var state ModelResourceModel
	if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if state.ID.IsNull() || !state.AdditionalModelInfoJSON.Equal(configured) {
		t.Fatalf("complete planned state was not retained: id=%#v json=%#v", state.ID, state.AdditionalModelInfoJSON)
	}
}

func TestModelAdditionalModelInfoJSONNonPersistentValuesSendNoRequest(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		response.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	resource := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	for _, raw := range []string{`{"close":1.0000000000000001}`, `{"top_level_null":null}`} {
		data := &ModelResourceModel{
			ModelName:               types.StringValue("model-json"),
			CustomLLMProvider:       types.StringValue("openai"),
			BaseModel:               types.StringValue("gpt-4o-mini"),
			AccessGroups:            types.ListNull(types.StringType),
			AdditionalLiteLLMParams: types.MapNull(types.StringType),
			AdditionalModelInfo:     types.MapNull(types.StringType),
			AdditionalModelInfoJSON: types.StringValue(raw),
		}
		if err := resource.createOrUpdateModel(context.Background(), data, "model-json", false); err == nil {
			t.Fatal("non-persistent value request unexpectedly succeeded")
		}
		if got := requests.Load(); got != 0 {
			t.Fatalf("non-persistent value sent %d requests", got)
		}
	}
}

func TestReadModelAdditionalModelInfoJSONProjectsOnlyOwnedPaths(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"data":[{"model_name":"model-json","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini"},"model_info":{"base_model":"gpt-4o-mini","owned":{"native":false,"api_only":"ignored"},"literal":"****","derived":{"ignored":true}}}]}`))
	}))
	defer server.Close()
	resource := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	configured := types.StringValue(`{ "owned":{"native":false}, "literal":"****" }`)
	_, provenance, err := modelAdditionalModelInfoJSONConfiguration(context.Background(), configured, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	data := ModelResourceModel{
		ID:                      types.StringValue("model-json"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: types.MapUnknown(types.StringType),
		AdditionalModelInfo:     types.MapUnknown(types.StringType),
		AdditionalModelInfoJSON: configured,
	}
	if err := resource.readModelWithOwnership(context.Background(), &data, modelReadOwnership{additionalModelInfoJSONProvenance: provenance}); err != nil {
		t.Fatal(err)
	}
	if !data.AdditionalModelInfoJSON.Equal(configured) {
		t.Fatalf("configured spelling was not preserved: %q", data.AdditionalModelInfoJSON.ValueString())
	}
	if strings.Contains(data.AdditionalModelInfoJSON.ValueString(), "api_only") || strings.Contains(data.AdditionalModelInfoJSON.ValueString(), "derived") {
		t.Fatalf("API-only fields leaked into state: %q", data.AdditionalModelInfoJSON.ValueString())
	}
}

func TestModelAdditionalModelInfoJSONCancellationReturnsNoPartialOutput(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	object, provenance, err := modelAdditionalModelInfoJSONConfiguration(ctx, types.StringValue(`{"native":true}`), types.MapNull(types.StringType))
	if !errors.Is(err, context.Canceled) || object != nil || provenance.Initialized {
		t.Fatalf("canceled configuration = %#v %#v %v", object, provenance, err)
	}
	configured := types.StringValue(`{"native":true}`)
	_, validProvenance, err := modelAdditionalModelInfoJSONConfiguration(context.Background(), configured, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	if projected, err := projectModelAdditionalModelInfoJSON(ctx, map[string]interface{}{"native": true}, validProvenance); !errors.Is(err, context.Canceled) || projected != nil {
		t.Fatalf("canceled projection = %#v %v", projected, err)
	}
}

func TestModelAdditionalModelInfoJSONRequestConversionIsTransactional(t *testing.T) {
	t.Parallel()

	data := ModelResourceModel{
		AccessGroups:            types.ListValueMust(types.StringType, []attr.Value{types.StringValue("valid")}),
		AdditionalLiteLLMParams: types.MapNull(types.StringType),
		AdditionalModelInfo:     types.MapNull(types.StringType),
		AdditionalModelInfoJSON: types.StringValue(`{"private-sensitive-key":`),
	}
	converted, diagnostics := convertModelRequestCollections(context.Background(), data)
	if !diagnostics.HasError() || converted.accessGroups != nil || converted.additionalModelInfoJSON != nil || converted.additionalModelInfoConfigured {
		t.Fatalf("failed conversion returned partial output: %#v %#v", converted, diagnostics)
	}
	text := diagnostics.Errors()[0].Summary() + diagnostics.Errors()[0].Detail()
	if strings.Contains(text, "private-sensitive-key") {
		t.Fatalf("diagnostics exposed collection content: %s", text)
	}
}
