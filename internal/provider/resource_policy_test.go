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

func TestBuildPolicyRequest_Minimal(t *testing.T) {
	t.Parallel()

	r := &PolicyResource{}
	data := &PolicyResourceModel{
		PolicyName: types.StringValue("global-baseline"),
	}

	req, err := r.buildPolicyRequest(context.Background(), data)
	if err != nil {
		t.Fatalf("buildPolicyRequest returned error: %v", err)
	}

	if req["policy_name"] != "global-baseline" {
		t.Errorf("expected policy_name 'global-baseline', got %v", req["policy_name"])
	}
	if _, exists := req["guardrails_add"]; exists {
		t.Error("guardrails_add should not be present when not configured")
	}
	if _, exists := req["condition"]; exists {
		t.Error("condition should not be present when not configured")
	}
	if _, exists := req["pipeline"]; exists {
		t.Error("pipeline should not be present when not configured")
	}
}

func TestBuildPolicyRequest_Full(t *testing.T) {
	t.Parallel()

	r := &PolicyResource{}
	conditionObj, diags := types.ObjectValue(
		policyConditionAttrTypes(),
		map[string]attr.Value{"model": types.StringValue("gpt-4.*")},
	)
	if diags.HasError() {
		t.Fatalf("failed to build condition object: %v", diags)
	}

	data := &PolicyResourceModel{
		PolicyName:       types.StringValue("healthcare-compliance"),
		Inherit:          types.StringValue("global-baseline"),
		Description:      types.StringValue("Policy for healthcare traffic"),
		GuardrailsAdd:    stringListValue("hipaa_audit", "pii_masking"),
		GuardrailsRemove: stringListValue("prompt_injection"),
		Condition:        conditionObj,
		Pipeline:         types.StringValue(`{"mode":"pre_call","steps":[{"guardrail":"hipaa_audit","on_fail":"block"}]}`),
	}

	req, err := r.buildPolicyRequest(context.Background(), data)
	if err != nil {
		t.Fatalf("buildPolicyRequest returned error: %v", err)
	}

	if req["policy_name"] != "healthcare-compliance" {
		t.Errorf("expected policy_name 'healthcare-compliance', got %v", req["policy_name"])
	}
	if req["inherit"] != "global-baseline" {
		t.Errorf("expected inherit 'global-baseline', got %v", req["inherit"])
	}

	guardrailsAdd, ok := req["guardrails_add"].([]string)
	if !ok || len(guardrailsAdd) != 2 {
		t.Fatalf("expected 2 guardrails_add entries, got %v", req["guardrails_add"])
	}

	condition, ok := req["condition"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected condition map, got %T", req["condition"])
	}
	if condition["model"] != "gpt-4.*" {
		t.Errorf("expected condition.model 'gpt-4.*', got %v", condition["model"])
	}

	pipeline, ok := req["pipeline"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected pipeline map, got %T", req["pipeline"])
	}
	if pipeline["mode"] != "pre_call" {
		t.Errorf("expected pipeline.mode 'pre_call', got %v", pipeline["mode"])
	}
}

func TestBuildPolicyRequest_InvalidPipelineJSON(t *testing.T) {
	t.Parallel()

	r := &PolicyResource{}
	data := &PolicyResourceModel{
		PolicyName: types.StringValue("broken-policy"),
		Pipeline:   types.StringValue(`{"mode":"pre_call"`),
	}

	_, err := r.buildPolicyRequest(context.Background(), data)
	if err == nil {
		t.Fatal("expected error for invalid pipeline JSON")
	}
}

