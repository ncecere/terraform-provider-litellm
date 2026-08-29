package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type agentBlockProjectionAPI struct {
	mu       sync.Mutex
	response map[string]interface{}
	patches  []map[string]interface{}
	requests atomic.Int64
}

func agentBlockProjectionSeed(serverURL string) map[string]interface{} {
	response := agentClearProtocolResponse(serverURL, true, "")
	card := response["agent_card_params"].(map[string]interface{})
	card["skills"] = []interface{}{
		map[string]interface{}{"id": "acceptance", "name": "Acceptance", "description": "owned after convergence", "tags": []interface{}{}, "examples": []interface{}{}, "inputModes": []interface{}{}, "outputModes": []interface{}{}, "security": []interface{}{}},
		map[string]interface{}{"id": "api-null", "name": "API Null", "security": nil},
		map[string]interface{}{"id": "api-omitted", "name": "API Omitted"},
	}
	card["signatures"] = []interface{}{
		map[string]interface{}{"protected": "duplicate", "signature": "secret", "header": nil},
		map[string]interface{}{"protected": "duplicate", "signature": "secret", "header": nil},
		map[string]interface{}{"protected": "duplicate", "signature": "secret"},
		map[string]interface{}{"protected": "duplicate", "signature": "secret"},
		map[string]interface{}{"protected": "number", "signature": "secret", "header": map[string]interface{}{"exact": json.Number("9007199254740993"), "spelling": json.Number("1e+09")}},
	}
	return response
}

