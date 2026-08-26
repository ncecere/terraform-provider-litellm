package provider

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &UserResource{}
var _ resource.ResourceWithImportState = &UserResource{}

func NewUserResource() resource.Resource {
	return &UserResource{}
}

type UserResource struct {
	client *Client
}

type UserResourceModel struct {
	ID              types.String  `tfsdk:"id"`
	UserID          types.String  `tfsdk:"user_id"`
	UserAlias       types.String  `tfsdk:"user_alias"`
	UserEmail       types.String  `tfsdk:"user_email"`
	UserRole        types.String  `tfsdk:"user_role"`
	Teams           types.List    `tfsdk:"teams"`
	Models          types.List    `tfsdk:"models"`
	MaxBudget       types.Float64 `tfsdk:"max_budget"`
	BudgetDuration  types.String  `tfsdk:"budget_duration"`
	TPMLimit        types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit        types.Int64   `tfsdk:"rpm_limit"`
	AutoCreateKey   types.Bool    `tfsdk:"auto_create_key"`
	SendInviteEmail types.Bool    `tfsdk:"send_invite_email"`
	Metadata        types.Map     `tfsdk:"metadata"`
	Key             types.String  `tfsdk:"key"`
}

func (r *UserResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *UserResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM internal user. Internal users can access the LiteLLM Admin UI to manage keys and request access to models.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this user (same as user_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"user_id": schema.StringAttribute{
				Description: "The user ID. If not specified, one will be generated.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_alias": schema.StringAttribute{
				Description: "A descriptive name for the user.",
				Optional:    true,
			},
			"user_email": schema.StringAttribute{
				Description: "The user's email address.",
				Optional:    true,
			},
			"user_role": schema.StringAttribute{
				Description: "The user's role. LiteLLM v1.98 user create/update accepts proxy_admin, proxy_admin_viewer, internal_user, or internal_user_viewer.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.OneOf(
						"proxy_admin",
						"proxy_admin_viewer",
						"internal_user",
						"internal_user_viewer",
					),
				},
			},
			"teams": schema.ListAttribute{
				Description: "List of team IDs the user belongs to. Membership order is not significant.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.UniqueValues(),
				},
			},
			"models": schema.ListAttribute{
				Description: "Model names the user is allowed to call. Set to ['no-default-models'] to block all model access.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"max_budget": schema.Float64Attribute{
				Description: "Maximum budget for the user.",
				Optional:    true,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Budget reset duration (e.g., '30s', '30m', '30h', '30d', '1mo').",
				Optional:    true,
			},
			"tpm_limit": schema.Int64Attribute{
				Description: "Tokens per minute limit for the user.",
				Optional:    true,
			},
			"rpm_limit": schema.Int64Attribute{
				Description: "Requests per minute limit for the user.",
				Optional:    true,
			},
			"auto_create_key": schema.BoolAttribute{
				Description: "Whether to auto-create an API key for the user. Default is true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"send_invite_email": schema.BoolAttribute{
				Description: "Create-only action flag that asks LiteLLM to asynchronously email this user. Requires user_email. It is write-only, is never sent during Update, and does not confirm delivery.",
				Optional:    true,
				WriteOnly:   true,
			},
			"metadata": schema.MapAttribute{
				Description: "Metadata for the user.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"key": schema.StringAttribute{
				Description: "The auto-generated API key for the user (if auto_create_key is true).",
				Computed:    true,
				Sensitive:   true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *UserResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := configuredClient(req.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *UserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data UserResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var sendInviteEmail types.Bool
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("send_invite_email"), &sendInviteEmail)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateUserSendInviteEmail(&data, sendInviteEmail); err != nil {
		resp.Diagnostics.AddError("Invalid User Invitation", err.Error())
		return
	}

	userReq := r.buildUserRequest(ctx, &data)
	if err := addSendInviteEmailToCreateRequest(userReq, sendInviteEmail); err != nil {
		resp.Diagnostics.AddError("Invalid User Invitation", err.Error())
		return
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/user/new", userReq, &result); err != nil {
		if IsAPIErrorStatus(err, 409) {
			if sendInviteEmailRequested(sendInviteEmail) {
				resp.Diagnostics.AddError(
					"Existing User Was Not Invited",
					"LiteLLM reported that the user already exists, so no create-time invitation was sent and Terraform did not adopt the account. Import the exact existing user with send_invite_email omitted, or invite the user outside this resource.",
				)
				return
			}
			mutated, adoptErr := r.adoptExistingUser(ctx, &data)
			if adoptErr != nil {
				if mutated {
					resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
				}
				resp.Diagnostics.AddError("User Adoption Error", fmt.Sprintf("LiteLLM reported that the user already exists, but the provider could not safely adopt an exact matching account: %s", adoptErr))
				return
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create user: %s", err))
		return
	}

	// Extract user_id from response
	if userID, ok := result["user_id"].(string); ok {
		data.UserID = types.StringValue(userID)
		data.ID = types.StringValue(userID)
	}

	// Extract key if created
	if key, ok := result["key"].(string); ok {
		data.Key = types.StringValue(key)
	}

	// Read back for full state
	if err := r.readUser(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("User created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *UserResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data UserResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	importedMarker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(importedMarker) == "true"

	if err := r.readUserWithNumericOwnership(ctx, &data, imported); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read user: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && imported {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
}

func (r *UserResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data UserResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state UserResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve IDs and key
	data.ID = state.ID
	data.UserID = state.UserID
	data.Key = state.Key

	userReq := r.buildUserRequest(ctx, &data)
	userReq["user_id"] = data.UserID.ValueString()
	// LiteLLM v1.98 accepts teams on /user/update but does not reconcile team
	// membership there. Manage membership through the dedicated team endpoints.
	delete(userReq, "teams")

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/user/update", userReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update user: %s", err))
		return
	}

	teamsChanged := userTeamMembershipsDiffer(state.Teams, data.Teams)
	if teamsChanged {
		if err := r.reconcileUserTeams(ctx, data.UserID.ValueString(), state.Teams, data.Teams); err != nil {
			resp.Diagnostics.AddError("Team Membership Error", fmt.Sprintf("Unable to update user team membership: %s", err))
			return
		}
		if err := r.readUserTeamsAfterUpdate(ctx, &data, 8); err != nil {
			resp.Diagnostics.AddError("User Team Update Not Yet Consistent", fmt.Sprintf("LiteLLM accepted the team membership update but did not return the planned membership before the consistency timeout: %s", err))
			return
		}
	} else if err := r.readUser(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("User updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *UserResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data UserResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := map[string]interface{}{
		"user_ids": []string{data.UserID.ValueString()},
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/user/delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete user: %s", err))
			return
		}
	}
}

func (r *UserResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), req.ID)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
	}
}

