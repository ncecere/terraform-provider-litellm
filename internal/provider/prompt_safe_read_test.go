package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func promptSafeReadBody(t *testing.T, id, environment string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"prompt_spec": map[string]interface{}{
			"prompt_id":   id,
			"environment": environment,
			"version":     3,
			"created_at":  "2026-08-29T00:00:00Z",
			"updated_at":  "2026-08-29T01:00:00Z",
			"litellm_params": map[string]interface{}{
				"prompt_integration":                    "dotprompt",
				"api_base":                              "https://response.invalid/private",
				"api_key":                               "response-api-key-secret",
				"provider_specific_query_params":        map[string]interface{}{"region": "west"},
				"ignore_prompt_manager_model":           true,
				"ignore_prompt_manager_optional_params": true,
				"dotprompt_content":                     "refreshed-content",
			},
			"prompt_info": map[string]interface{}{"prompt_type": "db", "environment": environment},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func promptSafeReadModel(id, environment string) PromptResourceModel {
	return PromptResourceModel{
		ID:                                types.StringValue(id),
		PromptID:                          types.StringValue(id),
		PromptIntegration:                 types.StringValue("dotprompt"),
		APIBase:                           types.StringValue("https://prior.invalid/private"),
		APIKey:                            types.StringValue("prior-api-key-secret"),
		ProviderSpecificQueryParams:       types.StringValue(`{ "region" : "east" }`),
		IgnorePromptManagerModel:          types.BoolValue(false),
		IgnorePromptManagerOptionalParams: types.BoolValue(false),
		DotpromptContent:                  types.StringValue("prior-content-secret"),
		PromptType:                        types.StringValue("db"),
		Environment:                       types.StringValue(environment),
		Version:                           types.Int64Value(2),
		CreatedAt:                         types.StringValue("2025-01-01T00:00:00Z"),
		UpdatedAt:                         types.StringValue("2025-01-02T00:00:00Z"),
	}
}

func refreshPromptWithTestPolicy(ctx context.Context, client *Client, data *PromptResourceModel, imported bool, policy safeReadRetryPolicy, hooks safeReadRetryHooks) error {
	id := data.PromptID.ValueString()
	if id == "" {
		id = data.ID.ValueString()
	}
	environment := promptEnvironment(data.Environment.ValueString())
	var raw map[string]interface{}
	if err := client.doReadWithResponsePolicy(ctx, http.MethodGet, promptEndpoint(id, environment, nil), nil, &raw, policy, hooks); err != nil {
		return err
	}
	return projectPromptResourceAPIObject(data, raw, id, environment, imported)
}

func TestPromptOrdinaryRefreshRetriesTransientSequenceAndProjectsAtomically(t *testing.T) {
	id, environment := "prompt ?percent=% colon: unicode-雪", "production ?blue=%"
	endpoint := promptEndpoint(id, environment, nil)
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
			return searchToolTestResponse(request, http.StatusOK, promptSafeReadBody(t, id, environment), nil), nil
		}
	})
	data := promptSafeReadModel(id, environment)
	if err := refreshPromptWithTestPolicy(context.Background(), client, &data, false, testReadPolicy(4), noWaitRetryHooks()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || data.Version.ValueInt64() != 3 || data.DotpromptContent.ValueString() != "refreshed-content" {
		t.Fatalf("calls=%d data=%#v", calls.Load(), data)
	}
	if data.APIKey.ValueString() != "prior-api-key-secret" {
		t.Fatal("response API credential replaced prior sensitive state")
	}
}

