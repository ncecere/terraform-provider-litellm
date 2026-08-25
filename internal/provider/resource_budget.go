package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &BudgetResource{}
var _ resource.ResourceWithImportState = &BudgetResource{}

func NewBudgetResource() resource.Resource {
	return &BudgetResource{}
}

type BudgetResource struct {
	client *Client
}

type BudgetResourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	ModelMaxBudget      types.String  `tfsdk:"model_max_budget"`
}

func (r *BudgetResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_budget"
}

func (r *BudgetResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM budget. Budgets can be used to control spending limits for keys, teams, and organizations.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this budget (same as budget_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"budget_id": schema.StringAttribute{
				Description: "The unique budget ID. If not specified, one will be generated.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"max_budget": schema.Float64Attribute{
				Description: "Max budget in USD. Requests will fail if this budget is exceeded.",
				Optional:    true,
			},
			"soft_budget": schema.Float64Attribute{
				Description: "Soft budget in USD. Requests will NOT fail if exceeded, but will fire alerting.",
				Optional:    true,
			},
			"max_parallel_requests": schema.Int64Attribute{
				Description: "Max concurrent requests allowed for this budget.",
				Optional:    true,
			},
			"tpm_limit": schema.Int64Attribute{
				Description: "Max tokens per minute allowed for this budget.",
				Optional:    true,
			},
			"rpm_limit": schema.Int64Attribute{
				Description: "Max requests per minute allowed for this budget.",
				Optional:    true,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Duration for budget reset (e.g., '1hr', '1d', '28d', '1mo').",
				Optional:    true,
			},
			"model_max_budget": schema.StringAttribute{
				Description: "JSON string for per-model budget configuration (e.g., '{\"gpt-4o\": {\"max_budget\": 0.01, \"budget_duration\": \"1d\"}}').",
				Optional:    true,
				Validators:  []validator.String{jsonShapeStringValidator{shape: '{'}},
			},
		},
	}
}

func (r *BudgetResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *BudgetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data BudgetResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	budgetReq, err := r.buildBudgetRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Budget JSON", err.Error())
		return
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/budget/new", budgetReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create budget: %s", err))
		return
	}

	// Extract budget_id from response
	if budgetID, ok := result["budget_id"].(string); ok {
		data.BudgetID = types.StringValue(budgetID)
		data.ID = types.StringValue(budgetID)
	}

	// Read back for full state
	if err := r.readBudget(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Budget created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *BudgetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data BudgetResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	importedMarker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(importedMarker) == "true"

	if err := r.readBudgetWithNumericOwnership(ctx, &data, imported); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read budget: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && imported {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
}

func (r *BudgetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data BudgetResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state BudgetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve IDs
	data.ID = state.ID
	data.BudgetID = state.BudgetID

	budgetReq, err := r.buildBudgetRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Budget JSON", err.Error())
		return
	}
	budgetReq["budget_id"] = data.BudgetID.ValueString()

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/budget/update", budgetReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update budget: %s", err))
		return
	}

	// Read back for full state
	if err := r.readBudget(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Budget updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *BudgetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data BudgetResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := map[string]interface{}{
		"id": data.BudgetID.ValueString(),
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/budget/delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete budget: %s", err))
			return
		}
	}
}

func (r *BudgetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("budget_id"), req.ID)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
	}
}