type userListResponse struct {
	Users      []map[string]interface{} `json:"users"`
	TotalPages int                      `json:"total_pages"`
}

func (r *UserResource) findExistingUserByExactEmail(ctx context.Context, email string) (string, error) {
	if email == "" {
		return "", fmt.Errorf("user_email must be configured to adopt an existing user")
	}

	matches := make(map[string]struct{})
	totalPages := 1
	for page := 1; page <= totalPages; page++ {
		if page > 100 {
			return "", fmt.Errorf("user lookup exceeded 100 pages")
		}
		endpoint := fmt.Sprintf("/user/list?user_email=%s&page=%d&page_size=100", url.QueryEscape(email), page)
		var response userListResponse
		if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &response); err != nil {
			return "", fmt.Errorf("unable to list users by email: %w", err)
		}
		if response.TotalPages > totalPages {
			totalPages = response.TotalPages
		}
		if totalPages > 100 {
			return "", fmt.Errorf("user lookup returned %d pages, exceeding the safe lookup limit", totalPages)
		}
		for _, user := range response.Users {
			candidateEmail, emailOK := user["user_email"].(string)
			candidateID, idOK := user["user_id"].(string)
			if emailOK && candidateEmail == email && idOK && candidateID != "" {
				matches[candidateID] = struct{}{}
			}
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("no user with the exact email %q was returned by /user/list", email)
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("multiple user IDs matched the exact email %q", email)
	}
	for userID := range matches {
		return userID, nil
	}
	return "", fmt.Errorf("exact user lookup returned no usable user ID")
}

