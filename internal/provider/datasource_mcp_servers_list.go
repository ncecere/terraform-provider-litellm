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
	ServerID     types.String `tfsdk:"server_id"`
	ServerName   types.String `tfsdk:"server_name"`
	Alias        types.String `tfsdk:"alias"`
	Description  types.String `tfsdk:"description"`
	URL          types.String `tfsdk:"url"`
	SpecPath     types.String `tfsdk:"spec_path"`
	Transport    types.String `tfsdk:"transport"`
	SpecVersion  types.String `tfsdk:"spec_version"`
	AuthType     types.String `tfsdk:"auth_type"`
	Status       types.String `tfsdk:"status"`
	AllowAllKeys types.Bool   `tfsdk:"allow_all_keys"`
	CreatedAt    types.String `tfsdk:"created_at"`
	UpdatedAt    types.String `tfsdk:"updated_at"`
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
				Description: "Placeholder identifier.",
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
						"status": schema.StringAttribute{
							Description: "Current status of the MCP server.",
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
	var data MCPServersListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	const endpoint = "/v1/mcp/server"
	result, err := fetchTopLevelListObjects(ctx, d.client, endpoint, "MCP server item")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list MCP servers: %s", err))
		return
	}

	data.ID = types.StringValue("mcp_servers")
	data.MCPServers = make([]MCPServerListItem, 0, len(result))
	for _, serverMap := range result {
		serverID, ok := serverMap["server_id"].(string)
		if !ok || serverID == "" || validateMCPServerResponse(serverMap, serverID) != nil {
			resp.Diagnostics.AddError("Invalid API Response", "LiteLLM returned a malformed MCP server object.")
			return
		}
		if err := validateMCPServerOptionalResponseFields(
			serverMap,
			[]string{"server_name", "alias", "description", "url", "spec_path", "auth_type", "status", "created_at", "updated_at"},
			[]string{"allow_all_keys"},
			nil,
			nil,
		); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", "LiteLLM returned malformed optional MCP server fields.")
			return
		}

		item := MCPServerListItem{ServerID: types.StringValue(serverID)}
		if serverName, ok := serverMap["server_name"].(string); ok {
			item.ServerName = types.StringValue(serverName)
		}
		if alias, ok := serverMap["alias"].(string); ok {
			item.Alias = types.StringValue(alias)
		}
		if desc, ok := serverMap["description"].(string); ok {
			item.Description = types.StringValue(desc)
		}
		if remoteURL, ok := serverMap["url"].(string); ok {
			item.URL = types.StringValue(remoteURL)
		}
		if specPath, ok := serverMap["spec_path"].(string); ok {
			item.SpecPath = types.StringValue(specPath)
		}
		if transport, ok := serverMap["transport"].(string); ok {
			item.Transport = types.StringValue(transport)
		}
		if authType, ok := serverMap["auth_type"].(string); ok {
			item.AuthType = types.StringValue(authType)
		}
		if status, ok := serverMap["status"].(string); ok {
			item.Status = types.StringValue(status)
		}
		if allowAllKeys, ok := serverMap["allow_all_keys"].(bool); ok {
			item.AllowAllKeys = types.BoolValue(allowAllKeys)
		}
		if createdAt, ok := serverMap["created_at"].(string); ok {
			item.CreatedAt = types.StringValue(createdAt)
		}
		if updatedAt, ok := serverMap["updated_at"].(string); ok {
			item.UpdatedAt = types.StringValue(updatedAt)
		}

		data.MCPServers = append(data.MCPServers, item)
	}
	sort.SliceStable(data.MCPServers, func(i, j int) bool {
		return data.MCPServers[i].ServerID.ValueString() < data.MCPServers[j].ServerID.ValueString()
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
