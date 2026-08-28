package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	semanticDictionaryProvenanceVersion = 1
	semanticDictionaryMaxPointers       = 1024
	semanticDictionaryMaxPointerDepth   = 128
	semanticDictionaryMaxPointerBytes   = 4096
)

var (
	errSemanticDictionaryObject       = errors.New("semantic dictionary must be a non-null JSON object")
	errSemanticDictionaryValue        = errors.New("semantic dictionary contains an unsupported JSON value")
	errSemanticDictionaryLimit        = errors.New("semantic dictionary exceeds a safety limit")
	errSemanticDictionaryCanonical    = errors.New("semantic dictionary cannot be canonicalized")
	errSemanticDictionaryOverlap      = errors.New("semantic dictionary overlaps another managed surface")
	errSemanticDictionaryPointer      = errors.New("semantic dictionary ownership pointer is invalid")
	errSemanticDictionaryConflict     = errors.New("semantic dictionary ownership pointers conflict")
	errSemanticDictionaryTraversal    = errors.New("semantic dictionary ownership traversal is invalid")
	errSemanticDictionaryMasked       = errors.New("semantic dictionary contains an unrecoverable masked value")
	errSemanticDictionaryMaskPolicy   = errors.New("semantic dictionary mask policy is required")
	errSemanticDictionaryPrivate      = errors.New("semantic dictionary private provenance is invalid")
	errSemanticDictionaryNotCanonical = errors.New("semantic dictionary private provenance is not canonical")
)

type semanticDictionaryPathSet map[string]bool

type semanticDictionaryMaskPredicate func(path []string, value string) bool

type semanticDictionaryProvenance struct {
	Initialized           bool
	Configured            bool
	TerraformOwned        semanticDictionaryPathSet
	APIOwned              semanticDictionaryPathSet
	PendingTerraformOwned semanticDictionaryPathSet
	PendingAPIOwned       semanticDictionaryPathSet
	PendingRemovals       semanticDictionaryPathSet
}

type semanticDictionaryProvenanceWire struct {
	Version               int      `json:"version"`
	Initialized           bool     `json:"initialized"`
	Configured            bool     `json:"configured"`
	TerraformOwned        []string `json:"terraform_owned"`
	APIOwned              []string `json:"api_owned"`
	PendingTerraformOwned []string `json:"pending_terraform_owned"`
	PendingAPIOwned       []string `json:"pending_api_owned"`
	PendingRemovals       []string `json:"pending_removals"`
}

func emptySemanticDictionaryProvenance() semanticDictionaryProvenance {
	return semanticDictionaryProvenance{
		TerraformOwned:        semanticDictionaryPathSet{},
		APIOwned:              semanticDictionaryPathSet{},
		PendingTerraformOwned: semanticDictionaryPathSet{},
		PendingAPIOwned:       semanticDictionaryPathSet{},
		PendingRemovals:       semanticDictionaryPathSet{},
	}
}

func cloneSemanticDictionaryPathSet(source semanticDictionaryPathSet) semanticDictionaryPathSet {
	result := semanticDictionaryPathSet{}
	for pointer, owned := range source {
		if owned {
			result[pointer] = true
		}
	}
	return result
}

func cloneSemanticDictionaryProvenance(ctx context.Context, source semanticDictionaryProvenance) (semanticDictionaryProvenance, error) {
	if err := validateSemanticDictionaryProvenance(ctx, source); err != nil {
		return semanticDictionaryProvenance{}, err
	}
	result := source
	result.TerraformOwned = cloneSemanticDictionaryPathSet(source.TerraformOwned)
	result.APIOwned = cloneSemanticDictionaryPathSet(source.APIOwned)
	result.PendingTerraformOwned = cloneSemanticDictionaryPathSet(source.PendingTerraformOwned)
	result.PendingAPIOwned = cloneSemanticDictionaryPathSet(source.PendingAPIOwned)
	result.PendingRemovals = cloneSemanticDictionaryPathSet(source.PendingRemovals)
	if err := ctx.Err(); err != nil {
		return semanticDictionaryProvenance{}, err
	}
	return result, nil
}

