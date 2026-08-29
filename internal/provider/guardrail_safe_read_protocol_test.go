package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func guardrailSafeReadProtocolPriorState(t *testing.T, schema *tfprotov6.Schema, id string) *tfprotov6.DynamicValue {
	t.Helper()
	value := organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id":             id,
		"guardrail_id":   id,
		"guardrail_name": "prior-name-secret",
		"guardrail":      "bedrock",
		"mode":           "pre_call",
		"default_on":     false,
		"litellm_params": `{"api_key":"secret-plaintext","region":"us-east-1"}`,
		"guardrail_info": `{"owner":"prior-info-secret"}`,
		"created_at":     "2025-01-01T00:00:00Z",
	})
	return accessGroupProtocolDynamicValue(t, schema, value)
}

func TestGuardrailResourceSafeReadProtocolSequences(t *testing.T) {
	ctx := context.Background()
	const id = "guardrail-protocol"
	var mu sync.Mutex
	mode := "success"
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mu.Lock()
		currentMode := mode
		attempt++
		currentAttempt := attempt
		mu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet || request.URL.Path != "/guardrails/"+id+"/info" {
			http.Error(writer, `{"detail":"unexpected-route-secret"}`, http.StatusBadRequest)
			return
		}
		switch currentMode {
		case "transient-success":
			if currentAttempt == 1 {
				writer.Header().Set("Retry-After", "0")
				http.Error(writer, `{"detail":"temporary-body-secret"}`, http.StatusServiceUnavailable)
				return
			}
			_, _ = writer.Write(guardrailSafeReadUnmaskedBody(t, id, "db"))
		case "exhaustion":
			writer.Header().Set("Retry-After", "0")
			http.Error(writer, `{"detail":"body-secret 404 not found"}`, http.StatusBadGateway)
		case "terminal-400":
			http.Error(writer, `{"detail":"404 not found body-secret Retry-After"}`, http.StatusBadRequest)
		case "malformed":
			_, _ = writer.Write([]byte(`{"guardrail_id":`))
		case "missing-location":
			_, _ = fmt.Fprintf(writer, `{"guardrail_id":%q,"guardrail_name":"name","litellm_params":{"guardrail":"bedrock","mode":"pre_call"}}`, id)
		case "config-collision":
			_, _ = writer.Write(guardrailSafeReadBody(t, id, "config"))
		case "mismatch":
			_, _ = writer.Write(guardrailSafeReadBody(t, "other-identity-secret", "db"))
		case "malformed-late":
			_, _ = fmt.Fprintf(writer, `{"guardrail_id":%q,"guardrail_name":"new-name","guardrail_definition_location":"db","created_at":"new-time","litellm_params":{"guardrail":"bedrock","mode":"post_call","default_on":true},"guardrail_info":7}`, id)
		case "404":
			http.Error(writer, `{"detail":"missing body-secret"}`, http.StatusNotFound)
		default:
			_, _ = writer.Write(guardrailSafeReadUnmaskedBody(t, id, "db"))
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_guardrail"]
	prior := guardrailSafeReadProtocolPriorState(t, schema, id)
	private, _ := json.Marshal(map[string][]byte{guardrailImportedPrivateKey: []byte("true"), "guardrail_private": []byte(`"private-state-secret"`)})
	read := func(readMode string) (*tfprotov6.ReadResourceResponse, int) {
		t.Helper()
		mu.Lock()
		mode, attempt = readMode, 0
		mu.Unlock()
		response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_guardrail", CurrentState: prior, Private: private})
		if err != nil {
			t.Fatalf("mode=%s err=%v", readMode, err)
		}
		mu.Lock()
		defer mu.Unlock()
		return response, attempt
	}

	t.Run("transient then success projects once", func(t *testing.T) {
		response, calls := read("transient-success")
		if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != 2 {
			t.Fatalf("calls=%d diagnostics=%s", calls, agentProtocolDiagnosticsText(response.Diagnostics))
		}
		attributes := protocolAttributeMap(t, schema, response.NewState)
		var name, params string
		if err := attributes["guardrail_name"].As(&name); err != nil || name != "refreshed-name" {
			t.Fatalf("name=%q err=%v", name, err)
		}
		if err := attributes["litellm_params"].As(&params); err != nil || params != `{"api_key":"secret-plaintext","region":"us-west-2"}` {
			t.Fatalf("params=%q err=%v", params, err)
		}
		if protocolPrivateHasKey(t, response.Private, guardrailImportedPrivateKey) || !protocolPrivateHasKey(t, response.Private, "guardrail_private") {
			t.Fatal("successful refresh did not clear only the import marker")
		}
	})

	for _, failureMode := range []string{"exhaustion", "terminal-400", "malformed", "missing-location", "config-collision", "mismatch", "malformed-late"} {
		failureMode := failureMode
		t.Run(failureMode+" retains exact state", func(t *testing.T) {
			response, calls := read(failureMode)
			wantCalls := 1
			if failureMode == "exhaustion" {
				wantCalls = defaultSafeReadRetryPolicy.maxAttempts
			}
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != wantCalls {
				t.Fatalf("calls=%d want=%d diagnostics=%s", calls, wantCalls, text)
			}
			assertGuardrailRawStateUnchanged(t, prior, response.NewState)
			if !protocolPrivateHasKey(t, response.Private, guardrailImportedPrivateKey) || !protocolPrivateHasKey(t, response.Private, "guardrail_private") {
				t.Fatal("failed refresh changed private state")
			}
			for _, forbidden := range []string{server.URL, id, "prior-name-secret", "secret-plaintext", "prior-info-secret", "private-state-secret", "body-secret", "other-identity-secret", "guardrail_definition_location", "guardrail_info", "Retry-After"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}

	t.Run("exact 404 removes after one attempt", func(t *testing.T) {
		response, calls := read("404")
		if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != 1 {
			t.Fatalf("calls=%d diagnostics=%s", calls, agentProtocolDiagnosticsText(response.Diagnostics))
		}
		value, err := response.NewState.Unmarshal(schema.ValueType())
		if err != nil || !value.IsNull() {
			t.Fatalf("state=%v err=%v", value, err)
		}
	})
}

func TestGuardrailDataSourceSafeReadProtocolSequences(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		calls      int
	}{
		{name: "transient success", mode: "transient", calls: 2},
		{name: "config definition", mode: "config", calls: 1},
		{name: "exact 404", mode: "404", calls: 1},
		{name: "misleading 400", mode: "400", calls: 1},
		{name: "missing location", mode: "missing", calls: 1},
		{name: "mismatch", mode: "mismatch", calls: 1},
		{name: "malformed late", mode: "malformed", calls: 1},
		{name: "exhaustion", mode: "exhaustion", calls: defaultSafeReadRetryPolicy.maxAttempts},
	} {
		t.Run(test.name, func(t *testing.T) {
			const id = "guardrail-datasource"
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				attempt := calls.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				switch test.mode {
				case "transient":
					if attempt == 1 {
						writer.Header().Set("Retry-After", "0")
						http.Error(writer, `{"detail":"temporary-body-secret"}`, http.StatusServiceUnavailable)
						return
					}
					_, _ = writer.Write(guardrailSafeReadBody(t, id, "db"))
				case "config":
					_, _ = writer.Write(guardrailSafeReadBody(t, id, "config"))
				case "404":
					http.Error(writer, `{"detail":"missing-body-secret"}`, http.StatusNotFound)
				case "400":
					http.Error(writer, `{"detail":"404 not found body-secret"}`, http.StatusBadRequest)
				case "missing":
					_, _ = fmt.Fprintf(writer, `{"guardrail_id":%q,"guardrail_name":"name","litellm_params":{"guardrail":"bedrock","mode":"pre_call"}}`, id)
				case "mismatch":
					_, _ = writer.Write(guardrailSafeReadBody(t, "wrong-id-secret", "db"))
				case "malformed":
					_, _ = fmt.Fprintf(writer, `{"guardrail_id":%q,"guardrail_name":"name","guardrail_definition_location":"db","litellm_params":{"guardrail":"bedrock","mode":"pre_call"},"guardrail_info":7}`, id)
				case "exhaustion":
					writer.Header().Set("Retry-After", "0")
					http.Error(writer, `{"detail":"body-secret"}`, http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			ctx := context.Background()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.DataSourceSchemas["litellm_guardrail"]
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"guardrail_id": id}))
			response, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_guardrail", Config: config})
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if test.mode == "transient" || test.mode == "config" {
				if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls.Load() != int32(test.calls) {
					t.Fatalf("err=%v calls=%d diagnostics=%s", err, calls.Load(), text)
				}
				return
			}
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls.Load() != int32(test.calls) {
				t.Fatalf("err=%v calls=%d want=%d diagnostics=%s", err, calls.Load(), test.calls, text)
			}
			assertSingularPresenceStateUnchanged(t, schema, config, response.State)
			for _, forbidden := range []string{server.URL, id, "body-secret", "wrong-id-secret", "guardrail_definition_location", "guardrail_info"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}
}

func TestGuardrailSafeReadCancellationAndDeadlineRetainProtocolState(t *testing.T) {
	for _, test := range []struct {
		name   string
		isData bool
		make   func(<-chan struct{}) (context.Context, context.CancelFunc)
	}{
		{name: "resource cancellation", make: func(started <-chan struct{}) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			go func() { <-started; cancel() }()
			return ctx, cancel
		}},
		{name: "data source deadline", isData: true, make: func(<-chan struct{}) (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 100*time.Millisecond)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			started := make(chan struct{})
			var once sync.Once
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
				once.Do(func() { close(started) })
				<-request.Context().Done()
			}))
			defer server.Close()
			baseCtx := context.Background()
			protocolServer, schemas := configuredImportProtocolServer(t, baseCtx, server.URL)
			readCtx, cancel := test.make(started)
			defer cancel()
			if test.isData {
				schema := schemas.DataSourceSchemas["litellm_guardrail"]
				config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"guardrail_id": "guardrail-deadline"}))
				response, err := protocolServer.ReadDataSource(readCtx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_guardrail", Config: config})
				if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
					t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
				}
				assertSingularPresenceStateUnchanged(t, schema, config, response.State)
				return
			}
			schema := schemas.ResourceSchemas["litellm_guardrail"]
			prior := guardrailSafeReadProtocolPriorState(t, schema, "guardrail-cancel")
			private, _ := json.Marshal(map[string][]byte{guardrailImportedPrivateKey: []byte("true")})
			response, err := protocolServer.ReadResource(readCtx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_guardrail", CurrentState: prior, Private: private})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
				t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
			}
			assertGuardrailRawStateUnchanged(t, prior, response.NewState)
			if !protocolPrivateHasKey(t, response.Private, guardrailImportedPrivateKey) {
				t.Fatal("canceled refresh changed import marker")
			}
		})
	}
}

func TestGuardrailDeleteUsesTyped404OnlyProtocol(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		http.Error(writer, `{"detail":"404 not found misleading-body-secret"}`, http.StatusInternalServerError)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_guardrail"]
	prior := guardrailSafeReadProtocolPriorState(t, schema, "guardrail-delete")
	response, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     "litellm_guardrail",
		PriorState:   prior,
		PlannedState: accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil)),
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d diagnostics=%s", err, calls.Load(), agentProtocolDiagnosticsText(response.Diagnostics))
	}
	if text := agentProtocolDiagnosticsText(response.Diagnostics); strings.Contains(text, "misleading-body-secret") || strings.Contains(text, "guardrail-delete") || strings.Contains(text, server.URL) {
		t.Fatalf("delete diagnostic leaked details: %s", text)
	}
}
