package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestOrganizationSemanticCreateRecoveryClassification(t *testing.T) {
	for _, test := range []struct {
		name     string
		accepted bool
		err      error
		want     bool
	}{
		{name: "accepted body failure", accepted: true, err: &safeResponseError{statusCode: http.StatusOK, dispatched: true, accepted: true}, want: true},
		{name: "dispatched transport loss", err: &safeTransportError{kind: "transport failure", dispatched: true}, want: true},
		{name: "dispatched cancellation", err: &safeTransportError{kind: "canceled", canceled: true, dispatched: true}, want: true},
		{name: "terminal HTTP rejection", err: &APIError{StatusCode: http.StatusBadRequest}},
		{name: "canceled known HTTP rejection", err: &safeResponseError{statusCode: http.StatusBadRequest, canceled: true, dispatched: true, stage: safeResponseFailureStatusBodyRead}},
		{name: "deadline known HTTP rejection", err: &safeResponseError{statusCode: http.StatusServiceUnavailable, deadline: true, dispatched: true, stage: safeResponseFailureStatusBodyRead}},
		{name: "terminal known HTTP rejection", err: &safeResponseError{statusCode: http.StatusBadGateway, terminal: true, dispatched: true, stage: safeResponseFailureStatusBodyRead}},
		{name: "local failure", err: &safeTransportError{kind: "local failure"}},
		{name: "no failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := organizationSemanticCreateRecoveryRequired(test.accepted, test.err); got != test.want {
				t.Fatalf("recovery=%t want=%t classification=%#v", got, test.want, ClassifyHTTPFailure(test.err))
			}
		})
	}
}

