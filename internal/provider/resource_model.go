package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework/attr"
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
				Description: "Model mode. Supported values: chat, completion, embedding, audio_speech, audio_transcription, image_generation, video_generation, batch, rerank, realtime, responses, ocr, moderation.",
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
				Computed:    true,
				ElementType: types.StringType,
			},
			"additional_litellm_params": schema.MapAttribute{
				Description: "Additional parameters to pass to litellm_params.",
				Optional:    true,
				Computed:    true,
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

	if err := r.readModel(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Model updated but failed to read back: %s", err))
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
	if !data.InputCostPerMillionTokens.IsNull() && !data.InputCostPerMillionTokens.IsUnknown() {
		litellmParams["input_cost_per_token"] = data.InputCostPerMillionTokens.ValueFloat64() / 1000000.0
	}
	if !data.OutputCostPerMillionTokens.IsNull() && !data.OutputCostPerMillionTokens.IsUnknown() {
		litellmParams["output_cost_per_token"] = data.OutputCostPerMillionTokens.ValueFloat64() / 1000000.0
	}

	// Add optional parameters
	if !data.TPM.IsNull() && !data.TPM.IsUnknown() && data.TPM.ValueInt64() > 0 {
		litellmParams["tpm"] = data.TPM.ValueInt64()
	}
	if !data.RPM.IsNull() && !data.RPM.IsUnknown() && data.RPM.ValueInt64() > 0 {
		litellmParams["rpm"] = data.RPM.ValueInt64()
	}
	if !data.ModelAPIKey.IsNull() && !data.ModelAPIKey.IsUnknown() && data.ModelAPIKey.ValueString() != "" {
		litellmParams["api_key"] = data.ModelAPIKey.ValueString()
	}
	if !data.ModelAPIBase.IsNull() && !data.ModelAPIBase.IsUnknown() && data.ModelAPIBase.ValueString() != "" {
		litellmParams["api_base"] = data.ModelAPIBase.ValueString()
	}
	if !data.APIVersion.IsNull() && !data.APIVersion.IsUnknown() && data.APIVersion.ValueString() != "" {
		litellmParams["api_version"] = data.APIVersion.ValueString()
	}
	if !data.ReasoningEffort.IsNull() && !data.ReasoningEffort.IsUnknown() && data.ReasoningEffort.ValueString() != "" {
		litellmParams["reasoning_effort"] = data.ReasoningEffort.ValueString()
	}
	if !data.MergeReasoningContentInChoices.IsNull() && !data.MergeReasoningContentInChoices.IsUnknown() {
		litellmParams["merge_reasoning_content_in_choices"] = data.MergeReasoningContentInChoices.ValueBool()
	}

	// Thinking configuration
	if !data.ThinkingEnabled.IsNull() && !data.ThinkingEnabled.IsUnknown() && data.ThinkingEnabled.ValueBool() {
		litellmParams["thinking"] = map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": data.ThinkingBudgetTokens.ValueInt64(),
		}
	}

	// AWS parameters
	if !data.AWSAccessKeyID.IsNull() && !data.AWSAccessKeyID.IsUnknown() && data.AWSAccessKeyID.ValueString() != "" {
		litellmParams["aws_access_key_id"] = data.AWSAccessKeyID.ValueString()
	}
	if !data.AWSSecretAccessKey.IsNull() && !data.AWSSecretAccessKey.IsUnknown() && data.AWSSecretAccessKey.ValueString() != "" {
		litellmParams["aws_secret_access_key"] = data.AWSSecretAccessKey.ValueString()
	}
	if !data.AWSRegionName.IsNull() && !data.AWSRegionName.IsUnknown() && data.AWSRegionName.ValueString() != "" {
		litellmParams["aws_region_name"] = data.AWSRegionName.ValueString()
	}
	if !data.AWSSessionName.IsNull() && !data.AWSSessionName.IsUnknown() && data.AWSSessionName.ValueString() != "" {
		litellmParams["aws_session_name"] = data.AWSSessionName.ValueString()
	}
	if !data.AWSRoleName.IsNull() && !data.AWSRoleName.IsUnknown() && data.AWSRoleName.ValueString() != "" {
		litellmParams["aws_role_name"] = data.AWSRoleName.ValueString()
	}

	// Vertex parameters
	if !data.VertexProject.IsNull() && !data.VertexProject.IsUnknown() && data.VertexProject.ValueString() != "" {
		litellmParams["vertex_project"] = data.VertexProject.ValueString()
	}
	if !data.VertexLocation.IsNull() && !data.VertexLocation.IsUnknown() && data.VertexLocation.ValueString() != "" {
		litellmParams["vertex_location"] = data.VertexLocation.ValueString()
	}
	if !data.VertexCredentials.IsNull() && !data.VertexCredentials.IsUnknown() && data.VertexCredentials.ValueString() != "" {
		litellmParams["vertex_credentials"] = data.VertexCredentials.ValueString()
	}

	// Credential reference
	if !data.LiteLLMCredentialName.IsNull() && !data.LiteLLMCredentialName.IsUnknown() && data.LiteLLMCredentialName.ValueString() != "" {
		litellmParams["litellm_credential_name"] = data.LiteLLMCredentialName.ValueString()
	}

	// Cost per pixel/second
	if !data.InputCostPerPixel.IsNull() && !data.InputCostPerPixel.IsUnknown() {
		litellmParams["input_cost_per_pixel"] = data.InputCostPerPixel.ValueFloat64()
	}
	if !data.OutputCostPerPixel.IsNull() && !data.OutputCostPerPixel.IsUnknown() {
		litellmParams["output_cost_per_pixel"] = data.OutputCostPerPixel.ValueFloat64()
	}
	if !data.InputCostPerSecond.IsNull() && !data.InputCostPerSecond.IsUnknown() {
		litellmParams["input_cost_per_second"] = data.InputCostPerSecond.ValueFloat64()
	}
	if !data.OutputCostPerSecond.IsNull() && !data.OutputCostPerSecond.IsUnknown() {
		litellmParams["output_cost_per_second"] = data.OutputCostPerSecond.ValueFloat64()
	}

	// Add additional_litellm_params to the request.
	// Values are strings in Terraform but converted to native types (int, float, bool, JSON)
	// for the API. This allows users to pass any litellm_params not covered by top-level attributes.
	if !data.AdditionalLiteLLMParams.IsNull() && !data.AdditionalLiteLLMParams.IsUnknown() {
		elements := make(map[string]string)
		data.AdditionalLiteLLMParams.ElementsAs(ctx, &elements, false)
		for key, value := range elements {
			litellmParams[key] = convertStringValue(value)
		}
	}

	modelInfo := map[string]interface{}{
		"id":         modelID,
		"db_model":   true,
		"base_model": baseModel,
	}

	// Only add optional model_info fields if they have values
	if !data.Tier.IsNull() && !data.Tier.IsUnknown() && data.Tier.ValueString() != "" {
		modelInfo["tier"] = data.Tier.ValueString()
	}
	if !data.Mode.IsNull() && !data.Mode.IsUnknown() && data.Mode.ValueString() != "" {
		modelInfo["mode"] = data.Mode.ValueString()
	}
	if !data.TeamID.IsNull() && !data.TeamID.IsUnknown() && data.TeamID.ValueString() != "" {
		modelInfo["team_id"] = data.TeamID.ValueString()
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

	var rawResult map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &rawResult); err != nil {
		return err
	}

	// The /model/info endpoint returns {"data": [{...}]}, extract the first element
	result := rawResult
	if dataArr, ok := rawResult["data"].([]interface{}); ok && len(dataArr) > 0 {
		if firstItem, ok := dataArr[0].(map[string]interface{}); ok {
			result = firstItem
		}
	}

	// Update data from response while preserving sensitive values
	if modelName, ok := result["model_name"].(string); ok && modelName != "" {
		data.ModelName = types.StringValue(modelName)
	}

	if litellmParams, ok := result["litellm_params"].(map[string]interface{}); ok {
		// Update top-level attributes from API response.
		// For optional attributes (tpm, rpm, merge_reasoning_content_in_choices),
		// only update if the attribute was set in the config (!IsNull).
		// Otherwise, values that exist in API but not in config would cause
		// an infinite plan diff: plan wants to remove → PATCH can't delete
		// (LiteLLM merges litellm_params) → Read sees it again → repeat.
		if provider, ok := litellmParams["custom_llm_provider"].(string); ok && provider != "" {
			data.CustomLLMProvider = types.StringValue(provider)
		}
		if apiBase, ok := litellmParams["api_base"].(string); ok && apiBase != "" {
			data.ModelAPIBase = types.StringValue(apiBase)
		}
		if apiVersion, ok := litellmParams["api_version"].(string); ok && apiVersion != "" {
			data.APIVersion = types.StringValue(apiVersion)
		}
		if tpm, ok := litellmParams["tpm"].(float64); ok && !data.TPM.IsNull() {
			data.TPM = types.Int64Value(int64(tpm))
		}
		if rpm, ok := litellmParams["rpm"].(float64); ok && !data.RPM.IsNull() {
			data.RPM = types.Int64Value(int64(rpm))
		}
		if tpm, ok := litellmParams["tpm"].(int64); ok && !data.TPM.IsNull() {
			data.TPM = types.Int64Value(tpm)
		}
		if rpm, ok := litellmParams["rpm"].(int64); ok && !data.RPM.IsNull() {
			data.RPM = types.Int64Value(rpm)
		}
		if awsRegion, ok := litellmParams["aws_region_name"].(string); ok && awsRegion != "" {
			data.AWSRegionName = types.StringValue(awsRegion)
		}
		if credName, ok := litellmParams["litellm_credential_name"].(string); ok && credName != "" {
			data.LiteLLMCredentialName = types.StringValue(credName)
		}
		// NOTE: merge_reasoning_content_in_choices is intentionally NOT read into the
		// top-level attribute here. It can be passed both as a top-level attribute and
		// via additional_litellm_params. Since templates commonly use additional_litellm_params,
		// we let it flow through the additional params path to avoid drift-loop conflicts.

		// Handle additional_litellm_params map - preserve state when API omits custom params.
		knownLiteLLMParams := map[string]struct{}{
			"custom_llm_provider":                {},
			"model":                              {},
			"input_cost_per_token":               {},
			"output_cost_per_token":              {},
			"tpm":                                {},
			"rpm":                                {},
			"api_key":                            {},
			"api_base":                           {},
			"api_version":                        {},
			"reasoning_effort":                   {},
			"thinking":                           {},
			"aws_access_key_id":                  {},
			"aws_secret_access_key":              {},
			"aws_region_name":                    {},
			"aws_session_name":                   {},
			"aws_role_name":                      {},
			"vertex_project":                     {},
			"vertex_location":                    {},
			"vertex_credentials":                 {},
			"litellm_credential_name":            {},
			"input_cost_per_pixel":               {},
			"output_cost_per_pixel":              {},
			"input_cost_per_second":              {},
			"output_cost_per_second":             {},
		}

		// Build a set of keys the user configured in additional_litellm_params.
		// During normal Read we only read back keys that exist in the prior state
		// to avoid "new element appeared" errors when the API returns defaults
		// (e.g. merge_reasoning_content_in_choices) that weren't in the config.
		// During Import (state is null/unknown) we read ALL non-known params so that
		// the imported resource captures the full API state.
		filterByState := !data.AdditionalLiteLLMParams.IsNull() && !data.AdditionalLiteLLMParams.IsUnknown()
		stateKeys := make(map[string]struct{})
		if filterByState {
			for k := range data.AdditionalLiteLLMParams.Elements() {
				stateKeys[k] = struct{}{}
			}
		}

		additionalParams := make(map[string]attr.Value)
		for key, rawValue := range litellmParams {
			if _, isKnown := knownLiteLLMParams[key]; isKnown {
				continue
			}
			// Only filter by state keys during normal Read (not Import).
			// This prevents API-added defaults from causing drift.
			if filterByState {
				if _, inState := stateKeys[key]; !inState {
					continue
				}
			}

			switch v := rawValue.(type) {
			case string:
				additionalParams[key] = types.StringValue(v)
			case bool:
				additionalParams[key] = types.StringValue(strconv.FormatBool(v))
			case float64:
				additionalParams[key] = types.StringValue(strconv.FormatFloat(v, 'f', -1, 64))
			case int:
				additionalParams[key] = types.StringValue(strconv.Itoa(v))
			case int64:
				additionalParams[key] = types.StringValue(strconv.FormatInt(v, 10))
			default:
				// Arrays, objects, and other complex types — serialize back to JSON string.
				if jsonBytes, err := json.Marshal(v); err == nil {
					additionalParams[key] = types.StringValue(string(jsonBytes))
				}
			}
		}

		// Set additional_litellm_params from API response to detect drift
		// for keys that the user configured.
		data.AdditionalLiteLLMParams, _ = types.MapValue(types.StringType, additionalParams)
	} else {
		data.AdditionalLiteLLMParams, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	modelInfo, hasModelInfo := result["model_info"].(map[string]interface{})
	if hasModelInfo {
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
		// The API may not echo back access_groups, so only update if the API
		// actually returns them. If the API is silent, preserve the current value.
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
		} else if data.AccessGroups.IsUnknown() {
			// Resolve unknown to empty list
			data.AccessGroups, _ = types.ListValue(types.StringType, []attr.Value{})
		}
		// If the API didn't return access_groups and we already have a concrete
		// value (from config/state), leave it as-is.
	} else if data.AccessGroups.IsUnknown() {
		data.AccessGroups, _ = types.ListValue(types.StringType, []attr.Value{})
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

	// Build litellm_params for the patch request.
	// NOTE: LiteLLM PATCH API merges litellm_params (via dict.update), it does not replace them.
	// Parameters removed from config will NOT be removed from the API.
	// To fully remove a parameter, the model must be recreated (e.g. terraform apply -replace=...).
	litellmParams := map[string]interface{}{
		"custom_llm_provider": customLLMProvider,
		"model":               modelName,
	}

	// Add cost parameters
	if !data.InputCostPerMillionTokens.IsNull() && !data.InputCostPerMillionTokens.IsUnknown() {
		litellmParams["input_cost_per_token"] = data.InputCostPerMillionTokens.ValueFloat64() / 1000000.0
	}
	if !data.OutputCostPerMillionTokens.IsNull() && !data.OutputCostPerMillionTokens.IsUnknown() {
		litellmParams["output_cost_per_token"] = data.OutputCostPerMillionTokens.ValueFloat64() / 1000000.0
	}

	// Add optional parameters
	if !data.TPM.IsNull() && !data.TPM.IsUnknown() && data.TPM.ValueInt64() > 0 {
		litellmParams["tpm"] = data.TPM.ValueInt64()
	}
	if !data.RPM.IsNull() && !data.RPM.IsUnknown() && data.RPM.ValueInt64() > 0 {
		litellmParams["rpm"] = data.RPM.ValueInt64()
	}
	if !data.ModelAPIKey.IsNull() && !data.ModelAPIKey.IsUnknown() && data.ModelAPIKey.ValueString() != "" {
		litellmParams["api_key"] = data.ModelAPIKey.ValueString()
	}
	if !data.ModelAPIBase.IsNull() && !data.ModelAPIBase.IsUnknown() && data.ModelAPIBase.ValueString() != "" {
		litellmParams["api_base"] = data.ModelAPIBase.ValueString()
	}
	if !data.APIVersion.IsNull() && !data.APIVersion.IsUnknown() && data.APIVersion.ValueString() != "" {
		litellmParams["api_version"] = data.APIVersion.ValueString()
	}
	if !data.ReasoningEffort.IsNull() && !data.ReasoningEffort.IsUnknown() && data.ReasoningEffort.ValueString() != "" {
		litellmParams["reasoning_effort"] = data.ReasoningEffort.ValueString()
	}
	if !data.MergeReasoningContentInChoices.IsNull() && !data.MergeReasoningContentInChoices.IsUnknown() {
		litellmParams["merge_reasoning_content_in_choices"] = data.MergeReasoningContentInChoices.ValueBool()
	}

	// Thinking configuration
	if !data.ThinkingEnabled.IsNull() && !data.ThinkingEnabled.IsUnknown() && data.ThinkingEnabled.ValueBool() {
		litellmParams["thinking"] = map[string]interface{}{
			"type":          "enabled",
			"budget_tokens": data.ThinkingBudgetTokens.ValueInt64(),
		}
	}

	// AWS parameters
	if !data.AWSAccessKeyID.IsNull() && !data.AWSAccessKeyID.IsUnknown() && data.AWSAccessKeyID.ValueString() != "" {
		litellmParams["aws_access_key_id"] = data.AWSAccessKeyID.ValueString()
	}
	if !data.AWSSecretAccessKey.IsNull() && !data.AWSSecretAccessKey.IsUnknown() && data.AWSSecretAccessKey.ValueString() != "" {
		litellmParams["aws_secret_access_key"] = data.AWSSecretAccessKey.ValueString()
	}
	if !data.AWSRegionName.IsNull() && !data.AWSRegionName.IsUnknown() && data.AWSRegionName.ValueString() != "" {
		litellmParams["aws_region_name"] = data.AWSRegionName.ValueString()
	}
	if !data.AWSSessionName.IsNull() && !data.AWSSessionName.IsUnknown() && data.AWSSessionName.ValueString() != "" {
		litellmParams["aws_session_name"] = data.AWSSessionName.ValueString()
	}
	if !data.AWSRoleName.IsNull() && !data.AWSRoleName.IsUnknown() && data.AWSRoleName.ValueString() != "" {
		litellmParams["aws_role_name"] = data.AWSRoleName.ValueString()
	}

	// Vertex parameters
	if !data.VertexProject.IsNull() && !data.VertexProject.IsUnknown() && data.VertexProject.ValueString() != "" {
		litellmParams["vertex_project"] = data.VertexProject.ValueString()
	}
	if !data.VertexLocation.IsNull() && !data.VertexLocation.IsUnknown() && data.VertexLocation.ValueString() != "" {
		litellmParams["vertex_location"] = data.VertexLocation.ValueString()
	}
	if !data.VertexCredentials.IsNull() && !data.VertexCredentials.IsUnknown() && data.VertexCredentials.ValueString() != "" {
		litellmParams["vertex_credentials"] = data.VertexCredentials.ValueString()
	}

	// Credential reference
	if !data.LiteLLMCredentialName.IsNull() && !data.LiteLLMCredentialName.IsUnknown() && data.LiteLLMCredentialName.ValueString() != "" {
		litellmParams["litellm_credential_name"] = data.LiteLLMCredentialName.ValueString()
	}

	// Cost per pixel/second
	if !data.InputCostPerPixel.IsNull() && !data.InputCostPerPixel.IsUnknown() {
		litellmParams["input_cost_per_pixel"] = data.InputCostPerPixel.ValueFloat64()
	}
	if !data.OutputCostPerPixel.IsNull() && !data.OutputCostPerPixel.IsUnknown() {
		litellmParams["output_cost_per_pixel"] = data.OutputCostPerPixel.ValueFloat64()
	}
	if !data.InputCostPerSecond.IsNull() && !data.InputCostPerSecond.IsUnknown() {
		litellmParams["input_cost_per_second"] = data.InputCostPerSecond.ValueFloat64()
	}
	if !data.OutputCostPerSecond.IsNull() && !data.OutputCostPerSecond.IsUnknown() {
		litellmParams["output_cost_per_second"] = data.OutputCostPerSecond.ValueFloat64()
	}

	// Add additional_litellm_params to the request.
	// NOTE: LiteLLM PATCH API merges litellm_params (via dict.update), it does not replace them.
	// Parameters removed from config will NOT be removed from the API.
	// To fully remove a parameter, the model must be recreated (e.g. terraform apply -replace=...).
	if !data.AdditionalLiteLLMParams.IsNull() && !data.AdditionalLiteLLMParams.IsUnknown() {
		elements := make(map[string]string)
		data.AdditionalLiteLLMParams.ElementsAs(ctx, &elements, false)
		for key, value := range elements {
			litellmParams[key] = convertStringValue(value)
		}
	}

	// Build model_info for the PATCH request
	modelInfo := map[string]interface{}{
		"base_model": baseModel,
	}

	// Only add optional model_info fields if they have values
	if !data.Tier.IsNull() && !data.Tier.IsUnknown() && data.Tier.ValueString() != "" {
		modelInfo["tier"] = data.Tier.ValueString()
	}
	if !data.Mode.IsNull() && !data.Mode.IsUnknown() && data.Mode.ValueString() != "" {
		modelInfo["mode"] = data.Mode.ValueString()
	}
	if !data.TeamID.IsNull() && !data.TeamID.IsUnknown() && data.TeamID.ValueString() != "" {
		modelInfo["team_id"] = data.TeamID.ValueString()
	}

	// Add access_groups to model_info if specified
	if !data.AccessGroups.IsNull() && !data.AccessGroups.IsUnknown() {
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

// convertStringValue converts a string to its most appropriate Go type.
// This allows additional_litellm_params values (which are stored as strings in
// Terraform state) to be sent as native JSON types in the API request.
func convertStringValue(s string) interface{} {
	// Try integer
	if intVal, err := strconv.ParseInt(s, 10, 64); err == nil {
		return intVal
	}
	// Try float
	if floatVal, err := strconv.ParseFloat(s, 64); err == nil {
		return floatVal
	}
	// Try boolean
	if boolVal, err := strconv.ParseBool(s); err == nil {
		return boolVal
	}
	// Try JSON (arrays and objects)
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
		var parsed interface{}
		if err := json.Unmarshal([]byte(s), &parsed); err == nil {
			return parsed
		}
	}
	return s
}
