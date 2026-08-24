package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestKeyRouterSettingsOwnershipSchema(t *testing.T) {
	t.Parallel()

	var resourceResponse resource.SchemaResponse
	(&KeyResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resourceResponse)
	if resourceResponse.Diagnostics.HasError() {
		t.Fatalf("resource schema diagnostics: %v", resourceResponse.Diagnostics)
	}
	resourceAttribute := resourceResponse.Schema.Attributes["router_settings"]
	if !resourceAttribute.IsOptional() || resourceAttribute.IsComputed() {
		t.Fatalf("resource router_settings must be Optional-only for selective ownership: %#v", resourceAttribute)
	}

	var dataSourceResponse datasource.SchemaResponse
	(&KeyDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &dataSourceResponse)
	if dataSourceResponse.Diagnostics.HasError() {
		t.Fatalf("data source schema diagnostics: %v", dataSourceResponse.Diagnostics)
	}
	dataSourceAttribute := dataSourceResponse.Schema.Attributes["router_settings"]
	if !dataSourceAttribute.IsComputed() || dataSourceAttribute.IsOptional() {
		t.Fatalf("data source router_settings must expose the complete computed document: %#v", dataSourceAttribute)
	}
	if got := len((&KeyDataSource{}).ConfigValidators(context.Background())); got != 1 {
		t.Fatalf("key data source config validators = %d, want exactly-one lookup validator", got)
	}
	if key := dataSourceResponse.Schema.Attributes["key"]; !key.IsOptional() || !key.IsSensitive() {
		t.Fatalf("key lookup must be optional and sensitive: %#v", key)
	}
	if keyHash := dataSourceResponse.Schema.Attributes["key_hash"]; !keyHash.IsOptional() || keyHash.IsSensitive() {
		t.Fatalf("key_hash lookup must be optional and non-sensitive: %#v", keyHash)
	}
}

func TestKeyDataSourceLookupSupportsRawAndWriteOnlyHash(t *testing.T) {
	t.Parallel()

	raw := "sk-data-source-lookup-test"
	managementID := hashKeyForID(raw)
	bareHash, err := keyHashFromID(managementID)
	if err != nil {
		t.Fatal(err)
	}
	tests := map[string]struct {
		data       KeyDataSourceModel
		wantLookup string
		wantID     string
		wantError  bool
	}{
		"raw key": {
			data:       KeyDataSourceModel{Key: types.StringValue(raw), KeyHash: types.StringNull()},
			wantLookup: raw,
			wantID:     managementID,
		},
		"write-only hash": {
			data:       KeyDataSourceModel{Key: types.StringNull(), KeyHash: types.StringValue(managementID)},
			wantLookup: bareHash,
			wantID:     managementID,
		},
		"uppercase write-only hash normalizes": {
			data:       KeyDataSourceModel{Key: types.StringNull(), KeyHash: types.StringValue("sha256:" + strings.ToUpper(bareHash))},
			wantLookup: bareHash,
			wantID:     managementID,
		},
		"invalid hash": {
			data:      KeyDataSourceModel{Key: types.StringNull(), KeyHash: types.StringValue("sha256:not-a-hash")},
			wantError: true,
		},
		"missing lookup": {
			data:      KeyDataSourceModel{Key: types.StringNull(), KeyHash: types.StringNull()},
			wantError: true,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			lookup, id, err := keyDataSourceLookup(&test.data)
			if test.wantError {
				if err == nil {
					t.Fatalf("lookup = %q, id = %q, want error", lookup, id)
				}
				return
			}
			if err != nil || lookup != test.wantLookup || id != test.wantID {
				t.Fatalf("keyDataSourceLookup = (%q, %q, %v), want (%q, %q, nil)", lookup, id, err, test.wantLookup, test.wantID)
			}
		})
	}
}

