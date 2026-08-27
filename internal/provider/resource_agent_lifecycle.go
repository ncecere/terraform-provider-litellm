package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	agentImportedFieldsPrivateKey       = "agent_imported_fields_v1"
	agentOwnershipInitializedPrivateKey = "agent_ownership_initialized_v1"
	agentOwnershipPendingPrivateKey     = "agent_ownership_pending_v1"
	agentOwnershipMigrationPrivateKey   = "agent_ownership_migration_v1"
	agentCollectionsPrivateKey          = "agent_hidden_collections_v1"
)

type agentCollectionProvenance struct {
	Skills     []interface{} `json:"skills"`
	Signatures []interface{} `json:"signatures"`
}

func emptyAgentCollectionProvenance() agentCollectionProvenance {
	return agentCollectionProvenance{Skills: []interface{}{}, Signatures: []interface{}{}}
}

func encodeAgentCollectionProvenance(value agentCollectionProvenance) []byte {
	if value.Skills == nil {
		value.Skills = []interface{}{}
	}
	if value.Signatures == nil {
		value.Signatures = []interface{}{}
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

func decodeAgentCollectionProvenance(raw []byte) (agentCollectionProvenance, error) {
	result := emptyAgentCollectionProvenance()
	if raw == nil {
		return result, nil
	}
	var decoded interface{}
	if err := decodeJSONUseNumber(raw, &decoded); err != nil {
		return result, fmt.Errorf("provider-private agent collection data is malformed")
	}
	object, ok := decoded.(map[string]interface{})
	if !ok || len(object) != 2 {
		return result, fmt.Errorf("provider-private agent collection data is malformed")
	}
	skills, skillsOK := object["skills"].([]interface{})
	signatures, signaturesOK := object["signatures"].([]interface{})
	if !skillsOK || !signaturesOK {
		return result, fmt.Errorf("provider-private agent collection data is malformed")
	}
	for _, rawSkill := range skills {
		skill, ok := rawSkill.(map[string]interface{})
		if !ok {
			return result, fmt.Errorf("provider-private agent collection data is malformed")
		}
		id, idOK := skill["id"].(string)
		if !idOK || strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) {
			return result, fmt.Errorf("provider-private agent collection data is malformed")
		}
	}
	for _, rawSignature := range signatures {
		if _, ok := rawSignature.(map[string]interface{}); !ok {
			return result, fmt.Errorf("provider-private agent collection data is malformed")
		}
	}
	result.Skills, result.Signatures = skills, signatures
	if !bytes.Equal(raw, encodeAgentCollectionProvenance(result)) {
		return emptyAgentCollectionProvenance(), fmt.Errorf("provider-private agent collection data is not canonical")
	}
	return result, nil
}

type agentPrivateReader interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}

type agentFieldSet map[string]bool

