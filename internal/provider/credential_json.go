package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var liteLLMCredentialMask = regexp.MustCompile(`^..\*{4}..$`)

var (
	errCredentialUnknown       = errors.New("credential configuration contains an unknown value")
	errCredentialNotJSONObject = errors.New("credential JSON must encode an object")
)

// credentialOwnership records configured ownership without storing any values
// in Terraform private state. Object nodes own only their configured children;
// atomic nodes own the complete scalar, array, or null value.
type credentialOwnership struct {
	Object   bool                            `json:"object,omitempty"`
	Atomic   bool                            `json:"atomic,omitempty"`
	Children map[string]*credentialOwnership `json:"children,omitempty"`
}

type credentialPrivateMetadata struct {
	Version  int  `json:"version"`
	Imported bool `json:"imported,omitempty"`

	LegacyInfoConfigured   bool `json:"legacy_info_configured,omitempty"`
	JSONInfoConfigured     bool `json:"json_info_configured,omitempty"`
	LegacyValuesConfigured bool `json:"legacy_values_configured,omitempty"`
	JSONValuesConfigured   bool `json:"json_values_configured,omitempty"`
	ModelDominant          bool `json:"model_dominant,omitempty"`
	AllRemoteOwned         bool `json:"all_remote_owned,omitempty"`
	ReplacementPending     bool `json:"replacement_pending,omitempty"`
	UncertainOwnership     bool `json:"uncertain_ownership,omitempty"`

	LegacyInfo   *credentialOwnership `json:"legacy_info,omitempty"`
	JSONInfo     *credentialOwnership `json:"json_info,omitempty"`
	LegacyValues *credentialOwnership `json:"legacy_values,omitempty"`
	JSONValues   *credentialOwnership `json:"json_values,omitempty"`

	// noPrivateFallback is process-local. A schema-v0 Read has no Config, so
	// it may preserve public compatibility values but must not infer ownership
	// from Optional+Computed state or persist that inference into private state.
	noPrivateFallback bool `json:"-"`
}

type credentialConfiguredObject struct {
	Object           map[string]interface{}
	LegacyOwnership  *credentialOwnership
	JSONOwnership    *credentialOwnership
	UnionOwnership   *credentialOwnership
	LegacyConfigured bool
	JSONConfigured   bool
}

func emptyCredentialOwnership() *credentialOwnership {
	return &credentialOwnership{Object: true, Children: map[string]*credentialOwnership{}}
}

func unownedCredentialPrivateMetadata() credentialPrivateMetadata {
	return credentialPrivateMetadata{
		Version:      1,
		LegacyInfo:   emptyCredentialOwnership(),
		JSONInfo:     emptyCredentialOwnership(),
		LegacyValues: emptyCredentialOwnership(),
		JSONValues:   emptyCredentialOwnership(),
	}
}

func credentialOwnershipForObject(value map[string]interface{}) *credentialOwnership {
	root := emptyCredentialOwnership()
	for key, child := range value {
		root.Children[key] = credentialOwnershipForValue(child)
	}
	return root
}

func credentialOwnershipForValue(value interface{}) *credentialOwnership {
	if object, ok := value.(map[string]interface{}); ok {
		return credentialOwnershipForObject(object)
	}
	return &credentialOwnership{Atomic: true}
}

func unionCredentialOwnership(left, right *credentialOwnership) *credentialOwnership {
	if left == nil {
		return cloneCredentialOwnership(right)
	}
	if right == nil {
		return cloneCredentialOwnership(left)
	}
	if left.Atomic || right.Atomic {
		return &credentialOwnership{Atomic: true}
	}
	result := emptyCredentialOwnership()
	for key, child := range left.Children {
		result.Children[key] = cloneCredentialOwnership(child)
	}
	for key, child := range right.Children {
		if existing, ok := result.Children[key]; ok {
			result.Children[key] = unionCredentialOwnership(existing, child)
		} else {
			result.Children[key] = cloneCredentialOwnership(child)
		}
	}
	return result
}

func cloneCredentialOwnership(value *credentialOwnership) *credentialOwnership {
	if value == nil {
		return emptyCredentialOwnership()
	}
	result := &credentialOwnership{Object: value.Object, Atomic: value.Atomic}
	if value.Children != nil {
		result.Children = make(map[string]*credentialOwnership, len(value.Children))
		for key, child := range value.Children {
			result.Children[key] = cloneCredentialOwnership(child)
		}
	}
	return result
}

