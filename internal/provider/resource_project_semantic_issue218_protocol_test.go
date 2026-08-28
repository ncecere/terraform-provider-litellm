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

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func projectSemanticPrivateValue(t *testing.T, private []byte, key string) []byte {
	t.Helper()
	var values map[string][]byte
	if err := json.Unmarshal(private, &values); err != nil {
		t.Fatalf("decode private: %v", err)
	}
	return values[key]
}

func projectSemanticProtocolPlan(t *testing.T, ctx context.Context, server tfprotov6.ProviderServer, schema *tfprotov6.Schema, values map[string]interface{}, prior, proposed *tfprotov6.DynamicValue, private []byte) (*tfprotov6.DynamicValue, *tfprotov6.PlanResourceChangeResponse) {
	t.Helper()
	config := projectSemanticConfig(t, schema, values)
	planned, err := server.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_project", Config: config, PriorState: prior, ProposedNewState: proposed, PriorPrivate: private,
	})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return config, planned
}

func TestProjectSemanticCreateUpdateExactLifecycleProtocol(t *testing.T) {
	ctx := context.Background()
	const teamID = "team-project-semantic"
	var creates, updates, reads atomic.Int64
	var projectID string
	var remoteMetadata map[string]interface{}
	var createBody, updateBody map[string]interface{}

	remote := func() map[string]interface{} {
		return map[string]interface{}{
			"project_id": projectID, "team_id": teamID, "models": []interface{}{}, "metadata": remoteMetadata, "blocked": false,
			"litellm_budget_table": map[string]interface{}{},
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/project/new":
			creates.Add(1)
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&createBody); err != nil {
				t.Errorf("decode create: %v", err)
				return
			}
			projectID, _ = createBody["project_id"].(string)
			remoteMetadata, _ = createBody["metadata"].(map[string]interface{})
			remoteMetadata["api_sibling"] = map[string]interface{}{"preserved": true}
			_ = json.NewEncoder(writer).Encode(remote())
		case request.Method == http.MethodPost && request.URL.Path == "/project/update":
			updates.Add(1)
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&updateBody); err != nil {
				t.Errorf("decode update: %v", err)
				return
			}
			if metadata, ok := updateBody["metadata"].(map[string]interface{}); ok {
				remoteMetadata = metadata
			}
			_ = json.NewEncoder(writer).Encode(remote())
		case request.Method == http.MethodGet && request.URL.Path == "/project/info":
			reads.Add(1)
			_ = json.NewEncoder(writer).Encode(remote())
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	semantic := `{"integer":9007199254740993123456789,"decimal":1.25,"native":true,"string":"true","nil":null,"list":[null,false,"1",1],"empty":{},"owned":{"keep":1,"remove":2}}`
	createValues := map[string]interface{}{
		"team_id":         teamID,
		"metadata":        map[string]tftypes.Value{"legacy": tftypes.NewValue(tftypes.String, "scalar-value")},
		"metadata_json":   semantic,
		"tags":            []tftypes.Value{tftypes.NewValue(tftypes.String, "one"), tftypes.NewValue(tftypes.String, "two")},
		"model_rpm_limit": map[string]tftypes.Value{"model": tftypes.NewValue(tftypes.Number, int64(9007199254740993))},
		"model_tpm_limit": map[string]tftypes.Value{"model": tftypes.NewValue(tftypes.Number, int64(9007199254740991))},
	}
	config, planned := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, createValues, nullState, keySemanticCreateProposed(t, schema, createValues), nil)
	if accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan diagnostics=%s", agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_project", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("create: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(created.Diagnostics))
	}
	if uuid.Validate(projectID) != nil || creates.Load() != 1 || updates.Load() != 0 || reads.Load() != 1 {
		t.Fatalf("create identity/calls: id=%q creates=%d updates=%d reads=%d", projectID, creates.Load(), updates.Load(), reads.Load())
	}
	metadata := createBody["metadata"].(map[string]interface{})
	if metadata["integer"] != json.Number("9007199254740993123456789") || metadata["decimal"] != json.Number("1.25") || metadata["native"] != true || metadata["string"] != "true" || metadata["nil"] != nil || metadata["legacy"] != "scalar-value" {
		t.Fatalf("create lost exact metadata identities: %#v", metadata)
	}
	if len(metadata["list"].([]interface{})) != 4 || len(metadata["tags"].([]interface{})) != 2 || metadata["model_rpm_limit"].(map[string]interface{})["model"] != json.Number("9007199254740993") || metadata["model_tpm_limit"].(map[string]interface{})["model"] != json.Number("9007199254740991") {
		t.Fatalf("create lost list/tags/rates: %#v", metadata)
	}

	updatedSemantic := `{"integer":9007199254740993123456789,"decimal":1.25,"native":true,"string":"true","nil":null,"list":[null,false,"1",1],"empty":{},"owned":{"keep":3}}`
	updateValues := map[string]interface{}{
		"team_id":         teamID,
		"metadata":        map[string]tftypes.Value{"legacy": tftypes.NewValue(tftypes.String, "updated-scalar")},
		"metadata_json":   updatedSemantic,
		"tags":            []tftypes.Value{tftypes.NewValue(tftypes.String, "updated")},
		"model_rpm_limit": map[string]tftypes.Value{"model": tftypes.NewValue(tftypes.Number, int64(17))},
		"model_tpm_limit": map[string]tftypes.Value{"model": tftypes.NewValue(tftypes.Number, int64(19))},
	}
	proposed := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{
		"metadata": updateValues["metadata"], "metadata_json": updatedSemantic, "tags": updateValues["tags"],
		"model_rpm_limit": updateValues["model_rpm_limit"], "model_tpm_limit": updateValues["model_tpm_limit"],
	})
	updateConfig, updatePlan := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, updateValues, created.NewState, proposed, created.Private)
	if accessGroupProtocolDiagnosticsHaveError(updatePlan.Diagnostics) {
		t.Fatalf("update plan diagnostics=%s", agentProtocolDiagnosticsText(updatePlan.Diagnostics))
	}
	readsBefore := reads.Load()
	updated, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_project", Config: updateConfig, PriorState: created.NewState, PlannedState: updatePlan.PlannedState, PlannedPrivate: updatePlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updated.Diagnostics) || updates.Load() != 1 || reads.Load() != readsBefore+2 {
		t.Fatalf("update: err=%v diagnostics=%s updates=%d reads-delta=%d", err, agentProtocolDiagnosticsText(updated.Diagnostics), updates.Load(), reads.Load()-readsBefore)
	}
	replacement := updateBody["metadata"].(map[string]interface{})
	if replacement["api_sibling"].(map[string]interface{})["preserved"] != true || replacement["legacy"] != "updated-scalar" || replacement["model_rpm_limit"].(map[string]interface{})["model"] != json.Number("17") || replacement["model_tpm_limit"].(map[string]interface{})["model"] != json.Number("19") {
		t.Fatalf("update did not preserve API sibling and managed roots: %#v", replacement)
	}
	owned := replacement["owned"].(map[string]interface{})
	if owned["keep"] != json.Number("3") {
		t.Fatalf("updated owned value=%#v", owned)
	}
	if _, present := owned["remove"]; present {
		t.Fatalf("nested removal retained: %#v", owned)
	}

	formattedValues := map[string]interface{}{
		"team_id": teamID, "metadata": updateValues["metadata"],
		"metadata_json": "{\n \"owned\": {\"keep\": 3}, \"empty\": {}, \"list\": [null,false,\"1\",1], \"nil\": null, \"string\": \"true\", \"native\": true, \"decimal\": 1.25, \"integer\": 9007199254740993123456789\n}",
		"tags":          updateValues["tags"], "model_rpm_limit": updateValues["model_rpm_limit"], "model_tpm_limit": updateValues["model_tpm_limit"],
	}
	_, formattingPlan := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, formattedValues, updated.NewState, updated.NewState, updated.Private)
	if accessGroupProtocolDiagnosticsHaveError(formattingPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, updated.NewState, formattingPlan) != organizationProjectProtocolActionNoOp || updates.Load() != 1 || reads.Load() != readsBefore+2 {
		t.Fatalf("formatting no-op: diagnostics=%s action=%s updates=%d reads=%d", agentProtocolDiagnosticsText(formattingPlan.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, updated.NewState, formattingPlan), updates.Load(), reads.Load())
	}

	emptyValues := map[string]interface{}{
		"team_id": teamID, "metadata": updateValues["metadata"], "metadata_json": `{}`, "tags": updateValues["tags"],
		"model_rpm_limit": updateValues["model_rpm_limit"], "model_tpm_limit": updateValues["model_tpm_limit"],
	}
	emptyProposed := organizationProjectProtocolReplace(t, schema, updated.NewState, map[string]interface{}{"metadata_json": `{}`})
	emptyConfig, emptyPlan := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, emptyValues, updated.NewState, emptyProposed, updated.Private)
	emptied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: emptyConfig, PriorState: updated.NewState, PlannedState: emptyPlan.PlannedState, PlannedPrivate: emptyPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(emptied.Diagnostics) {
		t.Fatalf("empty object apply: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(emptied.Diagnostics))
	}
	if _, present := remoteMetadata["owned"]; present {
		t.Fatalf("whole owned root remained after empty object: %#v", remoteMetadata)
	}
	if remoteMetadata["api_sibling"].(map[string]interface{})["preserved"] != true {
		t.Fatalf("empty object removed API sibling: %#v", remoteMetadata)
	}
	var emptyJSON string
	if err := protocolAttributeMap(t, schema, emptied.NewState)["metadata_json"].As(&emptyJSON); err != nil || emptyJSON != `{}` {
		t.Fatalf("empty semantic state=%q err=%v", emptyJSON, err)
	}

	removedValues := map[string]interface{}{
		"team_id": teamID, "metadata": updateValues["metadata"], "tags": updateValues["tags"],
		"model_rpm_limit": updateValues["model_rpm_limit"], "model_tpm_limit": updateValues["model_tpm_limit"],
	}
	removedProposed := organizationProjectProtocolReplace(t, schema, emptied.NewState, map[string]interface{}{"metadata_json": nil})
	removedConfig, removedPlan := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, removedValues, emptied.NewState, removedProposed, emptied.Private)
	removed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: removedConfig, PriorState: emptied.NewState, PlannedState: removedPlan.PlannedState, PlannedPrivate: removedPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(removed.Diagnostics) || !protocolAttributeMap(t, schema, removed.NewState)["metadata_json"].IsNull() {
		t.Fatalf("whole semantic removal: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(removed.Diagnostics))
	}
	if creates.Load() != 1 || updates.Load() != 3 || reads.Load() != 7 {
		t.Fatalf("final route counts create=%d update=%d GET=%d, want 1/3/7", creates.Load(), updates.Load(), reads.Load())
	}
}

