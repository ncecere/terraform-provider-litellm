package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &JWTKeyMappingResource{}
var _ resource.ResourceWithImportState = &JWTKeyMappingResource{}
var _ resource.ResourceWithConfigValidators = &JWTKeyMappingResource{}
var _ resource.ResourceWithModifyPlan = &JWTKeyMappingResource{}

const (
	jwtKeyMappingDescriptionOwnedPrivateKey   = "jwt_key_mapping_description_owned_v1"
	jwtKeyMappingDescriptionPendingPrivateKey = "jwt_key_mapping_description_pending_v1"
)

func NewJWTKeyMappingResource() resource.Resource { return &JWTKeyMappingResource{} }

type JWTKeyMappingResource struct{ client *Client }

type JWTKeyMappingResourceModel struct {
	ID           types.String `tfsdk:"id"`
	ClaimName    types.String `tfsdk:"jwt_claim_name"`
	ClaimValue   types.String `tfsdk:"jwt_claim_value"`
	KeyWO        types.String `tfsdk:"key_wo"`
	KeyWOVersion types.String `tfsdk:"key_wo_version"`
	Description  types.String `tfsdk:"description"`
	IsActive     types.Bool   `tfsdk:"is_active"`
	CreatedAt    types.String `tfsdk:"created_at"`
	UpdatedAt    types.String `tfsdk:"updated_at"`
	CreatedBy    types.String `tfsdk:"created_by"`
	UpdatedBy    types.String `tfsdk:"updated_by"`
}

func (r *JWTKeyMappingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_jwt_key_mapping"
}

func (r *JWTKeyMappingResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{resourcevalidator.RequiredTogether(path.MatchRoot("key_wo"), path.MatchRoot("key_wo_version"))}
}

