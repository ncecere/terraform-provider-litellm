package provider

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// assertDataSourceReadComputedKnown is protocol-test-only. Call it after a
// successful ReadDataSource RPC to enforce that every computed schema path is
// known (a typed null is known). Collection element paths use [*], so failures
// never disclose map keys, set values, IDs, URLs, response bodies, or secrets.
func assertDataSourceReadComputedKnown(t testing.TB, schema *tfprotov6.Schema, response *tfprotov6.ReadDataSourceResponse) {
	t.Helper()
	if schema == nil || response == nil {
		t.Fatal("data source completion assertion requires schema and response")
	}
	for _, diagnostic := range response.Diagnostics {
		if diagnostic != nil && diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatal("data source completion assertion requires a successful Read")
		}
	}
	if response.Deferred != nil {
		t.Fatal("data source completion assertion requires a non-deferred Read")
	}
	if response.State == nil {
		t.Fatal("successful data source Read returned no state")
	}
	value, err := response.State.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal("successful data source Read returned state with an unexpected schema shape")
	}
	paths, err := dataSourceUnknownComputedPaths(schema, value)
	if err != nil {
		t.Fatalf("inspect successful data source state: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("successful data source Read left computed paths unknown: %s", strings.Join(paths, ", "))
	}
}

func dataSourceUnknownComputedPaths(schema *tfprotov6.Schema, value tftypes.Value) ([]string, error) {
	if schema == nil || schema.Block == nil {
		return nil, fmt.Errorf("invalid data source schema: expected a root object")
	}
	paths, err := dataSourceUnknownComputedBlock(schema.Block, value, "")
	if err != nil {
		return nil, err
	}
	unique := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		unique[path] = struct{}{}
	}
	paths = paths[:0]
	for path := range unique {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func dataSourceUnknownComputedBlock(block *tfprotov6.SchemaBlock, value tftypes.Value, path string) ([]string, error) {
	if !value.IsKnown() {
		return dataSourceComputedPathsInBlock(block, path), nil
	}
	if value.IsNull() {
		return nil, nil
	}
	attributes := map[string]tftypes.Value{}
	if err := value.As(&attributes); err != nil {
		return nil, dataSourceCompletionShapeError(path, "an object")
	}

	var paths []string
	for _, attribute := range block.Attributes {
		if attribute == nil {
			continue
		}
		attributePath := dataSourceCompletionAttributePath(path, attribute.Name)
		attributeValue, ok := attributes[attribute.Name]
		if !ok {
			return nil, dataSourceCompletionShapeError(attributePath, "a schema attribute")
		}
		if attribute.Computed {
			unknown, err := dataSourceUnknownValuePaths(attributeValue, attributePath)
			if err != nil {
				return nil, err
			}
			paths = append(paths, unknown...)
			if !attributeValue.IsKnown() {
				continue
			}
		}
		if attribute.NestedType == nil {
			continue
		}
		if !attributeValue.IsKnown() {
			paths = append(paths, dataSourceComputedPathsInObject(attribute.NestedType, attributePath)...)
			continue
		}
		nested, err := dataSourceUnknownComputedObject(attribute.NestedType, attributeValue, attributePath)
		if err != nil {
			return nil, err
		}
		paths = append(paths, nested...)
	}
	for _, nestedBlock := range block.BlockTypes {
		if nestedBlock == nil {
			continue
		}
		blockPath := dataSourceCompletionAttributePath(path, nestedBlock.TypeName)
		blockValue, ok := attributes[nestedBlock.TypeName]
		if !ok {
			return nil, dataSourceCompletionShapeError(blockPath, "a nested block")
		}
		if !blockValue.IsKnown() {
			paths = append(paths, dataSourceComputedPathsInNestedBlock(nestedBlock, blockPath)...)
			continue
		}
		nested, err := dataSourceUnknownComputedNestedBlock(nestedBlock, blockValue, blockPath)
		if err != nil {
			return nil, err
		}
		paths = append(paths, nested...)
	}
	return paths, nil
}

func dataSourceUnknownComputedObject(object *tfprotov6.SchemaObject, value tftypes.Value, path string) ([]string, error) {
	if value.IsNull() {
		return nil, nil
	}
	switch object.Nesting {
	case tfprotov6.SchemaObjectNestingModeSingle:
		return dataSourceUnknownComputedAttributes(object.Attributes, value, path)
	case tfprotov6.SchemaObjectNestingModeList, tfprotov6.SchemaObjectNestingModeSet:
		values := []tftypes.Value{}
		if err := value.As(&values); err != nil {
			return nil, dataSourceCompletionShapeError(path, "a collection")
		}
		return dataSourceUnknownComputedAttributeElements(object.Attributes, values, path+"[*]")
	case tfprotov6.SchemaObjectNestingModeMap:
		values := map[string]tftypes.Value{}
		if err := value.As(&values); err != nil {
			return nil, dataSourceCompletionShapeError(path, "a map")
		}
		var paths []string
		for _, element := range values {
			nested, err := dataSourceUnknownComputedAttributes(object.Attributes, element, path+"[*]")
			if err != nil {
				return nil, err
			}
			paths = append(paths, nested...)
		}
		return paths, nil
	default:
		return nil, dataSourceCompletionShapeError(path, "a supported nested object")
	}
}

func dataSourceUnknownComputedNestedBlock(block *tfprotov6.SchemaNestedBlock, value tftypes.Value, path string) ([]string, error) {
	if value.IsNull() {
		return nil, nil
	}
	switch block.Nesting {
	case tfprotov6.SchemaNestedBlockNestingModeSingle, tfprotov6.SchemaNestedBlockNestingModeGroup:
		return dataSourceUnknownComputedBlock(block.Block, value, path)
	case tfprotov6.SchemaNestedBlockNestingModeList, tfprotov6.SchemaNestedBlockNestingModeSet:
		values := []tftypes.Value{}
		if err := value.As(&values); err != nil {
			return nil, dataSourceCompletionShapeError(path, "a collection")
		}
		var paths []string
		for _, element := range values {
			nested, err := dataSourceUnknownComputedBlock(block.Block, element, path+"[*]")
			if err != nil {
				return nil, err
			}
			paths = append(paths, nested...)
		}
		return paths, nil
	case tfprotov6.SchemaNestedBlockNestingModeMap:
		values := map[string]tftypes.Value{}
		if err := value.As(&values); err != nil {
			return nil, dataSourceCompletionShapeError(path, "a map")
		}
		var paths []string
		for _, element := range values {
			nested, err := dataSourceUnknownComputedBlock(block.Block, element, path+"[*]")
			if err != nil {
				return nil, err
			}
			paths = append(paths, nested...)
		}
		return paths, nil
	default:
		return nil, dataSourceCompletionShapeError(path, "a supported nested block")
	}
}

func dataSourceUnknownComputedAttributes(attributes []*tfprotov6.SchemaAttribute, value tftypes.Value, path string) ([]string, error) {
	if !value.IsKnown() {
		return dataSourceComputedPathsInAttributes(attributes, path), nil
	}
	if value.IsNull() {
		return nil, nil
	}
	values := map[string]tftypes.Value{}
	if err := value.As(&values); err != nil {
		return nil, dataSourceCompletionShapeError(path, "an object")
	}
	var paths []string
	for _, attribute := range attributes {
		if attribute == nil {
			continue
		}
		attributePath := dataSourceCompletionAttributePath(path, attribute.Name)
		attributeValue, ok := values[attribute.Name]
		if !ok {
			return nil, dataSourceCompletionShapeError(attributePath, "a schema attribute")
		}
		if attribute.Computed {
			unknown, err := dataSourceUnknownValuePaths(attributeValue, attributePath)
			if err != nil {
				return nil, err
			}
			paths = append(paths, unknown...)
			if !attributeValue.IsKnown() {
				continue
			}
		}
		if attribute.NestedType != nil {
			if !attributeValue.IsKnown() {
				paths = append(paths, dataSourceComputedPathsInObject(attribute.NestedType, attributePath)...)
				continue
			}
			nested, err := dataSourceUnknownComputedObject(attribute.NestedType, attributeValue, attributePath)
			if err != nil {
				return nil, err
			}
			paths = append(paths, nested...)
		}
	}
	return paths, nil
}

func dataSourceUnknownValuePaths(value tftypes.Value, path string) ([]string, error) {
	if !value.IsKnown() {
		return []string{path}, nil
	}
	if value.IsNull() {
		return nil, nil
	}
	switch value.Type().(type) {
	case tftypes.Object:
		values := map[string]tftypes.Value{}
		if err := value.As(&values); err != nil {
			return nil, dataSourceCompletionShapeError(path, "an object")
		}
		var paths []string
		for name, element := range values {
			nested, err := dataSourceUnknownValuePaths(element, dataSourceCompletionAttributePath(path, name))
			if err != nil {
				return nil, err
			}
			paths = append(paths, nested...)
		}
		return paths, nil
	case tftypes.Map:
		values := map[string]tftypes.Value{}
		if err := value.As(&values); err != nil {
			return nil, dataSourceCompletionShapeError(path, "a map")
		}
		var paths []string
		for _, element := range values {
			nested, err := dataSourceUnknownValuePaths(element, path+"[*]")
			if err != nil {
				return nil, err
			}
			paths = append(paths, nested...)
		}
		return paths, nil
	case tftypes.List, tftypes.Set, tftypes.Tuple:
		values := []tftypes.Value{}
		if err := value.As(&values); err != nil {
			return nil, dataSourceCompletionShapeError(path, "a collection")
		}
		var paths []string
		for _, element := range values {
			nested, err := dataSourceUnknownValuePaths(element, path+"[*]")
			if err != nil {
				return nil, err
			}
			paths = append(paths, nested...)
		}
		return paths, nil
	default:
		return nil, nil
	}
}

func dataSourceUnknownComputedAttributeElements(attributes []*tfprotov6.SchemaAttribute, values []tftypes.Value, path string) ([]string, error) {
	var paths []string
	for _, element := range values {
		nested, err := dataSourceUnknownComputedAttributes(attributes, element, path)
		if err != nil {
			return nil, err
		}
		paths = append(paths, nested...)
	}
	return paths, nil
}

func dataSourceComputedPathsInBlock(block *tfprotov6.SchemaBlock, path string) []string {
	if block == nil {
		return nil
	}
	paths := dataSourceComputedPathsInAttributes(block.Attributes, path)
	for _, nestedBlock := range block.BlockTypes {
		if nestedBlock == nil {
			continue
		}
		paths = append(paths, dataSourceComputedPathsInNestedBlock(nestedBlock, dataSourceCompletionAttributePath(path, nestedBlock.TypeName))...)
	}
	return paths
}

func dataSourceComputedPathsInAttributes(attributes []*tfprotov6.SchemaAttribute, path string) []string {
	var paths []string
	for _, attribute := range attributes {
		if attribute == nil {
			continue
		}
		attributePath := dataSourceCompletionAttributePath(path, attribute.Name)
		if attribute.Computed {
			paths = append(paths, attributePath)
			continue
		}
		if attribute.NestedType != nil {
			paths = append(paths, dataSourceComputedPathsInObject(attribute.NestedType, attributePath)...)
		}
	}
	return paths
}

func dataSourceComputedPathsInObject(object *tfprotov6.SchemaObject, path string) []string {
	if object == nil {
		return nil
	}
	if object.Nesting != tfprotov6.SchemaObjectNestingModeSingle {
		path += "[*]"
	}
	return dataSourceComputedPathsInAttributes(object.Attributes, path)
}

func dataSourceComputedPathsInNestedBlock(block *tfprotov6.SchemaNestedBlock, path string) []string {
	if block == nil {
		return nil
	}
	if block.Nesting != tfprotov6.SchemaNestedBlockNestingModeSingle && block.Nesting != tfprotov6.SchemaNestedBlockNestingModeGroup {
		path += "[*]"
	}
	return dataSourceComputedPathsInBlock(block.Block, path)
}

func dataSourceCompletionAttributePath(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "." + name
}

func dataSourceCompletionShapeError(path, expected string) error {
	if path == "" {
		return fmt.Errorf("invalid data source state at root: expected %s", expected)
	}
	return fmt.Errorf("invalid data source state at %q: expected %s", path, expected)
}

func TestDataSourceUnknownComputedPathsRecursesWithoutContent(t *testing.T) {
	t.Parallel()

	schema := dataSourceCompletionTestSchema()
	value := dataSourceCompletionTestValue(t, schema, tftypes.UnknownValue)
	paths, err := dataSourceUnknownComputedPaths(schema, value)
	if err != nil {
		t.Fatalf("completion paths: %v", err)
	}
	want := []string{"list[*].leaf", "map[*].leaf", "object.leaf", "scalar", "set[*].leaf"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("unknown computed paths = %v, want %v", paths, want)
	}
	for _, forbidden := range []string{"private-map-key", "private-set-value", "private-list-value"} {
		if strings.Contains(strings.Join(paths, ","), forbidden) {
			t.Fatalf("computed path disclosed collection content %q", forbidden)
		}
	}
}

func TestDataSourceUnknownComputedPathsAcceptsTypedNullAndKnownEmpty(t *testing.T) {
	t.Parallel()

	schema := dataSourceCompletionTestSchema()
	value := dataSourceCompletionTestValue(t, schema, nil)
	paths, err := dataSourceUnknownComputedPaths(schema, value)
	if err != nil {
		t.Fatalf("completion paths: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("typed nulls or known empties reported unknown: %v", paths)
	}

	dynamic, err := tfprotov6.NewDynamicValue(schema.ValueType(), value)
	if err != nil {
		t.Fatalf("dynamic test value: %v", err)
	}
	assertDataSourceReadComputedKnown(t, schema, &tfprotov6.ReadDataSourceResponse{State: &dynamic})
}

func dataSourceCompletionTestSchema() *tfprotov6.Schema {
	nested := func(nesting tfprotov6.SchemaObjectNestingMode) *tfprotov6.SchemaObject {
		return &tfprotov6.SchemaObject{Nesting: nesting, Attributes: []*tfprotov6.SchemaAttribute{
			{Name: "marker", Type: tftypes.String, Optional: true},
			{Name: "leaf", Type: tftypes.String, Computed: true},
		}}
	}
	return &tfprotov6.Schema{Block: &tfprotov6.SchemaBlock{Attributes: []*tfprotov6.SchemaAttribute{
		{Name: "scalar", Type: tftypes.String, Computed: true},
		{Name: "object", NestedType: nested(tfprotov6.SchemaObjectNestingModeSingle), Computed: true},
		{Name: "list", NestedType: nested(tfprotov6.SchemaObjectNestingModeList), Computed: true},
		{Name: "map", NestedType: nested(tfprotov6.SchemaObjectNestingModeMap), Computed: true},
		{Name: "set", NestedType: nested(tfprotov6.SchemaObjectNestingModeSet), Computed: true},
	}}}
}

func dataSourceCompletionTestValue(t *testing.T, schema *tfprotov6.Schema, leaf interface{}) tftypes.Value {
	t.Helper()
	rootType := schema.ValueType().(tftypes.Object)
	leafValue := tftypes.NewValue(tftypes.String, leaf)
	element := func(elementType tftypes.Type, marker string) tftypes.Value {
		return tftypes.NewValue(elementType, map[string]tftypes.Value{
			"marker": tftypes.NewValue(tftypes.String, marker),
			"leaf":   leafValue,
		})
	}
	objectType := rootType.AttributeTypes["object"].(tftypes.Object)
	listType := rootType.AttributeTypes["list"].(tftypes.List)
	mapType := rootType.AttributeTypes["map"].(tftypes.Map)
	setType := rootType.AttributeTypes["set"].(tftypes.Set)

	var scalar interface{} = leaf
	var object interface{} = map[string]tftypes.Value{
		"marker": tftypes.NewValue(tftypes.String, "private-object-value"),
		"leaf":   leafValue,
	}
	var list interface{} = []tftypes.Value{element(listType.ElementType, "private-list-value")}
	var mapped interface{} = map[string]tftypes.Value{"private-map-key": element(mapType.ElementType, "private-map-value")}
	var set interface{} = []tftypes.Value{element(setType.ElementType, "private-set-value")}
	if leaf == nil {
		scalar = nil
		object = nil
		list = []tftypes.Value{}
		mapped = map[string]tftypes.Value{}
		set = []tftypes.Value{}
	}
	return tftypes.NewValue(rootType, map[string]tftypes.Value{
		"scalar": tftypes.NewValue(tftypes.String, scalar),
		"object": tftypes.NewValue(objectType, object),
		"list":   tftypes.NewValue(listType, list),
		"map":    tftypes.NewValue(mapType, mapped),
		"set":    tftypes.NewValue(setType, set),
	})
}
