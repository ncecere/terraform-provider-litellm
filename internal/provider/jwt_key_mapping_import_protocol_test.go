package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestJWTKeyMappingEqualDescriptionTransferFailureRetainsAPIOwnershipProtocol(t *testing.T) {
	ctx := context.Background()
	var gets atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.URL.Path != jwtKeyMappingInfoPath {
			http.NotFound(w, r)
			return
		}
		if gets.Add(1) > 1 {
			http.Error(w, `{"detail":"role-secret"}`, http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(jwtMappingJSON(jwtMappingID1, "import-secret", "equal", true))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_jwt_key_mapping"
	schema := schemas.ResourceSchemas[typeName]
	imported, _ := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: jwtMappingID1})
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	config := jwtMappingProtocolValue(t, schema, map[string]interface{}{"jwt_claim_name": "sub", "jwt_claim_value": "import-secret", "description": "equal", "is_active": true})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: read.NewState, ProposedNewState: read.NewState, PriorPrivate: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || !protocolPrivateHasKey(t, planned.PlannedPrivate, jwtKeyMappingDescriptionPendingPrivateKey) {
		t.Fatalf("plan err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics), planned.PlannedPrivate)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: read.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || protocolPrivateHasKey(t, applied.Private, jwtKeyMappingDescriptionOwnedPrivateKey) || !protocolPrivateHasKey(t, applied.Private, jwtKeyMappingDescriptionPendingPrivateKey) {
		t.Fatalf("apply err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics), applied.Private)
	}
	if text := agentProtocolDiagnosticsText(applied.Diagnostics); strings.Contains(text, "role-secret") || strings.Contains(text, "import-secret") {
		t.Fatalf("diagnostic leaked response/config content: %s", text)
	}
}

func TestJWTKeyMappingImportAdoptsOmittedLeavesWithoutDriftProtocol(t *testing.T) {
	ctx := context.Background()
	var posts, gets atomic.Int64
	description := interface{}("api-owned-description")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == jwtKeyMappingInfoPath && r.URL.Query().Get("id") == jwtMappingID1:
			gets.Add(1)
			_ = json.NewEncoder(w).Encode(jwtMappingJSON(jwtMappingID1, "import-secret", description, false))
		case r.Method == http.MethodPost && r.URL.Path == jwtKeyMappingUpdatePath:
			posts.Add(1)
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if value, present := body["description"]; !present || value != nil {
				t.Errorf("clear body=%#v", body)
			}
			description = nil
			_ = json.NewEncoder(w).Encode(jwtMappingJSON(jwtMappingID1, "import-secret", description, false))
		default:
			http.NotFound(w, r)
		}
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

	explicitConfig := jwtMappingProtocolValue(t, schema, map[string]interface{}{"description": "api-owned-description"})
	equalPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: explicitConfig, PriorState: read.NewState, ProposedNewState: read.NewState, PriorPrivate: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(equalPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, equalPlan) != organizationProjectProtocolActionUpdate || !protocolPrivateHasKey(t, equalPlan.PlannedPrivate, jwtKeyMappingDescriptionPendingPrivateKey) {
		t.Fatalf("equal transfer plan err=%v diagnostics=%s action=%s private=%s", err, agentProtocolDiagnosticsText(equalPlan.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, read.NewState, equalPlan), equalPlan.PlannedPrivate)
	}
	equalApplied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: explicitConfig, PriorState: read.NewState, PlannedState: equalPlan.PlannedState, PlannedPrivate: equalPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(equalApplied.Diagnostics) || posts.Load() != 0 || !protocolPrivateHasKey(t, equalApplied.Private, jwtKeyMappingDescriptionOwnedPrivateKey) || protocolPrivateHasKey(t, equalApplied.Private, jwtKeyMappingDescriptionPendingPrivateKey) {
		t.Fatalf("equal transfer apply err=%v diagnostics=%s posts=%d private=%s", err, agentProtocolDiagnosticsText(equalApplied.Diagnostics), posts.Load(), equalApplied.Private)
	}

	clearConfig := jwtMappingProtocolValue(t, schema, map[string]interface{}{"description": nil})
	clearProposed := organizationProjectProtocolReplace(t, schema, equalApplied.NewState, map[string]interface{}{"description": nil})
	clearPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: clearConfig, PriorState: equalApplied.NewState, ProposedNewState: clearProposed, PriorPrivate: equalApplied.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(clearPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, equalApplied.NewState, clearPlan) != organizationProjectProtocolActionUpdate {
		t.Fatalf("clear plan err=%v diagnostics=%s action=%s", err, agentProtocolDiagnosticsText(clearPlan.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, equalApplied.NewState, clearPlan))
	}
	cleared, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: clearConfig, PriorState: equalApplied.NewState, PlannedState: clearPlan.PlannedState, PlannedPrivate: clearPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(cleared.Diagnostics) || posts.Load() != 1 || !protocolAttributeMap(t, schema, cleared.NewState)["description"].IsNull() {
		t.Fatalf("clear apply err=%v diagnostics=%s posts=%d", err, agentProtocolDiagnosticsText(cleared.Diagnostics), posts.Load())
	}
	if gets.Load() != 3 {
		t.Fatalf("authoritative reads=%d", gets.Load())
	}

	malformed, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "NOT-A-UUID/secret"})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(malformed.Diagnostics) {
		t.Fatalf("malformed import err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(malformed.Diagnostics))
	}
}
