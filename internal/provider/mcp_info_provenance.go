package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	// Version 1 keys remain read-only compatibility inputs. A successful version
	// 2 commit removes all of them.
	mcpInfoTerraformOwnedPrivateKey = "mcp_info_terraform_owned_v1"
	mcpInfoAPIOwnedPrivateKey       = "mcp_info_api_owned_v1"
	mcpInfoOwnershipVersionKey      = "mcp_info_ownership_version"
	mcpInfoPendingTerraformKey      = "mcp_info_pending_terraform_owned_v1"
	mcpInfoPendingAPIKey            = "mcp_info_pending_api_owned_v1"
	mcpInfoOwnershipVersion         = "1"
	mcpInfoOwnershipVersionV2       = "2"

	mcpInfoGenerationPrivateKey        = "mcp_info_generation_v2"
	mcpInfoModePrivateKey              = "mcp_info_mode_v2"
	mcpInfoFixedOwnedPrivateKey        = "mcp_info_fixed_owned_v2"
	mcpInfoOverrideOwnedPrivateKey     = "mcp_info_override_owned_v2"
	mcpInfoClearOwnedPrivateKey        = "mcp_info_clear_owned_v2"
	mcpInfoAPIOwnedV2PrivateKey        = "mcp_info_api_owned_v2"
	mcpInfoPendingVersionV2PrivateKey  = "mcp_info_pending_version_v2"
	mcpInfoPendingGenerationPrivateKey = "mcp_info_pending_generation_v2"
	mcpInfoPendingModePrivateKey       = "mcp_info_pending_mode_v2"
	mcpInfoPendingFixedPrivateKey      = "mcp_info_pending_fixed_owned_v2"
	mcpInfoPendingOverridePrivateKey   = "mcp_info_pending_override_owned_v2"
	mcpInfoPendingClearPrivateKey      = "mcp_info_pending_clear_owned_v2"
	mcpInfoPendingAPIV2PrivateKey      = "mcp_info_pending_api_owned_v2"

	mcpInfoModeNone      = "none"
	mcpInfoModeWhole     = "whole"
	mcpInfoModeSelective = "selective"

	mcpInfoServerNameLeaf  = "mcp_info.server_name"
	mcpInfoDescriptionLeaf = "mcp_info.description"
	mcpInfoLogoURLLeaf     = "mcp_info.logo_url"
	mcpInfoDefaultCostLeaf = "mcp_info.mcp_server_cost_info.default_cost_per_query"
	mcpInfoToolCostsLeaf   = "mcp_info.mcp_server_cost_info.tool_name_to_cost_per_query"

	mcpInfoServerNamePointer  = "/server_name"
	mcpInfoDescriptionPointer = "/description"
	mcpInfoLogoURLPointer     = "/logo_url"
	mcpInfoDefaultCostPointer = "/mcp_server_cost_info/default_cost_per_query"
	mcpInfoToolCostsPointer   = "/mcp_server_cost_info/tool_name_to_cost_per_query"
)

var (
	mcpInfoStringLeaves    = []string{mcpInfoServerNameLeaf, mcpInfoDescriptionLeaf, mcpInfoLogoURLLeaf}
	mcpInfoCostLeaves      = []string{mcpInfoDefaultCostLeaf, mcpInfoToolCostsLeaf}
	mcpInfoAllLeaves       = []string{mcpInfoServerNameLeaf, mcpInfoDescriptionLeaf, mcpInfoLogoURLLeaf, mcpInfoDefaultCostLeaf, mcpInfoToolCostsLeaf}
	mcpInfoFixedPointers   = []string{mcpInfoServerNamePointer, mcpInfoDescriptionPointer, mcpInfoLogoURLPointer, mcpInfoDefaultCostPointer, mcpInfoToolCostsPointer}
	mcpInfoAPICostPointers = []string{mcpInfoDefaultCostPointer, mcpInfoToolCostsPointer}
	mcpInfoLeafToPointer   = map[string]string{
		mcpInfoServerNameLeaf:  mcpInfoServerNamePointer,
		mcpInfoDescriptionLeaf: mcpInfoDescriptionPointer,
		mcpInfoLogoURLLeaf:     mcpInfoLogoURLPointer,
		mcpInfoDefaultCostLeaf: mcpInfoDefaultCostPointer,
		mcpInfoToolCostsLeaf:   mcpInfoToolCostsPointer,
	}
	mcpInfoPointerToLeaf = map[string]string{
		mcpInfoServerNamePointer:  mcpInfoServerNameLeaf,
		mcpInfoDescriptionPointer: mcpInfoDescriptionLeaf,
		mcpInfoLogoURLPointer:     mcpInfoLogoURLLeaf,
		mcpInfoDefaultCostPointer: mcpInfoDefaultCostLeaf,
		mcpInfoToolCostsPointer:   mcpInfoToolCostsLeaf,
	}
)

