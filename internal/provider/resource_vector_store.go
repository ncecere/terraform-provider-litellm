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

var _ resource.Resource = &VectorStoreResource{}
var _ resource.ResourceWithImportState = &VectorStoreResource{}

func NewVectorStoreResource() resource.Resource {
	return &VectorStoreResource{}
}

type VectorStoreResource struct {
	client *Client
}

type VectorStoreResourceModel struct {
	ID                     types.String `tfsdk:"id"`
	VectorStoreID          types.String `tfsdk:"vector_store_id"`
	VectorStoreName        types.String `tfsdk:"vector_store_name"`
	CustomLLMProvider      types.String `tfsdk:"custom_llm_provider"`
	VectorStoreDescription types.String `tfsdk:"vector_store_description"`
	VectorStoreMetadata    types.Map    `tfsdk:"vector_store_metadata"`
	LiteLLMCredentialName  types.String `tfsdk:"litellm_credential_name"`
	LiteLLMParams          types.Map    `tfsdk:"litellm_params"`
	CreatedAt              types.String `tfsdk:"created_at"`
	UpdatedAt              types.String `tfsdk:"updated_at"`
}

func (r *VectorStoreResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vector_store"
}

func (r *VectorStoreResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM vector store.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this vector store (same as vector_store_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"vector_store_id": schema.StringAttribute{
				Description: "Unique identifier for the vector store.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"vector_store_name": schema.StringAttribute{
				Description: "Name of the vector store.",
				Required:    true,
			},
			"custom_llm_provider": schema.StringAttribute{
				Description: "Custom LLM provider for the vector store.",
				Required:    true,
			},
			"vector_store_description": schema.StringAttribute{
				Description: "Description of the vector store.",
				Optional:    true,
			},
			"vector_store_metadata": schema.MapAttribute{
				Description: "Metadata associated with the vector store.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"litellm_credential_name": schema.StringAttribute{
				Description: "Name of the LiteLLM credential to use.",
				Optional:    true,
			},
			"litellm_params": schema.MapAttribute{
				Description: "Additional LiteLLM parameters.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the vector store was created.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the vector store was last updated.",
				Computed:    true,
			},
		},
	}
}

func (r *VectorStoreResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *VectorStoreResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data VectorStoreResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vsReq := r.buildVectorStoreRequest(ctx, &data)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/vector_store/new", vsReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create vector store: %s", err))
		return
	}

	// Set temporary ID to the name (we'll get the real ID on read)
	data.ID = data.VectorStoreName

	// Read back for full state including the actual vector_store_id
	if err := r.readVectorStore(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Vector store created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *VectorStoreResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data VectorStoreResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readVectorStore(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read vector store: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *VectorStoreResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data VectorStoreResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state VectorStoreResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve the IDs
	data.ID = state.ID
	data.VectorStoreID = state.VectorStoreID

	vsReq := r.buildVectorStoreRequest(ctx, &data)
	vsReq["vector_store_id"] = data.VectorStoreID.ValueString()

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/vector_store/update", vsReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update vector store: %s", err))
		return
	}

	// Read back for full state
	if err := r.readVectorStore(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Vector store updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *VectorStoreResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data VectorStoreResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	vectorStoreID := data.VectorStoreID.ValueString()
	if vectorStoreID == "" {
		vectorStoreID = data.ID.ValueString()
	}

	deleteReq := map[string]interface{}{
		"vector_store_id": vectorStoreID,
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/vector_store/delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete vector store: %s", err))
			return
		}
	}
}

func (r *VectorStoreResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("vector_store_id"), req.ID)...)
}

