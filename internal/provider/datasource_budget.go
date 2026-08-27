package provider

import (
	"context"
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
	var config BudgetDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	budgetID := config.BudgetID.ValueString()
	if config.BudgetID.IsNull() || config.BudgetID.IsUnknown() || budgetID == "" {
		resp.Diagnostics.AddError("Invalid Budget Lookup", "budget_id must be known and nonempty")
		return
	}

	// /budget/info expects POST with a budgets array.
	infoReq := map[string]interface{}{"budgets": []string{budgetID}}

	var results []map[string]interface{}
	if err := d.client.DoRequestWithResponse(ctx, "POST", "/budget/info", infoReq, &results); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read budget: %s", err))
		return
	}
	if len(results) != 1 {
		if len(results) == 0 {
			resp.Diagnostics.AddError("Not Found", fmt.Sprintf("Budget not found: %s", budgetID))
		} else {
			resp.Diagnostics.AddError("Invalid API Response", "Budget lookup did not return exactly one object.")
		}
		return
	}
	result := results[0]
	actualBudgetID, err := dataSourceRequiredStringAt(result, "budget_id")
	if err != nil || actualBudgetID.ValueString() != budgetID {
		resp.Diagnostics.AddError("Invalid API Response", "Budget response identity did not match the requested budget.")
		return
	}

	data := BudgetDataSourceModel{ID: actualBudgetID, BudgetID: config.BudgetID}
	if data.MaxBudget, err = dataSourceNullableFloat64At(result, "max_budget"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.SoftBudget, err = dataSourceNullableFloat64At(result, "soft_budget"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.MaxParallelRequests, err = dataSourceNullableInt64At(result, "max_parallel_requests"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.TPMLimit, err = dataSourceNullableInt64At(result, "tpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.RPMLimit, err = dataSourceNullableInt64At(result, "rpm_limit"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.BudgetDuration, err = dataSourceNullableStringAt(result, "budget_duration"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.BudgetResetAt, err = dataSourceNullableStringAt(result, "budget_reset_at"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	if data.ModelMaxBudget, err = dataSourceNullableCanonicalJSONObjectAt(result, "model_max_budget"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
