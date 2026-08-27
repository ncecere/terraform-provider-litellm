package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func configuredMCPInfoFixedPointers(config MCPServerResourceModel) mcpInfoPointerSet {
	result := mcpInfoPointerSet{}
	for leaf, state := range mcpInfoConfiguredLeafStates(config) {
		if state == 1 {
			result[mcpInfoLeafToPointer[leaf]] = true
		}
	}
	return result
}

// override ownership is recursive for non-empty objects. Scalars, arrays,
// null, and nested empty objects are atomic owned members.
func mcpInfoOverrideOwnedPointers(object map[string]interface{}) mcpInfoPointerSet {
	result := mcpInfoPointerSet{}
	var walk func(map[string]interface{}, []string)
	walk = func(current map[string]interface{}, parent []string) {
		names := make([]string, 0, len(current))
		for name := range current {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			members := append(append([]string(nil), parent...), name)
			child, isObject := current[name].(map[string]interface{})
			if isObject && len(child) > 0 {
				walk(child, members)
				continue
			}
			result[encodeMCPInfoClearPointer(members)] = true
		}
	}
	walk(object, nil)
	return result
}

func configuredMCPInfoClearPaths(value types.List) ([]string, bool, error) {
	pointers := make([]string, 0, len(value.Elements()))
	for _, element := range value.Elements() {
		pointer, ok := element.(types.String)
		if !ok || pointer.IsNull() {
			return nil, false, fmt.Errorf("invalid clear pointer element")
		}
		if pointer.IsUnknown() {
			return nil, true, nil
		}
		pointers = append(pointers, pointer.ValueString())
	}
	return pointers, false, nil
}

func mcpInfoClearPointerSet(pointers []string) mcpInfoPointerSet {
	result := mcpInfoPointerSet{}
	for _, pointer := range pointers {
		result[pointer] = true
	}
	return result
}

func addMCPInfoConfigError(diagnostics *diag.Diagnostics, attribute path.Path, title, detail string) {
	diagnostics.AddAttributeError(attribute, title, detail)
}

func validateMCPInfoJSONConfig(data MCPServerResourceModel, diagnostics *diag.Diagnostics) {
	wholePresent := !data.MCPInfoJSON.IsNull()
	overridesPresent := !data.MCPInfoOverridesJSON.IsNull()
	clearsPresent := !data.MCPInfoClearPaths.IsNull()
	fixedPresent := data.MCPInfo != nil
	if wholePresent && (fixedPresent || overridesPresent || clearsPresent) {
		addMCPInfoConfigError(diagnostics, path.Root("mcp_info_json"), "Conflicting MCP Info Ownership", "mcp_info_json cannot be combined with mcp_info, mcp_info_overrides_json, or mcp_info_clear_paths.")
		return
	}

	if wholePresent && !data.MCPInfoJSON.IsUnknown() {
		if _, err := parseMCPInfoJSONObject(data.MCPInfoJSON.ValueString()); err != nil {
			addMCPInfoConfigError(diagnostics, path.Root("mcp_info_json"), "Invalid MCP Info JSON", "mcp_info_json must contain exactly one non-null JSON object with unique members.")
		}
	}
	var overridePointers mcpInfoPointerSet
	if overridesPresent && !data.MCPInfoOverridesJSON.IsUnknown() {
		object, err := parseMCPInfoJSONObject(data.MCPInfoOverridesJSON.ValueString())
		if err != nil {
			addMCPInfoConfigError(diagnostics, path.Root("mcp_info_overrides_json"), "Invalid MCP Info Overrides JSON", "mcp_info_overrides_json must contain exactly one non-null JSON object with unique members.")
		} else {
			overridePointers = mcpInfoOverrideOwnedPointers(object)
		}
	}
	var clearPointers []string
	if clearsPresent && !data.MCPInfoClearPaths.IsUnknown() {
		pointers, hasUnknown, err := configuredMCPInfoClearPaths(data.MCPInfoClearPaths)
		if err != nil {
			addMCPInfoConfigError(diagnostics, path.Root("mcp_info_clear_paths"), "Invalid MCP Info Clear Paths", "mcp_info_clear_paths must contain non-null string pointers.")
		} else if !hasUnknown {
			if canonical, err := canonicalMCPInfoClearPointers(pointers); err != nil {
				addMCPInfoConfigError(diagnostics, path.Root("mcp_info_clear_paths"), "Invalid MCP Info Clear Paths", "mcp_info_clear_paths must contain unique, non-overlapping canonical RFC 6901 object-member pointers. Root and array-element pointers are not supported.")
			} else {
				clearPointers = canonical
			}
		}
	}
	if diagnostics.HasError() {
		return
	}
	fixedPointers := configuredMCPInfoFixedPointers(data)
	clearSet := mcpInfoClearPointerSet(clearPointers)
	if mcpInfoPointerSetsConflict(fixedPointers, overridePointers, clearSet) {
		addMCPInfoConfigError(diagnostics, path.Root("mcp_info"), "Overlapping MCP Info Ownership", "Fixed MCP info fields, recursive overrides, and clear paths must own disjoint paths without equal or ancestor/descendant overlap.")
	}
}

