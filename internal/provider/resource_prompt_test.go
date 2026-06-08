package provider

import "testing"

func TestParsePromptResultWrappedPromptSpec(t *testing.T) {
	t.Parallel()

	result := parsePromptResult(map[string]interface{}{
		"prompt_spec": map[string]interface{}{
			"prompt_id": "test-prompt",
			"litellm_params": map[string]interface{}{
				"prompt_integration": "dotprompt",
			},
		},
		"raw_prompt_template": map[string]interface{}{
			"content": "Hello {{name}}",
		},
	})

	if result["prompt_id"] != "test-prompt" {
		t.Fatalf("expected prompt_spec to be unwrapped, got %#v", result)
	}
}

func TestParsePromptResultDirectPrompt(t *testing.T) {
	t.Parallel()

	result := parsePromptResult(map[string]interface{}{
		"prompt_id": "test-prompt",
	})

	if result["prompt_id"] != "test-prompt" {
		t.Fatalf("expected direct prompt result, got %#v", result)
	}
}
