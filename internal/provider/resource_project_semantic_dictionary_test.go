package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestProjectSemanticSchema(t *testing.T) {
	var response resource.SchemaResponse
	(&ProjectResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	attribute, ok := response.Schema.Attributes["metadata_json"].(resourceschema.StringAttribute)
	if response.Schema.Version != 1 || !ok || !attribute.Optional || !attribute.Computed || !attribute.Sensitive {
		t.Fatalf("metadata_json schema=%#v version=%d", response.Schema.Attributes["metadata_json"], response.Schema.Version)
	}
}

func TestProjectSemanticCreateRecoveryClassification(t *testing.T) {
	for _, test := range []struct {
		name     string
		accepted bool
		err      error
		want     bool
	}{
		{name: "accepted malformed response", accepted: true, err: &safeResponseError{statusCode: http.StatusOK, dispatched: true, accepted: true}, want: true},
		{name: "transport loss", err: &safeTransportError{kind: "transport failure", dispatched: true}, want: true},
		{name: "dispatched cancellation", err: &safeTransportError{kind: "canceled", canceled: true, dispatched: true}, want: true},
		{name: "known rejection", err: &APIError{StatusCode: http.StatusBadRequest}},
		{name: "canceled rejection", err: &safeResponseError{statusCode: http.StatusBadRequest, canceled: true, dispatched: true, stage: safeResponseFailureStatusBodyRead}},
		{name: "pre-dispatch", err: &safeTransportError{kind: "local failure"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := semanticCreateRecoveryRequired(test.accepted, test.err); got != test.want {
				t.Fatalf("recovery=%t want=%t", got, test.want)
			}
		})
	}
}

func TestProjectSemanticDictionaryExactValuesValidationAndOverlap(t *testing.T) {
	ctx := context.Background()
	raw := `{"integer":9007199254740993123456789,"decimal":1.25,"native":true,"string":"true","nil":null,"list":[null,false,"1",1],"empty":{},"nested":{"a/b":{"~":1}}}`
	prepared, err := prepareProjectSemanticDictionary(ctx, types.StringValue(raw), types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.object["integer"] != json.Number("9007199254740993123456789") || prepared.object["decimal"] != json.Number("1.25") || prepared.object["native"] != true || prepared.object["string"] != "true" {
		t.Fatalf("exact identities lost: %#v", prepared.object)
	}
	want := semanticDictionaryPathSet{"/integer": true, "/decimal": true, "/native": true, "/string": true, "/nil": true, "/list": true, "/empty": true, "/nested/a~1b/~0": true}
	if !reflect.DeepEqual(prepared.provenance.TerraformOwned, want) {
		t.Fatalf("paths=%#v want=%#v", prepared.provenance.TerraformOwned, want)
	}
	legacy := types.MapValueMust(types.StringType, map[string]attr.Value{"same": types.StringValue("true")})
	for name, test := range map[string]struct {
		raw    string
		legacy types.Map
	}{
		"duplicate": {raw: `{"a":1,"a":2}`, legacy: types.MapNull(types.StringType)},
		"root":      {raw: `[1]`, legacy: types.MapNull(types.StringType)},
		"null":      {raw: `null`, legacy: types.MapNull(types.StringType)},
		"legacy":    {raw: `{"same":true}`, legacy: legacy},
		"tags":      {raw: `{"tags":[]}`, legacy: types.MapNull(types.StringType)},
		"rpm":       {raw: `{"model_rpm_limit":{}}`, legacy: types.MapNull(types.StringType)},
		"tpm":       {raw: `{"model_tpm_limit":{}}`, legacy: types.MapNull(types.StringType)},
		"lossy":     {raw: `{"a":0.10000000000000001}`, legacy: types.MapNull(types.StringType)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := prepareProjectSemanticDictionary(ctx, types.StringValue(test.raw), test.legacy); err == nil {
				t.Fatal("invalid semantic dictionary accepted")
			}
		})
	}
}

func TestProjectSemanticCreateOverlayKeepsLegacyScalarStringsAndDedicatedRoots(t *testing.T) {
	ctx := context.Background()
	prepared, err := prepareProjectSemanticDictionary(ctx, types.StringValue(`{"native":true}`), types.MapValueMust(types.StringType, map[string]attr.Value{"legacy": types.StringValue("true")}))
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]interface{}{"metadata": map[string]interface{}{
		"legacy": "true", "tags": []string{"one"}, "model_rpm_limit": map[string]int64{"m": 9}, "model_tpm_limit": map[string]int64{"m": 11},
	}}
	if err := overlayProjectCreateSemantic(ctx, request, prepared); err != nil {
		t.Fatal(err)
	}
	metadata := request["metadata"].(map[string]interface{})
	if metadata["legacy"] != "true" || metadata["native"] != true || metadata["model_rpm_limit"].(map[string]interface{})["m"] != json.Number("9") || len(metadata["tags"].([]interface{})) != 1 {
		t.Fatalf("overlay=%#v", metadata)
	}
}

