package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &ModelResource{}
var _ resource.ResourceWithImportState = &ModelResource{}

func NewModelResource() resource.Resource {
	return &ModelResource{}
}

// ModelResource defines the resource implementation.
type ModelResource struct {
	client *Client
}

// ModelResourceModel describes the resource data model.
type ModelResourceModel struct {
	ID                             types.String  `tfsdk:"id"`
	ModelName                      types.String  `tfsdk:"model_name"`
	CustomLLMProvider              types.String  `tfsdk:"custom_llm_provider"`
	TPM                            types.Int64   `tfsdk:"tpm"`
	RPM                            types.Int64   `tfsdk:"rpm"`
	ReasoningEffort                types.String  `tfsdk:"reasoning_effort"`
	ThinkingEnabled                types.Bool    `tfsdk:"thinking_enabled"`
	ThinkingBudgetTokens           types.Int64   `tfsdk:"thinking_budget_tokens"`
	MergeReasoningContentInChoices types.Bool    `tfsdk:"merge_reasoning_content_in_choices"`
	ModelAPIKey                    types.String  `tfsdk:"model_api_key"`
	ModelAPIBase                   types.String  `tfsdk:"model_api_base"`
	APIVersion                     types.String  `tfsdk:"api_version"`
	BaseModel                      types.String  `tfsdk:"base_model"`
	Tier                           types.String  `tfsdk:"tier"`
	TeamID                         types.String  `tfsdk:"team_id"`
	Mode                           types.String  `tfsdk:"mode"`
	LiteLLMCredentialName          types.String  `tfsdk:"litellm_credential_name"`
	InputCostPerMillionTokens      types.Float64 `tfsdk:"input_cost_per_million_tokens"`
	OutputCostPerMillionTokens     types.Float64 `tfsdk:"output_cost_per_million_tokens"`
	InputCostPerPixel              types.Float64 `tfsdk:"input_cost_per_pixel"`
	OutputCostPerPixel             types.Float64 `tfsdk:"output_cost_per_pixel"`
	InputCostPerSecond             types.Float64 `tfsdk:"input_cost_per_second"`
	OutputCostPerSecond            types.Float64 `tfsdk:"output_cost_per_second"`
	AWSAccessKeyID                 types.String  `tfsdk:"aws_access_key_id"`
	AWSSecretAccessKey             types.String  `tfsdk:"aws_secret_access_key"`
	AWSRegionName                  types.String  `tfsdk:"aws_region_name"`
	AWSSessionName                 types.String  `tfsdk:"aws_session_name"`
	AWSRoleName                    types.String  `tfsdk:"aws_role_name"`
	VertexProject                  types.String  `tfsdk:"vertex_project"`
	VertexLocation                 types.String  `tfsdk:"vertex_location"`
	VertexCredentials              types.String  `tfsdk:"vertex_credentials"`
	AccessGroups                   types.List    `tfsdk:"access_groups"`
	AdditionalLiteLLMParams        types.Map     `tfsdk:"additional_litellm_params"`
}

func (r *ModelResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_model"
}

