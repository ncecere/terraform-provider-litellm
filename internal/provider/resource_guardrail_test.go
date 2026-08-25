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

func TestRemoveUnconfiguredGuardrailNulls(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		configured string
		api        string
		want       string
	}{
		"pattern default": {
			configured: `[{"pattern_type":"regex","pattern":"*foo*","name":"test","action":"MASK"}]`,
			api:        `[{"pattern_type":"regex","pattern":"*foo*","pattern_name":null,"name":"test","action":"MASK"}]`,
			want:       `[{"pattern_type":"regex","pattern":"*foo*","name":"test","action":"MASK"}]`,
		},
		"rule defaults": {
			configured: `[{"id":"allow_bash","tool_name":"Bash","decision":"allow"}]`,
			api:        `[{"id":"allow_bash","tool_name":"Bash","decision":"allow","tool_type":null,"allowed_param_patterns":null}]`,
			want:       `[{"id":"allow_bash","tool_name":"Bash","decision":"allow"}]`,
		},
		"explicit null retained": {
			configured: `[{"id":"allow_bash","tool_type":null}]`,
			api:        `[{"id":"allow_bash","tool_type":null,"decision":null}]`,
			want:       `[{"id":"allow_bash","tool_type":null}]`,
		},
		"non-null addition retained": {
			configured: `[{"id":"allow_bash"}]`,
			api:        `[{"id":"allow_bash","decision":"deny","tool_type":null}]`,
			want:       `[{"id":"allow_bash","decision":"deny"}]`,
		},
		"missing configured value remains missing": {
			configured: `[{"id":"allow_bash","decision":"allow"}]`,
			api:        `[{"id":"allow_bash"}]`,
			want:       `[{"id":"allow_bash"}]`,
		},
		"extra array item retained": {
			configured: `[{"id":"one"}]`,
			api:        `[{"id":"one","decision":null},{"id":"two","decision":null}]`,
			want:       `[{"id":"one"},{"id":"two","decision":null}]`,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var configured, api, want interface{}
			if err := json.Unmarshal([]byte(test.configured), &configured); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.api), &api); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.want), &want); err != nil {
				t.Fatal(err)
			}
			got := removeUnconfiguredGuardrailNulls(api, configured)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("filtered value = %#v, want %#v", got, want)
			}
		})
	}
}

func TestReadGuardrailFiltersNestedNullDefaults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"guardrail_id":   "guardrail-1",
			"guardrail_name": "test",
			"litellm_params": map[string]interface{}{
				"guardrail":  "litellm_content_filter",
				"mode":       "pre_call",
				"default_on": true,
				"patterns": []interface{}{
					map[string]interface{}{
						"pattern_type": "regex",
						"pattern":      "*foo*",
						"pattern_name": nil,
						"name":         "test",
						"action":       "MASK",
					},
				},
				"api_default_not_configured": "ignored",
			},
		})
	}))
	defer server.Close()

	resource := &GuardrailResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	configured := `{"patterns":[{"action":"MASK","name":"test","pattern":"*foo*","pattern_type":"regex"}]}`
	data := GuardrailResourceModel{
		ID:            types.StringValue("guardrail-1"),
		GuardrailID:   types.StringValue("guardrail-1"),
		GuardrailName: types.StringValue("test"),
		Guardrail:     types.StringValue("litellm_content_filter"),
		Mode:          types.StringValue("pre_call"),
		DefaultOn:     types.BoolValue(true),
		LitellmParams: types.StringValue(configured),
	}

	if err := resource.readGuardrail(context.Background(), &data, false); err != nil {
		t.Fatalf("readGuardrail returned error: %v", err)
	}
	if got := data.LitellmParams.ValueString(); got != configured {
		t.Fatalf("litellm_params = %s, want %s", got, configured)
	}
}

func TestReadGuardrailPreservesRealNestedDrift(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"guardrail_id":   "guardrail-1",
			"guardrail_name": "test",
			"litellm_params": map[string]interface{}{
				"guardrail": "tool_permission",
				"mode":      "post_call",
				"rules": []interface{}{
					map[string]interface{}{
						"id":                     "allow_bash",
						"tool_name":              "Bash",
						"decision":               "deny",
						"tool_type":              nil,
						"allowed_param_patterns": nil,
					},
				},
			},
		})
	}))
	defer server.Close()

	resource := &GuardrailResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	data := GuardrailResourceModel{
		ID:            types.StringValue("guardrail-1"),
		GuardrailID:   types.StringValue("guardrail-1"),
		LitellmParams: types.StringValue(`{"rules":[{"decision":"allow","id":"allow_bash","tool_name":"Bash"}]}`),
	}
	if err := resource.readGuardrail(context.Background(), &data, false); err != nil {
		t.Fatalf("readGuardrail returned error: %v", err)
	}
	want := `{"rules":[{"decision":"deny","id":"allow_bash","tool_name":"Bash"}]}`
	if got := data.LitellmParams.ValueString(); got != want {
		t.Fatalf("litellm_params = %s, want %s", got, want)
	}
}
