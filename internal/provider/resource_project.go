package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &ProjectResource{}
var _ resource.ResourceWithImportState = &ProjectResource{}
var _ resource.ResourceWithModifyPlan = &ProjectResource{}

const (
	// projectImportedOptionalStringsPrivateKey is the pre-per-field marker read
	// only to migrate retained private state from provider builds before #188's
	// final ownership fix.
	projectImportedOptionalStringsPrivateKey     = "project_imported_optional_strings_v1"
	projectImportedAliasPrivateKey               = "project_imported_project_alias_v1"
	projectImportedDescriptionPrivateKey         = "project_imported_description_v1"
	projectAliasOwnershipPendingPrivateKey       = "project_alias_ownership_pending_v1"
	projectDescriptionOwnershipPendingPrivateKey = "project_description_ownership_pending_v1"
)

func NewProjectResource() resource.Resource { return &ProjectResource{} }

type ProjectResource struct{ client *Client }

type ProjectResourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	ProjectAlias        types.String  `tfsdk:"project_alias"`
	Description         types.String  `tfsdk:"description"`
	TeamID              types.String  `tfsdk:"team_id"`
	Models              types.List    `tfsdk:"models"`
	Metadata            types.Map     `tfsdk:"metadata"`
	Tags                types.List    `tfsdk:"tags"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	ModelMaxBudget      types.Map     `tfsdk:"model_max_budget"`
	ModelRPMLimit       types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit       types.Map     `tfsdk:"model_tpm_limit"`
	Blocked             types.Bool    `tfsdk:"blocked"`
	CreatedAt           types.String  `tfsdk:"created_at"`
	UpdatedAt           types.String  `tfsdk:"updated_at"`
	CreatedBy           types.String  `tfsdk:"created_by"`
	UpdatedBy           types.String  `tfsdk:"updated_by"`
}

func (r *ProjectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *ProjectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM Project. Project budget controls are read authoritatively from the nested litellm_budget_table relation.",
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Description: "The unique project ID (assigned by LiteLLM).", Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"project_alias":         schema.StringAttribute{Description: "Human-friendly name for the project.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"description":           schema.StringAttribute{Description: "Description of the project's purpose and use case.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"team_id":               schema.StringAttribute{Description: "The team ID that this project belongs to.", Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"models":                schema.ListAttribute{Description: "List of models the project can access.", Optional: true, Computed: true, ElementType: types.StringType},
			"metadata":              schema.MapAttribute{Description: "Metadata for the project. Values are strings; use jsonencode() for complex values.", Optional: true, Computed: true, ElementType: types.StringType},
			"tags":                  schema.ListAttribute{Description: "Tags associated with the project. LiteLLM v1.98 stores these in project metadata.", Optional: true, Computed: true, ElementType: types.StringType},
			"max_budget":            schema.Float64Attribute{Description: "Maximum budget for this project.", Optional: true},
			"soft_budget":           schema.Float64Attribute{Description: "Soft budget limit for warnings.", Optional: true},
			"budget_duration":       schema.StringAttribute{Description: "Budget reset duration (for example, '30d' or '1h').", Optional: true},
			"budget_id":             schema.StringAttribute{Description: "Budget ID associated with this project. Reassociation is not safely supported by LiteLLM v1.98.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"tpm_limit":             schema.Int64Attribute{Description: "Tokens per minute limit.", Optional: true},
			"rpm_limit":             schema.Int64Attribute{Description: "Requests per minute limit.", Optional: true},
			"max_parallel_requests": schema.Int64Attribute{Description: "Maximum parallel requests allowed.", Optional: true},
			"model_max_budget":      schema.MapAttribute{Description: "Legacy per-model budget map shape retained for schema compatibility.", Optional: true, Computed: true, ElementType: types.Float64Type, Validators: []validator.Map{mapvalidator.NoNullValues()}},
			"model_rpm_limit":       schema.MapAttribute{Description: "Per-model RPM limits stored in project metadata.", Optional: true, Computed: true, ElementType: types.Int64Type, Validators: []validator.Map{mapvalidator.NoNullValues()}},
			"model_tpm_limit":       schema.MapAttribute{Description: "Per-model TPM limits stored in project metadata.", Optional: true, Computed: true, ElementType: types.Int64Type, Validators: []validator.Map{mapvalidator.NoNullValues()}},
			"blocked":               schema.BoolAttribute{Description: "Whether the project is blocked from making requests.", Optional: true, Computed: true},
			"created_at":            schema.StringAttribute{Description: "Timestamp when the project was created.", Computed: true},
			"updated_at":            schema.StringAttribute{Description: "Timestamp when the project was last updated.", Computed: true},
			"created_by":            schema.StringAttribute{Description: "User who created the project.", Computed: true},
			"updated_by":            schema.StringAttribute{Description: "User who last updated the project.", Computed: true},
		},
	}
}

func (r *ProjectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *ProjectResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if organizationProjectPlanIsDestroy(req) {
		return
	}
	var state, plan, config ProjectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if req.State.Raw.IsNull() {
		if !config.BudgetID.IsNull() && projectBudgetControlsPresentInConfig(&config) {
			resp.Diagnostics.AddAttributeError(path.Root("budget_id"), "Unsafe Shared Project Budget Controls", "budget_id cannot be combined with project budget controls during creation because LiteLLM v1.98 ignores or strips those controls for an existing shared budget.")
		}
		if config.ModelMaxBudget.IsUnknown() || (knownMap(config.ModelMaxBudget) && len(config.ModelMaxBudget.Elements()) > 0) {
			resp.Diagnostics.AddAttributeError(path.Root("model_max_budget"), "Unsupported Structured Project Model Budget", "LiteLLM v1.98 requires GenericBudgetConfig objects for model_max_budget, but this resource's legacy schema is map(float64). Non-empty or unknown configuration is rejected until a migration-safe structured representation is available.")
		}
		return
	}
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	importedBudget, importedAlias, importedDescription, legacyImportedStrings := false, false, false, false
	if req.Private != nil {
		budgetMarker, diagnostics := req.Private.GetKey(ctx, organizationProjectImportedBudgetPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		importedBudget = string(budgetMarker) == "true"
		aliasMarker, diagnostics := req.Private.GetKey(ctx, projectImportedAliasPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		descriptionMarker, diagnostics := req.Private.GetKey(ctx, projectImportedDescriptionPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		legacyMarker, diagnostics := req.Private.GetKey(ctx, projectImportedOptionalStringsPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		legacyImportedStrings = string(legacyMarker) == "true"
		importedAlias = string(aliasMarker) == "true" || legacyImportedStrings
		importedDescription = string(descriptionMarker) == "true" || legacyImportedStrings
	}
	preserveOrganizationProjectBudgetID(ctx, "Project", state.BudgetID, config.BudgetID, plan.BudgetID, importedBudget, resp)

	for _, field := range []struct {
		name                string
		state, plan, config types.String
		imported            bool
	}{
		{"project_alias", state.ProjectAlias, plan.ProjectAlias, config.ProjectAlias, importedAlias},
		{"description", state.Description, plan.Description, config.Description, importedDescription},
	} {
		switch {
		case field.config.IsNull() && field.imported:
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(field.name), field.state)...)
		case knownString(field.state) && field.config.IsNull():
			resp.Diagnostics.AddAttributeError(path.Root(field.name), "Unsupported Project String Clear", fmt.Sprintf("LiteLLM v1.98's /project/update excludes null %s values, so removing this configured value cannot converge. Keep it configured or set an explicit non-null replacement.", field.name))
		case knownString(field.state) && (field.config.IsUnknown() || field.plan.IsUnknown()):
			resp.Diagnostics.AddAttributeError(path.Root(field.name), "Unknown Project String Transition", fmt.Sprintf("%s must be known while planning because an unknown value could resolve to an unsupported null clear on LiteLLM v1.98.", field.name))
		}
	}
	if config.ModelMaxBudget.IsNull() && knownMap(state.ModelMaxBudget) {
		// A known legacy scalar map can still be removed safely through an
		// explicit null budget update. Override the framework's computed unknown.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("model_max_budget"), types.MapNull(types.Float64Type))...)
	} else if config.ModelMaxBudget.IsUnknown() || (knownMap(config.ModelMaxBudget) && len(config.ModelMaxBudget.Elements()) > 0 && !config.ModelMaxBudget.Equal(state.ModelMaxBudget)) {
		resp.Diagnostics.AddAttributeError(path.Root("model_max_budget"), "Unsupported Structured Project Model Budget", "LiteLLM v1.98 requires GenericBudgetConfig objects for model_max_budget, but this resource's legacy schema is map(float64). Non-empty additions, changes, or unknown transitions are rejected until a migration-safe structured representation is available.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// Split the historical shared alias/description marker only in a valid
	// plan. Retain both individual permissions until Update proves any pending
	// explicit configuration succeeded; this is safe even if apply persists
	// partial private state after an error.
	if legacyImportedStrings && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectImportedOptionalStringsPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectImportedAliasPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectImportedDescriptionPrivateKey, []byte("true"))...)
	}
	planImportedOmissionOwnership(ctx, organizationProjectBudgetOwnershipPendingPrivateKey, importedBudget, !config.BudgetID.IsNull(), resp)
	planImportedOmissionOwnership(ctx, projectAliasOwnershipPendingPrivateKey, importedAlias, !config.ProjectAlias.IsNull(), resp)
	planImportedOmissionOwnership(ctx, projectDescriptionOwnershipPendingPrivateKey, importedDescription, !config.Description.IsNull(), resp)
}

func (r *ProjectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ProjectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	projectRequest, err := r.buildProjectCreateRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Project Request", err.Error())
		return
	}
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/project/new", projectRequest, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create project: %s", err))
		return
	}
	object, err := unwrapObjectEnvelope(result, "project_info", "data")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	projectID, ok := object["project_id"].(string)
	if !ok || projectID == "" {
		resp.Diagnostics.AddError("Invalid API Response", "Project create response did not contain a nonempty project_id.")
		return
	}
	data.ID = types.StringValue(projectID)

	// Project creation stores duration without setting the initial reset time.
	if knownString(data.BudgetDuration) {
		table, parseErr := parseBudgetTable(object)
		if parseErr != nil {
			resp.Diagnostics.AddError("Invalid API Response", parseErr.Error())
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
		budgetID, presence, budgetErr := budgetTableID(object, table)
		if budgetErr != nil || presence != apiValuePresent {
			resp.Diagnostics.AddError("Budget Reset Initialization Error", "Project was created, but the response did not identify its budget for reset initialization.")
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
		var budgetResult map[string]interface{}
		payload := map[string]interface{}{"budget_id": budgetID, "budget_duration": data.BudgetDuration.ValueString()}
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/budget/update", payload, &budgetResult); err != nil {
			resp.Diagnostics.AddError("Budget Reset Initialization Error", fmt.Sprintf("Project was created, but LiteLLM could not initialize its budget reset schedule: %s", err))
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
		if confirmedID, ok := budgetResult["budget_id"].(string); !ok || confirmedID != budgetID {
			resp.Diagnostics.AddError("Budget Reset Initialization Error", "Project was created and LiteLLM accepted reset initialization, but the response did not confirm the matching budget identity.")
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
	}
	if err := r.readProject(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Read Error", fmt.Sprintf("Project was created but its authoritative state could not be read: %s", err))
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProjectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ProjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	marker, privateDiagnostics := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(marker) == "true"
	if err := r.readProjectWithNumericOwnership(ctx, &data, imported); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read project: %s", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && imported {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
}

func (r *ProjectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config ProjectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if !req.Config.Raw.IsNull() {
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	} else {
		config = plan
	}
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID = state.ID
	if state.BudgetID.IsUnknown() || plan.BudgetID.IsUnknown() || !state.BudgetID.Equal(plan.BudgetID) {
		resp.Diagnostics.AddError("Unsafe Project Budget Reassociation", "The project budget_id changed or remained unknown despite the plan safety check; no API call was made.")
		return
	}
	importedAlias, importedDescription := false, false
	if req.Private != nil {
		aliasMarker, diagnostics := req.Private.GetKey(ctx, projectImportedAliasPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		descriptionMarker, diagnostics := req.Private.GetKey(ctx, projectImportedDescriptionPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		legacyMarker, diagnostics := req.Private.GetKey(ctx, projectImportedOptionalStringsPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		importedAlias = string(aliasMarker) == "true" || string(legacyMarker) == "true"
		importedDescription = string(descriptionMarker) == "true" || string(legacyMarker) == "true"
	}
	if (!importedAlias && knownString(state.ProjectAlias) && config.ProjectAlias.IsNull()) || (!importedDescription && knownString(state.Description) && config.Description.IsNull()) {
		resp.Diagnostics.AddError("Unsupported Project String Clear", "project_alias or description was removed despite the plan safety check; LiteLLM v1.98 ignores this null clear and no API call was made.")
		return
	}
	if config.ModelMaxBudget.IsUnknown() || (knownMap(plan.ModelMaxBudget) && len(plan.ModelMaxBudget.Elements()) > 0 && !plan.ModelMaxBudget.Equal(state.ModelMaxBudget)) {
		resp.Diagnostics.AddError("Unsupported Structured Project Model Budget", "The model_max_budget was unknown or changed despite the plan safety check; no API call was made.")
		return
	}
	projectRequest, rowChanged, err := buildProjectRowUpdateRequest(ctx, &plan, &state)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Project Request", err.Error())
		return
	}
	budgetRequest, budgetChanged, err := buildProjectBudgetUpdateRequest(&plan, &state)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Project Budget Request", err.Error())
		return
	}
	projectBudgetSets, directBudgetPatch := splitProjectBudgetUpdateRequest(budgetRequest)
	for field, value := range projectBudgetSets {
		projectRequest[field] = value
	}
	projectChanged := rowChanged || len(projectBudgetSets) > 0

	var budgetID string
	if budgetChanged {
		budgetID, err = r.lookupProjectBudgetID(ctx, state.ID.ValueString(), state.BudgetID)
		if err != nil {
			resp.Diagnostics.AddError("Project Budget Lookup Error", err.Error())
			return
		}
		if len(directBudgetPatch) > 0 {
			directBudgetPatch["budget_id"] = budgetID
		}
	}
	if projectChanged {
		projectRequest["project_id"] = state.ID.ValueString()
		var result map[string]interface{}
		// LiteLLM v1.98's exact /project/update handler invokes
		// _check_team_project_limits before writing its related budget row. All
		// non-null project budget changes must pass through this route; using
		// /budget/update directly would bypass parent-team max/TPM/RPM checks.
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/project/update", projectRequest, &result); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update project: %s", err))
			return
		}
		object, err := unwrapObjectEnvelope(result, "project_info", "data")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if err := validateImportedObjectIdentity(true, "project update", object, "project_id", state.ID.ValueString()); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
	}
	if len(directBudgetPatch) > 0 {
		var result map[string]interface{}
		// v1.98 serializes /project/update with exclude_none=True, so explicit
		// clears cannot reach the budget table there. Its handler also does not
		// recompute budget_reset_at. Only those null clears and an already-
		// validated duration replay use /budget/update, after /project/update.
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/budget/update", directBudgetPatch, &result); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("LiteLLM accepted any preceding project update, but the reset/clear budget update failed; prior Terraform state was retained: %s", err))
			return
		}
		confirmedID, ok := result["budget_id"].(string)
		if !ok || confirmedID != budgetID {
			resp.Diagnostics.AddError("Invalid API Response", "LiteLLM accepted the reset/clear budget update but did not return the matching budget_id; prior Terraform state was retained.")
			return
		}
	}
	desired := plan
	seedProjectClearOwnership(&plan, &state)
	if err := r.readProject(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Read Error", fmt.Sprintf("Project update was accepted but authoritative read-back failed; prior state was retained: %s", err))
		return
	}
	if field, ok := projectChangedFieldMismatch(&desired, &state, &plan); ok {
		resp.Diagnostics.AddError("Project Update Did Not Converge", fmt.Sprintf("LiteLLM accepted the update but authoritative read-back did not match planned %s; prior Terraform state was retained.", field))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		transitions := []struct {
			importKey, pendingKey string
			pending               bool
		}{
			{importKey: organizationProjectImportedBudgetPrivateKey, pendingKey: organizationProjectBudgetOwnershipPendingPrivateKey},
			{importKey: projectImportedAliasPrivateKey, pendingKey: projectAliasOwnershipPendingPrivateKey},
			{importKey: projectImportedDescriptionPrivateKey, pendingKey: projectDescriptionOwnershipPendingPrivateKey},
		}
		for index := range transitions {
			marker, diagnostics := resp.Private.GetKey(ctx, transitions[index].pendingKey)
			resp.Diagnostics.Append(diagnostics...)
			transitions[index].pending = string(marker) == "true"
		}
		if resp.Diagnostics.HasError() {
			return
		}
		for _, transition := range transitions {
			if !transition.pending {
				continue
			}
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, transition.importKey, nil)...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, transition.pendingKey, nil)...)
		}
	}
}

func (r *ProjectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ProjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", "/project/delete", map[string]interface{}{"project_ids": []string{data.ID.ValueString()}}, nil); err != nil && !IsNotFoundError(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete project: %s", err))
	}
}

func (r *ProjectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationProjectImportedBudgetPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectImportedAliasPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectImportedDescriptionPrivateKey, []byte("true"))...)
	}
}

// buildProjectRequest is retained as the create-payload test seam used by
// earlier releases. Updates are split into project-row and budget requests.
func (r *ProjectResource) buildProjectRequest(ctx context.Context, data *ProjectResourceModel) (map[string]interface{}, error) {
	return r.buildProjectCreateRequest(ctx, data)
}

func (r *ProjectResource) buildProjectCreateRequest(ctx context.Context, data *ProjectResourceModel) (map[string]interface{}, error) {
	if knownString(data.BudgetID) && projectBudgetControlsConfigured(data) {
		return nil, fmt.Errorf("budget_id cannot be combined with project budget controls during creation because LiteLLM v1.98 ignores or strips those controls for an existing shared budget")
	}
	if knownMap(data.ModelMaxBudget) && len(data.ModelMaxBudget.Elements()) > 0 {
		return nil, fmt.Errorf("non-empty model_max_budget is unsupported because LiteLLM v1.98 requires structured GenericBudgetConfig values while the migration-compatible project schema is map(float64)")
	}
	request := map[string]interface{}{"team_id": data.TeamID.ValueString()}
	if knownString(data.ProjectAlias) {
		request["project_alias"] = data.ProjectAlias.ValueString()
	}
	if knownString(data.Description) {
		request["description"] = data.Description.ValueString()
	}
	if knownString(data.BudgetID) {
		request["budget_id"] = data.BudgetID.ValueString()
	}
	if knownString(data.BudgetDuration) {
		request["budget_duration"] = data.BudgetDuration.ValueString()
	}
	addKnownFloat(request, "max_budget", data.MaxBudget)
	addKnownFloat(request, "soft_budget", data.SoftBudget)
	addKnownInt(request, "tpm_limit", data.TPMLimit)
	addKnownInt(request, "rpm_limit", data.RPMLimit)
	addKnownInt(request, "max_parallel_requests", data.MaxParallelRequests)
	if !data.ModelMaxBudget.IsNull() && !data.ModelMaxBudget.IsUnknown() {
		values, err := float64RequestMap(data.ModelMaxBudget, "model_max_budget")
		if err != nil {
			return nil, err
		}
		request["model_max_budget"] = values
	}
	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		models, err := stringListRequest(ctx, data.Models, "models")
		if err != nil {
			return nil, err
		}
		request["models"] = models
	}
	if !data.Blocked.IsNull() && !data.Blocked.IsUnknown() {
		request["blocked"] = data.Blocked.ValueBool()
	}
	metadata, managed, err := projectMetadataPayload(ctx, data)
	if err != nil {
		return nil, err
	}
	if managed {
		request["metadata"] = metadata
	}
	return request, nil
}

func buildProjectRowUpdateRequest(ctx context.Context, plan, state *ProjectResourceModel) (map[string]interface{}, bool, error) {
	request := map[string]interface{}{}
	if !plan.ProjectAlias.IsUnknown() && !plan.ProjectAlias.Equal(state.ProjectAlias) {
		if plan.ProjectAlias.IsNull() {
			return nil, false, fmt.Errorf("project_alias cannot be cleared because LiteLLM v1.98 excludes null on update")
		}
		request["project_alias"] = plan.ProjectAlias.ValueString()
	}
	if !plan.Description.IsUnknown() && !plan.Description.Equal(state.Description) {
		if plan.Description.IsNull() {
			return nil, false, fmt.Errorf("description cannot be cleared because LiteLLM v1.98 excludes null on update")
		}
		request["description"] = plan.Description.ValueString()
	}
	if !plan.Models.IsUnknown() && !plan.Models.Equal(state.Models) {
		if plan.Models.IsNull() {
			return nil, false, fmt.Errorf("models cannot be cleared with null; configure an empty list")
		}
		models, err := stringListRequest(ctx, plan.Models, "models")
		if err != nil {
			return nil, false, err
		}
		request["models"] = models
	}
	if !plan.Blocked.IsUnknown() && !plan.Blocked.Equal(state.Blocked) {
		if plan.Blocked.IsNull() {
			return nil, false, fmt.Errorf("blocked cannot be cleared with null; configure false")
		}
		request["blocked"] = plan.Blocked.ValueBool()
	}
	metadataChanged := (!plan.Metadata.IsUnknown() && !plan.Metadata.Equal(state.Metadata)) || (!plan.Tags.IsUnknown() && !plan.Tags.Equal(state.Tags)) || (!plan.ModelRPMLimit.IsUnknown() && !plan.ModelRPMLimit.Equal(state.ModelRPMLimit)) || (!plan.ModelTPMLimit.IsUnknown() && !plan.ModelTPMLimit.Equal(state.ModelTPMLimit))
	if metadataChanged {
		metadata, _, err := projectMetadataPayload(ctx, plan)
		if err != nil {
			return nil, false, err
		}
		request["metadata"] = metadata
	}
	return request, len(request) > 0, nil
}

func buildProjectBudgetUpdateRequest(plan, state *ProjectResourceModel) (map[string]interface{}, bool, error) {
	request := map[string]interface{}{}
	addChangedFloat(request, "max_budget", plan.MaxBudget, state.MaxBudget)
	addChangedFloat(request, "soft_budget", plan.SoftBudget, state.SoftBudget)
	addChangedInt(request, "tpm_limit", plan.TPMLimit, state.TPMLimit)
	addChangedInt(request, "rpm_limit", plan.RPMLimit, state.RPMLimit)
	addChangedInt(request, "max_parallel_requests", plan.MaxParallelRequests, state.MaxParallelRequests)
	if !plan.BudgetDuration.IsUnknown() && !plan.BudgetDuration.Equal(state.BudgetDuration) {
		if plan.BudgetDuration.IsNull() {
			request["budget_duration"] = nil
			request["budget_reset_at"] = nil
		} else {
			request["budget_duration"] = plan.BudgetDuration.ValueString()
		}
	}
	if !plan.ModelMaxBudget.IsUnknown() && !plan.ModelMaxBudget.Equal(state.ModelMaxBudget) {
		if plan.ModelMaxBudget.IsNull() {
			request["model_max_budget"] = nil
		} else {
			values, err := float64RequestMap(plan.ModelMaxBudget, "model_max_budget")
			if err != nil {
				return nil, false, err
			}
			request["model_max_budget"] = values
		}
	}
	return request, len(request) > 0, nil
}

// splitProjectBudgetUpdateRequest follows the exact v1.98 endpoint contracts:
// /project/update validates non-null project limits against the parent team but
// drops nulls with exclude_none=True; /budget/update preserves explicitly sent
// nulls with exclude_unset=True and recomputes the reset timestamp for a sent
// duration. A duration therefore goes through the validating route first and is
// replayed only to initialize its reset schedule.
func splitProjectBudgetUpdateRequest(request map[string]interface{}) (map[string]interface{}, map[string]interface{}) {
	projectSets := map[string]interface{}{}
	directPatch := map[string]interface{}{}
	for field, value := range request {
		if value == nil {
			directPatch[field] = nil
			continue
		}
		projectSets[field] = value
		if field == "budget_duration" {
			directPatch[field] = value
		}
	}
	return projectSets, directPatch
}

func projectMetadataPayload(ctx context.Context, data *ProjectResourceModel) (map[string]interface{}, bool, error) {
	metadata := map[string]interface{}{}
	managed := false
	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		managed = true
		var values map[string]string
		if diagnostics := data.Metadata.ElementsAs(ctx, &values, false); diagnostics.HasError() {
			return nil, false, fmt.Errorf("metadata contains a value that cannot be represented as a string")
		}
		metadata = convertMetadataToNative(values)
		delete(metadata, "tags")
		delete(metadata, "model_rpm_limit")
		delete(metadata, "model_tpm_limit")
	}
	if !data.Tags.IsNull() && !data.Tags.IsUnknown() {
		managed = true
		tags, err := stringListRequest(ctx, data.Tags, "tags")
		if err != nil {
			return nil, false, err
		}
		metadata["tags"] = tags
	}
	if !data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown() {
		managed = true
		values, err := int64RequestMap(data.ModelRPMLimit, "model_rpm_limit")
		if err != nil {
			return nil, false, err
		}
		metadata["model_rpm_limit"] = values
	}
	if !data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown() {
		managed = true
		values, err := int64RequestMap(data.ModelTPMLimit, "model_tpm_limit")
		if err != nil {
			return nil, false, err
		}
		metadata["model_tpm_limit"] = values
	}
	return metadata, managed, nil
}

func (r *ProjectResource) lookupProjectBudgetID(ctx context.Context, projectID string, configured types.String) (string, error) {
	var result map[string]interface{}
	endpoint := "/project/info?project_id=" + url.QueryEscape(projectID)
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return "", fmt.Errorf("unable to read authoritative project budget: %w", err)
	}
	object, err := unwrapObjectEnvelope(result, "project_info", "data")
	if err != nil {
		return "", err
	}
	if err := validateImportedObjectIdentity(true, "project budget lookup", object, "project_id", projectID); err != nil {
		return "", err
	}
	table, err := parseBudgetTable(object)
	if err != nil {
		return "", err
	}
	budgetID, presence, err := budgetTableID(object, table)
	if err != nil {
		return "", err
	}
	if presence != apiValuePresent {
		return "", fmt.Errorf("project %q has no authoritative budget identity", projectID)
	}
	if knownString(configured) && configured.ValueString() != budgetID {
		return "", fmt.Errorf("project budget reassociation detected: state budget_id %q, API budget_id %q", configured.ValueString(), budgetID)
	}
	return budgetID, nil
}

func (r *ProjectResource) readProject(ctx context.Context, data *ProjectResourceModel) error {
	return r.readProjectWithNumericOwnership(ctx, data, false)
}

func (r *ProjectResource) readProjectWithNumericOwnership(ctx context.Context, data *ProjectResourceModel, imported bool) error {
	projectID := data.ID.ValueString()
	if projectID == "" {
		return fmt.Errorf("project ID is empty, cannot read project")
	}
	var result map[string]interface{}
	endpoint := "/project/info?project_id=" + url.QueryEscape(projectID)
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}
	object, err := unwrapObjectEnvelope(result, "project_info", "data")
	if err != nil {
		return err
	}
	if err := validateImportedObjectIdentity(true, "project", object, "project_id", projectID); err != nil {
		return err
	}
	if err := requireImportedStringField(imported, "project", object, "team_id"); err != nil {
		return err
	}
	table, err := parseBudgetTable(object)
	if err != nil {
		return err
	}
	remoteBudgetID, budgetPresence, err := budgetTableID(object, table)
	if err != nil {
		return err
	}
	budgetOwned := imported || knownString(data.BudgetID)
	if budgetPresence == apiValuePresent && budgetOwned {
		if !imported && knownString(data.BudgetID) && data.BudgetID.ValueString() != remoteBudgetID {
			return fmt.Errorf("project budget reassociation detected: state budget_id %q, API budget_id %q", data.BudgetID.ValueString(), remoteBudgetID)
		}
		data.BudgetID = types.StringValue(remoteBudgetID)
	} else if budgetOwned && budgetPresence != apiValuePresent {
		data.BudgetID = types.StringNull()
	} else if !budgetOwned && data.BudgetID.IsUnknown() {
		// Do not adopt LiteLLM's generated budget for an ordinary omitted create.
		data.BudgetID = types.StringNull()
	}
	data.ID = types.StringValue(projectID)
	if err := updateProjectOptionalString(&data.ProjectAlias, object, "project_alias", imported); err != nil {
		return err
	}
	if err := updateProjectOptionalString(&data.Description, object, "description", imported); err != nil {
		return err
	}
	if err := updateNullableString(&data.TeamID, object, "team_id"); err != nil {
		return err
	}
	for _, field := range []struct {
		name   string
		target *types.String
	}{
		{"created_at", &data.CreatedAt}, {"updated_at", &data.UpdatedAt}, {"created_by", &data.CreatedBy}, {"updated_by", &data.UpdatedBy},
	} {
		if err := updateNullableString(field.target, object, field.name); err != nil {
			return err
		}
	}
	models, modelsPresence, err := stringListFromAPI(object, "models")
	if err != nil {
		return err
	}
	if modelsPresence == apiValuePresent {
		data.Models = models
	} else if data.Models.IsUnknown() {
		data.Models = types.ListNull(types.StringType)
	}
	if blocked, presence, err := apiValueAt(object, "blocked"); err != nil {
		return err
	} else if presence == apiValuePresent {
		value, ok := blocked.(bool)
		if !ok {
			return fmt.Errorf("invalid response field %q: expected a boolean", "blocked")
		}
		data.Blocked = types.BoolValue(value)
	} else {
		data.Blocked = types.BoolNull()
	}

	for _, field := range []struct {
		name   string
		target *types.Float64
	}{
		{"max_budget", &data.MaxBudget}, {"soft_budget", &data.SoftBudget},
	} {
		owned := imported || knownFloat(*field.target)
		if err := updateBudgetFloat64(field.target, table, owned, owned, field.name); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"tpm_limit", &data.TPMLimit}, {"rpm_limit", &data.RPMLimit}, {"max_parallel_requests", &data.MaxParallelRequests},
	} {
		owned := imported || knownInt(*field.target)
		if err := updateBudgetInt64(field.target, table, owned, owned, field.name); err != nil {
			return err
		}
	}
	durationOwned := imported || knownString(data.BudgetDuration)
	if err := updateBudgetDuration(&data.BudgetDuration, table, durationOwned, durationOwned); err != nil {
		return err
	}
	if err := updateLegacyProjectModelMaxBudget(&data.ModelMaxBudget, table); err != nil {
		return err
	}

	metadataState, metadataPresence, err := stringMapFromAPI(object, "metadata", "tags", "model_rpm_limit", "model_tpm_limit")
	if err != nil {
		return err
	}
	if metadataPresence != apiValuePresent {
		metadataState = types.MapNull(types.StringType)
	}
	data.Metadata = metadataState
	if metadataObject, ok := object["metadata"].(map[string]interface{}); ok {
		tags, presence, err := stringListFromAPI(metadataObject, "tags")
		if err != nil {
			return fmt.Errorf("invalid response field metadata.tags: %w", err)
		}
		if presence == apiValuePresent {
			data.Tags = tags
		} else {
			data.Tags = types.ListNull(types.StringType)
		}
	} else {
		data.Tags = types.ListNull(types.StringType)
	}
	rpmOwned := imported || knownMap(data.ModelRPMLimit)
	if err := updateInt64MapFromAPI(&data.ModelRPMLimit, object, rpmOwned, rpmOwned, "metadata", "model_rpm_limit"); err != nil {
		return err
	}
	tpmOwned := imported || knownMap(data.ModelTPMLimit)
	if err := updateInt64MapFromAPI(&data.ModelTPMLimit, object, tpmOwned, tpmOwned, "metadata", "model_tpm_limit"); err != nil {
		return err
	}
	return nil
}

func seedProjectClearOwnership(target, prior *ProjectResourceModel) {
	if target.MaxBudget.IsNull() && knownFloat(prior.MaxBudget) {
		target.MaxBudget = prior.MaxBudget
	}
	if target.SoftBudget.IsNull() && knownFloat(prior.SoftBudget) {
		target.SoftBudget = prior.SoftBudget
	}
	if target.TPMLimit.IsNull() && knownInt(prior.TPMLimit) {
		target.TPMLimit = prior.TPMLimit
	}
	if target.RPMLimit.IsNull() && knownInt(prior.RPMLimit) {
		target.RPMLimit = prior.RPMLimit
	}
	if target.MaxParallelRequests.IsNull() && knownInt(prior.MaxParallelRequests) {
		target.MaxParallelRequests = prior.MaxParallelRequests
	}
	if target.BudgetDuration.IsNull() && knownString(prior.BudgetDuration) {
		target.BudgetDuration = prior.BudgetDuration
	}
	if target.ModelMaxBudget.IsNull() && knownMap(prior.ModelMaxBudget) {
		target.ModelMaxBudget = prior.ModelMaxBudget
	}
	if target.ModelRPMLimit.IsNull() && knownMap(prior.ModelRPMLimit) {
		target.ModelRPMLimit = prior.ModelRPMLimit
	}
	if target.ModelTPMLimit.IsNull() && knownMap(prior.ModelTPMLimit) {
		target.ModelTPMLimit = prior.ModelTPMLimit
	}
}

func projectChangedFieldMismatch(desired, prior, actual *ProjectResourceModel) (string, bool) {
	for _, field := range []struct {
		name                   string
		desired, prior, actual attr.Value
	}{
		{"project_alias", desired.ProjectAlias, prior.ProjectAlias, actual.ProjectAlias}, {"description", desired.Description, prior.Description, actual.Description},
		{"models", desired.Models, prior.Models, actual.Models}, {"blocked", desired.Blocked, prior.Blocked, actual.Blocked},
		{"metadata", desired.Metadata, prior.Metadata, actual.Metadata}, {"tags", desired.Tags, prior.Tags, actual.Tags},
		{"max_budget", desired.MaxBudget, prior.MaxBudget, actual.MaxBudget}, {"soft_budget", desired.SoftBudget, prior.SoftBudget, actual.SoftBudget},
		{"budget_duration", desired.BudgetDuration, prior.BudgetDuration, actual.BudgetDuration},
		{"tpm_limit", desired.TPMLimit, prior.TPMLimit, actual.TPMLimit}, {"rpm_limit", desired.RPMLimit, prior.RPMLimit, actual.RPMLimit},
		{"max_parallel_requests", desired.MaxParallelRequests, prior.MaxParallelRequests, actual.MaxParallelRequests},
		{"model_max_budget", desired.ModelMaxBudget, prior.ModelMaxBudget, actual.ModelMaxBudget}, {"model_rpm_limit", desired.ModelRPMLimit, prior.ModelRPMLimit, actual.ModelRPMLimit}, {"model_tpm_limit", desired.ModelTPMLimit, prior.ModelTPMLimit, actual.ModelTPMLimit},
	} {
		if !field.desired.IsUnknown() && !field.desired.Equal(field.prior) && !field.desired.Equal(field.actual) {
			return field.name, true
		}
	}
	return "", false
}

func updateProjectOptionalString(target *types.String, object map[string]interface{}, field string, imported bool) error {
	owned := imported || knownString(*target)
	value, presence, err := apiValueAt(object, field)
	if err != nil {
		return err
	}
	if presence != apiValuePresent {
		if owned || target.IsUnknown() {
			*target = types.StringNull()
		}
		return nil
	}
	stringValue, ok := value.(string)
	if !ok {
		return fmt.Errorf("invalid response field %q: expected a string or null", field)
	}
	if owned {
		*target = types.StringValue(stringValue)
	} else if target.IsUnknown() {
		*target = types.StringNull()
	}
	return nil
}

func projectBudgetControlsConfigured(data *ProjectResourceModel) bool {
	return knownFloat(data.MaxBudget) || knownFloat(data.SoftBudget) || knownString(data.BudgetDuration) ||
		knownInt(data.TPMLimit) || knownInt(data.RPMLimit) || knownInt(data.MaxParallelRequests) || knownMap(data.ModelMaxBudget)
}

func projectBudgetControlsPresentInConfig(data *ProjectResourceModel) bool {
	return !data.MaxBudget.IsNull() || !data.SoftBudget.IsNull() || !data.BudgetDuration.IsNull() ||
		!data.TPMLimit.IsNull() || !data.RPMLimit.IsNull() || !data.MaxParallelRequests.IsNull() || !data.ModelMaxBudget.IsNull()
}

func updateLegacyProjectModelMaxBudget(target *types.Map, table budgetTableState) error {
	// The public project schema predates LiteLLM v1.98 and can only represent
	// map(float64), while v1.98 returns map(GenericBudgetConfig). Never adopt a
	// remote value into unconfigured/import state, and retain existing legacy
	// scalar state when the authoritative value has the structured v1.98 shape.
	if target.IsNull() || target.IsUnknown() {
		if target.IsUnknown() {
			*target = types.MapNull(types.Float64Type)
		}
		return nil
	}
	value, presence, err := table.value("model_max_budget")
	if err != nil {
		return err
	}
	if presence != apiValuePresent {
		*target = types.MapNull(types.Float64Type)
		return nil
	}
	object, ok := value.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid response field %q: expected an object", "litellm_budget_table.model_max_budget")
	}
	for _, raw := range object {
		if _, structured := raw.(map[string]interface{}); structured {
			return nil
		}
	}
	return updateFloat64MapFromAPI(target, table.object, true, true, "model_max_budget")
}

func updateNullableString(target *types.String, object map[string]interface{}, field string) error {
	value, exists := object[field]
	if !exists || value == nil {
		*target = types.StringNull()
		return nil
	}
	stringValue, ok := value.(string)
	if !ok {
		return fmt.Errorf("invalid response field %q: expected a string or null", field)
	}
	*target = types.StringValue(stringValue)
	return nil
}
