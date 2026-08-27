package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var teamManagedMetadataKeys = map[string]struct{}{
	"guardrails":            {},
	"model_rpm_limit":       {},
	"model_tpm_limit":       {},
	"prompts":               {},
	"rpm_limit_type":        {},
	"tags":                  {},
	"team_member_budget_id": {},
	"tpm_limit_type":        {},
}

// projectTeamInfoResponse decodes the response shape returned by LiteLLM v1.98
// without mutating prior. Keeping projection transactional is important: a
// malformed nested relation must not publish a partially refreshed state.
func projectTeamInfoResponse(ctx context.Context, prior TeamResourceModel, result map[string]interface{}, imported bool) (TeamResourceModel, error) {
	teamInfo, wrapped, err := unwrapTeamInfoResponse(result)
	if err != nil {
		return prior, err
	}

	teamID, err := requiredTeamString(teamInfo, "team_id")
	if err != nil {
		return prior, err
	}
	if prior.ID.IsNull() || prior.ID.IsUnknown() || prior.ID.ValueString() == "" || teamID != prior.ID.ValueString() {
		return prior, fmt.Errorf("invalid team response: team_info.team_id does not match the requested identity")
	}
	if wrapped {
		relations := map[string]bool{"keys": false, "team_memberships": false}
		for field, value := range result {
			switch field {
			case "team_id", "team_info":
			case "keys", "team_memberships":
				if _, ok := value.([]interface{}); !ok {
					return prior, fmt.Errorf("invalid team response: wrapped envelope relation has a malformed shape")
				}
				relations[field] = true
			default:
				return prior, fmt.Errorf("invalid team response: wrapped envelope contains a field outside its authoritative relation")
			}
		}
		if !relations["keys"] || !relations["team_memberships"] {
			return prior, fmt.Errorf("invalid team response: wrapped envelope is missing an authoritative relation")
		}
		rootID, presence, valueErr := apiValueAt(result, "team_id")
		if valueErr != nil {
			return prior, valueErr
		}
		if presence != apiValuePresent {
			return prior, fmt.Errorf("invalid team response field %q: expected a nonempty string", "team_id")
		}
		rootString, ok := rootID.(string)
		if !ok || rootString == "" || rootString != teamID {
			return prior, fmt.Errorf("invalid team response: root team_id does not match team_info.team_id")
		}
	}

	// These fields are accepted by create/update but are persisted in other
	// relations by v1.98. A flat copy is not an alternate authority.
	for _, field := range []string{
		"guardrails", "model_aliases", "model_rpm_limit", "model_tpm_limit",
		"prompts", "rpm_limit_type", "tags", "tpm_limit_type",
	} {
		if _, exists := teamInfo[field]; exists {
			return prior, fmt.Errorf("invalid team response field %q: value is outside its authoritative v1.98 relation", field)
		}
	}

	metadata, metadataPresence, err := optionalObjectAt(teamInfo, "metadata")
	if err != nil {
		return prior, err
	}
	modelTable, modelTablePresence, err := optionalObjectAt(teamInfo, "litellm_model_table")
	if err != nil {
		return prior, err
	}
	memberBudget, memberBudgetPresence, err := optionalObjectAt(teamInfo, "team_member_budget_table")
	if err != nil {
		return prior, err
	}

	next := prior
	next.ID = types.StringValue(teamID)
	next.TeamID = types.StringValue(teamID)

	// Although upstream permits a nullable team_alias, the existing public
	// Terraform contract requires it. Fail closed instead of retaining an old
	// alias or changing that compatibility contract.
	alias, err := requiredTeamString(teamInfo, "team_alias")
	if err != nil {
		return prior, err
	}
	next.TeamAlias = types.StringValue(alias)

	organizationOwned := imported || knownTeamString(prior.OrganizationID)
	if err := updateTeamOwnedString(&next.OrganizationID, teamInfo, organizationOwned, "organization_id"); err != nil {
		return prior, err
	}

	accessGroups, presence, err := teamStringSetAt(ctx, teamInfo, "access_group_ids")
	if err != nil {
		return prior, err
	}
	next.AccessGroupIDs = projectTeamSet(prior.AccessGroupIDs, accessGroups, presence)

	for _, field := range []struct {
		name   string
		target *types.Int64
		prior  types.Int64
	}{
		{"tpm_limit", &next.TPMLimit, prior.TPMLimit},
		{"rpm_limit", &next.RPMLimit, prior.RPMLimit},
	} {
		owned := imported || knownTeamInt64(field.prior)
		if err := updateInt64FromAPI(field.target, teamInfo, owned, owned, field.name); err != nil {
			return prior, err
		}
	}
	maxBudgetOwned := imported || knownTeamFloat64(prior.MaxBudget)
	if err := updateFloat64FromAPI(&next.MaxBudget, teamInfo, maxBudgetOwned, maxBudgetOwned, "max_budget"); err != nil {
		return prior, err
	}
	budgetDurationOwned := imported || knownTeamString(prior.BudgetDuration)
	if err := updateTeamOwnedString(&next.BudgetDuration, teamInfo, budgetDurationOwned, "budget_duration"); err != nil {
		return prior, err
	}

	if err := updateTeamBool(&next.Blocked, teamInfo, "blocked"); err != nil {
		return prior, err
	}

	limitMetadata := metadata
	if metadataPresence != apiValuePresent {
		limitMetadata = map[string]interface{}{}
	}
	for _, field := range []struct {
		name   string
		target *types.String
		prior  types.String
	}{
		{"tpm_limit_type", &next.TPMLimitType, prior.TPMLimitType},
		{"rpm_limit_type", &next.RPMLimitType, prior.RPMLimitType},
	} {
		owned := imported || knownTeamString(field.prior)
		if err := updateTeamOwnedString(field.target, limitMetadata, owned, field.name); err != nil {
			return prior, fmt.Errorf("invalid response field metadata.%s: %w", field.name, err)
		}
	}

	memberBudgetObject := memberBudget
	if memberBudgetPresence != apiValuePresent {
		memberBudgetObject = map[string]interface{}{}
	}
	memberBudgetOwned := imported || knownTeamFloat64(prior.TeamMemberBudget)
	if err := updateFloat64FromAPI(&next.TeamMemberBudget, memberBudgetObject, memberBudgetOwned, memberBudgetOwned, "max_budget"); err != nil {
		return prior, fmt.Errorf("invalid response field team_member_budget_table.max_budget: %w", err)
	}
	memberDurationOwned := imported || knownTeamString(prior.MemberBudgetDuration)
	if err := updateTeamOwnedString(&next.MemberBudgetDuration, memberBudgetObject, memberDurationOwned, "budget_duration"); err != nil {
		return prior, fmt.Errorf("invalid response field team_member_budget_table.budget_duration: %w", err)
	}
	memberRPMOwned := imported || knownTeamInt64(prior.TeamMemberRPMLimit)
	if err := updateInt64FromAPI(&next.TeamMemberRPMLimit, memberBudgetObject, memberRPMOwned, memberRPMOwned, "rpm_limit"); err != nil {
		return prior, fmt.Errorf("invalid response field team_member_budget_table.rpm_limit: %w", err)
	}
	memberTPMOwned := imported || knownTeamInt64(prior.TeamMemberTPMLimit)
	if err := updateInt64FromAPI(&next.TeamMemberTPMLimit, memberBudgetObject, memberTPMOwned, memberTPMOwned, "tpm_limit"); err != nil {
		return prior, fmt.Errorf("invalid response field team_member_budget_table.tpm_limit: %w", err)
	}

	models, modelsPresence, err := teamStringListAt(ctx, teamInfo, "models", path.Root("models"))
	if err != nil {
		return prior, err
	}
	next.Models = projectTeamList(prior.Models, models, modelsPresence)

	for _, field := range []struct {
		name   string
		target *types.List
		prior  types.List
	}{
		{"tags", &next.Tags, prior.Tags},
		{"guardrails", &next.Guardrails, prior.Guardrails},
		{"prompts", &next.Prompts, prior.Prompts},
	} {
		var value types.List
		var fieldPresence apiValuePresence
		if metadataPresence == apiValuePresent {
			value, fieldPresence, err = teamStringListAt(ctx, metadata, field.name, path.Root("metadata").AtMapKey(field.name))
		} else {
			value, fieldPresence = types.ListNull(types.StringType), metadataPresence
		}
		if err != nil {
			return prior, fmt.Errorf("invalid response field metadata.%s: %w", field.name, err)
		}
		*field.target = projectTeamList(field.prior, value, fieldPresence)
	}

	metadataState, err := projectTeamMetadata(ctx, prior.Metadata, metadata, metadataPresence, imported)
	if err != nil {
		return prior, err
	}
	next.Metadata = metadataState

	aliases, aliasesPresence, err := teamModelAliases(ctx, modelTable, modelTablePresence)
	if err != nil {
		return prior, err
	}
	next.ModelAliases = projectTeamMap(prior.ModelAliases, aliases, aliasesPresence)

	modelRPMOwned := imported || knownTeamMap(prior.ModelRPMLimit)
	if err := updateInt64MapFromAPI(&next.ModelRPMLimit, teamInfo, modelRPMOwned, modelRPMOwned, "metadata", "model_rpm_limit"); err != nil {
		return prior, err
	}
	modelTPMOwned := imported || knownTeamMap(prior.ModelTPMLimit)
	if err := updateInt64MapFromAPI(&next.ModelTPMLimit, teamInfo, modelTPMOwned, modelTPMOwned, "metadata", "model_tpm_limit"); err != nil {
		return prior, err
	}

	routerSettings, routerPresence, err := optionalObjectAt(teamInfo, "router_settings")
	if err != nil {
		return prior, err
	}
	if routerPresence == apiValuePresent {
		next.RouterSettings, err = parseTeamRouterSettings(ctx, routerSettings)
		if err != nil {
			return prior, err
		}
		if prior.RouterSettings.IsNull() && teamRouterSettingsEmpty(next.RouterSettings) {
			next.RouterSettings = prior.RouterSettings
		}
	} else {
		next.RouterSettings = types.ObjectNull(routerSettingsAttrTypes)
	}

	return next, nil
}

