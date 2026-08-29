package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const agentClearProtocolID = "agent-clear"

type agentClearOverlayProtocolAPI struct {
	mu          sync.Mutex
	serverURL   string
	created     bool
	cleared     bool
	adversarial string
	requests    atomic.Int64
	posts       atomic.Int64
	patches     atomic.Int64
	badMutation atomic.Bool
}

func agentClearProtocolSecuritySchemes() map[string]interface{} {
	return map[string]interface{}{
		"LiteLLMKey": map[string]interface{}{
			"type": "http", "scheme": "bearer", "description": "LiteLLM virtual key",
		},
	}
}

func agentClearProtocolSupportedInterfaces(serverURL string) []interface{} {
	return []interface{}{map[string]interface{}{
		"url": serverURL + "/a2a/" + agentClearProtocolID, "protocolBinding": "JSONRPC", "protocolVersion": "1.0",
	}}
}

func agentClearProtocolSetCard(serverURL string) map[string]interface{} {
	return map[string]interface{}{
		"name": "Smoke Agent Lifecycle", "description": "set then clear", "url": "https://agent.example.com/lifecycle",
		"version": "1.0.0", "protocolVersion": "1.0",
		"defaultInputModes": []interface{}{"text"}, "defaultOutputModes": []interface{}{"text"},
		"preferredTransport": "JSONRPC", "iconUrl": "https://agent.example.com/icon.png", "documentationUrl": "https://agent.example.com/docs",
		"supportsAuthenticatedExtendedCard": true,
		"capabilities":                      map[string]interface{}{"streaming": true},
		"provider":                          map[string]interface{}{"organization": "Acceptance", "url": "https://agent.example.com"},
		"skills": []interface{}{map[string]interface{}{
			"id": "acceptance", "name": "Acceptance", "description": "set then clear",
			"tags": []interface{}{"lifecycle"}, "examples": []interface{}{"test"}, "inputModes": []interface{}{"text"}, "outputModes": []interface{}{"text"},
			"security": []interface{}{map[string]interface{}{"oauth2": []interface{}{"read", "read"}}},
		}},
		"signatures": []interface{}{
			map[string]interface{}{"protected": "acceptance-protected", "signature": "acceptance-signature", "header": map[string]interface{}{"duplicate": json.Number("0"), "exact": json.Number("9007199254740993")}},
			map[string]interface{}{"protected": "acceptance-protected", "signature": "acceptance-signature", "header": map[string]interface{}{"duplicate": json.Number("1"), "exact": json.Number("9007199254740993")}},
		},
		"security":            []interface{}{map[string]interface{}{"LiteLLMKey": []interface{}{}}},
		"securitySchemes":     agentClearProtocolSecuritySchemes(),
		"supportedInterfaces": agentClearProtocolSupportedInterfaces(serverURL),
	}
}

func agentClearProtocolClearedCard(serverURL string) map[string]interface{} {
	return map[string]interface{}{
		"name": "Smoke Agent Lifecycle", "url": "https://agent.example.com/lifecycle", "version": "1.0.0", "protocolVersion": "1.0",
		"defaultInputModes": []interface{}{"text"}, "defaultOutputModes": []interface{}{"text"},
		"supportsAuthenticatedExtendedCard": false,
		// v1.98's merge always materializes the capabilities parent while filtering
		// every configured false leaf from it.
		"capabilities": map[string]interface{}{},
		"provider":     map[string]interface{}{"url": "https://agent.example.com"},
		"skills": []interface{}{map[string]interface{}{
			"id": "acceptance", "name": "Acceptance", "tags": []interface{}{}, "examples": []interface{}{},
			"inputModes": []interface{}{}, "outputModes": []interface{}{}, "security": []interface{}{},
		}},
		// The fixture's empty dynamic signatures block is represented by omission
		// on the complete-card PATCH; v1.98 preserves that omission on read-back.
		"security":            []interface{}{map[string]interface{}{"LiteLLMKey": []interface{}{}}},
		"securitySchemes":     agentClearProtocolSecuritySchemes(),
		"supportedInterfaces": agentClearProtocolSupportedInterfaces(serverURL),
	}
}

