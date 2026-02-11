package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &OrganizationResource{}
var _ resource.ResourceWithImportState = &OrganizationResource{}

func NewOrganizationResource() resource.Resource {
	return &OrganizationResource{}
}

type OrganizationResource struct {
	client *Client
}

type OrganizationResourceModel struct {
	ID                types.String  `tfsdk:"id"`
	OrganizationID    types.String  `tfsdk:"organization_id"`
	OrganizationAlias types.String  `tfsdk:"organization_alias"`
	Models            types.List    `tfsdk:"models"`
	BudgetID          types.String  `tfsdk:"budget_id"`
	MaxBudget         types.Float64 `tfsdk:"max_budget"`
	TPMLimit          types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit          types.Int64   `tfsdk:"rpm_limit"`
	ModelRPMLimit     types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit     types.Map     `tfsdk:"model_tpm_limit"`
	BudgetDuration    types.String  `tfsdk:"budget_duration"`
	Metadata          types.Map     `tfsdk:"metadata"`
	Blocked           types.Bool    `tfsdk:"blocked"`
	Tags              types.List    `tfsdk:"tags"`
	CreatedAt         types.String  `tfsdk:"created_at"`
}

func (r *OrganizationResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (r *OrganizationResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM organization. Organizations can own teams and have org-level budgets and model access.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this organization (same as organization_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"organization_id": schema.StringAttribute{
				Description: "The organization ID. If not specified, one will be generated.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"organization_alias": schema.StringAttribute{
				Description: "The name/alias of the organization.",
				Required:    true,
			},
			"models": schema.ListAttribute{
				Description: "The models the organization has access to.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"budget_id": schema.StringAttribute{
				Description: "The ID for a budget (tpm/rpm/max budget) for the organization.",
				Optional:    true,
			},
			"max_budget": schema.Float64Attribute{
				Description: "Max budget for the organization.",
				Optional:    true,
			},
			"tpm_limit": schema.Int64Attribute{
				Description: "Max TPM limit for the organization.",
				Optional:    true,
			},
			"rpm_limit": schema.Int64Attribute{
				Description: "Max RPM limit for the organization.",
				Optional:    true,
			},
			"model_rpm_limit": schema.MapAttribute{
				Description: "The RPM (Requests Per Minute) limit per model for this organization.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
			},
			"model_tpm_limit": schema.MapAttribute{
				Description: "The TPM (Tokens Per Minute) limit per model for this organization.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Frequency of resetting org budget (e.g., '30d', '1mo').",
				Optional:    true,
			},
			"metadata": schema.MapAttribute{
				Description: "Metadata for the organization.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"blocked": schema.BoolAttribute{
				Description: "Flag indicating if the org is blocked.",
				Optional:    true,
				Computed:    true,
			},
			"tags": schema.ListAttribute{
				Description: "Tags for tracking spend and/or tag-based routing.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the organization was created.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *OrganizationResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *OrganizationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data OrganizationResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgReq := r.buildOrganizationRequest(ctx, &data)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/organization/new", orgReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create organization: %s", err))
		return
	}

	// Extract organization_id from response
	if orgID, ok := result["organization_id"].(string); ok {
		data.OrganizationID = types.StringValue(orgID)
		data.ID = types.StringValue(orgID)
	}

	// Read back for full state
	if err := r.readOrganization(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Organization created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OrganizationResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readOrganization(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data OrganizationResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state OrganizationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve IDs
	data.ID = state.ID
	data.OrganizationID = state.OrganizationID

	orgReq := r.buildOrganizationRequest(ctx, &data)
	orgReq["organization_id"] = data.OrganizationID.ValueString()

	if err := r.client.DoRequestWithResponse(ctx, "PATCH", "/organization/update", orgReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update organization: %s", err))
		return
	}

	// Read back for full state
	if err := r.readOrganization(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Organization updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OrganizationResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := map[string]interface{}{
		"organization_ids": []string{data.OrganizationID.ValueString()},
	}

	if err := r.client.DoRequestWithResponse(ctx, "DELETE", "/organization/delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete organization: %s", err))
			return
		}
	}
}

func (r *OrganizationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), req.ID)...)
}

