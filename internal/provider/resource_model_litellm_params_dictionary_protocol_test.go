package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func modelAdditionalLiteLLMParamsJSONCreateProposed(t *testing.T, schema *tfprotov6.Schema, values map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	proposedValues := map[string]interface{}{}
	for name, value := range values {
		proposedValues[name] = value
	}
	for _, attribute := range schema.Block.Attributes {
		if attribute.Computed {
			if _, present := proposedValues[attribute.Name]; !present {
				proposedValues[attribute.Name] = tftypes.UnknownValue
			}
		}
	}
	return accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
}

func TestModelAdditionalLiteLLMParamsJSONImportReplacementAndOverlapProtocol(t *testing.T) {
	ctx := context.Background()
	var reads, mutations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/model/info":
			reads.Add(1)
			_, _ = fmt.Fprint(writer, `{"data":[{"model_name":"imported-model","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini","remote_custom":{"native":true}},"model_info":{"id":"model-params-import","base_model":"gpt-4o-mini","tier":"free"}}]}`)
		case request.Method == http.MethodPost || request.Method == http.MethodPatch || request.Method == http.MethodDelete:
			mutations.Add(1)
			http.Error(writer, "unexpected mutation", http.StatusInternalServerError)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_model"
	schema := schemas.ResourceSchemas[typeName]
	var semanticAttributeFound bool
	for _, attribute := range schema.Block.Attributes {
		if attribute.Name == "additional_litellm_params_json" {
			semanticAttributeFound = true
			if !attribute.Optional || !attribute.Computed || !attribute.Sensitive {
				t.Fatalf("protocol schema attribute = %#v", attribute)
			}
		}
	}
	if !semanticAttributeFound {
		t.Fatal("protocol resource schema omitted additional_litellm_params_json")
	}
	for _, dataSourceName := range []string{"litellm_model", "litellm_models"} {
		for _, attribute := range schemas.DataSourceSchemas[dataSourceName].Block.Attributes {
			if attribute.Name == "additional_litellm_params_json" {
				t.Fatalf("protocol data-source schema %s exposed resource-owned JSON", dataSourceName)
			}
		}
	}
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "model-params-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	if value := protocolAttributeMap(t, schema, read.NewState)["additional_litellm_params_json"]; !value.IsNull() {
		t.Fatalf("import adopted remote params JSON: %s", value)
	}

	base := map[string]interface{}{"model_name": "imported-model", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini"}
	omittedConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, base))
	omitted, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: omittedConfig, PriorState: read.NewState, ProposedNewState: read.NewState, PriorPrivate: read.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(omitted.Diagnostics) || len(omitted.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted) != organizationProjectProtocolActionNoOp {
		t.Fatalf("omitted import plan: err=%v diagnostics=%v replace=%v", err, omitted.Diagnostics, omitted.RequiresReplace)
	}

	unknownValues := map[string]interface{}{"model_name": "imported-model", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini", "additional_litellm_params_json": tftypes.UnknownValue}
	unknownConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, unknownValues))
	unknownProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"additional_litellm_params_json": tftypes.UnknownValue})
	unknown, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: unknownConfig, PriorState: read.NewState, ProposedNewState: unknownProposed, PriorPrivate: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(unknown.Diagnostics) || len(unknown.RequiresReplace) == 0 {
		t.Fatalf("unknown takeover: err=%v diagnostics=%v replace=%v", err, unknown.Diagnostics, unknown.RequiresReplace)
	}

	configuredValues := map[string]interface{}{"model_name": "imported-model", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini", "additional_litellm_params_json": `{ "remote_custom":{"native":true} }`}
	configured := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configuredValues))
	configuredProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"additional_litellm_params_json": configuredValues["additional_litellm_params_json"]})
	takeover, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: configured, PriorState: read.NewState, ProposedNewState: configuredProposed, PriorPrivate: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(takeover.Diagnostics) || len(takeover.RequiresReplace) == 0 || organizationProjectProtocolPlannedAction(t, schema, read.NewState, takeover) != organizationProjectProtocolActionReplace {
		t.Fatalf("takeover: err=%v diagnostics=%v replace=%v", err, takeover.Diagnostics, takeover.RequiresReplace)
	}
	var plannedLegacy map[string]tftypes.Value
	if err := protocolAttributeMap(t, schema, takeover.PlannedState)["additional_litellm_params"].As(&plannedLegacy); err != nil {
		t.Fatalf("decode takeover legacy map: %v", err)
	}
	if _, duplicated := plannedLegacy["remote_custom"]; duplicated {
		t.Fatalf("JSON takeover retained owned key in legacy plan: %#v", plannedLegacy)
	}

	_, configuredProvenance, err := modelAdditionalLiteLLMParamsJSONConfiguration(ctx, types.StringValue(configuredValues["additional_litellm_params_json"].(string)), types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	configuredRaw, err := encodeModelAdditionalLiteLLMParamsJSONProvenance(ctx, configuredProvenance)
	if err != nil {
		t.Fatal(err)
	}
	privateValues := map[string][]byte{}
	if err := json.Unmarshal(read.Private, &privateValues); err != nil {
		t.Fatal(err)
	}
	privateValues[modelAdditionalLiteLLMParamsJSONProvenancePrivateKey] = configuredRaw
	ownedPrivate, err := json.Marshal(privateValues)
	if err != nil {
		t.Fatal(err)
	}
	ownedState := takeover.PlannedState
	steady, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: configured, PriorState: ownedState, ProposedNewState: ownedState, PriorPrivate: ownedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) || len(steady.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, ownedState, steady) != organizationProjectProtocolActionNoOp {
		t.Fatalf("owned steady plan: err=%v diagnostics=%v replace=%v", err, steady.Diagnostics, steady.RequiresReplace)
	}
	unknownOwnedProposed := organizationProjectProtocolReplace(t, schema, ownedState, map[string]interface{}{"additional_litellm_params_json": tftypes.UnknownValue})
	unknownOwned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: unknownConfig, PriorState: ownedState, ProposedNewState: unknownOwnedProposed, PriorPrivate: ownedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(unknownOwned.Diagnostics) || len(unknownOwned.RequiresReplace) == 0 {
		t.Fatalf("unknown owned plan: err=%v diagnostics=%v replace=%v", err, unknownOwned.Diagnostics, unknownOwned.RequiresReplace)
	}

	for name, overlapValue := range map[string]interface{}{
		"legacy":              map[string]tftypes.Value{"private-sensitive-key": tftypes.NewValue(tftypes.String, "private-value")},
		"tpm":                 nil,
		"budget":              nil,
		"nested null":         nil,
		"sensitive native":    nil,
		"sensitive list null": nil,
		"stringified null":    nil,
		"sensitive empty":     nil,
	} {
		values := map[string]interface{}{"model_name": "imported-model", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini"}
		switch name {
		case "legacy":
			values["additional_litellm_params"] = overlapValue
			values["additional_litellm_params_json"] = `{"private-sensitive-key":true}`
		case "tpm":
			values["additional_litellm_params_json"] = `{"tpm":1}`
		case "budget":
			values["additional_litellm_params_json"] = `{"max_budget":1}`
		case "nested null":
			values["additional_litellm_params_json"] = `{"nested":{"nullable":null}}`
		case "sensitive native":
			values["additional_litellm_params_json"] = `{"private_key":123}`
		case "sensitive list null":
			values["additional_litellm_params_json"] = `{"private_key":["safe",null]}`
		case "stringified null":
			values["additional_litellm_params_json"] = `{"literal":"None"}`
		case "sensitive empty":
			values["additional_litellm_params_json"] = `{"private_key":""}`
		}
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
		proposed := organizationProjectProtocolReplace(t, schema, read.NewState, values)
		planned, planErr := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: read.NewState, ProposedNewState: proposed, PriorPrivate: read.Private})
		if planErr != nil || !accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("%s overlap: err=%v diagnostics=%v", name, planErr, planned.Diagnostics)
		}
		for _, diagnostic := range planned.Diagnostics {
			text := diagnostic.Summary + diagnostic.Detail
			if strings.Contains(text, "private-sensitive-key") || strings.Contains(text, "private-value") {
				t.Fatalf("overlap diagnostic exposed content: %#v", diagnostic)
			}
		}
	}
	if reads.Load() != 1 || mutations.Load() != 0 {
		t.Fatalf("planning requests: reads=%d mutations=%d", reads.Load(), mutations.Load())
	}
}

