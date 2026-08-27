package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	mcpInfoJSONMaxClearPointers = 256
	mcpInfoJSONMaxPointerDepth  = 64
	mcpInfoJSONMaxPointerBytes  = 4096
)

var (
	errMCPInfoJSONObject       = errors.New("MCP info JSON must be a non-null object")
	errMCPInfoJSONValue        = errors.New("MCP info contains an unsupported JSON value")
	errMCPInfoJSONLimit        = errors.New("MCP info JSON exceeds a safety limit")
	errMCPInfoJSONCanonical    = errors.New("MCP info JSON cannot be canonicalized")
	errMCPInfoClearPointer     = errors.New("MCP info clear pointer is invalid")
	errMCPInfoClearPointerRoot = errors.New("MCP info clear pointer cannot select the document root")
	errMCPInfoClearConflict    = errors.New("MCP info clear pointers conflict")
	errMCPInfoClearTraversal   = errors.New("MCP info clear pointer must traverse objects only")
)

// parseMCPInfoJSONObject accepts exactly one non-null JSON object. The shared
// decoder supplies duplicate-member rejection, resource limits, and exact
// json.Number values.
func parseMCPInfoJSONObject(raw string) (map[string]interface{}, error) {
	var decoded interface{}
	if err := decodeJSONUseNumber([]byte(raw), &decoded); err != nil {
		return nil, err
	}
	object, ok := decoded.(map[string]interface{})
	if !ok || object == nil {
		return nil, errMCPInfoJSONObject
	}
	if err := validateMCPInfoJSONValue(object); err != nil {
		return nil, err
	}
	return object, nil
}

// canonicalizeMCPInfoJSONObject produces compact, key-sorted JSON and an exact
// formatting-independent spelling for each number without converting through
// float64.
func canonicalizeMCPInfoJSONObject(raw string) (string, error) {
	object, err := parseMCPInfoJSONObject(raw)
	if err != nil {
		return "", err
	}
	return canonicalMCPInfoJSONObject(object)
}

func canonicalMCPInfoJSONObject(object map[string]interface{}) (string, error) {
	if object == nil {
		return "", errMCPInfoJSONObject
	}
	members := 0
	canonical, err := canonicalMCPInfoJSONValue(object, 1, &members)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(canonical); err != nil {
		return "", errMCPInfoJSONCanonical
	}
	return strings.TrimSuffix(output.String(), "\n"), nil
}

func canonicalMCPInfoJSONValue(value interface{}, depth int, members *int) (interface{}, error) {
	if depth > jsonDecodeMaxDepth {
		return nil, errMCPInfoJSONLimit
	}
	switch value := value.(type) {
	case nil, bool, string:
		return value, nil
	case json.Number:
		canonical, ok := canonicalJSONNumberString(value.String())
		if !ok {
			return nil, errMCPInfoJSONValue
		}
		return json.Number(canonical), nil
	case map[string]interface{}:
		*members += len(value)
		if *members > jsonDecodeMaxObjectMembers {
			return nil, errMCPInfoJSONLimit
		}
		result := make(map[string]interface{}, len(value))
		for name, child := range value {
			canonical, err := canonicalMCPInfoJSONValue(child, depth+1, members)
			if err != nil {
				return nil, err
			}
			result[name] = canonical
		}
		return result, nil
	case []interface{}:
		result := make([]interface{}, len(value))
		for index, child := range value {
			canonical, err := canonicalMCPInfoJSONValue(child, depth+1, members)
			if err != nil {
				return nil, err
			}
			result[index] = canonical
		}
		return result, nil
	default:
		return nil, errMCPInfoJSONValue
	}
}

func validateMCPInfoJSONValue(value interface{}) error {
	members := 0
	return walkMCPInfoJSONValue(value, 1, &members)
}

func walkMCPInfoJSONValue(value interface{}, depth int, members *int) error {
	if depth > jsonDecodeMaxDepth {
		return errMCPInfoJSONLimit
	}
	switch value := value.(type) {
	case nil, bool, string:
		return nil
	case json.Number:
		if _, ok := canonicalJSONNumberString(value.String()); !ok {
			return errMCPInfoJSONValue
		}
		return nil
	case map[string]interface{}:
		*members += len(value)
		if *members > jsonDecodeMaxObjectMembers {
			return errMCPInfoJSONLimit
		}
		for _, child := range value {
			if err := walkMCPInfoJSONValue(child, depth+1, members); err != nil {
				return err
			}
		}
		return nil
	case []interface{}:
		for _, child := range value {
			if err := walkMCPInfoJSONValue(child, depth+1, members); err != nil {
				return err
			}
		}
		return nil
	default:
		return errMCPInfoJSONValue
	}
}

func cloneMCPInfoJSONValue(value interface{}) interface{} {
	switch value := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(value))
		for name, child := range value {
			result[name] = cloneMCPInfoJSONValue(child)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(value))
		for index, child := range value {
			result[index] = cloneMCPInfoJSONValue(child)
		}
		return result
	default:
		return value
	}
}

func cloneMCPInfoJSONObject(source map[string]interface{}) map[string]interface{} {
	if source == nil {
		return nil
	}
	return cloneMCPInfoJSONValue(source).(map[string]interface{})
}

func mcpInfoJSONValuesEqual(left, right interface{}) bool {
	return exactJSONValuesEqual(left, right)
}

