package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type guardrailModeStringValidator struct{}

var _ validator.String = guardrailModeStringValidator{}

func (guardrailModeStringValidator) Description(context.Context) string {
	return "Value must be a mode string or a valid JSON array of strings."
}
func (v guardrailModeStringValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (guardrailModeStringValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	value := strings.TrimSpace(req.ConfigValue.ValueString())
	if !strings.HasPrefix(value, "[") {
		return
	}
	var modes []string
	if err := decodeJSONUseNumber([]byte(value), &modes); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Guardrail Mode JSON", "A guardrail mode beginning with '[' must be a valid JSON array containing only strings.")
	}
}
