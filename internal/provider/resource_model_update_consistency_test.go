package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestChangedModelFieldsNotConverged(t *testing.T) {
	t.Parallel()

	prior := consistencyTestModel("gpt-4o-mini", "free")
	planned := consistencyTestModel("gpt-4o", "free")

	tests := []struct {
		name     string
		observed ModelResourceModel
		want     []string
	}{
		{
			name:     "changed base model is stale",
			observed: consistencyTestModel("gpt-4o-mini", "free"),
			want:     []string{"base_model"},
		},
		{
			name:     "changed base model converged",
			observed: consistencyTestModel("gpt-4o", "free"),
			want:     nil,
		},
		{
			name:     "unchanged field is not part of post-update wait",
			observed: consistencyTestModel("gpt-4o", "paid"),
			want:     nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := changedModelFieldsNotConverged(planned, prior, test.observed); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("stale fields = %v, want %v", got, test.want)
			}
		})
	}
}

func TestReadModelAfterUpdateRequiresStableValues(t *testing.T) {
	t.Parallel()

	var reads atomic.Int32
	sequence := []string{"gpt-4o", "gpt-4o-mini", "gpt-4o", "gpt-4o"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := int(reads.Add(1)) - 1
		if index >= len(sequence) {
			index = len(sequence) - 1
		}
		writeConsistencyModelResponse(w, sequence[index])
	}))
	defer server.Close()

	resource := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	prior := consistencyTestModel("gpt-4o-mini", "free")
	planned := consistencyTestModel("gpt-4o", "free")
	data := planned

	if err := resource.readModelAfterUpdate(context.Background(), &data, planned, prior, 5); err != nil {
		t.Fatalf("readModelAfterUpdate returned error: %v", err)
	}
	if got := reads.Load(); got != 4 {
		t.Fatalf("read count = %d, want 4", got)
	}
	if got := data.BaseModel.ValueString(); got != "gpt-4o" {
		t.Fatalf("base_model = %q, want gpt-4o", got)
	}
}

func TestReadModelAfterUpdateReportsPersistentStaleValues(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeConsistencyModelResponse(w, "gpt-4o-mini")
	}))
	defer server.Close()

	resource := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	prior := consistencyTestModel("gpt-4o-mini", "free")
	planned := consistencyTestModel("gpt-4o", "free")
	data := planned

	err := resource.readModelAfterUpdate(context.Background(), &data, planned, prior, 2)
	if err == nil || !strings.Contains(err.Error(), "base_model") {
		t.Fatalf("error = %v, want stale base_model diagnostic", err)
	}
}

func TestReadModelAfterUpdateCancellation(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeConsistencyModelResponse(w, "gpt-4o-mini")
	}))
	defer server.Close()

	resource := &ModelResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	prior := consistencyTestModel("gpt-4o-mini", "free")
	planned := consistencyTestModel("gpt-4o", "free")
	data := planned
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	err := resource.readModelAfterUpdate(ctx, &data, planned, prior, 8)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(started); elapsed >= 200*time.Millisecond {
		t.Fatalf("cancellation took %s, want less than 200ms", elapsed)
	}
}

func consistencyTestModel(baseModel, tier string) ModelResourceModel {
	return ModelResourceModel{
		ID:                                types.StringValue("model-123"),
		ModelName:                         types.StringValue("test-model"),
		CustomLLMProvider:                 types.StringValue("openai"),
		BaseModel:                         types.StringValue(baseModel),
		Tier:                              types.StringValue(tier),
		AccessGroups:                      types.ListUnknown(types.StringType),
		AdditionalLiteLLMParams:           types.MapUnknown(types.StringType),
		AdditionalLiteLLMParamsConfigured: types.BoolValue(false),
	}
}

func writeConsistencyModelResponse(w http.ResponseWriter, baseModel string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"data": []interface{}{
			map[string]interface{}{
				"model_name": "test-model",
				"litellm_params": map[string]interface{}{
					"custom_llm_provider": "openai",
					"model":               "openai/" + baseModel,
				},
				"model_info": map[string]interface{}{
					"base_model": baseModel,
					"tier":       "free",
				},
			},
		},
	})
}
