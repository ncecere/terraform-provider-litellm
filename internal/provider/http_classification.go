package provider

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HTTPFailureKind is the content-free category of a failed HTTP operation.
// It deliberately carries no URL, response text, header value, or transport
// cause.
type HTTPFailureKind uint8

const (
	HTTPFailureNone HTTPFailureKind = iota
	HTTPFailureCanceled
	HTTPFailureDeadline
	HTTPFailureTransientTransport
	HTTPFailureTransientAcceptedResponse
	HTTPFailureTransientResponse
	HTTPFailureTerminalResponse
	HTTPFailureContractOrLocal
)

// HTTPFailureClassification contains only values that are safe to use for
// retry and mutation-uncertainty decisions. ResponseAccepted means that a 2xx
// response was received; it does not mean that its body satisfied the caller's
// response contract.
type HTTPFailureClassification struct {
	Kind HTTPFailureKind

	StatusCode        int
	RetryAfter        time.Duration
	HasRetryAfter     bool
	RequestDispatched bool
	ResponseAccepted  bool

	// retryAfterDeadline is retained only for HTTP-date scheduling. It is not
	// exported or rendered and lets slow body reads consume the server's wait.
	retryAfterDeadline time.Time
}

// ClassifyHTTPFailure returns a typed, content-free classification for err.
// Wrapped provider errors are recognized with errors.As/errors.Is.
func ClassifyHTTPFailure(err error) HTTPFailureClassification {
	if err == nil {
		return HTTPFailureClassification{Kind: HTTPFailureNone}
	}

	var transportErr *safeTransportError
	if errors.As(err, &transportErr) {
		classification := HTTPFailureClassification{RequestDispatched: transportErr.dispatched}
		switch {
		case transportErr.canceled:
			classification.Kind = HTTPFailureCanceled
		case transportErr.deadline:
			classification.Kind = HTTPFailureDeadline
		case transportErr.safeReadTransient || transportErr.Retryable():
			classification.Kind = HTTPFailureTransientTransport
		default:
			classification.Kind = HTTPFailureContractOrLocal
		}
		return classification
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		classification := HTTPFailureClassification{
			StatusCode:         apiErr.StatusCode,
			RetryAfter:         apiErr.retryAfter,
			HasRetryAfter:      apiErr.hasRetryAfter,
			RequestDispatched:  true,
			retryAfterDeadline: apiErr.retryAfterDeadline,
		}
		if isTransientHTTPStatus(apiErr.StatusCode) {
			classification.Kind = HTTPFailureTransientResponse
		} else {
			classification.Kind = HTTPFailureTerminalResponse
		}
		return classification
	}

	var responseErr *safeResponseError
	if errors.As(err, &responseErr) {
		classification := HTTPFailureClassification{
			Kind:               HTTPFailureContractOrLocal,
			StatusCode:         responseErr.statusCode,
			RetryAfter:         responseErr.retryAfter,
			HasRetryAfter:      responseErr.hasRetryAfter,
			RequestDispatched:  responseErr.dispatched,
			ResponseAccepted:   responseErr.accepted,
			retryAfterDeadline: responseErr.retryAfterDeadline,
		}
		switch {
		case errors.Is(responseErr, context.Canceled):
			classification.Kind = HTTPFailureCanceled
		case responseErr.stage == safeResponseFailureAcceptedBodyRead && responseErr.safeReadTransient:
			classification.Kind = HTTPFailureTransientAcceptedResponse
		case errors.Is(responseErr, context.DeadlineExceeded):
			classification.Kind = HTTPFailureDeadline
		case responseErr.dispatched && !responseErr.accepted && responseErr.statusCode >= http.StatusMultipleChoices && responseErr.statusCode <= 599:
			if isTransientHTTPStatus(responseErr.statusCode) {
				classification.Kind = HTTPFailureTransientResponse
			} else {
				classification.Kind = HTTPFailureTerminalResponse
			}
		}
		return classification
	}

	switch {
	case errors.Is(err, context.Canceled):
		return HTTPFailureClassification{Kind: HTTPFailureCanceled}
	case errors.Is(err, context.DeadlineExceeded):
		return HTTPFailureClassification{Kind: HTTPFailureDeadline}
	default:
		return HTTPFailureClassification{Kind: HTTPFailureContractOrLocal}
	}
}

func isTransientHTTPStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError && statusCode <= 599
}

const maxAcceptedRetryAfter = 5 * time.Minute

type safeRetryAfterSpec struct {
	delay    time.Duration
	deadline time.Time
}

func safeRetryAfter(headers http.Header, now time.Time, maximum time.Duration) (time.Duration, bool) {
	spec, ok := safeRetryAfterWithDeadline(headers, now, maximum)
	return spec.delay, ok
}

func safeRetryAfterWithDeadline(headers http.Header, now time.Time, maximum time.Duration) (safeRetryAfterSpec, bool) {
	values := headers.Values("Retry-After")
	if len(values) != 1 {
		return safeRetryAfterSpec{}, false
	}
	return parseRetryAfterSpec(values[0], now, maximum)
}

// parseRetryAfter converts either delta-seconds or an HTTP-date to a bounded
// duration. It returns false for negative, malformed, overflowed, excessive,
// or ambiguous values and never returns or stores the original header text.
func parseRetryAfter(value string, now time.Time, maximum time.Duration) (time.Duration, bool) {
	spec, ok := parseRetryAfterSpec(value, now, maximum)
	return spec.delay, ok
}

func parseRetryAfterSpec(value string, now time.Time, maximum time.Duration) (safeRetryAfterSpec, bool) {
	if value == "" || value != strings.TrimSpace(value) || maximum < 0 {
		return safeRetryAfterSpec{}, false
	}
	if strings.IndexFunc(value, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		seconds, err := strconv.ParseUint(value, 10, 64)
		if err != nil || seconds > uint64(maximum/time.Second) {
			return safeRetryAfterSpec{}, false
		}
		return safeRetryAfterSpec{delay: time.Duration(seconds) * time.Second}, true
	}

	when, err := http.ParseTime(value)
	if err != nil || when.Before(now) {
		return safeRetryAfterSpec{}, false
	}
	delay := when.Sub(now)
	if delay < 0 || delay > maximum {
		return safeRetryAfterSpec{}, false
	}
	return safeRetryAfterSpec{delay: delay, deadline: when}, true
}
