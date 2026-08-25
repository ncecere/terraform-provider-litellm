package provider

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

func unifiedAccessGroupTestSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&UnifiedAccessGroupResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func unifiedAccessGroupTestPlan(t *testing.T, schema resourceschema.Schema, data UnifiedAccessGroupResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := plan.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set test plan: %v", diagnostics)
	}
	return plan
}

func unifiedAccessGroupTestState(t *testing.T, schema resourceschema.Schema, data UnifiedAccessGroupResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := state.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set test state: %v", diagnostics)
	}
	return state
}

func unifiedAccessGroupStringList(values ...string) types.List {
	items := make([]attr.Value, 0, len(values))
	for _, value := range values {
		items = append(items, types.StringValue(value))
	}
	return types.ListValueMust(types.StringType, items)
}

func unifiedAccessGroupListStrings(t *testing.T, value types.List) []string {
	t.Helper()
	var result []string
	if diagnostics := value.ElementsAs(context.Background(), &result, false); diagnostics.HasError() {
		t.Fatalf("decode string list: %v", diagnostics)
	}
	return result
}

func unifiedAccessGroupPlanModel(name string, assigned types.List) UnifiedAccessGroupResourceModel {
	return UnifiedAccessGroupResourceModel{
		ID:                 types.StringUnknown(),
		AccessGroupID:      types.StringUnknown(),
		AccessGroupName:    types.StringValue(name),
		Description:        types.StringNull(),
		AccessModelNames:   types.ListUnknown(types.StringType),
		AccessMCPServerIDs: types.ListUnknown(types.StringType),
		AccessAgentIDs:     types.ListUnknown(types.StringType),
		AssignedTeamIDs:    types.ListUnknown(types.StringType),
		AssignedKeyIDs:     assigned,
		CreatedAt:          types.StringUnknown(),
		CreatedBy:          types.StringUnknown(),
		UpdatedAt:          types.StringUnknown(),
		UpdatedBy:          types.StringUnknown(),
	}
}

func unifiedAccessGroupAPIResponse(id, name string, assigned []string) map[string]interface{} {
	return map[string]interface{}{
		"access_group_id":       id,
		"access_group_name":     name,
		"description":           nil,
		"access_model_names":    []string{},
		"access_mcp_server_ids": []string{},
		"access_agent_ids":      []string{},
		"assigned_team_ids":     []string{},
		"assigned_key_ids":      assigned,
		"created_at":            "2026-08-24T00:00:00Z",
		"created_by":            "admin",
		"updated_at":            "2026-08-24T00:00:00Z",
		"updated_by":            "admin",
	}
}

func writeUnifiedAccessGroupKeyPage(writer http.ResponseWriter, page, totalPages int, hashes ...string) {
	items := make([]map[string]interface{}, 0, len(hashes))
	for _, hash := range hashes {
		items = append(items, map[string]interface{}{
			"token":    hash,
			"key_name": "unsafe-response-suffix-must-not-be-used",
		})
	}
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{
		"keys":         items,
		"total_count":  len(hashes),
		"current_page": page,
		"total_pages":  totalPages,
	})
}

func writeEmptyUnifiedAccessGroupKeyPage(writer http.ResponseWriter) {
	_ = json.NewEncoder(writer).Encode(map[string]interface{}{
		"keys":         []interface{}{},
		"total_count":  0,
		"current_page": 1,
		"total_pages":  0,
	})
}

