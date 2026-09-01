package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMCPToolsetResourceSchema(t *testing.T) {
	t.Parallel()

	underTest := &MCPToolsetResource{}
	var resp resource.SchemaResponse
	underTest.Schema(context.Background(), resource.SchemaRequest{}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("schema returned diagnostics: %v", resp.Diagnostics.Errors())
	}
	if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("schema implementation validation failed: %v", diags.Errors())
	}

	toolsetName, ok := resp.Schema.Attributes["toolset_name"].(schema.StringAttribute)
	if !ok || !toolsetName.Required {
		t.Fatalf("toolset_name must be a required string attribute, got %#v", resp.Schema.Attributes["toolset_name"])
	}

	description, ok := resp.Schema.Attributes["description"].(schema.StringAttribute)
	if !ok || !description.Optional {
		t.Fatalf("description must be an optional string attribute, got %#v", resp.Schema.Attributes["description"])
	}

	toolsetID, ok := resp.Schema.Attributes["toolset_id"].(schema.StringAttribute)
	if !ok || !toolsetID.Computed {
		t.Fatalf("toolset_id must be a computed string attribute, got %#v", resp.Schema.Attributes["toolset_id"])
	}

	tools, ok := resp.Schema.Attributes["tools"].(schema.SetNestedAttribute)
	if !ok {
		t.Fatalf("tools must be a nested set attribute, got %T", resp.Schema.Attributes["tools"])
	}
	if !tools.Optional || tools.Default == nil {
		t.Fatalf("tools must be optional with an empty-set default, got %#v", tools)
	}

	for _, name := range []string{"server_id", "tool_name"} {
		attribute, ok := tools.NestedObject.Attributes[name].(schema.StringAttribute)
		if !ok || !attribute.Required {
			t.Fatalf("tools.%s must be a required string attribute, got %#v", name, tools.NestedObject.Attributes[name])
		}
		if len(attribute.Validators) != 0 {
			t.Fatalf("tools.%s must not perform live catalog validation", name)
		}
	}
}

func TestProviderRegistersMCPToolsetResource(t *testing.T) {
	t.Parallel()

	provider := &LiteLLMProvider{}
	for _, factory := range provider.Resources(context.Background()) {
		var resp resource.MetadataResponse
		factory().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "litellm"}, &resp)
		if resp.TypeName == "litellm_mcp_toolset" {
			return
		}
	}

	t.Fatal("provider did not register litellm_mcp_toolset")
}

func TestBuildMCPToolsetRequestTreatsToolsAsAnUnorderedSet(t *testing.T) {
	t.Parallel()

	tools := types.SetValueMust(
		types.ObjectType{AttrTypes: mcpToolsetToolAttributeTypes},
		[]attr.Value{
			mcpToolsetToolValue("server-b", "deploy"),
			mcpToolsetToolValue("server-a", "search"),
			mcpToolsetToolValue("server-a", "search"),
		},
	)
	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringValue("toolset-1"),
		ToolsetName: types.StringValue("sre-readonly"),
		Description: types.StringValue("Read-only SRE tools"),
		Tools:       tools,
	}

	request, err := buildMCPToolsetRequest(context.Background(), &data, true)
	if err != nil {
		t.Fatalf("buildMCPToolsetRequest returned error: %v", err)
	}

	want := mcpToolsetRequest{
		ToolsetID:   "toolset-1",
		ToolsetName: "sre-readonly",
		Description: mcpToolsetStringPointer("Read-only SRE tools"),
		Tools: []mcpToolsetTool{
			{ServerID: "server-a", ToolName: "search"},
			{ServerID: "server-b", ToolName: "deploy"},
		},
	}
	if !reflect.DeepEqual(request, want) {
		t.Fatalf("unexpected request\nwant: %#v\n got: %#v", want, request)
	}
}

func TestBuildMCPToolsetRequestDefaultsToolsToEmpty(t *testing.T) {
	t.Parallel()

	data := MCPToolsetResourceModel{
		ToolsetName: types.StringValue("empty"),
		Description: types.StringNull(),
		Tools:       types.SetNull(types.ObjectType{AttrTypes: mcpToolsetToolAttributeTypes}),
	}

	request, err := buildMCPToolsetRequest(context.Background(), &data, false)
	if err != nil {
		t.Fatalf("buildMCPToolsetRequest returned error: %v", err)
	}
	if request.Tools == nil || len(request.Tools) != 0 {
		t.Fatalf("tools must be a non-nil empty list, got %#v", request.Tools)
	}
	if request.Description != nil {
		t.Fatalf("create request must omit an unconfigured description, got %#v", request.Description)
	}
}

