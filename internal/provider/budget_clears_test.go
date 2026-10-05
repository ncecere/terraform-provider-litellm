package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestApplyUserBudgetClears(t *testing.T) {
	prior := UserResourceModel{MaxBudget: types.Float64Value(10), BudgetDuration: types.StringValue("30d"), TPMLimit: types.Int64Value(5)}
	request := map[string]interface{}{}
	applyUserBudgetClears(request, UserResourceModel{MaxBudget: types.Float64Null(), BudgetDuration: types.StringNull(), TPMLimit: types.Int64Null()}, prior)
	for _, name := range []string{"max_budget", "budget_duration"} {
		if value, sent := request[name]; !sent || value != nil {
			t.Errorf("%s=%#v sent=%t, want explicit null", name, value, sent)
		}
	}
	if _, sent := request["tpm_limit"]; sent {
		t.Error("tpm_limit null is dropped upstream and must not be sent")
	}
	unchanged := map[string]interface{}{}
	applyUserBudgetClears(unchanged, prior, prior)
	if len(unchanged) != 0 {
		t.Fatalf("unchanged budgets sent clears: %#v", unchanged)
	}
}

func TestApplyBudgetClears(t *testing.T) {
	prior := BudgetResourceModel{
		BudgetDuration: types.StringValue("1mo"), MaxBudget: types.Float64Value(100), SoftBudget: types.Float64Value(80),
		MaxParallelRequests: types.Int64Value(4), TPMLimit: types.Int64Value(1000), RPMLimit: types.Int64Value(10),
	}
	planned := BudgetResourceModel{
		BudgetDuration: types.StringNull(), MaxBudget: types.Float64Null(), SoftBudget: types.Float64Value(80),
		MaxParallelRequests: types.Int64Null(), TPMLimit: types.Int64Null(), RPMLimit: types.Int64Value(10),
	}
	request := map[string]interface{}{"soft_budget": 80.0, "rpm_limit": int64(10)}
	applyBudgetClears(request, planned, prior)
	for _, name := range []string{"budget_duration", "max_budget", "max_parallel_requests", "tpm_limit"} {
		if value, sent := request[name]; !sent || value != nil {
			t.Errorf("%s=%#v sent=%t, want explicit null", name, value, sent)
		}
	}
	if request["soft_budget"] != 80.0 || request["rpm_limit"] != int64(10) {
		t.Fatalf("retained values changed: %#v", request)
	}
}
