package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestBudgetTableStateDistinguishesAbsentNullPresentAndMalformed(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		object   map[string]interface{}
		presence apiValuePresence
		wantErr  bool
	}{
		{"absent", map[string]interface{}{}, apiValueAbsent, false},
		{"null", map[string]interface{}{"litellm_budget_table": nil}, apiValueNull, false},
		{"present", map[string]interface{}{"litellm_budget_table": map[string]interface{}{}}, apiValuePresent, false},
		{"list", map[string]interface{}{"litellm_budget_table": []interface{}{}}, apiValueAbsent, true},
		{"scalar", map[string]interface{}{"litellm_budget_table": "bad"}, apiValueAbsent, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			state, err := parseBudgetTable(test.object)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %t", err, test.wantErr)
			}
			if !test.wantErr && state.presence != test.presence {
				t.Fatalf("presence = %v, want %v", state.presence, test.presence)
			}
		})
	}
}

func TestBudgetTableIdentityValidation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		object  map[string]interface{}
		want    string
		wantErr bool
	}{
		{"matching", map[string]interface{}{"budget_id": "b-1", "litellm_budget_table": map[string]interface{}{"budget_id": "b-1"}}, "b-1", false},
		{"nested only", map[string]interface{}{"litellm_budget_table": map[string]interface{}{"budget_id": "b-2"}}, "b-2", false},
		{"top only", map[string]interface{}{"budget_id": "b-3", "litellm_budget_table": map[string]interface{}{}}, "b-3", false},
		{"mismatch", map[string]interface{}{"budget_id": "b-1", "litellm_budget_table": map[string]interface{}{"budget_id": "b-2"}}, "", true},
		{"top null nested present", map[string]interface{}{"budget_id": nil, "litellm_budget_table": map[string]interface{}{"budget_id": "b-2"}}, "", true},
		{"top present nested null", map[string]interface{}{"budget_id": "b-1", "litellm_budget_table": map[string]interface{}{"budget_id": nil}}, "", true},
		{"empty", map[string]interface{}{"budget_id": "", "litellm_budget_table": nil}, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			table, err := parseBudgetTable(test.object)
			if err != nil {
				t.Fatal(err)
			}
			got, _, err := budgetTableID(test.object, table)
			if (err != nil) != test.wantErr || got != test.want {
				t.Fatalf("got %q, err %v; want %q, err=%t", got, err, test.want, test.wantErr)
			}
		})
	}
}

