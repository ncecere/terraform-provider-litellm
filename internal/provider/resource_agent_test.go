package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestBuildAgentRequest_Minimal(t *testing.T) {
	t.Parallel()

	r := &AgentResource{}
	data := &AgentResourceModel{
		AgentName: types.StringValue("test-agent"),
		AgentCard: &AgentCardModel{
			Name: types.StringValue("Test Agent"),
			URL:  types.StringValue("https://agent.example.com"),
		},
	}

	req := r.buildAgentRequest(data)

	if req["agent_name"] != "test-agent" {
		t.Errorf("expected agent_name 'test-agent', got %v", req["agent_name"])
	}

	card, ok := req["agent_card_params"].(map[string]interface{})
	if !ok {
		t.Fatal("expected agent_card_params to be a map")
	}
	if card["name"] != "Test Agent" {
		t.Errorf("expected card name 'Test Agent', got %v", card["name"])
	}
	if card["url"] != "https://agent.example.com" {
		t.Errorf("expected card url 'https://agent.example.com', got %v", card["url"])
	}

	// Should not have optional fields
	if _, exists := req["litellm_params"]; exists {
		t.Error("litellm_params should not be present when not configured")
	}
	if _, exists := req["tpm_limit"]; exists {
		t.Error("tpm_limit should not be present when not configured")
	}
}

func TestBuildAgentRequest_Full(t *testing.T) {
	t.Parallel()

	r := &AgentResource{}

	skills := []AgentSkillModel{
		{
			ID:          types.StringValue("skill-1"),
			Name:        types.StringValue("Code Review"),
			Description: types.StringValue("Reviews code for quality"),
			Tags:        stringListValue("go", "python"),
			Examples:    types.ListNull(types.StringType),
			InputModes:  types.ListNull(types.StringType),
			OutputModes: types.ListNull(types.StringType),
		},
	}

	data := &AgentResourceModel{
		AgentName: types.StringValue("full-agent"),
		AgentCard: &AgentCardModel{
			Name:            types.StringValue("Full Agent"),
			Description:     types.StringValue("A fully configured agent"),
			URL:             types.StringValue("https://agent.example.com/a2a"),
			Version:         types.StringValue("1.0.0"),
			ProtocolVersion: types.StringValue("0.2.6"),
			DefaultInputModes:  stringListValue("application/json"),
			DefaultOutputModes: stringListValue("application/json", "text/plain"),
			PreferredTransport: types.StringValue("httpsse"),
			IconURL:            types.StringValue("https://example.com/icon.png"),
			DocumentationURL:   types.StringValue("https://docs.example.com"),
			Capabilities: &AgentCapabilitiesModel{
				Streaming:              types.BoolValue(true),
				PushNotifications:      types.BoolValue(false),
				StateTransitionHistory: types.BoolValue(true),
			},
			Provider: &AgentProviderModel{
				Organization: types.StringValue("Acme Corp"),
				URL:          types.StringValue("https://acme.example.com"),
			},
			Skills: skills,
		},
		LiteLLMParams: stringMapValue(map[string]string{
			"model": "gpt-4o",
		}),
		ObjectPermission: &AgentObjectPermissionModel{
			Models:             stringListValue("gpt-4o", "gpt-4o-mini"),
			MCPServers:         stringListValue("mcp-server-1"),
			MCPAccessGroups:    types.ListNull(types.StringType),
			MCPToolPermissions: types.MapNull(types.StringType),
			Agents:             types.ListNull(types.StringType),
		},
		TPMLimit:        types.Int64Value(10000),
		RPMLimit:        types.Int64Value(100),
		SessionTPMLimit: types.Int64Value(5000),
		SessionRPMLimit: types.Int64Value(50),
		StaticHeaders: stringMapValue(map[string]string{
			"X-Custom": "value",
		}),
		ExtraHeaders: stringListValue("Authorization"),
	}

	req := r.buildAgentRequest(data)

	if req["agent_name"] != "full-agent" {
		t.Errorf("expected agent_name 'full-agent', got %v", req["agent_name"])
	}
	if req["tpm_limit"] != int64(10000) {
		t.Errorf("expected tpm_limit 10000, got %v", req["tpm_limit"])
	}
	if req["rpm_limit"] != int64(100) {
		t.Errorf("expected rpm_limit 100, got %v", req["rpm_limit"])
	}

	card, ok := req["agent_card_params"].(map[string]interface{})
	if !ok {
		t.Fatal("expected agent_card_params map")
	}
	if card["protocolVersion"] != "0.2.6" {
		t.Errorf("expected protocolVersion '0.2.6', got %v", card["protocolVersion"])
	}
	if card["preferredTransport"] != "httpsse" {
		t.Errorf("expected preferredTransport 'httpsse', got %v", card["preferredTransport"])
	}

	caps, ok := card["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatal("expected capabilities map")
	}
	if caps["streaming"] != true {
		t.Errorf("expected streaming true, got %v", caps["streaming"])
	}

	prov, ok := card["provider"].(map[string]interface{})
	if !ok {
		t.Fatal("expected provider map")
	}
	if prov["organization"] != "Acme Corp" {
		t.Errorf("expected organization 'Acme Corp', got %v", prov["organization"])
	}

	skillsReq, ok := card["skills"].([]map[string]interface{})
	if !ok || len(skillsReq) != 1 {
		t.Fatalf("expected 1 skill, got %v", card["skills"])
	}
	if skillsReq[0]["id"] != "skill-1" {
		t.Errorf("expected skill id 'skill-1', got %v", skillsReq[0]["id"])
	}
	if skillsReq[0]["name"] != "Code Review" {
		t.Errorf("expected skill name 'Code Review', got %v", skillsReq[0]["name"])
	}

	perm, ok := req["object_permission"].(map[string]interface{})
	if !ok {
		t.Fatal("expected object_permission map")
	}
	models, ok := perm["models"].([]string)
	if !ok || len(models) != 2 {
		t.Fatalf("expected 2 models, got %v", perm["models"])
	}

	headers, ok := req["static_headers"].(map[string]interface{})
	if !ok {
		t.Fatal("expected static_headers map")
	}
	if headers["X-Custom"] != "value" {
		t.Errorf("expected X-Custom header 'value', got %v", headers["X-Custom"])
	}
}

