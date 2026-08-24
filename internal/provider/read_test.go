package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// These tests exercise the readXxx helpers against a stub HTTP server, covering
// the response-parsing paths that translate an API payload back into the
// Terraform resource model. They complement the buildXxxRequest tests, which
// cover the outbound direction.

// jsonServer returns an httptest server that responds to every request with the
// given JSON-encodable body, plus a *Client wired to it.
func jsonServer(t *testing.T, body interface{}) (*httptest.Server, *Client) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	client := &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
	return server, client
}

func TestReadBudget(t *testing.T) {
	t.Parallel()

	// /budget/info returns an array of budget objects.
	server, client := jsonServer(t, []map[string]interface{}{{
		"budget_id":             "budget-1",
		"max_budget":            100.0,
		"soft_budget":           80.0,
		"max_parallel_requests": 5.0,
		"tpm_limit":             1000.0,
		"rpm_limit":             60.0,
		"budget_duration":       "30d",
	}})
	defer server.Close()

	r := &BudgetResource{client: client}
	data := &BudgetResourceModel{BudgetID: types.StringValue("budget-1")}
	if err := r.readBudget(context.Background(), data); err != nil {
		t.Fatalf("readBudget: %v", err)
	}

	if data.ID.ValueString() != "budget-1" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	if data.MaxBudget.ValueFloat64() != 100 {
		t.Errorf("max_budget = %v", data.MaxBudget.ValueFloat64())
	}
	if data.TPMLimit.ValueInt64() != 1000 {
		t.Errorf("tpm_limit = %v", data.TPMLimit.ValueInt64())
	}
	if data.BudgetDuration.ValueString() != "30d" {
		t.Errorf("budget_duration = %q", data.BudgetDuration.ValueString())
	}
}

func TestReadBudgetNotFound(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, []map[string]interface{}{})
	defer server.Close()

	r := &BudgetResource{client: client}
	data := &BudgetResourceModel{BudgetID: types.StringValue("missing")}
	if err := r.readBudget(context.Background(), data); err == nil {
		t.Fatal("expected error for empty budget result")
	}
}

func TestReadTag(t *testing.T) {
	t.Parallel()

	// /tag/info returns a map keyed by tag name.
	server, client := jsonServer(t, map[string]interface{}{
		"prod": map[string]interface{}{
			"name":            "prod",
			"description":     "production",
			"budget_id":       "b-1",
			"max_budget":      50.0,
			"tpm_limit":       500.0,
			"budget_duration": "7d",
		},
	})
	defer server.Close()

	r := &TagResource{client: client}
	data := &TagResourceModel{Name: types.StringValue("prod")}
	if err := r.readTag(context.Background(), data); err != nil {
		t.Fatalf("readTag: %v", err)
	}

	if data.ID.ValueString() != "prod" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	if data.Description.ValueString() != "production" {
		t.Errorf("description = %q", data.Description.ValueString())
	}
	if data.MaxBudget.ValueFloat64() != 50 {
		t.Errorf("max_budget = %v", data.MaxBudget.ValueFloat64())
	}
}

func TestReadCredential(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"credential_name": "openai-prod",
		"credential_info": map[string]interface{}{"env": "prod"},
	})
	defer server.Close()

	r := &CredentialResource{client: client}
	data := &CredentialResourceModel{
		CredentialName: types.StringValue("openai-prod"),
		CredentialInfo: stringMapValue(map[string]string{"env": "old"}),
	}
	if err := r.readCredential(context.Background(), data); err != nil {
		t.Fatalf("readCredential: %v", err)
	}

	if data.ID.ValueString() != "openai-prod" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	var info map[string]string
	data.CredentialInfo.ElementsAs(context.Background(), &info, false)
	if info["env"] != "prod" {
		t.Errorf("credential_info[env] = %q", info["env"])
	}
}

func TestReadOrganization(t *testing.T) {
	t.Parallel()

	// Response nested under "organization_info".
	server, client := jsonServer(t, map[string]interface{}{
		"organization_info": map[string]interface{}{
			"organization_id":    "org-1",
			"organization_alias": "acme",
			"max_budget":         500.0,
			"tpm_limit":          2000.0,
		},
	})
	defer server.Close()

	r := &OrganizationResource{client: client}
	data := &OrganizationResourceModel{OrganizationID: types.StringValue("org-1")}
	if err := r.readOrganization(context.Background(), data); err != nil {
		t.Fatalf("readOrganization: %v", err)
	}

	if data.ID.ValueString() != "org-1" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	if data.OrganizationAlias.ValueString() != "acme" {
		t.Errorf("organization_alias = %q", data.OrganizationAlias.ValueString())
	}
}

func TestReadAccessGroup(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"access_group": "ag-1",
		"model_names":  []interface{}{"gpt-4o", "gpt-4o-mini"},
	})
	defer server.Close()

	r := &AccessGroupResource{client: client}
	data := &AccessGroupResourceModel{AccessGroup: types.StringValue("ag-1")}
	if err := r.readAccessGroup(context.Background(), data); err != nil {
		t.Fatalf("readAccessGroup: %v", err)
	}

	if data.ID.ValueString() != "ag-1" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	var models []string
	data.ModelNames.ElementsAs(context.Background(), &models, false)
	if len(models) != 2 || models[0] != "gpt-4o" {
		t.Errorf("model_names = %#v", models)
	}
}

