package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

var _ resource.Resource = &OrganizationMemberResource{}
var _ resource.ResourceWithImportState = &OrganizationMemberResource{}
var _ planmodifier.Float64 = organizationMemberBudgetRemovalModifier{}

// organizationMemberBudgetRemovalModifier replaces only the unsupported
// known-value removal transition. Null or unknown prior values (including
// freshly imported state) and resource creation remain compatible.
type organizationMemberBudgetRemovalModifier struct{}

func (organizationMemberBudgetRemovalModifier) Description(context.Context) string {
	return "Replaces the membership when a previously known max budget is removed from configuration."
}

func (m organizationMemberBudgetRemovalModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (organizationMemberBudgetRemovalModifier) PlanModifyFloat64(_ context.Context, req planmodifier.Float64Request, resp *planmodifier.Float64Response) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() || req.ConfigValue.IsUnknown() || req.PlanValue.IsUnknown() {
		return
	}
	if req.ConfigValue.IsNull() && req.PlanValue.IsNull() {
		resp.RequiresReplace = true
	}
}

var organizationMemberRoles = []string{
	"org_admin",
	"internal_user",
	"internal_user_viewer",
}

func NewOrganizationMemberResource() resource.Resource {
	return &OrganizationMemberResource{}
}

type OrganizationMemberResource struct {
	client *Client
}

type OrganizationMemberResourceModel struct {
	ID                      types.String  `tfsdk:"id"`
	OrganizationID          types.String  `tfsdk:"organization_id"`
	UserID                  types.String  `tfsdk:"user_id"`
	UserEmail               types.String  `tfsdk:"user_email"`
	Role                    types.String  `tfsdk:"role"`
	MaxBudgetInOrganization types.Float64 `tfsdk:"max_budget_in_organization"`
}

type organizationMemberAPIModel struct {
	UserID             string                            `json:"user_id"`
	OrganizationID     string                            `json:"organization_id"`
	UserRole           *string                           `json:"user_role"`
	UserEmail          *string                           `json:"user_email"`
	BudgetID           *string                           `json:"budget_id"`
	LiteLLMBudgetTable *organizationMemberBudgetAPIModel `json:"litellm_budget_table"`
}

type organizationMemberBudgetAPIModel struct {
	BudgetID  *string      `json:"budget_id"`
	MaxBudget *json.Number `json:"max_budget"`
}

type organizationMemberUserAPIModel struct {
	UserID    string  `json:"user_id"`
	UserEmail *string `json:"user_email"`
}

type organizationMemberAddAPIResponse struct {
	OrganizationID                 string                           `json:"organization_id"`
	UpdatedUsers                   []organizationMemberUserAPIModel `json:"updated_users"`
	UpdatedOrganizationMemberships []organizationMemberAPIModel     `json:"updated_organization_memberships"`
}

type organizationInfoAPIResponse struct {
	OrganizationID string                       `json:"organization_id"`
	Members        []organizationMemberAPIModel `json:"members"`
}

func (r *OrganizationMemberResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_member"
}

func (r *OrganizationMemberResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a member of a LiteLLM organization. If the user does not exist, LiteLLM creates it as part of adding the membership.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Canonical membership identifier in organization_id:user_id form.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"organization_id": schema.StringAttribute{
				Description: "The organization ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_id": schema.StringAttribute{
				Description: "The user ID to add. Either user_id or user_email must be provided. When both are set, LiteLLM first looks up user_id and falls back to an existing user_email if that ID does not exist. Email-resolved creates are stored with the canonical user_id returned by LiteLLM.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_email": schema.StringAttribute{
				Description: "The user email used to resolve or create a user. Either user_id or user_email must be provided. This resource does not manage the user's email after LiteLLM resolves a user_id.",
				Optional:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"role": schema.StringAttribute{
				Description: "The member's organization role. LiteLLM v1.98.0 accepts org_admin, internal_user, or internal_user_viewer.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.OneOf(organizationMemberRoles...),
				},
			},
			"max_budget_in_organization": schema.Float64Attribute{
				Description: "Maximum spend for this user within the organization. LiteLLM v1.98.0 can set or change this value but cannot clear it in place; removing a previously configured value replaces the membership.",
				Optional:    true,
				PlanModifiers: []planmodifier.Float64{
					organizationMemberBudgetRemovalModifier{},
				},
			},
		},
	}
}

