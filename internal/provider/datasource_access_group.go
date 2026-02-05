package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &AccessGroupDataSource{}

func NewAccessGroupDataSource() datasource.DataSource {
	return &AccessGroupDataSource{}
}

type AccessGroupDataSource struct {
	client *Client
}

type AccessGroupDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	AccessGroup types.String `tfsdk:"access_group"`
	ModelNames  types.List   `tfsdk:"model_names"`
}

func (d *AccessGroupDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_access_group"
}

func (d *AccessGroupDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches information about a LiteLLM access group.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this access group.",
				Computed:    true,
			},
			"access_group": schema.StringAttribute{
				Description: "The access group name to look up.",
				Required:    true,
			},
			"model_names": schema.ListAttribute{
				Description: "List of model names in this access group.",
				Computed:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (d *AccessGroupDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *AccessGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data AccessGroupDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	accessGroup := data.AccessGroup.ValueString()
	endpoint := fmt.Sprintf("/access_group/%s/info", accessGroup)

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read access group: %s", err))
		return
	}

	// Populate the data model
	data.ID = types.StringValue(accessGroup)

	// Handle model_names list
	if modelNames, ok := result["model_names"].([]interface{}); ok {
		modelsList := make([]attr.Value, len(modelNames))
		for i, m := range modelNames {
			if str, ok := m.(string); ok {
				modelsList[i] = types.StringValue(str)
			}
		}
		data.ModelNames, _ = types.ListValue(types.StringType, modelsList)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
