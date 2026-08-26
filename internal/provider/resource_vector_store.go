package provider

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
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
var _ resource.ResourceWithModifyPlan = &VectorStoreResource{}

const vectorStoreImportedPrivateKey = "vector_store_imported_v1"

func NewVectorStoreResource() resource.Resource {
	return &VectorStoreResource{}
}

type VectorStoreResource struct {
	client *Client
}

type VectorStoreResourceModel struct {
	ID                               types.String `tfsdk:"id"`
	VectorStoreID                    types.String `tfsdk:"vector_store_id"`
	VectorStoreName                  types.String `tfsdk:"vector_store_name"`
	CustomLLMProvider                types.String `tfsdk:"custom_llm_provider"`
	VectorStoreDescription           types.String `tfsdk:"vector_store_description"`
	VectorStoreMetadata              types.Map    `tfsdk:"vector_store_metadata"`
	LiteLLMCredentialName            types.String `tfsdk:"litellm_credential_name"`
	LiteLLMParams                    types.Map    `tfsdk:"litellm_params"`
	CreatedAt                        types.String `tfsdk:"created_at"`
	VectorStoreDescriptionConfigured types.Bool   `tfsdk:"vector_store_description_configured"`
	VectorStoreMetadataConfigured    types.Bool   `tfsdk:"vector_store_metadata_configured"`
	LiteLLMCredentialNameConfigured  types.Bool   `tfsdk:"litellm_credential_name_configured"`
	LiteLLMParamsConfigured          types.Bool   `tfsdk:"litellm_params_configured"`
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
				Computed:    true,
			},
			"vector_store_metadata": schema.MapAttribute{
				Description: "Metadata associated with the vector store.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"litellm_credential_name": schema.StringAttribute{
				Description: "Name of the LiteLLM credential to use. Changes require replacement because LiteLLM v1.98 does not accept this field on update.",
				Optional:    true,
				Computed:    true,
			},
			"litellm_params": schema.MapAttribute{
				Description: "Additional LiteLLM parameters. Changes require replacement because LiteLLM v1.98 does not accept this field on update.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				ElementType: types.StringType,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the vector store was created.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"vector_store_description_configured": schema.BoolAttribute{
				Description: "Internal ownership marker for vector_store_description.",
				Computed:    true,
			},
			"vector_store_metadata_configured": schema.BoolAttribute{
				Description: "Internal ownership marker for vector_store_metadata.",
				Computed:    true,
			},
			"litellm_credential_name_configured": schema.BoolAttribute{
				Description: "Internal ownership marker for litellm_credential_name.",
				Computed:    true,
			},
			"litellm_params_configured": schema.BoolAttribute{
				Description: "Internal ownership marker for litellm_params.",
				Computed:    true,
			},
		},
	}
}