func (r *JWTKeyMappingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM JWT claim-to-virtual-key mapping. The raw virtual key is write-only and requires Terraform 1.11 or compatible OpenTofu support for create. LiteLLM v1.98 does not expose evidence that could verify in-place key rotation.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Description: "Authoritative LiteLLM mapping UUID and import identifier.", Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"jwt_claim_name":  schema.StringAttribute{Description: "JWT claim name. Immutable after creation; LiteLLM accepts the empty string.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{jwtKeyMappingImmutableClaimModifier{}}},
			"jwt_claim_value": schema.StringAttribute{Description: "Sensitive JWT claim value to match. Immutable after creation; LiteLLM accepts the empty string.", Optional: true, Computed: true, Sensitive: true, PlanModifiers: []planmodifier.String{jwtKeyMappingImmutableClaimModifier{}}},
			"key_wo":          schema.StringAttribute{Description: "Raw existing LiteLLM virtual key. Sent only on create and never stored in plan or state. Post-create rotation is rejected because LiteLLM v1.98 returns no verifiable token identity.", Optional: true, Sensitive: true, WriteOnly: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"key_wo_version":  schema.StringAttribute{Description: "Persisted create-time version marker for key_wo. Post-create changes are rejected; unchanged historical values remain plannable.", Optional: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"description":     schema.StringAttribute{Description: "Optional mapping description. Once configured on a provider-created or previously managed mapping, assigning null clears it. An imported omitted description remains API-owned until a non-null value is configured.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{jwtKeyMappingOwnedNullableModifier{}}},
			"is_active":       schema.BoolAttribute{Description: "Whether LiteLLM uses the mapping. Omitted imported values remain API-owned; false is sent explicitly.", Optional: true, Computed: true},
			"created_at":      schema.StringAttribute{Description: "Creation timestamp returned by LiteLLM.", Computed: true},
			"updated_at":      schema.StringAttribute{Description: "Last-update timestamp returned by LiteLLM.", Computed: true},
			"created_by":      schema.StringAttribute{Description: "LiteLLM creator provenance when present.", Computed: true, Sensitive: true},
			"updated_by":      schema.StringAttribute{Description: "LiteLLM updater provenance when present.", Computed: true, Sensitive: true},
		},
	}
}

type jwtKeyMappingImmutableClaimModifier struct{}

func (jwtKeyMappingImmutableClaimModifier) Description(context.Context) string {
	return "Preserves an omitted imported claim and requires replacement for an explicitly configured change."
}
func (m jwtKeyMappingImmutableClaimModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}
func (jwtKeyMappingImmutableClaimModifier) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() {
		return
	}
	if req.ConfigValue.IsNull() {
		resp.PlanValue = req.StateValue
		return
	}
	if !req.PlanValue.IsUnknown() && !req.PlanValue.Equal(req.StateValue) {
		resp.RequiresReplace = true
	}
}

type jwtKeyMappingOwnedNullableModifier struct{}

func (jwtKeyMappingOwnedNullableModifier) Description(context.Context) string {
	return "Preserves an API-owned imported description until ownership has been established."
}
func (m jwtKeyMappingOwnedNullableModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}
func (jwtKeyMappingOwnedNullableModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if !req.ConfigValue.IsNull() || req.State.Raw.IsNull() || req.Private == nil {
		return
	}
	owned, diags := req.Private.GetKey(ctx, jwtKeyMappingDescriptionOwnedPrivateKey)
	resp.Diagnostics.Append(diags...)
	if string(owned) == "true" {
		resp.PlanValue = types.StringNull()
	}
}

func (r *JWTKeyMappingResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state, config JWTKeyMappingResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Omitted API-owned leaves must remain exactly state-backed even when another
	// leaf causes an update plan. In particular, immutable claim omissions must
	// never become a spurious replacement during description ownership changes.
	if config.ClaimName.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("jwt_claim_name"), state.ClaimName)...)
	}
	if config.ClaimValue.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("jwt_claim_value"), state.ClaimValue)...)
	}
	if config.IsActive.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("is_active"), state.IsActive)...)
	}

	// key_wo is intentionally unavailable in state. key_wo_version is therefore
	// the only safe transition signal. Preserve an omitted historical marker,
	// but reject every addition/change before Update can send a mutation.
	switch {
	case config.KeyWOVersion.IsNull() && !state.KeyWOVersion.IsNull():
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("key_wo_version"), state.KeyWOVersion)...)
	case config.KeyWOVersion.IsUnknown() || !config.KeyWOVersion.Equal(state.KeyWOVersion):
		resp.Diagnostics.AddAttributeError(path.Root("key_wo_version"), "Unsupported JWT Key Rotation", "LiteLLM v1.98 returns no token, hash, or fingerprint that can verify an in-place key change. No mutation was sent. Create a replacement mapping or manage the rotation outside this resource and import the resulting canonical UUID.")
		return
	}

	owned := false
	if req.Private != nil {
		marker, diagnostics := req.Private.GetKey(ctx, jwtKeyMappingDescriptionOwnedPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		owned = string(marker) == "true"
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if !owned && config.Description.IsNull() {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("description"), state.Description)...)
	}
	mutableUpdate := (!config.IsActive.IsNull() && !config.IsActive.IsUnknown() && !config.IsActive.Equal(state.IsActive)) ||
		(owned && config.Description.IsNull() && !state.Description.IsNull()) ||
		(!config.Description.IsNull() && !config.Description.IsUnknown() && !config.Description.Equal(state.Description))
	if mutableUpdate {
		// LiteLLM advances these observable computed leaves on update. They must
		// not remain pinned to the prior state in Terraform's planned value.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("updated_at"), types.StringUnknown())...)
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("updated_by"), types.StringUnknown())...)
	}
	if resp.Private == nil {
		return
	}
	if owned || config.Description.IsNull() {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, jwtKeyMappingDescriptionPendingPrivateKey, nil)...)
		return
	}
	if config.Description.IsUnknown() {
		return
	}
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, jwtKeyMappingDescriptionPendingPrivateKey, []byte("true"))...)
	if config.Description.Equal(state.Description) {
		// A state-only equality would otherwise skip Apply and lose the fact that
		// description was explicitly present in configuration.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	}
}

