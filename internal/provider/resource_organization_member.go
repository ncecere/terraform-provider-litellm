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

var _ resource.Resource = &OrganizationMemberResource{}
var _ resource.ResourceWithImportState = &OrganizationMemberResource{}

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

func (r *OrganizationMemberResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_member"
}

func (r *OrganizationMemberResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a member of a LiteLLM organization. If the user doesn't exist, a new user row will be created.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this membership (organization_id:user_id).",
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
				Description: "The user ID to add to the organization. Either user_id or user_email must be provided.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_email": schema.StringAttribute{
				Description: "The user email to add to the organization. Either user_id or user_email must be provided.",
				Optional:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"role": schema.StringAttribute{
				Description: "The role of the member in the organization.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.OneOf(
						"proxy_admin",
						"proxy_admin_viewer",
						"internal_user",
						"internal_user_viewer",
						"org_admin",
					),
				},
			},
			"max_budget_in_organization": schema.Float64Attribute{
				Description: "Maximum budget for this user within the organization.",
				Optional:    true,
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

func (r *OrganizationMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data OrganizationMemberResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Validate that either user_id or user_email is provided
	if data.UserID.IsNull() && data.UserEmail.IsNull() {
		resp.Diagnostics.AddError(
			"Missing Required Attribute",
			"Either user_id or user_email must be provided.",
		)
		return
	}

	// Build member object
	member := map[string]interface{}{
		"role": data.Role.ValueString(),
	}

	if !data.UserID.IsNull() && data.UserID.ValueString() != "" {
		member["user_id"] = data.UserID.ValueString()
	}
	if !data.UserEmail.IsNull() && data.UserEmail.ValueString() != "" {
		member["user_email"] = data.UserEmail.ValueString()
	}

	addReq := map[string]interface{}{
		"organization_id": data.OrganizationID.ValueString(),
		"member":          member,
	}

	if !data.MaxBudgetInOrganization.IsNull() && !data.MaxBudgetInOrganization.IsUnknown() {
		addReq["max_budget_in_organization"] = data.MaxBudgetInOrganization.ValueFloat64()
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/organization/member_add", addReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add organization member: %s", err))
		return
	}

	// Try to get user_id from response if we used email
	if data.UserID.IsNull() || data.UserID.ValueString() == "" {
		if userID, ok := result["user_id"].(string); ok {
			data.UserID = types.StringValue(userID)
		}
	}

	// Set the ID
	userID := data.UserID.ValueString()
	if userID == "" && !data.UserEmail.IsNull() {
		userID = data.UserEmail.ValueString()
	}
	data.ID = types.StringValue(fmt.Sprintf("%s:%s", data.OrganizationID.ValueString(), userID))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OrganizationMemberResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get organization info and check if user is a member
	orgID := data.OrganizationID.ValueString()
	endpoint := fmt.Sprintf("/organization/info?organization_id=%s", orgID)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization: %s", err))
		return
	}

	// Check if user is still a member.
	// If user_id was not known at create time (email-only workflow), match by user_email and hydrate user_id.
	userID := ""
	if !data.UserID.IsNull() && !data.UserID.IsUnknown() {
		userID = data.UserID.ValueString()
	}
	userEmail := ""
	if !data.UserEmail.IsNull() && !data.UserEmail.IsUnknown() {
		userEmail = data.UserEmail.ValueString()
	}
	found := false

	if members, ok := result["members"].([]interface{}); ok {
		for _, m := range members {
			if memberMap, ok := m.(map[string]interface{}); ok {
				memberUserID, _ := memberMap["user_id"].(string)
				memberUserEmail, _ := memberMap["user_email"].(string)

				if matchOrganizationMember(memberUserID, memberUserEmail, userID, userEmail) {
					found = true
					if memberUserID != "" {
						data.UserID = types.StringValue(memberUserID)
					}
					if memberUserEmail != "" {
						data.UserEmail = types.StringValue(memberUserEmail)
					}
					if role, ok := memberMap["role"].(string); ok {
						data.Role = types.StringValue(role)
					}
					if maxBudget, ok := memberMap["max_budget_in_organization"].(float64); ok {
						data.MaxBudgetInOrganization = types.Float64Value(maxBudget)
					}
					break
				}
			}
		}
	}

	if !found {
		// User is no longer a member
		resp.State.RemoveResource(ctx)
		return
	}

	memberIdentifier := ""
	if !data.UserID.IsNull() && !data.UserID.IsUnknown() && data.UserID.ValueString() != "" {
		memberIdentifier = data.UserID.ValueString()
	} else if !data.UserEmail.IsNull() && !data.UserEmail.IsUnknown() {
		memberIdentifier = data.UserEmail.ValueString()
	}
	if memberIdentifier != "" {
		data.ID = types.StringValue(fmt.Sprintf("%s:%s", data.OrganizationID.ValueString(), memberIdentifier))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
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

	// Preserve ID
	data.ID = state.ID
	data.UserID = state.UserID

	// Build member update request
	member := map[string]interface{}{
		"role": data.Role.ValueString(),
	}

	if !data.UserID.IsNull() && data.UserID.ValueString() != "" {
		member["user_id"] = data.UserID.ValueString()
	}

	updateReq := map[string]interface{}{
		"organization_id": data.OrganizationID.ValueString(),
		"member":          member,
	}

	if !data.MaxBudgetInOrganization.IsNull() && !data.MaxBudgetInOrganization.IsUnknown() {
		updateReq["max_budget_in_organization"] = data.MaxBudgetInOrganization.ValueFloat64()
	}

	if err := r.client.DoRequestWithResponse(ctx, "PATCH", "/organization/member_update", updateReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update organization member: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OrganizationMemberResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := map[string]interface{}{
		"organization_id": data.OrganizationID.ValueString(),
		"user_id":         data.UserID.ValueString(),
	}

	if err := r.client.DoRequestWithResponse(ctx, "DELETE", "/organization/member_delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to remove organization member: %s", err))
			return
		}
	}
}

func (r *OrganizationMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import format: organization_id:user_id
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Expected import ID in format 'organization_id:user_id', got: %s", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), parts[1])...)
}
