package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestReadKeyResolvesUnknownOptionalComputedCollections(t *testing.T) {
	t.Parallel()

	// Test with flat response (backwards compatibility)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"max_budget":            10.0,
			"tpm_limit":             1000.0,
			"rpm_limit":             100.0,
			"blocked":               false,
			"organization_id":       "org-1",
			"max_parallel_requests": 5.0,
		})
	}))
	defer server.Close()

	r := &KeyResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := KeyResourceModel{
		ID:                       types.StringValue("key-123"),
		Key:                      types.StringValue("key-123"),
		Models:                   types.ListUnknown(types.StringType),
		AllowedRoutes:            types.ListUnknown(types.StringType),
		AllowedPassthroughRoutes: types.ListUnknown(types.StringType),
		AllowedCacheControls:     types.ListUnknown(types.StringType),
		Guardrails:               types.ListUnknown(types.StringType),
		Prompts:                  types.ListUnknown(types.StringType),
		EnforcedParams:           types.ListUnknown(types.StringType),
		Tags:                     types.ListUnknown(types.StringType),
		Metadata:                 types.MapUnknown(types.StringType),
		Aliases:                  types.MapUnknown(types.StringType),
		Config:                   types.MapUnknown(types.StringType),
		Permissions:              types.MapUnknown(types.StringType),
		ModelMaxBudget:           types.MapUnknown(types.Float64Type),
		ModelRPMLimit:            types.MapUnknown(types.Int64Type),
		ModelTPMLimit:            types.MapUnknown(types.Int64Type),
	}

	if err := r.readKey(context.Background(), &data); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	if data.Models.IsUnknown() {
		t.Fatal("models should be known after read")
	}
	if data.AllowedRoutes.IsUnknown() {
		t.Fatal("allowed_routes should be known after read")
	}
	if data.AllowedPassthroughRoutes.IsUnknown() {
		t.Fatal("allowed_passthrough_routes should be known after read")
	}
	if data.AllowedCacheControls.IsUnknown() {
		t.Fatal("allowed_cache_controls should be known after read")
	}
	if data.Guardrails.IsUnknown() {
		t.Fatal("guardrails should be known after read")
	}
	if data.Prompts.IsUnknown() {
		t.Fatal("prompts should be known after read")
	}
	if data.EnforcedParams.IsUnknown() {
		t.Fatal("enforced_params should be known after read")
	}
	if data.Tags.IsUnknown() {
		t.Fatal("tags should be known after read")
	}
	if data.Metadata.IsUnknown() {
		t.Fatal("metadata should be known after read")
	}
	if data.Aliases.IsUnknown() {
		t.Fatal("aliases should be known after read")
	}
	if data.Config.IsUnknown() {
		t.Fatal("config should be known after read")
	}
	if data.Permissions.IsUnknown() {
		t.Fatal("permissions should be known after read")
	}
	if data.ModelMaxBudget.IsUnknown() {
		t.Fatal("model_max_budget should be known after read")
	}
	if data.ModelRPMLimit.IsUnknown() {
		t.Fatal("model_rpm_limit should be known after read")
	}
	if data.ModelTPMLimit.IsUnknown() {
		t.Fatal("model_tpm_limit should be known after read")
	}
}

