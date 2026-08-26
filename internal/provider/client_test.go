package provider

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"syscall"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

type failingReadCloser struct{ err error }

func (r failingReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (r failingReadCloser) Close() error             { return nil }

type temporaryTransportTestError struct{}

func (temporaryTransportTestError) Error() string   { return "temporary detail must be discarded" }
func (temporaryTransportTestError) Timeout() bool   { return false }
func (temporaryTransportTestError) Temporary() bool { return true }

func TestClientPreservesGenericJSONNumbers(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"items":[{"limit":9007199254740993,"cost":12.5}],"maximum":9223372036854775807}`))
	}))
	defer server.Close()

	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
	var result map[string]interface{}
	if err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/limits", nil, &result); err != nil {
		t.Fatal(err)
	}
	items := result["items"].([]interface{})
	item := items[0].(map[string]interface{})
	if got, err := exactInt64FromAPI(item["limit"]); err != nil || got != 9007199254740993 {
		t.Fatalf("nested limit = %#v, %v", item["limit"], err)
	}
	if got, err := float64FromAPI(item["cost"]); err != nil || got != 12.5 {
		t.Fatalf("cost = %#v", item["cost"])
	}
	if got, err := exactInt64FromAPI(result["maximum"]); err != nil || got != math.MaxInt64 {
		t.Fatalf("maximum = %#v, %v", result["maximum"], err)
	}
}

func TestClientRequestPreservesInt64(t *testing.T) {
	t.Parallel()

	observed := make(chan map[string]interface{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]interface{}
		decoder := json.NewDecoder(request.Body)
		decoder.UseNumber()
		if err := decoder.Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		observed <- body
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
	request := map[string]interface{}{
		"minimum": math.MinInt64,
		"maximum": math.MaxInt64,
		"nested":  map[string]int64{"above_safe_float": 9007199254740993},
	}
	if err := client.DoRequestWithResponse(context.Background(), http.MethodPost, "/limits", request, nil); err != nil {
		t.Fatal(err)
	}
	body := <-observed
	for field, want := range map[string]string{"minimum": "-9223372036854775808", "maximum": "9223372036854775807"} {
		if got, ok := body[field].(json.Number); !ok || got.String() != want {
			t.Fatalf("%s = %#v, want %s", field, body[field], want)
		}
	}
	nested := body["nested"].(map[string]interface{})
	if got, ok := nested["above_safe_float"].(json.Number); !ok || got.String() != "9007199254740993" {
		t.Fatalf("nested integer = %#v", nested["above_safe_float"])
	}
}

func TestClientSensitiveEchoResponsesAreOmitted(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		path    string
		body    interface{}
		secrets []string
	}{
		{"key", "/key/generate", map[string]interface{}{"key": "sk-echo-key"}, []string{"sk-echo-key"}},
		{"model", "/model/new", map[string]interface{}{"litellm_params": map[string]interface{}{"api_key": "model-secret", "aws_secret_access_key": "aws-secret"}}, []string{"model-secret", "aws-secret"}},
		{"credential", "/credentials", map[string]interface{}{"credential_values": map[string]interface{}{"custom": "credential-secret"}}, []string{"credential-secret"}},
		{"agent", "/v1/agents", map[string]interface{}{"static_headers": map[string]interface{}{"Authorization": "Bearer agent-secret"}}, []string{"agent-secret"}},
		{"mcp", "/v1/mcp/server", map[string]interface{}{"credentials": map[string]interface{}{"custom": "mcp-secret"}}, []string{"mcp-secret"}},
		{"metadata", "/team/new", map[string]interface{}{"metadata": map[string]interface{}{"custom": "metadata-secret"}}, []string{"metadata-secret"}},
		{"query token", "/user/info?token=query-secret", nil, []string{"query-secret"}},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			secret := test.name + "-sentinel"
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requestBody := map[string]interface{}{}
				_ = json.NewDecoder(request.Body).Decode(&requestBody)
				writer.Header().Set("Content-Type", "application/json")
				writer.Header().Set("X-Request-ID", "req-safe-123")
				writer.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{
					"detail": map[string]interface{}{
						"echo":          requestBody,
						"url":           request.URL.String(),
						"authorization": "Bearer " + secret,
					},
				})
			}))
			defer server.Close()

			client := &Client{APIBase: server.URL, APIKey: "admin-secret", HTTPClient: server.Client()}
			err := client.DoRequestWithResponse(context.Background(), http.MethodPost, test.path, test.body, nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
				t.Fatalf("error = %#v, want typed HTTP 400", err)
			}
			if apiErr.RequestID != "" || !apiErr.DetailOmitted {
				t.Fatalf("sensitive request retained request metadata: %#v", apiErr)
			}
			combined := apiErr.Error() + apiErr.Detail + apiErr.Body
			for _, forbidden := range append([]string{secret, "admin-secret", test.path}, test.secrets...) {
				if forbidden != "" && strings.Contains(combined, forbidden) {
					t.Fatalf("safe error exposed %q: %q", forbidden, combined)
				}
			}
		})
	}
}

func TestSanitizeDiagnosticValuePreservesOnlyBoundedJSONNumbers(t *testing.T) {
	t.Parallel()

	for _, number := range []json.Number{"9007199254740993", "-1.25e+17"} {
		sanitized, ok := sanitizeDiagnosticValue(number, "limit", 0, nil)
		if !ok || sanitized != number {
			t.Fatalf("sanitizeDiagnosticValue(%q) = %#v, %t", number, sanitized, ok)
		}
	}
	for _, number := range []json.Number{"not-a-number", json.Number(strings.Repeat("9", maxDiagnosticNumber+1))} {
		if sanitized, ok := sanitizeDiagnosticValue(number, "limit", 0, nil); ok || sanitized != nil {
			t.Fatalf("unsafe json.Number %q was retained: %#v", number, sanitized)
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"detail":{"limit":9007199254740993,"ratio":1.25e3}}`))
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/team/info", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !strings.Contains(apiErr.Detail, `9007199254740993`) || !strings.Contains(apiErr.Detail, `1.25e3`) {
		t.Fatalf("exact bounded numeric detail was not preserved: %#v", err)
	}
}

