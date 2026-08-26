package provider

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type advancingFailReadCloser struct {
	advance func()
	err     error
}

func (r *advancingFailReadCloser) Read([]byte) (int, error) {
	if r.advance != nil {
		r.advance()
		r.advance = nil
	}
	return 0, r.err
}
func (*advancingFailReadCloser) Close() error { return nil }

func TestProviderRequestsNeverFollowRedirects(t *testing.T) {
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			status, method := status, method
			t.Run(fmt.Sprintf("%s-%d", method, status), func(t *testing.T) {
				var originCalls, targetCalls, configuredRedirectCalls atomic.Int32
				const secretLocation = "/redirect-target/location-secret"
				server := httptestServer(t, func(writer http.ResponseWriter, request *http.Request) {
					switch request.URL.Path {
					case "/origin":
						originCalls.Add(1)
						writer.Header().Set("Location", secretLocation)
						writer.Header().Set("Content-Type", "text/plain")
						writer.WriteHeader(status)
						_, _ = writer.Write([]byte("redirect to " + secretLocation))
					case secretLocation:
						targetCalls.Add(1)
						writer.WriteHeader(http.StatusOK)
					}
				})
				configuredCheckRedirect := func(*http.Request, []*http.Request) error {
					configuredRedirectCalls.Add(1)
					return nil
				}
				shared := &http.Client{
					Transport:     server.Client().Transport,
					CheckRedirect: configuredCheckRedirect,
					Timeout:       7 * time.Second,
				}
				client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: shared}
				err := client.DoRequestWithResponse(context.Background(), method, "/origin", map[string]string{"value": "one"}, nil)
				classification := ClassifyHTTPFailure(err)
				if classification.Kind != HTTPFailureTerminalResponse || classification.StatusCode != status ||
					!classification.RequestDispatched || classification.ResponseAccepted {
					t.Fatalf("classification = %#v, error = %v", classification, err)
				}
				if originCalls.Load() != 1 || targetCalls.Load() != 0 || configuredRedirectCalls.Load() != 0 {
					t.Fatalf("origin=%d target=%d configured_redirect=%d", originCalls.Load(), targetCalls.Load(), configuredRedirectCalls.Load())
				}
				if err == nil || strings.Contains(err.Error(), "redirect-target") || strings.Contains(err.Error(), "location-secret") {
					t.Fatalf("redirect location appeared in safe error: %v", err)
				}
				if shared.Transport != server.Client().Transport || shared.Timeout != 7*time.Second ||
					reflect.ValueOf(shared.CheckRedirect).Pointer() != reflect.ValueOf(configuredCheckRedirect).Pointer() {
					t.Fatal("provider mutated the shared HTTP client")
				}
			})
		}
	}
}

func TestSafeReadAttemptsNeverCreateRedirectTransportRequests(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		method := method
		t.Run(method, func(t *testing.T) {
			var originCalls, targetCalls atomic.Int32
			server := httptestServer(t, func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/never-follow" {
					targetCalls.Add(1)
					writer.WriteHeader(http.StatusOK)
					return
				}
				originCalls.Add(1)
				writer.Header().Set("Location", "/never-follow")
				writer.WriteHeader(http.StatusServiceUnavailable)
			})
			client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}
			err := client.doReadWithResponsePolicy(context.Background(), method, "/origin", nil, nil, testReadPolicy(3), noWaitRetryHooks())
			if !IsAPIErrorStatus(err, http.StatusServiceUnavailable) || originCalls.Load() != 3 || targetCalls.Load() != 0 {
				t.Fatalf("origin=%d target=%d error=%v", originCalls.Load(), targetCalls.Load(), err)
			}
		})
	}

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
			var calls atomic.Int32
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return testHTTPResponse(request, status, http.Header{"Location": []string{"https://location.invalid/secret"}}), nil
			})
			err := client.doReadWithResponsePolicy(context.Background(), method, "/origin", nil, nil, testReadPolicy(3), noWaitRetryHooks())
			if !IsAPIErrorStatus(err, status) || calls.Load() != 1 || strings.Contains(err.Error(), "location.invalid") {
				t.Fatalf("%s %d calls=%d error=%v", method, status, calls.Load(), err)
			}
		}
	}
}