func encodeAgentFieldSet(fields agentFieldSet) []byte {
	names := make([]string, 0, len(fields))
	for name, present := range fields {
		if present {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	encoded, _ := json.Marshal(names)
	return encoded
}

func agentOwnershipMarkerKnown(name string) bool {
	fixed := map[string]bool{
		agentFieldParamsJSON: true, agentFieldTPM: true, agentFieldRPM: true, agentFieldSessionTPM: true, agentFieldSessionRPM: true,
		agentFieldExtraHeaders: true, agentFieldCardName: true, agentFieldCardURL: true,
		agentFieldCardDescription: true, agentFieldCardVersion: true, agentFieldCardProtocol: true,
		agentFieldCardInputModes: true, agentFieldCardOutputModes: true, agentFieldCardCapStreaming: true,
		agentFieldCardCapPush: true, agentFieldCardCapHistory: true, agentFieldCardProviderOrg: true,
		agentFieldCardProviderURL: true, agentFieldCardTransport: true, agentFieldCardIcon: true,
		agentFieldCardDocumentation: true, agentFieldCardAuthenticated: true,
		agentFieldPermissionServers: true, agentFieldPermissionGroups: true, agentFieldPermissionTools: true,
		agentFieldPermissionModels: true, agentFieldPermissionAgents: true,
		agentScopeParams: true, agentScopeStaticHeaders: true, agentScopeCardCapabilities: true,
		agentScopeCardProvider: true, agentScopeCardSkills: true, agentScopeCardSignatures: true,
		agentScopePermission: true,
	}
	if fixed[name] {
		return true
	}
	decodeLeaf := func(prefix, suffix string) bool {
		if !strings.HasPrefix(name, prefix+"[") || !strings.HasSuffix(name, suffix) {
			return false
		}
		encoded := strings.TrimSuffix(strings.TrimPrefix(name, prefix+"["), suffix)
		decoded, err := base64.RawURLEncoding.DecodeString(encoded)
		return err == nil && len(decoded) > 0 && base64.RawURLEncoding.EncodeToString(decoded) == encoded
	}
	if decodeLeaf(agentFieldParams, "]") || decodeLeaf(agentFieldStaticHeaders, "]") {
		return true
	}
	for _, field := range []string{"id", "name", "description", "tags", "examples", "input_modes", "output_modes", "security"} {
		if decodeLeaf(agentFieldCardSkills, "]."+field) {
			return true
		}
	}
	if strings.HasPrefix(name, agentFieldCardSignatures+"[") {
		close := strings.Index(name[len(agentFieldCardSignatures)+1:], "]")
		if close >= 0 {
			indexText := name[len(agentFieldCardSignatures)+1 : len(agentFieldCardSignatures)+1+close]
			var index int
			if _, err := fmt.Sscanf(indexText, "%d", &index); err == nil && index >= 0 && fmt.Sprintf("%d", index) == indexText {
				suffix := name[len(agentFieldCardSignatures)+2+close:]
				return suffix == ".protected" || suffix == ".signature" || suffix == ".header"
			}
		}
	}
	return false
}

func decodeAgentFieldSet(raw []byte) (agentFieldSet, error) {
	if raw == nil {
		return nil, fmt.Errorf("provider-private agent ownership data is missing")
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil || names == nil {
		return nil, fmt.Errorf("provider-private agent ownership data is malformed")
	}
	fields := agentFieldSet{}
	for _, name := range names {
		if !agentOwnershipMarkerKnown(name) || fields[name] {
			return nil, fmt.Errorf("provider-private agent ownership data is malformed")
		}
		fields[name] = true
	}
	if !bytes.Equal(raw, encodeAgentFieldSet(fields)) {
		return nil, fmt.Errorf("provider-private agent ownership data is not canonical")
	}
	return fields, nil
}

type agentOwnershipBundle struct {
	committed   agentFieldSet
	pending     agentFieldSet
	collections agentCollectionProvenance
	versioned   bool
	migration   bool
}

func readAgentOwnershipBundle(ctx context.Context, private agentPrivateReader) (agentOwnershipBundle, diag.Diagnostics) {
	result := agentOwnershipBundle{committed: agentFieldSet{}, collections: emptyAgentCollectionProvenance()}
	var diagnostics diag.Diagnostics
	if private == nil {
		return result, diagnostics
	}
	committedRaw, committedDiags := private.GetKey(ctx, agentImportedFieldsPrivateKey)
	initializedRaw, initializedDiags := private.GetKey(ctx, agentOwnershipInitializedPrivateKey)
	pendingRaw, pendingDiags := private.GetKey(ctx, agentOwnershipPendingPrivateKey)
	migrationRaw, migrationDiags := private.GetKey(ctx, agentOwnershipMigrationPrivateKey)
	collectionsRaw, collectionsDiags := private.GetKey(ctx, agentCollectionsPrivateKey)
	diagnostics.Append(committedDiags...)
	diagnostics.Append(initializedDiags...)
	diagnostics.Append(pendingDiags...)
	diagnostics.Append(migrationDiags...)
	diagnostics.Append(collectionsDiags...)
	if diagnostics.HasError() {
		return result, diagnostics
	}
	any := committedRaw != nil || initializedRaw != nil || pendingRaw != nil || migrationRaw != nil || collectionsRaw != nil
	if !any {
		return result, diagnostics
	}
	invalid := func() (agentOwnershipBundle, diag.Diagnostics) {
		diagnostics.AddError("Invalid Agent Ownership State", "Provider-private agent ownership data is invalid. Prior public and private state was retained; no remote operation was attempted. This diagnostic contains no public values or identifiers.")
		return result, diagnostics
	}
	if committedRaw == nil || string(initializedRaw) != "true" {
		return invalid()
	}
	committed, err := decodeAgentFieldSet(committedRaw)
	if err != nil {
		return invalid()
	}
	collections, err := decodeAgentCollectionProvenance(collectionsRaw)
	if err != nil {
		return invalid()
	}
	result.committed, result.collections, result.versioned = committed, collections, true
	if migrationRaw != nil && (string(migrationRaw) != "true" || pendingRaw == nil) {
		return invalid()
	}
	result.migration = migrationRaw != nil
	if pendingRaw != nil {
		pending, err := decodeAgentFieldSet(pendingRaw)
		if err != nil {
			return invalid()
		}
		for field := range pending {
			if !committed[field] {
				return invalid()
			}
		}
		result.pending = pending
	}
	return result, diagnostics
}

func readAgentImportedFields(ctx context.Context, private agentPrivateReader) (agentFieldSet, diag.Diagnostics) {
	bundle, diagnostics := readAgentOwnershipBundle(ctx, private)
	return bundle.committed, diagnostics
}

func cloneAgentFieldSet(source agentFieldSet) agentFieldSet {
	result := agentFieldSet{}
	for field := range source {
		result[field] = true
	}
	return result
}

func agentFieldSetsEqual(left, right agentFieldSet) bool {
	if len(left) != len(right) {
		return false
	}
	for field := range left {
		if !right[field] {
			return false
		}
	}
	return true
}

const (
	agentFieldParams            = "litellm_params"
	agentFieldParamsJSON        = "litellm_params_json"
	agentFieldTPM               = "tpm_limit"
	agentFieldRPM               = "rpm_limit"
	agentFieldSessionTPM        = "session_tpm_limit"
	agentFieldSessionRPM        = "session_rpm_limit"
	agentFieldStaticHeaders     = "static_headers"
	agentFieldExtraHeaders      = "extra_headers"
	agentFieldCard              = "agent_card"
	agentFieldCardName          = "agent_card.name"
	agentFieldCardURL           = "agent_card.url"
	agentFieldCardDescription   = "agent_card.description"
	agentFieldCardVersion       = "agent_card.version"
	agentFieldCardProtocol      = "agent_card.protocol_version"
	agentFieldCardInputModes    = "agent_card.default_input_modes"
	agentFieldCardOutputModes   = "agent_card.default_output_modes"
	agentFieldCardCapabilities  = "agent_card.capabilities"
	agentFieldCardCapStreaming  = "agent_card.capabilities.streaming"
	agentFieldCardCapPush       = "agent_card.capabilities.push_notifications"
	agentFieldCardCapHistory    = "agent_card.capabilities.state_transition_history"
	agentFieldCardProvider      = "agent_card.provider"
	agentFieldCardProviderOrg   = "agent_card.provider.organization"
	agentFieldCardProviderURL   = "agent_card.provider.url"
	agentFieldCardSkills        = "agent_card.skills"
	agentFieldCardTransport     = "agent_card.preferred_transport"
	agentFieldCardIcon          = "agent_card.icon_url"
	agentFieldCardDocumentation = "agent_card.documentation_url"
	agentFieldCardAuthenticated = "agent_card.supports_authenticated_extended_card"
	agentFieldCardSignatures    = "agent_card.signatures"
	agentFieldPermission        = "object_permission"
	agentFieldPermissionServers = "object_permission.mcp_servers"
	agentFieldPermissionGroups  = "object_permission.mcp_access_groups"
	agentFieldPermissionTools   = "object_permission.mcp_tool_permissions"
	agentFieldPermissionModels  = "object_permission.models"
	agentFieldPermissionAgents  = "object_permission.agents"

	// Structural scope markers let imported collections adopt later API-side
	// additions without treating a configured child as ownership of its siblings.
	// They deliberately do not share the leaf prefixes used by removal checks.
	agentScopeParams           = "__api_scope.litellm_params"
	agentScopeStaticHeaders    = "__api_scope.static_headers"
	agentScopeCardCapabilities = "__api_scope.agent_card.capabilities"
	agentScopeCardProvider     = "__api_scope.agent_card.provider"
	agentScopeCardSkills       = "__api_scope.agent_card.skills"
	agentScopeCardSignatures   = "__api_scope.agent_card.signatures"
	agentScopePermission       = "__api_scope.object_permission"
)

func agentLeaf(prefix, value string) string {
	return prefix + "[" + base64.RawURLEncoding.EncodeToString([]byte(value)) + "]"
}

func agentSkillLeaf(id, field string) string {
	return agentLeaf(agentFieldCardSkills, id) + "." + field
}

func agentSignatureLeaf(index int, field string) string {
	return fmt.Sprintf("%s[%d].%s", agentFieldCardSignatures, index, field)
}

func agentFieldSetHasPrefix(fields agentFieldSet, prefix string) bool {
	for field := range fields {
		if strings.HasPrefix(field, prefix) {
			return true
		}
	}
	return false
}

func agentConfiguredFields(data AgentResourceModel) agentFieldSet {
	fields := agentFieldSet{}
	knownMap := func(name string, value types.Map) {
		if !value.IsNull() && !value.IsUnknown() {
			fields[name] = true
		}
	}
	knownList := func(name string, value types.List) {
		if !value.IsNull() && !value.IsUnknown() {
			fields[name] = true
		}
	}
	knownInt := func(name string, value types.Int64) {
		if !value.IsNull() && !value.IsUnknown() {
			fields[name] = true
		}
	}
	knownString := func(name string, value types.String) {
		if !value.IsNull() && !value.IsUnknown() {
			fields[name] = true
		}
	}
	knownBool := func(name string, value types.Bool) {
		if !value.IsNull() && !value.IsUnknown() {
			fields[name] = true
		}
	}
	knownMap(agentFieldParams, data.LiteLLMParams)
	knownString(agentFieldParamsJSON, data.LiteLLMParamsJSON)
	if !data.LiteLLMParamsJSON.IsNull() && !data.LiteLLMParamsJSON.IsUnknown() {
		if object, err := decodeAgentJSONObject(data.LiteLLMParamsJSON.ValueString()); err == nil {
			for key := range object {
				fields[agentLeaf(agentFieldParams, key)] = true
			}
		}
	}
	if !data.LiteLLMParams.IsNull() && !data.LiteLLMParams.IsUnknown() {
		for key := range data.LiteLLMParams.Elements() {
			fields[agentLeaf(agentFieldParams, key)] = true
		}
	}
	knownInt(agentFieldTPM, data.TPMLimit)
	knownInt(agentFieldRPM, data.RPMLimit)
	knownInt(agentFieldSessionTPM, data.SessionTPMLimit)
	knownInt(agentFieldSessionRPM, data.SessionRPMLimit)
	knownMap(agentFieldStaticHeaders, data.StaticHeaders)
	if !data.StaticHeaders.IsNull() && !data.StaticHeaders.IsUnknown() {
		for key := range data.StaticHeaders.Elements() {
			fields[agentLeaf(agentFieldStaticHeaders, key)] = true
		}
	}
	knownList(agentFieldExtraHeaders, data.ExtraHeaders)
	if data.AgentCard != nil {
		fields[agentFieldCard] = true
		knownString(agentFieldCardName, data.AgentCard.Name)
		knownString(agentFieldCardURL, data.AgentCard.URL)
		knownString(agentFieldCardDescription, data.AgentCard.Description)
		knownString(agentFieldCardVersion, data.AgentCard.Version)
		knownString(agentFieldCardProtocol, data.AgentCard.ProtocolVersion)
		knownList(agentFieldCardInputModes, data.AgentCard.DefaultInputModes)
		knownList(agentFieldCardOutputModes, data.AgentCard.DefaultOutputModes)
		knownString(agentFieldCardTransport, data.AgentCard.PreferredTransport)
		knownString(agentFieldCardIcon, data.AgentCard.IconURL)
		knownString(agentFieldCardDocumentation, data.AgentCard.DocumentationURL)
		knownBool(agentFieldCardAuthenticated, data.AgentCard.SupportsAuthenticatedExtendedCard)
		if data.AgentCard.Signatures != nil {
			fields[agentFieldCardSignatures] = true
			for index, signature := range data.AgentCard.Signatures {
				if !signature.Protected.IsNull() && !signature.Protected.IsUnknown() {
					fields[agentSignatureLeaf(index, "protected")] = true
				}
				if !signature.Signature.IsNull() && !signature.Signature.IsUnknown() {
					fields[agentSignatureLeaf(index, "signature")] = true
				}
				if (!signature.Header.IsNull() && !signature.Header.IsUnknown()) || (!signature.HeaderJSON.IsNull() && !signature.HeaderJSON.IsUnknown()) {
					fields[agentSignatureLeaf(index, "header")] = true
				}
			}
		}
		if data.AgentCard.Capabilities != nil {
			fields[agentFieldCardCapabilities] = true
			knownBool(agentFieldCardCapStreaming, data.AgentCard.Capabilities.Streaming)
			knownBool(agentFieldCardCapPush, data.AgentCard.Capabilities.PushNotifications)
			knownBool(agentFieldCardCapHistory, data.AgentCard.Capabilities.StateTransitionHistory)
		}
		if data.AgentCard.Provider != nil {
			fields[agentFieldCardProvider] = true
			knownString(agentFieldCardProviderOrg, data.AgentCard.Provider.Organization)
			knownString(agentFieldCardProviderURL, data.AgentCard.Provider.URL)
		}
		if data.AgentCard.Skills != nil {
			fields[agentFieldCardSkills] = true
			for _, skill := range data.AgentCard.Skills {
				if skill.ID.IsNull() || skill.ID.IsUnknown() || skill.ID.ValueString() == "" {
					continue
				}
				id := skill.ID.ValueString()
				fields[agentSkillLeaf(id, "id")] = true
				if !skill.Name.IsNull() && !skill.Name.IsUnknown() {
					fields[agentSkillLeaf(id, "name")] = true
				}
				if !skill.Description.IsNull() && !skill.Description.IsUnknown() {
					fields[agentSkillLeaf(id, "description")] = true
				}
				if !skill.Tags.IsNull() && !skill.Tags.IsUnknown() {
					fields[agentSkillLeaf(id, "tags")] = true
				}
				if !skill.Examples.IsNull() && !skill.Examples.IsUnknown() {
					fields[agentSkillLeaf(id, "examples")] = true
				}
				if !skill.InputModes.IsNull() && !skill.InputModes.IsUnknown() {
					fields[agentSkillLeaf(id, "input_modes")] = true
				}
				if !skill.OutputModes.IsNull() && !skill.OutputModes.IsUnknown() {
					fields[agentSkillLeaf(id, "output_modes")] = true
				}
				if (!skill.Security.IsNull() && !skill.Security.IsUnknown()) || (!skill.SecurityJSON.IsNull() && !skill.SecurityJSON.IsUnknown()) {
					fields[agentSkillLeaf(id, "security")] = true
				}
			}
		}
	}
	if data.ObjectPermission != nil {
		fields[agentFieldPermission] = true
		knownList(agentFieldPermissionServers, data.ObjectPermission.MCPServers)
		knownList(agentFieldPermissionGroups, data.ObjectPermission.MCPAccessGroups)
		knownMap(agentFieldPermissionTools, data.ObjectPermission.MCPToolPermissions)
		knownList(agentFieldPermissionModels, data.ObjectPermission.Models)
		knownList(agentFieldPermissionAgents, data.ObjectPermission.Agents)
	}
	return fields
}

func agentImportedFieldsFromState(data AgentResourceModel) agentFieldSet {
	all := agentConfiguredFields(data)
	// Ownership is leaf-scoped. The distinct structural markers allow later API
	// additions to imported collections without transferring sibling ownership.
	for _, parent := range []string{agentFieldParams, agentFieldStaticHeaders, agentFieldCard, agentFieldCardCapabilities, agentFieldCardProvider, agentFieldCardSkills, agentFieldCardSignatures, agentFieldPermission} {
		delete(all, parent)
	}
	all[agentScopeParams] = true
	all[agentScopeStaticHeaders] = true
	all[agentScopePermission] = true
	if data.AgentCard != nil {
		all[agentScopeCardCapabilities] = true
		all[agentScopeCardProvider] = true
		all[agentScopeCardSkills] = true
		all[agentScopeCardSignatures] = true
	}
	return all
}

func validateAgentSkillModels(skills []AgentSkillModel) error {
	seen := make(map[string]struct{}, len(skills))
	for _, skill := range skills {
		if skill.ID.IsNull() || skill.ID.IsUnknown() {
			return fmt.Errorf("agent card skills contain an invalid skill identity")
		}
		id := skill.ID.ValueString()
		if strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) {
			return fmt.Errorf("agent card skills contain an invalid skill identity")
		}
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("agent card skills contain duplicate skill identities")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func validateAgentModelSkillIdentities(models ...AgentResourceModel) error {
	for _, model := range models {
		if model.AgentCard == nil {
			continue
		}
		if model.AgentCard.Skills != nil {
			if err := validateAgentSkillModels(model.AgentCard.Skills); err != nil {
				return err
			}
			for _, skill := range model.AgentCard.Skills {
				if !skill.Security.IsNull() && !skill.Security.IsUnknown() && !skill.SecurityJSON.IsNull() && !skill.SecurityJSON.IsUnknown() {
					return fmt.Errorf("agent card skill security conflicts with security_json")
				}
				if !skill.SecurityJSON.IsNull() && !skill.SecurityJSON.IsUnknown() {
					if _, err := decodeAgentSecurityJSON(skill.SecurityJSON.ValueString()); err != nil {
						return fmt.Errorf("agent card skill security_json is malformed")
					}
				}
			}
		}
		for _, signature := range model.AgentCard.Signatures {
			if !signature.Header.IsNull() && !signature.Header.IsUnknown() && !signature.HeaderJSON.IsNull() && !signature.HeaderJSON.IsUnknown() {
				return fmt.Errorf("agent card signature header conflicts with header_json")
			}
			if !signature.HeaderJSON.IsNull() && !signature.HeaderJSON.IsUnknown() {
				if _, err := decodeAgentNullOrObject(signature.HeaderJSON.ValueString()); err != nil {
					return fmt.Errorf("agent card signature header_json is malformed")
				}
			}
		}
	}
	return nil
}

func (r *AgentResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var state, plan, config AgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateAgentModelSkillIdentities(state, plan, config); err != nil {
		resp.Diagnostics.AddError("Invalid Agent Skill Identity", err.Error())
		return
	}
	if _, _, err := configuredAgentParams(config.LiteLLMParams, config.LiteLLMParamsJSON); err != nil {
		resp.Diagnostics.AddError("Agent Parameter Conflict", err.Error())
		return
	}
	bundle, diagnostics := readAgentOwnershipBundle(ctx, req.Private)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		resp.Private = req.Private
		resp.Plan.Raw = req.State.Raw
		return
	}
	imported := bundle.committed
	if !bundle.versioned {
		// Older state has no ownership provenance. Conservatively classify every
		// known optional value as API-owned until explicit HCL transfers ownership
		// through a verified apply.
		imported = agentImportedFieldsFromState(state)
		if resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentImportedFieldsPrivateKey, encodeAgentFieldSet(imported))...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipInitializedPrivateKey, []byte("true"))...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentCollectionsPrivateKey, encodeAgentCollectionProvenance(emptyAgentCollectionProvenance()))...)
		}
	}
	mergeAPIMapLeavesIntoPlan := func(target *types.Map, prior types.Map, prefix string) bool {
		if target.IsNull() || target.IsUnknown() || prior.IsNull() || prior.IsUnknown() {
			return false
		}
		original := *target
		values := map[string]attr.Value{}
		for key, value := range target.Elements() {
			values[key] = value
		}
		for key, value := range prior.Elements() {
			if imported[agentLeaf(prefix, key)] {
				if _, configured := values[key]; !configured {
					values[key] = value
				}
			}
		}
		*target = types.MapValueMust(types.StringType, values)
		return !original.Equal(*target)
	}
	paramsMerged := false
	if config.LiteLLMParams.IsNull() || config.LiteLLMParams.IsUnknown() {
		paramsMerged = mergeAPIMapLeavesIntoPlan(&plan.LiteLLMParams, state.LiteLLMParams, agentFieldParams)
	}
	headersMerged := false
	if config.StaticHeaders.IsNull() || config.StaticHeaders.IsUnknown() {
		headersMerged = mergeAPIMapLeavesIntoPlan(&plan.StaticHeaders, state.StaticHeaders, agentFieldStaticHeaders)
	}
	configured := agentConfiguredFields(config)
	pending := cloneAgentFieldSet(imported)
	for field := range configured {
		delete(pending, field)
		// Do not copy imported Optional-only values into Plan when HCL omits
		// them: Terraform rejects provider-planned values for non-computed
		// attributes. Update builds a separate wire model from prior state so
		// the remote value is preserved while Terraform safely prunes it from
		// public state.
	}
	if err := validateAgentUpdateClears(plan, state, config, imported); err != nil {
		resp.Diagnostics.AddError("Unsupported Agent Clear", err.Error())
		return
	}
	// Set only changed attributes. Setting the whole model here would rewrite
	// computed timestamps to unknown and manufacture an update-only plan.
	if paramsMerged {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(agentFieldParams), plan.LiteLLMParams)...)
	}
	if headersMerged {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(agentFieldStaticHeaders), plan.StaticHeaders)...)
	}
	if agentParamsUpdateTouched(plan, state, config, imported) && config.LiteLLMParamsJSON.IsNull() && !state.LiteLLMParamsJSON.IsNull() {
		// An imported/API-owned JSON projection changes as a consequence of an
		// explicitly planned legacy-key update. Mark only that computed bridge
		// unknown so authoritative exact-type read-back is plan-consistent.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(agentFieldParamsJSON), types.StringUnknown())...)
	}
	pendingChanged := !agentFieldSetsEqual(imported, pending)
	if resp.Private != nil {
		if pendingChanged {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipPendingPrivateKey, encodeAgentFieldSet(pending))...)
			if !bundle.versioned || bundle.migration {
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipMigrationPrivateKey, []byte("true"))...)
			} else {
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipMigrationPrivateKey, nil)...)
			}
		} else {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipPendingPrivateKey, nil)...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, agentOwnershipMigrationPrivateKey, nil)...)
		}
	}
	if pendingChanged {
		// Equal-value ownership transfers require Apply so provenance is consumed
		// only after authoritative read-back succeeds.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, pathRootID, types.StringUnknown())...)
	} else {
		// Optional+Computed map reconciliation can eliminate the only proposed
		// change after Terraform has already unknowned computed fields. Restore
		// them only when the resulting lifecycle model exactly equals prior state.
		candidate := cloneAgentResourceModel(plan)
		candidate.ID = state.ID
		candidate.CreatedAt, candidate.UpdatedAt = state.CreatedAt, state.UpdatedAt
		candidate.CreatedBy, candidate.UpdatedBy = state.CreatedBy, state.UpdatedBy
		if reflect.DeepEqual(candidate, state) {
			for _, item := range []struct {
				name  string
				value attr.Value
			}{{"id", state.ID}, {"created_at", state.CreatedAt}, {"updated_at", state.UpdatedAt}, {"created_by", state.CreatedBy}, {"updated_by", state.UpdatedBy}} {
				resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(item.name), item.value)...)
			}
		}
	}
}