func agentClearProtocolObjectPermission(cleared bool) map[string]interface{} {
	permission := map[string]interface{}{
		"object_permission_id": "permission-clear", "vector_stores": []interface{}{}, "agent_access_groups": []interface{}{},
		"blocked_tools": []interface{}{}, "mcp_toolsets": []interface{}{}, "search_tools": []interface{}{}, "mcp_tool_search_enabled": nil,
		"teams": nil, "projects": nil, "verification_tokens": nil, "organizations": nil, "users": nil, "end_users": nil, "agents_table": nil,
	}
	if cleared {
		permission["mcp_servers"] = []interface{}{}
		permission["mcp_access_groups"] = []interface{}{}
		permission["mcp_tool_permissions"] = map[string]interface{}{}
		permission["models"] = []interface{}{}
		permission["agents"] = []interface{}{}
	} else {
		permission["mcp_servers"] = []interface{}{"acceptance-server"}
		permission["mcp_access_groups"] = []interface{}{"acceptance-group"}
		permission["mcp_tool_permissions"] = map[string]interface{}{"acceptance-server": []interface{}{"acceptance-tool"}}
		permission["models"] = []interface{}{"openai/gpt-4o-mini"}
		permission["agents"] = []interface{}{"acceptance-agent"}
	}
	return permission
}

func agentClearProtocolResponse(serverURL string, cleared bool, adversarial string) map[string]interface{} {
	card := agentClearProtocolSetCard(serverURL)
	params := map[string]interface{}{"model": "openai/gpt-4o-mini", "qualifier": "acceptance"}
	staticHeaders := map[string]interface{}{"X-Agent-Acceptance": "set"}
	extraHeaders := []interface{}{"X-Agent-Acceptance"}
	if cleared {
		card = agentClearProtocolClearedCard(serverURL)
		params = map[string]interface{}{"model": "openai/gpt-4o-mini"}
		staticHeaders = map[string]interface{}{}
		extraHeaders = []interface{}{}
	}
	switch adversarial {
	case "top-level":
		card["x-adversarial"] = map[string]interface{}{"must": "not mutate"}
	case "nested-capability":
		card["capabilities"].(map[string]interface{})["x-adversarial"] = map[string]interface{}{"must": "not mutate"}
	}
	response := map[string]interface{}{
		"agent_id": agentClearProtocolID, "agent_name": "smoke-agent-lifecycle",
		"litellm_params": params, "agent_card_params": card, "object_permission": agentClearProtocolObjectPermission(cleared),
		"static_headers": staticHeaders, "extra_headers": extraHeaders,
		"spend": json.Number("0.0"), "keys": nil,
		"created_at": "2026-08-26T10:08:47.128000Z", "updated_at": "2026-08-26T10:08:47.128000Z",
		"created_by": "default_user_id", "updated_by": "default_user_id",
	}
	for _, field := range []string{"tpm_limit", "rpm_limit", "session_tpm_limit", "session_rpm_limit"} {
		response[field] = nil
	}
	if !cleared {
		response["tpm_limit"] = json.Number("1200")
		response["rpm_limit"] = json.Number("120")
		response["session_tpm_limit"] = json.Number("600")
		response["session_rpm_limit"] = json.Number("60")
	}
	return response
}

