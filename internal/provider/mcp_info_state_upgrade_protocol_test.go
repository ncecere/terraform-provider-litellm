package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestMCPServerV1ToV2StateUpgradeProtocol(t *testing.T) {
	protocolServer, schema, closeServer := protocolMCPStage2Harness(t)
	defer closeServer()
	prior := map[string]interface{}{
		"id": "upgrade", "server_id": "upgrade", "server_name": "top", "transport": "http", "url": "https://mcp.example.test",
		"mcp_info": map[string]interface{}{
			"server_name": "nested", "description": "preserved", "logo_url": nil,
			"mcp_server_cost_info": map[string]interface{}{"default_cost_per_query": 1.25, "tool_name_to_cost_per_query": map[string]interface{}{"search": 2.5}},
		},
	}
	raw, err := json.Marshal(prior)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := protocolServer.UpgradeResourceState(context.Background(), &tfprotov6.UpgradeResourceStateRequest{TypeName: "litellm_mcp_server", Version: 1, RawState: &tfprotov6.RawState{JSON: raw}})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(upgraded.Diagnostics) {
		t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(upgraded.Diagnostics))
	}
	if upgraded.UpgradedState == nil {
		t.Fatal("missing upgraded state")
	}
	attributes := protocolAttributeMap(t, schema, upgraded.UpgradedState)
	for _, name := range []string{"mcp_info_json", "mcp_info_overrides_json", "mcp_info_clear_paths"} {
		if !attributes[name].IsNull() {
			t.Fatalf("%s is not typed null: %#v", name, attributes[name])
		}
	}
	if got := protocolMCPInt64(t, attributes["mcp_info_ownership_generation"]); got != 0 {
		t.Fatalf("generation=%d", got)
	}
	info := protocolNestedAttribute(t, attributes["mcp_info"], "description")
	var description string
	if err := info.As(&description); err != nil || description != "preserved" {
		t.Fatalf("fixed block changed: %q %v", description, err)
	}
}