func TestClientFreshCredentialProbePreservesRedaction(t *testing.T) {
	t.Parallel()
	const (
		nameSecret = "credential-name-secret"
		apiSecret  = "admin-secret"
	)
	closed := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		closed = request.Close
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"detail":        nameSecret,
			"request_url":   request.URL.String(),
			"authorization": request.Header.Get("Authorization"),
		})
	}))
	defer server.Close()

	client := &Client{APIBase: server.URL, APIKey: apiSecret, HTTPClient: server.Client()}
	err := client.doFreshRequestWithResponse(context.Background(), http.MethodGet, "/credentials/by_name/"+nameSecret, nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest || !closed {
		t.Fatalf("fresh probe contract: close=%t error=%#v", closed, err)
	}
	combined := apiErr.Error() + apiErr.Detail + apiErr.Body
	for _, secret := range []string{nameSecret, apiSecret, server.URL} {
		if strings.Contains(combined, secret) {
			t.Fatalf("fresh probe leaked %q in %q", secret, combined)
		}
	}
}

func TestClientFreshCredentialProbeRejectsUnverifiableCustomTransport(t *testing.T) {
	t.Parallel()
	calls := 0
	client := &Client{
		APIBase: "https://example.invalid",
		APIKey:  "admin",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("must not execute")
		})},
	}
	err := client.doFreshRequestWithResponse(context.Background(), http.MethodGet, "/credentials/by_name/test", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "fresh LiteLLM connection is unavailable") || calls != 0 {
		t.Fatalf("custom transport freshness did not fail closed: calls=%d error=%v", calls, err)
	}
}

func TestSanitizeDiagnosticStringRedactsCompleteKnownSecretBeforePatterns(t *testing.T) {
	t.Parallel()

	secret := "sk-abcd!highEntropyTail"
	diagnostic := sanitizeDiagnosticString("echo="+secret, []string{secret})
	if strings.Contains(diagnostic, "highEntropyTail") || diagnostic != "echo=[REDACTED]" {
		t.Fatalf("known secret was only partially redacted: %q", diagnostic)
	}
}