func unwrapTeamInfoResponse(result map[string]interface{}) (map[string]interface{}, bool, error) {
	if result == nil || len(result) == 0 {
		return nil, false, fmt.Errorf("invalid team response: expected a nonempty object")
	}
	value, exists := result["team_info"]
	if !exists {
		// Keep accepting the provider's historical flat fixture/deployment shape,
		// but decode it with the same strict field contract.
		return result, false, nil
	}
	if value == nil {
		return nil, true, fmt.Errorf("invalid team response envelope %q: expected an object, got null", "team_info")
	}
	object, ok := value.(map[string]interface{})
	if !ok {
		return nil, true, fmt.Errorf("invalid team response envelope %q: expected an object", "team_info")
	}
	if len(object) == 0 {
		return nil, true, fmt.Errorf("invalid team response envelope %q: expected a nonempty object", "team_info")
	}
	return object, true, nil
}

func optionalObjectAt(object map[string]interface{}, field string) (map[string]interface{}, apiValuePresence, error) {
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

func requiredTeamString(object map[string]interface{}, field string) (string, error) {
	value, presence, err := apiValueAt(object, field)
	if err != nil {
		return "", err
	}
	if presence != apiValuePresent {
		return "", fmt.Errorf("invalid team response field %q: expected a string, got null or omission", field)
	}
	result, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("invalid team response field %q: expected a string", field)
	}
	if field == "team_id" && result == "" {
		return "", fmt.Errorf("invalid team response field %q: expected a nonempty string", field)
	}
	return result, nil
}

