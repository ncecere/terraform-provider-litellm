package provider

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func protocolPrivateHasKey(t *testing.T, private []byte, key string) bool {
	t.Helper()
	if len(private) == 0 {
		return false
	}
	values := map[string]json.RawMessage{}
	if err := json.Unmarshal(private, &values); err != nil {
		t.Fatalf("decode protocol private state: %v", err)
	}
	_, exists := values[key]
	return exists
}

func configuredImportProtocolServer(t *testing.T, ctx context.Context, apiBase string) (tfprotov6.ProviderServer, *tfprotov6.GetProviderSchemaResponse) {
	t.Helper()
	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemas, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemas.Diagnostics) {
		t.Fatalf("schema: %v, %v", err, schemas.Diagnostics)
	}
	providerValue, err := tftypes.ValueFromJSON(
		[]byte(`{"api_base":"`+apiBase+`","api_key":"test-key","insecure_skip_verify":null,"litellm_changed_by":null}`),
		schemas.Provider.ValueType(),
	)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := protocolServer.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		Config: accessGroupProtocolDynamicValue(t, schemas.Provider, providerValue),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configured.Diagnostics) {
		t.Fatalf("configure: %v, %v", err, configured.Diagnostics)
	}
	return protocolServer, schemas
}

func TestModelImportPartialResponsesRetainStateUntilCompleteAdoption(t *testing.T) {
	ctx := context.Background()
	responses := []struct {
		name string
		body string
	}{
		{"empty data", `{"data":[]}`},
		{"multiple models", `{"data":[{"model_name":"imported","litellm_params":{"custom_llm_provider":"anthropic"},"model_info":{"id":"model-import","base_model":"claude"}},{"model_name":"other","litellm_params":{"custom_llm_provider":"anthropic"},"model_info":{"id":"other","base_model":"claude"}}]}`},
		{"invalid model object", `{"data":["model-import"]}`},
		{"empty identity", `{"data":[{"model_name":"imported","litellm_params":{"custom_llm_provider":"anthropic"},"model_info":{"id":"","base_model":"claude"}}]}`},
		{"wrong identity", `{"data":[{"model_name":"imported","litellm_params":{"custom_llm_provider":"anthropic"},"model_info":{"id":"other","base_model":"claude"}}]}`},
		{"empty model name", `{"data":[{"model_name":"","litellm_params":{"custom_llm_provider":"anthropic"},"model_info":{"id":"model-import","base_model":"claude"}}]}`},
		{"missing params object", `{"data":[{"model_name":"imported","model_info":{"id":"model-import","base_model":"claude"}}]}`},
		{"empty provider", `{"data":[{"model_name":"imported","litellm_params":{"custom_llm_provider":""},"model_info":{"id":"model-import","base_model":"claude"}}]}`},
		{"empty base model", `{"data":[{"model_name":"imported","litellm_params":{"custom_llm_provider":"anthropic"},"model_info":{"id":"model-import","base_model":""}}]}`},
	}
	complete := `{"data":[{"model_name":"imported","litellm_params":{"custom_llm_provider":"anthropic","model":"anthropic/claude"},"model_info":{"id":"model-import","base_model":"claude"}}]}`
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		index := int(reads.Add(1)) - 1
		if index < len(responses) {
			_, _ = writer.Write([]byte(responses[index].body))
			return
		}
		_, _ = writer.Write([]byte(complete))
	}))
	defer server.Close()

	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemas, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemas.Diagnostics) {
		t.Fatalf("schema: %v, %v", err, schemas.Diagnostics)
	}
	providerValue, err := tftypes.ValueFromJSON(
		[]byte(`{"api_base":"`+server.URL+`","api_key":"test-key","insecure_skip_verify":null,"litellm_changed_by":null}`),
		schemas.Provider.ValueType(),
	)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := protocolServer.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{Config: accessGroupProtocolDynamicValue(t, schemas.Provider, providerValue)})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configured.Diagnostics) {
		t.Fatalf("configure: %v, %v", err, configured.Diagnostics)
	}
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_model", ID: "model-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: %v, %v", err, imported.Diagnostics)
	}

	modelSchema := schemas.ResourceSchemas["litellm_model"]
	currentState := imported.ImportedResources[0].State
	private := imported.ImportedResources[0].Private
	for _, partial := range responses {
		response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_model", CurrentState: currentState, Private: private})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
			t.Fatalf("%s response was accepted: %v, %v", partial.name, err, response.Diagnostics)
		}
		if !protocolPrivateHasKey(t, response.Private, modelImportedPrivateKey) {
			t.Fatalf("%s response cleared the import marker", partial.name)
		}
		before, _ := currentState.Unmarshal(modelSchema.ValueType())
		after, _ := response.NewState.Unmarshal(modelSchema.ValueType())
		if !before.Equal(after) {
			t.Fatalf("%s response changed prior state", partial.name)
		}
		currentState, private = response.NewState, response.Private
	}

	adopted, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_model", CurrentState: currentState, Private: private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(adopted.Diagnostics) {
		t.Fatalf("complete read: %v, %v", err, adopted.Diagnostics)
	}
	if protocolPrivateHasKey(t, adopted.Private, modelImportedPrivateKey) {
		t.Fatalf("complete read retained marker: %x", adopted.Private)
	}
	attributes := protocolAttributeMap(t, modelSchema, adopted.NewState)
	for field, want := range map[string]string{"id": "model-import", "model_name": "imported", "custom_llm_provider": "anthropic", "base_model": "claude"} {
		var got string
		if err := attributes[field].As(&got); err != nil || got != want {
			t.Fatalf("adopted %s = %q, want %q (error %v)", field, got, want, err)
		}
	}
}

