package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &SearchToolDataSource{}

func NewSearchToolDataSource() datasource.DataSource {
	return &SearchToolDataSource{}
}

type SearchToolDataSource struct {
	client *Client
}

type SearchToolDataSourceModel struct {
	ID             types.String  `tfsdk:"id"`
	SearchToolID   types.String  `tfsdk:"search_tool_id"`
	SearchToolName types.String  `tfsdk:"search_tool_name"`
	SearchProvider types.String  `tfsdk:"search_provider"`
	APIBase        types.String  `tfsdk:"api_base"`
	Timeout        types.Float64 `tfsdk:"timeout"`
	MaxRetries     types.Int64   `tfsdk:"max_retries"`
	SearchToolInfo types.String  `tfsdk:"search_tool_info"`
}

func (d *SearchToolDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_search_tool"
}

func (d *SearchToolDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM search tool.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this search tool (same as search_tool_id).",
				Computed:    true,
			},
			"search_tool_id": schema.StringAttribute{
				Description: "Unique identifier for the search tool.",
				Required:    true,
			},
			"search_tool_name": schema.StringAttribute{
				Description: "Name of the search tool.",
				Computed:    true,
			},
			"search_provider": schema.StringAttribute{
				Description: "The search provider used (e.g., 'tavily', 'serper', 'bing', 'google').",
				Computed:    true,
			},
			"api_base": schema.StringAttribute{
				Description: "Base URL for the search API.",
				Computed:    true,
			},
			"timeout": schema.Float64Attribute{
				Description: "Timeout in seconds for search requests.",
				Computed:    true,
			},
			"max_retries": schema.Int64Attribute{
				Description: "Maximum number of retries for failed requests.",
				Computed:    true,
			},
			"search_tool_info": schema.StringAttribute{
				Description: "Additional search tool configuration as a JSON string.",
				Computed:    true,
			},
		},
	}
}

func (d *SearchToolDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *SearchToolDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config SearchToolDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	searchToolID := config.SearchToolID.ValueString()
	if searchToolDataSourceLookupInvalid(resp, searchToolID) {
		return
	}
	endpoint := endpointWithPathSegment("/search_tools/", searchToolID, "")
	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read search tool: %s", err))
		return
	}
	if err := validateSearchToolAPIObject(result, searchToolID); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}

	actualID, err := dataSourceRequiredStringAt(result, "search_tool_id")
	if err != nil || actualID.ValueString() != searchToolID {
		resp.Diagnostics.AddError("Invalid API Response", "Search tool response identity did not match the requested search tool.")
		return
	}
	data := SearchToolDataSourceModel{ID: actualID, SearchToolID: config.SearchToolID}
	if data.SearchToolName, err = dataSourceRequiredStringAt(result, "search_tool_name"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.SearchProvider, err = dataSourceRequiredStringAt(result, "litellm_params", "search_provider"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.APIBase, err = dataSourceNullableStringAt(result, "litellm_params", "api_base"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.Timeout, err = dataSourceNullableFloat64At(result, "litellm_params", "timeout"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.MaxRetries, err = dataSourceNullableInt64At(result, "litellm_params", "max_retries"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.SearchToolInfo, err = dataSourceNullableCanonicalJSONObjectAt(result, "search_tool_info"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func searchToolDataSourceLookupInvalid(resp *datasource.ReadResponse, searchToolID string) bool {
	if searchToolID != "" {
		return false
	}
	resp.Diagnostics.AddError("Invalid Search Tool Lookup", "search_tool_id must be known and nonempty")
	return true
}
