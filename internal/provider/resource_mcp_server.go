package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var _ resource.Resource = &MCPServerResource{}
var _ resource.ResourceWithImportState = &MCPServerResource{}
var _ resource.ResourceWithUpgradeState = &MCPServerResource{}
var _ resource.ResourceWithValidateConfig = &MCPServerResource{}
var _ resource.ResourceWithModifyPlan = &MCPServerResource{}

// Wire-contract values verified against LiteLLM commit
// d8f71d7bdbd7c9873d98293f83d64c6db72847e6: litellm/types/mcp.py,
// litellm/constants.py, and NewMCPServerRequest/UpdateMCPServerRequest in
// litellm/proxy/_types.py.
var mcpAuthTypesV198 = []string{
	"none",
	"api_key",
	"bearer_token",
	"basic",
	"authorization",
	"oauth2",
	"aws_sigv4",
	"token",
	"oauth2_token_exchange",
	"oauth2_id_jag",
	"true_passthrough",
	"oauth_delegate",
}

var mcpTransportsV198 = []string{"http", "sse", "stdio"}

var mcpStdioAllowedCommandsV198 = map[string]struct{}{
	"deno":    {},
	"docker":  {},
	"node":    {},
	"npx":     {},
	"python":  {},
	"python3": {},
	"uvx":     {},
}

type mcpSafeEnumValidator struct {
	allowed     map[string]struct{}
	description string
}

var _ validator.String = mcpSafeEnumValidator{}

func newMCPSafeEnumValidator(values []string, description string) mcpSafeEnumValidator {
	allowed := make(map[string]struct{}, len(values))
	for _, value := range values {
		allowed[value] = struct{}{}
	}
	return mcpSafeEnumValidator{allowed: allowed, description: description}
}

func (v mcpSafeEnumValidator) Description(context.Context) string {
	return v.description
}

func (v mcpSafeEnumValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v mcpSafeEnumValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, ok := v.allowed[req.ConfigValue.ValueString()]; !ok {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid MCP Configuration", v.description)
	}
}

func NewMCPServerResource() resource.Resource {
	return &MCPServerResource{}
}

type MCPServerResource struct {
	client *Client
}

type MCPServerCostInfoModel struct {
	DefaultCostPerQuery    types.Float64 `tfsdk:"default_cost_per_query"`
	ToolNameToCostPerQuery types.Map     `tfsdk:"tool_name_to_cost_per_query"`
}

type MCPInfoModel struct {
	ServerName        types.String            `tfsdk:"server_name"`
	Description       types.String            `tfsdk:"description"`
	LogoURL           types.String            `tfsdk:"logo_url"`
	MCPServerCostInfo *MCPServerCostInfoModel `tfsdk:"mcp_server_cost_info"`
}

type MCPServerResourceModel struct {
	ID              types.String  `tfsdk:"id"`
	ServerID        types.String  `tfsdk:"server_id"`
	ServerName      types.String  `tfsdk:"server_name"`
	Alias           types.String  `tfsdk:"alias"`
	Description     types.String  `tfsdk:"description"`
	URL             types.String  `tfsdk:"url"`
	SpecPath        types.String  `tfsdk:"spec_path"`
	Transport       types.String  `tfsdk:"transport"`
	SpecVersion     types.String  `tfsdk:"spec_version"`
	AuthType        types.String  `tfsdk:"auth_type"`
	MCPAccessGroups types.List    `tfsdk:"mcp_access_groups"`
	Command         types.String  `tfsdk:"command"`
	Args            types.List    `tfsdk:"args"`
	Env             types.Map     `tfsdk:"env"`
	MCPInfo         *MCPInfoModel `tfsdk:"mcp_info"`
	// New fields for expanded API support
	Credentials       types.Map    `tfsdk:"credentials"`
	AllowedTools      types.List   `tfsdk:"allowed_tools"`
	ExtraHeaders      types.List   `tfsdk:"extra_headers"`
	StaticHeaders     types.Map    `tfsdk:"static_headers"`
	AuthorizationURL  types.String `tfsdk:"authorization_url"`
	TokenURL          types.String `tfsdk:"token_url"`
	RegistrationURL   types.String `tfsdk:"registration_url"`
	AllowAllKeys      types.Bool   `tfsdk:"allow_all_keys"`
	SkipURLValidation types.Bool   `tfsdk:"skip_url_validation"`
	// Computed fields
	CreatedAt types.String `tfsdk:"created_at"`
	CreatedBy types.String `tfsdk:"created_by"`
}

func (r *MCPServerResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_server"
}

