package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestModelImportedLimitOwnershipTransitionsBeforeRemovalReplacementProtocol(t *testing.T) {
	ctx := context.Background()
	var reads, patches atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/model/info":
			reads.Add(1)
			_, _ = fmt.Fprint(writer, `{"data":[{"model_name":"imported-model","litellm_params":{"custom_llm_provider":"anthropic","model":"anthropic/claude","tpm":100,"input_cost_per_pixel":0.25},"model_info":{"id":"model-import","base_model":"claude","tier":"free","mode":"chat"}}]}`)
		case request.Method == http.MethodPatch:
			patches.Add(1)
			_, _ = fmt.Fprint(writer, `{"status":"ok"}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_model"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "model-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
	}

	configValues := map[string]interface{}{
		"model_name": "imported-model", "custom_llm_provider": "anthropic", "base_model": "claude",
	}
	plan := func(values map[string]interface{}, proposed *tfprotov6.DynamicValue, prior *tfprotov6.DynamicValue, private []byte) *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
		response, planErr := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
			TypeName: typeName, Config: config, PriorState: prior, ProposedNewState: proposed, PriorPrivate: private,
		})
		if planErr != nil {
			t.Fatal(planErr)
		}
		return response
	}

	omittedProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"tpm": nil})
	omitted := plan(configValues, omittedProposed, read.NewState, read.Private)
	if accessGroupProtocolDiagnosticsHaveError(omitted.Diagnostics) || len(omitted.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted) != organizationProjectProtocolActionNoOp {
		priorAttributes := protocolAttributeMap(t, schema, read.NewState)
		plannedAttributes := protocolAttributeMap(t, schema, omitted.PlannedState)
		for name, priorValue := range priorAttributes {
			if plannedValue := plannedAttributes[name]; !priorValue.Equal(plannedValue) {
				t.Logf("%s: prior=%s planned=%s", name, priorValue, plannedValue)
			}
		}
		t.Fatalf("fresh imported omission: diagnostics=%v replace=%v action=%s", omitted.Diagnostics, omitted.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted))
	}

	ownedConfig := map[string]interface{}{
		"model_name": "imported-model", "custom_llm_provider": "anthropic", "base_model": "claude", "tpm": int64(100),
	}
	owned := plan(ownedConfig, read.NewState, read.NewState, read.Private)
	if accessGroupProtocolDiagnosticsHaveError(owned.Diagnostics) || len(owned.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, read.NewState, owned) != organizationProjectProtocolActionUpdate {
		t.Fatalf("configured-equal transition: diagnostics=%v replace=%v action=%s", owned.Diagnostics, owned.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, read.NewState, owned))
	}
	ownedDynamic := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, ownedConfig))
	readsBefore := reads.Load()
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: ownedDynamic, PriorState: read.NewState, PlannedState: owned.PlannedState, PlannedPrivate: owned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || patches.Load() != 0 || reads.Load() != readsBefore+2 {
		t.Fatalf("ownership apply: err=%v diagnostics=%v patches=%d reads=%d, want %d", err, applied.Diagnostics, patches.Load(), reads.Load(), readsBefore+2)
	}

	removalProposed := organizationProjectProtocolReplace(t, schema, applied.NewState, map[string]interface{}{"tpm": nil})
	removal := plan(configValues, removalProposed, applied.NewState, applied.Private)
	if accessGroupProtocolDiagnosticsHaveError(removal.Diagnostics) || len(removal.RequiresReplace) == 0 || organizationProjectProtocolPlannedAction(t, schema, applied.NewState, removal) != organizationProjectProtocolActionReplace {
		t.Fatalf("owned limit removal: diagnostics=%v replace=%v action=%s", removal.Diagnostics, removal.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, applied.NewState, removal))
	}

	modeConfig := map[string]interface{}{
		"model_name": "imported-model", "custom_llm_provider": "anthropic", "base_model": "claude", "tpm": int64(100), "mode": "chat", "input_cost_per_pixel": 0.25,
	}
	modeOwned := plan(modeConfig, applied.NewState, applied.NewState, applied.Private)
	if accessGroupProtocolDiagnosticsHaveError(modeOwned.Diagnostics) || len(modeOwned.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, applied.NewState, modeOwned) != organizationProjectProtocolActionUpdate {
		t.Fatalf("configured-equal mode transition: diagnostics=%v replace=%v action=%s", modeOwned.Diagnostics, modeOwned.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, applied.NewState, modeOwned))
	}
	modeDynamic := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, modeConfig))
	readsBefore = reads.Load()
	modeApplied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: modeDynamic, PriorState: applied.NewState, PlannedState: modeOwned.PlannedState, PlannedPrivate: modeOwned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(modeApplied.Diagnostics) || patches.Load() != 0 || reads.Load() != readsBefore+2 {
		t.Fatalf("mode ownership apply: err=%v diagnostics=%v patches=%d reads=%d, want %d", err, modeApplied.Diagnostics, patches.Load(), reads.Load(), readsBefore+2)
	}

	modeRemoval := plan(ownedConfig, modeApplied.NewState, modeApplied.NewState, modeApplied.Private)
	if accessGroupProtocolDiagnosticsHaveError(modeRemoval.Diagnostics) || len(modeRemoval.RequiresReplace) < 2 || organizationProjectProtocolPlannedAction(t, schema, modeApplied.NewState, modeRemoval) != organizationProjectProtocolActionReplace {
		t.Fatalf("owned mode removal: diagnostics=%v replace=%v action=%s", modeRemoval.Diagnostics, modeRemoval.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, modeApplied.NewState, modeRemoval))
	}
}