func (r *OrganizationMemberResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func organizationMemberIdentity(data *OrganizationMemberResourceModel) (string, string, error) {
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

func buildOrganizationMemberAddRequest(data *OrganizationMemberResourceModel) (map[string]interface{}, error) {
	userID, userEmail, err := organizationMemberIdentity(data)
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

	// v1.98.0 declares max_budget_in_organization on the add model but the
	// endpoint implementation ignores it. Budget persistence is deliberately
	// sequenced through /organization/member_update after the add succeeds.
	return map[string]interface{}{
		"organization_id": data.OrganizationID.ValueString(),
		"member":          member,
	}, nil
}

func buildOrganizationMemberUpdateRequest(data *OrganizationMemberResourceModel) (map[string]interface{}, error) {
	userID, userEmail, err := organizationMemberIdentity(data)
	if err != nil {
		return nil, err
	}
	request := map[string]interface{}{
		"organization_id": data.OrganizationID.ValueString(),
		"role":            data.Role.ValueString(),
	}
	// LiteLLM gives user_id precedence when both identifiers are present. Once
	// resolved, use only that canonical identity for update and delete.
	if userID != "" {
		request["user_id"] = userID
	} else {
		request["user_email"] = userEmail
	}
	if !data.MaxBudgetInOrganization.IsNull() && !data.MaxBudgetInOrganization.IsUnknown() {
		request["max_budget_in_organization"] = data.MaxBudgetInOrganization.ValueFloat64()
	}
	return request, nil
}

func buildOrganizationMemberDeleteRequest(data *OrganizationMemberResourceModel) (map[string]interface{}, error) {
	userID, userEmail, err := organizationMemberIdentity(data)
	if err != nil {
		return nil, err
	}
	request := map[string]interface{}{"organization_id": data.OrganizationID.ValueString()}
	if userID != "" {
		request["user_id"] = userID
	} else {
		request["user_email"] = userEmail
	}
	return request, nil
}

func isOrganizationMemberRole(role string) bool {
	for _, allowed := range organizationMemberRoles {
		if role == allowed {
			return true
		}
	}
	return false
}

func validateOrganizationMember(member organizationMemberAPIModel) error {
	if member.UserID == "" {
		return fmt.Errorf("membership is missing user_id")
	}
	if member.OrganizationID == "" {
		return fmt.Errorf("membership is missing organization_id")
	}
	return nil
}

func applyOrganizationMemberResponse(data *OrganizationMemberResourceModel, member organizationMemberAPIModel) error {
	_, err := applyOrganizationMemberResponseWithBudgetConfirmation(data, member)
	return err
}

// applyOrganizationMemberResponseWithBudgetConfirmation applies fields to a
// last-known state snapshot, never to a requested plan. The boolean is true
// only when LiteLLM actually returned the nested budget relation. An omitted or
// null relation preserves the snapshot but cannot confirm a requested change.
func applyOrganizationMemberResponseWithBudgetConfirmation(data *OrganizationMemberResourceModel, member organizationMemberAPIModel) (bool, error) {
	return applyOrganizationMemberResponseWithNumericOwnership(data, member, true)
}

func applyOrganizationMemberResponseWithNumericOwnership(data *OrganizationMemberResourceModel, member organizationMemberAPIModel, budgetOwned bool) (bool, error) {
	if err := validateOrganizationMember(member); err != nil {
		return false, err
	}
	if member.UserRole == nil || !isOrganizationMemberRole(*member.UserRole) {
		return false, fmt.Errorf("membership has missing or unsupported user_role")
	}
	data.UserID = types.StringValue(member.UserID)
	data.Role = types.StringValue(*member.UserRole)
	data.ID = types.StringValue(fmt.Sprintf("%s:%s", data.OrganizationID.ValueString(), member.UserID))
	if data.UserEmail.IsUnknown() && member.UserEmail != nil && *member.UserEmail != "" {
		data.UserEmail = types.StringValue(*member.UserEmail)
	}

	// Exact LiteLLM v1.98 organization-admin responses can prove membership
	// through /organization/info while omitting or returning null for this
	// relation, even when budget_id is set. Preserve only the last-known value in
	// that case. A loaded nested object remains authoritative when LiteLLM
	// actually returns it.
	budget := member.LiteLLMBudgetTable
	if budget == nil {
		return false, nil
	}
	if member.BudgetID != nil && *member.BudgetID != "" {
		if budget.BudgetID == nil || *budget.BudgetID != *member.BudgetID {
			return true, fmt.Errorf("litellm_budget_table budget_id does not match membership budget_id")
		}
	} else if budget.BudgetID != nil && *budget.BudgetID != "" {
		return true, fmt.Errorf("litellm_budget_table has a budget_id but the membership does not")
	}
	if budget.MaxBudget == nil {
		if budgetOwned {
			data.MaxBudgetInOrganization = types.Float64Null()
		}
	} else {
		maxBudget, err := float64FromAPI(*budget.MaxBudget)
		if err != nil {
			return true, fmt.Errorf("invalid numeric response field %q: %w", "litellm_budget_table.max_budget", err)
		}
		if budgetOwned {
			data.MaxBudgetInOrganization = types.Float64Value(maxBudget)
		}
	}
	return true, nil
}

// validateOrganizationMemberAddResponseStructure proves the canonical identity
// returned by a successful mutation without deciding whether it matches the
// requested identity. LiteLLM can resolve a requested ID A through the supplied
// email to canonical ID B. That mismatch is still an apply error, but B is the
// only safe identity for partial state once this envelope is structurally valid.
func validateOrganizationMemberAddResponseStructure(response organizationMemberAddAPIResponse, data *OrganizationMemberResourceModel) (organizationMemberAPIModel, error) {
	if response.OrganizationID != data.OrganizationID.ValueString() {
		return organizationMemberAPIModel{}, fmt.Errorf("add response organization_id does not match the requested organization")
	}
	if len(response.UpdatedUsers) != 1 || len(response.UpdatedOrganizationMemberships) != 1 {
		return organizationMemberAPIModel{}, fmt.Errorf("add response must contain exactly one updated user and one updated organization membership")
	}
	member := response.UpdatedOrganizationMemberships[0]
	if err := validateOrganizationMember(member); err != nil {
		return organizationMemberAPIModel{}, err
	}
	if member.OrganizationID != response.OrganizationID {
		return organizationMemberAPIModel{}, fmt.Errorf("updated membership organization_id does not match the add response")
	}
	updatedUser := response.UpdatedUsers[0]
	if updatedUser.UserID == "" || updatedUser.UserID != member.UserID {
		return organizationMemberAPIModel{}, fmt.Errorf("updated user and membership user_id values are missing or inconsistent")
	}

	userID, userEmail, _ := organizationMemberIdentity(data)
	// Email is authoritative only for an email-only create or the documented
	// ID-then-email fallback. When the requested ID itself was returned, LiteLLM
	// gave that ID precedence and may ignore the supplied email.
	if userID == "" || member.UserID != userID {
		if userEmail == "" || updatedUser.UserEmail == nil || *updatedUser.UserEmail != userEmail {
			return organizationMemberAPIModel{}, fmt.Errorf("add response user_email does not match the requested email identity")
		}
	}

	validated := *data
	// member_add does not persist the requested budget. Validate its response
	// from that null post-add baseline so an omitted nested relation can never
	// inherit the plan, even in temporary validation state.
	validated.MaxBudgetInOrganization = types.Float64Null()
	if err := applyOrganizationMemberResponse(&validated, member); err != nil {
		return organizationMemberAPIModel{}, fmt.Errorf("invalid updated membership: %w", err)
	}
	return member, nil
}

func validateOrganizationMemberAddResponse(response organizationMemberAddAPIResponse, data *OrganizationMemberResourceModel) (organizationMemberAPIModel, error) {
	member, err := validateOrganizationMemberAddResponseStructure(response, data)
	if err != nil {
		return organizationMemberAPIModel{}, err
	}
	userID, _, _ := organizationMemberIdentity(data)
	if userID != "" && member.UserID != userID {
		return member, fmt.Errorf("add response user_id does not match the requested user_id")
	}
	if *member.UserRole != data.Role.ValueString() {
		return member, fmt.Errorf("updated membership user_role does not match the requested role")
	}
	return member, nil
}

// organizationMemberDiagnosticError deliberately discards all response-body,
// URL, request, and transport detail from centralized Client errors. Semantic
// contract errors created locally contain only fixed field/classification text.
func organizationMemberDiagnosticError(err error) string {
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

func (r *OrganizationMemberResource) readOrganizationMember(ctx context.Context, data *OrganizationMemberResourceModel) (bool, error) {
	exists, _, err := r.readOrganizationMemberWithBudgetConfirmation(ctx, data)
	return exists, err
}

func (r *OrganizationMemberResource) readOrganizationMemberWithBudgetConfirmation(ctx context.Context, data *OrganizationMemberResourceModel) (bool, bool, error) {
	return r.readOrganizationMemberWithNumericOwnership(ctx, data, false)
}

func (r *OrganizationMemberResource) readOrganizationMemberWithNumericOwnership(ctx context.Context, data *OrganizationMemberResourceModel, imported bool) (bool, bool, error) {
	userID, userEmail, err := organizationMemberIdentity(data)
	if err != nil {
		return false, false, err
	}
	organizationID := data.OrganizationID.ValueString()
	query := url.Values{"organization_id": []string{organizationID}}
	endpoint := endpointWithQuery("/organization/info", query)

	var response organizationInfoAPIResponse
	if err := r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
		return false, false, err
	}
	if response.OrganizationID != organizationID {
		return false, false, fmt.Errorf("organization info response organization_id does not match the requested organization")
	}
	if response.Members == nil {
		return false, false, fmt.Errorf("organization info response is missing the members array")
	}

	for _, member := range response.Members {
		if err := validateOrganizationMember(member); err != nil {
			return false, false, fmt.Errorf("organization info response contains an invalid member: %w", err)
		}
		if member.OrganizationID != organizationID {
			return false, false, fmt.Errorf("organization info response contains a member for another organization")
		}
		memberEmail := ""
		if member.UserEmail != nil {
			memberEmail = *member.UserEmail
		}
		if !matchOrganizationMember(member.UserID, memberEmail, userID, userEmail) {
			continue
		}
		budgetOwned := imported || (!data.MaxBudgetInOrganization.IsNull() && !data.MaxBudgetInOrganization.IsUnknown())
		budgetConfirmed, err := applyOrganizationMemberResponseWithNumericOwnership(data, member, budgetOwned)
		if err != nil {
			return false, budgetConfirmed, err
		}
		return true, budgetConfirmed, nil
	}
	return false, false, nil
}

func matchOrganizationMember(memberUserID, memberUserEmail, targetUserID, targetUserEmail string) bool {
	if targetUserID != "" {
		return memberUserID == targetUserID
	}
	if targetUserEmail != "" {
		return memberUserEmail == targetUserEmail
	}
	return false
}

// readOrganizationMemberWithEmailFallback mirrors member_add identity
// resolution when both inputs are configured. LiteLLM first looks up user_id,
// but if that user does not exist it can resolve an existing user_email to a
// different canonical ID. Normal lifecycle reads deliberately remain pinned to
// the canonical user_id; this fallback is only for create preflight/recovery.
func (r *OrganizationMemberResource) readOrganizationMemberWithEmailFallback(ctx context.Context, data *OrganizationMemberResourceModel) (bool, error) {
	exists, err := r.readOrganizationMember(ctx, data)
	if err != nil || exists {
		return exists, err
	}
	userID, userEmail, identityErr := organizationMemberIdentity(data)
	if identityErr != nil || userID == "" || userEmail == "" {
		return false, identityErr
	}
	byEmail := *data
	byEmail.UserID = types.StringNull()
	exists, err = r.readOrganizationMember(ctx, &byEmail)
	if err != nil || !exists {
		return exists, err
	}
	*data = byEmail
	return true, nil
}

func (r *OrganizationMemberResource) recoverOwnedOrganizationMember(ctx context.Context, owned OrganizationMemberResourceModel) (OrganizationMemberResourceModel, bool, error) {
	observed := owned
	exists, err := r.readOrganizationMemberWithEmailFallback(ctx, &observed)
	if err != nil || !exists {
		return owned, exists, err
	}
	return observed, true, nil
}

func (r *OrganizationMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data OrganizationMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, _, err := organizationMemberIdentity(&data); err != nil {
		resp.Diagnostics.AddError("Missing Member Identity", err.Error())
		return
	}

	// Refuse implicit adoption. A post-failure membership is deliberately not
	// adopted because LiteLLM provides no operation identity that proves whether
	// this request or a concurrent actor created it.
	preflight := data
	preexisting, err := r.readOrganizationMemberWithEmailFallback(ctx, &preflight)
	if err != nil {
		resp.Diagnostics.AddError("Organization Member Preflight Error", fmt.Sprintf("Unable to verify that the organization member does not already exist: %s", organizationMemberDiagnosticError(err)))
		return
	}
	if preexisting {
		resp.Diagnostics.AddError("Organization Member Already Exists", "The user is already a member of this organization. Import the membership before managing it with Terraform.")
		return
	}

	addRequest, err := buildOrganizationMemberAddRequest(&data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Member Identity", err.Error())
		return
	}
	var addResponse organizationMemberAddAPIResponse
	accepted, addErr := r.client.doRequestWithResponse(ctx, http.MethodPost, "/organization/member_add", addRequest, &addResponse)
	if addErr != nil && !accepted {
		postflight := data
		exists, verifyErr := r.readOrganizationMemberWithEmailFallback(ctx, &postflight)
		if verifyErr == nil && exists {
			resp.Diagnostics.AddError(
				"Ambiguous Organization Member Creation",
				"The add request failed, but the user is now present in the organization. The provider cannot prove who created the membership and did not adopt it. Verify ownership, then import it by organization_id:user_id.",
			)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add organization member: %s", organizationMemberDiagnosticError(addErr)))
		return
	}

	// A successful add is provider-owned even if LiteLLM's response is malformed.
	// Start from the known post-add contract: the membership exists with no
	// persisted member budget because v1.98.0 ignores that field on member_add.
	owned := data
	owned.MaxBudgetInOrganization = types.Float64Null()
	// Optional+Computed user_id is unknown in an email-only create plan. Once
	// the add is accepted, partial state must contain only known or null values;
	// a later Read can resolve this canonical identity from user_email.
	if owned.UserID.IsUnknown() {
		owned.UserID = types.StringNull()
	}
	var member organizationMemberAPIModel
	var structuralErr error
	var validationErr error
	if addErr == nil {
		member, structuralErr = validateOrganizationMemberAddResponseStructure(addResponse, &data)
		if structuralErr == nil {
			_, validationErr = validateOrganizationMemberAddResponse(addResponse, &data)
		} else {
			validationErr = structuralErr
		}
	}
	if addErr == nil && validationErr == nil {
		owned.UserID = types.StringValue(member.UserID)
		owned.ID = types.StringValue(fmt.Sprintf("%s:%s", owned.OrganizationID.ValueString(), member.UserID))
		owned.Role = types.StringValue(*member.UserRole)
	} else {
		switch {
		case addErr == nil && structuralErr == nil:
			// The response proves LiteLLM mutated canonical member B even when B
			// differs from requested ID A. Preserve B before recovery; retaining A
			// would make the next Read look up the wrong member and drop owned state.
			owned.UserID = types.StringValue(member.UserID)
			owned.ID = types.StringValue(fmt.Sprintf("%s:%s", owned.OrganizationID.ValueString(), member.UserID))
			owned.Role = types.StringValue(*member.UserRole)
		case !owned.UserEmail.IsNull() && !owned.UserEmail.IsUnknown() && owned.UserEmail.ValueString() != "":
			// A malformed or ambiguous response cannot prove any returned ID. Do
			// not guess from the requested ID when email remains a safe recovery
			// path; retain a known-null identity for the next Read.
			owned.UserID = types.StringNull()
			owned.ID = types.StringValue(fmt.Sprintf("%s:%s", owned.OrganizationID.ValueString(), owned.UserEmail.ValueString()))
		case !owned.UserID.IsNull() && !owned.UserID.IsUnknown() && owned.UserID.ValueString() != "":
			owned.ID = types.StringValue(fmt.Sprintf("%s:%s", owned.OrganizationID.ValueString(), owned.UserID.ValueString()))
		}
		recovered, _, recoverErr := r.recoverOwnedOrganizationMember(ctx, owned)
		if recoverErr == nil {
			owned = recovered
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &owned)...)
		responseErr := addErr
		if responseErr == nil {
			responseErr = validationErr
		}
		title := "Malformed Organization Member Add Response"
		detail := fmt.Sprintf("LiteLLM accepted the add, so the membership was retained in state, but its response did not match the v1.98.0 contract: %s", organizationMemberDiagnosticError(responseErr))
		if addErr == nil && structuralErr == nil {
			title = "Organization Member Add Verification Failed"
			detail = fmt.Sprintf("LiteLLM accepted the add and returned a structurally valid canonical membership, which was retained in state, but it did not match the requested configuration: %s", organizationMemberDiagnosticError(responseErr))
		}
		resp.Diagnostics.AddError(title, detail)
		return
	}

	budgetConfirmedByMutation := false
	budgetRequested := !data.MaxBudgetInOrganization.IsNull() && !data.MaxBudgetInOrganization.IsUnknown()
	if budgetRequested {
		budgetUpdate := owned
		budgetUpdate.MaxBudgetInOrganization = data.MaxBudgetInOrganization
		updateRequest, requestErr := buildOrganizationMemberUpdateRequest(&budgetUpdate)
		if requestErr != nil {
			resp.Diagnostics.Append(resp.State.Set(ctx, &owned)...)
			resp.Diagnostics.AddError("Invalid Member Identity", requestErr.Error())
			return
		}
		var updatedMember organizationMemberAPIModel
		updateAccepted, updateErr := r.client.doRequestWithResponse(ctx, http.MethodPatch, "/organization/member_update", updateRequest, &updatedMember)
		if updateErr == nil {
			if err := validateOrganizationMember(updatedMember); err != nil {
				updateErr = err
			} else if updatedMember.OrganizationID != owned.OrganizationID.ValueString() || updatedMember.UserID != owned.UserID.ValueString() {
				updateErr = fmt.Errorf("update response membership identity does not match the created membership")
			} else {
				// Apply onto the null post-add budget, not budgetUpdate (the plan).
				// Omission therefore remains unconfirmed until an authoritative
				// nested relation is returned by this response or the read-back.
				confirmed := owned
				budgetConfirmedByMutation, updateErr = applyOrganizationMemberResponseWithBudgetConfirmation(&confirmed, updatedMember)
				if updateErr == nil {
					// A present nested mutation response is authoritative even when it
					// proves LiteLLM stored a value different from the request.
					owned = confirmed
					if confirmed.Role.ValueString() != budgetUpdate.Role.ValueString() || (budgetConfirmedByMutation && !sameOrganizationMemberBudget(confirmed.MaxBudgetInOrganization, budgetUpdate.MaxBudgetInOrganization)) {
						updateErr = fmt.Errorf("update response did not confirm the requested role and max_budget_in_organization")
					}
				}
			}
		}
		if updateErr != nil {
			recovered, _, recoverErr := r.recoverOwnedOrganizationMember(ctx, owned)
			if recoverErr == nil {
				owned = recovered
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &owned)...)
			title := "Organization Member Budget Follow-up Error"
			if updateAccepted {
				title = "Malformed Organization Member Budget Response"
			}
			resp.Diagnostics.AddError(title, fmt.Sprintf("The membership was created and retained in state, but its max budget could not be confirmed through /organization/member_update: %s", organizationMemberDiagnosticError(updateErr)))
			return
		}
	}

	observed := owned
	exists, budgetConfirmedByRead, readErr := r.readOrganizationMemberWithBudgetConfirmation(ctx, &observed)
	if readErr != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &owned)...)
		resp.Diagnostics.AddError("Organization Member Read-Back Error", fmt.Sprintf("The membership was created and retained in state, but it could not be verified: %s", organizationMemberDiagnosticError(readErr)))
		return
	}
	if !exists {
		resp.Diagnostics.Append(resp.State.Set(ctx, &owned)...)
		resp.Diagnostics.AddError("Organization Member Missing After Create", "LiteLLM accepted the create request, but the user is not present in the organization's members array. The provider retained the owned identity in state.")
		return
	}
	budgetConfirmed := !budgetRequested || budgetConfirmedByMutation || budgetConfirmedByRead
	if observed.Role.ValueString() != data.Role.ValueString() || !budgetConfirmed || !sameOrganizationMemberBudget(observed.MaxBudgetInOrganization, data.MaxBudgetInOrganization) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &observed)...)
		detail := "The authoritative organization member role or nested budget does not match the requested value."
		if !budgetConfirmed {
			detail = "LiteLLM omitted litellm_budget_table from both the budget mutation response and the organization read-back, so the requested max budget could not be confirmed. The membership was retained with its last-known null budget."
		}
		resp.Diagnostics.AddError("Organization Member Create Verification Failed", detail)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &observed)...)
}