// pathRootID is shared here to keep the lifecycle helper independent of any
// nested path construction.
var pathRootID = path.Root("id")

func agentModelsExactlyEqual(left, right AgentResourceModel) bool {
	return reflect.DeepEqual(left, right)
}

func copyAgentField(target *AgentResourceModel, source AgentResourceModel, field string) {
	switch field {
	case agentFieldParams:
		target.LiteLLMParams = source.LiteLLMParams
	case agentFieldParamsJSON:
		target.LiteLLMParamsJSON = source.LiteLLMParamsJSON
	case agentFieldTPM:
		target.TPMLimit = source.TPMLimit
	case agentFieldRPM:
		target.RPMLimit = source.RPMLimit
	case agentFieldSessionTPM:
		target.SessionTPMLimit = source.SessionTPMLimit
	case agentFieldSessionRPM:
		target.SessionRPMLimit = source.SessionRPMLimit
	case agentFieldStaticHeaders:
		target.StaticHeaders = source.StaticHeaders
	case agentFieldExtraHeaders:
		target.ExtraHeaders = source.ExtraHeaders
	case agentFieldCard:
		target.AgentCard = cloneAgentResourceModel(source).AgentCard
	case agentFieldCardDescription, agentFieldCardVersion, agentFieldCardProtocol, agentFieldCardInputModes, agentFieldCardOutputModes,
		agentFieldCardCapabilities, agentFieldCardCapStreaming, agentFieldCardCapPush, agentFieldCardCapHistory,
		agentFieldCardProvider, agentFieldCardProviderOrg, agentFieldCardProviderURL, agentFieldCardSkills, agentFieldCardTransport, agentFieldCardIcon,
		agentFieldCardDocumentation, agentFieldCardAuthenticated, agentFieldCardSignatures:
		if target.AgentCard == nil || source.AgentCard == nil {
			return
		}
		switch field {
		case agentFieldCardDescription:
			target.AgentCard.Description = source.AgentCard.Description
		case agentFieldCardVersion:
			target.AgentCard.Version = source.AgentCard.Version
		case agentFieldCardProtocol:
			target.AgentCard.ProtocolVersion = source.AgentCard.ProtocolVersion
		case agentFieldCardInputModes:
			target.AgentCard.DefaultInputModes = source.AgentCard.DefaultInputModes
		case agentFieldCardOutputModes:
			target.AgentCard.DefaultOutputModes = source.AgentCard.DefaultOutputModes
		case agentFieldCardCapabilities:
			target.AgentCard.Capabilities = cloneAgentResourceModel(source).AgentCard.Capabilities
		case agentFieldCardCapStreaming, agentFieldCardCapPush, agentFieldCardCapHistory:
			if source.AgentCard.Capabilities == nil {
				return
			}
			if target.AgentCard.Capabilities == nil {
				target.AgentCard.Capabilities = &AgentCapabilitiesModel{}
			}
			switch field {
			case agentFieldCardCapStreaming:
				target.AgentCard.Capabilities.Streaming = source.AgentCard.Capabilities.Streaming
			case agentFieldCardCapPush:
				target.AgentCard.Capabilities.PushNotifications = source.AgentCard.Capabilities.PushNotifications
			case agentFieldCardCapHistory:
				target.AgentCard.Capabilities.StateTransitionHistory = source.AgentCard.Capabilities.StateTransitionHistory
			}
		case agentFieldCardProvider:
			target.AgentCard.Provider = cloneAgentResourceModel(source).AgentCard.Provider
		case agentFieldCardProviderOrg, agentFieldCardProviderURL:
			if source.AgentCard.Provider == nil {
				return
			}
			if target.AgentCard.Provider == nil {
				target.AgentCard.Provider = &AgentProviderModel{}
			}
			if field == agentFieldCardProviderOrg {
				target.AgentCard.Provider.Organization = source.AgentCard.Provider.Organization
			} else {
				target.AgentCard.Provider.URL = source.AgentCard.Provider.URL
			}
		case agentFieldCardSkills:
			target.AgentCard.Skills = append([]AgentSkillModel(nil), source.AgentCard.Skills...)
		case agentFieldCardTransport:
			target.AgentCard.PreferredTransport = source.AgentCard.PreferredTransport
		case agentFieldCardIcon:
			target.AgentCard.IconURL = source.AgentCard.IconURL
		case agentFieldCardDocumentation:
			target.AgentCard.DocumentationURL = source.AgentCard.DocumentationURL
		case agentFieldCardAuthenticated:
			target.AgentCard.SupportsAuthenticatedExtendedCard = source.AgentCard.SupportsAuthenticatedExtendedCard
		case agentFieldCardSignatures:
			target.AgentCard.Signatures = append([]AgentCardSignatureModel(nil), source.AgentCard.Signatures...)
		}
	case agentFieldPermission:
		target.ObjectPermission = cloneAgentResourceModel(source).ObjectPermission
	case agentFieldPermissionServers, agentFieldPermissionGroups, agentFieldPermissionTools, agentFieldPermissionModels, agentFieldPermissionAgents:
		if source.ObjectPermission == nil {
			return
		}
		if target.ObjectPermission == nil {
			target.ObjectPermission = &AgentObjectPermissionModel{}
		}
		switch field {
		case agentFieldPermissionServers:
			target.ObjectPermission.MCPServers = source.ObjectPermission.MCPServers
		case agentFieldPermissionGroups:
			target.ObjectPermission.MCPAccessGroups = source.ObjectPermission.MCPAccessGroups
		case agentFieldPermissionTools:
			target.ObjectPermission.MCPToolPermissions = source.ObjectPermission.MCPToolPermissions
		case agentFieldPermissionModels:
			target.ObjectPermission.Models = source.ObjectPermission.Models
		case agentFieldPermissionAgents:
			target.ObjectPermission.Agents = source.ObjectPermission.Agents
		}
	}
}

func validateAgentCardSourceShape(card map[string]interface{}) error {
	malformed := func() error { return fmt.Errorf("agent read response contains a malformed agent card") }
	stringValue := func(object map[string]interface{}, field string, nullable bool) bool {
		value, present := object[field]
		return !present || (value == nil && nullable) || func() bool { _, ok := value.(string); return ok }()
	}
	boolValue := func(object map[string]interface{}, field string, nullable bool) bool {
		value, present := object[field]
		return !present || (value == nil && nullable) || func() bool { _, ok := value.(bool); return ok }()
	}
	stringList := func(value interface{}, nullable bool) bool {
		if value == nil {
			return nullable
		}
		items, ok := value.([]interface{})
		if !ok {
			return false
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	}
	for _, field := range []string{"protocolVersion", "name", "description", "url", "version"} {
		if !stringValue(card, field, false) {
			return malformed()
		}
	}
	for _, field := range []string{"preferredTransport", "iconUrl", "documentationUrl"} {
		if !stringValue(card, field, true) {
			return malformed()
		}
	}
	if !boolValue(card, "supportsAuthenticatedExtendedCard", true) {
		return malformed()
	}
	for _, field := range []string{"defaultInputModes", "defaultOutputModes"} {
		if value, present := card[field]; present && !stringList(value, false) {
			return malformed()
		}
	}
	if raw, present := card["capabilities"]; present {
		capabilities, ok := raw.(map[string]interface{})
		if !ok {
			return malformed()
		}
		for _, field := range []string{"streaming", "pushNotifications", "stateTransitionHistory"} {
			if !boolValue(capabilities, field, true) {
				return malformed()
			}
		}
		if rawExtensions, present := capabilities["extensions"]; present && rawExtensions != nil {
			extensions, ok := rawExtensions.([]interface{})
			if !ok {
				return malformed()
			}
			for _, rawExtension := range extensions {
				extension, ok := rawExtension.(map[string]interface{})
				if !ok {
					return malformed()
				}
				if !stringValue(extension, "uri", false) || !stringValue(extension, "description", true) || !boolValue(extension, "required", true) {
					return malformed()
				}
				if params, present := extension["params"]; present && params != nil {
					if _, ok := params.(map[string]interface{}); !ok {
						return malformed()
					}
				}
			}
		}
	}
	if raw, present := card["provider"]; present && raw != nil {
		provider, ok := raw.(map[string]interface{})
		if !ok {
			return malformed()
		}
		if !stringValue(provider, "organization", false) || !stringValue(provider, "url", false) {
			return malformed()
		}
	}
	for _, field := range []string{"additionalInterfaces", "supportedInterfaces"} {
		if raw, present := card[field]; present && raw != nil {
			interfaces, ok := raw.([]interface{})
			if !ok {
				return malformed()
			}
			for _, rawInterface := range interfaces {
				item, ok := rawInterface.(map[string]interface{})
				if !ok {
					return malformed()
				}
				if !stringValue(item, "url", false) || !stringValue(item, "transport", false) || !stringValue(item, "protocolBinding", false) || !stringValue(item, "protocolVersion", false) {
					return malformed()
				}
			}
		}
	}
	validateSecurity := func(raw interface{}) bool {
		if raw == nil {
			return true
		}
		_, err := readAgentSecurity(raw)
		return err == nil
	}
	if raw, present := card["security"]; present && !validateSecurity(raw) {
		return malformed()
	}
	if raw, present := card["securitySchemes"]; present && raw != nil {
		schemes, ok := raw.(map[string]interface{})
		if !ok {
			return malformed()
		}
		for _, rawScheme := range schemes {
			scheme, ok := rawScheme.(map[string]interface{})
			if !ok {
				return malformed()
			}
			if !stringValue(scheme, "description", true) {
				return malformed()
			}
			typeValue, ok := scheme["type"].(string)
			if !ok {
				return malformed()
			}
			switch typeValue {
			case "apiKey":
				location, locationOK := scheme["in_"].(string)
				if !locationOK {
					location, locationOK = scheme["in"].(string)
				}
				if !locationOK || (location != "query" && location != "header" && location != "cookie") || !stringValue(scheme, "name", false) || scheme["name"] == nil {
					return malformed()
				}
			case "http":
				if value, present := scheme["scheme"]; !present || value == nil || !stringValue(scheme, "scheme", false) || !stringValue(scheme, "bearerFormat", true) {
					return malformed()
				}
			case "oauth2":
				flows, ok := scheme["flows"].(map[string]interface{})
				if !ok {
					return malformed()
				}
				for _, flow := range []string{"authorizationCode", "clientCredentials", "implicit", "password"} {
					if value, present := flows[flow]; present && value != nil {
						if _, ok := value.(map[string]interface{}); !ok {
							return malformed()
						}
					}
				}
				if !stringValue(scheme, "oauth2MetadataUrl", true) {
					return malformed()
				}
			case "openIdConnect":
				if value, present := scheme["openIdConnectUrl"]; !present || value == nil || !stringValue(scheme, "openIdConnectUrl", false) {
					return malformed()
				}
			case "mutualTLS":
			default:
				return malformed()
			}
		}
	}
	if raw, present := card["signatures"]; present && raw != nil {
		signatures, ok := raw.([]interface{})
		if !ok {
			return malformed()
		}
		for _, rawSignature := range signatures {
			signature, ok := rawSignature.(map[string]interface{})
			if !ok {
				return malformed()
			}
			if !stringValue(signature, "protected", false) || !stringValue(signature, "signature", false) {
				return malformed()
			}
			if header, present := signature["header"]; present && header != nil {
				if _, ok := header.(map[string]interface{}); !ok {
					return malformed()
				}
			}
		}
	}
	if raw, present := card["skills"]; present {
		skills, ok := raw.([]interface{})
		if !ok {
			return malformed()
		}
		for _, rawSkill := range skills {
			skill, ok := rawSkill.(map[string]interface{})
			if !ok {
				return malformed()
			}
			for _, field := range []string{"id", "name", "description"} {
				if !stringValue(skill, field, false) {
					return malformed()
				}
			}
			if value, present := skill["tags"]; present && !stringList(value, false) {
				return malformed()
			}
			for _, field := range []string{"examples", "inputModes", "outputModes"} {
				if value, present := skill[field]; present && !stringList(value, true) {
					return malformed()
				}
			}
			if value, present := skill["security"]; present && !validateSecurity(value) {
				return malformed()
			}
		}
	}
	return nil
}

func validateAgentCardResponse(card map[string]interface{}, requiredIdentity bool) error {
	if err := validateAgentCardSourceShape(card); err != nil {
		return err
	}
	if requiredIdentity {
		name, nameOK := card["name"].(string)
		_, urlOK := card["url"].(string)
		if !nameOK || name == "" || !urlOK {
			return fmt.Errorf("agent read response contains a malformed agent card")
		}
	}
	stringFields := []string{"name", "description", "url", "version", "protocolVersion", "preferredTransport", "iconUrl", "documentationUrl"}
	for _, field := range stringFields {
		if value, present := card[field]; present && value != nil {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("agent read response contains a malformed agent card")
			}
		}
	}
	if value, present := card["supportsAuthenticatedExtendedCard"]; present && value != nil {
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("agent read response contains a malformed agent card")
		}
	}
	validateStringArray := func(value interface{}) bool {
		items, ok := value.([]interface{})
		if !ok {
			return false
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return false
			}
		}
		return true
	}
	for _, field := range []string{"defaultInputModes", "defaultOutputModes"} {
		if value, present := card[field]; present && value != nil && !validateStringArray(value) {
			return fmt.Errorf("agent read response contains a malformed agent card")
		}
	}
	if raw, present := card["capabilities"]; present && raw != nil {
		capabilities, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("agent read response contains a malformed agent card")
		}
		for _, field := range []string{"streaming", "pushNotifications", "stateTransitionHistory"} {
			if value, present := capabilities[field]; present && value != nil {
				if _, ok := value.(bool); !ok {
					return fmt.Errorf("agent read response contains a malformed agent card")
				}
			}
		}
	}
	if raw, present := card["provider"]; present && raw != nil {
		provider, ok := raw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("agent read response contains a malformed agent card")
		}
		for _, field := range []string{"organization", "url"} {
			if value, present := provider[field]; present && value != nil {
				if _, ok := value.(string); !ok {
					return fmt.Errorf("agent read response contains a malformed agent card")
				}
			}
		}
	}
	if raw, present := card["signatures"]; present && raw != nil {
		signatures, ok := raw.([]interface{})
		if !ok {
			return fmt.Errorf("agent read response contains a malformed agent card")
		}
		for _, rawSignature := range signatures {
			signature, ok := rawSignature.(map[string]interface{})
			if !ok {
				return fmt.Errorf("agent read response contains a malformed agent card")
			}
			for _, field := range []string{"protected", "signature"} {
				value, present := signature[field]
				if present && value != nil {
					if _, ok := value.(string); !ok {
						return fmt.Errorf("agent read response contains a malformed agent card")
					}
				}
			}
			if header, present := signature["header"]; present && header != nil {
				if _, ok := header.(map[string]interface{}); !ok {
					return fmt.Errorf("agent read response contains a malformed agent card")
				}
			}
		}
	}
	if raw, present := card["skills"]; present && raw != nil {
		skills, ok := raw.([]interface{})
		if !ok {
			return fmt.Errorf("agent read response contains a malformed agent card")
		}
		seenSkillIDs := make(map[string]struct{}, len(skills))
		for _, rawSkill := range skills {
			skill, ok := rawSkill.(map[string]interface{})
			if !ok {
				return fmt.Errorf("agent read response contains a malformed agent card")
			}
			skillID, idOK := skill["id"].(string)
			skillName, skillNameOK := skill["name"].(string)
			if requiredIdentity && (!idOK || strings.TrimSpace(skillID) == "" || skillID != strings.TrimSpace(skillID) || !skillNameOK || strings.TrimSpace(skillName) == "") {
				return fmt.Errorf("agent read response contains a malformed agent card")
			}
			if idOK {
				if strings.TrimSpace(skillID) == "" || skillID != strings.TrimSpace(skillID) {
					return fmt.Errorf("agent read response contains a malformed agent card")
				}
				if _, duplicate := seenSkillIDs[skillID]; duplicate {
					return fmt.Errorf("agent read response contains duplicate agent skill identities")
				}
				seenSkillIDs[skillID] = struct{}{}
			}
			for _, field := range []string{"id", "name", "description"} {
				if value, present := skill[field]; present && value != nil {
					if _, ok := value.(string); !ok {
						return fmt.Errorf("agent read response contains a malformed agent card")
					}
				}
			}
			for _, field := range []string{"tags", "examples", "inputModes", "outputModes"} {
				if value, present := skill[field]; present && value != nil && !validateStringArray(value) {
					return fmt.Errorf("agent read response contains a malformed agent card")
				}
			}
			if value, present := skill["security"]; present && value != nil {
				if _, err := readAgentSecurity(value); err != nil {
					return fmt.Errorf("agent read response contains a malformed agent card")
				}
			}
		}
	}
	return nil
}

