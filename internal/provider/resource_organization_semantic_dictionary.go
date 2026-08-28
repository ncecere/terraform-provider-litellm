package provider

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	organizationMetadataJSONProvenancePrivateKey = "organization_metadata_json_provenance_v1"
	organizationAcceptedCreateRecoveryPrivateKey = "organization_semantic_create_accepted_v1"
	organizationPendingUpdatePrivateKey          = "organization_semantic_update_pending_v1"
)

var organizationMetadataJSONReservedKeys = []string{"model_rpm_limit", "model_tpm_limit"}

func organizationSemanticCreateRecoveryRequired(accepted bool, requestErr error) bool {
	if accepted {
		return true
	}
	observedRejection := false
	walkErrorTree(requestErr, func(node error) {
		status := 0
		switch typed := node.(type) {
		case *APIError:
			status = typed.StatusCode
		case *safeResponseError:
			status = typed.statusCode
		}
		if status != 0 && (status < 200 || status >= 300) {
			observedRejection = true
		}
	})
	if observedRejection {
		return false
	}
	// A request that reached dispatch without a known rejection can have
	// committed before a transport failure, deadline, or cancellation hid the
	// response. Inspect raw typed status metadata above because classifier
	// precedence intentionally suppresses status for some cancellation and
	// deadline outcomes. Local/pre-dispatch failures are not dispatched.
	return ClassifyHTTPFailure(requestErr).RequestDispatched
}

type organizationSemanticPrepared struct {
	object     map[string]interface{}
	provenance semanticDictionaryProvenance
}

type organizationSemanticOwnership struct {
	provenance          semanticDictionaryProvenance
	removals            semanticDictionaryPathSet
	transitionRemovals  semanticDictionaryPathSet
	pending             keySemanticPendingTransition
	reconcile           *keySemanticPendingReconcile
	acceptedCreate      bool
	fresh               bool
	confirmCurrentValue bool
}

func organizationUnconfiguredSemanticProvenance() semanticDictionaryProvenance {
	value := emptySemanticDictionaryProvenance()
	value.Initialized = true
	return value
}

func prepareOrganizationSemanticDictionary(ctx context.Context, value types.String, legacy types.Map) (organizationSemanticPrepared, error) {
	provenance := organizationUnconfiguredSemanticProvenance()
	if value.IsNull() {
		return organizationSemanticPrepared{provenance: provenance}, nil
	}
	if value.IsUnknown() {
		return organizationSemanticPrepared{}, errors.New("semantic organization dictionary configuration is unknown")
	}
	object, err := parseSemanticDictionary(ctx, value.ValueString())
	if err != nil {
		return organizationSemanticPrepared{}, err
	}
	if err := validateModelSemanticDictionaryNumbers(ctx, object); err != nil {
		return organizationSemanticPrepared{}, err
	}
	if err := semanticDictionaryTopLevelOverlap(ctx, object, configuredAdditionalParamKeys(legacy), organizationMetadataJSONReservedKeys); err != nil {
		return organizationSemanticPrepared{}, err
	}
	paths, err := semanticDictionaryLeafPaths(ctx, object)
	if err != nil {
		return organizationSemanticPrepared{}, err
	}
	provenance.Configured = true
	provenance.TerraformOwned = paths
	return organizationSemanticPrepared{object: object, provenance: provenance}, nil
}

