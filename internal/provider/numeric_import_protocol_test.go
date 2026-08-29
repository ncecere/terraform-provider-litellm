package provider

import (
	"context"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func protocolAttributeMap(t *testing.T, schema *tfprotov6.Schema, dynamic *tfprotov6.DynamicValue) map[string]tftypes.Value {
	t.Helper()
	value, err := dynamic.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatalf("decode protocol state: %v", err)
	}
	attributes := map[string]tftypes.Value{}
	if err := value.As(&attributes); err != nil {
		t.Fatalf("decode protocol attributes: %v", err)
	}
	return attributes
}

func protocolInt64(t *testing.T, value tftypes.Value) int64 {
	t.Helper()
	var decoded big.Float
	if err := value.As(&decoded); err != nil {
		t.Fatalf("decode protocol number: %v", err)
	}
	integer, accuracy := decoded.Int64()
	if accuracy != big.Exact {
		t.Fatalf("protocol number is not an exact int64: %s", decoded.String())
	}
	return integer
}

func protocolInt64Map(t *testing.T, value tftypes.Value) map[string]int64 {
	t.Helper()
	raw := map[string]tftypes.Value{}
	if err := value.As(&raw); err != nil {
		t.Fatalf("decode protocol number map: %v", err)
	}
	result := make(map[string]int64, len(raw))
	for key, number := range raw {
		result[key] = protocolInt64(t, number)
	}
	return result
}

