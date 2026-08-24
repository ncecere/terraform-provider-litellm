package provider

import (
	"context"

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
}
