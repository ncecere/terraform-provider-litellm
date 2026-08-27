package provider

import (
	"context"
	"errors"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// collectionValueState records the Terraform collection's outer state without
// conflating an omitted value with an unknown or explicitly empty value.
type collectionValueState uint8

const (
	collectionValueNull collectionValueState = iota
	collectionValueUnknown
	collectionValueEmpty
	collectionValuePopulated
)

const (
	invalidTerraformStringListSummary = "Invalid Terraform String List"
	invalidTerraformStringSetSummary  = "Invalid Terraform String Set"
	invalidTerraformStringMapSummary  = "Invalid Terraform String Map"
	invalidTerraformInt64MapSummary   = "Invalid Terraform Integer Map"
	invalidTerraformFloat64MapSummary = "Invalid Terraform Number Map"
	invalidTerraformSecuritySummary   = "Invalid Terraform Security Collection"
	invalidAPIStringListSummary       = "Invalid API String List"
	invalidAPIStringMapSummary        = "Invalid API String Map"
	collectionConversionCanceled      = "Collection Conversion Canceled"

	invalidTerraformStringListDetail   = "The collection must contain only known, non-null string elements. No collection value was converted."
	invalidTerraformStringSetDetail    = "The collection must contain only known, non-null string elements. No collection value was converted."
	invalidTerraformStringMapDetail    = "The collection must contain only known, non-null string elements. No collection value was converted."
	invalidTerraformInt64MapDetail     = "The collection must contain only known, non-null integer elements. No collection value was converted."
	invalidTerraformFloat64MapDetail   = "The collection must contain only known, non-null number elements. No collection value was converted."
	invalidTerraformSecurityDetail     = "The collection must contain only known, non-null maps of known, non-null string lists. No collection value was converted."
	invalidAPIStringListDetail         = "LiteLLM returned a collection that cannot be represented as a list of strings. No collection value was projected."
	invalidAPIStringMapDetail          = "LiteLLM returned a collection that cannot be represented as a map of strings. No collection value was projected."
	collectionConversionCanceledDetail = "The collection conversion was canceled before it completed. No collection value was converted or projected."
)

// strictTerraformStringList performs validation and conversion in separate
// passes. An invalid element therefore never produces a partial Go slice.
// List diagnostics identify at most an index and never include element content.
func strictTerraformStringList(ctx context.Context, value types.List, valuePath path.Path) ([]string, collectionValueState, diag.Diagnostics) {
	state := listCollectionState(value)
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return nil, state, diagnostics
	}
	if value.IsNull() || value.IsUnknown() {
		return nil, state, nil
	}

	var diagnostics diag.Diagnostics
	if !value.ElementType(ctx).Equal(types.StringType) {
		diagnostics.AddAttributeError(valuePath, invalidTerraformStringListSummary, invalidTerraformStringListDetail)
		return nil, state, diagnostics
	}
	elements := value.Elements()
	for index, element := range elements {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			diagnostics.Append(canceled...)
			return nil, state, diagnostics
		}
		stringValue, ok := element.(types.String)
		if !ok || stringValue.IsNull() || stringValue.IsUnknown() {
			diagnostics.AddAttributeError(valuePath.AtListIndex(index), invalidTerraformStringListSummary, invalidTerraformStringListDetail)
		}
	}
	if diagnostics.HasError() {
		return nil, state, diagnostics
	}

	converted := make([]string, len(elements))
	for index, element := range elements {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			return nil, state, canceled
		}
		converted[index] = element.(types.String).ValueString()
	}
	if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
		return nil, state, canceled
	}
	return converted, state, nil
}

// strictTerraformStringSet performs validation and conversion in separate
// passes. Set element paths contain the element value, so malformed set
// diagnostics are deliberately rooted at the supplied collection path.
func strictTerraformStringSet(ctx context.Context, value types.Set, valuePath path.Path) ([]string, collectionValueState, diag.Diagnostics) {
	state := setCollectionState(value)
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return nil, state, diagnostics
	}
	if value.IsNull() || value.IsUnknown() {
		return nil, state, nil
	}

	var diagnostics diag.Diagnostics
	if !value.ElementType(ctx).Equal(types.StringType) {
		diagnostics.AddAttributeError(valuePath, invalidTerraformStringSetSummary, invalidTerraformStringSetDetail)
		return nil, state, diagnostics
	}
	elements := value.Elements()
	for _, element := range elements {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			diagnostics.Append(canceled...)
			return nil, state, diagnostics
		}
		stringValue, ok := element.(types.String)
		if !ok || stringValue.IsNull() || stringValue.IsUnknown() {
			diagnostics.AddAttributeError(valuePath, invalidTerraformStringSetSummary, invalidTerraformStringSetDetail)
		}
	}
	if diagnostics.HasError() {
		return nil, state, diagnostics
	}

	converted := make([]string, len(elements))
	for index, element := range elements {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			return nil, state, canceled
		}
		converted[index] = element.(types.String).ValueString()
	}
	if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
		return nil, state, canceled
	}
	return converted, state, nil
}

