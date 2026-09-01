package provider

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func teamSemanticProtocolValue(t *testing.T, schema *tfprotov6.Schema, values map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	return accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
}

func teamSemanticCreateProposed(t *testing.T, schema *tfprotov6.Schema, values map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	proposed := map[string]interface{}{}
	for name, value := range values {
		proposed[name] = value
	}
	for _, name := range []string{"id", "team_id", "access_group_ids", "metadata", "metadata_json", "models", "model_aliases", "model_rpm_limit", "model_tpm_limit", "tags", "guardrails", "prompts", "blocked", "team_member_permissions"} {
		if _, configured := proposed[name]; !configured {
			proposed[name] = tftypes.UnknownValue
		}
	}
	return teamSemanticProtocolValue(t, schema, proposed)
}

func TestTeamSemanticSchemaAndDirectUpgradeProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_team"]
	if schema.Version != 2 {
		t.Fatalf("schema version=%d want=2", schema.Version)
	}
	raw, _ := json.Marshal(map[string]interface{}{"id": "team-upgrade", "team_id": "team-upgrade", "team_alias": "alias", "metadata": map[string]interface{}{"legacy": "keep"}})
	upgraded, err := protocolServer.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{TypeName: "litellm_team", Version: 0, RawState: &tfprotov6.RawState{JSON: raw}})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(upgraded.Diagnostics) || upgraded.UpgradedState == nil {
		t.Fatalf("upgrade err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(upgraded.Diagnostics))
	}
	attributes := protocolAttributeMap(t, schema, upgraded.UpgradedState)
	if !attributes["metadata_json"].IsNull() {
		t.Fatalf("metadata_json=%s", attributes["metadata_json"])
	}
}

