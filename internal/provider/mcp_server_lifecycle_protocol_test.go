package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func mcpServerProtocolCreatePlan(t *testing.T, protocolServer tfprotov6.ProviderServer, schema *tfprotov6.Schema, configValues map[string]interface{}) (*tfprotov6.DynamicValue, *tfprotov6.DynamicValue, *tfprotov6.PlanResourceChangeResponse) {
	t.Helper()
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposedValues := make(map[string]interface{}, len(configValues)+2)
	for key, value := range configValues {
		proposedValues[key] = value
	}
	proposedValues["id"], proposedValues["server_id"] = tftypes.UnknownValue, tftypes.UnknownValue
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: nullState, ProposedNewState: proposed,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	return config, nullState, planned
}

func assertMCPServerIdentityOnlyState(t *testing.T, schema *tfprotov6.Schema, state *tfprotov6.DynamicValue, expectedID string) {
	t.Helper()
	attributes := protocolAttributeMap(t, schema, state)
	for _, field := range []string{"id", "server_id"} {
		var got string
		if err := attributes[field].As(&got); err != nil || got != expectedID {
			t.Fatalf("%s=%q err=%v", field, got, err)
		}
	}
	for _, field := range []string{
		"server_name", "alias", "description", "url", "spec_path", "transport", "spec_version", "auth_type",
		"mcp_access_groups", "command", "args", "env", "mcp_info", "credentials", "allowed_tools", "extra_headers",
		"static_headers", "authorization_url", "token_url", "registration_url", "allow_all_keys", "skip_url_validation",
		"created_at", "created_by",
	} {
		if !attributes[field].IsNull() {
			t.Fatalf("unconfirmed %s was published: %s", field, attributes[field])
		}
	}
}

func assertMCPServerFailedUpdateRetainsPriorState(t *testing.T, schema *tfprotov6.Schema, prior, updated *tfprotov6.DynamicValue) {
	t.Helper()
	if updated == nil {
		return
	}
	priorValue, priorErr := prior.Unmarshal(schema.ValueType())
	updatedValue, updatedErr := updated.Unmarshal(schema.ValueType())
	if priorErr != nil || updatedErr != nil {
		t.Fatalf("decode failed update state: prior=%v updated=%v", priorErr, updatedErr)
	}
	if !updatedValue.IsNull() && !updatedValue.Equal(priorValue) {
		t.Fatalf("failed update published non-prior state: %s", updatedValue)
	}
}

func TestMCPServerMalformedCreateRetainsOnlyConfirmedIdentityProtocol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost && request.URL.Path == "/v1/mcp/server" {
			_, _ = writer.Write([]byte(`{"server_id":"malformed-create","server_name":"unconfirmed","url":"https://unconfirmed.invalid/mcp","created_at":"unconfirmed"}`))
			return
		}
		reads.Add(1)
		http.NotFound(writer, request)
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, map[string]interface{}{
		"server_name": "planned-name", "transport": "http", "url": "https://planned.invalid/mcp",
	})
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: nullState,
		PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || reads.Load() != 1 {
		t.Fatalf("malformed create: err=%v diagnostics=%v reads=%d", err, applied.Diagnostics, reads.Load())
	}
	assertMCPServerIdentityOnlyState(t, schema, applied.NewState, "malformed-create")
}

func TestMCPServerCreateReadbackFailureRetainsOnlyIdentityProtocol(t *testing.T) {
	for _, failure := range []string{"read failure", "endpoint mismatch", "transport mismatch"} {
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodPost {
					_, _ = writer.Write([]byte(`{"server_id":"create-readback","server_name":"server","transport":"http","url":"https://configured.invalid/mcp"}`))
					return
				}
				switch failure {
				case "read failure":
					http.Error(writer, `{"error":"unavailable"}`, http.StatusInternalServerError)
				case "endpoint mismatch":
					_, _ = writer.Write([]byte(`{"server_id":"create-readback","server_name":"server","transport":"http","url":"https://different.invalid/mcp"}`))
				case "transport mismatch":
					_, _ = writer.Write([]byte(`{"server_id":"create-readback","server_name":"server","transport":"sse","url":"https://configured.invalid/mcp"}`))
				}
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_mcp_server"]
			config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, map[string]interface{}{
				"server_name": "server", "transport": "http", "url": "https://configured.invalid/mcp",
			})
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: nullState,
				PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("create failure not surfaced: err=%v diagnostics=%v", err, applied.Diagnostics)
			}
			assertMCPServerIdentityOnlyState(t, schema, applied.NewState, "create-readback")
		})
	}
}

