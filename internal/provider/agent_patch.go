package provider

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

type agentPatchPreservation struct {
	paramsBase  map[string]interface{}
	paramsPatch map[string]interface{}
	cardBase    map[string]interface{}
	cardPatch   map[string]interface{}
}

func cloneAgentWireObject(source map[string]interface{}) map[string]interface{} {
	if source == nil {
		return nil
	}
	out := make(map[string]interface{}, len(source))
	for key, value := range source {
		out[key] = cloneAgentWireValue(value)
	}
	return out
}

func cloneAgentWireValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		return cloneAgentWireObject(typed)
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, item := range typed {
			out[index] = cloneAgentWireValue(item)
		}
		return out
	default:
		return value
	}
}

func agentParamsConfiguredKeys(data AgentResourceModel) (map[string]interface{}, error) {
	values, _, err := configuredAgentParams(data.LiteLLMParams, data.LiteLLMParamsJSON)
	return values, err
}

func agentParamsUpdateTouched(plan, prior, config AgentResourceModel, imported agentFieldSet) bool {
	desired, err := agentParamsConfiguredKeys(config)
	if err != nil {
		return true
	}
	priorObject := map[string]interface{}{}
	if !prior.LiteLLMParamsJSON.IsNull() && !prior.LiteLLMParamsJSON.IsUnknown() {
		priorObject, _ = decodeAgentJSONObject(prior.LiteLLMParamsJSON.ValueString())
	}
	if !prior.LiteLLMParams.IsNull() && !prior.LiteLLMParams.IsUnknown() {
		for key, raw := range prior.LiteLLMParams.Elements() {
			if value, ok := raw.(types.String); ok && !value.IsNull() && !value.IsUnknown() {
				if _, exists := priorObject[key]; !exists {
					priorObject[key] = value.ValueString()
				}
			}
		}
	}
	for key, value := range desired {
		priorValue, present := priorObject[key]
		if !present || !exactJSONValuesEqual(value, priorValue) {
			return true
		}
	}
	for key := range priorObject {
		if imported[agentLeaf(agentFieldParams, key)] {
			continue
		}
		if _, retained := desired[key]; !retained {
			return true
		}
	}
	_ = plan
	return false
}

func overlayAgentParamsWire(base map[string]interface{}, prior, config AgentResourceModel, imported agentFieldSet) (map[string]interface{}, error) {
	patch := cloneAgentWireObject(stripAgentSyntheticParams(base))
	desired, err := agentParamsConfiguredKeys(config)
	if err != nil {
		return nil, err
	}
	priorKeys := map[string]bool{}
	if !prior.LiteLLMParamsJSON.IsNull() && !prior.LiteLLMParamsJSON.IsUnknown() {
		object, decodeErr := decodeAgentJSONObject(prior.LiteLLMParamsJSON.ValueString())
		if decodeErr != nil {
			return nil, decodeErr
		}
		for key := range object {
			priorKeys[key] = true
		}
	}
	if !prior.LiteLLMParams.IsNull() && !prior.LiteLLMParams.IsUnknown() {
		for key := range prior.LiteLLMParams.Elements() {
			priorKeys[key] = true
		}
	}
	for key := range priorKeys {
		if imported[agentLeaf(agentFieldParams, key)] {
			continue
		}
		if _, retained := desired[key]; !retained {
			delete(patch, key)
		}
	}
	for key, value := range desired {
		patch[key] = cloneAgentWireValue(value)
	}
	if err := validateAgentCorePair(patch); err != nil {
		return nil, err
	}
	return patch, nil
}

func agentWireObjectsEqual(left, right map[string]interface{}) bool {
	return exactJSONValuesEqual(left, right)
}

// LiteLLM v1.98's merge_agent_card filters every full-card PATCH through this
// exact source allowlist and filters capabilities to truthy streaming only.
// Reject before PATCH when a fresh authoritative path would therefore be lost.
func validateAgentCardV198RoundTrip(card map[string]interface{}) error {
	allowed := map[string]bool{
		"protocolVersion": true, "name": true, "description": true, "version": true,
		"capabilities": true, "defaultInputModes": true, "defaultOutputModes": true,
		"skills": true, "preferredTransport": true, "supportedInterfaces": true,
		"iconUrl": true, "provider": true, "documentationUrl": true,
		"securitySchemes": true, "security": true, "supportsAuthenticatedExtendedCard": true,
		"signatures": true, "url": true,
	}
	for key := range card {
		if !allowed[key] {
			return fmt.Errorf("authoritative agent card contains a path LiteLLM v1.98 cannot round-trip")
		}
	}
	if raw, present := card["capabilities"]; present {
		capabilities, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("authoritative agent card contains a path LiteLLM v1.98 cannot round-trip")
		}
		for key, value := range capabilities {
			streaming, isBool := value.(bool)
			if key != "streaming" || !isBool || !streaming {
				return fmt.Errorf("authoritative agent card contains a path LiteLLM v1.98 cannot round-trip")
			}
		}
	}
	return nil
}

