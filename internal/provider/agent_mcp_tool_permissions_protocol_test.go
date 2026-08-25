package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func agentProtocolValue(t *testing.T, valueType tftypes.Type, configured interface{}) tftypes.Value {
	t.Helper()
	if configured == tftypes.UnknownValue {
		return tftypes.NewValue(valueType, tftypes.UnknownValue)
	}
	if configured == nil {
		return tftypes.NewValue(valueType, nil)
	}
	switch typed := valueType.(type) {
	case tftypes.Object:
		values, ok := configured.(map[string]interface{})
		if !ok {
			t.Fatalf("object configuration = %T", configured)
		}
		attributes := make(map[string]tftypes.Value, len(typed.AttributeTypes))
		for name, attributeType := range typed.AttributeTypes {
			attributes[name] = agentProtocolValue(t, attributeType, values[name])
		}
		return tftypes.NewValue(typed, attributes)
	case tftypes.Map:
		values, ok := configured.(map[string]interface{})
		if !ok {
			t.Fatalf("map configuration = %T", configured)
		}
		elements := make(map[string]tftypes.Value, len(values))
		for key, value := range values {
			elements[key] = agentProtocolValue(t, typed.ElementType, value)
		}
		return tftypes.NewValue(typed, elements)
	case tftypes.List:
		values, ok := configured.([]interface{})
		if !ok {
			t.Fatalf("list configuration = %T", configured)
		}
		elements := make([]tftypes.Value, len(values))
		for index, value := range values {
			elements[index] = agentProtocolValue(t, typed.ElementType, value)
		}
		return tftypes.NewValue(typed, elements)
	case tftypes.Set:
		values, ok := configured.([]interface{})
		if !ok {
			t.Fatalf("set configuration = %T", configured)
		}
		elements := make([]tftypes.Value, len(values))
		for index, value := range values {
			elements[index] = agentProtocolValue(t, typed.ElementType, value)
		}
		return tftypes.NewValue(typed, elements)
	default:
		return tftypes.NewValue(valueType, configured)
	}
}

func agentProtocolDynamicValue(t *testing.T, schema *tfprotov6.Schema, configured map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	value := agentProtocolValue(t, schema.ValueType(), configured)
	return accessGroupProtocolDynamicValue(t, schema, value)
}

func agentProtocolReplaceObjectPermission(t *testing.T, schema *tfprotov6.Schema, current *tfprotov6.DynamicValue, configured map[string]interface{}) *tfprotov6.DynamicValue {
	t.Helper()
	value, err := current.Unmarshal(schema.ValueType())
	if err != nil {
		t.Fatal(err)
	}
	attributes := map[string]tftypes.Value{}
	if err := value.As(&attributes); err != nil {
		t.Fatal(err)
	}
	objectType := schema.ValueType().(tftypes.Object).AttributeTypes["object_permission"]
	attributes["object_permission"] = agentProtocolValue(t, objectType, configured)
	return accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), attributes))
}

func agentProtocolDiagnosticsText(diagnostics []*tfprotov6.Diagnostic) string {
	var result strings.Builder
	for _, diagnostic := range diagnostics {
		result.WriteString(diagnostic.Summary)
		result.WriteString(": ")
		result.WriteString(diagnostic.Detail)
		result.WriteByte('\n')
	}
	return result.String()
}

func agentProtocolObjectPermissionAttributes(t *testing.T, schema *tfprotov6.Schema, state *tfprotov6.DynamicValue) map[string]tftypes.Value {
	t.Helper()
	attributes := protocolAttributeMap(t, schema, state)
	var permissionAttributes map[string]tftypes.Value
	if err := attributes["object_permission"].As(&permissionAttributes); err != nil {
		t.Fatal(err)
	}
	return permissionAttributes
}

func agentProtocolToolPermissionsValue(t *testing.T, schema *tfprotov6.Schema, state *tfprotov6.DynamicValue) tftypes.Value {
	t.Helper()
	return agentProtocolObjectPermissionAttributes(t, schema, state)["mcp_tool_permissions"]
}

