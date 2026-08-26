package contractapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
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

func TestResolvedOperationTableHasReviewedPathModes(t *testing.T) {
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
	if len(operations) != 108 {
		t.Fatalf("operation count = %d, want 108", len(operations))
	}
	captures := map[string]bool{
		"GET /credentials/by_name/{credential_name}": true,
		"DELETE /credentials/{credential_name}":      true,
		"PATCH /credentials/{credential_name}":       true,
	}
	for _, operation := range operations {
		wantMode := ""
		if len(operation.PathParameters) != 0 {
			wantMode = "ordinary"
			if captures[operation.Method+" "+operation.Path] {
				wantMode = "capture"
			}
		}
		if operation.pathMode != wantMode {
			t.Errorf("%s %s mode = %q, want %q (query keys %v)", operation.Method, operation.Path, operation.pathMode, wantMode, operation.QueryParameters)
		}
	}
}

func TestExtractorRejectsUnresolvedAndRawHTTP(t *testing.T) {
	dir := t.TempDir()
	writeHTTPFixtureSupport(t, dir)
	writeFixture(t, dir, "bad.go", `package provider
import ("context"; "net/http")
func externalBuilder() string { return "" }
func bad(ctx context.Context, c *Client) {
 endpoint := externalBuilder()
 c.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, nil)
 _, _ = http.NewRequest(http.MethodGet, endpoint, nil)
 _, _ = http.Get(endpoint)
}
`)
	_, err := ExtractProvider(dir)
	if err == nil || !strings.Contains(err.Error(), "raw net/http transport reference") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExtractorRejectsUnknownClientHTTPAbstraction(t *testing.T) {
	t.Run("direct", func(t *testing.T) {
		dir := t.TempDir()
		writeHTTPFixtureSupport(t, dir)
		writeFixture(t, dir, "client.go", `package provider
import "context"
func (c *Client) Sneaky(ctx context.Context, path string) error {
 return c.DoRequestWithResponse(ctx, "GET", path, nil, nil)
}
`)
		_, err := ExtractProvider(dir)
		if err == nil || !strings.Contains(err.Error(), "unknown Client HTTP abstraction Sneaky") {
			t.Fatalf("unknown Client transport wrapper was accepted: %v", err)
		}
	})
	t.Run("through-free-wrapper", func(t *testing.T) {
		dir := t.TempDir()
		writeHTTPFixtureSupport(t, dir)
		writeFixture(t, dir, "client.go", `package provider
import "context"
func transport(ctx context.Context, client *Client, path string) error {
 return client.DoRequestWithResponse(ctx, "GET", path, nil, nil)
}
func (c *Client) Sneaky(ctx context.Context, path string) error {
 invoke := transport
 return invoke(ctx, c, path)
}
`)
		_, err := ExtractProvider(dir)
		if err == nil || !strings.Contains(err.Error(), "unknown Client HTTP abstraction Sneaky") {
			t.Fatalf("aliased call-graph Client wrapper was accepted: %v", err)
		}
	})
}

func TestExtractorRejectsUnknownWrapperAndDynamicQuery(t *testing.T) {
	t.Run("wrapper", func(t *testing.T) {
		dir := t.TempDir()
		writeHTTPFixtureSupport(t, dir)
		writeFixture(t, dir, "wrapper.go", `package provider
import "context"
func wrapper(ctx context.Context, client *Client, requestPath string) error {
 return client.DoRequestWithResponse(ctx, "GET", requestPath, nil, nil)
}
`)
		_, err := ExtractProvider(dir)
		if err == nil || !strings.Contains(err.Error(), "unresolved dynamic HTTP path") {
			t.Fatalf("dynamic wrapper was accepted: %v", err)
		}
	})
	t.Run("query-name", func(t *testing.T) {
		dir := t.TempDir()
		writeHTTPFixtureSupport(t, dir)
		writeFixture(t, dir, "query.go", `package provider
import ("context"; "net/url")
func request(ctx context.Context, client *Client, queryName string) error {
 query := url.Values{}
 query.Set(queryName, "value")
 return client.DoRequestWithResponse(ctx, "GET", endpointWithQuery("/things", query), nil, nil)
}
`)
		_, err := ExtractProvider(dir)
		if err == nil || !strings.Contains(err.Error(), "dynamic url.Values") {
			t.Fatalf("dynamic query name was accepted: %v", err)
		}
	})
}

func TestExtractorNormalizesExactEndpointBuilders(t *testing.T) {
	dir := t.TempDir()
	writeHTTPFixtureSupport(t, dir)
	writeFixture(t, dir, "routes.go", `package provider
import ("context"; "net/url")
func safe(ctx context.Context, client *Client, ordinary, captured, queryValue string) {
 client.DoRequestWithResponse(ctx, "GET", endpointWithPathSegment("/things/", ordinary, "/info"), nil, nil)
 client.DoRequestWithResponse(ctx, "DELETE", endpointWithPathCapture("/captures/", captured, ""), nil, nil)
 query := url.Values{"scope": []string{queryValue}}
 client.DoRequestWithResponse(ctx, "GET", endpointWithQuery("/things", query), nil, nil)
}
`)
	operations, err := ExtractProvider(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, operation := range operations {
		got[operation.Method+" "+operation.Path] = operation.QueryParameters
	}
	for key, query := range map[string][]string{
		"GET /things/{}/info": {},
		"DELETE /captures/{}": {},
		"GET /things":         {"scope"},
	} {
		if !reflect.DeepEqual(got[key], query) {
			t.Errorf("%s query = %v, want %v", key, got[key], query)
		}
	}
}

func TestResolverRejectsWrongReviewedPathMode(t *testing.T) {
	root := repositoryRoot(t)
	contracts, _, _, err := LoadContracts(filepath.Join(root, "openapi.json"), filepath.Join(root, "internal", "contract", "supplemental-routes.json"))
	if err != nil {
		t.Fatal(err)
	}
	fixtures := map[string]string{
		"capture-route-with-ordinary-builder": `package provider
import "context"
func bad(ctx context.Context, client *Client, value string) {
 client.DoRequestWithResponse(ctx, "GET", endpointWithPathSegment("/credentials/by_name/", value, ""), nil, nil)
}
`,
		"ordinary-route-with-capture-builder": `package provider
import "context"
func bad(ctx context.Context, client *Client, value string) {
 client.DoRequestWithResponse(ctx, "GET", endpointWithPathCapture("/v1/agents/", value, ""), nil, nil)
}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			extracted, err := ExtractProvider(dir)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ResolveOperations(extracted, contracts); err == nil || !strings.Contains(err.Error(), "path builder mode") {
				t.Fatalf("wrong reviewed path mode was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsEndpointBuilderBypasses(t *testing.T) {
	fixtures := map[string]string{
		"alias": `package provider
func bad(value string) { build := endpointWithPathSegment; _ = build("/things/", value, "") }
`,
		"higher-order": `package provider
func take(func(string, string, string) string) {}
func bad() { take(endpointWithPathCapture) }
`,
		"interface": `package provider
type bad interface { endpointWithPathSegment(string, string, string) string }
`,
		"generic": `package provider
func bad[T ~string](value T) string { return endpointWithPathSegment("/things/", string(value), "") }
`,
		"reflection": `package provider
import "reflect"
func bad() { _ = reflect.ValueOf(endpointWithPathSegment) }
`,
		"container": `package provider
var bad = []func(string, string, string) string{endpointWithPathCapture}
`,
		"dynamic-prefix": `package provider
func bad(prefix, value string) string { return endpointWithPathSegment(prefix, value, "") }
`,
		"dynamic-suffix": `package provider
func bad(value, suffix string) string { return endpointWithPathCapture("/things/", value, suffix) }
`,
		"fallback-exception-wrong-route": `package provider
func bad(value string) string { return endpointWithFallbackPathSegment("/things/", value, "") }
`,
		"existing-query": `package provider
import "net/url"
func bad(value string) string { return endpointWithQuery("/things?fixed=true", url.Values{"scope": []string{value}}) }
`,
		"fragment": `package provider
import "net/url"
func bad(value string) string { return endpointWithQuery("/things#fragment", url.Values{"scope": []string{value}}) }
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			if _, err := ExtractProvider(dir); err == nil || (!strings.Contains(err.Error(), "endpoint builder") && !strings.Contains(err.Error(), "query builder") && !strings.Contains(err.Error(), "path builder") && !strings.Contains(err.Error(), "fallback slash exception")) {
				t.Fatalf("endpoint-builder bypass was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsPreescapedEndpointInputsAndEscapeAliases(t *testing.T) {
	fixtures := map[string]string{
		"path-preescaped": `package provider
import "net/url"
func bad(value string) string { return endpointWithPathSegment("/things/", url.PathEscape(value), "") }
`,
		"query-leaf-preescaped": `package provider
import "net/url"
func bad(value string) string {
 query := url.Values{"scope": []string{url.QueryEscape(value)}}
 return endpointWithQuery("/things", query)
}
`,
		"path-alias": `package provider
import "net/url"
func bad(value string) string {
 escape := url.PathEscape
 return endpointWithPathSegment("/things/", escape(value), "")
}
`,
		"query-alias": `package provider
import "net/url"
func bad(value string) string {
 escape := url.QueryEscape
 query := url.Values{"scope": []string{escape(value)}}
 return endpointWithQuery("/things", query)
}
`,
		"path-higher-order": `package provider
import "net/url"
func take(func(string) string) {}
func bad() { take(url.PathEscape) }
`,
		"query-higher-order": `package provider
import "net/url"
func take(func(string) string) {}
func bad() { take(url.QueryEscape) }
`,
		"encode-alias": `package provider
import "net/url"
func bad(values url.Values) { encode := values.Encode; _ = encode() }
`,
		"encode-higher-order": `package provider
import "net/url"
func take(func() string) {}
func bad(values url.Values) { take(values.Encode) }
`,
		"package-alias": `package provider
import "net/url"
var bad = url.PathEscape
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			if _, err := ExtractProvider(dir); err == nil || !strings.Contains(err.Error(), "URL escape") {
				t.Fatalf("preescaped endpoint input or escape alias was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsTransformedRawInputProvenance(t *testing.T) {
	fixtures := map[string]string{
		"path-replace": `package provider
import "strings"
func bad(value string) string { return endpointWithPathSegment("/things/", strings.ReplaceAll(value, "/", "%2F"), "") }
`,
		"path-concatenation": `package provider
func bad(value string) string { return endpointWithPathSegment("/things/", value + "-suffix", "") }
`,
		"path-manual-percent-escape": `package provider
func bad(value string) string { return endpointWithPathSegment("/things/", value + "%2Fchild", "") }
`,
		"path-custom-wrapper": `package provider
import "strings"
func rewrite(value string) string { return strings.ReplaceAll(value, "/", "%2F") }
func bad(value string) string { return endpointWithPathSegment("/things/", rewrite(value), "") }
`,
		"path-builder-wrapper-call": `package provider
import "strings"
func thingPath(value string) string { return endpointWithPathSegment("/things/", value, "") }
func bad(value string) string { return thingPath(strings.ReplaceAll(value, "/", "%2F")) }
`,
		"path-local-alias": `package provider
import "strings"
func bad(value string) string {
 rewritten := strings.ReplaceAll(value, "/", "%2F")
 alias := rewritten
 return endpointWithPathSegment("/things/", alias, "")
}
`,
		"path-indirect-assignment": `package provider
import "strings"
func bad(value string) string {
 raw := value
 var alias string
 alias = raw
 alias = strings.ReplaceAll(alias, "/", "%2F")
 return endpointWithPathSegment("/things/", alias, "")
}
`,
		"path-parameter-reassignment": `package provider
import "strings"
func bad(value string) string {
 value = strings.ReplaceAll(value, "/", "%2F")
 return endpointWithPathSegment("/things/", value, "")
}
`,
		"path-higher-order": `package provider
func apply(transform func(string) string, value string) string { return transform(value) }
func identity(value string) string { return value }
func bad(value string) string { return endpointWithPathSegment("/things/", apply(identity, value), "") }
`,
		"path-interface": `package provider
type transformer interface { Transform(string) string }
func bad(transformer transformer, value string) string { return endpointWithPathSegment("/things/", transformer.Transform(value), "") }
`,
		"path-interface-recovery": `package provider
import "strings"
func bad(value string) string {
 var hidden any = strings.ReplaceAll(value, "/", "%2F")
 return endpointWithPathSegment("/things/", hidden.(string), "")
}
`,
		"path-local-map-wrapper": `package provider
import "strings"
func bad(value string) string {
 hidden := map[string]any{}
 hidden["id"] = strings.ReplaceAll(value, "/", "%2F")
 return endpointWithPathSegment("/things/", hidden["id"].(string), "")
}
`,
		"path-string-map-wrapper": `package provider
import "strings"
func bad(value string) string {
 hidden := map[string]string{"id": strings.ReplaceAll(value, "/", "%2F")}
 return endpointWithPathSegment("/things/", hidden["id"], "")
}
`,
		"path-generic": `package provider
func transform[T ~string](value T) string { return string(value) }
func bad(value string) string { return endpointWithPathSegment("/things/", transform(value), "") }
`,
		"query-composite-replace": `package provider
import (
 "net/url"
 "strings"
)
func bad(value string) string { return endpointWithQuery("/things", url.Values{"scope": []string{strings.ReplaceAll(value, "/", "%2F")}}) }
`,
		"query-set-concatenation": `package provider
import "net/url"
func bad(value string) string {
 query := url.Values{}
 query.Set("scope", value + "%2F")
 return endpointWithQuery("/things", query)
}
`,
		"query-add-wrapper": `package provider
import "net/url"
func rewrite(value string) string { return value + "-changed" }
func bad(value string) string {
 query := url.Values{}
 query.Add("scope", rewrite(value))
 return endpointWithQuery("/things", query)
}
`,
		"query-index-assignment": `package provider
import (
 "net/url"
 "strings"
)
func bad(value string) string {
 query := url.Values{}
 query["scope"] = []string{strings.ReplaceAll(value, "/", "%2F")}
 return endpointWithQuery("/things", query)
}
`,
		"query-builder-wrapper-call": `package provider
import (
 "net/url"
 "strings"
)
func requestPath(values url.Values) string { return endpointWithQuery("/things", values) }
func bad(value string) string {
 return requestPath(url.Values{"scope": []string{strings.ReplaceAll(value, "/", "%2F")}})
}
`,
		"query-clone-flow": `package provider
import (
 "net/url"
 "strings"
)
func cloneURLValues(values url.Values) url.Values { return values }
func bad(values url.Values, value string) string {
 query := cloneURLValues(values)
 query.Set("scope", strings.ReplaceAll(value, "/", "%2F"))
 return endpointWithQuery("/things", query)
}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			if _, err := ExtractProvider(dir); err == nil || !strings.Contains(err.Error(), "raw-input provenance") {
				t.Fatalf("transformed builder input was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsKilledEndpointProvenance(t *testing.T) {
	fixtures := map[string]string{
		"sentinel-trim-prefix": `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint)
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"short-declaration-tuple": `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 endpoint, changed := strings.TrimPrefix(endpoint, invalidReviewedEndpoint), true
 _ = changed
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"multi-return": `package provider
import "context"
func reload(string) (string, error) { return "/things/reloaded", nil }
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 endpoint, _ = reload(endpoint)
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"compound-assignment": `package provider
import "context"
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 endpoint += "/reloaded"
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"conditional-reassignment": `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string, rewrite bool) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 if rewrite { endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint) }
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"switch-reassignment": `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string, rewrite int) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 switch rewrite { case 1: endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint); case 2: endpoint = "/things/static" }
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"type-switch-reassignment": `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string, rewrite any) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 switch rewrite.(type) { case string: endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint) }
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"select-reassignment": `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string, rewrite <-chan bool) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 select { case <-rewrite: endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint); default: }
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"loop-carried-reassignment": `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string, count int) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 for i := 0; i < count; i++ { endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint) }
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"struct-reload": `package provider
import "context"
type endpointHolder struct { endpoint string }
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 holder := endpointHolder{endpoint: endpoint}
 endpoint = holder.endpoint
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"array-reload": `package provider
import "context"
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 holder := [1]string{endpoint}
 endpoint = holder[0]
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"map-reload": `package provider
import "context"
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 holder := map[string]string{"endpoint": endpoint}
 endpoint = holder["endpoint"]
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"closure-result": `package provider
import "context"
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 reload := func() string { return endpoint }
 endpoint = reload()
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"closure-capture-write": `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 reload := func() { endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint) }
 reload()
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"method-result": `package provider
import "context"
type endpointReloader struct{}
func (endpointReloader) reload(value string) string { return value }
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 endpoint = (endpointReloader{}).reload(endpoint)
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"pointer-alias-write": `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 alias := &endpoint
 *alias = strings.TrimPrefix(*alias, invalidReviewedEndpoint)
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"unknown-pointer-write": `package provider
import "context"
func reload(value *string) { *value = "/things/reloaded" }
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 reload(&endpoint)
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"pointer-field-write": `package provider
import ("context"; "strings")
type pointerHolder struct { endpoint *string }
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 holder := pointerHolder{endpoint: &endpoint}
 *holder.endpoint = strings.TrimPrefix(*holder.endpoint, invalidReviewedEndpoint)
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"pointer-array-write": `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 holder := [1]*string{&endpoint}
 *holder[0] = strings.TrimPrefix(*holder[0], invalidReviewedEndpoint)
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			if _, err := ExtractProvider(dir); err == nil || (!strings.Contains(err.Error(), "unresolved") && !strings.Contains(err.Error(), "statically unqueried")) {
				t.Fatalf("killed endpoint provenance was accepted: %v", err)
			}
		})
	}
}

func TestExtractorAllowsCanonicalBranchAlternatives(t *testing.T) {
	dir := t.TempDir()
	writeHTTPFixtureSupport(t, dir)
	writeFixture(t, dir, "safe.go", `package provider
import "context"
func safe(ctx context.Context, client *Client, value string, alternate bool) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 if alternate { endpoint = endpointWithPathSegment("/other/", value, "") }
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`)
	operations, err := ExtractProvider(dir)
	if err != nil || len(operations) != 6 {
		t.Fatalf("canonical branch alternatives rejected: operations=%+v error=%v", operations, err)
	}
}

func TestExtractorRejectsLexicalEndpointShadowLeaks(t *testing.T) {
	fixtures := map[string]string{
		"nested-block": `
 {
  endpoint := endpointWithPathSegment("/inner/", value, "")
  _ = endpoint
 }
`,
		"if-init": `
 if endpoint := endpointWithPathSegment("/inner/", value, ""); enabled {
  _ = endpoint
 }
`,
		"switch-init": `
 switch endpoint := endpointWithPathSegment("/inner/", value, ""); enabled {
 case true:
  _ = endpoint
 }
`,
		"type-switch-init": `
 switch endpoint := endpointWithPathSegment("/inner/", value, ""); any(value).(type) {
 case string:
  _ = endpoint
 }
`,
		"type-switch-case": `
 switch any(value).(type) {
 case string:
  endpoint := endpointWithPathSegment("/inner/", value, "")
  _ = endpoint
 }
`,
		"for-init": `
 for endpoint := endpointWithPathSegment("/inner/", value, ""); enabled; {
  _ = endpoint
  break
 }
`,
		"range": `
 for _, endpoint := range []string{endpointWithPathSegment("/inner/", value, "")} {
  _ = endpoint
 }
`,
		"closure": `
 func() {
  endpoint := endpointWithPathSegment("/inner/", value, "")
  _ = endpoint
 }()
`,
		"case-clause": `
 switch enabled {
 case true:
  endpoint := endpointWithPathSegment("/inner/", value, "")
  _ = endpoint
 }
`,
		"comm-clause": `
 select {
 case <-ready:
  endpoint := endpointWithPathSegment("/inner/", value, "")
  _ = endpoint
 default:
 }
`,
	}
	for name, body := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			fixture := `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string, enabled bool, ready <-chan struct{}) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint)
` + body + `
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`
			writeFixture(t, dir, "bad.go", fixture)
			if _, err := ExtractProvider(dir); err == nil || (!strings.Contains(err.Error(), "unresolved") && !strings.Contains(err.Error(), "statically unqueried")) {
				t.Fatalf("outer endpoint provenance was restored by an inner shadow: %v", err)
			}
		})
	}
}

func TestExtractorKeepsLexicalEndpointShadowsIsolated(t *testing.T) {
	dir := t.TempDir()
	writeHTTPFixtureSupport(t, dir)
	writeFixture(t, dir, "safe.go", `package provider
import "context"
func safe(ctx context.Context, client *Client, value string, enabled bool, ready <-chan struct{}) {
 endpoint := endpointWithPathSegment("/outer/", value, "")
 {
  endpoint := "/not-reviewed"
  _ = endpoint
 }
 if endpoint := "/not-reviewed"; enabled { _ = endpoint }
 switch endpoint := "/not-reviewed"; enabled { case true: _ = endpoint }
 switch endpoint := "/not-reviewed"; any(value).(type) { case string: _ = endpoint }
 for endpoint := "/not-reviewed"; enabled; { _ = endpoint; break }
 for _, endpoint := range []string{"/not-reviewed"} { _ = endpoint }
 func() { endpoint := "/not-reviewed"; _ = endpoint }()
 switch enabled { case true: endpoint := "/not-reviewed"; _ = endpoint }
 select { case <-ready: endpoint := "/not-reviewed"; _ = endpoint; default: }
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
func inner(ctx context.Context, client *Client, value string, enabled bool, ready <-chan struct{}) {
 {
  endpoint := endpointWithPathSegment("/inner/", value, "")
  client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
 }
 if endpoint := endpointWithPathSegment("/inner/", value, ""); enabled {
  client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
 }
 switch endpoint := endpointWithPathSegment("/inner/", value, ""); enabled {
 case true:
  client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
 }
 switch endpoint := endpointWithPathSegment("/inner/", value, ""); any(value).(type) {
 case string:
  client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
 }
 for endpoint := endpointWithPathSegment("/inner/", value, ""); enabled; {
  client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
  break
 }
 for _, endpoint := range []string{endpointWithPathSegment("/inner/", value, "")} {
  client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
 }
 func() {
  endpoint := endpointWithPathSegment("/inner/", value, "")
  client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
 }()
 switch enabled {
 case true:
  endpoint := endpointWithPathSegment("/inner/", value, "")
  client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
 }
 select {
 case <-ready:
  endpoint := endpointWithPathSegment("/inner/", value, "")
  client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
 default:
 }
}
`)
	if operations, err := ExtractProvider(dir); err != nil || len(operations) == 0 {
		t.Fatalf("lexically isolated endpoint shadows were rejected: operations=%+v error=%v", operations, err)
	}
}

func TestExtractorRejectsSameEndpointObjectOverwriteAfterInnerShadow(t *testing.T) {
	dir := t.TempDir()
	writeHTTPFixtureSupport(t, dir)
	writeFixture(t, dir, "bad.go", `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 { endpoint := endpointWithPathSegment("/inner/", value, ""); _ = endpoint }
 endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint)
 client.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`)
	if _, err := ExtractProvider(dir); err == nil || (!strings.Contains(err.Error(), "unresolved") && !strings.Contains(err.Error(), "statically unqueried")) {
		t.Fatalf("same-object overwrite after an inner shadow was accepted: %v", err)
	}
}

func TestCopiedAgentEndpointShadowDoesNotRestoreKilledProvenance(t *testing.T) {
	root := repositoryRoot(t)
	source := filepath.Join(root, "internal", "provider")
	dir := t.TempDir()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	injected := false
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		contents, readErr := os.ReadFile(filepath.Join(source, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if entry.Name() == "resource_agent.go" {
			needle := `endpoint := endpointWithPathSegment("/v1/agents/", planned.ID.ValueString(), "")`
			replacement := needle + `
	endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint)
	{
		endpoint := endpointWithPathSegment("/v1/agents/", planned.ID.ValueString(), "")
		_ = endpoint
	}`
			updated := strings.Replace(string(contents), needle, replacement, 1)
			injected = updated != string(contents)
			contents = []byte(updated)
		}
		if writeErr := os.WriteFile(filepath.Join(dir, entry.Name()), contents, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if !injected {
		t.Fatal("resource_agent.go endpoint fixture was not transformed")
	}
	if _, err := ExtractProvider(dir); err == nil || !strings.Contains(err.Error(), "unresolved HTTP method or path") {
		t.Fatalf("copied agent endpoint shadow restored killed provenance: %v", err)
	}
}

func TestCopiedSearchToolEndpointOverwriteFailsContractCheck(t *testing.T) {
	root := repositoryRoot(t)
	source := filepath.Join(root, "internal", "provider")
	dir := t.TempDir()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		contents, readErr := os.ReadFile(filepath.Join(source, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if entry.Name() == "datasource_search_tool.go" {
			needle := `endpoint := endpointWithPathSegment("/search_tools/", searchToolID, "")`
			replacement := needle + `
	endpoint = strings.TrimPrefix(endpoint, invalidReviewedEndpoint)`
			contents = []byte(strings.Replace(string(contents), needle, replacement, 1))
			contents = []byte(strings.Replace(string(contents), `"fmt"`, `"fmt"
	"strings"`, 1))
		}
		if writeErr := os.WriteFile(filepath.Join(dir, entry.Name()), contents, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	if _, err := ExtractProvider(dir); err == nil || !strings.Contains(err.Error(), "unresolved HTTP method or path") {
		t.Fatalf("copied search-tool overwrite passed contract check: %v", err)
	}
}

func TestExtractorRejectsBuilderResultPostTransforms(t *testing.T) {
	fixtures := map[string]string{
		"replace-direct": `package provider
import ("context"; "strings")
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 client.DoRequestWithResponse(ctx, "GET", strings.ReplaceAll(endpoint, "things", "other"), nil, nil)
}
`,
		"concatenate-alias": `package provider
import "context"
func bad(ctx context.Context, client *Client, value string) {
 endpoint := endpointWithPathSegment("/things/", value, "")
 alias := endpoint
 client.DoRequestWithResponse(ctx, "GET", alias + "/extra", nil, nil)
}
`,
		"sprintf-wrapper": `package provider
import ("context"; "fmt")
func thingPath(value string) string { return endpointWithPathSegment("/things/", value, "") }
func bad(ctx context.Context, client *Client, value string) {
 client.DoRequestWithResponse(ctx, "GET", fmt.Sprintf("%s/extra", thingPath(value)), nil, nil)
}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			if _, err := ExtractProvider(dir); err == nil || (!strings.Contains(err.Error(), "unresolved") && !strings.Contains(err.Error(), "statically unqueried")) {
				t.Fatalf("post-transformed builder result was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsRangedTransformedProvenance(t *testing.T) {
	fixtures := map[string]string{
		"slice-literal": `package provider
import "strings"
func bad(value string) string {
 for _, ranged := range []string{strings.ReplaceAll(value, "/", "%2F")} {
  return endpointWithPathSegment("/things/", ranged, "")
 }
 return endpointWithPathSegment("/things/", "safe", "")
}
`,
		"slice-parameter-call": `package provider
import "strings"
func firstPath(values []string) string {
 for _, ranged := range values {
  return endpointWithPathSegment("/things/", ranged, "")
 }
 return endpointWithPathSegment("/things/", "safe", "")
}
func bad(value string) string {
 return firstPath([]string{strings.ReplaceAll(value, "/", "%2F")})
}
`,
		"slice-alias-mutation": `package provider
import "strings"
func bad(value string) string {
 values := []string{value}
 alias := values
 alias[0] = strings.ReplaceAll(value, "/", "%2F")
 for _, ranged := range values {
  return endpointWithPathSegment("/things/", ranged, "")
 }
 return endpointWithPathSegment("/things/", "safe", "")
}
`,
		"array-alias": `package provider
import "strings"
func bad(value string) string {
 values := [1]string{strings.ReplaceAll(value, "/", "%2F")}
 alias := values
 for _, ranged := range alias {
  return endpointWithPathSegment("/things/", ranged, "")
 }
 return endpointWithPathSegment("/things/", "safe", "")
}
`,
		"map-key": `package provider
import "strings"
func bad(value string) string {
 values := map[string]struct{}{strings.ReplaceAll(value, "/", "%2F"): {}}
 for ranged := range values {
  return endpointWithPathSegment("/things/", ranged, "")
 }
 return endpointWithPathSegment("/things/", "safe", "")
}
`,
		"map-value-alias": `package provider
import "strings"
func bad(value string) string {
 values := map[int]string{1: value}
 alias := values
 alias[1] = strings.ReplaceAll(value, "/", "%2F")
 for _, ranged := range values {
  return endpointWithPathSegment("/things/", ranged, "")
 }
 return endpointWithPathSegment("/things/", "safe", "")
}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			if _, err := ExtractProvider(dir); err == nil || !strings.Contains(err.Error(), "raw-input provenance") {
				t.Fatalf("ranged transformed identity was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsIndirectRangedContainerMutation(t *testing.T) {
	fixtures := map[string]string{
		"slice-helper": `package provider
import "strings"
func populate(values *[]string, value string) { *values = append(*values, strings.ReplaceAll(value, "/", "%2F")) }
func bad(value string) string {
 values := make([]string, 0, 1)
 populate(&values, value)
 for _, ranged := range values { return endpointWithPathSegment("/things/", ranged, "") }
 return endpointWithPathSegment("/things/", "safe", "")
}
`,
		"slice-view-helper": `package provider
import "strings"
func populate(values []string, value string) { values[0] = strings.ReplaceAll(value, "/", "%2F") }
func bad(value string) string {
 values := []string{value}
 populate(values[:], value)
 for _, ranged := range values { return endpointWithPathSegment("/things/", ranged, "") }
 return endpointWithPathSegment("/things/", "safe", "")
}
`,
		"map-key-helper": `package provider
import "strings"
func populate(values map[string]string, value string) { values[strings.ReplaceAll(value, "/", "%2F")] = "safe" }
func bad(value string) string {
 values := make(map[string]string)
 populate(values, value)
 for ranged := range values { return endpointWithPathSegment("/things/", ranged, "") }
 return endpointWithPathSegment("/things/", "safe", "")
}
`,
		"map-value-method": `package provider
import "strings"
type identities map[int]string
func (values identities) populate(value string) { values[1] = strings.ReplaceAll(value, "/", "%2F") }
func bad(value string) string {
 values := identities{}
 mutate := values.populate
 mutate(value)
 for _, ranged := range values { return endpointWithPathSegment("/things/", ranged, "") }
 return endpointWithPathSegment("/things/", "safe", "")
}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			if _, err := ExtractProvider(dir); err == nil || !strings.Contains(err.Error(), "raw-input provenance") {
				t.Fatalf("indirect ranged container mutation was accepted: %v", err)
			}
		})
	}
}