func (r *MCPServerResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM MCP (Model Context Protocol) server.",
		Version:     1,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this MCP server (same as server_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"server_id": schema.StringAttribute{
				Description: "Unique identifier for the MCP server.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"server_name": schema.StringAttribute{
				Description: "Name of the MCP server.",
				Required:    true,
			},
			"alias": schema.StringAttribute{
				Description: "Alias for the MCP server.",
				Optional:    true,
			},
			"description": schema.StringAttribute{
				Description: "Description of the MCP server.",
				Optional:    true,
			},
			"url": schema.StringAttribute{
				Description: "URL of the MCP server. HTTP and SSE transports require url or spec_path; stdio does not require a URL.",
				Optional:    true,
			},
			"spec_path": schema.StringAttribute{
				Description: "Path or URL of an OpenAPI specification. For HTTP and SSE transports this can be used instead of url.",
				Optional:    true,
			},
			"transport": schema.StringAttribute{
				Description: "Transport type for the MCP server (http, sse, stdio).",
				Required:    true,
				Validators: []validator.String{
					newMCPSafeEnumValidator(mcpTransportsV198, "Transport must be one of the values accepted by LiteLLM v1.98."),
				},
			},
			"spec_version": schema.StringAttribute{
				Description:        "Deprecated compatibility attribute. LiteLLM v1.98 does not accept or return this field.",
				DeprecationMessage: "spec_version is retained only for state and HCL compatibility and is not sent to LiteLLM. Remove it from configuration.",
				Optional:           true,
				Computed:           true,
				Default:            stringdefault.StaticString("2024-11-05"),
			},
			"auth_type": schema.StringAttribute{
				Description: "Authentication type accepted by the LiteLLM v1.98 MCP server request contract.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("none"),
				Validators: []validator.String{
					newMCPSafeEnumValidator(mcpAuthTypesV198, "Authentication type must be one of the values accepted by LiteLLM v1.98."),
				},
			},
			"mcp_access_groups": schema.ListAttribute{
				Description: "List of access groups for the MCP server.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"command": schema.StringAttribute{
				Description: "Command to run for stdio transport.",
				Optional:    true,
			},
			"args": schema.ListAttribute{
				Description: "Arguments for the command (stdio transport).",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"env": schema.MapAttribute{
				Description: "Environment variables for the command (stdio transport).",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"credentials": schema.MapAttribute{
				Description: "Credentials map for the MCP server authentication.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				ElementType: types.StringType,
			},
			"allowed_tools": schema.ListAttribute{
				Description: "List of allowed tool names for this MCP server.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"extra_headers": schema.ListAttribute{
				Description: "Extra header names to forward to the MCP server.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"static_headers": schema.MapAttribute{
				Description: "Static headers to always include with requests.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"authorization_url": schema.StringAttribute{
				Description: "OAuth authorization URL for the MCP server.",
				Optional:    true,
			},
			"token_url": schema.StringAttribute{
				Description: "OAuth token URL for the MCP server.",
				Optional:    true,
			},
			"registration_url": schema.StringAttribute{
				Description: "OAuth registration URL for the MCP server.",
				Optional:    true,
			},
			"allow_all_keys": schema.BoolAttribute{
				Description: "Whether to allow all API keys to access this MCP server.",
				Optional:    true,
			},
			"skip_url_validation": schema.BoolAttribute{
				Description:        "Deprecated compatibility attribute. LiteLLM v1.98 does not accept this field; new or changed true values are unsafe, while unchanged historical state remains plannable.",
				DeprecationMessage: "skip_url_validation is retained only for state and HCL compatibility and is not sent to LiteLLM. Remove it from configuration.",
				Optional:           true,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the server was created.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_by": schema.StringAttribute{
				Description: "User who created the server.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
		Blocks: map[string]schema.Block{
			"mcp_info": schema.SingleNestedBlock{
				Description: "MCP server information and configuration.",
				Attributes: map[string]schema.Attribute{
					"server_name": schema.StringAttribute{
						Description: "Server name in MCP info.",
						Optional:    true,
					},
					"description": schema.StringAttribute{
						Description: "Description in MCP info.",
						Optional:    true,
					},
					"logo_url": schema.StringAttribute{
						Description: "Logo URL for the MCP server.",
						Optional:    true,
					},
				},
				Blocks: map[string]schema.Block{
					"mcp_server_cost_info": schema.SingleNestedBlock{
						Description: "Cost information for MCP server tools.",
						Attributes: map[string]schema.Attribute{
							"default_cost_per_query": schema.Float64Attribute{
								Description: "Default cost per query.",
								Optional:    true,
							},
							"tool_name_to_cost_per_query": schema.MapAttribute{
								Description: "Map of tool names to their cost per query.",
								Optional:    true,
								Computed:    true,
								ElementType: types.Float64Type,
								Validators:  []validator.Map{mapvalidator.NoNullValues()},
							},
						},
					},
				},
			},
		},
	}
}

func (r *MCPServerResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data MCPServerResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for name, value := range map[string]types.String{"url": data.URL, "spec_path": data.SpecPath} {
		if !value.IsNull() && !value.IsUnknown() && value.ValueString() == "" {
			resp.Diagnostics.AddAttributeError(
				path.Root(name),
				"Invalid MCP Endpoint Configuration",
				"Configured MCP endpoint fields must be non-empty. Omit the field to clear or release it.",
			)
		}
	}
	if resp.Diagnostics.HasError() || data.Transport.IsNull() || data.Transport.IsUnknown() {
		return
	}
	switch data.Transport.ValueString() {
	case "http", "sse":
		if data.URL.IsUnknown() || data.SpecPath.IsUnknown() {
			return
		}
		if !mcpKnownNonEmptyString(data.URL) && !mcpKnownNonEmptyString(data.SpecPath) {
			resp.Diagnostics.AddAttributeError(
				path.Root("url"),
				"Invalid MCP Transport Configuration",
				"HTTP and SSE configurations require at least one non-empty endpoint field: url or spec_path.",
			)
		}
	case "stdio":
		if !data.Command.IsUnknown() && !mcpKnownNonEmptyString(data.Command) {
			resp.Diagnostics.AddAttributeError(
				path.Root("command"),
				"Invalid MCP Stdio Configuration",
				"A non-empty command is required for stdio transport.",
			)
		}
		if !data.Args.IsUnknown() && (data.Args.IsNull() || len(data.Args.Elements()) == 0) {
			resp.Diagnostics.AddAttributeError(
				path.Root("args"),
				"Invalid MCP Stdio Configuration",
				"At least one command argument is required for stdio transport.",
			)
		}
		if mcpKnownNonEmptyString(data.Command) {
			if _, ok := mcpStdioAllowedCommandsV198[mcpStdioCommandBaseV198(data.Command.ValueString())]; !ok {
				resp.Diagnostics.AddAttributeError(
					path.Root("command"),
					"Invalid MCP Stdio Configuration",
					"The command executable is not in LiteLLM v1.98's built-in stdio allowlist: deno, docker, node, npx, python, python3, uvx.",
				)
			}
		}
	}
}

func (r *MCPServerResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Destroy must remain possible for every historical phantom value.
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan MCPServerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state MCPServerResourceModel
	hasState := !req.State.Raw.IsNull()
	if hasState {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	unsupportedSpecVersion := !plan.SpecVersion.IsNull() && !plan.SpecVersion.IsUnknown() && plan.SpecVersion.ValueString() != "2024-11-05"
	unchangedSpecVersion := hasState && !state.SpecVersion.IsUnknown() && state.SpecVersion.Equal(plan.SpecVersion)
	if unsupportedSpecVersion && !unchangedSpecVersion {
		resp.Diagnostics.AddAttributeError(
			path.Root("spec_version"),
			"Unsupported Deprecated MCP Configuration",
			"LiteLLM v1.98 does not accept this compatibility field. A historical non-default value may remain unchanged, but new or changed non-default values are unsafe.",
		)
	}

	unsupportedSkipValidation := !plan.SkipURLValidation.IsNull() && !plan.SkipURLValidation.IsUnknown() && plan.SkipURLValidation.ValueBool()
	unchangedSkipValidation := hasState && !state.SkipURLValidation.IsUnknown() && state.SkipURLValidation.Equal(plan.SkipURLValidation)
	if unsupportedSkipValidation && !unchangedSkipValidation {
		resp.Diagnostics.AddAttributeError(
			path.Root("skip_url_validation"),
			"Unsupported Deprecated MCP Configuration",
			"LiteLLM v1.98 does not accept this compatibility field. A historical true value may remain unchanged, but a new or changed true value is unsafe.",
		)
	}
}

func validateMCPServerOptionalResponseFields(result map[string]interface{}, stringFields, boolFields, stringListFields, stringMapFields []string) error {
	for _, field := range stringFields {
		if value, present := result[field]; present && value != nil {
			if _, ok := value.(string); !ok {
				return fmt.Errorf("MCP server response contains a malformed optional string field")
			}
		}
	}
	for _, field := range boolFields {
		if value, present := result[field]; present && value != nil {
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("MCP server response contains a malformed optional boolean field")
			}
		}
	}
	for _, field := range stringListFields {
		value, present := result[field]
		if !present || value == nil {
			continue
		}
		items, ok := value.([]interface{})
		if !ok {
			return fmt.Errorf("MCP server response contains a malformed optional string-list field")
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("MCP server response contains a malformed optional string-list field")
			}
		}
	}
	for _, field := range stringMapFields {
		value, present := result[field]
		if !present || value == nil {
			continue
		}
		items, ok := value.(map[string]interface{})
		if !ok {
			return fmt.Errorf("MCP server response contains a malformed optional string-map field")
		}
		for _, item := range items {
			if _, ok := item.(string); !ok {
				return fmt.Errorf("MCP server response contains a malformed optional string-map field")
			}
		}
	}
	return nil
}

func mcpKnownNonEmptyString(value types.String) bool {
	return !value.IsNull() && !value.IsUnknown() && value.ValueString() != ""
}

// Python's os.path.basename on the LiteLLM runtime is deliberately not the
// same as path.Base: it does not clean the path, and a trailing slash yields an
// empty basename. Matching that behavior keeps plan validation wire-exact.
func mcpStdioCommandBaseV198(command string) string {
	if slash := strings.LastIndex(command, "/"); slash >= 0 {
		return command[slash+1:]
	}
	return command
}

func (r *MCPServerResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *MCPServerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data MCPServerResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	mcpReq, err := r.buildMCPServerRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid MCP Numeric Map", err.Error())
		return
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/v1/mcp/server", mcpReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create MCP server: %s", err))
		return
	}

	serverID, ok := result["server_id"].(string)
	if !ok || serverID == "" {
		resp.Diagnostics.AddError("Invalid Create Response", "LiteLLM accepted the MCP server create but returned a malformed required response shape.")
		return
	}
	data.ServerID = types.StringValue(serverID)
	data.ID = types.StringValue(serverID)
	if err := validateMCPServerResponse(result, serverID); err != nil {
		partial := partialMCPServerState(serverID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
		resp.Diagnostics.AddError("Invalid Create Response", "LiteLLM accepted the MCP server create but returned a malformed required response shape. Only the confirmed identity was retained for recovery.")
		return
	}

	// Require authoritative post-create confirmation. A successful POST with a
	// failed or inconsistent read retains only the confirmed identity so the
	// remote object remains recoverable without publishing planned endpoint data.
	planned := data
	if err := r.readMCPServer(ctx, &data); err != nil {
		partial := partialMCPServerState(serverID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
		resp.Diagnostics.AddError("MCP Server Readback Not Confirmed", "LiteLLM accepted the create, but authoritative readback failed. Only the confirmed identity was retained for recovery.")
		return
	}
	if mcpOwnedEndpointReadbackMismatch(&planned, &data, nil) {
		partial := partialMCPServerState(serverID)
		resp.Diagnostics.Append(resp.State.Set(ctx, &partial)...)
		resp.Diagnostics.AddError("Inconsistent MCP Endpoint Readback", "LiteLLM accepted the create but did not persist the requested endpoint or transport. Only the confirmed identity was retained for recovery.")
		return
	}
	resolveUnknownMCPServerState(&data, nil)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *MCPServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data MCPServerResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	importedMarker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(importedMarker) == "true"

	if err := r.readMCPServerWithNumericOwnership(ctx, &data, imported); err != nil {
		if IsAPIErrorStatus(err, 404) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read MCP server: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && imported {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
}

func (r *MCPServerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data MCPServerResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state MCPServerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve the server ID
	data.ID = state.ID
	data.ServerID = state.ServerID

	mcpReq, err := r.buildMCPServerRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid MCP Numeric Map", err.Error())
		return
	}
	mcpReq["server_id"] = data.ServerID.ValueString()
	if !state.URL.IsNull() && !state.URL.IsUnknown() && data.URL.IsNull() {
		mcpReq["url"] = nil
	}
	if !state.SpecPath.IsNull() && !state.SpecPath.IsUnknown() && data.SpecPath.IsNull() {
		mcpReq["spec_path"] = nil
	}

	var updateResult map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "PUT", "/v1/mcp/server", mcpReq, &updateResult); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update MCP server: %s", err))
		return
	}
	if len(updateResult) > 0 {
		if err := validateMCPServerResponse(updateResult, data.ServerID.ValueString()); err != nil {
			resp.Diagnostics.AddError("Invalid Update Response", "LiteLLM accepted the MCP server update but returned a malformed required response shape. Prior state was preserved.")
			return
		}
	}

	// The partial PUT response is not sufficient to prove convergence. A failed
	// or malformed authoritative read preserves prior state instead of publishing
	// requested values as confirmed.
	planned := data
	// A planned null after prior ownership is an explicit clear. Probe that
	// field authoritatively even though it will be unowned after convergence.
	if mcpEndpointWasCleared(state.URL, planned.URL) {
		data.URL = types.StringUnknown()
	}
	if mcpEndpointWasCleared(state.SpecPath, planned.SpecPath) {
		data.SpecPath = types.StringUnknown()
	}
	if err := r.readMCPServer(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Read Error", fmt.Sprintf("MCP server update was accepted but authoritative readback failed: %s", err))
		return
	}
	if mcpOwnedEndpointReadbackMismatch(&planned, &data, &state) {
		resp.Diagnostics.AddError("Inconsistent MCP Endpoint Readback", "LiteLLM accepted the update but did not persist the requested endpoint or transport. Prior Terraform state was retained for recovery.")
		return
	}
	resolveUnknownMCPServerState(&data, &state)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *MCPServerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data MCPServerResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := data.ID.ValueString()
	if serverID == "" {
		serverID = data.ServerID.ValueString()
	}

	endpoint := mcpServerEndpoint(serverID)
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsAPIErrorStatus(err, 404) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete MCP server: %s", err))
			return
		}
	}
}

