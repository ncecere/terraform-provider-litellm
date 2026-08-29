package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestOrganizationProjectModifyPlanTransitionsAndFullyNullDestroy(t *testing.T) {
	t.Parallel()
	t.Run("organization", func(t *testing.T) {
		schema := organizationBudgetTestSchema(t)
		prior := typedOrganizationBudgetModel()
		prior.ID, prior.OrganizationID, prior.BudgetID = types.StringValue("org-1"), types.StringValue("org-1"), types.StringValue("budget-1")
		for _, test := range []struct {
			name      string
			state     types.String
			plan      types.String
			config    types.String
			wantError bool
		}{
			{"configured removal", types.StringValue("budget-1"), types.StringValue("budget-1"), types.StringNull(), true},
			{"configured change", types.StringValue("budget-1"), types.StringValue("budget-2"), types.StringValue("budget-2"), true},
			{"unknown transition", types.StringValue("budget-1"), types.StringUnknown(), types.StringUnknown(), true},
			{"null to known", types.StringNull(), types.StringValue("budget-1"), types.StringValue("budget-1"), true},
			{"ordinary omission remains null", types.StringNull(), types.StringUnknown(), types.StringNull(), false},
		} {
			t.Run(test.name, func(t *testing.T) {
				stateModel, planModel, configModel := prior, prior, prior
				stateModel.BudgetID, planModel.BudgetID, configModel.BudgetID = test.state, test.plan, test.config
				plan := organizationBudgetTestPlan(t, schema, planModel)
				response := &resource.ModifyPlanResponse{Plan: plan}
				(&OrganizationResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: organizationBudgetTestState(t, schema, stateModel), Plan: plan, Config: organizationBudgetTestConfig(t, schema, configModel)}, response)
				if response.Diagnostics.HasError() != test.wantError {
					t.Fatalf("diagnostics=%v, wantError=%t", response.Diagnostics, test.wantError)
				}
			})
		}
		fullyNull := typedOrganizationBudgetModel()
		fullyNull.OrganizationAlias = types.StringNull()
		plan := organizationBudgetTestPlan(t, schema, fullyNull)
		response := &resource.ModifyPlanResponse{Plan: plan}
		(&OrganizationResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: organizationBudgetTestState(t, schema, prior), Plan: plan, Config: organizationBudgetTestConfig(t, schema, fullyNull)}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("fully-null destroy reached field checks: %v", response.Diagnostics)
		}
	})

	t.Run("project", func(t *testing.T) {
		schema := projectBudgetTestSchema(t)
		prior := typedProjectBudgetModel()
		prior.BudgetID = types.StringValue("budget-1")
		for _, test := range []struct {
			name      string
			mutate    func(*ProjectResourceModel, *ProjectResourceModel, *ProjectResourceModel)
			wantError bool
		}{
			{"configured budget removal", func(_, _, config *ProjectResourceModel) { config.BudgetID = types.StringNull() }, true},
			{"configured budget change", func(_, plan, config *ProjectResourceModel) {
				plan.BudgetID, config.BudgetID = types.StringValue("budget-2"), types.StringValue("budget-2")
			}, true},
			{"unknown budget transition", func(_, plan, config *ProjectResourceModel) {
				plan.BudgetID, config.BudgetID = types.StringUnknown(), types.StringUnknown()
			}, true},
			{"configured alias removal", func(state, _, config *ProjectResourceModel) {
				state.ProjectAlias, config.ProjectAlias = types.StringValue("configured"), types.StringNull()
			}, true},
		} {
			t.Run(test.name, func(t *testing.T) {
				stateModel, planModel, configModel := prior, prior, prior
				test.mutate(&stateModel, &planModel, &configModel)
				plan := projectBudgetTestPlan(t, schema, planModel)
				response := &resource.ModifyPlanResponse{Plan: plan}
				(&ProjectResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: projectBudgetTestState(t, schema, stateModel), Plan: plan, Config: projectBudgetTestConfig(t, schema, configModel)}, response)
				if response.Diagnostics.HasError() != test.wantError {
					t.Fatalf("diagnostics=%v, wantError=%t", response.Diagnostics, test.wantError)
				}
			})
		}
		legacyState, legacyPlan, legacyConfig := prior, prior, prior
		legacyState.ModelMaxBudget = types.MapValueMust(types.Float64Type, map[string]attr.Value{"gpt": types.Float64Value(1)})
		legacyPlan.ModelMaxBudget = types.MapUnknown(types.Float64Type)
		legacyConfig.ModelMaxBudget = types.MapNull(types.Float64Type)
		legacyPlanValue := projectBudgetTestPlan(t, schema, legacyPlan)
		legacyResponse := &resource.ModifyPlanResponse{Plan: legacyPlanValue}
		(&ProjectResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: projectBudgetTestState(t, schema, legacyState), Plan: legacyPlanValue, Config: projectBudgetTestConfig(t, schema, legacyConfig)}, legacyResponse)
		var plannedLegacy ProjectResourceModel
		if legacyResponse.Diagnostics.HasError() {
			t.Fatalf("legacy model budget removal was blocked: %v", legacyResponse.Diagnostics)
		}
		if diagnostics := legacyResponse.Plan.Get(context.Background(), &plannedLegacy); diagnostics.HasError() || !plannedLegacy.ModelMaxBudget.IsNull() {
			t.Fatalf("legacy model budget removal plan=%#v diagnostics=%v", plannedLegacy.ModelMaxBudget, diagnostics)
		}

		fullyNull := typedProjectBudgetModel()
		fullyNull.ID, fullyNull.TeamID = types.StringNull(), types.StringNull()
		plan := projectBudgetTestPlan(t, schema, fullyNull)
		response := &resource.ModifyPlanResponse{Plan: plan}
		(&ProjectResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: projectBudgetTestState(t, schema, prior), Plan: plan, Config: projectBudgetTestConfig(t, schema, fullyNull)}, response)
		if response.Diagnostics.HasError() {
			t.Fatalf("fully-null destroy reached field checks: %v", response.Diagnostics)
		}
	})
}

