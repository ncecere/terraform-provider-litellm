package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func organizationProjectProtocolValue(t *testing.T, schema *tfprotov6.Schema, values map[string]interface{}) tftypes.Value {
	t.Helper()
	objectType, ok := schema.ValueType().(tftypes.Object)
	if !ok {
		t.Fatalf("resource schema type = %T, want tftypes.Object", schema.ValueType())
	}
	attributes := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
	for name, attributeType := range objectType.AttributeTypes {
		value := interface{}(nil)
		if configured, exists := values[name]; exists {
			value = configured
		}
		attributes[name] = tftypes.NewValue(attributeType, value)
	}
	return tftypes.NewValue(objectType, attributes)
}

type organizationProjectProtocolAction string

const (
	organizationProjectProtocolActionNoOp    organizationProjectProtocolAction = "NoOp"
	organizationProjectProtocolActionCreate  organizationProjectProtocolAction = "Create"
	organizationProjectProtocolActionUpdate  organizationProjectProtocolAction = "Update"
	organizationProjectProtocolActionDelete  organizationProjectProtocolAction = "Delete"
	organizationProjectProtocolActionReplace organizationProjectProtocolAction = "Replace"
)

// PlanResourceChange does not return Terraform's derived action. Infer it from
// the exact public prior/planned states and replacement paths using Terraform's
// action rules. Planned private bytes intentionally do not participate.
func organizationProjectProtocolPlannedAction(t *testing.T, schema *tfprotov6.Schema, prior *tfprotov6.DynamicValue, planned *tfprotov6.PlanResourceChangeResponse) organizationProjectProtocolAction {
	t.Helper()
	priorValue, err := prior.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	plannedValue, err := planned.PlannedState.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	if len(planned.RequiresReplace) != 0 {
		return organizationProjectProtocolActionReplace
	}
	switch {
	case priorValue.IsNull() && !plannedValue.IsNull():
		return organizationProjectProtocolActionCreate
	case !priorValue.IsNull() && plannedValue.IsNull():
		return organizationProjectProtocolActionDelete
	case priorValue.Equal(plannedValue):
		return organizationProjectProtocolActionNoOp
	default:
		return organizationProjectProtocolActionUpdate
	}
}

func organizationProjectProtocolReplace(t *testing.T, schema *tfprotov6.Schema, dynamic *tfprotov6.DynamicValue, replacements map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	value, err := dynamic.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	attributes := map[string]tftypes.Value{}
	if err := value.As(&attributes); err != nil {
		t.Fatal(err)
	}
	for name, replacement := range replacements {
		current, exists := attributes[name]
		if !exists {
			t.Fatalf("replacement attribute %q is not in schema", name)
		}
		attributes[name] = tftypes.NewValue(current.Type(), replacement)
	}
	return accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), attributes))
}

