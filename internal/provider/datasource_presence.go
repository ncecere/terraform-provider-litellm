package provider

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Data-source response readers deliberately do not consult prior state. A
// successful Read must replace API null or omission with a Terraform typed
// null, while preserving explicit false, zero, and empty values.

func dataSourceRequiredStringAt(object map[string]interface{}, path ...string) (types.String, error) {
	raw, err := dataSourceRequiredValueAt(object, "a nonempty string", path...)
	if err != nil {
		return types.StringNull(), err
	}
	value, ok := raw.(string)
	if !ok || value == "" {
		return types.StringNull(), dataSourceShapeError(path, "a nonempty string")
	}
	return types.StringValue(value), nil
}

func dataSourceNullableStringAt(object map[string]interface{}, path ...string) (types.String, error) {
	raw, present, err := dataSourceNullableValueAt(object, "a string or null", path...)
	if err != nil || !present {
		return types.StringNull(), err
	}
	value, ok := raw.(string)
	if !ok {
		return types.StringNull(), dataSourceShapeError(path, "a string or null")
	}
	return types.StringValue(value), nil
}

func dataSourceRequiredBoolAt(object map[string]interface{}, path ...string) (types.Bool, error) {
	raw, err := dataSourceRequiredValueAt(object, "a boolean", path...)
	if err != nil {
		return types.BoolNull(), err
	}
	value, ok := raw.(bool)
	if !ok {
		return types.BoolNull(), dataSourceShapeError(path, "a boolean")
	}
	return types.BoolValue(value), nil
}

func dataSourceNullableBoolAt(object map[string]interface{}, path ...string) (types.Bool, error) {
	raw, present, err := dataSourceNullableValueAt(object, "a boolean or null", path...)
	if err != nil || !present {
		return types.BoolNull(), err
	}
	value, ok := raw.(bool)
	if !ok {
		return types.BoolNull(), dataSourceShapeError(path, "a boolean or null")
	}
	return types.BoolValue(value), nil
}

func dataSourceRequiredInt64At(object map[string]interface{}, path ...string) (types.Int64, error) {
	raw, err := dataSourceRequiredValueAt(object, "an exact integral JSON number in the int64 range", path...)
	if err != nil {
		return types.Int64Null(), err
	}
	if !dataSourceAPIJSONNumber(raw) {
		return types.Int64Null(), dataSourceShapeError(path, "an exact integral JSON number in the int64 range")
	}
	value, presence, err := apiInt64At(object, path...)
	if err != nil || presence != apiValuePresent {
		return types.Int64Null(), dataSourceShapeError(path, "an exact integral JSON number in the int64 range")
	}
	return types.Int64Value(value), nil
}

func dataSourceNullableInt64At(object map[string]interface{}, path ...string) (types.Int64, error) {
	raw, present, err := dataSourceNullableValueAt(object, "an exact integral JSON number in the int64 range or null", path...)
	if err != nil || !present {
		return types.Int64Null(), err
	}
	if !dataSourceAPIJSONNumber(raw) {
		return types.Int64Null(), dataSourceShapeError(path, "an exact integral JSON number in the int64 range or null")
	}
	value, presence, conversionErr := apiInt64At(object, path...)
	if conversionErr != nil || presence != apiValuePresent {
		return types.Int64Null(), dataSourceShapeError(path, "an exact integral JSON number in the int64 range or null")
	}
	return types.Int64Value(value), nil
}

func dataSourceRequiredFloat64At(object map[string]interface{}, path ...string) (types.Float64, error) {
	raw, err := dataSourceRequiredValueAt(object, "a finite JSON number", path...)
	if err != nil {
		return types.Float64Null(), err
	}
	if !dataSourceAPIJSONNumber(raw) {
		return types.Float64Null(), dataSourceShapeError(path, "a finite JSON number")
	}
	value, presence, err := apiFloat64At(object, path...)
	if err != nil || presence != apiValuePresent {
		return types.Float64Null(), dataSourceShapeError(path, "a finite JSON number")
	}
	return types.Float64Value(value), nil
}

func dataSourceNullableFloat64At(object map[string]interface{}, path ...string) (types.Float64, error) {
	raw, present, err := dataSourceNullableValueAt(object, "a finite JSON number or null", path...)
	if err != nil || !present {
		return types.Float64Null(), err
	}
	if !dataSourceAPIJSONNumber(raw) {
		return types.Float64Null(), dataSourceShapeError(path, "a finite JSON number or null")
	}
	value, presence, conversionErr := apiFloat64At(object, path...)
	if conversionErr != nil || presence != apiValuePresent {
		return types.Float64Null(), dataSourceShapeError(path, "a finite JSON number or null")
	}
	return types.Float64Value(value), nil
}

