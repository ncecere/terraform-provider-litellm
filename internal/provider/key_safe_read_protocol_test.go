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

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func keySafeReadProtocolPriorState(t *testing.T, schema *tfprotov6.Schema, raw string) *tfprotov6.DynamicValue {
	t.Helper()
	value := keyNumericProtocolValue(t, schema, false, map[string]tftypes.Value{
		"id":               tftypes.NewValue(tftypes.String, hashKeyForID(raw)),
		"key":              tftypes.NewValue(tftypes.String, raw),
		"key_alias":        tftypes.NewValue(tftypes.String, "prior-alias-secret"),
		"models":           tftypes.NewValue(tftypes.List{ElementType: tftypes.String}, []tftypes.Value{tftypes.NewValue(tftypes.String, "prior-model-secret")}),
		"blocked":          tftypes.NewValue(tftypes.Bool, false),
		"metadata_json":    tftypes.NewValue(tftypes.String, nil),
		"config_json":      tftypes.NewValue(tftypes.String, nil),
		"permissions_json": tftypes.NewValue(tftypes.String, nil),
	})
	return keyNumericProtocolDynamic(t, schema, value)
}

func assertKeyRawStateUnchanged(t *testing.T, want, got *tfprotov6.DynamicValue) {
	t.Helper()
	if want == nil || got == nil || !bytes.Equal(want.MsgPack, got.MsgPack) || !bytes.Equal(want.JSON, got.JSON) {
		t.Fatal("public raw state changed")
	}
}

