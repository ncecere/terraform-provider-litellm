package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestGlobalHTTPFailurePrecedenceIsJoinOrderIndependent(t *testing.T) {
	transient := safeDispatchedTransportFailure(temporaryTransportTestError{})
	terminal := safeDispatchedTransportFailure(x509.UnknownAuthorityError{})
	api404 := &APIError{StatusCode: http.StatusNotFound}

	tests := []struct {
		name  string
		left  error
		right error
		kind  HTTPFailureKind
	}{
		{"provider-transient-cancellation", transient, context.Canceled, HTTPFailureCanceled},
		{"api-404-cancellation", api404, context.Canceled, HTTPFailureCanceled},
		{"terminal-transient", terminal, transient, HTTPFailureContractOrLocal},
		{"deadline-api-404", context.DeadlineExceeded, api404, HTTPFailureDeadline},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, err := range []error{errors.Join(test.left, test.right), errors.Join(test.right, test.left)} {
				classification := ClassifyHTTPFailure(err)
				if classification.Kind != test.kind || classification.StatusCode != 0 ||
					classification.HasRetryAfter || !classification.RequestDispatched || IsNotFoundError(err) {
					t.Fatalf("classification=%#v not_found=%t error=%v", classification, IsNotFoundError(err), err)
				}
			}
		})
	}

	for _, err := range []error{errors.Join(transient, api404), errors.Join(api404, transient)} {
		classification := ClassifyHTTPFailure(err)
		if classification.Kind != HTTPFailureTransientTransport || classification.StatusCode != http.StatusNotFound ||
			IsNotFoundError(err) || IsAPIErrorStatus(err, http.StatusNotFound) {
			t.Fatalf("transient did not dominate exact 404: %#v / %v", classification, err)
		}
	}
}

func TestGlobalHTTPFailurePrecedenceCrossProduct(t *testing.T) {
	levels := []struct {
		name string
		err  error
		kind HTTPFailureKind
	}{
		{"canceled", context.Canceled, HTTPFailureCanceled},
		{"terminal", safeDispatchedTransportFailure(x509.UnknownAuthorityError{}), HTTPFailureContractOrLocal},
		{"deadline", safeDispatchedTransportFailure(context.DeadlineExceeded), HTTPFailureDeadline},
		{"transient", safeDispatchedTransportFailure(temporaryTransportTestError{}), HTTPFailureTransientTransport},
		{"typed-terminal-status", &APIError{StatusCode: http.StatusNotFound}, HTTPFailureTerminalResponse},
		{"opaque", errors.New("opaque local contract"), HTTPFailureContractOrLocal},
	}
	for higher := 0; higher < len(levels); higher++ {
		for lower := higher + 1; lower < len(levels); lower++ {
			name := levels[higher].name + "-over-" + levels[lower].name
			t.Run(name, func(t *testing.T) {
				orders := []error{
					errors.Join(levels[higher].err, levels[lower].err),
					errors.Join(levels[lower].err, levels[higher].err),
					fmt.Errorf("single: %w", errors.Join(fmt.Errorf("nested: %w", levels[lower].err), levels[higher].err)),
				}
				for _, err := range orders {
					classification := ClassifyHTTPFailure(err)
					if classification.Kind != levels[higher].kind {
						t.Fatalf("classification=%#v error=%v", classification, err)
					}
					if lower == 4 && higher < 4 && IsNotFoundError(err) {
						t.Fatalf("higher trait classified as exact 404: %#v / %v", classification, err)
					}
				}
			})
		}
	}
}