func (r *VectorStoreResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func vectorStoreFieldConfigured(value types.Bool, fallback bool) bool {
	if value.IsNull() || value.IsUnknown() {
		return fallback
	}
	return value.ValueBool()
}

func (r *VectorStoreResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, state, config VectorStoreResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	descriptionConfigured := vectorStoreFieldConfigured(state.VectorStoreDescriptionConfigured, !state.VectorStoreDescription.IsNull() && !state.VectorStoreDescription.IsUnknown())
	metadataConfigured := vectorStoreFieldConfigured(state.VectorStoreMetadataConfigured, !state.VectorStoreMetadata.IsNull() && !state.VectorStoreMetadata.IsUnknown())
	credentialConfigured := vectorStoreFieldConfigured(state.LiteLLMCredentialNameConfigured, !state.LiteLLMCredentialName.IsNull() && !state.LiteLLMCredentialName.IsUnknown())
	paramsConfigured := vectorStoreFieldConfigured(state.LiteLLMParamsConfigured, !state.LiteLLMParams.IsNull() && !state.LiteLLMParams.IsUnknown())

	if config.VectorStoreDescription.IsNull() {
		if descriptionConfigured {
			plan.VectorStoreDescription = types.StringNull()
		} else {
			plan.VectorStoreDescription = state.VectorStoreDescription
		}
		plan.VectorStoreDescriptionConfigured = types.BoolValue(false)
	} else if !config.VectorStoreDescription.IsUnknown() {
		plan.VectorStoreDescriptionConfigured = types.BoolValue(true)
	}

	if config.VectorStoreMetadata.IsNull() {
		if metadataConfigured {
			plan.VectorStoreMetadata = types.MapNull(types.StringType)
		} else {
			plan.VectorStoreMetadata = state.VectorStoreMetadata
		}
	} else if !config.VectorStoreMetadata.IsUnknown() {
		plan.VectorStoreMetadataConfigured = types.BoolValue(true)
	}
	if config.VectorStoreMetadata.IsNull() {
		plan.VectorStoreMetadataConfigured = types.BoolValue(false)
	}

	if config.LiteLLMCredentialName.IsNull() {
		if credentialConfigured && !state.LiteLLMCredentialName.IsNull() && !state.LiteLLMCredentialName.IsUnknown() {
			plan.LiteLLMCredentialName = types.StringUnknown()
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("litellm_credential_name"))
		} else {
			plan.LiteLLMCredentialName = state.LiteLLMCredentialName
		}
		plan.LiteLLMCredentialNameConfigured = types.BoolValue(false)
	} else if !config.LiteLLMCredentialName.IsUnknown() {
		plan.LiteLLMCredentialNameConfigured = types.BoolValue(true)
		if !config.LiteLLMCredentialName.Equal(state.LiteLLMCredentialName) {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("litellm_credential_name"))
		}
	}

	if config.LiteLLMParams.IsNull() {
		if paramsConfigured && !state.LiteLLMParams.IsNull() && !state.LiteLLMParams.IsUnknown() {
			plan.LiteLLMParams = types.MapUnknown(types.StringType)
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("litellm_params"))
		} else {
			plan.LiteLLMParams = state.LiteLLMParams
		}
		plan.LiteLLMParamsConfigured = types.BoolValue(false)
	} else if !config.LiteLLMParams.IsUnknown() {
		plan.LiteLLMParamsConfigured = types.BoolValue(true)
		if !config.LiteLLMParams.Equal(state.LiteLLMParams) {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("litellm_params"))
		}
	}

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *VectorStoreResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data, config VectorStoreResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.VectorStoreDescriptionConfigured = types.BoolValue(!config.VectorStoreDescription.IsNull() && !config.VectorStoreDescription.IsUnknown())
	data.VectorStoreMetadataConfigured = types.BoolValue(!config.VectorStoreMetadata.IsNull() && !config.VectorStoreMetadata.IsUnknown())
	data.LiteLLMCredentialNameConfigured = types.BoolValue(!config.LiteLLMCredentialName.IsNull() && !config.LiteLLMCredentialName.IsUnknown())
	data.LiteLLMParamsConfigured = types.BoolValue(!config.LiteLLMParams.IsNull() && !config.LiteLLMParams.IsUnknown())

	// Generate a UUID for vector_store_id if not already set
	vsID := uuid.New().String()
	data.VectorStoreID = types.StringValue(vsID)
	data.ID = types.StringValue(vsID)

	vsReq := r.buildVectorStoreRequest(ctx, &data)
	vsReq["vector_store_id"] = vsID

	var result map[string]interface{}
	mutationErr := r.client.DoRequestWithResponse(ctx, "POST", "/vector_store/new", vsReq, &result)
	if err := r.readVectorStoreAfterCreate(ctx, &data, data, 16); err != nil {
		if mutationErr != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create or recover vector store after LiteLLM returned an error: %s", mutationErr))
		} else {
			resp.Diagnostics.AddError("Vector Store Create Not Confirmed", fmt.Sprintf("LiteLLM accepted the create but stable reads did not confirm the resource: %s", err))
		}
		return
	}
	if mutationErr != nil {
		resp.Diagnostics.AddWarning("Vector Store Create Recovered", "LiteLLM returned an error after creation, but stable identity and configuration reads confirmed the created vector store.")
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *VectorStoreResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data VectorStoreResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	importedMarker, privateDiags := req.Private.GetKey(ctx, vectorStoreImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(importedMarker) == "true"
	if err := r.readVectorStore(ctx, &data, imported, false); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read vector store: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if imported && !resp.Diagnostics.HasError() && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, vectorStoreImportedPrivateKey, nil)...)
	}
}

