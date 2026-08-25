package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
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

	notFound         bool
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
	var jsonBody []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, requestSafety{}, &safeResponseError{kind: "failed to encode LiteLLM request", identity: safeErrorIdentity(err)}
		}
		jsonBody = encoded
	}

	request, err := http.NewRequestWithContext(ctx, method, c.APIBase+requestPath, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, requestSafety{}, safeTransportFailure(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("x-api-key", c.APIKey)
	request.Header.Set("Authorization", "Bearer "+c.APIKey)
	if c.LiteLLMChangedBy != "" {
		request.Header.Set("litellm-changed-by", c.LiteLLMChangedBy)
	}

	return request, classifyRequestSafety(request, requestPath, jsonBody, c.APIKey), nil
}

func (c *Client) executeRequest(request *http.Request) (*http.Response, error) {
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, safeDispatchedTransportFailure(err)
	}
	return response, nil
}

// DoRequest performs an HTTP request with context and standard headers. Any
// transport error returned by this method deliberately omits the request URL.
func (c *Client) DoRequest(ctx context.Context, method, requestPath string, body interface{}) (*http.Response, error) {
	request, _, err := c.prepareRequest(ctx, method, requestPath, body)
	if err != nil {
		return nil, err
	}
	return c.executeRequest(request)
}

// DoRequestWithResponse performs an HTTP request and decodes the JSON response.
// Response reads are bounded and every returned error is safe to include in a
// Terraform diagnostic.
func (c *Client) DoRequestWithResponse(ctx context.Context, method, requestPath string, body interface{}, result interface{}) error {
	_, err := c.doRequestWithResponse(ctx, method, requestPath, body, result)
	return err
}

// doRequestWithResponse is the single bounded, redacted response path used by
// callers that also need to know whether LiteLLM accepted a mutation before a
// success body failed validation. accepted is true only for an HTTP 2xx
// response; it does not imply that the bounded body was readable or valid JSON.
func (c *Client) doRequestWithResponse(ctx context.Context, method, requestPath string, body interface{}, result interface{}) (accepted bool, err error) {
	request, safety, err := c.prepareRequest(ctx, method, requestPath, body)
	if err != nil {
		return false, err
	}
	response, err := c.executeRequest(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()

	accepted = response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	requestID := safeRequestID(response.Header, safety)
	limit := maxSuccessResponseBody
	if !accepted {
		limit = maxErrorResponseBody
	}
	if response.ContentLength > limit {
		if !accepted {
			return false, &APIError{
				StatusCode:    response.StatusCode,
				RequestID:     requestID,
				DetailOmitted: true,
				BodyTruncated: true,
			}
		}
		return true, &safeResponseError{statusCode: response.StatusCode, requestID: requestID, kind: "LiteLLM response exceeded the provider safety limit"}
	}
	bodyBytes, truncated, readErr := readBoundedBody(response.Body, limit)

	if !accepted {
		if readErr != nil {
			return false, &safeResponseError{statusCode: response.StatusCode, requestID: requestID, kind: "failed to read LiteLLM error response", identity: safeErrorIdentity(readErr), temporary: safeTemporaryResponseFailure(readErr)}
		}
		notFound, fallbackNotReady := classifyRawErrorBody(bodyBytes)
		detail, detailOmitted := "", true
		if !truncated {
			detail, detailOmitted = safeResponseDetail(bodyBytes, response.Header.Get("Content-Type"), safety)
		}
		return false, &APIError{
			StatusCode:       response.StatusCode,
			Body:             detail,
			RequestID:        requestID,
			Detail:           detail,
			DetailOmitted:    detailOmitted,
			BodyTruncated:    truncated,
			notFound:         notFound,
			fallbackNotReady: fallbackNotReady,
		}
	}

	if readErr != nil {
		return true, &safeResponseError{statusCode: response.StatusCode, requestID: requestID, kind: "failed to read LiteLLM response", identity: safeErrorIdentity(readErr), temporary: safeTemporaryResponseFailure(readErr)}
	}
	if truncated {
		return true, &safeResponseError{statusCode: response.StatusCode, requestID: requestID, kind: "LiteLLM response exceeded the provider safety limit"}
	}
	if result == nil || len(bodyBytes) == 0 || string(bodyBytes) == "null" {
		return true, nil
	}
	if err := json.Unmarshal(bodyBytes, result); err != nil {
		return true, &safeResponseError{statusCode: response.StatusCode, requestID: requestID, kind: "failed to decode LiteLLM response as JSON", identity: safeErrorIdentity(err)}
	}
	return true, nil
}

// IsAPIErrorStatus reports whether an error came from an HTTP API response
// with the exact status code. Callers that must distinguish absence from an
// unexpected error should use this instead of response-body heuristics.
func IsAPIErrorStatus(err error, statusCode int) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == statusCode
}

// IsNotFoundError retains compatibility with LiteLLM endpoints that return
// non-404 statuses (commonly 400) with a not-found message. Client-generated
// API errors carry a private classification so their raw body can be discarded.
func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if IsAPIErrorStatus(err, http.StatusNotFound) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		if apiErr.notFound {
			return true
		}
		// Compatibility for synthetic APIError values in callers and tests.
		text := strings.ToLower(apiErr.Detail + " " + apiErr.Body)
		return strings.Contains(text, "not found") || strings.Contains(text, "does not exist") || strings.Contains(text, "404")
	}
	var responseErr *safeResponseError
	var transportErr *safeTransportError
	if errors.As(err, &responseErr) || errors.As(err, &transportErr) {
		return false
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "not found") || strings.Contains(errText, "404") || strings.Contains(errText, "does not exist")
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