func TestProjectSemanticImportRepeatedReadAndExplicitTakeoverProtocol(t *testing.T) {
	ctx := context.Background()
	const id, teamID = "project-import-semantic", "team-semantic"
	remoteMetadata := map[string]interface{}{"native": true, "list": []interface{}{json.Number("1"), false}, "api": map[string]interface{}{"preserved": true}}
	var reads, updates atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/project/info":
			reads.Add(1)
			_ = json.NewEncoder(writer).Encode(projectSemanticRemote(id, remoteMetadata))
		case request.Method == http.MethodPost && request.URL.Path == "/project/update":
			updates.Add(1)
			var body map[string]interface{}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			_ = decoder.Decode(&body)
			remoteMetadata = body["metadata"].(map[string]interface{})
			_ = json.NewEncoder(writer).Encode(projectSemanticRemote(id, remoteMetadata))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_project", ID: id})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) {
		t.Fatalf("import: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	state, private := imported.ImportedResources[0].State, imported.ImportedResources[0].Private
	for attempt := 0; attempt < 2; attempt++ {
		read, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: state, Private: private})
		if readErr != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
			t.Fatalf("read %d: err=%v diagnostics=%s", attempt+1, readErr, agentProtocolDiagnosticsText(read.Diagnostics))
		}
		attributes := protocolAttributeMap(t, schema, read.NewState)
		if !attributes["metadata_json"].IsNull() || !attributes["metadata"].IsNull() {
			t.Fatalf("read %d adopted heterogeneous metadata: json=%s legacy=%s", attempt+1, attributes["metadata_json"], attributes["metadata"])
		}
		state, private = read.NewState, read.Private
	}
	takeoverValues := map[string]interface{}{"team_id": teamID, "metadata_json": `{"native":false}`}
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"metadata_json": takeoverValues["metadata_json"]})
	config, planned := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, takeoverValues, state, proposed, private)
	if accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("takeover plan diagnostics=%s", agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	taken, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(taken.Diagnostics) || updates.Load() != 1 || reads.Load() != 4 {
		t.Fatalf("takeover: err=%v diagnostics=%s updates=%d reads=%d", err, agentProtocolDiagnosticsText(taken.Diagnostics), updates.Load(), reads.Load())
	}
	if remoteMetadata["native"] != false || remoteMetadata["api"].(map[string]interface{})["preserved"] != true {
		t.Fatalf("takeover did not preserve API sibling: %#v", remoteMetadata)
	}
}

