package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestGuardrailStructuredModeProtocol(t *testing.T) {
	ctx := context.Background()
	var captured atomic.Value
	mode := map[string]interface{}{"tags": map[string]interface{}{"trusted": "logging_only", "risky": []interface{}{"pre_call", "post_call"}}, "default": "pre_call"}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/guardrails":
			body, _ := io.ReadAll(request.Body)
			var payload map[string]interface{}
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Error(err)
			}
			captured.Store(payload)
			_, _ = fmt.Fprint(writer, `{"guardrail_id":"guardrail"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/guardrails/guardrail/info":
			_, _ = fmt.Fprint(writer, `{"guardrail_id":"guardrail","guardrail_name":"guardrail","guardrail_definition_location":"db","litellm_params":{"guardrail":"custom","mode":{"default":"pre_call","tags":{"risky":["pre_call","post_call"],"trusted":"logging_only"}}}}`)
		case request.Method == http.MethodGet && request.URL.Path == "/v2/guardrails/list":
			_, _ = fmt.Fprint(writer, `{"guardrails":[{"guardrail_id":"guardrail","guardrail_name":"guardrail","litellm_params":{"guardrail":"custom","mode":{"default":"pre_call","tags":{"risky":["pre_call","post_call"],"trusted":"logging_only"}}}}]}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_guardrail"
	schema := schemas.ResourceSchemas[typeName]
	configuredMode := `{ "tags": { "trusted": "logging_only", "risky": ["pre_call", "post_call"] }, "default": "pre_call" }`
	configValues := map[string]interface{}{"guardrail_name": "guardrail", "guardrail": "custom", "mode": configuredMode}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposedValues := map[string]interface{}{"guardrail_name": "guardrail", "guardrail": "custom", "mode": configuredMode, "id": tftypes.UnknownValue, "guardrail_id": tftypes.UnknownValue, "created_at": tftypes.UnknownValue}
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("apply: err=%v diagnostics=%v", err, applied.Diagnostics)
	}
	payload := captured.Load().(map[string]interface{})
	guardrail := payload["guardrail"].(map[string]interface{})
	params := guardrail["litellm_params"].(map[string]interface{})
	if fmt.Sprint(params["mode"]) == configuredMode {
		t.Fatal("structured mode was sent as a string")
	}
	if !reflect.DeepEqual(params["mode"], mode) {
		t.Fatalf("mode request=%#v", params["mode"])
	}
	var storedMode string
	if err := protocolAttributeMap(t, schema, applied.NewState)["mode"].As(&storedMode); err != nil || storedMode != configuredMode {
		t.Fatalf("stored mode=%q err=%v", storedMode, err)
	}

	singleSchema := schemas.DataSourceSchemas["litellm_guardrail"]
	singleConfig := accessGroupProtocolDynamicValue(t, singleSchema, organizationProjectProtocolValue(t, singleSchema, map[string]interface{}{"guardrail_id": "guardrail"}))
	single, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_guardrail", Config: singleConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(single.Diagnostics) {
		t.Fatalf("single: err=%v diagnostics=%v", err, single.Diagnostics)
	}
	var singleMode string
	if err := protocolAttributeMap(t, singleSchema, single.State)["mode"].As(&singleMode); err != nil || !jsonSemanticallyEqual(singleMode, configuredMode) {
		t.Fatalf("single mode=%q err=%v", singleMode, err)
	}

	listSchema := schemas.DataSourceSchemas["litellm_guardrails"]
	listConfig := accessGroupProtocolDynamicValue(t, listSchema, organizationProjectProtocolValue(t, listSchema, map[string]interface{}{}))
	list, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_guardrails", Config: listConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(list.Diagnostics) {
		t.Fatalf("list: err=%v diagnostics=%v", err, list.Diagnostics)
	}
	var items []tftypes.Value
	if err := protocolAttributeMap(t, listSchema, list.State)["guardrails"].As(&items); err != nil || len(items) != 1 {
		t.Fatalf("list items=%d err=%v", len(items), err)
	}
	var item map[string]tftypes.Value
	if err := items[0].As(&item); err != nil {
		t.Fatal(err)
	}
	var listMode string
	if err := item["mode"].As(&listMode); err != nil || !jsonSemanticallyEqual(listMode, configuredMode) {
		t.Fatalf("list mode=%q err=%v", listMode, err)
	}
}

func TestSemanticJSONPlanValidationProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const secret = "sentinel-json-secret"
	for name, test := range map[string]struct {
		typeName string
		values   map[string]interface{}
	}{
		"budget syntax":           {"litellm_budget", map[string]interface{}{"model_max_budget": `{"` + secret}},
		"budget nested shape":     {"litellm_budget", map[string]interface{}{"model_max_budget": `{"model":[]}`}},
		"search root":             {"litellm_search_tool", map[string]interface{}{"search_tool_name": "search", "search_provider": "provider", "search_tool_info": `[]`}},
		"prompt root":             {"litellm_prompt", map[string]interface{}{"prompt_id": "prompt", "prompt_integration": "custom", "environment": "development", "provider_specific_query_params": `null`}},
		"guardrail secret syntax": {"litellm_guardrail", map[string]interface{}{"guardrail_name": "guardrail", "guardrail": "custom", "mode": "pre_call", "litellm_params": `{"token":"` + secret}},
		"guardrail mode object":   {"litellm_guardrail", map[string]interface{}{"guardrail_name": "guardrail", "guardrail": "custom", "mode": `{"default":"pre_call"}`}},
	} {
		t.Run(name, func(t *testing.T) {
			schema := schemas.ResourceSchemas[test.typeName]
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, test.values))
			validated, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: test.typeName, Config: config})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(validated.Diagnostics) {
				t.Fatalf("validation: err=%v diagnostics=%v", err, validated.Diagnostics)
			}
			if strings.Contains(fmt.Sprint(validated.Diagnostics), secret) {
				t.Fatal("validation diagnostic leaked JSON content")
			}
		})
	}

	budgetSchema := schemas.ResourceSchemas["litellm_budget"]
	for name, value := range map[string]string{
		"canonical": `{"model":{"max_budget":10,"budget_duration":"1d","tpm_limit":9007199254740993}}`,
		"aliases":   `{"model":{"budget_limit":1e1,"time_period":"1d"}}`,
		"empty":     `{}`,
	} {
		t.Run("valid budget "+name, func(t *testing.T) {
			config := accessGroupProtocolDynamicValue(t, budgetSchema, organizationProjectProtocolValue(t, budgetSchema, map[string]interface{}{"model_max_budget": value}))
			validated, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: "litellm_budget", Config: config})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(validated.Diagnostics) {
				t.Fatalf("validation: err=%v diagnostics=%v", err, validated.Diagnostics)
			}
		})
	}

	guardrailSchema := schemas.ResourceSchemas["litellm_guardrail"]
	validMode := accessGroupProtocolDynamicValue(t, guardrailSchema, organizationProjectProtocolValue(t, guardrailSchema, map[string]interface{}{
		"guardrail_name": "guardrail", "guardrail": "custom", "mode": `{"tags":{"trusted":"logging_only"},"default":["pre_call","post_call"]}`,
	}))
	validated, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: "litellm_guardrail", Config: validMode})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(validated.Diagnostics) {
		t.Fatalf("valid structured mode: err=%v diagnostics=%v", err, validated.Diagnostics)
	}
}

func TestBudgetLegacyScalarCompatibilityProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_budget"
	schema := schemas.ResourceSchemas[typeName]
	legacy := `{"model":2}`
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"model_max_budget": legacy}))
	validated, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: typeName, Config: config})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(validated.Diagnostics) {
		t.Fatalf("legacy validation: err=%v diagnostics=%v", err, validated.Diagnostics)
	}
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	newProposed := organizationProjectProtocolValue(t, schema, map[string]interface{}{"id": tftypes.UnknownValue, "budget_id": tftypes.UnknownValue, "model_max_budget": legacy})
	newPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: accessGroupProtocolDynamicValue(t, schema, newProposed)})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(newPlan.Diagnostics) {
		t.Fatalf("new scalar plan: err=%v diagnostics=%v", err, newPlan.Diagnostics)
	}

	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"id": "budget", "budget_id": "budget", "model_max_budget": legacy}))
	unchanged, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: state})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(unchanged.Diagnostics) {
		t.Fatalf("unchanged scalar: err=%v diagnostics=%v", err, unchanged.Diagnostics)
	}
	changedConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"model_max_budget": `{"model":3}`}))
	changedProposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"model_max_budget": `{"model":3}`})
	changed, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: changedConfig, PriorState: state, ProposedNewState: changedProposed})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(changed.Diagnostics) {
		t.Fatalf("changed scalar: err=%v diagnostics=%v", err, changed.Diagnostics)
	}
	structuredConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"model_max_budget": `{"model":{"max_budget":2}}`}))
	structuredProposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"model_max_budget": `{"model":{"max_budget":2}}`})
	migrated, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: structuredConfig, PriorState: state, ProposedNewState: structuredProposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(migrated.Diagnostics) {
		t.Fatalf("structured migration: err=%v diagnostics=%v", err, migrated.Diagnostics)
	}
}