func TestCreateMCPToolsetStoresReturnedState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/mcp/toolset" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var request mcpToolsetRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.ToolsetID != "" || request.ToolsetName != "incident-response" || len(request.Tools) != 1 {
			t.Fatalf("unexpected create payload: %#v", request)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(mcpToolsetResponse{
			ToolsetID:   "toolset-created",
			ToolsetName: "incident-response",
			Description: mcpToolsetStringPointer("Incident response reads"),
			Tools:       []mcpToolsetTool{{ServerID: "pagerduty", ToolName: "list_incidents"}},
		})
	}))
	defer server.Close()

	resource := testMCPToolsetResource(server)
	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringUnknown(),
		ToolsetName: types.StringValue("incident-response"),
		Description: types.StringValue("Incident response reads"),
		Tools:       mcpToolsetToolsValue(mcpToolsetTool{"pagerduty", "list_incidents"}),
	}

	if _, err := resource.createMCPToolset(context.Background(), &data); err != nil {
		t.Fatalf("createMCPToolset returned error: %v", err)
	}
	if got := data.ToolsetID.ValueString(); got != "toolset-created" {
		t.Fatalf("expected returned toolset ID, got %q", got)
	}
}

func TestCreateMCPToolsetRejectsDuplicateNameWithoutPartialState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":{"error":"already exists"}}`, http.StatusConflict)
	}))
	defer server.Close()

	resource := testMCPToolsetResource(server)
	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringUnknown(),
		ToolsetName: types.StringValue("duplicate"),
		Description: types.StringNull(),
		Tools:       mcpToolsetToolsValue(),
	}

	outcome, err := resource.createMCPToolset(context.Background(), &data)
	if err == nil {
		t.Fatal("expected create conflict error")
	}
	if outcome != mcpToolsetCreateRejected {
		t.Fatalf("conflict create outcome = %v, want rejected", outcome)
	}
	if !data.ToolsetID.IsUnknown() {
		t.Fatalf("failed create changed toolset_id to %#v", data.ToolsetID)
	}
}

