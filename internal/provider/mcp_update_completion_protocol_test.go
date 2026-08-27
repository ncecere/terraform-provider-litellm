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

type mcpUpdateCompletionProtocolResult struct {
	applied *tfprotov6.ApplyResourceChangeResponse
	body    map[string]interface{}
	puts    int64
	schema  *tfprotov6.Schema
	state   *tfprotov6.DynamicValue
}

func runMCPUpdateCompletionProtocol(t *testing.T, stateValues, configValues, proposedChanges, before, after map[string]interface{}, private []byte) mcpUpdateCompletionProtocolResult {
	t.Helper()
	ctx := context.Background()
	var puts atomic.Int64
	var body map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			response := before
			if puts.Load() != 0 {
				response = after
			}
			_ = json.NewEncoder(writer).Encode(response)
		case http.MethodPut:
			_ = json.NewDecoder(request.Body).Decode(&body)
			puts.Add(1)
			_ = json.NewEncoder(writer).Encode(after)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := organizationProjectProtocolReplace(t, schema, state, proposedChanges)
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: state,
		PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil {
		t.Fatalf("apply transport error: %v", err)
	}
	return mcpUpdateCompletionProtocolResult{applied: applied, body: body, puts: puts.Load(), schema: schema, state: state}
}

func TestMCPServerNameUpdatePreservesUnownedAliasProtocol(t *testing.T) {
	private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
	state := map[string]interface{}{
		"id": "alias-preserve", "server_id": "alias-preserve", "server_name": "old-name", "alias": "remote_alias",
		"transport": "http", "url": "https://alias.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}
	config := map[string]interface{}{
		"server_name": "new-name", "transport": "http", "url": "https://alias.invalid/mcp",
	}
	before := map[string]interface{}{
		"server_id": "alias-preserve", "server_name": "old-name", "alias": "remote_alias", "transport": "http",
		"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
	}

	t.Run("exact readback", func(t *testing.T) {
		after := map[string]interface{}{
			"server_id": "alias-preserve", "server_name": "new-name", "alias": "remote_alias", "transport": "http",
			"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		}
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"server_name": "new-name", "alias": nil}, before, after, private)
		if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) {
			t.Fatalf("apply: diagnostics=%v", result.applied.Diagnostics)
		}
		if result.puts != 1 || result.body["alias"] != "remote_alias" {
			t.Fatalf("PUT alias was not preserved exactly: puts=%d body=%#v", result.puts, result.body)
		}
		ownership := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
		if ownership.Owned[mcpFieldAliasPath] {
			t.Fatalf("injected alias changed private ownership: %#v", ownership)
		}
	})

	t.Run("mismatched readback", func(t *testing.T) {
		after := map[string]interface{}{
			"server_id": "alias-preserve", "server_name": "new-name", "alias": "regenerated", "transport": "http",
			"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		}
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"server_name": "new-name", "alias": nil}, before, after, private)
		if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 {
			t.Fatalf("alias mismatch was not surfaced: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
		}
		assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
		ownership := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
		if ownership.Generation != 0 || ownership.Owned[mcpFieldAliasPath] {
			t.Fatalf("alias mismatch changed private ownership: %#v", ownership)
		}
	})
}

func TestMCPServerNameUpdateAliasAmbiguityHasZeroPUTProtocol(t *testing.T) {
	for name, alias := range map[string]interface{}{"null alias": nil, "empty alias": "", "normalizing alias": "remote alias"} {
		t.Run(name, func(t *testing.T) {
			private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
			state := map[string]interface{}{
				"id": "alias-ambiguous", "server_id": "alias-ambiguous", "server_name": "old-name", "alias": alias,
				"transport": "http", "url": "https://alias.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
			}
			config := map[string]interface{}{"server_name": "new-name", "transport": "http", "url": "https://alias.invalid/mcp"}
			before := map[string]interface{}{
				"server_id": "alias-ambiguous", "server_name": "old-name", "alias": alias, "transport": "http",
				"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
			}
			result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"server_name": "new-name", "alias": nil}, before, before, private)
			if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
				t.Fatalf("ambiguous alias was not rejected before PUT: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
			}
			assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
			if strings.Contains(fmtDiagnostics(result.applied.Diagnostics), "new-name") {
				t.Fatal("alias preflight diagnostic exposed configured content")
			}
		})
	}
}

