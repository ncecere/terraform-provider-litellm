package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestFallbackSpecialIdentityImportRefreshNoDriftDestroyProtocol(t *testing.T) {
	ctx := context.Background()
	model := "tenant/llama3:8b?revision=1&literal=%2F-雪"
	fallbackType := "content_policy"
	importID := model + ":" + fallbackType
	wantURI := fallbackEndpoint(model, fallbackType)
	var reads, deletes atomic.Int64
	var deleted atomic.Bool

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.RequestURI != wantURI {
			t.Errorf("request URI = %q, want %q", request.RequestURI, wantURI)
		}
		if request.URL.Query().Get("fallback_type") != fallbackType {
			t.Errorf("fallback_type query was not decoded exactly")
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			reads.Add(1)
			if deleted.Load() {
				writer.WriteHeader(http.StatusNotFound)
				_, _ = writer.Write([]byte(`{}`))
				return
			}
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"model": model, "fallback_type": fallbackType, "fallback_models": []string{"secondary"},
			})
		case http.MethodDelete:
			deletes.Add(1)
			deleted.Store(true)
			_, _ = writer.Write([]byte(`{}`))
		default:
			http.Error(writer, "unexpected method", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_fallback"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: importID})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v resources=%d", err, imported.Diagnostics, len(imported.ImportedResources))
	}

	refreshed, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(refreshed.Diagnostics) {
		t.Fatalf("refresh: err=%v diagnostics=%v", err, refreshed.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schema, refreshed.NewState)
	for field, want := range map[string]string{"model": model, "fallback_type": fallbackType, "id": importID} {
		var got string
		if err := attributes[field].As(&got); err != nil || got != want {
			t.Fatalf("%s = %q, want %q (error %v)", field, got, want, err)
		}
	}

	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, map[string]interface{}{
		"model": model, "fallback_type": fallbackType, "fallback_models": []tftypes.Value{tftypes.NewValue(tftypes.String, "secondary")},
	}))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: refreshed.NewState, ProposedNewState: refreshed.NewState, PriorPrivate: refreshed.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("no-drift plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	if action := organizationProjectProtocolPlannedAction(t, schema, refreshed.NewState, planned); action != organizationProjectProtocolActionNoOp {
		t.Fatalf("no-drift plan action = %s, want NoOp", action)
	}

	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	destroyPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: nullState, PriorState: refreshed.NewState, ProposedNewState: nullState, PriorPrivate: refreshed.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyPlan.Diagnostics) {
		t.Fatalf("destroy plan: err=%v diagnostics=%v", err, destroyPlan.Diagnostics)
	}
	destroyed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: nullState, PriorState: refreshed.NewState, PlannedState: destroyPlan.PlannedState, PlannedPrivate: destroyPlan.PlannedPrivate,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyed.Diagnostics) || deletes.Load() != 1 {
		t.Fatalf("destroy: err=%v diagnostics=%v deletes=%d", err, destroyed.Diagnostics, deletes.Load())
	}
	terminal, err := destroyed.NewState.Unmarshal(schema.ValueType())
	if err != nil || !terminal.IsNull() {
		t.Fatalf("terminal state = %v (error %v)", terminal, err)
	}
	if reads.Load() != 3 {
		t.Fatalf("GET requests = %d, want import, refresh, and delete confirmation", reads.Load())
	}
}

