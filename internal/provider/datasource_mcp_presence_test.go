package provider

import (
	"encoding/json"
	"testing"
)

func TestProjectMCPServerDataSourceV198Presence(t *testing.T) {
	t.Parallel()

	response := map[string]interface{}{
		"server_id":          "presence-mcp",
		"server_name":        "",
		"alias":              nil,
		"description":        "server",
		"url":                "",
		"spec_path":          nil,
		"transport":          "http",
		"auth_type":          "none",
		"mcp_access_groups":  []interface{}{},
		"mcp_info":           map[string]interface{}{"access": false, "nested": []interface{}{json.Number("9007199254740993"), nil}},
		"command":            nil,
		"args":               []interface{}{},
		"env":                map[string]interface{}{},
		"allowed_tools":      []interface{}{"search"},
		"extra_headers":      []interface{}{},
		"static_headers":     map[string]interface{}{},
		"authorization_url":  "",
		"token_url":          nil,
		"registration_url":   "",
		"allow_all_keys":     false,
		"created_at":         nil,
		"created_by":         "",
		"updated_at":         "2026-08-26T00:00:00Z",
		"updated_by":         nil,
		"status":             "unknown",
		"last_health_check":  nil,
		"health_check_error": "",
	}
	state, err := projectMCPServerDataSource(response, "presence-mcp")
	if err != nil {
		t.Fatal(err)
	}
	if state.ID.ValueString() != "presence-mcp" || state.ServerID.ValueString() != "presence-mcp" || state.Transport.ValueString() != "http" {
		t.Fatal("required identity or transport was not projected exactly")
	}
	if state.ServerName.IsNull() || state.ServerName.ValueString() != "" || !state.Alias.IsNull() {
		t.Fatal("empty and null strings were collapsed")
	}
	if state.AllowAllKeys.IsNull() || state.AllowAllKeys.ValueBool() {
		t.Fatal("explicit false was not projected as known false")
	}
	for name, value := range map[string]interface {
		IsNull() bool
		IsUnknown() bool
	}{
		"mcp_access_groups": state.MCPAccessGroups,
		"args":              state.Args,
		"env":               state.Env,
		"extra_headers":     state.ExtraHeaders,
		"static_headers":    state.StaticHeaders,
	} {
		if value.IsNull() || value.IsUnknown() {
			t.Fatalf("explicit empty %s was not known", name)
		}
	}
	if got := state.MCPInfoJSON.ValueString(); got != `{"access":false,"nested":[9007199254740993,null]}` {
		t.Fatalf("mcp_info_json = %q", got)
	}
	if !state.SpecVersion.IsNull() || state.SpecVersion.IsUnknown() {
		t.Fatal("unreturned compatibility field was not a typed null")
	}
}

func TestProjectMCPServerDataSourceNullableAbsenceAndMasking(t *testing.T) {
	t.Parallel()

	for name, response := range map[string]map[string]interface{}{
		"absent":               {"server_id": "presence-mcp", "transport": "stdio"},
		"null":                 {"server_id": "presence-mcp", "transport": "stdio", "server_name": nil, "mcp_access_groups": nil, "args": nil, "env": nil, "allow_all_keys": nil, "mcp_info": nil},
		"restricted singleton": {"server_id": "presence-mcp", "transport": "stdio", "mcp_info": map[string]interface{}{"is_public": true}},
	} {
		t.Run(name, func(t *testing.T) {
			state, err := projectMCPServerDataSource(response, "presence-mcp")
			if err != nil {
				t.Fatal(err)
			}
			if !state.ServerName.IsNull() || !state.MCPAccessGroups.IsNull() || !state.Args.IsNull() || !state.Env.IsNull() || !state.AllowAllKeys.IsNull() || !state.MCPInfoJSON.IsNull() {
				t.Fatal("nullable absence or masking did not resolve to typed nulls")
			}
		})
	}

	state, err := projectMCPServerDataSource(map[string]interface{}{
		"server_id": "presence-mcp", "transport": "stdio", "mcp_info": map[string]interface{}{"is_public": false},
	}, "presence-mcp")
	if err != nil || state.MCPInfoJSON.ValueString() != `{"is_public":false}` {
		t.Fatalf("complete is_public object was masked: value=%q err=%v", state.MCPInfoJSON.ValueString(), err)
	}
}

func TestProjectMCPServerDataSourceRejectsMalformedShapes(t *testing.T) {
	t.Parallel()

	secret := "response-secret-mcp"
	tests := map[string]map[string]interface{}{
		"wrong identity":        {"server_id": secret, "transport": "http"},
		"missing identity":      {"transport": "http"},
		"wrong transport type":  {"server_id": "presence-mcp", "transport": false},
		"wrong transport value": {"server_id": "presence-mcp", "transport": "websocket"},
		"wrong scalar":          {"server_id": "presence-mcp", "transport": "http", "description": false},
		"wrong boolean":         {"server_id": "presence-mcp", "transport": "http", "allow_all_keys": "false"},
		"wrong list root":       {"server_id": "presence-mcp", "transport": "http", "args": map[string]interface{}{}},
		"late list element":     {"server_id": "presence-mcp", "transport": "http", "allowed_tools": []interface{}{"valid", false}},
		"wrong map root":        {"server_id": "presence-mcp", "transport": "http", "env": []interface{}{}},
		"late map element":      {"server_id": "presence-mcp", "transport": "http", "static_headers": map[string]interface{}{"valid": "value", "invalid": false}},
		"wrong mcp info root":   {"server_id": "presence-mcp", "transport": "http", "mcp_info": []interface{}{secret}},
	}
	for name, response := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := projectMCPServerDataSource(response, "presence-mcp"); err == nil {
				t.Fatal("malformed MCP response was accepted")
			}
		})
	}
}