func TestCreateMCPToolsetRecoversAcceptedCreateWithUnusableResponse(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/mcp/toolset":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"toolset_name":"incident-response"}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset":
			_ = json.NewEncoder(w).Encode([]mcpToolsetResponse{
				{ToolsetID: "toolset-other", ToolsetName: "other"},
				{ToolsetID: "toolset-recovered", ToolsetName: "incident-response"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset/toolset-recovered":
			_ = json.NewEncoder(w).Encode(mcpToolsetResponse{
				ToolsetID:   "toolset-recovered",
				ToolsetName: "incident-response",
				Description: mcpToolsetStringPointer("Incident response reads"),
				Tools:       []mcpToolsetTool{{ServerID: "pagerduty", ToolName: "list_incidents"}},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	resource := testMCPToolsetResource(server)
	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringUnknown(),
		ToolsetName: types.StringValue("incident-response"),
		Description: types.StringValue("Incident response reads"),
		Tools:       mcpToolsetToolsValue(mcpToolsetTool{"pagerduty", "list_incidents"}),
	}

	if _, err := resource.createMCPToolset(context.Background(), &data); err != nil {
		t.Fatalf("createMCPToolset returned error: %v", err)
	}
	if got := data.ToolsetID.ValueString(); got != "toolset-recovered" {
		t.Fatalf("expected recovered toolset ID, got %q", got)
	}
}

func TestCreateMCPToolsetRecoveryRetriesTransientListFailures(t *testing.T) {
	t.Parallel()

	var listCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/mcp/toolset":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`not json`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset":
			switch atomic.AddInt32(&listCalls, 1) {
			case 1:
				http.Error(w, `{"error":"transient"}`, http.StatusBadGateway)
			case 2:
				_, _ = w.Write([]byte(`[]`))
			default:
				_ = json.NewEncoder(w).Encode([]mcpToolsetResponse{{
					ToolsetID:   "toolset-recovered",
					ToolsetName: "incident-response",
				}})
			}
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset/toolset-recovered":
			_ = json.NewEncoder(w).Encode(mcpToolsetResponse{
				ToolsetID:   "toolset-recovered",
				ToolsetName: "incident-response",
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	resource := testMCPToolsetResource(server)
	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringUnknown(),
		ToolsetName: types.StringValue("incident-response"),
		Description: types.StringNull(),
		Tools:       mcpToolsetToolsValue(),
	}

	if _, err := resource.createMCPToolset(context.Background(), &data); err != nil {
		t.Fatalf("createMCPToolset returned error: %v", err)
	}
	if got := data.ToolsetID.ValueString(); got != "toolset-recovered" {
		t.Fatalf("expected recovered toolset ID, got %q", got)
	}
	if got := atomic.LoadInt32(&listCalls); got != 3 {
		t.Fatalf("recovery list calls = %d, want 3", got)
	}
}

func TestCreateMCPToolsetReportsBothErrorsWhenRecoveryFindsNoMatch(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/mcp/toolset":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`not json`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset":
			_, _ = w.Write([]byte(`[]`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	resource := testMCPToolsetResource(server)
	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringUnknown(),
		ToolsetName: types.StringValue("incident-response"),
		Description: types.StringNull(),
		Tools:       mcpToolsetToolsValue(),
	}

	outcome, err := resource.createMCPToolset(context.Background(), &data)
	if err == nil {
		t.Fatal("expected create error when recovery finds no match")
	}
	if outcome != mcpToolsetCreateAccepted {
		t.Fatalf("accepted create with failed recovery outcome = %v, want accepted", outcome)
	}
	if !data.ToolsetID.IsUnknown() {
		t.Fatalf("failed create changed toolset_id to %#v", data.ToolsetID)
	}
	if !strings.Contains(err.Error(), "recovery by name failed") {
		t.Fatalf("error does not describe failed recovery: %v", err)
	}
}

func TestReadMCPToolsetUsesRemoteValuesWithoutOrderingDrift(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/mcp/toolset/toolset-read" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mcpToolsetResponse{
			ToolsetID:   "toolset-read",
			ToolsetName: "remote-name",
			Tools: []mcpToolsetTool{
				{ServerID: "server-b", ToolName: "beta"},
				{ServerID: "server-a", ToolName: "alpha"},
			},
		})
	}))
	defer server.Close()

	resource := testMCPToolsetResource(server)
	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringValue("toolset-read"),
		ToolsetName: types.StringValue("old-name"),
		Description: types.StringValue("old description"),
		Tools:       mcpToolsetToolsValue(mcpToolsetTool{"server-a", "alpha"}, mcpToolsetTool{"server-b", "beta"}),
	}
	wantTools := data.Tools

	if err := resource.readMCPToolset(context.Background(), &data); err != nil {
		t.Fatalf("readMCPToolset returned error: %v", err)
	}
	if got := data.ToolsetName.ValueString(); got != "remote-name" {
		t.Fatalf("expected remote name, got %q", got)
	}
	if !data.Description.IsNull() {
		t.Fatalf("expected remote null description, got %#v", data.Description)
	}
	if !data.Tools.Equal(wantTools) {
		t.Fatalf("tool order created drift\nwant: %#v\n got: %#v", wantTools, data.Tools)
	}
}

func TestReadMCPToolsetErrorsDoNotChangePriorState(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name       string
		statusCode int
		notFound   bool
	}{
		{name: "not found", statusCode: http.StatusNotFound, notFound: true},
		{name: "server error", statusCode: http.StatusInternalServerError},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "request failed", testCase.statusCode)
			}))
			defer server.Close()

			resource := testMCPToolsetResource(server)
			data := MCPToolsetResourceModel{
				ToolsetID:   types.StringValue("toolset-read-error"),
				ToolsetName: types.StringValue("prior-name"),
				Description: types.StringValue("prior description"),
				Tools:       mcpToolsetToolsValue(mcpToolsetTool{"server-a", "alpha"}),
			}
			before := data

			err := resource.readMCPToolset(context.Background(), &data)
			if err == nil {
				t.Fatal("expected read error")
			}
			if IsNotFoundError(err) != testCase.notFound {
				t.Fatalf("IsNotFoundError(%v) = %v, want %v", err, IsNotFoundError(err), testCase.notFound)
			}
			if !reflect.DeepEqual(data, before) {
				t.Fatalf("failed read changed prior state\nwant: %#v\n got: %#v", before, data)
			}
		})
	}
}

