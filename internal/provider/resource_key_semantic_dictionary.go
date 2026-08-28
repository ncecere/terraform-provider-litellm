package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	keyMetadataJSONProvenancePrivateKey    = "key_metadata_json_provenance_v1"
	keyConfigJSONProvenancePrivateKey      = "key_config_json_provenance_v1"
	keyPermissionsJSONProvenancePrivateKey = "key_permissions_json_provenance_v1"
	keyAcceptedCreateRecoveryPrivateKey    = "key_semantic_create_accepted_v1"
	keyPendingUpdatePrivateKey             = "key_semantic_update_pending_v1"
)

type keySemanticPrepared struct {
	metadataObject        map[string]interface{}
	configObject          map[string]interface{}
	permissionsObject     map[string]interface{}
	metadataProvenance    semanticDictionaryProvenance
	configProvenance      semanticDictionaryProvenance
	permissionsProvenance semanticDictionaryProvenance
}

type keySemanticReadOwnership struct {
	metadata                      semanticDictionaryProvenance
	config                        semanticDictionaryProvenance
	permissions                   semanticDictionaryProvenance
	metadataRemovals              semanticDictionaryPathSet
	configRemovals                semanticDictionaryPathSet
	permissionsRemovals           semanticDictionaryPathSet
	metadataTransitionRemovals    semanticDictionaryPathSet
	configTransitionRemovals      semanticDictionaryPathSet
	permissionsTransitionRemovals semanticDictionaryPathSet
	pending                       keySemanticPendingTransition
	reconcile                     *keySemanticPendingReconcile
	acceptedCreateRecovery        bool
	fresh                         bool
}

type keySemanticPendingRoot struct {
	Active         bool
	Configured     bool
	TerraformOwned semanticDictionaryPathSet
	Removals       semanticDictionaryPathSet
}

type keySemanticPendingRootWire struct {
	Active         bool     `json:"active"`
	Configured     bool     `json:"configured"`
	TerraformOwned []string `json:"terraform_owned"`
	Removals       []string `json:"removals"`
}

type keySemanticPendingTransition struct {
	Metadata    keySemanticPendingRoot
	Config      keySemanticPendingRoot
	Permissions keySemanticPendingRoot
}

type keySemanticPendingTransitionWire struct {
	Version     int                        `json:"version"`
	Metadata    keySemanticPendingRootWire `json:"metadata"`
	Config      keySemanticPendingRootWire `json:"config"`
	Permissions keySemanticPendingRootWire `json:"permissions"`
}

type keySemanticPendingReconcile struct {
	Committed bool
	Present   bool
	Effective keySemanticReadOwnership
}

var keyMetadataJSONReservedKeys = []string{
	"service_account_id",
	"model_rpm_limit",
	"model_tpm_limit",
	"guardrails",
	"prompts",
	"enforced_params",
	"tags",
	"allowed_passthrough_routes",
	"rpm_limit_type",
	"tpm_limit_type",
}

type keySemanticDictionaryValidator struct{}

var _ validator.String = keySemanticDictionaryValidator{}

func (keySemanticDictionaryValidator) Description(context.Context) string {
	return "Value must be one bounded, non-null JSON object with unique members and persistable numbers."
}

func (v keySemanticDictionaryValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (keySemanticDictionaryValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	object, err := parseSemanticDictionary(ctx, req.ConfigValue.ValueString())
	if err == nil {
		err = validateKeySemanticDictionaryPersistence(ctx, object)
	}
	if err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Semantic JSON Object", "The value must be one bounded, non-null JSON object with unique members and numbers that LiteLLM can persist without changing their value.")
	}
}

func prepareKeySemanticDictionaries(ctx context.Context, data KeyResourceModel) (keySemanticPrepared, error) {
	var result keySemanticPrepared
	var err error
	result.metadataObject, result.metadataProvenance, err = keySemanticDictionaryConfiguration(ctx, data.MetadataJSON, data.Metadata, keyMetadataJSONReservedKeys)
	if err != nil {
		return keySemanticPrepared{}, err
	}
	result.configObject, result.configProvenance, err = keySemanticDictionaryConfiguration(ctx, data.ConfigJSON, data.Config, nil)
	if err != nil {
		return keySemanticPrepared{}, err
	}
	result.permissionsObject, result.permissionsProvenance, err = keySemanticDictionaryConfiguration(ctx, data.PermissionsJSON, data.Permissions, nil)
	if err != nil {
		return keySemanticPrepared{}, err
	}
	return result, nil
}

func (p keySemanticPrepared) ownership(fresh bool) keySemanticReadOwnership {
	return keySemanticReadOwnership{
		metadata:            p.metadataProvenance,
		config:              p.configProvenance,
		permissions:         p.permissionsProvenance,
		metadataRemovals:    semanticDictionaryPathSet{},
		configRemovals:      semanticDictionaryPathSet{},
		permissionsRemovals: semanticDictionaryPathSet{},
		fresh:               fresh,
	}
}

