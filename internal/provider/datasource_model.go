package provider

import (
	"context"
	"fmt"
	"net/url"
	"time"

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
	var config ModelDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	modelID := config.ModelID.ValueString()
	if config.ModelID.IsNull() || config.ModelID.IsUnknown() || modelID == "" {
		resp.Diagnostics.AddError("Invalid Model Lookup", "model_id must be known and nonempty")
		return
	}
	endpoint := endpointWithQuery("/model/info", url.Values{"litellm_model_id": []string{modelID}})
	var rawResult map[string]interface{}
	if err := readModelDataSourceWithRetry(ctx, d.client, endpoint, &rawResult, 8); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read model '%s': %s", modelID, err))
		return
	}
	result, err := modelDataSourceResult(rawResult)
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if result == nil {
		resp.Diagnostics.AddError("Not Found", fmt.Sprintf("Model not found: %s", modelID))
		return
	}
	actualID, err := dataSourceRequiredStringAt(result, "model_info", "id")
	if err != nil || actualID.ValueString() != modelID {
		resp.Diagnostics.AddError("Invalid API Response", "Model response identity did not match the requested model.")
		return
	}

	data := ModelDataSourceModel{ID: actualID, ModelID: config.ModelID}
	if data.ModelName, err = dataSourceNullableStringAt(result, "model_name"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	teamPublicName, publicErr := dataSourceNullableStringAt(result, "model_info", "team_public_model_name")
	if publicErr != nil {
		resp.Diagnostics.AddError("Invalid API Response", publicErr.Error())
		return
	}
	if data.TeamID, err = dataSourceNullableStringAt(result, "model_info", "team_id"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if !data.TeamID.IsNull() && data.TeamID.ValueString() != "" && !teamPublicName.IsNull() && teamPublicName.ValueString() != "" {
		data.ModelName = teamPublicName
	}
	if data.CustomLLMProvider, err = dataSourceNullableStringAt(result, "litellm_params", "custom_llm_provider"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.ModelAPIBase, err = dataSourceNullableStringAt(result, "litellm_params", "api_base"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.APIVersion, err = dataSourceNullableStringAt(result, "litellm_params", "api_version"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.TPM, err = dataSourceNullableInt64At(result, "litellm_params", "tpm"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.RPM, err = dataSourceNullableInt64At(result, "litellm_params", "rpm"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.AWSRegionName, err = dataSourceNullableStringAt(result, "litellm_params", "aws_region_name"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.BaseModel, err = dataSourceNullableStringAt(result, "model_info", "base_model"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.Tier, err = dataSourceNullableStringAt(result, "model_info", "tier"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.Mode, err = dataSourceNullableStringAt(result, "model_info", "mode"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func modelDataSourceResult(rawResult map[string]interface{}) (map[string]interface{}, error) {
	raw, presence, err := apiValueAt(rawResult, "data")
	if err != nil {
		return nil, err
	}
	if presence == apiValueAbsent {
		return rawResult, nil
	}
	if presence == apiValueNull {
		return nil, dataSourceShapeError([]string{"data"}, "a list containing exactly one object")
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, dataSourceShapeError([]string{"data"}, "a list containing exactly one object")
	}
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) != 1 {
		return nil, dataSourceShapeError([]string{"data"}, "a list containing exactly one object")
	}
	item, ok := items[0].(map[string]interface{})
	if !ok {
		return nil, dataSourceShapeError([]string{"data"}, "a list containing exactly one object")
	}
	return item, nil
}

func parseModelInfoResult(rawResult map[string]interface{}) map[string]interface{} {
	result, _ := modelDataSourceResult(rawResult)
	return result
}

func readModelDataSourceWithRetry(ctx context.Context, client *Client, endpoint string, result *map[string]interface{}, maxRetries int) error {
	var err error
	delay := time.Second
	maxDelay := 10 * time.Second
	for i := 0; i < maxRetries; i++ {
		err = client.DoRequestWithResponse(ctx, "GET", endpoint, nil, result)
		if err == nil {
			return nil
		}
		if !IsNotFoundError(err) {
			return err
		}
		if i < maxRetries-1 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return ctx.Err()
			case <-timer.C:
			}
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
	return err
}
