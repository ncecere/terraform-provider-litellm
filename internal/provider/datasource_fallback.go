package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &FallbackDataSource{}

func NewFallbackDataSource() datasource.DataSource {
	return &FallbackDataSource{}
}

type FallbackDataSource struct {
	client *Client
}

type FallbackDataSourceModel struct {
	ID             types.String `tfsdk:"id"`
	Model          types.String `tfsdk:"model"`
	FallbackType   types.String `tfsdk:"fallback_type"`
	FallbackModels types.List   `tfsdk:"fallback_models"`
}

func (d *FallbackDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fallback"
}

func (d *FallbackDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves fallback configuration for a LiteLLM model.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for this fallback (model:fallback_type).",
				Computed:    true,
			},
			"model": schema.StringAttribute{
				Description: "The model name to get fallbacks for.",
				Required:    true,
			},
			"fallback_type": schema.StringAttribute{
				Description: "Type of fallback: 'general', 'context_window', or 'content_policy'. Defaults to 'general'.",
				Optional:    true,
				Computed:    true,
			},
			"fallback_models": schema.ListAttribute{
				Description: "List of fallback model names in order of priority.",
				Computed:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (d *FallbackDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *FallbackDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data FallbackDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fallbackType := data.FallbackType.ValueString()
	if fallbackType == "" {
		fallbackType = "general"
	}

	endpoint := fmt.Sprintf("/fallback/%s?fallback_type=%s",
		url.PathEscape(data.Model.ValueString()),
		url.QueryEscape(fallbackType))

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read fallback for model '%s': %s", data.Model.ValueString(), err))
		return
	}

	data.ID = types.StringValue(data.Model.ValueString() + ":" + fallbackType)
	data.FallbackType = types.StringValue(fallbackType)

	if fallbackModels, ok := result["fallback_models"].([]interface{}); ok {
		list := make([]attr.Value, 0, len(fallbackModels))
		for _, m := range fallbackModels {
			if s, ok := m.(string); ok {
				list = append(list, types.StringValue(s))
			}
		}
		data.FallbackModels, _ = types.ListValue(types.StringType, list)
	} else {
		data.FallbackModels, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
