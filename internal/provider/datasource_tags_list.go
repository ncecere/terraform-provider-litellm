package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &TagsListDataSource{}

func NewTagsListDataSource() datasource.DataSource {
	return &TagsListDataSource{}
}

type TagsListDataSource struct {
	client *Client
}

type TagsListDataSourceModel struct {
	ID   types.String       `tfsdk:"id"`
	Tags []TagListItemModel `tfsdk:"tags"`
}

type TagListItemModel struct {
	Name                types.String  `tfsdk:"name"`
	Description         types.String  `tfsdk:"description"`
	Models              types.List    `tfsdk:"models"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	ModelMaxBudget      types.String  `tfsdk:"model_max_budget"`
}

func (d *TagsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tags"
}

func (d *TagsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches a list of all LiteLLM tags.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier for this data source.",
				Computed:    true,
			},
			"tags": schema.ListNestedAttribute{
				Description: "List of tags.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Description: "The tag name.",
							Computed:    true,
						},
						"description": schema.StringAttribute{
							Description: "Description of the tag.",
							Computed:    true,
						},
						"models": schema.ListAttribute{
							Description: "Models associated with this tag.",
							Computed:    true,
							ElementType: types.StringType,
						},
						"budget_id": schema.StringAttribute{
							Description: "Budget ID associated with this tag.",
							Computed:    true,
						},
						"max_budget": schema.Float64Attribute{
							Description: "Max budget in USD.",
							Computed:    true,
						},
						"soft_budget": schema.Float64Attribute{
							Description: "Soft budget in USD.",
							Computed:    true,
						},
						"max_parallel_requests": schema.Int64Attribute{
							Description: "Max concurrent requests allowed.",
							Computed:    true,
						},
						"tpm_limit": schema.Int64Attribute{
							Description: "Max tokens per minute.",
							Computed:    true,
						},
						"rpm_limit": schema.Int64Attribute{
							Description: "Max requests per minute.",
							Computed:    true,
						},
						"budget_duration": schema.StringAttribute{
							Description: "Duration for budget reset.",
							Computed:    true,
						},
						"model_max_budget": schema.StringAttribute{
							Description: "JSON string for per-model budget configuration.",
							Computed:    true,
						},
					},
				},
			},
		},
	}
}

func (d *TagsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TagsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data TagsListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var results []map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", "/tag/list", nil, &results); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list tags: %s", err))
		return
	}

	tags := make([]TagListItemModel, 0, len(results))
	for _, result := range results {
		tag := TagListItemModel{}

		if name, ok := result["name"].(string); ok {
			tag.Name = types.StringValue(name)
		}
		if description, ok := result["description"].(string); ok {
			tag.Description = types.StringValue(description)
		}
		if budgetID, ok := result["budget_id"].(string); ok {
			tag.BudgetID = types.StringValue(budgetID)
		}
		if maxBudget, ok := result["max_budget"].(float64); ok {
			tag.MaxBudget = types.Float64Value(maxBudget)
		}
		if softBudget, ok := result["soft_budget"].(float64); ok {
			tag.SoftBudget = types.Float64Value(softBudget)
		}
		if maxParallel, ok := result["max_parallel_requests"].(float64); ok {
			tag.MaxParallelRequests = types.Int64Value(int64(maxParallel))
		}
		if tpmLimit, ok := result["tpm_limit"].(float64); ok {
			tag.TPMLimit = types.Int64Value(int64(tpmLimit))
		}
		if rpmLimit, ok := result["rpm_limit"].(float64); ok {
			tag.RPMLimit = types.Int64Value(int64(rpmLimit))
		}
		if budgetDuration, ok := result["budget_duration"].(string); ok {
			tag.BudgetDuration = types.StringValue(budgetDuration)
		}

		// Handle models list
		if models, ok := result["models"].([]interface{}); ok {
			modelsList := make([]attr.Value, len(models))
			for i, m := range models {
				if str, ok := m.(string); ok {
					modelsList[i] = types.StringValue(str)
				}
			}
			tag.Models, _ = types.ListValue(types.StringType, modelsList)
		}

		// Handle model_max_budget
		if modelMaxBudget, ok := result["model_max_budget"].(map[string]interface{}); ok && len(modelMaxBudget) > 0 {
			if jsonBytes, err := json.Marshal(modelMaxBudget); err == nil {
				tag.ModelMaxBudget = types.StringValue(string(jsonBytes))
			}
		}

		tags = append(tags, tag)
	}

	data.ID = types.StringValue("tags")
	data.Tags = tags

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