func updateTeamOwnedString(target *types.String, object map[string]interface{}, owned bool, field string) error {
	value, presence, err := apiValueAt(object, field)
	if err != nil {
		return err
	}
	switch presence {
	case apiValuePresent:
		stringValue, ok := value.(string)
		if !ok {
			return fmt.Errorf("invalid response field %q: expected a string or null", field)
		}
		if owned {
			*target = types.StringValue(stringValue)
		} else if target.IsUnknown() {
			*target = types.StringNull()
		}
	case apiValueNull, apiValueAbsent:
		if owned || target.IsUnknown() {
			*target = types.StringNull()
		}
	}
	return nil
}

func updateTeamBool(target *types.Bool, object map[string]interface{}, field string) error {
	value, presence, err := apiValueAt(object, field)
	if err != nil {
		return err
	}
	if presence == apiValuePresent {
		boolean, ok := value.(bool)
		if !ok {
			return fmt.Errorf("invalid response field %q: expected a boolean or null", field)
		}
		*target = types.BoolValue(boolean)
	} else {
		*target = types.BoolNull()
	}
	return nil
}

func teamStringListAt(ctx context.Context, object map[string]interface{}, field string, valuePath path.Path) (types.List, apiValuePresence, error) {
	value, presence, diagnostics := strictAPIStringList(ctx, object, field, valuePath)
	if diagnostics.HasError() {
		return types.ListNull(types.StringType), presence, collectionProjectionError(ctx, diagnostics)
	}
	return value, presence, nil
}

