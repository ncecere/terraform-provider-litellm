package provider

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &TeamResource{}
var _ resource.ResourceWithImportState = &TeamResource{}

func NewTeamResource() resource.Resource {
	return &TeamResource{}
}

type TeamResource struct {
	client *Client
}

type TeamResourceModel struct {
	ID                    types.String  `tfsdk:"id"`
	TeamAlias             types.String  `tfsdk:"team_alias"`
	OrganizationID        types.String  `tfsdk:"organization_id"`
	Metadata              types.Map     `tfsdk:"metadata"`
	TPMLimit              types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit              types.Int64   `tfsdk:"rpm_limit"`
	TPMLimitType          types.String  `tfsdk:"tpm_limit_type"`
	RPMLimitType          types.String  `tfsdk:"rpm_limit_type"`
	MaxBudget             types.Float64 `tfsdk:"max_budget"`
	BudgetDuration        types.String  `tfsdk:"budget_duration"`
	Models                types.List    `tfsdk:"models"`
	ModelAliases          types.Map     `tfsdk:"model_aliases"`
	ModelRPMLimit         types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit         types.Map     `tfsdk:"model_tpm_limit"`
	Tags                  types.List    `tfsdk:"tags"`
	Guardrails            types.List    `tfsdk:"guardrails"`
	Prompts               types.List    `tfsdk:"prompts"`
	Blocked               types.Bool    `tfsdk:"blocked"`
	TeamMemberPermissions types.List    `tfsdk:"team_member_permissions"`
	TeamMemberBudget      types.Float64 `tfsdk:"team_member_budget"`
	TeamMemberRPMLimit    types.Int64   `tfsdk:"team_member_rpm_limit"`
	TeamMemberTPMLimit    types.Int64   `tfsdk:"team_member_tpm_limit"`
}

func (r *TeamResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team"
}

func (r *TeamResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM team.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this team.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"team_alias": schema.StringAttribute{
				Description: "User-defined team alias.",
				Required:    true,
			},
			"organization_id": schema.StringAttribute{
				Description: "Organization ID for the team.",
				Optional:    true,
			},
			"metadata": schema.MapAttribute{
				Description: "Arbitrary metadata for the team.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"tpm_limit": schema.Int64Attribute{
				Description: "Tokens per minute limit for the team.",
				Optional:    true,
			},
			"rpm_limit": schema.Int64Attribute{
				Description: "Requests per minute limit for the team.",
				Optional:    true,
			},
			"tpm_limit_type": schema.StringAttribute{
				Description: "Type of TPM limit: 'key' or 'team'. If 'team', TPM is shared across all keys for the team.",
				Optional:    true,
			},
			"rpm_limit_type": schema.StringAttribute{
				Description: "Type of RPM limit: 'key' or 'team'. If 'team', RPM is shared across all keys for the team.",
				Optional:    true,
			},
			"max_budget": schema.Float64Attribute{
				Description: "Maximum budget for the team.",
				Optional:    true,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Budget reset duration.",
				Optional:    true,
			},
			"models": schema.ListAttribute{
				Description: "List of models the team can access.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"model_aliases": schema.MapAttribute{
				Description: "Model alias mappings for the team.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"model_rpm_limit": schema.MapAttribute{
				Description: "Per-model RPM limits for the team.",
				Optional:    true,
				ElementType: types.Int64Type,
			},
			"model_tpm_limit": schema.MapAttribute{
				Description: "Per-model TPM limits for the team.",
				Optional:    true,
				ElementType: types.Int64Type,
			},
			"tags": schema.ListAttribute{
				Description: "Tags for the team (for spend tracking and routing).",
				Optional:    true,
				ElementType: types.StringType,
			},
			"guardrails": schema.ListAttribute{
				Description: "Guardrails for the team.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"prompts": schema.ListAttribute{
				Description: "List of prompt IDs the team can access.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the team is blocked.",
				Optional:    true,
				Computed:    true,
			},
			"team_member_permissions": schema.ListAttribute{
				Description: "List of permissions granted to team members.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"team_member_budget": schema.Float64Attribute{
				Description: "Default budget for team members.",
				Optional:    true,
			},
			"team_member_rpm_limit": schema.Int64Attribute{
				Description: "Default RPM limit for team members.",
				Optional:    true,
			},
			"team_member_tpm_limit": schema.Int64Attribute{
				Description: "Default TPM limit for team members.",
				Optional:    true,
			},
		},
	}
}

