package provider

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func configuredKeyNumericModel(key string) KeyResourceModel {
	return KeyResourceModel{
		Key:                 types.StringValue(key),
		MaxBudget:           types.Float64Value(1),
		SoftBudget:          types.Float64Value(2),
		TPMLimit:            types.Int64Value(3),
		RPMLimit:            types.Int64Value(4),
		MaxParallelRequests: types.Int64Value(5),
		ModelMaxBudget: types.MapValueMust(types.Float64Type, map[string]attr.Value{
			"configured": types.Float64Value(6),
		}),
		ModelRPMLimit: types.MapValueMust(types.Int64Type, map[string]attr.Value{
			"configured": types.Int64Value(7),
		}),
		ModelTPMLimit: types.MapValueMust(types.Int64Type, map[string]attr.Value{
			"configured": types.Int64Value(8),
		}),
	}
}

func readKeyNumericResponse(t *testing.T, body string, data *KeyResourceModel, imported bool) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(body))
	}))
	defer server.Close()

	resource := &KeyResource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	if err := resource.readKeyWithNumericOwnership(context.Background(), data, imported); err != nil {
		t.Fatalf("read key numeric response: %v", err)
	}
}

func assertKeyNumericMaps(t *testing.T, data *KeyResourceModel, wantBudget map[string]float64, wantRPM, wantTPM map[string]int64) {
	t.Helper()
	var budget map[string]float64
	if diagnostics := data.ModelMaxBudget.ElementsAs(context.Background(), &budget, false); diagnostics.HasError() {
		t.Fatalf("decode model_max_budget: %v", diagnostics)
	}
	var rpm map[string]int64
	if diagnostics := data.ModelRPMLimit.ElementsAs(context.Background(), &rpm, false); diagnostics.HasError() {
		t.Fatalf("decode model_rpm_limit: %v", diagnostics)
	}
	var tpm map[string]int64
	if diagnostics := data.ModelTPMLimit.ElementsAs(context.Background(), &tpm, false); diagnostics.HasError() {
		t.Fatalf("decode model_tpm_limit: %v", diagnostics)
	}
	if len(budget) != len(wantBudget) || len(rpm) != len(wantRPM) || len(tpm) != len(wantTPM) {
		t.Fatalf("numeric map lengths = %#v, %#v, %#v", budget, rpm, tpm)
	}
	for key, want := range wantBudget {
		if budget[key] != want {
			t.Fatalf("model_max_budget[%q] = %v, want %v", key, budget[key], want)
		}
	}
	for key, want := range wantRPM {
		if rpm[key] != want {
			t.Fatalf("model_rpm_limit[%q] = %d, want %d", key, rpm[key], want)
		}
	}
	for key, want := range wantTPM {
		if tpm[key] != want {
			t.Fatalf("model_tpm_limit[%q] = %d, want %d", key, tpm[key], want)
		}
	}
}

func TestKeyNumericReadUsesV198BudgetRelationAndTopLevelLimits(t *testing.T) {
	t.Parallel()

	data := configuredKeyNumericModel("sk-live")
	readKeyNumericResponse(t, `{
		"key":"sk-live",
		"info":{
			"max_budget":999,
			"model_max_budget":{"legacy":999},
			"tpm_limit":9007199254740993,
			"rpm_limit":9223372036854775807,
			"max_parallel_requests":9007199254740997,
			"litellm_budget_table":{
				"max_budget":100.25,
				"soft_budget":80,
				"tpm_limit":1,
				"rpm_limit":2,
				"max_parallel_requests":3,
				"model_max_budget":{"gpt-4o":12.5}
			},
			"metadata":{
				"model_rpm_limit":{"gpt-4o":9007199254740995},
				"model_tpm_limit":{"gpt-4o":9223372036854775806}
			}
		}
	}`, &data, false)

	if data.MaxBudget.ValueFloat64() != 100.25 || data.SoftBudget.ValueFloat64() != 80 {
		t.Fatalf("nested budgets = %v, %v", data.MaxBudget.ValueFloat64(), data.SoftBudget.ValueFloat64())
	}
	if data.TPMLimit.ValueInt64() != 9007199254740993 || data.RPMLimit.ValueInt64() != math.MaxInt64 || data.MaxParallelRequests.ValueInt64() != 9007199254740997 {
		t.Fatalf("top-level limits = %d, %d, %d", data.TPMLimit.ValueInt64(), data.RPMLimit.ValueInt64(), data.MaxParallelRequests.ValueInt64())
	}
	assertKeyNumericMaps(t, &data,
		map[string]float64{"gpt-4o": 12.5},
		map[string]int64{"gpt-4o": 9007199254740995},
		map[string]int64{"gpt-4o": 9223372036854775806},
	)
}

