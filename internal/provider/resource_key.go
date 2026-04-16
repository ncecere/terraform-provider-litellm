package provider

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ resource.Resource = &KeyResource{}
var _ resource.ResourceWithImportState = &KeyResource{}
var _ resource.ResourceWithUpgradeState = &KeyResource{}

// hashKeyForID produces a non-sensitive identifier from a raw API key.
// Format: "sha256:<hex digest>" so it is self-documenting and non-reversible.
func hashKeyForID(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return fmt.Sprintf("sha256:%x", h)
}

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
	RouterSettings           types.Object  `tfsdk:"router_settings"`
	Blocked                  types.Bool    `tfsdk:"blocked"`
}

// KeyRouterSettingsModel represents all 21 fields from LiteLLM's ROUTER_SETTINGS_FIELDS.
type KeyRouterSettingsModel struct {
	RoutingStrategy            types.String  `tfsdk:"routing_strategy"`
	RoutingStrategyArgs        types.Map     `tfsdk:"routing_strategy_args"`
	NumRetries                 types.Int64   `tfsdk:"num_retries"`
	Timeout                    types.Float64 `tfsdk:"timeout"`
	StreamTimeout              types.Float64 `tfsdk:"stream_timeout"`
	MaxFallbacks               types.Int64   `tfsdk:"max_fallbacks"`
	Fallbacks                  types.List    `tfsdk:"fallbacks"`
	ContextWindowFallbacks     types.List    `tfsdk:"context_window_fallbacks"`
	ContentPolicyFallbacks     types.List    `tfsdk:"content_policy_fallbacks"`
	AllowedFails               types.Int64   `tfsdk:"allowed_fails"`
	CooldownTime               types.Float64 `tfsdk:"cooldown_time"`
	RetryAfter                 types.Int64   `tfsdk:"retry_after"`
	RetryPolicy                types.Object  `tfsdk:"retry_policy"`
	ModelGroupAlias            types.Map     `tfsdk:"model_group_alias"`
	EnablePreCallChecks        types.Bool    `tfsdk:"enable_pre_call_checks"`
	DefaultLitellmParams       types.Map     `tfsdk:"default_litellm_params"`
	SetVerbose                 types.Bool    `tfsdk:"set_verbose"`
	DefaultMaxParallelRequests types.Int64   `tfsdk:"default_max_parallel_requests"`
	EnableTagFiltering         types.Bool    `tfsdk:"enable_tag_filtering"`
	TagFilteringMatchAny       types.Bool    `tfsdk:"tag_filtering_match_any"`
	DisableCooldowns           types.Bool    `tfsdk:"disable_cooldowns"`
}

// KeyRetryPolicyModel maps to LiteLLM's RetryPolicy Pydantic model.
type KeyRetryPolicyModel struct {
	BadRequestErrorRetries             types.Int64 `tfsdk:"bad_request_error_retries"`
	AuthenticationErrorRetries         types.Int64 `tfsdk:"authentication_error_retries"`
	TimeoutErrorRetries                types.Int64 `tfsdk:"timeout_error_retries"`
	RateLimitErrorRetries              types.Int64 `tfsdk:"rate_limit_error_retries"`
	ContentPolicyViolationErrorRetries types.Int64 `tfsdk:"content_policy_violation_error_retries"`
	InternalServerErrorRetries         types.Int64 `tfsdk:"internal_server_error_retries"`
}

var keyRetryPolicyAttrTypes = map[string]attr.Type{
	"bad_request_error_retries":              types.Int64Type,
	"authentication_error_retries":           types.Int64Type,
	"timeout_error_retries":                  types.Int64Type,
	"rate_limit_error_retries":               types.Int64Type,
	"content_policy_violation_error_retries": types.Int64Type,
	"internal_server_error_retries":          types.Int64Type,
}

var keyRouterSettingsAttrTypes = map[string]attr.Type{
	"routing_strategy":              types.StringType,
	"routing_strategy_args":         types.MapType{ElemType: types.StringType},
	"num_retries":                   types.Int64Type,
	"timeout":                       types.Float64Type,
	"stream_timeout":                types.Float64Type,
	"max_fallbacks":                 types.Int64Type,
	"fallbacks":                     types.ListType{ElemType: types.ObjectType{AttrTypes: fallbackEntryAttrTypes}},
	"context_window_fallbacks":      types.ListType{ElemType: types.ObjectType{AttrTypes: fallbackEntryAttrTypes}},
	"content_policy_fallbacks":      types.ListType{ElemType: types.ObjectType{AttrTypes: fallbackEntryAttrTypes}},
	"allowed_fails":                 types.Int64Type,
	"cooldown_time":                 types.Float64Type,
	"retry_after":                   types.Int64Type,
	"retry_policy":                  types.ObjectType{AttrTypes: keyRetryPolicyAttrTypes},
	"model_group_alias":             types.MapType{ElemType: types.StringType},
	"enable_pre_call_checks":        types.BoolType,
	"default_litellm_params":        types.MapType{ElemType: types.StringType},
	"set_verbose":                   types.BoolType,
	"default_max_parallel_requests": types.Int64Type,
	"enable_tag_filtering":          types.BoolType,
	"tag_filtering_match_any":       types.BoolType,
	"disable_cooldowns":             types.BoolType,
}