func TestOrganizationSemanticSchema(t *testing.T) {
	var response resource.SchemaResponse
	(&OrganizationResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	attribute, ok := response.Schema.Attributes["metadata_json"].(resourceschema.StringAttribute)
	if response.Schema.Version != 1 || !ok || !attribute.Optional || !attribute.Computed || !attribute.Sensitive {
		t.Fatalf("metadata_json schema=%#v version=%d", response.Schema.Attributes["metadata_json"], response.Schema.Version)
	}
}

func organizationSemanticStringMap(values map[string]string) types.Map {
	elements := make(map[string]types.String, len(values))
	for name, value := range values {
		elements[name] = types.StringValue(value)
	}
	result, diagnostics := types.MapValueFrom(context.Background(), types.StringType, elements)
	if diagnostics.HasError() {
		panic(diagnostics)
	}
	return result
}

func TestOrganizationSemanticDictionaryExactValuesAndOwnership(t *testing.T) {
	raw := `{"integer":9007199254740993123456789,"decimal":1.25,"native":true,"string":"true","nil":null,"list":[null,false,"1",1],"empty":{},"nested":{"a/b":{"~":1}}}`
	prepared, err := prepareOrganizationSemanticDictionary(context.Background(), types.StringValue(raw), types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.object["integer"] != json.Number("9007199254740993123456789") || prepared.object["decimal"] != json.Number("1.25") || prepared.object["native"] != true || prepared.object["string"] != "true" {
		t.Fatalf("exact JSON identities were lost: %#v", prepared.object)
	}
	wantPaths := semanticDictionaryPathSet{
		"/integer": true, "/decimal": true, "/native": true, "/string": true, "/nil": true,
		"/list": true, "/empty": true, "/nested/a~1b/~0": true,
	}
	if !reflect.DeepEqual(prepared.provenance.TerraformOwned, wantPaths) {
		t.Fatalf("paths=%#v want=%#v", prepared.provenance.TerraformOwned, wantPaths)
	}

	request := map[string]interface{}{"metadata": map[string]interface{}{"legacy": "1"}}
	if err := overlayOrganizationCreateSemantic(context.Background(), request, prepared); err != nil {
		t.Fatal(err)
	}
	metadata := request["metadata"].(map[string]interface{})
	if metadata["legacy"] != "1" || metadata["integer"] != json.Number("9007199254740993123456789") {
		t.Fatalf("create overlay=%#v", metadata)
	}
}

func TestOrganizationSemanticDictionaryValidation(t *testing.T) {
	for name, raw := range map[string]string{
		"duplicate":     `{"a":1,"a":2}`,
		"nonobject":     `[1]`,
		"null root":     `null`,
		"lossy decimal": `{"a":0.10000000000000001}`,
		"overflow":      `{"a":1e999}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := prepareOrganizationSemanticDictionary(context.Background(), types.StringValue(raw), types.MapNull(types.StringType)); err == nil {
				t.Fatalf("invalid JSON accepted: %s", raw)
			}
		})
	}
	for name, test := range map[string]struct {
		raw    string
		legacy types.Map
	}{
		"legacy overlap": {`{"same":true}`, organizationSemanticStringMap(map[string]string{"same": "legacy"})},
		"rpm reserved":   {`{"model_rpm_limit":{}}`, types.MapNull(types.StringType)},
		"tpm reserved":   {`{"model_tpm_limit":{}}`, types.MapNull(types.StringType)},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := prepareOrganizationSemanticDictionary(context.Background(), types.StringValue(test.raw), test.legacy); err == nil {
				t.Fatal("overlapping root accepted")
			}
		})
	}
	if _, err := prepareOrganizationSemanticDictionary(context.Background(), types.StringValue(`{"a":1.5,"b":1e3}`), types.MapNull(types.StringType)); err != nil {
		t.Fatalf("float-safe decimals rejected: %v", err)
	}
}

func TestOrganizationSemanticDictionaryEmptyObjectAndFormatting(t *testing.T) {
	prepared, err := prepareOrganizationSemanticDictionary(context.Background(), types.StringValue(`{}`), types.MapNull(types.StringType))
	if err != nil || !prepared.provenance.Configured || len(prepared.provenance.TerraformOwned) != 0 {
		t.Fatalf("prepared=%#v err=%v", prepared, err)
	}
	request := map[string]interface{}{}
	if err := overlayOrganizationCreateSemantic(context.Background(), request, prepared); err != nil {
		t.Fatal(err)
	}
	if metadata, present := request["metadata"].(map[string]interface{}); !present || len(metadata) != 0 {
		t.Fatalf("explicit empty object was omitted: %#v", request)
	}
	provenanceRaw, err := encodeOrganizationSemanticProvenance(context.Background(), prepared.provenance)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeOrganizationSemanticProvenance(context.Background(), provenanceRaw, types.StringValue(`{}`))
	if err != nil || !decoded.Configured {
		t.Fatalf("decoded=%#v err=%v", decoded, err)
	}

	prior, _ := prepareOrganizationSemanticDictionary(context.Background(), types.StringValue(`{"b":2,"a":1}`), types.MapNull(types.StringType))
	changed, err := organizationSemanticNeedsChange(context.Background(), types.StringValue("{\n  \"a\": 1, \"b\": 2\n}"), types.StringValue(`{"b":2,"a":1}`), prior.provenance)
	if err != nil || changed {
		t.Fatalf("format-only change: changed=%t err=%v", changed, err)
	}
}

func TestOrganizationMetadataReplacementPreservesSiblingsAndDedicatedRates(t *testing.T) {
	ctx := context.Background()
	priorJSON := types.StringValue(`{"owned":{"remove":1,"keep":2}}`)
	priorPrepared, _ := prepareOrganizationSemanticDictionary(ctx, priorJSON, types.MapNull(types.StringType))
	nextPrepared, _ := prepareOrganizationSemanticDictionary(ctx, types.StringValue(`{"owned":{"keep":3}}`), types.MapNull(types.StringType))
	prior := OrganizationResourceModel{
		Metadata:      types.MapValueMust(types.StringType, map[string]attr.Value{"legacy": types.StringValue("old")}),
		MetadataJSON:  priorJSON,
		ModelRPMLimit: types.MapValueMust(types.Int64Type, map[string]attr.Value{"old": types.Int64Value(1)}),
		ModelTPMLimit: types.MapNull(types.Int64Type),
	}
	plan := prior
	plan.Metadata = types.MapValueMust(types.StringType, map[string]attr.Value{"legacy": types.StringValue(`{"native":true}`)})
	plan.MetadataJSON = types.StringValue(`{"owned":{"keep":3}}`)
	plan.ModelRPMLimit = types.MapValueMust(types.Int64Type, map[string]attr.Value{"new": types.Int64Value(9)})
	plan.ModelTPMLimit = types.MapValueMust(types.Int64Type, map[string]attr.Value{"m": types.Int64Value(11)})
	remote := map[string]interface{}{
		"api": map[string]interface{}{"preserved": true}, "legacy": "old",
		"owned":           map[string]interface{}{"remove": json.Number("1"), "keep": json.Number("2")},
		"model_rpm_limit": map[string]interface{}{"old": json.Number("1")},
	}
	replacement, err := composeOrganizationMetadataReplacement(ctx, remote, plan, prior, priorPrepared.provenance, nextPrepared)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replacement["api"], remote["api"]) || replacement["legacy"].(map[string]interface{})["native"] != true {
		t.Fatalf("siblings/legacy not preserved: %#v", replacement)
	}
	owned := replacement["owned"].(map[string]interface{})
	if owned["keep"] != json.Number("3") {
		t.Fatalf("owned=%#v", owned)
	}
	if _, present := owned["remove"]; present {
		t.Fatalf("removed leaf retained: %#v", owned)
	}
	if replacement["model_rpm_limit"].(map[string]interface{})["new"] != json.Number("9") || replacement["model_tpm_limit"].(map[string]interface{})["m"] != json.Number("11") {
		t.Fatalf("dedicated rates=%#v", replacement)
	}
}

func TestOrganizationPendingShapeReconciliation(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name      string
		prior     string
		next      string
		committed map[string]interface{}
		not       map[string]interface{}
		partial   map[string]interface{}
	}{
		{
			name: "expansion", prior: `{"shape":1}`, next: `{"shape":{"a":1,"b":2}}`,
			committed: map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1"), "b": json.Number("2")}},
			not:       map[string]interface{}{"shape": json.Number("1")},
			partial:   map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1")}},
		},
		{
			name: "contraction", prior: `{"shape":{"a":1,"b":2}}`, next: `{"shape":1}`,
			committed: map[string]interface{}{"shape": json.Number("1")},
			not:       map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1"), "b": json.Number("2")}},
			partial:   map[string]interface{}{"shape": map[string]interface{}{"a": json.Number("1")}},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			prior, _ := prepareOrganizationSemanticDictionary(ctx, types.StringValue(test.prior), types.MapNull(types.StringType))
			next, _ := prepareOrganizationSemanticDictionary(ctx, types.StringValue(test.next), types.MapNull(types.StringType))
			ownership, err := next.updateOwnership(ctx, prior.provenance)
			if err != nil {
				t.Fatal(err)
			}
			pending := pendingOrganizationSemanticTransition(ownership)
			if !pending.any() {
				t.Fatal("shape transition did not create pending provenance")
			}
			// Failed apply retains prior public/private state. Only the value-free
			// target/removal transition is added to private recovery provenance.
			ownership = organizationSemanticOwnership{provenance: prior.provenance, pending: pending, fresh: true}
			committed, reconciliation, err := resolveOrganizationSemanticPending(ctx, test.committed, ownership)
			if err != nil || !reconciliation.Present || !reconciliation.Committed || !modelSemanticDictionaryPathSetsEqual(committed.provenance.TerraformOwned, next.provenance.TerraformOwned) {
				t.Fatalf("committed=%#v reconcile=%#v err=%v", committed, reconciliation, err)
			}
			notCommitted, reconciliation, err := resolveOrganizationSemanticPending(ctx, test.not, ownership)
			if err != nil || !reconciliation.Present || reconciliation.Committed || !modelSemanticDictionaryPathSetsEqual(notCommitted.provenance.TerraformOwned, prior.provenance.TerraformOwned) {
				t.Fatalf("not committed=%#v reconcile=%#v err=%v", notCommitted, reconciliation, err)
			}
			if _, _, err := resolveOrganizationSemanticPending(ctx, test.partial, ownership); err == nil {
				t.Fatal("partial mixed shape was accepted")
			}
		})
	}
}

func TestOrganizationSemanticCancellationAndPrivateValidation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := prepareOrganizationSemanticDictionary(ctx, types.StringValue(`{"secret":true}`), types.MapNull(types.StringType)); err == nil {
		t.Fatal("canceled semantic conversion succeeded")
	}
	configured, _ := prepareOrganizationSemanticDictionary(context.Background(), types.StringValue(`{"secret":true}`), types.MapNull(types.StringType))
	raw, _ := encodeOrganizationSemanticProvenance(context.Background(), configured.provenance)
	for _, malformed := range [][]byte{nil, []byte(`null`), append([]byte(" "), raw...), []byte(`{"version":1,"initialized":true,"configured":true,"terraform_owned":["/wrong"],"api_owned":[],"pending_terraform_owned":[],"pending_api_owned":[],"pending_removals":[]}`)} {
		if _, err := decodeOrganizationSemanticProvenance(context.Background(), malformed, types.StringValue(`{"secret":true}`)); err == nil {
			t.Fatalf("malformed private provenance accepted: %s", malformed)
		}
	}
}
