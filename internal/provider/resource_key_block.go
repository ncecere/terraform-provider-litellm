package provider

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

var keyHashIDPattern = regexp.MustCompile(`^sha256:[0-9a-fA-F]{64}$`)

var _ resource.Resource = &KeyBlockResource{}
var _ resource.ResourceWithImportState = &KeyBlockResource{}
var _ resource.ResourceWithUpgradeState = &KeyBlockResource{}
var _ resource.ResourceWithConfigValidators = &KeyBlockResource{}
var _ resource.ResourceWithModifyPlan = &KeyBlockResource{}

func (r *KeyBlockResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(
			path.MatchRoot("key"),
			path.MatchRoot("key_hash"),
		),
	}
}

func (r *KeyBlockResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	var state, plan KeyBlockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	stateIdentity, err := keyBlockStateIdentity(&state)
	if err != nil {
		resp.Diagnostics.AddError("Key Identity Error", "The key block state does not contain a valid SHA256 management identifier.")
		return
	}

	var plannedIdentity keyBlockIdentity
	var replacementPath path.Path
	switch {
	case !plan.KeyHash.IsNull() && !plan.KeyHash.IsUnknown():
		plannedIdentity, err = keyBlockIdentityFromHashID(plan.KeyHash.ValueString())
		replacementPath = path.Root("key_hash")
	case !plan.Key.IsNull() && !plan.Key.IsUnknown():
		plannedIdentity, err = keyBlockIdentityFromLegacyKey(plan.Key.ValueString())
		replacementPath = path.Root("key")
	default:
		// Replacing an existing block before an unknown identity is resolved can
		// unblock the same key under create_before_destroy. Refuse the ambiguous
		// plan; users can apply the identity-producing dependency first.
		if !plan.KeyHash.IsNull() || !plan.Key.IsNull() {
			resp.Diagnostics.AddError(
				"Key Identity Unknown",
				"The replacement key identity is not known during planning. Apply the dependency that produces the key or hash first, then plan again.",
			)
		}
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Invalid Key Identity", "Exactly one known, non-empty key identity must be configured.")
		return
	}
	if plannedIdentity.managementID != stateIdentity.managementID {
		resp.RequiresReplace = append(resp.RequiresReplace, replacementPath)
		// UseStateForUnknown preserves the old ID before resource-level planning.
		// A real target replacement must restore unknown so Create can return the
		// new hash without producing an inconsistent result after apply.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	}
}

func NewKeyBlockResource() resource.Resource {
	return &KeyBlockResource{}
}

// KeyBlockResource implements a stateful block for a LiteLLM key.
// Creating this resource blocks the key, destroying it unblocks the key.
type KeyBlockResource struct {
	client *Client
}

type KeyBlockResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Key     types.String `tfsdk:"key"`
	KeyHash types.String `tfsdk:"key_hash"`
	Blocked types.Bool   `tfsdk:"blocked"`
}

type keyBlockIdentity struct {
	managementID string
	apiValue     string
}

func keyBlockIdentityFromRaw(raw string) (keyBlockIdentity, error) {
	if raw == "" {
		return keyBlockIdentity{}, errors.New("key input is empty")
	}
	managementID := hashKeyForID(raw)
	return keyBlockIdentity{
		managementID: managementID,
		apiValue:     strings.TrimPrefix(managementID, "sha256:"),
	}, nil
}

// keyBlockIdentityFromLegacyKey preserves the v0 key contract: values in the
// 64-hex verification-token format were already hashes, while sk-* values were
// raw tokens. This avoids double-hashing previously working configurations.
func keyBlockIdentityFromLegacyKey(value string) (keyBlockIdentity, error) {
	if identity, err := keyBlockIdentityFromBareHash(value); err == nil {
		return identity, nil
	}
	return keyBlockIdentityFromRaw(value)
}

func keyBlockIdentityFromHashID(id string) (keyBlockIdentity, error) {
	bareHash, err := keyHashFromID(id)
	if err != nil {
		return keyBlockIdentity{}, errors.New("invalid SHA256 management identifier")
	}
	bareHash = strings.ToLower(bareHash)
	return keyBlockIdentity{
		managementID: "sha256:" + bareHash,
		apiValue:     bareHash,
	}, nil
}

