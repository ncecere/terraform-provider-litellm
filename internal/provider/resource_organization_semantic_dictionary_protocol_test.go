package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func organizationSemanticConfig(t *testing.T, schema *tfprotov6.Schema, values map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	return accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
}

func organizationSemanticCreateProposed(t *testing.T, schema *tfprotov6.Schema, values map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	return keySemanticCreateProposed(t, schema, values)
}

func organizationSemanticRemote(id, alias string, metadata interface{}) map[string]interface{} {
	return map[string]interface{}{
		"organization_id": id, "organization_alias": alias, "models": []interface{}{},
		"metadata": metadata, "created_at": "2026-01-01T00:00:00Z",
		"litellm_budget_table": map[string]interface{}{},
	}
}

func TestOrganizationSemanticSchemaAndDirectUpgradeProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_organization"]
	if schema.Version != 1 {
		t.Fatalf("schema version=%d want=1", schema.Version)
	}
	raw, _ := json.Marshal(map[string]interface{}{
		"id": "org-upgrade", "organization_id": "org-upgrade", "organization_alias": "preserved",
		"metadata": map[string]interface{}{"legacy": "preserved"},
	})
	upgraded, err := protocolServer.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{
		TypeName: "litellm_organization", Version: 0, RawState: &tfprotov6.RawState{JSON: raw},
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(upgraded.Diagnostics) || upgraded.UpgradedState == nil {
		t.Fatalf("upgrade: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(upgraded.Diagnostics))
	}
	attributes := protocolAttributeMap(t, schema, upgraded.UpgradedState)
	if !attributes["metadata_json"].IsNull() {
		t.Fatalf("upgrade adopted metadata_json: %s", attributes["metadata_json"])
	}
	var alias string
	if err := attributes["organization_alias"].As(&alias); err != nil || alias != "preserved" {
		t.Fatalf("alias=%q err=%v", alias, err)
	}
}

func TestOrganizationSemanticValidationAndCallerIdentityProtocol(t *testing.T) {
	ctx := context.Background()
	var mutations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutations.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_organization"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))

	for name, values := range map[string]map[string]interface{}{
		"duplicate":        {"organization_id": "org-invalid-duplicate", "organization_alias": "invalid", "metadata_json": `{"a":1,"a":2}`},
		"nonobject":        {"organization_id": "org-invalid-root", "organization_alias": "invalid", "metadata_json": `[1]`},
		"lossy":            {"organization_id": "org-invalid-lossy", "organization_alias": "invalid", "metadata_json": `{"a":0.10000000000000001}`},
		"missing identity": {"organization_alias": "missing", "metadata_json": `{"owned":true}`},
		"legacy overlap":   {"organization_id": "org-overlap", "organization_alias": "overlap", "metadata": map[string]tftypes.Value{"same": tftypes.NewValue(tftypes.String, "legacy")}, "metadata_json": `{"same":true}`},
		"reserved rpm":     {"organization_id": "org-rpm", "organization_alias": "rpm", "metadata_json": `{"model_rpm_limit":{}}`},
		"reserved tpm":     {"organization_id": "org-tpm", "organization_alias": "tpm", "metadata_json": `{"model_tpm_limit":{}}`},
	} {
		t.Run(name, func(t *testing.T) {
			config := organizationSemanticConfig(t, schema, values)
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: config, PriorState: nullState, ProposedNewState: organizationSemanticCreateProposed(t, schema, values)})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("pre-apply plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_organization", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("unsafe create accepted: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
			}
		})
	}
	if mutations.Load() != 0 {
		t.Fatalf("validation dispatched %d mutations", mutations.Load())
	}
}

