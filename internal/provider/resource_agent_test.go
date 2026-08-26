package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	frameworkdatasource "github.com/hashicorp/terraform-plugin-framework/datasource"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAgentCredentialBearingMapsAreSensitive(t *testing.T) {
	t.Parallel()

	var resourceResponse frameworkresource.SchemaResponse
	(&AgentResource{}).Schema(context.Background(), frameworkresource.SchemaRequest{}, &resourceResponse)
	if resourceResponse.Diagnostics.HasError() {
		t.Fatalf("resource schema diagnostics: %v", resourceResponse.Diagnostics)
	}
	var dataSourceResponse frameworkdatasource.SchemaResponse
	(&AgentDataSource{}).Schema(context.Background(), frameworkdatasource.SchemaRequest{}, &dataSourceResponse)
	if dataSourceResponse.Diagnostics.HasError() {
		t.Fatalf("data source schema diagnostics: %v", dataSourceResponse.Diagnostics)
	}
	for _, name := range []string{"litellm_params", "static_headers"} {
		if !resourceResponse.Schema.Attributes[name].IsSensitive() {
			t.Errorf("resource %s must be sensitive", name)
		}
		if !dataSourceResponse.Schema.Attributes[name].IsSensitive() {
			t.Errorf("data source %s must be sensitive", name)
		}
	}
}

func TestBuildAgentRequest_BedrockAgentCore(t *testing.T) {
	t.Parallel()

	arn := "arn:aws:bedrock-agentcore:us-east-1:123456789012:runtime/example"
	data := &AgentResourceModel{
		AgentName: types.StringValue("agentcore-agent"),
		AgentCard: &AgentCardModel{
			Name:            types.StringValue("AgentCore Agent"),
			URL:             types.StringValue(""),
			ProtocolVersion: types.StringValue("1.0"),
			Capabilities: &AgentCapabilitiesModel{
				Streaming: types.BoolValue(true),
			},
		},
		LiteLLMParams: stringMapValue(map[string]string{
			"custom_llm_provider": "bedrock",
			"model":               "bedrock/agentcore/" + arn,
			"qualifier":           "PROD",
		}),
	}

	request, err := (&AgentResource{}).buildAgentRequest(data)
	if err != nil {
		t.Fatalf("build AgentCore request: %v", err)
	}
	card := request["agent_card_params"].(map[string]interface{})
	if card["url"] != "" || card["protocolVersion"] != "1.0" {
		t.Fatalf("AgentCore card = %#v, want empty URL and protocolVersion 1.0", card)
	}
	capabilities := card["capabilities"].(map[string]interface{})
	if capabilities["streaming"] != true {
		t.Fatalf("AgentCore capabilities = %#v", capabilities)
	}
	params := request["litellm_params"].(map[string]interface{})
	if params["custom_llm_provider"] != "bedrock" || params["model"] != "bedrock/agentcore/"+arn || params["qualifier"] != "PROD" {
		t.Fatalf("AgentCore litellm_params = %#v", params)
	}
	if _, present := request["agent_type"]; present {
		t.Fatalf("AgentCore must not send dashboard-only agent_type: %#v", request)
	}
}

func TestReconcileAgentStringMapOwnershipAndSecrets(t *testing.T) {
	t.Parallel()

	configured := stringMapValue(map[string]string{
		"model":   "bedrock/agentcore/runtime",
		"api_key": "secret-value",
	})
	observed := map[string]interface{}{
		"model":     "bedrock/agentcore/runtime",
		"api_key":   "litellm_enc::masked",
		"is_public": false,
	}
	reconciled, err := reconcileAgentStringMap(configured, observed, true)
	if err != nil {
		t.Fatalf("configured reconciliation: %v", err)
	}
	if !reconciled.Equal(configured) {
		t.Fatalf("configured keys or masked secret changed: %#v", reconciled.Elements())
	}
	if _, adopted := reconciled.Elements()["is_public"]; adopted {
		t.Fatalf("API-injected is_public was adopted into configured state: %#v", reconciled.Elements())
	}

	if _, err := reconcileAgentStringMap(types.MapUnknown(types.StringType), observed, true); err == nil {
		t.Fatal("unmanaged/imported masked API value must fail instead of entering state")
	}

	imported, err := reconcileAgentStringMap(types.MapUnknown(types.StringType), map[string]interface{}{
		"model":     "bedrock/agentcore/runtime",
		"is_public": false,
	}, true)
	if err != nil {
		t.Fatalf("unmasked import reconciliation: %v", err)
	}
	if _, adopted := imported.Elements()["is_public"]; adopted {
		t.Fatalf("imported map adopted synthetic is_public: %#v", imported.Elements())
	}
	if got := imported.Elements()["model"].(types.String).ValueString(); got != "bedrock/agentcore/runtime" {
		t.Fatalf("imported model = %q", got)
	}

	headers, err := reconcileAgentStringMap(types.MapUnknown(types.StringType), map[string]interface{}{
		"is_public": "legitimate-header-value",
	}, false)
	if err != nil {
		t.Fatalf("static header reconciliation: %v", err)
	}
	if got := headers.Elements()["is_public"].(types.String).ValueString(); got != "legitimate-header-value" {
		t.Fatalf("static header is_public = %q", got)
	}
}