func agentProtocolToolPermissions(t *testing.T, schema *tfprotov6.Schema, state *tfprotov6.DynamicValue) map[string]string {
	t.Helper()
	var permissionValues map[string]tftypes.Value
	if err := agentProtocolToolPermissionsValue(t, schema, state).As(&permissionValues); err != nil {
		t.Fatal(err)
	}
	result := make(map[string]string, len(permissionValues))
	for key, value := range permissionValues {
		var decoded string
		if err := value.As(&decoded); err != nil {
			t.Fatal(err)
		}
		result[key] = decoded
	}
	return result
}

func TestAgentMCPToolPermissionsProtocolValidation(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_agent"]
	const secret = "sentinel-protocol-secret"

	for _, invalid := range []string{`{}`, `null`, `[1]`, `["ok",{"secret":"` + secret + `"}]`, `["unterminated`} {
		config := agentProtocolDynamicValue(t, schema, map[string]interface{}{
			"agent_name": "agent",
			"agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"},
			"object_permission": map[string]interface{}{
				"mcp_tool_permissions": map[string]interface{}{"sentinel-server": invalid},
			},
		})
		validated, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: "litellm_agent", Config: config})
		if err != nil || !accessGroupProtocolDiagnosticsHaveError(validated.Diagnostics) {
			t.Fatalf("invalid value accepted: err=%v diagnostics=%v", err, validated.Diagnostics)
		}
		diagnostics := agentProtocolDiagnosticsText(validated.Diagnostics)
		for _, forbidden := range []string{secret, "sentinel-server", invalid, server.URL} {
			if strings.Contains(diagnostics, forbidden) {
				t.Fatal("validation diagnostics leaked protected content")
			}
		}
	}

	for _, valid := range []string{`[]`, `["one"]`, ` [ "one", "two" ] `} {
		config := agentProtocolDynamicValue(t, schema, map[string]interface{}{
			"agent_name": "agent",
			"agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"},
			"object_permission": map[string]interface{}{
				"mcp_tool_permissions": map[string]interface{}{"server": valid},
			},
		})
		validated, err := protocolServer.ValidateResourceConfig(ctx, &tfprotov6.ValidateResourceConfigRequest{TypeName: "litellm_agent", Config: config})
		if err != nil || accessGroupProtocolDiagnosticsHaveError(validated.Diagnostics) {
			t.Fatalf("valid value rejected: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(validated.Diagnostics))
		}
	}
}