func TestExtractorAllowsRangedRawProvenance(t *testing.T) {
	dir := t.TempDir()
	writeHTTPFixtureSupport(t, dir)
	writeFixture(t, dir, "safe.go", `package provider
func safe(value string) string {
 values := []string{value, "literal"}
 alias := values
 for _, ranged := range alias {
  return endpointWithPathSegment("/things/", ranged, "")
 }
 return endpointWithPathSegment("/things/", "safe", "")
}
`)
	if _, err := ExtractProvider(dir); err != nil {
		t.Fatalf("safe ranged raw provenance was rejected: %v", err)
	}
}

func TestExtractorRejectsAliasedEndpointDispatchWrappers(t *testing.T) {
	fixtures := map[string]string{
		"function-alias": `package provider
import "strings"
func thingPath(value string) string { return endpointWithPathSegment("/things/", value, "") }
func bad(value string) string {
 alias := thingPath
 return alias(strings.ReplaceAll(value, "/", "%2F"))
}
`,
		"alias-chain": `package provider
import "strings"
func thingPath(value string) string { return endpointWithPathSegment("/things/", value, "") }
func bad(value string) string {
 first := thingPath
 second := first
 return second(strings.ReplaceAll(value, "/", "%2F"))
}
`,
		"recursive-wrapper-alias": `package provider
import "strings"
func thingPath(value string) string { return endpointWithPathSegment("/things/", value, "") }
func outer(value string) string { return thingPath(value) }
func bad(value string) string {
 alias := outer
 return alias(strings.ReplaceAll(value, "/", "%2F"))
}
`,
		"closure-alias": `package provider
import "strings"
func bad(value string) string {
 alias := func(identity string) string { return endpointWithPathSegment("/things/", identity, "") }
 return alias(strings.ReplaceAll(value, "/", "%2F"))
}
`,
		"slice-storage": `package provider
func thingPath(value string) string { return endpointWithPathSegment("/things/", value, "") }
var bad = []func(string) string{thingPath}
`,
		"map-storage": `package provider
func thingPath(value string) string { return endpointWithPathSegment("/things/", value, "") }
var bad = map[string]func(string) string{"path": thingPath}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			if _, err := ExtractProvider(dir); err == nil || (!strings.Contains(err.Error(), "raw-input provenance") && !strings.Contains(err.Error(), "endpoint-dispatch wrappers")) {
				t.Fatalf("aliased endpoint wrapper bypass was accepted: %v", err)
			}
		})
	}
}

func TestExtractorAllowsRawEndpointDispatchAlias(t *testing.T) {
	dir := t.TempDir()
	writeHTTPFixtureSupport(t, dir)
	writeFixture(t, dir, "safe.go", `package provider
func thingPath(value string) string { return endpointWithPathSegment("/things/", value, "") }
func safe(value string) string {
 alias := thingPath
 return alias(value)
}
`)
	if _, err := ExtractProvider(dir); err != nil {
		t.Fatalf("safe endpoint wrapper alias was rejected: %v", err)
	}
}

func TestExtractorRejectsSpoofedRawHelpersAndEndpointMethodIndirection(t *testing.T) {
	fixtures := map[string]string{
		"spoofed-raw-helper": `package provider
import "strings"
type KeyResourceModel struct{ ID string }
func keyLookupIdentifier(data *KeyResourceModel) (string, error) {
 return strings.ReplaceAll(data.ID, "/", "%2F"), nil
}
func bad(value string) string {
 identity, _ := keyLookupIdentifier(&KeyResourceModel{ID: value})
 return endpointWithPathSegment("/things/", identity, "")
}
`,
		"receiver-method-value": `package provider
import "strings"
type routes struct{}
func (routes) path(value string) string { return endpointWithPathSegment("/things/", value, "") }
func bad(value string) string {
 build := (routes{}).path
 alias := build
 return alias(strings.ReplaceAll(value, "/", "%2F"))
}
`,
		"receiver-method-expression": `package provider
import "strings"
type routes struct{}
func (routes) path(value string) string { return endpointWithPathSegment("/things/", value, "") }
func bad(value string) string {
 build := routes.path
 return build(routes{}, strings.ReplaceAll(value, "/", "%2F"))
}
`,
		"nested-closure-struct": `package provider
import "strings"
type holder struct{ build func(string) string }
func bad(value string) string {
 outer := func() holder {
  inner := func(identity string) string { return endpointWithPathSegment("/things/", identity, "") }
  return holder{build: inner}
 }
 stored := outer()
 return stored.build(strings.ReplaceAll(value, "/", "%2F"))
}
`,
		"spoofed-higher-order-consumer": `package provider
import "context"
type page struct{}
type storedFetch struct { fetch func(context.Context, int) (page, error) }
func collectNumberedPages(ctx context.Context, endpoint string, fetch func(context.Context, int) (page, error)) {
 stored := storedFetch{fetch: fetch}
 _, _ = stored.fetch(ctx, 1)
}
func bad(ctx context.Context, client *Client, value string) {
 collectNumberedPages(ctx, "/things", func(ctx context.Context, pageNumber int) (page, error) {
  client.DoRequestWithResponse(ctx, "GET", endpointWithPathSegment("/things/", value, ""), nil, nil)
  return page{}, nil
 })
}
`,
		"nested-higher-order-consumer": `package provider
import "context"
type page struct{}
func collectNumberedPages(ctx context.Context, endpoint string, fetch func(context.Context, int) (page, error)) {
 invoke := func() { _, _ = fetch(ctx, 1) }
 invoke()
}
func bad(ctx context.Context, client *Client, value string) {
 collectNumberedPages(ctx, "/things", func(ctx context.Context, pageNumber int) (page, error) {
  client.DoRequestWithResponse(ctx, "GET", endpointWithPathSegment("/things/", value, ""), nil, nil)
  return page{}, nil
 })
}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			if _, err := ExtractProvider(dir); err == nil || (!strings.Contains(err.Error(), "raw-input provenance") && !strings.Contains(err.Error(), "endpoint-dispatch") && !strings.Contains(err.Error(), "composites")) {
				t.Fatalf("spoofed or indirectly stored helper was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsEveryRawIdentityProofMutation(t *testing.T) {
	bodies := map[string]string{
		"one-token":         `return data.Key.ValueString()[:], nil`,
		"rune-construction": `return data.Key.ValueString() + string(rune(37)) + "2Fchild", nil`,
		"byte-construction": `return data.Key.ValueString() + string([]byte{37, 50, 70}) + "child", nil`,
		"suffix":            `return data.Key.ValueString() + "-child", nil`,
		"prefix":            `return "child-" + data.Key.ValueString(), nil`,
		"alternate-hash":    `sum := sha256.Sum256([]byte(data.Key.ValueString())); return fmt.Sprintf("%x", sum), nil`,
		"alternate-format":  `return fmt.Sprintf("%s", data.Key.ValueString()), nil`,
		"extra-branch":      `if data.Key.ValueString() == "special" { return "special", nil }; return data.Key.ValueString(), nil`,
		"wrapper-call":      `return rawIdentityWrapper(data.Key.ValueString()), nil`,
		"same-name-spoof":   `return strings.ReplaceAll(data.Key.ValueString(), "/", "%2F"), nil`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", `package provider
import (
 "crypto/sha256"
 "fmt"
 "strings"
)
var _ = sha256.Size
var _ = fmt.Sprintf
var _ = strings.ReplaceAll
type rawString struct { value string }
func (v rawString) IsNull() bool { return false }
func (v rawString) IsUnknown() bool { return false }
func (v rawString) ValueString() string { return v.value }
type KeyResourceModel struct { ID, Key, KeyWOVersion rawString }
func rawIdentityWrapper(value string) string { return value }
func keyLookupIdentifier(data *KeyResourceModel) (string, error) { `+body+` }
func bad(value string) string {
 identity, _ := keyLookupIdentifier(&KeyResourceModel{Key: rawString{value: value}})
 return endpointWithPathSegment("/things/", identity, "")
}
`)
			if _, err := ExtractProvider(dir); err == nil || !strings.Contains(err.Error(), "raw-input provenance") {
				t.Fatalf("raw identity implementation mutation was accepted: %v", err)
			}
		})
	}
}

func fixtureRawDependencyProof(t *testing.T, source string) (string, bool) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
		Instances:  map[*ast.Ident]types.Instance{},
	}
	configuration := &types.Config{Importer: importer.Default()}
	if _, err := configuration.Check(providerPackagePath, fset, []*ast.File{file}, info); err != nil {
		t.Fatal(err)
	}
	x := &extractor{
		fset:      fset,
		files:     map[string]*ast.File{"fixture.go": file},
		funcDecls: map[*types.Func]*ast.FuncDecl{},
		typesInfo: info,
	}
	var reviewed *types.Func
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		object, _ := info.Defs[function.Name].(*types.Func)
		x.funcDecls[object] = function
		if function.Name.Name == "proof" {
			reviewed = object
		}
	}
	if reviewed == nil {
		t.Fatal("fixture omitted proof function")
	}
	return x.canonicalRawIdentityProof(reviewed, map[*types.Func]bool{})
}

func TestRawHelperProofClosesEverySemanticDependencyClass(t *testing.T) {
	base, valid := fixtureRawDependencyProof(t, `package provider
const semanticValue = "safe"
func proof(value string) string { return value + semanticValue }
`)
	changed, changedValid := fixtureRawDependencyProof(t, `package provider
const semanticValue = "changed"
func proof(value string) string { return value + semanticValue }
`)
	if !valid || !changedValid || base == changed {
		t.Fatalf("constant semantic value was absent from proof: base=(%t,%q) changed=(%t,%q)", valid, base, changedValid, changed)
	}

	fixtures := map[string]string{
		"package-global-initializer": `package provider
var semanticValue = "safe"
func proof(value string) string { return value + semanticValue }
`,
		"init-mutated-global": `package provider
var semanticValue = "safe"
func init() { semanticValue = "changed" }
func proof(value string) string { return value + semanticValue }
`,
		"function-variable-closure": `package provider
func proof(value string) string {
 transform := func(input string) string { return input + "changed" }
 return transform(value)
}
`,
		"interface-concrete-body": `package provider
type normalizer interface { normalize(string) string }
type concreteNormalizer struct{}
func (concreteNormalizer) normalize(value string) string { return value + "changed" }
func proof(value string, transform normalizer) string { return transform.normalize(value) }
`,
		"generic-callee": `package provider
func transform[T ~string](value T) string { return string(value) }
func proof(value string) string { return transform(value) }
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			if proof, closed := fixtureRawDependencyProof(t, fixture); closed {
				t.Fatalf("unclosed semantic dependency was proven: %q", proof)
			}
		})
	}
}

func TestRawHelperProofAllowsClosedLocalFreeFunction(t *testing.T) {
	source := `package provider
import "strings"
func normalize(value string) string { return strings.ToLower(value) }
func proof(value string) string { return normalize(value) }
`
	first, valid := fixtureRawDependencyProof(t, source)
	second, repeatedValid := fixtureRawDependencyProof(t, source)
	if !valid || !repeatedValid || first == "" || first != second {
		t.Fatalf("unchanged closed helper proof is not stable: first=(%t,%q) second=(%t,%q)", valid, first, repeatedValid, second)
	}
}

func TestRawHelperProofBindsFieldOwnersEmbeddingAliasesPointersAndCycles(t *testing.T) {
	proof := func(source string) string {
		t.Helper()
		digest, valid := fixtureRawDependencyProof(t, source)
		if !valid || digest == "" {
			t.Fatalf("fixture did not produce a closed proof: %q", digest)
		}
		return digest
	}

	embeddedA := proof(`package provider
type A struct { Value string }
type Wrapper struct { A }
func proof(value Wrapper) string { return value.Value }
`)
	embeddedB := proof(`package provider
type B struct { Value string }
type Wrapper struct { B }
func proof(value Wrapper) string { return value.Value }
`)
	if embeddedA == embeddedB {
		t.Fatal("embedded declaring owner and selection traversal were absent from proof")
	}

	alias := proof(`package provider
type Base struct { Value string }
type Alias = Base
func proof(value Alias) string { return value.Value }
`)
	named := proof(`package provider
type Base struct { Value string }
type Alias Base
func proof(value Alias) string { return value.Value }
`)
	pointer := proof(`package provider
type Base struct { Value string }
type Alias = Base
func proof(value *Alias) string { return value.Value }
`)
	if alias == named || alias == pointer || named == pointer {
		t.Fatalf("alias, named, and pointer field identities collided: %q %q %q", alias, named, pointer)
	}

	cycleSource := `package provider
type Node struct { Value string; Next *Node }
func proof(value *Node) string { return value.Next.Value }
`
	cycleFirst := proof(cycleSource)
	cycleSecond := proof(cycleSource)
	cycleChanged := proof(`package provider
type OtherNode struct { Value string; Next *OtherNode }
func proof(value *OtherNode) string { return value.Next.Value }
`)
	if cycleFirst != cycleSecond || cycleFirst == cycleChanged {
		t.Fatalf("recursive proof identity was unstable or unqualified: %q %q %q", cycleFirst, cycleSecond, cycleChanged)
	}
}

func TestExtractorDoesNotGrantSemanticsToSpoofedCallNames(t *testing.T) {
	fixtures := map[string]string{
		"sprintf-free": `func Sprintf(string, ...any) string { return "/spoof" }
func bad(ctx context.Context, client *Client, value string) { client.DoRequestWithResponse(ctx, "GET", Sprintf("/things/%s", value), nil, nil) }`,
		"sprintf-method": `type formatter struct{}
func (formatter) Sprintf(string, ...any) string { return "/spoof" }
func bad(ctx context.Context, client *Client, value string) { client.DoRequestWithResponse(ctx, "GET", (formatter{}).Sprintf("/things/%s", value), nil, nil) }`,
		"replace-all": `func ReplaceAll(string, string, string) string { return "/spoof" }
func bad(ctx context.Context, client *Client, value string) { client.DoRequestWithResponse(ctx, "GET", ReplaceAll("/things/"+value, "/", "x"), nil, nil) }`,
		"path-escape": `func PathEscape(string) string { return "/spoof" }
func bad(ctx context.Context, client *Client, value string) { client.DoRequestWithResponse(ctx, "GET", PathEscape(value), nil, nil) }`,
		"query-escape": `func QueryEscape(string) string { return "/spoof" }
func bad(ctx context.Context, client *Client, value string) { client.DoRequestWithResponse(ctx, "GET", QueryEscape(value), nil, nil) }`,
		"encode-method": `type values struct{}
func (values) Encode() string { return "/spoof" }
func bad(ctx context.Context, client *Client) { client.DoRequestWithResponse(ctx, "GET", (values{}).Encode(), nil, nil) }`,
		"itoa": `func Itoa(int) string { return "/spoof" }
func bad(ctx context.Context, client *Client) { client.DoRequestWithResponse(ctx, "GET", Itoa(1), nil, nil) }`,
		"format-int": `func FormatInt(int64, int) string { return "/spoof" }
func bad(ctx context.Context, client *Client) { client.DoRequestWithResponse(ctx, "GET", FormatInt(1, 10), nil, nil) }`,
		"append": `func append(string, ...string) string { return "/spoof" }
func bad(ctx context.Context, client *Client, value string) { client.DoRequestWithResponse(ctx, "GET", append("/things/", value), nil, nil) }`,
		"make": `func make(string, string) string { return "/spoof" }
func bad(ctx context.Context, client *Client, value string) { client.DoRequestWithResponse(ctx, "GET", make("/things/", value), nil, nil) }`,
		"endpoint-builder-method": `type routes struct{}
func (routes) endpointWithPathSegment(string, string, string) string { return "/spoof" }
func bad(ctx context.Context, client *Client, value string) { client.DoRequestWithResponse(ctx, "GET", (routes{}).endpointWithPathSegment("/things/", value, ""), nil, nil) }`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", "package provider\nimport \"context\"\n"+fixture+"\n")
			if _, err := ExtractProvider(dir); err == nil {
				t.Fatal("spoofed call name received reviewed semantics")
			}
		})
	}
}

