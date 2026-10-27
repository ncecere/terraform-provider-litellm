package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Issue #299: LiteLLM's admin UI stores team metadata flags such as
// disable_global_guardrails as JSON booleans. The legacy map(string)
// projection must stringify them instead of failing the read.
func TestTeamLegacyMetadataStringifiesScalarBooleansAndNumbers(t *testing.T) {
	ctx := context.Background()
	current := types.MapValueMust(types.StringType, map[string]attr.Value{
		"disable_global_guardrails": types.StringValue("false"),
		"tier":                      types.StringValue("1"),
		"owner":                     types.StringValue("platform"),
	})
	remote := map[string]interface{}{
		"disable_global_guardrails": true,
		"tier":                      json.Number("2"),
		"owner":                     "platform",
		"untracked":                 false,
	}
	projected, err := projectTeamLegacyMetadataWithSemantic(ctx, current, remote, teamSemanticOwnership{}, false)
	if err != nil {
		t.Fatalf("boolean/number team metadata failed the read: %v", err)
	}
	want := map[string]string{"disable_global_guardrails": "true", "tier": "2", "owner": "platform"}
	elements := projected.Elements()
	if len(elements) != len(want) {
		t.Fatalf("projected = %v, want %v", elements, want)
	}
	for key, value := range want {
		if got := elements[key].(types.String).ValueString(); got != value {
			t.Fatalf("%s = %q, want %q", key, got, value)
		}
	}

	// A configured string that matches the remote boolean is kept verbatim.
	same := types.MapValueMust(types.StringType, map[string]attr.Value{"disable_global_guardrails": types.StringValue("true")})
	kept, err := projectTeamLegacyMetadataWithSemantic(ctx, same, map[string]interface{}{"disable_global_guardrails": true}, teamSemanticOwnership{}, false)
	if err != nil || kept.Elements()["disable_global_guardrails"].(types.String).ValueString() != "true" {
		t.Fatalf("matching boolean: %v err=%v", kept, err)
	}
}

func TestTeamDataSourceMetadataStringifiesScalarBooleansAndNumbers(t *testing.T) {
	projected := withStringifiedScalarMetadata(map[string]interface{}{
		"disable_global_guardrails": true, "tier": json.Number("2"), "ratio": 0.5, "owner": "platform", "nested": map[string]interface{}{"x": "y"}, "empty": nil,
	})
	for key, want := range map[string]interface{}{"disable_global_guardrails": "true", "tier": "2", "ratio": "0.5", "owner": "platform"} {
		if projected[key] != want {
			t.Fatalf("%s = %#v, want %#v", key, projected[key], want)
		}
	}
	if _, ok := projected["nested"].(map[string]interface{}); !ok {
		t.Fatal("object member must stay an object and keep failing closed in the data source")
	}
	if projected["empty"] != nil {
		t.Fatal("null member must stay null")
	}
	model, err := dataSourceNullableStringMapAt(map[string]interface{}{"metadata": withStringifiedScalarMetadata(map[string]interface{}{"disable_global_guardrails": true})}, "metadata")
	if err != nil || model.Elements()["disable_global_guardrails"].(types.String).ValueString() != "true" {
		t.Fatalf("data source projection: %v err=%v", model, err)
	}
}
