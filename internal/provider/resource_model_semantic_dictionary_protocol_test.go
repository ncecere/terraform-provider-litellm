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

func modelAdditionalModelInfoJSONCreateProposed(t *testing.T, schema *tfprotov6.Schema, values map[string]interface{}) *tfprotov6.DynamicValue {
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

func TestModelAdditionalModelInfoJSONCreateFailureDoesNotPublishStateOrNewPrivate(t *testing.T) {
	ctx := context.Background()
	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == "/model/new" {
			posts.Add(1)
			http.Error(writer, "unavailable", http.StatusInternalServerError)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_model"
	schema := schemas.ResourceSchemas[typeName]
	values := map[string]interface{}{
		"model_name": "model-json", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini",
		"additional_model_info_json": `{"owned":{"native":true}}`,
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	proposed := modelAdditionalModelInfoJSONCreateProposed(t, schema, values)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || posts.Load() != 1 {
		t.Fatalf("apply: err=%v diagnostics=%v posts=%d", err, applied.Diagnostics, posts.Load())
	}
	stateValue, err := applied.NewState.Unmarshal(schema.ValueType())
	if err != nil || !stateValue.IsNull() {
		t.Fatalf("failed create published state: value=%#v err=%v", stateValue, err)
	}
	if !bytes.Equal(applied.Private, planned.PlannedPrivate) {
		t.Fatalf("failed create mutated planned private state")
	}
}

func TestModelAdditionalModelInfoJSONCreateProjectionFailurePublishesCompleteRecovery(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/model/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodGet && request.URL.Path == "/model/info":
			identity := request.URL.Query().Get("litellm_model_id")
			_, _ = fmt.Fprintf(writer, `{"data":[{"model_name":"model-json","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini"},"model_info":{"id":%q,"base_model":"gpt-4o-mini"}}]}`, identity)
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
		"additional_model_info_json": `{"owned":{"native":true}}`,
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: modelAdditionalModelInfoJSONCreateProposed(t, schema, values)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("apply: err=%v diagnostics=%v", err, applied.Diagnostics)
	}
	stateValue, err := applied.NewState.Unmarshal(schema.ValueType())
	if err != nil || stateValue.IsNull() {
		t.Fatalf("projection failure omitted recovery state: value=%#v err=%v", stateValue, err)
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	for name, value := range attributes {
		if !value.IsKnown() {
			t.Fatalf("projection failure left recovery attribute %q unknown", name)
		}
	}
	var recoveredID, recoveredJSON string
	if err := attributes["id"].As(&recoveredID); err != nil || recoveredID == "" {
		t.Fatalf("recovery id: %q %v", recoveredID, err)
	}
	if err := attributes["additional_model_info_json"].As(&recoveredJSON); err != nil || recoveredJSON != values["additional_model_info_json"] {
		t.Fatalf("recovery JSON: %q %v", recoveredJSON, err)
	}
	provenanceRaw := protocolPrivateValue(t, applied.Private, modelAdditionalModelInfoJSONProvenancePrivateKey)
	provenance, err := decodeModelAdditionalModelInfoJSONProvenance(ctx, provenanceRaw, types.StringValue(recoveredJSON))
	if err != nil || !provenance.Configured || len(provenance.TerraformOwned) == 0 {
		t.Fatalf("recovery provenance: %#v %v", provenance, err)
	}
}

func TestModelAdditionalModelInfoJSONUpdateFailuresRetainPriorStateAndPrivate(t *testing.T) {
	ctx := context.Background()
	var mode atomic.Int32
	var patches atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/model/new":
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodGet && request.URL.Path == "/model/info":
			identity := request.URL.Query().Get("litellm_model_id")
			if mode.Load() == 1 {
				_, _ = fmt.Fprint(writer, `{"data":[{"model_info":{"nested":{"api_only":true}}}]}`)
				return
			}
			_, _ = fmt.Fprintf(writer, `{"data":[{"model_name":"model-json","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini"},"model_info":{"id":%q,"base_model":"gpt-4o-mini","owned":{"native":true}}}]}`, identity)
		case request.Method == http.MethodPatch:
			patches.Add(1)
			http.Error(writer, "unavailable", http.StatusInternalServerError)
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
		"additional_model_info_json": `{"owned":{"native":true}}`,
	}
	baseConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, baseValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	createPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: baseConfig, PriorState: nullState, ProposedNewState: modelAdditionalModelInfoJSONCreateProposed(t, schema, baseValues)})
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

	mode.Store(1)
	hydrationPlan := planUpdate()
	hydrationFailure, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: updateConfig, PriorState: created.NewState, PlannedState: hydrationPlan.PlannedState, PlannedPrivate: hydrationPlan.PlannedPrivate})
	if err != nil || patches.Load() != 0 {
		t.Fatalf("hydration failure: err=%v patches=%d", err, patches.Load())
	}
	assertRetained("hydration failure", hydrationFailure, hydrationPlan)

	mode.Store(2)
	mutationPlan := planUpdate()
	mutationFailure, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: updateConfig, PriorState: created.NewState, PlannedState: mutationPlan.PlannedState, PlannedPrivate: mutationPlan.PlannedPrivate})
	if err != nil || patches.Load() != 1 {
		t.Fatalf("mutation failure: err=%v patches=%d", err, patches.Load())
	}
	assertRetained("mutation failure", mutationFailure, mutationPlan)
}