func TestExtractorAllowsExactlyQualifiedStandardCalls(t *testing.T) {
	dir := t.TempDir()
	writeHTTPFixtureSupport(t, dir)
	writeFixture(t, dir, "safe.go", `package provider
import (
 "context"
 "fmt"
 "strconv"
 "strings"
)
func safe(ctx context.Context, client *Client, value string) {
 client.DoRequestWithResponse(ctx, "GET", fmt.Sprintf("/format/%s", value), nil, nil)
 client.DoRequestWithResponse(ctx, "GET", strings.ReplaceAll("/replace/static", "old", "new"), nil, nil)
 client.DoRequestWithResponse(ctx, "GET", "/number/"+strconv.Itoa(1), nil, nil)
 client.DoRequestWithResponse(ctx, "GET", endpointWithPathSegment("/exact/", value, ""), nil, nil)
}
`)
	operations, err := ExtractProvider(dir)
	if err != nil {
		t.Fatalf("exact standard-library calls were rejected: %v", err)
	}
	if len(operations) != 6 {
		t.Fatalf("exact standard-library extraction returned %d operations, want 6", len(operations))
	}
}

func TestExtractorPinsPromptEnvironmentFreeHelper(t *testing.T) {
	fixtures := map[string]string{
		"transformed-free-function": `package provider
import (
 "context"
 "net/url"
 "strings"
)
const defaultPromptEnvironment = "development"
func promptEnvironment(value string) string {
 if value == "" { return defaultPromptEnvironment }
 return strings.ReplaceAll(value, "/", "%2F")
}
func bad(ctx context.Context, client *Client, value string) {
 query := url.Values{}
 query.Set("environment", promptEnvironment(value))
 client.DoRequestWithResponse(ctx, "GET", endpointWithQuery("/prompts/list", query), nil, nil)
}
`,
		"same-name-receiver-method": `package provider
import (
 "context"
 "net/url"
)
type promptSpoof struct{}
func (promptSpoof) promptEnvironment(value string) string { return value }
func bad(ctx context.Context, client *Client, value string) {
 query := url.Values{}
 query.Set("environment", (promptSpoof{}).promptEnvironment(value))
 client.DoRequestWithResponse(ctx, "GET", endpointWithQuery("/prompts/list", query), nil, nil)
}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			if _, err := ExtractProvider(dir); err == nil || !strings.Contains(err.Error(), "raw-input provenance") {
				t.Fatalf("unreviewed prompt environment helper was accepted: %v", err)
			}
		})
	}
}

func TestExtractorAllowsExactPromptEnvironmentFreeHelper(t *testing.T) {
	dir := t.TempDir()
	writeHTTPFixtureSupport(t, dir)
	writeFixture(t, dir, "safe.go", `package provider
import (
 "context"
 "net/url"
)
const defaultPromptEnvironment = "development"
func promptEnvironment(value string) string {
 if value == "" { return defaultPromptEnvironment }
 return value
}
func safe(ctx context.Context, client *Client, value string) {
 query := url.Values{}
 query.Set("environment", promptEnvironment(value))
 client.DoRequestWithResponse(ctx, "GET", endpointWithQuery("/prompts/list", query), nil, nil)
}
`)
	if _, err := ExtractProvider(dir); err != nil {
		t.Fatalf("exact unchanged prompt environment helper was rejected: %v", err)
	}
}

func TestExtractorRejectsCallableIdentityCollisions(t *testing.T) {
	fixtures := map[string]string{
		"interface-first-value-receiver": `
type routeDispatcher interface { route(string) string }
type endpointRoute struct{}
func (endpointRoute) route(value string) string { return endpointWithPathSegment("/things/", value, "") }
type benignRoute struct{}
func (benignRoute) route(value string) string { return value }
func bad(dispatch routeDispatcher, value string) string { return dispatch.route(strings.ReplaceAll(value, "/", "%2F")) }
`,
		"interface-last-pointer-receiver": `
type endpointRoute struct{}
func (*endpointRoute) route(value string) string { return endpointWithPathSegment("/things/", value, "") }
type benignRoute struct{}
func (*benignRoute) route(value string) string { return value }
type routeDispatcher interface { route(string) string }
func bad(dispatch routeDispatcher, value string) string { return dispatch.route(strings.ReplaceAll(value, "/", "%2F")) }
`,
		"embedded-interface": `
type routeDispatcher interface { route(string) string }
type embeddedDispatcher interface { routeDispatcher }
type endpointRoute struct{}
func (endpointRoute) route(value string) string { return endpointWithPathSegment("/things/", value, "") }
type benignRoute struct{}
func (benignRoute) route(value string) string { return value }
func bad(dispatch embeddedDispatcher, value string) string { return dispatch.route(strings.ReplaceAll(value, "/", "%2F")) }
`,
		"method-value": `
type routeDispatcher interface { route(string) string }
type endpointRoute struct{}
func (endpointRoute) route(value string) string { return endpointWithPathSegment("/things/", value, "") }
type benignRoute struct{}
func (benignRoute) route(value string) string { return value }
func bad(dispatch routeDispatcher, value string) string { build := dispatch.route; return build(strings.ReplaceAll(value, "/", "%2F")) }
`,
		"method-expression": `
type routeDispatcher interface { route(string) string }
type endpointRoute struct{}
func (endpointRoute) route(value string) string { return endpointWithPathSegment("/things/", value, "") }
type benignRoute struct{}
func (benignRoute) route(value string) string { return value }
func bad(dispatch routeDispatcher, value string) string { return routeDispatcher.route(dispatch, strings.ReplaceAll(value, "/", "%2F")) }
`,
		"free-function-same-name": `
type routeDispatcher interface { route(string) string }
type endpointRoute struct{}
func (*endpointRoute) route(value string) string { return endpointWithPathSegment("/things/", value, "") }
func route(value string) string { return value }
type benignRoute struct{}
func (benignRoute) route(value string) string { return route(value) }
func bad(dispatch routeDispatcher, value string) string { return dispatch.route(strings.ReplaceAll(value, "/", "%2F")) }
`,
		"transitive-wrapper-value-receiver-interface-first": `
type routeDispatcher interface { route(string) string }
func endpointRouteWrapper(value string) string { return endpointWithPathSegment("/things/", value, "") }
type endpointRoute struct{}
func (endpointRoute) route(value string) string { return endpointRouteWrapper(value) }
type benignRoute struct{}
func (benignRoute) route(value string) string { return value }
func bad(dispatch routeDispatcher, value string) string { return dispatch.route(strings.ReplaceAll(value, "/", "%2F")) }
`,
		"transitive-wrapper-pointer-receiver-interface-last": `
type endpointRoute struct{}
func (*endpointRoute) inner(value string) string { return endpointWithPathSegment("/things/", value, "") }
func (route *endpointRoute) route(value string) string { return route.inner(value) }
type benignRoute struct{}
func (*benignRoute) route(value string) string { return value }
type routeDispatcher interface { route(string) string }
func bad(dispatch routeDispatcher, value string) string { return dispatch.route(strings.ReplaceAll(value, "/", "%2F")) }
`,
		"unknown-dynamic-target": `
type routeDispatcher interface { route(string) string }
type endpointRoute struct { build func(string) string }
func (route endpointRoute) route(value string) string { return route.build(value) }
type benignRoute struct{}
func (benignRoute) route(value string) string { return value }
func bad(dispatch routeDispatcher, value string) string { return dispatch.route(strings.ReplaceAll(value, "/", "%2F")) }
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", "package provider\nimport \"strings\"\n"+fixture)
			if _, err := ExtractProvider(dir); err == nil || (!strings.Contains(err.Error(), "interface dynamic string dispatch") && !strings.Contains(err.Error(), "raw-input provenance")) {
				t.Fatalf("callable identity collision was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsGenericAndInterfaceMethodValueEndpointDispatch(t *testing.T) {
	fixtures := map[string]string{
		"inferred-generic-higher-order": `
func route(value string) string { return endpointWithPathSegment("/things/", value, "") }
func invoke[T any](call func(T) string, value T) string { return call(value) }
func bad(value string) string { return invoke(route, strings.ReplaceAll(value, "/", "%2F")) }
`,
		"explicit-generic-higher-order": `
func route(value string) string { return endpointWithPathSegment("/things/", value, "") }
func invoke[T any](call func(T) string, value T) string { return call(value) }
func bad(value string) string { return invoke[string](route, strings.ReplaceAll(value, "/", "%2F")) }
`,
		"type-parameter-callable": `
func route(value string) string { return endpointWithPathSegment("/things/", value, "") }
type routeFunction interface { ~func(string) string }
func invoke[F routeFunction](call F, value string) string { return call(value) }
func bad(value string) string { return invoke(route, strings.ReplaceAll(value, "/", "%2F")) }
`,
		"interface-method-value": `
type routeDispatcher interface { route(string) string }
type endpointRoute struct{}
func (endpointRoute) route(value string) string { return endpointWithPathSegment("/things/", value, "") }
func invoke(call func(string) string, value string) string { return call(value) }
func bad(dispatch routeDispatcher, value string) string { return invoke(dispatch.route, strings.ReplaceAll(value, "/", "%2F")) }
`,
		"generic-dynamic-method-set": `
type routeDispatcher interface { route(string) string }
type endpointRoute struct{}
func (endpointRoute) route(value string) string { return endpointWithPathSegment("/things/", value, "") }
func invoke[T routeDispatcher](dispatch T, value string) string { return dispatch.route(value) }
func bad(value string) string { return invoke(endpointRoute{}, strings.ReplaceAll(value, "/", "%2F")) }
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", "package provider\nimport \"strings\"\n"+fixture)
			if _, err := ExtractProvider(dir); err == nil || (!strings.Contains(err.Error(), "endpoint-dispatch") && !strings.Contains(err.Error(), "interface dynamic string dispatch") && !strings.Contains(err.Error(), "raw-input provenance")) {
				t.Fatalf("generic or dynamic method endpoint dispatch was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsSpoofedReviewedPathHelpers(t *testing.T) {
	fixtures := map[string]struct {
		contains string
		harden   string
	}{
		"dot-guard-always-false": {
			contains: `func containsDotPathComponent(value string) bool { return false }`,
			harden:   exactFixtureHardenDotSegment,
		},
		"dot-guard-skips-parent": {
			contains: `func containsDotPathComponent(value string) bool {
 for _, component := range strings.Split(value, "/") {
  if component == "." { return true }
 }
 return false
}`,
			harden: exactFixtureHardenDotSegment,
		},
		"hardener-post-transform": {
			contains: exactFixtureContainsDotPathComponent,
			harden: `func hardenDotSegment(value string) string {
 return strings.ReplaceAll(value, ".", "%2E")
}`,
		},
		"hardener-wrong-default": {
			contains: exactFixtureContainsDotPathComponent,
			harden: `func hardenDotSegment(value string) string {
 switch value {
 case ".": return "%2E"
 case "..": return "%2E%2E"
 default: return value + "changed"
 }
}`,
		},
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeReviewedBuilderFixture(t, dir, fixture.contains, fixture.harden, "", "")
			if _, err := ExtractProvider(dir); err == nil || (!strings.Contains(err.Error(), "dot-component guard") && !strings.Contains(err.Error(), "dot-segment hardening")) {
				t.Fatalf("spoofed reviewed path helper was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsAdditionalBuilderSentinels(t *testing.T) {
	fixtures := map[string]struct{ before, after string }{
		"before-reviewed-guard": {before: `if value == "blocked" { return invalidReviewedEndpoint }`},
		"after-escape":          {after: `if len(value) > 100 { return invalidReviewedEndpoint }`},
	}
	for name, extra := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeReviewedBuilderFixture(t, dir, exactFixtureContainsDotPathComponent, exactFixtureHardenDotSegment, extra.before, extra.after)
			if _, err := ExtractProvider(dir); err == nil || !strings.Contains(err.Error(), "escape result") {
				t.Fatalf("additional identity rejection sentinel was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsMaliciousEndpointBuilderBodies(t *testing.T) {
	fixtures := map[string]string{
		"discard": `func endpointWithPathCapture(prefix, value, suffix string) string {
 _ = url.PathEscape(value)
 return prefix + value + suffix
}`,
		"reassignment": `func endpointWithPathCapture(prefix, value, suffix string) string {
 escaped := url.PathEscape(value)
 escaped = value
 return prefix + escaped + suffix
}`,
		"post-transform": `func endpointWithPathCapture(prefix, value, suffix string) string {
 escaped := url.PathEscape(value)
 return prefix + strings.ReplaceAll(escaped, "A", "B") + suffix
}`,
		"second-escape": `func endpointWithPathCapture(prefix, value, suffix string) string {
 escaped := url.PathEscape(value)
 return prefix + url.PathEscape(escaped) + suffix
}`,
		"raw-return": `func endpointWithPathCapture(prefix, value, suffix string) string {
 escaped := url.PathEscape(value)
 if value == "" { return prefix + value + suffix }
 return prefix + escaped + suffix
}`,
		"duplicate-result": `func endpointWithPathCapture(prefix, value, suffix string) string {
 escaped := url.PathEscape(value)
 return prefix + escaped + escaped + suffix
}`,
		"discard-boundary": `func endpointWithPathCapture(prefix, value, suffix string) string {
 escaped := url.PathEscape(value)
 return "/different/" + escaped + suffix
}`,
		"query-discard": `func endpointWithQuery(path string, values url.Values) string {
 _ = values.Encode()
 return path
}`,
		"query-reassignment": `func endpointWithQuery(path string, values url.Values) string {
 encoded := values.Encode()
 encoded = "fixed=true"
 return path + "?" + encoded
}`,
		"query-post-transform": `func endpointWithQuery(path string, values url.Values) string {
 return path + "?" + strings.ReplaceAll(values.Encode(), "+", "%20")
}`,
		"query-altered-return": `func endpointWithQuery(path string, values url.Values) string {
 if strings.ContainsAny(path, "?#") { panic("endpoint path must not contain a query or fragment") }
 return "/different?" + values.Encode()
}`,
	}
	for name, body := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "bad.go", `package provider
import (
 "net/url"
 "strings"
)
var _ = strings.Builder{}
var _ = url.Values{}
`+body+"\n")
			if _, err := ExtractProvider(dir); err == nil || !strings.Contains(err.Error(), "escape result") {
				t.Fatalf("malicious endpoint builder body was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsClientAliasesAndMethodValues(t *testing.T) {
	fixtures := map[string]string{
		"receiver-alias": `package provider
import "context"
func bad(ctx context.Context, c *Client, endpoint string) {
 alias := c
 alias.DoRequestWithResponse(ctx, "GET", endpoint, nil, nil)
}
`,
		"method-value": `package provider
import "context"
type Client struct{}
func (c *Client) DoRequestWithResponse(context.Context, string, string, interface{}, interface{}) {}
func bad(ctx context.Context, c *Client, endpoint string) {
 request := c.DoRequestWithResponse
 request(ctx, "GET", endpoint, nil, nil)
}
`,
		"method-expression": `package provider
import "context"
type Client struct{}
func (c *Client) DoRequestWithResponse(context.Context, string, string, interface{}, interface{}) {}
func bad(ctx context.Context, c *Client, endpoint string) {
 request := (*Client).DoRequestWithResponse
 request(c, ctx, "GET", endpoint, nil, nil)
}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if name == "receiver-alias" {
				writeHTTPFixtureSupport(t, dir)
			}
			writeFixture(t, dir, "bad.go", fixture)
			_, err := ExtractProvider(dir)
			if err == nil {
				t.Fatalf("aliased Client transport escaped extraction")
			}
		})
	}
}

