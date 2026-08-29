package provider

import "fmt"

// validateImportedObjectIdentity gates one-time import adoption on an
// authoritative response identity. Diagnostics deliberately describe only the
// resource contract and never include response values, request paths, or
// potentially sensitive identifiers.
func validateImportedObjectIdentity(imported bool, resourceName string, object map[string]interface{}, identityField, expectedIdentity string) error {
	if !imported {
		return nil
	}
	if object == nil {
		return fmt.Errorf("%s import read response is missing the expected object", resourceName)
	}
	identity, ok := object[identityField].(string)
	if !ok || identity == "" {
		return fmt.Errorf("%s import read response is missing its required identity", resourceName)
	}
	if expectedIdentity == "" || identity != expectedIdentity {
		return fmt.Errorf("%s import read response identity does not match the imported identity", resourceName)
	}
	return nil
}

func requireImportedStringField(imported bool, resourceName string, object map[string]interface{}, field string) error {
	if !imported {
		return nil
	}
	value, ok := object[field].(string)
	if !ok || value == "" {
		return fmt.Errorf("%s import read response is missing a required string field", resourceName)
	}
	return nil
}

func requireImportedObjectField(imported bool, resourceName string, object map[string]interface{}, field string) (map[string]interface{}, error) {
	value, ok := object[field].(map[string]interface{})
	if !ok || value == nil {
		if imported {
			return nil, fmt.Errorf("%s import read response is missing a required object field", resourceName)
		}
		return nil, nil
	}
	return value, nil
}

func requireImportedArrayField(imported bool, resourceName string, object map[string]interface{}, field string) ([]interface{}, error) {
	value, ok := object[field].([]interface{})
	if !ok || value == nil {
		if imported {
			return nil, fmt.Errorf("%s import read response is missing a required array field", resourceName)
		}
		return nil, nil
	}
	return value, nil
}
