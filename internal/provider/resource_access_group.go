package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
				Description: "Non-empty list of model names (model_name from litellm_model) to include in this access group. Membership order and duplicate entries are not significant.",
				Required:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					listvalidator.IsRequired(),
					listvalidator.SizeAtLeast(1),
					listvalidator.NoNullValues(),
				},
			},
		},
	}
}

func (r *AccessGroupResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := configuredClient(req.ProviderData)
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

	modelNames, err := accessGroupModelNamesForRequest(data.ModelNames)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Model Names", err.Error())
		return
	}

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

	modelNames, err := accessGroupModelNamesForRequest(data.ModelNames)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Model Names", err.Error())
		return
	}

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

func accessGroupModelNamesForRequest(modelNames types.List) ([]string, error) {
	if modelNames.IsNull() {
		return nil, fmt.Errorf("model_names must not be null")
	}
	if modelNames.IsUnknown() {
		return nil, fmt.Errorf("model_names must be known before it can be sent to LiteLLM")
	}
	if len(modelNames.Elements()) == 0 {
		return nil, fmt.Errorf("model_names must contain at least one model name")
	}

	names := make([]string, 0, len(modelNames.Elements()))
	for index, element := range modelNames.Elements() {
		name, ok := element.(types.String)
		if !ok {
			return nil, fmt.Errorf("model_names[%d] must be a string, got %T", index, element)
		}
		if name.IsNull() {
			return nil, fmt.Errorf("model_names[%d] must not be null", index)
		}
		if name.IsUnknown() {
			return nil, fmt.Errorf("model_names[%d] must be known before it can be sent to LiteLLM", index)
		}
		names = append(names, name.ValueString())
	}

	return canonicalAccessGroupModelNames(names)
}

func canonicalAccessGroupModelNames(raw interface{}) ([]string, error) {
	var names []string
	switch values := raw.(type) {
	case nil:
		names = []string{}
	case []string:
		names = append([]string(nil), values...)
	case []interface{}:
		names = make([]string, 0, len(values))
		for index, value := range values {
			name, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("model_names[%d] must be a string, got %T", index, value)
			}
			names = append(names, name)
		}
	default:
		return nil, fmt.Errorf("model_names must be a list of strings, got %T", raw)
	}

	sort.Strings(names)
	unique := names[:0]
	for _, name := range names {
		if len(unique) == 0 || name != unique[len(unique)-1] {
			unique = append(unique, name)
		}
	}
	return unique, nil
}

func reconcileAccessGroupModelNames(ctx context.Context, current types.List, raw interface{}) (types.List, error) {
	remote, err := canonicalAccessGroupModelNames(raw)
	if err != nil {
		return types.ListNull(types.StringType), err
	}
	if accessGroupModelMembershipEqual(current, remote) {
		return current, nil
	}

	result, diagnostics := types.ListValueFrom(ctx, types.StringType, remote)
	if diagnostics.HasError() {
		return types.ListNull(types.StringType), fmt.Errorf("failed to convert model_names: %v", diagnostics.Errors())
	}
	return result, nil
}

func accessGroupModelMembershipEqual(current types.List, canonicalRemote []string) bool {
	if current.IsNull() || current.IsUnknown() {
		return false
	}

	currentNames := make([]string, 0, len(current.Elements()))
	for _, element := range current.Elements() {
		name, ok := element.(types.String)
		if !ok || name.IsNull() || name.IsUnknown() {
			return false
		}
		currentNames = append(currentNames, name.ValueString())
	}
	sort.Strings(currentNames)
	uniqueCurrent := currentNames[:0]
	for _, name := range currentNames {
		if len(uniqueCurrent) == 0 || name != uniqueCurrent[len(uniqueCurrent)-1] {
			uniqueCurrent = append(uniqueCurrent, name)
		}
	}
	if len(uniqueCurrent) != len(canonicalRemote) {
		return false
	}
	for index := range uniqueCurrent {
		if uniqueCurrent[index] != canonicalRemote[index] {
			return false
		}
	}
	return true
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

	if rawModelNames, ok := result["model_names"]; ok {
		modelNames, err := reconcileAccessGroupModelNames(ctx, data.ModelNames, rawModelNames)
		if err != nil {
			return fmt.Errorf("invalid model_names response: %w", err)
		}
		data.ModelNames = modelNames
	}

	return nil
}
