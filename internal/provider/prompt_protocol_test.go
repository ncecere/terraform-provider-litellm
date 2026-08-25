package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestPromptCreateAcceptsAuthoritativeAbsentScopedIdentityProtocol(t *testing.T) {
	ctx := context.Background()
	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/prompts":
			posts.Add(1)
			_, _ = fmt.Fprint(writer, `{"prompt_id":"new-prompt","environment":"production","version":1,"litellm_params":{"prompt_integration":"dotprompt"},"prompt_info":{"prompt_type":"db","environment":"production"}}`)
		case request.URL.RequestURI() == "/prompts/new-prompt?environment=production" && posts.Load() == 0:
			http.Error(writer, "prompt absent", http.StatusBadRequest)
		case request.URL.RequestURI() == "/prompts/new-prompt/versions?environment=production":
			http.Error(writer, "no versions", http.StatusNotFound)
		case request.URL.RequestURI() == "/prompts/new-prompt?environment=production":
			_, _ = fmt.Fprint(writer, `{"prompt_spec":{"prompt_id":"new-prompt","environment":"production","version":1,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","litellm_params":{"prompt_integration":"dotprompt"},"prompt_info":{"prompt_type":"db","environment":"production"}}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_prompt"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"prompt_id": "new-prompt", "environment": "production", "prompt_integration": "dotprompt", "prompt_type": "db"}
	proposedValues := map[string]interface{}{}
	for key, value := range configValues {
		proposedValues[key] = value
	}
	for _, key := range []string{"id", "version", "created_at", "updated_at"} {
		proposedValues[key] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || posts.Load() != 1 {
		t.Fatalf("create: err=%v diagnostics=%v posts=%d", err, applied.Diagnostics, posts.Load())
	}
}

func TestPromptCreateRefusesExistingScopedIdentityProtocol(t *testing.T) {
	ctx := context.Background()
	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			posts.Add(1)
			_, _ = fmt.Fprint(writer, `{}`)
			return
		}
		if request.URL.RequestURI() == "/prompts/existing?environment=production" {
			http.Error(writer, "ambiguous hidden/not-found response", http.StatusBadRequest)
			return
		}
		if request.URL.RequestURI() == "/prompts/existing/versions?environment=production" {
			_, _ = fmt.Fprint(writer, `{"prompts":[{"prompt_id":"existing","environment":"production","version":1,"litellm_params":{"prompt_integration":"dotprompt"},"prompt_info":{"prompt_type":"db","environment":"production"}}]}`)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_prompt"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{"prompt_id": "existing", "environment": "production", "prompt_integration": "dotprompt", "prompt_type": "db"}
	proposedValues := map[string]interface{}{}
	for key, value := range configValues {
		proposedValues[key] = value
	}
	for _, key := range []string{"id", "version", "created_at", "updated_at"} {
		proposedValues[key] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil {
		t.Fatal(err)
	}
	if !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || posts.Load() != 0 {
		t.Fatalf("existing prompt was mutated: diagnostics=%v posts=%d", applied.Diagnostics, posts.Load())
	}
}

func TestPromptConfigCreateIsRejectedBeforeAPIProtocol(t *testing.T) {
	ctx := context.Background()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, "http://127.0.0.1:1")
	const typeName = "litellm_prompt"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{
		"prompt_id": "config-prompt", "environment": "development",
		"prompt_integration": "dotprompt", "prompt_type": "config",
	}
	proposedValues := map[string]interface{}{}
	for key, value := range configValues {
		proposedValues[key] = value
	}
	for _, key := range []string{"id", "version", "created_at", "updated_at"} {
		proposedValues[key] = tftypes.UnknownValue
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	proposed := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, proposedValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil {
		t.Fatal(err)
	}
	if !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
		t.Fatal("config prompt create reached the API")
	}
}

func TestPromptDeleteRecoversMissingRegistryWithScopedPatchProtocol(t *testing.T) {
	ctx := context.Background()
	var deletes, patches atomic.Int64
	var gone atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.RequestURI() != "/prompts/managed?environment=production" {
			http.NotFound(writer, request)
			return
		}
		switch request.Method {
		case http.MethodDelete:
			if deletes.Add(1) == 1 {
				http.Error(writer, "registry key missing", http.StatusNotFound)
				return
			}
			gone.Store(true)
			_, _ = fmt.Fprint(writer, `{}`)
		case http.MethodPatch:
			patches.Add(1)
			_, _ = fmt.Fprint(writer, `{}`)
		case http.MethodGet:
			if gone.Load() {
				http.Error(writer, "prompt absent", http.StatusBadRequest)
				return
			}
			_, _ = fmt.Fprint(writer, `{"prompt_spec":{"prompt_id":"managed","environment":"production","version":1,"litellm_params":{"prompt_integration":"dotprompt"},"prompt_info":{"prompt_type":"db","environment":"production"}}}`)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_prompt"
	schema := schemas.ResourceSchemas[typeName]
	stateValues := map[string]interface{}{"id": "managed", "prompt_id": "managed", "environment": "production", "version": int64(1), "prompt_integration": "dotprompt", "prompt_type": "db"}
	state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: nullState, PriorState: state, ProposedNewState: nullState})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("destroy plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: nullState, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || deletes.Load() != 2 || patches.Load() != 1 {
		t.Fatalf("recovered delete: err=%v diagnostics=%v deletes=%d patches=%d", err, applied.Diagnostics, deletes.Load(), patches.Load())
	}
}

func TestPromptDeleteErrorsRequireScopedAbsenceConfirmation(t *testing.T) {
	ctx := context.Background()
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.RequestURI() != "/prompts/managed?environment=production" {
					http.NotFound(writer, request)
					return
				}
				if request.Method == http.MethodDelete {
					http.Error(writer, "simulated registry/config refusal", status)
					return
				}
				_, _ = fmt.Fprint(writer, `{"prompt_spec":{"prompt_id":"managed","environment":"production","version":1,"litellm_params":{"prompt_integration":"dotprompt"},"prompt_info":{"prompt_type":"db","environment":"production"}}}`)
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			const typeName = "litellm_prompt"
			schema := schemas.ResourceSchemas[typeName]
			stateValues := map[string]interface{}{
				"id": "managed", "prompt_id": "managed", "environment": "production",
				"version": int64(1), "prompt_integration": "dotprompt", "prompt_type": "db",
			}
			state := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, stateValues))
			nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: nullState, PriorState: state, ProposedNewState: nullState})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("destroy plan: err=%v diagnostics=%v", err, planned.Diagnostics)
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: nullState, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil {
				t.Fatal(err)
			}
			if !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) {
				t.Fatalf("DELETE %d discarded state despite surviving scoped prompt", status)
			}
		})
	}
}

func TestPromptLegacyStateRefreshesToDevelopmentWithoutReplacement(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.RequestURI() != "/prompts/legacy?environment=development" {
			http.NotFound(writer, request)
			return
		}
		_, _ = fmt.Fprint(writer, `{"prompt_spec":{"prompt_id":"legacy","environment":"development","version":4,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z","litellm_params":{"prompt_integration":"dotprompt","dotprompt_content":"content","ignore_prompt_manager_model":false,"ignore_prompt_manager_optional_params":false},"prompt_info":{"prompt_type":"db","environment":"development"}}}`)
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_prompt"
	schema := schemas.ResourceSchemas[typeName]
	legacyValues := map[string]interface{}{
		"id": "legacy", "prompt_id": "legacy", "prompt_integration": "dotprompt",
		"dotprompt_content": "content", "prompt_type": "db",
		"ignore_prompt_manager_model": false, "ignore_prompt_manager_optional_params": false,
	}
	legacyState := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, legacyValues))
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: legacyState})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("legacy read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	attributes := protocolAttributeMap(t, schema, read.NewState)
	var environment string
	if err := attributes["environment"].As(&environment); err != nil {
		t.Fatal(err)
	}
	version := protocolInt64(t, attributes["version"])
	if environment != defaultPromptEnvironment || version != 4 {
		t.Fatalf("migrated environment=%q version=%d", environment, version)
	}
	configValues := map[string]interface{}{
		"prompt_id": "legacy", "prompt_integration": "dotprompt", "dotprompt_content": "content",
		"prompt_type": "db", "ignore_prompt_manager_model": false, "ignore_prompt_manager_optional_params": false,
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{
		TypeName: typeName, Config: config, PriorState: read.NewState, ProposedNewState: read.NewState,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || len(planned.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned) != organizationProjectProtocolActionNoOp {
		t.Fatalf("legacy plan: err=%v diagnostics=%v replace=%v action=%s", err, planned.Diagnostics, planned.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned))
	}
}

func TestPromptImportAdoptsConfiguredDefaultsWithoutDrift(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.RequestURI() != "/prompts/imported?environment=production" {
			http.NotFound(writer, request)
			return
		}
		_, _ = fmt.Fprint(writer, `{"prompt_spec":{"prompt_id":"imported","environment":"production","version":2,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z","litellm_params":{"prompt_integration":"dotprompt","dotprompt_content":"content","ignore_prompt_manager_model":false,"ignore_prompt_manager_optional_params":false},"prompt_info":{"prompt_type":"db","environment":"production"}}}`)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_prompt"
	schema := schemas.ResourceSchemas[typeName]
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: promptImportID("imported", "production")})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
	}
	configValues := map[string]interface{}{
		"prompt_id": "imported", "environment": "production", "prompt_integration": "dotprompt",
		"dotprompt_content": "content", "prompt_type": "db",
		"ignore_prompt_manager_model": false, "ignore_prompt_manager_optional_params": false,
	}
	config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, configValues))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: read.NewState, ProposedNewState: read.NewState, PriorPrivate: read.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) || len(planned.RequiresReplace) != 0 || organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned) != organizationProjectProtocolActionNoOp {
		t.Fatalf("import plan: err=%v diagnostics=%v replace=%v action=%s", err, planned.Diagnostics, planned.RequiresReplace, organizationProjectProtocolPlannedAction(t, schema, read.NewState, planned))
	}
}