// semanticCreateRecoveryRequired classifies create failures without exposing
// endpoint-specific data. Raw typed status information is inspected before the
// general classifier because cancellation/deadline precedence can intentionally
// hide a known non-2xx response.
func semanticCreateRecoveryRequired(accepted bool, requestErr error) bool {
	if accepted {
		return true
	}
	observedRejection := false
	walkErrorTree(requestErr, func(node error) {
		status := 0
		switch typed := node.(type) {
		case *APIError:
			status = typed.StatusCode
		case *safeResponseError:
			status = typed.statusCode
		}
		if status != 0 && (status < 200 || status >= 300) {
			observedRejection = true
		}
	})
	if observedRejection {
		return false
	}
	return ClassifyHTTPFailure(requestErr).RequestDispatched
}

// parseSemanticDictionary accepts exactly one non-null JSON object. The shared
// decoder supplies duplicate-member rejection, exact numbers, and global input,
// depth, and object-member bounds. Errors never contain input text or keys.
func parseSemanticDictionary(ctx context.Context, raw string) (map[string]interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var decoded interface{}
	decodeErr := decodeJSONUseNumber([]byte(raw), &decoded)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if decodeErr != nil {
		return nil, decodeErr
	}
	object, ok := decoded.(map[string]interface{})
	if !ok || object == nil {
		return nil, errSemanticDictionaryObject
	}
	if err := validateSemanticDictionaryValue(ctx, object); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return object, nil
}

func canonicalizeSemanticDictionary(ctx context.Context, raw string) (string, error) {
	object, err := parseSemanticDictionary(ctx, raw)
	if err != nil {
		return "", err
	}
	return canonicalSemanticDictionary(ctx, object)
}