func TestAgentMCPToolPermissionsProtocolRequestReadImportAndClear(t *testing.T) {
	ctx := context.Background()
	var mutex sync.Mutex
	remote := map[string]interface{}{}
	var mutationPayloads []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/agents":
			body, _ := io.ReadAll(request.Body)
			var payload map[string]interface{}
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Error(err)
			}
			mutex.Lock()
			mutationPayloads = append(mutationPayloads, payload)
			remote = payload["object_permission"].(map[string]interface{})["mcp_tool_permissions"].(map[string]interface{})
			mutex.Unlock()
			_, _ = fmt.Fprint(writer, `{"agent_id":"agent-permissions"}`)
		case request.Method == http.MethodPut && request.URL.Path == "/v1/agents/agent-permissions":
			body, _ := io.ReadAll(request.Body)
			var payload map[string]interface{}
			if err := decodeJSONUseNumber(body, &payload); err != nil {
				t.Error(err)
			}
			mutex.Lock()
			mutationPayloads = append(mutationPayloads, payload)
			remote = payload["object_permission"].(map[string]interface{})["mcp_tool_permissions"].(map[string]interface{})
			mutex.Unlock()
			_, _ = fmt.Fprint(writer, `{}`)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/agents/agent-permissions":
			mutex.Lock()
			permissionCopy := remote
			mutex.Unlock()
			_ = json.NewEncoder(writer).Encode(map[string]interface{}{
				"agent_id": "agent-permissions", "agent_name": "agent", "object_permission": map[string]interface{}{"mcp_tool_permissions": permissionCopy},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_agent"
	schema := schemas.ResourceSchemas[typeName]
	configuredSpelling := ` [ "first", "second" ] `
	configValues := map[string]interface{}{
		"agent_name": "agent",
		"agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"},
		"object_permission": map[string]interface{}{
			"mcp_tool_permissions": map[string]interface{}{"server-a": configuredSpelling, "server-empty": `[]`},
		},
	}
	config := agentProtocolDynamicValue(t, schema, configValues)
	proposedValues := map[string]interface{}{
		"id": tftypes.UnknownValue, "agent_name": "agent",
		"agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"},
		"object_permission": map[string]interface{}{
			"mcp_tool_permissions": map[string]interface{}{"server-a": configuredSpelling, "server-empty": `[]`},
		},
		"litellm_params": tftypes.UnknownValue, "static_headers": tftypes.UnknownValue, "extra_headers": tftypes.UnknownValue,
		"created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue, "created_by": tftypes.UnknownValue, "updated_by": tftypes.UnknownValue,
	}
	proposed := agentProtocolDynamicValue(t, schema, proposedValues)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("create plan: err=%v diagnostics=%v", err, planned.Diagnostics)
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("create: err=%v diagnostics=%v", err, created.Diagnostics)
	}
	stored := agentProtocolToolPermissions(t, schema, created.NewState)
	if stored["server-a"] != configuredSpelling || stored["server-empty"] != `[]` {
		t.Fatalf("configured spelling drifted: %#v", stored)
	}
	mutex.Lock()
	createPayload := mutationPayloads[0]
	mutex.Unlock()
	wire := createPayload["object_permission"].(map[string]interface{})["mcp_tool_permissions"].(map[string]interface{})
	if !reflect.DeepEqual(wire["server-a"], []interface{}{"first", "second"}) || !reflect.DeepEqual(wire["server-empty"], []interface{}{}) {
		t.Fatalf("create wire values were not arrays: %#v", wire)
	}

	clearConfigValues := map[string]interface{}{
		"agent_name":        "agent",
		"agent_card":        map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"},
		"object_permission": map[string]interface{}{"mcp_tool_permissions": map[string]interface{}{}},
	}
	clearConfig := agentProtocolDynamicValue(t, schema, clearConfigValues)
	clearProposed := agentProtocolReplaceObjectPermission(t, schema, created.NewState, clearConfigValues["object_permission"].(map[string]interface{}))
	clearPlan, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: clearConfig, PriorState: created.NewState, ProposedNewState: clearProposed, PriorPrivate: created.Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(clearPlan.Diagnostics) {
		t.Fatalf("clear plan: err=%v diagnostics=%v", err, clearPlan.Diagnostics)
	}
	cleared, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: clearConfig, PriorState: created.NewState, PlannedState: clearPlan.PlannedState, PlannedPrivate: clearPlan.PlannedPrivate})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(cleared.Diagnostics) {
		t.Fatalf("clear: err=%v diagnostics=%v", err, cleared.Diagnostics)
	}
	if got := agentProtocolToolPermissions(t, schema, cleared.NewState); len(got) != 0 {
		t.Fatalf("clear state = %#v", got)
	}
	mutex.Lock()
	clearPayload := mutationPayloads[len(mutationPayloads)-1]
	mutex.Unlock()
	clearWire, present := clearPayload["object_permission"].(map[string]interface{})["mcp_tool_permissions"].(map[string]interface{})
	if !present || len(clearWire) != 0 {
		t.Fatalf("explicit empty-map clear omitted from request: %#v", clearPayload)
	}

	mutex.Lock()
	remote = map[string]interface{}{"import-server": []interface{}{"zeta", "alpha"}, "empty": []interface{}{}}
	mutex.Unlock()
	imported, err := protocolServer.ImportResourceState(ctx, &tfprotov6.ImportResourceStateRequest{TypeName: typeName, ID: "agent-permissions"})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(imported.Diagnostics) || len(imported.ImportedResources) != 1 {
		t.Fatalf("import: err=%v diagnostics=%v", err, imported.Diagnostics)
	}
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: typeName, CurrentState: imported.ImportedResources[0].State, Private: imported.ImportedResources[0].Private})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("import read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	importedPermissions := agentProtocolToolPermissions(t, schema, read.NewState)
	if importedPermissions["import-server"] != `["zeta","alpha"]` || importedPermissions["empty"] != `[]` {
		t.Fatalf("imported permissions are not deterministic compact JSON: %#v", importedPermissions)
	}
}

