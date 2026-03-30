package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &ProjectResource{}
var _ resource.ResourceWithImportState = &ProjectResource{}

func NewProjectResource() resource.Resource {
	return &ProjectResource{}
}

type ProjectResource struct {
	client *Client
}

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

func (r *ProjectResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *ProjectResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM Project. Projects sit between teams and keys in the hierarchy, allowing fine-grained budget and model access control within a team.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique project ID (assigned by LiteLLM).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"project_alias": schema.StringAttribute{
				Description: "Human-friendly name for the project.",
				Optional:    true,
			},
			"description": schema.StringAttribute{
				Description: "Description of the project's purpose and use case.",
				Optional:    true,
			},
			"team_id": schema.StringAttribute{
				Description: "The team ID that this project belongs to. Required.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"models": schema.ListAttribute{
				Description: "List of models the project can access.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"metadata": schema.MapAttribute{
				Description: "Metadata for the project. Values are strings; use jsonencode() for complex values.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"tags": schema.ListAttribute{
				Description: "Tags associated with the project.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"max_budget": schema.Float64Attribute{
				Description: "Maximum budget for this project.",
				Optional:    true,
			},
			"soft_budget": schema.Float64Attribute{
				Description: "Soft budget limit for warnings.",
				Optional:    true,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Budget reset duration (e.g. '30d', '1h').",
				Optional:    true,
			},
			"budget_id": schema.StringAttribute{
				Description: "Budget ID to associate with this project.",
				Optional:    true,
			},
			"tpm_limit": schema.Int64Attribute{
				Description: "Tokens per minute limit.",
				Optional:    true,
			},
			"rpm_limit": schema.Int64Attribute{
				Description: "Requests per minute limit.",
				Optional:    true,
			},
			"max_parallel_requests": schema.Int64Attribute{
				Description: "Maximum parallel requests allowed.",
				Optional:    true,
			},
			"model_max_budget": schema.MapAttribute{
				Description: "Per-model budget limits.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Float64Type,
			},
			"model_rpm_limit": schema.MapAttribute{
				Description: "Per-model RPM limits.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
			},
			"model_tpm_limit": schema.MapAttribute{
				Description: "Per-model TPM limits.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the project is blocked from making requests.",
				Optional:    true,
				Computed:    true,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the project was created.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the project was last updated.",
				Computed:    true,
			},
			"created_by": schema.StringAttribute{
				Description: "User who created the project.",
				Computed:    true,
			},
			"updated_by": schema.StringAttribute{
				Description: "User who last updated the project.",
				Computed:    true,
			},
		},
	}
}

func (r *ProjectResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *ProjectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ProjectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	projectReq := r.buildProjectRequest(ctx, &data)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/project/new", projectReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create project: %s", err))
		return
	}

	if projectID, ok := result["project_id"].(string); ok {
		data.ID = types.StringValue(projectID)
	}

	if err := r.readProject(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Project created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProjectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ProjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := RetryOnNotFound(ctx, func() error {
		return r.readProject(ctx, &data)
	}, 3); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read project: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProjectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ProjectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state ProjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ID = state.ID

	updateReq := r.buildProjectRequest(ctx, &data)
	updateReq["project_id"] = data.ID.ValueString()

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/project/update", updateReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update project: %s", err))
		return
	}

	if err := r.readProject(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Project updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProjectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ProjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := map[string]interface{}{
		"project_ids": []string{data.ID.ValueString()},
	}

	if err := r.client.DoRequestWithResponse(ctx, "DELETE", "/project/delete", deleteReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete project: %s", err))
		return
	}
}

