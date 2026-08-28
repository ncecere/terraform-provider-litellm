package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func mcpToolsetProtocolToolType() tftypes.Type {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"server_id": tftypes.String,
		"tool_name": tftypes.String,
	}}
}

func mcpToolsetProtocolValue(t *testing.T, schema *tfprotov6.Schema, toolsetID interface{}, name, description interface{}, tools []tftypes.Value) tftypes.Value {
	t.Helper()
	if tools == nil {
		tools = []tftypes.Value{}
	}
	return tftypes.NewValue(schema.ValueType(), map[string]tftypes.Value{
		"toolset_id":   tftypes.NewValue(tftypes.String, toolsetID),
		"toolset_name": tftypes.NewValue(tftypes.String, name),
		"description":  tftypes.NewValue(tftypes.String, description),
		"tools":        tftypes.NewValue(tftypes.Set{ElementType: mcpToolsetProtocolToolType()}, tools),
	})
}

// shortenMCPToolsetRecoveryDelay must only be called from tests that do not
// run in parallel: provider-constructed resources copy the base delay at
// construction time inside the calling test.
func shortenMCPToolsetRecoveryDelay(t *testing.T) {
	t.Helper()
	prior := mcpToolsetRecoveryBaseDelay
	mcpToolsetRecoveryBaseDelay = time.Millisecond
	t.Cleanup(func() { mcpToolsetRecoveryBaseDelay = prior })
}

func mcpToolsetProtocolCreate(t *testing.T, ctx context.Context, apiBase string) (*tfprotov6.ApplyResourceChangeResponse, *tfprotov6.Schema) {
	t.Helper()
	applied, schema, _ := mcpToolsetProtocolCreateWithServer(t, ctx, apiBase)
	return applied, schema
}

func mcpToolsetProtocolCreateWithServer(t *testing.T, ctx context.Context, apiBase string) (*tfprotov6.ApplyResourceChangeResponse, *tfprotov6.Schema, tfprotov6.ProviderServer) {
	t.Helper()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, apiBase)
	schema := schemas.ResourceSchemas["litellm_mcp_toolset"]
	config := accessGroupProtocolDynamicValue(t, schema, mcpToolsetProtocolValue(t, schema, nil, "incident-response", nil, nil))
	proposed := accessGroupProtocolDynamicValue(t, schema, mcpToolsetProtocolValue(t, schema, tftypes.UnknownValue, "incident-response", nil, nil))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_toolset", Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_mcp_toolset", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil {
		t.Fatalf("apply err=%v", err)
	}
	return applied, schema, protocolServer
}

func TestMCPToolsetProtocolAcceptedMalformedCreateRetainsNameBoundPartialState(t *testing.T) {
	shortenMCPToolsetRecoveryDelay(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/mcp/toolset":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`not json`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset":
			// Pinned list_mcp_toolsets converts database failures to an empty
			// list, so the provider must never treat this as absence.
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	applied, schema := mcpToolsetProtocolCreate(t, ctx, server.URL)
	if !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatal("unconfirmed accepted create did not error")
	}
	if text := agentProtocolDiagnosticsText(applied.Diagnostics); !strings.Contains(text, "name-bound partial state was retained") {
		t.Fatalf("diagnostic does not describe partial-state retention: %s", text)
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	if !attributes["toolset_id"].IsNull() {
		t.Fatalf("unconfirmed toolset_id persisted: %s", attributes["toolset_id"])
	}
	var name string
	if err := attributes["toolset_name"].As(&name); err != nil || name != "incident-response" {
		t.Fatalf("name-bound state lost the toolset name: %q err=%v", name, err)
	}
}

func TestMCPToolsetProtocolAcceptedInterruptedCreateRecoversByNameWithDirectConfirmation(t *testing.T) {
	shortenMCPToolsetRecoveryDelay(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/mcp/toolset":
			// Advertise more body than is sent so the accepted response dies
			// mid-read, the canceled/interrupted transactional shape.
			w.Header().Set("Content-Length", "512")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"toolset_id":"toolset-`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset":
			_, _ = w.Write([]byte(`[{"toolset_name":"other","toolset_id":"toolset-other"},{"toolset_name":"incident-response","toolset_id":"toolset-recovered"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset/toolset-recovered":
			_ = json.NewEncoder(w).Encode(mcpToolsetResponse{ToolsetID: "toolset-recovered", ToolsetName: "incident-response"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	applied, schema := mcpToolsetProtocolCreate(t, ctx, server.URL)
	if accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("recovered create errored: %s", agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	var toolsetID string
	if err := attributes["toolset_id"].As(&toolsetID); err != nil || toolsetID != "toolset-recovered" {
		t.Fatalf("toolset_id=%q err=%v, want recovered identity", toolsetID, err)
	}
}

func TestMCPToolsetProtocolRejectedCreateLeavesNoState(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":{"error":"A toolset named 'incident-response' already exists."}}`, http.StatusConflict)
	}))
	defer server.Close()

	applied, schema := mcpToolsetProtocolCreate(t, ctx, server.URL)
	if !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatal("rejected create did not error")
	}
	state, err := applied.NewState.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	if !state.IsNull() {
		t.Fatalf("rejected create persisted state: %s", state)
	}
}

