package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	jwtMappingID1 = "11111111-1111-4111-8111-111111111111"
	jwtMappingID2 = "22222222-2222-4222-8222-222222222222"
)

func jwtMappingJSON(id, claimValue string, description interface{}, active bool) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "jwt_claim_name": "sub", "jwt_claim_value": claimValue, "description": description,
		"is_active": active, "created_at": "2026-08-26T00:00:00Z", "updated_at": "2026-08-26T00:01:00.123456Z",
		"created_by": nil, "updated_by": "admin-user",
	}
}

func TestJWTKeyMappingSchemaParityAndSensitivity(t *testing.T) {
	r := NewJWTKeyMappingResource()
	resourceResp := &resource.SchemaResponse{}
	r.Schema(context.Background(), resource.SchemaRequest{}, resourceResp)
	if resourceResp.Diagnostics.HasError() {
		t.Fatal(resourceResp.Diagnostics.Errors())
	}
	want := []string{"id", "jwt_claim_name", "jwt_claim_value", "key_wo", "key_wo_version", "description", "is_active", "created_at", "updated_at", "created_by", "updated_by"}
	if len(resourceResp.Schema.Attributes) != len(want) {
		t.Fatalf("resource attributes=%d want=%d", len(resourceResp.Schema.Attributes), len(want))
	}
	for _, name := range want {
		if _, ok := resourceResp.Schema.Attributes[name]; !ok {
			t.Errorf("missing resource attribute %s", name)
		}
	}
	if a := resourceResp.Schema.Attributes["key_wo"]; !a.IsWriteOnly() || !a.IsSensitive() || !a.IsOptional() {
		t.Fatalf("key_wo schema=%#v", a)
	}
	if a := resourceResp.Schema.Attributes["jwt_claim_value"]; !a.IsSensitive() {
		t.Fatal("jwt_claim_value must be sensitive")
	}
	if a := resourceResp.Schema.Attributes["key_wo_version"]; a.IsSensitive() || a.IsWriteOnly() {
		t.Fatal("key_wo_version must be persisted and non-sensitive")
	}

	dsResp := &datasource.SchemaResponse{}
	NewJWTKeyMappingDataSource().Schema(context.Background(), datasource.SchemaRequest{}, dsResp)
	if len(dsResp.Schema.Attributes) != 9 {
		t.Fatalf("single data source attributes=%d", len(dsResp.Schema.Attributes))
	}
	listResp := &datasource.SchemaResponse{}
	NewJWTKeyMappingsListDataSource().Schema(context.Background(), datasource.SchemaRequest{}, listResp)
	mappings, ok := listResp.Schema.Attributes["mappings"]
	if !ok || !mappings.IsComputed() {
		t.Fatalf("list mappings schema=%#v", mappings)
	}
}

func TestJWTKeyMappingObjectStrictShapesAndTypedAbsence(t *testing.T) {
	valid, _ := json.Marshal(jwtMappingJSON(jwtMappingID1, "sensitive-value", nil, false))
	mapping, err := decodeJWTKeyMappingObject(valid)
	if err != nil {
		t.Fatal(err)
	}
	if mapping.Description != nil || mapping.CreatedBy != nil || mapping.IsActive {
		t.Fatalf("typed null/false lost: %#v", mapping)
	}
	for name, mutate := range map[string]func(map[string]interface{}){
		"alternate envelope": func(v map[string]interface{}) {
			for k := range v {
				delete(v, k)
			}
			v["mapping"] = jwtMappingJSON(jwtMappingID1, "x", nil, true)
		},
		"missing nullable":    func(v map[string]interface{}) { delete(v, "description") },
		"malformed boolean":   func(v map[string]interface{}) { v["is_active"] = "false" },
		"malformed timestamp": func(v map[string]interface{}) { v["updated_at"] = 17 },
		"extra field":         func(v map[string]interface{}) { v["token"] = "must-not-be-accepted" },
	} {
		t.Run(name, func(t *testing.T) {
			value := jwtMappingJSON(jwtMappingID1, "secret", nil, true)
			mutate(value)
			raw, _ := json.Marshal(value)
			if _, err := decodeJWTKeyMappingObject(raw); err == nil {
				t.Fatalf("shape accepted: %s", raw)
			}
		})
	}
}