func TestOrganizationNestedBudgetIsAuthoritativeAndExact(t *testing.T) {
	t.Parallel()
	server, client := jsonServer(t, map[string]interface{}{
		"data": map[string]interface{}{
			"organization_id": "org-1", "organization_alias": "acme", "budget_id": "budget-1",
			"max_budget": 999, "tpm_limit": 1, "rpm_limit": 2,
			"blocked": true, "tags": []interface{}{"phantom"},
			"metadata": map[string]interface{}{
				"environment": "production", "model_rpm_limit": map[string]interface{}{"gpt": int64(9007199254740993)}, "model_tpm_limit": map[string]interface{}{"gpt": int64(9007199254740995)},
			},
			"litellm_budget_table": map[string]interface{}{
				"budget_id": "budget-1", "max_budget": 100.5, "soft_budget": 80.5,
				"tpm_limit": int64(9007199254740993), "rpm_limit": int64(9007199254740995), "max_parallel_requests": int64(9007199254740997),
				"budget_duration": "30d", "model_max_budget": map[string]interface{}{"gpt": 12.5},
			},
		},
	})
	defer server.Close()
	data := OrganizationResourceModel{
		OrganizationID: types.StringValue("org-1"), BudgetID: types.StringNull(),
		Models: types.ListUnknown(types.StringType), Metadata: types.MapUnknown(types.StringType),
		ModelMaxBudget: types.MapUnknown(types.Float64Type), ModelRPMLimit: types.MapUnknown(types.Int64Type), ModelTPMLimit: types.MapUnknown(types.Int64Type),
		Blocked: types.BoolUnknown(), Tags: types.ListUnknown(types.StringType),
	}
	if err := (&OrganizationResource{client: client}).readOrganizationWithNumericOwnership(context.Background(), &data, true); err != nil {
		t.Fatal(err)
	}
	if data.TPMLimit.ValueInt64() != 9007199254740993 || data.RPMLimit.ValueInt64() != 9007199254740995 || data.MaxParallelRequests.ValueInt64() != 9007199254740997 {
		t.Fatalf("exact nested integers lost: %#v %#v %#v", data.TPMLimit, data.RPMLimit, data.MaxParallelRequests)
	}
	if data.MaxBudget.ValueFloat64() != 100.5 || data.SoftBudget.ValueFloat64() != 80.5 || data.BudgetDuration.ValueString() != "30d" {
		t.Fatalf("nested budget state = max %#v soft %#v duration %#v", data.MaxBudget, data.SoftBudget, data.BudgetDuration)
	}
	if data.BudgetID.ValueString() != "budget-1" {
		t.Fatalf("budget_id = %#v", data.BudgetID)
	}
	if data.Blocked.ValueBool() || len(data.Tags.Elements()) != 0 {
		t.Fatalf("phantom organization fields were adopted: blocked=%#v tags=%#v", data.Blocked, data.Tags)
	}
	var rpm map[string]int64
	if diagnostics := data.ModelRPMLimit.ElementsAs(context.Background(), &rpm, false); diagnostics.HasError() || rpm["gpt"] != 9007199254740993 {
		t.Fatalf("exact metadata map = %#v (%v)", rpm, diagnostics)
	}
}

func TestOrganizationOmittedBudgetDoesNotAdoptDefaults(t *testing.T) {
	t.Parallel()
	server, client := jsonServer(t, map[string]interface{}{
		"organization_id": "org-1", "organization_alias": "acme", "budget_id": "generated",
		"litellm_budget_table": map[string]interface{}{"budget_id": "generated", "max_budget": 100, "tpm_limit": int64(9007199254740993), "budget_duration": "30d"},
	})
	defer server.Close()
	data := OrganizationResourceModel{
		OrganizationID: types.StringValue("org-1"), BudgetID: types.StringNull(), MaxBudget: types.Float64Null(), TPMLimit: types.Int64Null(), BudgetDuration: types.StringNull(),
		Models: types.ListNull(types.StringType), Metadata: types.MapNull(types.StringType), ModelMaxBudget: types.MapNull(types.Float64Type), ModelRPMLimit: types.MapNull(types.Int64Type), ModelTPMLimit: types.MapNull(types.Int64Type),
		Blocked: types.BoolNull(), Tags: types.ListNull(types.StringType),
	}
	if err := (&OrganizationResource{client: client}).readOrganization(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	if !data.BudgetID.IsNull() || !data.MaxBudget.IsNull() || !data.TPMLimit.IsNull() || !data.BudgetDuration.IsNull() {
		t.Fatalf("unmanaged API defaults were adopted: budget=%#v max=%#v tpm=%#v duration=%#v", data.BudgetID, data.MaxBudget, data.TPMLimit, data.BudgetDuration)
	}
}

func TestConfiguredOrganizationBudgetTracksPresentNullAndAbsentDrift(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		relation interface{}
		include  bool
		wantNull bool
		want     float64
	}{
		{"present drift", map[string]interface{}{"max_budget": 125.0}, true, false, 125},
		{"explicit null relation", nil, true, true, 0},
		{"absent relation", nil, false, true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := map[string]interface{}{"organization_id": "org-1", "organization_alias": "acme"}
			if test.include {
				response["litellm_budget_table"] = test.relation
			}
			server, client := jsonServer(t, response)
			defer server.Close()
			data := OrganizationResourceModel{OrganizationID: types.StringValue("org-1"), MaxBudget: types.Float64Value(100)}
			if err := (&OrganizationResource{client: client}).readOrganization(context.Background(), &data); err != nil {
				t.Fatal(err)
			}
			if data.MaxBudget.IsNull() != test.wantNull || (!test.wantNull && data.MaxBudget.ValueFloat64() != test.want) {
				t.Fatalf("max budget = %#v", data.MaxBudget)
			}
		})
	}
}

