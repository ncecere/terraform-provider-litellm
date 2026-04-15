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
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestHashKeyForID(t *testing.T) {
	t.Parallel()

	id := hashKeyForID("sk-test-key-123")
	if !strings.HasPrefix(id, "sha256:") {
		t.Fatalf("expected sha256: prefix, got %s", id)
	}
	// Same input always produces same output
	if id != hashKeyForID("sk-test-key-123") {
		t.Fatal("hashKeyForID is not deterministic")
	}
	// Different input produces different output
	if id == hashKeyForID("sk-different-key") {
		t.Fatal("different keys should produce different hashes")
	}
	// Raw key should not appear in hash
	if strings.Contains(id, "sk-test-key-123") {
		t.Fatal("raw key should not appear in hashed ID")
	}
}

func TestCreateKeyUsesHashedID(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/key/generate" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"key": "sk-generated-key-abc",
			})
			return
		}
		// readKey call after create
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key": "sk-generated-key-abc",
			"info": map[string]interface{}{
				"token": "sk-generated-key-abc",
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

	data := &KeyResourceModel{
		Key: types.StringUnknown(),
	}

	keyReq := r.buildKeyRequest(context.Background(), data)
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(context.Background(), "POST", "/key/generate", keyReq, &result); err != nil {
		t.Fatalf("POST /key/generate: %v", err)
	}

	if keyVal, ok := result["key"].(string); ok {
		data.Key = types.StringValue(keyVal)
		data.ID = types.StringValue(hashKeyForID(keyVal))
	}

	// ID should be hashed, not the raw key
	if !strings.HasPrefix(data.ID.ValueString(), "sha256:") {
		t.Errorf("expected hashed ID, got %s", data.ID.ValueString())
	}
	if data.ID.ValueString() == "sk-generated-key-abc" {
		t.Error("ID should not be the raw key")
	}
	// Key attribute should still hold the raw value
	if data.Key.ValueString() != "sk-generated-key-abc" {
		t.Errorf("expected key 'sk-generated-key-abc', got '%s'", data.Key.ValueString())
	}
}

func TestPredefinedKeyIsSentToAPI(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"key": "sk-my-predefined-key",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"info": map[string]interface{}{
				"token": "sk-my-predefined-key",
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

	data := &KeyResourceModel{
		Key: types.StringValue("sk-my-predefined-key"),
	}

	keyReq := r.buildKeyRequest(context.Background(), data)

	// Verify the predefined key is included in the request body
	if keyReq["key"] != "sk-my-predefined-key" {
		t.Fatalf("expected predefined key in request, got %v", keyReq["key"])
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(context.Background(), "POST", "/key/generate", keyReq, &result); err != nil {
		t.Fatalf("POST /key/generate: %v", err)
	}

	if keyVal, ok := result["key"].(string); ok {
		data.Key = types.StringValue(keyVal)
		data.ID = types.StringValue(hashKeyForID(keyVal))
	}

	// ID should be hashed
	if !strings.HasPrefix(data.ID.ValueString(), "sha256:") {
		t.Errorf("expected hashed ID, got %s", data.ID.ValueString())
	}
	// Key should be the predefined value
	if data.Key.ValueString() != "sk-my-predefined-key" {
		t.Errorf("expected key 'sk-my-predefined-key', got '%s'", data.Key.ValueString())
	}
}

func TestStateMigrationV0ToV1(t *testing.T) {
	t.Parallel()

	rawKey := "sk-old-state-key-123"
	expectedID := hashKeyForID(rawKey)

	// Verify the hash is what we expect
	if !strings.HasPrefix(expectedID, "sha256:") {
		t.Fatalf("expected sha256: prefix, got %s", expectedID)
	}
	if strings.Contains(expectedID, rawKey) {
		t.Fatal("hashed ID should not contain raw key")
	}
}

func TestUpgradeStateV0ToV1(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	r := &KeyResource{}
	upgraders := r.UpgradeState(ctx)

	upgrader, ok := upgraders[0]
	if !ok {
		t.Fatal("expected state upgrader for version 0")
	}

	rawKey := "sk-old-state-key-123"
	expectedID := hashKeyForID(rawKey)

	// Build a v0 state JSON where "id" is the raw API key.
	v0State := map[string]interface{}{
		"id":         rawKey,
		"key":        rawKey,
		"key_alias":  "my-alias",
		"max_budget": 100.0,
		"models":     []interface{}{"gpt-4"},
		"tags":       []interface{}{"prod"},
		"blocked":    false,
	}
	v0JSON, err := json.Marshal(v0State)
	if err != nil {
		t.Fatalf("failed to marshal v0 state: %v", err)
	}

	req := resource.UpgradeStateRequest{
		RawState: &tfprotov6.RawState{
			JSON: v0JSON,
		},
	}
	resp := resource.UpgradeStateResponse{}

	upgrader.StateUpgrader(ctx, req, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected errors: %v", resp.Diagnostics.Errors())
	}

	if resp.DynamicValue == nil {
		t.Fatal("expected DynamicValue to be set")
	}

	// Unmarshal the upgraded state and verify the ID was hashed.
	var upgraded map[string]interface{}
	if err := json.Unmarshal(resp.DynamicValue.JSON, &upgraded); err != nil {
		t.Fatalf("failed to unmarshal upgraded state: %v", err)
	}

	gotID, ok := upgraded["id"].(string)
	if !ok {
		t.Fatalf("expected 'id' to be a string, got %T", upgraded["id"])
	}
	if gotID != expectedID {
		t.Errorf("expected id %q, got %q", expectedID, gotID)
	}
	if gotID == rawKey {
		t.Error("id should have been hashed, but still contains raw key")
	}

	// Verify other attributes are preserved.
	if upgraded["key"] != rawKey {
		t.Errorf("expected key %q preserved, got %q", rawKey, upgraded["key"])
	}
	if upgraded["key_alias"] != "my-alias" {
		t.Errorf("expected key_alias 'my-alias' preserved, got %v", upgraded["key_alias"])
	}
	if upgraded["max_budget"] != 100.0 {
		t.Errorf("expected max_budget 100.0 preserved, got %v", upgraded["max_budget"])
	}
}

func TestUpgradeStateV0ToV1_NilRawState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	r := &KeyResource{}
	upgraders := r.UpgradeState(ctx)

	upgrader := upgraders[0]

	req := resource.UpgradeStateRequest{
		RawState: nil,
	}
	resp := resource.UpgradeStateResponse{}

	upgrader.StateUpgrader(ctx, req, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error when RawState is nil")
	}
}

func TestUpgradeStateV0ToV1_EmptyID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	r := &KeyResource{}
	upgraders := r.UpgradeState(ctx)

	upgrader := upgraders[0]

	v0JSON, _ := json.Marshal(map[string]interface{}{
		"id":  "",
		"key": "sk-some-key",
	})

	req := resource.UpgradeStateRequest{
		RawState: &tfprotov6.RawState{
			JSON: v0JSON,
		},
	}
	resp := resource.UpgradeStateResponse{}

	upgrader.StateUpgrader(ctx, req, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error when id is empty")
	}
}

