package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// budgetTableState keeps relation presence separate from its contents. LiteLLM
// v1.98 returns organization and project limits only through this relation; a
// missing relation, an explicit null, and a malformed relation must not be
// collapsed into the same state transition.
type budgetTableState struct {
	object   map[string]interface{}
	presence apiValuePresence
}

func parseBudgetTable(owner map[string]interface{}) (budgetTableState, error) {
	value, exists := owner["litellm_budget_table"]
	if !exists {
		return budgetTableState{presence: apiValueAbsent}, nil
	}
	if value == nil {
		return budgetTableState{presence: apiValueNull}, nil
	}
	object, ok := value.(map[string]interface{})
	if !ok {
		return budgetTableState{}, fmt.Errorf("invalid response field %q: expected an object or null", "litellm_budget_table")
	}
	return budgetTableState{object: object, presence: apiValuePresent}, nil
}

func (b budgetTableState) value(field string) (interface{}, apiValuePresence, error) {
	if b.presence != apiValuePresent {
		return nil, b.presence, nil
	}
	return apiValueAt(b.object, field)
}

// budgetTableID validates both copies of the foreign key when LiteLLM returns
// them. Either copy may be omitted by historical/filtered deployments, but a
// present value must be a non-empty string and the two copies must agree.
func budgetTableID(owner map[string]interface{}, table budgetTableState) (string, apiValuePresence, error) {
	var topID string
	topPresence := apiValueAbsent
	if value, exists := owner["budget_id"]; exists {
		if value == nil {
			topPresence = apiValueNull
		} else {
			var ok bool
			topID, ok = value.(string)
			if !ok || topID == "" {
				return "", apiValuePresent, fmt.Errorf("invalid response field %q: expected a nonempty string", "budget_id")
			}
			topPresence = apiValuePresent
		}
	}

	if table.presence != apiValuePresent {
		return topID, topPresence, nil
	}
	nestedValue, nestedPresence, err := apiValueAt(table.object, "budget_id")
	if err != nil {
		return "", nestedPresence, err
	}
	if nestedPresence == apiValueNull {
		if topPresence == apiValuePresent {
			return "", apiValuePresent, fmt.Errorf("invalid response: budget_id is present while litellm_budget_table.budget_id is null")
		}
		return topID, topPresence, nil
	}
	if nestedPresence == apiValueAbsent {
		return topID, topPresence, nil
	}
	nestedID, ok := nestedValue.(string)
	if !ok || nestedID == "" {
		return "", apiValuePresent, fmt.Errorf("invalid response field %q: expected a nonempty string", "litellm_budget_table.budget_id")
	}
	if topPresence == apiValueNull {
		return "", apiValuePresent, fmt.Errorf("invalid response: budget_id is null while litellm_budget_table.budget_id is %q", nestedID)
	}
	if topPresence == apiValuePresent && topID != nestedID {
		return "", apiValuePresent, fmt.Errorf("invalid response: budget_id %q does not match litellm_budget_table.budget_id %q", topID, nestedID)
	}
	return nestedID, apiValuePresent, nil
}

func updateBudgetFloat64(target *types.Float64, table budgetTableState, clearAbsent, adoptPresent bool, field string) error {
	if table.presence == apiValuePresent {
		return updateFloat64FromAPI(target, table.object, clearAbsent, adoptPresent, field)
	}
	if clearAbsent || target.IsUnknown() {
		*target = types.Float64Null()
	}
	return nil
}

func updateBudgetInt64(target *types.Int64, table budgetTableState, clearAbsent, adoptPresent bool, field string) error {
	if table.presence == apiValuePresent {
		return updateInt64FromAPI(target, table.object, clearAbsent, adoptPresent, field)
	}
	if clearAbsent || target.IsUnknown() {
		*target = types.Int64Null()
	}
	return nil
}

func updateBudgetFloat64Map(target *types.Map, table budgetTableState, clearAbsent, adoptPresent bool, field string) error {
	if table.presence == apiValuePresent {
		return updateFloat64MapFromAPI(target, table.object, clearAbsent, adoptPresent, field)
	}
	if clearAbsent || target.IsUnknown() {
		*target = types.MapNull(types.Float64Type)
	}
	return nil
}

