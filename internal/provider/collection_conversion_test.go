package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestStrictTerraformStringListStatesAndValidation(t *testing.T) {
	t.Parallel()

	valid, validDiagnostics := types.ListValue(types.StringType, []attr.Value{
		types.StringValue("first-secret-value"),
		types.StringValue("second-secret-value"),
	})
	if validDiagnostics.HasError() {
		t.Fatalf("construct valid list: %v", validDiagnostics)
	}
	empty, emptyDiagnostics := types.ListValue(types.StringType, []attr.Value{})
	if emptyDiagnostics.HasError() {
		t.Fatalf("construct empty list: %v", emptyDiagnostics)
	}
	nullElement, nullElementDiagnostics := types.ListValue(types.StringType, []attr.Value{
		types.StringValue("partial-secret-value"),
		types.StringNull(),
	})
	if nullElementDiagnostics.HasError() {
		t.Fatalf("construct null-element list: %v", nullElementDiagnostics)
	}
	unknownElement, unknownElementDiagnostics := types.ListValue(types.StringType, []attr.Value{
		types.StringUnknown(),
	})
	if unknownElementDiagnostics.HasError() {
		t.Fatalf("construct unknown-element list: %v", unknownElementDiagnostics)
	}
	wrongType, wrongTypeDiagnostics := types.ListValue(types.DynamicType, []attr.Value{
		types.DynamicValue(types.StringValue("partial-secret-value")),
		types.DynamicValue(types.Int64Value(42)),
	})
	if wrongTypeDiagnostics.HasError() {
		t.Fatalf("construct dynamic list: %v", wrongTypeDiagnostics)
	}

	tests := []struct {
		name       string
		value      types.List
		want       []string
		wantState  collectionValueState
		wantErrors int
		wantPath   string
	}{
		{name: "null", value: types.ListNull(types.StringType), wantState: collectionValueNull},
		{name: "unknown", value: types.ListUnknown(types.StringType), wantState: collectionValueUnknown},
		{name: "empty", value: empty, want: []string{}, wantState: collectionValueEmpty},
		{name: "populated", value: valid, want: []string{"first-secret-value", "second-secret-value"}, wantState: collectionValuePopulated},
		{name: "null element", value: nullElement, wantState: collectionValuePopulated, wantErrors: 1, wantPath: `items[1]`},
		{name: "unknown element", value: unknownElement, wantState: collectionValuePopulated, wantErrors: 1, wantPath: `items[0]`},
		{name: "wrong element type", value: wrongType, wantState: collectionValuePopulated, wantErrors: 1, wantPath: "items"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, state, diagnostics := strictTerraformStringList(context.Background(), test.value, path.Root("items"))
			assertStringSlice(t, got, test.want)
			if state != test.wantState {
				t.Fatalf("state = %d, want %d", state, test.wantState)
			}
			assertCollectionDiagnostics(t, diagnostics, test.wantErrors, test.wantPath)
			if test.wantErrors > 0 && got != nil {
				t.Fatalf("malformed list returned a partial value: %#v", got)
			}
		})
	}
}

func TestStrictTerraformStringSetStatesAndValidation(t *testing.T) {
	t.Parallel()

	valid, diagnostics := types.SetValue(types.StringType, []attr.Value{types.StringValue("one"), types.StringValue("two")})
	if diagnostics.HasError() {
		t.Fatalf("construct valid set: %v", diagnostics)
	}
	empty, diagnostics := types.SetValue(types.StringType, []attr.Value{})
	if diagnostics.HasError() {
		t.Fatalf("construct empty set: %v", diagnostics)
	}
	nullElement, diagnostics := types.SetValue(types.StringType, []attr.Value{types.StringValue("partial-secret-value"), types.StringNull()})
	if diagnostics.HasError() {
		t.Fatalf("construct null-element set: %v", diagnostics)
	}
	unknownElement, diagnostics := types.SetValue(types.StringType, []attr.Value{types.StringValue("partial-secret-value"), types.StringUnknown()})
	if diagnostics.HasError() {
		t.Fatalf("construct unknown-element set: %v", diagnostics)
	}
	wrongType, diagnostics := types.SetValue(types.DynamicType, []attr.Value{types.DynamicValue(types.Int64Value(42))})
	if diagnostics.HasError() {
		t.Fatalf("construct wrong-type set: %v", diagnostics)
	}

	tests := []struct {
		name       string
		value      types.Set
		wantState  collectionValueState
		wantLength int
		wantErrors int
	}{
		{name: "null", value: types.SetNull(types.StringType), wantState: collectionValueNull},
		{name: "unknown", value: types.SetUnknown(types.StringType), wantState: collectionValueUnknown},
		{name: "empty", value: empty, wantState: collectionValueEmpty},
		{name: "populated", value: valid, wantState: collectionValuePopulated, wantLength: 2},
		{name: "null element", value: nullElement, wantState: collectionValuePopulated, wantErrors: 1},
		{name: "unknown element", value: unknownElement, wantState: collectionValuePopulated, wantErrors: 1},
		{name: "wrong element type", value: wrongType, wantState: collectionValuePopulated, wantErrors: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, state, gotDiagnostics := strictTerraformStringSet(context.Background(), test.value, path.Root("items"))
			if state != test.wantState {
				t.Fatalf("state = %d, want %d", state, test.wantState)
			}
			if len(got) != test.wantLength {
				t.Fatalf("value length = %d, want %d", len(got), test.wantLength)
			}
			if test.wantErrors == 0 && (test.wantState == collectionValueEmpty || test.wantState == collectionValuePopulated) && got == nil {
				t.Fatal("known set was returned as nil")
			}
			assertCollectionDiagnostics(t, gotDiagnostics, test.wantErrors, map[bool]string{true: "items", false: ""}[test.wantErrors > 0])
			if test.wantErrors > 0 && got != nil {
				t.Fatalf("malformed set returned a partial value: %#v", got)
			}
		})
	}
}