func (r *ModelResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM model deployment.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this model.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"model_name": schema.StringAttribute{
				Description: "The name of the model as it will appear in LiteLLM.",
				Required:    true,
			},
			"custom_llm_provider": schema.StringAttribute{
				Description: "The LLM provider (e.g., openai, anthropic, bedrock).",
				Required:    true,
			},
			"tpm": schema.Int64Attribute{
				Description: "Tokens per minute limit.",
				Optional:    true,
			},
			"rpm": schema.Int64Attribute{
				Description: "Requests per minute limit.",
				Optional:    true,
			},
			"reasoning_effort": schema.StringAttribute{
				Description: "Reasoning effort level (low, medium, high).",
				Optional:    true,
			},
			"thinking_enabled": schema.BoolAttribute{
				Description: "Enable thinking/reasoning mode.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"thinking_budget_tokens": schema.Int64Attribute{
				Description: "Budget tokens for thinking mode.",
				Optional:    true,
				Computed:    true,
				Default:     int64default.StaticInt64(1024),
			},
			"merge_reasoning_content_in_choices": schema.BoolAttribute{
				Description: "Merge reasoning content in choices.",
				Optional:    true,
			},
			"model_api_key": schema.StringAttribute{
				Description: "API key for the model provider.",
				Optional:    true,
				Sensitive:   true,
			},
			"model_api_base": schema.StringAttribute{
				Description: "Base URL for the model API.",
				Optional:    true,
			},
			"api_version": schema.StringAttribute{
				Description: "API version (e.g., for Azure OpenAI).",
				Optional:    true,
			},
			"base_model": schema.StringAttribute{
				Description: "The base model name from the provider.",
				Required:    true,
			},
			"tier": schema.StringAttribute{
				Description: "Model tier (free, paid, etc.).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("free"),
			},
			"team_id": schema.StringAttribute{
				Description: "Team ID to associate with this model.",
				Optional:    true,
			},
			"mode": schema.StringAttribute{
				Description: "Model mode (completion, embedding, image_generation, chat, moderation, audio_transcription, batch).",
				Optional:    true,
			},
			"litellm_credential_name": schema.StringAttribute{
				Description: "Name of the credential to use for this model. References a credential created via litellm_credential resource.",
				Optional:    true,
			},
			"input_cost_per_million_tokens": schema.Float64Attribute{
				Description: "Input cost per million tokens.",
				Optional:    true,
			},
			"output_cost_per_million_tokens": schema.Float64Attribute{
				Description: "Output cost per million tokens.",
				Optional:    true,
			},
			"input_cost_per_pixel": schema.Float64Attribute{
				Description: "Input cost per pixel.",
				Optional:    true,
			},
			"output_cost_per_pixel": schema.Float64Attribute{
				Description: "Output cost per pixel.",
				Optional:    true,
			},
			"input_cost_per_second": schema.Float64Attribute{
				Description: "Input cost per second.",
				Optional:    true,
			},
			"output_cost_per_second": schema.Float64Attribute{
				Description: "Output cost per second.",
				Optional:    true,
			},
			"aws_access_key_id": schema.StringAttribute{
				Description: "AWS access key ID for Bedrock.",
				Optional:    true,
				Sensitive:   true,
			},
			"aws_secret_access_key": schema.StringAttribute{
				Description: "AWS secret access key for Bedrock.",
				Optional:    true,
				Sensitive:   true,
			},
			"aws_region_name": schema.StringAttribute{
				Description: "AWS region name for Bedrock.",
				Optional:    true,
			},
			"aws_session_name": schema.StringAttribute{
				Description: "AWS session name for Bedrock.",
				Optional:    true,
				Sensitive:   true,
			},
			"aws_role_name": schema.StringAttribute{
				Description: "AWS role name for Bedrock.",
				Optional:    true,
				Sensitive:   true,
			},
			"vertex_project": schema.StringAttribute{
				Description: "Google Cloud project for Vertex AI.",
				Optional:    true,
				Sensitive:   true,
			},
			"vertex_location": schema.StringAttribute{
				Description: "Google Cloud location for Vertex AI.",
				Optional:    true,
				Sensitive:   true,
			},
			"vertex_credentials": schema.StringAttribute{
				Description: "Google Cloud credentials for Vertex AI.",
				Optional:    true,
			},
			"access_groups": schema.ListAttribute{
				Description: "List of access groups this model belongs to. Teams and keys with access to these groups can use this model.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"additional_litellm_params": schema.MapAttribute{
				Description: "Additional parameters to pass to litellm_params.",
				Optional:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *ModelResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *ModelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ModelResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	modelID := uuid.New().String()

	if err := r.createOrUpdateModel(ctx, &data, modelID, false); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create model: %s", err))
		return
	}

	data.ID = types.StringValue(modelID)

	// Read back to ensure consistency
	if err := r.readModelWithRetry(ctx, &data, 5); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Model created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ModelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ModelResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.readModel(ctx, &data)
	if err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read model: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ModelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ModelResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state ModelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = state.ID

	// Use PATCH endpoint for partial updates
	if err := r.patchModel(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update model: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ModelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ModelResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := map[string]string{"id": data.ID.ValueString()}
	err := r.client.DoRequestWithResponse(ctx, "POST", "/model/delete", deleteReq, nil)
	if err != nil && !IsNotFoundError(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete model: %s", err))
		return
	}
}

func (r *ModelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *ModelResource) createOrUpdateModel(ctx context.Context, data *ModelResourceModel, modelID string, isUpdate bool) error {
	customLLMProvider := data.CustomLLMProvider.ValueString()
	baseModel := data.BaseModel.ValueString()
	modelName := fmt.Sprintf("%s/%s", customLLMProvider, baseModel)

	litellmParams := map[string]interface{}{
		"custom_llm_provider": customLLMProvider,
		"model":               modelName,
	}

	// Add cost parameters
	if !data.InputCostPerMillionTokens.IsNull() {
		litellmParams["input_cost_per_token"] = data.InputCostPerMillionTokens.ValueFloat64() / 1000000.0
	}
	if !data.OutputCostPerMillionTokens.IsNull() {
		litellmParams["output_cost_per_token"] = data.OutputCostPerMillionTokens.ValueFloat64() / 1000000.0
	}

	// Add optional parameters
	if !data.TPM.IsNull() && data.TPM.ValueInt64() > 0 {
		litellmParams["tpm"] = data.TPM.ValueInt64()
	}
	if !data.RPM.IsNull() && data.RPM.ValueInt64() > 0 {
		litellmParams["rpm"] = data.RPM.ValueInt64()
	}
	if !data.ModelAPIKey.IsNull() {
		litellmParams["api_key"] = data.ModelAPIKey.ValueString()
	}
	if !data.ModelAPIBase.IsNull() {
		litellmParams["api_base"] = data.ModelAPIBase.ValueString()
	}
	if !data.APIVersion.IsNull() {
		litellmParams["api_version"] = data.APIVersion.ValueString()
	}
	if !data.ReasoningEffort.IsNull() {
		litellmParams["reasoning_effort"] = data.ReasoningEffort.ValueString()
	}
	if !data.MergeReasoningContentInChoices.IsNull() {
		litellmParams["merge_reasoning_content_in_choices"] = data.MergeReasoningContentInChoices.ValueBool()
	}

	// Thinking configuration
	if data.ThinkingEnabled.ValueBool() {
		litellmParams["thinking"] = map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": data.ThinkingBudgetTokens.ValueInt64(),
		}
	}

	// AWS parameters
	if !data.AWSAccessKeyID.IsNull() {
		litellmParams["aws_access_key_id"] = data.AWSAccessKeyID.ValueString()
	}
	if !data.AWSSecretAccessKey.IsNull() {
		litellmParams["aws_secret_access_key"] = data.AWSSecretAccessKey.ValueString()
	}
	if !data.AWSRegionName.IsNull() {
		litellmParams["aws_region_name"] = data.AWSRegionName.ValueString()
	}
	if !data.AWSSessionName.IsNull() {
		litellmParams["aws_session_name"] = data.AWSSessionName.ValueString()
	}
	if !data.AWSRoleName.IsNull() {
		litellmParams["aws_role_name"] = data.AWSRoleName.ValueString()
	}

	// Vertex parameters
	if !data.VertexProject.IsNull() {
		litellmParams["vertex_project"] = data.VertexProject.ValueString()
	}
	if !data.VertexLocation.IsNull() {
		litellmParams["vertex_location"] = data.VertexLocation.ValueString()
	}
	if !data.VertexCredentials.IsNull() {
		litellmParams["vertex_credentials"] = data.VertexCredentials.ValueString()
	}

	// Credential reference
	if !data.LiteLLMCredentialName.IsNull() {
		litellmParams["litellm_credential_name"] = data.LiteLLMCredentialName.ValueString()
	}

	// Cost per pixel/second
	if !data.InputCostPerPixel.IsNull() {
		litellmParams["input_cost_per_pixel"] = data.InputCostPerPixel.ValueFloat64()
	}
	if !data.OutputCostPerPixel.IsNull() {
		litellmParams["output_cost_per_pixel"] = data.OutputCostPerPixel.ValueFloat64()
	}
	if !data.InputCostPerSecond.IsNull() {
		litellmParams["input_cost_per_second"] = data.InputCostPerSecond.ValueFloat64()
	}
	if !data.OutputCostPerSecond.IsNull() {
		litellmParams["output_cost_per_second"] = data.OutputCostPerSecond.ValueFloat64()
	}

	modelInfo := map[string]interface{}{
		"id":         modelID,
		"db_model":   true,
		"base_model": baseModel,
		"tier":       data.Tier.ValueString(),
		"mode":       data.Mode.ValueString(),
		"team_id":    data.TeamID.ValueString(),
	}

	// Add access_groups to model_info if specified
	if !data.AccessGroups.IsNull() {
		var accessGroups []string
		data.AccessGroups.ElementsAs(ctx, &accessGroups, false)
		if len(accessGroups) > 0 {
			modelInfo["access_groups"] = accessGroups
		}
	}

	modelReq := map[string]interface{}{
		"model_name":     data.ModelName.ValueString(),
		"litellm_params": litellmParams,
		"model_info":     modelInfo,
	}

	endpoint := "/model/new"
	if isUpdate {
		endpoint = "/model/update"
	}

	return r.client.DoRequestWithResponse(ctx, "POST", endpoint, modelReq, nil)
}