func TestClientSanitizesSecretsInStructuredDetailKeys(t *testing.T) {
	t.Parallel()

	secret := "sk-response-secret-key"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"detail":{"` + secret + `":"x","safe":"ok"}}`))
	}))
	defer server.Close()

	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/team/list", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T: %v", err, err)
	}
	for _, diagnostic := range []string{apiErr.Detail, apiErr.Body, apiErr.Error()} {
		if strings.Contains(diagnostic, secret) || !strings.Contains(diagnostic, "[REDACTED]") {
			t.Fatalf("structured key was not redacted: %q", diagnostic)
		}
	}
}

func TestClientSensitiveErrorDoesNotInferNotFoundFromBody(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"code":404,"detail":"opaque"}`))
	}))
	defer server.Close()

	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/key/info", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || IsNotFoundError(err) || apiErr.Detail != "" || apiErr.Body != "" {
		t.Fatalf("numeric response content affected exact status classification: %#v", err)
	}
}

func TestClientSensitivePathClassificationIgnoresAPIBasePrefix(t *testing.T) {
	t.Parallel()

	secret := "sk-prefixed-base-secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("X-Request-ID", "req-prefixed")
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = writer.Write([]byte(`{"detail":"` + secret + `"}`))
	}))
	defer server.Close()

	client := &Client{APIBase: server.URL + "/litellm", APIKey: "admin", HTTPClient: server.Client()}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/key/list", nil, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Detail != "" || apiErr.Body != "" || apiErr.RequestID != "" {
		t.Fatalf("prefixed sensitive endpoint was not suppressed: %#v", err)
	}
	if strings.Contains(apiErr.Error(), secret) {
		t.Fatalf("prefixed endpoint exposed secret: %v", apiErr)
	}
}

func TestClientSanitizesNonSensitiveStructuredDetail(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusConflict)
		_, _ = writer.Write([]byte(`{"error":{"message":"team already exists","headers":{"X-Custom":"header-secret"},"token":"sk-response-secret","nested":{"safe":"visible"}}}`))
	}))
	defer server.Close()

	client := &Client{APIBase: server.URL, APIKey: "admin-secret", HTTPClient: server.Client()}
	err := client.DoRequestWithResponse(context.Background(), http.MethodPost, "/team/new", map[string]interface{}{"team_alias": "safe"}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %#v, want APIError", err)
	}
	if !strings.Contains(apiErr.Detail, "team already exists") || !strings.Contains(apiErr.Detail, "visible") {
		t.Fatalf("safe detail lost useful fields: %q", apiErr.Detail)
	}
	for _, secret := range []string{"header-secret", "sk-response-secret", "admin-secret"} {
		if strings.Contains(apiErr.Error()+apiErr.Detail+apiErr.Body, secret) {
			t.Fatalf("detail exposed %q: %#v", secret, apiErr)
		}
	}
	if apiErr.Body != apiErr.Detail || !strings.Contains(apiErr.Detail, "[REDACTED]") {
		t.Fatalf("deprecated Body must contain only sanitized detail: %#v", apiErr)
	}
}

func TestAPIErrorNeverRendersLegacyBody(t *testing.T) {
	t.Parallel()

	err := &APIError{StatusCode: http.StatusBadRequest, Body: "sk-legacy-secret"}
	if strings.Contains(err.Error(), err.Body) {
		t.Fatalf("APIError rendered deprecated Body: %q", err.Error())
	}
}

func TestSuppressedErrorsRetainTypedClassifications(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		path     string
		body     string
		notFound bool
		fallback bool
	}{
		{"not found", "/key/info?key=secret", `{"error":{"message":"Key not found"}}`, false, false},
		{"fallback propagation", "/fallback", `{"detail":"Invalid fallback models: model-a not found in router"}`, false, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
			err := client.DoRequestWithResponse(context.Background(), http.MethodGet, test.path, nil, nil)
			if IsNotFoundError(err) != test.notFound || isFallbackNotReadyError(err) != test.fallback {
				t.Fatalf("classification: notFound=%t fallback=%t error=%v", IsNotFoundError(err), isFallbackNotReadyError(err), err)
			}
		})
	}
}

