package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &TeamMemberAddResource{}
var _ resource.ResourceWithImportState = &TeamMemberAddResource{}
var _ resource.ResourceWithValidateConfig = &TeamMemberAddResource{}

const (
	teamMemberAddImportPrefix      = "v1."
	teamMemberAddPlainImportPrefix = "t~"
	teamMemberAddReadAttempts      = 5
	teamMemberAddReadInitialDelay  = 50 * time.Millisecond
	teamMemberAddReadMaximumDelay  = 400 * time.Millisecond
	teamMemberAddOrphanPrivateKey  = "team_member_add_owned_orphans_v1"
	teamMemberAddDeprecationNotice = "Prefer for_each with litellm_team_member for new configurations. This batch resource remains supported for compatibility and owns only the member blocks explicitly recorded in its state. LiteLLM v1.98 cannot repair or delete a membership-only orphan through its team-member API; that partial condition requires manual upstream remediation."
	teamMemberAddOrphanRemediation = "LiteLLM v1.98 returned an owned team_memberships row without a matching members_with_roles entry. In this partial condition, /team/member_delete first requires the missing roster entry. A direct user_id /team/member_update can return success and even mutate budget data, but it does not append the missing roster entry, so it is not a repair. The provider will not send either mutation after detecting the orphan and retained explicit Terraform ownership. Manually remove the inconsistent upstream membership row with LiteLLM administrator/support guidance (or upgrade to a version with corrected endpoints), then refresh. That refresh clears only the remediated orphan ownership, after which apply can recreate it or a pending removal/destroy can finish. Prefer for_each with litellm_team_member for new configurations."
)

func NewTeamMemberAddResource() resource.Resource {
	return &TeamMemberAddResource{}
}

type TeamMemberAddResource struct {
	client *Client
}

type TeamMemberAddResourceModel struct {
	ID              types.String  `tfsdk:"id"`
	TeamID          types.String  `tfsdk:"team_id"`
	Members         types.Set     `tfsdk:"member"`
	MaxBudgetInTeam types.Float64 `tfsdk:"max_budget_in_team"`
}

type MemberModel struct {
	UserID    types.String `tfsdk:"user_id"`
	UserEmail types.String `tfsdk:"user_email"`
	Role      types.String `tfsdk:"role"`
}

type batchMember struct {
	UserID       string
	UserEmail    string
	Role         string
	HasUserID    bool
	HasUserEmail bool
	RoleKnown    bool
}

type remoteBatchMember struct {
	UserID    string
	UserEmail string
	Role      string
}

type remoteBatchMembership struct {
	UserID    string
	BudgetID  string
	MaxBudget *float64
}

type teamMemberAddSnapshot struct {
	Members          []remoteBatchMember
	Memberships      []remoteBatchMembership
	TeamBudgetID     string
	MembershipsKnown bool
}

type observedBatch struct {
	Members     []batchMember
	RemoteIndex map[string]int
	Orphans     map[string]int
	Budget      types.Float64
}

type batchBudgetUpdate struct {
	Member      batchMember
	RemoteIndex int
	AffectedIDs []string
}

type teamMemberAddPartialResponseError struct {
	detail    string
	retryable bool
}

type teamMemberAddOrphanMarkers map[string]struct{}

func (e *teamMemberAddPartialResponseError) Error() string {
	return "LiteLLM returned a partial /team/info response: " + e.detail
}

func decodeTeamMemberAddOrphanMarkers(raw []byte) (teamMemberAddOrphanMarkers, error) {
	markers := teamMemberAddOrphanMarkers{}
	if len(raw) == 0 {
		return markers, nil
	}
	var keys []string
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil, fmt.Errorf("the provider-private membership-only ownership marker is malformed")
	}
	for _, key := range keys {
		encoded := strings.TrimPrefix(key, "i:")
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		if !strings.HasPrefix(key, "i:") || len(decoded) == 0 || err != nil || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
			return nil, fmt.Errorf("the provider-private membership-only ownership marker contains an invalid canonical identity")
		}
		markers[key] = struct{}{}
	}
	return markers, nil
}

func encodeTeamMemberAddOrphanMarkers(markers teamMemberAddOrphanMarkers) []byte {
	if len(markers) == 0 {
		return nil
	}
	keys := make([]string, 0, len(markers))
	for key := range markers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	encoded, _ := json.Marshal(keys)
	return encoded
}

func teamMemberAddOrphanMarkerForUserID(userID string) string {
	if userID == "" {
		return ""
	}
	return "i:" + base64.RawURLEncoding.EncodeToString([]byte(userID))
}

func teamMemberAddOrphanMarkerForMember(member batchMember) string {
	if !member.HasUserID {
		return ""
	}
	return teamMemberAddOrphanMarkerForUserID(member.UserID)
}

func addTeamMemberAddOrphanMarker(markers teamMemberAddOrphanMarkers, member batchMember) {
	if key := teamMemberAddOrphanMarkerForMember(member); key != "" {
		markers[key] = struct{}{}
	}
}

func removeBatchMembersByOrphanMarker(members []batchMember, removed teamMemberAddOrphanMarkers) []batchMember {
	if len(removed) == 0 {
		return append([]batchMember(nil), members...)
	}
	result := make([]batchMember, 0, len(members))
	for _, member := range members {
		if _, ok := removed[teamMemberAddOrphanMarkerForMember(member)]; !ok {
			result = append(result, member)
		}
	}
	return result
}

func (r *TeamMemberAddResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_member_add"
}

func (r *TeamMemberAddResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description:        "Manages an explicitly owned batch of LiteLLM team members.",
		DeprecationMessage: teamMemberAddDeprecationNotice,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Resource ID (team_id). Existing team-id state remains valid.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"team_id": schema.StringAttribute{
				Description: "Team ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"max_budget_in_team": schema.Float64Attribute{
				Description: "Maximum budget applied to every member owned by this batch. When omitted, member budgets are unmanaged and remote budget drift is not read into state.",
				Optional:    true,
			},
		},
		Blocks: map[string]schema.Block{
			"member": schema.SetNestedBlock{
				Description: "One or more explicitly owned team members.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
				},
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"user_id": schema.StringAttribute{
							Description: "User ID. At least one of user_id or user_email must be non-empty. For email-only ownership, the provider stores LiteLLM's canonical user ID after it resolves the roster entry.",
							Optional:    true,
							Computed:    true,
						},
						"user_email": schema.StringAttribute{
							Description: "User email. At least one of user_id or user_email must be non-empty.",
							Optional:    true,
						},
						"role": schema.StringAttribute{
							Description: "Role (admin, user).",
							Required:    true,
							Validators: []validator.String{
								stringvalidator.OneOf("admin", "user"),
							},
						},
					},
				},
			},
		},
	}
}

func (r *TeamMemberAddResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data TeamMemberAddResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() || data.Members.IsUnknown() {
		return
	}
	if data.Members.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("member"), "Missing Batch Members", "At least one member block is required.")
		return
	}

	members, parseDiags := batchMembersFromSet(data.Members, false)
	resp.Diagnostics.Append(parseDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateBatchMemberIdentities(members, true); err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("member"),
			"Invalid Batch Member Identity",
			err.Error(),
		)
	}
}

func (r *TeamMemberAddResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TeamMemberAddResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var planned TeamMemberAddResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &planned)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Create never starts with owned state. Only confirmed successful native
	// operations below are allowed to populate it.
	resp.State.RemoveResource(ctx)

	members, parseDiags := batchMembersFromSet(planned.Members, true)
	resp.Diagnostics.Append(parseDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateBatchMemberIdentities(members, true); err != nil {
		resp.Diagnostics.AddError("Invalid Batch Member Identity", err.Error())
		return
	}
	sortBatchMembers(members)

	teamID := planned.TeamID.ValueString()
	snapshot, err := r.readTeamMemberAddSnapshot(ctx, teamID)
	if err != nil {
		resp.Diagnostics.AddError("Team Member Preflight Error", fmt.Sprintf("Unable to verify the team roster before adding members: %s", teamMemberAddDiagnosticError(err)))
		return
	}
	if err := ensureMembersAbsent(snapshot, members); err != nil {
		resp.Diagnostics.AddError("Batch Member Already Exists", err.Error()+" Do not retry member_add. Import an existing roster-backed identity instead of adopting it; a membership-only v1.98 row requires manual upstream remediation because member_update and member_delete cannot repair or remove it.")
		return
	}

	confirmed := make([]batchMember, 0, len(members))
	orphanMarkers := teamMemberAddOrphanMarkers{}
	state := planned
	state.ID = types.StringValue(teamID)

	for _, member := range members {
		if presenceErr := ensureMembersAbsent(snapshot, []batchMember{member}); presenceErr != nil {
			resp.Diagnostics.AddError("Ambiguous Batch Member Identity", "A later batch identity resolved to a user or membership created by an earlier addition. The provider stopped before a duplicate mutation.")
			setCreatePartialState(ctx, resp, &state, confirmed, snapshot)
			return
		}
		beforeAdd := snapshot
		memberRequest := map[string]interface{}{
			"team_id": teamID,
			"member":  batchMemberRequest(member),
		}
		if !planned.MaxBudgetInTeam.IsNull() && !planned.MaxBudgetInTeam.IsUnknown() {
			memberRequest["max_budget_in_team"] = planned.MaxBudgetInTeam.ValueFloat64()
		}

		if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/team/member_add", memberRequest, nil); err != nil {
			postFailure, readErr := r.readTeamMemberAddSnapshot(ctx, teamID)
			if readErr != nil && (postFailure == nil || !postFailure.MembershipsKnown) && isRetryableTeamMemberAddReadError(readErr) {
				postFailure, _ = r.waitForTeamMemberAddSnapshot(ctx, teamID, func(candidate *teamMemberAddSnapshot) (bool, error) {
					return snapshotContainsBatchMember(candidate, member)
				})
			}
			recovered, owned, recoverErr := recoverAddedBatchMember(beforeAdd, postFailure, member)
			if recoverErr != nil {
				resp.Diagnostics.AddError("Ambiguous Batch Member Creation", "The add request failed and /team/info cannot uniquely reconcile the new team_memberships row. No unconfirmed identity was adopted.")
			} else if owned {
				confirmed = append(confirmed, recovered)
				if !recovered.RoleKnown {
					addTeamMemberAddOrphanMarker(orphanMarkers, recovered)
				}
				if recovered.RoleKnown {
					resp.Diagnostics.AddError("Partial Batch Member Creation", fmt.Sprintf("The add request failed after LiteLLM created the member's team_memberships row. The confirmed identity and roster role were retained for retry: %s", teamMemberAddDiagnosticError(err)))
				} else {
					resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only Partial Add", teamMemberAddOrphanRemediation+" Original add result: "+teamMemberAddDiagnosticError(err)+".")
				}
			} else if readErr != nil {
				resp.Diagnostics.AddError("Batch Member Create Error", fmt.Sprintf("Unable to add a team member, and post-failure ownership could not be confirmed: %s", teamMemberAddDiagnosticError(err)))
			} else {
				resp.Diagnostics.AddError("Batch Member Create Error", fmt.Sprintf("Unable to add a team member: %s", teamMemberAddDiagnosticError(err)))
			}
			setCreatePartialState(ctx, resp, &state, confirmed, postFailure)
			setCreateTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
			return
		}

		verified, waitErr := r.waitForTeamMemberAddSnapshot(ctx, teamID, func(candidate *teamMemberAddSnapshot) (bool, error) {
			index, matchErr := matchBatchMember(member, candidate.Members)
			return index >= 0, matchErr
		})
		if waitErr != nil {
			// HTTP success proves ownership, but only members_with_roles proves role.
			// Keep the configured identity while leaving the role unknown.
			recovered, recoveredFromMembership, _ := recoverAddedBatchMember(beforeAdd, verified, member)
			recoverySnapshot := verified
			if !recoveredFromMembership {
				recovered = member
				recovered.Role = ""
				recovered.RoleKnown = false
				recoverySnapshot = nil
			}
			confirmed = append(confirmed, recovered)
			if recoveredFromMembership && !recovered.RoleKnown {
				addTeamMemberAddOrphanMarker(orphanMarkers, recovered)
			}
			setCreatePartialState(ctx, resp, &state, confirmed, recoverySnapshot)
			setCreateTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
			if recoveredFromMembership && !recovered.RoleKnown {
				resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only Partial Add", teamMemberAddOrphanRemediation+" The add was accepted, but its roster write was not observed.")
			} else {
				resp.Diagnostics.AddError("Batch Member Read-Back Error", fmt.Sprintf("LiteLLM accepted a member add but the authoritative roster did not confirm it within five reads: %s", teamMemberAddDiagnosticError(waitErr)))
			}
			return
		}

		verifiedIndex, _ := matchBatchMember(member, verified.Members)
		if verifiedIndex >= 0 && verified.Members[verifiedIndex].UserID != "" {
			member.UserID = verified.Members[verifiedIndex].UserID
			member.HasUserID = true
		}
		confirmed = append(confirmed, member)
		snapshot = verified
		if err := ensureUniqueMatches(verified, confirmed); err != nil {
			setCreatePartialState(ctx, resp, &state, confirmed, verified)
			resp.Diagnostics.AddError("Ambiguous Batch Member Identity", err.Error())
			return
		}
	}

	finalSnapshot, err := r.readTeamMemberAddSnapshot(ctx, teamID)
	if err != nil {
		setCreatePartialState(ctx, resp, &state, confirmed, finalSnapshot)
		resp.Diagnostics.AddError("Batch Member Read-Back Error", fmt.Sprintf("The batch was added but its final state could not be read: %s", teamMemberAddDiagnosticError(err)))
		return
	}
	observed, err := observeBatch(finalSnapshot, members, planned.MaxBudgetInTeam)
	if err != nil {
		setCreatePartialState(ctx, resp, &state, confirmed, finalSnapshot)
		resp.Diagnostics.AddError("Batch Member Reconciliation Error", err.Error())
		return
	}
	if len(observed.Orphans) != 0 {
		for _, membershipIndex := range observed.Orphans {
			if membershipIndex >= 0 && membershipIndex < len(finalSnapshot.Memberships) {
				orphanMarkers[teamMemberAddOrphanMarkerForUserID(finalSnapshot.Memberships[membershipIndex].UserID)] = struct{}{}
			}
		}
		setCreatePartialState(ctx, resp, &state, observed.Members, finalSnapshot)
		setCreateTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
		resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only Partial Add", teamMemberAddOrphanRemediation)
		return
	}
	if len(observed.Members) != len(members) {
		setCreatePartialState(ctx, resp, &state, observed.Members, finalSnapshot)
		resp.Diagnostics.AddError("Batch Member Missing After Create", "LiteLLM accepted all add requests but /team/info did not return the complete owned membership. Confirmed ownership was retained without adopting unrelated members.")
		return
	}
	state.MaxBudgetInTeam = observed.Budget
	setDiags := setModelMembers(ctx, &state, observed.Members)
	resp.Diagnostics.Append(setDiags...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	setCreateTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
}

