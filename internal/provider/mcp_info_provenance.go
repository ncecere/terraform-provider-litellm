package provider

import (
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
	fields := mcpInfoLeafSet{}
	if len(raw) == 0 {
		return fields, nil
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil, fmt.Errorf("provider-private MCP ownership data is malformed")
	}
	valid := map[string]bool{}
	for _, name := range allowed {
		valid[name] = true
	}
	for _, name := range names {
		if !valid[name] {
			return nil, fmt.Errorf("provider-private MCP ownership data is malformed")
		}
		fields[name] = true
	}
	return fields, nil
}

func readMCPInfoProvenance(ctx context.Context, private mcpInfoPrivateReader) (mcpInfoProvenance, diag.Diagnostics) {
	result := mcpInfoProvenance{Terraform: mcpInfoLeafSet{}, API: mcpInfoLeafSet{}}
	var diagnostics diag.Diagnostics
	if private == nil {
		return result, diagnostics
	}
	version, versionDiags := private.GetKey(ctx, mcpInfoOwnershipVersionKey)
	diagnostics.Append(versionDiags...)
	if diagnostics.HasError() || len(version) == 0 {
		return result, diagnostics
	}
	if string(version) != mcpInfoOwnershipVersion {
		diagnostics.AddError("Unsupported MCP Ownership State", "The provider-private MCP ownership version is unsupported. No public values or identifiers were included in this diagnostic.")
		return result, diagnostics
	}
	terraformRaw, terraformDiags := private.GetKey(ctx, mcpInfoTerraformOwnedPrivateKey)
	apiRaw, apiDiags := private.GetKey(ctx, mcpInfoAPIOwnedPrivateKey)
	diagnostics.Append(terraformDiags...)
	diagnostics.Append(apiDiags...)
	if diagnostics.HasError() {
		return result, diagnostics
	}
	terraformOwned, err := decodeMCPInfoLeafSet(terraformRaw, mcpInfoAllLeaves)
	if err != nil {
		diagnostics.AddError("Invalid MCP Ownership State", err.Error()+". No public values or identifiers were included in this diagnostic.")
		return result, diagnostics
	}
	apiOwned, err := decodeMCPInfoLeafSet(apiRaw, mcpInfoCostLeaves)
	if err != nil {
		diagnostics.AddError("Invalid MCP Ownership State", err.Error()+". No public values or identifiers were included in this diagnostic.")
		return result, diagnostics
	}
	result.Terraform, result.API, result.Versioned = terraformOwned, apiOwned, true
	return result, diagnostics
}

func readPendingMCPInfoProvenance(ctx context.Context, private mcpInfoPrivateReader, fallback mcpInfoProvenance) (mcpInfoProvenance, diag.Diagnostics) {
	result := mcpInfoProvenance{Terraform: cloneMCPInfoLeafSet(fallback.Terraform), API: cloneMCPInfoLeafSet(fallback.API), Versioned: true}
	var diagnostics diag.Diagnostics
	if private == nil {
		return result, diagnostics
	}
	terraformRaw, terraformDiags := private.GetKey(ctx, mcpInfoPendingTerraformKey)
	apiRaw, apiDiags := private.GetKey(ctx, mcpInfoPendingAPIKey)
	diagnostics.Append(terraformDiags...)
	diagnostics.Append(apiDiags...)
	if diagnostics.HasError() {
		return result, diagnostics
	}
	if len(terraformRaw) != 0 {
		fields, err := decodeMCPInfoLeafSet(terraformRaw, mcpInfoAllLeaves)
		if err != nil {
			diagnostics.AddError("Invalid MCP Ownership State", err.Error()+". No public values or identifiers were included in this diagnostic.")
			return result, diagnostics
		}
		result.Terraform = fields
	}
	if len(apiRaw) != 0 {
		fields, err := decodeMCPInfoLeafSet(apiRaw, mcpInfoCostLeaves)
		if err != nil {
			diagnostics.AddError("Invalid MCP Ownership State", err.Error()+". No public values or identifiers were included in this diagnostic.")
			return result, diagnostics
		}
		result.API = fields
	}
	return result, diagnostics
}

func writeMCPInfoProvenance(ctx context.Context, private mcpInfoPrivateWriter, provenance mcpInfoProvenance) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if private == nil {
		return diagnostics
	}
	diagnostics.Append(private.SetKey(ctx, mcpInfoTerraformOwnedPrivateKey, encodeMCPInfoLeafSet(provenance.Terraform))...)
	diagnostics.Append(private.SetKey(ctx, mcpInfoAPIOwnedPrivateKey, encodeMCPInfoLeafSet(provenance.API))...)
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
	diagnostics.Append(private.SetKey(ctx, mcpInfoPendingTerraformKey, encodeMCPInfoLeafSet(provenance.Terraform))...)
	diagnostics.Append(private.SetKey(ctx, mcpInfoPendingAPIKey, encodeMCPInfoLeafSet(provenance.API))...)
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
