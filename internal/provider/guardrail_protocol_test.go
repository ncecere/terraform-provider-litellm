package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

func TestGuardrailUnmaskedImportOmissionIsStableProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/guardrails/guardrail-import/info" {
			http.NotFound(writer, request)
			return
		}
		_, _ = fmt.Fprint(writer, `{"guardrail_id":"guardrail-import","guardrail_name":"managed","litellm_params":{"guardrail":"bedrock","mode":"pre_call","default_on":true,"guardrailIdentifier":"remote-default"}}`)
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_guardrail"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "guardrail-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	configValue := organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"guardrail_name": "managed", "guardrail": "bedrock", "mode": "pre_call",
	})
	config := accessGroupProtocolDynamicValue(t, schema, configValue)
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: read.NewState, ProposedNewState: read.NewState, PriorPrivate: read.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned) != organizationProjectProtocolActionNoOp {
		t.Fatalf("stable omitted import: err=%v diagnostics=%v action=%s", err, planned.Diagnostics, organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned))
	}
}

func TestGuardrailSingleDataSourceRejectsNullParamsProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"guardrail_id":"guardrail-null","guardrail_name":"malformed","litellm_params":null}`)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_guardrail"
	schema := schemas.DataSourceSchemas[typeName]
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{"guardrail_id": "guardrail-null"}))
	read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: typeName, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	if !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("null litellm_params unexpectedly succeeded: diagnostics=%v", read.Diagnostics)
	}
}

func TestGuardrailMaskedImportFailsClosedProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/guardrails/guardrail-import/info" {
			http.NotFound(writer, request)
			return
		}
		_, _ = fmt.Fprint(writer, `{"guardrail_id":"guardrail-import","guardrail_name":"managed","litellm_params":{"guardrail":"bedrock","mode":"pre_call","api_key":"se****et"}}`)
	}))
	defer server.Close()

	protocolServer, _ := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_guardrail"
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "guardrail-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("masked import unexpectedly succeeded: diagnostics=%v", read.Diagnostics)
	}
}
