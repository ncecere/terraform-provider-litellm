package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// convertMetadataToNative takes a Terraform metadata map (map[string]string)
// and converts values that contain JSON objects or arrays into native Go types
// so the API receives structured data rather than escaped JSON strings.
//
// For example, a Terraform config like:
//
//	metadata = {
//	  "logging" = jsonencode([{ "callback_name": "langsmith" }])
//	}
//
// will send the "logging" value as a native JSON array, not a string.
func convertMetadataToNative(metadata map[string]string) map[string]interface{} {
	result := make(map[string]interface{}, len(metadata))
	for k, v := range metadata {
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
			var parsed interface{}
			if err := json.Unmarshal([]byte(v), &parsed); err == nil {
				result[k] = parsed
				continue
			}
		}
		result[k] = v
	}
	return result
}

// metadataValueToString converts a metadata value from the API response back
// to a string for storage in Terraform state. String values are returned as-is;
// non-string values (arrays, objects, numbers, booleans) are JSON-encoded.
func metadataValueToString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case nil:
		return ""
	default:
		b, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprintf("%v", val)
		}
		return string(b)
	}
}

// metadataValueToStringPreservingMasked converts an API metadata value while
// retaining configured values only at leaves LiteLLM returned as encrypted or
// redacted. The API structure and every unmasked value remain authoritative.
func metadataValueToStringPreservingMasked(apiValue interface{}, configured string) string {
	if apiString, ok := apiValue.(string); ok {
		if isMaskedAPIString(apiString) {
			return configured
		}
		return apiString
	}

	var configuredValue interface{}
	if err := json.Unmarshal([]byte(configured), &configuredValue); err != nil {
		return metadataValueToString(apiValue)
	}
	return metadataValueToString(restoreMaskedMetadataLeaves(apiValue, configuredValue))
}

func restoreMaskedMetadataLeaves(apiValue, configuredValue interface{}) interface{} {
	switch apiValue := apiValue.(type) {
	case string:
		if isMaskedAPIString(apiValue) {
			if configuredString, ok := configuredValue.(string); ok {
				return configuredString
			}
		}
		return apiValue
	case map[string]interface{}:
		configuredObject, _ := configuredValue.(map[string]interface{})
		restored := make(map[string]interface{}, len(apiValue))
		for key, child := range apiValue {
			if configuredChild, exists := configuredObject[key]; exists {
				restored[key] = restoreMaskedMetadataLeaves(child, configuredChild)
			} else {
				restored[key] = child
			}
		}
		return restored
	case []interface{}:
		configuredArray, _ := configuredValue.([]interface{})
		restored := make([]interface{}, len(apiValue))
		for index, child := range apiValue {
			if index < len(configuredArray) {
				restored[index] = restoreMaskedMetadataLeaves(child, configuredArray[index])
			} else {
				restored[index] = child
			}
		}
		return restored
	default:
		return apiValue
	}
}