func TestOrganizationMalformedBudgetRelationFailsBeforeStatePublication(t *testing.T) {
	t.Parallel()
	server, client := jsonServer(t, map[string]interface{}{"organization_id": "org-1", "organization_alias": "acme", "litellm_budget_table": []interface{}{}})
	defer server.Close()
	prior := types.Float64Value(42)
	data := OrganizationResourceModel{OrganizationID: types.StringValue("org-1"), MaxBudget: prior}
	if err := (&OrganizationResource{client: client}).readOrganization(context.Background(), &data); err == nil {
		t.Fatal("malformed relation was accepted")
	}
	if !data.MaxBudget.Equal(prior) {
		t.Fatalf("malformed response partially mutated budget state: %#v", data.MaxBudget)
	}
}

func TestProjectDataSourceReadsNestedBudgetAndMetadataTags(t *testing.T) {
	t.Parallel()
	server, client := jsonServer(t, map[string]interface{}{
		"data": map[string]interface{}{
			"project_id": "project-1", "team_id": "team-1", "budget_id": "budget-1", "blocked": false,
			"metadata":             map[string]interface{}{"tags": []interface{}{"production"}, "model_rpm_limit": map[string]interface{}{"gpt": int64(9007199254740993)}, "model_tpm_limit": map[string]interface{}{"gpt": int64(9007199254740995)}},
			"litellm_budget_table": map[string]interface{}{"budget_id": "budget-1", "max_budget": 100, "tpm_limit": int64(9007199254740997), "rpm_limit": int64(9007199254740999), "budget_duration": "7d"},
		},
	})
	defer server.Close()
	dataSource := &ProjectDataSource{client: client}
	var schemaResponse datasource.SchemaResponse
	dataSource.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
	raw, err := tftypes.ValueFromJSON([]byte(`{"id":"project-1","project_alias":null,"description":null,"team_id":null,"models":null,"metadata":null,"tags":null,"blocked":null,"spend":null,"budget_id":null,"max_budget":null,"soft_budget":null,"budget_duration":null,"tpm_limit":null,"rpm_limit":null,"max_parallel_requests":null,"model_max_budget":null,"model_rpm_limit":null,"model_tpm_limit":null,"created_at":null,"updated_at":null,"created_by":null,"updated_by":null}`), schemaResponse.Schema.Type().TerraformType(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	config := tfsdk.Config{Raw: raw, Schema: schemaResponse.Schema}
	response := &datasource.ReadResponse{State: tfsdk.State{Raw: raw, Schema: schemaResponse.Schema}}
	dataSource.Read(context.Background(), datasource.ReadRequest{Config: config}, response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var state ProjectDataSourceModel
	if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if state.BudgetID.ValueString() != "budget-1" || state.TPMLimit.ValueInt64() != 9007199254740997 || state.RPMLimit.ValueInt64() != 9007199254740999 || state.BudgetDuration.ValueString() != "7d" {
		t.Fatalf("nested project budget = %#v", state)
	}
	if len(state.Tags.Elements()) != 1 {
		t.Fatalf("metadata tags = %#v", state.Tags)
	}
}

func TestOrganizationAndProjectListsInventoryNestedBudgets(t *testing.T) {
	t.Parallel()
	t.Run("organizations", func(t *testing.T) {
		server, client := jsonServer(t, []interface{}{map[string]interface{}{"organization_id": "org-1", "organization_alias": "acme", "budget_id": "budget-1", "metadata": map[string]interface{}{"model_rpm_limit": map[string]interface{}{"gpt": int64(9007199254740993)}}, "litellm_budget_table": map[string]interface{}{"budget_id": "budget-1", "tpm_limit": int64(9007199254740995), "budget_duration": "30d"}}})
		defer server.Close()
		dataSource := &OrganizationsListDataSource{client: client}
		var schemaResponse datasource.SchemaResponse
		dataSource.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
		raw, err := tftypes.ValueFromJSON([]byte(`{"id":null,"org_alias":null,"organizations":null}`), schemaResponse.Schema.Type().TerraformType(context.Background()))
		if err != nil {
			t.Fatal(err)
		}
		config := tfsdk.Config{Raw: raw, Schema: schemaResponse.Schema}
		response := &datasource.ReadResponse{State: tfsdk.State{Raw: raw, Schema: schemaResponse.Schema}}
		dataSource.Read(context.Background(), datasource.ReadRequest{Config: config}, response)
		if response.Diagnostics.HasError() {
			t.Fatal(response.Diagnostics)
		}
		var state OrganizationsListDataSourceModel
		if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
			t.Fatal(diagnostics)
		}
		if len(state.Organizations) != 1 || state.Organizations[0].TPMLimit.ValueInt64() != 9007199254740995 || state.Organizations[0].BudgetDuration.ValueString() != "30d" {
			t.Fatalf("organization inventory = %#v", state.Organizations)
		}
	})
	t.Run("projects", func(t *testing.T) {
		server, client := jsonServer(t, []interface{}{map[string]interface{}{"project_id": "project-1", "team_id": "team-1", "budget_id": "budget-1", "metadata": map[string]interface{}{"model_tpm_limit": map[string]interface{}{"gpt": int64(9007199254740993)}}, "litellm_budget_table": map[string]interface{}{"budget_id": "budget-1", "rpm_limit": int64(9007199254740995), "budget_duration": "7d"}}})
		defer server.Close()
		dataSource := &ProjectsListDataSource{client: client}
		var schemaResponse datasource.SchemaResponse
		dataSource.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResponse)
		raw, err := tftypes.ValueFromJSON([]byte(`{"id":null,"projects":null}`), schemaResponse.Schema.Type().TerraformType(context.Background()))
		if err != nil {
			t.Fatal(err)
		}
		config := tfsdk.Config{Raw: raw, Schema: schemaResponse.Schema}
		response := &datasource.ReadResponse{State: tfsdk.State{Raw: raw, Schema: schemaResponse.Schema}}
		dataSource.Read(context.Background(), datasource.ReadRequest{Config: config}, response)
		if response.Diagnostics.HasError() {
			t.Fatal(response.Diagnostics)
		}
		var state ProjectsListDataSourceModel
		if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
			t.Fatal(diagnostics)
		}
		if len(state.Projects) != 1 || state.Projects[0].RPMLimit.ValueInt64() != 9007199254740995 || state.Projects[0].BudgetDuration.ValueString() != "7d" {
			t.Fatalf("project inventory = %#v", state.Projects)
		}
	})
}

