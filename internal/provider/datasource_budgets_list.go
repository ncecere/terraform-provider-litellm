package provider

import (
	"context"
	"fmt"
	"sort"

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
	BudgetResetAt       types.String  `tfsdk:"budget_reset_at"`
	CreatedAt           types.String  `tfsdk:"created_at"`
	UpdatedAt           types.String  `tfsdk:"updated_at"`
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
						"budget_reset_at": schema.StringAttribute{
							Description: "Timestamp when the budget will next reset.",
							Computed:    true,
						},
						"created_at": schema.StringAttribute{
							Description: "Timestamp when the budget was created.",
							Computed:    true,
						},
						"updated_at": schema.StringAttribute{
							Description: "Timestamp when the budget was last updated.",
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

	results, err := fetchTopLevelListObjects(ctx, d.client, "/budget/list", "budget item")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list budgets: %s", err))
		return
	}

	budgets := make([]BudgetListItemModel, 0, len(results))
	for _, result := range results {
		budget := BudgetListItemModel{}

		if budgetID, ok := result["budget_id"].(string); ok && budgetID != "" {
			budget.BudgetID = types.StringValue(budgetID)
		} else {
			resp.Diagnostics.AddError("Invalid API Response", "/budget/list returned a budget object without budget_id")
			return
		}
		for _, field := range []struct {
			name   string
			target *types.Float64
		}{
			{"max_budget", &budget.MaxBudget},
			{"soft_budget", &budget.SoftBudget},
		} {
			if err := updateFloat64FromAPI(field.target, result, true, true, field.name); err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		for _, field := range []struct {
			name   string
			target *types.Int64
		}{
			{"max_parallel_requests", &budget.MaxParallelRequests},
			{"tpm_limit", &budget.TPMLimit},
			{"rpm_limit", &budget.RPMLimit},
		} {
			if err := updateInt64FromAPI(field.target, result, true, true, field.name); err != nil {
				resp.Diagnostics.AddError("Invalid API Response", err.Error())
				return
			}
		}
		if budgetDuration, ok := result["budget_duration"].(string); ok {
			budget.BudgetDuration = types.StringValue(budgetDuration)
		}
		if err := updateModelBudgetStringState(&budget.ModelMaxBudget, result, "model_max_budget", true); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
		if value, ok := result["budget_reset_at"].(string); ok {
			budget.BudgetResetAt = types.StringValue(value)
		}
		if value, ok := result["created_at"].(string); ok {
			budget.CreatedAt = types.StringValue(value)
		}
		if value, ok := result["updated_at"].(string); ok {
			budget.UpdatedAt = types.StringValue(value)
		}

		budgets = append(budgets, budget)
	}
	sort.SliceStable(budgets, func(i, j int) bool {
		return budgets[i].BudgetID.ValueString() < budgets[j].BudgetID.ValueString()
	})

	data.ID = types.StringValue("budgets")
	data.Budgets = budgets

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
