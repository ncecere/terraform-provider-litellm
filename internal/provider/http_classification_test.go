package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type deterministicDeadlineContext struct {
	done chan struct{}
}

func (c *deterministicDeadlineContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *deterministicDeadlineContext) Done() <-chan struct{}       { return c.done }
func (c *deterministicDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}
func (c *deterministicDeadlineContext) Value(interface{}) interface{} { return nil }

func TestClassifyHTTPFailureKindsAndWrappedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		kind HTTPFailureKind
	}{
		{"canceled", fmt.Errorf("wrapped: %w", safeTransportFailure(context.Canceled)), HTTPFailureCanceled},
		{"deadline", fmt.Errorf("wrapped: %w", safeTransportFailure(context.DeadlineExceeded)), HTTPFailureDeadline},
		{"transient transport", fmt.Errorf("wrapped: %w", safeTransportFailure(temporaryTransportTestError{})), HTTPFailureTransientTransport},
		{"transient response 408", fmt.Errorf("wrapped: %w", &APIError{StatusCode: http.StatusRequestTimeout}), HTTPFailureTransientResponse},
		{"transient response 429", &APIError{StatusCode: http.StatusTooManyRequests}, HTTPFailureTransientResponse},
		{"transient response 5xx", &APIError{StatusCode: http.StatusBadGateway}, HTTPFailureTransientResponse},
		{"terminal response", fmt.Errorf("wrapped: %w", &APIError{StatusCode: http.StatusNotFound}), HTTPFailureTerminalResponse},
		{"transient response read failure", &safeResponseError{statusCode: http.StatusServiceUnavailable, dispatched: true}, HTTPFailureTransientResponse},
		{"terminal response read failure", &safeResponseError{statusCode: http.StatusNotFound, dispatched: true}, HTTPFailureTerminalResponse},
		{"local", errors.New("local contract detail"), HTTPFailureContractOrLocal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ClassifyHTTPFailure(test.err); got.Kind != test.kind {
				t.Fatalf("kind = %v, want %v: %#v", got.Kind, test.kind, got)
			}
		})
	}
}

func TestHTTPFailureClassificationCapturesOnlySafeMutationUncertainty(t *testing.T) {
	t.Parallel()

	secret := "transport-cause-secret"
	client := &Client{
		APIBase: "https://example.invalid",
		APIKey:  "api-secret",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("%s: %w", secret, syscall.ECONNRESET)
		})},
	}
	err := client.DoRequestWithResponse(context.Background(), http.MethodPost, "/mutation/secret", map[string]string{"token": "payload-secret"}, nil)
	classification := ClassifyHTTPFailure(err)
	if classification.Kind != HTTPFailureTransientTransport || !classification.RequestDispatched || classification.ResponseAccepted {
		t.Fatalf("transport classification = %#v", classification)
	}
	if strings.Contains(fmt.Sprint(classification), secret) || strings.Contains(err.Error(), secret) {
		t.Fatalf("classification retained transport cause: %#v / %q", classification, err)
	}

	acceptedClient := testRetryClient(func(request *http.Request) (*http.Response, error) {
		response := testHTTPResponse(request, http.StatusOK, nil)
		response.Body = io.NopCloser(strings.NewReader("not-json"))
		return response, nil
	})
	var result map[string]interface{}
	acceptedErr := acceptedClient.DoRequestWithResponse(context.Background(), http.MethodPost, "/mutation", nil, &result)
	accepted := ClassifyHTTPFailure(fmt.Errorf("wrapped: %w", acceptedErr))
	if accepted.Kind != HTTPFailureContractOrLocal || !accepted.RequestDispatched || !accepted.ResponseAccepted || accepted.StatusCode != http.StatusOK {
		t.Fatalf("accepted classification = %#v", accepted)
	}
	terminal := ClassifyHTTPFailure(&APIError{StatusCode: http.StatusBadRequest})
	if !terminal.RequestDispatched || terminal.ResponseAccepted {
		t.Fatalf("terminal response uncertainty = %#v", terminal)
	}
	localErr := acceptedClient.DoRequestWithResponse(context.Background(), http.MethodPost, "/mutation", map[string]interface{}{"unsupported": make(chan int)}, nil)
	local := ClassifyHTTPFailure(localErr)
	if local.Kind != HTTPFailureContractOrLocal || local.RequestDispatched || local.ResponseAccepted {
		t.Fatalf("local classification = %#v", local)
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	maximum := 30 * time.Second
	tests := []struct {
		name  string
		value string
		want  time.Duration
		ok    bool
	}{
		{"delta", "12", 12 * time.Second, true},
		{"zero", "0", 0, true},
		{"date", now.Add(17 * time.Second).Format(http.TimeFormat), 17 * time.Second, true},
		{"negative delta", "-1", 0, false},
		{"past date", now.Add(-time.Second).Format(http.TimeFormat), 0, false},
		{"malformed", "tomorrow", 0, false},
		{"whitespace", " 1", 0, false},
		{"overflow", "18446744073709551616", 0, false},
		{"oversized delta", "31", 0, false},
		{"oversized date", now.Add(31 * time.Second).Format(http.TimeFormat), 0, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := parseRetryAfter(test.value, now, maximum)
			if got != test.want || ok != test.ok {
				t.Fatalf("parseRetryAfter returned %v/%t, want %v/%t", got, ok, test.want, test.ok)
			}
		})
	}
	ambiguous := http.Header{"Retry-After": []string{"1", "2"}}
	if delay, ok := safeRetryAfter(ambiguous, now, maximum); ok || delay != 0 {
		t.Fatalf("ambiguous Retry-After was retained: %v/%t", delay, ok)
	}
}

