package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestBuildAgentUpdateRequestExplicitClears(t *testing.T) {
	t.Parallel()
	resource := &AgentResource{}
	state := AgentResourceModel{
		ID:              types.StringValue("agent-clear"),
		AgentName:       types.StringValue("agent"),
		TPMLimit:        types.Int64Value(1),
		RPMLimit:        types.Int64Value(2),
		SessionTPMLimit: types.Int64Value(3),
		SessionRPMLimit: types.Int64Value(4),
		LiteLLMParams:   stringMapValue(map[string]string{"model": "old", "removable": "old"}),
		StaticHeaders:   stringMapValue(map[string]string{"Authorization": "secret"}),
		ExtraHeaders:    stringListValue("X-Old"),
		AgentCard: &AgentCardModel{
			Name:               types.StringValue("Agent"),
			URL:                types.StringValue("https://agent.invalid"),
			Description:        types.StringValue("old"),
			PreferredTransport: types.StringValue("JSONRPC"),
			Capabilities:       &AgentCapabilitiesModel{Streaming: types.BoolValue(true)},
			Provider:           &AgentProviderModel{Organization: types.StringValue("old"), URL: types.StringValue("https://old.invalid")},
			Skills: []AgentSkillModel{{
				ID: types.StringValue("skill"), Name: types.StringValue("Skill"), Description: types.StringValue("old"),
				Tags: stringListValue("old"), Examples: stringListValue("old"), InputModes: stringListValue("text"), OutputModes: stringListValue("text"),
			}},
		},
		ObjectPermission: &AgentObjectPermissionModel{
			MCPServers: stringListValue("server"), MCPAccessGroups: stringListValue("group"),
			MCPToolPermissions: stringMapValue(map[string]string{"server": `["tool"]`}),
			Models:             stringListValue("model"), Agents: stringListValue("other"),
		},
	}
	plan := cloneAgentResourceModel(state)
	plan.TPMLimit, plan.RPMLimit = types.Int64Null(), types.Int64Null()
	plan.SessionTPMLimit, plan.SessionRPMLimit = types.Int64Null(), types.Int64Null()
	plan.LiteLLMParams = stringMapValue(map[string]string{"model": "old"})
	plan.StaticHeaders = stringMapValue(map[string]string{})
	plan.ExtraHeaders = stringListValue()
	plan.AgentCard.Description = types.StringNull()
	plan.AgentCard.PreferredTransport = types.StringNull()
	plan.AgentCard.Capabilities = nil
	plan.AgentCard.Provider = &AgentProviderModel{Organization: types.StringNull(), URL: types.StringValue("https://old.invalid")}
	plan.AgentCard.Skills[0].Description = types.StringNull()
	plan.AgentCard.Skills[0].Tags = stringListValue()
	plan.AgentCard.Skills[0].Examples = stringListValue()
	plan.AgentCard.Skills[0].InputModes = stringListValue()
	plan.AgentCard.Skills[0].OutputModes = stringListValue()
	plan.ObjectPermission = nil

	config := cloneAgentResourceModel(plan)
	request, err := resource.buildAgentUpdateRequest(&plan, &state, &config, agentFieldSet{})
	if err != nil {
		t.Fatalf("build update: %v", err)
	}
	for _, field := range []string{"tpm_limit", "rpm_limit", "session_tpm_limit", "session_rpm_limit"} {
		if value, present := request[field]; !present || value != nil {
			t.Fatalf("%s clear = %#v, want explicit null", field, value)
		}
	}
	if got := request["litellm_params"]; !reflect.DeepEqual(got, map[string]interface{}{"model": "old"}) {
		t.Fatalf("parameter key removal payload = %#v", got)
	}
	if got := request["static_headers"]; !reflect.DeepEqual(got, map[string]interface{}{}) {
		t.Fatalf("static header clear = %#v", got)
	}
	if got := request["extra_headers"]; !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("extra header clear = %#v", got)
	}
	permission := request["object_permission"].(map[string]interface{})
	for _, field := range []string{"mcp_servers", "mcp_access_groups", "models", "agents"} {
		if got := permission[field]; !reflect.DeepEqual(got, []string{}) {
			t.Fatalf("permission %s clear = %#v", field, got)
		}
	}
	if got := permission["mcp_tool_permissions"]; !reflect.DeepEqual(got, map[string][]string{}) {
		t.Fatalf("tool permission clear = %#v", got)
	}
	card := request["agent_card_params"].(map[string]interface{})
	for _, omitted := range []string{"description", "preferredTransport", "capabilities"} {
		if _, present := card[omitted]; present {
			t.Fatalf("card clear %s was not represented by whole-card omission: %#v", omitted, card)
		}
	}
	provider := card["provider"].(map[string]interface{})
	if _, present := provider["organization"]; present || provider["url"] != "https://old.invalid" {
		t.Fatalf("provider field clear = %#v", provider)
	}
	skill := card["skills"].([]map[string]interface{})[0]
	for _, collection := range []string{"tags", "examples", "inputModes", "outputModes"} {
		if got := skill[collection]; !reflect.DeepEqual(got, []string{}) {
			t.Fatalf("skill %s clear = %#v", collection, got)
		}
	}
}