func TestOrganizationSemanticCreateUpdateAndSiblingPreservationProtocol(t *testing.T) {
	ctx := context.Background()
	const id = "org-semantic"
	var posts, patches, reads atomic.Int64
	var serveMalformed, malformAfterPatch atomic.Bool
	alias := "semantic"
	remoteMetadata := map[string]interface{}{}
	var createBody, updateBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/organization/new":
			posts.Add(1)
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&createBody); err != nil {
				t.Errorf("decode create: %v", err)
			}
			remoteMetadata, _ = createBody["metadata"].(map[string]interface{})
			remoteMetadata["api"] = map[string]interface{}{"preserved": true}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"organization_id": id})
		case request.Method == http.MethodPatch && request.URL.Path == "/v2/organization/"+id:
			patches.Add(1)
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&updateBody); err != nil {
				t.Errorf("decode update: %v", err)
			}
			if metadata, ok := updateBody["metadata"].(map[string]interface{}); ok {
				remoteMetadata = metadata
			}
			if value, ok := updateBody["organization_alias"].(string); ok {
				alias = value
			}
			if malformAfterPatch.Load() {
				serveMalformed.Store(true)
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"organization_id": id})
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			reads.Add(1)
			var metadata interface{} = remoteMetadata
			if serveMalformed.Load() {
				metadata = []interface{}{"malformed"}
			}
			_ = json.NewEncoder(writer).Encode(organizationSemanticRemote(id, alias, metadata))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_organization"
	schema := schemas.ResourceSchemas[typeName]
	semantic := `{"integer":9007199254740993123456789,"native":true,"string":"true","nil":null,"list":[null,false,"1",1],"empty":{},"owned":{"keep":1,"remove":2}}`
	createValues := map[string]interface{}{
		"organization_id": id, "organization_alias": alias,
		"metadata":        map[string]tftypes.Value{"legacy": tftypes.NewValue(tftypes.String, `{"from":"legacy"}`)},
		"metadata_json":   semantic,
		"model_rpm_limit": map[string]tftypes.Value{"m": tftypes.NewValue(tftypes.Number, int64(7))},
	}
	config := organizationSemanticConfig(t, schema, createValues)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: organizationSemanticCreateProposed(t, schema, createValues),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("create: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(created.Diagnostics))
	}
	if posts.Load() != 1 || reads.Load() != 1 || patches.Load() != 0 {
		t.Fatalf("create calls posts=%d reads=%d patches=%d", posts.Load(), reads.Load(), patches.Load())
	}
	createdMetadata := createBody["metadata"].(map[string]interface{})
	if createdMetadata["integer"] != json.Number("9007199254740993123456789") || createdMetadata["native"] != true || createdMetadata["string"] != "true" || createdMetadata["nil"] != nil {
		t.Fatalf("create metadata lost exact identity: %#v", createdMetadata)
	}
	if createdMetadata["model_rpm_limit"].(map[string]interface{})["m"] != json.Number("7") {
		t.Fatalf("dedicated RPM missing: %#v", createdMetadata)
	}
	attributes := protocolAttributeMap(t, schema, created.NewState)
	var got string
	if err := attributes["metadata_json"].As(&got); err != nil || got != semantic {
		t.Fatalf("metadata_json=%q err=%v", got, err)
	}

	updatedSemantic := `{"integer":9007199254740993123456789,"native":true,"string":"true","nil":null,"list":[null,false,"1",1],"empty":{},"owned":{"keep":3}}`
	updateValues := map[string]interface{}{
		"organization_id": id, "organization_alias": alias,
		"metadata":        map[string]tftypes.Value{"legacy": tftypes.NewValue(tftypes.String, `{"from":"updated"}`)},
		"metadata_json":   updatedSemantic,
		"model_rpm_limit": map[string]tftypes.Value{"m": tftypes.NewValue(tftypes.Number, int64(9))},
		"model_tpm_limit": map[string]tftypes.Value{"m": tftypes.NewValue(tftypes.Number, int64(11))},
	}
	updateConfig := organizationSemanticConfig(t, schema, updateValues)
	proposed := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{
		"metadata": updateValues["metadata"], "metadata_json": updatedSemantic,
		"model_rpm_limit": updateValues["model_rpm_limit"], "model_tpm_limit": updateValues["model_tpm_limit"],
	})
	updatePlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: updateConfig, PriorState: created.NewState, ProposedNewState: proposed, PriorPrivate: created.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updatePlan.Diagnostics) {
		t.Fatalf("update plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(updatePlan.Diagnostics))
	}
	readsBefore := reads.Load()
	updated, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: updateConfig, PriorState: created.NewState, PlannedState: updatePlan.PlannedState, PlannedPrivate: updatePlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updated.Diagnostics) {
		t.Fatalf("update: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(updated.Diagnostics))
	}
	if patches.Load() != 1 || reads.Load() != readsBefore+2 {
		t.Fatalf("metadata update calls patches=%d reads delta=%d, want 1/2", patches.Load(), reads.Load()-readsBefore)
	}
	replacement := updateBody["metadata"].(map[string]interface{})
	if replacement["api"].(map[string]interface{})["preserved"] != true {
		t.Fatalf("API sibling not preserved: %#v", replacement)
	}
	owned := replacement["owned"].(map[string]interface{})
	if owned["keep"] != json.Number("3") {
		t.Fatalf("owned=%#v", owned)
	}
	if _, exists := owned["remove"]; exists {
		t.Fatalf("nested removal retained: %#v", owned)
	}
	if replacement["model_rpm_limit"].(map[string]interface{})["m"] != json.Number("9") || replacement["model_tpm_limit"].(map[string]interface{})["m"] != json.Number("11") {
		t.Fatalf("dedicated rates do not coexist: %#v", replacement)
	}

	// Formatting-only configuration keeps the state spelling and schedules no mutation.
	formattedValues := map[string]interface{}{
		"organization_id": id, "organization_alias": alias, "metadata": updateValues["metadata"],
		"metadata_json":   "{\n \"owned\": {\"keep\": 3}, \"empty\": {}, \"list\": [null,false,\"1\",1], \"nil\": null, \"string\": \"true\", \"native\": true, \"integer\": 9007199254740993123456789\n}",
		"model_rpm_limit": updateValues["model_rpm_limit"], "model_tpm_limit": updateValues["model_tpm_limit"],
	}
	formattedConfig := organizationSemanticConfig(t, schema, formattedValues)
	formattedPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: formattedConfig, PriorState: updated.NewState, ProposedNewState: updated.NewState, PriorPrivate: updated.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(formattedPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, updated.NewState, formattedPlan) != organizationProjectProtocolActionNoOp {
		t.Fatalf("format no-op: err=%v diagnostics=%s action=%s", err, agentProtocolDiagnosticsText(formattedPlan.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, updated.NewState, formattedPlan))
	}

	// A committed whole-attribute removal with malformed first readback retains
	// prior public state plus value-free pending provenance. A later exact read
	// confirms removal and clears recovery without losing the API sibling.
	removalValues := map[string]interface{}{
		"organization_id": id, "organization_alias": alias,
		"metadata": updateValues["metadata"], "model_rpm_limit": updateValues["model_rpm_limit"], "model_tpm_limit": updateValues["model_tpm_limit"],
	}
	removalConfig := organizationSemanticConfig(t, schema, removalValues)
	removalProposed := organizationProjectProtocolReplace(t, schema, updated.NewState, map[string]interface{}{"metadata_json": nil})
	removalPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: removalConfig, PriorState: updated.NewState, ProposedNewState: removalProposed, PriorPrivate: updated.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(removalPlan.Diagnostics) {
		t.Fatalf("root removal plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(removalPlan.Diagnostics))
	}
	malformAfterPatch.Store(true)
	removed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: removalConfig, PriorState: updated.NewState, PlannedState: removalPlan.PlannedState, PlannedPrivate: removalPlan.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(removed.Diagnostics) || !protocolPrivateHasKey(t, removed.Private, organizationPendingUpdatePrivateKey) {
		t.Fatalf("uncertain root removal: err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(removed.Diagnostics), removed.Private)
	}
	priorValue, _ := updated.NewState.Unmarshal(schema.ValueType())
	removedValue, _ := removed.NewState.Unmarshal(schema.ValueType())
	if !priorValue.Equal(removedValue) {
		t.Fatal("uncertain root removal changed prior public state")
	}
	serveMalformed.Store(false)
	malformAfterPatch.Store(false)
	reconciled, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: removed.NewState, Private: removed.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(reconciled.Diagnostics) || protocolPrivateHasKey(t, reconciled.Private, organizationPendingUpdatePrivateKey) {
		t.Fatalf("root removal reconcile: err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(reconciled.Diagnostics), reconciled.Private)
	}
	reconciledAttributes := protocolAttributeMap(t, schema, reconciled.NewState)
	if !reconciledAttributes["metadata_json"].IsNull() || remoteMetadata["api"].(map[string]interface{})["preserved"] != true {
		t.Fatalf("root removal did not converge safely: json=%s remote=%#v", reconciledAttributes["metadata_json"], remoteMetadata)
	}
}

func TestOrganizationSemanticExplicitEmptyObjectAndRootRemovalProtocol(t *testing.T) {
	ctx := context.Background()
	const id = "org-empty"
	remote := map[string]interface{}{"api": "preserved", "root": map[string]interface{}{"nested": true}}
	var patches atomic.Int64
	var lastPatch map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			_ = json.NewEncoder(writer).Encode(organizationSemanticRemote(id, "empty", remote))
		case request.Method == http.MethodPost && request.URL.Path == "/organization/new":
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			remote, _ = body["metadata"].(map[string]interface{})
			remote["api"] = "preserved"
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"organization_id": id})
		case request.Method == http.MethodPatch && request.URL.Path == "/v2/organization/"+id:
			patches.Add(1)
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			_ = decoder.Decode(&lastPatch)
			remote = lastPatch["metadata"].(map[string]interface{})
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"organization_id": id})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_organization"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	createValues := map[string]interface{}{"organization_id": id, "organization_alias": "empty", "metadata_json": `{"root":{"nested":true}}`}
	createConfig := organizationSemanticConfig(t, schema, createValues)
	plan, _ := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: createConfig, PriorState: nullState, ProposedNewState: organizationSemanticCreateProposed(t, schema, createValues)})
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_organization", Config: createConfig, PriorState: nullState, PlannedState: plan.PlannedState, PlannedPrivate: plan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("create: %v %s", err, agentProtocolDiagnosticsText(created.Diagnostics))
	}

	emptyValues := map[string]interface{}{"organization_id": id, "organization_alias": "empty", "metadata_json": `{}`}
	emptyConfig := organizationSemanticConfig(t, schema, emptyValues)
	emptyProposed := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{"metadata_json": `{}`})
	emptyPlan, _ := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: emptyConfig, PriorState: created.NewState, ProposedNewState: emptyProposed, PriorPrivate: created.Private})
	emptyApplied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_organization", Config: emptyConfig, PriorState: created.NewState, PlannedState: emptyPlan.PlannedState, PlannedPrivate: emptyPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(emptyApplied.Diagnostics) {
		t.Fatalf("empty apply: %v %s", err, agentProtocolDiagnosticsText(emptyApplied.Diagnostics))
	}
	metadata := lastPatch["metadata"].(map[string]interface{})
	if metadata["api"] != "preserved" {
		t.Fatalf("API root not preserved: %#v", metadata)
	}
	if _, present := metadata["root"]; present {
		t.Fatalf("owned root not removed: %#v", metadata)
	}
	attributes := protocolAttributeMap(t, schema, emptyApplied.NewState)
	var got string
	if err := attributes["metadata_json"].As(&got); err != nil || got != `{}` {
		t.Fatalf("explicit empty object=%q err=%v", got, err)
	}
}

