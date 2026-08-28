package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func keySafeReadBody(t *testing.T, lookup string, blocked bool) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]interface{}{
		"key": lookup,
		"info": map[string]interface{}{
			"blocked":   blocked,
			"key_alias": "refreshed-alias",
			"models":    []interface{}{"model-new"},
			"metadata":  map[string]interface{}{"owner": "refreshed", "tags": []interface{}{"tag"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func keySafeReadModel(raw string) KeyResourceModel {
	data := configuredKeyNumericModel(raw)
	data.ID = types.StringValue(hashKeyForID(raw))
	data.KeyAlias = types.StringValue("prior-alias-secret")
	data.Models = accessGroupStringList("prior-model-secret")
	data.Blocked = types.BoolValue(false)
	data.KeyWOVersion = types.StringNull()
	data.MetadataJSON = types.StringNull()
	data.ConfigJSON = types.StringNull()
	data.PermissionsJSON = types.StringNull()
	return data
}

func TestKeySafeReadProjectionAndIdentityHelpers(t *testing.T) {
	raw := "sk-special +plus /slash &and=% percent#hash 雪"
	body := keySafeReadBody(t, raw, true)
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatal(err)
	}
	info := result["info"].(map[string]interface{})
	if err := validateExactKeyInfoIdentity(result, info, raw); err != nil {
		t.Fatal(err)
	}

	dataSource := KeyDataSourceModel{Key: types.StringValue(raw), KeyHash: types.StringNull()}
	complete, err := projectKeyDataSourceAPIObject(dataSource, result, raw, hashKeyForID(raw))
	if err != nil || complete.KeyAlias.ValueString() != "refreshed-alias" || !complete.Metadata.Equal(types.MapValueMust(types.StringType, map[string]attr.Value{"owner": types.StringValue("refreshed")})) {
		t.Fatalf("complete=%#v err=%v", complete, err)
	}
	blocked, err := projectKeyBlockAPIObject(result, raw)
	if err != nil || !blocked {
		t.Fatalf("blocked=%t err=%v", blocked, err)
	}

	for _, malformed := range []map[string]interface{}{
		{"key": raw},
		{"key": raw, "info": nil},
		{"key": raw, "info": []interface{}{}},
		{"key": "other-secret", "info": map[string]interface{}{"blocked": true}},
		{"key": raw, "info": map[string]interface{}{"blocked": "true"}},
	} {
		if _, err := projectKeyDataSourceAPIObject(dataSource, malformed, raw, hashKeyForID(raw)); err == nil {
			t.Fatalf("data source accepted malformed=%#v", malformed)
		}
		if _, err := projectKeyBlockAPIObject(malformed, raw); err == nil {
			t.Fatalf("key block accepted malformed=%#v", malformed)
		}
	}
}

func TestKeyHashCanonicalizationAndDataSourceMetadataSensitivity(t *testing.T) {
	raw := "sk-canonical"
	upper := "sha256:" + strings.ToUpper(strings.TrimPrefix(hashKeyForID(raw), "sha256:"))
	hash, err := keyHashFromID(upper)
	if err != nil || hash != strings.ToLower(strings.TrimPrefix(upper, "sha256:")) {
		t.Fatalf("hash=%q err=%v", hash, err)
	}
	lookup, managementID, err := keyDataSourceLookup(&KeyDataSourceModel{Key: types.StringNull(), KeyHash: types.StringValue(upper)})
	if err != nil || lookup != hash || managementID != "sha256:"+hash {
		t.Fatalf("lookup=%q management=%q err=%v", lookup, managementID, err)
	}
	var response datasource.SchemaResponse
	(&KeyDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &response)
	attribute, ok := response.Schema.Attributes["metadata"].(datasourceschema.MapAttribute)
	if !ok || !attribute.Sensitive {
		t.Fatalf("metadata schema=%#v", response.Schema.Attributes["metadata"])
	}
}

func TestKeyOrdinaryRefreshRetriesButConfirmationStaysSingleAttempt(t *testing.T) {
	raw := "sk-retry-key"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			writer.Header().Set("Retry-After", "0")
			http.Error(writer, `{"detail":"temporary-body-secret"}`, http.StatusServiceUnavailable)
			return
		}
		_, _ = writer.Write(keySafeReadBody(t, raw, true))
	}))
	defer server.Close()
	resource := &KeyResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}
	data := keySafeReadModel(raw)
	if err := resource.refreshKeyWithOwnership(context.Background(), &data, false, keySemanticReadOwnership{}); err != nil || calls.Load() != 2 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}

	calls.Store(0)
	prior := keySafeReadModel(raw)
	if err := resource.readKey(context.Background(), &prior); err == nil || calls.Load() != 1 {
		t.Fatalf("confirmation calls=%d err=%v", calls.Load(), err)
	}
}

func TestKeySafeReadIncomplete404RetriesThenSucceeds(t *testing.T) {
	raw := "sk-incomplete-404"
	var calls atomic.Int32
	client := testRetryClient(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			response := searchToolTestResponse(request, http.StatusNotFound, nil, nil)
			response.Body = failingReadCloser{err: io.ErrUnexpectedEOF}
			response.ContentLength = -1
			return response, nil
		}
		return searchToolTestResponse(request, http.StatusOK, keySafeReadBody(t, raw, true), nil), nil
	})
	resource := &KeyResource{client: client}
	data := keySafeReadModel(raw)
	if err := resource.refreshKeyWithOwnership(context.Background(), &data, false, keySemanticReadOwnership{}); err != nil || calls.Load() != 2 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func TestKeySafeReadMalformedScalarsAreAtomic(t *testing.T) {
	raw := "sk-malformed"
	for _, field := range []string{"blocked", "key_alias", "user_id"} {
		data := keySafeReadModel(raw)
		prior := data
		result := map[string]interface{}{"key": raw, "info": map[string]interface{}{field: 7}}
		info := result["info"].(map[string]interface{})
		if err := validateExactKeyInfoIdentity(result, info, raw); err != nil {
			t.Fatal(err)
		}
		if err := validateOrdinaryKeyInfoScalars(info); err == nil || !reflect.DeepEqual(data, prior) {
			t.Fatalf("field=%s changed=%t err=%v", field, !reflect.DeepEqual(data, prior), err)
		}
	}
}
