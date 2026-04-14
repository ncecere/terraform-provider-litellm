package provider

import (
	"encoding/json"
	"fmt"
	"strings"
)

// convertJSONStringsToNative takes a map of string values and converts any values
// that contain JSON objects or arrays into native Go types so the API receives
// structured data rather than escaped JSON strings.
//
// For example, a Terraform config like:
//
//	metadata = {
//	  "logging" = jsonencode([{ "callback_name": "langsmith" }])
//	}
//
// will send the "logging" value as a native JSON array, not a string.
func convertJSONStringsToNative(values map[string]string) map[string]interface{} {
	result := make(map[string]interface{}, len(values))
	for k, v := range values {
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

// valueToJSONString converts a value from an API response back to a string
// for storage in Terraform state. String values are returned as-is;
// non-string values (arrays, objects, numbers, booleans) are JSON-encoded.
func valueToJSONString(v interface{}) string {
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
