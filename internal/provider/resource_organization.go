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

var _ resource.Resource = &OrganizationResource{}
var _ resource.ResourceWithImportState = &OrganizationResource{}
var _ resource.ResourceWithModifyPlan = &OrganizationResource{}

func NewOrganizationResource() resource.Resource { return &OrganizationResource{} }

type OrganizationResource struct{ client *Client }

type OrganizationResourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	OrganizationID      types.String  `tfsdk:"organization_id"`
	OrganizationAlias   types.String  `tfsdk:"organization_alias"`
	Models              types.List    `tfsdk:"models"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	ModelRPMLimit       types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit       types.Map     `tfsdk:"model_tpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	Metadata            types.Map     `tfsdk:"metadata"`
	Blocked             types.Bool    `tfsdk:"blocked"`
	Tags                types.List    `tfsdk:"tags"`
	CreatedAt           types.String  `tfsdk:"created_at"`
}

func (r *OrganizationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (r *OrganizationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM organization. Budget controls are read authoritatively from LiteLLM v1.98's nested litellm_budget_table relation.",
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Description: "The unique identifier for this organization (same as organization_id).", Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"organization_id":       schema.StringAttribute{Description: "The organization ID. If not specified, one will be generated.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}},
			"organization_alias":    schema.StringAttribute{Description: "The name/alias of the organization.", Required: true},
			"models":                schema.ListAttribute{Description: "The models the organization has access to.", Optional: true, Computed: true, ElementType: types.StringType},
			"budget_id":             schema.StringAttribute{Description: "The ID for the organization's budget. Reassociating an existing organization is not supported safely by LiteLLM v1.98.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"max_budget":            schema.Float64Attribute{Description: "Maximum hard budget for the organization.", Optional: true},
			"soft_budget":           schema.Float64Attribute{Description: "Soft budget alert threshold for the organization.", Optional: true},
			"tpm_limit":             schema.Int64Attribute{Description: "Maximum tokens per minute for the organization.", Optional: true},
			"rpm_limit":             schema.Int64Attribute{Description: "Maximum requests per minute for the organization.", Optional: true},
			"max_parallel_requests": schema.Int64Attribute{Description: "Maximum parallel requests for the organization budget.", Optional: true},
			"model_rpm_limit":       schema.MapAttribute{Description: "The RPM limit per model. Updated through v1.98's transactional complete-metadata replacement so owned keys can clear safely.", Optional: true, Computed: true, ElementType: types.Int64Type, Validators: []validator.Map{mapvalidator.NoNullValues()}},
			"model_tpm_limit":       schema.MapAttribute{Description: "The TPM limit per model. Updated through v1.98's transactional complete-metadata replacement so owned keys can clear safely.", Optional: true, Computed: true, ElementType: types.Int64Type, Validators: []validator.Map{mapvalidator.NoNullValues()}},
			"budget_duration":       schema.StringAttribute{Description: "Budget reset duration (for example, '30d' or '1h').", Optional: true},
			"metadata":              schema.MapAttribute{Description: "Metadata for the organization.", Optional: true, Computed: true, ElementType: types.StringType},
			"blocked":               schema.BoolAttribute{Description: "Deprecated compatibility field. LiteLLM v1.98 has no persistent organization blocked column; false is accepted as a no-op and true is rejected.", Optional: true, Computed: true, DeprecationMessage: "LiteLLM v1.98 does not persist organization blocked state. Remove this argument; use supported team/project controls instead."},
			"tags":                  schema.ListAttribute{Description: "Deprecated compatibility field. LiteLLM v1.98 has no persistent organization tags column; an empty list is accepted as a no-op and non-empty values are rejected.", Optional: true, Computed: true, ElementType: types.StringType, DeprecationMessage: "LiteLLM v1.98 does not persist organization tags. Remove this argument or store labels in metadata."},
			"created_at":            schema.StringAttribute{Description: "Timestamp when the organization was created.", Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *OrganizationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := configuredClient(req.ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *OrganizationResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if organizationProjectPlanIsDestroy(req) {
		return
	}
	var plan, config OrganizationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !req.Config.Raw.IsNull() {
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	var state OrganizationResourceModel
	hasState := !req.State.Raw.IsNull()
	importedBudget := false
	if !hasState && !config.BudgetID.IsNull() && organizationBudgetControlsPresentInConfig(&config) {
		resp.Diagnostics.AddAttributeError(path.Root("budget_id"), "Unsafe Shared Organization Budget Controls", "budget_id cannot be combined with organization budget controls during creation because LiteLLM v1.98 ignores or strips those controls for an existing shared budget.")
	}
	if hasState {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if req.Private != nil {
			marker, diagnostics := req.Private.GetKey(ctx, organizationProjectImportedBudgetPrivateKey)
			resp.Diagnostics.Append(diagnostics...)
			importedBudget = string(marker) == "true"
		}
		preserveOrganizationProjectBudgetID(ctx, "Organization", state.BudgetID, config.BudgetID, plan.BudgetID, importedBudget, resp)
	}

	if !config.Blocked.IsNull() && !config.Blocked.IsUnknown() {
		legacyUnchanged := hasState && !state.Blocked.IsNull() && !state.Blocked.IsUnknown() && state.Blocked.Equal(plan.Blocked)
		if config.Blocked.ValueBool() && !legacyUnchanged {
			resp.Diagnostics.AddAttributeError(path.Root("blocked"), "Unsupported Organization Blocked Setting", "LiteLLM v1.98 has no persistent organization blocked field. Setting blocked=true would report false success, so the provider refuses this plan.")
		} else {
			resp.Diagnostics.AddAttributeWarning(path.Root("blocked"), "Deprecated Organization Compatibility Field", "LiteLLM v1.98 does not persist organization blocked state. This value is retained only for compatibility and is never sent to LiteLLM.")
		}
	}
	if !config.Tags.IsNull() && !config.Tags.IsUnknown() {
		legacyUnchanged := hasState && !state.Tags.IsNull() && !state.Tags.IsUnknown() && state.Tags.Equal(plan.Tags)
		if len(config.Tags.Elements()) > 0 && !legacyUnchanged {
			resp.Diagnostics.AddAttributeError(path.Root("tags"), "Unsupported Organization Tags", "LiteLLM v1.98 has no persistent organization tags field. Non-empty tags would report false success, so the provider refuses this plan. Store labels in metadata instead.")
		} else {
			resp.Diagnostics.AddAttributeWarning(path.Root("tags"), "Deprecated Organization Compatibility Field", "LiteLLM v1.98 does not persist organization tags. This value is retained only for compatibility and is never sent to LiteLLM.")
		}
	}
	if hasState && !resp.Diagnostics.HasError() {
		budgetTransition := planImportedOmissionOwnership(ctx, organizationProjectBudgetOwnershipPendingPrivateKey, importedBudget, config.BudgetID, resp)
		forceImportedOwnershipUpdate(ctx, "created_at", budgetTransition && state.BudgetID.Equal(config.BudgetID), resp)
	}
}

func (r *OrganizationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data OrganizationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	organizationRequest, err := r.buildOrganizationCreateRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Organization Request", err.Error())
		return
	}
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/organization/new", organizationRequest, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create organization: %s", err))
		return
	}
	object, err := unwrapObjectEnvelope(result, "organization_info", "data")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	organizationID, ok := object["organization_id"].(string)
	if !ok || organizationID == "" {
		resp.Diagnostics.AddError("Invalid API Response", "Organization create response did not contain a nonempty organization_id.")
		return
	}
	data.OrganizationID = types.StringValue(organizationID)
	data.ID = types.StringValue(organizationID)

	// Organization creation stores budget_duration but does not initialize
	// budget_reset_at. Re-send the duration through v2's transactional writer.
	if knownString(data.BudgetDuration) {
		var resetResult map[string]interface{}
		endpoint := "/v2/organization/" + url.PathEscape(organizationID)
		if err := r.client.DoRequestWithResponse(ctx, "PATCH", endpoint, map[string]interface{}{"budget_duration": data.BudgetDuration.ValueString()}, &resetResult); err != nil {
			resp.Diagnostics.AddError("Budget Reset Initialization Error", fmt.Sprintf("Organization was created, but LiteLLM could not initialize its budget reset schedule: %s", err))
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
		resetObject, err := unwrapObjectEnvelope(resetResult, "organization_info", "data")
		if err != nil || validateImportedObjectIdentity(true, "organization reset initialization", resetObject, "organization_id", organizationID) != nil {
			resp.Diagnostics.AddError("Budget Reset Initialization Error", "Organization was created and LiteLLM accepted reset initialization, but the response did not confirm the matching organization identity.")
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
	}
	if err := r.readOrganization(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Read Error", fmt.Sprintf("Organization was created but its authoritative state could not be read: %s", err))
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OrganizationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	marker, privateDiagnostics := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(marker) == "true"
	if err := r.readOrganizationWithNumericOwnership(ctx, &data, imported); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization: %s", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && imported {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
}

func (r *OrganizationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state OrganizationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID, plan.OrganizationID = state.ID, state.OrganizationID
	if state.BudgetID.IsUnknown() || plan.BudgetID.IsUnknown() || !state.BudgetID.Equal(plan.BudgetID) {
		resp.Diagnostics.AddError("Unsafe Organization Budget Reassociation", "The organization budget_id changed or remained unknown despite the plan safety check; no API call was made.")
		return
	}
	if !plan.Blocked.IsNull() && !plan.Blocked.IsUnknown() && plan.Blocked.ValueBool() && !plan.Blocked.Equal(state.Blocked) {
		resp.Diagnostics.AddError("Unsupported Organization Blocked Setting", "blocked=true is not persisted by LiteLLM v1.98; no API call was made.")
		return
	}
	if !plan.Tags.IsNull() && !plan.Tags.IsUnknown() && len(plan.Tags.Elements()) > 0 && !plan.Tags.Equal(state.Tags) {
		resp.Diagnostics.AddError("Unsupported Organization Tags", "Non-empty organization tags are not persisted by LiteLLM v1.98; no API call was made.")
		return
	}
	updateRequest, err := buildOrganizationUpdateRequest(ctx, &plan, &state)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Organization Request", err.Error())
		return
	}
	if organizationUpdateChangesBudget(updateRequest) {
		if _, err := r.lookupOrganizationBudgetID(ctx, state.OrganizationID.ValueString(), state.BudgetID); err != nil {
			resp.Diagnostics.AddError("Organization Budget Lookup Error", err.Error())
			return
		}
	}
	if len(updateRequest) > 0 {
		var result map[string]interface{}
		endpoint := "/v2/organization/" + url.PathEscape(state.OrganizationID.ValueString())
		if err := r.client.DoRequestWithResponse(ctx, "PATCH", endpoint, updateRequest, &result); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update organization: %s", err))
			return
		}
		object, err := unwrapObjectEnvelope(result, "organization_info", "data")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if err := validateImportedObjectIdentity(true, "organization update", object, "organization_id", state.OrganizationID.ValueString()); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
	}
	desired := plan
	seedOrganizationClearOwnership(&plan, &state)
	if err := r.readOrganization(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Read Error", fmt.Sprintf("Organization update was accepted but authoritative read-back failed; prior state was retained: %s", err))
		return
	}
	if field, ok := organizationChangedFieldMismatch(&desired, &state, &plan); ok {
		resp.Diagnostics.AddError("Organization Update Did Not Converge", fmt.Sprintf("LiteLLM accepted the update but authoritative read-back did not match planned %s; prior Terraform state was retained.", field))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		pending, diagnostics := resp.Private.GetKey(ctx, organizationProjectBudgetOwnershipPendingPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		if !resp.Diagnostics.HasError() && string(pending) == "true" {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationProjectImportedBudgetPrivateKey, nil)...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationProjectBudgetOwnershipPendingPrivateKey, nil)...)
		}
	}
}

func (r *OrganizationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OrganizationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", "/organization/delete", map[string]interface{}{"organization_ids": []string{data.OrganizationID.ValueString()}}, nil); err != nil && !IsNotFoundError(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete organization: %s", err))
	}
}

