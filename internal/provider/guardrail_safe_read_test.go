package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func guardrailSafeReadBody(t *testing.T, id, location string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"guardrail_id":                  id,
		"guardrail_name":                "refreshed-name",
		"guardrail_definition_location": location,
		"created_at":                    "2026-08-29T00:00:00Z",
		"updated_at":                    "2026-08-29T01:00:00Z",
		"litellm_params": map[string]interface{}{
			"guardrail":  "bedrock",
			"mode":       "post_call",
			"default_on": true,
			"api_key":    "se****et",
			"region":     "us-west-2",
		},
		"guardrail_info": map[string]interface{}{"owner": "refreshed"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func guardrailSafeReadUnmaskedBody(t *testing.T, id, location string) []byte {
	t.Helper()
	return bytes.ReplaceAll(guardrailSafeReadBody(t, id, location), []byte(`se****et`), []byte(`secret-plaintext`))
}

func guardrailSafeReadModel(id string) GuardrailResourceModel {
	return GuardrailResourceModel{
		ID:            types.StringValue(id),
		GuardrailID:   types.StringValue(id),
		GuardrailName: types.StringValue("prior-name-secret"),
		Guardrail:     types.StringValue("bedrock"),
		Mode:          types.StringValue("pre_call"),
		DefaultOn:     types.BoolValue(false),
		LitellmParams: types.StringValue(`{"api_key":"secret-plaintext","region":"us-east-1"}`),
		GuardrailInfo: types.StringValue(`{"owner":"prior-info-secret"}`),
		CreatedAt:     types.StringValue("2025-01-01T00:00:00Z"),
	}
}

func refreshGuardrailWithTestPolicy(ctx context.Context, client *Client, data *GuardrailResourceModel, imported bool, policy safeReadRetryPolicy, hooks safeReadRetryHooks) error {
	id := data.GuardrailID.ValueString()
	if id == "" {
		id = data.ID.ValueString()
	}
	endpoint := endpointWithPathSegment("/guardrails/", id, "/info")
	var raw map[string]interface{}
	if err := client.doReadWithResponsePolicy(ctx, http.MethodGet, endpoint, nil, &raw, policy, hooks); err != nil {
		return err
	}
	return projectGuardrailResourceAPIObject(data, raw, id, imported)
}

func TestGuardrailOrdinaryRefreshRetriesTransientSequenceAndProjectsAtomically(t *testing.T) {
	id := "guardrail ?percent=% colon: unicode-雪"
	endpoint := endpointWithPathSegment("/guardrails/", id, "/info")
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
			return searchToolTestResponse(request, http.StatusOK, guardrailSafeReadBody(t, id, "db"), nil), nil
		}
	})
	data := guardrailSafeReadModel(id)
	if err := refreshGuardrailWithTestPolicy(context.Background(), client, &data, false, testReadPolicy(4), noWaitRetryHooks()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 || data.GuardrailName.ValueString() != "refreshed-name" || data.Mode.ValueString() != "post_call" {
		t.Fatalf("calls=%d data=%#v", calls.Load(), data)
	}
	if data.LitellmParams.ValueString() != `{"api_key":"secret-plaintext","region":"us-west-2"}` {
		t.Fatalf("masked reconciliation=%q", data.LitellmParams.ValueString())
	}
}