func TestTeamSemanticExactCreateUpdateHydrationAndFormattingProtocol(t *testing.T) {
	ctx := context.Background()
	const id = "team-semantic-protocol"
	var metadata map[string]interface{}
	var createBody, updateBody map[string]interface{}
	var creates, updates, infos atomic.Int64
	var alias atomic.Value
	alias.Store("semantic")
	response := func() map[string]interface{} {
		return map[string]interface{}{
			"team_id": id, "keys": []interface{}{}, "team_memberships": []interface{}{},
			"team_info": map[string]interface{}{
				"team_id": id, "team_alias": alias.Load(), "access_group_ids": []interface{}{}, "models": []interface{}{}, "blocked": false,
				"metadata": metadata, "litellm_model_table": map[string]interface{}{"model_aliases": map[string]interface{}{}}, "team_member_budget_table": nil,
			},
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/team/new":
			creates.Add(1)
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&createBody); err != nil {
				t.Errorf("decode create: %v", err)
				return
			}
			if createBody["team_id"] != id {
				t.Errorf("create team_id=%#v", createBody["team_id"])
			}
			metadata, _ = createBody["metadata"].(map[string]interface{})
			metadata["api_sibling"] = map[string]interface{}{"keep": true}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": id})
		case request.Method == http.MethodPost && request.URL.Path == "/team/update":
			updates.Add(1)
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			if err := decoder.Decode(&updateBody); err != nil {
				t.Errorf("decode update: %v", err)
				return
			}
			if replacement, ok := updateBody["metadata"].(map[string]interface{}); ok {
				metadata = replacement
			}
			if updatedAlias, ok := updateBody["team_alias"].(string); ok {
				alias.Store(updatedAlias)
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": id})
		case request.Method == http.MethodGet && request.URL.Path == "/team/info":
			infos.Add(1)
			_ = json.NewEncoder(writer).Encode(response())
		case request.Method == http.MethodGet && request.URL.Path == "/team/permissions_list":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": id, "team_member_permissions": []interface{}{}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_team"]
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	semantic := `{"integer":9007199254740993123456789,"native":true,"nil":null,"list":[1,false,null],"object":{"keep":1,"remove":2},"empty":{}}`
	values := map[string]interface{}{"team_id": id, "team_alias": alias.Load(), "metadata_json": semantic, "team_member_permissions": []tftypes.Value{}}
	config := teamSemanticProtocolValue(t, schema, values)
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_team", Config: config, PriorState: nullState, ProposedNewState: teamSemanticCreateProposed(t, schema, values)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_team", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) || creates.Load() != 1 || infos.Load() != 1 {
		t.Fatalf("create: err=%v diagnostics=%s creates=%d infos=%d", err, agentProtocolDiagnosticsText(created.Diagnostics), creates.Load(), infos.Load())
	}
	if createBody["team_id"] != id || createBody["metadata"].(map[string]interface{})["integer"] != json.Number("9007199254740993123456789") {
		t.Fatalf("create body=%#v", createBody)
	}

	updatedJSON := `{"integer":9007199254740993123456789,"native":true,"nil":null,"list":[1,false,null],"object":{"keep":3},"empty":{}}`
	updateValues := map[string]interface{}{"team_id": id, "team_alias": alias.Load(), "metadata_json": updatedJSON, "team_member_permissions": []tftypes.Value{}}
	updateConfig := teamSemanticProtocolValue(t, schema, updateValues)
	proposed := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{"metadata_json": updatedJSON})
	updatePlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_team", Config: updateConfig, PriorState: created.NewState, ProposedNewState: proposed, PriorPrivate: created.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updatePlan.Diagnostics) {
		t.Fatalf("update plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(updatePlan.Diagnostics))
	}
	before := infos.Load()
	updated, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_team", Config: updateConfig, PriorState: created.NewState, PlannedState: updatePlan.PlannedState, PlannedPrivate: updatePlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(updated.Diagnostics) || updates.Load() != 1 || infos.Load() != before+2 {
		t.Fatalf("update: err=%v diagnostics=%s updates=%d info-delta=%d", err, agentProtocolDiagnosticsText(updated.Diagnostics), updates.Load(), infos.Load()-before)
	}
	replacement := updateBody["metadata"].(map[string]interface{})
	if replacement["api_sibling"].(map[string]interface{})["keep"] != true || replacement["object"].(map[string]interface{})["keep"] != json.Number("3") {
		t.Fatalf("replacement=%#v", replacement)
	}
	if _, present := replacement["object"].(map[string]interface{})["remove"]; present {
		t.Fatalf("removed semantic leaf remained: %#v", replacement)
	}

	formattedJSON := "{\n \"empty\": {}, \"object\": {\"keep\": 3}, \"list\": [1,false,null], \"nil\": null, \"native\": true, \"integer\": 9007199254740993123456789\n}"
	formattedValues := map[string]interface{}{"team_id": id, "team_alias": alias.Load(), "metadata_json": formattedJSON, "team_member_permissions": []tftypes.Value{}}
	formattedConfig := teamSemanticProtocolValue(t, schema, formattedValues)
	formatted, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_team", Config: formattedConfig, PriorState: updated.NewState, ProposedNewState: updated.NewState, PriorPrivate: updated.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(formatted.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, updated.NewState, formatted) != organizationProjectProtocolActionNoOp || updates.Load() != 1 {
		t.Fatalf("format no-op: err=%v diagnostics=%s action=%s", err, agentProtocolDiagnosticsText(formatted.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, updated.NewState, formatted))
	}

	renamedValues := map[string]interface{}{"team_id": id, "team_alias": "semantic-renamed", "metadata_json": formattedJSON, "team_member_permissions": []tftypes.Value{}}
	renamedConfig := teamSemanticProtocolValue(t, schema, renamedValues)
	renamedProposed := organizationProjectProtocolReplace(t, schema, updated.NewState, map[string]interface{}{"team_alias": "semantic-renamed", "metadata_json": formattedJSON})
	renamedPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_team", Config: renamedConfig, PriorState: updated.NewState, ProposedNewState: renamedProposed, PriorPrivate: updated.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(renamedPlan.Diagnostics) {
		t.Fatalf("format plus alias plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(renamedPlan.Diagnostics))
	}
	renamed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_team", Config: renamedConfig, PriorState: updated.NewState, PlannedState: renamedPlan.PlannedState, PlannedPrivate: renamedPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(renamed.Diagnostics) || updates.Load() != 2 {
		t.Fatalf("format plus alias apply: err=%v diagnostics=%s updates=%d", err, agentProtocolDiagnosticsText(renamed.Diagnostics), updates.Load())
	}
	if got := protocolString(t, protocolAttributeMap(t, schema, renamed.NewState)["metadata_json"]); got != updatedJSON {
		t.Fatalf("format-equivalent unrelated update published %q want planned %q", got, updatedJSON)
	}
}