func agentClearProtocolCreateRequest() map[string]interface{} {
	return map[string]interface{}{
		"agent_name": "smoke-agent-lifecycle", "tpm_limit": json.Number("1200"), "rpm_limit": json.Number("120"),
		"session_tpm_limit": json.Number("600"), "session_rpm_limit": json.Number("60"),
		"litellm_params": map[string]interface{}{"model": "openai/gpt-4o-mini", "qualifier": "acceptance"},
		"static_headers": map[string]interface{}{"X-Agent-Acceptance": "set"}, "extra_headers": []interface{}{"X-Agent-Acceptance"},
		"agent_card_params": map[string]interface{}{
			"name": "Smoke Agent Lifecycle", "description": "set then clear", "url": "https://agent.example.com/lifecycle",
			"version": "1.0.0", "protocolVersion": "1.0", "defaultInputModes": []interface{}{"text"}, "defaultOutputModes": []interface{}{"text"},
			"preferredTransport": "JSONRPC", "iconUrl": "https://agent.example.com/icon.png", "documentationUrl": "https://agent.example.com/docs",
			"supportsAuthenticatedExtendedCard": true,
			"capabilities":                      map[string]interface{}{"streaming": true, "pushNotifications": false, "stateTransitionHistory": false},
			"provider":                          map[string]interface{}{"organization": "Acceptance", "url": "https://agent.example.com"},
			"skills": []interface{}{map[string]interface{}{
				"id": "acceptance", "name": "Acceptance", "description": "set then clear", "tags": []interface{}{"lifecycle"}, "examples": []interface{}{"test"},
				"inputModes": []interface{}{"text"}, "outputModes": []interface{}{"text"}, "security": []interface{}{map[string]interface{}{"oauth2": []interface{}{"read", "read"}}},
			}},
			"signatures": []interface{}{
				map[string]interface{}{"protected": "acceptance-protected", "signature": "acceptance-signature", "header": map[string]interface{}{"duplicate": json.Number("0"), "exact": json.Number("9007199254740993")}},
				map[string]interface{}{"protected": "acceptance-protected", "signature": "acceptance-signature", "header": map[string]interface{}{"duplicate": json.Number("1"), "exact": json.Number("9007199254740993")}},
			},
		},
		"object_permission": map[string]interface{}{
			"mcp_servers": []interface{}{"acceptance-server"}, "mcp_access_groups": []interface{}{"acceptance-group"},
			"mcp_tool_permissions": map[string]interface{}{"acceptance-server": []interface{}{"acceptance-tool"}},
			"models":               []interface{}{"openai/gpt-4o-mini"}, "agents": []interface{}{"acceptance-agent"},
		},
	}
}

func agentClearProtocolPatchRequest(serverURL string) map[string]interface{} {
	card := agentClearProtocolClearedCard(serverURL)
	// False capability leaves are clear intent in Terraform but v1.98 filters
	// them. The complete-card PATCH must omit the parent rather than sending an
	// empty or false-bearing object; GET canonicalization rematerializes {}.
	delete(card, "capabilities")
	return map[string]interface{}{
		"agent_name": "smoke-agent-lifecycle",
		"tpm_limit":  nil, "rpm_limit": nil, "session_tpm_limit": nil, "session_rpm_limit": nil,
		"litellm_params": map[string]interface{}{"model": "openai/gpt-4o-mini"},
		"static_headers": map[string]interface{}{}, "extra_headers": []interface{}{},
		"agent_card_params": card,
		"object_permission": map[string]interface{}{
			"mcp_servers": []interface{}{}, "mcp_access_groups": []interface{}{}, "mcp_tool_permissions": map[string]interface{}{},
			"models": []interface{}{}, "agents": []interface{}{},
		},
	}
}

func agentClearProtocolExactShape(got, want map[string]interface{}) error {
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("wire object did not match the exact v1.98 shape")
	}
	return nil
}

func agentClearProtocolCanonicalResponseShape(got, want map[string]interface{}) error {
	if err := agentClearProtocolExactShape(got, want); err != nil {
		return err
	}
	card := got["agent_card_params"].(map[string]interface{})
	capabilities, present := card["capabilities"].(map[string]interface{})
	if !present || len(capabilities) != 0 {
		return fmt.Errorf("canonical response did not materialize an empty capabilities parent")
	}
	return nil
}

func (a *agentClearOverlayProtocolAPI) response() map[string]interface{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	return agentClearProtocolResponse(a.serverURL, a.cleared, a.adversarial)
}

func (a *agentClearOverlayProtocolAPI) setAdversarial(value string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.adversarial = value
}

