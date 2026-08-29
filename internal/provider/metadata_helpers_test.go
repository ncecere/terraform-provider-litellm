package provider

import (
	"encoding/json"
	"testing"
)

func TestConvertMetadataToNative_StringValues(t *testing.T) {
	t.Parallel()

	input := map[string]string{
		"env":     "production",
		"team":    "platform",
		"version": "1.0",
	}

	result := convertMetadataToNative(input)

	if result["env"] != "production" {
		t.Errorf("expected 'production', got %v", result["env"])
	}
	if result["team"] != "platform" {
		t.Errorf("expected 'platform', got %v", result["team"])
	}
}

func TestConvertMetadataToNative_JSONArray(t *testing.T) {
	t.Parallel()

	input := map[string]string{
		"logging": `[{"callback_name":"langsmith","callback_type":"success","callback_vars":{"langsmith_project":"my-project"}}]`,
	}

	result := convertMetadataToNative(input)

	// Should be a native array, not a string
	arr, ok := result["logging"].([]interface{})
	if !ok {
		t.Fatalf("expected logging to be []interface{}, got %T", result["logging"])
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 element, got %d", len(arr))
	}
	item, ok := arr[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected first element to be map, got %T", arr[0])
	}
	if item["callback_name"] != "langsmith" {
		t.Errorf("expected callback_name 'langsmith', got %v", item["callback_name"])
	}
}

func TestConvertMetadataToNative_JSONObject(t *testing.T) {
	t.Parallel()

	input := map[string]string{
		"config": `{"key":"value","nested":{"a":1}}`,
	}

	result := convertMetadataToNative(input)

	obj, ok := result["config"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected config to be map, got %T", result["config"])
	}
	if obj["key"] != "value" {
		t.Errorf("expected key 'value', got %v", obj["key"])
	}
}

func TestConvertMetadataToNative_PreservesExactNestedNumbers(t *testing.T) {
	t.Parallel()

	result := convertMetadataToNative(map[string]string{
		"numeric": `{"large":9007199254740993,"close":1.0000000000000001}`,
	})
	object, ok := result["numeric"].(map[string]interface{})
	if !ok {
		t.Fatalf("numeric metadata = %T", result["numeric"])
	}
	large, largeOK := object["large"].(json.Number)
	closeValue, closeOK := object["close"].(json.Number)
	if !largeOK || large.String() != "9007199254740993" || !closeOK || closeValue.String() != "1.0000000000000001" {
		t.Fatalf("numeric metadata rounded: %#v", object)
	}
	if got := metadataValueToString(object); got != `{"close":1.0000000000000001,"large":9007199254740993}` {
		t.Fatalf("numeric metadata read-back = %s", got)
	}
}

func TestConvertMetadataToNative_MixedValues(t *testing.T) {
	t.Parallel()

	input := map[string]string{
		"simple":  "hello",
		"array":   `["a","b"]`,
		"object":  `{"x":1}`,
		"invalid": `{not valid json`,
	}

	result := convertMetadataToNative(input)

	// Simple string preserved
	if result["simple"] != "hello" {
		t.Errorf("expected 'hello', got %v", result["simple"])
	}
	// Array parsed
	if _, ok := result["array"].([]interface{}); !ok {
		t.Errorf("expected array to be []interface{}, got %T", result["array"])
	}
	// Object parsed
	if _, ok := result["object"].(map[string]interface{}); !ok {
		t.Errorf("expected object to be map, got %T", result["object"])
	}
	// Invalid JSON stays as string
	if result["invalid"] != `{not valid json` {
		t.Errorf("expected invalid JSON to be preserved as string, got %v", result["invalid"])
	}
}

func TestMetadataValueToString_String(t *testing.T) {
	t.Parallel()

	if got := metadataValueToString("hello"); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestMetadataValueToString_Nil(t *testing.T) {
	t.Parallel()

	if got := metadataValueToString(nil); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestMetadataValueToString_Array(t *testing.T) {
	t.Parallel()

	input := []interface{}{
		map[string]interface{}{
			"callback_name": "langsmith",
			"callback_type": "success",
		},
	}

	got := metadataValueToString(input)

	// Should be valid JSON
	var parsed interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got error: %v, value: %q", err, got)
	}

	// Should round-trip: parse back and verify
	arr, ok := parsed.([]interface{})
	if !ok {
		t.Fatalf("expected array, got %T", parsed)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 element, got %d", len(arr))
	}
}

func TestMetadataValueToString_Object(t *testing.T) {
	t.Parallel()

	input := map[string]interface{}{
		"key": "value",
		"num": float64(42),
	}

	got := metadataValueToString(input)

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("expected valid JSON, got error: %v, value: %q", err, got)
	}
	if parsed["key"] != "value" {
		t.Errorf("expected key 'value', got %v", parsed["key"])
	}
}

