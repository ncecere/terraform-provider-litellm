package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &PromptsListDataSource{}

func NewPromptsListDataSource() datasource.DataSource {
	return &PromptsListDataSource{}
}

type PromptsListDataSource struct {
	client *Client
}

type PromptsListDataSourceModel struct {
	ID      types.String          `tfsdk:"id"`
	Prompts []PromptListItemModel `tfsdk:"prompts"`
}

type PromptListItemModel struct {
	PromptID                          types.String `tfsdk:"prompt_id"`
	PromptIntegration                 types.String `tfsdk:"prompt_integration"`
	APIBase                           types.String `tfsdk:"api_base"`
	ProviderSpecificQueryParams       types.String `tfsdk:"provider_specific_query_params"`
	IgnorePromptManagerModel          types.Bool   `tfsdk:"ignore_prompt_manager_model"`
	IgnorePromptManagerOptionalParams types.Bool   `tfsdk:"ignore_prompt_manager_optional_params"`
	PromptType                        types.String `tfsdk:"prompt_type"`
}

func (d *PromptsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_prompts"
}

func (d *PromptsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches a list of all LiteLLM prompts.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier for this data source.",
				Computed:    true,
			},
			"prompts": schema.ListNestedAttribute{
				Description: "List of prompts.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"prompt_id": schema.StringAttribute{
							Description: "The prompt ID.",
							Computed:    true,
						},
						"prompt_integration": schema.StringAttribute{
							Description: "The prompt integration provider.",
							Computed:    true,
						},
						"api_base": schema.StringAttribute{
							Description: "Base URL for the prompt provider API.",
							Computed:    true,
						},
						"provider_specific_query_params": schema.StringAttribute{
							Description: "JSON string of provider-specific query parameters.",
							Computed:    true,
						},
						"ignore_prompt_manager_model": schema.BoolAttribute{
							Description: "If true, ignore the model specified in the prompt manager.",
							Computed:    true,
						},
						"ignore_prompt_manager_optional_params": schema.BoolAttribute{
							Description: "If true, ignore optional params from the prompt manager.",
							Computed:    true,
						},
						"prompt_type": schema.StringAttribute{
							Description: "Type of prompt: 'config' or 'db'.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *PromptsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PromptsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PromptsListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var results []map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", "/prompts/list", nil, &results); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list prompts: %s", err))
		return
	}

	prompts := make([]PromptListItemModel, 0, len(results))
	for _, result := range results {
		prompt := PromptListItemModel{}

		if promptID, ok := result["prompt_id"].(string); ok {
			prompt.PromptID = types.StringValue(promptID)
		}

		// Handle litellm_params
		if litellmParams, ok := result["litellm_params"].(map[string]interface{}); ok {
			if integration, ok := litellmParams["prompt_integration"].(string); ok {
				prompt.PromptIntegration = types.StringValue(integration)
			}
			if apiBase, ok := litellmParams["api_base"].(string); ok {
				prompt.APIBase = types.StringValue(apiBase)
			}
			if ignoreModel, ok := litellmParams["ignore_prompt_manager_model"].(bool); ok {
				prompt.IgnorePromptManagerModel = types.BoolValue(ignoreModel)
			}
			if ignoreParams, ok := litellmParams["ignore_prompt_manager_optional_params"].(bool); ok {
				prompt.IgnorePromptManagerOptionalParams = types.BoolValue(ignoreParams)
			}
			if providerParams, ok := litellmParams["provider_specific_query_params"].(map[string]interface{}); ok {
				if jsonBytes, err := json.Marshal(providerParams); err == nil {
					prompt.ProviderSpecificQueryParams = types.StringValue(string(jsonBytes))
				}
			}
		}

		// Handle prompt_info
		if promptInfo, ok := result["prompt_info"].(map[string]interface{}); ok {
			if promptType, ok := promptInfo["prompt_type"].(string); ok {
				prompt.PromptType = types.StringValue(promptType)
			}
		}

		prompts = append(prompts, prompt)
	}

	data.ID = types.StringValue("prompts")
	data.Prompts = prompts

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