func (a *agentClearOverlayProtocolAPI) handler(w http.ResponseWriter, r *http.Request) {
	a.requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	decode := func() (map[string]interface{}, bool) {
		var request map[string]interface{}
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&request); err != nil {
			a.badMutation.Store(true)
			http.Error(w, `{}`, http.StatusBadRequest)
			return nil, false
		}
		return request, true
	}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/agents":
		a.posts.Add(1)
		request, ok := decode()
		if !ok {
			return
		}
		if err := agentClearProtocolExactShape(request, agentClearProtocolCreateRequest()); err != nil {
			a.badMutation.Store(true)
			http.Error(w, `{}`, http.StatusBadRequest)
			return
		}
		a.mu.Lock()
		a.created = true
		a.mu.Unlock()
		_ = json.NewEncoder(w).Encode(a.response())
	case r.Method == http.MethodGet && r.URL.Path == "/v1/agents/"+agentClearProtocolID:
		a.mu.Lock()
		created := a.created
		cleared := a.cleared
		adversarial := a.adversarial
		a.mu.Unlock()
		if !created {
			http.NotFound(w, r)
			return
		}
		response := agentClearProtocolResponse(a.serverURL, cleared, adversarial)
		if cleared && adversarial == "" {
			if err := agentClearProtocolCanonicalResponseShape(response, agentClearProtocolResponse(a.serverURL, true, "")); err != nil {
				a.badMutation.Store(true)
				http.Error(w, `{}`, http.StatusInternalServerError)
				return
			}
		}
		_ = json.NewEncoder(w).Encode(response)
	case r.Method == http.MethodPatch && r.URL.Path == "/v1/agents/"+agentClearProtocolID:
		a.patches.Add(1)
		request, ok := decode()
		if !ok {
			return
		}
		if err := agentClearProtocolExactShape(request, agentClearProtocolPatchRequest(a.serverURL)); err != nil {
			a.badMutation.Store(true)
			http.Error(w, `{}`, http.StatusBadRequest)
			return
		}
		a.mu.Lock()
		a.cleared = true
		a.mu.Unlock()
		_ = json.NewEncoder(w).Encode(a.response())
	default:
		http.NotFound(w, r)
	}
}

func agentClearOverlaySetConfig() map[string]interface{} {
	return map[string]interface{}{
		"agent_name": "smoke-agent-lifecycle", "tpm_limit": int64(1200), "rpm_limit": int64(120),
		"session_tpm_limit": int64(600), "session_rpm_limit": int64(60),
		"litellm_params": map[string]interface{}{"model": "openai/gpt-4o-mini", "qualifier": "acceptance"},
		"static_headers": map[string]interface{}{"X-Agent-Acceptance": "set"}, "extra_headers": []interface{}{"X-Agent-Acceptance"},
		"agent_card": map[string]interface{}{
			"name": "Smoke Agent Lifecycle", "description": "set then clear", "url": "https://agent.example.com/lifecycle",
			"version": "1.0.0", "protocol_version": "1.0", "default_input_modes": []interface{}{"text"}, "default_output_modes": []interface{}{"text"},
			"preferred_transport": "JSONRPC", "icon_url": "https://agent.example.com/icon.png", "documentation_url": "https://agent.example.com/docs",
			"supports_authenticated_extended_card": true,
			"capabilities":                         map[string]interface{}{"streaming": true, "push_notifications": false, "state_transition_history": false},
			"provider":                             map[string]interface{}{"organization": "Acceptance", "url": "https://agent.example.com"},
			"skills": []interface{}{map[string]interface{}{
				"id": "acceptance", "name": "Acceptance", "description": "set then clear", "tags": []interface{}{"lifecycle"}, "examples": []interface{}{"test"},
				"input_modes": []interface{}{"text"}, "output_modes": []interface{}{"text"},
				"security": []interface{}{map[string]interface{}{"oauth2": []interface{}{"read", "read"}}},
			}},
			"signatures": []interface{}{
				map[string]interface{}{"protected": "acceptance-protected", "signature": "acceptance-signature", "header": `{"duplicate":0,"exact":9007199254740993}`},
				map[string]interface{}{"protected": "acceptance-protected", "signature": "acceptance-signature", "header": `{"duplicate":1,"exact":9007199254740993}`},
			},
		},
		"object_permission": map[string]interface{}{
			"mcp_servers": []interface{}{"acceptance-server"}, "mcp_access_groups": []interface{}{"acceptance-group"},
			"mcp_tool_permissions": map[string]interface{}{"acceptance-server": ` [ "acceptance-tool" ] `},
			"models":               []interface{}{"openai/gpt-4o-mini"}, "agents": []interface{}{"acceptance-agent"},
		},
	}
}