func TestMCPServerUpdateEndpointTransitionsSendExplicitNullProtocol(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		priorURL      interface{}
		priorSpec     interface{}
		desiredURL    interface{}
		desiredSpec   interface{}
		clearedField  string
		retainedField string
		retainedValue string
	}{
		{name: "URL to spec", priorURL: "https://old.invalid/mcp", priorSpec: nil, desiredURL: nil, desiredSpec: "/new/spec.json", clearedField: "url", retainedField: "spec_path", retainedValue: "/new/spec.json"},
		{name: "spec to URL", priorURL: nil, priorSpec: "/old/spec.json", desiredURL: "https://new.invalid/mcp", desiredSpec: nil, clearedField: "spec_path", retainedField: "url", retainedValue: "https://new.invalid/mcp"},
		{name: "clear owned spec while URL remains", priorURL: "https://same.invalid/mcp", priorSpec: "/old/spec.json", desiredURL: "https://same.invalid/mcp", desiredSpec: nil, clearedField: "spec_path", retainedField: "url", retainedValue: "https://same.invalid/mcp"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var putBody map[string]interface{}
			response := map[string]interface{}{"server_id": "transition", "server_name": "transition", "transport": "http", "description": "changed", "mcp_info": map[string]interface{}{}}
			if test.desiredURL != nil {
				response["url"] = test.desiredURL
			}
			if test.desiredSpec != nil {
				response["spec_path"] = test.desiredSpec
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch request.Method {
				case http.MethodPut:
					_ = json.NewDecoder(request.Body).Decode(&putBody)
					_ = json.NewEncoder(writer).Encode(response)
				case http.MethodGet:
					_ = json.NewEncoder(writer).Encode(response)
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_mcp_server"]
			stateValues := map[string]interface{}{
				"id": "transition", "server_id": "transition", "server_name": "transition", "description": "old",
				"transport": "http", "url": test.priorURL, "spec_path": test.priorSpec,
				"auth_type": "none", "spec_version": "2024-11-05",
			}
			state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
			configValues := map[string]interface{}{
				"server_name": "transition", "description": "changed", "transport": "http",
				"url": test.desiredURL, "spec_path": test.desiredSpec,
			}
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
			proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{
				"description": "changed", "url": test.desiredURL, "spec_path": test.desiredSpec,
			})
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: state,
				PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("apply: err=%v diagnostics=%v", err, applied.Diagnostics)
			}
			if value, present := putBody[test.clearedField]; !present || value != nil {
				t.Fatalf("%s payload=%#v, want explicit null; body=%#v", test.clearedField, value, putBody)
			}
			if value, present := putBody[test.retainedField]; !present || value != test.retainedValue {
				t.Fatalf("%s payload=%#v, want %q; body=%#v", test.retainedField, value, test.retainedValue, putBody)
			}

			steady, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: applied.NewState, ProposedNewState: applied.NewState, PriorPrivate: applied.Private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, applied.NewState, steady) != organizationProjectProtocolActionNoOp {
				t.Fatalf("steady plan: err=%v diagnostics=%v action=%s", err, steady.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, applied.NewState, steady))
			}
		})
	}
}

