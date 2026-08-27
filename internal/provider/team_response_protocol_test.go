package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestTeamProtocolCreateReadUpdateImportAndNoDrift(t *testing.T) {
	ctx := context.Background()
	var mutex sync.Mutex
	alias := "created"
	malformed := false
	posts := map[string]int{}
	var lastUpdateMetadata map[string]interface{}
	var createPermissionsPresent bool
	storedPermissions := []interface{}{"baseline"}

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/team/new", "/team/update":
			var body map[string]interface{}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&body); err != nil {
				http.Error(writer, "bad request", http.StatusBadRequest)
				return
			}
			mutex.Lock()
			posts[request.URL.Path]++
			if value, ok := body["team_alias"].(string); ok {
				alias = value
				malformed = value == "malformed"
			}
			if request.URL.Path == "/team/new" {
				if permissions, ok := body["team_member_permissions"].([]interface{}); ok {
					createPermissionsPresent = true
					storedPermissions = permissions
				}
			}
			if request.URL.Path == "/team/update" {
				if metadata, ok := body["metadata"].(map[string]interface{}); ok {
					lastUpdateMetadata = metadata
				}
			}
			mutex.Unlock()
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": "team-protocol"})
		case "/team/info":
			mutex.Lock()
			currentAlias, bad := alias, malformed
			mutex.Unlock()
			metadata := interface{}(map[string]interface{}{
				"external": map[string]interface{}{"owner": "keep"},
				"tags":     []interface{}{}, "guardrails": nil, "prompts": []interface{}{},
				"model_rpm_limit": map[string]interface{}{}, "model_tpm_limit": map[string]interface{}{},
			})
			if bad {
				metadata = []interface{}{"malformed"}
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"team_id":          "team-protocol",
				"keys":             []interface{}{},
				"team_memberships": []interface{}{},
				"team_info": map[string]interface{}{
					"team_id": "team-protocol", "team_alias": currentAlias,
					"models": []interface{}{}, "access_group_ids": []interface{}{}, "blocked": false,
					"metadata":                 metadata,
					"litellm_model_table":      map[string]interface{}{"model_aliases": map[string]interface{}{}},
					"team_member_budget_table": nil,
				},
			})
		case "/team/permissions_list":
			mutex.Lock()
			permissions := make([]interface{}, len(storedPermissions))
			copy(permissions, storedPermissions)
			mutex.Unlock()
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": request.URL.Query().Get("team_id"), "team_member_permissions": permissions})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_team"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"team_id": "team-protocol", "team_alias": "created", "team_member_permissions": []tftypes.Value{}}
	computed := map[string]interface{}{
		"id": tftypes.UnknownValue, "access_group_ids": tftypes.UnknownValue, "metadata": tftypes.UnknownValue,
		"models": tftypes.UnknownValue, "model_aliases": tftypes.UnknownValue, "model_rpm_limit": tftypes.UnknownValue,
		"model_tpm_limit": tftypes.UnknownValue, "tags": tftypes.UnknownValue, "guardrails": tftypes.UnknownValue,
		"prompts": tftypes.UnknownValue, "blocked": tftypes.UnknownValue,
	}
	proposedValues := map[string]interface{}{}
	for key, value := range configValues {
		proposedValues[key] = value
	}
	for key, value := range computed {
		proposedValues[key] = value
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))

	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("create apply: err=%v diagnostics=%v", err, created.Diagnostics)
	}
	createdAttributes := protocolAttributeMap(t, schema, created.NewState)
	if protocolString(t, createdAttributes["id"]) != "team-protocol" || createdAttributes["tags"].IsNull() || !createdAttributes["guardrails"].IsNull() {
		t.Fatalf("created nested projection: id=%v tags=%v guardrails=%v", createdAttributes["id"], createdAttributes["tags"], createdAttributes["guardrails"])
	}

	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: created.NewState, Private: created.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("read: err=%v diagnostics=%v", err, refreshed.Diagnostics)
	}
	noDrift, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: refreshed.NewState, ProposedNewState: refreshed.NewState, PriorPrivate: refreshed.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(noDrift.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, refreshed.NewState, noDrift) != organizationProjectProtocolActionNoOp {
		t.Fatalf("create/read no-drift plan: err=%v diagnostics=%v action=%s", err, noDrift.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, refreshed.NewState, noDrift))
	}

	updatedConfigValues := map[string]interface{}{"team_id": "team-protocol", "team_alias": "updated", "team_member_permissions": []tftypes.Value{}}
	updatedConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, updatedConfigValues))
	updatedProposed := organizationProjectProtocolReplace(t, schema, refreshed.NewState, map[string]interface{}{"team_alias": "updated"})
	updatePlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: updatedConfig, PriorState: refreshed.NewState, ProposedNewState: updatedProposed, PriorPrivate: refreshed.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updatePlan.Diagnostics) {
		t.Fatalf("update plan: err=%v diagnostics=%v", err, updatePlan.Diagnostics)
	}
	updated, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: updatedConfig, PriorState: refreshed.NewState, PlannedState: updatePlan.PlannedState, PlannedPrivate: updatePlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updated.Diagnostics) {
		t.Fatalf("update apply: err=%v diagnostics=%v", err, updated.Diagnostics)
	}
	if got := protocolString(t, protocolAttributeMap(t, schema, updated.NewState)["team_alias"]); got != "updated" {
		t.Fatalf("updated alias = %q", got)
	}
	mutex.Lock()
	unexpectedMetadata := lastUpdateMetadata
	permissionsWereExplicit := createPermissionsPresent
	mutex.Unlock()
	if unexpectedMetadata != nil {
		t.Fatalf("unrelated update sent replacement metadata: %#v", unexpectedMetadata)
	}
	if !permissionsWereExplicit {
		t.Fatal("create omitted explicitly configured empty permissions")
	}

	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "team-protocol"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	importRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importRead.Diagnostics) || protocolPrivateHasKey(t, importRead.Private, numericImportedPrivateKey) {
		t.Fatalf("import read: err=%v diagnostics=%v private=%s", err, importRead.Diagnostics, importRead.Private)
	}
	importConfig := updatedConfig
	importNoDrift, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: importConfig, PriorState: importRead.NewState, ProposedNewState: importRead.NewState, PriorPrivate: importRead.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importNoDrift.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, importRead.NewState, importNoDrift) != organizationProjectProtocolActionNoOp {
		t.Fatalf("import no-drift plan: err=%v diagnostics=%v action=%s", err, importNoDrift.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, importRead.NewState, importNoDrift))
	}

	malformedConfigValues := map[string]interface{}{"team_id": "team-protocol", "team_alias": "malformed", "team_member_permissions": []tftypes.Value{}}
	malformedConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, malformedConfigValues))
	malformedProposed := organizationProjectProtocolReplace(t, schema, updated.NewState, map[string]interface{}{"team_alias": "malformed"})
	malformedPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: malformedConfig, PriorState: updated.NewState, ProposedNewState: malformedProposed, PriorPrivate: updated.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(malformedPlan.Diagnostics) {
		t.Fatalf("malformed update plan: err=%v diagnostics=%v", err, malformedPlan.Diagnostics)
	}
	failed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: malformedConfig, PriorState: updated.NewState, PlannedState: malformedPlan.PlannedState, PlannedPrivate: malformedPlan.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) {
		t.Fatalf("malformed update read-back accepted: err=%v diagnostics=%v", err, failed.Diagnostics)
	}
	priorValue, _ := updated.NewState.Unmarshal(schema.ValueType())
	failedValue, _ := failed.NewState.Unmarshal(schema.ValueType())
	if !priorValue.Equal(failedValue) {
		t.Fatalf("malformed update published requested state: prior=%v failed=%v", priorValue, failedValue)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if posts["/team/new"] != 1 || posts["/team/update"] != 2 {
		t.Fatalf("mutation calls = %#v", posts)
	}
}

