package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestMCPSingularSafeCredentialProjectionProtocol(t *testing.T) {
	ctx := context.Background()
	const secret = "credential-response-secret"
	var payload atomic.Value
	payload.Store(`{"server_id":"credential","transport":"http","credentials":{"upstream_resource":"https://resource.invalid"}}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(payload.Load().(string)))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.DataSourceSchemas["litellm_mcp_server"]
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"server_id": "credential"}))
	read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_server", Config: config})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("safe credential read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	var upstream string
	if err := protocolAttributeMap(t, schema, read.State)["upstream_resource"].As(&upstream); err != nil || upstream != "https://resource.invalid" {
		t.Fatalf("upstream_resource=%q err=%v", upstream, err)
	}

	for name, malformed := range map[string]string{
		"wrong root":        `{"server_id":"credential","transport":"http","credentials":"` + secret + `"}`,
		"empty object":      `{"server_id":"credential","transport":"http","credentials":{}}`,
		"empty value":       `{"server_id":"credential","transport":"http","credentials":{"upstream_resource":""}}`,
		"wrong value type":  `{"server_id":"credential","transport":"http","credentials":{"upstream_resource":false}}`,
		"unexpected member": `{"server_id":"credential","transport":"http","credentials":{"upstream_resource":"safe","client_secret":"` + secret + `"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			payload.Store(malformed)
			failed, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_server", Config: config})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) {
				t.Fatalf("malformed credentials accepted: err=%v diagnostics=%v", err, failed.Diagnostics)
			}
			if diagnostics := agentProtocolDiagnosticsText(failed.Diagnostics); strings.Contains(diagnostics, secret) {
				t.Fatal("credential diagnostic exposed response content")
			}
			assertSingularPresenceStateUnchanged(t, schema, config, failed.State)
		})
	}
}

