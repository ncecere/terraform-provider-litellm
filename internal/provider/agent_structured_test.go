package provider

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type agentTestPrivate map[string][]byte

func (p agentTestPrivate) GetKey(_ context.Context, key string) ([]byte, diag.Diagnostics) {
	return p[key], nil
}

func TestConfiguredAgentParamsIsLiteralAndLossless(t *testing.T) {
	legacy := stringMapValue(map[string]string{
		"false_text": "false", "leading": "001", "json_text": `{"x":1}`,
	})
	structured := types.StringValue(`{
		"boolean":false,"integer":9007199254740993,"fraction":1.0000000000000001,
		"nothing":null,"empty_list":[],"empty_object":{},"nested":{"items":[true,"001",null]}
	}`)
	params, configured, err := configuredAgentParams(legacy, structured)
	if err != nil || !configured {
		t.Fatalf("configured=%v err=%v", configured, err)
	}
	for key, want := range map[string]string{"false_text": "false", "leading": "001", "json_text": `{"x":1}`} {
		if got, ok := params[key].(string); !ok || got != want {
			t.Fatalf("%s=%#v", key, params[key])
		}
	}
	encoded, err := canonicalAgentJSON(params)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := decodeJSONUseNumber([]byte(encoded), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["integer"].(json.Number).String() != "9007199254740993" || decoded["fraction"].(json.Number).String() != "1.0000000000000001" {
		t.Fatalf("numbers=%s", encoded)
	}
	if decoded["nothing"] != nil || len(decoded["empty_list"].([]interface{})) != 0 || len(decoded["empty_object"].(map[string]interface{})) != 0 {
		t.Fatalf("null/empty=%s", encoded)
	}
}

func TestConfiguredAgentParamsConflictDoesNotCoerce(t *testing.T) {
	for _, structured := range []string{`{"value":false}`, `{"value":1}`, `{"value":{"x":1}}`} {
		if _, _, err := configuredAgentParams(stringMapValue(map[string]string{"value": "false"}), types.StringValue(structured)); err == nil {
			t.Fatalf("conflict accepted: %s", structured)
		}
	}
	params, _, err := configuredAgentParams(stringMapValue(map[string]string{"value": "false"}), types.StringValue(`{"value":"false"}`))
	if err != nil || params["value"] != "false" {
		t.Fatalf("equal string conflict: %#v %v", params, err)
	}
}

func TestImportedLegacyProjectionNeverBecomesWireType(t *testing.T) {
	resource := &AgentResource{}
	state := emptyKnownAgentResourceModel()
	state.ID = types.StringValue("agent")
	state.AgentName = types.StringValue("Agent")
	state.LiteLLMParams = stringMapValue(map[string]string{"number": "9007199254740993", "flag": "false", "text": "remote"})
	state.LiteLLMParamsJSON = types.StringValue(`{"number":9007199254740993,"flag":false,"text":"remote"}`)
	plan := cloneAgentResourceModel(state)
	config := emptyKnownAgentResourceModel()
	config.AgentName = types.StringValue("Agent")
	config.LiteLLMParams = stringMapValue(map[string]string{"text": "configured"})
	request, err := resource.buildAgentUpdateRequest(&plan, &state, &config, agentFieldSet{
		agentFieldParamsJSON:                  true,
		agentLeaf(agentFieldParams, "number"): true,
		agentLeaf(agentFieldParams, "flag"):   true,
		agentLeaf(agentFieldParams, "text"):   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	params := request["litellm_params"].(map[string]interface{})
	if params["number"].(json.Number).String() != "9007199254740993" || params["flag"] != false || params["text"] != "configured" {
		t.Fatalf("import compatibility projection changed wire types: %#v", params)
	}
}

func TestAgentCoreProviderModelPairing(t *testing.T) {
	if err := validateAgentCorePair(map[string]interface{}{"model": "bedrock/agentcore/runtime", "custom_llm_provider": "openai"}); err != nil {
		t.Fatalf("provider mistook v1.98 AgentCore selection logic for request validation: %v", err)
	}
	if err := validateAgentCorePair(map[string]interface{}{"model": "bedrock/agentcore/runtime", "custom_llm_provider": "bedrock"}); err != nil {
		t.Fatalf("source-supported AgentCore pairing rejected: %v", err)
	}
	// CRUD accepts arbitrary dictionaries and does not itself require the
	// routing hint; do not invent a stronger API enum/required-field contract.
	if err := validateAgentCorePair(map[string]interface{}{"model": "bedrock/agentcore/runtime"}); err != nil {
		t.Fatalf("provider invented a registry restriction: %v", err)
	}
	if err := validateAgentCorePair(map[string]interface{}{"model": "bedrock/AgentCore/runtime", "custom_llm_provider": "openai"}); err != nil {
		t.Fatalf("provider broadened v1.98's case-sensitive AgentCore detection: %v", err)
	}
}

func TestAgentStructuredMaskingAndSemanticSpelling(t *testing.T) {
	prior := types.StringValue(`{ "token": "secret", "nested": { "api_key": "value" }, "large": 9007199254740993 }`)
	remote := map[string]interface{}{
		"token": "litellm_enc::ciphertext", "nested": map[string]interface{}{"api_key": "va****ue"}, "large": json.Number("9.007199254740993e15"),
	}
	observed, err := reconcileAgentJSONObject(prior, remote)
	if err != nil {
		t.Fatal(err)
	}
	if observed.ValueString() != prior.ValueString() {
		t.Fatalf("spelling changed: %q", observed.ValueString())
	}
	if _, err := reconcileAgentJSONObject(types.StringNull(), map[string]interface{}{"api_key": "*****"}); err == nil {
		t.Fatal("masked import succeeded without prior plaintext")
	}
}

func TestAgentSkillSecurityAndSignaturesWirePreserveOrderDuplicates(t *testing.T) {
	securityRaw := []interface{}{
		map[string]interface{}{"oauth": []interface{}{"b", "a", "a"}},
		map[string]interface{}{"oauth": []interface{}{"b", "a", "a"}},
	}
	security, err := readAgentSecurity(securityRaw)
	if err != nil {
		t.Fatal(err)
	}
	resource := &AgentResource{}
	model := emptyKnownAgentResourceModel()
	model.AgentName = types.StringValue("agent")
	model.AgentCard = &AgentCardModel{
		Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"),
		Skills: []AgentSkillModel{{ID: types.StringValue("skill"), Name: types.StringValue("Skill"), Security: security}},
		Signatures: []AgentCardSignatureModel{
			{Protected: types.StringValue("p"), Signature: types.StringValue("s"), Header: types.StringValue(`{ "n": 9007199254740993 }`)},
			{Protected: types.StringValue("p"), Signature: types.StringValue("s"), Header: types.StringNull()},
			{Protected: types.StringValue("p"), Signature: types.StringValue("s"), Header: types.StringNull()},
		},
	}
	request, err := resource.buildAgentRequest(&model)
	if err != nil {
		t.Fatal(err)
	}
	card := request["agent_card_params"].(map[string]interface{})
	signatures := card["signatures"].([]map[string]interface{})
	if len(signatures) != 3 || signatures[0]["protected"] != signatures[1]["protected"] || signatures[1]["protected"] != signatures[2]["protected"] || len(signatures[1]) != len(signatures[2]) {
		t.Fatalf("signatures=%#v", signatures)
	}
	if signatures[0]["header"].(map[string]interface{})["n"].(json.Number).String() != "9007199254740993" {
		t.Fatalf("header=%#v", signatures[0])
	}
	skills := card["skills"].([]map[string]interface{})
	got := skills[0]["security"].([]map[string][]string)
	if len(got) != 2 || got[0]["oauth"][1] != "a" || got[0]["oauth"][2] != "a" {
		t.Fatalf("security=%#v", got)
	}
}

func TestAgentDataSourceSchemaParityAndProjection(t *testing.T) {
	var singleResponse datasource.SchemaResponse
	(&AgentDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &singleResponse)
	var listResponse datasource.SchemaResponse
	(&AgentsListDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &listResponse)
	listAttribute := listResponse.Schema.Attributes["agents"].(datasourceschema.ListNestedAttribute)
	for name := range singleResponse.Schema.Attributes {
		if name == "id" {
			continue
		}
		if _, ok := listAttribute.NestedObject.Attributes[name]; !ok {
			t.Fatalf("list data source missing single field %s", name)
		}
	}
	for name := range listAttribute.NestedObject.Attributes {
		if name == "agent_id" {
			continue
		}
		if _, ok := singleResponse.Schema.Attributes[name]; !ok {
			t.Fatalf("single data source missing list field %s", name)
		}
	}

	item := map[string]interface{}{
		"agent_id": "agent", "agent_name": "Agent",
		"agent_card_params": map[string]interface{}{"name": "Card", "url": "https://agent.invalid", "signatures": []interface{}{map[string]interface{}{"protected": "p", "signature": "s", "header": map[string]interface{}{"n": json.Number("9007199254740993")}}}},
		"litellm_params":    map[string]interface{}{"text": "001", "flag": false, "nested": map[string]interface{}{"empty": []interface{}{}}},
		"object_permission": map[string]interface{}{"mcp_tool_permissions": map[string]interface{}{"server": []interface{}{"a", "a"}}},
	}
	projected, err := projectAgentData(item, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if len(projected.LiteLLMParams.Elements()) != 3 || projected.LiteLLMParams.Elements()["text"].(types.String).ValueString() != "001" || projected.LiteLLMParams.Elements()["flag"].(types.String).ValueString() != "false" {
		t.Fatalf("legacy compatibility projection=%#v", projected.LiteLLMParams.Elements())
	}
	if !jsonSemanticallyEqual(projected.LiteLLMParamsJSON.ValueString(), `{"text":"001","flag":false,"nested":{"empty":[]}}`) {
		t.Fatalf("json=%s", projected.LiteLLMParamsJSON.ValueString())
	}

	malformed := map[string]interface{}{"agent_id": "agent", "agent_name": "Agent", "agent_card_params": map[string]interface{}{"signatures": []interface{}{map[string]interface{}{"protected": true}}}}
	if _, err := projectAgentData(malformed, "agent"); err == nil {
		t.Fatal("malformed present signature accepted")
	}
	if _, err := projectAgentData(item, "other"); err == nil {
		t.Fatal("identity mismatch accepted")
	}
	roleOmitted, err := projectAgentData(map[string]interface{}{
		"agent_id": "agent", "agent_name": "Agent",
		"agent_card_params": map[string]interface{}{"description": "endpoint-observable partial card", "signatures": []interface{}{map[string]interface{}{"header": nil}}},
	}, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if !roleOmitted.LiteLLMParamsJSON.IsNull() || !roleOmitted.ObjectPermissionJSON.IsNull() || !roleOmitted.StaticHeaders.IsNull() {
		t.Fatalf("role omission retained stale data: %#v", roleOmitted)
	}
}

func TestAgentAuthoritativeParamsOverlayPreservesUnownedWireValues(t *testing.T) {
	base := map[string]interface{}{
		"owned": "before", "api_large": json.Number("9007199254740993"), "api_null": nil,
		"api_nested": map[string]interface{}{"present": nil, "items": []interface{}{json.Number("1.0000000000000001")}},
	}
	prior := emptyKnownAgentResourceModel()
	prior.LiteLLMParams = stringMapValue(map[string]string{"owned": "before", "api_large": "9007199254740993", "api_null": "null", "api_nested": `{"present":null,"items":[1.0000000000000001]}`})
	prior.LiteLLMParamsJSON = types.StringValue(`{"owned":"before","api_large":9007199254740993,"api_null":null,"api_nested":{"present":null,"items":[1.0000000000000001]}}`)
	config := emptyKnownAgentResourceModel()
	config.LiteLLMParams = stringMapValue(map[string]string{"owned": "after"})
	imported := agentFieldSet{
		agentLeaf(agentFieldParams, "api_large"):  true,
		agentLeaf(agentFieldParams, "api_null"):   true,
		agentLeaf(agentFieldParams, "api_nested"): true,
	}
	patch, err := overlayAgentParamsWire(base, prior, config, imported)
	if err != nil {
		t.Fatal(err)
	}
	if patch["owned"] != "after" || patch["api_large"].(json.Number).String() != "9007199254740993" || patch["api_null"] != nil || !exactJSONValuesEqual(base["api_nested"], patch["api_nested"]) {
		t.Fatalf("lossy parameter overlay: %#v", patch)
	}
	evidence := &agentPatchPreservation{paramsBase: base, paramsPatch: patch}
	if !evidence.matches(map[string]interface{}{"litellm_params": patch}) {
		t.Fatal("unchanged unowned values did not confirm")
	}
	drifted := cloneAgentWireObject(patch)
	drifted["api_large"] = json.Number("9007199254740992")
	if evidence.matches(map[string]interface{}{"litellm_params": drifted}) {
		t.Fatal("changed unowned exact number confirmed")
	}
	delete(drifted, "api_null")
	if evidence.matches(map[string]interface{}{"litellm_params": drifted}) {
		t.Fatal("changed unowned null presence confirmed")
	}
}

func TestAgentRawCardOverlayPreservesNullAndOmission(t *testing.T) {
	base := map[string]interface{}{
		"name": "Agent", "url": "https://agent.invalid", "description": "before", "x-api": map[string]interface{}{"n": json.Number("9007199254740993")},
		"signatures": []interface{}{
			map[string]interface{}{"protected": "p-null", "signature": "s", "header": nil},
			map[string]interface{}{"protected": "p-omitted", "signature": "s"},
		},
		"skills": []interface{}{
			map[string]interface{}{"id": "null", "name": "Null", "security": nil, "x": true},
			map[string]interface{}{"id": "omitted", "name": "Omitted", "x": false},
		},
	}
	prior := emptyKnownAgentResourceModel()
	prior.AgentCard = &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"), Description: types.StringValue("before")}
	plan := cloneAgentResourceModel(prior)
	plan.AgentCard.Description = types.StringValue("after")
	config := emptyKnownAgentResourceModel()
	config.AgentCard = &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"), Description: types.StringValue("after")}
	imported := agentFieldSet{agentFieldCardSignatures: true, agentScopeCardSkills: true}
	for id, raw := range agentSkillRawByID(base) {
		markAgentSkillWireLeaves(imported, id, raw)
	}
	patch, err := overlayAgentCardRaw(base, plan, prior, config, imported)
	if err != nil {
		t.Fatal(err)
	}
	if patch["description"] != "after" || !exactJSONValuesEqual(base["signatures"], patch["signatures"]) || !exactJSONValuesEqual(base["skills"], patch["skills"]) || !exactJSONValuesEqual(base["x-api"], patch["x-api"]) {
		t.Fatalf("lossy card overlay: %#v", patch)
	}
	evidence := &agentPatchPreservation{cardBase: base, cardPatch: patch}
	if !evidence.matches(map[string]interface{}{"agent_card_params": patch}) {
		t.Fatal("preserved card did not confirm")
	}
	drifted := cloneAgentWireObject(patch)
	signatures := drifted["signatures"].([]interface{})
	delete(signatures[0].(map[string]interface{}), "header")
	if evidence.matches(map[string]interface{}{"agent_card_params": drifted}) {
		t.Fatal("header null-to-omitted drift confirmed")
	}
	drifted = cloneAgentWireObject(patch)
	skills := drifted["skills"].([]interface{})
	skills[1].(map[string]interface{})["security"] = nil
	if evidence.matches(map[string]interface{}{"agent_card_params": drifted}) {
		t.Fatal("security omitted-to-null drift confirmed")
	}
}

func TestAgentSignatureLeafOverlayPreservesUnownedHeader(t *testing.T) {
	base := map[string]interface{}{"name": "Agent", "url": "https://agent.invalid", "signatures": []interface{}{
		map[string]interface{}{"protected": "before", "signature": "s", "header": nil},
		map[string]interface{}{"protected": "api", "signature": "api"},
	}}
	prior := emptyKnownAgentResourceModel()
	prior.AgentCard = &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"), Signatures: []AgentCardSignatureModel{
		{Protected: types.StringValue("before"), Signature: types.StringValue("s"), Header: types.StringNull(), HeaderJSON: types.StringValue("null")},
		{Protected: types.StringValue("api"), Signature: types.StringValue("api"), Header: types.StringNull(), HeaderJSON: types.StringNull()},
	}}
	plan := cloneAgentResourceModel(prior)
	plan.AgentCard.Signatures = []AgentCardSignatureModel{{Protected: types.StringValue("after"), Signature: types.StringValue("s"), Header: types.StringNull(), HeaderJSON: types.StringNull()}}
	config := emptyKnownAgentResourceModel()
	config.AgentCard = &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"), Signatures: append([]AgentCardSignatureModel(nil), plan.AgentCard.Signatures...)}
	imported := agentFieldSet{
		agentSignatureLeaf(0, "header"):    true,
		agentSignatureLeaf(1, "protected"): true, agentSignatureLeaf(1, "signature"): true,
		agentScopeCardSignatures: true,
	}
	patch, err := overlayAgentCardRaw(base, plan, prior, config, imported)
	if err != nil {
		t.Fatal(err)
	}
	signatures := agentWireObjectList(patch["signatures"])
	if len(signatures) != 2 || signatures[0]["protected"] != "after" {
		t.Fatalf("signature overlay=%#v", signatures)
	}
	if header, present := signatures[0]["header"]; !present || header != nil {
		t.Fatalf("unowned explicit-null header changed: %#v", signatures[0])
	}
	if !exactJSONValuesEqual(signatures[1], base["signatures"].([]interface{})[1]) {
		t.Fatalf("API-owned trailing signature changed: %#v", signatures[1])
	}
	evidence := &agentPatchPreservation{cardBase: base, cardPatch: patch}
	if !evidence.matches(map[string]interface{}{"agent_card_params": patch}) {
		t.Fatal("signature leaf preservation did not confirm")
	}
	drifted := cloneAgentWireObject(patch)
	delete(drifted["signatures"].([]interface{})[0].(map[string]interface{}), "header")
	if evidence.matches(map[string]interface{}{"agent_card_params": drifted}) {
		t.Fatal("unowned header omission confirmed")
	}
}

func TestAgentExplicitNullJSONBridges(t *testing.T) {
	model := emptyKnownAgentResourceModel()
	model.AgentName = types.StringValue("agent")
	model.AgentCard = &AgentCardModel{
		Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"),
		Signatures: []AgentCardSignatureModel{{Protected: types.StringValue("p"), Signature: types.StringValue("s"), Header: types.StringNull(), HeaderJSON: types.StringValue("null")}},
		Skills:     []AgentSkillModel{{ID: types.StringValue("skill"), Name: types.StringValue("Skill"), Security: types.ListNull(types.MapType{ElemType: types.ListType{ElemType: types.StringType}}), SecurityJSON: types.StringValue("null")}},
	}
	request, err := (&AgentResource{}).buildAgentRequest(&model)
	if err != nil {
		t.Fatal(err)
	}
	card := request["agent_card_params"].(map[string]interface{})
	if _, present := card["signatures"].([]map[string]interface{})[0]["header"]; !present || card["signatures"].([]map[string]interface{})[0]["header"] != nil {
		t.Fatal("explicit null header was not emitted")
	}
	if _, present := card["skills"].([]map[string]interface{})[0]["security"]; !present || card["skills"].([]map[string]interface{})[0]["security"] != nil {
		t.Fatal("explicit null security was not emitted")
	}
	model.AgentCard.Signatures[0].Header = types.StringValue(`{}`)
	if _, err := (&AgentResource{}).buildAgentRequest(&model); err == nil {
		t.Fatal("header/header_json conflict accepted")
	}
	model.AgentCard.Signatures[0].Header = types.StringNull()
	model.AgentCard.Skills[0].SecurityJSON = types.StringValue(`[{"oauth":[1]}]`)
	if _, err := (&AgentResource{}).buildAgentRequest(&model); err == nil {
		t.Fatal("malformed security_json accepted")
	}
}

func TestAgentImportSkillOwnershipUsesWirePresence(t *testing.T) {
	data := emptyKnownAgentResourceModel()
	data.AgentCard = &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"), Skills: []AgentSkillModel{
		{ID: types.StringValue("null"), Name: types.StringValue("Null"), Security: types.ListNull(types.MapType{ElemType: types.ListType{ElemType: types.StringType}}), SecurityJSON: types.StringValue("null")},
		{ID: types.StringValue("omitted"), Name: types.StringValue("Omitted"), Security: types.ListNull(types.MapType{ElemType: types.ListType{ElemType: types.StringType}}), SecurityJSON: types.StringNull()},
	}}
	raw := map[string]interface{}{"agent_card_params": map[string]interface{}{"skills": []interface{}{
		map[string]interface{}{"id": "null", "name": "Null", "security": nil},
		map[string]interface{}{"id": "omitted", "name": "Omitted"},
	}}}
	owned := agentImportedFieldsFromWire(data, raw)
	if !owned[agentSkillLeaf("null", "security")] || owned[agentSkillLeaf("omitted", "security")] {
		t.Fatalf("wire security ownership=%#v", owned)
	}
}

func TestAgentConfiguredChildrenDoNotClaimFreshHeaderOrSecurity(t *testing.T) {
	base := map[string]interface{}{"name": "Agent", "url": "https://agent.invalid",
		"signatures": []interface{}{map[string]interface{}{"protected": "p", "signature": "s", "header": nil}},
		"skills":     []interface{}{map[string]interface{}{"id": "skill", "name": "Skill", "security": nil}},
	}
	prior := emptyKnownAgentResourceModel()
	prior.AgentCard = &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"),
		Signatures: []AgentCardSignatureModel{{Protected: types.StringValue("p"), Signature: types.StringValue("s"), Header: types.StringNull(), HeaderJSON: types.StringNull()}},
		Skills:     []AgentSkillModel{{ID: types.StringValue("skill"), Name: types.StringValue("Skill"), Security: types.ListNull(types.MapType{ElemType: types.ListType{ElemType: types.StringType}}), SecurityJSON: types.StringNull()}},
	}
	plan, config := cloneAgentResourceModel(prior), cloneAgentResourceModel(prior)
	plan.AgentCard.Description, config.AgentCard.Description = types.StringValue("changed"), types.StringValue("changed")
	patch, err := overlayAgentCardRaw(base, plan, prior, config, agentFieldSet{})
	if err != nil {
		t.Fatal(err)
	}
	if header, present := agentWireObjectList(patch["signatures"])[0]["header"]; !present || header != nil {
		t.Fatalf("fresh signature header changed: %#v", patch)
	}
	if security, present := agentWireObjectList(patch["skills"])[0]["security"]; !present || security != nil {
		t.Fatalf("fresh skill security changed: %#v", patch)
	}
}

