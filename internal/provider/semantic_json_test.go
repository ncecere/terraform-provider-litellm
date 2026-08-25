package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSemanticJSONObjectStatePreservesSpellingAndPresence(t *testing.T) {
	t.Parallel()
	configured := `{ "z": 9.007199254740993e15, "a": { "enabled": true } }`
	state := types.StringValue(configured)
	response := map[string]interface{}{"info": map[string]interface{}{"a": map[string]interface{}{"enabled": true}, "z": mustJSONNumber(t, "9007199254740993")}}
	if err := updateJSONObjectStringState(&state, response, "info", true); err != nil {
		t.Fatal(err)
	}
	if state.ValueString() != configured {
		t.Fatalf("semantic spelling changed: %s", state.ValueString())
	}
	if err := updateJSONObjectStringState(&state, map[string]interface{}{"info": map[string]interface{}{}}, "info", true); err != nil || state.ValueString() != `{}` {
		t.Fatalf("empty object state=%q err=%v", state.ValueString(), err)
	}
	if err := updateJSONObjectStringState(&state, map[string]interface{}{"info": nil}, "info", true); err != nil || !state.IsNull() {
		t.Fatalf("null state=%#v err=%v", state, err)
	}
	if err := updateJSONObjectStringState(&state, map[string]interface{}{"info": "{}"}, "info", false); err == nil {
		t.Fatal("wrong API shape was silently ignored")
	}
}

func TestModelBudgetSemanticAliasesAndValidation(t *testing.T) {
	t.Parallel()
	configured := `{ "model": { "budget_limit": 1e1, "time_period": "1d", "tpm_limit": 9007199254740993 } }`
	observed := `{"model":{"max_budget":10,"budget_duration":"1d","tpm_limit":9007199254740993}}`
	if !modelBudgetSemanticallyEqual(configured, observed) {
		t.Fatal("BudgetConfig aliases or exact numbers were not normalized semantically")
	}
	if modelBudgetSemanticallyEqual(configured, `{"model":{"max_budget":11,"budget_duration":"1d","tpm_limit":9007199254740993}}`) {
		t.Fatal("actual budget drift was hidden")
	}
	state := types.StringValue(configured)
	var object map[string]interface{}
	if err := decodeJSONUseNumber([]byte(observed), &object); err != nil {
		t.Fatal(err)
	}
	if err := updateModelBudgetStringState(&state, map[string]interface{}{"model_max_budget": object}, "model_max_budget", true); err != nil {
		t.Fatal(err)
	}
	if state.ValueString() != configured {
		t.Fatalf("configured alias spelling changed: %s", state.ValueString())
	}
	for _, invalid := range []interface{}{
		map[string]interface{}{"model": map[string]interface{}{"ignored": 1}},
		map[string]interface{}{"model": []interface{}{}},
	} {
		if err := updateModelBudgetStringState(&state, map[string]interface{}{"model_max_budget": invalid}, "model_max_budget", true); err == nil {
			t.Fatalf("invalid model budget accepted: %#v", invalid)
		}
	}
}

func TestGuardrailModeUnion(t *testing.T) {
	t.Parallel()
	valid := []string{
		"pre_call",
		`["pre_call","post_call"]`,
		`{"tags":{"trusted":"logging_only","risky":["pre_call","post_call"]},"default":"pre_call"}`,
	}
	for _, value := range valid {
		decoded, err := decodeConfiguredGuardrailMode(value)
		if err != nil {
			t.Fatalf("valid mode %q: %v", value, err)
		}
		if object, ok := decoded.(map[string]interface{}); ok {
			encoded, present, err := guardrailModeFromAPI(map[string]interface{}{"mode": object})
			if err != nil || !present || !jsonSemanticallyEqual(value, encoded) {
				t.Fatalf("object mode round trip=%q present=%t err=%v", encoded, present, err)
			}
		}
	}
	for _, value := range []string{
		`{"default":"pre_call"}`,
		`{"tags":{"trusted":1}}`,
		`{"tags":{},"ignored":true}`,
		`["pre_call",1]`,
		`{"tags":`,
	} {
		if _, err := decodeConfiguredGuardrailMode(value); err == nil {
			t.Fatalf("invalid mode accepted: %s", value)
		}
	}
}

