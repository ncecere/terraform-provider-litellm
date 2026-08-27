package provider

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func readJWTKeyMappingWithTestPolicy(ctx context.Context, client *Client, id string, policy safeReadRetryPolicy, hooks safeReadRetryHooks) (jwtKeyMappingObject, error) {
	var raw json.RawMessage
	if err := client.doReadWithResponsePolicy(ctx, http.MethodGet, jwtKeyMappingInfoEndpoint(id), nil, &raw, policy, hooks); err != nil {
		return jwtKeyMappingObject{}, err
	}
	mapping, err := decodeJWTKeyMappingObject(raw)
	if err != nil {
		return jwtKeyMappingObject{}, err
	}
	if mapping.ID != id {
		return jwtKeyMappingObject{}, errors.New("JWT key mapping response identity did not match the requested UUID")
	}
	return mapping, nil
}

func jwtMappingTestResponse(request *http.Request, status int, body []byte, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{
		StatusCode:    status,
		Header:        headers,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       request,
	}
}

func jwtMappingTestBody(t *testing.T, id string) []byte {
	t.Helper()
	body, err := json.Marshal(jwtMappingJSON(id, "claim-secret", nil, true))
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestJWTKeyMappingOrdinaryReadUsesBoundedRetryTransport(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	body := jwtMappingTestBody(t, jwtMappingID1)
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.RequestURI() != jwtKeyMappingInfoEndpoint(jwtMappingID1) {
			t.Fatalf("request=%s %s", request.Method, request.URL.RequestURI())
		}
		if calls.Add(1) == 1 {
			return jwtMappingTestResponse(request, http.StatusServiceUnavailable, []byte(`{"detail":"temporary"}`), nil), nil
		}
		return jwtMappingTestResponse(request, http.StatusOK, body, nil), nil
	})
	mapping, err := readJWTKeyMapping(context.Background(), client, jwtMappingID1)
	if err != nil || mapping.ID != jwtMappingID1 || calls.Load() != 2 {
		t.Fatalf("mapping=%#v calls=%d err=%v", mapping, calls.Load(), err)
	}
}

