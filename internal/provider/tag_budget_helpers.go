package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type tagModelBudgetValidator struct{}
type budgetModelBudgetValidator struct{}

var _ validator.String = tagModelBudgetValidator{}
var _ validator.String = budgetModelBudgetValidator{}

func (tagModelBudgetValidator) Description(context.Context) string {
	return "Value must be a nonempty JSON object whose model values use LiteLLM GenericBudgetConfig objects."
}

func (v tagModelBudgetValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v tagModelBudgetValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var decoded interface{}
	if err := decodeJSONUseNumber([]byte(req.ConfigValue.ValueString()), &decoded); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Tag Model Budget JSON", v.Description(ctx))
		return
	}
	object, ok := decoded.(map[string]interface{})
	if !ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Tag Model Budget JSON", v.Description(ctx))
		return
	}
	if len(object) == 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Unsupported Empty Tag Model Budget", "LiteLLM v1.98 cannot persist an empty model_max_budget object through either tag or budget management APIs. Keep an existing value configured; clearing it requires database administration outside this API-only provider.")
		return
	}
	legacy, err := validateTagModelBudgetObject(object)
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Tag Model Budget", err.Error())
		return
	}
	if legacy {
		resp.Diagnostics.AddAttributeWarning(req.Path, "Legacy Scalar Tag Model Budget", "Scalar model_max_budget values remain accepted only for backward compatibility with earlier provider documentation and v1.98's unvalidated tag-create path. LiteLLM v1.98 requires GenericBudgetConfig objects for subsequent nonempty updates; migrate each model value to an object.")
	}
}

func (budgetModelBudgetValidator) Description(context.Context) string {
	return "Value must be a JSON object whose model values use LiteLLM BudgetConfig objects."
}

func (v budgetModelBudgetValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v budgetModelBudgetValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var decoded interface{}
	if decodeJSONUseNumber([]byte(req.ConfigValue.ValueString()), &decoded) != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Budget Model JSON", v.Description(ctx))
		return
	}
	object, ok := decoded.(map[string]interface{})
	if !ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Budget Model JSON", v.Description(ctx))
		return
	}
	legacy, err := validateTagModelBudgetObject(object)
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Budget Model JSON", err.Error())
		return
	}
	if legacy {
		resp.Diagnostics.AddAttributeWarning(req.Path, "Legacy Scalar Budget Model", "Finite scalar model budgets are accepted only for unchanged historical configuration. LiteLLM v1.98 requires BudgetConfig objects for new or changed values.")
	}
}

func validateTagModelBudgetObject(object map[string]interface{}) (bool, error) {
	legacy := false
	for _, raw := range object {
		config, structured := raw.(map[string]interface{})
		if !structured {
			if _, err := float64FromAPI(raw); err != nil {
				return false, fmt.Errorf("each model_max_budget value must be a GenericBudgetConfig object or a finite legacy numeric scalar")
			}
			legacy = true
			continue
		}
		if _, canonical := config["max_budget"]; canonical {
			if _, alias := config["budget_limit"]; alias {
				return false, fmt.Errorf("a model_max_budget entry cannot contain both max_budget and budget_limit")
			}
		}
		if _, canonical := config["budget_duration"]; canonical {
			if _, alias := config["time_period"]; alias {
				return false, fmt.Errorf("a model_max_budget entry cannot contain both budget_duration and time_period")
			}
		}
		for field, value := range config {
			if value == nil {
				continue
			}
			switch field {
			case "max_budget", "budget_limit":
				if _, err := float64FromAPI(value); err != nil {
					return false, fmt.Errorf("model_max_budget numeric limits must be finite numbers or null")
				}
			case "budget_duration", "time_period":
				if _, ok := value.(string); !ok {
					return false, fmt.Errorf("model_max_budget durations must be strings or null")
				}
			case "tpm_limit", "rpm_limit":
				if _, err := exactInt64FromAPI(value); err != nil {
					return false, fmt.Errorf("model_max_budget rate limits must be exact integers or null")
				}
			default:
				return false, fmt.Errorf("model_max_budget contains an unsupported BudgetConfig field; LiteLLM v1.98 silently ignores unknown fields")
			}
		}
	}
	return legacy, nil
}

type tagBudgetResetPending struct {
	BudgetID       string `json:"budget_id"`
	BudgetDuration string `json:"budget_duration"`
}

