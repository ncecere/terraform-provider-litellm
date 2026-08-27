package provider

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type dataSourcePresenceReader func(map[string]interface{}, ...string) (attr.Value, error)

func TestDataSourceRequiredPresenceReaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   interface{}
		read    dataSourcePresenceReader
		assert  func(*testing.T, attr.Value)
		wrong   interface{}
		emptyID bool
	}{
		{name: "string", input: "identity", wrong: false, read: requiredStringPresenceReader, assert: func(t *testing.T, value attr.Value) {
			if value.(types.String).ValueString() != "identity" {
				t.Fatal("required string was not preserved")
			}
		}, emptyID: true},
		{name: "false boolean", input: false, wrong: "false", read: requiredBoolPresenceReader, assert: func(t *testing.T, value attr.Value) {
			if value.(types.Bool).ValueBool() {
				t.Fatal("explicit false was not preserved")
			}
		}},
		{name: "exact integer", input: json.Number("9007199254740993"), wrong: "9007199254740993", read: requiredInt64PresenceReader, assert: func(t *testing.T, value attr.Value) {
			if value.(types.Int64).ValueInt64() != 9007199254740993 {
				t.Fatal("exact integer was not preserved")
			}
		}},
		{name: "zero float", input: json.Number("0"), wrong: "0", read: requiredFloat64PresenceReader, assert: func(t *testing.T, value attr.Value) {
			if value.(types.Float64).ValueFloat64() != 0 {
				t.Fatal("explicit zero was not preserved")
			}
		}},
		{name: "empty string list", input: []interface{}{}, wrong: map[string]interface{}{}, read: requiredStringListPresenceReader, assert: assertKnownEmptyCollection},
		{name: "empty string set", input: []interface{}{}, wrong: map[string]interface{}{}, read: requiredStringSetPresenceReader, assert: assertKnownEmptyCollection},
		{name: "empty string map", input: map[string]interface{}{}, wrong: []interface{}{}, read: requiredStringMapPresenceReader, assert: assertKnownEmptyCollection},
		{name: "canonical object", input: map[string]interface{}{"z": json.Number("9007199254740993123456789"), "a": []interface{}{json.Number("1.2500")}}, wrong: []interface{}{}, read: requiredCanonicalJSONPresenceReader, assert: func(t *testing.T, value attr.Value) {
			if got := value.(types.String).ValueString(); got != `{"a":[1.2500],"z":9007199254740993123456789}` {
				t.Fatalf("canonical JSON exact-number projection = %q", got)
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, err := test.read(map[string]interface{}{"field": test.input}, "field")
			if err != nil || value.IsNull() || value.IsUnknown() {
				t.Fatalf("known required value: value=%T null=%t unknown=%t error=%v", value, value.IsNull(), value.IsUnknown(), err)
			}
			test.assert(t, value)

			for name, object := range map[string]map[string]interface{}{
				"absent": {},
				"null":   {"field": nil},
			} {
				t.Run(name, func(t *testing.T) {
					failed, readErr := test.read(object, "field")
					if readErr == nil || !failed.IsNull() || failed.IsUnknown() {
						t.Fatalf("required %s accepted", name)
					}
				})
			}
			failed, readErr := test.read(map[string]interface{}{"field": test.wrong}, "field")
			if readErr == nil || !failed.IsNull() || failed.IsUnknown() {
				t.Fatal("required wrong type accepted")
			}
			if test.emptyID {
				failed, readErr = test.read(map[string]interface{}{"field": ""}, "field")
				if readErr == nil || !failed.IsNull() {
					t.Fatal("required empty identity accepted")
				}
			}
		})
	}
}

