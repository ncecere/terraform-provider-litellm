package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	mcpInfoTerraformOwnedPrivateKey = "mcp_info_terraform_owned_v1"
	mcpInfoAPIOwnedPrivateKey       = "mcp_info_api_owned_v1"
	mcpInfoOwnershipVersionKey      = "mcp_info_ownership_version"
	mcpInfoPendingTerraformKey      = "mcp_info_pending_terraform_owned_v1"
	mcpInfoPendingAPIKey            = "mcp_info_pending_api_owned_v1"
	mcpInfoOwnershipVersion         = "1"

	mcpInfoServerNameLeaf  = "mcp_info.server_name"
	mcpInfoDescriptionLeaf = "mcp_info.description"
	mcpInfoLogoURLLeaf     = "mcp_info.logo_url"
	mcpInfoDefaultCostLeaf = "mcp_info.mcp_server_cost_info.default_cost_per_query"
	mcpInfoToolCostsLeaf   = "mcp_info.mcp_server_cost_info.tool_name_to_cost_per_query"
)

var (
	mcpInfoStringLeaves = []string{mcpInfoServerNameLeaf, mcpInfoDescriptionLeaf, mcpInfoLogoURLLeaf}
	mcpInfoCostLeaves   = []string{mcpInfoDefaultCostLeaf, mcpInfoToolCostsLeaf}
	mcpInfoAllLeaves    = []string{mcpInfoServerNameLeaf, mcpInfoDescriptionLeaf, mcpInfoLogoURLLeaf, mcpInfoDefaultCostLeaf, mcpInfoToolCostsLeaf}
)

type mcpInfoLeafSet map[string]bool

type mcpInfoPrivateReader interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}

type mcpInfoPrivateWriter interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}

type mcpInfoProvenance struct {
	Terraform mcpInfoLeafSet
	API       mcpInfoLeafSet
	Versioned bool
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
	// The writer's byte representation is the contract. This rejects otherwise
	// equivalent whitespace, escaping, ordering, and duplicate spellings so
	// corrupt private state can never be normalized silently.
	if !bytes.Equal(raw, encodeMCPInfoLeafSet(fields)) {
		return nil, fmt.Errorf("provider-private MCP ownership data is not canonical")
	}
	return fields, nil
}

func mcpInfoPrivateError(diagnostics *diag.Diagnostics, title string) {
	diagnostics.AddError(title, "Provider-private MCP ownership data is invalid. Prior public and private state was retained; no remote operation was attempted. This diagnostic contains no public values or identifiers.")
}

func mcpInfoLeafSetsOverlap(terraformOwned, apiOwned mcpInfoLeafSet) bool {
	for leaf := range terraformOwned {
		if apiOwned[leaf] {
			return true
		}
	}
	return false
}

