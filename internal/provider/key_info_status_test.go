package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// keyArchivedInfoBody mirrors LiteLLM 1.104.0's /key/info response for a
// deleted or regenerated key: HTTP 200 with the archived row.
func keyArchivedInfoBody(t *testing.T, lookup string, blocked interface{}) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"key": lookup,
		"info": map[string]interface{}{
			"status":             "deleted",
			"blocked":            blocked,
			"key_alias":          "archived-alias-secret",
			"deleted_at":         "2026-10-05T12:00:00Z",
			"deleted_by":         "deleter-secret",
			"deleted_by_api_key": "deleter-key-secret",
			"access_group_ids":   []interface{}{"stale-group"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestClassifyKeyInfoStatus(t *testing.T) {
	for _, test := range []struct {
		name string
		info map[string]interface{}
		want error
	}{
		{"v1.98 row without status", map[string]interface{}{"blocked": false}, nil},
		{"active", map[string]interface{}{"status": "active"}, nil},
		{"expired row still exists", map[string]interface{}{"status": "expired"}, nil},
		{"revoked is a blocked row", map[string]interface{}{"status": "revoked", "blocked": true}, nil},
		{"null deletion markers", map[string]interface{}{"status": "active", "deleted_at": nil, "deleted_by": nil}, nil},
		{"deleted", map[string]interface{}{"status": "deleted", "deleted_at": "2026-10-05T12:00:00Z"}, errKeyInfoArchived},
		{"deleted without markers", map[string]interface{}{"status": "deleted"}, errKeyInfoArchived},
		{"status-less archived row", map[string]interface{}{"deleted_at": "2026-10-05T12:00:00Z"}, errKeyInfoArchived},
		{"live status with deletion marker", map[string]interface{}{"status": "active", "deleted_by": "someone"}, errKeyInfoStatusInvalid},
		{"unknown status", map[string]interface{}{"status": "suspended"}, errKeyInfoStatusInvalid},
		{"non-string status", map[string]interface{}{"status": true}, errKeyInfoStatusInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyKeyInfoStatus(test.info); !errors.Is(got, test.want) || (test.want == nil && got != nil) {
				t.Fatalf("classifyKeyInfoStatus=%v want %v", got, test.want)
			}
		})
	}
	if !isKeyInfoAbsence(errKeyInfoArchived) || isKeyInfoAbsence(errKeyInfoStatusInvalid) {
		t.Fatal("only the archived row is typed absence")
	}
}

func TestKeyResourceTreatsArchivedDeletedKeyAsAbsent(t *testing.T) {
	ctx := context.Background()
	const raw = "sk-archived-protocol-key"
	mode := "archived"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet || request.URL.Path != "/key/info" {
			http.Error(writer, `{}`, http.StatusBadRequest)
			return
		}
		switch mode {
		case "archived":
			_, _ = writer.Write(keyArchivedInfoBody(t, raw, nil))
		case "unknown-status":
			_, _ = writer.Write([]byte(`{"key":"` + raw + `","info":{"status":"suspended","blocked":false}}`))
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_key"]
	prior := keySafeReadProtocolPriorState(t, schema, raw)

	response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_key", CurrentState: prior})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
		t.Fatalf("archived read err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
	}
	if value, err := response.NewState.Unmarshal(schema.ValueType()); err != nil || !value.IsNull() {
		t.Fatalf("archived deleted key stayed in state: %v err=%v", value, err)
	}

	mode = "unknown-status"
	response, err = protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_key", CurrentState: prior})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
		t.Fatalf("unknown status was accepted: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(response.Diagnostics))
	}
	assertKeyRawStateUnchanged(t, prior, response.NewState)
	if text := agentProtocolDiagnosticsText(response.Diagnostics); strings.Contains(text, raw) || strings.Contains(text, "suspended") {
		t.Fatalf("diagnostic leaked response content: %s", text)
	}
}

func TestKeyBlockArchivedDeletedKeyIsAbsentEvenWhenBlocked(t *testing.T) {
	ctx := context.Background()
	const raw = "sk-archived-block"
	bare := strings.TrimPrefix(hashKeyForID(raw), "sha256:")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		// The archived row of a key deleted while blocked still says blocked=true.
		_, _ = writer.Write(keyArchivedInfoBody(t, bare, true))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_key_block"]
	prior := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"id": hashKeyForID(raw), "key": raw, "blocked": true}))
	response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_key_block", CurrentState: prior})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d diagnostics=%s", err, calls.Load(), agentProtocolDiagnosticsText(response.Diagnostics))
	}
	if value, _ := response.NewState.Unmarshal(schema.ValueType()); !value.IsNull() {
		t.Fatalf("block on a deleted key stayed in state: %v", value)
	}
}

func TestKeyDataSourceRejectsArchivedDeletedKey(t *testing.T) {
	ctx := context.Background()
	const raw = "sk-archived-data-source"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(keyArchivedInfoBody(t, raw, false))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.DataSourceSchemas["litellm_key"]
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"key": raw}))
	response, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_key", Config: config})
	if err != nil {
		t.Fatal(err)
	}
	text := agentProtocolDiagnosticsText(response.Diagnostics)
	if !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) || !strings.Contains(text, "Key Not Found") {
		t.Fatalf("archived key was readable: %s", text)
	}
	for _, secret := range []string{raw, "archived-alias-secret", "deleter-secret", "deleter-key-secret"} {
		if strings.Contains(text, secret) {
			t.Fatalf("diagnostic leaked %q: %s", secret, text)
		}
	}
}

func TestUnifiedAccessGroupMembershipRejectsArchivedKey(t *testing.T) {
	const raw = "sk-archived-membership"
	bare := strings.TrimPrefix(hashKeyForID(raw), "sha256:")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(keyArchivedInfoBody(t, bare, false))
	}))
	defer server.Close()
	r := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "sk-test", HTTPClient: server.Client()}}
	groups, err := r.readUnifiedAccessGroupKeyMembership(context.Background(), bare)
	if !errors.Is(err, errKeyInfoArchived) || groups != nil {
		t.Fatalf("archived key proved stale membership: groups=%v err=%v", groups, err)
	}
}
