package provider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	mcpFieldOwnershipPrivateKey        = "mcp_field_ownership_v1"
	mcpFieldPendingOwnershipPrivateKey = "mcp_field_pending_ownership_v1"
	mcpFieldImportedPrivateKey         = "mcp_field_imported_v1"
	mcpFieldOwnershipVersion           = 1
)

const (
	mcpFieldAliasPath            = "/alias"
	mcpFieldDescriptionPath      = "/description"
	mcpFieldCommandPath          = "/command"
	mcpFieldAuthorizationURLPath = "/authorization_url"
	mcpFieldTokenURLPath         = "/token_url"
	mcpFieldRegistrationURLPath  = "/registration_url"
	mcpFieldAccessGroupsPath     = "/mcp_access_groups"
	mcpFieldArgsPath             = "/args"
	mcpFieldEnvPath              = "/env"
	mcpFieldAllowedToolsPath     = "/allowed_tools"
	mcpFieldExtraHeadersPath     = "/extra_headers"
	mcpFieldStaticHeadersPath    = "/static_headers"
	mcpFieldCredentialsPath      = "/credentials"
	mcpFieldAllowAllKeysPath     = "/allow_all_keys"
)

var mcpFieldPaths = []string{
	mcpFieldAliasPath,
	mcpFieldAllowAllKeysPath,
	mcpFieldAllowedToolsPath,
	mcpFieldArgsPath,
	mcpFieldAuthorizationURLPath,
	mcpFieldCommandPath,
	mcpFieldCredentialsPath,
	mcpFieldDescriptionPath,
	mcpFieldEnvPath,
	mcpFieldExtraHeadersPath,
	mcpFieldAccessGroupsPath,
	mcpFieldRegistrationURLPath,
	mcpFieldStaticHeadersPath,
	mcpFieldTokenURLPath,
}

var mcpFieldAllowedPaths = func() map[string]bool {
	result := make(map[string]bool, len(mcpFieldPaths))
	for _, fieldPath := range mcpFieldPaths {
		result[fieldPath] = true
	}
	return result
}()

type mcpFieldOwnershipWire struct {
	Version         int      `json:"version"`
	Generation      int64    `json:"generation"`
	OwnedPaths      []string `json:"owned_paths"`
	Removals        []string `json:"removals"`
	CredentialClass string   `json:"credential_class"`
	CredentialKeys  []string `json:"credential_keys"`
	IntentDigest    string   `json:"intent_digest"`
}

type mcpFieldOwnership struct {
	Owned           map[string]bool
	Removals        map[string]bool
	CredentialClass string
	CredentialKeys  []string
	Generation      int64
	Versioned       bool
}

func emptyMCPFieldOwnership() mcpFieldOwnership {
	return mcpFieldOwnership{Owned: map[string]bool{}, Removals: map[string]bool{}}
}

func cloneMCPFieldSet(source map[string]bool) map[string]bool {
	result := make(map[string]bool, len(source))
	for name, present := range source {
		if present {
			result[name] = true
		}
	}
	return result
}

func cloneMCPFieldOwnership(source mcpFieldOwnership) mcpFieldOwnership {
	result := source
	result.Owned = cloneMCPFieldSet(source.Owned)
	result.Removals = cloneMCPFieldSet(source.Removals)
	result.CredentialKeys = slices.Clone(source.CredentialKeys)
	return result
}

func canonicalMCPFieldPaths(fields map[string]bool) []string {
	result := make([]string, 0, len(fields))
	for name, present := range fields {
		if present {
			result = append(result, name)
		}
	}
	slices.Sort(result)
	return result
}