func (p keySemanticPrepared) updateOwnership(ctx context.Context, prior keySemanticReadOwnership) (keySemanticReadOwnership, error) {
	metadataRemovals, err := keySemanticDictionaryRemovedPaths(ctx, prior.metadata.TerraformOwned, p.metadataProvenance.TerraformOwned)
	if err != nil {
		return keySemanticReadOwnership{}, err
	}
	configRemovals, err := keySemanticDictionaryRemovedPaths(ctx, prior.config.TerraformOwned, p.configProvenance.TerraformOwned)
	if err != nil {
		return keySemanticReadOwnership{}, err
	}
	permissionsRemovals, err := keySemanticDictionaryRemovedPaths(ctx, prior.permissions.TerraformOwned, p.permissionsProvenance.TerraformOwned)
	if err != nil {
		return keySemanticReadOwnership{}, err
	}
	metadataProjectionRemovals, err := keySemanticProjectionRemovals(ctx, p.metadataProvenance.TerraformOwned, metadataRemovals)
	if err != nil {
		return keySemanticReadOwnership{}, err
	}
	configProjectionRemovals, err := keySemanticProjectionRemovals(ctx, p.configProvenance.TerraformOwned, configRemovals)
	if err != nil {
		return keySemanticReadOwnership{}, err
	}
	permissionsProjectionRemovals, err := keySemanticProjectionRemovals(ctx, p.permissionsProvenance.TerraformOwned, permissionsRemovals)
	if err != nil {
		return keySemanticReadOwnership{}, err
	}
	return keySemanticReadOwnership{
		metadata: p.metadataProvenance, config: p.configProvenance, permissions: p.permissionsProvenance,
		metadataRemovals: metadataProjectionRemovals, configRemovals: configProjectionRemovals, permissionsRemovals: permissionsProjectionRemovals,
		metadataTransitionRemovals: metadataRemovals, configTransitionRemovals: configRemovals, permissionsTransitionRemovals: permissionsRemovals,
		fresh: true,
	}, nil
}

func pendingKeySemanticTransition(ownership keySemanticReadOwnership) keySemanticPendingTransition {
	root := func(provenance semanticDictionaryProvenance, removals semanticDictionaryPathSet) keySemanticPendingRoot {
		if len(removals) == 0 {
			return keySemanticPendingRoot{}
		}
		return keySemanticPendingRoot{
			Active:         true,
			Configured:     provenance.Configured,
			TerraformOwned: provenance.TerraformOwned,
			Removals:       removals,
		}
	}
	return keySemanticPendingTransition{
		Metadata:    root(ownership.metadata, ownership.metadataTransitionRemovals),
		Config:      root(ownership.config, ownership.configTransitionRemovals),
		Permissions: root(ownership.permissions, ownership.permissionsTransitionRemovals),
	}
}

func (p keySemanticPendingTransition) any() bool {
	return p.Metadata.Active || p.Config.Active || p.Permissions.Active
}

func encodeKeySemanticPendingTransition(ctx context.Context, pending keySemanticPendingTransition) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root := func(value keySemanticPendingRoot) (keySemanticPendingRootWire, error) {
		if !value.Active {
			if value.Configured || len(value.TerraformOwned) != 0 || len(value.Removals) != 0 {
				return keySemanticPendingRootWire{}, errSemanticDictionaryPrivate
			}
			return keySemanticPendingRootWire{}, nil
		}
		if len(value.Removals) == 0 || validateSemanticDictionaryPathSet(ctx, value.TerraformOwned) != nil || validateSemanticDictionaryPathSet(ctx, value.Removals) != nil {
			return keySemanticPendingRootWire{}, errSemanticDictionaryPrivate
		}
		if !value.Configured && len(value.TerraformOwned) != 0 {
			return keySemanticPendingRootWire{}, errSemanticDictionaryPrivate
		}
		return keySemanticPendingRootWire{
			Active: value.Active, Configured: value.Configured,
			TerraformOwned: sortedSemanticDictionaryPaths(value.TerraformOwned),
			Removals:       sortedSemanticDictionaryPaths(value.Removals),
		}, nil
	}
	metadata, err := root(pending.Metadata)
	if err != nil {
		return nil, err
	}
	config, err := root(pending.Config)
	if err != nil {
		return nil, err
	}
	permissions, err := root(pending.Permissions)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(keySemanticPendingTransitionWire{Version: 1, Metadata: metadata, Config: config, Permissions: permissions})
	if err != nil || len(encoded) > jsonDecodeMaxInputBytes {
		return nil, errSemanticDictionaryPrivate
	}
	return encoded, nil
}

func decodeKeySemanticPendingTransition(ctx context.Context, raw []byte) (keySemanticPendingTransition, error) {
	if len(raw) == 0 {
		return keySemanticPendingTransition{}, nil
	}
	if err := ctx.Err(); err != nil {
		return keySemanticPendingTransition{}, err
	}
	var wire keySemanticPendingTransitionWire
	if len(raw) > jsonDecodeMaxInputBytes || decodeJSONUseNumber(raw, &wire) != nil || wire.Version != 1 {
		return keySemanticPendingTransition{}, errSemanticDictionaryPrivate
	}
	root := func(value keySemanticPendingRootWire) (keySemanticPendingRoot, error) {
		if !value.Active {
			if value.Configured || len(value.TerraformOwned) != 0 || len(value.Removals) != 0 {
				return keySemanticPendingRoot{}, errSemanticDictionaryPrivate
			}
			return keySemanticPendingRoot{}, nil
		}
		owned, err := semanticDictionaryPathSetFromSlice(ctx, value.TerraformOwned)
		if err != nil {
			return keySemanticPendingRoot{}, errSemanticDictionaryPrivate
		}
		removals, err := semanticDictionaryPathSetFromSlice(ctx, value.Removals)
		if err != nil {
			return keySemanticPendingRoot{}, errSemanticDictionaryPrivate
		}
		return keySemanticPendingRoot{Active: value.Active, Configured: value.Configured, TerraformOwned: owned, Removals: removals}, nil
	}
	pending := keySemanticPendingTransition{}
	var err error
	if pending.Metadata, err = root(wire.Metadata); err != nil {
		return keySemanticPendingTransition{}, err
	}
	if pending.Config, err = root(wire.Config); err != nil {
		return keySemanticPendingTransition{}, err
	}
	if pending.Permissions, err = root(wire.Permissions); err != nil {
		return keySemanticPendingTransition{}, err
	}
	canonical, err := encodeKeySemanticPendingTransition(ctx, pending)
	if err != nil || !pending.any() || !bytes.Equal(canonical, raw) {
		return keySemanticPendingTransition{}, errSemanticDictionaryPrivate
	}
	return pending, nil
}