func TestStrictHTTPPolicyRejectsTypeCorrectBypasses(t *testing.T) {
	type fixture struct {
		files map[string]string
		want  string
	}
	fixtures := map[string]fixture{
		"method-value-in-client-go": {files: map[string]string{"client.go": `package provider
import "context"
func invokeTransport(operation func(context.Context, string, string, any, any) error) {}
func (c *Client) passTransport() { invokeTransport(c.DoRequestWithResponse) }
`}, want: "Client transport methods may not be used as values"},
		"package-client-alias": {files: map[string]string{"bad.go": `package provider
import "context"
type ProviderClient = Client
func badAlias(c *ProviderClient) { operation := c.DoRequestWithResponse; _ = operation; _ = context.Background() }
`}, want: "Client transport methods may not be used as values"},
		"method-value-return": {files: map[string]string{"bad.go": `package provider
import "context"
func returnTransport(c *Client) func(context.Context, string, string, any, any) error { return c.DoRequestWithResponse }
`}, want: "Client transport methods may not be used as values"},
		"nested-higher-order": {files: map[string]string{"bad.go": `package provider
import "context"
type operation func(context.Context, string, string, any, any) error
func outer(value operation) operation { return value }
func inner(value operation) operation { return value }
func badNested(c *Client) { _ = outer(inner(c.DoRequestWithResponse)) }
`}, want: "Client transport methods may not be used as values"},
		"cross-file-higher-order": {files: map[string]string{
			"pass.go": `package provider
import "context"
type requestOperation func(context.Context, string, string, any, any) error
func pass(operation requestOperation) requestOperation { return operation }
`,
			"use.go": `package provider
func badCrossFile(c *Client) { _ = pass(c.DoRequestWithResponse) }
`,
		}, want: "Client transport methods may not be used as values"},
		"client-interface-assignment": {files: map[string]string{"bad.go": `package provider
import "context"
type requester interface { DoRequestWithResponse(context.Context, string, string, any, any) error }
func badInterface(ctx context.Context, c *Client) error { var transport requester = c; return transport.DoRequestWithResponse(ctx, "GET", "/hidden", nil, nil) }
`}, want: "Client-to-interface assignment is forbidden"},
		"client-interface-conversion": {files: map[string]string{"bad.go": `package provider
import "context"
type requester interface { DoRequestWithResponse(context.Context, string, string, any, any) error }
func badConversion(ctx context.Context, c *Client) error { transport := requester(c); return transport.DoRequestWithResponse(ctx, "GET", "/hidden", nil, nil) }
`}, want: "Client-to-interface conversion is forbidden"},
		"client-interface-parameter": {files: map[string]string{"bad.go": `package provider
import "context"
type requester interface { DoRequestWithResponse(context.Context, string, string, any, any) error }
func callRequester(ctx context.Context, transport requester) error { return transport.DoRequestWithResponse(ctx, "GET", "/hidden", nil, nil) }
func badParameter(ctx context.Context, c *Client) error { return callRequester(ctx, c) }
`}, want: "passing Client to an interface parameter is forbidden"},
		"client-interface-return": {files: map[string]string{"bad.go": `package provider
import "context"
type requester interface { DoRequestWithResponse(context.Context, string, string, any, any) error }
func badReturn(c *Client) requester { return c }
`}, want: "returning Client as an interface is forbidden"},
		"client-interface-field": {files: map[string]string{"bad.go": `package provider
import "context"
type requester interface { DoRequestWithResponse(context.Context, string, string, any, any) error }
type holder struct { transport requester }
func badField(c *Client) holder { return holder{transport: c} }
`}, want: "storing Client in an interface field is forbidden"},
		"promoted-client-transport": {files: map[string]string{"bad.go": `package provider
import "context"
type wrappedClient struct { *Client }
func badPromoted(ctx context.Context, c *wrappedClient) error { return c.DoRequestWithResponse(ctx, "GET", "/hidden", nil, nil) }
`}, want: "embedding and promotion are forbidden"},
		"raw-function-value": {files: map[string]string{"bad.go": `package provider
import h "net/http"
func badRawValue() { operation := h.Get; _ = operation }
`}, want: "raw net/http transport reference"},
		"embedded-promoted-do": {files: map[string]string{"bad.go": `package provider
import h "net/http"
type wrapper struct { *h.Client }
func badEmbedded(value *wrapper, request *h.Request) { _, _ = value.Do(request) }
`}, want: "raw net/http transport reference"},
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			for filename, contents := range fixture.files {
				writeFixture(t, dir, filename, contents)
			}
			_, err := ExtractProvider(dir)
			if err == nil || !strings.Contains(err.Error(), fixture.want) {
				t.Fatalf("strict HTTP policy bypass was accepted: %v", err)
			}
		})
	}
}