func TestClientTransportErrorOmitsURLAndCause(t *testing.T) {
	t.Parallel()

	if err := safeTransportFailure(context.Canceled); !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("safe transport error did not preserve cancellation safely: %v", err)
	}
	rawURLCause := &url.Error{Op: "Get", URL: "https://example.invalid/key/info?key=raw-secret", Err: context.Canceled}
	safeCanceled := safeTransportFailure(rawURLCause)
	var retainedURLCause *url.Error
	if !errors.Is(safeCanceled, context.Canceled) || errors.As(safeCanceled, &retainedURLCause) || strings.Contains(safeCanceled.Error(), "raw-secret") {
		t.Fatalf("safe cancellation retained URL cause: %#v", safeCanceled)
	}

	secret := "query-secret"
	client := &Client{
		APIBase: "https://example.invalid",
		APIKey:  "admin-secret",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("dial failed for %s with admin-secret", request.URL.String())
		})},
	}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/key/info?key="+url.QueryEscape(secret), nil, nil)
	if err == nil {
		t.Fatal("expected transport error")
	}
	message := err.Error()
	for _, forbidden := range []string{secret, "admin-secret", "example.invalid", "/key/info", "key="} {
		if strings.Contains(message, forbidden) {
			t.Fatalf("transport error exposed %q: %q", forbidden, message)
		}
	}
	if !strings.Contains(message, "transport request failed") {
		t.Fatalf("unexpected safe transport error: %q", message)
	}
	if unwrapped := errors.Unwrap(err); unwrapped != nil {
		t.Fatalf("generic transport error retained raw cause: %v", unwrapped)
	}
}

func TestClientTransportRetryCategoryIsSafeAndNarrow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cause     error
		category  safeTransportRetryCategory
		retryable bool
	}{
		{name: "deadline", cause: context.DeadlineExceeded, category: safeTransportRetryTimeout, retryable: true},
		{name: "temporary", cause: temporaryTransportTestError{}, category: safeTransportRetryTemporary, retryable: true},
		{name: "connection reset", cause: fmt.Errorf("wrapped: %w", syscall.ECONNRESET), category: safeTransportRetryConnectionReset, retryable: true},
		{name: "early EOF", cause: io.ErrUnexpectedEOF, category: safeTransportRetryConnectionReset, retryable: true},
		{name: "certificate", cause: x509.UnknownAuthorityError{}, category: safeTransportRetryNone},
		{name: "configuration", cause: errors.New("invalid proxy configuration with secret"), category: safeTransportRetryNone},
		{name: "cancellation", cause: context.Canceled, category: safeTransportRetryNone},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := safeTransportFailure(&url.Error{Op: "Get", URL: "https://example.invalid/?token=secret", Err: test.cause})
			var safeErr *safeTransportError
			if !errors.As(err, &safeErr) {
				t.Fatalf("safeTransportFailure returned %T", err)
			}
			if safeErr.retryCategory != test.category || safeErr.Retryable() != test.retryable {
				t.Fatalf("category=%v retryable=%t, want %v/%t", safeErr.retryCategory, safeErr.Retryable(), test.category, test.retryable)
			}
			rawCauseRetained := test.cause != context.Canceled && test.cause != context.DeadlineExceeded && errors.Unwrap(err) == test.cause
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "example.invalid") || rawCauseRetained {
				t.Fatalf("safe retry classification retained raw transport detail: %#v", err)
			}
		})
	}
}

func TestClientErrorBodyCancellationIsNotClassifiedAsAPIAbsence(t *testing.T) {
	t.Parallel()

	client := &Client{
		APIBase: "https://example.invalid",
		APIKey:  "admin",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     make(http.Header),
				Body:       failingReadCloser{err: context.Canceled},
				Request:    request,
			}, nil
		})},
	}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/team/info", nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("body cancellation identity was lost: %v", err)
	}
	if IsAPIErrorStatus(err, http.StatusNotFound) || IsNotFoundError(err) {
		t.Fatalf("canceled 404 body read was classified as absence: %v", err)
	}
	if unwrapped := errors.Unwrap(err); unwrapped != context.Canceled {
		t.Fatalf("safe response error retained unexpected cause: %#v", unwrapped)
	}
}