func (r *ModelResource) readModel(ctx context.Context, data *ModelResourceModel) error {
	endpoint := fmt.Sprintf("/model/info?litellm_model_id=%s", data.ID.ValueString())

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// Update data from response while preserving sensitive values
	if modelName, ok := result["model_name"].(string); ok && modelName != "" {
		data.ModelName = types.StringValue(modelName)
	}

	if litellmParams, ok := result["litellm_params"].(map[string]interface{}); ok {
		if provider, ok := litellmParams["custom_llm_provider"].(string); ok && provider != "" {
			data.CustomLLMProvider = types.StringValue(provider)
		}
		if apiBase, ok := litellmParams["api_base"].(string); ok && apiBase != "" {
			data.ModelAPIBase = types.StringValue(apiBase)
		}
		if apiVersion, ok := litellmParams["api_version"].(string); ok && apiVersion != "" {
			data.APIVersion = types.StringValue(apiVersion)
		}
		if tpm, ok := litellmParams["tpm"].(float64); ok {
			data.TPM = types.Int64Value(int64(tpm))
		}
		if rpm, ok := litellmParams["rpm"].(float64); ok {
			data.RPM = types.Int64Value(int64(rpm))
		}
		if awsRegion, ok := litellmParams["aws_region_name"].(string); ok && awsRegion != "" {
			data.AWSRegionName = types.StringValue(awsRegion)
		}
		if credName, ok := litellmParams["litellm_credential_name"].(string); ok && credName != "" {
			data.LiteLLMCredentialName = types.StringValue(credName)
		}
	}

	if modelInfo, ok := result["model_info"].(map[string]interface{}); ok {
		if baseModel, ok := modelInfo["base_model"].(string); ok && baseModel != "" {
			data.BaseModel = types.StringValue(baseModel)
		}
		if tier, ok := modelInfo["tier"].(string); ok && tier != "" {
			data.Tier = types.StringValue(tier)
		}
		if mode, ok := modelInfo["mode"].(string); ok && mode != "" {
			data.Mode = types.StringValue(mode)
		}
		if teamID, ok := modelInfo["team_id"].(string); ok && teamID != "" {
			data.TeamID = types.StringValue(teamID)
		}
		// Read access_groups from model_info
		if accessGroups, ok := modelInfo["access_groups"].([]interface{}); ok && len(accessGroups) > 0 {
			groupStrings := make([]string, 0, len(accessGroups))
			for _, g := range accessGroups {
				if groupStr, ok := g.(string); ok {
					groupStrings = append(groupStrings, groupStr)
				}
			}
			if len(groupStrings) > 0 {
				listValue, diags := types.ListValueFrom(ctx, types.StringType, groupStrings)
				if !diags.HasError() {
					data.AccessGroups = listValue
				}
			}
		}
	}

	return nil
}

