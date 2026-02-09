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

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"spend":                 0.0,
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