// reconcileSemanticDictionaryString preserves configured spelling when an
// authoritative object is exactly semantically equal, including exact number
// identity. Actual remote changes use deterministic canonical JSON.
func reconcileSemanticDictionaryString(ctx context.Context, current types.String, object map[string]interface{}) (types.String, error) {
	observed, err := canonicalSemanticDictionary(ctx, object)
	if err != nil {
		return types.StringNull(), err
	}
	if !current.IsNull() && !current.IsUnknown() {
		configured, parseErr := parseSemanticDictionary(ctx, current.ValueString())
		if err := ctx.Err(); err != nil {
			return types.StringNull(), err
		}
		if parseErr == nil {
			equal, compareErr := semanticDictionaryValuesEqual(ctx, configured, object)
			if compareErr != nil {
				return types.StringNull(), compareErr
			}
			if equal {
				return current, nil
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return types.StringNull(), err
	}
	return types.StringValue(observed), nil
}

func canonicalSemanticDictionary(ctx context.Context, object map[string]interface{}) (string, error) {
	if object == nil {
		return "", errSemanticDictionaryObject
	}
	members := 0
	bytesUsed := 0
	canonical, err := canonicalSemanticDictionaryValue(ctx, object, 1, &members, &bytesUsed)
	if err != nil {
		return "", err
	}
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(canonical); err != nil {
		return "", errSemanticDictionaryCanonical
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return strings.TrimSuffix(output.String(), "\n"), nil
}

func canonicalSemanticDictionaryValue(ctx context.Context, value interface{}, depth int, members, bytesUsed *int) (interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if depth > jsonDecodeMaxDepth {
		return nil, errSemanticDictionaryLimit
	}
	switch value := value.(type) {
	case nil:
		*bytesUsed += 4
	case bool:
		*bytesUsed += 5
	case string:
		*bytesUsed += len(value)
	case json.Number:
		canonical, ok := canonicalJSONNumberString(value.String())
		if !ok {
			return nil, errSemanticDictionaryValue
		}
		*bytesUsed += len(canonical)
		if *bytesUsed > jsonDecodeMaxInputBytes {
			return nil, errSemanticDictionaryLimit
		}
		return json.Number(canonical), nil
	case map[string]interface{}:
		*members += len(value)
		if *members > jsonDecodeMaxObjectMembers {
			return nil, errSemanticDictionaryLimit
		}
		result := make(map[string]interface{}, len(value))
		for name, child := range value {
			*bytesUsed += len(name)
			canonical, err := canonicalSemanticDictionaryValue(ctx, child, depth+1, members, bytesUsed)
			if err != nil {
				return nil, err
			}
			result[name] = canonical
		}
		return result, nil
	case []interface{}:
		result := make([]interface{}, len(value))
		for index, child := range value {
			canonical, err := canonicalSemanticDictionaryValue(ctx, child, depth+1, members, bytesUsed)
			if err != nil {
				return nil, err
			}
			result[index] = canonical
		}
		return result, nil
	default:
		return nil, errSemanticDictionaryValue
	}
	if *bytesUsed > jsonDecodeMaxInputBytes {
		return nil, errSemanticDictionaryLimit
	}
	return value, nil
}

func validateSemanticDictionaryValue(ctx context.Context, value interface{}) error {
	members := 0
	bytesUsed := 0
	_, err := canonicalSemanticDictionaryValue(ctx, value, 1, &members, &bytesUsed)
	return err
}

func cloneSemanticDictionaryValue(ctx context.Context, value interface{}) (interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch value := value.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(value))
		for name, child := range value {
			cloned, err := cloneSemanticDictionaryValue(ctx, child)
			if err != nil {
				return nil, err
			}
			result[name] = cloned
		}
		return result, nil
	case []interface{}:
		result := make([]interface{}, len(value))
		for index, child := range value {
			cloned, err := cloneSemanticDictionaryValue(ctx, child)
			if err != nil {
				return nil, err
			}
			result[index] = cloned
		}
		return result, nil
	default:
		return value, nil
	}
}

func cloneSemanticDictionary(ctx context.Context, source map[string]interface{}) (map[string]interface{}, error) {
	if source == nil {
		return nil, errSemanticDictionaryObject
	}
	cloned, err := cloneSemanticDictionaryValue(ctx, source)
	if err != nil {
		return nil, err
	}
	return cloned.(map[string]interface{}), nil
}

func semanticDictionaryValuesEqual(ctx context.Context, left, right interface{}) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	switch left := left.(type) {
	case json.Number:
		right, ok := right.(json.Number)
		return ok && exactJSONNumbersEqual(left, right), nil
	case map[string]interface{}:
		right, ok := right.(map[string]interface{})
		if !ok || len(left) != len(right) {
			return false, nil
		}
		for name, leftValue := range left {
			rightValue, exists := right[name]
			if !exists {
				return false, nil
			}
			equal, err := semanticDictionaryValuesEqual(ctx, leftValue, rightValue)
			if err != nil || !equal {
				return equal, err
			}
		}
		return true, nil
	case []interface{}:
		right, ok := right.([]interface{})
		if !ok || len(left) != len(right) {
			return false, nil
		}
		for index := range left {
			equal, err := semanticDictionaryValuesEqual(ctx, left[index], right[index])
			if err != nil || !equal {
				return equal, err
			}
		}
		return true, nil
	default:
		return exactJSONValuesEqual(left, right), nil
	}
}

// semanticDictionaryLeafPaths returns prefix-free RFC 6901 pointers. Objects
// recurse, while arrays, scalars, null, and empty objects are atomic leaves.
// The root empty object is represented by Configured provenance plus no paths.
func semanticDictionaryLeafPaths(ctx context.Context, object map[string]interface{}) (semanticDictionaryPathSet, error) {
	if object == nil {
		return nil, errSemanticDictionaryObject
	}
	if err := validateSemanticDictionaryValue(ctx, object); err != nil {
		return nil, err
	}
	result := semanticDictionaryPathSet{}
	if err := collectSemanticDictionaryLeafPaths(ctx, object, nil, result); err != nil {
		return nil, err
	}
	return result, nil
}

