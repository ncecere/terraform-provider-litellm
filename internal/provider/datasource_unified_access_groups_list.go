package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &UnifiedAccessGroupsListDataSource{}

func NewUnifiedAccessGroupsListDataSource() datasource.DataSource {
	return &UnifiedAccessGroupsListDataSource{}
}

type UnifiedAccessGroupsListDataSource struct {
	client *Client
}

type UnifiedAccessGroupsListDataSourceModel struct {
	AccessGroups []UnifiedAccessGroupDataSourceModel `tfsdk:"access_groups"`
}

func (d *UnifiedAccessGroupsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_unified_access_groups"
}

func (d *UnifiedAccessGroupsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Lists LiteLLM unified access groups. These are the Access Groups shown in the LiteLLM UI and docs.",
		Attributes: map[string]schema.Attribute{
			"access_groups": schema.ListNestedAttribute{
				Description: "List of unified access groups.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: unifiedAccessGroupDataSourceAttributes(false),
				},
			},
		},
	}
}

func (d *UnifiedAccessGroupsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *UnifiedAccessGroupsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	result, err := fetchTopLevelListObjects(ctx, d.client, "/v1/access_group", "unified access group item")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list unified access groups: %s", err))
		return
	}

	data := UnifiedAccessGroupsListDataSourceModel{
		AccessGroups: make([]UnifiedAccessGroupDataSourceModel, 0, len(result)),
	}
	for _, item := range result {
		resourceData := UnifiedAccessGroupResourceModel{}
		readUnifiedAccessGroupResponse(ctx, item, &resourceData)
		if resourceData.AccessGroupID.ValueString() == "" {
			resp.Diagnostics.AddError("Invalid API Response", "/v1/access_group returned an access group object without access_group_id")
			return
		}
		assignedKeyIDs := types.ListNull(types.StringType)
		setSafeAssignedKeyListFromResponse(&assignedKeyIDs, item["assigned_key_ids"])
		data.AccessGroups = append(data.AccessGroups, UnifiedAccessGroupDataSourceModel{
			ID:                 resourceData.ID,
			AccessGroupID:      resourceData.AccessGroupID,
			AccessGroupName:    resourceData.AccessGroupName,
			Description:        resourceData.Description,
			AccessModelNames:   resourceData.AccessModelNames,
			AccessMCPServerIDs: resourceData.AccessMCPServerIDs,
			AccessAgentIDs:     resourceData.AccessAgentIDs,
			AssignedTeamIDs:    resourceData.AssignedTeamIDs,
			AssignedKeyIDs:     assignedKeyIDs,
			CreatedAt:          resourceData.CreatedAt,
			CreatedBy:          resourceData.CreatedBy,
			UpdatedAt:          resourceData.UpdatedAt,
			UpdatedBy:          resourceData.UpdatedBy,
		})
	}
	sort.SliceStable(data.AccessGroups, func(i, j int) bool {
		return data.AccessGroups[i].AccessGroupID.ValueString() < data.AccessGroups[j].AccessGroupID.ValueString()
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Keep types imported for generated nested schema element types in this file's package usage.
var _ = types.StringType
