package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type closeFailureBody struct {
	reader   io.Reader
	readErr  error
	closeErr error
	closes   atomic.Int32
}

func (b *closeFailureBody) Read(p []byte) (int, error) {
	if b.reader != nil {
		n, err := b.reader.Read(p)
		if n != 0 || err != io.EOF {
			return n, err
		}
		b.reader = nil
	}
	if b.readErr != nil {
		err := b.readErr
		b.readErr = nil
		return 0, err
	}
	return 0, io.EOF
}

func (b *closeFailureBody) Close() error {
	b.closes.Add(1)
	return b.closeErr
}

func TestResponseCloseFailuresUseIncompleteResponseClassification(t *testing.T) {
	opaque := errors.New("opaque close failure")
	for _, test := range []struct {
		name     string
		status   int
		closeErr error
		kind     HTTPFailureKind
		accepted bool
	}{
		{"accepted-transient", http.StatusOK, io.ErrUnexpectedEOF, HTTPFailureTransientAcceptedResponse, true},
		{"accepted-opaque", http.StatusNoContent, opaque, HTTPFailureContractOrLocal, true},
		{"not-found-transient", http.StatusNotFound, io.ErrUnexpectedEOF, HTTPFailureTransientTransport, false},
		{"not-found-opaque", http.StatusNotFound, opaque, HTTPFailureContractOrLocal, false},
		{"unavailable-opaque", http.StatusServiceUnavailable, opaque, HTTPFailureContractOrLocal, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := &closeFailureBody{reader: strings.NewReader(`{}`), closeErr: test.closeErr}
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				response := testHTTPResponse(request, test.status, nil)
				response.Body = body
				response.ContentLength = -1
				return response, nil
			})
			err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, nil)
			classification := ClassifyHTTPFailure(err)
			if err == nil || body.closes.Load() != 1 || classification.Kind != test.kind ||
				classification.StatusCode != test.status || classification.ResponseAccepted != test.accepted ||
				IsAPIErrorStatus(err, test.status) || IsNotFoundError(err) {
				t.Fatalf("closes=%d classification=%#v error=%v", body.closes.Load(), classification, err)
			}
		})
	}
}

func TestReadAndCloseFailuresAggregateOrderIndependently(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusServiceUnavailable} {
		for _, reverse := range []bool{false, true} {
			readErr, closeErr := error(io.ErrUnexpectedEOF), error(x509.UnknownAuthorityError{})
			if reverse {
				readErr, closeErr = closeErr, readErr
			}
			body := &closeFailureBody{readErr: readErr, closeErr: closeErr}
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				response := testHTTPResponse(request, status, nil)
				response.Body = body
				response.ContentLength = -1
				return response, nil
			})
			err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, nil)
			classification := ClassifyHTTPFailure(err)
			if classification.Kind != HTTPFailureContractOrLocal || classification.StatusCode != 0 ||
				body.closes.Load() != 1 || IsAPIErrorStatus(err, status) || IsNotFoundError(err) {
				t.Fatalf("status=%d reverse=%t closes=%d classification=%#v error=%v",
					status, reverse, body.closes.Load(), classification, err)
			}
		}
	}
}

func TestIncompleteStatusBodyNeverEstablishesExactStatus(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusBadRequest, http.StatusServiceUnavailable} {
		incomplete := &safeResponseError{
			statusCode: status, kind: "incomplete response", stage: safeResponseFailureStatusBodyRead, dispatched: true,
		}
		for _, err := range []error{
			incomplete,
			errors.Join(incomplete, &APIError{StatusCode: status}),
			errors.Join(&APIError{StatusCode: status}, incomplete),
		} {
			classification := ClassifyHTTPFailure(err)
			if classification.StatusCode != status || IsAPIErrorStatus(err, status) || IsNotFoundError(err) {
				t.Fatalf("status=%d classification=%#v error=%v", status, classification, err)
			}
		}
	}
}