func updateBudgetDuration(target *types.String, table budgetTableState, clearAbsent, adoptPresent bool) error {
	value, presence, err := table.value("budget_duration")
	if err != nil {
		return err
	}
	switch presence {
	case apiValuePresent:
		duration, ok := value.(string)
		if !ok {
			return fmt.Errorf("invalid response field %q: expected a string", "litellm_budget_table.budget_duration")
		}
		if adoptPresent {
			*target = types.StringValue(duration)
		} else if target.IsUnknown() {
			*target = types.StringNull()
		}
	case apiValueNull:
		if adoptPresent || target.IsUnknown() {
			*target = types.StringNull()
		}
	case apiValueAbsent:
		if clearAbsent || target.IsUnknown() {
			*target = types.StringNull()
		}
	}
	return nil
}

func stringListFromAPI(object map[string]interface{}, field string) (types.List, apiValuePresence, error) {
	value, presence, err := apiValueAt(object, field)
	if err != nil || presence != apiValuePresent {
		return types.ListNull(types.StringType), presence, err
	}
	values, ok := value.([]interface{})
	if !ok {
		return types.ListNull(types.StringType), presence, fmt.Errorf("invalid response field %q: expected a list of strings", field)
	}
	items := make([]attr.Value, len(values))
	for index, raw := range values {
		stringValue, ok := raw.(string)
		if !ok {
			return types.ListNull(types.StringType), presence, fmt.Errorf("invalid response field %q: element %d is not a string", field, index)
		}
		items[index] = types.StringValue(stringValue)
	}
	result, diagnostics := types.ListValue(types.StringType, items)
	if diagnostics.HasError() {
		return types.ListNull(types.StringType), presence, fmt.Errorf("invalid response field %q: cannot build string list state", field)
	}
	return result, presence, nil
}

func stringMapFromAPI(object map[string]interface{}, field string, excluded ...string) (types.Map, apiValuePresence, error) {
	value, presence, err := apiValueAt(object, field)
	if err != nil || presence != apiValuePresent {
		return types.MapNull(types.StringType), presence, err
	}
	values, ok := value.(map[string]interface{})
	if !ok {
		return types.MapNull(types.StringType), presence, fmt.Errorf("invalid response field %q: expected an object or null", field)
	}
	excludedSet := make(map[string]struct{}, len(excluded))
	for _, key := range excluded {
		excludedSet[key] = struct{}{}
	}
	mapped := make(map[string]attr.Value, len(values))
	for key, raw := range values {
		if _, skip := excludedSet[key]; skip {
			continue
		}
		mapped[key] = types.StringValue(metadataValueToString(raw))
	}
	result, diagnostics := types.MapValue(types.StringType, mapped)
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), presence, fmt.Errorf("invalid response field %q: cannot build metadata state", field)
	}
	return result, presence, nil
}

// unwrapObjectEnvelope accepts the documented flat response plus known wrapper
// variants. A present wrapper is authoritative and must contain exactly an
// object; accepting null or a scalar would let import ownership be consumed by
// a non-authoritative 2xx response.
func unwrapObjectEnvelope(result map[string]interface{}, wrappers ...string) (map[string]interface{}, error) {
	selected := ""
	var object map[string]interface{}
	for _, wrapper := range wrappers {
		value, exists := result[wrapper]
		if !exists {
			continue
		}
		if selected != "" {
			return nil, fmt.Errorf("invalid response: multiple object envelopes %q and %q", selected, wrapper)
		}
		if value == nil {
			return nil, fmt.Errorf("invalid response envelope %q: expected an object, got null", wrapper)
		}
		var ok bool
		object, ok = value.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("invalid response envelope %q: expected an object", wrapper)
		}
		selected = wrapper
	}
	if selected != "" {
		return object, nil
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("invalid response: expected a nonempty object")
	}
	return result, nil
}
