package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// mcpToolsetAssignmentProtocolServer simulates the LiteLLM team surface with
// an API-side object_permission that always carries an unmanaged mcp_servers
// sibling, so every step proves the provider neither clobbers nor adopts it.
type mcpToolsetAssignmentProtocolServer struct {
	mu             sync.Mutex
	remoteToolsets []interface{}
	remoteAlias    string
	mutations      []map[string]interface{}
	malformedInfo  bool
	divergentInfo  []interface{}
}

func (s *mcpToolsetAssignmentProtocolServer) handler(t *testing.T) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/team/new", "/team/update":
			var body map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(writer, "bad request", http.StatusBadRequest)
				return
			}
			s.mu.Lock()
			permission, present := body["object_permission"].(map[string]interface{})
			if present {
				if _, fabricated := permission["mcp_servers"]; fabricated {
					t.Errorf("mutation fabricated the unmanaged mcp_servers sibling: %#v", permission)
				}
				if toolsets, ok := permission["mcp_toolsets"].([]interface{}); ok {
					s.remoteToolsets = toolsets
				}
			}
			if alias, ok := body["team_alias"].(string); ok && alias != "" {
				s.remoteAlias = alias
			}
			if _, tracked := body["object_permission"]; tracked {
				s.mutations = append(s.mutations, permission)
			} else {
				s.mutations = append(s.mutations, nil)
			}
			s.mu.Unlock()
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": "team-toolsets"})
		case "/team/info":
			s.mu.Lock()
			toolsets := make([]interface{}, len(s.remoteToolsets))
			copy(toolsets, s.remoteToolsets)
			if s.divergentInfo != nil {
				toolsets = append([]interface{}{}, s.divergentInfo...)
			}
			alias := s.remoteAlias
			if alias == "" {
				alias = "toolsets"
			}
			malformed := s.malformedInfo
			s.mu.Unlock()
			if malformed {
				_, _ = writer.Write([]byte(`not json`))
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"team_id":          "team-toolsets",
				"keys":             []interface{}{},
				"team_memberships": []interface{}{},
				"team_info": map[string]interface{}{
					"team_id": "team-toolsets", "team_alias": alias,
					"models": []interface{}{}, "access_group_ids": []interface{}{}, "blocked": false,
					"metadata":                 map[string]interface{}{},
					"litellm_model_table":      map[string]interface{}{"model_aliases": map[string]interface{}{}},
					"team_member_budget_table": nil,
					"object_permission": map[string]interface{}{
						"object_permission_id": "permission-id",
						"mcp_servers":          []interface{}{"external-server"},
						"mcp_toolsets":         toolsets,
					},
				},
			})
		case "/team/permissions_list":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"team_id": request.URL.Query().Get("team_id"), "team_member_permissions": []interface{}{}})
		default:
			http.NotFound(writer, request)
		}
	}
}

func (s *mcpToolsetAssignmentProtocolServer) lastMutationToolsets(t *testing.T) (interface{}, bool) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.mutations) == 0 {
		t.Fatal("no mutations recorded")
	}
	permission := s.mutations[len(s.mutations)-1]
	if permission == nil {
		return nil, false
	}
	return permission["mcp_toolsets"], true
}

func mcpToolsetAssignmentSetValue(elements ...string) []tftypes.Value {
	values := make([]tftypes.Value, 0, len(elements))
	for _, element := range elements {
		values = append(values, tftypes.NewValue(tftypes.String, element))
	}
	return values
}

func mcpToolsetAssignmentStateSet(t *testing.T, value tftypes.Value) []string {
	t.Helper()
	if value.IsNull() {
		return nil
	}
	var elements []tftypes.Value
	if err := value.As(&elements); err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(elements))
	for _, element := range elements {
		var text string
		if err := element.As(&text); err != nil {
			t.Fatal(err)
		}
		out = append(out, text)
	}
	return out
}

