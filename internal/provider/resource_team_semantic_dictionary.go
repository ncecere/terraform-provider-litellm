package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	teamMetadataJSONProvenancePrivateKey = "team_metadata_json_provenance_v1"
	teamAcceptedCreateRecoveryPrivateKey = "team_semantic_create_accepted_v1"
	teamPendingUpdatePrivateKey          = "team_semantic_update_pending_v1"
	teamPendingMemberDefaultsPrivateKey  = "team_member_defaults_pending_v1"
)

var teamMetadataJSONReservedKeys = []string{
	"tags", "guardrails", "prompts", "model_rpm_limit", "model_tpm_limit",
	"rpm_limit_type", "tpm_limit_type", "team_member_budget_id",
}

var teamPendingMemberDefaultAllowedFields = map[string]bool{
	"team_member_budget": true, "team_member_budget_duration": true,
	"team_member_rpm_limit": true, "team_member_tpm_limit": true,
}

type teamPendingMemberDefaults map[string]bool

type teamPendingMemberDefaultsWire struct {
	Version int      `json:"version"`
	Fields  []string `json:"fields"`
}

func encodeTeamPendingMemberDefaults(ctx context.Context, fields teamPendingMemberDefaults) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(fields))
	for name, present := range fields {
		if !present || !teamPendingMemberDefaultAllowedFields[name] {
			return nil, errSemanticDictionaryPrivate
		}
		names = append(names, name)
	}
	sort.Strings(names)
	raw, err := json.Marshal(teamPendingMemberDefaultsWire{Version: 1, Fields: names})
	if err != nil || len(raw) > jsonDecodeMaxInputBytes {
		return nil, errSemanticDictionaryPrivate
	}
	return raw, nil
}

func changedTeamPendingMemberDefaults(plan, prior TeamResourceModel, request map[string]interface{}) teamPendingMemberDefaults {
	fields := teamPendingMemberDefaults{}
	for _, field := range []struct {
		wire        string
		plan, prior attr.Value
	}{
		{"team_member_budget", plan.TeamMemberBudget, prior.TeamMemberBudget},
		{"team_member_budget_duration", plan.MemberBudgetDuration, prior.MemberBudgetDuration},
		{"team_member_rpm_limit", plan.TeamMemberRPMLimit, prior.TeamMemberRPMLimit},
		{"team_member_tpm_limit", plan.TeamMemberTPMLimit, prior.TeamMemberTPMLimit},
	} {
		if _, dispatched := request[field.wire]; dispatched && !field.plan.IsUnknown() && !field.plan.Equal(field.prior) {
			fields[field.wire] = true
		}
	}
	return fields
}

func decodeTeamPendingMemberDefaults(ctx context.Context, raw []byte) (teamPendingMemberDefaults, error) {
	if len(raw) == 0 {
		return teamPendingMemberDefaults{}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var wire teamPendingMemberDefaultsWire
	if len(raw) > jsonDecodeMaxInputBytes || decodeJSONUseNumber(raw, &wire) != nil || wire.Version != 1 || len(wire.Fields) == 0 {
		return nil, errSemanticDictionaryPrivate
	}
	fields := teamPendingMemberDefaults{}
	for _, name := range wire.Fields {
		if !teamPendingMemberDefaultAllowedFields[name] || fields[name] {
			return nil, errSemanticDictionaryPrivate
		}
		fields[name] = true
	}
	canonical, err := encodeTeamPendingMemberDefaults(ctx, fields)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, errSemanticDictionaryPrivate
	}
	return fields, nil
}

type teamSemanticPrepared struct {
	object     map[string]interface{}
	provenance semanticDictionaryProvenance
}

type teamSemanticOwnership struct {
	provenance           semanticDictionaryProvenance
	removals             semanticDictionaryPathSet
	transitionRemovals   semanticDictionaryPathSet
	pending              keySemanticPendingTransition
	reconcile            *keySemanticPendingReconcile
	acceptedCreate       bool
	pendingMemberFields  teamPendingMemberDefaults
	fresh                bool
	confirmCurrentValue  bool
	expectMemberBudgetID bool
}

func teamUnconfiguredSemanticProvenance() semanticDictionaryProvenance {
	value := emptySemanticDictionaryProvenance()
	value.Initialized = true
	return value
}

