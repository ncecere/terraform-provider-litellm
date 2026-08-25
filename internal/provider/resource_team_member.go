package provider

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
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
var _ resource.ResourceWithConfigValidators = &TeamMemberResource{}
var _ resource.ResourceWithImportState = &TeamMemberResource{}

const (
	teamMemberImportPrefix        = "v1."
	teamMemberUncertainPrivateKey = "team_member_accepted_uncertain_v1"
)

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

type teamMemberAddAPIResponse struct {
	TeamID                 string                    `json:"team_id"`
	MembersWithRoles       []teamMemberRosterAPI     `json:"members_with_roles"`
	UpdatedUsers           []teamMemberUserAPI       `json:"updated_users"`
	UpdatedTeamMemberships []teamMemberMembershipAPI `json:"updated_team_memberships"`
}

type teamMemberUserAPI struct {
	UserID    string  `json:"user_id"`
	UserEmail *string `json:"user_email"`
}

type teamMemberRosterAPI struct {
	UserID    *string `json:"user_id"`
	UserEmail *string `json:"user_email"`
	Role      string  `json:"role"`
}

type teamMemberMembershipAPI struct {
	UserID string `json:"user_id"`
	TeamID string `json:"team_id"`
}

type teamMemberUpdateAPIResponse struct {
	UserID string `json:"user_id"`
	TeamID string `json:"team_id"`
}

type teamMemberRemoteStatus int

const (
	teamMemberRemoteAbsent teamMemberRemoteStatus = iota
	teamMemberRemoteComplete
	teamMemberRemoteMembershipOnly
	teamMemberRemoteRosterOnly
)

type teamMemberObservation struct {
	Status          teamMemberRemoteStatus
	CanonicalUserID string
	Roster          *remoteBatchMember
	Membership      *remoteBatchMembership
}

func (r *TeamMemberResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_member"
}

func (r *TeamMemberResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.AtLeastOneOf(
			path.MatchRoot("user_id"),
			path.MatchRoot("user_email"),
		),
	}
}

