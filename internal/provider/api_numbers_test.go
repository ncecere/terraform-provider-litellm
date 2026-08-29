package provider

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestExactInt64FromAPI(t *testing.T) {
	t.Parallel()

	valid := []struct {
		name  string
		value interface{}
		want  int64
	}{
		{"two to fifty-three minus one", json.Number("9007199254740991"), 9007199254740991},
		{"two to fifty-three plus one", json.Number("9007199254740993"), 9007199254740993},
		{"maximum", json.Number("9223372036854775807"), math.MaxInt64},
		{"minimum", json.Number("-9223372036854775808"), math.MinInt64},
		{"scientific maximum", json.Number("9.223372036854775807e18"), math.MaxInt64},
		{"scientific minimum", json.Number("-9.223372036854775808e18"), math.MinInt64},
		{"negative exponent maximum", json.Number("92233720368547758070e-1"), math.MaxInt64},
		{"integral decimal", json.Number("120.00"), 120},
		{"positive exponent", json.Number("1.2e3"), 1200},
		{"negative exponent integral", json.Number("1200e-2"), 12},
		{"large exponent string", "9.007199254740993e15", 9007199254740993},
		{"native int64", int64(math.MinInt64), math.MinInt64},
		{"exact float", float64(1024), 1024},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := exactInt64FromAPI(test.value)
			if err != nil || got != test.want {
				t.Fatalf("exactInt64FromAPI(%T) = %d, %v; want %d", test.value, got, err, test.want)
			}
		})
	}

	invalid := []struct {
		name  string
		value interface{}
	}{
		{"null", nil},
		{"fraction", json.Number("1.5")},
		{"fractional exponent", json.Number("1e-1")},
		{"positive overflow", json.Number("9223372036854775808")},
		{"negative overflow", json.Number("-9223372036854775809")},
		{"overflow exponent", json.Number("1e100")},
		{"malformed", "12x"},
		{"whitespace", " 12"},
		{"leading plus", "+12"},
		{"nan string", "NaN"},
		{"positive infinity string", "Infinity"},
		{"negative infinity string", "-Infinity"},
		{"nan float", math.NaN()},
		{"positive infinity float", math.Inf(1)},
		{"negative infinity float", math.Inf(-1)},
		{"ambiguous float above safe integer", float64(9007199254740993)},
		{"float above maximum", float64(math.MaxInt64)},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := exactInt64FromAPI(test.value)
			if err == nil {
				t.Fatalf("exactInt64FromAPI(%T) unexpectedly succeeded", test.value)
			}
			if text, ok := test.value.(string); ok && strings.Contains(err.Error(), text) {
				t.Fatalf("conversion error exposed response value %q: %v", text, err)
			}
		})
	}
}

func TestDecodeJSONUseNumberPreservesNestedIntegers(t *testing.T) {
	t.Parallel()

	var decoded map[string]interface{}
	input := []byte(`{"limit":9007199254740993,"nested":{"minimum":-9223372036854775808},"list":[9223372036854775807,1.25]}`)
	if err := decodeJSONUseNumber(input, &decoded); err != nil {
		t.Fatal(err)
	}
	if got, ok := decoded["limit"].(json.Number); !ok || got.String() != "9007199254740993" {
		t.Fatalf("top-level number = %#v", decoded["limit"])
	}
	nested := decoded["nested"].(map[string]interface{})
	if got, ok := nested["minimum"].(json.Number); !ok || got.String() != "-9223372036854775808" {
		t.Fatalf("nested number = %#v", nested["minimum"])
	}
	list := decoded["list"].([]interface{})
	if got, ok := list[0].(json.Number); !ok || got.String() != "9223372036854775807" {
		t.Fatalf("list integer = %#v", list[0])
	}
	if got, err := float64FromAPI(list[1]); err != nil || got != 1.25 {
		t.Fatalf("list float = %#v", list[1])
	}
}

