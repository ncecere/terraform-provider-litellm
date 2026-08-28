package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func userSafeReadProtocolPriorState(t *testing.T, schema *tfprotov6.Schema, userID string) *tfprotov6.DynamicValue {
	t.Helper()
	value := organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id":                userID,
		"user_id":           userID,
		"user_alias":        "prior-alias-secret",
		"user_email":        "prior@example.invalid",
		"user_role":         "internal_user_viewer",
		"teams":             []tftypes.Value{tftypes.NewValue(tftypes.String, "team-a"), tftypes.NewValue(tftypes.String, "team-b")},
		"models":            []tftypes.Value{tftypes.NewValue(tftypes.String, "prior-model-secret")},
		"max_budget":        1.5,
		"budget_duration":   "1d",
		"tpm_limit":         int64(10),
		"rpm_limit":         int64(1),
		"auto_create_key":   true,
		"send_invite_email": nil,
		"metadata":          map[string]tftypes.Value{"prior": tftypes.NewValue(tftypes.String, "private-metadata-secret")},
		"key":               "prior-key-secret",
	})
	return accessGroupProtocolDynamicValue(t, schema, value)
}

func assertUserRawStateUnchanged(t *testing.T, want, got *tfprotov6.DynamicValue) {
	t.Helper()
	if want == nil || got == nil || !bytes.Equal(want.MsgPack, got.MsgPack) || !bytes.Equal(want.JSON, got.JSON) {
		t.Fatal("public raw state changed")
	}
}