func mcpFieldIntentDigest(version int, generation int64, owned, removals []string, credentialClass string, credentialKeys []string) string {
	intent := struct {
		Version         int      `json:"version"`
		Generation      int64    `json:"generation"`
		Owned           []string `json:"owned_paths"`
		Removals        []string `json:"removals"`
		CredentialClass string   `json:"credential_class"`
		CredentialKeys  []string `json:"credential_keys"`
	}{version, generation, owned, removals, credentialClass, credentialKeys}
	raw, _ := json.Marshal(intent)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func encodeMCPFieldOwnership(ownership mcpFieldOwnership) []byte {
	owned := canonicalMCPFieldPaths(ownership.Owned)
	removals := canonicalMCPFieldPaths(ownership.Removals)
	credentialKeys := slices.Clone(ownership.CredentialKeys)
	if credentialKeys == nil {
		credentialKeys = []string{}
	}
	slices.Sort(credentialKeys)
	wire := mcpFieldOwnershipWire{
		Version:         mcpFieldOwnershipVersion,
		Generation:      ownership.Generation,
		OwnedPaths:      owned,
		Removals:        removals,
		CredentialClass: ownership.CredentialClass,
		CredentialKeys:  credentialKeys,
		IntentDigest:    mcpFieldIntentDigest(mcpFieldOwnershipVersion, ownership.Generation, owned, removals, ownership.CredentialClass, credentialKeys),
	}
	raw, _ := json.Marshal(wire)
	return raw
}

func decodeMCPFieldOwnership(raw []byte) (mcpFieldOwnership, error) {
	if raw == nil {
		return emptyMCPFieldOwnership(), fmt.Errorf("absent")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var wire mcpFieldOwnershipWire
	if err := decoder.Decode(&wire); err != nil || decoder.More() {
		return mcpFieldOwnership{}, fmt.Errorf("malformed MCP field ownership")
	}
	if wire.Version != mcpFieldOwnershipVersion || wire.Generation < 0 || wire.OwnedPaths == nil || wire.Removals == nil || wire.CredentialKeys == nil {
		return mcpFieldOwnership{}, fmt.Errorf("malformed MCP field ownership")
	}
	validate := func(paths []string) (map[string]bool, error) {
		result := make(map[string]bool, len(paths))
		for index, fieldPath := range paths {
			if !mcpFieldAllowedPaths[fieldPath] || result[fieldPath] || (index > 0 && paths[index-1] >= fieldPath) {
				return nil, fmt.Errorf("malformed MCP field ownership paths")
			}
			result[fieldPath] = true
		}
		return result, nil
	}
	owned, err := validate(wire.OwnedPaths)
	if err != nil {
		return mcpFieldOwnership{}, err
	}
	removals, err := validate(wire.Removals)
	if err != nil {
		return mcpFieldOwnership{}, err
	}
	for fieldPath := range removals {
		if owned[fieldPath] {
			return mcpFieldOwnership{}, fmt.Errorf("MCP field ownership overlaps removals")
		}
	}
	if wire.CredentialClass == "" {
		if len(wire.CredentialKeys) != 0 {
			return mcpFieldOwnership{}, fmt.Errorf("malformed MCP credential ownership")
		}
	} else {
		validClass := false
		for _, authType := range mcpAuthTypesV198 {
			if mcpAuthCredentialClass(authType) == wire.CredentialClass {
				validClass = true
				break
			}
		}
		if !validClass || (!owned[mcpFieldCredentialsPath] && !removals[mcpFieldCredentialsPath]) {
			return mcpFieldOwnership{}, fmt.Errorf("malformed MCP credential ownership")
		}
	}
	for index, key := range wire.CredentialKeys {
		if !mcpCredentialStringKeysV198[key] || (index > 0 && wire.CredentialKeys[index-1] >= key) {
			return mcpFieldOwnership{}, fmt.Errorf("malformed MCP credential ownership keys")
		}
	}
	if removals[mcpFieldCredentialsPath] && len(wire.CredentialKeys) != 0 {
		return mcpFieldOwnership{}, fmt.Errorf("cleared MCP credentials retain key ownership")
	}
	if len(wire.IntentDigest) != sha256.Size*2 {
		return mcpFieldOwnership{}, fmt.Errorf("malformed MCP field ownership digest")
	}
	if _, err := hex.DecodeString(wire.IntentDigest); err != nil || wire.IntentDigest != mcpFieldIntentDigest(wire.Version, wire.Generation, wire.OwnedPaths, wire.Removals, wire.CredentialClass, wire.CredentialKeys) {
		return mcpFieldOwnership{}, fmt.Errorf("MCP field ownership digest mismatch")
	}
	result := mcpFieldOwnership{Owned: owned, Removals: removals, CredentialClass: wire.CredentialClass, CredentialKeys: wire.CredentialKeys, Generation: wire.Generation, Versioned: true}
	if !bytes.Equal(raw, encodeMCPFieldOwnership(result)) {
		return mcpFieldOwnership{}, fmt.Errorf("non-canonical MCP field ownership")
	}
	return result, nil
}

func mcpFieldPrivateError(diagnostics *diag.Diagnostics) {
	diagnostics.AddError("Invalid MCP Field Ownership State", "Provider-private MCP field ownership data is malformed, non-canonical, unsupported, or does not match its exact intent digest. Prior public and private state was retained; no remote operation was attempted.")
}

func readMCPFieldOwnership(ctx context.Context, private mcpInfoPrivateReader) (mcpFieldOwnership, diag.Diagnostics) {
	result := emptyMCPFieldOwnership()
	var diagnostics diag.Diagnostics
	if private == nil {
		return result, diagnostics
	}
	raw, keyDiags := private.GetKey(ctx, mcpFieldOwnershipPrivateKey)
	diagnostics.Append(keyDiags...)
	pendingRaw, pendingDiags := private.GetKey(ctx, mcpFieldPendingOwnershipPrivateKey)
	diagnostics.Append(pendingDiags...)
	importedRaw, importedDiags := private.GetKey(ctx, mcpFieldImportedPrivateKey)
	diagnostics.Append(importedDiags...)
	if diagnostics.HasError() {
		return result, diagnostics
	}
	if importedRaw != nil && string(importedRaw) != "true" {
		mcpFieldPrivateError(&diagnostics)
		return result, diagnostics
	}
	if pendingRaw != nil {
		if _, err := decodeMCPFieldOwnership(pendingRaw); err != nil {
			mcpFieldPrivateError(&diagnostics)
			return result, diagnostics
		}
	}
	if raw == nil {
		return result, diagnostics
	}
	decoded, err := decodeMCPFieldOwnership(raw)
	if err != nil {
		mcpFieldPrivateError(&diagnostics)
		return result, diagnostics
	}
	return decoded, diagnostics
}

func mcpFieldPrivateHasPending(ctx context.Context, private mcpInfoPrivateReader) (bool, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if private == nil {
		return false, diagnostics
	}
	raw, keyDiags := private.GetKey(ctx, mcpFieldPendingOwnershipPrivateKey)
	diagnostics.Append(keyDiags...)
	if diagnostics.HasError() || raw == nil {
		return false, diagnostics
	}
	if _, err := decodeMCPFieldOwnership(raw); err != nil {
		mcpFieldPrivateError(&diagnostics)
		return false, diagnostics
	}
	return true, diagnostics
}

func readPendingMCPFieldOwnership(ctx context.Context, private mcpInfoPrivateReader, expected mcpFieldOwnership) (mcpFieldOwnership, diag.Diagnostics) {
	result := cloneMCPFieldOwnership(expected)
	var diagnostics diag.Diagnostics
	if private == nil {
		return result, diagnostics
	}
	raw, keyDiags := private.GetKey(ctx, mcpFieldPendingOwnershipPrivateKey)
	diagnostics.Append(keyDiags...)
	if diagnostics.HasError() || raw == nil {
		return result, diagnostics
	}
	decoded, err := decodeMCPFieldOwnership(raw)
	if err != nil || !mcpFieldOwnershipEqual(decoded, expected) {
		mcpFieldPrivateError(&diagnostics)
		return result, diagnostics
	}
	return decoded, diagnostics
}

func writeMCPFieldOwnership(ctx context.Context, private mcpInfoPrivateWriter, ownership mcpFieldOwnership) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if private == nil {
		return diagnostics
	}
	ownership.Versioned = true
	diagnostics.Append(private.SetKey(ctx, mcpFieldOwnershipPrivateKey, encodeMCPFieldOwnership(ownership))...)
	if diagnostics.HasError() {
		return diagnostics
	}
	diagnostics.Append(private.SetKey(ctx, mcpFieldPendingOwnershipPrivateKey, nil)...)
	return diagnostics
}

func writePendingMCPFieldOwnership(ctx context.Context, private mcpInfoPrivateWriter, ownership mcpFieldOwnership) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if private == nil {
		return diagnostics
	}
	ownership.Versioned = true
	diagnostics.Append(private.SetKey(ctx, mcpFieldPendingOwnershipPrivateKey, encodeMCPFieldOwnership(ownership))...)
	return diagnostics
}