func TestMetadataValueToString_Number(t *testing.T) {
	t.Parallel()

	if got := metadataValueToString(float64(42)); got != "42" {
		t.Errorf("expected '42', got %q", got)
	}
}

func TestMetadataValueToString_Bool(t *testing.T) {
	t.Parallel()

	if got := metadataValueToString(true); got != "true" {
		t.Errorf("expected 'true', got %q", got)
	}
}

// TestMetadataRoundTrip verifies the full cycle:
// Terraform map(string) → convertMetadataToNative → API → metadataValueToString → map(string)
func TestMetadataValueToStringPreservingMasked(t *testing.T) {
	t.Parallel()

	configured := `[{"callback_name":"langfuse_otel","callback_vars":{"host":"https://configured.example","public_key":"pk-original","secret_key":"sk-original"}}]`
	apiValue := []interface{}{
		map[string]interface{}{
			"callback_name": "langfuse_otel",
			"callback_vars": map[string]interface{}{
				"host":       "https://changed.example",
				"public_key": "litellm_enc::public",
				"secret_key": "litellm_enc::secret",
			},
		},
	}

	got := metadataValueToStringPreservingMasked(apiValue, configured)
	want := `[{"callback_name":"langfuse_otel","callback_vars":{"host":"https://changed.example","public_key":"pk-original","secret_key":"sk-original"}}]`
	if got != want {
		t.Fatalf("preserved metadata = %s, want %s", got, want)
	}
}

func TestMetadataValueToStringPreservingMaskedScalar(t *testing.T) {
	t.Parallel()

	if got := metadataValueToStringPreservingMasked("litellm_enc::opaque", "configured-secret"); got != "configured-secret" {
		t.Fatalf("masked scalar = %q, want configured value", got)
	}
	if got := metadataValueToStringPreservingMasked("***REDACTED***", "configured-secret"); got != "configured-secret" {
		t.Fatalf("redacted scalar = %q, want configured value", got)
	}
	for _, apiValue := range []string{"changed", "not-redacted", "status****"} {
		if got := metadataValueToStringPreservingMasked(apiValue, "configured"); got != apiValue {
			t.Fatalf("unmasked scalar = %q, want API value %q", got, apiValue)
		}
	}
}

func TestMetadataValueToStringPreservingMaskedKeepsExactSemanticFormatting(t *testing.T) {
	t.Parallel()

	configured := `{ "large": 9007199254740993, "fraction": 9.007199254740993e15 }`
	apiValue := map[string]interface{}{
		"fraction": json.Number("9007199254740993.0"),
		"large":    json.Number("9007199254740993"),
	}
	if got := metadataValueToStringPreservingMasked(apiValue, configured); got != configured {
		t.Fatalf("semantically equal exact metadata formatting changed: %s", got)
	}
	apiValue["large"] = json.Number("9007199254740992")
	if got := metadataValueToStringPreservingMasked(apiValue, configured); got == configured {
		t.Fatal("distinct metadata integer above 2^53 was hidden as semantically equal")
	}
}

func TestMetadataValueToStringPreservingMaskedDoesNotHideStructuralDrift(t *testing.T) {
	t.Parallel()

	for _, configured := range []string{`{"secret":"configured"}`, `["configured"]`} {
		if got := metadataValueToStringPreservingMasked("litellm_enc::opaque", configured); got != "litellm_enc::opaque" {
			t.Fatalf("masked scalar replacing %s = %q, want structural drift", configured, got)
		}
	}
}

func TestMetadataRoundTrip(t *testing.T) {
	t.Parallel()

	original := map[string]string{
		"env":     "prod",
		"logging": `[{"callback_name":"langsmith","callback_type":"success","callback_vars":{"langsmith_project":"my-project"}}]`,
		"config":  `{"retries":3}`,
	}

	// Simulate write: convert for API
	native := convertMetadataToNative(original)

	// Simulate read: convert back to strings
	roundTripped := make(map[string]string, len(native))
	for k, v := range native {
		roundTripped[k] = metadataValueToString(v)
	}

	// Simple string should be identical
	if roundTripped["env"] != "prod" {
		t.Errorf("env: expected 'prod', got %q", roundTripped["env"])
	}

	// JSON values should parse back to equivalent structures
	var origLogging, rtLogging interface{}
	json.Unmarshal([]byte(original["logging"]), &origLogging)
	json.Unmarshal([]byte(roundTripped["logging"]), &rtLogging)

	origJSON, _ := json.Marshal(origLogging)
	rtJSON, _ := json.Marshal(rtLogging)
	if string(origJSON) != string(rtJSON) {
		t.Errorf("logging round-trip mismatch:\n  original:     %s\n  round-tripped: %s", origJSON, rtJSON)
	}
}