func TestFrameworkProviderDataAssertionHasOneExactConsumerForm(t *testing.T) {
	t.Run("exact-configure-assertion", func(t *testing.T) {
		dir := t.TempDir()
		writeFixture(t, dir, "safe.go", `package provider
import (
 "context"
 "github.com/hashicorp/terraform-plugin-framework/resource"
)
type Client struct{}
type configured struct { client *Client }
func (configured) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
 if req.ProviderData == nil { return }
 client, ok := req.ProviderData.(*Client)
 _, _ = client, ok
}
`)
		if _, err := ExtractProvider(dir); err != nil {
			t.Fatalf("exact framework Configure assertion was rejected: %v", err)
		}
	})
	t.Run("alias-assertion", func(t *testing.T) {
		dir := t.TempDir()
		writeFixture(t, dir, "bad.go", `package provider
import (
 "context"
 "github.com/hashicorp/terraform-plugin-framework/resource"
)
type Client struct{}
type ProviderClient = Client
type configured struct { client *Client }
func (configured) Configure(_ context.Context, req resource.ConfigureRequest, _ *resource.ConfigureResponse) {
 if req.ProviderData == nil { return }
 client, ok := req.ProviderData.(*ProviderClient)
 _, _ = client, ok
}
`)
		_, err := ExtractProvider(dir)
		if err == nil || !strings.Contains(err.Error(), "exact Configure assertion") {
			t.Fatalf("aliased ProviderData assertion was accepted: %v", err)
		}
	})
	t.Run("outside-configure", func(t *testing.T) {
		dir := t.TempDir()
		writeFixture(t, dir, "bad.go", `package provider
type Client struct{}
func recoverClient(value any) { client, ok := value.(*Client); _, _ = client, ok }
`)
		_, err := ExtractProvider(dir)
		if err == nil || !strings.Contains(err.Error(), "exact Configure assertion") {
			t.Fatalf("ProviderData-style assertion outside Configure was accepted: %v", err)
		}
	})
}

