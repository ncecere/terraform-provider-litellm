package provider

import (
	"context"
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
			priorValue, _ := read.NewState.Unmarshal(schema.ValueType())
			plannedValue, _ := noOp.PlannedState.Unmarshal(schema.ValueType())
			if !priorValue.Equal(plannedValue) {
				t.Fatal("omitted imported fields produced a non-no-op plan")
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
			_, _ = writer.Write([]byte(`{"project_id":"project-created","project_alias":"generated-alias","description":"generated description","team_id":"team-1","budget_id":"generated-project","litellm_budget_table":{"budget_id":"generated-project"}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/project/info":
			_, _ = writer.Write([]byte(`{"project_id":"project-created","project_alias":"generated-alias","description":"generated description","team_id":"team-1","budget_id":"generated-project","litellm_budget_table":{"budget_id":"generated-project","model_max_budget":{"gpt-4o":{"max_budget":1}}}}`))
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
		{"project", "litellm_project", "/project/new", map[string]interface{}{"team_id": "team-1"}, map[string]interface{}{"id": tftypes.UnknownValue, "project_alias": tftypes.UnknownValue, "description": tftypes.UnknownValue, "models": tftypes.UnknownValue, "metadata": tftypes.UnknownValue, "tags": tftypes.UnknownValue, "budget_id": tftypes.UnknownValue, "model_max_budget": tftypes.UnknownValue, "model_rpm_limit": tftypes.UnknownValue, "model_tpm_limit": tftypes.UnknownValue, "blocked": tftypes.UnknownValue, "created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue, "created_by": tftypes.UnknownValue, "updated_by": tftypes.UnknownValue}},
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
