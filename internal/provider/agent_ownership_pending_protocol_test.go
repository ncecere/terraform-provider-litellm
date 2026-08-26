package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type agentOwnershipProtocolAPI struct {
	mu                 sync.Mutex
	tpm                int64
	getTPMShape        string
	patchStatus        int
	confirmationOldTPM bool
	requests           atomic.Int64
	patches            atomic.Int64
}

func (a *agentOwnershipProtocolAPI) handler(w http.ResponseWriter, r *http.Request) {
	a.requests.Add(1)
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path != "/v1/agents/ownership" {
		http.NotFound(w, r)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch r.Method {
	case http.MethodGet:
		tmp := a.tpm
		if a.confirmationOldTPM {
			tmp = 10
		}
		tpm := fmt.Sprintf(`,"tpm_limit":%d`, tmp)
		if a.getTPMShape == "omitted" {
			tpm = ""
		} else if a.getTPMShape == "null" {
			tpm = `,"tpm_limit":null`
		}
		_, _ = fmt.Fprintf(w, `{"agent_id":"ownership","agent_name":"agent"%s,"agent_card_params":{"name":"Agent","url":"https://agent.invalid"}}`, tpm)
	case http.MethodPatch:
		a.patches.Add(1)
		if a.patchStatus != 0 && a.patchStatus != http.StatusOK {
			http.Error(w, `{"detail":"rejected"}`, a.patchStatus)
			return
		}
		var request map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, `{"detail":"malformed"}`, http.StatusBadRequest)
			return
		}
		if raw, ok := request[agentFieldTPM].(float64); ok {
			a.tpm = int64(raw)
		}
		_, _ = io.WriteString(w, `{}`)
	default:
		http.NotFound(w, r)
	}
}

func (a *agentOwnershipProtocolAPI) setReadShape(shape string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.getTPMShape = shape
}

func (a *agentOwnershipProtocolAPI) setTPM(value int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tpm = value
}

func (a *agentOwnershipProtocolAPI) setPatch(status int, confirmationOldTPM bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.patchStatus = status
	a.confirmationOldTPM = confirmationOldTPM
}

func agentOwnershipProtocolFixture(t *testing.T, api *agentOwnershipProtocolAPI) (context.Context, tfprotov6.ProviderServer, *tfprotov6.Schema, *tfprotov6.ReadResourceResponse) {
	t.Helper()
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(api.handler))
	t.Cleanup(server.Close)
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_agent"
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "ownership"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(imported.Diagnostics))
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("import read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	committed, err := decodeAgentFieldSet(protocolPrivateValue(t, read.Private, agentImportedFieldsPrivateKey))
	if err != nil || !committed[agentFieldTPM] || protocolPrivateHasKey(t, read.Private, agentOwnershipPendingPrivateKey) {
		t.Fatalf("initial ownership: committed=%#v err=%v private=%s", committed, err, read.Private)
	}
	return ctx, protocolServer, schemas.ResourceSchemas[typeName], read
}

func agentOwnershipProtocolPlan(t *testing.T, ctx context.Context, protocolServer tfprotov6.ProviderServer, schema *tfprotov6.Schema, prior *tfprotov6.DynamicValue, private []byte, desiredTPM int64) (*tfprotov6.DynamicValue, *tfprotov6.PlanResourceChangeResponse) {
	t.Helper()
	config := agentProtocolDynamicValue(t, schema, map[string]interface{}{
		"agent_name": "agent",
		"tpm_limit":  desiredTPM,
		"agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"},
	})
	proposed := organizationProjectProtocolReplace(t, schema, prior, map[string]interface{}{agentFieldTPM: desiredTPM})
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: prior, ProposedNewState: proposed, PriorPrivate: private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, prior, planned) != organizationProjectProtocolActionUpdate {
		t.Fatalf("ownership plan: err=%v diagnostics=%s action=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, prior, planned))
	}
	committed, committedErr := decodeAgentFieldSet(protocolPrivateValue(t, planned.PlannedPrivate, agentImportedFieldsPrivateKey))
	pending, pendingErr := decodeAgentFieldSet(protocolPrivateValue(t, planned.PlannedPrivate, agentOwnershipPendingPrivateKey))
	if committedErr != nil || pendingErr != nil || !committed[agentFieldTPM] || pending[agentFieldTPM] {
		t.Fatalf("planned ownership: committed=%#v pending=%#v errors=%v/%v private=%s", committed, pending, committedErr, pendingErr, planned.PlannedPrivate)
	}
	return config, planned
}

