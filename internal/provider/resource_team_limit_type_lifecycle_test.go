package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func teamLimitTypeAttribute(t *testing.T, name string) resourceschema.StringAttribute {
	t.Helper()

	var response resource.SchemaResponse
	(&TeamResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	attribute, ok := response.Schema.Attributes[name].(resourceschema.StringAttribute)
	if !ok {
		t.Fatalf("%s schema type = %T, want schema.StringAttribute", name, response.Schema.Attributes[name])
	}
	return attribute
}

func TestTeamLimitTypesAreCreateOnlyInSchema(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"tpm_limit_type", "rpm_limit_type"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			attribute := teamLimitTypeAttribute(t, name)
			if !attribute.Optional || attribute.Required || attribute.Computed {
				t.Fatalf("%s must remain Optional-only: %#v", name, attribute)
			}
			if len(attribute.Validators) != 1 {
				t.Fatalf("%s validators = %d, want exact LiteLLM create validator", name, len(attribute.Validators))
			}
			if len(attribute.PlanModifiers) != 1 {
				t.Fatalf("%s plan modifiers = %d, want RequiresReplace", name, len(attribute.PlanModifiers))
			}
			if !strings.Contains(strings.ToLower(attribute.Description), "create-only") || !strings.Contains(strings.ToLower(attribute.Description), "replaces") {
				t.Fatalf("%s description does not document create-only replacement: %q", name, attribute.Description)
			}
		})
	}
}

func TestTeamLimitTypeLifecycleRequiresReplacementOnlyForExistingChanges(t *testing.T) {
	t.Parallel()

	nullResource := tftypes.NewValue(tftypes.String, nil)
	existingResource := tftypes.NewValue(tftypes.String, "existing-team")
	tests := []struct {
		name        string
		stateRaw    tftypes.Value
		planRaw     tftypes.Value
		stateValue  types.String
		planValue   types.String
		wantReplace bool
	}{
		{
			name:       "create accepts configured value",
			stateRaw:   nullResource,
			planRaw:    existingResource,
			stateValue: types.StringNull(),
			planValue:  types.StringValue("guaranteed_throughput"),
		},
		{
			name:       "unchanged configured value",
			stateRaw:   existingResource,
			planRaw:    existingResource,
			stateValue: types.StringValue("guaranteed_throughput"),
			planValue:  types.StringValue("guaranteed_throughput"),
		},
		{
			name:       "unchanged legacy read value",
			stateRaw:   existingResource,
			planRaw:    existingResource,
			stateValue: types.StringValue("team"),
			planValue:  types.StringValue("team"),
		},
		{
			name:       "unconfigured imported value",
			stateRaw:   existingResource,
			planRaw:    existingResource,
			stateValue: types.StringNull(),
			planValue:  types.StringNull(),
		},
		{
			name:        "configured value changes",
			stateRaw:    existingResource,
			planRaw:     existingResource,
			stateValue:  types.StringValue("guaranteed_throughput"),
			planValue:   types.StringValue("best_effort_throughput"),
			wantReplace: true,
		},
		{
			name:        "configured value is removed",
			stateRaw:    existingResource,
			planRaw:     existingResource,
			stateValue:  types.StringValue("guaranteed_throughput"),
			planValue:   types.StringNull(),
			wantReplace: true,
		},
		{
			name:        "value is added to existing team",
			stateRaw:    existingResource,
			planRaw:     existingResource,
			stateValue:  types.StringNull(),
			planValue:   types.StringValue("guaranteed_throughput"),
			wantReplace: true,
		},
	}

	for _, attributeName := range []string{"tpm_limit_type", "rpm_limit_type"} {
		attributeName := attributeName
		t.Run(attributeName, func(t *testing.T) {
			t.Parallel()
			modifier := teamLimitTypeAttribute(t, attributeName).PlanModifiers[0]
			for _, test := range tests {
				test := test
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					request := planmodifier.StringRequest{
						State:      tfsdk.State{Raw: test.stateRaw},
						Plan:       tfsdk.Plan{Raw: test.planRaw},
						StateValue: test.stateValue,
						PlanValue:  test.planValue,
					}
					response := planmodifier.StringResponse{PlanValue: test.planValue}
					modifier.PlanModifyString(context.Background(), request, &response)
					if response.Diagnostics.HasError() {
						t.Fatalf("plan modifier diagnostics: %v", response.Diagnostics)
					}
					if response.RequiresReplace != test.wantReplace {
						t.Fatalf("RequiresReplace = %t, want %t", response.RequiresReplace, test.wantReplace)
					}
				})
			}
		})
	}
}

func TestTeamLimitTypeCreateAndUpdatePayloadsMatchEndpointContracts(t *testing.T) {
	t.Parallel()

	data := &TeamResourceModel{
		TeamAlias:    types.StringValue("contract-team"),
		TPMLimitType: types.StringValue("best_effort_throughput"),
		RPMLimitType: types.StringValue("guaranteed_throughput"),
	}
	teamResource := &TeamResource{}
	createRequest := teamResource.buildTeamRequest(context.Background(), data, "team-contract")
	updateRequest := teamResource.buildTeamUpdateRequest(context.Background(), data, "team-contract")

	for name, want := range map[string]string{
		"tpm_limit_type": "best_effort_throughput",
		"rpm_limit_type": "guaranteed_throughput",
	} {
		if got := createRequest[name]; got != want {
			t.Errorf("create %s = %#v, want %q", name, got, want)
		}
		if _, exists := updateRequest[name]; exists {
			t.Errorf("create-only %s was included in team update: %#v", name, updateRequest[name])
		}
	}
}