func (r *ProjectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// --- Build request ---

func (r *ProjectResource) buildProjectRequest(ctx context.Context, data *ProjectResourceModel) map[string]interface{} {
	req := map[string]interface{}{}

	if !data.TeamID.IsNull() && !data.TeamID.IsUnknown() {
		req["team_id"] = data.TeamID.ValueString()
	}
	if !data.ProjectAlias.IsNull() && !data.ProjectAlias.IsUnknown() && data.ProjectAlias.ValueString() != "" {
		req["project_alias"] = data.ProjectAlias.ValueString()
	}
	if !data.Description.IsNull() && !data.Description.IsUnknown() && data.Description.ValueString() != "" {
		req["description"] = data.Description.ValueString()
	}
	if !data.BudgetID.IsNull() && !data.BudgetID.IsUnknown() && data.BudgetID.ValueString() != "" {
		req["budget_id"] = data.BudgetID.ValueString()
	}
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() && data.BudgetDuration.ValueString() != "" {
		req["budget_duration"] = data.BudgetDuration.ValueString()
	}

	// Float fields
	if !data.MaxBudget.IsNull() && !data.MaxBudget.IsUnknown() {
		req["max_budget"] = data.MaxBudget.ValueFloat64()
	}
	if !data.SoftBudget.IsNull() && !data.SoftBudget.IsUnknown() {
		req["soft_budget"] = data.SoftBudget.ValueFloat64()
	}

	// Int fields
	if !data.TPMLimit.IsNull() && !data.TPMLimit.IsUnknown() {
		req["tpm_limit"] = data.TPMLimit.ValueInt64()
	}
	if !data.RPMLimit.IsNull() && !data.RPMLimit.IsUnknown() {
		req["rpm_limit"] = data.RPMLimit.ValueInt64()
	}
	if !data.MaxParallelRequests.IsNull() && !data.MaxParallelRequests.IsUnknown() {
		req["max_parallel_requests"] = data.MaxParallelRequests.ValueInt64()
	}

	// Bool
	if !data.Blocked.IsNull() && !data.Blocked.IsUnknown() {
		req["blocked"] = data.Blocked.ValueBool()
	}

	// Lists
	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		var models []string
		data.Models.ElementsAs(ctx, &models, false)
		req["models"] = models
	}
	if !data.Tags.IsNull() && !data.Tags.IsUnknown() {
		var tags []string
		data.Tags.ElementsAs(ctx, &tags, false)
		if len(tags) > 0 {
			req["tags"] = tags
		}
	}

	// Maps
	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var metadata map[string]string
		data.Metadata.ElementsAs(ctx, &metadata, false)
		if len(metadata) > 0 {
			req["metadata"] = convertMetadataToNative(metadata)
		}
	}
	if !data.ModelMaxBudget.IsNull() && !data.ModelMaxBudget.IsUnknown() {
		budgetMap := map[string]interface{}{}
		for k, v := range data.ModelMaxBudget.Elements() {
			if fv, ok := v.(types.Float64); ok {
				budgetMap[k] = fv.ValueFloat64()
			}
		}
		if len(budgetMap) > 0 {
			req["model_max_budget"] = budgetMap
		}
	}
	if !data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown() {
		rpmMap := map[string]interface{}{}
		for k, v := range data.ModelRPMLimit.Elements() {
			if iv, ok := v.(types.Int64); ok {
				rpmMap[k] = iv.ValueInt64()
			}
		}
		if len(rpmMap) > 0 {
			req["model_rpm_limit"] = rpmMap
		}
	}
	if !data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown() {
		tpmMap := map[string]interface{}{}
		for k, v := range data.ModelTPMLimit.Elements() {
			if iv, ok := v.(types.Int64); ok {
				tpmMap[k] = iv.ValueInt64()
			}
		}
		if len(tpmMap) > 0 {
			req["model_tpm_limit"] = tpmMap
		}
	}

	return req
}

// --- Read project ---

