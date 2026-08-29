package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestListDataSourceUnknownAndEmptyFiltersFailBeforeHTTP(t *testing.T) {
	tests := []struct {
		typeName string
		field    string
	}{
		{"litellm_keys", "team_id"},
		{"litellm_models", "team_id"},
		{"litellm_organizations", "org_alias"},
		{"litellm_users", "user_role"},
		{"litellm_prompts", "environment"},
	}
	for _, test := range tests {
		t.Run(test.typeName, func(t *testing.T) {
			ctx := context.Background()
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				http.Error(writer, "unexpected request", http.StatusInternalServerError)
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.DataSourceSchemas[test.typeName]
			for name, value := range map[string]interface{}{"unknown": tftypes.UnknownValue, "empty": ""} {
				t.Run(name, func(t *testing.T) {
					config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{test.field: value}))
					read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: test.typeName, Config: config})
					if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
						t.Fatalf("invalid filter was accepted: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
					}
				})
			}
			if requests.Load() != 0 {
				t.Fatalf("invalid filters caused %d HTTP requests", requests.Load())
			}
		})
	}
}

func TestListDataSourceAbsentBlockedRemainsTypedNull(t *testing.T) {
	hash := strings.Repeat("a", sha256ManagementHashLength)
	for name, raw := range map[string][]byte{
		"string key": []byte(`"` + hash + `"`),
		"object key": []byte(`{"token":"` + hash + `"}`),
	} {
		t.Run(name, func(t *testing.T) {
			item, err := decodeKeyListItem(raw)
			if err != nil || !item.Blocked.IsNull() {
				t.Fatalf("absent blocked was fabricated: value=%v err=%v", item.Blocked, err)
			}
		})
	}

	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[{"organization_id":"org-presence"}]`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.DataSourceSchemas["litellm_organizations"]
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{}))
	read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_organizations", Config: config})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("organization read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	var items []tftypes.Value
	if err := protocolAttributeMap(t, schema, read.State)["organizations"].As(&items); err != nil || len(items) != 1 {
		t.Fatalf("organization items: err=%v len=%d", err, len(items))
	}
	attributes := map[string]tftypes.Value{}
	if err := items[0].As(&attributes); err != nil || !attributes["blocked"].IsNull() {
		t.Fatalf("organization absent blocked was fabricated: value=%v err=%v", attributes["blocked"], err)
	}
}

func TestFilteredPromptList404FailsAtomically(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/prompts/list" {
			_, _ = writer.Write([]byte(`{"prompts":[{"prompt_id":"listed"}]}`))
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client := &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}
	if items, err := fetchPromptListItems(context.Background(), client, "production", true); err == nil || items != nil {
		t.Fatalf("listed prompt disappearance produced partial success: items=%#v err=%v", items, err)
	}
}

func TestTagListBudgetDiagnosticsDoNotExposeResponseIdentity(t *testing.T) {
	const sensitiveBudgetID = "response-budget-identifier-must-not-leak"
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`[{"name":"tag","budget_id":null,"litellm_budget_table":{"budget_id":"` + sensitiveBudgetID + `"}}]`))
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.DataSourceSchemas["litellm_tags"]
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{}))
	read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_tags", Config: config})
	text := agentProtocolDiagnosticsText(read.Diagnostics)
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) || strings.Contains(text, sensitiveBudgetID) {
		t.Fatalf("tag diagnostic was not content-safe: err=%v diagnostics=%s", err, text)
	}
}

func TestUserDataSourceRequiresPinnedIdentityEnvelope(t *testing.T) {
	for name, body := range map[string]string{
		"flat":               `{"user_id":"requested"}`,
		"missing root":       `{"user_info":{"user_id":"requested"}}`,
		"conflicting nested": `{"user_id":"requested","user_info":{"user_id":"other"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(body))
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.DataSourceSchemas["litellm_user"]
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"user_id": "requested"}))
			read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: "litellm_user", Config: config})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("non-pinned user envelope accepted: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
			}
		})
	}
}