func deriveMCPInfoJSONPlanProvenance(ctx context.Context, prior mcpInfoProvenance, config, state MCPServerResourceModel) (mcpInfoProvenance, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	// An unknown whole-document expression retains every prior ownership fact.
	if !config.MCPInfoJSON.IsNull() && config.MCPInfoJSON.IsUnknown() {
		return cloneMCPInfoProvenance(prior), diagnostics
	}

	result := deriveMCPInfoPlanProvenance(prior, config, state)
	if !config.MCPInfoJSON.IsNull() {
		result.Mode = mcpInfoModeWhole
		result.Terraform = mcpInfoLeafSet{}
		result.Fixed = mcpInfoPointerSet{}
		result.Overrides = mcpInfoPointerSet{}
		result.Clears = mcpInfoPointerSet{}
		result.API = mcpInfoLeafSet{}
	} else {
		if config.MCPInfoOverridesJSON.IsUnknown() {
			result.Overrides = cloneMCPInfoPointerSet(prior.Overrides)
		} else if config.MCPInfoOverridesJSON.IsNull() {
			result.Overrides = mcpInfoPointerSet{}
		} else if object, err := parseMCPInfoJSONObject(config.MCPInfoOverridesJSON.ValueString()); err == nil {
			result.Overrides = mcpInfoOverrideOwnedPointers(object)
		} else {
			addMCPInfoConfigError(&diagnostics, path.Root("mcp_info_overrides_json"), "Invalid MCP Info Overrides JSON", "The configured MCP info overrides could not be validated safely.")
		}

		if config.MCPInfoClearPaths.IsUnknown() {
			result.Clears = cloneMCPInfoPointerSet(prior.Clears)
		} else if config.MCPInfoClearPaths.IsNull() {
			result.Clears = mcpInfoPointerSet{}
		} else {
			pointers, hasUnknown, err := configuredMCPInfoClearPaths(config.MCPInfoClearPaths)
			if err != nil {
				addMCPInfoConfigError(&diagnostics, path.Root("mcp_info_clear_paths"), "Invalid MCP Info Clear Paths", "The configured MCP info clear paths could not be validated safely.")
			} else if hasUnknown {
				result.Clears = cloneMCPInfoPointerSet(prior.Clears)
			} else if canonical, err := canonicalMCPInfoClearPointers(pointers); err != nil {
				addMCPInfoConfigError(&diagnostics, path.Root("mcp_info_clear_paths"), "Invalid MCP Info Clear Paths", "The configured MCP info clear paths could not be validated safely.")
			} else {
				result.Clears = mcpInfoClearPointerSet(canonical)
			}
		}
		if len(result.Fixed)+len(result.Overrides)+len(result.Clears) > 0 {
			result.Mode = mcpInfoModeSelective
		} else {
			result.Mode = mcpInfoModeNone
		}
		for leaf := range result.API {
			api := mcpInfoPointerSet{mcpInfoLeafToPointer[leaf]: true}
			if mcpInfoPointerSetsConflict(result.Fixed, api) || mcpInfoPointerSetsConflict(result.Overrides, api) || mcpInfoPointerSetsConflict(result.Clears, api) {
				delete(result.API, leaf)
			}
		}
	}
	if mcpInfoPointerSetsConflict(result.Fixed, result.Overrides, result.Clears) {
		addMCPInfoConfigError(&diagnostics, path.Root("mcp_info"), "Overlapping MCP Info Ownership", "MCP info ownership could not be resolved without overlapping equal or ancestor/descendant paths.")
	}
	if !mcpInfoOwnershipEqual(prior, result) {
		result.Generation = prior.Generation + 1
	} else {
		result.Generation = prior.Generation
	}
	result.Versioned = true
	result.V2 = true
	return result, diagnostics
}