func readMCPInfoPrivateKeys(ctx context.Context, private mcpInfoPrivateReader) (map[string][]byte, diag.Diagnostics) {
	keys := []string{
		mcpInfoOwnershipVersionKey,
		mcpInfoTerraformOwnedPrivateKey,
		mcpInfoAPIOwnedPrivateKey,
		mcpInfoPendingTerraformKey,
		mcpInfoPendingAPIKey,
	}
	values := make(map[string][]byte, len(keys))
	var diagnostics diag.Diagnostics
	if private == nil {
		return values, diagnostics
	}
	for _, key := range keys {
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

func validateMCPInfoPrivateBundle(values map[string][]byte) (mcpInfoProvenance, *mcpInfoProvenance, error) {
	committed := mcpInfoProvenance{Terraform: mcpInfoLeafSet{}, API: mcpInfoLeafSet{}}
	newKeyPresent := false
	for _, key := range []string{mcpInfoOwnershipVersionKey, mcpInfoTerraformOwnedPrivateKey, mcpInfoAPIOwnedPrivateKey, mcpInfoPendingTerraformKey, mcpInfoPendingAPIKey} {
		newKeyPresent = newKeyPresent || values[key] != nil
	}
	if !newKeyPresent {
		return committed, nil, nil
	}
	version := values[mcpInfoOwnershipVersionKey]
	if version == nil {
		return committed, nil, fmt.Errorf("provider-private MCP ownership version is missing")
	}
	if string(version) != mcpInfoOwnershipVersion {
		return committed, nil, fmt.Errorf("provider-private MCP ownership version is malformed or unsupported")
	}
	terraformOwned, apiOwned, err := validateMCPInfoOwnedPair(values[mcpInfoTerraformOwnedPrivateKey], values[mcpInfoAPIOwnedPrivateKey])
	if err != nil {
		return committed, nil, err
	}
	committed = mcpInfoProvenance{Terraform: terraformOwned, API: apiOwned, Versioned: true}

	pendingTerraform, pendingAPI := values[mcpInfoPendingTerraformKey], values[mcpInfoPendingAPIKey]
	if (pendingTerraform == nil) != (pendingAPI == nil) {
		return committed, nil, fmt.Errorf("provider-private pending MCP ownership data is incomplete")
	}
	if pendingTerraform == nil {
		return committed, nil, nil
	}
	terraformOwned, apiOwned, err = validateMCPInfoOwnedPair(pendingTerraform, pendingAPI)
	if err != nil {
		return committed, nil, err
	}
	pending := &mcpInfoProvenance{Terraform: terraformOwned, API: apiOwned, Versioned: true}
	return committed, pending, nil
}

func readMCPInfoProvenance(ctx context.Context, private mcpInfoPrivateReader) (mcpInfoProvenance, diag.Diagnostics) {
	result := mcpInfoProvenance{Terraform: mcpInfoLeafSet{}, API: mcpInfoLeafSet{}}
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

func readPendingMCPInfoProvenance(ctx context.Context, private mcpInfoPrivateReader, fallback mcpInfoProvenance) (mcpInfoProvenance, diag.Diagnostics) {
	result := mcpInfoProvenance{Terraform: cloneMCPInfoLeafSet(fallback.Terraform), API: cloneMCPInfoLeafSet(fallback.API), Versioned: true}
	values, diagnostics := readMCPInfoPrivateKeys(ctx, private)
	if diagnostics.HasError() {
		return result, diagnostics
	}
	_, pending, err := validateMCPInfoPrivateBundle(values)
	if err != nil {
		mcpInfoPrivateError(&diagnostics, "Invalid MCP Ownership State")
		return result, diagnostics
	}
	if pending == nil {
		return result, diagnostics
	}
	return *pending, diagnostics
}

func writeMCPInfoProvenance(ctx context.Context, private mcpInfoPrivateWriter, provenance mcpInfoProvenance) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if private == nil {
		return diagnostics
	}
	terraformRaw := encodeMCPInfoLeafSet(provenance.Terraform)
	apiRaw := encodeMCPInfoLeafSet(provenance.API)
	if _, _, err := validateMCPInfoOwnedPair(terraformRaw, apiRaw); err != nil {
		mcpInfoPrivateError(&diagnostics, "Invalid MCP Ownership State")
		return diagnostics
	}
	diagnostics.Append(private.SetKey(ctx, mcpInfoTerraformOwnedPrivateKey, terraformRaw)...)
	diagnostics.Append(private.SetKey(ctx, mcpInfoAPIOwnedPrivateKey, apiRaw)...)
	diagnostics.Append(private.SetKey(ctx, mcpInfoOwnershipVersionKey, []byte(mcpInfoOwnershipVersion))...)
	diagnostics.Append(private.SetKey(ctx, mcpInfoPendingTerraformKey, nil)...)
	diagnostics.Append(private.SetKey(ctx, mcpInfoPendingAPIKey, nil)...)
	return diagnostics
}

func writePendingMCPInfoProvenance(ctx context.Context, private mcpInfoPrivateWriter, provenance mcpInfoProvenance) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if private == nil {
		return diagnostics
	}
	terraformRaw := encodeMCPInfoLeafSet(provenance.Terraform)
	apiRaw := encodeMCPInfoLeafSet(provenance.API)
	if _, _, err := validateMCPInfoOwnedPair(terraformRaw, apiRaw); err != nil {
		mcpInfoPrivateError(&diagnostics, "Invalid MCP Ownership State")
		return diagnostics
	}
	diagnostics.Append(private.SetKey(ctx, mcpInfoPendingTerraformKey, terraformRaw)...)
	diagnostics.Append(private.SetKey(ctx, mcpInfoPendingAPIKey, apiRaw)...)
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

func mcpInfoConfiguredLeafStates(config MCPServerResourceModel) map[string]int {
	// 0 is omitted/null, 1 is known configured, and 2 is unknown. Unknowns retain
	// prior provenance and never manufacture ownership.
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
	}
	return states
}

func deriveMCPInfoPlanProvenance(prior mcpInfoProvenance, config, state MCPServerResourceModel) mcpInfoProvenance {
	result := mcpInfoProvenance{Terraform: cloneMCPInfoLeafSet(prior.Terraform), API: cloneMCPInfoLeafSet(prior.API), Versioned: true}
	states := mcpInfoConfiguredLeafStates(config)
	if !prior.Versioned {
		result.Terraform = mcpInfoLeafSet{}
		result.API = mcpInfoLeafSet{}
		// A legacy numeric value without explicit HCL is conservatively an API
		// projection. Legacy strings never receive ownership from public state.
		if states[mcpInfoDefaultCostLeaf] == 0 && state.MCPInfo != nil && state.MCPInfo.MCPServerCostInfo != nil && !state.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.IsNull() && !state.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.IsUnknown() {
			result.API[mcpInfoDefaultCostLeaf] = true
		}
		if states[mcpInfoToolCostsLeaf] == 0 && state.MCPInfo != nil && state.MCPInfo.MCPServerCostInfo != nil && !state.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsNull() && !state.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsUnknown() {
			result.API[mcpInfoToolCostsLeaf] = true
		}
	}
	for _, leaf := range mcpInfoAllLeaves {
		switch states[leaf] {
		case 1:
			result.Terraform[leaf] = true
			if leaf == mcpInfoDefaultCostLeaf || leaf == mcpInfoToolCostsLeaf {
				delete(result.API, leaf)
			}
		case 0:
			delete(result.Terraform, leaf)
		case 2:
			// Preserve exact prior provenance.
		}
	}
	return result
}
