package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestModelCredentialDetachSendsNullAndWaitsForWorkers covers LiteLLM 1.104.0:
// "" is rejected for litellm_credential_name and only JSON null detaches it,
// and a multi-worker proxy keeps returning the old value until each worker
// reloads its model list.
func TestModelCredentialDetachSendsNullAndWaitsForWorkers(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	credential := "shared-credential"
	staleReads := 0
	var patch map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/model/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodPatch:
			_ = json.NewDecoder(request.Body).Decode(&patch)
			params, _ := patch["litellm_params"].(map[string]interface{})
			if value, sent := params["litellm_credential_name"]; sent {
				if value == "" {
					http.Error(writer, `{"detail":"litellm_credential_name cannot be an empty string"}`, http.StatusBadRequest)
					return
				}
				if value == nil {
					credential, staleReads = "", 3
				}
			}
			_, _ = fmt.Fprint(writer, `{"status":"ok"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/model/info":
			shown := credential
			if staleReads > 0 {
				staleReads--
				shown = "shared-credential"
				if staleReads == 1 {
					// A worker reloading the updated model briefly cannot find it.
					http.Error(writer, `{"detail":{"error":"Model id = x not found on litellm proxy"}}`, http.StatusBadRequest)
					return
				}
			}
			params := map[string]interface{}{"custom_llm_provider": "openai", "model": "openai/gpt-4o-mini"}
			if shown != "" {
				params["litellm_credential_name"] = shown
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"data": []interface{}{map[string]interface{}{
				"model_name": "credential-model", "litellm_params": params,
				"model_info": map[string]interface{}{"id": request.URL.Query().Get("litellm_model_id"), "base_model": "gpt-4o-mini"},
			}}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_model"
	schema := schemas.ResourceSchemas[typeName]
	attached := map[string]interface{}{"model_name": "credential-model", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini", "litellm_credential_name": "shared-credential"}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, attached))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: modelAdditionalLiteLLMParamsJSONCreateProposed(t, schema, attached)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("create: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(created.Diagnostics))
	}

	detached := map[string]interface{}{"model_name": "credential-model", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini"}
	detachConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, detached))
	proposed := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{"litellm_credential_name": nil})
	detachPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: detachConfig, PriorState: created.NewState, ProposedNewState: proposed, PriorPrivate: created.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(detachPlan.Diagnostics) {
		t.Fatalf("detach plan: err=%v diagnostics=%v", err, detachPlan.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: detachConfig, PriorState: created.NewState, PlannedState: detachPlan.PlannedState, PlannedPrivate: detachPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("detach: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	params, _ := patch["litellm_params"].(map[string]interface{})
	if value, sent := params["litellm_credential_name"]; !sent || value != nil {
		t.Fatalf("detach PATCH litellm_credential_name=%#v sent=%t, want explicit null", value, sent)
	}
	if !protocolAttributeMap(t, schema, applied.NewState)["litellm_credential_name"].IsNull() {
		t.Fatal("detached credential name remained in state")
	}
}

func TestModelReadToleratesBriefWorkerReload400(t *testing.T) {
	for _, test := range []struct {
		name      string
		bad       int
		wantError bool
		wantReads int
	}{
		{"brief reload converges", 2, false, 3},
		{"persistent 400 still fails", 100, true, maxTransientModelReadRetries + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				reads++
				writer.Header().Set("Content-Type", "application/json")
				if reads <= test.bad {
					http.Error(writer, `{"detail":{"error":"Model id = m1 not found on litellm proxy"}}`, http.StatusBadRequest)
					return
				}
				_, _ = fmt.Fprint(writer, `{"data":[{"model_name":"reload-model","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini"},"model_info":{"id":"m1","base_model":"gpt-4o-mini"}}]}`)
			}))
			defer server.Close()
			ctx := context.Background()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_model", ID: "m1"})
			if err != nil || len(imported.ImportedResources) != 1 {
				t.Fatalf("import err=%v", err)
			}
			read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_model", CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
			if err != nil {
				t.Fatal(err)
			}
			if accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) != test.wantError || reads != test.wantReads {
				t.Fatalf("reads=%d want %d diagnostics=%s", reads, test.wantReads, agentProtocolDiagnosticsText(read.Diagnostics))
			}
			if test.wantError {
				if value, _ := read.NewState.Unmarshal(schemas.ResourceSchemas["litellm_model"].ValueType()); value.IsNull() {
					t.Fatal("persistent 400 removed the model from state")
				}
			}
		})
	}
}