func keySemanticDictionaryRemovedPaths(ctx context.Context, prior, next semanticDictionaryPathSet) (semanticDictionaryPathSet, error) {
	if err := validateSemanticDictionaryPathSet(ctx, prior); err != nil {
		return nil, errSemanticDictionaryPrivate
	}
	if err := validateSemanticDictionaryPathSet(ctx, next); err != nil {
		return nil, errSemanticDictionaryPrivate
	}
	removed := semanticDictionaryPathSet{}
	for priorPointer := range prior {
		if !next[priorPointer] {
			// Prefix-related but non-identical pointers represent an atomic shape
			// replacement (object expansion or contraction), not continuity of the
			// prior leaf.
			removed[priorPointer] = true
		}
	}
	return removed, nil
}

func verifyKeySemanticDictionaryRemovals(ctx context.Context, observed map[string]interface{}, removals semanticDictionaryPathSet) error {
	if removals == nil {
		return nil
	}
	if err := validateSemanticDictionaryPathSet(ctx, removals); err != nil {
		return errSemanticDictionaryPrivate
	}
	for pointer := range removals {
		members, err := decodeSemanticDictionaryPointer(pointer)
		if err != nil {
			return errSemanticDictionaryPrivate
		}
		var current interface{} = observed
		present := true
		for _, member := range members {
			object, ok := current.(map[string]interface{})
			if !ok {
				present = false
				break
			}
			current, present = object[member]
			if !present {
				break
			}
		}
		if present {
			return errSemanticDictionaryTraversal
		}
	}
	return nil
}

func keyHasNonSemanticConfigurationChange(config, state KeyResourceModel) bool {
	for _, value := range []struct{ config, state types.String }{
		{config.UserID, state.UserID}, {config.TeamID, state.TeamID},
		{config.OrganizationID, state.OrganizationID}, {config.ProjectID, state.ProjectID},
		{config.BudgetID, state.BudgetID}, {config.ServiceAccountID, state.ServiceAccountID},
		{config.TPMLimitType, state.TPMLimitType}, {config.RPMLimitType, state.RPMLimitType},
		{config.BudgetDuration, state.BudgetDuration}, {config.Duration, state.Duration},
		{config.KeyWOVersion, state.KeyWOVersion},
	} {
		if value.config.IsUnknown() || !value.config.Equal(value.state) {
			return true
		}
	}
	for _, value := range []struct{ config, state types.String }{
		{config.Key, state.Key}, {config.KeyAlias, state.KeyAlias},
	} {
		if value.config.IsUnknown() || (!value.config.IsNull() && !value.config.Equal(value.state)) {
			return true
		}
	}
	for _, value := range []struct{ config, state types.List }{
		{config.Models, state.Models}, {config.AllowedRoutes, state.AllowedRoutes},
		{config.AllowedPassthroughRoutes, state.AllowedPassthroughRoutes},
		{config.AllowedCacheControls, state.AllowedCacheControls},
		{config.Guardrails, state.Guardrails}, {config.Prompts, state.Prompts},
		{config.EnforcedParams, state.EnforcedParams}, {config.Tags, state.Tags},
	} {
		if value.config.IsUnknown() || (!value.config.IsNull() && !value.config.Equal(value.state)) {
			return true
		}
	}
	for _, value := range []struct{ config, state types.Map }{
		{config.Metadata, state.Metadata}, {config.Aliases, state.Aliases},
		{config.Config, state.Config}, {config.Permissions, state.Permissions},
		{config.ModelMaxBudget, state.ModelMaxBudget}, {config.ModelRPMLimit, state.ModelRPMLimit},
		{config.ModelTPMLimit, state.ModelTPMLimit},
	} {
		if value.config.IsUnknown() || (!value.config.IsNull() && !value.config.Equal(value.state)) {
			return true
		}
	}
	for _, value := range []struct{ config, state types.Float64 }{
		{config.MaxBudget, state.MaxBudget}, {config.SoftBudget, state.SoftBudget},
	} {
		if value.config.IsUnknown() || (!value.config.IsNull() && !value.config.Equal(value.state)) {
			return true
		}
	}
	for _, value := range []struct{ config, state types.Int64 }{
		{config.MaxParallelRequests, state.MaxParallelRequests},
		{config.TPMLimit, state.TPMLimit}, {config.RPMLimit, state.RPMLimit},
	} {
		if value.config.IsUnknown() || (!value.config.IsNull() && !value.config.Equal(value.state)) {
			return true
		}
	}
	if config.Blocked.IsUnknown() || (!config.Blocked.IsNull() && !config.Blocked.Equal(state.Blocked)) {
		return true
	}
	if config.RouterSettings.IsUnknown() || !config.RouterSettings.Equal(state.RouterSettings) {
		return true
	}
	return false
}

func (p keySemanticPrepared) anyConfigured() bool {
	return p.metadataProvenance.Configured || p.configProvenance.Configured || p.permissionsProvenance.Configured
}

func encodeKeySemanticProvenance(ctx context.Context, prepared keySemanticPrepared) (map[string][]byte, error) {
	result := make(map[string][]byte, 3)
	entries := []struct {
		name  string
		value semanticDictionaryProvenance
	}{
		{keyMetadataJSONProvenancePrivateKey, prepared.metadataProvenance},
		{keyConfigJSONProvenancePrivateKey, prepared.configProvenance},
		{keyPermissionsJSONProvenancePrivateKey, prepared.permissionsProvenance},
	}
	for _, entry := range entries {
		raw, err := encodeSemanticDictionaryProvenance(ctx, entry.value)
		if err != nil {
			return nil, err
		}
		result[entry.name] = raw
	}
	return result, nil
}

