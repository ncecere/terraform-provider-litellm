package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func guardrailAPIResponse() map[string]interface{} {
	return map[string]interface{}{
		"guardrail_id":   "guardrail-1",
		"guardrail_name": "managed",
		"created_at":     "2026-08-25T00:00:00Z",
		"updated_at":     nil,
		"litellm_params": map[string]interface{}{
			"guardrail":  "bedrock",
			"mode":       []interface{}{"pre_call", "post_call"},
			"default_on": true,
			"api_key":    "se****et",
			"nested": map[string]interface{}{
				"token":  "ne****et",
				"region": "us-west-2",
				"unset":  nil,
			},
		},
		"guardrail_info": map[string]interface{}{"description": "managed"},
	}
}

func TestGuardrailSensitiveSchemas(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var resourceResponse resource.SchemaResponse
	(&GuardrailResource{}).Schema(ctx, resource.SchemaRequest{}, &resourceResponse)
	resourceAttribute := resourceResponse.Schema.Attributes["litellm_params"].(resourceschema.StringAttribute)
	if !resourceAttribute.Sensitive {
		t.Fatal("resource litellm_params is not sensitive")
	}
	var singleResponse datasource.SchemaResponse
	(&GuardrailDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &singleResponse)
	if !singleResponse.Schema.Attributes["litellm_params"].(datasourceschema.StringAttribute).Sensitive {
		t.Fatal("single guardrail data source litellm_params is not sensitive")
	}
	var listResponse datasource.SchemaResponse
	(&GuardrailsListDataSource{}).Schema(ctx, datasource.SchemaRequest{}, &listResponse)
	listAttribute := listResponse.Schema.Attributes["guardrails"].(datasourceschema.ListNestedAttribute)
	if !listAttribute.NestedObject.Attributes["litellm_params"].(datasourceschema.StringAttribute).Sensitive {
		t.Fatal("guardrail list litellm_params is not sensitive")
	}
	if _, exists := listAttribute.NestedObject.Attributes["guardrail_info"]; exists {
		t.Fatal("guardrail list public item shape changed unexpectedly")
	}
}

func TestGuardrailSemanticReconciliationPreservesConfiguredJSONSpelling(t *testing.T) {
	t.Parallel()
	prior := "{\n  \"large\": 9007199254740993,\n  \"name\": \"value\"\n}"
	observed, err := reconcileOwnedGuardrailParams(map[string]interface{}{
		"large": json.Number("9007199254740993"), "name": "value",
	}, prior)
	if err != nil {
		t.Fatal(err)
	}
	if observed != prior {
		t.Fatalf("semantic reconciliation rewrote configured JSON: %q", observed)
	}
}

func TestGuardrailMaskRecognitionIsExact(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"*****", "ab****yz"} {
		if !isGuardrailMaskedAPIString(value) {
			t.Fatalf("LiteLLM marker %q was not recognized", value)
		}
	}
	for _, value := range []string{"****", "prefix****suffix", "ordinary"} {
		if isGuardrailMaskedAPIString(value) {
			t.Fatalf("ordinary value %q was treated as a mask", value)
		}
	}
}

func TestReadGuardrailPreservesOwnedMaskedLeavesAndVisibleDrift(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(guardrailAPIResponse())
	}))
	defer server.Close()

	data := GuardrailResourceModel{
		ID:            types.StringValue("guardrail-1"),
		GuardrailID:   types.StringValue("guardrail-1"),
		GuardrailName: types.StringValue("managed"),
		Guardrail:     types.StringValue("bedrock"),
		Mode:          types.StringValue(`["pre_call","post_call"]`),
		DefaultOn:     types.BoolValue(true),
		LitellmParams: types.StringValue(`{"api_key":"secret","nested":{"region":"us-east-1","token":"nested-secret"}}`),
	}
	resource := &GuardrailResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	if err := resource.readGuardrail(context.Background(), &data, false); err != nil {
		t.Fatal(err)
	}
	want := `{"api_key":"secret","nested":{"region":"us-west-2","token":"nested-secret"}}`
	if got := data.LitellmParams.ValueString(); got != want {
		t.Fatalf("masked reconciliation = %s, want %s", got, want)
	}
}

func TestReadGuardrailPreservesExplicitEmptyObjects(t *testing.T) {
	t.Parallel()
	response := map[string]interface{}{
		"guardrail_id": "guardrail-1", "guardrail_name": "managed",
		"litellm_params": map[string]interface{}{"guardrail": "bedrock", "mode": "pre_call"},
		"guardrail_info": map[string]interface{}{},
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(response)
	}))
	defer server.Close()
	data := GuardrailResourceModel{
		ID: types.StringValue("guardrail-1"), GuardrailID: types.StringValue("guardrail-1"),
		LitellmParams: types.StringValue(`{}`), GuardrailInfo: types.StringValue(`{}`),
	}
	resource := &GuardrailResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	if err := resource.readGuardrail(context.Background(), &data, false); err != nil {
		t.Fatal(err)
	}
	if data.LitellmParams.ValueString() != `{}` || data.GuardrailInfo.ValueString() != `{}` {
		t.Fatalf("empty object semantics changed: params=%q info=%q", data.LitellmParams.ValueString(), data.GuardrailInfo.ValueString())
	}
}

func TestReadGuardrailRejectsMaskedImportWithoutPriorState(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(guardrailAPIResponse())
	}))
	defer server.Close()
	data := GuardrailResourceModel{ID: types.StringValue("guardrail-1"), GuardrailID: types.StringValue("guardrail-1")}
	resource := &GuardrailResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	if err := resource.readGuardrail(context.Background(), &data, true); err == nil {
		t.Fatal("masked import succeeded without prior Terraform state")
	}
}

func TestGuardrailV2ListCreateParityAndStrictShape(t *testing.T) {
	t.Parallel()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.RawQuery != "" {
			t.Errorf("v1.98 list does not accept pagination/filter query: %s", request.URL.RawQuery)
		}
		if request.URL.Path != "/v2/guardrails/list" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{"guardrails": []interface{}{guardrailAPIResponse()}})
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}
	items, err := fetchGuardrailListItems(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 || len(items) != 1 || items[0].GuardrailID.ValueString() != "guardrail-1" {
		t.Fatalf("v2 list parity: requests=%d items=%#v", requests, items)
	}
	if got := items[0].LitellmParams.ValueString(); got == "" || !jsonContainsMaskedValue(got) {
		t.Fatalf("list did not retain masked sensitive projection: %q", got)
	}

	malformed := guardrailAPIResponse()
	malformed["litellm_params"] = []interface{}{}
	if _, err := guardrailListItemFromAPI(malformed); err == nil {
		t.Fatal("malformed litellm_params shape was accepted")
	}
}

func TestGuardrailV2ListRejectsMalformedEnvelopes(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"missing": `{"items":[]}`,
		"null":    `{"guardrails":null}`,
		"object":  `{"guardrails":{}}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(body))
			}))
			defer server.Close()
			client := &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}
			if _, err := fetchGuardrailListItems(context.Background(), client); err == nil {
				t.Fatalf("malformed envelope %s was accepted", body)
			}
		})
	}
}