func teamStringSetAt(ctx context.Context, object map[string]interface{}, field string) (types.Set, apiValuePresence, error) {
	list, presence, err := teamStringListAt(ctx, object, field, path.Root(field))
	if err != nil || presence != apiValuePresent {
		return types.SetNull(types.StringType), presence, err
	}
	set, diagnostics := checkedStringSetValue(ctx, list.Elements(), path.Root(field))
	if diagnostics.HasError() {
		return types.SetNull(types.StringType), presence, collectionProjectionError(ctx, diagnostics)
	}
	return set, presence, nil
}

func projectTeamList(prior, observed types.List, presence apiValuePresence) types.List {
	if presence == apiValuePresent {
		return observed
	}
	if !prior.IsNull() && !prior.IsUnknown() && len(prior.Elements()) == 0 {
		return prior
	}
	return types.ListNull(types.StringType)
}

func projectTeamSet(prior, observed types.Set, presence apiValuePresence) types.Set {
	if presence == apiValuePresent {
		return observed
	}
	if !prior.IsNull() && !prior.IsUnknown() && len(prior.Elements()) == 0 {
		return prior
	}
	return types.SetNull(types.StringType)
}

func projectTeamMap(prior, observed types.Map, presence apiValuePresence) types.Map {
	if presence == apiValuePresent {
		return observed
	}
	if !prior.IsNull() && !prior.IsUnknown() && len(prior.Elements()) == 0 {
		return prior
	}
	return types.MapNull(types.StringType)
}

func teamRouterSettingsEmpty(value types.Object) bool {
	if value.IsNull() || value.IsUnknown() {
		return value.IsNull()
	}
	for _, attribute := range value.Attributes() {
		list, ok := attribute.(types.List)
		if !ok || list.IsUnknown() || (!list.IsNull() && len(list.Elements()) != 0) {
			return false
		}
	}
	return true
}

func projectTeamMetadata(ctx context.Context, prior types.Map, metadata map[string]interface{}, presence apiValuePresence, imported bool) (types.Map, error) {
	if diagnostics := canceledCollectionDiagnostics(ctx, path.Root("metadata")); diagnostics.HasError() {
		return types.MapNull(types.StringType), collectionProjectionError(ctx, diagnostics)
	}
	if presence != apiValuePresent {
		return types.MapNull(types.StringType), nil
	}

	owned := map[string]attr.Value{}
	if !prior.IsNull() && !prior.IsUnknown() {
		for key, value := range prior.Elements() {
			owned[key] = value
		}
	} else if imported {
		for key, value := range metadata {
			if _, managed := teamManagedMetadataKeys[key]; !managed {
				if containsMaskedTeamMetadata(value) {
					return types.MapNull(types.StringType), fmt.Errorf("invalid response field %q: imported metadata contains an unrecoverable masked value", "metadata")
				}
				owned[key] = types.StringNull()
			}
		}
	} else {
		return types.MapNull(types.StringType), nil
	}

	projected := make(map[string]attr.Value, len(owned))
	for key, configured := range owned {
		if diagnostics := canceledCollectionDiagnostics(ctx, path.Root("metadata")); diagnostics.HasError() {
			return types.MapNull(types.StringType), collectionProjectionError(ctx, diagnostics)
		}
		if _, managed := teamManagedMetadataKeys[key]; managed {
			continue
		}
		remote, exists := metadata[key]
		if !exists {
			continue
		}
		if remote == nil {
			projected[key] = types.StringNull()
			continue
		}
		configuredString := ""
		if value, ok := configured.(types.String); ok && !value.IsNull() && !value.IsUnknown() {
			configuredString = value.ValueString()
		}
		projected[key] = types.StringValue(metadataValueToStringPreservingMasked(remote, configuredString))
	}
	result, diagnostics := checkedStringMapValue(ctx, projected, path.Root("metadata"), true)
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), collectionProjectionError(ctx, diagnostics)
	}
	return result, nil
}