func TestRedirectPolicyDoesNotMutateOrRaceSharedClient(t *testing.T) {
	var originCalls, targetCalls, configuredRedirectCalls atomic.Int32
	server := httptestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/target" {
			targetCalls.Add(1)
			writer.WriteHeader(http.StatusOK)
			return
		}
		originCalls.Add(1)
		writer.Header().Set("Location", "/target")
		writer.WriteHeader(http.StatusTemporaryRedirect)
	})
	configuredCheckRedirect := func(*http.Request, []*http.Request) error {
		configuredRedirectCalls.Add(1)
		return nil
	}
	shared := &http.Client{Transport: server.Client().Transport, CheckRedirect: configuredCheckRedirect, Timeout: 9 * time.Second}
	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: shared}

	const requests = 64
	var wait sync.WaitGroup
	errorsSeen := make(chan error, requests)
	for i := 0; i < requests; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsSeen <- client.DoRequestWithResponse(context.Background(), http.MethodGet, "/origin", nil, nil)
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if !IsAPIErrorStatus(err, http.StatusTemporaryRedirect) {
			t.Fatalf("concurrent redirect error = %v", err)
		}
	}
	if originCalls.Load() != requests || targetCalls.Load() != 0 || configuredRedirectCalls.Load() != 0 {
		t.Fatalf("origin=%d target=%d configured_redirect=%d", originCalls.Load(), targetCalls.Load(), configuredRedirectCalls.Load())
	}
	if shared.Timeout != 9*time.Second || reflect.ValueOf(shared.CheckRedirect).Pointer() != reflect.ValueOf(configuredCheckRedirect).Pointer() {
		t.Fatal("shared client configuration changed")
	}
}

func TestSafeReadRetriesTransientAcceptedBodyReadsOnly(t *testing.T) {
	causes := []struct {
		name string
		err  error
	}{
		{"reset", syscall.ECONNRESET},
		{"unexpected-eof", io.ErrUnexpectedEOF},
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, cause := range causes {
			method, cause := method, cause
			t.Run(method+"-"+cause.name, func(t *testing.T) {
				var calls atomic.Int32
				client := testRetryClient(func(request *http.Request) (*http.Response, error) {
					if calls.Add(1) == 1 {
						response := testHTTPResponse(request, http.StatusOK, nil)
						response.Body = failingReadCloser{err: cause.err}
						return response, nil
					}
					response := testHTTPResponse(request, http.StatusOK, nil)
					response.Body = io.NopCloser(strings.NewReader(`{"ok":true}`))
					return response, nil
				})
				var result map[string]interface{}
				if err := client.doReadWithResponsePolicy(context.Background(), method, "/safe", nil, &result, testReadPolicy(2), noWaitRetryHooks()); err != nil || calls.Load() != 2 {
					t.Fatalf("calls=%d error=%v", calls.Load(), err)
				}
			})
		}
	}

	for _, body := range []string{"", "null", "not-json"} {
		body := body
		t.Run("contract-"+body, func(t *testing.T) {
			var calls atomic.Int32
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				response := testHTTPResponse(request, http.StatusOK, nil)
				response.Body = io.NopCloser(strings.NewReader(body))
				return response, nil
			})
			var result map[string]interface{}
			err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, &result, testReadPolicy(3), noWaitRetryHooks())
			classification := ClassifyHTTPFailure(err)
			if err == nil || calls.Load() != 1 || classification.Kind != HTTPFailureContractOrLocal ||
				!classification.RequestDispatched || !classification.ResponseAccepted {
				t.Fatalf("calls=%d classification=%#v error=%v", calls.Load(), classification, err)
			}
			// Phase-1 safe reads must not inherit the older specialized loops'
			// intentional retry of empty/malformed successful responses.
			if !shouldRetryCredentialRecoveryRead(err) || !isRetryableTeamMemberAddReadError(err) || !shouldRetryUnifiedAccessGroupVerification(err) {
				t.Fatalf("legacy successful-response predicates changed for %q", body)
			}
		})
	}
}