func (r *ModelResource) readModelWithRetry(ctx context.Context, data *ModelResourceModel, maxRetries int) error {
	var err error
	delay := 1 * time.Second
	maxDelay := 10 * time.Second

	for i := 0; i < maxRetries; i++ {
		err = r.readModel(ctx, data)
		if err == nil {
			return nil
		}

		if !IsNotFoundError(err) {
			return err
		}

		if i < maxRetries-1 {
			time.Sleep(delay)
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}

	return err
}

// patchModel uses the PATCH /model/{model_id}/update endpoint for partial updates
func (r *ModelResource) patchModel(ctx context.Context, data *ModelResourceModel) error {
	modelID := data.ID.ValueString()
	customLLMProvider := data.CustomLLMProvider.ValueString()
	baseModel := data.BaseModel.ValueString()
	modelName := fmt.Sprintf("%s/%s", customLLMProvider, baseModel)

	// Build litellm_params for the patch request
	litellmParams := map[string]interface{}{
		"custom_llm_provider": customLLMProvider,
		"model":               modelName,
	}

	// Add cost parameters
	if !data.InputCostPerMillionTokens.IsNull() {
		litellmParams["input_cost_per_token"] = data.InputCostPerMillionTokens.ValueFloat64() / 1000000.0
	}
	if !data.OutputCostPerMillionTokens.IsNull() {
		litellmParams["output_cost_per_token"] = data.OutputCostPerMillionTokens.ValueFloat64() / 1000000.0
	}

	// Add optional parameters
	if !data.TPM.IsNull() && data.TPM.ValueInt64() > 0 {
		litellmParams["tpm"] = data.TPM.ValueInt64()
	}
	if !data.RPM.IsNull() && data.RPM.ValueInt64() > 0 {
		litellmParams["rpm"] = data.RPM.ValueInt64()
	}
	if !data.ModelAPIKey.IsNull() {
		litellmParams["api_key"] = data.ModelAPIKey.ValueString()
	}
	if !data.ModelAPIBase.IsNull() {
		litellmParams["api_base"] = data.ModelAPIBase.ValueString()
	}
	if !data.APIVersion.IsNull() {
		litellmParams["api_version"] = data.APIVersion.ValueString()
	}
	if !data.ReasoningEffort.IsNull() {
		litellmParams["reasoning_effort"] = data.ReasoningEffort.ValueString()
	}
	if !data.MergeReasoningContentInChoices.IsNull() {
		litellmParams["merge_reasoning_content_in_choices"] = data.MergeReasoningContentInChoices.ValueBool()
	}

	// Thinking configuration
	if data.ThinkingEnabled.ValueBool() {
		litellmParams["thinking"] = map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": data.ThinkingBudgetTokens.ValueInt64(),
		}
	}

	// AWS parameters
	if !data.AWSAccessKeyID.IsNull() {
		litellmParams["aws_access_key_id"] = data.AWSAccessKeyID.ValueString()
	}
	if !data.AWSSecretAccessKey.IsNull() {
		litellmParams["aws_secret_access_key"] = data.AWSSecretAccessKey.ValueString()
	}
	if !data.AWSRegionName.IsNull() {
		litellmParams["aws_region_name"] = data.AWSRegionName.ValueString()
	}
	if !data.AWSSessionName.IsNull() {
		litellmParams["aws_session_name"] = data.AWSSessionName.ValueString()
	}
	if !data.AWSRoleName.IsNull() {
		litellmParams["aws_role_name"] = data.AWSRoleName.ValueString()
	}

	// Vertex parameters
	if !data.VertexProject.IsNull() {
		litellmParams["vertex_project"] = data.VertexProject.ValueString()
	}
	if !data.VertexLocation.IsNull() {
		litellmParams["vertex_location"] = data.VertexLocation.ValueString()
	}
	if !data.VertexCredentials.IsNull() {
		litellmParams["vertex_credentials"] = data.VertexCredentials.ValueString()
	}

	// Credential reference
	if !data.LiteLLMCredentialName.IsNull() {
		litellmParams["litellm_credential_name"] = data.LiteLLMCredentialName.ValueString()
	}

	// Cost per pixel/second
	if !data.InputCostPerPixel.IsNull() {
		litellmParams["input_cost_per_pixel"] = data.InputCostPerPixel.ValueFloat64()
	}
	if !data.OutputCostPerPixel.IsNull() {
		litellmParams["output_cost_per_pixel"] = data.OutputCostPerPixel.ValueFloat64()
	}
	if !data.InputCostPerSecond.IsNull() {
		litellmParams["input_cost_per_second"] = data.InputCostPerSecond.ValueFloat64()
	}
	if !data.OutputCostPerSecond.IsNull() {
		litellmParams["output_cost_per_second"] = data.OutputCostPerSecond.ValueFloat64()
	}

	// Build model_info for the PATCH request
	modelInfo := map[string]interface{}{
		"base_model": baseModel,
		"tier":       data.Tier.ValueString(),
		"mode":       data.Mode.ValueString(),
		"team_id":    data.TeamID.ValueString(),
	}

	// Add access_groups to model_info if specified
	if !data.AccessGroups.IsNull() {
		var accessGroups []string
		data.AccessGroups.ElementsAs(ctx, &accessGroups, false)
		if len(accessGroups) > 0 {
			modelInfo["access_groups"] = accessGroups
		}
	}

	// Build the PATCH request body
	patchReq := map[string]interface{}{
		"model_name":     data.ModelName.ValueString(),
		"litellm_params": litellmParams,
		"model_info":     modelInfo,
	}

	endpoint := fmt.Sprintf("/model/%s/update", modelID)
	return r.client.DoRequestWithResponse(ctx, "PATCH", endpoint, patchReq, nil)
}
