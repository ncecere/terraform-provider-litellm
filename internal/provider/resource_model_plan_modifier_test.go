package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPlanAdditionalParamRemoval(t *testing.T) {
	t.Parallel()

	state := testStringMap(map[string]string{
		"cache_control_injection_points": `[{"location":"system"}]`,
		"timeout":                        "60",
	})
	empty := testStringMap(map[string]string{})

	tests := []struct {
		name        string
		config      types.Map
		state       types.Map
		proposed    types.Map
		configured  bool
		wantPlan    types.Map
		wantReplace bool
	}{
		{
			name:        "omitted configured map becomes empty replacement",
			config:      types.MapNull(types.StringType),
			state:       state,
			proposed:    state,
			configured:  true,
			wantPlan:    types.MapUnknown(types.StringType),
			wantReplace: true,
		},
		{
			name:        "omitted computed map remains adopted",
			config:      types.MapNull(types.StringType),
			state:       state,
			proposed:    types.MapUnknown(types.StringType),
			configured:  false,
			wantPlan:    state,
			wantReplace: false,
		},
		{
			name:        "adopted identical map has no key removal",
			config:      state,
			state:       state,
			proposed:    state,
			configured:  false,
			wantPlan:    state,
			wantReplace: false,
		},
		{
			name:        "explicit empty map replaces imported values",
			config:      empty,
			state:       state,
			proposed:    empty,
			configured:  false,
			wantPlan:    empty,
			wantReplace: true,
		},
		{
			name:   "individual key removal requires replacement",
			config: testStringMap(map[string]string{"timeout": "60"}),
			state:  state,
			proposed: testStringMap(map[string]string{
				"timeout": "60",
			}),
			configured:  true,
			wantPlan:    testStringMap(map[string]string{"timeout": "60"}),
			wantReplace: true,
		},
		{
			name:   "value change remains in place",
			config: testStringMap(map[string]string{"timeout": "120", "cache_control_injection_points": `[{"location":"system"}]`}),
			state:  state,
			proposed: testStringMap(map[string]string{
				"timeout":                        "120",
				"cache_control_injection_points": `[{"location":"system"}]`,
			}),
			configured: true,
			wantPlan: testStringMap(map[string]string{
				"timeout":                        "120",
				"cache_control_injection_points": `[{"location":"system"}]`,
			}),
			wantReplace: false,
		},
		{
			name:   "addition remains in place",
			config: testStringMap(map[string]string{"timeout": "60", "tags": `["test"]`, "cache_control_injection_points": `[{"location":"system"}]`}),
			state:  state,
			proposed: testStringMap(map[string]string{
				"timeout":                        "60",
				"tags":                           `["test"]`,
				"cache_control_injection_points": `[{"location":"system"}]`,
			}),
			configured: true,
			wantPlan: testStringMap(map[string]string{
				"timeout":                        "60",
				"tags":                           `["test"]`,
				"cache_control_injection_points": `[{"location":"system"}]`,
			}),
			wantReplace: false,
		},
		{
			name:        "unknown configuration is preserved",
			config:      types.MapUnknown(types.StringType),
			state:       state,
			proposed:    types.MapUnknown(types.StringType),
			configured:  true,
			wantPlan:    types.MapUnknown(types.StringType),
			wantReplace: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gotPlan, gotReplace := planAdditionalParamRemoval(test.config, test.state, test.proposed, test.configured)
			if !gotPlan.Equal(test.wantPlan) {
				t.Errorf("plan = %#v, want %#v", gotPlan, test.wantPlan)
			}
			if gotReplace != test.wantReplace {
				t.Errorf("requires replacement = %v, want %v", gotReplace, test.wantReplace)
			}
		})
	}
}

func TestInferLegacyConfiguredAdditionalParamKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state map[string]string
		want  []string
	}{
		{
			name: "complete API default signature is adopted",
			state: map[string]string{
				"allow_client_keepalive_override":    "false",
				"cache_control_injection_points":     `[{"location":"system"}]`,
				"merge_reasoning_content_in_choices": "false",
				"timeout":                            "60",
				"use_in_pass_through":                "false",
				"use_litellm_proxy":                  "false",
				"use_xai_oauth":                      "false",
			},
			want: []string{},
		},
		{
			name: "incomplete default signature remains configured",
			state: map[string]string{
				"allow_client_keepalive_override":    "false",
				"cache_control_injection_points":     `[{"location":"system"}]`,
				"merge_reasoning_content_in_choices": "false",
				"use_in_pass_through":                "false",
				"use_litellm_proxy":                  "false",
			},
			want: []string{"allow_client_keepalive_override", "cache_control_injection_points", "merge_reasoning_content_in_choices", "use_in_pass_through", "use_litellm_proxy"},
		},
		{
			name: "non-default value remains configured",
			state: map[string]string{
				"allow_client_keepalive_override":    "false",
				"merge_reasoning_content_in_choices": "false",
				"use_in_pass_through":                "false",
				"use_litellm_proxy":                  "true",
				"use_xai_oauth":                      "false",
			},
			want: []string{"allow_client_keepalive_override", "merge_reasoning_content_in_choices", "use_in_pass_through", "use_litellm_proxy", "use_xai_oauth"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got := inferLegacyConfiguredAdditionalParamKeys(testStringMap(test.state))
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("inferred configured keys = %v, want %v", got, test.want)
			}
		})
	}
}

