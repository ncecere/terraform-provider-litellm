package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type redactingKeyHashValidator struct{}

var _ validator.String = redactingKeyHashValidator{}

func (redactingKeyHashValidator) Description(context.Context) string {
	return "Value must use the sha256:<64-hex> management identifier format."
}

func (v redactingKeyHashValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v redactingKeyHashValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if !keyHashIDPattern.MatchString(req.ConfigValue.ValueString()) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Key Hash", v.Description(ctx))
	}
}
