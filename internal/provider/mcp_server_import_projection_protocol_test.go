package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func mcpInfoProtocolValue(t *testing.T, schema *tfprotov6.Schema, strings map[string]interface{}, costs map[string]interface{}) map[string]tftypes.Value {
	t.Helper()
	root := schema.ValueType().(tftypes.Object)
	infoType := root.AttributeTypes["mcp_info"].(tftypes.Object)
	info := make(map[string]tftypes.Value, len(infoType.AttributeTypes))
	for name, valueType := range infoType.AttributeTypes {
		value := interface{}(nil)
		if configured, ok := strings[name]; ok {
			value = configured
		}
		if name == "mcp_server_cost_info" && costs != nil {
			costType := valueType.(tftypes.Object)
			cost := make(map[string]tftypes.Value, len(costType.AttributeTypes))
			for costName, costValueType := range costType.AttributeTypes {
				costValue := interface{}(nil)
				if configured, ok := costs[costName]; ok {
					costValue = configured
					if configuredMap, ok := configured.(map[string]float64); ok {
						typed := make(map[string]tftypes.Value, len(configuredMap))
						for key, number := range configuredMap {
							typed[key] = tftypes.NewValue(tftypes.Number, big.NewFloat(number))
						}
						costValue = typed
					}
				}
				cost[costName] = tftypes.NewValue(costValueType, costValue)
			}
			value = cost
		}
		info[name] = tftypes.NewValue(valueType, value)
	}
	return info
}

func protocolPrivateValue(t *testing.T, private []byte, key string) []byte {
	t.Helper()
	values := map[string][]byte{}
	if err := json.Unmarshal(private, &values); err != nil {
		t.Fatalf("decode protocol private state: %v", err)
	}
	return values[key]
}

func protocolPrivateMCPLeafSet(t *testing.T, private []byte, key string, allowed []string) mcpInfoLeafSet {
	t.Helper()
	fields, err := decodeMCPInfoLeafSet(protocolPrivateValue(t, private, key), allowed)
	if err != nil {
		t.Fatal(err)
	}
	return fields
}

func protocolMCPPrivate(t *testing.T, terraformOwned, apiOwned mcpInfoLeafSet) []byte {
	t.Helper()
	private, err := json.Marshal(map[string][]byte{
		mcpInfoOwnershipVersionKey:      []byte(mcpInfoOwnershipVersion),
		mcpInfoTerraformOwnedPrivateKey: encodeMCPInfoLeafSet(terraformOwned),
		mcpInfoAPIOwnedPrivateKey:       encodeMCPInfoLeafSet(apiOwned),
	})
	if err != nil {
		t.Fatal(err)
	}
	return private
}

func protocolString(t *testing.T, value tftypes.Value) string {
	t.Helper()
	var result string
	if err := value.As(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestMCPServerImportProjectionIsImmediatelyAndRepeatedlyStableProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"server_id":"mcp-import","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"server_name":"nested-api","description":"api description","logo_url":"https://mcp.example.test/logo.svg"}}`))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "mcp-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	resourceState := imported.ImportedResources[0]
	first, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: resourceState.State, Private: resourceState.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(first.Diagnostics) {
		t.Fatalf("first import read: err=%v diagnostics=%v", err, first.Diagnostics)
	}
	if !protocolAttributeMap(t, schema, first.NewState)["mcp_info"].IsNull() {
		t.Fatal("API metadata created an unconfigured mcp_info block on import")
	}
	if protocolPrivateHasKey(t, first.Private, numericImportedPrivateKey) {
		t.Fatal("successful authoritative read retained the numeric import marker")
	}
	second, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: first.NewState, Private: first.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(second.Diagnostics) {
		t.Fatalf("repeated read: err=%v diagnostics=%v", err, second.Diagnostics)
	}
	firstValue, _ := first.NewState.Unmarshal(schema.ValueType())
	secondValue, _ := second.NewState.Unmarshal(schema.ValueType())
	if !firstValue.Equal(secondValue) {
		t.Fatal("repeated read adopted nested API metadata")
	}

	// Model the configuration-aware state after schema defaults and computed
	// empty collections have converged. An omitted mcp_info block must not be the
	// source of an ordinary update plan merely because the API returns metadata.
	emptyList := []tftypes.Value{}
	emptyMap := map[string]tftypes.Value{}
	steadyState := organizationProjectProtocolReplace(t, schema, second.NewState, map[string]interface{}{
		"auth_type": "none", "spec_version": "2024-11-05",
		"mcp_access_groups": emptyList, "args": emptyList, "allowed_tools": emptyList, "extra_headers": emptyList,
		"env": emptyMap, "credentials": emptyMap, "static_headers": emptyMap,
	})
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"server_name": "top-level", "transport": "http", "url": "https://mcp.example.test",
	}))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: steadyState, ProposedNewState: steadyState, PriorPrivate: second.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || len(planned.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, steadyState, planned) != organizationProjectProtocolActionNoOp {
		t.Fatalf("omitted-block plan: err=%v diagnostics=%v replace=%v action=%s", err, planned.Diagnostics, planned.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, steadyState, planned))
	}
}