func TestOrganizationImportAcceptsFlatAndNestedAuthoritativeEnvelopes(t *testing.T) {
	tests := []struct {
		name      string
		responses []string
	}{
		{
			name: "flat v1.98 object",
			responses: []string{
				"",
				`{}`,
				`{"organization_id":"wrong-organization","organization_alias":"wrong"}`,
				`{"organization_id":"org-import"}`,
				`{"organization_id":"org-import","organization_alias":"imported","litellm_budget_table":{"tpm_limit":9007199254740993}}`,
			},
		},
		{
			name: "nested organization_info object",
			responses: []string{
				"",
				`{}`,
				`{"organization_info":{"organization_id":"wrong-organization","organization_alias":"wrong"}}`,
				`{"organization_info":{"organization_id":"org-import"}}`,
				`{"organization_info":{"organization_id":"org-import","organization_alias":"imported","litellm_budget_table":{"tpm_limit":9007199254740993}}}`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var reads atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				index := int(reads.Add(1)) - 1
				if index >= len(test.responses) {
					index = len(test.responses) - 1
				}
				_, _ = writer.Write([]byte(test.responses[index]))
			}))
			defer server.Close()

			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{
				TypeName: "litellm_organization",
				ID:       "org-import",
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
				t.Fatalf("import: %v, %v", err, imported.Diagnostics)
			}

			currentState := imported.ImportedResources[0].State
			private := imported.ImportedResources[0].Private
			schema := schemas.ResourceSchemas["litellm_organization"]
			for index := 0; index < len(test.responses)-1; index++ {
				response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
					TypeName:     "litellm_organization",
					CurrentState: currentState,
					Private:      private,
				})
				if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
					t.Fatalf("invalid response %d was accepted: %v, %v", index, err, response.Diagnostics)
				}
				if !protocolPrivateHasKey(t, response.Private, numericImportedPrivateKey) {
					t.Fatalf("invalid response %d cleared the import marker", index)
				}
				before, _ := currentState.Unmarshal(schema.ValueType())
				after, _ := response.NewState.Unmarshal(schema.ValueType())
				if !before.Equal(after) {
					t.Fatalf("invalid response %d changed prior state", index)
				}
				currentState, private = response.NewState, response.Private
			}

			adopted, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
				TypeName:     "litellm_organization",
				CurrentState: currentState,
				Private:      private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(adopted.Diagnostics) {
				t.Fatalf("authoritative response: %v, %v", err, adopted.Diagnostics)
			}
			if protocolPrivateHasKey(t, adopted.Private, numericImportedPrivateKey) {
				t.Fatal("authoritative response retained the import marker")
			}
			attributes := protocolAttributeMap(t, schema, adopted.NewState)
			if got := protocolInt64(t, attributes["tpm_limit"]); got != 9007199254740993 {
				t.Fatalf("exact imported TPM = %d", got)
			}
			steady, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
				TypeName:     "litellm_organization",
				CurrentState: adopted.NewState,
				Private:      adopted.Private,
			})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) {
				t.Fatalf("steady read: %v, %v", err, steady.Diagnostics)
			}
			adoptedValue, _ := adopted.NewState.Unmarshal(schema.ValueType())
			steadyValue, _ := steady.NewState.Unmarshal(schema.ValueType())
			if !adoptedValue.Equal(steadyValue) {
				t.Fatal("steady read drifted after import adoption")
			}
		})
	}
}

