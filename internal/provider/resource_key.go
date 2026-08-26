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
	return hash, nil
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
	RouterSettings           types.Object  `tfsdk:"router_settings"`
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
				Description: "Service account ID for team-owned keys.",
				Optional:    true,
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

func (r *KeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data KeyResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var writeOnlyKey types.String
	var sendInviteEmail types.Bool
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

	if err := r.validateKeyInviteRecipient(ctx, &data, sendInviteEmail); err != nil {
		resp.Diagnostics.AddError("Invalid Key Invitation", err.Error())
		return
	}

	keyReq, err := r.buildKeyRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Key Request", err.Error())
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
	if err := r.client.DoRequestWithResponse(ctx, "POST", endpoint, keyReq, &result); err != nil {
		if writeOnlyMode {
			resp.Diagnostics.AddError("Write-Only Key Creation Error", writeOnlyKeyCreateError(err))
		} else {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create key: %s", err))
		}
		return
	}

	if writeOnlyMode {
		data.ID = types.StringValue(hashKeyForID(writeOnlyKey.ValueString()))
		data.Key = types.StringNull()
		data.KeyWO = types.StringNull()
	} else if keyVal, ok := result["key"].(string); ok {
		data.Key = types.StringValue(keyVal)
		data.ID = types.StringValue(hashKeyForID(keyVal))
	}

	if !data.RouterSettings.IsNull() {
		if err := r.waitForKeyRouterSettings(ctx, &data); err != nil {
			resp.Diagnostics.AddError("Router Settings Did Not Converge", "The key was created, but "+err.Error()+". Terraform retained the key identity for recovery.")
		}
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
	importedMarker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(importedMarker) == "true"

	if err := r.readKeyWithNumericOwnership(ctx, &data, imported); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read key: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && imported {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
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
	data.KeyWO = types.StringNull()

	keyIdentifier, err := keyLookupIdentifier(&data)
	if err != nil {
		resp.Diagnostics.AddError("Key Identity Error", fmt.Sprintf("Unable to identify key for update: %s", err))
		return
	}
	updateReq, err := r.buildKeyRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Key Request", err.Error())
		return
	}
	updateReq["key"] = keyIdentifier
	applyKeyRouterSettingsUpdateSemantics(updateReq, data.RouterSettings, state.RouterSettings)

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/key/update", updateReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update key: %s", err))
		return
	}

	if !data.RouterSettings.IsNull() || !state.RouterSettings.IsNull() {
		if err := r.waitForKeyRouterSettings(ctx, &data); err != nil {
			resp.Diagnostics.AddError("Router Settings Did Not Converge", err.Error()+". The remote key may have changed; run Terraform again after reviewing the key.")
		}
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

	keyIdentifier, err := keyLookupIdentifier(&data)
	if err != nil {
		resp.Diagnostics.AddError("Key Identity Error", fmt.Sprintf("Unable to identify key for deletion: %s", err))
		return
	}
	deleteReq := map[string]interface{}{
		"keys": []string{keyIdentifier},
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/key/delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete key: %s", err))
			return
		}
	}
}

func (r *KeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if strings.HasPrefix(req.ID, "sha256:") {
		if _, err := keyHashFromID(req.ID); err != nil {
			resp.Diagnostics.AddError("Invalid Write-Only Key Import", err.Error())
			return
		}
		// A hash import avoids placing the raw token in state. Version "1" is
		// the documented bootstrap value and must match configuration initially.
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
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

func (r *KeyResource) buildKeyRequest(ctx context.Context, data *KeyResourceModel) (map[string]interface{}, error) {
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
			// Dedicated per-model attributes own these reserved keys.
			metadataPayload := convertMetadataToNative(metadata)
			delete(metadataPayload, "model_rpm_limit")
			delete(metadataPayload, "model_tpm_limit")
			if len(metadataPayload) > 0 {
				keyReq["metadata"] = metadataPayload
			}
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
		modelMaxBudget, err := float64RequestMap(data.ModelMaxBudget, "model_max_budget")
		if err != nil {
			return nil, err
		}
		keyReq["model_max_budget"] = modelMaxBudget
	}

	if !data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown() {
		modelRPMLimit, err := int64RequestMap(data.ModelRPMLimit, "model_rpm_limit")
		if err != nil {
			return nil, err
		}
		keyReq["model_rpm_limit"] = modelRPMLimit
	}

	if !data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown() {
		modelTPMLimit, err := int64RequestMap(data.ModelTPMLimit, "model_tpm_limit")
		if err != nil {
			return nil, err
		}
		keyReq["model_tpm_limit"] = modelTPMLimit
	}

	if !data.RouterSettings.IsNull() && !data.RouterSettings.IsUnknown() {
		routerSettings, err := keyRouterSettingsPayload(data.RouterSettings)
		if err != nil {
			return nil, err
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

	return keyReq, nil
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

func (r *KeyResource) getKeyInfo(ctx context.Context, data *KeyResourceModel) (map[string]interface{}, map[string]interface{}, error) {
	keyIdentifier, err := keyLookupIdentifier(data)
	if err != nil {
		return nil, nil, err
	}

	// Canonical url.Values encoding ensures special characters in a plaintext
	// key (e.g. '#') are not interpreted as a URL fragment. LiteLLM also accepts
	// the SHA256 token hash used to manage write-only keys without recovering
	// plaintext.
	query := url.Values{"key": []string{keyIdentifier}}
	endpoint := endpointWithQuery("/key/info", query)
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
	return r.readKeyWithNumericOwnership(ctx, data, false)
}

func (r *KeyResource) readKeyWithNumericOwnership(ctx context.Context, data *KeyResourceModel, imported bool) error {
	keyIdentifier, err := keyLookupIdentifier(data)
	if err != nil {
		return err
	}
	result, info, err := r.getKeyInfo(ctx, data)
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

	// Handle metadata map - preserve null when API returns empty and config didn't specify metadata.
	// The API may inject internal keys (e.g. tpm_limit_type, rpm_limit_type) into metadata.
	// Only include keys that were in the user's original config to avoid drift.
	if metadata, ok := info["metadata"].(map[string]interface{}); ok && len(metadata) > 0 {
		// Build set of user-configured metadata keys
		configuredKeys := make(map[string]bool)
		currentMeta := make(map[string]string)
		if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
			data.Metadata.ElementsAs(ctx, &currentMeta, false)
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
				data.Metadata, _ = types.MapValue(types.StringType, metaMap)
			}
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

	return nil
}
