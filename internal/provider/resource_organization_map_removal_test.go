package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func organizationProtocolJSON(id, rpm, tpm string, config bool) string {
	idValue, organizationID, models, metadata, blocked, tags := "null", "null", "null", "null", "null", "null"
	if !config {
		idValue, organizationID = fmt.Sprintf("%q", id), fmt.Sprintf("%q", id)
		models, metadata, blocked, tags = "[]", "{}", "false", "[]"
	}
	return fmt.Sprintf(`{"id":%s,"organization_id":%s,"organization_alias":"acme","models":%s,"budget_id":null,"max_budget":null,"tpm_limit":null,"rpm_limit":null,"model_rpm_limit":%s,"model_tpm_limit":%s,"budget_duration":null,"metadata":%s,"blocked":%s,"tags":%s,"created_at":null}`, idValue, organizationID, models, rpm, tpm, metadata, blocked, tags)
}
func organizationProtocolDynamic(t *testing.T, schema *tfprotov6.Schema, raw string) *tfprotov6.DynamicValue {
	t.Helper()
	value, err := tftypes.ValueFromJSON([]byte(raw), schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	return accessGroupProtocolDynamicValue(t, schema, value)
}

func organizationProtocolOwnedKeys(t *testing.T, private []byte, key string) []string {
	t.Helper()
	values := map[string]json.RawMessage{}
	if len(private) == 0 {
		return nil
	}
	if err := json.Unmarshal(private, &values); err != nil {
		t.Fatalf("decode protocol private state: %v", err)
	}
	raw, exists := values[key]
	if !exists {
		return nil
	}
	var encoded []byte
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatalf("decode protocol private ownership bytes: %v", err)
	}
	var keys []string
	if err := decodeJSONUseNumber(encoded, &keys); err != nil {
		t.Fatalf("decode protocol private ownership keys: %v", err)
	}
	return keys
}

func TestOrganizationNumericMapRemovalFrameworkPlans(t *testing.T) {
	ctx := context.Background()
	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemas, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemas.Diagnostics) {
		t.Fatalf("schemas: %v %v", err, schemas.Diagnostics)
	}
	schema := schemas.ResourceSchemas["litellm_organization"]
	fullRPM, fullTPM := `{"a":1,"b":2}`, `{"a":10,"b":20}`
	state := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", fullRPM, fullTPM, false))
	config := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", fullRPM, fullTPM, true))
	seed, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: config, PriorState: state, ProposedNewState: state})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(seed.Diagnostics) || len(seed.RequiresReplace) != 0 || len(seed.PlannedPrivate) == 0 {
		t.Fatalf("seed: %v %v replace=%v private=%x", err, seed.Diagnostics, seed.RequiresReplace, seed.PlannedPrivate)
	}
	for _, test := range []struct {
		name, rpm string
		blocked   bool
	}{{"change", `{"a":3,"b":2}`, false}, {"addition", `{"a":1,"b":2,"c":3}`, false}, {"removal", `{"a":1}`, true}, {"empty", `{}`, true}, {"null", `null`, true}} {
		t.Run(test.name, func(t *testing.T) {
			planned := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", test.rpm, fullTPM, false))
			configured := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", test.rpm, fullTPM, true))
			response, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: configured, PriorState: state, ProposedNewState: planned, PriorPrivate: seed.PlannedPrivate})
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if got := accessGroupProtocolDiagnosticsHaveError(response.Diagnostics); got != test.blocked {
				t.Fatalf("plan errors=%t want=%t: %v", got, test.blocked, response.Diagnostics)
			}
			if len(response.RequiresReplace) != 0 {
				t.Fatalf("unsafe replacement planned: %v", response.RequiresReplace)
			}
			if test.blocked {
				if !bytes.Equal(response.PlannedPrivate, seed.PlannedPrivate) {
					t.Fatalf("blocked plan changed private ownership state: before=%x after=%x", seed.PlannedPrivate, response.PlannedPrivate)
				}
				rendered := ""
				for _, diagnostic := range response.Diagnostics {
					rendered += diagnostic.Summary + " " + diagnostic.Detail
				}
				for _, required := range []string{"Unsafe Organization Per-Model Limit Removal", "merge", "teams", "memberships", "keys", "Restore"} {
					if !strings.Contains(rendered, required) {
						t.Fatalf("diagnostic %q missing %q", rendered, required)
					}
				}
			}
		})
	}
	for _, rpm := range []string{`{"a":1}`, `{}`, `null`} {
		configured := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", rpm, fullTPM, true))
		planned := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", rpm, fullTPM, false))
		response, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: configured, PriorState: state, ProposedNewState: planned})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || len(response.RequiresReplace) != 0 {
			t.Fatalf("unowned %s: %v %v %v", rpm, err, response.Diagnostics, response.RequiresReplace)
		}
		values := protocolInt64Map(t, protocolAttributeMap(t, schema, response.PlannedState)["model_rpm_limit"])
		if values["a"] != 1 || values["b"] != 2 {
			t.Fatalf("false clear: %#v", values)
		}
	}
}