func TestOrganizationSemanticImportNonAdoptionAndTakeoverProtocol(t *testing.T) {
	ctx := context.Background()
	const id = "org-import-semantic"
	remote := map[string]interface{}{"native": true, "api": map[string]interface{}{"preserved": true}}
	var patches atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			_ = json.NewEncoder(writer).Encode(organizationSemanticRemote(id, "imported", remote))
		case request.Method == http.MethodPatch && request.URL.Path == "/v2/organization/"+id:
			patches.Add(1)
			var body map[string]interface{}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			_ = decoder.Decode(&body)
			remote = body["metadata"].(map[string]interface{})
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"organization_id": id})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_organization"]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_organization", ID: id})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: %v %s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	first, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(first.Diagnostics) {
		t.Fatalf("first read: %v %s", err, agentProtocolDiagnosticsText(first.Diagnostics))
	}
	second, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: first.NewState, Private: first.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(second.Diagnostics) {
		t.Fatalf("second read: %v %s", err, agentProtocolDiagnosticsText(second.Diagnostics))
	}
	for _, state := range []*tfprotov6.DynamicValue{first.NewState, second.NewState} {
		attributes := protocolAttributeMap(t, schema, state)
		if !attributes["metadata_json"].IsNull() || !attributes["metadata"].IsNull() {
			t.Fatalf("import adopted heterogeneous API metadata: json=%s legacy=%s", attributes["metadata_json"], attributes["metadata"])
		}
	}

	takeoverValues := map[string]interface{}{"organization_id": id, "organization_alias": "imported", "metadata_json": `{"native":false}`}
	takeoverConfig := organizationSemanticConfig(t, schema, takeoverValues)
	takeoverProposed := organizationProjectProtocolReplace(t, schema, second.NewState, map[string]interface{}{"metadata_json": `{"native":false}`})
	takeoverPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: takeoverConfig, PriorState: second.NewState, ProposedNewState: takeoverProposed, PriorPrivate: second.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(takeoverPlan.Diagnostics) {
		t.Fatalf("takeover plan: %v %s", err, agentProtocolDiagnosticsText(takeoverPlan.Diagnostics))
	}
	taken, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_organization", Config: takeoverConfig, PriorState: second.NewState, PlannedState: takeoverPlan.PlannedState, PlannedPrivate: takeoverPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(taken.Diagnostics) || patches.Load() != 1 {
		t.Fatalf("takeover: %v %s patches=%d", err, agentProtocolDiagnosticsText(taken.Diagnostics), patches.Load())
	}
	if remote["native"] != false || remote["api"].(map[string]interface{})["preserved"] != true {
		t.Fatalf("takeover replacement=%#v", remote)
	}
}