func TestOrganizationProjectImportOmissionNoOpAndTerminalDestroyProtocol(t *testing.T) {
	ctx := context.Background()
	var organizationDeletes, projectDeletes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			_, _ = writer.Write([]byte(`{"organization_id":"org-import","organization_alias":"imported-org","budget_id":"budget-org","litellm_budget_table":{"budget_id":"budget-org","model_max_budget":{"gpt-4o":{"max_budget":12.5,"budget_duration":"30d"}}}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/project/info":
			_, _ = writer.Write([]byte(`{"project_id":"project-import","project_alias":"imported-project","description":"imported description","team_id":"team-1","budget_id":"budget-project","litellm_budget_table":{"budget_id":"budget-project","model_max_budget":{"gpt-4o":{"max_budget":4.5,"budget_duration":"7d"}}}}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/organization/delete":
			organizationDeletes.Add(1)
			_, _ = writer.Write([]byte(`{}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/project/delete":
			projectDeletes.Add(1)
			_, _ = writer.Write([]byte(`{}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	for _, test := range []struct {
		name, typeName, id, budgetID string
		config                       map[string]interface{}
		deleteCount                  *atomic.Int64
	}{
		{"organization", "litellm_organization", "org-import", "budget-org", map[string]interface{}{"organization_alias": "imported-org"}, &organizationDeletes},
		{"project with omitted alias and description", "litellm_project", "project-import", "budget-project", map[string]interface{}{"team_id": "team-1"}, &projectDeletes},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := schemas.ResourceSchemas[test.typeName]
			imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: test.typeName, ID: test.id})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
				t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
			}
			read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: test.typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("read import: err=%v diagnostics=%v", err, read.Diagnostics)
			}
			attributes := protocolAttributeMap(t, schema, read.NewState)
			var budgetID string
			if err := attributes["budget_id"].As(&budgetID); err != nil || budgetID != test.budgetID {
				t.Fatalf("imported budget_id = %q, want %q (error %v)", budgetID, test.budgetID, err)
			}

			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, test.config))
			noOp, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: test.typeName, Config: config, PriorState: read.NewState, ProposedNewState: read.NewState, PriorPrivate: read.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(noOp.Diagnostics) || len(noOp.RequiresReplace) != 0 {
				t.Fatalf("import omission plan: err=%v diagnostics=%v replace=%v", err, noOp.Diagnostics, noOp.RequiresReplace)
			}
			if action := organizationProjectProtocolPlannedAction(t, schema, read.NewState, noOp); action != organizationProjectProtocolActionNoOp {
				t.Fatalf("omitted imported fields action = %s, want NoOp", action)
			}

			changedConfig := organizationProjectProtocolReplace(t, schema, config, map[string]interface{}{"budget_id": "other-budget"})
			changedProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"budget_id": "other-budget"})
			blocked, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: test.typeName, Config: changedConfig, PriorState: read.NewState, ProposedNewState: changedProposed, PriorPrivate: read.Private})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(blocked.Diagnostics) {
				t.Fatalf("configured reassociation plan: err=%v diagnostics=%v", err, blocked.Diagnostics)
			}

			nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
			destroyPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: test.typeName, Config: nullState, PriorState: read.NewState, ProposedNewState: nullState, PriorPrivate: read.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyPlan.Diagnostics) {
				t.Fatalf("destroy plan: err=%v diagnostics=%v", err, destroyPlan.Diagnostics)
			}
			destroyed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: test.typeName, Config: nullState, PriorState: read.NewState, PlannedState: destroyPlan.PlannedState, PlannedPrivate: destroyPlan.PlannedPrivate})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyed.Diagnostics) || test.deleteCount.Load() != 1 {
				t.Fatalf("destroy apply: err=%v diagnostics=%v deletes=%d", err, destroyed.Diagnostics, test.deleteCount.Load())
			}
			terminal, err := destroyed.NewState.Unmarshal(schema.ValueType())
			if err != nil || !terminal.IsNull() {
				t.Fatalf("terminal destroy state = %v (error %v)", terminal, err)
			}
		})
	}
}

