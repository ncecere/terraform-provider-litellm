package provider

import (
	"context"
	"fmt"
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
				Description: "Role in the team (org_admin, internal_user, internal_user_viewer, admin, user).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("org_admin", "internal_user", "internal_user_viewer", "admin", "user"),
				},
			},
			"max_budget_in_team": schema.Float64Attribute{
				Description: "Maximum budget for this member in the team.",
				Optional:    true,
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

func (r *TeamMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TeamMemberResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

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

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_add", memberReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add team member: %s", err))
		return
	}

	data.ID = types.StringValue(fmt.Sprintf("%s:%s", data.TeamID.ValueString(), data.UserID.ValueString()))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TeamMemberResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// No specific endpoint to read a single team member
	// Maintain state as-is
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

	updateReq := map[string]interface{}{
		"user_id":    data.UserID.ValueString(),
		"user_email": data.UserEmail.ValueString(),
		"team_id":    data.TeamID.ValueString(),
	}

	if !data.MaxBudgetInTeam.IsNull() && !data.MaxBudgetInTeam.IsUnknown() {
		updateReq["max_budget_in_team"] = data.MaxBudgetInTeam.ValueFloat64()
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_update", updateReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update team member: %s", err))
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