func TestFallbackDelete404WithAuthoritativeGETRetainsStateProtocol(t *testing.T) {
	ctx := context.Background()
	const model = "secret-fallback-delete-model"
	var reads, deletes atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodGet:
			reads.Add(1)
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"model": model, "fallback_type": "general", "fallback_models": []string{"secondary"},
			})
		case http.MethodDelete:
			deletes.Add(1)
			writer.WriteHeader(http.StatusNotFound)
			_, _ = writer.Write([]byte(`{}`))
		default:
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_fallback"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{
		TypeName: typeName, ID: model + ":general",
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	prior := imported.ImportedResources[0].State
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	destroyPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: nullState, PriorState: prior, ProposedNewState: nullState,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(destroyPlan.Diagnostics) {
		t.Fatalf("destroy plan: err=%v diagnostics=%v", err, destroyPlan.Diagnostics)
	}
	destroyed, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName: typeName, Config: nullState, PriorState: prior,
		PlannedState: destroyPlan.PlannedState, PlannedPrivate: destroyPlan.PlannedPrivate,
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(destroyed.Diagnostics) {
		t.Fatalf("unconfirmed destroy: err=%v diagnostics=%v", err, destroyed.Diagnostics)
	}
	retained, err := destroyed.NewState.Unmarshal(schema.ValueType())
	if err != nil || retained.IsNull() {
		t.Fatalf("unconfirmed delete discarded state: value=%v error=%v", retained, err)
	}
	attributes := protocolAttributeMap(t, schema, destroyed.NewState)
	var retainedID string
	if err := attributes["id"].As(&retainedID); err != nil || retainedID != model+":general" {
		t.Fatalf("retained id was not prior state (error %v)", err)
	}
	if deletes.Load() != 1 || reads.Load() != 6 {
		t.Fatalf("requests: deletes=%d reads=%d, want 1 and 6", deletes.Load(), reads.Load())
	}
	var diagnostic strings.Builder
	for _, item := range destroyed.Diagnostics {
		diagnostic.WriteString(item.Summary)
		diagnostic.WriteString(item.Detail)
	}
	for _, forbidden := range []string{model, server.URL, "secondary"} {
		if strings.Contains(diagnostic.String(), forbidden) {
			t.Fatalf("diagnostic exposed %q", forbidden)
		}
	}
	if !strings.Contains(diagnostic.String(), "Fallback Delete Unconfirmed") {
		t.Fatalf("diagnostic was not actionable: %s", diagnostic.String())
	}
}

func TestFallbackLegacyModelOnlyImportDefaultsGeneralProtocol(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.RequestURI != "/fallback/legacy-model?fallback_type=general" {
			t.Errorf("request URI = %q", request.RequestURI)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]interface{}{
			"model": "legacy-model", "fallback_type": "general", "fallback_models": []string{"secondary"},
		})
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_fallback"
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "legacy-model"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("legacy import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schemas.ResourceSchemas[typeName], imported.ImportedResources[0].State)
	for field, expected := range map[string]string{"id": "legacy-model:general", "model": "legacy-model", "fallback_type": "general"} {
		var actual string
		if err := attributes[field].As(&actual); err != nil || actual != expected {
			t.Fatalf("%s=%q err=%v", field, actual, err)
		}
	}
}

func TestFallbackDiagnosticsOmitIdentityResponseAndTransportDetailsProtocol(t *testing.T) {
	ctx := context.Background()
	const secretModel = "secret/model?token=do-not-print"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(writer, `{"detail":{"error":"model %s failed at https://internal.invalid/path because dial tcp 10.0.0.1"}}`, secretModel)
	}))
	defer server.Close()

	protocolServer, _ := configuredImportProtocolServer(t, ctx, server.URL)
	response, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{
		TypeName: "litellm_fallback", ID: secretModel + ":general",
	})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
		t.Fatalf("import failure: err=%v diagnostics=%v", err, response.Diagnostics)
	}
	var diagnosticBuilder strings.Builder
	for _, item := range response.Diagnostics {
		diagnosticBuilder.WriteString(item.Summary)
		diagnosticBuilder.WriteByte('\n')
		diagnosticBuilder.WriteString(item.Detail)
		diagnosticBuilder.WriteByte('\n')
	}
	diagnostic := diagnosticBuilder.String()
	for _, forbidden := range []string{secretModel, server.URL, "internal.invalid", "dial tcp", "10.0.0.1", "do-not-print"} {
		if strings.Contains(diagnostic, forbidden) {
			t.Fatalf("diagnostic exposed forbidden content %q: %s", forbidden, diagnostic)
		}
	}
	if !strings.Contains(diagnostic, "HTTP status 500") || !strings.Contains(diagnostic, "consult trusted LiteLLM logs") {
		t.Fatalf("diagnostic is not safe and actionable: %s", diagnostic)
	}
}
