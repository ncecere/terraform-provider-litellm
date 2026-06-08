package provider

import "testing"

func TestParseTagInfoResultKeyedObject(t *testing.T) {
	t.Parallel()

	result := parseTagInfoResult(map[string]interface{}{
		"test-tag": map[string]interface{}{
			"name":        "test-tag",
			"description": "test description",
			"models":      []interface{}{"gpt-4o-mini"},
		},
	}, "test-tag")

	if result["name"] != "test-tag" {
		t.Fatalf("expected keyed tag object, got %#v", result)
	}
}

func TestParseTagInfoResultArray(t *testing.T) {
	t.Parallel()

	result := parseTagInfoResult([]interface{}{
		map[string]interface{}{
			"name": "test-tag",
		},
	}, "test-tag")

	if result["name"] != "test-tag" {
		t.Fatalf("expected array tag object, got %#v", result)
	}
}