func TestOrganizationSemanticDispatchedCreateTransportLossRecoveryProtocol(t *testing.T) {
	ctx := context.Background()
	const id = "org-dispatched-recovery"
	var posts atomic.Int64
	remote := map[string]interface{}{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/organization/new":
			posts.Add(1)
			var body map[string]interface{}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&body); err != nil {
				t.Errorf("decode dispatched create: %v", err)
			}
			remote, _ = body["metadata"].(map[string]interface{})
			hijacker, ok := writer.(http.Hijacker)
			if !ok {
				t.Error("test response writer cannot simulate transport loss")
				return
			}
			connection, _, err := hijacker.Hijack()
			if err != nil {
				t.Errorf("hijack dispatched create: %v", err)
				return
			}
			_ = connection.Close()
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(organizationSemanticRemote(id, "dispatched", remote))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_organization"]
	values := map[string]interface{}{"organization_id": id, "organization_alias": "dispatched", "metadata_json": `{"owned":true}`}
	config := organizationSemanticConfig(t, schema, values)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	plan, _ := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: config, PriorState: nullState, ProposedNewState: organizationSemanticCreateProposed(t, schema, values)})
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_organization", Config: config, PriorState: nullState, PlannedState: plan.PlannedState, PlannedPrivate: plan.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) || posts.Load() != 1 || !protocolPrivateHasKey(t, created.Private, organizationAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("dispatched create: err=%v diagnostics=%s posts=%d private=%s", err, agentProtocolDiagnosticsText(created.Diagnostics), posts.Load(), created.Private)
	}
	if diagnostic := agentProtocolDiagnosticsText(created.Diagnostics); strings.Contains(strings.ToLower(diagnostic), "accepted") || !strings.Contains(diagnostic, "prevented the provider from determining whether it committed") {
		t.Fatalf("dispatched uncertainty diagnostic=%q", diagnostic)
	}
	attributes := protocolAttributeMap(t, schema, created.NewState)
	var gotID string
	if err := attributes["organization_id"].As(&gotID); err != nil || gotID != id || !attributes["metadata_json"].IsNull() {
		t.Fatalf("dispatched recovery identity/json: id=%q err=%v json=%s", gotID, err, attributes["metadata_json"])
	}

	reconciled, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: created.NewState, Private: created.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(reconciled.Diagnostics) || protocolPrivateHasKey(t, reconciled.Private, organizationAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("dispatched recovery read: err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(reconciled.Diagnostics), reconciled.Private)
	}
	reconciledAttributes := protocolAttributeMap(t, schema, reconciled.NewState)
	if !reconciledAttributes["metadata_json"].IsNull() || !reconciledAttributes["metadata"].IsNull() {
		t.Fatalf("dispatched recovery adopted API metadata: json=%s legacy=%s", reconciledAttributes["metadata_json"], reconciledAttributes["metadata"])
	}
}