func TestMCPServerImportProjectionAdoptsOnlyVisibleCostsProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"server_id":"mcp-cost","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"server_name":"nested-api","description":"api description","logo_url":"https://mcp.example.test/logo.svg","mcp_server_cost_info":{"default_cost_per_query":0.125,"tool_name_to_cost_per_query":{"search":0.25}}}}`))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "mcp-cost"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	first, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(first.Diagnostics) {
		t.Fatalf("first read: err=%v diagnostics=%v", err, first.Diagnostics)
	}
	info := protocolAttributeMap(t, schema, first.NewState)["mcp_info"]
	for _, field := range []string{"server_name", "description", "logo_url"} {
		if !protocolNestedAttribute(t, info, field).IsNull() {
			t.Fatalf("cost import adopted unowned mcp_info.%s", field)
		}
	}
	costInfo := protocolNestedAttribute(t, info, "mcp_server_cost_info")
	costs := map[string]tftypes.Value{}
	if err := costInfo.As(&costs); err != nil {
		t.Fatal(err)
	}
	var defaultCost big.Float
	if err := costs["default_cost_per_query"].As(&defaultCost); err != nil {
		t.Fatal(err)
	}
	gotDefault, _ := defaultCost.Float64()
	if gotDefault != 0.125 {
		t.Fatalf("default cost=%v", gotDefault)
	}
	toolCosts := map[string]tftypes.Value{}
	if err := costs["tool_name_to_cost_per_query"].As(&toolCosts); err != nil {
		t.Fatal(err)
	}
	var searchCost big.Float
	if err := toolCosts["search"].As(&searchCost); err != nil {
		t.Fatal(err)
	}
	gotSearch, _ := searchCost.Float64()
	if gotSearch != 0.25 {
		t.Fatalf("search cost=%v", gotSearch)
	}

	second, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: first.NewState, Private: first.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(second.Diagnostics) {
		t.Fatalf("second read: err=%v diagnostics=%v", err, second.Diagnostics)
	}
	firstValue, _ := first.NewState.Unmarshal(schema.ValueType())
	secondValue, _ := second.NewState.Unmarshal(schema.ValueType())
	if !firstValue.Equal(secondValue) {
		t.Fatal("cost-only import adopted nested strings on a later read")
	}

	emptyList := []tftypes.Value{}
	emptyMap := map[string]tftypes.Value{}
	steadyState := organizationProjectProtocolReplace(t, schema, second.NewState, map[string]interface{}{
		"auth_type": "none", "spec_version": "2024-11-05",
		"mcp_access_groups": emptyList, "args": emptyList, "allowed_tools": emptyList, "extra_headers": emptyList,
		"env": emptyMap, "credentials": emptyMap, "static_headers": emptyMap,
	})
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"server_name": "top-level", "transport": "http", "url": "https://mcp.example.test",
	}))
	// Terraform's real ProposedNewState nulls an omitted Optional block even
	// though prior state contains the API-owned cost shell.
	proposed := organizationProjectProtocolReplace(t, schema, steadyState, map[string]interface{}{"mcp_info": nil})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: steadyState, ProposedNewState: proposed, PriorPrivate: second.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || len(planned.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, steadyState, planned) != organizationProjectProtocolActionNoOp {
		priorAttrs := protocolAttributeMap(t, schema, steadyState)
		plannedAttrs := protocolAttributeMap(t, schema, planned.PlannedState)
		for name, priorValue := range priorAttrs {
			if !priorValue.Equal(plannedAttrs[name]) {
				t.Logf("changed %s: prior=%s planned=%s", name, priorValue, plannedAttrs[name])
			}
		}
		t.Fatalf("cost-only omitted-block plan: err=%v diagnostics=%v replace=%v action=%s", err, planned.Diagnostics, planned.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, steadyState, planned))
	}
}