func TestBuildAgentUpdateRequestPreservesImportedUnownedFields(t *testing.T) {
	t.Parallel()
	resource := &AgentResource{}
	state := AgentResourceModel{
		ID: types.StringValue("agent-import"), AgentName: types.StringValue("agent"),
		TPMLimit: types.Int64Value(100),
		AgentCard: &AgentCardModel{
			Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"),
			Version: types.StringValue("1.0.0"), ProtocolVersion: types.StringValue("1.0"),
			Capabilities: &AgentCapabilitiesModel{Streaming: types.BoolValue(true)},
			Provider:     &AgentProviderModel{Organization: types.StringValue("remote"), URL: types.StringValue("https://remote.invalid")},
		},
		ObjectPermission: &AgentObjectPermissionModel{Models: stringListValue("remote-model")},
	}
	plan := AgentResourceModel{
		ID: types.StringValue("agent-import"), AgentName: types.StringValue("agent"),
		AgentCard: &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid")},
	}
	config := cloneAgentResourceModel(plan)
	imported := agentImportedFieldsFromState(state)
	wire := cloneAgentResourceModel(plan)
	wire.AgentCard = overlayAgentCardWire(state, plan, state, config, imported)
	request, err := resource.buildAgentUpdateRequest(&wire, &state, &config, imported, true)
	if err != nil {
		t.Fatalf("build imported update: %v", err)
	}
	card := request["agent_card_params"].(map[string]interface{})
	if card["version"] != "1.0.0" || card["protocolVersion"] != "1.0" {
		t.Fatalf("wire-only imported card fields were lost: %#v", card)
	}
	if capabilities := card["capabilities"].(map[string]interface{}); capabilities["streaming"] != true {
		t.Fatalf("wire-only imported capability was lost: %#v", capabilities)
	}
	if provider := card["provider"].(map[string]interface{}); provider["organization"] != "remote" || provider["url"] != "https://remote.invalid" {
		t.Fatalf("wire-only imported provider was lost: %#v", provider)
	}
	for _, omitted := range []string{"tpm_limit", "object_permission"} {
		if _, present := request[omitted]; present {
			t.Fatalf("imported unconfigured %s was falsely sent or cleared: %#v", omitted, request)
		}
	}
	if plan.AgentCard.Version.IsNull() == false || plan.AgentCard.Capabilities != nil || plan.AgentCard.Provider != nil {
		t.Fatal("wire hydration claimed imported fields in planned public state")
	}
}

func TestValidateAgentUpdateClearsRejectsV198PhantomClears(t *testing.T) {
	t.Parallel()
	base := AgentResourceModel{
		LiteLLMParams: stringMapValue(map[string]string{"model": "old"}),
		AgentCard: &AgentCardModel{
			Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"),
			Version: types.StringValue("1.0.0"), ProtocolVersion: types.StringValue("1.0"),
			DefaultInputModes: stringListValue("text"), DefaultOutputModes: stringListValue("text"),
			Provider: &AgentProviderModel{Organization: types.StringValue("old")},
			Skills:   []AgentSkillModel{{ID: types.StringValue("skill"), Name: types.StringValue("Skill")}},
		},
	}
	for _, test := range []struct {
		name   string
		mutate func(*AgentResourceModel)
	}{
		{"empty params", func(value *AgentResourceModel) { value.LiteLLMParams = stringMapValue(map[string]string{}) }},
		{"version", func(value *AgentResourceModel) { value.AgentCard.Version = types.StringNull() }},
		{"protocol", func(value *AgentResourceModel) { value.AgentCard.ProtocolVersion = types.StringNull() }},
		{"input modes", func(value *AgentResourceModel) { value.AgentCard.DefaultInputModes = stringListValue() }},
		{"output modes", func(value *AgentResourceModel) { value.AgentCard.DefaultOutputModes = stringListValue() }},
		{"provider", func(value *AgentResourceModel) { value.AgentCard.Provider = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			plan := cloneAgentResourceModel(base)
			test.mutate(&plan)
			if err := validateAgentUpdateClears(plan, base, plan, agentFieldSet{}); err == nil {
				t.Fatal("unsafe v1.98 clear was accepted")
			}
		})
	}
}