func (r *TeamMemberResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a single LiteLLM team member by an immutable canonical user identity.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Stable canonical composite ID (team_id:user_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"team_id": schema.StringAttribute{
				Description: "Team ID. Changing it replaces the membership.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_id": schema.StringAttribute{
				Description: "Canonical user ID. Either user_id or user_email must be provided. Email-only creates store LiteLLM's resolved canonical ID in state. Changing a configured canonical ID replaces the membership.",
				Optional:    true,
				Computed:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_email": schema.StringAttribute{
				Description: "User email used to resolve identity. Either user_id or user_email must be provided. Matching is case-insensitive; safe configured spelling is preserved in state. This resource does not manage the user's account email.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"role": schema.StringAttribute{
				Description: "Role in the team (admin or user).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("admin", "user"),
				},
			},
			"max_budget_in_team": schema.Float64Attribute{
				Description: "Maximum budget for this member in the team. Removing a configured value sends an explicit JSON null.",
				Optional:    true,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Recurring reset interval for this member's budget. Accepts positive s, m, h, d, or w durations; hourly, daily, weekly, or monthly; or exactly 1mo. Removing a configured value sends an explicit JSON null.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						budgetDurationPattern,
						budgetDurationValidationMessage,
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

func teamMemberConfiguredIdentity(data *TeamMemberResourceModel) (string, string, error) {
	userID := ""
	if !data.UserID.IsNull() && !data.UserID.IsUnknown() {
		userID = data.UserID.ValueString()
	}
	userEmail := ""
	if !data.UserEmail.IsNull() && !data.UserEmail.IsUnknown() {
		userEmail = data.UserEmail.ValueString()
	}
	if userID == "" && userEmail == "" {
		return "", "", fmt.Errorf("either user_id or user_email must be a known, non-empty value")
	}
	return userID, userEmail, nil
}

func teamMemberStateIdentity(data *TeamMemberResourceModel) (string, string, error) {
	if data.TeamID.IsNull() || data.TeamID.IsUnknown() || data.TeamID.ValueString() == "" {
		return "", "", fmt.Errorf("stored team_id is missing")
	}
	if data.UserID.IsNull() || data.UserID.IsUnknown() || data.UserID.ValueString() == "" {
		return "", "", fmt.Errorf("stored canonical user_id is missing")
	}
	return data.TeamID.ValueString(), data.UserID.ValueString(), nil
}

func buildTeamMemberAddRequest(data *TeamMemberResourceModel) (map[string]interface{}, error) {
	userID, userEmail, err := teamMemberConfiguredIdentity(data)
	if err != nil {
		return nil, err
	}
	member := map[string]interface{}{"role": data.Role.ValueString()}
	if userID != "" {
		member["user_id"] = userID
	}
	if userEmail != "" {
		member["user_email"] = userEmail
	}
	request := map[string]interface{}{
		"member":  []map[string]interface{}{member},
		"team_id": data.TeamID.ValueString(),
	}
	if !data.MaxBudgetInTeam.IsNull() && !data.MaxBudgetInTeam.IsUnknown() {
		request["max_budget_in_team"] = data.MaxBudgetInTeam.ValueFloat64()
	}
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() {
		request["budget_duration"] = data.BudgetDuration.ValueString()
	}
	return request, nil
}

// buildTeamMemberUpdateRequest deliberately takes the stored identity separately
// from mutable planned values. Update, delete, and partial recovery never route a
// request through a newly planned identity.
func buildTeamMemberUpdateRequest(state, plan *TeamMemberResourceModel) (map[string]interface{}, error) {
	teamID, userID, err := teamMemberStateIdentity(state)
	if err != nil {
		return nil, err
	}
	if plan.Role.IsNull() || plan.Role.IsUnknown() || plan.Role.ValueString() == "" {
		return nil, fmt.Errorf("planned role must be known")
	}
	request := map[string]interface{}{
		"user_id": userID,
		"team_id": teamID,
		"role":    plan.Role.ValueString(),
	}
	// v1.98 updates a referenced historical budget row in place. An unchanged
	// budget field must therefore be omitted, not replayed alongside a role edit.
	// Explicit null is reserved for a real configured-value clear.
	if !teamMemberValuesEqualFloat(state.MaxBudgetInTeam, plan.MaxBudgetInTeam) {
		if !plan.MaxBudgetInTeam.IsNull() && !plan.MaxBudgetInTeam.IsUnknown() {
			request["max_budget_in_team"] = plan.MaxBudgetInTeam.ValueFloat64()
		}
	}
	if !teamMemberValuesEqualString(state.BudgetDuration, plan.BudgetDuration) {
		if !plan.BudgetDuration.IsNull() && !plan.BudgetDuration.IsUnknown() {
			request["budget_duration"] = plan.BudgetDuration.ValueString()
		}
	}
	applyTeamMemberNullableClears(request, state, plan)
	return request, nil
}

func buildTeamMemberDeleteRequest(state *TeamMemberResourceModel) (map[string]interface{}, error) {
	teamID, userID, err := teamMemberStateIdentity(state)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{"team_id": teamID, "user_id": userID}, nil
}

func (r *TeamMemberResource) readTeamMemberSnapshot(ctx context.Context, teamID string) (*teamMemberAddSnapshot, error) {
	return (&TeamMemberAddResource{client: r.client}).readTeamMemberAddSnapshot(ctx, teamID)
}

func (r *TeamMemberResource) waitForTeamMemberSnapshot(ctx context.Context, teamID string, predicate func(*teamMemberAddSnapshot) (bool, error)) (*teamMemberAddSnapshot, error) {
	return (&TeamMemberAddResource{client: r.client}).waitForTeamMemberAddSnapshot(ctx, teamID, predicate)
}

func teamMemberEmailMatches(email, candidate string) bool {
	return strings.EqualFold(email, candidate)
}

func findTeamMemberRosterByID(roster []remoteBatchMember, userID string) (int, error) {
	match := -1
	for index, member := range roster {
		if member.UserID != userID {
			continue
		}
		if match >= 0 {
			return -1, fmt.Errorf("/team/info returned duplicate members_with_roles entries for the stored canonical user")
		}
		match = index
	}
	return match, nil
}

func findTeamMemberRosterByEmail(roster []remoteBatchMember, email string) (int, error) {
	match := -1
	for index, member := range roster {
		if !teamMemberEmailMatches(email, member.UserEmail) {
			continue
		}
		if match >= 0 {
			return -1, fmt.Errorf("/team/info returned multiple members_with_roles entries for the configured email identity")
		}
		match = index
	}
	return match, nil
}

func teamMemberBudgetExpected(snapshot *teamMemberAddSnapshot, data *TeamMemberResourceModel) bool {
	if snapshot != nil && snapshot.TeamBudgetID != "" {
		return true
	}
	return (!data.MaxBudgetInTeam.IsNull() && !data.MaxBudgetInTeam.IsUnknown()) ||
		(!data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown())
}

func observeTeamMember(snapshot *teamMemberAddSnapshot, data *TeamMemberResourceModel) (teamMemberObservation, error) {
	if snapshot == nil {
		return teamMemberObservation{}, fmt.Errorf("team snapshot is unavailable")
	}
	userID := ""
	if !data.UserID.IsNull() && !data.UserID.IsUnknown() {
		userID = data.UserID.ValueString()
	}
	userEmail := ""
	if !data.UserEmail.IsNull() && !data.UserEmail.IsUnknown() {
		userEmail = data.UserEmail.ValueString()
	}
	if userID == "" && userEmail == "" {
		return teamMemberObservation{}, fmt.Errorf("stored member identity is missing")
	}

	rosterIndex := -1
	var err error
	if userID != "" {
		rosterIndex, err = findTeamMemberRosterByID(snapshot.Members, userID)
		if err != nil {
			return teamMemberObservation{}, err
		}
		if userEmail != "" {
			emailIndex, emailErr := findTeamMemberRosterByEmail(snapshot.Members, userEmail)
			if emailErr != nil {
				return teamMemberObservation{}, emailErr
			}
			if emailIndex >= 0 && emailIndex != rosterIndex {
				return teamMemberObservation{}, fmt.Errorf("stored user_id and user_email identify different team roster entries")
			}
			if rosterIndex >= 0 && emailIndex != rosterIndex {
				return teamMemberObservation{}, fmt.Errorf("configured user_email no longer case-insensitively matches the stored canonical user's team roster email")
			}
		}
	} else {
		rosterIndex, err = findTeamMemberRosterByEmail(snapshot.Members, userEmail)
		if err != nil {
			return teamMemberObservation{}, err
		}
		if rosterIndex >= 0 {
			userID = snapshot.Members[rosterIndex].UserID
			if userID == "" {
				return teamMemberObservation{}, fmt.Errorf("email-resolved roster entry is missing canonical user_id")
			}
		}
	}

	membershipIndex := -1
	if userID != "" {
		membershipIndex, err = matchBatchMembership(batchMember{UserID: userID, HasUserID: true}, snapshot.Memberships)
		if err != nil {
			return teamMemberObservation{}, err
		}
	}
	observation := teamMemberObservation{CanonicalUserID: userID}
	if rosterIndex >= 0 {
		roster := snapshot.Members[rosterIndex]
		observation.Roster = &roster
	}
	if membershipIndex >= 0 {
		membership := snapshot.Memberships[membershipIndex]
		observation.Membership = &membership
	}
	switch {
	case rosterIndex >= 0 && membershipIndex >= 0:
		observation.Status = teamMemberRemoteComplete
	case rosterIndex >= 0 && !teamMemberBudgetExpected(snapshot, data):
		// LiteLLM v1.98 intentionally stores a budgetless member only in
		// members_with_roles when neither the request nor team metadata selects a
		// member budget. In that endpoint shape, roster-only is complete state.
		observation.Status = teamMemberRemoteComplete
	case rosterIndex >= 0:
		observation.Status = teamMemberRemoteRosterOnly
	case membershipIndex >= 0:
		observation.Status = teamMemberRemoteMembershipOnly
	default:
		observation.Status = teamMemberRemoteAbsent
	}
	return observation, nil
}

func applyTeamMemberObservation(data *TeamMemberResourceModel, observation teamMemberObservation) error {
	if observation.CanonicalUserID == "" {
		return fmt.Errorf("authoritative member identity is missing canonical user_id")
	}
	data.UserID = types.StringValue(observation.CanonicalUserID)
	data.ID = types.StringValue(fmt.Sprintf("%s:%s", data.TeamID.ValueString(), observation.CanonicalUserID))
	if observation.Roster != nil {
		data.Role = types.StringValue(observation.Roster.Role)
		if data.UserEmail.IsUnknown() && observation.Roster.UserEmail != "" {
			data.UserEmail = types.StringValue(observation.Roster.UserEmail)
		}
	} else {
		// A v1.98 membership-only row proves the mutation identity but cannot
		// prove that the later roster role write happened.
		data.Role = types.StringUnknown()
	}

	if !data.MaxBudgetInTeam.IsNull() || data.MaxBudgetInTeam.IsUnknown() {
		switch {
		case observation.Membership == nil && observation.Status == teamMemberRemoteRosterOnly:
			data.MaxBudgetInTeam = types.Float64Unknown()
		case observation.Membership == nil || observation.Membership.MaxBudget == nil:
			data.MaxBudgetInTeam = types.Float64Null()
		default:
			data.MaxBudgetInTeam = types.Float64Value(*observation.Membership.MaxBudget)
		}
	}
	if !data.BudgetDuration.IsNull() || data.BudgetDuration.IsUnknown() {
		switch {
		case observation.Membership == nil && observation.Status == teamMemberRemoteRosterOnly:
			data.BudgetDuration = types.StringUnknown()
		case observation.Membership == nil:
			data.BudgetDuration = types.StringNull()
		case !observation.Membership.BudgetDurationKnown:
			return fmt.Errorf("team membership budget relation omits budget_duration")
		case observation.Membership.BudgetDuration == nil:
			data.BudgetDuration = types.StringNull()
		default:
			data.BudgetDuration = types.StringValue(*observation.Membership.BudgetDuration)
		}
	}
	return nil
}

func teamMemberObservationMatchesPlan(observation teamMemberObservation, plan *TeamMemberResourceModel, manageMaxBudget, manageBudgetDuration bool) bool {
	if observation.Status != teamMemberRemoteComplete || observation.Roster == nil {
		return false
	}
	if plan.Role.IsNull() || plan.Role.IsUnknown() || observation.Roster.Role != plan.Role.ValueString() {
		return false
	}
	if manageMaxBudget {
		switch {
		case plan.MaxBudgetInTeam.IsUnknown():
			return false
		case plan.MaxBudgetInTeam.IsNull():
			if observation.Membership != nil && observation.Membership.MaxBudget != nil {
				return false
			}
		default:
			if observation.Membership == nil || observation.Membership.MaxBudget == nil || *observation.Membership.MaxBudget != plan.MaxBudgetInTeam.ValueFloat64() {
				return false
			}
		}
	}
	if manageBudgetDuration {
		switch {
		case plan.BudgetDuration.IsUnknown():
			return false
		case plan.BudgetDuration.IsNull():
			if observation.Membership != nil && (!observation.Membership.BudgetDurationKnown || observation.Membership.BudgetDuration != nil) {
				return false
			}
		default:
			if observation.Membership == nil || !observation.Membership.BudgetDurationKnown || observation.Membership.BudgetDuration == nil || *observation.Membership.BudgetDuration != plan.BudgetDuration.ValueString() {
				return false
			}
		}
	}
	return true
}

func teamMemberValuesEqualFloat(left, right types.Float64) bool {
	if left.IsUnknown() || right.IsUnknown() {
		return false
	}
	if left.IsNull() || right.IsNull() {
		return left.IsNull() && right.IsNull()
	}
	return left.ValueFloat64() == right.ValueFloat64()
}

func teamMemberValuesEqualString(left, right types.String) bool {
	if left.IsUnknown() || right.IsUnknown() {
		return false
	}
	if left.IsNull() || right.IsNull() {
		return left.IsNull() && right.IsNull()
	}
	return left.ValueString() == right.ValueString()
}

func teamMemberOwnedReconciliationBase(state, plan *TeamMemberResourceModel, manageMaxBudget, manageBudgetDuration bool) TeamMemberResourceModel {
	result := *state
	if manageMaxBudget {
		result.MaxBudgetInTeam = plan.MaxBudgetInTeam
	}
	if manageBudgetDuration {
		result.BudgetDuration = plan.BudgetDuration
	}
	return result
}

func validateTeamMemberBudgetMutation(snapshot *teamMemberAddSnapshot, userID string) error {
	membershipIndex, err := matchBatchMembership(batchMember{UserID: userID, HasUserID: true}, snapshot.Memberships)
	if err != nil || membershipIndex < 0 {
		return err
	}
	budgetID := snapshot.Memberships[membershipIndex].BudgetID
	if budgetID == "" || budgetID == snapshot.TeamBudgetID {
		return nil
	}
	for index, membership := range snapshot.Memberships {
		if index != membershipIndex && membership.BudgetID == budgetID {
			return fmt.Errorf("the member's historical budget row is shared with another team membership; refusing a write that could mutate unowned budget state")
		}
	}
	return nil
}

func teamMemberDiagnosticError(err error) string {
	return teamMemberAddDiagnosticError(err)
}

func (r *TeamMemberResource) findCanonicalUsersByEmail(ctx context.Context, email string) ([]string, error) {
	users, err := listUsers(ctx, r.client, url.Values{"user_email": []string{email}})
	if err != nil {
		return nil, err
	}
	matches := make(map[string]struct{})
	for _, user := range users {
		if user.UserID.IsNull() || user.UserID.IsUnknown() || user.UserEmail.IsNull() || user.UserEmail.IsUnknown() {
			continue
		}
		if teamMemberEmailMatches(email, user.UserEmail.ValueString()) {
			matches[user.UserID.ValueString()] = struct{}{}
		}
	}
	result := make([]string, 0, len(matches))
	for userID := range matches {
		result = append(result, userID)
	}
	if len(result) > 1 {
		return nil, fmt.Errorf("the configured email resolves to multiple canonical users")
	}
	return result, nil
}

func (r *TeamMemberResource) lookupCanonicalUserByID(ctx context.Context, userID string) (string, bool, error) {
	endpoint := fmt.Sprintf("/v2/user/info?user_id=%s", url.QueryEscape(userID))
	var response teamMemberUserAPI
	if err := r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if response.UserID == "" || response.UserID != userID {
		return "", false, fmt.Errorf("user info response is missing or changed the requested canonical user_id")
	}
	email := ""
	if response.UserEmail != nil {
		email = *response.UserEmail
	}
	return email, true, nil
}

// resolveConfiguredTeamMemberIdentity proves the at-least-one identity contract
// without letting LiteLLM's ID/email precedence silently select another user.
// A zero canonical ID means an email-only identity names a user that does not yet
// exist and must be resolved from member_add's canonical response.
func (r *TeamMemberResource) resolveConfiguredTeamMemberIdentity(ctx context.Context, data *TeamMemberResourceModel) (string, error) {
	userID, userEmail, err := teamMemberConfiguredIdentity(data)
	if err != nil {
		return "", err
	}
	if userEmail == "" {
		return userID, nil
	}
	emailIDs, err := r.findCanonicalUsersByEmail(ctx, userEmail)
	if err != nil {
		return "", err
	}
	if userID == "" {
		if len(emailIDs) == 1 {
			return emailIDs[0], nil
		}
		return "", nil
	}
	if len(emailIDs) == 1 {
		if emailIDs[0] != userID {
			return "", fmt.Errorf("configured user_id and user_email resolve to different canonical users")
		}
		return userID, nil
	}
	observedEmail, exists, err := r.lookupCanonicalUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	if exists && observedEmail != "" && !teamMemberEmailMatches(userEmail, observedEmail) {
		return "", fmt.Errorf("configured user_id and user_email do not resolve to the same canonical user")
	}
	// When neither identity exists, LiteLLM creates one user with this explicit
	// ID/email pair. Existing ID-only users must have the same case-folded email.
	return userID, nil
}

func validateTeamMemberPlannedEmail(ctx context.Context, r *TeamMemberResource, state, plan *TeamMemberResourceModel) (types.String, error) {
	if plan.UserEmail.IsUnknown() {
		return state.UserEmail, nil
	}
	if plan.UserEmail.IsNull() {
		return types.StringNull(), nil
	}
	plannedEmail := plan.UserEmail.ValueString()
	if !state.UserEmail.IsNull() && !state.UserEmail.IsUnknown() && teamMemberEmailMatches(plannedEmail, state.UserEmail.ValueString()) {
		return plan.UserEmail, nil
	}
	matches, err := r.findCanonicalUsersByEmail(ctx, plannedEmail)
	if err != nil {
		return state.UserEmail, err
	}
	if len(matches) != 1 || matches[0] != state.UserID.ValueString() {
		return state.UserEmail, fmt.Errorf("planned user_email does not resolve to the stored canonical user_id; change user_id to plan replacement or use -replace")
	}
	return plan.UserEmail, nil
}

func preflightTeamMember(snapshot *teamMemberAddSnapshot, canonicalID, email string) error {
	target := batchMember{}
	if canonicalID != "" {
		target.UserID = canonicalID
		target.HasUserID = true
	}
	if email != "" {
		target.UserEmail = email
		target.HasUserEmail = true
	}
	rosterIndex, err := matchBatchMember(target, snapshot.Members)
	if err != nil {
		return err
	}
	if rosterIndex >= 0 {
		return fmt.Errorf("the configured identity is already present in the authoritative team roster")
	}
	if canonicalID != "" {
		membershipIndex, err := matchBatchMembership(target, snapshot.Memberships)
		if err != nil {
			return err
		}
		if membershipIndex >= 0 {
			return fmt.Errorf("the configured identity already has an unowned team_memberships row without a roster entry")
		}
		return nil
	}
	// v1.98 membership rows expose no email. An unresolved email-only create
	// cannot prove that any existing membership-only orphan is unrelated.
	rosterIDs := make(map[string]struct{}, len(snapshot.Members))
	for _, member := range snapshot.Members {
		if member.UserID != "" {
			rosterIDs[member.UserID] = struct{}{}
		}
	}
	for _, membership := range snapshot.Memberships {
		if _, rosterBacked := rosterIDs[membership.UserID]; !rosterBacked {
			return fmt.Errorf("an email-only identity cannot be safely added while an unowned membership-only row has no email correlation")
		}
	}
	return nil
}

func validateTeamMemberAddResponseStructure(response teamMemberAddAPIResponse, teamID string, membershipExpected bool) (string, error) {
	if response.TeamID == "" || response.TeamID != teamID {
		return "", fmt.Errorf("member_add response team_id is missing or inconsistent")
	}
	if len(response.UpdatedUsers) != 1 {
		return "", fmt.Errorf("member_add response must contain exactly one updated user")
	}
	userID := response.UpdatedUsers[0].UserID
	if userID == "" {
		return "", fmt.Errorf("member_add response canonical user identity is missing")
	}
	if membershipExpected && len(response.UpdatedTeamMemberships) != 1 {
		return userID, fmt.Errorf("member_add response must contain exactly one updated team membership when a requested or default member budget requires it")
	}
	if !membershipExpected && len(response.UpdatedTeamMemberships) > 1 {
		return userID, fmt.Errorf("member_add response contains multiple updated team memberships for one budgetless member")
	}
	if len(response.UpdatedTeamMemberships) == 1 {
		membership := response.UpdatedTeamMemberships[0]
		if membership.UserID != userID || membership.TeamID != teamID {
			return userID, fmt.Errorf("member_add response canonical user and membership identities are inconsistent")
		}
	}
	return userID, nil
}

func validateTeamMemberAddResponse(response teamMemberAddAPIResponse, data *TeamMemberResourceModel, membershipExpected bool) (string, error) {
	userID, err := validateTeamMemberAddResponseStructure(response, data.TeamID.ValueString(), membershipExpected)
	if err != nil {
		return userID, err
	}
	configuredID, configuredEmail, _ := teamMemberConfiguredIdentity(data)
	if configuredID != "" && configuredID != userID {
		return userID, fmt.Errorf("member_add response canonical user_id does not match the configured user_id")
	}
	// updated_users describes the account row, whose email this resource does
	// not manage. In particular, v1.98 may return null here for an ID-selected or
	// newly created user. Identity/email correlation comes from canonical
	// preflight plus the authoritative team roster pair below.
	roleMatches := 0
	for _, member := range response.MembersWithRoles {
		if member.UserID == nil || *member.UserID != userID {
			continue
		}
		roleMatches++
		if member.Role != data.Role.ValueString() {
			return userID, fmt.Errorf("member_add response role does not match the requested role")
		}
		if configuredEmail != "" && (member.UserEmail == nil || !teamMemberEmailMatches(configuredEmail, *member.UserEmail)) {
			return userID, fmt.Errorf("member_add response canonical roster ID/email pair does not match the configured identity")
		}
	}
	if roleMatches != 1 {
		return userID, fmt.Errorf("member_add response must contain exactly one canonical roster entry")
	}
	return userID, nil
}

func validateTeamMemberUpdateResponse(response teamMemberUpdateAPIResponse, teamID, userID string) error {
	if response.TeamID != teamID || response.UserID != userID {
		return fmt.Errorf("member_update response canonical membership identity is missing or inconsistent")
	}
	return nil
}

func recoverCreatedTeamMember(before, after *teamMemberAddSnapshot, planned *TeamMemberResourceModel, responseCanonical string) (string, teamMemberObservation, bool, error) {
	requested := batchMember{Role: planned.Role.ValueString(), RoleKnown: true}
	configuredID, configuredEmail, _ := teamMemberConfiguredIdentity(planned)
	if configuredID != "" {
		requested.UserID, requested.HasUserID = configuredID, true
	}
	if configuredEmail != "" {
		requested.UserEmail, requested.HasUserEmail = configuredEmail, true
	}
	canonical := responseCanonical
	if canonical == "" {
		recovered, owned, err := recoverAddedBatchMember(before, after, requested)
		if err != nil {
			return "", teamMemberObservation{}, false, err
		}
		if !owned {
			return "", teamMemberObservation{}, false, nil
		}
		if recovered.HasUserID {
			canonical = recovered.UserID
		}
	}
	if canonical == "" || after == nil {
		return canonical, teamMemberObservation{}, false, nil
	}
	probe := *planned
	probe.UserID = types.StringValue(canonical)
	observation, err := observeTeamMember(after, &probe)
	if err != nil {
		return canonical, teamMemberObservation{}, true, err
	}
	return canonical, observation, observation.Status != teamMemberRemoteAbsent, nil
}

func (r *TeamMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TeamMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A definitive, non-accepted create must never leave planned values looking
	// owned. Accepted operations repopulate only their recoverable identity below.
	resp.State.RemoveResource(ctx)
	if _, _, err := teamMemberConfiguredIdentity(&data); err != nil {
		resp.Diagnostics.AddError("Missing Team Member Identity", err.Error())
		return
	}

	canonicalBeforeAdd, err := r.resolveConfiguredTeamMemberIdentity(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Team Member Identity Error", teamMemberDiagnosticError(err))
		return
	}
	before, err := r.readTeamMemberSnapshot(ctx, data.TeamID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Team Member Preflight Error", fmt.Sprintf("Unable to verify the authoritative team state before adding the member: %s", teamMemberDiagnosticError(err)))
		return
	}
	_, configuredEmail, _ := teamMemberConfiguredIdentity(&data)
	if err := preflightTeamMember(before, canonicalBeforeAdd, configuredEmail); err != nil {
		resp.Diagnostics.AddError("Team Member Already Exists or Is Ambiguous", err.Error()+" Import a roster-backed membership before managing it; manually remediate a membership-only LiteLLM v1.98 row.")
		return
	}

	addRequest, err := buildTeamMemberAddRequest(&data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Member Identity", err.Error())
		return
	}
	var addResponse teamMemberAddAPIResponse
	accepted, addErr := r.client.doRequestWithResponse(ctx, http.MethodPost, "/team/member_add", addRequest, &addResponse)
	membershipExpected := teamMemberBudgetExpected(before, &data)
	responseCanonical := ""
	var responseValidationErr error
	if addErr == nil {
		responseCanonical, responseValidationErr = validateTeamMemberAddResponse(addResponse, &data, membershipExpected)
		if responseValidationErr != nil {
			if structuralCanonical, structuralErr := validateTeamMemberAddResponseStructure(addResponse, data.TeamID.ValueString(), membershipExpected); structuralErr == nil {
				responseCanonical = structuralCanonical
			}
		}
	}

	post, postErr := r.readTeamMemberSnapshot(ctx, data.TeamID.ValueString())
	recoveryCanonical := responseCanonical
	if accepted && recoveryCanonical == "" {
		// Canonical preflight proved this requested identity before the accepted
		// mutation. It is safe recovery identity even when the 2xx body is
		// malformed and a propagation-delayed read still shows the old roster.
		recoveryCanonical = canonicalBeforeAdd
	}
	canonical, observation, mutationVisible, recoveryErr := recoverCreatedTeamMember(before, post, &data, recoveryCanonical)
	if recoveryErr != nil && postErr == nil {
		postErr = recoveryErr
	}
	// An exact successful add response promises a complete roster-backed
	// membership. Retry bounded stale/partial reads until the configured role and
	// managed budgets are authoritative; a persistent partial is retained below.
	readRetryable := postErr != nil && isRetryableTeamMemberAddReadError(postErr)
	readNotConverged := postErr == nil && (!mutationVisible || !teamMemberObservationMatchesPlan(observation, &data, !data.MaxBudgetInTeam.IsNull(), !data.BudgetDuration.IsNull()))
	if accepted && addErr == nil && responseValidationErr == nil && (readRetryable || readNotConverged) {
		post, postErr = r.waitForTeamMemberSnapshot(ctx, data.TeamID.ValueString(), func(candidate *teamMemberAddSnapshot) (bool, error) {
			_, observed, visible, recoverErr := recoverCreatedTeamMember(before, candidate, &data, responseCanonical)
			if recoverErr != nil || !visible {
				return false, recoverErr
			}
			return teamMemberObservationMatchesPlan(observed, &data, !data.MaxBudgetInTeam.IsNull(), !data.BudgetDuration.IsNull()), nil
		})
		canonical, observation, mutationVisible, recoveryErr = recoverCreatedTeamMember(before, post, &data, recoveryCanonical)
		if recoveryErr != nil && postErr == nil {
			postErr = recoveryErr
		}
	}

	if !accepted {
		if mutationVisible {
			resp.Diagnostics.AddError("Ambiguous Team Member Creation", "The add request failed, but a matching roster or membership row appeared after preflight. The provider cannot prove who created it and did not adopt it. Verify ownership, then import the canonical membership.")
			return
		}
		resp.Diagnostics.AddError("Team Member Create Error", fmt.Sprintf("Unable to add the team member: %s", teamMemberDiagnosticError(addErr)))
		return
	}

	owned := data
	if canonical != "" {
		owned.UserID = types.StringValue(canonical)
		owned.ID = types.StringValue(fmt.Sprintf("%s:%s", owned.TeamID.ValueString(), canonical))
	} else {
		// Retain the only known recovery identity rather than inventing a composite
		// ID. A later authoritative roster can resolve an email-only accepted add.
		owned.UserID = types.StringNull()
		owned.ID = types.StringNull()
	}
	uncertain := !mutationVisible || observation.Status == teamMemberRemoteAbsent
	if uncertain {
		// A successful HTTP status proves an accepted ownership boundary, not any
		// requested mutable value. Keep only identity/email representation until a
		// later authoritative roster confirms role and budget state.
		owned.Role = types.StringUnknown()
		owned.MaxBudgetInTeam = types.Float64Unknown()
		owned.BudgetDuration = types.StringUnknown()
	} else if err := applyTeamMemberObservation(&owned, observation); err != nil && postErr == nil {
		postErr = err
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &owned)...)
	setCreateTeamMemberUncertain(ctx, resp, uncertain)

	if addErr != nil {
		resp.Diagnostics.AddError("Accepted Team Member Create Error", fmt.Sprintf("LiteLLM accepted the add and the provider retained its recoverable stored identity, but the response could not be safely processed: %s", teamMemberDiagnosticError(addErr)))
		return
	}
	if responseValidationErr != nil {
		resp.Diagnostics.AddError("Malformed Team Member Add Response", fmt.Sprintf("LiteLLM accepted the add and the provider retained the canonical mutation identity, but its response did not match the v1.98 contract: %s", teamMemberDiagnosticError(responseValidationErr)))
		return
	}
	if observation.Status == teamMemberRemoteMembershipOnly {
		resp.Diagnostics.AddError("Membership-Only Team Member Creation", teamMemberAddOrphanRemediation)
		return
	}
	if observation.Status == teamMemberRemoteRosterOnly {
		resp.Diagnostics.AddError("Roster-Only Team Member Creation", "LiteLLM accepted the add and wrote members_with_roles, but no canonical team_memberships row exists. The partial roster identity was retained; manually remediate the inconsistent v1.98 state before update.")
		return
	}
	if postErr != nil {
		resp.Diagnostics.AddError("Team Member Read-Back Error", fmt.Sprintf("The membership was created and retained in state, but its authoritative state could not be verified: %s", teamMemberDiagnosticError(postErr)))
		return
	}
	if !mutationVisible || observation.Status == teamMemberRemoteAbsent {
		resp.Diagnostics.AddError("Team Member Missing After Create", "LiteLLM accepted the add, but neither the canonical roster entry nor membership row was returned. The recoverable canonical identity was retained in state.")
		return
	}
	if !teamMemberObservationMatchesPlan(observation, &data, !data.MaxBudgetInTeam.IsNull(), !data.BudgetDuration.IsNull()) {
		resp.Diagnostics.AddError("Team Member Create Verification Failed", "The authoritative role or configured member budget does not match the requested value. The observed canonical membership was retained in state.")
	}
}

func (r *TeamMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TeamMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	uncertain := false
	if req.Private != nil {
		raw, privateDiags := req.Private.GetKey(ctx, teamMemberUncertainPrivateKey)
		resp.Diagnostics.Append(privateDiags...)
		if len(raw) != 0 && string(raw) != "1" {
			resp.Diagnostics.AddError("Invalid Private Team Member State", "The provider-private accepted-operation marker is malformed.")
		} else {
			uncertain = len(raw) != 0
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if data.TeamID.IsNull() || data.TeamID.IsUnknown() || data.TeamID.ValueString() == "" {
		resp.Diagnostics.AddError("Invalid Team Member State", "Stored team_id is missing; no remote request was sent.")
		return
	}

	snapshot, err := r.readTeamMemberSnapshot(ctx, data.TeamID.ValueString())
	if err != nil {
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Team Member Read Error", fmt.Sprintf("Unable to read the authoritative team state: %s", teamMemberDiagnosticError(err)))
		return
	}
	observation, err := observeTeamMember(snapshot, &data)
	if err != nil {
		resp.Diagnostics.AddError("Team Member Identity Error", err.Error())
		return
	}
	if observation.Status == teamMemberRemoteAbsent {
		if uncertain {
			// A stale 200 snapshot after an accepted create is not proof that the
			// operation failed. Retain only the recovery representation and keep the
			// private marker until a later authoritative row appears.
			data.Role = types.StringUnknown()
			data.MaxBudgetInTeam = types.Float64Unknown()
			data.BudgetDuration = types.StringUnknown()
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			setReadTeamMemberUncertain(ctx, resp, true)
			resp.Diagnostics.AddError("Accepted Team Member Is Not Yet Visible", "LiteLLM previously accepted this member operation, but the authoritative roster is still propagation-delayed. Ownership and canonical recovery identity were retained; role and budget values remain unconfirmed.")
			return
		}
		resp.State.RemoveResource(ctx)
		return
	}
	if err := applyTeamMemberObservation(&data, observation); err != nil {
		resp.Diagnostics.AddError("Team Member Response Error", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	setReadTeamMemberUncertain(ctx, resp, false)
	if observation.Status == teamMemberRemoteMembershipOnly {
		resp.Diagnostics.AddError("Membership-Only Team Member State", teamMemberAddOrphanRemediation)
	}
	if observation.Status == teamMemberRemoteRosterOnly {
		resp.Diagnostics.AddError("Roster-Only Team Member State", "LiteLLM returned the owned members_with_roles entry without its canonical team_memberships row. State was retained; manually remediate this partial v1.98 condition before update.")
	}
}

func (r *TeamMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state TeamMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if req.Private != nil {
		raw, privateDiags := req.Private.GetKey(ctx, teamMemberUncertainPrivateKey)
		resp.Diagnostics.Append(privateDiags...)
		if len(raw) != 0 {
			resp.State = req.State
			setUpdateTeamMemberUncertain(ctx, resp, true)
			resp.Diagnostics.AddError("Unconfirmed Accepted Team Member", "A prior accepted operation has not yet appeared in the authoritative roster. Refresh must converge before another mutation can be sent.")
			return
		}
	}
	teamID, userID, err := teamMemberStateIdentity(&state)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Member State", err.Error())
		return
	}
	if plan.TeamID.IsUnknown() || plan.UserID.IsUnknown() {
		resp.Diagnostics.AddError("Unknown Team Member Update Identity", "team_id and the canonical user_id must be known before update. Apply the identity-producing dependency first; no mutation was sent.")
		return
	}
	if plan.TeamID.IsNull() || plan.TeamID.ValueString() != teamID || plan.UserID.IsNull() || plan.UserID.ValueString() != userID {
		resp.Diagnostics.AddError("Team Member Replacement Required", "The planned team_id or canonical user_id differs from stored state. Terraform must replace this membership; no in-place mutation was sent.")
		return
	}
	plannedEmail, err := validateTeamMemberPlannedEmail(ctx, r, &state, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Team Member Email Identity Error", teamMemberDiagnosticError(err))
		return
	}
	if plan.MaxBudgetInTeam.IsUnknown() || plan.BudgetDuration.IsUnknown() {
		resp.Diagnostics.AddError("Unknown Team Member Budget", "Member budget values must be known before update; no mutation was sent.")
		return
	}

	before, err := r.readTeamMemberSnapshot(ctx, teamID)
	if err != nil {
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Team Member Update Preflight Error", fmt.Sprintf("Unable to read the authoritative membership before update: %s", teamMemberDiagnosticError(err)))
		return
	}
	observationIdentity := state
	observationIdentity.UserEmail = plannedEmail
	observation, err := observeTeamMember(before, &observationIdentity)
	if err != nil {
		resp.Diagnostics.AddError("Team Member Update Identity Error", err.Error())
		return
	}
	switch observation.Status {
	case teamMemberRemoteAbsent:
		resp.State.RemoveResource(ctx)
		resp.Diagnostics.AddError("Team Member Missing Before Update", "The stored canonical membership no longer exists; no mutation was sent.")
		return
	case teamMemberRemoteMembershipOnly:
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Membership-Only Team Member State", teamMemberAddOrphanRemediation)
		return
	case teamMemberRemoteRosterOnly:
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Roster-Only Team Member State", "The canonical team_memberships row is missing. The provider retained state and did not send v1.98 member_update.")
		return
	}
	manageMaxBudget := !state.MaxBudgetInTeam.IsNull() || !plan.MaxBudgetInTeam.IsNull()
	manageBudgetDuration := !state.BudgetDuration.IsNull() || !plan.BudgetDuration.IsNull()
	// A safe email spelling/alias change is state presentation, not membership
	// identity and not a user-account email update. Avoid a no-op remote mutation
	// when all authoritative mutable fields already match.
	if teamMemberObservationMatchesPlan(observation, &plan, manageMaxBudget, manageBudgetDuration) {
		observedState := teamMemberOwnedReconciliationBase(&state, &plan, manageMaxBudget, manageBudgetDuration)
		if err := applyTeamMemberObservation(&observedState, observation); err != nil {
			resp.Diagnostics.AddError("Team Member Update Response Error", err.Error())
			return
		}
		observedState.UserEmail = plannedEmail
		resp.Diagnostics.Append(resp.State.Set(ctx, &observedState)...)
		return
	}
	updateRequest, err := buildTeamMemberUpdateRequest(&state, &plan)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Member Update", err.Error())
		return
	}
	_, sendsMaxBudget := updateRequest["max_budget_in_team"]
	_, sendsBudgetDuration := updateRequest["budget_duration"]
	if sendsMaxBudget || sendsBudgetDuration {
		if err := validateTeamMemberBudgetMutation(before, userID); err != nil {
			resp.Diagnostics.AddError("Unsafe Team Member Budget Update", err.Error())
			return
		}
	}
	var updateResponse teamMemberUpdateAPIResponse
	accepted, updateErr := r.client.doRequestWithResponse(ctx, http.MethodPost, "/team/member_update", updateRequest, &updateResponse)
	if updateErr == nil {
		updateErr = validateTeamMemberUpdateResponse(updateResponse, teamID, userID)
	}

	var after *teamMemberAddSnapshot
	var readErr error
	if accepted {
		after, readErr = r.waitForTeamMemberSnapshot(ctx, teamID, func(candidate *teamMemberAddSnapshot) (bool, error) {
			// Planned budget ownership determines whether a post-update membership
			// row is required. Clearing the last budget on a no-default team may
			// legitimately return to the healthy roster-only v1.98 shape.
			candidateState := plan
			candidateState.UserEmail = plannedEmail
			observed, observeErr := observeTeamMember(candidate, &candidateState)
			if observeErr != nil {
				return false, observeErr
			}
			return teamMemberObservationMatchesPlan(observed, &plan, manageMaxBudget, manageBudgetDuration), nil
		})
	} else {
		after, readErr = r.readTeamMemberSnapshot(ctx, teamID)
	}
	if readErr != nil && IsAPIErrorStatus(readErr, http.StatusNotFound) {
		resp.State.RemoveResource(ctx)
	} else if after != nil {
		recovered := state
		observed, observeErr := observeTeamMember(after, &recovered)
		if observeErr == nil {
			switch observed.Status {
			case teamMemberRemoteAbsent:
				resp.State.RemoveResource(ctx)
			case teamMemberRemoteComplete, teamMemberRemoteRosterOnly, teamMemberRemoteMembershipOnly:
				if applyErr := applyTeamMemberObservation(&recovered, observed); applyErr == nil {
					if updateErr == nil && readErr == nil && observed.Status == teamMemberRemoteComplete && teamMemberObservationMatchesPlan(observed, &plan, manageMaxBudget, manageBudgetDuration) {
						recovered.UserEmail = plannedEmail
					}
					resp.Diagnostics.Append(resp.State.Set(ctx, &recovered)...)
				}
			}
		}
	}
	if updateErr != nil {
		detail := fmt.Sprintf("Unable to confirm the team member update: %s", teamMemberDiagnosticError(updateErr))
		if accepted {
			detail = "LiteLLM accepted the update, but its exact v1.98 response or authoritative read-back could not be confirmed. " + detail
		}
		if readErr != nil && !IsAPIErrorStatus(readErr, http.StatusNotFound) {
			detail += ". State reconciliation also failed: " + teamMemberDiagnosticError(readErr)
		}
		resp.Diagnostics.AddError("Team Member Update Error", detail)
		return
	}
	if readErr != nil {
		resp.Diagnostics.AddError("Team Member Update Read-Back Error", fmt.Sprintf("LiteLLM accepted the update, but authoritative verification failed: %s", teamMemberDiagnosticError(readErr)))
		return
	}
	if after == nil {
		resp.Diagnostics.AddError("Team Member Update Verification Failed", "LiteLLM accepted the update, but no authoritative team snapshot was available. Prior ownership was retained.")
		return
	}
	finalState := teamMemberOwnedReconciliationBase(&state, &plan, manageMaxBudget, manageBudgetDuration)
	finalState.UserEmail = plannedEmail
	finalObservation, err := observeTeamMember(after, &finalState)
	if err != nil {
		resp.Diagnostics.AddError("Team Member Update Identity Error", err.Error())
		return
	}
	if finalObservation.Status == teamMemberRemoteMembershipOnly {
		_ = applyTeamMemberObservation(&finalState, finalObservation)
		resp.Diagnostics.Append(resp.State.Set(ctx, &finalState)...)
		resp.Diagnostics.AddError("Membership-Only Team Member Update", teamMemberAddOrphanRemediation)
		return
	}
	if finalObservation.Status != teamMemberRemoteComplete || !teamMemberObservationMatchesPlan(finalObservation, &plan, manageMaxBudget, manageBudgetDuration) {
		if finalObservation.Status != teamMemberRemoteAbsent {
			_ = applyTeamMemberObservation(&finalState, finalObservation)
			resp.Diagnostics.Append(resp.State.Set(ctx, &finalState)...)
		}
		resp.Diagnostics.AddError("Team Member Update Verification Failed", "The authoritative role or configured member budget does not match the requested value. Observed state was retained.")
		return
	}
	if err := applyTeamMemberObservation(&finalState, finalObservation); err != nil {
		resp.Diagnostics.AddError("Team Member Update Response Error", err.Error())
		return
	}
	finalState.UserEmail = plannedEmail
	resp.Diagnostics.Append(resp.State.Set(ctx, &finalState)...)
}

func setTeamMemberDeleteRetainedState(ctx context.Context, resp *resource.DeleteResponse, data *TeamMemberResourceModel, observation *teamMemberObservation) {
	retained := *data
	if observation != nil && observation.Status != teamMemberRemoteAbsent {
		_ = applyTeamMemberObservation(&retained, *observation)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &retained)...)
}

func (r *TeamMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state TeamMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if req.Private != nil {
		raw, privateDiags := req.Private.GetKey(ctx, teamMemberUncertainPrivateKey)
		resp.Diagnostics.Append(privateDiags...)
		if len(raw) != 0 {
			setTeamMemberDeleteRetainedState(ctx, resp, &state, nil)
			setDeleteTeamMemberUncertain(ctx, resp, true)
			resp.Diagnostics.AddError("Unconfirmed Accepted Team Member", "A prior accepted operation has not yet appeared in the authoritative roster. Refresh must converge before destroy can safely decide whether a member_delete request is required.")
			return
		}
	}
	teamID, userID, err := teamMemberStateIdentity(&state)
	if err != nil {
		setTeamMemberDeleteRetainedState(ctx, resp, &state, nil)
		resp.Diagnostics.AddError("Invalid Team Member State", err.Error())
		return
	}

	before, err := r.readTeamMemberSnapshot(ctx, teamID)
	if err != nil {
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			return
		}
		setTeamMemberDeleteRetainedState(ctx, resp, &state, nil)
		resp.Diagnostics.AddError("Team Member Destroy Preflight Error", fmt.Sprintf("Unable to read the authoritative membership before deletion: %s", teamMemberDiagnosticError(err)))
		return
	}
	observation, err := observeTeamMember(before, &state)
	if err != nil {
		setTeamMemberDeleteRetainedState(ctx, resp, &state, nil)
		resp.Diagnostics.AddError("Team Member Destroy Identity Error", err.Error())
		return
	}
	if observation.Status == teamMemberRemoteAbsent {
		return
	}
	if observation.Status == teamMemberRemoteMembershipOnly {
		setTeamMemberDeleteRetainedState(ctx, resp, &state, &observation)
		resp.Diagnostics.AddError("Membership-Only Team Member Destroy", teamMemberAddOrphanRemediation)
		return
	}
	deleteRequest, _ := buildTeamMemberDeleteRequest(&state)
	deleteErr := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/team/member_delete", deleteRequest, nil)

	after, readErr := r.readTeamMemberSnapshot(ctx, teamID)
	if IsAPIErrorStatus(readErr, http.StatusNotFound) {
		return
	}
	if readErr != nil {
		setTeamMemberDeleteRetainedState(ctx, resp, &state, nil)
		detail := "The delete result could not be verified: " + teamMemberDiagnosticError(readErr)
		if deleteErr != nil {
			detail = "The delete request failed and authoritative verification also failed: " + teamMemberDiagnosticError(deleteErr) + "; " + teamMemberDiagnosticError(readErr)
		}
		resp.Diagnostics.AddError("Team Member Destroy Error", detail)
		return
	}
	probe := state
	probe.UserID = types.StringValue(userID)
	afterObservation, observeErr := observeTeamMember(after, &probe)
	if observeErr != nil {
		setTeamMemberDeleteRetainedState(ctx, resp, &state, nil)
		resp.Diagnostics.AddError("Team Member Destroy Identity Error", observeErr.Error())
		return
	}
	if afterObservation.Status == teamMemberRemoteAbsent {
		return
	}
	setTeamMemberDeleteRetainedState(ctx, resp, &state, &afterObservation)
	if afterObservation.Status == teamMemberRemoteMembershipOnly {
		resp.Diagnostics.AddError("Partial Team Member Destroy", "LiteLLM removed members_with_roles but retained the canonical team_memberships row. "+teamMemberAddOrphanRemediation)
		return
	}
	if deleteErr != nil {
		resp.Diagnostics.AddError("Team Member Destroy Error", fmt.Sprintf("LiteLLM did not remove the stored canonical membership: %s", teamMemberDiagnosticError(deleteErr)))
		return
	}
	resp.Diagnostics.AddError("Team Member Destroy Verification Failed", "LiteLLM accepted member_delete, but the stored canonical roster or membership row remains. State was retained.")
}

func parseTeamMemberImportID(importID string) (string, string, error) {
	if importID == "" {
		return "", "", fmt.Errorf("import ID must not be empty")
	}
	if strings.HasPrefix(importID, teamMemberImportPrefix) {
		parts := strings.Split(importID, ".")
		if len(parts) == 3 && parts[0] == "v1" {
			teamID, teamErr := decodeTeamMemberImportComponent(parts[1])
			userID, userErr := decodeTeamMemberImportComponent(parts[2])
			if teamErr == nil && userErr == nil {
				return teamID, userID, nil
			}
		}
		// Historical team IDs beginning with v1. remain valid when the whole
		// import still uses the old colon grammar.
		if !strings.Contains(importID, ":") {
			return "", "", fmt.Errorf("invalid versioned import ID; expected v1.<base64url-team_id>.<base64url-user_id>")
		}
	}
	// Historical grammar splits at the first colon. It therefore supports
	// colons in user_id but cannot represent a colon in team_id.
	parts := strings.SplitN(importID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected a non-empty import ID in historical team_id:user_id form or versioned v1.<team>.<user> form")
	}
	return parts[0], parts[1], nil
}

func decodeTeamMemberImportComponent(encoded string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 || !utf8.Valid(decoded) || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return "", fmt.Errorf("invalid canonical unpadded base64url component")
	}
	return string(decoded), nil
}

func formatTeamMemberImportID(teamID, userID string) (string, error) {
	if teamID == "" || userID == "" {
		return "", fmt.Errorf("team_id and user_id must not be empty")
	}
	return teamMemberImportPrefix + base64.RawURLEncoding.EncodeToString([]byte(teamID)) + "." + base64.RawURLEncoding.EncodeToString([]byte(userID)), nil
}

func (r *TeamMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	teamID, userID, err := parseTeamMemberImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Import ID", err.Error())
		return
	}
	stableID := fmt.Sprintf("%s:%s", teamID, userID)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), stableID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team_id"), teamID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), userID)...)
}

