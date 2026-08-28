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
	projectMetadataJSONProvenancePrivateKey = "project_metadata_json_provenance_v1"
	projectAcceptedCreateRecoveryPrivateKey = "project_semantic_create_accepted_v1"
	projectPendingUpdatePrivateKey          = "project_semantic_update_pending_v1"
	projectPendingBudgetPrivateKey          = "project_budget_update_pending_v1"
)

var projectMetadataJSONReservedKeys = []string{"tags", "model_rpm_limit", "model_tpm_limit"}

type projectPendingBudgetFields map[string]bool

type projectPendingBudgetWire struct {
	Version int      `json:"version"`
	Fields  []string `json:"fields"`
}

var projectPendingBudgetAllowedFields = map[string]bool{
	"max_budget": true, "soft_budget": true, "budget_duration": true,
	"tpm_limit": true, "rpm_limit": true, "max_parallel_requests": true,
	"model_max_budget": true,
}

func projectPendingBudgetFromPatch(patch map[string]interface{}) projectPendingBudgetFields {
	fields := projectPendingBudgetFields{}
	for name := range patch {
		if name == "budget_reset_at" {
			name = "budget_duration"
		}
		if projectPendingBudgetAllowedFields[name] {
			fields[name] = true
		}
	}
	return fields
}

func encodeProjectPendingBudget(ctx context.Context, fields projectPendingBudgetFields) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, nil
	}
	names := make([]string, 0, len(fields))
	for name, present := range fields {
		if !present || !projectPendingBudgetAllowedFields[name] {
			return nil, errSemanticDictionaryPrivate
		}
		names = append(names, name)
	}
	sort.Strings(names)
	encoded, err := json.Marshal(projectPendingBudgetWire{Version: 1, Fields: names})
	if err != nil || len(encoded) > jsonDecodeMaxInputBytes {
		return nil, errSemanticDictionaryPrivate
	}
	return encoded, nil
}