func TestTeamSemanticImportNonAdoptionAndExplicitTakeoverProtocol(t *testing.T) {
	ctx := context.Background()
	const id = "team-semantic-import"
	remote := map[string]interface{}{"native": true, "api": map[string]interface{}{"keep": true}}
	var updates atomic.Int64
	response := func() map[string]interface{} {
		return map[string]interface{}{"team_id": id, "keys": []interface{}{}, "team_memberships": []interface{}{}, "team_info": map[string]interface{}{
			"team_id": id, "team_alias": "imported", "models": []interface{}{}, "metadata": remote,
			"litellm_model_table": map[string]interface{}{"model_aliases": map[string]interface{}{}}, "team_member_budget_table": nil,
		}}
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/team/info":
			_ = json.NewEncoder(writer).Encode(response())
		case request.Method == http.MethodGet && request.URL.Path == "/team/permissions_list":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": id, "team_member_permissions": []interface{}{}})
		case request.Method == http.MethodPost && request.URL.Path == "/team/update":
			updates.Add(1)
			var body map[string]interface{}
			decoder := json.NewDecoder(request.Body)
			decoder.UseNumber()
			_ = decoder.Decode(&body)
			remote = body["metadata"].(map[string]interface{})
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": id})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_team"]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_team", ID: id})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) {
		t.Fatalf("import err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_team", CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) || !protocolAttributeMap(t, schema, read.NewState)["metadata_json"].IsNull() {
		t.Fatalf("import read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	values := map[string]interface{}{"team_id": id, "team_alias": "imported", "metadata_json": `{"native":false}`}
	config := teamSemanticProtocolValue(t, schema, values)
	proposed := organizationProjectProtocolReplace(t, schema, read.NewState, map[string]interface{}{"metadata_json": values["metadata_json"]})
	plan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_team", Config: config, PriorState: read.NewState, ProposedNewState: proposed, PriorPrivate: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(plan.Diagnostics) {
		t.Fatalf("takeover plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(plan.Diagnostics))
	}
	taken, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_team", Config: config, PriorState: read.NewState, PlannedState: plan.PlannedState, PlannedPrivate: plan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(taken.Diagnostics) || updates.Load() != 1 {
		t.Fatalf("takeover err=%v diagnostics=%s updates=%d", err, agentProtocolDiagnosticsText(taken.Diagnostics), updates.Load())
	}
	if remote["native"] != false || remote["api"].(map[string]interface{})["keep"] != true {
		t.Fatalf("remote=%#v", remote)
	}
}

func TestTeamSemanticCreateKnownRejectionAndAcceptedMalformedRecoveryProtocol(t *testing.T) {
	for _, test := range []struct {
		name       string
		status     int
		body       string
		wantState  bool
		wantMarker bool
	}{{"post-dispatch non-2xx", http.StatusBadRequest, `{"error":"rejected"}`, true, true}, {"accepted malformed", http.StatusOK, `{"team_id":`, true, true}} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_team"]
			nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
			values := map[string]interface{}{"team_id": "team-create-recovery", "team_alias": "alias", "metadata_json": `{"secret":true}`}
			config := teamSemanticProtocolValue(t, schema, values)
			plan, _ := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_team", Config: config, PriorState: nullState, ProposedNewState: teamSemanticCreateProposed(t, schema, values)})
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_team", Config: config, PriorState: nullState, PlannedState: plan.PlannedState, PlannedPrivate: plan.PlannedPrivate})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("apply err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
			}
			if protocolPrivateHasKey(t, applied.Private, teamAcceptedCreateRecoveryPrivateKey) != test.wantMarker {
				t.Fatalf("private=%s", applied.Private)
			}
			if test.wantState {
				attributes := protocolAttributeMap(t, schema, applied.NewState)
				if protocolString(t, attributes["id"]) != "team-create-recovery" || !attributes["metadata_json"].IsNull() || !attributes["metadata"].IsNull() {
					t.Fatalf("recovery state=%v", attributes)
				}
			} else if applied.NewState != nil {
				value, _ := applied.NewState.Unmarshal(schema.ValueType())
				if !value.IsNull() {
					t.Fatalf("definitive rejection published state: %v", value)
				}
			}
		})
	}
}

func teamSemanticCompleteResponse(id, alias string, metadata interface{}, memberBudget interface{}) map[string]interface{} {
	return map[string]interface{}{
		"team_id": id, "keys": []interface{}{}, "team_memberships": []interface{}{},
		"team_info": map[string]interface{}{
			"team_id": id, "team_alias": alias, "access_group_ids": []interface{}{}, "models": []interface{}{}, "blocked": false,
			"metadata": metadata, "litellm_model_table": map[string]interface{}{"model_aliases": map[string]interface{}{}}, "team_member_budget_table": memberBudget,
		},
	}
}