func prepareTeamSemanticDictionary(ctx context.Context, value types.String, legacy types.Map) (teamSemanticPrepared, error) {
	provenance := teamUnconfiguredSemanticProvenance()
	if value.IsNull() {
		return teamSemanticPrepared{provenance: provenance}, nil
	}
	if value.IsUnknown() {
		return teamSemanticPrepared{}, errors.New("semantic team dictionary configuration is unknown")
	}
	object, err := parseSemanticDictionary(ctx, value.ValueString())
	if err != nil || validateModelSemanticDictionaryNumbers(ctx, object) != nil {
		return teamSemanticPrepared{}, errSemanticDictionaryTraversal
	}
	if err := semanticDictionaryTopLevelOverlap(ctx, object, configuredAdditionalParamKeys(legacy), teamMetadataJSONReservedKeys); err != nil {
		return teamSemanticPrepared{}, err
	}
	paths, err := semanticDictionaryLeafPaths(ctx, object)
	if err != nil {
		return teamSemanticPrepared{}, err
	}
	provenance.Configured = true
	provenance.TerraformOwned = paths
	return teamSemanticPrepared{object: object, provenance: provenance}, nil
}

func decodeTeamSemanticProvenance(ctx context.Context, raw []byte, state types.String) (semanticDictionaryProvenance, error) {
	if len(raw) == 0 {
		if knownString(state) {
			return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
		}
		return teamUnconfiguredSemanticProvenance(), nil
	}
	value, err := decodeSemanticDictionaryProvenance(ctx, raw)
	if err != nil || !value.Initialized || value.Configured != knownString(state) || len(value.APIOwned) != 0 || len(value.PendingTerraformOwned) != 0 || len(value.PendingAPIOwned) != 0 || len(value.PendingRemovals) != 0 {
		return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
	}
	if !value.Configured {
		if len(value.TerraformOwned) != 0 {
			return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
		}
		return value, nil
	}
	object, parseErr := parseSemanticDictionary(ctx, state.ValueString())
	expected, pathErr := semanticDictionaryLeafPaths(ctx, object)
	if parseErr != nil || pathErr != nil || validateModelSemanticDictionaryNumbers(ctx, object) != nil || !modelSemanticDictionaryPathSetsEqual(expected, value.TerraformOwned) {
		return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
	}
	return value, nil
}

func (p teamSemanticPrepared) updateOwnership(ctx context.Context, prior semanticDictionaryProvenance) (teamSemanticOwnership, error) {
	removed, err := keySemanticDictionaryRemovedPaths(ctx, prior.TerraformOwned, p.provenance.TerraformOwned)
	if err != nil {
		return teamSemanticOwnership{}, err
	}
	projection, err := keySemanticProjectionRemovals(ctx, p.provenance.TerraformOwned, removed)
	if err != nil {
		return teamSemanticOwnership{}, err
	}
	return teamSemanticOwnership{provenance: p.provenance, removals: projection, transitionRemovals: removed, fresh: true, confirmCurrentValue: true}, nil
}

func pendingTeamSemanticTransition(ownership teamSemanticOwnership) keySemanticPendingTransition {
	if len(ownership.transitionRemovals) == 0 {
		return keySemanticPendingTransition{}
	}
	return keySemanticPendingTransition{Metadata: keySemanticPendingRoot{Active: true, Configured: ownership.provenance.Configured, TerraformOwned: ownership.provenance.TerraformOwned, Removals: ownership.transitionRemovals}}
}

func resolveTeamSemanticPending(ctx context.Context, metadata map[string]interface{}, ownership teamSemanticOwnership) (teamSemanticOwnership, keySemanticPendingReconcile, error) {
	keyOwnership := keySemanticReadOwnership{metadata: ownership.provenance, config: teamUnconfiguredSemanticProvenance(), permissions: teamUnconfiguredSemanticProvenance(), pending: ownership.pending}
	effective, reconcile, err := resolveKeySemanticPendingTransition(ctx, map[string]interface{}{"metadata": metadata}, keyOwnership)
	if err != nil {
		return teamSemanticOwnership{}, keySemanticPendingReconcile{}, err
	}
	ownership.provenance = effective.metadata
	ownership.removals = effective.metadataRemovals
	ownership.pending = keySemanticPendingTransition{}
	ownership.confirmCurrentValue = false
	return ownership, reconcile, nil
}