func TestClientInvalidJSONAndBodyLimitsAreSafe(t *testing.T) {
	t.Parallel()

	t.Run("invalid JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Request-ID", "req-json")
			_, _ = writer.Write([]byte(`{"secret":"sk-unclosed"`))
		}))
		defer server.Close()
		client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
		var result map[string]interface{}
		err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/team/info", nil, &result)
		if err == nil || strings.Contains(err.Error(), "sk-unclosed") || !strings.Contains(err.Error(), "failed to decode") {
			t.Fatalf("unsafe decode error: %v", err)
		}
	})

	t.Run("bounded reader", func(t *testing.T) {
		exact := strings.NewReader(strings.Repeat("x", int(maxErrorResponseBody)))
		body, truncated, err := readBoundedBody(exact, maxErrorResponseBody)
		if err != nil || truncated || int64(len(body)) != maxErrorResponseBody {
			t.Fatalf("exact limit: len=%d truncated=%t err=%v", len(body), truncated, err)
		}
		over := strings.NewReader(strings.Repeat("x", int(maxErrorResponseBody)+1))
		body, truncated, err = readBoundedBody(over, maxErrorResponseBody)
		if err != nil || !truncated || int64(len(body)) != maxErrorResponseBody {
			t.Fatalf("over limit: len=%d truncated=%t err=%v", len(body), truncated, err)
		}
	})

	t.Run("oversized API error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.WriteHeader(http.StatusBadRequest)
			_, _ = writer.Write([]byte(strings.Repeat("secret", int(maxErrorResponseBody/6)+2)))
		}))
		defer server.Close()
		client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
		err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/team/info", nil, nil)
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !apiErr.BodyTruncated || apiErr.Detail != "" || apiErr.Body != "" {
			t.Fatalf("oversized API error = %#v", err)
		}
	})

	t.Run("oversized success", func(t *testing.T) {
		client := &Client{
			APIBase: "https://example.invalid",
			APIKey:  "admin",
			HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode:    http.StatusOK,
					Header:        make(http.Header),
					Body:          http.NoBody,
					ContentLength: maxSuccessResponseBody + 1,
					Request:       request,
				}, nil
			})},
		}
		var result interface{}
		err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/team/list", nil, &result)
		if err == nil || !strings.Contains(err.Error(), "safety limit") {
			t.Fatalf("unsafe oversized success error: %v", err)
		}
	})
}

func TestClientRequestIDValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		id   string
		want string
	}{
		{"safe", "req-123_abc", "req-123_abc"},
		{"control", "req-123\nsecret", ""},
		{"too long", strings.Repeat("a", maxRequestIDLength+1), ""},
		{"token", "sk-request-secret", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			headers := http.Header{"X-Request-Id": []string{test.id}}
			if got := safeRequestID(headers, requestSafety{}); got != test.want {
				t.Fatalf("safeRequestID(%q) = %q, want %q", test.id, got, test.want)
			}
		})
	}
	headers := http.Header{"X-Request-Id": []string{"req-metadata-secret"}}
	if got := safeRequestID(headers, requestSafety{secrets: []string{"metadata-secret"}}); got != "" {
		t.Fatalf("request ID containing submitted secret was retained: %q", got)
	}
	overflowSafety := requestSafety{}
	for i := 0; i <= maxCollectedSecrets; i++ {
		overflowSafety.addSecret(fmt.Sprintf("secret-%d", i))
	}
	if got := safeRequestID(headers, overflowSafety); got != "" || !overflowSafety.requestIDUnsafe {
		t.Fatalf("request ID was retained after secret collection overflow: %q", got)
	}
	callIDHeaders := http.Header{"X-Litellm-Call-Id": []string{"call-123"}}
	if got := safeRequestID(callIDHeaders, requestSafety{}); got != "call-123" {
		t.Fatalf("x-litellm-call-id = %q, want call-123", got)
	}
}

func TestClientEmptyAndNullResponsesRequireNoResultTarget(t *testing.T) {
	t.Parallel()

	for _, body := range []string{"", "null", "  null\n"} {
		body := body
		t.Run(fmt.Sprintf("body-%q", body), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = writer.Write([]byte(body))
			}))
			defer server.Close()
			client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
			var result map[string]interface{}
			err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/team/info", nil, &result)
			var responseErr *safeResponseError
			if !errors.As(err, &responseErr) || !responseErr.Temporary() || !strings.Contains(err.Error(), "empty JSON response") {
				t.Fatalf("body %q returned %#v, want safe retryable response error", body, err)
			}
			if err := client.DoRequestWithResponse(context.Background(), http.MethodPost, "/team/update", nil, nil); err != nil {
				t.Fatalf("body %q with no result target returned error: %v", body, err)
			}
		})
	}
}