func TestKeyDataSourceReadErrorOmitsLookupToken(t *testing.T) {
	t.Parallel()

	secret := "sk-sensitive-data-source-lookup-token"
	for name, err := range map[string]error{
		"transport": fmt.Errorf("Get https://proxy.example/key/info?key=%s: connection reset", secret),
		"api":       &APIError{StatusCode: http.StatusBadRequest, Body: `{"echo":"` + secret + `"}`},
	} {
		t.Run(name, func(t *testing.T) {
			message := keyDataSourceReadError(err)
			if strings.Contains(message, secret) {
				t.Fatalf("safe data-source diagnostic exposed lookup token: %q", message)
			}
		})
	}
}

func TestRouterSettingsReadStatusClassification(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusNotFound, http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway} {
		if !isTransientRouterSettingsReadStatus(status) {
			t.Errorf("HTTP %d must be retried", status)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusUnprocessableEntity} {
		if isTransientRouterSettingsReadStatus(status) {
			t.Errorf("HTTP %d must fail fast", status)
		}
	}
}

func keyRouterSettingsTestValue(t *testing.T, overrides map[string]attr.Value) types.Object {
	t.Helper()
	values := map[string]attr.Value{}
	for _, name := range []string{"routing_strategy_args", "routing_strategy", "routing_groups", "model_group_retry_policy", "model_group_affinity_config", "fallbacks", "context_window_fallbacks", "model_group_alias", "tag_routing_prefix"} {
		values[name] = types.StringNull()
	}
	values["retry_policy"] = types.ObjectNull(keyRetryPolicyAttrTypes)
	for _, name := range []string{"allowed_fails", "num_retries", "max_retries"} {
		values[name] = types.Int64Null()
	}
	for _, name := range []string{"cooldown_time", "timeout", "retry_after"} {
		values[name] = types.Float64Null()
	}
	values["enable_tag_filtering"] = types.BoolNull()
	for name, value := range overrides {
		values[name] = value
	}
	object, diagnostics := types.ObjectValue(keyRouterSettingsAttrTypes, values)
	if diagnostics.HasError() {
		t.Fatalf("build router settings test value: %v", diagnostics)
	}
	return object
}

func retryPolicyTestValue(t *testing.T) types.Object {
	t.Helper()
	values := map[string]attr.Value{
		"bad_request_error_retries":              types.Int64Value(1),
		"authentication_error_retries":           types.Int64Value(2),
		"timeout_error_retries":                  types.Int64Value(3),
		"rate_limit_error_retries":               types.Int64Value(4),
		"content_policy_violation_error_retries": types.Int64Value(5),
		"internal_server_error_retries":          types.Int64Value(6),
	}
	object, diagnostics := types.ObjectValue(keyRetryPolicyAttrTypes, values)
	if diagnostics.HasError() {
		t.Fatalf("build retry policy: %v", diagnostics)
	}
	return object
}

