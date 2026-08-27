package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestModelAdditionalLiteLLMParamsJSONSchemaIsAdditiveSensitiveAndResourceOnly(t *testing.T) {
	t.Parallel()

	var response resource.SchemaResponse
	(&ModelResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	attribute, ok := response.Schema.Attributes["additional_litellm_params_json"].(resourceschema.StringAttribute)
	if !ok || !attribute.Optional || !attribute.Computed || !attribute.Sensitive {
		t.Fatalf("additional_litellm_params_json schema = %#v", response.Schema.Attributes["additional_litellm_params_json"])
	}
	legacy, ok := response.Schema.Attributes["additional_litellm_params"].(resourceschema.MapAttribute)
	if !ok || !legacy.Optional || !legacy.Computed || legacy.ElementType != types.StringType {
		t.Fatalf("legacy additional_litellm_params schema changed: %#v", response.Schema.Attributes["additional_litellm_params"])
	}
	if _, present := response.Schema.Attributes["vertex_credentials_json"]; present {
		t.Fatal("object Vertex credentials leaked into issue 218 consumer")
	}
	if _, present := response.Schema.Attributes["model_max_budget_json"]; present {
		t.Fatal("issue 223 model-budget behavior leaked into issue 218 consumer")
	}

	var singularResponse datasource.SchemaResponse
	(&ModelDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &singularResponse)
	if _, present := singularResponse.Schema.Attributes["additional_litellm_params_json"]; present {
		t.Fatal("resource-owned JSON leaked into singular model data source")
	}
	var listResponse datasource.SchemaResponse
	(&ModelsListDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &listResponse)
	if _, present := listResponse.Schema.Attributes["additional_litellm_params_json"]; present {
		t.Fatal("resource-owned JSON leaked into model list data source")
	}
}

func TestModelAdditionalLiteLLMParamsJSONNativePersistenceAndOverlap(t *testing.T) {
	t.Parallel()

	raw := `{ "string_false":"false", "native_false":false, "leading":"001", "large":9007199254740993, "decimal":0.1, "nested":{"items":[1,true,"1"],"api_token":[1,true,"authoritative"]} }`
	object, provenance, err := modelAdditionalLiteLLMParamsJSONConfiguration(context.Background(), types.StringValue(raw), types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	if !provenance.Configured || len(provenance.TerraformOwned) != 7 {
		t.Fatalf("provenance = %#v", provenance)
	}
	if object["string_false"] != "false" || object["native_false"] != false || object["leading"] != "001" {
		t.Fatalf("native identity changed: %#v", object)
	}
	if large, ok := object["large"].(json.Number); !ok || large.String() != "9007199254740993" {
		t.Fatalf("large integer = %#v", object["large"])
	}
	if decimal, ok := object["decimal"].(json.Number); !ok || decimal.String() != "0.1" {
		t.Fatalf("decimal = %#v", object["decimal"])
	}
	apiToken := object["nested"].(map[string]interface{})["api_token"].([]interface{})
	if apiToken[0].(json.Number).String() != "1" || apiToken[1] != true || apiToken[2] != "authoritative" {
		t.Fatalf("native values in sensitive list changed: %#v", apiToken)
	}

	for _, rejected := range []string{
		`null`, `[]`, `{"duplicate":1,"duplicate":2}`,
		`{"top_null":null}`, `{"nested":{"nullable":null}}`, `{"items":[null]}`,
		`{"private_key":123}`, `{"private_key":false}`, `{"private_key":null}`,
		`{"private_key":""}`, `{"literal":"None"}`, `{"items":["None"]}`,
		`{"private_key":["safe",null]}`, `{"lossy":1.0000000000000001}`, `{"lossy":1e-324}`,
	} {
		if value, gotProvenance, err := modelAdditionalLiteLLMParamsJSONConfiguration(context.Background(), types.StringValue(rejected), types.MapNull(types.StringType)); err == nil || value != nil || gotProvenance.Initialized {
			t.Fatalf("rejected value returned %#v %#v %v for %s", value, gotProvenance, err, rejected)
		}
	}

	secretKey := "private-sensitive-key"
	legacy := modelStringMap(map[string]string{secretKey: "private-value"})
	if value, gotProvenance, err := modelAdditionalLiteLLMParamsJSONConfiguration(context.Background(), types.StringValue(`{"private-sensitive-key":true}`), legacy); err == nil || value != nil || gotProvenance.Initialized || strings.Contains(err.Error(), secretKey) {
		t.Fatalf("legacy overlap = %#v %#v %v", value, gotProvenance, err)
	}
	computedLegacy := modelStringMap(map[string]string{"remote_custom": `{"native":true}`, "other": "keep"})
	filtered, err := excludeModelAdditionalLiteLLMParamsJSONTopLevelKeys(context.Background(), computedLegacy, map[string]interface{}{"remote_custom": map[string]interface{}{"native": true}})
	if err != nil {
		t.Fatal(err)
	}
	filteredStrings, _, diagnostics := strictTerraformStringMap(context.Background(), filtered, path.Root("additional_litellm_params"), true)
	if diagnostics.HasError() || len(filteredStrings) != 1 || filteredStrings["other"] != "keep" {
		t.Fatalf("JSON takeover legacy exclusion = %#v, %#v", filteredStrings, diagnostics)
	}

	for _, reserved := range modelAdditionalLiteLLMParamsJSONReservedKeys {
		raw, err := json.Marshal(map[string]interface{}{reserved: true})
		if err != nil {
			t.Fatal(err)
		}
		value, gotProvenance, overlapErr := modelAdditionalLiteLLMParamsJSONConfiguration(context.Background(), types.StringValue(string(raw)), types.MapNull(types.StringType))
		if overlapErr == nil || value != nil || gotProvenance.Initialized || strings.Contains(overlapErr.Error(), reserved) {
			t.Fatalf("reserved overlap %q = %#v %#v %v", reserved, value, gotProvenance, overlapErr)
		}
	}
}

func TestModelAdditionalLiteLLMParamsJSONReplacementContract(t *testing.T) {
	t.Parallel()

	state := types.StringValue(`{"nested":{"a":1,"b":2}}`)
	_, provenance, err := modelAdditionalLiteLLMParamsJSONConfiguration(context.Background(), state, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		config types.String
		want   bool
	}{
		{"formatting only", types.StringValue(`{ "nested" : { "b":2, "a":1 } }`), false},
		{"takeover", state, true},
		{"semantic change", types.StringValue(`{"nested":{"a":2,"b":2}}`), true},
		{"nested removal", types.StringValue(`{"nested":{"a":1}}`), true},
		{"empty", types.StringValue(`{}`), true},
		{"clear", types.StringNull(), true},
		{"unknown", types.StringUnknown(), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			usedProvenance := provenance
			usedState := state
			if test.name == "takeover" {
				usedProvenance = modelUnconfiguredSemanticDictionaryProvenance()
				usedState = types.StringNull()
			}
			got, err := modelAdditionalLiteLLMParamsJSONNeedsReplacement(context.Background(), test.config, usedState, usedProvenance)
			if err != nil || got != test.want {
				t.Fatalf("replacement = %v, %v; want %v", got, err, test.want)
			}
		})
	}
}

func TestModelInfoLiteLLMParamsMaskPredicateUsesObservedStructure(t *testing.T) {
	t.Parallel()

	observed := mustParseSemanticDictionary(t, `{
		"api_secret":["*****",["abcd****ijkl"],{"ordinary":"****","nested_key":"abcd***hijk"}],
		"secret_object":{"ordinary":"****","private-key":"abcd***hijk","0":"****"},
		"ordinary_list":["****",{"api_token":"****"}],
		"input_cost_per_token":"****",
		"litellm_credential_name":"****",
		"default_api_key_tpm_limit":"****",
		"0":{"api_key":"****"}
	}`)
	predicate := modelInfoLiteLLMParamsMaskPredicate(observed)
	for _, test := range []struct {
		path []string
		want bool
	}{
		{[]string{"api_secret", "0"}, true},
		{[]string{"api_secret", "1", "0"}, true},
		{[]string{"api_secret", "2", "ordinary"}, false},
		{[]string{"api_secret", "2", "nested_key"}, true},
		{[]string{"secret_object", "ordinary"}, false},
		{[]string{"secret_object", "private-key"}, true},
		{[]string{"secret_object", "0"}, false},
		{[]string{"ordinary_list", "0"}, false},
		{[]string{"ordinary_list", "1", "api_token"}, true},
		{[]string{"input_cost_per_token"}, false},
		{[]string{"litellm_credential_name"}, false},
		{[]string{"default_api_key_tpm_limit"}, false},
		{[]string{"0", "api_key"}, true},
	} {
		if got := predicate(test.path, modelSemanticDictionaryValueAtPathForTest(t, observed, test.path)); got != test.want {
			t.Fatalf("predicate(%v) = %v; want %v", test.path, got, test.want)
		}
	}
	for value, want := range map[string]bool{
		"": false, "***x": false, "********": true, "abcd*efgh": true,
		"abcd***hijk": true, "abcd**xhijk": false, "甲乙丙丁*戊己庚辛": true, "literal": false,
	} {
		if got := modelInfoMaskLike(value); got != want {
			t.Fatalf("modelInfoMaskLike(%q) = %v; want %v", value, got, want)
		}
	}

	deep := map[string]interface{}{}
	current := deep
	deepPath := make([]string, 0, modelInfoMaskMaxDepth+1)
	for index := 0; index < modelInfoMaskMaxDepth; index++ {
		name := fmt.Sprintf("level_%d", index)
		next := map[string]interface{}{}
		current[name] = next
		current = next
		deepPath = append(deepPath, name)
	}
	current["api_key"] = "****"
	deepPath = append(deepPath, "api_key")
	if modelInfoLiteLLMParamsMaskPredicate(deep)(deepPath, "****") {
		t.Fatal("mask predicate traversed beyond LiteLLM's pinned maximum depth")
	}
}

func modelSemanticDictionaryValueAtPathForTest(t *testing.T, object map[string]interface{}, path []string) string {
	t.Helper()
	var current interface{} = object
	for _, member := range path {
		switch value := current.(type) {
		case map[string]interface{}:
			current = value[member]
		case []interface{}:
			var index int
			if _, err := fmt.Sscanf(member, "%d", &index); err != nil {
				t.Fatal(err)
			}
			current = value[index]
		default:
			t.Fatalf("invalid test path %v", path)
		}
	}
	text, ok := current.(string)
	if !ok {
		t.Fatalf("test value at %v = %#v", path, current)
	}
	return text
}

func TestModelAdditionalLiteLLMParamsJSONFormattingOnlyChangeIsNotAnAPIChange(t *testing.T) {
	t.Parallel()

	prior := ModelResourceModel{
		AccessGroups:                types.ListNull(types.StringType),
		AdditionalLiteLLMParams:     types.MapNull(types.StringType),
		AdditionalLiteLLMParamsJSON: types.StringValue(`{"nested":{"a":1,"b":2}}`),
		AdditionalModelInfo:         types.MapNull(types.StringType),
		AdditionalModelInfoJSON:     types.StringNull(),
	}
	planned := prior
	planned.AdditionalLiteLLMParamsJSON = types.StringValue(`{ "nested" : { "b":2, "a":1 } }`)
	left, leftErr := parseSemanticDictionary(context.Background(), planned.AdditionalLiteLLMParamsJSON.ValueString())
	right, rightErr := parseSemanticDictionary(context.Background(), prior.AdditionalLiteLLMParamsJSON.ValueString())
	equal, equalErr := semanticDictionaryValuesEqual(context.Background(), left, right)
	if leftErr != nil || rightErr != nil || equalErr != nil || !equal {
		t.Fatalf("semantic precondition: left=%v right=%v equal=%v compare=%v", leftErr, rightErr, equal, equalErr)
	}
	changed, err := modelAPIFieldsChanged(context.Background(), planned, prior)
	if err != nil || changed {
		t.Fatalf("formatting-only API change = %v, %v", changed, err)
	}
	_, provenance, err := modelAdditionalLiteLLMParamsJSONConfiguration(context.Background(), planned.AdditionalLiteLLMParamsJSON, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	reconciled, err := reconcileModelAdditionalLiteLLMParamsJSON(context.Background(), planned.AdditionalLiteLLMParamsJSON, mustParseSemanticDictionary(t, `{"nested":{"a":1,"b":2}}`), provenance)
	if err != nil || !reconciled.Equal(planned.AdditionalLiteLLMParamsJSON) {
		t.Fatalf("configured spelling = %#v, %v", reconciled, err)
	}
}

func TestModelAdditionalLiteLLMParamsJSONProjectionMaskRecoveryAndShape(t *testing.T) {
	t.Parallel()

	configured := types.StringValue(`{ "custom":{"api_secret":"abcdefghijk","api_token":["short","abcdefghijkl"],"nested":{"private_key":"abcdefghijk"}}, "safe_literal":"****" }`)
	_, provenance, err := modelAdditionalLiteLLMParamsJSONConfiguration(context.Background(), configured, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	observed := mustParseSemanticDictionary(t, `{
		"custom":{"api_secret":"abcd***hijk","api_token":["*****","abcd****ijkl"],"nested":{"private_key":"abcd***hijk","api_only":true},"api_only":"ignored"},
		"safe_literal":"****","remote_only":true
	}`)
	reconciled, err := reconcileModelAdditionalLiteLLMParamsJSON(context.Background(), configured, observed, provenance)
	if err != nil || !reconciled.Equal(configured) {
		t.Fatalf("masked reconciliation = %#v, %v", reconciled, err)
	}
	if strings.Contains(reconciled.ValueString(), "api_only") || strings.Contains(reconciled.ValueString(), "remote_only") {
		t.Fatalf("API-only params leaked: %q", reconciled.ValueString())
	}

	drifted := mustParseSemanticDictionary(t, `{"custom":{"api_secret":"changed-plain","api_token":["short","abcdefghijkl"],"nested":{"private_key":"abcdefghijk"}},"safe_literal":"****"}`)
	reconciled, err = reconcileModelAdditionalLiteLLMParamsJSON(context.Background(), configured, drifted, provenance)
	if err != nil || !strings.Contains(reconciled.ValueString(), "changed-plain") {
		t.Fatalf("unmasked drift = %#v, %v", reconciled, err)
	}

	ambiguous := types.StringValue(`{"custom":{"api_secret":"****"}}`)
	_, ambiguousProvenance, err := modelAdditionalLiteLLMParamsJSONConfiguration(context.Background(), ambiguous, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	if value, err := reconcileModelAdditionalLiteLLMParamsJSON(context.Background(), ambiguous, mustParseSemanticDictionary(t, `{"custom":{"api_secret":"****"}}`), ambiguousProvenance); err == nil || !value.IsNull() {
		t.Fatalf("ambiguous configured mask recovered as %#v, %v", value, err)
	}

	for name, malformed := range map[string]string{
		"missing":          `{"custom":{"api_token":["short","abcdefghijkl"],"nested":{"private_key":"abcdefghijk"}},"safe_literal":"****"}`,
		"array shape":      `{"custom":{"api_secret":"abcdefghijk","api_token":["short"],"nested":{"private_key":"abcdefghijk"}},"safe_literal":"****"}`,
		"object shape":     `{"custom":{"api_secret":"abcdefghijk","api_token":["short","abcdefghijkl"],"nested":{"private_key":{"changed":true}}},"safe_literal":"****"}`,
		"stringified null": `{"custom":{"api_secret":"abcd***hijk","api_token":["*****","abcd****ijkl"],"nested":{"private_key":"abcd***hijk"}},"safe_literal":"None"}`,
		"empty sensitive":  `{"custom":{"api_secret":"","api_token":["*****","abcd****ijkl"],"nested":{"private_key":"abcd***hijk"}},"safe_literal":"****"}`,
		"missing prior":    `{"custom":{"api_secret":"abcd***hijk","api_token":["*****","abcd****ijkl"],"nested":{"private_key":"abcd***hijk"}},"safe_literal":"****"}`,
	} {
		t.Run(name, func(t *testing.T) {
			prior := configured
			if name == "missing prior" {
				prior = types.StringNull()
			}
			if value, err := reconcileModelAdditionalLiteLLMParamsJSON(context.Background(), prior, mustParseSemanticDictionary(t, malformed), provenance); err == nil || !value.IsNull() {
				t.Fatalf("unsafe projection = %#v, %v", value, err)
			}
		})
	}
}

func TestModelAdditionalLiteLLMParamsJSONHydrationPreservesSiblingsAndRejectsMasks(t *testing.T) {
	t.Parallel()

	configured := mustParseSemanticDictionary(t, `{"custom":{"owned":true,"nested":{"owned":1}}}`)
	_, provenance, err := modelAdditionalLiteLLMParamsJSONConfiguration(context.Background(), types.StringValue(`{"custom":{"owned":true,"nested":{"owned":1}}}`), types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	result := map[string]interface{}{"litellm_params": mustParseSemanticDictionary(t, `{"custom":{"owned":false,"api_only":true,"nested":{"owned":0,"api_only":"keep"}}}`)}
	hydrated, err := hydrateModelAdditionalLiteLLMParamsJSONPatch(context.Background(), result, configured, provenance)
	if err != nil {
		t.Fatal(err)
	}
	custom := hydrated["custom"].(map[string]interface{})
	if custom["owned"] != true || custom["api_only"] != true || custom["nested"].(map[string]interface{})["api_only"] != "keep" {
		t.Fatalf("hydrated params = %#v", hydrated)
	}

	// A sensitive parent map does not own nested-map values; the literal mask is
	// therefore ordinary and can be preserved.
	parentLiteral := map[string]interface{}{"litellm_params": mustParseSemanticDictionary(t, `{"custom":{"owned":false,"api_secret":{"ordinary":"****"}}}`)}
	if _, err := hydrateModelAdditionalLiteLLMParamsJSONPatch(context.Background(), parentLiteral, configured, provenance); err != nil {
		t.Fatalf("nested-map literal mask rejected: %v", err)
	}

	for name, raw := range map[string]string{
		"nested sibling":           `{"custom":{"owned":false,"api_only_key":"abcd***hijk"}}`,
		"list sibling":             `{"custom":{"owned":false,"api_token":["*****"]}}`,
		"stringified null sibling": `{"custom":{"owned":false,"api_only":"None"}}`,
		"empty sensitive sibling":  `{"custom":{"owned":false,"api_only_key":""}}`,
	} {
		t.Run(name, func(t *testing.T) {
			masked := map[string]interface{}{"litellm_params": mustParseSemanticDictionary(t, raw)}
			if value, err := hydrateModelAdditionalLiteLLMParamsJSONPatch(context.Background(), masked, configured, provenance); !errors.Is(err, errSemanticDictionaryMasked) || value != nil {
				t.Fatalf("unowned mask hydration = %#v, %v", value, err)
			}
		})
	}
}

func TestModelAdditionalLiteLLMParamsJSONRequestsAndSingleSharedHydration(t *testing.T) {
	t.Parallel()

	var reads, posts, patches atomic.Int64
	var capturedCreate, capturedPatch map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost:
			posts.Add(1)
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&capturedCreate); err != nil {
				t.Errorf("decode POST: %v", err)
			}
			_, _ = response.Write([]byte(`{}`))
		case request.Method == http.MethodGet:
			reads.Add(1)
			_, _ = response.Write([]byte(`{"data":[{"model_name":"model-json","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini","typed":{"api_only":true}},"model_info":{"id":"model-json","base_model":"gpt-4o-mini","typed_info":{"api_only":true}}}]}`))
		case request.Method == http.MethodPatch:
			patches.Add(1)
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&capturedPatch); err != nil {
				t.Errorf("decode PATCH: %v", err)
			}
			_ = json.NewEncoder(response).Encode(map[string]interface{}{"litellm_params": capturedPatch["litellm_params"], "model_info": capturedPatch["model_info"]})
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	resourceUnderTest := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	data := &ModelResourceModel{
		ID: types.StringValue("model-json"), ModelName: types.StringValue("model-json"),
		CustomLLMProvider: types.StringValue("openai"), BaseModel: types.StringValue("gpt-4o-mini"),
		AccessGroups: types.ListNull(types.StringType), AdditionalLiteLLMParams: types.MapNull(types.StringType),
		AdditionalLiteLLMParamsJSON: types.StringValue(`{"typed":{"native":false,"large":9007199254740993,"items":[1,true,"null"]}}`),
		AdditionalModelInfo:         types.MapNull(types.StringType),
		AdditionalModelInfoJSON:     types.StringValue(`{"typed_info":{"native":true}}`),
	}
	if err := resourceUnderTest.createOrUpdateModel(context.Background(), data, "model-json", false); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 0 || posts.Load() != 1 {
		t.Fatalf("create requests: reads=%d posts=%d", reads.Load(), posts.Load())
	}
	createTyped := capturedCreate["litellm_params"].(map[string]interface{})["typed"].(map[string]interface{})
	if createTyped["native"] != false || createTyped["items"] == nil {
		t.Fatalf("create litellm_params = %#v", capturedCreate["litellm_params"])
	}
	if _, err := resourceUnderTest.patchModel(context.Background(), data, &ModelResourceModel{}, false, false); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 1 || patches.Load() != 1 {
		t.Fatalf("update requests: reads=%d patches=%d", reads.Load(), patches.Load())
	}
	params := capturedPatch["litellm_params"].(map[string]interface{})
	typed := params["typed"].(map[string]interface{})
	if typed["native"] != false || typed["api_only"] != true || typed["items"] == nil {
		t.Fatalf("litellm_params = %#v", params)
	}
	if large, ok := typed["large"].(json.Number); !ok || large.String() != "9007199254740993" {
		t.Fatalf("large = %#v", typed["large"])
	}
	info := capturedPatch["model_info"].(map[string]interface{})["typed_info"].(map[string]interface{})
	if info["native"] != true || info["api_only"] != true {
		t.Fatalf("model_info = %#v", capturedPatch["model_info"])
	}
}

func TestReadModelAdditionalLiteLLMParamsJSONSelectiveProjectionAndLegacyExclusion(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{"data":[{"model_name":"model-json","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini","typed":{"owned":true,"api_only":true},"legacy_remote":false},"model_info":{"id":"model-json","base_model":"gpt-4o-mini"}}]}`))
	}))
	defer server.Close()
	resourceUnderTest := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	configured := types.StringValue(`{ "typed":{"owned":true} }`)
	_, provenance, err := modelAdditionalLiteLLMParamsJSONConfiguration(context.Background(), configured, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	data := ModelResourceModel{
		ID: types.StringValue("model-json"), AccessGroups: types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams:     modelStringMap(map[string]string{"typed": "stale-legacy-duplicate", "legacy_remote": "false"}),
		AdditionalLiteLLMParamsJSON: configured, AdditionalLiteLLMParamsConfigured: types.BoolValue(false),
		AdditionalModelInfo: types.MapUnknown(types.StringType), AdditionalModelInfoJSON: types.StringNull(),
	}
	if err := resourceUnderTest.readModelWithOwnership(context.Background(), &data, modelReadOwnership{additionalLiteLLMParamsJSONProvenance: provenance}); err != nil {
		t.Fatal(err)
	}
	if !data.AdditionalLiteLLMParamsJSON.Equal(configured) {
		t.Fatalf("configured spelling changed: %q", data.AdditionalLiteLLMParamsJSON.ValueString())
	}
	legacy, _, diagnostics := strictTerraformStringMap(context.Background(), data.AdditionalLiteLLMParams, path.Root("additional_litellm_params"), true)
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if _, leaked := legacy["typed"]; leaked || legacy["legacy_remote"] != "false" {
		t.Fatalf("legacy projection = %#v", legacy)
	}
}

func TestModelAdditionalLiteLLMParamsJSONInvalidConfigurationSendsNoRequest(t *testing.T) {
	t.Parallel()

	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		response.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	resourceUnderTest := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	for _, test := range []struct {
		legacy types.Map
		raw    string
	}{
		{types.MapNull(types.StringType), `{"top_null":null}`},
		{types.MapNull(types.StringType), `{"nested":{"nullable":null}}`},
		{types.MapNull(types.StringType), `{"private_key":123}`},
		{types.MapNull(types.StringType), `{"private_key":[null]}`},
		{types.MapNull(types.StringType), `{"lossy":1.0000000000000001}`},
		{types.MapNull(types.StringType), `{"duplicate":1,"duplicate":2}`},
		{types.MapNull(types.StringType), `{"tpm":1}`},
		{types.MapNull(types.StringType), `{"max_budget":1}`},
		{modelStringMap(map[string]string{"collision": "private-value"}), `{"collision":true}`},
	} {
		data := &ModelResourceModel{
			ModelName: types.StringValue("model-json"), CustomLLMProvider: types.StringValue("openai"), BaseModel: types.StringValue("gpt-4o-mini"),
			AccessGroups: types.ListNull(types.StringType), AdditionalLiteLLMParams: test.legacy,
			AdditionalLiteLLMParamsJSON: types.StringValue(test.raw), AdditionalModelInfo: types.MapNull(types.StringType), AdditionalModelInfoJSON: types.StringNull(),
		}
		if err := resourceUnderTest.createOrUpdateModel(context.Background(), data, "model-json", false); err == nil {
			t.Fatalf("invalid configuration %s unexpectedly succeeded", test.raw)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid configurations sent %d requests", requests.Load())
	}
}