func TestCanonicalJSONNumbersRemainExactAboveFloatPrecision(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"9007199254740993.0":      "9007199254740993",
		"9.007199254740993e15":    "9007199254740993",
		"0.000000175":             "0.000000175",
		"1.75e-7":                 "0.000000175",
		"-0.000":                  "0",
		"1e1000001":               "1e1000001",
		"9.223372036854775807e18": "9223372036854775807",
	}
	for input, want := range cases {
		got, ok := canonicalJSONNumberString(input)
		if !ok || got != want {
			t.Errorf("canonicalJSONNumberString(%q) = %q, %v; want %q", input, got, ok, want)
		}
	}
	for _, invalid := range []string{"NaN", "+1", "01", " 1"} {
		if _, ok := canonicalJSONNumberString(invalid); ok {
			t.Errorf("canonicalJSONNumberString(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestExactJSONSemanticEqualityDoesNotRoundCloseIntegers(t *testing.T) {
	t.Parallel()

	if !jsonSemanticallyEqual(`{"value":9007199254740993}`, `{ "value": 9.007199254740993e15 }`) {
		t.Fatal("equivalent exact integer spellings did not compare equal")
	}
	if jsonSemanticallyEqual(`{"value":9007199254740992}`, `{"value":9007199254740993}`) {
		t.Fatal("distinct integers above 2^53 compared equal")
	}
	if jsonSemanticallyEqual(`{"value":1.0000000000000001}`, `{"value":1.0000000000000002}`) {
		t.Fatal("distinct close decimals compared equal")
	}
}

func TestDecodeJSONUseNumberRejectsTrailingValues(t *testing.T) {
	t.Parallel()

	var decoded interface{}
	if err := decodeJSONUseNumber([]byte(`{"ok":true} {"also":true}`), &decoded); err == nil {
		t.Fatal("expected multiple JSON values to fail")
	}
}

func TestDecodeJSONUseNumberRejectsDuplicateMembersBeforeDecode(t *testing.T) {
	t.Parallel()

	for _, input := range []string{
		`{"same":1,"same":2}`,
		`{"outer":{"same":1,"same":2}}`,
		`[{"same":1,"same":2}]`,
		`{"key":1,"k\u0065y":2}`,
		`{"\/":1,"/":2}`,
	} {
		input := input
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			result := map[string]interface{}{"retained": true}
			err := decodeJSONUseNumber([]byte(input), &result)
			if !errors.Is(err, errJSONDuplicateMember) {
				t.Fatalf("duplicate member error = %v", err)
			}
			if len(result) != 1 || result["retained"] != true {
				t.Fatalf("destination was changed before duplicate rejection: %#v", result)
			}
		})
	}
}