func TestBudgetLegacyScalarOmittedFromUnrelatedUpdateProtocol(t *testing.T) {
	ctx := context.Background()
	var payload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/budget/update":
			body, _ := io.ReadAll(request.Body)
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Error(err)
			}
			_, _ = fmt.Fprint(writer, `{}`)
		case "/budget/info":
			_, _ = fmt.Fprint(writer, `[{"budget_id":"budget","max_budget":2,"model_max_budget":{"model":2}}]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_budget"
	schema := schemas.ResourceSchemas[typeName]
	legacy := `{"model":2}`
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"id": "budget", "budget_id": "budget", "max_budget": 1.0, "model_max_budget": legacy}))
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"budget_id": "budget", "max_budget": 2.0, "model_max_budget": legacy}))
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"max_budget": 2.0})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("apply: err=%v diagnostics=%v", err, applied.Diagnostics)
	}
	if _, sent := payload["model_max_budget"]; sent {
		t.Fatalf("unchanged legacy scalar sent on update: %#v", payload)
	}
	if payload["max_budget"] == nil {
		t.Fatalf("max_budget update omitted: %#v", payload)
	}
}

func TestSemanticJSONSingleDataSourceIdentityProtocol(t *testing.T) {
	ctx := context.Background()
	var mode atomic.Value
	mode.Store("budget wrong id")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch mode.Load().(string) {
		case "budget duplicate":
			_, _ = fmt.Fprint(writer, `[{"budget_id":"budget"},{"budget_id":"budget"}]`)
		case "budget wrong id":
			_, _ = fmt.Fprint(writer, `[{"budget_id":"other"}]`)
		case "search missing id":
			_, _ = fmt.Fprint(writer, `{"search_tool_name":"search","litellm_params":{"search_provider":"provider"}}`)
		case "search wrong id":
			_, _ = fmt.Fprint(writer, `{"search_tool_id":"other","search_tool_name":"search","litellm_params":{"search_provider":"provider"}}`)
		case "search missing name":
			_, _ = fmt.Fprint(writer, `{"search_tool_id":"search","litellm_params":{"search_provider":"provider"}}`)
		case "search missing params":
			_, _ = fmt.Fprint(writer, `{"search_tool_id":"search","search_tool_name":"search"}`)
		case "search missing provider":
			_, _ = fmt.Fprint(writer, `{"search_tool_id":"search","search_tool_name":"search","litellm_params":{}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	budgetSchema := schemas.DataSourceSchemas["litellm_budget"]
	budgetConfig := accessGroupProtocolDynamicValue(t, budgetSchema, organizationProjectProtocolValue(t, budgetSchema, map[string]interface{}{"budget_id": "budget"}))
	searchSchema := schemas.DataSourceSchemas["litellm_search_tool"]
	searchConfig := accessGroupProtocolDynamicValue(t, searchSchema, organizationProjectProtocolValue(t, searchSchema, map[string]interface{}{"search_tool_id": "search"}))
	for _, failure := range []string{"budget duplicate", "budget wrong id", "search missing id", "search wrong id", "search missing name", "search missing params", "search missing provider"} {
		t.Run(failure, func(t *testing.T) {
			mode.Store(failure)
			typeName, config := "litellm_search_tool", searchConfig
			if strings.HasPrefix(failure, "budget") {
				typeName, config = "litellm_budget", budgetConfig
			}
			read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: typeName, Config: config})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
			}
		})
	}
}