func (r *MCPServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), req.ID)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
	}
}

// UpgradeState handles state migrations from older schema versions.
// Version 0 → 1: extra_headers changed from map(string) to list(string)
// to match the LiteLLM API/OpenAPI schema. Existing map keys become the
// list of header names.
func (r *MCPServerResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: nil,
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				if req.RawState == nil {
					resp.Diagnostics.AddError(
						"Unable to Upgrade State",
						"RawState is nil. This is a bug in the provider.",
					)
					return
				}

				var priorState map[string]json.RawMessage
				if err := json.Unmarshal(req.RawState.JSON, &priorState); err != nil {
					resp.Diagnostics.AddError(
						"Unable to Upgrade State",
						fmt.Sprintf("Failed to unmarshal prior state JSON: %s", err),
					)
					return
				}

				if raw, ok := priorState["extra_headers"]; ok && string(raw) != "null" {
					var oldMap map[string]string
					if err := json.Unmarshal(raw, &oldMap); err != nil {
						resp.Diagnostics.AddError("Unable to Upgrade State", "Failed to decode legacy extra_headers.")
						return
					}
					headers := make([]string, 0, len(oldMap))
					for header := range oldMap {
						headers = append(headers, header)
					}
					sort.Strings(headers)
					converted, err := json.Marshal(headers)
					if err != nil {
						resp.Diagnostics.AddError(
							"Unable to Upgrade State",
							fmt.Sprintf("Failed to marshal upgraded extra_headers: %s", err),
						)
						return
					}
					priorState["extra_headers"] = converted
				}

				upgradedJSON, err := json.Marshal(priorState)
				if err != nil {
					resp.Diagnostics.AddError(
						"Unable to Upgrade State",
						fmt.Sprintf("Failed to marshal upgraded state: %s", err),
					)
					return
				}

				resp.DynamicValue = &tfprotov6.DynamicValue{JSON: upgradedJSON}
			},
		},
	}
}