// strictTerraformStringMap performs validation and conversion in separate
// passes. Keys may be attached to diagnostics only when the caller establishes
// that the map is not sensitive.
func strictTerraformStringMap(ctx context.Context, value types.Map, valuePath path.Path, sensitive bool) (map[string]string, collectionValueState, diag.Diagnostics) {
	state := mapCollectionState(value)
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return nil, state, diagnostics
	}
	if value.IsNull() || value.IsUnknown() {
		return nil, state, nil
	}

	var diagnostics diag.Diagnostics
	if !value.ElementType(ctx).Equal(types.StringType) {
		diagnostics.AddAttributeError(valuePath, invalidTerraformStringMapSummary, invalidTerraformStringMapDetail)
		return nil, state, diagnostics
	}
	elements := value.Elements()
	keys := sortedAttributeKeys(elements)
	for _, key := range keys {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			diagnostics.Append(canceled...)
			return nil, state, diagnostics
		}
		stringValue, ok := elements[key].(types.String)
		if !ok || stringValue.IsNull() || stringValue.IsUnknown() {
			diagnostics.AddAttributeError(safeMapDiagnosticPath(valuePath, key, sensitive), invalidTerraformStringMapSummary, invalidTerraformStringMapDetail)
		}
	}
	if diagnostics.HasError() {
		return nil, state, diagnostics
	}

	converted := make(map[string]string, len(elements))
	for _, key := range keys {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			return nil, state, canceled
		}
		converted[key] = elements[key].(types.String).ValueString()
	}
	if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
		return nil, state, canceled
	}
	return converted, state, nil
}

// strictTerraformInt64Map and strictTerraformFloat64Map preserve Terraform's
// outer collection state while rejecting malformed elements atomically.
func strictTerraformInt64Map(ctx context.Context, value types.Map, valuePath path.Path) (map[string]int64, collectionValueState, diag.Diagnostics) {
	state := mapCollectionState(value)
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return nil, state, diagnostics
	}
	if value.IsNull() || value.IsUnknown() {
		return nil, state, nil
	}
	var diagnostics diag.Diagnostics
	if !value.ElementType(ctx).Equal(types.Int64Type) {
		diagnostics.AddAttributeError(valuePath, invalidTerraformInt64MapSummary, invalidTerraformInt64MapDetail)
		return nil, state, diagnostics
	}
	keys := sortedAttributeKeys(value.Elements())
	for _, key := range keys {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			return nil, state, canceled
		}
		number, ok := value.Elements()[key].(types.Int64)
		if !ok || number.IsNull() || number.IsUnknown() {
			diagnostics.AddAttributeError(valuePath.AtMapKey(key), invalidTerraformInt64MapSummary, invalidTerraformInt64MapDetail)
		}
	}
	if diagnostics.HasError() {
		return nil, state, diagnostics
	}
	result := make(map[string]int64, len(keys))
	for _, key := range keys {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			return nil, state, canceled
		}
		result[key] = value.Elements()[key].(types.Int64).ValueInt64()
	}
	if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
		return nil, state, canceled
	}
	return result, state, nil
}

func strictTerraformFloat64Map(ctx context.Context, value types.Map, valuePath path.Path) (map[string]float64, collectionValueState, diag.Diagnostics) {
	state := mapCollectionState(value)
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return nil, state, diagnostics
	}
	if value.IsNull() || value.IsUnknown() {
		return nil, state, nil
	}
	var diagnostics diag.Diagnostics
	if !value.ElementType(ctx).Equal(types.Float64Type) {
		diagnostics.AddAttributeError(valuePath, invalidTerraformFloat64MapSummary, invalidTerraformFloat64MapDetail)
		return nil, state, diagnostics
	}
	keys := sortedAttributeKeys(value.Elements())
	for _, key := range keys {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			return nil, state, canceled
		}
		number, ok := value.Elements()[key].(types.Float64)
		if !ok || number.IsNull() || number.IsUnknown() {
			diagnostics.AddAttributeError(valuePath.AtMapKey(key), invalidTerraformFloat64MapSummary, invalidTerraformFloat64MapDetail)
		}
	}
	if diagnostics.HasError() {
		return nil, state, diagnostics
	}
	result := make(map[string]float64, len(keys))
	for _, key := range keys {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			return nil, state, canceled
		}
		result[key] = value.Elements()[key].(types.Float64).ValueFloat64()
	}
	if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
		return nil, state, canceled
	}
	return result, state, nil
}