type mcpInfoLeafSet map[string]bool
type mcpInfoPointerSet map[string]bool

type mcpInfoPrivateReader interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}
type mcpInfoPrivateWriter interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}

type mcpInfoProvenance struct {
	// Terraform and API are compatibility projections used by the unchanged
	// fixed-block/read code. Version 2 serializes pointers, never values.
	Terraform  mcpInfoLeafSet
	API        mcpInfoLeafSet
	Fixed      mcpInfoPointerSet
	Overrides  mcpInfoPointerSet
	Clears     mcpInfoPointerSet
	Generation int64
	Mode       string
	Versioned  bool
	V2         bool
}

func emptyMCPInfoProvenance() mcpInfoProvenance {
	return mcpInfoProvenance{Terraform: mcpInfoLeafSet{}, API: mcpInfoLeafSet{}, Fixed: mcpInfoPointerSet{}, Overrides: mcpInfoPointerSet{}, Clears: mcpInfoPointerSet{}, Mode: mcpInfoModeNone}
}

func cloneMCPInfoLeafSet(source mcpInfoLeafSet) mcpInfoLeafSet {
	result := mcpInfoLeafSet{}
	for leaf, owned := range source {
		if owned {
			result[leaf] = true
		}
	}
	return result
}
func cloneMCPInfoPointerSet(source mcpInfoPointerSet) mcpInfoPointerSet {
	result := mcpInfoPointerSet{}
	for pointer, owned := range source {
		if owned {
			result[pointer] = true
		}
	}
	return result
}
func cloneMCPInfoProvenance(source mcpInfoProvenance) mcpInfoProvenance {
	result := source
	result.Terraform = cloneMCPInfoLeafSet(source.Terraform)
	result.API = cloneMCPInfoLeafSet(source.API)
	result.Fixed = cloneMCPInfoPointerSet(source.Fixed)
	result.Overrides = cloneMCPInfoPointerSet(source.Overrides)
	result.Clears = cloneMCPInfoPointerSet(source.Clears)
	return result
}

