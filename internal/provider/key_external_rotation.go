package provider

import (
	"context"
	"errors"
	"reflect"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var externalRotationAttributeTypes = map[string]attr.Type{"secret_arn": types.StringType, "version_hash": types.StringType}

const externalKeyPlannedIdentityPrivateKey = "external_key_planned_identity"

func keyServiceAccountReplacementModifier() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
		var rotation types.Object
		resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("external_rotation"), &rotation)...)
		resp.RequiresReplace = !keyExternalRotationEnabled(rotation)
	}, "Service account changes replace provider-owned keys; external enrollment adopts a verified identity.", "Service account changes replace provider-owned keys; external enrollment adopts a verified identity.")
}

func keyExternalRotationAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Description: "Opt in to managing an existing externally rotated key. Reads AWSCURRENT using the AWS SDK default credential chain and the ARN's region; verifies service_account_id metadata, team_id, project_id, key_alias, and active status. Never generates a key. Destroy only forgets the binding and never deletes the external key. Requires explicit known identity fields; key/key_wo, invitation actions, and semantic JSON ownership are unsupported. Removing this configuration requires forgetting the resource first.",
		Optional:    true,
		Attributes: map[string]schema.Attribute{
			"secret_arn": schema.StringAttribute{
				Description: "Full Secrets Manager secret ARN, including region, twelve-digit account ID, and six-character secret suffix. AWSCURRENT must contain only the raw sk- token in SecretString.",
				Required:    true,
			},
			"version_hash": schema.StringAttribute{
				Description: "Non-sensitive SHA256 fingerprint of the verified AWSCURRENT version, used to detect plan/apply races. Never configure this value.",
				Computed:    true,
			},
		},
	}
}

func keyExternalRotationEnabled(value types.Object) bool { return !value.IsNull() }

func externalKeyConfiguration(data KeyResourceModel, configured bool) (string, error) {
	if data.ExternalRotation.IsNull() || data.ExternalRotation.IsUnknown() {
		return "", errors.New("external_rotation must be known before adopting or managing an external key")
	}
	secretARN, ok := data.ExternalRotation.Attributes()["secret_arn"].(types.String)
	if !ok || secretARN.IsNull() || secretARN.IsUnknown() {
		return "", errExternalSecretARN
	}
	if _, err := parseExternalSecretARN(secretARN.ValueString()); err != nil {
		return "", err
	}
	for _, value := range []types.String{data.ServiceAccountID, data.TeamID, data.ProjectID, data.KeyAlias} {
		if value.IsNull() || value.IsUnknown() || value.ValueString() == "" {
			return "", errors.New("external_rotation requires explicit known nonempty service_account_id, team_id, project_id, and key_alias")
		}
	}
	if !data.KeyWO.IsNull() || !data.KeyWOVersion.IsNull() || (configured && !data.Key.IsNull()) {
		return "", errors.New("external_rotation cannot own key, key_wo, or key_wo_version; omit these attributes and let the provider resolve the external token")
	}
	if !data.MetadataJSON.IsNull() || !data.ConfigJSON.IsNull() || !data.PermissionsJSON.IsNull() {
		return "", errors.New("external_rotation cannot be combined with semantic JSON dictionary ownership; forget an existing semantic binding before adopting it externally")
	}
	if !data.SendInviteEmail.IsNull() {
		return "", errors.New("external_rotation does not support send_invite_email or other key-generation actions")
	}
	if !data.Blocked.IsNull() && (data.Blocked.IsUnknown() || data.Blocked.ValueBool()) {
		return "", errors.New("externally rotated keys must remain active; blocked must be omitted or false")
	}
	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		if value, present := data.Metadata.Elements()["service_account_id"]; present && !value.Equal(data.ServiceAccountID) {
			return "", errors.New("metadata.service_account_id must agree with the dedicated service_account_id identity")
		}
	}
	return secretARN.ValueString(), nil
}

