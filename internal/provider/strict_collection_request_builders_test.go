package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestMigratedRequestBuildersRejectMalformedCollectionsWithoutPartialPayloads(t *testing.T) {
	t.Parallel()

	unknownList := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("known"), types.StringUnknown()})
	nullList := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("known"), types.StringNull()})
	unknownMap := types.MapValueMust(types.StringType, map[string]attr.Value{"sensitive-key": types.StringUnknown()})
	nullMap := types.MapValueMust(types.StringType, map[string]attr.Value{"ordinary-key": types.StringNull()})
	wrongList := types.ListValueMust(types.DynamicType, []attr.Value{types.DynamicValue(types.Int64Value(42))})

	tests := []struct {
		name     string
		build    func() (map[string]interface{}, diag.Diagnostics)
		wantPath string
	}{
		{
			name: "fallback unknown list element",
			build: func() (map[string]interface{}, diag.Diagnostics) {
				return (&FallbackResource{}).buildFallbackRequest(context.Background(), &FallbackResourceModel{
					Model: types.StringValue("primary"), FallbackModels: unknownList, FallbackType: types.StringValue("general"),
				})
			},
			wantPath: `fallback_models[1]`,
		},
		{
			name: "fallback wrong element type",
			build: func() (map[string]interface{}, diag.Diagnostics) {
				return (&FallbackResource{}).buildFallbackRequest(context.Background(), &FallbackResourceModel{
					Model: types.StringValue("primary"), FallbackModels: wrongList, FallbackType: types.StringValue("general"),
				})
			},
			wantPath: "fallback_models",
		},
		{
			name: "user null list element",
			build: func() (map[string]interface{}, diag.Diagnostics) {
				return (&UserResource{}).buildUserRequest(context.Background(), &UserResourceModel{Teams: nullList})
			},
			wantPath: `teams[1]`,
		},
		{
			name: "vector store ordinary metadata null element",
			build: func() (map[string]interface{}, diag.Diagnostics) {
				return (&VectorStoreResource{}).buildVectorStoreRequest(context.Background(), &VectorStoreResourceModel{
					VectorStoreName: types.StringValue("store"), CustomLLMProvider: types.StringValue("bedrock"),
					VectorStoreMetadata: nullMap, LiteLLMParams: types.MapNull(types.StringType),
				})
			},
			wantPath: `vector_store_metadata["ordinary-key"]`,
		},
		{
			name: "vector store sensitive params unknown element stays root only",
			build: func() (map[string]interface{}, diag.Diagnostics) {
				return (&VectorStoreResource{}).buildVectorStoreRequest(context.Background(), &VectorStoreResourceModel{
					VectorStoreName: types.StringValue("store"), CustomLLMProvider: types.StringValue("bedrock"),
					VectorStoreMetadata: types.MapNull(types.StringType), LiteLLMParams: unknownMap,
				})
			},
			wantPath: "litellm_params",
		},
		{
			name: "unified access group unknown list element",
			build: func() (map[string]interface{}, diag.Diagnostics) {
				return buildUnifiedAccessGroupRequest(context.Background(), &UnifiedAccessGroupResourceModel{
					AccessGroupName: types.StringValue("group"), AccessModelNames: unknownList,
				}, false)
			},
			wantPath: `access_model_names[1]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, diagnostics := test.build()
			if request != nil {
				t.Fatalf("malformed collection produced a partial request: %#v", request)
			}
			assertCollectionDiagnostics(t, diagnostics, 1, test.wantPath)
		})
	}
}

func TestMigratedRequestBuildersHonorCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value := types.ListValueMust(types.StringType, []attr.Value{types.StringValue("known")})
	request, diagnostics := (&FallbackResource{}).buildFallbackRequest(ctx, &FallbackResourceModel{
		Model: types.StringValue("primary"), FallbackModels: value, FallbackType: types.StringValue("general"),
	})
	if request != nil {
		t.Fatalf("canceled conversion produced a request: %#v", request)
	}
	assertCollectionDiagnostics(t, diagnostics, 1, "fallback_models")
}

func TestMigratedCollectionLifecycleRejectsUnknownElementsBeforeHTTPProtocol(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	unknownString := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	unknownList := []tftypes.Value{tftypes.NewValue(tftypes.String, "configured-secret-value"), unknownString}
	unknownMap := map[string]tftypes.Value{"sensitive-key": unknownString}

	tests := []struct {
		name        string
		typeName    string
		config      map[string]interface{}
		prior       map[string]interface{}
		plannedOnly map[string]interface{}
	}{
		{
			name: "fallback", typeName: "litellm_fallback",
			config: map[string]interface{}{"model": "primary", "fallback_type": "general", "fallback_models": unknownList},
			prior:  map[string]interface{}{"id": "primary:general", "model": "primary", "fallback_type": "general", "fallback_models": []tftypes.Value{tftypes.NewValue(tftypes.String, "old")}},
		},
		{
			name: "user", typeName: "litellm_user",
			config: map[string]interface{}{"user_id": "user-1", "models": unknownList, "auto_create_key": false},
			prior:  map[string]interface{}{"id": "user-1", "user_id": "user-1", "models": []tftypes.Value{tftypes.NewValue(tftypes.String, "old")}, "auto_create_key": false},
		},
		{
			name: "vector store", typeName: "litellm_vector_store",
			config: map[string]interface{}{"vector_store_name": "store", "custom_llm_provider": "bedrock", "vector_store_metadata": unknownMap},
			prior: map[string]interface{}{
				"id": "vs-1", "vector_store_id": "vs-1", "vector_store_name": "old", "custom_llm_provider": "bedrock",
				"vector_store_metadata": map[string]tftypes.Value{"old": tftypes.NewValue(tftypes.String, "value")},
			},
		},
		{
			name: "unified access group", typeName: "litellm_unified_access_group",
			config: map[string]interface{}{"access_group_name": "group", "access_model_names": unknownList},
			prior:  map[string]interface{}{"id": "group-1", "access_group_id": "group-1", "access_group_name": "old", "access_model_names": []tftypes.Value{tftypes.NewValue(tftypes.String, "old")}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schema := schemas.ResourceSchemas[test.typeName]
			config := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, test.config))
			plannedValues := make(map[string]interface{}, len(test.config)+len(test.plannedOnly))
			for name, value := range test.config {
				plannedValues[name] = value
			}
			for name, value := range test.plannedOnly {
				plannedValues[name] = value
			}
			planned := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, plannedValues))
			nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))

			before := requests.Load()
			created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: test.typeName, Config: config, PriorState: nullState, PlannedState: planned,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
				t.Fatalf("create invalid collection: err=%v diagnostics=%v", err, created.Diagnostics)
			}
			if requests.Load() != before {
				t.Fatalf("create dispatched HTTP for an unknown element: before=%d after=%d", before, requests.Load())
			}
			assertProtocolCollectionDiagnosticIsPathAwareAndContentSafe(t, created.Diagnostics)

			prior := accessGroupProtocolDynamicValue(t, schema, organizationProjectProtocolValue(t, schema, test.prior))
			before = requests.Load()
			updated, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
				TypeName: test.typeName, Config: config, PriorState: prior, PlannedState: planned,
			})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(updated.Diagnostics) {
				t.Fatalf("update invalid collection: err=%v diagnostics=%v", err, updated.Diagnostics)
			}
			if requests.Load() != before {
				t.Fatalf("update dispatched HTTP for an unknown element: before=%d after=%d", before, requests.Load())
			}
			assertProtocolCollectionDiagnosticIsPathAwareAndContentSafe(t, updated.Diagnostics)
		})
	}
}

func assertProtocolCollectionDiagnosticIsPathAwareAndContentSafe(t *testing.T, diagnostics []*tfprotov6.Diagnostic) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity != tfprotov6.DiagnosticSeverityError {
			continue
		}
		if diagnostic.Attribute == nil {
			t.Fatalf("collection diagnostic was not path-aware: %v", diagnostics)
		}
		text := diagnostic.Summary + " " + diagnostic.Detail
		for _, forbidden := range []string{"configured-secret-value", "sensitive-key"} {
			if strings.Contains(text, forbidden) {
				t.Fatalf("collection diagnostic disclosed %q: %q", forbidden, text)
			}
		}
		return
	}
	t.Fatalf("no error diagnostic found: %v", diagnostics)
}
