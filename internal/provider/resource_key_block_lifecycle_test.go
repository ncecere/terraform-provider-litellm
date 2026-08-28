package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func keyBlockTestSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	(&KeyBlockResource{}).Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func keyBlockTestState(t *testing.T, schema resourceschema.Schema, data KeyBlockResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := state.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set test state: %v", diagnostics)
	}
	return state
}

func keyBlockTestPlan(t *testing.T, schema resourceschema.Schema, data KeyBlockResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Raw: tftypes.NewValue(schema.Type().TerraformType(context.Background()), nil), Schema: schema}
	if diagnostics := plan.Set(context.Background(), &data); diagnostics.HasError() {
		t.Fatalf("set test plan: %v", diagnostics)
	}
	return plan
}

func TestKeyBlockModifyPlanComparesCanonicalIdentity(t *testing.T) {
	t.Parallel()
	schema := keyBlockTestSchema(t)
	raw := "sk-plan-identity"
	id := hashKeyForID(raw)
	state := keyBlockTestState(t, schema, KeyBlockResourceModel{
		ID: types.StringValue(id), Key: types.StringValue(raw), KeyHash: types.StringNull(), Blocked: types.BoolValue(true),
	})
	tests := []struct {
		name        string
		plan        KeyBlockResourceModel
		wantReplace bool
		wantError   bool
	}{
		{
			name: "same identity representation switch",
			plan: KeyBlockResourceModel{ID: types.StringValue(id), Key: types.StringNull(), KeyHash: types.StringValue(id), Blocked: types.BoolValue(true)},
		},
		{
			name:        "different identity",
			plan:        KeyBlockResourceModel{ID: types.StringValue(id), Key: types.StringNull(), KeyHash: types.StringValue(hashKeyForID("sk-other")), Blocked: types.BoolValue(true)},
			wantReplace: true,
		},
		{
			name:      "unknown identity",
			plan:      KeyBlockResourceModel{ID: types.StringValue(id), Key: types.StringNull(), KeyHash: types.StringUnknown(), Blocked: types.BoolValue(true)},
			wantError: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := keyBlockTestPlan(t, schema, test.plan)
			response := &resource.ModifyPlanResponse{Plan: plan}
			(&KeyBlockResource{}).ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: state, Plan: plan}, response)
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatalf("diagnostics = %v, want error %t", response.Diagnostics, test.wantError)
			}
			if got := len(response.RequiresReplace) > 0; got != test.wantReplace {
				t.Fatalf("RequiresReplace = %v, want replacement %t", response.RequiresReplace, test.wantReplace)
			}
			var modified KeyBlockResourceModel
			if diagnostics := response.Plan.Get(context.Background(), &modified); diagnostics.HasError() {
				t.Fatalf("decode modified plan: %v", diagnostics)
			}
			if test.wantReplace && !modified.ID.IsUnknown() {
				t.Fatalf("replacement ID = %#v, want unknown", modified.ID)
			}
			if !test.wantReplace && !test.wantError && modified.ID.ValueString() != id {
				t.Fatalf("in-place ID = %#v, want existing ID", modified.ID)
			}
		})
	}
}

