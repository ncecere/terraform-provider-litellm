package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestMCPServerPhantomCompatibilityModifyPlanProtocol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	schema := schemas.ResourceSchemas[typeName]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))

	legacyConfigValues := map[string]interface{}{
		"server_name": "legacy", "transport": "http", "url": "https://example.invalid/mcp",
		"spec_version": "legacy-custom-version", "skip_url_validation": true,
	}
	legacyStateValues := map[string]interface{}{
		"id": "legacy-id", "server_id": "legacy-id", "server_name": "legacy", "transport": "http",
		"url": "https://example.invalid/mcp", "auth_type": "none",
		"spec_version": "legacy-custom-version", "skip_url_validation": true,
	}
	legacyState := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, legacyStateValues))
	legacyConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, legacyConfigValues))

	plan := func(config, prior, proposed *tfprotov6.DynamicValue) *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		response, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
			TypeName: typeName, Config: config, PriorState: prior, ProposedNewState: proposed,
		})
		if err != nil {
			t.Fatalf("plan RPC: %v", err)
		}
		return response
	}

	for name, values := range map[string]map[string]interface{}{
		"new custom spec_version": {
			"server_name": "new-spec", "transport": "http", "url": "https://example.invalid/mcp",
			"spec_version": "new-custom-version",
		},
		"new true skip_url_validation": {
			"server_name": "new-skip", "transport": "http", "url": "https://example.invalid/mcp",
			"skip_url_validation": true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
			proposedValues := make(map[string]interface{}, len(values)+2)
			for key, value := range values {
				proposedValues[key] = value
			}
			proposedValues["id"], proposedValues["server_id"] = tftypes.UnknownValue, tftypes.UnknownValue
			proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
			if response := plan(config, nullState, proposed); !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
				t.Fatalf("new unsupported phantom value was accepted: %v", response.Diagnostics)
			}
		})
	}

	t.Run("unchanged legacy upgrade", func(t *testing.T) {
		response := plan(legacyConfig, legacyState, legacyState)
		if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
			t.Fatalf("unchanged legacy values were rejected: %v", response.Diagnostics)
		}
	})

	t.Run("changed legacy value", func(t *testing.T) {
		changedConfigValues := map[string]interface{}{
			"server_name": "legacy", "transport": "http", "url": "https://example.invalid/mcp",
			"spec_version": "different-custom-version", "skip_url_validation": true,
		}
		changedConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, changedConfigValues))
		changedProposed := organizationProjectProtocolReplace(t, schema, legacyState, map[string]interface{}{"spec_version": "different-custom-version"})
		if response := plan(changedConfig, legacyState, changedProposed); !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
			t.Fatalf("changed unsupported phantom value was accepted: %v", response.Diagnostics)
		}
	})

	t.Run("unrelated update preserves legacy values", func(t *testing.T) {
		updatedConfigValues := make(map[string]interface{}, len(legacyConfigValues)+1)
		for key, value := range legacyConfigValues {
			updatedConfigValues[key] = value
		}
		updatedConfigValues["description"] = "updated"
		updatedConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, updatedConfigValues))
		updatedProposed := organizationProjectProtocolReplace(t, schema, legacyState, map[string]interface{}{"description": "updated"})
		if response := plan(updatedConfig, legacyState, updatedProposed); accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
			t.Fatalf("unrelated update was rejected: %v", response.Diagnostics)
		}
	})

	t.Run("safe migration away from legacy values", func(t *testing.T) {
		safeConfigValues := map[string]interface{}{
			"server_name": "legacy", "transport": "http", "url": "https://example.invalid/mcp",
			"spec_version": "2024-11-05", "skip_url_validation": false,
		}
		safeConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, safeConfigValues))
		safeProposed := organizationProjectProtocolReplace(t, schema, legacyState, map[string]interface{}{"spec_version": "2024-11-05", "skip_url_validation": false})
		if response := plan(safeConfig, legacyState, safeProposed); accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
			t.Fatalf("safe migration was rejected: %v", response.Diagnostics)
		}
	})

	t.Run("destroy with legacy values", func(t *testing.T) {
		if response := plan(nullState, legacyState, nullState); accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
			t.Fatalf("destroy was rejected: %v", response.Diagnostics)
		}
	})
}
