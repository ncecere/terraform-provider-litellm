package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func protocolMCPFieldPrivate(t *testing.T, ownership mcpFieldOwnership) []byte {
	t.Helper()
	values := map[string][]byte{}
	if err := json.Unmarshal(protocolMCPV2Private(t, emptyMCPInfoProvenance()), &values); err != nil {
		t.Fatal(err)
	}
	values[mcpInfoDocumentAuthoritativePrivateKey] = []byte("true")
	values[mcpFieldOwnershipPrivateKey] = encodeMCPFieldOwnership(ownership)
	private, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	return private
}

func protocolCommittedMCPFieldOwnership(t *testing.T, private []byte) mcpFieldOwnership {
	t.Helper()
	ownership, diagnostics := readMCPFieldOwnership(context.Background(), protocolPrivateMapFromBytes(t, private))
	if diagnostics.HasError() {
		t.Fatalf("read MCP field ownership: %v", diagnostics)
	}
	return ownership
}

func TestMCPServerRestrictedReadRetainsKnownSensitiveStateProtocol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	response := map[string]interface{}{
		"server_id": "restricted-read", "server_name": "restricted-read", "transport": "http", "auth_type": "oauth2",
		"url": nil, "spec_path": nil, "command": nil, "authorization_url": nil, "token_url": nil, "registration_url": nil,
		"mcp_access_groups": []string{}, "args": []string{}, "allowed_tools": []string{}, "extra_headers": []string{},
		"env": map[string]string{}, "static_headers": map[string]string{}, "credentials": map[string]string{}, "mcp_info": map[string]interface{}{},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	values := map[string]interface{}{
		"id": "restricted-read", "server_id": "restricted-read", "server_name": "restricted-read", "transport": "http", "auth_type": "oauth2", "spec_version": "2024-11-05",
		"url": "https://owned.invalid/mcp", "spec_path": "/owned/spec.json", "command": "npx",
		"authorization_url": "https://owned.invalid/authorize", "token_url": "https://owned.invalid/token", "registration_url": "https://owned.invalid/register",
		"mcp_access_groups": protocolMCPStringList("group"), "args": protocolMCPStringList("package"), "allowed_tools": protocolMCPStringList("tool"), "extra_headers": protocolMCPStringList("X-Owned"),
		"env":            map[string]tftypes.Value{"KEY": tftypes.NewValue(tftypes.String, "value")},
		"static_headers": map[string]tftypes.Value{"X-Owned": tftypes.NewValue(tftypes.String, "value")},
		"credentials":    map[string]tftypes.Value{"client_secret": tftypes.NewValue(tftypes.String, "value")},
		"mcp_info_json":  "{}",
	}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	owned := map[string]bool{}
	for _, fieldPath := range mcpFieldPaths {
		owned[fieldPath] = true
	}
	delete(owned, mcpFieldAliasPath)
	delete(owned, mcpFieldDescriptionPath)
	delete(owned, mcpFieldAllowAllKeysPath)
	private := protocolMCPFieldPrivate(t, mcpFieldOwnership{Owned: owned, Removals: map[string]bool{}, Generation: 4, Versioned: true})

	prior := state
	for iteration := 0; iteration < 2; iteration++ {
		read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: prior, Private: private})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
			t.Fatalf("restricted read %d: err=%v diagnostics=%v", iteration, err, read.Diagnostics)
		}
		before, _ := state.Unmarshal(schema.ValueType())
		after, _ := read.NewState.Unmarshal(schema.ValueType())
		if !before.Equal(after) {
			t.Fatalf("restricted read %d drifted known sensitive public state: before=%s after=%s", iteration, before, after)
		}
		ownership := protocolCommittedMCPFieldOwnership(t, read.Private)
		if ownership.Generation != 4 || !mcpFieldSetsEqual(ownership.Owned, owned) {
			t.Fatalf("restricted read %d changed private ownership: %#v", iteration, ownership)
		}
		prior, private = read.NewState, read.Private
	}
}

