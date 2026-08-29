package provider

import (
	"context"
	"encoding/json"
	"testing"

	frameworkresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestKeySemanticDictionarySchemaAndDirectUpgrades(t *testing.T) {
	ctx := context.Background()
	var schemaResponse frameworkresource.SchemaResponse
	(&KeyResource{}).Schema(ctx, frameworkresource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Schema.Version != 2 {
		t.Fatalf("schema version = %d, want 2", schemaResponse.Schema.Version)
	}
	for _, name := range []string{"metadata_json", "config_json", "permissions_json"} {
		attribute, ok := schemaResponse.Schema.Attributes[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		stringAttribute := attribute.(interface {
			IsOptional() bool
			IsComputed() bool
			IsSensitive() bool
		})
		if !stringAttribute.IsOptional() || !stringAttribute.IsComputed() || !stringAttribute.IsSensitive() {
			t.Fatalf("%s does not have Optional+Computed+Sensitive semantics", name)
		}
	}

	upgraders := (&KeyResource{}).UpgradeState(ctx)
	for _, version := range []int64{0, 1} {
		state := map[string]interface{}{"id": "sha256:already", "key": "sk-old", "metadata": map[string]interface{}{"remote": "value"}}
		if version == 0 {
			state["id"] = "sk-old"
		}
		raw, _ := json.Marshal(state)
		response := frameworkresource.UpgradeStateResponse{}
		upgraders[version].StateUpgrader(ctx, frameworkresource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: raw}}, &response)
		if response.Diagnostics.HasError() || response.DynamicValue == nil {
			t.Fatalf("v%d upgrade failed: %v", version, response.Diagnostics.Errors())
		}
		var upgraded map[string]interface{}
		if err := json.Unmarshal(response.DynamicValue.JSON, &upgraded); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"metadata_json", "config_json", "permissions_json"} {
			value, present := upgraded[name]
			if !present || value != nil {
				t.Fatalf("v%d %s = %#v, want typed JSON null", version, name, value)
			}
		}
		if upgraded["metadata"].(map[string]interface{})["remote"] != "value" {
			t.Fatal("legacy state changed during upgrade")
		}
	}
}