func TestProjectSemanticAcceptedCreateTeamRecoveryAndPrivacyProtocol(t *testing.T) {
	for _, transport := range []string{"malformed response", "commit then hijack"} {
		t.Run(transport, func(t *testing.T) {
			ctx := context.Background()
			const teamID = "team-sensitive-recovery"
			const metadataKey = "sensitive_metadata_name"
			var projectID string
			var reads, creates atomic.Int64
			teamMode := atomic.Int64{}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch {
				case request.Method == http.MethodPost && request.URL.Path == "/project/new":
					creates.Add(1)
					var body map[string]interface{}
					_ = json.NewDecoder(request.Body).Decode(&body)
					projectID, _ = body["project_id"].(string)
					if transport == "commit then hijack" {
						connection, _, err := writer.(http.Hijacker).Hijack()
						if err != nil {
							t.Errorf("hijack: %v", err)
							return
						}
						_ = connection.Close()
						return
					}
					_, _ = writer.Write([]byte(`{"project_id":`))
				case request.Method == http.MethodGet && request.URL.Path == "/project/info":
					reads.Add(1)
					object := map[string]interface{}{"project_id": projectID, "metadata": map[string]interface{}{metadataKey: true}, "litellm_budget_table": map[string]interface{}{}}
					switch teamMode.Load() {
					case 1:
						object["team_id"] = nil
					case 2:
						object["team_id"] = "wrong-team"
					case 3:
						object["team_id"] = teamID
					}
					_ = json.NewEncoder(writer).Encode(object)
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_project"]
			nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
			values := map[string]interface{}{"team_id": teamID, "metadata_json": `{"` + metadataKey + `":true}`}
			config, planned := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, values, nullState, keySemanticCreateProposed(t, schema, values), nil)
			created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) || creates.Load() != 1 || !protocolPrivateHasKey(t, created.Private, projectAcceptedCreateRecoveryPrivateKey) {
				t.Fatalf("uncertain create: err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(created.Diagnostics), created.Private)
			}
			attributes := protocolAttributeMap(t, schema, created.NewState)
			var gotID, gotTeam string
			if attributes["id"].As(&gotID) != nil || attributes["team_id"].As(&gotTeam) != nil || gotID != projectID || gotTeam != teamID || !attributes["metadata_json"].IsNull() {
				t.Fatalf("partial state id=%q team=%q metadata=%s", gotID, gotTeam, attributes["metadata_json"])
			}
			diagnostic := agentProtocolDiagnosticsText(created.Diagnostics)
			for _, protected := range []string{projectID, teamID, metadataKey, "/project", "true"} {
				if strings.Contains(diagnostic, protected) || bytes.Contains(created.Private, []byte(protected)) {
					t.Fatalf("recovery exposed %q: diagnostics=%q private=%s", protected, diagnostic, created.Private)
				}
			}

			state, private := created.NewState, created.Private
			for mode, name := range []string{"missing", "null", "wrong"} {
				teamMode.Store(int64(mode))
				read, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: state, Private: private})
				if readErr != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) || !protocolPrivateHasKey(t, read.Private, projectAcceptedCreateRecoveryPrivateKey) {
					t.Fatalf("%s team recovery: err=%v diagnostics=%s private=%s", name, readErr, agentProtocolDiagnosticsText(read.Diagnostics), read.Private)
				}
			}
			teamMode.Store(3)
			reconciled, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: state, Private: private})
			if readErr != nil || accessGroupProtocolDiagnosticsHaveError(reconciled.Diagnostics) || protocolPrivateHasKey(t, reconciled.Private, projectAcceptedCreateRecoveryPrivateKey) {
				t.Fatalf("exact team recovery: err=%v diagnostics=%s private=%s", readErr, agentProtocolDiagnosticsText(reconciled.Diagnostics), reconciled.Private)
			}
			reconciledAttributes := protocolAttributeMap(t, schema, reconciled.NewState)
			if !reconciledAttributes["metadata_json"].IsNull() || !reconciledAttributes["metadata"].IsNull() {
				t.Fatalf("recovery adopted metadata: json=%s legacy=%s", reconciledAttributes["metadata_json"], reconciledAttributes["metadata"])
			}
		})
	}
}

