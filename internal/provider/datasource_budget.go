package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &BudgetDataSource{}

func NewBudgetDataSource() datasource.DataSource {
	return &BudgetDataSource{}
}

type BudgetDataSource struct {
	client *Client
}

type BudgetDataSourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	BudgetResetAt       types.String  `tfsdk:"budget_reset_at"`
	ModelMaxBudget      types.String  `tfsdk:"model_max_budget"`
}

func (d *BudgetDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_budget"
}

func (d *BudgetDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches information about a LiteLLM budget.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this budget.",
				Computed:    true,
			},
			"budget_id": schema.StringAttribute{
				Description: "The budget ID to look up.",
				Required:    true,
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
			"budget_reset_at": schema.StringAttribute{
				Description: "Datetime when the budget is reset.",
				Computed:    true,
			},
			"model_max_budget": schema.StringAttribute{
				Description: "JSON string for per-model budget configuration.",
				Computed:    true,
			},
		},
	}
}

func (d *BudgetDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *BudgetDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data BudgetDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	budgetID := data.BudgetID.ValueString()

	// /budget/info expects POST with budgets array
	infoReq := map[string]interface{}{
		"budgets": []string{budgetID},
	}

	var results []map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "POST", "/budget/info", infoReq, &results); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read budget: %s", err))
		return
	}

	if len(results) == 0 {
		resp.Diagnostics.AddError("Not Found", fmt.Sprintf("Budget not found: %s", budgetID))
		return
	}

	result := results[0]

	// Populate the data model
	data.ID = types.StringValue(budgetID)

	if maxBudget, ok := result["max_budget"].(float64); ok {
		data.MaxBudget = types.Float64Value(maxBudget)
	}
	if softBudget, ok := result["soft_budget"].(float64); ok {
		data.SoftBudget = types.Float64Value(softBudget)
	}
	if maxParallel, ok := result["max_parallel_requests"].(float64); ok {
		data.MaxParallelRequests = types.Int64Value(int64(maxParallel))
	}
	if tpmLimit, ok := result["tpm_limit"].(float64); ok {
		data.TPMLimit = types.Int64Value(int64(tpmLimit))
	}
	if rpmLimit, ok := result["rpm_limit"].(float64); ok {
		data.RPMLimit = types.Int64Value(int64(rpmLimit))
	}
	if budgetDuration, ok := result["budget_duration"].(string); ok {
		data.BudgetDuration = types.StringValue(budgetDuration)
	}
	if budgetResetAt, ok := result["budget_reset_at"].(string); ok {
		data.BudgetResetAt = types.StringValue(budgetResetAt)
	}
	if modelMaxBudget, ok := result["model_max_budget"].(map[string]interface{}); ok && len(modelMaxBudget) > 0 {
		if jsonBytes, err := json.Marshal(modelMaxBudget); err == nil {
			data.ModelMaxBudget = types.StringValue(string(jsonBytes))
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