func (r *TeamMemberAddResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state TeamMemberAddResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	members, parseDiags := batchMembersFromSet(state.Members, false)
	resp.Diagnostics.Append(parseDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateBatchMemberIdentities(members, false); err != nil {
		resp.Diagnostics.AddError("Invalid Batch Member State", err.Error())
		return
	}
	orphanMarkers := teamMemberAddOrphanMarkers{}
	if req.Private != nil {
		rawMarkers, privateDiags := req.Private.GetKey(ctx, teamMemberAddOrphanPrivateKey)
		resp.Diagnostics.Append(privateDiags...)
		var markerErr error
		orphanMarkers, markerErr = decodeTeamMemberAddOrphanMarkers(rawMarkers)
		if markerErr != nil {
			resp.Diagnostics.AddError("Invalid Private Batch Ownership State", markerErr.Error())
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}

	teamID := batchTeamID(&state)
	if teamID == "" {
		resp.Diagnostics.AddError("Missing Team Identity", "The resource state contains neither team_id nor its historical team-id resource ID.")
		return
	}
	state.TeamID = types.StringValue(teamID)
	if state.ID.IsNull() || state.ID.IsUnknown() || state.ID.ValueString() == "" {
		state.ID = types.StringValue(teamID)
	}

	snapshot, err := r.readTeamMemberAddSnapshot(ctx, teamID)
	if err != nil {
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Team Roster Read Error", fmt.Sprintf("Unable to read the authoritative team roster: %s", teamMemberAddDiagnosticError(err)))
		return
	}

	activeOrphans, remediatedOrphans, markerErr := reconcileTeamMemberAddOrphanMarkers(snapshot, members, orphanMarkers)
	if markerErr != nil {
		resp.Diagnostics.AddError("Team Roster Reconciliation Error", markerErr.Error())
		return
	}
	retainedMembers := removeBatchMembersByOrphanMarker(members, remediatedOrphans)
	if ambiguityErr := legacyEmailOnlyMembershipAmbiguity(snapshot, retainedMembers); ambiguityErr != nil {
		resp.Diagnostics.Append(setModelMembers(ctx, &state, retainedMembers)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		setReadTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
		resp.Diagnostics.AddError("Ambiguous Legacy Email-Only Ownership", ambiguityErr.Error())
		return
	}
	if unresolved := unresolvedUnknownBatchMembers(snapshot, retainedMembers, activeOrphans); len(unresolved) != 0 {
		resp.Diagnostics.Append(setModelMembers(ctx, &state, retainedMembers)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		setReadTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
		resp.Diagnostics.AddError("Imported Batch Is Not Authoritative", "At least one member named by the composite import identity is absent from the authoritative members_with_roles roster. No unrelated team member or unmatched membership row was adopted; the imported ownership remains unchanged until the roster resolves it.")
		return
	}

	observed, err := observeBatch(snapshot, retainedMembers, state.MaxBudgetInTeam)
	if err != nil {
		resp.Diagnostics.AddError("Team Roster Reconciliation Error", err.Error())
		return
	}
	for _, membershipIndex := range observed.Orphans {
		marker := teamMemberAddOrphanMarkerForUserID(snapshot.Memberships[membershipIndex].UserID)
		if _, ownedOrphan := activeOrphans[marker]; !ownedOrphan {
			resp.Diagnostics.Append(setModelMembers(ctx, &state, retainedMembers)...)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			setReadTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
			resp.Diagnostics.AddError("Ambiguous Membership-Only Ownership", "A member retained in state has no authoritative roster entry and its membership-only row was not recorded as a provider-retained partial operation. The provider preserved prior ownership and will not add, update, delete, or report success. Resolve the roster or membership row with a LiteLLM administrator, then refresh.")
			return
		}
	}

	state.MaxBudgetInTeam = observed.Budget
	resp.Diagnostics.Append(setModelMembers(ctx, &state, observed.Members)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	setReadTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
	if len(observed.Orphans) != 0 {
		resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only State", teamMemberAddOrphanRemediation)
		return
	}
}

func (r *TeamMemberAddResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Any early or unrecoverable error must retain prior ownership rather than
	// the planned state pre-populated by Terraform.
	resp.State = req.State
	var planned TeamMemberAddResourceModel
	var prior TeamMemberAddResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &planned)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	desired, desiredDiags := batchMembersFromSet(planned.Members, true)
	ownedBefore, stateDiags := batchMembersFromSet(prior.Members, false)
	resp.Diagnostics.Append(desiredDiags...)
	resp.Diagnostics.Append(stateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateBatchMemberIdentities(desired, true); err != nil {
		resp.Diagnostics.AddError("Invalid Batch Member Identity", err.Error())
		return
	}
	if err := validateBatchMemberIdentities(ownedBefore, false); err != nil {
		resp.Diagnostics.AddError("Invalid Batch Member State", err.Error())
		return
	}
	orphanMarkers := teamMemberAddOrphanMarkers{}
	if req.Private != nil {
		rawMarkers, privateDiags := req.Private.GetKey(ctx, teamMemberAddOrphanPrivateKey)
		resp.Diagnostics.Append(privateDiags...)
		var markerErr error
		orphanMarkers, markerErr = decodeTeamMemberAddOrphanMarkers(rawMarkers)
		if markerErr != nil {
			resp.Diagnostics.AddError("Invalid Private Batch Ownership State", markerErr.Error())
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}
	sortBatchMembers(desired)

	teamID := batchTeamID(&prior)
	if teamID == "" {
		teamID = planned.TeamID.ValueString()
	}
	planned.TeamID = types.StringValue(teamID)
	planned.ID = prior.ID
	if planned.ID.IsNull() || planned.ID.IsUnknown() || planned.ID.ValueString() == "" {
		planned.ID = types.StringValue(teamID)
	}

	snapshot, err := r.readTeamMemberAddSnapshot(ctx, teamID)
	if err != nil {
		resp.Diagnostics.AddError("Team Member Update Preflight Error", fmt.Sprintf("Unable to read the authoritative team roster before update: %s", teamMemberAddDiagnosticError(err)))
		return
	}
	activeOrphans, remediatedOrphans, markerErr := reconcileTeamMemberAddOrphanMarkers(snapshot, ownedBefore, orphanMarkers)
	if markerErr != nil {
		resp.Diagnostics.AddError("Team Member Update Preflight Error", markerErr.Error())
		return
	}
	setUpdateTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
	ownedBefore = removeBatchMembersByOrphanMarker(ownedBefore, remediatedOrphans)
	if ambiguityErr := legacyEmailOnlyMembershipAmbiguity(snapshot, ownedBefore); ambiguityErr != nil {
		resp.Diagnostics.Append(setModelMembers(ctx, &prior, ownedBefore)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &prior)...)
		resp.Diagnostics.AddError("Ambiguous Legacy Email-Only Ownership", ambiguityErr.Error())
		return
	}
	if unresolved := unresolvedUnknownBatchMembers(snapshot, ownedBefore, activeOrphans); len(unresolved) != 0 {
		resp.Diagnostics.Append(setModelMembers(ctx, &prior, ownedBefore)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &prior)...)
		resp.Diagnostics.AddError("Imported Batch Is Not Authoritative", "A composite-import member still has no authoritative members_with_roles entry. The provider retained imported ownership and did not mutate the team.")
		return
	}
	current, err := observeBatch(snapshot, ownedBefore, types.Float64Null())
	if err != nil {
		resp.Diagnostics.AddError("Team Member Update Preflight Error", err.Error())
		return
	}
	if len(current.Orphans) != 0 {
		setUpdateRecoveredState(ctx, resp, &prior, current.Members, snapshot)
		for _, membershipIndex := range current.Orphans {
			marker := teamMemberAddOrphanMarkerForUserID(snapshot.Memberships[membershipIndex].UserID)
			if _, ownedOrphan := activeOrphans[marker]; !ownedOrphan {
				resp.Diagnostics.AddError("Ambiguous Membership-Only Ownership", "A state member has an unmatched membership row that was not recorded as a provider-retained partial operation. No repair or retry mutation was sent; resolve it with a LiteLLM administrator and refresh.")
				return
			}
		}
		resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only State", teamMemberAddOrphanRemediation)
		return
	}

	desiredMatches, additions, removals, err := classifyBatchUpdate(snapshot, current, desired)
	if err != nil {
		setUpdateRecoveredState(ctx, resp, &prior, current.Members, snapshot)
		resp.Diagnostics.AddError("Ambiguous Batch Member Update", err.Error())
		return
	}

	ownedNow := append([]batchMember(nil), current.Members...)
	for index := range ownedNow {
		remoteIndex, ok := current.RemoteIndex[batchMemberIdentityKey(ownedNow[index])]
		if ok && remoteIndex >= 0 && snapshot.Members[remoteIndex].UserID != "" {
			ownedNow[index].UserID = snapshot.Members[remoteIndex].UserID
			ownedNow[index].HasUserID = true
		}
	}
	sortBatchMembers(ownedNow)
	for _, member := range additions {
		beforeAdd := snapshot
		requestBody := map[string]interface{}{
			"team_id": teamID,
			"member":  batchMemberRequest(member),
		}
		if !planned.MaxBudgetInTeam.IsNull() && !planned.MaxBudgetInTeam.IsUnknown() {
			requestBody["max_budget_in_team"] = planned.MaxBudgetInTeam.ValueFloat64()
		}
		if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/team/member_add", requestBody, nil); err != nil {
			postFailure, readErr := r.readTeamMemberAddSnapshot(ctx, teamID)
			if readErr != nil && (postFailure == nil || !postFailure.MembershipsKnown) && isRetryableTeamMemberAddReadError(readErr) {
				postFailure, _ = r.waitForTeamMemberAddSnapshot(ctx, teamID, func(candidate *teamMemberAddSnapshot) (bool, error) {
					return snapshotContainsBatchMember(candidate, member)
				})
			}
			recovered, recoveredOwnership, recoverErr := recoverAddedBatchMember(beforeAdd, postFailure, member)
			if recoverErr == nil && recoveredOwnership {
				ownedNow = append(ownedNow, recovered)
				if !recovered.RoleKnown {
					addTeamMemberAddOrphanMarker(orphanMarkers, recovered)
				}
			}
			setUpdateRecoveredState(ctx, resp, &prior, ownedNow, postFailure)
			setUpdateTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
			if recoveredOwnership && !recovered.RoleKnown {
				resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only Partial Add", teamMemberAddOrphanRemediation+" No planned removals were attempted. Original add result: "+teamMemberAddDiagnosticError(err)+".")
			} else {
				resp.Diagnostics.AddError("Batch Member Add Error", fmt.Sprintf("Unable to add a destination member before removals; no planned removals were attempted: %s", teamMemberAddDiagnosticError(err)))
			}
			return
		}

		verified, waitErr := r.waitForTeamMemberAddSnapshot(ctx, teamID, func(candidate *teamMemberAddSnapshot) (bool, error) {
			index, matchErr := matchBatchMember(member, candidate.Members)
			return index >= 0, matchErr
		})
		if waitErr != nil {
			recovered, recoveredFromMembership, _ := recoverAddedBatchMember(beforeAdd, verified, member)
			recoverySnapshot := verified
			if !recoveredFromMembership {
				recovered = member
				recovered.Role = ""
				recovered.RoleKnown = false
				recoverySnapshot = nil
			}
			ownedNow = append(ownedNow, recovered)
			if recoveredFromMembership && !recovered.RoleKnown {
				addTeamMemberAddOrphanMarker(orphanMarkers, recovered)
			}
			setUpdateRecoveredState(ctx, resp, &prior, ownedNow, recoverySnapshot)
			setUpdateTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
			if recoveredFromMembership && !recovered.RoleKnown {
				resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only Partial Add", teamMemberAddOrphanRemediation+" No planned removals were attempted. The add was accepted, but its roster write was not observed.")
			} else {
				resp.Diagnostics.AddError("Batch Member Add Read-Back Error", fmt.Sprintf("LiteLLM accepted an addition before removals, but /team/info did not confirm its roster role within five reads: %s", teamMemberAddDiagnosticError(waitErr)))
			}
			return
		}
		verifiedIndex, _ := matchBatchMember(member, verified.Members)
		if verifiedIndex >= 0 && verified.Members[verifiedIndex].UserID != "" {
			member.UserID = verified.Members[verifiedIndex].UserID
			member.HasUserID = true
		}
		ownedNow = append(ownedNow, member)
		snapshot = verified
	}

	// Reclassify after additions. This catches an email-vs-ID alias that only
	// became observable after an earlier member was created.
	currentAfterAdds, observeErr := observeBatch(snapshot, ownedNow, types.Float64Null())
	if observeErr != nil {
		setUpdateRecoveredState(ctx, resp, &prior, ownedNow, snapshot)
		resp.Diagnostics.AddError("Batch Member Reconciliation Error", observeErr.Error())
		return
	}
	desiredMatches, _, removals, err = classifyBatchUpdate(snapshot, currentAfterAdds, desired)
	if err != nil {
		setUpdateRecoveredState(ctx, resp, &prior, ownedNow, snapshot)
		resp.Diagnostics.AddError("Ambiguous Batch Member Update", err.Error())
		return
	}

	// Omission is the historical unmanaged mode. Only a known configured value
	// performs budget comparison or sends a budget mutation; null never claims
	// that a remote budget is absent and never clears one.
	budgetManaged := !planned.MaxBudgetInTeam.IsNull() && !planned.MaxBudgetInTeam.IsUnknown()
	var budgetUpdates []batchBudgetUpdate
	if budgetManaged {
		budgetUpdates, err = planBatchBudgetUpdates(snapshot, ownedNow, desired, desiredMatches, planned.MaxBudgetInTeam.ValueFloat64())
		if err != nil {
			setUpdateRecoveredState(ctx, resp, &prior, ownedNow, snapshot)
			resp.Diagnostics.AddError("Unsafe Shared Team Member Budget Update", err.Error())
			return
		}
	}

	for _, budgetUpdate := range budgetUpdates {
		remote := snapshot.Members[budgetUpdate.RemoteIndex]
		updateBody := map[string]interface{}{
			"team_id":            teamID,
			"max_budget_in_team": planned.MaxBudgetInTeam.ValueFloat64(),
		}
		addRemoteMemberIdentity(updateBody, remote)
		if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/team/member_update", updateBody, nil); err != nil {
			postFailure, _ := r.readTeamMemberAddSnapshot(ctx, teamID)
			if postFailure == nil {
				postFailure = snapshot
			}
			setUpdateRecoveredState(ctx, resp, &prior, ownedNow, postFailure)
			resp.Diagnostics.AddError("Batch Member Budget Update Error", fmt.Sprintf("Unable to persist a safely grouped member budget through /team/member_update: %s", teamMemberAddDiagnosticError(err)))
			return
		}
		verified, waitErr := r.waitForTeamMemberAddSnapshot(ctx, teamID, func(candidate *teamMemberAddSnapshot) (bool, error) {
			return batchBudgetMatchesUsers(candidate, budgetUpdate.AffectedIDs, planned.MaxBudgetInTeam.ValueFloat64())
		})
		if waitErr != nil {
			setUpdateRecoveredState(ctx, resp, &prior, ownedNow, verified)
			resp.Diagnostics.AddError("Batch Member Budget Update Read-Back Error", fmt.Sprintf("LiteLLM accepted a grouped budget update, but /team/info did not confirm it within five reads: %s", teamMemberAddDiagnosticError(waitErr)))
			return
		}
		snapshot = verified
	}

	for _, member := range desired {
		remoteIndex, ok := desiredMatches[batchMemberIdentityKey(member)]
		if !ok {
			setUpdateRecoveredState(ctx, resp, &prior, ownedNow, snapshot)
			resp.Diagnostics.AddError("Batch Member Missing During Update", "A desired member disappeared from the authoritative roster before role reconciliation.")
			return
		}
		remote := snapshot.Members[remoteIndex]
		if remote.Role == member.Role {
			continue
		}
		updateBody := map[string]interface{}{"team_id": teamID, "role": member.Role}
		addRemoteMemberIdentity(updateBody, remote)
		if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/team/member_update", updateBody, nil); err != nil {
			postFailure, _ := r.readTeamMemberAddSnapshot(ctx, teamID)
			if postFailure == nil {
				postFailure = snapshot
			}
			setUpdateRecoveredState(ctx, resp, &prior, ownedNow, postFailure)
			resp.Diagnostics.AddError("Batch Member Role Update Error", fmt.Sprintf("Unable to persist a member role through /team/member_update: %s", teamMemberAddDiagnosticError(err)))
			return
		}
		verified, waitErr := r.waitForTeamMemberAddSnapshot(ctx, teamID, func(candidate *teamMemberAddSnapshot) (bool, error) {
			index, matchErr := matchBatchMember(member, candidate.Members)
			return index >= 0 && candidate.Members[index].Role == member.Role, matchErr
		})
		if waitErr != nil {
			setUpdateRecoveredState(ctx, resp, &prior, ownedNow, verified)
			resp.Diagnostics.AddError("Batch Member Role Update Read-Back Error", fmt.Sprintf("LiteLLM accepted a role update, but /team/info did not confirm it within five reads: %s", teamMemberAddDiagnosticError(waitErr)))
			return
		}
		snapshot = verified
	}

	for _, removal := range removals {
		remoteIndex, matchErr := matchBatchMember(removal, snapshot.Members)
		if matchErr != nil {
			setUpdateRecoveredState(ctx, resp, &prior, ownedNow, snapshot)
			resp.Diagnostics.AddError("Ambiguous Batch Member Removal", matchErr.Error())
			return
		}
		if remoteIndex < 0 {
			membershipIndex, membershipErr := matchBatchMembership(removal, snapshot.Memberships)
			if membershipErr != nil {
				setUpdateRecoveredState(ctx, resp, &prior, ownedNow, snapshot)
				resp.Diagnostics.AddError("Ambiguous Batch Member Removal", membershipErr.Error())
				return
			}
			if membershipIndex >= 0 {
				setUpdateRecoveredState(ctx, resp, &prior, ownedNow, snapshot)
				resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only State", teamMemberAddOrphanRemediation+" The planned removal remains owned.")
				return
			}
			ownedNow = removeBatchMember(ownedNow, removal)
			continue
		}
		deleteBody := map[string]interface{}{"team_id": teamID}
		remoteRemoval := snapshot.Members[remoteIndex]
		addRemoteMemberIdentity(deleteBody, remoteRemoval)
		verificationMember := removal
		if remoteRemoval.UserID != "" {
			verificationMember.UserID = remoteRemoval.UserID
			verificationMember.HasUserID = true
		}
		// Preserve the canonical roster user_id before deletion. v1.98 membership
		// rows omit email, so this is the only durable correlation if the endpoint
		// removes the roster first and then leaves the membership behind.
		ownedNow = replaceBatchMember(ownedNow, removal, verificationMember)
		if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/team/member_delete", deleteBody, nil); err != nil {
			postFailure, readErr := r.readTeamMemberAddSnapshot(ctx, teamID)
			if IsAPIErrorStatus(readErr, http.StatusNotFound) && IsAPIErrorStatus(err, http.StatusNotFound) {
				resp.State.RemoveResource(ctx)
				return
			}
			if readErr != nil {
				postFailure = snapshot
			}
			setUpdateRecoveredState(ctx, resp, &prior, ownedNow, postFailure)
			membershipOnly, membershipOnlyErr := snapshotHasMembershipOnlyBatchMember(postFailure, verificationMember)
			if membershipOnlyErr == nil && membershipOnly {
				addTeamMemberAddOrphanMarker(orphanMarkers, verificationMember)
				setUpdateTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
				resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only Partial Delete", teamMemberAddOrphanRemediation+" The planned removal remains owned. Original delete result: "+teamMemberAddDiagnosticError(err)+".")
				return
			}
			resp.Diagnostics.AddError("Batch Member Removal Error", fmt.Sprintf("LiteLLM rejected a member removal. The resource retained the confirmed remote roster for a safe retry: %s", teamMemberAddDiagnosticError(err)))
			return
		}

		verified, waitErr := r.waitForTeamMemberAddSnapshot(ctx, teamID, func(candidate *teamMemberAddSnapshot) (bool, error) {
			index, matchErr := matchBatchMember(verificationMember, candidate.Members)
			if matchErr != nil || index >= 0 {
				return false, matchErr
			}
			membershipIndex, membershipErr := matchBatchMembership(verificationMember, candidate.Memberships)
			return membershipIndex < 0, membershipErr
		})
		if waitErr != nil {
			if IsAPIErrorStatus(waitErr, http.StatusNotFound) {
				resp.State.RemoveResource(ctx)
				return
			}
			recoverySnapshot := verified
			if recoverySnapshot == nil {
				recoverySnapshot = snapshot
			}
			setUpdateRecoveredState(ctx, resp, &prior, ownedNow, recoverySnapshot)
			membershipOnly, membershipOnlyErr := snapshotHasMembershipOnlyBatchMember(verified, verificationMember)
			if membershipOnlyErr == nil && membershipOnly {
				addTeamMemberAddOrphanMarker(orphanMarkers, verificationMember)
				setUpdateTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
				resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only Partial Delete", teamMemberAddOrphanRemediation+" The planned removal remains owned.")
				return
			}
			resp.Diagnostics.AddError("Batch Member Removal Read-Back Error", fmt.Sprintf("LiteLLM accepted a removal, but /team/info did not confirm it within five reads: %s", teamMemberAddDiagnosticError(waitErr)))
			return
		}
		ownedNow = removeBatchMember(ownedNow, verificationMember)
		snapshot = verified
	}

	finalObserved, err := observeBatch(snapshot, desired, planned.MaxBudgetInTeam)
	if err != nil {
		setUpdateRecoveredState(ctx, resp, &prior, ownedNow, snapshot)
		resp.Diagnostics.AddError("Batch Member Final Reconciliation Error", err.Error())
		return
	}
	if len(finalObserved.Members) != len(desired) || !batchRolesEqual(finalObserved.Members, desired) {
		setUpdateRecoveredState(ctx, resp, &prior, ownedNow, snapshot)
		resp.Diagnostics.AddError("Batch Member Update Not Yet Consistent", "The authoritative roster does not match the planned owned membership and roles after update.")
		return
	}
	if budgetManaged && !finalObserved.Budget.Equal(planned.MaxBudgetInTeam) {
		setUpdateRecoveredState(ctx, resp, &prior, ownedNow, snapshot)
		resp.Diagnostics.AddError("Batch Member Budget Update Not Yet Consistent", "The authoritative member budgets do not match the planned shared batch budget after update.")
		return
	}

	planned.MaxBudgetInTeam = finalObserved.Budget
	resp.Diagnostics.Append(setModelMembers(ctx, &planned, finalObserved.Members)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &planned)...)
	setUpdateTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
}