func TestModelAdditionalModelInfoJSONImportAndReplacementProtocol(t *testing.T) {
	ctx := context.Background()
	var reads, mutations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/model/info":
			reads.Add(1)
			_, _ = fmt.Fprint(writer, `{"data":[{"model_name":"imported-model","litellm_params":{"custom_llm_provider":"openai","model":"openai/gpt-4o-mini"},"model_info":{"id":"model-json-import","base_model":"gpt-4o-mini","tier":"free","remote_custom":{"native":true}}}]}`)
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
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "model-json-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schema, read.NewState)
	if value := attributes["additional_model_info_json"]; !value.IsNull() {
		t.Fatalf("import adopted remote custom model_info: %s", value)
	}

	baseConfig := map[string]interface{}{
		"model_name": "imported-model", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini",
	}
	omittedConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, baseConfig))
	omitted, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: omittedConfig, PriorState: read.NewState, ProposedNewState: read.NewState, PriorPrivate: read.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(omitted.Diagnostics) || len(omitted.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted) != organizationProjectProtocolActionNoOp {
		t.Fatalf("omitted import plan: err=%v diagnostics=%v replace=%v action=%s", err, omitted.Diagnostics, omitted.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted))
	}
	unknownValues := map[string]interface{}{
		"model_name": "imported-model", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini",
		"additional_model_info_json": tftypes.UnknownValue,
	}
	unknownConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, unknownValues))
	unknownImportedProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"additional_model_info_json": tftypes.UnknownValue})
	unknownImported, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: unknownConfig, PriorState: read.NewState, ProposedNewState: unknownImportedProposed, PriorPrivate: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(unknownImported.Diagnostics) || len(unknownImported.RequiresReplace) == 0 {
		t.Fatalf("unknown import takeover plan: err=%v diagnostics=%v replace=%v", err, unknownImported.Diagnostics, unknownImported.RequiresReplace)
	}

	configuredValues := map[string]interface{}{
		"model_name": "imported-model", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini",
		"additional_model_info_json": `{ "remote_custom":{"native":true} }`,
	}
	configured := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configuredValues))
	configuredProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{
		"additional_model_info_json": configuredValues["additional_model_info_json"],
	})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: configured, PriorState: read.NewState, ProposedNewState: configuredProposed, PriorPrivate: read.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || len(planned.RequiresReplace) == 0 || organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned) != organizationProjectProtocolActionReplace {
		t.Fatalf("takeover plan: err=%v diagnostics=%v replace=%v action=%s", err, planned.Diagnostics, planned.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned))
	}

	configuredObject, configuredProvenance, err := modelAdditionalModelInfoJSONConfiguration(ctx, types.StringValue(configuredValues["additional_model_info_json"].(string)), types.MapNull(types.StringType))
	if err != nil || configuredObject == nil {
		t.Fatal(err)
	}
	semanticRaw, err := encodeModelAdditionalModelInfoJSONProvenance(ctx, configuredProvenance)
	if err != nil {
		t.Fatal(err)
	}
	privateValues := map[string][]byte{}
	if err := json.Unmarshal(read.Private, &privateValues); err != nil {
		t.Fatal(err)
	}
	privateValues[modelAdditionalModelInfoJSONProvenancePrivateKey] = semanticRaw
	ownedPrivate, err := json.Marshal(privateValues)
	if err != nil {
		t.Fatal(err)
	}
	ownedState := configuredProposed
	steady, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: configured, PriorState: ownedState, ProposedNewState: ownedState, PriorPrivate: ownedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) || len(steady.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, ownedState, steady) != organizationProjectProtocolActionNoOp {
		t.Fatalf("steady plan: err=%v diagnostics=%v replace=%v action=%s", err, steady.Diagnostics, steady.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, ownedState, steady))
	}
	unknownOwnedProposed := organizationProjectProtocolReplace(t, schema, ownedState, map[string]interface{}{"additional_model_info_json": tftypes.UnknownValue})
	unknownOwned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: unknownConfig, PriorState: ownedState, ProposedNewState: unknownOwnedProposed, PriorPrivate: ownedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(unknownOwned.Diagnostics) || len(unknownOwned.RequiresReplace) == 0 {
		t.Fatalf("unknown owned plan: err=%v diagnostics=%v replace=%v", err, unknownOwned.Diagnostics, unknownOwned.RequiresReplace)
	}

	changedValues := map[string]interface{}{
		"model_name": "imported-model", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini",
		"additional_model_info_json": `{"remote_custom":{"native":false}}`,
	}
	changedConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, changedValues))
	changedProposed := organizationProjectProtocolReplace(t, schema, ownedState, map[string]interface{}{"additional_model_info_json": changedValues["additional_model_info_json"]})
	changed, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: changedConfig, PriorState: ownedState, ProposedNewState: changedProposed, PriorPrivate: ownedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(changed.Diagnostics) || len(changed.RequiresReplace) == 0 {
		t.Fatalf("semantic change plan: err=%v diagnostics=%v replace=%v", err, changed.Diagnostics, changed.RequiresReplace)
	}
	removed, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: omittedConfig, PriorState: ownedState, ProposedNewState: ownedState, PriorPrivate: ownedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(removed.Diagnostics) || len(removed.RequiresReplace) == 0 {
		t.Fatalf("semantic removal plan: err=%v diagnostics=%v replace=%v", err, removed.Diagnostics, removed.RequiresReplace)
	}

	overlapMap := map[string]tftypes.Value{"private-sensitive-key": tftypes.NewValue(tftypes.String, "private-value")}
	overlapValues := map[string]interface{}{
		"model_name": "imported-model", "custom_llm_provider": "openai", "base_model": "gpt-4o-mini",
		"additional_model_info":      overlapMap,
		"additional_model_info_json": `{"private-sensitive-key":true}`,
	}
	overlapConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, overlapValues))
	overlapProposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{
		"additional_model_info":      overlapValues["additional_model_info"],
		"additional_model_info_json": overlapValues["additional_model_info_json"],
	})
	overlap, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: overlapConfig, PriorState: read.NewState, ProposedNewState: overlapProposed, PriorPrivate: read.Private,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(overlap.Diagnostics) {
		t.Fatalf("overlap plan: err=%v diagnostics=%v", err, overlap.Diagnostics)
	}
	for _, diagnostic := range overlap.Diagnostics {
		text := diagnostic.Summary + diagnostic.Detail
		if strings.Contains(text, "private-sensitive-key") || strings.Contains(text, "private-value") {
			t.Fatalf("diagnostic exposed sensitive content: %#v", diagnostic)
		}
	}
	if mutations.Load() != 0 || reads.Load() != 1 {
		t.Fatalf("planning sent requests: reads=%d mutations=%d", reads.Load(), mutations.Load())
	}

}