func TestJSONValidatorsDoNotEchoSensitiveInput(t *testing.T) {
	t.Parallel()
	const secret = "sentinel-super-secret"
	validators := []validator.String{jsonShapeStringValidator{shape: '{'}, tagModelBudgetValidator{}, budgetModelBudgetValidator{}, guardrailModeStringValidator{}}
	values := []string{`{"` + secret, `{"` + secret + `":[]}`, `{"model":{"` + secret + `":1}}`, `{"tags":{"x":"` + secret}
	for index, item := range validators {
		var response validator.StringResponse
		item.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("secret_json"), ConfigValue: types.StringValue(values[index])}, &response)
		if !response.Diagnostics.HasError() {
			t.Fatalf("validator %d accepted malformed JSON", index)
		}
		if strings.Contains(fmt.Sprint(response.Diagnostics), secret) {
			t.Fatalf("validator %d leaked configured JSON", index)
		}
	}
}

func TestBudgetAndSearchReadsPreserveSemanticJSONAndRejectWrongShape(t *testing.T) {
	t.Parallel()
	var wrongSearchShape atomic.Bool
	var wrongBudgetShape atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/budget/info":
			if wrongBudgetShape.Load() {
				_, _ = fmt.Fprint(writer, `[{}]`)
			} else {
				_, _ = fmt.Fprint(writer, `[{"budget_id":"budget","model_max_budget":{"model":{"max_budget":10,"budget_duration":"1d"}}}]`)
			}
		case "/search_tools/search":
			if wrongSearchShape.Load() {
				_, _ = fmt.Fprint(writer, `{}`)
			} else {
				_, _ = fmt.Fprint(writer, `{"search_tool_id":"search","search_tool_name":"search","litellm_params":{"search_provider":"provider"},"search_tool_info":{"z":2,"a":1}}`)
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}
	budgetSpelling := `{ "model": { "budget_limit": 1e1, "time_period": "1d" } }`
	budget := BudgetResourceModel{BudgetID: types.StringValue("budget"), ModelMaxBudget: types.StringValue(budgetSpelling)}
	budgetResource := &BudgetResource{client: client}
	if err := budgetResource.readBudget(context.Background(), &budget); err != nil || budget.ModelMaxBudget.ValueString() != budgetSpelling {
		t.Fatalf("budget read=%q err=%v", budget.ModelMaxBudget.ValueString(), err)
	}
	wrongBudgetShape.Store(true)
	if err := budgetResource.readBudget(context.Background(), &budget); err == nil {
		t.Fatal("budget response without identity was accepted")
	}
	wrongBudgetShape.Store(false)
	searchSpelling := `{ "a": 1, "z": 2 }`
	search := SearchToolResourceModel{SearchToolID: types.StringValue("search"), SearchToolInfo: types.StringValue(searchSpelling)}
	resource := &SearchToolResource{client: client}
	if err := resource.readSearchTool(context.Background(), &search); err != nil || search.SearchToolInfo.ValueString() != searchSpelling {
		t.Fatalf("search read=%q err=%v", search.SearchToolInfo.ValueString(), err)
	}
	wrongSearchShape.Store(true)
	if err := resource.readSearchTool(context.Background(), &search); err == nil {
		t.Fatal("search string response was accepted as an object")
	}
}

func mustJSONNumber(t *testing.T, value string) interface{} {
	t.Helper()
	var number interface{}
	if err := decodeJSONUseNumber([]byte(value), &number); err != nil {
		t.Fatal(err)
	}
	return number
}