func TestOrganizationUpdateUsesV2ReplacementAndExplicitBudgetClears(t *testing.T) {
	t.Parallel()
	state := OrganizationResourceModel{
		OrganizationAlias: types.StringValue("acme"), MaxBudget: types.Float64Value(100), SoftBudget: types.Float64Value(80), BudgetDuration: types.StringValue("30d"),
		Metadata:      types.MapValueMust(types.StringType, map[string]attr.Value{"environment": types.StringValue("prod")}),
		ModelRPMLimit: types.MapValueMust(types.Int64Type, map[string]attr.Value{"a": types.Int64Value(1), "b": types.Int64Value(2)}),
		ModelTPMLimit: types.MapValueMust(types.Int64Type, map[string]attr.Value{"a": types.Int64Value(10)}), ModelMaxBudget: types.MapNull(types.Float64Type),
		Models: types.ListNull(types.StringType), Tags: types.ListNull(types.StringType),
	}
	plan := state
	plan.MaxBudget = types.Float64Null()
	plan.SoftBudget = types.Float64Null()
	plan.BudgetDuration = types.StringNull()
	plan.ModelRPMLimit = types.MapValueMust(types.Int64Type, map[string]attr.Value{"a": types.Int64Value(3)})
	request, err := buildOrganizationUpdateRequest(context.Background(), &plan, &state)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"max_budget", "soft_budget", "budget_duration"} {
		if value, exists := request[field]; !exists || value != nil {
			t.Fatalf("%s clear = %#v (present %t)", field, value, exists)
		}
	}
	metadata := request["metadata"].(map[string]interface{})
	rpm := metadata["model_rpm_limit"].(map[string]int64)
	if len(rpm) != 1 || rpm["a"] != 3 {
		t.Fatalf("complete replacement RPM map = %#v", rpm)
	}
	if _, exists := rpm["b"]; exists {
		t.Fatal("removed owned RPM key survived replacement payload")
	}
}

