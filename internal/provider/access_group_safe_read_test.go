package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func accessGroupSafeReadBody(t *testing.T, accessGroup string, modelNames ...string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"access_group":     accessGroup,
		"model_names":      modelNames,
		"deployment_count": len(modelNames),
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func accessGroupSafeReadModel(accessGroup string) AccessGroupResourceModel {
	return AccessGroupResourceModel{
		ID:          types.StringValue(accessGroup),
		AccessGroup: types.StringValue(accessGroup),
		ModelNames:  accessGroupStringList("prior-model"),
	}
}

func refreshAccessGroupWithTestPolicy(ctx context.Context, client *Client, data *AccessGroupResourceModel, policy safeReadRetryPolicy, hooks safeReadRetryHooks) error {
	accessGroup := data.AccessGroup.ValueString()
	if accessGroup == "" {
		accessGroup = data.ID.ValueString()
	}
	var result map[string]interface{}
	if err := client.doReadWithResponsePolicy(ctx, http.MethodGet, endpointWithPathSegment("/access_group/", accessGroup, "/info"), nil, &result, policy, hooks); err != nil {
		return err
	}
	return projectAccessGroupResourceAPIObject(ctx, data, result, accessGroup)
}

func TestAccessGroupOrdinaryRefreshRetriesTransientSequenceWithCanonicalURI(t *testing.T) {
	accessGroup := "group ?percent=% unicode-雪"
	endpoint := endpointWithPathSegment("/access_group/", accessGroup, "/info")
	var calls atomic.Int32
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.RequestURI() != endpoint {
			t.Fatalf("request=%s %s want=%s", request.Method, request.URL.RequestURI(), endpoint)
		}
		switch calls.Add(1) {
		case 1:
			return nil, syscall.ECONNRESET
		case 2:
			return searchToolTestResponse(request, http.StatusServiceUnavailable, []byte(`{"detail":"temporary-body-secret"}`), http.Header{"Retry-After": []string{"0"}}), nil
		default:
			return searchToolTestResponse(request, http.StatusOK, accessGroupSafeReadBody(t, accessGroup, "z-model", "a-model"), nil), nil
		}
	})
	data := accessGroupSafeReadModel(accessGroup)
	if err := refreshAccessGroupWithTestPolicy(context.Background(), client, &data, testReadPolicy(4), noWaitRetryHooks()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || !reflect.DeepEqual(accessGroupListStrings(t, data.ModelNames), []string{"a-model", "z-model"}) {
		t.Fatalf("calls=%d data=%#v", calls.Load(), data)
	}
}

func TestAccessGroupSafeReadStatusAndProjectionSequences(t *testing.T) {
	const accessGroup = "access-sequence"
	policy := testReadPolicy(4)

	t.Run("complete exact 404 is terminal", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return searchToolTestResponse(request, http.StatusNotFound, []byte(`{"detail":"missing"}`), nil), nil
		})
		data := accessGroupSafeReadModel(accessGroup)
		err := refreshAccessGroupWithTestPolicy(context.Background(), client, &data, policy, noWaitRetryHooks())
		if !IsAPIErrorStatus(err, http.StatusNotFound) || calls.Load() != 1 {
			t.Fatalf("calls=%d classification=%#v err=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})

	t.Run("misleading 400 body is terminal and not absence", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return searchToolTestResponse(request, http.StatusBadRequest, []byte(`{"detail":"404 not found; retry 503; private-body"}`), nil), nil
		})
		data := accessGroupSafeReadModel(accessGroup)
		err := refreshAccessGroupWithTestPolicy(context.Background(), client, &data, policy, noWaitRetryHooks())
		if !IsAPIErrorStatus(err, http.StatusBadRequest) || IsAPIErrorStatus(err, http.StatusNotFound) || calls.Load() != 1 {
			t.Fatalf("calls=%d classification=%#v err=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})

	t.Run("incomplete nominal 404 retries then succeeds", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				response := searchToolTestResponse(request, http.StatusNotFound, nil, nil)
				response.Body = failingReadCloser{err: io.ErrUnexpectedEOF}
				response.ContentLength = -1
				return response, nil
			}
			return searchToolTestResponse(request, http.StatusOK, accessGroupSafeReadBody(t, accessGroup, "new-model"), nil), nil
		})
		data := accessGroupSafeReadModel(accessGroup)
		if err := refreshAccessGroupWithTestPolicy(context.Background(), client, &data, policy, noWaitRetryHooks()); err != nil || calls.Load() != 2 {
			t.Fatalf("calls=%d err=%v", calls.Load(), err)
		}
	})

	t.Run("retry exhaustion is bounded and atomic", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return searchToolTestResponse(request, http.StatusBadGateway, []byte(`{"detail":"unavailable"}`), nil), nil
		})
		data := accessGroupSafeReadModel(accessGroup)
		prior := data
		err := refreshAccessGroupWithTestPolicy(context.Background(), client, &data, policy, noWaitRetryHooks())
		if !IsAPIErrorStatus(err, http.StatusBadGateway) || calls.Load() != int32(policy.maxAttempts) || !reflect.DeepEqual(data, prior) {
			t.Fatalf("calls=%d changed=%t err=%v", calls.Load(), !reflect.DeepEqual(data, prior), err)
		}
	})

	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "malformed success", body: []byte(`{"access_group":`)},
		{name: "identity mismatch", body: accessGroupSafeReadBody(t, "other-access", "new-model")},
		{name: "malformed membership", body: []byte(`{"access_group":"access-sequence","model_names":["ok",1]}`)},
	} {
		t.Run(test.name+" is terminal and atomic", func(t *testing.T) {
			var calls atomic.Int32
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return searchToolTestResponse(request, http.StatusOK, test.body, nil), nil
			})
			data := accessGroupSafeReadModel(accessGroup)
			prior := data
			err := refreshAccessGroupWithTestPolicy(context.Background(), client, &data, policy, noWaitRetryHooks())
			if err == nil || calls.Load() != 1 || !reflect.DeepEqual(data, prior) {
				t.Fatalf("calls=%d changed=%t err=%v", calls.Load(), !reflect.DeepEqual(data, prior), err)
			}
		})
	}
}

