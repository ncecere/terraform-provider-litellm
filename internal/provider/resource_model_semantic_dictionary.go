package provider

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const modelAdditionalModelInfoJSONProvenancePrivateKey = "model_additional_model_info_json_provenance_v1"

var (
	errModelSemanticDictionaryProjection     = errors.New("semantic model information projection failed")
	modelAdditionalModelInfoJSONReservedKeys = append(
		append([]string{}, reservedAdditionalModelInfoKeys...),
		"input_cost_per_token",
		"output_cost_per_token",
	)
)

type modelSemanticDictionaryValidator struct{}

var _ validator.String = modelSemanticDictionaryValidator{}

func (modelSemanticDictionaryValidator) Description(context.Context) string {
	return "Value must be one non-null JSON object with unique members."
}

func (v modelSemanticDictionaryValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (modelSemanticDictionaryValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	object, err := parseSemanticDictionary(ctx, req.ConfigValue.ValueString())
	if err == nil {
		err = validateModelSemanticDictionaryPersistence(ctx, object)
	}
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid Semantic JSON Object",
			"The value must be one bounded, non-null JSON object with unique members.",
		)
	}
}

func modelUnconfiguredSemanticDictionaryProvenance() semanticDictionaryProvenance {
	value := emptySemanticDictionaryProvenance()
	value.Initialized = true
	return value
}

func modelAdditionalModelInfoJSONConfiguration(
	ctx context.Context,
	value types.String,
	legacy types.Map,
) (map[string]interface{}, semanticDictionaryProvenance, error) {
	provenance := modelUnconfiguredSemanticDictionaryProvenance()
	if value.IsNull() {
		return nil, provenance, nil
	}
	if value.IsUnknown() {
		return nil, semanticDictionaryProvenance{}, errors.New("semantic model information configuration is unknown")
	}

	object, err := parseSemanticDictionary(ctx, value.ValueString())
	if err != nil {
		return nil, semanticDictionaryProvenance{}, err
	}
	if err := validateModelSemanticDictionaryPersistence(ctx, object); err != nil {
		return nil, semanticDictionaryProvenance{}, err
	}
	legacyKeys := configuredAdditionalParamKeys(legacy)
	if err := semanticDictionaryTopLevelOverlap(ctx, object, legacyKeys, modelAdditionalModelInfoJSONReservedKeys); err != nil {
		return nil, semanticDictionaryProvenance{}, err
	}
	paths, err := semanticDictionaryLeafPaths(ctx, object)
	if err != nil {
		return nil, semanticDictionaryProvenance{}, err
	}
	provenance.Configured = true
	provenance.TerraformOwned = paths
	return object, provenance, nil
}

func decodeModelAdditionalModelInfoJSONProvenance(
	ctx context.Context,
	raw []byte,
	state types.String,
) (semanticDictionaryProvenance, error) {
	if len(raw) == 0 {
		if !state.IsNull() && !state.IsUnknown() {
			return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
		}
		return modelUnconfiguredSemanticDictionaryProvenance(), nil
	}
	value, err := decodeSemanticDictionaryProvenance(ctx, raw)
	if err != nil {
		return semanticDictionaryProvenance{}, err
	}
	if value.Configured != (!state.IsNull() && !state.IsUnknown()) || len(value.APIOwned) != 0 || len(value.PendingTerraformOwned) != 0 || len(value.PendingAPIOwned) != 0 || len(value.PendingRemovals) != 0 {
		return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
	}
	if value.Configured {
		object, err := parseSemanticDictionary(ctx, state.ValueString())
		if err != nil {
			return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
		}
		expected, err := semanticDictionaryLeafPaths(ctx, object)
		if err != nil || !modelSemanticDictionaryPathSetsEqual(expected, value.TerraformOwned) {
			return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
		}
	} else if len(value.TerraformOwned) != 0 {
		return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
	}
	return value, nil
}