func validReturnedAgentID(result map[string]interface{}) (string, bool) {
	raw, ok := result["agent_id"].(string)
	if !ok || raw == "" || raw != strings.TrimSpace(raw) {
		return "", false
	}
	return raw, true
}

func waitAgentFreshSample(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	select {
	case <-ctx.Done():
		timer.Stop()
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func fetchFreshAgentListObjects(ctx context.Context, client *Client) ([]map[string]interface{}, error) {
	var raw json.RawMessage
//line internal/provider/resource_agent_lifecycle.go:747
	if err := client.doFreshRequestWithResponse(ctx, "GET", "/v1/agents", nil, &raw); err != nil {
		return nil, err
	}
	items, err := decodeTopLevelList(raw, "/v1/agents")
	if err != nil {
		return nil, err
	}
	return decodeListObjects(items, "/v1/agents", "agent item")
}

// recoverCreatedAgent samples independent connections because a successful
// create can be accepted by one v1.98 worker before another worker can list it.
// Candidate identity is the union across all valid samples; ambiguity or one
// malformed/error sample fails closed.
func (r *AgentResource) recoverCreatedAgent(ctx context.Context, planned, config AgentResourceModel) (string, error) {
	if planned.AgentName.IsNull() || planned.AgentName.IsUnknown() || strings.TrimSpace(planned.AgentName.ValueString()) == "" || validateAgentModelSkillIdentities(planned, config) != nil {
		return "", fmt.Errorf("agent create recovery was inconclusive")
	}
	candidates := map[string]struct{}{}
	delay := 250 * time.Millisecond
	for attempt := 0; attempt < 8; attempt++ {
		items, err := fetchFreshAgentListObjects(ctx, r.client)
		if err != nil {
			return "", fmt.Errorf("agent create recovery was inconclusive")
		}
		for _, item := range items {
			name, ok := item["agent_name"].(string)
			id, idOK := validReturnedAgentID(item)
			if !ok || strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) || !idOK {
				return "", fmt.Errorf("agent create recovery was inconclusive")
			}
			if name == planned.AgentName.ValueString() {
				candidates[id] = struct{}{}
				if len(candidates) > 1 {
					return "", fmt.Errorf("agent create recovery was ambiguous")
				}
			}
		}
		if attempt < 7 {
			if err := waitAgentFreshSample(ctx, delay); err != nil {
				return "", fmt.Errorf("agent create recovery was inconclusive")
			}
			delay *= 2
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
		}
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("agent create recovery was ambiguous")
	}
	var id string
	for candidate := range candidates {
		id = candidate
	}
	candidatePlan := cloneAgentResourceModel(planned)
	candidatePlan.ID = types.StringValue(id)
	observed := emptyKnownAgentResourceModel()
	observed.ID = types.StringValue(id)
	if err := r.readAgentFreshWithOwnership(ctx, &observed, true, nil); err != nil {
		return "", fmt.Errorf("agent create recovery was inconclusive")
	}
	resolveAgentUnknowns(&observed)
	if validateAgentModelSkillIdentities(candidatePlan, config, observed) != nil || len(agentMutationMismatches(candidatePlan, AgentResourceModel{}, config, nil, observed)) != 0 || agentResourceHasUnknowns(observed) {
		return "", fmt.Errorf("agent create recovery was inconclusive")
	}
	return id, nil
}

func agentStringMapSemanticallyEqual(left, right types.Map) bool {
	if left.IsNull() || left.IsUnknown() || right.IsNull() || right.IsUnknown() {
		return left.Equal(right)
	}
	if len(left.Elements()) != len(right.Elements()) {
		return false
	}
	for key, leftRaw := range left.Elements() {
		rightRaw, ok := right.Elements()[key]
		if !ok {
			return false
		}
		leftValue, leftOK := leftRaw.(types.String)
		rightValue, rightOK := rightRaw.(types.String)
		if !leftOK || !rightOK || leftValue.IsNull() || leftValue.IsUnknown() || rightValue.IsNull() || rightValue.IsUnknown() {
			if !leftRaw.Equal(rightRaw) {
				return false
			}
			continue
		}
		if leftValue.ValueString() != rightValue.ValueString() && !jsonSemanticallyEqual(leftValue.ValueString(), rightValue.ValueString()) {
			return false
		}
	}
	return true
}

func agentStringListSetEqual(left, right types.List) bool {
	if left.IsNull() || left.IsUnknown() || right.IsNull() || right.IsUnknown() {
		return left.Equal(right)
	}
	l, r := listToStringSlice(left), listToStringSlice(right)
	slices.Sort(l)
	slices.Sort(r)
	return slices.Equal(l, r)
}

func validateAgentUpdateClears(plan, state, config AgentResourceModel, imported agentFieldSet) error {
	if err := validateAgentModelSkillIdentities(plan, state, config); err != nil {
		return err
	}
	configured := agentConfiguredFields(config)
	stateFields := agentConfiguredFields(state)
	knownNullMap := func(value types.Map) bool { return value.IsNull() && !value.IsUnknown() }
	knownNullString := func(value types.String) bool { return value.IsNull() && !value.IsUnknown() }
	knownEmptyList := func(value types.List) bool {
		return !value.IsNull() && !value.IsUnknown() && len(value.Elements()) == 0
	}
	managedStringRemoval := func(field string, prior, planned types.String) bool {
		return !prior.IsNull() && !prior.IsUnknown() && knownNullString(planned) && !configured[field] && !imported[field]
	}

	structuredEmptyClear := false
	if !config.LiteLLMParamsJSON.IsNull() && !config.LiteLLMParamsJSON.IsUnknown() {
		configuredObject, err := decodeAgentJSONObject(config.LiteLLMParamsJSON.ValueString())
		if err != nil {
			return err
		}
		priorJSONNonempty := false
		if !state.LiteLLMParamsJSON.IsNull() && !state.LiteLLMParamsJSON.IsUnknown() {
			priorObject, priorErr := decodeAgentJSONObject(state.LiteLLMParamsJSON.ValueString())
			if priorErr != nil {
				return priorErr
			}
			priorJSONNonempty = len(priorObject) > 0
		}
		priorNonempty := priorJSONNonempty || (!state.LiteLLMParams.IsNull() && !state.LiteLLMParams.IsUnknown() && len(state.LiteLLMParams.Elements()) > 0)
		structuredEmptyClear = len(configuredObject) == 0 && config.LiteLLMParams.IsNull() && priorNonempty
	}
	if structuredEmptyClear || (!state.LiteLLMParams.IsNull() && !state.LiteLLMParams.IsUnknown() && knownNullMap(plan.LiteLLMParams) && !agentFieldSetHasPrefix(imported, agentFieldParams+"[")) ||
		(!config.LiteLLMParams.IsNull() && !config.LiteLLMParams.IsUnknown() && len(config.LiteLLMParams.Elements()) == 0 && !plan.LiteLLMParams.IsNull() && !plan.LiteLLMParams.IsUnknown() && len(plan.LiteLLMParams.Elements()) == 0 && !state.LiteLLMParams.IsNull() && len(state.LiteLLMParams.Elements()) > 0) {
		return fmt.Errorf("LiteLLM v1.98 ignores an empty litellm_params object. Keep at least one parameter, or retain the existing map; complete map clearing is not API-safe.")
	}
	if state.AgentCard != nil && plan.AgentCard == nil {
		if agentFieldSetHasPrefix(imported, "agent_card.") {
			return fmt.Errorf("the complete agent_card cannot be removed while it contains API-owned leaves; configure or transfer every leaf first")
		}
		return fmt.Errorf("LiteLLM v1.98 PATCH cannot remove the complete agent_card block. Keep the block configured.")
	}
	if state.AgentCard == nil || plan.AgentCard == nil || config.AgentCard == nil {
		return nil
	}
	if managedStringRemoval(agentFieldCardVersion, state.AgentCard.Version, plan.AgentCard.Version) {
		return fmt.Errorf("LiteLLM v1.98 injects a default agent-card version, so version cannot be cleared safely.")
	}
	if managedStringRemoval(agentFieldCardProtocol, state.AgentCard.ProtocolVersion, plan.AgentCard.ProtocolVersion) {
		return fmt.Errorf("LiteLLM v1.98 injects a default agent-card protocol version, so protocol_version cannot be cleared safely.")
	}
	if stateFields[agentFieldCardInputModes] && knownEmptyList(plan.AgentCard.DefaultInputModes) && !imported[agentFieldCardInputModes] && !state.AgentCard.DefaultInputModes.Equal(plan.AgentCard.DefaultInputModes) {
		return fmt.Errorf("LiteLLM v1.98 replaces empty default_input_modes with its own default, so this collection cannot be cleared safely.")
	}
	if stateFields[agentFieldCardOutputModes] && knownEmptyList(plan.AgentCard.DefaultOutputModes) && !imported[agentFieldCardOutputModes] && !state.AgentCard.DefaultOutputModes.Equal(plan.AgentCard.DefaultOutputModes) {
		return fmt.Errorf("LiteLLM v1.98 replaces empty default_output_modes with its own default, so this collection cannot be cleared safely.")
	}
	if state.AgentCard.Capabilities != nil && plan.AgentCard.Capabilities == nil && (imported[agentFieldCardCapStreaming] || imported[agentFieldCardCapPush] || imported[agentFieldCardCapHistory]) {
		return fmt.Errorf("the complete capabilities block cannot be removed while it contains API-owned leaves")
	}
	if config.AgentCard.Skills != nil {
		configuredSkillIDs := make(map[string]struct{}, len(config.AgentCard.Skills))
		for _, skill := range config.AgentCard.Skills {
			configuredSkillIDs[skill.ID.ValueString()] = struct{}{}
		}
		for _, skill := range state.AgentCard.Skills {
			id := skill.ID.ValueString()
			if _, retained := configuredSkillIDs[id]; retained {
				continue
			}
			if agentFieldSetHasPrefix(imported, agentLeaf(agentFieldCardSkills, id)+".") {
				// Omission of an API-owned skill is preservation, not removal. The
				// raw-card overlay retains it. Once HCL has transferred all present
				// leaves, the markers disappear and a later omission removes it.
				continue
			}
		}
	}
	if state.AgentCard.Provider != nil && plan.AgentCard.Provider == nil {
		if imported[agentFieldCardProviderOrg] || imported[agentFieldCardProviderURL] {
			return fmt.Errorf("the complete provider block cannot be removed while it contains API-owned leaves")
		}
		if stateFields[agentFieldCardProviderOrg] || stateFields[agentFieldCardProviderURL] {
			return fmt.Errorf("LiteLLM v1.98 replaces an empty agent-card provider with proxy-owned provider metadata, so the complete provider block cannot be cleared safely.")
		}
	}
	if state.AgentCard.Provider != nil && plan.AgentCard.Provider != nil {
		priorAny := stateFields[agentFieldCardProviderOrg] || stateFields[agentFieldCardProviderURL]
		plannedEmpty := plan.AgentCard.Provider.Organization.IsNull() && plan.AgentCard.Provider.URL.IsNull()
		changed := !state.AgentCard.Provider.Organization.Equal(plan.AgentCard.Provider.Organization) || !state.AgentCard.Provider.URL.Equal(plan.AgentCard.Provider.URL)
		if priorAny && plannedEmpty && changed {
			if imported[agentFieldCardProviderOrg] || imported[agentFieldCardProviderURL] {
				return fmt.Errorf("the complete provider block cannot be cleared while it contains API-owned leaves")
			}
			return fmt.Errorf("LiteLLM v1.98 replaces an empty agent-card provider with proxy-owned provider metadata, so the complete provider block cannot be cleared safely.")
		}
	}
	return nil
}

func agentCardUpdateTouched(plan, state, config AgentResourceModel, imported agentFieldSet) bool {
	if plan.AgentCard == nil || state.AgentCard == nil {
		return plan.AgentCard != state.AgentCard
	}
	configured, prior := agentConfiguredFields(config), agentConfiguredFields(state)
	changed := func(field string, equal bool, plannedKnownNull bool) bool {
		if configured[field] {
			return !equal
		}
		return prior[field] && !imported[field] && plannedKnownNull
	}
	if changed(agentFieldCardName, plan.AgentCard.Name.Equal(state.AgentCard.Name), plan.AgentCard.Name.IsNull()) ||
		changed(agentFieldCardURL, plan.AgentCard.URL.Equal(state.AgentCard.URL), plan.AgentCard.URL.IsNull()) ||
		changed(agentFieldCardDescription, plan.AgentCard.Description.Equal(state.AgentCard.Description), plan.AgentCard.Description.IsNull()) ||
		changed(agentFieldCardVersion, plan.AgentCard.Version.Equal(state.AgentCard.Version), plan.AgentCard.Version.IsNull()) ||
		changed(agentFieldCardProtocol, plan.AgentCard.ProtocolVersion.Equal(state.AgentCard.ProtocolVersion), plan.AgentCard.ProtocolVersion.IsNull()) ||
		changed(agentFieldCardInputModes, plan.AgentCard.DefaultInputModes.Equal(state.AgentCard.DefaultInputModes), plan.AgentCard.DefaultInputModes.IsNull()) ||
		changed(agentFieldCardOutputModes, plan.AgentCard.DefaultOutputModes.Equal(state.AgentCard.DefaultOutputModes), plan.AgentCard.DefaultOutputModes.IsNull()) ||
		changed(agentFieldCardTransport, plan.AgentCard.PreferredTransport.Equal(state.AgentCard.PreferredTransport), plan.AgentCard.PreferredTransport.IsNull()) ||
		changed(agentFieldCardIcon, plan.AgentCard.IconURL.Equal(state.AgentCard.IconURL), plan.AgentCard.IconURL.IsNull()) ||
		changed(agentFieldCardDocumentation, plan.AgentCard.DocumentationURL.Equal(state.AgentCard.DocumentationURL), plan.AgentCard.DocumentationURL.IsNull()) ||
		changed(agentFieldCardAuthenticated, plan.AgentCard.SupportsAuthenticatedExtendedCard.Equal(state.AgentCard.SupportsAuthenticatedExtendedCard), plan.AgentCard.SupportsAuthenticatedExtendedCard.IsNull()) {
		return true
	}
	if !reflect.DeepEqual(plan.AgentCard.Signatures, state.AgentCard.Signatures) && (config.AgentCard != nil && config.AgentCard.Signatures != nil || prior[agentFieldCardSignatures]) {
		return true
	}
	if !agentSkillsEqual(plan.AgentCard.Skills, state.AgentCard.Skills) && (config.AgentCard != nil && config.AgentCard.Skills != nil || prior[agentFieldCardSkills]) {
		return true
	}
	boolValue := func(card *AgentCardModel, field string) types.Bool {
		if card == nil || card.Capabilities == nil {
			return types.BoolNull()
		}
		switch field {
		case agentFieldCardCapStreaming:
			return card.Capabilities.Streaming
		case agentFieldCardCapPush:
			return card.Capabilities.PushNotifications
		default:
			return card.Capabilities.StateTransitionHistory
		}
	}
	for _, field := range []string{agentFieldCardCapStreaming, agentFieldCardCapPush, agentFieldCardCapHistory} {
		pv, sv := boolValue(plan.AgentCard, field), boolValue(state.AgentCard, field)
		if changed(field, pv.Equal(sv), pv.IsNull()) {
			return true
		}
	}
	stringValue := func(card *AgentCardModel, field string) types.String {
		if card == nil || card.Provider == nil {
			return types.StringNull()
		}
		if field == agentFieldCardProviderOrg {
			return card.Provider.Organization
		}
		return card.Provider.URL
	}
	for _, field := range []string{agentFieldCardProviderOrg, agentFieldCardProviderURL} {
		pv, sv := stringValue(plan.AgentCard, field), stringValue(state.AgentCard, field)
		if changed(field, pv.Equal(sv), pv.IsNull()) {
			return true
		}
	}
	return false
}

func agentCardPreservesImportedLeaves(fresh AgentResourceModel, imported agentFieldSet) bool {
	_ = imported
	// A fresh, validated omission is authoritative absence, not a preservation
	// failure. Structural scope can re-adopt a leaf if it later reappears.
	return fresh.AgentCard != nil && validateAgentModelSkillIdentities(fresh) == nil && !agentResourceHasUnknowns(fresh)
}

func (r *AgentResource) sampleFreshAgentCard(ctx context.Context, state AgentResourceModel, imported agentFieldSet, maxAttempts int) (AgentResourceModel, error) {
	if maxAttempts < 2 {
		maxAttempts = 2
	}
	delay := 250 * time.Millisecond
	var previous AgentResourceModel
	matched := false
	for attempt := 0; attempt < maxAttempts; attempt++ {
		candidate := emptyKnownAgentResourceModel()
		candidate.ID = state.ID
		if err := r.readAgentFreshWithOwnership(ctx, &candidate, true, nil); err == nil &&
			candidate.AgentCard != nil && candidate.AgentName.Equal(state.AgentName) &&
			validateAgentModelSkillIdentities(candidate) == nil && agentCardPreservesImportedLeaves(candidate, imported) {
			if matched && reflect.DeepEqual(previous.AgentCard, candidate.AgentCard) {
				return candidate, nil
			}
			previous = candidate
			matched = true
		} else {
			matched = false
		}
		if attempt < maxAttempts-1 {
			if err := waitAgentFreshSample(ctx, delay); err != nil {
				return AgentResourceModel{}, err
			}
			delay *= 2
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
		}
	}
	return AgentResourceModel{}, fmt.Errorf("agent card preflight did not converge")
}

func overlayAgentCardWire(fresh, plan, state, config AgentResourceModel, imported agentFieldSet) *AgentCardModel {
	if fresh.AgentCard == nil || plan.AgentCard == nil {
		return plan.AgentCard
	}
	wire := cloneAgentResourceModel(fresh).AgentCard
	configured, prior := agentConfiguredFields(config), agentConfiguredFields(state)
	usePlan := func(field string, plannedNull bool) bool {
		return configured[field] || (prior[field] && !imported[field] && plannedNull)
	}
	copyString := func(field string, target *types.String, value types.String) {
		if usePlan(field, value.IsNull()) {
			*target = value
		}
	}
	copyList := func(field string, target *types.List, value types.List) {
		if usePlan(field, value.IsNull()) {
			*target = value
		}
	}
	copyBool := func(field string, target *types.Bool, value types.Bool) {
		if usePlan(field, value.IsNull()) {
			*target = value
		}
	}
	copyString(agentFieldCardName, &wire.Name, plan.AgentCard.Name)
	copyString(agentFieldCardURL, &wire.URL, plan.AgentCard.URL)
	copyString(agentFieldCardDescription, &wire.Description, plan.AgentCard.Description)
	copyString(agentFieldCardVersion, &wire.Version, plan.AgentCard.Version)
	copyString(agentFieldCardProtocol, &wire.ProtocolVersion, plan.AgentCard.ProtocolVersion)
	copyList(agentFieldCardInputModes, &wire.DefaultInputModes, plan.AgentCard.DefaultInputModes)
	copyList(agentFieldCardOutputModes, &wire.DefaultOutputModes, plan.AgentCard.DefaultOutputModes)
	copyString(agentFieldCardTransport, &wire.PreferredTransport, plan.AgentCard.PreferredTransport)
	copyString(agentFieldCardIcon, &wire.IconURL, plan.AgentCard.IconURL)
	copyString(agentFieldCardDocumentation, &wire.DocumentationURL, plan.AgentCard.DocumentationURL)
	copyBool(agentFieldCardAuthenticated, &wire.SupportsAuthenticatedExtendedCard, plan.AgentCard.SupportsAuthenticatedExtendedCard)
	if configured[agentFieldCardSignatures] || (prior[agentFieldCardSignatures] && !imported[agentFieldCardSignatures] && plan.AgentCard.Signatures == nil) {
		wire.Signatures = append([]AgentCardSignatureModel(nil), plan.AgentCard.Signatures...)
	}
	if wire.Capabilities == nil {
		wire.Capabilities = &AgentCapabilitiesModel{Streaming: types.BoolNull(), PushNotifications: types.BoolNull(), StateTransitionHistory: types.BoolNull()}
	}
	var pc AgentCapabilitiesModel
	if plan.AgentCard.Capabilities != nil {
		pc = *plan.AgentCard.Capabilities
	}
	copyBool(agentFieldCardCapStreaming, &wire.Capabilities.Streaming, pc.Streaming)
	copyBool(agentFieldCardCapPush, &wire.Capabilities.PushNotifications, pc.PushNotifications)
	copyBool(agentFieldCardCapHistory, &wire.Capabilities.StateTransitionHistory, pc.StateTransitionHistory)
	if wire.Provider == nil {
		wire.Provider = &AgentProviderModel{Organization: types.StringNull(), URL: types.StringNull()}
	}
	var pp AgentProviderModel
	if plan.AgentCard.Provider != nil {
		pp = *plan.AgentCard.Provider
	}
	copyString(agentFieldCardProviderOrg, &wire.Provider.Organization, pp.Organization)
	copyString(agentFieldCardProviderURL, &wire.Provider.URL, pp.URL)
	if config.AgentCard != nil && config.AgentCard.Skills != nil {
		freshByID := map[string]AgentSkillModel{}
		for _, skill := range wire.Skills {
			freshByID[skill.ID.ValueString()] = skill
		}
		priorSkillIDs := map[string]bool{}
		if state.AgentCard != nil {
			for _, skill := range state.AgentCard.Skills {
				if !skill.ID.IsNull() && !skill.ID.IsUnknown() {
					priorSkillIDs[skill.ID.ValueString()] = true
				}
			}
		}
		merged := make([]AgentSkillModel, 0, len(wire.Skills))
		seen := map[string]bool{}
		for _, desired := range plan.AgentCard.Skills {
			id := desired.ID.ValueString()
			current, ok := freshByID[id]
			if !ok {
				current = desired
			}
			for _, item := range []struct {
				field  string
				target *types.String
				value  types.String
			}{{"name", &current.Name, desired.Name}, {"description", &current.Description, desired.Description}} {
				if configured[agentSkillLeaf(id, item.field)] || (!imported[agentSkillLeaf(id, item.field)] && item.value.IsNull()) {
					*item.target = item.value
				}
			}
			for _, item := range []struct {
				field  string
				target *types.List
				value  types.List
			}{{"tags", &current.Tags, desired.Tags}, {"examples", &current.Examples, desired.Examples}, {"input_modes", &current.InputModes, desired.InputModes}, {"output_modes", &current.OutputModes, desired.OutputModes}} {
				if configured[agentSkillLeaf(id, item.field)] || (!imported[agentSkillLeaf(id, item.field)] && item.value.IsNull()) {
					*item.target = item.value
				}
			}
			securityField := agentSkillLeaf(id, "security")
			if configured[securityField] || (!imported[securityField] && desired.Security.IsNull() && desired.SecurityJSON.IsNull()) {
				current.Security = desired.Security
				current.SecurityJSON = desired.SecurityJSON
			}
			merged = append(merged, current)
			seen[id] = true
		}
		for _, remote := range wire.Skills {
			id := remote.ID.ValueString()
			if seen[id] {
				continue
			}
			apiOwnedLeaves := agentFieldSetHasPrefix(imported, agentLeaf(agentFieldCardSkills, id)+".")
			apiAddedAfterImport := imported[agentScopeCardSkills] && !priorSkillIDs[id]
			if apiOwnedLeaves || apiAddedAfterImport {
				merged = append(merged, remote)
			}
		}
		wire.Skills = merged
	}
	return wire
}

func (r *AgentResource) buildAgentUpdateRequest(plan, state, config *AgentResourceModel, imported agentFieldSet, includeCard ...bool) (map[string]interface{}, error) {
	req := map[string]interface{}{"agent_name": plan.AgentName.ValueString()}
	configured := agentConfiguredFields(*config)
	stateFields := agentConfiguredFields(*state)
	cleared := func(field string) bool { return stateFields[field] && !configured[field] && !imported[field] }

	// Imported/API-owned card values cannot be placed into an Optional-only
	// Terraform plan when HCL omits them. Rehydrate a wire-only clone from prior
	// state so PATCH preserves those values without claiming them in state.
	wirePlan := cloneAgentResourceModel(*plan)
	for field := range imported {
		if !configured[field] && !strings.HasPrefix(field, "agent_card.") {
			copyAgentField(&wirePlan, *state, field)
		}
	}
	// An explicitly configured structured object is a complete ownership
	// transfer for that surface. Build it only with explicit legacy siblings,
	// not API-owned legacy projections retained in the Terraform plan.
	if configured[agentFieldParamsJSON] {
		wirePlan.LiteLLMParams = config.LiteLLMParams
		wirePlan.LiteLLMParamsJSON = config.LiteLLMParamsJSON
	} else if imported[agentFieldParamsJSON] && !wirePlan.LiteLLMParamsJSON.IsNull() && !wirePlan.LiteLLMParamsJSON.IsUnknown() {
		// The legacy import projection intentionally keeps its historic
		// map(string) rendering, but the private JSON marker proves the JSON
		// document is the wire authority. Overlay only explicitly configured
		// legacy strings so numeric/bool/null/container values never round-trip
		// as ambiguous strings.
		remoteObject, decodeErr := decodeAgentJSONObject(wirePlan.LiteLLMParamsJSON.ValueString())
		if decodeErr != nil {
			return nil, decodeErr
		}
		if !config.LiteLLMParams.IsNull() && !config.LiteLLMParams.IsUnknown() {
			for key, raw := range config.LiteLLMParams.Elements() {
				value, ok := raw.(types.String)
				if !ok || value.IsNull() || value.IsUnknown() {
					return nil, fmt.Errorf("invalid configured legacy agent parameter")
				}
				remoteObject[key] = value.ValueString()
			}
		}
		encoded, encodeErr := canonicalAgentJSON(remoteObject)
		if encodeErr != nil {
			return nil, encodeErr
		}
		wirePlan.LiteLLMParams = types.MapNull(types.StringType)
		wirePlan.LiteLLMParamsJSON = types.StringValue(encoded)
	}
	full, err := r.buildAgentRequest(&wirePlan)
	if err != nil {
		return nil, err
	}
	planCollections, diagnostics := convertAgentRequestCollections(context.Background(), *plan)
	if diagnostics.HasError() {
		return nil, fmt.Errorf("agent update collection conversion failed")
	}
	wireCollections, diagnostics := convertAgentRequestCollections(context.Background(), wirePlan)
	if diagnostics.HasError() {
		return nil, fmt.Errorf("agent update collection conversion failed")
	}
	sendCard := config.AgentCard != nil
	if len(includeCard) > 0 {
		sendCard = includeCard[0]
	}
	if sendCard {
		if card, ok := full["agent_card_params"]; ok {
			req["agent_card_params"] = card
		}
	}
	if configured[agentFieldParams] || configured[agentFieldParamsJSON] {
		paramsSource, jsonSource := plan.LiteLLMParams, plan.LiteLLMParamsJSON
		if configured[agentFieldParamsJSON] {
			paramsSource, jsonSource = config.LiteLLMParams, config.LiteLLMParamsJSON
		} else if imported[agentFieldParamsJSON] {
			paramsSource, jsonSource = wirePlan.LiteLLMParams, wirePlan.LiteLLMParamsJSON
		}
		params, _, err := configuredAgentParams(paramsSource, jsonSource)
		if err != nil {
			return nil, err
		}
		if err := validateAgentCorePair(params); err != nil {
			return nil, err
		}
		req["litellm_params"] = params
	}
	for _, field := range []struct {
		name  string
		value types.Int64
	}{
		{agentFieldTPM, plan.TPMLimit}, {agentFieldRPM, plan.RPMLimit},
		{agentFieldSessionTPM, plan.SessionTPMLimit}, {agentFieldSessionRPM, plan.SessionRPMLimit},
	} {
		if configured[field.name] {
			req[field.name] = field.value.ValueInt64()
		} else if cleared(field.name) {
			req[field.name] = nil
		}
	}
	if configured[agentFieldStaticHeaders] {
		headers := make(map[string]interface{}, len(wireCollections.staticHeaders))
		for key, value := range wireCollections.staticHeaders {
			headers[key] = value
		}
		req["static_headers"] = headers
	} else if cleared(agentFieldStaticHeaders) {
		req["static_headers"] = map[string]interface{}{}
	}
	if configured[agentFieldExtraHeaders] {
		req["extra_headers"] = planCollections.extraHeaders
	} else if cleared(agentFieldExtraHeaders) {
		req["extra_headers"] = []string{}
	}

	permission := map[string]interface{}{}
	addList := func(field, wire string, value types.List) {
		if configured[field] {
			permission[wire] = planCollections.permissionLists[wire]
		} else if cleared(field) {
			permission[wire] = []string{}
		}
	}
	var plannedPermission AgentObjectPermissionModel
	if plan.ObjectPermission != nil {
		plannedPermission = *plan.ObjectPermission
	}
	addList(agentFieldPermissionServers, "mcp_servers", plannedPermission.MCPServers)
	addList(agentFieldPermissionGroups, "mcp_access_groups", plannedPermission.MCPAccessGroups)
	addList(agentFieldPermissionModels, "models", plannedPermission.Models)
	addList(agentFieldPermissionAgents, "agents", plannedPermission.Agents)
	if configured[agentFieldPermissionTools] {
		decoded, err := decodeConfiguredAgentMCPToolPermissions(plannedPermission.MCPToolPermissions)
		if err != nil {
			return nil, err
		}
		permission["mcp_tool_permissions"] = decoded
	} else if cleared(agentFieldPermissionTools) {
		permission["mcp_tool_permissions"] = map[string][]string{}
	}
	if len(permission) > 0 {
		req["object_permission"] = permission
	}
	return req, nil
}

func (r *AgentResource) confirmAgentMutation(ctx context.Context, planned, prior, config AgentResourceModel, imported agentFieldSet, maxAttempts int) (AgentResourceModel, error) {
	return r.confirmAgentMutationWithPreservation(ctx, planned, prior, config, imported, nil, maxAttempts)
}

func (r *AgentResource) confirmAgentMutationWithPreservation(ctx context.Context, planned, prior, config AgentResourceModel, imported agentFieldSet, preservation *agentPatchPreservation, maxAttempts int) (AgentResourceModel, error) {
	if err := validateAgentModelSkillIdentities(planned, prior, config); err != nil {
		return AgentResourceModel{}, err
	}
	if maxAttempts < 2 {
		maxAttempts = 2
	}
	delay := 250 * time.Millisecond
	maxDelay := 2 * time.Second
	var lastConfirmed AgentResourceModel
	consecutive := 0
	for attempt := 0; attempt < maxAttempts; attempt++ {
		observed := emptyKnownAgentResourceModel()
		observed.ID = planned.ID
		var raw map[string]interface{}
		err := r.readAgentWithOwnershipTransportCapture(ctx, &observed, true, nil, true, &raw)
		if err == nil {
			resolveAgentUnknowns(&observed)
			if validateAgentModelSkillIdentities(observed) == nil && preservation.matches(raw) && len(agentMutationMismatches(planned, prior, config, imported, observed)) == 0 && !agentResourceHasUnknowns(observed) {
				consecutive++
				lastConfirmed = reconcileConfirmedAgentState(planned, observed, config, prior, imported)
				if preservation != nil {
					preservation.confirmedRaw = cloneAgentWireObject(raw)
				}
				if consecutive >= 2 {
					return lastConfirmed, nil
				}
			} else {
				consecutive = 0
			}
		} else {
			// Both 404 propagation lag and bounded transient non-404 read errors
			// are inconclusive. Retry without publishing candidate state.
			consecutive = 0
		}
		if attempt == maxAttempts-1 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return AgentResourceModel{}, ctx.Err()
		case <-timer.C:
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
	return AgentResourceModel{}, fmt.Errorf("agent mutation did not converge")
}

func agentMapMutationMatches(planned, prior, config, observed types.Map, imported agentFieldSet, prefix string) bool {
	plannedValues, priorValues, configValues, observedValues := map[string]string{}, map[string]string{}, map[string]string{}, map[string]string{}
	decode := func(value types.Map, target *map[string]string) bool {
		return value.IsNull() || value.IsUnknown() || !value.ElementsAs(context.Background(), target, false).HasError()
	}
	if !decode(planned, &plannedValues) || !decode(prior, &priorValues) || !decode(config, &configValues) || !decode(observed, &observedValues) {
		return false
	}
	for key := range configValues {
		pv, pok := plannedValues[key]
		ov, ook := observedValues[key]
		if !pok || !ook || (pv != ov && !jsonSemanticallyEqual(pv, ov)) {
			return false
		}
	}
	for key := range priorValues {
		if _, configured := configValues[key]; !configured && !imported[agentLeaf(prefix, key)] {
			if _, present := observedValues[key]; present {
				return false
			}
		}
	}
	return true
}

func agentMutationMismatches(planned, prior, config AgentResourceModel, imported agentFieldSet, observed AgentResourceModel) []string {
	mismatches := []string{}
	configured := agentConfiguredFields(config)
	priorFields := agentConfiguredFields(prior)
	check := func(field string, equal bool, clearEqual bool) {
		ownedClear := priorFields[field] && !configured[field] && !imported[field]
		if (configured[field] && !equal) || (ownedClear && !clearEqual) {
			mismatches = append(mismatches, field)
		}
	}
	check("agent_name", planned.AgentName.Equal(observed.AgentName), false)
	if !agentMapMutationMatches(planned.LiteLLMParams, prior.LiteLLMParams, config.LiteLLMParams, observed.LiteLLMParams, imported, agentFieldParams) {
		mismatches = append(mismatches, agentFieldParams)
	}
	if configured[agentFieldParamsJSON] {
		expected, _, err := configuredAgentParams(config.LiteLLMParams, config.LiteLLMParamsJSON)
		var actual map[string]interface{}
		if err == nil && !observed.LiteLLMParamsJSON.IsNull() && !observed.LiteLLMParamsJSON.IsUnknown() {
			actual, err = decodeAgentJSONObject(observed.LiteLLMParamsJSON.ValueString())
		}
		matches := err == nil
		if matches {
			for key, value := range expected {
				observedValue, present := actual[key]
				if !present || !exactJSONValuesEqual(value, observedValue) {
					matches = false
					break
				}
			}
		}
		if !matches {
			mismatches = append(mismatches, agentFieldParamsJSON)
		}
	}
	check(agentFieldTPM, planned.TPMLimit.Equal(observed.TPMLimit), observed.TPMLimit.IsNull())
	check(agentFieldRPM, planned.RPMLimit.Equal(observed.RPMLimit), observed.RPMLimit.IsNull())
	check(agentFieldSessionTPM, planned.SessionTPMLimit.Equal(observed.SessionTPMLimit), observed.SessionTPMLimit.IsNull())
	check(agentFieldSessionRPM, planned.SessionRPMLimit.Equal(observed.SessionRPMLimit), observed.SessionRPMLimit.IsNull())
	staticClear := observed.StaticHeaders.IsNull() || len(observed.StaticHeaders.Elements()) == 0
	plannedStaticClear := planned.StaticHeaders.IsNull() || (!planned.StaticHeaders.IsUnknown() && len(planned.StaticHeaders.Elements()) == 0)
	if !(plannedStaticClear && staticClear) && !agentMapMutationMatches(planned.StaticHeaders, prior.StaticHeaders, config.StaticHeaders, observed.StaticHeaders, imported, agentFieldStaticHeaders) {
		mismatches = append(mismatches, agentFieldStaticHeaders)
	}
	extraClear := observed.ExtraHeaders.IsNull() || len(observed.ExtraHeaders.Elements()) == 0
	plannedExtraClear := planned.ExtraHeaders.IsNull() || (!planned.ExtraHeaders.IsUnknown() && len(planned.ExtraHeaders.Elements()) == 0)
	check(agentFieldExtraHeaders, planned.ExtraHeaders.Equal(observed.ExtraHeaders) || (plannedExtraClear && extraClear), extraClear)
	compareAgentCard(&mismatches, planned, prior, config, imported, observed)
	compareAgentPermissions(&mismatches, planned, prior, config, imported, observed)
	return mismatches
}

func compareAgentCard(mismatches *[]string, planned, prior, config AgentResourceModel, imported agentFieldSet, observed AgentResourceModel) {
	if config.AgentCard == nil {
		return
	}
	if observed.AgentCard == nil {
		*mismatches = append(*mismatches, agentFieldCard)
		return
	}
	if !planned.AgentCard.Name.Equal(observed.AgentCard.Name) || !planned.AgentCard.URL.Equal(observed.AgentCard.URL) {
		*mismatches = append(*mismatches, agentFieldCard)
	}
	configured := agentConfiguredFields(config)
	priorFields := agentConfiguredFields(prior)
	check := func(field string, equal, absent bool) {
		if (configured[field] && !equal) || (priorFields[field] && !configured[field] && !imported[field] && !absent) {
			*mismatches = append(*mismatches, field)
		}
	}
	check(agentFieldCardDescription, planned.AgentCard.Description.Equal(observed.AgentCard.Description), observed.AgentCard.Description.IsNull())
	check(agentFieldCardVersion, planned.AgentCard.Version.Equal(observed.AgentCard.Version), observed.AgentCard.Version.IsNull())
	check(agentFieldCardProtocol, planned.AgentCard.ProtocolVersion.Equal(observed.AgentCard.ProtocolVersion), observed.AgentCard.ProtocolVersion.IsNull())
	check(agentFieldCardInputModes, planned.AgentCard.DefaultInputModes.Equal(observed.AgentCard.DefaultInputModes), observed.AgentCard.DefaultInputModes.IsNull() || len(observed.AgentCard.DefaultInputModes.Elements()) == 0)
	check(agentFieldCardOutputModes, planned.AgentCard.DefaultOutputModes.Equal(observed.AgentCard.DefaultOutputModes), observed.AgentCard.DefaultOutputModes.IsNull() || len(observed.AgentCard.DefaultOutputModes.Elements()) == 0)
	check(agentFieldCardTransport, planned.AgentCard.PreferredTransport.Equal(observed.AgentCard.PreferredTransport), observed.AgentCard.PreferredTransport.IsNull())
	check(agentFieldCardIcon, planned.AgentCard.IconURL.Equal(observed.AgentCard.IconURL), observed.AgentCard.IconURL.IsNull())
	check(agentFieldCardDocumentation, planned.AgentCard.DocumentationURL.Equal(observed.AgentCard.DocumentationURL), observed.AgentCard.DocumentationURL.IsNull())
	authEqual := planned.AgentCard.SupportsAuthenticatedExtendedCard.Equal(observed.AgentCard.SupportsAuthenticatedExtendedCard) ||
		(!planned.AgentCard.SupportsAuthenticatedExtendedCard.IsNull() && !planned.AgentCard.SupportsAuthenticatedExtendedCard.IsUnknown() && !planned.AgentCard.SupportsAuthenticatedExtendedCard.ValueBool() && observed.AgentCard.SupportsAuthenticatedExtendedCard.IsNull())
	check(agentFieldCardAuthenticated, authEqual, observed.AgentCard.SupportsAuthenticatedExtendedCard.IsNull() || !observed.AgentCard.SupportsAuthenticatedExtendedCard.ValueBool())
	if !agentSignaturesMutationMatch(planned.AgentCard.Signatures, prior.AgentCard, config.AgentCard.Signatures, observed.AgentCard.Signatures, imported) {
		*mismatches = append(*mismatches, agentFieldCardSignatures)
	}
	plannedCapabilities, observedCapabilities := planned.AgentCard.Capabilities, observed.AgentCard.Capabilities
	capabilityCheck := func(field string, plannedValue, observedValue types.Bool) {
		configuredValue := configured[field]
		clearedValue := priorFields[field] && !configuredValue && !imported[field]
		observedClear := observedValue.IsNull() || (!observedValue.IsUnknown() && !observedValue.ValueBool())
		configuredEqual := plannedValue.Equal(observedValue) ||
			(!plannedValue.IsNull() && !plannedValue.IsUnknown() && !plannedValue.ValueBool() && observedValue.IsNull())
		if (configuredValue && !configuredEqual) || (clearedValue && !observedClear) {
			*mismatches = append(*mismatches, field)
		}
	}
	if plannedCapabilities == nil {
		plannedCapabilities = &AgentCapabilitiesModel{Streaming: types.BoolNull(), PushNotifications: types.BoolNull(), StateTransitionHistory: types.BoolNull()}
	}
	if observedCapabilities == nil {
		observedCapabilities = &AgentCapabilitiesModel{Streaming: types.BoolNull(), PushNotifications: types.BoolNull(), StateTransitionHistory: types.BoolNull()}
	}
	capabilityCheck(agentFieldCardCapStreaming, plannedCapabilities.Streaming, observedCapabilities.Streaming)
	capabilityCheck(agentFieldCardCapPush, plannedCapabilities.PushNotifications, observedCapabilities.PushNotifications)
	capabilityCheck(agentFieldCardCapHistory, plannedCapabilities.StateTransitionHistory, observedCapabilities.StateTransitionHistory)

	plannedProvider, observedProvider := planned.AgentCard.Provider, observed.AgentCard.Provider
	providerCheck := func(field string, plannedValue, observedValue types.String) {
		if (configured[field] && !plannedValue.Equal(observedValue)) || (priorFields[field] && !configured[field] && !imported[field] && !observedValue.IsNull()) {
			*mismatches = append(*mismatches, field)
		}
	}
	if plannedProvider == nil {
		plannedProvider = &AgentProviderModel{Organization: types.StringNull(), URL: types.StringNull()}
	}
	if observedProvider == nil {
		observedProvider = &AgentProviderModel{Organization: types.StringNull(), URL: types.StringNull()}
	}
	providerCheck(agentFieldCardProviderOrg, plannedProvider.Organization, observedProvider.Organization)
	providerCheck(agentFieldCardProviderURL, plannedProvider.URL, observedProvider.URL)
	if (config.AgentCard.Skills != nil || (prior.AgentCard != nil && prior.AgentCard.Skills != nil)) && !agentSkillsMutationMatch(planned.AgentCard.Skills, prior.AgentCard, config.AgentCard.Skills, observed.AgentCard.Skills, imported) {
		*mismatches = append(*mismatches, agentFieldCardSkills)
	}
}

func agentSignaturesMutationMatch(planned []AgentCardSignatureModel, priorCard *AgentCardModel, config, observed []AgentCardSignatureModel, imported agentFieldSet) bool {
	prior := []AgentCardSignatureModel(nil)
	if priorCard != nil {
		prior = priorCard.Signatures
	}
	if config != nil {
		for index, configured := range config {
			if index >= len(planned) || index >= len(observed) {
				return false
			}
			if !planned[index].Protected.Equal(observed[index].Protected) || !planned[index].Signature.Equal(observed[index].Signature) {
				return false
			}
			if (!configured.Header.IsNull() && !configured.Header.IsUnknown()) || (!configured.HeaderJSON.IsNull() && !configured.HeaderJSON.IsUnknown()) {
				if !agentOptionalJSONEqual(planned[index].Header, observed[index].Header) || !agentOptionalJSONEqual(planned[index].HeaderJSON, observed[index].HeaderJSON) {
					return false
				}
			}
		}
	}
	// With API-owned hidden signatures, cardinality alone cannot identify a
	// removed public entry. The raw preservation confirmation verifies the exact
	// complete-list PATCH. Non-imported resources have no hidden tail and retain
	// strict cardinality confirmation here.
	if !imported[agentScopeCardSignatures] && len(observed) != len(planned) {
		return false
	}
	_ = prior
	return true
}

func agentSkillsMutationMatch(planned []AgentSkillModel, priorCard *AgentCardModel, config, observed []AgentSkillModel, imported agentFieldSet) bool {
	byID := func(skills []AgentSkillModel) map[string]AgentSkillModel {
		result := map[string]AgentSkillModel{}
		for _, skill := range skills {
			result[skill.ID.ValueString()] = skill
		}
		return result
	}
	p, c, o := byID(planned), byID(config), byID(observed)
	priorByID := map[string]AgentSkillModel{}
	if priorCard != nil {
		priorByID = byID(priorCard.Skills)
	}
	configured := agentConfiguredFields(AgentResourceModel{AgentCard: &AgentCardModel{Skills: config}})
	for id := range c {
		pv, pok := p[id]
		ov, ook := o[id]
		if !pok || !ook {
			return false
		}
		if configured[agentSkillLeaf(id, "name")] && !pv.Name.Equal(ov.Name) {
			return false
		}
		if configured[agentSkillLeaf(id, "description")] && !pv.Description.Equal(ov.Description) {
			return false
		}
		if configured[agentSkillLeaf(id, "tags")] && !agentStringListSetEqual(pv.Tags, ov.Tags) {
			return false
		}
		if configured[agentSkillLeaf(id, "examples")] && !pv.Examples.Equal(ov.Examples) {
			return false
		}
		if configured[agentSkillLeaf(id, "input_modes")] && !pv.InputModes.Equal(ov.InputModes) {
			return false
		}
		if configured[agentSkillLeaf(id, "output_modes")] && !pv.OutputModes.Equal(ov.OutputModes) {
			return false
		}
		if configured[agentSkillLeaf(id, "security")] && (!pv.Security.Equal(ov.Security) || !pv.SecurityJSON.Equal(ov.SecurityJSON)) {
			return false
		}
		if priorSkill, hadPrior := priorByID[id]; hadPrior && !priorSkill.Security.IsNull() && !configured[agentSkillLeaf(id, "security")] && !imported[agentSkillLeaf(id, "security")] && !ov.Security.IsNull() {
			return false
		}
	}
	if priorCard != nil {
		for _, skill := range priorCard.Skills {
			id := skill.ID.ValueString()
			if _, stillConfigured := c[id]; stillConfigured {
				continue
			}
			if agentFieldSetHasPrefix(imported, agentLeaf(agentFieldCardSkills, id)+".") {
				continue
			}
			if _, remains := o[id]; remains {
				return false
			}
		}
	}
	return true
}

func compareAgentPermissions(mismatches *[]string, planned, prior, config AgentResourceModel, imported agentFieldSet, observed AgentResourceModel) {
	configured := agentConfiguredFields(config)
	priorFields := agentConfiguredFields(prior)
	var p, o AgentObjectPermissionModel
	if planned.ObjectPermission != nil {
		p = *planned.ObjectPermission
	}
	if observed.ObjectPermission != nil {
		o = *observed.ObjectPermission
	}
	checkList := func(field string, plannedValue, observedValue types.List) {
		clear := observedValue.IsNull() || len(observedValue.Elements()) == 0
		if (configured[field] && !agentStringListSetEqual(plannedValue, observedValue)) || (priorFields[field] && !configured[field] && !imported[field] && !clear) {
			*mismatches = append(*mismatches, field)
		}
	}
	checkList(agentFieldPermissionServers, p.MCPServers, o.MCPServers)
	checkList(agentFieldPermissionGroups, p.MCPAccessGroups, o.MCPAccessGroups)
	checkList(agentFieldPermissionModels, p.Models, o.Models)
	checkList(agentFieldPermissionAgents, p.Agents, o.Agents)
	clearTools := o.MCPToolPermissions.IsNull() || len(o.MCPToolPermissions.Elements()) == 0
	plannedToolClear := p.MCPToolPermissions.IsNull() || (!p.MCPToolPermissions.IsUnknown() && len(p.MCPToolPermissions.Elements()) == 0)
	if (configured[agentFieldPermissionTools] && !(plannedToolClear && clearTools) && !agentMCPToolPermissionsConfirmed(planned, observed)) ||
		(priorFields[agentFieldPermissionTools] && !configured[agentFieldPermissionTools] && !imported[agentFieldPermissionTools] && !clearTools) {
		*mismatches = append(*mismatches, agentFieldPermissionTools)
	}
}

func agentOptionalJSONEqual(left, right types.String) bool {
	if left.Equal(right) {
		return true
	}
	return !left.IsNull() && !left.IsUnknown() && !right.IsNull() && !right.IsUnknown() && jsonSemanticallyEqual(left.ValueString(), right.ValueString())
}

func agentSignaturesEqual(left, right []AgentCardSignatureModel) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !left[index].Protected.Equal(right[index].Protected) || !left[index].Signature.Equal(right[index].Signature) {
			return false
		}
		if !agentOptionalJSONEqual(left[index].Header, right[index].Header) || !agentOptionalJSONEqual(left[index].HeaderJSON, right[index].HeaderJSON) {
			return false
		}
	}
	return true
}