func TestReadPolicy_PopulatesState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"policy_id":         "pol-123",
			"policy_name":       "global-baseline",
			"version_number":    2,
			"version_status":    "production",
			"parent_version_id": "pol-122",
			"is_latest":         true,
			"published_at":      "2026-04-01T00:00:00Z",
			"production_at":     "2026-04-02T00:00:00Z",
			"inherit":           "base-policy",
			"description":       "Baseline policy",
			"guardrails_add":    []interface{}{"pii_masking", "toxicity_filter"},
			"guardrails_remove": []interface{}{"prompt_injection"},
			"condition": map[string]interface{}{
				"model": "gpt-4.*",
			},
			"pipeline": map[string]interface{}{
				"mode": "pre_call",
				"steps": []interface{}{
					map[string]interface{}{"guardrail": "pii_masking", "on_fail": "block"},
				},
			},
			"created_at": "2026-03-30T10:00:00Z",
			"updated_at": "2026-04-02T11:00:00Z",
			"created_by": "admin",
			"updated_by": "admin",
		})
	}))
	defer server.Close()

	r := &PolicyResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := PolicyResourceModel{
		ID:               types.StringValue("pol-123"),
		GuardrailsAdd:    types.ListUnknown(types.StringType),
		GuardrailsRemove: types.ListUnknown(types.StringType),
		Condition:        types.ObjectUnknown(policyConditionAttrTypes()),
		Pipeline:         types.StringUnknown(),
	}

	if err := r.readPolicy(context.Background(), &data); err != nil {
		t.Fatalf("readPolicy returned error: %v", err)
	}

	if data.ID.ValueString() != "pol-123" {
		t.Errorf("expected id 'pol-123', got %q", data.ID.ValueString())
	}
	if data.PolicyName.ValueString() != "global-baseline" {
		t.Errorf("expected policy_name 'global-baseline', got %q", data.PolicyName.ValueString())
	}
	if data.VersionNumber.ValueInt64() != 2 {
		t.Errorf("expected version_number 2, got %d", data.VersionNumber.ValueInt64())
	}
	if data.Condition.IsNull() || data.Condition.IsUnknown() {
		t.Fatal("condition should be known and non-null")
	}
	if data.Pipeline.IsNull() || data.Pipeline.IsUnknown() {
		t.Fatal("pipeline should be known and non-null")
	}
	if data.CreatedBy.ValueString() != "admin" {
		t.Errorf("expected created_by 'admin', got %q", data.CreatedBy.ValueString())
	}
}

func TestReadPolicy_ResolvesUnknownToNull(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"policy_id":   "pol-minimal",
			"policy_name": "minimal-policy",
		})
	}))
	defer server.Close()

	r := &PolicyResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := PolicyResourceModel{
		ID:               types.StringValue("pol-minimal"),
		VersionNumber:    types.Int64Unknown(),
		VersionStatus:    types.StringUnknown(),
		ParentVersionID:  types.StringUnknown(),
		IsLatest:         types.BoolUnknown(),
		PublishedAt:      types.StringUnknown(),
		ProductionAt:     types.StringUnknown(),
		CreatedAt:        types.StringUnknown(),
		UpdatedAt:        types.StringUnknown(),
		CreatedBy:        types.StringUnknown(),
		UpdatedBy:        types.StringUnknown(),
		GuardrailsAdd:    types.ListUnknown(types.StringType),
		GuardrailsRemove: types.ListUnknown(types.StringType),
		Condition:        types.ObjectUnknown(policyConditionAttrTypes()),
		Pipeline:         types.StringUnknown(),
	}

	if err := r.readPolicy(context.Background(), &data); err != nil {
		t.Fatalf("readPolicy returned error: %v", err)
	}

	if data.VersionNumber.IsUnknown() {
		t.Error("version_number should not be Unknown after read")
	}
	if data.VersionStatus.IsUnknown() {
		t.Error("version_status should not be Unknown after read")
	}
	if data.Condition.IsUnknown() {
		t.Error("condition should not be Unknown after read")
	}
	if data.Pipeline.IsUnknown() {
		t.Error("pipeline should not be Unknown after read")
	}
	if !data.Condition.IsNull() {
		t.Error("condition should be null when API omits it")
	}
	if !data.Pipeline.IsNull() {
		t.Error("pipeline should be null when API omits it")
	}
}

func TestReadPolicy_OverwritesExistingPipeline(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"policy_id":   "pol-pipeline",
			"policy_name": "pipeline-policy",
			"pipeline": map[string]interface{}{
				"mode": "post_call",
				"steps": []interface{}{
					map[string]interface{}{"guardrail": "toxicity_filter", "on_fail": "mask"},
				},
			},
		})
	}))
	defer server.Close()

	r := &PolicyResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}

	data := PolicyResourceModel{
		ID:       types.StringValue("pol-pipeline"),
		Pipeline: types.StringValue(`{"mode":"pre_call","steps":[]}`),
	}

	if err := r.readPolicy(context.Background(), &data); err != nil {
		t.Fatalf("readPolicy returned error: %v", err)
	}

	if data.Pipeline.IsNull() || data.Pipeline.IsUnknown() {
		t.Fatal("pipeline should be known and non-null")
	}

	var pipeline map[string]interface{}
	if err := json.Unmarshal([]byte(data.Pipeline.ValueString()), &pipeline); err != nil {
		t.Fatalf("pipeline is not valid JSON: %v", err)
	}

	if pipeline["mode"] != "post_call" {
		t.Fatalf("expected pipeline mode 'post_call', got %v", pipeline["mode"])
	}
}
