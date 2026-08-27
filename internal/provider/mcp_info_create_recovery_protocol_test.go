package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestMCPInfoCommittedUnconfirmedCreateRetainsPendingOwnershipProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			_, _ = writer.Write([]byte(`{"server_id":"pending-create"}`))
			return
		}
		http.Error(writer, `{"error":"unavailable"}`, http.StatusInternalServerError)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, map[string]interface{}{
		"server_name": "pending", "url": "https://mcp.example.test", "transport": "http", "mcp_info_json": `{"access":false}`,
	})
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("unconfirmed create: %v %s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	assertMCPServerIdentityOnlyState(t, schema, applied.NewState, "pending-create")
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	if !attributes["mcp_info_json"].IsNull() || protocolMCPInt64(t, attributes["mcp_info_ownership_generation"]) != 0 {
		t.Fatal("unconfirmed planned MCP info was published")
	}
	private := protocolPrivateMapFromBytes(t, applied.Private)
	committed, pending, validationErr := validateMCPInfoPrivateBundle(map[string][]byte(private))
	if validationErr != nil || pending == nil || pending.Mode != mcpInfoModeWhole || pending.Generation != 1 || committed.Mode != mcpInfoModeNone {
		t.Fatalf("recovery provenance: committed=%#v pending=%#v err=%v", committed, pending, validationErr)
	}
}
