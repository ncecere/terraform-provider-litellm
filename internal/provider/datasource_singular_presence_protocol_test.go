package provider

import (
	"context"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

type singularPresenceCase struct {
	name             string
	typeName         string
	lookupName       string
	lookup           string
	path             string
	method           string
	body             func(string) string
	nullField        string
	emptyField       string
	emptyStringField string
	zeroField        string
	falseField       string
}

func singularPresenceCases() []singularPresenceCase {
	return []singularPresenceCase{
		{
			name: "access_group", typeName: "litellm_access_group", lookupName: "access_group", lookup: "presence-access", path: "/access_group/presence-access/info", method: http.MethodGet,
			body: func(mode string) string {
				switch mode {
				case "omit":
					return `{"access_group":"presence-access"}`
				case "null":
					return `{"access_group":"presence-access","model_names":null}`
				case "empty":
					return `{"access_group":"presence-access","model_names":[]}`
				case "malformed":
					return `{"access_group":"presence-access","model_names":["ok",1]}`
				case "mismatch":
					return `{"access_group":"other","model_names":[]}`
				}
				return ""
			},
			nullField: "model_names", emptyField: "model_names",
		},
		{
			name: "budget", typeName: "litellm_budget", lookupName: "budget_id", lookup: "presence-budget", path: "/budget/info", method: http.MethodPost,
			body: func(mode string) string {
				switch mode {
				case "omit":
					return `[{"budget_id":"presence-budget"}]`
				case "null":
					return `[{"budget_id":"presence-budget","max_budget":null,"model_max_budget":null}]`
				case "empty":
					return `[{"budget_id":"presence-budget","max_budget":0,"soft_budget":0,"max_parallel_requests":0,"tpm_limit":0,"rpm_limit":0,"budget_duration":"","budget_reset_at":"","model_max_budget":{}}]`
				case "malformed":
					return `[{"budget_id":"presence-budget","max_budget":"0"}]`
				case "mismatch":
					return `[{"budget_id":"other"}]`
				}
				return ""
			},
			nullField: "max_budget", emptyField: "model_max_budget", emptyStringField: "budget_duration", zeroField: "max_budget",
		},
		{
			name: "user", typeName: "litellm_user", lookupName: "user_id", lookup: "presence-user", path: "/user/info", method: http.MethodGet,
			body: func(mode string) string {
				switch mode {
				case "omit":
					return `{"user_info":{"user_id":"presence-user"}}`
				case "null":
					return `{"user_info":{"user_id":"presence-user","teams":null,"max_budget":null}}`
				case "empty":
					return `{"user_info":{"user_id":"presence-user","user_alias":"","user_email":"","user_role":"","teams":[],"models":[],"max_budget":0,"budget_duration":"","tpm_limit":0,"rpm_limit":0,"metadata":{},"spend":0}}`
				case "malformed":
					return `{"user_info":{"user_id":"presence-user","teams":["ok",1]}}`
				case "mismatch":
					return `{"user_info":{"user_id":"other"}}`
				}
				return ""
			},
			nullField: "teams", emptyField: "teams", emptyStringField: "user_alias", zeroField: "max_budget",
		},
		{
			name: "key", typeName: "litellm_key", lookupName: "key", lookup: "presence-key", path: "/key/info", method: http.MethodGet,
			body: func(mode string) string {
				switch mode {
				case "omit":
					return `{"key":"presence-key","info":{}}`
				case "null":
					return `{"key":"presence-key","info":{"models":null,"blocked":null}}`
				case "empty":
					return `{"key":"presence-key","info":{"key_alias":"","models":[],"max_budget":0,"spend":0,"user_id":"","team_id":"","project_id":"","max_parallel_requests":0,"tpm_limit":0,"rpm_limit":0,"budget_duration":"","litellm_budget_table":{"soft_budget":0},"metadata":{"tags":[]},"blocked":false,"router_settings":{}}}`
				case "malformed":
					return `{"key":"presence-key","info":{"models":["ok",1]}}`
				case "mismatch":
					return `{"key":"other","info":{}}`
				}
				return ""
			},
			nullField: "models", emptyField: "models", emptyStringField: "key_alias", zeroField: "max_budget", falseField: "blocked",
		},
		{
			name: "model", typeName: "litellm_model", lookupName: "model_id", lookup: "presence-model", path: "/model/info", method: http.MethodGet,
			body: func(mode string) string {
				switch mode {
				case "omit":
					return `{"data":[{"model_info":{"id":"presence-model"}}]}`
				case "null":
					return `{"data":[{"model_name":null,"litellm_params":null,"model_info":{"id":"presence-model","base_model":null}}]}`
				case "empty":
					return `{"data":[{"model_name":"","litellm_params":{"custom_llm_provider":"","api_base":"","api_version":"","tpm":0,"rpm":0,"aws_region_name":""},"model_info":{"id":"presence-model","base_model":"","tier":"","mode":"","team_id":"","team_public_model_name":""}}]}`
				case "malformed":
					return `{"data":[{"model_info":{"id":"presence-model"},"litellm_params":[]}]}`
				case "mismatch":
					return `{"data":[{"model_info":{"id":"other"}}]}`
				}
				return ""
			},
			nullField: "model_name", emptyField: "model_name", emptyStringField: "model_name", zeroField: "tpm",
		},
		{
			name: "search_tool", typeName: "litellm_search_tool", lookupName: "search_tool_id", lookup: "presence-search", path: "/search_tools/presence-search", method: http.MethodGet,
			body: func(mode string) string {
				switch mode {
				case "omit":
					return `{"search_tool_id":"presence-search","search_tool_name":"search","litellm_params":{"search_provider":"provider"}}`
				case "null":
					return `{"search_tool_id":"presence-search","search_tool_name":"search","litellm_params":{"search_provider":"provider","timeout":null},"search_tool_info":null}`
				case "empty":
					return `{"search_tool_id":"presence-search","search_tool_name":"search","litellm_params":{"search_provider":"provider","api_base":"","timeout":0,"max_retries":0},"search_tool_info":{}}`
				case "malformed":
					return `{"search_tool_id":"presence-search","search_tool_name":"search","litellm_params":{"search_provider":"provider","timeout":"0"}}`
				case "mismatch":
					return `{"search_tool_id":"other","search_tool_name":"search","litellm_params":{"search_provider":"provider"}}`
				}
				return ""
			},
			nullField: "timeout", emptyField: "search_tool_info", emptyStringField: "api_base", zeroField: "timeout",
		},
	}
}

func TestSingularDataSourcesResolveComputedPresenceProtocol(t *testing.T) {
	for _, test := range singularPresenceCases() {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var mode atomic.Value
			mode.Store("omit")
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path || request.Method != test.method {
					http.NotFound(writer, request)
					return
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(writer, test.body(mode.Load().(string)))
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.DataSourceSchemas[test.typeName]
			config := singularPresenceConfig(t, schema, map[string]interface{}{test.lookupName: test.lookup})

			for _, successMode := range []string{"omit", "null", "empty"} {
				t.Run(successMode, func(t *testing.T) {
					mode.Store(successMode)
					read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: test.typeName, Config: config})
					if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
						t.Fatalf("read: err=%v diagnostics=%v", err, read.Diagnostics)
					}
					assertDataSourceReadComputedKnown(t, schema, read)
					attributes := protocolAttributeMap(t, schema, read.State)
					if successMode != "empty" {
						if !attributes[test.nullField].IsNull() {
							t.Fatalf("%s should be typed null, got %v", test.nullField, attributes[test.nullField])
						}
					} else {
						if attributes[test.emptyField].IsNull() || !attributes[test.emptyField].IsKnown() {
							t.Fatalf("%s did not preserve explicit empty value: %v", test.emptyField, attributes[test.emptyField])
						}
						if test.emptyStringField != "" {
							var value string
							if err := attributes[test.emptyStringField].As(&value); err != nil || value != "" {
								t.Fatalf("%s did not preserve explicit empty string: value=%q err=%v", test.emptyStringField, value, err)
							}
						}
						if test.zeroField != "" {
							assertSingularPresenceZero(t, attributes[test.zeroField])
						}
						if test.falseField != "" {
							var value bool
							if err := attributes[test.falseField].As(&value); err != nil || value {
								t.Fatalf("%s did not preserve explicit false: value=%t err=%v", test.falseField, value, err)
							}
						}
					}
				})
			}
		})
	}
}