func TestProjectImportPermissionsTransitionIndependentlyAfterSuccessfulApplyProtocol(t *testing.T) {
	ctx := context.Background()
	projectAlias, description := "imported-project", "imported description"
	var failNextUpdate atomic.Bool
	var projectUpdates, deletes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/project/info":
			_, _ = fmt.Fprintf(writer, `{"project_id":"project-import","project_alias":%q,"description":%q,"team_id":"team-1","budget_id":"budget-project","litellm_budget_table":{"budget_id":"budget-project"}}`, projectAlias, description)
		case request.Method == http.MethodPost && request.URL.Path == "/project/update":
			projectUpdates.Add(1)
			if failNextUpdate.Swap(false) {
				http.Error(writer, `{"error":"retry"}`, http.StatusInternalServerError)
				return
			}
			body, _ := io.ReadAll(request.Body)
			var payload map[string]interface{}
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Fatal(err)
			}
			if value, ok := payload["project_alias"].(string); ok {
				projectAlias = value
			}
			if value, ok := payload["description"].(string); ok {
				description = value
			}
			_, _ = fmt.Fprintf(writer, `{"project_id":"project-import","project_alias":%q,"description":%q,"team_id":"team-1","budget_id":"budget-project","litellm_budget_table":{"budget_id":"budget-project"}}`, projectAlias, description)
		case request.Method == http.MethodDelete && request.URL.Path == "/project/delete":
			deletes.Add(1)
			_, _ = writer.Write([]byte(`{}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	typeName := "litellm_project"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "project-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read import: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	for _, key := range []string{organizationProjectImportedBudgetPrivateKey, projectImportedAliasPrivateKey, projectImportedDescriptionPrivateKey} {
		if !protocolPrivateHasKey(t, read.Private, key) {
			t.Fatalf("fresh import private state omitted %q: %s", key, read.Private)
		}
	}
	if protocolPrivateHasKey(t, read.Private, projectImportedOptionalStringsPrivateKey) {
		t.Fatalf("fresh import used shared optional-string marker: %s", read.Private)
	}

	plan := func(configValues map[string]interface{}, proposed *tfprotov6.DynamicValue, priorState *tfprotov6.DynamicValue, private []byte) *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		response, planErr := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: priorState, ProposedNewState: proposed, PriorPrivate: private})
		if planErr != nil {
			t.Fatalf("plan: %v", planErr)
		}
		return response
	}
	apply := func(configValues map[string]interface{}, priorState *tfprotov6.DynamicValue, planned *tfprotov6.PlanResourceChangeResponse) *tfprotov6.ApplyResourceChangeResponse {
		t.Helper()
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		response, applyErr := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: priorState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if applyErr != nil {
			t.Fatalf("apply: %v", applyErr)
		}
		return response
	}

	omitted := map[string]interface{}{"team_id": "team-1"}
	noOp := plan(omitted, read.NewState, read.NewState, read.Private)
	if accessGroupProtocolDiagnosticsHaveError(noOp.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, noOp) != organizationProjectProtocolActionNoOp {
		t.Fatalf("fresh imported omission: diagnostics=%v action=%s", noOp.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, noOp))
	}

	// Retained state from the pre-per-field implementation is split lazily.
	// Omission is a true NoOp, so planned-private-only migration is correctly
	// not treated as persisted until a later explicit field transition applies.
	legacyPrivate, err := json.Marshal(map[string][]byte{
		organizationProjectImportedBudgetPrivateKey: []byte("true"),
		projectImportedOptionalStringsPrivateKey:    []byte("true"),
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyPlan := plan(omitted, read.NewState, read.NewState, legacyPrivate)
	if accessGroupProtocolDiagnosticsHaveError(legacyPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, legacyPlan) != organizationProjectProtocolActionNoOp || protocolPrivateHasKey(t, legacyPlan.PlannedPrivate, projectImportedOptionalStringsPrivateKey) || !protocolPrivateHasKey(t, legacyPlan.PlannedPrivate, projectImportedAliasPrivateKey) || !protocolPrivateHasKey(t, legacyPlan.PlannedPrivate, projectImportedDescriptionPrivateKey) {
		t.Fatalf("legacy marker split plan: diagnostics=%v private=%s", legacyPlan.Diagnostics, legacyPlan.PlannedPrivate)
	}
	legacyAliasConfig := map[string]interface{}{"team_id": "team-1", "project_alias": "imported-project"}
	legacyAliasPlan := plan(legacyAliasConfig, read.NewState, read.NewState, legacyPrivate)
	if accessGroupProtocolDiagnosticsHaveError(legacyAliasPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, legacyAliasPlan) != organizationProjectProtocolActionUpdate || len(legacyAliasPlan.RequiresReplace) != 0 || protocolPrivateHasKey(t, legacyAliasPlan.PlannedPrivate, projectImportedOptionalStringsPrivateKey) || !protocolPrivateHasKey(t, legacyAliasPlan.PlannedPrivate, projectAliasOwnershipPendingPrivateKey) {
		t.Fatalf("legacy alias transition plan: diagnostics=%v action=%s replace=%v private=%s", legacyAliasPlan.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, legacyAliasPlan), legacyAliasPlan.RequiresReplace, legacyAliasPlan.PlannedPrivate)
	}
	legacyWritesBefore := projectUpdates.Load()
	legacyAliasApplied := apply(legacyAliasConfig, read.NewState, legacyAliasPlan)
	if accessGroupProtocolDiagnosticsHaveError(legacyAliasApplied.Diagnostics) || projectUpdates.Load() != legacyWritesBefore || protocolPrivateHasKey(t, legacyAliasApplied.Private, projectImportedOptionalStringsPrivateKey) || protocolPrivateHasKey(t, legacyAliasApplied.Private, projectImportedAliasPrivateKey) || !protocolPrivateHasKey(t, legacyAliasApplied.Private, projectImportedDescriptionPrivateKey) || !protocolPrivateHasKey(t, legacyAliasApplied.Private, organizationProjectImportedBudgetPrivateKey) {
		t.Fatalf("legacy alias transition apply: diagnostics=%v writes=%d, want %d private=%s", legacyAliasApplied.Diagnostics, projectUpdates.Load(), legacyWritesBefore, legacyAliasApplied.Private)
	}

	// A rejected reassociation is a failed plan, not an ownership transition.
	badBudgetConfig := map[string]interface{}{"team_id": "team-1", "budget_id": "other-budget"}
	badBudgetProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"budget_id": "other-budget"})
	badBudget := plan(badBudgetConfig, badBudgetProposed, read.NewState, read.Private)
	if !accessGroupProtocolDiagnosticsHaveError(badBudget.Diagnostics) || !protocolPrivateHasKey(t, badBudget.PlannedPrivate, organizationProjectImportedBudgetPrivateKey) {
		t.Fatalf("failed budget plan consumed import permission: diagnostics=%v private=%s", badBudget.Diagnostics, badBudget.PlannedPrivate)
	}

	// A failed apply likewise cannot affect the last successfully persisted
	// private checkpoint. Retrying from that checkpoint keeps omission valid.
	failedDescriptionConfig := map[string]interface{}{"team_id": "team-1", "description": "failed change"}
	failedDescriptionProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"description": "failed change"})
	failedDescriptionPlan := plan(failedDescriptionConfig, failedDescriptionProposed, read.NewState, read.Private)
	if accessGroupProtocolDiagnosticsHaveError(failedDescriptionPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, failedDescriptionPlan) != organizationProjectProtocolActionUpdate || len(failedDescriptionPlan.RequiresReplace) != 0 || !protocolPrivateHasKey(t, failedDescriptionPlan.PlannedPrivate, projectImportedDescriptionPrivateKey) || !protocolPrivateHasKey(t, failedDescriptionPlan.PlannedPrivate, projectDescriptionOwnershipPendingPrivateKey) {
		t.Fatalf("description transition plan: diagnostics=%v action=%s replace=%v private=%s", failedDescriptionPlan.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, failedDescriptionPlan), failedDescriptionPlan.RequiresReplace, failedDescriptionPlan.PlannedPrivate)
	}
	failNextUpdate.Store(true)
	failedDescription := apply(failedDescriptionConfig, read.NewState, failedDescriptionPlan)
	if !accessGroupProtocolDiagnosticsHaveError(failedDescription.Diagnostics) || !protocolPrivateHasKey(t, failedDescription.Private, projectImportedDescriptionPrivateKey) || !protocolPrivateHasKey(t, failedDescription.Private, projectDescriptionOwnershipPendingPrivateKey) {
		t.Fatalf("failed project apply consumed permission: diagnostics=%v private=%s", failedDescription.Diagnostics, failedDescription.Private)
	}
	retryOmission := plan(omitted, read.NewState, read.NewState, failedDescription.Private)
	if accessGroupProtocolDiagnosticsHaveError(retryOmission.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, retryOmission) != organizationProjectProtocolActionNoOp || !protocolPrivateHasKey(t, retryOmission.PlannedPrivate, projectImportedDescriptionPrivateKey) || protocolPrivateHasKey(t, retryOmission.PlannedPrivate, projectDescriptionOwnershipPendingPrivateKey) {
		t.Fatalf("failed apply prematurely persisted ownership: diagnostics=%v action=%s private=%s", retryOmission.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, retryOmission), retryOmission.PlannedPrivate)
	}

	aliasConfig := map[string]interface{}{"team_id": "team-1", "project_alias": "imported-project"}
	aliasPlan := plan(aliasConfig, read.NewState, read.NewState, read.Private)
	if accessGroupProtocolDiagnosticsHaveError(aliasPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, aliasPlan) != organizationProjectProtocolActionUpdate || len(aliasPlan.RequiresReplace) != 0 || !protocolPrivateHasKey(t, aliasPlan.PlannedPrivate, projectImportedAliasPrivateKey) || !protocolPrivateHasKey(t, aliasPlan.PlannedPrivate, projectAliasOwnershipPendingPrivateKey) || !protocolPrivateHasKey(t, aliasPlan.PlannedPrivate, projectImportedDescriptionPrivateKey) || !protocolPrivateHasKey(t, aliasPlan.PlannedPrivate, organizationProjectImportedBudgetPrivateKey) {
		t.Fatalf("alias per-field transition: diagnostics=%v action=%s replace=%v private=%s", aliasPlan.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, aliasPlan), aliasPlan.RequiresReplace, aliasPlan.PlannedPrivate)
	}
	aliasWritesBefore := projectUpdates.Load()
	aliasApplied := apply(aliasConfig, read.NewState, aliasPlan)
	if accessGroupProtocolDiagnosticsHaveError(aliasApplied.Diagnostics) || projectUpdates.Load() != aliasWritesBefore {
		t.Fatalf("apply equal alias: diagnostics=%v writes=%d, want %d", aliasApplied.Diagnostics, projectUpdates.Load(), aliasWritesBefore)
	}
	if protocolPrivateHasKey(t, aliasApplied.Private, projectImportedAliasPrivateKey) || protocolPrivateHasKey(t, aliasApplied.Private, projectAliasOwnershipPendingPrivateKey) || !protocolPrivateHasKey(t, aliasApplied.Private, projectImportedDescriptionPrivateKey) || !protocolPrivateHasKey(t, aliasApplied.Private, organizationProjectImportedBudgetPrivateKey) {
		t.Fatalf("successful alias apply private = %s", aliasApplied.Private)
	}
	otherFieldsOmitted := plan(aliasConfig, aliasApplied.NewState, aliasApplied.NewState, aliasApplied.Private)
	if accessGroupProtocolDiagnosticsHaveError(otherFieldsOmitted.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, aliasApplied.NewState, otherFieldsOmitted) != organizationProjectProtocolActionNoOp {
		t.Fatalf("steady alias plan: diagnostics=%v action=%s", otherFieldsOmitted.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, aliasApplied.NewState, otherFieldsOmitted))
	}
	aliasRemoval := plan(omitted, aliasApplied.NewState, aliasApplied.NewState, aliasApplied.Private)
	if !accessGroupProtocolDiagnosticsHaveError(aliasRemoval.Diagnostics) {
		t.Fatal("configured-equal imported alias could later be omitted")
	}

	descriptionConfig := map[string]interface{}{"team_id": "team-1", "project_alias": "imported-project", "description": "changed description"}
	descriptionProposed := organizationProjectProtocolReplace(t, schema, aliasApplied.NewState, map[string]interface{}{"description": "changed description"})
	descriptionPlan := plan(descriptionConfig, descriptionProposed, aliasApplied.NewState, aliasApplied.Private)
	if accessGroupProtocolDiagnosticsHaveError(descriptionPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, aliasApplied.NewState, descriptionPlan) != organizationProjectProtocolActionUpdate || len(descriptionPlan.RequiresReplace) != 0 || !protocolPrivateHasKey(t, descriptionPlan.PlannedPrivate, projectImportedDescriptionPrivateKey) || !protocolPrivateHasKey(t, descriptionPlan.PlannedPrivate, projectDescriptionOwnershipPendingPrivateKey) || !protocolPrivateHasKey(t, descriptionPlan.PlannedPrivate, organizationProjectImportedBudgetPrivateKey) {
		t.Fatalf("description per-field transition: diagnostics=%v action=%s replace=%v private=%s", descriptionPlan.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, aliasApplied.NewState, descriptionPlan), descriptionPlan.RequiresReplace, descriptionPlan.PlannedPrivate)
	}
	descriptionApplied := apply(descriptionConfig, aliasApplied.NewState, descriptionPlan)
	if accessGroupProtocolDiagnosticsHaveError(descriptionApplied.Diagnostics) {
		t.Fatalf("apply changed description: %v", descriptionApplied.Diagnostics)
	}
	descriptionSteady := plan(descriptionConfig, descriptionApplied.NewState, descriptionApplied.NewState, descriptionApplied.Private)
	if accessGroupProtocolDiagnosticsHaveError(descriptionSteady.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, descriptionApplied.NewState, descriptionSteady) != organizationProjectProtocolActionNoOp {
		t.Fatalf("steady description plan: diagnostics=%v action=%s", descriptionSteady.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, descriptionApplied.NewState, descriptionSteady))
	}
	descriptionRemovalConfig := map[string]interface{}{"team_id": "team-1", "project_alias": "imported-project"}
	descriptionRemoval := plan(descriptionRemovalConfig, descriptionApplied.NewState, descriptionApplied.NewState, descriptionApplied.Private)
	if !accessGroupProtocolDiagnosticsHaveError(descriptionRemoval.Diagnostics) {
		t.Fatal("changed configured imported description could later be omitted")
	}

	budgetConfig := map[string]interface{}{"team_id": "team-1", "project_alias": "imported-project", "description": "changed description", "budget_id": "budget-project"}
	budgetPlan := plan(budgetConfig, descriptionApplied.NewState, descriptionApplied.NewState, descriptionApplied.Private)
	if accessGroupProtocolDiagnosticsHaveError(budgetPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, descriptionApplied.NewState, budgetPlan) != organizationProjectProtocolActionUpdate || len(budgetPlan.RequiresReplace) != 0 || !protocolPrivateHasKey(t, budgetPlan.PlannedPrivate, organizationProjectImportedBudgetPrivateKey) || !protocolPrivateHasKey(t, budgetPlan.PlannedPrivate, organizationProjectBudgetOwnershipPendingPrivateKey) {
		t.Fatalf("budget transition: diagnostics=%v action=%s replace=%v private=%s", budgetPlan.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, descriptionApplied.NewState, budgetPlan), budgetPlan.RequiresReplace, budgetPlan.PlannedPrivate)
	}
	budgetWritesBefore := projectUpdates.Load()
	budgetApplied := apply(budgetConfig, descriptionApplied.NewState, budgetPlan)
	if accessGroupProtocolDiagnosticsHaveError(budgetApplied.Diagnostics) || projectUpdates.Load() != budgetWritesBefore {
		t.Fatalf("apply equal budget: diagnostics=%v writes=%d, want %d", budgetApplied.Diagnostics, projectUpdates.Load(), budgetWritesBefore)
	}
	budgetSteady := plan(budgetConfig, budgetApplied.NewState, budgetApplied.NewState, budgetApplied.Private)
	if accessGroupProtocolDiagnosticsHaveError(budgetSteady.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, budgetApplied.NewState, budgetSteady) != organizationProjectProtocolActionNoOp {
		t.Fatalf("steady budget plan: diagnostics=%v action=%s", budgetSteady.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, budgetApplied.NewState, budgetSteady))
	}
	budgetRemovalConfig := map[string]interface{}{"team_id": "team-1", "project_alias": "imported-project", "description": "changed description"}
	budgetRemoval := plan(budgetRemovalConfig, budgetApplied.NewState, budgetApplied.NewState, budgetApplied.Private)
	if !accessGroupProtocolDiagnosticsHaveError(budgetRemoval.Diagnostics) {
		t.Fatal("configured-equal imported budget could later be omitted")
	}

	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	destroyPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: nullState, PriorState: budgetApplied.NewState, ProposedNewState: nullState, PriorPrivate: budgetApplied.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyPlan.Diagnostics) {
		t.Fatalf("destroy plan after ownership transitions: err=%v diagnostics=%v", err, destroyPlan.Diagnostics)
	}
	destroyed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: nullState, PriorState: budgetApplied.NewState, PlannedState: destroyPlan.PlannedState, PlannedPrivate: destroyPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyed.Diagnostics) || deletes.Load() != 1 {
		t.Fatalf("destroy after ownership transitions: err=%v diagnostics=%v deletes=%d", err, destroyed.Diagnostics, deletes.Load())
	}
}

func TestOrganizationImportedBudgetPermissionConsumedOnlyAfterSuccessfulApplyProtocol(t *testing.T) {
	ctx := context.Background()
	var failNextRead atomic.Bool
	var patches, deletes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			if failNextRead.Swap(false) {
				http.Error(writer, `{"error":"retry"}`, http.StatusInternalServerError)
				return
			}
			_, _ = writer.Write([]byte(`{"organization_id":"org-import","organization_alias":"imported-org","budget_id":"budget-org","created_at":"2026-01-01T00:00:00Z","litellm_budget_table":{"budget_id":"budget-org"}}`))
		case request.Method == http.MethodPatch:
			patches.Add(1)
			http.Error(writer, `{"error":"unexpected mutation"}`, http.StatusInternalServerError)
		case request.Method == http.MethodDelete && request.URL.Path == "/organization/delete":
			deletes.Add(1)
			_, _ = writer.Write([]byte(`{}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	typeName := "litellm_organization"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "org-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) || !protocolPrivateHasKey(t, read.Private, organizationProjectImportedBudgetPrivateKey) {
		t.Fatalf("read import: err=%v diagnostics=%v private=%s", err, read.Diagnostics, read.Private)
	}

	plan := func(values map[string]interface{}, prior, proposed *tfprotov6.DynamicValue, private []byte) *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
		response, planErr := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: prior, ProposedNewState: proposed, PriorPrivate: private})
		if planErr != nil {
			t.Fatal(planErr)
		}
		return response
	}
	omittedConfig := map[string]interface{}{"organization_alias": "imported-org"}
	omitted := plan(omittedConfig, read.NewState, read.NewState, read.Private)
	if accessGroupProtocolDiagnosticsHaveError(omitted.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted) != organizationProjectProtocolActionNoOp {
		t.Fatalf("fresh omitted import: diagnostics=%v action=%s", omitted.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted))
	}
	badConfig := map[string]interface{}{"organization_alias": "imported-org", "budget_id": "other-budget"}
	badProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"budget_id": "other-budget"})
	bad := plan(badConfig, read.NewState, badProposed, read.Private)
	if !accessGroupProtocolDiagnosticsHaveError(bad.Diagnostics) || !protocolPrivateHasKey(t, bad.PlannedPrivate, organizationProjectImportedBudgetPrivateKey) {
		t.Fatalf("failed plan consumed organization permission: diagnostics=%v private=%s", bad.Diagnostics, bad.PlannedPrivate)
	}

	ownedConfig := map[string]interface{}{"organization_alias": "imported-org", "budget_id": "budget-org"}
	owned := plan(ownedConfig, read.NewState, read.NewState, read.Private)
	if accessGroupProtocolDiagnosticsHaveError(owned.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, owned) != organizationProjectProtocolActionUpdate || len(owned.RequiresReplace) != 0 || !protocolPrivateHasKey(t, owned.PlannedPrivate, organizationProjectImportedBudgetPrivateKey) || !protocolPrivateHasKey(t, owned.PlannedPrivate, organizationProjectBudgetOwnershipPendingPrivateKey) {
		t.Fatalf("equal configured organization budget plan: diagnostics=%v action=%s replace=%v private=%s", owned.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, owned), owned.RequiresReplace, owned.PlannedPrivate)
	}
	if createdAt := protocolAttributeMap(t, schema, owned.PlannedState)["created_at"]; createdAt.IsKnown() {
		t.Fatalf("organization created_at modifier was not overridden with unknown: %s", createdAt)
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, ownedConfig))
	failNextRead.Store(true)
	failed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: read.NewState, PlannedState: owned.PlannedState, PlannedPrivate: owned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) || !protocolPrivateHasKey(t, failed.Private, organizationProjectImportedBudgetPrivateKey) || !protocolPrivateHasKey(t, failed.Private, organizationProjectBudgetOwnershipPendingPrivateKey) || patches.Load() != 0 {
		t.Fatalf("failed ownership read: err=%v diagnostics=%v patches=%d private=%s", err, failed.Diagnostics, patches.Load(), failed.Private)
	}
	failedOmission := plan(omittedConfig, read.NewState, read.NewState, failed.Private)
	if accessGroupProtocolDiagnosticsHaveError(failedOmission.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, failedOmission) != organizationProjectProtocolActionNoOp || !protocolPrivateHasKey(t, failedOmission.PlannedPrivate, organizationProjectImportedBudgetPrivateKey) || protocolPrivateHasKey(t, failedOmission.PlannedPrivate, organizationProjectBudgetOwnershipPendingPrivateKey) {
		t.Fatalf("failed read consumed ownership: diagnostics=%v action=%s private=%s", failedOmission.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, failedOmission), failedOmission.PlannedPrivate)
	}
	owned = plan(ownedConfig, read.NewState, read.NewState, failed.Private)
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: read.NewState, PlannedState: owned.PlannedState, PlannedPrivate: owned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || protocolPrivateHasKey(t, applied.Private, organizationProjectImportedBudgetPrivateKey) || protocolPrivateHasKey(t, applied.Private, organizationProjectBudgetOwnershipPendingPrivateKey) || patches.Load() != 0 {
		t.Fatalf("apply equal organization budget: err=%v diagnostics=%v patches=%d private=%s", err, applied.Diagnostics, patches.Load(), applied.Private)
	}
	steady := plan(ownedConfig, applied.NewState, applied.NewState, applied.Private)
	if accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, applied.NewState, steady) != organizationProjectProtocolActionNoOp {
		t.Fatalf("steady organization plan: diagnostics=%v action=%s", steady.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, applied.NewState, steady))
	}
	omittedAfterOwnershipConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, omittedConfig))
	blocked, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: omittedAfterOwnershipConfig, PriorState: applied.NewState, ProposedNewState: applied.NewState, PriorPrivate: applied.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(blocked.Diagnostics) {
		t.Fatalf("owned organization budget omission: err=%v diagnostics=%v", err, blocked.Diagnostics)
	}

	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	destroyPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: nullState, PriorState: applied.NewState, ProposedNewState: nullState, PriorPrivate: applied.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyPlan.Diagnostics) {
		t.Fatalf("destroy plan: err=%v diagnostics=%v", err, destroyPlan.Diagnostics)
	}
	destroyed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: nullState, PriorState: applied.NewState, PlannedState: destroyPlan.PlannedState, PlannedPrivate: destroyPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyed.Diagnostics) || deletes.Load() != 1 {
		t.Fatalf("destroy: err=%v diagnostics=%v deletes=%d", err, destroyed.Diagnostics, deletes.Load())
	}
}

