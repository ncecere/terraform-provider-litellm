package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
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
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var _ resource.Resource = &KeyResource{}
var _ resource.ResourceWithImportState = &KeyResource{}
var _ resource.ResourceWithUpgradeState = &KeyResource{}
var _ resource.ResourceWithConfigValidators = &KeyResource{}
var _ resource.ResourceWithModifyPlan = &KeyResource{}

func (r *KeyResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.RequiredTogether(
			path.MatchRoot("key_wo"),
			path.MatchRoot("key_wo_version"),
		),
		resourcevalidator.Conflicting(
			path.MatchRoot("key"),
			path.MatchRoot("key_wo"),
		),
	}
}

// hashKeyForID produces the non-sensitive management identifier used by this
// provider. Because an unsalted digest permits offline guesses, callers should
// use cryptographically random, high-entropy predefined keys.
// Format: "sha256:<hex digest>".
func hashKeyForID(rawKey string) string {
	h := sha256.Sum256([]byte(rawKey))
	return fmt.Sprintf("sha256:%x", h)
}

func keyHashFromID(id string) (string, error) {
	const prefix = "sha256:"
	if !strings.HasPrefix(id, prefix) {
		return "", fmt.Errorf("key ID %q does not contain a SHA256 key hash", id)
	}
	hash := strings.TrimPrefix(id, prefix)
	if len(hash) != sha256.Size*2 {
		return "", fmt.Errorf("key ID contains an invalid SHA256 hash length")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", fmt.Errorf("key ID contains an invalid SHA256 hash: %w", err)
	}
	return strings.ToLower(hash), nil
}

func keyLookupIdentifier(data *KeyResourceModel) (string, error) {
	if !data.KeyWOVersion.IsNull() && !data.KeyWOVersion.IsUnknown() {
		return keyHashFromID(data.ID.ValueString())
	}
	key := data.Key.ValueString()
	if key == "" {
		return "", fmt.Errorf("key value is empty, cannot identify the LiteLLM key")
	}
	return key, nil
}

func writeOnlyKeyCreateError(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("LiteLLM returned HTTP %d while creating the write-only key. The response body was omitted because it may contain the submitted secret.", apiErr.StatusCode)
	}
	return "The write-only key request failed. Error details were omitted because an intermediary may include the submitted secret."
}

func keyResourceReadError(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("LiteLLM returned HTTP %d while reading the key. Response details were omitted because they may contain key dictionary values or the lookup token.", apiErr.StatusCode)
	}
	return "The key read failed. Transport details were omitted because they may contain key dictionary values or the lookup token."
}

func keyResourceUpdateError(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("LiteLLM returned HTTP %d while updating the key. Response details were omitted because they may contain key dictionary values or the lookup token.", apiErr.StatusCode)
	}
	return "The key update failed. Transport details were omitted because they may contain key dictionary values or the lookup token."
}

func keyResourceDeleteError(err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("LiteLLM returned HTTP %d while deleting the key. Response details were omitted because they may contain the lookup token.", apiErr.StatusCode)
	}
	return "The key delete failed. Transport details were omitted because they may contain the lookup token."
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
	KeyWO                    types.String  `tfsdk:"key_wo"`
	KeyWOVersion             types.String  `tfsdk:"key_wo_version"`
	SendInviteEmail          types.Bool    `tfsdk:"send_invite_email"`
	Models                   types.List    `tfsdk:"models"`
	AllowedRoutes            types.List    `tfsdk:"allowed_routes"`
	AllowedPassthroughRoutes types.List    `tfsdk:"allowed_passthrough_routes"`
	MaxBudget                types.Float64 `tfsdk:"max_budget"`
	UserID                   types.String  `tfsdk:"user_id"`
	TeamID                   types.String  `tfsdk:"team_id"`
	OrganizationID           types.String  `tfsdk:"organization_id"`
	ProjectID                types.String  `tfsdk:"project_id"`
	BudgetID                 types.String  `tfsdk:"budget_id"`
	ServiceAccountID         types.String  `tfsdk:"service_account_id"`
	MaxParallelRequests      types.Int64   `tfsdk:"max_parallel_requests"`
	Metadata                 types.Map     `tfsdk:"metadata"`
	MetadataJSON             types.String  `tfsdk:"metadata_json"`
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
	ConfigJSON               types.String  `tfsdk:"config_json"`
	Permissions              types.Map     `tfsdk:"permissions"`
	PermissionsJSON          types.String  `tfsdk:"permissions_json"`
	ModelMaxBudget           types.Map     `tfsdk:"model_max_budget"`
	ModelRPMLimit            types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit            types.Map     `tfsdk:"model_tpm_limit"`
	Guardrails               types.List    `tfsdk:"guardrails"`
	Prompts                  types.List    `tfsdk:"prompts"`
	EnforcedParams           types.List    `tfsdk:"enforced_params"`
	Tags                     types.List    `tfsdk:"tags"`
	Blocked                  types.Bool    `tfsdk:"blocked"`
	RouterSettings           types.Object  `tfsdk:"router_settings"`
}

func (r *KeyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_key"
}