func agentSkillsEqual(left, right []AgentSkillModel) bool {
	if len(left) != len(right) {
		return false
	}
	rightByID := make(map[string]AgentSkillModel, len(right))
	for _, skill := range right {
		if skill.ID.IsNull() || skill.ID.IsUnknown() || strings.TrimSpace(skill.ID.ValueString()) == "" || skill.ID.ValueString() != strings.TrimSpace(skill.ID.ValueString()) {
			return false
		}
		if _, duplicate := rightByID[skill.ID.ValueString()]; duplicate {
			return false
		}
		rightByID[skill.ID.ValueString()] = skill
	}
	seenLeft := map[string]struct{}{}
	for _, l := range left {
		if l.ID.IsNull() || l.ID.IsUnknown() || strings.TrimSpace(l.ID.ValueString()) == "" || l.ID.ValueString() != strings.TrimSpace(l.ID.ValueString()) {
			return false
		}
		if _, duplicate := seenLeft[l.ID.ValueString()]; duplicate {
			return false
		}
		seenLeft[l.ID.ValueString()] = struct{}{}
		r, ok := rightByID[l.ID.ValueString()]
		if !ok || !l.Name.Equal(r.Name) || !l.Description.Equal(r.Description) ||
			!agentStringListSetEqual(l.Tags, r.Tags) || !l.Examples.Equal(r.Examples) || !l.InputModes.Equal(r.InputModes) || !l.OutputModes.Equal(r.OutputModes) || !l.Security.Equal(r.Security) || !agentOptionalJSONEqual(l.SecurityJSON, r.SecurityJSON) {
			return false
		}
	}
	return true
}