func TestOrganizationProjectOrdinaryOmittedCreateDoesNotAdoptGeneratedBudgetProtocol(t *testing.T) {
	ctx := context.Background()
	requestBodies := map[string]map[string]interface{}{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			body, _ := io.ReadAll(request.Body)
			var payload map[string]interface{}
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Fatal(err)
			}
			requestBodies[request.URL.Path] = payload
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/organization/new":
			_, _ = writer.Write([]byte(`{"organization_id":"org-created","organization_alias":"created-org","budget_id":"generated-org","litellm_budget_table":{"budget_id":"generated-org"}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			_, _ = writer.Write([]byte(`{"organization_id":"org-created","organization_alias":"created-org","budget_id":"generated-org","litellm_budget_table":{"budget_id":"generated-org","model_max_budget":{"gpt-4o":{"max_budget":1}}}}`))
		case request.Method == http.MethodPost && request.URL.Path == "/project/new":
			projectID, _ := requestBodies[request.URL.Path]["project_id"].(string)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"project_id": projectID, "project_alias": "generated-alias", "description": "generated description", "team_id": "team-1", "budget_id": "generated-project", "litellm_budget_table": map[string]interface{}{"budget_id": "generated-project"}})
		case request.Method == http.MethodGet && request.URL.Path == "/project/info":
			projectID, _ := requestBodies["/project/new"]["project_id"].(string)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"project_id": projectID, "project_alias": "generated-alias", "description": "generated description", "team_id": "team-1", "budget_id": "generated-project", "litellm_budget_table": map[string]interface{}{"budget_id": "generated-project", "model_max_budget": map[string]interface{}{"gpt-4o": map[string]interface{}{"max_budget": 1}}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	for _, test := range []struct {
		name, typeName, createPath string
		config, computed           map[string]interface{}
	}{
		{"organization", "litellm_organization", "/organization/new", map[string]interface{}{"organization_alias": "created-org"}, map[string]interface{}{"id": tftypes.UnknownValue, "organization_id": tftypes.UnknownValue, "models": tftypes.UnknownValue, "budget_id": tftypes.UnknownValue, "model_rpm_limit": tftypes.UnknownValue, "model_tpm_limit": tftypes.UnknownValue, "metadata": tftypes.UnknownValue, "blocked": tftypes.UnknownValue, "tags": tftypes.UnknownValue, "created_at": tftypes.UnknownValue}},
		{"project", "litellm_project", "/project/new", map[string]interface{}{"team_id": "team-1"}, map[string]interface{}{"id": tftypes.UnknownValue, "project_alias": tftypes.UnknownValue, "description": tftypes.UnknownValue, "models": tftypes.UnknownValue, "metadata": tftypes.UnknownValue, "metadata_json": tftypes.UnknownValue, "tags": tftypes.UnknownValue, "budget_id": tftypes.UnknownValue, "model_max_budget": tftypes.UnknownValue, "model_rpm_limit": tftypes.UnknownValue, "model_tpm_limit": tftypes.UnknownValue, "blocked": tftypes.UnknownValue, "created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue, "created_by": tftypes.UnknownValue, "updated_by": tftypes.UnknownValue}},
	} {
		t.Run(test.name, func(t *testing.T) {
			schema := schemas.ResourceSchemas[test.typeName]
			configValue := organizationProjectProtocolValue(t, schema, test.config)
			proposedValues := make(map[string]interface{}, len(test.config)+len(test.computed))
			for name, value := range test.config {
				proposedValues[name] = value
			}
			for name, value := range test.computed {
				proposedValues[name] = value
			}
			config := accessGroupProtocolDynamicValue(t, schema, configValue)
			proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
			nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: test.typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("create plan: err=%v diagnostics=%v", err, planned.Diagnostics)
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: test.typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("create apply: err=%v diagnostics=%v", err, applied.Diagnostics)
			}
			if _, sent := requestBodies[test.createPath]["budget_id"]; sent {
				t.Fatalf("ordinary omitted create sent budget_id: %#v", requestBodies[test.createPath])
			}
			attributes := protocolAttributeMap(t, schema, applied.NewState)
			if !attributes["budget_id"].IsNull() {
				t.Fatalf("ordinary omitted create adopted generated budget_id: %v", attributes["budget_id"])
			}
			if test.typeName == "litellm_project" && (!attributes["project_alias"].IsNull() || !attributes["description"].IsNull() || !attributes["model_max_budget"].IsNull()) {
				t.Fatalf("ordinary project create adopted unconfigured API defaults: alias=%v description=%v model_max_budget=%v", attributes["project_alias"], attributes["description"], attributes["model_max_budget"])
			}
		})
	}
}