// strictTerraformStringListMapList converts list(map(list(string))) values in
// two passes. Map keys and element contents are never included in diagnostics.
func strictTerraformStringListMapList(ctx context.Context, value types.List, valuePath path.Path) ([]map[string][]string, collectionValueState, diag.Diagnostics) {
	state := listCollectionState(value)
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return nil, state, diagnostics
	}
	if value.IsNull() || value.IsUnknown() {
		return nil, state, nil
	}
	wantType := types.MapType{ElemType: types.ListType{ElemType: types.StringType}}
	var diagnostics diag.Diagnostics
	if !value.ElementType(ctx).Equal(wantType) {
		diagnostics.AddAttributeError(valuePath, invalidTerraformSecuritySummary, invalidTerraformSecurityDetail)
		return nil, state, diagnostics
	}
	for index, raw := range value.Elements() {
		itemPath := valuePath.AtListIndex(index)
		mapping, ok := raw.(types.Map)
		if !ok || mapping.IsNull() || mapping.IsUnknown() || !mapping.ElementType(ctx).Equal(types.ListType{ElemType: types.StringType}) {
			diagnostics.AddAttributeError(itemPath, invalidTerraformSecuritySummary, invalidTerraformSecurityDetail)
			continue
		}
		for _, key := range sortedAttributeKeys(mapping.Elements()) {
			scopes, ok := mapping.Elements()[key].(types.List)
			if !ok || scopes.IsNull() || scopes.IsUnknown() || !scopes.ElementType(ctx).Equal(types.StringType) {
				diagnostics.AddAttributeError(itemPath, invalidTerraformSecuritySummary, invalidTerraformSecurityDetail)
				continue
			}
			for _, scope := range scopes.Elements() {
				stringValue, ok := scope.(types.String)
				if !ok || stringValue.IsNull() || stringValue.IsUnknown() {
					diagnostics.AddAttributeError(itemPath, invalidTerraformSecuritySummary, invalidTerraformSecurityDetail)
				}
			}
		}
	}
	if diagnostics.HasError() {
		return nil, state, diagnostics
	}
	result := make([]map[string][]string, len(value.Elements()))
	for index, raw := range value.Elements() {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			return nil, state, canceled
		}
		mapping := raw.(types.Map)
		item := make(map[string][]string, len(mapping.Elements()))
		for _, key := range sortedAttributeKeys(mapping.Elements()) {
			scopes := mapping.Elements()[key].(types.List)
			converted := make([]string, len(scopes.Elements()))
			for scopeIndex, scope := range scopes.Elements() {
				converted[scopeIndex] = scope.(types.String).ValueString()
			}
			item[key] = converted
		}
		result[index] = item
	}
	if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
		return nil, state, canceled
	}
	return result, state, nil
}

// strictAPIStringList projects one API object field to Terraform while keeping
// absent and explicit-null presence separate in the return value.
func strictAPIStringList(ctx context.Context, object map[string]interface{}, field string, valuePath path.Path) (types.List, apiValuePresence, diag.Diagnostics) {
	raw, presence, err := apiValueAt(object, field)
	if err != nil {
		return apiStringListFailure(valuePath, presence)
	}
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return types.ListNull(types.StringType), presence, diagnostics
	}
	if presence != apiValuePresent {
		return types.ListNull(types.StringType), presence, nil
	}

	var values []interface{}
	switch typed := raw.(type) {
	case []interface{}:
		values = typed
	case []string:
		values = make([]interface{}, len(typed))
		for index := range typed {
			values[index] = typed[index]
		}
	default:
		return apiStringListFailure(valuePath, presence)
	}

	var diagnostics diag.Diagnostics
	for index, rawElement := range values {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			diagnostics.Append(canceled...)
			return types.ListNull(types.StringType), presence, diagnostics
		}
		if _, ok := rawElement.(string); !ok {
			diagnostics.AddAttributeError(valuePath.AtListIndex(index), invalidAPIStringListSummary, invalidAPIStringListDetail)
		}
	}
	if diagnostics.HasError() {
		return types.ListNull(types.StringType), presence, diagnostics
	}

	elements := make([]attr.Value, len(values))
	for index, rawElement := range values {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			return types.ListNull(types.StringType), presence, canceled
		}
		elements[index] = types.StringValue(rawElement.(string))
	}
	result, constructorDiagnostics := types.ListValue(types.StringType, elements)
	if len(constructorDiagnostics) != 0 {
		// Framework diagnostics can contain raw values. Replace them with a
		// stable, content-free diagnostic instead of forwarding them.
		return apiStringListFailure(valuePath, presence)
	}
	if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
		return types.ListNull(types.StringType), presence, canceled
	}
	return result, presence, nil
}

