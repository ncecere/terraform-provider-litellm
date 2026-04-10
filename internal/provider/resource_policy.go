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
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &PolicyResource{}
var _ resource.ResourceWithImportState = &PolicyResource{}

func NewPolicyResource() resource.Resource {
	return &PolicyResource{}
}

type PolicyResource struct {
	client *Client
}

type PolicyResourceModel struct {
	ID               types.String `tfsdk:"id"`
	PolicyName       types.String `tfsdk:"policy_name"`
	Inherit          types.String `tfsdk:"inherit"`
	Description      types.String `tfsdk:"description"`
	GuardrailsAdd    types.List   `tfsdk:"guardrails_add"`
	GuardrailsRemove types.List   `tfsdk:"guardrails_remove"`
	Condition        types.Object `tfsdk:"condition"`
	Pipeline         types.String `tfsdk:"pipeline"`

	VersionNumber   types.Int64  `tfsdk:"version_number"`
	VersionStatus   types.String `tfsdk:"version_status"`
	ParentVersionID types.String `tfsdk:"parent_version_id"`
	IsLatest        types.Bool   `tfsdk:"is_latest"`
	PublishedAt     types.String `tfsdk:"published_at"`
	ProductionAt    types.String `tfsdk:"production_at"`
	CreatedAt       types.String `tfsdk:"created_at"`
	UpdatedAt       types.String `tfsdk:"updated_at"`
	CreatedBy       types.String `tfsdk:"created_by"`
	UpdatedBy       types.String `tfsdk:"updated_by"`
}

func policyConditionAttrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"model": types.StringType,
	}
}

func policyConditionNullObject() types.Object {
	return types.ObjectNull(policyConditionAttrTypes())
}

func policyConditionObject(model string) (types.Object, error) {
	attrs := map[string]attr.Value{
		"model": types.StringNull(),
	}
	if model != "" {
		attrs["model"] = types.StringValue(model)
	}

	obj, diags := types.ObjectValue(policyConditionAttrTypes(), attrs)
	if diags.HasError() {
		return types.Object{}, fmt.Errorf("failed to build condition object")
	}
	return obj, nil
}

func (r *PolicyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy"
}

func (r *PolicyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM policy.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The policy ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"policy_name": schema.StringAttribute{
				Description: "Unique policy name.",
				Required:    true,
			},
			"inherit": schema.StringAttribute{
				Description: "Name of parent policy to inherit from.",
				Optional:    true,
			},
			"description": schema.StringAttribute{
				Description: "Human-readable policy description.",
				Optional:    true,
			},
			"guardrails_add": schema.ListAttribute{
				Description: "Guardrails to add.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"guardrails_remove": schema.ListAttribute{
				Description: "Guardrails to remove from inherited set.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"condition": schema.SingleNestedAttribute{
				Description: "Condition for when this policy applies.",
				Optional:    true,
				Attributes: map[string]schema.Attribute{
					"model": schema.StringAttribute{
						Description: "Model name pattern (exact match or regex).",
						Optional:    true,
					},
				},
			},
			"pipeline": schema.StringAttribute{
				Description: "JSON string defining optional guardrail pipeline.",
				Optional:    true,
			},

			"version_number": schema.Int64Attribute{
				Description: "Version number of this policy.",
				Computed:    true,
			},
			"version_status": schema.StringAttribute{
				Description: "Version status (draft, published, production).",
				Computed:    true,
			},
			"parent_version_id": schema.StringAttribute{
				Description: "Policy ID this version was cloned from.",
				Computed:    true,
			},
			"is_latest": schema.BoolAttribute{
				Description: "Whether this is the latest version.",
				Computed:    true,
			},
			"published_at": schema.StringAttribute{
				Description: "Timestamp when this version was published.",
				Computed:    true,
			},
			"production_at": schema.StringAttribute{
				Description: "Timestamp when this version was promoted to production.",
				Computed:    true,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the policy was created.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the policy was last updated.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_by": schema.StringAttribute{
				Description: "Who created the policy.",
				Computed:    true,
			},
			"updated_by": schema.StringAttribute{
				Description: "Who last updated the policy.",
				Computed:    true,
			},
		},
	}
}