func protocolTeamMemberBudget(t *testing.T, schema *tfprotov6.Schema, state *tfprotov6.DynamicValue) *float64 {
	t.Helper()
	value := protocolAttributeMap(t, schema, state)["team_member_budget"]
	if value.IsNull() {
		return nil
	}
	var number big.Float
	if err := value.As(&number); err != nil {
		t.Fatalf("team_member_budget decode: %v", err)
	}
	result, _ := number.Float64()
	return &result
}

func TestTeamMemberDefaultsPartialCommitRecoveryProtocol(t *testing.T) {
	for _, test := range []struct {
		name         string
		clear        bool
		want         interface{}
		responseLoss bool
	}{
		{name: "non-null partial commit before main failure", want: 20.0},
		{name: "clear commit before response failure", clear: true, want: nil, responseLoss: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			const id, alias = "team-member-recovery", "member-recovery"
			remoteBudget := 10.0
			hasRemoteBudget := true
			var updateCalls atomic.Int64
			failedOnce := false
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch {
				case request.Method == http.MethodPost && request.URL.Path == "/team/new":
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": id})
				case request.Method == http.MethodPost && request.URL.Path == "/team/update":
					updateCalls.Add(1)
					var body map[string]interface{}
					decoder := json.NewDecoder(request.Body)
					decoder.UseNumber()
					if err := decoder.Decode(&body); err != nil {
						t.Errorf("decode update: %v", err)
						return
					}
					if value, present := body["team_member_budget"]; present {
						if value == nil {
							hasRemoteBudget = false
						} else {
							number, ok := value.(json.Number)
							if !ok {
								t.Errorf("member budget wire type=%T", value)
								return
							}
							parsed, parseErr := number.Float64()
							if parseErr != nil {
								t.Errorf("member budget parse: %v", parseErr)
								return
							}
							remoteBudget, hasRemoteBudget = parsed, true
						}
						if !failedOnce {
							failedOnce = true
							if test.responseLoss {
								writer.Header().Set("Content-Length", "100")
								writer.WriteHeader(http.StatusOK)
								_, _ = writer.Write([]byte("{}"))
								return
							}
							http.Error(writer, "omitted", http.StatusInternalServerError)
							return
						}
					}
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": id})
				case request.Method == http.MethodGet && request.URL.Path == "/team/info":
					var budget interface{}
					if hasRemoteBudget {
						budget = map[string]interface{}{"max_budget": remoteBudget}
					}
					_ = json.NewEncoder(writer).Encode(teamSemanticCompleteResponse(id, alias, map[string]interface{}{}, budget))
				case request.Method == http.MethodGet && request.URL.Path == "/team/permissions_list":
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": id, "team_member_permissions": []interface{}{}})
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_team"]
			nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
			createValues := map[string]interface{}{"team_id": id, "team_alias": alias, "team_member_budget": 10.0, "team_member_permissions": []tftypes.Value{}}
			createConfig := teamSemanticProtocolValue(t, schema, createValues)
			createPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_team", Config: createConfig, PriorState: nullState, ProposedNewState: teamSemanticCreateProposed(t, schema, createValues)})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(createPlan.Diagnostics) {
				t.Fatalf("create plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(createPlan.Diagnostics))
			}
			created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_team", Config: createConfig, PriorState: nullState, PlannedState: createPlan.PlannedState, PlannedPrivate: createPlan.PlannedPrivate})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
				t.Fatalf("create err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(created.Diagnostics))
			}

			updateValues := map[string]interface{}{"team_id": id, "team_alias": alias, "team_member_permissions": []tftypes.Value{}}
			if !test.clear {
				updateValues["team_member_budget"] = test.want
			}
			updateConfig := teamSemanticProtocolValue(t, schema, updateValues)
			proposed := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{"team_member_budget": test.want})
			updatePlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_team", Config: updateConfig, PriorState: created.NewState, ProposedNewState: proposed, PriorPrivate: created.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(updatePlan.Diagnostics) {
				t.Fatalf("update plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(updatePlan.Diagnostics))
			}
			failed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_team", Config: updateConfig, PriorState: created.NewState, PlannedState: updatePlan.PlannedState, PlannedPrivate: updatePlan.PlannedPrivate})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) || !protocolPrivateHasKey(t, failed.Private, teamPendingMemberDefaultsPrivateKey) {
				t.Fatalf("failed update err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(failed.Diagnostics), failed.Private)
			}
			if got := protocolTeamMemberBudget(t, schema, failed.NewState); got == nil || *got != 10 {
				t.Fatalf("failed update published budget=%v", got)
			}

			reconciled, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_team", CurrentState: failed.NewState, Private: failed.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(reconciled.Diagnostics) || protocolPrivateHasKey(t, reconciled.Private, teamPendingMemberDefaultsPrivateKey) {
				t.Fatalf("reconcile err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(reconciled.Diagnostics), reconciled.Private)
			}
			if got := protocolTeamMemberBudget(t, schema, reconciled.NewState); got == nil || *got != 10 {
				t.Fatalf("reconcile did not restore prior budget: %v", got)
			}

			retryProposed := organizationProjectProtocolReplace(t, schema, reconciled.NewState, map[string]interface{}{"team_member_budget": test.want})
			retryPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_team", Config: updateConfig, PriorState: reconciled.NewState, ProposedNewState: retryProposed, PriorPrivate: reconciled.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(retryPlan.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, reconciled.NewState, retryPlan) == organizationProjectProtocolActionNoOp {
				t.Fatalf("retry plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(retryPlan.Diagnostics))
			}
			retried, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_team", Config: updateConfig, PriorState: reconciled.NewState, PlannedState: retryPlan.PlannedState, PlannedPrivate: retryPlan.PlannedPrivate})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(retried.Diagnostics) || protocolPrivateHasKey(t, retried.Private, teamPendingMemberDefaultsPrivateKey) {
				t.Fatalf("retry err=%v diagnostics=%s private=%s", err, agentProtocolDiagnosticsText(retried.Diagnostics), retried.Private)
			}
			got := protocolTeamMemberBudget(t, schema, retried.NewState)
			if test.clear {
				if got != nil {
					t.Fatalf("retry budget=%v want null", *got)
				}
			} else if got == nil || *got != 20 {
				t.Fatalf("retry budget=%v want 20", got)
			}
			if updateCalls.Load() < 2 {
				t.Fatalf("update calls=%d want retry", updateCalls.Load())
			}
		})
	}
}