func TestProjectSemanticPendingExpansionContractionAndMutationBlockingProtocol(t *testing.T) {
	ctx := context.Background()
	const id, teamID = "project-pending-shape", "team-pending-shape"
	var observed atomic.Value
	var mutations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet || request.URL.Path != "/project/info" {
			mutations.Add(1)
			http.Error(writer, "unexpected mutation", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{"project_id": id, "team_id": teamID, "metadata": observed.Load(), "litellm_budget_table": map[string]interface{}{}})
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	cases := []struct {
		name, prior, next       string
		committed, not, partial map[string]interface{}
	}{
		{"expansion", `{"shape":1}`, `{"shape":{"a":1,"b":2}}`, map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1"), "b": json.Number("2")}}, map[string]interface{}{"shape": json.Number("1")}, map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1")}}},
		{"contraction", `{"shape":{"a":1,"b":2}}`, `{"shape":1}`, map[string]interface{}{"shape": json.Number("1")}, map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1"), "b": json.Number("2")}}, map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1")}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			prior, _ := prepareProjectSemanticDictionary(ctx, types.StringValue(test.prior), types.MapNull(types.StringType))
			next, _ := prepareProjectSemanticDictionary(ctx, types.StringValue(test.next), types.MapNull(types.StringType))
			transition, err := next.updateOwnership(ctx, prior.provenance)
			if err != nil {
				t.Fatal(err)
			}
			pendingRaw, _ := encodeKeySemanticPendingTransition(ctx, pendingProjectSemanticTransition(transition))
			priorRaw, _ := encodeProjectSemanticProvenance(ctx, prior.provenance)
			private, _ := json.Marshal(map[string][]byte{projectMetadataJSONProvenancePrivateKey: priorRaw, projectPendingUpdatePrivateKey: pendingRaw})
			state := projectSemanticConfig(t, schema, map[string]interface{}{"id": id, "team_id": teamID, "metadata_json": test.prior})

			if test.name == "expansion" {
				plannedState := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"metadata_json": test.next})
				blockedUpdate, applyErr := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: state, PriorState: state, PlannedState: plannedState, PlannedPrivate: private})
				if applyErr != nil || !accessGroupProtocolDiagnosticsHaveError(blockedUpdate.Diagnostics) {
					t.Fatalf("pending update was not blocked: err=%v diagnostics=%s", applyErr, agentProtocolDiagnosticsText(blockedUpdate.Diagnostics))
				}
				nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
				blockedDelete, deleteErr := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", PriorState: state, PlannedState: nullState, PlannedPrivate: private})
				if deleteErr != nil || !accessGroupProtocolDiagnosticsHaveError(blockedDelete.Diagnostics) || mutations.Load() != 0 {
					t.Fatalf("pending delete was not blocked: err=%v diagnostics=%s mutations=%d", deleteErr, agentProtocolDiagnosticsText(blockedDelete.Diagnostics), mutations.Load())
				}
			}
			for name, metadata := range map[string]map[string]interface{}{"committed": test.committed, "not-committed": test.not} {
				observed.Store(metadata)
				read, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: state, Private: private})
				if readErr != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) || protocolPrivateHasKey(t, read.Private, projectPendingUpdatePrivateKey) {
					t.Fatalf("%s read: err=%v diagnostics=%s private=%s", name, readErr, agentProtocolDiagnosticsText(read.Diagnostics), read.Private)
				}
				var got string
				_ = protocolAttributeMap(t, schema, read.NewState)["metadata_json"].As(&got)
				want := test.prior
				if name == "committed" {
					want = test.next
				}
				gotObject, _ := parseSemanticDictionary(ctx, got)
				wantObject, _ := parseSemanticDictionary(ctx, want)
				equal, _ := semanticDictionaryValuesEqual(ctx, gotObject, wantObject)
				if !equal {
					t.Fatalf("%s JSON=%s want semantic %s", name, got, want)
				}
			}
			observed.Store(test.partial)
			partial, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: state, Private: private})
			if readErr != nil || !accessGroupProtocolDiagnosticsHaveError(partial.Diagnostics) || !protocolPrivateHasKey(t, partial.Private, projectPendingUpdatePrivateKey) {
				t.Fatalf("partial read: err=%v diagnostics=%s private=%s", readErr, agentProtocolDiagnosticsText(partial.Diagnostics), partial.Private)
			}
		})
	}
}

