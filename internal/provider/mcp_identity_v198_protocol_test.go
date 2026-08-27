package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestMCPV198IdentityAndPrefixValidation(t *testing.T) {
	for value, valid := range map[string]bool{
		"custom": true, "with space": true, "with?query": true,
		"": false, ".": false, "..": false, "with/slash": false,
		"all-team-mcpservers": false, "all-proxy-mcpservers": false,
	} {
		if got := mcpServerIDValidV198(value); got != valid {
			t.Fatalf("server id validity for %q = %t, want %t", value, got, valid)
		}
	}
	for value, valid := range map[string]bool{
		"name": true, "UPPER_123.name": true, "": false, "has-dash": false,
		"has space": false, "has/slash": false, strings.Repeat("a", 128): true, strings.Repeat("a", 129): false,
	} {
		if got := mcpToolPrefixValidV198(value); got != valid {
			t.Fatalf("tool prefix validity for %q = %t, want %t", value, got, valid)
		}
	}
	if got := mcpNormalizeAliasV198("alias with  spaces"); got != "alias_with__spaces" {
		t.Fatalf("alias normalization = %q", got)
	}
}

func TestMCPServerCreateCustomIdentityNullableNameAndAliasNormalizationProtocol(t *testing.T) {
	tests := []struct {
		name               string
		serverName         interface{}
		configuredAlias    interface{}
		expectedAlias      interface{}
		expectedServerName interface{}
	}{
		{name: "custom named alias fallback", serverName: "named_server", configuredAlias: nil, expectedAlias: "named_server", expectedServerName: "named_server"},
		{name: "custom unnamed id fallback", serverName: nil, configuredAlias: nil, expectedAlias: nil, expectedServerName: nil},
		{name: "custom alias fallback", serverName: nil, configuredAlias: "alias only", expectedAlias: "alias_only", expectedServerName: nil},
		{name: "normalized explicit alias", serverName: "named_server", configuredAlias: "alias with  spaces", expectedAlias: "alias_with__spaces", expectedServerName: "named_server"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			const customID = "custom.identity_123"
			var postBody atomic.Value
			var posts, reads atomic.Int64
			response := map[string]interface{}{
				"server_id": customID, "server_name": test.expectedServerName, "alias": test.expectedAlias,
				"transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch request.Method {
				case http.MethodPost:
					posts.Add(1)
					var body map[string]interface{}
					_ = json.NewDecoder(request.Body).Decode(&body)
					postBody.Store(body)
				case http.MethodGet:
					reads.Add(1)
				}
				_ = json.NewEncoder(writer).Encode(response)
			}))
			defer server.Close()

			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_mcp_server"]
			configValues := map[string]interface{}{
				"server_id": customID, "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none",
			}
			if test.serverName != nil {
				configValues["server_name"] = test.serverName
			}
			if test.configuredAlias != nil {
				configValues["alias"] = test.configuredAlias
			}
			config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, configValues)
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: nullState,
				PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || posts.Load() != 1 || reads.Load() != 1 {
				t.Fatalf("create: err=%v diagnostics=%v posts=%d reads=%d", err, applied.Diagnostics, posts.Load(), reads.Load())
			}
			body := postBody.Load().(map[string]interface{})
			if body["server_id"] != customID || !mcpWireValuesEqual(body["server_name"], test.expectedServerName) || !mcpWireValuesEqual(body["alias"], test.expectedAlias) {
				t.Fatalf("create body = %#v", body)
			}
			attributes := protocolAttributeMap(t, schema, applied.NewState)
			if got := protocolString(t, attributes["id"]); got != customID || protocolString(t, attributes["server_id"]) != customID {
				t.Fatalf("canonical identity = %q", got)
			}
			if test.expectedServerName == nil && !attributes["server_name"].IsNull() {
				t.Fatalf("unnamed state published server_name: %s", attributes["server_name"])
			}
			steady, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: applied.NewState,
				ProposedNewState: applied.NewState, PriorPrivate: applied.Private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) || len(steady.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, applied.NewState, steady) != organizationProjectProtocolActionNoOp {
				t.Fatalf("steady plan: err=%v diagnostics=%v replace=%v action=%s", err, steady.Diagnostics, steady.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, applied.NewState, steady))
			}
		})
	}
}