func (r *KeyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM API key.",
		Version:     2,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Non-sensitive unique identifier for this key (SHA256 hash of the key value).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"key": schema.StringAttribute{
				Description: "The API key value. If not specified, a key will be generated. Use key_wo instead to avoid storing a predefined key in Terraform artifacts.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"key_wo": schema.StringAttribute{
				Description: "Write-only predefined API key value. Terraform sends this value to LiteLLM but does not store it in plan or state artifacts. Use a cryptographically random, high-entropy key because its persisted SHA256 management identifier permits offline guesses of weak values. Requires Terraform 1.11 or compatible OpenTofu support.",
				Optional:    true,
				Sensitive:   true,
				WriteOnly:   true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"key_wo_version": schema.StringAttribute{
				Description: "Persisted version or nonce for key_wo. Change it when the write-only key changes; changing or removing it replaces the LiteLLM key.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"send_invite_email": schema.BoolAttribute{
				Description: "Create-only action flag that asks LiteLLM to asynchronously email the key's existing user. Requires user_id. It is write-only, is never sent during Update, and does not confirm delivery.",
				Optional:    true,
				WriteOnly:   true,
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
			"project_id": schema.StringAttribute{
				Description: "Project ID associated with this key. When set, models and budget are validated against the project's limits.",
				Optional:    true,
			},
			"budget_id": schema.StringAttribute{
				Description: "Budget ID to associate with this key.",
				Optional:    true,
			},
			"service_account_id": schema.StringAttribute{
				Description: "Service account ID for team-owned keys. LiteLLM v1.98 does not update this identity in place.",
				Optional:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"max_parallel_requests": schema.Int64Attribute{
				Description: "Maximum parallel requests allowed.",
				Optional:    true,
				Computed:    true,
			},
			"metadata": schema.MapAttribute{
				Description: "Metadata for the key. Marked sensitive because values may contain API keys or other credentials.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				ElementType: types.StringType,
			},
			"metadata_json": schema.StringAttribute{
				Description: "Additional key metadata as a semantic JSON object.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				Validators: []validator.String{
					keySemanticDictionaryValidator{},
				},
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
				Description: "TPM limit enforcement type. LiteLLM v1.98 accepts guaranteed_throughput, best_effort_throughput, or dynamic for keys.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("guaranteed_throughput", "best_effort_throughput", "dynamic"),
				},
			},
			"rpm_limit_type": schema.StringAttribute{
				Description: "RPM limit enforcement type. LiteLLM v1.98 accepts guaranteed_throughput, best_effort_throughput, or dynamic for keys.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("guaranteed_throughput", "best_effort_throughput", "dynamic"),
				},
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
			"config_json": schema.StringAttribute{
				Description: "Additional key configuration as a semantic JSON object.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				Validators: []validator.String{
					keySemanticDictionaryValidator{},
				},
			},
			"permissions": schema.MapAttribute{
				Description: "Key permissions.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"permissions_json": schema.StringAttribute{
				Description: "Additional key permissions as a semantic JSON object.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				Validators: []validator.String{
					keySemanticDictionaryValidator{},
				},
			},
			"model_max_budget": schema.MapAttribute{
				Description: "Per-model budget limits.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Float64Type,
				Validators:  []validator.Map{mapvalidator.NoNullValues()},
			},
			"model_rpm_limit": schema.MapAttribute{
				Description: "Per-model RPM limits.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
				Validators:  []validator.Map{mapvalidator.NoNullValues()},
			},
			"model_tpm_limit": schema.MapAttribute{
				Description: "Per-model TPM limits.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
				Validators:  []validator.Map{mapvalidator.NoNullValues()},
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
			"blocked": schema.BoolAttribute{
				Description: "Whether the key is blocked.",
				Optional:    true,
				Computed:    true,
			},
			"router_settings": keyRouterSettingsResourceAttribute(),
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

func (r *KeyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, state, config KeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	semanticChanged := false
	entries := []struct {
		name       string
		privateKey string
		configured types.String
		prior      types.String
		legacy     types.Map
		reserved   []string
		planValue  *types.String
		planLegacy *types.Map
	}{
		{"metadata_json", keyMetadataJSONProvenancePrivateKey, config.MetadataJSON, state.MetadataJSON, config.Metadata, keyMetadataJSONReservedKeys, &plan.MetadataJSON, &plan.Metadata},
		{"config_json", keyConfigJSONProvenancePrivateKey, config.ConfigJSON, state.ConfigJSON, config.Config, nil, &plan.ConfigJSON, &plan.Config},
		{"permissions_json", keyPermissionsJSONProvenancePrivateKey, config.PermissionsJSON, state.PermissionsJSON, config.Permissions, nil, &plan.PermissionsJSON, &plan.Permissions},
	}
	for _, entry := range entries {
		raw, diagnostics := req.Private.GetKey(ctx, entry.privateKey)
		resp.Diagnostics.Append(diagnostics...)
		if resp.Diagnostics.HasError() {
			return
		}
		provenance, err := decodeKeySemanticDictionaryProvenance(ctx, raw, entry.prior)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root(entry.name), "Invalid Semantic Dictionary Provenance", "Private ownership state is missing, malformed, or inconsistent with public state. No key plan was produced.")
			return
		}
		if entry.configured.IsUnknown() {
			semanticChanged = true
			// Preserve Terraform's unknown value through planning. Apply performs
			// complete validation after interpolation resolves and sends no request
			// if it remains unknown.
			*entry.planValue = types.StringUnknown()
			continue
		}
		object, _, err := keySemanticDictionaryConfiguration(ctx, entry.configured, entry.legacy, entry.reserved)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root(entry.name), "Invalid Semantic Key Dictionary", "The JSON object is malformed or overlaps another managed key surface. No key plan was produced.")
			return
		}
		changed, err := keySemanticDictionaryNeedsChange(ctx, entry.configured, entry.prior, provenance)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root(entry.name), "Invalid Semantic Key Dictionary", "The JSON object or its ownership state could not be compared safely. No key plan was produced.")
			return
		}
		semanticChanged = semanticChanged || changed
		if !provenance.Configured && entry.configured.IsNull() {
			*entry.planValue = types.StringNull()
		}
		if !changed && provenance.Configured && !entry.configured.IsNull() && !entry.configured.IsUnknown() {
			// Formatting-only JSON changes retain the configured spelling already
			// stored in state and must not schedule a state-only key mutation.
			*entry.planValue = entry.prior
		}
		if changed && entry.configured.IsNull() {
			// Optional+Computed omission otherwise retains state and suppresses Apply.
			*entry.planValue = types.StringUnknown()
		}
		if object != nil && entry.legacy.IsNull() {
			filtered, filterErr := excludeKeyLegacyJSONTopLevelKeys(ctx, *entry.planLegacy, object)
			if filterErr != nil {
				resp.Diagnostics.AddAttributeError(path.Root(entry.name), "Invalid Semantic Key Dictionary", "The JSON-owned legacy projection could not be produced safely. No key plan was produced.")
				return
			}
			*entry.planLegacy = filtered
		}
	}
	if !semanticChanged && !keyHasNonSemanticConfigurationChange(config, state) {
		plan = state
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *KeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data KeyResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var configuredKey, writeOnlyKey types.String
	var sendInviteEmail types.Bool
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("key"), &configuredKey)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("key_wo"), &writeOnlyKey)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("send_invite_email"), &sendInviteEmail)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if writeOnlyKey.IsUnknown() {
		resp.Diagnostics.AddError("Invalid Write-Only Key", "key_wo must be known during apply; the provider will not fall back to generating and storing a key.")
		return
	}
	writeOnlyMode := !writeOnlyKey.IsNull()
	if writeOnlyMode {
		if writeOnlyKey.ValueString() == "" {
			resp.Diagnostics.AddError("Invalid Write-Only Key", "key_wo must be non-empty during apply.")
			return
		}
		if data.KeyWOVersion.IsNull() || data.KeyWOVersion.IsUnknown() || data.KeyWOVersion.ValueString() == "" {
			resp.Diagnostics.AddError("Invalid Write-Only Key Version", "key_wo_version must be known and non-empty whenever key_wo is configured.")
			return
		}
	} else if !data.KeyWOVersion.IsNull() {
		resp.Diagnostics.AddError("Invalid Write-Only Key Version", "key_wo_version cannot be configured without key_wo.")
		return
	}

	var semanticConfig KeyResourceModel
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("metadata"), &semanticConfig.Metadata)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("metadata_json"), &semanticConfig.MetadataJSON)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("config"), &semanticConfig.Config)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("config_json"), &semanticConfig.ConfigJSON)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("permissions"), &semanticConfig.Permissions)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("permissions_json"), &semanticConfig.PermissionsJSON)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if semanticConfig.MetadataJSON.IsUnknown() || semanticConfig.ConfigJSON.IsUnknown() || semanticConfig.PermissionsJSON.IsUnknown() {
		resp.Diagnostics.AddError("Unknown Semantic Key Dictionary", "All semantic JSON key dictionaries must be known before creating a key.")
		return
	}
	prepared, err := prepareKeySemanticDictionaries(ctx, semanticConfig)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Key Dictionary", "A semantic JSON object is malformed, overlaps another managed key surface, or cannot be persisted exactly. No request was sent.")
		return
	}
	privateValues, err := encodeKeySemanticProvenance(ctx, prepared)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No request was sent.")
		return
	}
	if prepared.anyConfigured() && !writeOnlyMode && (configuredKey.IsNull() || configuredKey.IsUnknown() || configuredKey.ValueString() == "") {
		resp.Diagnostics.AddError("Explicit Key Identity Required", "Semantic key dictionaries require a caller-selected key or key_wo so an accepted create can be reconciled without list discovery. No request was sent.")
		return
	}
	data.MetadataJSON = semanticConfig.MetadataJSON
	data.ConfigJSON = semanticConfig.ConfigJSON
	data.PermissionsJSON = semanticConfig.PermissionsJSON

	keyReq, conversionDiagnostics := r.buildKeyRequest(ctx, &data)
	resp.Diagnostics.Append(conversionDiagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := overlayKeyCreateSemanticDictionaries(ctx, keyReq, prepared); err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Key Dictionary", "The semantic JSON objects could not be overlaid safely. No request was sent.")
		return
	}

	// Invitation validation can issue a recipient lookup. Complete every local
	// collection conversion before that first HTTP preflight request.
	if err := r.validateKeyInviteRecipient(ctx, &data, sendInviteEmail); err != nil {
		resp.Diagnostics.AddError("Invalid Key Invitation", err.Error())
		return
	}

	if writeOnlyMode {
		keyReq["key"] = writeOnlyKey.ValueString()
	}
	if err := addSendInviteEmailToCreateRequest(keyReq, sendInviteEmail); err != nil {
		resp.Diagnostics.AddError("Invalid Key Invitation", err.Error())
		return
	}

	endpoint := "/key/generate"
	if !data.ServiceAccountID.IsNull() && data.ServiceAccountID.ValueString() != "" {
		endpoint = "/key/service-account/generate"
	}

	var result map[string]interface{}
	accepted := false
	var createErr error
	if prepared.anyConfigured() {
		accepted, createErr = r.client.doRequestWithResponse(ctx, "POST", endpoint, keyReq, &result)
	} else {
		createErr = r.client.DoRequestWithResponse(ctx, "POST", endpoint, keyReq, &result)
	}
	requestedIdentity := configuredKey.ValueString()
	if writeOnlyMode {
		requestedIdentity = writeOnlyKey.ValueString()
	}
	retainAcceptedCreate := func(title, detail string) {
		// The request context may be canceled after LiteLLM accepted the mutation.
		// Local recovery state must still be serializable without dispatching any
		// further request.
		recoveryCtx := context.WithoutCancel(ctx)
		recovery := partialKeySemanticRecoveryState(data, requestedIdentity, writeOnlyMode)
		unconfigured := keyUnconfiguredSemanticDictionaryProvenance()
		recoveryPrivate, privateErr := encodeKeySemanticProvenance(recoveryCtx, keySemanticPrepared{
			metadataProvenance: unconfigured, configProvenance: unconfigured, permissionsProvenance: unconfigured,
		})
		if privateErr == nil && resp.Private != nil {
			for name, value := range recoveryPrivate {
				resp.Diagnostics.Append(resp.Private.SetKey(recoveryCtx, name, value)...)
			}
			resp.Diagnostics.Append(resp.Private.SetKey(recoveryCtx, keyAcceptedCreateRecoveryPrivateKey, []byte("true"))...)
		}
		resp.Diagnostics.Append(resp.State.Set(recoveryCtx, &recovery)...)
		resp.Diagnostics.AddError(title, detail)
	}
	if createErr != nil {
		if prepared.anyConfigured() && accepted {
			retainAcceptedCreate("Key Creation Not Confirmed", "LiteLLM accepted the key create, but its response could not be decoded safely. Only the caller-selected identity was retained for authoritative recovery.")
		} else if writeOnlyMode {
			resp.Diagnostics.AddError("Write-Only Key Creation Error", writeOnlyKeyCreateError(createErr))
		} else {
			resp.Diagnostics.AddError("Client Error", "Unable to create key. Response and transport details were omitted because they may contain key material.")
		}
		return
	}
	if prepared.anyConfigured() {
		if err := validateKeyCreateResponseIdentity(result, requestedIdentity); err != nil {
			retainAcceptedCreate("Key Creation Identity Not Confirmed", "LiteLLM accepted the key create, but the response identity was missing or contradictory. Only the caller-selected identity was retained for authoritative recovery.")
			return
		}
	}

	if writeOnlyMode {
		data.ID = types.StringValue(hashKeyForID(writeOnlyKey.ValueString()))
		data.Key = types.StringNull()
		data.KeyWO = types.StringNull()
	} else if keyVal, ok := result["key"].(string); ok {
		data.Key = types.StringValue(keyVal)
		data.ID = types.StringValue(hashKeyForID(keyVal))
	}

	if prepared.anyConfigured() {
		if err := r.readKeyWithOwnership(ctx, &data, false, prepared.ownership(true)); err != nil {
			retainAcceptedCreate("Semantic Key Dictionary Not Confirmed", "LiteLLM accepted the key create, but authoritative readback did not confirm the semantic dictionaries. Only the caller-selected identity was retained for recovery.")
			return
		}
	} else if err := r.readKey(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", keyResourceReadError(err))
	}

	if !data.RouterSettings.IsNull() {
		if err := r.waitForKeyRouterSettings(ctx, &data); err != nil {
			resp.Diagnostics.AddError("Router Settings Did Not Converge", "The key was created, but "+err.Error()+". Terraform retained the key identity for recovery.")
		}
	}
	localCtx := ctx
	if resp.Diagnostics.HasError() {
		localCtx = context.WithoutCancel(ctx)
	}
	if resp.Private != nil {
		for name, value := range privateValues {
			resp.Diagnostics.Append(resp.Private.SetKey(localCtx, name, value)...)
		}
		resp.Diagnostics.Append(resp.Private.SetKey(localCtx, keyAcceptedCreateRecoveryPrivateKey, nil)...)
	}
	resp.Diagnostics.Append(resp.State.Set(localCtx, &data)...)
}