func (r *PolicyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data PolicyResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	policyReq, err := r.buildPolicyRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid policy configuration", err.Error())
		return
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/policies", policyReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create policy: %s", err))
		return
	}

	policyID, err := requiredStringField(result, "policy_id")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create policy: %s", err))
		return
	}
	data.ID = types.StringValue(policyID)

	if err := r.readPolicy(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Policy created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data PolicyResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readPolicy(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read policy: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data PolicyResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state PolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = state.ID

	policyReq, err := r.buildPolicyRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid policy configuration", err.Error())
		return
	}

	endpoint := fmt.Sprintf("/policies/%s", data.ID.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "PUT", endpoint, policyReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update policy: %s", err))
		return
	}

	if err := r.readPolicy(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Policy updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data PolicyResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/policies/%s", data.ID.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete policy: %s", err))
			return
		}
	}
}

func (r *PolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *PolicyResource) buildPolicyRequest(ctx context.Context, data *PolicyResourceModel) (map[string]interface{}, error) {
	req := map[string]interface{}{
		"policy_name": data.PolicyName.ValueString(),
	}

	if !data.Inherit.IsNull() && !data.Inherit.IsUnknown() && data.Inherit.ValueString() != "" {
		req["inherit"] = data.Inherit.ValueString()
	}
	if !data.Description.IsNull() && !data.Description.IsUnknown() {
		req["description"] = data.Description.ValueString()
	}

	if !data.GuardrailsAdd.IsNull() && !data.GuardrailsAdd.IsUnknown() {
		var guardrailsAdd []string
		data.GuardrailsAdd.ElementsAs(ctx, &guardrailsAdd, false)
		req["guardrails_add"] = guardrailsAdd
	}
	if !data.GuardrailsRemove.IsNull() && !data.GuardrailsRemove.IsUnknown() {
		var guardrailsRemove []string
		data.GuardrailsRemove.ElementsAs(ctx, &guardrailsRemove, false)
		req["guardrails_remove"] = guardrailsRemove
	}

	if !data.Condition.IsNull() && !data.Condition.IsUnknown() {
		condition := map[string]interface{}{}
		if modelAttr, ok := data.Condition.Attributes()["model"]; ok {
			if modelValue, ok := modelAttr.(types.String); ok && !modelValue.IsNull() && !modelValue.IsUnknown() && modelValue.ValueString() != "" {
				condition["model"] = modelValue.ValueString()
			}
		}
		if len(condition) > 0 {
			req["condition"] = condition
		}
	}

	if !data.Pipeline.IsNull() && !data.Pipeline.IsUnknown() && data.Pipeline.ValueString() != "" {
		var pipeline map[string]interface{}
		if err := json.Unmarshal([]byte(data.Pipeline.ValueString()), &pipeline); err != nil {
			return nil, fmt.Errorf("pipeline must be valid JSON object: %w", err)
		}
		req["pipeline"] = pipeline
	}

	return req, nil
}