func TestMCPServerUpdateFailuresDoNotPublishPlanProtocol(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"malformed update response", "read failure", "malformed read response"} {
		failure := failure
		t.Run(failure, func(t *testing.T) {
			ctx := context.Background()
			var reads atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodPut {
					if failure == "malformed update response" {
						_, _ = writer.Write([]byte(`{"server_id":"failure"}`))
					} else {
						_, _ = writer.Write([]byte(`{"server_id":"failure","transport":"http","url":"https://configured.invalid/mcp"}`))
					}
					return
				}
				readNumber := reads.Add(1)
				if readNumber == 1 {
					_, _ = writer.Write([]byte(`{"server_id":"failure","server_name":"failure","description":"old","transport":"http","url":"https://configured.invalid/mcp","mcp_info":{}}`))
				} else if failure == "read failure" {
					http.Error(writer, `{"error":"unavailable"}`, http.StatusInternalServerError)
				} else {
					_, _ = writer.Write([]byte(`{"server_id":"failure","description":"changed","url":"https://configured.invalid/mcp","mcp_info":{}}`))
				}
			}))
			defer server.Close()

			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_mcp_server"]
			stateValues := map[string]interface{}{
				"id": "failure", "server_id": "failure", "server_name": "failure", "description": "old",
				"transport": "http", "url": "https://configured.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
			}
			state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
			configValues := map[string]interface{}{
				"server_name": "failure", "description": "changed", "transport": "http", "url": "https://configured.invalid/mcp",
			}
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
			proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"description": "changed"})
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "litellm_mcp_server", Config: config, PriorState: state,
				PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("failed update: err=%v diagnostics=%v", err, applied.Diagnostics)
			}
			if failure == "malformed update response" && reads.Load() != 1 {
				t.Fatalf("malformed update response triggered %d reads", reads.Load())
			}
			assertMCPServerFailedUpdateRetainsPriorState(t, schema, state, applied.NewState)
		})
	}
}

func TestMCPServerNoOpPlanDoesNotUpdateProtocol(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var puts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPut {
			puts.Add(1)
		}
		_, _ = writer.Write([]byte(`{"server_id":"no-op","server_name":"no-op","transport":"http","url":"https://configured.invalid/mcp"}`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	values := map[string]interface{}{
		"id": "no-op", "server_id": "no-op", "server_name": "no-op", "transport": "http",
		"url": "https://configured.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
	}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"server_name": "no-op", "transport": "http", "url": "https://configured.invalid/mcp",
	}))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: state,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, state, planned) != organizationProjectProtocolActionNoOp {
		t.Fatalf("no-op plan: err=%v diagnostics=%v action=%s", err, planned.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, state, planned))
	}
	if puts.Load() != 0 {
		t.Fatalf("no-op planning issued %d updates", puts.Load())
	}
}

