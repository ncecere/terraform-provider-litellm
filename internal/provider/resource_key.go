package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &KeyResource{}
var _ resource.ResourceWithImportState = &KeyResource{}

func NewKeyResource() resource.Resource {
	return &KeyResource{}
}

type KeyResource struct {
	client *Client
}

type KeyResourceModel struct {
	ID                       types.String  `tfsdk:"id"`
	Key                      types.String  `tfsdk:"key"`
	Models                   types.List    `tfsdk:"models"`
	AllowedRoutes            types.List    `tfsdk:"allowed_routes"`
	AllowedPassthroughRoutes types.List    `tfsdk:"allowed_passthrough_routes"`
	MaxBudget                types.Float64 `tfsdk:"max_budget"`
	UserID                   types.String  `tfsdk:"user_id"`
	TeamID                   types.String  `tfsdk:"team_id"`
	OrganizationID           types.String  `tfsdk:"organization_id"`
	BudgetID                 types.String  `tfsdk:"budget_id"`
	ServiceAccountID         types.String  `tfsdk:"service_account_id"`
	MaxParallelRequests      types.Int64   `tfsdk:"max_parallel_requests"`
	Metadata                 types.Map     `tfsdk:"metadata"`
	TPMLimit                 types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit                 types.Int64   `tfsdk:"rpm_limit"`
	TPMLimitType             types.String  `tfsdk:"tpm_limit_type"`
	RPMLimitType             types.String  `tfsdk:"rpm_limit_type"`
	BudgetDuration           types.String  `tfsdk:"budget_duration"`
	AllowedCacheControls     types.List    `tfsdk:"allowed_cache_controls"`
	SoftBudget               types.Float64 `tfsdk:"soft_budget"`
	KeyAlias                 types.String  `tfsdk:"key_alias"`
	Duration                 types.String  `tfsdk:"duration"`
	Aliases                  types.Map     `tfsdk:"aliases"`
	Config                   types.Map     `tfsdk:"config"`
	Permissions              types.Map     `tfsdk:"permissions"`
	ModelMaxBudget           types.Map     `tfsdk:"model_max_budget"`
	ModelRPMLimit            types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit            types.Map     `tfsdk:"model_tpm_limit"`
	Guardrails               types.List    `tfsdk:"guardrails"`
	Prompts                  types.List    `tfsdk:"prompts"`
	EnforcedParams           types.List    `tfsdk:"enforced_params"`
	Tags                     types.List    `tfsdk:"tags"`
	Blocked                  types.Bool    `tfsdk:"blocked"`
	Spend                    types.Float64 `tfsdk:"spend"`
}

func (r *KeyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_key"
}