func overlayKeyCreateSemanticDictionaries(ctx context.Context, request map[string]interface{}, prepared keySemanticPrepared) error {
	entries := []struct {
		name       string
		object     map[string]interface{}
		configured bool
	}{
		{"metadata", prepared.metadataObject, prepared.metadataProvenance.Configured},
		{"config", prepared.configObject, prepared.configProvenance.Configured},
		{"permissions", prepared.permissionsObject, prepared.permissionsProvenance.Configured},
	}
	for _, entry := range entries {
		if !entry.configured {
			continue
		}
		base := map[string]interface{}{}
		if existing, present := request[entry.name]; present {
			var ok bool
			base, ok = existing.(map[string]interface{})
			if !ok || base == nil {
				return errSemanticDictionaryTraversal
			}
		}
		overlaid, err := overlaySemanticDictionaryObject(ctx, base, entry.object)
		if err != nil {
			return err
		}
		request[entry.name] = overlaid
	}
	return nil
}

func replaceChangedKeySemanticDictionaries(
	ctx context.Context,
	request map[string]interface{},
	configuredRoots map[string]interface{},
	info map[string]interface{},
	desired keySemanticPrepared,
	prior keySemanticReadOwnership,
	priorState KeyResourceModel,
	changed map[string]bool,
) error {
	remote := make(map[string]map[string]interface{}, 3)
	for _, name := range []string{"metadata", "config", "permissions"} {
		object, ok := info[name].(map[string]interface{})
		if !ok || object == nil || validateSemanticDictionaryValue(ctx, object) != nil || validateKeySemanticDictionaryPersistence(ctx, object) != nil {
			return errSemanticDictionaryTraversal
		}
		remote[name] = object
	}
	if changed["metadata"] {
		if err := validateKeyMetadataReplacementCiphertext(ctx, remote["metadata"], priorState.MetadataJSON, prior.metadata); err != nil {
			return err
		}
	}
	entries := []struct {
		name       string
		object     map[string]interface{}
		priorOwned semanticDictionaryPathSet
	}{
		{"metadata", desired.metadataObject, prior.metadata.TerraformOwned},
		{"config", desired.configObject, prior.config.TerraformOwned},
		{"permissions", desired.permissionsObject, prior.permissions.TerraformOwned},
	}
	for _, entry := range entries {
		delete(request, entry.name)
		if !changed[entry.name] {
			continue
		}
		configured := entry.object
		if configured == nil {
			configured = map[string]interface{}{}
		}
		replacement, _, err := applySemanticDictionary(ctx, remote[entry.name], configured, entry.priorOwned)
		if err != nil {
			return err
		}
		if overlay, present := configuredRoots[entry.name]; present {
			legacy, ok := overlay.(map[string]interface{})
			if !ok || legacy == nil {
				return errSemanticDictionaryTraversal
			}
			replacement, err = overlaySemanticDictionaryObject(ctx, replacement, legacy)
			if err != nil {
				return err
			}
		}
		request[entry.name] = replacement
	}
	return nil
}

func validateKeySemanticDictionaryPersistence(ctx context.Context, object map[string]interface{}) error {
	// Key dictionaries retain top-level nulls. Decimal and exponent forms still
	// pass through Python float storage in the pinned LiteLLM implementation.
	return validateModelSemanticDictionaryNumbers(ctx, object)
}

func keyUnconfiguredSemanticDictionaryProvenance() semanticDictionaryProvenance {
	value := emptySemanticDictionaryProvenance()
	value.Initialized = true
	return value
}

func keySemanticDictionaryConfiguration(ctx context.Context, value types.String, legacy types.Map, reserved []string) (map[string]interface{}, semanticDictionaryProvenance, error) {
	provenance := keyUnconfiguredSemanticDictionaryProvenance()
	if value.IsNull() {
		return nil, provenance, nil
	}
	if value.IsUnknown() {
		return nil, semanticDictionaryProvenance{}, errors.New("semantic key dictionary configuration is unknown")
	}
	object, err := parseSemanticDictionary(ctx, value.ValueString())
	if err != nil {
		return nil, semanticDictionaryProvenance{}, err
	}
	if err := validateKeySemanticDictionaryPersistence(ctx, object); err != nil {
		return nil, semanticDictionaryProvenance{}, err
	}
	if err := semanticDictionaryTopLevelOverlap(ctx, object, configuredAdditionalParamKeys(legacy), reserved); err != nil {
		return nil, semanticDictionaryProvenance{}, err
	}
	paths, err := semanticDictionaryLeafPaths(ctx, object)
	if err != nil {
		return nil, semanticDictionaryProvenance{}, err
	}
	provenance.Configured = true
	provenance.TerraformOwned = paths
	return object, provenance, nil
}

func decodeKeySemanticDictionaryProvenance(ctx context.Context, raw []byte, state types.String) (semanticDictionaryProvenance, error) {
	if len(raw) == 0 {
		if !state.IsNull() && !state.IsUnknown() {
			return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
		}
		return keyUnconfiguredSemanticDictionaryProvenance(), nil
	}
	value, err := decodeSemanticDictionaryProvenance(ctx, raw)
	if err != nil {
		return semanticDictionaryProvenance{}, err
	}
	if !value.Initialized || value.Configured != (!state.IsNull() && !state.IsUnknown()) || len(value.APIOwned) != 0 || len(value.PendingTerraformOwned) != 0 || len(value.PendingAPIOwned) != 0 || len(value.PendingRemovals) != 0 {
		return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
	}
	if !value.Configured {
		if len(value.TerraformOwned) != 0 {
			return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
		}
		return value, nil
	}
	object, err := parseSemanticDictionary(ctx, state.ValueString())
	if err != nil || validateKeySemanticDictionaryPersistence(ctx, object) != nil {
		return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
	}
	expected, err := semanticDictionaryLeafPaths(ctx, object)
	if err != nil || !modelSemanticDictionaryPathSetsEqual(expected, value.TerraformOwned) {
		return semanticDictionaryProvenance{}, errSemanticDictionaryPrivate
	}
	return value, nil
}