// externalKeySnapshot resolves before any gateway lookup; it never uses the old
// state's token, which may already be archived after a rotation.
func (r *KeyResource) externalKeySnapshot(ctx context.Context, source KeyResourceModel) (KeyResourceModel, map[string]interface{}, externalKeyCredential, error) {
	secretARN, err := externalKeyConfiguration(source, false)
	if err != nil {
		return source, nil, externalKeyCredential{}, err
	}
	credential, err := r.resolveExternalKey(ctx, secretARN)
	if err != nil {
		return source, nil, externalKeyCredential{}, err
	}
	next := source
	next.Key = types.StringValue(credential.token)
	next.ID = types.StringValue(hashKeyForID(credential.token))
	rotation, diagnostics := types.ObjectValue(externalRotationAttributeTypes, map[string]attr.Value{
		"secret_arn": types.StringValue(secretARN), "version_hash": types.StringValue(hashKeyForID(credential.version)),
	})
	if diagnostics.HasError() {
		return source, nil, externalKeyCredential{}, errors.New("external key enrollment could not be encoded safely")
	}
	next.ExternalRotation = rotation
	result, info, err := r.getSafeExactKeyInfo(ctx, &next)
	if err != nil {
		return source, nil, externalKeyCredential{}, errors.New("the external key could not be read or its exact hashed identity could not be verified; prior state was retained and gateway response details were omitted")
	}
	// LiteLLM 1.106.4 strips info.token; getSafeExactKeyInfo verifies the
	// root hash echo and validates any nested token/key when present.
	if info["status"] != "active" || info["blocked"] != false {
		return source, nil, externalKeyCredential{}, errors.New("the external key must have the exact resolved token hash and active status, and must not be blocked; prior state was retained and response values were omitted")
	}
	serviceAccountID, present, err := keyResponseServiceAccountID(info)
	if err != nil || !present || serviceAccountID != source.ServiceAccountID.ValueString() || info["team_id"] != source.TeamID.ValueString() || info["project_id"] != source.ProjectID.ValueString() || info["key_alias"] != source.KeyAlias.ValueString() {
		return source, nil, externalKeyCredential{}, errors.New("the external key does not match the explicit service-account metadata, team, project, and alias identity; prior state was retained and response values were omitted")
	}
	if err := projectKeyInfoWithOwnership(ctx, &next, false, keySemanticReadOwnership{}, result, info); err != nil {
		return source, nil, externalKeyCredential{}, errors.New("the verified external key snapshot could not be projected safely; prior state was retained and response values were omitted")
	}
	// Optional-only fields never adopt unrelated API defaults. The identity
	// tuple has already been checked above; all other optional-only values retain
	// their configured/state presence unless the caller explicitly owns them.
	for _, field := range []struct {
		source types.String
		target *types.String
	}{
		{source.UserID, &next.UserID}, {source.OrganizationID, &next.OrganizationID},
		{source.Duration, &next.Duration}, {source.TPMLimitType, &next.TPMLimitType},
		{source.RPMLimitType, &next.RPMLimitType}, {source.BudgetDuration, &next.BudgetDuration},
	} {
		if field.source.IsNull() {
			*field.target = field.source
		}
	}
	if source.Metadata.IsNull() {
		next.Metadata = source.Metadata
	} else if !source.Metadata.IsUnknown() {
		// Dedicated/API-owned metadata is verified or hydrated independently;
		// only explicit legacy keys belong in the public metadata projection.
		elements := make(map[string]attr.Value, len(source.Metadata.Elements()))
		for name := range source.Metadata.Elements() {
			if value, present := next.Metadata.Elements()[name]; present {
				elements[name] = value
			}
		}
		metadata, diagnostics := checkedStringMapValue(ctx, elements, path.Root("metadata"), true)
		if diagnostics.HasError() {
			return source, nil, externalKeyCredential{}, errors.New("external key metadata could not be projected safely")
		}
		next.Metadata = metadata
	}
	// Dictionaries remain legacy map-owned, never semantic JSON-owned.
	next.MetadataJSON = types.StringNull()
	next.ConfigJSON = types.StringNull()
	next.PermissionsJSON = types.StringNull()
	return next, info, credential, nil
}

func externalKeyDiagnostic(diagnostics *diag.Diagnostics, err error) {
	diagnostics.AddAttributeError(path.Root("external_rotation"), "External Key Management Failed", err.Error())
}