func TestKeySemanticDictionaryValueIdentityAndValidation(t *testing.T) {
	ctx := context.Background()
	raw := `{"integer":9007199254740993,"decimal":0.5,"string":"true","native":true,"null":null,"nested":{"empty":{},"list":[null,false,"1",1]}}`
	object, provenance, err := keySemanticDictionaryConfiguration(ctx, types.StringValue(raw), types.MapNull(types.StringType), nil)
	if err != nil || !provenance.Configured {
		t.Fatalf("configuration: %#v %v", provenance, err)
	}
	canonical, err := canonicalSemanticDictionary(ctx, object)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := parseSemanticDictionary(ctx, canonical)
	if err != nil {
		t.Fatal(err)
	}
	equal, err := semanticDictionaryValuesEqual(ctx, object, reparsed)
	if err != nil || !equal {
		t.Fatalf("identity changed: equal=%v err=%v", equal, err)
	}
	emptyObject, emptyProvenance, err := keySemanticDictionaryConfiguration(ctx, types.StringValue(`{}`), types.MapNull(types.StringType), nil)
	if err != nil || len(emptyObject) != 0 || !emptyProvenance.Configured || len(emptyProvenance.TerraformOwned) != 0 {
		t.Fatalf("root empty object lost configured ownership: object=%#v provenance=%#v err=%v", emptyObject, emptyProvenance, err)
	}
	if _, _, err := keySemanticDictionaryConfiguration(ctx, types.StringValue(`{"x":1,"x":2}`), types.MapNull(types.StringType), nil); err == nil {
		t.Fatal("duplicate object member was accepted")
	}
	if _, _, err := keySemanticDictionaryConfiguration(ctx, types.StringValue(`null`), types.MapNull(types.StringType), nil); err == nil {
		t.Fatal("root null was accepted")
	}
	if _, _, err := keySemanticDictionaryConfiguration(ctx, types.StringValue(`{"lossy":0.10000000000000001}`), types.MapNull(types.StringType), nil); err == nil {
		t.Fatal("decimal changed by Python float persistence was accepted")
	}
	if _, _, err := keySemanticDictionaryConfiguration(ctx, types.StringValue(`{"tags":[]}`), types.MapNull(types.StringType), keyMetadataJSONReservedKeys); err == nil {
		t.Fatal("reserved metadata key was accepted")
	}
	legacy, diagnostics := types.MapValueFrom(ctx, types.StringType, map[string]string{"owned": "true"})
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if _, _, err := keySemanticDictionaryConfiguration(ctx, types.StringValue(`{"owned":true}`), legacy, nil); err == nil {
		t.Fatal("legacy overlap was accepted")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if object, provenance, err := keySemanticDictionaryConfiguration(canceled, types.StringValue(`{"owned":true}`), types.MapNull(types.StringType), nil); err == nil || object != nil || provenance.Initialized {
		t.Fatalf("canceled configuration escaped partial state: object=%#v provenance=%#v err=%v", object, provenance, err)
	}
}

func TestKeySemanticDictionaryExactIdentityBinding(t *testing.T) {
	const raw = "sk-key-identity"
	hash, _ := keyHashFromID(hashKeyForID(raw))
	if err := validateKeyCreateResponseIdentity(map[string]interface{}{"key": raw, "token": hash}, raw); err != nil {
		t.Fatalf("valid create identity: %v", err)
	}
	for _, result := range []map[string]interface{}{
		{"key": "different", "token": hash},
		{"key": raw, "token": "different"},
		{"token": hash},
	} {
		if err := validateKeyCreateResponseIdentity(result, raw); err == nil {
			t.Fatalf("contradictory create identity accepted: %#v", result)
		}
	}
	validResult := map[string]interface{}{"key": raw}
	validInfo := map[string]interface{}{}
	if err := validateExactKeyInfoIdentity(validResult, validInfo, raw); err != nil {
		t.Fatalf("valid info identity: %v", err)
	}
	for _, test := range []struct {
		result map[string]interface{}
		info   map[string]interface{}
	}{
		{map[string]interface{}{"key": "different"}, validInfo},
		{validResult, map[string]interface{}{"token": "different"}},
		{validResult, map[string]interface{}{"key": "different"}},
	} {
		if err := validateExactKeyInfoIdentity(test.result, test.info, raw); err == nil {
			t.Fatalf("contradictory info identity accepted: result=%#v info=%#v", test.result, test.info)
		}
	}
}

func TestKeySemanticDictionaryLegacyProjectionAndRemovalVerification(t *testing.T) {
	ctx := context.Background()
	configured := func(raw string) semanticDictionaryProvenance {
		_, provenance, err := keySemanticDictionaryConfiguration(ctx, types.StringValue(raw), types.MapNull(types.StringType), nil)
		if err != nil {
			t.Fatal(err)
		}
		return provenance
	}
	ownership := keySemanticReadOwnership{
		metadata:    configured(`{"native":{"enabled":true}}`),
		config:      configured(`{"count":9007199254740993}`),
		permissions: configured(`{"rules":[null,false]}`),
	}
	info := map[string]interface{}{
		"metadata":    map[string]interface{}{"native": map[string]interface{}{"enabled": true}, "legacy": "kept"},
		"config":      map[string]interface{}{"count": json.Number("9007199254740993"), "legacy": "kept"},
		"permissions": map[string]interface{}{"rules": []interface{}{nil, false}, "legacy": "kept"},
	}
	legacyMap, diagnostics := types.MapValueFrom(ctx, types.StringType, map[string]string{"legacy": "kept"})
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	data := KeyResourceModel{Metadata: legacyMap, Config: legacyMap, Permissions: legacyMap}
	legacy, err := keyLegacyDictionaryProjectionInfo(ctx, info, &data, ownership)
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"metadata", "config", "permissions"} {
		object := legacy[root].(map[string]interface{})
		if len(object) != 1 || object["legacy"] != "kept" {
			t.Fatalf("%s legacy projection = %#v", root, object)
		}
	}

	prior := semanticDictionaryPathSet{"/nested/remove": true, "/replaced/old": true}
	next := semanticDictionaryPathSet{"/nested/keep": true, "/replaced": true}
	removals, err := keySemanticDictionaryRemovedPaths(ctx, prior, next)
	if err != nil || len(removals) != 2 || !removals["/nested/remove"] || !removals["/replaced/old"] {
		t.Fatalf("removals=%#v err=%v", removals, err)
	}
	if err := verifyKeySemanticDictionaryRemovals(ctx, map[string]interface{}{"nested": map[string]interface{}{"keep": true}, "replaced": "new"}, removals); err != nil {
		t.Fatalf("confirmed removals failed: %v", err)
	}
	if err := verifyKeySemanticDictionaryRemovals(ctx, map[string]interface{}{"nested": map[string]interface{}{"remove": "stale"}}, removals); err == nil {
		t.Fatal("stale removed leaf was accepted")
	}
}