func TestMCPServerMaskedURLCannotBypassHiddenAuthFlowPreflightProtocol(t *testing.T) {
	for _, hiddenField := range []string{
		"issuer", "oauth2_flow", "dcr_bridge", "token_exchange_endpoint", "audience", "subject_token_type", "token_exchange_profile",
	} {
		hiddenField := hiddenField
		t.Run(hiddenField, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			var puts atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodPut {
					puts.Add(1)
				}
				response := map[string]interface{}{
					"server_id": "hidden-preflight", "server_name": "hidden-preflight", "transport": "http", "auth_type": "oauth2",
					"url": nil, "mcp_info": map[string]interface{}{}, hiddenField: "visible-non-null",
				}
				_ = json.NewEncoder(writer).Encode(response)
			}))
			defer server.Close()

			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_mcp_server"]
			state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
				"id": "hidden-preflight", "server_id": "hidden-preflight", "server_name": "hidden-preflight", "transport": "http", "auth_type": "oauth2", "spec_version": "2024-11-05",
				"url": "https://old.invalid/mcp", "mcp_info_json": "{}",
			}))
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
				"server_name": "hidden-preflight", "transport": "http", "auth_type": "oauth2", "url": "https://new.invalid/mcp",
			}))
			proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"url": "https://new.invalid/mcp"})
			private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || puts.Load() != 0 {
				t.Fatalf("masked URL bypass: err=%v diagnostics=%v puts=%d", err, applied.Diagnostics, puts.Load())
			}
			assertMCPServerFailedUpdateRetainsPriorState(t, schema, state, applied.NewState)
			if !bytes.Equal(applied.Private, planned.PlannedPrivate) {
				t.Fatal("preflight changed provider-private state")
			}
			if got := protocolCommittedMCPFieldOwnership(t, applied.Private); len(got.Owned) != 0 || got.Generation != 0 {
				t.Fatalf("preflight changed committed private ownership: %#v", got)
			}
		})
	}
}

