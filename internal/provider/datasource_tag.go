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

var _ datasource.DataSource = &TagDataSource{}

func NewTagDataSource() datasource.DataSource {
	return &TagDataSource{}
}

type TagDataSource struct {
	client *Client
}

type TagDataSourceModel struct {
	ID                  types.String  `tfsdk:"id"`
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

func (d *TagDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tag"
}

func (d *TagDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Fetches information about a LiteLLM tag.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this tag.",
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "The tag name to look up.",
				Required:    true,
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
	}
}

func (d *TagDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TagDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data TagDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tagName := data.Name.ValueString()

	// /tag/info expects POST with names array
	infoReq := map[string]interface{}{
		"names": []string{tagName},
	}

	var rawResult interface{}
	if err := d.client.DoRequestWithResponse(ctx, "POST", "/tag/info", infoReq, &rawResult); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tag: %s", err))
		return
	}

	result := parseTagInfoResult(rawResult, tagName)
	if len(result) == 0 {
		resp.Diagnostics.AddError("Not Found", fmt.Sprintf("Tag not found: %s", tagName))
		return
	}

	// Populate the data model
	data.ID = types.StringValue(tagName)

	if description, ok := result["description"].(string); ok {
		data.Description = types.StringValue(description)
	}
	if budgetID, ok := result["budget_id"].(string); ok {
		data.BudgetID = types.StringValue(budgetID)
	}
	for _, field := range []struct {
		name   string
		target *types.Float64
	}{
		{"max_budget", &data.MaxBudget},
		{"soft_budget", &data.SoftBudget},
	} {
		if err := updateFloat64FromAPI(field.target, result, true, true, "litellm_budget_table", field.name); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
	}
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"max_parallel_requests", &data.MaxParallelRequests},
		{"tpm_limit", &data.TPMLimit},
		{"rpm_limit", &data.RPMLimit},
	} {
		if err := updateInt64FromAPI(field.target, result, true, true, "litellm_budget_table", field.name); err != nil {
			resp.Diagnostics.AddError("Invalid API Response", err.Error())
			return
		}
	}
	if budgetDuration, presence, err := apiValueAt(result, "litellm_budget_table", "budget_duration"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	} else if presence == apiValuePresent {
		value, ok := budgetDuration.(string)
		if !ok {
			resp.Diagnostics.AddError("Invalid API Response", "invalid response field \"litellm_budget_table.budget_duration\": expected a string")
			return
		}
		data.BudgetDuration = types.StringValue(value)
	} else {
		data.BudgetDuration = types.StringNull()
	}

	// Handle models list
	if models, ok := result["models"].([]interface{}); ok {
		modelsList := make([]attr.Value, len(models))
		for i, m := range models {
			if str, ok := m.(string); ok {
				modelsList[i] = types.StringValue(str)
			}
		}
		data.Models, _ = types.ListValue(types.StringType, modelsList)
	}

	if modelMaxBudget, presence, err := apiValueAt(result, "litellm_budget_table", "model_max_budget"); err != nil {
		resp.Diagnostics.AddError("Invalid API Response", err.Error())
		return
	} else if presence == apiValuePresent {
		jsonBytes, marshalErr := json.Marshal(modelMaxBudget)
		if marshalErr != nil {
			resp.Diagnostics.AddError("Invalid API Response", "invalid response field \"litellm_budget_table.model_max_budget\": cannot encode JSON")
			return
		}
		data.ModelMaxBudget = types.StringValue(string(jsonBytes))
	} else {
		data.ModelMaxBudget = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func parseTagInfoResult(rawResult interface{}, tagName string) map[string]interface{} {
	switch typed := rawResult.(type) {
	case []interface{}:
		if len(typed) > 0 {
			result, _ := typed[0].(map[string]interface{})
			return result
		}
	case map[string]interface{}:
		// LiteLLM v1.88 returns an object keyed by tag name:
		// {"tag-name": { ...tag fields... }}
		if tag, ok := typed[tagName].(map[string]interface{}); ok {
			return tag
		}
		// Some versions may return the tag fields directly.
		return typed
	}

	return nil
}
