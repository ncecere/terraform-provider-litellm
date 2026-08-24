package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// These tests cover the pure buildXxxRequest helpers — the functions that
// translate a Terraform resource model into the map[string]interface{} payload
// sent to the LiteLLM API. They are side-effect-free, so they can be exercised
// directly without an HTTP client.

func TestBuildBudgetRequest(t *testing.T) {
	t.Parallel()

	r := &BudgetResource{}
	data := &BudgetResourceModel{
		BudgetID:            types.StringValue("budget-1"),
		BudgetDuration:      types.StringValue("30d"),
		MaxBudget:           types.Float64Value(100),
		SoftBudget:          types.Float64Value(80),
		MaxParallelRequests: types.Int64Value(5),
		TPMLimit:            types.Int64Value(1000),
		RPMLimit:            types.Int64Value(60),
		ModelMaxBudget:      types.StringValue(`{"gpt-4o":10}`),
	}

	req := r.buildBudgetRequest(context.Background(), data)

	if req["budget_id"] != "budget-1" {
		t.Errorf("budget_id = %v", req["budget_id"])
	}
	if req["budget_duration"] != "30d" {
		t.Errorf("budget_duration = %v", req["budget_duration"])
	}
	if req["max_budget"] != float64(100) {
		t.Errorf("max_budget = %v", req["max_budget"])
	}
	if req["tpm_limit"] != int64(1000) {
		t.Errorf("tpm_limit = %v", req["tpm_limit"])
	}
	mmb, ok := req["model_max_budget"].(map[string]interface{})
	if !ok || mmb["gpt-4o"] != float64(10) {
		t.Errorf("model_max_budget = %#v", req["model_max_budget"])
	}
}

func TestBuildBudgetRequestOmitsUnset(t *testing.T) {
	t.Parallel()

	r := &BudgetResource{}
	data := &BudgetResourceModel{
		BudgetID:            types.StringNull(),
		BudgetDuration:      types.StringNull(),
		MaxBudget:           types.Float64Null(),
		SoftBudget:          types.Float64Null(),
		MaxParallelRequests: types.Int64Null(),
		TPMLimit:            types.Int64Null(),
		RPMLimit:            types.Int64Null(),
		ModelMaxBudget:      types.StringNull(),
	}

	req := r.buildBudgetRequest(context.Background(), data)
	if len(req) != 0 {
		t.Errorf("expected empty request when all fields null, got %#v", req)
	}
}

func TestBuildTagRequest(t *testing.T) {
	t.Parallel()

	r := &TagResource{}
	data := &TagResourceModel{
		Name:        types.StringValue("prod"),
		Description: types.StringValue("production tag"),
		Models:      stringListValue("gpt-4o", "gpt-4o-mini"),
		MaxBudget:   types.Float64Value(50),
	}

	req := r.buildTagRequest(context.Background(), data)

	if req["name"] != "prod" {
		t.Errorf("name = %v", req["name"])
	}
	if req["description"] != "production tag" {
		t.Errorf("description = %v", req["description"])
	}
	models, ok := req["models"].([]string)
	if !ok || len(models) != 2 || models[0] != "gpt-4o" {
		t.Errorf("models = %#v", req["models"])
	}
}

func TestBuildCredentialRequest(t *testing.T) {
	t.Parallel()

	r := &CredentialResource{}
	data := &CredentialResourceModel{
		CredentialName:   types.StringValue("openai-prod"),
		ModelID:          types.StringValue("model-1"),
		CredentialInfo:   stringMapValue(map[string]string{"env": "prod"}),
		CredentialValues: stringMapValue(map[string]string{"api_key": "sk-secret"}),
	}

	req := r.buildCredentialRequest(context.Background(), data)

	if req["credential_name"] != "openai-prod" {
		t.Errorf("credential_name = %v", req["credential_name"])
	}
	if req["model_id"] != "model-1" {
		t.Errorf("model_id = %v", req["model_id"])
	}
	info, ok := req["credential_info"].(map[string]interface{})
	if !ok || info["env"] != "prod" {
		t.Errorf("credential_info = %#v", req["credential_info"])
	}
	vals, ok := req["credential_values"].(map[string]interface{})
	if !ok || vals["api_key"] != "sk-secret" {
		t.Errorf("credential_values = %#v", req["credential_values"])
	}
}