func TestMCPManagerListProjectionSortingAndSingleRequestProtocol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var requests atomic.Int64
	var unexpectedPath atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/v1/mcp/server" {
			unexpectedPath.Store(true)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[
			{"server_id":"z-server","transport":"http"},
			{"server_id":"a-server","server_name":"manager","transport":"stdio","auth_type":"none","mcp_access_groups":["group"],"allowed_tools":["search"],"command":"python3","args":["server.py"],"env":{"MODE":"safe"},"extra_headers":["X-Trace"],"static_headers":{},"authorization_url":"https://auth.invalid/authorize","token_url":"https://auth.invalid/token","registration_url":"https://auth.invalid/register","allow_all_keys":false,"mcp_info":{},"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}
		]`))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.DataSourceSchemas["litellm_mcp_servers"]
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{}))
	read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_servers", Config: config})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("manager list read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	if requests.Load() != 1 || unexpectedPath.Load() {
		t.Fatalf("manager list performed N+1 reads: requests=%d unexpected_path=%t", requests.Load(), unexpectedPath.Load())
	}
	attributes := protocolAttributeMap(t, schema, read.State)
	var stableID string
	if err := attributes["id"].As(&stableID); err != nil || stableID != "mcp_servers" {
		t.Fatalf("list id=%q err=%v", stableID, err)
	}
	var items []tftypes.Value
	if err := attributes["mcp_servers"].As(&items); err != nil || len(items) != 2 {
		t.Fatalf("list items=%d err=%v", len(items), err)
	}
	first := map[string]tftypes.Value{}
	second := map[string]tftypes.Value{}
	if err := items[0].As(&first); err != nil {
		t.Fatal(err)
	}
	if err := items[1].As(&second); err != nil {
		t.Fatal(err)
	}
	var firstID, secondID string
	_ = first["server_id"].As(&firstID)
	_ = second["server_id"].As(&secondID)
	if firstID != "a-server" || secondID != "z-server" {
		t.Fatalf("manager list order = %q, %q", firstID, secondID)
	}
	for _, name := range []string{"mcp_access_groups", "allowed_tools", "args", "env", "extra_headers", "static_headers"} {
		if first[name].IsNull() || !first[name].IsKnown() {
			t.Errorf("manager list common field %s was not known", name)
		}
	}
	for _, name := range []string{"command", "authorization_url", "token_url", "registration_url"} {
		if first[name].IsNull() || !first[name].IsKnown() {
			t.Errorf("manager list common scalar %s was not known", name)
		}
	}
	assertMCPProtocolFalse(t, first["allow_all_keys"])
}

func TestMCPResourceUpdatedAuditLifecycleProtocol(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		ctx := context.Background()
		response := `{"server_id":"audit-create","server_name":"audit_create","alias":"audit_create","transport":"http","auth_type":"none","url":"https://audit.invalid/mcp","updated_at":"2026-09-01T00:00:00Z","updated_by":"creator-id"}`
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			if request.Method != http.MethodPost && request.Method != http.MethodGet {
				http.NotFound(writer, request)
				return
			}
			_, _ = writer.Write([]byte(response))
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		schema := schemas.ResourceSchemas["litellm_mcp_server"]
		config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, map[string]interface{}{
			"server_id": "audit-create", "server_name": "audit_create", "transport": "http", "url": "https://audit.invalid/mcp",
		})
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
			TypeName: "litellm_mcp_server", Config: config, PriorState: nullState,
			PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
		})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
			t.Fatalf("create: err=%v diagnostics=%v", err, applied.Diagnostics)
		}
		attributes := protocolAttributeMap(t, schema, applied.NewState)
		assertMCPProtocolString(t, attributes["updated_at"], "2026-09-01T00:00:00Z")
		assertMCPProtocolString(t, attributes["updated_by"], "creator-id")
	})

	t.Run("update and redacted preserve", func(t *testing.T) {
		private := protocolMCPFieldPrivate(t, emptyMCPFieldOwnership())
		state := map[string]interface{}{
			"id": "audit-update", "server_id": "audit-update", "server_name": "audit_update", "description": "old",
			"transport": "http", "url": "https://audit.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05", "mcp_info_json": "{}",
			"updated_at": "2026-09-01T00:00:00Z", "updated_by": "old-admin",
		}
		config := map[string]interface{}{
			"server_name": "audit_update", "description": "new", "transport": "http", "url": "https://audit.invalid/mcp",
		}
		before := map[string]interface{}{
			"server_id": "audit-update", "server_name": "audit_update", "description": "old", "transport": "http", "auth_type": "none",
			"url": "https://audit.invalid/mcp", "mcp_info": map[string]interface{}{}, "updated_at": "2026-09-01T00:00:00Z", "updated_by": "old-admin",
		}
		after := map[string]interface{}{
			"server_id": "audit-update", "server_name": "audit_update", "description": "new", "transport": "http", "auth_type": "none",
			"url": "https://audit.invalid/mcp", "mcp_info": map[string]interface{}{}, "updated_at": "2026-09-02T00:00:00Z", "updated_by": "new-admin",
		}
		result := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"description": "new"}, before, after, private)
		plannedAttributes := protocolAttributeMap(t, result.schema, result.planned.PlannedState)
		if plannedAttributes["updated_at"].IsKnown() || plannedAttributes["updated_by"].IsKnown() {
			t.Fatal("mutable update audit fields remained pinned in the plan")
		}
		if accessGroupProtocolDiagnosticsHaveError(result.applied.Diagnostics) || result.puts != 1 {
			t.Fatalf("updated audit apply: puts=%d diagnostics=%v", result.puts, result.applied.Diagnostics)
		}
		attributes := protocolAttributeMap(t, result.schema, result.applied.NewState)
		assertMCPProtocolString(t, attributes["updated_at"], "2026-09-02T00:00:00Z")
		assertMCPProtocolString(t, attributes["updated_by"], "new-admin")

		after["updated_at"], after["updated_by"] = nil, nil
		redacted := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"description": "new"}, before, after, private)
		if accessGroupProtocolDiagnosticsHaveError(redacted.applied.Diagnostics) || redacted.puts != 1 {
			t.Fatalf("redacted audit apply: puts=%d diagnostics=%v", redacted.puts, redacted.applied.Diagnostics)
		}
		attributes = protocolAttributeMap(t, redacted.schema, redacted.applied.NewState)
		assertMCPProtocolString(t, attributes["updated_at"], "2026-09-01T00:00:00Z")
		assertMCPProtocolString(t, attributes["updated_by"], "old-admin")

		after["updated_at"] = false
		malformed := runMCPUpdateCompletionProtocol(t, state, config, map[string]interface{}{"description": "new"}, before, after, private)
		if !accessGroupProtocolDiagnosticsHaveError(malformed.applied.Diagnostics) || malformed.puts != 1 {
			t.Fatalf("malformed updated_at was accepted: puts=%d diagnostics=%v", malformed.puts, malformed.applied.Diagnostics)
		}
		assertMCPServerFailedUpdateRetainsPriorState(t, malformed.schema, malformed.state, malformed.applied.NewState)
	})

	t.Run("restricted import null", func(t *testing.T) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"server_id":"audit-import","transport":"http","updated_at":null,"updated_by":null}`))
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		schema := schemas.ResourceSchemas["litellm_mcp_server"]
		imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_mcp_server", ID: "audit-import"})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
			t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
		}
		resourceState := imported.ImportedResources[0]
		read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_server", CurrentState: resourceState.State, Private: resourceState.Private})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
			t.Fatalf("restricted import read: err=%v diagnostics=%v", err, read.Diagnostics)
		}
		attributes := protocolAttributeMap(t, schema, read.NewState)
		if !attributes["updated_at"].IsNull() || !attributes["updated_by"].IsNull() {
			t.Fatal("restricted import did not resolve updated audit fields to typed null")
		}
	})
}

func assertMCPProtocolString(t *testing.T, value tftypes.Value, want string) {
	t.Helper()
	var got string
	if err := value.As(&got); err != nil || got != want {
		t.Fatalf("string value=%q want=%q err=%v", got, want, err)
	}
}

func TestMCPManagerListRejectsAnyCredentialsProtocol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const secret = "list-credential-response-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[{"server_id":"credential-list","transport":"http","credentials":{"upstream_resource":"` + secret + `"}}]`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.DataSourceSchemas["litellm_mcp_servers"]
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{}))
	read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_mcp_servers", Config: config})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("manager list accepted credentials: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	if strings.Contains(agentProtocolDiagnosticsText(read.Diagnostics), secret) {
		t.Fatal("manager list diagnostic exposed credential content")
	}
	assertSingularPresenceStateUnchanged(t, schema, config, read.State)
}