func TestSingularDataSourcesRejectInvalidResponsesWithoutStateProtocol(t *testing.T) {
	for _, test := range singularPresenceCases() {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var mode atomic.Value
			mode.Store("malformed")
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if mode.Load().(string) == "404" {
					writer.WriteHeader(http.StatusNotFound)
					_, _ = fmt.Fprint(writer, `{"error":{"message":"not found"}}`)
					return
				}
				_, _ = fmt.Fprint(writer, test.body(mode.Load().(string)))
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.DataSourceSchemas[test.typeName]
			config := singularPresenceConfig(t, schema, map[string]interface{}{test.lookupName: test.lookup})
			for _, failureMode := range []string{"malformed", "mismatch", "404"} {
				t.Run(failureMode, func(t *testing.T) {
					mode.Store(failureMode)
					readCtx := ctx
					var cancel context.CancelFunc
					if test.name == "model" && failureMode == "404" {
						readCtx, cancel = context.WithTimeout(ctx, 20*time.Millisecond)
						defer cancel()
					}
					read, err := protocolServer.ReadDataSource(readCtx, &tfprotov6.ReadDataSourceRequest{TypeName: test.typeName, Config: config})
					if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
						t.Fatalf("invalid response accepted: err=%v diagnostics=%v", err, read.Diagnostics)
					}
					assertSingularPresenceStateUnchanged(t, schema, config, read.State)
				})
			}
		})
	}
}

