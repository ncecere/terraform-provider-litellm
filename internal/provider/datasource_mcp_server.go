package provider

import (
	"context"
	"fmt"

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
	SpecPath         types.String `tfsdk:"spec_path"`
	Transport        types.String `tfsdk:"transport"`
	SpecVersion      types.String `tfsdk:"spec_version"`
	AuthType         types.String `tfsdk:"auth_type"`
	MCPAccessGroups  types.List   `tfsdk:"mcp_access_groups"`
	MCPInfoJSON      types.String `tfsdk:"mcp_info_json"`
	Command          types.String `tfsdk:"command"`
	Args             types.List   `tfsdk:"args"`
	Env              types.Map    `tfsdk:"env"`
	AllowedTools     types.List   `tfsdk:"allowed_tools"`
	ExtraHeaders     types.List   `tfsdk:"extra_headers"`
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
				Description: "URL of the MCP server, when configured.",
				Computed:    true,
			},
			"spec_path": schema.StringAttribute{
				Description: "Path or URL of the server's OpenAPI specification, when configured.",
				Computed:    true,
			},
			"transport": schema.StringAttribute{
				Description: "Transport type for the MCP server (http, sse, stdio).",
				Computed:    true,
			},
			"spec_version": schema.StringAttribute{
				Description:        "Deprecated compatibility field. LiteLLM v1.98 does not return an MCP specification version.",
				DeprecationMessage: "spec_version is retained only for state compatibility and is not returned by LiteLLM v1.98.",
				Computed:           true,
			},
			"auth_type": schema.StringAttribute{
				Description: "Authentication type reported by LiteLLM.",
				Computed:    true,
			},
			"mcp_info_json": schema.StringAttribute{
				Description: "Sensitive canonical complete MCP info JSON object, or null when LiteLLM masks or omits it.",
				Computed:    true,
				Sensitive:   true,
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
			"extra_headers": schema.ListAttribute{
				Description: "Extra header names forwarded to the MCP server.",
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
	var config MCPServerDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.ServerID.IsNull() || config.ServerID.IsUnknown() || config.ServerID.ValueString() == "" {
		resp.Diagnostics.AddError("Invalid Data Source Configuration", "The MCP server lookup requires a known nonempty server_id.")
		return
	}

	serverID := config.ServerID.ValueString()
	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", mcpServerEndpoint(serverID), nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read MCP server: %s", err))
		return
	}

	data, err := projectMCPServerDataSource(result, serverID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", "LiteLLM returned a malformed MCP server response.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func projectMCPServerDataSource(result map[string]interface{}, expectedServerID string) (MCPServerDataSourceModel, error) {
	var data MCPServerDataSourceModel
	serverID, err := dataSourceRequiredStringAt(result, "server_id")
	if err != nil || serverID.ValueString() != expectedServerID {
		return data, fmt.Errorf("MCP server response identity mismatch")
	}
	transport, err := dataSourceRequiredStringAt(result, "transport")
	if err != nil || !mcpDataSourceTransportValid(transport.ValueString()) {
		return data, fmt.Errorf("MCP server response transport is invalid")
	}

	data.ID = serverID
	data.ServerID = serverID
	data.Transport = transport
	data.SpecVersion = types.StringNull()
	if data.ServerName, err = dataSourceNullableStringAt(result, "server_name"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.Alias, err = dataSourceNullableStringAt(result, "alias"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.Description, err = dataSourceNullableStringAt(result, "description"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.URL, err = dataSourceNullableStringAt(result, "url"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.SpecPath, err = dataSourceNullableStringAt(result, "spec_path"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.AuthType, err = dataSourceNullableStringAt(result, "auth_type"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.MCPAccessGroups, err = dataSourceNullableStringListAt(result, "mcp_access_groups"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.MCPInfoJSON, err = mcpInfoDataSourceValue(result); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.Command, err = dataSourceNullableStringAt(result, "command"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.Args, err = dataSourceNullableStringListAt(result, "args"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.Env, err = dataSourceNullableStringMapAt(result, "env"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.AllowedTools, err = dataSourceNullableStringListAt(result, "allowed_tools"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.ExtraHeaders, err = dataSourceNullableStringListAt(result, "extra_headers"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.StaticHeaders, err = dataSourceNullableStringMapAt(result, "static_headers"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.AuthorizationURL, err = dataSourceNullableStringAt(result, "authorization_url"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.TokenURL, err = dataSourceNullableStringAt(result, "token_url"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.RegistrationURL, err = dataSourceNullableStringAt(result, "registration_url"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.AllowAllKeys, err = dataSourceNullableBoolAt(result, "allow_all_keys"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.CreatedAt, err = dataSourceNullableStringAt(result, "created_at"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.CreatedBy, err = dataSourceNullableStringAt(result, "created_by"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.UpdatedAt, err = dataSourceNullableStringAt(result, "updated_at"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.UpdatedBy, err = dataSourceNullableStringAt(result, "updated_by"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.Status, err = dataSourceNullableStringAt(result, "status"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.LastHealthCheck, err = dataSourceNullableStringAt(result, "last_health_check"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	if data.HealthCheckError, err = dataSourceNullableStringAt(result, "health_check_error"); err != nil {
		return MCPServerDataSourceModel{}, err
	}
	return data, nil
}

func mcpInfoDataSourceValue(result map[string]interface{}) (types.String, error) {
	document, presence, err := mcpInfoDocumentFromResponse(result)
	if err != nil || presence != apiValuePresent {
		return types.StringNull(), err
	}
	canonical, err := canonicalMCPInfoJSONObject(document)
	if err != nil {
		return types.StringNull(), err
	}
	return types.StringValue(canonical), nil
}

func mcpDataSourceTransportValid(transport string) bool {
	for _, allowed := range mcpTransportsV198 {
		if transport == allowed {
			return true
		}
	}
	return false
}
