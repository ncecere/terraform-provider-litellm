package provider

import (
	"context"
	"fmt"

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
	ID             types.String  `tfsdk:"id"`
	UserID         types.String  `tfsdk:"user_id"`
	UserAlias      types.String  `tfsdk:"user_alias"`
	UserEmail      types.String  `tfsdk:"user_email"`
	UserRole       types.String  `tfsdk:"user_role"`
	Teams          types.List    `tfsdk:"teams"`
	Models         types.List    `tfsdk:"models"`
	MaxBudget      types.Float64 `tfsdk:"max_budget"`
	BudgetDuration types.String  `tfsdk:"budget_duration"`
	TPMLimit       types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit       types.Int64   `tfsdk:"rpm_limit"`
	AutoCreateKey  types.Bool    `tfsdk:"auto_create_key"`
	Metadata       types.Map     `tfsdk:"metadata"`
	Spend          types.Float64 `tfsdk:"spend"`
	Key            types.String  `tfsdk:"key"`
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
				Description: "The user's role: proxy_admin, proxy_admin_viewer, internal_user, internal_user_viewer, team, customer.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.OneOf(
						"proxy_admin",
						"proxy_admin_viewer",
						"internal_user",
						"internal_user_viewer",
						"team",
						"customer",
					),
				},
			},
			"teams": schema.ListAttribute{
				Description: "List of team IDs the user belongs to.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"models": schema.ListAttribute{
				Description: "Model names the user is allowed to call. Set to ['no-default-models'] to block all model access.",
				Optional:    true,
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
			"metadata": schema.MapAttribute{
				Description: "Metadata for the user.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"spend": schema.Float64Attribute{
				Description: "Amount spent by this user.",
				Computed:    true,
			},
			"key": schema.StringAttribute{
				Description: "The auto-generated API key for the user (if auto_create_key is true).",
				Computed:    true,
				Sensitive:   true,
			},
		},
	}
}

func (r *UserResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *UserResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data UserResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userReq := r.buildUserRequest(ctx, &data)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/user/new", userReq, &result); err != nil {
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
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readUser(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read user: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
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

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/user/update", userReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update user: %s", err))
		return
	}

	// Read back for full state
	if err := r.readUser(ctx, &data); err != nil {
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
}

func (r *UserResource) buildUserRequest(ctx context.Context, data *UserResourceModel) map[string]interface{} {
	userReq := map[string]interface{}{}

	if !data.UserID.IsNull() && data.UserID.ValueString() != "" {
		userReq["user_id"] = data.UserID.ValueString()
	}
	if !data.UserAlias.IsNull() && data.UserAlias.ValueString() != "" {
		userReq["user_alias"] = data.UserAlias.ValueString()
	}
	if !data.UserEmail.IsNull() && data.UserEmail.ValueString() != "" {
		userReq["user_email"] = data.UserEmail.ValueString()
	}
	if !data.UserRole.IsNull() && data.UserRole.ValueString() != "" {
		userReq["user_role"] = data.UserRole.ValueString()
	}
	if !data.MaxBudget.IsNull() {
		userReq["max_budget"] = data.MaxBudget.ValueFloat64()
	}
	if !data.BudgetDuration.IsNull() && data.BudgetDuration.ValueString() != "" {
		userReq["budget_duration"] = data.BudgetDuration.ValueString()
	}
	if !data.TPMLimit.IsNull() {
		userReq["tpm_limit"] = data.TPMLimit.ValueInt64()
	}
	if !data.RPMLimit.IsNull() {
		userReq["rpm_limit"] = data.RPMLimit.ValueInt64()
	}
	if !data.AutoCreateKey.IsNull() {
		userReq["auto_create_key"] = data.AutoCreateKey.ValueBool()
	}

	if !data.Teams.IsNull() {
		var teams []string
		data.Teams.ElementsAs(ctx, &teams, false)
		userReq["teams"] = teams
	}

	if !data.Models.IsNull() {
		var models []string
		data.Models.ElementsAs(ctx, &models, false)
		userReq["models"] = models
	}

	if !data.Metadata.IsNull() {
		var metadata map[string]string
		data.Metadata.ElementsAs(ctx, &metadata, false)
		userReq["metadata"] = metadata
	}

	return userReq
}

func (r *UserResource) readUser(ctx context.Context, data *UserResourceModel) error {
	userID := data.UserID.ValueString()
	if userID == "" {
		userID = data.ID.ValueString()
	}

	endpoint := fmt.Sprintf("/user/info?user_id=%s", userID)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// The /user/info endpoint returns user_info nested
	userInfo := result
	if ui, ok := result["user_info"].(map[string]interface{}); ok {
		userInfo = ui
	}

	// Update fields from response
	if userID, ok := userInfo["user_id"].(string); ok {
		data.UserID = types.StringValue(userID)
		data.ID = types.StringValue(userID)
	}
	if alias, ok := userInfo["user_alias"].(string); ok {
		data.UserAlias = types.StringValue(alias)
	}
	if email, ok := userInfo["user_email"].(string); ok {
		data.UserEmail = types.StringValue(email)
	}
	if role, ok := userInfo["user_role"].(string); ok {
		data.UserRole = types.StringValue(role)
	}
	if budgetDuration, ok := userInfo["budget_duration"].(string); ok {
		data.BudgetDuration = types.StringValue(budgetDuration)
	}

	// Numeric fields
	if maxBudget, ok := userInfo["max_budget"].(float64); ok {
		data.MaxBudget = types.Float64Value(maxBudget)
	}
	if spend, ok := userInfo["spend"].(float64); ok {
		data.Spend = types.Float64Value(spend)
	}
	if tpmLimit, ok := userInfo["tpm_limit"].(float64); ok {
		data.TPMLimit = types.Int64Value(int64(tpmLimit))
	}
	if rpmLimit, ok := userInfo["rpm_limit"].(float64); ok {
		data.RPMLimit = types.Int64Value(int64(rpmLimit))
	}

	// Handle teams list
	if teams, ok := userInfo["teams"].([]interface{}); ok {
		teamsList := make([]attr.Value, len(teams))
		for i, t := range teams {
			if str, ok := t.(string); ok {
				teamsList[i] = types.StringValue(str)
			}
		}
		data.Teams, _ = types.ListValue(types.StringType, teamsList)
	}

	// Handle models list
	if models, ok := userInfo["models"].([]interface{}); ok {
		modelsList := make([]attr.Value, len(models))
		for i, m := range models {
			if str, ok := m.(string); ok {
				modelsList[i] = types.StringValue(str)
			}
		}
		data.Models, _ = types.ListValue(types.StringType, modelsList)
	}

	// Handle metadata map
	if metadata, ok := userInfo["metadata"].(map[string]interface{}); ok {
		metaMap := make(map[string]attr.Value)
		for k, v := range metadata {
			if str, ok := v.(string); ok {
				metaMap[k] = types.StringValue(str)
			}
		}
		data.Metadata, _ = types.MapValue(types.StringType, metaMap)
	}

	return nil
}