func TestUnifiedAccessGroupAssignedKeySchemaPreservesHistoricalListContract(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	var response resource.SchemaResponse
	(&UnifiedAccessGroupResource{}).Schema(ctx, resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	if response.Schema.Version != 0 {
		t.Fatalf("schema version = %d, want historical version 0", response.Schema.Version)
	}
	attribute, ok := response.Schema.Attributes["assigned_key_ids"].(resourceschema.ListAttribute)
	if !ok {
		t.Fatalf("assigned_key_ids type = %T, want schema.ListAttribute", response.Schema.Attributes["assigned_key_ids"])
	}
	if got, want := attribute.GetType().TerraformType(ctx), (tftypes.List{ElementType: tftypes.String}); !got.Equal(want) {
		t.Fatalf("assigned_key_ids Terraform type = %s, want %s", got, want)
	}
	if !attribute.Optional || !attribute.Computed || attribute.Required {
		t.Fatalf("assigned_key_ids must remain Optional+Computed: %#v", attribute)
	}

	hash := strings.Repeat("a", 64)
	for name, test := range map[string]struct {
		value     types.List
		wantError bool
		secret    string
	}{
		"bare":                 {value: unifiedAccessGroupStringList(hash)},
		"bare uppercase":       {value: unifiedAccessGroupStringList(strings.ToUpper(hash))},
		"prefixed":             {value: unifiedAccessGroupStringList("sha256:" + hash)},
		"uppercase prefixed":   {value: unifiedAccessGroupStringList("SHA256:" + strings.ToUpper(hash))},
		"duplicates and order": {value: unifiedAccessGroupStringList("sha256:"+hash, hash, "sha256:"+hash)},
		"empty detach":         {value: unifiedAccessGroupStringList()},
		"unknown list":         {value: types.ListUnknown(types.StringType)},
		"unknown item":         {value: types.ListValueMust(types.StringType, []attr.Value{types.StringUnknown()})},
		"null item":            {value: types.ListValueMust(types.StringType, []attr.Value{types.StringNull()}), wantError: true},
		"malformed":            {value: unifiedAccessGroupStringList("sha256:not-a-hash"), wantError: true},
		"raw special":          {value: unifiedAccessGroupStringList("sk-do-not-echo#suffix?token=private"), wantError: true, secret: "sk-do-not-echo#suffix?token=private"},
	} {
		t.Run(name, func(t *testing.T) {
			var validationResponse validator.ListResponse
			request := validator.ListRequest{Path: path.Root("assigned_key_ids"), ConfigValue: test.value}
			for _, listValidator := range attribute.Validators {
				listValidator.ValidateList(ctx, request, &validationResponse)
			}
			if got := validationResponse.Diagnostics.HasError(); got != test.wantError {
				t.Fatalf("validation error = %t, want %t: %v", got, test.wantError, validationResponse.Diagnostics)
			}
			if test.secret != "" && strings.Contains(fmt.Sprint(validationResponse.Diagnostics), test.secret) {
				t.Fatal("validation diagnostics copied a raw key")
			}
		})
	}
}

func TestUnifiedAccessGroupAssignedKeyListRemainsUsableByHCLConsumers(t *testing.T) {
	t.Parallel()

	values := []cty.Value{cty.StringVal("z"), cty.StringVal("z"), cty.StringVal("a")}
	ctx := &hcl.EvalContext{
		Variables: map[string]cty.Value{"assigned_key_ids": cty.ListVal(values)},
		Functions: map[string]function.Function{"concat": stdlib.ConcatFunc},
	}
	indexedExpression, diagnostics := hclsyntax.ParseExpression([]byte(`assigned_key_ids[1]`), "index.tf", hcl.Pos{Line: 1, Column: 1})
	if diagnostics.HasErrors() {
		t.Fatalf("parse indexing expression: %v", diagnostics)
	}
	indexed, diagnostics := indexedExpression.Value(ctx)
	if diagnostics.HasErrors() || indexed.AsString() != "z" {
		t.Fatalf("list indexing result = %v, diagnostics = %v", indexed, diagnostics)
	}
	concatExpression, diagnostics := hclsyntax.ParseExpression([]byte(`concat(assigned_key_ids, ["new"])`), "concat.tf", hcl.Pos{Line: 1, Column: 1})
	if diagnostics.HasErrors() {
		t.Fatalf("parse concat expression: %v", diagnostics)
	}
	concatenated, diagnostics := concatExpression.Value(ctx)
	if diagnostics.HasErrors() {
		t.Fatalf("evaluate concat: %v", diagnostics)
	}
	moduleValue, err := convert.Convert(concatenated, cty.List(cty.String))
	if err != nil {
		t.Fatalf("convert to list(string) module input: %v", err)
	}
	if moduleValue.LengthInt() != 4 || moduleValue.Index(cty.NumberIntVal(1)).AsString() != "z" {
		t.Fatalf("concat/module value lost list order or duplicates: %s", moduleValue.GoString())
	}
}

func TestUnifiedAccessGroupNormalizesRequestsAndPreservesEquivalentListRepresentations(t *testing.T) {
	t.Parallel()

	a := strings.Repeat("a", 64)
	b := strings.Repeat("b", 64)
	configured := unifiedAccessGroupStringList("SHA256:"+strings.ToUpper(b), "sha256:"+a, a, "sha256:"+a)
	request := map[string]interface{}{}
	if err := addAssignedKeyListToRequest(request, configured); err != nil {
		t.Fatalf("normalize request: %v", err)
	}
	if got, want := request["assigned_key_ids"], []string{a, b}; !reflect.DeepEqual(got, want) {
		t.Fatalf("request hashes = %#v, want sorted deduplicated bare hashes %#v", got, want)
	}
	actual := map[string]bool{a: true, b: true}
	if reconciled := reconcileUnifiedAccessGroupKeyMembership(configured, types.ListNull(types.StringType), actual); !reconciled.Equal(configured) {
		t.Fatalf("equivalent membership changed list order, duplicates, or representation: %#v", reconciled)
	}
	drifted := reconcileUnifiedAccessGroupKeyMembership(configured, types.ListNull(types.StringType), map[string]bool{b: true})
	if got, want := unifiedAccessGroupListStrings(t, drifted), []string{b}; !reflect.DeepEqual(got, want) {
		t.Fatalf("real membership drift = %#v, want deterministic bare hashes %#v", got, want)
	}
}

func TestUnifiedAccessGroupAcceptsGeneratedStatefulAndWriteOnlyKeyIDs(t *testing.T) {
	t.Parallel()

	managementIDs := []string{
		hashKeyForID("sk-generated-key"),
		hashKeyForID("sk-stateful-predefined-key"),
		hashKeyForID("sk-write-only-predefined-key"),
	}
	request := map[string]interface{}{}
	if err := addAssignedKeyListToRequest(request, unifiedAccessGroupStringList(managementIDs...)); err != nil {
		t.Fatalf("normalize current litellm_key IDs: %v", err)
	}
	got := request["assigned_key_ids"].([]string)
	want := make([]string, 0, len(managementIDs))
	for _, managementID := range managementIDs {
		bare, err := keyHashFromID(managementID)
		if err != nil {
			t.Fatalf("decode management ID: %v", err)
		}
		want = append(want, bare)
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("normalized generated/write-only IDs = %#v, want %#v", got, want)
	}
}

func TestUnifiedAccessGroupCreateVerifiesDurableMembershipAndWarnsAboutPeerCaches(t *testing.T) {
	t.Parallel()

	a := strings.Repeat("1", 64)
	b := strings.Repeat("2", 64)
	configured := unifiedAccessGroupStringList("sha256:"+b, strings.ToUpper(a), "sha256:"+b)
	var mutex sync.Mutex
	lookups := map[string]int{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group":
			_, _ = writer.Write([]byte(`[]`))
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			hash := request.URL.Query().Get("key")
			mutex.Lock()
			lookups[hash]++
			mutex.Unlock()
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"key":  hash,
				"info": map[string]interface{}{"access_group_ids": []string{"ag-create"}},
			})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/access_group":
			var body map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if got, want := body["assigned_key_ids"], []interface{}{a, b}; !reflect.DeepEqual(got, want) {
				t.Errorf("assigned_key_ids request = %#v, want %#v", got, want)
			}
			_ = json.NewEncoder(writer).Encode(unifiedAccessGroupAPIResponse("ag-create", "keys", []string{a, b}))
		case request.Method == http.MethodGet && request.URL.Path == "/key/list":
			query := request.URL.Query()
			if query.Get("access_group_id") != "ag-create" || query.Get("return_full_object") != "true" || query.Get("page") != "1" || query.Get("size") != "100" {
				t.Errorf("global key discovery query = %q", request.URL.RawQuery)
			}
			writeUnifiedAccessGroupKeyPage(writer, 1, 1, a, b)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := unifiedAccessGroupTestSchema(t)
	plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("keys", configured))
	resourceUnderTest := &UnifiedAccessGroupResource{
		client:                      &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()},
		keyVerificationMaxAttempts:  3,
		keyVerificationInitialDelay: 0,
	}
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", response.Diagnostics)
	}
	var state UnifiedAccessGroupResourceModel
	if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
		t.Fatalf("decode created state: %v", diagnostics)
	}
	if state.AccessGroupID.ValueString() != "ag-create" || !state.AssignedKeyIDs.Equal(configured) {
		t.Fatalf("created state did not preserve configured list: %#v", state)
	}
	diagnostic := fmt.Sprint(response.Diagnostics)
	for _, required := range []string{"Peer Worker Authorization Caches May Remain Stale", "durable database membership", "configured cache TTL", "security-sensitive detach", "does not promise cross-worker"} {
		if !strings.Contains(diagnostic, required) {
			t.Fatalf("cache warning omitted %q: %s", required, diagnostic)
		}
	}
	mutex.Lock()
	defer mutex.Unlock()
	if lookups[a] != 2 || lookups[b] != 2 {
		t.Fatalf("key lookups = %#v, want one preflight and one durable postflight per unique hash", lookups)
	}
}

func TestUnifiedAccessGroupCreateDoesNotAdoptGroupOnlyMembership(t *testing.T) {
	hash := strings.Repeat("f", 64)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group":
			_, _ = writer.Write([]byte(`[]`))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/access_group":
			_ = json.NewEncoder(writer).Encode(unifiedAccessGroupAPIResponse("ag-one-sided", "one-sided", []string{hash}))
		case request.Method == http.MethodGet && request.URL.Path == "/key/list":
			writeEmptyUnifiedAccessGroupKeyPage(writer)
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"key":  hash,
				"info": map[string]interface{}{"access_group_ids": []string{}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := unifiedAccessGroupTestSchema(t)
	plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("one-sided", unifiedAccessGroupStringList("sha256:"+hash)))
	resourceUnderTest := &UnifiedAccessGroupResource{
		client:                     &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()},
		keyVerificationMaxAttempts: 1,
	}
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if !response.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(response.Diagnostics), "one-sided assignment") {
		t.Fatalf("group-only create was not reported: %v", response.Diagnostics)
	}
	var state UnifiedAccessGroupResourceModel
	if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
		t.Fatalf("decode one-sided create state: %v", diagnostics)
	}
	if state.AccessGroupID.ValueString() != "ag-one-sided" || len(unifiedAccessGroupListStrings(t, state.AssignedKeyIDs)) != 0 {
		t.Fatalf("one-sided create was adopted as converged: %#v", state)
	}
}

func TestUnifiedAccessGroupMissingKeyFailsBeforeMutationWithoutDisclosure(t *testing.T) {
	t.Parallel()

	hash := strings.Repeat("3", 64)
	postCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group":
			_, _ = writer.Write([]byte(`[]`))
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			http.Error(writer, `{"detail":"missing private suffix"}`, http.StatusNotFound)
		default:
			postCalls++
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := unifiedAccessGroupTestSchema(t)
	plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("keys", unifiedAccessGroupStringList("sha256:"+hash)))
	resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}, keyVerificationMaxAttempts: 2}
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if !response.Diagnostics.HasError() || postCalls != 0 {
		t.Fatalf("missing key was not stopped before mutation: calls=%d diagnostics=%v", postCalls, response.Diagnostics)
	}
	for _, forbidden := range []string{hash, "suffix", "private"} {
		if strings.Contains(fmt.Sprint(response.Diagnostics), forbidden) {
			t.Fatalf("diagnostic disclosed %q", forbidden)
		}
	}
}

type commitThenFailRoundTripper struct {
	base    http.RoundTripper
	failure error
	once    atomic.Bool
}