func TestDoReadWithResponseRetriesOnlyAllowedFailures(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, 599} {
		status := status
		t.Run(fmt.Sprintf("retry-%d", status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				if calls.Add(1) == 1 {
					return testHTTPResponse(request, status, http.Header{"Retry-After": []string{"0"}}), nil
				}
				return testHTTPResponse(request, http.StatusOK, nil), nil
			})
			if err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, nil, testReadPolicy(3), noWaitRetryHooks()); err != nil || calls.Load() != 2 {
				t.Fatalf("status %d: calls=%d error=%v", status, calls.Load(), err)
			}
		})
	}

	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		status := status
		t.Run(fmt.Sprintf("terminal-%d", status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return testHTTPResponse(request, status, nil), nil
			})
			err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, nil, testReadPolicy(3), noWaitRetryHooks())
			if !IsAPIErrorStatus(err, status) || calls.Load() != 1 {
				t.Fatalf("status %d: calls=%d error=%v", status, calls.Load(), err)
			}
		})
	}

	t.Run("transient transport", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return nil, temporaryTransportTestError{}
			}
			return testHTTPResponse(request, http.StatusOK, nil), nil
		})
		if err := client.doReadWithResponsePolicy(context.Background(), http.MethodHead, "/safe", nil, nil, testReadPolicy(3), noWaitRetryHooks()); err != nil || calls.Load() != 2 {
			t.Fatalf("calls=%d error=%v", calls.Load(), err)
		}
	})

	t.Run("contract failure", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			response := testHTTPResponse(request, http.StatusOK, nil)
			response.Body = io.NopCloser(strings.NewReader("not-json"))
			return response, nil
		})
		var result map[string]interface{}
		err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, &result, testReadPolicy(3), noWaitRetryHooks())
		if err == nil || calls.Load() != 1 || ClassifyHTTPFailure(err).Kind != HTTPFailureContractOrLocal {
			t.Fatalf("calls=%d classification=%#v error=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})
}

func TestDoReadWithResponseCancellationDuringRequest(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	var calls atomic.Int32
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		close(started)
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.doReadWithResponsePolicy(ctx, http.MethodGet, "/safe", nil, nil, testReadPolicy(3), noWaitRetryHooks())
	}()
	<-started
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) || ClassifyHTTPFailure(err).Kind != HTTPFailureCanceled || calls.Load() != 1 {
		t.Fatalf("calls=%d classification=%#v error=%v", calls.Load(), ClassifyHTTPFailure(err), err)
	}
}