func (r *UserResource) adoptExistingUser(ctx context.Context, data *UserResourceModel) (bool, error) {
	if data.UserEmail.IsNull() || data.UserEmail.IsUnknown() || data.UserEmail.ValueString() == "" {
		return false, fmt.Errorf("user_email must be known and non-empty")
	}
	planned := *data
	email := planned.UserEmail.ValueString()
	userID, err := r.findExistingUserByExactEmail(ctx, email)
	if err != nil {
		return false, err
	}
	if !planned.UserID.IsNull() && !planned.UserID.IsUnknown() && planned.UserID.ValueString() != "" && planned.UserID.ValueString() != userID {
		return false, fmt.Errorf("the exact email match has user_id %q, not the configured user_id %q", userID, planned.UserID.ValueString())
	}

	// Clear the pre-seeded identity fields so verification only succeeds when
	// /user/info explicitly returns both values.
	current := planned
	current.ID = types.StringValue(userID)
	current.UserID = types.StringNull()
	current.UserEmail = types.StringNull()
	current.Key = types.StringNull()
	if err := r.readUser(ctx, &current); err != nil {
		return false, fmt.Errorf("unable to verify the existing user: %w", err)
	}
	if current.UserID.IsNull() || current.UserID.IsUnknown() || current.UserEmail.IsNull() || current.UserEmail.IsUnknown() || current.UserID.ValueString() != userID || current.UserEmail.ValueString() != email {
		return false, fmt.Errorf("the user identity was missing or changed during verification")
	}
	if !planned.UserAlias.IsNull() && !planned.UserAlias.IsUnknown() && planned.UserAlias.ValueString() == "" && !current.UserAlias.IsNull() && current.UserAlias.ValueString() != "" {
		return false, fmt.Errorf("LiteLLM cannot clear the existing user_alias during adoption")
	}
	if !planned.Models.IsNull() && !planned.Models.IsUnknown() && len(planned.Models.Elements()) == 0 && !current.Models.IsNull() && !current.Models.IsUnknown() && len(current.Models.Elements()) > 0 {
		return false, fmt.Errorf("LiteLLM cannot clear the existing models list during adoption")
	}
	if !planned.Metadata.IsNull() && !planned.Metadata.IsUnknown() && len(planned.Metadata.Elements()) == 0 && !current.Metadata.IsNull() && !current.Metadata.IsUnknown() && len(current.Metadata.Elements()) > 0 {
		return false, fmt.Errorf("LiteLLM cannot clear the existing metadata map during adoption")
	}

	prepareRecoverableState := func(refresh bool) {
		*data = planned
		data.ID = types.StringValue(userID)
		data.UserID = types.StringValue(userID)
		data.Key = types.StringNull()
		if refresh {
			if err := r.readUser(ctx, data); err == nil {
				return
			}
		}
		if data.Teams.IsUnknown() {
			data.Teams = current.Teams
		}
		if data.Models.IsUnknown() {
			data.Models = current.Models
		}
		if data.Metadata.IsUnknown() {
			data.Metadata = current.Metadata
		}
	}
	prepareRecoverableState(false)

	updateRequest := r.buildUserRequest(ctx, &planned)
	updateRequest["user_id"] = userID
	delete(updateRequest, "teams")
	// auto_create_key is a Create action. Do not generate an inaccessible new
	// key while adopting an account whose existing raw keys cannot be read back.
	delete(updateRequest, "auto_create_key")
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/user/update", updateRequest, nil); err != nil {
		prepareRecoverableState(true)
		return true, fmt.Errorf("unable to converge the existing user: %w", err)
	}

	teamsChanged := userTeamMembershipsDiffer(current.Teams, planned.Teams)
	if teamsChanged {
		if err := r.reconcileUserTeams(ctx, userID, current.Teams, planned.Teams); err != nil {
			prepareRecoverableState(true)
			return true, fmt.Errorf("unable to converge existing user team membership: %w", err)
		}
	}

	prepareRecoverableState(false)
	if teamsChanged {
		if err := r.readUserTeamsAfterUpdate(ctx, data, 8); err != nil {
			prepareRecoverableState(true)
			return true, fmt.Errorf("existing user team membership did not converge: %w", err)
		}
	} else if err := r.readUser(ctx, data); err != nil {
		prepareRecoverableState(false)
		return true, fmt.Errorf("unable to read the adopted user: %w", err)
	}
	return true, nil
}