func TestGlobalHTTPFailureTraversalHandlesNestedSingleAndMultiUnwrap(t *testing.T) {
	transient := safeDispatchedTransportFailure(temporaryTransportTestError{})
	accepted := &safeResponseError{
		statusCode: http.StatusOK, kind: "safe accepted failure", stage: safeResponseFailureAcceptedBodyRead,
		safeReadTransient: true, dispatched: true, accepted: true,
	}
	for _, err := range []error{
		fmt.Errorf("single: %w", errors.Join(fmt.Errorf("nested: %w", transient), context.Canceled)),
		fmt.Errorf("single: %w", errors.Join(accepted, errors.Join(x509.UnknownAuthorityError{}, transient))),
	} {
		classification := ClassifyHTTPFailure(err)
		if classification.Kind != HTTPFailureCanceled && classification.Kind != HTTPFailureContractOrLocal {
			t.Fatalf("classification=%#v error=%v", classification, err)
		}
		if !classification.RequestDispatched {
			t.Fatalf("dispatch metadata was lost: %#v", classification)
		}
	}

	joined := errors.Join(accepted, context.Canceled)
	classification := ClassifyHTTPFailure(joined)
	if classification.Kind != HTTPFailureCanceled || !classification.RequestDispatched ||
		!classification.ResponseAccepted || classification.StatusCode != 0 {
		t.Fatalf("accepted metadata was not conservatively retained: %#v", classification)
	}
}

func TestAcceptedBodyReadTraitsAreSanitizedAndGloballyPrioritized(t *testing.T) {
	const secret = "raw-accepted-body-cause-secret"
	tests := []struct {
		name       string
		left       error
		right      error
		kind       HTTPFailureKind
		statusCode int
	}{
		{"canceled", fmt.Errorf("%s: %w", secret, io.ErrUnexpectedEOF), context.Canceled, HTTPFailureCanceled, 0},
		{"terminal", fmt.Errorf("%s: %w", secret, temporaryTransportTestError{}), x509.UnknownAuthorityError{}, HTTPFailureContractOrLocal, 0},
		{"deadline", fmt.Errorf("%s: %w", secret, temporaryTransportTestError{}), context.DeadlineExceeded, HTTPFailureDeadline, 0},
		{"transient", fmt.Errorf("%s: %w", secret, io.ErrUnexpectedEOF), errors.New("opaque"), HTTPFailureTransientAcceptedResponse, http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, reverse := range []bool{false, true} {
				cause := errors.Join(test.left, test.right)
				if reverse {
					cause = errors.Join(test.right, test.left)
				}
				var calls atomic.Int32
				client := testRetryClient(func(request *http.Request) (*http.Response, error) {
					calls.Add(1)
					response := testHTTPResponse(request, http.StatusOK, nil)
					response.Body = failingReadCloser{err: cause}
					return response, nil
				})
				var result map[string]interface{}
				err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, &result, testReadPolicy(2), noWaitRetryHooks())
				classification := ClassifyHTTPFailure(err)
				wantCalls := int32(1)
				if test.kind == HTTPFailureTransientAcceptedResponse {
					wantCalls = 2
				}
				if calls.Load() != wantCalls || classification.Kind != test.kind || classification.StatusCode != test.statusCode ||
					!classification.RequestDispatched || !classification.ResponseAccepted {
					t.Fatalf("reverse=%t calls=%d classification=%#v error=%v", reverse, calls.Load(), classification, err)
				}
				var responseErr *safeResponseError
				if !errors.As(err, &responseErr) || responseErr.identity != nil &&
					responseErr.identity != context.Canceled && responseErr.identity != context.DeadlineExceeded {
					t.Fatalf("unsafe identity retained: %#v", responseErr)
				}
				for _, rendered := range []string{err.Error(), fmt.Sprintf("%v", err), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), fmt.Sprint(classification)} {
					if strings.Contains(rendered, secret) {
						t.Fatalf("raw cause escaped: %q", rendered)
					}
				}
			}
		})
	}
}

type bodyReadTimeoutTestError struct{}

func (bodyReadTimeoutTestError) Error() string   { return "body-read-timeout-secret" }
func (bodyReadTimeoutTestError) Timeout() bool   { return true }
func (bodyReadTimeoutTestError) Temporary() bool { return false }