func TestDoReadWithResponseCancellationDuringBackoff(t *testing.T) {
	t.Parallel()

	backoffStarted := make(chan struct{})
	var calls atomic.Int32
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return testHTTPResponse(request, http.StatusServiceUnavailable, nil), nil
	})
	hooks := noWaitRetryHooks()
	hooks.sleep = func(ctx context.Context, _ time.Duration) error {
		close(backoffStarted)
		<-ctx.Done()
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- client.doReadWithResponsePolicy(ctx, http.MethodGet, "/safe", nil, nil, testReadPolicy(3), hooks)
	}()
	<-backoffStarted
	cancel()
	err := <-done
	if !errors.Is(err, context.Canceled) || ClassifyHTTPFailure(err).Kind != HTTPFailureCanceled || calls.Load() != 1 {
		t.Fatalf("calls=%d classification=%#v error=%v", calls.Load(), ClassifyHTTPFailure(err), err)
	}
}

func TestSafeReadRetryDeadlineAttemptsAndJitterBounds(t *testing.T) {
	t.Parallel()

	t.Run("parent deadline interrupts request", func(t *testing.T) {
		started := make(chan struct{})
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			close(started)
			<-request.Context().Done()
			return nil, request.Context().Err()
		})
		ctx := &deterministicDeadlineContext{done: make(chan struct{})}
		done := make(chan error, 1)
		go func() {
			done <- client.doReadWithResponsePolicy(ctx, http.MethodGet, "/safe", nil, nil, testReadPolicy(5), noWaitRetryHooks())
		}()
		<-started
		close(ctx.done)
		err := <-done
		if ClassifyHTTPFailure(err).Kind != HTTPFailureDeadline || calls.Load() != 1 {
			t.Fatalf("calls=%d classification=%#v error=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})

	t.Run("attempt bound", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return testHTTPResponse(request, http.StatusServiceUnavailable, nil), nil
		})
		err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, nil, testReadPolicy(3), noWaitRetryHooks())
		if !IsAPIErrorStatus(err, http.StatusServiceUnavailable) || calls.Load() != 3 {
			t.Fatalf("calls=%d error=%v", calls.Load(), err)
		}
	})

	t.Run("elapsed bound", func(t *testing.T) {
		now := time.Unix(1_700_000_000, 0)
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return testHTTPResponse(request, http.StatusServiceUnavailable, nil), nil
		})
		hooks := noWaitRetryHooks()
		hooks.now = func() time.Time { return now }
		hooks.sleep = func(ctx context.Context, delay time.Duration) error {
			now = now.Add(delay)
			return nil
		}
		policy := testReadPolicy(10)
		policy.maxElapsed = time.Second
		policy.initialDelay = 2 * time.Second
		policy.maxDelay = 2 * time.Second
		err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, nil, policy, hooks)
		if ClassifyHTTPFailure(err).Kind != HTTPFailureDeadline || calls.Load() != 1 {
			t.Fatalf("calls=%d classification=%#v error=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})

	policy := testReadPolicy(4)
	policy.initialDelay = 100 * time.Millisecond
	policy.maxDelay = 250 * time.Millisecond
	for _, unit := range []float64{-1, 0, .5, 1, 2} {
		hooks := noWaitRetryHooks()
		hooks.randomUnit = func() float64 { return unit }
		for attempt := 1; attempt <= 4; attempt++ {
			delay := safeReadRetryDelay(policy, hooks, attempt)
			base := time.Duration(100*(1<<(attempt-1))) * time.Millisecond
			if base > policy.maxDelay {
				base = policy.maxDelay
			}
			if delay < base/2 || delay > base || delay > policy.maxDelay {
				t.Fatalf("unit=%v attempt=%d delay=%v base=%v", unit, attempt, delay, base)
			}
		}
	}
}

func TestDoReadWithResponseRetryAfterAndMethodBoundary(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var slept time.Duration
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return testHTTPResponse(request, http.StatusTooManyRequests, http.Header{"Retry-After": []string{"3"}}), nil
		}
		return testHTTPResponse(request, http.StatusOK, nil), nil
	})
	hooks := noWaitRetryHooks()
	hooks.sleep = func(_ context.Context, delay time.Duration) error { slept = delay; return nil }
	if err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, nil, testReadPolicy(3), hooks); err != nil || calls.Load() != 2 || slept != 3*time.Second {
		t.Fatalf("calls=%d slept=%v error=%v", calls.Load(), slept, err)
	}

	policy := testReadPolicy(2)
	policy.maxRetryAfter = 5 * time.Second
	calls.Store(0)
	slept = 0
	err := retrySafeRead(context.Background(), policy, hooks, func(context.Context) error {
		if calls.Add(1) == 1 {
			return &APIError{StatusCode: http.StatusTooManyRequests, retryAfter: time.Minute, hasRetryAfter: true}
		}
		return nil
	})
	if err != nil || calls.Load() != 2 || slept != policy.maxRetryAfter {
		t.Fatalf("bounded Retry-After calls=%d slept=%v error=%v", calls.Load(), slept, err)
	}

	var dispatches atomic.Int32
	boundaryClient := testRetryClient(func(request *http.Request) (*http.Response, error) {
		dispatches.Add(1)
		return testHTTPResponse(request, http.StatusServiceUnavailable, nil), nil
	})
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		err := boundaryClient.DoReadWithResponse(context.Background(), method, "/mutation", nil, nil)
		if err == nil || ClassifyHTTPFailure(err).Kind != HTTPFailureContractOrLocal {
			t.Fatalf("method %s classification=%#v error=%v", method, ClassifyHTTPFailure(err), err)
		}
	}
	if dispatches.Load() != 0 {
		t.Fatalf("safe read dispatched %d mutation methods", dispatches.Load())
	}

	err = boundaryClient.DoRequestWithResponse(context.Background(), http.MethodPost, "/mutation", nil, nil)
	if !IsAPIErrorStatus(err, http.StatusServiceUnavailable) || dispatches.Load() != 1 {
		t.Fatalf("single-attempt mutation dispatches=%d error=%v", dispatches.Load(), err)
	}
}