func dataSourceRequiredStringListAt(object map[string]interface{}, path ...string) (types.List, error) {
	raw, err := dataSourceRequiredValueAt(object, "a list of strings", path...)
	if err != nil {
		return types.ListNull(types.StringType), err
	}
	return dataSourceStringListValue(raw, path)
}

func dataSourceNullableStringListAt(object map[string]interface{}, path ...string) (types.List, error) {
	raw, present, err := dataSourceNullableValueAt(object, "a list of strings or null", path...)
	if err != nil || !present {
		return types.ListNull(types.StringType), err
	}
	return dataSourceStringListValue(raw, path)
}

func dataSourceRequiredStringSetAt(object map[string]interface{}, path ...string) (types.Set, error) {
	raw, err := dataSourceRequiredValueAt(object, "a list of strings", path...)
	if err != nil {
		return types.SetNull(types.StringType), err
	}
	return dataSourceStringSetValue(raw, path)
}

func dataSourceNullableStringSetAt(object map[string]interface{}, path ...string) (types.Set, error) {
	raw, present, err := dataSourceNullableValueAt(object, "a list of strings or null", path...)
	if err != nil || !present {
		return types.SetNull(types.StringType), err
	}
	return dataSourceStringSetValue(raw, path)
}

func dataSourceRequiredStringMapAt(object map[string]interface{}, path ...string) (types.Map, error) {
	raw, err := dataSourceRequiredValueAt(object, "an object of strings", path...)
	if err != nil {
		return types.MapNull(types.StringType), err
	}
	return dataSourceStringMapValue(raw, path)
}

func dataSourceNullableStringMapAt(object map[string]interface{}, path ...string) (types.Map, error) {
	raw, present, err := dataSourceNullableValueAt(object, "an object of strings or null", path...)
	if err != nil || !present {
		return types.MapNull(types.StringType), err
	}
	return dataSourceStringMapValue(raw, path)
}

// Canonical JSON fields are Terraform strings whose API representation must be
// an object. json.Marshal keeps json.Number lexemes exact and deterministically
// orders object keys; no conversion through float64 occurs.
func dataSourceRequiredCanonicalJSONObjectAt(object map[string]interface{}, path ...string) (types.String, error) {
	raw, err := dataSourceRequiredValueAt(object, "an object", path...)
	if err != nil {
		return types.StringNull(), err
	}
	return dataSourceCanonicalJSONObjectValue(raw, path)
}

func dataSourceNullableCanonicalJSONObjectAt(object map[string]interface{}, path ...string) (types.String, error) {
	raw, present, err := dataSourceNullableValueAt(object, "an object or null", path...)
	if err != nil || !present {
		return types.StringNull(), err
	}
	return dataSourceCanonicalJSONObjectValue(raw, path)
}

// Role-redacted nullable fields intentionally have the same state semantics as
// other nullable fields: an omitted field resolves to typed null, never to a
// fabricated empty, false, or zero value. The separate entry points document
// that call-site contract.
func dataSourceRoleRedactedNullableStringAt(object map[string]interface{}, path ...string) (types.String, error) {
	return dataSourceNullableStringAt(object, path...)
}

func dataSourceRoleRedactedNullableBoolAt(object map[string]interface{}, path ...string) (types.Bool, error) {
	return dataSourceNullableBoolAt(object, path...)
}

func dataSourceRoleRedactedNullableInt64At(object map[string]interface{}, path ...string) (types.Int64, error) {
	return dataSourceNullableInt64At(object, path...)
}

func dataSourceRoleRedactedNullableFloat64At(object map[string]interface{}, path ...string) (types.Float64, error) {
	return dataSourceNullableFloat64At(object, path...)
}

func dataSourceRoleRedactedNullableStringListAt(object map[string]interface{}, path ...string) (types.List, error) {
	return dataSourceNullableStringListAt(object, path...)
}

func dataSourceRoleRedactedNullableStringSetAt(object map[string]interface{}, path ...string) (types.Set, error) {
	return dataSourceNullableStringSetAt(object, path...)
}

func dataSourceRoleRedactedNullableStringMapAt(object map[string]interface{}, path ...string) (types.Map, error) {
	return dataSourceNullableStringMapAt(object, path...)
}