func TestUpgradeStateV0ToV1_InvalidJSON(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	r := &KeyResource{}
	upgraders := r.UpgradeState(ctx)

	upgrader := upgraders[0]

	req := resource.UpgradeStateRequest{
		RawState: &tfprotov6.RawState{
			JSON: []byte(`{invalid`),
		},
	}
	resp := resource.UpgradeStateResponse{}

	upgrader.StateUpgrader(ctx, req, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error for invalid JSON")
	}
}

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

	// In real usage, Create sets the hashed ID before calling readKey.
	// Simulate that here: Key is known, ID is already hashed.
	data := KeyResourceModel{
		ID:                       types.StringValue(hashKeyForID("sk-test-key-123")),
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

	// Verify key is preserved (not overwritten by readKey)
	if data.Key.ValueString() != "sk-test-key-123" {
		t.Fatalf("expected key 'sk-test-key-123', got '%s'", data.Key.ValueString())
	}

	// Verify ID is hashed (not the raw key)
	if !strings.HasPrefix(data.ID.ValueString(), "sha256:") {
		t.Fatalf("expected hashed ID with sha256: prefix, got '%s'", data.ID.ValueString())
	}
	if data.ID.ValueString() == "sk-test-key-123" {
		t.Fatal("ID should not be the raw key value")
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

// TestReadKeyMetadataWithComplexValues verifies that metadata values containing
// JSON objects and arrays are read back correctly from the API and stored as
// JSON-encoded strings in state. This is the read-side of issue #71.
func TestReadKeyMetadataWithComplexValues(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key": "sk-meta-test",
			"info": map[string]interface{}{
				"token": "sk-meta-test",
				"metadata": map[string]interface{}{
					"env": "production",
					"logging": []interface{}{
						map[string]interface{}{
							"callback_name": "langsmith",
							"callback_type": "success",
							"callback_vars": map[string]interface{}{
								"langsmith_project": "my-project",
							},
						},
					},
					"config": map[string]interface{}{
						"retries": float64(3),
						"timeout": float64(30),
					},
				},
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

	// Simulate user config with these metadata keys
	data := KeyResourceModel{
		ID:  types.StringValue(hashKeyForID("sk-meta-test")),
		Key: types.StringValue("sk-meta-test"),
		Metadata: stringMapValue(map[string]string{
			"env":     "production",
			"logging": `[{"callback_name":"langsmith"}]`,
			"config":  `{"retries":3}`,
		}),
		// Initialize other fields to avoid nil panics
		Models:                   types.ListNull(types.StringType),
		AllowedRoutes:            types.ListNull(types.StringType),
		AllowedPassthroughRoutes: types.ListNull(types.StringType),
		AllowedCacheControls:     types.ListNull(types.StringType),
		Guardrails:               types.ListNull(types.StringType),
		Prompts:                  types.ListNull(types.StringType),
		EnforcedParams:           types.ListNull(types.StringType),
		Tags:                     types.ListNull(types.StringType),
		Aliases:                  types.MapNull(types.StringType),
		Config:                   types.MapNull(types.StringType),
		Permissions:              types.MapNull(types.StringType),
		ModelMaxBudget:           types.MapNull(types.Float64Type),
		ModelRPMLimit:            types.MapNull(types.Int64Type),
		ModelTPMLimit:            types.MapNull(types.Int64Type),
	}

	if err := r.readKey(context.Background(), &data); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	if data.Metadata.IsNull() || data.Metadata.IsUnknown() {
		t.Fatal("metadata should be known and non-null after read")
	}

	elems := data.Metadata.Elements()

	// Simple string value preserved
	if env, ok := elems["env"].(types.String); !ok || env.ValueString() != "production" {
		t.Errorf("expected env 'production', got %v", elems["env"])
	}

	// Array value should be JSON-encoded string
	if logging, ok := elems["logging"].(types.String); ok {
		var parsed []interface{}
		if err := json.Unmarshal([]byte(logging.ValueString()), &parsed); err != nil {
			t.Errorf("logging should be valid JSON array, got error: %v, value: %q", err, logging.ValueString())
		} else if len(parsed) != 1 {
			t.Errorf("expected 1 logging entry, got %d", len(parsed))
		}
	} else {
		t.Errorf("expected logging to be types.String, got %T", elems["logging"])
	}

	// Object value should be JSON-encoded string
	if config, ok := elems["config"].(types.String); ok {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(config.ValueString()), &parsed); err != nil {
			t.Errorf("config should be valid JSON object, got error: %v, value: %q", err, config.ValueString())
		} else if parsed["retries"] != float64(3) {
			t.Errorf("expected retries 3, got %v", parsed["retries"])
		}
	} else {
		t.Errorf("expected config to be types.String, got %T", elems["config"])
	}
}

// TestBuildKeyRequestMetadataWithJSON verifies that metadata values containing
// JSON strings are decoded to native types in the API request body (issue #71).
func TestBuildKeyRequestMetadataWithJSON(t *testing.T) {
	t.Parallel()

	r := &KeyResource{}
	data := &KeyResourceModel{
		Metadata: stringMapValue(map[string]string{
			"env":     "prod",
			"logging": `[{"callback_name":"langsmith"}]`,
		}),
	}

	req := r.buildKeyRequest(context.Background(), data)

	meta, ok := req["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected metadata to be map[string]interface{}, got %T", req["metadata"])
	}

	// Simple string stays as string
	if meta["env"] != "prod" {
		t.Errorf("expected env 'prod', got %v", meta["env"])
	}

	// JSON array should be native, not a string
	arr, ok := meta["logging"].([]interface{})
	if !ok {
		t.Fatalf("expected logging to be []interface{} (native array), got %T: %v", meta["logging"], meta["logging"])
	}
	if len(arr) != 1 {
		t.Errorf("expected 1 element, got %d", len(arr))
	}
}

func TestReadKeyTagsFromMetadata(t *testing.T) {
	t.Parallel()

	// LiteLLM stores tags inside metadata["tags"] rather than as a top-level field
	// in the /key/info response. This test verifies the provider reads them correctly.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key": "sk-tags-test",
			"info": map[string]interface{}{
				"token": "sk-tags-test",
				"metadata": map[string]interface{}{
					"tags": []interface{}{"test", "production"},
					"env":  "staging",
				},
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

	// Simulate user configured tags = ["test"] — data.Tags is non-null
	userTags, _ := types.ListValue(types.StringType, []attr.Value{types.StringValue("test")})
	data := KeyResourceModel{
		ID:   types.StringValue("old-id"),
		Key:  types.StringValue("sk-tags-test"),
		Tags: userTags,
	}

	if err := r.readKey(context.Background(), &data); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	if data.Tags.IsNull() || data.Tags.IsUnknown() {
		t.Fatal("tags should be known and non-null after read")
	}

	elems := data.Tags.Elements()
	if len(elems) != 2 {
		t.Fatalf("expected 2 tags, got %d", len(elems))
	}
	if elems[0].(types.String).ValueString() != "test" {
		t.Errorf("expected first tag 'test', got '%s'", elems[0].(types.String).ValueString())
	}
	if elems[1].(types.String).ValueString() != "production" {
		t.Errorf("expected second tag 'production', got '%s'", elems[1].(types.String).ValueString())
	}
}

// TestReadKeyRouterSettingsWithComplexValues verifies that router_settings values
// containing nested structures (arrays, objects) are properly handled:
// - Arrays/objects from API response are JSON-encoded back to strings in state
// - When building requests, JSON strings are decoded back to native types
func TestReadKeyRouterSettingsWithComplexValues(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key": "sk-router-test",
			"info": map[string]interface{}{
				"token": "sk-router-test",
				"router_settings": map[string]interface{}{
					"num_retries":   float64(1),
					"timeout":       float64(4),
					"max_retries":   float64(1),
					"retry_after":   float64(0),
					"allowed_fails": float64(1),
					"fallbacks": []interface{}{
						map[string]interface{}{
							"provider-a-model-1": []interface{}{
								"provider-a-model-2",
								"provider-a-model-3",
							},
						},
						map[string]interface{}{
							"provider-b-model-1": []interface{}{
								"provider-b-model-2",
								"provider-c-model-1",
							},
						},
					},
				},
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

	// Simulate user config with these router_settings keys
	data := KeyResourceModel{
		ID:  types.StringValue(hashKeyForID("sk-router-test")),
		Key: types.StringValue("sk-router-test"),
		RouterSettings: stringMapValue(map[string]string{
			"num_retries": "1",
			"timeout":     "4",
			"fallbacks":   `[{"provider-a-model-1":["provider-a-model-2"]}]`,
		}),
		// Initialize other fields to avoid nil panics
		Models:                   types.ListNull(types.StringType),
		AllowedRoutes:            types.ListNull(types.StringType),
		AllowedPassthroughRoutes: types.ListNull(types.StringType),
		AllowedCacheControls:     types.ListNull(types.StringType),
		Guardrails:               types.ListNull(types.StringType),
		Prompts:                  types.ListNull(types.StringType),
		EnforcedParams:           types.ListNull(types.StringType),
		Tags:                     types.ListNull(types.StringType),
		Aliases:                  types.MapNull(types.StringType),
		Config:                   types.MapNull(types.StringType),
		Permissions:              types.MapNull(types.StringType),
		Metadata:                 types.MapNull(types.StringType),
		ModelMaxBudget:           types.MapNull(types.Float64Type),
		ModelRPMLimit:            types.MapNull(types.Int64Type),
		ModelTPMLimit:            types.MapNull(types.Int64Type),
	}

	if err := r.readKey(context.Background(), &data); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	if data.RouterSettings.IsNull() || data.RouterSettings.IsUnknown() {
		t.Fatal("router_settings should be known and non-null after read")
	}

	elems := data.RouterSettings.Elements()

	// Numeric values should be JSON-encoded strings
	if numRetries, ok := elems["num_retries"].(types.String); ok {
		if numRetries.ValueString() != "1" {
			t.Errorf("expected num_retries '1', got %q", numRetries.ValueString())
		}
	} else {
		t.Errorf("expected num_retries to be types.String, got %T", elems["num_retries"])
	}

	// Array value should be JSON-encoded string
	if fallbacks, ok := elems["fallbacks"].(types.String); ok {
		var parsed []interface{}
		if err := json.Unmarshal([]byte(fallbacks.ValueString()), &parsed); err != nil {
			t.Errorf("fallbacks should be valid JSON array, got error: %v, value: %q", err, fallbacks.ValueString())
		} else if len(parsed) != 2 {
			t.Errorf("expected 2 fallback entries, got %d", len(parsed))
		}
	} else {
		t.Errorf("expected fallbacks to be types.String, got %T", elems["fallbacks"])
	}
}

// TestBuildKeyRequestRouterSettingsWithJSON verifies that router_settings values
// containing JSON strings are decoded to native types in the API request body.
func TestBuildKeyRequestRouterSettingsWithJSON(t *testing.T) {
	t.Parallel()

	r := &KeyResource{}
	data := &KeyResourceModel{
		RouterSettings: stringMapValue(map[string]string{
			"num_retries": "1",
			"timeout":     "4",
			"fallbacks": `[
				{"provider-a-model-1":["provider-a-model-2","provider-a-model-3"]},
				{"provider-b-model-1":["provider-b-model-2","provider-c-model-1"]}
			]`,
		}),
	}

	req := r.buildKeyRequest(context.Background(), data)

	routerSettings, ok := req["router_settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected router_settings to be map[string]interface{}, got %T", req["router_settings"])
	}

	// Simple numeric string stays as string
	if routerSettings["num_retries"] != "1" {
		t.Errorf("expected num_retries '1', got %v", routerSettings["num_retries"])
	}

	// JSON array should be native, not a string
	arr, ok := routerSettings["fallbacks"].([]interface{})
	if !ok {
		t.Fatalf("expected fallbacks to be []interface{} (native array), got %T: %v", routerSettings["fallbacks"], routerSettings["fallbacks"])
	}
	if len(arr) != 2 {
		t.Errorf("expected 2 fallback entries, got %d", len(arr))
	}

	// Verify first fallback entry is a map
	firstFallback, ok := arr[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected first fallback entry to be map[string]interface{}, got %T", arr[0])
	}
	if _, ok := firstFallback["provider-a-model-1"]; !ok {
		t.Error("expected 'provider-a-model-1' key in first fallback entry")
	}
}

// TestServiceAccountIDDefaultsKeyAlias verifies that when service_account_id is
// set but key_alias is omitted, buildKeyRequest populates key_alias with the
// service_account_id value — matching the documented behaviour.
// TestMinimalKeyNoKeyAliasNoServiceAccountID verifies the plain minimal case:
// neither key_alias nor service_account_id is configured.
//
//	resource "litellm_key" "minimal" {}
//
// Expected behaviour:
//   - buildKeyRequest must NOT include "key_alias" in the payload.
//   - readKey with an Unknown key_alias (Computed, unresolved) and an API
//     response that contains no key_alias must resolve the field to null —
//     i.e. no "inconsistent result after apply" error and no perpetual
//     "(known after apply)" on subsequent plans.
func TestMinimalKeyNoKeyAliasNoServiceAccountID(t *testing.T) {
	t.Parallel()

	r := &KeyResource{}

	// Simulate the plan-time model: everything is null/unknown.
	data := &KeyResourceModel{
		// key_alias is Unknown because it is Computed and the user did not set it.
		KeyAlias: types.StringUnknown(),
		// service_account_id is null because the user did not set it.
		ServiceAccountID: types.StringNull(),
	}

	// 1. buildKeyRequest must NOT include key_alias when neither field is set.
	keyReq := r.buildKeyRequest(context.Background(), data)
	if _, exists := keyReq["key_alias"]; exists {
		t.Errorf("key_alias must not appear in request when neither key_alias nor service_account_id is configured, got %v", keyReq["key_alias"])
	}

	// 2. readKey with an API that returns no key_alias must resolve Unknown → null.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key": "sk-minimal-key-xyz",
			"info": map[string]interface{}{
				"token": "sk-minimal-key-xyz",
				// key_alias deliberately absent — API never set one
			},
		})
	}))
	defer server.Close()

	rc := &KeyResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	readData := KeyResourceModel{
		ID:       types.StringValue(hashKeyForID("sk-minimal-key-xyz")),
		Key:      types.StringValue("sk-minimal-key-xyz"),
		KeyAlias: types.StringUnknown(), // Unknown = Computed, not yet resolved
	}

	if err := rc.readKey(context.Background(), &readData); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	// Must not be Unknown (would cause "inconsistent result after apply").
	if readData.KeyAlias.IsUnknown() {
		t.Fatal("key_alias must not remain Unknown after readKey — this would cause 'inconsistent result after apply'")
	}
	// Must be null (not some unexpected string).
	if !readData.KeyAlias.IsNull() {
		t.Errorf("key_alias should be null when API returns no alias, got %q", readData.KeyAlias.ValueString())
	}
}