func TestSingularDataSourcesRejectEmptyRequiredLookupProtocol(t *testing.T) {
	ctx := context.Background()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests.Add(1) }))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	for _, test := range singularPresenceCases() {
		t.Run(test.name, func(t *testing.T) {
			schema := schemas.DataSourceSchemas[test.typeName]
			config := singularPresenceConfig(t, schema, map[string]interface{}{test.lookupName: ""})
			read, err := protocolServer.ReadDataSource(ctx, &tfprotov6.ReadDataSourceRequest{TypeName: test.typeName, Config: config})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("empty lookup accepted: err=%v diagnostics=%v state=%v", err, read.Diagnostics, read.State)
			}
			assertSingularPresenceStateUnchanged(t, schema, config, read.State)
		})
	}
	if requests.Load() != 0 {
		t.Fatalf("empty lookups made %d requests", requests.Load())
	}
}

func singularPresenceConfig(t *testing.T, schema *tfprotov6.Schema, configured map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	objectType := schema.ValueType().(tftypes.Object)
	computed := make(map[string]bool)
	for _, attribute := range schema.Block.Attributes {
		computed[attribute.Name] = attribute.Computed
	}
	values := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
	for name, attributeType := range objectType.AttributeTypes {
		value := interface{}(nil)
		if computed[name] {
			value = tftypes.UnknownValue
		}
		if configuredValue, ok := configured[name]; ok {
			value = configuredValue
		}
		values[name] = tftypes.NewValue(attributeType, value)
	}
	dynamic, err := tfprotov6.NewDynamicValue(schema.ValueType(), tftypes.NewValue(objectType, values))
	if err != nil {
		t.Fatal(err)
	}
	return &dynamic
}

func assertSingularPresenceStateUnchanged(t *testing.T, schema *tfprotov6.Schema, config, state *tfprotov6.DynamicValue) {
	t.Helper()
	if state == nil {
		return
	}
	configured, err := config.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	published, err := state.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	if !published.Equal(configured) {
		differences, _ := published.Diff(configured)
		t.Fatalf("failed Read published partial state: %v", differences)
	}
}

func assertSingularPresenceZero(t *testing.T, value tftypes.Value) {
	t.Helper()
	if value.IsNull() || !value.IsKnown() {
		t.Fatalf("zero value was not known: %v", value)
	}
	var number big.Float
	if err := value.As(&number); err != nil {
		t.Fatalf("decode zero value: %v", err)
	}
	if number.Sign() != 0 {
		t.Fatalf("value = %s, want zero", number.String())
	}
}