func TestModelAdditionalLiteLLMParamsJSONCreateRecoversOwnedMasksProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/model/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodGet && request.URL.Path == "/model/info":
			identity := request.URL.Query().Get("litellm_model_id")
			_, _ = fmt.Fprintf(writer, `{"data":[{"model_name":"model-json","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini","typed":{"api_secret":"abcd***hijk","safe_literal":"****","api_only":true}},"model_info":{"id":%q,"base_model":"gpt-4o-mini"}}]}`, identity)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_model"
	schema := schemas.ResourceSchemas[typeName]
	configuredJSON := `{ "typed":{"api_secret":"abcdefghijk","safe_literal":"****"} }`
	values := map[string]interface{}{
		"model_name": "model-json", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini",
		"additional_litellm_params_json": configuredJSON,
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: modelAdditionalLiteLLMParamsJSONCreateProposed(t, schema, values)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("apply: err=%v diagnostics=%v", err, applied.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	var got string
	if err := attributes["additional_litellm_params_json"].As(&got); err != nil || got != configuredJSON {
		t.Fatalf("recovered JSON = %q, %v", got, err)
	}
	var legacy map[string]tftypes.Value
	if err := attributes["additional_litellm_params"].As(&legacy); err != nil {
		t.Fatal(err)
	}
	if _, leaked := legacy["typed"]; leaked {
		t.Fatalf("JSON-owned key leaked into legacy state: %#v", legacy)
	}
}

func TestModelAdditionalLiteLLMParamsJSONCreateProjectionFailurePublishesBothProvenances(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/model/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodGet && request.URL.Path == "/model/info":
			identity := request.URL.Query().Get("litellm_model_id")
			// model_info is complete, while the configured litellm_params JSON
			// leaf is missing and must trigger complete recovery state.
			_, _ = fmt.Fprintf(writer, `{"data":[{"model_name":"model-json","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini"},"model_info":{"id":%q,"base_model":"gpt-4o-mini","owned_info":{"native":true}}}]}`, identity)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_model"
	schema := schemas.ResourceSchemas[typeName]
	values := map[string]interface{}{
		"model_name": "model-json", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini",
		"additional_litellm_params_json": `{"owned_params":{"native":true}}`,
		"additional_model_info_json":     `{"owned_info":{"native":true}}`,
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: modelAdditionalLiteLLMParamsJSONCreateProposed(t, schema, values)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("apply: err=%v diagnostics=%v", err, applied.Diagnostics)
	}
	stateValue, err := applied.NewState.Unmarshal(schema.ValueType())
	if err != nil || stateValue.IsNull() {
		t.Fatalf("recovery state: value=%#v err=%v", stateValue, err)
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	for name, value := range attributes {
		if !value.IsKnown() {
			t.Fatalf("recovery attribute %q remained unknown", name)
		}
	}
	for _, name := range []string{"additional_litellm_params_json", "additional_model_info_json"} {
		var got string
		if err := attributes[name].As(&got); err != nil || got != values[name] {
			t.Fatalf("recovery %s = %q, %v", name, got, err)
		}
	}
	paramsRaw := protocolPrivateValue(t, applied.Private, modelAdditionalLiteLLMParamsJSONProvenancePrivateKey)
	paramsProvenance, err := decodeModelAdditionalLiteLLMParamsJSONProvenance(ctx, paramsRaw, types.StringValue(values["additional_litellm_params_json"].(string)))
	if err != nil || !paramsProvenance.Configured {
		t.Fatalf("params provenance: %#v %v", paramsProvenance, err)
	}
	infoRaw := protocolPrivateValue(t, applied.Private, modelAdditionalModelInfoJSONProvenancePrivateKey)
	infoProvenance, err := decodeModelAdditionalModelInfoJSONProvenance(ctx, infoRaw, types.StringValue(values["additional_model_info_json"].(string)))
	if err != nil || !infoProvenance.Configured {
		t.Fatalf("info provenance: %#v %v", infoProvenance, err)
	}
}

func TestModelAdditionalLiteLLMParamsJSONUpdateFailuresAreAtomicProtocol(t *testing.T) {
	ctx := context.Background()
	var mode atomic.Int32
	var patches atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		identity := request.URL.Query().Get("litellm_model_id")
		if identity == "" {
			identity = "model-json"
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/model/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodGet && request.URL.Path == "/model/info":
			switch mode.Load() {
			case 1:
				_, _ = fmt.Fprint(writer, `{"data":[{"litellm_params":{"typed":{"owned":true}},"model_info":{"base_model":"gpt-4o-mini"}}]}`)
			case 2:
				_, _ = fmt.Fprintf(writer, `{"data":[{"litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini","typed":{"owned":true,"api_secret":"abcd***hijk"}},"model_info":{"id":%q,"base_model":"gpt-4o-mini"}}]}`, identity)
			case 4:
				_, _ = fmt.Fprintf(writer, `{"data":[{"model_name":"model-json","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini"},"model_info":{"id":%q,"base_model":"gpt-4o-mini"}}]}`, identity)
			default:
				_, _ = fmt.Fprintf(writer, `{"data":[{"model_name":"model-json","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini","typed":{"owned":true,"api_only":true}},"model_info":{"id":%q,"base_model":"gpt-4o-mini"}}]}`, identity)
			}
		case request.Method == http.MethodPatch:
			patches.Add(1)
			if mode.Load() == 3 {
				http.Error(writer, "unavailable", http.StatusInternalServerError)
				return
			}
			_, _ = fmt.Fprint(writer, `{"litellm_params":{"tpm":100},"model_info":{}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_model"
	schema := schemas.ResourceSchemas[typeName]
	baseValues := map[string]interface{}{
		"model_name": "model-json", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini",
		"additional_litellm_params_json": `{"typed":{"owned":true}}`,
	}
	baseConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, baseValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	createPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: baseConfig, PriorState: nullState, ProposedNewState: modelAdditionalLiteLLMParamsJSONCreateProposed(t, schema, baseValues)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(createPlan.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%v", err, createPlan.Diagnostics)
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: baseConfig, PriorState: nullState, PlannedState: createPlan.PlannedState, PlannedPrivate: createPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("create: err=%v diagnostics=%v", err, created.Diagnostics)
	}

	updateValues := map[string]interface{}{}
	for name, value := range baseValues {
		updateValues[name] = value
	}
	updateValues["tpm"] = int64(100)
	updateConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, updateValues))
	updateProposed := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{"tpm": int64(100)})
	planUpdate := func() *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		planned, planErr := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: updateConfig, PriorState: created.NewState, ProposedNewState: updateProposed, PriorPrivate: created.Private})
		if planErr != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("update plan: err=%v diagnostics=%v", planErr, planned.Diagnostics)
		}
		return planned
	}
	assertRetained := func(label string, applied *tfprotov6.ApplyResourceChangeResponse, planned *tfprotov6.PlanResourceChangeResponse) {
		t.Helper()
		if !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
			t.Fatalf("%s unexpectedly succeeded", label)
		}
		priorValue, priorErr := created.NewState.Unmarshal(schema.ValueType())
		newValue, newErr := applied.NewState.Unmarshal(schema.ValueType())
		if priorErr != nil || newErr != nil || !priorValue.Equal(newValue) {
			t.Fatalf("%s changed public state: priorErr=%v newErr=%v", label, priorErr, newErr)
		}
		if !bytes.Equal(applied.Private, planned.PlannedPrivate) || !bytes.Equal(applied.Private, created.Private) {
			t.Fatalf("%s changed private state", label)
		}
	}

	for _, test := range []struct {
		name string
		mode int32
	}{
		{"identity hydration", 1},
		{"unowned masked sibling hydration", 2},
		{"PATCH", 3},
		{"readback projection", 4},
	} {
		t.Run(test.name, func(t *testing.T) {
			mode.Store(test.mode)
			beforePatches := patches.Load()
			planned := planUpdate()
			applied, applyErr := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: updateConfig, PriorState: created.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if applyErr != nil {
				t.Fatal(applyErr)
			}
			assertRetained(test.name, applied, planned)
			wantDelta := int64(0)
			if test.mode >= 3 {
				wantDelta = 1
			}
			if got := patches.Load() - beforePatches; got != wantDelta {
				t.Fatalf("%s PATCH delta = %d; want %d", test.name, got, wantDelta)
			}
		})
	}
}

func TestModelAdditionalLiteLLMParamsJSONPrivatePathBindingProtocol(t *testing.T) {
	ctx := context.Background()
	state := types.StringValue(`{"owned":{"leaf":true}}`)
	_, provenance, err := modelAdditionalLiteLLMParamsJSONConfiguration(ctx, state, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeModelAdditionalLiteLLMParamsJSONProvenance(ctx, provenance)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]interface{}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	wire["terraform_owned"] = []interface{}{`/different`}
	mismatched, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := decodeModelAdditionalLiteLLMParamsJSONProvenance(ctx, mismatched, state); err == nil || value.Initialized {
		t.Fatalf("mismatched private provenance decoded as %#v, %v", value, err)
	}
}
