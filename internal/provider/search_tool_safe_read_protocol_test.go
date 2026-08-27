package provider

import (
	"bytes"
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
)

func searchToolProtocolPriorState(t *testing.T, schema *tfprotov6.Schema, id string) *tfprotov6.DynamicValue {
	t.Helper()
	value := organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id":               id,
		"search_tool_id":   id,
		"search_tool_name": "prior-name-secret",
		"search_provider":  "prior-provider-secret",
		"api_key":          "prior-api-key-secret",
		"api_base":         "https://prior.invalid/private",
		"timeout":          1.5,
		"max_retries":      1,
		"search_tool_info": `{"prior":"private-state-secret"}`,
	})
	return accessGroupProtocolDynamicValue(t, schema, value)
}

func assertSearchToolProtocolRawStateUnchanged(t *testing.T, want, got *tfprotov6.DynamicValue) {
	t.Helper()
	if want == nil || got == nil || !bytes.Equal(want.MsgPack, got.MsgPack) || !bytes.Equal(want.JSON, got.JSON) {
		t.Fatal("public raw state changed")
	}
}

func TestSearchToolResourceSafeReadProtocolSequences(t *testing.T) {
	ctx := context.Background()
	const id = "protocol-search"
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
		if request.Method != http.MethodGet || request.URL.RequestURI() != endpointWithPathSegment("/search_tools/", id, "") {
			http.Error(writer, `{"detail":"unexpected-route-secret"}`, http.StatusBadRequest)
			return
		}
		switch currentMode {
		case "transient-success":
			if currentAttempt == 1 {
				writer.Header().Set("Retry-After", "0")
				http.Error(writer, `{"detail":"temporary-response-secret"}`, http.StatusServiceUnavailable)
				return
			}
			_, _ = writer.Write(searchToolTestBody(t, id))
		case "exhaustion":
			writer.Header().Set("Retry-After", "0")
			http.Error(writer, `{"detail":"body-secret 404 not found"}`, http.StatusBadGateway)
		case "terminal-4xx":
			http.Error(writer, `{"detail":"404 not found body-secret Retry-After"}`, http.StatusBadRequest)
		case "malformed":
			_, _ = writer.Write([]byte(`{"search_tool_id":`))
		case "mismatch":
			_, _ = writer.Write(searchToolTestBody(t, "wrong-search-secret"))
		case "null-api-base":
			_, _ = fmt.Fprintf(writer, `{"search_tool_id":%q,"search_tool_name":"name","litellm_params":{"search_provider":"provider","api_base":null}}`, id)
		case "malformed-api-base":
			_, _ = fmt.Fprintf(writer, `{"search_tool_id":%q,"search_tool_name":"name","litellm_params":{"search_provider":"provider","api_base":123}}`, id)
		case "404":
			http.Error(writer, `{"detail":"missing body-secret"}`, http.StatusNotFound)
		default:
			_, _ = writer.Write(searchToolTestBody(t, id))
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_search_tool"]
	prior := searchToolProtocolPriorState(t, schema, id)
	private, err := json.Marshal(map[string][]byte{"search_tool_test_private": []byte(`"private-state-secret"`)})
	if err != nil {
		t.Fatal(err)
	}
	read := func(readMode string) (*tfprotov6.ReadResourceResponse, int) {
		t.Helper()
		mu.Lock()
		mode = readMode
		attempt = 0
		mu.Unlock()
		response, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_search_tool", CurrentState: prior, Private: private})
		if readErr != nil {
			t.Fatalf("mode=%s err=%v", readMode, readErr)
		}
		mu.Lock()
		defer mu.Unlock()
		return response, attempt
	}

	t.Run("transient then 200 refreshes state", func(t *testing.T) {
		response, calls := read("transient-success")
		if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != 2 {
			t.Fatalf("calls=%d diagnostics=%s", calls, agentProtocolDiagnosticsText(response.Diagnostics))
		}
		attributes := protocolAttributeMap(t, schema, response.NewState)
		var name, apiKey string
		if err := attributes["search_tool_name"].As(&name); err != nil || name != "refreshed-name" {
			t.Fatalf("name=%q err=%v", name, err)
		}
		if err := attributes["api_key"].As(&apiKey); err != nil || apiKey != "prior-api-key-secret" {
			t.Fatalf("api_key was not retained: err=%v", err)
		}
		if !bytes.Equal(response.Private, private) {
			t.Fatal("successful refresh changed unrelated private state")
		}
	})

	t.Run("authoritative null api_base clears prior state", func(t *testing.T) {
		response, calls := read("null-api-base")
		if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != 1 {
			t.Fatalf("calls=%d diagnostics=%s", calls, agentProtocolDiagnosticsText(response.Diagnostics))
		}
		attributes := protocolAttributeMap(t, schema, response.NewState)
		if !attributes["api_base"].IsNull() {
			t.Fatalf("api_base = %#v", attributes["api_base"])
		}
	})

	for _, failureMode := range []string{"exhaustion", "terminal-4xx", "malformed", "mismatch", "malformed-api-base"} {
		failureMode := failureMode
		t.Run(failureMode+" retains byte-exact public and private state", func(t *testing.T) {
			response, calls := read(failureMode)
			wantCalls := 1
			if failureMode == "exhaustion" {
				wantCalls = defaultSafeReadRetryPolicy.maxAttempts
			}
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != wantCalls {
				t.Fatalf("calls=%d want=%d diagnostics=%s", calls, wantCalls, text)
			}
			assertSearchToolProtocolRawStateUnchanged(t, prior, response.NewState)
			if !bytes.Equal(response.Private, private) {
				t.Fatal("private state changed")
			}
			for _, forbidden := range []string{server.URL, id, "prior-name-secret", "prior-provider-secret", "prior-api-key-secret", "private-state-secret", "body-secret", "wrong-search-secret", "search_tools", "Retry-After"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}

	t.Run("complete exact 404 removes resource after one attempt", func(t *testing.T) {
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

func TestSearchToolImportedNumericMarkerRetainedOnFailedSafeReadProtocol(t *testing.T) {
	ctx := context.Background()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Retry-After", "0")
		http.Error(writer, `{"detail":"import-failure-body-secret"}`, http.StatusServiceUnavailable)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_search_tool"
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "imported-search"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	prior := imported.ImportedResources[0]
	if !protocolPrivateHasKey(t, prior.Private, numericImportedPrivateKey) {
		t.Fatal("import omitted numeric ownership marker")
	}
	failed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: prior.State, Private: prior.Private})
	text := agentProtocolDiagnosticsText(failed.Diagnostics)
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) || calls.Load() != int32(defaultSafeReadRetryPolicy.maxAttempts) {
		t.Fatalf("err=%v calls=%d diagnostics=%s", err, calls.Load(), text)
	}
	assertSearchToolProtocolRawStateUnchanged(t, prior.State, failed.NewState)
	if !protocolPrivateHasKey(t, failed.Private, numericImportedPrivateKey) ||
		!bytes.Equal(protocolPrivateValue(t, prior.Private, numericImportedPrivateKey), protocolPrivateValue(t, failed.Private, numericImportedPrivateKey)) {
		t.Fatal("failed imported refresh changed the numeric ownership marker")
	}
	// terraform-plugin-framework consumes its own .import_before_read key on the
	// first Read; the provider-owned numeric marker must nevertheless survive.
	for _, forbidden := range []string{server.URL, "imported-search", "import-failure-body-secret", "search_tools", "Retry-After"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
		}
	}
	_ = schemas
}

func TestSearchToolDataSourceSafeReadProtocolSequences(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		body        func(string) []byte
		calls       int
		firstStatus int
	}{
		{name: "transient then success", status: http.StatusOK, body: func(id string) []byte { return searchToolTestBody(t, id) }, calls: 2, firstStatus: http.StatusServiceUnavailable},
		{name: "complete exact 404", status: http.StatusNotFound, body: func(string) []byte { return []byte(`{"detail":"missing-body-secret"}`) }, calls: 1},
		{name: "misleading 400", status: http.StatusBadRequest, body: func(string) []byte { return []byte(`{"detail":"404 not found body-secret Retry-After"}`) }, calls: 1},
		{name: "malformed success", status: http.StatusOK, body: func(string) []byte { return []byte(`{"search_tool_id":`) }, calls: 1},
		{name: "identity mismatch", status: http.StatusOK, body: func(string) []byte { return searchToolTestBody(t, "wrong-data-source-secret") }, calls: 1},
		{name: "retry exhaustion", status: http.StatusServiceUnavailable, body: func(string) []byte { return []byte(`{"detail":"exhaustion-body-secret"}`) }, calls: defaultSafeReadRetryPolicy.maxAttempts},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			const id = "data-source-search"
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				attempt := calls.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				status := test.status
				if attempt == 1 && test.firstStatus != 0 {
					status = test.firstStatus
				}
				if status == http.StatusServiceUnavailable {
					writer.Header().Set("Retry-After", "0")
				}
				writer.WriteHeader(status)
				if status == test.status {
					_, _ = writer.Write(test.body(id))
				} else {
					_, _ = fmt.Fprint(writer, `{"detail":"temporary-body-secret"}`)
				}
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.DataSourceSchemas["litellm_search_tool"]
			config := singularPresenceConfig(t, schema, map[string]interface{}{"search_tool_id": id})
			response, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_search_tool", Config: config})
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if test.name == "transient then success" {
				if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls.Load() != int32(test.calls) {
					t.Fatalf("err=%v calls=%d diagnostics=%s", err, calls.Load(), text)
				}
				assertDataSourceReadComputedKnown(t, schema, response)
				return
			}
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls.Load() != int32(test.calls) {
				t.Fatalf("err=%v calls=%d want=%d diagnostics=%s", err, calls.Load(), test.calls, text)
			}
			assertSingularPresenceStateUnchanged(t, schema, config, response.State)
			for _, forbidden := range []string{server.URL, id, "body-secret", "wrong-data-source-secret", "search_tools", "Retry-After"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}
}

func TestSearchToolProtocolCancellationAndDeadlineRetainResourceState(t *testing.T) {
	for _, test := range []struct {
		name    string
		context func(<-chan struct{}) (context.Context, context.CancelFunc)
	}{
		{
			name: "cancellation",
			context: func(started <-chan struct{}) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				go func() { <-started; cancel() }()
				return ctx, cancel
			},
		},
		{
			name: "deadline",
			context: func(<-chan struct{}) (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 100*time.Millisecond)
			},
		},
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
			schema := schemas.ResourceSchemas["litellm_search_tool"]
			prior := searchToolProtocolPriorState(t, schema, "cancel-search")
			private, err := json.Marshal(map[string][]byte{numericImportedPrivateKey: []byte("true")})
			if err != nil {
				t.Fatal(err)
			}
			readCtx, cancel := test.context(started)
			defer cancel()
			response, readErr := protocolServer.ReadResource(readCtx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_search_tool", CurrentState: prior, Private: private})
			if readErr != nil {
				t.Fatalf("protocol read returned error instead of retained state diagnostic: %v", readErr)
			}
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
				t.Fatalf("missing error diagnostic: %s", text)
			}
			assertSearchToolProtocolRawStateUnchanged(t, prior, response.NewState)
			if !bytes.Equal(private, response.Private) {
				t.Fatal("private state changed")
			}
			for _, forbidden := range []string{server.URL, "cancel-search", "prior-api-key-secret", "search_tools"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}
}

func TestSearchToolProtocolCancellationAndDeadlinePublishNoDataSourcePartialState(t *testing.T) {
	for _, test := range []struct {
		name    string
		context func(<-chan struct{}) (context.Context, context.CancelFunc)
	}{
		{
			name: "cancellation",
			context: func(started <-chan struct{}) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				go func() { <-started; cancel() }()
				return ctx, cancel
			},
		},
		{
			name: "deadline",
			context: func(<-chan struct{}) (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 100*time.Millisecond)
			},
		},
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
			schema := schemas.DataSourceSchemas["litellm_search_tool"]
			config := singularPresenceConfig(t, schema, map[string]interface{}{"search_tool_id": "cancel-data-source"})
			readCtx, cancel := test.context(started)
			defer cancel()
			response, readErr := protocolServer.ReadDataSource(readCtx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_search_tool", Config: config})
			if readErr != nil {
				t.Fatalf("protocol read returned error instead of retained config diagnostic: %v", readErr)
			}
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
				t.Fatalf("missing error diagnostic: %s", text)
			}
			assertSingularPresenceStateUnchanged(t, schema, config, response.State)
			for _, forbidden := range []string{server.URL, "cancel-data-source", "search_tools"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}
}