func TestModelImportAcceptsSingleObjectDataEnvelope(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":{"model_name":"imported","litellm_params":{"custom_llm_provider":"anthropic","model":"anthropic/claude","tpm":9007199254740993},"model_info":{"id":"model-import","base_model":"claude"}}}`))
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_model", ID: "model-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: %v, %v", err, imported.Diagnostics)
	}
	resourceState := imported.ImportedResources[0]
	adopted, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_model",
		CurrentState: resourceState.State,
		Private:      resourceState.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(adopted.Diagnostics) {
		t.Fatalf("single-object read: %v, %v", err, adopted.Diagnostics)
	}
	if protocolPrivateHasKey(t, adopted.Private, modelImportedPrivateKey) {
		t.Fatal("single-object response retained the import marker")
	}
	schema := schemas.ResourceSchemas["litellm_model"]
	attributes := protocolAttributeMap(t, schema, adopted.NewState)
	if got := protocolInt64(t, attributes["tpm"]); got != 9007199254740993 {
		t.Fatalf("exact imported TPM = %d", got)
	}
	steady, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_model",
		CurrentState: adopted.NewState,
		Private:      adopted.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) {
		t.Fatalf("steady single-object read: %v, %v", err, steady.Diagnostics)
	}
	adoptedValue, _ := adopted.NewState.Unmarshal(schema.ValueType())
	steadyValue, _ := steady.NewState.Unmarshal(schema.ValueType())
	if !adoptedValue.Equal(steadyValue) {
		t.Fatal("steady single-object read drifted after import adoption")
	}
}