func TestMCPServerNameAndAliasRemovalHasZeroPUTProtocol(t *testing.T) {
	private := protocolMCPFieldPrivate(t, mcpFieldOwnership{
		Owned: map[string]bool{mcpFieldAliasPath: true}, Removals: map[string]bool{}, Generation: 2, Versioned: true,
	})
	state := map[string]interface{}{
		"id": "alias-remove", "server_id": "alias-remove", "server_name": "old-name", "alias": "managed",
		"transport": "http", "url": "https://alias.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}
	config := map[string]interface{}{"server_name": "new-name", "transport": "http", "url": "https://alias.invalid/mcp"}
	before := map[string]interface{}{
		"server_id": "alias-remove", "server_name": "old-name", "alias": "managed", "transport": "http",
		"url": "https://alias.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
	}
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"server_name": "new-name", "alias": nil}, before, before, private)
	if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
		t.Fatalf("simultaneous alias removal was not rejected before PUT: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
	}
	assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
	ownership := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
	if ownership.Generation != 2 || !ownership.Owned[mcpFieldAliasPath] {
		t.Fatalf("failed alias removal changed private ownership: %#v", ownership)
	}
}

func TestMCPServerTransportUpdateCompletesEndpointPayloadProtocol(t *testing.T) {
	for _, test := range []struct {
		name          string
		oldTransport  string
		newTransport  string
		url           interface{}
		specPath      interface{}
		mismatchURL   bool
		wantApplyFail bool
	}{
		{name: "http to sse preserves both endpoints", oldTransport: "http", newTransport: "sse", url: "https://transport.invalid/mcp", specPath: "/srv/openapi.json"},
		{name: "sse to http preserves URL", oldTransport: "sse", newTransport: "http", url: "https://transport.invalid/mcp", specPath: nil},
		{name: "injected endpoint readback mismatch", oldTransport: "http", newTransport: "sse", url: "https://transport.invalid/mcp", specPath: nil, mismatchURL: true, wantApplyFail: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
			state := map[string]interface{}{
				"id": "transport-endpoint", "server_id": "transport-endpoint", "server_name": "transport-endpoint",
				"transport": test.oldTransport, "url": test.url, "spec_path": test.specPath, "auth_type": "none", "spec_version": "2024-11-05",
			}
			config := map[string]interface{}{
				"server_name": "transport-endpoint", "transport": test.newTransport, "url": test.url, "spec_path": test.specPath,
			}
			before := map[string]interface{}{
				"server_id": "transport-endpoint", "server_name": "transport-endpoint", "transport": test.oldTransport,
				"url": test.url, "spec_path": test.specPath, "auth_type": "none", "mcp_info": map[string]interface{}{},
			}
			after := map[string]interface{}{
				"server_id": "transport-endpoint", "server_name": "transport-endpoint", "transport": test.newTransport,
				"url": test.url, "spec_path": test.specPath, "auth_type": "none", "mcp_info": map[string]interface{}{},
			}
			if test.mismatchURL {
				after["url"] = "https://different.invalid/mcp"
			}
			result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"transport": test.newTransport}, before, after, private)
			if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) != test.wantApplyFail || result.puts != 1 {
				t.Fatalf("apply result: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
			}
			if result.body["url"] != test.url {
				t.Fatalf("unchanged URL missing from transport PUT: %#v", result.body)
			}
			if test.specPath != nil && result.body["spec_path"] != test.specPath {
				t.Fatalf("unchanged spec_path missing from transport PUT: %#v", result.body)
			}
			if test.wantApplyFail {
				assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
			}
		})
	}
}

func TestMCPServerTransportUpdateCompletesStdioPayloadProtocol(t *testing.T) {
	private := protocolMCPFieldPrivate(t, mcpFieldOwnership{
		Owned: map[string]bool{mcpFieldCommandPath: true, mcpFieldArgsPath: true}, Removals: map[string]bool{}, Generation: 2, Versioned: true,
	})
	args := protocolMCPStringList("server.py")
	wireArgs := []string{"server.py"}
	state := map[string]interface{}{
		"id": "transport-stdio", "server_id": "transport-stdio", "server_name": "transport-stdio", "transport": "http",
		"url": "https://transport.invalid/mcp", "command": "python3", "args": args, "auth_type": "none", "spec_version": "2024-11-05",
	}
	config := map[string]interface{}{
		"server_name": "transport-stdio", "transport": "stdio", "command": "python3", "args": args,
	}
	before := map[string]interface{}{
		"server_id": "transport-stdio", "server_name": "transport-stdio", "transport": "http", "url": "https://transport.invalid/mcp",
		"command": "python3", "args": wireArgs, "auth_type": "none", "mcp_info": map[string]interface{}{},
	}
	after := map[string]interface{}{
		"server_id": "transport-stdio", "server_name": "transport-stdio", "transport": "stdio",
		"command": "python3", "args": wireArgs, "auth_type": "none", "mcp_info": map[string]interface{}{},
	}
	result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"transport": "stdio", "url": nil}, before, after, private)
	if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 {
		t.Fatalf("stdio apply: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
	}
	if result.body["command"] != "python3" || !mcpWireValuesEqual(result.body["args"], wireArgs) {
		t.Fatalf("unchanged stdio dependencies missing from PUT: %#v", result.body)
	}
	ownership := protocolCommittedMCPFieldOwnership(t, result.applied.Private)
	if !ownership.Owned[mcpFieldCommandPath] || !ownership.Owned[mcpFieldArgsPath] {
		t.Fatalf("stdio update lost private ownership: %#v", ownership)
	}
}