func agentClearOverlayClearedConfig() map[string]interface{} {
	return map[string]interface{}{
		"agent_name":     "smoke-agent-lifecycle",
		"litellm_params": map[string]interface{}{"model": "openai/gpt-4o-mini"},
		"static_headers": map[string]interface{}{}, "extra_headers": []interface{}{},
		"agent_card": map[string]interface{}{
			"name": "Smoke Agent Lifecycle", "url": "https://agent.example.com/lifecycle", "version": "1.0.0", "protocol_version": "1.0",
			"default_input_modes": []interface{}{"text"}, "default_output_modes": []interface{}{"text"}, "supports_authenticated_extended_card": false,
			"capabilities": map[string]interface{}{"streaming": false, "push_notifications": false, "state_transition_history": false},
			"provider":     map[string]interface{}{"url": "https://agent.example.com"},
			"skills": []interface{}{map[string]interface{}{
				"id": "acceptance", "name": "Acceptance", "tags": []interface{}{}, "examples": []interface{}{},
				"input_modes": []interface{}{}, "output_modes": []interface{}{}, "security": []interface{}{},
			}},
			"signatures": []interface{}{},
		},
		"object_permission": map[string]interface{}{
			"mcp_servers": []interface{}{}, "mcp_access_groups": []interface{}{}, "mcp_tool_permissions": map[string]interface{}{},
			"models": []interface{}{}, "agents": []interface{}{},
		},
	}
}

func agentProtocolReplaceMany(t *testing.T, schema *tfprotov6.Schema, current *tfprotov6.DynamicValue, replacements map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	value, err := current.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	attributes := map[string]tftypes.Value{}
	if err := value.As(&attributes); err != nil {
		t.Fatal(err)
	}
	attributeTypes := schema.ValueType().(tftypes.Object).AttributeTypes
	for name, replacement := range replacements {
		attributes[name] = agentProtocolValue(t, attributeTypes[name], replacement)
	}
	return accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), attributes))
}

type agentClearProtocolFixture struct {
	ctx            context.Context
	api            *agentClearOverlayProtocolAPI
	server         tfprotov6.ProviderServer
	schema         *tfprotov6.Schema
	createdState   *tfprotov6.DynamicValue
	createdPrivate []byte
}

func newAgentClearProtocolFixture(t *testing.T) agentClearProtocolFixture {
	t.Helper()
	ctx := context.Background()
	api := &agentClearOverlayProtocolAPI{}
	httpServer := httptest.NewServer(http.HandlerFunc(api.handler))
	t.Cleanup(httpServer.Close)
	api.serverURL = httpServer.URL
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, httpServer.URL)
	schema := schemas.ResourceSchemas["litellm_agent"]
	setValues := agentClearOverlaySetConfig()
	config := agentProtocolDynamicValue(t, schema, setValues)
	proposedValues := make(map[string]interface{}, len(setValues)+6)
	for key, value := range setValues {
		proposedValues[key] = value
	}
	proposedValues["id"] = tftypes.UnknownValue
	proposedValues["litellm_params_json"] = tftypes.UnknownValue
	for _, field := range []string{"created_at", "updated_at", "created_by", "updated_by"} {
		proposedValues[field] = tftypes.UnknownValue
	}
	proposed := agentProtocolDynamicValue(t, schema, proposedValues)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: nullState, ProposedNewState: proposed,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, nullState, planned) != organizationProjectProtocolActionCreate {
		t.Fatalf("set create plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) || api.posts.Load() != 1 || api.patches.Load() != 0 || api.badMutation.Load() {
		t.Fatalf("set create: err=%v diagnostics=%s posts=%d patches=%d bad=%t", err, agentProtocolDiagnosticsText(created.Diagnostics), api.posts.Load(), api.patches.Load(), api.badMutation.Load())
	}
	committed, decodeErr := decodeAgentFieldSet(protocolPrivateValue(t, created.Private, agentImportedFieldsPrivateKey))
	if decodeErr != nil || len(committed) != 0 || string(protocolPrivateValue(t, created.Private, agentOwnershipInitializedPrivateKey)) != "true" || protocolPrivateHasKey(t, created.Private, agentOwnershipPendingPrivateKey) {
		t.Fatalf("configured create ownership was not authoritative: committed=%#v err=%v private=%s", committed, decodeErr, created.Private)
	}
	return agentClearProtocolFixture{ctx: ctx, api: api, server: protocolServer, schema: schema, createdState: created.NewState, createdPrivate: created.Private}
}

