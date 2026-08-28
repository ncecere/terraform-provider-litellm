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

func TestKeySemanticDictionaryDirectUpgradeProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_key"]
	for _, test := range []struct {
		version int64
		id      string
		wantID  string
	}{
		{0, "sk-upgrade-v0", hashKeyForID("sk-upgrade-v0")},
		{1, hashKeyForID("sk-upgrade-v1"), hashKeyForID("sk-upgrade-v1")},
	} {
		raw, _ := json.Marshal(map[string]interface{}{"id": test.id, "key": "sk-preserved", "metadata": map[string]interface{}{"legacy": "preserved"}})
		upgraded, err := protocolServer.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{
			TypeName: "litellm_key", Version: test.version, RawState: &tfprotov6.RawState{JSON: raw},
		})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(upgraded.Diagnostics) || upgraded.UpgradedState == nil {
			t.Fatalf("v%d upgrade: err=%v diagnostics=%s", test.version, err, agentProtocolDiagnosticsText(upgraded.Diagnostics))
		}
		attributes := protocolAttributeMap(t, schema, upgraded.UpgradedState)
		var id string
		if err := attributes["id"].As(&id); err != nil || id != test.wantID {
			t.Fatalf("v%d id=%q err=%v", test.version, id, err)
		}
		for _, name := range []string{"metadata_json", "config_json", "permissions_json"} {
			if !attributes[name].IsNull() {
				t.Fatalf("v%d %s was adopted: %s", test.version, name, attributes[name])
			}
		}
	}
}

func keySemanticCreateProposed(t *testing.T, schema *tfprotov6.Schema, values map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	proposed := make(map[string]interface{}, len(values))
	for name, value := range values {
		proposed[name] = value
	}
	for _, attribute := range schema.Block.Attributes {
		if attribute.Computed {
			if _, present := proposed[attribute.Name]; !present {
				proposed[attribute.Name] = tftypes.UnknownValue
			}
		}
	}
	return accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposed))
}