func (r *UserResource) buildUserRequest(ctx context.Context, data *UserResourceModel) map[string]interface{} {
	userReq := map[string]interface{}{}

	// String fields - check IsNull, IsUnknown, and empty string
	if !data.UserID.IsNull() && !data.UserID.IsUnknown() && data.UserID.ValueString() != "" {
		userReq["user_id"] = data.UserID.ValueString()
	}
	if !data.UserAlias.IsNull() && !data.UserAlias.IsUnknown() && data.UserAlias.ValueString() != "" {
		userReq["user_alias"] = data.UserAlias.ValueString()
	}
	if !data.UserEmail.IsNull() && !data.UserEmail.IsUnknown() && data.UserEmail.ValueString() != "" {
		userReq["user_email"] = data.UserEmail.ValueString()
	}
	if !data.UserRole.IsNull() && !data.UserRole.IsUnknown() && data.UserRole.ValueString() != "" {
		userReq["user_role"] = data.UserRole.ValueString()
	}
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() && data.BudgetDuration.ValueString() != "" {
		userReq["budget_duration"] = data.BudgetDuration.ValueString()
	}

	// Numeric fields - check IsNull and IsUnknown
	if !data.MaxBudget.IsNull() && !data.MaxBudget.IsUnknown() {
		userReq["max_budget"] = data.MaxBudget.ValueFloat64()
	}
	if !data.TPMLimit.IsNull() && !data.TPMLimit.IsUnknown() {
		userReq["tpm_limit"] = data.TPMLimit.ValueInt64()
	}
	if !data.RPMLimit.IsNull() && !data.RPMLimit.IsUnknown() {
		userReq["rpm_limit"] = data.RPMLimit.ValueInt64()
	}

	// Boolean fields - check IsNull and IsUnknown (auto_create_key has default)
	if !data.AutoCreateKey.IsNull() && !data.AutoCreateKey.IsUnknown() {
		userReq["auto_create_key"] = data.AutoCreateKey.ValueBool()
	}

	// List fields - check IsNull, IsUnknown, and len > 0
	if !data.Teams.IsNull() && !data.Teams.IsUnknown() {
		var teams []string
		data.Teams.ElementsAs(ctx, &teams, false)
		if len(teams) > 0 {
			userReq["teams"] = teams
		}
	}

	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		var models []string
		data.Models.ElementsAs(ctx, &models, false)
		userReq["models"] = models
	}

	// Send explicitly configured empty metadata so existing values can be
	// cleared during Update or adoption.
	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var metadata map[string]string
		data.Metadata.ElementsAs(ctx, &metadata, false)
		userReq["metadata"] = metadata
	}

	return userReq
}

func (r *UserResource) readUser(ctx context.Context, data *UserResourceModel) error {
	return r.readUserWithNumericOwnership(ctx, data, false)
}