func decodeCredentialJSONObjectString(value string) (map[string]interface{}, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	var decoded interface{}
	if err := decoder.Decode(&decoded); err != nil {
		return nil, errors.New("credential JSON is invalid")
	}
	if err := ensureCredentialJSONEOF(decoder); err != nil {
		return nil, err
	}
	object, ok := decoded.(map[string]interface{})
	if !ok || object == nil {
		return nil, errCredentialNotJSONObject
	}
	return object, nil
}

func decodeCredentialJSONObjectBytes(value []byte) (map[string]interface{}, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var decoded interface{}
	if err := decoder.Decode(&decoded); err != nil {
		return nil, errors.New("LiteLLM returned a malformed credential object")
	}
	if err := ensureCredentialJSONEOF(decoder); err != nil {
		return nil, errors.New("LiteLLM returned a malformed credential object")
	}
	object, ok := decoded.(map[string]interface{})
	if !ok || object == nil {
		return nil, errors.New("LiteLLM returned a malformed credential object")
	}
	return object, nil
}

func ensureCredentialJSONEOF(decoder *json.Decoder) error {
	var extra interface{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("credential JSON must contain exactly one value")
	}
	return nil
}

func canonicalCredentialJSON(object map[string]interface{}) (string, error) {
	encoded, err := json.Marshal(object)
	if err != nil {
		return "", errors.New("credential object could not be encoded")
	}
	return string(encoded), nil
}

func credentialMapObject(ctx context.Context, value types.Map) (map[string]interface{}, error) {
	if value.IsNull() {
		return nil, nil
	}
	if value.IsUnknown() {
		return nil, errCredentialUnknown
	}
	result := make(map[string]interface{}, len(value.Elements()))
	for key, element := range value.Elements() {
		text, ok := element.(types.String)
		if !ok || text.IsUnknown() {
			return nil, errCredentialUnknown
		}
		if text.IsNull() {
			return nil, errors.New("credential map values must be non-null strings")
		}
		result[key] = text.ValueString()
	}
	return result, nil
}

func credentialStringObject(value types.String) (map[string]interface{}, error) {
	if value.IsNull() {
		return nil, nil
	}
	if value.IsUnknown() {
		return nil, errCredentialUnknown
	}
	return decodeCredentialJSONObjectString(value.ValueString())
}

func buildCredentialConfiguredObject(ctx context.Context, legacy types.Map, jsonValue types.String) (credentialConfiguredObject, error) {
	legacyObject, err := credentialMapObject(ctx, legacy)
	if err != nil {
		return credentialConfiguredObject{}, err
	}
	jsonObject, err := credentialStringObject(jsonValue)
	if err != nil {
		return credentialConfiguredObject{}, err
	}
	result := credentialConfiguredObject{
		Object:           map[string]interface{}{},
		LegacyOwnership:  emptyCredentialOwnership(),
		JSONOwnership:    emptyCredentialOwnership(),
		LegacyConfigured: legacyObject != nil,
		JSONConfigured:   jsonObject != nil,
	}
	if legacyObject != nil {
		result.LegacyOwnership = credentialOwnershipForObject(legacyObject)
		for key, value := range legacyObject {
			result.Object[key] = value
		}
	}
	if jsonObject != nil {
		result.JSONOwnership = credentialOwnershipForObject(jsonObject)
		for key, value := range jsonObject {
			if existing, exists := result.Object[key]; exists && !reflect.DeepEqual(existing, value) {
				return credentialConfiguredObject{}, errors.New("legacy and JSON credential attributes configure different values for the same key")
			}
			result.Object[key] = value
		}
	}
	result.UnionOwnership = unionCredentialOwnership(result.LegacyOwnership, result.JSONOwnership)
	return result, nil
}