func TestTeamProtocolMCPToolsetAssignmentLifecycle(t *testing.T) {
	ctx := context.Background()
	backend := &mcpToolsetAssignmentProtocolServer{remoteToolsets: []interface{}{}}
	server := httptest.NewServer(backend.handler(t))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_team"
	schema := schemas.ResourceSchemas[typeName]
	computed := map[string]interface{}{
		"id": tftypes.UnknownValue, "access_group_ids": tftypes.UnknownValue, "metadata": tftypes.UnknownValue,
		"models": tftypes.UnknownValue, "model_aliases": tftypes.UnknownValue, "model_rpm_limit": tftypes.UnknownValue,
		"model_tpm_limit": tftypes.UnknownValue, "tags": tftypes.UnknownValue, "guardrails": tftypes.UnknownValue,
		"prompts": tftypes.UnknownValue, "blocked": tftypes.UnknownValue,
	}
	baseConfig := func(toolsets interface{}) map[string]interface{} {
		values := map[string]interface{}{"team_id": "team-toolsets", "team_alias": "toolsets"}
		if toolsets != nil {
			values["mcp_toolset_ids"] = toolsets
		}
		return values
	}
	apply := func(configValues map[string]interface{}, prior *tfprotov6.DynamicValue, priorPrivate []byte, proposed *tfprotov6.DynamicValue) *tfprotov6.ApplyResourceChangeResponse {
		t.Helper()
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: prior, ProposedNewState: proposed, PriorPrivate: priorPrivate})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: prior, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
			t.Fatalf("apply err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
		}
		return applied
	}

	// Create with a nonempty assignment: the request carries the sorted
	// complete membership, LiteLLM's nonempty team list is the key ceiling.
	createValues := baseConfig(mcpToolsetAssignmentSetValue("toolset-b", "toolset-a"))
	proposedValues := map[string]interface{}{}
	for key, value := range createValues {
		proposedValues[key] = value
	}
	for key, value := range computed {
		proposedValues[key] = value
	}
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	created := apply(createValues, nullState, nil, accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues)))
	if sent, present := backend.lastMutationToolsets(t); !present || !reflect.DeepEqual(sent, []interface{}{"toolset-a", "toolset-b"}) {
		t.Fatalf("create mutation toolsets = %#v present=%v, want sorted complete membership", sent, present)
	}
	if got := mcpToolsetAssignmentStateSet(t, protocolAttributeMap(t, schema, created.NewState)["mcp_toolset_ids"]); !reflect.DeepEqual(got, []string{"toolset-a", "toolset-b"}) {
		t.Fatalf("created assignment state = %#v", got)
	}

	// Configured -> empty: the request pins an explicit empty list, which
	// removes the team ceiling in LiteLLM rather than denying all toolsets.
	emptied := apply(baseConfig(mcpToolsetAssignmentSetValue()), created.NewState, created.Private,
		organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{"mcp_toolset_ids": []tftypes.Value{}}))
	if sent, present := backend.lastMutationToolsets(t); !present || !reflect.DeepEqual(sent, []interface{}{}) {
		t.Fatalf("clear mutation toolsets = %#v present=%v, want explicit empty list", sent, present)
	}
	if got := mcpToolsetAssignmentStateSet(t, protocolAttributeMap(t, schema, emptied.NewState)["mcp_toolset_ids"]); !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("cleared assignment state = %#v", got)
	}

	// Omission releases ownership: the mutation carries no object_permission
	// and the state returns to null without adopting the remote value.
	released := apply(baseConfig(nil), emptied.NewState, emptied.Private,
		organizationProjectProtocolReplace(t, schema, emptied.NewState, map[string]interface{}{"mcp_toolset_ids": nil}))
	if _, present := backend.lastMutationToolsets(t); present {
		t.Fatal("ownership release still sent object_permission")
	}
	if got := protocolAttributeMap(t, schema, released.NewState)["mcp_toolset_ids"]; !got.IsNull() {
		t.Fatalf("released assignment state = %s, want null", got)
	}

	// Re-manage one toolset, then simulate LiteLLM dropping the assignment
	// after the toolset row was deleted externally: the managed read adopts
	// the remote membership so the next plan repairs the drift.
	managed := apply(baseConfig(mcpToolsetAssignmentSetValue("toolset-a")), released.NewState, released.Private,
		organizationProjectProtocolReplace(t, schema, released.NewState, map[string]interface{}{"mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-a")}))
	backend.mu.Lock()
	backend.remoteToolsets = []interface{}{}
	backend.mu.Unlock()
	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: managed.NewState, Private: managed.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(refreshed.Diagnostics))
	}
	if got := mcpToolsetAssignmentStateSet(t, protocolAttributeMap(t, schema, refreshed.NewState)["mcp_toolset_ids"]); !reflect.DeepEqual(got, []string{}) {
		t.Fatalf("stale assignment read = %#v, want emptied remote membership", got)
	}
	repairConfig := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, baseConfig(mcpToolsetAssignmentSetValue("toolset-a"))))
	repairPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: repairConfig, PriorState: refreshed.NewState, ProposedNewState: organizationProjectProtocolReplace(t, schema, refreshed.NewState, map[string]interface{}{"mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-a")}), PriorPrivate: refreshed.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(repairPlan.Diagnostics) {
		t.Fatalf("repair plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(repairPlan.Diagnostics))
	}
	if action := organizationProjectProtocolPlannedAction(t, schema, refreshed.NewState, repairPlan); action != organizationProjectProtocolActionUpdate {
		t.Fatalf("repair plan action = %s, want Update", action)
	}
}