func (r *OrganizationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), req.ID)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationProjectImportedBudgetPrivateKey, []byte("true"))...)
	}
}

// buildOrganizationRequest is retained as the create-payload test seam used by
// earlier releases. Updates use the differential v2 builder below.
func (r *OrganizationResource) buildOrganizationRequest(ctx context.Context, data *OrganizationResourceModel) (map[string]interface{}, error) {
	return r.buildOrganizationCreateRequest(ctx, data)
}

func (r *OrganizationResource) buildOrganizationCreateRequest(ctx context.Context, data *OrganizationResourceModel) (map[string]interface{}, error) {
	if knownString(data.BudgetID) && organizationBudgetControlsConfigured(data) {
		return nil, fmt.Errorf("budget_id cannot be combined with organization budget controls during creation because LiteLLM v1.98 ignores or strips those controls for an existing shared budget")
	}
	if !data.Blocked.IsNull() && !data.Blocked.IsUnknown() && data.Blocked.ValueBool() {
		return nil, fmt.Errorf("blocked=true is unsupported because LiteLLM v1.98 has no persistent organization blocked field")
	}
	if !data.Tags.IsNull() && !data.Tags.IsUnknown() && len(data.Tags.Elements()) > 0 {
		return nil, fmt.Errorf("non-empty tags are unsupported because LiteLLM v1.98 has no persistent organization tags field")
	}
	request := map[string]interface{}{"organization_alias": data.OrganizationAlias.ValueString()}
	if knownString(data.OrganizationID) {
		request["organization_id"] = data.OrganizationID.ValueString()
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
	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		models, err := stringListRequest(ctx, data.Models, "models")
		if err != nil {
			return nil, err
		}
		request["models"] = models
	}
	metadata, err := organizationMetadataPayload(ctx, data)
	if err != nil {
		return nil, err
	}
	if len(metadata) > 0 {
		request["metadata"] = metadata
	}
	return request, nil
}