func TestEveryAPIErrorNodeParticipatesInGlobalStatusAccounting(t *testing.T) {
	for _, invalid := range []int{0, -1, 600} {
		for _, valid := range []int{http.StatusNotFound, http.StatusServiceUnavailable} {
			for _, err := range []error{
				errors.Join(&APIError{StatusCode: invalid}, &APIError{StatusCode: valid}),
				errors.Join(&APIError{StatusCode: valid}, &APIError{StatusCode: invalid}),
			} {
				classification := ClassifyHTTPFailure(err)
				if classification.StatusCode != 0 || classification.HasRetryAfter ||
					IsAPIErrorStatus(err, valid) || IsNotFoundError(err) {
					t.Fatalf("invalid=%d valid=%d classification=%#v", invalid, valid, classification)
				}
			}
		}
	}

	scheduled := func() error {
		return withSafeRetrySchedule(&APIError{StatusCode: http.StatusServiceUnavailable}, safeRetryAfterSpec{delay: time.Second}, true)
	}
	classification := ClassifyHTTPFailure(errors.Join(scheduled(), scheduled()))
	if classification.StatusCode != http.StatusServiceUnavailable || !classification.HasRetryAfter ||
		classification.RetryAfter != time.Second {
		t.Fatalf("matching response schedules were not retained: %#v", classification)
	}
}

type staticErrorWrapper struct{ child error }

func (*staticErrorWrapper) Error() string   { return "static wrapper" }
func (e *staticErrorWrapper) Unwrap() error { return e.child }

type staticJoinedErrorWrapper struct{ children []error }

func (*staticJoinedErrorWrapper) Error() string     { return "static joined wrapper" }
func (e *staticJoinedErrorWrapper) Unwrap() []error { return e.children }

func TestCompleteTreeTraversalSkipsProviderTypedNils(t *testing.T) {
	var api *APIError
	var transport *safeTransportError
	var response *safeResponseError
	var scheduled *safeRetryScheduledError
	typedNils := []error{api, transport, response, scheduled}
	valid := &APIError{StatusCode: http.StatusNotFound}

	for _, typedNil := range typedNils {
		for _, err := range []error{
			&staticJoinedErrorWrapper{children: []error{typedNil, valid}},
			&staticJoinedErrorWrapper{children: []error{valid, typedNil}},
			&staticErrorWrapper{child: &staticJoinedErrorWrapper{children: []error{typedNil, valid}}},
		} {
			classification := ClassifyHTTPFailure(err)
			if classification.Kind != HTTPFailureTerminalResponse || classification.StatusCode != http.StatusNotFound ||
				!IsAPIErrorStatus(err, http.StatusNotFound) {
				t.Fatalf("typed nil %T fabricated traits: %#v", typedNil, classification)
			}
		}
		if classification := ClassifyHTTPFailure(typedNil); classification.Kind != HTTPFailureNone {
			t.Fatalf("root typed nil %T classification=%#v", typedNil, classification)
		}
	}
}

type cancelOnErrCheckContext struct {
	checks atomic.Int32
	after  int32
	done   chan struct{}
}

func (c *cancelOnErrCheckContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelOnErrCheckContext) Done() <-chan struct{}       { return c.done }
func (c *cancelOnErrCheckContext) Err() error {
	if c.checks.Add(1) >= c.after {
		select {
		case <-c.done:
		default:
			close(c.done)
		}
		return context.Canceled
	}
	return nil
}
func (*cancelOnErrCheckContext) Value(interface{}) interface{} { return nil }

type cancelingJSONMarshaler struct {
	cancel context.CancelFunc
	err    error
}

func (m cancelingJSONMarshaler) MarshalJSON() ([]byte, error) {
	m.cancel()
	if m.err != nil {
		return nil, m.err
	}
	return []byte(`{}`), nil
}

