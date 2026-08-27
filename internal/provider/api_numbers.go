package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
)

const (
	jsonDecodeMaxInputBytes    = 64 << 20
	jsonDecodeMaxDepth         = 256
	jsonDecodeMaxObjectMembers = 1_000_000
)

var (
	apiJSONNumberPattern = regexp.MustCompile(`^(-?)(0|[1-9][0-9]*)(?:\.([0-9]+))?(?:[eE]([+-]?[0-9]+))?$`)
	errInvalidAPIInteger = errors.New("expected an exact integral JSON number in the int64 range")
	errInvalidAPINumber  = errors.New("expected a finite JSON number")

	errJSONMalformed       = errors.New("malformed JSON")
	errJSONMultipleValues  = errors.New("JSON must contain exactly one value")
	errJSONDuplicateMember = errors.New("JSON contains a duplicate object member")
	errJSONInputLimit      = errors.New("JSON input exceeds the size limit")
	errJSONDepthLimit      = errors.New("JSON input exceeds the nesting limit")
	errJSONMemberLimit     = errors.New("JSON input exceeds the object member limit")
	errJSONDestination     = errors.New("JSON value is incompatible with the destination")
)

type apiValuePresence uint8

const (
	apiValueAbsent apiValuePresence = iota
	apiValueNull
	apiValuePresent
)

// apiValueAt reports absence and explicit null separately. Nested relations
// are traversed exactly as returned by LiteLLM; a malformed present relation
// is an error rather than being mistaken for a missing value.
func apiValueAt(object map[string]interface{}, path ...string) (interface{}, apiValuePresence, error) {
	if len(path) == 0 {
		return nil, apiValueAbsent, errors.New("response field path is empty")
	}
	current := object
	for index, name := range path {
		value, exists := current[name]
		if !exists {
			return nil, apiValueAbsent, nil
		}
		if value == nil {
			return nil, apiValueNull, nil
		}
		if index == len(path)-1 {
			return value, apiValuePresent, nil
		}
		nested, ok := value.(map[string]interface{})
		if !ok {
			return nil, apiValuePresent, fmt.Errorf("invalid response field %q: expected an object", strings.Join(path[:index+1], "."))
		}
		current = nested
	}
	panic("unreachable")
}

func apiInt64At(object map[string]interface{}, path ...string) (int64, apiValuePresence, error) {
	value, presence, err := apiValueAt(object, path...)
	if err != nil || presence != apiValuePresent {
		return 0, presence, err
	}
	result, err := exactInt64FromAPI(value)
	if err != nil {
		return 0, presence, fmt.Errorf("invalid numeric response field %q: %w", strings.Join(path, "."), err)
	}
	return result, presence, nil
}

func apiFloat64At(object map[string]interface{}, path ...string) (float64, apiValuePresence, error) {
	value, presence, err := apiValueAt(object, path...)
	if err != nil || presence != apiValuePresent {
		return 0, presence, err
	}
	result, err := float64FromAPI(value)
	if err != nil {
		return 0, presence, fmt.Errorf("invalid numeric response field %q: %w", strings.Join(path, "."), err)
	}
	return result, presence, nil
}

func apiInt64MapAt(object map[string]interface{}, path ...string) (map[string]int64, apiValuePresence, error) {
	raw, presence, err := apiValueAt(object, path...)
	if err != nil || presence != apiValuePresent {
		return nil, presence, err
	}
	values, ok := raw.(map[string]interface{})
	if !ok {
		return nil, presence, fmt.Errorf("invalid numeric response field %q: expected an object of exact integers", strings.Join(path, "."))
	}
	result := make(map[string]int64, len(values))
	for name, value := range values {
		number, conversionErr := exactInt64FromAPI(value)
		if conversionErr != nil {
			return nil, presence, fmt.Errorf("invalid numeric response field %q: %w", strings.Join(path, "."), conversionErr)
		}
		result[name] = number
	}
	return result, presence, nil
}

func apiFloat64MapAt(object map[string]interface{}, path ...string) (map[string]float64, apiValuePresence, error) {
	raw, presence, err := apiValueAt(object, path...)
	if err != nil || presence != apiValuePresent {
		return nil, presence, err
	}
	values, ok := raw.(map[string]interface{})
	if !ok {
		return nil, presence, fmt.Errorf("invalid numeric response field %q: expected an object of finite numbers", strings.Join(path, "."))
	}
	result := make(map[string]float64, len(values))
	for name, value := range values {
		number, conversionErr := float64FromAPI(value)
		if conversionErr != nil {
			return nil, presence, fmt.Errorf("invalid numeric response field %q: %w", strings.Join(path, "."), conversionErr)
		}
		result[name] = number
	}
	return result, presence, nil
}

