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
	var data SearchToolDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	searchToolID := data.SearchToolID.ValueString()
	endpoint := fmt.Sprintf("/search_tools/%s", searchToolID)

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read search tool '%s': %s", searchToolID, err))
		return
	}

	// Update fields from response
	if stID, ok := result["search_tool_id"].(string); ok {
		data.SearchToolID = types.StringValue(stID)
		data.ID = types.StringValue(stID)
	}

	if searchToolName, ok := result["search_tool_name"].(string); ok {
		data.SearchToolName = types.StringValue(searchToolName)
	}

	// Handle litellm_params
	if litellmParams, ok := result["litellm_params"].(map[string]interface{}); ok {
		if searchProvider, ok := litellmParams["search_provider"].(string); ok {
			data.SearchProvider = types.StringValue(searchProvider)
		}
		if apiBase, ok := litellmParams["api_base"].(string); ok {
			data.APIBase = types.StringValue(apiBase)
		}
		if timeout, ok := litellmParams["timeout"].(float64); ok {
			data.Timeout = types.Float64Value(timeout)
		}
		if maxRetries, ok := litellmParams["max_retries"].(float64); ok {
			data.MaxRetries = types.Int64Value(int64(maxRetries))
		}
	}

	if searchToolInfo, ok := result["search_tool_info"].(string); ok {
		data.SearchToolInfo = types.StringValue(searchToolInfo)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