func teamMetadataObject(ctx context.Context, teamInfo map[string]interface{}) (map[string]interface{}, apiValuePresence, error) {
	metadata, presence, err := optionalObjectAt(teamInfo, "metadata")
	if err != nil {
		return nil, presence, err
	}
	if presence != apiValuePresent {
		return map[string]interface{}{}, presence, nil
	}
	if metadata == nil || validateSemanticDictionaryValue(ctx, metadata) != nil || validateModelSemanticDictionaryNumbers(ctx, metadata) != nil {
		return nil, presence, errSemanticDictionaryTraversal
	}
	return metadata, presence, nil
}

func overlayTeamCreateSemantic(ctx context.Context, request map[string]interface{}, prepared teamSemanticPrepared) error {
	if !prepared.provenance.Configured {
		return nil
	}
	base := map[string]interface{}{}
	if raw, present := request["metadata"]; present {
		var ok bool
		base, ok = raw.(map[string]interface{})
		if !ok || base == nil {
			return errSemanticDictionaryTraversal
		}
	}
	if _, forbidden := base["team_member_budget_id"]; forbidden {
		return errSemanticDictionaryOverlap
	}
	result, err := overlaySemanticDictionaryObject(ctx, base, prepared.object)
	if err != nil || validateSemanticDictionaryValue(ctx, result) != nil || validateModelSemanticDictionaryNumbers(ctx, result) != nil {
		return errSemanticDictionaryTraversal
	}
	request["metadata"] = result
	return nil
}

func teamLegacyMetadataObject(ctx context.Context, value types.Map) (map[string]interface{}, error) {
	if value.IsNull() || value.IsUnknown() {
		return map[string]interface{}{}, nil
	}
	var values map[string]string
	if diagnostics := value.ElementsAs(ctx, &values, false); diagnostics.HasError() {
		return nil, errSemanticDictionaryTraversal
	}
	result := convertMetadataToNative(values)
	if _, forbidden := result["team_member_budget_id"]; forbidden {
		return nil, errSemanticDictionaryOverlap
	}
	for _, root := range teamMetadataJSONReservedKeys {
		delete(result, root)
	}
	if validateSemanticDictionaryValue(ctx, result) != nil {
		return nil, errSemanticDictionaryTraversal
	}
	return result, nil
}

func putTeamDedicatedMetadata(ctx context.Context, result map[string]interface{}, plan TeamResourceModel) error {
	for _, entry := range []struct {
		name  string
		value types.List
	}{{"tags", plan.Tags}, {"guardrails", plan.Guardrails}, {"prompts", plan.Prompts}} {
		if !knownList(entry.value) {
			continue
		}
		values, err := stringListRequest(ctx, entry.value, entry.name)
		if err != nil {
			return err
		}
		native := make([]interface{}, len(values))
		for index, value := range values {
			native[index] = value
		}
		result[entry.name] = native
	}
	for _, entry := range []struct {
		name  string
		value types.Map
	}{{"model_rpm_limit", plan.ModelRPMLimit}, {"model_tpm_limit", plan.ModelTPMLimit}} {
		if !knownMap(entry.value) {
			continue
		}
		values, err := int64RequestMap(entry.value, entry.name)
		if err != nil {
			return err
		}
		native := make(map[string]interface{}, len(values))
		for name, value := range values {
			native[name] = json.Number(strconv.FormatInt(value, 10))
		}
		result[entry.name] = native
	}
	for _, entry := range []struct {
		name  string
		value types.String
	}{{"rpm_limit_type", plan.RPMLimitType}, {"tpm_limit_type", plan.TPMLimitType}} {
		if knownString(entry.value) {
			result[entry.name] = entry.value.ValueString()
		}
	}
	return nil
}