func (r *TeamMemberAddResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State = req.State
	var state TeamMemberAddResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	owned, parseDiags := batchMembersFromSet(state.Members, false)
	resp.Diagnostics.Append(parseDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateBatchMemberIdentities(owned, false); err != nil {
		resp.Diagnostics.AddError("Invalid Batch Member State", err.Error())
		return
	}
	orphanMarkers := teamMemberAddOrphanMarkers{}
	if req.Private != nil {
		rawMarkers, privateDiags := req.Private.GetKey(ctx, teamMemberAddOrphanPrivateKey)
		resp.Diagnostics.Append(privateDiags...)
		var markerErr error
		orphanMarkers, markerErr = decodeTeamMemberAddOrphanMarkers(rawMarkers)
		if markerErr != nil {
			resp.Diagnostics.AddError("Invalid Private Batch Ownership State", markerErr.Error())
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}
	teamID := batchTeamID(&state)
	if teamID == "" {
		resp.Diagnostics.AddError("Missing Team Identity", "The resource state cannot identify the team for deletion.")
		return
	}

	snapshot, err := r.readTeamMemberAddSnapshot(ctx, teamID)
	if err != nil {
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			return
		}
		resp.Diagnostics.AddError("Batch Destroy Preflight Error", fmt.Sprintf("Unable to read the authoritative roster before deletion: %s", teamMemberAddDiagnosticError(err)))
		return
	}
	activeOrphans, remediatedOrphans, markerErr := reconcileTeamMemberAddOrphanMarkers(snapshot, owned, orphanMarkers)
	if markerErr != nil {
		resp.Diagnostics.AddError("Batch Destroy Preflight Error", markerErr.Error())
		return
	}
	setDeleteTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
	owned = removeBatchMembersByOrphanMarker(owned, remediatedOrphans)
	if ambiguityErr := legacyEmailOnlyMembershipAmbiguity(snapshot, owned); ambiguityErr != nil {
		resp.Diagnostics.Append(setModelMembers(ctx, &state, owned)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Ambiguous Legacy Email-Only Ownership", ambiguityErr.Error())
		return
	}
	if unresolved := unresolvedUnknownBatchMembers(snapshot, owned, activeOrphans); len(unresolved) != 0 {
		resp.Diagnostics.Append(setModelMembers(ctx, &state, owned)...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Imported Batch Is Not Authoritative", "A composite-import member still has no authoritative members_with_roles entry. Destroy retained state and did not report success.")
		return
	}
	current, err := observeBatch(snapshot, owned, types.Float64Null())
	if err != nil {
		resp.Diagnostics.AddError("Batch Destroy Preflight Error", err.Error())
		return
	}
	if len(current.Orphans) != 0 {
		setDeleteRecoveredState(ctx, resp, &state, current.Members, snapshot)
		for _, membershipIndex := range current.Orphans {
			marker := teamMemberAddOrphanMarkerForUserID(snapshot.Memberships[membershipIndex].UserID)
			if _, ownedOrphan := activeOrphans[marker]; !ownedOrphan {
				resp.Diagnostics.AddError("Ambiguous Membership-Only Ownership", "A state member has an unmatched membership row that was not recorded as a provider-retained partial operation. Destroy retained state and sent no mutation; resolve it with a LiteLLM administrator and refresh.")
				return
			}
		}
		resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only State", teamMemberAddOrphanRemediation+" Destroy cannot complete until the upstream partial state is manually remediated.")
		return
	}
	ownedNow := current.Members

	for _, member := range append([]batchMember(nil), ownedNow...) {
		remoteIndex, matchErr := matchBatchMember(member, snapshot.Members)
		if matchErr != nil {
			setDeleteRecoveredState(ctx, resp, &state, ownedNow, snapshot)
			resp.Diagnostics.AddError("Ambiguous Batch Member Destroy", matchErr.Error())
			return
		}
		membershipIndex, membershipErr := matchBatchMembership(member, snapshot.Memberships)
		if membershipErr != nil {
			setDeleteRecoveredState(ctx, resp, &state, ownedNow, snapshot)
			resp.Diagnostics.AddError("Ambiguous Batch Member Destroy", membershipErr.Error())
			return
		}
		if remoteIndex < 0 && membershipIndex < 0 {
			ownedNow = removeBatchMember(ownedNow, member)
			continue
		}

		if remoteIndex < 0 {
			setDeleteRecoveredState(ctx, resp, &state, ownedNow, snapshot)
			resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only State", teamMemberAddOrphanRemediation+" Destroy cannot complete until the upstream partial state is manually remediated.")
			return
		}
		deleteBody := map[string]interface{}{"team_id": teamID}
		verificationMember := member
		remoteMember := snapshot.Members[remoteIndex]
		addRemoteMemberIdentity(deleteBody, remoteMember)
		if remoteMember.UserID != "" {
			verificationMember.UserID = remoteMember.UserID
			verificationMember.HasUserID = true
		}
		ownedNow = replaceBatchMember(ownedNow, member, verificationMember)
		deleteErr := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/team/member_delete", deleteBody, nil)
		if deleteErr != nil {
			postFailure, readErr := r.readTeamMemberAddSnapshot(ctx, teamID)
			if IsAPIErrorStatus(deleteErr, http.StatusNotFound) && IsAPIErrorStatus(readErr, http.StatusNotFound) {
				return
			}
			if readErr != nil {
				postFailure = snapshot
			}
			setDeleteRecoveredState(ctx, resp, &state, ownedNow, postFailure)
			membershipOnly, membershipOnlyErr := snapshotHasMembershipOnlyBatchMember(postFailure, verificationMember)
			if membershipOnlyErr == nil && membershipOnly {
				addTeamMemberAddOrphanMarker(orphanMarkers, verificationMember)
				setDeleteTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
				resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only Partial Delete", teamMemberAddOrphanRemediation+" Destroy cannot complete until the upstream partial state is manually remediated. Original delete result: "+teamMemberAddDiagnosticError(deleteErr)+".")
				return
			}
			resp.Diagnostics.AddError("Batch Member Destroy Error", fmt.Sprintf("LiteLLM rejected a member deletion. Confirmed remote members remain in state for retry: %s", teamMemberAddDiagnosticError(deleteErr)))
			return
		}

		verified, waitErr := r.waitForTeamMemberAddSnapshot(ctx, teamID, func(candidate *teamMemberAddSnapshot) (bool, error) {
			index, candidateErr := matchBatchMember(verificationMember, candidate.Members)
			if candidateErr != nil || index >= 0 {
				return false, candidateErr
			}
			membershipIndex, membershipErr := matchBatchMembership(verificationMember, candidate.Memberships)
			return membershipIndex < 0, membershipErr
		})
		if waitErr != nil {
			if IsAPIErrorStatus(waitErr, http.StatusNotFound) {
				return
			}
			recoverySnapshot := verified
			if recoverySnapshot == nil {
				recoverySnapshot = snapshot
			}
			setDeleteRecoveredState(ctx, resp, &state, ownedNow, recoverySnapshot)
			membershipOnly, membershipOnlyErr := snapshotHasMembershipOnlyBatchMember(verified, verificationMember)
			if membershipOnlyErr == nil && membershipOnly {
				addTeamMemberAddOrphanMarker(orphanMarkers, verificationMember)
				setDeleteTeamMemberAddOrphanMarkers(ctx, resp, orphanMarkers)
				resp.Diagnostics.AddError("Unrepairable v1.98 Membership-Only Partial Delete", teamMemberAddOrphanRemediation+" Destroy cannot complete until the upstream partial state is manually remediated.")
				return
			}
			resp.Diagnostics.AddError("Batch Member Destroy Read-Back Error", fmt.Sprintf("LiteLLM accepted a deletion, but /team/info did not confirm it within five reads: %s", teamMemberAddDiagnosticError(waitErr)))
			return
		}
		ownedNow = removeBatchMember(ownedNow, verificationMember)
		snapshot = verified
	}
}

func (r *TeamMemberAddResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	teamID, members, legacy, err := parseTeamMemberAddImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Member Batch Import ID", err.Error())
		return
	}

	memberModels := make([]MemberModel, 0, len(members))
	for _, member := range members {
		model := MemberModel{
			UserID:    types.StringNull(),
			UserEmail: types.StringNull(),
			Role:      types.StringUnknown(),
		}
		if member.HasUserID {
			model.UserID = types.StringValue(member.UserID)
		}
		if member.HasUserEmail {
			model.UserEmail = types.StringValue(member.UserEmail)
		}
		memberModels = append(memberModels, model)
	}
	memberSet, setDiags := types.SetValueFrom(ctx, MemberObjectType(), memberModels)
	resp.Diagnostics.Append(setDiags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state := TeamMemberAddResourceModel{
		ID:              types.StringValue(teamID),
		TeamID:          types.StringValue(teamID),
		Members:         memberSet,
		MaxBudgetInTeam: types.Float64Unknown(),
	}
	if legacy {
		// Historical imports named only a team and therefore cannot identify any
		// member ownership. Preserve that grammar as an explicitly empty owned
		// roster rather than broadly adopting every member returned by /team/info.
		state.MaxBudgetInTeam = types.Float64Null()
		resp.Diagnostics.AddWarning(
			"Legacy Empty-Roster Import",
			"The historical team-id import form was accepted without adopting any remote members. Use the v1 composite import grammar to import an existing owned batch.",
		)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *TeamMemberAddResource) readTeamMemberAddSnapshot(ctx context.Context, teamID string) (*teamMemberAddSnapshot, error) {
	endpoint := fmt.Sprintf("/team/info?team_id=%s", url.QueryEscape(teamID))
	var raw json.RawMessage
	if err := r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &raw); err != nil {
		return nil, err
	}
	return decodeTeamMemberAddSnapshot(raw, teamID)
}

func decodeTeamMemberAddSnapshot(raw json.RawMessage, expectedTeamID string) (*teamMemberAddSnapshot, error) {
	var envelope struct {
		TeamID          *string         `json:"team_id"`
		TeamInfo        json.RawMessage `json:"team_info"`
		TeamMemberships json.RawMessage `json:"team_memberships"`
	}
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &envelope) != nil {
		return nil, &teamMemberAddPartialResponseError{detail: "the response is not a JSON object", retryable: true}
	}
	if envelope.TeamID == nil || *envelope.TeamID == "" || *envelope.TeamID != expectedTeamID {
		return nil, &teamMemberAddPartialResponseError{detail: "the echoed team_id is missing or does not match the requested team"}
	}

	snapshot := &teamMemberAddSnapshot{}
	if len(envelope.TeamMemberships) == 0 || string(envelope.TeamMemberships) == "null" {
		return snapshot, &teamMemberAddPartialResponseError{detail: "team_memberships is missing", retryable: true}
	}
	var rawMemberships []json.RawMessage
	if err := json.Unmarshal(envelope.TeamMemberships, &rawMemberships); err != nil {
		return snapshot, &teamMemberAddPartialResponseError{detail: "team_memberships is not an array", retryable: true}
	}
	memberships := make([]remoteBatchMembership, 0, len(rawMemberships))
	for _, rawMembership := range rawMemberships {
		var wire struct {
			UserID             *string         `json:"user_id"`
			TeamID             *string         `json:"team_id"`
			BudgetID           json.RawMessage `json:"budget_id"`
			LiteLLMBudgetTable json.RawMessage `json:"litellm_budget_table"`
		}
		if err := json.Unmarshal(rawMembership, &wire); err != nil {
			return snapshot, &teamMemberAddPartialResponseError{detail: "team_memberships contains a non-object row", retryable: true}
		}
		if wire.UserID == nil || *wire.UserID == "" || wire.TeamID == nil || *wire.TeamID != expectedTeamID {
			return snapshot, &teamMemberAddPartialResponseError{detail: "team_memberships contains a malformed identity or a row for another team"}
		}
		if len(wire.BudgetID) == 0 {
			return snapshot, &teamMemberAddPartialResponseError{detail: "a team membership row omits budget_id", retryable: true}
		}
		membership := remoteBatchMembership{UserID: *wire.UserID}
		if string(wire.BudgetID) != "null" {
			if err := json.Unmarshal(wire.BudgetID, &membership.BudgetID); err != nil || membership.BudgetID == "" {
				return snapshot, &teamMemberAddPartialResponseError{detail: "a team membership budget_id is malformed"}
			}
		}
		if len(wire.LiteLLMBudgetTable) == 0 {
			return snapshot, &teamMemberAddPartialResponseError{detail: "a team membership row omits litellm_budget_table", retryable: true}
		}
		if string(wire.LiteLLMBudgetTable) == "null" {
			if membership.BudgetID != "" {
				return snapshot, &teamMemberAddPartialResponseError{detail: "a team membership references a budget_id but its budget relation is null"}
			}
			memberships = append(memberships, membership)
			continue
		}
		var budget struct {
			BudgetID  json.RawMessage `json:"budget_id"`
			MaxBudget json.RawMessage `json:"max_budget"`
		}
		if err := json.Unmarshal(wire.LiteLLMBudgetTable, &budget); err != nil {
			return snapshot, &teamMemberAddPartialResponseError{detail: "a team membership budget relation is not an object", retryable: true}
		}
		if len(budget.BudgetID) == 0 || len(budget.MaxBudget) == 0 {
			return snapshot, &teamMemberAddPartialResponseError{detail: "a team membership budget object omits budget_id or max_budget", retryable: true}
		}
		var nestedBudgetID string
		if string(budget.BudgetID) == "null" || json.Unmarshal(budget.BudgetID, &nestedBudgetID) != nil || nestedBudgetID == "" || nestedBudgetID != membership.BudgetID {
			return snapshot, &teamMemberAddPartialResponseError{detail: "a team membership budget object has an inconsistent budget_id"}
		}
		if string(budget.MaxBudget) != "null" {
			var maxBudget float64
			if err := json.Unmarshal(budget.MaxBudget, &maxBudget); err != nil {
				return snapshot, &teamMemberAddPartialResponseError{detail: "a team membership max_budget is malformed"}
			}
			membership.MaxBudget = &maxBudget
		}
		memberships = append(memberships, membership)
	}
	snapshot.Memberships = memberships
	snapshot.MembershipsKnown = true

	if len(envelope.TeamInfo) == 0 || string(envelope.TeamInfo) == "null" {
		return snapshot, &teamMemberAddPartialResponseError{detail: "team_info is missing", retryable: true}
	}
	var info struct {
		TeamID           *string         `json:"team_id"`
		Metadata         json.RawMessage `json:"metadata"`
		MembersWithRoles json.RawMessage `json:"members_with_roles"`
	}
	if err := json.Unmarshal(envelope.TeamInfo, &info); err != nil {
		return snapshot, &teamMemberAddPartialResponseError{detail: "team_info is not a JSON object", retryable: true}
	}
	if info.TeamID == nil || *info.TeamID != expectedTeamID {
		return snapshot, &teamMemberAddPartialResponseError{detail: "team_info.team_id is missing or inconsistent"}
	}
	if len(info.Metadata) != 0 && string(info.Metadata) != "null" {
		var metadata map[string]json.RawMessage
		if err := json.Unmarshal(info.Metadata, &metadata); err != nil || metadata == nil {
			return snapshot, &teamMemberAddPartialResponseError{detail: "team_info.metadata is not a JSON object", retryable: true}
		}
		if rawBudgetID, exists := metadata["team_member_budget_id"]; exists && string(rawBudgetID) != "null" {
			if err := json.Unmarshal(rawBudgetID, &snapshot.TeamBudgetID); err != nil {
				return snapshot, &teamMemberAddPartialResponseError{detail: "team_info.metadata.team_member_budget_id is not a string", retryable: true}
			}
		}
	}
	if len(info.MembersWithRoles) == 0 || string(info.MembersWithRoles) == "null" {
		return snapshot, &teamMemberAddPartialResponseError{detail: "team_info.members_with_roles is missing", retryable: true}
	}

	var rawMembers []json.RawMessage
	if err := json.Unmarshal(info.MembersWithRoles, &rawMembers); err != nil {
		return snapshot, &teamMemberAddPartialResponseError{detail: "team_info.members_with_roles is not an array", retryable: true}
	}
	members := make([]remoteBatchMember, 0, len(rawMembers))
	for _, rawMember := range rawMembers {
		var wire struct {
			UserID    *string `json:"user_id"`
			UserEmail *string `json:"user_email"`
			Role      *string `json:"role"`
		}
		if err := json.Unmarshal(rawMember, &wire); err != nil {
			return snapshot, &teamMemberAddPartialResponseError{detail: "members_with_roles contains a non-object row", retryable: true}
		}
		if wire.Role == nil || (*wire.Role != "admin" && *wire.Role != "user") {
			return snapshot, &teamMemberAddPartialResponseError{detail: "members_with_roles contains a missing or unsupported role"}
		}
		member := remoteBatchMember{Role: *wire.Role}
		if wire.UserID != nil {
			member.UserID = *wire.UserID
		}
		if wire.UserEmail != nil {
			member.UserEmail = *wire.UserEmail
		}
		if member.UserID == "" && member.UserEmail == "" {
			return snapshot, &teamMemberAddPartialResponseError{detail: "members_with_roles contains an identity-less member"}
		}
		members = append(members, member)
	}
	snapshot.Members = members
	return snapshot, nil
}

func (r *TeamMemberAddResource) waitForTeamMemberAddSnapshot(ctx context.Context, teamID string, predicate func(*teamMemberAddSnapshot) (bool, error)) (*teamMemberAddSnapshot, error) {
	delay := teamMemberAddReadInitialDelay
	var lastSnapshot *teamMemberAddSnapshot
	var lastErr error
	for attempt := 0; attempt < teamMemberAddReadAttempts; attempt++ {
		candidate, err := r.readTeamMemberAddSnapshot(ctx, teamID)
		if candidate != nil {
			lastSnapshot = candidate
		}
		if err == nil {
			matched, predicateErr := predicate(candidate)
			if predicateErr != nil {
				return candidate, predicateErr
			}
			if matched {
				return candidate, nil
			}
			lastErr = nil
		} else {
			lastErr = err
			if ctx.Err() != nil || !isRetryableTeamMemberAddReadError(err) {
				return lastSnapshot, err
			}
		}
		if attempt == teamMemberAddReadAttempts-1 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return lastSnapshot, ctx.Err()
		case <-timer.C:
		}
		delay *= 2
		if delay > teamMemberAddReadMaximumDelay {
			delay = teamMemberAddReadMaximumDelay
		}
	}
	if lastErr != nil {
		return lastSnapshot, lastErr
	}
	return lastSnapshot, fmt.Errorf("the expected roster state was not observed after %d reads", teamMemberAddReadAttempts)
}

func isRetryableTeamMemberAddReadError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var partialErr *teamMemberAddPartialResponseError
	if errors.As(err, &partialErr) {
		return partialErr.retryable
	}
	var transportErr *safeTransportError
	if errors.As(err, &transportErr) {
		return transportErr.Retryable()
	}
	var responseErr *safeResponseError
	if errors.As(err, &responseErr) {
		if responseErr.statusCode >= http.StatusOK && responseErr.statusCode < http.StatusMultipleChoices {
			return responseErr.retryable
		}
		return responseErr.statusCode == http.StatusRequestTimeout || responseErr.statusCode == http.StatusTooManyRequests || responseErr.statusCode >= http.StatusInternalServerError
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusRequestTimeout || apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= http.StatusInternalServerError
}

// teamMemberAddDiagnosticError deliberately excludes LiteLLM bodies, request
// URLs, payloads, and transport causes. Local contract errors contain only the
// fixed field classifications emitted by this resource.
func teamMemberAddDiagnosticError(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("LiteLLM returned HTTP %d", apiErr.StatusCode)
	}
	var responseErr *safeResponseError
	if errors.As(err, &responseErr) {
		if responseErr.statusCode != 0 {
			return fmt.Sprintf("LiteLLM returned HTTP %d but its response could not be safely processed", responseErr.statusCode)
		}
		return "The LiteLLM response could not be safely processed"
	}
	var transportErr *safeTransportError
	if errors.As(err, &transportErr) {
		switch {
		case errors.Is(err, context.Canceled):
			return "The LiteLLM request was canceled"
		case errors.Is(err, context.DeadlineExceeded):
			return "The LiteLLM request timed out"
		default:
			return "The LiteLLM transport request failed"
		}
	}
	return err.Error()
}