func (r *KeyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM API key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this key (same as key value).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"key": schema.StringAttribute{
				Description: "The API key value. If not specified, a key will be generated.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"models": schema.ListAttribute{
				Description: "List of models this key can access.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"allowed_routes": schema.ListAttribute{
				Description: "List of allowed API routes.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"allowed_passthrough_routes": schema.ListAttribute{
				Description: "List of allowed passthrough routes.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"max_budget": schema.Float64Attribute{
				Description: "Maximum budget for this key.",
				Optional:    true,
				Computed:    true,
			},
			"user_id": schema.StringAttribute{
				Description: "User ID associated with this key.",
				Optional:    true,
			},
			"team_id": schema.StringAttribute{
				Description: "Team ID associated with this key.",
				Optional:    true,
			},
			"organization_id": schema.StringAttribute{
				Description: "Organization ID associated with this key.",
				Optional:    true,
			},
			"budget_id": schema.StringAttribute{
				Description: "Budget ID to associate with this key.",
				Optional:    true,
			},
			"service_account_id": schema.StringAttribute{
				Description: "Service account ID for team-owned keys.",
				Optional:    true,
			},
			"max_parallel_requests": schema.Int64Attribute{
				Description: "Maximum parallel requests allowed.",
				Optional:    true,
				Computed:    true,
			},
			"metadata": schema.MapAttribute{
				Description: "Metadata for the key.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"tpm_limit": schema.Int64Attribute{
				Description: "Tokens per minute limit.",
				Optional:    true,
				Computed:    true,
			},
			"rpm_limit": schema.Int64Attribute{
				Description: "Requests per minute limit.",
				Optional:    true,
				Computed:    true,
			},
			"tpm_limit_type": schema.StringAttribute{
				Description: "Type of TPM limit: 'key' (default) or 'team'. If 'team', TPM is shared across all keys for the team.",
				Optional:    true,
			},
			"rpm_limit_type": schema.StringAttribute{
				Description: "Type of RPM limit: 'key' (default) or 'team'. If 'team', RPM is shared across all keys for the team.",
				Optional:    true,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Budget reset duration (e.g., '30d', '1h').",
				Optional:    true,
			},
			"allowed_cache_controls": schema.ListAttribute{
				Description: "Allowed cache control values.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"soft_budget": schema.Float64Attribute{
				Description: "Soft budget limit for warnings.",
				Optional:    true,
				Computed:    true,
			},
			"key_alias": schema.StringAttribute{
				Description: "User-friendly alias for the key.",
				Optional:    true,
			},
			"duration": schema.StringAttribute{
				Description: "Key validity duration.",
				Optional:    true,
			},
			"aliases": schema.MapAttribute{
				Description: "Model alias mappings.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"config": schema.MapAttribute{
				Description: "Key-specific configuration.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"permissions": schema.MapAttribute{
				Description: "Key permissions.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"model_max_budget": schema.MapAttribute{
				Description: "Per-model budget limits.",
				Optional:    true,
				ElementType: types.Float64Type,
			},
			"model_rpm_limit": schema.MapAttribute{
				Description: "Per-model RPM limits.",
				Optional:    true,
				ElementType: types.Int64Type,
			},
			"model_tpm_limit": schema.MapAttribute{
				Description: "Per-model TPM limits.",
				Optional:    true,
				ElementType: types.Int64Type,
			},
			"guardrails": schema.ListAttribute{
				Description: "Guardrails for the key.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"prompts": schema.ListAttribute{
				Description: "List of prompt IDs this key can access.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"enforced_params": schema.ListAttribute{
				Description: "List of enforced params for this key (params that must be present in requests).",
				Optional:    true,
				ElementType: types.StringType,
			},
			"tags": schema.ListAttribute{
				Description: "Tags for the key.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the key is blocked.",
				Optional:    true,
				Computed:    true,
			},
			"spend": schema.Float64Attribute{
				Description: "Amount spent by this key.",
				Computed:    true,
			},
		},
	}
}

func (r *KeyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *KeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data KeyResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyReq := r.buildKeyRequest(ctx, &data)

	endpoint := "/key/generate"
	if !data.ServiceAccountID.IsNull() && data.ServiceAccountID.ValueString() != "" {
		endpoint = "/key/service-account/generate"
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", endpoint, keyReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create key: %s", err))
		return
	}

	if keyVal, ok := result["key"].(string); ok {
		data.Key = types.StringValue(keyVal)
		data.ID = types.StringValue(keyVal)
	}

	// Read back for full state
	if err := r.readKey(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Key created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *KeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data KeyResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readKey(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read key: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *KeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data KeyResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state KeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = state.ID
	data.Key = state.Key

	updateReq := r.buildKeyRequest(ctx, &data)
	updateReq["key"] = data.Key.ValueString()

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/key/update", updateReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update key: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *KeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data KeyResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := map[string]interface{}{
		"keys": []string{data.Key.ValueString()},
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/key/delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete key: %s", err))
			return
		}
	}
}

func (r *KeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), req.ID)...)
}

