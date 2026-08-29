package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

type mcpInfoMapPrivate map[string][]byte

func (p mcpInfoMapPrivate) GetKey(_ context.Context, key string) ([]byte, diag.Diagnostics) {
	return p[key], nil
}

func (p mcpInfoMapPrivate) SetKey(_ context.Context, key string, value []byte) diag.Diagnostics {
	if value == nil {
		delete(p, key)
	} else {
		p[key] = append([]byte(nil), value...)
	}
	return nil
}

func canonicalMCPInfoPrivateMap() mcpInfoMapPrivate {
	return mcpInfoMapPrivate{
		mcpInfoOwnershipVersionKey:      []byte(mcpInfoOwnershipVersion),
		mcpInfoTerraformOwnedPrivateKey: []byte(`[]`),
		mcpInfoAPIOwnedPrivateKey:       []byte(`[]`),
	}
}

func cloneMCPInfoPrivateMap(source mcpInfoMapPrivate) mcpInfoMapPrivate {
	result := make(mcpInfoMapPrivate, len(source))
	for key, value := range source {
		result[key] = append([]byte(nil), value...)
	}
	return result
}

func TestMCPInfoPrivateProvenanceCorruptionCrossProduct(t *testing.T) {
	malformedPayloads := []struct {
		name string
		raw  []byte
	}{
		{name: "empty", raw: []byte{}},
		{name: "null", raw: []byte(`null`)},
		{name: "object", raw: []byte(`{}`)},
		{name: "malformed", raw: []byte(`[")`)},
		{name: "duplicate", raw: []byte(`["mcp_info.description","mcp_info.description"]`)},
		{name: "unsorted", raw: []byte(`["mcp_info.logo_url","mcp_info.description"]`)},
		{name: "whitespace", raw: []byte(`[ ]`)},
	}

	for _, pair := range []struct {
		name         string
		terraformKey string
		apiKey       string
	}{
		{name: "committed", terraformKey: mcpInfoTerraformOwnedPrivateKey, apiKey: mcpInfoAPIOwnedPrivateKey},
		{name: "pending", terraformKey: mcpInfoPendingTerraformKey, apiKey: mcpInfoPendingAPIKey},
	} {
		for _, key := range []struct {
			name string
			key  string
		}{
			{name: "terraform", key: pair.terraformKey},
			{name: "api", key: pair.apiKey},
		} {
			for _, malformed := range malformedPayloads {
				t.Run(pair.name+"/"+key.name+"/"+malformed.name, func(t *testing.T) {
					private := cloneMCPInfoPrivateMap(canonicalMCPInfoPrivateMap())
					if pair.name == "pending" {
						private[pair.terraformKey] = []byte(`[]`)
						private[pair.apiKey] = []byte(`[]`)
					}
					private[key.key] = malformed.raw
					_, diagnostics := readMCPInfoProvenance(context.Background(), private)
					if !diagnostics.HasError() {
						t.Fatal("corrupt private provenance was accepted")
					}
				})
			}
		}
	}

	structural := map[string]func(mcpInfoMapPrivate){
		"terraform without version": func(p mcpInfoMapPrivate) {
			delete(p, mcpInfoOwnershipVersionKey)
			delete(p, mcpInfoAPIOwnedPrivateKey)
		},
		"api without version": func(p mcpInfoMapPrivate) {
			delete(p, mcpInfoOwnershipVersionKey)
			delete(p, mcpInfoTerraformOwnedPrivateKey)
		},
		"pending without version": func(p mcpInfoMapPrivate) {
			delete(p, mcpInfoOwnershipVersionKey)
			delete(p, mcpInfoTerraformOwnedPrivateKey)
			delete(p, mcpInfoAPIOwnedPrivateKey)
			p[mcpInfoPendingTerraformKey] = []byte(`[]`)
			p[mcpInfoPendingAPIKey] = []byte(`[]`)
		},
		"missing committed terraform": func(p mcpInfoMapPrivate) { delete(p, mcpInfoTerraformOwnedPrivateKey) },
		"missing committed api":       func(p mcpInfoMapPrivate) { delete(p, mcpInfoAPIOwnedPrivateKey) },
		"unsupported version":         func(p mcpInfoMapPrivate) { p[mcpInfoOwnershipVersionKey] = []byte(`2`) },
		"malformed version":           func(p mcpInfoMapPrivate) { p[mcpInfoOwnershipVersionKey] = []byte{} },
		"pending terraform only": func(p mcpInfoMapPrivate) {
			p[mcpInfoPendingTerraformKey] = []byte(`[]`)
		},
		"pending api only": func(p mcpInfoMapPrivate) { p[mcpInfoPendingAPIKey] = []byte(`[]`) },
		"committed overlap": func(p mcpInfoMapPrivate) {
			p[mcpInfoTerraformOwnedPrivateKey] = []byte(`["mcp_info.mcp_server_cost_info.default_cost_per_query"]`)
			p[mcpInfoAPIOwnedPrivateKey] = []byte(`["mcp_info.mcp_server_cost_info.default_cost_per_query"]`)
		},
		"pending overlap": func(p mcpInfoMapPrivate) {
			p[mcpInfoPendingTerraformKey] = []byte(`["mcp_info.mcp_server_cost_info.tool_name_to_cost_per_query"]`)
			p[mcpInfoPendingAPIKey] = []byte(`["mcp_info.mcp_server_cost_info.tool_name_to_cost_per_query"]`)
		},
		"unknown terraform leaf": func(p mcpInfoMapPrivate) {
			p[mcpInfoTerraformOwnedPrivateKey] = []byte(`["mcp_info.secret"]`)
		},
		"string leaf in api set": func(p mcpInfoMapPrivate) {
			p[mcpInfoAPIOwnedPrivateKey] = []byte(`["mcp_info.description"]`)
		},
		"unknown pending terraform leaf": func(p mcpInfoMapPrivate) {
			p[mcpInfoPendingTerraformKey] = []byte(`["mcp_info.secret"]`)
			p[mcpInfoPendingAPIKey] = []byte(`[]`)
		},
		"string leaf in pending api set": func(p mcpInfoMapPrivate) {
			p[mcpInfoPendingTerraformKey] = []byte(`[]`)
			p[mcpInfoPendingAPIKey] = []byte(`["mcp_info.description"]`)
		},
	}
	for name, corrupt := range structural {
		t.Run(name, func(t *testing.T) {
			private := cloneMCPInfoPrivateMap(canonicalMCPInfoPrivateMap())
			corrupt(private)
			_, diagnostics := readMCPInfoProvenance(context.Background(), private)
			if !diagnostics.HasError() {
				t.Fatal("corrupt private provenance was accepted")
			}
		})
	}
}

