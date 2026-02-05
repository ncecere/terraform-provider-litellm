package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &SearchToolsListDataSource{}

func NewSearchToolsListDataSource() datasource.DataSource {
	return &SearchToolsListDataSource{}
}

type SearchToolsListDataSource struct {
	client *Client
}

type SearchToolListItem struct {
	SearchToolID   types.String  `tfsdk:"search_tool_id"`
	SearchToolName types.String  `tfsdk:"search_tool_name"`
	SearchProvider types.String  `tfsdk:"search_provider"`
	APIBase        types.String  `tfsdk:"api_base"`
	Timeout        types.Float64 `tfsdk:"timeout"`
	MaxRetries     types.Int64   `tfsdk:"max_retries"`
}

type SearchToolsListDataSourceModel struct {
	ID          types.String         `tfsdk:"id"`
	SearchTools []SearchToolListItem `tfsdk:"search_tools"`
}

func (d *SearchToolsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_search_tools"
}

func (d *SearchToolsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves a list of LiteLLM search tools.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier.",
				Computed:    true,
			},
			"search_tools": schema.ListNestedAttribute{
				Description: "List of search tools.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"search_tool_id": schema.StringAttribute{
							Description: "The unique identifier for this search tool.",
							Computed:    true,
						},
						"search_tool_name": schema.StringAttribute{
							Description: "Name of the search tool.",
							Computed:    true,
						},
						"search_provider": schema.StringAttribute{
							Description: "The search provider used.",
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
					},
				},
			},
		},
	}
}

func (d *SearchToolsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *SearchToolsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data SearchToolsListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := "/search_tools/list"

	var result []interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		// Try parsing as object with data field
		var objResult map[string]interface{}
		if err2 := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &objResult); err2 != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list search tools: %s", err))
			return
		}
		if dataArr, ok := objResult["data"].([]interface{}); ok {
			result = dataArr
		} else if toolsArr, ok := objResult["search_tools"].([]interface{}); ok {
			result = toolsArr
		}
	}

	// Set placeholder ID
	data.ID = types.StringValue("search_tools")

	data.SearchTools = make([]SearchToolListItem, 0, len(result))
	for _, s := range result {
		toolMap, ok := s.(map[string]interface{})
		if !ok {
			continue
		}

		item := SearchToolListItem{}

		if searchToolID, ok := toolMap["search_tool_id"].(string); ok {
			item.SearchToolID = types.StringValue(searchToolID)
		}
		if searchToolName, ok := toolMap["search_tool_name"].(string); ok {
			item.SearchToolName = types.StringValue(searchToolName)
		}

		// Handle litellm_params
		if litellmParams, ok := toolMap["litellm_params"].(map[string]interface{}); ok {
			if searchProvider, ok := litellmParams["search_provider"].(string); ok {
				item.SearchProvider = types.StringValue(searchProvider)
			}
			if apiBase, ok := litellmParams["api_base"].(string); ok {
				item.APIBase = types.StringValue(apiBase)
			}
			if timeout, ok := litellmParams["timeout"].(float64); ok {
				item.Timeout = types.Float64Value(timeout)
			}
			if maxRetries, ok := litellmParams["max_retries"].(float64); ok {
				item.MaxRetries = types.Int64Value(int64(maxRetries))
			}
		}

		data.SearchTools = append(data.SearchTools, item)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