// overlayMCPInfoJSONObjects preserves unmentioned base members and recursively
// overlays configured object members. Arrays, scalars, and null are atomic. A
// configured empty object is also atomic when it is a member, so {"owner":{}}
// owns and replaces owner rather than accidentally preserving its old fields.
func overlayMCPInfoJSONObjects(base, configured map[string]interface{}) (map[string]interface{}, error) {
	if base == nil || configured == nil {
		return nil, errMCPInfoJSONObject
	}
	if err := validateMCPInfoJSONValue(base); err != nil {
		return nil, err
	}
	if err := validateMCPInfoJSONValue(configured); err != nil {
		return nil, err
	}
	return overlayMCPInfoJSONObjectUnchecked(base, configured), nil
}

func overlayMCPInfoJSONObjectUnchecked(base, configured map[string]interface{}) map[string]interface{} {
	result := cloneMCPInfoJSONObject(base)
	for name, configuredValue := range configured {
		configuredObject, configuredIsObject := configuredValue.(map[string]interface{})
		baseObject, baseIsObject := base[name].(map[string]interface{})
		if configuredIsObject && len(configuredObject) > 0 && baseIsObject {
			result[name] = overlayMCPInfoJSONObjectUnchecked(baseObject, configuredObject)
			continue
		}
		result[name] = cloneMCPInfoJSONValue(configuredValue)
	}
	return result
}

type mcpInfoClearPointer struct {
	canonical string
	members   []string
}

// canonicalMCPInfoClearPointers validates RFC 6901 string-form pointers and
// returns a deterministic sorted copy. Each pointer must select an object
// member, never the document root. Object-only traversal is checked when the
// pointers are applied to a document.
func canonicalMCPInfoClearPointers(pointers []string) ([]string, error) {
	parsed, err := parseMCPInfoClearPointers(pointers)
	if err != nil {
		return nil, err
	}
	result := make([]string, len(parsed))
	for index, pointer := range parsed {
		result[index] = pointer.canonical
	}
	return result, nil
}

func parseMCPInfoClearPointers(pointers []string) ([]mcpInfoClearPointer, error) {
	if len(pointers) > mcpInfoJSONMaxClearPointers {
		return nil, errMCPInfoJSONLimit
	}
	parsed := make([]mcpInfoClearPointer, 0, len(pointers))
	seen := make(map[string]struct{}, len(pointers))
	for _, pointer := range pointers {
		if pointer == "" {
			return nil, errMCPInfoClearPointerRoot
		}
		if len(pointer) > mcpInfoJSONMaxPointerBytes || !utf8.ValidString(pointer) || pointer[0] != '/' {
			return nil, errMCPInfoClearPointer
		}
		rawMembers := strings.Split(pointer[1:], "/")
		if len(rawMembers) > mcpInfoJSONMaxPointerDepth {
			return nil, errMCPInfoJSONLimit
		}
		members := make([]string, len(rawMembers))
		for index, rawMember := range rawMembers {
			member, ok := decodeMCPInfoPointerMember(rawMember)
			if !ok {
				return nil, errMCPInfoClearPointer
			}
			members[index] = member
		}
		canonical := encodeMCPInfoClearPointer(members)
		if canonical != pointer {
			return nil, errMCPInfoClearPointer
		}
		if _, duplicate := seen[canonical]; duplicate {
			return nil, errMCPInfoClearConflict
		}
		seen[canonical] = struct{}{}
		parsed = append(parsed, mcpInfoClearPointer{canonical: canonical, members: members})
	}

	sort.Slice(parsed, func(left, right int) bool {
		return parsed[left].canonical < parsed[right].canonical
	})
	for left := range parsed {
		for right := left + 1; right < len(parsed); right++ {
			if mcpInfoPointerIsAncestor(parsed[left].members, parsed[right].members) || mcpInfoPointerIsAncestor(parsed[right].members, parsed[left].members) {
				return nil, errMCPInfoClearConflict
			}
		}
	}
	return parsed, nil
}

func decodeMCPInfoPointerMember(raw string) (string, bool) {
	var result strings.Builder
	result.Grow(len(raw))
	for index := 0; index < len(raw); index++ {
		if raw[index] != '~' {
			result.WriteByte(raw[index])
			continue
		}
		if index+1 >= len(raw) {
			return "", false
		}
		index++
		switch raw[index] {
		case '0':
			result.WriteByte('~')
		case '1':
			result.WriteByte('/')
		default:
			return "", false
		}
	}
	return result.String(), true
}

func encodeMCPInfoClearPointer(members []string) string {
	var result strings.Builder
	for _, member := range members {
		result.WriteByte('/')
		for _, character := range member {
			switch character {
			case '~':
				result.WriteString("~0")
			case '/':
				result.WriteString("~1")
			default:
				result.WriteRune(character)
			}
		}
	}
	return result.String()
}

func mcpInfoPointerIsAncestor(ancestor, descendant []string) bool {
	if len(ancestor) >= len(descendant) {
		return false
	}
	for index := range ancestor {
		if ancestor[index] != descendant[index] {
			return false
		}
	}
	return true
}

func clearMCPInfoJSONMembers(source map[string]interface{}, pointers []string) (map[string]interface{}, error) {
	if source == nil {
		return nil, errMCPInfoJSONObject
	}
	if err := validateMCPInfoJSONValue(source); err != nil {
		return nil, err
	}
	parsed, err := parseMCPInfoClearPointers(pointers)
	if err != nil {
		return nil, err
	}
	result := cloneMCPInfoJSONObject(source)
	for _, pointer := range parsed {
		current := result
		for index, member := range pointer.members {
			if index == len(pointer.members)-1 {
				delete(current, member)
				break
			}
			child, present := current[member]
			if !present {
				break
			}
			childObject, ok := child.(map[string]interface{})
			if !ok {
				return nil, errMCPInfoClearTraversal
			}
			current = childObject
		}
	}
	return result, nil
}