func mcpFieldSetsEqual(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for name := range left {
		if !right[name] {
			return false
		}
	}
	return true
}

func mcpFieldOwnershipEqual(left, right mcpFieldOwnership) bool {
	return left.Generation == right.Generation && left.CredentialClass == right.CredentialClass && slices.Equal(left.CredentialKeys, right.CredentialKeys) && mcpFieldSetsEqual(left.Owned, right.Owned) && mcpFieldSetsEqual(left.Removals, right.Removals)
}

// 0 is absent, 1 is known-present, and 2 is unknown. Presence is taken only
// from req.Config; proposed state and public state are never ownership proof.
func mcpFieldConfigPresence(config MCPServerResourceModel) map[string]int {
	result := make(map[string]int, len(mcpFieldPaths))
	set := func(fieldPath string, value attr.Value) {
		if value.IsUnknown() {
			result[fieldPath] = 2
		} else if !value.IsNull() {
			result[fieldPath] = 1
		}
	}
	set(mcpFieldAliasPath, config.Alias)
	set(mcpFieldDescriptionPath, config.Description)
	set(mcpFieldCommandPath, config.Command)
	set(mcpFieldAuthorizationURLPath, config.AuthorizationURL)
	set(mcpFieldTokenURLPath, config.TokenURL)
	set(mcpFieldRegistrationURLPath, config.RegistrationURL)
	set(mcpFieldAccessGroupsPath, config.MCPAccessGroups)
	set(mcpFieldArgsPath, config.Args)
	set(mcpFieldEnvPath, config.Env)
	set(mcpFieldAllowedToolsPath, config.AllowedTools)
	set(mcpFieldExtraHeadersPath, config.ExtraHeaders)
	set(mcpFieldStaticHeadersPath, config.StaticHeaders)
	set(mcpFieldCredentialsPath, config.Credentials)
	set(mcpFieldAllowAllKeysPath, config.AllowAllKeys)
	return result
}