func (r *VectorStoreResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, config VectorStoreResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.VectorStoreDescriptionConfigured = types.BoolValue(!config.VectorStoreDescription.IsNull() && !config.VectorStoreDescription.IsUnknown())
	data.VectorStoreMetadataConfigured = types.BoolValue(!config.VectorStoreMetadata.IsNull() && !config.VectorStoreMetadata.IsUnknown())
	data.LiteLLMCredentialNameConfigured = types.BoolValue(!config.LiteLLMCredentialName.IsNull() && !config.LiteLLMCredentialName.IsUnknown())
	data.LiteLLMParamsConfigured = types.BoolValue(!config.LiteLLMParams.IsNull() && !config.LiteLLMParams.IsUnknown())

	var state VectorStoreResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve the IDs
	data.ID = state.ID
	data.VectorStoreID = state.VectorStoreID

	planned := data
	vsReq := r.buildVectorStoreUpdateRequest(ctx, &data, &state)
	mutationErr := r.client.DoRequestWithResponse(ctx, "POST", "/vector_store/update", vsReq, nil)

	// The v1.98 endpoint can persist a mutation and then return an error while
	// synchronizing its process-local registry. Recover only after stable reads
	// prove the complete planned update.
	if err := r.readVectorStoreAfterUpdate(ctx, &data, planned, state, 24); err != nil {
		if mutationErr != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to confirm vector store update after LiteLLM returned an error: %s", mutationErr))
		} else {
			resp.Diagnostics.AddError("Vector Store Update Not Confirmed", fmt.Sprintf("LiteLLM accepted the update but stable fresh-worker reads did not return the planned values; Terraform retained prior state: %s", err))
		}
		return
	}
	if mutationErr != nil {
		resp.Diagnostics.AddWarning("Vector Store Update Recovered", "LiteLLM returned an error after the vector store mutation, but stable authoritative reads confirmed the complete planned state.")
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
		if IsNotFoundError(err) {
			return
		}
		probe := data
		probeErr := r.readVectorStore(ctx, &probe, false, true)
		if IsNotFoundError(probeErr) {
			resp.Diagnostics.AddWarning("Vector Store Delete Recovered", "LiteLLM returned an error after deletion, but an authoritative fresh read confirmed the vector store is absent.")
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete vector store: %s", err))
		return
	}
}

func (r *VectorStoreResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("vector_store_id"), req.ID)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, vectorStoreImportedPrivateKey, []byte("true"))...)
	}
}

func (r *VectorStoreResource) buildVectorStoreRequest(ctx context.Context, data *VectorStoreResourceModel) map[string]interface{} {
	vsReq := map[string]interface{}{
		"vector_store_name":   data.VectorStoreName.ValueString(),
		"custom_llm_provider": data.CustomLLMProvider.ValueString(),
	}

	// String fields - check IsNull, IsUnknown, and empty string
	if !data.VectorStoreDescription.IsNull() && !data.VectorStoreDescription.IsUnknown() {
		vsReq["vector_store_description"] = data.VectorStoreDescription.ValueString()
	}

	if !data.LiteLLMCredentialName.IsNull() && !data.LiteLLMCredentialName.IsUnknown() {
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
		} else {
			vsReq["litellm_params"] = map[string]interface{}{}
		}
	} else {
		// API requires litellm_params even if empty
		vsReq["litellm_params"] = map[string]interface{}{}
	}

	return vsReq
}

