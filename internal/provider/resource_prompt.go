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
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &PromptResource{}
var _ resource.ResourceWithImportState = &PromptResource{}

func NewPromptResource() resource.Resource {
	return &PromptResource{}
}

type PromptResource struct {
	client *Client
}

type PromptResourceModel struct {
	ID                                types.String `tfsdk:"id"`
	PromptID                          types.String `tfsdk:"prompt_id"`
	PromptIntegration                 types.String `tfsdk:"prompt_integration"`
	APIBase                           types.String `tfsdk:"api_base"`
	APIKey                            types.String `tfsdk:"api_key"`
	ProviderSpecificQueryParams       types.String `tfsdk:"provider_specific_query_params"`
	IgnorePromptManagerModel          types.Bool   `tfsdk:"ignore_prompt_manager_model"`
	IgnorePromptManagerOptionalParams types.Bool   `tfsdk:"ignore_prompt_manager_optional_params"`
	DotpromptContent                  types.String `tfsdk:"dotprompt_content"`
	PromptType                        types.String `tfsdk:"prompt_type"`
}

func (r *PromptResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_prompt"
}

func (r *PromptResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM prompt. Prompts allow you to manage prompt templates from external providers like Langfuse, Humanloop, etc.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this prompt (same as prompt_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"prompt_id": schema.StringAttribute{
				Description: "The unique prompt ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"prompt_integration": schema.StringAttribute{
				Description: "The prompt integration provider (e.g., 'langfuse', 'humanloop', 'promptlayer', 'dotprompt').",
				Required:    true,
			},
			"api_base": schema.StringAttribute{
				Description: "Base URL for the prompt provider API.",
				Optional:    true,
			},
			"api_key": schema.StringAttribute{
				Description: "API key for the prompt provider.",
				Optional:    true,
				Sensitive:   true,
			},
			"provider_specific_query_params": schema.StringAttribute{
				Description: "JSON string of provider-specific query parameters.",
				Optional:    true,
			},
			"ignore_prompt_manager_model": schema.BoolAttribute{
				Description: "If true, ignore the model specified in the prompt manager.",
				Optional:    true,
			},
			"ignore_prompt_manager_optional_params": schema.BoolAttribute{
				Description: "If true, ignore optional params from the prompt manager.",
				Optional:    true,
			},
			"dotprompt_content": schema.StringAttribute{
				Description: "Content for dotprompt integration (Firebase Genkit format).",
				Optional:    true,
			},
			"prompt_type": schema.StringAttribute{
				Description: "Type of prompt: 'config' or 'db'.",
				Optional:    true,
			},
		},
	}
}

func (r *PromptResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PromptResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data PromptResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	promptReq := r.buildPromptRequest(ctx, &data)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/prompts", promptReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create prompt: %s", err))
		return
	}

	data.ID = data.PromptID

	// Read back for full state
	if err := r.readPrompt(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Prompt created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PromptResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data PromptResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readPrompt(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read prompt: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PromptResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data PromptResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state PromptResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve IDs
	data.ID = state.ID
	data.PromptID = state.PromptID

	promptReq := r.buildPromptRequest(ctx, &data)

	endpoint := fmt.Sprintf("/prompts/%s", data.PromptID.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "PUT", endpoint, promptReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update prompt: %s", err))
		return
	}

	// Read back for full state
	if err := r.readPrompt(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Prompt updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PromptResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data PromptResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/prompts/%s", data.PromptID.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete prompt: %s", err))
			return
		}
	}
}

func (r *PromptResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("prompt_id"), req.ID)...)
}

func (r *PromptResource) buildPromptRequest(ctx context.Context, data *PromptResourceModel) map[string]interface{} {
	litellmParams := map[string]interface{}{
		"prompt_integration": data.PromptIntegration.ValueString(),
	}

	if !data.APIBase.IsNull() && data.APIBase.ValueString() != "" {
		litellmParams["api_base"] = data.APIBase.ValueString()
	}
	if !data.APIKey.IsNull() && data.APIKey.ValueString() != "" {
		litellmParams["api_key"] = data.APIKey.ValueString()
	}
	if !data.IgnorePromptManagerModel.IsNull() {
		litellmParams["ignore_prompt_manager_model"] = data.IgnorePromptManagerModel.ValueBool()
	}
	if !data.IgnorePromptManagerOptionalParams.IsNull() {
		litellmParams["ignore_prompt_manager_optional_params"] = data.IgnorePromptManagerOptionalParams.ValueBool()
	}
	if !data.DotpromptContent.IsNull() && data.DotpromptContent.ValueString() != "" {
		litellmParams["dotprompt_content"] = data.DotpromptContent.ValueString()
	}
	if !data.ProviderSpecificQueryParams.IsNull() && data.ProviderSpecificQueryParams.ValueString() != "" {
		var params map[string]interface{}
		if err := json.Unmarshal([]byte(data.ProviderSpecificQueryParams.ValueString()), &params); err == nil {
			litellmParams["provider_specific_query_params"] = params
		}
	}

	promptReq := map[string]interface{}{
		"prompt_id":      data.PromptID.ValueString(),
		"litellm_params": litellmParams,
	}

	if !data.PromptType.IsNull() && data.PromptType.ValueString() != "" {
		promptReq["prompt_info"] = map[string]interface{}{
			"prompt_type": data.PromptType.ValueString(),
		}
	}

	return promptReq
}

func (r *PromptResource) readPrompt(ctx context.Context, data *PromptResourceModel) error {
	promptID := data.PromptID.ValueString()
	if promptID == "" {
		promptID = data.ID.ValueString()
	}

	endpoint := fmt.Sprintf("/prompts/%s/info", promptID)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// Update fields from response
	if id, ok := result["prompt_id"].(string); ok {
		data.PromptID = types.StringValue(id)
		data.ID = types.StringValue(id)
	}

	// Handle litellm_params
	if litellmParams, ok := result["litellm_params"].(map[string]interface{}); ok {
		if integration, ok := litellmParams["prompt_integration"].(string); ok {
			data.PromptIntegration = types.StringValue(integration)
		}
		if apiBase, ok := litellmParams["api_base"].(string); ok {
			data.APIBase = types.StringValue(apiBase)
		}
		if ignoreModel, ok := litellmParams["ignore_prompt_manager_model"].(bool); ok {
			data.IgnorePromptManagerModel = types.BoolValue(ignoreModel)
		}
		if ignoreParams, ok := litellmParams["ignore_prompt_manager_optional_params"].(bool); ok {
			data.IgnorePromptManagerOptionalParams = types.BoolValue(ignoreParams)
		}
		if dotprompt, ok := litellmParams["dotprompt_content"].(string); ok {
			data.DotpromptContent = types.StringValue(dotprompt)
		}
		if providerParams, ok := litellmParams["provider_specific_query_params"].(map[string]interface{}); ok {
			if jsonBytes, err := json.Marshal(providerParams); err == nil {
				data.ProviderSpecificQueryParams = types.StringValue(string(jsonBytes))
			}
		}
	}

	// Handle prompt_info
	if promptInfo, ok := result["prompt_info"].(map[string]interface{}); ok {
		if promptType, ok := promptInfo["prompt_type"].(string); ok {
			data.PromptType = types.StringValue(promptType)
		}
	}

	return nil
}