func TestUserResourceSafeReadProtocolSequences(t *testing.T) {
	ctx := context.Background()
	const userID = "protocol-user"
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
		expectedURI := endpointWithQuery("/user/info", url.Values{"user_id": []string{userID}})
		if request.Method != http.MethodGet || request.URL.RequestURI() != expectedURI {
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
			_, _ = writer.Write(userSafeReadBody(t, userID, userID))
		case "exhaustion":
			writer.Header().Set("Retry-After", "0")
			http.Error(writer, `{"detail":"body-secret 404 not found"}`, http.StatusBadGateway)
		case "terminal-4xx":
			http.Error(writer, `{"detail":"404 not found body-secret Retry-After"}`, http.StatusBadRequest)
		case "malformed":
			_, _ = writer.Write([]byte(`{"user_id":`))
		case "missing-envelope":
			_, _ = fmt.Fprintf(writer, `{"user_id":%q}`, userID)
		case "root-mismatch":
			_, _ = writer.Write(userSafeReadBody(t, "wrong-root-secret", userID))
		case "nested-mismatch":
			_, _ = writer.Write(userSafeReadBody(t, userID, "wrong-nested-secret"))
		case "malformed-collection":
			_, _ = fmt.Fprintf(writer, `{"user_id":%q,"user_info":{"user_id":%q,"teams":["ok",1]}}`, userID, userID)
		case "404":
			http.Error(writer, `{"detail":"missing body-secret"}`, http.StatusNotFound)
		default:
			_, _ = writer.Write(userSafeReadBody(t, userID, userID))
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_user"]
	prior := userSafeReadProtocolPriorState(t, schema, userID)
	private, err := json.Marshal(map[string][]byte{
		numericImportedPrivateKey: []byte("true"),
		"user_private":            []byte(`"private-state-secret"`),
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func(readMode string, priorPrivate []byte) (*tfprotov6.ReadResourceResponse, int) {
		t.Helper()
		mu.Lock()
		mode = readMode
		attempt = 0
		mu.Unlock()
		response, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_user", CurrentState: prior, Private: priorPrivate})
		if readErr != nil {
			t.Fatalf("mode=%s err=%v", readMode, readErr)
		}
		mu.Lock()
		defer mu.Unlock()
		return response, attempt
	}

	t.Run("transient then 200 refreshes state and clears import marker", func(t *testing.T) {
		response, calls := read("transient-success", private)
		if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != 2 {
			t.Fatalf("calls=%d diagnostics=%s", calls, agentProtocolDiagnosticsText(response.Diagnostics))
		}
		attributes := protocolAttributeMap(t, schema, response.NewState)
		var alias, key string
		if err := attributes["user_alias"].As(&alias); err != nil || alias != "refreshed-alias" {
			t.Fatalf("alias=%q err=%v", alias, err)
		}
		if err := attributes["key"].As(&key); err != nil || key != "prior-key-secret" {
			t.Fatalf("key was not retained: err=%v", err)
		}
		if protocolPrivateHasKey(t, response.Private, numericImportedPrivateKey) || !protocolPrivateHasKey(t, response.Private, "user_private") {
			t.Fatal("successful imported refresh did not clear only the import marker")
		}
	})

	for _, failureMode := range []string{"exhaustion", "terminal-4xx", "malformed", "missing-envelope", "root-mismatch", "nested-mismatch", "malformed-collection"} {
		failureMode := failureMode
		t.Run(failureMode+" retains byte-exact public and private state", func(t *testing.T) {
			response, calls := read(failureMode, private)
			wantCalls := 1
			if failureMode == "exhaustion" {
				wantCalls = defaultSafeReadRetryPolicy.maxAttempts
			}
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != wantCalls {
				t.Fatalf("calls=%d want=%d diagnostics=%s", calls, wantCalls, text)
			}
			assertUserRawStateUnchanged(t, prior, response.NewState)
			if !protocolPrivateHasKey(t, response.Private, numericImportedPrivateKey) || !protocolPrivateHasKey(t, response.Private, "user_private") {
				t.Fatal("failed refresh changed private state")
			}
			for _, forbidden := range []string{server.URL, userID, "prior-alias-secret", "prior@example.invalid", "prior-model-secret", "prior-key-secret", "private-metadata-secret", "private-state-secret", "body-secret", "wrong-root-secret", "wrong-nested-secret", "user_info", "user_id", "Retry-After"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}

	t.Run("complete exact 404 removes resource after one attempt", func(t *testing.T) {
		response, calls := read("404", private)
		if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != 1 {
			t.Fatalf("calls=%d diagnostics=%s", calls, agentProtocolDiagnosticsText(response.Diagnostics))
		}
		value, err := response.NewState.Unmarshal(schema.ValueType())
		if err != nil || !value.IsNull() {
			t.Fatalf("state=%v err=%v", value, err)
		}
	})
}

func TestUserDataSourceSafeReadProtocolSequences(t *testing.T) {
	for _, test := range []struct {
		name        string
		status      int
		body        func(string) []byte
		calls       int
		firstStatus int
	}{
		{name: "transient then success", status: http.StatusOK, body: func(id string) []byte { return userSafeReadBody(t, id, id) }, calls: 2, firstStatus: http.StatusServiceUnavailable},
		{name: "complete exact 404", status: http.StatusNotFound, body: func(string) []byte { return []byte(`{"detail":"missing-body-secret"}`) }, calls: 1},
		{name: "misleading 400", status: http.StatusBadRequest, body: func(string) []byte { return []byte(`{"detail":"404 not found body-secret Retry-After"}`) }, calls: 1},
		{name: "malformed success", status: http.StatusOK, body: func(string) []byte { return []byte(`{"user_id":`) }, calls: 1},
		{name: "missing envelope", status: http.StatusOK, body: func(id string) []byte { return []byte(fmt.Sprintf(`{"user_id":%q}`, id)) }, calls: 1},
		{name: "root mismatch", status: http.StatusOK, body: func(id string) []byte { return userSafeReadBody(t, "wrong-root-secret", id) }, calls: 1},
		{name: "nested mismatch", status: http.StatusOK, body: func(id string) []byte { return userSafeReadBody(t, id, "wrong-nested-secret") }, calls: 1},
		{name: "malformed collection", status: http.StatusOK, body: func(id string) []byte {
			return []byte(fmt.Sprintf(`{"user_id":%q,"user_info":{"user_id":%q,"teams":["ok",1]}}`, id, id))
		}, calls: 1},
		{name: "retry exhaustion", status: http.StatusServiceUnavailable, body: func(string) []byte { return []byte(`{"detail":"exhaustion-body-secret"}`) }, calls: defaultSafeReadRetryPolicy.maxAttempts},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			const userID = "data-source-user"
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
					_, _ = writer.Write(test.body(userID))
				} else {
					_, _ = fmt.Fprint(writer, `{"detail":"temporary-body-secret"}`)
				}
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.DataSourceSchemas["litellm_user"]
			config := singularPresenceConfig(t, schema, map[string]interface{}{"user_id": userID})
			response, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_user", Config: config})
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
			for _, forbidden := range []string{server.URL, userID, "body-secret", "wrong-root-secret", "wrong-nested-secret", "user_info", "user_id", "Retry-After"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}
}

func TestUserProtocolCancellationAndDeadlineRetainState(t *testing.T) {
	for _, test := range []struct {
		name    string
		isData  bool
		context func(<-chan struct{}) (context.Context, context.CancelFunc)
	}{
		{name: "resource cancellation", context: func(started <-chan struct{}) (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			go func() { <-started; cancel() }()
			return ctx, cancel
		}},
		{name: "data source deadline", isData: true, context: func(<-chan struct{}) (context.Context, context.CancelFunc) {
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
			readCtx, cancel := test.context(started)
			defer cancel()
			if test.isData {
				schema := schemas.DataSourceSchemas["litellm_user"]
				config := singularPresenceConfig(t, schema, map[string]interface{}{"user_id": "deadline-user"})
				response, err := protocolServer.ReadDataSource(readCtx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_user", Config: config})
				if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
					t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
				}
				assertSingularPresenceStateUnchanged(t, schema, config, response.State)
				return
			}
			schema := schemas.ResourceSchemas["litellm_user"]
			prior := userSafeReadProtocolPriorState(t, schema, "cancel-user")
			private, _ := json.Marshal(map[string][]byte{numericImportedPrivateKey: []byte("true")})
			response, err := protocolServer.ReadResource(readCtx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_user", CurrentState: prior, Private: private})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
				t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
			}
			assertUserRawStateUnchanged(t, prior, response.NewState)
			if !protocolPrivateHasKey(t, response.Private, numericImportedPrivateKey) {
				t.Fatal("canceled refresh changed private marker")
			}
		})
	}
}
