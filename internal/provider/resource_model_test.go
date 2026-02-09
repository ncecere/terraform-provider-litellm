package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestReadModelResolvesUnknownOptionalComputedCollections(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"model_name": "text-embedding-3-small",
			"litellm_params": map[string]interface{}{
				"custom_llm_provider": "openai",
				"model":               "openai/text-embedding-3-small",
			},
			"model_info": map[string]interface{}{
				"base_model": "text-embedding-3-small",
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

func TestReadModelExtractsAdditionalLiteLLMParams(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
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
		ID:                      types.StringValue("model-456"),
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

	if got := additional["custom_flag"]; got != "true" {
		t.Fatalf("expected custom_flag=true, got %q", got)
	}
	if got := additional["max_retries"]; got != "3" {
		t.Fatalf("expected max_retries=3, got %q", got)
	}
}