func (r *KeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data KeyResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	importedMarker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	acceptedCreateMarker, acceptedCreateDiags := req.Private.GetKey(ctx, keyAcceptedCreateRecoveryPrivateKey)
	pendingUpdateMarker, pendingUpdateDiags := req.Private.GetKey(ctx, keyPendingUpdatePrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	resp.Diagnostics.Append(acceptedCreateDiags...)
	resp.Diagnostics.Append(pendingUpdateDiags...)
	if len(acceptedCreateMarker) != 0 && string(acceptedCreateMarker) != "true" {
		resp.Diagnostics.AddError("Invalid Key Recovery State", "Accepted-create recovery state is malformed. No key read was performed.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(importedMarker) == "true"
	metadataMarker, metadataDiags := req.Private.GetKey(ctx, keyMetadataJSONProvenancePrivateKey)
	configMarker, configDiags := req.Private.GetKey(ctx, keyConfigJSONProvenancePrivateKey)
	permissionsMarker, permissionsDiags := req.Private.GetKey(ctx, keyPermissionsJSONProvenancePrivateKey)
	resp.Diagnostics.Append(metadataDiags...)
	resp.Diagnostics.Append(configDiags...)
	resp.Diagnostics.Append(permissionsDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	metadataProvenance, err := decodeKeySemanticDictionaryProvenance(ctx, metadataMarker, data.MetadataJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No key read was performed.")
		return
	}
	configProvenance, err := decodeKeySemanticDictionaryProvenance(ctx, configMarker, data.ConfigJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("config_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No key read was performed.")
		return
	}
	permissionsProvenance, err := decodeKeySemanticDictionaryProvenance(ctx, permissionsMarker, data.PermissionsJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("permissions_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No key read was performed.")
		return
	}
	pendingUpdate, err := decodeKeySemanticPendingTransition(ctx, pendingUpdateMarker)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Key Recovery State", "Pending semantic-update recovery state is malformed. No key read was performed.")
		return
	}
	acceptedRecovery := string(acceptedCreateMarker) == "true"
	reconcile := keySemanticPendingReconcile{}
	ownership := keySemanticReadOwnership{
		metadata: metadataProvenance, config: configProvenance, permissions: permissionsProvenance,
		pending: pendingUpdate, reconcile: &reconcile,
		acceptedCreateRecovery: acceptedRecovery, fresh: acceptedRecovery || pendingUpdate.any(),
	}

	if err := r.refreshKeyWithOwnership(ctx, &data, imported, ownership); err != nil {
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", keyResourceReadError(err))
		return
	}

	if reconcile.Present && reconcile.Committed {
		metadataProvenance = reconcile.Effective.metadata
		configProvenance = reconcile.Effective.config
		permissionsProvenance = reconcile.Effective.permissions
	}
	prepared := keySemanticPrepared{metadataProvenance: metadataProvenance, configProvenance: configProvenance, permissionsProvenance: permissionsProvenance}
	privateValues, err := encodeKeySemanticProvenance(ctx, prepared)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No key state was produced.")
		return
	}
	if resp.Private != nil {
		for name, value := range privateValues {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, name, value)...)
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() {
		if imported {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
		}
		if string(acceptedCreateMarker) == "true" {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, keyAcceptedCreateRecoveryPrivateKey, nil)...)
		}
		if reconcile.Present {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, keyPendingUpdatePrivateKey, nil)...)
		}
	}
}

