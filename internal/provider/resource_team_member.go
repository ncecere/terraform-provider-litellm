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
			"budget_duration": schema.StringAttribute{
				Description: "Budget reset interval for this member's in-team budget (e.g. \"30d\", \"24h\"). " +
					"The LiteLLM /team/member_add and /team/member_update endpoints do not accept a duration, " +
					"so when set, the provider resolves the member's budget object via /team/info and applies " +
					"the duration through /budget/update. Without this, max_budget_in_team accrues for the " +
					"lifetime of the membership and never resets.",
				Optional: true,
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
		if !isTeamMemberAlreadyInTeamError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add team member: %s", err))
			return
		}
	}

	if err := r.applyMemberBudgetDuration(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to apply member budget_duration: %s", err))
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

	applyTeamMemberNullableClears(updateReq, &state, &data)

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_update", updateReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update team member: %s", err))
		return
	}

	if err := r.applyMemberBudgetDuration(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to apply member budget_duration: %s", err))
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
}

// applyMemberBudgetDuration applies the configured budget_duration to the member's
// in-team budget object. The LiteLLM /team/member_add and /team/member_update endpoints
// accept only max_budget_in_team (no duration), so the per-member budget object LiteLLM
// creates has a null reset interval and accrues for the lifetime of the membership. To
// set a real reset interval we resolve the member's budget_id via /team/info and patch it
// through /budget/update. No-op when budget_duration is unset.
func (r *TeamMemberResource) applyMemberBudgetDuration(ctx context.Context, data *TeamMemberResourceModel) error {
	if data.BudgetDuration.IsNull() || data.BudgetDuration.IsUnknown() || data.BudgetDuration.ValueString() == "" {
		return nil
	}

	teamID := data.TeamID.ValueString()
	userID := data.UserID.ValueString()

	var teamInfo map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", fmt.Sprintf("/team/info?team_id=%s", teamID), nil, &teamInfo); err != nil {
		return fmt.Errorf("reading team info to resolve member budget_id: %w", err)
	}

	budgetID, ok := findMembershipBudgetID(teamInfo, userID)
	if !ok {
		return fmt.Errorf("could not resolve budget_id for user %q in team %q from /team/info; "+
			"set max_budget_in_team so LiteLLM creates a member budget object before applying budget_duration", userID, teamID)
	}

	budgetReq := map[string]interface{}{
		"budget_id":       budgetID,
		"budget_duration": data.BudgetDuration.ValueString(),
	}
	if !data.MaxBudgetInTeam.IsNull() && !data.MaxBudgetInTeam.IsUnknown() {
		budgetReq["max_budget"] = data.MaxBudgetInTeam.ValueFloat64()
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/budget/update", budgetReq, nil); err != nil {
		return fmt.Errorf("updating member budget %q duration: %w", budgetID, err)
	}

	return nil
}

// findMembershipBudgetID locates the budget object id for userID in a /team/info
// response. LiteLLM nests per-member budgets under the top-level "team_memberships"
// array, each entry carrying either a "litellm_budget_table" object (with "budget_id")
// or a flat "budget_id".
func findMembershipBudgetID(teamInfo map[string]interface{}, userID string) (string, bool) {
	memberships, ok := teamInfo["team_memberships"].([]interface{})
	if !ok {
		return "", false
	}
	for _, m := range memberships {
		membership, ok := m.(map[string]interface{})
		if !ok {
			continue
		}
		if uid, ok := membership["user_id"].(string); !ok || uid != userID {
			continue
		}
		if table, ok := membership["litellm_budget_table"].(map[string]interface{}); ok {
			if bid, ok := table["budget_id"].(string); ok && bid != "" {
				return bid, true
			}
		}
		if bid, ok := membership["budget_id"].(string); ok && bid != "" {
			return bid, true
		}
	}
	return "", false
}

func isTeamMemberAlreadyInTeamError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "status 400") && contains(errStr, "team_member_already_in_team")
}
