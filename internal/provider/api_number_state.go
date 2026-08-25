package provider

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const numericImportedPrivateKey = "numeric_imported_v1"

func updateInt64FromAPI(target *types.Int64, object map[string]interface{}, clearAbsent, adoptPresent bool, path ...string) error {
	value, presence, err := apiInt64At(object, path...)
	if err != nil {
		return err
	}
	switch presence {
	case apiValuePresent:
		if adoptPresent {
			*target = types.Int64Value(value)
		} else if target.IsUnknown() {
			*target = types.Int64Null()
		}
	case apiValueNull:
		*target = types.Int64Null()
	case apiValueAbsent:
		if clearAbsent || target.IsUnknown() {
			*target = types.Int64Null()
		}
	}
	return nil
}

func updateFloat64FromAPI(target *types.Float64, object map[string]interface{}, clearAbsent, adoptPresent bool, path ...string) error {
	value, presence, err := apiFloat64At(object, path...)
	if err != nil {
		return err
	}
	switch presence {
	case apiValuePresent:
		if adoptPresent {
			*target = types.Float64Value(value)
		} else if target.IsUnknown() {
			*target = types.Float64Null()
		}
	case apiValueNull:
		*target = types.Float64Null()
	case apiValueAbsent:
		if clearAbsent || target.IsUnknown() {
			*target = types.Float64Null()
		}
	}
	return nil
}

func updateInt64FromAPIAliases(target *types.Int64, object map[string]interface{}, clearAbsent, adoptPresent bool, fields ...string) error {
	for _, field := range fields {
		_, presence, err := apiValueAt(object, field)
		if err != nil {
			return err
		}
		if presence != apiValueAbsent {
			return updateInt64FromAPI(target, object, false, adoptPresent, field)
		}
	}
	if clearAbsent || target.IsUnknown() {
		*target = types.Int64Null()
	}
	return nil
}

func updateFloat64FromAPIAliases(target *types.Float64, object map[string]interface{}, clearAbsent, adoptPresent bool, fields ...string) error {
	for _, field := range fields {
		_, presence, err := apiValueAt(object, field)
		if err != nil {
			return err
		}
		if presence != apiValueAbsent {
			return updateFloat64FromAPI(target, object, false, adoptPresent, field)
		}
	}
	if clearAbsent || target.IsUnknown() {
		*target = types.Float64Null()
	}
	return nil
}

// firstAPIFieldPath selects the first non-omitted response path. Explicit null
// at the final field is authoritative and stops fallback traversal. A missing
// or null parent relation makes that nested path unavailable, allowing a
// historical flat field to be used instead.
func firstAPIFieldPath(object map[string]interface{}, paths ...[]string) ([]string, error) {
	for _, path := range paths {
		parentUnavailable := false
		for length := 1; length < len(path); length++ {
			_, presence, err := apiValueAt(object, path[:length]...)
			if err != nil {
				return nil, err
			}
			if presence == apiValueAbsent || presence == apiValueNull {
				parentUnavailable = true
				break
			}
		}
		if parentUnavailable {
			continue
		}
		_, presence, err := apiValueAt(object, path...)
		if err != nil {
			return nil, err
		}
		if presence != apiValueAbsent {
			return path, nil
		}
	}
	return nil, nil
}

func updateFloat64FromAPIPaths(target *types.Float64, object map[string]interface{}, clearAbsent, adoptPresent bool, paths ...[]string) error {
	selected, err := firstAPIFieldPath(object, paths...)
	if err != nil {
		return err
	}
	if selected != nil {
		return updateFloat64FromAPI(target, object, false, adoptPresent, selected...)
	}
	if clearAbsent || target.IsUnknown() {
		*target = types.Float64Null()
	}
	return nil
}

func updateInt64MapFromAPI(target *types.Map, object map[string]interface{}, clearAbsent, adoptPresent bool, path ...string) error {
	values, presence, err := apiInt64MapAt(object, path...)
	if err != nil {
		return err
	}
	switch presence {
	case apiValuePresent:
		if !adoptPresent {
			if target.IsUnknown() {
				*target = types.MapNull(types.Int64Type)
			}
			return nil
		}
		mapped := make(map[string]attr.Value, len(values))
		for name, value := range values {
			mapped[name] = types.Int64Value(value)
		}
		result, diagnostics := types.MapValue(types.Int64Type, mapped)
		if diagnostics.HasError() {
			return fmt.Errorf("invalid numeric response field %q: cannot build exact integer map state", strings.Join(path, "."))
		}
		*target = result
	case apiValueNull:
		if adoptPresent || target.IsUnknown() {
			*target = types.MapNull(types.Int64Type)
		}
	case apiValueAbsent:
		if clearAbsent || target.IsUnknown() {
			*target = types.MapNull(types.Int64Type)
		}
	}
	return nil
}

func updateFloat64MapFromAPI(target *types.Map, object map[string]interface{}, clearAbsent, adoptPresent bool, path ...string) error {
	values, presence, err := apiFloat64MapAt(object, path...)
	if err != nil {
		return err
	}
	switch presence {
	case apiValuePresent:
		if !adoptPresent {
			if target.IsUnknown() {
				*target = types.MapNull(types.Float64Type)
			}
			return nil
		}
		mapped := make(map[string]attr.Value, len(values))
		for name, value := range values {
			mapped[name] = types.Float64Value(value)
		}
		result, diagnostics := types.MapValue(types.Float64Type, mapped)
		if diagnostics.HasError() {
			return fmt.Errorf("invalid numeric response field %q: cannot build finite number map state", strings.Join(path, "."))
		}
		*target = result
	case apiValueNull:
		if adoptPresent || target.IsUnknown() {
			*target = types.MapNull(types.Float64Type)
		}
	case apiValueAbsent:
		if clearAbsent || target.IsUnknown() {
			*target = types.MapNull(types.Float64Type)
		}
	}
	return nil
}

func updateFloat64MapFromAPIPaths(target *types.Map, object map[string]interface{}, clearAbsent, adoptPresent bool, paths ...[]string) error {
	selected, err := firstAPIFieldPath(object, paths...)
	if err != nil {
		return err
	}
	if selected != nil {
		return updateFloat64MapFromAPI(target, object, false, adoptPresent, selected...)
	}
	if clearAbsent || target.IsUnknown() {
		*target = types.MapNull(types.Float64Type)
	}
	return nil
}
