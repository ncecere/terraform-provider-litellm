package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestSemanticJSONUpdateReadbackFailureProtocol(t *testing.T) {
	for name, test := range map[string]struct {
		typeName   string
		state      map[string]interface{}
		config     map[string]interface{}
		change     map[string]interface{}
		updatePath string
		readPath   string
	}{
		"budget": {
			typeName: "litellm_budget", updatePath: "/budget/update", readPath: "/budget/info",
			state:  map[string]interface{}{"id": "budget", "budget_id": "budget", "model_max_budget": `{"model":{"max_budget":1}}`},
			config: map[string]interface{}{"budget_id": "budget", "model_max_budget": `{"model":{"max_budget":2}}`},
			change: map[string]interface{}{"model_max_budget": `{"model":{"max_budget":2}}`},
		},
		"search": {
			typeName: "litellm_search_tool", updatePath: "/search_tools/search", readPath: "/search_tools/search",
			state:  map[string]interface{}{"id": "search", "search_tool_id": "search", "search_tool_name": "search", "search_provider": "provider", "search_tool_info": `{"value":1}`},
			config: map[string]interface{}{"search_tool_name": "search", "search_provider": "provider", "search_tool_info": `{"value":2}`},
			change: map[string]interface{}{"search_tool_info": `{"value":2}`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			var updates atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.Path == test.updatePath && (request.Method == http.MethodPost || request.Method == http.MethodPut) {
					updates.Add(1)
					_, _ = fmt.Fprint(writer, `{}`)
					return
				}
				if request.URL.Path == test.readPath {
					http.Error(writer, "read unavailable", http.StatusInternalServerError)
					return
				}
				http.NotFound(writer, request)
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas[test.typeName]
			state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, test.state))
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, test.config))
			proposed := organizationProjectProtocolReplace(t, schema, state, test.change)
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: test.typeName, Config: config, PriorState: state, ProposedNewState: proposed})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: test.typeName, Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || updates.Load() != 1 {
				t.Fatalf("apply: err=%v diagnostics=%v updates=%d", err, applied.Diagnostics, updates.Load())
			}
			if applied.NewState != nil {
				field, expected := "model_max_budget", `{"model":{"max_budget":1}}`
				if name == "search" {
					field, expected = "search_tool_info", `{"value":1}`
				}
				var actual string
				if err := protocolAttributeMap(t, schema, applied.NewState)[field].As(&actual); err != nil || actual != expected {
					t.Fatalf("failed update published %s=%q, want prior %q (err=%v)", field, actual, expected, err)
				}
			}
		})
	}
}