func TestAcceptedBodyReadClassificationIsContentFree(t *testing.T) {
	const secret = "body-read-secret"
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		response := testHTTPResponse(request, http.StatusOK, nil)
		response.Body = failingReadCloser{err: fmt.Errorf("%s: %w", secret, io.ErrUnexpectedEOF)}
		return response, nil
	})
	var result map[string]interface{}
	err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, &result)
	classification := ClassifyHTTPFailure(err)
	if classification.Kind != HTTPFailureTransientAcceptedResponse || classification.StatusCode != http.StatusOK ||
		!classification.RequestDispatched || !classification.ResponseAccepted {
		t.Fatalf("classification = %#v, error = %v", classification, err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(fmt.Sprint(classification), secret) {
		t.Fatalf("raw body-read cause escaped: %#v / %v", classification, err)
	}
	if shouldRetryCredentialRecoveryRead(err) || isRetryableTeamMemberAddReadError(err) || shouldRetryUnifiedAccessGroupVerification(err) {
		t.Fatal("broader accepted-body safe-read category changed a specialized retry loop")
	}
}

func TestLegacySpecializedRetryPredicatesRemainExact(t *testing.T) {
	baseline := []struct {
		name string
		err  error
		want bool
	}{
		{"deadline", context.DeadlineExceeded, true},
		{"temporary", temporaryTransportTestError{}, true},
		{"connection-reset", syscall.ECONNRESET, true},
		{"canceled", context.Canceled, false},
		{"generic", errors.New("terminal configuration"), false},
	}
	for _, test := range baseline {
		err := safeTransportFailure(&url.Error{Op: "Get", URL: "https://secret.invalid", Err: test.err})
		var transportErr *safeTransportError
		if !errors.As(err, &transportErr) || transportErr.Retryable() != test.want || transportErr.Temporary() != test.want {
			t.Fatalf("%s legacy transport predicate = %#v, want %t", test.name, transportErr, test.want)
		}
		if shouldRetryCredentialRecoveryRead(err) != test.want || isRetryableTeamMemberAddReadError(err) != test.want ||
			shouldRetryUnifiedAccessGroupVerification(err) != test.want {
			t.Fatalf("%s specialized predicates changed: credential=%t team=%t access_group=%t want=%t",
				test.name, shouldRetryCredentialRecoveryRead(err), isRetryableTeamMemberAddReadError(err), shouldRetryUnifiedAccessGroupVerification(err), test.want)
		}
	}

	broaderSafeReadOnly := []error{
		io.EOF,
		io.ErrUnexpectedEOF,
		syscall.ECONNABORTED,
		syscall.ECONNREFUSED,
		syscall.EPIPE,
		syscall.ENETDOWN,
		syscall.ENETUNREACH,
		syscall.EHOSTUNREACH,
	}
	for _, cause := range broaderSafeReadOnly {
		err := safeTransportFailure(&url.Error{Op: "Get", URL: "https://secret.invalid", Err: cause})
		var transportErr *safeTransportError
		if !errors.As(err, &transportErr) || transportErr.Retryable() || transportErr.Temporary() {
			t.Fatalf("%v changed legacy Retryable/Temporary: %#v", cause, transportErr)
		}
		if ClassifyHTTPFailure(err).Kind != HTTPFailureTransientTransport {
			t.Fatalf("%v was not available to the separate safe-read classifier", cause)
		}
		if shouldRetryCredentialRecoveryRead(err) || isRetryableTeamMemberAddReadError(err) || shouldRetryUnifiedAccessGroupVerification(err) {
			t.Fatalf("%v broadened a specialized phase-1 loop", cause)
		}
	}
}

func TestTerminalTransportTraitsDominateJoinedTransients(t *testing.T) {
	terminalCauses := []struct {
		name string
		err  error
	}{
		{"certificate", x509.UnknownAuthorityError{}},
		{"tls-protocol", tls.RecordHeaderError{}},
		{"http-protocol", &http.ProtocolError{ErrorString: "invalid response framing"}},
		{"configuration", net.InvalidAddrError("invalid local address")},
	}
	for _, terminalCause := range terminalCauses {
		terminalCause := terminalCause
		t.Run(terminalCause.name, func(t *testing.T) {
			for _, reverse := range []bool{false, true} {
				joined := errors.Join(terminalCause.err, temporaryTransportTestError{})
				if reverse {
					joined = errors.Join(temporaryTransportTestError{}, fmt.Errorf("wrapped terminal: %w", terminalCause.err))
				}

				transportErr := safeTransportFailure(fmt.Errorf("outer wrapper: %w", joined))
				var safeErr *safeTransportError
				if !errors.As(transportErr, &safeErr) {
					t.Fatal("safe transport error was not retained")
				}
				classification := ClassifyHTTPFailure(transportErr)
				if classification.Kind != HTTPFailureContractOrLocal || safeErr.safeReadTransient ||
					safeErr.Retryable() || safeErr.Temporary() || shouldRetryCredentialRecoveryRead(transportErr) ||
					isRetryableTeamMemberAddReadError(transportErr) || shouldRetryUnifiedAccessGroupVerification(transportErr) {
					t.Fatalf("reverse=%t classification=%#v transport=%#v", reverse, classification, safeErr)
				}
			}

			var calls atomic.Int32
			client := testRetryClient(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, errors.Join(terminalCause.err, temporaryTransportTestError{})
			})
			err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, nil, testReadPolicy(3), noWaitRetryHooks())
			classification := ClassifyHTTPFailure(err)
			var safeErr *safeTransportError
			if !errors.As(err, &safeErr) || calls.Load() != 1 || classification.Kind != HTTPFailureContractOrLocal ||
				!classification.RequestDispatched || classification.ResponseAccepted || safeErr.safeReadTransient ||
				safeErr.Retryable() || safeErr.Temporary() {
				t.Fatalf("calls=%d classification=%#v transport=%#v error=%v", calls.Load(), classification, safeErr, err)
			}
		})
	}
}