func (r *AgentResource) sampleFreshAgentUpdateBase(ctx context.Context, state AgentResourceModel, needParams, needCard bool, maxAttempts int) (map[string]interface{}, map[string]interface{}, error) {
	if maxAttempts < 2 {
		maxAttempts = 2
	}
	endpoint := fmt.Sprintf("/v1/agents/%s", url.PathEscape(state.ID.ValueString()))
	delay := 250 * time.Millisecond
	var priorParams, priorCard map[string]interface{}
	matched := false
	for attempt := 0; attempt < maxAttempts; attempt++ {
		var result map[string]interface{}
//line internal/provider/agent_patch.go:139
		err := r.client.doFreshRequestWithResponse(ctx, "GET", endpoint, nil, &result)
		if err == nil {
			err = validateImportedObjectIdentity(true, "agent", result, "agent_id", state.ID.ValueString())
		}
		if err == nil {
			err = requireImportedStringField(true, "agent", result, "agent_name")
		}
		var params, card map[string]interface{}
		if err == nil && needParams {
			raw, present := result["litellm_params"]
			var ok bool
			params, ok = raw.(map[string]interface{})
			if !present || !ok || params == nil {
				err = fmt.Errorf("fresh agent parameters are not an authoritative object")
			} else {
				params = stripAgentSyntheticParams(params)
			}
		}
		if err == nil && needCard {
			raw, present := result["agent_card_params"]
			var ok bool
			card, ok = raw.(map[string]interface{})
			if !present || !ok || card == nil || validateAgentCardResponse(card, true) != nil {
				err = fmt.Errorf("fresh agent card is not an authoritative object")
			}
		}
		if err == nil {
			if matched && (!needParams || agentWireObjectsEqual(priorParams, params)) && (!needCard || agentWireObjectsEqual(priorCard, card)) {
				return cloneAgentWireObject(params), cloneAgentWireObject(card), nil
			}
			priorParams, priorCard = cloneAgentWireObject(params), cloneAgentWireObject(card)
			matched = true
		} else {
			matched = false
		}
		if attempt < maxAttempts-1 {
			if err := waitAgentFreshSample(ctx, delay); err != nil {
				return nil, nil, err
			}
			delay *= 2
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
		}
	}
	return nil, nil, fmt.Errorf("fresh agent update base did not converge")
}

func setAgentWireField(target map[string]interface{}, source map[string]interface{}, key string) {
	if value, present := source[key]; present {
		target[key] = cloneAgentWireValue(value)
	} else {
		delete(target, key)
	}
}

func agentWireObject(raw interface{}) map[string]interface{} {
	object, _ := raw.(map[string]interface{})
	return object
}

func agentWireObjectList(raw interface{}) []map[string]interface{} {
	result := []map[string]interface{}{}
	switch items := raw.(type) {
	case []interface{}:
		for _, item := range items {
			if object, ok := item.(map[string]interface{}); ok {
				result = append(result, object)
			}
		}
	case []map[string]interface{}:
		result = append(result, items...)
	}
	return result
}

func agentSkillRawByID(card map[string]interface{}) map[string]map[string]interface{} {
	result := map[string]map[string]interface{}{}
	for _, skill := range agentWireObjectList(card["skills"]) {
		if id, ok := skill["id"].(string); ok {
			result[id] = skill
		}
	}
	return result
}

func markAgentSkillWireLeaves(fields agentFieldSet, id string, raw map[string]interface{}) {
	if raw == nil {
		return
	}
	for field, wire := range map[string]string{
		"id": "id", "name": "name", "description": "description", "tags": "tags", "examples": "examples",
		"input_modes": "inputModes", "output_modes": "outputModes", "security": "security",
	} {
		if _, present := raw[wire]; present {
			fields[agentSkillLeaf(id, field)] = true
		}
	}
}

