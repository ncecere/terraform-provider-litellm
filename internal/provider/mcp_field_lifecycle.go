package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var mcpCredentialLiftedAliasNames = map[string]bool{
	"token_exchange_endpoint": true,
	"audience":                true,
	"subject_token_type":      true,
	"token_exchange_profile":  true,
}

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
		if name == "upstream_resource" && value == "" {
			return fmt.Errorf("credentials contain an empty observable upstream resource that LiteLLM v1.98 cannot return")
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
	floatValue := func(value types.Float64) (interface{}, error) {
		if value.IsNull() || value.IsUnknown() || math.IsNaN(value.ValueFloat64()) || math.IsInf(value.ValueFloat64(), 0) || value.ValueFloat64() <= 0 {
			return nil, fmt.Errorf("unknown, null, or invalid MCP positive-number field")
		}
		return value.ValueFloat64(), nil
	}
	intValue := func(value types.Int64) (interface{}, error) {
		if value.IsNull() || value.IsUnknown() || value.ValueInt64() <= 0 || value.ValueInt64() > math.MaxInt32 {
			return nil, fmt.Errorf("unknown, null, or invalid MCP positive-integer field")
		}
		return value.ValueInt64(), nil
	}
	switch fieldPath {
	case mcpFieldAliasPath:
		value, err := stringValue(data.Alias)
		if err != nil {
			return nil, err
		}
		return mcpNormalizeAliasV198(value.(string)), nil
	case mcpFieldDescriptionPath:
		return stringValue(data.Description)
	case mcpFieldCommandPath:
		return stringValue(data.Command)
	case mcpFieldIssuerPath:
		return stringValue(data.Issuer)
	case mcpFieldAuthorizationURLPath:
		return stringValue(data.AuthorizationURL)
	case mcpFieldTokenURLPath:
		return stringValue(data.TokenURL)
	case mcpFieldRegistrationURLPath:
		return stringValue(data.RegistrationURL)
	case mcpFieldTokenExchangeEndpointPath:
		return stringValue(data.TokenExchangeEndpoint)
	case mcpFieldAudiencePath:
		return stringValue(data.Audience)
	case mcpFieldSubjectTokenTypePath:
		return stringValue(data.SubjectTokenType)
	case mcpFieldTokenExchangeProfilePath:
		return stringValue(data.TokenExchangeProfile)
	case mcpFieldAllowAllKeysPath:
		return boolValue(data.AllowAllKeys)
	case mcpFieldAvailablePublicInternetPath:
		return boolValue(data.AvailableOnPublicInternet)
	case mcpFieldOAuth2FlowPath:
		return stringValue(data.OAuth2Flow)
	case mcpFieldInstructionsPath:
		return stringValue(data.Instructions)
	case mcpFieldDelegateAuthToUpstreamPath:
		value, err := boolValue(data.DelegateAuthToUpstream)
		if err != nil {
			return nil, err
		}
		if value.(bool) && (data.AuthType.IsNull() || data.AuthType.IsUnknown() || data.AuthType.ValueString() != "oauth2" ||
			data.OAuth2Flow.IsNull() || data.OAuth2Flow.IsUnknown() || data.OAuth2Flow.ValueString() != "authorization_code") {
			return nil, fmt.Errorf("upstream delegation requires complete interactive OAuth intent")
		}
		return value, nil
	case mcpFieldOAuthPassthroughPath:
		return boolValue(data.OAuthPassthrough)
	case mcpFieldDCRBridgePath:
		return boolValue(data.DCRBridge)
	case mcpFieldIsBYOKPath:
		return boolValue(data.IsBYOK)
	case mcpFieldBYOKAPIKeyHelpURLPath:
		return stringValue(data.BYOKAPIKeyHelpURL)
	case mcpFieldSourceURLPath:
		return stringValue(data.SourceURL)
	case mcpFieldTimeoutPath:
		return floatValue(data.Timeout)
	case mcpFieldMaxConcurrentRequestsPath:
		return intValue(data.MaxConcurrentRequests)
	case mcpFieldOAuthScopesPath:
		return mcpFieldStringList(ctx, data.OAuthScopes)
	case mcpFieldAccessGroupsPath:
		return mcpFieldStringList(ctx, data.MCPAccessGroups)
	case mcpFieldArgsPath:
		return mcpFieldStringList(ctx, data.Args)
	case mcpFieldAllowedToolsPath:
		return mcpFieldStringList(ctx, data.AllowedTools)
	case mcpFieldExtraHeadersPath:
		return mcpFieldStringList(ctx, data.ExtraHeaders)
	case mcpFieldBYOKDescriptionPath:
		return mcpFieldStringList(ctx, data.BYOKDescription)
	case mcpFieldEnvVarsPath:
		values, _, diagnostics := strictTerraformMCPEnvVars(ctx, data.EnvVars, path.Root("env_vars"))
		if diagnostics.HasError() {
			return nil, fmt.Errorf("invalid MCP environment-variable collection")
		}
		return mcpEnvVarsWire(values), nil
	case mcpFieldEnvPath:
		return mcpFieldStringMap(ctx, data.Env)
	case mcpFieldStaticHeadersPath:
		return mcpFieldStringMap(ctx, data.StaticHeaders)
	case mcpFieldToolNameToDisplayNamePath:
		return mcpFieldStringMap(ctx, data.ToolNameToDisplayName)
	case mcpFieldToolNameToDescriptionPath:
		return mcpFieldStringMap(ctx, data.ToolNameToDescription)
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

// liftMCPTokenExchangeCredentialAliases implements LiteLLM v1.98's legacy
// request dialect without changing Terraform's configured/state map shape.
// Explicit top-level values are installed by the canonical sibling lifecycle;
// callers reject dual-source intent before this wire-only transformation.
func liftMCPTokenExchangeCredentialAliases(request map[string]interface{}) error {
	raw, present := request["credentials"]
	if !present || raw == nil {
		return nil
	}
	credentials := map[string]string{}
	switch value := raw.(type) {
	case map[string]string:
		for name, member := range value {
			credentials[name] = member
		}
	case map[string]interface{}:
		for name, member := range value {
			stringMember, ok := member.(string)
			if !ok {
				// Native scopes are merged after the string map and are not a
				// lifted alias. Preserve their exact list value.
				continue
			}
			credentials[name] = stringMember
		}
	default:
		return fmt.Errorf("credential intent has an incompatible shape")
	}
	for _, name := range mcpCredentialLiftedColumnNames {
		value, configured := credentials[name]
		if !configured {
			continue
		}
		if _, collision := request[name]; collision {
			return fmt.Errorf("canonical and legacy token-exchange sources collide")
		}
		request[name] = value
		delete(credentials, name)
	}
	switch value := raw.(type) {
	case map[string]string:
		request["credentials"] = credentials
	case map[string]interface{}:
		stripped := make(map[string]interface{}, len(value))
		for name, member := range value {
			if !mcpCredentialLiftedAliasNames[name] {
				stripped[name] = member
			}
		}
		request["credentials"] = stripped
	}
	return nil
}

func mcpFieldRemovalSentinel(fieldPath string) interface{} {
	switch fieldPath {
	case mcpFieldAliasPath, mcpFieldDescriptionPath, mcpFieldCommandPath,
		mcpFieldIssuerPath, mcpFieldAuthorizationURLPath, mcpFieldTokenURLPath, mcpFieldRegistrationURLPath,
		mcpFieldTokenExchangeEndpointPath, mcpFieldAudiencePath, mcpFieldSubjectTokenTypePath,
		mcpFieldTokenExchangeProfilePath,
		mcpFieldCredentialsPath, mcpFieldOAuth2FlowPath, mcpFieldInstructionsPath,
		mcpFieldDCRBridgePath, mcpFieldBYOKAPIKeyHelpURLPath, mcpFieldSourceURLPath,
		mcpFieldTimeoutPath, mcpFieldMaxConcurrentRequestsPath:
		return nil
	case mcpFieldAccessGroupsPath, mcpFieldArgsPath, mcpFieldAllowedToolsPath, mcpFieldExtraHeadersPath,
		mcpFieldOAuthScopesPath, mcpFieldBYOKDescriptionPath:
		return []string{}
	case mcpFieldEnvVarsPath:
		return []map[string]interface{}{}
	case mcpFieldEnvPath, mcpFieldStaticHeadersPath, mcpFieldToolNameToDisplayNamePath,
		mcpFieldToolNameToDescriptionPath:
		return map[string]string{}
	case mcpFieldAllowAllKeysPath, mcpFieldDelegateAuthToUpstreamPath, mcpFieldOAuthPassthroughPath, mcpFieldIsBYOKPath:
		return false
	case mcpFieldAvailablePublicInternetPath:
		// The column is non-nullable with @default(true), and the partial PUT
		// contract cannot transmit null. Releasing ownership restores that exact
		// durable default explicitly.
		return true
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
	return !value.IsNull() && !value.IsUnknown() && value.ValueString() == ""
}

func mcpAliasUpdateIntentCannotConverge(value types.String) bool {
	// Pinned v1.98 preserves an explicit empty alias on a partial Update when
	// server_name is omitted. Non-empty aliases are normalized before planning
	// and request construction.
	return false
}

func (r *MCPServerResource) buildMCPServerCreateRequest(ctx context.Context, plan, config *MCPServerResourceModel, resolvedMCPInfo map[string]interface{}, mcpInfoPresent bool) (map[string]interface{}, error) {
	if mcpAliasCreateIntentCannotConverge(config.Alias) {
		return nil, fmt.Errorf("configured alias must be non-empty on create")
	}
	request, err := r.buildMCPServerRequest(ctx, plan, resolvedMCPInfo, mcpInfoPresent)
	if err != nil {
		return nil, err
	}
	presence := mcpFieldConfigPresence(*config)
	for _, fieldPath := range mcpFieldPaths {
		if presence[fieldPath] != 1 || fieldPath == mcpFieldOAuthScopesPath {
			continue
		}
		desiredSource := *config
		if fieldPath == mcpFieldEnvVarsPath {
			// Nested defaults are materialized in the plan, while top-level Config
			// remains the sole proof that the list itself was configured.
			desiredSource.EnvVars = plan.EnvVars
		}
		value, err := mcpFieldDesiredValue(ctx, desiredSource, fieldPath)
		if err != nil {
			return nil, fmt.Errorf("configured MCP collection is invalid")
		}
		request[mcpFieldWireName(fieldPath)] = value
	}
	if presence[mcpFieldOAuthScopesPath] == 1 {
		scopes, err := mcpFieldStringList(ctx, config.OAuthScopes)
		if err != nil || mergeMCPScopesCredentialIntent(request, scopes) != nil {
			return nil, fmt.Errorf("configured MCP credential intent is invalid")
		}
	}
	if err := liftMCPTokenExchangeCredentialAliases(request); err != nil {
		return nil, fmt.Errorf("configured MCP token-exchange intent is invalid")
	}
	return request, nil
}

func mergeMCPScopesCredentialIntent(request map[string]interface{}, scopes []string) error {
	credentials := map[string]interface{}{}
	if raw, present := request["credentials"]; present {
		switch existing := raw.(type) {
		case map[string]string:
			for name, value := range existing {
				credentials[name] = value
			}
		case map[string]interface{}:
			for name, value := range existing {
				credentials[name] = value
			}
		case nil:
			return fmt.Errorf("credential clear and OAuth scope intent cannot be combined safely")
		default:
			return fmt.Errorf("credential intent has an incompatible shape")
		}
	}
	if _, collision := credentials["scopes"]; collision {
		return fmt.Errorf("credential scope aliases cannot be combined")
	}
	credentials["scopes"] = scopes
	request["credentials"] = credentials
	return nil
}

func mcpObservedCredentialString(observed map[string]interface{}, name string) (string, bool) {
	raw, present := observed["credentials"]
	if !present || raw == nil {
		return "", false
	}
	var value string
	switch credentials := raw.(type) {
	case map[string]interface{}:
		value, _ = credentials[name].(string)
	case map[string]string:
		value = credentials[name]
	default:
		return "", false
	}
	return value, value != ""
}

func verifyMCPObservableCredentialReadback(ctx context.Context, desired types.Map, observed map[string]interface{}) error {
	credentials, err := mcpFieldStringMap(ctx, desired)
	if err != nil {
		return err
	}
	for _, name := range mcpCredentialLiftedColumnNames {
		want, configured := credentials[name]
		if !configured {
			continue
		}
		got, present := observed[name]
		if !present || !mcpWireValuesEqual(want, got) {
			return fmt.Errorf("an observable credential column did not converge")
		}
	}
	if want, configured := credentials["upstream_resource"]; configured {
		got, present := mcpObservedCredentialString(observed, "upstream_resource")
		if !present || want != got {
			return fmt.Errorf("observable credential configuration did not converge")
		}
	}
	return nil
}

func verifyMCPFieldCreateReadback(ctx context.Context, config MCPServerResourceModel, observed map[string]interface{}, ownership mcpFieldOwnership) error {
	for fieldPath := range ownership.Owned {
		if fieldPath == mcpFieldOAuthScopesPath {
			// v1.98 redacts credentials.scopes from every management response.
			// Successful mutation plus a complete identity-valid direct response is
			// the strongest available confirmation; scopes are never observable.
			continue
		}
		if fieldPath == mcpFieldEnvVarsPath {
			want, err := mcpFieldDesiredValue(ctx, config, fieldPath)
			got, presence, errObserved := canonicalMCPEnvVarsAPIWire(observed)
			if err != nil || errObserved != nil || presence != apiValuePresent || !mcpWireValuesEqual(want, got) {
				return fmt.Errorf("owned MCP environment variables did not converge")
			}
			continue
		}
		if fieldPath == mcpFieldCredentialsPath {
			// Secret credential values are redacted, but v1.98 exposes four
			// lifted token-exchange columns and upstream_resource. Verify every
			// configured observable member before committing ownership.
			if err := verifyMCPObservableCredentialReadback(ctx, config.Credentials, observed); err != nil {
				return err
			}
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

func validateMCPImplicitClearSafety(config, state MCPServerResourceModel, planned mcpFieldOwnership, hydration map[string]interface{}, delta map[string]interface{}, urlChanged, authClassChanged bool, issuerChange ...bool) error {
	issuerChanged := len(issuerChange) != 0 && issuerChange[0]
	if !urlChanged && !authClassChanged && !issuerChanged {
		return nil
	}
	presence := mcpFieldConfigPresence(config)
	legacyPrior := map[string]bool{}
	legacyConfigured := map[string]bool{}
	if !state.Credentials.IsNull() && !state.Credentials.IsUnknown() {
		for name := range state.Credentials.Elements() {
			if mcpCredentialLiftedAliasNames[name] {
				legacyPrior[name] = true
			}
		}
	}
	if !config.Credentials.IsNull() && !config.Credentials.IsUnknown() {
		for name := range config.Credentials.Elements() {
			if mcpCredentialLiftedAliasNames[name] {
				legacyConfigured[name] = true
			}
		}
	}
	for _, item := range []struct {
		fieldPath string
		prior     types.String
	}{
		{fieldPath: mcpFieldIssuerPath, prior: state.Issuer},
		{fieldPath: mcpFieldAuthorizationURLPath, prior: state.AuthorizationURL},
		{fieldPath: mcpFieldTokenURLPath, prior: state.TokenURL},
		{fieldPath: mcpFieldRegistrationURLPath, prior: state.RegistrationURL},
		{fieldPath: mcpFieldOAuth2FlowPath, prior: state.OAuth2Flow},
		{fieldPath: mcpFieldTokenExchangeEndpointPath, prior: state.TokenExchangeEndpoint},
		{fieldPath: mcpFieldAudiencePath, prior: state.Audience},
		{fieldPath: mcpFieldSubjectTokenTypePath, prior: state.SubjectTokenType},
		{fieldPath: mcpFieldTokenExchangeProfilePath, prior: state.TokenExchangeProfile},
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
		if mcpCredentialLiftedAliasNames[name] && supplied {
			legacyRemoval := desired == nil && legacyPrior[name] && (planned.Removals[mcpFieldCredentialsPath] || (authClassChanged && planned.Owned[mcpFieldCredentialsPath] && !legacyConfigured[name]))
			legacyChange := legacyConfigured[name] && planned.Owned[mcpFieldCredentialsPath] && presence[mcpFieldCredentialsPath] == 1 && !mcpWireValuesEqual(desired, raw)
			explicitRemoval = explicitRemoval || legacyRemoval
			explicitChange = explicitChange || legacyChange
		}
		if !explicitRemoval && !explicitChange {
			return fmt.Errorf("an unowned or unchanged OAuth endpoint would be cleared implicitly")
		}
	}
	dcrRaw, dcrPresent := hydration["dcr_bridge"]
	if (!dcrPresent || dcrRaw == nil) && !state.DCRBridge.IsNull() && !state.DCRBridge.IsUnknown() {
		dcrRaw, dcrPresent = state.DCRBridge.ValueBool(), true
	}
	if dcrPresent && dcrRaw != nil {
		desired, supplied := delta["dcr_bridge"]
		explicitRemoval := planned.Removals[mcpFieldDCRBridgePath] && supplied && desired == nil
		explicitChange := planned.Owned[mcpFieldDCRBridgePath] && presence[mcpFieldDCRBridgePath] == 1 && supplied && !mcpWireValuesEqual(desired, dcrRaw)
		if !explicitRemoval && !explicitChange {
			return fmt.Errorf("a DCR bridge value lacks explicit changed intent")
		}
	}
	if authClassChanged {
		// v1.98 replaces the complete credentials object on a credential-class
		// change. Both the generic string members and native scopes must therefore
		// be explicit and known in this apply. Prior or API-only scopes cannot be
		// recovered from management responses because they are always redacted.
		rawCredentials, supplied := delta["credentials"]
		scopesSupplied := false
		switch credentials := rawCredentials.(type) {
		case map[string]interface{}:
			_, scopesSupplied = credentials["scopes"]
		case map[string]string:
			_, scopesSupplied = credentials["scopes"]
		}
		if !supplied || presence[mcpFieldCredentialsPath] != 1 || !planned.Owned[mcpFieldCredentialsPath] ||
			presence[mcpFieldOAuthScopesPath] != 1 || !planned.Owned[mcpFieldOAuthScopesPath] || !scopesSupplied {
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

func mcpCredentialOwnershipClassWillReplace(committed, candidate mcpFieldOwnership) bool {
	return committed.CredentialClass != "" && candidate.CredentialClass != "" && committed.CredentialClass != candidate.CredentialClass
}

func validateMCPFieldCredentialMerge(ctx context.Context, plan, state, config MCPServerResourceModel, hydration map[string]interface{}, committed, candidate mcpFieldOwnership) error {
	if !committed.Owned[mcpFieldCredentialsPath] || mcpCredentialClassWillReplace(plan, state, hydration) || mcpCredentialOwnershipClassWillReplace(committed, candidate) || config.Credentials.IsNull() || config.Credentials.IsUnknown() || state.Credentials.IsNull() || state.Credentials.IsUnknown() {
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
	presence := mcpFieldConfigPresence(config)
	canonicalHandoff := map[string]string{
		"token_exchange_endpoint": mcpFieldTokenExchangeEndpointPath,
		"audience":                mcpFieldAudiencePath,
		"subject_token_type":      mcpFieldSubjectTokenTypePath,
		"token_exchange_profile":  mcpFieldTokenExchangeProfilePath,
	}
	for key := range prior {
		if _, present := desired[key]; present {
			continue
		}
		if siblingPath, lifted := canonicalHandoff[key]; lifted && presence[siblingPath] == 1 && candidate.Owned[siblingPath] {
			// LiteLLM atomically strips the legacy blob key while applying the
			// explicitly configured canonical column. No other merge-only key
			// deletion is representable in one PUT.
			continue
		}
		return fmt.Errorf("LiteLLM v1.98 merges credential maps; clear credentials first, apply, then re-add the replacement map")
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

func buildMCPFieldDelta(ctx context.Context, plan MCPServerResourceModel, config, state MCPServerResourceModel, committed, candidate mcpFieldOwnership, hydration map[string]interface{}, recoverAcceptedCreate bool) (map[string]interface{}, error) {
	if err := validateMCPFieldCredentialMerge(ctx, plan, state, config, hydration, committed, candidate); err != nil {
		return nil, err
	}
	delta := map[string]interface{}{}
	presence := mcpFieldConfigPresence(config)
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
		if fieldPath == mcpFieldOAuthScopesPath {
			continue
		}
		delta[mcpFieldWireName(fieldPath)] = mcpFieldRemovalSentinel(fieldPath)
	}
	for fieldPath := range candidate.Owned {
		if candidate.Removals[fieldPath] || fieldPath == mcpFieldOAuthScopesPath {
			continue
		}
		// Unknown configuration is not mutation intent. It retains candidate
		// ownership, but neither proposed-state placeholders nor scalar zero
		// values may reach the wire.
		if presence[fieldPath] != 1 {
			continue
		}
		desiredSource := config
		if fieldPath == mcpFieldEnvVarsPath {
			desiredSource.EnvVars = plan.EnvVars
		}
		desired, err := mcpFieldDesiredValue(ctx, desiredSource, fieldPath)
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
				_, validCredentials := desired.(map[string]string)
				if recoverAcceptedCreate && validCredentials {
					// The accepted-create marker is bound to this exact credential
					// class and key set. Re-send every configured value so opaque value
					// changes are applied rather than inferred from masked readback.
					delta[name] = desired
					continue
				}
				replacesCredentialClass := mcpCredentialClassWillReplace(plan, state, hydration) || mcpCredentialOwnershipClassWillReplace(committed, candidate)
				confirmedClear := committed.Removals[fieldPath]
				if !validCredentials || (!replacesCredentialClass && !confirmedClear) {
					return nil, fmt.Errorf("credentials cannot be adopted safely through LiteLLM's merge-only update without a credential-class replacement or a confirmed Terraform clear")
				}
				delta[name] = desired
			} else {
				credentials, validCredentials := desired.(map[string]string)
				if !validCredentials {
					return nil, fmt.Errorf("configured credentials are invalid")
				}
				replacesCredentialClass := mcpCredentialClassWillReplace(plan, state, hydration) || mcpCredentialOwnershipClassWillReplace(committed, candidate)
				if replacesCredentialClass {
					priorCredentials, err := mcpFieldStringMap(ctx, state.Credentials)
					if err != nil {
						return nil, fmt.Errorf("credential-class replacement requires known prior credential state")
					}
					// v1.98 clears omitted auth-flow columns on a class change. Make
					// every previously Terraform-owned lifted-key deletion explicit
					// so preflight and readback can distinguish it from unowned loss.
					for _, liftedName := range mcpCredentialLiftedColumnNames {
						if _, previouslyOwned := priorCredentials[liftedName]; previouslyOwned {
							if _, retained := credentials[liftedName]; !retained {
								delta[liftedName] = nil
							}
						}
					}
				}
				writeCredentials := !config.Credentials.Equal(state.Credentials) || replacesCredentialClass
				for _, liftedName := range mcpCredentialLiftedColumnNames {
					if liftedValue, configured := credentials[liftedName]; configured {
						remote, present := hydration[liftedName]
						writeCredentials = writeCredentials || !present || !mcpWireValuesEqual(liftedValue, remote)
					}
				}
				if upstreamResource, configured := credentials["upstream_resource"]; configured {
					remote, present := mcpObservedCredentialString(hydration, "upstream_resource")
					writeCredentials = writeCredentials || !present || upstreamResource != remote
				}
				if writeCredentials {
					delta[name] = desired
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

	// A lifted-alias deletion is representable in one merge-only PUT only as an
	// explicit handoff to its configured canonical sibling. Force that sibling
	// onto the wire even when authoritative readback is already equal, so the
	// credentials rewrite and column authority are one upstream transaction.
	if !state.Credentials.IsNull() && !state.Credentials.IsUnknown() && !config.Credentials.IsNull() && !config.Credentials.IsUnknown() {
		priorCredentials, priorErr := mcpFieldStringMap(ctx, state.Credentials)
		desiredCredentials, desiredErr := mcpFieldStringMap(ctx, config.Credentials)
		if priorErr != nil || desiredErr != nil {
			return nil, fmt.Errorf("credential handoff requires complete known maps")
		}
		canonicalValues := map[string]types.String{
			"token_exchange_endpoint": config.TokenExchangeEndpoint,
			"audience":                config.Audience,
			"subject_token_type":      config.SubjectTokenType,
			"token_exchange_profile":  config.TokenExchangeProfile,
		}
		for name, canonicalValue := range canonicalValues {
			if _, previouslyLegacy := priorCredentials[name]; !previouslyLegacy {
				continue
			}
			if _, retainedLegacy := desiredCredentials[name]; retainedLegacy || canonicalValue.IsNull() || canonicalValue.IsUnknown() {
				continue
			}
			delta[name] = canonicalValue.ValueString()
		}
	}

	// Scopes share the native credentials object but have independent public
	// ownership. Construct that object only after generic credential delta logic
	// so neither intent can overwrite the other.
	scopesOwned := candidate.Owned[mcpFieldOAuthScopesPath]
	scopesRemoved := candidate.Removals[mcpFieldOAuthScopesPath]
	scopesSatisfiedByCredentialClear := false
	if candidate.Removals[mcpFieldCredentialsPath] && scopesOwned {
		scopes, err := mcpFieldStringList(ctx, config.OAuthScopes)
		if err != nil || len(scopes) != 0 {
			return nil, fmt.Errorf("credentials cannot be cleared while configured OAuth scopes must be retained")
		}
		// credentials=null clears every blob member, so it exactly satisfies an
		// explicitly configured empty scope list without a second mutation.
		scopesSatisfiedByCredentialClear = true
	}
	if scopesRemoved {
		if !candidate.Removals[mcpFieldCredentialsPath] {
			if err := mergeMCPScopesCredentialIntent(delta, []string{}); err != nil {
				return nil, err
			}
		}
	} else if scopesOwned && !scopesSatisfiedByCredentialClear && presence[mcpFieldOAuthScopesPath] == 1 {
		scopes, err := mcpFieldStringList(ctx, config.OAuthScopes)
		if err != nil {
			return nil, err
		}
		writeScopes := recoverAcceptedCreate || !committed.Owned[mcpFieldOAuthScopesPath] ||
			!config.OAuthScopes.Equal(state.OAuthScopes) || mcpCredentialClassWillReplace(plan, state, hydration)
		if writeScopes {
			if err := mergeMCPScopesCredentialIntent(delta, scopes); err != nil {
				return nil, err
			}
		}
	}
	if err := liftMCPTokenExchangeCredentialAliases(delta); err != nil {
		return nil, err
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
		{name: "server_name", value: plan.ServerName, priorValue: state.ServerName, nullable: true},
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
	if serverName, serverNameSent := delta["server_name"]; serverNameSent {
		if serverName == nil {
			// Clearing both values is valid: LiteLLM falls back to server_id.
			if alias, aliasSent := delta["alias"]; aliasSent && alias != nil {
				aliasString, ok := alias.(string)
				if !ok || !mcpToolPrefixValidV198(aliasString) {
					return fmt.Errorf("alias intent cannot converge with a server name clear")
				}
			}
		} else if alias, aliasSent := delta["alias"]; aliasSent {
			aliasString, ok := alias.(string)
			if !ok || aliasString == "" || !mcpToolPrefixValidV198(aliasString) {
				return fmt.Errorf("alias intent cannot converge with a server name change")
			}
		} else if alias, ok := hydration["alias"].(string); ok && alias != "" {
			if !mcpToolPrefixValidV198(alias) {
				return fmt.Errorf("authoritative alias is invalid for a server name change")
			}
			delta["alias"] = alias
		} else {
			name, ok := serverName.(string)
			if !ok || !mcpToolPrefixValidV198(name) {
				return fmt.Errorf("server name intent cannot provide an alias fallback")
			}
			delta["alias"] = mcpNormalizeAliasV198(name)
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
	if !planned.ServerName.IsNull() && !planned.ServerName.IsUnknown() {
		intent["server_name"] = planned.ServerName.ValueString()
	}
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
		if fieldPath == mcpFieldOAuthScopesPath {
			// credentials.scopes is deliberately not projected by LiteLLM v1.98.
			continue
		}
		if fieldPath == mcpFieldEnvVarsPath {
			sent, changed := delta[name]
			if changed || (candidate.Owned[fieldPath] && !committed.Owned[fieldPath]) {
				want := sent
				if !changed {
					var err error
					desiredSource := config
					desiredSource.EnvVars = plan.EnvVars
					want, err = mcpFieldDesiredValue(ctx, desiredSource, fieldPath)
					if err != nil {
						return fmt.Errorf("MCP environment-variable intent is invalid")
					}
				}
				got, presence, err := canonicalMCPEnvVarsAPIWire(observed)
				if err != nil || presence != apiValuePresent || !mcpWireValuesEqual(want, got) {
					return fmt.Errorf("MCP environment-variable readback did not converge")
				}
				continue
			}
			if committed.Owned[fieldPath] || candidate.Owned[fieldPath] {
				continue
			}
			prior, priorPresence, priorErr := canonicalMCPEnvVarsAPIWire(baseline)
			if priorErr != nil || priorPresence != apiValuePresent {
				continue
			}
			got, presence, err := canonicalMCPEnvVarsAPIWire(observed)
			if err != nil || presence != apiValuePresent || !mcpWireValuesEqual(prior, got) {
				return fmt.Errorf("visible unmanaged MCP environment variables changed")
			}
			continue
		}
		if sent, changed := delta[name]; changed {
			if fieldPath == mcpFieldCredentialsPath {
				// A native scopes-only mutation necessarily uses the credentials
				// object without creating generic credential ownership. Do not ask a
				// null generic map to confirm that write; scopes remain unobservable.
				genericCredentialIntent := candidate.Owned[mcpFieldCredentialsPath] || candidate.Removals[mcpFieldCredentialsPath]
				if genericCredentialIntent && sent != nil && verifyMCPObservableCredentialReadback(ctx, config.Credentials, observed) != nil {
					return fmt.Errorf("observable credential configuration did not converge")
				}
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
	if bindingErr := validateMCPFieldOwnershipGeneration(state.FieldOwnershipGeneration, committedFields); bindingErr != nil {
		mcpFieldPrivateError(&resp.Diagnostics)
	}
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
	acceptedCreateRecovery, acceptedCreateDiags := readMCPAcceptedCreateRecovery(ctx, req.Private, expectedFields)
	resp.Diagnostics.Append(acceptedCreateDiags...)
	if resp.Diagnostics.HasError() {
		resp.State, resp.Private = req.State, req.Private
		return
	}
	if mcpAliasUpdateIntentCannotConverge(config.Alias) {
		resp.State, resp.Private = req.State, req.Private
		resp.Diagnostics.AddError("Invalid MCP Alias Configuration", "Configured alias cannot converge safely. No update was attempted; prior public and private state was retained.")
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
	recoverAcceptedCreate := acceptedCreateRecovery && !state.FieldOwnershipGeneration.IsNull() && !state.FieldOwnershipGeneration.IsUnknown() && state.FieldOwnershipGeneration.ValueInt64() == 0 && committedFields.Generation == 0 && len(committedFields.Owned) == 0 && len(committedFields.Removals) == 0
	delta, err := buildMCPFieldDelta(ctx, plan, config, state, committedFields, plannedFields, hydration, recoverAcceptedCreate)
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
	remoteIssuer := ""
	if raw, present := hydration["issuer"].(string); present {
		remoteIssuer = strings.TrimSpace(raw)
	} else if !state.Issuer.IsNull() && !state.Issuer.IsUnknown() {
		remoteIssuer = strings.TrimSpace(state.Issuer.ValueString())
	}
	issuerChanged := false
	if desired, sent := delta["issuer"]; sent && remoteIssuer != "" {
		desiredIssuer := ""
		if value, ok := desired.(string); ok {
			desiredIssuer = strings.TrimSpace(value)
		}
		issuerChanged = desiredIssuer != remoteIssuer
	}
	if err := validateMCPImplicitClearSafety(config, state, plannedFields, hydration, delta, urlChanged, authClassChanged, issuerChanged); err != nil {
		resp.State, resp.Private = req.State, req.Private
		resp.Diagnostics.AddError("Unsafe MCP URL or Authentication Update", "LiteLLM v1.98 would implicitly clear an unowned, unknown, or unchanged OAuth/credential value ("+err.Error()+"). Configure every affected value with a genuinely changed or cleared complete intent in one apply. No PUT was attempted; restorative PUTs are never used.")
		return
	}
	if config.Alias.IsNull() {
		if canonicalAlias, ok := delta["alias"].(string); ok {
			plan.Alias = types.StringValue(canonicalAlias)
		}
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
		accepted, putErr := r.putMCPServer(ctx, delta, &updateResult)
		if putErr != nil && !accepted {
			resp.State, resp.Private = req.State, req.Private
			resp.Diagnostics.AddError("Client Error", "LiteLLM did not confirm the MCP server update. Prior public and private state was retained.")
			return
		}
		// Accepted response-body failures and malformed success bodies are
		// reconciled by the mandatory identity-valid direct read below.
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