func (r *KeyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_key"
}

func (r *KeyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM API key.",
		Version:     1,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Non-sensitive unique identifier for this key (SHA256 hash of the key value).",
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
				Computed:    true,
				ElementType: types.StringType,
			},
			"allowed_routes": schema.ListAttribute{
				Description: "List of allowed API routes.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"allowed_passthrough_routes": schema.ListAttribute{
				Description: "List of allowed passthrough routes.",
				Optional:    true,
				Computed:    true,
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
				Computed:    true,
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
				Computed:    true,
				ElementType: types.StringType,
			},
			"soft_budget": schema.Float64Attribute{
				Description: "Soft budget limit for warnings.",
				Optional:    true,
				Computed:    true,
			},
			"key_alias": schema.StringAttribute{
				Description: "User-friendly alias for the key. When service_account_id is set and key_alias is omitted, the provider defaults key_alias to the service_account_id value.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"duration": schema.StringAttribute{
				Description: "Key validity duration.",
				Optional:    true,
			},
			"aliases": schema.MapAttribute{
				Description: "Model alias mappings.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"config": schema.MapAttribute{
				Description: "Key-specific configuration.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"permissions": schema.MapAttribute{
				Description: "Key permissions.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"model_max_budget": schema.MapAttribute{
				Description: "Per-model budget limits.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Float64Type,
			},
			"model_rpm_limit": schema.MapAttribute{
				Description: "Per-model RPM limits.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
			},
			"model_tpm_limit": schema.MapAttribute{
				Description: "Per-model TPM limits.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
			},
			"guardrails": schema.ListAttribute{
				Description: "Guardrails for the key.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"prompts": schema.ListAttribute{
				Description: "List of prompt IDs this key can access.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"enforced_params": schema.ListAttribute{
				Description: "List of enforced params for this key (params that must be present in requests).",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"tags": schema.ListAttribute{
				Description: "Tags for the key.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"router_settings": schema.SingleNestedAttribute{
				Description: "Router settings for the key. These override global and team-level routing " +
					"settings for requests made with this key. Resolution order: Key > Team > Global.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"routing_strategy": schema.StringAttribute{
						Description: "Strategy for routing requests across model deployments (e.g. 'simple-shuffle', 'least-busy', 'latency-based-routing', 'cost-based-routing', 'usage-based-routing').",
						Optional:    true,
					},
					"routing_strategy_args": schema.MapAttribute{
						Description: "Additional arguments for the routing strategy.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"num_retries": schema.Int64Attribute{
						Description: "Number of retries on failure.",
						Optional:    true,
					},
					"timeout": schema.Float64Attribute{
						Description: "Request timeout in seconds.",
						Optional:    true,
					},
					"stream_timeout": schema.Float64Attribute{
						Description: "Timeout in seconds for streaming requests.",
						Optional:    true,
					},
					"max_fallbacks": schema.Int64Attribute{
						Description: "Maximum number of fallbacks to attempt.",
						Optional:    true,
					},
					"fallbacks": schema.ListNestedAttribute{
						Description: "Fallback model chains triggered when a model call fails after retries.",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"model": schema.StringAttribute{
									Description: "The primary model name to configure fallbacks for.",
									Required:    true,
								},
								"fallback_models": schema.ListAttribute{
									Description: "Ordered list of fallback model names.",
									Required:    true,
									ElementType: types.StringType,
								},
							},
						},
					},
					"context_window_fallbacks": schema.ListNestedAttribute{
						Description: "Fallback model chains triggered when a context window exceeded error occurs.",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"model": schema.StringAttribute{
									Description: "The primary model name to configure fallbacks for.",
									Required:    true,
								},
								"fallback_models": schema.ListAttribute{
									Description: "Ordered list of fallback model names.",
									Required:    true,
									ElementType: types.StringType,
								},
							},
						},
					},
					"content_policy_fallbacks": schema.ListNestedAttribute{
						Description: "Fallback model chains triggered when a content policy violation error occurs.",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"model": schema.StringAttribute{
									Description: "The primary model name to configure fallbacks for.",
									Required:    true,
								},
								"fallback_models": schema.ListAttribute{
									Description: "Ordered list of fallback model names.",
									Required:    true,
									ElementType: types.StringType,
								},
							},
						},
					},
					"allowed_fails": schema.Int64Attribute{
						Description: "Number of failures allowed before a model is put into cooldown.",
						Optional:    true,
					},
					"cooldown_time": schema.Float64Attribute{
						Description: "Duration in seconds a model stays in cooldown after exceeding allowed_fails.",
						Optional:    true,
					},
					"retry_after": schema.Int64Attribute{
						Description: "Minimum seconds to wait before retrying a rate-limited request.",
						Optional:    true,
					},
					"retry_policy": schema.SingleNestedAttribute{
						Description: "Per-error-type retry counts that override num_retries.",
						Optional:    true,
						Attributes: map[string]schema.Attribute{
							"bad_request_error_retries": schema.Int64Attribute{
								Description: "Retries for BadRequestError (HTTP 400).",
								Optional:    true,
							},
							"authentication_error_retries": schema.Int64Attribute{
								Description: "Retries for AuthenticationError (HTTP 401).",
								Optional:    true,
							},
							"timeout_error_retries": schema.Int64Attribute{
								Description: "Retries for TimeoutError.",
								Optional:    true,
							},
							"rate_limit_error_retries": schema.Int64Attribute{
								Description: "Retries for RateLimitError (HTTP 429).",
								Optional:    true,
							},
							"content_policy_violation_error_retries": schema.Int64Attribute{
								Description: "Retries for ContentPolicyViolationError.",
								Optional:    true,
							},
							"internal_server_error_retries": schema.Int64Attribute{
								Description: "Retries for InternalServerError (HTTP 500).",
								Optional:    true,
							},
						},
					},
					"model_group_alias": schema.MapAttribute{
						Description: "Alias mappings from one model group name to another.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"enable_pre_call_checks": schema.BoolAttribute{
						Description: "Enable pre-call checks (e.g. context window validation) before routing.",
						Optional:    true,
					},
					"default_litellm_params": schema.MapAttribute{
						Description: "Default parameters to merge into every LiteLLM request for this key.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"set_verbose": schema.BoolAttribute{
						Description: "Enable verbose logging for requests made with this key.",
						Optional:    true,
					},
					"default_max_parallel_requests": schema.Int64Attribute{
						Description: "Default maximum parallel requests allowed per deployment.",
						Optional:    true,
					},
					"enable_tag_filtering": schema.BoolAttribute{
						Description: "Enable tag-based routing so only deployments matching request tags are considered.",
						Optional:    true,
					},
					"tag_filtering_match_any": schema.BoolAttribute{
						Description: "When tag filtering is enabled, match deployments that have any of the request tags (true) vs all tags (false).",
						Optional:    true,
					},
					"disable_cooldowns": schema.BoolAttribute{
						Description: "Disable the cooldown mechanism so failed deployments are always retried.",
						Optional:    true,
					},
				},
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the key is blocked.",
				Optional:    true,
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
		data.ID = types.StringValue(hashKeyForID(keyVal))
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

	if err := r.readKey(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Key updated but failed to read back: %s", err))
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
	// The import ID is the raw API key value. Store it in "key" (sensitive)
	// and use a SHA256 hash as the non-sensitive resource ID.
	rawKey := req.ID
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), hashKeyForID(rawKey))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), rawKey)...)
}