func TestAgentRemoveOwnedSkillsPreservesAPISiblings(t *testing.T) {
	base := map[string]interface{}{"name": "Agent", "url": "https://agent.invalid", "skills": []interface{}{
		map[string]interface{}{"id": "owned", "name": "Owned"}, map[string]interface{}{"id": "imported", "name": "Imported"}, map[string]interface{}{"id": "fresh", "name": "Fresh"},
	}}
	prior := emptyKnownAgentResourceModel()
	prior.AgentCard = &AgentCardModel{Name: types.StringValue("Agent"), URL: types.StringValue("https://agent.invalid"), Skills: []AgentSkillModel{
		{ID: types.StringValue("owned"), Name: types.StringValue("Owned")}, {ID: types.StringValue("imported"), Name: types.StringValue("Imported")},
	}}
	plan, config := cloneAgentResourceModel(prior), cloneAgentResourceModel(prior)
	plan.AgentCard.Skills, config.AgentCard.Skills = nil, nil
	patch, err := overlayAgentCardRaw(base, plan, prior, config, agentFieldSet{agentScopeCardSkills: true, agentSkillLeaf("imported", "id"): true})
	if err != nil {
		t.Fatal(err)
	}
	byID := agentSkillRawByID(map[string]interface{}{"skills": patch["skills"]})
	if byID["owned"] != nil || byID["imported"] == nil || byID["fresh"] == nil {
		t.Fatalf("skill removal overlay=%#v", byID)
	}
	observedSkills := agentWireModelsForSkillsForTest(byID)
	unconfirmed := append(append([]AgentSkillModel(nil), observedSkills...), AgentSkillModel{ID: types.StringValue("owned"), Name: types.StringValue("Owned")})
	ownership := agentFieldSet{agentScopeCardSkills: true, agentSkillLeaf("imported", "id"): true}
	if agentSkillsMutationMatch(nil, prior.AgentCard, nil, unconfirmed, ownership) {
		t.Fatal("unconfirmed owned skill removal accepted")
	}
	observed := cloneAgentResourceModel(plan)
	observed.AgentCard.Skills = observedSkills
	confirmed := reconcileConfirmedAgentState(plan, observed, config, prior, ownership)
	confirmedByID := map[string]bool{}
	for _, skill := range confirmed.AgentCard.Skills {
		confirmedByID[skill.ID.ValueString()] = true
	}
	if confirmedByID["owned"] || !confirmedByID["imported"] || !confirmedByID["fresh"] {
		t.Fatalf("confirmed state forgot preserved siblings: %#v", confirmedByID)
	}
}

