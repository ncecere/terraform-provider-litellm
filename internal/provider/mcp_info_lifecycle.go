package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

const mcpInfoDocumentAuthoritativePrivateKey = "mcp_info_document_authoritative_v2"

type mcpInfoResolvedDocument struct {
	Document map[string]interface{}
	Present  bool
}

func resolveMCPInfoCreateDocument(ctx context.Context, config MCPServerResourceModel) (mcpInfoResolvedDocument, error) {
	if !config.MCPInfoJSON.IsNull() {
		if config.MCPInfoJSON.IsUnknown() {
			return mcpInfoResolvedDocument{}, fmt.Errorf("whole MCP info document is unknown")
		}
		document, err := parseMCPInfoJSONObject(config.MCPInfoJSON.ValueString())
		return mcpInfoResolvedDocument{Document: document, Present: err == nil}, err
	}
	return applyMCPInfoSelectiveDocument(ctx, map[string]interface{}{}, config, mcpInfoHasConfiguredSelectiveOperation(config))
}

func resolveMCPInfoUpdateDocument(ctx context.Context, base map[string]interface{}, config MCPServerResourceModel) (mcpInfoResolvedDocument, error) {
	if base == nil {
		return mcpInfoResolvedDocument{}, errMCPInfoJSONObject
	}
	if !config.MCPInfoJSON.IsNull() {
		if config.MCPInfoJSON.IsUnknown() {
			return mcpInfoResolvedDocument{}, fmt.Errorf("whole MCP info document is unknown")
		}
		document, err := parseMCPInfoJSONObject(config.MCPInfoJSON.ValueString())
		return mcpInfoResolvedDocument{Document: document, Present: err == nil}, err
	}
	// Updates always carry the complete hydrated document. This includes an
	// ownership relinquish and unrelated server-field mutations.
	return applyMCPInfoSelectiveDocument(ctx, base, config, true)
}

func mcpInfoHasConfiguredSelectiveOperation(config MCPServerResourceModel) bool {
	if len(configuredMCPInfoFixedPointers(config)) != 0 {
		return true
	}
	if !config.MCPInfoOverridesJSON.IsNull() {
		if config.MCPInfoOverridesJSON.IsUnknown() {
			return true
		}
		object, err := parseMCPInfoJSONObject(config.MCPInfoOverridesJSON.ValueString())
		return err != nil || len(object) != 0
	}
	if !config.MCPInfoClearPaths.IsNull() {
		return config.MCPInfoClearPaths.IsUnknown() || len(config.MCPInfoClearPaths.Elements()) != 0
	}
	return false
}

func applyMCPInfoSelectiveDocument(ctx context.Context, base map[string]interface{}, config MCPServerResourceModel, present bool) (mcpInfoResolvedDocument, error) {
	document := cloneMCPInfoJSONObject(base)
	fixed, err := fixedMCPInfoJSONValues(ctx, config)
	if err != nil {
		return mcpInfoResolvedDocument{}, err
	}
	for pointer, value := range fixed {
		if err := setMCPInfoJSONPointer(document, pointer, value); err != nil {
			return mcpInfoResolvedDocument{}, err
		}
	}
	if !config.MCPInfoOverridesJSON.IsNull() {
		if config.MCPInfoOverridesJSON.IsUnknown() {
			return mcpInfoResolvedDocument{}, fmt.Errorf("MCP info overrides are unknown")
		}
		overrides, err := parseMCPInfoJSONObject(config.MCPInfoOverridesJSON.ValueString())
		if err != nil {
			return mcpInfoResolvedDocument{}, err
		}
		document, err = overlayMCPInfoJSONObjects(document, overrides)
		if err != nil {
			return mcpInfoResolvedDocument{}, err
		}
	}
	if !config.MCPInfoClearPaths.IsNull() {
		if config.MCPInfoClearPaths.IsUnknown() {
			return mcpInfoResolvedDocument{}, fmt.Errorf("MCP info clear paths are unknown")
		}
		pointers, unknown, err := configuredMCPInfoClearPaths(config.MCPInfoClearPaths)
		if err != nil || unknown {
			return mcpInfoResolvedDocument{}, fmt.Errorf("MCP info clear paths are invalid")
		}
		document, err = clearMCPInfoJSONMembers(document, pointers)
		if err != nil {
			return mcpInfoResolvedDocument{}, err
		}
	}
	return mcpInfoResolvedDocument{Document: document, Present: present}, nil
}