func TestAccessGroupRetryAfterCancellationAndMutationConfirmationPolicy(t *testing.T) {
	t.Run("retry after is bounded", func(t *testing.T) {
		const accessGroup = "retry-after-access"
		var calls atomic.Int32
		var slept time.Duration
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return searchToolTestResponse(request, http.StatusTooManyRequests, []byte(`{"detail":"rate-limit-body"}`), http.Header{"Retry-After": []string{"30"}}), nil
			}
			return searchToolTestResponse(request, http.StatusOK, accessGroupSafeReadBody(t, accessGroup, "model"), nil), nil
		})
		policy := testReadPolicy(3)
		policy.maxRetryAfter = 2 * time.Second
		hooks := noWaitRetryHooks()
		hooks.sleep = func(_ context.Context, delay time.Duration) error { slept = delay; return nil }
		data := accessGroupSafeReadModel(accessGroup)
		if err := refreshAccessGroupWithTestPolicy(context.Background(), client, &data, policy, hooks); err != nil || calls.Load() != 2 || slept != policy.maxRetryAfter {
			t.Fatalf("calls=%d slept=%s err=%v", calls.Load(), slept, err)
		}
	})

	for _, test := range []struct {
		name    string
		context func() (context.Context, context.CancelFunc)
	}{
		{name: "cancellation", context: func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, func() {}
		}},
		{name: "deadline", context: func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), time.Nanosecond)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return searchToolTestResponse(request, http.StatusServiceUnavailable, nil, nil), nil
			})
			data := accessGroupSafeReadModel("context-access")
			prior := data
			ctx, cancel := test.context()
			defer cancel()
			err := refreshAccessGroupWithTestPolicy(ctx, client, &data, testReadPolicy(4), noWaitRetryHooks())
			if err == nil || !reflect.DeepEqual(data, prior) || calls.Load() > 1 {
				t.Fatalf("calls=%d changed=%t err=%v", calls.Load(), !reflect.DeepEqual(data, prior), err)
			}
		})
	}

	t.Run("mutation confirmation stays single attempt", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return searchToolTestResponse(request, http.StatusServiceUnavailable, []byte(`{"detail":"temporary"}`), nil), nil
		})
		resource := &AccessGroupResource{client: client}
		data := accessGroupSafeReadModel("mutation-access")
		if err := resource.readAccessGroup(context.Background(), &data); err == nil || calls.Load() != 1 {
			t.Fatalf("calls=%d err=%v", calls.Load(), err)
		}
	})
}

func TestAccessGroupSafeReadConcurrentPoliciesAreIsolated(t *testing.T) {
	var calls atomic.Int32
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return searchToolTestResponse(request, http.StatusServiceUnavailable, nil, nil), nil
	})
	var wait sync.WaitGroup
	for _, attempts := range []int{2, 4} {
		attempts := attempts
		wait.Add(1)
		go func() {
			defer wait.Done()
			data := accessGroupSafeReadModel("parallel-access")
			_ = refreshAccessGroupWithTestPolicy(context.Background(), client, &data, testReadPolicy(attempts), noWaitRetryHooks())
		}()
	}
	wait.Wait()
	if calls.Load() != 6 {
		t.Fatalf("calls=%d want=6", calls.Load())
	}
}
