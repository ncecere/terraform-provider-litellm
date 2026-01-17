package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ModelDataSource{}

func NewModelDataSource() datasource.DataSource {
	return &ModelDataSource{}
}

type ModelDataSource struct {
	client *Client
}

type ModelDataSourceModel struct {
	ID                types.String `tfsdk:"id"`
	ModelID           types.String `tfsdk:"model_id"`
	ModelName         types.String `tfsdk:"model_name"`
	CustomLLMProvider types.String `tfsdk:"custom_llm_provider"`
	BaseModel         types.String `tfsdk:"base_model"`
	Tier              types.String `tfsdk:"tier"`
	Mode              types.String `tfsdk:"mode"`
	TeamID            types.String `tfsdk:"team_id"`
	TPM               types.Int64  `tfsdk:"tpm"`
	RPM               types.Int64  `tfsdk:"rpm"`
	ModelAPIBase      types.String `tfsdk:"model_api_base"`
	APIVersion        types.String `tfsdk:"api_version"`
	AWSRegionName     types.String `tfsdk:"aws_region_name"`
}

func (d *ModelDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_model"
}

func (d *ModelDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves information about a LiteLLM model.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this model.",
				Computed:    true,
			},
			"model_id": schema.StringAttribute{
				Description: "The model ID to look up (litellm_model_id).",
				Required:    true,
			},
			"model_name": schema.StringAttribute{
				Description: "The name of the model as it appears in LiteLLM.",
				Computed:    true,
			},
			"custom_llm_provider": schema.StringAttribute{
				Description: "The LLM provider (e.g., openai, anthropic, bedrock).",
				Computed:    true,
			},
			"base_model": schema.StringAttribute{
				Description: "The base model name from the provider.",
				Computed:    true,
			},
			"tier": schema.StringAttribute{
				Description: "Model tier (free, paid, etc.).",
				Computed:    true,
			},
			"mode": schema.StringAttribute{
				Description: "Model mode (completion, embedding, image_generation, chat, etc.).",
				Computed:    true,
			},
			"team_id": schema.StringAttribute{
				Description: "Team ID associated with this model.",
				Computed:    true,
			},
			"tpm": schema.Int64Attribute{
				Description: "Tokens per minute limit.",
				Computed:    true,
			},
			"rpm": schema.Int64Attribute{
				Description: "Requests per minute limit.",
				Computed:    true,
			},
			"model_api_base": schema.StringAttribute{
				Description: "Base URL for the model API.",
				Computed:    true,
			},
			"api_version": schema.StringAttribute{
				Description: "API version (e.g., for Azure OpenAI).",
				Computed:    true,
			},
			"aws_region_name": schema.StringAttribute{
				Description: "AWS region name for Bedrock.",
				Computed:    true,
			},
		},
	}
}

func (d *ModelDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ModelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ModelDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	modelID := data.ModelID.ValueString()
	endpoint := fmt.Sprintf("/model/info?litellm_model_id=%s", modelID)

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read model '%s': %s", modelID, err))
		return
	}

	// Set ID
	data.ID = data.ModelID

	// Update fields from response
	if modelName, ok := result["model_name"].(string); ok {
		data.ModelName = types.StringValue(modelName)
	}

	// Parse litellm_params
	if litellmParams, ok := result["litellm_params"].(map[string]interface{}); ok {
		if provider, ok := litellmParams["custom_llm_provider"].(string); ok {
			data.CustomLLMProvider = types.StringValue(provider)
		}
		if apiBase, ok := litellmParams["api_base"].(string); ok {
			data.ModelAPIBase = types.StringValue(apiBase)
		}
		if apiVersion, ok := litellmParams["api_version"].(string); ok {
			data.APIVersion = types.StringValue(apiVersion)
		}
		if tpm, ok := litellmParams["tpm"].(float64); ok {
			data.TPM = types.Int64Value(int64(tpm))
		}
		if rpm, ok := litellmParams["rpm"].(float64); ok {
			data.RPM = types.Int64Value(int64(rpm))
		}
		if awsRegion, ok := litellmParams["aws_region_name"].(string); ok {
			data.AWSRegionName = types.StringValue(awsRegion)
		}
	}

	// Parse model_info
	if modelInfo, ok := result["model_info"].(map[string]interface{}); ok {
		if baseModel, ok := modelInfo["base_model"].(string); ok {
			data.BaseModel = types.StringValue(baseModel)
		}
		if tier, ok := modelInfo["tier"].(string); ok {
			data.Tier = types.StringValue(tier)
		}
		if mode, ok := modelInfo["mode"].(string); ok {
			data.Mode = types.StringValue(mode)
		}
		if teamID, ok := modelInfo["team_id"].(string); ok {
			data.TeamID = types.StringValue(teamID)
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
