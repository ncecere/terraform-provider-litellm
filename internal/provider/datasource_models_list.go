package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
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
				Description: "Stable historical identifier for this data source.",
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

	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("team_id"), &data.TeamID)...)
	if resp.Diagnostics.HasError() {
		return
	}

	filters := modelListFilters(data.TeamID)
	endpoint := endpointWithQuery("/model/info", filters)

	var rawResult json.RawMessage
	if err := d.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &rawResult); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list models: %s", safeListDiagnostic(err, filters)))
		return
	}
	modelsData, err := decodeEnvelopeListOrObject(rawResult, "/model/info", "data")
	if err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}

	data.ID = types.StringValue("models")
	data.Models = make([]ModelListItem, 0, len(modelsData))
	seen := make(map[string]struct{}, len(modelsData))
	for _, rawModel := range modelsData {
		modelMap, err := decodeListObject(rawModel, "/model/info", "model item")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		modelInfo, err := dataSourceRequiredObjectAt(modelMap, "model_info")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}

		item := ModelListItem{}
		item.ID, err = dataSourceRequiredStringAt(modelInfo, "id")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", "/model/info returned a model object without a canonical model_info.id")
			return
		}
		if err := dataSourceListIdentity(seen, item.ID.ValueString(), "/model/info", "model_info.id"); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		for _, field := range []struct {
			name   string
			target *types.String
		}{
			{"base_model", &item.BaseModel}, {"tier", &item.Tier}, {"mode", &item.Mode}, {"team_id", &item.TeamID},
		} {
			*field.target, err = dataSourceNullableStringAt(modelInfo, field.name)
			if err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		publicName, err := dataSourceNullableStringAt(modelInfo, "team_public_model_name")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		item.ModelName, err = dataSourceNullableStringAt(modelMap, "model_name")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if !item.TeamID.IsNull() && item.TeamID.ValueString() != "" && !publicName.IsNull() && publicName.ValueString() != "" {
			item.ModelName = publicName
		}

		litellmParams, present, err := dataSourceNullableObjectAt(modelMap, "litellm_params")
		if err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		item.CustomLLMProvider = types.StringNull()
		if present {
			item.CustomLLMProvider, err = dataSourceNullableStringAt(litellmParams, "custom_llm_provider")
			if err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		data.Models = append(data.Models, item)
	}
	sort.SliceStable(data.Models, func(i, j int) bool {
		left := []string{data.Models[i].ID.ValueString(), data.Models[i].ModelName.ValueString(), data.Models[i].TeamID.ValueString()}
		right := []string{data.Models[j].ID.ValueString(), data.Models[j].ModelName.ValueString(), data.Models[j].TeamID.ValueString()}
		for index := range left {
			if left[index] != right[index] {
				return left[index] < right[index]
			}
		}
		return false
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func modelListFilters(teamID types.String) url.Values {
	filters := url.Values{}
	// LiteLLM v1.98 intentionally uses camel-case teamId on /model/info.
	addKnownStringFilter(filters, "teamId", teamID)
	return filters
}