func TestReadAgent_PopulatesState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"agent_id":   "agent-abc-123",
			"agent_name": "my-agent",
			"agent_card_params": map[string]interface{}{
				"name":            "My Agent",
				"description":     "A helpful agent",
				"url":             "https://agent.example.com",
				"version":         "1.0.0",
				"protocolVersion": "0.2.6",
				"capabilities": map[string]interface{}{
					"streaming":         true,
					"pushNotifications": false,
				},
				"provider": map[string]interface{}{
					"organization": "TestOrg",
					"url":          "https://testorg.example.com",
				},
				"skills": []interface{}{
					map[string]interface{}{
						"id":          "s1",
						"name":        "Summarize",
						"description": "Summarizes text",
						"tags":        []interface{}{"nlp"},
					},
				},
				"defaultInputModes":  []interface{}{"application/json"},
				"defaultOutputModes": []interface{}{"text/plain"},
			},
			"litellm_params": map[string]interface{}{
				"model": "gpt-4o",
			},
			"object_permission": map[string]interface{}{
				"models": []interface{}{"gpt-4o"},
			},
			"tpm_limit":         10000.0,
			"rpm_limit":         100.0,
			"session_tpm_limit": 5000.0,
			"session_rpm_limit": 50.0,
			"static_headers": map[string]interface{}{
				"X-Test": "value",
			},
			"extra_headers": []interface{}{"Authorization"},
			"created_at":    "2026-03-20T10:00:00Z",
			"updated_at":    "2026-03-20T11:00:00Z",
			"created_by":    "admin",
			"updated_by":    "admin",
		})
	}))
	defer server.Close()

	r := &AgentResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := AgentResourceModel{
		ID:            types.StringValue("agent-abc-123"),
		LiteLLMParams: types.MapUnknown(types.StringType),
		StaticHeaders: types.MapUnknown(types.StringType),
		ExtraHeaders:  types.ListUnknown(types.StringType),
	}

	if err := r.readAgent(context.Background(), &data); err != nil {
		t.Fatalf("readAgent returned error: %v", err)
	}

	// Top-level
	if data.ID.ValueString() != "agent-abc-123" {
		t.Errorf("expected ID 'agent-abc-123', got %q", data.ID.ValueString())
	}
	if data.AgentName.ValueString() != "my-agent" {
		t.Errorf("expected agent_name 'my-agent', got %q", data.AgentName.ValueString())
	}

	// Rate limits
	if data.TPMLimit.ValueInt64() != 10000 {
		t.Errorf("expected tpm_limit 10000, got %d", data.TPMLimit.ValueInt64())
	}
	if data.RPMLimit.ValueInt64() != 100 {
		t.Errorf("expected rpm_limit 100, got %d", data.RPMLimit.ValueInt64())
	}
	if data.SessionTPMLimit.ValueInt64() != 5000 {
		t.Errorf("expected session_tpm_limit 5000, got %d", data.SessionTPMLimit.ValueInt64())
	}
	if data.SessionRPMLimit.ValueInt64() != 50 {
		t.Errorf("expected session_rpm_limit 50, got %d", data.SessionRPMLimit.ValueInt64())
	}

	// Computed
	if data.CreatedAt.ValueString() != "2026-03-20T10:00:00Z" {
		t.Errorf("expected created_at, got %q", data.CreatedAt.ValueString())
	}
	if data.CreatedBy.ValueString() != "admin" {
		t.Errorf("expected created_by 'admin', got %q", data.CreatedBy.ValueString())
	}

	// Agent card
	if data.AgentCard == nil {
		t.Fatal("expected agent_card to be populated")
	}
	if data.AgentCard.Name.ValueString() != "My Agent" {
		t.Errorf("expected card name 'My Agent', got %q", data.AgentCard.Name.ValueString())
	}
	if data.AgentCard.Description.ValueString() != "A helpful agent" {
		t.Errorf("expected card description 'A helpful agent', got %q", data.AgentCard.Description.ValueString())
	}
	if data.AgentCard.ProtocolVersion.ValueString() != "0.2.6" {
		t.Errorf("expected protocolVersion '0.2.6', got %q", data.AgentCard.ProtocolVersion.ValueString())
	}

	// Capabilities
	if data.AgentCard.Capabilities == nil {
		t.Fatal("expected capabilities to be populated")
	}
	if data.AgentCard.Capabilities.Streaming.ValueBool() != true {
		t.Error("expected streaming true")
	}
	if data.AgentCard.Capabilities.PushNotifications.ValueBool() != false {
		t.Error("expected pushNotifications false")
	}

	// Provider
	if data.AgentCard.Provider == nil {
		t.Fatal("expected provider to be populated")
	}
	if data.AgentCard.Provider.Organization.ValueString() != "TestOrg" {
		t.Errorf("expected organization 'TestOrg', got %q", data.AgentCard.Provider.Organization.ValueString())
	}

	// Skills
	if len(data.AgentCard.Skills) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(data.AgentCard.Skills))
	}
	if data.AgentCard.Skills[0].ID.ValueString() != "s1" {
		t.Errorf("expected skill id 's1', got %q", data.AgentCard.Skills[0].ID.ValueString())
	}
	if data.AgentCard.Skills[0].Name.ValueString() != "Summarize" {
		t.Errorf("expected skill name 'Summarize', got %q", data.AgentCard.Skills[0].Name.ValueString())
	}

	// Object permission
	if data.ObjectPermission == nil {
		t.Fatal("expected object_permission to be populated")
	}
	models := data.ObjectPermission.Models.Elements()
	if len(models) != 1 {
		t.Fatalf("expected 1 model in permissions, got %d", len(models))
	}

	// LiteLLM params
	if data.LiteLLMParams.IsNull() || data.LiteLLMParams.IsUnknown() {
		t.Fatal("expected litellm_params to be populated")
	}

	// Static headers
	if data.StaticHeaders.IsNull() || data.StaticHeaders.IsUnknown() {
		t.Fatal("expected static_headers to be populated")
	}

	// Extra headers
	if data.ExtraHeaders.IsNull() || data.ExtraHeaders.IsUnknown() {
		t.Fatal("expected extra_headers to be populated")
	}
}