func (r *KeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, state, config KeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("metadata"), &config.Metadata)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("metadata_json"), &config.MetadataJSON)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("config"), &config.Config)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("config_json"), &config.ConfigJSON)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("permissions"), &config.Permissions)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("permissions_json"), &config.PermissionsJSON)...)
	acceptedCreateMarker, acceptedCreateDiags := req.Private.GetKey(ctx, keyAcceptedCreateRecoveryPrivateKey)
	pendingUpdateMarker, pendingUpdateDiags := req.Private.GetKey(ctx, keyPendingUpdatePrivateKey)
	resp.Diagnostics.Append(acceptedCreateDiags...)
	resp.Diagnostics.Append(pendingUpdateDiags...)
	if len(pendingUpdateMarker) != 0 {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Key Recovery Required", "A prior semantic key update has not been reconciled. Refresh must determine whether its removals committed before another update can be sent.")
		return
	}
	if string(acceptedCreateMarker) == "true" {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Key Recovery Required", "A prior key create was accepted without complete readback. Refresh must reconcile the caller-selected identity before any update can be sent.")
		return
	}
	if len(acceptedCreateMarker) != 0 {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Invalid Key Recovery State", "Accepted-create recovery state is malformed. No key update was sent.")
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if config.MetadataJSON.IsUnknown() || config.ConfigJSON.IsUnknown() || config.PermissionsJSON.IsUnknown() {
		resp.Diagnostics.AddError("Unknown Semantic Key Dictionary", "All semantic JSON key dictionaries must be known before updating a key.")
		return
	}

	metadataMarker, metadataDiags := req.Private.GetKey(ctx, keyMetadataJSONProvenancePrivateKey)
	configMarker, configDiags := req.Private.GetKey(ctx, keyConfigJSONProvenancePrivateKey)
	permissionsMarker, permissionsDiags := req.Private.GetKey(ctx, keyPermissionsJSONProvenancePrivateKey)
	resp.Diagnostics.Append(metadataDiags...)
	resp.Diagnostics.Append(configDiags...)
	resp.Diagnostics.Append(permissionsDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	priorMetadata, err := decodeKeySemanticDictionaryProvenance(ctx, metadataMarker, state.MetadataJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No key request was sent.")
		return
	}
	priorConfig, err := decodeKeySemanticDictionaryProvenance(ctx, configMarker, state.ConfigJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("config_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No key request was sent.")
		return
	}
	priorPermissions, err := decodeKeySemanticDictionaryProvenance(ctx, permissionsMarker, state.PermissionsJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("permissions_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No key request was sent.")
		return
	}
	priorOwnership := keySemanticReadOwnership{metadata: priorMetadata, config: priorConfig, permissions: priorPermissions}

	prepared, err := prepareKeySemanticDictionaries(ctx, config)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Key Dictionary", "A semantic JSON object is malformed, overlaps another managed key surface, or cannot be persisted exactly. No request was sent.")
		return
	}
	privateValues, err := encodeKeySemanticProvenance(ctx, prepared)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No request was sent.")
		return
	}
	confirmationOwnership, err := prepared.updateOwnership(ctx, priorOwnership)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic removal ownership could not be validated safely. No request was sent.")
		return
	}
	metadataChanged, err := keySemanticDictionaryNeedsChange(ctx, config.MetadataJSON, state.MetadataJSON, priorMetadata)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Key Dictionary", "The semantic value could not be compared safely. No request was sent.")
		return
	}
	configChanged, err := keySemanticDictionaryNeedsChange(ctx, config.ConfigJSON, state.ConfigJSON, priorConfig)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("config_json"), "Invalid Semantic Key Dictionary", "The semantic value could not be compared safely. No request was sent.")
		return
	}
	permissionsChanged, err := keySemanticDictionaryNeedsChange(ctx, config.PermissionsJSON, state.PermissionsJSON, priorPermissions)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("permissions_json"), "Invalid Semantic Key Dictionary", "The semantic value could not be compared safely. No request was sent.")
		return
	}
	semanticInvolved := prepared.anyConfigured() || priorMetadata.Configured || priorConfig.Configured || priorPermissions.Configured
	if semanticInvolved {
		metadataChanged = metadataChanged || (!config.Metadata.IsNull() && !data.Metadata.Equal(state.Metadata))
		configChanged = configChanged || (!config.Config.IsNull() && !data.Config.Equal(state.Config))
		permissionsChanged = permissionsChanged || (!config.Permissions.IsNull() && !data.Permissions.Equal(state.Permissions))
	}
	changedRoots := map[string]bool{"metadata": metadataChanged, "config": configChanged, "permissions": permissionsChanged}
	anyRootChanged := metadataChanged || configChanged || permissionsChanged
	pendingTransition := pendingKeySemanticTransition(confirmationOwnership)
	var pendingTransitionPrivate []byte
	if pendingTransition.any() {
		pendingTransitionPrivate, err = encodeKeySemanticPendingTransition(ctx, pendingTransition)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Pending semantic removals could not be encoded safely. No request was sent.")
			return
		}
	}

	data.ID = state.ID
	data.Key = state.Key
	data.KeyWO = types.StringNull()
	data.MetadataJSON = config.MetadataJSON
	data.ConfigJSON = config.ConfigJSON
	data.PermissionsJSON = config.PermissionsJSON

	keyIdentifier, err := keyLookupIdentifier(&data)
	if err != nil {
		resp.Diagnostics.AddError("Key Identity Error", "Unable to identify the key for update without exposing its value.")
		return
	}
	updateReq, conversionDiagnostics := r.buildKeyRequest(ctx, &data)
	resp.Diagnostics.Append(conversionDiagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	applyKeyRouterSettingsUpdateSemantics(updateReq, data.RouterSettings, state.RouterSettings)

	if semanticInvolved {
		// service_account_id is the only current dedicated provider field that
		// buildKeyRequest writes directly into the metadata root. Whenever it is
		// present, preserve semantic and API-owned siblings through a hydrated
		// whole-root replacement rather than deleting the metadata request.
		if _, present := updateReq["metadata"]; present && (prepared.metadataProvenance.Configured || priorMetadata.Configured) {
			metadataChanged = true
			changedRoots["metadata"] = true
			anyRootChanged = true
		}
		configuredRootData := data
		configuredRootData.Metadata = config.Metadata
		configuredRootData.Config = config.Config
		configuredRootData.Permissions = config.Permissions
		configuredRoots, diagnostics := r.buildKeyRequest(ctx, &configuredRootData)
		resp.Diagnostics.Append(diagnostics...)
		if resp.Diagnostics.HasError() {
			return
		}
		if anyRootChanged {
			_, info, hydrationErr := r.getFreshExactKeyInfo(ctx, &data)
			if hydrationErr != nil {
				resp.Diagnostics.AddError("Semantic Key Dictionary Hydration Failed", "The complete identity-bound key dictionaries could not be read safely. No update request was sent.")
				return
			}
			if err := replaceChangedKeySemanticDictionaries(ctx, updateReq, configuredRoots, info, prepared, priorOwnership, state, changedRoots); err != nil {
				resp.Diagnostics.AddError("Semantic Key Dictionary Hydration Failed", "The remote dictionaries were malformed, masked without owned plaintext, or inconsistent with private ownership. No update request was sent.")
				return
			}
		} else {
			delete(updateReq, "metadata")
			delete(updateReq, "config")
			delete(updateReq, "permissions")
		}
	}
	updateReq["key"] = keyIdentifier

	retainPriorUpdate := func(localCtx context.Context) {
		if len(pendingTransitionPrivate) != 0 && resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(localCtx, keyPendingUpdatePrivateKey, pendingTransitionPrivate)...)
		}
		resp.Diagnostics.Append(resp.State.Set(localCtx, &state)...)
	}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/key/update", updateReq, nil); err != nil {
		// A transport failure or non-success response cannot prove whether the
		// mutation committed. Retain the complete prior public/private state until
		// a later authoritative refresh reconciles it.
		retainPriorUpdate(context.WithoutCancel(ctx))
		resp.Diagnostics.AddError("Client Error", keyResourceUpdateError(err))
		return
	}

	if semanticInvolved {
		if err := r.readKeyWithOwnership(ctx, &data, false, confirmationOwnership); err != nil {
			retainPriorUpdate(context.WithoutCancel(ctx))
			resp.Diagnostics.AddError("Semantic Key Dictionary Update Not Confirmed", "LiteLLM accepted the update but a single authoritative identity-bound read did not confirm the owned dictionaries. Terraform retained prior public and private state.")
			return
		}
	} else if err := r.readKey(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", keyResourceReadError(err))
	}

	if !data.RouterSettings.IsNull() || !state.RouterSettings.IsNull() {
		if err := r.waitForKeyRouterSettings(ctx, &data); err != nil {
			resp.Diagnostics.AddError("Router Settings Did Not Converge", err.Error()+". The remote key may have changed; run Terraform again after reviewing the key.")
		}
	}
	localCtx := ctx
	if resp.Diagnostics.HasError() {
		localCtx = context.WithoutCancel(ctx)
	}
	if resp.Private != nil {
		for name, value := range privateValues {
			resp.Diagnostics.Append(resp.Private.SetKey(localCtx, name, value)...)
		}
		resp.Diagnostics.Append(resp.Private.SetKey(localCtx, keyPendingUpdatePrivateKey, nil)...)
	}
	resp.Diagnostics.Append(resp.State.Set(localCtx, &data)...)
}