func TestGuardrailSafeReadStatusAndAuthoritySequences(t *testing.T) {
	const id = "guardrail-sequence"
	policy := testReadPolicy(4)

	t.Run("complete exact 404 is terminal", func(t *testing.T) {
		var calls atomic.Int32
		client := testRetryClient(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			return searchToolTestResponse(request, http.StatusNotFound, []byte(`{"detail":"missing"}`), nil), nil
		})
		data := guardrailSafeReadModel(id)
		err := refreshGuardrailWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks())
		if !IsAPIErrorStatus(err, http.StatusNotFound) || calls.Load() != 1 {
			t.Fatalf("calls=%d err=%v", calls.Load(), err)
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
			return searchToolTestResponse(request, http.StatusOK, guardrailSafeReadBody(t, id, "db"), nil), nil
		})
		data := guardrailSafeReadModel(id)
		if err := refreshGuardrailWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks()); err != nil || calls.Load() != 2 {
			t.Fatalf("calls=%d err=%v", calls.Load(), err)
		}
	})

	for _, test := range []struct {
		name string
		body []byte
	}{
		{name: "config collision", body: guardrailSafeReadBody(t, id, "config")},
		{name: "missing authority", body: []byte(`{"guardrail_id":"guardrail-sequence","guardrail_name":"name","litellm_params":{"guardrail":"bedrock","mode":"pre_call"}}`)},
		{name: "identity mismatch", body: guardrailSafeReadBody(t, "other-identity-secret", "db")},
		{name: "late malformed info", body: []byte(`{"guardrail_id":"guardrail-sequence","guardrail_name":"new-name","guardrail_definition_location":"db","created_at":"new-time","litellm_params":{"guardrail":"bedrock","mode":"post_call","default_on":true},"guardrail_info":7}`)},
		{name: "malformed default", body: []byte(`{"guardrail_id":"guardrail-sequence","guardrail_name":"new-name","guardrail_definition_location":"db","litellm_params":{"guardrail":"bedrock","mode":"post_call","default_on":"true"}}`)},
	} {
		t.Run(test.name+" retains candidate", func(t *testing.T) {
			client := testRetryClient(func(request *http.Request) (*http.Response, error) {
				return searchToolTestResponse(request, http.StatusOK, test.body, nil), nil
			})
			data := guardrailSafeReadModel(id)
			prior := data
			if err := refreshGuardrailWithTestPolicy(context.Background(), client, &data, false, policy, noWaitRetryHooks()); err == nil {
				t.Fatal("malformed or non-database response succeeded")
			}
			if !reflect.DeepEqual(data, prior) {
				t.Fatalf("partial projection escaped: before=%#v after=%#v", prior, data)
			}
		})
	}
}

func TestGuardrailDataSourceAcceptsKnownDefinitionLocationsOnly(t *testing.T) {
	const id = "guardrail-datasource"
	for _, location := range []string{"db", "config"} {
		var raw map[string]interface{}
		if err := json.Unmarshal(guardrailSafeReadBody(t, id, location), &raw); err != nil {
			t.Fatal(err)
		}
		complete, err := projectGuardrailDataSourceAPIObject(raw, id)
		if err != nil || complete.GuardrailName.ValueString() != "refreshed-name" {
			t.Fatalf("location=%s complete=%#v err=%v", location, complete, err)
		}
	}
	for _, location := range []string{"", "unknown"} {
		var raw map[string]interface{}
		if err := json.Unmarshal(guardrailSafeReadBody(t, id, location), &raw); err != nil {
			t.Fatal(err)
		}
		if location == "" {
			delete(raw, "guardrail_definition_location")
		}
		if _, err := projectGuardrailDataSourceAPIObject(raw, id); err == nil {
			t.Fatalf("location=%q unexpectedly succeeded", location)
		}
	}
}

func TestGuardrailConfirmationReadRemainsSingleAttempt(t *testing.T) {
	const id = "guardrail-confirmation"
	var calls atomic.Int32
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return searchToolTestResponse(request, http.StatusServiceUnavailable, []byte(`{"detail":"temporary-secret"}`), http.Header{"Retry-After": []string{"0"}}), nil
	})
	resource := &GuardrailResource{client: client}
	data := guardrailSafeReadModel(id)
	if err := resource.readGuardrail(context.Background(), &data, false); err == nil || calls.Load() != 1 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func assertGuardrailRawStateUnchanged(t *testing.T, want, got *tfprotov6.DynamicValue) {
	t.Helper()
	if want == nil || got == nil || !bytes.Equal(want.MsgPack, got.MsgPack) || !bytes.Equal(want.JSON, got.JSON) {
		t.Fatal("public raw state changed")
	}
}