func TestOrganizationSemanticAcceptedCreateRecoveryAndBlockingProtocol(t *testing.T) {
	ctx := context.Background()
	const id = "org-recovery-private-id"
	const secretKey = "private_metadata_key"
	var posts, patches, deletes atomic.Int64
	remote := map[string]interface{}{secretKey: true}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/organization/new":
			posts.Add(1)
			_, _ = writer.Write([]byte(`{"accepted":`))
		case request.Method == http.MethodPatch:
			patches.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"organization_id": id})
		case request.Method == http.MethodDelete:
			deletes.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{})
		case request.Method == http.MethodGet && request.URL.Path == "/organization/info":
			_ = json.NewEncoder(writer).Encode(organizationSemanticRemote(id, "recovery", remote))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_organization"]
	values := map[string]interface{}{"organization_id": id, "organization_alias": "recovery", "metadata_json": `{"` + secretKey + `":true}`}
	config := organizationSemanticConfig(t, schema, values)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	plan, _ := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: config, PriorState: nullState, ProposedNewState: organizationSemanticCreateProposed(t, schema, values)})
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_organization", Config: config, PriorState: nullState, PlannedState: plan.PlannedState, PlannedPrivate: plan.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) || posts.Load() != 1 {
		t.Fatalf("accepted create: err=%v diagnostics=%s posts=%d", err, agentProtocolDiagnosticsText(created.Diagnostics), posts.Load())
	}
	diagnostic := agentProtocolDiagnosticsText(created.Diagnostics)
	for _, protected := range []string{id, secretKey, "true", "/organization"} {
		if strings.Contains(diagnostic, protected) {
			t.Fatalf("diagnostic exposed %q: %s", protected, diagnostic)
		}
	}
	if bytes.Contains(created.Private, []byte(secretKey)) || !protocolPrivateHasKey(t, created.Private, organizationAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("unsafe/missing recovery private: %s", created.Private)
	}
	attributes := protocolAttributeMap(t, schema, created.NewState)
	var gotID string
	if err := attributes["organization_id"].As(&gotID); err != nil || gotID != id || !attributes["metadata_json"].IsNull() {
		t.Fatalf("recovery identity/json: id=%q err=%v json=%s", gotID, err, attributes["metadata_json"])
	}

	updateState := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{"organization_alias": "changed"})
	blockedUpdate, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_organization", Config: config, PriorState: created.NewState, PlannedState: updateState, PlannedPrivate: created.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(blockedUpdate.Diagnostics) || patches.Load() != 0 {
		t.Fatalf("blocked update: err=%v diagnostics=%s patches=%d", err, agentProtocolDiagnosticsText(blockedUpdate.Diagnostics), patches.Load())
	}
	blockedDelete, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_organization", PriorState: created.NewState, PlannedState: nullState, PlannedPrivate: created.Private})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(blockedDelete.Diagnostics) || deletes.Load() != 0 {
		t.Fatalf("blocked delete: err=%v diagnostics=%s deletes=%d", err, agentProtocolDiagnosticsText(blockedDelete.Diagnostics), deletes.Load())
	}

	// Exact identity-bound refresh clears recovery without adopting API metadata.
	reconciled, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: created.NewState, Private: created.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(reconciled.Diagnostics) || protocolPrivateHasKey(t, reconciled.Private, organizationAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("recovery read: err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(reconciled.Diagnostics), reconciled.Private)
	}
	reconciledAttributes := protocolAttributeMap(t, schema, reconciled.NewState)
	if !reconciledAttributes["metadata_json"].IsNull() || !reconciledAttributes["metadata"].IsNull() {
		t.Fatalf("recovery adopted API metadata: json=%s legacy=%s", reconciledAttributes["metadata_json"], reconciledAttributes["metadata"])
	}
}

