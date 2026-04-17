package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestGuardrailLitellmParamKeyMaskedByLiteLLM(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key  string
		want bool
	}{
		{"api_key", true},
		{"API_KEY", true},
		{"aws_secret_access_key", true},
		{"authorization_header", true},
		{"vertex_credentials", true},
		{"token_endpoint", true},
		{"api_base", false},
		{"guardrailIdentifier", false},
		{"mode", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			t.Parallel()
			if got := guardrailLitellmParamKeyMaskedByLiteLLM(tt.key); got != tt.want {
				t.Errorf("guardrailLitellmParamKeyMaskedByLiteLLM(%q) = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestMergeGuardrailLitellmParamsFromRead(t *testing.T) {
	t.Parallel()

	t.Run("preserves sensitive keys from user and takes non-sensitive from API", func(t *testing.T) {
		t.Parallel()
		userJSON := `{"api_key":"real-secret","api_base":"https://new.example"}`
		api := map[string]interface{}{
			"api_key":  "sk-****",
			"api_base": "https://new.example",
		}
		out, err := mergeGuardrailLitellmParamsFromRead(userJSON, api)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatal(err)
		}
		if m["api_key"] != "real-secret" {
			t.Errorf("api_key: got %v", m["api_key"])
		}
		if m["api_base"] != "https://new.example" {
			t.Errorf("api_base: got %v", m["api_base"])
		}
	})

	t.Run("empty user object returns empty string", func(t *testing.T) {
		t.Parallel()
		out, err := mergeGuardrailLitellmParamsFromRead("{}", map[string]interface{}{"api_base": "x"})
		if err != nil {
			t.Fatal(err)
		}
		if out != "" {
			t.Errorf("got %q, want empty", out)
		}
	})

	t.Run("skips guardrail mode default_on keys in user JSON", func(t *testing.T) {
		t.Parallel()
		userJSON := `{"guardrail":"x","mode":"pre_call","default_on":true,"api_base":"https://a"}`
		api := map[string]interface{}{
			"guardrail": "bedrock",
			"mode":      "pre_call",
			"api_base":  "https://b",
		}
		out, err := mergeGuardrailLitellmParamsFromRead(userJSON, api)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(out), &m); err != nil {
			t.Fatal(err)
		}
		if len(m) != 1 || m["api_base"] != "https://b" {
			t.Fatalf("unexpected merge: %#v", m)
		}
	})
}

// TestReadGuardrailPreservesSensitiveMergesNonSensitive verifies read merges API values for
// non-sensitive litellm_params keys while keeping real secrets from config (LiteLLM masks those on GET).
func TestReadGuardrailPreservesSensitiveMergesNonSensitive(t *testing.T) {
	t.Parallel()

	const userSecret = "sk-real-secret-from-user"
	const userBase = "https://crowdstrike.example/v1"
	const apiBaseUpdated = "https://updated.example/v1"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"guardrail_id":   "11111111-1111-1111-1111-111111111111",
			"guardrail_name": "crowdstrike-aidr",
			"created_at":     "2024-01-01T00:00:00Z",
			"litellm_params": map[string]interface{}{
				"guardrail":  "crowdstrike_aidr",
				"mode":       []interface{}{},
				"default_on": true,
				"api_key":    "sk-****",
				"api_base":   apiBaseUpdated,
			},
			"guardrail_info": map[string]interface{}{},
		})
	}))
	defer server.Close()

	res := &GuardrailResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	userLitellmParams := `{"api_key":` + jsonString(userSecret) + `,"api_base":` + jsonString(userBase) + `}`
	data := GuardrailResourceModel{
		ID:            types.StringValue("11111111-1111-1111-1111-111111111111"),
		GuardrailID:   types.StringValue("11111111-1111-1111-1111-111111111111"),
		GuardrailName: types.StringValue("crowdstrike-aidr"),
		Guardrail:     types.StringValue("crowdstrike_aidr"),
		Mode:          types.StringValue("[]"),
		DefaultOn:     types.BoolValue(true),
		LitellmParams: types.StringValue(userLitellmParams),
	}

	if err := res.readGuardrail(context.Background(), &data); err != nil {
		t.Fatalf("readGuardrail: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal([]byte(data.LitellmParams.ValueString()), &got); err != nil {
		t.Fatalf("parse litellm_params: %v", err)
	}
	if got["api_key"] != userSecret {
		t.Errorf("api_key: got %q, want real secret preserved", got["api_key"])
	}
	if got["api_base"] != apiBaseUpdated {
		t.Errorf("api_base: got %v, want API value %q", got["api_base"], apiBaseUpdated)
	}

	if data.GuardrailName.ValueString() != "crowdstrike-aidr" {
		t.Errorf("guardrail_name: got %q", data.GuardrailName.ValueString())
	}
	if data.CreatedAt.ValueString() != "2024-01-01T00:00:00Z" {
		t.Errorf("created_at: got %q", data.CreatedAt.ValueString())
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// TestReadGuardrailUpdatesOtherFieldsWhenLitellmParamsUnset verifies that when litellm_params is not set,
// read still refreshes guardrail metadata from the API and leaves litellm_params null.
func TestReadGuardrailUpdatesOtherFieldsWhenLitellmParamsUnset(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"guardrail_id":   "22222222-2222-2222-2222-222222222222",
			"guardrail_name": "bedrock-guard",
			"created_at":     "2025-06-15T12:00:00Z",
			"litellm_params": map[string]interface{}{
				"guardrail":  "bedrock",
				"mode":       "pre_call",
				"default_on": false,
			},
		})
	}))
	defer server.Close()

	res := &GuardrailResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := GuardrailResourceModel{
		ID:            types.StringValue("22222222-2222-2222-2222-222222222222"),
		GuardrailID:   types.StringValue("22222222-2222-2222-2222-222222222222"),
		GuardrailName: types.StringValue("bedrock-guard"),
		Guardrail:     types.StringValue("bedrock"),
		Mode:          types.StringValue("pre_call"),
		LitellmParams: types.StringNull(),
	}

	if err := res.readGuardrail(context.Background(), &data); err != nil {
		t.Fatalf("readGuardrail: %v", err)
	}

	if !data.LitellmParams.IsNull() {
		t.Errorf("expected litellm_params to stay null, got %v", data.LitellmParams)
	}
	if data.Guardrail.ValueString() != "bedrock" {
		t.Errorf("guardrail: got %q", data.Guardrail.ValueString())
	}
	if data.CreatedAt.ValueString() != "2025-06-15T12:00:00Z" {
		t.Errorf("created_at: got %q", data.CreatedAt.ValueString())
	}
}