func mcpInfoDocumentFromResponse(result map[string]interface{}) (map[string]interface{}, apiValuePresence, error) {
	document, presence, err := apiJSONObject(result, "mcp_info")
	if err != nil {
		return nil, presence, errMCPInfoJSONObject
	}
	if presence == apiValuePresent {
		if err := validateMCPInfoJSONValue(document); err != nil {
			return nil, presence, err
		}
		// LiteLLM v1.98 masks arbitrary MCP info for restricted virtual keys as
		// this exact singleton. It is observationally equivalent to a masked
		// parent, never a complete document suitable for refresh or import.
		if len(document) == 1 {
			if isPublic, ok := document["is_public"].(bool); ok && isPublic {
				return nil, apiValueNull, nil
			}
		}
	}
	return document, presence, nil
}

func setCompleteMCPInfoJSONState(data *MCPServerResourceModel, document map[string]interface{}) error {
	canonical, err := canonicalMCPInfoJSONObject(document)
	if err != nil {
		return err
	}
	if !data.MCPInfoJSON.IsNull() && !data.MCPInfoJSON.IsUnknown() {
		prior, priorErr := parseMCPInfoJSONObject(data.MCPInfoJSON.ValueString())
		if priorErr == nil && mcpInfoJSONValuesEqual(prior, document) {
			return nil
		}
	}
	data.MCPInfoJSON = types.StringValue(canonical)
	return nil
}

func mcpInfoPointerValue(document map[string]interface{}, pointer string) (interface{}, bool, error) {
	parsed, err := parseMCPInfoClearPointers([]string{pointer})
	if err != nil {
		return nil, false, err
	}
	var current interface{} = document
	for _, member := range parsed[0].members {
		object, ok := current.(map[string]interface{})
		if !ok {
			return nil, false, errMCPInfoClearTraversal
		}
		current, ok = object[member]
		if !ok {
			return nil, false, nil
		}
	}
	return current, true, nil
}

func mcpInfoPointerAbsent(document map[string]interface{}, pointer string) bool {
	_, present, err := mcpInfoPointerValue(document, pointer)
	return err == nil && !present
}

func verifyMCPInfoReadback(base, desired, observed map[string]interface{}, ownership mcpInfoProvenance) error {
	if ownership.Mode == mcpInfoModeWhole {
		if !mcpInfoJSONValuesEqual(desired, observed) {
			return fmt.Errorf("whole MCP info document did not converge")
		}
		return nil
	}
	owned := cloneMCPInfoPointerSet(ownership.Fixed)
	for pointer := range ownership.Overrides {
		owned[pointer] = true
	}
	for pointer := range owned {
		want, wantPresent, err := mcpInfoPointerValue(desired, pointer)
		if err != nil {
			return err
		}
		got, gotPresent, err := mcpInfoPointerValue(observed, pointer)
		if err != nil || wantPresent != gotPresent || (wantPresent && !mcpInfoJSONValuesEqual(want, got)) {
			return fmt.Errorf("owned MCP info value did not converge")
		}
	}
	for pointer := range ownership.Clears {
		if !mcpInfoPointerAbsent(observed, pointer) {
			return fmt.Errorf("MCP info clear did not converge")
		}
	}
	if !mcpInfoPreservedPathsEqual(base, observed, owned, ownership.Clears, nil) {
		return fmt.Errorf("an unowned MCP info value changed")
	}
	return nil
}

func mcpInfoPreservedPathsEqual(base, observed interface{}, owned, clears mcpInfoPointerSet, members []string) bool {
	pointer := encodeMCPInfoClearPointer(members)
	if len(members) != 0 && (owned[pointer] || clears[pointer]) {
		return true
	}
	baseObject, baseIsObject := base.(map[string]interface{})
	if !baseIsObject {
		return mcpInfoJSONValuesEqual(base, observed)
	}
	observedObject, observedIsObject := observed.(map[string]interface{})
	if !observedIsObject {
		return false
	}
	for name, baseValue := range baseObject {
		childMembers := append(append([]string(nil), members...), name)
		observedValue, present := observedObject[name]
		childPointer := encodeMCPInfoClearPointer(childMembers)
		if owned[childPointer] || clears[childPointer] {
			continue
		}
		if !present || !mcpInfoPreservedPathsEqual(baseValue, observedValue, owned, clears, childMembers) {
			return false
		}
	}
	return true
}

func mcpInfoCompatibleFloat(value interface{}) (float64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(number.String(), 64)
	return parsed, err == nil && !math.IsInf(parsed, 0) && !math.IsNaN(parsed)
}

func mcpInfoCompatibleFloatMap(value interface{}) (map[string]float64, bool) {
	object, ok := value.(map[string]interface{})
	if !ok {
		return nil, false
	}
	result := make(map[string]float64, len(object))
	for name, raw := range object {
		parsed, ok := mcpInfoCompatibleFloat(raw)
		if !ok {
			return nil, false
		}
		result[name] = parsed
	}
	return result, true
}

func mcpInfoRequestWithoutDocument(request map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(request))
	for name, value := range request {
		if name != "mcp_info" && name != "server_id" {
			result[name] = value
		}
	}
	return result
}