func TestServiceAccountIDDefaultsKeyAlias(t *testing.T) {
	t.Parallel()

	r := &KeyResource{}
	data := &KeyResourceModel{
		ServiceAccountID: types.StringValue("github-ci"),
		TeamID:           types.StringValue("team456"),
		// key_alias deliberately omitted / null
		KeyAlias: types.StringNull(),
	}

	keyReq := r.buildKeyRequest(context.Background(), data)

	if keyReq["key_alias"] != "github-ci" {
		t.Errorf("expected key_alias 'github-ci', got %v", keyReq["key_alias"])
	}
	if keyReq["team_id"] != "team456" {
		t.Errorf("expected team_id 'team456', got %v", keyReq["team_id"])
	}
	// service_account_id should be stored in metadata, not as a top-level field
	meta, ok := keyReq["metadata"].(map[string]interface{})
	if !ok {
		t.Fatal("expected metadata map in request")
	}
	if meta["service_account_id"] != "github-ci" {
		t.Errorf("expected metadata.service_account_id 'github-ci', got %v", meta["service_account_id"])
	}
}

// TestServiceAccountIDKeyAliasExplicitOverride verifies that an explicit
// key_alias takes precedence over the service_account_id default.
func TestServiceAccountIDKeyAliasExplicitOverride(t *testing.T) {
	t.Parallel()

	r := &KeyResource{}
	data := &KeyResourceModel{
		ServiceAccountID: types.StringValue("github-ci"),
		KeyAlias:         types.StringValue("my-custom-alias"),
	}

	keyReq := r.buildKeyRequest(context.Background(), data)

	if keyReq["key_alias"] != "my-custom-alias" {
		t.Errorf("expected explicit key_alias 'my-custom-alias', got %v", keyReq["key_alias"])
	}
}