func TestTeamCreateRecoveryWithoutSemanticConfigurationProtocol(t *testing.T) {
	for _, behavior := range []string{"accepted malformed", "dispatched transport loss", "post-dispatch non-2xx"} {
		t.Run(behavior, func(t *testing.T) {
			ctx := context.Background()
			var requestedID atomic.Value
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method != http.MethodPost || request.URL.Path != "/team/new" {
					http.NotFound(writer, request)
					return
				}
				var body map[string]interface{}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode create: %v", err)
					return
				}
				requestedID.Store(body["team_id"])
				switch behavior {
				case "accepted malformed":
					writer.Header().Set("Content-Type", "application/json")
					_, _ = writer.Write([]byte(`{"team_id":`))
				case "dispatched transport loss":
					connection, _, err := writer.(http.Hijacker).Hijack()
					if err != nil {
						t.Errorf("hijack: %v", err)
						return
					}
					_ = connection.Close()
				case "post-dispatch non-2xx":
					http.Error(writer, "omitted", http.StatusBadRequest)
				}
			}))
			defer server.Close()

			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_team"]
			nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
			values := map[string]interface{}{"team_alias": "generated"}
			config := teamSemanticProtocolValue(t, schema, values)
			plan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_team", Config: config, PriorState: nullState, ProposedNewState: teamSemanticCreateProposed(t, schema, values)})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(plan.Diagnostics) {
				t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(plan.Diagnostics))
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_team", Config: config, PriorState: nullState, PlannedState: plan.PlannedState, PlannedPrivate: plan.PlannedPrivate})
			createdID, _ := requestedID.Load().(string)
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || createdID == "" {
				t.Fatalf("apply err=%v diagnostics=%s requested-id-empty=%t", err, agentProtocolDiagnosticsText(applied.Diagnostics), createdID == "")
			}
			attributes := protocolAttributeMap(t, schema, applied.NewState)
			if protocolString(t, attributes["id"]) != createdID || protocolString(t, attributes["team_id"]) != createdID {
				t.Fatalf("recovery identity does not match generated request identity")
			}
			if !attributes["team_alias"].IsNull() || !attributes["metadata"].IsNull() || !attributes["metadata_json"].IsNull() {
				t.Fatalf("recovery published legacy or semantic values: alias=%s metadata=%s metadata_json=%s", attributes["team_alias"], attributes["metadata"], attributes["metadata_json"])
			}
			if !protocolPrivateHasKey(t, applied.Private, teamAcceptedCreateRecoveryPrivateKey) || !protocolPrivateHasKey(t, applied.Private, teamMetadataJSONProvenancePrivateKey) {
				t.Fatalf("recovery private state=%s", applied.Private)
			}
			unconfirmed, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_team", CurrentState: applied.NewState, Private: applied.Private})
			if readErr != nil || !accessGroupProtocolDiagnosticsHaveError(unconfirmed.Diagnostics) || !protocolPrivateHasKey(t, unconfirmed.Private, teamAcceptedCreateRecoveryPrivateKey) {
				t.Fatalf("transient absence discarded create recovery: err=%v diagnostics=%s private=%s", readErr, agentProtocolDiagnosticsText(unconfirmed.Diagnostics), unconfirmed.Private)
			}
			before, _ := applied.NewState.Unmarshal(schema.ValueType())
			after, _ := unconfirmed.NewState.Unmarshal(schema.ValueType())
			if !before.Equal(after) {
				t.Fatal("transient absence changed accepted-create recovery state")
			}
		})
	}
}