func collectSemanticDictionaryLeafPaths(ctx context.Context, object map[string]interface{}, prefix []string, result semanticDictionaryPathSet) error {
	if len(result) > semanticDictionaryMaxPointers {
		return errSemanticDictionaryLimit
	}
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		members := append(append([]string{}, prefix...), name)
		value := object[name]
		if nested, ok := value.(map[string]interface{}); ok && len(nested) > 0 {
			if err := collectSemanticDictionaryLeafPaths(ctx, nested, members, result); err != nil {
				return err
			}
			continue
		}
		pointer, err := encodeSemanticDictionaryPointer(members)
		if err != nil {
			return err
		}
		result[pointer] = true
		if len(result) > semanticDictionaryMaxPointers {
			return errSemanticDictionaryLimit
		}
	}
	return nil
}

func encodeSemanticDictionaryPointer(members []string) (string, error) {
	if len(members) == 0 || len(members) > semanticDictionaryMaxPointerDepth {
		return "", errSemanticDictionaryPointer
	}
	var result strings.Builder
	for _, member := range members {
		if !utf8.ValidString(member) {
			return "", errSemanticDictionaryPointer
		}
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
	if result.Len() > semanticDictionaryMaxPointerBytes {
		return "", errSemanticDictionaryPointer
	}
	return result.String(), nil
}

func decodeSemanticDictionaryPointer(pointer string) ([]string, error) {
	if pointer == "" || len(pointer) > semanticDictionaryMaxPointerBytes || !utf8.ValidString(pointer) || pointer[0] != '/' {
		return nil, errSemanticDictionaryPointer
	}
	rawMembers := strings.Split(pointer[1:], "/")
	if len(rawMembers) == 0 || len(rawMembers) > semanticDictionaryMaxPointerDepth {
		return nil, errSemanticDictionaryPointer
	}
	members := make([]string, len(rawMembers))
	for index, raw := range rawMembers {
		var decoded strings.Builder
		for offset := 0; offset < len(raw); offset++ {
			if raw[offset] != '~' {
				decoded.WriteByte(raw[offset])
				continue
			}
			if offset+1 >= len(raw) {
				return nil, errSemanticDictionaryPointer
			}
			offset++
			switch raw[offset] {
			case '0':
				decoded.WriteByte('~')
			case '1':
				decoded.WriteByte('/')
			default:
				return nil, errSemanticDictionaryPointer
			}
		}
		members[index] = decoded.String()
	}
	canonical, err := encodeSemanticDictionaryPointer(members)
	if err != nil || canonical != pointer {
		return nil, errSemanticDictionaryPointer
	}
	return members, nil
}

type semanticDictionaryPathTrie struct {
	terminal bool
	children map[string]*semanticDictionaryPathTrie
}

func (trie *semanticDictionaryPathTrie) insert(members []string) bool {
	current := trie
	for _, member := range members {
		if current.terminal {
			return false
		}
		if current.children == nil {
			current.children = map[string]*semanticDictionaryPathTrie{}
		}
		if current.children[member] == nil {
			current.children[member] = &semanticDictionaryPathTrie{}
		}
		current = current.children[member]
	}
	if current.terminal || len(current.children) != 0 {
		return false
	}
	current.terminal = true
	return true
}

func (trie *semanticDictionaryPathTrie) conflicts(members []string) bool {
	current := trie
	for _, member := range members {
		if current.terminal {
			return true
		}
		current = current.children[member]
		if current == nil {
			return false
		}
	}
	return current.terminal || len(current.children) != 0
}

func validateSemanticDictionaryPathSet(ctx context.Context, paths semanticDictionaryPathSet) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if paths == nil || len(paths) > semanticDictionaryMaxPointers {
		return errSemanticDictionaryPrivate
	}
	trie := &semanticDictionaryPathTrie{}
	for pointer, owned := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !owned {
			return errSemanticDictionaryPrivate
		}
		members, err := decodeSemanticDictionaryPointer(pointer)
		if err != nil {
			return errSemanticDictionaryPrivate
		}
		if !trie.insert(members) {
			return errSemanticDictionaryConflict
		}
	}
	return nil
}