func TestPromptSafeReadStatusAndProjectionSequences(t *testing.T) {
	const id, environment = "prompt-sequence", "production"
	policy := testReadPolicy(4)

	t.Run("retry after is bounded", func(t *testing.T) {
		var calls atomic.Int32
		var waited []time.Duration
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				return searchToolTestResponse(request, http.StatusTooManyRequests, []byte(`{"detail":"throttled-secret"}`), http.Header{"Retry-After": []string{"60"}}), nil
			}
			return searchToolTestResponse(request, http.StatusOK, promptSafeReadBody(t, id, environment), nil), nil
		})
		hooks := safeReadRetryHooks{
			now: time.Now,
			sleep: func(_ context.Context, duration time.Duration) error {
				waited = append(waited, duration)
				return nil
			},
			randomUnit: func() float64 { return 0 },
		}
		data := promptSafeReadModel(id, environment)
		if err := refreshPromptWithTestPolicy(context.Background(), client, &data, false, policy, hooks); err != nil {
			t.Fatal(err)
		}
		if calls.Load() != 2 || len(waited) != 1 || waited[0] != policy.maxRetryAfter {
			t.Fatalf("calls=%d waited=%v max=%v", calls.Load(), waited, policy.maxRetryAfter)
		}
	})

	t.Run("incomplete 404 retries", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			if calls.Add(1) == 1 {
				response := searchToolTestResponse(request, http.StatusNotFound, nil, nil)
				response.Body = failingReadCloser{err: io.ErrUnexpectedEOF}
				response.ContentLength = -1
				return response, nil
			}
			return searchToolTestResponse(request, http.StatusOK, promptSafeReadBody(t, id, environment), nil), nil
		})
		data := promptSafeReadModel(id, environment)
		if err := refreshPromptWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks()); err != nil || calls.Load() != 2 {
			t.Fatalf("calls=%d err=%v", calls.Load(), err)
		}
	})

	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "missing wrapper", body: []byte(`{"prompt_id":"prompt-sequence"}`)},
		{name: "identity mismatch", body: promptSafeReadBody(t, "other-prompt-secret", environment)},
		{name: "environment mismatch", body: promptSafeReadBody(t, id, "other-environment-secret")},
		{name: "missing prompt info", body: []byte(`{"prompt_spec":{"prompt_id":"prompt-sequence","environment":"production","version":3,"litellm_params":{"prompt_integration":"dotprompt"}}}`)},
		{name: "malformed late prompt type", body: []byte(`{"prompt_spec":{"prompt_id":"prompt-sequence","environment":"production","version":3,"created_at":"new-time","litellm_params":{"prompt_integration":"dotprompt","api_base":"new-base","ignore_prompt_manager_model":true},"prompt_info":{"prompt_type":7,"environment":"production"}}}`)},
		{name: "malformed unowned boolean", body: []byte(`{"prompt_spec":{"prompt_id":"prompt-sequence","environment":"production","version":3,"litellm_params":{"prompt_integration":"dotprompt","ignore_prompt_manager_model":"true"},"prompt_info":{"prompt_type":"db","environment":"production"}}}`)},
		{name: "malformed api key", body: []byte(`{"prompt_spec":{"prompt_id":"prompt-sequence","environment":"production","version":3,"litellm_params":{"prompt_integration":"dotprompt","api_key":7},"prompt_info":{"prompt_type":"db","environment":"production"}}}`)},
	} {
		t.Run(test.name+" retains candidate", func(t *testing.T) {
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				return searchToolTestResponse(request, http.StatusOK, test.body, nil), nil
			})
			data := promptSafeReadModel(id, environment)
			prior := data
			if err := refreshPromptWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks()); err == nil {
				t.Fatal("malformed response succeeded")
			}
			if !reflect.DeepEqual(data, prior) {
				t.Fatalf("partial projection escaped: before=%#v after=%#v", prior, data)
			}
		})
	}
}

func TestPromptConfirmationReadRemainsSingleAttempt(t *testing.T) {
	const id, environment = "prompt-confirmation", "production"
	var calls atomic.Int32
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return searchToolTestResponse(request, http.StatusServiceUnavailable, []byte(`{"detail":"404 not found misleading-body-secret"}`), http.Header{"Retry-After": []string{"0"}}), nil
	})
	resource := &PromptResource{client: client}
	data := promptSafeReadModel(id, environment)
	if err := resource.readPromptWithRetry(context.Background(), &data, 8, false); err == nil || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func TestPromptScopedVersionsAbsenceIsExactAndSingleAttempt(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		absent bool
		err    bool
	}{
		{name: "exact 404", status: http.StatusNotFound, body: `{"detail":"missing"}`, absent: true},
		{name: "misleading 400", status: http.StatusBadRequest, body: `{"detail":"404 not found"}`, err: true},
		{name: "empty success is not proof", status: http.StatusOK, body: `{"prompts":[]}`},
		{name: "nonempty success", status: http.StatusOK, body: `{"prompts":[{}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			resource := &PromptResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}
			absent, err := resource.promptScopedVersionsAbsent(context.Background(), "prompt", "production")
			if absent != test.absent || (err != nil) != test.err || calls.Load() != 1 {
				t.Fatalf("absent=%t err=%v calls=%d", absent, err, calls.Load())
			}
		})
	}
}

func assertPromptRawStateUnchanged(t *testing.T, want, got *tfprotov6.DynamicValue) {
	t.Helper()
	if want == nil || got == nil || !bytes.Equal(want.MsgPack, got.MsgPack) || !bytes.Equal(want.JSON, got.JSON) {
		t.Fatal("public raw state changed")
	}
}
