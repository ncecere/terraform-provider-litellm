package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

type guardrailModeStringValidator struct{}

var _ validator.String = guardrailModeStringValidator{}

func (guardrailModeStringValidator) Description(context.Context) string {
	return "Value must be a mode string, JSON string array, or JSON Mode object with a tags object and optional default string or string array."
}
func (v guardrailModeStringValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v guardrailModeStringValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := decodeConfiguredGuardrailMode(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Guardrail Mode", v.Description(ctx))
	}
}

func decodeConfiguredGuardrailMode(value string) (interface{}, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("mode must not be empty")
	}
	if !strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "{") {
		return trimmed, nil
	}
	var decoded interface{}
	if decodeJSONUseNumber([]byte(trimmed), &decoded) != nil {
		return nil, fmt.Errorf("invalid mode JSON")
	}
	if err := validateGuardrailModeValue(decoded); err != nil {
		return nil, err
	}
	if items, ok := decoded.([]interface{}); ok {
		modes := make([]string, 0, len(items))
		for _, item := range items {
			modes = append(modes, item.(string))
		}
		return modes, nil
	}
	return decoded, nil
}

func validateGuardrailModeValue(value interface{}) error {
	switch value := value.(type) {
	case string:
		if value == "" {
			return fmt.Errorf("mode string must not be empty")
		}
		return nil
	case []interface{}:
		return validateGuardrailModeStringOrArray(value)
	case []string:
		for _, item := range value {
			if item == "" {
				return fmt.Errorf("mode arrays must contain only non-empty strings")
			}
		}
		return nil
	case map[string]interface{}:
		for key := range value {
			if key != "tags" && key != "default" {
				return fmt.Errorf("mode object contains an unsupported field")
			}
		}
		tags, ok := value["tags"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("mode object requires a tags object")
		}
		for _, tagMode := range tags {
			if err := validateGuardrailModeStringOrArray(tagMode); err != nil {
				return err
			}
		}
		if defaultMode, exists := value["default"]; exists && defaultMode != nil {
			if err := validateGuardrailModeStringOrArray(defaultMode); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported mode shape")
	}
}

func validateGuardrailModeStringOrArray(value interface{}) error {
	if text, ok := value.(string); ok {
		if text == "" {
			return fmt.Errorf("mode values must not be empty")
		}
		return nil
	}
	if strings, ok := value.([]string); ok {
		for _, text := range strings {
			if text == "" {
				return fmt.Errorf("mode arrays must contain only non-empty strings")
			}
		}
		return nil
	}
	items, ok := value.([]interface{})
	if !ok {
		return fmt.Errorf("mode values must be strings or string arrays")
	}
	for _, item := range items {
		text, ok := item.(string)
		if !ok || text == "" {
			return fmt.Errorf("mode arrays must contain only non-empty strings")
		}
	}
	return nil
}