func (r *BudgetResource) buildBudgetRequest(ctx context.Context, data *BudgetResourceModel) (map[string]interface{}, error) {
	budgetReq := map[string]interface{}{}

	// String fields - check IsNull, IsUnknown, and empty string
	if !data.BudgetID.IsNull() && !data.BudgetID.IsUnknown() && data.BudgetID.ValueString() != "" {
		budgetReq["budget_id"] = data.BudgetID.ValueString()
	}
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() && data.BudgetDuration.ValueString() != "" {
		budgetReq["budget_duration"] = data.BudgetDuration.ValueString()
	}
	if !data.ModelMaxBudget.IsNull() && !data.ModelMaxBudget.IsUnknown() && data.ModelMaxBudget.ValueString() != "" {
		modelBudget, err := decodeRequestJSONObject(data.ModelMaxBudget.ValueString(), "model_max_budget")
		if err != nil {
			return nil, err
		}
		budgetReq["model_max_budget"] = modelBudget
	}

	// Numeric fields - check IsNull and IsUnknown
	if !data.MaxBudget.IsNull() && !data.MaxBudget.IsUnknown() {
		budgetReq["max_budget"] = data.MaxBudget.ValueFloat64()
	}
	if !data.SoftBudget.IsNull() && !data.SoftBudget.IsUnknown() {
		budgetReq["soft_budget"] = data.SoftBudget.ValueFloat64()
	}
	if !data.MaxParallelRequests.IsNull() && !data.MaxParallelRequests.IsUnknown() {
		budgetReq["max_parallel_requests"] = data.MaxParallelRequests.ValueInt64()
	}
	if !data.TPMLimit.IsNull() && !data.TPMLimit.IsUnknown() {
		budgetReq["tpm_limit"] = data.TPMLimit.ValueInt64()
	}
	if !data.RPMLimit.IsNull() && !data.RPMLimit.IsUnknown() {
		budgetReq["rpm_limit"] = data.RPMLimit.ValueInt64()
	}

	return budgetReq, nil
}

func (r *BudgetResource) readBudget(ctx context.Context, data *BudgetResourceModel) error {
	return r.readBudgetWithNumericOwnership(ctx, data, false)
}

func (r *BudgetResource) readBudgetWithNumericOwnership(ctx context.Context, data *BudgetResourceModel, imported bool) error {
	budgetID := data.BudgetID.ValueString()
	if budgetID == "" {
		budgetID = data.ID.ValueString()
	}

	// /budget/info expects POST with budgets array
	infoReq := map[string]interface{}{
		"budgets": []string{budgetID},
	}

	var results []map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/budget/info", infoReq, &results); err != nil {
		return err
	}

	if len(results) == 0 {
		if imported {
			return fmt.Errorf("budget import read response did not contain exactly one budget")
		}
		return fmt.Errorf("budget not found: %s", budgetID)
	}
	if imported && len(results) != 1 {
		return fmt.Errorf("budget import read response did not contain exactly one budget")
	}

	result := results[0]
	if err := validateImportedObjectIdentity(imported, "budget", result, "budget_id", budgetID); err != nil {
		return err
	}

	// Update fields from response
	if id, ok := result["budget_id"].(string); ok {
		data.BudgetID = types.StringValue(id)
		data.ID = types.StringValue(id)
	}
	for _, field := range []struct {
		name   string
		target *types.Float64
	}{
		{"max_budget", &data.MaxBudget},
		{"soft_budget", &data.SoftBudget},
	} {
		owned := imported || (!field.target.IsNull() && !field.target.IsUnknown())
		if err := updateFloat64FromAPI(field.target, result, owned, owned, field.name); err != nil {
			return err
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
		owned := imported || (!field.target.IsNull() && !field.target.IsUnknown())
		if err := updateInt64FromAPI(field.target, result, owned, owned, field.name); err != nil {
			return err
		}
	}
	if budgetDuration, ok := result["budget_duration"].(string); ok {
		data.BudgetDuration = types.StringValue(budgetDuration)
	}
	modelBudgetOwned := imported || (!data.ModelMaxBudget.IsNull() && !data.ModelMaxBudget.IsUnknown())
	if modelMaxBudget, presence, err := apiValueAt(result, "model_max_budget"); err != nil {
		return err
	} else if presence == apiValuePresent {
		object, ok := modelMaxBudget.(map[string]interface{})
		if !ok {
			return fmt.Errorf("invalid response field %q: expected an object", "model_max_budget")
		}
		if modelBudgetOwned {
			jsonBytes, err := json.Marshal(object)
			if err != nil {
				return fmt.Errorf("invalid response field %q: cannot encode JSON", "model_max_budget")
			}
			data.ModelMaxBudget = types.StringValue(string(jsonBytes))
		} else if data.ModelMaxBudget.IsUnknown() {
			data.ModelMaxBudget = types.StringNull()
		}
	} else if modelBudgetOwned || data.ModelMaxBudget.IsUnknown() {
		data.ModelMaxBudget = types.StringNull()
	}

	return nil
}
