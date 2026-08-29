package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &MCPServersListDataSource{}

func NewMCPServersListDataSource() datasource.DataSource {
	return &MCPServersListDataSource{}
}

type MCPServersListDataSource struct {
	client *Client
}

type MCPServerListItem struct {
	ServerID                  types.String  `tfsdk:"server_id"`
	ServerName                types.String  `tfsdk:"server_name"`
	Alias                     types.String  `tfsdk:"alias"`
	Description               types.String  `tfsdk:"description"`
	URL                       types.String  `tfsdk:"url"`
	SpecPath                  types.String  `tfsdk:"spec_path"`
	Transport                 types.String  `tfsdk:"transport"`
	SpecVersion               types.String  `tfsdk:"spec_version"`
	AuthType                  types.String  `tfsdk:"auth_type"`
	MCPAccessGroups           types.List    `tfsdk:"mcp_access_groups"`
	MCPInfoJSON               types.String  `tfsdk:"mcp_info_json"`
	Command                   types.String  `tfsdk:"command"`
	Args                      types.List    `tfsdk:"args"`
	Env                       types.Map     `tfsdk:"env"`
	AllowedTools              types.List    `tfsdk:"allowed_tools"`
	ExtraHeaders              types.List    `tfsdk:"extra_headers"`
	StaticHeaders             types.Map     `tfsdk:"static_headers"`
	AuthorizationURL          types.String  `tfsdk:"authorization_url"`
	TokenURL                  types.String  `tfsdk:"token_url"`
	RegistrationURL           types.String  `tfsdk:"registration_url"`
	Status                    types.String  `tfsdk:"status"`
	AllowAllKeys              types.Bool    `tfsdk:"allow_all_keys"`
	AvailableOnPublicInternet types.Bool    `tfsdk:"available_on_public_internet"`
	OAuth2Flow                types.String  `tfsdk:"oauth2_flow"`
	Instructions              types.String  `tfsdk:"instructions"`
	ToolNameToDisplayName     types.Map     `tfsdk:"tool_name_to_display_name"`
	ToolNameToDescription     types.Map     `tfsdk:"tool_name_to_description"`
	DelegateAuthToUpstream    types.Bool    `tfsdk:"delegate_auth_to_upstream"`
	OAuthPassthrough          types.Bool    `tfsdk:"oauth_passthrough"`
	DCRBridge                 types.Bool    `tfsdk:"dcr_bridge"`
	IsBYOK                    types.Bool    `tfsdk:"is_byok"`
	BYOKDescription           types.List    `tfsdk:"byok_description"`
	BYOKAPIKeyHelpURL         types.String  `tfsdk:"byok_api_key_help_url"`
	SourceURL                 types.String  `tfsdk:"source_url"`
	Timeout                   types.Float64 `tfsdk:"timeout"`
	MaxConcurrentRequests     types.Int64   `tfsdk:"max_concurrent_requests"`
	CreatedAt                 types.String  `tfsdk:"created_at"`
	UpdatedAt                 types.String  `tfsdk:"updated_at"`
}

type MCPServersListDataSourceModel struct {
	ID         types.String        `tfsdk:"id"`
	MCPServers []MCPServerListItem `tfsdk:"mcp_servers"`
}

func (d *MCPServersListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_servers"
}