func TestBuildGuardrailRequest(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := &GuardrailResource{}

	t.Run("mode as JSON array string", func(t *testing.T) {
		t.Parallel()
		data := GuardrailResourceModel{
			GuardrailName: types.StringValue("g"),
			Guardrail:     types.StringValue("crowdstrike_aidr"),
			Mode:          types.StringValue(`[]`),
			DefaultOn:     types.BoolValue(true),
			LitellmParams: types.StringValue(`{"api_base":"https://x"}`),
		}
		body := r.buildGuardrailRequest(ctx, &data)
		g := body["guardrail"].(map[string]interface{})
		lp := g["litellm_params"].(map[string]interface{})
		mode := lp["mode"]
		arr, ok := mode.([]string)
		if !ok || len(arr) != 0 {
			t.Fatalf("mode: got %#v, want empty []string", mode)
		}
		if lp["guardrail"] != "crowdstrike_aidr" || lp["default_on"] != true {
			t.Fatalf("litellm_params: %#v", lp)
		}
		if lp["api_base"] != "https://x" {
			t.Errorf("api_base merge: %v", lp["api_base"])
		}
	})

	t.Run("mode as scalar string", func(t *testing.T) {
		t.Parallel()
		data := GuardrailResourceModel{
			GuardrailName: types.StringValue("g"),
			Guardrail:     types.StringValue("lakera"),
			Mode:          types.StringValue("pre_call"),
			LitellmParams: types.StringNull(),
		}
		body := r.buildGuardrailRequest(ctx, &data)
		lp := body["guardrail"].(map[string]interface{})["litellm_params"].(map[string]interface{})
		if lp["mode"] != "pre_call" {
			t.Errorf("mode: %v", lp["mode"])
		}
	})

	t.Run("optional guardrail_id and guardrail_info", func(t *testing.T) {
		t.Parallel()
		data := GuardrailResourceModel{
			GuardrailID:   types.StringValue("uuid-here"),
			GuardrailName: types.StringValue("n"),
			Guardrail:     types.StringValue("bedrock"),
			Mode:          types.StringValue("pre_call"),
			GuardrailInfo: types.StringValue(`{"description":"d"}`),
		}
		body := r.buildGuardrailRequest(ctx, &data)
		g := body["guardrail"].(map[string]interface{})
		if g["guardrail_id"] != "uuid-here" {
			t.Errorf("guardrail_id: %v", g["guardrail_id"])
		}
		info := g["guardrail_info"].(map[string]interface{})
		if info["description"] != "d" {
			t.Errorf("guardrail_info: %#v", info)
		}
	})

	t.Run("invalid litellm_params JSON is skipped", func(t *testing.T) {
		t.Parallel()
		data := GuardrailResourceModel{
			GuardrailName: types.StringValue("g"),
			Guardrail:     types.StringValue("x"),
			Mode:          types.StringValue("pre_call"),
			LitellmParams: types.StringValue(`not-json`),
		}
		body := r.buildGuardrailRequest(ctx, &data)
		lp := body["guardrail"].(map[string]interface{})["litellm_params"].(map[string]interface{})
		if _, ok := lp["not"]; ok {
			t.Fatal("unexpected merge from invalid JSON")
		}
	})
}

