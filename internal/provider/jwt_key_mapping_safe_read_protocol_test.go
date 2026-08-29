package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func jwtMappingProtocolPriorState(t *testing.T, schema *tfprotov6.Schema) *tfprotov6.DynamicValue {
	t.Helper()
	return jwtMappingProtocolValue(t, schema, map[string]interface{}{
		"id":              jwtMappingID1,
		"jwt_claim_name":  "sub",
		"jwt_claim_value": "prior-claim-secret",
		"key_wo_version":  "prior-version",
		"description":     "prior-description-secret",
		"is_active":       false,
		"created_at":      "2026-08-26T00:00:00Z",
		"updated_at":      "2026-08-26T00:01:00Z",
		"created_by":      "prior-creator-secret",
		"updated_by":      "prior-updater-secret",
	})
}

func assertJWTMappingProtocolStateEqual(t *testing.T, schema *tfprotov6.Schema, want, got *tfprotov6.DynamicValue) {
	t.Helper()
	wantValue, err := want.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	gotValue, err := got.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	if !wantValue.Equal(gotValue) {
		differences, _ := gotValue.Diff(wantValue)
		t.Fatalf("state changed: %v", differences)
	}
}

func TestJWTKeyMappingResourceSafeReadProtocolSequences(t *testing.T) {
	ctx := context.Background()
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
		if request.Method != http.MethodGet || request.URL.RequestURI() != jwtKeyMappingInfoEndpoint(jwtMappingID1) {
			http.Error(writer, `{"detail":"unexpected route"}`, http.StatusBadRequest)
			return
		}
		switch currentMode {
		case "transient-success":
			if currentAttempt == 1 {
				writer.Header().Set("Retry-After", "0")
				http.Error(writer, `{"detail":"temporary-response-secret"}`, http.StatusServiceUnavailable)
				return
			}
			_ = json.NewEncoder(writer).Encode(jwtMappingJSON(jwtMappingID1, "refreshed-claim-secret", "refreshed-description-secret", true))
		case "exhaustion":
			writer.Header().Set("Retry-After", "0")
			http.Error(writer, `{"detail":"body-secret 404 not found"}`, http.StatusBadGateway)
		case "terminal-4xx":
			http.Error(writer, `{"detail":"404 not found body-secret"}`, http.StatusBadRequest)
		case "malformed":
			_, _ = writer.Write([]byte(`{"id":`))
		case "mismatch":
			_ = json.NewEncoder(writer).Encode(jwtMappingJSON(jwtMappingID2, "wrong-claim-secret", nil, true))
		case "404":
			http.Error(writer, `{"detail":"missing body-secret"}`, http.StatusNotFound)
		default:
			_ = json.NewEncoder(writer).Encode(jwtMappingJSON(jwtMappingID1, "refreshed-claim-secret", nil, true))
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_jwt_key_mapping"]
	prior := jwtMappingProtocolPriorState(t, schema)
	private, err := json.Marshal(map[string][]byte{jwtKeyMappingDescriptionOwnedPrivateKey: []byte("true")})
	if err != nil {
		t.Fatal(err)
	}
	read := func(readMode string) (*tfprotov6.ReadResourceResponse, int) {
		t.Helper()
		mu.Lock()
		mode = readMode
		attempt = 0
		mu.Unlock()
		response, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_jwt_key_mapping", CurrentState: prior, Private: private})
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
		var claimValue string
		if err := attributes["jwt_claim_value"].As(&claimValue); err != nil || claimValue != "refreshed-claim-secret" {
			t.Fatalf("claim=%q err=%v", claimValue, err)
		}
		if !bytes.Equal(response.Private, private) {
			t.Fatalf("private changed: got=%s want=%s", response.Private, private)
		}
	})

	for _, failureMode := range []string{"exhaustion", "terminal-4xx", "malformed", "mismatch"} {
		failureMode := failureMode
		t.Run(failureMode+" retains public and private state", func(t *testing.T) {
			response, calls := read(failureMode)
			wantCalls := 1
			if failureMode == "exhaustion" {
				wantCalls = defaultSafeReadRetryPolicy.maxAttempts
			}
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != wantCalls {
				t.Fatalf("calls=%d want=%d diagnostics=%s", calls, wantCalls, text)
			}
			assertJWTMappingProtocolStateEqual(t, schema, prior, response.NewState)
			if !bytes.Equal(response.Private, private) {
				t.Fatalf("private changed: got=%s want=%s", response.Private, private)
			}
			for _, forbidden := range []string{server.URL, jwtMappingID1, "prior-claim-secret", "prior-description-secret", "body-secret", "wrong-claim-secret", "jwt/key/mapping", "id="} {
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

func TestJWTKeyMappingDataSourceSafeReadFailurePublishesNoPartialStateProtocol(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		calls  int
	}{
		{name: "complete exact 404", status: http.StatusNotFound, body: `{"detail":"missing secret"}`, calls: 1},
		{name: "malformed JSON", status: http.StatusOK, body: `{"id":`, calls: 1},
		{name: "identity mismatch", status: http.StatusOK, body: `{"id":"22222222-2222-4222-8222-222222222222","jwt_claim_name":"sub","jwt_claim_value":"wrong-claim-secret","description":null,"is_active":true,"created_at":"2026-08-26T00:00:00Z","updated_at":"2026-08-26T00:01:00Z","created_by":null,"updated_by":null}`, calls: 1},
		{name: "retry exhaustion", status: http.StatusServiceUnavailable, body: `{"detail":"404 not found body-secret"}`, calls: defaultSafeReadRetryPolicy.maxAttempts},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				if test.status == http.StatusServiceUnavailable {
					writer.Header().Set("Retry-After", "0")
				}
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.DataSourceSchemas["litellm_jwt_key_mapping"]
			config := singularPresenceConfig(t, schema, map[string]interface{}{"id": jwtMappingID1})
			response, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_jwt_key_mapping", Config: config})
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls.Load() != int32(test.calls) {
				t.Fatalf("err=%v calls=%d want=%d diagnostics=%s", err, calls.Load(), test.calls, text)
			}
			assertSingularPresenceStateUnchanged(t, schema, config, response.State)
			for _, forbidden := range []string{server.URL, jwtMappingID1, "missing secret", "body-secret", "wrong-claim-secret", "jwt/key/mapping", "id="} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}
}
