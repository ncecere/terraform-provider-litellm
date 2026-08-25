package provider

import (
	"encoding/json"
	"testing"
)

func TestDecodePromptsListExactEnvelope(t *testing.T) {
	t.Parallel()

	items, err := decodeEnvelopeList(json.RawMessage(`{"prompts":[{"prompt_id":"test-prompt"}]}`), "/prompts/list", "prompts")
	if err != nil {
		t.Fatalf("decodeEnvelopeList() error = %v", err)
	}
	objects, err := decodeListObjects(items, "/prompts/list", "prompt item")
	if err != nil {
		t.Fatalf("decodeListObjects() error = %v", err)
	}
	if len(objects) != 1 || objects[0]["prompt_id"] != "test-prompt" {
		t.Fatalf("unexpected prompts result: %#v", objects)
	}
}

func TestDecodePromptsListRejectsLegacyArrayShape(t *testing.T) {
	t.Parallel()

	_, err := decodeEnvelopeList(json.RawMessage(`[{"prompt_id":"test-prompt"}]`), "/prompts/list", "prompts")
	if err == nil {
		t.Fatal("decodeEnvelopeList() accepted unsupported array response")
	}
}