func (r *VectorStoreResource) buildVectorStoreUpdateRequest(ctx context.Context, data, prior *VectorStoreResourceModel) map[string]interface{} {
	request := map[string]interface{}{
		"vector_store_id":     data.VectorStoreID.ValueString(),
		"vector_store_name":   data.VectorStoreName.ValueString(),
		"custom_llm_provider": data.CustomLLMProvider.ValueString(),
	}
	if data.VectorStoreDescription.IsNull() {
		if !prior.VectorStoreDescription.IsNull() && !prior.VectorStoreDescription.IsUnknown() {
			request["vector_store_description"] = ""
		}
	} else if !data.VectorStoreDescription.IsUnknown() {
		request["vector_store_description"] = data.VectorStoreDescription.ValueString()
	}
	if data.VectorStoreMetadata.IsNull() {
		if !prior.VectorStoreMetadata.IsNull() && !prior.VectorStoreMetadata.IsUnknown() {
			request["vector_store_metadata"] = map[string]string{}
		}
	} else if !data.VectorStoreMetadata.IsUnknown() {
		metadata := map[string]string{}
		data.VectorStoreMetadata.ElementsAs(ctx, &metadata, false)
		request["vector_store_metadata"] = metadata
	}
	return request
}

func vectorStoreCreateMatches(planned, observed VectorStoreResourceModel) bool {
	for _, pair := range [][2]attr.Value{
		{planned.VectorStoreID, observed.VectorStoreID},
		{planned.VectorStoreName, observed.VectorStoreName},
		{planned.CustomLLMProvider, observed.CustomLLMProvider},
		{planned.VectorStoreDescription, observed.VectorStoreDescription},
		{planned.VectorStoreMetadata, observed.VectorStoreMetadata},
		{planned.LiteLLMCredentialName, observed.LiteLLMCredentialName},
		{planned.LiteLLMParams, observed.LiteLLMParams},
	} {
		if !pair[0].IsUnknown() && !pair[0].Equal(pair[1]) {
			return false
		}
	}
	return true
}

func (r *VectorStoreResource) readVectorStoreAfterCreate(ctx context.Context, data *VectorStoreResourceModel, planned VectorStoreResourceModel, attempts int) error {
	stable := 0
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		candidate := planned
		if err := r.readVectorStore(ctx, &candidate, false, true); err != nil {
			lastErr = err
			stable = 0
		} else if !vectorStoreCreateMatches(planned, candidate) {
			lastErr = fmt.Errorf("created vector store did not match its planned configuration")
			stable = 0
		} else {
			stable++
			if stable >= 2 {
				*data = candidate
				return nil
			}
		}
		if attempt+1 < attempts {
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("created vector store was not observed")
	}
	return lastErr
}

func vectorStoreChangedFieldsNotConverged(planned, prior, observed VectorStoreResourceModel) []string {
	plannedValue, priorValue, observedValue := reflect.ValueOf(planned), reflect.ValueOf(prior), reflect.ValueOf(observed)
	modelType := plannedValue.Type()
	var stale []string
	for i := 0; i < plannedValue.NumField(); i++ {
		plannedAttr, plannedOK := plannedValue.Field(i).Interface().(attr.Value)
		priorAttr, priorOK := priorValue.Field(i).Interface().(attr.Value)
		observedAttr, observedOK := observedValue.Field(i).Interface().(attr.Value)
		if !plannedOK || !priorOK || !observedOK || plannedAttr.IsUnknown() || plannedAttr.Equal(priorAttr) {
			continue
		}
		if !plannedAttr.Equal(observedAttr) {
			stale = append(stale, modelType.Field(i).Tag.Get("tfsdk"))
		}
	}
	return stale
}