func (r *VectorStoreResource) buildVectorStoreRequest(ctx context.Context, data *VectorStoreResourceModel) map[string]interface{} {
	vsReq := map[string]interface{}{
		"vector_store_name":   data.VectorStoreName.ValueString(),
		"custom_llm_provider": data.CustomLLMProvider.ValueString(),
	}

	// String fields - check IsNull, IsUnknown, and empty string
	if !data.VectorStoreDescription.IsNull() && !data.VectorStoreDescription.IsUnknown() && data.VectorStoreDescription.ValueString() != "" {
		vsReq["vector_store_description"] = data.VectorStoreDescription.ValueString()
	}

	if !data.LiteLLMCredentialName.IsNull() && !data.LiteLLMCredentialName.IsUnknown() && data.LiteLLMCredentialName.ValueString() != "" {
		vsReq["litellm_credential_name"] = data.LiteLLMCredentialName.ValueString()
	}

	// Map fields - check IsNull, IsUnknown, and len > 0
	if !data.VectorStoreMetadata.IsNull() && !data.VectorStoreMetadata.IsUnknown() {
		var metadata map[string]string
		data.VectorStoreMetadata.ElementsAs(ctx, &metadata, false)
		if len(metadata) > 0 {
			// Convert to map[string]interface{} for JSON
			metadataInterface := make(map[string]interface{})
			for k, v := range metadata {
				metadataInterface[k] = v
			}
			vsReq["vector_store_metadata"] = metadataInterface
		}
	}

	if !data.LiteLLMParams.IsNull() && !data.LiteLLMParams.IsUnknown() {
		var params map[string]string
		data.LiteLLMParams.ElementsAs(ctx, &params, false)
		if len(params) > 0 {
			// Convert to map[string]interface{} for JSON
			paramsInterface := make(map[string]interface{})
			for k, v := range params {
				paramsInterface[k] = v
			}
			vsReq["litellm_params"] = paramsInterface
		}
	}

	return vsReq
}

func (r *VectorStoreResource) readVectorStore(ctx context.Context, data *VectorStoreResourceModel) error {
	vectorStoreID := data.VectorStoreID.ValueString()
	if vectorStoreID == "" {
		vectorStoreID = data.ID.ValueString()
	}

	infoReq := map[string]interface{}{
		"vector_store_id": vectorStoreID,
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/vector_store/info", infoReq, &result); err != nil {
		return err
	}

	// Update fields from response
	if vsID, ok := result["vector_store_id"].(string); ok {
		data.VectorStoreID = types.StringValue(vsID)
		data.ID = types.StringValue(vsID)
	}
	if vsName, ok := result["vector_store_name"].(string); ok {
		data.VectorStoreName = types.StringValue(vsName)
	}
	if provider, ok := result["custom_llm_provider"].(string); ok {
		data.CustomLLMProvider = types.StringValue(provider)
	}
	if desc, ok := result["vector_store_description"].(string); ok {
		data.VectorStoreDescription = types.StringValue(desc)
	}
	if credName, ok := result["litellm_credential_name"].(string); ok {
		data.LiteLLMCredentialName = types.StringValue(credName)
	}
	if createdAt, ok := result["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(createdAt)
	}
	if updatedAt, ok := result["updated_at"].(string); ok {
		data.UpdatedAt = types.StringValue(updatedAt)
	}

	// Handle vector_store_metadata - preserve null when API returns empty and config didn't specify
	if metadata, ok := result["vector_store_metadata"].(map[string]interface{}); ok && len(metadata) > 0 {
		metaMap := make(map[string]attr.Value)
		for k, v := range metadata {
			if str, ok := v.(string); ok {
				metaMap[k] = types.StringValue(str)
			}
		}
		data.VectorStoreMetadata, _ = types.MapValue(types.StringType, metaMap)
	} else if !data.VectorStoreMetadata.IsNull() {
		data.VectorStoreMetadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle litellm_params - preserve null when API returns empty and config didn't specify
	if params, ok := result["litellm_params"].(map[string]interface{}); ok && len(params) > 0 {
		paramsMap := make(map[string]attr.Value)
		for k, v := range params {
			if str, ok := v.(string); ok {
				paramsMap[k] = types.StringValue(str)
			}
		}
		data.LiteLLMParams, _ = types.MapValue(types.StringType, paramsMap)
	} else if !data.LiteLLMParams.IsNull() {
		data.LiteLLMParams, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	return nil
}