func decodeOrganizationSemanticProvenance(ctx context.Context, raw []byte, state types.String) (semanticDictionaryProvenance, error) {
	if len(raw) == 0 {
		if !state.IsNull() && !state.IsUnknown() {
			return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
		}
		return organizationUnconfiguredSemanticProvenance(), nil
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

func encodeOrganizationSemanticProvenance(ctx context.Context, provenance semanticDictionaryProvenance) ([]byte, error) {
	return encodeSemanticDictionaryProvenance(ctx, provenance)
}

func organizationSemanticNeedsChange(ctx context.Context, configured, prior types.String, provenance semanticDictionaryProvenance) (bool, error) {
	return keySemanticDictionaryNeedsChange(ctx, configured, prior, provenance)
}

func (p organizationSemanticPrepared) updateOwnership(ctx context.Context, prior semanticDictionaryProvenance) (organizationSemanticOwnership, error) {
	removed, err := keySemanticDictionaryRemovedPaths(ctx, prior.TerraformOwned, p.provenance.TerraformOwned)
	if err != nil {
		return organizationSemanticOwnership{}, err
	}
	projectionRemovals, err := keySemanticProjectionRemovals(ctx, p.provenance.TerraformOwned, removed)
	if err != nil {
		return organizationSemanticOwnership{}, err
	}
	return organizationSemanticOwnership{
		provenance: p.provenance, removals: projectionRemovals, transitionRemovals: removed,
		fresh: true, confirmCurrentValue: true,
	}, nil
}

func pendingOrganizationSemanticTransition(ownership organizationSemanticOwnership) keySemanticPendingTransition {
	if len(ownership.transitionRemovals) == 0 {
		return keySemanticPendingTransition{}
	}
	return keySemanticPendingTransition{Metadata: keySemanticPendingRoot{
		Active: true, Configured: ownership.provenance.Configured,
		TerraformOwned: ownership.provenance.TerraformOwned, Removals: ownership.transitionRemovals,
	}}
}

func resolveOrganizationSemanticPending(ctx context.Context, metadata map[string]interface{}, ownership organizationSemanticOwnership) (organizationSemanticOwnership, keySemanticPendingReconcile, error) {
	keyOwnership := keySemanticReadOwnership{
		metadata: ownership.provenance, config: organizationUnconfiguredSemanticProvenance(), permissions: organizationUnconfiguredSemanticProvenance(),
		pending: ownership.pending,
	}
	effective, reconcile, err := resolveKeySemanticPendingTransition(ctx, map[string]interface{}{"metadata": metadata}, keyOwnership)
	if err != nil {
		return organizationSemanticOwnership{}, keySemanticPendingReconcile{}, err
	}
	ownership.provenance = effective.metadata
	ownership.removals = effective.metadataRemovals
	ownership.pending = keySemanticPendingTransition{}
	ownership.confirmCurrentValue = false
	return ownership, reconcile, nil
}

func organizationMetadataObject(ctx context.Context, object map[string]interface{}) (map[string]interface{}, error) {
	raw, present := object["metadata"]
	if !present || raw == nil {
		return map[string]interface{}{}, nil
	}
	metadata, ok := raw.(map[string]interface{})
	if !ok || metadata == nil || validateSemanticDictionaryValue(ctx, metadata) != nil || validateModelSemanticDictionaryNumbers(ctx, metadata) != nil {
		return nil, errSemanticDictionaryTraversal
	}
	return metadata, nil
}

func overlayOrganizationCreateSemantic(ctx context.Context, request map[string]interface{}, prepared organizationSemanticPrepared) error {
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
	// The legacy request builder uses map[string]int64 for dedicated rate
	// roots. Normalize those exact integers to the same json.Number domain used
	// by semantic parsing before validating the complete metadata document.
	for _, root := range organizationMetadataJSONReservedKeys {
		if values, ok := base[root].(map[string]int64); ok {
			native := make(map[string]interface{}, len(values))
			for name, value := range values {
				native[name] = json.Number(strconv.FormatInt(value, 10))
			}
			base[root] = native
		}
	}
	result, err := overlaySemanticDictionaryObject(ctx, base, prepared.object)
	if err != nil {
		return err
	}
	if err := validateSemanticDictionaryValue(ctx, result); err != nil {
		return err
	}
	if err := validateModelSemanticDictionaryNumbers(ctx, result); err != nil {
		return err
	}
	request["metadata"] = result // Keep an explicitly configured {} distinct from null.
	return nil
}

func composeOrganizationMetadataReplacement(ctx context.Context, remote map[string]interface{}, plan, prior OrganizationResourceModel, priorProvenance semanticDictionaryProvenance, prepared organizationSemanticPrepared) (map[string]interface{}, error) {
	result, err := cloneSemanticDictionary(ctx, remote)
	if err != nil {
		return nil, err
	}
	for name := range prior.Metadata.Elements() {
		delete(result, name)
	}
	if knownMap(prior.ModelRPMLimit) {
		delete(result, "model_rpm_limit")
	}
	if knownMap(prior.ModelTPMLimit) {
		delete(result, "model_tpm_limit")
	}
	result, _, err = applySemanticDictionary(ctx, result, map[string]interface{}{}, priorProvenance.TerraformOwned)
	if err != nil {
		return nil, err
	}
	legacy, err := organizationLegacyMetadataObject(ctx, plan.Metadata)
	if err != nil {
		return nil, err
	}
	for name, value := range legacy {
		result[name] = value
	}
	if knownMap(plan.ModelRPMLimit) {
		values, err := int64RequestMap(plan.ModelRPMLimit, "model_rpm_limit")
		if err != nil {
			return nil, err
		}
		native := make(map[string]interface{}, len(values))
		for name, value := range values {
			native[name] = json.Number(strconv.FormatInt(value, 10))
		}
		result["model_rpm_limit"] = native
	}
	if knownMap(plan.ModelTPMLimit) {
		values, err := int64RequestMap(plan.ModelTPMLimit, "model_tpm_limit")
		if err != nil {
			return nil, err
		}
		native := make(map[string]interface{}, len(values))
		for name, value := range values {
			native[name] = json.Number(strconv.FormatInt(value, 10))
		}
		result["model_tpm_limit"] = native
	}
	if prepared.provenance.Configured {
		result, err = overlaySemanticDictionaryObject(ctx, result, prepared.object)
		if err != nil {
			return nil, err
		}
	}
	if err := validateSemanticDictionaryValue(ctx, result); err != nil {
		return nil, err
	}
	if err := validateModelSemanticDictionaryNumbers(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func organizationLegacyMetadataObject(ctx context.Context, value types.Map) (map[string]interface{}, error) {
	if value.IsNull() || value.IsUnknown() {
		return map[string]interface{}{}, nil
	}
	var stringsMap map[string]string
	if diagnostics := value.ElementsAs(ctx, &stringsMap, false); diagnostics.HasError() {
		return nil, errSemanticDictionaryTraversal
	}
	result := convertMetadataToNative(stringsMap)
	delete(result, "model_rpm_limit")
	delete(result, "model_tpm_limit")
	if err := validateSemanticDictionaryValue(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func projectOrganizationSemanticMetadata(ctx context.Context, current types.String, metadata map[string]interface{}, ownership organizationSemanticOwnership) (types.String, error) {
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
	if ownership.confirmCurrentValue {
		if !knownString(current) {
			return types.StringNull(), errSemanticDictionaryTraversal
		}
		desired, parseErr := parseSemanticDictionary(ctx, current.ValueString())
		equal, compareErr := semanticDictionaryValuesEqual(ctx, desired, projected)
		if parseErr != nil || compareErr != nil || !equal {
			return types.StringNull(), errSemanticDictionaryTraversal
		}
	}
	return reconcileSemanticDictionaryString(ctx, current, projected)
}

func projectOrganizationLegacyMetadata(ctx context.Context, current types.Map, metadata map[string]interface{}, ownership organizationSemanticOwnership) (types.Map, error) {
	ownedTop := map[string]bool{}
	if ownership.provenance.Configured {
		var err error
		ownedTop, err = semanticDictionaryTopLevelOwnedKeys(ctx, ownership.provenance)
		if err != nil {
			return types.MapNull(types.StringType), err
		}
	}
	filtered := make(map[string]interface{}, len(metadata))
	for name, value := range metadata {
		if ownedTop[name] || name == "model_rpm_limit" || name == "model_tpm_limit" {
			continue
		}
		filtered[name] = value
	}
	if ownership.acceptedCreate {
		filtered = map[string]interface{}{}
	} else if ownership.provenance.Configured {
		// A configured semantic sibling must not silently adopt unowned API
		// metadata into the compatibility map. Retain only already-managed
		// legacy keys; all other API siblings remain unmanaged.
		managed := map[string]interface{}{}
		if !current.IsNull() && !current.IsUnknown() {
			for name := range current.Elements() {
				if value, present := filtered[name]; present {
					managed[name] = value
				}
			}
		}
		filtered = managed
	}
	heterogeneous := false
	for _, value := range filtered {
		if _, ok := value.(string); !ok {
			heterogeneous = true
			break
		}
	}
	if heterogeneous && !ownership.provenance.Configured {
		managed := map[string]interface{}{}
		if !current.IsNull() && !current.IsUnknown() {
			for name := range current.Elements() {
				if value, present := filtered[name]; present {
					managed[name] = value
				}
			}
		}
		filtered = managed
	}
	if len(filtered) == 0 {
		if current.IsUnknown() {
			return types.MapNull(types.StringType), nil
		}
		if current.IsNull() {
			return current, nil
		}
		empty, diagnostics := checkedStringMapValue(ctx, map[string]attr.Value{}, path.Root("metadata"), false)
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return types.MapNull(types.StringType), err
		}
		return empty, nil
	}
	values := make(map[string]attr.Value, len(filtered))
	for name, value := range filtered {
		values[name] = types.StringValue(metadataValueToString(value))
	}
	result, diagnostics := types.MapValue(types.StringType, values)
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), errSemanticDictionaryTraversal
	}
	return result, nil
}

func partialOrganizationSemanticRecoveryState(data OrganizationResourceModel, identity string) OrganizationResourceModel {
	return OrganizationResourceModel{
		ID: types.StringValue(identity), OrganizationID: types.StringValue(identity),
		OrganizationAlias: types.StringNull(), Models: types.ListNull(types.StringType), BudgetID: types.StringNull(),
		MaxBudget: types.Float64Null(), SoftBudget: types.Float64Null(), TPMLimit: types.Int64Null(), RPMLimit: types.Int64Null(), MaxParallelRequests: types.Int64Null(),
		ModelRPMLimit: types.MapNull(types.Int64Type), ModelTPMLimit: types.MapNull(types.Int64Type), BudgetDuration: types.StringNull(),
		Metadata: types.MapNull(types.StringType), MetadataJSON: types.StringNull(), Blocked: types.BoolNull(), Tags: types.ListNull(types.StringType), CreatedAt: types.StringNull(),
	}
}

func validateOrganizationCreateResponseIdentity(result map[string]interface{}, identity string) error {
	object, err := unwrapObjectEnvelope(result, "organization_info", "data")
	if err != nil {
		return errSemanticDictionaryTraversal
	}
	returned, ok := object["organization_id"].(string)
	if !ok || returned == "" || returned != identity {
		return errSemanticDictionaryTraversal
	}
	return nil
}

func marshalOrganizationUpgrade(raw []byte) ([]byte, error) {
	var prior map[string]json.RawMessage
	if err := json.Unmarshal(raw, &prior); err != nil {
		return nil, err
	}
	prior["metadata_json"] = json.RawMessage("null")
	return json.Marshal(prior)
}