func TestProjectSemanticBudgetSecondPhaseRecoveryProtocol(t *testing.T) {
	ctx := context.Background()
	const id, teamID, budgetID = "project-budget-recovery", "team-budget-recovery", "budget-project-recovery"
	var remoteMetadata = map[string]interface{}{"owned": json.Number("1"), "api": "preserved"}
	var remoteDuration = "7d"
	var reads, projectUpdates, budgetUpdates, deletes atomic.Int64
	var failBudget atomic.Bool
	failBudget.Store(true)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		remote := func() map[string]interface{} {
			return map[string]interface{}{"project_id": id, "team_id": teamID, "metadata": remoteMetadata, "budget_id": budgetID, "litellm_budget_table": map[string]interface{}{"budget_id": budgetID, "budget_duration": remoteDuration}}
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/project/info":
			reads.Add(1)
			_ = json.NewEncoder(writer).Encode(remote())
		case request.Method == http.MethodPost && request.URL.Path == "/project/update":
			projectUpdates.Add(1)
			var body map[string]interface{}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			_ = decoder.Decode(&body)
			if metadata, ok := body["metadata"].(map[string]interface{}); ok {
				remoteMetadata = metadata
			}
			if duration, ok := body["budget_duration"].(string); ok {
				remoteDuration = duration
			}
			_ = json.NewEncoder(writer).Encode(remote())
		case request.Method == http.MethodPost && request.URL.Path == "/budget/update":
			budgetUpdates.Add(1)
			if failBudget.Swap(false) {
				http.Error(writer, `{"error":"lost"}`, http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"budget_id": budgetID})
		case request.Method == http.MethodDelete && request.URL.Path == "/project/delete":
			deletes.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	priorJSON := `{"owned":1}`
	priorPrepared, _ := prepareProjectSemanticDictionary(ctx, types.StringValue(priorJSON), types.MapNull(types.StringType))
	provenance, _ := encodeProjectSemanticProvenance(ctx, priorPrepared.provenance)
	private, _ := json.Marshal(map[string][]byte{projectMetadataJSONProvenancePrivateKey: provenance})
	state := projectSemanticConfig(t, schema, map[string]interface{}{"id": id, "team_id": teamID, "budget_id": budgetID, "budget_duration": "7d", "metadata_json": priorJSON})
	values := map[string]interface{}{"team_id": teamID, "budget_id": budgetID, "budget_duration": "30d", "metadata_json": `{"owned":2}`}
	proposed := organizationProjectProtocolReplace(t, schema, state, map[string]interface{}{"budget_duration": "30d", "metadata_json": values["metadata_json"]})
	config, planned := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, values, state, proposed, private)
	if accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("combined plan diagnostics=%s", agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	failed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) || reads.Load() != 1 || projectUpdates.Load() != 1 || budgetUpdates.Load() != 1 || !protocolPrivateHasKey(t, failed.Private, projectPendingBudgetPrivateKey) {
		t.Fatalf("second phase failure: err=%v diagnostics=%s GET=%d projectPOST=%d budgetPOST=%d private=%s", err, agentProtocolDiagnosticsText(failed.Diagnostics), reads.Load(), projectUpdates.Load(), budgetUpdates.Load(), failed.Private)
	}
	priorValue, _ := state.Unmarshal(schema.ValueType())
	failedValue, _ := failed.NewState.Unmarshal(schema.ValueType())
	if !priorValue.Equal(failedValue) {
		t.Fatal("second phase failure did not retain prior public state")
	}
	marker := projectSemanticPrivateValue(t, failed.Private, projectPendingBudgetPrivateKey)
	if string(marker) != `{"version":1,"fields":["budget_duration"]}` || bytes.Contains(marker, []byte("7d")) || bytes.Contains(marker, []byte("30d")) {
		t.Fatalf("pending budget marker is not canonical/value-free: %s", marker)
	}

	blockedPlan := organizationProjectProtocolReplace(t, schema, failed.NewState, map[string]interface{}{"budget_duration": "30d"})
	blocked, applyErr := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: failed.NewState, PlannedState: blockedPlan, PlannedPrivate: failed.Private})
	if applyErr != nil || !accessGroupProtocolDiagnosticsHaveError(blocked.Diagnostics) || projectUpdates.Load() != 1 || budgetUpdates.Load() != 1 {
		t.Fatalf("pending budget update not blocked: err=%v diagnostics=%s", applyErr, agentProtocolDiagnosticsText(blocked.Diagnostics))
	}
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	deleted, deleteErr := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", PriorState: failed.NewState, PlannedState: nullState, PlannedPrivate: failed.Private})
	if deleteErr != nil || !accessGroupProtocolDiagnosticsHaveError(deleted.Diagnostics) || deletes.Load() != 0 {
		t.Fatalf("pending budget delete not blocked: err=%v diagnostics=%s deletes=%d", deleteErr, agentProtocolDiagnosticsText(deleted.Diagnostics), deletes.Load())
	}

	refreshed, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: failed.NewState, Private: failed.Private})
	if readErr != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) || protocolPrivateHasKey(t, refreshed.Private, projectPendingBudgetPrivateKey) {
		t.Fatalf("budget recovery refresh: err=%v diagnostics=%s private=%s", readErr, agentProtocolDiagnosticsText(refreshed.Diagnostics), refreshed.Private)
	}
	var duration string
	if err := protocolAttributeMap(t, schema, refreshed.NewState)["budget_duration"].As(&duration); err != nil || duration != "7d" {
		t.Fatalf("refresh duration=%q want prior 7d err=%v", duration, err)
	}

	retryValues := map[string]interface{}{"team_id": teamID, "budget_id": budgetID, "budget_duration": "30d", "metadata_json": `{"owned":2}`}
	retryProposed := organizationProjectProtocolReplace(t, schema, refreshed.NewState, map[string]interface{}{"budget_duration": "30d"})
	retryConfig, retryPlan := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, retryValues, refreshed.NewState, retryProposed, refreshed.Private)
	if accessGroupProtocolDiagnosticsHaveError(retryPlan.Diagnostics) {
		t.Fatalf("retry plan diagnostics=%s", agentProtocolDiagnosticsText(retryPlan.Diagnostics))
	}
	retried, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: retryConfig, PriorState: refreshed.NewState, PlannedState: retryPlan.PlannedState, PlannedPrivate: retryPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(retried.Diagnostics) || projectUpdates.Load() != 2 || budgetUpdates.Load() != 2 || protocolPrivateHasKey(t, retried.Private, projectPendingBudgetPrivateKey) {
		t.Fatalf("retry: err=%v diagnostics=%s projectPOST=%d budgetPOST=%d private=%s", err, agentProtocolDiagnosticsText(retried.Diagnostics), projectUpdates.Load(), budgetUpdates.Load(), retried.Private)
	}
	if reads.Load() != 4 {
		t.Fatalf("GET count=%d want 4 (one combined hydration, one recovery, one retry lookup, one retry readback)", reads.Load())
	}

	requestsBefore := reads.Load() + projectUpdates.Load() + budgetUpdates.Load() + deletes.Load()
	malformedPrivate, _ := json.Marshal(map[string][]byte{
		projectMetadataJSONProvenancePrivateKey: projectSemanticPrivateValue(t, refreshed.Private, projectMetadataJSONProvenancePrivateKey),
		projectPendingBudgetPrivateKey:          []byte(`{"version":1,"fields":["budget_duration"],"extra":true}`),
	})
	malformed, malformedErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: refreshed.NewState, Private: malformedPrivate})
	requestsAfter := reads.Load() + projectUpdates.Load() + budgetUpdates.Load() + deletes.Load()
	if malformedErr != nil || !accessGroupProtocolDiagnosticsHaveError(malformed.Diagnostics) || requestsAfter != requestsBefore {
		t.Fatalf("malformed budget marker reached HTTP: err=%v diagnostics=%s requests=%d/%d", malformedErr, agentProtocolDiagnosticsText(malformed.Diagnostics), requestsAfter, requestsBefore)
	}
}

