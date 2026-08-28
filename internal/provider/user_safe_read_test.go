package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func userSafeReadBody(t *testing.T, rootID, nestedID string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"user_id": rootID,
		"user_info": map[string]interface{}{
			"user_id":         nestedID,
			"user_alias":      "refreshed-alias",
			"user_email":      "refreshed@example.invalid",
			"user_role":       "internal_user",
			"teams":           []interface{}{"team-b", "team-a"},
			"models":          []interface{}{"model-new"},
			"max_budget":      12.5,
			"budget_duration": "30d",
			"tpm_limit":       300,
			"rpm_limit":       30,
			"metadata":        map[string]interface{}{"owner": "refreshed"},
			"spend":           1.25,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func userSafeReadModel(userID string) UserResourceModel {
	return UserResourceModel{
		ID:             types.StringValue(userID),
		UserID:         types.StringValue(userID),
		UserAlias:      types.StringValue("prior-alias-secret"),
		UserEmail:      types.StringValue("prior@example.invalid"),
		UserRole:       types.StringValue("internal_user_viewer"),
		Teams:          accessGroupStringList("team-a", "team-b"),
		Models:         accessGroupStringList("prior-model"),
		MaxBudget:      types.Float64Value(1.5),
		BudgetDuration: types.StringValue("1d"),
		TPMLimit:       types.Int64Value(10),
		RPMLimit:       types.Int64Value(1),
		AutoCreateKey:  types.BoolValue(true),
		Metadata:       types.MapValueMust(types.StringType, map[string]attr.Value{"prior": types.StringValue("private-metadata")}),
		Key:            types.StringValue("prior-key-secret"),
	}
}

func refreshUserWithTestPolicy(ctx context.Context, client *Client, data *UserResourceModel, imported bool, policy safeReadRetryPolicy, hooks safeReadRetryHooks) error {
	userID := data.UserID.ValueString()
	if userID == "" {
		userID = data.ID.ValueString()
	}
	endpoint := endpointWithQuery("/user/info", url.Values{"user_id": []string{userID}})
	var result map[string]interface{}
	if err := client.doReadWithResponsePolicy(ctx, http.MethodGet, endpoint, nil, &result, policy, hooks); err != nil {
		return err
	}
	return projectUserResourceAPIObject(ctx, data, result, userID, imported, true)
}

func TestUserOrdinaryRefreshRetriesTransientSequenceWithCanonicalQuery(t *testing.T) {
	userID := "user +plus /slash &and=% percent#hash 雪"
	endpoint := endpointWithQuery("/user/info", url.Values{"user_id": []string{userID}})
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
			return searchToolTestResponse(request, http.StatusOK, userSafeReadBody(t, userID, userID), nil), nil
		}
	})
	data := userSafeReadModel(userID)
	if err := refreshUserWithTestPolicy(context.Background(), client, &data, false, testReadPolicy(4), noWaitRetryHooks()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || data.UserAlias.ValueString() != "refreshed-alias" || data.Key.ValueString() != "prior-key-secret" {
		t.Fatalf("calls=%d data=%#v", calls.Load(), data)
	}
	if got := accessGroupListStrings(t, data.Teams); !reflect.DeepEqual(got, []string{"team-a", "team-b"}) {
		t.Fatalf("equivalent team ordering changed: %v", got)
	}
}

