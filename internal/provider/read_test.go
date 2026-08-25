package provider

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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
		"max_budget":            100.25,
		"soft_budget":           80.5,
		"max_parallel_requests": int64(math.MinInt64),
		"tpm_limit":             int64(math.MaxInt64),
		"rpm_limit":             int64(9007199254740993),
		"budget_duration":       "30d",
	}})
	defer server.Close()

	r := &BudgetResource{client: client}
	data := &BudgetResourceModel{BudgetID: types.StringValue("budget-1")}
	if err := r.readBudgetWithNumericOwnership(context.Background(), data, true); err != nil {
		t.Fatalf("readBudget: %v", err)
	}

	if data.ID.ValueString() != "budget-1" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	if data.MaxBudget.ValueFloat64() != 100.25 || data.SoftBudget.ValueFloat64() != 80.5 {
		t.Errorf("budgets = %v, %v", data.MaxBudget.ValueFloat64(), data.SoftBudget.ValueFloat64())
	}
	if data.MaxParallelRequests.ValueInt64() != math.MinInt64 || data.TPMLimit.ValueInt64() != math.MaxInt64 || data.RPMLimit.ValueInt64() != 9007199254740993 {
		t.Errorf("integer limits = %d, %d, %d", data.MaxParallelRequests.ValueInt64(), data.TPMLimit.ValueInt64(), data.RPMLimit.ValueInt64())
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
			"name":        "prod",
			"description": "production",
			"budget_id":   "b-1",
			"litellm_budget_table": map[string]interface{}{
				"max_budget":            50.75,
				"max_parallel_requests": int64(9007199254740993),
				"tpm_limit":             int64(math.MinInt64),
				"rpm_limit":             int64(math.MaxInt64),
				"budget_duration":       "7d",
			},
		},
	})
	defer server.Close()

	r := &TagResource{client: client}
	data := &TagResourceModel{
		Name:                types.StringValue("prod"),
		MaxBudget:           types.Float64Value(1),
		MaxParallelRequests: types.Int64Value(1),
		TPMLimit:            types.Int64Value(1),
		RPMLimit:            types.Int64Value(1),
	}
	if err := r.readTag(context.Background(), data); err != nil {
		t.Fatalf("readTag: %v", err)
	}

	if data.ID.ValueString() != "prod" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	if data.Description.ValueString() != "production" {
		t.Errorf("description = %q", data.Description.ValueString())
	}
	if data.MaxBudget.ValueFloat64() != 50.75 {
		t.Errorf("max_budget = %v", data.MaxBudget.ValueFloat64())
	}
	if data.MaxParallelRequests.ValueInt64() != 9007199254740993 || data.TPMLimit.ValueInt64() != math.MinInt64 || data.RPMLimit.ValueInt64() != math.MaxInt64 {
		t.Errorf("tag integer limits = %d, %d, %d", data.MaxParallelRequests.ValueInt64(), data.TPMLimit.ValueInt64(), data.RPMLimit.ValueInt64())
	}
}

