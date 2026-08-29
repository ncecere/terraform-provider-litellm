package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

func accessGroupTestSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&AccessGroupResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func accessGroupTestState(t *testing.T, schema resourceschema.Schema, data AccessGroupResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := state.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set test state: %v", diagnostics)
	}
	return state
}

func accessGroupTestPlan(t *testing.T, schema resourceschema.Schema, data AccessGroupResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := plan.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set test plan: %v", diagnostics)
	}
	return plan
}

func accessGroupStringList(values ...string) types.List {
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.ListValueMust(types.StringType, elements)
}

func accessGroupListStrings(t *testing.T, value types.List) []string {
	t.Helper()
	var result []string
	if diagnostics := value.ElementsAs(context.Background(), &result, false); diagnostics.HasError() {
		t.Fatalf("decode string list: %v", diagnostics)
	}
	return result
}

func accessGroupProtocolDynamicValue(t *testing.T, schema *tfprotov6.Schema, value tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	dynamicValue, err := tfprotov6.NewDynamicValue(schema.ValueType(), value)
	if err != nil {
		t.Fatalf("encode protocol value: %v", err)
	}
	return &dynamicValue
}

func accessGroupProtocolDiagnosticsHaveError(diagnostics []*tfprotov6.Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
			return true
		}
	}
	return false
}