func TestStrictTransportNamePolicyRejectsInterfacesGenericsAndEmbedding(t *testing.T) {
	fixtures := map[string]string{
		"generic-constraint": `package provider
import "context"
type requester interface { DoRequestWithResponse(context.Context, string, string, any, any) error }
func invoke[T requester](ctx context.Context, transport T) error { return transport.DoRequestWithResponse(ctx, "GET", "/hidden", nil, nil) }
`,
		"embedded-interface": `package provider
import "context"
type requester interface { DoRequestWithResponse(context.Context, string, string, any, any) error }
type embedded interface { requester }
func invoke(ctx context.Context, transport embedded) error { return transport.DoRequestWithResponse(ctx, "GET", "/hidden", nil, nil) }
`,
		"embedded-struct-interface": `package provider
import "context"
type requester interface { DoRequestWithResponse(context.Context, string, string, any, any) error }
type wrapper struct { requester }
func invoke(ctx context.Context, transport wrapper) error { return transport.DoRequestWithResponse(ctx, "GET", "/hidden", nil, nil) }
`,
		"same-name-custom-method": `package provider
import "context"
type requester struct{}
func (requester) DoRequestWithResponse(context.Context, string, string, any, any) error { return nil }
func invoke(ctx context.Context, transport requester) error { return transport.DoRequestWithResponse(ctx, "GET", "/hidden", nil, nil) }
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", fixture)
			_, err := ExtractProvider(dir)
			if err == nil || (!strings.Contains(err.Error(), "exposes reviewed transport name") && !strings.Contains(err.Error(), "exact Client object") && !strings.Contains(err.Error(), "exact Client methods")) {
				t.Fatalf("transport-name policy bypass was accepted: %v", err)
			}
		})
	}
}

func TestStrictRawHTTPPolicyRejectsGenericEmbeddingWrappersAndReflection(t *testing.T) {
	fixtures := map[string]string{
		"generic-hide-client": `package provider
import "net/http"
func hide[T any](value T) any { return value }
func bad(client *http.Client) { _ = hide(client) }
`,
		"generic-hide-transport": `package provider
import "net/http"
func hide[T any](value T) T { return value }
func bad(transport *http.Transport) { var erased any = hide(transport); _ = erased }
`,
		"generic-struct-storage": `package provider
import "net/http"
type holder[T any] struct { value T }
func bad(client *http.Client) { _ = holder[*http.Client]{value: client} }
`,
		"generic-field-assignment": `package provider
import "net/http"
type holder[T any] struct { value T }
func bad(client *http.Client, destination *holder[*http.Client]) { destination.value = client }
`,
		"generic-slice-storage": `package provider
import "net/http"
type holder[T any] []T
func bad(client *http.Client) { _ = holder[*http.Client]{client} }
`,
		"round-tripper-interface": `package provider
import "net/http"
type transport interface { RoundTrip(*http.Request) (*http.Response, error) }
`,
		"embedded-do-interface": `package provider
import "net/http"
type doer interface { Do(*http.Request) (*http.Response, error) }
type embedded interface { doer }
`,
		"custom-do": `package provider
import "net/http"
type custom struct{}
func (custom) Do(*http.Request) (*http.Response, error) { return nil, nil }
`,
		"custom-round-trip": `package provider
import "net/http"
type custom struct{}
func (custom) RoundTrip(*http.Request) (*http.Response, error) { return nil, nil }
`,
		"sneaky-wrapper": `package provider
import "net/http"
func Sneaky(client *http.Client, request *http.Request) { _, _ = client.Do(request) }
`,
		"method-expression": `package provider
import "net/http"
func bad() { operation := (*http.Client).Do; _ = operation }
`,
		"client-alias": `package provider
import "net/http"
type HTTPClient = http.Client
`,
		"default-client-interface": `package provider
import "net/http"
func bad() { var hidden any = http.DefaultClient; _ = hidden }
`,
		"default-transport-interface": `package provider
import "net/http"
func bad() { var hidden http.RoundTripper = http.DefaultTransport; _ = hidden }
`,
		"closure-interface-return": `package provider
import "net/http"
func bad(client *http.Client) { hidden := func() any { return client }; _ = hidden }
`,
		"interface-assertion": `package provider
import "net/http"
func bad(hidden any) { client, _ := hidden.(*http.Client); _ = client }
`,
		"type-switch-recovery": `package provider
import "net/http"
func bad(hidden any) { switch hidden.(type) { case *http.Transport: } }
`,
		"reflect-method-by-name": `package provider
import ("net/http"; "reflect")
func bad() { reflected := reflect.ValueOf(http.DefaultClient); method := reflected.MethodByName("Do"); method.Call(nil) }
`,
		"reflect-call": `package provider
import "reflect"
func bad(value reflect.Value) { value.Call(nil) }
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "bad.go", fixture)
			_, err := ExtractProvider(dir)
			if err == nil {
				t.Fatal("raw HTTP or reflective dispatch bypass was accepted")
			}
		})
	}
}