func TestKeySemanticPendingRemovalReconciliation(t *testing.T) {
	ctx := context.Background()
	priorProvenance := keyUnconfiguredSemanticDictionaryProvenance()
	priorProvenance.Configured = true
	priorProvenance.TerraformOwned = semanticDictionaryPathSet{"/owned/keep": true}
	pending := keySemanticPendingTransition{
		Metadata: keySemanticPendingRoot{
			Active: true, Configured: false,
			TerraformOwned: semanticDictionaryPathSet{},
			Removals:       semanticDictionaryPathSet{"/owned/keep": true},
		},
	}
	prior := keySemanticReadOwnership{metadata: priorProvenance, config: keyUnconfiguredSemanticDictionaryProvenance(), permissions: keyUnconfiguredSemanticDictionaryProvenance(), pending: pending}

	effective, result, err := resolveKeySemanticPendingTransition(ctx, map[string]interface{}{
		"metadata": map[string]interface{}{"owned": map[string]interface{}{"keep": json.Number("1")}},
	}, prior)
	if err != nil || !result.Present || result.Committed || !effective.metadata.Configured || len(effective.metadataRemovals) != 0 {
		t.Fatalf("non-committed reconciliation: effective=%#v result=%#v err=%v", effective, result, err)
	}

	effective, result, err = resolveKeySemanticPendingTransition(ctx, map[string]interface{}{
		"metadata": map[string]interface{}{"api": true},
	}, prior)
	if err != nil || !result.Present || !result.Committed || effective.metadata.Configured || len(effective.metadataRemovals) != 1 {
		t.Fatalf("committed reconciliation: effective=%#v result=%#v err=%v", effective, result, err)
	}

	for _, shape := range []struct {
		name               string
		priorPath          string
		priorJSON          string
		target             semanticDictionaryPathSet
		remote             map[string]interface{}
		projectionRemovals int
	}{
		{"contraction", "/node/leaf", `{"node":{"leaf":1}}`, semanticDictionaryPathSet{"/node": true}, map[string]interface{}{"node": json.Number("2")}, 1},
		{"expansion", "/node", `{"node":1}`, semanticDictionaryPathSet{"/node/leaf": true}, map[string]interface{}{"node": map[string]interface{}{"leaf": json.Number("2")}}, 0},
	} {
		t.Run(shape.name, func(t *testing.T) {
			removals := semanticDictionaryPathSet{shape.priorPath: true}
			committed, err := keySemanticPendingCommitted(ctx, shape.remote, shape.target, removals)
			if err != nil || !committed {
				t.Fatalf("shape replacement was not confirmed: committed=%t err=%v", committed, err)
			}
			shapePrior := keyUnconfiguredSemanticDictionaryProvenance()
			shapePrior.Configured = true
			shapePrior.TerraformOwned = semanticDictionaryPathSet{shape.priorPath: true}
			shapeOwnership := keySemanticReadOwnership{
				metadata: shapePrior, config: keyUnconfiguredSemanticDictionaryProvenance(), permissions: keyUnconfiguredSemanticDictionaryProvenance(),
				pending: keySemanticPendingTransition{Metadata: keySemanticPendingRoot{Active: true, Configured: true, TerraformOwned: shape.target, Removals: removals}},
			}
			targetProvenance := keyUnconfiguredSemanticDictionaryProvenance()
			targetProvenance.Configured = true
			targetProvenance.TerraformOwned = shape.target
			confirmation, err := (keySemanticPrepared{
				metadataProvenance: targetProvenance,
				configProvenance:   keyUnconfiguredSemanticDictionaryProvenance(), permissionsProvenance: keyUnconfiguredSemanticDictionaryProvenance(),
			}).updateOwnership(ctx, keySemanticReadOwnership{metadata: shapePrior, config: keyUnconfiguredSemanticDictionaryProvenance(), permissions: keyUnconfiguredSemanticDictionaryProvenance()})
			if err != nil || len(confirmation.metadataTransitionRemovals) != 1 || len(confirmation.metadataRemovals) != shape.projectionRemovals {
				t.Fatalf("shape confirmation removals: confirmation=%#v err=%v", confirmation, err)
			}
			info := map[string]interface{}{"metadata": shape.remote, "config": map[string]interface{}{}, "permissions": map[string]interface{}{}}
			effective, result, err := resolveKeySemanticPendingTransition(ctx, info, shapeOwnership)
			if err != nil || !result.Committed {
				t.Fatalf("shape ownership reconciliation failed: result=%#v err=%v", result, err)
			}
			data := KeyResourceModel{
				MetadataJSON: types.StringValue(shape.priorJSON), ConfigJSON: types.StringNull(), PermissionsJSON: types.StringNull(),
				Metadata: types.MapNull(types.StringType), Config: types.MapNull(types.StringType), Permissions: types.MapNull(types.StringType),
			}
			if err := projectKeySemanticDictionariesFromInfo(ctx, &data, info, effective); err != nil {
				t.Fatalf("shape projection failed: %v", err)
			}
		})
	}

	expansionTarget := semanticDictionaryPathSet{"/node/a": true, "/node/b": true}
	expansionRemovals := semanticDictionaryPathSet{"/node": true}
	partialExpansion := map[string]interface{}{"node": map[string]interface{}{"a": true}}
	committed, err := keySemanticPendingCommitted(ctx, partialExpansion, expansionTarget, expansionRemovals)
	if err != nil || committed {
		t.Fatalf("partial expansion classified committed: committed=%t err=%v", committed, err)
	}
	notCommitted, err := keySemanticPendingNotCommitted(ctx, partialExpansion, expansionTarget, expansionRemovals)
	if err != nil || notCommitted {
		t.Fatalf("partial expansion classified not committed: notCommitted=%t err=%v", notCommitted, err)
	}
	partialPrior := keyUnconfiguredSemanticDictionaryProvenance()
	partialPrior.Configured = true
	partialPrior.TerraformOwned = semanticDictionaryPathSet{"/node": true}
	_, _, err = resolveKeySemanticPendingTransition(ctx, map[string]interface{}{
		"metadata": partialExpansion,
	}, keySemanticReadOwnership{
		metadata: partialPrior, config: keyUnconfiguredSemanticDictionaryProvenance(), permissions: keyUnconfiguredSemanticDictionaryProvenance(),
		pending: keySemanticPendingTransition{Metadata: keySemanticPendingRoot{Active: true, Configured: true, TerraformOwned: expansionTarget, Removals: expansionRemovals}},
	})
	if err == nil {
		t.Fatal("partial expansion reconciliation did not fail closed")
	}
	mixedTarget := semanticDictionaryPathSet{"/node/a": true, "/node/b": true}
	mixedRemovals := semanticDictionaryPathSet{"/node": true, "/gone": true}
	mixedObserved := map[string]interface{}{"node": map[string]interface{}{"a": true, "b": true}, "gone": true}
	committed, err = keySemanticPendingCommitted(ctx, mixedObserved, mixedTarget, mixedRemovals)
	if err != nil || committed {
		t.Fatalf("mixed expansion classified committed: committed=%t err=%v", committed, err)
	}
	notCommitted, err = keySemanticPendingNotCommitted(ctx, mixedObserved, mixedTarget, mixedRemovals)
	if err != nil || notCommitted {
		t.Fatalf("mixed expansion classified not committed: notCommitted=%t err=%v", notCommitted, err)
	}

	encoded, err := encodeKeySemanticPendingTransition(ctx, pending)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeKeySemanticPendingTransition(ctx, encoded)
	if err != nil || !decoded.Metadata.Active || decoded.Metadata.Configured || len(decoded.Metadata.Removals) != 1 {
		t.Fatalf("pending round trip: decoded=%#v err=%v", decoded, err)
	}
}