func TestBuildGuardrailRequest(t *testing.T) {
	t.Parallel()

	r := &GuardrailResource{}
	data := &GuardrailResourceModel{
		GuardrailName: types.StringValue("pii"),
		Guardrail:     types.StringValue("presidio"),
		Mode:          types.StringValue("pre_call"),
		DefaultOn:     types.BoolValue(true),
	}

	req := r.buildGuardrailRequest(context.Background(), data)

	guardrail, ok := req["guardrail"].(map[string]interface{})
	if !ok {
		t.Fatalf("guardrail wrapper missing: %#v", req)
	}
	if guardrail["guardrail_name"] != "pii" {
		t.Errorf("guardrail_name = %v", guardrail["guardrail_name"])
	}
	params, ok := guardrail["litellm_params"].(map[string]interface{})
	if !ok {
		t.Fatalf("litellm_params missing: %#v", guardrail)
	}
	if params["guardrail"] != "presidio" {
		t.Errorf("litellm_params.guardrail = %v", params["guardrail"])
	}
	if params["mode"] != "pre_call" {
		t.Errorf("mode = %v", params["mode"])
	}
	if params["default_on"] != true {
		t.Errorf("default_on = %v", params["default_on"])
	}
}

func TestBuildGuardrailRequestParsesModeArray(t *testing.T) {
	t.Parallel()

	r := &GuardrailResource{}
	data := &GuardrailResourceModel{
		GuardrailName: types.StringValue("pii"),
		Guardrail:     types.StringValue("presidio"),
		Mode:          types.StringValue(`["pre_call","post_call"]`),
	}

	req := r.buildGuardrailRequest(context.Background(), data)
	guardrail := req["guardrail"].(map[string]interface{})
	params := guardrail["litellm_params"].(map[string]interface{})
	modes, ok := params["mode"].([]string)
	if !ok || len(modes) != 2 || modes[1] != "post_call" {
		t.Errorf("mode array = %#v", params["mode"])
	}
}

func TestBuildOrganizationRequest(t *testing.T) {
	t.Parallel()

	r := &OrganizationResource{}
	data := &OrganizationResourceModel{
		OrganizationAlias: types.StringValue("acme"),
		OrganizationID:    types.StringValue("org-1"),
		Models:            stringListValue("gpt-4o"),
		MaxBudget:         types.Float64Value(500),
		TPMLimit:          types.Int64Value(2000),
		Blocked:           types.BoolValue(false),
		Tags:              stringListValue("team-a"),
	}

	req := r.buildOrganizationRequest(context.Background(), data)

	if req["organization_alias"] != "acme" {
		t.Errorf("organization_alias = %v", req["organization_alias"])
	}
	if req["organization_id"] != "org-1" {
		t.Errorf("organization_id = %v", req["organization_id"])
	}
	if req["max_budget"] != float64(500) {
		t.Errorf("max_budget = %v", req["max_budget"])
	}
	if req["blocked"] != false {
		t.Errorf("blocked = %v", req["blocked"])
	}
	models, ok := req["models"].([]string)
	if !ok || len(models) != 1 {
		t.Errorf("models = %#v", req["models"])
	}
}

func TestBuildPromptRequest(t *testing.T) {
	t.Parallel()

	r := &PromptResource{}
	data := &PromptResourceModel{
		PromptID:                 types.StringValue("prompt-1"),
		PromptIntegration:        types.StringValue("langfuse"),
		APIBase:                  types.StringValue("https://cloud.langfuse.com"),
		IgnorePromptManagerModel: types.BoolValue(true),
		PromptType:               types.StringValue("chat"),
	}

	req := r.buildPromptRequest(context.Background(), data)

	if req["prompt_id"] != "prompt-1" {
		t.Errorf("prompt_id = %v", req["prompt_id"])
	}
	params, ok := req["litellm_params"].(map[string]interface{})
	if !ok {
		t.Fatalf("litellm_params missing: %#v", req)
	}
	if params["prompt_integration"] != "langfuse" {
		t.Errorf("prompt_integration = %v", params["prompt_integration"])
	}
	if params["api_base"] != "https://cloud.langfuse.com" {
		t.Errorf("api_base = %v", params["api_base"])
	}
	if params["ignore_prompt_manager_model"] != true {
		t.Errorf("ignore_prompt_manager_model = %v", params["ignore_prompt_manager_model"])
	}
	info, ok := req["prompt_info"].(map[string]interface{})
	if !ok || info["prompt_type"] != "chat" {
		t.Errorf("prompt_info = %#v", req["prompt_info"])
	}
}