func (r *ProjectResource) readProject(ctx context.Context, data *ProjectResourceModel) error {
	projectID := data.ID.ValueString()
	if projectID == "" {
		return fmt.Errorf("project ID is empty, cannot read project")
	}

	endpoint := fmt.Sprintf("/project/info?project_id=%s", url.QueryEscape(projectID))

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// Top-level fields
	if v, ok := result["project_id"].(string); ok {
		data.ID = types.StringValue(v)
	}
	if v, ok := result["project_alias"].(string); ok && v != "" {
		data.ProjectAlias = types.StringValue(v)
	}
	if v, ok := result["description"].(string); ok && v != "" {
		data.Description = types.StringValue(v)
	}
	if v, ok := result["team_id"].(string); ok && v != "" {
		data.TeamID = types.StringValue(v)
	}

	// Budget fields — only set if user configured them
	if v, ok := result["budget_id"].(string); ok && v != "" && !data.BudgetID.IsNull() {
		data.BudgetID = types.StringValue(v)
	}

	// Bool
	if v, ok := result["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(v)
	} else if data.Blocked.IsUnknown() {
		data.Blocked = types.BoolValue(false)
	}

	// Computed timestamps
	if v, ok := result["created_at"].(string); ok && v != "" {
		data.CreatedAt = types.StringValue(v)
	}
	if v, ok := result["updated_at"].(string); ok && v != "" {
		data.UpdatedAt = types.StringValue(v)
	}
	if v, ok := result["created_by"].(string); ok && v != "" {
		data.CreatedBy = types.StringValue(v)
	}
	if v, ok := result["updated_by"].(string); ok && v != "" {
		data.UpdatedBy = types.StringValue(v)
	}

	// Models list
	if models, ok := result["models"].([]interface{}); ok && len(models) > 0 {
		modelsList := make([]attr.Value, 0, len(models))
		for _, m := range models {
			if str, ok := m.(string); ok {
				modelsList = append(modelsList, types.StringValue(str))
			}
		}
		data.Models, _ = types.ListValue(types.StringType, modelsList)
	} else if data.Models.IsUnknown() {
		data.Models, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Tags
	if tags, ok := result["tags"].([]interface{}); ok && len(tags) > 0 {
		tagsList := make([]attr.Value, 0, len(tags))
		for _, t := range tags {
			if str, ok := t.(string); ok {
				tagsList = append(tagsList, types.StringValue(str))
			}
		}
		data.Tags, _ = types.ListValue(types.StringType, tagsList)
	} else if data.Tags.IsUnknown() {
		data.Tags, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Metadata
	if metadata, ok := result["metadata"].(map[string]interface{}); ok && len(metadata) > 0 {
		metaMap := make(map[string]attr.Value)
		for k, v := range metadata {
			metaMap[k] = types.StringValue(metadataValueToString(v))
		}
		data.Metadata, _ = types.MapValue(types.StringType, metaMap)
	} else if data.Metadata.IsUnknown() {
		data.Metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Model max budget
	if mmb, ok := result["model_max_budget"].(map[string]interface{}); ok && len(mmb) > 0 {
		budgetMap := make(map[string]attr.Value)
		for k, v := range mmb {
			if num, ok := v.(float64); ok {
				budgetMap[k] = types.Float64Value(num)
			}
		}
		data.ModelMaxBudget, _ = types.MapValue(types.Float64Type, budgetMap)
	} else if data.ModelMaxBudget.IsUnknown() {
		data.ModelMaxBudget, _ = types.MapValue(types.Float64Type, map[string]attr.Value{})
	}

	// Model RPM limit
	if mrpm, ok := result["model_rpm_limit"].(map[string]interface{}); ok && len(mrpm) > 0 {
		rpmMap := make(map[string]attr.Value)
		for k, v := range mrpm {
			if num, ok := v.(float64); ok {
				rpmMap[k] = types.Int64Value(int64(num))
			}
		}
		data.ModelRPMLimit, _ = types.MapValue(types.Int64Type, rpmMap)
	} else if data.ModelRPMLimit.IsUnknown() {
		data.ModelRPMLimit, _ = types.MapValue(types.Int64Type, map[string]attr.Value{})
	}

	// Model TPM limit
	if mtpm, ok := result["model_tpm_limit"].(map[string]interface{}); ok && len(mtpm) > 0 {
		tpmMap := make(map[string]attr.Value)
		for k, v := range mtpm {
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
