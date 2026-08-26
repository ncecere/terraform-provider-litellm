package provider

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func protocolMCPFloat(t *testing.T, value interface{ As(interface{}) error }) float64 {
	t.Helper()
	var number big.Float
	if err := value.As(&number); err != nil {
		t.Fatal(err)
	}
	result, _ := number.Float64()
	return result
}

func TestMCPServerImportedCostsSurviveExactSiblingConfigurationProtocol(t *testing.T) {
	tests := []struct {
		name            string
		strings         map[string]interface{}
		costs           map[string]interface{}
		terraformOwned  mcpInfoLeafSet
		apiOwned        mcpInfoLeafSet
		wantDescription bool
		wantDefault     bool
		wantTools       bool
	}{
		{
			name: "configured string siblings preserve both imported costs",
			strings: map[string]interface{}{
				"server_name": "configured nested name", "description": "configured description", "logo_url": "https://mcp.example.test/configured.svg",
			},
			terraformOwned:  mcpInfoLeafSet{mcpInfoServerNameLeaf: true, mcpInfoDescriptionLeaf: true, mcpInfoLogoURLLeaf: true},
			apiOwned:        mcpInfoLeafSet{mcpInfoDefaultCostLeaf: true, mcpInfoToolCostsLeaf: true},
			wantDescription: true,
			wantDefault:     true,
			wantTools:       true,
		},
		{
			name:           "configured equal scalar takes over while imported map survives",
			strings:        map[string]interface{}{},
			costs:          map[string]interface{}{"default_cost_per_query": 1.25},
			terraformOwned: mcpInfoLeafSet{mcpInfoDefaultCostLeaf: true},
			apiOwned:       mcpInfoLeafSet{mcpInfoToolCostsLeaf: true},
			wantDefault:    true,
			wantTools:      true,
		},
		{
			name:           "null scalar sibling preserves imported scalar while equal map takes over",
			strings:        map[string]interface{}{},
			costs:          map[string]interface{}{"tool_name_to_cost_per_query": map[string]float64{"search": 2.5}},
			terraformOwned: mcpInfoLeafSet{mcpInfoToolCostsLeaf: true},
			apiOwned:       mcpInfoLeafSet{mcpInfoDefaultCostLeaf: true},
			wantDefault:    true,
			wantTools:      true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var mu sync.Mutex
			var putBody map[string]interface{}
			remoteInfo := map[string]interface{}{
				"mcp_server_cost_info": map[string]interface{}{
					"default_cost_per_query":      1.25,
					"tool_name_to_cost_per_query": map[string]interface{}{"search": 2.5},
				},
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if request.Method == http.MethodPut {
					if err := json.NewDecoder(request.Body).Decode(&putBody); err != nil {
						t.Error(err)
					}
					if info, ok := putBody["mcp_info"].(map[string]interface{}); ok {
						remoteInfo = info
					}
				}
				writer.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{
					"server_id": "cost-siblings", "server_name": "top-level", "url": "https://mcp.example.test", "transport": "http", "mcp_info": remoteInfo,
				})
			}))
			defer server.Close()

			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			const typeName = "litellm_mcp_server"
			schema := schemas.ResourceSchemas[typeName]
			priorInfo := mcpInfoProtocolValue(t, schema, map[string]interface{}{}, map[string]interface{}{
				"default_cost_per_query": 1.25, "tool_name_to_cost_per_query": map[string]float64{"search": 2.5},
			})
			state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
				"id": "cost-siblings", "server_id": "cost-siblings", "server_name": "top-level", "url": "https://mcp.example.test", "transport": "http", "mcp_info": priorInfo,
			}))
			private := protocolMCPPrivate(t, mcpInfoLeafSet{}, mcpInfoLeafSet{mcpInfoDefaultCostLeaf: true, mcpInfoToolCostsLeaf: true})
			configuredInfo := mcpInfoProtocolValue(t, schema, test.strings, test.costs)
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
				"server_name": "top-level", "url": "https://mcp.example.test", "transport": "http", "mcp_info": configuredInfo,
			}))
			proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"mcp_info": configuredInfo})

			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
			}
			plannedCosts := protocolNestedAttribute(t, protocolAttributeMap(t, schema, planned.PlannedState)["mcp_info"], "mcp_server_cost_info")
			if got := protocolMCPFloat(t, protocolNestedAttribute(t, plannedCosts, "default_cost_per_query")); got != 1.25 {
				t.Fatalf("planned default cost=%v", got)
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: typeName, Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("apply: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
			}
			mu.Lock()
			body := putBody
			mu.Unlock()
			info, ok := body["mcp_info"].(map[string]interface{})
			if !ok {
				t.Fatalf("update omitted mcp_info: %#v", body)
			}
			wireCosts, ok := info["mcp_server_cost_info"].(map[string]interface{})
			if !ok || (test.wantDefault && wireCosts["default_cost_per_query"] != 1.25) {
				t.Fatalf("update lost scalar cost: %#v", body)
			}
			if _, ok := wireCosts["tool_name_to_cost_per_query"].(map[string]interface{}); test.wantTools && !ok {
				t.Fatalf("update lost map cost: %#v", body)
			}
			if test.wantDescription && info["description"] != "configured description" {
				t.Fatalf("configured string sibling was not preserved: %#v", body)
			}

			terraformOwned := protocolPrivateMCPLeafSet(t, applied.Private, mcpInfoTerraformOwnedPrivateKey, mcpInfoAllLeaves)
			apiOwned := protocolPrivateMCPLeafSet(t, applied.Private, mcpInfoAPIOwnedPrivateKey, mcpInfoCostLeaves)
			if !mcpInfoLeafSetsEqual(terraformOwned, test.terraformOwned) || !mcpInfoLeafSetsEqual(apiOwned, test.apiOwned) {
				t.Fatalf("ownership after apply: terraform=%#v api=%#v", terraformOwned, apiOwned)
			}

			read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: applied.NewState, Private: applied.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
			}
			steadyProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"mcp_info": configuredInfo})
			steady, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName: typeName, Config: config, PriorState: read.NewState, ProposedNewState: steadyProposed, PriorPrivate: read.Private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, steady) != organizationProjectProtocolActionNoOp {
				t.Fatalf("post-read drift: err=%v diagnostics=%s action=%s", err, agentProtocolDiagnosticsText(steady.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, read.NewState, steady))
			}
		})
	}
}
