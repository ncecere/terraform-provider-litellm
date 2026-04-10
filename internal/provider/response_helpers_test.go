package provider

import (
	"strings"
	"testing"
)

func TestRequiredStringField(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		result := map[string]interface{}{"policy_id": "pol-123"}
		value, err := requiredStringField(result, "policy_id")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if value != "pol-123" {
			t.Fatalf("expected value 'pol-123', got %q", value)
		}
	})

	t.Run("missing key", func(t *testing.T) {
		t.Parallel()

		_, err := requiredStringField(map[string]interface{}{}, "policy_id")
		if err == nil {
			t.Fatal("expected error for missing key")
		}
		if !strings.Contains(err.Error(), "response missing policy_id") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("empty string", func(t *testing.T) {
		t.Parallel()

		_, err := requiredStringField(map[string]interface{}{"policy_id": ""}, "policy_id")
		if err == nil {
			t.Fatal("expected error for empty string")
		}
		if !strings.Contains(err.Error(), "response missing policy_id") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("wrong type", func(t *testing.T) {
		t.Parallel()

		_, err := requiredStringField(map[string]interface{}{"policy_id": 123}, "policy_id")
		if err == nil {
			t.Fatal("expected error for non-string type")
		}
		if !strings.Contains(err.Error(), "response missing policy_id") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
