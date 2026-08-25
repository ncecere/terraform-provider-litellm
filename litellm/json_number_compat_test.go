package litellm

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"
)

func TestParseKeyResponseConsumesUseNumberExactly(t *testing.T) {
	t.Parallel()

	var response map[string]interface{}
	if err := decodeJSONUseNumber([]byte(`{
		"key":"sk-test",
		"spend":12.5,
		"max_budget":1e2,
		"soft_budget":2.5e1,
		"max_parallel_requests":9,
		"tpm_limit":9007199254740993,
		"rpm_limit":1.2e3,
		"metadata":{"exact":9007199254740993},
		"model_max_budget":{"model":1.25},
		"model_rpm_limit":{"model":9007199254740993},
		"model_tpm_limit":{"model":2e3}
	}`), &response); err != nil {
		t.Fatal(err)
	}
	key, err := (&Client{}).parseKeyResponse(response)
	if err != nil {
		t.Fatal(err)
	}
	if key.Spend != 12.5 || key.MaxBudget != 100 || key.SoftBudget != 25 || key.MaxParallelRequests != 9 || key.TPMLimit != 9007199254740993 || key.RPMLimit != 1200 {
		t.Fatalf("parsed scalar numbers = %#v", key)
	}
	if got := key.Metadata["exact"]; got != "9007199254740993" {
		t.Fatalf("metadata exact number = %#v", got)
	}
	if got := key.ModelMaxBudget["model"]; got != 1.25 {
		t.Fatalf("model budget = %#v", got)
	}
	if got := key.ModelRPMLimit["model"]; got != int(9007199254740993) {
		t.Fatalf("model RPM = %#v", got)
	}
	if got := key.ModelTPMLimit["model"]; got != int(2000) {
		t.Fatalf("model TPM = %#v", got)
	}
}

func TestParseKeyResponseRejectsMalformedUseNumberFields(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		field string
		value interface{}
	}{
		{"fractional integer", "tpm_limit", json.Number("1.5")},
		{"integer overflow", "rpm_limit", json.Number("9223372036854775808")},
		{"invalid number token", "rpm_limit", json.Number("1/1")},
		{"non-finite float", "max_budget", math.Inf(1)},
		{"malformed integer map", "model_rpm_limit", map[string]interface{}{"model": json.Number("1.5")}},
		{"malformed float map", "model_max_budget", map[string]interface{}{"model": "not-a-number"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if key, err := (&Client{}).parseKeyResponse(map[string]interface{}{test.field: test.value}); err == nil || key != nil {
				t.Fatalf("parseKeyResponse accepted %s=%#v: key=%#v err=%v", test.field, test.value, key, err)
			}
		})
	}
}

func TestHandleCredentialAPIResponseRejectsMalformedSuccessJSON(t *testing.T) {
	t.Parallel()

	newResponse := func(body string) *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
	}
	for _, body := range []string{"", "null", `{`, `{"credential_name":"ok"} trailing`} {
		var result CredentialResponse
		if err := handleCredentialAPIResponse(newResponse(body), &result); err == nil {
			t.Fatalf("typed credential response accepted %q", body)
		}
	}
	if err := handleCredentialAPIResponse(newResponse(`not-json`), nil); err == nil {
		t.Fatal("ignored credential response accepted malformed JSON")
	}
	var result CredentialResponse
	if err := handleCredentialAPIResponse(newResponse(`{"credential_name":"credential"}`), &result); err != nil || result.CredentialName != "credential" {
		t.Fatalf("valid credential response = %#v, %v", result, err)
	}
	if err := handleCredentialAPIResponse(newResponse(""), nil); err != nil {
		t.Fatalf("empty mutation response was rejected: %v", err)
	}
}