func observeBatch(snapshot *teamMemberAddSnapshot, owned []batchMember, managedBudget types.Float64) (*observedBatch, error) {
	if snapshot == nil {
		return &observedBatch{RemoteIndex: map[string]int{}, Orphans: map[string]int{}, Budget: managedBudget}, nil
	}
	members := append([]batchMember(nil), owned...)
	sortBatchMembers(members)
	observed := &observedBatch{
		Members:     make([]batchMember, 0, len(members)),
		RemoteIndex: make(map[string]int, len(members)),
		Orphans:     make(map[string]int, len(members)),
		Budget:      managedBudget,
	}
	usedRemote := make(map[int]string, len(members))
	usedMembership := make(map[int]string, len(members))
	var commonBudget *float64
	budgetSet := false
	for _, member := range members {
		remoteIndex, err := matchBatchMember(member, snapshot.Members)
		if err != nil {
			return nil, err
		}
		membershipIndex, membershipErr := matchBatchMembership(member, snapshot.Memberships)
		if membershipErr != nil {
			return nil, membershipErr
		}
		if remoteIndex < 0 && membershipIndex < 0 {
			continue
		}
		if remoteIndex >= 0 {
			remote := snapshot.Members[remoteIndex]
			// user_id is Optional+Computed specifically so email-only ownership can
			// retain its configured email while persisting LiteLLM's canonical ID.
			if remote.UserID != "" {
				member.UserID = remote.UserID
				member.HasUserID = true
			}
			key := batchMemberIdentityKey(member)
			if priorKey, duplicate := usedRemote[remoteIndex]; duplicate && priorKey != key {
				return nil, fmt.Errorf("two owned member blocks resolve to the same remote roster entry")
			}
			usedRemote[remoteIndex] = key
			member.Role = remote.Role
			member.RoleKnown = true
			observed.RemoteIndex[key] = remoteIndex
		} else {
			key := batchMemberIdentityKey(member)
			// v1.98 writes the team-membership and optional budget before the
			// authoritative members_with_roles roster. Membership proves retained
			// ownership after an add, but it cannot prove that the requested role
			// reached the later roster write.
			if priorKey, duplicate := usedMembership[membershipIndex]; duplicate && priorKey != key {
				return nil, fmt.Errorf("two owned member blocks resolve to the same remote membership row")
			}
			usedMembership[membershipIndex] = key
			member.Role = ""
			member.RoleKnown = false
			observed.Orphans[key] = membershipIndex
		}
		observed.Members = append(observed.Members, member)

		if managedBudget.IsNull() {
			continue
		}
		var budget *float64
		if membershipIndex >= 0 {
			budget = snapshot.Memberships[membershipIndex].MaxBudget
		} else {
			budget, err = snapshot.memberBudget(remoteIndex)
			if err != nil {
				return nil, err
			}
		}
		if !budgetSet {
			commonBudget = budget
			budgetSet = true
		} else if !equalOptionalFloat(commonBudget, budget) {
			return nil, fmt.Errorf("owned members have different remote max budgets, which cannot be represented by the shared max_budget_in_team attribute")
		}
	}
	if !managedBudget.IsNull() && budgetSet {
		if commonBudget == nil {
			observed.Budget = types.Float64Null()
		} else {
			observed.Budget = types.Float64Value(*commonBudget)
		}
	}
	return observed, nil
}