func TestDataSourceNullablePresenceReaders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		explicit interface{}
		read     dataSourcePresenceReader
		assert   func(*testing.T, attr.Value)
	}{
		{name: "empty string", explicit: "", read: nullableStringPresenceReader, assert: func(t *testing.T, value attr.Value) {
			if value.(types.String).ValueString() != "" {
				t.Fatal("explicit empty string changed")
			}
		}},
		{name: "false boolean", explicit: false, read: nullableBoolPresenceReader, assert: func(t *testing.T, value attr.Value) {
			if value.(types.Bool).ValueBool() {
				t.Fatal("explicit false changed")
			}
		}},
		{name: "zero integer", explicit: json.Number("0e1000000"), read: nullableInt64PresenceReader, assert: func(t *testing.T, value attr.Value) {
			if value.(types.Int64).ValueInt64() != 0 {
				t.Fatal("explicit exact zero changed")
			}
		}},
		{name: "zero float", explicit: json.Number("-0"), read: nullableFloat64PresenceReader, assert: func(t *testing.T, value attr.Value) {
			if value.(types.Float64).ValueFloat64() != 0 {
				t.Fatal("explicit zero changed")
			}
		}},
		{name: "empty string list", explicit: []interface{}{}, read: nullableStringListPresenceReader, assert: assertKnownEmptyCollection},
		{name: "empty string set", explicit: []interface{}{}, read: nullableStringSetPresenceReader, assert: assertKnownEmptyCollection},
		{name: "empty string map", explicit: map[string]interface{}{}, read: nullableStringMapPresenceReader, assert: assertKnownEmptyCollection},
		{name: "empty canonical object", explicit: map[string]interface{}{}, read: nullableCanonicalJSONPresenceReader, assert: func(t *testing.T, value attr.Value) {
			if value.(types.String).ValueString() != "{}" {
				t.Fatal("explicit empty object changed")
			}
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for name, object := range map[string]map[string]interface{}{
				"absent": {},
				"null":   {"outer": map[string]interface{}{"field": nil}},
			} {
				path := []string{"outer", "field"}
				if name == "absent" {
					path = []string{"field"}
				}
				value, err := test.read(object, path...)
				if err != nil || !value.IsNull() || value.IsUnknown() {
					t.Fatalf("nullable %s did not resolve to typed null: null=%t unknown=%t error=%v", name, value.IsNull(), value.IsUnknown(), err)
				}
			}

			value, err := test.read(map[string]interface{}{"field": test.explicit}, "field")
			if err != nil || value.IsNull() || value.IsUnknown() {
				t.Fatalf("explicit nullable value not known: null=%t unknown=%t error=%v", value.IsNull(), value.IsUnknown(), err)
			}
			test.assert(t, value)
		})
	}
}

func TestDataSourcePresenceContainerValuesRemainTyped(t *testing.T) {
	t.Parallel()

	list, err := dataSourceNullableStringListAt(map[string]interface{}{"field": []interface{}{"", "two"}}, "field")
	if err != nil || len(list.Elements()) != 2 || list.Elements()[0].(types.String).ValueString() != "" || list.Elements()[1].(types.String).ValueString() != "two" {
		t.Fatalf("string list was not preserved atomically: error=%v", err)
	}
	set, err := dataSourceNullableStringSetAt(map[string]interface{}{"field": []interface{}{"one", "two"}}, "field")
	if err != nil || len(set.Elements()) != 2 || set.Elements()[0].(types.String).ValueString() != "one" || set.Elements()[1].(types.String).ValueString() != "two" {
		t.Fatalf("string set was not preserved atomically: error=%v", err)
	}
	mapped, err := dataSourceNullableStringMapAt(map[string]interface{}{"field": map[string]interface{}{"empty": "", "known": "value"}}, "field")
	if err != nil || len(mapped.Elements()) != 2 || mapped.Elements()["empty"].(types.String).ValueString() != "" || mapped.Elements()["known"].(types.String).ValueString() != "value" {
		t.Fatalf("string map was not preserved atomically: error=%v", err)
	}
}

func TestDataSourcePresenceReadersRejectMalformedValuesAtomically(t *testing.T) {
	t.Parallel()

	secret := "secret-response-body-value"
	tests := []struct {
		name  string
		input interface{}
		read  dataSourcePresenceReader
	}{
		{name: "string type", input: []interface{}{secret}, read: nullableStringPresenceReader},
		{name: "boolean type", input: secret, read: nullableBoolPresenceReader},
		{name: "integer fractional", input: json.Number("1.5"), read: nullableInt64PresenceReader},
		{name: "integer string coercion", input: "1", read: nullableInt64PresenceReader},
		{name: "float string coercion", input: "1.5", read: nullableFloat64PresenceReader},
		{name: "float nonfinite", input: math.Inf(1), read: nullableFloat64PresenceReader},
		{name: "list element", input: []interface{}{"valid", secret, false}, read: nullableStringListPresenceReader},
		{name: "set element", input: []interface{}{"valid", secret, false}, read: nullableStringSetPresenceReader},
		{name: "map element", input: map[string]interface{}{"valid": "yes", "private-key": false}, read: nullableStringMapPresenceReader},
		{name: "canonical object shape", input: []interface{}{}, read: nullableCanonicalJSONPresenceReader},
		{name: "canonical object encoding", input: map[string]interface{}{"private-key": func() {}}, read: nullableCanonicalJSONPresenceReader},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, err := test.read(map[string]interface{}{"reviewed_field": test.input}, "reviewed_field")
			if err == nil || !value.IsNull() || value.IsUnknown() {
				t.Fatalf("malformed value was not rejected atomically: null=%t unknown=%t", value.IsNull(), value.IsUnknown())
			}
			rendered := err.Error()
			if !strings.Contains(rendered, "reviewed_field") || strings.Contains(rendered, secret) || strings.Contains(rendered, "private-key") {
				t.Fatalf("error was not content-safe: %q", rendered)
			}
		})
	}

	_, err := dataSourceNullableStringAt(map[string]interface{}{"reviewed": secret}, "reviewed", "nested")
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "reviewed") || !strings.Contains(err.Error(), "expected an object") {
		t.Fatalf("nested relation error was not content-safe: %v", err)
	}
}