func TestMCPServerEqualRemoteOwnershipTakeoverAndUnknownRetentionProtocol(t *testing.T) {
	t.Run("scalar and bool equal remote takeover", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		var puts atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			if request.Method == http.MethodPut {
				puts.Add(1)
			}
			_, _ = writer.Write([]byte(`{"server_id":"equal-takeover","server_name":"equal-takeover","description":"desired","allow_all_keys":true,"transport":"http","auth_type":"none","url":"https://same.invalid/mcp","mcp_info":{}}`))
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		schema := schemas.ResourceSchemas["litellm_mcp_server"]
		state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
			"id": "equal-takeover", "server_id": "equal-takeover", "server_name": "equal-takeover", "description": "prior", "allow_all_keys": false,
			"transport": "http", "auth_type": "none", "url": "https://same.invalid/mcp", "spec_version": "2024-11-05", "mcp_info_json": "{}",
		}))
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
			"server_name": "equal-takeover", "description": "desired", "allow_all_keys": true, "transport": "http", "url": "https://same.invalid/mcp",
		}))
		proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"description": "desired", "allow_all_keys": true})
		private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || puts.Load() != 0 {
			t.Fatalf("equal takeover: err=%v diagnostics=%v puts=%d", err, applied.Diagnostics, puts.Load())
		}
		ownership := protocolCommittedMCPFieldOwnership(t, applied.Private)
		if !ownership.Owned[mcpFieldDescriptionPath] || !ownership.Owned[mcpFieldAllowAllKeysPath] {
			t.Fatalf("equal takeover did not commit scalar/bool ownership: %#v", ownership)
		}
	})

	t.Run("unknown owned values survive role masking", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		var puts atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			if request.Method == http.MethodPut {
				puts.Add(1)
			}
			_, _ = writer.Write([]byte(`{"server_id":"unknown-fields","server_name":"unknown-fields","alias":"retained_alias","description":"retained","transport":"http","auth_type":"none","url":null,"spec_path":null,"command":null,"authorization_url":null,"token_url":null,"registration_url":null,"mcp_access_groups":[],"args":[],"allowed_tools":[],"extra_headers":[],"env":{},"static_headers":{},"credentials":null,"allow_all_keys":true,"mcp_info":{}}`))
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		schema := schemas.ResourceSchemas["litellm_mcp_server"]
		state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
			"id": "unknown-fields", "server_id": "unknown-fields", "server_name": "unknown-fields", "alias": "retained_alias", "description": "retained",
			"transport": "http", "auth_type": "none", "url": "https://known.invalid/mcp", "spec_path": "/known/spec.json", "command": "node",
			"authorization_url": "https://known.invalid/authorize", "token_url": "https://known.invalid/token", "registration_url": "https://known.invalid/register",
			"mcp_access_groups": protocolMCPStringList("group"), "args": protocolMCPStringList("server.js"), "allowed_tools": protocolMCPStringList("tool"), "extra_headers": protocolMCPStringList("X-Known"),
			"env": map[string]tftypes.Value{"KNOWN": tftypes.NewValue(tftypes.String, "value")}, "static_headers": map[string]tftypes.Value{"X-Known": tftypes.NewValue(tftypes.String, "value")},
			"credentials": map[string]tftypes.Value{"secret": tftypes.NewValue(tftypes.String, "value")}, "allow_all_keys": true,
			"spec_version": "2024-11-05", "mcp_info_json": "{}", "field_ownership_generation": int64(2),
		}))
		unknowns := map[string]interface{}{
			"alias": tftypes.UnknownValue, "description": tftypes.UnknownValue, "url": tftypes.UnknownValue, "spec_path": tftypes.UnknownValue, "command": tftypes.UnknownValue,
			"authorization_url": tftypes.UnknownValue, "token_url": tftypes.UnknownValue, "registration_url": tftypes.UnknownValue,
			"mcp_access_groups": tftypes.UnknownValue, "args": tftypes.UnknownValue, "allowed_tools": tftypes.UnknownValue, "extra_headers": tftypes.UnknownValue,
			"env": tftypes.UnknownValue, "static_headers": tftypes.UnknownValue, "credentials": tftypes.UnknownValue, "allow_all_keys": tftypes.UnknownValue,
		}
		configValues := map[string]interface{}{"server_name": "unknown-fields", "transport": "http"}
		for name, value := range unknowns {
			configValues[name] = value
		}
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		proposed := organizationProjectProtocolReplace(t, schema, state, unknowns)
		owned := map[string]bool{}
		for _, fieldPath := range mcpFieldPaths {
			owned[fieldPath] = true
		}
		committed := mcpFieldOwnership{Owned: owned, Removals: map[string]bool{}, Generation: 2, Versioned: true}
		private := protocolMCPFieldPrivate(t, committed)
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || puts.Load() != 0 {
			t.Fatalf("unknown retention: err=%v diagnostics=%v puts=%d", err, applied.Diagnostics, puts.Load())
		}
		before, _ := state.Unmarshal(schema.ValueType())
		after, _ := applied.NewState.Unmarshal(schema.ValueType())
		if !before.Equal(after) {
			t.Fatalf("unknown owned values were replaced by masking sentinels: before=%s after=%s", before, after)
		}
		ownership := protocolCommittedMCPFieldOwnership(t, applied.Private)
		if ownership.Generation != 2 || !mcpFieldSetsEqual(ownership.Owned, committed.Owned) {
			t.Fatalf("unknown config changed ownership: %#v", ownership)
		}
	})
}