func TestReadGuardrail(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"guardrail_id":   "g-1",
		"guardrail_name": "pii",
		"created_at":     "2024-01-01T00:00:00Z",
		"litellm_params": map[string]interface{}{
			"guardrail":  "presidio",
			"mode":       "pre_call",
			"default_on": true,
		},
	})
	defer server.Close()

	r := &GuardrailResource{client: client}
	data := &GuardrailResourceModel{
		GuardrailID: types.StringValue("g-1"),
		DefaultOn:   types.BoolValue(false),
	}
	if err := r.readGuardrail(context.Background(), data); err != nil {
		t.Fatalf("readGuardrail: %v", err)
	}

	if data.ID.ValueString() != "g-1" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	if data.GuardrailName.ValueString() != "pii" {
		t.Errorf("guardrail_name = %q", data.GuardrailName.ValueString())
	}
	if data.Guardrail.ValueString() != "presidio" {
		t.Errorf("guardrail = %q", data.Guardrail.ValueString())
	}
	if data.Mode.ValueString() != "pre_call" {
		t.Errorf("mode = %q", data.Mode.ValueString())
	}
}

func TestReadSearchTool(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"search_tool_id":   "st-1",
		"search_tool_name": "tavily",
		"litellm_params": map[string]interface{}{
			"search_provider": "tavily",
			"api_base":        "https://api.tavily.com",
			"timeout":         30.0,
			"max_retries":     3.0,
		},
	})
	defer server.Close()

	r := &SearchToolResource{client: client}
	data := &SearchToolResourceModel{SearchToolID: types.StringValue("st-1")}
	if err := r.readSearchTool(context.Background(), data); err != nil {
		t.Fatalf("readSearchTool: %v", err)
	}

	if data.ID.ValueString() != "st-1" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	if data.SearchProvider.ValueString() != "tavily" {
		t.Errorf("search_provider = %q", data.SearchProvider.ValueString())
	}
	if data.Timeout.ValueFloat64() != 30 {
		t.Errorf("timeout = %v", data.Timeout.ValueFloat64())
	}
	if data.MaxRetries.ValueInt64() != 3 {
		t.Errorf("max_retries = %v", data.MaxRetries.ValueInt64())
	}
}

func TestReadVectorStore(t *testing.T) {
	t.Parallel()

	// Response nested under "vector_store".
	server, client := jsonServer(t, map[string]interface{}{
		"vector_store": map[string]interface{}{
			"vector_store_id":          "vs-1",
			"vector_store_name":        "kb",
			"custom_llm_provider":      "bedrock",
			"vector_store_description": "knowledge base",
		},
	})
	defer server.Close()

	r := &VectorStoreResource{client: client}
	data := &VectorStoreResourceModel{VectorStoreID: types.StringValue("vs-1")}
	if err := r.readVectorStore(context.Background(), data); err != nil {
		t.Fatalf("readVectorStore: %v", err)
	}

	if data.ID.ValueString() != "vs-1" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	if data.VectorStoreName.ValueString() != "kb" {
		t.Errorf("vector_store_name = %q", data.VectorStoreName.ValueString())
	}
	if data.CustomLLMProvider.ValueString() != "bedrock" {
		t.Errorf("custom_llm_provider = %q", data.CustomLLMProvider.ValueString())
	}
}

// TestReadCredentialWithRetrySucceedsFirstTry verifies the retry wrapper returns
// immediately (no sleep) when the underlying read succeeds.
func TestReadCredentialWithRetrySucceedsFirstTry(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"credential_name": "cred-1",
	})
	defer server.Close()

	r := &CredentialResource{client: client}
	data := &CredentialResourceModel{CredentialName: types.StringValue("cred-1")}
	if err := r.readCredentialWithRetry(context.Background(), data, 3); err != nil {
		t.Fatalf("readCredentialWithRetry: %v", err)
	}
	if data.ID.ValueString() != "cred-1" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
}

// TestReadCredentialWithRetryReturnsNonNotFoundImmediately verifies a non-404
// error is returned without retrying (so the test doesn't sleep).
func TestReadCredentialWithRetryReturnsNonNotFoundImmediately(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server exploded", http.StatusInternalServerError)
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}

	r := &CredentialResource{client: client}
	data := &CredentialResourceModel{CredentialName: types.StringValue("cred-1")}
	// maxRetries=3, but a non-not-found error must short-circuit on the first
	// attempt without sleeping.
	if err := r.readCredentialWithRetry(context.Background(), data, 3); err == nil {
		t.Fatal("expected error to propagate")
	}
}

// TestReadPromptWithRetrySucceedsFirstTry mirrors the credential retry success
// path for the prompt resource.
func TestReadPromptWithRetrySucceedsFirstTry(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"prompt_id": "prompt-1",
		"litellm_params": map[string]interface{}{
			"prompt_integration": "langfuse",
		},
	})
	defer server.Close()

	r := &PromptResource{client: client}
	data := &PromptResourceModel{PromptID: types.StringValue("prompt-1")}
	if err := r.readPromptWithRetry(context.Background(), data, 3); err != nil {
		t.Fatalf("readPromptWithRetry: %v", err)
	}
}

// TestReadPropagatesClientError verifies readXxx surfaces transport errors from
// the API (here, a 500 that DoRequestWithResponse turns into an error).
func TestReadPropagatesClientError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}

	r := &SearchToolResource{client: client}
	data := &SearchToolResourceModel{SearchToolID: types.StringValue("st-1")}
	if err := r.readSearchTool(context.Background(), data); err == nil {
		t.Fatal("expected error from 500 response")
	}
}
