package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestVectorStoreCreateRecoversFromPostMutationErrorProtocol(t *testing.T) {
	ctx := context.Background()
	var createdID string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/vector_store/new":
			var body map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(writer, "invalid", http.StatusBadRequest)
				return
			}
			createdID, _ = body["vector_store_id"].(string)
			http.Error(writer, "simulated response loss", http.StatusInternalServerError)
		case "/vector_store/info":
			if createdID == "" {
				http.NotFound(writer, request)
				return
			}
			_, _ = fmt.Fprintf(writer, `{"vector_store":{"vector_store_id":%q,"vector_store_name":"created","custom_llm_provider":"bedrock","litellm_params":{}}}`, createdID)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_vector_store"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"vector_store_name": "created", "custom_llm_provider": "bedrock"}
	proposedValues := map[string]interface{}{}
	for key, value := range configValues {
		proposedValues[key] = value
	}
	for _, key := range []string{"id", "vector_store_id", "vector_store_description", "vector_store_metadata", "litellm_credential_name", "litellm_params", "created_at", "vector_store_description_configured", "vector_store_metadata_configured", "litellm_credential_name_configured", "litellm_params_configured"} {
		proposedValues[key] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || createdID == "" {
		t.Fatalf("recovered create: err=%v diagnostics=%v id=%q", err, applied.Diagnostics, createdID)
	}
	attributes := protocolAttributeMap(t, schema, applied.NewState)
	var id string
	if err := attributes["id"].As(&id); err != nil || id != createdID {
		t.Fatalf("recovered ID=%q err=%v, want %q", id, err, createdID)
	}
}

func TestVectorStoreImportOwnershipAndCreateOnlyReplacementProtocol(t *testing.T) {
	ctx := context.Background()
	var updates atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/vector_store/info":
			_, _ = fmt.Fprint(writer, `{"vector_store":{"vector_store_id":"vs-import","vector_store_name":"store","custom_llm_provider":"bedrock","vector_store_description":"description","vector_store_metadata":{"environment":"prod"},"litellm_credential_name":"credential","litellm_params":{"api_base":"https://example.invalid"},"created_at":"2026-01-01T00:00:00Z"}}`)
		case "/vector_store/update":
			updates.Add(1)
			var body map[string]interface{}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				http.Error(writer, "invalid", http.StatusBadRequest)
				return
			}
			for _, unsupported := range []string{"litellm_credential_name", "litellm_params"} {
				if _, present := body[unsupported]; present {
					t.Errorf("unsupported update field %s was sent", unsupported)
				}
			}
			http.Error(writer, "simulated post-mutation registry failure", http.StatusInternalServerError)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_vector_store"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "vs-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
	}

	baseConfig := map[string]interface{}{"vector_store_name": "store", "custom_llm_provider": "bedrock"}
	plan := func(values map[string]interface{}, proposed, prior *tfprotov6.DynamicValue, private []byte) *tfprotov6.PlanResourceChangeResponse {
		t.Helper()
		config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, values))
		response, planErr := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
			TypeName: typeName, Config: config, PriorState: prior, ProposedNewState: proposed, PriorPrivate: private,
		})
		if planErr != nil {
			t.Fatal(planErr)
		}
		return response
	}

	omitted := plan(baseConfig, read.NewState, read.NewState, read.Private)
	if accessGroupProtocolDiagnosticsHaveError(omitted.Diagnostics) || len(omitted.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted) != organizationProjectProtocolActionNoOp {
		t.Fatalf("import omission: diagnostics=%v replace=%v action=%s", omitted.Diagnostics, omitted.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, read.NewState, omitted))
	}

	ownedConfig := map[string]interface{}{
		"vector_store_name": "store", "custom_llm_provider": "bedrock",
		"vector_store_description": "description",
		"vector_store_metadata":    map[string]tftypes.Value{"environment": tftypes.NewValue(tftypes.String, "prod")},
		"litellm_credential_name":  "credential",
		"litellm_params":           map[string]tftypes.Value{"api_base": tftypes.NewValue(tftypes.String, "https://example.invalid")},
	}
	owned := plan(ownedConfig, read.NewState, read.NewState, read.Private)
	if accessGroupProtocolDiagnosticsHaveError(owned.Diagnostics) || len(owned.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, read.NewState, owned) != organizationProjectProtocolActionUpdate {
		t.Fatalf("equal ownership transition: diagnostics=%v replace=%v action=%s", owned.Diagnostics, owned.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, read.NewState, owned))
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, ownedConfig))
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: read.NewState, PlannedState: owned.PlannedState, PlannedPrivate: owned.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || updates.Load() != 1 {
		t.Fatalf("ownership apply: err=%v diagnostics=%v updates=%d", err, applied.Diagnostics, updates.Load())
	}

	removal := plan(baseConfig, applied.NewState, applied.NewState, applied.Private)
	if accessGroupProtocolDiagnosticsHaveError(removal.Diagnostics) || len(removal.RequiresReplace) < 2 || organizationProjectProtocolPlannedAction(t, schema, applied.NewState, removal) != organizationProjectProtocolActionReplace {
		t.Fatalf("owned create-only removal: diagnostics=%v replace=%v action=%s", removal.Diagnostics, removal.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, applied.NewState, removal))
	}

	dataSourceSchema := schemas.DataSourceSchemas[typeName]
	dataSourceConfig := accessGroupProtocolDynamicValue(t, dataSourceSchema, organizationProjectProtocolValue(t, dataSourceSchema, map[string]interface{}{"vector_store_id": "vs-import"}))
	dataSource, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: typeName, Config: dataSourceConfig})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(dataSource.Diagnostics) {
		t.Fatalf("data source read: err=%v diagnostics=%v", err, dataSource.Diagnostics)
	}
	attributes := protocolAttributeMap(t, dataSourceSchema, dataSource.State)
	var name string
	if err := attributes["vector_store_name"].As(&name); err != nil || name != "store" {
		t.Fatalf("nested data source name = %q, err=%v", name, err)
	}
}