func (r *JWTKeyMappingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *JWTKeyMappingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data JWTKeyMappingResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	var key types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("key_wo"), &key)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if data.ClaimName.IsNull() || data.ClaimName.IsUnknown() || data.ClaimValue.IsNull() || data.ClaimValue.IsUnknown() {
		resp.Diagnostics.AddError("Invalid JWT Key Mapping", "jwt_claim_name and jwt_claim_value must be known when creating a mapping; each may be the empty string.")
		return
	}
	if key.IsNull() || key.IsUnknown() || key.ValueString() == "" || data.KeyWOVersion.IsNull() || data.KeyWOVersion.IsUnknown() || data.KeyWOVersion.ValueString() == "" {
		resp.Diagnostics.AddError("Invalid JWT Key Mapping Key", "key_wo and key_wo_version must be known and non-empty when creating a mapping.")
		return
	}
	body := map[string]interface{}{"jwt_claim_name": data.ClaimName.ValueString(), "jwt_claim_value": data.ClaimValue.ValueString(), "key": key.ValueString()}
	if !data.Description.IsNull() && !data.Description.IsUnknown() {
		body["description"] = data.Description.ValueString()
	}
	var raw json.RawMessage
	accepted, err := r.client.doRequestWithResponse(ctx, http.MethodPost, jwtKeyMappingCreatePath, body, &raw)
	if err != nil {
		if accepted {
			if id := confirmedJWTKeyMappingID(raw); id != "" {
				setJWTKeyMappingIdentityOnly(&data, id)
				resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			}
		}
		resp.Diagnostics.AddError("JWT Key Mapping Create Outcome Unconfirmed", jwtKeyMappingCreateRecoveryDiagnostic(err))
		return
	}
	created, err := decodeJWTKeyMappingObject(raw)
	if err != nil {
		if id := confirmedJWTKeyMappingID(raw); id != "" {
			setJWTKeyMappingIdentityOnly(&data, id)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		}
		resp.Diagnostics.AddError("Invalid API Response", "LiteLLM accepted the mapping create but did not return a valid recoverable mapping object. Sensitive response details were omitted.")
		return
	}
	if !jwtKeyMappingCreateMatchesRequest(created, data) {
		setJWTKeyMappingIdentityOnly(&data, created.ID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("Invalid API Response", "LiteLLM created a mapping whose observable fields did not match the request. Only the confirmed UUID was retained; sensitive values were omitted.")
		return
	}

	if !data.IsActive.IsNull() && !data.IsActive.IsUnknown() && !data.IsActive.ValueBool() {
		var updateRaw json.RawMessage
		_, updateErr := r.client.doRequestWithResponse(ctx, http.MethodPost, jwtKeyMappingUpdatePath, map[string]interface{}{"id": created.ID, "is_active": false}, &updateRaw)
		if updateErr != nil {
			setJWTKeyMappingIdentityOnly(&data, created.ID)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			resp.Diagnostics.AddError("JWT Key Mapping Deactivation Not Confirmed", "LiteLLM created the mapping and returned its UUID, but the single deactivation update did not return a valid success response. Only the confirmed UUID was retained; response details were omitted.")
			return
		}
		updated, decodeErr := decodeJWTKeyMappingObject(updateRaw)
		if decodeErr != nil || updated.ID != created.ID || !jwtKeyMappingFinalMatchesPlan(updated, data) {
			setJWTKeyMappingIdentityOnly(&data, created.ID)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			resp.Diagnostics.AddError("JWT Key Mapping Deactivation Not Confirmed", "LiteLLM created the mapping, but the deactivation response did not confirm the same UUID, claim identity, and inactive state. Only the confirmed UUID was retained; response details were omitted.")
			return
		}
	}

	observed, readErr := readFreshJWTKeyMapping(ctx, r.client, created.ID)
	if readErr != nil {
		setJWTKeyMappingIdentityOnly(&data, created.ID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("JWT Key Mapping Create Not Confirmed", "LiteLLM returned a committed mapping UUID, but fresh authoritative read-back failed. Only the confirmed UUID was retained for recovery; response details were omitted.")
		return
	}
	if !jwtKeyMappingFinalMatchesPlan(observed, data) {
		setJWTKeyMappingIdentityOnly(&data, created.ID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("JWT Key Mapping Create Not Confirmed", "Fresh authoritative read-back did not confirm the requested observable fields. Only the confirmed UUID was retained for recovery; sensitive values were omitted.")
		return
	}
	setJWTKeyMappingResourceState(&data, observed)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !data.Description.IsNull() && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, jwtKeyMappingDescriptionOwnedPrivateKey, []byte("true"))...)
	}
}