func (snapshot *teamMemberAddSnapshot) memberBudget(remoteIndex int) (*float64, error) {
	if remoteIndex < 0 || remoteIndex >= len(snapshot.Members) {
		return nil, fmt.Errorf("remote member index is invalid")
	}
	userID := snapshot.Members[remoteIndex].UserID
	if userID == "" {
		return nil, fmt.Errorf("an owned roster entry has no user_id, so its native membership budget cannot be reconciled")
	}
	membershipIndex, err := matchBatchMembership(batchMember{UserID: userID, HasUserID: true}, snapshot.Memberships)
	if err != nil {
		return nil, err
	}
	if membershipIndex < 0 {
		return nil, nil
	}
	return snapshot.Memberships[membershipIndex].MaxBudget, nil
}

func matchBatchMembership(member batchMember, memberships []remoteBatchMembership) (int, error) {
	if !member.HasUserID {
		return -1, nil
	}
	match := -1
	for index, membership := range memberships {
		if membership.UserID != member.UserID {
			continue
		}
		if match >= 0 {
			return -1, fmt.Errorf("/team/info returned duplicate team_memberships rows for an owned user")
		}
		match = index
	}
	return match, nil
}

func reconcileTeamMemberAddOrphanMarkers(snapshot *teamMemberAddSnapshot, owned []batchMember, markers teamMemberAddOrphanMarkers) (teamMemberAddOrphanMarkers, teamMemberAddOrphanMarkers, error) {
	active := teamMemberAddOrphanMarkers{}
	remediated := teamMemberAddOrphanMarkers{}
	seen := teamMemberAddOrphanMarkers{}
	for _, member := range owned {
		marker := teamMemberAddOrphanMarkerForMember(member)
		if _, marked := markers[marker]; !marked {
			continue
		}
		seen[marker] = struct{}{}
		rosterIndex, err := matchBatchMember(member, snapshot.Members)
		if err != nil {
			return nil, nil, err
		}
		membershipIndex, err := matchBatchMembership(member, snapshot.Memberships)
		if err != nil {
			return nil, nil, err
		}
		if rosterIndex < 0 && membershipIndex >= 0 {
			active[marker] = struct{}{}
			continue
		}
		delete(markers, marker)
		if rosterIndex < 0 && membershipIndex < 0 {
			remediated[marker] = struct{}{}
		}
	}
	for marker := range markers {
		if _, stillOwned := seen[marker]; !stillOwned {
			delete(markers, marker)
		}
	}
	return active, remediated, nil
}