func TestProjectMetadataReplacementPreservesSiblingsAndRemovesOwnedPaths(t *testing.T) {
	ctx := context.Background()
	priorJSON := types.StringValue(`{"owned":{"remove":1,"keep":2}}`)
	priorPrepared, _ := prepareProjectSemanticDictionary(ctx, priorJSON, types.MapNull(types.StringType))
	nextPrepared, _ := prepareProjectSemanticDictionary(ctx, types.StringValue(`{"owned":{"keep":3}}`), types.MapNull(types.StringType))
	prior := ProjectResourceModel{
		Metadata: types.MapValueMust(types.StringType, map[string]attr.Value{"legacy": types.StringValue("old")}), MetadataJSON: priorJSON,
		Tags:          types.ListValueMust(types.StringType, []attr.Value{types.StringValue("old")}),
		ModelRPMLimit: types.MapValueMust(types.Int64Type, map[string]attr.Value{"old": types.Int64Value(1)}), ModelTPMLimit: types.MapNull(types.Int64Type),
	}
	plan := prior
	plan.Metadata = types.MapValueMust(types.StringType, map[string]attr.Value{"legacy": types.StringValue(`{"native":true}`), "scalar": types.StringValue("1")})
	plan.MetadataJSON = types.StringValue(`{"owned":{"keep":3}}`)
	plan.Tags = types.ListValueMust(types.StringType, []attr.Value{types.StringValue("new")})
	plan.ModelRPMLimit = types.MapValueMust(types.Int64Type, map[string]attr.Value{"new": types.Int64Value(9)})
	plan.ModelTPMLimit = types.MapValueMust(types.Int64Type, map[string]attr.Value{})
	remote := map[string]interface{}{"api": map[string]interface{}{"preserved": true}, "legacy": "old", "owned": map[string]interface{}{"remove": json.Number("1"), "keep": json.Number("2")}, "tags": []interface{}{"old"}, "model_rpm_limit": map[string]interface{}{"old": json.Number("1")}}
	replacement, err := composeProjectMetadataReplacement(ctx, remote, plan, prior, priorPrepared.provenance, nextPrepared)
	if err != nil {
		t.Fatal(err)
	}
	owned := replacement["owned"].(map[string]interface{})
	if !reflect.DeepEqual(replacement["api"], remote["api"]) || replacement["scalar"] != "1" || owned["keep"] != json.Number("3") {
		t.Fatalf("replacement=%#v", replacement)
	}
	if _, present := owned["remove"]; present {
		t.Fatalf("removed path retained: %#v", owned)
	}
	if replacement["tags"].([]interface{})[0] != "new" || replacement["model_rpm_limit"].(map[string]interface{})["new"] != json.Number("9") || len(replacement["model_tpm_limit"].(map[string]interface{})) != 0 {
		t.Fatalf("dedicated roots=%#v", replacement)
	}
}