func (r *KeyResource) buildKeyRequest(ctx context.Context, data *KeyResourceModel) map[string]interface{} {
	keyReq := make(map[string]interface{})

	// String fields - check IsNull, IsUnknown, and empty string
	if !data.Key.IsNull() && !data.Key.IsUnknown() && data.Key.ValueString() != "" {
		keyReq["key"] = data.Key.ValueString()
	}
	if !data.UserID.IsNull() && !data.UserID.IsUnknown() && data.UserID.ValueString() != "" {
		keyReq["user_id"] = data.UserID.ValueString()
	}
	if !data.TeamID.IsNull() && !data.TeamID.IsUnknown() && data.TeamID.ValueString() != "" {
		keyReq["team_id"] = data.TeamID.ValueString()
	}
	if !data.OrganizationID.IsNull() && !data.OrganizationID.IsUnknown() && data.OrganizationID.ValueString() != "" {
		keyReq["organization_id"] = data.OrganizationID.ValueString()
	}
	if !data.BudgetID.IsNull() && !data.BudgetID.IsUnknown() && data.BudgetID.ValueString() != "" {
		keyReq["budget_id"] = data.BudgetID.ValueString()
	}
	if !data.TPMLimitType.IsNull() && !data.TPMLimitType.IsUnknown() && data.TPMLimitType.ValueString() != "" {
		keyReq["tpm_limit_type"] = data.TPMLimitType.ValueString()
	}
	if !data.RPMLimitType.IsNull() && !data.RPMLimitType.IsUnknown() && data.RPMLimitType.ValueString() != "" {
		keyReq["rpm_limit_type"] = data.RPMLimitType.ValueString()
	}
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() && data.BudgetDuration.ValueString() != "" {
		keyReq["budget_duration"] = data.BudgetDuration.ValueString()
	}
	if !data.KeyAlias.IsNull() && !data.KeyAlias.IsUnknown() && data.KeyAlias.ValueString() != "" {
		keyReq["key_alias"] = data.KeyAlias.ValueString()
	}
	if !data.Duration.IsNull() && !data.Duration.IsUnknown() && data.Duration.ValueString() != "" {
		keyReq["duration"] = data.Duration.ValueString()
	}

	// Numeric fields - check IsNull and IsUnknown
	if !data.MaxBudget.IsNull() && !data.MaxBudget.IsUnknown() {
		keyReq["max_budget"] = data.MaxBudget.ValueFloat64()
	}
	if !data.MaxParallelRequests.IsNull() && !data.MaxParallelRequests.IsUnknown() {
		keyReq["max_parallel_requests"] = data.MaxParallelRequests.ValueInt64()
	}
	if !data.TPMLimit.IsNull() && !data.TPMLimit.IsUnknown() {
		keyReq["tpm_limit"] = data.TPMLimit.ValueInt64()
	}
	if !data.RPMLimit.IsNull() && !data.RPMLimit.IsUnknown() {
		keyReq["rpm_limit"] = data.RPMLimit.ValueInt64()
	}
	if !data.SoftBudget.IsNull() && !data.SoftBudget.IsUnknown() {
		keyReq["soft_budget"] = data.SoftBudget.ValueFloat64()
	}

	// Boolean fields - check IsNull and IsUnknown
	if !data.Blocked.IsNull() && !data.Blocked.IsUnknown() {
		keyReq["blocked"] = data.Blocked.ValueBool()
	}

	// Models list - special handling for team models
	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		var models []string
		data.Models.ElementsAs(ctx, &models, false)
		if len(models) == 0 && !data.TeamID.IsNull() && !data.TeamID.IsUnknown() && data.TeamID.ValueString() != "" {
			models = []string{"all-team-models"}
		}
		if len(models) > 0 {
			keyReq["models"] = models
		}
	} else if !data.TeamID.IsNull() && !data.TeamID.IsUnknown() && data.TeamID.ValueString() != "" {
		keyReq["models"] = []string{"all-team-models"}
	}

	// List fields - check IsNull, IsUnknown, and len > 0
	if !data.AllowedRoutes.IsNull() && !data.AllowedRoutes.IsUnknown() {
		var routes []string
		data.AllowedRoutes.ElementsAs(ctx, &routes, false)
		if len(routes) > 0 {
			keyReq["allowed_routes"] = routes
		}
	}

	if !data.AllowedPassthroughRoutes.IsNull() && !data.AllowedPassthroughRoutes.IsUnknown() {
		var routes []string
		data.AllowedPassthroughRoutes.ElementsAs(ctx, &routes, false)
		if len(routes) > 0 {
			keyReq["allowed_passthrough_routes"] = routes
		}
	}

	if !data.AllowedCacheControls.IsNull() && !data.AllowedCacheControls.IsUnknown() {
		var cacheControls []string
		data.AllowedCacheControls.ElementsAs(ctx, &cacheControls, false)
		if len(cacheControls) > 0 {
			keyReq["allowed_cache_controls"] = cacheControls
		}
	}

	if !data.Guardrails.IsNull() && !data.Guardrails.IsUnknown() {
		var guardrails []string
		data.Guardrails.ElementsAs(ctx, &guardrails, false)
		if len(guardrails) > 0 {
			keyReq["guardrails"] = guardrails
		}
	}

	if !data.Prompts.IsNull() && !data.Prompts.IsUnknown() {
		var prompts []string
		data.Prompts.ElementsAs(ctx, &prompts, false)
		if len(prompts) > 0 {
			keyReq["prompts"] = prompts
		}
	}

	if !data.EnforcedParams.IsNull() && !data.EnforcedParams.IsUnknown() {
		var enforcedParams []string
		data.EnforcedParams.ElementsAs(ctx, &enforcedParams, false)
		if len(enforcedParams) > 0 {
			keyReq["enforced_params"] = enforcedParams
		}
	}

	if !data.Tags.IsNull() && !data.Tags.IsUnknown() {
		var tags []string
		data.Tags.ElementsAs(ctx, &tags, false)
		if len(tags) > 0 {
			keyReq["tags"] = tags
		}
	}

	// Map fields - check IsNull, IsUnknown, and len > 0
	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var metadata map[string]string
		data.Metadata.ElementsAs(ctx, &metadata, false)
		if len(metadata) > 0 {
			keyReq["metadata"] = metadata
		}
	}

	if !data.Aliases.IsNull() && !data.Aliases.IsUnknown() {
		var aliases map[string]string
		data.Aliases.ElementsAs(ctx, &aliases, false)
		if len(aliases) > 0 {
			keyReq["aliases"] = aliases
		}
	}

	if !data.Config.IsNull() && !data.Config.IsUnknown() {
		var config map[string]string
		data.Config.ElementsAs(ctx, &config, false)
		if len(config) > 0 {
			keyReq["config"] = config
		}
	}

	if !data.Permissions.IsNull() && !data.Permissions.IsUnknown() {
		var permissions map[string]string
		data.Permissions.ElementsAs(ctx, &permissions, false)
		if len(permissions) > 0 {
			keyReq["permissions"] = permissions
		}
	}

	if !data.ModelMaxBudget.IsNull() && !data.ModelMaxBudget.IsUnknown() {
		var modelMaxBudget map[string]float64
		data.ModelMaxBudget.ElementsAs(ctx, &modelMaxBudget, false)
		if len(modelMaxBudget) > 0 {
			keyReq["model_max_budget"] = modelMaxBudget
		}
	}

	if !data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown() {
		var modelRPMLimit map[string]int64
		data.ModelRPMLimit.ElementsAs(ctx, &modelRPMLimit, false)
		if len(modelRPMLimit) > 0 {
			keyReq["model_rpm_limit"] = modelRPMLimit
		}
	}

	if !data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown() {
		var modelTPMLimit map[string]int64
		data.ModelTPMLimit.ElementsAs(ctx, &modelTPMLimit, false)
		if len(modelTPMLimit) > 0 {
			keyReq["model_tpm_limit"] = modelTPMLimit
		}
	}

	// Handle service account
	if !data.ServiceAccountID.IsNull() && !data.ServiceAccountID.IsUnknown() && data.ServiceAccountID.ValueString() != "" {
		saID := data.ServiceAccountID.ValueString()
		if keyReq["metadata"] == nil {
			keyReq["metadata"] = map[string]interface{}{}
		}
		if m, ok := keyReq["metadata"].(map[string]interface{}); ok {
			m["service_account_id"] = saID
		}
		if keyReq["key_alias"] == nil || keyReq["key_alias"] == "" {
			keyReq["key_alias"] = saID
		}
	}

	return keyReq
}