func buildOrganizationUpdateRequest(ctx context.Context, plan, state *OrganizationResourceModel) (map[string]interface{}, error) {
	request := map[string]interface{}{}
	if !plan.OrganizationAlias.IsUnknown() && !plan.OrganizationAlias.Equal(state.OrganizationAlias) {
		request["organization_alias"] = plan.OrganizationAlias.ValueString()
	}
	if !plan.Models.IsUnknown() && !plan.Models.Equal(state.Models) {
		if plan.Models.IsNull() {
			return nil, fmt.Errorf("models cannot be cleared with null on LiteLLM v1.98; configure an empty list instead")
		}
		models, err := stringListRequest(ctx, plan.Models, "models")
		if err != nil {
			return nil, err
		}
		request["models"] = models
	}
	addChangedFloat(request, "max_budget", plan.MaxBudget, state.MaxBudget)
	addChangedFloat(request, "soft_budget", plan.SoftBudget, state.SoftBudget)
	addChangedInt(request, "tpm_limit", plan.TPMLimit, state.TPMLimit)
	addChangedInt(request, "rpm_limit", plan.RPMLimit, state.RPMLimit)
	addChangedInt(request, "max_parallel_requests", plan.MaxParallelRequests, state.MaxParallelRequests)
	if !plan.BudgetDuration.IsUnknown() && !plan.BudgetDuration.Equal(state.BudgetDuration) {
		if plan.BudgetDuration.IsNull() {
			request["budget_duration"] = nil
		} else {
			request["budget_duration"] = plan.BudgetDuration.ValueString()
		}
	}
	metadataChanged := (!plan.Metadata.IsUnknown() && !plan.Metadata.Equal(state.Metadata)) || (!plan.ModelRPMLimit.IsUnknown() && !plan.ModelRPMLimit.Equal(state.ModelRPMLimit)) || (!plan.ModelTPMLimit.IsUnknown() && !plan.ModelTPMLimit.Equal(state.ModelTPMLimit))
	if metadataChanged {
		metadata, err := organizationMetadataPayload(ctx, plan)
		if err != nil {
			return nil, err
		}
		request["metadata"] = metadata
	}
	return request, nil
}