func planAgentProtocolCreate(t *testing.T, ctx context.Context, protocolServer tfprotov6.ProviderServer, schema *tfprotov6.Schema, configValues map[string]interface{}) (*tfprotov6.DynamicValue, *tfprotov6.DynamicValue, []byte) {
	t.Helper()
	config := agentProtocolDynamicValue(t, schema, configValues)
	proposedValues := map[string]interface{}{
		"id": tftypes.UnknownValue, "agent_name": configValues["agent_name"], "agent_card": configValues["agent_card"],
		"litellm_params": tftypes.UnknownValue, "static_headers": tftypes.UnknownValue, "extra_headers": tftypes.UnknownValue,
		"created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue, "created_by": tftypes.UnknownValue, "updated_by": tftypes.UnknownValue,
	}
	proposed := agentProtocolDynamicValue(t, schema, proposedValues)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan create: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	return config, nullState, planned.PlannedPrivate
}

func TestAgentProtocolCreateMissingIDNeverReadsEmptyIdentity(t *testing.T) {
	for _, response := range []string{`{}`, `{"agent_id":""}`, `{"agent_id":"   "}`, `{"agent_id":`} {
		t.Run(response, func(t *testing.T) {
			ctx := context.Background()
			var identityGets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodPost {
					_, _ = fmt.Fprint(writer, response)
					return
				}
				if request.URL.Path != "/v1/agents" {
					identityGets.Add(1)
				}
				http.Error(writer, "not found", http.StatusNotFound)
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_agent"]
			values := map[string]interface{}{"agent_name": "agent", "agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"}}
			config, nullState, _ := planAgentProtocolCreate(t, ctx, protocolServer, schema, values)
			planned, _ := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: nullState, ProposedNewState: agentProtocolDynamicValue(t, schema, map[string]interface{}{
				"id": tftypes.UnknownValue, "agent_name": "agent", "agent_card": values["agent_card"],
				"litellm_params": tftypes.UnknownValue, "static_headers": tftypes.UnknownValue, "extra_headers": tftypes.UnknownValue,
				"created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue, "created_by": tftypes.UnknownValue, "updated_by": tftypes.UnknownValue,
			})})
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("missing identity accepted: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
			}
			if identityGets.Load() != 0 {
				t.Fatalf("create attempted %d identity read(s) without a confirmed identity", identityGets.Load())
			}
			if applied.NewState != nil {
				value, _ := applied.NewState.Unmarshal(schema.ValueType())
				if !value.IsNull() {
					t.Fatal("missing create identity published state")
				}
			}
		})
	}
}

func TestAgentProtocolCreateResponseLossRecoveryIsUniqueAndExact(t *testing.T) {
	for _, test := range []struct {
		name        string
		lists       []string
		wantSuccess bool
	}{
		{"unique", []string{`[{"agent_id":"recovered","agent_name":"agent"}]`}, true},
		{"stale worker then visible", []string{`[]`, `[{"agent_id":"recovered","agent_name":"agent"}]`}, true},
		{"zero", []string{`[]`}, false},
		{"malformed list", []string{`{"agents":`}, false},
		{"multiple", []string{`[{"agent_id":"one","agent_name":"agent"},{"agent_id":"two","agent_name":"agent"}]`}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var listReads atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodPost:
					_, _ = io.WriteString(w, `{"agent_id":`)
				case r.Method == http.MethodGet && r.URL.Path == "/v1/agents":
					index := int(listReads.Add(1)) - 1
					if index >= len(test.lists) {
						index = len(test.lists) - 1
					}
					_, _ = io.WriteString(w, test.lists[index])
				case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/agents/"):
					id := strings.TrimPrefix(r.URL.Path, "/v1/agents/")
					_, _ = fmt.Fprintf(w, `{"agent_id":%q,"agent_name":"agent","agent_card_params":{"name":"Agent","url":"https://agent.invalid"}}`, id)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_agent"]
			values := map[string]interface{}{"agent_name": "agent", "agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"}}
			config, nullState, _ := planAgentProtocolCreate(t, ctx, protocolServer, schema, values)
			proposed := agentProtocolDynamicValue(t, schema, map[string]interface{}{"id": tftypes.UnknownValue, "agent_name": "agent", "agent_card": values["agent_card"], "litellm_params": tftypes.UnknownValue, "static_headers": tftypes.UnknownValue, "extra_headers": tftypes.UnknownValue, "created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue, "created_by": tftypes.UnknownValue, "updated_by": tftypes.UnknownValue})
			planned, _ := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: nullState, ProposedNewState: proposed})
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil {
				t.Fatal(err)
			}
			if accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) == test.wantSuccess {
				t.Fatalf("diagnostics=%s", agentProtocolDiagnosticsText(applied.Diagnostics))
			}
			if test.wantSuccess {
				var id string
				if err := protocolAttributeMap(t, schema, applied.NewState)["id"].As(&id); err != nil || id != "recovered" {
					t.Fatalf("id=%q err=%v", id, err)
				}
			}
		})
	}
}