func TestAccessGroupModelNamesSchemaPreservesCompatibleListContract(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var response resource.SchemaResponse
	(&AccessGroupResource{}).Schema(ctx, resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	if response.Schema.Version != 0 {
		t.Fatalf("schema version = %d, want unchanged version 0", response.Schema.Version)
	}
	attribute, ok := response.Schema.Attributes["model_names"].(resourceschema.ListAttribute)
	if !ok {
		t.Fatalf("model_names schema type = %T, want schema.ListAttribute", response.Schema.Attributes["model_names"])
	}
	if !attribute.Required || attribute.Optional || attribute.Computed {
		t.Fatalf("model_names must remain required: %#v", attribute)
	}
	if got, want := attribute.GetType().TerraformType(ctx), (tftypes.List{ElementType: tftypes.String}); !got.Equal(want) {
		t.Fatalf("model_names Terraform type = %s, want %s", got, want)
	}

	for name, test := range map[string]struct {
		value     types.List
		wantError bool
	}{
		"null collection":      {value: types.ListNull(types.StringType), wantError: true},
		"empty collection":     {value: accessGroupStringList(), wantError: true},
		"null model name":      {value: types.ListValueMust(types.StringType, []attr.Value{types.StringNull()}), wantError: true},
		"empty model name":     {value: accessGroupStringList("")},
		"duplicate model name": {value: accessGroupStringList("gpt-4o", "gpt-4o")},
		"one model name":       {value: accessGroupStringList("gpt-4o")},
		"unknown collection":   {value: types.ListUnknown(types.StringType)},
		"unknown model name":   {value: types.ListValueMust(types.StringType, []attr.Value{types.StringUnknown()})},
	} {
		t.Run(name, func(t *testing.T) {
			var validationResponse validator.ListResponse
			request := validator.ListRequest{Path: path.Root("model_names"), ConfigValue: test.value}
			for _, listValidator := range attribute.Validators {
				listValidator.ValidateList(ctx, request, &validationResponse)
			}
			if got := validationResponse.Diagnostics.HasError(); got != test.wantError {
				t.Fatalf("validation error = %t, want %t: %v", got, test.wantError, validationResponse.Diagnostics)
			}
		})
	}
}

func TestAccessGroupDuplicateAndEmptyHCLSurvivesValidationAndPlan(t *testing.T) {
	t.Parallel()

	expression, diagnostics := hclsyntax.ParseExpression(
		[]byte(`["z-model", "", "a-model", "a-model", ""]`),
		"duplicates.tf",
		hcl.Pos{Line: 1, Column: 1},
	)
	if diagnostics.HasErrors() {
		t.Fatalf("parse duplicate HCL list: %v", diagnostics)
	}
	configured, diagnostics := expression.Value(nil)
	if diagnostics.HasErrors() {
		t.Fatalf("evaluate duplicate HCL list: %v", diagnostics)
	}
	var configuredNames []string
	iterator := configured.ElementIterator()
	for iterator.Next() {
		_, value := iterator.Element()
		configuredNames = append(configuredNames, value.AsString())
	}

	ctx := context.Background()
	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemaResponse, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("get provider schema: %v", err)
	}
	if accessGroupProtocolDiagnosticsHaveError(schemaResponse.Diagnostics) {
		t.Fatalf("provider schema diagnostics: %v", schemaResponse.Diagnostics)
	}
	resourceSchema := schemaResponse.ResourceSchemas["litellm_access_group"]
	if resourceSchema == nil {
		t.Fatal("litellm_access_group protocol schema is missing")
	}

	listType := tftypes.List{ElementType: tftypes.String}
	listElements := make([]tftypes.Value, 0, len(configuredNames))
	for _, name := range configuredNames {
		listElements = append(listElements, tftypes.NewValue(tftypes.String, name))
	}
	configuredModels := tftypes.NewValue(listType, listElements)
	resourceValue := func(id interface{}, modelNames tftypes.Value) tftypes.Value {
		return tftypes.NewValue(resourceSchema.ValueType(), map[string]tftypes.Value{
			"id":           tftypes.NewValue(tftypes.String, id),
			"access_group": tftypes.NewValue(tftypes.String, "duplicate-group"),
			"model_names":  modelNames,
		})
	}
	config := accessGroupProtocolDynamicValue(t, resourceSchema, resourceValue(nil, configuredModels))
	proposed := accessGroupProtocolDynamicValue(t, resourceSchema, resourceValue(tftypes.UnknownValue, configuredModels))

	validationResponse, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{
		TypeName: "litellm_access_group",
		Config:   config,
	})
	if err != nil {
		t.Fatalf("validate duplicate HCL config: %v", err)
	}
	if accessGroupProtocolDiagnosticsHaveError(validationResponse.Diagnostics) {
		t.Fatalf("duplicate/empty HCL validation diagnostics: %v", validationResponse.Diagnostics)
	}

	planResponse, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "litellm_access_group",
		Config:           config,
		PriorState:       accessGroupProtocolDynamicValue(t, resourceSchema, tftypes.NewValue(resourceSchema.ValueType(), nil)),
		ProposedNewState: proposed,
	})
	if err != nil {
		t.Fatalf("plan duplicate HCL config: %v", err)
	}
	if accessGroupProtocolDiagnosticsHaveError(planResponse.Diagnostics) {
		t.Fatalf("duplicate/empty HCL plan diagnostics: %v", planResponse.Diagnostics)
	}
	planned, err := planResponse.PlannedState.Unmarshal(resourceSchema.ValueType())
	if err != nil {
		t.Fatalf("decode planned state: %v", err)
	}
	var plannedAttributes map[string]tftypes.Value
	if err := planned.As(&plannedAttributes); err != nil {
		t.Fatalf("decode planned attributes: %v", err)
	}
	if !plannedAttributes["model_names"].Equal(configuredModels) {
		t.Fatalf("planned model_names = %s, want exact duplicate HCL value %s", plannedAttributes["model_names"], configuredModels)
	}

	unknownModels := tftypes.NewValue(listType, tftypes.UnknownValue)
	unknownConfig := accessGroupProtocolDynamicValue(t, resourceSchema, resourceValue(nil, unknownModels))
	unknownProposed := accessGroupProtocolDynamicValue(t, resourceSchema, resourceValue(tftypes.UnknownValue, unknownModels))
	unknownPlanResponse, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName:         "litellm_access_group",
		Config:           unknownConfig,
		PriorState:       accessGroupProtocolDynamicValue(t, resourceSchema, tftypes.NewValue(resourceSchema.ValueType(), nil)),
		ProposedNewState: unknownProposed,
	})
	if err != nil {
		t.Fatalf("plan unknown model_names: %v", err)
	}
	if accessGroupProtocolDiagnosticsHaveError(unknownPlanResponse.Diagnostics) {
		t.Fatalf("unknown model_names plan diagnostics: %v", unknownPlanResponse.Diagnostics)
	}
	unknownPlanned, err := unknownPlanResponse.PlannedState.Unmarshal(resourceSchema.ValueType())
	if err != nil {
		t.Fatalf("decode unknown planned state: %v", err)
	}
	plannedAttributes = nil
	if err := unknownPlanned.As(&plannedAttributes); err != nil {
		t.Fatalf("decode unknown planned attributes: %v", err)
	}
	if plannedAttributes["model_names"].IsKnown() || plannedAttributes["model_names"].IsNull() {
		t.Fatalf("unknown model_names plan was not preserved: %s", plannedAttributes["model_names"])
	}
}

