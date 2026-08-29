package provider

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func protocolMCPV2Private(t *testing.T, provenance mcpInfoProvenance) []byte {
	t.Helper()
	values := mcpInfoMapPrivate{}
	if diagnostics := writeMCPInfoProvenance(context.Background(), values, provenance); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	raw, err := json.Marshal(map[string][]byte(values))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func protocolMCPStage2Harness(t *testing.T) (tfprotov6.ProviderServer, *tfprotov6.Schema, func()) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"server_id":"stage2","server_name":"top","url":"https://mcp.example.test","transport":"http"}`))
	}))
	protocolServer, schemas := configuredImportProtocolServer(t, context.Background(), server.URL)
	return protocolServer, schemas.ResourceSchemas["litellm_mcp_server"], server.Close
}

func protocolMCPStage2State(t *testing.T, schema *tfprotov6.Schema, document interface{}, generation int64) *tfprotov6.DynamicValue {
	t.Helper()
	return accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "stage2", "server_id": "stage2", "server_name": "top", "url": "https://mcp.example.test", "transport": "http",
		"mcp_info_json": document, "mcp_info_ownership_generation": generation,
	}))
}

func protocolMCPStage2Config(t *testing.T, schema *tfprotov6.Schema, extra map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	values := map[string]interface{}{"server_name": "top", "url": "https://mcp.example.test", "transport": "http"}
	for key, value := range extra {
		values[key] = value
	}
	return accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
}

func protocolMCPStringList(values ...string) []tftypes.Value {
	result := make([]tftypes.Value, len(values))
	for index, value := range values {
		result[index] = tftypes.NewValue(tftypes.String, value)
	}
	return result
}

func protocolMCPInt64(t *testing.T, value tftypes.Value) int64 {
	t.Helper()
	var number big.Float
	if err := value.As(&number); err != nil {
		t.Fatal(err)
	}
	result, _ := number.Int64()
	return result
}

func protocolMCPAttribute(t *testing.T, schema *tfprotov6.Schema, dynamic *tfprotov6.DynamicValue, name string) tftypes.Value {
	t.Helper()
	attributes := protocolAttributeMap(t, schema, dynamic)
	value, ok := attributes[name]
	if !ok {
		t.Fatalf("missing %s", name)
	}
	return value
}

func TestMCPInfoStage2ValidationProtocol(t *testing.T) {
	protocolServer, schema, closeServer := protocolMCPStage2Harness(t)
	defer closeServer()
	state := protocolMCPStage2State(t, schema, `{"remote":true}`, 0)
	cases := map[string]map[string]interface{}{
		"whole null":             {"mcp_info_json": "null"},
		"whole duplicate":        {"mcp_info_json": `{"secret":1,"secret":2}`},
		"override null":          {"mcp_info_overrides_json": "null"},
		"override duplicate":     {"mcp_info_overrides_json": `{"secret":1,"secret":2}`},
		"root clear":             {"mcp_info_clear_paths": protocolMCPStringList("")},
		"noncanonical pointer":   {"mcp_info_clear_paths": protocolMCPStringList("/bad~2pointer")},
		"duplicate pointer":      {"mcp_info_clear_paths": protocolMCPStringList("/owner", "/owner")},
		"ancestor pointer":       {"mcp_info_clear_paths": protocolMCPStringList("/owner", "/owner/team")},
		"override clear overlap": {"mcp_info_overrides_json": `{"owner":{"team":"configured"}}`, "mcp_info_clear_paths": protocolMCPStringList("/owner")},
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			config := protocolMCPStage2Config(t, schema, extra)
			proposed := organizationProjectProtocolReplace(t, schema, state, extra)
			response, err := protocolServer.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: protocolMCPV2Private(t, emptyMCPInfoProvenance())})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
				t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
			}
			if strings.Contains(agentProtocolDiagnosticsText(response.Diagnostics), "secret") || strings.Contains(agentProtocolDiagnosticsText(response.Diagnostics), "bad~2pointer") {
				t.Fatal("diagnostic exposed JSON content")
			}
		})
	}

	fixed := mcpInfoProtocolValue(t, schema, map[string]interface{}{"description": "fixed"}, nil)
	config := protocolMCPStage2Config(t, schema, map[string]interface{}{"mcp_info_json": `{"description":"fixed"}`, "mcp_info": fixed})
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"mcp_info_json": `{"description":"fixed"}`, "mcp_info": fixed})
	response, err := protocolServer.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: protocolMCPV2Private(t, emptyMCPInfoProvenance())})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
		t.Fatalf("whole/fixed conflict accepted: %v %s", err, agentProtocolDiagnosticsText(response.Diagnostics))
	}

	arrayState := protocolMCPStage2State(t, schema, `{"items":[{"name":"remote"}]}`, 0)
	arrayConfig := protocolMCPStage2Config(t, schema, map[string]interface{}{"mcp_info_clear_paths": protocolMCPStringList("/items/0")})
	arrayProposed := organizationProjectProtocolReplace(t, schema, arrayState, map[string]interface{}{"mcp_info_clear_paths": protocolMCPStringList("/items/0"), "mcp_info_json": tftypes.UnknownValue, "mcp_info_ownership_generation": tftypes.UnknownValue})
	response, err = protocolServer.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: arrayConfig, PriorState: arrayState, ProposedNewState: arrayProposed, PriorPrivate: protocolMCPV2Private(t, emptyMCPInfoProvenance())})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
		t.Fatalf("array clear traversal accepted: %v %s", err, agentProtocolDiagnosticsText(response.Diagnostics))
	}
}

func TestMCPInfoStage2ModifyPlanWholeEqualTakeoverAndEmptyClearProtocol(t *testing.T) {
	protocolServer, schema, closeServer := protocolMCPStage2Harness(t)
	defer closeServer()
	priorDocument := `{"owner":{"team":"security"}}`
	state := protocolMCPStage2State(t, schema, priorDocument, 0)
	private := protocolMCPV2Private(t, emptyMCPInfoProvenance())
	for _, test := range []struct{ name, configured, want string }{
		{name: "equal takeover", configured: `{ "owner" : { "team" : "security" } }`, want: `{ "owner" : { "team" : "security" } }`},
		{name: "whole empty clear", configured: `{}`, want: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := protocolMCPStage2Config(t, schema, map[string]interface{}{"mcp_info_json": test.configured})
			proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"mcp_info_json": test.configured, "mcp_info_ownership_generation": tftypes.UnknownValue})
			planned, err := protocolServer.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
			}
			var got string
			if err := protocolMCPAttribute(t, schema, planned.PlannedState, "mcp_info_json").As(&got); err != nil || got != test.want {
				t.Fatalf("planned document=%q err=%v", got, err)
			}
			generation := protocolMCPInt64(t, protocolMCPAttribute(t, schema, planned.PlannedState, "mcp_info_ownership_generation"))
			if generation != 1 {
				t.Fatalf("generation=%d", generation)
			}
			var id string
			if err := protocolMCPAttribute(t, schema, planned.PlannedState, "id").As(&id); err != nil || id != "stage2" {
				t.Fatalf("id churned: %q %v", id, err)
			}
			pending, diagnostics := readPendingMCPInfoProvenance(context.Background(), protocolPrivateMapFromBytes(t, planned.PlannedPrivate), emptyMCPInfoProvenance())
			if diagnostics.HasError() || pending.Mode != mcpInfoModeWhole || pending.Generation != 1 {
				t.Fatalf("pending=%#v diagnostics=%v", pending, diagnostics)
			}
		})
	}
}

func protocolPrivateMapFromBytes(t *testing.T, raw []byte) mcpInfoMapPrivate {
	t.Helper()
	values := mcpInfoMapPrivate{}
	if err := json.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestMCPInfoStage2PendingOwnershipSurvivesOrdinaryReadProtocol(t *testing.T) {
	protocolServer, schema, closeServer := protocolMCPStage2Harness(t)
	defer closeServer()
	state := protocolMCPStage2State(t, schema, `{"owner":{"team":"security"}}`, 2)
	committed := emptyMCPInfoProvenance()
	committed.Generation = 2
	privateMap := mcpInfoMapPrivate{}
	if diagnostics := writeMCPInfoProvenance(context.Background(), privateMap, committed); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	pending := cloneMCPInfoProvenance(committed)
	pending.Generation = 3
	pending.Mode = mcpInfoModeSelective
	pending.Overrides["/owner/team"] = true
	if diagnostics := writePendingMCPInfoProvenance(context.Background(), privateMap, pending); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	private, _ := json.Marshal(map[string][]byte(privateMap))
	read, err := protocolServer.ReadResource(context.Background(), &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: state, Private: private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	gotPending, diagnostics := readPendingMCPInfoProvenance(context.Background(), protocolPrivateMapFromBytes(t, read.Private), committed)
	if diagnostics.HasError() || gotPending.Generation != 3 || !gotPending.Overrides["/owner/team"] {
		t.Fatalf("pending ownership did not survive read: %#v %v", gotPending, diagnostics)
	}
}

func TestMCPInfoStage2ModifyPlanSelectiveNestedNullAndUnknownProtocol(t *testing.T) {
	protocolServer, schema, closeServer := protocolMCPStage2Harness(t)
	defer closeServer()
	state := protocolMCPStage2State(t, schema, `{"owner":{"team":"security","contact":"old"},"keep":true}`, 4)
	committed := emptyMCPInfoProvenance()
	committed.Generation = 4
	private := protocolMCPV2Private(t, committed)
	config := protocolMCPStage2Config(t, schema, map[string]interface{}{"mcp_info_overrides_json": `{"owner":{"contact":null}}`})
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"mcp_info_overrides_json": `{"owner":{"contact":null}}`, "mcp_info_json": tftypes.UnknownValue, "mcp_info_ownership_generation": tftypes.UnknownValue})
	planned, err := protocolServer.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	var document string
	_ = protocolMCPAttribute(t, schema, planned.PlannedState, "mcp_info_json").As(&document)
	if document != `{"keep":true,"owner":{"contact":null,"team":"security"}}` {
		t.Fatalf("effective document = %s", document)
	}
	generation := protocolMCPInt64(t, protocolMCPAttribute(t, schema, planned.PlannedState, "mcp_info_ownership_generation"))
	if generation != 5 {
		t.Fatalf("generation=%d", generation)
	}

	whole := cloneMCPInfoProvenance(committed)
	whole.Mode = mcpInfoModeWhole
	unknownPrivate := protocolMCPV2Private(t, whole)
	unknownConfig := protocolMCPStage2Config(t, schema, map[string]interface{}{"mcp_info_json": tftypes.UnknownValue})
	unknownProposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"mcp_info_json": tftypes.UnknownValue, "mcp_info_ownership_generation": tftypes.UnknownValue})
	unknown, err := protocolServer.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: unknownConfig, PriorState: state, ProposedNewState: unknownProposed, PriorPrivate: unknownPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(unknown.Diagnostics) {
		t.Fatalf("unknown err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(unknown.Diagnostics))
	}
	generation = protocolMCPInt64(t, protocolMCPAttribute(t, schema, unknown.PlannedState, "mcp_info_ownership_generation"))
	if generation != 4 {
		t.Fatalf("unknown generation=%d", generation)
	}
	var id string
	_ = protocolMCPAttribute(t, schema, unknown.PlannedState, "id").As(&id)
	if id != "stage2" {
		t.Fatalf("unknown configuration churned id: %q", id)
	}
}