func (r *MCPServerResource) buildMCPServerRequest(ctx context.Context, data *MCPServerResourceModel) (map[string]interface{}, error) {
	mcpReq := map[string]interface{}{
		"server_name": data.ServerName.ValueString(),
		"transport":   data.Transport.ValueString(),
		"auth_type":   data.AuthType.ValueString(),
	}

	// String fields - check IsNull, IsUnknown, and empty string.
	if mcpKnownNonEmptyString(data.URL) {
		mcpReq["url"] = data.URL.ValueString()
	}
	if mcpKnownNonEmptyString(data.SpecPath) {
		mcpReq["spec_path"] = data.SpecPath.ValueString()
	}
	if !data.Alias.IsNull() && !data.Alias.IsUnknown() && data.Alias.ValueString() != "" {
		mcpReq["alias"] = data.Alias.ValueString()
	}
	if !data.Description.IsNull() && !data.Description.IsUnknown() && data.Description.ValueString() != "" {
		mcpReq["description"] = data.Description.ValueString()
	}
	if !data.Command.IsNull() && !data.Command.IsUnknown() && data.Command.ValueString() != "" {
		mcpReq["command"] = data.Command.ValueString()
	}
	if !data.AuthorizationURL.IsNull() && !data.AuthorizationURL.IsUnknown() && data.AuthorizationURL.ValueString() != "" {
		mcpReq["authorization_url"] = data.AuthorizationURL.ValueString()
	}
	if !data.TokenURL.IsNull() && !data.TokenURL.IsUnknown() && data.TokenURL.ValueString() != "" {
		mcpReq["token_url"] = data.TokenURL.ValueString()
	}
	if !data.RegistrationURL.IsNull() && !data.RegistrationURL.IsUnknown() && data.RegistrationURL.ValueString() != "" {
		mcpReq["registration_url"] = data.RegistrationURL.ValueString()
	}

	// Boolean fields - check IsNull and IsUnknown
	if !data.AllowAllKeys.IsNull() && !data.AllowAllKeys.IsUnknown() {
		mcpReq["allow_all_keys"] = data.AllowAllKeys.ValueBool()
	}

	// List fields - check IsNull, IsUnknown, and len > 0
	if !data.MCPAccessGroups.IsNull() && !data.MCPAccessGroups.IsUnknown() {
		var groups []string
		data.MCPAccessGroups.ElementsAs(ctx, &groups, false)
		if len(groups) > 0 {
			mcpReq["mcp_access_groups"] = groups
		}
	}

	if !data.Args.IsNull() && !data.Args.IsUnknown() {
		var args []string
		data.Args.ElementsAs(ctx, &args, false)
		if len(args) > 0 {
			mcpReq["args"] = args
		}
	}

	if !data.AllowedTools.IsNull() && !data.AllowedTools.IsUnknown() {
		var allowedTools []string
		data.AllowedTools.ElementsAs(ctx, &allowedTools, false)
		if len(allowedTools) > 0 {
			mcpReq["allowed_tools"] = allowedTools
		}
	}

	// Map fields - check IsNull, IsUnknown, and len > 0
	if !data.Env.IsNull() && !data.Env.IsUnknown() {
		var env map[string]string
		data.Env.ElementsAs(ctx, &env, false)
		if len(env) > 0 {
			mcpReq["env"] = env
		}
	}

	if !data.Credentials.IsNull() && !data.Credentials.IsUnknown() {
		var credentials map[string]string
		data.Credentials.ElementsAs(ctx, &credentials, false)
		if len(credentials) > 0 {
			mcpReq["credentials"] = credentials
		}
	}

	if !data.ExtraHeaders.IsNull() && !data.ExtraHeaders.IsUnknown() {
		var extraHeaders []string
		data.ExtraHeaders.ElementsAs(ctx, &extraHeaders, false)
		if len(extraHeaders) > 0 {
			mcpReq["extra_headers"] = extraHeaders
		}
	}

	if !data.StaticHeaders.IsNull() && !data.StaticHeaders.IsUnknown() {
		var staticHeaders map[string]string
		data.StaticHeaders.ElementsAs(ctx, &staticHeaders, false)
		if len(staticHeaders) > 0 {
			mcpReq["static_headers"] = staticHeaders
		}
	}

	// Handle mcp_info block
	if data.MCPInfo != nil {
		mcpInfo := map[string]interface{}{}

		if !data.MCPInfo.ServerName.IsNull() && !data.MCPInfo.ServerName.IsUnknown() && data.MCPInfo.ServerName.ValueString() != "" {
			mcpInfo["server_name"] = data.MCPInfo.ServerName.ValueString()
		}
		if !data.MCPInfo.Description.IsNull() && !data.MCPInfo.Description.IsUnknown() && data.MCPInfo.Description.ValueString() != "" {
			mcpInfo["description"] = data.MCPInfo.Description.ValueString()
		}
		if !data.MCPInfo.LogoURL.IsNull() && !data.MCPInfo.LogoURL.IsUnknown() && data.MCPInfo.LogoURL.ValueString() != "" {
			mcpInfo["logo_url"] = data.MCPInfo.LogoURL.ValueString()
		}

		if data.MCPInfo.MCPServerCostInfo != nil {
			costInfo := map[string]interface{}{}

			if !data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.IsNull() && !data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.IsUnknown() {
				costInfo["default_cost_per_query"] = data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.ValueFloat64()
			}
			if !data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsNull() && !data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsUnknown() {
				toolCosts, err := float64RequestMap(data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery, "mcp_info.mcp_server_cost_info.tool_name_to_cost_per_query")
				if err != nil {
					return nil, err
				}
				if len(toolCosts) > 0 {
					costInfo["tool_name_to_cost_per_query"] = toolCosts
				}
			}

			if len(costInfo) > 0 {
				mcpInfo["mcp_server_cost_info"] = costInfo
			}
		}

		if len(mcpInfo) > 0 {
			mcpReq["mcp_info"] = mcpInfo
		}
	}

	return mcpReq, nil
}

