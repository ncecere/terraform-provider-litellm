package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPatchModelEmitsOnlySupportedClearSentinels(t *testing.T) {
	t.Parallel()
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPatch || request.URL.Path != "/model/model-clear/update" {
			http.NotFound(writer, request)
			return
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(writer, "bad request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()

	prior := ModelResourceModel{
		ID:                             types.StringValue("model-clear"),
		ModelName:                      types.StringValue("clear-model"),
		CustomLLMProvider:              types.StringValue("anthropic"),
		BaseModel:                      types.StringValue("claude"),
		Tier:                           types.StringValue("paid"),
		ModelAPIKey:                    types.StringValue("prior-key"),
		ModelAPIBase:                   types.StringValue("https://example.invalid"),
		APIVersion:                     types.StringValue("v1"),
		ReasoningEffort:                types.StringValue("high"),
		ThinkingEnabled:                types.BoolValue(true),
		ThinkingBudgetTokens:           types.Int64Value(2048),
		MergeReasoningContentInChoices: types.BoolValue(true),
		AWSAccessKeyID:                 types.StringValue("access"),
		AWSSecretAccessKey:             types.StringValue("secret"),
		AWSRegionName:                  types.StringValue("us-east-1"),
		AWSSessionName:                 types.StringValue("session"),
		AWSRoleName:                    types.StringValue("role"),
		VertexProject:                  types.StringValue("project"),
		VertexLocation:                 types.StringValue("location"),
		VertexCredentials:              types.StringValue("credentials"),
		LiteLLMCredentialName:          types.StringValue("credential"),
		InputCostPerMillionTokens:      types.Float64Value(3),
		OutputCostPerMillionTokens:     types.Float64Value(4),
		InputCostPerPixel:              types.Float64Value(5),
		OutputCostPerPixel:             types.Float64Value(6),
		InputCostPerSecond:             types.Float64Value(7),
		OutputCostPerSecond:            types.Float64Value(8),
		Mode:                           types.StringNull(),
		AccessGroups:                   types.ListValueMust(types.StringType, []attr.Value{types.StringValue("group")}),
	}
	planned := ModelResourceModel{
		ID:                             prior.ID,
		ModelName:                      prior.ModelName,
		CustomLLMProvider:              prior.CustomLLMProvider,
		BaseModel:                      prior.BaseModel,
		Tier:                           types.StringValue("free"),
		ModelAPIKey:                    types.StringNull(),
		ModelAPIBase:                   types.StringNull(),
		APIVersion:                     types.StringNull(),
		ReasoningEffort:                types.StringNull(),
		ThinkingEnabled:                types.BoolValue(false),
		ThinkingBudgetTokens:           types.Int64Value(1024),
		MergeReasoningContentInChoices: types.BoolNull(),
		AWSAccessKeyID:                 types.StringNull(),
		AWSSecretAccessKey:             types.StringNull(),
		AWSRegionName:                  types.StringNull(),
		AWSSessionName:                 types.StringNull(),
		AWSRoleName:                    types.StringNull(),
		VertexProject:                  types.StringNull(),
		VertexLocation:                 types.StringNull(),
		VertexCredentials:              types.StringNull(),
		LiteLLMCredentialName:          types.StringNull(),
		InputCostPerMillionTokens:      types.Float64Null(),
		OutputCostPerMillionTokens:     types.Float64Null(),
		InputCostPerPixel:              types.Float64Null(),
		OutputCostPerPixel:             types.Float64Null(),
		InputCostPerSecond:             types.Float64Null(),
		OutputCostPerSecond:            types.Float64Null(),
		Mode:                           types.StringNull(),
		AccessGroups:                   types.ListValueMust(types.StringType, []attr.Value{}),
	}
	resource := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	if _, err := resource.patchModel(context.Background(), &planned, &prior, false, true); err != nil {
		t.Fatal(err)
	}

	params, ok := body["litellm_params"].(map[string]interface{})
	if !ok {
		t.Fatalf("litellm_params = %#v", body["litellm_params"])
	}
	for _, key := range []string{"api_key", "api_base", "api_version", "aws_access_key_id", "aws_secret_access_key", "aws_region_name", "aws_session_name", "aws_role_name", "vertex_project", "vertex_location", "vertex_credentials", "litellm_credential_name"} {
		if value, present := params[key]; !present || value != "" {
			t.Errorf("%s clear = %#v, present=%t", key, value, present)
		}
	}
	if params["reasoning_effort"] != "none" {
		t.Errorf("reasoning_effort clear = %#v", params["reasoning_effort"])
	}
	if params["merge_reasoning_content_in_choices"] != false {
		t.Errorf("merge reasoning clear = %#v", params["merge_reasoning_content_in_choices"])
	}
	thinking, ok := params["thinking"].(map[string]interface{})
	if !ok || thinking["type"] != "disabled" {
		t.Errorf("thinking clear = %#v", params["thinking"])
	}
	for _, key := range []string{"input_cost_per_token", "output_cost_per_token"} {
		value, present := params[key]
		if !present || value != nil {
			t.Errorf("%s clear = %#v, present=%t", key, value, present)
		}
	}
	for _, key := range []string{"input_cost_per_pixel", "output_cost_per_pixel", "input_cost_per_second", "output_cost_per_second", "tpm", "rpm"} {
		if _, present := params[key]; present {
			t.Errorf("unsupported %s clear was sent", key)
		}
	}

	info, ok := body["model_info"].(map[string]interface{})
	if !ok {
		t.Fatalf("model_info = %#v", body["model_info"])
	}
	if _, present := info["mode"]; present {
		t.Error("unsupported mode clear was sent")
	}
	if info["tier"] != "free" {
		t.Errorf("tier reset = %#v", info["tier"])
	}
	groups, ok := info["access_groups"].([]interface{})
	if !ok || len(groups) != 0 {
		t.Errorf("access_groups clear = %#v", info["access_groups"])
	}
}

func TestPartitionModelClearsRequiresDecryptedReadbackForSecrets(t *testing.T) {
	t.Parallel()
	patchVerified, readback := partitionModelClears(map[string]struct{}{
		"api_key": {}, "aws_session_name": {}, "thinking": {}, "api_base": {}, "access_groups": {},
	})
	for _, field := range []string{"api_key", "aws_session_name", "api_base"} {
		if _, ok := readback[field]; !ok {
			t.Errorf("%s was not assigned to decrypted readback verification", field)
		}
	}
	for _, field := range []string{"thinking", "access_groups"} {
		if _, ok := patchVerified[field]; !ok {
			t.Errorf("%s was not assigned to authoritative PATCH verification", field)
		}
	}
}

func TestVerifyModelPatchClearsRequiresAuthoritativeDocuments(t *testing.T) {
	t.Parallel()
	cleared := map[string]struct{}{"api_key": {}}
	if err := verifyModelPatchClears(map[string]interface{}{"status": "ok"}, cleared); err == nil {
		t.Fatal("clear accepted without authoritative response documents")
	}
	if err := verifyModelPatchClears(map[string]interface{}{
		"litellm_params": map[string]interface{}{"api_key": ""},
		"model_info":     map[string]interface{}{},
	}, cleared); err != nil {
		t.Fatalf("authoritative clear rejected: %v", err)
	}
}

func TestModelClearedFieldsUsesWireNames(t *testing.T) {
	t.Parallel()
	planned := ModelResourceModel{ModelAPIKey: types.StringNull(), ModelAPIBase: types.StringNull()}
	prior := ModelResourceModel{ModelAPIKey: types.StringValue("secret"), ModelAPIBase: types.StringValue("https://example.invalid")}
	cleared := modelClearedFields(planned, prior, false)
	for _, field := range []string{"api_key", "api_base"} {
		if _, ok := cleared[field]; !ok {
			t.Errorf("clear did not use %s wire name", field)
		}
	}
	for _, field := range []string{"model_api_key", "model_api_base"} {
		if _, ok := cleared[field]; ok {
			t.Errorf("schema name %s leaked into API clear verification", field)
		}
	}
}

func TestVerifyModelClearsRejectsRetainedValues(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		params map[string]interface{}
		info   map[string]interface{}
		field  string
	}{
		{"masked secret", map[string]interface{}{"api_key": "********"}, nil, "api_key"},
		{"reasoning", map[string]interface{}{"reasoning_effort": "high"}, nil, "reasoning_effort"},
		{"thinking", map[string]interface{}{"thinking": map[string]interface{}{"type": "enabled"}}, nil, "thinking"},
		{"cost", map[string]interface{}{"input_cost_per_token": 0.1}, nil, "input_cost_per_token"},
		{"mode", nil, map[string]interface{}{"mode": "chat"}, "mode"},
		{"access groups", nil, map[string]interface{}{"access_groups": []interface{}{"group"}}, "access_groups"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := verifyModelClears(test.params, test.info, map[string]struct{}{test.field: {}}); err == nil {
				t.Fatal("retained clear value was accepted")
			}
		})
	}
}