func TestMCPInfoPrivateProvenanceLegacyAndCanonicalPairs(t *testing.T) {
	legacy, diagnostics := readMCPInfoProvenance(context.Background(), mcpInfoMapPrivate{"unrelated": []byte("value")})
	if diagnostics.HasError() || legacy.Versioned {
		t.Fatalf("legacy provenance rejected or versioned: %#v", diagnostics)
	}
	private := canonicalMCPInfoPrivateMap()
	private[mcpInfoTerraformOwnedPrivateKey] = []byte(`["mcp_info.description"]`)
	private[mcpInfoAPIOwnedPrivateKey] = []byte(`["mcp_info.mcp_server_cost_info.default_cost_per_query"]`)
	private[mcpInfoPendingTerraformKey] = []byte(`["mcp_info.logo_url"]`)
	private[mcpInfoPendingAPIKey] = []byte(`["mcp_info.mcp_server_cost_info.tool_name_to_cost_per_query"]`)
	committed, diagnostics := readMCPInfoProvenance(context.Background(), private)
	if diagnostics.HasError() || !committed.Versioned || !committed.Terraform[mcpInfoDescriptionLeaf] || !committed.API[mcpInfoDefaultCostLeaf] {
		t.Fatalf("canonical committed pair rejected: %#v %#v", committed, diagnostics)
	}
	pending, diagnostics := readPendingMCPInfoProvenance(context.Background(), private, committed)
	if diagnostics.HasError() || !pending.Terraform[mcpInfoLogoURLLeaf] || !pending.API[mcpInfoToolCostsLeaf] {
		t.Fatalf("canonical pending pair rejected: %#v %#v", pending, diagnostics)
	}
}