func TestReadAgent_ResolvesUnknownToNull(t *testing.T) {
	t.Parallel()

	// API returns minimal response — Unknown optional fields should resolve to null
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"agent_id":   "agent-minimal",
			"agent_name": "minimal-agent",
			"agent_card_params": map[string]interface{}{
				"name": "Minimal",
				"url":  "https://example.com",
			},
		})
	}))
	defer server.Close()

	r := &AgentResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := AgentResourceModel{
		ID:            types.StringValue("agent-minimal"),
		LiteLLMParams: types.MapUnknown(types.StringType),
		StaticHeaders: types.MapUnknown(types.StringType),
		ExtraHeaders:  types.ListUnknown(types.StringType),
	}

	if err := r.readAgent(context.Background(), &data); err != nil {
		t.Fatalf("readAgent returned error: %v", err)
	}

	if data.LiteLLMParams.IsUnknown() {
		t.Error("litellm_params should not be Unknown after read")
	}
	if data.StaticHeaders.IsUnknown() {
		t.Error("static_headers should not be Unknown after read")
	}
	if data.ExtraHeaders.IsUnknown() {
		t.Error("extra_headers should not be Unknown after read")
	}
}

// --- Test helpers ---

func stringListValue(vals ...string) types.List {
	elems := make([]attr.Value, len(vals))
	for i, v := range vals {
		elems[i] = types.StringValue(v)
	}
	l, _ := types.ListValue(types.StringType, elems)
	return l
}

func stringMapValue(m map[string]string) types.Map {
	elems := make(map[string]attr.Value, len(m))
	for k, v := range m {
		elems[k] = types.StringValue(v)
	}
	mv, _ := types.MapValue(types.StringType, elems)
	return mv
}