func (r *commitThenFailRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := r.base.RoundTrip(request)
	if err != nil {
		return response, err
	}
	if request.Method == http.MethodPost && request.URL.Path == "/v1/access_group" && !r.once.Swap(true) {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return nil, r.failure
	}
	return response, nil
}

type failResponseReadBody struct {
	io.ReadCloser
}

func (b *failResponseReadBody) Read([]byte) (int, error) {
	return 0, errors.New("response stream failed")
}

type failCreateResponseReadRoundTripper struct {
	base http.RoundTripper
}

func (r *failCreateResponseReadRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := r.base.RoundTrip(request)
	if err == nil && request.Method == http.MethodPost && request.URL.Path == "/v1/access_group" {
		response.Body = &failResponseReadBody{ReadCloser: response.Body}
		response.ContentLength = -1
	}
	return response, err
}

func TestUnifiedAccessGroupAcceptedAndAmbiguousCreateRecoveryRetainsExplicitPartialState(t *testing.T) {
	for _, mode := range []string{
		"malformed-success",
		"missing-identity-success",
		"accepted-read-failure",
		"request-timeout-after-commit",
		"internal-server-error-after-commit",
		"bad-gateway-after-commit",
		"transport-after-commit",
		"temporary-transport-after-commit",
		"timeout-transport-after-commit",
	} {
		mode := mode
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			hash := strings.Repeat("4", 64)
			var databaseCommitted atomic.Bool
			var listCalls atomic.Int32
			var deleteCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch {
				case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group":
					call := listCalls.Add(1)
					if !databaseCommitted.Load() {
						_, _ = writer.Write([]byte(`[]`))
						return
					}
					if mode == "malformed-success" && call == 2 {
						http.Error(writer, `{"detail":"cache read failed"}`, http.StatusServiceUnavailable)
						return
					}
					_ = json.NewEncoder(writer).Encode([]interface{}{unifiedAccessGroupAPIResponse("ag-recovered", "recovered", []string{hash})})
				case request.Method == http.MethodPost && request.URL.Path == "/v1/access_group":
					databaseCommitted.Store(true)
					switch mode {
					case "malformed-success":
						writer.WriteHeader(http.StatusCreated)
						_, _ = writer.Write([]byte(`{"access_group_id":`))
					case "missing-identity-success":
						writer.WriteHeader(http.StatusCreated)
						_, _ = writer.Write([]byte(`{"accepted":true}`))
					case "request-timeout-after-commit":
						http.Error(writer, `{"detail":"response timed out"}`, http.StatusRequestTimeout)
					case "internal-server-error-after-commit":
						http.Error(writer, `{"detail":"response cache failed"}`, http.StatusInternalServerError)
					case "bad-gateway-after-commit":
						http.Error(writer, `{"detail":"proxy lost upstream response"}`, http.StatusBadGateway)
					default:
						_ = json.NewEncoder(writer).Encode(unifiedAccessGroupAPIResponse("ignored-by-transport", "recovered", []string{hash}))
					}
				case request.Method == http.MethodGet && request.URL.Path == "/key/info":
					groups := []string{}
					if databaseCommitted.Load() {
						groups = []string{"ag-recovered"}
					}
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": hash, "info": map[string]interface{}{"access_group_ids": groups}})
				case request.Method == http.MethodGet && request.URL.Path == "/key/list":
					writeUnifiedAccessGroupKeyPage(writer, 1, 1, hash)
				case request.Method == http.MethodDelete && request.URL.Path == "/v1/access_group/ag-recovered":
					deleteCalls.Add(1)
					writer.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			httpClient := server.Client()
			if mode == "accepted-read-failure" {
				httpClient = &http.Client{Transport: &failCreateResponseReadRoundTripper{base: server.Client().Transport}}
			}
			var transportFailure error
			switch mode {
			case "transport-after-commit":
				transportFailure = errors.New("connection reset after database commit")
			case "temporary-transport-after-commit":
				transportFailure = temporaryNetworkError{}
			case "timeout-transport-after-commit":
				transportFailure = timeoutNetworkError{}
			}
			if transportFailure != nil {
				httpClient = &http.Client{Transport: &commitThenFailRoundTripper{base: server.Client().Transport, failure: transportFailure}}
			}
			schema := unifiedAccessGroupTestSchema(t)
			configured := unifiedAccessGroupStringList("sha256:" + hash)
			plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("recovered", configured))
			resourceUnderTest := &UnifiedAccessGroupResource{
				client:                      &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: httpClient},
				keyVerificationMaxAttempts:  3,
				keyVerificationInitialDelay: 0,
			}
			response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
			resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
			if !response.Diagnostics.HasError() {
				t.Fatal("recovered ambiguous create must return an explicit error diagnostic")
			}
			var state UnifiedAccessGroupResourceModel
			if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
				t.Fatalf("decode recovered state: %v", diagnostics)
			}
			if state.AccessGroupID.ValueString() != "ag-recovered" || !state.AssignedKeyIDs.Equal(configured) {
				t.Fatalf("recovered partial state = %#v", state)
			}
			diagnostic := fmt.Sprint(response.Diagnostics)
			for _, required := range []string{"Recovered With Uncertain Ownership", "retained in partial state", "cannot prove"} {
				if !strings.Contains(diagnostic, required) {
					t.Fatalf("recovery diagnostic omitted %q: %s", required, diagnostic)
				}
			}

			deleteState := response.State
			deleteResponse := &resource.DeleteResponse{}
			resourceUnderTest.Delete(context.Background(), resource.DeleteRequest{State: deleteState}, deleteResponse)
			if deleteResponse.Diagnostics.HasError() || deleteCalls.Load() != 1 {
				t.Fatalf("destroy after recovery was not safe: calls=%d diagnostics=%v", deleteCalls.Load(), deleteResponse.Diagnostics)
			}
		})
	}
}

func TestUnifiedAccessGroupCreateDoesNotRecoverDefinitiveHTTPFailures(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
		http.StatusConflict,
		http.StatusMethodNotAllowed,
		http.StatusUnprocessableEntity,
		http.StatusTooManyRequests,
	} {
		status := status
		t.Run(fmt.Sprintf("status-%d", status), func(t *testing.T) {
			t.Parallel()

			var postAttempted atomic.Bool
			var listCalls atomic.Int32
			var deleteCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch {
				case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group":
					listCalls.Add(1)
					if postAttempted.Load() {
						_ = json.NewEncoder(writer).Encode([]interface{}{unifiedAccessGroupAPIResponse("other-actor", "raced", []string{})})
						return
					}
					_, _ = writer.Write([]byte(`[]`))
				case request.Method == http.MethodPost && request.URL.Path == "/v1/access_group":
					postAttempted.Store(true)
					http.Error(writer, `{"detail":"definitive request failure"}`, status)
				case request.Method == http.MethodDelete:
					deleteCalls.Add(1)
					writer.WriteHeader(http.StatusNoContent)
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			schema := unifiedAccessGroupTestSchema(t)
			plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("raced", unifiedAccessGroupStringList()))
			resourceUnderTest := &UnifiedAccessGroupResource{
				client:                     &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()},
				keyVerificationMaxAttempts: 1,
			}
			response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
			resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)

			if !response.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(response.Diagnostics), fmt.Sprintf("status %d", status)) {
				t.Fatalf("HTTP %d create diagnostics: %v", status, response.Diagnostics)
			}
			if listCalls.Load() != 1 {
				t.Fatalf("HTTP %d exact-name list calls = %d, want preflight only", status, listCalls.Load())
			}
			if deleteCalls.Load() != 0 {
				t.Fatalf("HTTP %d create deleted another actor's group", status)
			}
			var state UnifiedAccessGroupResourceModel
			if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
				t.Fatalf("decode HTTP %d failed-create state: %v", status, diagnostics)
			}
			if !state.AccessGroupID.IsUnknown() || !state.ID.IsUnknown() {
				t.Fatalf("HTTP %d adopted another actor's identity: id=%#v access_group_id=%#v", status, state.ID, state.AccessGroupID)
			}
			if status == http.StatusConflict && strings.Contains(fmt.Sprint(response.Diagnostics), "Uncertain Ownership") {
				t.Fatalf("raced 409 entered create recovery: %v", response.Diagnostics)
			}
		})
	}
}