func mcpEndpointWasCleared(previous, planned types.String) bool {
	return !previous.IsNull() && !previous.IsUnknown() && planned.IsNull()
}

func mcpOwnedEndpointReadbackMismatch(planned, observed, previous *MCPServerResourceModel) bool {
	plannedFields := []types.String{planned.URL, planned.SpecPath, planned.Transport}
	observedFields := []types.String{observed.URL, observed.SpecPath, observed.Transport}
	previousFields := []types.String{types.StringNull(), types.StringNull(), types.StringNull()}
	if previous != nil {
		previousFields = []types.String{previous.URL, previous.SpecPath, previous.Transport}
	}
	for index := range plannedFields {
		if !plannedFields[index].IsNull() && !plannedFields[index].IsUnknown() {
			if !plannedFields[index].Equal(observedFields[index]) {
				return true
			}
			continue
		}
		if mcpEndpointWasCleared(previousFields[index], plannedFields[index]) && !observedFields[index].IsNull() {
			return true
		}
	}
	return false
}

func partialMCPServerState(serverID string) MCPServerResourceModel {
	return MCPServerResourceModel{
		ID:              types.StringValue(serverID),
		ServerID:        types.StringValue(serverID),
		MCPAccessGroups: types.ListNull(types.StringType),
		Args:            types.ListNull(types.StringType),
		Env:             types.MapNull(types.StringType),
		Credentials:     types.MapNull(types.StringType),
		AllowedTools:    types.ListNull(types.StringType),
		ExtraHeaders:    types.ListNull(types.StringType),
		StaticHeaders:   types.MapNull(types.StringType),
	}
}

