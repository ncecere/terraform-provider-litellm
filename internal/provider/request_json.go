package provider

import "fmt"

func decodeRequestJSONObject(value, field string) (map[string]interface{}, error) {
	var decoded interface{}
	if err := decodeJSONUseNumber([]byte(value), &decoded); err != nil {
		return nil, fmt.Errorf("invalid %s: must be valid JSON", field)
	}
	object, ok := decoded.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid %s: must be a JSON object", field)
	}
	return object, nil
}