func teamMetadataHasNonCallbackMask(ctx context.Context, value interface{}, path []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch typed := value.(type) {
	case string:
		if isMaskedMetadataAPIString(typed) && !keyMetadataCallbackCiphertext(path, typed) {
			return errSemanticDictionaryMasked
		}
	case map[string]interface{}:
		for name, child := range typed {
			if err := teamMetadataHasNonCallbackMask(ctx, child, append(path, name)); err != nil {
				return err
			}
		}
	case []interface{}:
		for index, child := range typed {
			if err := teamMetadataHasNonCallbackMask(ctx, child, append(path, strconv.Itoa(index))); err != nil {
				return err
			}
		}
	}
	return nil
}

func teamCallbackRecoveryOwnership(ctx context.Context, priorJSON types.String, priorLegacy types.Map, provenance semanticDictionaryProvenance) (map[string]interface{}, semanticDictionaryPathSet, error) {
	prior := map[string]interface{}{}
	owned := cloneSemanticDictionaryPathSet(provenance.TerraformOwned)
	if provenance.Configured {
		if !knownString(priorJSON) {
			return nil, nil, errSemanticDictionaryPrivate
		}
		semantic, err := parseSemanticDictionary(ctx, priorJSON.ValueString())
		if err != nil {
			return nil, nil, errSemanticDictionaryPrivate
		}
		prior, err = overlaySemanticDictionaryObject(ctx, prior, semantic)
		if err != nil {
			return nil, nil, errSemanticDictionaryPrivate
		}
	}
	if knownMap(priorLegacy) {
		for name, raw := range priorLegacy.Elements() {
			value, ok := raw.(types.String)
			if !ok || value.IsNull() || value.IsUnknown() {
				continue
			}
			converted := convertMetadataToNative(map[string]string{name: value.ValueString()})
			prior[name] = converted[name]
			pointer, err := encodeSemanticDictionaryPointer([]string{name})
			if err != nil || owned[pointer] {
				return nil, nil, errSemanticDictionaryPrivate
			}
			owned[pointer] = true
		}
	}
	if validateSemanticDictionaryPathSet(ctx, owned) != nil {
		return nil, nil, errSemanticDictionaryPrivate
	}
	return prior, owned, nil
}

func restoreTeamOwnedCallbackCiphertext(ctx context.Context, remote map[string]interface{}, priorJSON types.String, priorLegacy types.Map, provenance semanticDictionaryProvenance) (map[string]interface{}, error) {
	if err := teamMetadataHasNonCallbackMask(ctx, remote, nil); err != nil {
		return nil, err
	}
	contains, err := semanticDictionaryContainsMaskedValue(ctx, remote, nil, keyMetadataCallbackCiphertext)
	if err != nil || !contains {
		return cloneSemanticDictionary(ctx, remote)
	}
	prior, owned, err := teamCallbackRecoveryOwnership(ctx, priorJSON, priorLegacy, provenance)
	if err != nil || len(owned) == 0 {
		return nil, errSemanticDictionaryMasked
	}
	if err := rejectUnownedKeyMetadataCiphertext(ctx, remote, owned, nil); err != nil {
		return nil, err
	}
	projectionProvenance := teamUnconfiguredSemanticProvenance()
	projectionProvenance.Configured = true
	projectionProvenance.TerraformOwned = owned
	projected, err := projectModelAdditionalModelInfoJSON(ctx, remote, projectionProvenance)
	if err != nil {
		return nil, errSemanticDictionaryMasked
	}
	restored, err := restoreSemanticDictionaryMaskedValues(ctx, prior, projected, true, keyMetadataCallbackCiphertext)
	if err != nil {
		return nil, errSemanticDictionaryMasked
	}
	result, err := cloneSemanticDictionary(ctx, remote)
	if err != nil {
		return nil, err
	}
	return overlaySemanticDictionaryObject(ctx, result, restored)
}

