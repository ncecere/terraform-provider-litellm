package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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
				Description: "List of access groups.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"access_group": schema.StringAttribute{
							Description: "The access group name.",
							Computed:    true,
						},
						"model_names": schema.ListAttribute{
							Description: "List of model names in this access group.",
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

	var rawResult interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", "/access_group/list", nil, &rawResult); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list access groups: %s", err))
		return
	}

	var rawGroups []interface{}
	if result, ok := rawResult.(map[string]interface{}); ok {
		if groups, ok := result["access_groups"].([]interface{}); ok {
			rawGroups = groups
		} else {
			// Older shape: {"group-name": ["model-a", "model-b"]}
			for accessGroup, models := range result {
				rawGroups = append(rawGroups, map[string]interface{}{
					"access_group": accessGroup,
					"model_names":  models,
				})
			}
		}
	}

	accessGroups := make([]AccessGroupListItemModel, 0, len(rawGroups))
	for _, rawGroup := range rawGroups {
		groupMap, ok := rawGroup.(map[string]interface{})
		if !ok {
			continue
		}
		item := AccessGroupListItemModel{}
		if accessGroup, ok := groupMap["access_group"].(string); ok {
			item.AccessGroup = types.StringValue(accessGroup)
		}

		var modelsList []attr.Value
		if modelNames, ok := groupMap["model_names"].([]interface{}); ok {
			modelsList = make([]attr.Value, 0, len(modelNames))
			for _, m := range modelNames {
				if str, ok := m.(string); ok {
					modelsList = append(modelsList, types.StringValue(str))
				}
			}
		}
		item.ModelNames, _ = types.ListValue(types.StringType, modelsList)

		accessGroups = append(accessGroups, item)
	}

	data.ID = types.StringValue("access_groups")
	data.AccessGroups = accessGroups

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