func TestSemanticJSONDataSourceProjectionProtocol(t *testing.T) {
	ctx := context.Background()
	var malformed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		wrong := malformed.Load()
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/budget/info":
			if wrong {
				_, _ = fmt.Fprint(writer, `[{"budget_id":"budget","model_max_budget":[]}]`)
			} else {
				_, _ = fmt.Fprint(writer, `[{"budget_id":"budget","model_max_budget":{}}]`)
			}
		case request.Method == http.MethodGet && request.URL.Path == "/budget/list":
			if wrong {
				_, _ = fmt.Fprint(writer, `[{"budget_id":"budget","model_max_budget":"{}"}]`)
			} else {
				_, _ = fmt.Fprint(writer, `[{"budget_id":"budget","model_max_budget":{}}]`)
			}
		case request.Method == http.MethodGet && request.URL.Path == "/search_tools/search":
			if wrong {
				_, _ = fmt.Fprint(writer, `{"search_tool_id":"search","search_tool_name":"search","litellm_params":{"search_provider":"provider"},"search_tool_info":"{}"}`)
			} else {
				_, _ = fmt.Fprint(writer, `{"search_tool_id":"search","search_tool_name":"search","litellm_params":{"search_provider":"provider"},"search_tool_info":{"z":2,"a":1}}`)
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)

	budgetSchema := schemas.DataSourceSchemas["litellm_budget"]
	budgetConfig := accessGroupProtocolDynamicValue(t, budgetSchema, organizationProjectProtocolValue(t, budgetSchema, map[string]interface{}{"budget_id": "budget"}))
	budgetRead, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_budget", Config: budgetConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(budgetRead.Diagnostics) {
		t.Fatalf("budget read: err=%v diagnostics=%v", err, budgetRead.Diagnostics)
	}
	var budgetJSON string
	if err := protocolAttributeMap(t, budgetSchema, budgetRead.State)["model_max_budget"].As(&budgetJSON); err != nil || budgetJSON != `{}` {
		t.Fatalf("budget JSON=%q err=%v", budgetJSON, err)
	}

	searchSchema := schemas.DataSourceSchemas["litellm_search_tool"]
	searchConfig := accessGroupProtocolDynamicValue(t, searchSchema, organizationProjectProtocolValue(t, searchSchema, map[string]interface{}{"search_tool_id": "search"}))
	searchRead, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_search_tool", Config: searchConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(searchRead.Diagnostics) {
		t.Fatalf("search read: err=%v diagnostics=%v", err, searchRead.Diagnostics)
	}
	var searchJSON string
	if err := protocolAttributeMap(t, searchSchema, searchRead.State)["search_tool_info"].As(&searchJSON); err != nil || searchJSON != `{"a":1,"z":2}` {
		t.Fatalf("search JSON=%q err=%v", searchJSON, err)
	}

	listSchema := schemas.DataSourceSchemas["litellm_budgets"]
	listConfig := accessGroupProtocolDynamicValue(t, listSchema, organizationProjectProtocolValue(t, listSchema, map[string]interface{}{}))
	listRead, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_budgets", Config: listConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(listRead.Diagnostics) {
		t.Fatalf("list read: err=%v diagnostics=%v", err, listRead.Diagnostics)
	}
	var listValues []tftypes.Value
	if err := protocolAttributeMap(t, listSchema, listRead.State)["budgets"].As(&listValues); err != nil || len(listValues) != 1 {
		t.Fatalf("budget list: err=%v count=%d", err, len(listValues))
	}
	var listItem map[string]tftypes.Value
	if err := listValues[0].As(&listItem); err != nil {
		t.Fatal(err)
	}
	var listJSON string
	if err := listItem["model_max_budget"].As(&listJSON); err != nil || listJSON != `{}` {
		t.Fatalf("list JSON=%q err=%v", listJSON, err)
	}

	malformed.Store(true)
	for name, request := range map[string]*tfprotov6.ReadDataSourceRequest{
		"budget":  {TypeName: "litellm_budget", Config: budgetConfig},
		"budgets": {TypeName: "litellm_budgets", Config: listConfig},
		"search":  {TypeName: "litellm_search_tool", Config: searchConfig},
	} {
		t.Run(name+" malformed", func(t *testing.T) {
			read, err := protocolServer.ReadDataSource(ctx, request)
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("malformed read: err=%v diagnostics=%v", err, read.Diagnostics)
			}
		})
	}
}