func TestTeamProtocolMCPToolsetAssignmentMalformedReadRetainsPriorState(t *testing.T) {
	ctx := context.Background()
	backend := &mcpToolsetAssignmentProtocolServer{remoteToolsets: []interface{}{"toolset-a"}}
	server := httptest.NewServer(backend.handler(t))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_team"
	schema := schemas.ResourceSchemas[typeName]

	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "team-toolsets"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	importRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importRead.Diagnostics) {
		t.Fatalf("import read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(importRead.Diagnostics))
	}
	if got := mcpToolsetAssignmentStateSet(t, protocolAttributeMap(t, schema, importRead.NewState)["mcp_toolset_ids"]); !reflect.DeepEqual(got, []string{"toolset-a"}) {
		t.Fatalf("imported assignment = %#v, want adopted remote membership", got)
	}

	// A malformed transactional read after an accepted mutation must retain
	// the prior public state instead of publishing the requested values.
	backend.mu.Lock()
	backend.malformedInfo = true
	backend.mu.Unlock()
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"team_id": "team-toolsets", "team_alias": "toolsets", "mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-b")}))
	proposed := organizationProjectProtocolReplace(t, schema, importRead.NewState, map[string]interface{}{"mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-b")})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: importRead.NewState, ProposedNewState: proposed, PriorPrivate: importRead.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	failed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: importRead.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil {
		t.Fatal(err)
	}
	if !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) {
		t.Fatal("malformed transactional read did not error")
	}
	priorValue, _ := importRead.NewState.Unmarshal(schema.ValueType())
	failedValue, _ := failed.NewState.Unmarshal(schema.ValueType())
	if !priorValue.Equal(failedValue) {
		t.Fatalf("malformed read published requested state\nprior: %s\n  got: %s", priorValue, failedValue)
	}
}

// mcpToolsetKeyAssignmentProtocolServer simulates the LiteLLM key surface with
// an API-side object_permission that always carries an unmanaged mcp_servers
// sibling, so every step proves the provider neither clobbers nor adopts it.
type mcpToolsetKeyAssignmentProtocolServer struct {
	mu             sync.Mutex
	remoteToolsets []interface{}
	mutations      []map[string]interface{}
	malformedInfo  bool
	divergentInfo  []interface{}
}