func credentialOwnedObjectFromSurfaces(ctx context.Context, legacy types.Map, jsonValue types.String, legacyOwnership, jsonOwnership *credentialOwnership) (map[string]interface{}, error) {
	legacyObject, err := credentialMapObject(ctx, legacy)
	if err != nil {
		if !errors.Is(err, errCredentialUnknown) || legacyOwnership == nil || len(legacyOwnership.Children) != 0 {
			return nil, err
		}
		legacyObject = nil
	}
	jsonObject, err := credentialStringObject(jsonValue)
	if err != nil {
		if !errors.Is(err, errCredentialUnknown) || jsonOwnership == nil || len(jsonOwnership.Children) != 0 {
			return nil, err
		}
		jsonObject = nil
	}
	result := map[string]interface{}{}
	if legacyObject != nil {
		projected, err := projectCredentialObject(legacyObject, legacyObject, legacyOwnership, false)
		if err != nil {
			return nil, err
		}
		for key, value := range projected {
			result[key] = value
		}
	}
	if jsonObject != nil {
		projected, err := projectCredentialObject(jsonObject, jsonObject, jsonOwnership, false)
		if err != nil {
			return nil, err
		}
		for key, value := range projected {
			if existing, ok := result[key]; ok && !reflect.DeepEqual(existing, value) {
				return nil, errors.New("credential state surfaces disagree about an owned key")
			}
			result[key] = value
		}
	}
	return result, nil
}

func credentialTopLevelKeyRemoved(prior, desired *credentialOwnership) bool {
	if prior == nil {
		return false
	}
	for key := range prior.Children {
		if desired == nil || desired.Children[key] == nil {
			return true
		}
	}
	return false
}

func inferCredentialPrivateMetadata(ctx context.Context, data CredentialResourceModel) (credentialPrivateMetadata, error) {
	info, err := buildCredentialConfiguredObject(ctx, data.CredentialInfo, data.CredentialInfoJSON)
	if err != nil {
		return credentialPrivateMetadata{}, err
	}
	values, err := buildCredentialConfiguredObject(ctx, data.CredentialValues, data.CredentialValuesJSON)
	if err != nil {
		return credentialPrivateMetadata{}, err
	}
	modelDominant := !data.ModelID.IsNull() && !data.ModelID.IsUnknown() && data.ModelID.ValueString() != ""
	if modelDominant {
		values.LegacyOwnership = emptyCredentialOwnership()
		values.JSONOwnership = emptyCredentialOwnership()
		values.UnionOwnership = emptyCredentialOwnership()
	}
	return credentialPrivateMetadata{
		Version:                1,
		LegacyInfoConfigured:   info.LegacyConfigured,
		JSONInfoConfigured:     info.JSONConfigured,
		LegacyValuesConfigured: values.LegacyConfigured,
		JSONValuesConfigured:   values.JSONConfigured,
		ModelDominant:          modelDominant,
		LegacyInfo:             info.LegacyOwnership,
		JSONInfo:               info.JSONOwnership,
		LegacyValues:           values.LegacyOwnership,
		JSONValues:             values.JSONOwnership,
	}, nil
}

func credentialMetadataOwnership(metadata credentialPrivateMetadata, values bool) *credentialOwnership {
	if values {
		return unionCredentialOwnership(metadata.LegacyValues, metadata.JSONValues)
	}
	return unionCredentialOwnership(metadata.LegacyInfo, metadata.JSONInfo)
}

func encodeCredentialPrivateMetadata(metadata credentialPrivateMetadata) ([]byte, error) {
	metadata.Version = 1
	for _, ownership := range []*credentialOwnership{metadata.LegacyInfo, metadata.JSONInfo, metadata.LegacyValues, metadata.JSONValues} {
		normalizeCredentialOwnership(ownership)
	}
	if !validCredentialPrivateMetadata(metadata) {
		return nil, errors.New("credential ownership metadata is invalid")
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, errors.New("credential ownership metadata could not be encoded")
	}
	return encoded, nil
}

func decodeCredentialPrivateMetadata(encoded []byte) (credentialPrivateMetadata, bool) {
	if len(encoded) == 0 {
		return credentialPrivateMetadata{}, false
	}
	var metadata credentialPrivateMetadata
	if err := json.Unmarshal(encoded, &metadata); err != nil || metadata.Version != 1 {
		return credentialPrivateMetadata{}, false
	}
	if metadata.LegacyInfo == nil {
		metadata.LegacyInfo = emptyCredentialOwnership()
	}
	if metadata.JSONInfo == nil {
		metadata.JSONInfo = emptyCredentialOwnership()
	}
	if metadata.LegacyValues == nil {
		metadata.LegacyValues = emptyCredentialOwnership()
	}
	if metadata.JSONValues == nil {
		metadata.JSONValues = emptyCredentialOwnership()
	}
	for _, ownership := range []*credentialOwnership{metadata.LegacyInfo, metadata.JSONInfo, metadata.LegacyValues, metadata.JSONValues} {
		normalizeCredentialOwnership(ownership)
	}
	if !validCredentialPrivateMetadata(metadata) {
		return credentialPrivateMetadata{}, false
	}
	return metadata, true
}