func TestMCPToolsetProtocolReadRepairsAcceptedNameBoundPartialState(t *testing.T) {
	shortenMCPToolsetRecoveryDelay(t)
	ctx := context.Background()
	var healthy atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/mcp/toolset":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`not json`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset":
			if !healthy.Load() {
				http.Error(w, `{"error":"transient"}`, http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte(`[{"toolset_name":"incident-response","toolset_id":"toolset-repaired"}]`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset/toolset-repaired":
			_ = json.NewEncoder(w).Encode(mcpToolsetResponse{ToolsetID: "toolset-repaired", ToolsetName: "incident-response"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	applied, schema, protocolServer := mcpToolsetProtocolCreateWithServer(t, ctx, server.URL)
	if !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatal("unconfirmed accepted create did not error")
	}
	if !protocolPrivateHasKey(t, applied.Private, mcpToolsetAcceptedCreatePrivateKey) {
		t.Fatalf("accepted-create marker missing from private state: %s", applied.Private)
	}

	healthy.Store(true)
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_toolset", CurrentState: applied.NewState, Private: applied.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	attributes := protocolAttributeMap(t, schema, read.NewState)
	var toolsetID string
	if err := attributes["toolset_id"].As(&toolsetID); err != nil || toolsetID != "toolset-repaired" {
		t.Fatalf("toolset_id=%q err=%v, want repaired identity", toolsetID, err)
	}
	if protocolPrivateHasKey(t, read.Private, mcpToolsetAcceptedCreatePrivateKey) {
		t.Fatalf("recovered read retained the accepted-create marker: %s", read.Private)
	}
}

func TestMCPToolsetProtocolDispatchedCreateWithoutStatusRequiresReconciliation(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/v1/mcp/toolset" {
			// Drop the connection before any status line: the mutation was
			// dispatched but its outcome was never proven.
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("test server does not support hijacking")
			}
			connection, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("hijack: %v", err)
			}
			_ = connection.Close()
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	applied, schema, protocolServer := mcpToolsetProtocolCreateWithServer(t, ctx, server.URL)
	if !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatal("uncertain dispatched create did not error")
	}
	if text := agentProtocolDiagnosticsText(applied.Diagnostics); !strings.Contains(text, "may or may not exist") {
		t.Fatalf("diagnostic does not describe the uncertain outcome: %s", text)
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	if !attributes["toolset_id"].IsNull() {
		t.Fatalf("uncertain create persisted an identity: %s", attributes["toolset_id"])
	}
	var name string
	if err := attributes["toolset_name"].As(&name); err != nil || name != "incident-response" {
		t.Fatalf("uncertain create lost the blocking name-bound state: %q err=%v", name, err)
	}
	if protocolPrivateHasKey(t, applied.Private, mcpToolsetAcceptedCreatePrivateKey) {
		t.Fatalf("uncertain create wrote the accepted-create marker: %s", applied.Private)
	}

	// Refresh must not adopt by name: an exact-name match could be a
	// pre-existing toolset the interrupted create was rejected against.
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_toolset", CurrentState: applied.NewState, Private: applied.Private})
	if err != nil {
		t.Fatal(err)
	}
	if !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatal("uncertain identity read did not instruct reconciliation")
	}
	if text := agentProtocolDiagnosticsText(read.Diagnostics); !strings.Contains(text, "import it by ID") {
		t.Fatalf("read diagnostic does not instruct reconciliation: %s", text)
	}
}

func TestMCPToolsetProtocolUnrepairablePartialStateFailsWithoutRemoval(t *testing.T) {
	shortenMCPToolsetRecoveryDelay(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_toolset"]
	prior := accessGroupProtocolDynamicValue(t, schema, mcpToolsetProtocolValue(t, schema, nil, "incident-response", nil, nil))
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_mcp_toolset", CurrentState: prior})
	if err != nil {
		t.Fatal(err)
	}
	if !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatal("unrepairable partial state did not error")
	}
	state, err := read.NewState.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	if state.IsNull() {
		t.Fatal("empty collection was treated as absence authority and removed the partial state")
	}
}

func TestMCPToolsetProtocolAcceptedMalformedUpdateConfirmsThroughDirectReadback(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v1/mcp/toolset":
			_, _ = w.Write([]byte(`not json`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset/toolset-stable":
			_ = json.NewEncoder(w).Encode(mcpToolsetResponse{ToolsetID: "toolset-stable", ToolsetName: "after"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_toolset"]
	prior := accessGroupProtocolDynamicValue(t, schema, mcpToolsetProtocolValue(t, schema, "toolset-stable", "before", nil, nil))
	config := accessGroupProtocolDynamicValue(t, schema, mcpToolsetProtocolValue(t, schema, nil, "after", nil, nil))
	proposed := accessGroupProtocolDynamicValue(t, schema, mcpToolsetProtocolValue(t, schema, "toolset-stable", "after", nil, nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_toolset", Config: config, PriorState: prior, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_mcp_toolset", Config: config, PriorState: prior, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("apply err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	var name string
	if err := attributes["toolset_name"].As(&name); err != nil || name != "after" {
		t.Fatalf("toolset_name=%q err=%v, want direct-readback value", name, err)
	}
}

// A decodable accepted create response that does not match the requested
// definition must not enter state; the provider recovers through exact-name
// evidence plus direct confirmation of the requested definition.
func TestMCPToolsetProtocolMismatchedCreateResponseRecoversRequestedRow(t *testing.T) {
	shortenMCPToolsetRecoveryDelay(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/mcp/toolset":
			// Decodable body describing a different row.
			_ = json.NewEncoder(w).Encode(mcpToolsetResponse{ToolsetID: "toolset-other", ToolsetName: "other-name"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset":
			_ = json.NewEncoder(w).Encode([]mcpToolsetResponse{
				{ToolsetID: "toolset-other", ToolsetName: "other-name"},
				{ToolsetID: "toolset-real", ToolsetName: "incident-response"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset/toolset-real":
			_ = json.NewEncoder(w).Encode(mcpToolsetResponse{ToolsetID: "toolset-real", ToolsetName: "incident-response"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	applied, schema := mcpToolsetProtocolCreate(t, ctx, server.URL)
	if accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("apply diagnostics=%s", agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	var toolsetID, name string
	if err := attributes["toolset_id"].As(&toolsetID); err != nil || toolsetID != "toolset-real" {
		t.Fatalf("toolset_id=%q err=%v, want the recovered requested row", toolsetID, err)
	}
	if err := attributes["toolset_name"].As(&name); err != nil || name != "incident-response" {
		t.Fatalf("toolset_name=%q err=%v, want the requested name", name, err)
	}
}

// An accepted update whose direct readback describes a different definition
// must error and retain prior state instead of publishing either value.
func TestMCPToolsetProtocolUpdateReadbackDefinitionMismatchRetainsPriorState(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/v1/mcp/toolset":
			_ = json.NewEncoder(w).Encode(mcpToolsetResponse{ToolsetID: "toolset-stable", ToolsetName: "after"})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset/toolset-stable":
			// Authoritative readback does not reflect the planned definition.
			_ = json.NewEncoder(w).Encode(mcpToolsetResponse{ToolsetID: "toolset-stable", ToolsetName: "someone-else"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_toolset"]
	prior := accessGroupProtocolDynamicValue(t, schema, mcpToolsetProtocolValue(t, schema, "toolset-stable", "before", nil, nil))
	config := accessGroupProtocolDynamicValue(t, schema, mcpToolsetProtocolValue(t, schema, nil, "after", nil, nil))
	proposed := accessGroupProtocolDynamicValue(t, schema, mcpToolsetProtocolValue(t, schema, "toolset-stable", "after", nil, nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_toolset", Config: config, PriorState: prior, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_mcp_toolset", Config: config, PriorState: prior, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil {
		t.Fatal(err)
	}
	if !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatal("mismatched readback did not error")
	}
	priorValue, _ := prior.Unmarshal(schema.ValueType())
	failedValue, _ := applied.NewState.Unmarshal(schema.ValueType())
	if !priorValue.Equal(failedValue) {
		t.Fatalf("mismatched readback published state\nprior: %s\n  got: %s", priorValue, failedValue)
	}
}