func keyBlockIdentityFromBareHash(hash string) (keyBlockIdentity, error) {
	if len(hash) != 64 {
		return keyBlockIdentity{}, errors.New("invalid SHA256 management identifier")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return keyBlockIdentity{}, errors.New("invalid SHA256 management identifier")
	}
	return keyBlockIdentityFromHashID("sha256:" + hash)
}

func keyBlockStateIdentity(data *KeyBlockResourceModel) (keyBlockIdentity, error) {
	if !data.ID.IsNull() && !data.ID.IsUnknown() && data.ID.ValueString() != "" {
		if identity, err := keyBlockIdentityFromHashID(data.ID.ValueString()); err == nil {
			return identity, nil
		}
	}

	// Defensive compatibility fallback for raw v0 state. Normal Framework use
	// upgrades that state before lifecycle methods run.
	if !data.Key.IsNull() && !data.Key.IsUnknown() && data.Key.ValueString() != "" {
		return keyBlockIdentityFromLegacyKey(data.Key.ValueString())
	}
	return keyBlockIdentity{}, errors.New("key block state has no valid identity")
}

func keyBlockOperationError(operation string, err error) string {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("LiteLLM returned HTTP %d while %s the key. The response body was omitted because it may contain key material.", apiErr.StatusCode, operation)
	}
	return fmt.Sprintf("Unable to finish %s the key. Error details were omitted because an intermediary may include key material.", operation)
}

func keyBlockInfoEndpoint(apiValue string) string {
	query := url.Values{}
	query.Set("key", apiValue)
	return endpointWithQuery("/key/info", query)
}

func (r *KeyBlockResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_key_block"
}

func (r *KeyBlockResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the blocked state of a LiteLLM API key. Creating this resource blocks the key; destroying it unblocks the key.",
		Version:     1,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Non-sensitive SHA256 management identifier for this block. The raw key is never used as the resource ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"key": schema.StringAttribute{
				Description: "Legacy stateful API key input. Existing configurations remain supported; prefer key_hash for new resources.",
				Optional:    true,
				Sensitive:   true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"key_hash": schema.StringAttribute{
				Description: "A sha256:<64-hex> key management identifier, such as litellm_key.example.id. LiteLLM receives only the bare hash.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(keyHashIDPattern, "must use the sha256:<64-hex> management identifier format"),
				},
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the key is currently blocked. Always true when this resource exists.",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *KeyBlockResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *KeyBlockResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data KeyBlockResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var identity keyBlockIdentity
	var err error
	switch {
	case !data.KeyHash.IsNull() && !data.KeyHash.IsUnknown():
		identity, err = keyBlockIdentityFromHashID(data.KeyHash.ValueString())
	case !data.Key.IsNull() && !data.Key.IsUnknown():
		identity, err = keyBlockIdentityFromLegacyKey(data.Key.ValueString())
	default:
		err = errors.New("key identity is unknown or missing")
	}
	if err != nil {
		resp.Diagnostics.AddError("Invalid Key Identity", "Exactly one known, non-empty key identity must be configured.")
		return
	}

	blockReq := map[string]interface{}{"key": identity.apiValue}
	if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/key/block", blockReq, nil); err != nil {
		resp.Diagnostics.AddError("Key Block Error", keyBlockOperationError("blocking", err))
		return
	}

	data.ID = types.StringValue(identity.managementID)
	data.Blocked = types.BoolValue(true)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *KeyBlockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data KeyBlockResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	identity, err := keyBlockStateIdentity(&data)
	if err != nil {
		resp.Diagnostics.AddError("Key Identity Error", "The key block state does not contain a valid SHA256 management identifier.")
		return
	}
	endpoint := keyBlockInfoEndpoint(identity.apiValue)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			// Key no longer exists, remove the block resource.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Key Read Error", keyBlockOperationError("reading", err))
		return
	}

	// The /key/info endpoint may return key data nested inside "info"
	info := result
	if nested, ok := result["info"].(map[string]interface{}); ok {
		info = nested
	}

	// Check blocked status
	if blocked, ok := info["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
		if !blocked {
			// Key is no longer blocked, remove this resource
			resp.State.RemoveResource(ctx)
			return
		}
	} else {
		// If blocked field doesn't exist or is not a bool, assume not blocked
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *KeyBlockResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Key attribute has RequiresReplace, so this should never be called for key changes.
	// Just preserve state.
	var data KeyBlockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state KeyBlockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = state.ID
	data.Blocked = types.BoolValue(true)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *KeyBlockResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data KeyBlockResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	identity, err := keyBlockStateIdentity(&data)
	if err != nil {
		resp.Diagnostics.AddError("Key Identity Error", "The key block state does not contain a valid SHA256 management identifier.")
		return
	}
	unblockReq := map[string]interface{}{"key": identity.apiValue}

	if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/key/unblock", unblockReq, nil); err != nil {
		// Don't fail if the key doesn't exist.
		if !IsAPIErrorStatus(err, http.StatusNotFound) {
			resp.Diagnostics.AddError("Key Unblock Error", keyBlockOperationError("unblocking", err))
			return
		}
	}
}