func (r *UserResource) readUserWithNumericOwnership(ctx context.Context, data *UserResourceModel, imported bool) error {
	userID := data.UserID.ValueString()
	if userID == "" {
		userID = data.ID.ValueString()
	}

	endpoint := fmt.Sprintf("/user/info?user_id=%s", userID)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// The /user/info endpoint returns user_info nested.
	userInfo := result
	if ui, ok := result["user_info"].(map[string]interface{}); ok {
		userInfo = ui
	}
	if imported {
		validated, err := requireImportedObjectField(true, "user", result, "user_info")
		if err != nil {
			return err
		}
		userInfo = validated
	}
	if err := validateImportedObjectIdentity(imported, "user", userInfo, "user_id", userID); err != nil {
		return err
	}

	// Update fields from response
	if userID, ok := userInfo["user_id"].(string); ok {
		data.UserID = types.StringValue(userID)
		data.ID = types.StringValue(userID)
	}
	if alias, ok := userInfo["user_alias"].(string); ok && !data.UserAlias.IsNull() {
		data.UserAlias = types.StringValue(alias)
	}
	if email, ok := userInfo["user_email"].(string); ok {
		data.UserEmail = types.StringValue(email)
	}
	if role, ok := userInfo["user_role"].(string); ok && !data.UserRole.IsNull() {
		data.UserRole = types.StringValue(role)
	}
	if budgetDuration, ok := userInfo["budget_duration"].(string); ok && !data.BudgetDuration.IsNull() {
		data.BudgetDuration = types.StringValue(budgetDuration)
	}

	// Numeric fields are Optional-only: validate every present API value, but
	// do not adopt server defaults for unconfigured attributes. Null/omission
	// clears configured state so out-of-band removals remain visible.
	maxBudgetOwned := imported || (!data.MaxBudget.IsNull() && !data.MaxBudget.IsUnknown())
	if err := updateFloat64FromAPI(&data.MaxBudget, userInfo, maxBudgetOwned, maxBudgetOwned, "max_budget"); err != nil {
		return err
	}
	tpmOwned := imported || (!data.TPMLimit.IsNull() && !data.TPMLimit.IsUnknown())
	if err := updateInt64FromAPI(&data.TPMLimit, userInfo, tpmOwned, tpmOwned, "tpm_limit"); err != nil {
		return err
	}
	rpmOwned := imported || (!data.RPMLimit.IsNull() && !data.RPMLimit.IsUnknown())
	if err := updateInt64FromAPI(&data.RPMLimit, userInfo, rpmOwned, rpmOwned, "rpm_limit"); err != nil {
		return err
	}

	// Team membership is unordered in LiteLLM. Preserve Terraform's current
	// ordering when the API returns the same members in a different order; when
	// membership truly changes, use a stable canonical order so drift remains
	// visible without positional churn.
	if teams, ok := userInfo["teams"].([]interface{}); ok {
		data.Teams = reconcileUnorderedUserTeams(data.Teams, teams)
	}

	// Handle models list - preserve null when API returns empty and config didn't specify models
	if models, ok := userInfo["models"].([]interface{}); ok && len(models) > 0 {
		modelsList := make([]attr.Value, len(models))
		for i, m := range models {
			if str, ok := m.(string); ok {
				modelsList[i] = types.StringValue(str)
			}
		}
		data.Models, _ = types.ListValue(types.StringType, modelsList)
	} else if !data.Models.IsNull() {
		// User specified models in config but API returned empty — set to empty list
		data.Models, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle metadata map - preserve null when API returns empty and config didn't specify metadata
	if metadata, ok := userInfo["metadata"].(map[string]interface{}); ok && len(metadata) > 0 {
		metaMap := make(map[string]attr.Value)
		for k, v := range metadata {
			if str, ok := v.(string); ok {
				metaMap[k] = types.StringValue(str)
			}
		}
		data.Metadata, _ = types.MapValue(types.StringType, metaMap)
	} else if !data.Metadata.IsNull() {
		// User specified metadata in config but API returned empty — set to empty map
		data.Metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	return nil
}

func reconcileUnorderedUserTeams(current types.List, rawTeams []interface{}) types.List {
	remoteTeams := make([]string, 0, len(rawTeams))
	for _, rawTeam := range rawTeams {
		if team, ok := rawTeam.(string); ok {
			remoteTeams = append(remoteTeams, team)
		}
	}

	if len(remoteTeams) == 0 {
		if current.IsNull() {
			return current
		}
		return types.ListValueMust(types.StringType, []attr.Value{})
	}
	if userTeamMembershipEqual(current, remoteTeams) {
		return current
	}

	sort.Strings(remoteTeams)
	values := make([]attr.Value, len(remoteTeams))
	for i, team := range remoteTeams {
		values[i] = types.StringValue(team)
	}
	return types.ListValueMust(types.StringType, values)
}

func userTeamMembershipEqual(current types.List, remoteTeams []string) bool {
	if current.IsNull() || current.IsUnknown() || len(current.Elements()) != len(remoteTeams) {
		return false
	}

	counts := make(map[string]int, len(remoteTeams))
	for _, team := range remoteTeams {
		counts[team]++
	}
	for _, element := range current.Elements() {
		team, ok := element.(types.String)
		if !ok || team.IsNull() || team.IsUnknown() || counts[team.ValueString()] == 0 {
			return false
		}
		counts[team.ValueString()]--
	}
	return true
}

func userTeamIDs(value types.List) ([]string, bool) {
	if value.IsNull() || value.IsUnknown() {
		return nil, false
	}
	ids := make([]string, 0, len(value.Elements()))
	for _, element := range value.Elements() {
		team, ok := element.(types.String)
		if !ok || team.IsNull() || team.IsUnknown() {
			return nil, false
		}
		ids = append(ids, team.ValueString())
	}
	return ids, true
}

func userTeamMembershipsDiffer(current, planned types.List) bool {
	plannedIDs, managed := userTeamIDs(planned)
	if !managed {
		return false
	}
	return !userTeamMembershipEqual(current, plannedIDs)
}

func (r *UserResource) reconcileUserTeams(ctx context.Context, userID string, current, planned types.List) error {
	desiredIDs, managed := userTeamIDs(planned)
	if !managed {
		return nil
	}
	currentIDs, _ := userTeamIDs(current)

	desired := make(map[string]struct{}, len(desiredIDs))
	for _, teamID := range desiredIDs {
		desired[teamID] = struct{}{}
	}
	existing := make(map[string]struct{}, len(currentIDs))
	for _, teamID := range currentIDs {
		existing[teamID] = struct{}{}
	}

	removals := make([]string, 0)
	for teamID := range existing {
		if _, keep := desired[teamID]; !keep {
			removals = append(removals, teamID)
		}
	}
	additions := make([]string, 0)
	for teamID := range desired {
		if _, present := existing[teamID]; !present {
			additions = append(additions, teamID)
		}
	}
	sort.Strings(removals)
	sort.Strings(additions)

	// Add destinations before removing old memberships. A failed destination
	// must not revoke existing access or delete LiteLLM team-scoped keys. If a
	// later removal fails, refresh exposes the temporary extra membership and a
	// subsequent apply can safely retry the removal.
	for _, teamID := range additions {
		request := map[string]interface{}{
			"team_id": teamID,
			"member": map[string]interface{}{
				"role":    "user",
				"user_id": userID,
			},
		}
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_add", request, nil); err != nil {
			return fmt.Errorf("add user to team %s: %w", teamID, err)
		}
	}
	for _, teamID := range removals {
		request := map[string]interface{}{"team_id": teamID, "user_id": userID}
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_delete", request, nil); err != nil {
			return fmt.Errorf("remove user from team %s: %w", teamID, err)
		}
	}
	return nil
}

func (r *UserResource) readUserTeamsAfterUpdate(ctx context.Context, data *UserResourceModel, maxRetries int) error {
	if maxRetries < 1 {
		return fmt.Errorf("maxRetries must be at least 1")
	}

	expected := data.Teams
	delay := 250 * time.Millisecond
	maxDelay := 2 * time.Second
	consecutiveMatches := 0
	for attempt := 0; attempt < maxRetries; attempt++ {
		candidate := *data
		err := r.readUser(ctx, &candidate)
		if err == nil {
			if !userTeamMembershipsDiffer(candidate.Teams, expected) {
				consecutiveMatches++
				if consecutiveMatches >= 2 {
					*data = candidate
					return nil
				}
			} else {
				consecutiveMatches = 0
			}
		} else if !IsNotFoundError(err) {
			return err
		} else {
			consecutiveMatches = 0
		}

		if attempt == maxRetries-1 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
	return fmt.Errorf("team membership did not remain at its planned value after %d reads", maxRetries)
}
