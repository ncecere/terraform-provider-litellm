package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

// keyInfoServer returns a test server that answers /key/info with the given info payload.
func keyInfoServer(t *testing.T, keyValue string, info map[string]interface{}) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key":  keyValue,
			"info": info,
		})
	}))
	t.Cleanup(server.Close)

	return server
}

func newKeyDataSource(server *httptest.Server) *KeyDataSource {
	return &KeyDataSource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
	}
}

// fallbackChain returns the fallback models configured for primary in m.
func fallbackChain(t *testing.T, m types.Map, primary string) []string {
	t.Helper()

	if m.IsNull() || m.IsUnknown() {
		t.Fatalf("expected known, non-null map, got %v", m)
	}

	lv, ok := m.Elements()[primary]
	if !ok {
		t.Fatalf("expected primary model %q in map, got %v", primary, m.Elements())
	}

	list, ok := lv.(types.List)
	if !ok {
		t.Fatalf("expected types.List for primary model, got %T", lv)
	}

	var fallbacks []string
	list.ElementsAs(context.Background(), &fallbacks, false)
	return fallbacks
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestKeyDataSourceReadReadsRouterSettingsFallbacks(t *testing.T) {
	t.Parallel()

	server := keyInfoServer(t, "sk-ds-fallbacks", map[string]interface{}{
		"token": "sk-ds-fallbacks",
		"router_settings": map[string]interface{}{
			"fallbacks": []interface{}{
				map[string]interface{}{
					"gpt-4o": []interface{}{"gpt-4o-mini", "gpt-3.5-turbo"},
				},
			},
		},
	})

	data := KeyDataSourceModel{Key: types.StringValue("sk-ds-fallbacks")}

	if err := newKeyDataSource(server).readKeyDataSource(context.Background(), &data); err != nil {
		t.Fatalf("readKeyDataSource returned error: %v", err)
	}

	if got := len(data.RouterSettingsFallbacks.Elements()); got != 1 {
		t.Fatalf("expected 1 primary model, got %d", got)
	}
	if got := fallbackChain(t, data.RouterSettingsFallbacks, "gpt-4o"); !equalStrings(got, []string{"gpt-4o-mini", "gpt-3.5-turbo"}) {
		t.Errorf("unexpected fallback values: %v", got)
	}

	// context_window_fallbacks was absent from the response and must stay null.
	if !data.RouterSettingsContextWindowFallbacks.IsNull() {
		t.Error("router_settings_context_window_fallbacks should be null when absent from the response")
	}
}

func TestKeyDataSourceReadReadsContextWindowFallbacks(t *testing.T) {
	t.Parallel()

	server := keyInfoServer(t, "sk-ds-cw-fallbacks", map[string]interface{}{
		"token": "sk-ds-cw-fallbacks",
		"router_settings": map[string]interface{}{
			"context_window_fallbacks": []interface{}{
				map[string]interface{}{
					"claude-sonnet-4.6": []interface{}{"claude-opus-4.8"},
				},
			},
		},
	})

	data := KeyDataSourceModel{Key: types.StringValue("sk-ds-cw-fallbacks")}

	if err := newKeyDataSource(server).readKeyDataSource(context.Background(), &data); err != nil {
		t.Fatalf("readKeyDataSource returned error: %v", err)
	}

	if got := fallbackChain(t, data.RouterSettingsContextWindowFallbacks, "claude-sonnet-4.6"); !equalStrings(got, []string{"claude-opus-4.8"}) {
		t.Errorf("unexpected context window fallback values: %v", got)
	}

	if !data.RouterSettingsFallbacks.IsNull() {
		t.Error("router_settings_fallbacks should be null when absent from the response")
	}
}

func TestKeyDataSourceReadBothFallbackTypes(t *testing.T) {
	t.Parallel()

	server := keyInfoServer(t, "sk-ds-both-fallbacks", map[string]interface{}{
		"token": "sk-ds-both-fallbacks",
		"router_settings": map[string]interface{}{
			"fallbacks": []interface{}{
				map[string]interface{}{
					"gpt-4o": []interface{}{"gpt-4o-mini"},
				},
			},
			"context_window_fallbacks": []interface{}{
				map[string]interface{}{
					"gpt-4o": []interface{}{"gpt-3.5-turbo", "gpt-4o-mini"},
				},
			},
		},
	})

	data := KeyDataSourceModel{Key: types.StringValue("sk-ds-both-fallbacks")}

	if err := newKeyDataSource(server).readKeyDataSource(context.Background(), &data); err != nil {
		t.Fatalf("readKeyDataSource returned error: %v", err)
	}

	if got := fallbackChain(t, data.RouterSettingsFallbacks, "gpt-4o"); !equalStrings(got, []string{"gpt-4o-mini"}) {
		t.Errorf("unexpected fallback values: %v", got)
	}
	if got := fallbackChain(t, data.RouterSettingsContextWindowFallbacks, "gpt-4o"); !equalStrings(got, []string{"gpt-3.5-turbo", "gpt-4o-mini"}) {
		t.Errorf("unexpected context window fallback values: %v", got)
	}
}

func TestKeyDataSourceReadFallbacksNullWhenAbsent(t *testing.T) {
	t.Parallel()

	server := keyInfoServer(t, "sk-ds-no-fallbacks", map[string]interface{}{
		"token": "sk-ds-no-fallbacks",
	})

	data := KeyDataSourceModel{Key: types.StringValue("sk-ds-no-fallbacks")}

	if err := newKeyDataSource(server).readKeyDataSource(context.Background(), &data); err != nil {
		t.Fatalf("readKeyDataSource returned error: %v", err)
	}

	// Unlike the resource, the data source has no prior state to preserve, so
	// missing fallbacks stay null rather than collapsing to an empty map.
	if !data.RouterSettingsFallbacks.IsNull() {
		t.Errorf("router_settings_fallbacks should be null, got %v", data.RouterSettingsFallbacks)
	}
	if !data.RouterSettingsContextWindowFallbacks.IsNull() {
		t.Errorf("router_settings_context_window_fallbacks should be null, got %v", data.RouterSettingsContextWindowFallbacks)
	}
}

func TestKeyDataSourceReadFallbacksNullWhenEmpty(t *testing.T) {
	t.Parallel()

	server := keyInfoServer(t, "sk-ds-empty-fallbacks", map[string]interface{}{
		"token": "sk-ds-empty-fallbacks",
		"router_settings": map[string]interface{}{
			"fallbacks":                []interface{}{},
			"context_window_fallbacks": []interface{}{},
		},
	})

	data := KeyDataSourceModel{Key: types.StringValue("sk-ds-empty-fallbacks")}

	if err := newKeyDataSource(server).readKeyDataSource(context.Background(), &data); err != nil {
		t.Fatalf("readKeyDataSource returned error: %v", err)
	}

	if !data.RouterSettingsFallbacks.IsNull() {
		t.Errorf("router_settings_fallbacks should be null for an empty list, got %v", data.RouterSettingsFallbacks)
	}
	if !data.RouterSettingsContextWindowFallbacks.IsNull() {
		t.Errorf("router_settings_context_window_fallbacks should be null for an empty list, got %v", data.RouterSettingsContextWindowFallbacks)
	}
}

func TestKeyDataSourceReadURLEncodesSpecialChars(t *testing.T) {
	t.Parallel()

	const keyWithSpecialChars = "sk-ds-key#with?special&chars"

	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"key":  keyWithSpecialChars,
			"info": map[string]interface{}{"token": keyWithSpecialChars},
		})
	}))
	defer server.Close()

	data := KeyDataSourceModel{Key: types.StringValue(keyWithSpecialChars)}

	if err := newKeyDataSource(server).readKeyDataSource(context.Background(), &data); err != nil {
		t.Fatalf("readKeyDataSource returned error: %v", err)
	}

	if gotKey != keyWithSpecialChars {
		t.Errorf("expected server to receive key %q, got %q", keyWithSpecialChars, gotKey)
	}
}