func legacyEmailOnlyMembershipAmbiguity(snapshot *teamMemberAddSnapshot, owned []batchMember) error {
	rosterIDs := make(map[string]struct{}, len(snapshot.Members))
	for _, remote := range snapshot.Members {
		if remote.UserID != "" {
			rosterIDs[remote.UserID] = struct{}{}
		}
	}
	unmatchedMemberships := 0
	for _, membership := range snapshot.Memberships {
		if _, rosterBacked := rosterIDs[membership.UserID]; !rosterBacked {
			unmatchedMemberships++
		}
	}
	if unmatchedMemberships == 0 {
		return nil
	}
	for _, member := range owned {
		if member.HasUserID || !member.HasUserEmail {
			continue
		}
		rosterIndex, err := matchBatchMember(member, snapshot.Members)
		if err != nil {
			return err
		}
		if rosterIndex < 0 {
			return fmt.Errorf("an email-only historical state member is absent from members_with_roles while /team/info contains %d unmatched team_memberships row(s). Those rows expose only user_id, so ownership cannot be correlated safely. The provider preserved prior state and will not retry add, mutate, delete, or report destroy success. Resolve the roster or orphan rows with a LiteLLM administrator, then refresh", unmatchedMemberships)
		}
	}
	return nil
}

func unresolvedUnknownBatchMembers(snapshot *teamMemberAddSnapshot, owned []batchMember, activeOrphans teamMemberAddOrphanMarkers) []batchMember {
	unresolved := make([]batchMember, 0)
	for _, member := range owned {
		if member.RoleKnown {
			continue
		}
		if _, retainedOrphan := activeOrphans[teamMemberAddOrphanMarkerForMember(member)]; retainedOrphan {
			continue
		}
		rosterIndex, err := matchBatchMember(member, snapshot.Members)
		if err != nil || rosterIndex < 0 {
			unresolved = append(unresolved, member)
		}
	}
	return unresolved
}

func planBatchBudgetUpdates(snapshot *teamMemberAddSnapshot, owned []batchMember, desired []batchMember, desiredMatches map[string]int, desiredBudget float64) ([]batchBudgetUpdate, error) {
	ownedIDs := make(map[string]struct{}, len(owned))
	for _, member := range owned {
		remoteIndex, err := matchBatchMember(member, snapshot.Members)
		if err != nil {
			return nil, err
		}
		if remoteIndex < 0 || snapshot.Members[remoteIndex].UserID == "" {
			return nil, fmt.Errorf("every budget-managed owned member must have an authoritative user_id")
		}
		ownedIDs[snapshot.Members[remoteIndex].UserID] = struct{}{}
	}

	budgetReferences := make(map[string][]string)
	for _, membership := range snapshot.Memberships {
		if membership.BudgetID != "" {
			budgetReferences[membership.BudgetID] = append(budgetReferences[membership.BudgetID], membership.UserID)
		}
	}
	for budgetID := range budgetReferences {
		sort.Strings(budgetReferences[budgetID])
	}

	desiredByID := make(map[string]float64, len(desired))
	desiredMemberByID := make(map[string]batchMember, len(desired))
	for _, member := range desired {
		remoteIndex, ok := desiredMatches[batchMemberIdentityKey(member)]
		if !ok || remoteIndex < 0 || remoteIndex >= len(snapshot.Members) || snapshot.Members[remoteIndex].UserID == "" {
			return nil, fmt.Errorf("every budget-managed desired member must have an authoritative user_id")
		}
		userID := snapshot.Members[remoteIndex].UserID
		desiredByID[userID] = desiredBudget
		desiredMemberByID[userID] = member
	}

	grouped := make(map[string]batchBudgetUpdate)
	for userID, wanted := range desiredByID {
		membershipIndex, err := matchBatchMembership(batchMember{UserID: userID, HasUserID: true}, snapshot.Memberships)
		if err != nil {
			return nil, err
		}
		var membership remoteBatchMembership
		if membershipIndex >= 0 {
			membership = snapshot.Memberships[membershipIndex]
			if membership.MaxBudget != nil && *membership.MaxBudget == wanted {
				continue
			}
		}

		// v1.98 clones only the selected member away from the current team-member
		// default row identified by team_info.metadata.team_member_budget_id. A
		// null reference also creates a private budget. Every other budget ID is a
		// historical row updated in place, so all references must be classified
		// before any write. The team ID is never a default-budget identifier.
		groupKey := "user:" + userID
		affected := []string{userID}
		if membership.BudgetID != "" && membership.BudgetID != snapshot.TeamBudgetID {
			groupKey = "budget:" + membership.BudgetID
			affected = append([]string(nil), budgetReferences[membership.BudgetID]...)
			for _, referencedUserID := range affected {
				if _, isOwned := ownedIDs[referencedUserID]; !isOwned {
					return nil, fmt.Errorf("historical budget row %q is also referenced by a member outside this resource; refusing a write that would mutate unrelated membership state", membership.BudgetID)
				}
				referencedDesired, retained := desiredByID[referencedUserID]
				if !retained || referencedDesired != wanted {
					return nil, fmt.Errorf("owned members sharing historical budget row %q do not have one compatible retained desired budget; split removal and budget changes into separate applies", membership.BudgetID)
				}
			}
		}

		candidate := batchBudgetUpdate{Member: desiredMemberByID[userID], RemoteIndex: desiredMatches[batchMemberIdentityKey(desiredMemberByID[userID])], AffectedIDs: affected}
		if existing, ok := grouped[groupKey]; !ok || batchMemberIdentityKey(candidate.Member) < batchMemberIdentityKey(existing.Member) {
			grouped[groupKey] = candidate
		}
	}

	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	updates := make([]batchBudgetUpdate, 0, len(keys))
	for _, key := range keys {
		updates = append(updates, grouped[key])
	}
	return updates, nil
}