func TestMCPServerTransportUpdateUnsafeDependenciesHaveZeroPUTProtocol(t *testing.T) {
	for _, test := range []struct {
		name      string
		transport string
		state     map[string]interface{}
		config    map[string]interface{}
		changes   map[string]interface{}
		before    map[string]interface{}
	}{
		{
			name: "unknown HTTP endpoint", transport: "sse",
			state: map[string]interface{}{
				"id": "unsafe-http", "server_id": "unsafe-http", "server_name": "unsafe-http", "transport": "stdio",
				"command": "python3", "args": protocolMCPStringList("server.py"), "auth_type": "none", "spec_version": "2024-11-05",
			},
			config:  map[string]interface{}{"server_name": "unsafe-http", "transport": "sse", "url": tftypes.UnknownValue},
			changes: map[string]interface{}{"transport": "sse", "url": tftypes.UnknownValue, "command": nil, "args": nil},
			before: map[string]interface{}{
				"server_id": "unsafe-http", "server_name": "unsafe-http", "transport": "stdio", "command": "python3",
				"args": []string{"server.py"}, "auth_type": "none", "mcp_info": map[string]interface{}{},
			},
		},
		{
			name: "unknown stdio command", transport: "stdio",
			state: map[string]interface{}{
				"id": "unsafe-stdio", "server_id": "unsafe-stdio", "server_name": "unsafe-stdio", "transport": "http",
				"url": "https://transport.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
			},
			config:  map[string]interface{}{"server_name": "unsafe-stdio", "transport": "stdio", "command": tftypes.UnknownValue, "args": protocolMCPStringList("server.py")},
			changes: map[string]interface{}{"transport": "stdio", "url": nil, "command": tftypes.UnknownValue, "args": protocolMCPStringList("server.py")},
			before: map[string]interface{}{
				"server_id": "unsafe-stdio", "server_name": "unsafe-stdio", "transport": "http", "url": "https://transport.invalid/mcp",
				"auth_type": "none", "mcp_info": map[string]interface{}{},
			},
		},
		{
			name: "unknown stdio args", transport: "stdio",
			state: map[string]interface{}{
				"id": "unsafe-args", "server_id": "unsafe-args", "server_name": "unsafe-args", "transport": "http",
				"url": "https://transport.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
			},
			config:  map[string]interface{}{"server_name": "unsafe-args", "transport": "stdio", "command": "python3", "args": tftypes.UnknownValue},
			changes: map[string]interface{}{"transport": "stdio", "url": nil, "command": "python3", "args": tftypes.UnknownValue},
			before: map[string]interface{}{
				"server_id": "unsafe-args", "server_name": "unsafe-args", "transport": "http", "url": "https://transport.invalid/mcp",
				"auth_type": "none", "mcp_info": map[string]interface{}{},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
			result := runMCPUpdateCompletionProtocol(t, test.state, test.config, test.changes, test.before, test.before, private)
			if !accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 0 {
				t.Fatalf("unsafe %s dependencies were not rejected before PUT: puts=%d diagnostics=%v", test.transport, result.puts, result.applied.Diagnostics)
			}
			assertMCPServerFailedUpdateRetainsPriorState(t, result.schema, result.state, result.applied.NewState)
		})
	}
}

func TestMCPServerTransportUpdateAbsentDependenciesHasZeroPUTProtocol(t *testing.T) {
	ctx := context.Background()
	var puts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPut {
			puts.Add(1)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"server_id": "absent-dependencies", "server_name": "absent-dependencies", "transport": "http",
			"url": "https://transport.invalid/mcp", "auth_type": "none", "mcp_info": map[string]interface{}{},
		})
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "absent-dependencies", "server_id": "absent-dependencies", "server_name": "absent-dependencies", "transport": "http",
		"url": "https://transport.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}))
	for _, transport := range []string{"sse", "stdio"} {
		t.Run(transport, func(t *testing.T) {
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
				"server_name": "absent-dependencies", "transport": transport,
			}))
			proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"transport": transport, "url": nil})
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("absent dependency plan: err=%v diagnostics=%v", err, planned.Diagnostics)
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: state,
				PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || puts.Load() != 0 {
				t.Fatalf("absent dependency apply: err=%v diagnostics=%v puts=%d", err, applied.Diagnostics, puts.Load())
			}
			assertMCPServerFailedUpdateRetainsPriorState(t, schema, state, applied.NewState)
		})
	}
}

func fmtDiagnostics(diagnostics []*tfprotov6.Diagnostic) string {
	var builder strings.Builder
	for _, diagnostic := range diagnostics {
		builder.WriteString(diagnostic.Summary)
		builder.WriteString(diagnostic.Detail)
	}
	return builder.String()
}