type failCreateRoundTripper struct {
	base    http.RoundTripper
	failure error
}

func (r *failCreateRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.Method == http.MethodPost && request.URL.Path == "/v1/access_group" {
		return nil, r.failure
	}
	return r.base.RoundTrip(request)
}

func TestUnifiedAccessGroupCreateDoesNotRecoverTerminalTransportFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure error
	}{
		{name: "TLS hostname", failure: x509.HostnameError{Host: "wrong-host"}},
		{name: "canceled", failure: context.Canceled},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var listCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.Method == http.MethodGet && request.URL.Path == "/v1/access_group" {
					listCalls.Add(1)
					_, _ = writer.Write([]byte(`[]`))
					return
				}
				http.NotFound(writer, request)
			}))
			defer server.Close()

			httpClient := &http.Client{Transport: &failCreateRoundTripper{base: server.Client().Transport, failure: test.failure}}
			schema := unifiedAccessGroupTestSchema(t)
			plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("terminal", unifiedAccessGroupStringList()))
			resourceUnderTest := &UnifiedAccessGroupResource{
				client:                     &Client{APIBase: server.URL, APIKey: "test", HTTPClient: httpClient},
				keyVerificationMaxAttempts: 1,
			}
			response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
			resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)

			if !response.Diagnostics.HasError() {
				t.Fatal("terminal create transport failure unexpectedly succeeded")
			}
			if listCalls.Load() != 1 {
				t.Fatalf("terminal create transport exact-name list calls = %d, want preflight only", listCalls.Load())
			}
			var state UnifiedAccessGroupResourceModel
			if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
				t.Fatalf("decode terminal transport state: %v", diagnostics)
			}
			if !state.AccessGroupID.IsUnknown() {
				t.Fatalf("terminal create transport failure adopted an identity: %#v", state.AccessGroupID)
			}
		})
	}
}

func TestUnifiedAccessGroupCreateRecoveryRequiresExactAssignedKeyMembership(t *testing.T) {
	t.Run("same name with different keys is rejected", func(t *testing.T) {
		t.Parallel()

		configuredHash := strings.Repeat("6", 64)
		otherHash := strings.Repeat("7", 64)
		var committed atomic.Bool
		var listCalls atomic.Int32
		var keyListCalls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch {
			case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group":
				listCalls.Add(1)
				if committed.Load() {
					_ = json.NewEncoder(writer).Encode([]interface{}{unifiedAccessGroupAPIResponse("other-actor", "same-name", []string{otherHash})})
					return
				}
				_, _ = writer.Write([]byte(`[]`))
			case request.Method == http.MethodGet && request.URL.Path == "/key/info":
				hash := request.URL.Query().Get("key")
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": hash, "info": map[string]interface{}{"access_group_ids": []string{}}})
			case request.Method == http.MethodPost && request.URL.Path == "/v1/access_group":
				committed.Store(true)
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(`{"access_group_id":`))
			case request.Method == http.MethodGet && request.URL.Path == "/key/list":
				keyListCalls.Add(1)
				writeUnifiedAccessGroupKeyPage(writer, 1, 1, otherHash)
			default:
				http.NotFound(writer, request)
			}
		}))
		defer server.Close()

		schema := unifiedAccessGroupTestSchema(t)
		plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("same-name", unifiedAccessGroupStringList("sha256:"+configuredHash)))
		resourceUnderTest := &UnifiedAccessGroupResource{
			client:                     &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()},
			keyVerificationMaxAttempts: 1,
		}
		response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
		resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)

		if !response.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(response.Diagnostics), "full identity did not exactly match") {
			t.Fatalf("different-key candidate diagnostics: %v", response.Diagnostics)
		}
		if listCalls.Load() != 2 || keyListCalls.Load() != 0 {
			t.Fatalf("different-key recovery list calls: groups=%d keys=%d", listCalls.Load(), keyListCalls.Load())
		}
		var state UnifiedAccessGroupResourceModel
		if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
			t.Fatalf("decode different-key recovery state: %v", diagnostics)
		}
		if !state.AccessGroupID.IsUnknown() {
			t.Fatalf("same-name different-key candidate was adopted: %#v", state.AccessGroupID)
		}
	})

	t.Run("normalized unordered duplicate keys match before two-sided verification", func(t *testing.T) {
		t.Parallel()

		a := strings.Repeat("8", 64)
		b := strings.Repeat("9", 64)
		configured := unifiedAccessGroupStringList("SHA256:"+strings.ToUpper(b), "sha256:"+a, a, "SHA256:"+strings.ToUpper(a))
		candidateAssignments := []string{"sha256:" + strings.ToUpper(a), b, "SHA256:" + strings.ToUpper(b), a}
		var committed atomic.Bool
		var keyInfoCalls atomic.Int32
		var keyListCalls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch {
			case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group":
				if committed.Load() {
					_ = json.NewEncoder(writer).Encode([]interface{}{unifiedAccessGroupAPIResponse("ag-normalized", "normalized", candidateAssignments)})
					return
				}
				_, _ = writer.Write([]byte(`[]`))
			case request.Method == http.MethodGet && request.URL.Path == "/key/info":
				keyInfoCalls.Add(1)
				hash := request.URL.Query().Get("key")
				groups := []string{}
				if committed.Load() {
					groups = []string{"ag-normalized"}
				}
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": hash, "info": map[string]interface{}{"access_group_ids": groups}})
			case request.Method == http.MethodPost && request.URL.Path == "/v1/access_group":
				var body map[string]interface{}
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Errorf("decode normalized create request: %v", err)
				}
				if got, want := body["assigned_key_ids"], []interface{}{a, b}; !reflect.DeepEqual(got, want) {
					t.Errorf("normalized create key hashes = %#v, want %#v", got, want)
				}
				committed.Store(true)
				writer.WriteHeader(http.StatusCreated)
				_, _ = writer.Write([]byte(`{"access_group_id":`))
			case request.Method == http.MethodGet && request.URL.Path == "/key/list":
				keyListCalls.Add(1)
				writeUnifiedAccessGroupKeyPage(writer, 1, 1, b, a)
			default:
				http.NotFound(writer, request)
			}
		}))
		defer server.Close()

		schema := unifiedAccessGroupTestSchema(t)
		plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("normalized", configured))
		resourceUnderTest := &UnifiedAccessGroupResource{
			client:                     &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()},
			keyVerificationMaxAttempts: 1,
		}
		response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
		resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)

		if !response.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(response.Diagnostics), "Recovered With Uncertain Ownership") {
			t.Fatalf("normalized candidate recovery diagnostics: %v", response.Diagnostics)
		}
		var state UnifiedAccessGroupResourceModel
		if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
			t.Fatalf("decode normalized recovery state: %v", diagnostics)
		}
		if state.AccessGroupID.ValueString() != "ag-normalized" || !state.AssignedKeyIDs.Equal(configured) {
			t.Fatalf("normalized exact-key candidate was not retained after two-sided verification: %#v", state)
		}
		if keyInfoCalls.Load() != 4 || keyListCalls.Load() != 1 {
			t.Fatalf("normalized candidate did not run preflight plus two-sided verification: key info=%d key list=%d", keyInfoCalls.Load(), keyListCalls.Load())
		}
	})
}

