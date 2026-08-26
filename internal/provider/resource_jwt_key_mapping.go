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

const jwtKeyMappingDescriptionOwnedPrivateKey = "jwt_key_mapping_description_owned_v1"

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
		Description: "Manages a LiteLLM JWT claim-to-virtual-key mapping. The raw virtual key is write-only and requires Terraform 1.11 or compatible OpenTofu support for create and rotation.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{Description: "Authoritative LiteLLM mapping UUID and import identifier.", Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"jwt_claim_name":  schema.StringAttribute{Description: "JWT claim name. Immutable after creation.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"jwt_claim_value": schema.StringAttribute{Description: "Sensitive JWT claim value to match. Immutable after creation.", Optional: true, Computed: true, Sensitive: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"key_wo":          schema.StringAttribute{Description: "Raw existing LiteLLM virtual key. Sent only on create or when key_wo_version changes; never stored in plan or state. LiteLLM never returns the token or its hash.", Optional: true, Sensitive: true, WriteOnly: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"key_wo_version":  schema.StringAttribute{Description: "Persisted nonce for key_wo. Change it to rotate this mapping to the new write-only key without replacing the mapping UUID.", Optional: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"description":     schema.StringAttribute{Description: "Optional mapping description. Once configured on a provider-created or previously managed mapping, assigning null clears it. An imported omitted description remains API-owned until a non-null value is configured.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{jwtKeyMappingOwnedNullableModifier{}}},
			"is_active":       schema.BoolAttribute{Description: "Whether LiteLLM uses the mapping. Omitted imported values remain API-owned; false is sent explicitly.", Optional: true, Computed: true},
			"created_at":      schema.StringAttribute{Description: "Creation timestamp returned by LiteLLM.", Computed: true},
			"updated_at":      schema.StringAttribute{Description: "Last-update timestamp returned by LiteLLM.", Computed: true},
			"created_by":      schema.StringAttribute{Description: "LiteLLM creator provenance when present.", Computed: true, Sensitive: true},
			"updated_by":      schema.StringAttribute{Description: "LiteLLM updater provenance when present.", Computed: true, Sensitive: true},
		},
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
	if data.ClaimName.IsNull() || data.ClaimName.IsUnknown() || data.ClaimName.ValueString() == "" || data.ClaimValue.IsNull() || data.ClaimValue.IsUnknown() || data.ClaimValue.ValueString() == "" {
		resp.Diagnostics.AddError("Invalid JWT Key Mapping", "jwt_claim_name and jwt_claim_value must be known and non-empty when creating a mapping.")
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
		resp.Diagnostics.AddError("JWT Key Mapping Creation Failed", mappingMutationDiagnostic("create", err))
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
	if !jwtKeyMappingCreateMatchesPlan(created, data) {
		setJWTKeyMappingIdentityOnly(&data, created.ID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("Invalid API Response", "LiteLLM created a mapping whose observable fields did not match the request. Only the confirmed UUID was retained; sensitive values were omitted.")
		return
	}
	observed, readErr := readJWTKeyMapping(ctx, r.client, created.ID)
	if readErr != nil {
		setJWTKeyMappingIdentityOnly(&data, created.ID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("JWT Key Mapping Create Not Confirmed", "LiteLLM returned a committed mapping UUID, but authoritative read-back failed. Only the confirmed UUID was retained for recovery; response details were omitted.")
		return
	}
	if !jwtKeyMappingCreateMatchesPlan(observed, data) {
		setJWTKeyMappingIdentityOnly(&data, created.ID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("JWT Key Mapping Create Not Confirmed", "Authoritative read-back did not confirm the requested observable fields. Only the confirmed UUID was retained for recovery; sensitive values were omitted.")
		return
	}
	setJWTKeyMappingResourceState(&data, observed)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !data.Description.IsNull() && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, jwtKeyMappingDescriptionOwnedPrivateKey, []byte("true"))...)
	}
}

func jwtKeyMappingCreateMatchesPlan(mapping jwtKeyMappingObject, data JWTKeyMappingResourceModel) bool {
	if mapping.ClaimName != data.ClaimName.ValueString() || mapping.ClaimValue != data.ClaimValue.ValueString() || !mapping.IsActive {
		return false
	}
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
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	var key types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("key_wo"), &key)...)
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
	rotation := !plan.KeyWOVersion.Equal(state.KeyWOVersion)
	if rotation {
		if key.IsNull() || key.IsUnknown() || key.ValueString() == "" {
			resp.Diagnostics.AddError("Invalid JWT Key Mapping Rotation", "A known non-empty key_wo is required when key_wo_version changes.")
			return
		}
		body["key"] = key.ValueString()
		changed = true
	}
	if !changed {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
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
	observed, err := readJWTKeyMapping(ctx, r.client, state.ID.ValueString())
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
	setJWTKeyMappingResourceState(&plan, observed)
	plan.ID, plan.KeyWO = state.ID, types.StringNull()
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if _, ok := body["description"]; ok && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, jwtKeyMappingDescriptionOwnedPrivateKey, []byte("true"))...)
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
	if deleteErr != nil && IsAPIErrorStatus(deleteErr, http.StatusNotFound) {
		return
	}
	if deleteErr == nil {
		if err := validateJWTKeyMappingDeleteResponse(raw); err != nil {
			deleteErr = err
		}
	}
	if deleteErr != nil && !accepted {
		resp.Diagnostics.AddError("JWT Key Mapping Delete Failed", mappingMutationDiagnostic("delete", deleteErr))
		return
	}
	_, readErr := readJWTKeyMapping(ctx, r.client, id)
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