func TestKeyRouterSettingsPayloadMatchesLiteLLMV198Schema(t *testing.T) {
	t.Parallel()

	settings := keyRouterSettingsTestValue(t, map[string]attr.Value{
		"routing_strategy_args":       types.StringValue(`{"ttl":30,"nested":{"weight":0.5}}`),
		"routing_strategy":            types.StringValue("usage-based-routing-v2"),
		"routing_groups":              types.StringValue(`[{"group_name":"primary","models":["gpt-4o"],"routing_strategy":"simple-shuffle","routing_strategy_args":{"weight":2}}]`),
		"retry_policy":                retryPolicyTestValue(t),
		"model_group_retry_policy":    types.StringValue(`{"gpt-4o":{"RateLimitErrorRetries":7}}`),
		"model_group_affinity_config": types.StringValue(`{"us":["gpt-4o","gpt-4o-mini"]}`),
		"allowed_fails":               types.Int64Value(8),
		"cooldown_time":               types.Float64Value(9.5),
		"num_retries":                 types.Int64Value(10),
		"timeout":                     types.Float64Value(11.5),
		"max_retries":                 types.Int64Value(12),
		"retry_after":                 types.Float64Value(0.25),
		"fallbacks":                   types.StringValue(`[{"gpt-4o":["gpt-4o-mini"]},{"*":["fallback"]}]`),
		"context_window_fallbacks":    types.StringValue(`[{"gpt-4o":["large-context"]}]`),
		"model_group_alias":           types.StringValue(`{"fast":"gpt-4o-mini","hidden":{"model":"gpt-4o","hidden":true}}`),
		"enable_tag_filtering":        types.BoolValue(true),
		"tag_routing_prefix":          types.StringValue("tenant:"),
	})

	payload, err := keyRouterSettingsPayload(settings)
	if err != nil {
		t.Fatalf("keyRouterSettingsPayload: %v", err)
	}
	if len(payload) != 17 {
		t.Fatalf("payload has %d fields, want 17: %#v", len(payload), payload)
	}
	if payload["retry_after"] != 0.25 {
		t.Fatalf("retry_after = %#v, want decimal 0.25", payload["retry_after"])
	}
	policy, ok := payload["retry_policy"].(map[string]interface{})
	if !ok || policy["RateLimitErrorRetries"] != int64(4) || len(policy) != 6 {
		t.Fatalf("retry_policy does not use exact LiteLLM wire names: %#v", payload["retry_policy"])
	}
	fallbacks := payload["fallbacks"].([]interface{})
	if len(fallbacks) != 2 {
		t.Fatalf("fallback order was not preserved: %#v", fallbacks)
	}
	alias := payload["model_group_alias"].(map[string]interface{})
	if _, ok := alias["hidden"].(map[string]interface{}); !ok {
		t.Fatalf("object-valued model-group alias was lost: %#v", alias)
	}
}

func TestKeyRouterSettingsUpdateReplacesAndClearsCompleteDocument(t *testing.T) {
	t.Parallel()

	prior := keyRouterSettingsTestValue(t, map[string]attr.Value{
		"routing_strategy": types.StringValue("simple-shuffle"),
		"num_retries":      types.Int64Value(5),
		"fallbacks":        types.StringValue(`[{"gpt-4o":["gpt-4o-mini"]}]`),
	})
	planned := keyRouterSettingsTestValue(t, map[string]attr.Value{
		"num_retries": types.Int64Value(2),
	})
	data := &KeyResourceModel{RouterSettings: planned}
	request := mustBuildKeyRequest(t, &KeyResource{}, data)
	applyKeyRouterSettingsUpdateSemantics(request, planned, prior)
	settings, ok := request["router_settings"].(map[string]interface{})
	if !ok || len(settings) != 1 || settings["num_retries"] != int64(2) {
		t.Fatalf("partial plan must replace, not merge, the complete document: %#v", request["router_settings"])
	}
	if _, retained := settings["routing_strategy"]; retained {
		t.Fatalf("removed prior field was retained: %#v", settings)
	}

	clearRequest := map[string]interface{}{"key": "hash"}
	applyKeyRouterSettingsUpdateSemantics(clearRequest, types.ObjectNull(keyRouterSettingsAttrTypes), prior)
	cleared, ok := clearRequest["router_settings"].(map[string]interface{})
	if !ok || len(cleared) != 0 {
		t.Fatalf("block removal must send an explicit empty object, got %#v", clearRequest["router_settings"])
	}

	unmanagedRequest := map[string]interface{}{"key": "hash"}
	applyKeyRouterSettingsUpdateSemantics(unmanagedRequest, types.ObjectNull(keyRouterSettingsAttrTypes), types.ObjectNull(keyRouterSettingsAttrTypes))
	if _, sent := unmanagedRequest["router_settings"]; sent {
		t.Fatalf("unmanaged settings must be omitted: %#v", unmanagedRequest)
	}
}

