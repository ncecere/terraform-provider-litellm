package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestVectorStoreUpdateRequestUsesOnlyV198UpdateFieldsAndClearSentinels(t *testing.T) {
	t.Parallel()
	resource := &VectorStoreResource{}
	prior := VectorStoreResourceModel{
		VectorStoreID:          types.StringValue("vs-1"),
		VectorStoreName:        types.StringValue("before"),
		CustomLLMProvider:      types.StringValue("bedrock"),
		VectorStoreDescription: types.StringValue("description"),
		VectorStoreMetadata: types.MapValueMust(types.StringType, map[string]attr.Value{
			"environment": types.StringValue("prod"),
		}),
		LiteLLMCredentialName: types.StringValue("credential"),
		LiteLLMParams: types.MapValueMust(types.StringType, map[string]attr.Value{
			"api_key": types.StringValue("secret"),
		}),
	}
	planned := prior
	planned.VectorStoreName = types.StringValue("after")
	planned.VectorStoreDescription = types.StringNull()
	planned.VectorStoreMetadata = types.MapNull(types.StringType)
	request := resource.buildVectorStoreUpdateRequest(context.Background(), &planned, &prior)
	if request["vector_store_description"] != "" {
		t.Fatalf("description clear sentinel = %#v", request["vector_store_description"])
	}
	metadata, ok := request["vector_store_metadata"].(map[string]string)
	if !ok || len(metadata) != 0 {
		t.Fatalf("metadata clear sentinel = %#v", request["vector_store_metadata"])
	}
	for _, unsupported := range []string{"litellm_credential_name", "litellm_params"} {
		if _, present := request[unsupported]; present {
			t.Errorf("unsupported update field %s was sent", unsupported)
		}
	}
}

func TestReadVectorStorePreservesOwnedMaskedParametersAndRejectsMaskedImport(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"vector_store": map[string]interface{}{
				"vector_store_id":     "vs-1",
				"vector_store_name":   "store",
				"custom_llm_provider": "bedrock",
				"litellm_params": map[string]interface{}{
					"api_key": "REDACTED_BY_LITELM",
					"nested":  map[string]interface{}{"api_key": "REDACTED_BY_LITELM", "region": "us-east-1"},
				},
			},
		})
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}
	prior := types.MapValueMust(types.StringType, map[string]attr.Value{
		"api_key": types.StringValue("secret"),
		"nested":  types.StringValue(`{"api_key":"nested-secret","region":"us-east-1"}`),
	})
	data := &VectorStoreResourceModel{
		VectorStoreID:           types.StringValue("vs-1"),
		LiteLLMParams:           prior,
		LiteLLMParamsConfigured: types.BoolValue(true),
	}
	if err := (&VectorStoreResource{client: client}).readVectorStore(context.Background(), data, false, false); err != nil {
		t.Fatal(err)
	}
	if !data.LiteLLMParams.Equal(prior) {
		t.Fatalf("masked parameters changed state: %#v", data.LiteLLMParams)
	}

	imported := &VectorStoreResourceModel{VectorStoreID: types.StringValue("vs-1"), LiteLLMParams: types.MapNull(types.StringType)}
	if err := (&VectorStoreResource{client: client}).readVectorStore(context.Background(), imported, true, false); err == nil {
		t.Fatal("masked import without prior values was accepted")
	}
}

func TestVectorStoreStringMapPreservesOnlyMaskedLeaves(t *testing.T) {
	t.Parallel()
	prior := types.MapValueMust(types.StringType, map[string]attr.Value{
		"nested": types.StringValue(`{"api_key":"secret","region":"us-east-1"}`),
	})
	value, err := vectorStoreStringMap(map[string]interface{}{
		"nested": map[string]interface{}{"api_key": "REDACTED_BY_LITELM", "region": "us-west-2"},
	}, prior, true, true, "litellm_params")
	if err != nil {
		t.Fatal(err)
	}
	got := value.Elements()["nested"].(types.String).ValueString()
	if got != `{"api_key":"secret","region":"us-west-2"}` {
		t.Fatalf("leaf-aware masked merge = %s", got)
	}
}

func TestVectorStoreStringMapCanonicalizesStructuredValues(t *testing.T) {
	t.Parallel()
	value, err := vectorStoreStringMap(map[string]interface{}{
		"nested": map[string]interface{}{"b": json.Number("2"), "a": true},
	}, types.MapNull(types.StringType), false, false, "vector_store_metadata")
	if err != nil {
		t.Fatal(err)
	}
	text := value.Elements()["nested"].(types.String).ValueString()
	if text != `{"a":true,"b":2}` {
		t.Fatalf("canonical nested value = %s", text)
	}
}

func TestUnwrapVectorStoreResponseRejectsMissingMalformedAndMismatchedEnvelopes(t *testing.T) {
	t.Parallel()
	for _, result := range []map[string]interface{}{
		{},
		{"vector_store": "invalid"},
		{"vector_store": map[string]interface{}{"vector_store_id": "other"}},
	} {
		if _, err := unwrapVectorStoreResponse(result, "vs-1"); err == nil {
			t.Fatalf("invalid response accepted: %#v", result)
		}
	}
}
