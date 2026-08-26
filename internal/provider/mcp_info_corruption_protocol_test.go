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

func TestMCPServerPrivateCorruptionFailsClosedAcrossLifecycleProtocol(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"server_id":"private-sensitive-id","server_name":"top-level","url":"https://mcp.example.test","transport":"http"}`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_mcp_server"
	const sensitiveID = "private-sensitive-id"
	const sensitiveValue = "private-sensitive-value"
	schema := schemas.ResourceSchemas[typeName]
	oldInfo := mcpInfoProtocolValue(t, schema, map[string]interface{}{"description": "old"}, nil)
	newInfo := mcpInfoProtocolValue(t, schema, map[string]interface{}{"description": sensitiveValue}, nil)
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": sensitiveID, "server_id": sensitiveID, "server_name": "top-level", "url": "https://mcp.example.test", "transport": "http", "mcp_info": oldInfo,
	}))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"server_name": "top-level", "url": "https://mcp.example.test", "transport": "http", "mcp_info": newInfo,
	}))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"mcp_info": newInfo})
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))

	base := map[string][]byte{
		mcpInfoOwnershipVersionKey:      []byte(mcpInfoOwnershipVersion),
		mcpInfoTerraformOwnedPrivateKey: []byte(`[]`),
		mcpInfoAPIOwnedPrivateKey:       []byte(`[]`),
	}
	corruptions := map[string]func(map[string][]byte){
		"key without version":   func(values map[string][]byte) { delete(values, mcpInfoOwnershipVersionKey) },
		"missing committed key": func(values map[string][]byte) { delete(values, mcpInfoAPIOwnedPrivateKey) },
		"unsupported version":   func(values map[string][]byte) { values[mcpInfoOwnershipVersionKey] = []byte("2") },
		"null array":            func(values map[string][]byte) { values[mcpInfoTerraformOwnedPrivateKey] = []byte("null") },
		"duplicate": func(values map[string][]byte) {
			values[mcpInfoTerraformOwnedPrivateKey] = []byte(`["mcp_info.description","mcp_info.description"]`)
		},
		"unsorted": func(values map[string][]byte) {
			values[mcpInfoTerraformOwnedPrivateKey] = []byte(`["mcp_info.logo_url","mcp_info.description"]`)
		},
		"unknown": func(values map[string][]byte) {
			values[mcpInfoTerraformOwnedPrivateKey] = []byte(`["mcp_info.private-sensitive-value"]`)
		},
		"overlap": func(values map[string][]byte) {
			values[mcpInfoTerraformOwnedPrivateKey] = []byte(`["mcp_info.mcp_server_cost_info.default_cost_per_query"]`)
			values[mcpInfoAPIOwnedPrivateKey] = []byte(`["mcp_info.mcp_server_cost_info.default_cost_per_query"]`)
		},
		"one pending key": func(values map[string][]byte) { values[mcpInfoPendingTerraformKey] = []byte(`[]`) },
		"malformed pending": func(values map[string][]byte) {
			values[mcpInfoPendingTerraformKey] = []byte(`[ ]`)
			values[mcpInfoPendingAPIKey] = []byte(`[]`)
		},
	}

	assertFailure := func(t *testing.T, diagnostics []*tfprotov6.Diagnostic, gotState *tfprotov6.DynamicValue, gotPrivate []byte, private []byte, before int64) {
		t.Helper()
		if !accessGroupProtocolDiagnosticsHaveError(diagnostics) || requests.Load() != before {
			t.Fatalf("corruption did not fail closed: diagnostics=%s requests=%d", agentProtocolDiagnosticsText(diagnostics), requests.Load()-before)
		}
		text := agentProtocolDiagnosticsText(diagnostics)
		if strings.Contains(text, sensitiveID) || strings.Contains(text, sensitiveValue) || strings.Contains(text, "private-sensitive-value") {
			t.Fatal("private corruption diagnostic exposed a value or identifier")
		}
		priorValue, priorErr := state.Unmarshal(schema.ValueType())
		gotValue, gotErr := gotState.Unmarshal(schema.ValueType())
		if priorErr != nil || gotErr != nil || !priorValue.Equal(gotValue) {
			t.Fatal("private corruption changed public state")
		}
		if string(gotPrivate) != string(private) {
			t.Fatal("private corruption changed private state")
		}
	}

	for name, corrupt := range corruptions {
		t.Run(name, func(t *testing.T) {
			values := make(map[string][]byte, len(base))
			for key, value := range base {
				values[key] = append([]byte(nil), value...)
			}
			corrupt(values)
			private, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}

			before := requests.Load()
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed, PriorPrivate: private})
			if err != nil {
				t.Fatal(err)
			}
			assertFailure(t, planned.Diagnostics, planned.PlannedState, planned.PlannedPrivate, private, before)

			before = requests.Load()
			destroyPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: nullState, PriorState: state, ProposedNewState: nullState, PriorPrivate: private})
			if err != nil {
				t.Fatal(err)
			}
			assertFailure(t, destroyPlan.Diagnostics, destroyPlan.PlannedState, destroyPlan.PlannedPrivate, private, before)

			before = requests.Load()
			read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: state, Private: private})
			if err != nil {
				t.Fatal(err)
			}
			assertFailure(t, read.Diagnostics, read.NewState, read.Private, private, before)

			before = requests.Load()
			updated, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, PlannedState: proposed, PlannedPrivate: private})
			if err != nil {
				t.Fatal(err)
			}
			assertFailure(t, updated.Diagnostics, updated.NewState, updated.Private, private, before)

			before = requests.Load()
			destroyed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: nullState, PriorState: state, PlannedState: nullState, PlannedPrivate: private})
			if err != nil {
				t.Fatal(err)
			}
			assertFailure(t, destroyed.Diagnostics, destroyed.NewState, destroyed.Private, private, before)
		})
	}
}