func encodeTagBudgetResetPending(budgetID, duration string) []byte {
	if budgetID == "" || duration == "" {
		return nil
	}
	encoded, _ := json.Marshal(tagBudgetResetPending{BudgetID: budgetID, BudgetDuration: duration})
	return encoded
}

func decodeTagBudgetResetPending(raw []byte) (*tagBudgetResetPending, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var pending tagBudgetResetPending
	if err := json.Unmarshal(raw, &pending); err != nil || pending.BudgetID == "" || pending.BudgetDuration == "" {
		return nil, fmt.Errorf("invalid retained tag budget reset state")
	}
	return &pending, nil
}

var tagBudgetControlNames = []string{
	"max_budget",
	"soft_budget",
	"max_parallel_requests",
	"tpm_limit",
	"rpm_limit",
	"budget_duration",
	"model_max_budget",
}

type tagBudgetTargets struct {
	budgetID            *types.String
	maxBudget           *types.Float64
	softBudget          *types.Float64
	maxParallelRequests *types.Int64
	tpmLimit            *types.Int64
	rpmLimit            *types.Int64
	budgetDuration      *types.String
	modelMaxBudget      *types.String
}

func tagResourceBudgetTargets(data *TagResourceModel) tagBudgetTargets {
	return tagBudgetTargets{
		budgetID: &data.BudgetID, maxBudget: &data.MaxBudget, softBudget: &data.SoftBudget,
		maxParallelRequests: &data.MaxParallelRequests, tpmLimit: &data.TPMLimit, rpmLimit: &data.RPMLimit,
		budgetDuration: &data.BudgetDuration, modelMaxBudget: &data.ModelMaxBudget,
	}
}

func tagDataSourceBudgetTargets(data *TagDataSourceModel) tagBudgetTargets {
	return tagBudgetTargets{
		budgetID: &data.BudgetID, maxBudget: &data.MaxBudget, softBudget: &data.SoftBudget,
		maxParallelRequests: &data.MaxParallelRequests, tpmLimit: &data.TPMLimit, rpmLimit: &data.RPMLimit,
		budgetDuration: &data.BudgetDuration, modelMaxBudget: &data.ModelMaxBudget,
	}
}

func tagListBudgetTargets(data *TagListItemModel) tagBudgetTargets {
	return tagBudgetTargets{
		budgetID: &data.BudgetID, maxBudget: &data.MaxBudget, softBudget: &data.SoftBudget,
		maxParallelRequests: &data.MaxParallelRequests, tpmLimit: &data.TPMLimit, rpmLimit: &data.RPMLimit,
		budgetDuration: &data.BudgetDuration, modelMaxBudget: &data.ModelMaxBudget,
	}
}

// updateTagBudgetState strictly validates the complete v1.98 budget relation.
// Ownership only controls whether a valid remote value is adopted; malformed
// present values always fail instead of being hidden by an omitted attribute.
func updateTagBudgetState(targets tagBudgetTargets, owner map[string]interface{}, imported bool, adoptAll bool) error {
	table, err := parseBudgetTable(owner)
	if err != nil {
		return err
	}
	budgetID, budgetIDPresence, err := authoritativeTagBudgetID(owner, table)
	if err != nil {
		return err
	}
	switch budgetIDPresence {
	case apiValuePresent:
		*targets.budgetID = types.StringValue(budgetID)
	case apiValueAbsent, apiValueNull:
		*targets.budgetID = types.StringNull()
	}

	for _, field := range []struct {
		name   string
		target *types.Float64
	}{
		{"max_budget", targets.maxBudget},
		{"soft_budget", targets.softBudget},
	} {
		if table.presence == apiValuePresent {
			if _, _, err := apiFloat64At(table.object, field.name); err != nil {
				return err
			}
		}
		owned := adoptAll || imported || knownFloat(*field.target)
		if err := updateBudgetFloat64(field.target, table, owned, owned, field.name); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"max_parallel_requests", targets.maxParallelRequests},
		{"tpm_limit", targets.tpmLimit},
		{"rpm_limit", targets.rpmLimit},
	} {
		if table.presence == apiValuePresent {
			if _, _, err := apiInt64At(table.object, field.name); err != nil {
				return err
			}
		}
		owned := adoptAll || imported || knownInt(*field.target)
		if err := updateBudgetInt64(field.target, table, owned, owned, field.name); err != nil {
			return err
		}
	}
	durationOwned := adoptAll || imported || knownString(*targets.budgetDuration)
	if err := updateBudgetDuration(targets.budgetDuration, table, durationOwned, durationOwned); err != nil {
		return err
	}
	modelOwned := adoptAll || imported || knownString(*targets.modelMaxBudget)
	return updateTagModelMaxBudget(targets.modelMaxBudget, table, modelOwned)
}