func TestProjectSemanticMalformedIdentityMetadataPrivateAndCancellationProtocol(t *testing.T) {
	ctx := context.Background()
	const id, teamID, secret = "project-private-identity", "team-private", "secret_metadata_path"
	var requests atomic.Int64
	var mode atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch mode.Load() {
		case 0:
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"project_id": "wrong-identity", "team_id": teamID, "metadata": map[string]interface{}{secret: true}, "litellm_budget_table": map[string]interface{}{}})
		case 1:
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"project_id": id, "team_id": teamID, "metadata": []interface{}{secret}, "litellm_budget_table": map[string]interface{}{}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	prepared, _ := prepareProjectSemanticDictionary(ctx, types.StringValue(`{"`+secret+`":true}`), types.MapNull(types.StringType))
	provenance, _ := encodeProjectSemanticProvenance(ctx, prepared.provenance)
	private, _ := json.Marshal(map[string][]byte{projectMetadataJSONProvenancePrivateKey: provenance})
	state := projectSemanticConfig(t, schema, map[string]interface{}{"id": id, "team_id": teamID, "metadata_json": `{"` + secret + `":true}`})
	for _, testMode := range []int64{0, 1} {
		mode.Store(testMode)
		read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: state, Private: private})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
			t.Fatalf("malformed mode %d: err=%v diagnostics=%s", testMode, err, agentProtocolDiagnosticsText(read.Diagnostics))
		}
		if strings.Contains(agentProtocolDiagnosticsText(read.Diagnostics), secret) || strings.Contains(agentProtocolDiagnosticsText(read.Diagnostics), id) {
			t.Fatalf("malformed diagnostic exposed protected input: %s", agentProtocolDiagnosticsText(read.Diagnostics))
		}
	}

	corruptPrivate, _ := json.Marshal(map[string][]byte{projectMetadataJSONProvenancePrivateKey: []byte(`{"version":1,"initialized":true,"configured":true,"terraform_owned":["/wrong"],"api_owned":[],"pending_terraform_owned":[],"pending_api_owned":[],"pending_removals":[]}`)})
	before := requests.Load()
	corrupt, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: state, Private: corruptPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(corrupt.Diagnostics) || requests.Load() != before {
		t.Fatalf("corrupt private reached HTTP: err=%v diagnostics=%s requests=%d/%d", err, agentProtocolDiagnosticsText(corrupt.Diagnostics), requests.Load(), before)
	}

	resource := &ProjectResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	model := ProjectResourceModel{ID: types.StringValue(id), TeamID: types.StringValue(teamID), MetadataJSON: types.StringValue(`{"` + secret + `":true}`)}
	original := model
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := resource.readProjectWithOwnership(canceled, &model, false, projectSemanticOwnership{provenance: prepared.provenance, fresh: true}); err == nil {
		t.Fatal("canceled transactional read succeeded")
	}
	if !model.ID.Equal(original.ID) || !model.TeamID.Equal(original.TeamID) || !model.MetadataJSON.Equal(original.MetadataJSON) {
		t.Fatalf("canceled read partially mutated state: %#v", model)
	}
}

