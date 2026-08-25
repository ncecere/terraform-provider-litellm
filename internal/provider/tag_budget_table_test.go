package provider

import (
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestTagBudgetStateReadsAuthoritativeNestedRelation(t *testing.T) {
	data := TagResourceModel{
		MaxBudget: types.Float64Value(1), SoftBudget: types.Float64Value(1),
		MaxParallelRequests: types.Int64Value(1), TPMLimit: types.Int64Value(1), RPMLimit: types.Int64Value(1),
		BudgetDuration: types.StringValue("1d"), ModelMaxBudget: types.StringValue(`{ "model-b": {"max_budget": 2}, "model-a": {"rpm_limit": 3} }`),
	}
	object := map[string]interface{}{
		"budget_id": "budget-1",
		"litellm_budget_table": map[string]interface{}{
			"budget_id": "budget-1", "max_budget": json.Number("12.5"), "soft_budget": json.Number("9.25"),
			"max_parallel_requests": json.Number("12"), "tpm_limit": json.Number("9007199254740993"), "rpm_limit": json.Number("81"),
			"budget_duration": "30d", "model_max_budget": map[string]interface{}{
				"model-a": map[string]interface{}{"rpm_limit": json.Number("3")},
				"model-b": map[string]interface{}{"max_budget": json.Number("2.0")},
			},
		},
	}
	if err := updateTagBudgetState(tagResourceBudgetTargets(&data), object, false, false); err != nil {
		t.Fatal(err)
	}
	if data.BudgetID.ValueString() != "budget-1" || data.MaxBudget.ValueFloat64() != 12.5 || data.TPMLimit.ValueInt64() != 9007199254740993 || data.BudgetDuration.ValueString() != "30d" {
		t.Fatalf("nested budget not projected: %#v", data)
	}
	if got := data.ModelMaxBudget.ValueString(); got != `{ "model-b": {"max_budget": 2}, "model-a": {"rpm_limit": 3} }` {
		t.Fatalf("semantically equal configured JSON spelling changed: %q", got)
	}
}

func TestTagBudgetStateDistinguishesNullMissingEmptyAndMalformed(t *testing.T) {
	for name, object := range map[string]struct {
		object   map[string]interface{}
		wantErr  bool
		wantJSON string
	}{
		"missing relation":          {object: map[string]interface{}{}},
		"null relation":             {object: map[string]interface{}{"litellm_budget_table": nil}},
		"empty relation":            {object: map[string]interface{}{"litellm_budget_table": map[string]interface{}{}}, wantErr: true},
		"empty model map":           {object: map[string]interface{}{"litellm_budget_table": map[string]interface{}{"budget_id": "budget", "model_max_budget": map[string]interface{}{}}}, wantJSON: `{}`},
		"malformed relation":        {object: map[string]interface{}{"litellm_budget_table": []interface{}{}}, wantErr: true},
		"malformed model map":       {object: map[string]interface{}{"litellm_budget_table": map[string]interface{}{"budget_id": "budget", "model_max_budget": []interface{}{}}}, wantErr: true},
		"malformed model child":     {object: map[string]interface{}{"litellm_budget_table": map[string]interface{}{"budget_id": "budget", "model_max_budget": map[string]interface{}{"model": []interface{}{}}}}, wantErr: true},
		"unknown model field":       {object: map[string]interface{}{"litellm_budget_table": map[string]interface{}{"budget_id": "budget", "model_max_budget": map[string]interface{}{"model": map[string]interface{}{"ignored": json.Number("1")}}}}, wantErr: true},
		"fractional model integer":  {object: map[string]interface{}{"litellm_budget_table": map[string]interface{}{"budget_id": "budget", "model_max_budget": map[string]interface{}{"model": map[string]interface{}{"tpm_limit": json.Number("1.5")}}}}, wantErr: true},
		"malformed unowned integer": {object: map[string]interface{}{"litellm_budget_table": map[string]interface{}{"budget_id": "budget", "tpm_limit": true}}, wantErr: true},
		"top-level-only ID":         {object: map[string]interface{}{"budget_id": "top"}, wantErr: true},
		"mismatched IDs":            {object: map[string]interface{}{"budget_id": "top", "litellm_budget_table": map[string]interface{}{"budget_id": "nested"}}, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			data := TagResourceModel{MaxBudget: types.Float64Value(3), ModelMaxBudget: types.StringNull()}
			err := updateTagBudgetState(tagResourceBudgetTargets(&data), object.object, false, name == "empty model map")
			if (err != nil) != object.wantErr {
				t.Fatalf("err=%v", err)
			}
			if object.wantErr {
				return
			}
			if name != "empty model map" && !data.BudgetID.IsNull() {
				t.Fatalf("budget ID was not cleared: %#v", data.BudgetID)
			}
			if name != "empty model map" && !data.MaxBudget.IsNull() {
				t.Fatalf("owned value survived absent/null table: %#v", data.MaxBudget)
			}
			if object.wantJSON != "" && data.ModelMaxBudget.ValueString() != object.wantJSON {
				t.Fatalf("model JSON=%q", data.ModelMaxBudget.ValueString())
			}
		})
	}
}