func TestTransportTraitPriorityIsDeterministicAcrossJoinedErrors(t *testing.T) {
	terminal := x509.UnknownAuthorityError{}
	transient := temporaryTransportTestError{}
	tests := []struct {
		name            string
		err             error
		kind            HTTPFailureKind
		legacyRetryable bool
	}{
		{"cancellation-over-all", errors.Join(transient, terminal, context.DeadlineExceeded, context.Canceled), HTTPFailureCanceled, false},
		{"cancellation-over-all-reversed", errors.Join(context.Canceled, context.DeadlineExceeded, terminal, transient), HTTPFailureCanceled, false},
		{"terminal-over-deadline-and-transient", errors.Join(transient, terminal, context.DeadlineExceeded), HTTPFailureContractOrLocal, false},
		{"terminal-over-deadline-and-transient-reversed", errors.Join(context.DeadlineExceeded, terminal, transient), HTTPFailureContractOrLocal, false},
		{"deadline-over-transient", errors.Join(transient, context.DeadlineExceeded), HTTPFailureDeadline, true},
		{"terminal-over-transient", errors.Join(transient, terminal), HTTPFailureContractOrLocal, false},
		{"transient-over-opaque", errors.Join(errors.New("opaque"), transient), HTTPFailureTransientTransport, true},
		{"opaque", errors.New("opaque"), HTTPFailureContractOrLocal, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := safeTransportFailure(fmt.Errorf("wrapped: %w", test.err))
			classification := ClassifyHTTPFailure(err)
			var safeErr *safeTransportError
			if !errors.As(err, &safeErr) || classification.Kind != test.kind ||
				safeErr.Retryable() != test.legacyRetryable || safeErr.Temporary() != test.legacyRetryable {
				t.Fatalf("classification=%#v transport=%#v", classification, safeErr)
			}
		})
	}
}