// applyTeamMemberNullableClears sends explicit JSON null for nullable fields
// moving from a known configured value to null. LiteLLM's Pydantic update path
// otherwise ignores omitted fields. This preserves #112's native duration
// behavior and the historical max-budget clear contract.
func applyTeamMemberNullableClears(updateReq map[string]interface{}, state, plan *TeamMemberResourceModel) {
	if !state.MaxBudgetInTeam.IsNull() && !state.MaxBudgetInTeam.IsUnknown() && plan.MaxBudgetInTeam.IsNull() {
		updateReq["max_budget_in_team"] = nil
	}
	if !state.BudgetDuration.IsNull() && !state.BudgetDuration.IsUnknown() && plan.BudgetDuration.IsNull() {
		updateReq["budget_duration"] = nil
	}
}

func setCreateTeamMemberUncertain(ctx context.Context, resp *resource.CreateResponse, uncertain bool) {
	if resp.Private == nil {
		return
	}
	value := []byte(nil)
	if uncertain {
		value = []byte("1")
	}
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMemberUncertainPrivateKey, value)...)
}

func setReadTeamMemberUncertain(ctx context.Context, resp *resource.ReadResponse, uncertain bool) {
	if resp.Private == nil {
		return
	}
	value := []byte(nil)
	if uncertain {
		value = []byte("1")
	}
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMemberUncertainPrivateKey, value)...)
}

func setUpdateTeamMemberUncertain(ctx context.Context, resp *resource.UpdateResponse, uncertain bool) {
	if resp.Private == nil {
		return
	}
	value := []byte(nil)
	if uncertain {
		value = []byte("1")
	}
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMemberUncertainPrivateKey, value)...)
}

func setDeleteTeamMemberUncertain(ctx context.Context, resp *resource.DeleteResponse, uncertain bool) {
	if resp.Private == nil {
		return
	}
	value := []byte(nil)
	if uncertain {
		value = []byte("1")
	}
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMemberUncertainPrivateKey, value)...)
}