func (a *agentBlockProjectionAPI) handler(w http.ResponseWriter, r *http.Request) {
	a.requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path != "/v1/agents/"+agentClearProtocolID {
		http.NotFound(w, r)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(a.response)
	case http.MethodPatch:
		var request map[string]interface{}
		decoder := json.NewDecoder(r.Body)
		decoder.UseNumber()
		if decoder.Decode(&request) != nil {
			http.Error(w, `{}`, http.StatusBadRequest)
			return
		}
		a.patches = append(a.patches, cloneAgentWireObject(request))
		for _, field := range []string{"litellm_params", "static_headers", "agent_card_params"} {
			if value, present := request[field].(map[string]interface{}); present {
				a.response[field] = cloneAgentWireObject(value)
			}
		}
		_, _ = io.WriteString(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

func (a *agentBlockProjectionAPI) addConcurrent() {
	a.mu.Lock()
	defer a.mu.Unlock()
	card := a.response["agent_card_params"].(map[string]interface{})
	card["skills"] = append(card["skills"].([]interface{}), map[string]interface{}{"id": "api-concurrent", "name": "API Concurrent", "security": nil})
	card["signatures"] = append(card["signatures"].([]interface{}), map[string]interface{}{"protected": "concurrent", "signature": "secret"})
}

func (a *agentBlockProjectionAPI) patch(index int) map[string]interface{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	return cloneAgentWireObject(a.patches[index])
}

func agentBlockProjectionValues(description string, includeSkill bool) map[string]interface{} {
	values := agentClearOverlayClearedConfig()
	card := values["agent_card"].(map[string]interface{})
	card["signatures"] = []interface{}{}
	if includeSkill {
		card["skills"] = []interface{}{map[string]interface{}{
			"id": "acceptance", "name": "Acceptance", "tags": []interface{}{}, "examples": []interface{}{},
			"input_modes": []interface{}{}, "output_modes": []interface{}{}, "security": []interface{}{},
		}}
	} else {
		card["skills"] = []interface{}{}
	}
	if description != "" {
		card["description"] = description
	}
	return values
}

func assertAgentBlockCollectionCounts(t *testing.T, schema *tfprotov6.Schema, state *tfprotov6.DynamicValue, skills, signatures int) {
	t.Helper()
	attributes := protocolAttributeMap(t, schema, state)
	var card map[string]tftypes.Value
	if err := attributes["agent_card"].As(&card); err != nil {
		t.Fatal(err)
	}
	var skillValues, signatureValues []tftypes.Value
	if err := card["skills"].As(&skillValues); err != nil || len(skillValues) != skills {
		t.Fatalf("skills=%d err=%v, want %d", len(skillValues), err, skills)
	}
	if err := card["signatures"].As(&signatureValues); err != nil || len(signatureValues) != signatures {
		t.Fatalf("signatures=%d err=%v, want %d", len(signatureValues), err, signatures)
	}
}

type agentBlockProjectionFixture struct {
	ctx     context.Context
	api     *agentBlockProjectionAPI
	server  tfprotov6.ProviderServer
	schema  *tfprotov6.Schema
	state   *tfprotov6.DynamicValue
	private []byte
}

func newAgentBlockProjectionFixture(t *testing.T) agentBlockProjectionFixture {
	t.Helper()
	ctx := context.Background()
	api := &agentBlockProjectionAPI{}
	httpServer := httptest.NewServer(http.HandlerFunc(api.handler))
	t.Cleanup(httpServer.Close)
	api.response = agentBlockProjectionSeed(httpServer.URL)
	server, schemas := configuredImportProtocolServer(t, ctx, httpServer.URL)
	schema := schemas.ResourceSchemas["litellm_agent"]
	imported, err := server.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_agent", ID: agentClearProtocolID})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	read, err := server.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("import read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	assertAgentBlockCollectionCounts(t, schema, read.NewState, 3, 5)
	return agentBlockProjectionFixture{ctx: ctx, api: api, server: server, schema: schema, state: read.NewState, private: read.Private}
}

func planAgentBlockProjection(t *testing.T, f agentBlockProjectionFixture, values map[string]interface{}) (*tfprotov6.DynamicValue, *tfprotov6.PlanResourceChangeResponse) {
	t.Helper()
	config := agentProtocolDynamicValue(t, f.schema, values)
	proposed := agentProtocolReplaceMany(t, f.schema, f.state, map[string]interface{}{
		"litellm_params": values["litellm_params"], "static_headers": values["static_headers"], "extra_headers": values["extra_headers"],
		"agent_card": values["agent_card"], "object_permission": values["object_permission"],
	})
	planned, err := f.server.PlanResourceChange(f.ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: f.state, ProposedNewState: proposed, PriorPrivate: f.private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	return config, planned
}

func applyAgentBlockProjection(t *testing.T, f agentBlockProjectionFixture, values map[string]interface{}, skills int) agentBlockProjectionFixture {
	t.Helper()
	config, planned := planAgentBlockProjection(t, f, values)
	assertAgentBlockCollectionCounts(t, f.schema, planned.PlannedState, skills, 0)
	applied, err := f.server.ApplyResourceChange(f.ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: f.state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		preserved := false
		if len(f.api.patches) > 0 {
			preserved = (&agentPatchPreservation{cardBase: agentBlockProjectionSeed("")["agent_card_params"].(map[string]interface{}), cardPatch: f.api.patches[0]["agent_card_params"].(map[string]interface{})}).matches(context.Background(), f.api.response)
		}
		t.Fatalf("apply: err=%v diagnostics=%s preservation=%t patches=%#v response=%#v", err, agentProtocolDiagnosticsText(applied.Diagnostics), preserved, f.api.patches, f.api.response)
	}
	assertAgentBlockCollectionCounts(t, f.schema, applied.NewState, skills, 0)
	plannedValues, appliedValues := protocolAttributeMap(t, f.schema, planned.PlannedState), protocolAttributeMap(t, f.schema, applied.NewState)
	for _, field := range []string{"agent_name", "agent_card", "litellm_params", "litellm_params_json", "static_headers", "extra_headers", "object_permission", "tpm_limit", "rpm_limit", "session_tpm_limit", "session_rpm_limit"} {
		if !plannedValues[field].Equal(appliedValues[field]) {
			t.Fatalf("Apply %s differs from plan", field)
		}
	}
	f.state, f.private = applied.NewState, applied.Private
	return f
}

func TestAgentListNestedBlocksHideAndPreserveAPICollections(t *testing.T) {
	f := newAgentBlockProjectionFixture(t)
	seedCard := agentBlockProjectionSeed("")["agent_card_params"].(map[string]interface{})
	f = applyAgentBlockProjection(t, f, agentBlockProjectionValues("", true), 1)
	if len(f.api.patches) != 1 {
		t.Fatalf("patches=%d", len(f.api.patches))
	}
	patchCard := f.api.patch(0)["agent_card_params"].(map[string]interface{})
	if !reflect.DeepEqual(patchCard["skills"], seedCard["skills"]) || !reflect.DeepEqual(patchCard["signatures"], seedCard["signatures"]) {
		t.Fatal("convergence PATCH did not preserve hidden skill/signature order, duplicates, nulls, omissions, and numbers")
	}
	provenance, err := decodeAgentCollectionProvenance(protocolPrivateValue(t, f.private, agentCollectionsPrivateKey))
	if err != nil || len(provenance.Skills) != 2 || len(provenance.Signatures) != 5 {
		t.Fatalf("hidden provenance: skills=%d signatures=%d err=%v", len(provenance.Skills), len(provenance.Signatures), err)
	}

	for i := 0; i < 2; i++ {
		read, readErr := f.server.ReadResource(f.ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: f.state, Private: f.private})
		if readErr != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
			t.Fatalf("read %d: err=%v diagnostics=%s", i+1, readErr, agentProtocolDiagnosticsText(read.Diagnostics))
		}
		assertAgentProtocolStateUnchanged(t, f.schema, f.state, read.NewState)
		assertAgentProtocolPrivateUnchanged(t, f.private, read.Private)
		f.state, f.private = read.NewState, read.Private
	}
	values := agentBlockProjectionValues("", true)
	config := agentProtocolDynamicValue(t, f.schema, values)
	noop, err := f.server.PlanResourceChange(f.ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: f.state, ProposedNewState: f.state, PriorPrivate: f.private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(noop.Diagnostics) || organizationProjectProtocolPlannedAction(t, f.schema, f.state, noop) != organizationProjectProtocolActionNoOp {
		t.Fatalf("no-op: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(noop.Diagnostics))
	}

	ownedValues := agentBlockProjectionValues("unrelated update", true)
	ownedValues["agent_card"].(map[string]interface{})["skills"].([]interface{})[0].(map[string]interface{})["description"] = "owned after convergence"
	f = applyAgentBlockProjection(t, f, ownedValues, 1)
	patchCard = f.api.patch(1)["agent_card_params"].(map[string]interface{})
	if len(agentWireObjectList(patchCard["skills"])) != 3 || len(agentWireObjectList(patchCard["signatures"])) != 5 {
		t.Fatal("unrelated update lost hidden API collections")
	}

	// The matching configured skill transferred ownership. Its removal is real,
	// while the two hidden API skills and all hidden signatures survive remotely.
	f = applyAgentBlockProjection(t, f, agentBlockProjectionValues("unrelated update", false), 0)
	patchCard = f.api.patch(2)["agent_card_params"].(map[string]interface{})
	if skills := agentSkillRawByID(patchCard); len(skills) != 2 || skills["acceptance"] != nil || skills["api-null"] == nil || skills["api-omitted"] == nil {
		t.Fatalf("Terraform-owned skill removal did not preserve only hidden siblings: %#v", skills)
	}
	if len(agentWireObjectList(patchCard["signatures"])) != 5 {
		t.Fatal("Terraform-owned removal lost hidden signatures")
	}
}

func TestAgentConcurrentCollectionsStayDeferredAndCorruptionDispatchesNothing(t *testing.T) {
	f := newAgentBlockProjectionFixture(t)
	values := agentBlockProjectionValues("", true)
	config, planned := planAgentBlockProjection(t, f, values)
	assertAgentBlockCollectionCounts(t, f.schema, planned.PlannedState, 1, 0)
	f.api.addConcurrent()
	applied, err := f.server.ApplyResourceChange(f.ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: f.state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("concurrent apply: err=%v diagnostics=%s patches=%#v response=%#v", err, agentProtocolDiagnosticsText(applied.Diagnostics), f.api.patches, f.api.response)
	}
	assertAgentBlockCollectionCounts(t, f.schema, applied.NewState, 1, 0)
	patchCard := f.api.patch(0)["agent_card_params"].(map[string]interface{})
	if len(agentWireObjectList(patchCard["skills"])) != 4 || len(agentWireObjectList(patchCard["signatures"])) != 6 {
		t.Fatal("concurrent additions were not preserved remotely")
	}
	for i := 0; i < 2; i++ {
		read, readErr := f.server.ReadResource(f.ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: applied.NewState, Private: applied.Private})
		if readErr != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
			t.Fatalf("deferred read %d: err=%v diagnostics=%s", i+1, readErr, agentProtocolDiagnosticsText(read.Diagnostics))
		}
		assertAgentProtocolStateUnchanged(t, f.schema, applied.NewState, read.NewState)
		assertAgentProtocolPrivateUnchanged(t, applied.Private, read.Private)
		applied.NewState, applied.Private = read.NewState, read.Private
	}

	stable, planErr := f.server.PlanResourceChange(f.ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: applied.NewState, ProposedNewState: applied.NewState, PriorPrivate: applied.Private,
	})
	if planErr != nil || accessGroupProtocolDiagnosticsHaveError(stable.Diagnostics) || organizationProjectProtocolPlannedAction(t, f.schema, applied.NewState, stable) != organizationProjectProtocolActionNoOp {
		t.Fatalf("concurrent no-op: err=%v diagnostics=%s", planErr, agentProtocolDiagnosticsText(stable.Diagnostics))
	}

	privateValues := map[string][]byte{}
	if json.Unmarshal(applied.Private, &privateValues) != nil {
		t.Fatal("decode private")
	}
	privateValues[agentCollectionsPrivateKey] = []byte(`{"skills":[{"id":"secret"}],"signatures":[],"extra":true}`)
	corrupt, _ := json.Marshal(privateValues)
	before := f.api.requests.Load()
	failed, applyErr := f.server.ApplyResourceChange(f.ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: applied.NewState, PlannedState: planned.PlannedState, PlannedPrivate: corrupt})
	if applyErr != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) || f.api.requests.Load() != before {
		t.Fatalf("corrupt provenance: err=%v diagnostics=%s requests=%d/%d", applyErr, agentProtocolDiagnosticsText(failed.Diagnostics), f.api.requests.Load(), before)
	}
	for _, protected := range []string{"secret", "api-null", "acceptance"} {
		if strings.Contains(agentProtocolDiagnosticsText(failed.Diagnostics), protected) {
			t.Fatal("content-bearing corrupt-provenance diagnostic")
		}
	}
}