func semanticDictionaryPathSetsConflict(ctx context.Context, left, right semanticDictionaryPathSet) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	trie := &semanticDictionaryPathTrie{}
	for pointer := range left {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		members, err := decodeSemanticDictionaryPointer(pointer)
		if err != nil || !trie.insert(members) {
			return true, nil
		}
	}
	for pointer := range right {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		members, err := decodeSemanticDictionaryPointer(pointer)
		if err != nil || trie.conflicts(members) {
			return true, nil
		}
	}
	return false, nil
}

func sortedSemanticDictionaryPaths(paths semanticDictionaryPathSet) []string {
	result := make([]string, 0, len(paths))
	for pointer, owned := range paths {
		if owned {
			result = append(result, pointer)
		}
	}
	sort.Strings(result)
	return result
}

func semanticDictionaryPathSetFromSlice(ctx context.Context, pointers []string) (semanticDictionaryPathSet, error) {
	if pointers == nil || len(pointers) > semanticDictionaryMaxPointers {
		return nil, errSemanticDictionaryPrivate
	}
	result := semanticDictionaryPathSet{}
	for _, pointer := range pointers {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if result[pointer] {
			return nil, errSemanticDictionaryPrivate
		}
		result[pointer] = true
	}
	if err := validateSemanticDictionaryPathSet(ctx, result); err != nil {
		return nil, err
	}
	return result, nil
}

func validateSemanticDictionaryProvenance(ctx context.Context, value semanticDictionaryProvenance) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	allPaths := []semanticDictionaryPathSet{value.TerraformOwned, value.APIOwned, value.PendingTerraformOwned, value.PendingAPIOwned, value.PendingRemovals}
	totalPointers := 0
	for _, paths := range allPaths {
		totalPointers += len(paths)
		if totalPointers > semanticDictionaryMaxPointers {
			return errSemanticDictionaryPrivate
		}
		if err := validateSemanticDictionaryPathSet(ctx, paths); err != nil {
			return err
		}
	}
	committedConflict, err := semanticDictionaryPathSetsConflict(ctx, value.TerraformOwned, value.APIOwned)
	if err != nil {
		return err
	}
	pendingConflict, err := semanticDictionaryPathSetsConflict(ctx, value.PendingTerraformOwned, value.PendingAPIOwned)
	if err != nil {
		return err
	}
	if committedConflict || pendingConflict {
		return errSemanticDictionaryPrivate
	}
	for pointer := range value.PendingRemovals {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !value.TerraformOwned[pointer] {
			return errSemanticDictionaryPrivate
		}
	}
	if !value.Initialized && (value.Configured || len(value.TerraformOwned) != 0 || len(value.APIOwned) != 0 || len(value.PendingTerraformOwned) != 0 || len(value.PendingAPIOwned) != 0 || len(value.PendingRemovals) != 0) {
		return errSemanticDictionaryPrivate
	}
	return nil
}