func jwtKeyMappingCreateMatchesRequest(mapping jwtKeyMappingObject, data JWTKeyMappingResourceModel) bool {
	if mapping.ClaimName != data.ClaimName.ValueString() || mapping.ClaimValue != data.ClaimValue.ValueString() || !mapping.IsActive {
		return false
	}
	return jwtKeyMappingDescriptionMatches(mapping, data)
}

func jwtKeyMappingFinalMatchesPlan(mapping jwtKeyMappingObject, data JWTKeyMappingResourceModel) bool {
	if mapping.ClaimName != data.ClaimName.ValueString() || mapping.ClaimValue != data.ClaimValue.ValueString() {
		return false
	}
	if !data.IsActive.IsNull() && !data.IsActive.IsUnknown() && mapping.IsActive != data.IsActive.ValueBool() {
		return false
	}
	return jwtKeyMappingDescriptionMatches(mapping, data)
}

func jwtKeyMappingDescriptionMatches(mapping jwtKeyMappingObject, data JWTKeyMappingResourceModel) bool {
	if data.Description.IsNull() || data.Description.IsUnknown() {
		return mapping.Description == nil
	}
	return mapping.Description != nil && *mapping.Description == data.Description.ValueString()
}

func setJWTKeyMappingIdentityOnly(data *JWTKeyMappingResourceModel, id string) {
	*data = JWTKeyMappingResourceModel{
		ID:           types.StringValue(id),
		ClaimName:    types.StringNull(),
		ClaimValue:   types.StringNull(),
		KeyWO:        types.StringNull(),
		KeyWOVersion: types.StringNull(),
		Description:  types.StringNull(),
		IsActive:     types.BoolNull(),
		CreatedAt:    types.StringNull(),
		UpdatedAt:    types.StringNull(),
		CreatedBy:    types.StringNull(),
		UpdatedBy:    types.StringNull(),
	}
}

func confirmedJWTKeyMappingID(raw json.RawMessage) string {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return ""
	}
	value, err := requiredJSONString(object, "id")
	if err != nil {
		return ""
	}
	value, err = canonicalJWTKeyMappingID(value)
	if err != nil {
		return ""
	}
	return value
}