func decodeProjectPendingBudget(ctx context.Context, raw []byte) (projectPendingBudgetFields, error) {
	if len(raw) == 0 {
		return projectPendingBudgetFields{}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var wire projectPendingBudgetWire
	if len(raw) > jsonDecodeMaxInputBytes || decodeJSONUseNumber(raw, &wire) != nil || wire.Version != 1 || len(wire.Fields) == 0 {
		return nil, errSemanticDictionaryPrivate
	}
	fields := projectPendingBudgetFields{}
	for _, name := range wire.Fields {
		if !projectPendingBudgetAllowedFields[name] || fields[name] {
			return nil, errSemanticDictionaryPrivate
		}
		fields[name] = true
	}
	canonical, err := encodeProjectPendingBudget(ctx, fields)
	if err != nil || !bytes.Equal(canonical, raw) {
		return nil, errSemanticDictionaryPrivate
	}
	return fields, nil
}

type projectSemanticPrepared struct {
	object     map[string]interface{}
	provenance semanticDictionaryProvenance
}

type projectSemanticOwnership struct {
	provenance          semanticDictionaryProvenance
	removals            semanticDictionaryPathSet
	transitionRemovals  semanticDictionaryPathSet
	pending             keySemanticPendingTransition
	reconcile           *keySemanticPendingReconcile
	acceptedCreate      bool
	pendingBudget       projectPendingBudgetFields
	fresh               bool
	confirmCurrentValue bool
}

func projectUnconfiguredSemanticProvenance() semanticDictionaryProvenance {
	value := emptySemanticDictionaryProvenance()
	value.Initialized = true
	return value
}

func prepareProjectSemanticDictionary(ctx context.Context, value types.String, legacy types.Map) (projectSemanticPrepared, error) {
	provenance := projectUnconfiguredSemanticProvenance()
	if value.IsNull() {
		return projectSemanticPrepared{provenance: provenance}, nil
	}
	if value.IsUnknown() {
		return projectSemanticPrepared{}, errors.New("semantic project dictionary configuration is unknown")
	}
	object, err := parseSemanticDictionary(ctx, value.ValueString())
	if err != nil {
		return projectSemanticPrepared{}, err
	}
	if err := validateModelSemanticDictionaryNumbers(ctx, object); err != nil {
		return projectSemanticPrepared{}, err
	}
	if err := semanticDictionaryTopLevelOverlap(ctx, object, configuredAdditionalParamKeys(legacy), projectMetadataJSONReservedKeys); err != nil {
		return projectSemanticPrepared{}, err
	}
	paths, err := semanticDictionaryLeafPaths(ctx, object)
	if err != nil {
		return projectSemanticPrepared{}, err
	}
	provenance.Configured = true
	provenance.TerraformOwned = paths
	return projectSemanticPrepared{object: object, provenance: provenance}, nil
}

func decodeProjectSemanticProvenance(ctx context.Context, raw []byte, state types.String) (semanticDictionaryProvenance, error) {
	if len(raw) == 0 {
		if knownString(state) {
			return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
		}
		return projectUnconfiguredSemanticProvenance(), nil
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

func encodeProjectSemanticProvenance(ctx context.Context, provenance semanticDictionaryProvenance) ([]byte, error) {
	return encodeSemanticDictionaryProvenance(ctx, provenance)
}

func projectSemanticNeedsChange(ctx context.Context, configured, prior types.String, provenance semanticDictionaryProvenance) (bool, error) {
	return keySemanticDictionaryNeedsChange(ctx, configured, prior, provenance)
}

func (p projectSemanticPrepared) updateOwnership(ctx context.Context, prior semanticDictionaryProvenance) (projectSemanticOwnership, error) {
	removed, err := keySemanticDictionaryRemovedPaths(ctx, prior.TerraformOwned, p.provenance.TerraformOwned)
	if err != nil {
		return projectSemanticOwnership{}, err
	}
	projectionRemovals, err := keySemanticProjectionRemovals(ctx, p.provenance.TerraformOwned, removed)
	if err != nil {
		return projectSemanticOwnership{}, err
	}
	return projectSemanticOwnership{
		provenance: p.provenance, removals: projectionRemovals, transitionRemovals: removed,
		fresh: true, confirmCurrentValue: true,
	}, nil
}

func pendingProjectSemanticTransition(ownership projectSemanticOwnership) keySemanticPendingTransition {
	if len(ownership.transitionRemovals) == 0 {
		return keySemanticPendingTransition{}
	}
	return keySemanticPendingTransition{Metadata: keySemanticPendingRoot{
		Active: true, Configured: ownership.provenance.Configured,
		TerraformOwned: ownership.provenance.TerraformOwned, Removals: ownership.transitionRemovals,
	}}
}

func resolveProjectSemanticPending(ctx context.Context, metadata map[string]interface{}, ownership projectSemanticOwnership) (projectSemanticOwnership, keySemanticPendingReconcile, error) {
	keyOwnership := keySemanticReadOwnership{
		metadata: ownership.provenance, config: projectUnconfiguredSemanticProvenance(), permissions: projectUnconfiguredSemanticProvenance(), pending: ownership.pending,
	}
	effective, reconcile, err := resolveKeySemanticPendingTransition(ctx, map[string]interface{}{"metadata": metadata}, keyOwnership)
	if err != nil {
		return projectSemanticOwnership{}, keySemanticPendingReconcile{}, err
	}
	ownership.provenance = effective.metadata
	ownership.removals = effective.metadataRemovals
	ownership.pending = keySemanticPendingTransition{}
	ownership.confirmCurrentValue = false
	return ownership, reconcile, nil
}

func projectMetadataObject(ctx context.Context, object map[string]interface{}) (map[string]interface{}, error) {
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

func overlayProjectCreateSemantic(ctx context.Context, request map[string]interface{}, prepared projectSemanticPrepared) error {
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
	if values, ok := base["tags"].([]string); ok {
		native := make([]interface{}, len(values))
		for index, value := range values {
			native[index] = value
		}
		base["tags"] = native
	}
	for _, root := range []string{"model_rpm_limit", "model_tpm_limit"} {
		if values, ok := base[root].(map[string]int64); ok {
			native := make(map[string]interface{}, len(values))
			for name, value := range values {
				native[name] = json.Number(strconv.FormatInt(value, 10))
			}
			base[root] = native
		}
	}
	result, err := overlaySemanticDictionaryObject(ctx, base, prepared.object)
	if err != nil || validateSemanticDictionaryValue(ctx, result) != nil || validateModelSemanticDictionaryNumbers(ctx, result) != nil {
		return errSemanticDictionaryTraversal
	}
	request["metadata"] = result
	return nil
}

func composeProjectMetadataReplacement(ctx context.Context, remote map[string]interface{}, plan, prior ProjectResourceModel, priorProvenance semanticDictionaryProvenance, prepared projectSemanticPrepared) (map[string]interface{}, error) {
	result, err := cloneSemanticDictionary(ctx, remote)
	if err != nil {
		return nil, err
	}
	if knownMap(prior.Metadata) {
		for name := range prior.Metadata.Elements() {
			delete(result, name)
		}
	}
	if knownList(prior.Tags) {
		delete(result, "tags")
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
	legacy, err := projectLegacyMetadataObject(ctx, plan.Metadata)
	if err != nil {
		return nil, err
	}
	for name, value := range legacy {
		result[name] = value
	}
	if knownList(plan.Tags) {
		tags, err := stringListRequest(ctx, plan.Tags, "tags")
		if err != nil {
			return nil, err
		}
		native := make([]interface{}, len(tags))
		for index, value := range tags {
			native[index] = value
		}
		result["tags"] = native
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
			return nil, err
		}
		native := make(map[string]interface{}, len(values))
		for name, value := range values {
			native[name] = json.Number(strconv.FormatInt(value, 10))
		}
		result[entry.name] = native
	}
	if prepared.provenance.Configured {
		result, err = overlaySemanticDictionaryObject(ctx, result, prepared.object)
		if err != nil {
			return nil, err
		}
	}
	if validateSemanticDictionaryValue(ctx, result) != nil || validateModelSemanticDictionaryNumbers(ctx, result) != nil {
		return nil, errSemanticDictionaryTraversal
	}
	return result, nil
}

func projectLegacyMetadataObject(ctx context.Context, value types.Map) (map[string]interface{}, error) {
	if value.IsNull() || value.IsUnknown() {
		return map[string]interface{}{}, nil
	}
	var values map[string]string
	if diagnostics := value.ElementsAs(ctx, &values, false); diagnostics.HasError() {
		return nil, errSemanticDictionaryTraversal
	}
	result := convertMetadataToNative(values)
	for _, root := range projectMetadataJSONReservedKeys {
		delete(result, root)
	}
	if validateSemanticDictionaryValue(ctx, result) != nil {
		return nil, errSemanticDictionaryTraversal
	}
	return result, nil
}

func projectProjectSemanticMetadata(ctx context.Context, current types.String, metadata map[string]interface{}, ownership projectSemanticOwnership) (types.String, error) {
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

func projectProjectLegacyMetadata(ctx context.Context, current types.Map, metadata map[string]interface{}, ownership projectSemanticOwnership) (types.Map, error) {
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
		if ownedTop[name] || name == "tags" || name == "model_rpm_limit" || name == "model_tpm_limit" {
			continue
		}
		filtered[name] = value
	}
	if ownership.acceptedCreate {
		filtered = map[string]interface{}{}
	} else if ownership.provenance.Configured {
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
	heterogeneous := false
	for _, value := range filtered {
		if _, ok := value.(string); !ok {
			heterogeneous = true
			break
		}
	}
	if heterogeneous && !ownership.provenance.Configured {
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
	// Legacy project metadata historically round-trips strings and JSON-encoded
	// objects/arrays. Scalar-looking strings are never parsed on write, so a
	// remote boolean, number, or null at an already-managed legacy key cannot be
	// represented without changing its identity. Fail closed instead of storing
	// a lossy string that a later complete-root update would replay.
	for _, value := range filtered {
		switch value.(type) {
		case string, map[string]interface{}, []interface{}:
		default:
			return types.MapNull(types.StringType), errSemanticDictionaryTraversal
		}
	}
	if len(filtered) == 0 {
		if current.IsUnknown() {
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
		values[name] = types.StringValue(metadataValueToString(value))
	}
	result, diagnostics := types.MapValue(types.StringType, values)
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), errSemanticDictionaryTraversal
	}
	return result, nil
}

func partialProjectSemanticRecoveryState(data ProjectResourceModel, identity string) ProjectResourceModel {
	budgetID := types.StringNull()
	if knownString(data.BudgetID) {
		// A configured shared-budget identity is required to prove the exact
		// association during recovery. Generated/unknown budget identities remain
		// null so an omitted budget is never adopted implicitly.
		budgetID = data.BudgetID
	}
	return ProjectResourceModel{
		ID: types.StringValue(identity), ProjectAlias: types.StringNull(), Description: types.StringNull(), TeamID: data.TeamID,
		Models: types.ListNull(types.StringType), Metadata: types.MapNull(types.StringType), MetadataJSON: types.StringNull(), Tags: types.ListNull(types.StringType),
		MaxBudget: types.Float64Null(), SoftBudget: types.Float64Null(), BudgetDuration: types.StringNull(), BudgetID: budgetID,
		TPMLimit: types.Int64Null(), RPMLimit: types.Int64Null(), MaxParallelRequests: types.Int64Null(),
		ModelMaxBudget: types.MapNull(types.Float64Type), ModelRPMLimit: types.MapNull(types.Int64Type), ModelTPMLimit: types.MapNull(types.Int64Type),
		Blocked: types.BoolNull(), CreatedAt: types.StringNull(), UpdatedAt: types.StringNull(), CreatedBy: types.StringNull(), UpdatedBy: types.StringNull(),
	}
}

func validateProjectCreateResponseIdentity(result map[string]interface{}, identity string) error {
	object, err := unwrapObjectEnvelope(result, "project_info", "data")
	if err != nil {
		return errSemanticDictionaryTraversal
	}
	returned, ok := object["project_id"].(string)
	if !ok || returned == "" || returned != identity {
		return errSemanticDictionaryTraversal
	}
	return nil
}

func marshalProjectUpgrade(raw []byte) ([]byte, error) {
	var prior map[string]json.RawMessage
	if err := json.Unmarshal(raw, &prior); err != nil {
		return nil, err
	}
	prior["metadata_json"] = json.RawMessage("null")
	return json.Marshal(prior)
}

func knownList(value types.List) bool { return !value.IsNull() && !value.IsUnknown() }
