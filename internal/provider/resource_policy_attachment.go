package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &PolicyAttachmentResource{}
var _ resource.ResourceWithImportState = &PolicyAttachmentResource{}

func NewPolicyAttachmentResource() resource.Resource {
	return &PolicyAttachmentResource{}
}

type PolicyAttachmentResource struct {
	client *Client
}

type PolicyAttachmentResourceModel struct {
	ID         types.String `tfsdk:"id"`
	PolicyName types.String `tfsdk:"policy_name"`
	Scope      types.String `tfsdk:"scope"`
	Teams      types.List   `tfsdk:"teams"`
	Keys       types.List   `tfsdk:"keys"`
	Models     types.List   `tfsdk:"models"`
	Tags       types.List   `tfsdk:"tags"`
	CreatedAt  types.String `tfsdk:"created_at"`
	UpdatedAt  types.String `tfsdk:"updated_at"`
	CreatedBy  types.String `tfsdk:"created_by"`
	UpdatedBy  types.String `tfsdk:"updated_by"`
}

func (r *PolicyAttachmentResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy_attachment"
}

func (r *PolicyAttachmentResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM policy attachment.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The attachment ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"policy_name": schema.StringAttribute{
				Description: "Name of the policy to attach.",
				Required:    true,
			},
			"scope": schema.StringAttribute{
				Description: "Use '*' for global scope.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("*"),
				},
			},
			"teams": schema.ListAttribute{
				Description: "Team aliases or patterns this attachment applies to.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"keys": schema.ListAttribute{
				Description: "Key aliases or patterns this attachment applies to.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"models": schema.ListAttribute{
				Description: "Model names or patterns this attachment applies to.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"tags": schema.ListAttribute{
				Description: "Tag patterns this attachment applies to.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"created_at": schema.StringAttribute{
				Description: "When the attachment was created.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"updated_at": schema.StringAttribute{
				Description: "When the attachment was last updated.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"created_by": schema.StringAttribute{
				Description: "Who created the attachment.",
				Computed:    true,
			},
			"updated_by": schema.StringAttribute{
				Description: "Who last updated the attachment.",
				Computed:    true,
			},
		},
	}
}

func (r *PolicyAttachmentResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PolicyAttachmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data PolicyAttachmentResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validatePolicyAttachmentTargeting(ctx, data.Scope, data.Teams, data.Keys, data.Models, data.Tags); err != nil {
		resp.Diagnostics.AddError("Invalid attachment targeting", err.Error())
		return
	}

	attachmentReq := buildPolicyAttachmentRequest(ctx, data.PolicyName, data.Scope, data.Teams, data.Keys, data.Models, data.Tags)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/policies/attachments", attachmentReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create policy attachment: %s", err))
		return
	}

	attachmentID, err := requiredStringField(result, "attachment_id")
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create policy attachment: %s", err))
		return
	}
	data.ID = types.StringValue(attachmentID)

	if err := r.readPolicyAttachment(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Attachment created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PolicyAttachmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data PolicyAttachmentResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readPolicyAttachment(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read policy attachment: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PolicyAttachmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data PolicyAttachmentResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state PolicyAttachmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := validatePolicyAttachmentTargeting(ctx, data.Scope, data.Teams, data.Keys, data.Models, data.Tags); err != nil {
		resp.Diagnostics.AddError("Invalid attachment targeting", err.Error())
		return
	}

	data.ID = state.ID

	deleteEndpoint := fmt.Sprintf("/policies/attachments/%s", data.ID.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", deleteEndpoint, nil, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update policy attachment: %s", err))
			return
		}
	}

	attachmentReq := buildPolicyAttachmentRequest(ctx, data.PolicyName, data.Scope, data.Teams, data.Keys, data.Models, data.Tags)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/policies/attachments", attachmentReq, &result); err != nil {
		resp.State.RemoveResource(ctx)
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update policy attachment: %s", err))
		return
	}

	attachmentID, err := requiredStringField(result, "attachment_id")
	if err != nil {
		resp.State.RemoveResource(ctx)
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update policy attachment: %s", err))
		return
	}
	data.ID = types.StringValue(attachmentID)

	if err := r.readPolicyAttachment(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Attachment updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PolicyAttachmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data PolicyAttachmentResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/policies/attachments/%s", data.ID.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete policy attachment: %s", err))
			return
		}
	}
}