func TestAgentMCPToolPermissionsProtocolAuthoritativeAbsence(t *testing.T) {
	for _, test := range []struct {
		name               string
		configured         interface{}
		responsePermission string
		wantNull           bool
	}{
		{"empty map and null field", map[string]interface{}{}, "null field", false},
		{"empty map and omitted field", map[string]interface{}{}, "omitted field", false},
		{"empty map and null object", map[string]interface{}{}, "null object", false},
		{"empty map and omitted object", map[string]interface{}{}, "omitted object", false},
		{"nonempty map and null field", map[string]interface{}{"server": `["tool"]`}, "null field", true},
		{"nonempty map and omitted field", map[string]interface{}{"server": `["tool"]`}, "omitted field", true},
		{"nonempty map and null object", map[string]interface{}{"server": `["tool"]`}, "null object", true},
		{"nonempty map and omitted object", map[string]interface{}{"server": `["tool"]`}, "omitted object", true},
		{"unowned map and omitted object", nil, "omitted object", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				response := map[string]interface{}{"agent_id": "agent-absence", "agent_name": "agent"}
				switch test.responsePermission {
				case "null field":
					response["object_permission"] = map[string]interface{}{"mcp_tool_permissions": nil}
				case "omitted field":
					response["object_permission"] = map[string]interface{}{}
				case "null object":
					response["object_permission"] = nil
				case "omitted object":
					// Leave object_permission absent.
				}
				_ = json.NewEncoder(writer).Encode(response)
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_agent"]
			state := agentProtocolDynamicValue(t, schema, map[string]interface{}{
				"id": "agent-absence", "agent_name": "agent",
				"object_permission": map[string]interface{}{
					"models":               []interface{}{"model-owned"},
					"mcp_tool_permissions": test.configured,
				},
			})
			read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: state})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
			}
			permission := agentProtocolToolPermissionsValue(t, schema, read.NewState)
			if permission.IsNull() != test.wantNull {
				t.Fatalf("permission null=%t, want %t", permission.IsNull(), test.wantNull)
			}
			if !test.wantNull {
				if got := agentProtocolToolPermissions(t, schema, read.NewState); len(got) != 0 {
					t.Fatalf("explicit empty clear did not converge: %#v", got)
				}
			}
			var modelValues []tftypes.Value
			if err := agentProtocolObjectPermissionAttributes(t, schema, read.NewState)["models"].As(&modelValues); err != nil {
				t.Fatal(err)
			}
			var model string
			if len(modelValues) != 1 {
				t.Fatalf("whole-object absence disturbed sibling models: %#v", modelValues)
			}
			if err := modelValues[0].As(&model); err != nil {
				t.Fatal(err)
			}
			if model != "model-owned" {
				t.Fatalf("whole-object absence changed sibling model: %q", model)
			}
		})
	}
}

