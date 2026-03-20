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

// TestServiceAccountIDDefaultsKeyAlias verifies that when service_account_id is
// set but key_alias is omitted, buildKeyRequest populates key_alias with the
// service_account_id value — matching the documented behaviour.
// TestMinimalKeyNoKeyAliasNoServiceAccountID verifies the plain minimal case:
// neither key_alias nor service_account_id is configured.
//
//  resource "litellm_key" "minimal" {}
//
// Expected behaviour:
//  - buildKeyRequest must NOT include "key_alias" in the payload.
//  - readKey with an Unknown key_alias (Computed, unresolved) and an API
//    response that contains no key_alias must resolve the field to null —
//    i.e. no "inconsistent result after apply" error and no perpetual
//    "(known after apply)" on subsequent plans.
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