func TestMCPServerMaskedEmptyCollectionsRequireEstablishingPUTProtocol(t *testing.T) {
	emptyList := protocolMCPStringList()
	emptyMap := map[string]tftypes.Value{}
	configuredEmpty := map[string]interface{}{
		"mcp_access_groups": emptyList, "args": emptyList, "allowed_tools": emptyList, "extra_headers": emptyList,
		"env": emptyMap, "static_headers": emptyMap,
	}
	masked := map[string]interface{}{
		"server_id": "empty-collections", "server_name": "empty-collections", "transport": "http", "auth_type": "none",
		"url": "https://known.invalid/mcp", "mcp_access_groups": []string{}, "args": []string{}, "allowed_tools": []string{}, "extra_headers": []string{},
		"env": map[string]string{}, "static_headers": map[string]string{}, "mcp_info": map[string]interface{}{},
	}
	tracked := map[string]bool{
		mcpFieldAccessGroupsPath: true, mcpFieldArgsPath: true, mcpFieldAllowedToolsPath: true,
		mcpFieldExtraHeadersPath: true, mcpFieldEnvPath: true, mcpFieldStaticHeadersPath: true,
	}
	for _, test := range []struct {
		name      string
		state     map[string]interface{}
		ownership mcpFieldOwnership
	}{
		{
			name: "initial empty takeover",
			state: map[string]interface{}{
				"id": "empty-collections", "server_id": "empty-collections", "server_name": "empty-collections",
				"transport": "http", "auth_type": "none", "url": "https://known.invalid/mcp", "spec_version": "2024-11-05",
			},
			ownership: emptyMCPFieldOwnership(),
		},
		{
			name: "owned non-empty to empty",
			state: map[string]interface{}{
				"id": "empty-collections", "server_id": "empty-collections", "server_name": "empty-collections",
				"transport": "http", "auth_type": "none", "url": "https://known.invalid/mcp", "spec_version": "2024-11-05", "field_ownership_generation": int64(2),
				"mcp_access_groups": protocolMCPStringList("group"), "args": protocolMCPStringList("arg"), "allowed_tools": protocolMCPStringList("tool"), "extra_headers": protocolMCPStringList("X-Known"),
				"env": map[string]tftypes.Value{"KNOWN": tftypes.NewValue(tftypes.String, "value")}, "static_headers": map[string]tftypes.Value{"X-Known": tftypes.NewValue(tftypes.String, "value")},
			},
			ownership: mcpFieldOwnership{Owned: tracked, Removals: map[string]bool{}, Generation: 2, Versioned: true},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := map[string]interface{}{"server_name": "empty-collections", "transport": "http", "url": "https://known.invalid/mcp"}
			changes := map[string]interface{}{}
			for name, value := range configuredEmpty {
				config[name], changes[name] = value, value
			}
			result := runMCPUpdateCompletionProtocol(t, test.state, config, changes, masked, masked, protocolMCPFieldPrivate(t, test.ownership))
			if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 {
				t.Fatalf("masked empty collection intent was not established: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
			}
			for _, name := range []string{"mcp_access_groups", "args", "allowed_tools", "extra_headers", "env", "static_headers"} {
				if value, present := result.body[name]; !present || !mcpWireValuesEqual(value, masked[name]) {
					t.Fatalf("PUT omitted explicit empty %s: %#v", name, result.body)
				}
			}
			ownership := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
			for fieldPath := range tracked {
				if !ownership.Owned[fieldPath] {
					t.Fatalf("empty collection ownership was not committed: %#v", ownership)
				}
			}
		})
	}
}

