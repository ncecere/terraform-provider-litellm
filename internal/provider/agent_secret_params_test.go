package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestIsLiteLLMSensitiveParamKey(t *testing.T) {
	for key, want := range map[string]bool{
		"api_key": true, "vertex_credentials": true, "aws_access_key_id": true, "aws_secret_access_key": true,
		"client-secret": true, "auth_type": true, "private_key": true, "Authorization": true,
		"model": false, "api_base": false, "max_tokens": false, "input_cost_per_token": false,
		"keyword": false, "monkey": false,
	} {
		if got := isLiteLLMSensitiveParamKey(key); got != want {
			t.Errorf("isLiteLLMSensitiveParamKey(%q)=%t want %t", key, got, want)
		}
	}
}

func TestRestoreMaskedAgentLeavesAcceptsV1104MarkerForAnyType(t *testing.T) {
	prior := map[string]interface{}{"type": "service_account", "project_id": "p"}
	restored, err := restoreMaskedAgentLeaves(map[string]interface{}{"vertex_credentials": liteLLMRedactedMarker, "model": "m"}, map[string]interface{}{"vertex_credentials": prior, "model": "m"}, "")
	if err != nil {
		t.Fatal(err)
	}
	object := restored.(map[string]interface{})
	if credentials, ok := object["vertex_credentials"].(map[string]interface{}); !ok || credentials["type"] != "service_account" {
		t.Fatalf("object secret not restored: %#v", object)
	}
	if _, err := restoreMaskedAgentLeaves(liteLLMRedactedMarker, nil, "api_key"); err == nil {
		t.Fatal("marker without an owned prior value was accepted")
	}
	if !isMaskedMetadataAPIString(liteLLMRedactedMarker) || !isMaskedAgentAPIValue("anything", liteLLMRedactedMarker) {
		t.Fatal("v1.104 marker not recognized")
	}
}

// agentV1104API mimics LiteLLM 1.104.0 agent storage: secret litellm_params
// are masked on every response and kept when a PATCH omits them.
type agentV1104API struct {
	mu      sync.Mutex
	params  map[string]interface{}
	patches []map[string]interface{}
}

func (a *agentV1104API) response() map[string]interface{} {
	masked := map[string]interface{}{}
	for key, value := range a.params {
		if isLiteLLMSensitiveParamKey(key) {
			masked[key] = liteLLMRedactedMarker
		} else {
			masked[key] = value
		}
	}
	return map[string]interface{}{"agent_id": "secret-agent", "agent_name": "secret-agent", "litellm_params": masked,
		"agent_card_params": map[string]interface{}{"name": "Secret Agent", "url": "https://agent.invalid"}}
}

func (a *agentV1104API) handler(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	var body map[string]interface{}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/agents":
		a.params, _ = body["litellm_params"].(map[string]interface{})
	case r.Method == http.MethodPatch && r.URL.Path == "/v1/agents/secret-agent":
		a.patches = append(a.patches, body)
		if incoming, ok := body["litellm_params"].(map[string]interface{}); ok {
			next := map[string]interface{}{}
			for key, value := range incoming {
				next[key] = value
			}
			for key, value := range a.params {
				if _, sent := incoming[key]; !sent && isLiteLLMSensitiveParamKey(key) {
					next[key] = value // 1.104.0 keeps omitted secrets
				}
			}
			a.params = next
		}
	case r.Method == http.MethodGet && r.URL.Path == "/v1/agents/secret-agent":
	default:
		http.NotFound(w, r)
		return
	}
	_ = json.NewEncoder(w).Encode(a.response())
}

