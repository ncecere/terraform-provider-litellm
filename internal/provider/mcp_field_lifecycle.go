package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var mcpCredentialStringKeysV198 = map[string]bool{
	"auth_value": true, "client_id": true, "client_secret": true,
	"aws_access_key_id": true, "aws_secret_access_key": true, "aws_session_token": true,
	"aws_region_name": true, "aws_service_name": true, "aws_role_name": true, "aws_session_name": true,
	"audience": true, "token_exchange_endpoint": true, "subject_token_type": true,
	"id_jag_resource_token_endpoint": true, "id_jag_resource": true, "upstream_resource": true,
	"client_private_key": true, "client_private_key_id": true, "client_assertion_signing_alg": true,
	"token_endpoint_auth_method": true, "token_exchange_profile": true,
}

func validateMCPCredentialStringMapV198(credentials map[string]string) error {
	for name, value := range credentials {
		if !mcpCredentialStringKeysV198[name] {
			return fmt.Errorf("credentials contain a key that LiteLLM v1.98 cannot represent through this schema")
		}
		if name == "token_endpoint_auth_method" && value != "client_secret_basic" && value != "client_secret_post" {
			return fmt.Errorf("credentials contain an unsupported token endpoint authentication method")
		}
	}
	return nil
}

func mcpFieldDesiredValue(ctx context.Context, data MCPServerResourceModel, fieldPath string) (interface{}, error) {
	stringValue := func(value types.String) (interface{}, error) {
		if value.IsNull() || value.IsUnknown() {
			return nil, fmt.Errorf("unknown or null MCP string field")
		}
		return value.ValueString(), nil
	}
	boolValue := func(value types.Bool) (interface{}, error) {
		if value.IsNull() || value.IsUnknown() {
			return nil, fmt.Errorf("unknown or null MCP boolean field")
		}
		return value.ValueBool(), nil
	}
	switch fieldPath {
	case mcpFieldAliasPath:
		return stringValue(data.Alias)
	case mcpFieldDescriptionPath:
		return stringValue(data.Description)
	case mcpFieldCommandPath:
		return stringValue(data.Command)
	case mcpFieldAuthorizationURLPath:
		return stringValue(data.AuthorizationURL)
	case mcpFieldTokenURLPath:
		return stringValue(data.TokenURL)
	case mcpFieldRegistrationURLPath:
		return stringValue(data.RegistrationURL)
	case mcpFieldAllowAllKeysPath:
		return boolValue(data.AllowAllKeys)
	case mcpFieldAccessGroupsPath:
		return mcpFieldStringList(ctx, data.MCPAccessGroups)
	case mcpFieldArgsPath:
		return mcpFieldStringList(ctx, data.Args)
	case mcpFieldAllowedToolsPath:
		return mcpFieldStringList(ctx, data.AllowedTools)
	case mcpFieldExtraHeadersPath:
		return mcpFieldStringList(ctx, data.ExtraHeaders)
	case mcpFieldEnvPath:
		return mcpFieldStringMap(ctx, data.Env)
	case mcpFieldStaticHeadersPath:
		return mcpFieldStringMap(ctx, data.StaticHeaders)
	case mcpFieldCredentialsPath:
		credentials, err := mcpFieldStringMap(ctx, data.Credentials)
		if err != nil {
			return nil, err
		}
		if err := validateMCPCredentialStringMapV198(credentials); err != nil {
			return nil, err
		}
		return credentials, nil
	default:
		return nil, fmt.Errorf("unknown MCP field path")
	}
}

func mcpFieldWireName(fieldPath string) string { return fieldPath[1:] }

var mcpCredentialLiftedColumnNames = []string{
	"audience",
	"subject_token_type",
	"token_exchange_endpoint",
	"token_exchange_profile",
}

func mcpFieldRemovalSentinel(fieldPath string) interface{} {
	switch fieldPath {
	case mcpFieldAliasPath, mcpFieldDescriptionPath, mcpFieldCommandPath,
		mcpFieldAuthorizationURLPath, mcpFieldTokenURLPath, mcpFieldRegistrationURLPath,
		mcpFieldCredentialsPath:
		return nil
	case mcpFieldAccessGroupsPath, mcpFieldArgsPath, mcpFieldAllowedToolsPath, mcpFieldExtraHeadersPath:
		return []string{}
	case mcpFieldEnvPath, mcpFieldStaticHeadersPath:
		return map[string]string{}
	case mcpFieldAllowAllKeysPath:
		return false
	default:
		return nil
	}
}