func (d *MCPServersListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves a list of LiteLLM MCP (Model Context Protocol) servers.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Stable inventory identifier (mcp_servers).",
				Computed:    true,
			},
			"mcp_servers": schema.ListNestedAttribute{
				Description: "List of MCP servers.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"server_id": schema.StringAttribute{
							Description: "The unique identifier for this MCP server.",
							Computed:    true,
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
							Sensitive:   true,
						},
						"spec_path": schema.StringAttribute{
							Description: "Path or URL of the server's OpenAPI specification, when configured.",
							Computed:    true,
							Sensitive:   true,
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
						"mcp_access_groups": schema.ListAttribute{
							Description: "List of access groups for the MCP server.",
							Computed:    true,
							ElementType: types.StringType,
						},
						"mcp_info_json": schema.StringAttribute{
							Description: "Sensitive canonical complete MCP info JSON object, or null when LiteLLM masks or omits it.",
							Computed:    true,
							Sensitive:   true,
						},
						"command": schema.StringAttribute{
							Description: "Command to run for stdio transport.",
							Computed:    true,
							Sensitive:   true,
						},
						"args": schema.ListAttribute{
							Description: "Arguments for the command (stdio transport).",
							Computed:    true,
							Sensitive:   true,
							ElementType: types.StringType,
						},
						"env": schema.MapAttribute{
							Description: "Environment variables for the command (stdio transport).",
							Computed:    true,
							Sensitive:   true,
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
							Sensitive:   true,
							ElementType: types.StringType,
						},
						"authorization_url": schema.StringAttribute{
							Description: "OAuth authorization URL for the MCP server.",
							Computed:    true,
							Sensitive:   true,
						},
						"token_url": schema.StringAttribute{
							Description: "OAuth token URL for the MCP server.",
							Computed:    true,
							Sensitive:   true,
						},
						"registration_url": schema.StringAttribute{
							Description: "OAuth registration URL for the MCP server.",
							Computed:    true,
							Sensitive:   true,
						},
						"status": schema.StringAttribute{
							Description: "Current status of the MCP server.",
							Computed:    true,
						},
						"allow_all_keys": schema.BoolAttribute{
							Description: "Whether all API keys are allowed to access this MCP server.",
							Computed:    true,
						},
						"available_on_public_internet": schema.BoolAttribute{
							Description: "Whether the MCP server is available from the public internet.",
							Computed:    true,
						},
						"oauth2_flow": schema.StringAttribute{
							Description: "OAuth2 flow persisted by LiteLLM.",
							Computed:    true,
						},
						"instructions": schema.StringAttribute{
							Description: "Instructions associated with the MCP server.",
							Computed:    true,
						},
						"tool_name_to_display_name": schema.MapAttribute{
							Description: "Tool-name display overrides.",
							Computed:    true,
							ElementType: types.StringType,
						},
						"tool_name_to_description": schema.MapAttribute{
							Description: "Tool-name description overrides.",
							Computed:    true,
							ElementType: types.StringType,
						},
						"delegate_auth_to_upstream": schema.BoolAttribute{
							Description: "Whether authentication is delegated upstream.",
							Computed:    true,
						},
						"oauth_passthrough": schema.BoolAttribute{
							Description: "Whether OAuth Authorization headers are passed through.",
							Computed:    true,
						},
						"dcr_bridge": schema.BoolAttribute{
							Description: "Whether the dynamic client registration bridge is enabled.",
							Computed:    true,
						},
						"is_byok": schema.BoolAttribute{
							Description: "Whether bring-your-own-key configuration is enabled.",
							Computed:    true,
						},
						"byok_description": schema.ListAttribute{
							Description: "Bring-your-own-key setup description lines.",
							Computed:    true,
							ElementType: types.StringType,
						},
						"byok_api_key_help_url": schema.StringAttribute{
							Description: "Bring-your-own-key API key help URL.",
							Computed:    true,
						},
						"source_url": schema.StringAttribute{
							Description: "Source URL associated with the MCP server.",
							Computed:    true,
						},
						"timeout": schema.Float64Attribute{
							Description: "Positive finite request timeout.",
							Computed:    true,
						},
						"max_concurrent_requests": schema.Int64Attribute{
							Description: "Positive maximum number of concurrent requests.",
							Computed:    true,
						},
						"created_at": schema.StringAttribute{
							Description: "Timestamp when the server was created.",
							Computed:    true,
						},
						"updated_at": schema.StringAttribute{
							Description: "Timestamp when the server was last updated.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *MCPServersListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *MCPServersListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	const endpoint = "/v1/mcp/server"
	result, err := fetchTopLevelListObjects(ctx, d.client, endpoint, "MCP server item")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list MCP servers: %s", err))
		return
	}

	data := MCPServersListDataSourceModel{
		ID:         types.StringValue("mcp_servers"),
		MCPServers: make([]MCPServerListItem, 0, len(result)),
	}
	seen := make(map[string]struct{}, len(result))
	for _, serverMap := range result {
		serverID, identityErr := dataSourceRequiredStringAt(serverMap, "server_id")
		if identityErr != nil || dataSourceListIdentity(seen, serverID.ValueString(), endpoint, "server_id") != nil {
			resp.Diagnostics.AddError("Invalid API Response", "LiteLLM returned a malformed MCP server list.")
			return
		}
		server, projectionErr := projectMCPServerManagerListDataSource(serverMap, serverID.ValueString())
		if projectionErr != nil {
			resp.Diagnostics.AddError("Invalid API Response", "LiteLLM returned a malformed MCP server list.")
			return
		}
		data.MCPServers = append(data.MCPServers, MCPServerListItem{
			ServerID:                  server.ServerID,
			ServerName:                server.ServerName,
			Alias:                     server.Alias,
			Description:               server.Description,
			URL:                       server.URL,
			SpecPath:                  server.SpecPath,
			Transport:                 server.Transport,
			SpecVersion:               server.SpecVersion,
			AuthType:                  server.AuthType,
			MCPAccessGroups:           server.MCPAccessGroups,
			MCPInfoJSON:               server.MCPInfoJSON,
			Command:                   server.Command,
			Args:                      server.Args,
			Env:                       server.Env,
			AllowedTools:              server.AllowedTools,
			ExtraHeaders:              server.ExtraHeaders,
			StaticHeaders:             server.StaticHeaders,
			AuthorizationURL:          server.AuthorizationURL,
			TokenURL:                  server.TokenURL,
			RegistrationURL:           server.RegistrationURL,
			Status:                    server.Status,
			AllowAllKeys:              server.AllowAllKeys,
			AvailableOnPublicInternet: server.AvailableOnPublicInternet,
			OAuth2Flow:                server.OAuth2Flow,
			Instructions:              server.Instructions,
			ToolNameToDisplayName:     server.ToolNameToDisplayName,
			ToolNameToDescription:     server.ToolNameToDescription,
			DelegateAuthToUpstream:    server.DelegateAuthToUpstream,
			OAuthPassthrough:          server.OAuthPassthrough,
			DCRBridge:                 server.DCRBridge,
			IsBYOK:                    server.IsBYOK,
			BYOKDescription:           server.BYOKDescription,
			BYOKAPIKeyHelpURL:         server.BYOKAPIKeyHelpURL,
			SourceURL:                 server.SourceURL,
			Timeout:                   server.Timeout,
			MaxConcurrentRequests:     server.MaxConcurrentRequests,
			CreatedAt:                 server.CreatedAt,
			UpdatedAt:                 server.UpdatedAt,
		})
	}
	sort.SliceStable(data.MCPServers, func(i, j int) bool {
		return data.MCPServers[i].ServerID.ValueString() < data.MCPServers[j].ServerID.ValueString()
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