func validateModelSemanticDictionaryPersistence(ctx context.Context, object map[string]interface{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, value := range object {
		// Pinned LiteLLM v1.98 serializes ModelInfo with exclude_none=True, so
		// arbitrary top-level null extras are not observable after persistence.
		// Nulls nested inside an object or array remain ordinary atomic values.
		if value == nil {
			return errSemanticDictionaryTraversal
		}
	}
	return validateModelSemanticDictionaryNumbers(ctx, object)
}

func validateModelSemanticDictionaryNumbers(ctx context.Context, value interface{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch value := value.(type) {
	case json.Number:
		raw := value.String()
		if !strings.ContainsAny(raw, ".eE") {
			return nil
		}
		parsed, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) {
			return errSemanticDictionaryValue
		}
		wire := json.Number(strconv.FormatFloat(parsed, 'g', -1, 64))
		if !exactJSONNumbersEqual(value, wire) {
			return errSemanticDictionaryValue
		}
	case map[string]interface{}:
		for _, child := range value {
			if err := validateModelSemanticDictionaryNumbers(ctx, child); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, child := range value {
			if err := validateModelSemanticDictionaryNumbers(ctx, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func modelSemanticDictionaryPathSetsEqual(left, right semanticDictionaryPathSet) bool {
	if len(left) != len(right) {
		return false
	}
	for pointer := range left {
		if !right[pointer] {
			return false
		}
	}
	return true
}

func encodeModelAdditionalModelInfoJSONProvenance(
	ctx context.Context,
	value semanticDictionaryProvenance,
) ([]byte, error) {
	return encodeSemanticDictionaryProvenance(ctx, value)
}

func modelAdditionalModelInfoJSONNeedsReplacement(
	ctx context.Context,
	config, state types.String,
	provenance semanticDictionaryProvenance,
) (bool, error) {
	if config.IsUnknown() {
		// An existing resource cannot prove semantic equality or safe takeover
		// until the value resolves. Conservatively require replacement; this is
		// harmless on create, where there is no prior instance to replace.
		return true, nil
	}
	if config.IsNull() {
		return provenance.Configured, nil
	}
	configured, err := parseSemanticDictionary(ctx, config.ValueString())
	if err != nil {
		return false, err
	}
	if !provenance.Configured || state.IsNull() || state.IsUnknown() {
		return true, nil
	}
	prior, err := parseSemanticDictionary(ctx, state.ValueString())
	if err != nil {
		return false, err
	}
	equal, err := semanticDictionaryValuesEqual(ctx, configured, prior)
	if err != nil {
		return false, err
	}
	return !equal, nil
}

func (r *ModelResource) hydrateModelAdditionalModelInfoJSONPatch(
	ctx context.Context,
	modelID string,
	configured map[string]interface{},
) (map[string]interface{}, error) {
	query := url.Values{"litellm_model_id": []string{modelID}}
	endpoint := endpointWithQuery("/model/info", query)
	var raw map[string]interface{}
	if err := r.client.doFreshRequestWithResponse(ctx, "GET", endpoint, nil, &raw); err != nil {
		return nil, err
	}
	result, err := exactModelInfoResult(raw)
	if err != nil {
		return nil, err
	}
	modelInfo, ok := result["model_info"].(map[string]interface{})
	if !ok || modelInfo == nil {
		return nil, errSemanticDictionaryTraversal
	}
	identity, present := modelInfo["id"]
	text, ok := identity.(string)
	if !present || !ok || text == "" || text != modelID {
		return nil, errSemanticDictionaryTraversal
	}

	base := map[string]interface{}{}
	for name, configuredValue := range configured {
		configuredObject, configuredIsObject := configuredValue.(map[string]interface{})
		if !configuredIsObject || len(configuredObject) == 0 {
			continue
		}
		remoteValue, present := modelInfo[name]
		if !present {
			continue
		}
		remoteObject, ok := remoteValue.(map[string]interface{})
		if !ok {
			return nil, errSemanticDictionaryTraversal
		}
		base[name] = remoteObject
	}
	return overlaySemanticDictionaryObject(ctx, base, configured)
}

func exactModelInfoResult(raw map[string]interface{}) (map[string]interface{}, error) {
	value, present := raw["data"]
	if !present {
		return nil, errSemanticDictionaryTraversal
	}
	switch value := value.(type) {
	case []interface{}:
		if len(value) != 1 {
			return nil, errSemanticDictionaryTraversal
		}
		result, ok := value[0].(map[string]interface{})
		if !ok || result == nil {
			return nil, errSemanticDictionaryTraversal
		}
		return result, nil
	case map[string]interface{}:
		if value == nil {
			return nil, errSemanticDictionaryTraversal
		}
		return value, nil
	default:
		return nil, errSemanticDictionaryTraversal
	}
}

func overlayModelAdditionalModelInfoJSON(
	ctx context.Context,
	modelInfo map[string]interface{},
	configured map[string]interface{},
) (map[string]interface{}, error) {
	if configured == nil {
		return cloneSemanticDictionary(ctx, modelInfo)
	}
	return overlaySemanticDictionaryObject(ctx, modelInfo, configured)
}

func projectModelAdditionalModelInfoJSON(
	ctx context.Context,
	observed map[string]interface{},
	provenance semanticDictionaryProvenance,
) (map[string]interface{}, error) {
	if !provenance.Configured {
		return nil, nil
	}
	if observed == nil {
		return nil, errSemanticDictionaryTraversal
	}
	if err := validateSemanticDictionaryValue(ctx, observed); err != nil {
		return nil, err
	}
	if err := validateSemanticDictionaryPathSet(ctx, provenance.TerraformOwned); err != nil {
		return nil, err
	}
	result := map[string]interface{}{}
	for _, pointer := range sortedSemanticDictionaryPaths(provenance.TerraformOwned) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		members, err := decodeSemanticDictionaryPointer(pointer)
		if err != nil {
			return nil, errSemanticDictionaryPrivate
		}
		value, err := modelSemanticDictionaryValueAt(observed, members)
		if err != nil {
			return nil, err
		}
		if err := setModelSemanticDictionaryValue(ctx, result, members, value); err != nil {
			return nil, err
		}
	}
	projectedPaths, err := semanticDictionaryLeafPaths(ctx, result)
	if err != nil || !modelSemanticDictionaryPathSetsEqual(projectedPaths, provenance.TerraformOwned) {
		return nil, errSemanticDictionaryTraversal
	}
	return result, nil
}

func modelSemanticDictionaryValueAt(object map[string]interface{}, members []string) (interface{}, error) {
	if len(members) == 0 {
		return nil, errSemanticDictionaryTraversal
	}
	current := object
	for index, member := range members {
		value, present := current[member]
		if !present {
			return nil, errSemanticDictionaryTraversal
		}
		if index == len(members)-1 {
			return value, nil
		}
		nested, ok := value.(map[string]interface{})
		if !ok {
			return nil, errSemanticDictionaryTraversal
		}
		current = nested
	}
	return nil, errSemanticDictionaryTraversal
}

func setModelSemanticDictionaryValue(
	ctx context.Context,
	object map[string]interface{},
	members []string,
	value interface{},
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(members) == 0 {
		return errSemanticDictionaryTraversal
	}
	current := object
	for _, member := range members[:len(members)-1] {
		nested, present := current[member]
		if !present {
			created := map[string]interface{}{}
			current[member] = created
			current = created
			continue
		}
		var ok bool
		current, ok = nested.(map[string]interface{})
		if !ok {
			return errSemanticDictionaryConflict
		}
	}
	cloned, err := cloneSemanticDictionaryValue(ctx, value)
	if err != nil {
		return err
	}
	current[members[len(members)-1]] = cloned
	return nil
}

func reconcileModelAdditionalModelInfoJSON(
	ctx context.Context,
	current types.String,
	observed map[string]interface{},
	provenance semanticDictionaryProvenance,
) (types.String, error) {
	if !provenance.Configured {
		return types.StringNull(), nil
	}
	projected, err := projectModelAdditionalModelInfoJSON(ctx, observed, provenance)
	if err != nil {
		return types.StringNull(), err
	}
	return reconcileSemanticDictionaryString(ctx, current, projected)
}