func (r *PolicyResource) readPolicy(ctx context.Context, data *PolicyResourceModel) error {
	policyID := data.ID.ValueString()
	if policyID == "" {
		return fmt.Errorf("policy ID is empty, cannot read policy")
	}

	endpoint := fmt.Sprintf("/policies/%s", policyID)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	if v, ok := result["policy_id"].(string); ok && v != "" {
		data.ID = types.StringValue(v)
	}
	if v, ok := result["policy_name"].(string); ok {
		data.PolicyName = types.StringValue(v)
	}
	if v, ok := result["inherit"].(string); ok && v != "" {
		data.Inherit = types.StringValue(v)
	} else if data.Inherit.IsUnknown() || !data.Inherit.IsNull() {
		data.Inherit = types.StringNull()
	}
	if v, ok := result["description"].(string); ok {
		data.Description = types.StringValue(v)
	} else if data.Description.IsUnknown() || !data.Description.IsNull() {
		data.Description = types.StringNull()
	}

	if v, ok := result["version_number"].(float64); ok {
		data.VersionNumber = types.Int64Value(int64(v))
	} else if data.VersionNumber.IsUnknown() {
		data.VersionNumber = types.Int64Null()
	}
	if v, ok := result["version_status"].(string); ok && v != "" {
		data.VersionStatus = types.StringValue(v)
	} else if data.VersionStatus.IsUnknown() {
		data.VersionStatus = types.StringNull()
	}
	if v, ok := result["parent_version_id"].(string); ok && v != "" {
		data.ParentVersionID = types.StringValue(v)
	} else if data.ParentVersionID.IsUnknown() {
		data.ParentVersionID = types.StringNull()
	}
	if v, ok := result["is_latest"].(bool); ok {
		data.IsLatest = types.BoolValue(v)
	} else if data.IsLatest.IsUnknown() {
		data.IsLatest = types.BoolNull()
	}
	if v, ok := result["published_at"].(string); ok && v != "" {
		data.PublishedAt = types.StringValue(v)
	} else if data.PublishedAt.IsUnknown() {
		data.PublishedAt = types.StringNull()
	}
	if v, ok := result["production_at"].(string); ok && v != "" {
		data.ProductionAt = types.StringValue(v)
	} else if data.ProductionAt.IsUnknown() {
		data.ProductionAt = types.StringNull()
	}
	if v, ok := result["created_at"].(string); ok && v != "" {
		data.CreatedAt = types.StringValue(v)
	} else if data.CreatedAt.IsUnknown() {
		data.CreatedAt = types.StringNull()
	}
	if v, ok := result["updated_at"].(string); ok && v != "" {
		data.UpdatedAt = types.StringValue(v)
	} else if data.UpdatedAt.IsUnknown() {
		data.UpdatedAt = types.StringNull()
	}
	if v, ok := result["created_by"].(string); ok && v != "" {
		data.CreatedBy = types.StringValue(v)
	} else if data.CreatedBy.IsUnknown() {
		data.CreatedBy = types.StringNull()
	}
	if v, ok := result["updated_by"].(string); ok && v != "" {
		data.UpdatedBy = types.StringValue(v)
	} else if data.UpdatedBy.IsUnknown() {
		data.UpdatedBy = types.StringNull()
	}

	if values, ok := result["guardrails_add"].([]interface{}); ok {
		if len(values) > 0 {
			list := make([]attr.Value, 0, len(values))
			for _, value := range values {
				if strValue, ok := value.(string); ok {
					list = append(list, types.StringValue(strValue))
				}
			}
			data.GuardrailsAdd, _ = types.ListValue(types.StringType, list)
		} else if !data.GuardrailsAdd.IsNull() {
			data.GuardrailsAdd, _ = types.ListValue(types.StringType, []attr.Value{})
		} else if data.GuardrailsAdd.IsUnknown() {
			data.GuardrailsAdd = types.ListNull(types.StringType)
		}
	} else if data.GuardrailsAdd.IsUnknown() {
		data.GuardrailsAdd = types.ListNull(types.StringType)
	}

	if values, ok := result["guardrails_remove"].([]interface{}); ok {
		if len(values) > 0 {
			list := make([]attr.Value, 0, len(values))
			for _, value := range values {
				if strValue, ok := value.(string); ok {
					list = append(list, types.StringValue(strValue))
				}
			}
			data.GuardrailsRemove, _ = types.ListValue(types.StringType, list)
		} else if !data.GuardrailsRemove.IsNull() {
			data.GuardrailsRemove, _ = types.ListValue(types.StringType, []attr.Value{})
		} else if data.GuardrailsRemove.IsUnknown() {
			data.GuardrailsRemove = types.ListNull(types.StringType)
		}
	} else if data.GuardrailsRemove.IsUnknown() {
		data.GuardrailsRemove = types.ListNull(types.StringType)
	}

	if condition, ok := result["condition"].(map[string]interface{}); ok && len(condition) > 0 {
		conditionModel := ""
		if v, ok := condition["model"].(string); ok {
			conditionModel = v
		}
		obj, err := policyConditionObject(conditionModel)
		if err != nil {
			return err
		}
		data.Condition = obj
	} else if data.Condition.IsUnknown() || !data.Condition.IsNull() {
		data.Condition = policyConditionNullObject()
	}

	if pipeline, ok := result["pipeline"]; ok && pipeline != nil {
		pipelineBytes, err := json.Marshal(pipeline)
		if err != nil {
			return fmt.Errorf("failed to marshal pipeline from response: %w", err)
		}
		data.Pipeline = types.StringValue(string(pipelineBytes))
	} else if data.Pipeline.IsUnknown() || !data.Pipeline.IsNull() {
		data.Pipeline = types.StringNull()
	}

	return nil
}
