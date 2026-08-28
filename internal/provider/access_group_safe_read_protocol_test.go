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
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func accessGroupSafeReadProtocolPriorState(t *testing.T, schema *tfprotov6.Schema, accessGroup string) *tfprotov6.DynamicValue {
	t.Helper()
	value := organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id":           accessGroup,
		"access_group": accessGroup,
		"model_names":  []tftypes.Value{tftypes.NewValue(tftypes.String, "prior-model-secret")},
	})
	return accessGroupProtocolDynamicValue(t, schema, value)
}

func assertAccessGroupRawStateUnchanged(t *testing.T, want, got *tfprotov6.DynamicValue) {
	t.Helper()
	if want == nil || got == nil || !bytes.Equal(want.MsgPack, got.MsgPack) || !bytes.Equal(want.JSON, got.JSON) {
		t.Fatal("public raw state changed")
	}
}

func TestAccessGroupResourceSafeReadProtocolSequences(t *testing.T) {
	ctx := context.Background()
	const accessGroup = "protocol-access"
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
		if request.Method != http.MethodGet || request.URL.RequestURI() != endpointWithPathSegment("/access_group/", accessGroup, "/info") {
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
			_, _ = writer.Write(accessGroupSafeReadBody(t, accessGroup, "z-model", "a-model"))
		case "exhaustion":
			writer.Header().Set("Retry-After", "0")
			http.Error(writer, `{"detail":"body-secret 404 not found"}`, http.StatusBadGateway)
		case "terminal-4xx":
			http.Error(writer, `{"detail":"404 not found body-secret Retry-After"}`, http.StatusBadRequest)
		case "malformed":
			_, _ = writer.Write([]byte(`{"access_group":`))
		case "mismatch":
			_, _ = writer.Write(accessGroupSafeReadBody(t, "wrong-access-secret", "model"))
		case "malformed-membership":
			_, _ = fmt.Fprintf(writer, `{"access_group":%q,"model_names":["ok",1]}`, accessGroup)
		case "404":
			http.Error(writer, `{"detail":"missing body-secret"}`, http.StatusNotFound)
		default:
			_, _ = writer.Write(accessGroupSafeReadBody(t, accessGroup, "model"))
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_access_group"]
	prior := accessGroupSafeReadProtocolPriorState(t, schema, accessGroup)
	private, err := json.Marshal(map[string][]byte{"access_group_private": []byte(`"private-state-secret"`)})
	if err != nil {
		t.Fatal(err)
	}
	read := func(readMode string) (*tfprotov6.ReadResourceResponse, int) {
		t.Helper()
		mu.Lock()
		mode = readMode
		attempt = 0
		mu.Unlock()
		response, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_access_group", CurrentState: prior, Private: private})
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
		expectedModels := tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{
			tftypes.NewValue(tftypes.String, "a-model"),
			tftypes.NewValue(tftypes.String, "z-model"),
		})
		if !attributes["model_names"].Equal(expectedModels) {
			t.Fatalf("models=%v want=%v", attributes["model_names"], expectedModels)
		}
		if !bytes.Equal(response.Private, private) {
			t.Fatal("successful refresh changed private state")
		}
	})

	for _, failureMode := range []string{"exhaustion", "terminal-4xx", "malformed", "mismatch", "malformed-membership"} {
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
			assertAccessGroupRawStateUnchanged(t, prior, response.NewState)
			if !bytes.Equal(response.Private, private) {
				t.Fatal("private state changed")
			}
			for _, forbidden := range []string{server.URL, accessGroup, "prior-model-secret", "private-state-secret", "body-secret", "wrong-access-secret", "access_group", "model_names", "Retry-After"} {
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

func TestAccessGroupDataSourceSafeReadProtocolSequences(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		body        func(string) []byte
		calls       int
		firstStatus int
	}{
		{name: "transient then success", status: http.StatusOK, body: func(id string) []byte { return accessGroupSafeReadBody(t, id, "model") }, calls: 2, firstStatus: http.StatusServiceUnavailable},
		{name: "complete exact 404", status: http.StatusNotFound, body: func(string) []byte { return []byte(`{"detail":"missing-body-secret"}`) }, calls: 1},
		{name: "misleading 400", status: http.StatusBadRequest, body: func(string) []byte { return []byte(`{"detail":"404 not found body-secret Retry-After"}`) }, calls: 1},
		{name: "malformed success", status: http.StatusOK, body: func(string) []byte { return []byte(`{"access_group":`) }, calls: 1},
		{name: "identity mismatch", status: http.StatusOK, body: func(string) []byte { return accessGroupSafeReadBody(t, "wrong-data-source-secret", "model") }, calls: 1},
		{name: "malformed membership", status: http.StatusOK, body: func(id string) []byte { return []byte(fmt.Sprintf(`{"access_group":%q,"model_names":["ok",1]}`, id)) }, calls: 1},
		{name: "retry exhaustion", status: http.StatusServiceUnavailable, body: func(string) []byte { return []byte(`{"detail":"exhaustion-body-secret"}`) }, calls: defaultSafeReadRetryPolicy.maxAttempts},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			const accessGroup = "data-source-access"
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
					_, _ = writer.Write(test.body(accessGroup))
				} else {
					_, _ = fmt.Fprint(writer, `{"detail":"temporary-body-secret"}`)
				}
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.DataSourceSchemas["litellm_access_group"]
			config := singularPresenceConfig(t, schema, map[string]interface{}{"access_group": accessGroup})
			response, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_access_group", Config: config})
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
			for _, forbidden := range []string{server.URL, accessGroup, "body-secret", "wrong-data-source-secret", "access_group", "model_names", "Retry-After"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}
}

func TestAccessGroupProtocolCancellationRetainsResourceState(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		once.Do(func() { close(started) })
		<-request.Context().Done()
	}))
	defer server.Close()
	baseCtx := context.Background()
	protocolServer, schemas := configuredImportProtocolServer(t, baseCtx, server.URL)
	schema := schemas.ResourceSchemas["litellm_access_group"]
	prior := accessGroupSafeReadProtocolPriorState(t, schema, "cancel-access")
	private, err := json.Marshal(map[string][]byte{"access_group_private": []byte(`"private-state-secret"`)})
	if err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithCancel(baseCtx)
	go func() { <-started; cancel() }()
	response, err := protocolServer.ReadResource(readCtx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_access_group", CurrentState: prior, Private: private})
	if err != nil {
		t.Fatalf("protocol read returned error instead of retained state diagnostic: %v", err)
	}
	text := agentProtocolDiagnosticsText(response.Diagnostics)
	if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
		t.Fatalf("missing error diagnostic: %s", text)
	}
	assertAccessGroupRawStateUnchanged(t, prior, response.NewState)
	if !bytes.Equal(private, response.Private) {
		t.Fatal("private state changed")
	}
	for _, forbidden := range []string{server.URL, "cancel-access", "prior-model-secret", "access_group"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
		}
	}
}

func TestAccessGroupProtocolDeadlineRetainsDataSourceConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) { <-request.Context().Done() }))
	defer server.Close()
	baseCtx := context.Background()
	protocolServer, schemas := configuredImportProtocolServer(t, baseCtx, server.URL)
	schema := schemas.DataSourceSchemas["litellm_access_group"]
	config := singularPresenceConfig(t, schema, map[string]interface{}{"access_group": "deadline-access"})
	ctx, cancel := context.WithTimeout(baseCtx, 100*time.Millisecond)
	defer cancel()
	response, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_access_group", Config: config})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
		t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
	}
	assertSingularPresenceStateUnchanged(t, schema, config, response.State)
}
