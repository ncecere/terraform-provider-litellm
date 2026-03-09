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

func TestFallbackBuildFallbackRequest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	list, _ := types.ListValue(types.StringType, []attr.Value{
		types.StringValue("gpt-4o"),
		types.StringValue("gpt-4o-mini"),
	})

	r := &FallbackResource{}
	data := &FallbackResourceModel{
		Model:          types.StringValue("gpt-3.5-turbo"),
		FallbackModels: list,
		FallbackType:   types.StringValue("general"),
	}

	req := r.buildFallbackRequest(ctx, data)

	if req["model"] != "gpt-3.5-turbo" {
		t.Errorf("model = %v, want gpt-3.5-turbo", req["model"])
	}
	if req["fallback_type"] != "general" {
		t.Errorf("fallback_type = %v, want general", req["fallback_type"])
	}
	models, ok := req["fallback_models"].([]string)
	if !ok {
		t.Fatalf("fallback_models type = %T, want []string", req["fallback_models"])
	}
	if len(models) != 2 || models[0] != "gpt-4o" || models[1] != "gpt-4o-mini" {
		t.Errorf("fallback_models = %v, want [gpt-4o, gpt-4o-mini]", models)
	}
}

func TestFallbackReadFallback_populatesState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"fallback_models": []interface{}{"gpt-4o", "gpt-4o-mini"},
			"fallback_type":   "general",
		})
	}))
	defer server.Close()

	res := &FallbackResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := &FallbackResourceModel{
		Model:        types.StringValue("gpt-3.5-turbo"),
		FallbackType: types.StringValue("general"),
	}

	if err := res.readFallback(context.Background(), data); err != nil {
		t.Fatalf("readFallback: %v", err)
	}

	if data.ID.ValueString() != "gpt-3.5-turbo:general" {
		t.Errorf("id = %s, want gpt-3.5-turbo:general", data.ID.ValueString())
	}
	if data.FallbackType.ValueString() != "general" {
		t.Errorf("fallback_type = %s, want general", data.FallbackType.ValueString())
	}
	elems := data.FallbackModels.Elements()
	if len(elems) != 2 {
		t.Fatalf("fallback_models length = %d, want 2", len(elems))
	}
	if elems[0].(types.String).ValueString() != "gpt-4o" || elems[1].(types.String).ValueString() != "gpt-4o-mini" {
		t.Errorf("fallback_models = %v", elems)
	}
}

func TestFallbackReadFallback_handlesEmptyFallbackModels(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"fallback_models": []interface{}{},
			"fallback_type":   "context_window",
		})
	}))
	defer server.Close()

	res := &FallbackResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := &FallbackResourceModel{
		Model:        types.StringValue("my-model"),
		FallbackType: types.StringValue("context_window"),
	}

	if err := res.readFallback(context.Background(), data); err != nil {
		t.Fatalf("readFallback: %v", err)
	}

	if data.ID.ValueString() != "my-model:context_window" {
		t.Errorf("id = %s, want my-model:context_window", data.ID.ValueString())
	}
	if len(data.FallbackModels.Elements()) != 0 {
		t.Errorf("fallback_models should be empty, got %d elements", len(data.FallbackModels.Elements()))
	}
}

func TestFallbackCreateSendsCorrectBody(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/fallback" {
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	ctx := context.Background()
	list, _ := types.ListValue(types.StringType, []attr.Value{types.StringValue("gpt-4o-mini")})

	res := &FallbackResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	plan := FallbackResourceModel{
		Model:          types.StringValue("test-model"),
		FallbackModels: list,
		FallbackType:   types.StringValue("general"),
	}

	req := res.buildFallbackRequest(ctx, &plan)
	if err := res.client.DoRequestWithResponse(ctx, "POST", "/fallback", req, nil); err != nil {
		t.Fatalf("POST /fallback: %v", err)
	}

	if capturedBody["model"] != "test-model" {
		t.Errorf("body model = %v, want test-model", capturedBody["model"])
	}
	if capturedBody["fallback_type"] != "general" {
		t.Errorf("body fallback_type = %v, want general", capturedBody["fallback_type"])
	}
	models, ok := capturedBody["fallback_models"].([]interface{})
	if !ok {
		t.Fatalf("body fallback_models = %T, want []interface{}", capturedBody["fallback_models"])
	}
	if len(models) != 1 || models[0].(string) != "gpt-4o-mini" {
		t.Errorf("body fallback_models = %v", models)
	}
}