func authoritativeTagBudgetID(owner map[string]interface{}, table budgetTableState) (string, apiValuePresence, error) {
	if table.presence != apiValuePresent {
		if value, exists := owner["budget_id"]; exists && value != nil {
			return "", apiValuePresent, fmt.Errorf("invalid tag response: budget_id is present without an authoritative litellm_budget_table relation")
		}
		return "", table.presence, nil
	}
	value, presence, err := apiValueAt(table.object, "budget_id")
	if err != nil {
		return "", presence, err
	}
	if presence != apiValuePresent {
		return "", presence, fmt.Errorf("invalid tag response: litellm_budget_table is missing required budget_id")
	}
	budgetID, ok := value.(string)
	if !ok || budgetID == "" {
		return "", presence, fmt.Errorf("invalid response field %q: expected a nonempty string", "litellm_budget_table.budget_id")
	}
	if top, exists := owner["budget_id"]; exists {
		if top == nil {
			return "", presence, fmt.Errorf("invalid tag response: budget_id is null while litellm_budget_table.budget_id is %q", budgetID)
		}
		topID, ok := top.(string)
		if !ok || topID != budgetID {
			return "", presence, fmt.Errorf("invalid tag response: top-level budget_id does not match litellm_budget_table.budget_id %q", budgetID)
		}
	}
	return budgetID, apiValuePresent, nil
}

func configuredModelBudgetIsLegacy(value types.String) (bool, error) {
	if !knownString(value) {
		return false, nil
	}
	var decoded interface{}
	if err := decodeJSONUseNumber([]byte(value.ValueString()), &decoded); err != nil {
		return false, err
	}
	object, ok := decoded.(map[string]interface{})
	if !ok {
		return false, fmt.Errorf("model_max_budget must be a JSON object")
	}
	return validateTagModelBudgetObject(object)
}

func updateTagModelMaxBudget(target *types.String, table budgetTableState, adopt bool) error {
	value, presence, err := table.value("model_max_budget")
	if err != nil {
		return err
	}
	if presence != apiValuePresent {
		if adopt || target.IsUnknown() {
			*target = types.StringNull()
		}
		return nil
	}
	object, ok := value.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid response field %q: expected an object or null", "litellm_budget_table.model_max_budget")
	}
	if _, err := validateTagModelBudgetObject(object); err != nil {
		return fmt.Errorf("invalid response field %q: %w", "litellm_budget_table.model_max_budget", err)
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return fmt.Errorf("invalid response field %q: cannot encode JSON", "litellm_budget_table.model_max_budget")
	}
	observed := string(encoded)
	if !adopt {
		if target.IsUnknown() {
			*target = types.StringNull()
		}
		return nil
	}
	if knownString(*target) && modelBudgetSemanticallyEqual(target.ValueString(), observed) {
		return nil
	}
	*target = types.StringValue(observed)
	return nil
}

func encodeTagFieldSet(fields map[string]bool) []byte {
	values := make([]string, 0, len(fields))
	for _, name := range tagBudgetControlNames {
		if fields[name] {
			values = append(values, name)
		}
	}
	if len(values) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(values)
	return encoded
}

func decodeTagFieldSet(raw []byte) (map[string]bool, error) {
	result := map[string]bool{}
	if len(raw) == 0 {
		return result, nil
	}
	var fields []string
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("invalid retained tag budget ownership state")
	}
	allowed := map[string]bool{}
	for _, name := range tagBudgetControlNames {
		allowed[name] = true
	}
	for _, name := range fields {
		if !allowed[name] {
			return nil, fmt.Errorf("invalid retained tag budget ownership field %q", name)
		}
		result[name] = true
	}
	return result, nil
}

func allTagBudgetFields() map[string]bool {
	result := make(map[string]bool, len(tagBudgetControlNames))
	for _, name := range tagBudgetControlNames {
		result[name] = true
	}
	return result
}

func sortedTagFieldNames(fields map[string]bool) []string {
	result := make([]string, 0, len(fields))
	for name, included := range fields {
		if included {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}