func TestOrganizationProjectCreateRejectsSharedBudgetControls(t *testing.T) {
	t.Parallel()
	organizationControls := []struct {
		name string
		set  func(*OrganizationResourceModel)
	}{
		{"max_budget", func(data *OrganizationResourceModel) { data.MaxBudget = types.Float64Value(1) }},
		{"soft_budget", func(data *OrganizationResourceModel) { data.SoftBudget = types.Float64Value(1) }},
		{"budget_duration", func(data *OrganizationResourceModel) { data.BudgetDuration = types.StringValue("30d") }},
		{"tpm_limit", func(data *OrganizationResourceModel) { data.TPMLimit = types.Int64Value(1) }},
		{"rpm_limit", func(data *OrganizationResourceModel) { data.RPMLimit = types.Int64Value(1) }},
		{"max_parallel_requests", func(data *OrganizationResourceModel) { data.MaxParallelRequests = types.Int64Value(1) }},
	}
	for _, test := range organizationControls {
		t.Run("organization "+test.name, func(t *testing.T) {
			data := typedOrganizationBudgetModel()
			data.BudgetID = types.StringValue("shared-budget")
			test.set(&data)
			if _, err := (&OrganizationResource{}).buildOrganizationCreateRequest(context.Background(), &data); err == nil || !strings.Contains(err.Error(), "budget_id") {
				t.Fatalf("shared budget control accepted: %v", err)
			}
		})
	}
	projectControls := []struct {
		name string
		set  func(*ProjectResourceModel)
	}{
		{"max_budget", func(data *ProjectResourceModel) { data.MaxBudget = types.Float64Value(1) }},
		{"soft_budget", func(data *ProjectResourceModel) { data.SoftBudget = types.Float64Value(1) }},
		{"budget_duration", func(data *ProjectResourceModel) { data.BudgetDuration = types.StringValue("30d") }},
		{"tpm_limit", func(data *ProjectResourceModel) { data.TPMLimit = types.Int64Value(1) }},
		{"rpm_limit", func(data *ProjectResourceModel) { data.RPMLimit = types.Int64Value(1) }},
		{"max_parallel_requests", func(data *ProjectResourceModel) { data.MaxParallelRequests = types.Int64Value(1) }},
		{"model_max_budget", func(data *ProjectResourceModel) {
			data.ModelMaxBudget = types.MapValueMust(types.Float64Type, map[string]attr.Value{})
		}},
	}
	for _, test := range projectControls {
		t.Run("project "+test.name, func(t *testing.T) {
			data := typedProjectBudgetModel()
			data.BudgetID = types.StringValue("shared-budget")
			test.set(&data)
			if _, err := (&ProjectResource{}).buildProjectCreateRequest(context.Background(), &data); err == nil || !strings.Contains(err.Error(), "budget_id") {
				t.Fatalf("shared budget control accepted: %v", err)
			}
		})
	}
}