func TestStrictTerraformStringMapStatesSensitivityAndValidation(t *testing.T) {
	t.Parallel()

	valid, diagnostics := types.MapValue(types.StringType, map[string]attr.Value{"ordinary": types.StringValue("secret-value")})
	if diagnostics.HasError() {
		t.Fatalf("construct valid map: %v", diagnostics)
	}
	empty, diagnostics := types.MapValue(types.StringType, map[string]attr.Value{})
	if diagnostics.HasError() {
		t.Fatalf("construct empty map: %v", diagnostics)
	}
	nullElement, diagnostics := types.MapValue(types.StringType, map[string]attr.Value{
		"good":             types.StringValue("partial-secret-value"),
		"credential-id-42": types.StringNull(),
	})
	if diagnostics.HasError() {
		t.Fatalf("construct null-element map: %v", diagnostics)
	}
	unknownElement, diagnostics := types.MapValue(types.StringType, map[string]attr.Value{
		"credential-id-42": types.StringUnknown(),
	})
	if diagnostics.HasError() {
		t.Fatalf("construct unknown-element map: %v", diagnostics)
	}
	wrongType, diagnostics := types.MapValue(types.DynamicType, map[string]attr.Value{
		"credential-id-42": types.DynamicValue(types.Int64Value(42)),
	})
	if diagnostics.HasError() {
		t.Fatalf("construct wrong-type map: %v", diagnostics)
	}

	tests := []struct {
		name       string
		value      types.Map
		sensitive  bool
		wantState  collectionValueState
		want       map[string]string
		wantErrors int
		wantPath   string
	}{
		{name: "null", value: types.MapNull(types.StringType), wantState: collectionValueNull},
		{name: "unknown", value: types.MapUnknown(types.StringType), wantState: collectionValueUnknown},
		{name: "empty", value: empty, want: map[string]string{}, wantState: collectionValueEmpty},
		{name: "populated", value: valid, want: map[string]string{"ordinary": "secret-value"}, wantState: collectionValuePopulated},
		{name: "ordinary null element exposes key", value: nullElement, wantState: collectionValuePopulated, wantErrors: 1, wantPath: `metadata["credential-id-42"]`},
		{name: "ordinary unknown element exposes key", value: unknownElement, wantState: collectionValuePopulated, wantErrors: 1, wantPath: `metadata["credential-id-42"]`},
		{name: "wrong element type uses root", value: wrongType, wantState: collectionValuePopulated, wantErrors: 1, wantPath: "metadata"},
		{name: "sensitive malformed map uses root", value: nullElement, sensitive: true, wantState: collectionValuePopulated, wantErrors: 1, wantPath: "metadata"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, state, gotDiagnostics := strictTerraformStringMap(context.Background(), test.value, path.Root("metadata"), test.sensitive)
			assertStringMap(t, got, test.want)
			if state != test.wantState {
				t.Fatalf("state = %d, want %d", state, test.wantState)
			}
			assertCollectionDiagnostics(t, gotDiagnostics, test.wantErrors, test.wantPath)
			if test.wantErrors > 0 && got != nil {
				t.Fatalf("malformed map returned a partial value: %#v", got)
			}
		})
	}
}

