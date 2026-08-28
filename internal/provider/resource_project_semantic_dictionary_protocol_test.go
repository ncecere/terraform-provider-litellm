package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func projectSemanticConfig(t *testing.T, schema *tfprotov6.Schema, values map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	return accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
}

func projectSemanticRemote(id string, metadata interface{}) map[string]interface{} {
	return map[string]interface{}{
		"project_id": id, "team_id": "team-semantic", "models": []interface{}{}, "metadata": metadata, "blocked": false,
		"litellm_budget_table": map[string]interface{}{},
	}
}

func TestProjectSemanticSchemaAndDirectUpgradeProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	if schema.Version != 1 {
		t.Fatalf("schema version=%d want=1", schema.Version)
	}
	raw, _ := json.Marshal(map[string]interface{}{
		"id": "project-upgrade", "team_id": "team-preserved", "metadata": map[string]interface{}{"legacy": "preserved"},
	})
	upgraded, err := protocolServer.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{TypeName: "litellm_project", Version: 0, RawState: &tfprotov6.RawState{JSON: raw}})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(upgraded.Diagnostics) || upgraded.UpgradedState == nil {
		t.Fatalf("upgrade: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(upgraded.Diagnostics))
	}
	attributes := protocolAttributeMap(t, schema, upgraded.UpgradedState)
	if !attributes["metadata_json"].IsNull() {
		t.Fatalf("upgrade adopted metadata_json: %s", attributes["metadata_json"])
	}
	var team string
	if err := attributes["team_id"].As(&team); err != nil || team != "team-preserved" {
		t.Fatalf("team=%q err=%v", team, err)
	}
}

func TestProjectSemanticValidationProtocol(t *testing.T) {
	ctx := context.Background()
	var mutations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		mutations.Add(1)
		http.Error(writer, "unexpected", http.StatusInternalServerError)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	for name, values := range map[string]map[string]interface{}{
		"duplicate":      {"team_id": "team-semantic", "metadata_json": `{"a":1,"a":2}`},
		"nonobject":      {"team_id": "team-semantic", "metadata_json": `[1]`},
		"legacy overlap": {"team_id": "team-semantic", "metadata": map[string]tftypes.Value{"same": tftypes.NewValue(tftypes.String, "legacy")}, "metadata_json": `{"same":true}`},
		"reserved tags":  {"team_id": "team-semantic", "metadata_json": `{"tags":[]}`},
		"reserved rpm":   {"team_id": "team-semantic", "metadata_json": `{"model_rpm_limit":{}}`},
		"reserved tpm":   {"team_id": "team-semantic", "metadata_json": `{"model_tpm_limit":{}}`},
	} {
		t.Run(name, func(t *testing.T) {
			config := projectSemanticConfig(t, schema, values)
			proposed := keySemanticCreateProposed(t, schema, values)
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: nullState, ProposedNewState: proposed})
			if err != nil {
				t.Fatal(err)
			}
			if accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				return
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("unsafe create accepted: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
			}
		})
	}
	if mutations.Load() != 0 {
		t.Fatalf("validation dispatched %d mutations", mutations.Load())
	}
}

func TestProjectSemanticGeneratedIdentityCreateAndNonAdoptingRecoveryProtocol(t *testing.T) {
	ctx := context.Background()
	var createBody map[string]interface{}
	var malformed atomic.Bool
	malformed.Store(true)
	remoteMetadata := map[string]interface{}{"owned": true, "api_sibling": map[string]interface{}{"private": "value"}}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/project/new":
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&createBody); err != nil {
				t.Errorf("decode create: %v", err)
			}
			if malformed.Load() {
				_, _ = writer.Write([]byte(`{"project_id":`))
				return
			}
			_ = json.NewEncoder(writer).Encode(projectSemanticRemote(createBody["project_id"].(string), remoteMetadata))
		case request.Method == http.MethodGet && request.URL.Path == "/project/info":
			_ = json.NewEncoder(writer).Encode(projectSemanticRemote(createBody["project_id"].(string), remoteMetadata))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	values := map[string]interface{}{"team_id": "team-semantic", "metadata_json": `{"owned":true}`}
	config := projectSemanticConfig(t, schema, values)
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: nullState, ProposedNewState: keySemanticCreateProposed(t, schema, values)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || applied.NewState == nil {
		t.Fatalf("accepted malformed create: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	generated, ok := createBody["project_id"].(string)
	if !ok || uuid.Validate(generated) != nil {
		t.Fatalf("generated project_id=%#v", createBody["project_id"])
	}
	metadata := createBody["metadata"].(map[string]interface{})
	if metadata["owned"] != true {
		t.Fatalf("create metadata=%#v", metadata)
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	var stateID string
	if err := attributes["id"].As(&stateID); err != nil || stateID != generated || !attributes["metadata_json"].IsNull() || !protocolPrivateHasKey(t, applied.Private, projectAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("recovery identity=%q metadata_json=%v private=%s err=%v", stateID, attributes["metadata_json"], applied.Private, err)
	}

	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: applied.NewState, Private: applied.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("recovery read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	attributes = protocolAttributeMap(t, schema, read.NewState)
	if !attributes["metadata_json"].IsNull() || protocolPrivateHasKey(t, read.Private, projectAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("recovery adopted semantic value or retained marker: metadata_json=%v private=%s", attributes["metadata_json"], read.Private)
	}
	malformed.Store(false)
	secondRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_project", CurrentState: read.NewState, Private: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(secondRead.Diagnostics) || !protocolAttributeMap(t, schema, secondRead.NewState)["metadata_json"].IsNull() {
		t.Fatalf("repeated read adopted semantic value: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(secondRead.Diagnostics))
	}
}

func TestProjectSemanticKnownCreateRejectionPublishesNoRecoveryProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "rejected", http.StatusBadRequest)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_project"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	values := map[string]interface{}{"team_id": "team-semantic", "metadata_json": `{"owned":true}`}
	config := projectSemanticConfig(t, schema, values)
	planned, _ := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: nullState, ProposedNewState: keySemanticCreateProposed(t, schema, values)})
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_project", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || protocolPrivateHasKey(t, applied.Private, projectAcceptedCreateRecoveryPrivateKey) {
		t.Fatalf("known rejection recovery: err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics), applied.Private)
	}
}
