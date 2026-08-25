package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestKeyBlockSchemaSupportsCompatibleSafeIdentityModes(t *testing.T) {
	t.Parallel()

	keyBlock := &KeyBlockResource{}
	var response resource.SchemaResponse
	keyBlock.Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	if response.Schema.Version != 1 {
		t.Fatalf("schema version = %d, want 1", response.Schema.Version)
	}
	key, ok := response.Schema.Attributes["key"].(resourceschema.StringAttribute)
	if !ok || !key.Optional || key.Required || !key.Sensitive || len(key.PlanModifiers) != 0 {
		t.Fatalf("legacy key schema must remain optional and sensitive: %#v", response.Schema.Attributes["key"])
	}
	keyHash, ok := response.Schema.Attributes["key_hash"].(resourceschema.StringAttribute)
	if !ok || !keyHash.Optional || keyHash.Sensitive || len(keyHash.Validators) != 1 || len(keyHash.PlanModifiers) != 0 {
		t.Fatalf("key_hash schema mismatch: %#v", response.Schema.Attributes["key_hash"])
	}
	if _, ok := response.Schema.Attributes["key_wo"]; ok {
		t.Fatal("key_wo is unnecessary because LiteLLM accepts the non-secret key hash directly")
	}
	if got := len(keyBlock.ConfigValidators(context.Background())); got != 1 {
		t.Fatalf("config validators = %d, want 1", got)
	}
}

func TestKeyBlockIdentityModes(t *testing.T) {
	t.Parallel()

	raw := "sk-special-#&+%-key"
	expectedID := hashKeyForID(raw)
	expectedHash := strings.TrimPrefix(expectedID, "sha256:")

	rawIdentity, err := keyBlockIdentityFromRaw(raw)
	if err != nil {
		t.Fatalf("raw identity: %v", err)
	}
	if rawIdentity.managementID != expectedID || rawIdentity.apiValue != expectedHash {
		t.Fatalf("raw identity = %#v", rawIdentity)
	}

	legacyHashIdentity, err := keyBlockIdentityFromLegacyKey(expectedHash)
	if err != nil {
		t.Fatalf("legacy hash identity: %v", err)
	}
	if legacyHashIdentity.managementID != expectedID || legacyHashIdentity.apiValue != expectedHash {
		t.Fatalf("legacy hash identity = %#v", legacyHashIdentity)
	}

	hashIdentity, err := keyBlockIdentityFromHashID("sha256:" + strings.ToUpper(expectedHash))
	if err != nil {
		t.Fatalf("hash identity: %v", err)
	}
	if hashIdentity.managementID != expectedID || hashIdentity.apiValue != expectedHash {
		t.Fatalf("hash identity = %#v", hashIdentity)
	}

	bareIdentity, err := keyBlockIdentityFromBareHash(strings.ToUpper(expectedHash))
	if err != nil {
		t.Fatalf("bare hash identity: %v", err)
	}
	if bareIdentity != hashIdentity {
		t.Fatalf("bare identity = %#v, want %#v", bareIdentity, hashIdentity)
	}

	for _, invalid := range []string{"", "sha256:nope", "sha256:" + strings.Repeat("z", 64)} {
		if _, err := keyBlockIdentityFromHashID(invalid); err == nil {
			t.Fatalf("keyBlockIdentityFromHashID(%q) succeeded", invalid)
		}
	}
}

func TestKeyBlockStateUsesHashWithoutRawKey(t *testing.T) {
	t.Parallel()

	raw := "sk-state-raw-key"
	id := hashKeyForID(raw)
	identity, err := keyBlockStateIdentity(&KeyBlockResourceModel{
		ID:  types.StringValue(id),
		Key: types.StringValue(raw),
	})
	if err != nil {
		t.Fatalf("state identity: %v", err)
	}
	if identity.apiValue != strings.TrimPrefix(id, "sha256:") {
		t.Fatalf("API identity = %q, want bare hash", identity.apiValue)
	}
	if strings.Contains(identity.apiValue, raw) {
		t.Fatal("state lookup exposed the raw key")
	}
}

