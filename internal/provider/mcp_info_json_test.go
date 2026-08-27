package provider

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseAndCanonicalizeMCPInfoJSONObject(t *testing.T) {
	t.Parallel()

	raw := `{
		"tool_allowlist_enforced": true,
		"owner": {"team":"security","contact":null},
		"tools": ["search", {"enabled":false}, null],
		"large": 9007199254740993,
		"exponent": 9.007199254740993e15
	}`
	object, err := parseMCPInfoJSONObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	if object["tool_allowlist_enforced"] != true {
		t.Fatalf("access-control boolean = %#v", object["tool_allowlist_enforced"])
	}
	owner := object["owner"].(map[string]interface{})
	if owner["team"] != "security" {
		t.Fatalf("owner = %#v", owner)
	}
	if value, present := owner["contact"]; !present || value != nil {
		t.Fatalf("nested null was not retained: %#v", owner)
	}
	if number, ok := object["large"].(json.Number); !ok || number.String() != "9007199254740993" {
		t.Fatalf("large number = %#v", object["large"])
	}
	if number, ok := object["exponent"].(json.Number); !ok || number.String() != "9.007199254740993e15" {
		t.Fatalf("exponent number = %#v", object["exponent"])
	}
	tools := object["tools"].([]interface{})
	if len(tools) != 3 || tools[2] != nil || tools[1].(map[string]interface{})["enabled"] != false {
		t.Fatalf("tools = %#v", tools)
	}

	canonical, err := canonicalizeMCPInfoJSONObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"exponent":9007199254740993,"large":9007199254740993,"owner":{"contact":null,"team":"security"},"tool_allowlist_enforced":true,"tools":["search",{"enabled":false},null]}`
	if canonical != want {
		t.Fatalf("canonical JSON = %s; want %s", canonical, want)
	}
}

func TestParseMCPInfoJSONObjectRejectsMalformedRootsAndDuplicates(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"", "null", `[]`, `true`, `1`, `"text"`, `{`, `{} {}`} {
		if _, err := parseMCPInfoJSONObject(raw); err == nil {
			t.Errorf("malformed or non-object root %q was accepted", raw)
		}
	}
	for _, raw := range []string{
		`{"duplicate-secret":1,"duplicate-secret":2}`,
		`{"key":1,"k\u0065y":2}`,
		`{"outer":{"same":1,"same":2}}`,
	} {
		if _, err := parseMCPInfoJSONObject(raw); !errors.Is(err, errJSONDuplicateMember) {
			t.Errorf("duplicate object error = %v", err)
		} else if strings.Contains(err.Error(), "duplicate-secret") || strings.Contains(err.Error(), "same") || strings.Contains(err.Error(), "key") {
			t.Errorf("duplicate error exposed member content: %v", err)
		}
	}
}

func TestMCPInfoJSONDeepCloneAndSemanticEquality(t *testing.T) {
	t.Parallel()

	source := map[string]interface{}{
		"owner": map[string]interface{}{"team": "security", "nullable": nil},
		"items": []interface{}{map[string]interface{}{"large": json.Number("9007199254740993")}, "value"},
	}
	clone := cloneMCPInfoJSONObject(source)
	clone["owner"].(map[string]interface{})["team"] = "changed"
	clone["items"].([]interface{})[0].(map[string]interface{})["large"] = json.Number("1")
	if source["owner"].(map[string]interface{})["team"] != "security" || source["items"].([]interface{})[0].(map[string]interface{})["large"].(json.Number).String() != "9007199254740993" {
		t.Fatalf("clone mutation escaped into source: %#v", source)
	}

	left, _ := parseMCPInfoJSONObject(`{"number":9007199254740993,"nested":[null,{"ok":true}]}`)
	right, _ := parseMCPInfoJSONObject(`{"nested":[null,{"ok":true}],"number":9.007199254740993e15}`)
	if !mcpInfoJSONValuesEqual(left, right) {
		t.Fatal("semantically equal exact JSON objects compared unequal")
	}
	right["number"] = json.Number("9007199254740992")
	if mcpInfoJSONValuesEqual(left, right) {
		t.Fatal("distinct exact numbers compared equal")
	}
}

func TestOverlayMCPInfoJSONObjects(t *testing.T) {
	t.Parallel()

	base := map[string]interface{}{
		"owner":  map[string]interface{}{"team": "security", "api_only": true},
		"access": true,
		"array":  []interface{}{json.Number("1"), map[string]interface{}{"remote": true}},
		"nested": map[string]interface{}{"nullable": nil, "keep": "remote"},
		"large":  json.Number("9007199254740993"),
	}
	configured := map[string]interface{}{
		"owner":  map[string]interface{}{"team": "platform"},
		"access": false,
		"array":  []interface{}{json.Number("2")},
		"nested": map[string]interface{}{"nullable": nil},
	}
	overlaid, err := overlayMCPInfoJSONObjects(base, configured)
	if err != nil {
		t.Fatal(err)
	}
	owner := overlaid["owner"].(map[string]interface{})
	if owner["team"] != "platform" || owner["api_only"] != true {
		t.Fatalf("recursive owner overlay = %#v", owner)
	}
	if overlaid["access"] != false || !reflect.DeepEqual(overlaid["array"], []interface{}{json.Number("2")}) {
		t.Fatalf("atomic values were not replaced: %#v", overlaid)
	}
	nested := overlaid["nested"].(map[string]interface{})
	if nested["keep"] != "remote" {
		t.Fatalf("nested sibling was not preserved: %#v", nested)
	}
	if value, present := nested["nullable"]; !present || value != nil {
		t.Fatalf("nested null was not overlaid as data: %#v", nested)
	}
	if overlaid["large"].(json.Number).String() != "9007199254740993" {
		t.Fatalf("unowned exact number was lost: %#v", overlaid["large"])
	}

	emptyOwned, err := overlayMCPInfoJSONObjects(base, map[string]interface{}{"owner": map[string]interface{}{}})
	if err != nil {
		t.Fatal(err)
	}
	if owner := emptyOwned["owner"].(map[string]interface{}); len(owner) != 0 {
		t.Fatalf("empty nested object did not own and replace object: %#v", owner)
	}
	emptyRoot, err := overlayMCPInfoJSONObjects(base, map[string]interface{}{})
	if err != nil || !mcpInfoJSONValuesEqual(emptyRoot, base) {
		t.Fatalf("empty root patch did not preserve base: %#v, %v", emptyRoot, err)
	}

	overlaid["owner"].(map[string]interface{})["team"] = "mutated"
	if base["owner"].(map[string]interface{})["team"] != "security" || configured["owner"].(map[string]interface{})["team"] != "platform" {
		t.Fatal("overlay result aliases an input")
	}
}

func TestCanonicalMCPInfoClearPointersAndClears(t *testing.T) {
	t.Parallel()

	pointers, err := canonicalMCPInfoClearPointers([]string{"/z", "/a~1b/~0member", "/owner/team"})
	if err != nil {
		t.Fatal(err)
	}
	wantPointers := []string{"/a~1b/~0member", "/owner/team", "/z"}
	if !reflect.DeepEqual(pointers, wantPointers) {
		t.Fatalf("canonical pointers = %#v; want %#v", pointers, wantPointers)
	}

	source := map[string]interface{}{
		"owner":  map[string]interface{}{"team": "security", "keep": true},
		"a/b":    map[string]interface{}{"~member": "clear", "keep": "value"},
		"array":  []interface{}{json.Number("1"), json.Number("2")},
		"nested": map[string]interface{}{"nullable": nil, "keep": true},
	}
	cleared, err := clearMCPInfoJSONMembers(source, []string{"/owner/team", "/a~1b/~0member", "/array", "/nested/nullable"})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := cleared["array"]; present {
		t.Fatal("atomic array member was not cleared")
	}
	if _, present := cleared["owner"].(map[string]interface{})["team"]; present || cleared["owner"].(map[string]interface{})["keep"] != true {
		t.Fatalf("owner clear was not selective: %#v", cleared["owner"])
	}
	if _, present := cleared["a/b"].(map[string]interface{})["~member"]; present || cleared["a/b"].(map[string]interface{})["keep"] != "value" {
		t.Fatalf("escaped clear was not selective: %#v", cleared["a/b"])
	}
	if _, present := cleared["nested"].(map[string]interface{})["nullable"]; present {
		t.Fatal("nested null member was not cleared")
	}
	if source["owner"].(map[string]interface{})["team"] != "security" {
		t.Fatal("clear mutated its source")
	}
	if absent, err := clearMCPInfoJSONMembers(source, []string{"/not-present/child"}); err != nil || !mcpInfoJSONValuesEqual(absent, source) {
		t.Fatalf("idempotent absent clear = %#v, %v", absent, err)
	}
}

func TestMCPInfoClearPointersRejectUnsafeForms(t *testing.T) {
	t.Parallel()

	invalid := [][]string{
		{""},
		{"owner/team"},
		{"#/owner/team"},
		{"/owner/~"},
		{"/owner/~2team"},
		{"/owner", "/owner"},
		{"/owner", "/owner/team"},
		{"/a~1b", "/a~1b/child"},
	}
	for _, pointers := range invalid {
		if _, err := canonicalMCPInfoClearPointers(pointers); err == nil {
			t.Errorf("unsafe clear pointers %#v were accepted", pointers)
		}
	}

	tooMany := make([]string, mcpInfoJSONMaxClearPointers+1)
	for index := range tooMany {
		tooMany[index] = "/member" + strings.Repeat("x", index)
	}
	if _, err := canonicalMCPInfoClearPointers(tooMany); !errors.Is(err, errMCPInfoJSONLimit) {
		t.Fatalf("pointer count limit error = %v", err)
	}
	tooDeep := strings.Repeat("/member", mcpInfoJSONMaxPointerDepth+1)
	if _, err := canonicalMCPInfoClearPointers([]string{tooDeep}); !errors.Is(err, errMCPInfoJSONLimit) {
		t.Fatalf("pointer depth limit error = %v", err)
	}
	tooLong := "/" + strings.Repeat("x", mcpInfoJSONMaxPointerBytes)
	if _, err := canonicalMCPInfoClearPointers([]string{tooLong}); err == nil {
		t.Fatal("oversized pointer was accepted")
	}

	source := map[string]interface{}{
		"items": []interface{}{map[string]interface{}{"name": "secret"}},
		"flag":  true,
		"none":  nil,
	}
	for _, pointer := range []string{"/items/0", "/flag/child", "/none/child"} {
		if _, err := clearMCPInfoJSONMembers(source, []string{pointer}); !errors.Is(err, errMCPInfoClearTraversal) {
			t.Errorf("non-object traversal %q error = %v", pointer, err)
		}
	}
}

func TestMCPInfoJSONErrorsAreContentFree(t *testing.T) {
	t.Parallel()

	secret := "mcp-super-secret-value"
	if _, err := parseMCPInfoJSONObject(`{"` + secret + `":1,"` + secret + `":2}`); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe parse error: %v", err)
	}
	if _, err := canonicalMCPInfoJSONObject(map[string]interface{}{"field": json.Number(secret)}); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe canonicalization error: %v", err)
	}
	if _, err := clearMCPInfoJSONMembers(map[string]interface{}{"field": secret}, []string{"/field/" + secret}); err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe clear error: %v", err)
	}
}