func TestReadCredential(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"credential_name":   "openai-prod",
		"credential_info":   map[string]interface{}{"env": "prod"},
		"credential_values": map[string]interface{}{},
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
			"litellm_budget_table": map[string]interface{}{
				"max_budget": 500.5,
				"tpm_limit":  int64(9007199254740993),
				"rpm_limit":  int64(math.MaxInt64),
			},
			"metadata": map[string]interface{}{
				"environment":     "production",
				"model_rpm_limit": map[string]int64{"large": 9007199254740993},
				"model_tpm_limit": map[string]int64{"maximum": math.MaxInt64},
			},
		},
	})
	defer server.Close()

	r := &OrganizationResource{client: client}
	data := &OrganizationResourceModel{
		OrganizationID: types.StringValue("org-1"),
		MaxBudget:      types.Float64Value(1),
		TPMLimit:       types.Int64Value(1),
		RPMLimit:       types.Int64Value(1),
		ModelRPMLimit:  types.MapUnknown(types.Int64Type),
		ModelTPMLimit:  types.MapUnknown(types.Int64Type),
	}
	if err := r.readOrganizationWithNumericOwnership(context.Background(), data, true); err != nil {
		t.Fatalf("readOrganization: %v", err)
	}

	if data.ID.ValueString() != "org-1" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	if data.OrganizationAlias.ValueString() != "acme" {
		t.Errorf("organization_alias = %q", data.OrganizationAlias.ValueString())
	}
	if data.MaxBudget.ValueFloat64() != 500.5 || data.TPMLimit.ValueInt64() != 9007199254740993 || data.RPMLimit.ValueInt64() != math.MaxInt64 {
		t.Errorf("organization numbers = %v, %d, %d", data.MaxBudget.ValueFloat64(), data.TPMLimit.ValueInt64(), data.RPMLimit.ValueInt64())
	}
	var modelRPM, modelTPM map[string]int64
	data.ModelRPMLimit.ElementsAs(context.Background(), &modelRPM, false)
	data.ModelTPMLimit.ElementsAs(context.Background(), &modelTPM, false)
	if modelRPM["large"] != 9007199254740993 || modelTPM["maximum"] != math.MaxInt64 {
		t.Errorf("organization model limits = %#v, %#v", modelRPM, modelTPM)
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
	if err := r.readGuardrail(context.Background(), data, false); err != nil {
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
			"timeout":         30.5,
			"max_retries":     int64(9007199254740993),
		},
	})
	defer server.Close()

	r := &SearchToolResource{client: client}
	data := &SearchToolResourceModel{SearchToolID: types.StringValue("st-1")}
	if err := r.readSearchToolWithNumericOwnership(context.Background(), data, true); err != nil {
		t.Fatalf("readSearchTool: %v", err)
	}

	if data.ID.ValueString() != "st-1" {
		t.Errorf("id = %q", data.ID.ValueString())
	}
	if data.SearchProvider.ValueString() != "tavily" {
		t.Errorf("search_provider = %q", data.SearchProvider.ValueString())
	}
	if data.Timeout.ValueFloat64() != 30.5 {
		t.Errorf("timeout = %v", data.Timeout.ValueFloat64())
	}
	if data.MaxRetries.ValueInt64() != 9007199254740993 {
		t.Errorf("max_retries = %v", data.MaxRetries.ValueInt64())
	}
}

func TestReadKeyPreservesExactNestedLimits(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"key": "sk-test",
		"info": map[string]interface{}{
			"tpm_limit":             int64(9007199254740993),
			"rpm_limit":             int64(math.MaxInt64),
			"max_parallel_requests": int64(math.MinInt64),
			"metadata": map[string]interface{}{
				"model_rpm_limit": map[string]int64{"large": 9007199254740993},
				"model_tpm_limit": map[string]int64{"maximum": math.MaxInt64},
			},
		},
	})
	defer server.Close()

	r := &KeyResource{client: client}
	data := &KeyResourceModel{
		Key:           types.StringValue("sk-test"),
		ModelRPMLimit: types.MapUnknown(types.Int64Type),
		ModelTPMLimit: types.MapUnknown(types.Int64Type),
	}
	if err := r.readKeyWithNumericOwnership(context.Background(), data, true); err != nil {
		t.Fatalf("readKey: %v", err)
	}
	if data.TPMLimit.ValueInt64() != 9007199254740993 || data.RPMLimit.ValueInt64() != math.MaxInt64 || data.MaxParallelRequests.ValueInt64() != math.MinInt64 {
		t.Errorf("key limits = %d, %d, %d", data.TPMLimit.ValueInt64(), data.RPMLimit.ValueInt64(), data.MaxParallelRequests.ValueInt64())
	}
	var modelRPM, modelTPM map[string]int64
	data.ModelRPMLimit.ElementsAs(context.Background(), &modelRPM, false)
	data.ModelTPMLimit.ElementsAs(context.Background(), &modelTPM, false)
	if modelRPM["large"] != 9007199254740993 || modelTPM["maximum"] != math.MaxInt64 {
		t.Errorf("key model limits = %#v, %#v", modelRPM, modelTPM)
	}
}

