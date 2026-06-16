package provider

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
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
				Description: "Reset interval for this member's in-team budget (e.g. \"30d\", \"24h\"). " +
					"The /team/member_add and /team/member_update endpoints accept only max_budget_in_team " +
					"(no duration), so the provider patches the duration onto the member's budget object via " +
					"/budget/update; LiteLLM's reset job then resets the budget on that interval. Requires " +
					"max_budget_in_team to be set (that is the budget object the duration applies to).",
				Optional: true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^[1-9][0-9]*(s|m|h|d|w|mo)$`),
						`must be a positive duration like "30d", "24h", "60m" (units: s, m, h, d, w, mo)`,
					),
					stringvalidator.AlsoRequires(path.MatchRoot("max_budget_in_team")),
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

// applyMemberBudgetDuration applies (or clears) the member's in-team budget reset
// interval. /team/member_add and /team/member_update accept only max_budget_in_team and
// no duration, so the duration is patched onto the member's auto-created budget object
// via /budget/update. We do NOT set budget_reset_at: LiteLLM's reset job selects
// budget-table rows where (budget_reset_at IS NULL AND budget_duration IS NOT NULL) OR
// (budget_reset_at < now), so it resets a duration-only budget and computes the next
// reset itself. Idempotent: skips the call when the live duration already matches.
func (r *TeamMemberResource) applyMemberBudgetDuration(ctx context.Context, data *TeamMemberResourceModel) error {
	desired := ""
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() {
		desired = data.BudgetDuration.ValueString()
	}

	teamID := data.TeamID.ValueString()
	userID := data.UserID.ValueString()

	var teamInfo map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", fmt.Sprintf("/team/info?team_id=%s", url.QueryEscape(teamID)), nil, &teamInfo); err != nil {
		return fmt.Errorf("reading team info to resolve member budget_id: %w", err)
	}

	budgetID, curDuration, ok := findMembershipBudget(teamInfo, userID)
	if !ok {
		if desired == "" {
			// No member budget object and none required — nothing to do.
			return nil
		}
		return fmt.Errorf("could not resolve budget_id for user %q in team %q from /team/info; "+
			"set max_budget_in_team so LiteLLM creates a member budget object before applying budget_duration", userID, teamID)
	}

	// Already in the desired state (covers both "matches" and "both unset").
	if curDuration == desired {
		return nil
	}

	budgetReq := map[string]interface{}{"budget_id": budgetID}
	if desired == "" {
		budgetReq["budget_duration"] = nil // clear the reset interval
	} else {
		budgetReq["budget_duration"] = desired
	}
	if !data.MaxBudgetInTeam.IsNull() && !data.MaxBudgetInTeam.IsUnknown() {
		budgetReq["max_budget"] = data.MaxBudgetInTeam.ValueFloat64()
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/budget/update", budgetReq, nil); err != nil {
		return fmt.Errorf("updating member budget %q: %w", budgetID, err)
	}

	return nil
}

// findMembershipBudget locates the budget object id and current budget_duration for
// userID in a /team/info response. LiteLLM nests per-member budgets under the top-level
// "team_memberships" array, each entry carrying either a "litellm_budget_table" object
// (with "budget_id" / "budget_duration") or a flat "budget_id". Note: /team/info does not
// expose the budget's "budget_reset_at" (it serialises the membership budget through the
// non-Full budget type), so only budget_id and budget_duration are reliable here.
func findMembershipBudget(teamInfo map[string]interface{}, userID string) (budgetID string, duration string, ok bool) {
	memberships, mok := teamInfo["team_memberships"].([]interface{})
	if !mok {
		return "", "", false
	}
	for _, m := range memberships {
		membership, mok := m.(map[string]interface{})
		if !mok {
			continue
		}
		if uid, uok := membership["user_id"].(string); !uok || uid != userID {
			continue
		}
		if table, tok := membership["litellm_budget_table"].(map[string]interface{}); tok {
			if bid, _ := table["budget_id"].(string); bid != "" {
				dur, _ := table["budget_duration"].(string)
				return bid, dur, true
			}
		}
		if bid, bok := membership["budget_id"].(string); bok && bid != "" {
			dur, _ := membership["budget_duration"].(string)
			return bid, dur, true
		}
	}
	return "", "", false
}

func isTeamMemberAlreadyInTeamError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "status 400") && contains(errStr, "team_member_already_in_team")
}