func (s *mcpToolsetKeyAssignmentProtocolServer) handler(t *testing.T) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/key/generate", "/key/update":
			var body map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(writer, "bad request", http.StatusBadRequest)
				return
			}
			s.mu.Lock()
			permission, present := body["object_permission"].(map[string]interface{})
			if present {
				if _, fabricated := permission["mcp_servers"]; fabricated {
					t.Errorf("mutation fabricated the unmanaged mcp_servers sibling: %#v", permission)
				}
				if toolsets, ok := permission["mcp_toolsets"].([]interface{}); ok {
					s.remoteToolsets = toolsets
				}
			}
			if _, tracked := body["object_permission"]; tracked {
				s.mutations = append(s.mutations, permission)
			} else {
				s.mutations = append(s.mutations, nil)
			}
			s.mu.Unlock()
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": "sk-key-toolsets"})
		case "/key/info":
			s.mu.Lock()
			toolsets := make([]interface{}, len(s.remoteToolsets))
			copy(toolsets, s.remoteToolsets)
			if s.divergentInfo != nil {
				toolsets = append([]interface{}{}, s.divergentInfo...)
			}
			malformed := s.malformedInfo
			s.mu.Unlock()
			if malformed {
				_, _ = writer.Write([]byte(`not json`))
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"key": "sk-key-toolsets",
				"info": map[string]interface{}{
					"metadata": map[string]interface{}{}, "config": map[string]interface{}{}, "permissions": map[string]interface{}{},
					"object_permission": map[string]interface{}{
						"object_permission_id": "permission-id",
						"mcp_servers":          []interface{}{"external-server"},
						"mcp_toolsets":         toolsets,
					},
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}
}

func (s *mcpToolsetKeyAssignmentProtocolServer) lastMutationToolsets(t *testing.T) (interface{}, bool) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.mutations) == 0 {
		t.Fatal("no mutations recorded")
	}
	permission := s.mutations[len(s.mutations)-1]
	if permission == nil {
		return nil, false
	}
	return permission["mcp_toolsets"], true
}

