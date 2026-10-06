package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func mcpScopesList(values ...string) types.List {
	elements := make([]attr.Value, 0, len(values))
	for _, value := range values {
		elements = append(elements, types.StringValue(value))
	}
	return types.ListValueMust(types.StringType, elements)
}

func TestMCPObservableScopesVerification(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		name     string
		observed map[string]interface{}
		wantErr  bool
	}{
		{"v1.98 redacted credentials", map[string]interface{}{"credentials": nil}, false},
		{"v1.104 without scopes member", map[string]interface{}{"credentials": map[string]interface{}{"upstream_resource": "https://r.invalid"}}, false},
		{"v1.104 matching scopes", map[string]interface{}{"credentials": map[string]interface{}{"scopes": []interface{}{"read", "write"}}}, false},
		{"v1.104 different scopes", map[string]interface{}{"credentials": map[string]interface{}{"scopes": []interface{}{"read"}}}, true},
		{"v1.104 reordered scopes", map[string]interface{}{"credentials": map[string]interface{}{"scopes": []interface{}{"write", "read"}}}, true},
		{"malformed scopes", map[string]interface{}{"credentials": map[string]interface{}{"scopes": "read write"}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := verifyMCPObservableScopes(ctx, mcpScopesList("read", "write"), test.observed)
			if (err != nil) != test.wantErr {
				t.Fatalf("err=%v wantErr=%t", err, test.wantErr)
			}
		})
	}
}

func TestMCPObservableCredentialReadbackVerifiesUpstreamTokenHeader(t *testing.T) {
	ctx := context.Background()
	desired := types.MapValueMust(types.StringType, map[string]attr.Value{"upstream_token_header": types.StringValue("X-Upstream-Token"), "client_secret": types.StringValue("secret-value")})
	matching := map[string]interface{}{"credentials": map[string]interface{}{"upstream_token_header": "X-Upstream-Token", "scopes": []interface{}{"read"}}}
	if err := verifyMCPObservableCredentialReadback(ctx, desired, matching); err != nil {
		t.Fatalf("matching upstream_token_header rejected: %v", err)
	}
	stale := map[string]interface{}{"credentials": map[string]interface{}{"upstream_token_header": "X-Old"}}
	if err := verifyMCPObservableCredentialReadback(ctx, desired, stale); err == nil {
		t.Fatal("stale upstream_token_header accepted")
	}
}

func TestMCPResourceResponseAcceptsV1104CredentialProjection(t *testing.T) {
	projection, err := decodeMCPCredentialProjection(map[string]interface{}{"credentials": map[string]interface{}{
		"upstream_resource": "https://r.invalid", "upstream_token_header": "X-Upstream-Token", "scopes": []interface{}{"read"},
	}}, true)
	if err != nil || !projection.ScopesVisible || projection.Strings["upstream_token_header"] != "X-Upstream-Token" {
		t.Fatalf("projection=%#v err=%v", projection, err)
	}
	if _, err := decodeMCPCredentialProjection(map[string]interface{}{"credentials": map[string]interface{}{"scopes": []interface{}{"read", 1}}}, false); err == nil {
		t.Fatal("non-string scope accepted")
	}
	if _, err := decodeMCPCredentialProjection(map[string]interface{}{"credentials": map[string]interface{}{"client_secret": "x"}}, true); err == nil {
		t.Fatal("strict projection accepted a secret member")
	}
}

func TestValidateMCPServerResponseAcceptsNativeScopesList(t *testing.T) {
	base := func(credentials interface{}) map[string]interface{} {
		return map[string]interface{}{"server_id": "scoped", "transport": "http", "credentials": credentials}
	}
	if err := validateMCPServerResponse(base(map[string]interface{}{"upstream_resource": "https://r.invalid", "scopes": []interface{}{"read"}}), "scoped"); err != nil {
		t.Fatalf("v1.104 scopes list rejected: %v", err)
	}
	if err := validateMCPServerResponse(base(map[string]interface{}{"scopes": []interface{}{"read", false}}), "scoped"); err == nil {
		t.Fatal("malformed scopes accepted")
	}
	if err := validateMCPServerResponse(base(map[string]interface{}{"upstream_resource": false}), "scoped"); err == nil {
		t.Fatal("non-string credential member accepted")
	}
}
