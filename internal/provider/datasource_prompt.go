package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &PromptDataSource{}

func NewPromptDataSource() datasource.DataSource {
	return &PromptDataSource{}
}

type PromptDataSource struct {
	client *Client
}

type PromptDataSourceModel struct {
	ID                                types.String `tfsdk:"id"`
	PromptID                          types.String `tfsdk:"prompt_id"`
	PromptIntegration                 types.String `tfsdk:"prompt_integration"`
	APIBase                           types.String `tfsdk:"api_base"`
	ProviderSpecificQueryParams       types.String `tfsdk:"provider_specific_query_params"`
	IgnorePromptManagerModel          types.Bool   `tfsdk:"ignore_prompt_manager_model"`
	IgnorePromptManagerOptionalParams types.Bool   `tfsdk:"ignore_prompt_manager_optional_params"`
	DotpromptContent                  types.String `tfsdk:"dotprompt_content"`
	PromptType                        types.String `tfsdk:"prompt_type"`
}

func (d *PromptDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_prompt"
}

func (d *PromptDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches information about a LiteLLM prompt.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this prompt.",
				Computed:    true,
			},
			"prompt_id": schema.StringAttribute{
				Description: "The prompt ID to look up.",
				Required:    true,
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
			"dotprompt_content": schema.StringAttribute{
				Description: "Content for dotprompt integration.",
				Computed:    true,
			},
			"prompt_type": schema.StringAttribute{
				Description: "Type of prompt: 'config' or 'db'.",
				Computed:    true,
			},
		},
	}
}

func (d *PromptDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PromptDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PromptDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	promptID := data.PromptID.ValueString()
	endpoint := fmt.Sprintf("/prompts/%s/info", promptID)

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read prompt: %s", err))
		return
	}

	// Populate the data model
	data.ID = types.StringValue(promptID)

	// Handle litellm_params
	if litellmParams, ok := result["litellm_params"].(map[string]interface{}); ok {
		if integration, ok := litellmParams["prompt_integration"].(string); ok {
			data.PromptIntegration = types.StringValue(integration)
		}
		if apiBase, ok := litellmParams["api_base"].(string); ok {
			data.APIBase = types.StringValue(apiBase)
		}
		if ignoreModel, ok := litellmParams["ignore_prompt_manager_model"].(bool); ok {
			data.IgnorePromptManagerModel = types.BoolValue(ignoreModel)
		}
		if ignoreParams, ok := litellmParams["ignore_prompt_manager_optional_params"].(bool); ok {
			data.IgnorePromptManagerOptionalParams = types.BoolValue(ignoreParams)
		}
		if dotprompt, ok := litellmParams["dotprompt_content"].(string); ok {
			data.DotpromptContent = types.StringValue(dotprompt)
		}
		if providerParams, ok := litellmParams["provider_specific_query_params"].(map[string]interface{}); ok {
			if jsonBytes, err := json.Marshal(providerParams); err == nil {
				data.ProviderSpecificQueryParams = types.StringValue(string(jsonBytes))
			}
		}
	}

	// Handle prompt_info
	if promptInfo, ok := result["prompt_info"].(map[string]interface{}); ok {
		if promptType, ok := promptInfo["prompt_type"].(string); ok {
			data.PromptType = types.StringValue(promptType)
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