func TestKeyBlockInfoEndpointEscapesQueryIdentity(t *testing.T) {
	t.Parallel()

	raw := "sk-fragment-#-amp-&-plus-+-percent-%"
	endpoint := keyBlockInfoEndpoint(raw)
	parsed, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	if got := parsed.Query().Get("key"); got != raw {
		t.Fatalf("decoded key = %q, want %q (endpoint %q)", got, raw, endpoint)
	}
	if strings.Contains(endpoint, "#") || strings.Contains(endpoint, "&-plus") {
		t.Fatalf("endpoint did not escape query delimiters: %q", endpoint)
	}
}

func TestKeyBlockErrorsOmitEchoedKeyMaterial(t *testing.T) {
	t.Parallel()

	secret := "sk-echoed-secret"
	apiMessage := keyBlockOperationError("blocking", &APIError{StatusCode: 400, Body: secret})
	if strings.Contains(apiMessage, secret) || !strings.Contains(apiMessage, "HTTP 400") {
		t.Fatalf("unsafe API diagnostic: %q", apiMessage)
	}
	transportMessage := keyBlockOperationError("reading", errors.New("GET /key/info?key="+url.QueryEscape(secret)))
	if strings.Contains(transportMessage, secret) || strings.Contains(transportMessage, "key=") {
		t.Fatalf("unsafe transport diagnostic: %q", transportMessage)
	}
}

func TestKeyBlockImportForms(t *testing.T) {
	t.Parallel()

	raw := "sk-import-secret"
	id := hashKeyForID(raw)
	bareHash := strings.TrimPrefix(id, "sha256:")
	tests := []struct {
		name        string
		importID    string
		wantID      string
		wantKey     string
		wantKeyHash string
	}{
		{name: "legacy raw", importID: raw, wantID: id, wantKey: raw},
		{name: "management ID", importID: id, wantID: id, wantKeyHash: id},
		{name: "bare hash", importID: bareHash, wantID: id, wantKeyHash: id},
		{name: "uppercase management ID", importID: "sha256:" + strings.ToUpper(bareHash), wantID: id, wantKeyHash: "sha256:" + strings.ToUpper(bareHash)},
		{name: "uppercase bare hash", importID: strings.ToUpper(bareHash), wantID: id, wantKeyHash: "sha256:" + strings.ToUpper(bareHash)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			keyBlock := &KeyBlockResource{}
			var schemaResponse resource.SchemaResponse
			keyBlock.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
			state, err := nullStateFor(schemaResponse.Schema)
			if err != nil {
				t.Fatalf("build state: %v", err)
			}
			response := &resource.ImportStateResponse{State: state}
			keyBlock.ImportState(context.Background(), resource.ImportStateRequest{ID: test.importID}, response)
			if response.Diagnostics.HasError() {
				t.Fatalf("import diagnostics: %v", response.Diagnostics)
			}
			var data KeyBlockResourceModel
			response.Diagnostics.Append(response.State.Get(context.Background(), &data)...)
			if response.Diagnostics.HasError() {
				t.Fatalf("decode import state: %v", response.Diagnostics)
			}
			if data.ID.ValueString() != test.wantID || data.Key.ValueString() != test.wantKey || data.KeyHash.ValueString() != test.wantKeyHash || !data.Blocked.ValueBool() {
				t.Fatalf("imported state = %#v", data)
			}
		})
	}
}

func TestKeyBlockImportRejectsMalformedReservedHashWithoutEcho(t *testing.T) {
	t.Parallel()

	keyBlock := &KeyBlockResource{}
	var schemaResponse resource.SchemaResponse
	keyBlock.Schema(context.Background(), resource.SchemaRequest{}, &schemaResponse)
	state, err := nullStateFor(schemaResponse.Schema)
	if err != nil {
		t.Fatalf("build state: %v", err)
	}
	response := &resource.ImportStateResponse{State: state}
	secretLikeID := "sha256:not-a-valid-secret-hash"
	keyBlock.ImportState(context.Background(), resource.ImportStateRequest{ID: secretLikeID}, response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected malformed reserved hash import to fail")
	}
	if strings.Contains(response.Diagnostics.Errors()[0].Detail(), secretLikeID) {
		t.Fatalf("diagnostic echoed import identity: %q", response.Diagnostics.Errors()[0].Detail())
	}
}