func (r *KeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data KeyResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	acceptedCreateMarker, acceptedCreateDiags := req.Private.GetKey(ctx, keyAcceptedCreateRecoveryPrivateKey)
	pendingUpdateMarker, pendingUpdateDiags := req.Private.GetKey(ctx, keyPendingUpdatePrivateKey)
	resp.Diagnostics.Append(acceptedCreateDiags...)
	resp.Diagnostics.Append(pendingUpdateDiags...)
	if len(pendingUpdateMarker) != 0 {
		resp.Diagnostics.AddError("Key Recovery Required", "A prior semantic key update has not been reconciled. Refresh must determine whether its removals committed before deletion can be sent.")
		return
	}
	if string(acceptedCreateMarker) == "true" {
		resp.Diagnostics.AddError("Key Recovery Required", "A prior key create was accepted without complete readback. Refresh must reconcile the caller-selected identity before deletion can be sent.")
		return
	}
	if len(acceptedCreateMarker) != 0 {
		resp.Diagnostics.AddError("Invalid Key Recovery State", "Accepted-create recovery state is malformed. No key deletion was sent.")
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}

	keyIdentifier, err := keyLookupIdentifier(&data)
	if err != nil {
		resp.Diagnostics.AddError("Key Identity Error", "Unable to identify the key for deletion without exposing its value.")
		return
	}
	deleteReq := map[string]interface{}{
		"keys": []string{keyIdentifier},
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/key/delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", keyResourceDeleteError(err))
			return
		}
	}
}

func (r *KeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	unconfigured := keyUnconfiguredSemanticDictionaryProvenance()
	semanticPrivate, err := encodeKeySemanticProvenance(ctx, keySemanticPrepared{metadataProvenance: unconfigured, configProvenance: unconfigured, permissionsProvenance: unconfigured})
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Import ownership could not be initialized safely.")
		return
	}
	if resp.Private != nil {
		for name, value := range semanticPrivate {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, name, value)...)
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata_json"), types.StringNull())...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("config_json"), types.StringNull())...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("permissions_json"), types.StringNull())...)
	if resp.Diagnostics.HasError() {
		return
	}
	if strings.HasPrefix(req.ID, "sha256:") {
		if _, err := keyHashFromID(req.ID); err != nil {
			resp.Diagnostics.AddError("Invalid Write-Only Key Import", err.Error())
			return
		}
		// A hash import avoids placing the raw token in state. Version "1" is
		// the documented bootstrap value and must match configuration initially.
		canonicalHash, err := keyHashFromID(req.ID)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Write-Only Key Import", "The SHA256 management identifier is invalid.")
			return
		}
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), "sha256:"+canonicalHash)...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), types.StringNull())...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key_wo_version"), "1")...)
		if resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
		}
		return
	}

	// Legacy/stateful import accepts the raw API key and stores it in the
	// sensitive key attribute so existing workflows remain compatible.
	rawKey := req.ID
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), hashKeyForID(rawKey))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), rawKey)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
	}
}

// UpgradeState handles direct migrations to schema v2. Version 0 also hashes
// the historical raw key ID; both prior versions initialize semantic JSON as
// unconfigured typed nulls and never adopt remote dictionary data.
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
				priorState["metadata_json"] = json.RawMessage("null")
				priorState["config_json"] = json.RawMessage("null")
				priorState["permissions_json"] = json.RawMessage("null")

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
		1: {
			PriorSchema: nil,
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				if req.RawState == nil {
					resp.Diagnostics.AddError("Unable to Upgrade State", "RawState is nil. This is a bug in the provider.")
					return
				}
				var priorState map[string]json.RawMessage
				if err := json.Unmarshal(req.RawState.JSON, &priorState); err != nil {
					resp.Diagnostics.AddError("Unable to Upgrade State", "Failed to decode prior key state.")
					return
				}
				priorState["metadata_json"] = json.RawMessage("null")
				priorState["config_json"] = json.RawMessage("null")
				priorState["permissions_json"] = json.RawMessage("null")
				upgraded, err := json.Marshal(priorState)
				if err != nil {
					resp.Diagnostics.AddError("Unable to Upgrade State", "Failed to encode upgraded key state.")
					return
				}
				resp.DynamicValue = &tfprotov6.DynamicValue{JSON: upgraded}
			},
		},
	}
}