func reconcileConfirmedAgentState(planned, observed, config, prior AgentResourceModel, imported agentFieldSet) AgentResourceModel {
	result := cloneAgentResourceModel(planned)
	result.ID, result.AgentName = observed.ID, observed.AgentName
	result.CreatedAt, result.UpdatedAt = observed.CreatedAt, observed.UpdatedAt
	result.CreatedBy, result.UpdatedBy = observed.CreatedBy, observed.UpdatedBy
	// API-owned Optional+Computed map keys stay adopted; configured keys remain
	// exactly planned so semantically equal JSON keeps the user's spelling.
	mergeMap := func(target *types.Map, remote types.Map, prefix string) {
		values := map[string]attr.Value{}
		if !target.IsNull() && !target.IsUnknown() {
			for k, v := range target.Elements() {
				values[k] = v
			}
		}
		if !remote.IsNull() && !remote.IsUnknown() {
			for k, v := range remote.Elements() {
				if imported[agentLeaf(prefix, k)] {
					values[k] = v
				}
			}
		}
		if len(values) == 0 {
			return
		}
		*target = types.MapValueMust(types.StringType, values)
	}
	if config.LiteLLMParams.IsNull() || config.LiteLLMParams.IsUnknown() {
		mergeMap(&result.LiteLLMParams, observed.LiteLLMParams, agentFieldParams)
	} else {
		result.LiteLLMParams = config.LiteLLMParams
	}
	if !config.LiteLLMParamsJSON.IsNull() && !config.LiteLLMParamsJSON.IsUnknown() {
		result.LiteLLMParams = config.LiteLLMParams
	}
	if imported[agentFieldParamsJSON] && config.LiteLLMParamsJSON.IsNull() {
		result.LiteLLMParamsJSON = observed.LiteLLMParamsJSON
	}
	if config.StaticHeaders.IsNull() || config.StaticHeaders.IsUnknown() {
		mergeMap(&result.StaticHeaders, observed.StaticHeaders, agentFieldStaticHeaders)
	} else {
		result.StaticHeaders = config.StaticHeaders
	}
	// Apply must publish exactly the planned ListNestedBlock cardinality. Remote
	// API-owned siblings are confirmed against the canonical raw response and
	// retained only in provider-private provenance for future wire overlays.
	_ = observed
	_ = prior
	_ = imported
	_ = config
	resolveAgentUnknowns(&result)
	return result
}

