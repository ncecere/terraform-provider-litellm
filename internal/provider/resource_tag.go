package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &TagResource{}
var _ resource.ResourceWithImportState = &TagResource{}

func NewTagResource() resource.Resource {
	return &TagResource{}
}

type TagResource struct {
	client *Client
}

type TagResourceModel struct {
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

func (r *TagResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tag"
}

func (r *TagResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM tag. Tags can be used for tracking spend and tag-based routing.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this tag (same as name).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "The unique name of the tag.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Description: "Description of the tag.",
				Optional:    true,
			},
			"models": schema.ListAttribute{
				Description: "Models associated with this tag.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"budget_id": schema.StringAttribute{
				Description: "Budget ID to associate with this tag.",
				Optional:    true,
			},
			"max_budget": schema.Float64Attribute{
				Description: "Max budget in USD for this tag.",
				Optional:    true,
			},
			"soft_budget": schema.Float64Attribute{
				Description: "Soft budget in USD for this tag.",
				Optional:    true,
			},
			"max_parallel_requests": schema.Int64Attribute{
				Description: "Max concurrent requests allowed for this tag.",
				Optional:    true,
			},
			"tpm_limit": schema.Int64Attribute{
				Description: "Max tokens per minute for this tag.",
				Optional:    true,
			},
			"rpm_limit": schema.Int64Attribute{
				Description: "Max requests per minute for this tag.",
				Optional:    true,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Duration for budget reset (e.g., '1hr', '1d', '28d').",
				Optional:    true,
			},
			"model_max_budget": schema.StringAttribute{
				Description: "JSON string for per-model budget configuration.",
				Optional:    true,
				Validators:  []validator.String{jsonShapeStringValidator{shape: '{'}},
			},
		},
	}
}

func (r *TagResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TagResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TagResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tagReq, err := r.buildTagRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Tag JSON", err.Error())
		return
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/tag/new", tagReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create tag: %s", err))
		return
	}

	// Set ID to name
	data.ID = data.Name

	// Read back for full state
	if err := r.readTag(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Tag created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TagResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TagResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	importedMarker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(importedMarker) == "true"

	if err := r.readTagWithNumericOwnership(ctx, &data, imported); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tag: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && imported {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
}

func (r *TagResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data TagResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state TagResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve IDs
	data.ID = state.ID
	data.Name = state.Name

	tagReq, err := r.buildTagRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Tag JSON", err.Error())
		return
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/tag/update", tagReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update tag: %s", err))
		return
	}

	// Read back for full state
	if err := r.readTag(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Tag updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TagResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TagResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := map[string]interface{}{
		"name": data.Name.ValueString(),
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/tag/delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete tag: %s", err))
			return
		}
	}
}

func (r *TagResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
	}
}

func (r *TagResource) buildTagRequest(ctx context.Context, data *TagResourceModel) (map[string]interface{}, error) {
	tagReq := map[string]interface{}{
		"name": data.Name.ValueString(),
	}

	// String fields - check IsNull, IsUnknown, and empty string
	if !data.Description.IsNull() && !data.Description.IsUnknown() && data.Description.ValueString() != "" {
		tagReq["description"] = data.Description.ValueString()
	}
	if !data.BudgetID.IsNull() && !data.BudgetID.IsUnknown() && data.BudgetID.ValueString() != "" {
		tagReq["budget_id"] = data.BudgetID.ValueString()
	}
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() && data.BudgetDuration.ValueString() != "" {
		tagReq["budget_duration"] = data.BudgetDuration.ValueString()
	}
	if !data.ModelMaxBudget.IsNull() && !data.ModelMaxBudget.IsUnknown() && data.ModelMaxBudget.ValueString() != "" {
		modelBudget, err := decodeRequestJSONObject(data.ModelMaxBudget.ValueString(), "model_max_budget")
		if err != nil {
			return nil, err
		}
		tagReq["model_max_budget"] = modelBudget
	}

	// Numeric fields - check IsNull and IsUnknown
	if !data.MaxBudget.IsNull() && !data.MaxBudget.IsUnknown() {
		tagReq["max_budget"] = data.MaxBudget.ValueFloat64()
	}
	if !data.SoftBudget.IsNull() && !data.SoftBudget.IsUnknown() {
		tagReq["soft_budget"] = data.SoftBudget.ValueFloat64()
	}
	if !data.MaxParallelRequests.IsNull() && !data.MaxParallelRequests.IsUnknown() {
		tagReq["max_parallel_requests"] = data.MaxParallelRequests.ValueInt64()
	}
	if !data.TPMLimit.IsNull() && !data.TPMLimit.IsUnknown() {
		tagReq["tpm_limit"] = data.TPMLimit.ValueInt64()
	}
	if !data.RPMLimit.IsNull() && !data.RPMLimit.IsUnknown() {
		tagReq["rpm_limit"] = data.RPMLimit.ValueInt64()
	}

	// List fields - check IsNull, IsUnknown, and len > 0
	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		var models []string
		data.Models.ElementsAs(ctx, &models, false)
		if len(models) > 0 {
			tagReq["models"] = models
		}
	}

	return tagReq, nil
}

