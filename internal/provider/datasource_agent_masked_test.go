package provider

import (
	"context"
	"testing"
)

// LiteLLM 1.104.0 masks secret agent litellm_params for every caller, proxy
// admins included. A read-only data source cannot restore them, so it reports
// the placeholder instead of failing the whole read.
func TestProjectAgentDataKeepsLiteLLMMaskedSecretParams(t *testing.T) {
	item := map[string]interface{}{
		"agent_id":   "agent-1",
		"agent_name": "masked-agent",
		"litellm_params": map[string]interface{}{
			"model":     "openai/gpt-4o-mini",
			"api_key":   liteLLMRedactedMarker,
			"is_public": false,
		},
	}
	data, err := projectAgentData(context.Background(), item, "agent-1")
	if err != nil {
		t.Fatalf("project masked agent: %v", err)
	}
	want := `{"api_key":"REDACTED_BY_LITELM","model":"openai/gpt-4o-mini"}`
	if got := data.LiteLLMParamsJSON.ValueString(); !jsonSemanticallyEqual(got, want) {
		t.Fatalf("litellm_params_json = %s, want %s", got, want)
	}
	if got := data.LiteLLMParams.Elements()["api_key"].String(); got != `"REDACTED_BY_LITELM"` {
		t.Fatalf("litellm_params.api_key = %s", got)
	}
	if _, present := data.LiteLLMParams.Elements()["is_public"]; present {
		t.Fatal("synthetic is_public leaked into the projection")
	}
}
