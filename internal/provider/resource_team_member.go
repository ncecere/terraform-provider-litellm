package provider

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &TeamMemberResource{}
var _ resource.ResourceWithImportState = &TeamMemberResource{}

func NewTeamMemberResource() resource.Resource {
	return &TeamMemberResource{}
}

type TeamMemberResource struct {
	client *Client
}

type TeamMemberResourceModel struct {
	ID              types.String  `tfsdk:"id"`
	TeamID          types.String  `tfsdk:"team_id"`
	UserID          types.String  `tfsdk:"user_id"`
	UserEmail       types.String  `tfsdk:"user_email"`
	Role            types.String  `tfsdk:"role"`
	MaxBudgetInTeam types.Float64 `tfsdk:"max_budget_in_team"`
	BudgetDuration  types.String  `tfsdk:"budget_duration"`
}

func (r *TeamMemberResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_member"
}

func (r *TeamMemberResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a single LiteLLM team member.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Composite ID (team_id:user_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"team_id": schema.StringAttribute{
				Description: "Team ID.",
				Required:    true,
			},
			"user_id": schema.StringAttribute{
				Description: "User ID.",
				Required:    true,
			},
			"user_email": schema.StringAttribute{
				Description: "User email.",
				Required:    true,
			},
			"role": schema.StringAttribute{
				Description: "Role in the team (admin or user).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("admin", "user"),
				},
			},
			"max_budget_in_team": schema.Float64Attribute{
				Description: "Maximum budget for this member in the team.",
				Optional:    true,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Recurring reset interval for this member's budget (for example, 30d or 24h). It may be configured without an explicit max to override an inherited/default interval. LiteLLM manages the reset schedule.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						budgetDurationPattern,
						`must be hourly, daily, weekly, monthly, 1mo, or a positive integer with unit s, m, h, d, or w`,
					),
				},
			},
		},
	}
}

func (r *TeamMemberResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func buildTeamMemberAddRequest(data *TeamMemberResourceModel) map[string]interface{} {
	memberReq := map[string]interface{}{
		"member": []map[string]interface{}{
			{
				"role":       data.Role.ValueString(),
				"user_id":    data.UserID.ValueString(),
				"user_email": data.UserEmail.ValueString(),
			},
		},
		"team_id": data.TeamID.ValueString(),
	}
	if !data.MaxBudgetInTeam.IsNull() && !data.MaxBudgetInTeam.IsUnknown() {
		memberReq["max_budget_in_team"] = data.MaxBudgetInTeam.ValueFloat64()
	}
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() {
		memberReq["budget_duration"] = data.BudgetDuration.ValueString()
	}
	return memberReq
}

func buildTeamMemberUpdateRequest(data *TeamMemberResourceModel) map[string]interface{} {
	updateReq := map[string]interface{}{
		"user_id":    data.UserID.ValueString(),
		"user_email": data.UserEmail.ValueString(),
		"team_id":    data.TeamID.ValueString(),
		"role":       data.Role.ValueString(),
	}
	if !data.MaxBudgetInTeam.IsNull() && !data.MaxBudgetInTeam.IsUnknown() {
		updateReq["max_budget_in_team"] = data.MaxBudgetInTeam.ValueFloat64()
	}
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() {
		updateReq["budget_duration"] = data.BudgetDuration.ValueString()
	}
	return updateReq
}

func (r *TeamMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TeamMemberResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Refuse implicit adoption. This preflight distinguishes an account that
	// existed before Create from a membership that appears only after an
	// ambiguous/partially successful add request.
	observed := data
	preexisting, err := r.readTeamMember(ctx, &observed)
	if err != nil {
		resp.Diagnostics.AddError("Team Member Preflight Error", fmt.Sprintf("Unable to verify that the team member does not already exist: %s", err))
		return
	}
	if preexisting {
		resp.Diagnostics.AddError("Team Member Already Exists", "The user is already in this team. Import the membership before managing it with Terraform.")
		return
	}

	memberReq := buildTeamMemberAddRequest(&data)
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_add", memberReq, nil); err != nil {
		// If the exact roster entry appeared after a preflight that proved it was
		// absent, recover a partially successful/ambiguous add through the native
		// update endpoint. Budget rows alone are not membership proof.
		postAdd := data
		exists, verifyErr := r.readTeamMember(ctx, &postAdd)
		if verifyErr != nil || !exists {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add team member: %s", err))
			return
		}
		if updateErr := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_update", buildTeamMemberUpdateRequest(&data), nil); updateErr != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Team member appeared after add failed but could not be reconciled: %s", updateErr))
			return
		}
	}

	data.ID = types.StringValue(fmt.Sprintf("%s:%s", data.TeamID.ValueString(), data.UserID.ValueString()))

	exists, readErr := r.readTeamMember(ctx, &data)
	if readErr != nil {
		resp.Diagnostics.AddError("Team Member Read-Back Error", fmt.Sprintf("The membership may have been created, but it could not be verified: %s", readErr))
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Team Member Missing After Create", "LiteLLM accepted the create request but the user is not present in the team's member roster.")
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamMemberResource) readTeamMember(ctx context.Context, data *TeamMemberResourceModel) (bool, error) {
	endpoint := fmt.Sprintf("/team/info?team_id=%s", url.QueryEscape(data.TeamID.ValueString()))
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return false, err
	}

	rosterFound := false
	teamInfo := result
	if nested, ok := result["team_info"].(map[string]interface{}); ok {
		teamInfo = nested
	}
	if members, ok := teamInfo["members_with_roles"].([]interface{}); ok {
		for _, rawMember := range members {
			member, ok := rawMember.(map[string]interface{})
			if !ok || member["user_id"] != data.UserID.ValueString() {
				continue
			}
			rosterFound = true
			if role, ok := member["role"].(string); ok && role != "" {
				data.Role = types.StringValue(role)
			}
			if email, ok := member["user_email"].(string); ok && email != "" {
				data.UserEmail = types.StringValue(email)
			}
			break
		}
	}

	var budget map[string]interface{}
	if memberships, ok := result["team_memberships"].([]interface{}); ok {
		for _, rawMembership := range memberships {
			membership, ok := rawMembership.(map[string]interface{})
			if !ok || membership["user_id"] != data.UserID.ValueString() {
				continue
			}
			budget, _ = membership["litellm_budget_table"].(map[string]interface{})
			break
		}
	}
	if !rosterFound {
		return false, nil
	}

	if !data.MaxBudgetInTeam.IsNull() || data.MaxBudgetInTeam.IsUnknown() {
		if value, ok := budget["max_budget"].(float64); ok {
			data.MaxBudgetInTeam = types.Float64Value(value)
		} else {
			data.MaxBudgetInTeam = types.Float64Null()
		}
	}
	if !data.BudgetDuration.IsNull() || data.BudgetDuration.IsUnknown() {
		if value, ok := budget["budget_duration"].(string); ok && value != "" {
			data.BudgetDuration = types.StringValue(value)
		} else {
			data.BudgetDuration = types.StringNull()
		}
	}

	return true, nil
}