func TestKeyBlockLifecycleUsesOnlyBareHash(t *testing.T) {
	t.Parallel()
	raw := "sk-lifecycle-#&+%-secret"
	id := hashKeyForID(raw)
	bareHash := strings.TrimPrefix(id, "sha256:")
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls = append(calls, request.Method+" "+request.URL.Path)
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && (request.URL.Path == "/key/block" || request.URL.Path == "/key/unblock"):
			var body map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode request: %v", err)
			}
			if body["key"] != bareHash {
				t.Errorf("%s key = %#v, want bare hash", request.URL.Path, body["key"])
			}
			_, _ = writer.Write([]byte(`{}`))
		case request.Method == http.MethodGet && request.URL.Path == "/key/info":
			if request.URL.Query().Get("key") != bareHash {
				t.Errorf("read key = %q, want bare hash", request.URL.Query().Get("key"))
			}
			_, _ = fmt.Fprintf(writer, `{"key":%q,"info":{"blocked":true}}`, bareHash)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	schema := keyBlockTestSchema(t)
	plan := keyBlockTestPlan(t, schema, KeyBlockResourceModel{
		ID: types.StringUnknown(), Key: types.StringValue(raw), KeyHash: types.StringNull(), Blocked: types.BoolUnknown(),
	})
	keyBlock := &KeyBlockResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}
	createResponse := &resource.CreateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: schema}}
	keyBlock.Create(context.Background(), resource.CreateRequest{Plan: plan}, createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}
	var created KeyBlockResourceModel
	if diagnostics := createResponse.State.Get(context.Background(), &created); diagnostics.HasError() {
		t.Fatalf("read create state: %v", diagnostics)
	}
	if created.ID.ValueString() != id || !created.Blocked.ValueBool() || created.Key.ValueString() != raw {
		t.Fatalf("created state = %#v", created)
	}

	readResponse := &resource.ReadResponse{State: createResponse.State}
	keyBlock.Read(context.Background(), resource.ReadRequest{State: createResponse.State}, readResponse)
	if readResponse.Diagnostics.HasError() || readResponse.State.Raw.IsNull() {
		t.Fatalf("read response: diagnostics=%v null=%t", readResponse.Diagnostics, readResponse.State.Raw.IsNull())
	}
	deleteResponse := &resource.DeleteResponse{}
	keyBlock.Delete(context.Background(), resource.DeleteRequest{State: readResponse.State}, deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", deleteResponse.Diagnostics)
	}
	if got, want := strings.Join(calls, ","), "POST /key/block,GET /key/info,POST /key/unblock"; got != want {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestKeyBlockDoesNotTreatUnrelated404TextAsAbsence(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":"upstream on port 4040 is unavailable"}`))
	}))
	defer server.Close()

	schema := keyBlockTestSchema(t)
	id := "sha256:" + strings.Repeat("a", 64)
	state := keyBlockTestState(t, schema, KeyBlockResourceModel{
		ID: types.StringValue(id), Key: types.StringNull(), KeyHash: types.StringValue(id), Blocked: types.BoolValue(true),
	})
	keyBlock := &KeyBlockResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}
	readResponse := &resource.ReadResponse{State: state}
	keyBlock.Read(context.Background(), resource.ReadRequest{State: state}, readResponse)
	if !readResponse.Diagnostics.HasError() || readResponse.State.Raw.IsNull() {
		t.Fatalf("500 containing 404 must retain state with an error: diagnostics=%v null=%t", readResponse.Diagnostics, readResponse.State.Raw.IsNull())
	}
	deleteResponse := &resource.DeleteResponse{}
	keyBlock.Delete(context.Background(), resource.DeleteRequest{State: state}, deleteResponse)
	if !deleteResponse.Diagnostics.HasError() {
		t.Fatal("500 containing 404 must not be treated as successful deletion")
	}
}

func TestKeyBlockExact404IsIdempotentAbsence(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Error(writer, "missing", http.StatusNotFound)
	}))
	defer server.Close()

	schema := keyBlockTestSchema(t)
	id := "sha256:" + strings.Repeat("b", 64)
	state := keyBlockTestState(t, schema, KeyBlockResourceModel{
		ID: types.StringValue(id), Key: types.StringNull(), KeyHash: types.StringValue(id), Blocked: types.BoolValue(true),
	})
	keyBlock := &KeyBlockResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}}
	readResponse := &resource.ReadResponse{State: state}
	keyBlock.Read(context.Background(), resource.ReadRequest{State: state}, readResponse)
	if readResponse.Diagnostics.HasError() || !readResponse.State.Raw.IsNull() {
		t.Fatalf("exact 404 read: diagnostics=%v null=%t", readResponse.Diagnostics, readResponse.State.Raw.IsNull())
	}
	deleteResponse := &resource.DeleteResponse{}
	keyBlock.Delete(context.Background(), resource.DeleteRequest{State: state}, deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("exact 404 delete diagnostics: %v", deleteResponse.Diagnostics)
	}
}