func TestProjectSemanticPendingBudgetMarkerCanonicalAndStrict(t *testing.T) {
	ctx := context.Background()
	fields := projectPendingBudgetFields{"soft_budget": true, "budget_duration": true, "max_budget": true}
	raw, err := encodeProjectPendingBudget(ctx, fields)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(`{"version":1,"fields":["budget_duration","max_budget","soft_budget"]}`)
	if !bytes.Equal(raw, want) {
		t.Fatalf("canonical marker=%s want=%s", raw, want)
	}
	decoded, err := decodeProjectPendingBudget(ctx, raw)
	if err != nil || !reflect.DeepEqual(decoded, fields) {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}
	patch := map[string]interface{}{"budget_duration": "30d", "budget_reset_at": nil, "budget_id": "must-not-be-recorded"}
	if got := projectPendingBudgetFromPatch(patch); !reflect.DeepEqual(got, projectPendingBudgetFields{"budget_duration": true}) {
		t.Fatalf("patch marker fields=%#v", got)
	}
	for name, malformed := range map[string][]byte{
		"duplicate":    []byte(`{"version":1,"fields":["max_budget","max_budget"]}`),
		"unknown":      []byte(`{"version":1,"fields":["metadata_json"]}`),
		"noncanonical": []byte(`{"fields":["max_budget"],"version":1}`),
		"unsorted":     []byte(`{"version":1,"fields":["soft_budget","max_budget"]}`),
		"empty":        []byte(`{"version":1,"fields":[]}`),
		"extra":        []byte(`{"version":1,"fields":["max_budget"],"value":1}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeProjectPendingBudget(ctx, malformed); err == nil {
				t.Fatalf("malformed marker accepted: %s", malformed)
			}
		})
	}
	if _, err := encodeProjectPendingBudget(ctx, projectPendingBudgetFields{"max_budget": false}); err == nil {
		t.Fatal("false field marker encoded")
	}
	if _, err := encodeProjectPendingBudget(ctx, projectPendingBudgetFields{"unknown": true}); err == nil {
		t.Fatal("unknown field marker encoded")
	}
}

func TestProjectSemanticHeterogeneousLegacyNonAdoption(t *testing.T) {
	ctx := context.Background()
	remote := map[string]interface{}{"string": "remote", "native": true, "list": []interface{}{json.Number("1")}}
	unconfigured := projectSemanticOwnership{provenance: projectUnconfiguredSemanticProvenance()}
	projected, err := projectProjectLegacyMetadata(ctx, types.MapNull(types.StringType), remote, unconfigured)
	if err != nil || !projected.IsNull() {
		t.Fatalf("unconfigured heterogeneous projection=%s err=%v", projected, err)
	}
	managed := types.MapValueMust(types.StringType, map[string]attr.Value{"string": types.StringValue("prior")})
	projected, err = projectProjectLegacyMetadata(ctx, managed, remote, unconfigured)
	if err != nil || projected.IsNull() || len(projected.Elements()) != 1 || projected.Elements()["string"].(types.String).ValueString() != "remote" {
		t.Fatalf("managed heterogeneous projection=%s err=%v", projected, err)
	}
	if _, present := projected.Elements()["native"]; present {
		t.Fatalf("heterogeneous native sibling was adopted: %s", projected)
	}
	remote["string"] = true
	if _, err := projectProjectLegacyMetadata(ctx, managed, remote, unconfigured); err == nil {
		t.Fatal("native scalar at an already-managed legacy key was coerced to a string")
	}
	configured, err := prepareProjectSemanticDictionary(ctx, types.StringValue(`{"owned":true}`), managed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := projectProjectLegacyMetadata(ctx, managed, remote, projectSemanticOwnership{provenance: configured.provenance}); err == nil {
		t.Fatal("configured semantic projection coerced a managed native scalar")
	}
}

func TestProjectSemanticAcceptedCreateRequiresExactTeam(t *testing.T) {
	ctx := context.Background()
	const id, teamID = "project-team-confirm", "team-confirm"
	var remoteTeam interface{}
	var includeTeam bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		object := map[string]interface{}{"project_id": id, "metadata": map[string]interface{}{}, "litellm_budget_table": map[string]interface{}{}}
		if includeTeam {
			object["team_id"] = remoteTeam
		}
		_ = json.NewEncoder(writer).Encode(object)
	}))
	defer server.Close()
	resource := &ProjectResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	for _, test := range []struct {
		name    string
		include bool
		team    interface{}
	}{
		{name: "missing"},
		{name: "null", include: true},
		{name: "wrong", include: true, team: "other-team"},
	} {
		t.Run(test.name, func(t *testing.T) {
			includeTeam, remoteTeam = test.include, test.team
			data := partialProjectSemanticRecoveryState(ProjectResourceModel{TeamID: types.StringValue(teamID)}, id)
			if err := resource.readProjectWithOwnership(ctx, &data, false, projectSemanticOwnership{provenance: projectUnconfiguredSemanticProvenance(), acceptedCreate: true, fresh: true}); err == nil {
				t.Fatalf("%s accepted team confirmation succeeded", test.name)
			}
			if data.TeamID.ValueString() != teamID {
				t.Fatalf("%s team state changed to %s", test.name, data.TeamID)
			}
		})
	}
	includeTeam, remoteTeam = true, "wrong-team"
	data := partialProjectSemanticRecoveryState(ProjectResourceModel{TeamID: types.StringValue(teamID)}, id)
	if err := resource.readProjectWithOwnership(ctx, &data, false, projectSemanticOwnership{provenance: projectUnconfiguredSemanticProvenance()}); err == nil {
		t.Fatal("ordinary authoritative read adopted a different team")
	}
	includeTeam, remoteTeam = true, teamID
	data = partialProjectSemanticRecoveryState(ProjectResourceModel{TeamID: types.StringValue(teamID), BudgetID: types.StringValue("shared-budget")}, id)
	if err := resource.readProjectWithOwnership(ctx, &data, false, projectSemanticOwnership{provenance: projectUnconfiguredSemanticProvenance(), acceptedCreate: true, fresh: true}); err == nil {
		t.Fatal("accepted recovery cleared an unconfirmed shared budget association")
	}
	data = partialProjectSemanticRecoveryState(ProjectResourceModel{TeamID: types.StringValue(teamID)}, id)
	if err := resource.readProjectWithOwnership(ctx, &data, false, projectSemanticOwnership{provenance: projectUnconfiguredSemanticProvenance(), acceptedCreate: true, fresh: true}); err != nil || data.TeamID.ValueString() != teamID {
		t.Fatalf("exact team confirmation failed: team=%s err=%v", data.TeamID, err)
	}
}

func TestProjectSemanticKnownImportedBudgetMismatchFailsClosed(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"project_id": "project-budget-drift", "team_id": "team-budget-drift", "budget_id": "budget-new", "metadata": map[string]interface{}{},
			"litellm_budget_table": map[string]interface{}{"budget_id": "budget-new"},
		})
	}))
	defer server.Close()
	data := ProjectResourceModel{ID: types.StringValue("project-budget-drift"), TeamID: types.StringValue("team-budget-drift"), BudgetID: types.StringValue("budget-old")}
	resource := &ProjectResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}}
	if err := resource.readProjectWithOwnership(ctx, &data, true, projectSemanticOwnership{provenance: projectUnconfiguredSemanticProvenance()}); err == nil {
		t.Fatal("known imported shared-budget reassignment was adopted")
	}
	if data.BudgetID.ValueString() != "budget-old" {
		t.Fatalf("failed read changed prior budget authority: %s", data.BudgetID)
	}
}

func TestProjectSemanticFormattingPendingReconciliationAndPrivateValidation(t *testing.T) {
	ctx := context.Background()
	prior, _ := prepareProjectSemanticDictionary(ctx, types.StringValue(`{"b":2,"a":1}`), types.MapNull(types.StringType))
	changed, err := projectSemanticNeedsChange(ctx, types.StringValue("{\n  \"a\": 1, \"b\": 2\n}"), types.StringValue(`{"b":2,"a":1}`), prior.provenance)
	if err != nil || changed {
		t.Fatalf("format-only change=%t err=%v", changed, err)
	}
	for _, test := range []struct {
		prior, next string
		committed   map[string]interface{}
		not, mixed  map[string]interface{}
	}{
		{`{"shape":1}`, `{"shape":{"a":1,"b":2}}`, map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1"), "b": json.Number("2")}}, map[string]interface{}{"shape": json.Number("1")}, map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1")}}},
		{`{"shape":{"a":1,"b":2}}`, `{"shape":1}`, map[string]interface{}{"shape": json.Number("1")}, map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1"), "b": json.Number("2")}}, map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1")}}},
	} {
		old, _ := prepareProjectSemanticDictionary(ctx, types.StringValue(test.prior), types.MapNull(types.StringType))
		next, _ := prepareProjectSemanticDictionary(ctx, types.StringValue(test.next), types.MapNull(types.StringType))
		confirmation, _ := next.updateOwnership(ctx, old.provenance)
		pending := pendingProjectSemanticTransition(confirmation)
		ownership := projectSemanticOwnership{provenance: old.provenance, pending: pending}
		committed, reconcile, err := resolveProjectSemanticPending(ctx, test.committed, ownership)
		if err != nil || !reconcile.Committed || !modelSemanticDictionaryPathSetsEqual(committed.provenance.TerraformOwned, next.provenance.TerraformOwned) {
			t.Fatalf("committed=%#v reconcile=%#v err=%v", committed, reconcile, err)
		}
		not, reconcile, err := resolveProjectSemanticPending(ctx, test.not, ownership)
		if err != nil || reconcile.Committed || !modelSemanticDictionaryPathSetsEqual(not.provenance.TerraformOwned, old.provenance.TerraformOwned) {
			t.Fatalf("not committed=%#v reconcile=%#v err=%v", not, reconcile, err)
		}
		if _, _, err := resolveProjectSemanticPending(ctx, test.mixed, ownership); err == nil {
			t.Fatal("partial transition accepted")
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := prepareProjectSemanticDictionary(canceled, types.StringValue(`{"secret":true}`), types.MapNull(types.StringType)); err == nil {
		t.Fatal("canceled conversion succeeded")
	}
	raw, _ := encodeProjectSemanticProvenance(ctx, prior.provenance)
	for _, malformed := range [][]byte{nil, []byte(`null`), append([]byte(" "), raw...)} {
		if _, err := decodeProjectSemanticProvenance(ctx, malformed, types.StringValue(`{"b":2,"a":1}`)); err == nil {
			t.Fatal("malformed private provenance accepted")
		}
	}
}
