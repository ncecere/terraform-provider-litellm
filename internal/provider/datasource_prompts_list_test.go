package provider

import "testing"

func TestParsePromptsListResultWrappedObject(t *testing.T) {
	t.Parallel()

	result := parsePromptsListResult(map[string]interface{}{
		"prompts": []interface{}{
			map[string]interface{}{"prompt_id": "test-prompt"},
		},
	})

	if len(result) != 1 || result[0]["prompt_id"] != "test-prompt" {
		t.Fatalf("unexpected prompts result: %#v", result)
	}
}

func TestParsePromptsListResultArray(t *testing.T) {
	t.Parallel()

	result := parsePromptsListResult([]interface{}{
		map[string]interface{}{"prompt_id": "test-prompt"},
	})

	if len(result) != 1 || result[0]["prompt_id"] != "test-prompt" {
		t.Fatalf("unexpected prompts result: %#v", result)
	}
}