func (r *OrganizationResource) buildOrganizationRequest(ctx context.Context, data *OrganizationResourceModel) map[string]interface{} {
	orgReq := map[string]interface{}{
		"organization_alias": data.OrganizationAlias.ValueString(),
	}

	// String fields - check IsNull, IsUnknown, and empty string
	if !data.OrganizationID.IsNull() && !data.OrganizationID.IsUnknown() && data.OrganizationID.ValueString() != "" {
		orgReq["organization_id"] = data.OrganizationID.ValueString()
	}
	if !data.BudgetID.IsNull() && !data.BudgetID.IsUnknown() && data.BudgetID.ValueString() != "" {
		orgReq["budget_id"] = data.BudgetID.ValueString()
	}
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() && data.BudgetDuration.ValueString() != "" {
		orgReq["budget_duration"] = data.BudgetDuration.ValueString()
	}

	// Numeric fields - check IsNull and IsUnknown
	if !data.MaxBudget.IsNull() && !data.MaxBudget.IsUnknown() {
		orgReq["max_budget"] = data.MaxBudget.ValueFloat64()
	}
	if !data.TPMLimit.IsNull() && !data.TPMLimit.IsUnknown() {
		orgReq["tpm_limit"] = data.TPMLimit.ValueInt64()
	}
	if !data.RPMLimit.IsNull() && !data.RPMLimit.IsUnknown() {
		orgReq["rpm_limit"] = data.RPMLimit.ValueInt64()
	}

	// Boolean fields - check IsNull and IsUnknown
	if !data.Blocked.IsNull() && !data.Blocked.IsUnknown() {
		orgReq["blocked"] = data.Blocked.ValueBool()
	}

	// List fields - check IsNull, IsUnknown, and len > 0
	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		var models []string
		data.Models.ElementsAs(ctx, &models, false)
		if len(models) > 0 {
			orgReq["models"] = models
		}
	}

	if !data.Tags.IsNull() && !data.Tags.IsUnknown() {
		var tags []string
		data.Tags.ElementsAs(ctx, &tags, false)
		if len(tags) > 0 {
			orgReq["tags"] = tags
		}
	}

	// Map fields - check IsNull, IsUnknown, and len > 0
	if !data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown() {
		var modelRPM map[string]int64
		data.ModelRPMLimit.ElementsAs(ctx, &modelRPM, false)
		if len(modelRPM) > 0 {
			orgReq["model_rpm_limit"] = modelRPM
		}
	}

	if !data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown() {
		var modelTPM map[string]int64
		data.ModelTPMLimit.ElementsAs(ctx, &modelTPM, false)
		if len(modelTPM) > 0 {
			orgReq["model_tpm_limit"] = modelTPM
		}
	}

	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var metadata map[string]string
		data.Metadata.ElementsAs(ctx, &metadata, false)
		if len(metadata) > 0 {
			orgReq["metadata"] = metadata
		}
	}

	return orgReq
}