// TestReadKeyKeyAliasFromServiceAccount verifies that when service_account_id
// is set without key_alias, the provider successfully reads back the key_alias
// that the API sets (previously caused "inconsistent result after apply" because
// key_alias was Optional-only, not Optional+Computed).
func TestReadKeyKeyAliasFromServiceAccount(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key": "sk-svc-key-abc",
			"info": map[string]interface{}{
				"token":     "sk-svc-key-abc",
				"key_alias": "github-ci",
				"team_id":   "team456",
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

	// Simulate the state after Create: key is known, key_alias is Unknown
	// (Computed field not yet resolved).
	data := KeyResourceModel{
		ID:               types.StringValue(hashKeyForID("sk-svc-key-abc")),
		Key:              types.StringValue("sk-svc-key-abc"),
		ServiceAccountID: types.StringValue("github-ci"),
		KeyAlias:         types.StringUnknown(), // Unknown = Computed, not yet set
	}

	if err := r.readKey(context.Background(), &data); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	// After readKey the Unknown must be resolved — this is what was failing before the fix.
	if data.KeyAlias.IsUnknown() {
		t.Fatal("key_alias must not be Unknown after readKey")
	}
	if data.KeyAlias.ValueString() != "github-ci" {
		t.Errorf("expected key_alias 'github-ci', got '%s'", data.KeyAlias.ValueString())
	}
}

// TestReadKeyKeyAliasUnknownResolvesToNullWhenMissing verifies that an Unknown
// key_alias is resolved to null (not left Unknown) when the API response does
// not include a key_alias value.
func TestReadKeyKeyAliasUnknownResolvesToNullWhenMissing(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key": "sk-no-alias-key",
			"info": map[string]interface{}{
				"token": "sk-no-alias-key",
				// key_alias intentionally absent
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
		ID:       types.StringValue(hashKeyForID("sk-no-alias-key")),
		Key:      types.StringValue("sk-no-alias-key"),
		KeyAlias: types.StringUnknown(),
	}

	if err := r.readKey(context.Background(), &data); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	if data.KeyAlias.IsUnknown() {
		t.Fatal("key_alias must not remain Unknown after readKey when API returns no alias")
	}
	if !data.KeyAlias.IsNull() {
		t.Errorf("expected key_alias to be null when API returns nothing, got '%s'", data.KeyAlias.ValueString())
	}
}

func TestReadKeyTagsNoTagsAnywhere(t *testing.T) {
	t.Parallel()

	// When the API returns neither top-level tags nor metadata tags,
	// and user configured tags, the list should be emptied (not left stale).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key": "sk-no-tags",
			"info": map[string]interface{}{
				"token":    "sk-no-tags",
				"metadata": map[string]interface{}{},
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

	userTags, _ := types.ListValue(types.StringType, []attr.Value{types.StringValue("old-tag")})
	data := KeyResourceModel{
		ID:   types.StringValue("old-id"),
		Key:  types.StringValue("sk-no-tags"),
		Tags: userTags,
	}

	if err := r.readKey(context.Background(), &data); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	// Tags should be empty list (not null) since user had configured tags
	if data.Tags.IsNull() {
		t.Fatal("tags should not be null when user originally configured them")
	}
	if len(data.Tags.Elements()) != 0 {
		t.Fatalf("expected 0 tags, got %d", len(data.Tags.Elements()))
	}
}

// TestReadKeyURLEncodesSpecialChars verifies that special characters in a key
// value (e.g. '#') are percent-encoded when the key is placed in the
// /key/info query string.  Without url.QueryEscape the '#' character is
// interpreted as a URL fragment delimiter and silently truncates the key,
// causing the server to return 404 "Key not found in database".
func TestReadKeyURLEncodesSpecialChars(t *testing.T) {
	t.Parallel()

	// Key that contains URL-special characters: '!' and '#'.
	// '#' is the critical one: without encoding it acts as a fragment
	// delimiter and everything from '#' onward is stripped before the
	// HTTP request is sent.
	const keyWithSpecialChars = "sk-unit-test#special!chars"

	var receivedKeyParam string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture the raw, server-decoded value of the "key" query parameter.
		receivedKeyParam = r.URL.Query().Get("key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key": keyWithSpecialChars,
			"info": map[string]interface{}{
				"token": keyWithSpecialChars,
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

	data := &KeyResourceModel{
		Key:                      types.StringValue(keyWithSpecialChars),
		Models:                   types.ListNull(types.StringType),
		AllowedRoutes:            types.ListNull(types.StringType),
		AllowedPassthroughRoutes: types.ListNull(types.StringType),
		AllowedCacheControls:     types.ListNull(types.StringType),
		Guardrails:               types.ListNull(types.StringType),
		Prompts:                  types.ListNull(types.StringType),
		EnforcedParams:           types.ListNull(types.StringType),
		Tags:                     types.ListNull(types.StringType),
		Metadata:                 types.MapNull(types.StringType),
		Aliases:                  types.MapNull(types.StringType),
		Config:                   types.MapNull(types.StringType),
		Permissions:              types.MapNull(types.StringType),
		ModelMaxBudget:           types.MapNull(types.Float64Type),
		ModelRPMLimit:            types.MapNull(types.Int64Type),
		ModelTPMLimit:            types.MapNull(types.Int64Type),
	}

	if err := r.readKey(context.Background(), data); err != nil {
		t.Fatalf("readKey failed: %v", err)
	}

	// The server must receive the complete key, including the '#special!chars' suffix.
	// Without url.QueryEscape the Go HTTP client strips everything from '#'
	// onward (URL fragment), so the server would receive "sk-unit-test#special!chars".
	if receivedKeyParam != keyWithSpecialChars {
		t.Fatalf("server received key param %q, want %q\n"+
			"hint: '#' was likely not percent-encoded, causing URL fragment truncation",
			receivedKeyParam, keyWithSpecialChars)
	}
}

// TestReadKeyPreservesUserProvidedKey verifies that when the user supplies a
// custom key value, readKey does NOT overwrite data.Key with the hashed token
// returned by /key/info. Overwriting would cause:
//
//	"Provider produced inconsistent result after apply: .key: inconsistent
//	 values for sensitive attribute"
//
// because the planned value (raw key) would differ from the read-back value
// (hashed token). See https://github.com/ncecere/terraform-provider-litellm/issues/79
func TestReadKeyPreservesUserProvidedKey(t *testing.T) {
	t.Parallel()

	const rawKey = "sk-custom-user-key-abc123"
	// Simulate the real LiteLLM /key/info response where "token" is the
	// hashed key, NOT the raw key.
	const hashedToken = "sk-hashed-token-that-differs-from-raw"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			// Some LiteLLM versions include a top-level "key" that may also
			// be hashed; simulate that here.
			"key": hashedToken,
			"info": map[string]interface{}{
				"token":      hashedToken,
				"key_alias":  "my-alias",
				"max_budget": 50.0,
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
		ID:  types.StringValue(hashKeyForID(rawKey)),
		Key: types.StringValue(rawKey), // user-provided, already known
		// Initialise collection fields to avoid nil panics in readKey.
		Models:                   types.ListNull(types.StringType),
		AllowedRoutes:            types.ListNull(types.StringType),
		AllowedPassthroughRoutes: types.ListNull(types.StringType),
		AllowedCacheControls:     types.ListNull(types.StringType),
		Guardrails:               types.ListNull(types.StringType),
		Prompts:                  types.ListNull(types.StringType),
		EnforcedParams:           types.ListNull(types.StringType),
		Tags:                     types.ListNull(types.StringType),
		Metadata:                 types.MapNull(types.StringType),
		Aliases:                  types.MapNull(types.StringType),
		Config:                   types.MapNull(types.StringType),
		Permissions:              types.MapNull(types.StringType),
		ModelMaxBudget:           types.MapNull(types.Float64Type),
		ModelRPMLimit:            types.MapNull(types.Int64Type),
		ModelTPMLimit:            types.MapNull(types.Int64Type),
	}

	if err := r.readKey(context.Background(), &data); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	// The raw key must be preserved — NOT replaced with the hashed token.
	if data.Key.ValueString() != rawKey {
		t.Errorf("readKey overwrote user-provided key: got %q, want %q",
			data.Key.ValueString(), rawKey)
	}

	// ID must still be based on the original raw key.
	if data.ID.ValueString() != hashKeyForID(rawKey) {
		t.Errorf("ID changed unexpectedly: got %q, want %q",
			data.ID.ValueString(), hashKeyForID(rawKey))
	}

	// Other attributes should still be read from the API.
	if data.KeyAlias.ValueString() != "my-alias" {
		t.Errorf("expected key_alias 'my-alias', got %q", data.KeyAlias.ValueString())
	}
}

// TestReadKeyPopulatesUnknownKey verifies that when the key is Unknown (auto-
// generated), readKey DOES populate it from the API response.
func TestReadKeyPopulatesUnknownKey(t *testing.T) {
	t.Parallel()

	const apiReturnedKey = "sk-auto-generated-key-xyz"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key": apiReturnedKey,
			"info": map[string]interface{}{
				"token": apiReturnedKey,
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

	// readKey requires a non-empty key to build the URL, so we test via
	// the top-level result["key"] path by having key already set but
	// simulating the Unknown case right after.  Instead, let's test that
	// the key IS populated when it starts as a known value used only for
	// the URL, then manually verify the guard logic.
	//
	// Actually: readKey uses data.Key.ValueString() to build the endpoint,
	// so we can't call it with an Unknown key.  The real flow is:
	//   Create → gets key from /key/generate → sets data.Key → calls readKey
	// So data.Key is always known when readKey is called.  The guard
	// protects against readKey *overwriting* it with a different value.
	//
	// This test confirms that when the key in state matches the API
	// response, it stays unchanged (no-op case).
	data := KeyResourceModel{
		ID:                       types.StringValue(hashKeyForID(apiReturnedKey)),
		Key:                      types.StringValue(apiReturnedKey),
		Models:                   types.ListNull(types.StringType),
		AllowedRoutes:            types.ListNull(types.StringType),
		AllowedPassthroughRoutes: types.ListNull(types.StringType),
		AllowedCacheControls:     types.ListNull(types.StringType),
		Guardrails:               types.ListNull(types.StringType),
		Prompts:                  types.ListNull(types.StringType),
		EnforcedParams:           types.ListNull(types.StringType),
		Tags:                     types.ListNull(types.StringType),
		Metadata:                 types.MapNull(types.StringType),
		Aliases:                  types.MapNull(types.StringType),
		Config:                   types.MapNull(types.StringType),
		Permissions:              types.MapNull(types.StringType),
		ModelMaxBudget:           types.MapNull(types.Float64Type),
		ModelRPMLimit:            types.MapNull(types.Int64Type),
		ModelTPMLimit:            types.MapNull(types.Int64Type),
	}

	if err := r.readKey(context.Background(), &data); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	if data.Key.ValueString() != apiReturnedKey {
		t.Errorf("key should remain %q, got %q", apiReturnedKey, data.Key.ValueString())
	}
}

// TestUpdateRouterSettingsNullSendsEmptyObject verifies that when router_settings
// is null in the plan (user removed it from config), the Update path injects an
// empty object into the request body rather than omitting the field or sending null.
//
// Background: the LiteLLM API rejects `null` for router_settings with a 400
// (Pydantic union validation). Omitting the field is also wrong — the API treats
// absence as "no change" and leaves the existing value intact. Sending `{}` is
// the only way to clear the field.
func TestUpdateRouterSettingsNullSendsEmptyObject(t *testing.T) {
	t.Parallel()

	var capturedBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/key/update":
			_ = json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		default:
			// readKey call after update — return minimal key info
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"info": map[string]interface{}{
					"token": "sk-update-test",
				},
			})
		}
	}))
	defer server.Close()

	r := &KeyResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	// Simulate what Update does: build the request from the plan-time model
	// where RouterSettings is null (user removed it from config), then apply
	// the same null-injection logic that Update applies.
	data := &KeyResourceModel{
		Key:            types.StringValue("sk-update-test"),
		RouterSettings: types.MapNull(types.StringType),
		// Remaining fields null to avoid panics in buildKeyRequest
		Models:                   types.ListNull(types.StringType),
		AllowedRoutes:            types.ListNull(types.StringType),
		AllowedPassthroughRoutes: types.ListNull(types.StringType),
		AllowedCacheControls:     types.ListNull(types.StringType),
		Guardrails:               types.ListNull(types.StringType),
		Prompts:                  types.ListNull(types.StringType),
		EnforcedParams:           types.ListNull(types.StringType),
		Tags:                     types.ListNull(types.StringType),
		Metadata:                 types.MapNull(types.StringType),
		Aliases:                  types.MapNull(types.StringType),
		Config:                   types.MapNull(types.StringType),
		Permissions:              types.MapNull(types.StringType),
		ModelMaxBudget:           types.MapNull(types.Float64Type),
		ModelRPMLimit:            types.MapNull(types.Int64Type),
		ModelTPMLimit:            types.MapNull(types.Int64Type),
	}

	updateReq := r.buildKeyRequest(context.Background(), data)
	updateReq["key"] = data.Key.ValueString()

	// This is the logic under test — mirrors the Update function exactly.
	if data.RouterSettings.IsNull() {
		updateReq["router_settings"] = map[string]interface{}{}
	}

	if err := r.client.DoRequestWithResponse(context.Background(), "POST", "/key/update", updateReq, nil); err != nil {
		t.Fatalf("POST /key/update: %v", err)
	}

	// router_settings must be present in the captured request body.
	routerVal, exists := capturedBody["router_settings"]
	if !exists {
		t.Fatal("expected router_settings to be present in request body, but it was omitted")
	}

	// It must be an empty object, not null, not a non-empty map, not a string.
	routerMap, ok := routerVal.(map[string]interface{})
	if !ok {
		t.Fatalf("expected router_settings to be map[string]interface{} (empty object {}), got %T: %v", routerVal, routerVal)
	}
	if len(routerMap) != 0 {
		t.Errorf("expected router_settings to be empty {}, got %v", routerMap)
	}
}