func TestJWTKeyMappingInfoEscapesExactlyOnceAndRequiresIdentity(t *testing.T) {
	if got := jwtKeyMappingInfoEndpoint("id/with ?%&"); got != "/jwt/key/mapping/info?id=id%2Fwith+%3F%25%26" {
		t.Fatalf("endpoint=%q", got)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != jwtKeyMappingInfoPath || r.URL.Query().Get("id") != jwtMappingID1 {
			t.Fatalf("request=%s", r.URL.String())
		}
		_ = json.NewEncoder(w).Encode(jwtMappingJSON(jwtMappingID2, "secret", nil, true))
	}))
	defer server.Close()
	_, err := readJWTKeyMapping(context.Background(), &Client{APIBase: server.URL, APIKey: "key", HTTPClient: server.Client()}, jwtMappingID1)
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("err=%v", err)
	}
}

func TestJWTKeyMappingListCompletenessOrderingAndDuplicateGuard(t *testing.T) {
	var mu sync.Mutex
	pages := []int{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != jwtKeyMappingListPath || r.URL.Query().Get("size") != "100" {
			t.Fatalf("request=%s", r.URL.String())
		}
		page := r.URL.Query().Get("page")
		mu.Lock()
		if page == "1" {
			pages = append(pages, 1)
		} else {
			pages = append(pages, 2)
		}
		mu.Unlock()
		items := []interface{}{}
		if page == "1" {
			for i := 0; i < 100; i++ {
				id := fmt.Sprintf("%08d-0000-4000-8000-000000000000", i+10)
				items = append(items, jwtMappingJSON(id, fmt.Sprintf("secret-%d", i), nil, true))
			}
		} else {
			items = append(items, jwtMappingJSON(jwtMappingID1, "last-secret", "description", false))
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"mappings": items, "total_count": 101, "current_page": json.Number(page), "total_pages": 2})
	}))
	defer server.Close()
	mappings, err := listJWTKeyMappings(context.Background(), &Client{APIBase: server.URL, APIKey: "key", HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if len(mappings) != 101 || len(pages) != 4 {
		t.Fatalf("mappings=%d pages=%v", len(mappings), pages)
	}
	for i := 1; i < len(mappings); i++ {
		if mappings[i-1].ID > mappings[i].ID {
			t.Fatal("results not UUID sorted")
		}
	}

	duplicatePage := map[string]interface{}{"mappings": []interface{}{jwtMappingJSON(jwtMappingID1, "secret", nil, true)}, "total_count": 2, "current_page": 1, "total_pages": 2}
	raw, _ := json.Marshal(duplicatePage)
	if _, err := decodeJWTKeyMappingListPage(raw, 1); err == nil {
		t.Fatal("inconsistent list metadata accepted")
	}
}

func TestJWTKeyMappingListRejectsDuplicateIdentityAcrossPages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"mappings": []interface{}{jwtMappingJSON(jwtMappingID1, "secret", nil, true)}, "total_count": 101, "current_page": json.Number(page), "total_pages": 2})
	}))
	defer server.Close()
	_, err := listJWTKeyMappings(context.Background(), &Client{APIBase: server.URL, APIKey: "key", HTTPClient: server.Client()})
	if err == nil {
		t.Fatal("duplicate or incomplete identity listing accepted")
	}
}

func TestJWTKeyMappingDoubleScanRejectsEqualCountReplacementAndRowChange(t *testing.T) {
	for _, test := range []struct {
		name   string
		second map[string]interface{}
	}{
		{"equal-count insert-delete", jwtMappingJSON(jwtMappingID2, "secret", nil, true)},
		{"same UUID observable row change", jwtMappingJSON(jwtMappingID1, "changed", nil, true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				item := jwtMappingJSON(jwtMappingID1, "secret", nil, true)
				if calls.Add(1) == 2 {
					item = test.second
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"mappings": []interface{}{item}, "total_count": 1, "current_page": 1, "total_pages": 1})
			}))
			defer server.Close()
			_, err := listJWTKeyMappings(context.Background(), &Client{APIBase: server.URL, APIKey: "key", HTTPClient: server.Client()})
			if err == nil {
				t.Fatal("different bounded scans were accepted")
			}
		})
	}
}

func TestJWTKeyMappingDoubleScanAllowsWorkerOrderVariation(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := []interface{}{jwtMappingJSON(jwtMappingID1, "one", nil, true), jwtMappingJSON(jwtMappingID2, "two", nil, true)}
		if calls.Add(1) == 2 {
			items[0], items[1] = items[1], items[0]
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"mappings": items, "total_count": 2, "current_page": 1, "total_pages": 1})
	}))
	defer server.Close()
	mappings, err := listJWTKeyMappings(context.Background(), &Client{APIBase: server.URL, APIKey: "key", HTTPClient: server.Client()})
	if err != nil || len(mappings) != 2 || mappings[0].ID != jwtMappingID1 || mappings[1].ID != jwtMappingID2 {
		t.Fatalf("mappings=%#v err=%v", mappings, err)
	}
}