func TestDataSourceRoleRedactedNullableOmissionIsTypedNull(t *testing.T) {
	t.Parallel()

	readers := []dataSourcePresenceReader{
		roleRedactedStringPresenceReader,
		roleRedactedBoolPresenceReader,
		roleRedactedInt64PresenceReader,
		roleRedactedFloat64PresenceReader,
		roleRedactedStringListPresenceReader,
		roleRedactedStringSetPresenceReader,
		roleRedactedStringMapPresenceReader,
		roleRedactedCanonicalJSONPresenceReader,
	}
	for _, read := range readers {
		value, err := read(map[string]interface{}{}, "role_redacted_field")
		if err != nil || !value.IsNull() || value.IsUnknown() {
			t.Fatalf("role-redacted omission fabricated a value: null=%t unknown=%t error=%v", value.IsNull(), value.IsUnknown(), err)
		}
	}
}

func assertKnownEmptyCollection(t *testing.T, value attr.Value) {
	t.Helper()
	switch value := value.(type) {
	case types.List:
		if len(value.Elements()) != 0 {
			t.Fatal("list is not empty")
		}
	case types.Set:
		if len(value.Elements()) != 0 {
			t.Fatal("set is not empty")
		}
	case types.Map:
		if len(value.Elements()) != 0 {
			t.Fatal("map is not empty")
		}
	default:
		t.Fatalf("unexpected collection type %T", value)
	}
}

func requiredStringPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRequiredStringAt(object, path...)
}
func requiredBoolPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRequiredBoolAt(object, path...)
}
func requiredInt64PresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRequiredInt64At(object, path...)
}
func requiredFloat64PresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRequiredFloat64At(object, path...)
}
func requiredStringListPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRequiredStringListAt(object, path...)
}
func requiredStringSetPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRequiredStringSetAt(object, path...)
}
func requiredStringMapPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRequiredStringMapAt(object, path...)
}
func requiredCanonicalJSONPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRequiredCanonicalJSONObjectAt(object, path...)
}
func nullableStringPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceNullableStringAt(object, path...)
}
func nullableBoolPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceNullableBoolAt(object, path...)
}
func nullableInt64PresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceNullableInt64At(object, path...)
}
func nullableFloat64PresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceNullableFloat64At(object, path...)
}
func nullableStringListPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceNullableStringListAt(object, path...)
}
func nullableStringSetPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceNullableStringSetAt(object, path...)
}
func nullableStringMapPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceNullableStringMapAt(object, path...)
}
func nullableCanonicalJSONPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceNullableCanonicalJSONObjectAt(object, path...)
}
func roleRedactedStringPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRoleRedactedNullableStringAt(object, path...)
}
func roleRedactedBoolPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRoleRedactedNullableBoolAt(object, path...)
}
func roleRedactedInt64PresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRoleRedactedNullableInt64At(object, path...)
}
func roleRedactedFloat64PresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRoleRedactedNullableFloat64At(object, path...)
}
func roleRedactedStringListPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRoleRedactedNullableStringListAt(object, path...)
}
func roleRedactedStringSetPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRoleRedactedNullableStringSetAt(object, path...)
}
func roleRedactedStringMapPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRoleRedactedNullableStringMapAt(object, path...)
}
func roleRedactedCanonicalJSONPresenceReader(object map[string]interface{}, path ...string) (attr.Value, error) {
	return dataSourceRoleRedactedNullableCanonicalJSONObjectAt(object, path...)
}