func TestAgentMCPToolPermissionsProtocolCreateAbsenceConvergence(t *testing.T) {
	for _, test := range []struct {
		name               string
		configured         map[string]interface{}
		readbackPermission interface{}
		wantError          bool
	}{
		{"explicit empty clear converges", map[string]interface{}{}, nil, false},
		{"nonempty omission fails closed", map[string]interface{}{"server": `["tool"]`}, nil, true},
		{"different nonempty value fails closed", map[string]interface{}{"server": `["tool"]`}, map[string]interface{}{"server": []interface{}{"different"}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodPost {
					_, _ = fmt.Fprint(writer, `{"agent_id":"agent-create-absence"}`)
					return
				}
				if request.Method == http.MethodGet {
					objectPermission := map[string]interface{}{}
					if test.readbackPermission != nil {
						objectPermission["mcp_tool_permissions"] = test.readbackPermission
					}
					_ = json.NewEncoder(writer).Encode(map[string]interface{}{"agent_id": "agent-create-absence", "agent_name": "agent", "object_permission": objectPermission})
					return
				}
				http.NotFound(writer, request)
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			const typeName = "litellm_agent"
			schema := schemas.ResourceSchemas[typeName]
			configValues := map[string]interface{}{
				"agent_name": "agent",
				"agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"},
				"object_permission": map[string]interface{}{
					"mcp_tool_permissions": test.configured,
				},
			}
			config := agentProtocolDynamicValue(t, schema, configValues)
			proposedValues := map[string]interface{}{
				"id": tftypes.UnknownValue, "agent_name": "agent",
				"agent_card": map[string]interface{}{"name": "Agent", "url": "https://agent.invalid"},
				"object_permission": map[string]interface{}{
					"mcp_tool_permissions": test.configured,
				},
				"litellm_params": tftypes.UnknownValue, "static_headers": tftypes.UnknownValue, "extra_headers": tftypes.UnknownValue,
				"created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue, "created_by": tftypes.UnknownValue, "updated_by": tftypes.UnknownValue,
			}
			proposed := agentProtocolDynamicValue(t, schema, proposedValues)
			nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
			}
			created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) != test.wantError {
				t.Fatalf("create: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(created.Diagnostics))
			}
			if test.wantError {
				attributes := protocolAttributeMap(t, schema, created.NewState)
				var id string
				if err := attributes["id"].As(&id); err != nil || id != "agent-create-absence" {
					t.Fatalf("partial state ID=%q err=%v", id, err)
				}
				for name, value := range attributes {
					if name != "id" && !value.IsNull() {
						t.Fatalf("partial state published unconfirmed attribute %q", name)
					}
				}
				return
			}
			if got := agentProtocolToolPermissions(t, schema, created.NewState); len(got) != 0 {
				t.Fatal("explicit empty clear did not converge after create")
			}
		})
	}
}

func TestAgentMCPToolPermissionsProtocolUpdateMismatchRetainsPriorState(t *testing.T) {
	for _, test := range []struct {
		name               string
		readbackPermission interface{}
		changeCapability   bool
	}{
		{"omitted permission", nil, false},
		{"different permission", map[string]interface{}{"server": []interface{}{"different"}}, false},
		{"capability retry still checks permission", map[string]interface{}{"server": []interface{}{"different"}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			var updated atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.Method == http.MethodPut {
					updated.Store(true)
					_, _ = fmt.Fprint(writer, `{}`)
					return
				}
				if request.Method != http.MethodGet {
					http.NotFound(writer, request)
					return
				}
				var permission interface{} = map[string]interface{}{"server": []interface{}{"prior"}}
				streaming := false
				if updated.Load() {
					permission = test.readbackPermission
					streaming = test.changeCapability
				}
				objectPermission := map[string]interface{}{}
				if permission != nil {
					objectPermission["mcp_tool_permissions"] = permission
				}
				_ = json.NewEncoder(writer).Encode(map[string]interface{}{
					"agent_id": "agent-update", "agent_name": "agent",
					"agent_card":        map[string]interface{}{"name": "Agent", "url": "https://agent.invalid", "capabilities": map[string]interface{}{"streaming": streaming}},
					"object_permission": objectPermission,
				})
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			const typeName = "litellm_agent"
			schema := schemas.ResourceSchemas[typeName]
			priorCard := map[string]interface{}{"name": "Agent", "url": "https://agent.invalid", "capabilities": map[string]interface{}{"streaming": false}}
			plannedCard := priorCard
			if test.changeCapability {
				plannedCard = map[string]interface{}{"name": "Agent", "url": "https://agent.invalid", "capabilities": map[string]interface{}{"streaming": true}}
			}
			state := agentProtocolDynamicValue(t, schema, map[string]interface{}{
				"id": "agent-update", "agent_name": "agent", "agent_card": priorCard,
				"object_permission": map[string]interface{}{"mcp_tool_permissions": map[string]interface{}{"server": `["prior"]`}},
			})
			config := agentProtocolDynamicValue(t, schema, map[string]interface{}{
				"agent_name": "agent", "agent_card": plannedCard,
				"object_permission": map[string]interface{}{"mcp_tool_permissions": map[string]interface{}{"server": `["planned"]`}},
			})
			proposed := agentProtocolDynamicValue(t, schema, map[string]interface{}{
				"id": "agent-update", "agent_name": "agent", "agent_card": plannedCard,
				"object_permission": map[string]interface{}{"mcp_tool_permissions": map[string]interface{}{"server": `["planned"]`}},
			})
			planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, ProposedNewState: proposed})
			if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
				t.Fatalf("plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
			}
			applied, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: state, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(applied.Diagnostics) || !updated.Load() {
				t.Fatalf("apply: err=%v diagnostics=%s updated=%t", err, agentProtocolDiagnosticsText(applied.Diagnostics), updated.Load())
			}
			if applied.NewState != nil {
				got := agentProtocolToolPermissions(t, schema, applied.NewState)
				if got["server"] != `["prior"]` {
					t.Fatalf("failed update published permissions: %#v", got)
				}
			}
		})
	}
}