func (r *KeyResource) buildKeyRequest(ctx context.Context, data *KeyResourceModel) (map[string]interface{}, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	models, modelsState, converted := strictTerraformStringList(ctx, data.Models, path.Root("models"))
	diagnostics.Append(converted...)
	allowedRoutes, _, converted := strictTerraformStringList(ctx, data.AllowedRoutes, path.Root("allowed_routes"))
	diagnostics.Append(converted...)
	allowedPassthroughRoutes, _, converted := strictTerraformStringList(ctx, data.AllowedPassthroughRoutes, path.Root("allowed_passthrough_routes"))
	diagnostics.Append(converted...)
	allowedCacheControls, _, converted := strictTerraformStringList(ctx, data.AllowedCacheControls, path.Root("allowed_cache_controls"))
	diagnostics.Append(converted...)
	guardrails, _, converted := strictTerraformStringList(ctx, data.Guardrails, path.Root("guardrails"))
	diagnostics.Append(converted...)
	prompts, _, converted := strictTerraformStringList(ctx, data.Prompts, path.Root("prompts"))
	diagnostics.Append(converted...)
	enforcedParams, _, converted := strictTerraformStringList(ctx, data.EnforcedParams, path.Root("enforced_params"))
	diagnostics.Append(converted...)
	tags, _, converted := strictTerraformStringList(ctx, data.Tags, path.Root("tags"))
	diagnostics.Append(converted...)
	metadata, _, converted := strictTerraformStringMap(ctx, data.Metadata, path.Root("metadata"), true)
	diagnostics.Append(converted...)
	aliases, _, converted := strictTerraformStringMap(ctx, data.Aliases, path.Root("aliases"), false)
	diagnostics.Append(converted...)
	config, _, converted := strictTerraformStringMap(ctx, data.Config, path.Root("config"), false)
	diagnostics.Append(converted...)
	permissions, _, converted := strictTerraformStringMap(ctx, data.Permissions, path.Root("permissions"), false)
	diagnostics.Append(converted...)
	modelMaxBudget, _, converted := strictTerraformFloat64Map(ctx, data.ModelMaxBudget, path.Root("model_max_budget"))
	diagnostics.Append(converted...)
	modelRPMLimit, _, converted := strictTerraformInt64Map(ctx, data.ModelRPMLimit, path.Root("model_rpm_limit"))
	diagnostics.Append(converted...)
	modelTPMLimit, _, converted := strictTerraformInt64Map(ctx, data.ModelTPMLimit, path.Root("model_tpm_limit"))
	diagnostics.Append(converted...)
	if diagnostics.HasError() {
		return nil, diagnostics
	}

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
	if !data.ProjectID.IsNull() && !data.ProjectID.IsUnknown() && data.ProjectID.ValueString() != "" {
		keyReq["project_id"] = data.ProjectID.ValueString()
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

	// Models list - special handling for team models. A failed conversion has
	// already returned, so it can never activate the all-team sentinel.
	teamConfigured := !data.TeamID.IsNull() && !data.TeamID.IsUnknown() && data.TeamID.ValueString() != ""
	if modelsState == collectionValueEmpty && teamConfigured {
		models = []string{"all-team-models"}
	}
	if len(models) > 0 {
		keyReq["models"] = models
	} else if (modelsState == collectionValueNull || modelsState == collectionValueUnknown) && teamConfigured {
		keyReq["models"] = []string{"all-team-models"}
	}

	for _, item := range []struct {
		name  string
		value []string
	}{
		{"allowed_routes", allowedRoutes},
		{"allowed_passthrough_routes", allowedPassthroughRoutes},
		{"allowed_cache_controls", allowedCacheControls},
		{"guardrails", guardrails},
		{"prompts", prompts},
		{"enforced_params", enforcedParams},
		{"tags", tags},
	} {
		if len(item.value) > 0 {
			keyReq[item.name] = item.value
		}
	}

	if len(metadata) > 0 {
		// Dedicated per-model attributes own these reserved keys.
		metadataPayload := convertMetadataToNative(metadata)
		delete(metadataPayload, "model_rpm_limit")
		delete(metadataPayload, "model_tpm_limit")
		if len(metadataPayload) > 0 {
			keyReq["metadata"] = metadataPayload
		}
	}
	if len(aliases) > 0 {
		keyReq["aliases"] = aliases
	}
	if len(config) > 0 {
		keyReq["config"] = config
	}
	if len(permissions) > 0 {
		keyReq["permissions"] = permissions
	}
	if mapCollectionState(data.ModelMaxBudget) == collectionValueEmpty || mapCollectionState(data.ModelMaxBudget) == collectionValuePopulated {
		keyReq["model_max_budget"] = modelMaxBudget
	}
	if mapCollectionState(data.ModelRPMLimit) == collectionValueEmpty || mapCollectionState(data.ModelRPMLimit) == collectionValuePopulated {
		keyReq["model_rpm_limit"] = modelRPMLimit
	}
	if mapCollectionState(data.ModelTPMLimit) == collectionValueEmpty || mapCollectionState(data.ModelTPMLimit) == collectionValuePopulated {
		keyReq["model_tpm_limit"] = modelTPMLimit
	}

	if !data.RouterSettings.IsNull() && !data.RouterSettings.IsUnknown() {
		routerSettings, err := keyRouterSettingsPayload(data.RouterSettings)
		if err != nil {
			diagnostics.AddAttributeError(path.Root("router_settings"), "Invalid Router Settings", "The router settings could not be converted. No request was sent.")
			return nil, diagnostics
		}
		keyReq["router_settings"] = routerSettings
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

	return keyReq, diagnostics
}

func stringMapMatchesAttrValues(current types.Map, observed map[string]attr.Value) bool {
	if current.IsNull() || current.IsUnknown() || len(current.Elements()) != len(observed) {
		return false
	}
	for key, observedValue := range observed {
		currentValue, exists := current.Elements()[key]
		if !exists || !currentValue.Equal(observedValue) {
			return false
		}
	}
	return true
}

func keyInfoEndpoint(keyIdentifier string) string {
	// Canonical url.Values encoding ensures special characters in a plaintext
	// key (e.g. '#') are not interpreted as a URL fragment. LiteLLM also accepts
	// the SHA256 token hash used to manage write-only keys without recovering
	// plaintext.
	query := url.Values{"key": []string{keyIdentifier}}
	return endpointWithQuery("/key/info", query)
}

func (r *KeyResource) getKeyInfo(ctx context.Context, data *KeyResourceModel) (map[string]interface{}, map[string]interface{}, error) {
	keyIdentifier, err := keyLookupIdentifier(data)
	if err != nil {
		return nil, nil, err
	}
	endpoint := keyInfoEndpoint(keyIdentifier)
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return nil, nil, err
	}
	info := result
	if nested, ok := result["info"].(map[string]interface{}); ok {
		info = nested
	}
	return result, info, nil
}

func (r *KeyResource) getSafeExactKeyInfo(ctx context.Context, data *KeyResourceModel) (map[string]interface{}, map[string]interface{}, error) {
	keyIdentifier, err := keyLookupIdentifier(data)
	if err != nil {
		return nil, nil, err
	}
	endpoint := keyInfoEndpoint(keyIdentifier)
	var result map[string]interface{}
	if err := r.client.DoReadWithResponse(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
		return nil, nil, err
	}
	info, ok := result["info"].(map[string]interface{})
	if !ok || info == nil {
		return nil, nil, errSemanticDictionaryTraversal
	}
	if err := validateExactKeyInfoIdentity(result, info, keyIdentifier); err != nil {
		return nil, nil, err
	}
	if err := validateOrdinaryKeyInfoScalars(info); err != nil {
		return nil, nil, err
	}
	return result, info, nil
}

func validateOrdinaryKeyInfoScalars(info map[string]interface{}) error {
	if value, presence, err := apiValueAt(info, "blocked"); err != nil {
		return errSemanticDictionaryTraversal
	} else if presence == apiValuePresent && value != nil {
		if _, ok := value.(bool); !ok {
			return errSemanticDictionaryTraversal
		}
	}
	for _, field := range []string{"organization_id", "project_id", "budget_id", "key_alias", "duration", "tpm_limit_type", "rpm_limit_type", "budget_duration", "team_id", "user_id"} {
		value, presence, err := apiValueAt(info, field)
		if err != nil {
			return errSemanticDictionaryTraversal
		}
		if presence == apiValuePresent && value != nil {
			if _, ok := value.(string); !ok {
				return errSemanticDictionaryTraversal
			}
		}
	}
	return nil
}

func (r *KeyResource) getFreshExactKeyInfo(ctx context.Context, data *KeyResourceModel) (map[string]interface{}, map[string]interface{}, error) {
	keyIdentifier, err := keyLookupIdentifier(data)
	if err != nil {
		return nil, nil, err
	}
	endpoint := keyInfoEndpoint(keyIdentifier)
	var result map[string]interface{}
	if err := r.client.doFreshRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return nil, nil, err
	}
	info, ok := result["info"].(map[string]interface{})
	if !ok || info == nil {
		return nil, nil, errSemanticDictionaryTraversal
	}
	if err := validateExactKeyInfoIdentity(result, info, keyIdentifier); err != nil {
		return nil, nil, err
	}
	return result, info, nil
}