// mcpToolsetKeyAssignmentCreate applies a healthy create carrying the given
// assignment and returns the applied response for follow-up steps.
func mcpToolsetKeyAssignmentCreate(t *testing.T, ctx context.Context, protocolServer tfprotov6.ProviderServer, schema *tfprotov6.Schema, toolsets []tftypes.Value) *tfprotov6.ApplyResourceChangeResponse {
	t.Helper()
	const typeName = "litellm_key"
	values := map[string]interface{}{"key": "sk-key-toolsets", "mcp_toolset_ids": toolsets}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: keySemanticCreateProposed(t, schema, values),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("create apply err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	return applied
}

// Assignment-only key plans must survive ModifyPlan: an update, an explicit
// [] clear, and an omission-based ownership release each produce a plan that
// differs from prior state instead of being replaced by it.
func TestKeyProtocolMCPToolsetAssignmentPlansAreNotSuppressed(t *testing.T) {
	ctx := context.Background()
	backend := &mcpToolsetKeyAssignmentProtocolServer{remoteToolsets: []interface{}{}}
	server := httptest.NewServer(backend.handler(t))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_key"
	schema := schemas.ResourceSchemas[typeName]
	created := mcpToolsetKeyAssignmentCreate(t, ctx, protocolServer, schema, mcpToolsetAssignmentSetValue("toolset-a", "toolset-b"))

	plan := func(configValues map[string]interface{}, replacement map[string]interface{}) *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
			TypeName: typeName, Config: config, PriorState: created.NewState,
			ProposedNewState: organizationProjectProtocolReplace(t, schema, created.NewState, replacement),
			PriorPrivate:     created.Private,
		})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
		}
		return planned
	}

	updated := plan(map[string]interface{}{"key": "sk-key-toolsets", "mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-b")},
		map[string]interface{}{"mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-b")})
	if got := mcpToolsetAssignmentStateSet(t, protocolAttributeMap(t, schema, updated.PlannedState)["mcp_toolset_ids"]); !reflect.DeepEqual(got, []string{"toolset-b"}) {
		t.Fatalf("assignment-only update plan = %#v, want [toolset-b]", got)
	}

	cleared := plan(map[string]interface{}{"key": "sk-key-toolsets", "mcp_toolset_ids": mcpToolsetAssignmentSetValue()},
		map[string]interface{}{"mcp_toolset_ids": []tftypes.Value{}})
	if got := mcpToolsetAssignmentStateSet(t, protocolAttributeMap(t, schema, cleared.PlannedState)["mcp_toolset_ids"]); got == nil || len(got) != 0 {
		t.Fatalf("assignment clear plan = %#v, want []", got)
	}

	released := plan(map[string]interface{}{"key": "sk-key-toolsets"},
		map[string]interface{}{"mcp_toolset_ids": nil})
	if got := protocolAttributeMap(t, schema, released.PlannedState)["mcp_toolset_ids"]; !got.IsNull() {
		t.Fatalf("omission release plan = %s, want null", got)
	}

	// Composition: an assignment change planned together with a semantic
	// dictionary change keeps both surfaces in the planned state.
	composed := plan(map[string]interface{}{"key": "sk-key-toolsets", "metadata_json": `{"tier":"gold"}`, "mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-b")},
		map[string]interface{}{"metadata_json": "{\"tier\":\"gold\"}", "mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-b")})
	attributes := protocolAttributeMap(t, schema, composed.PlannedState)
	if got := mcpToolsetAssignmentStateSet(t, attributes["mcp_toolset_ids"]); !reflect.DeepEqual(got, []string{"toolset-b"}) {
		t.Fatalf("composed plan assignment = %#v, want [toolset-b]", got)
	}
	var metadataJSON string
	if err := attributes["metadata_json"].As(&metadataJSON); err != nil || metadataJSON != `{"tier":"gold"}` {
		t.Fatalf("composed plan metadata_json = %q err=%v", metadataJSON, err)
	}
}

// Importing a key adopts the remote assignment membership exactly.
func TestKeyProtocolMCPToolsetAssignmentImportAdoptsRemoteMembership(t *testing.T) {
	ctx := context.Background()
	backend := &mcpToolsetKeyAssignmentProtocolServer{remoteToolsets: []interface{}{"toolset-b", "toolset-a"}}
	server := httptest.NewServer(backend.handler(t))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_key"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "sk-key-toolsets"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	importRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importRead.Diagnostics) {
		t.Fatalf("import read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(importRead.Diagnostics))
	}
	got := mcpToolsetAssignmentStateSet(t, protocolAttributeMap(t, schema, importRead.NewState)["mcp_toolset_ids"])
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"toolset-a", "toolset-b"}) {
		t.Fatalf("imported assignment = %#v, want adopted remote membership", got)
	}
}