func keySemanticDictionaryChanged(ctx context.Context, configured, prior types.String, priorProvenance semanticDictionaryProvenance) (bool, error) {
	if configured.IsUnknown() {
		return false, errors.New("semantic key dictionary configuration is unknown")
	}
	if configured.IsNull() {
		return priorProvenance.Configured, nil
	}
	if !priorProvenance.Configured || prior.IsNull() || prior.IsUnknown() {
		return true, nil
	}
	left, err := parseSemanticDictionary(ctx, configured.ValueString())
	if err != nil {
		return false, err
	}
	right, err := parseSemanticDictionary(ctx, prior.ValueString())
	if err != nil {
		return false, err
	}
	return semanticDictionaryValuesEqual(ctx, left, right)
}

// keySemanticDictionaryNeedsChange is the non-inverted form used by callers.
func keySemanticDictionaryNeedsChange(ctx context.Context, configured, prior types.String, priorProvenance semanticDictionaryProvenance) (bool, error) {
	equal, err := keySemanticDictionaryChanged(ctx, configured, prior, priorProvenance)
	if err != nil || configured.IsNull() || !priorProvenance.Configured {
		return equal, err
	}
	return !equal, nil
}

func projectKeySemanticDictionary(ctx context.Context, current types.String, observed map[string]interface{}, provenance semanticDictionaryProvenance, masked semanticDictionaryMaskPredicate) (types.String, error) {
	if !provenance.Configured {
		return types.StringNull(), nil
	}
	projected, err := projectModelAdditionalModelInfoJSON(ctx, observed, provenance)
	if err != nil {
		return types.StringNull(), err
	}
	if masked != nil {
		prior, parseErr := parseSemanticDictionary(ctx, current.ValueString())
		if parseErr != nil {
			return types.StringNull(), errSemanticDictionaryPrivate
		}
		projected, err = restoreSemanticDictionaryMaskedValues(ctx, prior, projected, true, masked)
		if err != nil {
			return types.StringNull(), err
		}
	}
	return reconcileSemanticDictionaryString(ctx, current, projected)
}

func keySemanticDictionaryPathsPresent(ctx context.Context, observed map[string]interface{}, paths semanticDictionaryPathSet) (bool, error) {
	if err := validateSemanticDictionaryPathSet(ctx, paths); err != nil {
		return false, errSemanticDictionaryPrivate
	}
	for pointer := range paths {
		members, err := decodeSemanticDictionaryPointer(pointer)
		if err != nil {
			return false, errSemanticDictionaryPrivate
		}
		var current interface{} = observed
		for _, member := range members {
			object, ok := current.(map[string]interface{})
			if !ok {
				return false, nil
			}
			current, ok = object[member]
			if !ok {
				return false, nil
			}
		}
	}
	return true, nil
}

func keySemanticProjectionRemovals(ctx context.Context, target, removals semanticDictionaryPathSet) (semanticDictionaryPathSet, error) {
	if err := validateSemanticDictionaryPathSet(ctx, target); err != nil {
		return nil, errSemanticDictionaryPrivate
	}
	if err := validateSemanticDictionaryPathSet(ctx, removals); err != nil {
		return nil, errSemanticDictionaryPrivate
	}
	result := semanticDictionaryPathSet{}
	for removalPointer := range removals {
		removalMembers, err := decodeSemanticDictionaryPointer(removalPointer)
		if err != nil {
			return nil, errSemanticDictionaryPrivate
		}
		expanded := false
		for targetPointer := range target {
			targetMembers, err := decodeSemanticDictionaryPointer(targetPointer)
			if err != nil {
				return nil, errSemanticDictionaryPrivate
			}
			if len(targetMembers) > len(removalMembers) && pathHasPrefix(targetMembers, removalMembers) {
				expanded = true
				break
			}
		}
		if !expanded {
			result[removalPointer] = true
		}
	}
	return result, nil
}

func keySemanticPendingCommitted(ctx context.Context, observed map[string]interface{}, target, removals semanticDictionaryPathSet) (bool, error) {
	if err := validateSemanticDictionaryPathSet(ctx, target); err != nil {
		return false, errSemanticDictionaryPrivate
	}
	if err := validateSemanticDictionaryPathSet(ctx, removals); err != nil {
		return false, errSemanticDictionaryPrivate
	}
	for removalPointer := range removals {
		removalMembers, err := decodeSemanticDictionaryPointer(removalPointer)
		if err != nil {
			return false, errSemanticDictionaryPrivate
		}
		expansionTargets := semanticDictionaryPathSet{}
		for targetPointer := range target {
			targetMembers, err := decodeSemanticDictionaryPointer(targetPointer)
			if err != nil {
				return false, errSemanticDictionaryPrivate
			}
			if len(targetMembers) > len(removalMembers) && pathHasPrefix(targetMembers, removalMembers) {
				expansionTargets[targetPointer] = true
			}
		}
		if len(expansionTargets) != 0 {
			present, err := keySemanticDictionaryPathsPresent(ctx, observed, expansionTargets)
			if err != nil || !present {
				return false, err
			}
			continue
		}
		if verifyKeySemanticDictionaryRemovals(ctx, observed, semanticDictionaryPathSet{removalPointer: true}) != nil {
			return false, nil
		}
	}
	return true, nil
}

