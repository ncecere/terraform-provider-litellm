package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &UnifiedAccessGroupDataSource{}

func NewUnifiedAccessGroupDataSource() datasource.DataSource {
	return &UnifiedAccessGroupDataSource{}
}

type UnifiedAccessGroupDataSource struct {
	client *Client
}

type UnifiedAccessGroupDataSourceModel struct {
	ID                 types.String `tfsdk:"id"`
	AccessGroupID      types.String `tfsdk:"access_group_id"`
	AccessGroupName    types.String `tfsdk:"access_group_name"`
	Description        types.String `tfsdk:"description"`
	AccessModelNames   types.List   `tfsdk:"access_model_names"`
	AccessMCPServerIDs types.List   `tfsdk:"access_mcp_server_ids"`
	AccessAgentIDs     types.List   `tfsdk:"access_agent_ids"`
	AssignedTeamIDs    types.List   `tfsdk:"assigned_team_ids"`
	AssignedKeyIDs     types.List   `tfsdk:"assigned_key_ids"`
	CreatedAt          types.String `tfsdk:"created_at"`
	CreatedBy          types.String `tfsdk:"created_by"`
	UpdatedAt          types.String `tfsdk:"updated_at"`
	UpdatedBy          types.String `tfsdk:"updated_by"`
}

func (d *UnifiedAccessGroupDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_unified_access_group"
}

func (d *UnifiedAccessGroupDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM unified access group.",
		Attributes:  unifiedAccessGroupDataSourceAttributes(true),
	}
}

func unifiedAccessGroupDataSourceAttributes(requireID bool) map[string]schema.Attribute {
	accessGroupID := schema.StringAttribute{Description: "The unique identifier for this unified access group.", Computed: true}
	if requireID {
		accessGroupID = schema.StringAttribute{Description: "The unique identifier for this unified access group.", Required: true}
	}
	return map[string]schema.Attribute{
		"id":                    schema.StringAttribute{Description: "The unique identifier for this unified access group.", Computed: true},
		"access_group_id":       accessGroupID,
		"access_group_name":     schema.StringAttribute{Description: "The display/name of the unified access group.", Computed: true},
		"description":           schema.StringAttribute{Description: "Description of the unified access group.", Computed: true},
		"access_model_names":    schema.ListAttribute{Description: "Model names this access group grants access to.", Computed: true, ElementType: types.StringType},
		"access_mcp_server_ids": schema.ListAttribute{Description: "MCP server IDs this access group grants access to.", Computed: true, ElementType: types.StringType},
		"access_agent_ids":      schema.ListAttribute{Description: "Agent IDs this access group grants access to.", Computed: true, ElementType: types.StringType},
		"assigned_team_ids":     schema.ListAttribute{Description: "Team IDs assigned to this access group.", Computed: true, ElementType: types.StringType},
		"assigned_key_ids":      schema.ListAttribute{Description: "Key IDs assigned to this access group.", Computed: true, ElementType: types.StringType},
		"created_at":            schema.StringAttribute{Description: "Timestamp when the access group was created.", Computed: true},
		"created_by":            schema.StringAttribute{Description: "User who created the access group.", Computed: true},
		"updated_at":            schema.StringAttribute{Description: "Timestamp when the access group was last updated.", Computed: true},
		"updated_by":            schema.StringAttribute{Description: "User who last updated the access group.", Computed: true},
	}
}

func (d *UnifiedAccessGroupDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	d.client = client
}

func (d *UnifiedAccessGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data UnifiedAccessGroupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/v1/access_group/%s", data.AccessGroupID.ValueString())
	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read unified access group: %s", err))
		return
	}

	resourceData := UnifiedAccessGroupResourceModel{AccessGroupID: data.AccessGroupID}
	readUnifiedAccessGroupResponse(ctx, result, &resourceData)
	data.ID = resourceData.ID
	data.AccessGroupID = resourceData.AccessGroupID
	data.AccessGroupName = resourceData.AccessGroupName
	data.Description = resourceData.Description
	data.AccessModelNames = resourceData.AccessModelNames
	data.AccessMCPServerIDs = resourceData.AccessMCPServerIDs
	data.AccessAgentIDs = resourceData.AccessAgentIDs
	data.AssignedTeamIDs = resourceData.AssignedTeamIDs
	data.AssignedKeyIDs = resourceData.AssignedKeyIDs
	data.CreatedAt = resourceData.CreatedAt
	data.CreatedBy = resourceData.CreatedBy
	data.UpdatedAt = resourceData.UpdatedAt
	data.UpdatedBy = resourceData.UpdatedBy

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