func encodeMCPInfoLeafSet(fields mcpInfoLeafSet) []byte {
	names := make([]string, 0, len(fields))
	for name, present := range fields {
		if present {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	encoded, _ := json.Marshal(names)
	return encoded
}
func encodeMCPInfoPointerSet(fields mcpInfoPointerSet) []byte {
	names := make([]string, 0, len(fields))
	for name, present := range fields {
		if present {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	encoded, _ := json.Marshal(names)
	return encoded
}

func decodeMCPInfoLeafSet(raw []byte, allowed []string) (mcpInfoLeafSet, error) {
	if raw == nil {
		return nil, fmt.Errorf("provider-private MCP ownership data is missing")
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil || names == nil {
		return nil, fmt.Errorf("provider-private MCP ownership data is malformed")
	}
	valid := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		valid[name] = true
	}
	fields := make(mcpInfoLeafSet, len(names))
	for _, name := range names {
		if !valid[name] || fields[name] {
			return nil, fmt.Errorf("provider-private MCP ownership data is malformed")
		}
		fields[name] = true
	}
	if !bytes.Equal(raw, encodeMCPInfoLeafSet(fields)) {
		return nil, fmt.Errorf("provider-private MCP ownership data is not canonical")
	}
	return fields, nil
}

func decodeMCPInfoPointerSet(raw []byte, allowed []string, arbitrary bool) (mcpInfoPointerSet, error) {
	if raw == nil {
		return nil, fmt.Errorf("provider-private MCP ownership data is missing")
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil || names == nil {
		return nil, fmt.Errorf("provider-private MCP ownership data is malformed")
	}
	valid := map[string]bool{}
	for _, name := range allowed {
		valid[name] = true
	}
	fields := make(mcpInfoPointerSet, len(names))
	for _, name := range names {
		if fields[name] || (!arbitrary && !valid[name]) {
			return nil, fmt.Errorf("provider-private MCP ownership data is malformed")
		}
		parsed, err := parseMCPInfoClearPointers([]string{name})
		if err != nil || len(parsed) != 1 || parsed[0].canonical != name {
			return nil, fmt.Errorf("provider-private MCP ownership data is malformed")
		}
		fields[name] = true
	}
	if !bytes.Equal(raw, encodeMCPInfoPointerSet(fields)) {
		return nil, fmt.Errorf("provider-private MCP ownership data is not canonical")
	}
	return fields, nil
}

func mcpInfoPrivateError(diagnostics *diag.Diagnostics, title string) {
	diagnostics.AddError(title, "Provider-private MCP ownership data is invalid. Prior public and private state was retained; no remote operation was attempted. This diagnostic contains no public values or identifiers.")
}
func mcpInfoLeafSetsOverlap(left, right mcpInfoLeafSet) bool {
	for leaf := range left {
		if right[leaf] {
			return true
		}
	}
	return false
}

func mcpInfoPointerSetsConflict(sets ...mcpInfoPointerSet) bool {
	type item struct {
		pointer string
		members []string
	}
	items := []item{}
	for _, set := range sets {
		for pointer := range set {
			parsed, err := parseMCPInfoClearPointers([]string{pointer})
			if err != nil {
				return true
			}
			for _, existing := range items {
				if pointer == existing.pointer || mcpInfoPointerIsAncestor(parsed[0].members, existing.members) || mcpInfoPointerIsAncestor(existing.members, parsed[0].members) {
					return true
				}
			}
			items = append(items, item{pointer: pointer, members: parsed[0].members})
		}
	}
	return false
}

var mcpInfoAllPrivateKeys = []string{
	mcpInfoOwnershipVersionKey, mcpInfoDocumentAuthoritativePrivateKey, mcpInfoTerraformOwnedPrivateKey, mcpInfoAPIOwnedPrivateKey, mcpInfoPendingTerraformKey, mcpInfoPendingAPIKey,
	mcpInfoGenerationPrivateKey, mcpInfoModePrivateKey, mcpInfoFixedOwnedPrivateKey, mcpInfoOverrideOwnedPrivateKey, mcpInfoClearOwnedPrivateKey, mcpInfoAPIOwnedV2PrivateKey,
	mcpInfoPendingVersionV2PrivateKey, mcpInfoPendingGenerationPrivateKey, mcpInfoPendingModePrivateKey, mcpInfoPendingFixedPrivateKey, mcpInfoPendingOverridePrivateKey, mcpInfoPendingClearPrivateKey, mcpInfoPendingAPIV2PrivateKey,
}

func readMCPInfoPrivateKeys(ctx context.Context, private mcpInfoPrivateReader) (map[string][]byte, diag.Diagnostics) {
	values := make(map[string][]byte, len(mcpInfoAllPrivateKeys))
	var diagnostics diag.Diagnostics
	if private == nil {
		return values, diagnostics
	}
	for _, key := range mcpInfoAllPrivateKeys {
		raw, keyDiags := private.GetKey(ctx, key)
		diagnostics.Append(keyDiags...)
		values[key] = raw
	}
	return values, diagnostics
}

func validateMCPInfoOwnedPair(terraformRaw, apiRaw []byte) (mcpInfoLeafSet, mcpInfoLeafSet, error) {
	terraformOwned, err := decodeMCPInfoLeafSet(terraformRaw, mcpInfoAllLeaves)
	if err != nil {
		return nil, nil, err
	}
	apiOwned, err := decodeMCPInfoLeafSet(apiRaw, mcpInfoCostLeaves)
	if err != nil {
		return nil, nil, err
	}
	if mcpInfoLeafSetsOverlap(terraformOwned, apiOwned) {
		return nil, nil, fmt.Errorf("provider-private MCP ownership data overlaps")
	}
	return terraformOwned, apiOwned, nil
}

func provenanceFromV1(terraformOwned, apiOwned mcpInfoLeafSet) mcpInfoProvenance {
	result := emptyMCPInfoProvenance()
	result.Versioned = true
	result.Terraform = terraformOwned
	result.API = apiOwned
	for leaf := range terraformOwned {
		result.Fixed[mcpInfoLeafToPointer[leaf]] = true
	}
	if len(result.Fixed) > 0 {
		result.Mode = mcpInfoModeSelective
	}
	return result
}

func decodeCanonicalGeneration(raw []byte) (int64, error) {
	if raw == nil {
		return 0, fmt.Errorf("provider-private MCP ownership data is missing")
	}
	value, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || value < 0 || string(raw) != strconv.FormatInt(value, 10) {
		return 0, fmt.Errorf("provider-private MCP ownership data is malformed")
	}
	return value, nil
}
func decodeCanonicalMode(raw []byte) (string, error) {
	if raw == nil {
		return "", fmt.Errorf("provider-private MCP ownership data is missing")
	}
	var mode string
	if json.Unmarshal(raw, &mode) != nil || (mode != mcpInfoModeNone && mode != mcpInfoModeWhole && mode != mcpInfoModeSelective) {
		return "", fmt.Errorf("provider-private MCP ownership data is malformed")
	}
	canonical, _ := json.Marshal(mode)
	if !bytes.Equal(raw, canonical) {
		return "", fmt.Errorf("provider-private MCP ownership data is not canonical")
	}
	return mode, nil
}

func decodeMCPInfoV2Bundle(values map[string][]byte, pending bool) (mcpInfoProvenance, error) {
	keys := []string{mcpInfoGenerationPrivateKey, mcpInfoModePrivateKey, mcpInfoFixedOwnedPrivateKey, mcpInfoOverrideOwnedPrivateKey, mcpInfoClearOwnedPrivateKey, mcpInfoAPIOwnedV2PrivateKey}
	if pending {
		keys = []string{mcpInfoPendingGenerationPrivateKey, mcpInfoPendingModePrivateKey, mcpInfoPendingFixedPrivateKey, mcpInfoPendingOverridePrivateKey, mcpInfoPendingClearPrivateKey, mcpInfoPendingAPIV2PrivateKey}
	}
	present := 0
	for _, key := range keys {
		if values[key] != nil {
			present++
		}
	}
	if present == 0 {
		return mcpInfoProvenance{}, fmt.Errorf("absent")
	}
	if present != len(keys) {
		return mcpInfoProvenance{}, fmt.Errorf("provider-private MCP ownership bundle is incomplete")
	}
	generation, err := decodeCanonicalGeneration(values[keys[0]])
	if err != nil {
		return mcpInfoProvenance{}, err
	}
	mode, err := decodeCanonicalMode(values[keys[1]])
	if err != nil {
		return mcpInfoProvenance{}, err
	}
	fixed, err := decodeMCPInfoPointerSet(values[keys[2]], mcpInfoFixedPointers, false)
	if err != nil {
		return mcpInfoProvenance{}, err
	}
	overrides, err := decodeMCPInfoPointerSet(values[keys[3]], nil, true)
	if err != nil {
		return mcpInfoProvenance{}, err
	}
	clears, err := decodeMCPInfoPointerSet(values[keys[4]], nil, true)
	if err != nil {
		return mcpInfoProvenance{}, err
	}
	apiPointers, err := decodeMCPInfoPointerSet(values[keys[5]], mcpInfoAPICostPointers, false)
	if err != nil {
		return mcpInfoProvenance{}, err
	}
	if mcpInfoPointerSetsConflict(fixed, overrides, clears) || mcpInfoPointerSetsConflict(fixed, apiPointers) || mcpInfoPointerSetsConflict(overrides, apiPointers) || mcpInfoPointerSetsConflict(clears, apiPointers) {
		return mcpInfoProvenance{}, fmt.Errorf("provider-private MCP ownership data overlaps")
	}
	ownedCount := len(fixed) + len(overrides) + len(clears)
	if (mode == mcpInfoModeNone && ownedCount != 0) || (mode == mcpInfoModeWhole && (ownedCount != 0 || len(apiPointers) != 0)) || (mode == mcpInfoModeSelective && ownedCount == 0) {
		return mcpInfoProvenance{}, fmt.Errorf("provider-private MCP ownership mode is inconsistent")
	}
	result := emptyMCPInfoProvenance()
	result.Generation = generation
	result.Mode = mode
	result.Fixed = fixed
	result.Overrides = overrides
	result.Clears = clears
	result.Versioned = true
	result.V2 = true
	for pointer := range fixed {
		result.Terraform[mcpInfoPointerToLeaf[pointer]] = true
	}
	for pointer := range apiPointers {
		result.API[mcpInfoPointerToLeaf[pointer]] = true
	}
	return result, nil
}

func validateMCPInfoPrivateBundle(values map[string][]byte) (mcpInfoProvenance, *mcpInfoProvenance, error) {
	committed := emptyMCPInfoProvenance()
	v1CommittedPresent := values[mcpInfoTerraformOwnedPrivateKey] != nil || values[mcpInfoAPIOwnedPrivateKey] != nil || string(values[mcpInfoOwnershipVersionKey]) == mcpInfoOwnershipVersion
	v1PendingPresent := values[mcpInfoPendingTerraformKey] != nil || values[mcpInfoPendingAPIKey] != nil
	v2Committed, v2CommittedErr := decodeMCPInfoV2Bundle(values, false)
	v2CommittedPresent := v2CommittedErr == nil
	if v2CommittedErr != nil && v2CommittedErr.Error() != "absent" {
		return committed, nil, v2CommittedErr
	}
	v2Pending, v2PendingErr := decodeMCPInfoV2Bundle(values, true)
	v2PendingPresent := v2PendingErr == nil
	if v2PendingErr != nil && v2PendingErr.Error() != "absent" {
		return committed, nil, v2PendingErr
	}

	if v2CommittedPresent {
		if string(values[mcpInfoOwnershipVersionKey]) != mcpInfoOwnershipVersionV2 || v1CommittedPresent || v1PendingPresent {
			return committed, nil, fmt.Errorf("provider-private MCP ownership data mixes versions")
		}
		if values[mcpInfoPendingVersionV2PrivateKey] != nil && string(values[mcpInfoPendingVersionV2PrivateKey]) != mcpInfoOwnershipVersionV2 {
			return committed, nil, fmt.Errorf("provider-private pending MCP ownership version is malformed")
		}
		if (values[mcpInfoPendingVersionV2PrivateKey] != nil) != v2PendingPresent {
			return committed, nil, fmt.Errorf("provider-private pending MCP ownership bundle is incomplete")
		}
		if v2PendingPresent {
			return v2Committed, &v2Pending, nil
		}
		return v2Committed, nil, nil
	}

	if v2PendingPresent || values[mcpInfoPendingVersionV2PrivateKey] != nil {
		// A complete v2 pending bundle may accompany a complete v1 committed
		// pair during migration, or stand alone for a create plan. No other
		// partial or mixed form is accepted.
		if v1PendingPresent || !v2PendingPresent || string(values[mcpInfoPendingVersionV2PrivateKey]) != mcpInfoOwnershipVersionV2 {
			return committed, nil, fmt.Errorf("provider-private MCP ownership data mixes versions")
		}
		if !v1CommittedPresent && values[mcpInfoOwnershipVersionKey] != nil {
			return committed, nil, fmt.Errorf("provider-private MCP ownership data mixes versions")
		}
	}

	anyV1 := v1CommittedPresent || v1PendingPresent || values[mcpInfoOwnershipVersionKey] != nil
	if !anyV1 {
		if v2PendingPresent {
			return committed, &v2Pending, nil
		}
		return committed, nil, nil
	}
	if string(values[mcpInfoOwnershipVersionKey]) != mcpInfoOwnershipVersion {
		return committed, nil, fmt.Errorf("provider-private MCP ownership version is malformed or unsupported")
	}
	terraformOwned, apiOwned, err := validateMCPInfoOwnedPair(values[mcpInfoTerraformOwnedPrivateKey], values[mcpInfoAPIOwnedPrivateKey])
	if err != nil {
		return committed, nil, err
	}
	committed = provenanceFromV1(terraformOwned, apiOwned)
	if v1PendingPresent {
		if (values[mcpInfoPendingTerraformKey] == nil) != (values[mcpInfoPendingAPIKey] == nil) {
			return committed, nil, fmt.Errorf("provider-private pending MCP ownership data is incomplete")
		}
		pendingTerraform, pendingAPI, err := validateMCPInfoOwnedPair(values[mcpInfoPendingTerraformKey], values[mcpInfoPendingAPIKey])
		if err != nil {
			return committed, nil, err
		}
		pending := provenanceFromV1(pendingTerraform, pendingAPI)
		return committed, &pending, nil
	}
	if v2PendingPresent {
		return committed, &v2Pending, nil
	}
	return committed, nil, nil
}

func readMCPInfoProvenance(ctx context.Context, private mcpInfoPrivateReader) (mcpInfoProvenance, diag.Diagnostics) {
	result := emptyMCPInfoProvenance()
	values, diagnostics := readMCPInfoPrivateKeys(ctx, private)
	if diagnostics.HasError() {
		return result, diagnostics
	}
	committed, _, err := validateMCPInfoPrivateBundle(values)
	if err != nil {
		mcpInfoPrivateError(&diagnostics, "Invalid MCP Ownership State")
		return result, diagnostics
	}
	return committed, diagnostics
}
func mcpInfoPrivateDocumentAuthoritative(ctx context.Context, private mcpInfoPrivateReader) (bool, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if private == nil {
		return false, diagnostics
	}
	raw, keyDiags := private.GetKey(ctx, mcpInfoDocumentAuthoritativePrivateKey)
	diagnostics.Append(keyDiags...)
	if diagnostics.HasError() || raw == nil {
		return false, diagnostics
	}
	if string(raw) != "true" {
		mcpInfoPrivateError(&diagnostics, "Invalid MCP Document State")
		return false, diagnostics
	}
	return true, diagnostics
}

func writeMCPInfoPrivateDocumentAuthoritative(ctx context.Context, private mcpInfoPrivateWriter, authoritative bool) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if private == nil {
		return diagnostics
	}
	var value []byte
	if authoritative {
		value = []byte("true")
	}
	diagnostics.Append(private.SetKey(ctx, mcpInfoDocumentAuthoritativePrivateKey, value)...)
	return diagnostics
}

func mcpInfoPrivateHasPending(ctx context.Context, private mcpInfoPrivateReader) (bool, diag.Diagnostics) {
	values, diagnostics := readMCPInfoPrivateKeys(ctx, private)
	if diagnostics.HasError() {
		return false, diagnostics
	}
	_, pending, err := validateMCPInfoPrivateBundle(values)
	if err != nil {
		mcpInfoPrivateError(&diagnostics, "Invalid MCP Ownership State")
		return false, diagnostics
	}
	return pending != nil, diagnostics
}

func readPendingMCPInfoProvenance(ctx context.Context, private mcpInfoPrivateReader, fallback mcpInfoProvenance) (mcpInfoProvenance, diag.Diagnostics) {
	result := cloneMCPInfoProvenance(fallback)
	result.Versioned = true
	values, diagnostics := readMCPInfoPrivateKeys(ctx, private)
	if diagnostics.HasError() {
		return result, diagnostics
	}
	_, pending, err := validateMCPInfoPrivateBundle(values)
	if err != nil {
		mcpInfoPrivateError(&diagnostics, "Invalid MCP Ownership State")
		return result, diagnostics
	}
	if pending != nil {
		return *pending, diagnostics
	}
	return result, diagnostics
}

func validateMCPInfoProvenanceForWrite(provenance mcpInfoProvenance) error {
	values := map[string][]byte{
		mcpInfoGenerationPrivateKey: []byte(strconv.FormatInt(provenance.Generation, 10)), mcpInfoModePrivateKey: mustJSONMarshalString(provenance.Mode),
		mcpInfoFixedOwnedPrivateKey: encodeMCPInfoPointerSet(provenance.Fixed), mcpInfoOverrideOwnedPrivateKey: encodeMCPInfoPointerSet(provenance.Overrides), mcpInfoClearOwnedPrivateKey: encodeMCPInfoPointerSet(provenance.Clears),
		mcpInfoAPIOwnedV2PrivateKey: encodeMCPInfoPointerSet(apiPointerSetFromLeaves(provenance.API)),
	}
	_, err := decodeMCPInfoV2Bundle(values, false)
	return err
}
func mustJSONMarshalString(value string) []byte { raw, _ := json.Marshal(value); return raw }
func apiPointerSetFromLeaves(leaves mcpInfoLeafSet) mcpInfoPointerSet {
	result := mcpInfoPointerSet{}
	for leaf := range leaves {
		if pointer := mcpInfoLeafToPointer[leaf]; pointer != "" {
			result[pointer] = true
		}
	}
	return result
}

func setMCPInfoPrivateKey(ctx context.Context, private mcpInfoPrivateWriter, diagnostics *diag.Diagnostics, key string, value []byte) bool {
	diagnostics.Append(private.SetKey(ctx, key, value)...)
	return !diagnostics.HasError()
}

func writeMCPInfoProvenance(ctx context.Context, private mcpInfoPrivateWriter, provenance mcpInfoProvenance) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if private == nil {
		return diagnostics
	}
	provenance.Versioned = true
	provenance.V2 = true
	if err := validateMCPInfoProvenanceForWrite(provenance); err != nil {
		mcpInfoPrivateError(&diagnostics, "Invalid MCP Ownership State")
		return diagnostics
	}
	writes := []struct {
		key   string
		value []byte
	}{
		{mcpInfoGenerationPrivateKey, []byte(strconv.FormatInt(provenance.Generation, 10))}, {mcpInfoModePrivateKey, mustJSONMarshalString(provenance.Mode)},
		{mcpInfoFixedOwnedPrivateKey, encodeMCPInfoPointerSet(provenance.Fixed)}, {mcpInfoOverrideOwnedPrivateKey, encodeMCPInfoPointerSet(provenance.Overrides)}, {mcpInfoClearOwnedPrivateKey, encodeMCPInfoPointerSet(provenance.Clears)}, {mcpInfoAPIOwnedV2PrivateKey, encodeMCPInfoPointerSet(apiPointerSetFromLeaves(provenance.API))},
		{mcpInfoOwnershipVersionKey, []byte(mcpInfoOwnershipVersionV2)},
	}
	for _, write := range writes {
		if !setMCPInfoPrivateKey(ctx, private, &diagnostics, write.key, write.value) {
			return diagnostics
		}
	}
	// Clear pending and all legacy keys only after the complete v2 committed
	// bundle has been accepted by the private-state writer.
	for _, key := range []string{mcpInfoPendingVersionV2PrivateKey, mcpInfoPendingGenerationPrivateKey, mcpInfoPendingModePrivateKey, mcpInfoPendingFixedPrivateKey, mcpInfoPendingOverridePrivateKey, mcpInfoPendingClearPrivateKey, mcpInfoPendingAPIV2PrivateKey, mcpInfoTerraformOwnedPrivateKey, mcpInfoAPIOwnedPrivateKey, mcpInfoPendingTerraformKey, mcpInfoPendingAPIKey} {
		if !setMCPInfoPrivateKey(ctx, private, &diagnostics, key, nil) {
			return diagnostics
		}
	}
	return diagnostics
}

func writePendingMCPInfoProvenance(ctx context.Context, private mcpInfoPrivateWriter, provenance mcpInfoProvenance) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if private == nil {
		return diagnostics
	}
	provenance.Versioned = true
	provenance.V2 = true
	if err := validateMCPInfoProvenanceForWrite(provenance); err != nil {
		mcpInfoPrivateError(&diagnostics, "Invalid MCP Ownership State")
		return diagnostics
	}
	writes := []struct {
		key   string
		value []byte
	}{
		{mcpInfoPendingGenerationPrivateKey, []byte(strconv.FormatInt(provenance.Generation, 10))}, {mcpInfoPendingModePrivateKey, mustJSONMarshalString(provenance.Mode)},
		{mcpInfoPendingFixedPrivateKey, encodeMCPInfoPointerSet(provenance.Fixed)}, {mcpInfoPendingOverridePrivateKey, encodeMCPInfoPointerSet(provenance.Overrides)}, {mcpInfoPendingClearPrivateKey, encodeMCPInfoPointerSet(provenance.Clears)}, {mcpInfoPendingAPIV2PrivateKey, encodeMCPInfoPointerSet(apiPointerSetFromLeaves(provenance.API))},
		{mcpInfoPendingVersionV2PrivateKey, []byte(mcpInfoOwnershipVersionV2)},
	}
	for _, write := range writes {
		if !setMCPInfoPrivateKey(ctx, private, &diagnostics, write.key, write.value) {
			return diagnostics
		}
	}
	for _, key := range []string{mcpInfoPendingTerraformKey, mcpInfoPendingAPIKey} {
		if !setMCPInfoPrivateKey(ctx, private, &diagnostics, key, nil) {
			return diagnostics
		}
	}
	return diagnostics
}

func mcpInfoLeafSetsEqual(left, right mcpInfoLeafSet) bool {
	for _, leaf := range mcpInfoAllLeaves {
		if left[leaf] != right[leaf] {
			return false
		}
	}
	return true
}
func mcpInfoPointerSetsEqual(left, right mcpInfoPointerSet) bool {
	if len(left) != len(right) {
		return false
	}
	for pointer := range left {
		if !right[pointer] {
			return false
		}
	}
	return true
}
func mcpInfoOwnershipEqual(left, right mcpInfoProvenance) bool {
	return left.Mode == right.Mode && mcpInfoPointerSetsEqual(left.Fixed, right.Fixed) && mcpInfoPointerSetsEqual(left.Overrides, right.Overrides) && mcpInfoPointerSetsEqual(left.Clears, right.Clears) && mcpInfoLeafSetsEqual(left.API, right.API)
}

func mcpInfoConfiguredLeafStates(config MCPServerResourceModel) map[string]int {
	states := map[string]int{}
	if config.MCPInfo == nil {
		return states
	}
	stringState := func(value types.String) int {
		if value.IsUnknown() {
			return 2
		}
		if !value.IsNull() {
			return 1
		}
		return 0
	}
	states[mcpInfoServerNameLeaf] = stringState(config.MCPInfo.ServerName)
	states[mcpInfoDescriptionLeaf] = stringState(config.MCPInfo.Description)
	states[mcpInfoLogoURLLeaf] = stringState(config.MCPInfo.LogoURL)
	if config.MCPInfo.MCPServerCostInfo == nil {
		return states
	}
	if config.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.IsUnknown() {
		states[mcpInfoDefaultCostLeaf] = 2
	} else if !config.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.IsNull() {
		states[mcpInfoDefaultCostLeaf] = 1
	}
	if config.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsUnknown() {
		states[mcpInfoToolCostsLeaf] = 2
	} else if !config.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsNull() {
		states[mcpInfoToolCostsLeaf] = 1
		for _, element := range config.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.Elements() {
			if element.IsUnknown() {
				states[mcpInfoToolCostsLeaf] = 2
				break
			}
		}
	}
	return states
}

// deriveMCPInfoPlanProvenance preserves the historical fixed-block behavior.
// The complete stage-2 derivation layers JSON ownership onto this baseline in
// resource_mcp_server.go.
func deriveMCPInfoPlanProvenance(prior mcpInfoProvenance, config, state MCPServerResourceModel) mcpInfoProvenance {
	result := cloneMCPInfoProvenance(prior)
	result.Versioned = true
	result.V2 = true
	if result.Fixed == nil {
		result.Fixed = mcpInfoPointerSet{}
	}
	if result.Overrides == nil {
		result.Overrides = mcpInfoPointerSet{}
	}
	if result.Clears == nil {
		result.Clears = mcpInfoPointerSet{}
	}
	states := mcpInfoConfiguredLeafStates(config)
	if !prior.Versioned {
		// Public values are never ownership evidence. Imports establish API cost
		// ownership explicitly in private state; an unversioned historical state
		// begins with no inferred owner.
		result = emptyMCPInfoProvenance()
		result.Versioned = true
		result.V2 = true
	}
	for _, leaf := range mcpInfoAllLeaves {
		pointer := mcpInfoLeafToPointer[leaf]
		switch states[leaf] {
		case 1:
			result.Terraform[leaf] = true
			result.Fixed[pointer] = true
			if leaf == mcpInfoDefaultCostLeaf || leaf == mcpInfoToolCostsLeaf {
				delete(result.API, leaf)
			}
		case 0:
			delete(result.Terraform, leaf)
			delete(result.Fixed, pointer)
		case 2:
		}
	}
	if prior.Mode != mcpInfoModeWhole {
		if len(result.Fixed)+len(result.Overrides)+len(result.Clears) > 0 {
			result.Mode = mcpInfoModeSelective
		} else {
			result.Mode = mcpInfoModeNone
		}
	}
	return result
}