func TestProjectBudgetClearPayloadClearsResetSchedule(t *testing.T) {
	t.Parallel()
	state := ProjectResourceModel{MaxBudget: types.Float64Value(100), BudgetDuration: types.StringValue("30d"), ModelMaxBudget: types.MapValueMust(types.Float64Type, map[string]attr.Value{"gpt": types.Float64Value(5)})}
	plan := state
	plan.MaxBudget = types.Float64Null()
	plan.BudgetDuration = types.StringNull()
	plan.ModelMaxBudget = types.MapNull(types.Float64Type)
	request, changed, err := buildProjectBudgetUpdateRequest(&plan, &state)
	if err != nil || !changed {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
	for _, field := range []string{"max_budget", "budget_duration", "budget_reset_at", "model_max_budget"} {
		if value, exists := request[field]; !exists || value != nil {
			t.Fatalf("%s clear = %#v (present %t)", field, value, exists)
		}
	}
}

func organizationBudgetTestSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&OrganizationResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	return response.Schema
}

func organizationBudgetTestState(t *testing.T, schema resourceschema.Schema, data OrganizationResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := state.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	return state
}

func organizationBudgetTestPlan(t *testing.T, schema resourceschema.Schema, data OrganizationResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := plan.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	return plan
}

func typedOrganizationBudgetModel() OrganizationResourceModel {
	return OrganizationResourceModel{
		ID: types.StringNull(), OrganizationID: types.StringNull(), OrganizationAlias: types.StringValue("acme"), Models: types.ListNull(types.StringType), BudgetID: types.StringNull(),
		MaxBudget: types.Float64Null(), SoftBudget: types.Float64Null(), TPMLimit: types.Int64Null(), RPMLimit: types.Int64Null(), MaxParallelRequests: types.Int64Null(),
		ModelMaxBudget: types.MapNull(types.Float64Type), ModelRPMLimit: types.MapNull(types.Int64Type), ModelTPMLimit: types.MapNull(types.Int64Type), BudgetDuration: types.StringNull(), Metadata: types.MapNull(types.StringType),
		Blocked: types.BoolNull(), Tags: types.ListNull(types.StringType), CreatedAt: types.StringNull(),
	}
}