func TestReviewedDoReadWithResponseTransportPolicy(t *testing.T) {
	t.Run("direct-dynamic-path", func(t *testing.T) {
		dir := t.TempDir()
		writeFixture(t, dir, "bad.go", `package provider
import "context"
type Client struct{}
func (*Client) DoReadWithResponse(context.Context, string, string, any, any) error { return nil }
func bad(ctx context.Context, client *Client, path string) error {
 return client.DoReadWithResponse(ctx, "GET", path, nil, nil)
}
`)
		_, err := ExtractProvider(dir)
		if err == nil || (!strings.Contains(err.Error(), "unresolved HTTP method or path") && !strings.Contains(err.Error(), "unresolved dynamic HTTP path or query name")) {
			t.Fatalf("direct DoReadWithResponse call escaped path extraction: %v", err)
		}
	})

	t.Run("internal-policy-outside-client", func(t *testing.T) {
		dir := t.TempDir()
		writeFixture(t, dir, "bad.go", `package provider
import "context"
type Client struct{}
type safeReadRetryPolicy struct{}
type safeReadRetryHooks struct{}
func (*Client) doReadWithResponsePolicy(context.Context, string, string, any, any, safeReadRetryPolicy, safeReadRetryHooks) error { return nil }
func bad(ctx context.Context, client *Client) error {
 return client.doReadWithResponsePolicy(ctx, "GET", "/hidden", nil, nil, safeReadRetryPolicy{}, safeReadRetryHooks{})
}
`)
		_, err := ExtractProvider(dir)
		if err == nil || !strings.Contains(err.Error(), "internal Client transport method called outside Client implementation") {
			t.Fatalf("internal read policy escaped its exact Client-only allowlist: %v", err)
		}
	})

	fixtures := map[string]string{
		"method-value": `package provider
import "context"
type Client struct{}
func (*Client) DoReadWithResponse(context.Context, string, string, any, any) error { return nil }
func bad(client *Client) { operation := client.DoReadWithResponse; _ = operation }
`,
		"interface": `package provider
import "context"
type requester interface { DoReadWithResponse(context.Context, string, string, any, any) error }
`,
		"generic": `package provider
import "context"
type requester interface { DoReadWithResponse(context.Context, string, string, any, any) error }
func bad[T requester](client T) { _ = client }
`,
		"reflection": `package provider
import "reflect"
type Client struct{}
func bad(client *Client) {
 method := reflect.ValueOf(client).MethodByName("DoReadWithResponse")
 method.Call(nil)
}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "bad.go", fixture)
			_, err := ExtractProvider(dir)
			if err == nil {
				t.Fatal("DoReadWithResponse transport bypass was accepted")
			}
		})
	}
}

func TestStrictQueryPolicyRejectsPointerMethodValuesAndHigherOrderEscape(t *testing.T) {
	fixtures := map[string]map[string]string{
		"pointer-dynamic": {"bad.go": `package provider
import "net/url"
func bad(query *url.Values, key string) { query.Set(key, "value") }
`},
		"pointer-index-dynamic": {"bad.go": `package provider
import "net/url"
func bad(query *url.Values, key string) { (*query)[key] = []string{"value"} }
`},
		"pointer-alias-dynamic": {"bad.go": `package provider
import "net/url"
func bad(query *url.Values, key string) { alias := query; alias.Add(key, "value") }
`},
		"bound-method": {"bad.go": `package provider
import "net/url"
func bad(query url.Values) { set := query.Set; set("hidden", "value") }
`},
		"higher-order-argument": {"bad.go": `package provider
import "net/url"
func take(func(string, string)) {}
func bad(query url.Values) { take(query.Add) }
`},
		"higher-order-return": {"bad.go": `package provider
import "net/url"
func bad(query url.Values) func(string, string) { return query.Set }
`},
		"method-expression": {"bad.go": `package provider
import "net/url"
func bad() { set := url.Values.Set; _ = set }
`},
		"cross-file-pointer": {
			"mutate.go": `package provider
import "net/url"
func mutate(query *url.Values, key string) { query.Set(key, "value") }
`,
			"use.go": `package provider
import "net/url"
func bad(query url.Values, key string) { mutate(&query, key) }
`,
		},
	}
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for filename, fixture := range files {
				writeFixture(t, dir, filename, fixture)
			}
			_, err := ExtractProvider(dir)
			if err == nil || (!strings.Contains(err.Error(), "url.Values") && !strings.Contains(err.Error(), "exact reviewed query helper")) {
				t.Fatalf("query method-object or pointer escape was accepted: %v", err)
			}
		})
	}
}

func TestStrictQueryPolicyRejectsDefinedTypeConversionBypasses(t *testing.T) {
	fixtures := map[string]map[string]string{
		"review-complete": {"bad.go": `package provider
import "net/url"
type hiddenValues map[string][]string
func bad(query url.Values, key string) {
 hidden := hiddenValues(query)
 hidden[key] = []string{"dynamic"}
 restored := url.Values(hidden)
 restored.Set("literal", "value")
}
`},
		"map-alias": {"bad.go": `package provider
import "net/url"
type hiddenValues = map[string][]string
func bad(query url.Values, key string) {
 hidden := hiddenValues(query)
 hidden[key] = []string{"dynamic"}
 restored := url.Values(hidden)
 restored.Set("literal", "value")
}
`},
		"defined-url-values": {"bad.go": `package provider
import "net/url"
type hiddenValues url.Values
func bad(query url.Values, key string) {
 hidden := hiddenValues(query)
 hidden[key] = []string{"dynamic"}
 restored := url.Values(hidden)
 restored.Set("literal", "value")
}
`},
		"pointer-defined-type": {"bad.go": `package provider
import "net/url"
type hiddenValues url.Values
func bad(query url.Values, key string) {
 hidden := (*hiddenValues)(&query)
 (*hidden)[key] = []string{"dynamic"}
 restored := url.Values(*hidden)
 restored.Set("literal", "value")
}
`},
		"pointer-conversion": {"bad.go": `package provider
import "net/url"
type hiddenValues url.Values
func bad(query url.Values, key string) {
 hidden := hiddenValues(query)
 hidden[key] = []string{"dynamic"}
 restored := (*url.Values)(&hidden)
 restored.Set("literal", "value")
}
`},
		"plain-map": {"bad.go": `package provider
import "net/url"
func bad(query url.Values, key string) {
 hidden := map[string][]string(query)
 hidden[key] = []string{"dynamic"}
 restored := url.Values(hidden)
 restored.Set("literal", "value")
}
`},
		"generic-conversion": {"bad.go": `package provider
import "net/url"
func restore[T ~map[string][]string](hidden T) url.Values { return url.Values(hidden) }
func bad(query url.Values, key string) {
 hidden := map[string][]string(query)
 hidden[key] = []string{"dynamic"}
 restored := restore(hidden)
 restored.Set("literal", "value")
}
`},
		"generic-defined-round-trip": {"bad.go": `package provider
import "net/url"
type hiddenValues map[string][]string
func hide[T ~map[string][]string](value T, key string) T { value[key] = []string{"dynamic"}; return value }
func bad(query url.Values, key string) {
 hidden := hide(hiddenValues(query), key)
 restored := url.Values(hidden)
 restored.Set("literal", "value")
}
`},
		"conversion-round-trip": {"bad.go": `package provider
import "net/url"
type first map[string][]string
type second map[string][]string
func bad(query url.Values, key string) {
 hidden := second(first(query))
 hidden[key] = []string{"dynamic"}
 restored := url.Values(map[string][]string(hidden))
 restored.Set("literal", "value")
}
`},
		"cross-file": {
			"hide.go": `package provider
import "net/url"
type hiddenValues map[string][]string
func hide(query url.Values, key string) hiddenValues {
 hidden := hiddenValues(query)
 hidden[key] = []string{"dynamic"}
 return hidden
}
`,
			"restore.go": `package provider
import "net/url"
func bad(query url.Values, key string) {
 restored := url.Values(hide(query, key))
 restored.Set("literal", "value")
}
`,
		},
	}
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for filename, fixture := range files {
				writeFixture(t, dir, filename, fixture)
			}
			_, err := ExtractProvider(dir)
			if err == nil || !strings.Contains(err.Error(), "non-identity conversion to exact url.Values is forbidden") {
				t.Fatalf("defined-type url.Values conversion bypass was accepted: %v", err)
			}
		})
	}
}

func TestStrictQueryPolicyAllowsExactURLValuesIdentityConversion(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "safe.go", `package provider
import "net/url"
type queryAlias = url.Values
func safe(query queryAlias) {
 restored := url.Values(query)
 restored.Set("literal", "value")
}
`)
	operations, err := ExtractProvider(dir)
	if err != nil || len(operations) != 0 {
		t.Fatalf("exact url.Values identity conversion was rejected: operations=%v error=%v", operations, err)
	}
}

func TestStrictQueryPolicyRejectsBackingMapStorageErasure(t *testing.T) {
	fixtures := map[string]string{
		"package-map-var": `package provider
import "net/url"
var query = url.Values{}
var hidden map[string][]string = query
`,
		"package-container": `package provider
import "net/url"
var query = url.Values{}
var hidden = []url.Values{query}
`,
		"package-function-return": `package provider
import "net/url"
var erase = func(query url.Values) any { return query }
`,
		"unnamed-map-var": `package provider
import "net/url"
func bad(query url.Values, key string) {
 var hidden map[string][]string = query
 hidden[key] = []string{"dynamic"}
}
`,
		"unnamed-map-assignment": `package provider
import "net/url"
func bad(query url.Values, key string) {
 var hidden map[string][]string
 hidden = query
 hidden[key] = []string{"dynamic"}
}
`,
		"map-alias-var": `package provider
import "net/url"
type hiddenValues = map[string][]string
func bad(query url.Values, key string) {
 var hidden hiddenValues = query
 hidden[key] = []string{"dynamic"}
}
`,
		"defined-map-conversion": `package provider
import "net/url"
type hiddenValues map[string][]string
func bad(query url.Values, key string) {
 hidden := hiddenValues(query)
 hidden[key] = []string{"dynamic"}
}
`,
		"pointer-defined-conversion": `package provider
import "net/url"
type hiddenValues map[string][]string
func bad(query url.Values, key string) {
 hidden := (*hiddenValues)(&query)
 (*hidden)[key] = []string{"dynamic"}
}
`,
		"struct-map-literal": `package provider
import "net/url"
type holder struct { query map[string][]string }
func bad(query url.Values, key string) {
 hidden := holder{query: query}
 hidden.query[key] = []string{"dynamic"}
}
`,
		"struct-interface-assignment": `package provider
import "net/url"
type holder struct { query any }
func bad(query url.Values, key string) {
 var hidden holder
 hidden.query = query
 hidden.query.(url.Values)[key] = []string{"dynamic"}
}
`,
		"struct-exact-field": `package provider
import "net/url"
type holder struct { query url.Values }
func bad(query url.Values, key string) {
 hidden := holder{query: query}
 hidden.query[key] = []string{"dynamic"}
}
`,
		"interface-assertion": `package provider
import "net/url"
func bad(query url.Values, key string) {
 var hidden any = query
 restored := hidden.(url.Values)
 restored[key] = []string{"dynamic"}
}
`,
		"interface-type-switch": `package provider
import "net/url"
func bad(query url.Values, key string) {
 var hidden any = query
 switch restored := hidden.(type) {
 case url.Values:
  restored[key] = []string{"dynamic"}
 }
}
`,
		"map-parameter": `package provider
import "net/url"
func erase(hidden map[string][]string, key string) { hidden[key] = []string{"dynamic"} }
func bad(query url.Values, key string) { erase(query, key) }
`,
		"interface-parameter": `package provider
import "net/url"
func erase(hidden any, key string) { hidden.(url.Values)[key] = []string{"dynamic"} }
func bad(query url.Values, key string) { erase(query, key) }
`,
		"map-return": `package provider
import "net/url"
func erase(query url.Values) map[string][]string { return query }
func bad(query url.Values, key string) { erase(query)[key] = []string{"dynamic"} }
`,
		"interface-return": `package provider
import "net/url"
func erase(query url.Values) any { return query }
func bad(query url.Values, key string) { erase(query).(url.Values)[key] = []string{"dynamic"} }
`,
		"slice-literal": `package provider
import "net/url"
func bad(query url.Values, key string) {
 hidden := []url.Values{query}
 hidden[0][key] = []string{"dynamic"}
}
`,
		"array-literal": `package provider
import "net/url"
func bad(query url.Values, key string) {
 hidden := [1]url.Values{query}
 hidden[0][key] = []string{"dynamic"}
}
`,
		"channel-send": `package provider
import "net/url"
func bad(query url.Values, hidden chan url.Values) { hidden <- query }
`,
		"map-literal": `package provider
import "net/url"
func bad(query url.Values, key string) {
 hidden := map[string]url.Values{"query": query}
 hidden["query"][key] = []string{"dynamic"}
}
`,
		"append-container": `package provider
import "net/url"
func bad(query url.Values, key string) {
 hidden := append([]url.Values(nil), query)
 hidden[0][key] = []string{"dynamic"}
}
`,
		"higher-order-interface": `package provider
import "net/url"
func apply(operation func(any, string), query url.Values, key string) { operation(query, key) }
func mutate(hidden any, key string) { hidden.(url.Values)[key] = []string{"dynamic"} }
func bad(query url.Values, key string) { apply(mutate, query, key) }
`,
		"generic-parameter": `package provider
import "net/url"
func store[T any](value T) []T { return []T{value} }
func bad(query url.Values, key string) {
 hidden := store(query)
 hidden[0][key] = []string{"dynamic"}
}
`,
		"generic-map-constraint": `package provider
import "net/url"
func erase[T ~map[string][]string](value T, key string) T { value[key] = []string{"dynamic"}; return value }
func bad(query url.Values, key string) { _ = erase(query, key) }
`,
		"closure-interface-capture": `package provider
import "net/url"
func bad(query url.Values, key string) func() {
 hidden := any(query)
 return func() { hidden.(url.Values)[key] = []string{"dynamic"} }
}
`,
	}
	for name, fixture := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeFixture(t, dir, "bad.go", fixture)
			_, err := ExtractProvider(dir)
			if err == nil || (!strings.Contains(err.Error(), "url.Values backing map") && !strings.Contains(err.Error(), "url.Values may only be passed")) {
				t.Fatalf("url.Values backing-map erasure was accepted: %v", err)
			}
		})
	}
}

func TestStrictQueryPolicyAllowsExactTrackedFlowAndClone(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "safe.go", `package provider
import "net/url"
type queryAlias = url.Values
var packageQuery = url.Values{"scope": {"package"}}
var packageAlias url.Values = packageQuery
func cloneURLValues(values url.Values) url.Values {
 cloned := make(url.Values, len(values))
 for key, entries := range values {
  cloned[key] = append([]string(nil), entries...)
 }
 return cloned
}
func safe(query url.Values) {
 alias := query
 var exact url.Values = alias
 var trueAlias queryAlias = exact
 trueAlias.Set("page", "1")
 trueAlias["sort"] = []string{"name"}
 cloned := cloneURLValues(trueAlias)
 cloned.Add("filter", "active")
}
`)
	operations, err := ExtractProvider(dir)
	if err != nil || len(operations) != 0 {
		t.Fatalf("exact tracked url.Values flow or reviewed clone was rejected: operations=%v error=%v", operations, err)
	}
}

func TestStaticPointerQueryMutationIsInventoried(t *testing.T) {
	dir := t.TempDir()
	writeHTTPFixtureSupport(t, dir)
	writeFixture(t, dir, "query.go", `package provider
import (
 "context"
 "net/url"
)
func request(ctx context.Context, client *Client, query *url.Values) error {
 (*query).Set("page", "1")
 (*query)["sort"] = []string{"name"}
 return client.DoRequestWithResponse(ctx, "GET", endpointWithQuery("/things", *query), nil, nil)
}
`)
	operations, err := ExtractProvider(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0].Path != "/things" || strings.Join(operations[0].QueryParameters, ",") != "page,sort" {
		t.Fatalf("static pointer query was not inventoried: %+v", operations)
	}
}

func TestStrictPolicyAllowsUnrelatedInterfacesGenericsAndStaticQueries(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "safe.go", `package provider
import (
 "net/url"
 "reflect"
)
type runner interface { Run(string) error }
type box[T any] struct { value T }
func identity[T any](value T) T { return value }
type helper struct{}
func (helper) Set(string, string) {}
func safe(query url.Values, h helper) {
 query.Set("page", "1")
 query.Add("sort", "name")
 h.Set("unrelated", "value")
 _ = identity(box[int]{value: 1})
 _ = reflect.DeepEqual(1, 1)
}
`)
	operations, err := ExtractProvider(dir)
	if err != nil || len(operations) != 0 {
		t.Fatalf("ordinary interface/generic/static-query helper produced a false positive: operations=%v error=%v", operations, err)
	}
}

func TestExtractorRejectsIncompletePackages(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "bad.go", `package provider
func bad() { missing() }
`)
	_, err := ExtractProvider(dir)
	if err == nil || !strings.Contains(err.Error(), "complete type-correct package") || !strings.Contains(err.Error(), "undefined: missing") {
		t.Fatalf("missing type information was accepted: %v", err)
	}
}

func TestStrictQueryPolicyRejectsHelperAndCrossFileMutation(t *testing.T) {
	fixtures := map[string]map[string]string{
		"set-helper": {"bad.go": `package provider
import "net/url"
func addFilter(query url.Values, key string) { query.Set(key, "value") }
func bad(query url.Values, key string) { addFilter(query, key) }
`},
		"add-helper": {"bad.go": `package provider
import "net/url"
func addFilter(query url.Values, key string) { query.Add(key, "value") }
func bad(query url.Values, key string) { addFilter(query, key) }
`},
		"index-helper": {"bad.go": `package provider
import "net/url"
func addFilter(query url.Values, key string) { query[key] = []string{"value"} }
func bad(query url.Values, key string) { addFilter(query, key) }
`},
		"alias-helper": {"bad.go": `package provider
import "net/url"
func addFilter(query url.Values, key string) { query.Set(key, "value") }
func bad(query url.Values, key string) { mutate := addFilter; mutate(query, key) }
`},
		"alias-fixed-helper": {"bad.go": `package provider
import "net/url"
func addFilter(query url.Values) { query.Set("hidden", "value") }
func bad(query url.Values) { mutate := addFilter; mutate(query) }
`},
		"return-helper": {"bad.go": `package provider
import "net/url"
func addFilter(query url.Values, key string) url.Values { query.Set(key, "value"); return query }
func bad(query url.Values, key string) { _ = addFilter(query, key) }
`},
		"package-values-alias": {"bad.go": `package provider
import "net/url"
type Query = url.Values
func bad(query Query, key string) { query.Add(key, "value") }
`},
		"fixed-unknown-helper": {"bad.go": `package provider
import "net/url"
func mutate(query url.Values) { query.Set("hidden", "value") }
func bad(query url.Values) { mutate(query) }
`},
		"cross-file": {
			"mutate.go": `package provider
import "net/url"
func mutateCrossFile(query url.Values, key string) { query.Set(key, "value") }
`,
			"use.go": `package provider
import "net/url"
func badCrossFileQuery(query url.Values, key string) { mutateCrossFile(query, key) }
`,
		},
	}
	for name, files := range fixtures {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			for filename, contents := range files {
				writeFixture(t, dir, filename, contents)
			}
			_, err := ExtractProvider(dir)
			if err == nil || (!strings.Contains(err.Error(), "dynamic url.Values") && !strings.Contains(err.Error(), "exact reviewed query helper")) {
				t.Fatalf("query mutation bypass was accepted: %v", err)
			}
		})
	}
}

func TestExtractorRejectsAliasedNetHTTPAndDynamicURLValuesKeys(t *testing.T) {
	t.Run("aliased-net-http", func(t *testing.T) {
		dir := t.TempDir()
		writeFixture(t, dir, "bad.go", `package provider
import h "net/http"
func bad(endpoint string) {
 get := h.Get
 _, _ = get(endpoint)
}
`)
		_, err := ExtractProvider(dir)
		if err == nil || !strings.Contains(err.Error(), "raw net/http transport reference") {
			t.Fatalf("aliased net/http call escaped detection: %v", err)
		}
	})
	for name, mutation := range map[string]string{
		"map-literal":      `query := url.Values{name: {"value"}}`,
		"index-assignment": `query := url.Values{}; query[name] = []string{"value"}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeHTTPFixtureSupport(t, dir)
			writeFixture(t, dir, "bad.go", `package provider
import ("context"; "net/url")
func bad(ctx context.Context, c *Client, name string) {
 `+mutation+`
 c.DoRequestWithResponse(ctx, "GET", endpointWithQuery("/things", query), nil, nil)
}
`)
			_, err := ExtractProvider(dir)
			if err == nil || !strings.Contains(err.Error(), "dynamic url.Values") {
				t.Fatalf("dynamic url.Values key escaped detection: %v", err)
			}
		})
	}
}

func TestExtractorAllowsNonHTTPClientHelper(t *testing.T) {
	dir := t.TempDir()
	writeFixture(t, dir, "helper.go", `package provider
import "net/http"
type Client struct{}
type helper struct{}
func (c *Client) Label() string { return "safe" }
func (helper) Get(string) {}
func (helper) NewRequest(string) {}
func safe(headers http.Header, h helper) { _ = headers.Get("X-Request-ID"); h.Get("value"); h.NewRequest("value") }
`)
	operations, err := ExtractProvider(dir)
	if err != nil || len(operations) != 0 {
		t.Fatalf("non-HTTP helper produced a false positive: operations=%v error=%v", operations, err)
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
	if manifest.Upstream.Tag != "v1.98.0" || manifest.Upstream.Commit != "d8f71d7bdbd7c9873d98293f83d64c6db72847e6" || manifest.Upstream.Python != "3.12.14" || manifest.Upstream.UV != "0.12.6" {
		t.Fatalf("upstream provenance is not pinned: %+v", manifest.Upstream)
	}
	if manifest.OpenAPI.PathCount != 586 || manifest.OpenAPI.OperationCount != 800 || manifest.Supplemental.RouteCount != 1 || len(manifest.RequiredLazyFeatures) != 33 {
		t.Fatalf("artifact or complete lazy-feature review counts changed: %+v %+v lazy=%d", manifest.OpenAPI, manifest.Supplemental, len(manifest.RequiredLazyFeatures))
	}
	var pins ReviewedPins
	pinsData, err := os.ReadFile(filepath.Join(root, "internal", "contract", "reviewed-pins.json"))
	if err != nil || json.Unmarshal(pinsData, &pins) != nil {
		t.Fatalf("load reviewed pins: %v", err)
	}
	if pins.Upstream.UV != "0.12.6" || pins.Artifacts.ProviderGolden.OperationCount != 108 || pins.Artifacts.Classification.OperationCount != 693 || len(pins.LazyFeatures) != 33 {
		t.Fatalf("reviewed pins changed unexpectedly: artifacts=%+v lazy=%d", pins.Artifacts, len(pins.LazyFeatures))
	}
}

func TestJWTKeyMappingOperationsAreExactSupportedInventory(t *testing.T) {
	root := repositoryRoot(t)
	var golden []Operation
	if err := readJSONFile(filepath.Join(root, "internal", "contractapi", "testdata", "provider-operations.golden.json"), &golden); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"POST /jwt/key/mapping/delete": "",
		"GET /jwt/key/mapping/info":    "id",
		"GET /jwt/key/mapping/list":    "page,size",
		"POST /jwt/key/mapping/new":    "",
		"POST /jwt/key/mapping/update": "",
	}
	seen := map[string]string{}
	for _, operation := range golden {
		key := operation.Method + " " + operation.Path
		if _, reviewed := want[key]; reviewed {
			seen[key] = strings.Join(operation.QueryParameters, ",")
		}
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("JWT key mapping provider inventory = %#v, want %#v", seen, want)
	}

	var classification ReviewedClassification
	if err := readJSONFile(filepath.Join(root, "internal", "contract", "reviewed-operation-classification.json"), &classification); err != nil {
		t.Fatal(err)
	}
	for _, operation := range classification.Operations {
		key := operation.Method + " " + operation.Path
		if _, supported := want[key]; supported {
			t.Fatalf("supported JWT key mapping operation remains classified as unsupported: %s", key)
		}
	}
}