func keySemanticPendingNotCommitted(ctx context.Context, observed map[string]interface{}, target, removals semanticDictionaryPathSet) (bool, error) {
	if err := validateSemanticDictionaryPathSet(ctx, target); err != nil {
		return false, errSemanticDictionaryPrivate
	}
	if err := validateSemanticDictionaryPathSet(ctx, removals); err != nil {
		return false, errSemanticDictionaryPrivate
	}
	for removalPointer := range removals {
		removalMembers, err := decodeSemanticDictionaryPointer(removalPointer)
		if err != nil {
			return false, errSemanticDictionaryPrivate
		}
		for targetPointer := range target {
			targetMembers, err := decodeSemanticDictionaryPointer(targetPointer)
			if err != nil {
				return false, errSemanticDictionaryPrivate
			}
			if len(targetMembers) <= len(removalMembers) || !pathHasPrefix(targetMembers, removalMembers) {
				continue
			}
			present, err := keySemanticDictionaryPathsPresent(ctx, observed, semanticDictionaryPathSet{targetPointer: true})
			if err != nil {
				return false, err
			}
			if present {
				// Any descendant from an expansion proves that the exact prior
				// atomic shape is no longer intact. A subset is a partial commit,
				// not evidence that the mutation failed entirely.
				return false, nil
			}
		}
		present, err := keySemanticDictionaryPathsPresent(ctx, observed, semanticDictionaryPathSet{removalPointer: true})
		if err != nil || !present {
			return false, err
		}
	}
	return true, nil
}

func resolveKeySemanticPendingTransition(ctx context.Context, info map[string]interface{}, prior keySemanticReadOwnership) (keySemanticReadOwnership, keySemanticPendingReconcile, error) {
	if !prior.pending.any() {
		return prior, keySemanticPendingReconcile{}, nil
	}
	effective := prior
	effective.pending = keySemanticPendingTransition{}
	effective.reconcile = prior.reconcile
	outcome := 0 // 1 committed, 2 not committed
	entries := []struct {
		name       string
		pending    keySemanticPendingRoot
		provenance *semanticDictionaryProvenance
		removals   *semanticDictionaryPathSet
	}{
		{"metadata", prior.pending.Metadata, &effective.metadata, &effective.metadataRemovals},
		{"config", prior.pending.Config, &effective.config, &effective.configRemovals},
		{"permissions", prior.pending.Permissions, &effective.permissions, &effective.permissionsRemovals},
	}
	for _, entry := range entries {
		if !entry.pending.Active {
			continue
		}
		object, present, ok := keyResponseObject(info[entry.name])
		if !present {
			object = map[string]interface{}{}
		} else if !ok || object == nil {
			return keySemanticReadOwnership{}, keySemanticPendingReconcile{}, errSemanticDictionaryTraversal
		}
		removed, err := keySemanticPendingCommitted(ctx, object, entry.pending.TerraformOwned, entry.pending.Removals)
		if err != nil {
			return keySemanticReadOwnership{}, keySemanticPendingReconcile{}, err
		}
		priorPresent, err := keySemanticPendingNotCommitted(ctx, object, entry.pending.TerraformOwned, entry.pending.Removals)
		if err != nil {
			return keySemanticReadOwnership{}, keySemanticPendingReconcile{}, err
		}
		currentOutcome := 0
		switch {
		case removed:
			currentOutcome = 1
			*entry.provenance = keyUnconfiguredSemanticDictionaryProvenance()
			entry.provenance.Configured = entry.pending.Configured
			entry.provenance.TerraformOwned = entry.pending.TerraformOwned
			projectionRemovals, err := keySemanticProjectionRemovals(ctx, entry.pending.TerraformOwned, entry.pending.Removals)
			if err != nil {
				return keySemanticReadOwnership{}, keySemanticPendingReconcile{}, err
			}
			*entry.removals = projectionRemovals
		case priorPresent:
			currentOutcome = 2
			*entry.removals = semanticDictionaryPathSet{}
		default:
			return keySemanticReadOwnership{}, keySemanticPendingReconcile{}, errSemanticDictionaryTraversal
		}
		if outcome != 0 && outcome != currentOutcome {
			return keySemanticReadOwnership{}, keySemanticPendingReconcile{}, errSemanticDictionaryTraversal
		}
		outcome = currentOutcome
	}
	if outcome == 0 {
		return keySemanticReadOwnership{}, keySemanticPendingReconcile{}, errSemanticDictionaryPrivate
	}
	reconcile := keySemanticPendingReconcile{Present: true, Committed: outcome == 1, Effective: effective}
	return effective, reconcile, nil
}

func keyMetadataCallbackCiphertext(path []string, value string) bool {
	if !strings.HasPrefix(value, "litellm_enc::") || len(path) < 3 {
		return false
	}
	var final string
	switch {
	case len(path) == 4 && path[0] == "logging" && isDecimalIndex(path[1]) && path[2] == "callback_vars":
		final = path[3]
	case len(path) == 3 && path[0] == "callback_settings" && path[1] == "callback_vars":
		final = path[2]
	default:
		return false
	}
	if strings.EqualFold(final, "gcs_path_service_account") {
		return true
	}
	segments := strings.FieldsFunc(strings.ToLower(final), func(r rune) bool { return r == '_' || r == '-' })
	for _, segment := range segments {
		if segment == "cost" {
			return false
		}
	}
	for _, segment := range segments {
		switch segment {
		case "password", "secret", "key", "token", "auth", "authorization", "credential", "credentials", "access", "private", "certificate", "fingerprint", "tenancy":
			return true
		}
	}
	return false
}