func TestAgentProtocolMinimalCreateReadNoDriftDestroyWithServerDefaults(t *testing.T) {
	ctx := context.Background()
	var deleted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			_, _ = io.WriteString(w, `{"agent_id":"minimal"}`)
		case http.MethodGet:
			_, _ = io.WriteString(w, `{"agent_id":"minimal","agent_name":"agent","litellm_params":{},"agent_card_params":{"name":"Agent","url":"https://agent.invalid","version":"1.0.0","protocolVersion":"1.0","defaultInputModes":["text"],"defaultOutputModes":["text"],"capabilities":{},"provider":{"organization":"LiteLLM Proxy","url":"http://proxy"},"skills":[{"id":"chat","name":"Chat","description":"default","tags":["chat"]}]}}`)
		case http.MethodDelete:
			deleted.Store(true)
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_agent"]
	values := map[string]interface{}{"agent_name": "agent", "agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"}}
	config, nullState, _ := planAgentProtocolCreate(t, ctx, protocolServer, schema, values)
	proposed := agentProtocolDynamicValue(t, schema, map[string]interface{}{
		"id": tftypes.UnknownValue, "agent_name": "agent", "agent_card": values["agent_card"],
		"litellm_params": tftypes.UnknownValue, "static_headers": tftypes.UnknownValue, "extra_headers": tftypes.UnknownValue,
		"created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue, "created_by": tftypes.UnknownValue, "updated_by": tftypes.UnknownValue,
	})
	planned, _ := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: nullState, ProposedNewState: proposed})
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("minimal create: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: applied.NewState, Private: applied.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("minimal read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(refreshed.Diagnostics))
	}
	noDrift, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: refreshed.NewState, ProposedNewState: refreshed.NewState, PriorPrivate: refreshed.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(noDrift.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, refreshed.NewState, noDrift) != organizationProjectProtocolActionNoOp {
		t.Fatalf("minimal no-drift plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(noDrift.Diagnostics))
	}
	destroy, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: nullState, PriorState: refreshed.NewState, ProposedNewState: nullState, PriorPrivate: refreshed.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroy.Diagnostics) {
		t.Fatalf("minimal destroy plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(destroy.Diagnostics))
	}
	removed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_agent", Config: nullState, PriorState: refreshed.NewState, PlannedState: destroy.PlannedState, PlannedPrivate: destroy.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(removed.Diagnostics) || !deleted.Load() {
		t.Fatalf("minimal destroy: err=%v diagnostics=%s deleted=%t", err, agentProtocolDiagnosticsText(removed.Diagnostics), deleted.Load())
	}
}

