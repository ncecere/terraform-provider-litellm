package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func requireExactRequestNumber(t *testing.T, value interface{}) {
	t.Helper()
	number, ok := value.(json.Number)
	if !ok || number.String() != "9007199254740993" {
		t.Fatalf("request number = %#v (%T)", value, value)
	}
}

func TestJSONRequestBuildersPreserveExactNumbersAndRejectInvalidJSON(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	exact := `{"large":9007199254740993}`
	invalid := `{"large":`
	budget, err := (&BudgetResource{}).buildBudgetRequest(ctx, &BudgetResourceModel{ModelMaxBudget: types.StringValue(exact)})
	if err != nil {
		t.Fatal(err)
	}
	requireExactRequestNumber(t, budget["model_max_budget"].(map[string]interface{})["large"])
	if _, err := (&BudgetResource{}).buildBudgetRequest(ctx, &BudgetResourceModel{ModelMaxBudget: types.StringValue(invalid)}); err == nil {
		t.Fatal("budget silently omitted invalid JSON")
	}
	tag, err := (&TagResource{}).buildTagRequest(ctx, &TagResourceModel{Name: types.StringValue("tag"), ModelMaxBudget: types.StringValue(exact)})
	if err != nil {
		t.Fatal(err)
	}
	requireExactRequestNumber(t, tag["model_max_budget"].(map[string]interface{})["large"])
	if _, err := (&TagResource{}).buildTagRequest(ctx, &TagResourceModel{Name: types.StringValue("tag"), ModelMaxBudget: types.StringValue(invalid)}); err == nil {
		t.Fatal("tag silently omitted invalid JSON")
	}
	search, err := (&SearchToolResource{}).buildSearchToolRequest(ctx, &SearchToolResourceModel{SearchToolName: types.StringValue("s"), SearchProvider: types.StringValue("p"), SearchToolInfo: types.StringValue(exact)})
	if err != nil {
		t.Fatal(err)
	}
	requireExactRequestNumber(t, search["search_tool_info"].(map[string]interface{})["large"])
	if _, err := (&SearchToolResource{}).buildSearchToolRequest(ctx, &SearchToolResourceModel{SearchToolName: types.StringValue("s"), SearchProvider: types.StringValue("p"), SearchToolInfo: types.StringValue(invalid)}); err == nil {
		t.Fatal("search tool accepted invalid JSON")
	}
	prompt, err := (&PromptResource{}).buildPromptRequest(ctx, &PromptResourceModel{PromptID: types.StringValue("p"), PromptIntegration: types.StringValue("x"), ProviderSpecificQueryParams: types.StringValue(exact)})
	if err != nil {
		t.Fatal(err)
	}
	requireExactRequestNumber(t, prompt["litellm_params"].(map[string]interface{})["provider_specific_query_params"].(map[string]interface{})["large"])
	if _, err := (&PromptResource{}).buildPromptRequest(ctx, &PromptResourceModel{PromptID: types.StringValue("p"), PromptIntegration: types.StringValue("x"), ProviderSpecificQueryParams: types.StringValue(invalid)}); err == nil {
		t.Fatal("prompt accepted invalid JSON")
	}
	guardrail, err := (&GuardrailResource{}).buildGuardrailRequest(ctx, &GuardrailResourceModel{GuardrailName: types.StringValue("g"), Guardrail: types.StringValue("x"), Mode: types.StringValue("pre_call"), LitellmParams: types.StringValue(exact)})
	if err != nil {
		t.Fatal(err)
	}
	requireExactRequestNumber(t, guardrail["guardrail"].(map[string]interface{})["litellm_params"].(map[string]interface{})["large"])
	if _, err := (&GuardrailResource{}).buildGuardrailRequest(ctx, &GuardrailResourceModel{GuardrailName: types.StringValue("g"), Guardrail: types.StringValue("x"), Mode: types.StringValue("[bad")}); err == nil {
		t.Fatal("guardrail accepted invalid mode JSON")
	}
	if _, err := (&GuardrailResource{}).buildGuardrailRequest(ctx, &GuardrailResourceModel{GuardrailName: types.StringValue("g"), Guardrail: types.StringValue("x"), Mode: types.StringValue("pre_call"), GuardrailInfo: types.StringValue(invalid)}); err == nil {
		t.Fatal("guardrail accepted invalid info JSON")
	}
}

func TestJSONAttributesRejectInvalidObjectsAtPlanTime(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	tests := []struct {
		name, attribute string
		r               resource.Resource
	}{{"budget", "model_max_budget", &BudgetResource{}}, {"tag", "model_max_budget", &TagResource{}}, {"search", "search_tool_info", &SearchToolResource{}}, {"prompt", "provider_specific_query_params", &PromptResource{}}, {"guardrail params", "litellm_params", &GuardrailResource{}}, {"guardrail info", "guardrail_info", &GuardrailResource{}}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var sr resource.SchemaResponse
			test.r.Schema(ctx, resource.SchemaRequest{}, &sr)
			attribute := sr.Schema.Attributes[test.attribute].(resourceschema.StringAttribute)
			if len(attribute.Validators) == 0 {
				t.Fatalf("%s has no JSON validator", test.attribute)
			}
			for _, v := range attribute.Validators {
				var response validator.StringResponse
				v.ValidateString(ctx, validator.StringRequest{Path: path.Root(test.attribute), ConfigValue: types.StringValue(`{"bad":`)}, &response)
				if !response.Diagnostics.HasError() {
					t.Fatalf("%s accepted invalid JSON", test.attribute)
				}
			}
		})
	}
}