func TestTagSingleAndListBudgetProjectionParity(t *testing.T) {
	object := map[string]interface{}{"litellm_budget_table": map[string]interface{}{
		"budget_id": "budget-parity", "max_budget": json.Number("4.5"), "soft_budget": nil,
		"max_parallel_requests": json.Number("5"), "tpm_limit": json.Number("9007199254740993"), "rpm_limit": json.Number("7"),
		"budget_duration": "1d", "model_max_budget": map[string]interface{}{"model": map[string]interface{}{"max_budget": json.Number("2.25")}},
	}}
	var single TagDataSourceModel
	var listed TagListItemModel
	if err := updateTagBudgetState(tagDataSourceBudgetTargets(&single), object, false, true); err != nil {
		t.Fatal(err)
	}
	if err := updateTagBudgetState(tagListBudgetTargets(&listed), object, false, true); err != nil {
		t.Fatal(err)
	}
	if !single.BudgetID.Equal(listed.BudgetID) || !single.MaxBudget.Equal(listed.MaxBudget) || !single.SoftBudget.Equal(listed.SoftBudget) || !single.MaxParallelRequests.Equal(listed.MaxParallelRequests) || !single.TPMLimit.Equal(listed.TPMLimit) || !single.RPMLimit.Equal(listed.RPMLimit) || !single.BudgetDuration.Equal(listed.BudgetDuration) || !single.ModelMaxBudget.Equal(listed.ModelMaxBudget) {
		t.Fatalf("single/list projection mismatch: single=%#v list=%#v", single, listed)
	}
}

func TestTagRowEchoPreservesFieldsDuringAssociationUpdate(t *testing.T) {
	models, diagnostics := types.ListValueFrom(t.Context(), types.StringType, []string{"model-b", "model-a"})
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	state := TagResourceModel{Description: types.StringValue("keep"), Models: models}
	plan := TagResourceModel{Description: types.StringUnknown(), Models: types.ListUnknown(types.StringType)}
	request := map[string]interface{}{"budget_id": "budget"}
	if err := addTagRowEcho(t.Context(), request, &plan, &state); err != nil {
		t.Fatal(err)
	}
	if request["description"] != "keep" {
		t.Fatalf("description echo=%#v", request)
	}
	values, ok := request["models"].([]string)
	if !ok || len(values) != 2 || values[0] != "model-b" || values[1] != "model-a" {
		t.Fatalf("models echo=%#v", request)
	}
}

func TestTagBudgetUpdateRequestUsesExplicitClearSentinels(t *testing.T) {
	state := TagResourceModel{
		MaxBudget: types.Float64Value(10), SoftBudget: types.Float64Value(8), MaxParallelRequests: types.Int64Value(4),
		TPMLimit: types.Int64Value(100), RPMLimit: types.Int64Value(10), BudgetDuration: types.StringValue("30d"),
		ModelMaxBudget: types.StringNull(),
	}
	plan := TagResourceModel{
		MaxBudget: types.Float64Null(), SoftBudget: types.Float64Null(), MaxParallelRequests: types.Int64Null(),
		TPMLimit: types.Int64Null(), RPMLimit: types.Int64Null(), BudgetDuration: types.StringNull(), ModelMaxBudget: types.StringNull(),
	}
	request, changed, err := buildTagBudgetUpdateRequest(&plan, &state)
	if err != nil || !changed {
		t.Fatalf("request: changed=%t err=%v", changed, err)
	}
	for _, name := range []string{"max_budget", "soft_budget", "max_parallel_requests", "tpm_limit", "rpm_limit", "budget_duration", "budget_reset_at"} {
		value, exists := request[name]
		if !exists || value != nil {
			t.Fatalf("%s clear=%#v exists=%t", name, value, exists)
		}
	}
}

func TestTagModelBudgetClearFailsBeforeRequest(t *testing.T) {
	state := TagResourceModel{ModelMaxBudget: types.StringValue(`{"model":{"max_budget":2}}`)}
	plan := TagResourceModel{ModelMaxBudget: types.StringNull()}
	if _, _, err := buildTagBudgetUpdateRequest(&plan, &state); err == nil {
		t.Fatal("unsupported model budget clear produced a request")
	}
}

func TestTagPendingOwnershipRequiresExactAuthoritativeValue(t *testing.T) {
	desired := TagResourceModel{MaxBudget: types.Float64Value(42)}
	actual := TagResourceModel{MaxBudget: types.Float64Value(43)}
	if field, mismatch := tagPendingOwnershipMismatch(&desired, &actual, map[string]bool{"max_budget": true}); !mismatch || field != "max_budget" {
		t.Fatalf("concurrent drift was accepted: field=%q mismatch=%t", field, mismatch)
	}
	actual.MaxBudget = types.Float64Value(42)
	if field, mismatch := tagPendingOwnershipMismatch(&desired, &actual, map[string]bool{"max_budget": true}); mismatch {
		t.Fatalf("exact ownership transfer rejected: field=%q", field)
	}
}

func TestTagImportedBudgetFieldSetsAreIndependent(t *testing.T) {
	fields := allTagBudgetFields()
	delete(fields, "max_budget")
	decoded, err := decodeTagFieldSet(encodeTagFieldSet(fields))
	if err != nil {
		t.Fatal(err)
	}
	if decoded["max_budget"] || !decoded["soft_budget"] || len(decoded) != len(tagBudgetControlNames)-1 {
		t.Fatalf("decoded fields=%v", sortedTagFieldNames(decoded))
	}
	if _, err := decodeTagFieldSet([]byte(`["unknown"]`)); err == nil {
		t.Fatal("unknown retained ownership field was accepted")
	}
}