// UpgradeState handles state migrations from older schema versions.
// Version 0 → 1: The resource ID changes from the raw API key to a SHA256 hash.
func (r *KeyResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: nil,
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				// PriorSchema is nil, so req.State is unavailable.
				// Use RawState JSON to read the prior state.
				if req.RawState == nil {
					resp.Diagnostics.AddError(
						"Unable to Upgrade State",
						"RawState is nil. This is a bug in the provider.",
					)
					return
				}

				var priorState map[string]json.RawMessage
				if err := json.Unmarshal(req.RawState.JSON, &priorState); err != nil {
					resp.Diagnostics.AddError(
						"Unable to Upgrade State",
						fmt.Sprintf("Failed to unmarshal prior state JSON: %s", err),
					)
					return
				}

				// In v0, "id" contained the raw API key.
				var rawID string
				if idJSON, ok := priorState["id"]; ok {
					if err := json.Unmarshal(idJSON, &rawID); err != nil {
						resp.Diagnostics.AddError(
							"Unable to Upgrade State",
							fmt.Sprintf("Failed to unmarshal 'id' from prior state: %s", err),
						)
						return
					}
				}

				if rawID == "" {
					resp.Diagnostics.AddError(
						"Unable to Upgrade State",
						"Prior state 'id' is empty.",
					)
					return
				}

				tflog.Info(ctx, "Upgrading litellm_key state from v0 to v1: hashing raw key ID")

				// Replace "id" with the hashed value in the raw state, then
				// write the full JSON back via DynamicValue so all other
				// attributes are preserved.
				priorState["id"], _ = json.Marshal(hashKeyForID(rawID))

				upgradedJSON, err := json.Marshal(priorState)
				if err != nil {
					resp.Diagnostics.AddError(
						"Unable to Upgrade State",
						fmt.Sprintf("Failed to marshal upgraded state: %s", err),
					)
					return
				}

				// Use DynamicValue to pass the upgraded JSON directly to the
				// framework. This avoids needing a typed State object and
				// preserves all existing attributes as-is.
				resp.DynamicValue = &tfprotov6.DynamicValue{
					JSON: upgradedJSON,
				}
			},
		},
	}
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
	if !data.RouterSettings.IsNull() && !data.RouterSettings.IsUnknown() {
		keyReq["router_settings"] = buildKeyRouterSettingsPayload(ctx, data.RouterSettings)
	} else {
		keyReq["router_settings"] = map[string]interface{}{}
	}

	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var metadata map[string]string
		data.Metadata.ElementsAs(ctx, &metadata, false)
		if len(metadata) > 0 {
			// Convert string values that contain JSON objects/arrays to native
			// types so the API receives them as structured data rather than
			// escaped strings (e.g. logging configuration).
			keyReq["metadata"] = convertJSONStringsToNative(metadata)
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

// buildKeyRouterSettingsPayload converts the Terraform router_settings object
// into the LiteLLM API wire format.
func buildKeyRouterSettingsPayload(ctx context.Context, obj types.Object) map[string]interface{} {
	var rs KeyRouterSettingsModel
	obj.As(ctx, &rs, basetypes.ObjectAsOptions{})

	payload := map[string]interface{}{}

	if !rs.RoutingStrategy.IsNull() && !rs.RoutingStrategy.IsUnknown() {
		payload["routing_strategy"] = rs.RoutingStrategy.ValueString()
	}
	if !rs.NumRetries.IsNull() && !rs.NumRetries.IsUnknown() {
		payload["num_retries"] = rs.NumRetries.ValueInt64()
	}
	if !rs.MaxFallbacks.IsNull() && !rs.MaxFallbacks.IsUnknown() {
		payload["max_fallbacks"] = rs.MaxFallbacks.ValueInt64()
	}
	if !rs.AllowedFails.IsNull() && !rs.AllowedFails.IsUnknown() {
		payload["allowed_fails"] = rs.AllowedFails.ValueInt64()
	}
	if !rs.RetryAfter.IsNull() && !rs.RetryAfter.IsUnknown() {
		payload["retry_after"] = rs.RetryAfter.ValueInt64()
	}
	if !rs.DefaultMaxParallelRequests.IsNull() && !rs.DefaultMaxParallelRequests.IsUnknown() {
		payload["default_max_parallel_requests"] = rs.DefaultMaxParallelRequests.ValueInt64()
	}
	if !rs.Timeout.IsNull() && !rs.Timeout.IsUnknown() {
		payload["timeout"] = rs.Timeout.ValueFloat64()
	}
	if !rs.StreamTimeout.IsNull() && !rs.StreamTimeout.IsUnknown() {
		payload["stream_timeout"] = rs.StreamTimeout.ValueFloat64()
	}
	if !rs.CooldownTime.IsNull() && !rs.CooldownTime.IsUnknown() {
		payload["cooldown_time"] = rs.CooldownTime.ValueFloat64()
	}
	if !rs.EnablePreCallChecks.IsNull() && !rs.EnablePreCallChecks.IsUnknown() {
		payload["enable_pre_call_checks"] = rs.EnablePreCallChecks.ValueBool()
	}
	if !rs.SetVerbose.IsNull() && !rs.SetVerbose.IsUnknown() {
		payload["set_verbose"] = rs.SetVerbose.ValueBool()
	}
	if !rs.EnableTagFiltering.IsNull() && !rs.EnableTagFiltering.IsUnknown() {
		payload["enable_tag_filtering"] = rs.EnableTagFiltering.ValueBool()
	}
	if !rs.TagFilteringMatchAny.IsNull() && !rs.TagFilteringMatchAny.IsUnknown() {
		payload["tag_filtering_match_any"] = rs.TagFilteringMatchAny.ValueBool()
	}
	if !rs.DisableCooldowns.IsNull() && !rs.DisableCooldowns.IsUnknown() {
		payload["disable_cooldowns"] = rs.DisableCooldowns.ValueBool()
	}
	if !rs.Fallbacks.IsNull() && !rs.Fallbacks.IsUnknown() {
		payload["fallbacks"] = fallbackEntriesToAPIFormat(ctx, rs.Fallbacks)
	}
	if !rs.ContextWindowFallbacks.IsNull() && !rs.ContextWindowFallbacks.IsUnknown() {
		payload["context_window_fallbacks"] = fallbackEntriesToAPIFormat(ctx, rs.ContextWindowFallbacks)
	}
	if !rs.ContentPolicyFallbacks.IsNull() && !rs.ContentPolicyFallbacks.IsUnknown() {
		payload["content_policy_fallbacks"] = fallbackEntriesToAPIFormat(ctx, rs.ContentPolicyFallbacks)
	}
	if !rs.RoutingStrategyArgs.IsNull() && !rs.RoutingStrategyArgs.IsUnknown() {
		var m map[string]string
		rs.RoutingStrategyArgs.ElementsAs(ctx, &m, false)
		payload["routing_strategy_args"] = m
	}
	if !rs.ModelGroupAlias.IsNull() && !rs.ModelGroupAlias.IsUnknown() {
		var m map[string]string
		rs.ModelGroupAlias.ElementsAs(ctx, &m, false)
		payload["model_group_alias"] = m
	}
	if !rs.DefaultLitellmParams.IsNull() && !rs.DefaultLitellmParams.IsUnknown() {
		var m map[string]string
		rs.DefaultLitellmParams.ElementsAs(ctx, &m, false)
		payload["default_litellm_params"] = m
	}
	if !rs.RetryPolicy.IsNull() && !rs.RetryPolicy.IsUnknown() {
		var rp KeyRetryPolicyModel
		rs.RetryPolicy.As(ctx, &rp, basetypes.ObjectAsOptions{})
		rpMap := map[string]interface{}{}
		if !rp.BadRequestErrorRetries.IsNull() {
			rpMap["BadRequestErrorRetries"] = rp.BadRequestErrorRetries.ValueInt64()
		}
		if !rp.AuthenticationErrorRetries.IsNull() {
			rpMap["AuthenticationErrorRetries"] = rp.AuthenticationErrorRetries.ValueInt64()
		}
		if !rp.TimeoutErrorRetries.IsNull() {
			rpMap["TimeoutErrorRetries"] = rp.TimeoutErrorRetries.ValueInt64()
		}
		if !rp.RateLimitErrorRetries.IsNull() {
			rpMap["RateLimitErrorRetries"] = rp.RateLimitErrorRetries.ValueInt64()
		}
		if !rp.ContentPolicyViolationErrorRetries.IsNull() {
			rpMap["ContentPolicyViolationErrorRetries"] = rp.ContentPolicyViolationErrorRetries.ValueInt64()
		}
		if !rp.InternalServerErrorRetries.IsNull() {
			rpMap["InternalServerErrorRetries"] = rp.InternalServerErrorRetries.ValueInt64()
		}
		payload["retry_policy"] = rpMap
	}

	return payload
}

// parseKeyRouterSettingsFromAPI converts the LiteLLM API router_settings response
// back into a Terraform types.Object matching the KeyRouterSettingsModel schema.
func parseKeyRouterSettingsFromAPI(rs map[string]interface{}) types.Object {
	rsAttrs := map[string]attr.Value{
		"routing_strategy":              stringFromAPIVal(rs["routing_strategy"]),
		"num_retries":                   int64FromAPIVal(rs["num_retries"]),
		"max_fallbacks":                 int64FromAPIVal(rs["max_fallbacks"]),
		"allowed_fails":                 int64FromAPIVal(rs["allowed_fails"]),
		"retry_after":                   int64FromAPIVal(rs["retry_after"]),
		"default_max_parallel_requests": int64FromAPIVal(rs["default_max_parallel_requests"]),
		"timeout":                       float64FromAPIVal(rs["timeout"]),
		"stream_timeout":                float64FromAPIVal(rs["stream_timeout"]),
		"cooldown_time":                 float64FromAPIVal(rs["cooldown_time"]),
		"enable_pre_call_checks":        boolFromAPIVal(rs["enable_pre_call_checks"]),
		"set_verbose":                   boolFromAPIVal(rs["set_verbose"]),
		"enable_tag_filtering":          boolFromAPIVal(rs["enable_tag_filtering"]),
		"tag_filtering_match_any":       boolFromAPIVal(rs["tag_filtering_match_any"]),
		"disable_cooldowns":             boolFromAPIVal(rs["disable_cooldowns"]),
		"routing_strategy_args":         stringMapFromAPIVal(rs["routing_strategy_args"]),
		"model_group_alias":             stringMapFromAPIVal(rs["model_group_alias"]),
		"default_litellm_params":        stringMapFromAPIVal(rs["default_litellm_params"]),
		"retry_policy":                  parseKeyRetryPolicyFromAPIVal(rs["retry_policy"]),
	}

	if fb, ok := rs["fallbacks"].([]interface{}); ok {
		rsAttrs["fallbacks"] = apiFormatToFallbackEntries(fb)
	} else {
		rsAttrs["fallbacks"] = types.ListNull(types.ObjectType{AttrTypes: fallbackEntryAttrTypes})
	}
	if cwf, ok := rs["context_window_fallbacks"].([]interface{}); ok {
		rsAttrs["context_window_fallbacks"] = apiFormatToFallbackEntries(cwf)
	} else {
		rsAttrs["context_window_fallbacks"] = types.ListNull(types.ObjectType{AttrTypes: fallbackEntryAttrTypes})
	}
	if cpf, ok := rs["content_policy_fallbacks"].([]interface{}); ok {
		rsAttrs["content_policy_fallbacks"] = apiFormatToFallbackEntries(cpf)
	} else {
		rsAttrs["content_policy_fallbacks"] = types.ListNull(types.ObjectType{AttrTypes: fallbackEntryAttrTypes})
	}

	obj, _ := types.ObjectValue(keyRouterSettingsAttrTypes, rsAttrs)
	return obj
}

func stringFromAPIVal(v interface{}) types.String {
	if s, ok := v.(string); ok {
		return types.StringValue(s)
	}
	return types.StringNull()
}

func int64FromAPIVal(v interface{}) types.Int64 {
	if f, ok := v.(float64); ok {
		return types.Int64Value(int64(f))
	}
	return types.Int64Null()
}

func float64FromAPIVal(v interface{}) types.Float64 {
	if f, ok := v.(float64); ok {
		return types.Float64Value(f)
	}
	return types.Float64Null()
}

func boolFromAPIVal(v interface{}) types.Bool {
	if b, ok := v.(bool); ok {
		return types.BoolValue(b)
	}
	return types.BoolNull()
}

func stringMapFromAPIVal(v interface{}) types.Map {
	m, ok := v.(map[string]interface{})
	if !ok || len(m) == 0 {
		return types.MapNull(types.StringType)
	}
	elems := make(map[string]attr.Value, len(m))
	for k, val := range m {
		if s, ok := val.(string); ok {
			elems[k] = types.StringValue(s)
		} else {
			elems[k] = types.StringValue(valueToJSONString(val))
		}
	}
	result, _ := types.MapValue(types.StringType, elems)
	return result
}

func parseKeyRetryPolicyFromAPIVal(v interface{}) types.Object {
	m, ok := v.(map[string]interface{})
	if !ok {
		return types.ObjectNull(keyRetryPolicyAttrTypes)
	}
	attrs := map[string]attr.Value{
		"bad_request_error_retries":              int64FromAPIVal(m["BadRequestErrorRetries"]),
		"authentication_error_retries":           int64FromAPIVal(m["AuthenticationErrorRetries"]),
		"timeout_error_retries":                  int64FromAPIVal(m["TimeoutErrorRetries"]),
		"rate_limit_error_retries":               int64FromAPIVal(m["RateLimitErrorRetries"]),
		"content_policy_violation_error_retries": int64FromAPIVal(m["ContentPolicyViolationErrorRetries"]),
		"internal_server_error_retries":          int64FromAPIVal(m["InternalServerErrorRetries"]),
	}
	obj, _ := types.ObjectValue(keyRetryPolicyAttrTypes, attrs)
	return obj
}

func (r *KeyResource) readKey(ctx context.Context, data *KeyResourceModel) error {
	keyVal := data.Key.ValueString()
	if keyVal == "" {
		return fmt.Errorf("key value is empty, cannot read key info")
	}

	// url.QueryEscape ensures special characters in the key (e.g. '#') are
	// percent-encoded and not interpreted as a URL fragment, which would
	// silently truncate the key value before it reaches the server.
	endpoint := fmt.Sprintf("/key/info?key=%s", url.QueryEscape(keyVal))

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// The /key/info endpoint may return key data nested inside "info"
	info := result
	if nested, ok := result["info"].(map[string]interface{}); ok {
		info = nested
	}

	// Update computed fields from response.
	// For Optional+Computed scalars, resolve Unknown to Null when the API
	// returns nil so Terraform never sees an unknown value after apply.
	if maxBudget, ok := info["max_budget"].(float64); ok {
		data.MaxBudget = types.Float64Value(maxBudget)
	} else if data.MaxBudget.IsUnknown() {
		data.MaxBudget = types.Float64Null()
	}
	if tpmLimit, ok := info["tpm_limit"].(float64); ok {
		data.TPMLimit = types.Int64Value(int64(tpmLimit))
	} else if data.TPMLimit.IsUnknown() {
		data.TPMLimit = types.Int64Null()
	}
	if rpmLimit, ok := info["rpm_limit"].(float64); ok {
		data.RPMLimit = types.Int64Value(int64(rpmLimit))
	} else if data.RPMLimit.IsUnknown() {
		data.RPMLimit = types.Int64Null()
	}
	if maxParallel, ok := info["max_parallel_requests"].(float64); ok {
		data.MaxParallelRequests = types.Int64Value(int64(maxParallel))
	} else if data.MaxParallelRequests.IsUnknown() {
		data.MaxParallelRequests = types.Int64Null()
	}
	if softBudget, ok := info["soft_budget"].(float64); ok {
		data.SoftBudget = types.Float64Value(softBudget)
	} else if data.SoftBudget.IsUnknown() {
		data.SoftBudget = types.Float64Null()
	}
	if blocked, ok := info["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
	} else if data.Blocked.IsUnknown() {
		data.Blocked = types.BoolNull()
	}
	if orgID, ok := info["organization_id"].(string); ok && orgID != "" {
		data.OrganizationID = types.StringValue(orgID)
	}
	// Only set budget_id if the user explicitly configured it or if the
	// current value is unknown (needs resolving). The API auto-creates budgets
	// but we don't want to adopt server-side budget IDs into state.
	if budgetID, ok := info["budget_id"].(string); ok && budgetID != "" {
		if !data.BudgetID.IsNull() {
			data.BudgetID = types.StringValue(budgetID)
		}
	}
	if keyAlias, ok := info["key_alias"].(string); ok && keyAlias != "" {
		data.KeyAlias = types.StringValue(keyAlias)
	} else if data.KeyAlias.IsUnknown() {
		data.KeyAlias = types.StringNull()
	}
	if duration, ok := info["duration"].(string); ok && duration != "" {
		data.Duration = types.StringValue(duration)
	}
	if tpmLimitType, ok := info["tpm_limit_type"].(string); ok && tpmLimitType != "" {
		data.TPMLimitType = types.StringValue(tpmLimitType)
	}
	if rpmLimitType, ok := info["rpm_limit_type"].(string); ok && rpmLimitType != "" {
		data.RPMLimitType = types.StringValue(rpmLimitType)
	}
	if budgetDuration, ok := info["budget_duration"].(string); ok && budgetDuration != "" {
		data.BudgetDuration = types.StringValue(budgetDuration)
	}
	if teamID, ok := info["team_id"].(string); ok && teamID != "" {
		data.TeamID = types.StringValue(teamID)
	}
	if userID, ok := info["user_id"].(string); ok && userID != "" {
		data.UserID = types.StringValue(userID)
	}
	// "key" may be at top level or inside "info" (as "token" or "key").
	// Only update data.Key when it is currently unknown or null (i.e. the key
	// was auto-generated and we need to capture it).  When the user supplied a
	// custom key value it is already known and must NOT be overwritten — the
	// /key/info endpoint returns a hashed token, not the raw key, so
	// overwriting would cause "inconsistent values for sensitive attribute".
	if data.Key.IsUnknown() || data.Key.IsNull() {
		if keyValue, ok := result["key"].(string); ok && keyValue != "" {
			data.Key = types.StringValue(keyValue)
			data.ID = types.StringValue(hashKeyForID(keyValue))
		} else if keyValue, ok := info["token"].(string); ok && keyValue != "" {
			data.Key = types.StringValue(keyValue)
			data.ID = types.StringValue(hashKeyForID(keyValue))
		}
	}

	// Handle models list - preserve null when API returns empty and config didn't specify models
	if models, ok := info["models"].([]interface{}); ok && len(models) > 0 {
		modelsList := make([]attr.Value, 0, len(models))
		for _, m := range models {
			if str, ok := m.(string); ok {
				modelsList = append(modelsList, types.StringValue(str))
			}
		}
		data.Models, _ = types.ListValue(types.StringType, modelsList)
	} else if !data.Models.IsNull() {
		data.Models, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle allowed_routes list - preserve null when API returns empty and config didn't specify allowed_routes
	if routes, ok := info["allowed_routes"].([]interface{}); ok && len(routes) > 0 {
		routesList := make([]attr.Value, 0, len(routes))
		for _, r := range routes {
			if str, ok := r.(string); ok {
				routesList = append(routesList, types.StringValue(str))
			}
		}
		data.AllowedRoutes, _ = types.ListValue(types.StringType, routesList)
	} else if !data.AllowedRoutes.IsNull() {
		data.AllowedRoutes, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle allowed_passthrough_routes list - preserve null when API returns empty and config didn't specify allowed_passthrough_routes
	if routes, ok := info["allowed_passthrough_routes"].([]interface{}); ok && len(routes) > 0 {
		routesList := make([]attr.Value, 0, len(routes))
		for _, r := range routes {
			if str, ok := r.(string); ok {
				routesList = append(routesList, types.StringValue(str))
			}
		}
		data.AllowedPassthroughRoutes, _ = types.ListValue(types.StringType, routesList)
	} else if !data.AllowedPassthroughRoutes.IsNull() {
		data.AllowedPassthroughRoutes, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle allowed_cache_controls list - preserve null when API returns empty and config didn't specify allowed_cache_controls
	if controls, ok := info["allowed_cache_controls"].([]interface{}); ok && len(controls) > 0 {
		controlsList := make([]attr.Value, 0, len(controls))
		for _, c := range controls {
			if str, ok := c.(string); ok {
				controlsList = append(controlsList, types.StringValue(str))
			}
		}
		data.AllowedCacheControls, _ = types.ListValue(types.StringType, controlsList)
	} else if !data.AllowedCacheControls.IsNull() {
		data.AllowedCacheControls, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle guardrails list - preserve null when API returns empty and config didn't specify guardrails
	if guardrails, ok := info["guardrails"].([]interface{}); ok && len(guardrails) > 0 {
		guardrailsList := make([]attr.Value, 0, len(guardrails))
		for _, g := range guardrails {
			if str, ok := g.(string); ok {
				guardrailsList = append(guardrailsList, types.StringValue(str))
			}
		}
		data.Guardrails, _ = types.ListValue(types.StringType, guardrailsList)
	} else if !data.Guardrails.IsNull() {
		data.Guardrails, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle prompts list - preserve null when API returns empty and config didn't specify prompts
	if prompts, ok := info["prompts"].([]interface{}); ok && len(prompts) > 0 {
		promptsList := make([]attr.Value, 0, len(prompts))
		for _, p := range prompts {
			if str, ok := p.(string); ok {
				promptsList = append(promptsList, types.StringValue(str))
			}
		}
		data.Prompts, _ = types.ListValue(types.StringType, promptsList)
	} else if !data.Prompts.IsNull() {
		data.Prompts, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle enforced_params list - preserve null when API returns empty and config didn't specify enforced_params
	if enforcedParams, ok := info["enforced_params"].([]interface{}); ok && len(enforcedParams) > 0 {
		paramsList := make([]attr.Value, 0, len(enforcedParams))
		for _, p := range enforcedParams {
			if str, ok := p.(string); ok {
				paramsList = append(paramsList, types.StringValue(str))
			}
		}
		data.EnforcedParams, _ = types.ListValue(types.StringType, paramsList)
	} else if !data.EnforcedParams.IsNull() {
		data.EnforcedParams, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle tags list - preserve null when API returns empty and config didn't specify tags.
	// LiteLLM stores tags inside metadata["tags"] rather than as a top-level field in /key/info,
	// so we check both locations.
	var rawTags []interface{}
	if tags, ok := info["tags"].([]interface{}); ok {
		rawTags = tags
	} else if metadata, ok := info["metadata"].(map[string]interface{}); ok {
		if tags, ok := metadata["tags"].([]interface{}); ok {
			rawTags = tags
		}
	}
	if len(rawTags) > 0 {
		tagsList := make([]attr.Value, 0, len(rawTags))
		for _, t := range rawTags {
			if str, ok := t.(string); ok {
				tagsList = append(tagsList, types.StringValue(str))
			}
		}
		data.Tags, _ = types.ListValue(types.StringType, tagsList)
	} else if !data.Tags.IsNull() {
		data.Tags, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle router_settings - preserve null when user didn't configure it.
	if data.RouterSettings.IsNull() {
		// Preserve null - do not repopulate from API
	} else if routerSettings, ok := info["router_settings"].(map[string]interface{}); ok && len(routerSettings) > 0 {
		data.RouterSettings = parseKeyRouterSettingsFromAPI(routerSettings)
	} else {
		data.RouterSettings = types.ObjectNull(keyRouterSettingsAttrTypes)
	}

	// Handle metadata map - preserve null when API returns empty and config didn't specify metadata.
	// The API may inject internal keys (e.g. tpm_limit_type, rpm_limit_type) into metadata.
	// Only include keys that were in the user's original config to avoid drift.
	if metadata, ok := info["metadata"].(map[string]interface{}); ok && len(metadata) > 0 {
		// Build set of user-configured metadata keys
		configuredKeys := make(map[string]bool)
		if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
			var currentMeta map[string]string
			data.Metadata.ElementsAs(ctx, &currentMeta, false)
			for k := range currentMeta {
				configuredKeys[k] = true
			}
		}

		metaMap := make(map[string]attr.Value)
		for k, v := range metadata {
			// If user had specific keys, only keep those. Otherwise keep all.
			if len(configuredKeys) > 0 && !configuredKeys[k] {
				continue
			}
			metaMap[k] = types.StringValue(valueToJSONString(v))
		}
		if len(metaMap) > 0 {
			data.Metadata, _ = types.MapValue(types.StringType, metaMap)
		} else if data.Metadata.IsUnknown() {
			data.Metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
		}
	} else if data.Metadata.IsUnknown() {
		data.Metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle aliases map - preserve null when API returns empty and config didn't specify aliases
	if aliases, ok := info["aliases"].(map[string]interface{}); ok && len(aliases) > 0 {
		aliasMap := make(map[string]attr.Value)
		for k, v := range aliases {
			if str, ok := v.(string); ok {
				aliasMap[k] = types.StringValue(str)
			}
		}
		data.Aliases, _ = types.MapValue(types.StringType, aliasMap)
	} else if !data.Aliases.IsNull() {
		data.Aliases, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle config map - preserve null when API returns empty and config didn't specify config
	if configMapRaw, ok := info["config"].(map[string]interface{}); ok && len(configMapRaw) > 0 {
		configMap := make(map[string]attr.Value)
		for k, v := range configMapRaw {
			if str, ok := v.(string); ok {
				configMap[k] = types.StringValue(str)
			}
		}
		data.Config, _ = types.MapValue(types.StringType, configMap)
	} else if !data.Config.IsNull() {
		data.Config, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle permissions map - preserve null when API returns empty and config didn't specify permissions
	if permissions, ok := info["permissions"].(map[string]interface{}); ok && len(permissions) > 0 {
		permMap := make(map[string]attr.Value)
		for k, v := range permissions {
			if str, ok := v.(string); ok {
				permMap[k] = types.StringValue(str)
			}
		}
		data.Permissions, _ = types.MapValue(types.StringType, permMap)
	} else if !data.Permissions.IsNull() {
		data.Permissions, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle model_max_budget map - preserve null when API returns empty and config didn't specify model_max_budget
	if modelMaxBudget, ok := info["model_max_budget"].(map[string]interface{}); ok && len(modelMaxBudget) > 0 {
		budgetMap := make(map[string]attr.Value)
		for k, v := range modelMaxBudget {
			if num, ok := v.(float64); ok {
				budgetMap[k] = types.Float64Value(num)
			}
		}
		data.ModelMaxBudget, _ = types.MapValue(types.Float64Type, budgetMap)
	} else if !data.ModelMaxBudget.IsNull() {
		data.ModelMaxBudget, _ = types.MapValue(types.Float64Type, map[string]attr.Value{})
	}

	// Handle model_rpm_limit map
	// The API may not echo back per-model limits, so only clear on Unknown.
	if modelRPM, ok := info["model_rpm_limit"].(map[string]interface{}); ok && len(modelRPM) > 0 {
		rpmMap := make(map[string]attr.Value)
		for k, v := range modelRPM {
			if num, ok := v.(float64); ok {
				rpmMap[k] = types.Int64Value(int64(num))
			}
		}
		data.ModelRPMLimit, _ = types.MapValue(types.Int64Type, rpmMap)
	} else if data.ModelRPMLimit.IsUnknown() {
		data.ModelRPMLimit, _ = types.MapValue(types.Int64Type, map[string]attr.Value{})
	}

	// Handle model_tpm_limit map
	// The API may not echo back per-model limits, so only clear on Unknown.
	if modelTPM, ok := info["model_tpm_limit"].(map[string]interface{}); ok && len(modelTPM) > 0 {
		tpmMap := make(map[string]attr.Value)
		for k, v := range modelTPM {
			if num, ok := v.(float64); ok {
				tpmMap[k] = types.Int64Value(int64(num))
			}
		}
		data.ModelTPMLimit, _ = types.MapValue(types.Int64Type, tpmMap)
	} else if data.ModelTPMLimit.IsUnknown() {
		data.ModelTPMLimit, _ = types.MapValue(types.Int64Type, map[string]attr.Value{})
	}

	return nil
}