func TestSearchToolImportJSONOwnershipReleaseProtocol(t *testing.T) {
	ctx := context.Background()
	var updates atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/search_tools/search":
			_, _ = fmt.Fprint(writer, `{"search_tool_id":"search","search_tool_name":"search","litellm_params":{"search_provider":"provider"},"search_tool_info":{"z":2,"a":1}}`)
		case request.Method == http.MethodPut && request.URL.Path == "/search_tools/search":
			updates.Add(1)
			_, _ = fmt.Fprint(writer, `{}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_search_tool"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "search"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	var info string
	if err := protocolAttributeMap(t, schema, read.NewState)["search_tool_info"].As(&info); err != nil || info != `{"a":1,"z":2}` {
		t.Fatalf("imported info=%q err=%v", info, err)
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"search_tool_name": "search", "search_provider": "provider"}))
	proposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"search_tool_info": nil})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: read.NewState, ProposedNewState: proposed, PriorPrivate: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: read.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || updates.Load() != 1 {
		t.Fatalf("apply: err=%v diagnostics=%v updates=%d", err, applied.Diagnostics, updates.Load())
	}
	if !protocolAttributeMap(t, schema, applied.NewState)["search_tool_info"].IsNull() {
		t.Fatal("omitted search_tool_info remained owned")
	}
}

func TestSemanticJSONCreateIdentityAndReadbackProtocol(t *testing.T) {
	t.Run("generated budget missing identity", func(t *testing.T) {
		ctx := context.Background()
		var reads atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			if request.URL.Path == "/budget/new" {
				_, _ = fmt.Fprint(writer, `{}`)
				return
			}
			if request.URL.Path == "/budget/info" {
				reads.Add(1)
			}
			http.NotFound(writer, request)
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		typeName := "litellm_budget"
		schema := schemas.ResourceSchemas[typeName]
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{}))
		proposed := organizationProjectProtocolValue(t, schema, map[string]interface{}{"id": tftypes.UnknownValue, "budget_id": tftypes.UnknownValue})
		nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: accessGroupProtocolDynamicValue(t, schema, proposed)})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || reads.Load() != 0 {
			t.Fatalf("apply: err=%v diagnostics=%v reads=%d", err, applied.Diagnostics, reads.Load())
		}
	})

	t.Run("configured budget identity recovers missing echo", func(t *testing.T) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/budget/new":
				_, _ = fmt.Fprint(writer, `{}`)
			case "/budget/info":
				_, _ = fmt.Fprint(writer, `[{"budget_id":"fixed","model_max_budget":{}}]`)
			default:
				http.NotFound(writer, request)
			}
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		typeName := "litellm_budget"
		schema := schemas.ResourceSchemas[typeName]
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"budget_id": "fixed"}))
		proposed := organizationProjectProtocolValue(t, schema, map[string]interface{}{"id": tftypes.UnknownValue, "budget_id": "fixed"})
		nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: accessGroupProtocolDynamicValue(t, schema, proposed)})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
			t.Fatalf("apply: err=%v diagnostics=%v", err, applied.Diagnostics)
		}
		var id string
		if err := protocolAttributeMap(t, schema, applied.NewState)["id"].As(&id); err != nil || id != "fixed" {
			t.Fatalf("id=%q err=%v", id, err)
		}
	})

	t.Run("budget readback failure retains only identity", func(t *testing.T) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			if request.URL.Path == "/budget/new" {
				_, _ = fmt.Fprint(writer, `{"budget_id":"budget-id"}`)
				return
			}
			http.Error(writer, "read unavailable", http.StatusInternalServerError)
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		typeName := "litellm_budget"
		schema := schemas.ResourceSchemas[typeName]
		configValues := map[string]interface{}{"max_budget": 10.0, "model_max_budget": `{"model":{"max_budget":2}}`}
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		proposedValues := map[string]interface{}{"id": tftypes.UnknownValue, "budget_id": tftypes.UnknownValue, "max_budget": 10.0, "model_max_budget": `{"model":{"max_budget":2}}`}
		proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
		nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
			t.Fatalf("apply: err=%v diagnostics=%v", err, applied.Diagnostics)
		}
		attributes := protocolAttributeMap(t, schema, applied.NewState)
		var id string
		if err := attributes["id"].As(&id); err != nil || id != "budget-id" {
			t.Fatalf("partial id=%q err=%v", id, err)
		}
		for _, field := range []string{"max_budget", "soft_budget", "max_parallel_requests", "tpm_limit", "rpm_limit", "budget_duration", "model_max_budget"} {
			if !attributes[field].IsNull() {
				t.Fatalf("unconfirmed %s was published: %s", field, attributes[field])
			}
		}
	})

	t.Run("search missing identity skips read", func(t *testing.T) {
		ctx := context.Background()
		var reads atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			if request.Method == http.MethodPost && request.URL.Path == "/search_tools" {
				_, _ = fmt.Fprint(writer, `{}`)
				return
			}
			if request.Method == http.MethodGet {
				reads.Add(1)
			}
			http.NotFound(writer, request)
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		typeName := "litellm_search_tool"
		schema := schemas.ResourceSchemas[typeName]
		configValues := map[string]interface{}{"search_tool_name": "search", "search_provider": "provider", "search_tool_info": `{"a":1}`}
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		proposedValues := map[string]interface{}{"search_tool_name": "search", "search_provider": "provider", "search_tool_info": `{"a":1}`, "id": tftypes.UnknownValue, "search_tool_id": tftypes.UnknownValue}
		proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
		nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || reads.Load() != 0 {
			t.Fatalf("apply: err=%v diagnostics=%v reads=%d", err, applied.Diagnostics, reads.Load())
		}
	})

	t.Run("search readback failure retains recovered identity", func(t *testing.T) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			if request.Method == http.MethodPost && request.URL.Path == "/search_tools" {
				_, _ = fmt.Fprint(writer, `{"search_tool_id":"search-id"}`)
				return
			}
			http.Error(writer, "read unavailable", http.StatusInternalServerError)
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		typeName := "litellm_search_tool"
		schema := schemas.ResourceSchemas[typeName]
		configValues := map[string]interface{}{"search_tool_name": "search", "search_provider": "provider"}
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		proposedValues := map[string]interface{}{"search_tool_name": "search", "search_provider": "provider", "id": tftypes.UnknownValue, "search_tool_id": tftypes.UnknownValue}
		proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
		nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
			t.Fatalf("apply: err=%v diagnostics=%v", err, applied.Diagnostics)
		}
		attributes := protocolAttributeMap(t, schema, applied.NewState)
		var id string
		if err := attributes["id"].As(&id); err != nil || id != "search-id" {
			t.Fatalf("partial id=%q err=%v", id, err)
		}
		for _, field := range []string{"search_tool_name", "search_provider", "search_tool_info", "api_base", "timeout", "max_retries"} {
			if !attributes[field].IsNull() {
				t.Fatalf("unconfirmed %s was published: %s", field, attributes[field])
			}
		}
	})
}