func TestUserSafeReadStatusEnvelopeAndProjectionSequences(t *testing.T) {
	const userID = "user-sequence"
	policy := testReadPolicy(4)

	t.Run("complete exact 404 is terminal", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return searchToolTestResponse(request, http.StatusNotFound, []byte(`{"detail":"missing"}`), nil), nil
		})
		data := userSafeReadModel(userID)
		err := refreshUserWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks())
		if !IsAPIErrorStatus(err, http.StatusNotFound) || calls.Load() != 1 {
			t.Fatalf("calls=%d classification=%#v err=%v", calls.Load(), ClassifyHTTPFailure(err), err)
		}
	})

	t.Run("misleading 400 is terminal and not absence", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return searchToolTestResponse(request, http.StatusBadRequest, []byte(`{"detail":"404 not found; retry 503; private-body"}`), nil), nil
		})
		data := userSafeReadModel(userID)
		err := refreshUserWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks())
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
			return searchToolTestResponse(request, http.StatusOK, userSafeReadBody(t, userID, userID), nil), nil
		})
		data := userSafeReadModel(userID)
		if err := refreshUserWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks()); err != nil || calls.Load() != 2 {
			t.Fatalf("calls=%d err=%v", calls.Load(), err)
		}
	})

	t.Run("retry exhaustion is bounded and atomic", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return searchToolTestResponse(request, http.StatusBadGateway, []byte(`{"detail":"unavailable"}`), nil), nil
		})
		data := userSafeReadModel(userID)
		prior := data
		err := refreshUserWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks())
		if !IsAPIErrorStatus(err, http.StatusBadGateway) || calls.Load() != int32(policy.maxAttempts) || !reflect.DeepEqual(data, prior) {
			t.Fatalf("calls=%d changed=%t err=%v", calls.Load(), !reflect.DeepEqual(data, prior), err)
		}
	})

	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "malformed JSON", body: []byte(`{"user_id":`)},
		{name: "missing envelope", body: []byte(`{"user_id":"user-sequence"}`)},
		{name: "null envelope", body: []byte(`{"user_id":"user-sequence","user_info":null}`)},
		{name: "empty envelope", body: []byte(`{"user_id":"user-sequence","user_info":{}}`)},
		{name: "root mismatch", body: userSafeReadBody(t, "wrong-root", userID)},
		{name: "nested mismatch", body: userSafeReadBody(t, userID, "wrong-nested")},
		{name: "malformed string", body: []byte(`{"user_id":"user-sequence","user_info":{"user_id":"user-sequence","user_alias":7}}`)},
		{name: "malformed unowned string", body: []byte(`{"user_id":"user-sequence","user_info":{"user_id":"user-sequence","user_role":false}}`)},
		{name: "malformed number", body: []byte(`{"user_id":"user-sequence","user_info":{"user_id":"user-sequence","max_budget":"secret"}}`)},
		{name: "malformed collection", body: []byte(`{"user_id":"user-sequence","user_info":{"user_id":"user-sequence","teams":["ok",1]}}`)},
	} {
		t.Run(test.name+" is terminal and atomic", func(t *testing.T) {
			var calls atomic.Int32
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				calls.Add(1)
				return searchToolTestResponse(request, http.StatusOK, test.body, nil), nil
			})
			data := userSafeReadModel(userID)
			prior := data
			err := refreshUserWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks())
			if err == nil || calls.Load() != 1 || !reflect.DeepEqual(data, prior) {
				t.Fatalf("calls=%d changed=%t err=%v", calls.Load(), !reflect.DeepEqual(data, prior), err)
			}
		})
	}
}

func TestUserSafeReadRetryAfterContextAndMutationConfirmationPolicy(t *testing.T) {
	t.Run("retry after is bounded", func(t *testing.T) {
		const userID = "retry-after-user"
		var calls atomic.Int32
		var slept time.Duration
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return searchToolTestResponse(request, http.StatusTooManyRequests, []byte(`{"detail":"rate-limit-body"}`), http.Header{"Retry-After": []string{"30"}}), nil
			}
			return searchToolTestResponse(request, http.StatusOK, userSafeReadBody(t, userID, userID), nil), nil
		})
		policy := testReadPolicy(3)
		policy.maxRetryAfter = 2 * time.Second
		hooks := noWaitRetryHooks()
		hooks.sleep = func(_ context.Context, delay time.Duration) error { slept = delay; return nil }
		data := userSafeReadModel(userID)
		if err := refreshUserWithTestPolicy(context.Background(), client, &data, false, policy, hooks); err != nil || calls.Load() != 2 || slept != policy.maxRetryAfter {
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
			data := userSafeReadModel("context-user")
			prior := data
			ctx, cancel := test.context()
			defer cancel()
			err := refreshUserWithTestPolicy(ctx, client, &data, false, testReadPolicy(4), noWaitRetryHooks())
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
		resource := &UserResource{client: client}
		data := userSafeReadModel("mutation-user")
		if err := resource.readUser(context.Background(), &data); err == nil || calls.Load() != 1 {
			t.Fatalf("calls=%d err=%v", calls.Load(), err)
		}
	})
}

func TestUserSafeReadConcurrentPoliciesAreIsolated(t *testing.T) {
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
			data := userSafeReadModel("parallel-user")
			_ = refreshUserWithTestPolicy(context.Background(), client, &data, false, testReadPolicy(attempts), noWaitRetryHooks())
		}()
	}
	wait.Wait()
	if calls.Load() != 6 {
		t.Fatalf("calls=%d want=6", calls.Load())
	}
}