func agentWireModelsForSkillsForTest(byID map[string]map[string]interface{}) []AgentSkillModel {
	result := make([]AgentSkillModel, 0, len(byID))
	for id, raw := range byID {
		result = append(result, AgentSkillModel{ID: types.StringValue(id), Name: types.StringValue(raw["name"].(string))})
	}
	return result
}

func TestAgentMergedLifecycleFixtureClearOverlay(t *testing.T) {
	securityType := types.MapType{ElemType: types.ListType{ElemType: types.StringType}}
	security := types.ListValueMust(securityType, []attr.Value{
		types.MapValueMust(types.ListType{ElemType: types.StringType}, map[string]attr.Value{
			"oauth2": types.ListValueMust(types.StringType, []attr.Value{types.StringValue("read"), types.StringValue("read")}),
		}),
	})
	prior := emptyKnownAgentResourceModel()
	prior.AgentName = types.StringValue("smoke-agent-lifecycle")
	prior.AgentCard = &AgentCardModel{
		Name: types.StringValue("Smoke Agent Lifecycle"), Description: types.StringValue("set then clear"), URL: types.StringValue("https://agent.example.com/lifecycle"),
		Version: types.StringValue("1.0.0"), ProtocolVersion: types.StringValue("1.0"), DefaultInputModes: stringListValue("text"), DefaultOutputModes: stringListValue("text"),
		PreferredTransport: types.StringValue("JSONRPC"), IconURL: types.StringValue("https://agent.example.com/icon.png"), DocumentationURL: types.StringValue("https://agent.example.com/docs"), SupportsAuthenticatedExtendedCard: types.BoolValue(true),
		Capabilities: &AgentCapabilitiesModel{Streaming: types.BoolValue(true), PushNotifications: types.BoolValue(false), StateTransitionHistory: types.BoolValue(false)},
		Provider:     &AgentProviderModel{Organization: types.StringValue("Acceptance"), URL: types.StringValue("https://agent.example.com")},
		Skills: []AgentSkillModel{{
			ID: types.StringValue("acceptance"), Name: types.StringValue("Acceptance"), Description: types.StringValue("set then clear"),
			Tags: stringListValue("lifecycle"), Examples: stringListValue("test"), InputModes: stringListValue("text"), OutputModes: stringListValue("text"), Security: security, SecurityJSON: types.StringNull(),
		}},
		Signatures: []AgentCardSignatureModel{
			{Protected: types.StringValue("acceptance-protected"), Signature: types.StringValue("acceptance-signature"), Header: types.StringValue(`{"duplicate":0,"exact":9007199254740993}`), HeaderJSON: types.StringNull()},
			{Protected: types.StringValue("acceptance-protected"), Signature: types.StringValue("acceptance-signature"), Header: types.StringValue(`{"duplicate":1,"exact":9007199254740993}`), HeaderJSON: types.StringNull()},
		},
	}
	plan := cloneAgentResourceModel(prior)
	plan.AgentCard.Description = types.StringNull()
	plan.AgentCard.PreferredTransport = types.StringNull()
	plan.AgentCard.IconURL = types.StringNull()
	plan.AgentCard.DocumentationURL = types.StringNull()
	plan.AgentCard.SupportsAuthenticatedExtendedCard = types.BoolValue(false)
	plan.AgentCard.Capabilities = &AgentCapabilitiesModel{Streaming: types.BoolValue(false), PushNotifications: types.BoolValue(false), StateTransitionHistory: types.BoolValue(false)}
	plan.AgentCard.Provider.Organization = types.StringNull()
	plan.AgentCard.Skills[0].Description = types.StringNull()
	plan.AgentCard.Skills[0].Tags = stringListValue()
	plan.AgentCard.Skills[0].Examples = stringListValue()
	plan.AgentCard.Skills[0].InputModes = stringListValue()
	plan.AgentCard.Skills[0].OutputModes = stringListValue()
	plan.AgentCard.Skills[0].Security = types.ListValueMust(securityType, []attr.Value{})
	plan.AgentCard.Signatures = []AgentCardSignatureModel{}
	config := cloneAgentResourceModel(plan)

	base := map[string]interface{}{
		"name": "Smoke Agent Lifecycle", "description": "set then clear", "url": "https://agent.example.com/lifecycle", "version": "1.0.0", "protocolVersion": "1.0",
		"defaultInputModes": []interface{}{"text"}, "defaultOutputModes": []interface{}{"text"}, "preferredTransport": "JSONRPC", "iconUrl": "https://agent.example.com/icon.png", "documentationUrl": "https://agent.example.com/docs", "supportsAuthenticatedExtendedCard": true,
		"capabilities": map[string]interface{}{"streaming": true}, "provider": map[string]interface{}{"organization": "Acceptance", "url": "https://agent.example.com"},
		"skills": []interface{}{map[string]interface{}{
			"id": "acceptance", "name": "Acceptance", "description": "set then clear", "tags": []interface{}{"lifecycle"}, "examples": []interface{}{"test"}, "inputModes": []interface{}{"text"}, "outputModes": []interface{}{"text"},
			"security": []interface{}{map[string]interface{}{"oauth2": []interface{}{"read", "read"}}}, "x-api-owned": map[string]interface{}{"exact": json.Number("9007199254740993"), "present": nil},
		}},
		"signatures": []interface{}{
			map[string]interface{}{"protected": "acceptance-protected", "signature": "acceptance-signature", "header": map[string]interface{}{"duplicate": json.Number("0"), "exact": json.Number("9007199254740993")}},
			map[string]interface{}{"protected": "acceptance-protected", "signature": "acceptance-signature", "header": map[string]interface{}{"duplicate": json.Number("1"), "exact": json.Number("9007199254740993")}},
		},
		"security": []interface{}{}, "securitySchemes": map[string]interface{}{}, "supportedInterfaces": []interface{}{},
	}
	patch, err := overlayAgentCardRaw(base, plan, prior, config, agentFieldSet{})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := patch["capabilities"]; present {
		t.Fatalf("false capability clear was not normalized to v1.98 wire omission: %#v", patch["capabilities"])
	}
	if err := validateAgentCardV198RoundTrip(patch); err != nil {
		t.Fatalf("exact merged lifecycle clear failed preflight: %v", err)
	}
	for _, cleared := range []string{"description", "preferredTransport", "iconUrl", "documentationUrl"} {
		if _, present := patch[cleared]; present {
			t.Fatalf("card clear retained %s", cleared)
		}
	}
	if patch["supportsAuthenticatedExtendedCard"] != false {
		t.Fatal("authenticated-card flag did not clear")
	}
	provider := agentWireObject(patch["provider"])
	if _, present := provider["organization"]; present || provider["url"] != "https://agent.example.com" {
		t.Fatal("provider leaf clear changed its configured sibling")
	}
	skill := agentSkillRawByID(patch)["acceptance"]
	if skill == nil || !exactJSONValuesEqual(skill["x-api-owned"], base["skills"].([]interface{})[0].(map[string]interface{})["x-api-owned"]) {
		t.Fatal("API-owned unknown structured skill value was not preserved exactly")
	}
	if _, present := skill["description"]; present {
		t.Fatal("skill description clear was not represented by omission")
	}
	for _, cleared := range []string{"tags", "examples", "inputModes", "outputModes", "security"} {
		items := reflect.ValueOf(skill[cleared])
		if !items.IsValid() || items.Kind() != reflect.Slice || items.Len() != 0 {
			t.Fatalf("skill collection clear did not remain explicit empty: %s", cleared)
		}
	}
	if signatures := agentWireObjectList(patch["signatures"]); len(signatures) != 0 {
		t.Fatalf("signature clear retained %d entries", len(signatures))
	}
	for _, adversarial := range []map[string]interface{}{
		func() map[string]interface{} {
			value := cloneAgentWireObject(patch)
			value["x-unknown"] = true
			return value
		}(),
		func() map[string]interface{} {
			value := cloneAgentWireObject(patch)
			value["capabilities"] = map[string]interface{}{"unknown": true}
			return value
		}(),
	} {
		if validateAgentCardV198RoundTrip(adversarial) == nil {
			t.Fatal("adversarial filtered key passed the strict v1.98 preflight")
		}
	}
}