// TestReadGuardrailUpdatesGuardrailInfoFromAPI verifies guardrail_info is refreshed from GET.
func TestReadGuardrailUpdatesGuardrailInfoFromAPI(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"guardrail_id":   "33333333-3333-3333-3333-333333333333",
			"guardrail_name": "gi",
			"created_at":     "2020-01-01T00:00:00Z",
			"litellm_params": map[string]interface{}{
				"guardrail": "aporia",
				"mode":      "pre_call",
			},
			"guardrail_info": map[string]interface{}{
				"description": "from-api",
				"extra":       float64(1),
			},
		})
	}))
	defer server.Close()

	res := &GuardrailResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := GuardrailResourceModel{
		ID:            types.StringValue("33333333-3333-3333-3333-333333333333"),
		GuardrailID:   types.StringValue("33333333-3333-3333-3333-333333333333"),
		GuardrailName: types.StringValue("gi"),
		Guardrail:     types.StringValue("aporia"),
		Mode:          types.StringValue("pre_call"),
		LitellmParams: types.StringNull(),
		GuardrailInfo: types.StringValue(`{"description":"old"}`),
	}

	if err := res.readGuardrail(context.Background(), &data); err != nil {
		t.Fatalf("readGuardrail: %v", err)
	}

	var info map[string]interface{}
	if err := json.Unmarshal([]byte(data.GuardrailInfo.ValueString()), &info); err != nil {
		t.Fatal(err)
	}
	if info["description"] != "from-api" || info["extra"] != float64(1) {
		t.Fatalf("guardrail_info: %#v", info)
	}
}

// TestReadGuardrailModeArrayRoundTrip checks mode [] serializes consistently after read.
func TestReadGuardrailModeArrayRoundTrip(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"guardrail_id":   "44444444-4444-4444-4444-444444444444",
			"guardrail_name": "m",
			"litellm_params": map[string]interface{}{
				"guardrail": "g",
				"mode":      []interface{}{"pre_call", "post_call"},
			},
		})
	}))
	defer server.Close()

	res := &GuardrailResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := GuardrailResourceModel{
		ID:            types.StringValue("44444444-4444-4444-4444-444444444444"),
		GuardrailID:   types.StringValue("44444444-4444-4444-4444-444444444444"),
		GuardrailName: types.StringValue("m"),
		Guardrail:     types.StringValue("g"),
		Mode:          types.StringValue(`["pre_call","post_call"]`),
		LitellmParams: types.StringNull(),
	}

	if err := res.readGuardrail(context.Background(), &data); err != nil {
		t.Fatalf("readGuardrail: %v", err)
	}

	var want []interface{}
	_ = json.Unmarshal([]byte(`["pre_call","post_call"]`), &want)
	var got []interface{}
	if err := json.Unmarshal([]byte(data.Mode.ValueString()), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("mode: got %v, want %v", got, want)
	}
}