func (r *OrganizationMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OrganizationMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	importedMarker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(importedMarker) == "true"

	exists, _, err := r.readOrganizationMemberWithNumericOwnership(ctx, &data, imported)
	if err != nil {
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization member: %s", organizationMemberDiagnosticError(err)))
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && imported {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
}

func (r *OrganizationMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data OrganizationMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state OrganizationMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = state.ID
	data.UserID = state.UserID
	desiredRole := data.Role.ValueString()
	desiredBudget := data.MaxBudgetInOrganization
	budgetChanged := !sameOrganizationMemberBudget(state.MaxBudgetInOrganization, desiredBudget)
	if !state.MaxBudgetInOrganization.IsNull() && data.MaxBudgetInOrganization.IsNull() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError(
			"Unsupported Organization Member Budget Clear",
			"LiteLLM v1.98.0 ignores max_budget_in_organization=null on /organization/member_update, so the provider did not send a mutation that would falsely report a clear. Replace the membership to remove its effective member budget.",
		)
		return
	}

	updateRequest, err := buildOrganizationMemberUpdateRequest(&data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Member Identity", err.Error())
		return
	}
	var updatedMember organizationMemberAPIModel
	accepted, updateErr := r.client.doRequestWithResponse(ctx, http.MethodPatch, "/organization/member_update", updateRequest, &updatedMember)
	// Keep the last-known state independent from the requested plan. Responses
	// that omit litellm_budget_table may update other observed fields, but can
	// only preserve this prior budget and never initialize it from desiredBudget.
	reconciliationBase := state
	budgetConfirmedByMutation := false
	if updateErr == nil {
		if err := validateOrganizationMember(updatedMember); err != nil {
			updateErr = err
		} else if updatedMember.OrganizationID != data.OrganizationID.ValueString() || updatedMember.UserID != data.UserID.ValueString() {
			updateErr = fmt.Errorf("update response membership identity does not match the requested membership")
		} else {
			confirmed := reconciliationBase
			budgetConfirmedByMutation, updateErr = applyOrganizationMemberResponseWithBudgetConfirmation(&confirmed, updatedMember)
			if updateErr == nil {
				// Preserve every valid authoritative mutation field before testing
				// whether it matched the plan. A later organization-info response
				// may omit the nested budget and must start from this confirmation.
				reconciliationBase = confirmed
				if confirmed.Role.ValueString() != desiredRole || (budgetConfirmedByMutation && !sameOrganizationMemberBudget(confirmed.MaxBudgetInOrganization, desiredBudget)) {
					updateErr = fmt.Errorf("update response did not confirm the requested role and max_budget_in_organization")
				}
			}
		}
	}
	if updateErr != nil {
		// A transport failure can be ambiguous even before an HTTP status is
		// available, and a 2xx response can be malformed after the mutation was
		// committed. Reconcile from any valid mutation confirmation so omitted
		// organization-info fields cannot restore stale prior state.
		observed := reconciliationBase
		exists, _, readErr := r.readOrganizationMemberWithBudgetConfirmation(ctx, &observed)
		switch {
		case readErr == nil && exists:
			resp.Diagnostics.Append(resp.State.Set(ctx, &observed)...)
		case readErr == nil && !exists:
			resp.State.RemoveResource(ctx)
		case IsAPIErrorStatus(readErr, http.StatusNotFound):
			resp.State.RemoveResource(ctx)
		default:
			resp.Diagnostics.Append(resp.State.Set(ctx, &reconciliationBase)...)
		}
		detail := fmt.Sprintf("Unable to confirm organization member update: %s", organizationMemberDiagnosticError(updateErr))
		if readErr != nil && !IsAPIErrorStatus(readErr, http.StatusNotFound) {
			detail = fmt.Sprintf("%s. State reconciliation also failed: %s", detail, organizationMemberDiagnosticError(readErr))
		}
		if accepted {
			detail = "LiteLLM accepted the update, but its response could not be confirmed. " + detail
		}
		resp.Diagnostics.AddError("Organization Member Update Error", detail)
		return
	}

	observed := reconciliationBase
	exists, budgetConfirmedByRead, readErr := r.readOrganizationMemberWithBudgetConfirmation(ctx, &observed)
	if readErr != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &reconciliationBase)...)
		resp.Diagnostics.AddError("Organization Member Read-Back Error", fmt.Sprintf("The membership was updated and retained in state, but it could not be verified: %s", organizationMemberDiagnosticError(readErr)))
		return
	}
	if !exists {
		resp.State.RemoveResource(ctx)
		resp.Diagnostics.AddError("Organization Member Missing After Update", "LiteLLM accepted the update request, but the user is no longer present in the organization's members array.")
		return
	}
	budgetConfirmed := !budgetChanged || budgetConfirmedByMutation || budgetConfirmedByRead
	if observed.Role.ValueString() != desiredRole || !budgetConfirmed || !sameOrganizationMemberBudget(observed.MaxBudgetInOrganization, desiredBudget) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &observed)...)
		detail := "The authoritative organization member role or nested budget does not match the requested value."
		if !budgetConfirmed {
			detail = "LiteLLM omitted litellm_budget_table from both the mutation response and the organization read-back, so the requested max budget change could not be confirmed. The membership was retained with its last-known budget."
		}
		resp.Diagnostics.AddError("Organization Member Update Verification Failed", detail)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &observed)...)
}

func sameOrganizationMemberBudget(observed, planned types.Float64) bool {
	if planned.IsUnknown() || observed.IsUnknown() {
		return false
	}
	if planned.IsNull() {
		return observed.IsNull()
	}
	return !observed.IsNull() && observed.ValueFloat64() == planned.ValueFloat64()
}

func (r *OrganizationMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OrganizationMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	deleteRequest, err := buildOrganizationMemberDeleteRequest(&data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Member Identity", err.Error())
		return
	}
	if err := r.client.DoRequestWithResponse(ctx, http.MethodDelete, "/organization/member_delete", deleteRequest, nil); err != nil {
		if !IsAPIErrorStatus(err, http.StatusNotFound) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to remove organization member: %s", organizationMemberDiagnosticError(err)))
		}
	}
}

func (r *OrganizationMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Only the canonical organization_id:user_id form is accepted. Email lookup
	// imports are intentionally unsupported because v1.98.0 exposes no distinct
	// email import grammar and membership identity is the user_id composite key.
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			"Expected a non-empty import ID in format 'organization_id:user_id'.",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), parts[1])...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
	}
}
