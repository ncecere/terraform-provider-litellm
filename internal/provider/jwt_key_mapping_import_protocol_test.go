package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestJWTKeyMappingImportAdoptsOmittedLeavesWithoutDriftProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.URL.Path != jwtKeyMappingInfoPath || r.URL.Query().Get("id") != jwtMappingID1 {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(jwtMappingJSON(jwtMappingID1, "import-secret", "api-owned-description", false))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_jwt_key_mapping"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: jwtMappingID1})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	config := jwtMappingProtocolValue(t, schema, map[string]interface{}{})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: read.NewState, ProposedNewState: read.NewState, PriorPrivate: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned) != organizationProjectProtocolActionNoOp {
		t.Fatalf("no-drift plan err=%v diagnostics=%s action=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned))
	}

	malformed, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "NOT-A-UUID/secret"})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(malformed.Diagnostics) {
		t.Fatalf("malformed import err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(malformed.Diagnostics))
	}
}