func TestUpdateMCPToolsetPreservesIDAndSendsFullDefinition(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/v1/mcp/toolset/toolset-stable" {
			_ = json.NewEncoder(w).Encode(mcpToolsetResponse{
				ToolsetID:   "toolset-stable",
				ToolsetName: "renamed",
				Description: mcpToolsetStringPointer(""),
				Tools:       []mcpToolsetTool{},
			})
			return
		}
		if r.Method != http.MethodPut || r.URL.Path != "/v1/mcp/toolset" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var request mcpToolsetRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if request.ToolsetID != "toolset-stable" || request.ToolsetName != "renamed" || request.Description == nil || *request.Description != "" || len(request.Tools) != 0 {
			t.Fatalf("unexpected update payload: %#v", request)
		}

		_ = json.NewEncoder(w).Encode(mcpToolsetResponse{
			ToolsetID:   "toolset-stable",
			ToolsetName: "renamed",
			Description: mcpToolsetStringPointer(""),
			Tools:       []mcpToolsetTool{},
		})
	}))
	defer server.Close()

	resource := testMCPToolsetResource(server)
	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringValue("toolset-stable"),
		ToolsetName: types.StringValue("renamed"),
		Description: types.StringNull(),
		Tools:       mcpToolsetToolsValue(),
	}

	if err := resource.updateMCPToolset(context.Background(), &data); err != nil {
		t.Fatalf("updateMCPToolset returned error: %v", err)
	}
	if got := data.ToolsetID.ValueString(); got != "toolset-stable" {
		t.Fatalf("update changed toolset ID to %q", got)
	}
	if !data.Description.IsNull() {
		t.Fatalf("empty remote description should normalize to null, got %#v", data.Description)
	}
}

func TestUpdateMCPToolsetFailureDoesNotApplyResponseData(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(mcpToolsetResponse{ToolsetID: "partial-id", ToolsetName: "partial-name"})
	}))
	defer server.Close()

	resource := testMCPToolsetResource(server)
	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringValue("toolset-stable"),
		ToolsetName: types.StringValue("planned-name"),
		Description: types.StringNull(),
		Tools:       mcpToolsetToolsValue(),
	}
	before := data

	if err := resource.updateMCPToolset(context.Background(), &data); err == nil {
		t.Fatal("expected update error")
	}
	if !reflect.DeepEqual(data, before) {
		t.Fatalf("failed update applied response data\nwant: %#v\n got: %#v", before, data)
	}
}

func TestApplyMCPToolsetResponsePreservesConfiguredEmptyDescription(t *testing.T) {
	t.Parallel()

	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringUnknown(),
		ToolsetName: types.StringValue("empty-description"),
		Description: types.StringValue(""),
		Tools:       mcpToolsetToolsValue(),
	}
	result := mcpToolsetResponse{
		ToolsetID:   "toolset-empty-description",
		ToolsetName: "empty-description",
		Description: mcpToolsetStringPointer(""),
		Tools:       []mcpToolsetTool{},
	}

	if err := applyMCPToolsetResponse(context.Background(), result, "", &data); err != nil {
		t.Fatalf("applyMCPToolsetResponse returned error: %v", err)
	}
	if data.Description.IsNull() || data.Description.ValueString() != "" {
		t.Fatalf("configured empty description must remain an empty string, got %#v", data.Description)
	}
}

func TestDeleteMCPToolsetConvergesOnSuccessAndNotFound(t *testing.T) {
	t.Parallel()

	for _, statusCode := range []int{http.StatusAccepted, http.StatusNotFound} {
		statusCode := statusCode
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodDelete || r.URL.Path != "/v1/mcp/toolset/toolset-delete" {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(statusCode)
			}))
			defer server.Close()

			resource := testMCPToolsetResource(server)
			if err := resource.deleteMCPToolset(context.Background(), "toolset-delete"); err != nil {
				t.Fatalf("deleteMCPToolset returned error for %d: %v", statusCode, err)
			}
		})
	}
}