func (r *TagResource) readTag(ctx context.Context, data *TagResourceModel) error {
	return r.readTagWithNumericOwnership(ctx, data, false)
}

func (r *TagResource) readTagWithNumericOwnership(ctx context.Context, data *TagResourceModel, imported bool) error {
	tagName := data.Name.ValueString()
	if tagName == "" {
		tagName = data.ID.ValueString()
	}

	// /tag/info expects POST with names array.
	// The API may return either:
	//   - a map keyed by tag name: {"tag-name": {...tag data...}}
	//   - an array of tag objects: [{...tag data...}]
	infoReq := map[string]interface{}{
		"names": []string{tagName},
	}

	// The API returns a map keyed by tag name: {"tag-name": {...tag data...}}
	var rawResult map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/tag/info", infoReq, &rawResult); err != nil {
		return err
	}

	// Extract tag data from the map-keyed response. Imported state requires the
	// authoritative keyed envelope; the flat shape remains read-compatible for
	// established state but cannot clear the one-time import marker.
	var result map[string]interface{}
	if tagData, ok := rawResult[tagName].(map[string]interface{}); ok {
		result = tagData
	} else {
		result = rawResult
	}
	if imported {
		tagData, ok := rawResult[tagName].(map[string]interface{})
		if !ok || tagData == nil {
			return fmt.Errorf("tag import read response is missing the authoritative keyed envelope")
		}
		result = tagData
	}

	if result == nil || len(result) == 0 {
		return fmt.Errorf("tag not found: %s", tagName)
	}
	if err := validateImportedObjectIdentity(imported, "tag", result, "name", tagName); err != nil {
		return err
	}

	// Update fields from response
	if name, ok := result["name"].(string); ok {
		data.Name = types.StringValue(name)
		data.ID = types.StringValue(name)
	}
	if description, ok := result["description"].(string); ok {
		data.Description = types.StringValue(description)
	}
	if budgetID, ok := result["budget_id"].(string); ok {
		data.BudgetID = types.StringValue(budgetID)
	}
	// LiteLLM v1.98 returns budget and rate fields through the nested budget
	// relation. Tag budget fields are Optional-only: validate defaults, adopt
	// only configured values, and expose explicit/omitted remote clears.
	for _, field := range []struct {
		name   string
		target *types.Float64
	}{
		{"max_budget", &data.MaxBudget},
		{"soft_budget", &data.SoftBudget},
	} {
		owned := imported || (!field.target.IsNull() && !field.target.IsUnknown())
		if err := updateFloat64FromAPI(field.target, result, owned, owned, "litellm_budget_table", field.name); err != nil {
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
		if err := updateInt64FromAPI(field.target, result, owned, owned, "litellm_budget_table", field.name); err != nil {
			return err
		}
	}
	if budgetDuration, presence, err := apiValueAt(result, "litellm_budget_table", "budget_duration"); err != nil {
		return err
	} else if presence == apiValuePresent && !data.BudgetDuration.IsNull() {
		value, ok := budgetDuration.(string)
		if !ok {
			return fmt.Errorf("invalid response field %q: expected a string", "litellm_budget_table.budget_duration")
		}
		data.BudgetDuration = types.StringValue(value)
	} else if presence != apiValuePresent && !data.BudgetDuration.IsNull() {
		data.BudgetDuration = types.StringNull()
	}

	// Handle models list - resolve Unknown to empty, preserve null
	if models, ok := result["models"].([]interface{}); ok && len(models) > 0 {
		modelsList := make([]attr.Value, len(models))
		for i, m := range models {
			if str, ok := m.(string); ok {
				modelsList[i] = types.StringValue(str)
			}
		}
		data.Models, _ = types.ListValue(types.StringType, modelsList)
	} else if data.Models.IsUnknown() {
		data.Models, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// model_max_budget is owned by its existing JSON-string schema. Read it
	// from the exact v1.98 budget relation without introducing a flattened
	// compatibility field.
	if modelMaxBudget, presence, err := apiValueAt(result, "litellm_budget_table", "model_max_budget"); err != nil {
		return err
	} else if presence == apiValuePresent && (imported || (!data.ModelMaxBudget.IsNull() && !data.ModelMaxBudget.IsUnknown())) {
		if jsonBytes, err := json.Marshal(modelMaxBudget); err == nil {
			data.ModelMaxBudget = types.StringValue(string(jsonBytes))
		} else {
			return fmt.Errorf("invalid response field %q: cannot encode JSON", "litellm_budget_table.model_max_budget")
		}
	} else if presence != apiValuePresent && (imported || (!data.ModelMaxBudget.IsNull() && !data.ModelMaxBudget.IsUnknown())) {
		data.ModelMaxBudget = types.StringNull()
	}

	return nil
}