func TestUnifiedAccessGroupAcceptedCreateRecoveryRetriesSuccessfulEmptyDiscovery(t *testing.T) {
	var committed atomic.Bool
	var listCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group":
			call := listCalls.Add(1)
			// Call 1 is preflight. Call 2 is a successful but still-empty
			// accepted-create postflight. The bounded operation-aware retry must
			// discover call 3 rather than orphaning the accepted group.
			if !committed.Load() || call == 2 {
				_, _ = writer.Write([]byte(`[]`))
				return
			}
			_ = json.NewEncoder(writer).Encode([]interface{}{unifiedAccessGroupAPIResponse("ag-propagated", "propagated", []string{})})
		case request.Method == http.MethodPost && request.URL.Path == "/v1/access_group":
			committed.Store(true)
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"access_group_id":`))
		case request.Method == http.MethodGet && request.URL.Path == "/key/list":
			writeEmptyUnifiedAccessGroupKeyPage(writer)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := unifiedAccessGroupTestSchema(t)
	plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("propagated", unifiedAccessGroupStringList()))
	resourceUnderTest := &UnifiedAccessGroupResource{
		client:                      &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()},
		keyVerificationMaxAttempts:  3,
		keyVerificationInitialDelay: 0,
	}
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	if !response.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(response.Diagnostics), "Uncertain Ownership") {
		t.Fatalf("accepted create recovery diagnostics: %v", response.Diagnostics)
	}
	var state UnifiedAccessGroupResourceModel
	if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
		t.Fatalf("decode propagated recovery state: %v", diagnostics)
	}
	if state.AccessGroupID.ValueString() != "ag-propagated" || listCalls.Load() != 3 {
		t.Fatalf("propagated recovery state=%#v list calls=%d", state.AccessGroupID, listCalls.Load())
	}
}

func TestUnifiedAccessGroupAcceptedCreateRecoveryEmptyExhaustionIsActionable(t *testing.T) {
	var listCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group":
			listCalls.Add(1)
			_, _ = writer.Write([]byte(`[]`))
		case request.Method == http.MethodPost && request.URL.Path == "/v1/access_group":
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"access_group_id":`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := unifiedAccessGroupTestSchema(t)
	plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("delayed", unifiedAccessGroupStringList()))
	resourceUnderTest := &UnifiedAccessGroupResource{
		client:                     &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()},
		keyVerificationMaxAttempts: 3,
	}
	response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
	diagnostic := fmt.Sprint(response.Diagnostics)
	if !response.Diagnostics.HasError() || listCalls.Load() != 4 {
		t.Fatalf("empty recovery exhaustion: list calls=%d diagnostics=%v", listCalls.Load(), response.Diagnostics)
	}
	for _, required := range []string{"Recovery Exhausted", "Inspect LiteLLM", "import", "not orphaned or duplicated"} {
		if !strings.Contains(diagnostic, required) {
			t.Fatalf("exhaustion diagnostic omitted %q: %s", required, diagnostic)
		}
	}
	var state UnifiedAccessGroupResourceModel
	if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
		t.Fatalf("decode exhausted recovery state: %v", diagnostics)
	}
	if !state.AccessGroupID.IsUnknown() {
		t.Fatalf("empty recovery invented an identity: %#v", state.AccessGroupID)
	}
}

func TestUnifiedAccessGroupCreateNeverSilentlyAdoptsPreexistingOrAmbiguousGroups(t *testing.T) {
	t.Parallel()

	t.Run("preexisting", func(t *testing.T) {
		mutations := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.Method == http.MethodGet && request.URL.Path == "/v1/access_group" {
				_ = json.NewEncoder(writer).Encode([]interface{}{unifiedAccessGroupAPIResponse("existing", "same", []string{})})
				return
			}
			mutations++
			http.NotFound(writer, request)
		}))
		defer server.Close()
		schema := unifiedAccessGroupTestSchema(t)
		plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("same", unifiedAccessGroupStringList()))
		resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}, keyVerificationMaxAttempts: 1}
		response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
		resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
		if !response.Diagnostics.HasError() || mutations != 0 || !strings.Contains(fmt.Sprint(response.Diagnostics), "Already Exists") {
			t.Fatalf("preexisting group was not rejected: mutations=%d diagnostics=%v", mutations, response.Diagnostics)
		}
	})

	t.Run("ambiguous postflight", func(t *testing.T) {
		committed := false
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch {
			case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group":
				if !committed {
					_, _ = writer.Write([]byte(`[]`))
					return
				}
				_ = json.NewEncoder(writer).Encode([]interface{}{
					unifiedAccessGroupAPIResponse("candidate-a", "same", []string{}),
					unifiedAccessGroupAPIResponse("candidate-b", "same", []string{}),
				})
			case request.Method == http.MethodPost:
				committed = true
				_, _ = writer.Write([]byte(`{"accepted":`))
			default:
				http.NotFound(writer, request)
			}
		}))
		defer server.Close()
		schema := unifiedAccessGroupTestSchema(t)
		plan := unifiedAccessGroupTestPlan(t, schema, unifiedAccessGroupPlanModel("same", unifiedAccessGroupStringList()))
		resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}, keyVerificationMaxAttempts: 1}
		response := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
		resourceUnderTest.Create(context.Background(), resource.CreateRequest{Plan: plan}, response)
		if !response.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(response.Diagnostics), "did not adopt any candidate") {
			t.Fatalf("ambiguous postflight was not rejected: %v", response.Diagnostics)
		}
		var state UnifiedAccessGroupResourceModel
		if diagnostics := response.State.Get(context.Background(), &state); diagnostics.HasError() {
			t.Fatalf("decode unchanged plan state: %v", diagnostics)
		}
		if !state.AccessGroupID.IsUnknown() {
			t.Fatalf("ambiguous candidate identity was adopted: %#v", state.AccessGroupID)
		}
	})
}