func (r *KeyBlockResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if strings.HasPrefix(req.ID, "sha256:") {
		identity, err := keyBlockIdentityFromHashID(req.ID)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Key Hash Import", "The import ID must use sha256:<64-hex> format.")
			return
		}
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), identity.managementID)...)
		// Preserve configured hexadecimal casing to avoid a replacement after
		// import while keeping the resource ID canonical and lowercase.
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key_hash"), req.ID)...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("blocked"), true)...)
		return
	}

	if identity, err := keyBlockIdentityFromBareHash(req.ID); err == nil {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), identity.managementID)...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key_hash"), "sha256:"+req.ID)...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("blocked"), true)...)
		return
	}

	// Legacy raw-key imports remain supported for backward compatibility.
	identity, err := keyBlockIdentityFromRaw(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Key Block Import", "The import identity must be a non-empty raw key or SHA256 management identifier.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), identity.managementID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("blocked"), true)...)
}

// UpgradeState migrates legacy v0 state, whose non-sensitive ID contained the
// raw API key, to a SHA256 management identifier while preserving the existing
// sensitive key attribute for configuration compatibility.
func (r *KeyBlockResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: nil,
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				if req.RawState == nil {
					resp.Diagnostics.AddError("Unable to Upgrade Key Block State", "The prior raw state is unavailable.")
					return
				}

				var priorState map[string]json.RawMessage
				if err := json.Unmarshal(req.RawState.JSON, &priorState); err != nil {
					resp.Diagnostics.AddError("Unable to Upgrade Key Block State", "The prior state could not be decoded.")
					return
				}

				var rawID string
				if raw, ok := priorState["id"]; ok {
					if err := json.Unmarshal(raw, &rawID); err != nil {
						resp.Diagnostics.AddError("Unable to Upgrade Key Block State", "The prior state does not contain a valid key identity.")
						return
					}
				}
				if rawID == "" {
					resp.Diagnostics.AddError("Unable to Upgrade Key Block State", "The prior state does not contain a valid key identity.")
					return
				}

				identity, err := keyBlockIdentityFromLegacyKey(rawID)
				if err != nil {
					resp.Diagnostics.AddError("Unable to Upgrade Key Block State", "The prior state does not contain a valid key identity.")
					return
				}
				tflog.Info(ctx, "Upgrading litellm_key_block state from v0 to v1 by normalizing the resource ID")
				encodedID, err := json.Marshal(identity.managementID)
				if err != nil {
					resp.Diagnostics.AddError("Unable to Upgrade Key Block State", "The normalized key identity could not be encoded.")
					return
				}
				priorState["id"] = encodedID
				if _, ok := priorState["key_hash"]; !ok {
					priorState["key_hash"] = json.RawMessage("null")
				}

				upgradedJSON, err := json.Marshal(priorState)
				if err != nil {
					resp.Diagnostics.AddError("Unable to Upgrade Key Block State", "The upgraded state could not be encoded.")
					return
				}
				resp.DynamicValue = &tfprotov6.DynamicValue{JSON: upgradedJSON}
			},
		},
	}
}