func TestDeleteMCPToolsetPropagatesNonNotFoundFailure(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "request failed", http.StatusBadGateway)
	}))
	defer server.Close()

	resource := testMCPToolsetResource(server)
	if err := resource.deleteMCPToolset(context.Background(), "toolset-delete"); err == nil {
		t.Fatal("expected delete error")
	}
}

func TestMCPToolsetSpecialIDUsesSingleEscapedPathSegment(t *testing.T) {
	t.Parallel()

	toolsetID := "tenant:admin toolset?revision=1"
	var requestURI string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestURI = r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(mcpToolsetResponse{
			ToolsetID: toolsetID, ToolsetName: "special", Tools: []mcpToolsetTool{},
		})
	}))
	defer server.Close()

	resource := testMCPToolsetResource(server)
	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringValue(toolsetID),
		ToolsetName: types.StringValue("special"),
		Description: types.StringNull(),
		Tools:       mcpToolsetToolsValue(),
	}
	if err := resource.readMCPToolset(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	if requestURI != mcpToolsetEndpoint(toolsetID) {
		t.Fatalf("request URI = %q, want %q", requestURI, mcpToolsetEndpoint(toolsetID))
	}
	if !strings.Contains(requestURI, "%3F") || !strings.Contains(requestURI, "tenant:admin") {
		t.Fatalf("special ID was not safely represented as one path segment: %q", requestURI)
	}
}

func TestMCPToolsetSlashIDFailsBeforeDispatch(t *testing.T) {
	t.Parallel()

	toolsetID := "tenant:admin/toolset?revision=1"
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()

	resource := testMCPToolsetResource(server)
	data := MCPToolsetResourceModel{
		ToolsetID:   types.StringValue(toolsetID),
		ToolsetName: types.StringValue("special"),
		Description: types.StringNull(),
		Tools:       mcpToolsetToolsValue(),
	}
	if err := resource.readMCPToolset(context.Background(), &data); err == nil || requests != 0 {
		t.Fatalf("slash ID result: err=%v requests=%d", err, requests)
	}
	if err := resource.deleteMCPToolset(context.Background(), toolsetID); err == nil || requests != 0 {
		t.Fatalf("slash ID delete result: err=%v requests=%d", err, requests)
	}
}

func TestMCPToolsetReadRemovesExternallyDeletedResourceFromState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	underTest := testMCPToolsetResource(server)
	state := mcpToolsetTestState(t, MCPToolsetResourceModel{
		ToolsetID:   types.StringValue("toolset-gone"),
		ToolsetName: types.StringValue("gone"),
		Description: types.StringNull(),
		Tools:       mcpToolsetToolsValue(),
	})
	resp := resource.ReadResponse{State: state}
	underTest.Read(context.Background(), resource.ReadRequest{State: state}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("read returned diagnostics for a missing toolset: %v", resp.Diagnostics.Errors())
	}
	if !resp.State.Raw.IsNull() {
		t.Fatalf("missing toolset remained in state: %#v", resp.State.Raw)
	}
}

func TestMCPToolsetReadFailureLeavesResourceInState(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "request failed", http.StatusBadGateway)
	}))
	defer server.Close()

	underTest := testMCPToolsetResource(server)
	state := mcpToolsetTestState(t, MCPToolsetResourceModel{
		ToolsetID:   types.StringValue("toolset-still-present"),
		ToolsetName: types.StringValue("prior-name"),
		Description: types.StringNull(),
		Tools:       mcpToolsetToolsValue(),
	})
	resp := resource.ReadResponse{State: state}
	underTest.Read(context.Background(), resource.ReadRequest{State: state}, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected read diagnostic")
	}
	if resp.State.Raw.IsNull() {
		t.Fatal("non-404 read failure removed resource from state")
	}
}

func TestMCPToolsetImportStoresToolsetIDForRefresh(t *testing.T) {
	t.Parallel()

	underTest := &MCPToolsetResource{}
	state := mcpToolsetTestState(t, MCPToolsetResourceModel{
		ToolsetID:   types.StringNull(),
		ToolsetName: types.StringNull(),
		Description: types.StringNull(),
		Tools:       types.SetNull(types.ObjectType{AttrTypes: mcpToolsetToolAttributeTypes}),
	})
	resp := resource.ImportStateResponse{State: state}
	underTest.ImportState(context.Background(), resource.ImportStateRequest{ID: "toolset-imported"}, &resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("import returned diagnostics: %v", resp.Diagnostics.Errors())
	}
	var imported MCPToolsetResourceModel
	if diags := resp.State.Get(context.Background(), &imported); diags.HasError() {
		t.Fatalf("decode imported state: %v", diags.Errors())
	}
	if got := imported.ToolsetID.ValueString(); got != "toolset-imported" {
		t.Fatalf("imported toolset ID = %q, want toolset-imported", got)
	}
}