func TestReadAgentPreservesExactLimits(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"agent_id":          "agent-1",
		"agent_name":        "agent",
		"tpm_limit":         int64(9007199254740993),
		"rpm_limit":         int64(math.MaxInt64),
		"session_tpm_limit": int64(math.MinInt64),
		"session_rpm_limit": int64(9007199254740991),
	})
	defer server.Close()

	r := &AgentResource{client: client}
	data := &AgentResourceModel{ID: types.StringValue("agent-1")}
	if err := r.readAgentWithNumericOwnership(context.Background(), data, true); err != nil {
		t.Fatalf("readAgent: %v", err)
	}
	if data.TPMLimit.ValueInt64() != 9007199254740993 || data.RPMLimit.ValueInt64() != math.MaxInt64 || data.SessionTPMLimit.ValueInt64() != math.MinInt64 || data.SessionRPMLimit.ValueInt64() != 9007199254740991 {
		t.Errorf("agent limits = %d, %d, %d, %d", data.TPMLimit.ValueInt64(), data.RPMLimit.ValueInt64(), data.SessionTPMLimit.ValueInt64(), data.SessionRPMLimit.ValueInt64())
	}
}

func TestReadModelPreservesExactLimitsAndFloatCosts(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"model_name": "model-1",
		"litellm_params": map[string]interface{}{
			"tpm":                   int64(9007199254740993),
			"rpm":                   int64(math.MaxInt64),
			"input_cost_per_token":  0.00000125,
			"output_cost_per_token": 0.0000025,
			"thinking": map[string]interface{}{
				"type":          "enabled",
				"budget_tokens": int64(9007199254740993),
			},
		},
	})
	defer server.Close()

	r := &ModelResource{client: client}
	data := &ModelResourceModel{
		ID:                         types.StringValue("model-1"),
		TPM:                        types.Int64Value(1),
		RPM:                        types.Int64Value(1),
		InputCostPerMillionTokens:  types.Float64Value(1),
		OutputCostPerMillionTokens: types.Float64Value(1),
		ThinkingEnabled:            types.BoolValue(true),
	}
	if err := r.readModel(context.Background(), data); err != nil {
		t.Fatalf("readModel: %v", err)
	}
	if data.TPM.ValueInt64() != 9007199254740993 || data.RPM.ValueInt64() != math.MaxInt64 || data.ThinkingBudgetTokens.ValueInt64() != 9007199254740993 {
		t.Errorf("model limits = %d, %d, %d", data.TPM.ValueInt64(), data.RPM.ValueInt64(), data.ThinkingBudgetTokens.ValueInt64())
	}
	if data.InputCostPerMillionTokens.ValueFloat64() != 1.25 || data.OutputCostPerMillionTokens.ValueFloat64() != 2.5 {
		t.Errorf("model costs = %v, %v", data.InputCostPerMillionTokens.ValueFloat64(), data.OutputCostPerMillionTokens.ValueFloat64())
	}
}

func TestReadTeamPreservesExactNestedLimits(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"team_member_permissions": []interface{}{},
		"team_info": map[string]interface{}{
			"team_id":    "team-1",
			"team_alias": "team",
			"tpm_limit":  int64(9007199254740993),
			"rpm_limit":  int64(math.MaxInt64),
			"team_member_budget_table": map[string]interface{}{
				"team_member_tpm_limit": int64(math.MinInt64),
				"team_member_rpm_limit": int64(9007199254740991),
			},
			"metadata": map[string]interface{}{
				"model_rpm_limit": map[string]int64{"large": 9007199254740993},
				"model_tpm_limit": map[string]int64{"maximum": math.MaxInt64},
			},
		},
	})
	defer server.Close()

	r := &TeamResource{client: client}
	data := &TeamResourceModel{
		ID:                 types.StringValue("team-1"),
		TPMLimit:           types.Int64Value(1),
		RPMLimit:           types.Int64Value(1),
		TeamMemberTPMLimit: types.Int64Unknown(),
		TeamMemberRPMLimit: types.Int64Unknown(),
		ModelRPMLimit:      types.MapUnknown(types.Int64Type),
		ModelTPMLimit:      types.MapUnknown(types.Int64Type),
	}
	if err := r.readTeamWithNumericOwnership(context.Background(), data, true); err != nil {
		t.Fatalf("readTeam: %v", err)
	}
	if data.TPMLimit.ValueInt64() != 9007199254740993 || data.RPMLimit.ValueInt64() != math.MaxInt64 || data.TeamMemberTPMLimit.ValueInt64() != math.MinInt64 || data.TeamMemberRPMLimit.ValueInt64() != 9007199254740991 {
		t.Errorf("team limits = %d, %d, %d, %d", data.TPMLimit.ValueInt64(), data.RPMLimit.ValueInt64(), data.TeamMemberTPMLimit.ValueInt64(), data.TeamMemberRPMLimit.ValueInt64())
	}
	var modelRPM, modelTPM map[string]int64
	data.ModelRPMLimit.ElementsAs(context.Background(), &modelRPM, false)
	data.ModelTPMLimit.ElementsAs(context.Background(), &modelTPM, false)
	if modelRPM["large"] != 9007199254740993 || modelTPM["maximum"] != math.MaxInt64 {
		t.Errorf("team model limits = %#v, %#v", modelRPM, modelTPM)
	}
}

