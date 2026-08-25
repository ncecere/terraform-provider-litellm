package provider

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestModelThinkingAdditionalParamsDoNotOwnTopLevelPlanOrRead(t *testing.T) {
	t.Parallel()

	var created map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/model/new":
			defer request.Body.Close()
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&created); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			_, _ = w.Write([]byte(`{}`))
		case "/model/info":
			_, _ = w.Write([]byte(`{"data":[{"model_name":"claude","litellm_params":{"custom_llm_provider":"anthropic","model":"anthropic/claude","thinking":{"type":"enabled","budget_tokens":2048}},"model_info":{"base_model":"claude"}}]}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	resourceUnderTest := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	var schemaResponse resource.SchemaResponse
	resourceUnderTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResponse.Diagnostics)
	}
	schema := schemaResponse.Schema
	empty := tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil)
	planned := ModelResourceModel{
		ModelName:            types.StringValue("claude"),
		CustomLLMProvider:    types.StringValue("anthropic"),
		BaseModel:            types.StringValue("claude"),
		ThinkingEnabled:      types.BoolValue(true),
		ThinkingBudgetTokens: types.Int64Value(4096),
		AccessGroups:         types.ListNull(types.StringType),
		AdditionalLiteLLMParams: types.MapValueMust(types.StringType, map[string]attr.Value{
			"thinking": types.StringValue(`{"type":"enabled","budget_tokens":2048}`),
		}),
		AdditionalModelInfo: types.MapNull(types.StringType),
	}
	plan := tfsdk.Plan{Raw: empty, Schema: schema}
	if diagnostics := plan.Set(context.Background(), &planned); diagnostics.HasError() {
		t.Fatalf("set plan: %v", diagnostics)
	}
	config := tfsdk.Config{Raw: plan.Raw, Schema: schema}
	createResponse := &resource.CreateResponse{State: tfsdk.State{Raw: empty, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan, Config: config}, createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}

	var state ModelResourceModel
	if diagnostics := createResponse.State.Get(context.Background(), &state); diagnostics.HasError() {
		t.Fatalf("get state: %v", diagnostics)
	}
	if !state.ThinkingEnabled.ValueBool() || state.ThinkingBudgetTokens.ValueInt64() != 4096 {
		t.Fatalf("additional thinking overwrote top-level plan/read state: enabled=%v budget=%d", state.ThinkingEnabled, state.ThinkingBudgetTokens.ValueInt64())
	}
	var stateAdditional map[string]string
	state.AdditionalLiteLLMParams.ElementsAs(context.Background(), &stateAdditional, false)
	if got := stateAdditional["thinking"]; got != `{"type":"enabled","budget_tokens":2048}` {
		t.Fatalf("both configured forms produced inconsistent additional state: %s", got)
	}
	params := created["litellm_params"].(map[string]interface{})
	thinking := params["thinking"].(map[string]interface{})
	if budget, err := exactInt64FromAPI(thinking["budget_tokens"]); err != nil || budget != 2048 {
		t.Fatalf("additional thinking was not retained in API payload: %#v, %v", thinking, err)
	}
}

func TestReadModelAdditionalThinkingOwnsRemoteAndPreservesTopLevelState(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"model_name": "claude",
		"litellm_params": map[string]interface{}{
			"thinking": map[string]interface{}{"type": "enabled", "budget_tokens": int64(9007199254740993)},
		},
	})
	defer server.Close()
	priorThinking := `{"type":"enabled","budget_tokens":2048}`
	data := &ModelResourceModel{
		ID:                   types.StringValue("model-1"),
		ThinkingEnabled:      types.BoolValue(false),
		ThinkingBudgetTokens: types.Int64Value(1024),
		AdditionalLiteLLMParams: types.MapValueMust(types.StringType, map[string]attr.Value{
			"thinking": types.StringValue(priorThinking),
		}),
		AdditionalLiteLLMParamsConfigured: types.BoolValue(true),
	}
	if err := (&ModelResource{client: client}).readModelWithOwnership(context.Background(), data, modelReadOwnership{topThinkingOwned: true}); err != nil {
		t.Fatal(err)
	}
	if data.ThinkingEnabled.ValueBool() || data.ThinkingBudgetTokens.ValueInt64() != 1024 {
		t.Fatalf("additional ownership changed top-level state: %#v", data)
	}
	var additional map[string]string
	data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &additional, false)
	if got := additional["thinking"]; got != `{"budget_tokens":9007199254740993,"type":"enabled"}` {
		t.Fatalf("additional thinking did not refresh exact remote value: %s", got)
	}
}

func TestReadModelImportAdoptsRemoteThinkingThroughAdditionalParams(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"data": []interface{}{map[string]interface{}{
			"model_name": "claude",
			"litellm_params": map[string]interface{}{
				"custom_llm_provider": "anthropic",
				"model":               "anthropic/claude",
				"thinking": map[string]interface{}{
					"type":          "enabled",
					"budget_tokens": int64(9007199254740993),
				},
			},
			"model_info": map[string]interface{}{"id": "model-1", "base_model": "claude"},
		}},
	})
	defer server.Close()
	data := &ModelResourceModel{
		ID:                      types.StringValue("model-1"),
		ThinkingEnabled:         types.BoolNull(),
		ThinkingBudgetTokens:    types.Int64Null(),
		AdditionalLiteLLMParams: types.MapNull(types.StringType),
	}
	if err := (&ModelResource{client: client}).readModelWithOwnership(context.Background(), data, modelReadOwnership{imported: true}); err != nil {
		t.Fatal(err)
	}
	if data.ThinkingEnabled.IsNull() || data.ThinkingEnabled.ValueBool() || data.ThinkingBudgetTokens.ValueInt64() != 1024 {
		t.Fatalf("import did not resolve schema defaults without adopting remote thinking: %#v", data)
	}
	var additional map[string]string
	data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &additional, false)
	if got := additional["thinking"]; got != `{"budget_tokens":9007199254740993,"type":"enabled"}` {
		t.Fatalf("imported thinking = %s", got)
	}
}

func TestReadModelTopLevelThinkingOwnsBudgetWhenEnabled(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"model_name": "claude",
		"litellm_params": map[string]interface{}{
			"thinking": map[string]interface{}{"type": "enabled", "budget_tokens": int64(9007199254740993)},
		},
	})
	defer server.Close()
	data := &ModelResourceModel{
		ID:                   types.StringValue("model-1"),
		ThinkingEnabled:      types.BoolValue(true),
		ThinkingBudgetTokens: types.Int64Value(1024),
	}
	if err := (&ModelResource{client: client}).readModel(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if data.ThinkingBudgetTokens.ValueInt64() != 9007199254740993 {
		t.Fatalf("enabled top-level thinking budget = %d", data.ThinkingBudgetTokens.ValueInt64())
	}
}

func TestReadModelTopLevelThinkingRefreshesEnabledState(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"model_name":     "claude",
		"litellm_params": map[string]interface{}{},
	})
	defer server.Close()
	data := &ModelResourceModel{
		ID:                   types.StringValue("model-1"),
		ThinkingEnabled:      types.BoolValue(true),
		ThinkingBudgetTokens: types.Int64Value(2048),
	}
	if err := (&ModelResource{client: client}).readModelWithOwnership(context.Background(), data, modelReadOwnership{topThinkingOwned: true}); err != nil {
		t.Fatal(err)
	}
	if data.ThinkingEnabled.ValueBool() {
		t.Fatal("owned top-level thinking did not observe remote disable")
	}
	if data.ThinkingBudgetTokens.ValueInt64() != 2048 {
		t.Fatalf("disabled remote thinking unnecessarily rewrote budget state: %v", data.ThinkingBudgetTokens)
	}
}

func TestReadModelResolvesUnknownOptionalComputedCollections(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "text-embedding-3-small",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openai",
						"model":               "openai/text-embedding-3-small",
					},
					"model_info": map[string]interface{}{
						"base_model": "text-embedding-3-small",
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := ModelResourceModel{
		ID:                      types.StringValue("model-123"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: types.MapUnknown(types.StringType),
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	if data.AccessGroups.IsUnknown() {
		t.Fatal("access_groups should be known after read")
	}
	if data.AdditionalLiteLLMParams.IsUnknown() {
		t.Fatal("additional_litellm_params should be known after read")
	}
}

func TestReadModelTeamScopedPrefersTeamPublicModelName(t *testing.T) {
	t.Parallel()

	const publicName = "gpt-4o-2024-11-20-prod-filter"
	const internalName = "model_name_GRP-DCAI-LLM-GATEWAY_a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	const teamID = "GRP-DCAI-LLM-GATEWAY"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": internalName,
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openai",
						"model":               "openai/gpt-4o",
					},
					"model_info": map[string]interface{}{
						"base_model":             "gpt-4o",
						"team_id":                teamID,
						"team_public_model_name": publicName,
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := ModelResourceModel{
		ID:                      types.StringValue("model-123"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: types.MapUnknown(types.StringType),
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	if got := data.ModelName.ValueString(); got != publicName {
		t.Fatalf("expected model_name=%q (team_public_model_name), got %q", publicName, got)
	}
	if got := data.TeamID.ValueString(); got != teamID {
		t.Fatalf("expected team_id=%q, got %q", teamID, got)
	}
}

// TestReadModelResolvesUnknownModeForWildcardRouting verifies that when the
// LiteLLM API does not return a "mode" value (e.g. for wildcard routes like
// openai/*), readModel resolves the Unknown mode to Null rather than leaving
// it Unknown. Terraform requires all Computed attributes to be known (or null)
// after apply, so leaving it Unknown causes:
//
//	"provider still indicated an unknown value for litellm_model.*.mode"
func TestReadModelResolvesUnknownModeForWildcardRouting(t *testing.T) {
	t.Parallel()

	// Simulate LiteLLM returning a model with no "mode" in model_info,
	// which happens for wildcard routes (e.g. openai/*).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "openai/*",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openai",
						"model":               "openai/openai/*",
					},
					"model_info": map[string]interface{}{
						"base_model": "openai/*",
						// "mode" is intentionally absent – wildcard routes have no mode
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	// Simulate the state that Terraform builds from the plan when mode is not
	// specified in config: Computed+Optional means mode is Unknown pre-apply.
	data := ModelResourceModel{
		ID:                      types.StringValue("wildcard-model-123"),
		Mode:                    types.StringUnknown(),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: types.MapUnknown(types.StringType),
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	if data.Mode.IsUnknown() {
		t.Fatal("mode must not be Unknown after read (would cause 'provider returned unknown value after apply' error)")
	}
	// When no mode is returned by the API, the attribute should be null
	// (not an empty string – that would be a non-null known value).
	if !data.Mode.IsNull() {
		t.Fatalf("mode should be null when API returns no mode, got %q", data.Mode.ValueString())
	}
}

func TestConvertStringValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected interface{}
	}{
		{"0", int64(0)},
		{"42", int64(42)},
		{"-1", int64(-1)},
		{"3.14", float64(3.14)},
		{"true", true},
		{"false", false},
		{"hello", "hello"},
		{`["a","b"]`, []interface{}{"a", "b"}},
		{`{"key":"val"}`, map[string]interface{}{"key": "val"}},
		{"not json {", "not json {"},
	}

	for _, tt := range tests {
		got := convertStringValue(tt.input)
		gotJSON, _ := json.Marshal(got)
		expJSON, _ := json.Marshal(tt.expected)
		if string(gotJSON) != string(expJSON) {
			t.Errorf("convertStringValue(%q) = %v (%T), want %v (%T)", tt.input, got, got, tt.expected, tt.expected)
		}
	}
}

func TestConvertStringValuePreservesExactJSONNumbers(t *testing.T) {
	t.Parallel()

	nested, ok := convertStringValue(`{"large":9007199254740993,"close":1.0000000000000001}`).(map[string]interface{})
	if !ok {
		t.Fatalf("converted nested JSON = %T", nested)
	}
	large, largeOK := nested["large"].(json.Number)
	closeValue, closeOK := nested["close"].(json.Number)
	if !largeOK || large.String() != "9007199254740993" || !closeOK || closeValue.String() != "1.0000000000000001" {
		t.Fatalf("converted nested numbers rounded: %#v", nested)
	}
	scalar, ok := convertStringValue("9007199254740993.0").(json.Number)
	if !ok || scalar.String() != "9007199254740993" {
		t.Fatalf("converted scalar = %#v", scalar)
	}
}

func TestJSONSemanticallyEqual(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{"whitespace after colon", `{"inputs": "{prompt}"}`, `{"inputs":"{prompt}"}`, true},
		{"key ordering", `{"a":1,"b":2}`, `{"b":2,"a":1}`, true},
		{"identical", `{"inputs":"{prompt}"}`, `{"inputs":"{prompt}"}`, true},
		{"arrays equal", `["a", "b"]`, `["a","b"]`, true},
		{"exact numeric notation", `{"n":9007199254740993}`, `{"n":9.007199254740993e15}`, true},
		{"close large integers differ", `{"n":9007199254740992}`, `{"n":9007199254740993}`, false},
		{"close decimals differ", `{"n":1.0000000000000001}`, `{"n":1.0000000000000002}`, false},
		{"different values", `{"inputs":"{prompt}"}`, `{"inputs":"{other}"}`, false},
		{"different keys", `{"a":1}`, `{"b":1}`, false},
		{"a not json", `not json {`, `{"a":1}`, false},
		{"b not json", `{"a":1}`, `not json {`, false},
	}

	for _, tt := range tests {
		if got := jsonSemanticallyEqual(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: jsonSemanticallyEqual(%q, %q) = %v, want %v", tt.name, tt.a, tt.b, got, tt.want)
		}
	}
}

func TestJSONSameShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		// Masking case: same keys, a scalar value differs (secret masked on read).
		{"masked scalar value", `{"x-api-key":"realsecret","X-Model-Id":"m"}`, `{"x-api-key":"sk-masked-abc","X-Model-Id":"m"}`, true},
		{"identical", `{"x-api-key":"a"}`, `{"x-api-key":"a"}`, true},
		{"nested masked", `{"h":{"k":"real"}}`, `{"h":{"k":"masked"}}`, true},
		{"array same len differing scalars", `["a","b"]`, `["x","y"]`, true},
		// Not the same shape -- real structural drift, should NOT be tolerated.
		{"extra key", `{"a":"1"}`, `{"a":"1","b":"2"}`, false},
		{"missing key", `{"a":"1","b":"2"}`, `{"a":"1"}`, false},
		{"renamed key", `{"a":"1"}`, `{"b":"1"}`, false},
		{"array length differs", `["a"]`, `["a","b"]`, false},
		{"scalar vs object", `{"a":"1"}`, `"1"`, false},
		{"a not json", `not json`, `{"a":"1"}`, false},
	}

	for _, tt := range tests {
		if got := jsonSameShape(tt.a, tt.b); got != tt.want {
			t.Errorf("%s: jsonSameShape(%q, %q) = %v, want %v", tt.name, tt.a, tt.b, got, tt.want)
		}
	}
}

func TestJSONContainsMaskedValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{"masked nested secret", `{"headers":{"x-api-key":"sk****99"}}`, true},
		{"redacted marker", `{"token":"***REDACTED***"}`, true},
		{"unmasked scalar drift", `{"headers":{"x-api-key":"changed-value"}}`, false},
		{"asterisks below mask threshold", `{"value":"a***b"}`, false},
		{"invalid JSON", `not json`, false},
	}

	for _, test := range tests {
		if got := jsonContainsMaskedValue(test.value); got != test.want {
			t.Errorf("%s: jsonContainsMaskedValue(%q) = %v, want %v", test.name, test.value, got, test.want)
		}
	}
}

func TestReadModelPreservesSemanticallyEqualJSONFormatting(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "gpt-4o-mini",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openai",
						"model":               "openai/gpt-4o-mini",
						"input_schema":        map[string]interface{}{"inputs": "{prompt}"},
						"ordering":            map[string]interface{}{"a": 1.0, "b": 2.0},
						"stop_sequences":      []interface{}{"</end>", "STOP"},
					},
					"model_info": map[string]interface{}{"base_model": "gpt-4o-mini"},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{client: &Client{
		APIBase:    server.URL,
		APIKey:     "test-key",
		HTTPClient: server.Client(),
	}}
	prior, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"input_schema":   types.StringValue(`{"inputs": "{prompt}"}`),
		"ordering":       types.StringValue(`{"b": 2, "a": 1}`),
		"stop_sequences": types.StringValue(`["</end>", "STOP"]`),
	})
	data := ModelResourceModel{
		ID:                      types.StringValue("model-123"),
		AdditionalLiteLLMParams: prior,
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	var got map[string]string
	if diags := data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &got, false); diags.HasError() {
		t.Fatalf("decode additional_litellm_params: %v", diags)
	}
	want := map[string]string{
		"input_schema":   `{"inputs": "{prompt}"}`,
		"ordering":       `{"b": 2, "a": 1}`,
		"stop_sequences": `["</end>", "STOP"]`,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("additional_litellm_params[%q] = %q, want preserved %q", key, got[key], value)
		}
	}
}

func TestCreateModelSendsAdditionalLiteLLMParams(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	additionalParams, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"cooldown_time":  types.StringValue("0"),
		"timeout":        types.StringValue("500"),
		"custom_flag":    types.StringValue("true"),
		"stream_timeout": types.StringValue("300"),
	})

	data := &ModelResourceModel{
		ModelName:               types.StringValue("test-model"),
		CustomLLMProvider:       types.StringValue("openai"),
		BaseModel:               types.StringValue("gpt-4o-mini"),
		Tier:                    types.StringNull(),
		Mode:                    types.StringNull(),
		AdditionalLiteLLMParams: additionalParams,
		AccessGroups:            types.ListNull(types.StringType),
	}

	err := r.createOrUpdateModel(context.Background(), data, "test-id", false)
	if err != nil {
		t.Fatalf("createOrUpdateModel returned error: %v", err)
	}

	litellmParams, ok := capturedBody["litellm_params"].(map[string]interface{})
	if !ok {
		t.Fatal("litellm_params not found in request body")
	}

	// cooldown_time should be sent as int 0
	if v, ok := litellmParams["cooldown_time"]; !ok {
		t.Fatal("cooldown_time not found in litellm_params")
	} else if v != float64(0) { // JSON numbers decode as float64
		t.Fatalf("expected cooldown_time=0, got %v (%T)", v, v)
	}

	// timeout should be sent as int 500
	if v := litellmParams["timeout"]; v != float64(500) {
		t.Fatalf("expected timeout=500, got %v (%T)", v, v)
	}

	// custom_flag should be sent as bool true
	if v := litellmParams["custom_flag"]; v != true {
		t.Fatalf("expected custom_flag=true, got %v (%T)", v, v)
	}

	// stream_timeout should be sent as int 300
	if v := litellmParams["stream_timeout"]; v != float64(300) {
		t.Fatalf("expected stream_timeout=300, got %v (%T)", v, v)
	}
}

func TestCreateModelSendsTeamPublicModelNameWhenTeamIDSet(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	const wantName = "gpt-4o-2024-11-20-prod-filter"
	const wantTeamID = "GRP-DCAI-LLM-GATEWAY"

	data := &ModelResourceModel{
		ModelName:         types.StringValue(wantName),
		CustomLLMProvider: types.StringValue("openai"),
		BaseModel:         types.StringValue("gpt-4o"),
		TeamID:            types.StringValue(wantTeamID),
		Tier:              types.StringNull(),
		Mode:              types.StringNull(),
		AccessGroups:      types.ListNull(types.StringType),
	}

	err := r.createOrUpdateModel(context.Background(), data, "test-id", false)
	if err != nil {
		t.Fatalf("createOrUpdateModel returned error: %v", err)
	}

	modelInfo, ok := capturedBody["model_info"].(map[string]interface{})
	if !ok {
		t.Fatal("model_info not found in request body")
	}
	if got := modelInfo["team_public_model_name"]; got != wantName {
		t.Fatalf("expected model_info.team_public_model_name=%q, got %v", wantName, got)
	}
	if got := modelInfo["team_id"]; got != wantTeamID {
		t.Fatalf("expected model_info.team_id=%q, got %v", wantTeamID, got)
	}
}

func TestPatchModelSendsAdditionalLiteLLMParams(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	additionalParams, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"cooldown_time": types.StringValue("0"),
		"max_retries":   types.StringValue("3"),
	})

	data := &ModelResourceModel{
		ID:                      types.StringValue("model-789"),
		ModelName:               types.StringValue("test-model"),
		CustomLLMProvider:       types.StringValue("openrouter"),
		BaseModel:               types.StringValue("anthropic/claude-3.7-sonnet"),
		Tier:                    types.StringNull(),
		Mode:                    types.StringNull(),
		AdditionalLiteLLMParams: additionalParams,
		AccessGroups:            types.ListNull(types.StringType),
	}

	_, err := r.patchModel(context.Background(), data, &ModelResourceModel{}, false, false)
	if err != nil {
		t.Fatalf("patchModel returned error: %v", err)
	}

	litellmParams, ok := capturedBody["litellm_params"].(map[string]interface{})
	if !ok {
		t.Fatal("litellm_params not found in request body")
	}

	if v := litellmParams["cooldown_time"]; v != float64(0) {
		t.Fatalf("expected cooldown_time=0, got %v (%T)", v, v)
	}
	if v := litellmParams["max_retries"]; v != float64(3) {
		t.Fatalf("expected max_retries=3, got %v (%T)", v, v)
	}
}

func TestPatchModelSendsTeamPublicModelNameWhenTeamIDSet(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	const wantName = "gpt-4o-prod-filter"
	const wantTeamID = "GRP-TEAM-1"

	data := &ModelResourceModel{
		ID:                types.StringValue("model-789"),
		ModelName:         types.StringValue(wantName),
		CustomLLMProvider: types.StringValue("openai"),
		BaseModel:         types.StringValue("gpt-4o"),
		TeamID:            types.StringValue(wantTeamID),
		Tier:              types.StringNull(),
		Mode:              types.StringNull(),
		AccessGroups:      types.ListNull(types.StringType),
	}

	_, err := r.patchModel(context.Background(), data, &ModelResourceModel{}, false, false)
	if err != nil {
		t.Fatalf("patchModel returned error: %v", err)
	}

	modelInfo, ok := capturedBody["model_info"].(map[string]interface{})
	if !ok {
		t.Fatal("model_info not found in request body")
	}
	if got := modelInfo["team_public_model_name"]; got != wantName {
		t.Fatalf("expected model_info.team_public_model_name=%q, got %v", wantName, got)
	}
	if got := modelInfo["team_id"]; got != wantTeamID {
		t.Fatalf("expected model_info.team_id=%q, got %v", wantTeamID, got)
	}
}

func TestReadModelExtractsAdditionalLiteLLMParams(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "gpt-4o-mini",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openai",
						"model":               "openai/gpt-4o-mini",
						"custom_flag":         true,
						"max_retries":         3.0,
					},
					"model_info": map[string]interface{}{
						"base_model":    "gpt-4o-mini",
						"access_groups": []interface{}{"team-a"},
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	// Simulate state with keys the user configured — readModel only reads back
	// keys that already exist in state to avoid "new element appeared" errors.
	priorParams, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"custom_flag": types.StringValue(""),
		"max_retries": types.StringValue(""),
	})

	data := ModelResourceModel{
		ID:                      types.StringValue("model-456"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: priorParams,
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	additional := map[string]string{}
	if diags := data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &additional, false); diags.HasError() {
		t.Fatalf("failed to decode additional_litellm_params: %v", diags)
	}

	if got := additional["custom_flag"]; got != "true" {
		t.Fatalf("expected custom_flag=true, got %q", got)
	}
	if got := additional["max_retries"]; got != "3" {
		t.Fatalf("expected max_retries=3, got %q", got)
	}
}

// TestReadModelPreservesMaskedAdditionalParams verifies both direct scalar
// masking (the azure_ad_token case from #119) and a masked secret nested inside
// a JSON-valued additional parameter.
func TestReadModelPreservesMaskedAdditionalParams(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "needs-attribution-classifier-xlab",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openai",
						"model":               "openai/checkpoint-3400",
						"azure_ad_token":      "_oidc*****ange_",
						"extra_headers": map[string]interface{}{
							"x-api-key":  "sk****99",
							"X-Model-Id": "needs-attribution-classifier-xlab",
						},
					},
					"model_info": map[string]interface{}{"base_model": "checkpoint-3400"},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()},
	}

	priorHeaders := `{"x-api-key":"realsecret","X-Model-Id":"needs-attribution-classifier-xlab"}`
	priorParams, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"azure_ad_token": types.StringValue("real-oidc-token"),
		"extra_headers":  types.StringValue(priorHeaders),
	})
	data := ModelResourceModel{
		ID:                      types.StringValue("model-xlab"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: priorParams,
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	additional := map[string]string{}
	if diags := data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &additional, false); diags.HasError() {
		t.Fatalf("failed to decode additional_litellm_params: %v", diags)
	}
	if got := additional["azure_ad_token"]; got != "real-oidc-token" {
		t.Errorf("expected azure_ad_token preserved from prior state, got %q", got)
	}
	if got := additional["extra_headers"]; got != priorHeaders {
		t.Errorf("expected extra_headers preserved from prior state %q, got %q", priorHeaders, got)
	}
}

func TestReadModelDoesNotHideUnmaskedJSONDrift(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "test-model",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openai",
						"model":               "openai/test-model",
						"extra_headers": map[string]interface{}{
							"x-api-key": "changed-without-mask-marker",
						},
					},
					"model_info": map[string]interface{}{"base_model": "test-model"},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	prior, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"extra_headers": types.StringValue(`{"x-api-key":"original"}`),
	})
	data := ModelResourceModel{
		ID:                      types.StringValue("model-123"),
		AdditionalLiteLLMParams: prior,
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}
	var additional map[string]string
	if diags := data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &additional, false); diags.HasError() {
		t.Fatalf("decode additional_litellm_params: %v", diags)
	}
	want := `{"x-api-key":"changed-without-mask-marker"}`
	if got := additional["extra_headers"]; got != want {
		t.Errorf("extra_headers = %q, want API drift %q", got, want)
	}
}

func TestReadModelCarriesForwardConfiguredParamsOmittedByAPI(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "gpt-4o-mini",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openai",
						"model":               "openai/gpt-4o-mini",
					},
					"model_info": map[string]interface{}{"base_model": "gpt-4o-mini"},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{client: &Client{
		APIBase:    server.URL,
		APIKey:     "test-key",
		HTTPClient: server.Client(),
	}}
	prior, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"tags":           types.StringValue(`["production"]`),
		"max_retries":    types.StringValue("3"),
		"timeout":        types.StringValue("30"),
		"stream_timeout": types.StringValue("60"),
	})
	data := ModelResourceModel{
		ID:                      types.StringValue("model-123"),
		AdditionalLiteLLMParams: prior,
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	var got map[string]string
	if diags := data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &got, false); diags.HasError() {
		t.Fatalf("decode additional_litellm_params: %v", diags)
	}
	want := map[string]string{
		"tags":           `["production"]`,
		"max_retries":    "3",
		"timeout":        "30",
		"stream_timeout": "60",
	}
	if len(got) != len(want) {
		t.Fatalf("additional_litellm_params = %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("additional_litellm_params[%q] = %q, want %q", key, got[key], value)
		}
	}
}

func TestReadModelUnwrapsDataArray(t *testing.T) {
	t.Parallel()

	// LiteLLM /model/info API returns {"data": [{...}]}, not a flat object.
	// readModel must unwrap the data array to extract model fields.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "openrouter/anthropic/claude-3.7-sonnet",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openrouter",
						"model":               "openrouter/anthropic/claude-3.7-sonnet",
						"cooldown_time":       0,
						"timeout":             500.0,
						"stream_timeout":      500.0,
						"max_retries":         1,
					},
					"model_info": map[string]interface{}{
						"id":         "test-uuid",
						"base_model": "anthropic/claude-3.7-sonnet",
						"tier":       "paid",
						"mode":       "chat",
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	// Simulate state with keys the user configured
	priorParams, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"cooldown_time":  types.StringValue("0"),
		"timeout":        types.StringValue("500"),
		"stream_timeout": types.StringValue("500"),
		"max_retries":    types.StringValue("1"),
	})

	data := ModelResourceModel{
		ID:                      types.StringValue("test-uuid"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: priorParams,
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	// Verify model_name was read
	if data.ModelName.ValueString() != "openrouter/anthropic/claude-3.7-sonnet" {
		t.Fatalf("expected model_name='openrouter/anthropic/claude-3.7-sonnet', got %q", data.ModelName.ValueString())
	}

	// Verify custom_llm_provider was read
	if data.CustomLLMProvider.ValueString() != "openrouter" {
		t.Fatalf("expected custom_llm_provider='openrouter', got %q", data.CustomLLMProvider.ValueString())
	}

	// Verify base_model was read from model_info
	if data.BaseModel.ValueString() != "anthropic/claude-3.7-sonnet" {
		t.Fatalf("expected base_model='anthropic/claude-3.7-sonnet', got %q", data.BaseModel.ValueString())
	}

	// Verify additional_litellm_params were extracted
	additional := map[string]string{}
	if diags := data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &additional, false); diags.HasError() {
		t.Fatalf("failed to decode additional_litellm_params: %v", diags)
	}

	if got := additional["cooldown_time"]; got != "0" {
		t.Fatalf("expected cooldown_time='0', got %q", got)
	}
	if got := additional["timeout"]; got != "500" {
		t.Fatalf("expected timeout='500', got %q", got)
	}
	if got := additional["max_retries"]; got != "1" {
		t.Fatalf("expected max_retries='1', got %q", got)
	}
}

func TestReadModelPassesMergeReasoningThroughAdditionalParams(t *testing.T) {
	t.Parallel()

	// merge_reasoning_content_in_choices can be passed both as a top-level attribute
	// and via additional_litellm_params. Since templates commonly use additional_litellm_params,
	// readModel should pass it through additional params (not filter it as "known").
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "test-model",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider":                "openrouter",
						"model":                              "openrouter/test-model",
						"merge_reasoning_content_in_choices": false,
						"use_in_pass_through":                false,
						"cooldown_time":                      0,
					},
					"model_info": map[string]interface{}{
						"base_model": "test-model",
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	// Simulate state with keys the user configured via additional_litellm_params
	priorParams, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"merge_reasoning_content_in_choices": types.StringValue("false"),
		"use_in_pass_through":                types.StringValue("false"),
		"cooldown_time":                      types.StringValue("0"),
	})

	data := ModelResourceModel{
		ID:                      types.StringValue("test-uuid"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: priorParams,
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	additional := map[string]string{}
	if diags := data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &additional, false); diags.HasError() {
		t.Fatalf("failed to decode additional_litellm_params: %v", diags)
	}

	// merge_reasoning_content_in_choices must be in additional_litellm_params
	if got, ok := additional["merge_reasoning_content_in_choices"]; !ok {
		t.Fatal("merge_reasoning_content_in_choices missing from additional_litellm_params")
	} else if got != "false" {
		t.Fatalf("expected merge_reasoning_content_in_choices='false', got %q", got)
	}

	// use_in_pass_through and cooldown_time should also be present
	if _, ok := additional["use_in_pass_through"]; !ok {
		t.Fatal("use_in_pass_through missing from additional_litellm_params")
	}
	if _, ok := additional["cooldown_time"]; !ok {
		t.Fatal("cooldown_time missing from additional_litellm_params")
	}
}

func TestNormalizeNumericString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		// Scientific notation → decimal
		{"1.75e-07", "0.000000175"},
		{"2.5e-06", "0.0000025"},
		{"1.25e-05", "0.0000125"},
		{"5e-08", "0.00000005"},
		{"4e-06", "0.000004"},
		{"6e-06", "0.000006"},
		{"1e-06", "0.000001"},
		{"1.8e-05", "0.000018"},
		{"2e-07", "0.0000002"},
		{"4e-07", "0.0000004"},
		// Already decimal — should stay the same
		{"0.000000175", "0.000000175"},
		{"0.0000025", "0.0000025"},
		{"0.0016384", "0.0016384"},
		{"3.14", "3.14"},
		// Integers — should stay the same
		{"0", "0"},
		{"42", "42"},
		{"500", "500"},
		{"-1", "-1"},
		// Non-numeric strings — unchanged
		{"hello", "hello"},
		{"true", "true"},
		{`["a"]`, `["a"]`},
	}

	for _, tt := range tests {
		got := normalizeNumericString(tt.input)
		if got != tt.expected {
			t.Errorf("normalizeNumericString(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestReadModelNormalizesScientificNotationStrings(t *testing.T) {
	t.Parallel()

	// The API may return numeric values as JSON strings in scientific notation.
	// readModel must normalise them to decimal notation to match the user's config.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "test-model",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider":          "openai",
						"model":                        "openai/test-model",
						"cache_read_input_token_cost":  "1.75e-07",
						"input_cost_per_token_batches": "2.5e-06",
					},
					"model_info": map[string]interface{}{
						"base_model": "test-model",
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	priorParams, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"cache_read_input_token_cost":  types.StringValue("0.000000175"),
		"input_cost_per_token_batches": types.StringValue("0.0000025"),
	})

	data := ModelResourceModel{
		ID:                      types.StringValue("test-id"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: priorParams,
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	additional := map[string]string{}
	if diags := data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &additional, false); diags.HasError() {
		t.Fatalf("failed to decode additional_litellm_params: %v", diags)
	}

	// Scientific notation strings should be normalised to decimal
	if got := additional["cache_read_input_token_cost"]; got != "0.000000175" {
		t.Fatalf("expected cache_read_input_token_cost='0.000000175', got %q", got)
	}
	if got := additional["input_cost_per_token_batches"]; got != "0.0000025" {
		t.Fatalf("expected input_cost_per_token_batches='0.0000025', got %q", got)
	}
}

func TestReadModelPreservesKnownParamsInAdditionalWhenUserConfigured(t *testing.T) {
	t.Parallel()

	// When a user explicitly puts input_cost_per_token in additional_litellm_params,
	// readModel must NOT filter it out (the "element has vanished" bug).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "test-model",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider":   "openai",
						"model":                 "openai/test-model",
						"input_cost_per_token":  0.000001,
						"output_cost_per_token": 0.000002,
						"cooldown_time":         0,
					},
					"model_info": map[string]interface{}{
						"base_model": "test-model",
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	// User configured these "known" params in additional_litellm_params
	priorParams, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"input_cost_per_token":  types.StringValue("0.000001"),
		"output_cost_per_token": types.StringValue("0.000002"),
		"cooldown_time":         types.StringValue("0"),
	})

	data := ModelResourceModel{
		ID:                      types.StringValue("test-id"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: priorParams,
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	additional := map[string]string{}
	if diags := data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &additional, false); diags.HasError() {
		t.Fatalf("failed to decode additional_litellm_params: %v", diags)
	}

	// The "known" params should NOT have vanished
	if _, ok := additional["input_cost_per_token"]; !ok {
		t.Fatal("input_cost_per_token should NOT be filtered when user configured it in additional_litellm_params")
	}
	if _, ok := additional["output_cost_per_token"]; !ok {
		t.Fatal("output_cost_per_token should NOT be filtered when user configured it in additional_litellm_params")
	}
	if _, ok := additional["cooldown_time"]; !ok {
		t.Fatal("cooldown_time missing from additional_litellm_params")
	}
}

func TestReadModelDoesNotSetModeWhenNull(t *testing.T) {
	t.Parallel()

	// When the user didn't set mode (null), readModel must NOT populate it
	// from the API response. This prevents "was null, but now 'video_generation'" errors.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "sora-2",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "azure",
						"model":               "azure/sora-2",
					},
					"model_info": map[string]interface{}{
						"base_model": "sora-2",
						"mode":       "video_generation",
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := ModelResourceModel{
		ID:                      types.StringValue("test-id"),
		Mode:                    types.StringNull(), // User did NOT set mode
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: types.MapNull(types.StringType),
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	if !data.Mode.IsNull() {
		t.Fatalf("expected mode to remain null, got %q", data.Mode.ValueString())
	}
}

func TestReadModelSetsModeWhenAlreadySet(t *testing.T) {
	t.Parallel()

	// When the user set mode, readModel should update it from the API.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "sora-2",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "azure",
						"model":               "azure/sora-2",
					},
					"model_info": map[string]interface{}{
						"base_model": "sora-2",
						"mode":       "video_generation",
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := ModelResourceModel{
		ID:                      types.StringValue("test-id"),
		Mode:                    types.StringValue("chat"), // User set mode
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: types.MapNull(types.StringType),
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	if data.Mode.ValueString() != "video_generation" {
		t.Fatalf("expected mode='video_generation', got %q", data.Mode.ValueString())
	}
}

func TestReadModelImportReadsAllAdditionalParams(t *testing.T) {
	t.Parallel()

	// During Import, additional_litellm_params is Unknown (no prior state).
	// readModel should read ALL non-known params from the API so the imported
	// resource captures the full state.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "test-model",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openai",
						"model":               "openai/test-model",
						"cooldown_time":       0,
						"timeout":             500.0,
						"custom_flag":         true,
					},
					"model_info": map[string]interface{}{
						"base_model": "test-model",
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	// Simulate Import: additional_litellm_params is Unknown
	data := ModelResourceModel{
		ID:                      types.StringValue("import-id"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: types.MapUnknown(types.StringType),
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	additional := map[string]string{}
	if diags := data.AdditionalLiteLLMParams.ElementsAs(context.Background(), &additional, false); diags.HasError() {
		t.Fatalf("failed to decode additional_litellm_params: %v", diags)
	}

	// All non-known params should be present
	if _, ok := additional["cooldown_time"]; !ok {
		t.Fatal("cooldown_time missing after import")
	}
	if _, ok := additional["timeout"]; !ok {
		t.Fatal("timeout missing after import")
	}
	if _, ok := additional["custom_flag"]; !ok {
		t.Fatal("custom_flag missing after import")
	}
}

func TestReadBackCost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		current types.Float64
		object  map[string]interface{}
		scale   float64
		want    types.Float64
		wantErr bool
	}{
		{"null state stays null when API returns a value", types.Float64Null(), map[string]interface{}{"cost": 2.5e-06}, 1000000.0, types.Float64Null(), false},
		{"changed API value updates state with scaling", types.Float64Value(3.0), map[string]interface{}{"cost": 2.5e-06}, 1000000.0, types.Float64Value(2.5), false},
		{"round-trip within tolerance keeps state", types.Float64Value(3.0), map[string]interface{}{"cost": 3.0 / 1000000.0}, 1000000.0, types.Float64Value(3.0), false},
		{"numeric string from API is parsed", types.Float64Value(3.0), map[string]interface{}{"cost": "2.5e-06"}, 1000000.0, types.Float64Value(2.5), false},
		{"missing API value keeps state", types.Float64Value(3.0), map[string]interface{}{}, 1000000.0, types.Float64Value(3.0), false},
		{"explicit null clears state", types.Float64Value(3.0), map[string]interface{}{"cost": nil}, 1000000.0, types.Float64Null(), false},
		{"non-numeric API value is rejected without stale state", types.Float64Value(3.0), map[string]interface{}{"cost": "not-a-number"}, 1000000.0, types.Float64Null(), true},
		{"scaled overflow is rejected without stale state", types.Float64Value(3.0), map[string]interface{}{"cost": math.MaxFloat64}, 2.0, types.Float64Null(), true},
		{"unscaled cost updates directly", types.Float64Value(0.001), map[string]interface{}{"cost": 0.002}, 1.0, types.Float64Value(0.002), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readBackCost(tc.current, tc.object, "cost", tc.scale)
			if (err != nil) != tc.wantErr {
				t.Fatalf("readBackCost() error = %v, wantErr %t", err, tc.wantErr)
			}
			if !got.Equal(tc.want) {
				t.Fatalf("readBackCost() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReadModelReadsBackTokenCosts(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "gpt-4o-mini",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider":   "openai",
						"model":                 "openai/gpt-4o-mini",
						"input_cost_per_token":  2.5e-06,
						"output_cost_per_token": 1e-05,
						"input_cost_per_pixel":  0.002,
					},
					"model_info": map[string]interface{}{
						"base_model": "gpt-4o-mini",
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := ModelResourceModel{
		ID: types.StringValue("model-789"),
		// Costs were changed out-of-band (e.g. via the UI); state has the old values.
		InputCostPerMillionTokens:  types.Float64Value(3.0),
		OutputCostPerMillionTokens: types.Float64Value(15.0),
		// input_cost_per_pixel was never configured — the API-returned value
		// must not surface, otherwise apply would report an inconsistent result.
		InputCostPerPixel: types.Float64Null(),
		AccessGroups:      types.ListUnknown(types.StringType),
	}
	data.AdditionalLiteLLMParams, _ = types.MapValue(types.StringType, map[string]attr.Value{})

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	if got := data.InputCostPerMillionTokens.ValueFloat64(); got != 2.5 {
		t.Fatalf("expected input_cost_per_million_tokens=2.5, got %v", got)
	}
	if got := data.OutputCostPerMillionTokens.ValueFloat64(); got != 10.0 {
		t.Fatalf("expected output_cost_per_million_tokens=10, got %v", got)
	}
	if !data.InputCostPerPixel.IsNull() {
		t.Fatalf("expected input_cost_per_pixel to stay null, got %v", data.InputCostPerPixel)
	}
}

func TestReadModelCostRoundTripCausesNoDrift(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "claude-sonnet",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "anthropic",
						"model":               "anthropic/claude-sonnet",
						// Exactly what createOrUpdateModel sends for a configured 3.0.
						"input_cost_per_token":  3.0 / 1000000.0,
						"output_cost_per_token": 15.0 / 1000000.0,
					},
					"model_info": map[string]interface{}{
						"base_model": "claude-sonnet",
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	inputCost := types.Float64Value(3.0)
	outputCost := types.Float64Value(15.0)
	data := ModelResourceModel{
		ID:                         types.StringValue("model-round-trip"),
		InputCostPerMillionTokens:  inputCost,
		OutputCostPerMillionTokens: outputCost,
		AccessGroups:               types.ListUnknown(types.StringType),
	}
	data.AdditionalLiteLLMParams, _ = types.MapValue(types.StringType, map[string]attr.Value{})

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	// The read-back value must be bit-identical to the configured one so the
	// framework does not report drift after the per-token round-trip.
	if !data.InputCostPerMillionTokens.Equal(inputCost) {
		t.Fatalf("input cost drifted after round-trip: %v", data.InputCostPerMillionTokens)
	}
	if !data.OutputCostPerMillionTokens.Equal(outputCost) {
		t.Fatalf("output cost drifted after round-trip: %v", data.OutputCostPerMillionTokens)
	}
}

func TestReassertPlannedCostsOverridesStaleReadBack(t *testing.T) {
	t.Parallel()

	// Simulate the post-apply consistency read hitting a stale router:
	// the plan set output cost to 16, but /model/info still echoes 15.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "gpt-4o-mini",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider":   "openai",
						"model":                 "openai/gpt-4o-mini",
						"output_cost_per_token": 15.0 / 1000000.0, // stale
					},
					"model_info": map[string]interface{}{
						"base_model": "gpt-4o-mini",
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := ModelResourceModel{
		ID:                         types.StringValue("model-stale"),
		OutputCostPerMillionTokens: types.Float64Value(16.0),
		AccessGroups:               types.ListUnknown(types.StringType),
	}
	data.AdditionalLiteLLMParams, _ = types.MapValue(types.StringType, map[string]attr.Value{})

	planned := data

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	// Without reassertion the stale echo would clobber the planned value…
	if got := data.OutputCostPerMillionTokens.ValueFloat64(); got != 15.0 {
		t.Fatalf("precondition failed: expected stale read-back 15, got %v", got)
	}

	// …which is exactly what reassertPlannedCosts prevents in Create/Update.
	reassertPlannedCosts(&data, &planned)
	if got := data.OutputCostPerMillionTokens.ValueFloat64(); got != 16.0 {
		t.Fatalf("expected planned cost 16 after reassert, got %v", got)
	}
}

func TestCreateModelSendsAdditionalModelInfo(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	additionalInfo, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"supports_vision":           types.StringValue("true"),
		"supports_function_calling": types.StringValue("true"),
		"max_input_tokens":          types.StringValue("128000"),
	})

	data := &ModelResourceModel{
		ModelName:           types.StringValue("kimi-k3"),
		CustomLLMProvider:   types.StringValue("openrouter"),
		BaseModel:           types.StringValue("moonshotai/kimi-k3"),
		AdditionalModelInfo: additionalInfo,
	}

	if err := r.createOrUpdateModel(context.Background(), data, "test-id", false); err != nil {
		t.Fatalf("createOrUpdateModel returned error: %v", err)
	}

	modelInfo, ok := capturedBody["model_info"].(map[string]interface{})
	if !ok {
		t.Fatalf("model_info missing in request body: %v", capturedBody)
	}

	if got := modelInfo["supports_vision"]; got != true {
		t.Fatalf("expected supports_vision=true (bool), got %v (%T)", got, got)
	}
	if got := modelInfo["supports_function_calling"]; got != true {
		t.Fatalf("expected supports_function_calling=true (bool), got %v (%T)", got, got)
	}
	// convertStringValue turns "128000" into an integer.
	if got := modelInfo["max_input_tokens"]; got != float64(128000) {
		t.Fatalf("expected max_input_tokens=128000, got %v (%T)", got, got)
	}
	// Fixed fields must still be present.
	if got := modelInfo["base_model"]; got != "moonshotai/kimi-k3" {
		t.Fatalf("expected base_model to be preserved, got %v", got)
	}
}

func TestPatchModelSendsAdditionalModelInfo(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" {
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "ok"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	additionalInfo, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"supports_reasoning": types.StringValue("true"),
	})

	data := &ModelResourceModel{
		ID:                  types.StringValue("model-info-patch"),
		ModelName:           types.StringValue("kimi-k3"),
		CustomLLMProvider:   types.StringValue("openrouter"),
		BaseModel:           types.StringValue("moonshotai/kimi-k3"),
		AdditionalModelInfo: additionalInfo,
	}

	if _, err := r.patchModel(context.Background(), data, &ModelResourceModel{}, false, false); err != nil {
		t.Fatalf("patchModel returned error: %v", err)
	}

	modelInfo, ok := capturedBody["model_info"].(map[string]interface{})
	if !ok {
		t.Fatalf("model_info missing in request body: %v", capturedBody)
	}
	if got := modelInfo["supports_reasoning"]; got != true {
		t.Fatalf("expected supports_reasoning=true (bool), got %v (%T)", got, got)
	}
}

func TestReadModelExtractsAdditionalModelInfoOnlyForConfiguredKeys(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "kimi-k3",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openrouter",
						"model":               "openrouter/moonshotai/kimi-k3",
					},
					"model_info": map[string]interface{}{
						"base_model":       "moonshotai/kimi-k3",
						"supports_vision":  true,
						"request_template": map[string]interface{}{"inputs": "{prompt}"},
						// Metadata merged from LiteLLM's model cost map — the
						// user never configured these and they must NOT be
						// read into additional_model_info.
						"max_tokens":              8192.0,
						"supports_prompt_caching": false,
						"litellm_provider":        "openrouter",
						"input_cost_per_token":    2.5e-07,
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	priorInfo, _ := types.MapValue(types.StringType, map[string]attr.Value{
		"supports_vision":  types.StringValue("true"),
		"request_template": types.StringValue(`{ "inputs": "{prompt}" }`),
	})

	data := ModelResourceModel{
		ID:                      types.StringValue("model-info-read"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: types.MapUnknown(types.StringType),
		AdditionalModelInfo:     priorInfo,
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	info := map[string]string{}
	if diags := data.AdditionalModelInfo.ElementsAs(context.Background(), &info, false); diags.HasError() {
		t.Fatalf("failed to decode additional_model_info: %v", diags)
	}

	if got := info["supports_vision"]; got != "true" {
		t.Fatalf("expected supports_vision=true, got %q", got)
	}
	if got := info["request_template"]; got != `{ "inputs": "{prompt}" }` {
		t.Fatalf("expected semantically equal JSON formatting to be preserved, got %q", got)
	}
	if len(info) != 2 {
		t.Fatalf("expected only configured keys to be read back, got %v", info)
	}
}

func TestReadModelResolvesUnknownAdditionalModelInfo(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"data": []interface{}{
				map[string]interface{}{
					"model_name": "gpt-4o-mini",
					"litellm_params": map[string]interface{}{
						"custom_llm_provider": "openai",
						"model":               "openai/gpt-4o-mini",
					},
					"model_info": map[string]interface{}{
						"base_model": "gpt-4o-mini",
						// Cost-map metadata that must not be captured.
						"max_tokens":      16384.0,
						"supports_vision": true,
					},
				},
			},
		})
	}))
	defer server.Close()

	r := &ModelResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	// Simulate Create (or Import) where additional_model_info was not configured.
	data := ModelResourceModel{
		ID:                      types.StringValue("model-info-unknown"),
		AccessGroups:            types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams: types.MapUnknown(types.StringType),
		AdditionalModelInfo:     types.MapUnknown(types.StringType),
	}

	if err := r.readModel(context.Background(), &data); err != nil {
		t.Fatalf("readModel returned error: %v", err)
	}

	if data.AdditionalModelInfo.IsUnknown() {
		t.Fatal("additional_model_info must be known after read")
	}
	if got := len(data.AdditionalModelInfo.Elements()); got != 0 {
		t.Fatalf("expected empty additional_model_info, got %d elements: %v", got, data.AdditionalModelInfo)
	}
}