func TestOrganizationSemanticPendingExpansionContractionProtocol(t *testing.T) {
	ctx := context.Background()
	const id = "org-pending-shape"
	var observed atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(organizationSemanticRemote(id, "pending", observed.Load().(map[string]interface{})))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_organization"]

	cases := []struct {
		name      string
		prior     string
		next      string
		committed map[string]interface{}
		not       map[string]interface{}
		partial   map[string]interface{}
	}{
		{
			name: "expansion", prior: `{"shape":1}`, next: `{"shape":{"a":1,"b":2}}`,
			committed: map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1"), "b": json.Number("2")}},
			not:       map[string]interface{}{"shape": json.Number("1")},
			partial:   map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1")}},
		},
		{
			name: "contraction", prior: `{"shape":{"a":1,"b":2}}`, next: `{"shape":1}`,
			committed: map[string]interface{}{"shape": json.Number("1")},
			not:       map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1"), "b": json.Number("2")}},
			partial:   map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1")}},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			prior, _ := prepareOrganizationSemanticDictionary(ctx, types.StringValue(test.prior), types.MapNull(types.StringType))
			next, _ := prepareOrganizationSemanticDictionary(ctx, types.StringValue(test.next), types.MapNull(types.StringType))
			transition, err := next.updateOwnership(ctx, prior.provenance)
			if err != nil {
				t.Fatal(err)
			}
			pendingRaw, err := encodeKeySemanticPendingTransition(ctx, pendingOrganizationSemanticTransition(transition))
			if err != nil {
				t.Fatal(err)
			}
			priorRaw, _ := encodeOrganizationSemanticProvenance(ctx, prior.provenance)
			private, _ := json.Marshal(map[string][]byte{
				organizationMetadataJSONProvenancePrivateKey: priorRaw,
				organizationPendingUpdatePrivateKey:          pendingRaw,
			})
			state := organizationSemanticConfig(t, schema, map[string]interface{}{
				"id": id, "organization_id": id, "organization_alias": "pending", "metadata_json": test.prior,
			})

			observed.Store(test.committed)
			committed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: state, Private: private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(committed.Diagnostics) || protocolPrivateHasKey(t, committed.Private, organizationPendingUpdatePrivateKey) {
				t.Fatalf("committed read: err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(committed.Diagnostics), committed.Private)
			}
			attributes := protocolAttributeMap(t, schema, committed.NewState)
			var got string
			if err := attributes["metadata_json"].As(&got); err != nil {
				t.Fatal(err)
			}
			gotObject, _ := parseSemanticDictionary(ctx, got)
			wantObject, _ := parseSemanticDictionary(ctx, test.next)
			equal, _ := semanticDictionaryValuesEqual(ctx, gotObject, wantObject)
			if !equal {
				t.Fatalf("committed JSON=%s want semantic %s", got, test.next)
			}

			observed.Store(test.not)
			notCommitted, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: state, Private: private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(notCommitted.Diagnostics) || protocolPrivateHasKey(t, notCommitted.Private, organizationPendingUpdatePrivateKey) {
				t.Fatalf("not-committed read: err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(notCommitted.Diagnostics), notCommitted.Private)
			}
			attributes = protocolAttributeMap(t, schema, notCommitted.NewState)
			if err := attributes["metadata_json"].As(&got); err != nil || got != test.prior {
				t.Fatalf("not-committed JSON=%q want=%q err=%v", got, test.prior, err)
			}

			observed.Store(test.partial)
			partial, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: state, Private: private})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(partial.Diagnostics) || !protocolPrivateHasKey(t, partial.Private, organizationPendingUpdatePrivateKey) {
				t.Fatalf("partial read: err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(partial.Diagnostics), partial.Private)
			}
		})
	}
}

