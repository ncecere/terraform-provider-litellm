package provider

import (
	"context"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

var reservedAdditionalModelInfoKeys = []string{
	"access_groups",
	"base_model",
	"created_at",
	"created_by",
	"db_model",
	"id",
	"mode",
	"team_id",
	"team_public_model_name",
	"tier",
	"updated_at",
	"updated_by",
}

type modelInfoReservedKeysValidator struct{}

var _ validator.Map = modelInfoReservedKeysValidator{}

func (modelInfoReservedKeysValidator) Description(context.Context) string {
	return "Keys managed by dedicated litellm_model attributes cannot be set in additional_model_info."
}

func (v modelInfoReservedKeysValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (modelInfoReservedKeysValidator) ValidateMap(ctx context.Context, req validator.MapRequest, resp *validator.MapResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	elements := req.ConfigValue.Elements()
	for _, key := range reservedAdditionalModelInfoKeys {
		if _, present := elements[key]; present {
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Reserved Additional Model Information Key",
				"additional_model_info cannot manage \""+key+"\" because the provider manages it through a dedicated litellm_model attribute.",
			)
		}
	}
	addModelInfoPricingKeyDiagnostics(req.Path, "additional_model_info", sortedKeys(elements), &resp.Diagnostics)
}

// addModelInfoPricingKeyDiagnostics rejects pricing keys in a model_info
// surface before any request is sent. Key names are not sensitive; values are
// never included.
func addModelInfoPricingKeyDiagnostics(attributePath path.Path, attribute string, keys []string, diagnostics *diag.Diagnostics) {
	for _, key := range keys {
		if isLiteLLMModelInfoPricingKey(key) {
			diagnostics.AddAttributeError(
				attributePath,
				"Custom Pricing Is Not Supported in Model Information",
				attribute+" cannot manage \""+key+"\". "+modelInfoPricingKeyDiagnostic,
			)
		}
	}
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// modelInfoJSONPricingKeyValidator applies the same pricing rule to the
// top-level members of additional_model_info_json. Malformed JSON is reported
// by modelSemanticDictionaryValidator instead.
type modelInfoJSONPricingKeyValidator struct{}

var _ validator.String = modelInfoJSONPricingKeyValidator{}

func (modelInfoJSONPricingKeyValidator) Description(context.Context) string {
	return "Top-level members cannot be LiteLLM pricing fields, which LiteLLM 1.102.0 and later ignore in model_info."
}

func (v modelInfoJSONPricingKeyValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (modelInfoJSONPricingKeyValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	object, err := parseSemanticDictionary(ctx, req.ConfigValue.ValueString())
	if err != nil {
		return
	}
	addModelInfoPricingKeyDiagnostics(req.Path, "additional_model_info_json", sortedKeys(object), &resp.Diagnostics)
}
