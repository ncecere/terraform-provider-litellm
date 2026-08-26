package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIError represents a non-2xx LiteLLM response without retaining the raw
// response body. Body is retained for source compatibility but contains only
// the same bounded, sanitized content as Detail.
type APIError struct {
	StatusCode int
	Body       string // Deprecated: use Detail. Raw response bodies are never stored.
	RequestID  string
	Detail     string

	DetailOmitted bool
	BodyTruncated bool

	fallbackNotReady bool
}

func (e *APIError) Error() string {
	message := fmt.Sprintf("API request failed with status %d", e.StatusCode)
	if e.RequestID != "" {
		message += fmt.Sprintf(" (request ID %q)", e.RequestID)
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	} else if e.DetailOmitted || e.BodyTruncated {
		message += "; response detail omitted"
	}
	return message
}

func (c *Client) prepareRequest(ctx context.Context, method, requestPath string, body interface{}) (*http.Request, requestSafety, error) {
	// Explicit cancellation is the only context state that precedes local
	// request validation. A deadline still allows malformed bodies, URLs, and
	// other terminal local configuration to fail closed before dispatch.
	if err := ctx.Err(); errors.Is(err, context.Canceled) {
		return nil, requestSafety{}, safeTransportFailure(context.Canceled)
	}
	if strings.HasPrefix(requestPath, invalidReviewedEndpoint) {
		return nil, requestSafety{}, &safeResponseError{
			kind:     "LiteLLM endpoint identity cannot be represented safely by the reviewed route",
			terminal: true,
			stage:    safeResponseFailureLocal,
		}
	}

	var jsonBody []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil, requestSafety{}, safeTransportFailure(context.Canceled)
			}
			return nil, requestSafety{}, &safeResponseError{
				kind:     "failed to encode LiteLLM request",
				identity: safeErrorIdentity(err),
				terminal: true,
				stage:    safeResponseFailureLocal,
			}
		}
		jsonBody = encoded
	}
	// Marshaler implementations are caller code and may cancel the request
	// context whether they return an encoding error or valid JSON.
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, requestSafety{}, safeTransportFailure(context.Canceled)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.APIBase+requestPath, bytes.NewReader(jsonBody))
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, requestSafety{}, safeTransportFailure(context.Canceled)
		}
		// Preserve the established sanitized diagnostic while recording that
		// request construction is terminal local validation, not transport.
		return nil, requestSafety{}, &safeTransportError{
			kind:     safeTransportFailure(err).Error(),
			terminal: true,
		}
	}
	if (request.URL.Scheme != "http" && request.URL.Scheme != "https") || request.URL.Host == "" {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, requestSafety{}, safeTransportFailure(context.Canceled)
		}
		return nil, requestSafety{}, &safeResponseError{
			kind:     "LiteLLM API URL configuration is invalid",
			terminal: true,
			stage:    safeResponseFailureLocal,
		}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("x-api-key", c.APIKey)
	request.Header.Set("Authorization", "Bearer "+c.APIKey)
	if c.LiteLLMChangedBy != "" {
		request.Header.Set("litellm-changed-by", c.LiteLLMChangedBy)
	}

	safety := classifyRequestSafety(request, requestPath, jsonBody, c.APIKey)
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, requestSafety{}, safeTransportFailure(context.Canceled)
	}
	return request, safety, nil
}

func (c *Client) executeRequest(request *http.Request) (*http.Response, error) {
	return c.executeRequestWithOptions(request, clientRequestOptions{})
}

type clientRequestOptions struct {
	freshConnection bool
	now             func() time.Time
}