func TestAgentV1104SecretParamsProtocol(t *testing.T) {
	ctx := context.Background()
	api := &agentV1104API{}
	server := httptest.NewServer(http.HandlerFunc(api.handler))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_agent"]
	params := func(values map[string]string) map[string]tftypes.Value {
		out := map[string]tftypes.Value{}
		for key, value := range values {
			out[key] = tftypes.NewValue(tftypes.String, value)
		}
		return out
	}
	cardType := schema.ValueType().(tftypes.Object).AttributeTypes["agent_card"]
	var card interface{}
	cardObject := func(objectType tftypes.Object) map[string]tftypes.Value {
		values := map[string]tftypes.Value{}
		for name, attributeType := range objectType.AttributeTypes {
			values[name] = tftypes.NewValue(attributeType, nil)
		}
		values["name"] = tftypes.NewValue(tftypes.String, "Secret Agent")
		values["url"] = tftypes.NewValue(tftypes.String, "https://agent.invalid")
		return values
	}
	switch typed := cardType.(type) {
	case tftypes.Object:
		card = cardObject(typed)
	case tftypes.List:
		element := typed.ElementType.(tftypes.Object)
		card = []tftypes.Value{tftypes.NewValue(element, cardObject(element))}
	default:
		t.Fatalf("unexpected agent_card type %T", cardType)
	}
	configValues := func(p map[string]string) map[string]interface{} {
		return map[string]interface{}{"agent_name": "secret-agent", "litellm_params": params(p), "agent_card": card}
	}
	config := func(p map[string]string) *tfprotov6.DynamicValue {
		return accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues(p)))
	}
	null := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))

	initial := map[string]string{"model": "openai/gpt-4o-mini", "api_key": "sk-agent-secret", "region": "us"}
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: config(initial), PriorState: null, ProposedNewState: config(initial)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_agent", Config: config(initial), PriorState: null, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("create apply err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(created.Diagnostics))
	}
	var stored map[string]tftypes.Value
	var apiKey string
	if err := protocolAttributeMap(t, schema, created.NewState)["litellm_params"].As(&stored); err != nil || stored["api_key"].As(&apiKey) != nil || apiKey != "sk-agent-secret" {
		t.Fatalf("configured secret not retained in state (marker leaked?): %q err=%v", apiKey, err)
	}

	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: created.NewState, Private: created.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("refresh err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	plan := func(p map[string]string) *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		response, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: config(p), PriorState: read.NewState, ProposedNewState: organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"litellm_params": params(p)}), PriorPrivate: read.Private})
		if err != nil {
			t.Fatal(err)
		}
		return response
	}
	if steady := plan(initial); accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, steady) != organizationProjectProtocolActionNoOp {
		t.Fatalf("masked refresh caused drift: diagnostics=%s action=%s", agentProtocolDiagnosticsText(steady.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, read.NewState, steady))
	}

	removal := plan(map[string]string{"model": "openai/gpt-4o-mini", "region": "us"})
	text := agentProtocolDiagnosticsText(removal.Diagnostics)
	if !accessGroupProtocolDiagnosticsHaveError(removal.Diagnostics) || !strings.Contains(text, "Agent Secret Parameter Cannot Be Removed In Place") || !strings.Contains(text, "api_key") {
		t.Fatalf("secret removal was not rejected: %s", text)
	}
	if strings.Contains(text, "sk-agent-secret") {
		t.Fatalf("diagnostic leaked the secret: %s", text)
	}

	changed := map[string]string{"model": "openai/gpt-4o-mini", "api_key": "sk-agent-rotated", "region": "us"}
	changePlan := plan(changed)
	if accessGroupProtocolDiagnosticsHaveError(changePlan.Diagnostics) {
		t.Fatalf("secret change plan: %s", agentProtocolDiagnosticsText(changePlan.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_agent", Config: config(changed), PriorState: read.NewState, PlannedState: changePlan.PlannedState, PlannedPrivate: changePlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("secret change apply err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	if api.params["api_key"] != "sk-agent-rotated" {
		t.Fatalf("rotated secret not sent: %#v", api.params)
	}

	// Removing a non-secret key still converges in place.
	nonSecret := map[string]string{"model": "openai/gpt-4o-mini", "api_key": "sk-agent-rotated"}
	nonSecretPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: config(nonSecret), PriorState: applied.NewState, ProposedNewState: organizationProjectProtocolReplace(t, schema, applied.NewState, map[string]interface{}{"litellm_params": params(nonSecret)}), PriorPrivate: applied.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(nonSecretPlan.Diagnostics) {
		t.Fatalf("non-secret removal plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(nonSecretPlan.Diagnostics))
	}
	if _, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_agent", Config: config(nonSecret), PriorState: applied.NewState, PlannedState: nonSecretPlan.PlannedState, PlannedPrivate: nonSecretPlan.PlannedPrivate}); err != nil {
		t.Fatal(err)
	}
	if _, kept := api.params["region"]; kept {
		t.Fatalf("non-secret key was not removed: %#v", api.params)
	}
}