func TestKeyRouterSettingsFromAPIPreservesEquivalentConfiguredJSON(t *testing.T) {
	t.Parallel()

	configuredFallbacks := "[ { \"gpt-4o\" : [\"gpt-4o-mini\"] } ]"
	current := keyRouterSettingsTestValue(t, map[string]attr.Value{
		"fallbacks":    types.StringValue(configuredFallbacks),
		"retry_after":  types.Float64Value(0.25),
		"retry_policy": retryPolicyTestValue(t),
	})
	api := map[string]interface{}{
		"fallbacks":   []interface{}{map[string]interface{}{"gpt-4o": []interface{}{"gpt-4o-mini"}}},
		"retry_after": 0.25,
		"retry_policy": map[string]interface{}{
			"BadRequestErrorRetries":             float64(1),
			"AuthenticationErrorRetries":         float64(2),
			"TimeoutErrorRetries":                float64(3),
			"RateLimitErrorRetries":              float64(4),
			"ContentPolicyViolationErrorRetries": float64(5),
			"InternalServerErrorRetries":         float64(6),
		},
	}

	got, present, err := keyRouterSettingsFromAPI(api, current)
	if err != nil || !present {
		t.Fatalf("keyRouterSettingsFromAPI = present %t, error %v", present, err)
	}
	if got.Attributes()["fallbacks"].(types.String).ValueString() != configuredFallbacks {
		t.Fatalf("equivalent configured JSON formatting was not preserved: %q", got.Attributes()["fallbacks"])
	}
	if !got.Attributes()["retry_policy"].Equal(current.Attributes()["retry_policy"]) {
		t.Fatalf("retry policy changed: %#v", got.Attributes()["retry_policy"])
	}
}

func TestKeyRouterSettingsCompleteDocumentComparison(t *testing.T) {
	t.Parallel()

	wanted := map[string]interface{}{
		"num_retries": int64(3),
		"fallbacks": []interface{}{
			map[string]interface{}{"gpt-4o": []interface{}{"gpt-4o-mini"}},
		},
	}
	observed := map[string]interface{}{
		"num_retries": float64(3),
		"fallbacks": []interface{}{
			map[string]interface{}{"gpt-4o": []interface{}{"gpt-4o-mini"}},
		},
	}
	matches, err := keyRouterSettingsMatchAPI(wanted, observed)
	if err != nil || !matches {
		t.Fatalf("equivalent complete documents = %t, %v", matches, err)
	}
	observed["routing_strategy"] = "simple-shuffle"
	matches, err = keyRouterSettingsMatchAPI(wanted, observed)
	if err != nil || matches {
		t.Fatalf("API-added field must be drift: matches %t, error %v", matches, err)
	}
	for name, raw := range map[string]interface{}{"missing": nil, "empty object": map[string]interface{}{}, "encoded empty object": `{}`} {
		t.Run(name, func(t *testing.T) {
			matches, err := keyRouterSettingsMatchAPI(nil, raw)
			if err != nil || !matches {
				t.Fatalf("cleared document = %t, %v", matches, err)
			}
		})
	}
}

func TestKeyRouterSettingsRejectsUnsupportedReadbackFields(t *testing.T) {
	t.Parallel()

	if _, _, err := keyRouterSettingsFromAPI(map[string]interface{}{"stream_timeout": 30.0}, types.ObjectNull(keyRouterSettingsAttrTypes)); err == nil {
		t.Fatal("expected stale unsupported top-level field to fail instead of being silently discarded")
	}
	if _, _, err := keyRouterSettingsFromAPI(map[string]interface{}{
		"retry_policy": map[string]interface{}{"UnknownErrorRetries": float64(1)},
	}, types.ObjectNull(keyRouterSettingsAttrTypes)); err == nil {
		t.Fatal("expected unsupported retry-policy field to fail instead of being silently discarded")
	}
}