func TestUnifiedAccessGroupRepairsEveryOneSidedDesiredDirection(t *testing.T) {
	hash := strings.Repeat("5", 64)
	const groupID = "ag-delta"

	for _, test := range []struct {
		name           string
		initialGroup   bool
		initialKey     bool
		desired        bool
		wantGroupPuts  int
		wantKeyUpdates int
	}{
		{name: "group-only desired attach", initialGroup: true, desired: true, wantGroupPuts: 2},
		{name: "key-only desired attach", initialKey: true, desired: true, wantGroupPuts: 1},
		{name: "key-only desired detach", initialKey: true, desired: false, wantGroupPuts: 1, wantKeyUpdates: 1},
		{name: "group-only desired detach", initialGroup: true, desired: false, wantGroupPuts: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			groupAssignments := map[string]bool{}
			if test.initialGroup {
				groupAssignments[hash] = true
			}
			keyGroups := map[string][]string{hash: {"unrelated-a", "unrelated-b"}}
			if test.initialKey {
				keyGroups[hash] = []string{"unrelated-a", groupID, "unrelated-b"}
			}
			groupPuts := 0
			keyUpdates := 0
			putIncludedTarget := false

			groupHashes := func() []string {
				result := make([]string, 0, len(groupAssignments))
				for assignedHash := range groupAssignments {
					result = append(result, assignedHash)
				}
				sort.Strings(result)
				return result
			}
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch {
				case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group/"+groupID:
					_ = json.NewEncoder(writer).Encode(unifiedAccessGroupAPIResponse(groupID, "delta", groupHashes()))
				case request.Method == http.MethodPut && request.URL.Path == "/v1/access_group/"+groupID:
					groupPuts++
					var body map[string]interface{}
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Fatalf("decode access-group update: %v", err)
					}
					assigned, ok := body["assigned_key_ids"].([]interface{})
					if !ok {
						t.Fatalf("assigned_key_ids request = %#v", body)
					}
					newAssignments := make(map[string]bool, len(assigned))
					for _, value := range assigned {
						assignedHash := value.(string)
						newAssignments[assignedHash] = true
						if assignedHash == hash {
							putIncludedTarget = true
						}
					}
					// Reproduce v1.98 exactly: key rows change only for a delta
					// against the access-group row, not from desired truth.
					for oldHash := range groupAssignments {
						if !newAssignments[oldHash] {
							keyGroups[oldHash] = removeExactString(keyGroups[oldHash], groupID)
						}
					}
					for newHash := range newAssignments {
						if !groupAssignments[newHash] && !containsExactString(keyGroups[newHash], groupID) {
							keyGroups[newHash] = append(keyGroups[newHash], groupID)
						}
					}
					groupAssignments = newAssignments
					_ = json.NewEncoder(writer).Encode(unifiedAccessGroupAPIResponse(groupID, "delta", groupHashes()))
				case request.Method == http.MethodPost && request.URL.Path == "/key/update":
					keyUpdates++
					var body struct {
						Key            string   `json:"key"`
						AccessGroupIDs []string `json:"access_group_ids"`
					}
					if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
						t.Fatalf("decode key update: %v", err)
					}
					if body.Key != hash || containsExactString(body.AccessGroupIDs, groupID) {
						t.Fatalf("unsafe key detach request: %#v", body)
					}
					keyGroups[hash] = append([]string(nil), body.AccessGroupIDs...)
					writer.WriteHeader(http.StatusOK)
				case request.Method == http.MethodGet && request.URL.Path == "/key/info":
					requestedHash := request.URL.Query().Get("key")
					groups, exists := keyGroups[requestedHash]
					if !exists {
						http.NotFound(writer, request)
						return
					}
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": requestedHash, "info": map[string]interface{}{"access_group_ids": groups}})
				case request.Method == http.MethodGet && request.URL.Path == "/key/list":
					hashes := []string{}
					for listedHash, groups := range keyGroups {
						if containsExactString(groups, groupID) {
							hashes = append(hashes, listedHash)
						}
					}
					sort.Strings(hashes)
					if len(hashes) == 0 {
						writeEmptyUnifiedAccessGroupKeyPage(writer)
					} else {
						writeUnifiedAccessGroupKeyPage(writer, 1, 1, hashes...)
					}
				default:
					http.NotFound(writer, request)
				}
			}))
			defer server.Close()

			schema := unifiedAccessGroupTestSchema(t)
			priorAssigned := unifiedAccessGroupStringList()
			if !test.desired {
				priorAssigned = unifiedAccessGroupStringList("sha256:" + hash)
			}
			priorModel := unifiedAccessGroupPlanModel("delta", priorAssigned)
			priorModel.ID = types.StringValue(groupID)
			priorModel.AccessGroupID = types.StringValue(groupID)
			priorState := unifiedAccessGroupTestState(t, schema, priorModel)
			planModel := priorModel
			if test.desired {
				planModel.AssignedKeyIDs = unifiedAccessGroupStringList("sha256:" + hash)
			} else {
				planModel.AssignedKeyIDs = unifiedAccessGroupStringList()
			}
			plan := unifiedAccessGroupTestPlan(t, schema, planModel)
			resourceUnderTest := &UnifiedAccessGroupResource{
				client:                      &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()},
				keyVerificationMaxAttempts:  2,
				keyVerificationInitialDelay: 0,
			}
			response := &resource.UpdateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
			resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: priorState}, response)
			if response.Diagnostics.HasError() {
				t.Fatalf("one-sided repair diagnostics: %v", response.Diagnostics)
			}
			if groupPuts != test.wantGroupPuts || keyUpdates != test.wantKeyUpdates {
				t.Fatalf("mutations: group puts=%d key updates=%d, want %d/%d", groupPuts, keyUpdates, test.wantGroupPuts, test.wantKeyUpdates)
			}
			if got := groupAssignments[hash]; got != test.desired {
				t.Fatalf("group row attached=%t, want %t", got, test.desired)
			}
			if got := containsExactString(keyGroups[hash], groupID); got != test.desired {
				t.Fatalf("key row attached=%t, want %t", got, test.desired)
			}
			if !containsExactString(keyGroups[hash], "unrelated-a") || !containsExactString(keyGroups[hash], "unrelated-b") {
				t.Fatalf("unrelated key groups were dropped: %#v", keyGroups[hash])
			}
			if test.name == "key-only desired detach" && putIncludedTarget {
				t.Fatal("key-only detach temporarily added the access-group side")
			}
		})
	}
}

func TestUnifiedAccessGroupGroupOnlyAttachStopsAtConfirmedPartialReset(t *testing.T) {
	hash := strings.Repeat("a", 64)
	const groupID = "ag-partial-reset"
	groupAttached := true
	var putCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group/"+groupID:
			assigned := []string{}
			if groupAttached {
				assigned = []string{hash}
			}
			_ = json.NewEncoder(writer).Encode(unifiedAccessGroupAPIResponse(groupID, "partial-reset", assigned))
		case request.Method == http.MethodPut && request.URL.Path == "/v1/access_group/"+groupID:
			if putCalls.Add(1) != 1 {
				t.Fatal("provider continued to re-add after an unconfirmed reset")
			}
			groupAttached = false
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte(`{"access_group_id":`))
		case request.Method == http.MethodGet && request.URL.Path == "/key/list":
			writeEmptyUnifiedAccessGroupKeyPage(writer)
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"key":  hash,
				"info": map[string]interface{}{"access_group_ids": []string{}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := unifiedAccessGroupTestSchema(t)
	priorModel := unifiedAccessGroupPlanModel("partial-reset", unifiedAccessGroupStringList())
	priorModel.ID = types.StringValue(groupID)
	priorModel.AccessGroupID = types.StringValue(groupID)
	priorState := unifiedAccessGroupTestState(t, schema, priorModel)
	planModel := priorModel
	planModel.AssignedKeyIDs = unifiedAccessGroupStringList("sha256:" + hash)
	plan := unifiedAccessGroupTestPlan(t, schema, planModel)
	resourceUnderTest := &UnifiedAccessGroupResource{
		client:                     &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()},
		keyVerificationMaxAttempts: 1,
	}
	response := &resource.UpdateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: priorState}, response)
	if !response.Diagnostics.HasError() || putCalls.Load() != 1 {
		t.Fatalf("partial reset outcome: puts=%d diagnostics=%v", putCalls.Load(), response.Diagnostics)
	}
	if !strings.Contains(fmt.Sprint(response.Diagnostics), "stopped before re-adding") {
		t.Fatalf("partial reset diagnostic was not actionable: %v", response.Diagnostics)
	}
	var observed UnifiedAccessGroupResourceModel
	if diagnostics := response.State.Get(context.Background(), &observed); diagnostics.HasError() {
		t.Fatalf("decode partial-reset state: %v", diagnostics)
	}
	if observed.AccessGroupID.ValueString() != groupID || len(unifiedAccessGroupListStrings(t, observed.AssignedKeyIDs)) != 0 {
		t.Fatalf("partial reset state was not the confirmed intersection: %#v", observed)
	}
}

func TestUnifiedAccessGroupKeyOnlyDetachNeverUsesAuthorizationBroadeningFallback(t *testing.T) {
	hash := strings.Repeat("6", 64)
	const groupID = "ag-no-fallback"
	var keyUpdateCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group/"+groupID:
			_ = json.NewEncoder(writer).Encode(unifiedAccessGroupAPIResponse(groupID, "no-fallback", []string{}))
		case request.Method == http.MethodPut && request.URL.Path == "/v1/access_group/"+groupID:
			var body map[string]interface{}
			_ = json.NewDecoder(request.Body).Decode(&body)
			assigned, ok := body["assigned_key_ids"].([]interface{})
			if !ok || len(assigned) != 0 {
				t.Fatalf("key-only detach broadened the group row: %#v", body)
			}
			_ = json.NewEncoder(writer).Encode(unifiedAccessGroupAPIResponse(groupID, "no-fallback", []string{}))
		case request.Method == http.MethodPost && request.URL.Path == "/key/update":
			keyUpdateCalls.Add(1)
			http.Error(writer, `{"detail":"hash identity unsupported"}`, http.StatusUnprocessableEntity)
		case request.Method == http.MethodGet && request.URL.Path == "/key/list":
			writeUnifiedAccessGroupKeyPage(writer, 1, 1, hash)
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"key":  hash,
				"info": map[string]interface{}{"access_group_ids": []string{"unrelated", groupID}},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := unifiedAccessGroupTestSchema(t)
	priorModel := unifiedAccessGroupPlanModel("no-fallback", unifiedAccessGroupStringList("sha256:"+hash))
	priorModel.ID = types.StringValue(groupID)
	priorModel.AccessGroupID = types.StringValue(groupID)
	priorState := unifiedAccessGroupTestState(t, schema, priorModel)
	planModel := priorModel
	planModel.AssignedKeyIDs = unifiedAccessGroupStringList()
	plan := unifiedAccessGroupTestPlan(t, schema, planModel)
	resourceUnderTest := &UnifiedAccessGroupResource{
		client:                     &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()},
		keyVerificationMaxAttempts: 1,
	}
	response := &resource.UpdateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	resourceUnderTest.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: priorState}, response)
	if !response.Diagnostics.HasError() || keyUpdateCalls.Load() != 1 {
		t.Fatalf("unsafe detach fallback result: calls=%d diagnostics=%v", keyUpdateCalls.Load(), response.Diagnostics)
	}
	diagnostic := fmt.Sprint(response.Diagnostics)
	for _, required := range []string{"did not temporarily add", "broaden authorization", "intersection confirmed by both"} {
		if !strings.Contains(diagnostic, required) {
			t.Fatalf("safe fallback diagnostic omitted %q: %s", required, diagnostic)
		}
	}
	var observed UnifiedAccessGroupResourceModel
	if diagnostics := response.State.Get(context.Background(), &observed); diagnostics.HasError() {
		t.Fatalf("decode unsupported detach state: %v", diagnostics)
	}
	if got := unifiedAccessGroupListStrings(t, observed.AssignedKeyIDs); len(got) != 0 {
		t.Fatalf("unsupported one-sided detach was imported as converged: %#v", got)
	}
}