func mcpToolsetToolValue(serverID, toolName string) types.Object {
	return types.ObjectValueMust(mcpToolsetToolAttributeTypes, map[string]attr.Value{
		"server_id": types.StringValue(serverID),
		"tool_name": types.StringValue(toolName),
	})
}

func mcpToolsetToolsValue(tools ...mcpToolsetTool) types.Set {
	values := make([]attr.Value, 0, len(tools))
	for _, tool := range tools {
		values = append(values, mcpToolsetToolValue(tool.ServerID, tool.ToolName))
	}
	return types.SetValueMust(types.ObjectType{AttrTypes: mcpToolsetToolAttributeTypes}, values)
}

func testMCPToolsetResource(server *httptest.Server) *MCPToolsetResource {
	return &MCPToolsetResource{
		client: &Client{
			APIBase:    server.URL,
			APIKey:     "test-key",
			HTTPClient: server.Client(),
		},
		createRecoveryDelay: time.Millisecond,
	}
}

func mcpToolsetTestState(t *testing.T, model MCPToolsetResourceModel) tfsdk.State {
	t.Helper()

	underTest := &MCPToolsetResource{}
	var schemaResp resource.SchemaResponse
	underTest.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema returned diagnostics: %v", schemaResp.Diagnostics.Errors())
	}

	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(context.Background(), &model); diags.HasError() {
		t.Fatalf("initialize state: %v", diags.Errors())
	}
	return state
}

func mcpToolsetStringPointer(value string) *string {
	return &value
}

// Unknown values must fail closed before dispatch instead of serializing to
// empty or omitted wire values.
func TestBuildMCPToolsetRequestRejectsUnknownValues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	toolType := types.ObjectType{AttrTypes: mcpToolsetToolAttributeTypes}

	base := func() MCPToolsetResourceModel {
		return MCPToolsetResourceModel{
			ToolsetName: types.StringValue("toolset"),
			Description: types.StringNull(),
			Tools:       types.SetNull(toolType),
		}
	}

	name := base()
	name.ToolsetName = types.StringUnknown()
	if _, err := buildMCPToolsetRequest(ctx, &name, false); err == nil {
		t.Fatal("unknown toolset_name did not fail closed")
	}

	description := base()
	description.Description = types.StringUnknown()
	if _, err := buildMCPToolsetRequest(ctx, &description, false); err == nil {
		t.Fatal("unknown description did not fail closed")
	}

	tool := base()
	toolValue, diags := types.ObjectValue(mcpToolsetToolAttributeTypes, map[string]attr.Value{
		"server_id": types.StringValue("server"),
		"tool_name": types.StringUnknown(),
	})
	if diags.HasError() {
		t.Fatal(diags.Errors())
	}
	tools, diags := types.SetValue(toolType, []attr.Value{toolValue})
	if diags.HasError() {
		t.Fatal(diags.Errors())
	}
	tool.Tools = tools
	if _, err := buildMCPToolsetRequest(ctx, &tool, false); err == nil {
		t.Fatal("unknown tool attribute did not fail closed")
	}
}