// strictAPIStringMap projects one API object field to Terraform while keeping
// absent and explicit-null presence separate. The sensitive flag controls
// whether a rejected key may be represented in the diagnostic path.
func strictAPIStringMap(ctx context.Context, object map[string]interface{}, field string, valuePath path.Path, sensitive bool) (types.Map, apiValuePresence, diag.Diagnostics) {
	raw, presence, err := apiValueAt(object, field)
	if err != nil {
		return apiStringMapFailure(valuePath, presence)
	}
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return types.MapNull(types.StringType), presence, diagnostics
	}
	if presence != apiValuePresent {
		return types.MapNull(types.StringType), presence, nil
	}

	values := make(map[string]interface{})
	switch typed := raw.(type) {
	case map[string]interface{}:
		values = typed
	case map[string]string:
		for key, value := range typed {
			values[key] = value
		}
	default:
		return apiStringMapFailure(valuePath, presence)
	}

	var diagnostics diag.Diagnostics
	keys := sortedInterfaceKeys(values)
	for _, key := range keys {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			diagnostics.Append(canceled...)
			return types.MapNull(types.StringType), presence, diagnostics
		}
		if _, ok := values[key].(string); !ok {
			diagnostics.AddAttributeError(safeMapDiagnosticPath(valuePath, key, sensitive), invalidAPIStringMapSummary, invalidAPIStringMapDetail)
		}
	}
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), presence, diagnostics
	}

	elements := make(map[string]attr.Value, len(values))
	for _, key := range keys {
		if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
			return types.MapNull(types.StringType), presence, canceled
		}
		elements[key] = types.StringValue(values[key].(string))
	}
	result, constructorDiagnostics := types.MapValue(types.StringType, elements)
	if len(constructorDiagnostics) != 0 {
		return apiStringMapFailure(valuePath, presence)
	}
	if canceled := canceledCollectionDiagnostics(ctx, valuePath); canceled.HasError() {
		return types.MapNull(types.StringType), presence, canceled
	}
	return result, presence, nil
}

// checkedStringListValue and checkedStringMapValue validate all elements before
// constructing a Terraform value. Constructor diagnostics are replaced with
// stable, content-free diagnostics so callers can fail without leaking values.
func checkedStringListValue(ctx context.Context, elements []attr.Value, valuePath path.Path) (types.List, diag.Diagnostics) {
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return types.ListNull(types.StringType), diagnostics
	}
	var diagnostics diag.Diagnostics
	for index, element := range elements {
		value, ok := element.(types.String)
		if !ok || value.IsNull() || value.IsUnknown() {
			diagnostics.AddAttributeError(valuePath.AtListIndex(index), invalidAPIStringListSummary, invalidAPIStringListDetail)
		}
	}
	if diagnostics.HasError() {
		return types.ListNull(types.StringType), diagnostics
	}
	result, constructorDiagnostics := types.ListValue(types.StringType, elements)
	if len(constructorDiagnostics) != 0 {
		return apiStringListFailureValue(valuePath)
	}
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return types.ListNull(types.StringType), diagnostics
	}
	return result, nil
}

func checkedStringSetValue(ctx context.Context, elements []attr.Value, valuePath path.Path) (types.Set, diag.Diagnostics) {
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return types.SetNull(types.StringType), diagnostics
	}
	var diagnostics diag.Diagnostics
	for _, element := range elements {
		value, ok := element.(types.String)
		if !ok || value.IsNull() || value.IsUnknown() {
			// Set element paths contain values, so keep diagnostics at the
			// collection root to avoid exposing response content.
			diagnostics.AddAttributeError(valuePath, invalidAPIStringListSummary, invalidAPIStringListDetail)
		}
	}
	if diagnostics.HasError() {
		return types.SetNull(types.StringType), diagnostics
	}
	result, constructorDiagnostics := types.SetValue(types.StringType, elements)
	if len(constructorDiagnostics) != 0 {
		return apiStringSetFailureValue(valuePath)
	}
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return types.SetNull(types.StringType), diagnostics
	}
	return result, nil
}

