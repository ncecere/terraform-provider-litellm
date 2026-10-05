package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestPromptDeleteUsesVersionHistoryAcrossWorkers covers LiteLLM 1.104.0 on a
// multi-worker proxy: after a successful DELETE, another worker's in-memory
// registry can still answer the info route, while the database-only versions
// route already reports the scoped history absent.
func TestPromptDeleteUsesVersionHistoryAcrossWorkers(t *testing.T) {
	for _, test := range []struct {
		name          string
		versionsBody  string
		versionsCode  int
		wantDestroyed bool
	}{
		{"history gone", `{"detail":"not found"}`, http.StatusNotFound, true},
		{"history empty", `{"prompts":[]}`, http.StatusOK, true},
		{"history remains", `{"prompts":[{"prompt_id":"managed","version":1}]}`, http.StatusOK, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch {
				case request.Method == http.MethodDelete && request.URL.RequestURI() == "/prompts/managed?environment=production":
					_, _ = fmt.Fprint(writer, `{}`)
				case request.Method == http.MethodGet && request.URL.Path == "/prompts/managed/versions":
					writer.WriteHeader(test.versionsCode)
					_, _ = fmt.Fprint(writer, test.versionsBody)
				case request.Method == http.MethodGet && request.URL.Path == "/prompts/managed":
					// A stale worker still serves the prompt from its registry.
					_, _ = fmt.Fprint(writer, `{"prompt_spec":{"prompt_id":"managed","environment":"production","version":1,"litellm_params":{"prompt_integration":"dotprompt"},"prompt_info":{"prompt_type":"db","environment":"production"}}}`)
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_prompt"]
			state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
				"id": "managed", "prompt_id": "managed", "environment": "production", "version": int64(1), "prompt_integration": "dotprompt", "prompt_type": "db",
			}))
			nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_prompt", Config: nullState, PriorState: state, ProposedNewState: nullState})
			if err != nil {
				t.Fatal(err)
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_prompt", Config: nullState, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil {
				t.Fatal(err)
			}
			if destroyed := !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics); destroyed != test.wantDestroyed {
				t.Fatalf("destroyed=%t want %t diagnostics=%s", destroyed, test.wantDestroyed, agentProtocolDiagnosticsText(applied.Diagnostics))
			}
		})
	}
}

// TestPromptDeleteRecoveryThenStaleWorkerUsesVersionHistory reproduces the
// observed dev sequence: the first DELETE reaches a worker without the
// registry key (404), the PATCH-and-retry recovery succeeds, and the final
// singular read lands on a worker whose registry still holds the prompt.
func TestPromptDeleteRecoveryThenStaleWorkerUsesVersionHistory(t *testing.T) {
	ctx := context.Background()
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodDelete:
			deletes++
			if deletes == 1 {
				http.Error(writer, `{"detail":"registry key missing"}`, http.StatusNotFound)
				return
			}
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodPatch:
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodGet && request.URL.Path == "/prompts/managed/versions":
			if deletes >= 2 {
				http.Error(writer, `{"detail":"not found"}`, http.StatusNotFound)
				return
			}
			_, _ = fmt.Fprint(writer, `{"prompts":[{"prompt_id":"managed","version":1}]}`)
		case request.Method == http.MethodGet && request.URL.Path == "/prompts/managed":
			_, _ = fmt.Fprint(writer, `{"prompt_spec":{"prompt_id":"managed","environment":"production","version":1,"litellm_params":{"prompt_integration":"dotprompt"},"prompt_info":{"prompt_type":"db","environment":"production"}}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_prompt"]
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id": "managed", "prompt_id": "managed", "environment": "production", "version": int64(1), "prompt_integration": "dotprompt", "prompt_type": "db",
	}))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_prompt", Config: nullState, PriorState: state, ProposedNewState: nullState})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_prompt", Config: nullState, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || deletes != 2 {
		t.Fatalf("err=%v deletes=%d diagnostics=%s", err, deletes, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
}

func TestPromptScopedExistsIgnoresStaleRegistryForDatabasePrompts(t *testing.T) {
	for _, test := range []struct {
		name, promptType string
		versionsCode     int
		want             bool
	}{
		{"stale database prompt", "db", http.StatusNotFound, false},
		{"live database prompt", "db", http.StatusOK, true},
		{"config prompt", "config", http.StatusNotFound, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.Path == "/prompts/managed/versions" {
					writer.WriteHeader(test.versionsCode)
					if test.versionsCode == http.StatusOK {
						_, _ = fmt.Fprint(writer, `{"prompts":[{"prompt_id":"managed","version":1}]}`)
					} else {
						_, _ = fmt.Fprint(writer, `{"detail":"not found"}`)
					}
					return
				}
				_, _ = fmt.Fprintf(writer, `{"prompt_spec":{"prompt_id":"managed","environment":"production","version":1,"litellm_params":{"prompt_integration":"dotprompt"},"prompt_info":{"prompt_type":%q,"environment":"production"}}}`, test.promptType)
			}))
			defer server.Close()
			client := &Client{APIBase: server.URL, APIKey: "sk-test", HTTPClient: server.Client()}
			exists, err := promptScopedExists(context.Background(), client, "managed", "production")
			if err != nil || exists != test.want {
				t.Fatalf("exists=%t err=%v want %t", exists, err, test.want)
			}
		})
	}
}