func TestHydrateUnmanagedAgentUpdateFieldsPreservesRemoteSecrets(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"agent_id": "agent-update",
			"litellm_params": map[string]interface{}{
				"model":     "bedrock/agentcore/runtime",
				"is_public": false,
			},
			"static_headers": map[string]interface{}{
				"Authorization": "Bearer preserved",
				"is_public":     "legitimate-header-value",
			},
			"extra_headers": []interface{}{"X-Request-ID"},
		})
	}))
	defer server.Close()

	resource := &AgentResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	data := AgentResourceModel{
		ID:            types.StringValue("agent-update"),
		AgentName:     types.StringValue("agent"),
		LiteLLMParams: types.MapNull(types.StringType),
		StaticHeaders: types.MapNull(types.StringType),
		ExtraHeaders:  types.ListNull(types.StringType),
	}
	if err := resource.hydrateUnmanagedAgentUpdateFields(context.Background(), &data); err != nil {
		t.Fatalf("hydrate unmanaged fields: %v", err)
	}
	if _, present := data.LiteLLMParams.Elements()["is_public"]; present {
		t.Fatalf("synthetic litellm_params.is_public was retained: %#v", data.LiteLLMParams.Elements())
	}
	if got := data.StaticHeaders.Elements()["is_public"].(types.String).ValueString(); got != "legitimate-header-value" {
		t.Fatalf("legitimate static header is_public = %q", got)
	}
	request, err := resource.buildAgentRequest(&data)
	if err != nil {
		t.Fatalf("build hydrated request: %v", err)
	}
	params := request["litellm_params"].(map[string]interface{})
	if _, present := params["is_public"]; present {
		t.Fatalf("update request included synthetic is_public: %#v", params)
	}
	headers := request["static_headers"].(map[string]interface{})
	if headers["Authorization"] != "Bearer preserved" || headers["is_public"] != "legitimate-header-value" {
		t.Fatalf("update request lost static headers: %#v", headers)
	}
}

func TestHydrateMaskedPlannedAgentValuesPreservesRealChanges(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"agent_id": "agent-masked-plan",
			"litellm_params": map[string]interface{}{
				"model":   "remote-old-model",
				"api_key": "real-api-key-value",
			},
			"static_headers": map[string]interface{}{},
			"extra_headers":  []interface{}{},
		})
	}))
	defer server.Close()

	resource := &AgentResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	data := AgentResourceModel{
		ID:        types.StringValue("agent-masked-plan"),
		AgentName: types.StringValue("agent"),
		LiteLLMParams: stringMapValue(map[string]string{
			"model":   "planned-new-model",
			"api_key": "ab****yz",
		}),
		StaticHeaders: types.MapNull(types.StringType),
		ExtraHeaders:  types.ListNull(types.StringType),
	}
	if err := resource.hydrateUnmanagedAgentUpdateFields(context.Background(), &data); err != nil {
		t.Fatalf("hydrate masked planned value: %v", err)
	}
	params := data.LiteLLMParams.Elements()
	if got := params["api_key"].(types.String).ValueString(); got != "real-api-key-value" {
		t.Fatalf("hydrated api_key = %q", got)
	}
	if got := params["model"].(types.String).ValueString(); got != "planned-new-model" {
		t.Fatalf("genuine planned model update was overwritten: %q", got)
	}
	request, err := resource.buildAgentRequest(&data)
	if err != nil {
		t.Fatalf("build masked request: %v", err)
	}
	requestParams := request["litellm_params"].(map[string]interface{})
	if requestParams["api_key"] != "real-api-key-value" || requestParams["model"] != "planned-new-model" {
		t.Fatalf("update request params = %#v", requestParams)
	}
}