func setMCPInfoJSONPointer(document map[string]interface{}, pointer string, value interface{}) error {
	parsed, err := parseMCPInfoClearPointers([]string{pointer})
	if err != nil {
		return err
	}
	current := document
	for index, member := range parsed[0].members {
		if index == len(parsed[0].members)-1 {
			current[member] = cloneMCPInfoJSONValue(value)
			return nil
		}
		child, present := current[member]
		if !present {
			next := map[string]interface{}{}
			current[member] = next
			current = next
			continue
		}
		next, ok := child.(map[string]interface{})
		if !ok {
			return errMCPInfoClearTraversal
		}
		current = next
	}
	return nil
}

func fixedMCPInfoJSONValues(ctx context.Context, config MCPServerResourceModel) (map[string]interface{}, error) {
	values := map[string]interface{}{}
	if config.MCPInfo == nil {
		return values, nil
	}
	if !config.MCPInfo.ServerName.IsNull() && !config.MCPInfo.ServerName.IsUnknown() {
		values[mcpInfoServerNamePointer] = config.MCPInfo.ServerName.ValueString()
	}
	if !config.MCPInfo.Description.IsNull() && !config.MCPInfo.Description.IsUnknown() {
		values[mcpInfoDescriptionPointer] = config.MCPInfo.Description.ValueString()
	}
	if !config.MCPInfo.LogoURL.IsNull() && !config.MCPInfo.LogoURL.IsUnknown() {
		values[mcpInfoLogoURLPointer] = config.MCPInfo.LogoURL.ValueString()
	}
	if config.MCPInfo.MCPServerCostInfo == nil {
		return values, nil
	}
	if value := config.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery; !value.IsNull() && !value.IsUnknown() {
		values[mcpInfoDefaultCostPointer] = json.Number(fmt.Sprintf("%g", value.ValueFloat64()))
	}
	if value := config.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery; mcpInfoConfiguredLeafStates(config)[mcpInfoToolCostsLeaf] == 1 && !value.IsNull() && !value.IsUnknown() {
		decoded := map[string]float64{}
		if diagnostics := value.ElementsAs(ctx, &decoded, false); diagnostics.HasError() {
			return nil, fmt.Errorf("invalid fixed MCP cost map")
		}
		object := map[string]interface{}{}
		for name, number := range decoded {
			object[name] = json.Number(fmt.Sprintf("%g", number))
		}
		values[mcpInfoToolCostsPointer] = object
	}
	return values, nil
}