func TestKeyNumericHistoricalFlatBudgetFallback(t *testing.T) {
	t.Parallel()

	data := configuredKeyNumericModel("sk-flat")
	readKeyNumericResponse(t, `{
		"key":"sk-flat",
		"info":{
			"max_budget":25.5,
			"soft_budget":20,
			"model_max_budget":{"legacy":2.5},
			"litellm_budget_table":null
		}
	}`, &data, false)

	if data.MaxBudget.ValueFloat64() != 25.5 || data.SoftBudget.ValueFloat64() != 20 {
		t.Fatalf("flat budget fallback = %v, %v", data.MaxBudget.ValueFloat64(), data.SoftBudget.ValueFloat64())
	}
	var modelBudget map[string]float64
	if diagnostics := data.ModelMaxBudget.ElementsAs(context.Background(), &modelBudget, false); diagnostics.HasError() {
		t.Fatalf("decode flat model_max_budget: %v", diagnostics)
	}
	if len(modelBudget) != 1 || modelBudget["legacy"] != 2.5 {
		t.Fatalf("flat model_max_budget fallback = %#v", modelBudget)
	}
}

func TestKeyNumericConfiguredOmissionPreservesLastKnownState(t *testing.T) {
	t.Parallel()

	data := configuredKeyNumericModel("sk-configured")
	want := data
	readKeyNumericResponse(t, `{"key":"sk-configured","info":{"litellm_budget_table":{},"metadata":{}}}`, &data, false)

	if data.MaxBudget != want.MaxBudget || data.SoftBudget != want.SoftBudget || data.TPMLimit != want.TPMLimit || data.RPMLimit != want.RPMLimit || data.MaxParallelRequests != want.MaxParallelRequests {
		t.Fatalf("configured scalar omission changed state: %#v", data)
	}
	if !data.ModelMaxBudget.Equal(want.ModelMaxBudget) || !data.ModelRPMLimit.Equal(want.ModelRPMLimit) || !data.ModelTPMLimit.Equal(want.ModelTPMLimit) {
		t.Fatalf("configured map omission changed state: %#v, %#v, %#v", data.ModelMaxBudget, data.ModelRPMLimit, data.ModelTPMLimit)
	}
}

func TestKeyNumericExplicitNullClearsConfiguredState(t *testing.T) {
	t.Parallel()

	data := configuredKeyNumericModel("sk-null")
	readKeyNumericResponse(t, `{
		"key":"sk-null",
		"info":{
			"max_budget":999,
			"soft_budget":999,
			"model_max_budget":{"legacy":999},
			"tpm_limit":null,
			"rpm_limit":null,
			"max_parallel_requests":null,
			"litellm_budget_table":{"max_budget":null,"soft_budget":null,"model_max_budget":null},
			"metadata":{"model_rpm_limit":null,"model_tpm_limit":null}
		}
	}`, &data, false)

	if !data.MaxBudget.IsNull() || !data.SoftBudget.IsNull() || !data.TPMLimit.IsNull() || !data.RPMLimit.IsNull() || !data.MaxParallelRequests.IsNull() {
		t.Fatalf("explicit scalar null did not clear state: %#v", data)
	}
	if !data.ModelMaxBudget.IsNull() || !data.ModelRPMLimit.IsNull() || !data.ModelTPMLimit.IsNull() {
		t.Fatalf("explicit map null did not clear state: %#v, %#v, %#v", data.ModelMaxBudget, data.ModelRPMLimit, data.ModelTPMLimit)
	}
}