// decodeJSONUseNumber is the single response-decoding path for the provider
// client. UseNumber affects interface-backed fields while typed DTO numeric
// fields continue to use encoding/json's normal checked decoding. A token pass
// runs first because encoding/json otherwise silently keeps the last occurrence
// of a duplicate object member. All errors are deliberately content-free.
func decodeJSONUseNumber(data []byte, result interface{}) error {
	if len(data) > jsonDecodeMaxInputBytes {
		return errJSONInputLimit
	}
	if err := validateJSONStructure(data); err != nil {
		return err
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(result); err != nil {
		return errJSONDestination
	}
	var trailing interface{}
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errJSONMultipleValues
		}
		return errJSONMalformed
	}
	return nil
}

func validateJSONStructure(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	members := 0
	if err := validateJSONValue(decoder, 1, &members); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errJSONMultipleValues
		}
		return errJSONMalformed
	}
	return nil
}

func validateJSONValue(decoder *json.Decoder, depth int, members *int) error {
	if depth > jsonDecodeMaxDepth {
		return errJSONDepthLimit
	}
	token, err := decoder.Token()
	if err != nil {
		return errJSONMalformed
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}

	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			nameToken, tokenErr := decoder.Token()
			if tokenErr != nil {
				return errJSONMalformed
			}
			name, ok := nameToken.(string)
			if !ok {
				return errJSONMalformed
			}
			*members++
			if *members > jsonDecodeMaxObjectMembers {
				return errJSONMemberLimit
			}
			if _, duplicate := seen[name]; duplicate {
				return errJSONDuplicateMember
			}
			seen[name] = struct{}{}
			if valueErr := validateJSONValue(decoder, depth+1, members); valueErr != nil {
				return valueErr
			}
		}
		closing, closingErr := decoder.Token()
		if closingErr != nil || closing != json.Delim('}') {
			return errJSONMalformed
		}
		return nil
	case '[':
		for decoder.More() {
			if valueErr := validateJSONValue(decoder, depth+1, members); valueErr != nil {
				return valueErr
			}
		}
		closing, closingErr := decoder.Token()
		if closingErr != nil || closing != json.Delim(']') {
			return errJSONMalformed
		}
		return nil
	default:
		return errJSONMalformed
	}
}