func organizationBudgetControlsConfigured(data *OrganizationResourceModel) bool {
	return knownFloat(data.MaxBudget) || knownFloat(data.SoftBudget) || knownString(data.BudgetDuration) ||
		knownInt(data.TPMLimit) || knownInt(data.RPMLimit) || knownInt(data.MaxParallelRequests)
}

func organizationBudgetControlsPresentInConfig(data *OrganizationResourceModel) bool {
	return !data.MaxBudget.IsNull() || !data.SoftBudget.IsNull() || !data.BudgetDuration.IsNull() ||
		!data.TPMLimit.IsNull() || !data.RPMLimit.IsNull() || !data.MaxParallelRequests.IsNull()
}

func organizationUpdateChangesBudget(request map[string]interface{}) bool {
	for _, field := range []string{"max_budget", "soft_budget", "budget_duration", "tpm_limit", "rpm_limit", "max_parallel_requests"} {
		if _, changed := request[field]; changed {
			return true
		}
	}
	return false
}

func (r *OrganizationResource) lookupOrganizationBudgetID(ctx context.Context, organizationID string, configured types.String) (string, error) {
	var result map[string]interface{}
	endpoint := "/organization/info?organization_id=" + url.QueryEscape(organizationID)
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return "", fmt.Errorf("unable to read authoritative organization budget: %w", err)
	}
	object, err := unwrapObjectEnvelope(result, "organization_info", "data")
	if err != nil {
		return "", err
	}
	if err := validateImportedObjectIdentity(true, "organization budget lookup", object, "organization_id", organizationID); err != nil {
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
		return "", fmt.Errorf("organization %q has no authoritative budget identity", organizationID)
	}
	if knownString(configured) && configured.ValueString() != budgetID {
		return "", fmt.Errorf("organization budget reassociation detected: state budget_id %q, API budget_id %q", configured.ValueString(), budgetID)
	}
	return budgetID, nil
}