func TestJWTKeyMappingEmptyClaimPairIsSourceValid(t *testing.T) {
	value := jwtMappingJSON(jwtMappingID1, "", nil, true)
	value["jwt_claim_name"] = ""
	raw, _ := json.Marshal(value)
	mapping, err := decodeJWTKeyMappingObject(raw)
	if err != nil || mapping.ClaimName != "" || mapping.ClaimValue != "" {
		t.Fatalf("mapping=%#v err=%v", mapping, err)
	}
}

func TestJWTKeyMappingExact404OnlyAbsenceContract(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		absent bool
	}{
		{"exact 404", 404, `{"detail":"anything"}`, true},
		{"misleading 400", 400, `{"detail":"Mapping not found 404"}`, false},
		{"misleading 500", 500, `{"detail":"does not exist"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, tc.body, tc.status) }))
			defer server.Close()
			_, err := readJWTKeyMapping(context.Background(), &Client{APIBase: server.URL, APIKey: "key", HTTPClient: server.Client()}, jwtMappingID1)
			if IsAPIErrorStatus(err, http.StatusNotFound) != tc.absent {
				t.Fatalf("err=%v absent=%v", err, IsAPIErrorStatus(err, 404))
			}
		})
	}
}

func TestJWTKeyMappingMutationDiagnosticsRedactClaimsKeysAndBodies(t *testing.T) {
	secrets := []string{"claim-secret", "sk-key-secret", "server-body-secret"}
	err := &APIError{StatusCode: 409, Detail: strings.Join(secrets, " "), Body: strings.Join(secrets, " ")}
	got := mappingMutationDiagnostic("create", err)
	for _, secret := range secrets {
		if strings.Contains(got, secret) {
			t.Fatalf("diagnostic leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "HTTP 409") {
		t.Fatalf("diagnostic lost safe status: %s", got)
	}
}

func TestJWTKeyMappingStaleUpdateResponseDoesNotConverge(t *testing.T) {
	description := "old"
	mapping := jwtKeyMappingObject{ID: jwtMappingID1, ClaimName: "sub", ClaimValue: "secret", Description: &description, IsActive: true}
	plan := JWTKeyMappingResourceModel{Description: types.StringValue("new"), IsActive: types.BoolValue(false)}
	body := map[string]interface{}{"description": "new", "is_active": false}
	if jwtKeyMappingUpdateMatchesPlan(mapping, plan, body) {
		t.Fatal("stale update response was accepted")
	}
}

func TestJWTKeyMappingCommittedResponseLossIsSingleAttemptAndRetainsNoInventedIdentity(t *testing.T) {
	var calls atomic.Int64
	client := &Client{APIBase: "https://mapping.invalid", APIKey: "admin-secret", HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: failingReadCloser{err: errors.New("response lost")}, Request: request}, nil
	})}}
	var raw json.RawMessage
	accepted, err := client.doRequestWithResponse(context.Background(), http.MethodPost, jwtKeyMappingCreatePath, map[string]interface{}{"jwt_claim_value": "claim-secret", "key": "sk-key-secret"}, &raw)
	if !accepted || err == nil || calls.Load() != 1 {
		t.Fatalf("accepted=%v err=%v calls=%d", accepted, err, calls.Load())
	}
	if id := confirmedJWTKeyMappingID(raw); id != "" {
		t.Fatalf("invented identity %q", id)
	}
	diagnostic := mappingMutationDiagnostic("create", err)
	if containsAny(diagnostic, "claim-secret", "sk-key-secret", "admin-secret", "mapping.invalid", "response lost") {
		t.Fatalf("unsafe diagnostic: %s", diagnostic)
	}
}

func TestJWTKeyMappingDeleteResponseStrict(t *testing.T) {
	for raw, valid := range map[string]bool{`{"status":"success"}`: true, `{"status":"ok"}`: false, `{"status":"success","id":"x"}`: false, `[{"status":"success"}]`: false} {
		if (validateJWTKeyMappingDeleteResponse(json.RawMessage(raw)) == nil) != valid {
			t.Errorf("raw=%s", raw)
		}
	}
}