func assertAgentProtocolPrivateUnchanged(t *testing.T, want, got []byte) {
	t.Helper()
	if !bytes.Equal(want, got) {
		t.Fatalf("private ownership changed during unconfirmed lifecycle step:\nwant %s\n got %s", want, got)
	}
}

func assertAgentProtocolStateUnchanged(t *testing.T, schema *tfprotov6.Schema, want, got *tfprotov6.DynamicValue) {
	t.Helper()
	wantValue, wantErr := want.Unmarshal(schema.ValueType())
	gotValue, gotErr := got.Unmarshal(schema.ValueType())
	if wantErr != nil || gotErr != nil || !wantValue.Equal(gotValue) {
		t.Fatalf("public state changed during failed apply: wantErr=%v gotErr=%v", wantErr, gotErr)
	}
}

func TestAgentProtocolCreateFinalizesOwnershipOnlyAfterExplicitConfirmation(t *testing.T) {
	ctx := context.Background()
	var confirmationReads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/agents":
			_, _ = io.WriteString(w, `{"agent_id":"ownership"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/agents/ownership":
			tpm := int64(10)
			if confirmationReads.Add(1) == 1 {
				tpm = 9
			}
			_, _ = fmt.Fprintf(w, `{"agent_id":"ownership","agent_name":"agent","tpm_limit":%d,"agent_card_params":{"name":"Agent","url":"https://agent.invalid"}}`, tpm)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_agent"]
	values := map[string]interface{}{
		"agent_name": "agent", "tpm_limit": int64(10),
		"agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"},
	}
	config := agentProtocolDynamicValue(t, schema, values)
	proposed := agentProtocolDynamicValue(t, schema, map[string]interface{}{
		"id": tftypes.UnknownValue, "agent_name": "agent", "tpm_limit": int64(10), "agent_card": values["agent_card"],
		"litellm_params": tftypes.UnknownValue, "static_headers": tftypes.UnknownValue, "extra_headers": tftypes.UnknownValue,
		"created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue, "created_by": tftypes.UnknownValue, "updated_by": tftypes.UnknownValue,
	})
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: "litellm_agent", Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || confirmationReads.Load() != 3 {
		t.Fatalf("create apply: err=%v diagnostics=%s confirmation reads=%d", err, agentProtocolDiagnosticsText(applied.Diagnostics), confirmationReads.Load())
	}
	committed, decodeErr := decodeAgentFieldSet(protocolPrivateValue(t, applied.Private, agentImportedFieldsPrivateKey))
	if decodeErr != nil || len(committed) != 0 || protocolPrivateHasKey(t, applied.Private, agentOwnershipPendingPrivateKey) {
		t.Fatalf("create ownership: committed=%#v err=%v private=%s", committed, decodeErr, applied.Private)
	}
}

func TestAgentProtocolLegacyEqualValueTransferVerifiesWithoutPatchAndKeepsPublicState(t *testing.T) {
	api := &agentOwnershipProtocolAPI{tpm: 10}
	ctx, protocolServer, schema, prior := agentOwnershipProtocolFixture(t, api)
	config, planned := agentOwnershipProtocolPlan(t, ctx, protocolServer, schema, prior.NewState, nil, 10)
	if string(protocolPrivateValue(t, planned.PlannedPrivate, agentOwnershipMigrationPrivateKey)) != "true" {
		t.Fatal("legacy equal-value plan lacks its private migration marker")
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: prior.NewState,
		PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("equal-value transfer apply: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	if api.patches.Load() != 0 {
		t.Fatalf("equal-value ownership transfer sent %d PATCH requests", api.patches.Load())
	}
	assertAgentProtocolStateUnchanged(t, schema, prior.NewState, applied.NewState)
	before, beforeErr := decodeAgentFieldSet(protocolPrivateValue(t, planned.PlannedPrivate, agentImportedFieldsPrivateKey))
	expected, pendingErr := decodeAgentFieldSet(protocolPrivateValue(t, planned.PlannedPrivate, agentOwnershipPendingPrivateKey))
	committed, decodeErr := decodeAgentFieldSet(protocolPrivateValue(t, applied.Private, agentImportedFieldsPrivateKey))
	if beforeErr != nil || pendingErr != nil || decodeErr != nil || !agentFieldSetsEqual(committed, expected) || protocolPrivateHasKey(t, applied.Private, agentOwnershipPendingPrivateKey) || protocolPrivateHasKey(t, applied.Private, agentOwnershipMigrationPrivateKey) {
		t.Fatalf("equal-value ownership was not committed exactly: before=%#v expected=%#v committed=%#v errors=%v/%v/%v", before, expected, committed, beforeErr, pendingErr, decodeErr)
	}
	for _, scope := range []string{agentScopeCardCapabilities, agentScopeCardProvider, agentScopeCardSkills, agentScopeCardSignatures, agentScopePermission} {
		if !before[scope] || !committed[scope] {
			t.Fatalf("equal-value transfer lost API-owned scope %q: before=%#v committed=%#v", scope, before, committed)
		}
	}
	refreshed, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: "litellm_agent", CurrentState: applied.NewState, Private: applied.Private,
	})
	if readErr != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("equal-value transfer refresh: err=%v diagnostics=%s", readErr, agentProtocolDiagnosticsText(refreshed.Diagnostics))
	}
	assertAgentProtocolStateUnchanged(t, schema, applied.NewState, refreshed.NewState)
	assertAgentProtocolPrivateUnchanged(t, applied.Private, refreshed.Private)
}

func TestAgentProtocolLegacyEqualValueTransferFailureKeepsPriorStateAndPrivate(t *testing.T) {
	api := &agentOwnershipProtocolAPI{tpm: 10}
	ctx, protocolServer, schema, prior := agentOwnershipProtocolFixture(t, api)
	config, planned := agentOwnershipProtocolPlan(t, ctx, protocolServer, schema, prior.NewState, nil, 10)
	api.setTPM(11)
	applyCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	applied, err := protocolServer.ApplyResourceChange(applyCtx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: prior.NewState,
		PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatalf("unconfirmed equal-value transfer: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(applied.Diagnostics))
	}
	if api.patches.Load() != 0 {
		t.Fatalf("unconfirmed equal-value ownership transfer sent %d PATCH requests", api.patches.Load())
	}
	assertAgentProtocolStateUnchanged(t, schema, prior.NewState, applied.NewState)
	assertAgentProtocolPrivateUnchanged(t, planned.PlannedPrivate, applied.Private)
}

func TestAgentProtocolRejectedPatchRefreshNeverPromotesPending(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
	}{
		{name: "failed patch", status: http.StatusInternalServerError},
		{name: "rejected patch", status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			api := &agentOwnershipProtocolAPI{tpm: 10}
			ctx, protocolServer, schema, imported := agentOwnershipProtocolFixture(t, api)
			config, planned := agentOwnershipProtocolPlan(t, ctx, protocolServer, schema, imported.NewState, imported.Private, 20)
			api.setPatch(test.status, false)
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: "litellm_agent", Config: config, PriorState: imported.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || api.patches.Load() != 1 {
				t.Fatalf("failed apply: err=%v diagnostics=%s patches=%d", err, agentProtocolDiagnosticsText(applied.Diagnostics), api.patches.Load())
			}
			assertAgentProtocolPrivateUnchanged(t, planned.PlannedPrivate, applied.Private)
			assertAgentProtocolStateUnchanged(t, schema, imported.NewState, applied.NewState)

			state, private := applied.NewState, applied.Private
			for _, shape := range []string{"omitted", "null", "present", "present"} {
				api.setReadShape(shape)
				refreshed, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: state, Private: private})
				if readErr != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
					t.Fatalf("%s refresh: err=%v diagnostics=%s", shape, readErr, agentProtocolDiagnosticsText(refreshed.Diagnostics))
				}
				assertAgentProtocolPrivateUnchanged(t, planned.PlannedPrivate, refreshed.Private)
				state, private = refreshed.NewState, refreshed.Private
			}
		})
	}
}

func TestAgentProtocolFailedConfirmationRefreshAndRetryPromotesOnce(t *testing.T) {
	api := &agentOwnershipProtocolAPI{tpm: 10}
	ctx, protocolServer, schema, imported := agentOwnershipProtocolFixture(t, api)
	config, planned := agentOwnershipProtocolPlan(t, ctx, protocolServer, schema, imported.NewState, imported.Private, 20)

	api.setPatch(http.StatusOK, true)
	applyCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
	failed, err := protocolServer.ApplyResourceChange(applyCtx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: imported.NewState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate,
	})
	cancel()
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(failed.Diagnostics) || api.patches.Load() != 1 {
		t.Fatalf("unconfirmed mutation: err=%v diagnostics=%s patches=%d", err, agentProtocolDiagnosticsText(failed.Diagnostics), api.patches.Load())
	}
	assertAgentProtocolPrivateUnchanged(t, planned.PlannedPrivate, failed.Private)
	assertAgentProtocolStateUnchanged(t, schema, imported.NewState, failed.NewState)

	api.setPatch(http.StatusOK, false)
	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: failed.NewState, Private: failed.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("post-failure refresh: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(refreshed.Diagnostics))
	}
	assertAgentProtocolPrivateUnchanged(t, planned.PlannedPrivate, refreshed.Private)

	config, retry := agentOwnershipProtocolPlan(t, ctx, protocolServer, schema, refreshed.NewState, refreshed.Private, 20)
	promoted, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: refreshed.NewState, PlannedState: retry.PlannedState, PlannedPrivate: retry.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(promoted.Diagnostics) || api.patches.Load() != 2 {
		t.Fatalf("confirmed retry: err=%v diagnostics=%s patches=%d", err, agentProtocolDiagnosticsText(promoted.Diagnostics), api.patches.Load())
	}
	committed, decodeErr := decodeAgentFieldSet(protocolPrivateValue(t, promoted.Private, agentImportedFieldsPrivateKey))
	if decodeErr != nil || committed[agentFieldTPM] || protocolPrivateHasKey(t, promoted.Private, agentOwnershipPendingPrivateKey) {
		t.Fatalf("retry did not promote exactly once: committed=%#v err=%v private=%s", committed, decodeErr, promoted.Private)
	}

	stable := promoted
	for range 2 {
		refreshedAgain, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: stable.NewState, Private: stable.Private})
		if readErr != nil || accessGroupProtocolDiagnosticsHaveError(refreshedAgain.Diagnostics) {
			t.Fatalf("post-promotion refresh: err=%v diagnostics=%s", readErr, agentProtocolDiagnosticsText(refreshedAgain.Diagnostics))
		}
		assertAgentProtocolPrivateUnchanged(t, promoted.Private, refreshedAgain.Private)
		stable = &tfprotov6.ApplyResourceChangeResponse{NewState: refreshedAgain.NewState, Private: refreshedAgain.Private}
	}
	noOpConfig := agentProtocolDynamicValue(t, schema, map[string]interface{}{
		"agent_name": "agent", "tpm_limit": int64(20),
		"agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"},
	})
	noOp, planErr := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: "litellm_agent", Config: noOpConfig, PriorState: stable.NewState, ProposedNewState: stable.NewState, PriorPrivate: stable.Private,
	})
	if planErr != nil || accessGroupProtocolDiagnosticsHaveError(noOp.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, stable.NewState, noOp) != organizationProjectProtocolActionNoOp || api.patches.Load() != 2 {
		t.Fatalf("post-promotion plan: err=%v diagnostics=%s action=%s patches=%d", planErr, agentProtocolDiagnosticsText(noOp.Diagnostics), organizationProjectProtocolPlannedAction(t, schema, stable.NewState, noOp), api.patches.Load())
	}
}

func TestAgentProtocolMalformedPendingFailsClosedBeforeRequests(t *testing.T) {
	api := &agentOwnershipProtocolAPI{tpm: 10}
	ctx, protocolServer, schema, imported := agentOwnershipProtocolFixture(t, api)
	config, planned := agentOwnershipProtocolPlan(t, ctx, protocolServer, schema, imported.NewState, imported.Private, 20)
	privateValues := map[string][]byte{}
	if err := json.Unmarshal(planned.PlannedPrivate, &privateValues); err != nil {
		t.Fatal(err)
	}
	privateValues[agentOwnershipPendingPrivateKey] = []byte(`["tpm_limit","tpm_limit"]`)
	malformed, err := json.Marshal(privateValues)
	if err != nil {
		t.Fatal(err)
	}
	before := api.requests.Load()
	read, readErr := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: imported.NewState, Private: malformed})
	if readErr != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) || api.requests.Load() != before {
		t.Fatalf("malformed read: err=%v diagnostics=%s requests=%d/%d", readErr, agentProtocolDiagnosticsText(read.Diagnostics), api.requests.Load(), before)
	}
	applied, applyErr := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "litellm_agent", Config: config, PriorState: imported.NewState, PlannedState: planned.PlannedState, PlannedPrivate: malformed,
	})
	if applyErr != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || api.requests.Load() != before {
		t.Fatalf("malformed apply: err=%v diagnostics=%s requests=%d/%d", applyErr, agentProtocolDiagnosticsText(applied.Diagnostics), api.requests.Load(), before)
	}
}
