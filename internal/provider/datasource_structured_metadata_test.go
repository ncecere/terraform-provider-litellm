package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// LiteLLM 1.104.0 stores per-model limits inside metadata as objects, even
// when configured empty. The data-source metadata map is map(string).
func TestDataSourcesOmitStructuredDedicatedMetadata(t *testing.T) {
	const bare = "1111111111111111111111111111111111111111111111111111111111111111"
	keyInfo := map[string]interface{}{
		"key": bare,
		"info": map[string]interface{}{
			"token":    bare,
			"metadata": map[string]interface{}{"owner": "terraform", "model_rpm_limit": map[string]interface{}{}, "model_tpm_limit": map[string]interface{}{"gpt-4o": 10.0}, "guardrails": []interface{}{"g"}},
			"status":   "active",
		},
	}
	model, err := projectKeyDataSourceAPIObject(KeyDataSourceModel{KeyHash: types.StringValue("sha256:" + bare)}, keyInfo, bare, "sha256:"+bare)
	if err != nil {
		t.Fatalf("key data source rejected structured metadata: %v", err)
	}
	if elements := model.Metadata.Elements(); len(elements) != 1 || elements["owner"] == nil {
		t.Fatalf("key metadata=%v, want only owner", model.Metadata)
	}

	projected := withoutStructuredMetadata(map[string]interface{}{
		"environment": "dev", "model_rpm_limit": map[string]interface{}{}, "guardrails": "plain-string-kept", "custom": map[string]interface{}{"x": 1.0},
	}, teamMetadataStructuredFields)
	if _, kept := projected["model_rpm_limit"]; kept {
		t.Fatal("structured dedicated member was kept")
	}
	if projected["guardrails"] != "plain-string-kept" || projected["environment"] != "dev" {
		t.Fatalf("string members changed: %#v", projected)
	}
	if _, kept := projected["custom"]; !kept {
		t.Fatal("non-dedicated member was removed; it must still fail closed downstream if not a string")
	}
}
