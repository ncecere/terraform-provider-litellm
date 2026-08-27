package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestStage4CheckedComputedConstructorsRejectAtomicallyAndSanitizeDiagnostics(t *testing.T) {
	t.Parallel()

	list, listDiagnostics := checkedStringListValue(context.Background(), []attr.Value{
		types.StringValue("valid-before-error"),
		types.Int64Value(42),
	}, path.Root("models"))
	if !list.IsNull() || !listDiagnostics.HasError() {
		t.Fatalf("malformed list = %#v diagnostics=%#v", list, listDiagnostics)
	}
	assertCollectionDiagnostics(t, listDiagnostics, 1, "models[1]")

	mapped, mapDiagnostics := checkedStringMapValue(context.Background(), map[string]attr.Value{
		"ordinary":   types.StringValue("valid"),
		"secret-key": types.StringNull(),
	}, path.Root("metadata"), true)
	if !mapped.IsNull() || !mapDiagnostics.HasError() {
		t.Fatalf("malformed map = %#v diagnostics=%#v", mapped, mapDiagnostics)
	}
	for _, diagnostic := range mapDiagnostics {
		text := diagnostic.Summary() + " " + diagnostic.Detail()
		if strings.Contains(text, "secret-key") || strings.Contains(text, "ordinary") {
			t.Fatalf("diagnostic leaked collection content: %q", text)
		}
	}
}

func TestStage4ResponseProjectionPreservesNullEmptyOrderAndHonorsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if value, _, diagnostics := strictAPIStringList(ctx, map[string]interface{}{"models": []interface{}{"one"}}, "models", path.Root("models")); !value.IsNull() || !diagnostics.HasError() {
		t.Fatalf("canceled projection = %#v diagnostics=%#v", value, diagnostics)
	}

	nullValue, nullPresence, diagnostics := strictAPIStringList(context.Background(), map[string]interface{}{"models": nil}, "models", path.Root("models"))
	if diagnostics.HasError() || nullPresence != apiValueNull || !nullValue.IsNull() {
		t.Fatalf("null projection = %#v presence=%v diagnostics=%#v", nullValue, nullPresence, diagnostics)
	}
	emptyValue, emptyPresence, diagnostics := strictAPIStringList(context.Background(), map[string]interface{}{"models": []interface{}{}}, "models", path.Root("models"))
	if diagnostics.HasError() || emptyPresence != apiValuePresent || emptyValue.IsNull() || len(emptyValue.Elements()) != 0 {
		t.Fatalf("empty projection = %#v presence=%v diagnostics=%#v", emptyValue, emptyPresence, diagnostics)
	}
	ordered, _, diagnostics := strictAPIStringList(context.Background(), map[string]interface{}{"models": []interface{}{"b", "a", "b"}}, "models", path.Root("models"))
	if diagnostics.HasError() || ordered.Elements()[0].(types.String).ValueString() != "b" || ordered.Elements()[2].(types.String).ValueString() != "b" {
		t.Fatalf("ordered projection = %#v diagnostics=%#v", ordered, diagnostics)
	}
}