func planEffectiveMCPInfoJSON(ctx context.Context, hasState bool, state, config MCPServerResourceModel) (types.String, bool, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if !config.MCPInfoJSON.IsNull() {
		if config.MCPInfoJSON.IsUnknown() {
			return types.StringUnknown(), false, diagnostics
		}
		if _, err := parseMCPInfoJSONObject(config.MCPInfoJSON.ValueString()); err != nil {
			addMCPInfoConfigError(&diagnostics, path.Root("mcp_info_json"), "Invalid MCP Info JSON", "The configured MCP info document could not be validated safely.")
			return types.StringNull(), false, diagnostics
		}
		return config.MCPInfoJSON, true, diagnostics
	}

	knownOverrides := !config.MCPInfoOverridesJSON.IsNull() && !config.MCPInfoOverridesJSON.IsUnknown()
	overridesHaveMembers := false
	if knownOverrides {
		if object, parseErr := parseMCPInfoJSONObject(config.MCPInfoOverridesJSON.ValueString()); parseErr == nil {
			overridesHaveMembers = len(object) > 0
		}
	}
	clearPaths := []string(nil)
	clearHasUnknown := false
	var clearPathErr error
	if !config.MCPInfoClearPaths.IsNull() && !config.MCPInfoClearPaths.IsUnknown() {
		clearPaths, clearHasUnknown, clearPathErr = configuredMCPInfoClearPaths(config.MCPInfoClearPaths)
	}
	knownClears := clearPathErr == nil && !clearHasUnknown && len(clearPaths) > 0
	fixedValues, err := fixedMCPInfoJSONValues(ctx, config)
	if err != nil {
		diagnostics.AddError("Invalid MCP Info Fixed Values", "Fixed MCP info values could not be represented safely.")
		return types.StringNull(), false, diagnostics
	}
	hasKnownOperation := len(fixedValues) > 0 || overridesHaveMembers || knownClears
	if !hasKnownOperation {
		if hasState {
			// Stabilize direct protocol states produced before schema upgrade as
			// well as ordinary upgraded states. Optional+Computed must not leave
			// an omitted control unknown merely because it is computed.
			return state.MCPInfoJSON, true, diagnostics
		}
		return types.StringNull(), false, diagnostics
	}

	base := map[string]interface{}{}
	priorSpelling := ""
	if hasState && !state.MCPInfoJSON.IsNull() && !state.MCPInfoJSON.IsUnknown() {
		base, err = parseMCPInfoJSONObject(state.MCPInfoJSON.ValueString())
		priorSpelling = state.MCPInfoJSON.ValueString()
		if err != nil {
			addMCPInfoConfigError(&diagnostics, path.Root("mcp_info_json"), "Invalid Prior MCP Info JSON", "The prior complete MCP info document is malformed; selective ownership cannot proceed safely.")
			return types.StringNull(), false, diagnostics
		}
	} else if hasState && (overridesHaveMembers || knownClears) {
		addMCPInfoConfigError(&diagnostics, path.Root("mcp_info_json"), "Authoritative MCP Info Required", "Selective overrides or clears require a known prior complete mcp_info_json document. Refresh with a lifecycle version that hydrates the document before applying this operation.")
		return types.StringNull(), false, diagnostics
	}
	for pointer, value := range fixedValues {
		if err := setMCPInfoJSONPointer(base, pointer, value); err != nil {
			addMCPInfoConfigError(&diagnostics, path.Root("mcp_info"), "Unsafe MCP Info Traversal", "A fixed MCP info field would traverse a non-object member in the complete JSON document.")
			return types.StringNull(), false, diagnostics
		}
	}
	if knownOverrides {
		overrides, parseErr := parseMCPInfoJSONObject(config.MCPInfoOverridesJSON.ValueString())
		if parseErr != nil {
			addMCPInfoConfigError(&diagnostics, path.Root("mcp_info_overrides_json"), "Invalid MCP Info Overrides JSON", "The configured MCP info overrides could not be validated safely.")
			return types.StringNull(), false, diagnostics
		}
		base, err = overlayMCPInfoJSONObjects(base, overrides)
		if err != nil {
			diagnostics.AddError("Invalid MCP Info Overlay", "The MCP info overlay could not be applied safely.")
			return types.StringNull(), false, diagnostics
		}
	}
	if knownClears {
		base, err = clearMCPInfoJSONMembers(base, clearPaths)
		if err != nil {
			addMCPInfoConfigError(&diagnostics, path.Root("mcp_info_clear_paths"), "Unsafe MCP Info Clear", "An MCP info clear path would traverse a non-object member or is otherwise unsafe.")
			return types.StringNull(), false, diagnostics
		}
	}
	canonical, err := canonicalMCPInfoJSONObject(base)
	if err != nil {
		diagnostics.AddError("Invalid Effective MCP Info JSON", "The effective MCP info document could not be canonicalized safely.")
		return types.StringNull(), false, diagnostics
	}
	if priorSpelling != "" {
		prior, _ := parseMCPInfoJSONObject(priorSpelling)
		if mcpInfoJSONValuesEqual(prior, base) {
			return types.StringValue(priorSpelling), true, diagnostics
		}
	}
	return types.StringValue(canonical), true, diagnostics
}