func TestPromptCompositeAndLegacyImportProtocol(t *testing.T) {
	ctx := context.Background()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, "http://127.0.0.1:1")
	const typeName = "litellm_prompt"
	schema := schemas.ResourceSchemas[typeName]
	for _, test := range []struct {
		name, importID, promptID, environment string
	}{
		{"composite", promptImportID("prompt:one", "production/east"), "prompt:one", "production/east"},
		{"legacy", "legacy-prompt", "legacy-prompt", defaultPromptEnvironment},
		{"escaped legacy", legacyPromptImportID("v1.YQ.Yg"), "v1.YQ.Yg", defaultPromptEnvironment},
	} {
		t.Run(test.name, func(t *testing.T) {
			imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: test.importID})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
				t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
			}
			attributes := protocolAttributeMap(t, schema, imported.ImportedResources[0].State)
			var promptID, environment, id string
			if err := attributes["prompt_id"].As(&promptID); err != nil {
				t.Fatal(err)
			}
			if err := attributes["environment"].As(&environment); err != nil {
				t.Fatal(err)
			}
			if err := attributes["id"].As(&id); err != nil {
				t.Fatal(err)
			}
			if promptID != test.promptID || id != test.promptID || environment != test.environment {
				t.Fatalf("state prompt=%q id=%q environment=%q", promptID, id, environment)
			}
		})
	}
}