func TestNonAcceptedStatusBodyReadTransientTraitsDominateEveryStatus(t *testing.T) {
	causes := []struct {
		name string
		err  error
	}{
		{"timeout", bodyReadTimeoutTestError{}},
		{"reset", syscall.ECONNRESET},
		{"unexpected-eof", io.ErrUnexpectedEOF},
	}
	for _, status := range []int{
		http.StatusNotFound,
		http.StatusBadRequest,
		http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
	} {
		for _, cause := range causes {
			t.Run(fmt.Sprintf("%d-%s", status, cause.name), func(t *testing.T) {
				var calls atomic.Int32
				client := testRetryClient(func(request *http.Request) (*http.Response, error) {
					calls.Add(1)
					response := testHTTPResponse(request, status, http.Header{"Retry-After": []string{"2"}})
					response.Body = failingReadCloser{err: cause.err}
					return response, nil
				})
				err := client.doReadWithResponsePolicy(
					context.Background(), http.MethodGet, "/safe", nil, nil, testReadPolicy(2), noWaitRetryHooks(),
				)
				classification := ClassifyHTTPFailure(err)
				if calls.Load() != 2 || classification.Kind != HTTPFailureTransientTransport || classification.StatusCode != status ||
					classification.RetryAfter != 2*time.Second || !classification.HasRetryAfter ||
					!classification.RequestDispatched || classification.ResponseAccepted ||
					IsAPIErrorStatus(err, status) || IsNotFoundError(err) {
					t.Fatalf("classification=%#v api_status=%t not_found=%t error=%v",
						classification, IsAPIErrorStatus(err, status), IsNotFoundError(err), err)
				}
				for _, rendered := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), fmt.Sprint(classification)} {
					if strings.Contains(rendered, "body-read-timeout-secret") {
						t.Fatalf("body read cause escaped: %q", rendered)
					}
				}
			})
		}
	}
}

func TestStatusBodyReadHigherTraitsPreventRetryInBothJoinOrders(t *testing.T) {
	tests := []struct {
		name  string
		cause func(bool) error
		kind  HTTPFailureKind
	}{
		{"terminal", func(reverse bool) error {
			if reverse {
				return errors.Join(temporaryTransportTestError{}, x509.UnknownAuthorityError{})
			}
			return errors.Join(x509.UnknownAuthorityError{}, temporaryTransportTestError{})
		}, HTTPFailureContractOrLocal},
		{"deadline", func(reverse bool) error {
			if reverse {
				return errors.Join(temporaryTransportTestError{}, context.DeadlineExceeded)
			}
			return errors.Join(context.DeadlineExceeded, temporaryTransportTestError{})
		}, HTTPFailureDeadline},
	}
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		for _, test := range tests {
			for _, reverse := range []bool{false, true} {
				var calls atomic.Int32
				client := testRetryClient(func(request *http.Request) (*http.Response, error) {
					calls.Add(1)
					response := testHTTPResponse(request, status, http.Header{"Retry-After": []string{"3"}})
					response.Body = failingReadCloser{err: test.cause(reverse)}
					return response, nil
				})
				err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, nil, testReadPolicy(3), noWaitRetryHooks())
				classification := ClassifyHTTPFailure(err)
				if calls.Load() != 1 || classification.Kind != test.kind || classification.StatusCode != 0 ||
					classification.HasRetryAfter || retryableSafeReadClassification(classification) {
					t.Fatalf("status=%d trait=%s reverse=%t calls=%d classification=%#v error=%v",
						status, test.name, reverse, calls.Load(), classification, err)
				}
			}
		}
	}
}

