package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestProjectImportedFieldOwnershipTransitionsPlanRealActionsIndependentlyProtocol(t *testing.T) {
	for _, test := range []struct {
		name       string
		attribute  string
		importKey  string
		pendingKey string
		desired    string
		changed    bool
		rejected   bool
	}{
		{name: "alias equal", attribute: "project_alias", importKey: projectImportedAliasPrivateKey, pendingKey: projectAliasOwnershipPendingPrivateKey, desired: "imported-project"},
		{name: "alias changed", attribute: "project_alias", importKey: projectImportedAliasPrivateKey, pendingKey: projectAliasOwnershipPendingPrivateKey, desired: "managed-project", changed: true},
		{name: "description equal", attribute: "description", importKey: projectImportedDescriptionPrivateKey, pendingKey: projectDescriptionOwnershipPendingPrivateKey, desired: "imported description"},
		{name: "description changed", attribute: "description", importKey: projectImportedDescriptionPrivateKey, pendingKey: projectDescriptionOwnershipPendingPrivateKey, desired: "managed description", changed: true},
		{name: "budget equal", attribute: "budget_id", importKey: organizationProjectImportedBudgetPrivateKey, pendingKey: organizationProjectBudgetOwnershipPendingPrivateKey, desired: "budget-project"},
		{name: "budget changed is rejected", attribute: "budget_id", importKey: organizationProjectImportedBudgetPrivateKey, pendingKey: organizationProjectBudgetOwnershipPendingPrivateKey, desired: "other-budget", changed: true, rejected: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var projectAlias, description atomic.Value
			projectAlias.Store("imported-project")
			description.Store("imported description")
			var readCalls, updateCalls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch {
				case request.Method == http.MethodGet && request.URL.Path == "/project/info":
					readCalls.Add(1)
					_, _ = fmt.Fprintf(writer, `{"project_id":"project-import","project_alias":%q,"description":%q,"team_id":"team-1","budget_id":"budget-project","created_at":"2026-01-01T00:00:00Z","litellm_budget_table":{"budget_id":"budget-project"}}`, projectAlias.Load().(string), description.Load().(string))
				case request.Method == http.MethodPost && request.URL.Path == "/project/update":
					updateCalls.Add(1)
					body, _ := io.ReadAll(request.Body)
					var payload map[string]interface{}
					if err := decodeJSONUseNumber(body, &payload); err != nil {
						t.Error(err)
						http.Error(writer, `{"error":"bad request"}`, http.StatusBadRequest)
						return
					}
					if value, ok := payload["project_alias"].(string); ok {
						projectAlias.Store(value)
					}
					if value, ok := payload["description"].(string); ok {
						description.Store(value)
					}
					_, _ = fmt.Fprintf(writer, `{"project_id":"project-import","project_alias":%q,"description":%q,"team_id":"team-1","budget_id":"budget-project","created_at":"2026-01-01T00:00:00Z","litellm_budget_table":{"budget_id":"budget-project"}}`, projectAlias.Load().(string), description.Load().(string))
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			const typeName = "litellm_project"
			schema := schemas.ResourceSchemas[typeName]
			imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "project-import"})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
				t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
			}
			read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("read import: err=%v diagnostics=%v", err, read.Diagnostics)
			}

			plan := func(configValues map[string]interface{}, prior, proposed *tfprotov6.DynamicValue, private []byte) *tfprotov6.PlanResourceChangeResponse {
				t.Helper()
				config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
				response, planErr := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
					TypeName: typeName, Config: config, PriorState: prior, ProposedNewState: proposed, PriorPrivate: private,
				})
				if planErr != nil {
					t.Fatalf("plan: %v", planErr)
				}
				return response
			}

			omittedConfig := map[string]interface{}{"team_id": "team-1"}
			omitted := plan(omittedConfig, read.NewState, read.NewState, read.Private)
			if accessGroupProtocolDiagnosticsHaveError(omitted.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted) != organizationProjectProtocolActionNoOp {
				t.Fatalf("import omission: diagnostics=%v action=%s", omitted.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted))
			}

			configuredValues := map[string]interface{}{"team_id": "team-1", test.attribute: test.desired}
			proposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{test.attribute: test.desired})
			planned := plan(configuredValues, read.NewState, proposed, read.Private)
			if test.rejected {
				if !accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || !protocolPrivateHasKey(t, planned.PlannedPrivate, test.importKey) || updateCalls.Load() != 0 {
					t.Fatalf("rejected transition: diagnostics=%v writes=%d private=%s", planned.Diagnostics, updateCalls.Load(), planned.PlannedPrivate)
				}
				retryOmission := plan(omittedConfig, read.NewState, read.NewState, read.Private)
				if accessGroupProtocolDiagnosticsHaveError(retryOmission.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, retryOmission) != organizationProjectProtocolActionNoOp {
					t.Fatalf("rejected transition consumed marker: diagnostics=%v action=%s", retryOmission.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, retryOmission))
				}
				return
			}
			if accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned) != organizationProjectProtocolActionUpdate || len(planned.RequiresReplace) != 0 || !protocolPrivateHasKey(t, planned.PlannedPrivate, test.importKey) || !protocolPrivateHasKey(t, planned.PlannedPrivate, test.pendingKey) {
				t.Fatalf("transition plan: diagnostics=%v action=%s replace=%v private=%s", planned.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned), planned.RequiresReplace, planned.PlannedPrivate)
			}
			if !test.changed {
				attributes := protocolAttributeMap(t, schema, planned.PlannedState)
				if attributes["created_at"].IsKnown() {
					t.Fatalf("equal private-only transition left created_at known: %s", attributes["created_at"])
				}
			}

			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configuredValues))
			readsBeforeApply := readCalls.Load()
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: typeName, Config: config, PriorState: read.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			wantWrites := int64(0)
			if test.changed {
				wantWrites = 1
			}
			if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || updateCalls.Load() != wantWrites || readCalls.Load() != readsBeforeApply+1 {
				t.Fatalf("apply: err=%v diagnostics=%v writes=%d, want %d; authoritative reads=%d, want %d", err, applied.Diagnostics, updateCalls.Load(), wantWrites, readCalls.Load(), readsBeforeApply+1)
			}
			if protocolPrivateHasKey(t, applied.Private, test.importKey) || protocolPrivateHasKey(t, applied.Private, test.pendingKey) {
				t.Fatalf("successful transition retained target markers: %s", applied.Private)
			}
			for _, independentKey := range []string{organizationProjectImportedBudgetPrivateKey, projectImportedAliasPrivateKey, projectImportedDescriptionPrivateKey} {
				if independentKey != test.importKey && !protocolPrivateHasKey(t, applied.Private, independentKey) {
					t.Fatalf("successful transition consumed independent marker %q: %s", independentKey, applied.Private)
				}
			}

			steady := plan(configuredValues, applied.NewState, applied.NewState, applied.Private)
			if accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, applied.NewState, steady) != organizationProjectProtocolActionNoOp {
				t.Fatalf("steady plan: diagnostics=%v action=%s", steady.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, applied.NewState, steady))
			}
			removal := plan(omittedConfig, applied.NewState, applied.NewState, applied.Private)
			if !accessGroupProtocolDiagnosticsHaveError(removal.Diagnostics) {
				t.Fatalf("configured %s could be omitted after ownership transition", test.attribute)
			}
		})
	}
}