// Only explicit configuration owns mutable attributes. This also resolves
// Optional+Computed fields from the snapshot without sending API defaults back.
func externalKeyDesired(config, snapshot KeyResourceModel) KeyResourceModel {
	desired := snapshot
	configured := reflect.ValueOf(config)
	target := reflect.ValueOf(&desired).Elem()
	typeOf := configured.Type()
	for index := 0; index < configured.NumField(); index++ {
		name := typeOf.Field(index).Name
		if name == "ID" || name == "Key" || name == "KeyWO" || name == "KeyWOVersion" || name == "ExternalRotation" {
			continue
		}
		value := configured.Field(index).Interface().(attr.Value)
		if !value.IsNull() {
			target.Field(index).Set(configured.Field(index))
		}
	}
	// Optional-only removal semantics remain explicit.
	desired.TPDLimit = config.TPDLimit
	desired.RouterSettings = config.RouterSettings
	return desired
}

func externalKeyHasDrift(config, observed KeyResourceModel) bool {
	configured := reflect.ValueOf(config)
	remote := reflect.ValueOf(observed)
	for index := 0; index < configured.NumField(); index++ {
		name := configured.Type().Field(index).Name
		if name == "ID" || name == "Key" || name == "KeyWO" || name == "KeyWOVersion" || name == "ExternalRotation" {
			continue
		}
		value := configured.Field(index).Interface().(attr.Value)
		if !value.IsNull() && !value.Equal(remote.Field(index).Interface().(attr.Value)) {
			return true
		}
	}
	return false
}

func externalKeySameIdentity(config, state KeyResourceModel) bool {
	return config.ExternalRotation.Attributes()["secret_arn"].Equal(state.ExternalRotation.Attributes()["secret_arn"]) && config.ServiceAccountID.Equal(state.ServiceAccountID) && config.TeamID.Equal(state.TeamID) && config.ProjectID.Equal(state.ProjectID) && config.KeyAlias.Equal(state.KeyAlias)
}

func (r *KeyResource) modifyExternalKeyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) bool {
	if req.Plan.Raw.IsNull() {
		return false
	} // Destroy is always forget-only.
	var config, state KeyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	}
	if resp.Diagnostics.HasError() {
		return true
	}
	if !keyExternalRotationEnabled(config.ExternalRotation) {
		if !req.State.Raw.IsNull() && keyExternalRotationEnabled(state.ExternalRotation) {
			externalKeyDiagnostic(&resp.Diagnostics, errors.New("external_rotation cannot be removed in place; destroy or forget this binding before managing a provider-owned key"))
			return true
		}
		return false
	}
	if _, err := externalKeyConfiguration(config, true); err != nil {
		externalKeyDiagnostic(&resp.Diagnostics, err)
		return true
	}
	if !req.State.Raw.IsNull() {
		if !state.MetadataJSON.IsNull() || !state.ConfigJSON.IsNull() || !state.PermissionsJSON.IsNull() || !state.KeyWOVersion.IsNull() {
			externalKeyDiagnostic(&resp.Diagnostics, errors.New("an existing write-only or semantic JSON binding cannot enroll in external_rotation; forget it before adopting externally"))
			return true
		}
		if keyExternalRotationEnabled(state.ExternalRotation) && !externalKeySameIdentity(config, state) {
			externalKeyDiagnostic(&resp.Diagnostics, errors.New("an enrolled external key's secret ARN and identity tuple cannot change in place; forget the binding before adopting another identity"))
			return true
		}
	}
	snapshot, _, _, err := r.externalKeySnapshot(ctx, config)
	if err != nil {
		externalKeyDiagnostic(&resp.Diagnostics, err)
		return true
	}
	plan := externalKeyDesired(config, snapshot)
	if !req.State.Raw.IsNull() && keyExternalRotationEnabled(state.ExternalRotation) {
		// Refresh adopts current external credentials into state. Planning must
		// preserve that verified state identity: Terraform re-expands saved plans
		// using original prior private data and resets computed plan values.
		// Apply re-resolves AWSCURRENT and refuses races before mutation.
		plan.ID, plan.Key, plan.ExternalRotation = state.ID, state.Key, state.ExternalRotation
	} else {
		// First-time adoption has no immutable credential identity in state yet.
		// Leave the credential/version computed until apply, rather than fixing
		// a value which may rotate during Terraform's final plan expansion.
		plan.ID, plan.Key = types.StringUnknown(), types.StringUnknown()
		rotation, diagnostics := types.ObjectValue(externalRotationAttributeTypes, map[string]attr.Value{
			"secret_arn":   snapshot.ExternalRotation.Attributes()["secret_arn"],
			"version_hash": types.StringUnknown(),
		})
		resp.Diagnostics.Append(diagnostics...)
		if resp.Diagnostics.HasError() {
			return true
		}
		plan.ExternalRotation = rotation
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
	// Enrollment of an already verified service account is adoption, not the
	// ordinary service_account_id replacement/generation lifecycle.
	resp.RequiresReplace = nil
	return true
}