func TestReviewedAdjacentIndexAndV1BetaClassifications(t *testing.T) {
	root := repositoryRoot(t)
	var classification ReviewedClassification
	if err := readJSONFile(filepath.Join(root, "internal", "contract", "reviewed-operation-classification.json"), &classification); err != nil {
		t.Fatal(err)
	}
	actual := map[string]string{}
	for _, operation := range classification.Operations {
		actual[operation.Method+" "+operation.Path] = operation.Category
	}
	want := map[string]string{
		"GET /v1/indexes":                                        "vector_store_management",
		"POST /v1/indexes":                                       "vector_store_management",
		"GET /v1beta/agents":                                     "agent_prompt_tool_management",
		"POST /v1beta/agents":                                    "agent_prompt_tool_management",
		"DELETE /v1beta/agents/{name}":                           "agent_prompt_tool_management",
		"GET /v1beta/agents/{name}":                              "agent_prompt_tool_management",
		"GET /v1beta/agents/{name}/versions":                     "agent_prompt_tool_management",
		"POST /v1beta/interactions":                              "inference_workload",
		"DELETE /v1beta/interactions/{interaction_id}":           "inference_workload",
		"GET /v1beta/interactions/{interaction_id}":              "inference_workload",
		"POST /v1beta/interactions/{interaction_id}/cancel":      "operational_action",
		"POST /v1beta/models/{model_name}:countTokens":           "inference_workload",
		"POST /v1beta/models/{model_name}:generateContent":       "inference_workload",
		"POST /v1beta/models/{model_name}:streamGenerateContent": "inference_workload",
	}
	for operation, category := range want {
		if actual[operation] != category {
			t.Errorf("%s category = %q, want %q", operation, actual[operation], category)
		}
	}
}

func TestReviewedClassificationRejectsCoverageDrift(t *testing.T) {
	root := repositoryRoot(t)
	load := func(t *testing.T) (Manifest, ReviewedPins, ReviewedClassification, map[string][]contractOperation, []Operation) {
		t.Helper()
		var manifest Manifest
		var pins ReviewedPins
		var classification ReviewedClassification
		if err := readJSONFile(filepath.Join(root, "internal", "contract", "manifest.json"), &manifest); err != nil {
			t.Fatal(err)
		}
		if err := readJSONFile(filepath.Join(root, "internal", "contract", "reviewed-pins.json"), &pins); err != nil {
			t.Fatal(err)
		}
		if err := readJSONFile(filepath.Join(root, "internal", "contract", "reviewed-operation-classification.json"), &classification); err != nil {
			t.Fatal(err)
		}
		contracts, _, _, err := LoadContracts(filepath.Join(root, "openapi.json"), filepath.Join(root, "internal", "contract", "supplemental-routes.json"))
		if err != nil {
			t.Fatal(err)
		}
		extracted, err := ExtractProvider(filepath.Join(root, "internal", "provider"))
		if err != nil {
			t.Fatal(err)
		}
		supported, err := ResolveOperations(extracted, contracts)
		if err != nil {
			t.Fatal(err)
		}
		return manifest, pins, classification, contracts, supported
	}

	t.Run("unclassified-route", func(t *testing.T) {
		manifest, pins, classification, contracts, supported := load(t)
		classification.Operations = classification.Operations[1:]
		if err := validateReview(manifest, pins, classification, contracts, supported); err == nil || !strings.Contains(err.Error(), "unclassified API operation") {
			t.Fatalf("removed classification was accepted: %v", err)
		}
	})
	t.Run("stale-route", func(t *testing.T) {
		manifest, pins, classification, contracts, supported := load(t)
		classification.Operations = append(classification.Operations, ClassifiedOperation{Method: "GET", Path: "/removed-route", Category: "health"})
		if err := validateReview(manifest, pins, classification, contracts, supported); err == nil || !strings.Contains(err.Error(), "stale operation") {
			t.Fatalf("stale classification was accepted: %v", err)
		}
	})
	t.Run("new-openapi-route", func(t *testing.T) {
		manifest, pins, classification, contracts, supported := load(t)
		contracts["GET"] = append(contracts["GET"], contractOperation{method: "GET", path: "/new-durable-record", queryParams: map[string]bool{}})
		if err := validateReview(manifest, pins, classification, contracts, supported); err == nil || !strings.Contains(err.Error(), "unclassified API operation GET /new-durable-record") {
			t.Fatalf("new API route was accepted without review: %v", err)
		}
	})
	t.Run("unknown-category", func(t *testing.T) {
		manifest, pins, classification, contracts, supported := load(t)
		classification.Operations[0].Category = "catch_all"
		if err := validateReview(manifest, pins, classification, contracts, supported); err == nil || !strings.Contains(err.Error(), "unknown classification category") {
			t.Fatalf("unknown category was accepted: %v", err)
		}
	})
}

func TestLazyExpansionHasExactReviewedClassification(t *testing.T) {
	root := repositoryRoot(t)
	var classification ReviewedClassification
	if err := readJSONFile(filepath.Join(root, "internal", "contract", "reviewed-operation-classification.json"), &classification); err != nil {
		t.Fatal(err)
	}
	var golden []Operation
	if err := readJSONFile(filepath.Join(root, "internal", "contractapi", "testdata", "provider-operations.golden.json"), &golden); err != nil {
		t.Fatal(err)
	}
	classified := map[string]string{}
	for _, operation := range classification.Operations {
		classified[operation.Method+" "+operation.Path] = operation.Category
	}
	supported := map[string]bool{}
	for _, operation := range golden {
		supported[operation.Method+" "+operation.Path] = true
	}
	var openapi struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := readJSONFile(filepath.Join(root, "openapi.json"), &openapi); err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for path, item := range openapi.Paths {
		group := ""
		switch {
		case strings.HasPrefix(path, "/v1/mcp/"):
			group = "mcp"
		case strings.HasPrefix(path, "/prompts") || path == "/utils/dotprompt_json_converter":
			group = "prompt"
		case strings.HasPrefix(path, "/cloudzero") || strings.HasPrefix(path, "/vantage") || strings.HasPrefix(path, "/config_overrides"):
			group = "integration"
		}
		if group == "" {
			continue
		}
		for method := range item {
			upper := strings.ToUpper(method)
			if _, ok := map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true, "HEAD": true, "OPTIONS": true, "TRACE": true}[upper]; !ok {
				continue
			}
			counts[group]++
			key := upper + " " + path
			if !supported[key] && classified[key] == "" {
				t.Errorf("lazy expansion operation lacks exact review: %s", key)
			}
		}
	}
	if counts["mcp"] != 33 || counts["prompt"] != 10 || counts["integration"] != 16 {
		t.Fatalf("lazy expansion counts changed: %v", counts)
	}
	for key, category := range map[string]string{
		"GET /v1/mcp/toolset":                             "mcp_toolset_management",
		"POST /v1/mcp/server/{server_id}/user-credential": "mcp_credential_configuration",
		"GET /prompts/{prompt_id}/info":                   "agent_prompt_tool_management",
		"POST /prompts/test":                              "testing_validation",
		"POST /cloudzero/init":                            "global_proxy_configuration",
		"POST /vantage/export":                            "operational_action",
		"POST /config_overrides/hashicorp_vault":          "global_proxy_configuration",
	} {
		if classified[key] != category {
			t.Errorf("%s category = %q, want %q", key, classified[key], category)
		}
	}
}

func TestReviewedSemanticClassifications(t *testing.T) {
	root := repositoryRoot(t)
	var classification ReviewedClassification
	if err := readJSONFile(filepath.Join(root, "internal", "contract", "reviewed-operation-classification.json"), &classification); err != nil {
		t.Fatal(err)
	}
	actual := map[string]string{}
	for _, operation := range classification.Operations {
		actual[operation.Method+" "+operation.Path] = operation.Category
	}
	for key, expected := range map[string]string{
		"POST /guardrails/register":                       "guardrail_management",
		"GET /customer/daily/activity":                    "spend_analytics",
		"GET /management/v1/spend_logs/end_users":         "spend_analytics",
		"GET /callbacks/list":                             "suggestion_discovery",
		"GET /team/{team_id}/callback":                    "integration_callback_configuration",
		"POST /utils/token_counter":                       "inference_workload",
		"POST /models/{model_name}:countTokens":           "inference_workload",
		"POST /apply_guardrail":                           "inference_workload",
		"GET /auto_router/shadow_eval":                    "spend_analytics",
		"POST /auto_router/shadow_eval/start":             "operational_action",
		"GET /scim/v2/Schemas":                            "public_metadata",
		"POST /v1/vector_stores/{vector_store_id}/search": "inference_workload",
		"POST /v1/a2a/discover":                           "suggestion_discovery",
		"GET /mcp/enabled":                                "health",
	} {
		if actual[key] != expected {
			t.Errorf("%s category = %q, want %q", key, actual[key], expected)
		}
	}
	if _, stale := actual["GET /v1/a2a/discover"]; stale {
		t.Error("stale lazy snapshot method remained classified")
	}
}

func TestReviewedPinsRejectCoordinatedGeneratedArtifactEdits(t *testing.T) {
	root := repositoryRoot(t)
	stage := t.TempDir()
	for _, relative := range []string{
		"openapi.json",
		"internal/contract/supplemental-routes.json",
		"internal/contract/manifest.json",
		"internal/contract/reviewed-pins.json",
		"internal/contract/reviewed-operation-classification.json",
		"internal/contractapi/testdata/provider-operations.golden.json",
	} {
		source := filepath.Join(root, relative)
		destination := filepath.Join(stage, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(stage, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "internal", "provider"), filepath.Join(stage, "internal", "provider")); err != nil {
		t.Fatal(err)
	}

	openapiPath := filepath.Join(stage, "openapi.json")
	body, err := os.ReadFile(openapiPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(openapiPath, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	manifestPath := filepath.Join(stage, "internal", "contract", "manifest.json")
	if err := readJSONFile(manifestPath, &manifest); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append(body, '\n'))
	manifest.OpenAPI.SHA256 = hex.EncodeToString(sum[:])
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(stage); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("coordinated generated artifact edits bypassed reviewed pins: %v", err)
	}
}

func TestProviderGoldenPinRejectsByteChange(t *testing.T) {
	root := repositoryRoot(t)
	var pins ReviewedPins
	if err := readJSONFile(filepath.Join(root, "internal", "contract", "reviewed-pins.json"), &pins); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(root, pins.Artifacts.ProviderGolden.Path))
	if err != nil {
		t.Fatal(err)
	}
	changed := filepath.Join(t.TempDir(), "provider-operations.golden.json")
	if err := os.WriteFile(changed, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyChecksum(changed, pins.Artifacts.ProviderGolden.SHA256); err == nil {
		t.Fatal("byte-changed provider golden matched reviewed pin")
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

const exactFixtureContainsDotPathComponent = `func containsDotPathComponent(value string) bool {
 for _, component := range strings.Split(value, "/") {
  if component == "." || component == ".." { return true }
 }
 return false
}`

const exactFixtureHardenDotSegment = `func hardenDotSegment(value string) string {
 switch value {
 case ".":
  return "%2E"
 case "..":
  return "%2E%2E"
 default:
  return value
 }
}`

func writeReviewedBuilderFixture(t *testing.T, dir, contains, harden, beforeEscape, afterEscape string) {
	t.Helper()
	writeFixture(t, dir, "builders.go", `package provider
import (
 "net/url"
 "strings"
)
const invalidReviewedEndpoint = "/.terraform-provider-litellm-invalid-reviewed-endpoint"
`+contains+"\n"+harden+`
func endpointWithPathSegment(prefix, value, suffix string) string {
 `+beforeEscape+`
 if strings.Contains(value, "/") { return invalidReviewedEndpoint }
 escaped := url.PathEscape(value)
 `+afterEscape+`
 return prefix + hardenDotSegment(escaped) + suffix
}
func endpointWithPathCapture(prefix, value, suffix string) string {
 if containsDotPathComponent(value) { return invalidReviewedEndpoint }
 escaped := url.PathEscape(value)
 return prefix + escaped + suffix
}
func endpointWithFallbackPathSegment(prefix, value, suffix string) string {
 escaped := url.PathEscape(value)
 return prefix + hardenDotSegment(escaped) + suffix
}
func endpointWithQuery(path string, values url.Values) string {
 if strings.HasPrefix(path, invalidReviewedEndpoint) { return invalidReviewedEndpoint }
 if strings.ContainsAny(path, "?#") { panic("endpoint path must not contain a query or fragment") }
 if len(values) == 0 { return path }
 return path + "?" + values.Encode()
}
`)
}

func writeHTTPFixtureSupport(t *testing.T, dir string) {
	t.Helper()
	writeFixture(t, dir, "support.go", `package provider
import (
 "context"
 "net/url"
 "strings"
)
const invalidReviewedEndpoint = "/.terraform-provider-litellm-invalid-reviewed-endpoint"
type Client struct{}
func (*Client) DoRequestWithResponse(context.Context, string, string, any, any) error { return nil }
func hardenDotSegment(value string) string {
 switch value {
 case ".":
  return "%2E"
 case "..":
  return "%2E%2E"
 default:
  return value
 }
}
func containsDotPathComponent(value string) bool {
 for _, component := range strings.Split(value, "/") {
  if component == "." || component == ".." { return true }
 }
 return false
}
func endpointWithPathSegment(prefix, value, suffix string) string {
 if strings.Contains(value, "/") { return invalidReviewedEndpoint }
 escaped := url.PathEscape(value)
 return prefix + hardenDotSegment(escaped) + suffix
}
func endpointWithPathCapture(prefix, value, suffix string) string {
 if containsDotPathComponent(value) { return invalidReviewedEndpoint }
 escaped := url.PathEscape(value)
 return prefix + escaped + suffix
}
func endpointWithFallbackPathSegment(prefix, value, suffix string) string {
 escaped := url.PathEscape(value)
 return prefix + hardenDotSegment(escaped) + suffix
}
func endpointWithQuery(path string, values url.Values) string {
 if strings.HasPrefix(path, invalidReviewedEndpoint) { return invalidReviewedEndpoint }
 if strings.ContainsAny(path, "?#") { panic("endpoint path must not contain a query or fragment") }
 if len(values) == 0 { return path }
 return path + "?" + values.Encode()
}
`)
}

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