func resolveUnknownMCPServerState(data *MCPServerResourceModel, previous *MCPServerResourceModel) {
	var prior MCPServerResourceModel
	if previous != nil {
		prior = *previous
	}

	resolveString := func(current, fallback types.String) types.String {
		if !current.IsUnknown() {
			return current
		}
		if previous != nil && !fallback.IsUnknown() {
			return fallback
		}
		return types.StringNull()
	}
	resolveBool := func(current, fallback types.Bool) types.Bool {
		if !current.IsUnknown() {
			return current
		}
		if previous != nil && !fallback.IsUnknown() {
			return fallback
		}
		return types.BoolNull()
	}
	resolveList := func(current, fallback types.List) types.List {
		if !current.IsUnknown() {
			return current
		}
		if previous != nil && !fallback.IsUnknown() {
			return fallback
		}
		return types.ListNull(types.StringType)
	}
	resolveStringMap := func(current, fallback types.Map) types.Map {
		if !current.IsUnknown() {
			return current
		}
		if previous != nil && !fallback.IsUnknown() {
			return fallback
		}
		return types.MapNull(types.StringType)
	}

	data.ID = resolveString(data.ID, prior.ID)
	data.ServerID = resolveString(data.ServerID, prior.ServerID)
	data.ServerName = resolveString(data.ServerName, prior.ServerName)
	data.Alias = resolveString(data.Alias, prior.Alias)
	data.Description = resolveString(data.Description, prior.Description)
	data.URL = resolveString(data.URL, prior.URL)
	data.SpecPath = resolveString(data.SpecPath, prior.SpecPath)
	data.Transport = resolveString(data.Transport, prior.Transport)
	data.SpecVersion = resolveString(data.SpecVersion, prior.SpecVersion)
	data.AuthType = resolveString(data.AuthType, prior.AuthType)
	data.Command = resolveString(data.Command, prior.Command)
	data.AuthorizationURL = resolveString(data.AuthorizationURL, prior.AuthorizationURL)
	data.TokenURL = resolveString(data.TokenURL, prior.TokenURL)
	data.RegistrationURL = resolveString(data.RegistrationURL, prior.RegistrationURL)
	data.CreatedAt = resolveString(data.CreatedAt, prior.CreatedAt)
	data.CreatedBy = resolveString(data.CreatedBy, prior.CreatedBy)
	data.AllowAllKeys = resolveBool(data.AllowAllKeys, prior.AllowAllKeys)
	data.SkipURLValidation = resolveBool(data.SkipURLValidation, prior.SkipURLValidation)
	data.MCPAccessGroups = resolveList(data.MCPAccessGroups, prior.MCPAccessGroups)
	data.Args = resolveList(data.Args, prior.Args)
	data.AllowedTools = resolveList(data.AllowedTools, prior.AllowedTools)
	data.ExtraHeaders = resolveList(data.ExtraHeaders, prior.ExtraHeaders)
	data.Env = resolveStringMap(data.Env, prior.Env)
	data.Credentials = resolveStringMap(data.Credentials, prior.Credentials)
	data.StaticHeaders = resolveStringMap(data.StaticHeaders, prior.StaticHeaders)

	if data.MCPInfo != nil {
		var priorInfo MCPInfoModel
		if prior.MCPInfo != nil {
			priorInfo = *prior.MCPInfo
		}
		data.MCPInfo.ServerName = resolveString(data.MCPInfo.ServerName, priorInfo.ServerName)
		data.MCPInfo.Description = resolveString(data.MCPInfo.Description, priorInfo.Description)
		data.MCPInfo.LogoURL = resolveString(data.MCPInfo.LogoURL, priorInfo.LogoURL)
		if data.MCPInfo.MCPServerCostInfo != nil {
			var priorCost MCPServerCostInfoModel
			hasPriorCost := priorInfo.MCPServerCostInfo != nil
			if hasPriorCost {
				priorCost = *priorInfo.MCPServerCostInfo
			}
			if data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.IsUnknown() {
				if hasPriorCost && !priorCost.DefaultCostPerQuery.IsUnknown() {
					data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery = priorCost.DefaultCostPerQuery
				} else {
					data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery = types.Float64Null()
				}
			}
			if data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsUnknown() {
				if hasPriorCost && !priorCost.ToolNameToCostPerQuery.IsUnknown() {
					data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery = priorCost.ToolNameToCostPerQuery
				} else {
					data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery = types.MapNull(types.Float64Type)
				}
			}
		}
	}
}