func TestMCPServerCustomIdentityCollisionAndMismatchFailClosedProtocol(t *testing.T) {
	for _, mode := range []string{"collision", "mismatched readback"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			var reads atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodPost {
					if mode == "collision" {
						http.Error(writer, `{"error":"collision"}`, http.StatusBadRequest)
						return
					}
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{"server_id": "custom-id"})
					return
				}
				reads.Add(1)
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{
					"server_id": "different-id", "server_name": "name", "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none",
				})
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_mcp_server"]
			config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, map[string]interface{}{
				"server_id": "custom-id", "server_name": "name", "transport": "http", "url": "https://known.invalid/mcp",
			})
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: nullState,
				PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("%s was not rejected: err=%v diagnostics=%v", mode, err, applied.Diagnostics)
			}
			if mode == "collision" {
				published := false
				if applied.NewState != nil {
					value, decodeErr := applied.NewState.Unmarshal(schema.ValueType())
					published = decodeErr != nil || !value.IsNull()
				}
				if reads.Load() != 0 || published {
					t.Fatalf("collision published state or read back: reads=%d state=%v", reads.Load(), applied.NewState)
				}
			} else {
				assertMCPServerIdentityOnlyState(t, schema, applied.NewState, "custom-id")
			}
		})
	}
}

func TestMCPServerIdentityReplacementAndLegacyOmissionProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{})
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	stateValues := map[string]interface{}{
		"id": "old-id", "server_id": "old-id", "server_name": "name", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))

	t.Run("configured change replaces", func(t *testing.T) {
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
			"server_id": "new-id", "server_name": "name", "transport": "http", "url": "https://known.invalid/mcp",
		}))
		proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"server_id": "new-id", "id": tftypes.UnknownValue})
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
			TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed,
		})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || len(planned.RequiresReplace) == 0 {
			t.Fatalf("replacement plan: err=%v diagnostics=%v replace=%v", err, planned.Diagnostics, planned.RequiresReplace)
		}
		if id := protocolAttributeMap(t, schema, planned.PlannedState)["id"]; id.IsKnown() {
			t.Fatalf("replacement retained old computed id: %s", id)
		}
	})

	for _, test := range []struct {
		name   string
		config map[string]interface{}
	}{
		{name: "legacy generated id omission", config: map[string]interface{}{"server_name": "name", "transport": "http", "url": "https://known.invalid/mcp"}},
		{name: "custom id omission", config: map[string]interface{}{"server_name": "name", "transport": "http", "url": "https://known.invalid/mcp"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, test.config))
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: state,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || len(planned.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, state, planned) != organizationProjectProtocolActionNoOp {
				t.Fatalf("omission plan: err=%v diagnostics=%v replace=%v action=%s", err, planned.Diagnostics, planned.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, state, planned))
			}
		})
	}
}

func TestMCPServerNameClearAndAliasNormalizationUpdateProtocol(t *testing.T) {
	state := map[string]interface{}{
		"id": "identity-update", "server_id": "identity-update", "server_name": "old_name", "alias": "old_alias",
		"transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}
	before := map[string]interface{}{
		"server_id": "identity-update", "server_name": "old_name", "alias": "old_alias", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
	}

	t.Run("name clear retains alias fallback", func(t *testing.T) {
		config := map[string]interface{}{"alias": "old_alias", "transport": "http", "url": "https://known.invalid/mcp"}
		after := map[string]interface{}{
			"server_id": "identity-update", "server_name": nil, "alias": "old_alias", "transport": "http",
			"url": "https://known.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		}
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"server_name": nil}, before, after, protocolMCPFieldPrivate(t, emptyMCPFieldOwnership()))
		value, present := result.body["server_name"]
		if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 || !present || value != nil {
			t.Fatalf("name clear: puts=%d body=%#v diagnostics=%v", result.puts, result.body, result.applied.Diagnostics)
		}
	})

	t.Run("name and alias clear use id fallback", func(t *testing.T) {
		owned := mcpFieldOwnership{Owned: map[string]bool{mcpFieldAliasPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
		config := map[string]interface{}{"transport": "http", "url": "https://known.invalid/mcp"}
		after := map[string]interface{}{
			"server_id": "identity-update", "server_name": nil, "alias": nil, "transport": "http",
			"url": "https://known.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		}
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"server_name": nil, "alias": nil}, before, after, protocolMCPFieldPrivate(t, owned))
		serverName, namePresent := result.body["server_name"]
		alias, aliasPresent := result.body["alias"]
		if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 || !namePresent || serverName != nil || !aliasPresent || alias != nil {
			t.Fatalf("id fallback clear: puts=%d body=%#v diagnostics=%v", result.puts, result.body, result.applied.Diagnostics)
		}
	})

	t.Run("alias normalization", func(t *testing.T) {
		owned := mcpFieldOwnership{Owned: map[string]bool{mcpFieldAliasPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true}
		config := map[string]interface{}{"server_name": "old_name", "alias": "new alias", "transport": "http", "url": "https://known.invalid/mcp"}
		after := map[string]interface{}{
			"server_id": "identity-update", "server_name": "old_name", "alias": "new_alias", "transport": "http",
			"url": "https://known.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		}
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"alias": "new alias"}, before, after, protocolMCPFieldPrivate(t, owned))
		if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 || result.body["alias"] != "new_alias" {
			t.Fatalf("alias normalization: puts=%d body=%#v diagnostics=%v", result.puts, result.body, result.applied.Diagnostics)
		}
		attributes := protocolAttributeMap(t, result.schema, result.applied.NewState)
		if got := protocolString(t, attributes["alias"]); got != "new_alias" {
			t.Fatalf("normalized alias state = %q", got)
		}
	})
}

func TestMCPServerInvalidIdentityAndPrefixConfigRejectedProtocol(t *testing.T) {
	ctx := context.Background()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, "http://127.0.0.1:1")
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	tests := []map[string]interface{}{
		{"server_id": ""}, {"server_id": "with/slash"}, {"server_id": "all-proxy-mcpservers"},
		{"server_name": ""}, {"server_name": "has-dash"}, {"server_name": "has space"}, {"server_name": strings.Repeat("a", 129)},
		{"alias": "has-dash"}, {"alias": "has/slash"}, {"alias": strings.Repeat("a", 129)},
	}
	for _, invalid := range tests {
		values := map[string]interface{}{"transport": "http", "url": "https://known.invalid/mcp"}
		for key, value := range invalid {
			values[key] = value
		}
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
		validated, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: "litellm_mcp_server", Config: config})
		if err != nil {
			t.Fatalf("invalid config validation transport %#v: %v", invalid, err)
		}
		if accessGroupProtocolDiagnosticsHaveError(validated.Diagnostics) {
			continue
		}
		proposedValues := make(map[string]interface{}, len(values)+2)
		for key, value := range values {
			proposedValues[key] = value
		}
		proposedValues["id"] = tftypes.UnknownValue
		if _, configured := values["server_id"]; !configured {
			proposedValues["server_id"] = tftypes.UnknownValue
		}
		proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
		nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, ProposedNewState: proposed})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("invalid config %#v: err=%v diagnostics=%v", invalid, err, planned.Diagnostics)
		}
	}
}