// A key assignment mutation is transactional: an accepted update whose
// authoritative readback fails or diverges retains prior state and errors
// instead of publishing unconfirmed assignments.
func TestKeyProtocolMCPToolsetAssignmentUnconfirmedUpdateRetainsPriorState(t *testing.T) {
	ctx := context.Background()
	backend := &mcpToolsetKeyAssignmentProtocolServer{remoteToolsets: []interface{}{}}
	server := httptest.NewServer(backend.handler(t))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_key"
	schema := schemas.ResourceSchemas[typeName]
	created := mcpToolsetKeyAssignmentCreate(t, ctx, protocolServer, schema, mcpToolsetAssignmentSetValue("toolset-a"))

	applyUpdate := func() *tfprotov6.ApplyResourceChangeResponse {
		t.Helper()
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"key": "sk-key-toolsets", "mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-b")}))
		proposed := organizationProjectProtocolReplace(t, schema, created.NewState, map[string]interface{}{"mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-b")})
		planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: created.NewState, ProposedNewState: proposed, PriorPrivate: created.Private})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
			t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
		}
		applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: created.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
		if err != nil {
			t.Fatal(err)
		}
		return applied
	}
	assertRetained := func(step string, applied *tfprotov6.ApplyResourceChangeResponse) {
		t.Helper()
		if !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
			t.Fatalf("%s: unconfirmed assignment mutation did not error", step)
		}
		priorValue, _ := created.NewState.Unmarshal(schema.ValueType())
		failedValue, _ := applied.NewState.Unmarshal(schema.ValueType())
		if !priorValue.Equal(failedValue) {
			t.Fatalf("%s published unconfirmed state\nprior: %s\n  got: %s", step, priorValue, failedValue)
		}
	}

	backend.mu.Lock()
	backend.malformedInfo = true
	backend.mu.Unlock()
	assertRetained("malformed readback", applyUpdate())

	backend.mu.Lock()
	backend.malformedInfo = false
	backend.divergentInfo = []interface{}{"toolset-x"}
	backend.mu.Unlock()
	assertRetained("divergent readback", applyUpdate())
}

// A team assignment mutation whose accepted readback reports a different
// membership must retain prior state instead of publishing either value.
func TestTeamProtocolMCPToolsetAssignmentDivergentReadbackRetainsPriorState(t *testing.T) {
	ctx := context.Background()
	backend := &mcpToolsetAssignmentProtocolServer{remoteToolsets: []interface{}{"toolset-a"}}
	server := httptest.NewServer(backend.handler(t))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_team"
	schema := schemas.ResourceSchemas[typeName]

	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "team-toolsets"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	importRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importRead.Diagnostics) {
		t.Fatalf("import read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(importRead.Diagnostics))
	}

	backend.mu.Lock()
	backend.divergentInfo = []interface{}{"toolset-x"}
	backend.mu.Unlock()
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"team_id": "team-toolsets", "team_alias": "toolsets", "mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-b")}))
	proposed := organizationProjectProtocolReplace(t, schema, importRead.NewState, map[string]interface{}{"mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-b")})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: importRead.NewState, ProposedNewState: proposed, PriorPrivate: importRead.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	failed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: importRead.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil {
		t.Fatal(err)
	}
	if !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) {
		t.Fatal("divergent readback did not error")
	}
	priorValue, _ := importRead.NewState.Unmarshal(schema.ValueType())
	failedValue, _ := failed.NewState.Unmarshal(schema.ValueType())
	if !priorValue.Equal(failedValue) {
		t.Fatalf("divergent readback published state\nprior: %s\n  got: %s", priorValue, failedValue)
	}
}