func TestProjectSemanticConfiguredBudgetCreateRecoveryProtocol(t *testing.T) {
	ctx := context.Background()
	const teamID, budgetID = "team-shared-recovery", "budget-shared-recovery"
	var projectID string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/project/new":
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			projectID, _ = body["project_id"].(string)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"project_id": "wrong-response-identity"})
		case request.Method == http.MethodGet && request.URL.Path == "/project/info":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"project_id": projectID, "team_id": teamID, "budget_id": budgetID, "models": []interface{}{}, "metadata": map[string]interface{}{}, "blocked": false,
				"litellm_budget_table": map[string]interface{}{"budget_id": budgetID},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	values := map[string]interface{}{"team_id": teamID, "budget_id": budgetID}
	config, planned := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, values, nullState, keySemanticCreateProposed(t, schema, values), nil)
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) || !protocolPrivateHasKey(t, created.Private, projectAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("shared-budget recovery create: err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(created.Diagnostics), created.Private)
	}
	var recoveredBudgetID string
	if err := protocolAttributeMap(t, schema, created.NewState)["budget_id"].As(&recoveredBudgetID); err != nil || recoveredBudgetID != budgetID {
		t.Fatalf("retained shared budget_id=%q err=%v", recoveredBudgetID, err)
	}

	refreshed, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: created.NewState, Private: created.Private})
	if readErr != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) || protocolPrivateHasKey(t, refreshed.Private, projectAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("shared-budget recovery read: err=%v diagnostics=%s private=%s", readErr, agentProtocolDiagnosticsText(refreshed.Diagnostics), refreshed.Private)
	}
	if err := protocolAttributeMap(t, schema, refreshed.NewState)["budget_id"].As(&recoveredBudgetID); err != nil || recoveredBudgetID != budgetID {
		t.Fatalf("confirmed shared budget_id=%q err=%v", recoveredBudgetID, err)
	}
	proposed := organizationProjectProtocolReplace(t, schema, refreshed.NewState, map[string]interface{}{"budget_id": budgetID})
	_, retryPlan := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, values, refreshed.NewState, proposed, refreshed.Private)
	if accessGroupProtocolDiagnosticsHaveError(retryPlan.Diagnostics) {
		t.Fatalf("shared-budget recovery did not converge: %s", agentProtocolDiagnosticsText(retryPlan.Diagnostics))
	}
}

