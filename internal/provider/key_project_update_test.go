package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestKeyProjectAssignmentChanges(t *testing.T) {
	for _, test := range []struct {
		name              string
		configured, prior types.String
		want              bool
	}{
		{"unchanged", types.StringValue("p1"), types.StringValue("p1"), false},
		{"detach", types.StringNull(), types.StringValue("p1"), false},
		{"first assignment", types.StringValue("p1"), types.StringNull(), true},
		{"reassignment", types.StringValue("p2"), types.StringValue("p1"), true},
		{"unknown configuration", types.StringUnknown(), types.StringValue("p1"), false},
	} {
		if got := keyProjectAssignmentChanges(test.configured, test.prior); got != test.want {
			t.Errorf("%s: got %t want %t", test.name, got, test.want)
		}
	}
}

func TestApplyKeyProjectAndSoftBudgetUpdateSemantics(t *testing.T) {
	prior := KeyResourceModel{ProjectID: types.StringValue("p1"), SoftBudget: types.Float64Value(5)}

	detach := map[string]interface{}{"soft_budget": 5.0}
	applyKeyProjectAndSoftBudgetUpdateSemantics(detach, KeyResourceModel{ProjectID: types.StringNull(), SoftBudget: types.Float64Value(5)}, prior)
	if value, sent := detach["project_id"]; !sent || value != nil {
		t.Fatalf("detach project_id=%#v sent=%t, want explicit null", value, sent)
	}
	if _, sent := detach["soft_budget"]; sent {
		t.Fatal("unchanged soft_budget was re-sent and could rewrite a shared budget")
	}

	change := map[string]interface{}{"project_id": "p1", "soft_budget": 7.0}
	applyKeyProjectAndSoftBudgetUpdateSemantics(change, KeyResourceModel{ProjectID: types.StringValue("p1"), SoftBudget: types.Float64Value(7)}, prior)
	if change["soft_budget"] != 7.0 || change["project_id"] != "p1" {
		t.Fatalf("changed soft_budget or retained project was dropped: %#v", change)
	}
}