func TestKeyBlockUpgradeStateV0ToV1(t *testing.T) {
	t.Parallel()

	raw := "sk-old-#&+%-id"
	v0JSON, err := json.Marshal(map[string]interface{}{
		"id":      raw,
		"key":     raw,
		"blocked": true,
	})
	if err != nil {
		t.Fatalf("marshal v0 state: %v", err)
	}
	upgrader := (&KeyBlockResource{}).UpgradeState(context.Background())[0]
	response := &resource.UpgradeStateResponse{}
	upgrader.StateUpgrader(context.Background(), resource.UpgradeStateRequest{
		RawState: &tfprotov6.RawState{JSON: v0JSON},
	}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("upgrade diagnostics: %v", response.Diagnostics)
	}
	if response.DynamicValue == nil {
		t.Fatal("upgrader did not return dynamic state")
	}
	var upgraded map[string]interface{}
	if err := json.Unmarshal(response.DynamicValue.JSON, &upgraded); err != nil {
		t.Fatalf("decode upgraded state: %v", err)
	}
	if upgraded["id"] != hashKeyForID(raw) || upgraded["key"] != raw || upgraded["blocked"] != true {
		t.Fatalf("upgraded state = %#v", upgraded)
	}
	if upgraded["key_hash"] != nil {
		t.Fatalf("new attribute was not initialized to null: %#v", upgraded)
	}
}

func TestKeyBlockUpgradePreservesLegacyBareHashIdentity(t *testing.T) {
	t.Parallel()

	bareHash := strings.Repeat("a1", 32)
	v0JSON, err := json.Marshal(map[string]interface{}{
		"id":      bareHash,
		"key":     bareHash,
		"blocked": true,
	})
	if err != nil {
		t.Fatalf("marshal v0 state: %v", err)
	}
	upgrader := (&KeyBlockResource{}).UpgradeState(context.Background())[0]
	response := &resource.UpgradeStateResponse{}
	upgrader.StateUpgrader(context.Background(), resource.UpgradeStateRequest{
		RawState: &tfprotov6.RawState{JSON: v0JSON},
	}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("upgrade diagnostics: %v", response.Diagnostics)
	}
	var upgraded map[string]interface{}
	if err := json.Unmarshal(response.DynamicValue.JSON, &upgraded); err != nil {
		t.Fatalf("decode upgraded state: %v", err)
	}
	if upgraded["id"] != "sha256:"+bareHash {
		t.Fatalf("legacy bare hash was not preserved: %#v", upgraded)
	}
	if upgraded["key"] != bareHash {
		t.Fatalf("legacy key changed: %#v", upgraded)
	}
}

func TestKeyBlockUpgradeErrorsAreSecretSafe(t *testing.T) {
	t.Parallel()

	upgrader := (&KeyBlockResource{}).UpgradeState(context.Background())[0]
	for name, rawState := range map[string]*tfprotov6.RawState{
		"nil":       nil,
		"malformed": {JSON: []byte(`{"id":"sk-secret"`)},
		"empty":     {JSON: []byte(`{"id":""}`)},
	} {
		t.Run(name, func(t *testing.T) {
			response := &resource.UpgradeStateResponse{}
			upgrader.StateUpgrader(context.Background(), resource.UpgradeStateRequest{RawState: rawState}, response)
			if !response.Diagnostics.HasError() {
				t.Fatal("expected upgrade error")
			}
			for _, diagnostic := range response.Diagnostics.Errors() {
				if strings.Contains(diagnostic.Detail(), "sk-secret") {
					t.Fatalf("upgrade diagnostic leaked state: %q", diagnostic.Detail())
				}
			}
		})
	}
}