func (r *KeyResource) readKey(ctx context.Context, data *KeyResourceModel) error {
	keyID := data.ID.ValueString()
	if keyID == "" {
		keyID = data.Key.ValueString()
	}

	endpoint := fmt.Sprintf("/key/info?key=%s", keyID)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// Update computed fields from response
	if spend, ok := result["spend"].(float64); ok {
		data.Spend = types.Float64Value(spend)
	}
	if maxBudget, ok := result["max_budget"].(float64); ok {
		data.MaxBudget = types.Float64Value(maxBudget)
	}
	if tpmLimit, ok := result["tpm_limit"].(float64); ok {
		data.TPMLimit = types.Int64Value(int64(tpmLimit))
	}
	if rpmLimit, ok := result["rpm_limit"].(float64); ok {
		data.RPMLimit = types.Int64Value(int64(rpmLimit))
	}
	if maxParallel, ok := result["max_parallel_requests"].(float64); ok {
		data.MaxParallelRequests = types.Int64Value(int64(maxParallel))
	}
	if softBudget, ok := result["soft_budget"].(float64); ok {
		data.SoftBudget = types.Float64Value(softBudget)
	}
	if blocked, ok := result["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
	}
	if orgID, ok := result["organization_id"].(string); ok && orgID != "" {
		data.OrganizationID = types.StringValue(orgID)
	}
	if budgetID, ok := result["budget_id"].(string); ok && budgetID != "" {
		data.BudgetID = types.StringValue(budgetID)
	}

	return nil
}