func TestOrganizationNumericMapRemovalRefreshRetiresOwnedKeys(t *testing.T) {
	ctx := context.Background()
	var remoteStage atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/organization/info" {
			http.NotFound(writer, request)
			return
		}
		if request.URL.Query().Get("organization_id") == "org-import" {
			_, _ = writer.Write([]byte(`{"organization_info":{"organization_id":"org-import","organization_alias":"imported","metadata":{"model_rpm_limit":{"imported":7},"model_tpm_limit":{}}}}`))
			return
		}
		if remoteStage.Load() == 0 {
			_, _ = writer.Write([]byte(`{"organization_info":{"organization_id":"org-1","organization_alias":"acme","metadata":{"model_rpm_limit":{"b":2},"model_tpm_limit":{"a":10,"b":20}}}}`))
			return
		}
		_, _ = writer.Write([]byte(`{"organization_info":{"organization_id":"org-1","organization_alias":"acme","metadata":{"model_rpm_limit":{"external":9},"model_tpm_limit":{"a":10,"b":20}}}}`))
	}))
	defer server.Close()

	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemas, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemas.Diagnostics) {
		t.Fatalf("schemas: %v %v", err, schemas.Diagnostics)
	}
	providerValue, err := tftypes.ValueFromJSON(
		[]byte(`{"api_base":"`+server.URL+`","api_key":"test-key","insecure_skip_verify":null,"litellm_changed_by":null}`),
		schemas.Provider.ValueType(),
	)
	if err != nil {
		t.Fatal(err)
	}
	configuredProvider, err := protocolServer.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: accessGroupProtocolDynamicValue(t, schemas.Provider, providerValue)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configuredProvider.Diagnostics) {
		t.Fatalf("configure: %v, %v", err, configuredProvider.Diagnostics)
	}

	schema := schemas.ResourceSchemas["litellm_organization"]
	fullRPM, fullTPM := `{"a":1,"b":2}`, `{"a":10,"b":20}`
	state := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", fullRPM, fullTPM, false))
	seedConfig := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", fullRPM, fullTPM, true))
	seed, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: seedConfig, PriorState: state, ProposedNewState: state})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(seed.Diagnostics) {
		t.Fatalf("seed: %v %v", err, seed.Diagnostics)
	}
	if got := strings.Join(organizationProtocolOwnedKeys(t, seed.PlannedPrivate, organizationModelRPMOwnedPrivateKey), ","); got != "a,b" {
		t.Fatalf("seed ownership = %q", got)
	}

	// With refresh disabled, stale state still proves that a exists and removal
	// remains blocked without changing private ownership.
	keepBConfig := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", `{"b":2}`, fullTPM, true))
	keepBProposed := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", `{"b":2}`, fullTPM, false))
	stale, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: keepBConfig, PriorState: state, ProposedNewState: keepBProposed, PriorPrivate: seed.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(stale.Diagnostics) {
		t.Fatalf("stale removal was not blocked: %v %v", err, stale.Diagnostics)
	}
	if !bytes.Equal(stale.PlannedPrivate, seed.PlannedPrivate) {
		t.Fatal("stale blocked plan changed ownership")
	}

	// A normal read confirms that external migration removed a. Planning the
	// remaining configured b key then retires only a from the marker.
	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: state, Private: seed.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("refresh after migration: %v %v", err, refreshed.Diagnostics)
	}
	if got := protocolInt64Map(t, protocolAttributeMap(t, schema, refreshed.NewState)["model_rpm_limit"]); len(got) != 1 || got["b"] != 2 {
		t.Fatalf("refreshed RPM state = %#v", got)
	}
	retiredA, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: keepBConfig, PriorState: refreshed.NewState, ProposedNewState: keepBProposed, PriorPrivate: refreshed.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(retiredA.Diagnostics) || len(retiredA.RequiresReplace) != 0 {
		t.Fatalf("refreshed retirement plan: %v %v replace=%v", err, retiredA.Diagnostics, retiredA.RequiresReplace)
	}
	if got := strings.Join(organizationProtocolOwnedKeys(t, retiredA.PlannedPrivate, organizationModelRPMOwnedPrivateKey), ","); got != "b" {
		t.Fatalf("partial ownership retirement = %q", got)
	}

	// A second migration removes b while leaving an unowned remote key. Refresh
	// confirms absence, omitted configuration is a no-op, and ownership clears.
	remoteStage.Store(1)
	refreshedAgain, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: refreshed.NewState, Private: retiredA.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshedAgain.Diagnostics) {
		t.Fatalf("second refresh after migration: %v %v", err, refreshedAgain.Diagnostics)
	}
	omittedConfig := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", `null`, fullTPM, true))
	remoteRPM := `{"external":9}`
	omittedProposed := organizationProtocolDynamic(t, schema, organizationProtocolJSON("org-1", remoteRPM, fullTPM, false))
	retiredAll, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: omittedConfig, PriorState: refreshedAgain.NewState, ProposedNewState: omittedProposed, PriorPrivate: refreshedAgain.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(retiredAll.Diagnostics) || len(retiredAll.RequiresReplace) != 0 {
		t.Fatalf("omitted refreshed plan: %v %v replace=%v", err, retiredAll.Diagnostics, retiredAll.RequiresReplace)
	}
	if protocolPrivateHasKey(t, retiredAll.PlannedPrivate, organizationModelRPMOwnedPrivateKey) {
		t.Fatalf("fully retired ownership marker remains: %x", retiredAll.PlannedPrivate)
	}
	if got := protocolInt64Map(t, protocolAttributeMap(t, schema, retiredAll.PlannedState)["model_rpm_limit"]); len(got) != 1 || got["external"] != 9 {
		t.Fatalf("omitted plan was not a remote-map no-op: %#v", got)
	}
	refreshedValue, _ := refreshedAgain.NewState.Unmarshal(schema.ValueType())
	plannedValue, _ := retiredAll.PlannedState.Unmarshal(schema.ValueType())
	if !refreshedValue.Equal(plannedValue) {
		t.Fatal("refreshed ownership retirement planned a resource change")
	}

	// Unknown state cannot prove absence and therefore must retain every owned
	// key without raising a false removal error.
	stateValue, _ := state.Unmarshal(schema.ValueType())
	stateAttributes := map[string]tftypes.Value{}
	if err := stateValue.As(&stateAttributes); err != nil {
		t.Fatal(err)
	}
	stateAttributes["model_rpm_limit"] = tftypes.NewValue(stateAttributes["model_rpm_limit"].Type(), tftypes.UnknownValue)
	unknownState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), stateAttributes))
	unknownPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: omittedConfig, PriorState: unknownState, ProposedNewState: unknownState, PriorPrivate: seed.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(unknownPlan.Diagnostics) {
		t.Fatalf("unknown state plan: %v %v", err, unknownPlan.Diagnostics)
	}
	if got := strings.Join(organizationProtocolOwnedKeys(t, unknownPlan.PlannedPrivate, organizationModelRPMOwnedPrivateKey), ","); got != "a,b" {
		t.Fatalf("unknown state retired ownership = %q", got)
	}

	// Import and its authoritative read adopt remote maps without establishing
	// private removal ownership; omitting the imported map remains safe.
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_organization", ID: "org-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: %v %v", err, imported.Diagnostics)
	}
	importedResource := imported.ImportedResources[0]
	if protocolPrivateHasKey(t, importedResource.Private, organizationModelRPMOwnedPrivateKey) {
		t.Fatal("import established map removal ownership")
	}
	importedRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_organization", CurrentState: importedResource.State, Private: importedResource.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importedRead.Diagnostics) {
		t.Fatalf("import read: %v %v", err, importedRead.Diagnostics)
	}
	importConfigRaw := strings.Replace(organizationProtocolJSON("org-import", `null`, `null`, true), `"organization_alias":"acme"`, `"organization_alias":"imported"`, 1)
	importConfig := organizationProtocolDynamic(t, schema, importConfigRaw)
	importPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: "litellm_organization", Config: importConfig, PriorState: importedRead.NewState, ProposedNewState: importedRead.NewState, PriorPrivate: importedRead.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importPlan.Diagnostics) || protocolPrivateHasKey(t, importPlan.PlannedPrivate, organizationModelRPMOwnedPrivateKey) {
		t.Fatalf("import omission safety: %v %v private=%x", err, importPlan.Diagnostics, importPlan.PlannedPrivate)
	}
}