func isTransientRouterSettingsReadStatus(status int) bool {
	return status == http.StatusNotFound || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func (r *KeyResource) waitForKeyRouterSettings(ctx context.Context, data *KeyResourceModel) error {
	var wanted map[string]interface{}
	if !data.RouterSettings.IsNull() {
		var err error
		wanted, err = keyRouterSettingsPayload(data.RouterSettings)
		if err != nil {
			return fmt.Errorf("build planned router settings")
		}
	}

	const attempts = 6
	stableMatches := 0
	lastDetail := "the read-back document did not match the planned document"
	for attempt := 0; attempt < attempts; attempt++ {
		_, info, err := r.getKeyInfo(ctx, data)
		if err == nil {
			matches, comparisonErr := keyRouterSettingsMatchAPI(wanted, info["router_settings"])
			if comparisonErr != nil {
				// Do not include response values because arbitrary router settings
				// can contain sensitive data.
				return fmt.Errorf("LiteLLM returned malformed or unsupported router settings during read-back")
			}
			if matches {
				stableMatches++
				if stableMatches >= 2 {
					return nil
				}
			} else {
				stableMatches = 0
				lastDetail = "the read-back document did not match the planned document"
			}
		} else {
			stableMatches = 0
			var apiErr *APIError
			if errors.As(err, &apiErr) {
				lastDetail = fmt.Sprintf("the read-back request returned HTTP %d", apiErr.StatusCode)
				if !isTransientRouterSettingsReadStatus(apiErr.StatusCode) {
					return fmt.Errorf("LiteLLM router-settings read-back returned HTTP %d", apiErr.StatusCode)
				}
			} else {
				// Transport errors can embed the request URL and therefore a raw
				// stateful key. Keep the convergence diagnostic deliberately generic.
				lastDetail = "the read-back transport request failed"
			}
		}
		if attempt+1 < attempts {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	return fmt.Errorf("LiteLLM did not return the complete planned router settings after the key mutation: %s", lastDetail)
}

func (r *KeyResource) readKey(ctx context.Context, data *KeyResourceModel) error {
	return r.readKeyWithOwnership(ctx, data, false, keySemanticReadOwnership{})
}

func (r *KeyResource) readKeyWithNumericOwnership(ctx context.Context, data *KeyResourceModel, imported bool) error {
	return r.readKeyWithOwnership(ctx, data, imported, keySemanticReadOwnership{})
}

func (r *KeyResource) readKeyWithOwnership(ctx context.Context, data *KeyResourceModel, imported bool, semantic keySemanticReadOwnership) error {
	return r.readKeyWithTransport(ctx, data, imported, semantic, false)
}

func (r *KeyResource) refreshKeyWithOwnership(ctx context.Context, data *KeyResourceModel, imported bool, semantic keySemanticReadOwnership) error {
	return r.readKeyWithTransport(ctx, data, imported, semantic, true)
}

func (r *KeyResource) readKeyWithTransport(ctx context.Context, data *KeyResourceModel, imported bool, semantic keySemanticReadOwnership, safeRead bool) error {
	keyIdentifier, err := keyLookupIdentifier(data)
	if err != nil {
		return err
	}
	var result, info map[string]interface{}
	if semantic.fresh {
		result, info, err = r.getFreshExactKeyInfo(ctx, data)
	} else if safeRead {
		result, info, err = r.getSafeExactKeyInfo(ctx, data)
	} else {
		result, info, err = r.getKeyInfo(ctx, data)
	}
	if err != nil {
		return err
	}
	if err := validateImportedObjectIdentity(imported, "key", result, "key", keyIdentifier); err != nil {
		return err
	}
	if imported {
		validatedInfo, err := requireImportedObjectField(true, "key", result, "info")
		if err != nil {
			return err
		}
		info = validatedInfo
	}
	if semantic.pending.any() {
		effective, reconcile, err := resolveKeySemanticPendingTransition(ctx, info, semantic)
		if err != nil {
			return fmt.Errorf("pending semantic key update could not be reconciled safely")
		}
		semantic = effective
		if semantic.reconcile != nil {
			*semantic.reconcile = reconcile
		}
	}
	original := data
	next := *data
	data = &next

	// LiteLLM v1.98 stores max_budget on the verification-token row, while
	// soft_budget belongs to the budget relation. Older responses may expose
	// either field at the other location, so each field has its own fallback
	// order. In particular, the unrelated nullable relation max_budget must not
	// override the key row's max_budget. Normal reads refresh configured values
	// when visible, preserve them across API omission, and never adopt defaults
	// for unconfigured fields. Imports adopt the authoritative snapshot and clear
	// values it omits.
	maxBudgetOwned := imported || (!data.MaxBudget.IsNull() && !data.MaxBudget.IsUnknown())
	if err := updateFloat64FromAPIPaths(&data.MaxBudget, info, imported, maxBudgetOwned,
		[]string{"max_budget"},
		[]string{"litellm_budget_table", "max_budget"},
	); err != nil {
		return err
	}
	softBudgetOwned := imported || (!data.SoftBudget.IsNull() && !data.SoftBudget.IsUnknown())
	if err := updateFloat64FromAPIPaths(&data.SoftBudget, info, imported, softBudgetOwned,
		[]string{"litellm_budget_table", "soft_budget"},
		[]string{"soft_budget"},
	); err != nil {
		return err
	}

	// Key rate and parallelism limits remain columns on the v1.98 key row.
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"tpm_limit", &data.TPMLimit},
		{"rpm_limit", &data.RPMLimit},
		{"max_parallel_requests", &data.MaxParallelRequests},
	} {
		owned := imported || (!field.target.IsNull() && !field.target.IsUnknown())
		if err := updateInt64FromAPI(field.target, info, imported, owned, field.name); err != nil {
			return err
		}
	}
	if blocked, ok := info["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
	} else if data.Blocked.IsUnknown() {
		data.Blocked = types.BoolNull()
	}
	if orgID, ok := info["organization_id"].(string); ok && orgID != "" {
		data.OrganizationID = types.StringValue(orgID)
	}
	if projectID, ok := info["project_id"].(string); ok && projectID != "" {
		data.ProjectID = types.StringValue(projectID)
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
		// LiteLLM may return a default budget_duration (e.g. "30d") even when
		// the user did not configure one. Only set it if Terraform already had a
		// configured/known value, otherwise it causes inconsistent result errors.
		if !data.BudgetDuration.IsNull() {
			data.BudgetDuration = types.StringValue(budgetDuration)
		}
	}
	if teamID, ok := info["team_id"].(string); ok && teamID != "" {
		data.TeamID = types.StringValue(teamID)
	}
	if userID, ok := info["user_id"].(string); ok && userID != "" {
		// LiteLLM may return its server-side default user ID even when the key was
		// created/managed without a configured user_id. Since user_id is Optional
		// (not Computed), adopting this API-injected default into state causes
		// "was null, but now cty.StringVal(\"default_user_id\")" after apply.
		if userID != "default_user_id" || !data.UserID.IsNull() {
			data.UserID = types.StringValue(userID)
		}
	}
	// "key" may be at top level or inside "info" (as "token" or "key").
	// Only update data.Key when it is currently unknown or null (i.e. the key
	// was auto-generated and we need to capture it).  When the user supplied a
	// custom key value it is already known and must NOT be overwritten — the
	// /key/info endpoint returns a hashed token, not the raw key, so
	// overwriting would cause "inconsistent values for sensitive attribute".
	if data.KeyWOVersion.IsNull() && (data.Key.IsUnknown() || data.Key.IsNull()) {
		if keyValue, ok := result["key"].(string); ok && keyValue != "" {
			data.Key = types.StringValue(keyValue)
			data.ID = types.StringValue(hashKeyForID(keyValue))
		} else if keyValue, ok := info["token"].(string); ok && keyValue != "" {
			data.Key = types.StringValue(keyValue)
			data.ID = types.StringValue(hashKeyForID(keyValue))
		}
	}

	// Preserve null for unconfigured lists while validating every present API
	// element before publishing any part of the refreshed key state.
	if err := projectKeyStringList(ctx, info, "models", &data.Models); err != nil {
		return err
	}

	if err := projectKeyStringList(ctx, info, "allowed_routes", &data.AllowedRoutes); err != nil {
		return err
	}

	if err := projectKeyStringList(ctx, info, "allowed_passthrough_routes", &data.AllowedPassthroughRoutes); err != nil {
		return err
	}

	if err := projectKeyStringList(ctx, info, "allowed_cache_controls", &data.AllowedCacheControls); err != nil {
		return err
	}

	if err := projectKeyStringList(ctx, info, "guardrails", &data.Guardrails); err != nil {
		return err
	}

	if err := projectKeyStringList(ctx, info, "prompts", &data.Prompts); err != nil {
		return err
	}

	if err := projectKeyStringList(ctx, info, "enforced_params", &data.EnforcedParams); err != nil {
		return err
	}

	// LiteLLM stores tags inside metadata["tags"] in some responses. A present
	// top-level value is authoritative; only absence/null falls back to metadata.
	if err := projectKeyTags(ctx, info, &data.Tags); err != nil {
		return err
	}

	// Remove semantic-JSON-owned top-level members before the legacy map(string)
	// projection. Native semantic values must never be coerced into, or rejected
	// by, the compatibility map before their independent projection runs.
	legacyInfo, err := keyLegacyDictionaryProjectionInfo(ctx, info, data, semantic)
	if err != nil {
		return fmt.Errorf("LiteLLM returned key dictionaries that could not be projected safely; response contents were omitted")
	}

	// Handle metadata map - preserve null when API returns empty and config didn't specify metadata.
	// The API may inject internal keys (e.g. tpm_limit_type, rpm_limit_type) into metadata.
	// Only include keys that were in the user's original config to avoid drift.
	metadata, metadataPresent, metadataOK := keyResponseObject(legacyInfo["metadata"])
	if metadataPresent && !metadataOK {
		return fmt.Errorf("LiteLLM returned malformed key metadata; response contents were omitted")
	}
	if len(metadata) > 0 {
		// Build set of user-configured metadata keys
		configuredKeys := make(map[string]bool)
		currentMeta := make(map[string]string)
		if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
			var diagnostics diag.Diagnostics
			currentMeta, _, diagnostics = strictTerraformStringMap(ctx, data.Metadata, path.Root("metadata"), true)
			if err := collectionProjectionError(ctx, diagnostics); err != nil {
				return err
			}
			for k := range currentMeta {
				configuredKeys[k] = true
			}
		}

		metaMap := make(map[string]attr.Value)
		for k, v := range metadata {
			// Per-model rates have dedicated attributes and must never be
			// flattened into the user metadata map.
			if k == "model_rpm_limit" || k == "model_tpm_limit" {
				continue
			}
			// If user had specific keys, only keep those. Otherwise keep all.
			if len(configuredKeys) > 0 && !configuredKeys[k] {
				continue
			}
			value := metadataValueToString(v)
			if configured, exists := currentMeta[k]; exists {
				value = metadataValueToStringPreservingMasked(v, configured)
			}
			metaMap[k] = types.StringValue(value)
		}
		if len(metaMap) > 0 {
			// Keep the planned/state value when the API representation is
			// semantically identical. Rebuilding the map would discard dynamic
			// sensitivity marks inherited from sensitive input expressions.
			if !stringMapMatchesAttrValues(data.Metadata, metaMap) {
				value, diagnostics := checkedStringMapValue(ctx, metaMap, path.Root("metadata"), true)
				if err := collectionProjectionError(ctx, diagnostics); err != nil {
					return err
				}
				data.Metadata = value
			}
		} else if data.Metadata.IsUnknown() {
			value, diagnostics := checkedStringMapValue(ctx, nil, path.Root("metadata"), true)
			if err := collectionProjectionError(ctx, diagnostics); err != nil {
				return err
			}
			data.Metadata = value
		}
	} else if data.Metadata.IsUnknown() {
		value, diagnostics := checkedStringMapValue(ctx, nil, path.Root("metadata"), true)
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		data.Metadata = value
	}

	if err := projectKeyStringMap(ctx, info, "aliases", &data.Aliases, false); err != nil {
		return err
	}
	if err := projectKeyStringMap(ctx, legacyInfo, "config", &data.Config, false); err != nil {
		return err
	}
	if err := projectKeyStringMap(ctx, legacyInfo, "permissions", &data.Permissions, false); err != nil {
		return err
	}

	if err := projectKeySemanticDictionariesFromInfo(ctx, data, info, semantic); err != nil {
		return err
	}

	modelBudgetOwned := imported || (!data.ModelMaxBudget.IsNull() && !data.ModelMaxBudget.IsUnknown())
	if err := updateFloat64MapFromAPIPaths(&data.ModelMaxBudget, info, imported, modelBudgetOwned,
		[]string{"litellm_budget_table", "model_max_budget"},
		[]string{"model_max_budget"},
	); err != nil {
		return err
	}

	// LiteLLM v1.98 stores key per-model rates under info.metadata. Explicit
	// null is authoritative, while omission is preserved for configured state
	// and normalized to null for unconfigured/imported state as applicable.
	modelRPMOwned := imported || (!data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown())
	if err := updateInt64MapFromAPI(&data.ModelRPMLimit, info, imported, modelRPMOwned, "metadata", "model_rpm_limit"); err != nil {
		return err
	}

	// router_settings is Optional rather than Optional+Computed. An absent block
	// intentionally leaves remote key settings unmanaged; a present block owns
	// the complete LiteLLM router-settings document.
	if !data.RouterSettings.IsNull() {
		routerSettings, _, err := keyRouterSettingsFromAPI(info["router_settings"], data.RouterSettings)
		if err != nil {
			return err
		}
		data.RouterSettings = routerSettings
	}

	modelTPMOwned := imported || (!data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown())
	if err := updateInt64MapFromAPI(&data.ModelTPMLimit, info, imported, modelTPMOwned, "metadata", "model_tpm_limit"); err != nil {
		return err
	}

	*original = *data
	return nil
}

