package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &AccessGroupResource{}
var _ resource.ResourceWithImportState = &AccessGroupResource{}

func NewAccessGroupResource() resource.Resource {
	return &AccessGroupResource{}
}

type AccessGroupResource struct {
	client *Client
}

type AccessGroupResourceModel struct {
	ID          types.String `tfsdk:"id"`
	AccessGroup types.String `tfsdk:"access_group"`
	ModelNames  types.List   `tfsdk:"model_names"`
}

func (r *AccessGroupResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_access_group"
}

func (r *AccessGroupResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM access group. Access groups allow you to group models together for access control on keys and teams.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this access group (same as access_group name).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"access_group": schema.StringAttribute{
				Description: "The name of the access group.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"model_names": schema.ListAttribute{
				Description: "List of model names (model_name from litellm_model) to include in this access group.",
				Required:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *AccessGroupResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *AccessGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data AccessGroupResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var modelNames []string
	data.ModelNames.ElementsAs(ctx, &modelNames, false)

	createReq := map[string]interface{}{
		"access_group": data.AccessGroup.ValueString(),
		"model_names":  modelNames,
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/access_group/new", createReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create access group: %s", err))
		return
	}

	data.ID = data.AccessGroup

	// Read back for full state
	if err := r.readAccessGroup(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Access group created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AccessGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data AccessGroupResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readAccessGroup(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read access group: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AccessGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data AccessGroupResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state AccessGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve IDs
	data.ID = state.ID
	data.AccessGroup = state.AccessGroup

	var modelNames []string
	data.ModelNames.ElementsAs(ctx, &modelNames, false)

	updateReq := map[string]interface{}{
		"model_names": modelNames,
	}

	endpoint := fmt.Sprintf("/access_group/%s/update", data.AccessGroup.ValueString())
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "PUT", endpoint, updateReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update access group: %s", err))
		return
	}

	// Read back for full state
	if err := r.readAccessGroup(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Access group updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AccessGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data AccessGroupResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/access_group/%s/delete", data.AccessGroup.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete access group: %s", err))
			return
		}
	}
}

func (r *AccessGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("access_group"), req.ID)...)
}

func (r *AccessGroupResource) readAccessGroup(ctx context.Context, data *AccessGroupResourceModel) error {
	accessGroup := data.AccessGroup.ValueString()
	if accessGroup == "" {
		accessGroup = data.ID.ValueString()
	}

	endpoint := fmt.Sprintf("/access_group/%s/info", accessGroup)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// Update fields from response
	if ag, ok := result["access_group"].(string); ok {
		data.AccessGroup = types.StringValue(ag)
		data.ID = types.StringValue(ag)
	}

	// Handle model_names list
	if modelNames, ok := result["model_names"].([]interface{}); ok {
		modelsList := make([]attr.Value, len(modelNames))
		for i, m := range modelNames {
			if str, ok := m.(string); ok {
				modelsList[i] = types.StringValue(str)
			}
		}
		data.ModelNames, _ = types.ListValue(types.StringType, modelsList)
	}

	return nil
}