func planAgentClearProtocolFixture(t *testing.T, fixture agentClearProtocolFixture) (*tfprotov6.DynamicValue, *tfprotov6.PlanResourceChangeResponse) {
	t.Helper()
	clearValues := agentClearOverlayClearedConfig()
	config := agentProtocolDynamicValue(t, fixture.schema, clearValues)
	proposed := agentProtocolReplaceMany(t, fixture.schema, fixture.createdState, map[string]interface{}{
		"tpm_limit": nil, "rpm_limit": nil, "session_tpm_limit": nil, "session_rpm_limit": nil,
		"litellm_params": clearValues["litellm_params"], "static_headers": clearValues["static_headers"], "extra_headers": clearValues["extra_headers"],
		"agent_card": clearValues["agent_card"], "object_permission": clearValues["object_permission"],
	})
	planned, err := fixture.server.PlanResourceChange(fixture.ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: fixture.createdState, ProposedNewState: proposed, PriorPrivate: fixture.createdPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || organizationProjectProtocolPlannedAction(t, fixture.schema, fixture.createdState, planned) != organizationProjectProtocolActionUpdate {
		t.Fatalf("set-to-cleared plan: err=%v diagnostics=%s action=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics), organizationProjectProtocolPlannedAction(t, fixture.schema, fixture.createdState, planned))
	}
	return config, planned
}

func assertAgentClearProtocolClearedState(t *testing.T, schema *tfprotov6.Schema, state *tfprotov6.DynamicValue) {
	t.Helper()
	attributes := protocolAttributeMap(t, schema, state)
	for _, field := range []string{"tpm_limit", "rpm_limit", "session_tpm_limit", "session_rpm_limit"} {
		if !attributes[field].IsNull() {
			t.Fatalf("%s did not clear", field)
		}
	}
	assertEmpty := func(field string, value tftypes.Value) {
		t.Helper()
		if value.IsNull() || value.IsKnown() == false {
			t.Fatalf("%s clear is not a known empty collection", field)
		}
		switch value.Type().(type) {
		case tftypes.Map:
			var items map[string]tftypes.Value
			if err := value.As(&items); err != nil || len(items) != 0 {
				t.Fatalf("%s clear = %#v, err=%v", field, items, err)
			}
		default:
			var items []tftypes.Value
			if err := value.As(&items); err != nil || len(items) != 0 {
				t.Fatalf("%s clear = %#v, err=%v", field, items, err)
			}
		}
	}
	var params map[string]tftypes.Value
	if err := attributes["litellm_params"].As(&params); err != nil || len(params) != 1 {
		t.Fatalf("litellm_params replacement = %#v, err=%v", params, err)
	}
	var model string
	if err := params["model"].As(&model); err != nil || model != "openai/gpt-4o-mini" {
		t.Fatalf("litellm_params.model = %q, err=%v", model, err)
	}
	assertEmpty("static_headers", attributes["static_headers"])
	assertEmpty("extra_headers", attributes["extra_headers"])

	var card map[string]tftypes.Value
	if err := attributes["agent_card"].As(&card); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"description", "preferred_transport", "icon_url", "documentation_url"} {
		if !card[field].IsNull() {
			t.Fatalf("agent_card.%s did not clear", field)
		}
	}
	var authenticated bool
	if err := card["supports_authenticated_extended_card"].As(&authenticated); err != nil || authenticated {
		t.Fatalf("authenticated extended card clear = %t, err=%v", authenticated, err)
	}
	var capabilities map[string]tftypes.Value
	if err := card["capabilities"].As(&capabilities); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"streaming", "push_notifications", "state_transition_history"} {
		var enabled bool
		if err := capabilities[field].As(&enabled); err != nil || enabled {
			t.Fatalf("capability %s clear = %t, err=%v", field, enabled, err)
		}
	}
	var provider map[string]tftypes.Value
	if err := card["provider"].As(&provider); err != nil || !provider["organization"].IsNull() {
		t.Fatalf("provider organization clear = %#v, err=%v", provider, err)
	}
	var providerURL string
	if err := provider["url"].As(&providerURL); err != nil || providerURL != "https://agent.example.com" {
		t.Fatalf("provider URL = %q, err=%v", providerURL, err)
	}
	var skills []tftypes.Value
	if err := card["skills"].As(&skills); err != nil || len(skills) != 1 {
		t.Fatalf("skills = %#v, err=%v", skills, err)
	}
	var skill map[string]tftypes.Value
	if err := skills[0].As(&skill); err != nil || !skill["description"].IsNull() {
		t.Fatalf("skill scalar clear = %#v, err=%v", skill, err)
	}
	for _, field := range []string{"tags", "examples", "input_modes", "output_modes", "security"} {
		assertEmpty("agent_card.skills.acceptance."+field, skill[field])
	}
	assertEmpty("agent_card.signatures", card["signatures"])

	var permission map[string]tftypes.Value
	if err := attributes["object_permission"].As(&permission); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"mcp_servers", "mcp_access_groups", "mcp_tool_permissions", "models", "agents"} {
		assertEmpty("object_permission."+field, permission[field])
	}
}