func dataSourceRoleRedactedNullableCanonicalJSONObjectAt(object map[string]interface{}, path ...string) (types.String, error) {
	return dataSourceNullableCanonicalJSONObjectAt(object, path...)
}

func dataSourceRequiredValueAt(object map[string]interface{}, expected string, path ...string) (interface{}, error) {
	if len(path) == 0 {
		return nil, dataSourceShapeError(path, expected)
	}
	value, presence, err := apiValueAt(object, path...)
	if err != nil {
		return nil, err
	}
	if presence != apiValuePresent {
		return nil, dataSourceShapeError(path, expected)
	}
	return value, nil
}

func dataSourceNullableValueAt(object map[string]interface{}, expected string, path ...string) (interface{}, bool, error) {
	if len(path) == 0 {
		return nil, false, dataSourceShapeError(path, expected)
	}
	value, presence, err := apiValueAt(object, path...)
	if err != nil {
		return nil, false, err
	}
	if presence != apiValuePresent {
		return nil, false, nil
	}
	return value, true, nil
}

func dataSourceAPIJSONNumber(value interface{}) bool {
	switch value.(type) {
	case json.Number, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	default:
		return false
	}
}

func dataSourceShapeError(path []string, expected string) error {
	if len(path) == 0 {
		return fmt.Errorf("invalid response field path: expected %s", expected)
	}
	return fmt.Errorf("invalid response field %q: expected %s", strings.Join(path, "."), expected)
}

func dataSourceStringElements(raw interface{}, path []string, nullable bool) ([]attr.Value, error) {
	var values []interface{}
	switch value := raw.(type) {
	case []interface{}:
		values = value
	case []string:
		values = make([]interface{}, len(value))
		for index := range value {
			values[index] = value[index]
		}
	default:
		expected := "a list of strings"
		if nullable {
			expected += " or null"
		}
		return nil, dataSourceShapeError(path, expected)
	}

	elements := make([]attr.Value, len(values))
	for index, rawElement := range values {
		value, ok := rawElement.(string)
		if !ok {
			expected := "a list of strings"
			if nullable {
				expected += " or null"
			}
			return nil, dataSourceShapeError(path, expected)
		}
		elements[index] = types.StringValue(value)
	}
	return elements, nil
}

func dataSourceStringListValue(raw interface{}, path []string) (types.List, error) {
	elements, err := dataSourceStringElements(raw, path, false)
	if err != nil {
		return types.ListNull(types.StringType), err
	}
	value, diagnostics := types.ListValue(types.StringType, elements)
	if diagnostics.HasError() {
		return types.ListNull(types.StringType), dataSourceShapeError(path, "a list of strings")
	}
	return value, nil
}

func dataSourceStringSetValue(raw interface{}, path []string) (types.Set, error) {
	elements, err := dataSourceStringElements(raw, path, false)
	if err != nil {
		return types.SetNull(types.StringType), err
	}
	value, diagnostics := types.SetValue(types.StringType, elements)
	if diagnostics.HasError() {
		return types.SetNull(types.StringType), dataSourceShapeError(path, "a list of strings")
	}
	return value, nil
}

func dataSourceStringMapValue(raw interface{}, path []string) (types.Map, error) {
	values := make(map[string]attr.Value)
	switch object := raw.(type) {
	case map[string]interface{}:
		values = make(map[string]attr.Value, len(object))
		for key, rawValue := range object {
			value, ok := rawValue.(string)
			if !ok {
				return types.MapNull(types.StringType), dataSourceShapeError(path, "an object of strings")
			}
			values[key] = types.StringValue(value)
		}
	case map[string]string:
		values = make(map[string]attr.Value, len(object))
		for key, value := range object {
			values[key] = types.StringValue(value)
		}
	default:
		return types.MapNull(types.StringType), dataSourceShapeError(path, "an object of strings")
	}
	value, diagnostics := types.MapValue(types.StringType, values)
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), dataSourceShapeError(path, "an object of strings")
	}
	return value, nil
}

func dataSourceCanonicalJSONObjectValue(raw interface{}, path []string) (types.String, error) {
	object, ok := raw.(map[string]interface{})
	if !ok {
		return types.StringNull(), dataSourceShapeError(path, "an object")
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return types.StringNull(), dataSourceShapeError(path, "an object representable as canonical JSON")
	}
	return types.StringValue(string(encoded)), nil
}
