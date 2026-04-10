package provider

import "fmt"

func requiredStringField(result map[string]interface{}, fieldName string) (string, error) {
	value, ok := result[fieldName]
	if !ok {
		return "", fmt.Errorf("response missing %s", fieldName)
	}

	stringValue, ok := value.(string)
	if !ok || stringValue == "" {
		return "", fmt.Errorf("response missing %s", fieldName)
	}

	return stringValue, nil
}