func TestOrganizationSemanticMalformedIdentityRootAndPrivatePrivacyProtocol(t *testing.T) {
	ctx := context.Background()
	const id = "org-sensitive-identity"
	const pathName = "sensitive_path_name"
	mode := atomic.Int64{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch mode.Load() {
		case 0:
			_ = json.NewEncoder(writer).Encode(organizationSemanticRemote("wrong-identity", "private", map[string]interface{}{pathName: true}))
		case 1:
			_ = json.NewEncoder(writer).Encode(organizationSemanticRemote(id, "private", []interface{}{pathName}))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_organization"]
	stateValues := map[string]interface{}{"id": id, "organization_id": id, "organization_alias": "private", "metadata_json": `{"` + pathName + `":true}`}
	state := organizationSemanticConfig(t, schema, stateValues)
	prepared, _ := prepareOrganizationSemanticDictionary(ctx, types.StringValue(stateValues["metadata_json"].(string)), types.MapNull(types.StringType))
	privateValue, _ := encodeOrganizationSemanticProvenance(ctx, prepared.provenance)
	privateRaw, _ := json.Marshal(map[string][]byte{organizationMetadataJSONProvenancePrivateKey: privateValue})
	for testMode := int64(0); testMode < 2; testMode++ {
		mode.Store(testMode)
		read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: state, Private: privateRaw})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
			t.Fatalf("mode %d accepted malformed response: err=%v diagnostics=%s", testMode, err, agentProtocolDiagnosticsText(read.Diagnostics))
		}
		diagnostic := agentProtocolDiagnosticsText(read.Diagnostics)
		for _, protected := range []string{id, "wrong-identity", pathName, "/organization/info"} {
			if strings.Contains(diagnostic, protected) {
				t.Fatalf("mode %d exposed %q: %s", testMode, protected, diagnostic)
			}
		}
	}
	corruptRaw, _ := json.Marshal(map[string][]byte{organizationMetadataJSONProvenancePrivateKey: []byte(`{"version":1,"initialized":true,"configured":true,"terraform_owned":["/wrong"],"api_owned":[],"pending_terraform_owned":[],"pending_api_owned":[],"pending_removals":[]}`)})
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: state, Private: corruptRaw})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("corrupt private accepted: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	if diagnostic := agentProtocolDiagnosticsText(read.Diagnostics); strings.Contains(diagnostic, pathName) || strings.Contains(diagnostic, id) {
		t.Fatalf("private diagnostic exposed state: %s", diagnostic)
	}
}