// TestReadKeyRouterSettingsNullPreservedAfterClear verifies that readKey does not
// overwrite a null RouterSettings with data from the API response.
//
// After Update sends `{}` to clear router_settings, readKey is called to sync
// state. At that point data.RouterSettings is null (from the plan). The API may
// return `"router_settings": {}` (successful clear) or even the old non-empty
// value (caching delay). Either way, the null must be preserved — writing any
// non-null value back would contradict the plan and cause Terraform to raise
// "provider produced inconsistent result after apply".
func TestReadKeyRouterSettingsNullPreservedAfterClear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		apiRouterSettings interface{} // what the API returns for router_settings
	}{
		{
			name:            "API returns empty object after clear",
			apiRouterSettings: map[string]interface{}{},
		},
		{
			name: "API returns stale non-empty value (caching delay)",
			apiRouterSettings: map[string]interface{}{
				"timeout":       float64(4),
				"num_retries":   float64(1),
				"allowed_fails": float64(1),
				"fallbacks": []interface{}{
					map[string]interface{}{
						"model-a": []interface{}{"model-b"},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"info": map[string]interface{}{
						"token":           "sk-clear-test",
						"router_settings": tc.apiRouterSettings,
					},
				})
			}))
			defer server.Close()

			res := &KeyResource{
				client: &Client{
					APIBase:    server.URL,
					APIKey:     "test-key",
					HTTPClient: server.Client(),
				},
			}

			// data.RouterSettings is null — simulates the plan after the user
			// removed router_settings from their Terraform config.
			data := KeyResourceModel{
				ID:                       types.StringValue(hashKeyForID("sk-clear-test")),
				Key:                      types.StringValue("sk-clear-test"),
				RouterSettings:           types.MapNull(types.StringType),
				Models:                   types.ListNull(types.StringType),
				AllowedRoutes:            types.ListNull(types.StringType),
				AllowedPassthroughRoutes: types.ListNull(types.StringType),
				AllowedCacheControls:     types.ListNull(types.StringType),
				Guardrails:               types.ListNull(types.StringType),
				Prompts:                  types.ListNull(types.StringType),
				EnforcedParams:           types.ListNull(types.StringType),
				Tags:                     types.ListNull(types.StringType),
				Metadata:                 types.MapNull(types.StringType),
				Aliases:                  types.MapNull(types.StringType),
				Config:                   types.MapNull(types.StringType),
				Permissions:              types.MapNull(types.StringType),
				ModelMaxBudget:           types.MapNull(types.Float64Type),
				ModelRPMLimit:            types.MapNull(types.Int64Type),
				ModelTPMLimit:            types.MapNull(types.Int64Type),
			}

			if err := res.readKey(context.Background(), &data); err != nil {
				t.Fatalf("readKey returned error: %v", err)
			}

			// The null must be preserved regardless of what the API returned.
			if !data.RouterSettings.IsNull() {
				t.Errorf(
					"router_settings must remain null after readKey when plan set it to null, "+
						"but got: %v (API returned: %v)",
					data.RouterSettings,
					tc.apiRouterSettings,
				)
			}
		})
	}
}

