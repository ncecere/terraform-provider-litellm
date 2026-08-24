package provider

import (
	"context"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const modelImportedPrivateKey = "model_imported_v1"

// LiteLLM v1.98.0 injects this complete set into litellm_params for models
// created without additional_litellm_params. Older provider state has no
// configured-key metadata, so the full key/value signature identifies an
// adopted API-computed map. Checking both the complete set and values avoids
// misclassifying an explicitly configured non-default value by key name alone.
var legacyComputedAdditionalParamDefaults = map[string]string{
	"allow_client_keepalive_override":    "false",
	"merge_reasoning_content_in_choices": "false",
	"use_in_pass_through":                "false",
	"use_litellm_proxy":                  "false",
	"use_xai_oauth":                      "false",
}

type modelAdditionalParamsRemovalModifier struct{}

var _ planmodifier.Map = modelAdditionalParamsRemovalModifier{}

func (modelAdditionalParamsRemovalModifier) Description(context.Context) string {
	return "Replaces the model when configured additional LiteLLM parameter keys are removed."
}

func (m modelAdditionalParamsRemovalModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (modelAdditionalParamsRemovalModifier) PlanModifyMap(ctx context.Context, req planmodifier.MapRequest, resp *planmodifier.MapResponse) {
	if req.State.Raw.IsNull() || req.ConfigValue.IsUnknown() || req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		return
	}

	var configuredState types.Bool
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("additional_litellm_params_configured"), &configuredState)...)
	if resp.Diagnostics.HasError() {
		return
	}
	configured := !configuredState.IsNull() && !configuredState.IsUnknown() && configuredState.ValueBool()
	if configuredState.IsNull() || configuredState.IsUnknown() {
		configured = len(inferLegacyConfiguredAdditionalParamKeys(req.StateValue)) > 0
	}

	resp.PlanValue, resp.RequiresReplace = planAdditionalParamRemoval(
		req.ConfigValue,
		req.StateValue,
		req.PlanValue,
		configured,
	)
}

type modelAdditionalModelInfoRemovalModifier struct{}

var _ planmodifier.Map = modelAdditionalModelInfoRemovalModifier{}

func (modelAdditionalModelInfoRemovalModifier) Description(context.Context) string {
	return "Replaces the model when configured additional model information keys are removed."
}

func (m modelAdditionalModelInfoRemovalModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (modelAdditionalModelInfoRemovalModifier) PlanModifyMap(ctx context.Context, req planmodifier.MapRequest, resp *planmodifier.MapResponse) {
	if req.State.Raw.IsNull() || req.ConfigValue.IsUnknown() || req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		return
	}

	var configuredState types.Bool
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("additional_model_info_configured"), &configuredState)...)
	if resp.Diagnostics.HasError() {
		return
	}
	configured := !configuredState.IsNull() && !configuredState.IsUnknown() && configuredState.ValueBool()
	if configuredState.IsNull() || configuredState.IsUnknown() {
		configured = len(configuredAdditionalParamKeys(req.StateValue)) > 0
	}

	resp.PlanValue, resp.RequiresReplace = planAdditionalParamRemoval(
		req.ConfigValue,
		req.StateValue,
		req.PlanValue,
		configured,
	)
}

func planAdditionalParamRemoval(config, state, proposedPlan types.Map, configured bool) (types.Map, bool) {
	if config.IsUnknown() || state.IsNull() || state.IsUnknown() {
		return proposedPlan, false
	}

	// Optional+Computed maps retain prior state when their configuration is
	// removed. Override that proposed value only when state records that the
	// map was explicitly configured. Imported and API-computed-only maps remain
	// adopted when omitted.
	if config.IsNull() {
		if !configured {
			return state, false
		}
		// Leave the replacement result unknown so Create adopts API-computed
		// values exactly like an ordinary resource created with this argument
		// omitted. The remote model is still recreated without configured keys.
		return types.MapUnknown(types.StringType), true
	}

	// Once the map is explicitly configured, it describes the complete desired
	// set. Any state key omitted from it is a removal and must replace the model;
	// LiteLLM's update endpoints cannot reliably delete every parameter class.
	configKeys := mapKeys(config)
	for key := range state.Elements() {
		if _, present := configKeys[key]; !present {
			return proposedPlan, true
		}
	}

	return proposedPlan, false
}

func configuredAdditionalParamKeys(value types.Map) []string {
	if value.IsNull() || value.IsUnknown() {
		return []string{}
	}

	keys := make([]string, 0, len(value.Elements()))
	for key := range value.Elements() {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func inferLegacyConfiguredAdditionalParamKeys(value types.Map) []string {
	elements := value.Elements()
	matchesComputedSignature := len(elements) >= len(legacyComputedAdditionalParamDefaults)
	for key, want := range legacyComputedAdditionalParamDefaults {
		raw, present := elements[key]
		stringValue, isString := raw.(types.String)
		if !present || !isString || stringValue.IsNull() || stringValue.IsUnknown() || stringValue.ValueString() != want {
			matchesComputedSignature = false
			break
		}
	}
	if matchesComputedSignature {
		return []string{}
	}
	return configuredAdditionalParamKeys(value)
}

func mapKeys(value types.Map) map[string]struct{} {
	keys := make(map[string]struct{}, len(value.Elements()))
	for key := range value.Elements() {
		keys[key] = struct{}{}
	}
	return keys
}

type modelAdditionalParamsOwnershipModifier struct{}

var _ planmodifier.Bool = modelAdditionalParamsOwnershipModifier{}

func (modelAdditionalParamsOwnershipModifier) Description(context.Context) string {
	return "Records whether additional_litellm_params is explicitly configured."
}

func (m modelAdditionalParamsOwnershipModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (modelAdditionalParamsOwnershipModifier) PlanModifyBool(ctx context.Context, req planmodifier.BoolRequest, resp *planmodifier.BoolResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var configuredMap types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("additional_litellm_params"), &configuredMap)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.PlanValue = planAdditionalParamsOwnership(configuredMap)
}

type modelAdditionalModelInfoOwnershipModifier struct{}

var _ planmodifier.Bool = modelAdditionalModelInfoOwnershipModifier{}

func (modelAdditionalModelInfoOwnershipModifier) Description(context.Context) string {
	return "Records whether additional_model_info is explicitly configured."
}

func (m modelAdditionalModelInfoOwnershipModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (modelAdditionalModelInfoOwnershipModifier) PlanModifyBool(ctx context.Context, req planmodifier.BoolRequest, resp *planmodifier.BoolResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var configuredMap types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("additional_model_info"), &configuredMap)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.PlanValue = planAdditionalParamsOwnership(configuredMap)
}

func planAdditionalParamsOwnership(config types.Map) types.Bool {
	if config.IsUnknown() {
		return types.BoolUnknown()
	}
	return types.BoolValue(!config.IsNull())
}