func TestNumericImportProtocolAdoptsExactStateThenDetectsDrift(t *testing.T) {
	ctx := context.Background()
	var organizationRPM atomic.Int64
	var organizationReads atomic.Int64
	var modelReads atomic.Int64
	organizationRPM.Store(9007199254740993)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/organization/info":
			if organizationReads.Add(1) == 1 {
				_, _ = writer.Write([]byte(`{"organization_info":{"organization_id":"wrong-organization","organization_alias":"wrong"}}`))
			} else if organizationRPM.Load() == 9007199254740993 {
				_, _ = writer.Write([]byte(`{"organization_info":{"organization_id":"org-import","organization_alias":"imported","metadata":{"model_rpm_limit":{"large":9007199254740993},"model_tpm_limit":{}},"litellm_budget_table":{"max_budget":12.5,"tpm_limit":9007199254740993,"rpm_limit":9223372036854775807}}}`))
			} else {
				_, _ = writer.Write([]byte(`{"organization_info":{"organization_id":"org-import","organization_alias":"imported","metadata":{"model_rpm_limit":{"large":9007199254740995},"model_tpm_limit":{}},"litellm_budget_table":{"max_budget":12.5,"tpm_limit":9007199254740995,"rpm_limit":9223372036854775807}}}`))
			}
		case "/model/info":
			if modelReads.Add(1) == 1 {
				_, _ = writer.Write([]byte(`{"data":[{"model_name":"wrong-model","litellm_params":{},"model_info":{"id":"wrong-model"}}]}`))
			} else {
				_, _ = writer.Write([]byte(`{"data":[{"model_name":"claude-import","litellm_params":{"custom_llm_provider":"anthropic","model":"anthropic/claude","tpm":9007199254740993,"thinking":{"type":"enabled","budget_tokens":9007199254740993}},"model_info":{"id":"model-import","base_model":"claude"}}]}`))
			}
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemaResponse, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemaResponse.Diagnostics) {
		t.Fatalf("get provider schema: %v, %v", err, schemaResponse.Diagnostics)
	}
	providerValue, err := tftypes.ValueFromJSON(
		[]byte(`{"api_base":"`+server.URL+`","api_key":"test-key","insecure_skip_verify":null,"litellm_changed_by":null}`),
		schemaResponse.Provider.ValueType(),
	)
	if err != nil {
		t.Fatalf("build provider config: %v", err)
	}
	providerConfig := accessGroupProtocolDynamicValue(t, schemaResponse.Provider, providerValue)
	configureResponse, err := protocolServer.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: providerConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configureResponse.Diagnostics) {
		t.Fatalf("configure provider: %v, %v", err, configureResponse.Diagnostics)
	}

	organizationSchema := schemaResponse.ResourceSchemas["litellm_organization"]
	importResponse, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_organization", ID: "org-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(importResponse.Diagnostics) || len(importResponse.ImportedResources) != 1 {
		t.Fatalf("import organization: %v, %v", err, importResponse.Diagnostics)
	}
	importedOrganization := importResponse.ImportedResources[0]
	rejectedOrganization, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_organization",
		CurrentState: importedOrganization.State,
		Private:      importedOrganization.Private,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(rejectedOrganization.Diagnostics) || !protocolPrivateHasKey(t, rejectedOrganization.Private, numericImportedPrivateKey) {
		t.Fatalf("non-authoritative organization read was accepted: %v, %v private=%x", err, rejectedOrganization.Diagnostics, rejectedOrganization.Private)
	}
	importedValue, _ := importedOrganization.State.Unmarshal(organizationSchema.ValueType())
	rejectedValue, _ := rejectedOrganization.NewState.Unmarshal(organizationSchema.ValueType())
	if !importedValue.Equal(rejectedValue) {
		t.Fatal("rejected organization read changed imported state")
	}
	firstRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_organization",
		CurrentState: rejectedOrganization.NewState,
		Private:      rejectedOrganization.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(firstRead.Diagnostics) || protocolPrivateHasKey(t, firstRead.Private, numericImportedPrivateKey) {
		t.Fatalf("authoritative imported organization read: %v, %v private=%x", err, firstRead.Diagnostics, firstRead.Private)
	}
	firstAttributes := protocolAttributeMap(t, organizationSchema, firstRead.NewState)
	if got := protocolInt64Map(t, firstAttributes["model_rpm_limit"])["large"]; got != 9007199254740993 {
		t.Fatalf("imported organization RPM = %d", got)
	}
	if importedTPM := protocolInt64(t, firstAttributes["tpm_limit"]); importedTPM != 9007199254740993 {
		t.Fatalf("imported organization TPM = %d", importedTPM)
	}

	secondRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_organization",
		CurrentState: firstRead.NewState,
		Private:      firstRead.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(secondRead.Diagnostics) {
		t.Fatalf("second imported organization read: %v, %v", err, secondRead.Diagnostics)
	}
	firstValue, _ := firstRead.NewState.Unmarshal(organizationSchema.ValueType())
	secondValue, _ := secondRead.NewState.Unmarshal(organizationSchema.ValueType())
	if !firstValue.Equal(secondValue) {
		t.Fatal("second imported organization read drifted without a remote change")
	}

	organizationRPM.Store(9007199254740995)
	driftRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_organization",
		CurrentState: secondRead.NewState,
		Private:      secondRead.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(driftRead.Diagnostics) {
		t.Fatalf("organization drift read: %v, %v", err, driftRead.Diagnostics)
	}
	driftAttributes := protocolAttributeMap(t, organizationSchema, driftRead.NewState)
	if got := protocolInt64Map(t, driftAttributes["model_rpm_limit"])["large"]; got != 9007199254740995 {
		t.Fatalf("organization map drift = %d", got)
	}
	if driftTPM := protocolInt64(t, driftAttributes["tpm_limit"]); driftTPM != 9007199254740995 {
		t.Fatalf("organization scalar drift = %d", driftTPM)
	}

	modelSchema := schemaResponse.ResourceSchemas["litellm_model"]
	modelImport, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_model", ID: "model-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(modelImport.Diagnostics) || len(modelImport.ImportedResources) != 1 {
		t.Fatalf("import model: %v, %v", err, modelImport.Diagnostics)
	}
	importedModel := modelImport.ImportedResources[0]
	rejectedModel, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_model",
		CurrentState: importedModel.State,
		Private:      importedModel.Private,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(rejectedModel.Diagnostics) || !protocolPrivateHasKey(t, rejectedModel.Private, modelImportedPrivateKey) {
		t.Fatalf("non-authoritative model read was accepted: %v, %v private=%x", err, rejectedModel.Diagnostics, rejectedModel.Private)
	}
	modelImportValue, _ := importedModel.State.Unmarshal(modelSchema.ValueType())
	modelRejectedValue, _ := rejectedModel.NewState.Unmarshal(modelSchema.ValueType())
	if !modelImportValue.Equal(modelRejectedValue) {
		t.Fatal("rejected model read changed imported state")
	}
	modelRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_model",
		CurrentState: rejectedModel.NewState,
		Private:      rejectedModel.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(modelRead.Diagnostics) {
		t.Fatalf("read imported model after rejected identity: %v, %v", err, modelRead.Diagnostics)
	}
	if protocolPrivateHasKey(t, modelRead.Private, modelImportedPrivateKey) {
		t.Fatalf("authoritative model read retained import marker: %x", modelRead.Private)
	}
	modelAttributes := protocolAttributeMap(t, modelSchema, modelRead.NewState)
	additionalRaw := map[string]tftypes.Value{}
	if err := modelAttributes["additional_litellm_params"].As(&additionalRaw); err != nil {
		t.Fatalf("decode imported model additional params: %v", err)
	}
	var thinking string
	if err := additionalRaw["thinking"].As(&thinking); err != nil {
		t.Fatalf("decode imported model thinking: %v", err)
	}
	if thinking != `{"budget_tokens":9007199254740993,"type":"enabled"}` {
		t.Fatalf("imported model thinking = %s", thinking)
	}
	if modelTPM := protocolInt64(t, modelAttributes["tpm"]); modelTPM != 9007199254740993 {
		t.Fatalf("imported model TPM = %d", modelTPM)
	}
	secondModelRead, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_model",
		CurrentState: modelRead.NewState,
		Private:      modelRead.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(secondModelRead.Diagnostics) {
		t.Fatalf("second imported model read: %v, %v", err, secondModelRead.Diagnostics)
	}
	firstModelValue, _ := modelRead.NewState.Unmarshal(modelSchema.ValueType())
	secondModelValue, _ := secondModelRead.NewState.Unmarshal(modelSchema.ValueType())
	if !firstModelValue.Equal(secondModelValue) {
		t.Fatal("second imported model read drifted after import ownership transitioned")
	}
}
