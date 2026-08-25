package provider

import (
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// apiJSONObject reads one object-or-null field without collapsing an invalid
// present value into absence. HTTP JSON has already been decoded with UseNumber.
func apiJSONObject(object map[string]interface{}, field string) (map[string]interface{}, apiValuePresence, error) {
	value, presence, err := apiValueAt(object, field)
	if err != nil || presence != apiValuePresent {
		return nil, presence, err
	}
	result, ok := value.(map[string]interface{})
	if !ok {
		return nil, presence, fmt.Errorf("invalid response field %q: expected an object or null", field)
	}
	return result, presence, nil
}

func canonicalJSONObject(object map[string]interface{}, field string) (string, error) {
	encoded, err := json.Marshal(object)
	if err != nil {
		return "", fmt.Errorf("invalid response field %q: could not encode object", field)
	}
	return string(encoded), nil
}

// reconcileJSONObjectString preserves the prior Terraform spelling when the
// authoritative object is semantically equal. This avoids formatting-only
// churn while still exposing actual remote changes.
func reconcileJSONObjectString(current types.String, object map[string]interface{}, field string) (types.String, error) {
	observed, err := canonicalJSONObject(object, field)
	if err != nil {
		return types.StringNull(), err
	}
	if !current.IsNull() && !current.IsUnknown() && jsonSemanticallyEqual(current.ValueString(), observed) {
		return current, nil
	}
	return types.StringValue(observed), nil
}

func normalizeModelBudgetObject(object map[string]interface{}) map[string]interface{} {
	normalized := make(map[string]interface{}, len(object))
	for model, raw := range object {
		config, ok := raw.(map[string]interface{})
		if !ok {
			normalized[model] = raw
			continue
		}
		entry := make(map[string]interface{}, len(config))
		for field, value := range config {
			switch field {
			case "budget_limit":
				entry["max_budget"] = value
			case "time_period":
				entry["budget_duration"] = value
			default:
				entry[field] = value
			}
		}
		normalized[model] = entry
	}
	return normalized
}

// modelBudgetSemanticallyEqual additionally treats LiteLLM BudgetConfig aliases
// as equivalent to their canonical response keys.
func modelBudgetSemanticallyEqual(configured, observed string) bool {
	var configuredValue, observedValue interface{}
	if decodeJSONUseNumber([]byte(configured), &configuredValue) != nil || decodeJSONUseNumber([]byte(observed), &observedValue) != nil {
		return false
	}
	configuredObject, configuredOK := configuredValue.(map[string]interface{})
	observedObject, observedOK := observedValue.(map[string]interface{})
	if !configuredOK || !observedOK {
		return false
	}
	configuredJSON, configuredErr := json.Marshal(normalizeModelBudgetObject(configuredObject))
	observedJSON, observedErr := json.Marshal(normalizeModelBudgetObject(observedObject))
	return configuredErr == nil && observedErr == nil && jsonSemanticallyEqual(string(configuredJSON), string(observedJSON))
}

func reconcileModelBudgetString(current types.String, object map[string]interface{}, field string) (types.String, error) {
	observed, err := canonicalJSONObject(object, field)
	if err != nil {
		return types.StringNull(), err
	}
	if !current.IsNull() && !current.IsUnknown() && modelBudgetSemanticallyEqual(current.ValueString(), observed) {
		return current, nil
	}
	return types.StringValue(observed), nil
}

func updateJSONObjectStringState(target *types.String, source map[string]interface{}, field string, adopt bool) error {
	object, presence, err := apiJSONObject(source, field)
	if err != nil {
		return err
	}
	if presence == apiValuePresent {
		if adopt {
			*target, err = reconcileJSONObjectString(*target, object, field)
		}
	} else if adopt {
		*target = types.StringNull()
	}
	return err
}

func updateModelBudgetStringState(target *types.String, source map[string]interface{}, field string, adopt bool) error {
	object, presence, err := apiJSONObject(source, field)
	if err != nil {
		return err
	}
	if presence == apiValuePresent {
		if _, validationErr := validateTagModelBudgetObject(object); validationErr != nil {
			return fmt.Errorf("invalid response field %q: expected BudgetConfig objects or finite legacy numeric values", field)
		}
		if adopt {
			*target, err = reconcileModelBudgetString(*target, object, field)
		}
	} else if adopt {
		*target = types.StringNull()
	}
	return err
}