func TestOrganizationBudgetUpdatePreflightBlocksOutOfBandReassociation(t *testing.T) {
	t.Parallel()
	var patchCalls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodGet && request.URL.Path == "/organization/info" {
			_, _ = writer.Write([]byte(`{"organization_id":"org-1","organization_alias":"acme","budget_id":"budget-2","litellm_budget_table":{"budget_id":"budget-2","max_budget":100}}`))
			return
		}
		if request.Method == http.MethodPatch {
			patchCalls.Add(1)
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	prior := typedOrganizationBudgetModel()
	prior.ID, prior.OrganizationID, prior.BudgetID, prior.MaxBudget = types.StringValue("org-1"), types.StringValue("org-1"), types.StringValue("budget-1"), types.Float64Value(100)
	planModel := prior
	planModel.MaxBudget = types.Float64Value(200)
	schema := organizationBudgetTestSchema(t)
	state, plan := organizationBudgetTestState(t, schema, prior), organizationBudgetTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}
	(&OrganizationResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}).Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, response)
	if !response.Diagnostics.HasError() || patchCalls.Load() != 0 {
		t.Fatalf("stale budget mutation not blocked: diagnostics=%v patches=%d", response.Diagnostics, patchCalls.Load())
	}
}

func TestOrganizationProjectStructuredModelBudgetSurfaceIsDeferred(t *testing.T) {
	t.Parallel()
	var organizationSchema, projectSchema resource.SchemaResponse
	(&OrganizationResource{}).Schema(context.Background(), resource.SchemaRequest{}, &organizationSchema)
	(&ProjectResource{}).Schema(context.Background(), resource.SchemaRequest{}, &projectSchema)
	if organizationSchema.Schema.Version != 1 || projectSchema.Schema.Version != 1 {
		t.Fatalf("schema versions changed: organization=%d project=%d", organizationSchema.Schema.Version, projectSchema.Schema.Version)
	}
	for name, response := range map[string]resource.SchemaResponse{"organization": organizationSchema, "project": projectSchema} {
		budget, ok := response.Schema.Attributes["budget_id"].(resourceschema.StringAttribute)
		if !ok || !budget.Optional || !budget.Computed {
			t.Fatalf("%s budget_id schema=%#v; want Optional+Computed string", name, response.Schema.Attributes["budget_id"])
		}
	}
	if _, exposed := organizationSchema.Schema.Attributes["model_max_budget"]; exposed {
		t.Fatal("organization resource exposed inaccurate map(float64) model_max_budget")
	}
	for _, test := range []struct {
		name, listAttribute string
		dataSource          interface {
			Schema(context.Context, datasource.SchemaRequest, *datasource.SchemaResponse)
		}
	}{
		{"organization", "", &OrganizationDataSource{}}, {"organizations", "organizations", &OrganizationsListDataSource{}},
		{"project", "", &ProjectDataSource{}}, {"projects", "projects", &ProjectsListDataSource{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var response datasource.SchemaResponse
			test.dataSource.Schema(context.Background(), datasource.SchemaRequest{}, &response)
			if test.listAttribute == "" {
				if _, exposed := response.Schema.Attributes["model_max_budget"]; exposed {
					t.Fatal("data source exposed inaccurate model_max_budget")
				}
				return
			}
			list := response.Schema.Attributes[test.listAttribute].(datasourceschema.ListNestedAttribute)
			if _, exposed := list.NestedObject.Attributes["model_max_budget"]; exposed {
				t.Fatal("list data source exposed inaccurate model_max_budget")
			}
		})
	}
}

func TestProjectLegacyModelBudgetStructuredRemoteIsMigrationSafe(t *testing.T) {
	t.Parallel()
	server, client := jsonServer(t, map[string]interface{}{"project_id": "project-1", "team_id": "team-1", "litellm_budget_table": map[string]interface{}{"model_max_budget": map[string]interface{}{"gpt-4o": map[string]interface{}{"max_budget": 12.5, "budget_duration": "30d"}}}})
	defer server.Close()
	unconfigured := typedProjectBudgetModel()
	unconfigured.ModelMaxBudget = types.MapUnknown(types.Float64Type)
	if err := (&ProjectResource{client: client}).readProjectWithNumericOwnership(context.Background(), &unconfigured, true); err != nil || !unconfigured.ModelMaxBudget.IsNull() {
		t.Fatalf("structured import read: state=%#v error=%v", unconfigured.ModelMaxBudget, err)
	}
	legacyValue := types.MapValueMust(types.Float64Type, map[string]attr.Value{"gpt-4o": types.Float64Value(10)})
	legacy := typedProjectBudgetModel()
	legacy.ModelMaxBudget = legacyValue
	if err := (&ProjectResource{client: client}).readProject(context.Background(), &legacy); err != nil || !legacy.ModelMaxBudget.Equal(legacyValue) {
		t.Fatalf("structured refresh broke legacy state: state=%#v error=%v", legacy.ModelMaxBudget, err)
	}
	newConfiguration := typedProjectBudgetModel()
	newConfiguration.ModelMaxBudget = legacyValue
	if _, err := (&ProjectResource{}).buildProjectCreateRequest(context.Background(), &newConfiguration); err == nil || !strings.Contains(err.Error(), "GenericBudgetConfig") {
		t.Fatalf("known-inaccurate model budget accepted: %v", err)
	}
}