func TestJWTKeyMappingSafeReadStatusAndBodySequences(t *testing.T) {
	t.Parallel()

	policy := testReadPolicy(4)
	body := jwtMappingTestBody(t, jwtMappingID1)

	t.Run("complete exact 404 is terminal", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return jwtMappingTestResponse(request, http.StatusNotFound, []byte(`{"detail":"missing"}`), nil), nil
		})
		_, err := readJWTKeyMapping(context.Background(), client, jwtMappingID1)
		if !IsAPIErrorStatus(err, http.StatusNotFound) || calls.Load() != 1 {
			t.Fatalf("calls=%d classification=%#v err=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})

	t.Run("misleading terminal body is not classification input", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return jwtMappingTestResponse(request, http.StatusBadRequest, []byte(`{"detail":"404 not found; retry 503 timeout"}`), nil), nil
		})
		_, err := readJWTKeyMapping(context.Background(), client, jwtMappingID1)
		if !IsAPIErrorStatus(err, http.StatusBadRequest) || IsAPIErrorStatus(err, http.StatusNotFound) || calls.Load() != 1 {
			t.Fatalf("calls=%d classification=%#v err=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})

	t.Run("incomplete 404 retries then succeeds", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				response := jwtMappingTestResponse(request, http.StatusNotFound, nil, nil)
				response.Body = failingReadCloser{err: io.ErrUnexpectedEOF}
				response.ContentLength = -1
				return response, nil
			}
			return jwtMappingTestResponse(request, http.StatusOK, body, nil), nil
		})
		mapping, err := readJWTKeyMappingWithTestPolicy(context.Background(), client, jwtMappingID1, policy, noWaitRetryHooks())
		if err != nil || mapping.ID != jwtMappingID1 || calls.Load() != 2 {
			t.Fatalf("mapping=%#v calls=%d err=%v", mapping, calls.Load(), err)
		}
	})

	t.Run("retry exhaustion is bounded", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return jwtMappingTestResponse(request, http.StatusBadGateway, []byte(`{"detail":"still unavailable"}`), nil), nil
		})
		_, err := readJWTKeyMappingWithTestPolicy(context.Background(), client, jwtMappingID1, policy, noWaitRetryHooks())
		if !IsAPIErrorStatus(err, http.StatusBadGateway) || calls.Load() != int32(policy.maxAttempts) {
			t.Fatalf("calls=%d classification=%#v err=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})

	t.Run("malformed JSON is terminal", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return jwtMappingTestResponse(request, http.StatusOK, []byte(`{"id":`), nil), nil
		})
		_, err := readJWTKeyMapping(context.Background(), client, jwtMappingID1)
		if err == nil || calls.Load() != 1 || ClassifyHTTPFailure(err).Kind != HTTPFailureContractOrLocal {
			t.Fatalf("calls=%d classification=%#v err=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})

	t.Run("identity mismatch is terminal", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		mismatchBody := jwtMappingTestBody(t, jwtMappingID2)
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return jwtMappingTestResponse(request, http.StatusOK, mismatchBody, nil), nil
		})
		_, err := readJWTKeyMapping(context.Background(), client, jwtMappingID1)
		if err == nil || calls.Load() != 1 {
			t.Fatalf("calls=%d err=%v", calls.Load(), err)
		}
	})

	t.Run("terminal TLS failure is not retried", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		client := testRetryClient(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, x509.UnknownAuthorityError{}
		})
		_, err := readJWTKeyMapping(context.Background(), client, jwtMappingID1)
		if err == nil || calls.Load() != 1 || ClassifyHTTPFailure(err).Kind != HTTPFailureContractOrLocal {
			t.Fatalf("calls=%d classification=%#v err=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})

	t.Run("invalid local configuration does not dispatch", func(t *testing.T) {
		t.Parallel()
		var calls atomic.Int32
		client := &Client{APIBase: "://invalid", APIKey: "admin", HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return nil, errors.New("unexpected dispatch")
		})}}
		_, err := readJWTKeyMapping(context.Background(), client, jwtMappingID1)
		if err == nil || calls.Load() != 0 || ClassifyHTTPFailure(err).Kind != HTTPFailureContractOrLocal {
			t.Fatalf("calls=%d classification=%#v err=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})
}

func TestJWTKeyMappingRetryAfterIsBoundedWithInjectedTiming(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	var slept time.Duration
	body := jwtMappingTestBody(t, jwtMappingID1)
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return jwtMappingTestResponse(request, http.StatusTooManyRequests, []byte(`{"detail":"rate limited"}`), http.Header{"Retry-After": []string{"30"}}), nil
		}
		return jwtMappingTestResponse(request, http.StatusOK, body, nil), nil
	})
	policy := testReadPolicy(3)
	policy.maxRetryAfter = 2 * time.Second
	hooks := noWaitRetryHooks()
	hooks.sleep = func(_ context.Context, delay time.Duration) error {
		slept = delay
		return nil
	}
	mapping, err := readJWTKeyMappingWithTestPolicy(context.Background(), client, jwtMappingID1, policy, hooks)
	if err != nil || mapping.ID != jwtMappingID1 || calls.Load() != 2 || slept != policy.maxRetryAfter {
		t.Fatalf("mapping=%#v calls=%d slept=%s err=%v", mapping, calls.Load(), slept, err)
	}
}

func TestJWTKeyMappingSafeReadCancellationAndDeadlinePrecedence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		context  func() (context.Context, context.CancelFunc)
		wantKind HTTPFailureKind
	}{
		{
			name: "cancellation",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			wantKind: HTTPFailureCanceled,
		},
		{
			name: "deadline",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Unix(1, 0))
			},
			wantKind: HTTPFailureDeadline,
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int32
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return jwtMappingTestResponse(request, http.StatusOK, jwtMappingTestBody(t, jwtMappingID1), nil), nil
			})
			ctx, cancel := test.context()
			defer cancel()
			_, err := readJWTKeyMapping(ctx, client, jwtMappingID1)
			if ClassifyHTTPFailure(err).Kind != test.wantKind || calls.Load() != 0 {
				t.Fatalf("calls=%d classification=%#v err=%v", calls.Load(), ClassifyHTTPFailure(err), err)
			}
		})
	}
}