func TestAcceptedBodyReadTerminalTraitsAreNotSafeReadTransient(t *testing.T) {
	var calls atomic.Int32
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		response := testHTTPResponse(request, http.StatusOK, nil)
		response.Body = failingReadCloser{err: errors.Join(x509.UnknownAuthorityError{}, context.DeadlineExceeded, temporaryTransportTestError{})}
		return response, nil
	})
	var result map[string]interface{}
	err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, &result, testReadPolicy(3), noWaitRetryHooks())
	classification := ClassifyHTTPFailure(err)
	var responseErr *safeResponseError
	if !errors.As(err, &responseErr) || calls.Load() != 1 || classification.Kind != HTTPFailureContractOrLocal ||
		!classification.RequestDispatched || !classification.ResponseAccepted || responseErr.safeReadTransient || responseErr.Temporary() {
		t.Fatalf("calls=%d classification=%#v response=%#v error=%v", calls.Load(), classification, responseErr, err)
	}
}

func TestPreCanceledContextsAreNeverDispatched(t *testing.T) {
	for _, test := range []struct {
		name string
		ctx  func() (context.Context, context.CancelFunc)
		kind HTTPFailureKind
	}{
		{"canceled", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}, HTTPFailureCanceled},
		{"deadline", func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}, HTTPFailureDeadline},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			client := testRetryClient(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, errors.New("must not be called")
			})
			ctx, cancel := test.ctx()
			defer cancel()
			err := client.DoRequestWithResponse(ctx, http.MethodGet, "/safe", nil, nil)
			classification := ClassifyHTTPFailure(err)
			if calls.Load() != 0 || classification.Kind != test.kind || classification.RequestDispatched || classification.ResponseAccepted {
				t.Fatalf("calls=%d classification=%#v error=%v", calls.Load(), classification, err)
			}
		})
	}
}

func TestTransportInvocationConservativelyMarksFailuresDispatched(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("opaque transport failure")} {
		var calls atomic.Int32
		client := testRetryClient(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, cause
		})
		err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, nil)
		classification := ClassifyHTTPFailure(err)
		if calls.Load() != 1 || !classification.RequestDispatched || classification.ResponseAccepted {
			t.Fatalf("cause=%v calls=%d classification=%#v error=%v", cause, calls.Load(), classification, err)
		}
	}
}

func TestRetryAfterSurvivesTransientStatusBodyReadFailures(t *testing.T) {
	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			response := testHTTPResponse(request, status, http.Header{"Retry-After": []string{"3"}})
			response.Body = failingReadCloser{err: io.ErrUnexpectedEOF}
			return response, nil
		})
		err := client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, nil)
		classification := ClassifyHTTPFailure(err)
		if classification.Kind != HTTPFailureTransientTransport || classification.StatusCode != status ||
			classification.RetryAfter != 3*time.Second || !classification.HasRetryAfter ||
			!classification.RequestDispatched || classification.ResponseAccepted {
			t.Fatalf("status=%d classification=%#v error=%v", status, classification, err)
		}
	}
}

func TestRetryAfterSchedulingAccountsForSlowBodyReads(t *testing.T) {
	start := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	for _, test := range []struct {
		name        string
		header      func(time.Time) string
		bodyAdvance time.Duration
		wantSleep   time.Duration
	}{
		{"delta", func(time.Time) string { return "5" }, 4 * time.Second, 5 * time.Second},
		{"date", func(now time.Time) string { return now.Add(5 * time.Second).Format(http.TimeFormat) }, 4 * time.Second, time.Second},
		{"expired-date", func(now time.Time) string { return now.Add(5 * time.Second).Format(http.TimeFormat) }, 7 * time.Second, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := start
			var calls atomic.Int32
			var slept time.Duration
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				if calls.Add(1) == 1 {
					response := testHTTPResponse(request, http.StatusServiceUnavailable, http.Header{"Retry-After": []string{test.header(now)}})
					response.Body = &advancingFailReadCloser{
						advance: func() { now = now.Add(test.bodyAdvance) },
						err:     io.ErrUnexpectedEOF,
					}
					return response, nil
				}
				return testHTTPResponse(request, http.StatusOK, nil), nil
			})
			hooks := noWaitRetryHooks()
			hooks.now = func() time.Time { return now }
			hooks.sleep = func(_ context.Context, delay time.Duration) error {
				slept = delay
				now = now.Add(delay)
				return nil
			}
			policy := testReadPolicy(2)
			policy.maxRetryAfter = 10 * time.Second
			if err := client.doReadWithResponsePolicy(context.Background(), http.MethodGet, "/safe", nil, nil, policy, hooks); err != nil || calls.Load() != 2 || slept != test.wantSleep {
				t.Fatalf("calls=%d slept=%v want=%v error=%v", calls.Load(), slept, test.wantSleep, err)
			}
		})
	}
}