func TestReadKeyWithNestedInfoResponse(t *testing.T) {
	t.Parallel()

	// Test with nested "info" response matching actual LiteLLM API format
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key": "sk-test-key-123",
			"info": map[string]interface{}{
				"token":                  "sk-test-key-123",
				"key_alias":              "my-test-key",
				"spend":                  0.05,
				"max_budget":             100.0,
				"tpm_limit":              5000.0,
				"rpm_limit":              500.0,
				"blocked":                false,
				"organization_id":        "org-1",
				"team_id":                "team-1",
				"user_id":                "user-1",
				"models":                 []interface{}{"gpt-4", "gpt-3.5-turbo"},
				"aliases":                map[string]interface{}{"fast": "gpt-3.5-turbo"},
				"config":                 map[string]interface{}{},
				"permissions":            map[string]interface{}{},
				"allowed_routes":         []interface{}{"llm_api_routes"},
				"tags":                   []interface{}{"production"},
				"metadata":               map[string]interface{}{"env": "prod"},
				"guardrails":             []interface{}{},
				"prompts":                []interface{}{},
				"enforced_params":        []interface{}{},
				"model_max_budget":       map[string]interface{}{},
				"model_rpm_limit":        map[string]interface{}{},
				"model_tpm_limit":        map[string]interface{}{},
				"allowed_cache_controls": []interface{}{},
			},
		})
	}))
	defer server.Close()

	r := &KeyResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := KeyResourceModel{
		ID:                       types.StringValue("sk-test-key-123"),
		Key:                      types.StringValue("sk-test-key-123"),
		Models:                   types.ListUnknown(types.StringType),
		AllowedRoutes:            types.ListUnknown(types.StringType),
		AllowedPassthroughRoutes: types.ListUnknown(types.StringType),
		AllowedCacheControls:     types.ListUnknown(types.StringType),
		Guardrails:               types.ListUnknown(types.StringType),
		Prompts:                  types.ListUnknown(types.StringType),
		EnforcedParams:           types.ListUnknown(types.StringType),
		Tags:                     types.ListUnknown(types.StringType),
		Metadata:                 types.MapUnknown(types.StringType),
		Aliases:                  types.MapUnknown(types.StringType),
		Config:                   types.MapUnknown(types.StringType),
		Permissions:              types.MapUnknown(types.StringType),
		ModelMaxBudget:           types.MapUnknown(types.Float64Type),
		ModelRPMLimit:            types.MapUnknown(types.Int64Type),
		ModelTPMLimit:            types.MapUnknown(types.Int64Type),
	}

	if err := r.readKey(context.Background(), &data); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	// Verify key was extracted from top-level response
	if data.Key.ValueString() != "sk-test-key-123" {
		t.Fatalf("expected key 'sk-test-key-123', got '%s'", data.Key.ValueString())
	}

	// Verify fields were extracted from nested "info" block
	if data.KeyAlias.ValueString() != "my-test-key" {
		t.Fatalf("expected key_alias 'my-test-key', got '%s'", data.KeyAlias.ValueString())
	}
	if data.MaxBudget.ValueFloat64() != 100.0 {
		t.Fatalf("expected max_budget 100.0, got %f", data.MaxBudget.ValueFloat64())
	}
	if data.TeamID.ValueString() != "team-1" {
		t.Fatalf("expected team_id 'team-1', got '%s'", data.TeamID.ValueString())
	}
	if data.OrganizationID.ValueString() != "org-1" {
		t.Fatalf("expected organization_id 'org-1', got '%s'", data.OrganizationID.ValueString())
	}

	// Verify lists were populated from nested response
	if data.Models.IsUnknown() || data.Models.IsNull() {
		t.Fatal("models should be known and non-null after read with nested response")
	}
	if data.AllowedRoutes.IsUnknown() || data.AllowedRoutes.IsNull() {
		t.Fatal("allowed_routes should be known and non-null after read with nested response")
	}
	if data.Tags.IsUnknown() || data.Tags.IsNull() {
		t.Fatal("tags should be known and non-null after read with nested response")
	}

	// Verify all Unknown fields are resolved
	if data.Guardrails.IsUnknown() {
		t.Fatal("guardrails should be known after read")
	}
	if data.Prompts.IsUnknown() {
		t.Fatal("prompts should be known after read")
	}
	if data.EnforcedParams.IsUnknown() {
		t.Fatal("enforced_params should be known after read")
	}
	if data.Metadata.IsUnknown() {
		t.Fatal("metadata should be known after read")
	}
	if data.Aliases.IsUnknown() {
		t.Fatal("aliases should be known after read")
	}
	if data.Config.IsUnknown() {
		t.Fatal("config should be known after read")
	}
	if data.Permissions.IsUnknown() {
		t.Fatal("permissions should be known after read")
	}
	if data.ModelMaxBudget.IsUnknown() {
		t.Fatal("model_max_budget should be known after read")
	}
	if data.ModelRPMLimit.IsUnknown() {
		t.Fatal("model_rpm_limit should be known after read")
	}
	if data.ModelTPMLimit.IsUnknown() {
		t.Fatal("model_tpm_limit should be known after read")
	}
}
