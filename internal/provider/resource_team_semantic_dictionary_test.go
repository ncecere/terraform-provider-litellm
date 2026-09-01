package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestTeamSemanticSchemaAndDirectUpgrade(t *testing.T) {
	ctx := context.Background()
	resourceUnderTest := &TeamResource{}
	var schemaResponse frameworkresource.SchemaResponse
	resourceUnderTest.Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Schema.Version != 2 {
		t.Fatalf("schema version=%d want=2", schemaResponse.Schema.Version)
	}
	attribute, ok := schemaResponse.Schema.Attributes["metadata_json"].(resourceschema.StringAttribute)
	if !ok || !attribute.Optional || !attribute.Computed || !attribute.Sensitive || attribute.Required {
		t.Fatalf("metadata_json schema=%#v", schemaResponse.Schema.Attributes["metadata_json"])
	}
	raw := []byte(`{"id":"team","team_id":"team","team_alias":"alias","metadata":{"legacy":"keep"}}`)
	upgrader := resourceUnderTest.UpgradeState(ctx)[0]
	response := &frameworkresource.UpgradeStateResponse{}
	upgrader.StateUpgrader(ctx, frameworkresource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: raw}}, response)
	if response.Diagnostics.HasError() || response.DynamicValue == nil {
		t.Fatalf("upgrade diagnostics=%v", response.Diagnostics)
	}
	var upgraded map[string]json.RawMessage
	if err := json.Unmarshal(response.DynamicValue.JSON, &upgraded); err != nil {
		t.Fatal(err)
	}
	if string(upgraded["metadata_json"]) != "null" || string(upgraded["metadata"]) != `{"legacy":"keep"}` {
		t.Fatalf("upgraded=%s", response.DynamicValue.JSON)
	}
}

