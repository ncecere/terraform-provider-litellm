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

var _ resource.Resource = &CredentialResource{}
var _ resource.ResourceWithImportState = &CredentialResource{}

func NewCredentialResource() resource.Resource {
	return &CredentialResource{}
}

type CredentialResource struct {
	client *Client
}

type CredentialResourceModel struct {
	ID               types.String `tfsdk:"id"`
	CredentialName   types.String `tfsdk:"credential_name"`
	ModelID          types.String `tfsdk:"model_id"`
	CredentialInfo   types.Map    `tfsdk:"credential_info"`
	CredentialValues types.Map    `tfsdk:"credential_values"`
}

func (r *CredentialResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential"
}

func (r *CredentialResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM credential.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this credential (same as credential_name).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"credential_name": schema.StringAttribute{
				Description: "Name of the credential.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"model_id": schema.StringAttribute{
				Description: "Model ID associated with this credential.",
				Optional:    true,
			},
			"credential_info": schema.MapAttribute{
				Description: "Additional information about the credential.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"credential_values": schema.MapAttribute{
				Description: "Sensitive credential values (API keys, tokens, etc.).",
				Required:    true,
				Sensitive:   true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *CredentialResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *CredentialResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data CredentialResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	credReq := r.buildCredentialRequest(ctx, &data)

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/credentials", credReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create credential: %s", err))
		return
	}

	// Set the ID to credential name
	data.ID = data.CredentialName

	// Read back for full state (note: credential_values won't be returned for security)
	if err := r.readCredential(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Credential created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *CredentialResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data CredentialResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readCredential(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read credential: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *CredentialResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data CredentialResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state CredentialResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve the ID
	data.ID = state.ID

	credReq := r.buildCredentialRequest(ctx, &data)

	endpoint := fmt.Sprintf("/credentials/%s", data.CredentialName.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "PATCH", endpoint, credReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update credential: %s", err))
		return
	}

	// Read back for full state
	if err := r.readCredential(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Credential updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *CredentialResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data CredentialResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/credentials/%s", data.CredentialName.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete credential: %s", err))
			return
		}
	}
}

func (r *CredentialResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("credential_name"), req.ID)...)
}

func (r *CredentialResource) buildCredentialRequest(ctx context.Context, data *CredentialResourceModel) map[string]interface{} {
	credReq := map[string]interface{}{
		"credential_name": data.CredentialName.ValueString(),
	}

	if !data.ModelID.IsNull() && data.ModelID.ValueString() != "" {
		credReq["model_id"] = data.ModelID.ValueString()
	}

	if !data.CredentialInfo.IsNull() {
		var credInfo map[string]string
		data.CredentialInfo.ElementsAs(ctx, &credInfo, false)
		// Convert to map[string]interface{} for JSON
		credInfoInterface := make(map[string]interface{})
		for k, v := range credInfo {
			credInfoInterface[k] = v
		}
		credReq["credential_info"] = credInfoInterface
	}

	if !data.CredentialValues.IsNull() {
		var credValues map[string]string
		data.CredentialValues.ElementsAs(ctx, &credValues, false)
		// Convert to map[string]interface{} for JSON
		credValuesInterface := make(map[string]interface{})
		for k, v := range credValues {
			credValuesInterface[k] = v
		}
		credReq["credential_values"] = credValuesInterface
	}

	return credReq
}

func (r *CredentialResource) readCredential(ctx context.Context, data *CredentialResourceModel) error {
	credentialName := data.CredentialName.ValueString()
	if credentialName == "" {
		credentialName = data.ID.ValueString()
	}

	endpoint := fmt.Sprintf("/credentials/by_name/%s", credentialName)
	if !data.ModelID.IsNull() && data.ModelID.ValueString() != "" {
		endpoint += fmt.Sprintf("?model_id=%s", data.ModelID.ValueString())
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// Update fields from response
	if credName, ok := result["credential_name"].(string); ok {
		data.CredentialName = types.StringValue(credName)
		data.ID = types.StringValue(credName)
	}

	// Handle credential_info
	if credInfo, ok := result["credential_info"].(map[string]interface{}); ok {
		infoMap := make(map[string]attr.Value)
		for k, v := range credInfo {
			if str, ok := v.(string); ok {
				infoMap[k] = types.StringValue(str)
			}
		}
		data.CredentialInfo, _ = types.MapValue(types.StringType, infoMap)
	}

	// Note: We don't update credential_values from the response for security reasons
	// The API might not return sensitive values, and we want to preserve what's in state

	return nil
}
