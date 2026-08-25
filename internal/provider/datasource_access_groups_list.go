package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &AccessGroupsListDataSource{}

func NewAccessGroupsListDataSource() datasource.DataSource {
	return &AccessGroupsListDataSource{}
}

type AccessGroupsListDataSource struct {
	client *Client
}

type AccessGroupsListDataSourceModel struct {
	ID           types.String               `tfsdk:"id"`
	AccessGroups []AccessGroupListItemModel `tfsdk:"access_groups"`
}

type AccessGroupListItemModel struct {
	AccessGroup types.String `tfsdk:"access_group"`
	ModelNames  types.List   `tfsdk:"model_names"`
}

func (d *AccessGroupsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_access_groups"
}

func (d *AccessGroupsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches a list of all LiteLLM access groups.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier for this data source.",
				Computed:    true,
			},
			"access_groups": schema.ListNestedAttribute{
				Description: "List of access groups sorted by name.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"access_group": schema.StringAttribute{
							Description: "The access group name.",
							Computed:    true,
						},
						"model_names": schema.ListAttribute{
							Description: "Sorted, deduplicated list of model names in this access group.",
							Computed:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
		},
	}
}

func (d *AccessGroupsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *AccessGroupsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data AccessGroupsListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groups, err := fetchEnvelopeListObjects(ctx, d.client, "/access_group/list", "access_groups", "access group item")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list access groups: %s", err))
		return
	}

	accessGroups := make([]AccessGroupListItemModel, 0, len(groups))
	for _, groupMap := range groups {
		item := AccessGroupListItemModel{}
		if accessGroup, ok := groupMap["access_group"].(string); ok && accessGroup != "" {
			item.AccessGroup = types.StringValue(accessGroup)
		} else {
			resp.Diagnostics.AddError("Invalid API Response", "/access_group/list returned an access group object without access_group")
			return
		}

		modelNames, err := reconcileAccessGroupModelNames(ctx, types.ListNull(types.StringType), groupMap["model_names"])
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", fmt.Sprintf("Unable to decode model_names for access group %q: %s", item.AccessGroup.ValueString(), err))
			return
		}
		item.ModelNames = modelNames

		accessGroups = append(accessGroups, item)
	}
	sort.Slice(accessGroups, func(i, j int) bool {
		return accessGroups[i].AccessGroup.ValueString() < accessGroups[j].AccessGroup.ValueString()
	})

	data.ID = types.StringValue("access_groups")
	data.AccessGroups = accessGroups

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