func agentImportedFieldsFromWire(data AgentResourceModel, raw map[string]interface{}) agentFieldSet {
	fields := agentImportedFieldsFromState(data)
	card, _ := raw["agent_card_params"].(map[string]interface{})
	for marker := range fields {
		if strings.HasPrefix(marker, "agent_card.") {
			delete(fields, marker)
		}
	}
	if card != nil {
		for field, wire := range map[string]string{
			agentFieldCardName: "name", agentFieldCardURL: "url", agentFieldCardDescription: "description", agentFieldCardVersion: "version",
			agentFieldCardProtocol: "protocolVersion", agentFieldCardInputModes: "defaultInputModes", agentFieldCardOutputModes: "defaultOutputModes",
			agentFieldCardTransport: "preferredTransport", agentFieldCardIcon: "iconUrl", agentFieldCardDocumentation: "documentationUrl",
			agentFieldCardAuthenticated: "supportsAuthenticatedExtendedCard",
		} {
			if _, present := card[wire]; present {
				fields[field] = true
			}
		}
		if capabilities, present := card["capabilities"].(map[string]interface{}); present {
			for field, wire := range map[string]string{agentFieldCardCapStreaming: "streaming", agentFieldCardCapPush: "pushNotifications", agentFieldCardCapHistory: "stateTransitionHistory"} {
				if _, present := capabilities[wire]; present {
					fields[field] = true
				}
			}
		}
		if provider, present := card["provider"].(map[string]interface{}); present {
			for field, wire := range map[string]string{agentFieldCardProviderOrg: "organization", agentFieldCardProviderURL: "url"} {
				if _, present := provider[wire]; present {
					fields[field] = true
				}
			}
		}
		for id, skill := range agentSkillRawByID(card) {
			markAgentSkillWireLeaves(fields, id, skill)
		}
		if rawSignatures, present := card["signatures"]; present {
			for index, signature := range agentWireObjectList(rawSignatures) {
				for _, field := range []string{"protected", "signature", "header"} {
					if _, wirePresent := signature[field]; wirePresent {
						fields[agentSignatureLeaf(index, field)] = true
					}
				}
			}
		}
	}
	return fields
}