func TestMCPServerNestedStringOwnershipTriStateProtocol(t *testing.T) {
	ctx := context.Background()
	var payload atomic.Value
	payload.Store(`{"server_id":"mcp-owned","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"server_name":"remote name","description":"remote description","logo_url":"https://mcp.example.test/remote.svg"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(payload.Load().(string)))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "mcp-owned", "server_id": "mcp-owned", "server_name": "top-level", "url": "https://mcp.example.test", "transport": "http",
		"mcp_info": mcpInfoProtocolValue(t, schema, map[string]interface{}{
			"server_name": "configured name", "description": "configured description", "logo_url": "https://mcp.example.test/configured.svg",
		}, nil),
	}))
	private, err := json.Marshal(map[string][]byte{
		mcpInfoOwnershipVersionKey:      []byte(mcpInfoOwnershipVersion),
		mcpInfoTerraformOwnedPrivateKey: encodeMCPInfoLeafSet(mcpInfoLeafSet{mcpInfoServerNameLeaf: true, mcpInfoDescriptionLeaf: true, mcpInfoLogoURLLeaf: true}),
		mcpInfoAPIOwnedPrivateKey:       []byte(`[]`),
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func(prior *tfprotov6.DynamicValue) *tfprotov6.ReadResourceResponse {
		t.Helper()
		response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: prior, Private: private})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
			t.Fatalf("read: err=%v diagnostics=%v", err, response.Diagnostics)
		}
		return response
	}

	reconciled := read(state)
	info := protocolAttributeMap(t, schema, reconciled.NewState)["mcp_info"]
	if got := protocolString(t, protocolNestedAttribute(t, info, "server_name")); got != "remote name" {
		t.Fatalf("configured server_name=%q", got)
	}
	if got := protocolString(t, protocolNestedAttribute(t, info, "description")); got != "remote description" {
		t.Fatalf("configured description=%q", got)
	}

	payload.Store(`{"server_id":"mcp-owned","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"description":null}}`)
	cleared := read(reconciled.NewState)
	clearedInfo := protocolAttributeMap(t, schema, cleared.NewState)["mcp_info"]
	if got := protocolString(t, protocolNestedAttribute(t, clearedInfo, "server_name")); got != "remote name" {
		t.Fatalf("omitted owned server_name=%q, want preserved", got)
	}
	if !protocolNestedAttribute(t, clearedInfo, "description").IsNull() {
		t.Fatal("explicit null did not clear the owned description")
	}
	if got := protocolString(t, protocolNestedAttribute(t, clearedInfo, "logo_url")); got != "https://mcp.example.test/remote.svg" {
		t.Fatalf("omitted owned logo_url=%q, want preserved", got)
	}

	payload.Store(`{"server_id":"mcp-owned","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"description":"reappeared"}}`)
	final := read(cleared.NewState)
	if got := protocolString(t, protocolNestedAttribute(t, protocolAttributeMap(t, schema, final.NewState)["mcp_info"], "description")); got != "reappeared" {
		t.Fatalf("owned description did not reappear after authoritative null: %q", got)
	}
}

func TestMCPServerMalformedInfoPreservesImportMarkerAndStateProtocol(t *testing.T) {
	ctx := context.Background()
	var payload atomic.Value
	payload.Store(`{"server_id":"mcp-malformed","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":"malformed"}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(payload.Load().(string)))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "mcp-malformed"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	prior := imported.ImportedResources[0]
	for _, malformed := range []string{
		`{"server_id":"mcp-malformed","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":"malformed"}`,
		`{"server_id":"mcp-malformed","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"server_name":42}}`,
		`{"server_id":"mcp-malformed","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"mcp_server_cost_info":[]}}`,
	} {
		payload.Store(malformed)
		failed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: prior.State, Private: prior.Private})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) {
			t.Fatalf("malformed mcp_info accepted: err=%v diagnostics=%v", err, failed.Diagnostics)
		}
		if !protocolPrivateHasKey(t, failed.Private, numericImportedPrivateKey) || !protocolPrivateHasKey(t, failed.Private, mcpInfoOwnershipVersionKey) {
			t.Fatal("failed import read lost its private markers")
		}
		for _, key := range []string{numericImportedPrivateKey, mcpInfoOwnershipVersionKey, mcpInfoTerraformOwnedPrivateKey, mcpInfoAPIOwnedPrivateKey} {
			if string(protocolPrivateValue(t, failed.Private, key)) != string(protocolPrivateValue(t, prior.Private, key)) {
				t.Fatalf("failed import read changed private provenance key %q", key)
			}
		}
		if strings.Contains(fmt.Sprint(failed.Diagnostics), "mcp-malformed") {
			t.Fatal("failed import diagnostic exposed the resource identifier")
		}
		priorValue, _ := prior.State.Unmarshal(schema.ValueType())
		failedValue, _ := failed.NewState.Unmarshal(schema.ValueType())
		if !priorValue.Equal(failedValue) {
			t.Fatal("failed import read changed public state")
		}
	}
}