// confirmMCPToolsetDefinition accepts exact matches (tolerating duplicate and
// unordered response tools) and rejects any name, description, or membership
// divergence.
func TestConfirmMCPToolsetDefinition(t *testing.T) {
	t.Parallel()
	description := "desc"
	request := mcpToolsetRequest{
		ToolsetName: "toolset",
		Description: &description,
		Tools: []mcpToolsetTool{
			{ServerID: "server-a", ToolName: "tool-1"},
			{ServerID: "server-b", ToolName: "tool-2"},
		},
	}
	match := mcpToolsetResponse{
		ToolsetID:   "toolset-1",
		ToolsetName: "toolset",
		Description: &description,
		Tools: []mcpToolsetTool{
			{ServerID: "server-b", ToolName: "tool-2"},
			{ServerID: "server-a", ToolName: "tool-1"},
			{ServerID: "server-a", ToolName: "tool-1"},
		},
	}
	if err := confirmMCPToolsetDefinition(request, match); err != nil {
		t.Fatalf("exact definition rejected: %v", err)
	}

	wrongName := match
	wrongName.ToolsetName = "other"
	if err := confirmMCPToolsetDefinition(request, wrongName); err == nil {
		t.Fatal("wrong name accepted")
	}

	wrongDescription := match
	other := "other"
	wrongDescription.Description = &other
	if err := confirmMCPToolsetDefinition(request, wrongDescription); err == nil {
		t.Fatal("wrong description accepted")
	}

	missingTools := match
	missingTools.Tools = []mcpToolsetTool{{ServerID: "server-a", ToolName: "tool-1"}}
	if err := confirmMCPToolsetDefinition(request, missingTools); err == nil {
		t.Fatal("missing tool membership accepted")
	}

	nilDescriptionRequest := request
	nilDescriptionRequest.Description = nil
	nilResponse := match
	nilResponse.Description = nil
	if err := confirmMCPToolsetDefinition(nilDescriptionRequest, nilResponse); err != nil {
		t.Fatalf("nil description pair rejected: %v", err)
	}
	empty := ""
	nilResponse.Description = &empty
	if err := confirmMCPToolsetDefinition(nilDescriptionRequest, nilResponse); err != nil {
		t.Fatalf("nil request description with empty response rejected: %v", err)
	}
}

// A status-bearing terminal answer stops recovery immediately instead of
// burning the bounded retry budget, and a canceled context stops the backoff
// wait promptly.
func TestRecoverMCPToolsetByNameStopsOnTerminalAndCanceled(t *testing.T) {
	t.Parallel()

	var terminalRequests atomic.Int64
	terminalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		terminalRequests.Add(1)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer terminalServer.Close()
	underTest := &MCPToolsetResource{client: &Client{APIBase: terminalServer.URL, APIKey: "admin", HTTPClient: terminalServer.Client()}, createRecoveryDelay: time.Second}
	data := MCPToolsetResourceModel{ToolsetName: types.StringValue("toolset")}
	if _, err := underTest.recoverMCPToolsetByName(context.Background(), "toolset", nil, &data); err == nil {
		t.Fatal("terminal response did not fail recovery")
	}
	if got := terminalRequests.Load(); got != 1 {
		t.Fatalf("terminal response was retried: %d requests", got)
	}

	var transientRequests atomic.Int64
	transientServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		transientRequests.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer transientServer.Close()
	underTest = &MCPToolsetResource{client: &Client{APIBase: transientServer.URL, APIKey: "admin", HTTPClient: transientServer.Client()}, createRecoveryDelay: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := underTest.recoverMCPToolsetByName(ctx, "toolset", nil, &data); err == nil {
		t.Fatal("canceled context did not fail recovery")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("canceled context waited %s in backoff", elapsed)
	}
}

// Read repair (nil definition) adopts a drifted remote definition as
// ordinary drift instead of refusing recovery.
func TestRecoverMCPToolsetByNameWithoutDefinitionAdoptsDrift(t *testing.T) {
	t.Parallel()
	drifted := mcpToolsetResponse{ToolsetID: "toolset-1", ToolsetName: "toolset", Tools: []mcpToolsetTool{{ServerID: "server-x", ToolName: "tool-x"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/mcp/toolset":
			_ = json.NewEncoder(w).Encode([]mcpToolsetResponse{drifted})
		case "/v1/mcp/toolset/toolset-1":
			_ = json.NewEncoder(w).Encode(drifted)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	underTest := &MCPToolsetResource{client: &Client{APIBase: server.URL, APIKey: "admin", HTTPClient: server.Client()}, createRecoveryDelay: time.Millisecond}
	data := MCPToolsetResourceModel{ToolsetName: types.StringValue("toolset")}
	recovered, err := underTest.recoverMCPToolsetByName(context.Background(), "toolset", nil, &data)
	if err != nil {
		t.Fatalf("drift adoption failed: %v", err)
	}
	if recovered.ToolsetID.ValueString() != "toolset-1" || len(recovered.Tools.Elements()) != 1 {
		t.Fatalf("recovered = %#v, want drifted remote definition", recovered)
	}
}