func TestAgentMCPToolPermissionsProtocolRepairsLegacyReadState(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(writer, `{"agent_id":"agent-legacy","agent_name":"agent","object_permission":{"mcp_tool_permissions":{"server":["first","second"]}}}`)
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	schema := schemas.ResourceSchemas["litellm_agent"]
	state := agentProtocolDynamicValue(t, schema, map[string]interface{}{
		"id": "agent-legacy", "agent_name": "agent",
		"object_permission": map[string]interface{}{"mcp_tool_permissions": map[string]interface{}{"server": `[first second]`}},
	})
	read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: state})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
		t.Fatalf("legacy read: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(read.Diagnostics))
	}
	if got := agentProtocolToolPermissions(t, schema, read.NewState)["server"]; got != `["first","second"]` {
		t.Fatalf("legacy state was not repaired: %q", got)
	}
}

func TestAgentMCPToolPermissionsProtocolMalformedReadFailsClosed(t *testing.T) {
	const secret = "sentinel-private-tool"
	for name, response := range map[string]string{
		"malformed permission":   `{"agent_id":"agent-malformed","agent_name":"agent","object_permission":{"mcp_tool_permissions":{"sentinel-private-server":["` + secret + `",1]}}}`,
		"malformed outer object": `{"agent_id":"agent-malformed","agent_name":"agent","object_permission":"` + secret + `"}`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(writer, response)
			}))
			defer server.Close()
			protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
			schema := schemas.ResourceSchemas["litellm_agent"]
			state := agentProtocolDynamicValue(t, schema, map[string]interface{}{
				"id": "agent-malformed", "agent_name": "prior",
				"object_permission": map[string]interface{}{"mcp_tool_permissions": map[string]interface{}{"prior-server": ` [ "prior-tool" ] `}},
			})
			read, err := protocolServer.ReadResource(ctx, &tfprotov6.ReadResourceRequest{TypeName: "litellm_agent", CurrentState: state})
			if err != nil || !accessGroupProtocolDiagnosticsHaveError(read.Diagnostics) {
				t.Fatalf("malformed response accepted: err=%v diagnostics=%v", err, read.Diagnostics)
			}
			diagnostics := agentProtocolDiagnosticsText(read.Diagnostics)
			for _, forbidden := range []string{secret, "sentinel-private-server", "prior-server", "prior-tool", server.URL} {
				if strings.Contains(diagnostics, forbidden) {
					t.Fatal("read diagnostics leaked protected content")
				}
			}
			before, _ := state.Unmarshal(schema.ValueType())
			after, _ := read.NewState.Unmarshal(schema.ValueType())
			if !before.Equal(after) {
				t.Fatal("malformed response changed prior state")
			}
		})
	}
}