func TestKeyNumericUnconfiguredFieldsDoNotAdoptRemoteValues(t *testing.T) {
	t.Parallel()

	data := KeyResourceModel{
		Key:                 types.StringValue("sk-unconfigured"),
		MaxBudget:           types.Float64Unknown(),
		SoftBudget:          types.Float64Null(),
		TPMLimit:            types.Int64Unknown(),
		RPMLimit:            types.Int64Null(),
		MaxParallelRequests: types.Int64Unknown(),
		ModelMaxBudget:      types.MapUnknown(types.Float64Type),
		ModelRPMLimit:       types.MapNull(types.Int64Type),
		ModelTPMLimit:       types.MapUnknown(types.Int64Type),
	}
	readKeyNumericResponse(t, `{
		"key":"sk-unconfigured",
		"info":{
			"tpm_limit":9007199254740993,
			"rpm_limit":10,
			"max_parallel_requests":11,
			"litellm_budget_table":{"max_budget":100,"soft_budget":80,"model_max_budget":{"gpt-4o":12.5}},
			"metadata":{"model_rpm_limit":{"gpt-4o":9007199254740995},"model_tpm_limit":{"gpt-4o":9007199254740997}}
		}
	}`, &data, false)

	if !data.MaxBudget.IsNull() || !data.SoftBudget.IsNull() || !data.TPMLimit.IsNull() || !data.RPMLimit.IsNull() || !data.MaxParallelRequests.IsNull() {
		t.Fatalf("unconfigured scalar adopted remote value or remained unknown: %#v", data)
	}
	if !data.ModelMaxBudget.IsNull() || !data.ModelRPMLimit.IsNull() || !data.ModelTPMLimit.IsNull() {
		t.Fatalf("unconfigured map adopted remote value or remained unknown: %#v, %#v, %#v", data.ModelMaxBudget, data.ModelRPMLimit, data.ModelTPMLimit)
	}
}

func TestKeyNumericImportAdoptsVisibleValuesAndClearsOmissions(t *testing.T) {
	t.Parallel()

	adopted := KeyResourceModel{Key: types.StringValue("sk-import")}
	readKeyNumericResponse(t, `{
		"key":"sk-import",
		"info":{
			"tpm_limit":9007199254740993,
			"rpm_limit":12,
			"max_parallel_requests":13,
			"litellm_budget_table":{"max_budget":50.5,"soft_budget":40,"model_max_budget":{"gpt-4o":1.5}},
			"metadata":{"model_rpm_limit":{"gpt-4o":9007199254740995},"model_tpm_limit":{}}
		}
	}`, &adopted, true)
	if adopted.MaxBudget.ValueFloat64() != 50.5 || adopted.SoftBudget.ValueFloat64() != 40 || adopted.TPMLimit.ValueInt64() != 9007199254740993 || adopted.RPMLimit.ValueInt64() != 12 || adopted.MaxParallelRequests.ValueInt64() != 13 {
		t.Fatalf("import did not adopt visible scalars: %#v", adopted)
	}
	assertKeyNumericMaps(t, &adopted,
		map[string]float64{"gpt-4o": 1.5},
		map[string]int64{"gpt-4o": 9007199254740995},
		map[string]int64{},
	)

	cleared := configuredKeyNumericModel("sk-import-absent")
	readKeyNumericResponse(t, `{"key":"sk-import-absent","info":{"litellm_budget_table":{},"metadata":{}}}`, &cleared, true)
	if !cleared.MaxBudget.IsNull() || !cleared.SoftBudget.IsNull() || !cleared.TPMLimit.IsNull() || !cleared.RPMLimit.IsNull() || !cleared.MaxParallelRequests.IsNull() || !cleared.ModelMaxBudget.IsNull() || !cleared.ModelRPMLimit.IsNull() || !cleared.ModelTPMLimit.IsNull() {
		t.Fatalf("import omissions did not clear prior numeric state: %#v", cleared)
	}
}