func normalizeMCPWireValue(value interface{}) interface{} {
	raw, err := json.Marshal(value)
	if err != nil {
		return value
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var normalized interface{}
	if decoder.Decode(&normalized) != nil {
		return value
	}
	return normalized
}

func mcpWireValuesEqual(left, right interface{}) bool {
	return mcpInfoJSONValuesEqual(normalizeMCPWireValue(left), normalizeMCPWireValue(right))
}

func mcpAliasCreateIntentCannotConverge(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && (value.ValueString() == "" || strings.Contains(value.ValueString(), " "))
}

func mcpAliasUpdateIntentCannotConverge(value types.String) bool {
	// Pinned v1.98 preserves an explicit empty alias on a partial Update when
	// server_name is omitted. Spaces are always normalized and cannot converge;
	// completeMCPUpdateDelta separately rejects empty alias plus server_name.
	return !value.IsNull() && !value.IsUnknown() && strings.Contains(value.ValueString(), " ")
}

func (r *MCPServerResource) buildMCPServerCreateRequest(ctx context.Context, plan, config *MCPServerResourceModel, resolvedMCPInfo map[string]interface{}, mcpInfoPresent bool) (map[string]interface{}, error) {
	if mcpAliasCreateIntentCannotConverge(config.Alias) {
		return nil, fmt.Errorf("configured alias must be non-empty and must not require LiteLLM normalization")
	}
	request, err := r.buildMCPServerRequest(ctx, plan, resolvedMCPInfo, mcpInfoPresent)
	if err != nil {
		return nil, err
	}
	presence := mcpFieldConfigPresence(*config)
	for _, fieldPath := range mcpFieldPaths {
		if presence[fieldPath] != 1 {
			continue
		}
		value, err := mcpFieldDesiredValue(ctx, *config, fieldPath)
		if err != nil {
			return nil, fmt.Errorf("configured MCP collection is invalid")
		}
		request[mcpFieldWireName(fieldPath)] = value
	}
	return request, nil
}

func verifyMCPFieldCreateReadback(ctx context.Context, config MCPServerResourceModel, observed map[string]interface{}, ownership mcpFieldOwnership) error {
	for fieldPath := range ownership.Owned {
		if fieldPath == mcpFieldCredentialsPath {
			// Credential values are redacted. A successful write plus an
			// identity/schema-valid direct read is the only v1.98 evidence.
			continue
		}
		want, err := mcpFieldDesiredValue(ctx, config, fieldPath)
		if err != nil {
			return err
		}
		got, present := observed[mcpFieldWireName(fieldPath)]
		if !present || !mcpWireValuesEqual(want, got) {
			return fmt.Errorf("owned MCP field did not converge")
		}
	}
	return nil
}

func mcpAuthCredentialClass(authType string) string {
	if authType == "true_passthrough" || authType == "oauth_delegate" {
		return "client_forwarded"
	}
	return authType
}

func mcpKnownRawString(result map[string]interface{}, name string) (string, bool) {
	value, present := result[name]
	if !present || value == nil {
		return "", false
	}
	resultValue, ok := value.(string)
	return resultValue, ok
}

func validateMCPImplicitClearSafety(config, state MCPServerResourceModel, planned mcpFieldOwnership, hydration map[string]interface{}, delta map[string]interface{}, urlChanged, authClassChanged bool) error {
	if !urlChanged && !authClassChanged {
		return nil
	}
	presence := mcpFieldConfigPresence(config)
	for _, item := range []struct {
		fieldPath string
		prior     types.String
	}{
		{fieldPath: mcpFieldAuthorizationURLPath, prior: state.AuthorizationURL},
		{fieldPath: mcpFieldTokenURLPath, prior: state.TokenURL},
		{fieldPath: mcpFieldRegistrationURLPath, prior: state.RegistrationURL},
	} {
		name := mcpFieldWireName(item.fieldPath)
		raw, present := hydration[name]
		if (!present || raw == nil) && !item.prior.IsNull() && !item.prior.IsUnknown() {
			raw, present = item.prior.ValueString(), true
		}
		if !present || raw == nil {
			continue
		}
		desired, supplied := delta[name]
		explicitRemoval := planned.Removals[item.fieldPath] && supplied && desired == nil
		explicitChange := planned.Owned[item.fieldPath] && presence[item.fieldPath] == 1 && supplied && !mcpWireValuesEqual(desired, raw)
		if !explicitRemoval && !explicitChange {
			return fmt.Errorf("an unowned or unchanged OAuth endpoint would be cleared implicitly")
		}
	}
	for _, name := range []string{
		"issuer", "oauth2_flow", "dcr_bridge", "token_exchange_endpoint",
		"audience", "subject_token_type", "token_exchange_profile",
	} {
		if raw, present := hydration[name]; present && raw != nil {
			desired, supplied := delta[name]
			if supplied && !mcpWireValuesEqual(desired, raw) {
				continue
			}
			return fmt.Errorf("a hidden authentication-flow value would be cleared implicitly")
		}
	}
	if authClassChanged {
		// v1.98 clears credentials on a credential-class change unless a complete
		// credential intent is supplied in that same PUT. Management responses
		// redact values, so they are always non-authoritative.
		if _, supplied := delta["credentials"]; !supplied || presence[mcpFieldCredentialsPath] == 2 {
			return fmt.Errorf("credential intent is incomplete for an authentication change")
		}
	}
	return nil
}

func mcpCredentialClassWillReplace(plan, state MCPServerResourceModel, hydration map[string]interface{}) bool {
	priorAuth, priorAuthKnown := mcpKnownRawString(hydration, "auth_type")
	if !priorAuthKnown && !state.AuthType.IsNull() && !state.AuthType.IsUnknown() {
		priorAuth, priorAuthKnown = state.AuthType.ValueString(), true
	}
	return priorAuthKnown && !plan.AuthType.IsNull() && !plan.AuthType.IsUnknown() &&
		mcpAuthCredentialClass(priorAuth) != mcpAuthCredentialClass(plan.AuthType.ValueString())
}

func validateMCPFieldCredentialMerge(ctx context.Context, plan, state, config MCPServerResourceModel, hydration map[string]interface{}, committed mcpFieldOwnership) error {
	if !committed.Owned[mcpFieldCredentialsPath] || mcpCredentialClassWillReplace(plan, state, hydration) || config.Credentials.IsNull() || config.Credentials.IsUnknown() || state.Credentials.IsNull() || state.Credentials.IsUnknown() {
		return nil
	}
	prior, err := mcpFieldStringMap(ctx, state.Credentials)
	if err != nil {
		return err
	}
	desired, err := mcpFieldStringMap(ctx, config.Credentials)
	if err != nil {
		return err
	}
	for key := range prior {
		if _, present := desired[key]; !present {
			return fmt.Errorf("LiteLLM v1.98 merges credential maps; clear credentials first, apply, then re-add the replacement map")
		}
	}
	return nil
}

func mcpAmbiguousEmptyCollectionNeedsWrite(ctx context.Context, fieldPath string, desired, remote interface{}, state MCPServerResourceModel, committed mcpFieldOwnership) bool {
	switch fieldPath {
	case mcpFieldAccessGroupsPath, mcpFieldArgsPath, mcpFieldAllowedToolsPath, mcpFieldExtraHeadersPath,
		mcpFieldEnvPath, mcpFieldStaticHeadersPath:
	default:
		return false
	}
	isEmptyCollection := func(value interface{}) bool {
		switch normalized := normalizeMCPWireValue(value).(type) {
		case []interface{}:
			return len(normalized) == 0
		case map[string]interface{}:
			return len(normalized) == 0
		default:
			return false
		}
	}
	if !isEmptyCollection(desired) || !isEmptyCollection(remote) {
		return false
	}
	// Empty lists/maps are LiteLLM's restricted-role masking sentinels. An
	// initial takeover must establish explicit emptiness with a PUT. Existing
	// ownership can skip the PUT only when prior state already proves the same
	// empty intent; a non-empty→empty transition must still be sent.
	if !committed.Owned[fieldPath] {
		return true
	}
	prior, err := mcpFieldDesiredValue(ctx, state, fieldPath)
	return err != nil || !mcpWireValuesEqual(desired, prior)
}

func buildMCPFieldDelta(ctx context.Context, plan MCPServerResourceModel, config, state MCPServerResourceModel, committed, candidate mcpFieldOwnership, hydration map[string]interface{}) (map[string]interface{}, error) {
	if err := validateMCPFieldCredentialMerge(ctx, plan, state, config, hydration, committed); err != nil {
		return nil, err
	}
	delta := map[string]interface{}{}
	presence := mcpFieldConfigPresence(config)
	addLiftedCredentialIntent := func(credentials map[string]string) {
		for _, name := range mcpCredentialLiftedColumnNames {
			if value, present := credentials[name]; present {
				delta[name] = value
			}
		}
	}
	if candidate.Removals[mcpFieldCredentialsPath] {
		priorCredentials, err := mcpFieldStringMap(ctx, state.Credentials)
		if err != nil {
			return nil, fmt.Errorf("owned credentials cannot be cleared without known prior state")
		}
		// v1.98 lifts these accepted legacy credential keys into dedicated
		// columns. Clear exactly the columns established by prior Terraform
		// intent alongside the credential blob; never clear an unowned column.
		for _, name := range mcpCredentialLiftedColumnNames {
			if _, owned := priorCredentials[name]; owned {
				delta[name] = nil
			}
		}
	}
	for fieldPath := range candidate.Removals {
		delta[mcpFieldWireName(fieldPath)] = mcpFieldRemovalSentinel(fieldPath)
	}
	for fieldPath := range candidate.Owned {
		if candidate.Removals[fieldPath] {
			continue
		}
		// Unknown configuration is not mutation intent. It retains candidate
		// ownership, but neither proposed-state placeholders nor scalar zero
		// values may reach the wire.
		if presence[fieldPath] != 1 {
			continue
		}
		desired, err := mcpFieldDesiredValue(ctx, config, fieldPath)
		if err != nil {
			return nil, err
		}
		name := mcpFieldWireName(fieldPath)
		if fieldPath == mcpFieldCredentialsPath {
			// Values cannot be compared to API markers. v1.98 merges credential
			// maps, so an initial empty Update takeover cannot establish exact
			// emptiness over hidden existing keys. Create remains safe because no
			// prior row exists.
			if !committed.Owned[fieldPath] {
				credentials, validCredentials := desired.(map[string]string)
				replacesCredentialClass := mcpCredentialClassWillReplace(plan, state, hydration)
				if !validCredentials || !replacesCredentialClass {
					return nil, fmt.Errorf("credentials cannot be adopted safely through LiteLLM's merge-only update without a credential-class replacement")
				}
				delta[name] = desired
				addLiftedCredentialIntent(credentials)
			} else {
				credentials, validCredentials := desired.(map[string]string)
				if !validCredentials {
					return nil, fmt.Errorf("configured credentials are invalid")
				}
				writeCredentials := !config.Credentials.Equal(state.Credentials) || mcpCredentialClassWillReplace(plan, state, hydration)
				for _, liftedName := range mcpCredentialLiftedColumnNames {
					if liftedValue, configured := credentials[liftedName]; configured {
						remote, present := hydration[liftedName]
						writeCredentials = writeCredentials || !present || !mcpWireValuesEqual(liftedValue, remote)
					}
				}
				if writeCredentials {
					delta[name] = desired
					addLiftedCredentialIntent(credentials)
				}
			}
			continue
		}
		remote, present := hydration[name]
		// An authoritative equal remote value is sufficient for an ownership
		// takeover even when Terraform's prior public value differs. Restricted-
		// role empty collection sentinels are not authoritative equality.
		if !present || !mcpWireValuesEqual(desired, remote) || mcpAmbiguousEmptyCollectionNeedsWrite(ctx, fieldPath, desired, remote, state, committed) {
			delta[name] = desired
		}
	}
	return delta, nil
}

func addMCPBaseDelta(delta map[string]interface{}, plan, state MCPServerResourceModel, hydration map[string]interface{}) {
	for _, item := range []struct {
		name       string
		value      types.String
		priorValue types.String
		nullable   bool
	}{
		{name: "server_name", value: plan.ServerName, priorValue: state.ServerName},
		{name: "transport", value: plan.Transport, priorValue: state.Transport},
		{name: "auth_type", value: plan.AuthType, priorValue: state.AuthType},
		{name: "url", value: plan.URL, priorValue: state.URL, nullable: true},
		{name: "spec_path", value: plan.SpecPath, priorValue: state.SpecPath, nullable: true},
	} {
		if item.value.IsUnknown() {
			continue
		}
		var desired interface{}
		if item.value.IsNull() && item.nullable {
			desired = nil
		} else if item.value.IsNull() {
			continue
		} else {
			desired = item.value.ValueString()
		}
		remote, present := hydration[item.name]
		if !present || (item.nullable && remote == nil) {
			switch item.name {
			case "server_name":
				if !state.ServerName.IsNull() && !state.ServerName.IsUnknown() {
					remote, present = state.ServerName.ValueString(), true
				}
			case "auth_type":
				if !state.AuthType.IsNull() && !state.AuthType.IsUnknown() {
					remote, present = state.AuthType.ValueString(), true
				}
			case "url":
				if !state.URL.IsNull() && !state.URL.IsUnknown() {
					remote, present = state.URL.ValueString(), true
				}
			case "spec_path":
				if !state.SpecPath.IsNull() && !state.SpecPath.IsUnknown() {
					remote, present = state.SpecPath.ValueString(), true
				}
			}
		}
		changedInTerraform := !item.value.Equal(item.priorValue)
		if item.name == "auth_type" && !present && (item.priorValue.IsNull() || item.priorValue.IsUnknown()) {
			changedInTerraform = false
		}
		missingNeedsWrite := !present && !item.nullable && item.name != "auth_type"
		if changedInTerraform || missingNeedsWrite || (present && !mcpWireValuesEqual(desired, remote)) {
			delta[item.name] = desired
		}
	}
	// Endpoint transitions are supplied as a complete supported endpoint pair.
	// This satisfies the v1.98 transport validator without projecting unrelated
	// MCP fields into the delta.
	_, urlSent := delta["url"]
	_, specSent := delta["spec_path"]
	if urlSent || specSent {
		if !plan.URL.IsNull() && !plan.URL.IsUnknown() {
			delta["url"] = plan.URL.ValueString()
		}
		if !plan.SpecPath.IsNull() && !plan.SpecPath.IsUnknown() {
			delta["spec_path"] = plan.SpecPath.ValueString()
		}
	}
}

func completeMCPUpdateDelta(ctx context.Context, delta map[string]interface{}, plan MCPServerResourceModel, hydration map[string]interface{}) error {
	if _, serverNameSent := delta["server_name"]; serverNameSent {
		if alias, aliasSent := delta["alias"]; aliasSent {
			aliasString, ok := alias.(string)
			if !ok || aliasString == "" || strings.Contains(aliasString, " ") {
				return fmt.Errorf("alias intent cannot converge with a server name change")
			}
		} else {
			alias, ok := hydration["alias"].(string)
			if !ok || alias == "" || strings.Contains(alias, " ") {
				return fmt.Errorf("a stable authoritative alias is required for a server name change")
			}
			delta["alias"] = alias
		}
	}

	transportValue, transportSent := delta["transport"]
	if !transportSent {
		return nil
	}
	transport, ok := transportValue.(string)
	if !ok {
		return fmt.Errorf("transport update dependencies are invalid")
	}
	switch transport {
	case "http", "sse":
		endpointKnown := false
		if mcpKnownNonEmptyString(plan.URL) {
			delta["url"] = plan.URL.ValueString()
			endpointKnown = true
		}
		if mcpKnownNonEmptyString(plan.SpecPath) {
			delta["spec_path"] = plan.SpecPath.ValueString()
			endpointKnown = true
		}
		if !endpointKnown {
			return fmt.Errorf("a known non-empty endpoint is required for an HTTP or SSE transport update")
		}
	case "stdio":
		if !mcpKnownNonEmptyString(plan.Command) {
			return fmt.Errorf("a known non-empty command is required for a stdio transport update")
		}
		command := plan.Command.ValueString()
		if _, allowed := mcpStdioAllowedCommandsV198[mcpStdioCommandBaseV198(command)]; !allowed {
			return fmt.Errorf("the stdio command is not safe for a transport update")
		}
		args, err := mcpFieldStringList(ctx, plan.Args)
		if err != nil || len(args) == 0 {
			return fmt.Errorf("known non-empty string arguments are required for a stdio transport update")
		}
		delta["command"] = command
		delta["args"] = args
	default:
		return fmt.Errorf("transport update dependencies are invalid")
	}
	return nil
}

func verifyMCPCreateEndpointReadback(planned MCPServerResourceModel, observed map[string]interface{}) error {
	intent := map[string]interface{}{"transport": planned.Transport.ValueString()}
	if !planned.URL.IsNull() && !planned.URL.IsUnknown() {
		intent["url"] = planned.URL.ValueString()
	}
	if !planned.SpecPath.IsNull() && !planned.SpecPath.IsUnknown() {
		intent["spec_path"] = planned.SpecPath.ValueString()
	}
	return verifyMCPBaseDeltaReadback(intent, observed)
}

func verifyMCPBaseDeltaReadback(delta, observed map[string]interface{}) error {
	for _, name := range []string{"server_name", "transport", "auth_type", "url", "spec_path"} {
		want, sent := delta[name]
		if !sent {
			continue
		}
		got, present := observed[name]
		if want == nil {
			if present && got != nil {
				return fmt.Errorf("cleared MCP base field did not converge")
			}
			continue
		}
		if !present || !mcpWireValuesEqual(want, got) {
			return fmt.Errorf("changed MCP base field did not converge")
		}
	}
	return nil
}

func verifyMCPCredentialLiftedColumns(baseline, observed, delta map[string]interface{}) error {
	for _, name := range mcpCredentialLiftedColumnNames {
		want, sent := delta[name]
		got, present := observed[name]
		if sent {
			if want == nil {
				if present && got != nil {
					return fmt.Errorf("a lifted credential column did not clear")
				}
				continue
			}
			if !present || !mcpWireValuesEqual(want, got) {
				return fmt.Errorf("a lifted credential column did not converge")
			}
			continue
		}
		prior, visible := baseline[name]
		if !visible || prior == nil {
			continue
		}
		if !present || !mcpWireValuesEqual(prior, got) {
			return fmt.Errorf("a visible unowned lifted credential column changed")
		}
	}
	return nil
}

func verifyMCPFieldUpdateReadback(ctx context.Context, plan, config MCPServerResourceModel, committed, candidate mcpFieldOwnership, baseline, observed, delta map[string]interface{}) error {
	for _, fieldPath := range mcpFieldPaths {
		name := mcpFieldWireName(fieldPath)
		if sent, changed := delta[name]; changed {
			if fieldPath == mcpFieldCredentialsPath {
				continue
			}
			got, present := observed[name]
			if !present || !mcpWireValuesEqual(sent, got) {
				return fmt.Errorf("changed MCP field did not converge")
			}
			continue
		}
		if committed.Owned[fieldPath] || candidate.Owned[fieldPath] {
			continue
		}
		prior, priorVisible := baseline[name]
		if !priorVisible || prior == nil {
			continue
		}
		got, present := observed[name]
		if !present || !mcpWireValuesEqual(prior, got) {
			return fmt.Errorf("a visible unmanaged MCP field changed")
		}
	}
	return nil
}

func (r *MCPServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, config, state MCPServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	committedInfo, infoDiags := readMCPInfoProvenance(ctx, req.Private)
	resp.Diagnostics.Append(infoDiags...)
	committedFields, fieldDiags := readMCPFieldOwnership(ctx, req.Private)
	resp.Diagnostics.Append(fieldDiags...)
	if resp.Diagnostics.HasError() {
		resp.State, resp.Private = req.State, req.Private
		return
	}
	fallbackInfo := deriveMCPInfoPlanProvenance(committedInfo, config, state)
	plannedInfo, pendingInfoDiags := readPendingMCPInfoProvenance(ctx, req.Private, fallbackInfo)
	resp.Diagnostics.Append(pendingInfoDiags...)
	expectedFields := deriveMCPFieldPlanOwnership(committedFields, config)
	plannedFields, pendingFieldDiags := readPendingMCPFieldOwnership(ctx, req.Private, expectedFields)
	resp.Diagnostics.Append(pendingFieldDiags...)
	if resp.Diagnostics.HasError() {
		resp.State, resp.Private = req.State, req.Private
		return
	}
	if mcpAliasUpdateIntentCannotConverge(config.Alias) {
		resp.State, resp.Private = req.State, req.Private
		resp.Diagnostics.AddError("Invalid MCP Alias Configuration", "Configured alias must not require LiteLLM normalization. No update was attempted; prior public and private state was retained.")
		return
	}
	plan.ID, plan.ServerID = state.ID, state.ServerID

	hydrated := state
	_, _, hydration, err := r.readMCPServerWithAllProvenanceDirect(ctx, &hydrated, committedInfo, committedFields, false)
	if err != nil {
		resp.State, resp.Private = req.State, req.Private
		resp.Diagnostics.AddError("MCP Server Hydration Failed", "The direct endpoint did not return an identity-valid, schema-valid authoritative response. No PUT was attempted and prior state/private ownership was retained.")
		return
	}
	baseInfo, infoPresence, err := mcpInfoDocumentFromResponse(hydration)
	if err != nil {
		resp.State, resp.Private = req.State, req.Private
		resp.Diagnostics.AddError("MCP Server Hydration Failed", "The direct endpoint returned malformed MCP info. No PUT was attempted and prior state/private ownership was retained.")
		return
	}
	if infoPresence != apiValuePresent {
		authoritative, markerDiags := mcpInfoPrivateDocumentAuthoritative(ctx, req.Private)
		resp.Diagnostics.Append(markerDiags...)
		if resp.Diagnostics.HasError() || !authoritative || state.MCPInfoJSON.IsNull() || state.MCPInfoJSON.IsUnknown() {
			resp.State, resp.Private = req.State, req.Private
			resp.Diagnostics.AddError("Authoritative MCP Info Required", "LiteLLM masked mcp_info and no authoritative complete document is available. No PUT was attempted.")
			return
		}
		baseInfo, err = parseMCPInfoJSONObject(state.MCPInfoJSON.ValueString())
		if err != nil {
			resp.State, resp.Private = req.State, req.Private
			resp.Diagnostics.AddError("Authoritative MCP Info Required", "The prior complete MCP info document is malformed. No PUT was attempted.")
			return
		}
	}
	resolvedInfo, err := resolveMCPInfoUpdateDocument(ctx, baseInfo, config)
	if err != nil {
		resp.State, resp.Private = req.State, req.Private
		resp.Diagnostics.AddError("Invalid MCP Info Configuration", "The complete MCP info update could not be resolved safely. No PUT was attempted.")
		return
	}
	delta, err := buildMCPFieldDelta(ctx, plan, config, state, committedFields, plannedFields, hydration)
	if err != nil {
		resp.State, resp.Private = req.State, req.Private
		resp.Diagnostics.AddError("Unsafe MCP Field Update", err.Error())
		return
	}
	addMCPBaseDelta(delta, plan, state, hydration)
	if err := completeMCPUpdateDelta(ctx, delta, plan, hydration); err != nil {
		resp.State, resp.Private = req.State, req.Private
		resp.Diagnostics.AddError("Unsafe MCP Update", err.Error()+". No PUT was attempted; prior public and private state was retained.")
		return
	}
	legacyInfoMigration := committedInfo.Versioned && !committedInfo.V2 && plannedInfo.V2
	fixedInfoOwnershipChange := !mcpInfoOwnershipEqual(committedInfo, plannedInfo) && (committedInfo.Mode == mcpInfoModeSelective || plannedInfo.Mode == mcpInfoModeSelective)
	if !mcpInfoJSONValuesEqual(baseInfo, resolvedInfo.Document) || legacyInfoMigration || fixedInfoOwnershipChange {
		// Preserve #213's established complete-document apply behavior. General
		// field removals never synthesize mcp_info; only #213 ownership intent
		// can put this member in the delta.
		delta["mcp_info"] = cloneMCPInfoJSONObject(resolvedInfo.Document)
	}
	delta["server_id"] = plan.ServerID.ValueString()

	remoteURL, remoteURLKnown := hydration["url"]
	if !remoteURLKnown || remoteURL == nil {
		switch {
		case !state.URL.IsNull() && !state.URL.IsUnknown():
			remoteURL, remoteURLKnown = state.URL.ValueString(), true
		case state.URL.IsNull():
			remoteURL, remoteURLKnown = nil, true
		default:
			remoteURLKnown = false
		}
	}
	urlChanged := false
	if desired, sent := delta["url"]; sent && desired != nil {
		// v1.98 treats only a supplied non-null URL as a URL-change trigger.
		// Null→value is therefore a change, while value→null preserves existing
		// auth-flow fields. If the previous endpoint is unknown, assume a change.
		urlChanged = !remoteURLKnown || !mcpWireValuesEqual(desired, remoteURL)
	}
	remoteAuth, remoteAuthPresent := mcpKnownRawString(hydration, "auth_type")
	if !remoteAuthPresent && !state.AuthType.IsNull() && !state.AuthType.IsUnknown() {
		remoteAuth = state.AuthType.ValueString()
	}
	desiredAuth, authSent := delta["auth_type"].(string)
	authClassChanged := authSent && mcpAuthCredentialClass(remoteAuth) != mcpAuthCredentialClass(desiredAuth)
	if err := validateMCPImplicitClearSafety(config, state, plannedFields, hydration, delta, urlChanged, authClassChanged); err != nil {
		resp.State, resp.Private = req.State, req.Private
		resp.Diagnostics.AddError("Unsafe MCP URL or Authentication Update", "LiteLLM v1.98 would implicitly clear an unowned, unknown, or unchanged OAuth/credential value ("+err.Error()+"). Configure every affected value with a genuinely changed or cleared complete intent in one apply. No PUT was attempted; restorative PUTs are never used.")
		return
	}

	desiredPlan := plan
	// Unknown configuration retains ownership but is not mutation intent. Seed
	// proposed unknowns from prior state before projecting role-masked readback,
	// otherwise null/empty sanitizers could replace known owned values before
	// the final unknown-state reconciliation has a chance to restore them.
	resolveUnknownMCPServerState(&plan, &state)
	mutation := len(delta) > 1
	readback := hydration
	if mutation {
		var updateResult map[string]interface{}
		if err := r.putMCPServer(ctx, delta, &updateResult); err != nil {
			resp.State, resp.Private = req.State, req.Private
			resp.Diagnostics.AddError("Client Error", "LiteLLM did not confirm the MCP server update. Prior public and private state was retained.")
			return
		}
		if len(updateResult) > 0 && validateMCPServerResponse(updateResult, plan.ServerID.ValueString()) != nil {
			resp.State, resp.Private = req.State, req.Private
			resp.Diagnostics.AddError("Invalid Update Response", "LiteLLM accepted the update but returned a malformed response. Prior state/private ownership was retained.")
			return
		}
		_, _, readback, err = r.readMCPServerWithAllProvenanceDirect(ctx, &plan, plannedInfo, committedMCPFieldOwnership(plannedFields), false)
	} else {
		err = r.readMCPServerResultProjection(ctx, &plan, readback, plannedInfo, committedMCPFieldOwnership(plannedFields), false, mcpInfoLeafSet{}, cloneMCPInfoLeafSet(plannedInfo.API))
	}
	if err != nil {
		resp.State, resp.Private = req.State, req.Private
		resp.Diagnostics.AddError("Read Error", "Authoritative direct readback failed. Prior public and private state was retained.")
		return
	}
	observedInfo, observedInfoPresence, infoErr := mcpInfoDocumentFromResponse(readback)
	if infoErr != nil || observedInfoPresence != apiValuePresent || mcpOwnedEndpointReadbackMismatch(&desiredPlan, &plan, &state) || verifyMCPInfoReadback(baseInfo, resolvedInfo.Document, observedInfo, plannedInfo) != nil || verifyMCPBaseDeltaReadback(delta, readback) != nil || verifyMCPCredentialLiftedColumns(hydration, readback, delta) != nil || verifyMCPFieldUpdateReadback(ctx, plan, config, committedFields, plannedFields, hydration, readback, delta) != nil {
		resp.State, resp.Private = req.State, req.Private
		resp.Diagnostics.AddError("Inconsistent MCP Server Readback", "LiteLLM did not confirm all changed owned values, clear sentinels, and visible unmanaged values. Prior public and private state was retained.")
		return
	}
	plan.FieldOwnershipGeneration = types.Int64Value(plannedFields.Generation)
	resolveUnknownMCPServerState(&plan, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		resp.Diagnostics.Append(writeMCPInfoProvenance(ctx, resp.Private, plannedInfo)...)
		resp.Diagnostics.Append(writeMCPInfoPrivateDocumentAuthoritative(ctx, resp.Private, true)...)
		resp.Diagnostics.Append(writeMCPFieldOwnership(ctx, resp.Private, committedMCPFieldOwnership(plannedFields))...)
	}
}