func (r *JWTKeyMappingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data JWTKeyMappingResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := canonicalJWTKeyMappingID(data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid JWT Key Mapping State", "Stored mapping identity is not a canonical UUID.")
		return
	}
	mapping, err := readJWTKeyMapping(ctx, r.client, id)
	if err != nil {
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("JWT Key Mapping Read Failed", "Unable to read the JWT key mapping. Response details were omitted because they may contain sensitive claim data.")
		return
	}
	setJWTKeyMappingResourceState(&data, mapping)
	data.KeyWO = types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *JWTKeyMappingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state JWTKeyMappingResourceModel
	var configDescription types.String
	var configIsActive types.Bool
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("description"), &configDescription)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("is_active"), &configIsActive)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.KeyWOVersion.Equal(state.KeyWOVersion) {
		resp.Diagnostics.AddError("Unsupported JWT Key Rotation", "LiteLLM v1.98 returns no token, hash, or fingerprint that can verify an in-place key change. No mutation was sent.")
		return
	}
	pendingDescriptionOwnership := false
	if req.Private != nil {
		marker, diagnostics := req.Private.GetKey(ctx, jwtKeyMappingDescriptionPendingPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		pendingDescriptionOwnership = string(marker) == "true"
	}
	if resp.Diagnostics.HasError() {
		return
	}
	body := map[string]interface{}{"id": state.ID.ValueString()}
	changed := false
	if !plan.Description.Equal(state.Description) {
		if plan.Description.IsUnknown() {
			resp.Diagnostics.AddError("Invalid JWT Key Mapping Description", "description must be known during apply; no update was sent.")
			return
		}
		changed = true
		if plan.Description.IsNull() {
			body["description"] = nil
		} else {
			body["description"] = plan.Description.ValueString()
		}
	}
	if !plan.IsActive.Equal(state.IsActive) && !plan.IsActive.IsUnknown() && !plan.IsActive.IsNull() {
		body["is_active"] = plan.IsActive.ValueBool()
		changed = true
	}
	if !changed {
		if !pendingDescriptionOwnership {
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
		observed, err := readFreshJWTKeyMapping(ctx, r.client, state.ID.ValueString())
		if err != nil || observed.ClaimName != state.ClaimName.ValueString() || observed.ClaimValue != state.ClaimValue.ValueString() || configDescription.IsNull() || configDescription.IsUnknown() || observed.Description == nil || *observed.Description != configDescription.ValueString() || (!configIsActive.IsNull() && !configIsActive.IsUnknown() && observed.IsActive != configIsActive.ValueBool()) {
			resp.Diagnostics.AddError("JWT Key Mapping Description Ownership Not Confirmed", "Fresh authoritative read-back did not confirm the explicitly configured description. Prior state and API ownership were retained; no mutation was sent.")
			return
		}
		setJWTKeyMappingResourceState(&plan, observed)
		plan.ID, plan.KeyWO, plan.KeyWOVersion = state.ID, types.StringNull(), state.KeyWOVersion
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		if !resp.Diagnostics.HasError() && resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, jwtKeyMappingDescriptionOwnedPrivateKey, []byte("true"))...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, jwtKeyMappingDescriptionPendingPrivateKey, nil)...)
		}
		return
	}
	var raw json.RawMessage
	_, err := r.client.doRequestWithResponse(ctx, http.MethodPost, jwtKeyMappingUpdatePath, body, &raw)
	if err != nil {
		resp.Diagnostics.AddError("JWT Key Mapping Update Failed", mappingMutationDiagnostic("update", err))
		return
	}
	updated, err := decodeJWTKeyMappingObject(raw)
	if err != nil || updated.ID != state.ID.ValueString() || updated.ClaimName != state.ClaimName.ValueString() || updated.ClaimValue != state.ClaimValue.ValueString() || !jwtKeyMappingUpdateMatchesPlan(updated, plan, body) {
		resp.Diagnostics.AddError("Invalid API Response", "LiteLLM accepted the update but did not return the same mapping identity and requested observable state. Prior state and private ownership were retained.")
		return
	}
	observed, err := readFreshJWTKeyMapping(ctx, r.client, state.ID.ValueString())
	if err != nil || observed.ClaimName != state.ClaimName.ValueString() || observed.ClaimValue != state.ClaimValue.ValueString() {
		resp.Diagnostics.AddError("JWT Key Mapping Update Not Confirmed", "LiteLLM accepted the update, but authoritative read-back did not confirm the same mapping identity. Prior state and private ownership were retained.")
		return
	}
	if _, ok := body["description"]; ok {
		if (plan.Description.IsNull() && observed.Description != nil) || (!plan.Description.IsNull() && (observed.Description == nil || *observed.Description != plan.Description.ValueString())) {
			resp.Diagnostics.AddError("JWT Key Mapping Update Not Confirmed", "Authoritative read-back did not confirm the requested description state. Prior state was retained.")
			return
		}
	}
	if _, ok := body["is_active"]; ok && observed.IsActive != plan.IsActive.ValueBool() {
		resp.Diagnostics.AddError("JWT Key Mapping Update Not Confirmed", "Authoritative read-back did not confirm the requested active state. Prior state was retained.")
		return
	}
	if !configDescription.IsNull() && !configDescription.IsUnknown() && (observed.Description == nil || *observed.Description != configDescription.ValueString()) {
		resp.Diagnostics.AddError("JWT Key Mapping Update Not Confirmed", "Authoritative read-back did not confirm the explicitly configured description. Prior state and private ownership were retained.")
		return
	}
	if !configIsActive.IsNull() && !configIsActive.IsUnknown() && observed.IsActive != configIsActive.ValueBool() {
		resp.Diagnostics.AddError("JWT Key Mapping Update Not Confirmed", "Authoritative read-back did not confirm the explicitly configured active state. Prior state and private ownership were retained.")
		return
	}
	setJWTKeyMappingResourceState(&plan, observed)
	plan.ID, plan.KeyWO, plan.KeyWOVersion = state.ID, types.StringNull(), state.KeyWOVersion
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		if _, ok := body["description"]; ok || pendingDescriptionOwnership {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, jwtKeyMappingDescriptionOwnedPrivateKey, []byte("true"))...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, jwtKeyMappingDescriptionPendingPrivateKey, nil)...)
		}
	}
}

