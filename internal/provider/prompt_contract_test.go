package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func promptTestSpec(promptID, environment string, version int64, content string) map[string]interface{} {
	return map[string]interface{}{
		"prompt_id": promptID, "environment": environment, "version": version,
		"created_at": "2026-08-25T00:00:00Z", "updated_at": nil,
		"litellm_params": map[string]interface{}{
			"prompt_integration": "dotprompt", "dotprompt_content": content,
			"ignore_prompt_manager_model": false,
		},
		"prompt_info": map[string]interface{}{"prompt_type": "db", "environment": environment},
	}
}

func TestPromptImportIDRoundTripAndLegacyCompatibility(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ promptID, environment string }{
		{"prompt:with/slash.v2", "staging/us east"},
		{"v1.looks.canonical", "production"},
		{"ümlaut", "环境"},
	} {
		encoded := promptImportID(test.promptID, test.environment)
		promptID, environment, err := parsePromptImportID(encoded)
		if err != nil || promptID != test.promptID || environment != test.environment {
			t.Fatalf("round trip %q: prompt=%q environment=%q err=%v", encoded, promptID, environment, err)
		}
	}
	promptID, environment, err := parsePromptImportID("legacy-prompt")
	if err != nil || promptID != "legacy-prompt" || environment != defaultPromptEnvironment {
		t.Fatalf("legacy import: prompt=%q environment=%q err=%v", promptID, environment, err)
	}
	ambiguous := "v1.YQ.Yg"
	promptID, environment, err = parsePromptImportID(legacyPromptImportID(ambiguous))
	if err != nil || promptID != ambiguous || environment != defaultPromptEnvironment {
		t.Fatalf("escaped legacy import: prompt=%q environment=%q err=%v", promptID, environment, err)
	}
	for _, malformed := range []string{"", "v1.***.eA", "legacy.***"} {
		if _, _, err := parsePromptImportID(malformed); err == nil {
			t.Fatalf("malformed import ID %q was accepted", malformed)
		}
	}
}

func TestPromptScopedExistenceRequiresAuthoritativeVersionResult(t *testing.T) {
	t.Parallel()
	for name, versionsStatus := range map[string]struct {
		status int
		exists bool
		err    bool
	}{
		"absent 404":    {http.StatusNotFound, false, false},
		"ambiguous 400": {http.StatusBadRequest, false, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/prompts/prompt" && request.URL.Query().Get("environment") == "production" {
					http.Error(writer, "ambiguous info absence", http.StatusBadRequest)
					return
				}
				if request.URL.Path == "/prompts/prompt/versions" {
					http.Error(writer, "versions result", versionsStatus.status)
					return
				}
				http.NotFound(writer, request)
			}))
			defer server.Close()
			client := &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}
			exists, err := promptScopedExists(context.Background(), client, "prompt", "production")
			if exists != versionsStatus.exists || (err != nil) != versionsStatus.err {
				t.Fatalf("exists=%t err=%v", exists, err)
			}
		})
	}
}

func TestPromptEndpointsAlwaysScopeEnvironment(t *testing.T) {
	t.Parallel()
	endpoint := promptEndpoint("prompt/with spaces", "prod/east", nil)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("environment") != "prod/east" || parsed.EscapedPath() != "/prompts/prompt%2Fwith%20spaces" {
		t.Fatalf("scoped endpoint = %s", endpoint)
	}
	version := int64(7)
	versionEndpoint := promptEndpoint("prompt", "production", &version)
	if versionEndpoint != "/prompts/prompt.v7?environment=production" {
		t.Fatalf("version endpoint = %s", versionEndpoint)
	}
	if got := promptVersionsEndpoint("prompt", "production"); got != "/prompts/prompt/versions?environment=production" {
		t.Fatalf("versions endpoint = %s", got)
	}
}

func TestBuildPromptRequestAlwaysIncludesEnvironment(t *testing.T) {
	t.Parallel()
	request, err := (&PromptResource{}).buildPromptRequest(context.Background(), &PromptResourceModel{
		PromptID: types.StringValue("prompt"), PromptIntegration: types.StringValue("dotprompt"), Environment: types.StringValue("staging"),
	})
	if err != nil {
		t.Fatal(err)
	}
	info, ok := request["prompt_info"].(map[string]interface{})
	if !ok || info["environment"] != "staging" || info["prompt_type"] != "db" {
		t.Fatalf("prompt_info = %#v", request["prompt_info"])
	}
}

func TestPromptAuthoritativeConfigTypeIsReadOnly(t *testing.T) {
	t.Parallel()
	for name, info := range map[string]map[string]interface{}{
		"config":     {"prompt_type": "config"},
		"missing":    {},
		"null":       {"prompt_type": nil},
		"wrong type": {"prompt_type": true},
		"unknown":    {"prompt_type": "other"},
	} {
		if err := validateMutablePromptInfo(info); err == nil {
			t.Fatalf("%s prompt info was treated as mutable", name)
		}
	}
	if err := validateMutablePromptInfo(map[string]interface{}{"prompt_type": "db"}); err != nil {
		t.Fatalf("database prompt rejected: %v", err)
	}
}