func TestMCPServerUnchangedHistoricalPrefixRemainsPlannableAndDestroyableProtocol(t *testing.T) {
	ctx := context.Background()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, "http://127.0.0.1:1")
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "historical-id", "server_id": "historical-id", "server_name": "historical-name", "alias": "historical-alias",
		"transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05", "field_ownership_generation": int64(1),
	}))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"server_name": "historical-name", "alias": "historical-alias", "transport": "http", "url": "https://known.invalid/mcp",
	}))
	private := protocolMCPFieldPrivate(t, mcpFieldOwnership{Owned: map[string]bool{mcpFieldAliasPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: state, PriorPrivate: private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, state, planned) != organizationProjectProtocolActionNoOp {
		t.Fatalf("historical no-op: err=%v diagnostics=%v action=%s", err, planned.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, state, planned))
	}
	nullValue := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	destroy, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: nullValue, PriorState: state, ProposedNewState: nullValue, PriorPrivate: private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroy.Diagnostics) {
		t.Fatalf("historical destroy plan: err=%v diagnostics=%v", err, destroy.Diagnostics)
	}
}

func TestMCPServerCustomIdentityDestroyUsesCanonicalOldIDProtocol(t *testing.T) {
	ctx := context.Background()
	var deletedPath atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			deletedPath.Store(request.URL.EscapedPath())
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{})
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "old.custom_id", "server_id": "old.custom_id", "server_name": "name", "transport": "http",
		"url": "https://known.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}))
	nullValue := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: nullValue, PriorState: state, ProposedNewState: nullValue,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("destroy plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: nullValue, PriorState: state,
		PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || deletedPath.Load() != "/v1/mcp/server/old.custom_id" {
		t.Fatalf("destroy: err=%v diagnostics=%v path=%v", err, applied.Diagnostics, deletedPath.Load())
	}
}

func TestMCPServerInvalidImportIdentityRejectedProtocol(t *testing.T) {
	ctx := context.Background()
	protocolServer, _ := configuredImportProtocolServer(t, ctx, "http://127.0.0.1:1")
	for _, id := range []string{"", ".", "..", "with/slash", "all-team-mcpservers", "all-proxy-mcpservers"} {
		imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_mcp_server", ID: id})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 0 {
			t.Fatalf("invalid import %q: err=%v diagnostics=%v resources=%d", id, err, imported.Diagnostics, len(imported.ImportedResources))
		}
	}
}