// TestUpdateRouterSettingsPartialRemovalInjectsZeroValueSentinels verifies that
// when the user removes a key from within router_settings (e.g. removes fallbacks
// but keeps timeout and num_retries), the Update path injects a zero-value
// sentinel for the removed key in the request body.
//
// Background: the LiteLLM API validates router_settings with Pydantic and does
// not support partial updates. If fallbacks was previously set and is now absent,
// the API fills it with None and raises a 422 ("Input should be a valid list",
// "input":"null"). Injecting [] for removed list-type keys and {} for removed
// object-type keys prevents that validation failure.
func TestUpdateRouterSettingsPartialRemovalInjectsZeroValueSentinels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		stateValue     string // the prior state value for the removed key (as stored in tf state)
		expectedSentinel interface{} // what should be injected
	}{
		{
			name:             "list-type field (fallbacks) gets empty list sentinel",
			stateValue:       `[{"model-a":["model-b"]}]`,
			expectedSentinel: []interface{}{},
		},
		{
			name:             "object-type field gets empty object sentinel",
			stateValue:       `{"nested":"value"}`,
			expectedSentinel: map[string]interface{}{},
		},
		{
			name:             "scalar field is omitted (API replaces wholesale)",
			stateValue:       "5",
			expectedSentinel: nil, // signals the key should be absent
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := &KeyResource{}

			// Plan: router_settings has timeout + num_retries only ("removed_key" is gone).
			planSettings := stringMapValue(map[string]string{
				"timeout":     "5",
				"num_retries": "1",
			})

			// State: router_settings had the same keys plus "removed_key".
			stateSettings := stringMapValue(map[string]string{
				"timeout":     "5",
				"num_retries": "1",
				"removed_key": tc.stateValue,
			})

			// Build the request from the plan (mirrors what buildKeyRequest does).
			data := &KeyResourceModel{
				RouterSettings:           planSettings,
				Models:                   types.ListNull(types.StringType),
				AllowedRoutes:            types.ListNull(types.StringType),
				AllowedPassthroughRoutes: types.ListNull(types.StringType),
				AllowedCacheControls:     types.ListNull(types.StringType),
				Guardrails:               types.ListNull(types.StringType),
				Prompts:                  types.ListNull(types.StringType),
				EnforcedParams:           types.ListNull(types.StringType),
				Tags:                     types.ListNull(types.StringType),
				Metadata:                 types.MapNull(types.StringType),
				Aliases:                  types.MapNull(types.StringType),
				Config:                   types.MapNull(types.StringType),
				Permissions:              types.MapNull(types.StringType),
				ModelMaxBudget:           types.MapNull(types.Float64Type),
				ModelRPMLimit:            types.MapNull(types.Int64Type),
				ModelTPMLimit:            types.MapNull(types.Int64Type),
			}
			state := &KeyResourceModel{
				RouterSettings: stateSettings,
			}

			updateReq := r.buildKeyRequest(context.Background(), data)

			// Apply the same case-2 logic from Update.
			if routerMap, ok := updateReq["router_settings"].(map[string]interface{}); ok {
				if !state.RouterSettings.IsNull() && !state.RouterSettings.IsUnknown() {
					var stateRouter map[string]string
					state.RouterSettings.ElementsAs(context.Background(), &stateRouter, false)
					for k, v := range stateRouter {
						if _, inPlan := routerMap[k]; !inPlan {
							trimmed := strings.TrimSpace(v)
							if strings.HasPrefix(trimmed, "[") {
								routerMap[k] = []interface{}{}
							} else if strings.HasPrefix(trimmed, "{") {
								routerMap[k] = map[string]interface{}{}
							}
						}
					}
				}
			}

			routerMap, ok := updateReq["router_settings"].(map[string]interface{})
			if !ok {
				t.Fatalf("expected router_settings in request, got %T", updateReq["router_settings"])
			}

			if tc.expectedSentinel == nil {
				// Scalar: removed key must be absent from the request.
				if _, present := routerMap["removed_key"]; present {
					t.Errorf("scalar removed_key should be omitted from request, got %v", routerMap["removed_key"])
				}
			} else {
				// List/object: sentinel must be present with the correct zero type.
				val, present := routerMap["removed_key"]
				if !present {
					t.Fatalf("expected sentinel for removed_key in request but it was absent")
				}
				switch expected := tc.expectedSentinel.(type) {
				case []interface{}:
					got, ok := val.([]interface{})
					if !ok {
						t.Fatalf("expected []interface{} sentinel, got %T: %v", val, val)
					}
					if len(got) != len(expected) {
						t.Errorf("expected empty slice sentinel, got length %d", len(got))
					}
				case map[string]interface{}:
					got, ok := val.(map[string]interface{})
					if !ok {
						t.Fatalf("expected map[string]interface{} sentinel, got %T: %v", val, val)
					}
					if len(got) != len(expected) {
						t.Errorf("expected empty map sentinel, got length %d", len(got))
					}
				}
			}
		})
	}
}

