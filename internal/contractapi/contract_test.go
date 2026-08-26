package contractapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestRepositoryContract(t *testing.T) {
	if err := Verify(repositoryRoot(t)); err != nil {
		t.Fatal(err)
	}
}

func TestExtractionGolden(t *testing.T) {
	root := repositoryRoot(t)
	extracted, err := ExtractProvider(filepath.Join(root, "internal", "provider"))
	if err != nil {
		t.Fatal(err)
	}
	contracts, _, _, err := LoadContracts(filepath.Join(root, "openapi.json"), filepath.Join(root, "internal", "contract", "supplemental-routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	operations, err := ResolveOperations(extracted, contracts)
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.MarshalIndent(operations, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	want, err := os.ReadFile(filepath.Join("testdata", "provider-operations.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("provider operation extraction differs from golden; run contract review before updating it")
	}
}

func TestExtractionCoversReviewedSpecialRoutes(t *testing.T) {
	root := repositoryRoot(t)
	extracted, err := ExtractProvider(filepath.Join(root, "internal", "provider"))
	if err != nil {
		t.Fatal(err)
	}
	rawKeys := map[string]bool{}
	for _, operation := range extracted {
		rawKeys[operation.Method+" "+operation.Path] = true
	}
	for _, key := range []string{"GET /fallback/%2E", "GET /fallback/%2E%2E", "DELETE /fallback/%2E", "DELETE /fallback/%2E%2E"} {
		if !rawKeys[key] {
			t.Errorf("fallback special-segment extraction omitted %s", key)
		}
	}
	contracts, _, _, err := LoadContracts(filepath.Join(root, "openapi.json"), filepath.Join(root, "internal", "contract", "supplemental-routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	operations, err := ResolveOperations(extracted, contracts)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]Operation{}
	for _, operation := range operations {
		byKey[operation.Method+" "+operation.Path] = operation
	}
	checks := map[string][]string{
		"GET /key/info":                              {"key"},
		"GET /key/list":                              {"access_group_id", "page", "return_full_object", "size", "sort_by", "sort_order", "team_id", "user_id"},
		"GET /fallback/{model}":                      {"fallback_type"},
		"DELETE /fallback/{model}":                   {"fallback_type"},
		"GET /prompts/{prompt_id}":                   {"environment"},
		"PATCH /prompts/{prompt_id}":                 {"environment"},
		"GET /prompts/{prompt_id}/versions":          {"environment"},
		"GET /v1/mcp/server/{server_id}":             {},
		"DELETE /v1/mcp/server/{server_id}":          {},
		"PATCH /v2/organization/{organization_id}":   {},
		"GET /credentials/by_name/{credential_name}": {},
		"PATCH /credentials/{credential_name}":       {},
		"GET /v1/access_group/{access_group_id}":     {},
		"GET /v1/agents/{agent_id}":                  {},
		"POST /guardrails":                           {},
		"GET /guardrails/{guardrail_id}/info":        {},
		"POST /search_tools":                         {},
		"GET /search_tools/{search_tool_id}":         {},
		"PATCH /model/{model_id}/update":             {},
		"PATCH /organization/member_update":          {},
		"POST /vector_store/info":                    {},
	}
	for key, queries := range checks {
		operation, ok := byKey[key]
		if !ok {
			t.Errorf("missing extracted operation %s", key)
			continue
		}
		if strings.Join(operation.QueryParameters, ",") != strings.Join(queries, ",") {
			t.Errorf("%s query names = %v, want %v", key, operation.QueryParameters, queries)
		}
		if len(operation.Evidence) == 0 {
			t.Errorf("%s has no code evidence", key)
		}
	}
}

func TestExtractorRejectsUnresolvedAndRawHTTP(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "bad.go", `package provider
import ("context"; "net/http")
func bad(ctx context.Context, c *Client) {
 endpoint := externalBuilder()
 c.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, nil)
 http.NewRequest(http.MethodGet, endpoint, nil)
 http.Get(endpoint)
}
`)
	_, err := ExtractProvider(dir)
	if err == nil || !strings.Contains(err.Error(), "unresolved HTTP") || !strings.Contains(err.Error(), "raw HTTP request construction") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolveRejectsWrongMethodAndQueryParameter(t *testing.T) {
	contracts := map[string][]contractOperation{
		"GET": {{method: "GET", path: "/things/{thing_id}", pathParams: []string{"thing_id"}, queryParams: map[string]bool{"page": true}}},
	}
	_, err := ResolveOperations([]Operation{{Method: "POST", Path: "/things/{}"}}, contracts)
	if err == nil || !strings.Contains(err.Error(), "found 0") {
		t.Fatalf("wrong method was accepted: %v", err)
	}
	_, err = ResolveOperations([]Operation{{Method: "GET", Path: "/things/{}", QueryParameters: []string{"secret"}}}, contracts)
	if err == nil || !strings.Contains(err.Error(), `query parameter "secret"`) {
		t.Fatalf("wrong query was accepted: %v", err)
	}
}

func TestLoadContractsRejectsConflictingDuplicate(t *testing.T) {
	dir := t.TempDir()
	openapi := filepath.Join(dir, "openapi.json")
	supplemental := filepath.Join(dir, "supplemental.json")
	writeFixture(t, dir, "openapi.json", `{"paths":{"/things/{thing_id}":{"get":{"parameters":[{"name":"thing_id","in":"path"}]}}}}`)
	writeFixture(t, dir, "supplemental.json", `{"schema_version":1,"routes":[{"method":"GET","path":"/things/{thing_id}","path_parameters":["other_id"],"query_parameters":[]}]}`)
	_, _, _, err := LoadContracts(openapi, supplemental)
	if err == nil || !strings.Contains(err.Error(), "conflicting duplicate") {
		t.Fatalf("conflicting duplicate was accepted: %v", err)
	}
}

func TestManifestPinnedMetadataCountsAndReviewInventory(t *testing.T) {
	root := repositoryRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "internal", "contract", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Upstream.Tag != "v1.98.0" || manifest.Upstream.Commit != "d8f71d7bdbd7c9873d98293f83d64c6db72847e6" || manifest.Upstream.Python != "3.12.14" {
		t.Fatalf("upstream provenance is not pinned: %+v", manifest.Upstream)
	}
	if manifest.OpenAPI.PathCount != 561 || manifest.OpenAPI.OperationCount != 772 || manifest.Supplemental.RouteCount != 1 {
		t.Fatalf("artifact review counts changed: %+v %+v", manifest.OpenAPI, manifest.Supplemental)
	}
	if err := validateReview(manifest); err != nil {
		t.Fatal(err)
	}
}

func TestStaleManifestEntryFails(t *testing.T) {
	actual := []Operation{{Method: "GET", Path: "/things", QueryParameters: []string{"page"}}}
	want := append([]Operation(nil), actual...)
	want = append(want, Operation{Method: "DELETE", Path: "/stale"})
	if diff := compareInventory(actual, want); !strings.Contains(diff, "stale manifest") {
		t.Fatalf("stale entry was not detected: %q", diff)
	}
	want = []Operation{{Method: "GET", Path: "/things", QueryParameters: []string{"wrong"}}}
	if diff := compareInventory(actual, want); !strings.Contains(diff, "parameters stale") {
		t.Fatalf("stale query parameters were not detected: %q", diff)
	}
	actual = []Operation{{Method: "GET", Path: "/things/{thing_id}", PathParameters: []string{"thing_id"}}}
	want = []Operation{{Method: "GET", Path: "/things/{thing_id}", PathParameters: []string{"other_id"}}}
	if diff := compareInventory(actual, want); !strings.Contains(diff, "parameters stale") {
		t.Fatalf("stale path parameters were not detected: %q", diff)
	}
}

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