func TestGlobalStatusAndRetryAfterCandidatesAreOrderIndependent(t *testing.T) {
	accepted200 := &safeResponseError{
		statusCode: http.StatusOK, kind: "accepted contract", stage: safeResponseFailureContract,
		dispatched: true, accepted: true,
	}
	api404 := &APIError{StatusCode: http.StatusNotFound}
	api503 := &APIError{StatusCode: http.StatusServiceUnavailable}

	for _, test := range []struct {
		name  string
		left  error
		right error
		kind  HTTPFailureKind
	}{
		{"accepted-200-and-404", accepted200, api404, HTTPFailureTerminalResponse},
		{"404-and-503", api404, api503, HTTPFailureTransientResponse},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, err := range []error{
				errors.Join(test.left, test.right),
				errors.Join(test.right, test.left),
				fmt.Errorf("outer: %w", errors.Join(fmt.Errorf("nested: %w", test.right), test.left)),
			} {
				classification := ClassifyHTTPFailure(err)
				if classification.Kind != test.kind || classification.StatusCode != 0 ||
					classification.HasRetryAfter || IsAPIErrorStatus(err, http.StatusNotFound) ||
					IsAPIErrorStatus(err, http.StatusServiceUnavailable) {
					t.Fatalf("classification=%#v error=%v", classification, err)
				}
			}
		})
	}

	scheduled503 := func(delay time.Duration) error {
		return withSafeRetrySchedule(
			&APIError{StatusCode: http.StatusServiceUnavailable},
			safeRetryAfterSpec{delay: delay},
			true,
		)
	}
	for _, err := range []error{
		errors.Join(scheduled503(time.Second), scheduled503(2*time.Second)),
		errors.Join(scheduled503(2*time.Second), scheduled503(time.Second)),
		fmt.Errorf("outer: %w", errors.Join(scheduled503(time.Second), fmt.Errorf("nested: %w", scheduled503(2*time.Second)))),
		errors.Join(scheduled503(time.Second), &APIError{StatusCode: http.StatusServiceUnavailable}),
	} {
		classification := ClassifyHTTPFailure(err)
		if classification.Kind != HTTPFailureTransientResponse || classification.StatusCode != http.StatusServiceUnavailable ||
			classification.HasRetryAfter || classification.RetryAfter != 0 {
			t.Fatalf("ambiguous Retry-After survived: %#v / %v", classification, err)
		}
	}
}

func TestRetryAfterRequiresRetryableFinalKindAndOneResponse(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
	} {
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			return testHTTPResponse(request, status, http.Header{"Retry-After": []string{"4"}}), nil
		})
		err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, nil)
		classification := ClassifyHTTPFailure(err)
		if classification.Kind != HTTPFailureTerminalResponse || classification.StatusCode != status ||
			classification.HasRetryAfter || classification.RetryAfter != 0 {
			t.Fatalf("status=%d classification=%#v error=%v", status, classification, err)
		}
	}

	acceptedClient := testRetryClient(func(request *http.Request) (*http.Response, error) {
		response := testHTTPResponse(request, http.StatusOK, http.Header{"Retry-After": []string{"4"}})
		response.Body = failingReadCloser{err: io.ErrUnexpectedEOF}
		return response, nil
	})
	var result map[string]interface{}
	acceptedErr := acceptedClient.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, &result)
	accepted := ClassifyHTTPFailure(acceptedErr)
	if accepted.Kind != HTTPFailureTransientAcceptedResponse || accepted.StatusCode != http.StatusOK ||
		!accepted.HasRetryAfter || accepted.RetryAfter != 4*time.Second {
		t.Fatalf("accepted classification=%#v error=%v", accepted, acceptedErr)
	}

	local := ClassifyHTTPFailure(withSafeRetrySchedule(&safeResponseError{
		statusCode: http.StatusOK,
		kind:       "local response contract",
		stage:      safeResponseFailureContract,
		dispatched: true,
		accepted:   true,
	}, safeRetryAfterSpec{delay: 4 * time.Second}, true))
	if local.Kind != HTTPFailureContractOrLocal || local.StatusCode != http.StatusOK ||
		local.HasRetryAfter || local.RetryAfter != 0 {
		t.Fatalf("local classification=%#v", local)
	}

	scheduled := withSafeRetrySchedule(
		&APIError{StatusCode: http.StatusServiceUnavailable},
		safeRetryAfterSpec{delay: 4 * time.Second},
		true,
	)
	for _, higher := range []error{
		context.Canceled,
		x509.UnknownAuthorityError{},
		context.DeadlineExceeded,
	} {
		for _, err := range []error{errors.Join(scheduled, higher), errors.Join(higher, scheduled)} {
			classification := ClassifyHTTPFailure(err)
			if classification.HasRetryAfter || classification.RetryAfter != 0 || classification.StatusCode != 0 {
				t.Fatalf("higher trait retained metadata: %#v / %v", classification, err)
			}
		}
	}
}