func TestStrictAPIStringListPresenceValidationAndContentSafety(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		object     map[string]interface{}
		want       []string
		wantNull   bool
		presence   apiValuePresence
		wantErrors int
		wantPath   string
	}{
		{name: "absent", object: map[string]interface{}{}, wantNull: true, presence: apiValueAbsent},
		{name: "explicit null", object: map[string]interface{}{"items": nil}, wantNull: true, presence: apiValueNull},
		{name: "empty interface list", object: map[string]interface{}{"items": []interface{}{}}, want: []string{}, presence: apiValuePresent},
		{name: "populated interface list", object: map[string]interface{}{"items": []interface{}{"first", "second"}}, want: []string{"first", "second"}, presence: apiValuePresent},
		{name: "typed string list", object: map[string]interface{}{"items": []string{"first", "second"}}, want: []string{"first", "second"}, presence: apiValuePresent},
		{name: "wrong collection shape", object: map[string]interface{}{"items": "api-body-secret"}, wantNull: true, presence: apiValuePresent, wantErrors: 1, wantPath: "items"},
		{name: "null element", object: map[string]interface{}{"items": []interface{}{"partial-secret-value", nil}}, wantNull: true, presence: apiValuePresent, wantErrors: 1, wantPath: `items[1]`},
		{name: "mixed element types", object: map[string]interface{}{"items": []interface{}{"partial-secret-value", 42, false}}, wantNull: true, presence: apiValuePresent, wantErrors: 2, wantPath: `items[1]`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, presence, diagnostics := strictAPIStringList(context.Background(), test.object, "items", path.Root("items"))
			if presence != test.presence {
				t.Fatalf("presence = %d, want %d", presence, test.presence)
			}
			if got.IsNull() != test.wantNull {
				t.Fatalf("null = %t, want %t", got.IsNull(), test.wantNull)
			}
			if !got.IsNull() {
				assertListValue(t, got, test.want)
			}
			assertCollectionDiagnostics(t, diagnostics, test.wantErrors, test.wantPath)
		})
	}
}

func TestStrictAPIStringMapPresenceSensitivityAndContentSafety(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		object     map[string]interface{}
		sensitive  bool
		want       map[string]string
		wantNull   bool
		presence   apiValuePresence
		wantErrors int
		wantPath   string
	}{
		{name: "absent", object: map[string]interface{}{}, wantNull: true, presence: apiValueAbsent},
		{name: "explicit null", object: map[string]interface{}{"metadata": nil}, wantNull: true, presence: apiValueNull},
		{name: "empty interface map", object: map[string]interface{}{"metadata": map[string]interface{}{}}, want: map[string]string{}, presence: apiValuePresent},
		{name: "populated interface map", object: map[string]interface{}{"metadata": map[string]interface{}{"one": "first", "two": "second"}}, want: map[string]string{"one": "first", "two": "second"}, presence: apiValuePresent},
		{name: "typed string map", object: map[string]interface{}{"metadata": map[string]string{"one": "first"}}, want: map[string]string{"one": "first"}, presence: apiValuePresent},
		{name: "wrong collection shape", object: map[string]interface{}{"metadata": []interface{}{}}, wantNull: true, presence: apiValuePresent, wantErrors: 1, wantPath: "metadata"},
		{name: "ordinary mixed map exposes key", object: map[string]interface{}{"metadata": map[string]interface{}{"good": "partial-secret-value", "credential-id-42": nil}}, wantNull: true, presence: apiValuePresent, wantErrors: 1, wantPath: `metadata["credential-id-42"]`},
		{name: "sensitive mixed map uses root", object: map[string]interface{}{"metadata": map[string]interface{}{"good": "partial-secret-value", "credential-id-42": nil}}, sensitive: true, wantNull: true, presence: apiValuePresent, wantErrors: 1, wantPath: "metadata"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, presence, diagnostics := strictAPIStringMap(context.Background(), test.object, "metadata", path.Root("metadata"), test.sensitive)
			if presence != test.presence {
				t.Fatalf("presence = %d, want %d", presence, test.presence)
			}
			if got.IsNull() != test.wantNull {
				t.Fatalf("null = %t, want %t", got.IsNull(), test.wantNull)
			}
			if !got.IsNull() {
				assertMapValue(t, got, test.want)
			}
			assertCollectionDiagnostics(t, diagnostics, test.wantErrors, test.wantPath)
		})
	}
}

func TestStrictCollectionConversionsHonorCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	list, listDiagnostics := types.ListValue(types.StringType, []attr.Value{types.StringValue("secret-value")})
	if listDiagnostics.HasError() {
		t.Fatalf("construct list: %v", listDiagnostics)
	}
	set, setDiagnostics := types.SetValue(types.StringType, []attr.Value{types.StringValue("secret-value")})
	if setDiagnostics.HasError() {
		t.Fatalf("construct set: %v", setDiagnostics)
	}
	mapped, mapDiagnostics := types.MapValue(types.StringType, map[string]attr.Value{"secret-key": types.StringValue("secret-value")})
	if mapDiagnostics.HasError() {
		t.Fatalf("construct map: %v", mapDiagnostics)
	}

	listResult, _, diagnostics := strictTerraformStringList(ctx, list, path.Root("items"))
	if listResult != nil {
		t.Fatal("canceled list conversion returned a value")
	}
	assertCollectionDiagnostics(t, diagnostics, 1, "items")
	setResult, _, diagnostics := strictTerraformStringSet(ctx, set, path.Root("items"))
	if setResult != nil {
		t.Fatal("canceled set conversion returned a value")
	}
	assertCollectionDiagnostics(t, diagnostics, 1, "items")
	mapResult, _, diagnostics := strictTerraformStringMap(ctx, mapped, path.Root("metadata"), false)
	if mapResult != nil {
		t.Fatal("canceled map conversion returned a value")
	}
	assertCollectionDiagnostics(t, diagnostics, 1, "metadata")

	apiList, presence, diagnostics := strictAPIStringList(ctx, map[string]interface{}{"items": []interface{}{"secret-value"}}, "items", path.Root("items"))
	if !apiList.IsNull() || presence != apiValuePresent {
		t.Fatal("canceled API list projection did not preserve presence with a null result")
	}
	assertCollectionDiagnostics(t, diagnostics, 1, "items")
	apiMap, presence, diagnostics := strictAPIStringMap(ctx, map[string]interface{}{"metadata": map[string]interface{}{"secret-key": "secret-value"}}, "metadata", path.Root("metadata"), false)
	if !apiMap.IsNull() || presence != apiValuePresent {
		t.Fatal("canceled API map projection did not preserve presence with a null result")
	}
	assertCollectionDiagnostics(t, diagnostics, 1, "metadata")
}

func assertCollectionDiagnostics(t *testing.T, diagnostics diag.Diagnostics, wantErrors int, wantFirstPath string) {
	t.Helper()
	if len(diagnostics.Errors()) != wantErrors {
		t.Fatalf("diagnostic errors = %d, want %d: %#v", len(diagnostics.Errors()), wantErrors, diagnostics)
	}
	for _, diagnostic := range diagnostics {
		text := diagnostic.Summary() + " " + diagnostic.Detail()
		for _, forbidden := range []string{"secret-value", "partial-secret-value", "api-body-secret", "credential-id-42", "Invalid List Element Type", "List Index"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("diagnostic summary/detail disclosed content %q: %q", forbidden, text)
			}
		}
	}
	if wantErrors == 0 || wantFirstPath == "" {
		return
	}
	withPath, ok := diagnostics.Errors()[0].(diag.DiagnosticWithPath)
	if !ok {
		t.Fatal("error diagnostic is not rooted at an attribute path")
	}
	if got := withPath.Path().String(); got != wantFirstPath {
		t.Fatalf("diagnostic path = %q, want %q", got, wantFirstPath)
	}
}

func assertStringSlice(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("slice length = %d, want %d; got %#v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("slice element %d = %q, want %q", index, got[index], want[index])
		}
	}
	if want != nil && got == nil {
		t.Fatal("empty/populated slice was returned as nil")
	}
}

func assertStringMap(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("map length = %d, want %d; got %#v", len(got), len(want), got)
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Fatalf("map element %q differs", key)
		}
	}
	if want != nil && got == nil {
		t.Fatal("empty/populated map was returned as nil")
	}
}

func assertListValue(t *testing.T, got types.List, want []string) {
	t.Helper()
	elements := got.Elements()
	if len(elements) != len(want) {
		t.Fatalf("list length = %d, want %d", len(elements), len(want))
	}
	for index, wantValue := range want {
		stringValue, ok := elements[index].(types.String)
		if !ok || stringValue.IsNull() || stringValue.IsUnknown() || stringValue.ValueString() != wantValue {
			t.Fatalf("list element %d was not the expected known string", index)
		}
	}
}

func assertMapValue(t *testing.T, got types.Map, want map[string]string) {
	t.Helper()
	elements := got.Elements()
	if len(elements) != len(want) {
		t.Fatalf("map length = %d, want %d", len(elements), len(want))
	}
	for key, wantValue := range want {
		stringValue, ok := elements[key].(types.String)
		if !ok || stringValue.IsNull() || stringValue.IsUnknown() || stringValue.ValueString() != wantValue {
			t.Fatalf("map element %q was not the expected known string", key)
		}
	}
}