func mcpServerEndpoint(serverID string) string {
	return "/v1/mcp/server/" + url.PathEscape(serverID)
}

func (r *MCPServerResource) getMCPServer(ctx context.Context, serverID string) (map[string]interface{}, error) {
	endpoint := mcpServerEndpoint(serverID)
	var result map[string]interface{}
	individualErr := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result)
	if individualErr == nil || IsAPIErrorStatus(individualErr, 404) {
		return result, individualErr
	}

	// Older LiteLLM versions can return 500 for the individual endpoint while
	// the collection endpoint still returns the successfully created server.
	delay := 250 * time.Millisecond
	for attempt := 0; attempt < 5; attempt++ {
		var servers []map[string]interface{}
		if err := r.client.DoRequestWithResponse(ctx, "GET", "/v1/mcp/server", nil, &servers); err == nil {
			for _, server := range servers {
				if id, ok := server["server_id"].(string); ok && id == serverID {
					return server, nil
				}
			}
		}
		if attempt == 4 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		delay *= 2
	}
	return nil, individualErr
}

func validateMCPServerResponse(result map[string]interface{}, expectedServerID string) error {
	serverID, ok := result["server_id"].(string)
	if !ok || serverID == "" {
		return fmt.Errorf("MCP server response is missing its required identity")
	}
	if expectedServerID == "" || serverID != expectedServerID {
		return fmt.Errorf("MCP server response identity does not match the requested identity")
	}

	transport, ok := result["transport"].(string)
	if !ok {
		return fmt.Errorf("MCP server response is missing its required transport")
	}
	validTransport := false
	for _, allowed := range mcpTransportsV198 {
		if transport == allowed {
			validTransport = true
			break
		}
	}
	if !validTransport {
		return fmt.Errorf("MCP server response contains an invalid transport")
	}
	return validateMCPServerOptionalResponseFields(
		result,
		[]string{"server_name", "url", "spec_path"},
		nil,
		nil,
		nil,
	)
}

func (r *MCPServerResource) readMCPServer(ctx context.Context, data *MCPServerResourceModel) error {
	return r.readMCPServerWithNumericOwnership(ctx, data, false)
}