func emptyKnownAgentResourceModel() AgentResourceModel {
	return AgentResourceModel{
		ID:                types.StringNull(),
		AgentName:         types.StringNull(),
		AgentCard:         nil,
		LiteLLMParams:     types.MapNull(types.StringType),
		LiteLLMParamsJSON: types.StringNull(),
		ObjectPermission:  nil,
		TPMLimit:          types.Int64Null(),
		RPMLimit:          types.Int64Null(),
		SessionTPMLimit:   types.Int64Null(),
		SessionRPMLimit:   types.Int64Null(),
		StaticHeaders:     types.MapNull(types.StringType),
		ExtraHeaders:      types.ListNull(types.StringType),
		CreatedAt:         types.StringNull(),
		UpdatedAt:         types.StringNull(),
		CreatedBy:         types.StringNull(),
		UpdatedBy:         types.StringNull(),
	}
}

func resolveAgentUnknowns(data *AgentResourceModel) {
	if data.ID.IsUnknown() {
		data.ID = types.StringNull()
	}
	if data.AgentName.IsUnknown() {
		data.AgentName = types.StringNull()
	}
	if data.LiteLLMParams.IsUnknown() {
		data.LiteLLMParams = types.MapNull(types.StringType)
	}
	if data.LiteLLMParamsJSON.IsUnknown() {
		data.LiteLLMParamsJSON = types.StringNull()
	}
	if data.TPMLimit.IsUnknown() {
		data.TPMLimit = types.Int64Null()
	}
	if data.RPMLimit.IsUnknown() {
		data.RPMLimit = types.Int64Null()
	}
	if data.SessionTPMLimit.IsUnknown() {
		data.SessionTPMLimit = types.Int64Null()
	}
	if data.SessionRPMLimit.IsUnknown() {
		data.SessionRPMLimit = types.Int64Null()
	}
	if data.StaticHeaders.IsUnknown() {
		data.StaticHeaders = types.MapNull(types.StringType)
	}
	if data.ExtraHeaders.IsUnknown() {
		data.ExtraHeaders = types.ListNull(types.StringType)
	}
	if data.CreatedAt.IsUnknown() {
		data.CreatedAt = types.StringNull()
	}
	if data.UpdatedAt.IsUnknown() {
		data.UpdatedAt = types.StringNull()
	}
	if data.CreatedBy.IsUnknown() {
		data.CreatedBy = types.StringNull()
	}
	if data.UpdatedBy.IsUnknown() {
		data.UpdatedBy = types.StringNull()
	}
	if data.AgentCard != nil {
		card := data.AgentCard
		if card.Name.IsUnknown() {
			card.Name = types.StringNull()
		}
		if card.Description.IsUnknown() {
			card.Description = types.StringNull()
		}
		if card.URL.IsUnknown() {
			card.URL = types.StringNull()
		}
		if card.Version.IsUnknown() {
			card.Version = types.StringNull()
		}
		if card.ProtocolVersion.IsUnknown() {
			card.ProtocolVersion = types.StringNull()
		}
		if card.DefaultInputModes.IsUnknown() {
			card.DefaultInputModes = types.ListNull(types.StringType)
		}
		if card.DefaultOutputModes.IsUnknown() {
			card.DefaultOutputModes = types.ListNull(types.StringType)
		}
		if card.PreferredTransport.IsUnknown() {
			card.PreferredTransport = types.StringNull()
		}
		if card.IconURL.IsUnknown() {
			card.IconURL = types.StringNull()
		}
		if card.DocumentationURL.IsUnknown() {
			card.DocumentationURL = types.StringNull()
		}
		if card.SupportsAuthenticatedExtendedCard.IsUnknown() {
			card.SupportsAuthenticatedExtendedCard = types.BoolNull()
		}
		if card.Capabilities != nil {
			if card.Capabilities.Streaming.IsUnknown() {
				card.Capabilities.Streaming = types.BoolNull()
			}
			if card.Capabilities.PushNotifications.IsUnknown() {
				card.Capabilities.PushNotifications = types.BoolNull()
			}
			if card.Capabilities.StateTransitionHistory.IsUnknown() {
				card.Capabilities.StateTransitionHistory = types.BoolNull()
			}
		}
		if card.Provider != nil {
			if card.Provider.Organization.IsUnknown() {
				card.Provider.Organization = types.StringNull()
			}
			if card.Provider.URL.IsUnknown() {
				card.Provider.URL = types.StringNull()
			}
		}
		for index := range card.Signatures {
			signature := &card.Signatures[index]
			if signature.Protected.IsUnknown() {
				signature.Protected = types.StringNull()
			}
			if signature.Signature.IsUnknown() {
				signature.Signature = types.StringNull()
			}
			if signature.Header.IsUnknown() {
				signature.Header = types.StringNull()
			}
			if signature.HeaderJSON.IsUnknown() {
				signature.HeaderJSON = types.StringNull()
			}
		}
		for index := range card.Skills {
			skill := &card.Skills[index]
			if skill.ID.IsUnknown() {
				skill.ID = types.StringNull()
			}
			if skill.Name.IsUnknown() {
				skill.Name = types.StringNull()
			}
			if skill.Description.IsUnknown() {
				skill.Description = types.StringNull()
			}
			if skill.Tags.IsUnknown() {
				skill.Tags = types.ListNull(types.StringType)
			}
			if skill.Examples.IsUnknown() {
				skill.Examples = types.ListNull(types.StringType)
			}
			if skill.InputModes.IsUnknown() {
				skill.InputModes = types.ListNull(types.StringType)
			}
			if skill.OutputModes.IsUnknown() {
				skill.OutputModes = types.ListNull(types.StringType)
			}
			if skill.Security.IsUnknown() {
				skill.Security = types.ListNull(types.MapType{ElemType: types.ListType{ElemType: types.StringType}})
			}
			if skill.SecurityJSON.IsUnknown() {
				skill.SecurityJSON = types.StringNull()
			}
		}
	}
	if data.ObjectPermission != nil {
		permission := data.ObjectPermission
		if permission.MCPServers.IsUnknown() {
			permission.MCPServers = types.ListNull(types.StringType)
		}
		if permission.MCPAccessGroups.IsUnknown() {
			permission.MCPAccessGroups = types.ListNull(types.StringType)
		}
		if permission.MCPToolPermissions.IsUnknown() {
			permission.MCPToolPermissions = types.MapNull(types.StringType)
		}
		if permission.Models.IsUnknown() {
			permission.Models = types.ListNull(types.StringType)
		}
		if permission.Agents.IsUnknown() {
			permission.Agents = types.ListNull(types.StringType)
		}
	}
}