func normalizeCredentialOwnership(ownership *credentialOwnership) {
	if ownership == nil || !ownership.Object {
		return
	}
	if ownership.Children == nil {
		ownership.Children = map[string]*credentialOwnership{}
	}
	for _, child := range ownership.Children {
		normalizeCredentialOwnership(child)
	}
}

func validCredentialPrivateMetadata(metadata credentialPrivateMetadata) bool {
	for _, ownership := range []*credentialOwnership{metadata.LegacyInfo, metadata.JSONInfo, metadata.LegacyValues, metadata.JSONValues} {
		if !validCredentialOwnership(ownership, true) {
			return false
		}
	}
	if (!metadata.LegacyInfoConfigured && len(metadata.LegacyInfo.Children) != 0) ||
		(!metadata.JSONInfoConfigured && len(metadata.JSONInfo.Children) != 0) ||
		(!metadata.LegacyValuesConfigured && len(metadata.LegacyValues.Children) != 0) ||
		(!metadata.JSONValuesConfigured && len(metadata.JSONValues.Children) != 0) {
		return false
	}
	if metadata.ModelDominant && (len(metadata.LegacyValues.Children) != 0 || len(metadata.JSONValues.Children) != 0) {
		return false
	}
	if metadata.Imported && (metadata.ModelDominant || metadata.AllRemoteOwned ||
		len(metadata.LegacyInfo.Children) != 0 || len(metadata.JSONInfo.Children) != 0 ||
		len(metadata.LegacyValues.Children) != 0 || len(metadata.JSONValues.Children) != 0) {
		return false
	}
	if metadata.UncertainOwnership && (metadata.AllRemoteOwned ||
		metadata.LegacyInfoConfigured || metadata.JSONInfoConfigured ||
		metadata.LegacyValuesConfigured || metadata.JSONValuesConfigured ||
		len(metadata.LegacyInfo.Children) != 0 || len(metadata.JSONInfo.Children) != 0 ||
		len(metadata.LegacyValues.Children) != 0 || len(metadata.JSONValues.Children) != 0) {
		return false
	}
	return true
}

func validCredentialOwnership(ownership *credentialOwnership, root bool) bool {
	if ownership == nil || ownership.Object == ownership.Atomic {
		return false
	}
	if root && !ownership.Object {
		return false
	}
	if ownership.Atomic {
		return len(ownership.Children) == 0
	}
	if ownership.Children == nil {
		return false
	}
	for key, child := range ownership.Children {
		if key == "" || !validCredentialOwnership(child, false) {
			return false
		}
	}
	return true
}

// credentialJSONObjectValidator validates shape while preserving json.Number
// lexemes; this prevents large integers from being rounded through float64.
type credentialJSONObjectValidator struct{}

var _ validator.String = credentialJSONObjectValidator{}

func (credentialJSONObjectValidator) Description(context.Context) string {
	return "Value must be a JSON object."
}

