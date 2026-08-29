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
	seen := make(map[string]struct{}, len(result))
	for _, raw := range result {
		item := UnifiedAccessGroupDataSourceModel{}
		item.AccessGroupID, err = dataSourceRequiredStringAt(raw, "access_group_id")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", "/v1/access_group returned an access group object without a canonical access_group_id")
			return
		}
		item.ID = item.AccessGroupID
		if err := dataSourceListIdentity(seen, item.AccessGroupID.ValueString(), "/v1/access_group", "access_group_id"); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		for _, field := range []struct {
			name   string
			target *types.String
		}{
			{"access_group_name", &item.AccessGroupName}, {"description", &item.Description},
			{"created_at", &item.CreatedAt}, {"created_by", &item.CreatedBy},
			{"updated_at", &item.UpdatedAt}, {"updated_by", &item.UpdatedBy},
		} {
			*field.target, err = dataSourceNullableStringAt(raw, field.name)
			if err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		for _, field := range []struct {
			name   string
			target *types.List
		}{
			{"access_model_names", &item.AccessModelNames}, {"access_mcp_server_ids", &item.AccessMCPServerIDs},
			{"access_agent_ids", &item.AccessAgentIDs}, {"assigned_team_ids", &item.AssignedTeamIDs},
			{"assigned_key_ids", &item.AssignedKeyIDs},
		} {
			*field.target, err = dataSourceNullableStringListAt(raw, field.name)
			if err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		if !item.AssignedKeyIDs.IsNull() {
			for _, value := range item.AssignedKeyIDs.Elements() {
				if _, keyErr := unifiedAccessGroupKeyHash(value.(types.String).ValueString()); keyErr != nil {
					resp.Diagnostics.AddError("Invalid API Response", "/v1/access_group returned an invalid assigned_key_ids element")
					return
				}
			}
		}
		data.AccessGroups = append(data.AccessGroups, item)
	}
	sort.SliceStable(data.AccessGroups, func(i, j int) bool {
		return data.AccessGroups[i].AccessGroupID.ValueString() < data.AccessGroups[j].AccessGroupID.ValueString()
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Keep types imported for generated nested schema element types in this file's package usage.
var _ = types.StringType