func TestAgentV198RoundTripPreflightRejectsFilteredPaths(t *testing.T) {
	for _, card := range []map[string]interface{}{
		{"name": "Agent", "url": "https://agent.invalid", "x-unknown": true},
		{"name": "Agent", "url": "https://agent.invalid", "additionalInterfaces": []interface{}{}},
		{"name": "Agent", "url": "https://agent.invalid", "capabilities": map[string]interface{}{"pushNotifications": true}},
	} {
		if err := validateAgentCardV198RoundTrip(card); err == nil {
			t.Fatalf("filtered path accepted: %#v", card)
		}
	}
	if err := validateAgentCardV198RoundTrip(map[string]interface{}{"name": "Agent", "url": "https://agent.invalid", "capabilities": map[string]interface{}{"streaming": true}}); err != nil {
		t.Fatal(err)
	}
}

func TestAgentCardStrictPresentShapesAndPartialAbsence(t *testing.T) {
	malformed := []map[string]interface{}{
		{"signatures": []interface{}{map[string]interface{}{"protected": nil}}},
		{"signatures": []interface{}{map[string]interface{}{"signature": true}}},
		{"skills": []interface{}{map[string]interface{}{"security": []interface{}{map[string]interface{}{"oauth": []interface{}{true}}}}}},
		{"capabilities": map[string]interface{}{"extensions": []interface{}{map[string]interface{}{"uri": nil}}}},
		{"securitySchemes": map[string]interface{}{"key": map[string]interface{}{"type": "http", "scheme": nil}}},
	}
	for _, card := range malformed {
		if validateAgentCardResponse(card, false) == nil {
			t.Fatalf("malformed present field accepted: %#v", card)
		}
	}
	if err := validateAgentCardResponse(map[string]interface{}{"signatures": []interface{}{map[string]interface{}{"header": nil}}}, false); err != nil {
		t.Fatalf("valid role-partial card rejected: %v", err)
	}
}