func encodeSemanticDictionaryProvenance(ctx context.Context, value semanticDictionaryProvenance) ([]byte, error) {
	if err := validateSemanticDictionaryProvenance(ctx, value); err != nil {
		return nil, err
	}
	wire := semanticDictionaryProvenanceWire{
		Version:               semanticDictionaryProvenanceVersion,
		Initialized:           value.Initialized,
		Configured:            value.Configured,
		TerraformOwned:        sortedSemanticDictionaryPaths(value.TerraformOwned),
		APIOwned:              sortedSemanticDictionaryPaths(value.APIOwned),
		PendingTerraformOwned: sortedSemanticDictionaryPaths(value.PendingTerraformOwned),
		PendingAPIOwned:       sortedSemanticDictionaryPaths(value.PendingAPIOwned),
		PendingRemovals:       sortedSemanticDictionaryPaths(value.PendingRemovals),
	}
	encoded, err := json.Marshal(wire)
	if err != nil || len(encoded) > jsonDecodeMaxInputBytes {
		return nil, errSemanticDictionaryPrivate
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return encoded, nil
}

func decodeSemanticDictionaryProvenance(ctx context.Context, raw []byte) (semanticDictionaryProvenance, error) {
	if err := ctx.Err(); err != nil {
		return semanticDictionaryProvenance{}, err
	}
	if raw == nil || len(raw) > jsonDecodeMaxInputBytes {
		return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
	}
	var wire semanticDictionaryProvenanceWire
	decodeErr := decodeJSONUseNumber(raw, &wire)
	if err := ctx.Err(); err != nil {
		return semanticDictionaryProvenance{}, err
	}
	if decodeErr != nil || wire.Version != semanticDictionaryProvenanceVersion {
		return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
	}
	value := semanticDictionaryProvenance{Initialized: wire.Initialized, Configured: wire.Configured}
	var err error
	if value.TerraformOwned, err = semanticDictionaryPathSetFromSlice(ctx, wire.TerraformOwned); err != nil {
		return semanticDictionaryProvenance{}, semanticDictionaryPrivateDecodeError(ctx)
	}
	if value.APIOwned, err = semanticDictionaryPathSetFromSlice(ctx, wire.APIOwned); err != nil {
		return semanticDictionaryProvenance{}, semanticDictionaryPrivateDecodeError(ctx)
	}
	if value.PendingTerraformOwned, err = semanticDictionaryPathSetFromSlice(ctx, wire.PendingTerraformOwned); err != nil {
		return semanticDictionaryProvenance{}, semanticDictionaryPrivateDecodeError(ctx)
	}
	if value.PendingAPIOwned, err = semanticDictionaryPathSetFromSlice(ctx, wire.PendingAPIOwned); err != nil {
		return semanticDictionaryProvenance{}, semanticDictionaryPrivateDecodeError(ctx)
	}
	if value.PendingRemovals, err = semanticDictionaryPathSetFromSlice(ctx, wire.PendingRemovals); err != nil {
		return semanticDictionaryProvenance{}, semanticDictionaryPrivateDecodeError(ctx)
	}
	if err := validateSemanticDictionaryProvenance(ctx, value); err != nil {
		return semanticDictionaryProvenance{}, err
	}
	canonical, err := encodeSemanticDictionaryProvenance(ctx, value)
	if err != nil {
		return semanticDictionaryProvenance{}, err
	}
	if !bytes.Equal(raw, canonical) {
		return semanticDictionaryProvenance{}, errSemanticDictionaryNotCanonical
	}
	return value, nil
}

func semanticDictionaryPrivateDecodeError(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return errSemanticDictionaryPrivate
}

// semanticDictionaryTopLevelOverlap rejects overlap without returning key names.
func semanticDictionaryTopLevelOverlap(ctx context.Context, object map[string]interface{}, legacyKeys, reservedKeys []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	occupied := make(map[string]bool, len(legacyKeys)+len(reservedKeys))
	for _, name := range legacyKeys {
		if err := ctx.Err(); err != nil {
			return err
		}
		occupied[name] = true
	}
	for _, name := range reservedKeys {
		if err := ctx.Err(); err != nil {
			return err
		}
		occupied[name] = true
	}
	for name := range object {
		if err := ctx.Err(); err != nil {
			return err
		}
		if occupied[name] {
			return errSemanticDictionaryOverlap
		}
	}
	return nil
}

// applySemanticDictionary removes previously Terraform-owned leaves, pruning
// now-empty structural parents, then overlays the configured document. It
// returns the complete replacement object and the new configured leaf paths.
func applySemanticDictionary(ctx context.Context, base, configured map[string]interface{}, priorOwned semanticDictionaryPathSet) (map[string]interface{}, semanticDictionaryPathSet, error) {
	if base == nil || configured == nil {
		return nil, nil, errSemanticDictionaryObject
	}
	if err := validateSemanticDictionaryValue(ctx, base); err != nil {
		return nil, nil, err
	}
	if err := validateSemanticDictionaryValue(ctx, configured); err != nil {
		return nil, nil, err
	}
	if err := validateSemanticDictionaryPathSet(ctx, priorOwned); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, nil, contextErr
		}
		return nil, nil, errSemanticDictionaryPrivate
	}
	result, err := cloneSemanticDictionary(ctx, base)
	if err != nil {
		return nil, nil, err
	}
	for _, pointer := range sortedSemanticDictionaryPaths(priorOwned) {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		members, err := decodeSemanticDictionaryPointer(pointer)
		if err != nil {
			return nil, nil, errSemanticDictionaryPrivate
		}
		if _, err := removeSemanticDictionaryPath(ctx, result, members); err != nil {
			return nil, nil, err
		}
	}
	result, err = overlaySemanticDictionaryObject(ctx, result, configured)
	if err != nil {
		return nil, nil, err
	}
	owned, err := semanticDictionaryLeafPaths(ctx, configured)
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return result, owned, nil
}