func TestPromptUpdateCoverageRejectsDestructiveImportedUpdates(t *testing.T) {
	t.Parallel()
	base := PromptResourceModel{APIKey: types.StringNull()}
	if err := validatePromptUpdateCoverage(map[string]interface{}{"prompt_integration": "dotprompt", "custom_secret": "value"}, &base, &base); err == nil {
		t.Fatal("unmodeled remote parameter was silently dropped")
	}
	if err := validatePromptUpdateCoverage(map[string]interface{}{"prompt_integration": "dotprompt", "api_key": "masked-or-remote"}, &base, &base); err == nil {
		t.Fatal("unowned remote API key was silently dropped")
	}
	configured := base
	configured.APIKey = types.StringValue("configured")
	if err := validatePromptUpdateCoverage(map[string]interface{}{"prompt_integration": "dotprompt", "api_key": "remote"}, &configured, &base); err != nil {
		t.Fatalf("configured API key update rejected: %v", err)
	}
	removed, prior := base, base
	prior.APIKey = types.StringValue("prior-owned")
	if err := validatePromptUpdateCoverage(map[string]interface{}{"prompt_integration": "dotprompt", "api_key": "remote"}, &removed, &prior); err != nil {
		t.Fatalf("owned API key removal rejected: %v", err)
	}
}

func TestPromptObjectStrictEnvironmentVersionIdentity(t *testing.T) {
	t.Parallel()
	wrapped := map[string]interface{}{"prompt_spec": promptTestSpec("prompt", "production", 3, "content")}
	observed, err := promptObject(wrapped, true, "prompt", "production")
	if err != nil || !observed.HasVersion || observed.Version != 3 {
		t.Fatalf("observed=%#v err=%v", observed, err)
	}
	for name, mutate := range map[string]func(map[string]interface{}){
		"identity":            func(value map[string]interface{}) { value["prompt_id"] = "other" },
		"environment":         func(value map[string]interface{}) { value["environment"] = "development" },
		"missing environment": func(value map[string]interface{}) { delete(value, "environment") },
		"conflicting environment": func(value map[string]interface{}) {
			value["prompt_info"].(map[string]interface{})["environment"] = "development"
		},
		"version": func(value map[string]interface{}) { value["version"] = json.Number("0") },
		"params":  func(value map[string]interface{}) { value["litellm_params"] = []interface{}{} },
	} {
		t.Run(name, func(t *testing.T) {
			value := promptTestSpec("prompt", "production", 3, "content")
			mutate(value)
			if _, err := promptObject(map[string]interface{}{"prompt_spec": value}, true, "prompt", "production"); err == nil {
				t.Fatal("malformed prompt response was accepted")
			}
		})
	}
}

func TestPromptEnvironmentFilteredListRecoversRegistryCollision(t *testing.T) {
	t.Parallel()
	requests := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests[request.URL.RequestURI()]++
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.RequestURI() {
		case "/prompts/list":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"prompts": []interface{}{promptTestSpec("same", "development", 2, "development")}})
		case "/prompts/list?environment=production":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"prompts": []interface{}{}})
		case "/prompts/same?environment=production":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"prompt_spec": promptTestSpec("same", "production", 1, "production")})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}
	items, err := fetchPromptListItems(context.Background(), client, "production", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Environment.ValueString() != "production" || items[0].Version.ValueInt64() != 1 {
		t.Fatalf("collision recovery items=%#v", items)
	}
	if requests["/prompts/same?environment=production"] != 1 {
		t.Fatalf("scoped info requests=%#v", requests)
	}
}

func TestPromptEnvironmentFilteredListEnrichmentIsBounded(t *testing.T) {
	t.Parallel()
	prompts := make([]interface{}, 201)
	for index := range prompts {
		prompts[index] = promptTestSpec(fmt.Sprintf("prompt-%03d", index), "development", 1, "content")
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.RequestURI() == "/prompts/list" {
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"prompts": prompts})
			return
		}
		if request.URL.RequestURI() == "/prompts/list?environment=production" {
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"prompts": []interface{}{}})
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}
	if _, err := fetchPromptListItems(context.Background(), client, "production", true); err == nil {
		t.Fatal("over-limit prompt enrichment was accepted")
	}
}

func TestPromptResourceSchemaRemainsV0WithAdditiveIdentityFields(t *testing.T) {
	t.Parallel()
	var response resource.SchemaResponse
	(&PromptResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Schema.Version != 0 {
		t.Fatalf("schema version = %d", response.Schema.Version)
	}
	for name := range map[string]struct{}{"environment": {}, "version": {}, "created_at": {}, "updated_at": {}} {
		if _, exists := response.Schema.Attributes[name]; !exists {
			t.Fatalf("missing additive attribute %s", name)
		}
	}
	if _, ok := response.Schema.Attributes["prompt_id"].(resourceschema.StringAttribute); !ok {
		t.Fatal("prompt_id public type changed")
	}
}