func TestKeyRouterSettingsStringAndNullReadback(t *testing.T) {
	t.Parallel()

	got, present, err := keyRouterSettingsFromAPI(`{"fallbacks":[],"model_group_alias":{"fast":"gpt-4o"}}`, types.ObjectNull(keyRouterSettingsAttrTypes))
	if err != nil || !present {
		t.Fatalf("string readback = present %t, error %v", present, err)
	}
	if got.Attributes()["fallbacks"].(types.String).ValueString() != "[]" {
		t.Fatalf("explicit empty fallback list not retained: %#v", got)
	}

	got, present, err = keyRouterSettingsFromAPI(nil, got)
	if err != nil || present || !got.IsNull() {
		t.Fatalf("null readback = %#v, present %t, error %v", got, present, err)
	}
}

func TestWaitForKeyRouterSettingsUsesWriteOnlyHashAndStableReads(t *testing.T) {
	t.Parallel()

	rawKey := "sk-write-only-router-settings-test"
	hashID := hashKeyForID(rawKey)
	hash, err := keyHashFromID(hashID)
	if err != nil {
		t.Fatal(err)
	}
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.URL.Query().Get("key"); got != hash {
			http.Error(writer, "unexpected key identifier", http.StatusBadRequest)
			return
		}
		reads.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"info": map[string]interface{}{
				"router_settings": map[string]interface{}{
					"routing_strategy": "simple-shuffle",
				},
			},
		})
	}))
	defer server.Close()

	keyResource := &KeyResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	data := KeyResourceModel{
		ID:           types.StringValue(hashID),
		Key:          types.StringNull(),
		KeyWOVersion: types.StringValue("1"),
		RouterSettings: keyRouterSettingsTestValue(t, map[string]attr.Value{
			"routing_strategy": types.StringValue("simple-shuffle"),
		}),
	}
	if err := keyResource.waitForKeyRouterSettings(context.Background(), &data); err != nil {
		t.Fatalf("waitForKeyRouterSettings: %v", err)
	}
	if got := reads.Load(); got != 2 {
		t.Fatalf("stable read count = %d, want 2", got)
	}
}

func TestReadKeyRouterSettingsOwnership(t *testing.T) {
	t.Parallel()

	apiSettings := map[string]interface{}{
		"fallbacks":         []interface{}{map[string]interface{}{"gpt-4o": []interface{}{"gpt-4o-mini"}}},
		"routing_strategy":  "simple-shuffle",
		"model_group_alias": map[string]interface{}{"fast": "gpt-4o"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"key": "sk-router-settings-test",
			"info": map[string]interface{}{
				"token":           "hash",
				"router_settings": apiSettings,
			},
		})
	}))
	defer server.Close()

	resource := &KeyResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	unmanaged := KeyResourceModel{Key: types.StringValue("sk-router-settings-test"), RouterSettings: types.ObjectNull(keyRouterSettingsAttrTypes)}
	if err := resource.readKey(context.Background(), &unmanaged); err != nil {
		t.Fatalf("unmanaged read: %v", err)
	}
	if !unmanaged.RouterSettings.IsNull() {
		t.Fatalf("absent block adopted API settings: %#v", unmanaged.RouterSettings)
	}

	managed := KeyResourceModel{
		Key: types.StringValue("sk-router-settings-test"),
		RouterSettings: keyRouterSettingsTestValue(t, map[string]attr.Value{
			"fallbacks":         types.StringValue(`[{"gpt-4o":["gpt-4o-mini"]}]`),
			"routing_strategy":  types.StringValue("simple-shuffle"),
			"model_group_alias": types.StringValue(`{"fast":"gpt-4o"}`),
		}),
	}
	if err := resource.readKey(context.Background(), &managed); err != nil {
		t.Fatalf("managed read: %v", err)
	}
	want, _, _ := keyRouterSettingsFromAPI(apiSettings, managed.RouterSettings)
	if !reflect.DeepEqual(managed.RouterSettings, want) {
		t.Fatalf("managed settings did not refresh\n got: %#v\nwant: %#v", managed.RouterSettings, want)
	}
}