func overlayAgentCardRaw(base map[string]interface{}, plan, prior, config AgentResourceModel, imported agentFieldSet) (map[string]interface{}, error) {
	patch := cloneAgentWireObject(base)
	if plan.AgentCard == nil || config.AgentCard == nil {
		return patch, nil
	}
	builder := &AgentResource{}
	model := cloneAgentResourceModel(plan)
	model.LiteLLMParams = types.MapNull(types.StringType)
	model.LiteLLMParamsJSON = types.StringNull()
	built, err := builder.buildAgentRequest(&model)
	if err != nil {
		return nil, err
	}
	desired, ok := built["agent_card_params"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("configured agent card could not be represented")
	}
	configured, priorFields := agentConfiguredFields(config), agentConfiguredFields(prior)
	usePlan := func(field string) bool {
		return configured[field] || (priorFields[field] && !imported[field] && !configured[field])
	}
	for field, wire := range map[string]string{
		agentFieldCardName: "name", agentFieldCardURL: "url", agentFieldCardDescription: "description", agentFieldCardVersion: "version",
		agentFieldCardProtocol: "protocolVersion", agentFieldCardInputModes: "defaultInputModes", agentFieldCardOutputModes: "defaultOutputModes",
		agentFieldCardTransport: "preferredTransport", agentFieldCardIcon: "iconUrl", agentFieldCardDocumentation: "documentationUrl",
		agentFieldCardAuthenticated: "supportsAuthenticatedExtendedCard",
	} {
		if usePlan(field) {
			setAgentWireField(patch, desired, wire)
		}
	}
	for _, wire := range map[string]struct {
		wire   string
		leaves map[string]string
	}{
		agentFieldCardCapabilities: {"capabilities", map[string]string{agentFieldCardCapStreaming: "streaming", agentFieldCardCapPush: "pushNotifications", agentFieldCardCapHistory: "stateTransitionHistory"}},
		agentFieldCardProvider:     {"provider", map[string]string{agentFieldCardProviderOrg: "organization", agentFieldCardProviderURL: "url"}},
	} {
		current := cloneAgentWireObject(agentWireObject(patch[wire.wire]))
		if current == nil {
			current = map[string]interface{}{}
		}
		desiredObject := agentWireObject(desired[wire.wire])
		for field, childWire := range wire.leaves {
			if usePlan(field) {
				setAgentWireField(current, desiredObject, childWire)
			}
		}
		if len(current) == 0 {
			delete(patch, wire.wire)
		} else {
			patch[wire.wire] = current
		}
	}
	if config.AgentCard.Signatures != nil {
		baseSignatures := agentWireObjectList(patch["signatures"])
		desiredSignatures := agentWireObjectList(desired["signatures"])
		merged := make([]interface{}, 0, len(baseSignatures)+len(desiredSignatures))
		priorSignatureCount := 0
		if prior.AgentCard != nil {
			priorSignatureCount = len(prior.AgentCard.Signatures)
		}
		for index, desiredSignature := range desiredSignatures {
			current := map[string]interface{}{}
			if index < len(baseSignatures) {
				current = cloneAgentWireObject(baseSignatures[index])
			}
			for _, wire := range []string{"protected", "signature", "header"} {
				marker := agentSignatureLeaf(index, wire)
				if configured[marker] || (priorFields[marker] && !imported[marker] && !configured[marker]) {
					setAgentWireField(current, desiredSignature, wire)
				}
			}
			merged = append(merged, current)
		}
		for index := len(desiredSignatures); index < len(baseSignatures); index++ {
			if agentFieldSetHasPrefix(imported, fmt.Sprintf("%s[%d].", agentFieldCardSignatures, index)) || (imported[agentScopeCardSignatures] && index >= priorSignatureCount) {
				merged = append(merged, cloneAgentWireObject(baseSignatures[index]))
			}
		}
		patch["signatures"] = merged
	} else if prior.AgentCard != nil && prior.AgentCard.Signatures != nil && !agentFieldSetHasPrefix(imported, agentFieldCardSignatures+"[") {
		delete(patch, "signatures")
	}
	if config.AgentCard.Skills != nil {
		freshByID := agentSkillRawByID(patch)
		desiredByID := agentSkillRawByID(desired)
		priorIDs := map[string]bool{}
		if prior.AgentCard != nil {
			for _, skill := range prior.AgentCard.Skills {
				if !skill.ID.IsNull() && !skill.ID.IsUnknown() {
					priorIDs[skill.ID.ValueString()] = true
				}
			}
		}
		merged := make([]interface{}, 0, len(freshByID)+len(plan.AgentCard.Skills))
		seen := map[string]bool{}
		for _, desiredModel := range plan.AgentCard.Skills {
			id := desiredModel.ID.ValueString()
			current := cloneAgentWireObject(freshByID[id])
			if current == nil {
				current = map[string]interface{}{}
			}
			desiredSkill := desiredByID[id]
			for field, wire := range map[string]string{"id": "id", "name": "name", "description": "description", "tags": "tags", "examples": "examples", "input_modes": "inputModes", "output_modes": "outputModes", "security": "security"} {
				marker := agentSkillLeaf(id, field)
				if configured[marker] || (priorFields[marker] && !imported[marker] && !configured[marker]) {
					setAgentWireField(current, desiredSkill, wire)
				}
			}
			merged = append(merged, current)
			seen[id] = true
		}
		for _, remote := range agentWireObjectList(patch["skills"]) {
			id, _ := remote["id"].(string)
			if seen[id] {
				continue
			}
			if agentFieldSetHasPrefix(imported, agentLeaf(agentFieldCardSkills, id)+".") || (imported[agentScopeCardSkills] && !priorIDs[id]) {
				merged = append(merged, cloneAgentWireObject(remote))
			}
		}
		patch["skills"] = merged
	} else if prior.AgentCard != nil && prior.AgentCard.Skills != nil {
		merged := make([]interface{}, 0, len(agentWireObjectList(patch["skills"])))
		priorIDs := map[string]bool{}
		for _, skill := range prior.AgentCard.Skills {
			if !skill.ID.IsNull() && !skill.ID.IsUnknown() {
				priorIDs[skill.ID.ValueString()] = true
			}
		}
		for _, remote := range agentWireObjectList(patch["skills"]) {
			id, _ := remote["id"].(string)
			if priorIDs[id] && !agentFieldSetHasPrefix(imported, agentLeaf(agentFieldCardSkills, id)+".") {
				continue
			}
			merged = append(merged, cloneAgentWireObject(remote))
		}
		patch["skills"] = merged
	}
	return patch, nil
}