func (r *TeamMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TeamMemberResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	exists, err := r.readTeamMember(ctx, &data)
	if err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read team member: %s", err))
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data TeamMemberResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state TeamMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = state.ID

	updateReq := buildTeamMemberUpdateRequest(&data)

	applyTeamMemberNullableClears(updateReq, &state, &data)

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_update", updateReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update team member: %s", err))
		return
	}

	exists, readErr := r.readTeamMember(ctx, &data)
	if readErr != nil {
		resp.Diagnostics.AddError("Team Member Read-Back Error", fmt.Sprintf("The membership was updated but could not be verified: %s", readErr))
		return
	}
	if !exists {
		resp.Diagnostics.AddError("Team Member Missing After Update", "LiteLLM accepted the update request but the user is not present in the team's member roster.")
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TeamMemberResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := map[string]interface{}{
		"user_id":    data.UserID.ValueString(),
		"user_email": data.UserEmail.ValueString(),
		"team_id":    data.TeamID.ValueString(),
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete team member: %s", err))
			return
		}
	}
}

func (r *TeamMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import ID format: team_id:user_id
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError("Invalid Import ID", "Import ID must be in format team_id:user_id")
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), parts[1])...)
}

// applyTeamMemberNullableClears mutates updateReq to send explicit JSON null for
// nullable fields that transition from set (non-null in state) to cleared (null in
// plan). See applyTeamNullableClears in resource_team.go for the rationale.
func applyTeamMemberNullableClears(updateReq map[string]interface{}, state, plan *TeamMemberResourceModel) {
	if !state.MaxBudgetInTeam.IsNull() && plan.MaxBudgetInTeam.IsNull() {
		updateReq["max_budget_in_team"] = nil
	}
	if !state.BudgetDuration.IsNull() && plan.BudgetDuration.IsNull() {
		updateReq["budget_duration"] = nil
	}
}