func teamModelAliases(ctx context.Context, modelTable map[string]interface{}, tablePresence apiValuePresence) (types.Map, apiValuePresence, error) {
	if tablePresence != apiValuePresent {
		return types.MapNull(types.StringType), tablePresence, nil
	}
	raw, presence, err := apiValueAt(modelTable, "model_aliases")
	if err != nil || presence != apiValuePresent {
		return types.MapNull(types.StringType), presence, err
	}
	if encoded, ok := raw.(string); ok {
		var decoded interface{}
		if err := decodeJSONUseNumber([]byte(encoded), &decoded); err != nil {
			return types.MapNull(types.StringType), presence, fmt.Errorf("invalid response field %q: expected a JSON object of strings", "litellm_model_table.model_aliases")
		}
		raw = decoded
	}
	object, ok := raw.(map[string]interface{})
	if !ok {
		return types.MapNull(types.StringType), presence, fmt.Errorf("invalid response field %q: expected an object of strings or null", "litellm_model_table.model_aliases")
	}
	values := make(map[string]attr.Value, len(object))
	for key, rawValue := range object {
		if diagnostics := canceledCollectionDiagnostics(ctx, path.Root("model_aliases")); diagnostics.HasError() {
			return types.MapNull(types.StringType), presence, collectionProjectionError(ctx, diagnostics)
		}
		value, ok := rawValue.(string)
		if !ok {
			return types.MapNull(types.StringType), presence, fmt.Errorf("invalid response field %q: an alias value is not a string", "litellm_model_table.model_aliases")
		}
		values[key] = types.StringValue(value)
	}
	result, diagnostics := checkedStringMapValue(ctx, values, path.Root("model_aliases"), true)
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), presence, collectionProjectionError(ctx, diagnostics)
	}
	return result, presence, nil
}