func (r *KeyResource) readExternalKey(ctx context.Context, prior KeyResourceModel, resp *resource.ReadResponse) {
	next, _, _, err := r.externalKeySnapshot(ctx, prior)
	if err != nil {
		externalKeyDiagnostic(&resp.Diagnostics, err)
		return
	}
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, externalKeyPlannedIdentityPrivateKey, nil)...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
}

func (r *KeyResource) sendKeyUpdate(ctx context.Context, payload map[string]interface{}) error {
	return r.client.DoRequestWithResponse(ctx, "POST", "/key/update", payload, nil)
}

// externalKeyUpdatePayload starts from configuration, not a computed snapshot.
// Only explicit settings are sent; the identity tuple is validated, never moved.
func (r *KeyResource) externalKeyUpdatePayload(ctx context.Context, config, prior KeyResourceModel, info map[string]interface{}) (map[string]interface{}, error) {
	payload, diagnostics := r.buildKeyRequest(ctx, &config)
	if diagnostics.HasError() {
		return nil, errors.New("explicit external key settings could not be converted; no update was sent and values were omitted")
	}
	applyKeyRouterSettingsUpdateSemantics(payload, config.RouterSettings, prior.RouterSettings)
	applyKeyProjectAndSoftBudgetUpdateSemantics(payload, config, prior)
	for _, name := range []string{"service_account_id", "team_id", "project_id", "key_alias"} {
		delete(payload, name)
	}
	if config.Metadata.IsNull() {
		delete(payload, "metadata") // dedicated identity was already verified
	} else {
		remote, present, ok := keyResponseObject(info["metadata"])
		if !present || !ok {
			return nil, errors.New("external key metadata could not be hydrated safely; no update was sent")
		}
		// API-native or encrypted siblings may not be round-trippable. Refuse
		// masked/ciphertext roots rather than overwrite external metadata.
		mask := func(p []string, value string) bool {
			return isMaskedMetadataAPIString(value) || keyMetadataCallbackCiphertext(p, value)
		}
		masked, err := semanticDictionaryContainsMaskedValue(ctx, remote, nil, mask)
		if err != nil || masked {
			return nil, errors.New("external key metadata contains masked or encrypted values that cannot be preserved safely; no update was sent")
		}
		merged := make(map[string]interface{}, len(remote))
		for name, value := range remote {
			merged[name] = value
		}
		configured, ok := payload["metadata"].(map[string]interface{})
		if !ok {
			return nil, errors.New("external key metadata could not be converted safely; no update was sent")
		}
		for name, value := range configured {
			merged[name] = value
		}
		merged["service_account_id"] = config.ServiceAccountID.ValueString()
		payload["metadata"] = merged
	}
	hash, err := keyHashFromID(prior.ID.ValueString())
	if err != nil {
		return nil, errors.New("the verified external key hash was unavailable; no update was sent")
	}
	payload["key"] = hash
	return payload, nil
}