func TestAgentProtocolImportAdoptsLaterAPISkillSibling(t *testing.T) {
	ctx := context.Background()
	var added atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		skills := `[{"id":"original","name":"Original","description":"remote"}]`
		if added.Load() {
			skills = `[{"id":"original","name":"Original","description":"remote"},{"id":"added","name":"Added","description":"remote"}]`
		}
		_, _ = fmt.Fprintf(w, `{"agent_id":"imported","agent_name":"agent","agent_card_params":{"name":"Agent","url":"https://agent.invalid","skills":%s}}`, skills)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_agent"
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "imported"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("initial import read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	added.Store(true)
	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: read.NewState, Private: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("API sibling read: err=%v diagnostics=%v", err, refreshed.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schemas.ResourceSchemas[typeName], refreshed.NewState)
	var card map[string]tftypes.Value
	if err := attributes["agent_card"].As(&card); err != nil {
		t.Fatal(err)
	}
	var skills []tftypes.Value
	if err := card["skills"].As(&skills); err != nil || len(skills) != 2 {
		t.Fatalf("imported skills=%d err=%v", len(skills), err)
	}
	var second map[string]tftypes.Value
	if err := skills[1].As(&second); err != nil {
		t.Fatal(err)
	}
	var secondID string
	if err := second["id"].As(&secondID); err != nil || secondID != "added" {
		t.Fatalf("added skill id=%q err=%v", secondID, err)
	}
}

func TestValidateAgentUpdateClearsUnknownAndUnchangedEmptyBlocks(t *testing.T) {
	state := AgentResourceModel{AgentCard: &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"), Version: types.StringValue("1"), Provider: &AgentProviderModel{Organization: types.StringNull(), URL: types.StringNull()}, Capabilities: &AgentCapabilitiesModel{Streaming: types.BoolNull(), PushNotifications: types.BoolNull(), StateTransitionHistory: types.BoolNull()}}}
	plan := cloneAgentResourceModel(state)
	config := cloneAgentResourceModel(state)
	if err := validateAgentUpdateClears(plan, state, config, nil); err != nil {
		t.Fatalf("unchanged empty blocks rejected: %v", err)
	}
	plan.AgentCard.Version = types.StringUnknown()
	config.AgentCard.Version = types.StringUnknown()
	plan.AgentCard.Provider.Organization = types.StringUnknown()
	config.AgentCard.Provider.Organization = types.StringUnknown()
	if err := validateAgentUpdateClears(plan, state, config, nil); err != nil {
		t.Fatalf("unknown values treated as removals: %v", err)
	}
}

func TestReadAgentAppliesLeafOwnershipAndStableSkillIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"agent_id":"owned","agent_name":"agent","litellm_params":{"configured":{"x":1},"api":"new"},"static_headers":{"X-API":"new"},"agent_card_params":{"name":"Agent","url":"https://agent.invalid","provider":{"organization":"remote-new","url":"https://configured.invalid"},"skills":[{"id":"second","name":"Second"},{"id":"first","name":"First","description":"remote-new"}]},"object_permission":{"models":["model"],"mcp_servers":["must-not-adopt"]}}`)
	}))
	defer server.Close()
	data := AgentResourceModel{ID: types.StringValue("owned"), AgentName: types.StringValue("agent"), LiteLLMParams: stringMapValue(map[string]string{"configured": `{ "x": 1 }`, "api": "old"}), StaticHeaders: stringMapValue(map[string]string{"X-API": "old"}), AgentCard: &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"), Provider: &AgentProviderModel{Organization: types.StringValue("remote-old"), URL: types.StringValue("https://configured.invalid")}, Skills: []AgentSkillModel{{ID: types.StringValue("first"), Name: types.StringValue("First"), Description: types.StringValue("remote-old")}, {ID: types.StringValue("second"), Name: types.StringValue("Second")}}}, ObjectPermission: &AgentObjectPermissionModel{Models: stringListValue("model"), MCPServers: types.ListNull(types.StringType)}}
	owned := agentFieldSet{agentLeaf(agentFieldParams, "api"): true, agentLeaf(agentFieldStaticHeaders, "X-API"): true, agentFieldCardProviderOrg: true, agentSkillLeaf("first", "description"): true}
	r := &AgentResource{client: &Client{APIBase: server.URL, HTTPClient: server.Client()}}
	if err := r.readAgentWithOwnership(context.Background(), &data, false, owned); err != nil {
		t.Fatal(err)
	}
	var params map[string]string
	_ = data.LiteLLMParams.ElementsAs(context.Background(), &params, false)
	if params["configured"] != `{ "x": 1 }` || params["api"] != "new" {
		t.Fatalf("params=%#v", params)
	}
	if data.AgentCard.Provider.Organization.ValueString() != "remote-new" || data.AgentCard.Skills[0].ID.ValueString() != "first" || data.AgentCard.Skills[0].Description.ValueString() != "remote-new" {
		t.Fatalf("card=%#v", data.AgentCard)
	}
	if !data.ObjectPermission.MCPServers.IsNull() {
		t.Fatal("unconfigured object-permission sibling was adopted")
	}
}

func TestAgentCardWireUsesFreshPreflightAndExactLeafOwnership(t *testing.T) {
	state := AgentResourceModel{AgentCard: &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"), Description: types.StringValue("old"), Provider: &AgentProviderModel{Organization: types.StringValue("stale"), URL: types.StringValue("https://configured.invalid")}, Skills: []AgentSkillModel{{ID: types.StringValue("skill"), Name: types.StringValue("Skill"), Description: types.StringValue("stale")}}}}
	plan := cloneAgentResourceModel(state)
	plan.AgentCard.Description = types.StringValue("new")
	config := cloneAgentResourceModel(plan)
	config.AgentCard.Provider.Organization = types.StringNull()
	config.AgentCard.Skills[0].Description = types.StringNull()
	fresh := cloneAgentResourceModel(state)
	fresh.AgentCard.Provider.Organization = types.StringValue("direct-api")
	fresh.AgentCard.Skills[0].Description = types.StringValue("direct-api")
	imported := agentFieldSet{agentFieldCardProviderOrg: true, agentSkillLeaf("skill", "description"): true}
	if !agentCardUpdateTouched(plan, state, config, imported) {
		t.Fatal("card change not detected")
	}
	wire := overlayAgentCardWire(fresh, plan, state, config, imported)
	if wire.Description.ValueString() != "new" || wire.Provider.Organization.ValueString() != "direct-api" || wire.Skills[0].Description.ValueString() != "direct-api" {
		t.Fatalf("wire=%#v", wire)
	}
	configured := agentConfiguredFields(config)
	for field := range configured {
		delete(imported, field)
	}
	if !imported[agentFieldCardProviderOrg] || !imported[agentSkillLeaf("skill", "description")] {
		t.Fatal("configuring siblings erased API-owned leaf provenance")
	}
}

func TestConfirmAgentMutationRetriesTransientFailuresWith124Backoff(t *testing.T) {
	var reads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if reads.Add(1) == 1 {
			http.Error(w, "transient", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"agent_id":"retry","agent_name":"agent","agent_card_params":{"name":"Agent","url":"https://agent.invalid"}}`)
	}))
	defer server.Close()
	model := AgentResourceModel{ID: types.StringValue("retry"), AgentName: types.StringValue("agent"), AgentCard: &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid")}}
	r := &AgentResource{client: &Client{APIBase: server.URL, HTTPClient: server.Client()}}
	started := time.Now()
	if _, err := r.confirmAgentMutation(context.Background(), model, AgentResourceModel{}, model, nil, 8); err != nil {
		t.Fatal(err)
	}
	if reads.Load() != 3 || time.Since(started) < 700*time.Millisecond {
		t.Fatalf("reads=%d elapsed=%s", reads.Load(), time.Since(started))
	}
}