// Adding mcp_toolset_ids to the key and team schemas requires a version bump
// and direct upgraders from every historical version: upgraded states carry a
// null (unmanaged) assignment and preserve the other attributes exactly.
func TestMCPToolsetAssignmentSchemaUpgrades(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)

	keySchema := schemas.ResourceSchemas["litellm_key"]
	if keySchema.Version != 3 {
		t.Fatalf("litellm_key schema version = %d, want 3 after adding mcp_toolset_ids", keySchema.Version)
	}
	teamSchema := schemas.ResourceSchemas["litellm_team"]
	if teamSchema.Version != 2 {
		t.Fatalf("litellm_team schema version = %d, want 2 after adding mcp_toolset_ids", teamSchema.Version)
	}

	for _, test := range []struct {
		typeName string
		version  int64
		raw      map[string]interface{}
		wantJSON map[string]string
	}{
		{"litellm_key", 0, map[string]interface{}{"id": "sk-upgrade-v0", "key": "sk-preserved"}, map[string]string{"metadata_json": ""}},
		{"litellm_key", 1, map[string]interface{}{"id": hashKeyForID("sk-upgrade-v1"), "key": "sk-preserved"}, map[string]string{"metadata_json": ""}},
		{"litellm_key", 2, map[string]interface{}{"id": hashKeyForID("sk-upgrade-v2"), "key": "sk-preserved", "metadata_json": `{"tier":"gold"}`}, map[string]string{"metadata_json": `{"tier":"gold"}`}},
		{"litellm_team", 0, map[string]interface{}{"id": "team-upgrade", "team_id": "team-upgrade", "team_alias": "upgrade", "metadata_json": `{"tier":"gold"}`}, map[string]string{"metadata_json": ""}},
		{"litellm_team", 1, map[string]interface{}{"id": "team-upgrade", "team_id": "team-upgrade", "team_alias": "upgrade", "metadata_json": `{"tier":"gold"}`}, map[string]string{"metadata_json": `{"tier":"gold"}`}},
	} {
		raw, err := json.Marshal(test.raw)
		if err != nil {
			t.Fatal(err)
		}
		upgraded, err := protocolServer.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{
			TypeName: test.typeName, Version: test.version, RawState: &tfprotov6.RawState{JSON: raw},
		})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(upgraded.Diagnostics) || upgraded.UpgradedState == nil {
			t.Fatalf("%s v%d upgrade: err=%v diagnostics=%s", test.typeName, test.version, err, agentProtocolDiagnosticsText(upgraded.Diagnostics))
		}
		schema := schemas.ResourceSchemas[test.typeName]
		attributes := protocolAttributeMap(t, schema, upgraded.UpgradedState)
		if !attributes["mcp_toolset_ids"].IsNull() {
			t.Fatalf("%s v%d upgraded mcp_toolset_ids = %s, want null (unmanaged)", test.typeName, test.version, attributes["mcp_toolset_ids"])
		}
		for name, want := range test.wantJSON {
			if want == "" {
				if !attributes[name].IsNull() {
					t.Fatalf("%s v%d %s was adopted: %s", test.typeName, test.version, name, attributes[name])
				}
				continue
			}
			var got string
			if err := attributes[name].As(&got); err != nil || got != want {
				t.Fatalf("%s v%d %s = %q err=%v, want preserved %q", test.typeName, test.version, name, got, err, want)
			}
		}
	}
}

// An update that changes another team field while the managed assignment is
// unchanged still dispatched object_permission, so a divergent readback must
// retain prior state instead of publishing the divergent access control.
func TestTeamProtocolMCPToolsetAssignmentUnchangedDivergentReadbackRetainsPriorState(t *testing.T) {
	ctx := context.Background()
	backend := &mcpToolsetAssignmentProtocolServer{remoteToolsets: []interface{}{"toolset-a"}}
	server := httptest.NewServer(backend.handler(t))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_team"
	schema := schemas.ResourceSchemas[typeName]

	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "team-toolsets"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	importRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importRead.Diagnostics) {
		t.Fatalf("import read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(importRead.Diagnostics))
	}

	// The alias changes; the managed assignment stays ["toolset-a"], but the
	// readback reports an emptied membership.
	backend.mu.Lock()
	backend.divergentInfo = []interface{}{}
	backend.mu.Unlock()
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"team_id": "team-toolsets", "team_alias": "renamed", "mcp_toolset_ids": mcpToolsetAssignmentSetValue("toolset-a")}))
	proposed := organizationProjectProtocolReplace(t, schema, importRead.NewState, map[string]interface{}{"team_alias": "renamed"})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: importRead.NewState, ProposedNewState: proposed, PriorPrivate: importRead.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	failed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: importRead.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil {
		t.Fatal(err)
	}
	if !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) {
		t.Fatal("unchanged-assignment divergent readback did not error")
	}
	priorValue, _ := importRead.NewState.Unmarshal(schema.ValueType())
	failedValue, _ := failed.NewState.Unmarshal(schema.ValueType())
	if !priorValue.Equal(failedValue) {
		t.Fatalf("divergent readback published state\nprior: %s\n  got: %s", priorValue, failedValue)
	}
}