func TestAgentProtocolMergedLifecycleSetToClearCanonicalNoDrift(t *testing.T) {
	fixture := newAgentClearProtocolFixture(t)
	config, planned := planAgentClearProtocolFixture(t, fixture)
	cleared, err := fixture.server.ApplyResourceChange(fixture.ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: fixture.createdState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(cleared.Diagnostics) || fixture.api.patches.Load() != 1 || fixture.api.badMutation.Load() {
		t.Fatalf("set-to-cleared apply: err=%v diagnostics=%s patches=%d bad=%t", err, agentProtocolDiagnosticsText(cleared.Diagnostics), fixture.api.patches.Load(), fixture.api.badMutation.Load())
	}
	committed, decodeErr := decodeAgentFieldSet(protocolPrivateValue(t, cleared.Private, agentImportedFieldsPrivateKey))
	if decodeErr != nil || len(committed) != 0 || string(protocolPrivateValue(t, cleared.Private, agentOwnershipInitializedPrivateKey)) != "true" || protocolPrivateHasKey(t, cleared.Private, agentOwnershipPendingPrivateKey) {
		t.Fatalf("clear ownership was not promoted: committed=%#v err=%v private=%s", committed, decodeErr, cleared.Private)
	}
	assertAgentClearProtocolClearedState(t, fixture.schema, cleared.NewState)

	state, private := cleared.NewState, cleared.Private
	for readNumber := 1; readNumber <= 2; readNumber++ {
		refreshed, readErr := fixture.server.ReadResource(fixture.ctx, &tfprotov6.ReadResourceRequest{
			TypeName: "litellm_agent", CurrentState: state, Private: private,
		})
		if readErr != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
			t.Fatalf("stable read %d: err=%v diagnostics=%s", readNumber, readErr, agentProtocolDiagnosticsText(refreshed.Diagnostics))
		}
		assertAgentProtocolStateUnchanged(t, fixture.schema, state, refreshed.NewState)
		assertAgentProtocolPrivateUnchanged(t, private, refreshed.Private)
		state, private = refreshed.NewState, refreshed.Private
	}
	noOp, planErr := fixture.server.PlanResourceChange(fixture.ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: state, ProposedNewState: state, PriorPrivate: private,
	})
	if planErr != nil || accessGroupProtocolDiagnosticsHaveError(noOp.Diagnostics) || organizationProjectProtocolPlannedAction(t, fixture.schema, state, noOp) != organizationProjectProtocolActionNoOp || fixture.api.patches.Load() != 1 {
		t.Fatalf("post-clear protocol plan: err=%v diagnostics=%s action=%s patches=%d", planErr, agentProtocolDiagnosticsText(noOp.Diagnostics), organizationProjectProtocolPlannedAction(t, fixture.schema, state, noOp), fixture.api.patches.Load())
	}
}

