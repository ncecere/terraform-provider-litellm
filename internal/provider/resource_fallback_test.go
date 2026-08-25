package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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
			"model":           "gpt-3.5-turbo",
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
			"model":           "my-model",
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

func TestParseFallbackImportIDFromSupportedRightSuffix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		importID, model, fallbackType string
	}{
		{"legacy-simple-model", "legacy-simple-model", "general"},
		{"simple:general", "simple", "general"},
		{"llama3:8b:general", "llama3:8b", "general"},
		{"model:general:context_window", "model:general", "context_window"},
		{"tenant/model?revision=1&weight=50%:雪:content_policy", "tenant/model?revision=1&weight=50%:雪", "content_policy"},
	}
	for _, test := range tests {
		t.Run(test.fallbackType+"/"+test.model, func(t *testing.T) {
			model, fallbackType, err := parseFallbackImportID(test.importID)
			if err != nil {
				t.Fatalf("parseFallbackImportID returned error: %v", err)
			}
			if model != test.model || fallbackType != test.fallbackType {
				t.Fatalf("parsed (%q, %q), want (%q, %q)", model, fallbackType, test.model, test.fallbackType)
			}
		})
	}
}

func TestParseFallbackImportIDRejectsInvalidIDsWithoutEchoingContent(t *testing.T) {
	t.Parallel()

	for name, importID := range map[string]string{
		"empty":                             "",
		"empty suffix":                      "sensitive-model:",
		"empty model":                       ":general",
		"unknown suffix":                    "sensitive-model:unknown-secret-type",
		"supported token before bad suffix": "sensitive-model:general:unknown-secret-type",
		"wrong case":                        "sensitive-model:GENERAL",
	} {
		t.Run(name, func(t *testing.T) {
			model, fallbackType, err := parseFallbackImportID(importID)
			if err == nil || model != "" || fallbackType != "" {
				t.Fatalf("parse result = (%q, %q, %v), want empty components and error", model, fallbackType, err)
			}
			for _, sensitive := range []string{"sensitive-model", "unknown-secret-type", importID} {
				if sensitive != "" && strings.Contains(err.Error(), sensitive) {
					t.Fatalf("diagnostic exposed import content: %v", err)
				}
			}
			if !strings.Contains(err.Error(), "<model>:<fallback_type>") && !strings.Contains(err.Error(), "general") {
				t.Fatalf("diagnostic is not actionable: %v", err)
			}
		})
	}
}

func TestFallbackEndpointEscapesSpecialModelExactlyOnce(t *testing.T) {
	t.Parallel()

	model := "tenant/route?revision=1&literal=%2F:雪"
	endpoint := fallbackEndpoint(model, "context_window")
	want := "/fallback/tenant%2Froute%3Frevision=1&literal=%252F:%E9%9B%AA?fallback_type=context_window"
	if endpoint != want {
		t.Fatalf("endpoint = %q, want %q", endpoint, want)
	}
	for model, wantPath := range map[string]string{
		".":  "/fallback/%2E?fallback_type=general",
		"..": "/fallback/%2E%2E?fallback_type=general",
	} {
		if got := fallbackEndpoint(model, "general"); got != wantPath {
			t.Errorf("dot endpoint = %q, want %q", got, wantPath)
		}
	}
}

func TestFallbackResourceAndDataSourceBuildEscapedSlashIdentityRequests(t *testing.T) {
	t.Parallel()

	// This transport-level test verifies exact-once escaping in every provider
	// call path. LiteLLM v1.98's non-path-capturing /fallback/{model} route
	// rejects decoded slash identities before its handler runs; it does not make
	// a slash-bearing identity lifecycle-capable on that server version.
	model := "tenant/route?revision=1&literal=%2F:雪"
	fallbackType := "content_policy"
	wantURI := fallbackEndpoint(model, fallbackType)
	requests := make(chan string, 3)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r.Method + " " + r.RequestURI
		if got := r.URL.Query().Get("fallback_type"); got != fallbackType {
			t.Errorf("fallback_type query = %q, want %q", got, fallbackType)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"model": model, "fallback_type": fallbackType, "fallback_models": []string{"secondary"},
		})
	}))
	defer server.Close()

	client := &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
	resourceData := &FallbackResourceModel{Model: types.StringValue(model), FallbackType: types.StringValue(fallbackType)}
	if err := (&FallbackResource{client: client}).readFallback(context.Background(), resourceData); err != nil {
		t.Fatalf("resource read: %v", err)
	}
	dataSourceData := &FallbackDataSourceModel{Model: types.StringValue(model), FallbackType: types.StringValue(fallbackType)}
	if err := (&FallbackDataSource{client: client}).readFallback(context.Background(), dataSourceData); err != nil {
		t.Fatalf("data source read: %v", err)
	}
	if err := client.DoRequestWithResponse(context.Background(), http.MethodDelete, fallbackEndpoint(model, fallbackType), nil, nil); err != nil {
		t.Fatalf("delete request: %v", err)
	}
	for _, method := range []string{http.MethodGet, http.MethodGet, http.MethodDelete} {
		if got := <-requests; got != method+" "+wantURI {
			t.Fatalf("request = %q, want %q", got, method+" "+wantURI)
		}
	}
}

func TestFallbackReadRemovesRemotelyDeletedSpecialIdentity(t *testing.T) {
	t.Parallel()

	model := "tenant/model%雪"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.RequestURI, fallbackEndpoint(model, "general"); got != want {
			t.Errorf("remote deletion request URI = %q, want %q", got, want)
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	ctx := context.Background()
	var schemaResponse resource.SchemaResponse
	resourceUnderTest := &FallbackResource{
		client:           &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()},
		readMaxAttempts:  1,
		readInitialDelay: 0,
		readMaxDelay:     0,
	}
	resourceUnderTest.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
	state := fallbackTestState(t, schemaResponse.Schema, FallbackResourceModel{
		ID:             types.StringValue(model + ":general"),
		Model:          types.StringValue(model),
		FallbackType:   types.StringValue("general"),
		FallbackModels: types.ListValueMust(types.StringType, []attr.Value{types.StringValue("secondary")}),
	})
	response := &resource.ReadResponse{State: state}
	resourceUnderTest.Read(ctx, resource.ReadRequest{State: state}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("remote deletion diagnostics: %v", response.Diagnostics)
	}
	if !response.State.Raw.IsNull() {
		t.Fatal("remote deletion did not remove resource state")
	}
}

func fallbackTestState(t *testing.T, schema resourceschema.Schema, model FallbackResourceModel) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	state := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil), Schema: schema}
	if diagnostics := state.Set(ctx, &model); diagnostics.HasError() {
		t.Fatalf("set fallback state: %v", diagnostics)
	}
	return state
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
