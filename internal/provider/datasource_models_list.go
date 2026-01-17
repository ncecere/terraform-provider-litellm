package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &ModelsListDataSource{}

func NewModelsListDataSource() datasource.DataSource {
	return &ModelsListDataSource{}
}

type ModelsListDataSource struct {
	client *Client
}

type ModelListItem struct {
	ID                types.String `tfsdk:"id"`
	ModelName         types.String `tfsdk:"model_name"`
	CustomLLMProvider types.String `tfsdk:"custom_llm_provider"`
	BaseModel         types.String `tfsdk:"base_model"`
	Tier              types.String `tfsdk:"tier"`
	Mode              types.String `tfsdk:"mode"`
	TeamID            types.String `tfsdk:"team_id"`
}

type ModelsListDataSourceModel struct {
	ID     types.String    `tfsdk:"id"`
	TeamID types.String    `tfsdk:"team_id"`
	Models []ModelListItem `tfsdk:"models"`
}

func (d *ModelsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_models"
}

func (d *ModelsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Retrieves a list of all LiteLLM models.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier.",
				Computed:    true,
			},
			"team_id": schema.StringAttribute{
				Description: "Optional team ID to filter models by team.",
				Optional:    true,
			},
			"models": schema.ListNestedAttribute{
				Description: "List of models.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Description: "The unique identifier for this model.",
							Computed:    true,
						},
						"model_name": schema.StringAttribute{
							Description: "The name of the model.",
							Computed:    true,
						},
						"custom_llm_provider": schema.StringAttribute{
							Description: "The LLM provider.",
							Computed:    true,
						},
						"base_model": schema.StringAttribute{
							Description: "The base model name.",
							Computed:    true,
						},
						"tier": schema.StringAttribute{
							Description: "Model tier.",
							Computed:    true,
						},
						"mode": schema.StringAttribute{
							Description: "Model mode.",
							Computed:    true,
						},
						"team_id": schema.StringAttribute{
							Description: "Team ID associated with this model.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *ModelsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ModelsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ModelsListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := "/model/info"
	if !data.TeamID.IsNull() && data.TeamID.ValueString() != "" {
		endpoint = fmt.Sprintf("/model/info?team_id=%s", data.TeamID.ValueString())
	}

	var result map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list models: %s", err))
		return
	}

	// Set placeholder ID
	data.ID = types.StringValue("models")

	// Parse the response - it may be in "data" array
	var modelsData []interface{}
	if dataArr, ok := result["data"].([]interface{}); ok {
		modelsData = dataArr
	} else if models, ok := result["models"].([]interface{}); ok {
		modelsData = models
	}

	data.Models = make([]ModelListItem, 0, len(modelsData))
	for _, m := range modelsData {
		modelMap, ok := m.(map[string]interface{})
		if !ok {
			continue
		}

		item := ModelListItem{}

		// Get model_info
		if modelInfo, ok := modelMap["model_info"].(map[string]interface{}); ok {
			if id, ok := modelInfo["id"].(string); ok {
				item.ID = types.StringValue(id)
			}
			if baseModel, ok := modelInfo["base_model"].(string); ok {
				item.BaseModel = types.StringValue(baseModel)
			}
			if tier, ok := modelInfo["tier"].(string); ok {
				item.Tier = types.StringValue(tier)
			}
			if mode, ok := modelInfo["mode"].(string); ok {
				item.Mode = types.StringValue(mode)
			}
			if teamID, ok := modelInfo["team_id"].(string); ok {
				item.TeamID = types.StringValue(teamID)
			}
		}

		// Get model name
		if modelName, ok := modelMap["model_name"].(string); ok {
			item.ModelName = types.StringValue(modelName)
		}

		// Get litellm_params
		if litellmParams, ok := modelMap["litellm_params"].(map[string]interface{}); ok {
			if provider, ok := litellmParams["custom_llm_provider"].(string); ok {
				item.CustomLLMProvider = types.StringValue(provider)
			}
		}

		data.Models = append(data.Models, item)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
