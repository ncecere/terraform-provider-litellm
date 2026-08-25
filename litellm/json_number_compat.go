package litellm

import (
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"strconv"
)

var (
	errLegacyJSONFloat = errors.New("expected a finite JSON number")
	errLegacyJSONInt   = errors.New("expected an exact JSON integer in the platform int range")
)

func legacyFloat64FromJSON(value interface{}) (float64, error) {
	var result float64
	var err error
	switch value := value.(type) {
	case json.Number:
		result, err = value.Float64()
	case float64:
		result = value
	case float32:
		result = float64(value)
	case int:
		result = float64(value)
	case int64:
		result = float64(value)
	default:
		return 0, errLegacyJSONFloat
	}
	if err != nil || math.IsNaN(result) || math.IsInf(result, 0) {
		return 0, errLegacyJSONFloat
	}
	return result, nil
}

func legacyIntFromJSON(value interface{}) (int, error) {
	switch value := value.(type) {
	case int:
		return value, nil
	case int64:
		return legacyIntFromInt64(value)
	case json.Number:
		if !json.Valid([]byte(value.String())) {
			return 0, errLegacyJSONInt
		}
		rational, ok := new(big.Rat).SetString(value.String())
		if !ok || !rational.IsInt() || !rational.Num().IsInt64() {
			return 0, errLegacyJSONInt
		}
		return legacyIntFromInt64(rational.Num().Int64())
	case float64:
		// A binary float cannot carry an exact integer identity above 2^53.
		// Reject that legacy representation rather than silently rounding it.
		const maxSafeFloatInteger = float64(1<<53 - 1)
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || math.Abs(value) > maxSafeFloatInteger {
			return 0, errLegacyJSONInt
		}
		return legacyIntFromInt64(int64(value))
	default:
		return 0, errLegacyJSONInt
	}
}

func legacyIntFromInt64(value int64) (int, error) {
	if strconv.IntSize == 32 && (value < math.MinInt32 || value > math.MaxInt32) {
		return 0, errLegacyJSONInt
	}
	return int(value), nil
}

func legacyFloatMapFromJSON(raw map[string]interface{}) (map[string]interface{}, error) {
	result := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		number, err := legacyFloat64FromJSON(value)
		if err != nil {
			return nil, err
		}
		result[key] = number
	}
	return result, nil
}

func legacyIntMapFromJSON(raw map[string]interface{}) (map[string]interface{}, error) {
	result := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		number, err := legacyIntFromJSON(value)
		if err != nil {
			return nil, err
		}
		result[key] = number
	}
	return result, nil
}

// legacyStringMapFromJSON removes json.Number from maps consumed by SDK
// TypeMap(string) fields while preserving the exact JSON number text.
func legacyStringMapFromJSON(raw map[string]interface{}) map[string]interface{} {
	if raw == nil {
		return nil
	}
	result := make(map[string]interface{}, len(raw))
	for key, value := range raw {
		switch value := value.(type) {
		case json.Number:
			result[key] = value.String()
		case map[string]interface{}:
			result[key] = legacyStringMapFromJSON(value)
		case []interface{}:
			result[key] = legacyStringSliceValuesFromJSON(value)
		default:
			result[key] = value
		}
	}
	return result
}

func legacyStringSliceValuesFromJSON(raw []interface{}) []interface{} {
	result := make([]interface{}, len(raw))
	for index, value := range raw {
		switch value := value.(type) {
		case json.Number:
			result[index] = value.String()
		case map[string]interface{}:
			result[index] = legacyStringMapFromJSON(value)
		case []interface{}:
			result[index] = legacyStringSliceValuesFromJSON(value)
		default:
			result[index] = value
		}
	}
	return result
}