func TestProjectSemanticLegacyCreateBudgetResetRecoveryProtocol(t *testing.T) {
	ctx := context.Background()
	const teamID, budgetID = "team-legacy-reset", "budget-legacy-reset"
	var projectID string
	var projectCreates, projectUpdates, budgetUpdates, reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		project := func() map[string]interface{} {
			return map[string]interface{}{
				"project_id": projectID, "team_id": teamID, "models": []interface{}{}, "metadata": map[string]interface{}{}, "blocked": false,
				"budget_id": budgetID, "litellm_budget_table": map[string]interface{}{"budget_id": budgetID, "budget_duration": "30d"},
			}
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/project/new":
			projectCreates.Add(1)
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			projectID, _ = body["project_id"].(string)
			_ = json.NewEncoder(writer).Encode(project())
		case request.Method == http.MethodPost && request.URL.Path == "/project/update":
			projectUpdates.Add(1)
			_ = json.NewEncoder(writer).Encode(project())
		case request.Method == http.MethodPost && request.URL.Path == "/budget/update":
			if budgetUpdates.Add(1) == 1 {
				http.Error(writer, `{"error":"uncertain"}`, http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"budget_id": budgetID})
		case request.Method == http.MethodGet && request.URL.Path == "/project/info":
			reads.Add(1)
			_ = json.NewEncoder(writer).Encode(project())
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	values := map[string]interface{}{"team_id": teamID, "budget_duration": "30d"}
	config, planned := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, values, nullState, keySemanticCreateProposed(t, schema, values), nil)
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) || projectCreates.Load() != 1 || budgetUpdates.Load() != 1 || !protocolPrivateHasKey(t, created.Private, projectAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("legacy reset failure: err=%v diagnostics=%s creates=%d budget=%d private=%s", err, agentProtocolDiagnosticsText(created.Diagnostics), projectCreates.Load(), budgetUpdates.Load(), created.Private)
	}
	attributes := protocolAttributeMap(t, schema, created.NewState)
	if !attributes["budget_duration"].IsNull() || !attributes["metadata_json"].IsNull() {
		t.Fatalf("legacy recovery published completed values: duration=%s json=%s", attributes["budget_duration"], attributes["metadata_json"])
	}

	refreshed, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: created.NewState, Private: created.Private})
	if readErr != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) || protocolPrivateHasKey(t, refreshed.Private, projectAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("legacy recovery read: err=%v diagnostics=%s private=%s", readErr, agentProtocolDiagnosticsText(refreshed.Diagnostics), refreshed.Private)
	}
	if !protocolAttributeMap(t, schema, refreshed.NewState)["budget_duration"].IsNull() {
		t.Fatal("legacy recovery adopted duration and lost reset retry")
	}

	proposed := organizationProjectProtocolReplace(t, schema, refreshed.NewState, map[string]interface{}{"budget_duration": "30d"})
	retryConfig, retryPlan := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, values, refreshed.NewState, proposed, refreshed.Private)
	if accessGroupProtocolDiagnosticsHaveError(retryPlan.Diagnostics) {
		t.Fatalf("legacy retry plan diagnostics=%s", agentProtocolDiagnosticsText(retryPlan.Diagnostics))
	}
	retried, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: retryConfig, PriorState: refreshed.NewState, PlannedState: retryPlan.PlannedState, PlannedPrivate: retryPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(retried.Diagnostics) || projectUpdates.Load() != 1 || budgetUpdates.Load() != 2 {
		t.Fatalf("legacy retry: err=%v diagnostics=%s project=%d budget=%d", err, agentProtocolDiagnosticsText(retried.Diagnostics), projectUpdates.Load(), budgetUpdates.Load())
	}
	var duration string
	if err := protocolAttributeMap(t, schema, retried.NewState)["budget_duration"].As(&duration); err != nil || duration != "30d" {
		t.Fatalf("legacy retry duration=%q err=%v", duration, err)
	}
}

func TestProjectSemanticKnownRejectionRecoveryPrivacyProtocol(t *testing.T) {
	ctx := context.Background()
	const teamID, secret = "team-known-rejection", "known_rejection_secret"
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		http.Error(writer, "rejected", http.StatusBadRequest)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	values := map[string]interface{}{"team_id": teamID, "metadata_json": fmt.Sprintf(`{"%s":"value"}`, secret)}
	config, planned := projectSemanticProtocolPlan(t, ctx, protocolServer, schema, values, nullState, keySemanticCreateProposed(t, schema, values), nil)
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || calls.Load() != 1 || protocolPrivateHasKey(t, applied.Private, projectAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("known rejection: err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics), applied.Private)
	}
	if strings.Contains(agentProtocolDiagnosticsText(applied.Diagnostics), secret) || strings.Contains(agentProtocolDiagnosticsText(applied.Diagnostics), teamID) || bytes.Contains(applied.Private, []byte(secret)) {
		t.Fatal("known rejection exposed protected values")
	}
}