func (r *PolicyAttachmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func buildPolicyAttachmentRequest(ctx context.Context, policyName, scope types.String, teams, keys, models, tags types.List) map[string]interface{} {
	req := map[string]interface{}{
		"policy_name": policyName.ValueString(),
	}

	if !scope.IsNull() && !scope.IsUnknown() && scope.ValueString() != "" {
		req["scope"] = scope.ValueString()
	}

	if teamsList := listStringValues(ctx, teams); len(teamsList) > 0 {
		req["teams"] = teamsList
	}
	if keysList := listStringValues(ctx, keys); len(keysList) > 0 {
		req["keys"] = keysList
	}
	if modelsList := listStringValues(ctx, models); len(modelsList) > 0 {
		req["models"] = modelsList
	}
	if tagsList := listStringValues(ctx, tags); len(tagsList) > 0 {
		req["tags"] = tagsList
	}

	return req
}

func listStringValues(ctx context.Context, list types.List) []string {
	if list.IsNull() || list.IsUnknown() {
		return nil
	}

	vals := make([]string, 0)
	list.ElementsAs(ctx, &vals, false)
	return vals
}

func validatePolicyAttachmentTargeting(ctx context.Context, scope types.String, teams, keys, models, tags types.List) error {
	hasScope := !scope.IsNull() && !scope.IsUnknown() && scope.ValueString() != ""
	hasTargets := len(listStringValues(ctx, teams)) > 0 ||
		len(listStringValues(ctx, keys)) > 0 ||
		len(listStringValues(ctx, models)) > 0 ||
		len(listStringValues(ctx, tags)) > 0

	if hasScope && scope.ValueString() != "*" {
		return fmt.Errorf("scope must be '*' when provided")
	}

	if hasScope && hasTargets {
		return fmt.Errorf("set either scope='*' OR one or more of teams/keys/models/tags, not both")
	}

	if !hasScope && !hasTargets {
		return fmt.Errorf("set either scope='*' OR at least one of teams/keys/models/tags")
	}

	return nil
}

func (r *PolicyAttachmentResource) readPolicyAttachment(ctx context.Context, data *PolicyAttachmentResourceModel) error {
	attachmentID := data.ID.ValueString()
	if attachmentID == "" {
		return fmt.Errorf("attachment ID is empty, cannot read policy attachment")
	}

	endpoint := fmt.Sprintf("/policies/attachments/%s", attachmentID)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	if v, ok := result["attachment_id"].(string); ok && v != "" {
		data.ID = types.StringValue(v)
	}
	if v, ok := result["policy_name"].(string); ok {
		data.PolicyName = types.StringValue(v)
	}
	if v, ok := result["scope"].(string); ok && v != "" {
		data.Scope = types.StringValue(v)
	} else if data.Scope.IsUnknown() || !data.Scope.IsNull() {
		data.Scope = types.StringNull()
	}

	if values, ok := result["teams"].([]interface{}); ok {
		if len(values) > 0 || !data.Teams.IsNull() {
			list := make([]attr.Value, 0, len(values))
			for _, value := range values {
				if strValue, ok := value.(string); ok {
					list = append(list, types.StringValue(strValue))
				}
			}
			data.Teams, _ = types.ListValue(types.StringType, list)
		} else if data.Teams.IsUnknown() {
			data.Teams = types.ListNull(types.StringType)
		}
	}

	if values, ok := result["keys"].([]interface{}); ok {
		if len(values) > 0 || !data.Keys.IsNull() {
			list := make([]attr.Value, 0, len(values))
			for _, value := range values {
				if strValue, ok := value.(string); ok {
					list = append(list, types.StringValue(strValue))
				}
			}
			data.Keys, _ = types.ListValue(types.StringType, list)
		} else if data.Keys.IsUnknown() {
			data.Keys = types.ListNull(types.StringType)
		}
	}

	if values, ok := result["models"].([]interface{}); ok {
		if len(values) > 0 || !data.Models.IsNull() {
			list := make([]attr.Value, 0, len(values))
			for _, value := range values {
				if strValue, ok := value.(string); ok {
					list = append(list, types.StringValue(strValue))
				}
			}
			data.Models, _ = types.ListValue(types.StringType, list)
		} else if data.Models.IsUnknown() {
			data.Models = types.ListNull(types.StringType)
		}
	}

	if values, ok := result["tags"].([]interface{}); ok {
		if len(values) > 0 || !data.Tags.IsNull() {
			list := make([]attr.Value, 0, len(values))
			for _, value := range values {
				if strValue, ok := value.(string); ok {
					list = append(list, types.StringValue(strValue))
				}
			}
			data.Tags, _ = types.ListValue(types.StringType, list)
		} else if data.Tags.IsUnknown() {
			data.Tags = types.ListNull(types.StringType)
		}
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

	return nil
}