func composeTeamMetadataReplacement(ctx context.Context, remote map[string]interface{}, plan, prior TeamResourceModel, priorProvenance semanticDictionaryProvenance, prepared teamSemanticPrepared, request map[string]interface{}) (map[string]interface{}, bool, error) {
	result, err := restoreTeamOwnedCallbackCiphertext(ctx, remote, prior.MetadataJSON, prior.Metadata, priorProvenance)
	if err != nil {
		return nil, false, err
	}
	memberBudgetID, hasMemberBudgetID := result["team_member_budget_id"]
	if hasMemberBudgetID {
		memberBudgetText, validMemberBudgetID := memberBudgetID.(string)
		if !validMemberBudgetID || memberBudgetText == "" {
			return nil, false, errSemanticDictionaryTraversal
		}
		willRestore := false
		for name := range teamPendingMemberDefaultAllowedFields {
			if value, present := request[name]; present && value != nil {
				willRestore = true
				break
			}
		}
		if !willRestore {
			return nil, false, errSemanticDictionaryTraversal
		}
		delete(result, "team_member_budget_id")
	}
	if knownMap(prior.Metadata) {
		for name := range prior.Metadata.Elements() {
			delete(result, name)
		}
	}
	for _, entry := range []struct {
		name  string
		owned bool
	}{
		{"tags", knownList(prior.Tags) || knownList(plan.Tags)},
		{"guardrails", knownList(prior.Guardrails) || knownList(plan.Guardrails)},
		{"prompts", knownList(prior.Prompts) || knownList(plan.Prompts)},
		{"model_rpm_limit", knownMap(prior.ModelRPMLimit) || knownMap(plan.ModelRPMLimit)},
		{"model_tpm_limit", knownMap(prior.ModelTPMLimit) || knownMap(plan.ModelTPMLimit)},
		{"rpm_limit_type", knownString(prior.RPMLimitType) || knownString(plan.RPMLimitType)},
		{"tpm_limit_type", knownString(prior.TPMLimitType) || knownString(plan.TPMLimitType)},
	} {
		if entry.owned {
			delete(result, entry.name)
		}
	}
	result, _, err = applySemanticDictionary(ctx, result, map[string]interface{}{}, priorProvenance.TerraformOwned)
	if err != nil {
		return nil, false, err
	}
	legacy, err := teamLegacyMetadataObject(ctx, plan.Metadata)
	if err != nil {
		return nil, false, err
	}
	for name, value := range legacy {
		result[name] = value
	}
	if err := putTeamDedicatedMetadata(ctx, result, plan); err != nil {
		return nil, false, err
	}
	if prepared.provenance.Configured {
		result, err = overlaySemanticDictionaryObject(ctx, result, prepared.object)
		if err != nil {
			return nil, false, err
		}
	}
	delete(result, "team_member_budget_id")
	if validateSemanticDictionaryValue(ctx, result) != nil || validateModelSemanticDictionaryNumbers(ctx, result) != nil {
		return nil, false, errSemanticDictionaryTraversal
	}
	return result, hasMemberBudgetID, nil
}

func projectTeamSemanticMetadata(ctx context.Context, current types.String, metadata map[string]interface{}, ownership teamSemanticOwnership) (types.String, error) {
	if err := verifyKeySemanticDictionaryRemovals(ctx, metadata, ownership.removals); err != nil {
		return types.StringNull(), err
	}
	if !ownership.provenance.Configured {
		return types.StringNull(), nil
	}
	projected, err := projectModelAdditionalModelInfoJSON(ctx, metadata, ownership.provenance)
	if err != nil {
		return types.StringNull(), err
	}
	prior, err := parseSemanticDictionary(ctx, current.ValueString())
	if err != nil {
		return types.StringNull(), errSemanticDictionaryPrivate
	}
	projected, err = restoreSemanticDictionaryMaskedValues(ctx, prior, projected, true, keyMetadataCallbackCiphertext)
	if err != nil {
		return types.StringNull(), err
	}
	if ownership.confirmCurrentValue {
		desired, parseErr := parseSemanticDictionary(ctx, current.ValueString())
		equal, compareErr := semanticDictionaryValuesEqual(ctx, desired, projected)
		if parseErr != nil || compareErr != nil || !equal {
			return types.StringNull(), errSemanticDictionaryTraversal
		}
	}
	return reconcileSemanticDictionaryString(ctx, current, projected)
}