func agentPreservedValueMatches(base, patch, observed interface{}, path string) bool {
	if exactJSONValuesEqual(base, patch) {
		return exactJSONValuesEqual(base, observed)
	}
	baseObject, baseOK := base.(map[string]interface{})
	patchObject, patchOK := patch.(map[string]interface{})
	observedObject, observedOK := observed.(map[string]interface{})
	if baseOK && patchOK {
		if !observedOK {
			return false
		}
		keys := map[string]bool{}
		for key := range baseObject {
			keys[key] = true
		}
		for key := range patchObject {
			keys[key] = true
		}
		for key := range observedObject {
			keys[key] = true
		}
		for key := range keys {
			baseValue, basePresent := baseObject[key]
			patchValue, patchPresent := patchObject[key]
			observedValue, observedPresent := observedObject[key]
			if basePresent == patchPresent && (!basePresent || exactJSONValuesEqual(baseValue, patchValue)) {
				if basePresent != observedPresent || (basePresent && !exactJSONValuesEqual(baseValue, observedValue)) {
					return false
				}
				continue
			}
			if basePresent && patchPresent && !agentPreservedValueMatches(baseValue, patchValue, observedValue, path+"."+key) {
				return false
			}
		}
		return true
	}
	if path == "agent_card_params.skills" {
		return agentPreservedSkillsMatch(base, patch, observed)
	}
	if path == "agent_card_params.signatures" {
		return agentPreservedSignaturesMatch(base, patch, observed)
	}
	return true
}

func agentPreservedSignaturesMatch(base, patch, observed interface{}) bool {
	baseList, patchList, observedList := agentWireObjectList(base), agentWireObjectList(patch), agentWireObjectList(observed)
	max := len(baseList)
	if len(patchList) > max {
		max = len(patchList)
	}
	if len(observedList) > max {
		max = len(observedList)
	}
	for index := 0; index < max; index++ {
		bp, pp, op := index < len(baseList), index < len(patchList), index < len(observedList)
		if bp == pp && (!bp || exactJSONValuesEqual(baseList[index], patchList[index])) {
			if bp != op || (bp && !exactJSONValuesEqual(baseList[index], observedList[index])) {
				return false
			}
			continue
		}
		if bp && pp && (!op || !agentPreservedValueMatches(baseList[index], patchList[index], observedList[index], fmt.Sprintf("agent_card_params.signatures.%d", index))) {
			return false
		}
	}
	return true
}

func agentPreservedSkillsMatch(base, patch, observed interface{}) bool {
	asMap := func(value interface{}) map[string]map[string]interface{} {
		card := map[string]interface{}{"skills": value}
		return agentSkillRawByID(card)
	}
	baseByID, patchByID, observedByID := asMap(base), asMap(patch), asMap(observed)
	ids := map[string]bool{}
	for id := range baseByID {
		ids[id] = true
	}
	for id := range patchByID {
		ids[id] = true
	}
	for id := range observedByID {
		ids[id] = true
	}
	for id := range ids {
		baseSkill, bp := baseByID[id]
		patchSkill, pp := patchByID[id]
		observedSkill, op := observedByID[id]
		if bp == pp && (!bp || exactJSONValuesEqual(baseSkill, patchSkill)) {
			if bp != op || (bp && !exactJSONValuesEqual(baseSkill, observedSkill)) {
				return false
			}
			continue
		}
		if bp && pp && (!op || !agentPreservedValueMatches(baseSkill, patchSkill, observedSkill, "agent_card_params.skills."+id)) {
			return false
		}
	}
	return true
}

func (e *agentPatchPreservation) matches(raw map[string]interface{}) bool {
	if e == nil {
		return true
	}
	if e.paramsBase != nil {
		rawParams, ok := raw["litellm_params"].(map[string]interface{})
		if !ok || !agentPreservedValueMatches(e.paramsBase, e.paramsPatch, stripAgentSyntheticParams(rawParams), "litellm_params") {
			return false
		}
	}
	if e.cardBase != nil {
		rawCard, ok := raw["agent_card_params"].(map[string]interface{})
		if !ok || validateAgentCardResponse(rawCard, true) != nil || !agentPreservedValueMatches(e.cardBase, e.cardPatch, rawCard, "agent_card_params") {
			return false
		}
	}
	return true
}