func TestTeamMutationRequiresCompleteWrappedMetadataAuthorityProtocol(t *testing.T) {
	for _, mode := range []string{"flat", "wrapped omitted metadata", "complete wrapped"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			const id, alias = "team-exact-mutation", "exact-mutation"
			remote := map[string]interface{}{"owned": json.Number("1"), "api_sibling": map[string]interface{}{"keep": true}}
			responseMode := "complete wrapped"
			var updates atomic.Int64
			var updateBody map[string]interface{}
			response := func() map[string]interface{} {
				wrapped := teamSemanticCompleteResponse(id, alias, remote, nil)
				teamInfo := wrapped["team_info"].(map[string]interface{})
				switch responseMode {
				case "flat":
					return teamInfo
				case "wrapped omitted metadata":
					delete(teamInfo, "metadata")
				}
				return wrapped
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch {
				case request.Method == http.MethodGet && request.URL.Path == "/team/info":
					_ = json.NewEncoder(writer).Encode(response())
				case request.Method == http.MethodGet && request.URL.Path == "/team/permissions_list":
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": id, "team_member_permissions": []interface{}{}})
				case request.Method == http.MethodPost && request.URL.Path == "/team/update":
					updates.Add(1)
					decoder := json.NewDecoder(request.Body)
					decoder.UseNumber()
					if err := decoder.Decode(&updateBody); err != nil {
						t.Errorf("decode update: %v", err)
						return
					}
					if metadata, ok := updateBody["metadata"].(map[string]interface{}); ok {
						remote = metadata
					}
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": id})
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_team"]
			imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_team", ID: id})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) {
				t.Fatalf("import err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
			}
			initial, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_team", CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(initial.Diagnostics) {
				t.Fatalf("initial read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(initial.Diagnostics))
			}
			responseMode = mode
			desired := `{"owned":2}`
			values := map[string]interface{}{"team_id": id, "team_alias": alias, "metadata_json": desired, "team_member_permissions": []tftypes.Value{}}
			config := teamSemanticProtocolValue(t, schema, values)
			proposed := organizationProjectProtocolReplace(t, schema, initial.NewState, map[string]interface{}{"metadata_json": desired})
			plan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_team", Config: config, PriorState: initial.NewState, ProposedNewState: proposed, PriorPrivate: initial.Private})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(plan.Diagnostics) {
				t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(plan.Diagnostics))
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_team", Config: config, PriorState: initial.NewState, PlannedState: plan.PlannedState, PlannedPrivate: plan.PlannedPrivate})
			if err != nil {
				t.Fatalf("apply err=%v", err)
			}
			if mode != "complete wrapped" {
				if !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || updates.Load() != 0 {
					t.Fatalf("unsafe authority diagnostics=%s updates=%d", agentProtocolDiagnosticsText(applied.Diagnostics), updates.Load())
				}
				if !protocolAttributeMap(t, schema, applied.NewState)["metadata_json"].IsNull() || remote["api_sibling"].(map[string]interface{})["keep"] != true {
					t.Fatalf("unsafe authority changed prior state or remote siblings")
				}
				return
			}
			if accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || updates.Load() != 1 {
				t.Fatalf("complete authority diagnostics=%s updates=%d", agentProtocolDiagnosticsText(applied.Diagnostics), updates.Load())
			}
			replacement, ok := updateBody["metadata"].(map[string]interface{})
			if !ok || replacement["api_sibling"].(map[string]interface{})["keep"] != true || replacement["owned"] != json.Number("2") {
				t.Fatalf("complete authority replacement=%#v", updateBody["metadata"])
			}
		})
	}
}