func TestJWTKeyMappingRetryTimingIsRaceIsolatedPerRead(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	calls := map[string]int{}
	bodyByID := map[string][]byte{jwtMappingID1: jwtMappingTestBody(t, jwtMappingID1), jwtMappingID2: jwtMappingTestBody(t, jwtMappingID2)}
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		id := request.URL.Query().Get("id")
		mu.Lock()
		calls[id]++
		attempt := calls[id]
		mu.Unlock()
		if attempt == 1 {
			retryAfter := "1"
			if id == jwtMappingID2 {
				retryAfter = "3"
			}
			return jwtMappingTestResponse(request, http.StatusTooManyRequests, []byte(`{"detail":"retry"}`), http.Header{"Retry-After": []string{retryAfter}}), nil
		}
		return jwtMappingTestResponse(request, http.StatusOK, bodyByID[id], nil), nil
	})

	type result struct {
		id    string
		slept time.Duration
		err   error
	}
	results := make(chan result, 2)
	for _, id := range []string{jwtMappingID1, jwtMappingID2} {
		id := id
		go func() {
			var slept time.Duration
			hooks := noWaitRetryHooks()
			hooks.sleep = func(_ context.Context, delay time.Duration) error { slept = delay; return nil }
			mapping, err := readJWTKeyMappingWithTestPolicy(context.Background(), client, id, testReadPolicy(3), hooks)
			if err == nil && mapping.ID != id {
				err = errors.New("mapping identity changed")
			}
			results <- result{id: id, slept: slept, err: err}
		}()
	}
	for range 2 {
		result := <-results
		want := time.Second
		if result.id == jwtMappingID2 {
			want = 3 * time.Second
		}
		if result.err != nil || result.slept != want {
			t.Fatalf("id=%s slept=%s want=%s err=%v", result.id, result.slept, want, result.err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if calls[jwtMappingID1] != 2 || calls[jwtMappingID2] != 2 {
		t.Fatalf("calls=%v", calls)
	}
}

func TestJWTKeyMappingFreshListAndMutationPathsRemainSingleAttempt(t *testing.T) {
	t.Parallel()

	var freshCalls, listCalls atomic.Int32
	mutationCalls := map[string]int{}
	var mutationMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case jwtKeyMappingInfoPath:
			freshCalls.Add(1)
		case jwtKeyMappingListPath:
			listCalls.Add(1)
		default:
			mutationMu.Lock()
			mutationCalls[request.Method+" "+request.URL.Path]++
			mutationMu.Unlock()
		}
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{"detail":"temporary"}`))
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}

	if _, err := readFreshJWTKeyMapping(context.Background(), client, jwtMappingID1); !IsAPIErrorStatus(err, http.StatusServiceUnavailable) || freshCalls.Load() != 1 {
		t.Fatalf("fresh calls=%d err=%v", freshCalls.Load(), err)
	}
	if _, err := listJWTKeyMappings(context.Background(), client); !IsAPIErrorStatus(err, http.StatusServiceUnavailable) || listCalls.Load() != 1 {
		t.Fatalf("list calls=%d err=%v", listCalls.Load(), err)
	}
	for _, mutation := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, jwtKeyMappingCreatePath},
		{http.MethodPost, jwtKeyMappingUpdatePath},
		{http.MethodPost, jwtKeyMappingDeletePath},
	} {
		var raw json.RawMessage
		err := client.DoRequestWithResponse(context.Background(), mutation.method, mutation.path, map[string]string{"value": "secret"}, &raw)
		if !IsAPIErrorStatus(err, http.StatusServiceUnavailable) {
			t.Fatalf("%s %s err=%v", mutation.method, mutation.path, err)
		}
	}
	mutationMu.Lock()
	defer mutationMu.Unlock()
	for _, mutation := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, jwtKeyMappingCreatePath},
		{http.MethodPost, jwtKeyMappingUpdatePath},
		{http.MethodPost, jwtKeyMappingDeletePath},
	} {
		key := mutation.method + " " + mutation.path
		if mutationCalls[key] != 1 {
			t.Fatalf("%s calls=%d", key, mutationCalls[key])
		}
	}
}
