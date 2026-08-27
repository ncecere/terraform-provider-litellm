package provider

import (
	"context"
	"fmt"

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
				Description: "Sorted, deduplicated list of model names in this access group.",
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
	var config AccessGroupDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	accessGroup := config.AccessGroup.ValueString()
	if accessGroup == "" {
		resp.Diagnostics.AddError("Invalid Access Group Lookup", "access_group must be known and nonempty")
		return
	}
	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpointWithPathSegment("/access_group/", accessGroup, "/info"), nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read access group: %s", err))
		return
	}

	actualAccessGroup, err := dataSourceRequiredStringAt(result, "access_group")
	if err != nil || actualAccessGroup.ValueString() != accessGroup {
		resp.Diagnostics.AddError("Invalid API Response", "Access group response identity did not match the requested access group.")
		return
	}
	modelNames, err := dataSourceNullableStringListAt(result, "model_names")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", fmt.Sprintf("Unable to decode model_names: %s", err))
		return
	}
	if !modelNames.IsNull() {
		modelNames, err = reconcileAccessGroupModelNames(ctx, types.ListNull(types.StringType), result["model_names"])
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", fmt.Sprintf("Unable to decode model_names: %s", err))
			return
		}
	}

	data := AccessGroupDataSourceModel{
		ID:          types.StringValue(accessGroup),
		AccessGroup: config.AccessGroup,
		ModelNames:  modelNames,
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