func organizationMetadataPayload(ctx context.Context, data *OrganizationResourceModel) (map[string]interface{}, error) {
	metadata := map[string]interface{}{}
	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var values map[string]string
		if diagnostics := data.Metadata.ElementsAs(ctx, &values, false); diagnostics.HasError() {
			return nil, fmt.Errorf("metadata contains a value that cannot be represented as a string")
		}
		metadata = convertMetadataToNative(values)
		delete(metadata, "model_rpm_limit")
		delete(metadata, "model_tpm_limit")
	}
	if !data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown() {
		values, err := int64RequestMap(data.ModelRPMLimit, "model_rpm_limit")
		if err != nil {
			return nil, err
		}
		metadata["model_rpm_limit"] = values
	}
	if !data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown() {
		values, err := int64RequestMap(data.ModelTPMLimit, "model_tpm_limit")
		if err != nil {
			return nil, err
		}
		metadata["model_tpm_limit"] = values
	}
	return metadata, nil
}

func (r *OrganizationResource) readOrganization(ctx context.Context, data *OrganizationResourceModel) error {
	return r.readOrganizationWithNumericOwnership(ctx, data, false)
}

func (r *OrganizationResource) readOrganizationWithNumericOwnership(ctx context.Context, data *OrganizationResourceModel, imported bool) error {
	organizationID := data.OrganizationID.ValueString()
	if organizationID == "" {
		organizationID = data.ID.ValueString()
	}
	if organizationID == "" {
		return fmt.Errorf("organization ID is empty, cannot read organization")
	}
	var result map[string]interface{}
	endpoint := "/organization/info?organization_id=" + url.QueryEscape(organizationID)
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}
	object, err := unwrapObjectEnvelope(result, "organization_info", "data")
	if err != nil {
		return err
	}
	if err := validateImportedObjectIdentity(true, "organization", object, "organization_id", organizationID); err != nil {
		return err
	}
	if err := requireImportedStringField(imported, "organization", object, "organization_alias"); err != nil {
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
			return fmt.Errorf("organization budget reassociation detected: state budget_id %q, API budget_id %q", data.BudgetID.ValueString(), remoteBudgetID)
		}
		data.BudgetID = types.StringValue(remoteBudgetID)
	} else if budgetOwned && (budgetPresence == apiValueNull || budgetPresence == apiValueAbsent) {
		data.BudgetID = types.StringNull()
	} else if !budgetOwned && data.BudgetID.IsUnknown() {
		// Optional+Computed is required for import adoption, but ordinary creates
		// with omitted budget_id must not adopt LiteLLM's generated default.
		data.BudgetID = types.StringNull()
	}

	data.ID = types.StringValue(organizationID)
	data.OrganizationID = types.StringValue(organizationID)
	if value, ok := object["organization_alias"].(string); ok {
		data.OrganizationAlias = types.StringValue(value)
	}
	if value, ok := object["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(value)
	} else if data.CreatedAt.IsUnknown() {
		data.CreatedAt = types.StringNull()
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
	nextMetadata, metadataPresence, err := stringMapFromAPI(object, "metadata", "model_rpm_limit", "model_tpm_limit")
	if err != nil {
		return err
	}
	if metadataPresence != apiValuePresent {
		nextMetadata = types.MapNull(types.StringType)
	}
	nextRPM, nextTPM := data.ModelRPMLimit, data.ModelTPMLimit
	rpmOwned := imported || knownMap(data.ModelRPMLimit)
	if err := updateInt64MapFromAPI(&nextRPM, object, rpmOwned, rpmOwned, "metadata", "model_rpm_limit"); err != nil {
		return err
	}
	tpmOwned := imported || knownMap(data.ModelTPMLimit)
	if err := updateInt64MapFromAPI(&nextTPM, object, tpmOwned, tpmOwned, "metadata", "model_tpm_limit"); err != nil {
		return err
	}
	data.Metadata, data.ModelRPMLimit, data.ModelTPMLimit = nextMetadata, nextRPM, nextTPM

	// These fields do not exist in the v1.98 organization request/table/response
	// contracts. Ignore equally named API extras rather than adopting phantoms.
	if data.Blocked.IsUnknown() || (imported && data.Blocked.IsNull()) {
		data.Blocked = types.BoolValue(false)
	}
	if data.Tags.IsUnknown() || (imported && data.Tags.IsNull()) {
		data.Tags = types.ListValueMust(types.StringType, []attr.Value{})
	}
	return nil
}

