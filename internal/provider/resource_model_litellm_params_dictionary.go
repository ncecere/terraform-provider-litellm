package provider

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	modelAdditionalLiteLLMParamsJSONProvenancePrivateKey = "model_additional_litellm_params_json_provenance_v1"
	modelInfoMaskMaxDepth                                = 10
)

var (
	errModelLiteLLMParamsJSONProjection          = errors.New("semantic LiteLLM parameters projection failed")
	modelAdditionalLiteLLMParamsJSONReservedKeys = []string{
		"model",
		"provider",
		"custom_llm_provider",
		"tpm",
		"rpm",
		"api_key",
		"api_base",
		"api_version",
		"reasoning_effort",
		"thinking",
		"merge_reasoning_content_in_choices",
		"aws_access_key_id",
		"aws_secret_access_key",
		"aws_region_name",
		"aws_session_name",
		"aws_role_name",
		"vertex_project",
		"vertex_location",
		"vertex_credentials",
		"litellm_credential_name",
		"input_cost_per_token",
		"output_cost_per_token",
		"input_cost_per_pixel",
		"output_cost_per_pixel",
		"input_cost_per_second",
		"output_cost_per_second",
		// /model/info always removes these credential fields, so their
		// persistence can never be confirmed through this resource lifecycle.
		"client_secret",
		"vertex_ai_credentials",
		// Reserved for issue #223's dedicated model-budget lifecycle.
		"max_budget",
		"budget_duration",
	}
	modelInfoMaskExcludedKeys = map[string]bool{
		"litellm_credential_name":   true,
		"default_api_key_tpm_limit": true,
		"default_api_key_rpm_limit": true,
	}
	modelInfoMaskSensitiveSegments = map[string]bool{
		"password": true, "secret": true, "key": true, "token": true,
		"auth": true, "authorization": true, "credential": true,
		"credentials": true, "access": true, "private": true,
		"certificate": true, "fingerprint": true, "tenancy": true,
	}
)