func TestKeyResourceSafeReadProtocolSequences(t *testing.T) {
	ctx := context.Background()
	const raw = "sk-protocol-key"
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
		if request.Method != http.MethodGet || request.URL.Path != "/key/info" || request.URL.Query().Get("key") != raw {
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
			_, _ = writer.Write(keySafeReadBody(t, raw, true))
		case "exhaustion", "accepted-exhaustion":
			writer.Header().Set("Retry-After", "0")
			http.Error(writer, `{"detail":"body-secret 404 not found"}`, http.StatusBadGateway)
		case "terminal-4xx":
			http.Error(writer, `{"detail":"404 not found body-secret Retry-After"}`, http.StatusBadRequest)
		case "malformed":
			_, _ = writer.Write([]byte(`{"key":`))
		case "missing-info":
			_, _ = fmt.Fprintf(writer, `{"key":%q}`, raw)
		case "mismatch":
			_, _ = writer.Write(keySafeReadBody(t, "wrong-key-secret", true))
		case "malformed-scalar":
			_, _ = fmt.Fprintf(writer, `{"key":%q,"info":{"blocked":"true"}}`, raw)
		case "404":
			http.Error(writer, `{"detail":"missing body-secret"}`, http.StatusNotFound)
		default:
			_, _ = writer.Write(keySafeReadBody(t, raw, true))
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_key"]
	prior := keySafeReadProtocolPriorState(t, schema, raw)
	private, _ := json.Marshal(map[string][]byte{numericImportedPrivateKey: []byte("true"), "key_private": []byte(`"private-state-secret"`)})
	read := func(readMode string, priorPrivate []byte) (*tfprotov6.ReadResourceResponse, int) {
		t.Helper()
		mu.Lock()
		mode, attempt = readMode, 0
		mu.Unlock()
		response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_key", CurrentState: prior, Private: priorPrivate})
		if err != nil {
			t.Fatalf("mode=%s err=%v", readMode, err)
		}
		mu.Lock()
		defer mu.Unlock()
		return response, attempt
	}

	t.Run("transient then success projects once", func(t *testing.T) {
		response, calls := read("transient-success", private)
		if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != 2 {
			t.Fatalf("calls=%d diagnostics=%s", calls, agentProtocolDiagnosticsText(response.Diagnostics))
		}
		attributes := protocolAttributeMap(t, schema, response.NewState)
		var alias, key string
		if err := attributes["key_alias"].As(&alias); err != nil || alias != "refreshed-alias" {
			t.Fatalf("alias=%q err=%v", alias, err)
		}
		if err := attributes["key"].As(&key); err != nil || key != raw {
			t.Fatalf("key changed: %q err=%v", key, err)
		}
		if protocolPrivateHasKey(t, response.Private, numericImportedPrivateKey) || !protocolPrivateHasKey(t, response.Private, "key_private") {
			t.Fatal("successful refresh did not clear only numeric marker")
		}
	})

	for _, failureMode := range []string{"exhaustion", "terminal-4xx", "malformed", "missing-info", "mismatch", "malformed-scalar"} {
		failureMode := failureMode
		t.Run(failureMode+" retains exact state", func(t *testing.T) {
			response, calls := read(failureMode, private)
			wantCalls := 1
			if failureMode == "exhaustion" {
				wantCalls = defaultSafeReadRetryPolicy.maxAttempts
			}
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != wantCalls {
				t.Fatalf("calls=%d want=%d diagnostics=%s", calls, wantCalls, text)
			}
			assertKeyRawStateUnchanged(t, prior, response.NewState)
			if !protocolPrivateHasKey(t, response.Private, numericImportedPrivateKey) || !protocolPrivateHasKey(t, response.Private, "key_private") {
				t.Fatal("failed refresh changed private state")
			}
			for _, forbidden := range []string{server.URL, raw, "prior-alias-secret", "prior-model-secret", "private-state-secret", "body-secret", "wrong-key-secret", "key_alias", "blocked", "Retry-After"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}

	t.Run("accepted recovery remains fresh and single attempt", func(t *testing.T) {
		accepted, _ := json.Marshal(map[string][]byte{keyAcceptedCreateRecoveryPrivateKey: []byte("true")})
		response, calls := read("accepted-exhaustion", accepted)
		if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls != 1 {
			t.Fatalf("calls=%d diagnostics=%s", calls, agentProtocolDiagnosticsText(response.Diagnostics))
		}
		assertKeyRawStateUnchanged(t, prior, response.NewState)
		if !protocolPrivateHasKey(t, response.Private, keyAcceptedCreateRecoveryPrivateKey) {
			t.Fatal("accepted recovery marker changed")
		}
	})

	t.Run("exact 404 removes after one attempt", func(t *testing.T) {
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

func TestKeyDataSourceSafeReadProtocolSequences(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		calls      int
	}{
		{name: "transient success", mode: "transient", calls: 2},
		{name: "exact 404", mode: "404", calls: 1},
		{name: "misleading 400", mode: "400", calls: 1},
		{name: "missing info", mode: "missing", calls: 1},
		{name: "mismatch", mode: "mismatch", calls: 1},
		{name: "malformed scalar", mode: "malformed", calls: 1},
		{name: "exhaustion", mode: "exhaustion", calls: defaultSafeReadRetryPolicy.maxAttempts},
	} {
		t.Run(test.name, func(t *testing.T) {
			const raw = "sk-data-source"
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
					_, _ = writer.Write(keySafeReadBody(t, raw, true))
				case "404":
					http.Error(writer, `{"detail":"missing-body-secret"}`, http.StatusNotFound)
				case "400":
					http.Error(writer, `{"detail":"404 not found body-secret"}`, http.StatusBadRequest)
				case "missing":
					_, _ = fmt.Fprintf(writer, `{"key":%q}`, raw)
				case "mismatch":
					_, _ = writer.Write(keySafeReadBody(t, "wrong-key-secret", true))
				case "malformed":
					_, _ = fmt.Fprintf(writer, `{"key":%q,"info":{"key_alias":7}}`, raw)
				case "exhaustion":
					writer.Header().Set("Retry-After", "0")
					http.Error(writer, `{"detail":"body-secret"}`, http.StatusServiceUnavailable)
				}
			}))
			defer server.Close()
			ctx := context.Background()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.DataSourceSchemas["litellm_key"]
			config := singularPresenceConfig(t, schema, map[string]interface{}{"key": raw})
			response, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_key", Config: config})
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if test.mode == "transient" {
				if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls.Load() != int32(test.calls) {
					t.Fatalf("err=%v calls=%d diagnostics=%s", err, calls.Load(), text)
				}
				return
			}
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls.Load() != int32(test.calls) {
				t.Fatalf("err=%v calls=%d want=%d diagnostics=%s", err, calls.Load(), test.calls, text)
			}
			assertSingularPresenceStateUnchanged(t, schema, config, response.State)
			for _, forbidden := range []string{server.URL, raw, "body-secret", "wrong-key-secret", "key_alias"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}
}

func TestKeySafeReadCancellationAndDeadlineRetainProtocolState(t *testing.T) {
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
				schema := schemas.DataSourceSchemas["litellm_key"]
				config := singularPresenceConfig(t, schema, map[string]interface{}{"key": "sk-deadline-key"})
				response, err := protocolServer.ReadDataSource(readCtx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_key", Config: config})
				if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
					t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
				}
				assertSingularPresenceStateUnchanged(t, schema, config, response.State)
				return
			}
			schema := schemas.ResourceSchemas["litellm_key"]
			prior := keySafeReadProtocolPriorState(t, schema, "sk-cancel-key")
			private, _ := json.Marshal(map[string][]byte{numericImportedPrivateKey: []byte("true")})
			response, err := protocolServer.ReadResource(readCtx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_key", CurrentState: prior, Private: private})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
				t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
			}
			assertKeyRawStateUnchanged(t, prior, response.NewState)
			if !protocolPrivateHasKey(t, response.Private, numericImportedPrivateKey) {
				t.Fatal("canceled refresh changed private marker")
			}
		})
	}
}

func TestKeyHashValidationDiagnosticsRedactProtocol(t *testing.T) {
	ctx := context.Background()
	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemas, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemas.Diagnostics) {
		t.Fatalf("schemas: err=%v diagnostics=%v", err, schemas.Diagnostics)
	}
	malformed := "sha256:malformed-sensitive-hash-value"

	dataSourceSchema := schemas.DataSourceSchemas["litellm_key"]
	dataSourceConfig := keyNumericProtocolValue(t, dataSourceSchema, false, map[string]tftypes.Value{
		"key_hash": tftypes.NewValue(tftypes.String, malformed),
	})
	validatedDataSource, err := protocolServer.ValidateDataResourceConfig(ctx, &tfprotov6.ValidateDataResourceConfigRequest{
		TypeName: "litellm_key",
		Config:   keyNumericProtocolDynamic(t, dataSourceSchema, dataSourceConfig),
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(validatedDataSource.Diagnostics) {
		t.Fatalf("data source validation: err=%v diagnostics=%v", err, validatedDataSource.Diagnostics)
	}
	if text := agentProtocolDiagnosticsText(validatedDataSource.Diagnostics); strings.Contains(text, malformed) {
		t.Fatalf("data source diagnostic exposed hash: %s", text)
	}

	resourceSchema := schemas.ResourceSchemas["litellm_key_block"]
	resourceConfig := organizationProjectProtocolValue(t, resourceSchema, map[string]interface{}{"key_hash": malformed})
	validatedResource, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{
		TypeName: "litellm_key_block",
		Config:   accessGroupProtocolDynamicValue(t, resourceSchema, resourceConfig),
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(validatedResource.Diagnostics) {
		t.Fatalf("resource validation: err=%v diagnostics=%v", err, validatedResource.Diagnostics)
	}
	if text := agentProtocolDiagnosticsText(validatedResource.Diagnostics); strings.Contains(text, malformed) {
		t.Fatalf("resource diagnostic exposed hash: %s", text)
	}
}

func TestKeyUppercaseHashImportCanonicalizesManagementIdentity(t *testing.T) {
	ctx := context.Background()
	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemas, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemas.Diagnostics) {
		t.Fatalf("schemas: err=%v diagnostics=%v", err, schemas.Diagnostics)
	}
	lower := strings.TrimPrefix(hashKeyForID("sk-import-canonical"), "sha256:")
	upperID := "sha256:" + strings.ToUpper(lower)
	response, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_key", ID: upperID})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || len(response.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, response.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schemas.ResourceSchemas["litellm_key"], response.ImportedResources[0].State)
	var got string
	if err := attributes["id"].As(&got); err != nil || got != "sha256:"+lower {
		t.Fatalf("id=%q err=%v", got, err)
	}
}

func TestKeyBlockSafeReadProtocolSequences(t *testing.T) {
	const raw = "sk-block-protocol"
	bare := strings.TrimPrefix(hashKeyForID(raw), "sha256:")
	for _, test := range []struct {
		name, mode string
		calls      int
		removed    bool
	}{
		{name: "transient success", mode: "transient", calls: 2},
		{name: "exact 404", mode: "404", calls: 1, removed: true},
		{name: "unblocked", mode: "unblocked", calls: 1, removed: true},
		{name: "missing info", mode: "missing", calls: 1},
		{name: "malformed blocked", mode: "malformed", calls: 1},
		{name: "exhaustion", mode: "exhaustion", calls: defaultSafeReadRetryPolicy.maxAttempts},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				attempt := calls.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				switch test.mode {
				case "transient":
					if attempt == 1 {
						writer.Header().Set("Retry-After", "0")
						http.Error(writer, `{}`, http.StatusServiceUnavailable)
						return
					}
					_, _ = writer.Write(keySafeReadBody(t, bare, true))
				case "404":
					http.Error(writer, `{}`, http.StatusNotFound)
				case "unblocked":
					_, _ = writer.Write(keySafeReadBody(t, bare, false))
				case "missing":
					_, _ = fmt.Fprintf(writer, `{"key":%q}`, bare)
				case "malformed":
					_, _ = fmt.Fprintf(writer, `{"key":%q,"info":{"blocked":"true"}}`, bare)
				case "exhaustion":
					writer.Header().Set("Retry-After", "0")
					http.Error(writer, `{}`, http.StatusBadGateway)
				}
			}))
			defer server.Close()
			ctx := context.Background()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_key_block"]
			prior := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"id": hashKeyForID(raw), "key": raw, "blocked": true}))
			response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_key_block", CurrentState: prior})
			if err != nil || calls.Load() != int32(test.calls) {
				t.Fatalf("err=%v calls=%d diagnostics=%s", err, calls.Load(), agentProtocolDiagnosticsText(response.Diagnostics))
			}
			value, _ := response.NewState.Unmarshal(schema.ValueType())
			if test.removed {
				if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || !value.IsNull() {
					t.Fatalf("state=%v diagnostics=%s", value, agentProtocolDiagnosticsText(response.Diagnostics))
				}
				return
			}
			if test.mode == "transient" {
				if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || value.IsNull() {
					t.Fatalf("state=%v diagnostics=%s", value, agentProtocolDiagnosticsText(response.Diagnostics))
				}
				return
			}
			if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
				t.Fatalf("missing diagnostic")
			}
			assertKeyRawStateUnchanged(t, prior, response.NewState)
		})
	}
}