func TestBuildSearchToolRequest(t *testing.T) {
	t.Parallel()

	r := &SearchToolResource{}
	data := &SearchToolResourceModel{
		SearchToolName: types.StringValue("tavily"),
		SearchProvider: types.StringValue("tavily"),
		APIKey:         types.StringValue("tvly-secret"),
		APIBase:        types.StringValue("https://api.tavily.com"),
		Timeout:        types.Float64Value(30),
		MaxRetries:     types.Int64Value(3),
	}

	req := r.buildSearchToolRequest(context.Background(), data)

	if req["search_tool_name"] != "tavily" {
		t.Errorf("search_tool_name = %v", req["search_tool_name"])
	}
	params, ok := req["litellm_params"].(map[string]interface{})
	if !ok {
		t.Fatalf("litellm_params missing: %#v", req)
	}
	if params["search_provider"] != "tavily" {
		t.Errorf("search_provider = %v", params["search_provider"])
	}
	if params["api_key"] != "tvly-secret" {
		t.Errorf("api_key = %v", params["api_key"])
	}
	if params["timeout"] != float64(30) {
		t.Errorf("timeout = %v", params["timeout"])
	}
	if params["max_retries"] != int64(3) {
		t.Errorf("max_retries = %v", params["max_retries"])
	}
}

func TestBuildUserRequest(t *testing.T) {
	t.Parallel()

	r := &UserResource{}
	data := &UserResourceModel{
		UserID:        types.StringValue("user-1"),
		UserAlias:     types.StringValue("Alice"),
		UserEmail:     types.StringValue("alice@example.com"),
		UserRole:      types.StringValue("internal_user"),
		Teams:         stringListValue("team-a", "team-b"),
		Models:        stringListValue("gpt-4o"),
		MaxBudget:     types.Float64Value(25),
		TPMLimit:      types.Int64Value(500),
		AutoCreateKey: types.BoolValue(false),
		Metadata:      stringMapValue(map[string]string{"dept": "eng"}),
	}

	req := r.buildUserRequest(context.Background(), data)

	if req["user_id"] != "user-1" {
		t.Errorf("user_id = %v", req["user_id"])
	}
	if req["user_email"] != "alice@example.com" {
		t.Errorf("user_email = %v", req["user_email"])
	}
	if req["auto_create_key"] != false {
		t.Errorf("auto_create_key = %v", req["auto_create_key"])
	}
	teams, ok := req["teams"].([]string)
	if !ok || len(teams) != 2 {
		t.Errorf("teams = %#v", req["teams"])
	}
	meta, ok := req["metadata"].(map[string]string)
	if !ok || meta["dept"] != "eng" {
		t.Errorf("metadata = %#v", req["metadata"])
	}
}

func TestBuildVectorStoreRequest(t *testing.T) {
	t.Parallel()

	r := &VectorStoreResource{}
	data := &VectorStoreResourceModel{
		VectorStoreName:        types.StringValue("kb"),
		CustomLLMProvider:      types.StringValue("bedrock"),
		VectorStoreDescription: types.StringValue("knowledge base"),
		VectorStoreMetadata:    stringMapValue(map[string]string{"team": "docs"}),
		LiteLLMParams:          types.MapNull(types.StringType),
	}

	req := r.buildVectorStoreRequest(context.Background(), data)

	if req["vector_store_name"] != "kb" {
		t.Errorf("vector_store_name = %v", req["vector_store_name"])
	}
	if req["custom_llm_provider"] != "bedrock" {
		t.Errorf("custom_llm_provider = %v", req["custom_llm_provider"])
	}
	meta, ok := req["vector_store_metadata"].(map[string]interface{})
	if !ok || meta["team"] != "docs" {
		t.Errorf("vector_store_metadata = %#v", req["vector_store_metadata"])
	}
	// litellm_params is always present (API requires it), even when null.
	if _, ok := req["litellm_params"]; !ok {
		t.Errorf("litellm_params should always be present, got %#v", req)
	}
}

func TestBuildUnifiedAccessGroupRequest(t *testing.T) {
	t.Parallel()

	data := &UnifiedAccessGroupResourceModel{
		AccessGroupName:  types.StringValue("ag-1"),
		Description:      types.StringValue("access group one"),
		AccessModelNames: stringListValue("gpt-4o"),
	}

	// includeOptionalName=true path
	req := buildUnifiedAccessGroupRequest(context.Background(), data, true)
	if req["access_group_name"] != "ag-1" {
		t.Errorf("access_group_name = %v", req["access_group_name"])
	}
	if req["description"] != "access group one" {
		t.Errorf("description = %v", req["description"])
	}

	// includeOptionalName=false with a set name still includes it (non-empty).
	req2 := buildUnifiedAccessGroupRequest(context.Background(), data, false)
	if req2["access_group_name"] != "ag-1" {
		t.Errorf("access_group_name (false path) = %v", req2["access_group_name"])
	}
}