func TestAccessGroupModelNamesRequestsAreSortedDeduplicatedAndRejectOnlyUnsuitableValues(t *testing.T) {
	t.Parallel()

	got, err := accessGroupModelNamesForRequest(accessGroupStringList("z-model", "", "a-model", "a-model", ""))
	if err != nil {
		t.Fatalf("accessGroupModelNamesForRequest: %v", err)
	}
	if want := []string{"", "a-model", "z-model"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("model_names = %v, want sorted deduplicated %v", got, want)
	}

	for name, value := range map[string]types.List{
		"null collection":    types.ListNull(types.StringType),
		"unknown collection": types.ListUnknown(types.StringType),
		"empty collection":   accessGroupStringList(),
		"null item":          types.ListValueMust(types.StringType, []attr.Value{types.StringNull()}),
		"unknown item":       types.ListValueMust(types.StringType, []attr.Value{types.StringUnknown()}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := accessGroupModelNamesForRequest(value); err == nil {
				t.Fatal("expected model_names request error")
			}
		})
	}
}

func TestReconcileAccessGroupModelNamesPreservesDuplicateConfigurationAndSurfacesDrift(t *testing.T) {
	t.Parallel()

	configured := accessGroupStringList("z-model", "", "a-model", "a-model", "")
	noDrift, err := reconcileAccessGroupModelNames(context.Background(), configured, []interface{}{"a-model", "z-model", ""})
	if err != nil {
		t.Fatalf("reconcile set-equivalent response: %v", err)
	}
	if !noDrift.Equal(configured) {
		t.Fatalf("set-equivalent response changed duplicate state: got %v, want %v", noDrift, configured)
	}

	drifted, err := reconcileAccessGroupModelNames(context.Background(), configured, []interface{}{"new-model", "a-model", "new-model"})
	if err != nil {
		t.Fatalf("reconcile membership drift: %v", err)
	}
	if got, want := accessGroupListStrings(t, drifted), []string{"a-model", "new-model"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("drifted model_names = %v, want %v", got, want)
	}
}

