package provider

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func nullableVectorStoreString(raw interface{}) types.String {
	if text, ok := raw.(string); ok && text != "" {
		return types.StringValue(text)
	}
	return types.StringNull()
}

func unwrapVectorStoreResponse(result map[string]interface{}, expectedID string) (map[string]interface{}, error) {
	raw, present := result["vector_store"]
	if !present || raw == nil {
		return nil, fmt.Errorf("vector store response is missing the required vector_store object")
	}
	store, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("vector store response field %q must be an object", "vector_store")
	}
	id, ok := store["vector_store_id"].(string)
	if !ok || id == "" {
		return nil, fmt.Errorf("vector store response is missing a valid vector_store_id")
	}
	if expectedID != "" && id != expectedID {
		return nil, fmt.Errorf("vector store response identity did not match the requested ID")
	}
	return store, nil
}

func vectorStoreObject(raw interface{}, field string) (map[string]interface{}, error) {
	if raw == nil {
		return map[string]interface{}{}, nil
	}
	if object, ok := raw.(map[string]interface{}); ok {
		return object, nil
	}
	if text, ok := raw.(string); ok {
		var object map[string]interface{}
		if err := decodeJSONUseNumber([]byte(text), &object); err != nil || object == nil {
			return nil, fmt.Errorf("vector store response field %q must be an object", field)
		}
		return object, nil
	}
	return nil, fmt.Errorf("vector store response field %q must be an object", field)
}

func restoreVectorStoreMaskedLeaves(remote, prior interface{}, field string) (interface{}, error) {
	if text, ok := remote.(string); ok && isMaskedAPIString(text) {
		if prior == nil {
			return nil, fmt.Errorf("vector store response field %q contains a masked value without prior Terraform state", field)
		}
		return prior, nil
	}
	switch typed := remote.(type) {
	case map[string]interface{}:
		priorMap, ok := prior.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("vector store response field %q changed shape while masked", field)
		}
		result := make(map[string]interface{}, len(typed))
		for key, child := range typed {
			merged, err := restoreVectorStoreMaskedLeaves(child, priorMap[key], field+"."+key)
			if err != nil {
				return nil, err
			}
			result[key] = merged
		}
		return result, nil
	case []interface{}:
		priorList, ok := prior.([]interface{})
		if !ok || len(priorList) != len(typed) {
			return nil, fmt.Errorf("vector store response field %q changed shape while masked", field)
		}
		result := make([]interface{}, len(typed))
		for index, child := range typed {
			merged, err := restoreVectorStoreMaskedLeaves(child, priorList[index], fmt.Sprintf("%s[%d]", field, index))
			if err != nil {
				return nil, err
			}
			result[index] = merged
		}
		return result, nil
	default:
		return remote, nil
	}
}

func vectorStoreStringMap(raw interface{}, prior types.Map, filterToPrior, rejectUnownedMasks bool, field string) (types.Map, error) {
	object, err := vectorStoreObject(raw, field)
	if err != nil {
		return types.MapNull(types.StringType), err
	}
	priorValues := map[string]string{}
	if !prior.IsNull() && !prior.IsUnknown() {
		for key, value := range prior.Elements() {
			if text, ok := value.(types.String); ok && !text.IsNull() && !text.IsUnknown() {
				priorValues[key] = text.ValueString()
			}
		}
	}

	values := make(map[string]attr.Value)
	for key, rawValue := range object {
		priorValue, hasPrior := priorValues[key]
		if filterToPrior && !hasPrior {
			continue
		}
		masked := containsMaskedValue(rawValue)
		if masked {
			if !hasPrior {
				if rejectUnownedMasks {
					return types.MapNull(types.StringType), fmt.Errorf("vector store response field %q contains a masked value without prior Terraform state", field+"."+key)
				}
			} else if isJSONContainer(rawValue) {
				var priorJSON interface{}
				if err := decodeJSONUseNumber([]byte(priorValue), &priorJSON); err != nil {
					return types.MapNull(types.StringType), fmt.Errorf("vector store response field %q changed shape while masked", field+"."+key)
				}
				merged, err := restoreVectorStoreMaskedLeaves(rawValue, priorJSON, field+"."+key)
				if err != nil {
					return types.MapNull(types.StringType), err
				}
				encoded, _ := json.Marshal(merged)
				if jsonSemanticallyEqual(priorValue, string(encoded)) {
					values[key] = types.StringValue(priorValue)
				} else {
					values[key] = types.StringValue(string(encoded))
				}
				continue
			} else {
				values[key] = types.StringValue(priorValue)
				continue
			}
		}
		value, ok := stringifyAPIValue(rawValue)
		if !ok {
			return types.MapNull(types.StringType), fmt.Errorf("vector store response field %q has an unsupported value", field+"."+key)
		}
		if hasPrior && jsonSemanticallyEqual(priorValue, value.(types.String).ValueString()) {
			values[key] = types.StringValue(priorValue)
		} else {
			values[key] = value
		}
	}
	return types.MapValueMust(types.StringType, values), nil
}