func TestReadProjectPreservesExactNestedLimits(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"project_id": "project-1",
		"team_id":    "team-1",
		"litellm_budget_table": map[string]interface{}{
			"max_budget":            100.25,
			"soft_budget":           80.5,
			"tpm_limit":             int64(9007199254740993),
			"rpm_limit":             int64(math.MaxInt64),
			"max_parallel_requests": int64(math.MinInt64),
			"model_max_budget":      map[string]float64{"fractional": 12.5},
		},
		"metadata": map[string]interface{}{
			"model_rpm_limit": map[string]int64{"large": 9007199254740993},
			"model_tpm_limit": map[string]int64{"maximum": math.MaxInt64},
		},
	})
	defer server.Close()

	r := &ProjectResource{client: client}
	data := &ProjectResourceModel{
		ID:                  types.StringValue("project-1"),
		MaxBudget:           types.Float64Value(1),
		SoftBudget:          types.Float64Value(1),
		TPMLimit:            types.Int64Value(1),
		RPMLimit:            types.Int64Value(1),
		MaxParallelRequests: types.Int64Value(1),
		ModelMaxBudget:      types.MapUnknown(types.Float64Type),
		ModelRPMLimit:       types.MapUnknown(types.Int64Type),
		ModelTPMLimit:       types.MapUnknown(types.Int64Type),
	}
	if err := r.readProjectWithNumericOwnership(context.Background(), data, true); err != nil {
		t.Fatalf("readProject: %v", err)
	}
	var modelRPM, modelTPM map[string]int64
	data.ModelRPMLimit.ElementsAs(context.Background(), &modelRPM, false)
	data.ModelTPMLimit.ElementsAs(context.Background(), &modelTPM, false)
	if data.MaxBudget.ValueFloat64() != 100.25 || data.SoftBudget.ValueFloat64() != 80.5 || data.TPMLimit.ValueInt64() != 9007199254740993 || data.RPMLimit.ValueInt64() != math.MaxInt64 || data.MaxParallelRequests.ValueInt64() != math.MinInt64 {
		t.Errorf("project numbers = %v, %v, %d, %d, %d", data.MaxBudget.ValueFloat64(), data.SoftBudget.ValueFloat64(), data.TPMLimit.ValueInt64(), data.RPMLimit.ValueInt64(), data.MaxParallelRequests.ValueInt64())
	}
	if modelRPM["large"] != 9007199254740993 || modelTPM["maximum"] != math.MaxInt64 {
		t.Errorf("project nested numbers = %#v, %#v", modelRPM, modelTPM)
	}
	if !data.ModelMaxBudget.IsNull() {
		t.Fatalf("legacy map(float64) adopted remote model budget: %#v", data.ModelMaxBudget)
	}
}

func TestManagedReadRejectsMalformedNumericWithoutEcho(t *testing.T) {
	t.Parallel()

	secret := "https://secret.invalid/body?token=must-not-echo"
	server, client := jsonServer(t, []map[string]interface{}{{
		"budget_id": "budget-1",
		"tpm_limit": secret,
	}})
	defer server.Close()
	err := (&BudgetResource{client: client}).readBudget(context.Background(), &BudgetResourceModel{BudgetID: types.StringValue("budget-1")})
	if err == nil || !strings.Contains(err.Error(), "tpm_limit") {
		t.Fatalf("missing field-specific numeric error: %v", err)
	}
	if strings.Contains(err.Error(), "secret.invalid") || strings.Contains(err.Error(), "must-not-echo") {
		t.Fatalf("numeric error exposed response value: %v", err)
	}
}