func removeSemanticDictionaryPath(ctx context.Context, object map[string]interface{}, members []string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if len(members) == 0 {
		return false, errSemanticDictionaryTraversal
	}
	name := members[0]
	value, exists := object[name]
	if !exists {
		return len(object) == 0, nil
	}
	if len(members) == 1 {
		delete(object, name)
		return len(object) == 0, nil
	}
	nested, ok := value.(map[string]interface{})
	if !ok {
		return false, errSemanticDictionaryTraversal
	}
	empty, err := removeSemanticDictionaryPath(ctx, nested, members[1:])
	if err != nil {
		return false, err
	}
	if empty {
		delete(object, name)
	}
	return len(object) == 0, nil
}

func overlaySemanticDictionaryObject(ctx context.Context, base, configured map[string]interface{}) (map[string]interface{}, error) {
	result, err := cloneSemanticDictionary(ctx, base)
	if err != nil {
		return nil, err
	}
	for name, configuredValue := range configured {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		configuredObject, configuredIsObject := configuredValue.(map[string]interface{})
		baseObject, baseIsObject := base[name].(map[string]interface{})
		if configuredIsObject && len(configuredObject) > 0 && baseIsObject {
			result[name], err = overlaySemanticDictionaryObject(ctx, baseObject, configuredObject)
			if err != nil {
				return nil, err
			}
			continue
		}
		result[name], err = cloneSemanticDictionaryValue(ctx, configuredValue)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// restoreSemanticDictionaryMaskedValues restores endpoint-specific masking
// sentinels only from a same-shaped authoritative prior document. A masked
// parent, shape change, missing prior plaintext, or missing endpoint policy
// fails atomically. The predicate receives an unescaped object/array path.
func restoreSemanticDictionaryMaskedValues(ctx context.Context, prior, observed map[string]interface{}, priorAuthoritative bool, isMasked semanticDictionaryMaskPredicate) (map[string]interface{}, error) {
	if observed == nil {
		return nil, errSemanticDictionaryObject
	}
	if isMasked == nil {
		return nil, errSemanticDictionaryMaskPolicy
	}
	if err := validateSemanticDictionaryValue(ctx, observed); err != nil {
		return nil, err
	}
	containsMask, err := semanticDictionaryContainsMaskedValue(ctx, observed, nil, isMasked)
	if err != nil {
		return nil, err
	}
	if !containsMask {
		return cloneSemanticDictionary(ctx, observed)
	}
	if !priorAuthoritative || prior == nil {
		return nil, errSemanticDictionaryMasked
	}
	if err := validateSemanticDictionaryValue(ctx, prior); err != nil {
		return nil, err
	}
	sameShape, err := semanticDictionarySameShape(ctx, prior, observed)
	if err != nil {
		return nil, err
	}
	if !sameShape {
		return nil, errSemanticDictionaryMasked
	}
	restored, err := restoreSemanticDictionaryMaskedValue(ctx, prior, observed, nil, isMasked)
	if err != nil {
		return nil, err
	}
	return restored.(map[string]interface{}), nil
}

func semanticDictionarySameShape(ctx context.Context, left, right interface{}) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	switch left := left.(type) {
	case map[string]interface{}:
		right, ok := right.(map[string]interface{})
		if !ok || len(left) != len(right) {
			return false, nil
		}
		for name, leftValue := range left {
			rightValue, exists := right[name]
			if !exists {
				return false, nil
			}
			same, err := semanticDictionarySameShape(ctx, leftValue, rightValue)
			if err != nil || !same {
				return same, err
			}
		}
		return true, nil
	case []interface{}:
		right, ok := right.([]interface{})
		if !ok || len(left) != len(right) {
			return false, nil
		}
		for index := range left {
			same, err := semanticDictionarySameShape(ctx, left[index], right[index])
			if err != nil || !same {
				return same, err
			}
		}
		return true, nil
	default:
		switch right.(type) {
		case map[string]interface{}, []interface{}:
			return false, nil
		default:
			return true, nil
		}
	}
}

func semanticDictionaryContainsMaskedValue(ctx context.Context, value interface{}, path []string, isMasked semanticDictionaryMaskPredicate) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	switch value := value.(type) {
	case string:
		return isMasked(append([]string{}, path...), value), nil
	case map[string]interface{}:
		for name, child := range value {
			masked, err := semanticDictionaryContainsMaskedValue(ctx, child, append(path, name), isMasked)
			if err != nil || masked {
				return masked, err
			}
		}
	case []interface{}:
		for index, child := range value {
			masked, err := semanticDictionaryContainsMaskedValue(ctx, child, append(path, strconv.Itoa(index)), isMasked)
			if err != nil || masked {
				return masked, err
			}
		}
	}
	return false, nil
}