func parseTeamRouterSettings(ctx context.Context, object map[string]interface{}) (types.Object, error) {
	routerPath := path.Root("router_settings")
	if diagnostics := canceledCollectionDiagnostics(ctx, routerPath); diagnostics.HasError() {
		return types.ObjectNull(routerSettingsAttrTypes), collectionProjectionError(ctx, diagnostics)
	}
	attributes := map[string]attr.Value{}
	for _, field := range []string{"fallbacks", "context_window_fallbacks"} {
		fieldPath := routerPath.AtName(field)
		if diagnostics := canceledCollectionDiagnostics(ctx, fieldPath); diagnostics.HasError() {
			return types.ObjectNull(routerSettingsAttrTypes), collectionProjectionError(ctx, diagnostics)
		}
		value, presence, err := apiValueAt(object, field)
		if err != nil {
			return types.ObjectNull(routerSettingsAttrTypes), err
		}
		if presence != apiValuePresent {
			attributes[field] = types.ListNull(types.ObjectType{AttrTypes: fallbackEntryAttrTypes})
			continue
		}
		items, ok := value.([]interface{})
		if !ok {
			return types.ObjectNull(routerSettingsAttrTypes), fmt.Errorf("invalid response field router_settings.%s: expected a list or null", field)
		}
		entries := make([]attr.Value, 0, len(items))
		for index, item := range items {
			itemPath := fieldPath.AtListIndex(index)
			if diagnostics := canceledCollectionDiagnostics(ctx, itemPath); diagnostics.HasError() {
				return types.ObjectNull(routerSettingsAttrTypes), collectionProjectionError(ctx, diagnostics)
			}
			entry, ok := item.(map[string]interface{})
			if !ok || len(entry) != 1 {
				return types.ObjectNull(routerSettingsAttrTypes), fmt.Errorf("invalid response field router_settings.%s: entry %d must be a single-key object", field, index)
			}
			for model, rawFallbacks := range entry {
				fallbacks, ok := rawFallbacks.([]interface{})
				if !ok {
					return types.ObjectNull(routerSettingsAttrTypes), fmt.Errorf("invalid response field router_settings.%s: entry %d value must be a string list", field, index)
				}
				fallbackValues := make([]attr.Value, len(fallbacks))
				for fallbackIndex, rawFallback := range fallbacks {
					if diagnostics := canceledCollectionDiagnostics(ctx, itemPath); diagnostics.HasError() {
						return types.ObjectNull(routerSettingsAttrTypes), collectionProjectionError(ctx, diagnostics)
					}
					fallback, ok := rawFallback.(string)
					if !ok {
						return types.ObjectNull(routerSettingsAttrTypes), fmt.Errorf("invalid response field router_settings.%s: entry %d fallback %d is not a string", field, index, fallbackIndex)
					}
					fallbackValues[fallbackIndex] = types.StringValue(fallback)
				}
				fallbackList, diagnostics := types.ListValue(types.StringType, fallbackValues)
				if diagnostics.HasError() {
					return types.ObjectNull(routerSettingsAttrTypes), fmt.Errorf("invalid response field router_settings.%s: cannot build fallback state", field)
				}
				entryValue, diagnostics := types.ObjectValue(fallbackEntryAttrTypes, map[string]attr.Value{
					"model": types.StringValue(model), "fallback_models": fallbackList,
				})
				if diagnostics.HasError() {
					return types.ObjectNull(routerSettingsAttrTypes), fmt.Errorf("invalid response field router_settings.%s: cannot build entry state", field)
				}
				entries = append(entries, entryValue)
			}
		}
		if diagnostics := canceledCollectionDiagnostics(ctx, fieldPath); diagnostics.HasError() {
			return types.ObjectNull(routerSettingsAttrTypes), collectionProjectionError(ctx, diagnostics)
		}
		list, diagnostics := types.ListValue(types.ObjectType{AttrTypes: fallbackEntryAttrTypes}, entries)
		if diagnostics.HasError() {
			return types.ObjectNull(routerSettingsAttrTypes), fmt.Errorf("invalid response field router_settings.%s: cannot build list state", field)
		}
		attributes[field] = list
	}
	if diagnostics := canceledCollectionDiagnostics(ctx, routerPath); diagnostics.HasError() {
		return types.ObjectNull(routerSettingsAttrTypes), collectionProjectionError(ctx, diagnostics)
	}
	result, diagnostics := types.ObjectValue(routerSettingsAttrTypes, attributes)
	if diagnostics.HasError() {
		return types.ObjectNull(routerSettingsAttrTypes), fmt.Errorf("invalid response field router_settings: cannot build object state")
	}
	return result, nil
}

func projectTeamPermissions(ctx context.Context, prior types.List, result map[string]interface{}, expectedTeamID string) (types.List, error) {
	if result == nil || len(result) == 0 {
		return prior, fmt.Errorf("invalid team permissions response: expected a nonempty object")
	}
	teamID, err := requiredTeamString(result, "team_id")
	if err != nil || teamID != expectedTeamID {
		return prior, fmt.Errorf("invalid team permissions response: identity does not match the requested team")
	}
	permissions, presence, err := teamStringListAt(ctx, result, "team_member_permissions", path.Root("team_member_permissions"))
	if err != nil {
		return prior, fmt.Errorf("invalid team permissions response: %w", err)
	}
	if presence != apiValuePresent {
		return prior, fmt.Errorf("invalid team permissions response: permissions list is missing or null")
	}
	return permissions, nil
}

func containsMaskedTeamMetadata(value interface{}) bool {
	switch typed := value.(type) {
	case string:
		return isMaskedMetadataAPIString(typed)
	case map[string]interface{}:
		for _, child := range typed {
			if containsMaskedTeamMetadata(child) {
				return true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if containsMaskedTeamMetadata(child) {
				return true
			}
		}
	}
	return false
}

func knownTeamString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown()
}

func knownTeamInt64(value types.Int64) bool {
	return !value.IsNull() && !value.IsUnknown()
}

func knownTeamFloat64(value types.Float64) bool {
	return !value.IsNull() && !value.IsUnknown()
}

func knownTeamMap(value types.Map) bool {
	return !value.IsNull() && !value.IsUnknown()
}