func (r *VectorStoreResource) readVectorStoreAfterUpdate(ctx context.Context, data *VectorStoreResourceModel, planned, prior VectorStoreResourceModel, attempts int) error {
	stable := 0
	var stale []string
	for attempt := 0; attempt < attempts; attempt++ {
		candidate := planned
		if err := r.readVectorStore(ctx, &candidate, false, true); err != nil {
			stale = []string{err.Error()}
			stable = 0
		} else if fields := vectorStoreChangedFieldsNotConverged(planned, prior, candidate); len(fields) > 0 {
			stale = fields
			stable = 0
		} else {
			stable++
			if stable >= 2 {
				*data = candidate
				return nil
			}
		}
		if attempt+1 < attempts {
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return fmt.Errorf("fields did not converge after %d reads: %s", attempts, strings.Join(stale, ", "))
}

func (r *VectorStoreResource) readVectorStore(ctx context.Context, data *VectorStoreResourceModel, imported, fresh bool) error {
	descriptionConfigured := vectorStoreFieldConfigured(data.VectorStoreDescriptionConfigured, !imported && !data.VectorStoreDescription.IsNull() && !data.VectorStoreDescription.IsUnknown())
	metadataConfigured := vectorStoreFieldConfigured(data.VectorStoreMetadataConfigured, !imported && !data.VectorStoreMetadata.IsNull() && !data.VectorStoreMetadata.IsUnknown())
	credentialConfigured := vectorStoreFieldConfigured(data.LiteLLMCredentialNameConfigured, !imported && !data.LiteLLMCredentialName.IsNull() && !data.LiteLLMCredentialName.IsUnknown())
	paramsConfigured := vectorStoreFieldConfigured(data.LiteLLMParamsConfigured, !imported && !data.LiteLLMParams.IsNull() && !data.LiteLLMParams.IsUnknown())
	if imported {
		descriptionConfigured, metadataConfigured, credentialConfigured, paramsConfigured = false, false, false, false
	}
	priorParams := data.LiteLLMParams
	priorMetadata := data.VectorStoreMetadata

	vectorStoreID := data.VectorStoreID.ValueString()
	if vectorStoreID == "" {
		vectorStoreID = data.ID.ValueString()
	}

	infoReq := map[string]interface{}{
		"vector_store_id": vectorStoreID,
	}

	var result map[string]interface{}
	var err error
	if fresh {
		err = r.client.doFreshRequestWithResponse(ctx, "POST", "/vector_store/info", infoReq, &result)
	} else {
		err = r.client.DoRequestWithResponse(ctx, "POST", "/vector_store/info", infoReq, &result)
	}
	if err != nil {
		return err
	}

	store, err := unwrapVectorStoreResponse(result, vectorStoreID)
	if err != nil {
		return err
	}
	name, nameOK := store["vector_store_name"].(string)
	provider, providerOK := store["custom_llm_provider"].(string)
	if !nameOK || name == "" || !providerOK || provider == "" {
		return fmt.Errorf("vector store response is missing required name or provider identity")
	}
	data.VectorStoreID = types.StringValue(vectorStoreID)
	data.ID = types.StringValue(vectorStoreID)
	data.VectorStoreName = types.StringValue(name)
	data.CustomLLMProvider = types.StringValue(provider)

	if description, ok := store["vector_store_description"].(string); ok && (description != "" || descriptionConfigured) {
		data.VectorStoreDescription = types.StringValue(description)
	} else {
		data.VectorStoreDescription = types.StringNull()
	}
	if credential, ok := store["litellm_credential_name"].(string); ok && (credential != "" || credentialConfigured) {
		data.LiteLLMCredentialName = types.StringValue(credential)
	} else {
		data.LiteLLMCredentialName = types.StringNull()
	}
	if createdAt, ok := store["created_at"].(string); ok && createdAt != "" {
		data.CreatedAt = types.StringValue(createdAt)
	} else {
		data.CreatedAt = types.StringNull()
	}

	metadata, err := vectorStoreStringMap(store["vector_store_metadata"], priorMetadata, false, false, "vector_store_metadata")
	if err != nil {
		return err
	}
	if !metadataConfigured && len(metadata.Elements()) == 0 {
		data.VectorStoreMetadata = types.MapNull(types.StringType)
	} else {
		data.VectorStoreMetadata = metadata
	}

	params, err := vectorStoreStringMap(store["litellm_params"], priorParams, paramsConfigured, true, "litellm_params")
	if err != nil {
		return err
	}
	if !paramsConfigured && len(params.Elements()) == 0 {
		data.LiteLLMParams = types.MapNull(types.StringType)
	} else {
		data.LiteLLMParams = params
	}
	data.VectorStoreDescriptionConfigured = types.BoolValue(descriptionConfigured)
	data.VectorStoreMetadataConfigured = types.BoolValue(metadataConfigured)
	data.LiteLLMCredentialNameConfigured = types.BoolValue(credentialConfigured)
	data.LiteLLMParamsConfigured = types.BoolValue(paramsConfigured)
	return nil
}
