package provider

import (
	"context"
	"fmt"
	"sort"

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

	client, ok := configuredClient(req.ProviderData)
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

	const endpoint = "/search_tools/list"
	result, err := fetchEnvelopeListObjects(ctx, d.client, endpoint, "search_tools", "search tool item")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list search tools: %s", err))
		return
	}

	data.ID = types.StringValue("search_tools")
	data.SearchTools = make([]SearchToolListItem, 0, len(result))
	for _, toolMap := range result {

		item := SearchToolListItem{}

		if searchToolID, ok := toolMap["search_tool_id"].(string); ok {
			item.SearchToolID = types.StringValue(searchToolID)
		}
		if searchToolName, ok := toolMap["search_tool_name"].(string); ok && searchToolName != "" {
			item.SearchToolName = types.StringValue(searchToolName)
		} else {
			resp.Diagnostics.AddError("Invalid API Response", "/search_tools/list returned a search tool object without search_tool_name")
			return
		}

		// Handle litellm_params
		if litellmParams, ok := toolMap["litellm_params"].(map[string]interface{}); ok {
			if searchProvider, ok := litellmParams["search_provider"].(string); ok {
				item.SearchProvider = types.StringValue(searchProvider)
			}
			if apiBase, ok := litellmParams["api_base"].(string); ok {
				item.APIBase = types.StringValue(apiBase)
			}
			if err := updateFloat64FromAPI(&item.Timeout, litellmParams, true, true, "timeout"); err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
			if err := updateInt64FromAPI(&item.MaxRetries, litellmParams, true, true, "max_retries"); err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}

		data.SearchTools = append(data.SearchTools, item)
	}
	sort.SliceStable(data.SearchTools, func(i, j int) bool {
		leftID := data.SearchTools[i].SearchToolID.ValueString()
		rightID := data.SearchTools[j].SearchToolID.ValueString()
		if leftID != rightID {
			return leftID < rightID
		}
		return data.SearchTools[i].SearchToolName.ValueString() < data.SearchTools[j].SearchToolName.ValueString()
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