func TestReadAgentRejectsMaskedImportState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"agent_id":   "agent-masked-import",
			"agent_name": "masked",
			"litellm_params": map[string]interface{}{
				"model":   "bedrock/agentcore/runtime",
				"api_key": "ab****yz",
			},
		})
	}))
	defer server.Close()

	resource := &AgentResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	data := AgentResourceModel{
		ID:            types.StringValue("agent-masked-import"),
		LiteLLMParams: types.MapUnknown(types.StringType),
	}
	err := resource.readAgent(context.Background(), &data)
	if err == nil || !strings.Contains(err.Error(), "PROXY_ADMIN") {
		t.Fatalf("masked import read error = %v, want PROXY_ADMIN guidance", err)
	}
	if !data.LiteLLMParams.IsUnknown() {
		t.Fatalf("masked import value entered state: %#v", data.LiteLLMParams)
	}
}

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

	req, err := r.buildAgentRequest(data)
	if err != nil {
		t.Fatalf("build minimal request: %v", err)
	}

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
			Name:                              types.StringValue("Full Agent"),
			Description:                       types.StringValue("A fully configured agent"),
			URL:                               types.StringValue("https://agent.example.com/a2a"),
			Version:                           types.StringValue("1.0.0"),
			ProtocolVersion:                   types.StringValue("0.3"),
			DefaultInputModes:                 stringListValue("application/json"),
			DefaultOutputModes:                stringListValue("application/json", "text/plain"),
			PreferredTransport:                types.StringValue("httpsse"),
			IconURL:                           types.StringValue("https://example.com/icon.png"),
			DocumentationURL:                  types.StringValue("https://docs.example.com"),
			SupportsAuthenticatedExtendedCard: types.BoolValue(true),
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

	req, err := r.buildAgentRequest(data)
	if err != nil {
		t.Fatalf("build full request: %v", err)
	}

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
	if card["protocolVersion"] != "0.3" {
		t.Errorf("expected protocolVersion '0.3', got %v", card["protocolVersion"])
	}
	if card["preferredTransport"] != "httpsse" {
		t.Errorf("expected preferredTransport 'httpsse', got %v", card["preferredTransport"])
	}
	if card["supportsAuthenticatedExtendedCard"] != true {
		t.Errorf("expected supportsAuthenticatedExtendedCard true, got %v", card["supportsAuthenticatedExtendedCard"])
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
				"name":                              "My Agent",
				"description":                       "A helpful agent",
				"url":                               "https://agent.example.com",
				"version":                           "1.0.0",
				"protocolVersion":                   "1.0",
				"supportsAuthenticatedExtendedCard": true,
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

	if err := r.readAgentWithNumericOwnership(context.Background(), &data, true); err != nil {
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
	if data.AgentCard.ProtocolVersion.ValueString() != "1.0" {
		t.Errorf("expected protocolVersion '1.0', got %q", data.AgentCard.ProtocolVersion.ValueString())
	}
	if !data.AgentCard.SupportsAuthenticatedExtendedCard.ValueBool() {
		t.Error("expected supportsAuthenticatedExtendedCard true")
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

func TestReadAgentCardMissingManagedCapabilitiesBecomeFalse(t *testing.T) {
	t.Parallel()

	resource := &AgentResource{}
	data := AgentResourceModel{AgentCard: &AgentCardModel{
		SupportsAuthenticatedExtendedCard: types.BoolValue(true),
		Capabilities: &AgentCapabilitiesModel{
			Streaming:              types.BoolValue(true),
			PushNotifications:      types.BoolValue(true),
			StateTransitionHistory: types.BoolValue(true),
		},
	}}

	resource.readAgentCard(map[string]interface{}{
		"capabilities": map[string]interface{}{"streaming": true},
	}, &data)

	if !data.AgentCard.Capabilities.Streaming.ValueBool() {
		t.Error("expected returned streaming capability to remain true")
	}
	if data.AgentCard.Capabilities.PushNotifications.ValueBool() {
		t.Error("expected omitted pushNotifications capability to become false")
	}
	if data.AgentCard.Capabilities.StateTransitionHistory.ValueBool() {
		t.Error("expected omitted stateTransitionHistory capability to become false")
	}
	if data.AgentCard.SupportsAuthenticatedExtendedCard.ValueBool() {
		t.Error("expected omitted supportsAuthenticatedExtendedCard to become false")
	}
}

func TestReadAgentCardMissingCapabilitiesMapClearsManagedValues(t *testing.T) {
	t.Parallel()

	resource := &AgentResource{}
	data := AgentResourceModel{AgentCard: &AgentCardModel{Capabilities: &AgentCapabilitiesModel{
		Streaming:         types.BoolValue(true),
		PushNotifications: types.BoolValue(true),
	}}}
	resource.readAgentCard(map[string]interface{}{}, &data)

	if data.AgentCard.Capabilities.Streaming.ValueBool() || data.AgentCard.Capabilities.PushNotifications.ValueBool() {
		t.Fatalf("omitted capabilities map retained managed values: %#v", data.AgentCard.Capabilities)
	}
}

func TestReadAgentCardLeavesUnmanagedCapabilityNull(t *testing.T) {
	t.Parallel()

	resource := &AgentResource{}
	data := AgentResourceModel{AgentCard: &AgentCardModel{Capabilities: &AgentCapabilitiesModel{
		Streaming:         types.BoolValue(true),
		PushNotifications: types.BoolNull(),
	}}}
	resource.readAgentCard(map[string]interface{}{
		"capabilities": map[string]interface{}{
			"streaming":         true,
			"pushNotifications": true,
		},
	}, &data)

	if !data.AgentCard.Capabilities.PushNotifications.IsNull() {
		t.Fatalf("unmanaged push_notifications became %v, want null", data.AgentCard.Capabilities.PushNotifications)
	}
}

func TestReadAgentCardImportAdoptsOnlyVisibleCapabilities(t *testing.T) {
	t.Parallel()

	resource := &AgentResource{}
	data := AgentResourceModel{}
	resource.readAgentCard(map[string]interface{}{
		"capabilities": map[string]interface{}{"streaming": true},
	}, &data)

	if data.AgentCard == nil || data.AgentCard.Capabilities == nil {
		t.Fatal("import did not populate capabilities")
	}
	if !data.AgentCard.Capabilities.Streaming.ValueBool() || !data.AgentCard.Capabilities.PushNotifications.IsNull() || !data.AgentCard.Capabilities.StateTransitionHistory.IsNull() {
		t.Fatalf("unexpected imported capabilities: %#v", data.AgentCard.Capabilities)
	}
}

func TestChangedAgentCapabilityFieldsNotConverged(t *testing.T) {
	t.Parallel()

	prior := AgentResourceModel{AgentCard: &AgentCardModel{
		SupportsAuthenticatedExtendedCard: types.BoolValue(false),
		Capabilities: &AgentCapabilitiesModel{
			PushNotifications: types.BoolValue(false),
		},
	}}
	planned := cloneAgentResourceModel(prior)
	planned.AgentCard.SupportsAuthenticatedExtendedCard = types.BoolValue(true)
	planned.AgentCard.Capabilities.PushNotifications = types.BoolValue(true)
	observed := cloneAgentResourceModel(planned)
	observed.AgentCard.Capabilities.PushNotifications = types.BoolValue(false)

	got := changedAgentCapabilityFieldsNotConverged(planned, prior, observed)
	want := []string{"agent_card.capabilities.push_notifications"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("stale fields = %v, want %v", got, want)
	}
}

func TestReadAgentCapabilitiesAfterUpdateRequiresStableValues(t *testing.T) {
	t.Parallel()

	var reads atomic.Int32
	sequence := []bool{true, false, true, true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		index := int(reads.Add(1)) - 1
		if index >= len(sequence) {
			index = len(sequence) - 1
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"agent_id": "agent-1", "agent_name": "agent",
			"agent_card_params": map[string]interface{}{
				"name": "Agent", "url": "https://agent.invalid",
				"supportsAuthenticatedExtendedCard": sequence[index],
			},
		})
	}))
	defer server.Close()

	resource := &AgentResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	prior := AgentResourceModel{
		ID: types.StringValue("agent-1"),
		AgentCard: &AgentCardModel{
			SupportsAuthenticatedExtendedCard: types.BoolValue(false),
		},
	}
	planned := cloneAgentResourceModel(prior)
	planned.AgentCard.SupportsAuthenticatedExtendedCard = types.BoolValue(true)
	data := cloneAgentResourceModel(planned)

	if err := resource.readAgentCapabilitiesAfterUpdate(context.Background(), &data, planned, prior, 5); err != nil {
		t.Fatalf("readAgentCapabilitiesAfterUpdate returned error: %v", err)
	}
	if got := reads.Load(); got != 4 {
		t.Fatalf("read count = %d, want 4", got)
	}
	if !data.AgentCard.SupportsAuthenticatedExtendedCard.ValueBool() || !planned.AgentCard.SupportsAuthenticatedExtendedCard.ValueBool() {
		t.Fatal("stable read did not preserve planned true value")
	}
}

func TestReadAgentCapabilitiesAfterUpdateRejectsPersistentOmission(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"agent_id": "agent-1", "agent_name": "agent",
			"agent_card_params": map[string]interface{}{
				"name": "Agent", "url": "https://agent.invalid",
				"capabilities": map[string]interface{}{"streaming": true},
			},
		})
	}))
	defer server.Close()

	resource := &AgentResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	prior := AgentResourceModel{
		ID: types.StringValue("agent-1"),
		AgentCard: &AgentCardModel{Capabilities: &AgentCapabilitiesModel{
			PushNotifications: types.BoolValue(false),
		}},
	}
	planned := cloneAgentResourceModel(prior)
	planned.AgentCard.Capabilities.PushNotifications = types.BoolValue(true)
	data := cloneAgentResourceModel(planned)

	err := resource.readAgentCapabilitiesAfterUpdate(context.Background(), &data, planned, prior, 3)
	if err == nil || !strings.Contains(err.Error(), "push_notifications") {
		t.Fatalf("error = %v, want persistent push_notifications omission", err)
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