func TestMCPServerUnsupportedCredentialKeysRejectedBeforeMutationProtocol(t *testing.T) {
	ctx := context.Background()
	unsupported := map[string]tftypes.Value{"bogus": tftypes.NewValue(tftypes.String, "value")}

	t.Run("create", func(t *testing.T) {
		var requests atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			requests.Add(1)
			http.NotFound(writer, request)
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		schema := schemas.ResourceSchemas["litellm_mcp_server"]
		configValues := map[string]interface{}{
			"server_name": "unsupported-credentials", "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none", "credentials": unsupported,
		}
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		proposedValues := map[string]interface{}{}
		for name, value := range configValues {
			proposedValues[name] = value
		}
		proposedValues["id"], proposedValues["server_id"] = tftypes.UnknownValue, tftypes.UnknownValue
		proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
		nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, ProposedNewState: proposed})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || requests.Load() != 0 {
			t.Fatalf("unsupported create credentials: err=%v diagnostics=%v requests=%d", err, applied.Diagnostics, requests.Load())
		}
		if strings.Contains(fmtDiagnostics(applied.Diagnostics), "bogus") {
			t.Fatal("credential diagnostic exposed an unsupported configured key")
		}
	})

	t.Run("update", func(t *testing.T) {
		state := map[string]interface{}{
			"id": "unsupported-credentials", "server_id": "unsupported-credentials", "server_name": "unsupported-credentials",
			"transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
		}
		config := map[string]interface{}{
			"server_name": "unsupported-credentials", "transport": "http", "url": "https://known.invalid/mcp", "auth_type": "none", "credentials": unsupported,
		}
		before := map[string]interface{}{
			"server_id": "unsupported-credentials", "server_name": "unsupported-credentials", "transport": "http",
			"url": "https://known.invalid/mcp", "auth_type": "none", "credentials": nil, "mcp_info": map[string]interface{}{},
		}
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"credentials": unsupported}, before, before, protocolMCPFieldPrivate(t, emptyMCPFieldOwnership()))
		if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
			t.Fatalf("unsupported update credentials: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
		}
		assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
	})
}

func TestMCPServerEmptyAliasCreateRejectedAndUpdateConvergesProtocol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.NotFound(writer, request)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]

	t.Run("create", func(t *testing.T) {
		configValues := map[string]interface{}{"server_name": "empty-alias-create", "alias": "", "transport": "http", "url": "https://alias.invalid/mcp"}
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		proposedValues := map[string]interface{}{}
		for key, value := range configValues {
			proposedValues[key] = value
		}
		proposedValues["id"], proposedValues["server_id"] = tftypes.UnknownValue, tftypes.UnknownValue
		proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
		nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, ProposedNewState: proposed})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("empty create alias plan: err=%v diagnostics=%v", err, planned.Diagnostics)
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || requests.Load() != 0 {
			t.Fatalf("empty create alias: err=%v diagnostics=%v requests=%d", err, applied.Diagnostics, requests.Load())
		}
		if strings.Contains(fmt.Sprint(applied.Diagnostics), "empty-alias-create") {
			t.Fatal("empty alias diagnostic exposed unrelated configured content")
		}
	})

	t.Run("update", func(t *testing.T) {
		state := map[string]interface{}{
			"id": "empty-alias-update", "server_id": "empty-alias-update", "server_name": "empty-alias-update", "alias": "old",
			"transport": "http", "auth_type": "none", "url": "https://alias.invalid/mcp", "spec_version": "2024-11-05",
		}
		config := map[string]interface{}{
			"server_name": "empty-alias-update", "alias": "", "transport": "http", "url": "https://alias.invalid/mcp",
		}
		before := map[string]interface{}{
			"server_id": "empty-alias-update", "server_name": "empty-alias-update", "alias": "old", "transport": "http",
			"auth_type": "none", "url": "https://alias.invalid/mcp", "mcp_info": map[string]interface{}{},
		}
		after := map[string]interface{}{
			"server_id": "empty-alias-update", "server_name": "empty-alias-update", "alias": "", "transport": "http",
			"auth_type": "none", "url": "https://alias.invalid/mcp", "mcp_info": map[string]interface{}{},
		}
		private := protocolMCPFieldPrivate(t, mcpFieldOwnership{Owned: map[string]bool{mcpFieldAliasPath: true}, Removals: map[string]bool{}, Generation: 1, Versioned: true})
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"alias": ""}, before, after, private)
		if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 || result.body["alias"] != "" {
			t.Fatalf("empty update alias did not converge: puts=%d body=%#v diagnostics=%v", result.puts, result.body, result.applied.Diagnostics)
		}
		if got := protocolCommittedMCPFieldOwnership(t, result.applied.Private); got.Generation != 1 || !got.Owned[mcpFieldAliasPath] {
			t.Fatalf("empty update alias changed ownership unexpectedly: %#v", got)
		}
	})
}