func (v credentialJSONObjectValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (credentialJSONObjectValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := decodeCredentialJSONObjectString(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Credential JSON Object", "The value must encode exactly one non-null JSON object.")
	}
}

type canonicalCredentialJSONPlanModifier struct{}

var _ planmodifier.String = canonicalCredentialJSONPlanModifier{}

func (canonicalCredentialJSONPlanModifier) Description(context.Context) string {
	return "Stores JSON objects in deterministic compact form while preserving configured number lexemes provider-side."
}

func (m canonicalCredentialJSONPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (canonicalCredentialJSONPlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	object, err := decodeCredentialJSONObjectString(req.ConfigValue.ValueString())
	if err != nil {
		return
	}
	canonical, err := canonicalCredentialJSON(object)
	if err != nil {
		return
	}
	resp.PlanValue = types.StringValue(canonical)
}

func stringMapValueFromObject(object map[string]interface{}) (types.Map, error) {
	values := make(map[string]attr.Value)
	for key, value := range object {
		if text, ok := value.(string); ok {
			values[key] = types.StringValue(text)
		}
	}
	result, diagnostics := types.MapValue(types.StringType, values)
	if diagnostics.HasError() {
		return types.MapNull(types.StringType), errors.New("credential string map could not be represented in Terraform state")
	}
	return result, nil
}

type credentialMaskMode uint8

const (
	credentialMaskNone credentialMaskMode = iota
	credentialMaskScalar
	credentialMaskObject
)

func credentialChildMasking(active bool, key string, value interface{}) credentialMaskMode {
	if !active {
		return credentialMaskNone
	}
	// Keep walking every object on a credential-values surface. LiteLLM masks
	// sensitive leaves recursively even when their parent key (for example
	// "oauth") is not itself sensitive.
	if _, ok := value.(map[string]interface{}); ok {
		return credentialMaskObject
	}
	if isLiteLLMSensitiveCredentialKey(key) {
		if _, ok := value.(string); ok {
			return credentialMaskScalar
		}
	}
	return credentialMaskNone
}

func isLiteLLMSensitiveCredentialKey(key string) bool {
	key = strings.ToLower(key)
	for _, fragment := range []string{
		"authorization", "token", "key", "secret", "vertex_credentials", "credentials", "password", "passwd",
	} {
		if strings.Contains(key, fragment) {
			return true
		}
	}
	return false
}

func isLiteLLMCredentialMask(value string) bool {
	return value == "*****" || liteLLMCredentialMask.MatchString(value)
}

func maskLiteLLMCredentialString(value string) string {
	runes := []rune(value)
	if len(runes) <= 4 {
		return "*****"
	}
	return string(runes[:2]) + "****" + string(runes[len(runes)-2:])
}

func projectCredentialObject(remote, prior map[string]interface{}, ownership *credentialOwnership, masked bool) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if ownership == nil {
		return result, nil
	}
	for key, node := range ownership.Children {
		remoteValue, exists := remote[key]
		if !exists {
			continue
		}
		priorValue := prior[key]
		projected, err := projectCredentialValue(remoteValue, priorValue, node, credentialChildMasking(masked, key, remoteValue))
		if err != nil {
			return nil, err
		}
		result[key] = projected
	}
	return result, nil
}

func projectCredentialValue(remote, prior interface{}, ownership *credentialOwnership, maskMode credentialMaskMode) (interface{}, error) {
	if ownership == nil {
		return nil, errors.New("credential ownership metadata is incomplete")
	}
	if ownership.Object {
		remoteObject, ok := remote.(map[string]interface{})
		if !ok {
			return nil, errors.New("an owned credential object changed to a non-object value")
		}
		priorObject, _ := prior.(map[string]interface{})
		if priorObject == nil {
			priorObject = map[string]interface{}{}
		}
		return projectCredentialObject(remoteObject, priorObject, ownership, maskMode == credentialMaskObject)
	}
	if !ownership.Atomic {
		return nil, errors.New("credential ownership metadata does not describe an object or atomic value")
	}
	if _, remoteIsObject := remote.(map[string]interface{}); remoteIsObject {
		return nil, errors.New("an owned atomic credential value changed to an object")
	}
	if maskMode == credentialMaskScalar {
		remoteText, remoteIsText := remote.(string)
		priorText, priorIsText := prior.(string)
		if remoteIsText && isLiteLLMCredentialMask(remoteText) {
			if priorIsText && !isLiteLLMCredentialMask(priorText) && remoteText == maskLiteLLMCredentialString(priorText) {
				return priorText, nil
			}
			return nil, errors.New("LiteLLM returned a credential mask that cannot be matched to an owned value")
		}
	}
	return remote, nil
}

func hydrateCredentialPatch(remote, prior, desired map[string]interface{}, priorOwnership, desiredOwnership *credentialOwnership, masked bool) (map[string]interface{}, error) {
	if credentialTopLevelKeyRemoved(priorOwnership, desiredOwnership) {
		return nil, errors.New("LiteLLM PATCH cannot safely remove an owned top-level credential key")
	}
	if err := validateCredentialOwnedAtomicPreconditions(remote, prior, priorOwnership, masked); err != nil {
		return nil, err
	}
	result := map[string]interface{}{}
	for key, desiredNode := range desiredOwnership.Children {
		desiredValue := desired[key]
		priorNode := priorOwnership.Children[key]
		remoteValue, existsRemote := remote[key]
		if !existsRemote {
			result[key] = desiredValue
			continue
		}
		merged, err := hydrateCredentialValue(
			remoteValue,
			prior[key],
			desiredValue,
			priorNode,
			desiredNode,
			credentialChildMasking(masked, key, remoteValue),
		)
		if err != nil {
			return nil, err
		}
		result[key] = merged
	}
	return result, nil
}

// hydrateCredentialInfoTopLevel works around v1.98's credential_info update
// implementation, which can rebuild that whole dictionary before applying the
// shallow patch. Readable unmanaged top-level metadata must therefore ride
// along in the PATCH even though it remains excluded from Terraform ownership.
func hydrateCredentialInfoTopLevel(remote, patch map[string]interface{}, priorOwnership, desiredOwnership *credentialOwnership) (map[string]interface{}, error) {
	result := make(map[string]interface{}, len(remote)+len(patch))
	for key, value := range patch {
		result[key] = value
	}
	for key, value := range remote {
		if desiredOwnership.Children[key] != nil {
			continue
		}
		if priorOwnership.Children[key] != nil {
			return nil, errors.New("LiteLLM PATCH cannot safely remove an owned top-level credential metadata key")
		}
		result[key] = value
	}
	return result, nil
}

func hydrateCredentialValue(remote, prior, desired interface{}, priorOwnership, desiredOwnership *credentialOwnership, maskMode credentialMaskMode) (interface{}, error) {
	desiredObject, desiredIsObject := desired.(map[string]interface{})
	remoteObject, remoteIsObject := remote.(map[string]interface{})
	priorObject, priorIsObject := prior.(map[string]interface{})

	if !desiredOwnership.Object {
		if remoteIsObject {
			if priorOwnership == nil || !priorOwnership.Object || !credentialRemoteFullyOwned(remoteObject, priorObject, priorOwnership, maskMode == credentialMaskObject) {
				return nil, errors.New("an object-to-scalar credential change could discard unmanaged nested values")
			}
		}
		return desired, nil
	}
	if !desiredIsObject {
		return nil, errors.New("credential ownership metadata does not match the configured JSON value")
	}
	if !remoteIsObject {
		if priorOwnership != nil && priorOwnership.Object {
			return nil, errors.New("an owned credential object changed remotely to a non-object value")
		}
		// A prior atomic value has no nested siblings. A planned scalar-to-object
		// transition may replace it; postflight still proves the object persisted.
		return desired, nil
	}
	if !priorIsObject {
		priorObject = map[string]interface{}{}
	}

	result := make(map[string]interface{}, len(remoteObject)+len(desiredObject))
	for key, desiredChild := range desiredOwnership.Children {
		desiredValue := desiredObject[key]
		remoteValue, exists := remoteObject[key]
		if !exists {
			result[key] = desiredValue
			continue
		}
		priorChild := (*credentialOwnership)(nil)
		if priorOwnership != nil && priorOwnership.Object {
			priorChild = priorOwnership.Children[key]
		}
		childMode := credentialChildMasking(maskMode == credentialMaskObject, key, remoteValue)
		merged, err := hydrateCredentialValue(remoteValue, priorObject[key], desiredValue, priorChild, desiredChild, childMode)
		if err != nil {
			return nil, err
		}
		result[key] = merged
	}
	for key, remoteValue := range remoteObject {
		if desiredOwnership.Children[key] != nil {
			continue
		}
		wasOwned := priorOwnership != nil && priorOwnership.Object && priorOwnership.Children[key] != nil
		if wasOwned {
			// Nested removals are safe because this complete top-level object is
			// replaced by LiteLLM's shallow dictionary merge.
			continue
		}
		childMode := credentialChildMasking(maskMode == credentialMaskObject, key, remoteValue)
		if containsUnownedCredentialMask(remoteValue, childMode) {
			return nil, errors.New("an unmanaged masked nested credential value cannot be reconstructed for PATCH")
		}
		result[key] = remoteValue
	}
	return result, nil
}

func containsUnownedCredentialMask(value interface{}, maskMode credentialMaskMode) bool {
	if maskMode == credentialMaskScalar {
		text, ok := value.(string)
		return ok && isLiteLLMCredentialMask(text)
	}
	if maskMode != credentialMaskObject {
		return false
	}
	object, ok := value.(map[string]interface{})
	if !ok {
		return false
	}
	for key, child := range object {
		if containsUnownedCredentialMask(child, credentialChildMasking(true, key, child)) {
			return true
		}
	}
	return false
}

// validateCredentialOwnedAtomicPreconditions makes PATCH compare-and-set-like
// for every previously owned atomic leaf. LiteLLM v1.98 does not expose a
// revision token, so an unmasked value must equal prior state and a mask must
// be exactly the mask generated from the prior configured secret.
func validateCredentialOwnedAtomicPreconditions(remote, prior map[string]interface{}, ownership *credentialOwnership, masked bool) error {
	if ownership == nil {
		return nil
	}
	for key, node := range ownership.Children {
		remoteValue, remoteExists := remote[key]
		priorValue, priorExists := prior[key]
		if !remoteExists || !priorExists {
			return errors.New("an owned credential value is missing from the PATCH preflight")
		}
		if err := validateCredentialOwnedAtomicValue(remoteValue, priorValue, node, credentialChildMasking(masked, key, remoteValue)); err != nil {
			return err
		}
	}
	return nil
}

func validateCredentialOwnedAtomicValue(remote, prior interface{}, ownership *credentialOwnership, maskMode credentialMaskMode) error {
	if ownership == nil {
		return errors.New("credential ownership metadata is incomplete")
	}
	if ownership.Object {
		remoteObject, remoteOK := remote.(map[string]interface{})
		priorObject, priorOK := prior.(map[string]interface{})
		if !remoteOK || !priorOK {
			return errors.New("an owned credential object changed shape before PATCH")
		}
		return validateCredentialOwnedAtomicPreconditions(remoteObject, priorObject, ownership, maskMode == credentialMaskObject)
	}
	if !ownership.Atomic {
		return errors.New("credential ownership metadata does not describe an atomic PATCH precondition")
	}
	if _, remoteIsObject := remote.(map[string]interface{}); remoteIsObject {
		return errors.New("an owned atomic credential value changed to an object before PATCH")
	}
	if maskMode == credentialMaskScalar {
		if remoteText, ok := remote.(string); ok && isLiteLLMCredentialMask(remoteText) {
			priorText, priorOK := prior.(string)
			if !priorOK || isLiteLLMCredentialMask(priorText) || remoteText != maskLiteLLMCredentialString(priorText) {
				return errors.New("a credential mask does not match the prior owned value before PATCH")
			}
			return nil
		}
	}
	if !reflect.DeepEqual(remote, prior) {
		return errors.New("an owned credential value changed outside Terraform before PATCH")
	}
	return nil
}

func credentialRemoteFullyOwned(remote, prior map[string]interface{}, ownership *credentialOwnership, masked bool) bool {
	if ownership == nil {
		return len(remote) == 0
	}
	for key, remoteValue := range remote {
		node := ownership.Children[key]
		if node == nil {
			return false
		}
		priorValue := prior[key]
		if node.Object {
			remoteObject, ok := remoteValue.(map[string]interface{})
			if !ok {
				return false
			}
			priorObject, _ := priorValue.(map[string]interface{})
			if !credentialRemoteFullyOwned(remoteObject, priorObject, node, credentialChildMasking(masked, key, remoteValue) == credentialMaskObject) {
				return false
			}
			continue
		}
		if !node.Atomic {
			return false
		}
		if _, remoteIsObject := remoteValue.(map[string]interface{}); remoteIsObject {
			return false
		}
		mode := credentialChildMasking(masked, key, remoteValue)
		if mode == credentialMaskScalar {
			remoteText, remoteOK := remoteValue.(string)
			priorText, priorOK := priorValue.(string)
			if remoteOK && isLiteLLMCredentialMask(remoteText) && (!priorOK || isLiteLLMCredentialMask(priorText) || remoteText != maskLiteLLMCredentialString(priorText)) {
				return false
			}
		}
	}
	return true
}

func verifyCredentialPostflight(remote, prior, desired map[string]interface{}, priorOwnership, desiredOwnership *credentialOwnership, masked bool) error {
	if err := verifyCredentialOwnedObject(remote, desired, desiredOwnership, masked); err != nil {
		return err
	}
	if err := verifyCredentialNestedRemovals(remote, priorOwnership, desiredOwnership, true); err != nil {
		return err
	}
	return nil
}

func verifyCredentialOwnedObject(remote, desired map[string]interface{}, ownership *credentialOwnership, masked bool) error {
	for key, node := range ownership.Children {
		remoteValue, exists := remote[key]
		if !exists {
			return errors.New("LiteLLM did not return an owned credential key after mutation")
		}
		if err := verifyCredentialOwnedValue(remoteValue, desired[key], node, credentialChildMasking(masked, key, remoteValue)); err != nil {
			return err
		}
	}
	return nil
}

func verifyCredentialOwnedValue(remote, desired interface{}, ownership *credentialOwnership, maskMode credentialMaskMode) error {
	if ownership.Object {
		remoteObject, remoteOK := remote.(map[string]interface{})
		desiredObject, desiredOK := desired.(map[string]interface{})
		if !remoteOK || !desiredOK {
			return errors.New("LiteLLM did not preserve an owned credential object shape")
		}
		return verifyCredentialOwnedObject(remoteObject, desiredObject, ownership, maskMode == credentialMaskObject)
	}
	if maskMode == credentialMaskScalar {
		remoteText, remoteOK := remote.(string)
		desiredText, desiredOK := desired.(string)
		if remoteOK && desiredOK && (remoteText == desiredText || remoteText == maskLiteLLMCredentialString(desiredText)) {
			return nil
		}
	}
	if !reflect.DeepEqual(remote, desired) {
		return errors.New("LiteLLM did not return an owned credential value after mutation")
	}
	return nil
}

func verifyCredentialNestedRemovals(remote map[string]interface{}, prior, desired *credentialOwnership, root bool) error {
	if prior == nil {
		return nil
	}
	for key, priorNode := range prior.Children {
		desiredNode := (*credentialOwnership)(nil)
		if desired != nil {
			desiredNode = desired.Children[key]
		}
		if desiredNode == nil {
			if root {
				return errors.New("an owned top-level credential key removal reached postflight unexpectedly")
			}
			if _, exists := remote[key]; exists {
				return errors.New("LiteLLM did not remove an owned nested credential key")
			}
			continue
		}
		if priorNode.Object && desiredNode.Object {
			remoteObject, ok := remote[key].(map[string]interface{})
			if !ok {
				continue
			}
			if err := verifyCredentialNestedRemovals(remoteObject, priorNode, desiredNode, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendCredentialPrivateDiagnostic(diagnostics *diag.Diagnostics, err error) {
	if err != nil {
		diagnostics.AddError("Credential Ownership Metadata Error", "The provider could not preserve credential ownership metadata safely.")
	}
}

func credentialReplacementPaths(state, plan CredentialResourceModel) path.Paths {
	var result path.Paths
	if state.CredentialName.IsUnknown() || plan.CredentialName.IsUnknown() || !state.CredentialName.Equal(plan.CredentialName) {
		result = append(result, path.Root("credential_name"))
	}
	if state.ModelID.IsUnknown() || plan.ModelID.IsUnknown() || !state.ModelID.Equal(plan.ModelID) {
		result = append(result, path.Root("model_id"))
	}
	return result
}

func credentialKnownModelSource(modelID types.String) bool {
	return !modelID.IsNull() && !modelID.IsUnknown() && modelID.ValueString() != ""
}

// Configured empty values are still a known source declaration. This matters
// for imports: an empty map must not be confused with omission even though it
// is not sufficient for a values-only create on LiteLLM v1.98.
func credentialConfigHasKnownSource(info CredentialResourceModel) bool {
	model := credentialKnownModelSource(info.ModelID)
	legacyValues := !info.CredentialValues.IsNull() && !info.CredentialValues.IsUnknown()
	jsonValues := !info.CredentialValuesJSON.IsNull() && !info.CredentialValuesJSON.IsUnknown()
	return model || legacyValues || jsonValues
}

func credentialConfigHasSource(info CredentialResourceModel) bool {
	return credentialConfigHasKnownSource(info) || credentialConfigHasUnknownSource(info)
}

func credentialConfigHasUnknownSource(info CredentialResourceModel) bool {
	return info.ModelID.IsUnknown() || info.CredentialValues.IsUnknown() || info.CredentialValuesJSON.IsUnknown()
}

func formatCredentialSafetyError(_ error) string {
	// Credential errors deliberately omit paths, keys, values, and response
	// bodies because all can carry secret material.
	return "The operation was not sent because credential ownership, object shape, or masked values could not be preserved safely."
}

func credentialConflictDiagnostic(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("Credential configuration is unsafe: %s.", err.Error())
}