func TestClientResponseDecodeRejectsDuplicateMembers(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"outer":{"response-secret":1,"response-secret":2}}`))
	}))
	t.Cleanup(server.Close)
	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
	result := map[string]interface{}{"retained": true}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/mcp/info", nil, &result)
	if err == nil || !strings.Contains(err.Error(), "failed to decode") || strings.Contains(err.Error(), "response-secret") {
		t.Fatalf("client duplicate response error = %v", err)
	}
	if len(result) != 1 || result["retained"] != true {
		t.Fatalf("client changed destination before rejecting duplicate: %#v", result)
	}
}

func TestDecodeJSONUseNumberErrorsNeverContainInput(t *testing.T) {
	t.Parallel()

	secrets := []string{"duplicate-secret", "value-secret", "trailing-secret"}
	inputs := []string{
		`{"duplicate-secret":1,"duplicate-secret":2}`,
		`{"field":"value-secret"`,
		`{"ok":true} "trailing-secret"`,
	}
	for index, input := range inputs {
		var decoded interface{}
		err := decodeJSONUseNumber([]byte(input), &decoded)
		if err == nil {
			t.Fatalf("input %d unexpectedly decoded", index)
		}
		for _, secret := range secrets {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error exposed JSON content: %v", err)
			}
		}
	}
}

func TestDecodeJSONUseNumberEnforcesStructuralLimits(t *testing.T) {
	t.Parallel()

	tooLarge := make([]byte, jsonDecodeMaxInputBytes+1)
	var decoded interface{}
	if err := decodeJSONUseNumber(tooLarge, &decoded); !errors.Is(err, errJSONInputLimit) {
		t.Fatalf("input limit error = %v", err)
	}

	deep := strings.Repeat("[", jsonDecodeMaxDepth) + "0" + strings.Repeat("]", jsonDecodeMaxDepth)
	if err := decodeJSONUseNumber([]byte(deep), &decoded); !errors.Is(err, errJSONDepthLimit) {
		t.Fatalf("depth limit error = %v", err)
	}

	decoder := json.NewDecoder(strings.NewReader(`{"member":true}`))
	members := jsonDecodeMaxObjectMembers
	if err := validateJSONValue(decoder, 1, &members); !errors.Is(err, errJSONMemberLimit) {
		t.Fatalf("member limit error = %v", err)
	}
}

func TestFloat64FromAPIKeepsOrdinaryNumbers(t *testing.T) {
	t.Parallel()

	for _, value := range []interface{}{json.Number("12"), json.Number("12.5"), json.Number("1.25e-2"), float64(0.5), "3.75"} {
		if _, err := float64FromAPI(value); err != nil {
			t.Fatalf("float64FromAPI(%T) rejected an ordinary finite number", value)
		}
	}
	for _, value := range []interface{}{json.Number("1e10000"), "NaN", math.Inf(1), nil} {
		if _, err := float64FromAPI(value); err == nil {
			t.Fatalf("float64FromAPI(%T) accepted a non-finite or malformed value", value)
		}
	}
}

func TestAPINumericFieldsTrackPresenceAndReturnSafeErrors(t *testing.T) {
	t.Parallel()

	object := map[string]interface{}{
		"null": nil,
		"nested": map[string]interface{}{
			"exact":     json.Number("9.007199254740993e15"),
			"fraction":  json.Number("1.5"),
			"overflow":  json.Number("9223372036854775808"),
			"malformed": "https://secret.invalid/body?token=do-not-echo",
		},
	}
	if _, presence, err := apiInt64At(object, "missing"); err != nil || presence != apiValueAbsent {
		t.Fatalf("absent = %v, %v", presence, err)
	}
	if _, presence, err := apiInt64At(object, "null"); err != nil || presence != apiValueNull {
		t.Fatalf("null = %v, %v", presence, err)
	}
	if value, presence, err := apiInt64At(object, "nested", "exact"); err != nil || presence != apiValuePresent || value != 9007199254740993 {
		t.Fatalf("exact = %d, %v, %v", value, presence, err)
	}
	for _, field := range []string{"fraction", "overflow", "malformed"} {
		_, presence, err := apiInt64At(object, "nested", field)
		if err == nil || presence != apiValuePresent {
			t.Fatalf("%s = %v, %v", field, presence, err)
		}
		if !strings.Contains(err.Error(), "nested."+field) {
			t.Fatalf("error is not field-specific: %v", err)
		}
		if strings.Contains(err.Error(), "secret.invalid") || strings.Contains(err.Error(), "do-not-echo") {
			t.Fatalf("error exposed response data: %v", err)
		}
	}
	if _, _, err := apiInt64At(map[string]interface{}{"nested": "response-body"}, "nested", "value"); err == nil || strings.Contains(err.Error(), "response-body") {
		t.Fatalf("unsafe malformed relation error: %v", err)
	}
}

func TestAPINumericMapConversionIsAtomic(t *testing.T) {
	t.Parallel()

	object := map[string]interface{}{
		"metadata": map[string]interface{}{
			"model_rpm_limit": map[string]interface{}{
				"valid":   json.Number("9007199254740993"),
				"invalid": json.Number("1.25"),
			},
		},
	}
	if values, presence, err := apiInt64MapAt(object, "metadata", "model_rpm_limit"); err == nil || presence != apiValuePresent || values != nil {
		t.Fatalf("partial map escaped: %#v, %v, %v", values, presence, err)
	}

	object["metadata"].(map[string]interface{})["model_rpm_limit"] = map[string]interface{}{
		"large": json.Number("9007199254740993"),
	}
	values, presence, err := apiInt64MapAt(object, "metadata", "model_rpm_limit")
	if err != nil || presence != apiValuePresent || values["large"] != 9007199254740993 {
		t.Fatalf("exact map = %#v, %v, %v", values, presence, err)
	}
}

func TestNumericStateBridgeMakesRemoteClearsVisible(t *testing.T) {
	t.Parallel()

	integer := types.Int64Value(42)
	if err := updateInt64FromAPI(&integer, map[string]interface{}{"limit": nil}, false, true, "limit"); err != nil || !integer.IsNull() {
		t.Fatalf("explicit integer null = %v, %v", integer, err)
	}
	integer = types.Int64Value(42)
	if err := updateInt64FromAPI(&integer, map[string]interface{}{}, true, true, "limit"); err != nil || !integer.IsNull() {
		t.Fatalf("absent owned integer = %v, %v", integer, err)
	}
	integer = types.Int64Value(42)
	secret := "malformed-response-value"
	if err := updateInt64FromAPI(&integer, map[string]interface{}{"limit": secret}, true, true, "limit"); err == nil || integer.ValueInt64() != 42 || strings.Contains(err.Error(), secret) {
		t.Fatalf("malformed integer mutated state or leaked: %v, %v", integer, err)
	}

	ordinary := types.Float64Value(1.5)
	if err := updateFloat64FromAPI(&ordinary, map[string]interface{}{"cost": math.Inf(1)}, true, true, "cost"); err == nil || ordinary.ValueFloat64() != 1.5 {
		t.Fatalf("non-finite float mutated state: %v, %v", ordinary, err)
	}

	unmanagedMap := types.MapNull(types.Int64Type)
	remoteMap := map[string]interface{}{"limits": map[string]interface{}{"large": json.Number("9007199254740993")}}
	if err := updateInt64MapFromAPI(&unmanagedMap, remoteMap, false, false, "limits"); err != nil || !unmanagedMap.IsNull() {
		t.Fatalf("unmanaged map was adopted: %v, %v", unmanagedMap, err)
	}
	if err := updateInt64MapFromAPI(&unmanagedMap, remoteMap, true, true, "limits"); err != nil || unmanagedMap.IsNull() {
		t.Fatalf("imported map was not adopted: %v, %v", unmanagedMap, err)
	}
}