func TestKeyDataSourcesAdoptNestedBudgetsAndExactTopLevelLimits(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"key":"sk-data",
			"info":{
				"key_alias":"data",
				"models":[],
				"max_budget":999,
				"soft_budget":999,
				"spend":5.25,
				"tpm_limit":9007199254740993,
				"rpm_limit":9223372036854775807,
				"max_parallel_requests":9007199254740997,
				"litellm_budget_table":{"max_budget":100.25,"soft_budget":80,"tpm_limit":1,"rpm_limit":2,"max_parallel_requests":3},
				"metadata":{},
				"blocked":false
			}
		}`))
	}))
	defer server.Close()

	ctx := context.Background()
	dataSource := &KeyDataSource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	var schemaResponse datasource.SchemaResponse
	dataSource.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("key data source schema: %v", schemaResponse.Diagnostics)
	}
	raw, err := tftypes.ValueFromJSON([]byte(`{
		"id":null,
		"key":"sk-data",
		"key_hash":null,
		"key_alias":null,
		"models":null,
		"max_budget":null,
		"spend":null,
		"user_id":null,
		"team_id":null,
		"project_id":null,
		"max_parallel_requests":null,
		"tpm_limit":null,
		"rpm_limit":null,
		"budget_duration":null,
		"soft_budget":null,
		"metadata":null,
		"tags":null,
		"blocked":null,
		"router_settings":null
	}`), schemaResponse.Schema.Type().TerraformType(ctx))
	if err != nil {
		t.Fatalf("build key data source config: %v", err)
	}
	config := tfsdk.Config{Raw: raw, Schema: schemaResponse.Schema}
	response := &datasource.ReadResponse{State: tfsdk.State{Raw: raw, Schema: schemaResponse.Schema}}
	dataSource.Read(ctx, datasource.ReadRequest{Config: config}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("key data source read: %v", response.Diagnostics)
	}
	var data KeyDataSourceModel
	if diagnostics := response.State.Get(ctx, &data); diagnostics.HasError() {
		t.Fatalf("decode key data source state: %v", diagnostics)
	}
	if data.MaxBudget.ValueFloat64() != 100.25 || data.SoftBudget.ValueFloat64() != 80 || data.Spend.ValueFloat64() != 5.25 {
		t.Fatalf("key data source budgets = %v, %v, spend %v", data.MaxBudget.ValueFloat64(), data.SoftBudget.ValueFloat64(), data.Spend.ValueFloat64())
	}
	if data.TPMLimit.ValueInt64() != 9007199254740993 || data.RPMLimit.ValueInt64() != math.MaxInt64 || data.MaxParallelRequests.ValueInt64() != 9007199254740997 {
		t.Fatalf("key data source limits = %d, %d, %d", data.TPMLimit.ValueInt64(), data.RPMLimit.ValueInt64(), data.MaxParallelRequests.ValueInt64())
	}

	item, err := decodeKeyListItem(json.RawMessage(`{
		"token":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"max_budget":999,
		"spend":3.5,
		"tpm_limit":9007199254740993,
		"rpm_limit":9223372036854775807,
		"litellm_budget_table":{"max_budget":77.5}
	}`))
	if err != nil {
		t.Fatalf("decode key inventory item: %v", err)
	}
	if item.MaxBudget.ValueFloat64() != 77.5 || item.Spend.ValueFloat64() != 3.5 || item.TPMLimit.ValueInt64() != 9007199254740993 || item.RPMLimit.ValueInt64() != math.MaxInt64 {
		t.Fatalf("key inventory values = %#v", item)
	}
}
