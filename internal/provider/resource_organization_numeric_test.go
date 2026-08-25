package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestBuildOrganizationRequestPreservesNumericMapPresence(t *testing.T) {
	t.Parallel()

	resourceUnderTest := &OrganizationResource{}
	unmanaged := &OrganizationResourceModel{
		OrganizationAlias: types.StringValue("acme"),
		ModelRPMLimit:     types.MapNull(types.Int64Type),
		ModelTPMLimit:     types.MapNull(types.Int64Type),
	}
	request, err := resourceUnderTest.buildOrganizationRequest(context.Background(), unmanaged)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := request["model_rpm_limit"]; present {
		t.Fatal("null model_rpm_limit was sent")
	}
	if _, present := request["model_tpm_limit"]; present {
		t.Fatal("null model_tpm_limit was sent")
	}

	managedEmpty := &OrganizationResourceModel{
		OrganizationAlias: types.StringValue("acme"),
		ModelRPMLimit:     types.MapValueMust(types.Int64Type, map[string]attr.Value{}),
		ModelTPMLimit:     types.MapValueMust(types.Int64Type, map[string]attr.Value{}),
		Metadata: types.MapValueMust(types.StringType, map[string]attr.Value{
			"environment":     types.StringValue("production"),
			"model_rpm_limit": types.StringValue(`{"wrong":1}`),
			"model_tpm_limit": types.StringValue(`{"wrong":2}`),
		}),
	}
	request, err = resourceUnderTest.buildOrganizationRequest(context.Background(), managedEmpty)
	if err != nil {
		t.Fatal(err)
	}
	if values, ok := request["model_rpm_limit"].(map[string]int64); !ok || len(values) != 0 {
		t.Fatalf("known empty model_rpm_limit = %#v", request["model_rpm_limit"])
	}
	if values, ok := request["model_tpm_limit"].(map[string]int64); !ok || len(values) != 0 {
		t.Fatalf("known empty model_tpm_limit = %#v", request["model_tpm_limit"])
	}
	metadata := request["metadata"].(map[string]interface{})
	if metadata["environment"] != "production" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if _, present := metadata["model_rpm_limit"]; present {
		t.Fatal("reserved RPM map leaked into metadata request")
	}
	if _, present := metadata["model_tpm_limit"]; present {
		t.Fatal("reserved TPM map leaked into metadata request")
	}
}

func TestReadOrganizationMetadataMapsAreAtomic(t *testing.T) {
	t.Parallel()

	server, client := jsonServer(t, map[string]interface{}{
		"organization_info": map[string]interface{}{
			"organization_id":    "org-1",
			"organization_alias": "acme",
			"metadata": map[string]interface{}{
				"environment":     "changed",
				"model_rpm_limit": map[string]interface{}{"large": int64(9007199254740993)},
				"model_tpm_limit": map[string]interface{}{"bad": "not-an-integer"},
			},
		},
	})
	defer server.Close()

	priorMetadata := types.MapValueMust(types.StringType, map[string]attr.Value{"environment": types.StringValue("prior")})
	priorRPM := types.MapValueMust(types.Int64Type, map[string]attr.Value{"old": types.Int64Value(1)})
	priorTPM := types.MapValueMust(types.Int64Type, map[string]attr.Value{"old": types.Int64Value(2)})
	data := &OrganizationResourceModel{
		OrganizationID: types.StringValue("org-1"),
		Metadata:       priorMetadata,
		ModelRPMLimit:  priorRPM,
		ModelTPMLimit:  priorTPM,
	}
	err := (&OrganizationResource{client: client}).readOrganization(context.Background(), data)
	if err == nil {
		t.Fatal("expected malformed model_tpm_limit to fail")
	}
	if !data.Metadata.Equal(priorMetadata) || !data.ModelRPMLimit.Equal(priorRPM) || !data.ModelTPMLimit.Equal(priorTPM) {
		t.Fatalf("related organization state was partially updated: %#v", data)
	}
}

func TestOrganizationDataSourceReadsExactMetadataModelLimits(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"organization_info":{"organization_id":"org-1","organization_alias":"acme","metadata":{"environment":"production","model_rpm_limit":{"large":9007199254740993},"model_tpm_limit":{"maximum":9223372036854775807}},"litellm_budget_table":null}}`))
	}))
	defer server.Close()

	ctx := context.Background()
	dataSource := &OrganizationDataSource{client: &Client{APIBase: server.URL, APIKey: "test-key", HTTPClient: server.Client()}}
	var schemaResponse datasource.SchemaResponse
	dataSource.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	if schemaResponse.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", schemaResponse.Diagnostics)
	}
	raw, err := tftypes.ValueFromJSON([]byte(`{"id":null,"organization_id":"org-1","organization_alias":null,"models":null,"budget_id":null,"max_budget":null,"tpm_limit":null,"rpm_limit":null,"model_rpm_limit":null,"model_tpm_limit":null,"budget_duration":null,"metadata":null,"blocked":null,"tags":null,"spend":null,"created_at":null,"updated_at":null}`), schemaResponse.Schema.Type().TerraformType(ctx))
	if err != nil {
		t.Fatalf("build config: %v", err)
	}
	config := tfsdk.Config{Raw: raw, Schema: schemaResponse.Schema}
	response := &datasource.ReadResponse{State: tfsdk.State{Raw: raw, Schema: schemaResponse.Schema}}
	dataSource.Read(ctx, datasource.ReadRequest{Config: config}, response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	var data OrganizationDataSourceModel
	if diagnostics := response.State.Get(ctx, &data); diagnostics.HasError() {
		t.Fatalf("decode state: %v", diagnostics)
	}
	var rpm, tpm map[string]int64
	data.ModelRPMLimit.ElementsAs(ctx, &rpm, false)
	data.ModelTPMLimit.ElementsAs(ctx, &tpm, false)
	if rpm["large"] != 9007199254740993 || tpm["maximum"] != 9223372036854775807 {
		t.Fatalf("model limits = %#v, %#v", rpm, tpm)
	}
	var metadata map[string]string
	data.Metadata.ElementsAs(ctx, &metadata, false)
	if len(metadata) != 1 || metadata["environment"] != "production" {
		t.Fatalf("metadata was inconsistent with dedicated maps: %#v", metadata)
	}
}