func deriveMCPFieldPlanOwnership(prior mcpFieldOwnership, config MCPServerResourceModel) mcpFieldOwnership {
	result := cloneMCPFieldOwnership(prior)
	result.Versioned = true
	presence := mcpFieldConfigPresence(config)
	legacy := !prior.Versioned
	if legacy {
		result = emptyMCPFieldOwnership()
		result.Versioned = true
	}
	for _, fieldPath := range mcpFieldPaths {
		switch presence[fieldPath] {
		case 1:
			result.Owned[fieldPath] = true
			delete(result.Removals, fieldPath)
		case 0:
			if !legacy && prior.Owned[fieldPath] {
				delete(result.Owned, fieldPath)
				result.Removals[fieldPath] = true
			}
		case 2:
			// Unknown configuration retains exactly the prior owner.
		}
	}
	if result.Owned[mcpFieldCredentialsPath] && presence[mcpFieldCredentialsPath] == 1 {
		result.CredentialKeys = result.CredentialKeys[:0]
		for key := range config.Credentials.Elements() {
			result.CredentialKeys = append(result.CredentialKeys, key)
		}
		slices.Sort(result.CredentialKeys)
		if !config.AuthType.IsNull() && !config.AuthType.IsUnknown() {
			result.CredentialClass = mcpAuthCredentialClass(config.AuthType.ValueString())
		}
	} else if result.Removals[mcpFieldCredentialsPath] {
		result.CredentialKeys = []string{}
		if !config.AuthType.IsNull() && !config.AuthType.IsUnknown() {
			result.CredentialClass = mcpAuthCredentialClass(config.AuthType.ValueString())
		}
	} else if !result.Owned[mcpFieldCredentialsPath] {
		result.CredentialClass = ""
		result.CredentialKeys = []string{}
	}
	if !mcpFieldSetsEqual(prior.Owned, result.Owned) || !mcpFieldSetsEqual(prior.Removals, result.Removals) || prior.CredentialClass != result.CredentialClass || !slices.Equal(prior.CredentialKeys, result.CredentialKeys) {
		result.Generation = prior.Generation + 1
	}
	return result
}

func committedMCPFieldOwnership(candidate mcpFieldOwnership) mcpFieldOwnership {
	result := cloneMCPFieldOwnership(candidate)
	result.Versioned = true
	return result
}

func validateMCPFieldOwnershipGeneration(generation types.Int64, ownership mcpFieldOwnership) error {
	if generation.IsNull() || generation.IsUnknown() {
		return nil
	}
	value := generation.ValueInt64()
	if value < 0 || value != ownership.Generation || (value > 0 && !ownership.Versioned) {
		return fmt.Errorf("MCP field ownership generation mismatch")
	}
	return nil
}

func mcpFieldGenerationValue(ownership mcpFieldOwnership) types.Int64 {
	return types.Int64Value(ownership.Generation)
}

func mcpFieldStringMap(ctx context.Context, value types.Map) (map[string]string, error) {
	if value.IsNull() || value.IsUnknown() {
		return nil, fmt.Errorf("unknown or null string map")
	}
	result := map[string]string{}
	if diagnostics := value.ElementsAs(ctx, &result, false); diagnostics.HasError() {
		return nil, fmt.Errorf("invalid string map")
	}
	return result, nil
}

func mcpFieldStringList(ctx context.Context, value types.List) ([]string, error) {
	if value.IsNull() || value.IsUnknown() {
		return nil, fmt.Errorf("unknown or null string list")
	}
	result := []string{}
	if diagnostics := value.ElementsAs(ctx, &result, false); diagnostics.HasError() {
		return nil, fmt.Errorf("invalid string list")
	}
	return result, nil
}
