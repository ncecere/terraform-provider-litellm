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

func promptSafeReadProtocolPriorState(t *testing.T, schema *tfprotov6.Schema, id, environment string) *tfprotov6.DynamicValue {
	t.Helper()
	value := organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"id":                                    id,
		"prompt_id":                             id,
		"prompt_integration":                    "dotprompt",
		"api_base":                              "https://prior.invalid/private",
		"api_key":                               "prior-api-key-secret",
		"provider_specific_query_params":        `{ "region" : "east" }`,
		"ignore_prompt_manager_model":           false,
		"ignore_prompt_manager_optional_params": false,
		"dotprompt_content":                     "prior-content-secret",
		"prompt_type":                           "db",
		"environment":                           environment,
		"version":                               int64(2),
		"created_at":                            "2025-01-01T00:00:00Z",
		"updated_at":                            "2025-01-02T00:00:00Z",
	})
	return accessGroupProtocolDynamicValue(t, schema, value)
}

func TestPromptResourceSafeReadProtocolSequences(t *testing.T) {
	ctx := context.Background()
	const id, environment = "prompt-protocol", "production"
	var mu sync.Mutex
	mode := "success"
	infoAttempts, versionAttempts := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		mu.Lock()
		currentMode := mode
		isVersions := request.URL.Path == "/prompts/"+id+"/versions"
		if isVersions {
			versionAttempts++
		} else {
			infoAttempts++
		}
		currentInfoAttempt := infoAttempts
		mu.Unlock()
		if request.URL.Query().Get("environment") != environment {
			http.Error(writer, `{"detail":"unexpected-environment-secret"}`, http.StatusBadRequest)
			return
		}
		if isVersions {
			switch currentMode {
			case "absence-400", "absence-404":
				http.Error(writer, `{"detail":"version-missing-body-secret"}`, http.StatusNotFound)
			case "versions-nonempty":
				_, _ = writer.Write([]byte(`{"prompts":[{"prompt_id":"prompt-protocol"}]}`))
			case "versions-empty":
				_, _ = writer.Write([]byte(`{"prompts":[]}`))
			case "versions-error":
				http.Error(writer, `{"detail":"versions-body-secret"}`, http.StatusServiceUnavailable)
			default:
				http.Error(writer, `{"detail":"unexpected-versions-secret"}`, http.StatusInternalServerError)
			}
			return
		}
		if request.Method != http.MethodGet || request.URL.Path != "/prompts/"+id {
			http.Error(writer, `{"detail":"unexpected-route-secret"}`, http.StatusBadRequest)
			return
		}
		switch currentMode {
		case "transient-success":
			if currentInfoAttempt == 1 {
				writer.Header().Set("Retry-After", "0")
				http.Error(writer, `{"detail":"temporary-body-secret"}`, http.StatusServiceUnavailable)
				return
			}
			_, _ = writer.Write(promptSafeReadBody(t, id, environment))
		case "exhaustion":
			writer.Header().Set("Retry-After", "0")
			http.Error(writer, `{"detail":"body-secret 404 not found"}`, http.StatusBadGateway)
		case "terminal-403":
			http.Error(writer, `{"detail":"404 not found body-secret"}`, http.StatusForbidden)
		case "malformed":
			_, _ = writer.Write([]byte(`{"prompt_spec":`))
		case "mismatch":
			_, _ = writer.Write(promptSafeReadBody(t, "other-prompt-secret", environment))
		case "malformed-late":
			_, _ = fmt.Fprintf(writer, `{"prompt_spec":{"prompt_id":%q,"environment":%q,"version":3,"created_at":"new-time","litellm_params":{"prompt_integration":"dotprompt","api_base":"new-base","ignore_prompt_manager_model":true},"prompt_info":{"prompt_type":7,"environment":%q}}}`, id, environment, environment)
		case "absence-400", "versions-nonempty", "versions-empty", "versions-error":
			http.Error(writer, `{"detail":"ambiguous-absence-body-secret"}`, http.StatusBadRequest)
		case "absence-404":
			http.Error(writer, `{"detail":"route-absence-body-secret"}`, http.StatusNotFound)
		default:
			_, _ = writer.Write(promptSafeReadBody(t, id, environment))
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_prompt"]
	prior := promptSafeReadProtocolPriorState(t, schema, id, environment)
	private, _ := json.Marshal(map[string][]byte{promptImportedPrivateKey: []byte("true"), "prompt_private": []byte(`"private-state-secret"`)})
	read := func(readMode string) (*tfprotov6.ReadResourceResponse, int, int) {
		t.Helper()
		mu.Lock()
		mode, infoAttempts, versionAttempts = readMode, 0, 0
		mu.Unlock()
		response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_prompt", CurrentState: prior, Private: private})
		if err != nil {
			t.Fatalf("mode=%s err=%v", readMode, err)
		}
		mu.Lock()
		defer mu.Unlock()
		return response, infoAttempts, versionAttempts
	}

	t.Run("transient then success projects once", func(t *testing.T) {
		response, infoCalls, versionCalls := read("transient-success")
		if accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || infoCalls != 2 || versionCalls != 0 {
			t.Fatalf("info=%d versions=%d diagnostics=%s", infoCalls, versionCalls, agentProtocolDiagnosticsText(response.Diagnostics))
		}
		attributes := protocolAttributeMap(t, schema, response.NewState)
		if version := protocolInt64(t, attributes["version"]); version != 3 {
			t.Fatalf("version=%d", version)
		}
		var apiKey string
		if err := attributes["api_key"].As(&apiKey); err != nil || apiKey != "prior-api-key-secret" {
			t.Fatalf("api_key changed err=%v", err)
		}
		if protocolPrivateHasKey(t, response.Private, promptImportedPrivateKey) || !protocolPrivateHasKey(t, response.Private, "prompt_private") {
			t.Fatal("successful refresh did not clear only the import marker")
		}
	})

	for _, failureMode := range []string{"exhaustion", "terminal-403", "malformed", "mismatch", "malformed-late", "absence-400", "absence-404", "versions-nonempty", "versions-empty", "versions-error"} {
		failureMode := failureMode
		t.Run(failureMode+" retains exact state", func(t *testing.T) {
			response, infoCalls, versionCalls := read(failureMode)
			wantInfo, wantVersions := 1, 0
			if failureMode == "exhaustion" {
				wantInfo = defaultSafeReadRetryPolicy.maxAttempts
			}
			text := agentProtocolDiagnosticsText(response.Diagnostics)
			if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || infoCalls != wantInfo || versionCalls != wantVersions {
				t.Fatalf("info=%d/%d versions=%d/%d diagnostics=%s", infoCalls, wantInfo, versionCalls, wantVersions, text)
			}
			assertPromptRawStateUnchanged(t, prior, response.NewState)
			if !protocolPrivateHasKey(t, response.Private, promptImportedPrivateKey) || !protocolPrivateHasKey(t, response.Private, "prompt_private") {
				t.Fatal("failed refresh changed private state")
			}
			for _, forbidden := range []string{server.URL, id, environment, "prior-api-key-secret", "prior-content-secret", "private-state-secret", "body-secret", "other-prompt-secret", "prompt_spec", "prompt_type", "Retry-After"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("diagnostic exposed %q: %s", forbidden, text)
				}
			}
		})
	}
}