func batchBudgetMatchesUsers(snapshot *teamMemberAddSnapshot, userIDs []string, wanted float64) (bool, error) {
	for _, userID := range userIDs {
		membershipIndex, err := matchBatchMembership(batchMember{UserID: userID, HasUserID: true}, snapshot.Memberships)
		if err != nil {
			return false, err
		}
		if membershipIndex < 0 {
			return false, nil
		}
		budget := snapshot.Memberships[membershipIndex].MaxBudget
		if budget == nil || *budget != wanted {
			return false, nil
		}
	}
	return true, nil
}

func matchBatchMember(member batchMember, roster []remoteBatchMember) (int, error) {
	idMatches := make([]int, 0, 1)
	emailMatches := make([]int, 0, 1)
	for index, remote := range roster {
		if member.HasUserID && remote.UserID == member.UserID {
			idMatches = append(idMatches, index)
		}
		if member.HasUserEmail && canonicalBatchEmail(remote.UserEmail) == canonicalBatchEmail(member.UserEmail) {
			emailMatches = append(emailMatches, index)
		}
	}
	if len(idMatches) > 1 || len(emailMatches) > 1 {
		return -1, fmt.Errorf("an owned member identity matches multiple remote roster entries")
	}
	if member.HasUserID && member.HasUserEmail {
		if len(idMatches) == 0 && len(emailMatches) == 0 {
			return -1, nil
		}
		if len(idMatches) != 1 || len(emailMatches) != 1 || idMatches[0] != emailMatches[0] {
			return -1, fmt.Errorf("an owned member's user_id and user_email do not identify the same remote roster entry")
		}
		return idMatches[0], nil
	}
	if member.HasUserID && len(idMatches) == 1 {
		return idMatches[0], nil
	}
	if member.HasUserEmail && len(emailMatches) == 1 {
		return emailMatches[0], nil
	}
	return -1, nil
}

func classifyBatchUpdate(snapshot *teamMemberAddSnapshot, current *observedBatch, desired []batchMember) (map[string]int, []batchMember, []batchMember, error) {
	matches := make(map[string]int, len(desired))
	usedRemote := make(map[int]string, len(desired))
	additions := make([]batchMember, 0)
	for _, member := range desired {
		key := batchMemberIdentityKey(member)
		remoteIndex, err := matchBatchMember(member, snapshot.Members)
		if err != nil {
			return nil, nil, nil, err
		}
		membershipIndex, membershipErr := matchBatchMembership(member, snapshot.Memberships)
		if membershipErr != nil {
			return nil, nil, nil, membershipErr
		}
		if remoteIndex < 0 {
			if membershipIndex >= 0 {
				if _, ownedOrphan := current.Orphans[key]; !ownedOrphan {
					return nil, nil, nil, fmt.Errorf("a desired addition already has an unowned team_memberships row without a roster entry; do not retry member_add because LiteLLM v1.98 cannot repair or delete this condition through its team-member API; manually remediate it upstream (import only if Terraform must record ownership during remediation)")
				}
			}
			additions = append(additions, member)
			continue
		}
		if _, alreadyUsed := usedRemote[remoteIndex]; alreadyUsed {
			return nil, nil, nil, fmt.Errorf("two desired member blocks resolve to the same remote roster entry")
		}
		usedRemote[remoteIndex] = key
		matches[key] = remoteIndex

		owned := false
		for _, currentRemoteIndex := range current.RemoteIndex {
			if currentRemoteIndex == remoteIndex {
				owned = true
				break
			}
		}
		if !owned {
			return nil, nil, nil, fmt.Errorf("a desired addition already exists in the team but is not in this resource's explicitly owned roster; import it instead of adopting it during update")
		}
	}

	removals := make([]batchMember, 0)
	for _, member := range current.Members {
		remoteIndex := current.RemoteIndex[batchMemberIdentityKey(member)]
		if _, retained := usedRemote[remoteIndex]; !retained {
			removals = append(removals, member)
		}
	}
	sortBatchMembers(additions)
	sortBatchMembers(removals)
	return matches, additions, removals, nil
}

func ensureMembersAbsent(snapshot *teamMemberAddSnapshot, members []batchMember) error {
	used := make(map[int]struct{}, len(members))
	for _, member := range members {
		index, err := matchBatchMember(member, snapshot.Members)
		if err != nil {
			return err
		}
		if index >= 0 {
			if _, duplicate := used[index]; duplicate {
				return fmt.Errorf("multiple requested members resolve to the same existing roster entry")
			}
			return fmt.Errorf("at least one requested member is already present in the authoritative team roster")
		}
		membershipIndex, membershipErr := matchBatchMembership(member, snapshot.Memberships)
		if membershipErr != nil {
			return membershipErr
		}
		if membershipIndex >= 0 {
			return fmt.Errorf("at least one requested member already has a team_memberships row without an authoritative roster entry")
		}
	}
	return nil
}

func snapshotContainsBatchMember(snapshot *teamMemberAddSnapshot, member batchMember) (bool, error) {
	if snapshot == nil {
		return false, nil
	}
	rosterIndex, err := matchBatchMember(member, snapshot.Members)
	if err != nil || rosterIndex >= 0 {
		return rosterIndex >= 0, err
	}
	membershipIndex, err := matchBatchMembership(member, snapshot.Memberships)
	return membershipIndex >= 0, err
}

func snapshotHasMembershipOnlyBatchMember(snapshot *teamMemberAddSnapshot, member batchMember) (bool, error) {
	if snapshot == nil || !snapshot.MembershipsKnown {
		return false, nil
	}
	rosterIndex, err := matchBatchMember(member, snapshot.Members)
	if err != nil || rosterIndex >= 0 {
		return false, err
	}
	membershipIndex, err := matchBatchMembership(member, snapshot.Memberships)
	return membershipIndex >= 0, err
}

func recoverAddedBatchMember(before, after *teamMemberAddSnapshot, requested batchMember) (batchMember, bool, error) {
	if after == nil || !after.MembershipsKnown {
		return batchMember{}, false, nil
	}
	beforeIndex := -1
	var err error
	if before != nil {
		beforeIndex, err = matchBatchMembership(requested, before.Memberships)
		if err != nil {
			return batchMember{}, false, err
		}
	}
	afterIndex, err := matchBatchMembership(requested, after.Memberships)
	if err != nil {
		return batchMember{}, false, err
	}

	recovered := requested
	if !requested.HasUserID && afterIndex < 0 {
		// team_memberships carries only user_id. For an email-only add that fails
		// before the roster write, the single membership-row delta is the only
		// canonical identity v1.98 returns. Preserve the configured email spelling
		// and add that canonical ID so Read/apply/destroy retain and diagnose the
		// otherwise unrepairable ownership without sending unsafe cleanup.
		beforeIDs := make(map[string]struct{})
		if before != nil && before.MembershipsKnown {
			for _, membership := range before.Memberships {
				beforeIDs[membership.UserID] = struct{}{}
			}
		}
		newIndices := make([]int, 0, 1)
		for index, membership := range after.Memberships {
			if _, existed := beforeIDs[membership.UserID]; !existed {
				newIndices = append(newIndices, index)
			}
		}
		if len(newIndices) > 1 {
			return batchMember{}, false, fmt.Errorf("multiple new team_memberships rows appeared after one email-only add")
		}
		if len(newIndices) == 1 {
			afterIndex = newIndices[0]
			recovered.UserID = after.Memberships[afterIndex].UserID
			recovered.HasUserID = true
		}
	}
	if beforeIndex >= 0 || afterIndex < 0 {
		return batchMember{}, false, nil
	}

	recovered.Role = ""
	recovered.RoleKnown = false
	rosterIndex, rosterErr := matchBatchMember(recovered, after.Members)
	if rosterErr != nil {
		return batchMember{}, false, rosterErr
	}
	if rosterIndex >= 0 {
		recovered.Role = after.Members[rosterIndex].Role
		recovered.RoleKnown = true
	}
	return recovered, true, nil
}

func ensureUniqueMatches(snapshot *teamMemberAddSnapshot, members []batchMember) error {
	used := make(map[int]struct{}, len(members))
	for _, member := range members {
		index, err := matchBatchMember(member, snapshot.Members)
		if err != nil {
			return err
		}
		if index < 0 {
			continue
		}
		if _, duplicate := used[index]; duplicate {
			return fmt.Errorf("two configured member identities resolve to the same remote roster entry")
		}
		used[index] = struct{}{}
	}
	return nil
}

func batchMembersFromSet(value types.Set, requireKnownRole bool) ([]batchMember, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if value.IsNull() {
		return nil, diagnostics
	}
	if value.IsUnknown() {
		diagnostics.AddError("Unknown Batch Members", "The member set must be known before the batch membership lifecycle can run.")
		return nil, diagnostics
	}

	var models []MemberModel
	diagnostics.Append(value.ElementsAs(context.Background(), &models, false)...)
	if diagnostics.HasError() {
		return nil, diagnostics
	}
	members := make([]batchMember, 0, len(models))
	for _, model := range models {
		member := batchMember{}
		if !model.UserID.IsNull() && !model.UserID.IsUnknown() {
			member.UserID = model.UserID.ValueString()
			member.HasUserID = member.UserID != ""
		}
		if !model.UserEmail.IsNull() && !model.UserEmail.IsUnknown() {
			member.UserEmail = model.UserEmail.ValueString()
			member.HasUserEmail = member.UserEmail != ""
		}
		if !model.Role.IsNull() && !model.Role.IsUnknown() {
			member.Role = model.Role.ValueString()
			member.RoleKnown = true
		} else if requireKnownRole {
			diagnostics.AddError("Unknown Batch Member Role", "Every member role must be known before mutation.")
		}
		members = append(members, member)
	}
	return members, diagnostics
}

func validateBatchMemberIdentities(members []batchMember, requireNonEmpty bool) error {
	if requireNonEmpty && len(members) == 0 {
		return fmt.Errorf("at least one member block is required")
	}
	seenIDs := make(map[string]struct{}, len(members))
	seenEmails := make(map[string]struct{}, len(members))
	for _, member := range members {
		if !member.HasUserID && !member.HasUserEmail {
			return fmt.Errorf("every member must have a non-empty user_id, user_email, or both")
		}
		if member.HasUserID {
			if strings.TrimSpace(member.UserID) == "" {
				return fmt.Errorf("member user_id values must not be blank")
			}
			if _, duplicate := seenIDs[member.UserID]; duplicate {
				return fmt.Errorf("member user_id values must be unique within the batch")
			}
			seenIDs[member.UserID] = struct{}{}
		}
		if member.HasUserEmail {
			if strings.TrimSpace(member.UserEmail) == "" {
				return fmt.Errorf("member user_email values must not be blank")
			}
			canonicalEmail := canonicalBatchEmail(member.UserEmail)
			if _, duplicate := seenEmails[canonicalEmail]; duplicate {
				return fmt.Errorf("member user_email values must be unique within the batch, ignoring case")
			}
			seenEmails[canonicalEmail] = struct{}{}
		}
	}
	return nil
}

func setModelMembers(ctx context.Context, data *TeamMemberAddResourceModel, members []batchMember) diag.Diagnostics {
	models := make([]MemberModel, 0, len(members))
	for _, member := range members {
		model := MemberModel{
			UserID:    types.StringNull(),
			UserEmail: types.StringNull(),
			Role:      types.StringUnknown(),
		}
		if member.HasUserID {
			model.UserID = types.StringValue(member.UserID)
		}
		if member.HasUserEmail {
			model.UserEmail = types.StringValue(member.UserEmail)
		}
		if member.RoleKnown {
			model.Role = types.StringValue(member.Role)
		}
		models = append(models, model)
	}
	value, diagnostics := types.SetValueFrom(ctx, MemberObjectType(), models)
	data.Members = value
	return diagnostics
}