func TestAccessGroupCreateUpdateAndReadNormalizeRequestsAndPreserveDuplicateHCL(t *testing.T) {
	t.Parallel()

	remoteModels := []string{"", "a-model", "z-model"}
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		methods = append(methods, request.Method+" "+request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/access_group/new":
			var body struct {
				AccessGroup string   `json:"access_group"`
				ModelNames  []string `json:"model_names"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if body.AccessGroup != "ordered-group" || !reflect.DeepEqual(body.ModelNames, []string{"", "a-model", "z-model"}) {
				t.Errorf("create body = %#v, want sorted deduplicated model_names", body)
			}
			remoteModels = append([]string(nil), body.ModelNames...)
			_, _ = writer.Write([]byte(`{}`))
		case request.Method == http.MethodPut && request.URL.Path == "/access_group/ordered-group/update":
			var body struct {
				ModelNames []string `json:"model_names"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode update body: %v", err)
			}
			if !reflect.DeepEqual(body.ModelNames, []string{"", "a-model", "c-model"}) {
				t.Errorf("update model_names = %v, want sorted deduplicated values", body.ModelNames)
			}
			remoteModels = append([]string(nil), body.ModelNames...)
			_, _ = writer.Write([]byte(`{}`))
		case request.Method == http.MethodGet && request.URL.Path == "/access_group/ordered-group/info":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"access_group": "ordered-group",
				"model_names":  remoteModels,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := accessGroupTestSchema(t)
	resourceUnderTest := &AccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	createPlan := accessGroupTestPlan(t, schema, AccessGroupResourceModel{
		ID:          types.StringUnknown(),
		AccessGroup: types.StringValue("ordered-group"),
		ModelNames:  accessGroupStringList("z-model", "", "a-model", "a-model", ""),
	})
	createResponse := &resource.CreateResponse{State: tfsdk.State{Raw: createPlan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: createPlan}, createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}
	var created AccessGroupResourceModel
	if diagnostics := createResponse.State.Get(context.Background(), &created); diagnostics.HasError() {
		t.Fatalf("decode create state: %v", diagnostics)
	}
	if got, want := accessGroupListStrings(t, created.ModelNames), []string{"z-model", "", "a-model", "a-model", ""}; !reflect.DeepEqual(got, want) {
		t.Fatalf("create state = %v, want duplicate-containing configured list %v", got, want)
	}

	updatePlan := accessGroupTestPlan(t, schema, AccessGroupResourceModel{
		ID:          created.ID,
		AccessGroup: created.AccessGroup,
		ModelNames:  accessGroupStringList("c-model", "", "a-model", "c-model"),
	})
	updateResponse := &resource.UpdateResponse{State: tfsdk.State{Raw: updatePlan.Raw, Schema: schema}}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: updatePlan, State: createResponse.State}, updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", updateResponse.Diagnostics)
	}
	var updated AccessGroupResourceModel
	if diagnostics := updateResponse.State.Get(context.Background(), &updated); diagnostics.HasError() {
		t.Fatalf("decode update state: %v", diagnostics)
	}
	if got, want := accessGroupListStrings(t, updated.ModelNames), []string{"c-model", "", "a-model", "c-model"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("update state = %v, want duplicate-containing configured list %v", got, want)
	}

	remoteModels = []string{"drift-model", "a-model"}
	readResponse := &resource.ReadResponse{State: updateResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: updateResponse.State}, readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}
	var drifted AccessGroupResourceModel
	if diagnostics := readResponse.State.Get(context.Background(), &drifted); diagnostics.HasError() {
		t.Fatalf("decode drifted state: %v", diagnostics)
	}
	if got, want := accessGroupListStrings(t, drifted.ModelNames), []string{"a-model", "drift-model"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("drift order = %v, want deterministic authoritative order %v", got, want)
	}

	wantMethods := []string{
		"POST /access_group/new",
		"GET /access_group/ordered-group/info",
		"PUT /access_group/ordered-group/update",
		"GET /access_group/ordered-group/info",
		"GET /access_group/ordered-group/info",
	}
	if !reflect.DeepEqual(methods, wantMethods) {
		t.Fatalf("API methods = %v, want %v", methods, wantMethods)
	}
}

func TestAccessGroupImportAdoptsSortedMembership(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/access_group/imported-group/info" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"access_group":"imported-group","model_names":["z-model","a-model"]}`))
	}))
	defer server.Close()

	schema := accessGroupTestSchema(t)
	resourceUnderTest := &AccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	importState := accessGroupTestState(t, schema, AccessGroupResourceModel{
		ID:          types.StringNull(),
		AccessGroup: types.StringNull(),
		ModelNames:  types.ListNull(types.StringType),
	})
	importResponse := &resource.ImportStateResponse{State: importState}
	resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: "imported-group"}, importResponse)
	if importResponse.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", importResponse.Diagnostics)
	}

	readResponse := &resource.ReadResponse{State: importResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: importResponse.State}, readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("import read diagnostics: %v", readResponse.Diagnostics)
	}
	var imported AccessGroupResourceModel
	if diagnostics := readResponse.State.Get(context.Background(), &imported); diagnostics.HasError() {
		t.Fatalf("decode imported state: %v", diagnostics)
	}
	if imported.ID.ValueString() != "imported-group" || imported.AccessGroup.ValueString() != "imported-group" {
		t.Fatalf("imported identity = %#v", imported)
	}
	if got, want := accessGroupListStrings(t, imported.ModelNames), []string{"a-model", "z-model"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("imported model_names = %v, want %v", got, want)
	}
}