func TestOrganizationNumericMapMergeMutationAndRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var mutex sync.Mutex
	rpm := map[string]int64{"a": 1, "b": 2}
	tpm := map[string]int64{"a": 10}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mutex.Lock()
		defer mutex.Unlock()
		switch r.URL.Path {
		case "/organization/update":
			body, _ := io.ReadAll(r.Body)
			var payload map[string]interface{}
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Fatal(err)
			}
			if values, ok := payload["model_rpm_limit"].(map[string]interface{}); ok {
				for key, value := range values {
					number, err := exactInt64FromAPI(value)
					if err != nil {
						t.Fatal(err)
					}
					rpm[key] = number
				}
			}
			_, _ = w.Write([]byte(`{}`))
		case "/organization/info":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"organization_info": map[string]interface{}{"organization_id": "org-1", "organization_alias": "acme", "metadata": map[string]interface{}{"model_rpm_limit": rpm, "model_tpm_limit": tpm}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
	request, err := (&OrganizationResource{}).buildOrganizationRequest(ctx, &OrganizationResourceModel{OrganizationID: types.StringValue("org-1"), OrganizationAlias: types.StringValue("acme"), ModelRPMLimit: types.MapValueMust(types.Int64Type, map[string]attr.Value{"a": types.Int64Value(3), "c": types.Int64Value(4)})})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DoRequestWithResponse(ctx, http.MethodPatch, "/organization/update", request, nil); err != nil {
		t.Fatal(err)
	}
	state := &OrganizationResourceModel{OrganizationID: types.StringValue("org-1"), ModelRPMLimit: types.MapValueMust(types.Int64Type, map[string]attr.Value{"a": types.Int64Value(1), "b": types.Int64Value(2), "c": types.Int64Value(4)}), ModelTPMLimit: types.MapValueMust(types.Int64Type, map[string]attr.Value{"a": types.Int64Value(10)})}
	if err := (&OrganizationResource{client: client}).readOrganization(ctx, state); err != nil {
		t.Fatal(err)
	}
	values := map[string]int64{}
	if d := state.ModelRPMLimit.ElementsAs(ctx, &values, false); d.HasError() {
		t.Fatal(d)
	}
	if values["a"] != 3 || values["b"] != 2 || values["c"] != 4 {
		t.Fatalf("merge lost keys: %#v", values)
	}
}