func (r *MCPServerResource) readMCPServerWithNumericOwnership(ctx context.Context, data *MCPServerResourceModel, imported bool) error {
	serverID := data.ID.ValueString()
	if serverID == "" {
		serverID = data.ServerID.ValueString()
	}

	result, err := r.getMCPServer(ctx, serverID)
	if err != nil {
		return err
	}
	if err := validateMCPServerResponse(result, serverID); err != nil {
		return err
	}

	// Update fields from response
	if serverID, ok := result["server_id"].(string); ok {
		data.ServerID = types.StringValue(serverID)
		data.ID = types.StringValue(serverID)
	}
	if serverName, ok := result["server_name"].(string); ok {
		data.ServerName = types.StringValue(serverName)
	}
	if alias, ok := result["alias"].(string); ok && !data.Alias.IsNull() {
		data.Alias = types.StringValue(alias)
	}
	if desc, ok := result["description"].(string); ok && !data.Description.IsNull() {
		data.Description = types.StringValue(desc)
	}
	urlOwned := imported || !data.URL.IsNull()
	if urlOwned {
		if remoteURL, ok := result["url"].(string); ok {
			data.URL = types.StringValue(remoteURL)
		} else {
			data.URL = types.StringNull()
		}
	}
	specPathOwned := imported || !data.SpecPath.IsNull()
	if specPathOwned {
		if specPath, ok := result["spec_path"].(string); ok {
			data.SpecPath = types.StringValue(specPath)
		} else {
			data.SpecPath = types.StringNull()
		}
	}
	if transport, ok := result["transport"].(string); ok {
		data.Transport = types.StringValue(transport)
	}
	if authType, ok := result["auth_type"].(string); ok {
		data.AuthType = types.StringValue(authType)
	}
	if command, ok := result["command"].(string); ok && (imported || !data.Command.IsNull()) {
		data.Command = types.StringValue(command)
	}
	if createdAt, ok := result["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(createdAt)
	}
	if createdBy, ok := result["created_by"].(string); ok {
		data.CreatedBy = types.StringValue(createdBy)
	}
	// Handle access groups - preserve null when API returns empty and config didn't specify
	if accessGroups, ok := result["mcp_access_groups"].([]interface{}); ok && len(accessGroups) > 0 {
		groups := make([]attr.Value, len(accessGroups))
		for i, g := range accessGroups {
			if str, ok := g.(string); ok {
				groups[i] = types.StringValue(str)
			}
		}
		data.MCPAccessGroups, _ = types.ListValue(types.StringType, groups)
	} else if data.MCPAccessGroups.IsUnknown() {
		data.MCPAccessGroups, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle args - preserve null when API returns empty and config didn't specify
	if args, ok := result["args"].([]interface{}); ok && len(args) > 0 {
		argsList := make([]attr.Value, len(args))
		for i, a := range args {
			if str, ok := a.(string); ok {
				argsList[i] = types.StringValue(str)
			}
		}
		data.Args, _ = types.ListValue(types.StringType, argsList)
	} else if data.Args.IsUnknown() {
		data.Args, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle env - preserve null when API returns empty and config didn't specify
	if env, ok := result["env"].(map[string]interface{}); ok && len(env) > 0 {
		envMap := make(map[string]attr.Value)
		for k, v := range env {
			if str, ok := v.(string); ok {
				envMap[k] = types.StringValue(str)
			}
		}
		data.Env, _ = types.MapValue(types.StringType, envMap)
	} else if data.Env.IsUnknown() {
		data.Env, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle credentials - preserve null when API returns empty and config didn't specify
	if credentials, ok := result["credentials"].(map[string]interface{}); ok && len(credentials) > 0 {
		credMap := make(map[string]attr.Value)
		for k, v := range credentials {
			if str, ok := v.(string); ok {
				credMap[k] = types.StringValue(str)
			}
		}
		data.Credentials, _ = types.MapValue(types.StringType, credMap)
	} else if data.Credentials.IsUnknown() {
		data.Credentials, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle allowed_tools - preserve null when API returns empty and config didn't specify
	if allowedTools, ok := result["allowed_tools"].([]interface{}); ok && len(allowedTools) > 0 {
		tools := make([]attr.Value, len(allowedTools))
		for i, t := range allowedTools {
			if str, ok := t.(string); ok {
				tools[i] = types.StringValue(str)
			}
		}
		data.AllowedTools, _ = types.ListValue(types.StringType, tools)
	} else if data.AllowedTools.IsUnknown() {
		data.AllowedTools, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle extra_headers - preserve null when API returns empty and config didn't specify
	if extraHeaders, ok := result["extra_headers"].([]interface{}); ok && len(extraHeaders) > 0 {
		headers := make([]attr.Value, 0, len(extraHeaders))
		for _, v := range extraHeaders {
			if str, ok := v.(string); ok {
				headers = append(headers, types.StringValue(str))
			}
		}
		data.ExtraHeaders, _ = types.ListValue(types.StringType, headers)
	} else if data.ExtraHeaders.IsUnknown() {
		data.ExtraHeaders, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle static_headers - preserve null when API returns empty and config didn't specify
	if staticHeaders, ok := result["static_headers"].(map[string]interface{}); ok && len(staticHeaders) > 0 {
		headersMap := make(map[string]attr.Value)
		for k, v := range staticHeaders {
			if str, ok := v.(string); ok {
				headersMap[k] = types.StringValue(str)
			}
		}
		data.StaticHeaders, _ = types.MapValue(types.StringType, headersMap)
	} else if data.StaticHeaders.IsUnknown() {
		data.StaticHeaders, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	if authURL, ok := result["authorization_url"].(string); ok && !data.AuthorizationURL.IsNull() {
		data.AuthorizationURL = types.StringValue(authURL)
	}

	if tokenURL, ok := result["token_url"].(string); ok && !data.TokenURL.IsNull() {
		data.TokenURL = types.StringValue(tokenURL)
	}

	if regURL, ok := result["registration_url"].(string); ok && !data.RegistrationURL.IsNull() {
		data.RegistrationURL = types.StringValue(regURL)
	}

	if allowAllKeys, ok := result["allow_all_keys"].(bool); ok && !data.AllowAllKeys.IsNull() {
		data.AllowAllKeys = types.BoolValue(allowAllKeys)
	}

	// The import marker is deliberately scoped to numeric cost ownership. When
	// mcp_info was not configured, import may create only the nested shells needed
	// to expose visible cost fields; reconstructing heterogeneous mcp_info belongs
	// to #213 and remains out of scope.
	if mcpInfoRaw, ok := result["mcp_info"].(map[string]interface{}); ok && (data.MCPInfo != nil || imported) {
		if data.MCPInfo == nil {
			data.MCPInfo = &MCPInfoModel{}
		}
		if !imported {
			if serverName, ok := mcpInfoRaw["server_name"].(string); ok {
				data.MCPInfo.ServerName = types.StringValue(serverName)
			}
			if description, ok := mcpInfoRaw["description"].(string); ok {
				data.MCPInfo.Description = types.StringValue(description)
			}
			if logoURL, ok := mcpInfoRaw["logo_url"].(string); ok {
				data.MCPInfo.LogoURL = types.StringValue(logoURL)
			}
		}

		if costInfoRaw, ok := mcpInfoRaw["mcp_server_cost_info"].(map[string]interface{}); ok {
			if data.MCPInfo.MCPServerCostInfo == nil {
				data.MCPInfo.MCPServerCostInfo = &MCPServerCostInfoModel{}
			}
			defaultOwned := imported || (!data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.IsNull() && !data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery.IsUnknown())
			if err := updateFloat64FromAPI(&data.MCPInfo.MCPServerCostInfo.DefaultCostPerQuery, costInfoRaw, defaultOwned, defaultOwned, "default_cost_per_query"); err != nil {
				return err
			}

			toolCostsOwned := imported || (!data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsNull() && !data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsUnknown())
			if err := updateFloat64MapFromAPI(&data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery, costInfoRaw, toolCostsOwned, toolCostsOwned, "tool_name_to_cost_per_query"); err != nil {
				return err
			}
		} else if data.MCPInfo.MCPServerCostInfo != nil && data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsUnknown() {
			data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery, _ = types.MapValue(types.Float64Type, map[string]attr.Value{})
		}
	} else if data.MCPInfo != nil && data.MCPInfo.MCPServerCostInfo != nil && data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.IsUnknown() {
		data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery, _ = types.MapValue(types.Float64Type, map[string]attr.Value{})
	}

	return nil
}