func TestAgentProtocolMergedLifecycleAdversarialPreflightRetainsOwnedState(t *testing.T) {
	for _, test := range []struct {
		name        string
		adversarial string
	}{
		{name: "unknown top-level card key", adversarial: "top-level"},
		{name: "unknown nested capability key", adversarial: "nested-capability"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAgentClearProtocolFixture(t)
			fixture.api.setAdversarial(test.adversarial)
			config, planned := planAgentClearProtocolFixture(t, fixture)
			assertAgentProtocolPrivateUnchanged(t, fixture.createdPrivate, planned.PlannedPrivate)
			beforeApply := fixture.api.requests.Load()
			applied, err := fixture.server.ApplyResourceChange(fixture.ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "litellm_agent", Config: config, PriorState: fixture.createdState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("adversarial clear apply: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
			}
			if fixture.api.requests.Load() <= beforeApply || fixture.api.patches.Load() != 0 || fixture.api.badMutation.Load() {
				t.Fatalf("preflight requests=%d/%d patches=%d bad=%t", fixture.api.requests.Load(), beforeApply, fixture.api.patches.Load(), fixture.api.badMutation.Load())
			}
			diagnostic := agentProtocolDiagnosticsText(applied.Diagnostics)
			for _, protected := range []string{"x-adversarial", "must", "not mutate", fixture.api.serverURL, "smoke-agent-lifecycle"} {
				if strings.Contains(diagnostic, protected) {
					t.Fatal("content-bearing preflight diagnostic")
				}
			}
			assertAgentProtocolStateUnchanged(t, fixture.schema, fixture.createdState, applied.NewState)
			assertAgentProtocolPrivateUnchanged(t, fixture.createdPrivate, applied.Private)
		})
	}
}

func TestAgentClearProtocolMockRejectsPermissiveEvidence(t *testing.T) {
	serverURL := "http://127.0.0.1:41998"
	expectedPatch := agentClearProtocolPatchRequest(serverURL)
	setCard := agentClearProtocolSetCard(serverURL)
	for _, test := range []struct {
		name   string
		mutate func(map[string]interface{})
	}{
		{name: "capability false sent", mutate: func(request map[string]interface{}) {
			request["agent_card_params"].(map[string]interface{})["capabilities"] = map[string]interface{}{"streaming": false}
		}},
		{name: "required top-level clear omitted", mutate: func(request map[string]interface{}) { delete(request, "tpm_limit") }},
		{name: "required nested clear omitted", mutate: func(request map[string]interface{}) {
			delete(request["agent_card_params"].(map[string]interface{})["skills"].([]interface{})[0].(map[string]interface{}), "security")
		}},
		{name: "object permissions preserved", mutate: func(request map[string]interface{}) {
			request["object_permission"].(map[string]interface{})["mcp_servers"] = []interface{}{"acceptance-server"}
		}},
		{name: "duplicate signatures preserved", mutate: func(request map[string]interface{}) {
			request["agent_card_params"].(map[string]interface{})["signatures"] = cloneAgentWireValue(setCard["signatures"])
		}},
		{name: "skill security preserved", mutate: func(request map[string]interface{}) {
			request["agent_card_params"].(map[string]interface{})["skills"].([]interface{})[0].(map[string]interface{})["security"] = []interface{}{map[string]interface{}{"oauth2": []interface{}{"read", "read"}}}
		}},
		{name: "unexpected field accepted", mutate: func(request map[string]interface{}) { request["unexpected"] = true }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := cloneAgentWireObject(expectedPatch)
			test.mutate(candidate)
			if err := agentClearProtocolExactShape(candidate, expectedPatch); err == nil {
				t.Fatal("permissive mutation evidence passed the exact mock assertion")
			}
		})
	}

	// A request echo has neither v1.98's empty capabilities parent nor its
	// canonical object-permission and response fields. It cannot be used as
	// confirmation evidence by this mock.
	echo := cloneAgentWireObject(expectedPatch)
	echo["agent_id"] = agentClearProtocolID
	if err := agentClearProtocolCanonicalResponseShape(echo, agentClearProtocolResponse(serverURL, true, "")); err == nil {
		t.Fatal("request echo passed as canonical v1.98 confirmation evidence")
	}
}
