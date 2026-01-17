package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &BudgetsListDataSource{}

func NewBudgetsListDataSource() datasource.DataSource {
	return &BudgetsListDataSource{}
}

type BudgetsListDataSource struct {
	client *Client
}

type BudgetsListDataSourceModel struct {
	ID      types.String          `tfsdk:"id"`
	Budgets []BudgetListItemModel `tfsdk:"budgets"`
}

type BudgetListItemModel struct {
	BudgetID            types.String  `tfsdk:"budget_id"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	ModelMaxBudget      types.String  `tfsdk:"model_max_budget"`
}

func (d *BudgetsListDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_budgets"
}

func (d *BudgetsListDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches a list of all LiteLLM budgets.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Placeholder identifier for this data source.",
				Computed:    true,
			},
			"budgets": schema.ListNestedAttribute{
				Description: "List of budgets.",
				Computed:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"budget_id": schema.StringAttribute{
							Description: "The budget ID.",
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

func (d *BudgetsListDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *BudgetsListDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data BudgetsListDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var results []map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "GET", "/budget/list", nil, &results); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list budgets: %s", err))
		return
	}

	budgets := make([]BudgetListItemModel, 0, len(results))
	for _, result := range results {
		budget := BudgetListItemModel{}

		if budgetID, ok := result["budget_id"].(string); ok {
			budget.BudgetID = types.StringValue(budgetID)
		}
		if maxBudget, ok := result["max_budget"].(float64); ok {
			budget.MaxBudget = types.Float64Value(maxBudget)
		}
		if softBudget, ok := result["soft_budget"].(float64); ok {
			budget.SoftBudget = types.Float64Value(softBudget)
		}
		if maxParallel, ok := result["max_parallel_requests"].(float64); ok {
			budget.MaxParallelRequests = types.Int64Value(int64(maxParallel))
		}
		if tpmLimit, ok := result["tpm_limit"].(float64); ok {
			budget.TPMLimit = types.Int64Value(int64(tpmLimit))
		}
		if rpmLimit, ok := result["rpm_limit"].(float64); ok {
			budget.RPMLimit = types.Int64Value(int64(rpmLimit))
		}
		if budgetDuration, ok := result["budget_duration"].(string); ok {
			budget.BudgetDuration = types.StringValue(budgetDuration)
		}
		if modelMaxBudget, ok := result["model_max_budget"].(map[string]interface{}); ok && len(modelMaxBudget) > 0 {
			if jsonBytes, err := json.Marshal(modelMaxBudget); err == nil {
				budget.ModelMaxBudget = types.StringValue(string(jsonBytes))
			}
		}

		budgets = append(budgets, budget)
	}

	data.ID = types.StringValue("budgets")
	data.Budgets = budgets

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
