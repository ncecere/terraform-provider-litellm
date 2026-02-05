package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &MCPServerDataSource{}

func NewMCPServerDataSource() datasource.DataSource {
	return &MCPServerDataSource{}
}

type MCPServerDataSource struct {
	client *Client
}

type MCPServerDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	ServerID         types.String `tfsdk:"server_id"`
	ServerName       types.String `tfsdk:"server_name"`
	Alias            types.String `tfsdk:"alias"`
	Description      types.String `tfsdk:"description"`
	URL              types.String `tfsdk:"url"`
	Transport        types.String `tfsdk:"transport"`
	SpecVersion      types.String `tfsdk:"spec_version"`
	AuthType         types.String `tfsdk:"auth_type"`
	MCPAccessGroups  types.List   `tfsdk:"mcp_access_groups"`
	Command          types.String `tfsdk:"command"`
	Args             types.List   `tfsdk:"args"`
	Env              types.Map    `tfsdk:"env"`
	AllowedTools     types.List   `tfsdk:"allowed_tools"`
	ExtraHeaders     types.Map    `tfsdk:"extra_headers"`
	StaticHeaders    types.Map    `tfsdk:"static_headers"`
	AuthorizationURL types.String `tfsdk:"authorization_url"`
	TokenURL         types.String `tfsdk:"token_url"`
	RegistrationURL  types.String `tfsdk:"registration_url"`
	AllowAllKeys     types.Bool   `tfsdk:"allow_all_keys"`
	CreatedAt        types.String `tfsdk:"created_at"`
	CreatedBy        types.String `tfsdk:"created_by"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
	UpdatedBy        types.String `tfsdk:"updated_by"`
	Status           types.String `tfsdk:"status"`
	LastHealthCheck  types.String `tfsdk:"last_health_check"`
	HealthCheckError types.String `tfsdk:"health_check_error"`
}

func (d *MCPServerDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_server"
}

func (d *MCPServerDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM MCP (Model Context Protocol) server.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this MCP server (same as server_id).",
				Computed:    true,
			},
			"server_id": schema.StringAttribute{
				Description: "Unique identifier for the MCP server.",
				Required:    true,
			},
			"server_name": schema.StringAttribute{
				Description: "Name of the MCP server.",
				Computed:    true,
			},
			"alias": schema.StringAttribute{
				Description: "Alias for the MCP server.",
				Computed:    true,
			},
			"description": schema.StringAttribute{
				Description: "Description of the MCP server.",
				Computed:    true,
			},
			"url": schema.StringAttribute{
				Description: "URL of the MCP server.",
				Computed:    true,
			},
			"transport": schema.StringAttribute{
				Description: "Transport type for the MCP server (http, sse, stdio).",
				Computed:    true,
			},
			"spec_version": schema.StringAttribute{
				Description: "MCP specification version.",
				Computed:    true,
			},
			"auth_type": schema.StringAttribute{
				Description: "Authentication type (none, bearer, basic).",
				Computed:    true,
			},
			"mcp_access_groups": schema.ListAttribute{
				Description: "List of access groups for the MCP server.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"command": schema.StringAttribute{
				Description: "Command to run for stdio transport.",
				Computed:    true,
			},
			"args": schema.ListAttribute{
				Description: "Arguments for the command (stdio transport).",
				Computed:    true,
				ElementType: types.StringType,
			},
			"env": schema.MapAttribute{
				Description: "Environment variables for the command (stdio transport).",
				Computed:    true,
				ElementType: types.StringType,
			},
			"allowed_tools": schema.ListAttribute{
				Description: "List of allowed tool names for this MCP server.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"extra_headers": schema.MapAttribute{
				Description: "Extra headers to send with requests to the MCP server.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"static_headers": schema.MapAttribute{
				Description: "Static headers to always include with requests.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"authorization_url": schema.StringAttribute{
				Description: "OAuth authorization URL for the MCP server.",
				Computed:    true,
			},
			"token_url": schema.StringAttribute{
				Description: "OAuth token URL for the MCP server.",
				Computed:    true,
			},
			"registration_url": schema.StringAttribute{
				Description: "OAuth registration URL for the MCP server.",
				Computed:    true,
			},
			"allow_all_keys": schema.BoolAttribute{
				Description: "Whether all API keys are allowed to access this MCP server.",
				Computed:    true,
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
	}
}

func (d *MCPServerDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData),
		)
		return
	}

	d.client = client
}

func (d *MCPServerDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data MCPServerDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serverID := data.ServerID.ValueString()
	endpoint := fmt.Sprintf("/v1/mcp/server/%s", serverID)

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read MCP server '%s': %s", serverID, err))
		return
	}

	// Update fields from response
	if sid, ok := result["server_id"].(string); ok {
		data.ServerID = types.StringValue(sid)
		data.ID = types.StringValue(sid)
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

	// Handle access groups
	if accessGroups, ok := result["mcp_access_groups"].([]interface{}); ok {
		groups := make([]attr.Value, len(accessGroups))
		for i, g := range accessGroups {
			if str, ok := g.(string); ok {
				groups[i] = types.StringValue(str)
			}
		}
		data.MCPAccessGroups, _ = types.ListValue(types.StringType, groups)
	} else {
		data.MCPAccessGroups, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle args
	if args, ok := result["args"].([]interface{}); ok {
		argsList := make([]attr.Value, len(args))
		for i, a := range args {
			if str, ok := a.(string); ok {
				argsList[i] = types.StringValue(str)
			}
		}
		data.Args, _ = types.ListValue(types.StringType, argsList)
	} else {
		data.Args, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle env
	if env, ok := result["env"].(map[string]interface{}); ok {
		envMap := make(map[string]attr.Value)
		for k, v := range env {
			if str, ok := v.(string); ok {
				envMap[k] = types.StringValue(str)
			}
		}
		data.Env, _ = types.MapValue(types.StringType, envMap)
	} else {
		data.Env, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle allowed_tools
	if allowedTools, ok := result["allowed_tools"].([]interface{}); ok {
		tools := make([]attr.Value, len(allowedTools))
		for i, t := range allowedTools {
			if str, ok := t.(string); ok {
				tools[i] = types.StringValue(str)
			}
		}
		data.AllowedTools, _ = types.ListValue(types.StringType, tools)
	} else {
		data.AllowedTools, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle extra_headers
	if extraHeaders, ok := result["extra_headers"].(map[string]interface{}); ok {
		headersMap := make(map[string]attr.Value)
		for k, v := range extraHeaders {
			if str, ok := v.(string); ok {
				headersMap[k] = types.StringValue(str)
			}
		}
		data.ExtraHeaders, _ = types.MapValue(types.StringType, headersMap)
	} else {
		data.ExtraHeaders, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle static_headers
	if staticHeaders, ok := result["static_headers"].(map[string]interface{}); ok {
		headersMap := make(map[string]attr.Value)
		for k, v := range staticHeaders {
			if str, ok := v.(string); ok {
				headersMap[k] = types.StringValue(str)
			}
		}
		data.StaticHeaders, _ = types.MapValue(types.StringType, headersMap)
	} else {
		data.StaticHeaders, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle OAuth URLs
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

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