func TestUnifiedAccessGroupGlobalOneSidedDriftAndImportUsePaginatedFullObjectDiscovery(t *testing.T) {
	t.Parallel()

	removed := strings.Repeat("7", 64)
	added := strings.Repeat("8", 64)
	var mutex sync.Mutex
	requestedPages := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/v1/access_group/ag-global":
			// Import and refresh both see opposite one-sided directions: removed
			// exists only on the group row, while added/other exist only on keys.
			_ = json.NewEncoder(writer).Encode(unifiedAccessGroupAPIResponse("ag-global", "global", []string{removed}))
		case request.Method == http.MethodGet && request.URL.Path == "/key/list":
			query := request.URL.Query()
			if query.Get("access_group_id") != "ag-global" || query.Get("return_full_object") != "true" || query.Get("size") != "100" || query.Get("sort_by") != "token" || query.Get("sort_order") != "asc" {
				t.Errorf("unexpected global discovery query: %q", request.URL.RawQuery)
			}
			mutex.Lock()
			requestedPages = append(requestedPages, query.Get("page"))
			mutex.Unlock()
			page := query.Get("page")
			if page == "1" {
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{
					"keys":         []interface{}{map[string]interface{}{"token": added, "key_name": "raw-suffix-never-use"}},
					"total_count":  2,
					"current_page": 1,
					"total_pages":  2,
				})
			} else {
				other := strings.Repeat("9", 64)
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{
					"keys":         []interface{}{map[string]interface{}{"token": other}},
					"total_count":  2,
					"current_page": 2,
					"total_pages":  2,
				})
			}
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			hash := request.URL.Query().Get("key")
			groups := []string{}
			if hash != removed {
				groups = []string{"ag-global"}
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": hash, "info": map[string]interface{}{"access_group_ids": groups}})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := unifiedAccessGroupTestSchema(t)
	stateModel := unifiedAccessGroupPlanModel("global", unifiedAccessGroupStringList("sha256:"+removed, "sha256:"+removed))
	stateModel.ID = types.StringValue("ag-global")
	stateModel.AccessGroupID = types.StringValue("ag-global")
	state := unifiedAccessGroupTestState(t, schema, stateModel)
	resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}, keyVerificationMaxAttempts: 2}
	response := &resource.ReadResponse{State: state}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: state}, response)
	if !response.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(response.Diagnostics), "one-sided assignment") {
		t.Fatalf("global one-sided drift was not reported: %v", response.Diagnostics)
	}
	var drifted UnifiedAccessGroupResourceModel
	if diagnostics := response.State.Get(context.Background(), &drifted); diagnostics.HasError() {
		t.Fatalf("decode drift state: %v", diagnostics)
	}
	if got := unifiedAccessGroupListStrings(t, drifted.AssignedKeyIDs); len(got) != 0 {
		t.Fatalf("global one-sided drift = %#v, want empty two-sided intersection", got)
	}

	importModel := unifiedAccessGroupPlanModel("", types.ListNull(types.StringType))
	importModel.ID = types.StringNull()
	importModel.AccessGroupID = types.StringNull()
	importModel.AccessGroupName = types.StringNull()
	importState := unifiedAccessGroupTestState(t, schema, importModel)
	importResponse := &resource.ImportStateResponse{State: importState}
	resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: "ag-global"}, importResponse)
	if importResponse.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", importResponse.Diagnostics)
	}
	importRead := &resource.ReadResponse{State: importResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: importResponse.State}, importRead)
	if !importRead.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(importRead.Diagnostics), "one-sided assignment") {
		t.Fatalf("import one-sided drift was not reported: %v", importRead.Diagnostics)
	}
	var imported UnifiedAccessGroupResourceModel
	if diagnostics := importRead.State.Get(context.Background(), &imported); diagnostics.HasError() {
		t.Fatalf("decode import state: %v", diagnostics)
	}
	if got := unifiedAccessGroupListStrings(t, imported.AssignedKeyIDs); len(got) != 0 {
		t.Fatalf("imported one-sided assignments = %#v, want empty two-sided intersection", got)
	}

	mutex.Lock()
	defer mutex.Unlock()
	if got, want := strings.Join(requestedPages, ","), "1,2,1,2"; got != want {
		t.Fatalf("global discovery pages = %s, want %s", got, want)
	}
}

func TestUnifiedAccessGroupImportEscapesSpecialAccessGroupID(t *testing.T) {
	t.Parallel()

	const id = "group/special value"
	var escapedPath string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/v1/access_group/"):
			escapedPath = request.URL.EscapedPath()
			_ = json.NewEncoder(writer).Encode(unifiedAccessGroupAPIResponse(id, "special", []string{}))
		case request.Method == http.MethodGet && request.URL.Path == "/key/list":
			if request.URL.Query().Get("access_group_id") != id {
				t.Errorf("special access_group_id query = %q", request.URL.RawQuery)
			}
			writeEmptyUnifiedAccessGroupKeyPage(writer)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := unifiedAccessGroupTestSchema(t)
	model := unifiedAccessGroupPlanModel("", types.ListNull(types.StringType))
	model.ID = types.StringNull()
	model.AccessGroupID = types.StringNull()
	model.AccessGroupName = types.StringNull()
	state := unifiedAccessGroupTestState(t, schema, model)
	resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}, keyVerificationMaxAttempts: 1}
	importResponse := &resource.ImportStateResponse{State: state}
	resourceUnderTest.ImportState(context.Background(), resource.ImportStateRequest{ID: id}, importResponse)
	readResponse := &resource.ReadResponse{State: importResponse.State}
	resourceUnderTest.Read(context.Background(), resource.ReadRequest{State: importResponse.State}, readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("special import read diagnostics: %v", readResponse.Diagnostics)
	}
	if escapedPath != "/v1/access_group/group%2Fspecial%20value" {
		t.Fatalf("escaped access-group path = %q", escapedPath)
	}
}