func TestPromptSafeReadCancellationRetainsProtocolState(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		once.Do(func() { close(started) })
		<-request.Context().Done()
	}))
	defer server.Close()
	baseCtx := context.Background()
	protocolServer, schemas := configuredImportProtocolServer(t, baseCtx, server.URL)
	schema := schemas.ResourceSchemas["litellm_prompt"]
	prior := promptSafeReadProtocolPriorState(t, schema, "prompt-cancel", "production")
	private, _ := json.Marshal(map[string][]byte{promptImportedPrivateKey: []byte("true")})
	readCtx, cancel := context.WithCancel(context.Background())
	go func() { <-started; cancel() }()
	defer cancel()
	response, err := protocolServer.ReadResource(readCtx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_prompt", CurrentState: prior, Private: private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
		t.Fatalf("err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
	}
	assertPromptRawStateUnchanged(t, prior, response.NewState)
	if !protocolPrivateHasKey(t, response.Private, promptImportedPrivateKey) {
		t.Fatal("canceled refresh changed import marker")
	}
}

func TestPromptDataSourceTransientFailureRemainsSingleAttemptProtocol(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Retry-After", "0")
		http.Error(writer, `{"detail":"temporary-body-secret"}`, http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	protocolServer, schemas := configuredImportProtocolServer(t, context.Background(), server.URL)
	schema := schemas.DataSourceSchemas["litellm_prompt"]
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"prompt_id": "prompt-data", "environment": "production"}))
	response, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_prompt", Config: config})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d diagnostics=%s", err, calls.Load(), agentProtocolDiagnosticsText(response.Diagnostics))
	}
}

func assertPromptProtocolStateBytesEqual(t *testing.T, left, right *tfprotov6.DynamicValue) {
	t.Helper()
	if !bytes.Equal(left.MsgPack, right.MsgPack) || !bytes.Equal(left.JSON, right.JSON) {
		t.Fatal("protocol state bytes differ")
	}
}