func TestOrganizationUpdateUsesV2EndpointAndConvergesMapRemoval(t *testing.T) {
	t.Parallel()
	var patchPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		object := `{"organization_id":"org-1","organization_alias":"acme","budget_id":"budget-1","metadata":{"environment":"prod","model_rpm_limit":{"a":3}},"litellm_budget_table":{"budget_id":"budget-1","max_budget":null}}`
		switch {
		case request.Method == http.MethodPatch && request.URL.Path == "/v2/organization/org-1":
			body, _ := io.ReadAll(request.Body)
			if err := decodeJSONUseNumber(body, &patchPayload); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(object))
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			_, _ = w.Write([]byte(object))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	prior := typedOrganizationBudgetModel()
	prior.ID, prior.OrganizationID = types.StringValue("org-1"), types.StringValue("org-1")
	prior.MaxBudget = types.Float64Value(100)
	prior.Metadata = types.MapValueMust(types.StringType, map[string]attr.Value{"environment": types.StringValue("prod")})
	prior.ModelRPMLimit = types.MapValueMust(types.Int64Type, map[string]attr.Value{"a": types.Int64Value(1), "b": types.Int64Value(2)})
	prior.Blocked = types.BoolValue(false)
	prior.Tags = types.ListValueMust(types.StringType, []attr.Value{})
	planModel := prior
	planModel.MaxBudget = types.Float64Null()
	planModel.ModelRPMLimit = types.MapValueMust(types.Int64Type, map[string]attr.Value{"a": types.Int64Value(3)})
	schema := organizationBudgetTestSchema(t)
	state := organizationBudgetTestState(t, schema, prior)
	plan := organizationBudgetTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}
	(&OrganizationResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}).Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if value, exists := patchPayload["max_budget"]; !exists || value != nil {
		t.Fatalf("max_budget clear = %#v", value)
	}
	metadata := patchPayload["metadata"].(map[string]interface{})
	rpm := metadata["model_rpm_limit"].(map[string]interface{})
	if len(rpm) != 1 {
		t.Fatalf("replacement RPM payload = %#v", rpm)
	}
}

func TestOrganizationModifyPlanRejectsPhantomValuesAndBudgetReassociation(t *testing.T) {
	t.Parallel()
	schema := organizationBudgetTestSchema(t)
	terraformType := schema.Type().TerraformType(context.Background())
	makeState := func(t *testing.T, model OrganizationResourceModel) tfsdk.State {
		value := tfsdk.State{Raw: tftypes.NewValue(terraformType, nil), Schema: schema}
		if diagnostics := value.Set(context.Background(), &model); diagnostics.HasError() {
			t.Fatal(diagnostics)
		}
		return value
	}
	makePlan := func(t *testing.T, model OrganizationResourceModel) tfsdk.Plan {
		value := tfsdk.Plan{Raw: tftypes.NewValue(terraformType, nil), Schema: schema}
		if diagnostics := value.Set(context.Background(), &model); diagnostics.HasError() {
			t.Fatal(diagnostics)
		}
		return value
	}
	makeConfig := func(t *testing.T, model OrganizationResourceModel) tfsdk.Config {
		state := makeState(t, model)
		return tfsdk.Config{Raw: state.Raw, Schema: schema}
	}

	unsupported := typedOrganizationBudgetModel()
	unsupported.Blocked = types.BoolValue(true)
	unsupported.Tags = types.ListValueMust(types.StringType, []attr.Value{types.StringValue("phantom")})
	plan := makePlan(t, unsupported)
	nullState := tfsdk.State{Raw: tftypes.NewValue(terraformType, nil), Schema: schema}
	response := &resource.ModifyPlanResponse{Plan: plan}
	(&OrganizationResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: nullState, Plan: plan, Config: makeConfig(t, unsupported)}, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("unsupported organization phantom values were accepted")
	}

	prior := typedOrganizationBudgetModel()
	prior.ID, prior.OrganizationID, prior.BudgetID = types.StringValue("org-1"), types.StringValue("org-1"), types.StringValue("budget-1")
	reassociated := prior
	reassociated.BudgetID = types.StringValue("budget-2")
	plan = makePlan(t, reassociated)
	response = &resource.ModifyPlanResponse{Plan: plan}
	(&OrganizationResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: makeState(t, prior), Plan: plan, Config: makeConfig(t, reassociated)}, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("budget reassociation was accepted")
	}
}

func projectBudgetTestSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&ProjectResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func projectBudgetTestState(t *testing.T, schema resourceschema.Schema, data ProjectResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := state.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set state: %v", diagnostics)
	}
	return state
}

func projectBudgetTestPlan(t *testing.T, schema resourceschema.Schema, data ProjectResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := plan.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set plan: %v", diagnostics)
	}
	return plan
}