func TestExplicitCancellationPrecedesPreparationButDeadlineDoesNot(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	deadline, deadlineCancel := context.WithDeadline(context.Background(), time.Unix(1, 0))
	defer deadlineCancel()

	var calls atomic.Int32
	client := testRetryClient(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("must not dispatch")
	})
	invalidURLClient := &Client{APIBase: "://invalid", APIKey: "admin", HTTPClient: client.HTTPClient}
	invalidSchemeClient := &Client{APIBase: "ftp://pre-dispatch-secret.invalid", APIKey: "admin", HTTPClient: client.HTTPClient}
	emptyHostClient := &Client{APIBase: "https:///pre-dispatch-secret", APIKey: "admin", HTTPClient: client.HTTPClient}
	typedNilTransportClient := &Client{
		APIBase: "https://example.invalid",
		APIKey:  "admin",
		HTTPClient: &http.Client{
			Transport: (*http.Transport)(nil),
		},
	}

	canceledErrors := []error{
		client.DoRequestWithResponse(canceled, "BAD METHOD", "/safe", nil, nil),
		client.DoRequestWithResponse(canceled, http.MethodPost, "/safe", make(chan int), nil),
		invalidURLClient.DoRequestWithResponse(canceled, http.MethodGet, "/safe", nil, nil),
		invalidSchemeClient.DoRequestWithResponse(canceled, http.MethodGet, "/safe", nil, nil),
		emptyHostClient.DoReadWithResponse(canceled, http.MethodGet, "/safe", nil, nil),
		client.doFreshRequestWithResponse(canceled, http.MethodGet, "/safe", nil, nil),
		typedNilTransportClient.doFreshRequestWithResponse(canceled, http.MethodGet, "/safe", nil, nil),
		client.DoReadWithResponse(canceled, http.MethodPost, "/safe", make(chan int), nil),
		client.doReadWithResponsePolicy(canceled, http.MethodGet, "/safe", nil, nil, safeReadRetryPolicy{}, safeReadRetryHooks{}),
	}
	for _, err := range canceledErrors {
		classification := ClassifyHTTPFailure(err)
		if classification.Kind != HTTPFailureCanceled || classification.RequestDispatched {
			t.Fatalf("cancellation did not win before preparation: %#v / %v", classification, err)
		}
	}

	deadlineErrors := []error{
		client.DoRequestWithResponse(deadline, "BAD METHOD", "/safe", nil, nil),
		client.DoRequestWithResponse(deadline, http.MethodPost, "/safe", make(chan int), nil),
		invalidURLClient.DoRequestWithResponse(deadline, http.MethodGet, "/safe", nil, nil),
		invalidSchemeClient.DoRequestWithResponse(deadline, http.MethodGet, "/safe", nil, nil),
		emptyHostClient.DoReadWithResponse(deadline, http.MethodGet, "/safe", nil, nil),
		client.doFreshRequestWithResponse(deadline, http.MethodGet, "/safe", nil, nil),
		typedNilTransportClient.doFreshRequestWithResponse(deadline, http.MethodGet, "/safe", nil, nil),
		client.DoReadWithResponse(deadline, http.MethodPost, "/safe", nil, nil),
		client.doReadWithResponsePolicy(deadline, http.MethodGet, "/safe", nil, nil, safeReadRetryPolicy{}, safeReadRetryHooks{}),
	}
	for _, err := range deadlineErrors {
		classification := ClassifyHTTPFailure(err)
		if classification.Kind != HTTPFailureContractOrLocal || classification.RequestDispatched {
			t.Fatalf("deadline hid terminal/local configuration: %#v / %v", classification, err)
		}
		for _, rendered := range []string{err.Error(), fmt.Sprintf("%+v", err), fmt.Sprintf("%#v", err), fmt.Sprint(classification)} {
			if strings.Contains(rendered, "pre-dispatch-secret") {
				t.Fatalf("pre-dispatch configuration leaked: %q", rendered)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("pre-dispatch cases invoked transport %d times", calls.Load())
	}
}