func TestPlanAdditionalParamsOwnership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config types.Map
		want   types.Bool
	}{
		{"omitted", types.MapNull(types.StringType), types.BoolValue(false)},
		{"explicit empty", testStringMap(map[string]string{}), types.BoolValue(true)},
		{"explicit values", testStringMap(map[string]string{"timeout": "60"}), types.BoolValue(true)},
		{"unknown", types.MapUnknown(types.StringType), types.BoolUnknown()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := planAdditionalParamsOwnership(test.config); !got.Equal(test.want) {
				t.Fatalf("ownership plan = %v, want %v", got, test.want)
			}
		})
	}
}

func TestConfiguredAdditionalParamKeysSorted(t *testing.T) {
	t.Parallel()

	got := configuredAdditionalParamKeys(testStringMap(map[string]string{
		"z": "1",
		"a": "2",
		"m": "3",
	}))
	want := []string{"a", "m", "z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configured keys = %v, want %v", got, want)
	}
}

func TestModelComputedCollectionPlanModifiers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	(&ModelResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}

	accessGroups, ok := schemaResp.Schema.Attributes["access_groups"].(resourceschema.ListAttribute)
	if !ok {
		t.Fatal("access_groups is not a list attribute")
	}
	if len(accessGroups.PlanModifiers) != 1 {
		t.Fatalf("access_groups plan modifiers = %d, want 1", len(accessGroups.PlanModifiers))
	}
	if got := accessGroups.PlanModifiers[0].Description(ctx); got != "Once set, the value of this attribute in state will not change." {
		t.Fatalf("unexpected access_groups plan modifier: %q", got)
	}

	additionalParams, ok := schemaResp.Schema.Attributes["additional_litellm_params"].(resourceschema.MapAttribute)
	if !ok {
		t.Fatal("additional_litellm_params is not a map attribute")
	}
	if len(additionalParams.PlanModifiers) != 1 {
		t.Fatalf("additional_litellm_params plan modifiers = %d, want only the removal-aware modifier", len(additionalParams.PlanModifiers))
	}
	if got := additionalParams.PlanModifiers[0].Description(ctx); got != "Replaces the model when configured additional LiteLLM parameter keys are removed." {
		t.Fatalf("unexpected additional_litellm_params plan modifier: %q", got)
	}

	additionalInfo, ok := schemaResp.Schema.Attributes["additional_model_info"].(resourceschema.MapAttribute)
	if !ok {
		t.Fatal("additional_model_info is not a map attribute")
	}
	if len(additionalInfo.PlanModifiers) != 1 {
		t.Fatalf("additional_model_info plan modifiers = %d, want only the removal-aware modifier", len(additionalInfo.PlanModifiers))
	}
	if got := additionalInfo.PlanModifiers[0].Description(ctx); got != "Replaces the model when configured additional model information keys are removed." {
		t.Fatalf("unexpected additional_model_info plan modifier: %q", got)
	}
}

func TestModelAdditionalParamsRejectNullValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	(&ModelResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}
	attribute, ok := schemaResp.Schema.Attributes["additional_litellm_params"].(resourceschema.MapAttribute)
	if !ok {
		t.Fatal("additional_litellm_params is not a map attribute")
	}

	tests := []struct {
		name      string
		value     attr.Value
		wantError bool
	}{
		{"null element", types.StringNull(), true},
		{"unknown element", types.StringUnknown(), false},
		{"known element", types.StringValue("60"), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var validationResp validator.MapResponse
			request := validator.MapRequest{ConfigValue: types.MapValueMust(types.StringType, map[string]attr.Value{"timeout": test.value})}
			for _, mapValidator := range attribute.Validators {
				mapValidator.ValidateMap(ctx, request, &validationResp)
			}
			if got := validationResp.Diagnostics.HasError(); got != test.wantError {
				t.Fatalf("validation error = %v, want %v: %v", got, test.wantError, validationResp.Diagnostics)
			}
		})
	}
}

func TestModelAdditionalInfoValidators(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var schemaResp resource.SchemaResponse
	(&ModelResource{}).Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResp.Diagnostics)
	}
	attribute, ok := schemaResp.Schema.Attributes["additional_model_info"].(resourceschema.MapAttribute)
	if !ok {
		t.Fatal("additional_model_info is not a map attribute")
	}

	tests := []struct {
		name      string
		values    map[string]attr.Value
		wantError bool
	}{
		{"capability flag", map[string]attr.Value{"supports_vision": types.StringValue("true")}, false},
		{"dedicated reserved key", map[string]attr.Value{"base_model": types.StringValue("other")}, true},
		{"audit reserved key", map[string]attr.Value{"updated_by": types.StringValue("someone")}, true},
		{"null value", map[string]attr.Value{"supports_vision": types.StringNull()}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var validationResp validator.MapResponse
			request := validator.MapRequest{
				Path:        path.Root("additional_model_info"),
				ConfigValue: types.MapValueMust(types.StringType, test.values),
			}
			for _, mapValidator := range attribute.Validators {
				mapValidator.ValidateMap(ctx, request, &validationResp)
			}
			if got := validationResp.Diagnostics.HasError(); got != test.wantError {
				t.Fatalf("validation error = %v, want %v: %v", got, test.wantError, validationResp.Diagnostics)
			}
		})
	}
}

func testStringMap(values map[string]string) types.Map {
	elements := make(map[string]attr.Value, len(values))
	for key, value := range values {
		elements[key] = types.StringValue(value)
	}
	return types.MapValueMust(types.StringType, elements)
}