func TestCancellationDuringURLAndFreshValidation(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("must not dispatch")
	})

	urlContext := &cancelOnErrCheckContext{after: 3, done: make(chan struct{})}
	invalidURLClient := &Client{APIBase: "://invalid", APIKey: "admin", HTTPClient: &http.Client{Transport: transport}}
	urlErr := invalidURLClient.DoRequestWithResponse(urlContext, http.MethodGet, "/safe", nil, nil)
	if classification := ClassifyHTTPFailure(urlErr); classification.Kind != HTTPFailureCanceled ||
		classification.RequestDispatched || calls.Load() != 0 {
		t.Fatalf("URL checks=%d calls=%d classification=%#v error=%v",
			urlContext.checks.Load(), calls.Load(), classification, urlErr)
	}

	freshContext := &cancelOnErrCheckContext{after: 5, done: make(chan struct{})}
	freshClient := &Client{APIBase: "https://example.invalid", APIKey: "admin", HTTPClient: &http.Client{Transport: transport}}
	freshErr := freshClient.doFreshRequestWithResponse(freshContext, http.MethodGet, "/safe", nil, nil)
	if classification := ClassifyHTTPFailure(freshErr); classification.Kind != HTTPFailureCanceled ||
		classification.RequestDispatched || calls.Load() != 0 {
		t.Fatalf("fresh checks=%d calls=%d classification=%#v error=%v",
			freshContext.checks.Load(), calls.Load(), classification, freshErr)
	}
}

func TestCancellationDuringMarshalPrecedesAllValidation(t *testing.T) {
	for _, marshalErr := range []error{nil, errors.New("marshal failed")} {
		ctx, cancel := context.WithCancel(context.Background())
		var calls atomic.Int32
		client := testRetryClient(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("must not dispatch")
		})
		client.APIBase = "://invalid"
		err := client.DoRequestWithResponse(ctx, "BAD METHOD", "/safe", cancelingJSONMarshaler{cancel: cancel, err: marshalErr}, nil)
		classification := ClassifyHTTPFailure(err)
		if classification.Kind != HTTPFailureCanceled || classification.RequestDispatched || calls.Load() != 0 {
			t.Fatalf("marshalErr=%v calls=%d classification=%#v error=%v", marshalErr, calls.Load(), classification, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	var calls atomic.Int32
	client := testRetryClient(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return nil, errors.New("must not dispatch")
	})
	err := client.doFreshRequestWithResponse(ctx, http.MethodGet, "/safe", cancelingJSONMarshaler{cancel: cancel}, nil)
	if classification := ClassifyHTTPFailure(err); classification.Kind != HTTPFailureCanceled ||
		classification.RequestDispatched || calls.Load() != 0 {
		t.Fatalf("fresh calls=%d classification=%#v error=%v", calls.Load(), classification, err)
	}
}

func TestRetryBoundaryWinsAfterEveryOperationAttempt(t *testing.T) {
	for _, attempts := range []int{1, 3} {
		t.Run("cancel", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			var calls atomic.Int32
			err := retrySafeRead(ctx, testReadPolicy(attempts), noWaitRetryHooks(), func(context.Context) error {
				calls.Add(1)
				cancel()
				return &APIError{StatusCode: http.StatusServiceUnavailable}
			})
			classification := ClassifyHTTPFailure(err)
			if classification.Kind != HTTPFailureCanceled || !classification.RequestDispatched ||
				classification.ResponseAccepted || calls.Load() != 1 {
				t.Fatalf("attempts=%d calls=%d classification=%#v", attempts, calls.Load(), classification)
			}
		})

		for _, operationErr := range []bool{false, true} {
			t.Run("fake-clock-budget", func(t *testing.T) {
				now := time.Unix(1_700_000_000, 0)
				hooks := noWaitRetryHooks()
				hooks.now = func() time.Time { return now }
				policy := testReadPolicy(attempts)
				policy.maxElapsed = time.Second
				var calls atomic.Int32
				err := retrySafeRead(context.Background(), policy, hooks, func(context.Context) error {
					calls.Add(1)
					now = now.Add(policy.maxElapsed)
					if !operationErr {
						return nil
					}
					return &safeResponseError{
						statusCode: http.StatusOK, kind: "accepted incomplete", stage: safeResponseFailureAcceptedBodyRead,
						dispatched: true, accepted: true, safeReadTransient: true,
					}
				})
				classification := ClassifyHTTPFailure(err)
				if classification.Kind != HTTPFailureDeadline || !classification.RequestDispatched ||
					!classification.ResponseAccepted || calls.Load() != 1 {
					t.Fatalf("attempts=%d operationErr=%t calls=%d classification=%#v",
						attempts, operationErr, calls.Load(), classification)
				}
			})
		}
	}
}