func TestMCPServerImportThenEqualNestedStringOwnershipTransferProtocol(t *testing.T) {
	ctx := context.Background()
	var puts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPut {
			puts.Add(1)
			var body map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			info, _ := body["mcp_info"].(map[string]interface{})
			if info["server_name"] != "nested-api" {
				t.Errorf("ownership payload=%#v", body)
			}
		}
		_, _ = writer.Write([]byte(`{"server_id":"mcp-transfer","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"server_name":"nested-api"}}`))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "mcp-transfer"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	if !protocolAttributeMap(t, schema, read.NewState)["mcp_info"].IsNull() {
		t.Fatal("import unexpectedly owned nested server_name")
	}

	info := mcpInfoProtocolValue(t, schema, map[string]interface{}{"server_name": "nested-api"}, nil)
	configValues := map[string]interface{}{
		"server_name": "top-level", "url": "https://mcp.example.test", "transport": "http", "mcp_info": info,
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"mcp_info": info})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: read.NewState, ProposedNewState: proposed, PriorPrivate: read.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || len(planned.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned) != organizationProjectProtocolActionUpdate {
		t.Fatalf("ownership plan: err=%v diagnostics=%v replace=%v action=%s", err, planned.Diagnostics, planned.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: read.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || puts.Load() != 1 {
		t.Fatalf("ownership apply: err=%v diagnostics=%v puts=%d", err, applied.Diagnostics, puts.Load())
	}
	ownedInfo := protocolAttributeMap(t, schema, applied.NewState)["mcp_info"]
	if got := protocolString(t, protocolNestedAttribute(t, ownedInfo, "server_name")); got != "nested-api" {
		t.Fatalf("owned server_name=%q", got)
	}
	terraformOwned := protocolPrivateMCPLeafSet(t, applied.Private, mcpInfoTerraformOwnedPrivateKey, mcpInfoAllLeaves)
	if !terraformOwned[mcpInfoServerNameLeaf] || protocolPrivateMCPLeafSet(t, applied.Private, mcpInfoAPIOwnedPrivateKey, mcpInfoCostLeaves)[mcpInfoServerNameLeaf] {
		t.Fatal("equal-value apply did not commit exact Terraform string ownership")
	}
}

func TestMCPServerLegacyUnconfiguredStringNeverBecomesOwnedProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"server_id":"legacy","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"server_name":"remote-changed"}}`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "legacy", "server_id": "legacy", "server_name": "top-level", "url": "https://mcp.example.test", "transport": "http",
		"mcp_info": mcpInfoProtocolValue(t, schema, map[string]interface{}{"server_name": "legacy-imported"}, nil),
	}))
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: state})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("legacy read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	got := protocolString(t, protocolNestedAttribute(t, protocolAttributeMap(t, schema, read.NewState)["mcp_info"], "server_name"))
	if got != "legacy-imported" {
		t.Fatalf("legacy unconfigured string followed API change: %q", got)
	}
	if protocolPrivateHasKey(t, read.Private, mcpInfoOwnershipVersionKey) {
		t.Fatal("configuration-blind read fabricated legacy string provenance")
	}
}