func isDecimalIndex(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validateKeyMetadataReplacementCiphertext(ctx context.Context, remote map[string]interface{}, prior types.String, provenance semanticDictionaryProvenance) error {
	contains, err := semanticDictionaryContainsMaskedValue(ctx, remote, nil, keyMetadataCallbackCiphertext)
	if err != nil || !contains {
		return err
	}
	if !provenance.Configured || prior.IsNull() || prior.IsUnknown() {
		return errSemanticDictionaryMasked
	}
	projected, err := projectModelAdditionalModelInfoJSON(ctx, remote, provenance)
	if err != nil {
		return errSemanticDictionaryMasked
	}
	priorObject, err := parseSemanticDictionary(ctx, prior.ValueString())
	if err != nil {
		return errSemanticDictionaryMasked
	}
	_, err = restoreSemanticDictionaryMaskedValues(ctx, priorObject, projected, true, keyMetadataCallbackCiphertext)
	if err != nil {
		return errSemanticDictionaryMasked
	}
	// Every ciphertext in the replacement must be under a Terraform-owned leaf.
	for pointer := range provenance.TerraformOwned {
		_ = pointer
	}
	return rejectUnownedKeyMetadataCiphertext(ctx, remote, provenance.TerraformOwned, nil)
}

func rejectUnownedKeyMetadataCiphertext(ctx context.Context, value interface{}, owned semanticDictionaryPathSet, path []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch value := value.(type) {
	case string:
		if !keyMetadataCallbackCiphertext(path, value) {
			return nil
		}
		for pointer := range owned {
			members, err := decodeSemanticDictionaryPointer(pointer)
			if err != nil {
				return errSemanticDictionaryPrivate
			}
			if pathHasPrefix(path, members) || pathHasPrefix(members, path) {
				return nil
			}
		}
		return errSemanticDictionaryMasked
	case map[string]interface{}:
		for name, child := range value {
			if err := rejectUnownedKeyMetadataCiphertext(ctx, child, owned, append(path, name)); err != nil {
				return err
			}
		}
	case []interface{}:
		for index, child := range value {
			if err := rejectUnownedKeyMetadataCiphertext(ctx, child, owned, append(path, strconv.Itoa(index))); err != nil {
				return err
			}
		}
	}
	return nil
}

func partialKeySemanticRecoveryState(data KeyResourceModel, identity string, writeOnly bool) KeyResourceModel {
	data.ID = types.StringValue(hashKeyForID(identity))
	if writeOnly {
		data.Key = types.StringNull()
	} else {
		data.Key = types.StringValue(identity)
	}
	data.KeyWO = types.StringNull()
	data.SendInviteEmail = types.BoolNull()
	data.MetadataJSON = types.StringNull()
	data.ConfigJSON = types.StringNull()
	data.PermissionsJSON = types.StringNull()

	for _, value := range []*types.String{
		&data.UserID, &data.TeamID, &data.OrganizationID, &data.ProjectID, &data.BudgetID,
		&data.ServiceAccountID, &data.TPMLimitType, &data.RPMLimitType, &data.BudgetDuration,
		&data.KeyAlias, &data.Duration,
	} {
		if value.IsUnknown() {
			*value = types.StringNull()
		}
	}
	for _, value := range []*types.List{
		&data.Models, &data.AllowedRoutes, &data.AllowedPassthroughRoutes, &data.AllowedCacheControls,
		&data.Guardrails, &data.Prompts, &data.EnforcedParams, &data.Tags,
	} {
		if value.IsUnknown() {
			*value = types.ListNull(types.StringType)
		}
	}
	for _, value := range []struct {
		value       *types.Map
		elementType attr.Type
	}{
		{&data.Metadata, types.StringType}, {&data.Aliases, types.StringType},
		{&data.Config, types.StringType}, {&data.Permissions, types.StringType},
		{&data.ModelMaxBudget, types.Float64Type}, {&data.ModelRPMLimit, types.Int64Type},
		{&data.ModelTPMLimit, types.Int64Type},
	} {
		if value.value.IsUnknown() {
			*value.value = types.MapNull(value.elementType)
		}
	}
	for _, value := range []*types.Float64{&data.MaxBudget, &data.SoftBudget} {
		if value.IsUnknown() {
			*value = types.Float64Null()
		}
	}
	for _, value := range []*types.Int64{&data.MaxParallelRequests, &data.TPMLimit, &data.RPMLimit} {
		if value.IsUnknown() {
			*value = types.Int64Null()
		}
	}
	if data.Blocked.IsUnknown() {
		data.Blocked = types.BoolNull()
	}
	if data.RouterSettings.IsUnknown() {
		data.RouterSettings = types.ObjectNull(keyRouterSettingsAttrTypes)
	}
	return data
}

func validateKeyCreateResponseIdentity(result map[string]interface{}, identity string) error {
	returned, ok := result["key"].(string)
	if !ok || returned == "" || returned != identity {
		return errSemanticDictionaryTraversal
	}
	if raw, present := result["token"]; present && raw != nil {
		token, ok := raw.(string)
		expected, err := keyHashFromID(hashKeyForID(identity))
		if !ok || err != nil || token != expected {
			return errSemanticDictionaryTraversal
		}
	}
	return nil
}

func validateExactKeyInfoIdentity(result, info map[string]interface{}, identifier string) error {
	root, ok := result["key"].(string)
	if !ok || root == "" || root != identifier {
		return errSemanticDictionaryTraversal
	}
	expectedToken := identifier
	if canonical, ok := canonicalSHA256ManagementHash(identifier); ok {
		expectedToken = canonical
	} else {
		hashed, err := keyHashFromID(hashKeyForID(identifier))
		if err != nil {
			return errSemanticDictionaryTraversal
		}
		expectedToken = hashed
	}
	if raw, present := info["token"]; present && raw != nil {
		token, ok := raw.(string)
		if !ok || token == "" || token != expectedToken {
			return errSemanticDictionaryTraversal
		}
	}
	if nested, present := info["key"]; present && nested != nil {
		text, ok := nested.(string)
		if !ok || text == "" || text != identifier {
			return errSemanticDictionaryTraversal
		}
	}
	return nil
}

func keyLegacyDictionaryProjectionInfo(ctx context.Context, info map[string]interface{}, data *KeyResourceModel, ownership keySemanticReadOwnership) (map[string]interface{}, error) {
	projected := make(map[string]interface{}, len(info))
	for name, value := range info {
		projected[name] = value
	}
	for _, entry := range []struct {
		name       string
		legacy     types.Map
		provenance semanticDictionaryProvenance
	}{
		{"metadata", data.Metadata, ownership.metadata},
		{"config", data.Config, ownership.config},
		{"permissions", data.Permissions, ownership.permissions},
	} {
		remote, ok := info[entry.name].(map[string]interface{})
		if !ok || remote == nil {
			if entry.provenance.Configured {
				return nil, errSemanticDictionaryTraversal
			}
			continue
		}
		if ownership.acceptedCreateRecovery {
			projected[entry.name] = map[string]interface{}{}
			continue
		}
		if !entry.provenance.Configured && entry.name != "metadata" {
			stringOnly := true
			for _, value := range remote {
				if _, ok := value.(string); !ok {
					stringOnly = false
					break
				}
			}
			if !stringOnly {
				// The compatibility map cannot represent a heterogeneous root. Keep
				// only already-managed legacy string keys and leave API-native
				// siblings unmanaged until an explicit JSON takeover.
				legacy := map[string]interface{}{}
				if !entry.legacy.IsNull() && !entry.legacy.IsUnknown() {
					for name := range entry.legacy.Elements() {
						if value, present := remote[name]; present {
							if _, ok := value.(string); !ok {
								return nil, errSemanticDictionaryTraversal
							}
							legacy[name] = value
						}
					}
				}
				projected[entry.name] = legacy
				continue
			}
		}
		if !entry.provenance.Configured {
			continue
		}
		owned, err := semanticDictionaryTopLevelOwnedKeys(ctx, entry.provenance)
		if err != nil {
			return nil, errSemanticDictionaryPrivate
		}
		legacy := make(map[string]interface{}, len(entry.legacy.Elements()))
		if !entry.legacy.IsNull() && !entry.legacy.IsUnknown() {
			for name := range entry.legacy.Elements() {
				if owned[name] {
					continue
				}
				if value, present := remote[name]; present {
					legacy[name] = value
				}
			}
		}
		projected[entry.name] = legacy
	}
	return projected, nil
}

func projectKeySemanticDictionariesFromInfo(ctx context.Context, data *KeyResourceModel, info map[string]interface{}, ownership keySemanticReadOwnership) error {
	entries := []struct {
		name       string
		current    *types.String
		legacy     *types.Map
		provenance semanticDictionaryProvenance
		removals   semanticDictionaryPathSet
		masked     semanticDictionaryMaskPredicate
	}{
		{"metadata", &data.MetadataJSON, &data.Metadata, ownership.metadata, ownership.metadataRemovals, keyMetadataCallbackCiphertext},
		{"config", &data.ConfigJSON, &data.Config, ownership.config, ownership.configRemovals, nil},
		{"permissions", &data.PermissionsJSON, &data.Permissions, ownership.permissions, ownership.permissionsRemovals, nil},
	}
	for _, entry := range entries {
		raw, present := info[entry.name]
		if !present || raw == nil {
			if entry.provenance.Configured {
				return fmt.Errorf("semantic key dictionary projection failed: %s root missing", entry.name)
			}
			*entry.current = types.StringNull()
			continue
		}
		object, ok := raw.(map[string]interface{})
		if !ok || object == nil || validateSemanticDictionaryValue(ctx, object) != nil || validateKeySemanticDictionaryPersistence(ctx, object) != nil {
			return fmt.Errorf("semantic key dictionary projection failed: %s root malformed", entry.name)
		}
		if err := verifyKeySemanticDictionaryRemovals(ctx, object, entry.removals); err != nil {
			return fmt.Errorf("semantic key dictionary projection failed: %s removal mismatch", entry.name)
		}
		projected, err := projectKeySemanticDictionary(ctx, *entry.current, object, entry.provenance, entry.masked)
		if err != nil {
			return fmt.Errorf("semantic key dictionary projection failed: %s owned projection", entry.name)
		}
		*entry.current = projected
		if entry.provenance.Configured {
			ownedTop, err := semanticDictionaryTopLevelOwnedKeys(ctx, entry.provenance)
			if err != nil {
				return fmt.Errorf("semantic key dictionary projection failed: %s owned paths", entry.name)
			}
			if err := filterKeyLegacyOwnedTopLevel(ctx, entry.legacy, ownedTop); err != nil {
				return fmt.Errorf("semantic key dictionary projection failed: %s legacy filter", entry.name)
			}
		}
	}
	return nil
}

func filterKeyLegacyOwnedTopLevel(ctx context.Context, legacy *types.Map, owned map[string]bool) error {
	if legacy.IsNull() || legacy.IsUnknown() || len(owned) == 0 {
		return nil
	}
	elements := make(map[string]attr.Value, len(legacy.Elements()))
	for name, value := range legacy.Elements() {
		if !owned[name] {
			elements[name] = value
		}
	}
	result, diagnostics := types.MapValue(legacy.ElementType(ctx), elements)
	if diagnostics.HasError() {
		return errSemanticDictionaryTraversal
	}
	*legacy = result
	return nil
}

func excludeKeyLegacyJSONTopLevelKeys(ctx context.Context, legacy types.Map, object map[string]interface{}) (types.Map, error) {
	if legacy.IsNull() || legacy.IsUnknown() || object == nil {
		return legacy, nil
	}
	elements := make(map[string]attr.Value, len(legacy.Elements()))
	for name, value := range legacy.Elements() {
		if _, excluded := object[name]; !excluded {
			elements[name] = value
		}
	}
	result, diagnostics := types.MapValue(legacy.ElementType(ctx), elements)
	if diagnostics.HasError() {
		return types.MapNull(legacy.ElementType(ctx)), errSemanticDictionaryTraversal
	}
	return result, nil
}

func pathHasPrefix(path, prefix []string) bool {
	if len(prefix) > len(path) {
		return false
	}
	for index := range prefix {
		if path[index] != prefix[index] {
			return false
		}
	}
	return true
}