func keyResponseObject(raw interface{}) (map[string]interface{}, bool, bool) {
	if raw == nil {
		return nil, false, true
	}
	switch typed := raw.(type) {
	case map[string]interface{}:
		return typed, true, typed != nil
	case map[string]string:
		result := make(map[string]interface{}, len(typed))
		for key, value := range typed {
			result[key] = value
		}
		return result, true, typed != nil
	default:
		return nil, true, false
	}
}

func projectKeyStringList(ctx context.Context, object map[string]interface{}, field string, target *types.List) error {
	value, presence, diagnostics := strictAPIStringList(ctx, object, field, path.Root(field))
	if err := collectionProjectionError(ctx, diagnostics); err != nil {
		return err
	}
	if presence == apiValuePresent && len(value.Elements()) > 0 {
		*target = value
		return nil
	}
	if !target.IsNull() {
		empty, diagnostics := checkedStringListValue(ctx, nil, path.Root(field))
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		*target = empty
	}
	return nil
}

func projectKeyStringMap(ctx context.Context, object map[string]interface{}, field string, target *types.Map, sensitive bool) error {
	value, presence, diagnostics := strictAPIStringMap(ctx, object, field, path.Root(field), sensitive)
	if err := collectionProjectionError(ctx, diagnostics); err != nil {
		return err
	}
	if presence == apiValuePresent && len(value.Elements()) > 0 {
		*target = value
		return nil
	}
	if !target.IsNull() {
		empty, diagnostics := checkedStringMapValue(ctx, nil, path.Root(field), sensitive)
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		*target = empty
	}
	return nil
}

func projectKeyTags(ctx context.Context, info map[string]interface{}, target *types.List) error {
	value, presence, diagnostics := strictAPIStringList(ctx, info, "tags", path.Root("tags"))
	if err := collectionProjectionError(ctx, diagnostics); err != nil {
		return err
	}
	if presence != apiValuePresent {
		metadata, metadataPresence, err := apiValueAt(info, "metadata")
		if err != nil {
			return err
		}
		if metadataPresence == apiValuePresent {
			object, _, ok := keyResponseObject(metadata)
			if !ok {
				return fmt.Errorf("LiteLLM returned malformed key metadata; response contents were omitted")
			}
			value, presence, diagnostics = strictAPIStringList(ctx, object, "tags", path.Root("tags"))
			if err := collectionProjectionError(ctx, diagnostics); err != nil {
				return err
			}
		}
	}
	if presence == apiValuePresent && len(value.Elements()) > 0 {
		*target = value
		return nil
	}
	if !target.IsNull() {
		empty, diagnostics := checkedStringListValue(ctx, nil, path.Root("tags"))
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		*target = empty
	}
	return nil
}