func modelAdditionalLiteLLMParamsJSONConfiguration(
	ctx context.Context,
	value types.String,
	legacy types.Map,
) (map[string]interface{}, semanticDictionaryProvenance, error) {
	provenance := modelUnconfiguredSemanticDictionaryProvenance()
	if value.IsNull() {
		return nil, provenance, nil
	}
	if value.IsUnknown() {
		return nil, semanticDictionaryProvenance{}, errors.New("semantic LiteLLM parameters configuration is unknown")
	}

	object, err := parseSemanticDictionary(ctx, value.ValueString())
	if err != nil {
		return nil, semanticDictionaryProvenance{}, err
	}
	if err := validateModelSemanticDictionaryPersistence(ctx, object); err != nil {
		return nil, semanticDictionaryProvenance{}, err
	}
	if err := validateModelAdditionalLiteLLMParamsJSONMaskPersistence(ctx, object); err != nil {
		return nil, semanticDictionaryProvenance{}, err
	}
	legacyKeys := configuredAdditionalParamKeys(legacy)
	if err := semanticDictionaryTopLevelOverlap(ctx, object, legacyKeys, modelAdditionalLiteLLMParamsJSONReservedKeys); err != nil {
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

func decodeModelAdditionalLiteLLMParamsJSONProvenance(
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

func encodeModelAdditionalLiteLLMParamsJSONProvenance(
	ctx context.Context,
	value semanticDictionaryProvenance,
) ([]byte, error) {
	return encodeSemanticDictionaryProvenance(ctx, value)
}

func modelAdditionalLiteLLMParamsJSONNeedsReplacement(
	ctx context.Context,
	config, state types.String,
	provenance semanticDictionaryProvenance,
) (bool, error) {
	return modelAdditionalModelInfoJSONNeedsReplacement(ctx, config, state, provenance)
}

// validateModelAdditionalLiteLLMParamsJSONMaskPersistence rejects values whose
// JSON type /model/info cannot report authoritatively after applying the pinned
// SensitiveDataMasker. The masker stringifies every null and direct non-string
// scalar beneath a sensitive map key. Its maximum recursion depth is runtime
// configurable, so validation remains conservative at every depth.
func validateModelAdditionalLiteLLMParamsJSONMaskPersistence(ctx context.Context, value interface{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch value := value.(type) {
	case nil:
		return errSemanticDictionaryTraversal
	case map[string]interface{}:
		for key, child := range value {
			if err := ctx.Err(); err != nil {
				return err
			}
			if text, isString := child.(string); isString {
				// The masker also emits these exact strings for values whose
				// original JSON type is no longer observable.
				if text == "None" || (text == "" && modelInfoMaskKeySensitive(key)) {
					return errSemanticDictionaryTraversal
				}
			}
			if modelInfoMaskKeySensitive(key) {
				switch child.(type) {
				case string, map[string]interface{}, []interface{}:
				default:
					return errSemanticDictionaryTraversal
				}
			}
			if err := validateModelAdditionalLiteLLMParamsJSONMaskPersistence(ctx, child); err != nil {
				return err
			}
		}
	case []interface{}:
		for _, child := range value {
			if text, isString := child.(string); isString && text == "None" {
				return errSemanticDictionaryTraversal
			}
			if err := validateModelAdditionalLiteLLMParamsJSONMaskPersistence(ctx, child); err != nil {
				return err
			}
		}
	}
	return nil
}

func modelInfoMaskKeySensitive(key string) bool {
	if modelInfoMaskExcludedKeys[key] {
		return false
	}
	segments := strings.FieldsFunc(strings.ToLower(key), func(character rune) bool {
		return character == '_' || character == '-'
	})
	for _, segment := range segments {
		if segment == "cost" {
			return false
		}
	}
	for _, segment := range segments {
		if modelInfoMaskSensitiveSegments[segment] {
			return true
		}
	}
	return false
}

func modelInfoMaskLike(value string) bool {
	characters := []rune(value)
	if len(characters) == 0 {
		return false
	}
	start, end := 0, len(characters)
	if len(characters) > 8 {
		start, end = 4, len(characters)-4
	}
	if start == end {
		return false
	}
	for _, character := range characters[start:end] {
		if character != '*' {
			return false
		}
	}
	return true
}

// modelInfoLiteLLMParamsMaskPredicate mirrors SensitiveDataMasker's ownership
// rules for /model/info. A sensitive map key controls its direct scalar or a
// list value (including nested lists). Entering a map resets that inheritance,
// so the map's own keys control its children. Traversing the observed value is
// essential because a numeric segment can be either a list index or an object
// key and those cases have different ownership.
func modelInfoLiteLLMParamsMaskPredicate(observed map[string]interface{}) semanticDictionaryMaskPredicate {
	return func(path []string, value string) bool {
		if !modelInfoMaskLike(value) || len(path) == 0 {
			return false
		}
		var current interface{} = observed
		listKeySensitive := false
		for index, member := range path {
			// Pinned LiteLLM v1.98 stops SensitiveDataMasker traversal when
			// the current container reaches its default maximum depth.
			if index >= modelInfoMaskMaxDepth {
				return false
			}
			switch container := current.(type) {
			case map[string]interface{}:
				child, present := container[member]
				if !present {
					return false
				}
				keySensitive := modelInfoMaskKeySensitive(member)
				if index == len(path)-1 {
					_, isString := child.(string)
					return isString && keySensitive
				}
				current = child
				if _, isList := child.([]interface{}); isList {
					listKeySensitive = keySensitive
				} else {
					listKeySensitive = false
				}
			case []interface{}:
				position, err := strconv.Atoi(member)
				if err != nil || position < 0 || position >= len(container) {
					return false
				}
				child := container[position]
				if index == len(path)-1 {
					_, isString := child.(string)
					return isString && listKeySensitive
				}
				current = child
				if _, isMap := child.(map[string]interface{}); isMap {
					listKeySensitive = false
				}
			default:
				return false
			}
		}
		return false
	}
}

func modelInfoDirectSensitiveMapString(observed map[string]interface{}, path []string) bool {
	if len(path) == 0 {
		return false
	}
	var current interface{} = observed
	for index, member := range path {
		switch container := current.(type) {
		case map[string]interface{}:
			child, present := container[member]
			if !present {
				return false
			}
			if index == len(path)-1 {
				_, isString := child.(string)
				return isString && modelInfoMaskKeySensitive(member)
			}
			current = child
		case []interface{}:
			position, err := strconv.Atoi(member)
			if err != nil || position < 0 || position >= len(container) || index == len(path)-1 {
				return false
			}
			current = container[position]
		default:
			return false
		}
	}
	return false
}

func modelInfoIrreversiblyStringifiedValue(observed map[string]interface{}, path []string, value string) bool {
	return value == "None" || (value == "" && modelInfoDirectSensitiveMapString(observed, path))
}

func semanticDictionaryPathCovered(paths semanticDictionaryPathSet, members []string) bool {
	for length := len(members); length > 0; length-- {
		pointer, err := encodeSemanticDictionaryPointer(members[:length])
		if err == nil && paths[pointer] {
			return true
		}
	}
	return false
}

func hydrateModelAdditionalLiteLLMParamsJSONPatch(
	ctx context.Context,
	result map[string]interface{},
	configured map[string]interface{},
	provenance semanticDictionaryProvenance,
) (map[string]interface{}, error) {
	litellmParams, ok := result["litellm_params"].(map[string]interface{})
	if !ok || litellmParams == nil {
		return nil, errSemanticDictionaryTraversal
	}
	base := map[string]interface{}{}
	for name, configuredValue := range configured {
		configuredObject, configuredIsObject := configuredValue.(map[string]interface{})
		if !configuredIsObject || len(configuredObject) == 0 {
			continue
		}
		remoteValue, present := litellmParams[name]
		if !present {
			continue
		}
		remoteObject, ok := remoteValue.(map[string]interface{})
		if !ok {
			return nil, errSemanticDictionaryTraversal
		}
		base[name] = remoteObject
	}

	maskPredicate := modelInfoLiteLLMParamsMaskPredicate(litellmParams)
	unsafeOutsideOwned, err := semanticDictionaryContainsMaskedValue(ctx, base, nil, func(path []string, value string) bool {
		if semanticDictionaryPathCovered(provenance.TerraformOwned, path) {
			return false
		}
		return maskPredicate(path, value) || modelInfoIrreversiblyStringifiedValue(litellmParams, path, value)
	})
	if err != nil {
		return nil, err
	}
	if unsafeOutsideOwned {
		return nil, errSemanticDictionaryMasked
	}
	return overlaySemanticDictionaryObject(ctx, base, configured)
}

func reconcileModelAdditionalLiteLLMParamsJSON(
	ctx context.Context,
	current types.String,
	observed map[string]interface{},
	provenance semanticDictionaryProvenance,
) (types.String, error) {
	if !provenance.Configured {
		return types.StringNull(), nil
	}
	if current.IsNull() || current.IsUnknown() || observed == nil {
		return types.StringNull(), errSemanticDictionaryTraversal
	}
	projected, err := projectModelAdditionalModelInfoJSON(ctx, observed, provenance)
	if err != nil {
		return types.StringNull(), err
	}
	irreversiblyStringified, err := semanticDictionaryContainsMaskedValue(ctx, projected, nil, func(path []string, value string) bool {
		return modelInfoIrreversiblyStringifiedValue(observed, path, value)
	})
	if err != nil {
		return types.StringNull(), err
	}
	if irreversiblyStringified {
		return types.StringNull(), errSemanticDictionaryMasked
	}
	prior, err := parseSemanticDictionary(ctx, current.ValueString())
	if err != nil {
		return types.StringNull(), err
	}
	sameShape, err := semanticDictionarySameShape(ctx, prior, projected)
	if err != nil {
		return types.StringNull(), err
	}
	if !sameShape {
		return types.StringNull(), errSemanticDictionaryTraversal
	}
	restored, err := restoreSemanticDictionaryMaskedValues(
		ctx,
		prior,
		projected,
		true,
		modelInfoLiteLLMParamsMaskPredicate(observed),
	)
	if err != nil {
		return types.StringNull(), err
	}
	return reconcileSemanticDictionaryString(ctx, current, restored)
}

func excludeModelAdditionalLiteLLMParamsJSONTopLevelKeys(ctx context.Context, value types.Map, object map[string]interface{}) (types.Map, error) {
	if err := ctx.Err(); err != nil {
		return types.MapNull(types.StringType), err
	}
	if value.IsNull() || value.IsUnknown() || len(object) == 0 {
		return value, nil
	}
	elements := make(map[string]attr.Value, len(value.Elements()))
	for name, element := range value.Elements() {
		if err := ctx.Err(); err != nil {
			return types.MapNull(types.StringType), err
		}
		if _, owned := object[name]; !owned {
			elements[name] = element
		}
	}
	result, diagnostics := types.MapValue(types.StringType, elements)
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), errSemanticDictionaryValue
	}
	return result, nil
}

func semanticDictionaryTopLevelOwnedKeys(ctx context.Context, provenance semanticDictionaryProvenance) (map[string]bool, error) {
	result := map[string]bool{}
	for pointer := range provenance.TerraformOwned {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		members, err := decodeSemanticDictionaryPointer(pointer)
		if err != nil || len(members) == 0 {
			return nil, errSemanticDictionaryPrivate
		}
		result[members[0]] = true
	}
	return result, nil
}