func TestHTTPFailureDispatchAndAcceptanceFlagsByStage(t *testing.T) {
	assert := func(t *testing.T, err error, kind HTTPFailureKind, status int, dispatched, accepted bool) {
		t.Helper()
		classification := ClassifyHTTPFailure(err)
		if classification.Kind != kind || classification.StatusCode != status ||
			classification.RequestDispatched != dispatched || classification.ResponseAccepted != accepted {
			t.Fatalf("classification=%#v error=%v", classification, err)
		}
	}

	t.Run("encoding", func(t *testing.T) {
		client := testRetryClient(func(*http.Request) (*http.Response, error) { t.Fatal("dispatched encoding failure"); return nil, nil })
		assert(t, client.DoRequestWithResponse(context.Background(), http.MethodPost, "/safe", make(chan int), nil), HTTPFailureContractOrLocal, 0, false, false)
	})
	t.Run("request-creation", func(t *testing.T) {
		client := &Client{APIBase: "://invalid", APIKey: "admin", HTTPClient: http.DefaultClient}
		assert(t, client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, nil), HTTPFailureContractOrLocal, 0, false, false)
	})
	t.Run("transport", func(t *testing.T) {
		client := testRetryClient(func(*http.Request) (*http.Response, error) { return nil, errors.New("opaque") })
		assert(t, client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, nil), HTTPFailureContractOrLocal, 0, true, false)
	})
	t.Run("status", func(t *testing.T) {
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			return testHTTPResponse(request, http.StatusBadRequest, nil), nil
		})
		assert(t, client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, nil), HTTPFailureTerminalResponse, http.StatusBadRequest, true, false)
	})
	t.Run("status-body-read", func(t *testing.T) {
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			response := testHTTPResponse(request, http.StatusBadRequest, nil)
			response.Body = failingReadCloser{err: io.ErrUnexpectedEOF}
			return response, nil
		})
		assert(t, client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, nil), HTTPFailureTransientTransport, http.StatusBadRequest, true, false)
	})
	t.Run("accepted-body-read", func(t *testing.T) {
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			response := testHTTPResponse(request, http.StatusOK, nil)
			response.Body = failingReadCloser{err: io.ErrUnexpectedEOF}
			return response, nil
		})
		assert(t, client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, nil), HTTPFailureTransientAcceptedResponse, http.StatusOK, true, true)
	})
	t.Run("json-contract", func(t *testing.T) {
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			response := testHTTPResponse(request, http.StatusOK, nil)
			response.Body = io.NopCloser(strings.NewReader("not-json"))
			return response, nil
		})
		var result map[string]interface{}
		assert(t, client.DoRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, &result), HTTPFailureContractOrLocal, http.StatusOK, true, true)
	})
	t.Run("no-content", func(t *testing.T) {
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			return testHTTPResponse(request, http.StatusNoContent, nil), nil
		})
		accepted, err := client.doRequestWithResponse(context.Background(), http.MethodDelete, "/safe", nil, nil)
		if err != nil || !accepted {
			t.Fatalf("accepted=%t error=%v", accepted, err)
		}
		var result map[string]interface{}
		accepted, err = client.doRequestWithResponse(context.Background(), http.MethodGet, "/safe", nil, &result)
		if !accepted {
			t.Fatal("204 response was not retained as accepted")
		}
		assert(t, err, HTTPFailureContractOrLocal, http.StatusNoContent, true, true)
	})
}
