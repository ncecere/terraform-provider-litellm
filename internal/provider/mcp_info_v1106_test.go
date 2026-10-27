package provider

import (
	"testing"
)

// LiteLLM 1.106 overlays computed is_public/is_public_explicit on every MCP
// server response. They are derived, never stored, so they must not reach
// mcp_info_json; the restricted virtual-key view (only those two keys, audit
// fields nulled) stays masked like the v1.98 singleton.
func TestMCPInfoDocumentFromResponseStripsV1106DerivedPublicFlags(t *testing.T) {
	admin := map[string]interface{}{
		"server_id": "s1", "created_at": "2026-10-27T00:00:00Z", "updated_at": "2026-10-27T00:00:00Z",
		"mcp_info": map[string]interface{}{"owner": "platform", "server_name": "s1", "is_public": false, "is_public_explicit": false},
	}
	document, presence, err := mcpInfoDocumentFromResponse(admin)
	if err != nil || presence != apiValuePresent || len(document) != 2 || document["owner"] != "platform" || document["server_name"] != "s1" {
		t.Fatalf("admin view = %#v presence=%v err=%v", document, presence, err)
	}

	emptyAdmin := map[string]interface{}{"server_id": "s1", "created_at": "2026-10-27T00:00:00Z", "mcp_info": map[string]interface{}{"is_public": true, "is_public_explicit": true}}
	document, presence, err = mcpInfoDocumentFromResponse(emptyAdmin)
	if err != nil || presence != apiValuePresent || len(document) != 0 {
		t.Fatalf("admin view of empty stored info = %#v presence=%v err=%v", document, presence, err)
	}

	restricted := map[string]interface{}{"server_id": "s1", "created_at": nil, "updated_at": nil, "mcp_info": map[string]interface{}{"is_public": false, "is_public_explicit": false}}
	document, presence, err = mcpInfoDocumentFromResponse(restricted)
	if err != nil || presence != apiValueNull || document != nil {
		t.Fatalf("restricted view must be masked: %#v presence=%v err=%v", document, presence, err)
	}

	// 1.104 responses never carry is_public_explicit; a stored is_public stays.
	legacy := map[string]interface{}{"server_id": "s1", "created_at": "2026-10-27T00:00:00Z", "mcp_info": map[string]interface{}{"is_public": false, "owner": "x"}}
	document, presence, err = mcpInfoDocumentFromResponse(legacy)
	if err != nil || presence != apiValuePresent || document["is_public"] != false || len(document) != 2 {
		t.Fatalf("1.104 document changed: %#v presence=%v err=%v", document, presence, err)
	}
}