func projectTeamLegacyMetadataWithSemantic(ctx context.Context, current types.Map, metadata map[string]interface{}, ownership teamSemanticOwnership, imported bool) (types.Map, error) {
	ownedTop := map[string]bool{}
	if ownership.provenance.Configured {
		var err error
		ownedTop, err = semanticDictionaryTopLevelOwnedKeys(ctx, ownership.provenance)
		if err != nil {
			return types.MapNull(types.StringType), err
		}
	}
	filtered := map[string]interface{}{}
	for name, value := range metadata {
		if ownedTop[name] {
			continue
		}
		reserved := false
		for _, root := range teamMetadataJSONReservedKeys {
			if name == root {
				reserved = true
				break
			}
		}
		if !reserved {
			filtered[name] = value
		}
	}
	if ownership.acceptedCreate {
		filtered = map[string]interface{}{}
	} else if ownership.provenance.Configured || !imported {
		managed := map[string]interface{}{}
		if knownMap(current) {
			for name := range current.Elements() {
				if value, present := filtered[name]; present {
					managed[name] = value
				}
			}
		}
		filtered = managed
	}
	for name, value := range filtered {
		if err := teamMetadataHasNonCallbackMask(ctx, map[string]interface{}{name: value}, nil); err != nil {
			return types.MapNull(types.StringType), errSemanticDictionaryMasked
		}
		contains, err := semanticDictionaryContainsMaskedValue(ctx, map[string]interface{}{name: value}, nil, keyMetadataCallbackCiphertext)
		if err != nil {
			return types.MapNull(types.StringType), err
		}
		if contains {
			configured, ok := current.Elements()[name].(types.String)
			if !ok || configured.IsNull() || configured.IsUnknown() {
				return types.MapNull(types.StringType), errSemanticDictionaryMasked
			}
			priorValue := convertMetadataToNative(map[string]string{name: configured.ValueString()})
			ownedPointer, pointerErr := encodeSemanticDictionaryPointer([]string{name})
			if pointerErr != nil {
				return types.MapNull(types.StringType), errSemanticDictionaryPrivate
			}
			if err := rejectUnownedKeyMetadataCiphertext(ctx, map[string]interface{}{name: value}, semanticDictionaryPathSet{ownedPointer: true}, nil); err != nil {
				return types.MapNull(types.StringType), err
			}
			restored, restoreErr := restoreSemanticDictionaryMaskedValues(ctx, priorValue, map[string]interface{}{name: value}, true, keyMetadataCallbackCiphertext)
			if restoreErr != nil {
				return types.MapNull(types.StringType), errSemanticDictionaryMasked
			}
			value = restored[name]
			filtered[name] = value
		}
		switch value.(type) {
		case string, map[string]interface{}, []interface{}, nil:
		default:
			return types.MapNull(types.StringType), errSemanticDictionaryTraversal
		}
	}
	if len(filtered) == 0 {
		if current.IsUnknown() || ownership.acceptedCreate {
			return types.MapNull(types.StringType), nil
		}
		if current.IsNull() {
			return current, nil
		}
		empty, diagnostics := checkedStringMapValue(ctx, map[string]attr.Value{}, path.Root("metadata"), false)
		if collectionProjectionError(ctx, diagnostics) != nil {
			return types.MapNull(types.StringType), errSemanticDictionaryTraversal
		}
		return empty, nil
	}
	values := make(map[string]attr.Value, len(filtered))
	for name, value := range filtered {
		if value == nil {
			values[name] = types.StringNull()
			continue
		}
		observed := metadataValueToString(value)
		if configured, ok := current.Elements()[name].(types.String); ok && knownString(configured) {
			priorValue := convertMetadataToNative(map[string]string{name: configured.ValueString()})[name]
			if equal, compareErr := semanticDictionaryValuesEqual(ctx, priorValue, value); compareErr == nil && equal {
				observed = configured.ValueString()
			}
		}
		values[name] = types.StringValue(observed)
	}
	result, diagnostics := types.MapValue(types.StringType, values)
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), errSemanticDictionaryTraversal
	}
	return result, nil
}

func partialTeamSemanticRecoveryState(identity string) TeamResourceModel {
	return partialTeamState(identity)
}

func marshalTeamUpgrade(raw []byte) ([]byte, error) {
	var prior map[string]json.RawMessage
	if err := json.Unmarshal(raw, &prior); err != nil {
		return nil, err
	}
	prior["metadata_json"] = json.RawMessage("null")
	return json.Marshal(prior)
}
