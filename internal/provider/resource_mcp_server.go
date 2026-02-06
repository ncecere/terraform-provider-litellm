package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &MCPServerResource{}
var _ resource.ResourceWithImportState = &MCPServerResource{}

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
	Transport       types.String  `tfsdk:"transport"`
	SpecVersion     types.String  `tfsdk:"spec_version"`
	AuthType        types.String  `tfsdk:"auth_type"`
	MCPAccessGroups types.List    `tfsdk:"mcp_access_groups"`
	Command         types.String  `tfsdk:"command"`
	Args            types.List    `tfsdk:"args"`
	Env             types.Map     `tfsdk:"env"`
	MCPInfo         *MCPInfoModel `tfsdk:"mcp_info"`
	// New fields for expanded API support
	Credentials      types.Map    `tfsdk:"credentials"`
	AllowedTools     types.List   `tfsdk:"allowed_tools"`
	ExtraHeaders     types.Map    `tfsdk:"extra_headers"`
	StaticHeaders    types.Map    `tfsdk:"static_headers"`
	AuthorizationURL types.String `tfsdk:"authorization_url"`
	TokenURL         types.String `tfsdk:"token_url"`
	RegistrationURL  types.String `tfsdk:"registration_url"`
	AllowAllKeys     types.Bool   `tfsdk:"allow_all_keys"`
	// Computed fields
	CreatedAt        types.String `tfsdk:"created_at"`
	CreatedBy        types.String `tfsdk:"created_by"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
	UpdatedBy        types.String `tfsdk:"updated_by"`
	Status           types.String `tfsdk:"status"`
	LastHealthCheck  types.String `tfsdk:"last_health_check"`
	HealthCheckError types.String `tfsdk:"health_check_error"`
}

func (r *MCPServerResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_server"
}

func (r *MCPServerResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM MCP (Model Context Protocol) server.",
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
				Description: "URL of the MCP server.",
				Required:    true,
			},
			"transport": schema.StringAttribute{
				Description: "Transport type for the MCP server (http, sse, stdio).",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("http", "sse", "stdio"),
				},
			},
			"spec_version": schema.StringAttribute{
				Description: "MCP specification version.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("2024-11-05"),
			},
			"auth_type": schema.StringAttribute{
				Description: "Authentication type (none, bearer, basic).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("none"),
				Validators: []validator.String{
					stringvalidator.OneOf("none", "bearer", "basic"),
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
			"extra_headers": schema.MapAttribute{
				Description: "Extra headers to send with requests to the MCP server.",
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
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the server was created.",
				Computed:    true,
			},
			"created_by": schema.StringAttribute{
				Description: "User who created the server.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the server was last updated.",
				Computed:    true,
			},
			"updated_by": schema.StringAttribute{
				Description: "User who last updated the server.",
				Computed:    true,
			},
			"status": schema.StringAttribute{
				Description: "Current status of the MCP server.",
				Computed:    true,
			},
			"last_health_check": schema.StringAttribute{
				Description: "Timestamp of the last health check.",
				Computed:    true,
			},
			"health_check_error": schema.StringAttribute{
				Description: "Error message from the last health check, if any.",
				Computed:    true,
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
							},
						},
					},
				},
			},
		},
	}
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

	mcpReq := r.buildMCPServerRequest(ctx, &data)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/v1/mcp/server", mcpReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create MCP server: %s", err))
		return
	}

	// Extract server_id from response
	if serverID, ok := result["server_id"].(string); ok {
		data.ServerID = types.StringValue(serverID)
		data.ID = types.StringValue(serverID)
	}

	// Read back for full state
	if err := r.readMCPServer(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("MCP server created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *MCPServerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data MCPServerResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readMCPServer(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read MCP server: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
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

	mcpReq := r.buildMCPServerRequest(ctx, &data)
	mcpReq["server_id"] = data.ServerID.ValueString()

	if err := r.client.DoRequestWithResponse(ctx, "PUT", "/v1/mcp/server", mcpReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update MCP server: %s", err))
		return
	}

	// Read back for full state
	if err := r.readMCPServer(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("MCP server updated but failed to read back: %s", err))
	}

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

	endpoint := fmt.Sprintf("/v1/mcp/server/%s", serverID)
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete MCP server: %s", err))
			return
		}
	}
}

func (r *MCPServerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_id"), req.ID)...)
}

func (r *MCPServerResource) buildMCPServerRequest(ctx context.Context, data *MCPServerResourceModel) map[string]interface{} {
	mcpReq := map[string]interface{}{
		"server_name":  data.ServerName.ValueString(),
		"url":          data.URL.ValueString(),
		"transport":    data.Transport.ValueString(),
		"spec_version": data.SpecVersion.ValueString(),
		"auth_type":    data.AuthType.ValueString(),
	}

	// String fields - check IsNull, IsUnknown, and empty string
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
		var extraHeaders map[string]string
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
				var toolCosts map[string]float64
				data.MCPInfo.MCPServerCostInfo.ToolNameToCostPerQuery.ElementsAs(ctx, &toolCosts, false)
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

	return mcpReq
}

func (r *MCPServerResource) readMCPServer(ctx context.Context, data *MCPServerResourceModel) error {
	serverID := data.ID.ValueString()
	if serverID == "" {
		serverID = data.ServerID.ValueString()
	}

	endpoint := fmt.Sprintf("/v1/mcp/server/%s", serverID)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
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
	if alias, ok := result["alias"].(string); ok {
		data.Alias = types.StringValue(alias)
	}
	if desc, ok := result["description"].(string); ok {
		data.Description = types.StringValue(desc)
	}
	if url, ok := result["url"].(string); ok {
		data.URL = types.StringValue(url)
	}
	if transport, ok := result["transport"].(string); ok {
		data.Transport = types.StringValue(transport)
	}
	if specVersion, ok := result["spec_version"].(string); ok {
		data.SpecVersion = types.StringValue(specVersion)
	}
	if authType, ok := result["auth_type"].(string); ok {
		data.AuthType = types.StringValue(authType)
	}
	if command, ok := result["command"].(string); ok {
		data.Command = types.StringValue(command)
	}
	if createdAt, ok := result["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(createdAt)
	}
	if createdBy, ok := result["created_by"].(string); ok {
		data.CreatedBy = types.StringValue(createdBy)
	}
	if updatedAt, ok := result["updated_at"].(string); ok {
		data.UpdatedAt = types.StringValue(updatedAt)
	}
	if updatedBy, ok := result["updated_by"].(string); ok {
		data.UpdatedBy = types.StringValue(updatedBy)
	}
	if status, ok := result["status"].(string); ok {
		data.Status = types.StringValue(status)
	}
	if lastHealthCheck, ok := result["last_health_check"].(string); ok {
		data.LastHealthCheck = types.StringValue(lastHealthCheck)
	}
	if healthCheckError, ok := result["health_check_error"].(string); ok {
		data.HealthCheckError = types.StringValue(healthCheckError)
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
	} else if !data.MCPAccessGroups.IsNull() {
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
	} else if !data.Args.IsNull() {
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
	} else if !data.Env.IsNull() {
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
	} else if !data.Credentials.IsNull() {
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
	} else if !data.AllowedTools.IsNull() {
		data.AllowedTools, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle extra_headers - preserve null when API returns empty and config didn't specify
	if extraHeaders, ok := result["extra_headers"].(map[string]interface{}); ok && len(extraHeaders) > 0 {
		headersMap := make(map[string]attr.Value)
		for k, v := range extraHeaders {
			if str, ok := v.(string); ok {
				headersMap[k] = types.StringValue(str)
			}
		}
		data.ExtraHeaders, _ = types.MapValue(types.StringType, headersMap)
	} else if !data.ExtraHeaders.IsNull() {
		data.ExtraHeaders, _ = types.MapValue(types.StringType, map[string]attr.Value{})
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
	} else if !data.StaticHeaders.IsNull() {
		data.StaticHeaders, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	if authURL, ok := result["authorization_url"].(string); ok {
		data.AuthorizationURL = types.StringValue(authURL)
	}

	if tokenURL, ok := result["token_url"].(string); ok {
		data.TokenURL = types.StringValue(tokenURL)
	}

	if regURL, ok := result["registration_url"].(string); ok {
		data.RegistrationURL = types.StringValue(regURL)
	}

	if allowAllKeys, ok := result["allow_all_keys"].(bool); ok {
		data.AllowAllKeys = types.BoolValue(allowAllKeys)
	}

	return nil
}