func (r *OrganizationResource) readOrganization(ctx context.Context, data *OrganizationResourceModel) error {
	orgID := data.OrganizationID.ValueString()
	if orgID == "" {
		orgID = data.ID.ValueString()
	}

	endpoint := fmt.Sprintf("/organization/info?organization_id=%s", orgID)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// The /organization/info endpoint may return data nested inside "organization_info"
	orgInfo := result
	if nested, ok := result["organization_info"].(map[string]interface{}); ok {
		orgInfo = nested
	}

	// Update fields from response
	if orgID, ok := orgInfo["organization_id"].(string); ok {
		data.OrganizationID = types.StringValue(orgID)
		data.ID = types.StringValue(orgID)
	}
	if alias, ok := orgInfo["organization_alias"].(string); ok {
		data.OrganizationAlias = types.StringValue(alias)
	}
	if budgetID, ok := orgInfo["budget_id"].(string); ok && !data.BudgetID.IsNull() {
		data.BudgetID = types.StringValue(budgetID)
	}
	if budgetDuration, ok := orgInfo["budget_duration"].(string); ok {
		data.BudgetDuration = types.StringValue(budgetDuration)
	}
	if createdAt, ok := orgInfo["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(createdAt)
	}

	// Numeric fields
	if maxBudget, ok := orgInfo["max_budget"].(float64); ok {
		data.MaxBudget = types.Float64Value(maxBudget)
	}
	if tpmLimit, ok := orgInfo["tpm_limit"].(float64); ok {
		data.TPMLimit = types.Int64Value(int64(tpmLimit))
	}
	if rpmLimit, ok := orgInfo["rpm_limit"].(float64); ok {
		data.RPMLimit = types.Int64Value(int64(rpmLimit))
	}

	// Boolean fields - resolve Unknown to Null when API returns nil
	if blocked, ok := orgInfo["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
	} else if data.Blocked.IsUnknown() {
		data.Blocked = types.BoolNull()
	}

	// Handle models list - preserve null when API returns empty and config didn't specify models
	if models, ok := orgInfo["models"].([]interface{}); ok && len(models) > 0 {
		modelsList := make([]attr.Value, len(models))
		for i, m := range models {
			if str, ok := m.(string); ok {
				modelsList[i] = types.StringValue(str)
			}
		}
		data.Models, _ = types.ListValue(types.StringType, modelsList)
	} else if data.Models.IsUnknown() {
		data.Models, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle tags list - preserve null when API returns empty and config didn't specify tags
	if tags, ok := orgInfo["tags"].([]interface{}); ok && len(tags) > 0 {
		tagsList := make([]attr.Value, len(tags))
		for i, t := range tags {
			if str, ok := t.(string); ok {
				tagsList[i] = types.StringValue(str)
			}
		}
		data.Tags, _ = types.ListValue(types.StringType, tagsList)
	} else if data.Tags.IsUnknown() {
		data.Tags, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle metadata map - preserve null when API returns empty and config didn't specify metadata
	if metadata, ok := orgInfo["metadata"].(map[string]interface{}); ok && len(metadata) > 0 {
		metaMap := make(map[string]attr.Value)
		for k, v := range metadata {
			if str, ok := v.(string); ok {
				metaMap[k] = types.StringValue(str)
			}
		}
		data.Metadata, _ = types.MapValue(types.StringType, metaMap)
	} else if data.Metadata.IsUnknown() {
		data.Metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle model_rpm_limit map - preserve null when API returns empty and config didn't specify model_rpm_limit
	if modelRPM, ok := orgInfo["model_rpm_limit"].(map[string]interface{}); ok && len(modelRPM) > 0 {
		rpmMap := make(map[string]attr.Value)
		for k, v := range modelRPM {
			if num, ok := v.(float64); ok {
				rpmMap[k] = types.Int64Value(int64(num))
			}
		}
		data.ModelRPMLimit, _ = types.MapValue(types.Int64Type, rpmMap)
	} else if data.ModelRPMLimit.IsUnknown() {
		data.ModelRPMLimit, _ = types.MapValue(types.Int64Type, map[string]attr.Value{})
	}

	// Handle model_tpm_limit map - preserve null when API returns empty and config didn't specify model_tpm_limit
	if modelTPM, ok := orgInfo["model_tpm_limit"].(map[string]interface{}); ok && len(modelTPM) > 0 {
		tpmMap := make(map[string]attr.Value)
		for k, v := range modelTPM {
			if num, ok := v.(float64); ok {
				tpmMap[k] = types.Int64Value(int64(num))
			}
		}
		data.ModelTPMLimit, _ = types.MapValue(types.Int64Type, tpmMap)
	} else if data.ModelTPMLimit.IsUnknown() {
		data.ModelTPMLimit, _ = types.MapValue(types.Int64Type, map[string]attr.Value{})
	}

	return nil
}