func TestReadProjectRemoteNumericClearsAreVisible(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"project_id": "project-1",
		"litellm_budget_table": map[string]interface{}{
			"max_budget":            nil,
			"soft_budget":           nil,
			"tpm_limit":             nil,
			"rpm_limit":             nil,
			"max_parallel_requests": nil,
			"model_max_budget":      nil,
		},
		"metadata": map[string]interface{}{
			"model_rpm_limit": nil,
			"model_tpm_limit": nil,
		},
	})
	defer server.Close()

	configuredIntMap := types.MapValueMust(types.Int64Type, map[string]attr.Value{"model": types.Int64Value(1)})
	configuredFloatMap := types.MapValueMust(types.Float64Type, map[string]attr.Value{"model": types.Float64Value(1)})
	data := &ProjectResourceModel{
		ID:                  types.StringValue("project-1"),
		MaxBudget:           types.Float64Value(1),
		SoftBudget:          types.Float64Value(1),
		TPMLimit:            types.Int64Value(1),
		RPMLimit:            types.Int64Value(1),
		MaxParallelRequests: types.Int64Value(1),
		ModelMaxBudget:      configuredFloatMap,
		ModelRPMLimit:       configuredIntMap,
		ModelTPMLimit:       configuredIntMap,
	}
	if err := (&ProjectResource{client: client}).readProject(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if !data.MaxBudget.IsNull() || !data.SoftBudget.IsNull() || !data.TPMLimit.IsNull() || !data.RPMLimit.IsNull() || !data.MaxParallelRequests.IsNull() {
		t.Fatalf("project scalar clears stayed stale: %#v", data)
	}
	if !data.ModelMaxBudget.IsNull() || !data.ModelRPMLimit.IsNull() || !data.ModelTPMLimit.IsNull() {
		t.Fatalf("project map clears stayed stale: %#v %#v %#v", data.ModelMaxBudget, data.ModelRPMLimit, data.ModelTPMLimit)
	}
}

func TestKeyMetadataRateOmissionPreservesConfiguredState(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"info": map[string]interface{}{"metadata": map[string]interface{}{}},
	})
	defer server.Close()
	configured := types.MapValueMust(types.Int64Type, map[string]attr.Value{"model": types.Int64Value(1)})
	data := &KeyResourceModel{
		Key:           types.StringValue("sk-test"),
		ModelRPMLimit: configured,
		ModelTPMLimit: configured,
	}
	if err := (&KeyResource{client: client}).readKey(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if !data.ModelRPMLimit.Equal(configured) || !data.ModelTPMLimit.Equal(configured) {
		t.Fatalf("key metadata omission discarded configured rates: %#v %#v", data.ModelRPMLimit, data.ModelTPMLimit)
	}
}

func TestOrganizationPerModelMetadataOmissionClearsOwnedState(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"organization_info": map[string]interface{}{
			"organization_id":      "org-1",
			"organization_alias":   "acme",
			"litellm_budget_table": nil,
		},
	})
	defer server.Close()
	configured := types.MapValueMust(types.Int64Type, map[string]attr.Value{"model": types.Int64Value(1)})
	data := &OrganizationResourceModel{
		OrganizationID: types.StringValue("org-1"),
		MaxBudget:      types.Float64Value(1),
		TPMLimit:       types.Int64Value(1),
		RPMLimit:       types.Int64Value(1),
		ModelRPMLimit:  configured,
		ModelTPMLimit:  configured,
	}
	if err := (&OrganizationResource{client: client}).readOrganization(context.Background(), data); err != nil {
		t.Fatal(err)
	}
	if !data.MaxBudget.IsNull() || !data.TPMLimit.IsNull() || !data.RPMLimit.IsNull() {
		t.Fatalf("organization budget relation null did not clear owned scalars: %#v", data)
	}
	if !data.ModelRPMLimit.IsNull() || !data.ModelTPMLimit.IsNull() {
		t.Fatal("organization per-model state was not cleared when its exact metadata source omitted the keys")
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
	if err := r.readVectorStore(context.Background(), data, false, false); err != nil {
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
		"credential_name":   "cred-1",
		"credential_info":   map[string]interface{}{},
		"credential_values": map[string]interface{}{},
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