// executeRequestWithOptions keeps fresh-connection behavior deliberately narrow.
// Bounded worker-cache and write-convergence probes require the provider's cloneable *http.Transport, then
// set request.Close and use a cloned transport with an empty connection pool.
// request.Close also makes Go's HTTP/2 transport allocate a single-use connection.
// An arbitrary custom RoundTripper fails closed because connection freshness cannot
// be proved. The cloned transport is closed after the response body is consumed.
func (c *Client) executeRequestWithOptions(request *http.Request, options clientRequestOptions) (*http.Response, error) {
	configuredClient := c.HTTPClient
	if configuredClient == nil {
		configuredClient = http.DefaultClient
	}
	// Cancellation always dominates local execution configuration. Deadlines are
	// checked only after the terminal fresh-transport requirement below.
	if err := request.Context().Err(); errors.Is(err, context.Canceled) {
		return nil, safeTransportFailure(context.Canceled)
	}

	// Redirect policy is mutable client configuration. Clone the value for every
	// execution so provider requests never follow redirects and never race with
	// or modify a caller-supplied client.
	httpClientValue := *configuredClient
	httpClientValue.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	httpClient := &httpClientValue

	cleanup := func() {}
	if options.freshConnection {
		transport := httpClient.Transport
		if transport == nil {
			transport = http.DefaultTransport
		}
		baseTransport, ok := transport.(*http.Transport)
		if !ok || baseTransport == nil {
			// Cancellation is re-checked at the validation return because caller
			// code may cancel between request preparation and fresh dispatch.
			if errors.Is(request.Context().Err(), context.Canceled) {
				return nil, safeTransportFailure(context.Canceled)
			}
			// Validate the terminal freshness requirement before consulting an
			// expired deadline. request.Close is only a hint to an arbitrary
			// RoundTripper, so independent connections cannot otherwise be proved.
			return nil, &safeTransportError{
				kind:     "fresh LiteLLM connection is unavailable for the configured HTTP transport",
				terminal: true,
			}
		}
		request.Close = true
		freshTransport := baseTransport.Clone()
		freshTransport.DisableKeepAlives = true
		httpClient.Transport = freshTransport
		cleanup = freshTransport.CloseIdleConnections
	}
	if err := request.Context().Err(); err != nil {
		cleanup()
		return nil, safeTransportFailure(err)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		cleanup()
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, safeDispatchedTransportFailure(err)
	}
	if options.freshConnection {
		response.Body = &cleanupReadCloser{ReadCloser: response.Body, cleanup: cleanup}
	}
	return response, nil
}

type cleanupReadCloser struct {
	io.ReadCloser
	cleanup func()
}

func (c *cleanupReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cleanup()
	return err
}

// DoRequest performs an HTTP request with context and standard headers. Any
// transport error returned by this method deliberately omits the request URL.
func (c *Client) DoRequest(ctx context.Context, method, requestPath string, body interface{}) (*http.Response, error) {
	request, _, err := c.prepareRequest(ctx, method, requestPath, body)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, safeTransportFailure(context.Canceled)
		}
		return nil, err
	}
	return c.executeRequest(request)
}

// DoRequestWithResponse performs one HTTP request and decodes the JSON response.
// It never retries, including for mutations. Response reads are bounded and
// every returned error is safe to include in a Terraform diagnostic.
func (c *Client) DoRequestWithResponse(ctx context.Context, method, requestPath string, body interface{}, result interface{}) error {
	_, err := c.doRequestWithResponse(ctx, method, requestPath, body, result)
	return err
}

// doFreshRequestWithResponse is reserved for bounded worker-cache and
// write-convergence probes. It preserves the normal bounded response and
// redaction path while preventing HTTP keepalive from pinning consecutive
// probes to one LiteLLM worker. Every fresh probe remains single-attempt and
// never invokes the safe-read retry layer.
func (c *Client) doFreshRequestWithResponse(ctx context.Context, method, requestPath string, body interface{}, result interface{}) error {
	_, err := c.doRequestWithResponseOptions(ctx, method, requestPath, body, result, clientRequestOptions{freshConnection: true})
	return err
}

// doRequestWithResponse is the single bounded, redacted response path used by
// callers that also need to know whether LiteLLM accepted a mutation before a
// success body failed validation. accepted is true only for an HTTP 2xx
// response; it does not imply that the bounded body was readable or valid JSON.
func (c *Client) doRequestWithResponse(ctx context.Context, method, requestPath string, body interface{}, result interface{}) (accepted bool, err error) {
	return c.doRequestWithResponseOptions(ctx, method, requestPath, body, result, clientRequestOptions{})
}