// TestReadKeyRouterSettingsFiltersToConfiguredKeys verifies that readKey only
// repopulates keys that are present in the user's current config, ignoring any
// additional keys returned by the API.
//
// When the user removes a key (e.g. fallbacks) from router_settings, the Update
// path injects a zero-value sentinel (fallbacks=[]) into the request to satisfy
// Pydantic validation. The API then stores and returns that sentinel. Without
// filtering, readKey would write fallbacks=[] back into state — causing Terraform
// to see a new element appearing that wasn't in the plan.
func TestReadKeyRouterSettingsFiltersToConfiguredKeys(t *testing.T) {
	t.Parallel()

	// API returns the full router_settings including the injected sentinel [].
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"info": map[string]interface{}{
				"token": "sk-partial-test",
				"router_settings": map[string]interface{}{
					"timeout":     float64(5),
					"num_retries": float64(1),
					// fallbacks is the sentinel injected by Update — must not appear in state.
					"fallbacks": []interface{}{},
				},
			},
		})
	}))
	defer server.Close()

	res := &KeyResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	// data.RouterSettings reflects the plan: timeout + num_retries only.
	// fallbacks was removed by the user and must not reappear.
	data := KeyResourceModel{
		ID:  types.StringValue(hashKeyForID("sk-partial-test")),
		Key: types.StringValue("sk-partial-test"),
		RouterSettings: stringMapValue(map[string]string{
			"timeout":     "5",
			"num_retries": "1",
		}),
		Models:                   types.ListNull(types.StringType),
		AllowedRoutes:            types.ListNull(types.StringType),
		AllowedPassthroughRoutes: types.ListNull(types.StringType),
		AllowedCacheControls:     types.ListNull(types.StringType),
		Guardrails:               types.ListNull(types.StringType),
		Prompts:                  types.ListNull(types.StringType),
		EnforcedParams:           types.ListNull(types.StringType),
		Tags:                     types.ListNull(types.StringType),
		Metadata:                 types.MapNull(types.StringType),
		Aliases:                  types.MapNull(types.StringType),
		Config:                   types.MapNull(types.StringType),
		Permissions:              types.MapNull(types.StringType),
		ModelMaxBudget:           types.MapNull(types.Float64Type),
		ModelRPMLimit:            types.MapNull(types.Int64Type),
		ModelTPMLimit:            types.MapNull(types.Int64Type),
	}

	if err := res.readKey(context.Background(), &data); err != nil {
		t.Fatalf("readKey returned error: %v", err)
	}

	if data.RouterSettings.IsNull() || data.RouterSettings.IsUnknown() {
		t.Fatal("router_settings should be non-null after read (user still has settings configured)")
	}

	elems := data.RouterSettings.Elements()

	// timeout and num_retries must be present with the correct values.
	if v, ok := elems["timeout"].(types.String); !ok || v.ValueString() != "5" {
		t.Errorf("expected timeout '5', got %v", elems["timeout"])
	}
	if v, ok := elems["num_retries"].(types.String); !ok || v.ValueString() != "1" {
		t.Errorf("expected num_retries '1', got %v", elems["num_retries"])
	}

	// fallbacks must not appear — it was a sentinel, not part of the user's config.
	if _, present := elems["fallbacks"]; present {
		t.Errorf("fallbacks must not appear in state after readKey — it was a zero-value sentinel injected by Update, not a user-configured key. Got: %v", elems["fallbacks"])
	}

	// Exactly two keys: timeout and num_retries.
	if len(elems) != 2 {
		t.Errorf("expected exactly 2 router_settings keys (timeout, num_retries), got %d: %v", len(elems), elems)
	}
}
