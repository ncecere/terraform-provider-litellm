package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

func searchToolTestResponse(request *http.Request, status int, body []byte, headers http.Header) *http.Response {
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

func searchToolTestBody(t *testing.T, id string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"search_tool_id":   id,
		"search_tool_name": "refreshed-name",
		"litellm_params": map[string]interface{}{
			"search_provider": "refreshed-provider",
			"api_base":        "https://response.invalid/private",
			"timeout":         12.5,
			"max_retries":     3,
		},
		"search_tool_info": map[string]interface{}{"category": "private-category"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func searchToolTestModel(id string) SearchToolResourceModel {
	return SearchToolResourceModel{
		ID:             types.StringValue(id),
		SearchToolID:   types.StringValue(id),
		SearchToolName: types.StringValue("prior-name"),
		SearchProvider: types.StringValue("prior-provider"),
		APIKey:         types.StringValue("prior-api-key-secret"),
		APIBase:        types.StringValue("https://prior.invalid/private"),
		Timeout:        types.Float64Value(1.5),
		MaxRetries:     types.Int64Value(1),
		SearchToolInfo: types.StringValue(`{"prior":"private-state"}`),
	}
}

func refreshSearchToolWithTestPolicy(ctx context.Context, client *Client, data *SearchToolResourceModel, imported bool, policy safeReadRetryPolicy, hooks safeReadRetryHooks) error {
	id := data.SearchToolID.ValueString()
	if id == "" {
		id = data.ID.ValueString()
	}
	endpoint := endpointWithPathSegment("/search_tools/", id, "")
	var result map[string]interface{}
	if err := client.doReadWithResponsePolicy(ctx, http.MethodGet, endpoint, nil, &result, policy, hooks); err != nil {
		return err
	}
	return projectSearchToolResourceAPIObject(data, result, id, imported)
}

func TestSearchToolOrdinaryRefreshRetriesTransientSequenceWithCanonicalURI(t *testing.T) {
	id := "tool ?percent=% unicode-雪"
	endpoint := endpointWithPathSegment("/search_tools/", id, "")
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
			return searchToolTestResponse(request, http.StatusOK, searchToolTestBody(t, id), nil), nil
		}
	})
	data := searchToolTestModel(id)
	if err := refreshSearchToolWithTestPolicy(context.Background(), client, &data, false, testReadPolicy(4), noWaitRetryHooks()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || data.SearchToolName.ValueString() != "refreshed-name" || data.APIKey.ValueString() != "prior-api-key-secret" {
		t.Fatalf("calls=%d data=%#v", calls.Load(), data)
	}
}

func TestSearchToolSafeReadStatusBodyAndProjectionSequences(t *testing.T) {
	const id = "search-sequence"
	policy := testReadPolicy(4)
	body := searchToolTestBody(t, id)

	t.Run("complete exact 404 is terminal", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return searchToolTestResponse(request, http.StatusNotFound, []byte(`{"detail":"missing"}`), nil), nil
		})
		data := searchToolTestModel(id)
		err := refreshSearchToolWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks())
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
		data := searchToolTestModel(id)
		err := refreshSearchToolWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks())
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
			return searchToolTestResponse(request, http.StatusOK, body, nil), nil
		})
		data := searchToolTestModel(id)
		if err := refreshSearchToolWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks()); err != nil || calls.Load() != 2 {
			t.Fatalf("calls=%d err=%v", calls.Load(), err)
		}
	})

	t.Run("retry exhaustion is bounded", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return searchToolTestResponse(request, http.StatusBadGateway, []byte(`{"detail":"unavailable"}`), nil), nil
		})
		data := searchToolTestModel(id)
		prior := data
		err := refreshSearchToolWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks())
		if !IsAPIErrorStatus(err, http.StatusBadGateway) || calls.Load() != int32(policy.maxAttempts) || !reflect.DeepEqual(data, prior) {
			t.Fatalf("calls=%d changed=%t err=%v", calls.Load(), !reflect.DeepEqual(data, prior), err)
		}
	})

	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "malformed success", body: []byte(`{"search_tool_id":`)},
		{name: "identity mismatch", body: searchToolTestBody(t, "other-search")},
		{name: "malformed projection", body: []byte(`{"search_tool_id":"search-sequence","search_tool_name":"name","litellm_params":{"search_provider":"provider"},"search_tool_info":[]}`)},
		{name: "malformed api base", body: []byte(`{"search_tool_id":"search-sequence","search_tool_name":"name","litellm_params":{"search_provider":"provider","api_base":123}}`)},
	} {
		t.Run(test.name+" is terminal and atomic", func(t *testing.T) {
			var calls atomic.Int32
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return searchToolTestResponse(request, http.StatusOK, test.body, nil), nil
			})
			data := searchToolTestModel(id)
			prior := data
			err := refreshSearchToolWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks())
			if err == nil || calls.Load() != 1 || !reflect.DeepEqual(data, prior) {
				t.Fatalf("calls=%d changed=%t err=%v", calls.Load(), !reflect.DeepEqual(data, prior), err)
			}
		})
	}
}