func TestAgentFreshConfirmationAndCardPreflightFailClosedForCustomTransport(t *testing.T) {
	calls := atomic.Int32{}
	client := &Client{APIBase: "http://example.invalid", HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, fmt.Errorf("must not dispatch")
	})}}
	r := &AgentResource{client: client}
	model := AgentResourceModel{ID: types.StringValue("agent"), AgentName: types.StringValue("agent"), AgentCard: &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid")}}
	if _, err := r.confirmAgentMutation(context.Background(), model, AgentResourceModel{}, model, nil, 2); err == nil {
		t.Fatal("mutation confirmation accepted a transport without provable fresh connections")
	}
	if _, err := r.sampleFreshAgentCard(context.Background(), model, nil, 2); err == nil {
		t.Fatal("card preflight accepted a transport without provable fresh connections")
	}
	if calls.Load() != 0 {
		t.Fatalf("custom transport dispatched %d requests", calls.Load())
	}
}

func TestAgentProtocolReadUsesExact404Only(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		body       string
		wantAbsent bool
	}{
		{"exact 404", http.StatusNotFound, `{"detail":"gone"}`, true},
		{"500 body says not found", http.StatusInternalServerError, `{"detail":"not found after cache failure"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_agent"]
			state := agentProtocolDynamicValue(t, schema, map[string]interface{}{"id": "agent-read", "agent_name": "prior", "agent_card": map[string]interface{}{"name": "Prior", "url": "https://prior.invalid"}})
			read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: state})
			if err != nil {
				t.Fatal(err)
			}
			value, _ := read.NewState.Unmarshal(schema.ValueType())
			if value.IsNull() != test.wantAbsent {
				t.Fatalf("state absent=%t, want %t; diagnostics=%s", value.IsNull(), test.wantAbsent, agentProtocolDiagnosticsText(read.Diagnostics))
			}
			if !test.wantAbsent {
				attributes := protocolAttributeMap(t, schema, read.NewState)
				var id, name string
				idErr := attributes["id"].As(&id)
				nameErr := attributes["agent_name"].As(&name)
				if idErr != nil || nameErr != nil || id != "agent-read" || name != "prior" || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
					t.Fatalf("500 not-found body changed prior identity/name or omitted error: id=%q name=%q diagnostics=%s", id, name, agentProtocolDiagnosticsText(read.Diagnostics))
				}
			}
		})
	}
}

func TestAgentProtocolDeleteIsIdempotentOnlyOnExact404(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		wantError bool
	}{
		{"exact 404", http.StatusNotFound, false},
		{"500", http.StatusInternalServerError, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, `{"detail":"not found"}`)
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_agent"]
			state := agentProtocolDynamicValue(t, schema, map[string]interface{}{"id": "agent-delete", "agent_name": "agent", "agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"}})
			null := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: null, PriorState: state, ProposedNewState: null})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("destroy plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_agent", Config: null, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) != test.wantError {
				t.Fatalf("delete: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
			}
		})
	}
}

func TestConfiguredMinimalAgentDoesNotAdoptServerCardDefaults(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"agent_id":"minimal","agent_name":"agent","litellm_params":{},"agent_card_params":{"name":"Agent","url":"https://agent.invalid","version":"1.0.0","protocolVersion":"1.0","defaultInputModes":["text"],"defaultOutputModes":["text"],"capabilities":{},"provider":{"organization":"LiteLLM Proxy","url":"http://proxy"},"skills":[{"id":"chat","name":"Chat","description":"default","tags":["chat"]}]}}`)
	}))
	defer server.Close()
	data := AgentResourceModel{
		ID: types.StringValue("minimal"), AgentName: types.StringValue("agent"),
		LiteLLMParams: types.MapNull(types.StringType), StaticHeaders: types.MapNull(types.StringType), ExtraHeaders: types.ListNull(types.StringType),
		AgentCard: &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"),
			Version: types.StringNull(), ProtocolVersion: types.StringNull(), DefaultInputModes: types.ListNull(types.StringType), DefaultOutputModes: types.ListNull(types.StringType),
			Description: types.StringNull(), PreferredTransport: types.StringNull(), IconURL: types.StringNull(), DocumentationURL: types.StringNull(), SupportsAuthenticatedExtendedCard: types.BoolNull()},
	}
	r := &AgentResource{client: &Client{APIBase: server.URL, HTTPClient: server.Client()}}
	owned := agentFieldSet{}
	if err := r.readAgentWithOwnership(context.Background(), &data, false, owned); err != nil {
		t.Fatal(err)
	}
	if !data.AgentCard.Version.IsNull() || !data.AgentCard.ProtocolVersion.IsNull() || !data.AgentCard.DefaultInputModes.IsNull() ||
		data.AgentCard.Capabilities != nil || data.AgentCard.Provider != nil || data.AgentCard.Skills != nil || len(owned) != 0 || !data.LiteLLMParams.IsNull() {
		t.Fatalf("configured omission adopted server defaults: card=%#v owned=%#v params=%#v", data.AgentCard, owned, data.LiteLLMParams)
	}
}