func TestAgentMCPToolPermissionsProtocolMalformedCreateReadBackKeepsOnlyID(t *testing.T) {
	ctx := context.Background()
	const (
		configuredServer = "sentinel-configured-server"
		configuredTool   = "sentinel-configured-tool"
		responseServer   = "sentinel-response-server"
		responseTool     = "sentinel-response-tool"
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case http.MethodPost:
			_, _ = fmt.Fprint(writer, `{"agent_id":"agent-created-partial"}`)
		case http.MethodGet:
			_, _ = fmt.Fprint(writer, `{"agent_id":"agent-untrusted-read","agent_name":"untrusted","object_permission":{"mcp_tool_permissions":{"`+responseServer+`":["`+responseTool+`",false]}}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	protocolServer, schemas := configuredImportProtocolServer(t, ctx, server.URL)
	const typeName = "litellm_agent"
	schema := schemas.ResourceSchemas[typeName]
	configValues := map[string]interface{}{
		"agent_name": "configured-agent",
		"agent_card": map[string]interface{}{"name": "Configured Agent", "url": "https://configured.invalid"},
		"object_permission": map[string]interface{}{
			"mcp_tool_permissions": map[string]interface{}{configuredServer: `["` + configuredTool + `"]`},
		},
	}
	config := agentProtocolDynamicValue(t, schema, configValues)
	proposedValues := map[string]interface{}{
		"id": tftypes.UnknownValue, "agent_name": "configured-agent",
		"agent_card": map[string]interface{}{"name": "Configured Agent", "url": "https://configured.invalid"},
		"object_permission": map[string]interface{}{
			"mcp_tool_permissions": map[string]interface{}{configuredServer: `["` + configuredTool + `"]`},
		},
		"litellm_params": tftypes.UnknownValue, "static_headers": tftypes.UnknownValue, "extra_headers": tftypes.UnknownValue,
		"created_at": tftypes.UnknownValue, "updated_at": tftypes.UnknownValue, "created_by": tftypes.UnknownValue, "updated_by": tftypes.UnknownValue,
	}
	proposed := agentProtocolDynamicValue(t, schema, proposedValues)
	nullState := accessGroupProtocolDynamicValue(t, schema, tftypes.NewValue(schema.ValueType(), nil))
	planned, err := protocolServer.PlanResourceChange(ctx, &tfprotov6.PlanResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, ProposedNewState: proposed})
	if err != nil || accessGroupProtocolDiagnosticsHaveError(planned.Diagnostics) {
		t.Fatalf("plan: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(planned.Diagnostics))
	}
	created, err := protocolServer.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{TypeName: typeName, Config: config, PriorState: nullState, PlannedState: planned.PlannedState, PlannedPrivate: planned.PlannedPrivate})
	if err != nil || !accessGroupProtocolDiagnosticsHaveError(created.Diagnostics) {
		t.Fatalf("malformed create read-back accepted: err=%v diagnostics=%s", err, agentProtocolDiagnosticsText(created.Diagnostics))
	}
	diagnosticText := agentProtocolDiagnosticsText(created.Diagnostics)
	for _, forbidden := range []string{configuredServer, configuredTool, responseServer, responseTool, "agent-untrusted-read", "configured.invalid", server.URL} {
		if strings.Contains(diagnosticText, forbidden) {
			t.Fatal("create diagnostic leaked protected content")
		}
	}
	attributes := protocolAttributeMap(t, schema, created.NewState)
	var id string
	if err := attributes["id"].As(&id); err != nil || id != "agent-created-partial" {
		t.Fatalf("partial state ID=%q err=%v", id, err)
	}
	for name, value := range attributes {
		if name != "id" && !value.IsNull() {
			t.Fatalf("partial state published unconfirmed attribute %q", name)
		}
	}
}