func restoreSemanticDictionaryMaskedValue(ctx context.Context, prior, observed interface{}, path []string, isMasked semanticDictionaryMaskPredicate) (interface{}, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch observed := observed.(type) {
	case string:
		if !isMasked(append([]string{}, path...), observed) {
			return observed, nil
		}
		priorString, ok := prior.(string)
		if !ok || isMasked(append([]string{}, path...), priorString) {
			return nil, errSemanticDictionaryMasked
		}
		return priorString, nil
	case map[string]interface{}:
		priorObject, ok := prior.(map[string]interface{})
		if !ok || len(priorObject) != len(observed) {
			return nil, errSemanticDictionaryMasked
		}
		result := make(map[string]interface{}, len(observed))
		for name, child := range observed {
			priorChild, exists := priorObject[name]
			if !exists {
				return nil, errSemanticDictionaryMasked
			}
			restored, err := restoreSemanticDictionaryMaskedValue(ctx, priorChild, child, append(path, name), isMasked)
			if err != nil {
				return nil, err
			}
			result[name] = restored
		}
		return result, nil
	case []interface{}:
		priorList, ok := prior.([]interface{})
		if !ok || len(priorList) != len(observed) {
			return nil, errSemanticDictionaryMasked
		}
		result := make([]interface{}, len(observed))
		for index, child := range observed {
			restored, err := restoreSemanticDictionaryMaskedValue(ctx, priorList[index], child, append(path, strconv.Itoa(index)), isMasked)
			if err != nil {
				return nil, err
			}
			result[index] = restored
		}
		return result, nil
	default:
		return observed, nil
	}
}
