package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func int64RequestMap(value types.Map, field string) (map[string]int64, error) {
	result := make(map[string]int64, len(value.Elements()))
	for key, element := range value.Elements() {
		number, ok := element.(types.Int64)
		if !ok || number.IsNull() {
			return nil, fmt.Errorf("invalid %s: element %q must be a non-null integer", field, key)
		}
		if number.IsUnknown() {
			return nil, fmt.Errorf("invalid %s: element %q must be known before apply", field, key)
		}
		result[key] = number.ValueInt64()
	}
	return result, nil
}

func float64RequestMap(value types.Map, field string) (map[string]float64, error) {
	result := make(map[string]float64, len(value.Elements()))
	for key, element := range value.Elements() {
		number, ok := element.(types.Float64)
		if !ok || number.IsNull() {
			return nil, fmt.Errorf("invalid %s: element %q must be a non-null number", field, key)
		}
		if number.IsUnknown() {
			return nil, fmt.Errorf("invalid %s: element %q must be known before apply", field, key)
		}
		result[key] = number.ValueFloat64()
	}
	return result, nil
}