func TestImportedAgentSkillScopeAdoptsAddedSiblingWithoutOwnershipTransfer(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"agent_id":"imported","agent_name":"agent","agent_card_params":{"name":"Agent","url":"https://agent.invalid","skills":[{"id":"configured","name":"Configured","description":"remote"},{"id":"api-added","name":"API Added","description":"remote"}]}}`)
	}))
	defer server.Close()
	data := AgentResourceModel{ID: types.StringValue("imported"), AgentName: types.StringValue("agent"), AgentCard: &AgentCardModel{
		Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"),
		Skills: []AgentSkillModel{{ID: types.StringValue("configured"), Name: types.StringValue("Configured"), Description: types.StringValue("configured")}},
	}}
	owned := agentFieldSet{agentScopeCardSkills: true}
	r := &AgentResource{client: &Client{APIBase: server.URL, HTTPClient: server.Client()}}
	if err := r.readAgentWithOwnership(context.Background(), &data, false, owned); err != nil {
		t.Fatal(err)
	}
	if len(data.AgentCard.Skills) != 2 || data.AgentCard.Skills[0].Description.ValueString() != "remote" || !owned[agentSkillLeaf("api-added", "name")] {
		t.Fatalf("skill reconciliation=%#v ownership=%#v", data.AgentCard.Skills, owned)
	}
	if owned[agentSkillLeaf("configured", "description")] {
		t.Fatal("structural scope transferred configured sibling ownership")
	}
}

func TestAgentSkillRemovalOwnershipAndPayload(t *testing.T) {
	t.Parallel()
	skill := func(id string) AgentSkillModel {
		return AgentSkillModel{ID: types.StringValue(id), Name: types.StringValue(id)}
	}
	state := AgentResourceModel{AgentName: types.StringValue("agent"), AgentCard: &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"), Skills: []AgentSkillModel{skill("keep"), skill("remove")}}}
	plan := cloneAgentResourceModel(state)
	plan.AgentCard.Skills = []AgentSkillModel{skill("keep")}
	config := cloneAgentResourceModel(plan)
	if err := validateAgentUpdateClears(plan, state, config, agentFieldSet{agentSkillLeaf("remove", "name"): true}); err == nil {
		t.Fatal("API-owned skill removal was accepted")
	}
	structuralScope := agentFieldSet{agentScopeCardSkills: true}
	if err := validateAgentUpdateClears(plan, state, config, structuralScope); err != nil {
		t.Fatalf("Terraform-owned skill removal rejected with retained import scope: %v", err)
	}
	fresh := cloneAgentResourceModel(state)
	wire := cloneAgentResourceModel(plan)
	wire.AgentCard = overlayAgentCardWire(fresh, plan, state, config, structuralScope)
	request, err := (&AgentResource{}).buildAgentUpdateRequest(&wire, &state, &config, structuralScope, true)
	if err != nil {
		t.Fatal(err)
	}
	skills := request["agent_card_params"].(map[string]interface{})["skills"].([]map[string]interface{})
	if len(skills) != 1 || skills[0]["id"] != "keep" {
		t.Fatalf("removal payload retained omitted skill: %#v", skills)
	}
	observed := cloneAgentResourceModel(plan)
	if !agentSkillsMutationMatch(plan.AgentCard.Skills, state.AgentCard, config.AgentCard.Skills, observed.AgentCard.Skills, structuralScope) {
		t.Fatal("confirmed Terraform-owned skill absence did not match")
	}
}

func TestImportedAgentPermissionScopeAdoptsFieldAndParentAbsence(t *testing.T) {
	t.Parallel()
	list := func(values ...string) types.List { return stringListValue(values...) }
	data := AgentResourceModel{ObjectPermission: &AgentObjectPermissionModel{
		MCPServers: list("server"), MCPAccessGroups: list("group"), MCPToolPermissions: types.MapNull(types.StringType),
		Models: list("model"), Agents: list("agent"),
	}}
	owned := agentFieldSet{
		agentScopePermission: true, agentFieldPermissionServers: true, agentFieldPermissionGroups: true,
		agentFieldPermissionModels: true, agentFieldPermissionAgents: true,
	}
	r := &AgentResource{}
	if err := r.readObjectPermissionWithOwnership(map[string]interface{}{
		"mcp_servers": nil, "mcp_access_groups": nil, "models": []interface{}{}, "agents": []interface{}{"agent"},
	}, &data, false, owned); err != nil {
		t.Fatal(err)
	}
	if !data.ObjectPermission.MCPServers.IsNull() || !data.ObjectPermission.MCPAccessGroups.IsNull() {
		t.Fatalf("explicit-null imported permission lists were retained: %#v", data.ObjectPermission)
	}
	if owned[agentFieldPermissionServers] || owned[agentFieldPermissionGroups] || !owned[agentScopePermission] {
		t.Fatalf("permission field/scope ownership after absence = %#v", owned)
	}
	if data.ObjectPermission.Models.IsNull() || len(data.ObjectPermission.Models.Elements()) != 0 {
		t.Fatalf("present empty imported permission list was not adopted: %#v", data.ObjectPermission.Models)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"agent_id":"permission","agent_name":"agent","object_permission":null}`)
	}))
	defer server.Close()
	data.ID = types.StringValue("permission")
	data.AgentName = types.StringValue("agent")
	r.client = &Client{APIBase: server.URL, HTTPClient: server.Client()}
	if err := r.readAgentWithOwnership(context.Background(), &data, false, owned); err != nil {
		t.Fatal(err)
	}
	if data.ObjectPermission != nil || !owned[agentScopePermission] {
		t.Fatalf("absent imported permission parent was not reconciled: data=%#v ownership=%#v", data.ObjectPermission, owned)
	}
	for _, field := range []string{agentFieldPermissionServers, agentFieldPermissionGroups, agentFieldPermissionTools, agentFieldPermissionModels, agentFieldPermissionAgents} {
		if owned[field] {
			t.Fatalf("absent permission parent retained leaf ownership %q", field)
		}
	}
}