func typedProjectBudgetModel() ProjectResourceModel {
	return ProjectResourceModel{
		ID: types.StringValue("project-1"), TeamID: types.StringValue("team-1"),
		ProjectAlias: types.StringNull(), Description: types.StringNull(), Models: types.ListNull(types.StringType), Metadata: types.MapNull(types.StringType), Tags: types.ListNull(types.StringType),
		MaxBudget: types.Float64Null(), SoftBudget: types.Float64Null(), BudgetDuration: types.StringNull(), BudgetID: types.StringNull(), TPMLimit: types.Int64Null(), RPMLimit: types.Int64Null(), MaxParallelRequests: types.Int64Null(),
		ModelMaxBudget: types.MapNull(types.Float64Type), ModelRPMLimit: types.MapNull(types.Int64Type), ModelTPMLimit: types.MapNull(types.Int64Type), Blocked: types.BoolNull(),
		CreatedAt: types.StringNull(), UpdatedAt: types.StringNull(), CreatedBy: types.StringNull(), UpdatedBy: types.StringNull(),
	}
}

func TestProjectAcceptedButIgnoredBudgetClearRetainsPriorState(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/project/info":
			_, _ = w.Write([]byte(`{"project_id":"project-1","team_id":"team-1","budget_id":"budget-1","litellm_budget_table":{"budget_id":"budget-1","max_budget":100}}`))
		case "/budget/update":
			_, _ = w.Write([]byte(`{"budget_id":"budget-1","max_budget":null}`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	prior := typedProjectBudgetModel()
	prior.MaxBudget = types.Float64Value(100)
	planModel := prior
	planModel.MaxBudget = types.Float64Null()
	schema := projectBudgetTestSchema(t)
	state := projectBudgetTestState(t, schema, prior)
	plan := projectBudgetTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}
	(&ProjectResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}).Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("ignored budget clear was treated as converged")
	}
	var retained ProjectResourceModel
	if diagnostics := response.State.Get(context.Background(), &retained); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if retained.MaxBudget.ValueFloat64() != 100 {
		t.Fatalf("ignored clear published planned state: %#v", retained.MaxBudget)
	}
}

func TestProjectAcceptedBudgetResponseWithWrongIdentityRetainsPriorState(t *testing.T) {
	t.Parallel()
	var budgetPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/project/info":
			_, _ = w.Write([]byte(`{"project_id":"project-1","team_id":"team-1","budget_id":"budget-1","litellm_budget_table":{"budget_id":"budget-1","max_budget":100}}`))
		case "/budget/update":
			body, _ := io.ReadAll(request.Body)
			if err := decodeJSONUseNumber(body, &budgetPayload); err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(`{"budget_id":"wrong-budget","max_budget":null}`))
		case "/project/update":
			t.Fatal("budget-only update called /project/update")
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	prior := typedProjectBudgetModel()
	prior.MaxBudget = types.Float64Value(100)
	planModel := prior
	planModel.MaxBudget = types.Float64Null()
	schema := projectBudgetTestSchema(t)
	state := projectBudgetTestState(t, schema, prior)
	plan := projectBudgetTestPlan(t, schema, planModel)
	response := &resource.UpdateResponse{State: state}
	(&ProjectResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}).Update(context.Background(), resource.UpdateRequest{State: state, Plan: plan}, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("accepted wrong-identity budget response was treated as success")
	}
	if value, exists := budgetPayload["max_budget"]; !exists || value != nil {
		t.Fatalf("max_budget clear payload = %#v (present %t)", value, exists)
	}
	if budgetPayload["budget_id"] != "budget-1" {
		t.Fatalf("budget identity payload = %#v", budgetPayload["budget_id"])
	}
	var retained ProjectResourceModel
	if diagnostics := response.State.Get(context.Background(), &retained); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if retained.MaxBudget.ValueFloat64() != 100 {
		t.Fatalf("accepted partial failure published planned state: %#v", retained.MaxBudget)
	}
}