func seedOrganizationClearOwnership(target, prior *OrganizationResourceModel) {
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
	if target.ModelRPMLimit.IsNull() && knownMap(prior.ModelRPMLimit) {
		target.ModelRPMLimit = prior.ModelRPMLimit
	}
	if target.ModelTPMLimit.IsNull() && knownMap(prior.ModelTPMLimit) {
		target.ModelTPMLimit = prior.ModelTPMLimit
	}
}

func organizationChangedFieldMismatch(desired, prior, actual *OrganizationResourceModel) (string, bool) {
	for _, field := range []struct {
		name                   string
		desired, prior, actual attr.Value
	}{
		{"organization_alias", desired.OrganizationAlias, prior.OrganizationAlias, actual.OrganizationAlias},
		{"models", desired.Models, prior.Models, actual.Models},
		{"max_budget", desired.MaxBudget, prior.MaxBudget, actual.MaxBudget}, {"soft_budget", desired.SoftBudget, prior.SoftBudget, actual.SoftBudget},
		{"tpm_limit", desired.TPMLimit, prior.TPMLimit, actual.TPMLimit}, {"rpm_limit", desired.RPMLimit, prior.RPMLimit, actual.RPMLimit},
		{"max_parallel_requests", desired.MaxParallelRequests, prior.MaxParallelRequests, actual.MaxParallelRequests},
		{"budget_duration", desired.BudgetDuration, prior.BudgetDuration, actual.BudgetDuration},
		{"metadata", desired.Metadata, prior.Metadata, actual.Metadata}, {"model_rpm_limit", desired.ModelRPMLimit, prior.ModelRPMLimit, actual.ModelRPMLimit}, {"model_tpm_limit", desired.ModelTPMLimit, prior.ModelTPMLimit, actual.ModelTPMLimit},
	} {
		if !field.desired.IsUnknown() && !field.desired.Equal(field.prior) && !field.desired.Equal(field.actual) {
			return field.name, true
		}
	}
	return "", false
}

func knownString(value types.String) bool { return !value.IsNull() && !value.IsUnknown() }
func knownFloat(value types.Float64) bool { return !value.IsNull() && !value.IsUnknown() }
func knownInt(value types.Int64) bool     { return !value.IsNull() && !value.IsUnknown() }
func knownMap(value types.Map) bool       { return !value.IsNull() && !value.IsUnknown() }

func addKnownFloat(request map[string]interface{}, field string, value types.Float64) {
	if knownFloat(value) {
		request[field] = value.ValueFloat64()
	}
}
func addKnownInt(request map[string]interface{}, field string, value types.Int64) {
	if knownInt(value) {
		request[field] = value.ValueInt64()
	}
}
func addChangedFloat(request map[string]interface{}, field string, plan, state types.Float64) {
	if plan.IsUnknown() || plan.Equal(state) {
		return
	}
	if plan.IsNull() {
		request[field] = nil
	} else {
		request[field] = plan.ValueFloat64()
	}
}
func addChangedInt(request map[string]interface{}, field string, plan, state types.Int64) {
	if plan.IsUnknown() || plan.Equal(state) {
		return
	}
	if plan.IsNull() {
		request[field] = nil
	} else {
		request[field] = plan.ValueInt64()
	}
}
func stringListRequest(ctx context.Context, value types.List, field string) ([]string, error) {
	var result []string
	if diagnostics := value.ElementsAs(ctx, &result, false); diagnostics.HasError() {
		return nil, fmt.Errorf("%s contains a value that is not a known string", field)
	}
	return result, nil
}