func TestFreshConnectionSamplingRemainsSingleAttempt(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	server := httptestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusServiceUnavailable)
	})
	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
	err := client.doFreshRequestWithResponse(context.Background(), http.MethodGet, "/fresh", nil, nil)
	if !IsAPIErrorStatus(err, http.StatusServiceUnavailable) || calls.Load() != 1 {
		t.Fatalf("fresh calls=%d error=%v", calls.Load(), err)
	}
}

func TestMisleadingHTTP500NeverClassifiesAbsenceOrLeaksContent(t *testing.T) {
	t.Parallel()

	secret := "sk-secret-model-tool-path"
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		response := testHTTPResponse(request, http.StatusInternalServerError, http.Header{"Retry-After": []string{"invalid-" + secret}})
		response.Status = "500 raw-secret-reason-" + secret
		response.Header.Set("Content-Type", "application/json")
		response.Body = io.NopCloser(strings.NewReader(`{"detail":"404 not found ` + secret + ` ` + request.URL.String() + `"}`))
		return response, nil
	})
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/model/tool/secret-path", nil, nil)
	classification := ClassifyHTTPFailure(fmt.Errorf("wrapped: %w", err))
	if IsNotFoundError(err) || IsAPIErrorStatus(err, http.StatusNotFound) || classification.Kind != HTTPFailureTransientResponse || classification.StatusCode != http.StatusInternalServerError || classification.HasRetryAfter {
		t.Fatalf("misleading 500 classification=%#v error=%v", classification, err)
	}
	for _, rendered := range []string{err.Error(), fmt.Sprint(classification)} {
		for _, forbidden := range []string{secret, "raw-secret-reason", "404 not found", "example.invalid", "/model", "Retry-After"} {
			if strings.Contains(rendered, forbidden) {
				t.Fatalf("content-free error exposed %q: %q", forbidden, rendered)
			}
		}
	}
}

func testReadPolicy(attempts int) safeReadRetryPolicy {
	return safeReadRetryPolicy{
		maxAttempts:   attempts,
		maxElapsed:    10 * time.Second,
		initialDelay:  time.Millisecond,
		maxDelay:      time.Second,
		maxRetryAfter: 5 * time.Second,
	}
}

func noWaitRetryHooks() safeReadRetryHooks {
	return safeReadRetryHooks{
		now:        time.Now,
		sleep:      func(context.Context, time.Duration) error { return nil },
		randomUnit: func() float64 { return 0 },
	}
}

func testRetryClient(roundTrip func(*http.Request) (*http.Response, error)) *Client {
	return &Client{
		APIBase: "https://example.invalid",
		APIKey:  "admin",
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			return roundTrip(request)
		})},
	}
}

func testHTTPResponse(request *http.Request, status int, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: headers, Body: http.NoBody, Request: request}
}

func httptestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}
