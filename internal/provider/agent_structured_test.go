package provider

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

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
	if err := validateAgentCorePair(map[string]interface{}{"model": "bedrock/agentcore/runtime", "custom_llm_provider": "openai"}); err == nil {
		t.Fatal("contradictory AgentCore provider pairing accepted")
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