func TestMCPServerImportedNumericNullThenReappearsAndChangesProtocol(t *testing.T) {
	ctx := context.Background()
	var payload atomic.Value
	payload.Store(`{"server_id":"cost-cycle","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"mcp_server_cost_info":{"default_cost_per_query":1.25,"tool_name_to_cost_per_query":{"search":2.5}}}}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(payload.Load().(string)))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "cost-cycle"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read := func(state *tfprotov6.DynamicValue, private []byte) *tfprotov6.ReadResourceResponse {
		t.Helper()
		response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: state, Private: private})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
			t.Fatalf("read: err=%v diagnostics=%v", err, response.Diagnostics)
		}
		return response
	}
	first := read(imported.ImportedResources[0].State, imported.ImportedResources[0].Private)
	apiOwned := protocolPrivateMCPLeafSet(t, first.Private, mcpInfoAPIOwnedPrivateKey, mcpInfoCostLeaves)
	if !apiOwned[mcpInfoDefaultCostLeaf] || !apiOwned[mcpInfoToolCostsLeaf] {
		t.Fatal("first import read did not persist exact visible API cost ownership")
	}
	payload.Store(`{"server_id":"cost-cycle","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"mcp_server_cost_info":{"default_cost_per_query":null,"tool_name_to_cost_per_query":null}}}`)
	cleared := read(first.NewState, first.Private)
	clearedCosts := protocolNestedAttribute(t, protocolAttributeMap(t, schema, cleared.NewState)["mcp_info"], "mcp_server_cost_info")
	if !protocolNestedAttribute(t, clearedCosts, "default_cost_per_query").IsNull() || !protocolNestedAttribute(t, clearedCosts, "tool_name_to_cost_per_query").IsNull() {
		t.Fatal("authoritative numeric child null did not clear public cost leaves")
	}
	payload.Store(`{"server_id":"cost-cycle","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"mcp_server_cost_info":{"default_cost_per_query":3.75,"tool_name_to_cost_per_query":{"search":4.5}}}}`)
	reappeared := read(cleared.NewState, cleared.Private)
	costs := protocolNestedAttribute(t, protocolAttributeMap(t, schema, reappeared.NewState)["mcp_info"], "mcp_server_cost_info")
	var number big.Float
	if err := protocolNestedAttribute(t, costs, "default_cost_per_query").As(&number); err != nil {
		t.Fatal(err)
	}
	got, _ := number.Float64()
	if got != 3.75 {
		t.Fatalf("API-owned numeric leaf did not reappear/follow change: %v", got)
	}
}

func TestMCPServerParentRedactionPreservesEveryOwnedLeafProtocol(t *testing.T) {
	ctx := context.Background()
	var payload atomic.Value
	payload.Store(`{"server_id":"redacted","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":null}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(payload.Load().(string)))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	info := mcpInfoProtocolValue(t, schema, map[string]interface{}{
		"server_name": "owned-name", "description": "owned-description", "logo_url": "https://mcp.example.test/logo.svg",
	}, map[string]interface{}{"default_cost_per_query": 1.5, "tool_name_to_cost_per_query": map[string]float64{"search": 2.5}})
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "redacted", "server_id": "redacted", "server_name": "top-level", "url": "https://mcp.example.test", "transport": "http", "mcp_info": info,
	}))
	private := protocolMCPPrivate(t,
		mcpInfoLeafSet{mcpInfoServerNameLeaf: true, mcpInfoDescriptionLeaf: true, mcpInfoLogoURLLeaf: true, mcpInfoDefaultCostLeaf: true},
		mcpInfoLeafSet{mcpInfoToolCostsLeaf: true},
	)
	read := func(prior *tfprotov6.DynamicValue) *tfprotov6.ReadResourceResponse {
		t.Helper()
		response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: prior, Private: private})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
			t.Fatalf("redacted read: err=%v diagnostics=%v", err, response.Diagnostics)
		}
		return response
	}
	parentNull := read(state)
	before, _ := state.Unmarshal(schema.ValueType())
	after, _ := parentNull.NewState.Unmarshal(schema.ValueType())
	if !before.Equal(after) {
		t.Fatal("null role-sanitized parent changed an owned/API leaf")
	}
	payload.Store(`{"server_id":"redacted","server_name":"top-level","url":"https://mcp.example.test","transport":"http"}`)
	parentAbsent := read(parentNull.NewState)
	afterAbsent, _ := parentAbsent.NewState.Unmarshal(schema.ValueType())
	if !after.Equal(afterAbsent) {
		t.Fatal("absent role-sanitized parent changed an owned/API leaf")
	}
}

func TestMCPServerHCLRemovalRelinquishesExactOwnershipProtocol(t *testing.T) {
	ctx := context.Background()
	var puts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPut {
			puts.Add(1)
			var body map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if _, present := body["mcp_info"]; present {
				t.Fatalf("HCL removal sent a stale mcp_info value: %#v", body)
			}
		}
		_, _ = writer.Write([]byte(`{"server_id":"remove-owned","server_name":"top-level","url":"https://mcp.example.test","transport":"http"}`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "remove-owned", "server_id": "remove-owned", "server_name": "top-level", "url": "https://mcp.example.test", "transport": "http",
		"mcp_info": mcpInfoProtocolValue(t, schema, map[string]interface{}{"description": "managed"}, nil),
	}))
	private := protocolMCPPrivate(t, mcpInfoLeafSet{mcpInfoDescriptionLeaf: true}, mcpInfoLeafSet{})
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"server_name": "top-level", "url": "https://mcp.example.test", "transport": "http",
	}))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"mcp_info": nil})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, state, planned) != organizationProjectProtocolActionUpdate {
		t.Fatalf("removal plan: err=%v diagnostics=%v action=%s", err, planned.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, state, planned))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || puts.Load() != 1 {
		t.Fatalf("removal apply: err=%v diagnostics=%v puts=%d", err, applied.Diagnostics, puts.Load())
	}
	if !protocolAttributeMap(t, schema, applied.NewState)["mcp_info"].IsNull() {
		t.Fatal("HCL removal retained a public mcp_info shell without API-owned costs")
	}
	if len(protocolPrivateMCPLeafSet(t, applied.Private, mcpInfoTerraformOwnedPrivateKey, mcpInfoAllLeaves)) != 0 {
		t.Fatal("HCL removal retained Terraform ownership")
	}
}

func TestMCPServerUnknownConfigRetainsPriorProvenanceProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"server_id":"unknown-owned","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"description":"managed"}}`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	knownInfo := mcpInfoProtocolValue(t, schema, map[string]interface{}{"description": "managed"}, nil)
	unknownInfo := mcpInfoProtocolValue(t, schema, map[string]interface{}{"description": tftypes.UnknownValue}, nil)
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "unknown-owned", "server_id": "unknown-owned", "server_name": "top-level", "url": "https://mcp.example.test", "transport": "http", "mcp_info": knownInfo,
	}))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"server_name": "top-level", "url": "https://mcp.example.test", "transport": "http", "mcp_info": unknownInfo,
	}))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"mcp_info": unknownInfo})
	private := protocolMCPPrivate(t, mcpInfoLeafSet{mcpInfoDescriptionLeaf: true}, mcpInfoLeafSet{})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("unknown plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	pending := protocolPrivateMCPLeafSet(t, planned.PlannedPrivate, mcpInfoPendingTerraformKey, mcpInfoAllLeaves)
	if !pending[mcpInfoDescriptionLeaf] || len(pending) != 1 {
		t.Fatalf("unknown config guessed different ownership: %#v", pending)
	}
}