func (c *Client) doRequestWithResponseOptions(ctx context.Context, method, requestPath string, body interface{}, result interface{}, options clientRequestOptions) (accepted bool, err error) {
	request, safety, err := c.prepareRequest(ctx, method, requestPath, body)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return false, safeTransportFailure(context.Canceled)
		}
		return false, err
	}
	response, err := c.executeRequestWithOptions(request, options)
	if err != nil {
		return false, err
	}
	bodyClosed := false
	defer func() {
		if !bodyClosed {
			_ = response.Body.Close()
		}
	}()
	closeBody := func() error {
		// Mark before invoking caller-controlled Close so the deferred fallback
		// can never double-close a body that reports an error.
		bodyClosed = true
		return response.Body.Close()
	}

	accepted = response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	requestID := safeRequestID(response.Header, safety)
	now := time.Now()
	if options.now != nil {
		now = options.now()
	}
	retryAfter, hasRetryAfter := safeRetryAfterWithDeadline(response.Header, now, maxAcceptedRetryAfter)
	limit := maxSuccessResponseBody
	if !accepted {
		limit = maxErrorResponseBody
	}

	advertisedOversize := response.ContentLength > limit
	readLimit := limit
	if advertisedOversize {
		// The advertised length already proves the safety contract failed. Still
		// perform a bounded read before Close so an immediate read failure is
		// combined with any close failure without consuming the oversized body.
		readLimit = 0
	}
	bodyBytes, truncated, readErr := readBoundedBody(response.Body, readLimit)
	truncated = truncated || advertisedOversize
	closeErr := closeBody()
	bodyErr := errors.Join(readErr, closeErr)
	if bodyErr != nil {
		traits := collectRawHTTPFailureTraits(bodyErr)
		kind := "failed to read or close LiteLLM response"
		stage := safeResponseFailureAcceptedBodyRead
		if !accepted {
			kind = "failed to read or close LiteLLM error response"
			stage = safeResponseFailureStatusBodyRead
		}
		return accepted, withSafeRetrySchedule(&safeResponseError{
			statusCode:        response.StatusCode,
			requestID:         requestID,
			kind:              kind,
			identity:          safeErrorIdentity(bodyErr),
			retryable:         safeTemporaryResponseFailure(bodyErr),
			canceled:          traits.canceled,
			terminal:          traits.terminal(),
			deadline:          traits.deadline,
			safeReadTransient: safeReadTransientFailure(bodyErr),
			stage:             stage,
			dispatched:        true,
			accepted:          accepted,
		}, retryAfter, hasRetryAfter)
	}
	if advertisedOversize {
		if !accepted {
			return false, withSafeRetrySchedule(&APIError{
				StatusCode:    response.StatusCode,
				RequestID:     requestID,
				DetailOmitted: true,
				BodyTruncated: true,
			}, retryAfter, hasRetryAfter)
		}
		return true, &safeResponseError{statusCode: response.StatusCode, requestID: requestID, kind: "LiteLLM response exceeded the provider safety limit", stage: safeResponseFailureContract, dispatched: true, accepted: true}
	}

	if !accepted {
		fallbackNotReady := classifyFallbackNotReadyBody(bodyBytes)
		detail, detailOmitted := "", true
		if !truncated && (response.StatusCode < http.StatusMultipleChoices || response.StatusCode >= http.StatusBadRequest) {
			detail, detailOmitted = safeResponseDetail(bodyBytes, response.Header.Get("Content-Type"), safety)
		}
		return false, withSafeRetrySchedule(&APIError{
			StatusCode:       response.StatusCode,
			Body:             detail,
			RequestID:        requestID,
			Detail:           detail,
			DetailOmitted:    detailOmitted,
			BodyTruncated:    truncated,
			fallbackNotReady: fallbackNotReady,
		}, retryAfter, hasRetryAfter)
	}

	if truncated {
		return true, &safeResponseError{statusCode: response.StatusCode, requestID: requestID, kind: "LiteLLM response exceeded the provider safety limit", stage: safeResponseFailureContract, dispatched: true, accepted: true}
	}
	if result == nil {
		return true, nil
	}
	trimmedBody := bytes.TrimSpace(bodyBytes)
	if len(trimmedBody) == 0 || bytes.Equal(trimmedBody, []byte("null")) {
		return true, &safeResponseError{statusCode: response.StatusCode, requestID: requestID, kind: "LiteLLM returned an empty JSON response where an object or array was required", retryable: true, stage: safeResponseFailureContract, dispatched: true, accepted: true}
	}
	if err := decodeJSONUseNumber(trimmedBody, result); err != nil {
		return true, &safeResponseError{statusCode: response.StatusCode, requestID: requestID, kind: "failed to decode LiteLLM response as JSON", identity: safeErrorIdentity(err), retryable: true, stage: safeResponseFailureContract, dispatched: true, accepted: true}
	}
	return true, nil
}

// IsAPIErrorStatus reports whether an error came from an HTTP API response
// with the exact status code. Callers that must distinguish absence from an
// unexpected error should use this instead of response-body heuristics.
func IsAPIErrorStatus(err error, statusCode int) bool {
	classification := ClassifyHTTPFailure(err)
	return (classification.Kind == HTTPFailureTransientResponse || classification.Kind == HTTPFailureTerminalResponse) &&
		classification.StatusCode == statusCode && hasAuthoritativeAPIStatus(err, statusCode)
}

// IsNotFoundError reports exact typed HTTP 404 responses only.
// Deprecated: use IsAPIErrorStatus(err, http.StatusNotFound) when the endpoint's
// absence contract is explicit.
func IsNotFoundError(err error) bool {
	return IsAPIErrorStatus(err, http.StatusNotFound)
}

func isFallbackNotReadyError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.fallbackNotReady {
			return true
		}
		text := strings.ToLower(apiErr.Detail + " " + apiErr.Body)
		return strings.Contains(text, "invalid fallback models") || strings.Contains(text, "not found in router")
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "invalid fallback models") || strings.Contains(text, "not found in router")
}