func TestKeySemanticDictionaryRemovalAndMaskPolicy(t *testing.T) {
	ctx := context.Background()
	prior := map[string]interface{}{"owned": map[string]interface{}{"keep": "x", "remove": "y"}}
	paths, err := semanticDictionaryLeafPaths(ctx, prior)
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]interface{}{"owned": map[string]interface{}{"keep": "x", "remove": "y", "api": true}, "api_root": "preserved"}
	desired := map[string]interface{}{"owned": map[string]interface{}{"keep": "x"}}
	replacement, owned, err := applySemanticDictionary(ctx, base, desired, paths)
	if err != nil {
		t.Fatal(err)
	}
	ownedObject := replacement["owned"].(map[string]interface{})
	if _, present := ownedObject["remove"]; present || ownedObject["api"] != true || replacement["api_root"] != "preserved" || !owned["/owned/keep"] {
		t.Fatalf("unexpected replacement: %#v owned=%#v", replacement, owned)
	}

	for _, segment := range []string{"password", "secret", "key", "token", "auth", "authorization", "credential", "credentials", "access", "private", "certificate", "fingerprint", "tenancy"} {
		if !keyMetadataCallbackCiphertext([]string{"logging", "0", "callback_vars", "prefix-" + segment + "-suffix"}, "litellm_enc::cipher") {
			t.Fatalf("callback ciphertext segment %q was not recognized", segment)
		}
	}
	for _, special := range []string{"gcs_path_service_account", "GCS_PATH_SERVICE_ACCOUNT"} {
		if !keyMetadataCallbackCiphertext([]string{"callback_settings", "callback_vars", special}, "litellm_enc::cipher") {
			t.Fatalf("special callback credential %q was not recognized", special)
		}
	}
	for _, ordinary := range []string{"api", "account", "public", "monkey", "secretary"} {
		if keyMetadataCallbackCiphertext([]string{"logging", "0", "callback_vars", ordinary}, "litellm_enc::cipher") {
			t.Fatalf("ordinary callback variable %q was treated as ciphertext", ordinary)
		}
	}
	if keyMetadataCallbackCiphertext([]string{"logging", "0", "callback_vars", "input_cost_key"}, "litellm_enc::cipher") {
		t.Fatal("cost callback variable was treated as recoverable ciphertext")
	}
	for _, literal := range []string{" litellm_enc::cipher", "LITELLM_ENC::cipher", "litellm_enc", "litellm_encx::cipher"} {
		if keyMetadataCallbackCiphertext([]string{"logging", "0", "callback_vars", "secret_key"}, literal) {
			t.Fatalf("non-exact ciphertext prefix %q was accepted", literal)
		}
	}
	plaintext := `{"logging":[{"callback_vars":{"secret_key":"plain-value"}}]}`
	_, plaintextProvenance, err := keySemanticDictionaryConfiguration(ctx, types.StringValue(plaintext), types.MapNull(types.StringType), nil)
	if err != nil {
		t.Fatal(err)
	}
	observed := map[string]interface{}{"logging": []interface{}{map[string]interface{}{"callback_vars": map[string]interface{}{"secret_key": "litellm_enc::cipher"}}}}
	projected, err := projectKeySemanticDictionary(ctx, types.StringValue(plaintext), observed, plaintextProvenance, keyMetadataCallbackCiphertext)
	if err != nil || projected.ValueString() != plaintext {
		t.Fatalf("owned ciphertext recovery = %q err=%v", projected.ValueString(), err)
	}
	unowned := map[string]interface{}{"logging": []interface{}{map[string]interface{}{"callback_vars": map[string]interface{}{"secret_key": "litellm_enc::cipher"}}}}
	if err := validateKeyMetadataReplacementCiphertext(ctx, unowned, types.StringNull(), keyUnconfiguredSemanticDictionaryProvenance()); err == nil {
		t.Fatal("unowned callback ciphertext was accepted for replacement")
	}
}