func agentResourceHasUnknowns(data AgentResourceModel) bool {
	if data.ID.IsUnknown() || data.AgentName.IsUnknown() || data.LiteLLMParams.IsUnknown() || data.LiteLLMParamsJSON.IsUnknown() || data.TPMLimit.IsUnknown() || data.RPMLimit.IsUnknown() ||
		data.SessionTPMLimit.IsUnknown() || data.SessionRPMLimit.IsUnknown() || data.StaticHeaders.IsUnknown() || data.ExtraHeaders.IsUnknown() ||
		data.CreatedAt.IsUnknown() || data.UpdatedAt.IsUnknown() || data.CreatedBy.IsUnknown() || data.UpdatedBy.IsUnknown() {
		return true
	}
	if data.AgentCard != nil {
		card := data.AgentCard
		if card.Name.IsUnknown() || card.Description.IsUnknown() || card.URL.IsUnknown() || card.Version.IsUnknown() || card.ProtocolVersion.IsUnknown() ||
			card.DefaultInputModes.IsUnknown() || card.DefaultOutputModes.IsUnknown() || card.PreferredTransport.IsUnknown() || card.IconURL.IsUnknown() ||
			card.DocumentationURL.IsUnknown() || card.SupportsAuthenticatedExtendedCard.IsUnknown() {
			return true
		}
		if card.Capabilities != nil && (card.Capabilities.Streaming.IsUnknown() || card.Capabilities.PushNotifications.IsUnknown() || card.Capabilities.StateTransitionHistory.IsUnknown()) {
			return true
		}
		if card.Provider != nil && (card.Provider.Organization.IsUnknown() || card.Provider.URL.IsUnknown()) {
			return true
		}
		for _, signature := range card.Signatures {
			if signature.Protected.IsUnknown() || signature.Signature.IsUnknown() || signature.Header.IsUnknown() || signature.HeaderJSON.IsUnknown() {
				return true
			}
		}
		for _, skill := range card.Skills {
			if skill.ID.IsUnknown() || skill.Name.IsUnknown() || skill.Description.IsUnknown() || skill.Tags.IsUnknown() || skill.Examples.IsUnknown() || skill.InputModes.IsUnknown() || skill.OutputModes.IsUnknown() || skill.Security.IsUnknown() || skill.SecurityJSON.IsUnknown() {
				return true
			}
		}
	}
	if data.ObjectPermission != nil {
		permission := data.ObjectPermission
		if permission.MCPServers.IsUnknown() || permission.MCPAccessGroups.IsUnknown() || permission.MCPToolPermissions.IsUnknown() || permission.Models.IsUnknown() || permission.Agents.IsUnknown() {
			return true
		}
	}
	return false
}