func TestAccessGroupDataSourcesKeepListTypesAndSortComputedMembership(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var singleSchemaResponse datasource.SchemaResponse
	(&AccessGroupDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &singleSchemaResponse)
	if singleSchemaResponse.Diagnostics.HasError() {
		t.Fatalf("single data source schema diagnostics: %v", singleSchemaResponse.Diagnostics)
	}
	if _, ok := singleSchemaResponse.Schema.Attributes["model_names"].(datasourceschema.ListAttribute); !ok {
		t.Fatalf("single data source model_names type = %T, want schema.ListAttribute", singleSchemaResponse.Schema.Attributes["model_names"])
	}

	var listSchemaResponse datasource.SchemaResponse
	(&AccessGroupsListDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &listSchemaResponse)
	if listSchemaResponse.Diagnostics.HasError() {
		t.Fatalf("list data source schema diagnostics: %v", listSchemaResponse.Diagnostics)
	}
	groups, ok := listSchemaResponse.Schema.Attributes["access_groups"].(datasourceschema.ListNestedAttribute)
	if !ok {
		t.Fatalf("access_groups schema type = %T", listSchemaResponse.Schema.Attributes["access_groups"])
	}
	if _, ok := groups.NestedObject.Attributes["model_names"].(datasourceschema.ListAttribute); !ok {
		t.Fatalf("list data source nested model_names type = %T, want schema.ListAttribute", groups.NestedObject.Attributes["model_names"])
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/access_group/data-group/info":
			_, _ = writer.Write([]byte(`{"access_group":"data-group","model_names":["z-model","a-model"]}`))
		case "/access_group/list":
			_, _ = writer.Write([]byte(`{"access_groups":[{"access_group":"z-group","model_names":["z-model","a-model"]},{"access_group":"a-group","model_names":["c-model","b-model"]}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}

	singleDataSource := &AccessGroupDataSource{client: client}
	singleRaw, err := tftypes.ValueFromJSON(
		[]byte(`{"id":null,"access_group":"data-group","model_names":null}`),
		singleSchemaResponse.Schema.Type().TerraformType(ctx),
	)
	if err != nil {
		t.Fatalf("build single data source config: %v", err)
	}
	singleConfig := tfsdk.Config{Raw: singleRaw, Schema: singleSchemaResponse.Schema}
	singleReadResponse := &datasource.ReadResponse{State: tfsdk.State{Raw: singleRaw, Schema: singleSchemaResponse.Schema}}
	singleDataSource.Read(ctx, datasource.ReadRequest{Config: singleConfig}, singleReadResponse)
	if singleReadResponse.Diagnostics.HasError() {
		t.Fatalf("single data source read diagnostics: %v", singleReadResponse.Diagnostics)
	}
	var singleData AccessGroupDataSourceModel
	if diagnostics := singleReadResponse.State.Get(ctx, &singleData); diagnostics.HasError() {
		t.Fatalf("decode single data source state: %v", diagnostics)
	}
	if got, want := accessGroupListStrings(t, singleData.ModelNames), []string{"a-model", "z-model"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("single data source model_names = %v, want %v", got, want)
	}

	listDataSource := &AccessGroupsListDataSource{client: client}
	listRaw, err := tftypes.ValueFromJSON(
		[]byte(`{"id":null,"access_groups":null}`),
		listSchemaResponse.Schema.Type().TerraformType(ctx),
	)
	if err != nil {
		t.Fatalf("build list data source config: %v", err)
	}
	listConfig := tfsdk.Config{Raw: listRaw, Schema: listSchemaResponse.Schema}
	listReadResponse := &datasource.ReadResponse{State: tfsdk.State{Raw: listRaw, Schema: listSchemaResponse.Schema}}
	listDataSource.Read(ctx, datasource.ReadRequest{Config: listConfig}, listReadResponse)
	if listReadResponse.Diagnostics.HasError() {
		t.Fatalf("list data source read diagnostics: %v", listReadResponse.Diagnostics)
	}
	var listData AccessGroupsListDataSourceModel
	if diagnostics := listReadResponse.State.Get(ctx, &listData); diagnostics.HasError() {
		t.Fatalf("decode list data source state: %v", diagnostics)
	}
	if len(listData.AccessGroups) != 2 || listData.AccessGroups[0].AccessGroup.ValueString() != "a-group" || listData.AccessGroups[1].AccessGroup.ValueString() != "z-group" {
		t.Fatalf("list data source group order = %#v", listData.AccessGroups)
	}
	if got, want := accessGroupListStrings(t, listData.AccessGroups[0].ModelNames), []string{"b-model", "c-model"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("first group model_names = %v, want %v", got, want)
	}
	if got, want := accessGroupListStrings(t, listData.AccessGroups[1].ModelNames), []string{"a-model", "z-model"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("second group model_names = %v, want %v", got, want)
	}
}

func TestAccessGroupModelNamesRemainUsableByListConsumers(t *testing.T) {
	t.Parallel()

	ctx := &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"model_names": cty.ListVal([]cty.Value{cty.StringVal("z-model"), cty.StringVal("a-model")}),
		},
		Functions: map[string]function.Function{"concat": stdlib.ConcatFunc},
	}

	indexExpression, diagnostics := hclsyntax.ParseExpression([]byte(`model_names[0]`), "index.tf", hcl.Pos{Line: 1, Column: 1})
	if diagnostics.HasErrors() {
		t.Fatalf("parse indexing expression: %v", diagnostics)
	}
	indexed, diagnostics := indexExpression.Value(ctx)
	if diagnostics.HasErrors() || indexed.AsString() != "z-model" {
		t.Fatalf("list indexing result = %v, diagnostics = %v", indexed, diagnostics)
	}

	concatExpression, diagnostics := hclsyntax.ParseExpression([]byte(`concat(model_names, ["new-model"])`), "concat.tf", hcl.Pos{Line: 1, Column: 1})
	if diagnostics.HasErrors() {
		t.Fatalf("parse concat expression: %v", diagnostics)
	}
	concatenated, diagnostics := concatExpression.Value(ctx)
	if diagnostics.HasErrors() {
		t.Fatalf("evaluate concat expression: %v", diagnostics)
	}
	moduleInput, err := convert.Convert(concatenated, cty.List(cty.String))
	if err != nil {
		t.Fatalf("convert concatenated result for list(string) module input: %v", err)
	}
	if moduleInput.LengthInt() != 3 || moduleInput.Index(cty.NumberIntVal(2)).AsString() != "new-model" {
		t.Fatalf("concat/module list result = %s", moduleInput.GoString())
	}
}

func TestAccessGroupModelIDsRemainDeferredWithoutReadIdentity(t *testing.T) {
	t.Parallel()

	var resourceResponse resource.SchemaResponse
	(&AccessGroupResource{}).Schema(context.Background(), resource.SchemaRequest{}, &resourceResponse)
	if _, ok := resourceResponse.Schema.Attributes["model_ids"]; ok {
		t.Fatal("model_ids must not be exposed until LiteLLM read endpoints preserve deployment identity")
	}

	var dataSourceResponse datasource.SchemaResponse
	(&AccessGroupDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &dataSourceResponse)
	if _, ok := dataSourceResponse.Schema.Attributes["model_ids"]; ok {
		t.Fatal("data source unexpectedly exposes unreadable model_ids")
	}
}