func TestTeamSemanticConfigurationIdentityOverlapAndRemovals(t *testing.T) {
	ctx := context.Background()
	legacy := types.MapValueMust(types.StringType, map[string]attr.Value{"legacy": types.StringValue("value")})
	value := types.StringValue(`{"integer":9007199254740993123456789,"decimal":1.25,"native":true,"nil":null,"list":[1,false,null],"object":{"leaf":"value"},"empty":{}}`)
	prepared, err := prepareTeamSemanticDictionary(ctx, value, legacy)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.object["integer"] != json.Number("9007199254740993123456789") || prepared.object["decimal"] != json.Number("1.25") || !prepared.provenance.TerraformOwned["/list"] || !prepared.provenance.TerraformOwned["/empty"] || !prepared.provenance.TerraformOwned["/object/leaf"] {
		t.Fatalf("prepared=%#v provenance=%#v", prepared.object, prepared.provenance)
	}
	for _, invalid := range []string{`{"legacy":true}`, `{"tags":[]}`, `{"guardrails":[]}`, `{"prompts":[]}`, `{"model_rpm_limit":{}}`, `{"model_tpm_limit":{}}`, `{"rpm_limit_type":"x"}`, `{"tpm_limit_type":"x"}`, `{"team_member_budget_id":"x"}`} {
		if _, err := prepareTeamSemanticDictionary(ctx, types.StringValue(invalid), legacy); err == nil {
			t.Fatalf("reserved overlap accepted: %s", invalid)
		}
	}
	if _, err := teamLegacyMetadataObject(ctx, types.MapValueMust(types.StringType, map[string]attr.Value{"team_member_budget_id": types.StringValue("forbidden")})); err == nil {
		t.Fatal("legacy server-owned ID accepted")
	}

	next, err := prepareTeamSemanticDictionary(ctx, types.StringValue(`{"object":{"leaf":2}}`), types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	ownership, err := next.updateOwnership(ctx, prepared.provenance)
	if err != nil || !pendingTeamSemanticTransition(ownership).Metadata.Active {
		t.Fatalf("removal ownership=%#v err=%v", ownership, err)
	}
}

func TestComposeTeamMetadataReplacementCiphertextAndMemberBudgetID(t *testing.T) {
	ctx := context.Background()
	priorJSON := types.StringValue(`{"logging":[{"callback_vars":{"api_key":"plaintext"}}]}`)
	priorPrepared, err := prepareTeamSemanticDictionary(ctx, priorJSON, types.MapNull(types.StringType))
	if err != nil {
		t.Fatal(err)
	}
	plan := TeamResourceModel{
		MetadataJSON: priorJSON,
		Metadata:     types.MapValueMust(types.StringType, map[string]attr.Value{"legacy": types.StringValue("new")}),
		Tags:         types.ListValueMust(types.StringType, []attr.Value{types.StringValue("tag")}),
	}
	prior := TeamResourceModel{MetadataJSON: priorJSON, Metadata: types.MapValueMust(types.StringType, map[string]attr.Value{"legacy": types.StringValue("old")})}
	remote := mustParseSemanticDictionary(t, `{"logging":[{"callback_vars":{"api_key":"litellm_enc::ciphertext"}}],"legacy":"old","api":{"keep":true}}`)
	request := map[string]interface{}{"team_member_budget": 10.0}
	replacement, reinsert, err := composeTeamMetadataReplacement(ctx, remote, plan, prior, priorPrepared.provenance, priorPrepared, request)
	if err != nil || reinsert {
		t.Fatalf("replacement err=%v reinsert=%v", err, reinsert)
	}
	logging := replacement["logging"].([]interface{})
	callback := logging[0].(map[string]interface{})["callback_vars"].(map[string]interface{})
	if callback["api_key"] != "plaintext" || replacement["legacy"] != "new" || replacement["api"].(map[string]interface{})["keep"] != true {
		t.Fatalf("replacement=%#v", replacement)
	}

	unowned := mustParseSemanticDictionary(t, `{"callback_settings":{"callback_vars":{"api_key":"litellm_enc::ciphertext"}}}`)
	if _, _, err := composeTeamMetadataReplacement(ctx, unowned, plan, prior, priorPrepared.provenance, priorPrepared, request); err == nil {
		t.Fatal("unowned callback ciphertext was replayable")
	}
	generic := mustParseSemanticDictionary(t, `{"api":"[REDACTED]"}`)
	if _, _, err := composeTeamMetadataReplacement(ctx, generic, plan, prior, priorPrepared.provenance, priorPrepared, request); err == nil {
		t.Fatal("generic redaction was replayable")
	}

	remoteID := mustParseSemanticDictionary(t, `{"team_member_budget_id":"server-id","api":true}`)
	if _, _, err := composeTeamMetadataReplacement(ctx, remoteID, plan, prior, priorPrepared.provenance, priorPrepared, map[string]interface{}{"team_member_budget": nil}); err == nil {
		t.Fatal("metadata plus all-null member defaults accepted")
	}
	replacement, reinsert, err = composeTeamMetadataReplacement(ctx, remoteID, plan, prior, priorPrepared.provenance, priorPrepared, request)
	if err != nil || !reinsert {
		t.Fatalf("reinsert composition err=%v reinsert=%v", err, reinsert)
	}
	if _, sent := replacement["team_member_budget_id"]; sent {
		t.Fatal("server-owned member budget ID was caller-sent")
	}
}

func TestTeamSemanticPendingCompleteNotAndPartial(t *testing.T) {
	ctx := context.Background()
	prior, _ := prepareTeamSemanticDictionary(ctx, types.StringValue(`{"shape":1}`), types.MapNull(types.StringType))
	next, _ := prepareTeamSemanticDictionary(ctx, types.StringValue(`{"shape":{"a":1,"b":2}}`), types.MapNull(types.StringType))
	ownership, _ := next.updateOwnership(ctx, prior.provenance)
	ownership.provenance = prior.provenance
	ownership.pending = pendingTeamSemanticTransition(func() teamSemanticOwnership { value, _ := next.updateOwnership(ctx, prior.provenance); return value }())
	for name, metadata := range map[string]map[string]interface{}{
		"complete": mustParseSemanticDictionary(t, `{"shape":{"a":1,"b":2}}`),
		"not":      mustParseSemanticDictionary(t, `{"shape":1}`),
	} {
		_, reconcile, err := resolveTeamSemanticPending(ctx, metadata, ownership)
		if err != nil || !reconcile.Present || reconcile.Committed != (name == "complete") {
			t.Fatalf("%s reconcile=%#v err=%v", name, reconcile, err)
		}
	}
	if _, _, err := resolveTeamSemanticPending(ctx, mustParseSemanticDictionary(t, `{"shape":{"a":1}}`), ownership); err == nil {
		t.Fatal("partial shape transition did not fail closed")
	}
}