func (r *KeyResource) applyExternalKey(ctx context.Context, config, plan KeyResourceModel, prior *KeyResourceModel) (KeyResourceModel, bool, error) {
	if _, err := externalKeyConfiguration(config, true); err != nil {
		return plan, false, err
	}
	snapshot, info, credential, err := r.externalKeySnapshot(ctx, config)
	if err != nil {
		return plan, false, err
	}
	if prior != nil && keyExternalRotationEnabled(prior.ExternalRotation) {
		if plan.ID.IsNull() || plan.ID.IsUnknown() || plan.Key.IsNull() || plan.Key.IsUnknown() || !plan.ID.Equal(snapshot.ID) || !plan.Key.Equal(snapshot.Key) || !plan.ExternalRotation.Equal(snapshot.ExternalRotation) {
			return snapshot, false, errors.New("the external AWSCURRENT credential or version changed after planning, or verified planned identity is missing; no mutation was sent, refresh and create a new plan")
		}
	} else {
		// New adoption resolves computed identity at apply after exact remote
		// ownership verification. Configured identities remain immutable.
		plan.ID, plan.Key, plan.ExternalRotation = snapshot.ID, snapshot.Key, snapshot.ExternalRotation
	}
	observed := snapshot
	if prior != nil {
		// Overlay observed remote fields onto previously owned attributes so
		// optional-only removals use the prior enrollment's presence.
		observed.TPDLimit = prior.TPDLimit
		observed.RouterSettings = prior.RouterSettings
	}
	needsUpdate := externalKeyHasDrift(config, snapshot) || (prior != nil && ((!config.TPDLimit.Equal(prior.TPDLimit)) || !config.RouterSettings.Equal(prior.RouterSettings)))
	if !needsUpdate {
		return plan, true, nil
	}
	payload, err := r.externalKeyUpdatePayload(ctx, config, observed, info)
	if err != nil {
		return snapshot, true, err
	}
	if err := r.sendKeyUpdate(ctx, payload); err != nil {
		return snapshot, true, errors.New("the external key settings update could not be confirmed; the verified adopted binding was retained for refresh and response details were omitted")
	}
	confirmed, confirmedInfo, current, err := r.externalKeySnapshot(ctx, config)
	if err != nil || current != credential || externalKeyHasDrift(config, confirmed) {
		return snapshot, true, errors.New("the external key settings update was accepted but authoritative readback did not confirm the same credential and explicit settings; the verified adopted binding was retained for refresh")
	}
	// Optional-only removals are still owned by the prior enrollment. Inspect
	// the raw fresh exact read above: projecting a null configuration leaves
	// these fields unowned and can hide an ignored clear. Omission is not proof
	// of clearing, especially on older backends that do not expose the field.
	if prior != nil && config.TPDLimit.IsNull() && !prior.TPDLimit.IsNull() && !prior.TPDLimit.IsUnknown() {
		value, present := confirmedInfo["tpd_limit"]
		if !present || value != nil {
			return snapshot, true, errors.New("the external key limit clear could not be confirmed; the verified adopted binding was retained for refresh")
		}
	}
	if prior != nil && config.RouterSettings.IsNull() && !prior.RouterSettings.IsNull() {
		value, present := confirmedInfo["router_settings"]
		matches, comparisonErr := keyRouterSettingsMatchAPI(nil, value)
		if !present || comparisonErr != nil || !matches {
			return snapshot, true, errors.New("the external key router settings clear could not be confirmed; the verified adopted binding was retained for refresh")
		}
	}
	return plan, true, nil
}

func (r *KeyResource) createExternalKey(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) bool {
	var config, plan KeyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return true
	}
	if !keyExternalRotationEnabled(config.ExternalRotation) {
		return false
	}
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return true
	}
	next, retain, err := r.applyExternalKey(ctx, config, plan, nil)
	if err != nil {
		if retain {
			resp.Diagnostics.Append(resp.State.Set(context.WithoutCancel(ctx), &next)...)
		}
		externalKeyDiagnostic(&resp.Diagnostics, err)
		return true
	}
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, externalKeyPlannedIdentityPrivateKey, nil)...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
	return true
}

func (r *KeyResource) updateExternalKey(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) bool {
	var config, plan, state KeyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return true
	}
	if !keyExternalRotationEnabled(config.ExternalRotation) {
		if keyExternalRotationEnabled(state.ExternalRotation) {
			externalKeyDiagnostic(&resp.Diagnostics, errors.New("external_rotation cannot be removed in place; forget the external binding first"))
			return true
		}
		return false
	}
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return true
	}
	next, adopted, err := r.applyExternalKey(ctx, config, plan, &state)
	if err != nil {
		// Once enrollment verified ownership, uncertain settings mutations must
		// retain the external binding, not restore ordinary destructive ownership.
		if adopted && !keyExternalRotationEnabled(state.ExternalRotation) {
			state = next
		}
		resp.Diagnostics.Append(resp.State.Set(context.WithoutCancel(ctx), &state)...)
		externalKeyDiagnostic(&resp.Diagnostics, err)
		return true
	}
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, externalKeyPlannedIdentityPrivateKey, nil)...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &next)...)
	return true
}