func TestUnifiedAccessGroupVerificationRetriesOnlyBoundedTransientClasses(t *testing.T) {
	hash := strings.Repeat("a", 64)

	for _, status := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway} {
		status := status
		t.Run(fmt.Sprintf("retry-%d", status), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests++
				if requests == 1 {
					http.Error(writer, `{"detail":"transient"}`, status)
					return
				}
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": hash, "info": map[string]interface{}{"access_group_ids": []string{}}})
			}))
			defer server.Close()
			resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}, keyVerificationMaxAttempts: 3}
			if _, err := resourceUnderTest.readUnifiedAccessGroupKeyMembershipWithRetry(context.Background(), hash); err != nil {
				t.Fatalf("transient HTTP %d was not recovered: %v", status, err)
			}
			if requests != 2 {
				t.Fatalf("HTTP %d requests = %d, want 2", status, requests)
			}
		})
	}

	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		status := status
		t.Run(fmt.Sprintf("no-retry-%d", status), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests++
				http.Error(writer, `{"detail":"permanent"}`, status)
			}))
			defer server.Close()
			resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}, keyVerificationMaxAttempts: 3}
			if _, err := resourceUnderTest.readUnifiedAccessGroupKeyMembershipWithRetry(context.Background(), hash); err == nil {
				t.Fatalf("HTTP %d unexpectedly succeeded", status)
			}
			if requests != 1 {
				t.Fatalf("HTTP %d requests = %d, want no retry", status, requests)
			}
		})
	}

	t.Run("safe transport", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": hash, "info": map[string]interface{}{"access_group_ids": []string{}}})
		}))
		defer server.Close()
		transport := &failOnceRoundTripper{base: server.Client().Transport}
		resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: &http.Client{Transport: transport}}, keyVerificationMaxAttempts: 3}
		if _, err := resourceUnderTest.readUnifiedAccessGroupKeyMembershipWithRetry(context.Background(), hash); err != nil {
			t.Fatalf("safe transport error was not retried: %v", err)
		}
		if transport.calls.Load() != 2 {
			t.Fatalf("transport calls = %d, want 2", transport.calls.Load())
		}
	})

	t.Run("terminal transport", func(t *testing.T) {
		transport := &terminalRoundTripper{}
		resourceUnderTest := &UnifiedAccessGroupResource{
			client:                     &Client{APIBase: "http://terminal.invalid", APIKey: "test", HTTPClient: &http.Client{Transport: transport}},
			keyVerificationMaxAttempts: 3,
		}
		if _, err := resourceUnderTest.readUnifiedAccessGroupKeyMembershipWithRetry(context.Background(), hash); err == nil {
			t.Fatal("terminal transport error unexpectedly succeeded")
		}
		if transport.calls.Load() != 1 {
			t.Fatalf("terminal transport calls = %d, want no retry", transport.calls.Load())
		}
	})

	t.Run("safe transport categories", func(t *testing.T) {
		if shouldRetryUnifiedAccessGroupVerification(safeTransportFailure(x509.HostnameError{Host: "wrong-host"})) {
			t.Fatal("TLS hostname failure was classified transient")
		}
		if shouldRetryUnifiedAccessGroupVerification(safeTransportFailure(errors.New("invalid proxy configuration"))) {
			t.Fatal("generic configuration failure was classified transient")
		}
		if shouldRetryUnifiedAccessGroupVerification(safeTransportFailure(context.Canceled)) {
			t.Fatal("cancellation was classified transient")
		}
		if !shouldRetryUnifiedAccessGroupVerification(safeTransportFailure(context.DeadlineExceeded)) {
			t.Fatal("timeout was not classified transient")
		}
	})

	t.Run("propagation mismatch", func(t *testing.T) {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			requests++
			groups := []string{}
			if requests == 2 {
				groups = []string{"ag"}
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{"key": hash, "info": map[string]interface{}{"access_group_ids": groups}})
		}))
		defer server.Close()
		resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}, keyVerificationMaxAttempts: 3}
		attached, observed, err := resourceUnderTest.verifyUnifiedAccessGroupMembership(context.Background(), hash, "ag", true)
		if err != nil || !attached || !observed || requests != 2 {
			t.Fatalf("propagation mismatch retry = attached:%t observed:%t requests:%d err:%v", attached, observed, requests, err)
		}
	})

	t.Run("malformed contract", func(t *testing.T) {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			requests++
			_, _ = writer.Write([]byte(`{"info":{"access_group_ids":[]}}`))
		}))
		defer server.Close()
		resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}, keyVerificationMaxAttempts: 3}
		if _, err := resourceUnderTest.readUnifiedAccessGroupKeyMembershipWithRetry(context.Background(), hash); !errors.Is(err, errUnifiedAccessGroupKeyInfoContract) {
			t.Fatalf("malformed contract error = %v", err)
		}
		if requests != 1 {
			t.Fatalf("malformed contract requests = %d, want no retry", requests)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		resourceUnderTest := &UnifiedAccessGroupResource{keyVerificationMaxAttempts: 3}
		if _, err := resourceUnderTest.readUnifiedAccessGroupKeyMembershipWithRetry(ctx, hash); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled context error = %v", err)
		}
	})
}

type terminalRoundTripper struct {
	calls atomic.Int32
}

func (r *terminalRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	r.calls.Add(1)
	return nil, errors.New("permanent transport configuration failure")
}

type failOnceRoundTripper struct {
	base  http.RoundTripper
	calls atomic.Int32
}

func (r *failOnceRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if r.calls.Add(1) == 1 {
		return nil, temporaryNetworkError{}
	}
	return r.base.RoundTrip(request)
}

type temporaryNetworkError struct{}

func (temporaryNetworkError) Error() string   { return "temporary network failure" }
func (temporaryNetworkError) Timeout() bool   { return false }
func (temporaryNetworkError) Temporary() bool { return true }

type timeoutNetworkError struct{}

func (timeoutNetworkError) Error() string   { return "network timeout" }
func (timeoutNetworkError) Timeout() bool   { return true }
func (timeoutNetworkError) Temporary() bool { return true }

func TestUnifiedAccessGroupKeyInfoRequiresMatchingTopLevelEcho(t *testing.T) {
	t.Parallel()

	requested := strings.Repeat("b", 64)
	mismatched := strings.Repeat("c", 64)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests++
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"key":  "SHA256:" + strings.ToUpper(mismatched),
			"info": map[string]interface{}{"access_group_ids": []string{"ag"}},
		})
	}))
	defer server.Close()
	resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}, keyVerificationMaxAttempts: 3}
	if _, err := resourceUnderTest.readUnifiedAccessGroupKeyMembershipWithRetry(context.Background(), requested); !errors.Is(err, errUnifiedAccessGroupKeyInfoContract) {
		t.Fatalf("mismatched key echo error = %v", err)
	}
	if requests != 1 {
		t.Fatalf("mismatched echo requests = %d, want no retry", requests)
	}
}

func TestUnifiedAccessGroupMissingPriorKeySurfacesAuthoritativeRemoval(t *testing.T) {
	t.Parallel()

	hash := strings.Repeat("e", 64)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/key/list":
			writeEmptyUnifiedAccessGroupKeyPage(writer)
		case "/key/info":
			http.Error(writer, `{"detail":"not found"}`, http.StatusNotFound)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	resourceUnderTest := &UnifiedAccessGroupResource{client: &Client{APIBase: server.URL, APIKey: "test", HTTPClient: server.Client()}, keyVerificationMaxAttempts: 3}
	state, err := resourceUnderTest.synchronizeUnifiedAccessGroupKeys(
		context.Background(),
		"ag-missing",
		types.ListNull(types.StringType),
		unifiedAccessGroupStringList("sha256:"+hash, "sha256:"+hash),
		[]interface{}{hash},
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "intersection confirmed by both") {
		t.Fatalf("missing prior one-sided group row was not reported: %v", err)
	}
	if got := unifiedAccessGroupListStrings(t, state); len(got) != 0 {
		t.Fatalf("missing prior key remained in state: %#v", got)
	}
}

func TestUnifiedAccessGroupPartialSynchronizationAndDataSourceLeakageFiltering(t *testing.T) {
	t.Parallel()

	valid := strings.Repeat("d", 64)
	value := types.ListUnknown(types.StringType)
	setSafeAssignedKeyListFromResponse(&value, []interface{}{
		"sha256:" + strings.ToUpper(valid),
		valid,
		"sk-never-publish#raw-suffix",
		"malformed",
		float64(1),
	})
	if got, want := unifiedAccessGroupListStrings(t, value), []string{"sha256:" + strings.ToUpper(valid), valid}; !reflect.DeepEqual(got, want) {
		t.Fatalf("data-source filtered assignments = %#v, want only hash representations %#v", got, want)
	}
	if strings.Contains(strings.Join(unifiedAccessGroupListStrings(t, value), ","), "suffix") {
		t.Fatal("data-source state published a suffix identifier")
	}
}