func (r *TeamResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TeamResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TeamResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	teamID := uuid.New().String()
	teamReq := r.buildTeamRequest(ctx, &data, teamID)

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/new", teamReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create team: %s", err))
		return
	}

	data.ID = types.StringValue(teamID)

	// Read back
	if err := r.readTeam(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Team created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TeamResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readTeam(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read team: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data TeamResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state TeamResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = state.ID
	teamReq := r.buildTeamRequest(ctx, &data, data.ID.ValueString())

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/update", teamReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update team: %s", err))
		return
	}

	// Update permissions if changed
	if !data.TeamMemberPermissions.Equal(state.TeamMemberPermissions) {
		var permissions []string
		data.TeamMemberPermissions.ElementsAs(ctx, &permissions, false)
		permReq := map[string]interface{}{
			"team_id":                 data.ID.ValueString(),
			"team_member_permissions": permissions,
		}
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/permissions_update", permReq, nil); err != nil {
			resp.Diagnostics.AddWarning("Permissions Update Error", fmt.Sprintf("Failed to update permissions: %s", err))
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TeamResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := map[string]interface{}{
		"team_ids": []string{data.ID.ValueString()},
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete team: %s", err))
			return
		}
	}
}

func (r *TeamResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *TeamResource) buildTeamRequest(ctx context.Context, data *TeamResourceModel, teamID string) map[string]interface{} {
	teamReq := map[string]interface{}{
		"team_id":    teamID,
		"team_alias": data.TeamAlias.ValueString(),
	}

	// String fields - check IsNull, IsUnknown, and empty string
	if !data.OrganizationID.IsNull() && !data.OrganizationID.IsUnknown() && data.OrganizationID.ValueString() != "" {
		teamReq["organization_id"] = data.OrganizationID.ValueString()
	}
	if !data.TPMLimitType.IsNull() && !data.TPMLimitType.IsUnknown() && data.TPMLimitType.ValueString() != "" {
		teamReq["tpm_limit_type"] = data.TPMLimitType.ValueString()
	}
	if !data.RPMLimitType.IsNull() && !data.RPMLimitType.IsUnknown() && data.RPMLimitType.ValueString() != "" {
		teamReq["rpm_limit_type"] = data.RPMLimitType.ValueString()
	}
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() && data.BudgetDuration.ValueString() != "" {
		teamReq["budget_duration"] = data.BudgetDuration.ValueString()
	}

	// Numeric fields - check IsNull and IsUnknown
	if !data.TPMLimit.IsNull() && !data.TPMLimit.IsUnknown() {
		teamReq["tpm_limit"] = data.TPMLimit.ValueInt64()
	}
	if !data.RPMLimit.IsNull() && !data.RPMLimit.IsUnknown() {
		teamReq["rpm_limit"] = data.RPMLimit.ValueInt64()
	}
	if !data.MaxBudget.IsNull() && !data.MaxBudget.IsUnknown() {
		teamReq["max_budget"] = data.MaxBudget.ValueFloat64()
	}
	if !data.TeamMemberBudget.IsNull() && !data.TeamMemberBudget.IsUnknown() {
		teamReq["team_member_budget"] = data.TeamMemberBudget.ValueFloat64()
	}
	if !data.TeamMemberRPMLimit.IsNull() && !data.TeamMemberRPMLimit.IsUnknown() {
		teamReq["team_member_rpm_limit"] = data.TeamMemberRPMLimit.ValueInt64()
	}
	if !data.TeamMemberTPMLimit.IsNull() && !data.TeamMemberTPMLimit.IsUnknown() {
		teamReq["team_member_tpm_limit"] = data.TeamMemberTPMLimit.ValueInt64()
	}

	// Boolean fields - check IsNull and IsUnknown
	if !data.Blocked.IsNull() && !data.Blocked.IsUnknown() {
		teamReq["blocked"] = data.Blocked.ValueBool()
	}

	// List fields - check IsNull, IsUnknown, and len > 0
	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		var models []string
		data.Models.ElementsAs(ctx, &models, false)
		if len(models) > 0 {
			teamReq["models"] = models
		}
	}

	if !data.Tags.IsNull() && !data.Tags.IsUnknown() {
		var tags []string
		data.Tags.ElementsAs(ctx, &tags, false)
		if len(tags) > 0 {
			teamReq["tags"] = tags
		}
	}

	if !data.Guardrails.IsNull() && !data.Guardrails.IsUnknown() {
		var guardrails []string
		data.Guardrails.ElementsAs(ctx, &guardrails, false)
		if len(guardrails) > 0 {
			teamReq["guardrails"] = guardrails
		}
	}

	if !data.Prompts.IsNull() && !data.Prompts.IsUnknown() {
		var prompts []string
		data.Prompts.ElementsAs(ctx, &prompts, false)
		if len(prompts) > 0 {
			teamReq["prompts"] = prompts
		}
	}

	if !data.TeamMemberPermissions.IsNull() && !data.TeamMemberPermissions.IsUnknown() {
		var permissions []string
		data.TeamMemberPermissions.ElementsAs(ctx, &permissions, false)
		if len(permissions) > 0 {
			teamReq["team_member_permissions"] = permissions
		}
	}

	// Map fields - check IsNull, IsUnknown, and len > 0
	if !data.ModelAliases.IsNull() && !data.ModelAliases.IsUnknown() {
		var modelAliases map[string]string
		data.ModelAliases.ElementsAs(ctx, &modelAliases, false)
		if len(modelAliases) > 0 {
			teamReq["model_aliases"] = modelAliases
		}
	}

	if !data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown() {
		var modelRPM map[string]int64
		data.ModelRPMLimit.ElementsAs(ctx, &modelRPM, false)
		if len(modelRPM) > 0 {
			teamReq["model_rpm_limit"] = modelRPM
		}
	}

	if !data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown() {
		var modelTPM map[string]int64
		data.ModelTPMLimit.ElementsAs(ctx, &modelTPM, false)
		if len(modelTPM) > 0 {
			teamReq["model_tpm_limit"] = modelTPM
		}
	}

	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var metadata map[string]string
		data.Metadata.ElementsAs(ctx, &metadata, false)
		if len(metadata) > 0 {
			teamReq["metadata"] = metadata
		}
	}

	return teamReq
}