func checkedStringMapValue(ctx context.Context, elements map[string]attr.Value, valuePath path.Path, sensitive bool) (types.Map, diag.Diagnostics) {
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return types.MapNull(types.StringType), diagnostics
	}
	var diagnostics diag.Diagnostics
	for _, key := range sortedAttributeKeys(elements) {
		value, ok := elements[key].(types.String)
		if !ok || value.IsNull() || value.IsUnknown() {
			diagnostics.AddAttributeError(safeMapDiagnosticPath(valuePath, key, sensitive), invalidAPIStringMapSummary, invalidAPIStringMapDetail)
		}
	}
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), diagnostics
	}
	result, constructorDiagnostics := types.MapValue(types.StringType, elements)
	if len(constructorDiagnostics) != 0 {
		return apiStringMapFailureValue(valuePath)
	}
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return types.MapNull(types.StringType), diagnostics
	}
	return result, nil
}

// collectionProjectionError intentionally omits response contents. Attribute
// paths remain available to callers that can append the original diagnostics.
func collectionProjectionError(ctx context.Context, diagnostics diag.Diagnostics) error {
	if !diagnostics.HasError() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return errors.New("LiteLLM returned a malformed collection; response contents were omitted")
}

func listCollectionState(value types.List) collectionValueState {
	switch {
	case value.IsNull():
		return collectionValueNull
	case value.IsUnknown():
		return collectionValueUnknown
	case len(value.Elements()) == 0:
		return collectionValueEmpty
	default:
		return collectionValuePopulated
	}
}

func setCollectionState(value types.Set) collectionValueState {
	switch {
	case value.IsNull():
		return collectionValueNull
	case value.IsUnknown():
		return collectionValueUnknown
	case len(value.Elements()) == 0:
		return collectionValueEmpty
	default:
		return collectionValuePopulated
	}
}

func mapCollectionState(value types.Map) collectionValueState {
	switch {
	case value.IsNull():
		return collectionValueNull
	case value.IsUnknown():
		return collectionValueUnknown
	case len(value.Elements()) == 0:
		return collectionValueEmpty
	default:
		return collectionValuePopulated
	}
}

func canceledCollectionDiagnostics(ctx context.Context, valuePath path.Path) diag.Diagnostics {
	if ctx.Err() == nil {
		return nil
	}
	var diagnostics diag.Diagnostics
	diagnostics.AddAttributeError(valuePath, collectionConversionCanceled, collectionConversionCanceledDetail)
	return diagnostics
}

func safeMapDiagnosticPath(valuePath path.Path, key string, sensitive bool) path.Path {
	if sensitive {
		return valuePath
	}
	return valuePath.AtMapKey(key)
}

func sortedAttributeKeys(values map[string]attr.Value) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedInterfaceKeys(values map[string]interface{}) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func apiStringListFailure(valuePath path.Path, presence apiValuePresence) (types.List, apiValuePresence, diag.Diagnostics) {
	value, diagnostics := apiStringListFailureValue(valuePath)
	return value, presence, diagnostics
}

func apiStringListFailureValue(valuePath path.Path) (types.List, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	diagnostics.AddAttributeError(valuePath, invalidAPIStringListSummary, invalidAPIStringListDetail)
	return types.ListNull(types.StringType), diagnostics
}

func apiStringSetFailureValue(valuePath path.Path) (types.Set, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	diagnostics.AddAttributeError(valuePath, invalidAPIStringListSummary, invalidAPIStringListDetail)
	return types.SetNull(types.StringType), diagnostics
}

func apiStringMapFailure(valuePath path.Path, presence apiValuePresence) (types.Map, apiValuePresence, diag.Diagnostics) {
	value, diagnostics := apiStringMapFailureValue(valuePath)
	return value, presence, diagnostics
}

func apiStringMapFailureValue(valuePath path.Path) (types.Map, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	diagnostics.AddAttributeError(valuePath, invalidAPIStringMapSummary, invalidAPIStringMapDetail)
	return types.MapNull(types.StringType), diagnostics
}