func TestKeySemanticDictionaryCreateProtocol(t *testing.T) {
	ctx := context.Background()
	var posts, reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/key/generate":
			posts.Add(1)
			var body map[string]interface{}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&body); err != nil {
				t.Errorf("decode generate: %v", err)
			}
			metadata, ok := body["metadata"].(map[string]interface{})
			if !ok || metadata["integer"] != json.Number("9007199254740993") || metadata["native"] != true || metadata["string"] != "true" {
				t.Errorf("generate metadata lost identity: %#v", body["metadata"])
			}
			_, _ = writer.Write([]byte(`{"key":"sk-key-json"}`))
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			reads.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"key": "sk-key-json",
				"info": map[string]interface{}{
					"metadata": map[string]interface{}{"integer": json.Number("9007199254740993"), "native": true, "string": "true", "nested": map[string]interface{}{"null": nil, "list": []interface{}{nil, false, "1", json.Number("1")}, "empty": map[string]interface{}{}}},
					"config":   map[string]interface{}{}, "permissions": map[string]interface{}{},
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_key"
	schema := schemas.ResourceSchemas[typeName]
	values := map[string]interface{}{
		"key":           "sk-key-json",
		"metadata_json": `{"integer":9007199254740993,"native":true,"string":"true","nested":{"null":null,"list":[null,false,"1",1],"empty":{}}}`,
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: keySemanticCreateProposed(t, schema, values),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("apply: err=%v diagnostics=%v", err, applied.Diagnostics)
	}
	if posts.Load() != 1 || reads.Load() != 1 {
		t.Fatalf("posts=%d reads=%d, want one each", posts.Load(), reads.Load())
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	var got string
	if err := attributes["metadata_json"].As(&got); err != nil || got != values["metadata_json"] {
		t.Fatalf("metadata_json = %q err=%v", got, err)
	}
	provenanceRaw := protocolPrivateValue(t, applied.Private, keyMetadataJSONProvenancePrivateKey)
	provenance, err := decodeKeySemanticDictionaryProvenance(ctx, provenanceRaw, types.StringValue(got))
	if err != nil || !provenance.Configured || len(provenance.TerraformOwned) == 0 {
		t.Fatalf("provenance=%#v err=%v", provenance, err)
	}
}

func TestKeySemanticDictionaryUpdateRemovalAndSiblingPreservationProtocol(t *testing.T) {
	ctx := context.Background()
	var posts, reads atomic.Int64
	var corruptAfterPost, serveMalformed atomic.Bool
	remote := map[string]map[string]interface{}{}
	var updateBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/key/generate":
			posts.Add(1)
			var body map[string]interface{}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&body); err != nil {
				t.Errorf("decode generate: %v", err)
			}
			for _, root := range []string{"metadata", "config", "permissions"} {
				object, _ := body[root].(map[string]interface{})
				remote[root] = object
			}
			remote["metadata"]["api"] = map[string]interface{}{"preserved": true}
			remote["config"]["api"] = []interface{}{"preserved"}
			remote["permissions"]["api"] = "preserved"
			_, _ = writer.Write([]byte(`{"key":"sk-key-update"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/key/update":
			posts.Add(1)
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&updateBody); err != nil {
				t.Errorf("decode update: %v", err)
			}
			for _, root := range []string{"metadata", "config", "permissions"} {
				if object, present := updateBody[root].(map[string]interface{}); present {
					remote[root] = object
				}
			}
			if corruptAfterPost.Load() {
				serveMalformed.Store(true)
			}
			_, _ = writer.Write([]byte(`{}`))
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			reads.Add(1)
			var configRoot interface{} = remote["config"]
			if serveMalformed.Load() {
				configRoot = []interface{}{"malformed"}
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"key": "sk-key-update",
				"info": map[string]interface{}{
					"metadata": remote["metadata"], "config": configRoot, "permissions": remote["permissions"],
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_key"
	schema := schemas.ResourceSchemas[typeName]
	createValues := map[string]interface{}{
		"key":              "sk-key-update",
		"metadata":         map[string]tftypes.Value{"legacy": tftypes.NewValue(tftypes.String, "value")},
		"metadata_json":    `{"owned":{"keep":1,"remove":2}}`,
		"config_json":      `{"native":true}`,
		"permissions_json": `{"rule":{"enabled":true}}`,
	}
	createConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, createValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	createdPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: createConfig, PriorState: nullState, ProposedNewState: keySemanticCreateProposed(t, schema, createValues),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(createdPlan.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%v", err, createdPlan.Diagnostics)
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: createConfig, PriorState: nullState, PlannedState: createdPlan.PlannedState, PlannedPrivate: createdPlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("create: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(created.Diagnostics))
	}

	updateValues := map[string]interface{}{
		"key":           "sk-key-update",
		"metadata":      map[string]tftypes.Value{"legacy": tftypes.NewValue(tftypes.String, "value")},
		"metadata_json": `{"owned":{"keep":3}}`,
		"config_json":   `{"native":true}`,
	}
	updateConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, updateValues))
	proposed := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{
		"metadata_json": `{"owned":{"keep":3}}`,
	})
	updatedPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: updateConfig, PriorState: created.NewState, ProposedNewState: proposed, PriorPrivate: created.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updatedPlan.Diagnostics) {
		t.Fatalf("update plan: err=%v diagnostics=%v", err, updatedPlan.Diagnostics)
	}
	updated, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: updateConfig, PriorState: created.NewState, PlannedState: updatedPlan.PlannedState, PlannedPrivate: updatedPlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updated.Diagnostics) {
		t.Fatalf("update: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(updated.Diagnostics))
	}
	if posts.Load() != 2 || reads.Load() != 3 {
		t.Fatalf("posts=%d reads=%d, want one generate, one update, and three reads", posts.Load(), reads.Load())
	}
	metadata := updateBody["metadata"].(map[string]interface{})
	owned := metadata["owned"].(map[string]interface{})
	if owned["keep"] != json.Number("3") {
		t.Fatalf("updated metadata lost exact value: %#v", metadata)
	}
	if _, present := owned["remove"]; present || metadata["legacy"] != "value" || metadata["api"].(map[string]interface{})["preserved"] != true {
		t.Fatalf("metadata replacement did not preserve/remove exact siblings: %#v", metadata)
	}
	if _, present := updateBody["config"]; present {
		t.Fatalf("unchanged config root was sent: %#v", updateBody["config"])
	}
	permissions := updateBody["permissions"].(map[string]interface{})
	if len(permissions) != 1 || permissions["api"] != "preserved" {
		t.Fatalf("permissions root removal did not preserve API sibling: %#v", permissions)
	}
	attributes := protocolAttributeMap(t, schema, updated.NewState)
	if !attributes["permissions_json"].IsNull() {
		t.Fatalf("removed permissions_json remained in state: %s", attributes["permissions_json"])
	}

	formattedValues := map[string]interface{}{
		"key":           "sk-key-update",
		"metadata":      map[string]tftypes.Value{"legacy": tftypes.NewValue(tftypes.String, "value")},
		"metadata_json": "{\n  \"owned\": { \"keep\": 3 }\n}",
		"config_json":   `{"native":true}`,
	}
	formattedConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, formattedValues))
	formattedProposed := organizationProjectProtocolReplace(t, schema, updated.NewState, map[string]interface{}{
		"metadata_json": formattedValues["metadata_json"],
	})
	steady, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: formattedConfig, PriorState: updated.NewState, ProposedNewState: formattedProposed, PriorPrivate: updated.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, updated.NewState, steady) != organizationProjectProtocolActionNoOp {
		priorAttributes := protocolAttributeMap(t, schema, updated.NewState)
		plannedAttributes := protocolAttributeMap(t, schema, steady.PlannedState)
		for name, priorValue := range priorAttributes {
			if !priorValue.Equal(plannedAttributes[name]) {
				t.Logf("formatting change %s: prior=%s planned=%s", name, priorValue, plannedAttributes[name])
			}
		}
		t.Fatalf("formatting-only plan: err=%v diagnostics=%s action=%s", err, agentProtocolDiagnosticsText(steady.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, updated.NewState, steady))
	}
	if posts.Load() != 2 {
		t.Fatalf("formatting-only plan issued a mutation: posts=%d", posts.Load())
	}

	unknownValues := map[string]interface{}{
		"key":           "sk-key-update",
		"metadata":      map[string]tftypes.Value{"legacy": tftypes.NewValue(tftypes.String, "value")},
		"metadata_json": tftypes.UnknownValue,
		"config_json":   `{"native":true}`,
	}
	unknownConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, unknownValues))
	unknownProposed := organizationProjectProtocolReplace(t, schema, updated.NewState, map[string]interface{}{"metadata_json": tftypes.UnknownValue})
	unknownPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: unknownConfig, PriorState: updated.NewState, ProposedNewState: unknownProposed, PriorPrivate: updated.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(unknownPlan.Diagnostics) {
		t.Fatalf("unknown semantic plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(unknownPlan.Diagnostics))
	}
	if protocolAttributeMap(t, schema, unknownPlan.PlannedState)["metadata_json"].IsKnown() || posts.Load() != 2 {
		t.Fatalf("unknown semantic value was not preserved without mutation")
	}

	var corruptPrivate map[string][]byte
	if err := json.Unmarshal(updated.Private, &corruptPrivate); err != nil {
		t.Fatal(err)
	}
	corruptPrivate[keyMetadataJSONProvenancePrivateKey] = []byte(`{"version":1,"initialized":true,"configured":true,"terraform_owned":["/wrong"],"api_owned":[],"pending_terraform_owned":[],"pending_api_owned":[],"pending_removals":[]}`)
	corruptRaw, _ := json.Marshal(corruptPrivate)
	corruptPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: formattedConfig, PriorState: updated.NewState, ProposedNewState: updated.NewState, PriorPrivate: corruptRaw,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(corruptPlan.Diagnostics) || posts.Load() != 2 {
		t.Fatalf("corrupt provenance: err=%v diagnostics=%s posts=%d", err, agentProtocolDiagnosticsText(corruptPlan.Diagnostics), posts.Load())
	}
	for _, protected := range []string{"wrong", "owned", "sk-key-update"} {
		if strings.Contains(agentProtocolDiagnosticsText(corruptPlan.Diagnostics), protected) {
			t.Fatalf("corrupt provenance diagnostic exposed protected content %q", protected)
		}
	}

	uncertainValues := map[string]interface{}{
		"key":           "sk-key-update",
		"metadata":      map[string]tftypes.Value{"legacy": tftypes.NewValue(tftypes.String, "value")},
		"metadata_json": `{"owned":{"keep":4}}`,
		"config_json":   `{"native":true}`,
	}
	uncertainConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, uncertainValues))
	uncertainProposed := organizationProjectProtocolReplace(t, schema, updated.NewState, map[string]interface{}{"metadata_json": uncertainValues["metadata_json"]})
	uncertainPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: uncertainConfig, PriorState: updated.NewState, ProposedNewState: uncertainProposed, PriorPrivate: updated.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(uncertainPlan.Diagnostics) {
		t.Fatalf("uncertain plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(uncertainPlan.Diagnostics))
	}
	corruptAfterPost.Store(true)
	uncertain, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: uncertainConfig, PriorState: updated.NewState, PlannedState: uncertainPlan.PlannedState, PlannedPrivate: uncertainPlan.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(uncertain.Diagnostics) || posts.Load() != 3 {
		t.Fatalf("uncertain apply: err=%v diagnostics=%s posts=%d", err, agentProtocolDiagnosticsText(uncertain.Diagnostics), posts.Load())
	}
	priorValue, _ := updated.NewState.Unmarshal(schema.ValueType())
	uncertainValue, _ := uncertain.NewState.Unmarshal(schema.ValueType())
	if !priorValue.Equal(uncertainValue) || !bytes.Equal(updated.Private, uncertain.Private) {
		t.Fatal("uncertain mutation changed prior public/private state")
	}
	for _, protected := range []string{"keep", "4", "malformed", "sk-key-update"} {
		if strings.Contains(agentProtocolDiagnosticsText(uncertain.Diagnostics), protected) {
			t.Fatalf("uncertain diagnostic exposed protected content %q", protected)
		}
	}

	// An uncertain value change retains prior state, then an ordinary refresh
	// adopts the authoritative remote value so planning can decide whether a
	// retry is still needed.
	serveMalformed.Store(false)
	reconciled, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: typeName, CurrentState: uncertain.NewState, Private: uncertain.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(reconciled.Diagnostics) {
		t.Fatalf("uncertain value refresh: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(reconciled.Diagnostics))
	}
	var reconciledJSON string
	if err := protocolAttributeMap(t, schema, reconciled.NewState)["metadata_json"].As(&reconciledJSON); err != nil || reconciledJSON != `{"owned":{"keep":4}}` {
		t.Fatalf("uncertain value refresh did not adopt authoritative value: value=%q err=%v", reconciledJSON, err)
	}

	// A committed removal whose first readback is malformed retains prior public
	// state plus value-free pending-removal provenance. The next healthy refresh
	// confirms absence, publishes the removed state, and clears recovery.
	removalValues := map[string]interface{}{
		"key":         "sk-key-update",
		"metadata":    map[string]tftypes.Value{"legacy": tftypes.NewValue(tftypes.String, "value")},
		"config_json": `{"native":true}`,
	}
	removalConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, removalValues))
	removalProposed := organizationProjectProtocolReplace(t, schema, reconciled.NewState, map[string]interface{}{"metadata_json": nil})
	removalPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: removalConfig, PriorState: reconciled.NewState, ProposedNewState: removalProposed, PriorPrivate: reconciled.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(removalPlan.Diagnostics) {
		t.Fatalf("uncertain removal plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(removalPlan.Diagnostics))
	}
	removal, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: removalConfig, PriorState: reconciled.NewState, PlannedState: removalPlan.PlannedState, PlannedPrivate: removalPlan.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(removal.Diagnostics) {
		t.Fatalf("uncertain removal apply: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(removal.Diagnostics))
	}
	if bytes.Equal(reconciled.Private, removal.Private) {
		t.Fatal("uncertain removal did not retain pending recovery provenance")
	}
	serveMalformed.Store(false)
	removalReconciled, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: typeName, CurrentState: removal.NewState, Private: removal.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(removalReconciled.Diagnostics) {
		t.Fatalf("uncertain removal refresh: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(removalReconciled.Diagnostics))
	}
	if !protocolAttributeMap(t, schema, removalReconciled.NewState)["metadata_json"].IsNull() {
		t.Fatal("confirmed semantic root removal remained in state")
	}
	var reconciledPrivate map[string][]byte
	if err := json.Unmarshal(removalReconciled.Private, &reconciledPrivate); err != nil {
		t.Fatal(err)
	}
	if len(reconciledPrivate[keyPendingUpdatePrivateKey]) != 0 {
		t.Fatal("confirmed semantic removal retained pending recovery provenance")
	}
}

func TestKeySemanticDictionaryAcceptedCreateRecoveryProtocol(t *testing.T) {
	ctx := context.Background()
	var posts, reads atomic.Int64
	const rawKey = "sk-key-recovery"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/key/generate":
			posts.Add(1)
			_, _ = writer.Write([]byte(`{"key":`))
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			reads.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"key": rawKey,
				"info": map[string]interface{}{
					"metadata": map[string]interface{}{"owned": true},
					"config":   map[string]interface{}{}, "permissions": map[string]interface{}{},
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_key"
	schema := schemas.ResourceSchemas[typeName]
	values := map[string]interface{}{"key": rawKey, "metadata_json": `{"owned":true}`}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: keySemanticCreateProposed(t, schema, values),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || posts.Load() != 1 {
		t.Fatalf("accepted create: err=%v diagnostics=%s posts=%d", err, agentProtocolDiagnosticsText(applied.Diagnostics), posts.Load())
	}
	diagnostic := agentProtocolDiagnosticsText(applied.Diagnostics)
	for _, protected := range []string{rawKey, "owned", "true", "metadata_json"} {
		if strings.Contains(diagnostic, protected) {
			t.Fatalf("accepted-create diagnostic exposed protected content %q: %s", protected, diagnostic)
		}
	}
	for _, protected := range [][]byte{[]byte(rawKey), []byte("owned")} {
		if bytes.Contains(applied.Private, protected) {
			t.Fatalf("accepted-create private state exposed protected content %q", protected)
		}
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	var id, key string
	if err := attributes["id"].As(&id); err != nil || id != hashKeyForID(rawKey) {
		t.Fatalf("recovery id=%q err=%v", id, err)
	}
	if err := attributes["key"].As(&key); err != nil || key != rawKey || !attributes["metadata_json"].IsNull() {
		t.Fatalf("recovery key/json mismatch: key=%q err=%v json=%s", key, err, attributes["metadata_json"])
	}
	if marker := protocolPrivateValue(t, applied.Private, keyAcceptedCreateRecoveryPrivateKey); string(marker) != "true" {
		t.Fatalf("recovery marker=%q", marker)
	}

	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: typeName, CurrentState: applied.NewState, Private: applied.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) || reads.Load() != 1 {
		t.Fatalf("recovery read: err=%v diagnostics=%s reads=%d", err, agentProtocolDiagnosticsText(refreshed.Diagnostics), reads.Load())
	}
	refreshedAttributes := protocolAttributeMap(t, schema, refreshed.NewState)
	if !refreshedAttributes["metadata_json"].IsNull() {
		t.Fatalf("recovery read adopted semantic metadata: %s", refreshedAttributes["metadata_json"])
	}
	if marker := protocolPrivateValue(t, refreshed.Private, keyAcceptedCreateRecoveryPrivateKey); len(marker) != 0 {
		t.Fatalf("recovery marker was not cleared: %q", marker)
	}
}

func TestKeySemanticDictionaryWriteOnlyRecoveryBlocksMutationProtocol(t *testing.T) {
	ctx := context.Background()
	const rawKey = "sk-key-wo-semantic-recovery"
	hashID := hashKeyForID(rawKey)
	hash, _ := keyHashFromID(hashID)
	var posts, updates, deletes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/key/generate":
			posts.Add(1)
			_, _ = writer.Write([]byte(`{"key":`))
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"key": hash,
				"info": map[string]interface{}{
					"metadata": map[string]interface{}{"owned": true},
					"config":   map[string]interface{}{}, "permissions": map[string]interface{}{},
				},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/key/update":
			updates.Add(1)
			_, _ = writer.Write([]byte(`{}`))
		case request.Method == http.MethodPost && request.URL.Path == "/key/delete":
			deletes.Add(1)
			_, _ = writer.Write([]byte(`{}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_key"
	schema := schemas.ResourceSchemas[typeName]
	values := map[string]interface{}{"key_wo": rawKey, "key_wo_version": "v1", "metadata_json": `{"owned":true}`}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: keySemanticCreateProposed(t, schema, values),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("write-only plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || posts.Load() != 1 {
		t.Fatalf("write-only accepted create: err=%v diagnostics=%s posts=%d", err, agentProtocolDiagnosticsText(applied.Diagnostics), posts.Load())
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	var id string
	if err := attributes["id"].As(&id); err != nil || id != hashID || !attributes["key"].IsNull() || !attributes["metadata_json"].IsNull() {
		t.Fatalf("write-only recovery state: id=%q key=%s json=%s err=%v", id, attributes["key"], attributes["metadata_json"], err)
	}

	updateValues := map[string]interface{}{"key_wo": rawKey, "key_wo_version": "v1", "metadata_json": `{"owned":true}`, "key_alias": "blocked"}
	updateConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, updateValues))
	updateProposed := organizationProjectProtocolReplace(t, schema, applied.NewState, map[string]interface{}{"key_alias": "blocked"})
	updatePlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: updateConfig, PriorState: applied.NewState, ProposedNewState: updateProposed, PriorPrivate: applied.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updatePlan.Diagnostics) {
		t.Fatalf("recovery update plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(updatePlan.Diagnostics))
	}
	blockedUpdate, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: updateConfig, PriorState: applied.NewState, PlannedState: updatePlan.PlannedState, PlannedPrivate: updatePlan.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(blockedUpdate.Diagnostics) || updates.Load() != 0 {
		t.Fatalf("recovery update was not blocked: err=%v diagnostics=%s updates=%d", err, agentProtocolDiagnosticsText(blockedUpdate.Diagnostics), updates.Load())
	}

	blockedDelete, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil)),
		PriorState: applied.NewState, PlannedState: nullState, PlannedPrivate: applied.Private,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(blockedDelete.Diagnostics) || deletes.Load() != 0 {
		t.Fatalf("recovery delete was not blocked: err=%v diagnostics=%s deletes=%d", err, agentProtocolDiagnosticsText(blockedDelete.Diagnostics), deletes.Load())
	}

	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: applied.NewState, Private: applied.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("write-only recovery read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(refreshed.Diagnostics))
	}
	if marker := protocolPrivateValue(t, refreshed.Private, keyAcceptedCreateRecoveryPrivateKey); len(marker) != 0 {
		t.Fatalf("write-only recovery marker remained: %q", marker)
	}
}

func TestKeySemanticDictionaryImportNonAdoptionAndTakeoverProtocol(t *testing.T) {
	ctx := context.Background()
	const rawKey = "sk-key-import-json"
	remoteConfig := map[string]interface{}{"native": true, "api": map[string]interface{}{"preserved": true}}
	var updates atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"key": rawKey,
				"info": map[string]interface{}{
					"metadata": map[string]interface{}{},
					"config":   remoteConfig, "permissions": map[string]interface{}{},
				},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/key/update":
			updates.Add(1)
			var body map[string]interface{}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&body); err != nil {
				t.Errorf("decode update: %v", err)
			}
			object, ok := body["config"].(map[string]interface{})
			if !ok || object["native"] != true || object["api"].(map[string]interface{})["preserved"] != true {
				t.Errorf("takeover did not preserve complete config: %#v", body["config"])
			}
			remoteConfig = object
			_, _ = writer.Write([]byte(`{}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_key"
	schema := schemas.ResourceSchemas[typeName]
	importedResponse, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: rawKey})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importedResponse.Diagnostics) || len(importedResponse.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(importedResponse.Diagnostics))
	}
	imported := importedResponse.ImportedResources[0]
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.State, Private: imported.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("import read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	attributes := protocolAttributeMap(t, schema, read.NewState)
	if !attributes["config_json"].IsNull() || !attributes["config"].IsNull() {
		t.Fatalf("import adopted heterogeneous config: json=%s legacy=%s", attributes["config_json"], attributes["config"])
	}
	steadyRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: read.NewState, Private: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(steadyRead.Diagnostics) {
		t.Fatalf("post-import read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(steadyRead.Diagnostics))
	}
	steadyAttributes := protocolAttributeMap(t, schema, steadyRead.NewState)
	if !steadyAttributes["config_json"].IsNull() || !steadyAttributes["config"].IsNull() {
		t.Fatalf("post-import read adopted heterogeneous config: json=%s legacy=%s", steadyAttributes["config_json"], steadyAttributes["config"])
	}

	values := map[string]interface{}{"key": rawKey, "config_json": `{"native":true}`}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	proposed := organizationProjectProtocolReplace(t, schema, steadyRead.NewState, map[string]interface{}{"config_json": `{"native":true}`})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: steadyRead.NewState, ProposedNewState: proposed, PriorPrivate: steadyRead.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, steadyRead.NewState, planned) != organizationProjectProtocolActionUpdate {
		t.Fatalf("takeover plan: err=%v diagnostics=%s action=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, steadyRead.NewState, planned))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: steadyRead.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || updates.Load() != 1 {
		t.Fatalf("takeover apply: err=%v diagnostics=%s updates=%d", err, agentProtocolDiagnosticsText(applied.Diagnostics), updates.Load())
	}
	attributes = protocolAttributeMap(t, schema, applied.NewState)
	var semantic string
	if err := attributes["config_json"].As(&semantic); err != nil || semantic != `{"native":true}` {
		t.Fatalf("takeover state=%q err=%v", semantic, err)
	}
}

func TestKeyServiceAccountSemanticMetadataRequiresReplacementProtocol(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_key"
	schema := schemas.ResourceSchemas[typeName]
	semantic := `{"custom":{"enabled":true}}`
	priorValues := map[string]interface{}{
		"id": hashKeyForID("sk-service-semantic"), "key": "sk-service-semantic",
		"service_account_id": "service-old", "metadata_json": semantic,
	}
	prior := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, priorValues))
	_, metadataProvenance, err := keySemanticDictionaryConfiguration(ctx, types.StringValue(semantic), types.MapNull(types.StringType), keyMetadataJSONReservedKeys)
	if err != nil {
		t.Fatal(err)
	}
	unconfigured := keyUnconfiguredSemanticDictionaryProvenance()
	privateValues, err := encodeKeySemanticProvenance(ctx, keySemanticPrepared{
		metadataProvenance: metadataProvenance, configProvenance: unconfigured, permissionsProvenance: unconfigured,
	})
	if err != nil {
		t.Fatal(err)
	}
	private, _ := json.Marshal(privateValues)
	configValues := map[string]interface{}{"key": "sk-service-semantic", "service_account_id": "service-new", "metadata_json": semantic}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := organizationProjectProtocolReplace(t, schema, prior, map[string]interface{}{"service_account_id": "service-new"})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: prior, ProposedNewState: proposed, PriorPrivate: private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || len(planned.RequiresReplace) == 0 || requests.Load() != 0 {
		t.Fatalf("service replacement: err=%v diagnostics=%s replace=%v requests=%d", err, agentProtocolDiagnosticsText(planned.Diagnostics), planned.RequiresReplace, requests.Load())
	}
}

func TestKeySemanticDictionaryOverlapFailsBeforeRequestProtocol(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_key"]
	values := map[string]interface{}{
		"key": "sk-overlap", "metadata_json": `{"tags":[]}`,
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_key", Config: config, PriorState: nullState, ProposedNewState: keySemanticCreateProposed(t, schema, values),
	})
	if err != nil {
		t.Fatal(err)
	}
	if accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		if requests.Load() != 0 {
			t.Fatalf("requests=%d", requests.Load())
		}
		return
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_key", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || requests.Load() != 0 {
		t.Fatalf("apply err=%v diagnostics=%v requests=%d", err, applied.Diagnostics, requests.Load())
	}
}
