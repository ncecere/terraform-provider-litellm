package contractapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func writeHTTPFixtureSupport(t *testing.T, dir string) {
	t.Helper()
	writeFixture(t, dir, "support.go", `package provider
import (
 "context"
 "net/url"
)
type Client struct{}
func (*Client) DoRequestWithResponse(context.Context, string, string, any, any) error { return nil }
func endpointWithPathSegment(prefix, value, suffix string) string { return prefix + url.PathEscape(value) + suffix }
func endpointWithPathCapture(prefix, value, suffix string) string { return prefix + url.PathEscape(value) + suffix }
func endpointWithFallbackPathSegment(prefix, value, suffix string) string { return prefix + url.PathEscape(value) + suffix }
func endpointWithQuery(path string, values url.Values) string { return path + "?" + values.Encode() }
`)
}

func writeFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