func TestAgentImportCardOwnershipUsesRawNullPresence(t *testing.T) {
	data := emptyKnownAgentResourceModel()
	data.AgentCard = &AgentCardModel{Name: types.StringNull(), URL: types.StringNull(), Capabilities: &AgentCapabilitiesModel{Streaming: types.BoolNull()}, Provider: &AgentProviderModel{Organization: types.StringNull()}}
	owned := agentImportedFieldsFromWire(data, map[string]interface{}{"agent_card_params": map[string]interface{}{"name": nil, "capabilities": map[string]interface{}{"streaming": nil}, "provider": map[string]interface{}{"organization": nil}}})
	if !owned[agentFieldCardName] || !owned[agentFieldCardCapStreaming] || !owned[agentFieldCardProviderOrg] || owned[agentFieldCardURL] {
		t.Fatalf("raw card presence ownership=%#v", owned)
	}
}

func TestAgentOwnershipMarkerCanonicalGrammar(t *testing.T) {
	valid := agentFieldSet{agentScopeCardSkills: true, agentSkillLeaf("skill", "security"): true, agentSignatureLeaf(0, "header"): true}
	raw := encodeAgentFieldSet(valid)
	decoded, err := decodeAgentFieldSet(raw)
	if err != nil || !agentFieldSetsEqual(valid, decoded) {
		t.Fatalf("canonical ownership rejected: %#v %v", decoded, err)
	}
	for _, corrupt := range [][]byte{[]byte(`null`), []byte(`["unknown"]`), []byte(`["agent_card.name","agent_card.name"]`), append([]byte(" "), raw...)} {
		if _, err := decodeAgentFieldSet(corrupt); err == nil {
			t.Fatalf("corrupt ownership accepted: %s", corrupt)
		}
	}
}