func jwtKeyMappingUpdateMatchesPlan(mapping jwtKeyMappingObject, plan JWTKeyMappingResourceModel, body map[string]interface{}) bool {
	if _, ok := body["description"]; ok {
		if plan.Description.IsNull() {
			if mapping.Description != nil {
				return false
			}
		} else if mapping.Description == nil || *mapping.Description != plan.Description.ValueString() {
			return false
		}
	}
	if _, ok := body["is_active"]; ok && mapping.IsActive != plan.IsActive.ValueBool() {
		return false
	}
	return true
}

func (r *JWTKeyMappingResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data JWTKeyMappingResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, err := canonicalJWTKeyMappingID(data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid JWT Key Mapping State", "Stored mapping identity is not a canonical UUID.")
		return
	}
	var raw json.RawMessage
	accepted, deleteErr := r.client.doRequestWithResponse(ctx, http.MethodPost, jwtKeyMappingDeletePath, map[string]interface{}{"id": id}, &raw)
	deleteReturned404 := IsAPIErrorStatus(deleteErr, http.StatusNotFound)
	if deleteErr == nil {
		if err := validateJWTKeyMappingDeleteResponse(raw); err != nil {
			deleteErr = err
		}
	}
	if deleteErr != nil && !accepted && !deleteReturned404 {
		resp.Diagnostics.AddError("JWT Key Mapping Delete Failed", mappingMutationDiagnostic("delete", deleteErr))
		return
	}
	_, readErr := readFreshJWTKeyMapping(ctx, r.client, id)
	if IsAPIErrorStatus(readErr, http.StatusNotFound) {
		return
	}
	if readErr == nil {
		resp.Diagnostics.AddError("JWT Key Mapping Delete Not Confirmed", "LiteLLM still returned the mapping after delete; Terraform retained it in state.")
		return
	}
	resp.Diagnostics.AddError("JWT Key Mapping Delete Not Confirmed", "Delete did not produce exact HTTP 404 absence proof. Terraform retained the mapping in state; response details were omitted.")
}

func validateJWTKeyMappingDeleteResponse(raw json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || len(object) != 1 {
		return fmt.Errorf("invalid delete response")
	}
	value, ok := object["status"]
	if !ok || !bytes.Equal(bytes.TrimSpace(value), []byte(`"success"`)) {
		return fmt.Errorf("invalid delete response")
	}
	return nil
}

func (r *JWTKeyMappingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := canonicalJWTKeyMappingID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid JWT Key Mapping Import ID", "Import ID must be the canonical lowercase UUID returned by LiteLLM.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}

func setJWTKeyMappingResourceState(data *JWTKeyMappingResourceModel, mapping jwtKeyMappingObject) {
	data.ID = types.StringValue(mapping.ID)
	data.ClaimName = types.StringValue(mapping.ClaimName)
	data.ClaimValue = types.StringValue(mapping.ClaimValue)
	if mapping.Description == nil {
		data.Description = types.StringNull()
	} else {
		data.Description = types.StringValue(*mapping.Description)
	}
	data.IsActive = types.BoolValue(mapping.IsActive)
	data.CreatedAt, data.UpdatedAt = types.StringValue(mapping.CreatedAt), types.StringValue(mapping.UpdatedAt)
	if mapping.CreatedBy == nil {
		data.CreatedBy = types.StringNull()
	} else {
		data.CreatedBy = types.StringValue(*mapping.CreatedBy)
	}
	if mapping.UpdatedBy == nil {
		data.UpdatedBy = types.StringNull()
	} else {
		data.UpdatedBy = types.StringValue(*mapping.UpdatedBy)
	}
}