func TestStage4AgentNestedSecurityLateFailureRetainsCompletePriorCard(t *testing.T) {
	t.Parallel()

	prior := AgentResourceModel{AgentCard: &AgentCardModel{
		Name:              types.StringValue("prior"),
		URL:               types.StringValue("https://prior.invalid"),
		DefaultInputModes: stringListValue("prior-mode"),
		Skills:            []AgentSkillModel{{ID: types.StringValue("prior-skill"), Name: types.StringValue("Prior")}},
	}}
	data := cloneAgentResourceModel(prior)
	raw := map[string]interface{}{
		"name":              "remote",
		"url":               "https://remote.invalid",
		"defaultInputModes": []interface{}{"text"},
		"skills": []interface{}{
			map[string]interface{}{"id": "good", "name": "Good", "security": []interface{}{map[string]interface{}{"oauth": []interface{}{"read"}}}},
			map[string]interface{}{"id": "bad", "name": "Bad", "security": []interface{}{map[string]interface{}{"oauth": []interface{}{"read", json.Number("2")}}}},
		},
	}
	if err := (&AgentResource{}).readAgentCardContext(context.Background(), raw, &data); err == nil {
		t.Fatal("late malformed security was accepted")
	}
	if !reflect.DeepEqual(data, prior) {
		t.Fatalf("late failure published partial agent card:\n got %#v\nwant %#v", data, prior)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readAgentSecurityContext(ctx, []interface{}{}); err == nil {
		t.Fatal("canceled nested security projection succeeded")
	}
}

func TestStage4TagLateHugeModelBudgetFailureRetainsPriorState(t *testing.T) {
	t.Parallel()

	prior := TagResourceModel{
		ID:             types.StringValue("tag-a"),
		Name:           types.StringValue("tag-a"),
		Models:         stringListValue("prior-model"),
		BudgetID:       types.StringValue("budget-prior"),
		TPMLimit:       types.Int64Value(7),
		ModelMaxBudget: types.StringValue(`{"prior":{"max_budget":1}}`),
	}
	data := prior
	response := map[string]interface{}{
		"name":   "tag-a",
		"models": []interface{}{"new-model"},
		"litellm_budget_table": map[string]interface{}{
			"budget_id":        "budget-new",
			"tpm_limit":        json.Number("9223372036854775808"),
			"model_max_budget": map[string]interface{}{"model": map[string]interface{}{"max_budget": json.Number("1")}},
		},
	}
	if err := applyTagObjectToResource(context.Background(), &data, response, "tag-a", true); err == nil {
		t.Fatal("out-of-range late budget value was accepted")
	}
	if !reflect.DeepEqual(data, prior) {
		t.Fatalf("late tag failure published partial state:\n got %#v\nwant %#v", data, prior)
	}
}

func TestStage4MalformedRefreshRetainsCompletePriorProtocolState(t *testing.T) {
	ctx := context.Background()
	var malformed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		models := []interface{}{"secondary"}
		if malformed.Load() {
			models = append(models, json.Number("42"))
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"model": "primary", "fallback_type": "general", "fallback_models": models,
		})
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_fallback"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "primary:general"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	valid, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(valid.Diagnostics) {
		t.Fatalf("valid refresh: err=%v diagnostics=%v", err, valid.Diagnostics)
	}
	malformed.Store(true)
	failed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: typeName, CurrentState: valid.NewState, Private: valid.Private,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) {
		t.Fatalf("malformed refresh: err=%v diagnostics=%v", err, failed.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schema, failed.NewState)
	var id string
	if err := attributes["id"].As(&id); err != nil || id != "primary:general" {
		t.Fatalf("failed refresh did not retain prior identity: id=%q err=%v", id, err)
	}
	var modelValues []tftypes.Value
	if err := attributes["fallback_models"].As(&modelValues); err != nil || len(modelValues) != 1 {
		t.Fatalf("failed refresh did not retain prior collection: values=%#v err=%v", modelValues, err)
	}
	var model string
	if err := modelValues[0].As(&model); err != nil || model != "secondary" {
		t.Fatalf("failed refresh retained model=%q err=%v", model, err)
	}
}

func TestStage4UnifiedGroupLateMalformedCollectionFailsBeforeProjection(t *testing.T) {
	t.Parallel()

	response := map[string]interface{}{
		"access_model_names":    []interface{}{"valid"},
		"access_mcp_server_ids": []interface{}{},
		"access_agent_ids":      []interface{}{"valid", nil},
		"assigned_team_ids":     []interface{}{"team"},
	}
	if err := validateUnifiedAccessGroupResponseCollections(context.Background(), response); err == nil {
		t.Fatal("late malformed unified access-group element was accepted")
	}
}