func TestMCPServerUpdateFailureRetainsPublicAndCommittedPrivateProtocol(t *testing.T) {
	ctx := context.Background()
	const sensitiveID = "sensitive-id-254"
	const sensitiveValue = "sensitive-value-254"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			http.Error(writer, "rejected", http.StatusInternalServerError)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"server_id":"sensitive-id-254","server_name":"top-level","url":"https://mcp.example.test","transport":"http","mcp_info":{"description":"old"}}`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	oldInfo := mcpInfoProtocolValue(t, schema, map[string]interface{}{"description": "old"}, nil)
	newInfo := mcpInfoProtocolValue(t, schema, map[string]interface{}{"description": sensitiveValue}, nil)
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": sensitiveID, "server_id": sensitiveID, "server_name": "top-level", "url": "https://mcp.example.test", "transport": "http", "mcp_info": oldInfo,
	}))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"server_name": "top-level", "url": "https://mcp.example.test", "transport": "http", "mcp_info": newInfo,
	}))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"mcp_info": newInfo})
	private := protocolMCPPrivate(t, mcpInfoLeafSet{mcpInfoDescriptionLeaf: true}, mcpInfoLeafSet{})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("failure plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("failed update accepted: err=%v diagnostics=%v", err, applied.Diagnostics)
	}
	priorValue, _ := state.Unmarshal(schema.ValueType())
	failedValue, _ := applied.NewState.Unmarshal(schema.ValueType())
	if !priorValue.Equal(failedValue) {
		t.Fatal("failed update changed public state")
	}
	committed := protocolPrivateMCPLeafSet(t, applied.Private, mcpInfoTerraformOwnedPrivateKey, mcpInfoAllLeaves)
	if !committed[mcpInfoDescriptionLeaf] || len(committed) != 1 {
		t.Fatal("failed update changed committed ownership provenance")
	}
	diagnosticsText := fmt.Sprint(applied.Diagnostics)
	if strings.Contains(diagnosticsText, sensitiveID) || strings.Contains(diagnosticsText, sensitiveValue) {
		t.Fatal("failure diagnostics exposed a public value or identifier")
	}
}