func (r *TeamResource) readTeam(ctx context.Context, data *TeamResourceModel) error {
	endpoint := fmt.Sprintf("/team/info?team_id=%s", data.ID.ValueString())

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// Update fields from response
	if teamAlias, ok := result["team_alias"].(string); ok && teamAlias != "" {
		data.TeamAlias = types.StringValue(teamAlias)
	}
	if orgID, ok := result["organization_id"].(string); ok && orgID != "" {
		data.OrganizationID = types.StringValue(orgID)
	}
	if tpm, ok := result["tpm_limit"].(float64); ok {
		data.TPMLimit = types.Int64Value(int64(tpm))
	}
	if rpm, ok := result["rpm_limit"].(float64); ok {
		data.RPMLimit = types.Int64Value(int64(rpm))
	}
	if maxBudget, ok := result["max_budget"].(float64); ok {
		data.MaxBudget = types.Float64Value(maxBudget)
	}
	if budgetDuration, ok := result["budget_duration"].(string); ok && budgetDuration != "" {
		data.BudgetDuration = types.StringValue(budgetDuration)
	}
	if blocked, ok := result["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
	}

	// Fetch permissions separately
	permEndpoint := fmt.Sprintf("/team/permissions_list?team_id=%s", data.ID.ValueString())
	var permResult map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", permEndpoint, nil, &permResult); err == nil {
		if perms, ok := permResult["team_member_permissions"].([]interface{}); ok {
			permissions := make([]string, len(perms))
			for i, p := range perms {
				if s, ok := p.(string); ok {
					permissions[i] = s
				}
			}
			data.TeamMemberPermissions, _ = types.ListValueFrom(ctx, types.StringType, permissions)
		}
	}

	return nil
}