func setCreateTeamMemberAddOrphanMarkers(ctx context.Context, resp *resource.CreateResponse, markers teamMemberAddOrphanMarkers) {
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMemberAddOrphanPrivateKey, encodeTeamMemberAddOrphanMarkers(markers))...)
	}
}

func setReadTeamMemberAddOrphanMarkers(ctx context.Context, resp *resource.ReadResponse, markers teamMemberAddOrphanMarkers) {
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMemberAddOrphanPrivateKey, encodeTeamMemberAddOrphanMarkers(markers))...)
	}
}

func setUpdateTeamMemberAddOrphanMarkers(ctx context.Context, resp *resource.UpdateResponse, markers teamMemberAddOrphanMarkers) {
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMemberAddOrphanPrivateKey, encodeTeamMemberAddOrphanMarkers(markers))...)
	}
}

func setDeleteTeamMemberAddOrphanMarkers(ctx context.Context, resp *resource.DeleteResponse, markers teamMemberAddOrphanMarkers) {
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMemberAddOrphanPrivateKey, encodeTeamMemberAddOrphanMarkers(markers))...)
	}
}

func setCreatePartialState(ctx context.Context, resp *resource.CreateResponse, base *TeamMemberAddResourceModel, members []batchMember, snapshot *teamMemberAddSnapshot) {
	if len(members) == 0 && resp.Diagnostics.HasError() {
		resp.State.RemoveResource(ctx)
		return
	}
	partial := recoveredBatchModel(ctx, base, members, snapshot)
	resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
}

func setUpdateRecoveredState(ctx context.Context, resp *resource.UpdateResponse, base *TeamMemberAddResourceModel, owned []batchMember, snapshot *teamMemberAddSnapshot) {
	recovered := recoveredBatchModel(ctx, base, owned, snapshot)
	resp.Diagnostics.Append(resp.State.Set(ctx, &recovered)...)
}

func setDeleteRecoveredState(ctx context.Context, resp *resource.DeleteResponse, base *TeamMemberAddResourceModel, owned []batchMember, snapshot *teamMemberAddSnapshot) {
	recovered := recoveredBatchModel(ctx, base, owned, snapshot)
	resp.Diagnostics.Append(resp.State.Set(ctx, &recovered)...)
}

func recoveredBatchModel(ctx context.Context, base *TeamMemberAddResourceModel, owned []batchMember, snapshot *teamMemberAddSnapshot) TeamMemberAddResourceModel {
	recovered := *base
	if snapshot != nil && snapshot.MembershipsKnown {
		// Recover only from the complete membership collection. A decoder error
		// before that boundary must retain every prior identity; treating a
		// malformed partial collection as empty would silently abandon ownership.
		// If the shared budget is temporarily non-representable, retain its
		// previous value while still recovering authoritative roster roles.
		if observed, err := observeBatch(snapshot, owned, base.MaxBudgetInTeam); err == nil {
			owned = observed.Members
			recovered.MaxBudgetInTeam = observed.Budget
		} else if observed, roleErr := observeBatch(snapshot, owned, types.Float64Null()); roleErr == nil {
			owned = observed.Members
		}
	}
	_ = setModelMembers(ctx, &recovered, owned)
	return recovered
}

func batchMemberRequest(member batchMember) map[string]interface{} {
	request := map[string]interface{}{"role": member.Role}
	if member.HasUserID {
		request["user_id"] = member.UserID
	}
	if member.HasUserEmail {
		request["user_email"] = member.UserEmail
	}
	return request
}

func addRemoteMemberIdentity(request map[string]interface{}, member remoteBatchMember) {
	if member.UserID != "" {
		request["user_id"] = member.UserID
		return
	}
	request["user_email"] = member.UserEmail
}

func batchTeamID(data *TeamMemberAddResourceModel) string {
	if !data.TeamID.IsNull() && !data.TeamID.IsUnknown() && data.TeamID.ValueString() != "" {
		return data.TeamID.ValueString()
	}
	if !data.ID.IsNull() && !data.ID.IsUnknown() {
		return data.ID.ValueString()
	}
	return ""
}

func canonicalBatchEmail(email string) string {
	return strings.ToLower(email)
}

func batchMemberIdentityKey(member batchMember) string {
	if member.HasUserID && member.HasUserEmail {
		return "b:" + base64.RawURLEncoding.EncodeToString([]byte(member.UserID)) + ":" + base64.RawURLEncoding.EncodeToString([]byte(canonicalBatchEmail(member.UserEmail)))
	}
	if member.HasUserID {
		return "i:" + base64.RawURLEncoding.EncodeToString([]byte(member.UserID))
	}
	return "e:" + base64.RawURLEncoding.EncodeToString([]byte(canonicalBatchEmail(member.UserEmail)))
}

func sortBatchMembers(members []batchMember) {
	sort.Slice(members, func(i, j int) bool {
		return batchMemberIdentityKey(members[i]) < batchMemberIdentityKey(members[j])
	})
}

func removeBatchMember(members []batchMember, remove batchMember) []batchMember {
	key := batchMemberIdentityKey(remove)
	result := make([]batchMember, 0, len(members))
	for _, member := range members {
		if batchMemberIdentityKey(member) != key {
			result = append(result, member)
		}
	}
	return result
}

func replaceBatchMember(members []batchMember, oldMember, replacement batchMember) []batchMember {
	oldKey := batchMemberIdentityKey(oldMember)
	result := make([]batchMember, 0, len(members))
	for _, member := range members {
		if batchMemberIdentityKey(member) == oldKey {
			result = append(result, replacement)
		} else {
			result = append(result, member)
		}
	}
	sortBatchMembers(result)
	return result
}

func batchRolesEqual(observed, desired []batchMember) bool {
	if len(observed) != len(desired) {
		return false
	}
	used := make(map[int]struct{}, len(observed))
	for _, wanted := range desired {
		match := -1
		for index, actual := range observed {
			if _, alreadyUsed := used[index]; alreadyUsed || !batchMemberIdentitiesOverlap(wanted, actual) {
				continue
			}
			if match >= 0 {
				return false
			}
			match = index
		}
		if match < 0 || observed[match].Role != wanted.Role {
			return false
		}
		used[match] = struct{}{}
	}
	return true
}

func batchMemberIdentitiesOverlap(left, right batchMember) bool {
	if left.HasUserID && right.HasUserID && left.UserID != right.UserID {
		return false
	}
	if left.HasUserEmail && right.HasUserEmail && canonicalBatchEmail(left.UserEmail) != canonicalBatchEmail(right.UserEmail) {
		return false
	}
	return left.HasUserID && right.HasUserID || left.HasUserEmail && right.HasUserEmail
}

func equalOptionalFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func parseTeamMemberAddImportID(importID string) (string, []batchMember, bool, error) {
	if importID == "" {
		return "", nil, false, fmt.Errorf("import ID must not be empty")
	}
	if strings.HasPrefix(importID, teamMemberAddPlainImportPrefix) {
		teamID, err := decodeTeamMemberAddImportComponent(strings.TrimPrefix(importID, teamMemberAddPlainImportPrefix))
		if err != nil {
			return "", nil, false, fmt.Errorf("invalid escaped plain team-ID import: %w", err)
		}
		return teamID, nil, true, nil
	}
	if !strings.HasPrefix(importID, teamMemberAddImportPrefix) {
		return importID, nil, true, nil
	}
	teamID, members, err := parseTeamMemberAddCompositeImportID(importID)
	if err != nil {
		// Team IDs have historically been unrestricted strings. In particular,
		// IDs such as v1.production predate the composite grammar. Only a fully
		// valid composite claims the v1 namespace; every parse failure remains a
		// plain team-ID import with an empty owned roster.
		return importID, nil, true, nil
	}
	return teamID, members, false, nil
}

func parseTeamMemberAddCompositeImportID(importID string) (string, []batchMember, error) {
	parts := strings.SplitN(importID, ".", 3)
	if len(parts) != 3 || parts[0] != "v1" || parts[1] == "" || parts[2] == "" {
		return "", nil, fmt.Errorf("not a complete composite import ID")
	}
	teamID, err := decodeTeamMemberAddImportComponent(parts[1])
	if err != nil {
		return "", nil, fmt.Errorf("invalid team_id component")
	}
	members := make([]batchMember, 0)
	for _, token := range strings.Split(parts[2], ",") {
		tokenParts := strings.Split(token, "~")
		member := batchMember{}
		switch {
		case len(tokenParts) == 2 && tokenParts[0] == "i":
			member.UserID, err = decodeTeamMemberAddImportComponent(tokenParts[1])
			member.HasUserID = err == nil
		case len(tokenParts) == 2 && tokenParts[0] == "e":
			member.UserEmail, err = decodeTeamMemberAddImportComponent(tokenParts[1])
			member.HasUserEmail = err == nil
		case len(tokenParts) == 3 && tokenParts[0] == "b":
			member.UserID, err = decodeTeamMemberAddImportComponent(tokenParts[1])
			if err == nil {
				member.UserEmail, err = decodeTeamMemberAddImportComponent(tokenParts[2])
			}
			member.HasUserID = err == nil
			member.HasUserEmail = err == nil
		default:
			return "", nil, fmt.Errorf("invalid member token")
		}
		if err != nil {
			return "", nil, fmt.Errorf("invalid base64url member data")
		}
		members = append(members, member)
	}
	if err := validateBatchMemberIdentities(members, true); err != nil {
		return "", nil, fmt.Errorf("invalid composite roster: %w", err)
	}
	return teamID, members, nil
}

func decodeTeamMemberAddImportComponent(encoded string) (string, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(decoded) == 0 || !utf8.Valid(decoded) || base64.RawURLEncoding.EncodeToString(decoded) != encoded {
		return "", fmt.Errorf("invalid canonical unpadded base64url component")
	}
	return string(decoded), nil
}

func formatTeamMemberAddPlainImportID(teamID string) (string, error) {
	if teamID == "" {
		return "", fmt.Errorf("team_id must not be empty")
	}
	return teamMemberAddPlainImportPrefix + base64.RawURLEncoding.EncodeToString([]byte(teamID)), nil
}

func formatTeamMemberAddImportID(teamID string, members []batchMember) (string, error) {
	if teamID == "" {
		return "", fmt.Errorf("team_id must not be empty")
	}
	if err := validateBatchMemberIdentities(members, true); err != nil {
		return "", err
	}
	members = append([]batchMember(nil), members...)
	sortBatchMembers(members)
	tokens := make([]string, 0, len(members))
	for _, member := range members {
		if member.HasUserID && member.HasUserEmail {
			tokens = append(tokens, "b~"+base64.RawURLEncoding.EncodeToString([]byte(member.UserID))+"~"+base64.RawURLEncoding.EncodeToString([]byte(member.UserEmail)))
		} else if member.HasUserID {
			tokens = append(tokens, "i~"+base64.RawURLEncoding.EncodeToString([]byte(member.UserID)))
		} else {
			tokens = append(tokens, "e~"+base64.RawURLEncoding.EncodeToString([]byte(member.UserEmail)))
		}
	}
	return teamMemberAddImportPrefix + base64.RawURLEncoding.EncodeToString([]byte(teamID)) + "." + strings.Join(tokens, ","), nil
}

// MemberObjectType returns the object type for members.
func MemberObjectType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"user_id":    types.StringType,
			"user_email": types.StringType,
			"role":       types.StringType,
		},
	}
}