func TestTeamProtocolCommittedCreateRetainsOnlyIdentityWhenReadbackFails(t *testing.T) {
	ctx := context.Background()
	var posts int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/team/new":
			posts++
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": "team-recovery"})
		case "/team/info":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"team_id":          "team-recovery",
				"keys":             []interface{}{},
				"team_memberships": []interface{}{},
				"team_info": map[string]interface{}{
					"team_id": "team-recovery", "team_alias": "created", "metadata": []interface{}{"malformed"},
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_team"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"team_id": "team-recovery", "team_alias": "created"}
	proposedValues := map[string]interface{}{}
	for key, value := range configValues {
		proposedValues[key] = value
	}
	for _, key := range []string{"id", "access_group_ids", "metadata", "models", "model_aliases", "model_rpm_limit", "model_tpm_limit", "tags", "guardrails", "prompts", "blocked", "team_member_permissions"} {
		proposedValues[key] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("unconfirmed create did not fail: err=%v diagnostics=%v", err, created.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schema, created.NewState)
	if protocolString(t, attributes["id"]) != "team-recovery" || protocolString(t, attributes["team_id"]) != "team-recovery" {
		t.Fatalf("confirmed recovery identity was not retained: id=%v team_id=%v", attributes["id"], attributes["team_id"])
	}
	if !attributes["team_alias"].IsNull() || posts != 1 {
		t.Fatalf("planned values leaked into recovery state or create repeated: alias=%v posts=%d", attributes["team_alias"], posts)
	}
}