// canonicalJSONNumberString returns an exact, formatting-independent decimal
// representation of a JSON number. It never converts through float64, so
// integers above 2^53 and close decimal values remain distinct. Very large
// exponents are represented in normalized scientific notation to keep response
// handling bounded.
func canonicalJSONNumberString(value string) (string, bool) {
	parts := apiJSONNumberPattern.FindStringSubmatch(value)
	if parts == nil {
		return "", false
	}

	digits := parts[2] + parts[3]
	exponent := new(big.Int)
	if parts[4] != "" {
		if _, ok := exponent.SetString(parts[4], 10); !ok {
			return "", false
		}
	}
	decimalPosition := new(big.Int).SetInt64(int64(len(parts[2])))
	decimalPosition.Add(decimalPosition, exponent)

	leading := 0
	for leading < len(digits) && digits[leading] == '0' {
		leading++
	}
	if leading == len(digits) {
		return "0", true
	}
	digits = digits[leading:]
	decimalPosition.Sub(decimalPosition, big.NewInt(int64(leading)))

	negative := parts[1] == "-"
	prefix := ""
	if negative {
		prefix = "-"
	}

	// A one-million-digit expansion is already far beyond any useful LiteLLM
	// parameter while keeping this helper safe for hostile response exponents.
	const maxPlainNumberDigits = int64(1_000_000)
	if decimalPosition.IsInt64() {
		position := decimalPosition.Int64()
		if position >= -maxPlainNumberDigits && position <= maxPlainNumberDigits && int64(len(digits))+absInt64(position) <= maxPlainNumberDigits {
			var canonical string
			switch {
			case position <= 0:
				canonical = "0." + strings.Repeat("0", int(-position)) + digits
			case position >= int64(len(digits)):
				canonical = digits + strings.Repeat("0", int(position)-len(digits))
			default:
				canonical = digits[:position] + "." + digits[position:]
			}
			if strings.Contains(canonical, ".") {
				canonical = strings.TrimRight(canonical, "0")
				canonical = strings.TrimRight(canonical, ".")
			}
			return prefix + canonical, true
		}
	}

	// Scientific fallback: remove coefficient trailing zeroes and account for
	// them in the exponent. This is still an exact representation.
	coefficient := strings.TrimRight(digits, "0")
	scientificExponent := new(big.Int).Sub(decimalPosition, big.NewInt(1))
	if len(coefficient) == 1 {
		return prefix + coefficient + "e" + scientificExponent.String(), true
	}
	return prefix + coefficient[:1] + "." + coefficient[1:] + "e" + scientificExponent.String(), true
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

func exactJSONNumbersEqual(left, right json.Number) bool {
	leftCanonical, leftOK := canonicalJSONNumberString(left.String())
	rightCanonical, rightOK := canonicalJSONNumberString(right.String())
	return leftOK && rightOK && leftCanonical == rightCanonical
}

// exactInt64FromAPI converts the numeric representations used by decoded API
// responses without passing through float64. Decimal and scientific notation
// are accepted only when their mathematical value is integral and fits int64.
// The error deliberately excludes the response value so callers can safely
// include it in Terraform diagnostics.
func exactInt64FromAPI(value interface{}) (int64, error) {
	switch value := value.(type) {
	case int:
		return int64(value), nil
	case int8:
		return int64(value), nil
	case int16:
		return int64(value), nil
	case int32:
		return int64(value), nil
	case int64:
		return value, nil
	case uint:
		if uint64(value) > math.MaxInt64 {
			return 0, errInvalidAPIInteger
		}
		return int64(value), nil
	case uint8:
		return int64(value), nil
	case uint16:
		return int64(value), nil
	case uint32:
		return int64(value), nil
	case uint64:
		if value > math.MaxInt64 {
			return 0, errInvalidAPIInteger
		}
		return int64(value), nil
	case float32:
		return exactInt64FromFloat(float64(value))
	case float64:
		return exactInt64FromFloat(value)
	case json.Number:
		return exactInt64FromString(value.String())
	case string:
		return exactInt64FromString(value)
	default:
		return 0, errInvalidAPIInteger
	}
}

func exactInt64FromFloat(value float64) (int64, error) {
	const maxSafeFloatInteger = float64(1<<53 - 1)
	if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > maxSafeFloatInteger {
		return 0, errInvalidAPIInteger
	}
	rational := new(big.Rat).SetFloat64(value)
	if rational == nil || !rational.IsInt() || !rational.Num().IsInt64() {
		return 0, errInvalidAPIInteger
	}
	return rational.Num().Int64(), nil
}

func exactInt64FromString(value string) (int64, error) {
	parts := apiJSONNumberPattern.FindStringSubmatch(value)
	if parts == nil {
		return 0, errInvalidAPIInteger
	}

	// coefficient * 10^(exponent-fractionDigits). Work on decimal digits so
	// even values above 2^53 never pass through a binary float. The bounded
	// digit operations also avoid allocating enormous big integers for hostile
	// exponents in an API response.
	digits := strings.TrimLeft(parts[2]+parts[3], "0")
	if digits == "" {
		return 0, nil
	}
	exponent := int64(0)
	if parts[4] != "" {
		var err error
		exponent, err = strconv.ParseInt(parts[4], 10, 64)
		if err != nil {
			return 0, errInvalidAPIInteger
		}
	}
	fractionDigits := int64(len(parts[3]))
	if exponent <= math.MinInt64+fractionDigits {
		return 0, errInvalidAPIInteger
	}
	scale := exponent - fractionDigits
	if scale < 0 {
		remove := -scale
		if remove > int64(len(digits)) {
			return 0, errInvalidAPIInteger
		}
		cut := len(digits) - int(remove)
		if strings.Trim(digits[cut:], "0") != "" {
			return 0, errInvalidAPIInteger
		}
		digits = digits[:cut]
		if digits == "" {
			return 0, nil
		}
	} else {
		if scale > 19 || int64(len(digits))+scale > 19 {
			return 0, errInvalidAPIInteger
		}
		digits += strings.Repeat("0", int(scale))
	}
	if parts[1] == "-" {
		digits = "-" + digits
	}
	result, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, errInvalidAPIInteger
	}
	return result, nil
}

// float64FromAPI keeps ordinary floating-point API fields separate from exact
// integer conversion. Float schemas retain their existing precision, but
// malformed and non-finite present values are always reported to callers.
func float64FromAPI(value interface{}) (float64, error) {
	var result float64
	var err error
	switch value := value.(type) {
	case float64:
		result = value
	case float32:
		result = float64(value)
	case json.Number:
		result, err = value.Float64()
	case string:
		result, err = strconv.ParseFloat(value, 64)
	case int:
		result = float64(value)
	case int8:
		result = float64(value)
	case int16:
		result = float64(value)
	case int32:
		result = float64(value)
	case int64:
		result = float64(value)
	case uint:
		result = float64(value)
	case uint8:
		result = float64(value)
	case uint16:
		result = float64(value)
	case uint32:
		result = float64(value)
	case uint64:
		result = float64(value)
	default:
		return 0, errInvalidAPINumber
	}
	if err != nil || math.IsNaN(result) || math.IsInf(result, 0) {
		return 0, errInvalidAPINumber
	}
	return result, nil
}