func TestAgentSkillIdentityValidationRejectsDuplicateAndBlankIDs(t *testing.T) {
	t.Parallel()
	const protectedID = "protected-duplicate-skill-id"
	model := func(ids ...string) AgentResourceModel {
		skills := make([]AgentSkillModel, 0, len(ids))
		for _, id := range ids {
			skills = append(skills, AgentSkillModel{ID: types.StringValue(id), Name: types.StringValue("name")})
		}
		return AgentResourceModel{AgentCard: &AgentCardModel{Skills: skills}}
	}
	for _, value := range []AgentResourceModel{model(protectedID, protectedID), model(" ")} {
		if err := validateAgentModelSkillIdentities(value); err == nil || strings.Contains(err.Error(), protectedID) {
			t.Fatalf("unsafe/content-bearing skill identity diagnostic: %v", err)
		}
	}
	api := map[string]interface{}{"skills": []interface{}{map[string]interface{}{"id": protectedID, "name": "one"}, map[string]interface{}{"id": protectedID, "name": "two"}}}
	if err := validateAgentCardResponse(api, false); err == nil || strings.Contains(err.Error(), protectedID) {
		t.Fatalf("duplicate API skill IDs accepted or leaked: %v", err)
	}
}

func TestAgentLifecycleDiagnosticsDoNotLeakProtectedContent(t *testing.T) {
	t.Parallel()
	const secret = "sentinel-agent-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(writer, `{"detail":"not found %s"}`, secret)
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: secret, HTTPClient: server.Client()}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/v1/agents/agent", nil, nil)
	if err == nil || IsAPIErrorStatus(err, http.StatusNotFound) {
		t.Fatalf("unexpected classification: %v", err)
	}
	for _, protected := range []string{secret, server.URL, "/v1/agents/agent"} {
		if strings.Contains(err.Error(), protected) {
			t.Fatal("safe lifecycle error leaked protected content")
		}
	}
}