func TestSearchToolAuthoritativeAPIBaseAbsenceProjectsNullAtomically(t *testing.T) {
	const id = "api-base-absence"
	for _, test := range []struct {
		name       string
		includeKey bool
	}{
		{name: "explicit null", includeKey: true},
		{name: "omitted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := map[string]interface{}{
				"search_tool_id":   id,
				"search_tool_name": "name",
				"litellm_params":   map[string]interface{}{"search_provider": "provider"},
			}
			if test.includeKey {
				result["litellm_params"].(map[string]interface{})["api_base"] = nil
			}
			data := searchToolTestModel(id)
			if err := projectSearchToolResourceAPIObject(&data, result, id, false); err != nil {
				t.Fatal(err)
			}
			if !data.APIBase.IsNull() {
				t.Fatalf("api_base = %#v", data.APIBase)
			}
		})
	}
}

func TestSearchToolRetryAfterIsBoundedWithInjectedTiming(t *testing.T) {
	const id = "retry-after-search"
	var calls atomic.Int32
	var slept time.Duration
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return searchToolTestResponse(request, http.StatusTooManyRequests, []byte(`{"detail":"rate-limit-body"}`), http.Header{"Retry-After": []string{"30"}}), nil
		}
		return searchToolTestResponse(request, http.StatusOK, searchToolTestBody(t, id), nil), nil
	})
	policy := testReadPolicy(3)
	policy.maxRetryAfter = 2 * time.Second
	hooks := noWaitRetryHooks()
	hooks.sleep = func(_ context.Context, delay time.Duration) error {
		slept = delay
		return nil
	}
	data := searchToolTestModel(id)
	if err := refreshSearchToolWithTestPolicy(context.Background(), client, &data, false, policy, hooks); err != nil || calls.Load() != 2 || slept != policy.maxRetryAfter {
		t.Fatalf("calls=%d slept=%s err=%v", calls.Load(), slept, err)
	}
}

func TestSearchToolSafeReadCancellationDeadlineAndSlashAreLocal(t *testing.T) {
	for _, test := range []struct {
		name     string
		id       string
		context  func() (context.Context, context.CancelFunc)
		wantKind HTTPFailureKind
	}{
		{
			name: "cancellation",
			id:   "canceled-search",
			context: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			wantKind: HTTPFailureCanceled,
		},
		{
			name: "deadline",
			id:   "deadline-search",
			context: func() (context.Context, context.CancelFunc) {
				return context.WithDeadline(context.Background(), time.Unix(1, 0))
			},
			wantKind: HTTPFailureDeadline,
		},
		{
			name: "slash-bearing identity",
			id:   "private/search",
			context: func() (context.Context, context.CancelFunc) {
				return context.Background(), func() {}
			},
			wantKind: HTTPFailureContractOrLocal,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return searchToolTestResponse(request, http.StatusOK, nil, nil), errors.New("unexpected dispatch")
			})
			ctx, cancel := test.context()
			defer cancel()
			data := searchToolTestModel(test.id)
			err := refreshSearchToolWithTestPolicy(ctx, client, &data, false, testReadPolicy(3), noWaitRetryHooks())
			if ClassifyHTTPFailure(err).Kind != test.wantKind || calls.Load() != 0 {
				t.Fatalf("calls=%d classification=%#v err=%v", calls.Load(), ClassifyHTTPFailure(err), err)
			}
		})
	}
}

func TestSearchToolConfirmationListAndMutationsRemainSingleAttempt(t *testing.T) {
	const id = "single-attempt-search"
	var mu sync.Mutex
	calls := map[string]int{}
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		calls[request.Method+" "+request.URL.RequestURI()]++
		mu.Unlock()
		return searchToolTestResponse(request, http.StatusServiceUnavailable, []byte(`{"detail":"temporary"}`), nil), nil
	})

	resource := &SearchToolResource{client: client}
	data := searchToolTestModel(id)
	if err := resource.readSearchTool(context.Background(), &data); !IsAPIErrorStatus(err, http.StatusServiceUnavailable) {
		t.Fatalf("confirmation err=%v", err)
	}
	if _, err := fetchEnvelopeListObjects(context.Background(), client, "/search_tools/list", "search_tools", "search tool item"); !IsAPIErrorStatus(err, http.StatusServiceUnavailable) {
		t.Fatalf("list err=%v", err)
	}
	for _, mutation := range []struct {
		method string
		path   string
	}{
		{method: http.MethodPost, path: "/search_tools"},
		{method: http.MethodPut, path: endpointWithPathSegment("/search_tools/", id, "")},
		{method: http.MethodDelete, path: endpointWithPathSegment("/search_tools/", id, "")},
	} {
		if err := client.DoRequestWithResponse(context.Background(), mutation.method, mutation.path, map[string]string{"secret": "request-value"}, nil); !IsAPIErrorStatus(err, http.StatusServiceUnavailable) {
			t.Fatalf("%s %s err=%v", mutation.method, mutation.path, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	for key, count := range calls {
		if count != 1 {
			t.Fatalf("%s calls=%d", key, count)
		}
	}
	if len(calls) != 5 {
		t.Fatalf("calls=%v", calls)
	}
}