func TestMCPServerCreateAndUpdateReadbackExposeOwnedEndpointOmissionProtocol(t *testing.T) {
	t.Parallel()
	t.Run("create", func(t *testing.T) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.Method {
			case http.MethodPost:
				_, _ = writer.Write([]byte(`{"server_id":"create-omission","server_name":"create","transport":"http","url":"https://configured.invalid/mcp"}`))
			case http.MethodGet:
				_, _ = writer.Write([]byte(`{"server_id":"create-omission","server_name":"create","transport":"http","spec_path":"/remote/unowned.json"}`))
			default:
				http.NotFound(writer, request)
			}
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		schema := schemas.ResourceSchemas["litellm_mcp_server"]
		config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, map[string]interface{}{
			"server_name": "create", "transport": "http", "url": "https://configured.invalid/mcp",
		})
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
			TypeName: "litellm_mcp_server", Config: config, PriorState: nullState,
			PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
		})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
			t.Fatalf("create omission was not surfaced: err=%v diagnostics=%v", err, applied.Diagnostics)
		}
		assertMCPServerIdentityOnlyState(t, schema, applied.NewState, "create-omission")
	})

	t.Run("update", func(t *testing.T) {
		ctx := context.Background()
		var puts atomic.Int64
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.Method {
			case http.MethodPut:
				puts.Add(1)
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{})
			case http.MethodGet:
				_, _ = writer.Write([]byte(`{"server_id":"update-omission","server_name":"update","description":"changed","transport":"http","spec_path":"/remote/unowned.json","mcp_info":{}}`))
			default:
				http.NotFound(writer, request)
			}
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		schema := schemas.ResourceSchemas["litellm_mcp_server"]
		stateValues := map[string]interface{}{
			"id": "update-omission", "server_id": "update-omission", "server_name": "update", "description": "old",
			"transport": "http", "url": "https://configured.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
		}
		state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
		configValues := map[string]interface{}{
			"server_name": "update", "description": "changed", "transport": "http", "url": "https://configured.invalid/mcp",
		}
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"description": "changed"})
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
			TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed,
		})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("update plan: err=%v diagnostics=%v", err, planned.Diagnostics)
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
			TypeName: "litellm_mcp_server", Config: config, PriorState: state,
			PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
		})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || puts.Load() != 1 {
			t.Fatalf("update omission was not surfaced: err=%v diagnostics=%v puts=%d", err, applied.Diagnostics, puts.Load())
		}
		assertMCPServerFailedUpdateRetainsPriorState(t, schema, state, applied.NewState)
	})

	t.Run("clear not persisted", func(t *testing.T) {
		ctx := context.Background()
		var putBody map[string]interface{}
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			remote := map[string]interface{}{
				"server_id": "clear-failure", "server_name": "clear", "transport": "http",
				"url": "https://old.invalid/mcp", "spec_path": "/new/spec.json", "mcp_info": map[string]interface{}{},
			}
			if request.Method == http.MethodPut {
				_ = json.NewDecoder(request.Body).Decode(&putBody)
			}
			_ = json.NewEncoder(writer).Encode(remote)
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		schema := schemas.ResourceSchemas["litellm_mcp_server"]
		state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
			"id": "clear-failure", "server_id": "clear-failure", "server_name": "clear", "transport": "http",
			"url": "https://old.invalid/mcp", "auth_type": "none", "spec_version": "2024-11-05",
		}))
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
			"server_name": "clear", "transport": "http", "spec_path": "/new/spec.json",
		}))
		proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"url": nil, "spec_path": "/new/spec.json"})
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_mcp_server", Config: config, PriorState: state, ProposedNewState: proposed})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
			TypeName: "litellm_mcp_server", Config: config, PriorState: state,
			PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
		})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
			t.Fatalf("non-convergent clear was not surfaced: err=%v diagnostics=%v", err, applied.Diagnostics)
		}
		if value, present := putBody["url"]; !present || value != nil {
			t.Fatalf("clear payload did not contain url=null: %#v", putBody)
		}
		assertMCPServerFailedUpdateRetainsPriorState(t, schema, state, applied.NewState)
	})

	t.Run("transport mismatch", func(t *testing.T) {
		ctx := context.Background()
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			if request.Method == http.MethodPost {
				_, _ = writer.Write([]byte(`{"server_id":"transport-mismatch","server_name":"transport","transport":"http","url":"https://configured.invalid/mcp"}`))
				return
			}
			_, _ = writer.Write([]byte(`{"server_id":"transport-mismatch","server_name":"transport","transport":"sse","url":"https://configured.invalid/mcp"}`))
		}))
		defer server.Close()
		protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
		schema := schemas.ResourceSchemas["litellm_mcp_server"]
		config, nullState, planned := mcpServerProtocolCreatePlan(t, protocolServer, schema, map[string]interface{}{
			"server_name": "transport", "transport": "http", "url": "https://configured.invalid/mcp",
		})
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
			TypeName: "litellm_mcp_server", Config: config, PriorState: nullState,
			PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
		})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
			t.Fatalf("transport mismatch was not surfaced: err=%v diagnostics=%v", err, applied.Diagnostics)
		}
		assertMCPServerIdentityOnlyState(t, schema, applied.NewState, "transport-mismatch")
	})
}