func TestMCPServerImportAcceptsCollectionFallbackEnvelope(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/mcp/server/mcp-import":
			http.Error(writer, `{"error":"individual read unavailable"}`, http.StatusInternalServerError)
		case "/v1/mcp/server":
			_, _ = writer.Write([]byte(`[{"server_id":"mcp-import","server_name":"imported","url":"https://mcp.example.test","transport":"http","mcp_info":{"mcp_server_cost_info":{"default_cost_per_query":0.125}}}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: "litellm_mcp_server", ID: "mcp-import"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: %v, %v", err, imported.Diagnostics)
	}
	resourceState := imported.ImportedResources[0]
	adopted, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_mcp_server",
		CurrentState: resourceState.State,
		Private:      resourceState.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(adopted.Diagnostics) {
		t.Fatalf("collection fallback read: %v, %v", err, adopted.Diagnostics)
	}
	if protocolPrivateHasKey(t, adopted.Private, numericImportedPrivateKey) {
		t.Fatal("collection fallback retained the import marker")
	}
	attributes := protocolAttributeMap(t, schemas.ResourceSchemas["litellm_mcp_server"], adopted.NewState)
	costInfo := protocolNestedAttribute(t, attributes["mcp_info"], "mcp_server_cost_info")
	costs := map[string]tftypes.Value{}
	if err := costInfo.As(&costs); err != nil {
		t.Fatal(err)
	}
	var cost big.Float
	if err := costs["default_cost_per_query"].As(&cost); err != nil {
		t.Fatal(err)
	}
	value, _ := cost.Float64()
	if value != 0.125 {
		t.Fatalf("default MCP cost = %v", value)
	}
	steady, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_mcp_server",
		CurrentState: adopted.NewState,
		Private:      adopted.Private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(steady.Diagnostics) {
		t.Fatalf("steady collection fallback read: %v, %v", err, steady.Diagnostics)
	}
	schema := schemas.ResourceSchemas["litellm_mcp_server"]
	adoptedValue, _ := adopted.NewState.Unmarshal(schema.ValueType())
	steadyValue, _ := steady.NewState.Unmarshal(schema.ValueType())
	if !adoptedValue.Equal(steadyValue) {
		t.Fatal("steady collection fallback read drifted after import adoption")
	}
}

func TestImportMarkerSurvivesEmptyNullAndWrongIdentityBeforeValidRead(t *testing.T) {
	ctx := context.Background()
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch reads.Add(1) {
		case 1:
			// A 2xx empty body is not an authoritative resource response.
		case 2:
			_, _ = writer.Write([]byte("null"))
		case 3:
			_, _ = writer.Write([]byte(`{"agent_id":"wrong-agent","agent_name":"wrong","tpm_limit":1}`))
		default:
			_, _ = writer.Write([]byte(`{"agent_id":"agent-import","agent_name":"imported","tpm_limit":9007199254740993}`))
		}
	}))
	defer server.Close()

	protocolServer := providerserver.NewProtocol6(New("test")())()
	schemas, err := protocolServer.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(schemas.Diagnostics) {
		t.Fatalf("schema: %v, %v", err, schemas.Diagnostics)
	}
	providerValue, err := tftypes.ValueFromJSON(
		[]byte(`{"api_base":"`+server.URL+`","api_key":"test-key","insecure_skip_verify":null,"litellm_changed_by":null}`),
		schemas.Provider.ValueType(),
	)
	if err != nil {
		t.Fatal(err)
	}
	configured, err := protocolServer.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		Config: accessGroupProtocolDynamicValue(t, schemas.Provider, providerValue),
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(configured.Diagnostics) {
		t.Fatalf("configure: %v, %v", err, configured.Diagnostics)
	}
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{
		TypeName: "litellm_agent",
		ID:       "agent-import",
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: %v, %v", err, imported.Diagnostics)
	}

	resourceState := imported.ImportedResources[0]
	currentState := resourceState.State
	private := resourceState.Private
	schema := schemas.ResourceSchemas["litellm_agent"]
	for _, failure := range []string{"empty", "null", "wrong identity"} {
		response, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
			TypeName:     "litellm_agent",
			CurrentState: currentState,
			Private:      private,
		})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(response.Diagnostics) {
			t.Fatalf("%s first-read response was accepted: %v, %v", failure, err, response.Diagnostics)
		}
		if !protocolPrivateHasKey(t, response.Private, numericImportedPrivateKey) {
			t.Fatalf("%s first-read response cleared the import marker", failure)
		}
		before, _ := currentState.Unmarshal(schema.ValueType())
		after, _ := response.NewState.Unmarshal(schema.ValueType())
		if !before.Equal(after) {
			t.Fatalf("%s first-read response changed prior state", failure)
		}
		currentState = response.NewState
		private = response.Private
	}

	valid, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{
		TypeName:     "litellm_agent",
		CurrentState: currentState,
		Private:      private,
	})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(valid.Diagnostics) {
		t.Fatalf("valid read: %v, %v", err, valid.Diagnostics)
	}
	if protocolPrivateHasKey(t, valid.Private, numericImportedPrivateKey) {
		t.Fatalf("authoritative read retained marker: %x", valid.Private)
	}
	attributes := protocolAttributeMap(t, schema, valid.NewState)
	if got := protocolInt64(t, attributes["tpm_limit"]); got != 9007199254740993 {
		t.Fatalf("exact imported TPM = %d", got)
	}
}