func TestAgentOwnershipBundleIsAllOrNothing(t *testing.T) {
	committed := agentFieldSet{agentScopeCardSkills: true, agentSkillLeaf("skill", "id"): true}
	pending := agentFieldSet{agentScopeCardSkills: true}
	valid := agentTestPrivate{agentOwnershipInitializedPrivateKey: []byte("true"), agentImportedFieldsPrivateKey: encodeAgentFieldSet(committed), agentOwnershipPendingPrivateKey: encodeAgentFieldSet(pending)}
	bundle, diagnostics := readAgentOwnershipBundle(context.Background(), valid)
	if diagnostics.HasError() || !bundle.versioned || !agentFieldSetsEqual(bundle.pending, pending) {
		t.Fatalf("valid bundle rejected: %#v %#v", bundle, diagnostics)
	}
	corrupt := []agentTestPrivate{
		{agentOwnershipInitializedPrivateKey: []byte("true")},
		{agentImportedFieldsPrivateKey: encodeAgentFieldSet(committed)},
		{agentOwnershipInitializedPrivateKey: []byte("1"), agentImportedFieldsPrivateKey: encodeAgentFieldSet(committed)},
		{agentOwnershipInitializedPrivateKey: []byte("true"), agentImportedFieldsPrivateKey: encodeAgentFieldSet(committed), agentOwnershipPendingPrivateKey: encodeAgentFieldSet(agentFieldSet{agentFieldCardName: true})},
	}
	for _, private := range corrupt {
		if _, diagnostics := readAgentOwnershipBundle(context.Background(), private); !diagnostics.HasError() {
			t.Fatalf("partial/overlapping bundle accepted: %#v", private)
		}
	}
}

func TestAgentResourceStructuredSchemaCompatibility(t *testing.T) {
	var response resource.SchemaResponse
	(&AgentResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	legacy := response.Schema.Attributes["litellm_params"].(resourceschema.MapAttribute)
	if legacy.ElementType != types.StringType || !legacy.Optional || !legacy.Computed || !legacy.Sensitive {
		t.Fatalf("legacy schema changed: %#v", legacy)
	}
	structured := response.Schema.Attributes["litellm_params_json"].(resourceschema.StringAttribute)
	if !structured.Optional || !structured.Computed || !structured.Sensitive {
		t.Fatalf("structured schema=%#v", structured)
	}
	params, _, err := configuredAgentParams(types.MapNull(types.StringType), types.StringValue(`{}`))
	if err != nil || len(params) != 0 {
		t.Fatalf("empty structured object: %#v %v", params, err)
	}
}
