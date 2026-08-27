package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &ModelResource{}
var _ resource.ResourceWithImportState = &ModelResource{}
var _ resource.ResourceWithModifyPlan = &ModelResource{}

func NewModelResource() resource.Resource {
	return &ModelResource{}
}

// ModelResource defines the resource implementation.
type ModelResource struct {
	client *Client
}

type modelReadOwnership struct {
	imported                          bool
	importedFields                    map[string]struct{}
	topThinkingOwned                  bool
	additionalModelInfoJSONProvenance semanticDictionaryProvenance
	clearedFields                     map[string]struct{}
	durablyClearedFields              map[string]struct{}
	freshConnection                   bool
}

// ModelResourceModel describes the resource data model.
type ModelResourceModel struct {
	ID                                types.String  `tfsdk:"id"`
	ModelName                         types.String  `tfsdk:"model_name"`
	CustomLLMProvider                 types.String  `tfsdk:"custom_llm_provider"`
	TPM                               types.Int64   `tfsdk:"tpm"`
	RPM                               types.Int64   `tfsdk:"rpm"`
	ReasoningEffort                   types.String  `tfsdk:"reasoning_effort"`
	ThinkingEnabled                   types.Bool    `tfsdk:"thinking_enabled"`
	ThinkingBudgetTokens              types.Int64   `tfsdk:"thinking_budget_tokens"`
	MergeReasoningContentInChoices    types.Bool    `tfsdk:"merge_reasoning_content_in_choices"`
	ModelAPIKey                       types.String  `tfsdk:"model_api_key"`
	ModelAPIBase                      types.String  `tfsdk:"model_api_base"`
	APIVersion                        types.String  `tfsdk:"api_version"`
	BaseModel                         types.String  `tfsdk:"base_model"`
	Tier                              types.String  `tfsdk:"tier"`
	TeamID                            types.String  `tfsdk:"team_id"`
	Mode                              types.String  `tfsdk:"mode"`
	LiteLLMCredentialName             types.String  `tfsdk:"litellm_credential_name"`
	InputCostPerMillionTokens         types.Float64 `tfsdk:"input_cost_per_million_tokens"`
	OutputCostPerMillionTokens        types.Float64 `tfsdk:"output_cost_per_million_tokens"`
	InputCostPerPixel                 types.Float64 `tfsdk:"input_cost_per_pixel"`
	OutputCostPerPixel                types.Float64 `tfsdk:"output_cost_per_pixel"`
	InputCostPerSecond                types.Float64 `tfsdk:"input_cost_per_second"`
	OutputCostPerSecond               types.Float64 `tfsdk:"output_cost_per_second"`
	AWSAccessKeyID                    types.String  `tfsdk:"aws_access_key_id"`
	AWSSecretAccessKey                types.String  `tfsdk:"aws_secret_access_key"`
	AWSRegionName                     types.String  `tfsdk:"aws_region_name"`
	AWSSessionName                    types.String  `tfsdk:"aws_session_name"`
	AWSRoleName                       types.String  `tfsdk:"aws_role_name"`
	VertexProject                     types.String  `tfsdk:"vertex_project"`
	VertexLocation                    types.String  `tfsdk:"vertex_location"`
	VertexCredentials                 types.String  `tfsdk:"vertex_credentials"`
	AccessGroups                      types.List    `tfsdk:"access_groups"`
	AdditionalLiteLLMParams           types.Map     `tfsdk:"additional_litellm_params"`
	AdditionalLiteLLMParamsConfigured types.Bool    `tfsdk:"additional_litellm_params_configured"`
	AdditionalModelInfo               types.Map     `tfsdk:"additional_model_info"`
	AdditionalModelInfoJSON           types.String  `tfsdk:"additional_model_info_json"`
	AdditionalModelInfoConfigured     types.Bool    `tfsdk:"additional_model_info_configured"`
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
				Computed:    true,
			},
			"rpm": schema.Int64Attribute{
				Description: "Requests per minute limit.",
				Optional:    true,
				Computed:    true,
			},
			"reasoning_effort": schema.StringAttribute{
				Description: "Reasoning effort level (low, medium, high).",
				Optional:    true,
				Computed:    true,
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
				Computed:    true,
			},
			"api_version": schema.StringAttribute{
				Description: "API version (e.g., for Azure OpenAI).",
				Optional:    true,
				Computed:    true,
			},
			"base_model": schema.StringAttribute{
				Description: "The base model name from the provider.",
				Required:    true,
			},
			"tier": schema.StringAttribute{
				Description: "Model tier: free or paid.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("free"),
				Validators: []validator.String{
					stringvalidator.OneOf("free", "paid"),
				},
			},
			"team_id": schema.StringAttribute{
				Description: "Team ID to associate with this model.",
				Optional:    true,
				Computed:    true,
			},
			"mode": schema.StringAttribute{
				Description: "Model mode. Supported values: chat, completion, embedding, audio_speech, audio_transcription, image_generation, video_generation, batch, rerank, realtime, responses, ocr, moderation.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"litellm_credential_name": schema.StringAttribute{
				Description: "Name of the credential to use for this model. References a credential created via litellm_credential resource.",
				Optional:    true,
				Computed:    true,
			},
			"input_cost_per_million_tokens": schema.Float64Attribute{
				Description: "Input cost per million tokens.",
				Optional:    true,
				Computed:    true,
			},
			"output_cost_per_million_tokens": schema.Float64Attribute{
				Description: "Output cost per million tokens.",
				Optional:    true,
				Computed:    true,
			},
			"input_cost_per_pixel": schema.Float64Attribute{
				Description: "Input cost per pixel.",
				Optional:    true,
				Computed:    true,
			},
			"output_cost_per_pixel": schema.Float64Attribute{
				Description: "Output cost per pixel.",
				Optional:    true,
				Computed:    true,
			},
			"input_cost_per_second": schema.Float64Attribute{
				Description: "Input cost per second.",
				Optional:    true,
				Computed:    true,
			},
			"output_cost_per_second": schema.Float64Attribute{
				Description: "Output cost per second.",
				Optional:    true,
				Computed:    true,
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
				Computed:    true,
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
				PlanModifiers: []planmodifier.List{
					listplanmodifier.UseStateForUnknown(),
				},
			},
			"additional_litellm_params": schema.MapAttribute{
				Description: "Additional parameters to pass to litellm_params. Removing configured keys replaces the model so LiteLLM cannot silently retain them.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Validators: []validator.Map{
					mapvalidator.NoNullValues(),
				},
				PlanModifiers: []planmodifier.Map{
					modelAdditionalParamsRemovalModifier{},
				},
			},
			"additional_litellm_params_configured": schema.BoolAttribute{
				Description: "Internal state marker indicating whether additional_litellm_params is explicitly managed by configuration.",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					modelAdditionalParamsOwnershipModifier{},
				},
			},
			"additional_model_info": schema.MapAttribute{
				Description: "Additional fields to store in model_info, e.g. capability flags " +
					"(supports_vision, supports_function_calling, supports_reasoning, …) for models " +
					"missing from LiteLLM's model cost map. Values are strings and are converted to " +
					"native JSON types (int, float, bool, JSON) for the API. Only keys configured " +
					"here are managed; fields LiteLLM derives from its model cost map are left alone.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Validators: []validator.Map{
					mapvalidator.NoNullValues(),
					modelInfoReservedKeysValidator{},
				},
				PlanModifiers: []planmodifier.Map{
					modelAdditionalModelInfoRemovalModifier{},
				},
			},
			"additional_model_info_json": schema.StringAttribute{
				Description: "Sensitive lossless JSON-object sibling for heterogeneous model_info fields. Keys cannot overlap additional_model_info or fields managed by dedicated attributes. Any change replaces the model so LiteLLM cannot retain removed nested values.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				Validators: []validator.String{
					modelSemanticDictionaryValidator{},
				},
			},
			"additional_model_info_configured": schema.BoolAttribute{
				Description: "Internal state marker indicating whether additional_model_info is explicitly managed by configuration.",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					modelAdditionalModelInfoOwnershipModifier{},
				},
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

func (r *ModelResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}

	var plan, state, config ModelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	marker, diagnostics := req.Private.GetKey(ctx, modelImportedFieldsPrivateKey)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	importedFields := decodeModelImportedFields(marker)
	semanticMarker, diagnostics := req.Private.GetKey(ctx, modelAdditionalModelInfoJSONProvenancePrivateKey)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	semanticProvenance, err := decodeModelAdditionalModelInfoJSONProvenance(ctx, semanticMarker, state.AdditionalModelInfoJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("additional_model_info_json"),
			"Invalid Semantic Dictionary Provenance",
			"Private ownership state is missing or malformed. No model plan was produced.",
		)
		return
	}
	if !config.AdditionalModelInfoJSON.IsNull() && !config.AdditionalModelInfoJSON.IsUnknown() {
		if _, _, err := modelAdditionalModelInfoJSONConfiguration(ctx, config.AdditionalModelInfoJSON, config.AdditionalModelInfo); err != nil {
			resp.Diagnostics.AddAttributeError(
				path.Root("additional_model_info_json"),
				"Invalid Semantic Model Information",
				"The JSON object is malformed or overlaps another managed model information surface. No model plan was produced.",
			)
			return
		}
	}
	semanticReplace, err := modelAdditionalModelInfoJSONNeedsReplacement(ctx, config.AdditionalModelInfoJSON, state.AdditionalModelInfoJSON, semanticProvenance)
	if err != nil {
		resp.Diagnostics.AddAttributeError(
			path.Root("additional_model_info_json"),
			"Invalid Semantic Model Information",
			"The JSON object or its ownership state could not be compared safely. No model plan was produced.",
		)
		return
	}
	if !semanticProvenance.Configured && config.AdditionalModelInfoJSON.IsNull() {
		plan.AdditionalModelInfoJSON = types.StringNull()
	}
	if semanticReplace {
		if config.AdditionalModelInfoJSON.IsNull() {
			plan.AdditionalModelInfoJSON = types.StringUnknown()
		}
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("additional_model_info_json"))
	}
	forceOwnershipUpdate := false

	stringFields := []struct {
		name                string
		plan, state, config *types.String
	}{
		{"model_api_base", &plan.ModelAPIBase, &state.ModelAPIBase, &config.ModelAPIBase},
		{"api_version", &plan.APIVersion, &state.APIVersion, &config.APIVersion},
		{"reasoning_effort", &plan.ReasoningEffort, &state.ReasoningEffort, &config.ReasoningEffort},
		{"aws_region_name", &plan.AWSRegionName, &state.AWSRegionName, &config.AWSRegionName},
		{"litellm_credential_name", &plan.LiteLLMCredentialName, &state.LiteLLMCredentialName, &config.LiteLLMCredentialName},
		{"mode", &plan.Mode, &state.Mode, &config.Mode},
		{"team_id", &plan.TeamID, &state.TeamID, &config.TeamID},
	}
	for _, field := range stringFields {
		_, imported := importedFields[field.name]
		if imported {
			if field.config.IsNull() {
				*field.plan = *field.state
				continue
			}
			if field.config.IsUnknown() {
				continue
			}
			delete(importedFields, field.name)
			if field.plan.Equal(*field.state) {
				forceOwnershipUpdate = true
			}
			continue
		}
		if field.config.IsNull() && field.state.IsNull() {
			*field.plan = *field.state
			continue
		}
		if field.config.IsNull() && !field.state.IsNull() && !field.state.IsUnknown() && field.name != "mode" && field.name != "team_id" {
			*field.plan = types.StringNull()
		}
	}

	intFields := []struct {
		name                string
		plan, state, config *types.Int64
	}{
		{"tpm", &plan.TPM, &state.TPM, &config.TPM},
		{"rpm", &plan.RPM, &state.RPM, &config.RPM},
	}
	for _, field := range intFields {
		_, imported := importedFields[field.name]
		if imported {
			if field.config.IsNull() {
				*field.plan = *field.state
				continue
			}
			if field.config.IsUnknown() {
				continue
			}
			delete(importedFields, field.name)
			if field.plan.Equal(*field.state) {
				forceOwnershipUpdate = true
			}
			continue
		}
		if field.config.IsNull() && field.state.IsNull() {
			*field.plan = *field.state
			continue
		}
		if field.config.IsNull() && !field.state.IsNull() && !field.state.IsUnknown() {
			*field.plan = types.Int64Unknown()
		}
	}

	floatFields := []struct {
		name                string
		plan, state, config *types.Float64
	}{
		{"input_cost_per_million_tokens", &plan.InputCostPerMillionTokens, &state.InputCostPerMillionTokens, &config.InputCostPerMillionTokens},
		{"output_cost_per_million_tokens", &plan.OutputCostPerMillionTokens, &state.OutputCostPerMillionTokens, &config.OutputCostPerMillionTokens},
		{"input_cost_per_pixel", &plan.InputCostPerPixel, &state.InputCostPerPixel, &config.InputCostPerPixel},
		{"output_cost_per_pixel", &plan.OutputCostPerPixel, &state.OutputCostPerPixel, &config.OutputCostPerPixel},
		{"input_cost_per_second", &plan.InputCostPerSecond, &state.InputCostPerSecond, &config.InputCostPerSecond},
		{"output_cost_per_second", &plan.OutputCostPerSecond, &state.OutputCostPerSecond, &config.OutputCostPerSecond},
	}
	for _, field := range floatFields {
		_, imported := importedFields[field.name]
		if imported {
			if field.config.IsNull() {
				*field.plan = *field.state
				continue
			}
			if field.config.IsUnknown() {
				continue
			}
			delete(importedFields, field.name)
			if field.plan.Equal(*field.state) {
				forceOwnershipUpdate = true
			}
			continue
		}
		if field.config.IsNull() && field.state.IsNull() {
			*field.plan = *field.state
			continue
		}
		if field.config.IsNull() && !field.state.IsNull() && !field.state.IsUnknown() {
			switch field.name {
			case "input_cost_per_million_tokens", "output_cost_per_million_tokens":
				*field.plan = types.Float64Null()
			default:
				*field.plan = types.Float64Unknown()
			}
		}
	}

	if !config.AdditionalLiteLLMParams.IsNull() && !config.AdditionalLiteLLMParams.IsUnknown() {
		delete(importedFields, "additional_litellm_params.thinking")
		delete(importedFields, "additional_litellm_params.merge_reasoning_content_in_choices")
	}

	if _, imported := importedFields["access_groups"]; imported {
		switch {
		case config.AccessGroups.IsNull():
			plan.AccessGroups = state.AccessGroups
		case !config.AccessGroups.IsUnknown():
			delete(importedFields, "access_groups")
			if plan.AccessGroups.Equal(state.AccessGroups) {
				forceOwnershipUpdate = true
			}
		}
	}

	// LiteLLM v1.98 merges these fields and has no semantic clear sentinel.
	// Replacement is safer than publishing null state while the old limit,
	// inferred mode, or non-token pricing remains active.
	for _, field := range []struct {
		name   string
		remove bool
	}{
		{"tpm", config.TPM.IsNull() && !state.TPM.IsNull() && !state.TPM.IsUnknown()},
		{"rpm", config.RPM.IsNull() && !state.RPM.IsNull() && !state.RPM.IsUnknown()},
		{"input_cost_per_pixel", config.InputCostPerPixel.IsNull() && !state.InputCostPerPixel.IsNull() && !state.InputCostPerPixel.IsUnknown()},
		{"output_cost_per_pixel", config.OutputCostPerPixel.IsNull() && !state.OutputCostPerPixel.IsNull() && !state.OutputCostPerPixel.IsUnknown()},
		{"input_cost_per_second", config.InputCostPerSecond.IsNull() && !state.InputCostPerSecond.IsNull() && !state.InputCostPerSecond.IsUnknown()},
		{"output_cost_per_second", config.OutputCostPerSecond.IsNull() && !state.OutputCostPerSecond.IsNull() && !state.OutputCostPerSecond.IsUnknown()},
		{"mode", config.Mode.IsNull() && !state.Mode.IsNull() && !state.Mode.IsUnknown()},
	} {
		_, imported := importedFields[field.name]
		if field.remove && !imported {
			if field.name == "mode" {
				// Optional+Computed would otherwise retain state, leaving no public
				// diff for Terraform to attach replacement to.
				plan.Mode = types.StringUnknown()
			}
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root(field.name))
		}
	}
	if _, imported := importedFields["team_id"]; !imported && config.TeamID.IsNull() && !state.TeamID.IsNull() && !state.TeamID.IsUnknown() {
		plan.TeamID = types.StringUnknown()
	}
	if !plan.TeamID.Equal(state.TeamID) {
		if _, imported := importedFields["team_id"]; !imported {
			resp.RequiresReplace = append(resp.RequiresReplace, path.Root("team_id"))
		}
	}

	// Optional+Computed collections otherwise retain prior state when omitted.
	// A non-imported, previously configured value is owned and omission clears it.
	if config.AccessGroups.IsNull() && !state.AccessGroups.IsNull() && !state.AccessGroups.IsUnknown() && len(state.AccessGroups.Elements()) > 0 {
		if _, imported := importedFields["access_groups"]; !imported {
			empty, diagnostics := checkedStringListValue(ctx, nil, path.Root("access_groups"))
			resp.Diagnostics.Append(diagnostics...)
			if resp.Diagnostics.HasError() {
				return
			}
			plan.AccessGroups = empty
		}
	}

	if forceOwnershipUpdate {
		plan.ID = types.StringUnknown()
	}
	semanticRaw, err := encodeModelAdditionalModelInfoJSONProvenance(ctx, semanticProvenance)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("additional_model_info_json"), "Invalid Semantic Dictionary Provenance", "Private ownership state could not be encoded safely. No model plan was produced.")
		return
	}
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelImportedFieldsPrivateKey, encodeModelImportedFields(importedFields))...)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelAdditionalModelInfoJSONProvenancePrivateKey, semanticRaw)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *ModelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ModelResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var configuredThinkingEnabled types.Bool
	var configuredThinkingBudget types.Int64
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("thinking_enabled"), &configuredThinkingEnabled)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("thinking_budget_tokens"), &configuredThinkingBudget)...)
	if resp.Diagnostics.HasError() {
		return
	}
	topThinkingOwned := !configuredThinkingEnabled.IsNull() || !configuredThinkingBudget.IsNull()

	var configuredAdditionalParams types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("additional_litellm_params"), &configuredAdditionalParams)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !configuredAdditionalParams.IsNull() && !configuredAdditionalParams.IsUnknown() {
		data.AdditionalLiteLLMParams = configuredAdditionalParams
	}
	data.AdditionalLiteLLMParamsConfigured = types.BoolValue(!configuredAdditionalParams.IsNull() && !configuredAdditionalParams.IsUnknown())

	var configuredModelInfo types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("additional_model_info"), &configuredModelInfo)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !configuredModelInfo.IsNull() && !configuredModelInfo.IsUnknown() {
		data.AdditionalModelInfo = configuredModelInfo
	}
	data.AdditionalModelInfoConfigured = types.BoolValue(!configuredModelInfo.IsNull() && !configuredModelInfo.IsUnknown())

	var configuredModelInfoJSON types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("additional_model_info_json"), &configuredModelInfoJSON)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if configuredModelInfoJSON.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("additional_model_info_json"), "Unknown Semantic Model Information", "The JSON object must be known before creating a model.")
		return
	}
	data.AdditionalModelInfoJSON = configuredModelInfoJSON
	_, semanticProvenance, err := modelAdditionalModelInfoJSONConfiguration(ctx, configuredModelInfoJSON, configuredModelInfo)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("additional_model_info_json"), "Invalid Semantic Model Information", "The JSON object is malformed or overlaps another managed model information surface. No model request was sent.")
		return
	}
	semanticRaw, err := encodeModelAdditionalModelInfoJSONProvenance(ctx, semanticProvenance)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("additional_model_info_json"), "Invalid Semantic Dictionary Provenance", "Private ownership state could not be encoded safely. No model request was sent.")
		return
	}
	resp.Diagnostics.Append(validateModelRequestCollections(ctx, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Normalise numeric strings in string-map attributes so that planned values
	// use the same canonical form as their API read-back values.
	data.AdditionalLiteLLMParams = normalizeAdditionalParams(ctx, data.AdditionalLiteLLMParams)
	data.AdditionalModelInfo = normalizeAdditionalParams(ctx, data.AdditionalModelInfo)

	modelID := uuid.New().String()

	if err := r.createOrUpdateModel(ctx, &data, modelID, false); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create model: %s", err))
		return
	}

	data.ID = types.StringValue(modelID)

	planned := data

	// Read back to ensure consistency using the same ownership that built the
	// request. Additional thinking still wins when both forms are configured.
	ownership := modelReadOwnership{
		topThinkingOwned:                  topThinkingOwned,
		additionalModelInfoJSONProvenance: semanticProvenance,
	}
	if err := r.readModelWithRetryOwnership(ctx, &data, 8, ownership); err != nil {
		if finalizeErr := finalizeModelComputedDefaults(ctx, &data); finalizeErr != nil {
			resp.Diagnostics.AddError("Model State Projection Failed", finalizeErr.Error())
			return
		}
		reassertPlannedCosts(&data, &planned)
		if semanticProvenance.Configured && errors.Is(err, errModelSemanticDictionaryProjection) {
			if resp.Private != nil {
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelAdditionalModelInfoJSONProvenancePrivateKey, semanticRaw)...)
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelTopThinkingOwnedPrivateKey, []byte(strconv.FormatBool(topThinkingOwned)))...)
				if resp.Diagnostics.HasError() {
					return
				}
			}
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			resp.Diagnostics.AddAttributeError(
				path.Root("additional_model_info_json"),
				"Semantic Model Information Not Confirmed",
				"LiteLLM created the model but did not return the complete owned JSON shape required for confirmation. Terraform retained the complete planned state and ownership so a later refresh can reconcile safely.",
			)
			return
		}
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Model created but failed to read back: %s", err))
	}
	reassertPlannedCosts(&data, &planned)

	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelAdditionalModelInfoJSONProvenancePrivateKey, semanticRaw)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelTopThinkingOwnedPrivateKey, []byte(strconv.FormatBool(topThinkingOwned)))...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ModelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ModelResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	priorAdditionalParams := data.AdditionalLiteLLMParams
	priorModelInfo := data.AdditionalModelInfo
	importedMarker, privateDiags := req.Private.GetKey(ctx, modelImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	importedFieldsMarker, importedFieldsDiags := req.Private.GetKey(ctx, modelImportedFieldsPrivateKey)
	resp.Diagnostics.Append(importedFieldsDiags...)
	topThinkingMarker, topThinkingDiags := req.Private.GetKey(ctx, modelTopThinkingOwnedPrivateKey)
	resp.Diagnostics.Append(topThinkingDiags...)
	semanticMarker, semanticDiags := req.Private.GetKey(ctx, modelAdditionalModelInfoJSONProvenancePrivateKey)
	resp.Diagnostics.Append(semanticDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	semanticProvenance, err := decodeModelAdditionalModelInfoJSONProvenance(ctx, semanticMarker, data.AdditionalModelInfoJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("additional_model_info_json"), "Invalid Semantic Dictionary Provenance", "Private ownership state is missing or malformed. No model read was performed.")
		return
	}
	imported := string(importedMarker) == "true"
	topThinkingOwned := string(topThinkingMarker) == "true"
	if len(topThinkingMarker) == 0 && !imported {
		// Backward compatibility for state written before private ownership was
		// recorded. Enabled top-level thinking was necessarily request-owned.
		topThinkingOwned = !data.ThinkingEnabled.IsNull() && !data.ThinkingEnabled.IsUnknown() && data.ThinkingEnabled.ValueBool()
	}

	importedFields := decodeModelImportedFields(importedFieldsMarker)
	err = r.readModelWithRetryOwnership(ctx, &data, 8, modelReadOwnership{
		imported:                          imported,
		importedFields:                    importedFields,
		topThinkingOwned:                  topThinkingOwned,
		additionalModelInfoJSONProvenance: semanticProvenance,
	})
	if err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read model: %s", err))
		return
	}

	if data.AdditionalLiteLLMParamsConfigured.IsNull() || data.AdditionalLiteLLMParamsConfigured.IsUnknown() {
		configured := len(inferLegacyConfiguredAdditionalParamKeys(priorAdditionalParams)) > 0
		if imported {
			configured = false
		}
		data.AdditionalLiteLLMParamsConfigured = types.BoolValue(configured)
	}
	if data.AdditionalModelInfoConfigured.IsNull() || data.AdditionalModelInfoConfigured.IsUnknown() {
		configured := len(configuredAdditionalParamKeys(priorModelInfo)) > 0
		if imported {
			configured = false
		}
		data.AdditionalModelInfoConfigured = types.BoolValue(configured)
	}

	semanticRaw, semanticErr := encodeModelAdditionalModelInfoJSONProvenance(ctx, semanticProvenance)
	if semanticErr != nil {
		resp.Diagnostics.AddAttributeError(path.Root("additional_model_info_json"), "Invalid Semantic Dictionary Provenance", "Private ownership state could not be encoded safely. No model state was produced.")
		return
	}
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelTopThinkingOwnedPrivateKey, []byte(strconv.FormatBool(topThinkingOwned)))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelAdditionalModelInfoJSONProvenancePrivateKey, semanticRaw)...)
		if imported {
			fields := importedFields
			for name := range modelImportedFieldsFromState(data) {
				fields[name] = struct{}{}
			}
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelImportedFieldsPrivateKey, encodeModelImportedFields(fields))...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelImportedPrivateKey, nil)...)
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ModelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data ModelResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var configuredThinkingEnabled types.Bool
	var configuredThinkingBudget types.Int64
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("thinking_enabled"), &configuredThinkingEnabled)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("thinking_budget_tokens"), &configuredThinkingBudget)...)
	if resp.Diagnostics.HasError() {
		return
	}
	topThinkingOwned := !configuredThinkingEnabled.IsNull() || !configuredThinkingBudget.IsNull()

	var configuredAdditionalParams types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("additional_litellm_params"), &configuredAdditionalParams)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.AdditionalLiteLLMParamsConfigured = types.BoolValue(!configuredAdditionalParams.IsNull() && !configuredAdditionalParams.IsUnknown())

	var configuredModelInfo types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("additional_model_info"), &configuredModelInfo)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.AdditionalModelInfoConfigured = types.BoolValue(!configuredModelInfo.IsNull() && !configuredModelInfo.IsUnknown())

	var configuredModelInfoJSON types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("additional_model_info_json"), &configuredModelInfoJSON)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if configuredModelInfoJSON.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("additional_model_info_json"), "Unknown Semantic Model Information", "The JSON object must be known before updating a model.")
		return
	}
	data.AdditionalModelInfoJSON = configuredModelInfoJSON
	_, semanticProvenance, err := modelAdditionalModelInfoJSONConfiguration(ctx, configuredModelInfoJSON, configuredModelInfo)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("additional_model_info_json"), "Invalid Semantic Model Information", "The JSON object is malformed or overlaps another managed model information surface. No model request was sent.")
		return
	}

	resp.Diagnostics.Append(validateModelRequestCollections(ctx, data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Normalise numeric strings in string-map attributes so that planned values
	// use the same canonical form as their API read-back values.
	data.AdditionalLiteLLMParams = normalizeAdditionalParams(ctx, data.AdditionalLiteLLMParams)
	data.AdditionalModelInfo = normalizeAdditionalParams(ctx, data.AdditionalModelInfo)

	var state ModelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	priorThinkingMarker, priorThinkingDiags := req.Private.GetKey(ctx, modelTopThinkingOwnedPrivateKey)
	resp.Diagnostics.Append(priorThinkingDiags...)
	importedFieldsMarker, importedFieldsDiags := req.Private.GetKey(ctx, modelImportedFieldsPrivateKey)
	resp.Diagnostics.Append(importedFieldsDiags...)
	semanticMarker, semanticDiags := req.Private.GetKey(ctx, modelAdditionalModelInfoJSONProvenancePrivateKey)
	resp.Diagnostics.Append(semanticDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := decodeModelAdditionalModelInfoJSONProvenance(ctx, semanticMarker, state.AdditionalModelInfoJSON); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("additional_model_info_json"), "Invalid Semantic Dictionary Provenance", "Private ownership state is missing or malformed. No model request was sent.")
		return
	}
	semanticRaw, err := encodeModelAdditionalModelInfoJSONProvenance(ctx, semanticProvenance)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("additional_model_info_json"), "Invalid Semantic Dictionary Provenance", "Private ownership state could not be encoded safely. No model request was sent.")
		return
	}
	priorTopThinkingOwned := string(priorThinkingMarker) == "true"
	if len(priorThinkingMarker) == 0 {
		priorTopThinkingOwned = !state.ThinkingEnabled.IsNull() && !state.ThinkingEnabled.IsUnknown() && state.ThinkingEnabled.ValueBool()
	}

	data.ID = state.ID
	plannedData := data
	clearedFields := modelClearedFields(plannedData, state, priorTopThinkingOwned)
	patchVerifiedClears, readbackClears := partitionModelClears(clearedFields)
	var durablyClearedFields map[string]struct{}

	// A configured-equal imported field uses an unknown ID only to force a
	// read-backed ownership transition. Avoid a mutation when no API field changed.
	apiFieldsChanged, err := modelAPIFieldsChanged(ctx, data, state)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("additional_model_info_json"), "Invalid Semantic Model Information", "The JSON object could not be compared safely. No model request was sent.")
		return
	}
	if apiFieldsChanged {
		patchResult, err := r.patchModel(ctx, &data, &state, topThinkingOwned, priorTopThinkingOwned)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update model: %s", err))
			return
		}
		if len(patchVerifiedClears) > 0 {
			if err := verifyModelPatchClears(patchResult, patchVerifiedClears); err != nil {
				resp.Diagnostics.AddError("Model Clear Not Confirmed", err.Error())
				return
			}
			durablyClearedFields = patchVerifiedClears
		}
	}

	ownership := modelReadOwnership{
		importedFields:                    decodeModelImportedFields(importedFieldsMarker),
		topThinkingOwned:                  topThinkingOwned,
		additionalModelInfoJSONProvenance: semanticProvenance,
		clearedFields:                     readbackClears,
		durablyClearedFields:              durablyClearedFields,
		freshConnection:                   true,
	}
	// v1.98 model reads can lag durable writes across workers for several seconds.
	if err := r.readModelAfterUpdateWithOwnership(ctx, &data, plannedData, state, 24, ownership); err != nil {
		if len(readbackClears) > 0 {
			resp.Diagnostics.AddError("Model Clear Readback Not Yet Consistent", fmt.Sprintf("LiteLLM accepted the model update, but bounded fresh-worker reads did not return the decrypted cleared values before the consistency timeout. Terraform retained prior state so a retry can confirm worker-cache convergence: %s", err))
		} else {
			resp.Diagnostics.AddError("Model Update Not Yet Consistent", fmt.Sprintf("LiteLLM accepted the model update but did not return the planned values before the consistency timeout: %s", err))
		}
		return
	}

	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelAdditionalModelInfoJSONProvenancePrivateKey, semanticRaw)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelTopThinkingOwnedPrivateKey, []byte(strconv.FormatBool(topThinkingOwned)))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelImportedPrivateKey, nil)...)
		if resp.Diagnostics.HasError() {
			return
		}
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
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelImportedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelImportedFieldsPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelTopThinkingOwnedPrivateKey, []byte("false"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, modelAdditionalModelInfoJSONProvenancePrivateKey, nil)...)
	}
}

type modelRequestCollections struct {
	accessGroups                  []string
	additionalLiteLLMParams       map[string]string
	additionalModelInfo           map[string]string
	additionalModelInfoJSON       map[string]interface{}
	additionalModelInfoConfigured bool
}

func convertModelRequestCollections(ctx context.Context, data ModelResourceModel) (modelRequestCollections, diag.Diagnostics) {
	var result modelRequestCollections
	var diagnostics diag.Diagnostics
	var converted diag.Diagnostics
	result.accessGroups, _, converted = strictTerraformStringList(ctx, data.AccessGroups, path.Root("access_groups"))
	diagnostics.Append(converted...)
	result.additionalLiteLLMParams, _, converted = strictTerraformStringMap(ctx, data.AdditionalLiteLLMParams, path.Root("additional_litellm_params"), false)
	diagnostics.Append(converted...)
	result.additionalModelInfo, _, converted = strictTerraformStringMap(ctx, data.AdditionalModelInfo, path.Root("additional_model_info"), false)
	diagnostics.Append(converted...)
	semanticObject, semanticProvenance, semanticErr := modelAdditionalModelInfoJSONConfiguration(ctx, data.AdditionalModelInfoJSON, data.AdditionalModelInfo)
	if semanticErr != nil {
		diagnostics.AddAttributeError(
			path.Root("additional_model_info_json"),
			"Invalid Semantic Model Information",
			"The JSON object is malformed, unknown, or overlaps another managed model information surface.",
		)
	} else {
		result.additionalModelInfoJSON = semanticObject
		result.additionalModelInfoConfigured = semanticProvenance.Configured
	}
	if diagnostics.HasError() {
		return modelRequestCollections{}, diagnostics
	}
	return result, diagnostics
}

func validateModelRequestCollections(ctx context.Context, data ModelResourceModel) diag.Diagnostics {
	_, diagnostics := convertModelRequestCollections(ctx, data)
	return diagnostics
}

func (r *ModelResource) createOrUpdateModel(ctx context.Context, data *ModelResourceModel, modelID string, isUpdate bool) error {
	collections, diagnostics := convertModelRequestCollections(ctx, *data)
	if diagnostics.HasError() {
		return fmt.Errorf("model request collection conversion failed")
	}
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
		for key, value := range collections.additionalLiteLLMParams {
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
		modelInfo["team_public_model_name"] = data.ModelName.ValueString()
	}

	// Add access_groups to model_info if specified
	if !data.AccessGroups.IsNull() {
		if len(collections.accessGroups) > 0 {
			modelInfo["access_groups"] = collections.accessGroups
		}
	}

	// Add additional_model_info to the request. Like additional_litellm_params,
	// values are strings in Terraform but converted to native types for the API.
	if !data.AdditionalModelInfo.IsNull() && !data.AdditionalModelInfo.IsUnknown() {
		for key, value := range collections.additionalModelInfo {
			modelInfo[key] = convertStringValue(value)
		}
	}
	if collections.additionalModelInfoConfigured {
		var overlayErr error
		modelInfo, overlayErr = overlayModelAdditionalModelInfoJSON(ctx, modelInfo, collections.additionalModelInfoJSON)
		if overlayErr != nil {
			return fmt.Errorf("semantic model information overlay failed")
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
	return r.readModelWithOwnership(ctx, data, modelReadOwnership{
		topThinkingOwned: !data.ThinkingEnabled.IsNull() && !data.ThinkingEnabled.IsUnknown() && data.ThinkingEnabled.ValueBool(),
	})
}

func (r *ModelResource) readModelWithOwnership(ctx context.Context, data *ModelResourceModel, ownership modelReadOwnership) error {
	query := url.Values{"litellm_model_id": []string{data.ID.ValueString()}}
	endpoint := endpointWithQuery("/model/info", query)

	var rawResult map[string]interface{}
	var err error
	if ownership.freshConnection {
		err = r.client.doFreshRequestWithResponse(ctx, "GET", endpoint, nil, &rawResult)
	} else {
		err = r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &rawResult)
	}
	if err != nil {
		return err
	}

	// /model/info normally returns {"data": [{...}]}. In v1.98's
	// user_model mode, the same endpoint legitimately returns {"data": {...}}.
	// Normalize both exact envelopes before applying the same identity and
	// required-field authority checks.
	result := rawResult
	dataValue, dataPresent := rawResult["data"]
	switch data := dataValue.(type) {
	case []interface{}:
		if len(data) > 0 {
			if firstItem, ok := data[0].(map[string]interface{}); ok {
				result = firstItem
			}
		}
	case map[string]interface{}:
		if data != nil {
			result = data
		}
	}
	if ownership.imported {
		switch data := dataValue.(type) {
		case []interface{}:
			if len(data) != 1 {
				return fmt.Errorf("model import read response did not contain exactly one model")
			}
			var ok bool
			result, ok = data[0].(map[string]interface{})
			if !ok || result == nil {
				return fmt.Errorf("model import read response contains an invalid model object")
			}
		case map[string]interface{}:
			if data == nil {
				return fmt.Errorf("model import read response contains an invalid model object")
			}
			result = data
		default:
			if !dataPresent {
				return fmt.Errorf("model import read response is missing a required data field")
			}
			return fmt.Errorf("model import read response contains an invalid data field")
		}
		modelInfo, err := requireImportedObjectField(true, "model", result, "model_info")
		if err != nil {
			return err
		}
		if err := validateImportedObjectIdentity(true, "model", modelInfo, "id", data.ID.ValueString()); err != nil {
			return err
		}
		if err := requireImportedStringField(true, "model", modelInfo, "base_model"); err != nil {
			return err
		}
		litellmParams, err := requireImportedObjectField(true, "model", result, "litellm_params")
		if err != nil {
			return err
		}
		if err := requireImportedStringField(true, "model", litellmParams, "custom_llm_provider"); err != nil {
			return err
		}
		if err := requireImportedStringField(true, "model", result, "model_name"); err != nil {
			return err
		}
	}
	original := data
	next := *data
	data = &next

	// Update data from response while preserving sensitive values.
	// For team-scoped models, LiteLLM rewrites top-level model_name to an internal
	// value (model_name_${TEAM_ID}_${GUID}); the user-facing name is in model_info.team_public_model_name.
	if modelInfo, hasModelInfo := result["model_info"].(map[string]interface{}); hasModelInfo {
		if teamID, _ := modelInfo["team_id"].(string); teamID != "" {
			if publicName, ok := modelInfo["team_public_model_name"].(string); ok && publicName != "" {
				data.ModelName = types.StringValue(publicName)
			} else if modelName, ok := result["model_name"].(string); ok && modelName != "" {
				data.ModelName = types.StringValue(modelName)
			}
		} else if modelName, ok := result["model_name"].(string); ok && modelName != "" {
			data.ModelName = types.StringValue(modelName)
		}
	} else if modelName, ok := result["model_name"].(string); ok && modelName != "" {
		data.ModelName = types.StringValue(modelName)
	}

	litellmParams, hasLiteLLMParams := result["litellm_params"].(map[string]interface{})
	modelInfo, hasModelInfo := result["model_info"].(map[string]interface{})
	if len(ownership.clearedFields) > 0 {
		if !hasLiteLLMParams {
			litellmParams = map[string]interface{}{}
		}
		if !hasModelInfo {
			modelInfo = map[string]interface{}{}
		}
		if err := verifyModelClears(litellmParams, modelInfo, ownership.clearedFields); err != nil {
			return err
		}
	}

	if hasLiteLLMParams {
		// Update top-level attributes from API response.
		// For optional attributes (tpm, rpm, merge_reasoning_content_in_choices),
		// only update if the attribute was set in the config (!IsNull).
		// Otherwise, values that exist in API but not in config would cause
		// an infinite plan diff: plan wants to remove → PATCH can't delete
		// (LiteLLM merges litellm_params) → Read sees it again → repeat.
		if provider, ok := litellmParams["custom_llm_provider"].(string); ok && provider != "" {
			data.CustomLLMProvider = types.StringValue(provider)
		}
		for _, field := range []struct {
			name      string
			target    *types.String
			sensitive bool
		}{
			{"api_key", &data.ModelAPIKey, true},
			{"api_base", &data.ModelAPIBase, false},
			{"api_version", &data.APIVersion, false},
			{"reasoning_effort", &data.ReasoningEffort, false},
			{"aws_access_key_id", &data.AWSAccessKeyID, true},
			{"aws_secret_access_key", &data.AWSSecretAccessKey, true},
			{"aws_region_name", &data.AWSRegionName, false},
			{"aws_session_name", &data.AWSSessionName, true},
			{"aws_role_name", &data.AWSRoleName, true},
			{"vertex_project", &data.VertexProject, true},
			{"vertex_location", &data.VertexLocation, true},
			{"vertex_credentials", &data.VertexCredentials, true},
			{"litellm_credential_name", &data.LiteLLMCredentialName, false},
		} {
			if _, cleared := ownership.durablyClearedFields[field.name]; cleared {
				continue
			}
			owned := ownership.imported || (!field.target.IsNull() && !field.target.IsUnknown())
			if err := updateModelStringFromAPI(field.target, litellmParams, field.name, owned, field.sensitive); err != nil {
				return err
			}
		}
		tpmOwned := ownership.imported || (!data.TPM.IsNull() && !data.TPM.IsUnknown())
		if err := updateInt64FromAPI(&data.TPM, litellmParams, tpmOwned, tpmOwned, "tpm"); err != nil {
			return err
		}
		rpmOwned := ownership.imported || (!data.RPM.IsNull() && !data.RPM.IsUnknown())
		if err := updateInt64FromAPI(&data.RPM, litellmParams, rpmOwned, rpmOwned, "rpm"); err != nil {
			return err
		}
		_, additionalThinkingOwned := data.AdditionalLiteLLMParams.Elements()["thinking"]
		thinkingRaw, thinkingPresence, err := apiValueAt(litellmParams, "thinking")
		if err != nil {
			return err
		}
		var thinking map[string]interface{}
		if thinkingPresence == apiValuePresent {
			var ok bool
			thinking, ok = thinkingRaw.(map[string]interface{})
			if !ok {
				return fmt.Errorf("invalid response field %q: expected an object", "thinking")
			}
		}
		// Validate the exact budget even when additional parameters or an
		// unmanaged remote default own the document.
		budget, budgetPresence, err := apiInt64At(litellmParams, "thinking", "budget_tokens")
		if err != nil {
			return err
		}
		// Request construction writes top-level thinking first and additional
		// parameters last. Mirror that precedence: an owned additional key is
		// authoritative while top-level state remains byte-for-byte unchanged.
		_, thinkingDurablyCleared := ownership.durablyClearedFields["thinking"]
		if ownership.topThinkingOwned && !additionalThinkingOwned && !thinkingDurablyCleared {
			enabled := false
			if thinkingPresence == apiValuePresent {
				typeValue, exists := thinking["type"]
				if exists && typeValue != nil {
					typeString, ok := typeValue.(string)
					if !ok {
						return fmt.Errorf("invalid response field %q: expected a string", "thinking.type")
					}
					enabled = typeString == "enabled"
				}
			}
			data.ThinkingEnabled = types.BoolValue(enabled)
			if enabled {
				switch budgetPresence {
				case apiValuePresent:
					data.ThinkingBudgetTokens = types.Int64Value(budget)
				case apiValueNull, apiValueAbsent:
					data.ThinkingBudgetTokens = types.Int64Null()
				}
			}
		}
		// Read back cost attributes. The API stores costs per token while the
		// resource exposes them per million tokens, so scale on the way back.
		// Like tpm/rpm above, only update when the attribute is set in the
		// config (!IsNull) — costs inferred by LiteLLM from its model cost map
		// must not create drift for users who never configured them.
		for _, cost := range []struct {
			name   string
			scale  float64
			target *types.Float64
		}{
			{"input_cost_per_token", 1000000.0, &data.InputCostPerMillionTokens},
			{"output_cost_per_token", 1000000.0, &data.OutputCostPerMillionTokens},
			{"input_cost_per_pixel", 1.0, &data.InputCostPerPixel},
			{"output_cost_per_pixel", 1.0, &data.OutputCostPerPixel},
			{"input_cost_per_second", 1.0, &data.InputCostPerSecond},
			{"output_cost_per_second", 1.0, &data.OutputCostPerSecond},
		} {
			value, err := readBackCostWithOwnership(*cost.target, litellmParams, cost.name, cost.scale, ownership.imported)
			if err != nil {
				return err
			}
			*cost.target = value
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
			"merge_reasoning_content_in_choices": {},
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
		priorStrings := make(map[string]string)
		if filterByState {
			var diagnostics diag.Diagnostics
			priorStrings, _, diagnostics = strictTerraformStringMap(ctx, data.AdditionalLiteLLMParams, path.Root("additional_litellm_params"), true)
			if err := collectionProjectionError(ctx, diagnostics); err != nil {
				return err
			}
			for k := range data.AdditionalLiteLLMParams.Elements() {
				stateKeys[k] = struct{}{}
			}
		}

		additionalParamsOwned := !data.AdditionalLiteLLMParamsConfigured.IsNull() && !data.AdditionalLiteLLMParamsConfigured.IsUnknown() && data.AdditionalLiteLLMParamsConfigured.ValueBool()
		if data.AdditionalLiteLLMParamsConfigured.IsNull() || data.AdditionalLiteLLMParamsConfigured.IsUnknown() {
			additionalParamsOwned = len(inferLegacyConfiguredAdditionalParamKeys(data.AdditionalLiteLLMParams)) > 0
		}
		additionalParams := make(map[string]attr.Value)
		for key, rawValue := range litellmParams {
			// Skip "known" params (handled by top-level attributes) UNLESS the
			// user explicitly placed them in additional_litellm_params. Import is
			// the one extra case: remote thinking has no top-level ownership, so
			// adopt that complete document through additional parameters.
			if _, isKnown := knownLiteLLMParams[key]; isKnown {
				_, inState := stateKeys[key]
				_, importedDocument := ownership.importedFields["additional_litellm_params."+key]
				importAdoptsDocument := (ownership.imported || importedDocument) && (key == "thinking" || key == "merge_reasoning_content_in_choices")
				if (!inState || !additionalParamsOwned) && !importAdoptsDocument {
					continue
				}
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
				// LiteLLM masks sensitive scalar values such as azure_ad_token
				// before returning them. Preserve the configured value when the API
				// clearly returned a masking marker instead of exposing the masked
				// value to Terraform's post-apply consistency check.
				if prior, hasPrior := priorStrings[key]; hasPrior && isMaskedAPIString(v) {
					additionalParams[key] = types.StringValue(prior)
				} else if number, ok := canonicalJSONNumberString(v); ok {
					// Normalize without float64 so close values above 2^53 remain
					// distinct and scientific notation round-trips exactly.
					additionalParams[key] = types.StringValue(number)
				} else {
					additionalParams[key] = types.StringValue(v)
				}
			case bool:
				additionalParams[key] = types.StringValue(strconv.FormatBool(v))
			case float64:
				additionalParams[key] = types.StringValue(strconv.FormatFloat(v, 'f', -1, 64))
			case json.Number:
				if number, ok := canonicalJSONNumberString(v.String()); ok {
					additionalParams[key] = types.StringValue(number)
				} else {
					additionalParams[key] = types.StringValue(v.String())
				}
			case int:
				additionalParams[key] = types.StringValue(strconv.Itoa(v))
			case int64:
				additionalParams[key] = types.StringValue(strconv.FormatInt(v, 10))
			default:
				// Arrays, objects, and other complex types — serialize back to JSON string.
				if jsonBytes, err := json.Marshal(v); err == nil {
					apiJSON := string(jsonBytes)
					prior, hasPrior := priorStrings[key]
					switch {
					// Preserve the prior string when it's semantically equal to
					// the API value: json.Marshal emits compact, key-sorted JSON,
					// so a config value like {"inputs": "{prompt}"} would
					// otherwise round-trip to {"inputs":"{prompt}"} and fail the
					// post-apply consistency check purely on formatting.
					case hasPrior && jsonSemanticallyEqual(prior, apiJSON):
						additionalParams[key] = types.StringValue(prior)
					// Preserve same-shaped JSON only when the API value contains an
					// explicit masking marker. Shape alone is insufficient because
					// it would hide genuine out-of-band scalar drift.
					case hasPrior && jsonSameShape(prior, apiJSON) && jsonContainsMaskedValue(apiJSON):
						additionalParams[key] = types.StringValue(prior)
					default:
						additionalParams[key] = types.StringValue(apiJSON)
					}
				}
			}
		}

		// Carry forward keys the user configured in additional_litellm_params
		// that the API does not echo back in litellm_params (e.g. tags,
		// max_retries, timeout, stream_timeout). Without this, those keys are
		// dropped on read-back and Terraform fails the post-apply consistency
		// check with "element <key> has vanished" ("Provider produced
		// inconsistent result after apply"). We only do this during normal
		// Read (filterByState), preserving the value from prior state; on
		// Import we intentionally reflect the full API state instead.
		if filterByState {
			priorParams := data.AdditionalLiteLLMParams.Elements()
			for k := range stateKeys {
				if _, dedicated := knownLiteLLMParams[k]; dedicated && !additionalParamsOwned {
					continue
				}
				if _, present := additionalParams[k]; !present {
					if prior, ok := priorParams[k]; ok {
						additionalParams[k] = prior
					}
				}
			}
		}

		// Set additional_litellm_params from API response to detect drift
		// for keys that the user configured.
		value, diagnostics := checkedStringMapValue(ctx, additionalParams, path.Root("additional_litellm_params"), true)
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		data.AdditionalLiteLLMParams = value
	} else {
		value, diagnostics := checkedStringMapValue(ctx, nil, path.Root("additional_litellm_params"), true)
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		data.AdditionalLiteLLMParams = value
	}

	if hasModelInfo {
		if baseModel, ok := modelInfo["base_model"].(string); ok && baseModel != "" {
			data.BaseModel = types.StringValue(baseModel)
		}
		if tier, ok := modelInfo["tier"].(string); ok && tier != "" {
			data.Tier = types.StringValue(tier)
		}
		if mode, ok := modelInfo["mode"].(string); ok && mode != "" {
			// API-inferred mode is unmanaged on create/refresh. Import adopts it;
			// configured state remains authoritative until explicitly replaced.
			if ownership.imported || (!data.Mode.IsNull() && !data.Mode.IsUnknown()) {
				data.Mode = types.StringValue(mode)
			}
		}
		if teamID, ok := modelInfo["team_id"].(string); ok && teamID != "" {
			data.TeamID = types.StringValue(teamID)
		}
		// Read access_groups from model_info
		// The API may not echo back access_groups, so only update if the API
		// actually returns them. If the API is silent, preserve the current value.
		_, accessGroupsDurablyCleared := ownership.durablyClearedFields["access_groups"]
		accessGroups, accessGroupsPresence, diagnostics := strictAPIStringList(ctx, modelInfo, "access_groups", path.Root("access_groups"))
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		if accessGroupsPresence == apiValuePresent && len(accessGroups.Elements()) > 0 && !accessGroupsDurablyCleared {
			if ownership.imported || (!data.AccessGroups.IsNull() && !data.AccessGroups.IsUnknown()) {
				data.AccessGroups = accessGroups
			}
		} else if data.AccessGroups.IsUnknown() {
			empty, diagnostics := checkedStringListValue(ctx, nil, path.Root("access_groups"))
			if err := collectionProjectionError(ctx, diagnostics); err != nil {
				return err
			}
			data.AccessGroups = empty
		}
		// If the API didn't return access_groups and we already have a concrete
		// value (from config/state), leave it as-is.
	} else if data.AccessGroups.IsUnknown() {
		empty, diagnostics := checkedStringListValue(ctx, nil, path.Root("access_groups"))
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		data.AccessGroups = empty
	}

	// Read back additional_model_info. Only keys configured in state are
	// consulted because /model/info also merges model cost-map metadata.
	if !data.AdditionalModelInfo.IsNull() && !data.AdditionalModelInfo.IsUnknown() {
		priorValues := data.AdditionalModelInfo.Elements()
		infoValues := make(map[string]attr.Value)
		if hasModelInfo {
			for key, priorValue := range priorValues {
				rawValue, exists := modelInfo[key]
				if !exists {
					continue
				}
				value, ok := stringifyAPIValue(rawValue)
				if !ok {
					continue
				}
				priorString, hasPriorString := priorValue.(types.String)
				readString, hasReadString := value.(types.String)
				if hasPriorString && hasReadString && jsonSemanticallyEqual(priorString.ValueString(), readString.ValueString()) {
					value = priorValue
				}
				infoValues[key] = value
			}
		}
		value, diagnostics := checkedStringMapValue(ctx, infoValues, path.Root("additional_model_info"), false)
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		data.AdditionalModelInfo = value
	} else {
		value, diagnostics := checkedStringMapValue(ctx, nil, path.Root("additional_model_info"), false)
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		data.AdditionalModelInfo = value
	}

	semanticModelInfo := modelInfo
	if !hasModelInfo {
		semanticModelInfo = nil
	}
	semanticJSON, err := reconcileModelAdditionalModelInfoJSON(
		ctx,
		data.AdditionalModelInfoJSON,
		semanticModelInfo,
		ownership.additionalModelInfoJSONProvenance,
	)
	if err != nil {
		return fmt.Errorf("%w", errModelSemanticDictionaryProjection)
	}
	data.AdditionalModelInfoJSON = semanticJSON

	// Ensure mode is never Unknown after a Read. Terraform requires all
	// Computed attributes to resolve to a known (or null) value after apply.
	// Wildcard routes (e.g. openai/*) may not have a mode set in the API
	// response, which would leave the attribute Unknown and cause:
	//   "provider still indicated an unknown value for litellm_model.*.mode"
	if err := finalizeModelComputedDefaults(ctx, data); err != nil {
		return err
	}
	if data.ThinkingEnabled.IsNull() || data.ThinkingEnabled.IsUnknown() {
		data.ThinkingEnabled = types.BoolValue(false)
	}
	if data.ThinkingBudgetTokens.IsNull() || data.ThinkingBudgetTokens.IsUnknown() {
		data.ThinkingBudgetTokens = types.Int64Value(1024)
	}

	*original = *data
	return nil
}

// reassertPlannedCosts restores planned cost values after Create's read-back.
// LiteLLM's router can lag a just-created database row, while Terraform
// requires these non-Computed attributes to equal the creation plan. Update
// uses readModelAfterUpdate instead and verifies changed costs stabilize before
// publishing state. Ordinary Read remains authoritative for out-of-band drift.
func reassertPlannedCosts(data *ModelResourceModel, planned *ModelResourceModel) {
	for _, field := range []struct {
		state, plan *types.Float64
	}{
		{&data.InputCostPerMillionTokens, &planned.InputCostPerMillionTokens},
		{&data.OutputCostPerMillionTokens, &planned.OutputCostPerMillionTokens},
		{&data.InputCostPerPixel, &planned.InputCostPerPixel},
		{&data.OutputCostPerPixel, &planned.OutputCostPerPixel},
		{&data.InputCostPerSecond, &planned.InputCostPerSecond},
		{&data.OutputCostPerSecond, &planned.OutputCostPerSecond},
	} {
		if !field.plan.IsUnknown() {
			*field.state = *field.plan
		}
	}
}

// readBackCost returns the refreshed value for a cost attribute, scaled
// (per-token → per-million-tokens where applicable). Present malformed values
// are errors. API-inferred values remain unowned when state is null/unknown;
// explicit null clears owned state; an omitted compatibility field preserves
// state because LiteLLM does not consistently echo inferred costs.
func readBackCost(current types.Float64, object map[string]interface{}, field string, scale float64) (types.Float64, error) {
	return readBackCostWithOwnership(current, object, field, scale, false)
}

func readBackCostWithOwnership(current types.Float64, object map[string]interface{}, field string, scale float64, adopt bool) (types.Float64, error) {
	value, presence, err := apiFloat64At(object, field)
	if err != nil {
		return types.Float64Null(), err
	}
	if !adopt && (current.IsNull() || current.IsUnknown()) {
		return current, nil
	}
	if presence == apiValueNull {
		return types.Float64Null(), nil
	}
	if presence == apiValueAbsent {
		return current, nil
	}

	value *= scale
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return types.Float64Null(), fmt.Errorf("invalid numeric response field %q: scaled value must be finite", field)
	}
	if adopt && (current.IsNull() || current.IsUnknown()) {
		return types.Float64Value(value), nil
	}
	stateValue := current.ValueFloat64()
	tolerance := 1e-9 * math.Max(math.Abs(stateValue), math.Abs(value))
	if math.Abs(value-stateValue) <= tolerance {
		return current, nil
	}
	return types.Float64Value(value), nil
}

// stringifyAPIValue converts a raw JSON value into the canonical string
// representation used by string-map attributes.
func stringifyAPIValue(rawValue interface{}) (attr.Value, bool) {
	switch value := rawValue.(type) {
	case string:
		if number, ok := canonicalJSONNumberString(value); ok {
			return types.StringValue(number), true
		}
		return types.StringValue(value), true
	case bool:
		return types.StringValue(strconv.FormatBool(value)), true
	case float64:
		return types.StringValue(strconv.FormatFloat(value, 'f', -1, 64)), true
	case json.Number:
		if number, ok := canonicalJSONNumberString(value.String()); ok {
			return types.StringValue(number), true
		}
		return types.StringValue(value.String()), true
	case int:
		return types.StringValue(strconv.Itoa(value)), true
	case int64:
		return types.StringValue(strconv.FormatInt(value, 10)), true
	default:
		jsonValue, err := json.Marshal(value)
		if err != nil {
			return nil, false
		}
		return types.StringValue(string(jsonValue)), true
	}
}

func modelImportedFieldsFromState(data ModelResourceModel) map[string]struct{} {
	fields := make(map[string]struct{})
	for _, field := range []struct {
		name  string
		value types.String
	}{
		{"model_api_base", data.ModelAPIBase},
		{"api_version", data.APIVersion},
		{"reasoning_effort", data.ReasoningEffort},
		{"aws_region_name", data.AWSRegionName},
		{"litellm_credential_name", data.LiteLLMCredentialName},
		{"mode", data.Mode},
		{"team_id", data.TeamID},
	} {
		if !field.value.IsNull() && !field.value.IsUnknown() {
			fields[field.name] = struct{}{}
		}
	}
	for _, field := range []struct {
		name  string
		value types.Int64
	}{
		{"tpm", data.TPM}, {"rpm", data.RPM},
	} {
		if !field.value.IsNull() && !field.value.IsUnknown() {
			fields[field.name] = struct{}{}
		}
	}
	for _, field := range []struct {
		name  string
		value types.Float64
	}{
		{"input_cost_per_million_tokens", data.InputCostPerMillionTokens},
		{"output_cost_per_million_tokens", data.OutputCostPerMillionTokens},
		{"input_cost_per_pixel", data.InputCostPerPixel},
		{"output_cost_per_pixel", data.OutputCostPerPixel},
		{"input_cost_per_second", data.InputCostPerSecond},
		{"output_cost_per_second", data.OutputCostPerSecond},
	} {
		if !field.value.IsNull() && !field.value.IsUnknown() {
			fields[field.name] = struct{}{}
		}
	}
	if !data.AdditionalLiteLLMParams.IsNull() && !data.AdditionalLiteLLMParams.IsUnknown() {
		for _, key := range []string{"thinking", "merge_reasoning_content_in_choices"} {
			if _, exists := data.AdditionalLiteLLMParams.Elements()[key]; exists {
				fields["additional_litellm_params."+key] = struct{}{}
			}
		}
	}
	// Optional+Computed access_groups resolves to an empty list during import.
	// Keep that list import-owned too, so later remote additions remain unmanaged
	// until the user explicitly configures this argument.
	fields["access_groups"] = struct{}{}
	return fields
}

func finalizeModelComputedDefaults(ctx context.Context, data *ModelResourceModel) error {
	for _, target := range []*types.String{
		&data.ModelAPIBase, &data.APIVersion, &data.ReasoningEffort,
		&data.TeamID, &data.Mode, &data.LiteLLMCredentialName, &data.AWSRegionName,
	} {
		if target.IsUnknown() {
			*target = types.StringNull()
		}
	}
	for _, target := range []*types.Int64{&data.TPM, &data.RPM} {
		if target.IsUnknown() {
			*target = types.Int64Null()
		}
	}
	for _, target := range []*types.Float64{
		&data.InputCostPerMillionTokens, &data.OutputCostPerMillionTokens,
		&data.InputCostPerPixel, &data.OutputCostPerPixel,
		&data.InputCostPerSecond, &data.OutputCostPerSecond,
	} {
		if target.IsUnknown() {
			*target = types.Float64Null()
		}
	}
	if data.AccessGroups.IsUnknown() {
		value, diagnostics := checkedStringListValue(ctx, nil, path.Root("access_groups"))
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		data.AccessGroups = value
	}
	if data.AdditionalLiteLLMParams.IsUnknown() {
		value, diagnostics := checkedStringMapValue(ctx, nil, path.Root("additional_litellm_params"), true)
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		data.AdditionalLiteLLMParams = value
	}
	if data.AdditionalModelInfo.IsUnknown() {
		value, diagnostics := checkedStringMapValue(ctx, nil, path.Root("additional_model_info"), false)
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		data.AdditionalModelInfo = value
	}
	return nil
}

func (r *ModelResource) readModelWithRetry(ctx context.Context, data *ModelResourceModel, maxRetries int) error {
	return r.readModelWithRetryOwnership(ctx, data, maxRetries, modelReadOwnership{
		topThinkingOwned: !data.ThinkingEnabled.IsNull() && !data.ThinkingEnabled.IsUnknown() && data.ThinkingEnabled.ValueBool(),
	})
}

func (r *ModelResource) readModelWithRetryOwnership(ctx context.Context, data *ModelResourceModel, maxRetries int, ownership modelReadOwnership) error {
	var err error
	delay := 1 * time.Second
	maxDelay := 10 * time.Second

	for i := 0; i < maxRetries; i++ {
		err = r.readModelWithOwnership(ctx, data, ownership)
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

func (r *ModelResource) readModelAfterUpdate(ctx context.Context, data *ModelResourceModel, planned, prior ModelResourceModel, maxRetries int) error {
	return r.readModelAfterUpdateWithOwnership(ctx, data, planned, prior, maxRetries, modelReadOwnership{
		topThinkingOwned: !planned.ThinkingEnabled.IsNull() && !planned.ThinkingEnabled.IsUnknown() && planned.ThinkingEnabled.ValueBool(),
	})
}

func (r *ModelResource) readModelAfterUpdateWithOwnership(ctx context.Context, data *ModelResourceModel, planned, prior ModelResourceModel, maxRetries int, ownership modelReadOwnership) error {
	if maxRetries < 1 {
		return fmt.Errorf("maxRetries must be at least 1")
	}

	delay := 250 * time.Millisecond
	maxDelay := 2 * time.Second
	var lastErr error
	var staleFields []string
	changedFields := changedModelFieldsNotConverged(planned, prior, prior)
	consecutiveMatches := 0

	for attempt := 0; attempt < maxRetries; attempt++ {
		candidate := planned
		lastErr = r.readModelWithOwnership(ctx, &candidate, ownership)
		if lastErr == nil {
			staleFields = changedModelFieldsNotConverged(planned, prior, candidate)
			if len(staleFields) == 0 {
				consecutiveMatches++
				// LiteLLM's router cache can briefly return the new value and then
				// regress to the old value during reload. Require two consecutive
				// matching reads before publishing post-update state.
				if consecutiveMatches >= 2 {
					*data = candidate
					return nil
				}
			} else {
				consecutiveMatches = 0
			}
		} else if !IsNotFoundError(lastErr) {
			return lastErr
		} else {
			consecutiveMatches = 0
		}

		if attempt == maxRetries-1 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}

	if lastErr != nil {
		return lastErr
	}
	if len(staleFields) == 0 {
		staleFields = changedFields
	}
	return fmt.Errorf("fields did not remain at their planned values after %d reads: %s", maxRetries, strings.Join(staleFields, ", "))
}

func changedModelFieldsNotConverged(planned, prior, observed ModelResourceModel) []string {
	plannedValue := reflect.ValueOf(planned)
	priorValue := reflect.ValueOf(prior)
	observedValue := reflect.ValueOf(observed)
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
			name := modelType.Field(i).Tag.Get("tfsdk")
			if name == "" {
				name = modelType.Field(i).Name
			}
			stale = append(stale, name)
		}
	}
	return stale
}

func modelAPIFieldsChanged(ctx context.Context, planned, prior ModelResourceModel) (bool, error) {
	plannedValue := reflect.ValueOf(planned)
	priorValue := reflect.ValueOf(prior)
	modelType := plannedValue.Type()
	for i := 0; i < plannedValue.NumField(); i++ {
		name := modelType.Field(i).Tag.Get("tfsdk")
		if name == "id" || name == "additional_litellm_params_configured" || name == "additional_model_info_configured" {
			continue
		}
		plannedAttr, plannedOK := plannedValue.Field(i).Interface().(attr.Value)
		priorAttr, priorOK := priorValue.Field(i).Interface().(attr.Value)
		if name == "additional_model_info_json" {
			plannedJSON, plannedIsString := plannedAttr.(types.String)
			priorJSON, priorIsString := priorAttr.(types.String)
			if plannedIsString && priorIsString && !plannedJSON.IsNull() && !plannedJSON.IsUnknown() && !priorJSON.IsNull() && !priorJSON.IsUnknown() {
				plannedObject, plannedErr := parseSemanticDictionary(ctx, plannedJSON.ValueString())
				priorObject, priorErr := parseSemanticDictionary(ctx, priorJSON.ValueString())
				if plannedErr != nil || priorErr != nil {
					return false, errors.New("semantic model information comparison failed")
				}
				equal, compareErr := semanticDictionaryValuesEqual(ctx, plannedObject, priorObject)
				if compareErr != nil {
					return false, compareErr
				}
				if equal {
					continue
				}
			}
		}
		if plannedOK && priorOK && !plannedAttr.IsUnknown() && !plannedAttr.Equal(priorAttr) {
			return true, nil
		}
	}
	return false, ctx.Err()
}

func modelClearedFields(planned, prior ModelResourceModel, topThinkingOwned bool) map[string]struct{} {
	cleared := make(map[string]struct{})
	for _, field := range []struct {
		name           string
		planned, prior types.String
	}{
		{"api_key", planned.ModelAPIKey, prior.ModelAPIKey},
		{"api_base", planned.ModelAPIBase, prior.ModelAPIBase},
		{"api_version", planned.APIVersion, prior.APIVersion},
		{"aws_access_key_id", planned.AWSAccessKeyID, prior.AWSAccessKeyID},
		{"aws_secret_access_key", planned.AWSSecretAccessKey, prior.AWSSecretAccessKey},
		{"aws_region_name", planned.AWSRegionName, prior.AWSRegionName},
		{"aws_session_name", planned.AWSSessionName, prior.AWSSessionName},
		{"aws_role_name", planned.AWSRoleName, prior.AWSRoleName},
		{"vertex_project", planned.VertexProject, prior.VertexProject},
		{"vertex_location", planned.VertexLocation, prior.VertexLocation},
		{"vertex_credentials", planned.VertexCredentials, prior.VertexCredentials},
		{"litellm_credential_name", planned.LiteLLMCredentialName, prior.LiteLLMCredentialName},
		{"reasoning_effort", planned.ReasoningEffort, prior.ReasoningEffort},
		{"mode", planned.Mode, prior.Mode},
	} {
		if field.planned.IsNull() && !field.prior.IsNull() && !field.prior.IsUnknown() {
			cleared[field.name] = struct{}{}
		}
	}
	for _, field := range []struct {
		name           string
		planned, prior types.Float64
	}{
		{"input_cost_per_token", planned.InputCostPerMillionTokens, prior.InputCostPerMillionTokens},
		{"output_cost_per_token", planned.OutputCostPerMillionTokens, prior.OutputCostPerMillionTokens},
		{"input_cost_per_pixel", planned.InputCostPerPixel, prior.InputCostPerPixel},
		{"output_cost_per_pixel", planned.OutputCostPerPixel, prior.OutputCostPerPixel},
		{"input_cost_per_second", planned.InputCostPerSecond, prior.InputCostPerSecond},
		{"output_cost_per_second", planned.OutputCostPerSecond, prior.OutputCostPerSecond},
	} {
		if field.planned.IsNull() && !field.prior.IsNull() && !field.prior.IsUnknown() {
			cleared[field.name] = struct{}{}
		}
	}
	if topThinkingOwned && !prior.ThinkingEnabled.IsNull() && prior.ThinkingEnabled.ValueBool() && !planned.ThinkingEnabled.ValueBool() {
		cleared["thinking"] = struct{}{}
	}
	if !prior.MergeReasoningContentInChoices.IsNull() && prior.MergeReasoningContentInChoices.ValueBool() && (planned.MergeReasoningContentInChoices.IsNull() || (!planned.MergeReasoningContentInChoices.IsUnknown() && !planned.MergeReasoningContentInChoices.ValueBool())) {
		cleared["merge_reasoning_content_in_choices"] = struct{}{}
	}
	if !prior.AccessGroups.IsNull() && !prior.AccessGroups.IsUnknown() && len(prior.AccessGroups.Elements()) > 0 && !planned.AccessGroups.IsUnknown() && len(planned.AccessGroups.Elements()) == 0 {
		cleared["access_groups"] = struct{}{}
	}
	return cleared
}

func updateModelStringFromAPI(target *types.String, object map[string]interface{}, field string, owned, sensitive bool) error {
	if !owned {
		return nil
	}
	value, presence, err := apiValueAt(object, field)
	if err != nil {
		return err
	}
	if presence == apiValueAbsent {
		if sensitive {
			return nil
		}
		*target = types.StringNull()
		return nil
	}
	if presence == apiValueNull {
		*target = types.StringNull()
		return nil
	}
	text, ok := value.(string)
	if !ok {
		return fmt.Errorf("invalid response field %q: expected a string", field)
	}
	if text == "" {
		*target = types.StringNull()
		return nil
	}
	if sensitive && isMaskedAPIString(text) {
		return nil
	}
	*target = types.StringValue(text)
	return nil
}

func partitionModelClears(cleared map[string]struct{}) (patchVerified, readback map[string]struct{}) {
	patchVerified = make(map[string]struct{})
	readback = make(map[string]struct{})
	for field := range cleared {
		switch field {
		case "api_key", "api_base", "api_version", "reasoning_effort",
			"aws_access_key_id", "aws_secret_access_key", "aws_region_name", "aws_session_name", "aws_role_name",
			"vertex_project", "vertex_location", "vertex_credentials", "litellm_credential_name":
			// PATCH returns encrypted-at-rest string values. Only a fresh
			// decrypted read can prove the clear without trusting ciphertext.
			readback[field] = struct{}{}
		default:
			patchVerified[field] = struct{}{}
		}
	}
	return patchVerified, readback
}

func verifyModelPatchClears(result map[string]interface{}, cleared map[string]struct{}) error {
	litellmParams, paramsOK := result["litellm_params"].(map[string]interface{})
	modelInfo, infoOK := result["model_info"].(map[string]interface{})
	if !paramsOK || !infoOK {
		return fmt.Errorf("LiteLLM accepted the model update but did not return the authoritative litellm_params and model_info documents needed to verify requested clears; Terraform retained prior state")
	}
	return verifyModelClears(litellmParams, modelInfo, cleared)
}

func verifyModelClears(litellmParams, modelInfo map[string]interface{}, cleared map[string]struct{}) error {
	for field := range cleared {
		switch field {
		case "mode", "access_groups":
			value, present := modelInfo[field]
			if !present || value == nil {
				continue
			}
			if field == "mode" {
				if text, ok := value.(string); ok && text == "" {
					continue
				}
			} else if list, ok := value.([]interface{}); ok && len(list) == 0 {
				continue
			}
			return fmt.Errorf("model_info.%s remains set after LiteLLM accepted its clear", field)
		case "thinking":
			value, present := litellmParams[field]
			if !present || value == nil {
				continue
			}
			document, ok := value.(map[string]interface{})
			if ok {
				kind, _ := document["type"].(string)
				if kind == "disabled" {
					continue
				}
			}
			return fmt.Errorf("litellm_params.thinking remains enabled after LiteLLM accepted its clear")
		case "merge_reasoning_content_in_choices":
			value, present := litellmParams[field]
			if !present || value == nil || value == false {
				continue
			}
			return fmt.Errorf("litellm_params.%s remains enabled after LiteLLM accepted its clear", field)
		case "reasoning_effort":
			value, present := litellmParams[field]
			if !present || value == nil || value == "" || value == "none" {
				continue
			}
			return fmt.Errorf("litellm_params.reasoning_effort remains enabled after LiteLLM accepted its clear")
		case "input_cost_per_token", "output_cost_per_token", "input_cost_per_pixel", "output_cost_per_pixel", "input_cost_per_second", "output_cost_per_second":
			if value, present := litellmParams[field]; !present || value == nil {
				continue
			}
			return fmt.Errorf("litellm_params.%s remains set after LiteLLM accepted its clear", field)
		default:
			value, present := litellmParams[field]
			if !present || value == nil || value == "" {
				continue
			}
			return fmt.Errorf("litellm_params.%s remains set after LiteLLM accepted its clear", field)
		}
	}
	return nil
}

func setModelPatchString(target map[string]interface{}, key string, planned, prior types.String) {
	if !planned.IsNull() && !planned.IsUnknown() {
		target[key] = planned.ValueString()
		return
	}
	if planned.IsNull() && !prior.IsNull() && !prior.IsUnknown() {
		target[key] = ""
	}
}

func setModelPatchCost(target map[string]interface{}, key string, planned, prior types.Float64, scale float64, clearable bool) {
	if !planned.IsNull() && !planned.IsUnknown() {
		target[key] = planned.ValueFloat64() / scale
		return
	}
	if clearable && planned.IsNull() && !prior.IsNull() && !prior.IsUnknown() {
		// LiteLLM v1.98 recognizes explicit null only for SPECIAL_MODEL_INFO_PARAMS.
		target[key] = nil
	}
}

// patchModel uses the PATCH /model/{model_id}/update endpoint for partial updates.
func (r *ModelResource) patchModel(ctx context.Context, data, prior *ModelResourceModel, topThinkingOwned, priorTopThinkingOwned bool) (map[string]interface{}, error) {
	collections, diagnostics := convertModelRequestCollections(ctx, *data)
	if diagnostics.HasError() {
		return nil, fmt.Errorf("model request collection conversion failed")
	}
	modelID := data.ID.ValueString()
	semanticModelInfoPatch := collections.additionalModelInfoJSON
	if collections.additionalModelInfoConfigured {
		var hydrationErr error
		semanticModelInfoPatch, hydrationErr = r.hydrateModelAdditionalModelInfoJSONPatch(ctx, modelID, collections.additionalModelInfoJSON)
		if hydrationErr != nil {
			return nil, fmt.Errorf("semantic model information hydration failed")
		}
	}
	customLLMProvider := data.CustomLLMProvider.ValueString()
	baseModel := data.BaseModel.ValueString()
	modelName := fmt.Sprintf("%s/%s", customLLMProvider, baseModel)

	// LiteLLM v1.98 shallow-merges both documents. Use only endpoint-proven
	// semantic sentinels here; unsupported removals are replacement-planned.
	litellmParams := map[string]interface{}{
		"custom_llm_provider": customLLMProvider,
		"model":               modelName,
	}

	setModelPatchCost(litellmParams, "input_cost_per_token", data.InputCostPerMillionTokens, prior.InputCostPerMillionTokens, 1000000.0, true)
	setModelPatchCost(litellmParams, "output_cost_per_token", data.OutputCostPerMillionTokens, prior.OutputCostPerMillionTokens, 1000000.0, true)

	// Zero is a real deny-all limit, not a clear sentinel.
	if !data.TPM.IsNull() && !data.TPM.IsUnknown() {
		litellmParams["tpm"] = data.TPM.ValueInt64()
	}
	if !data.RPM.IsNull() && !data.RPM.IsUnknown() {
		litellmParams["rpm"] = data.RPM.ValueInt64()
	}
	setModelPatchString(litellmParams, "api_key", data.ModelAPIKey, prior.ModelAPIKey)
	setModelPatchString(litellmParams, "api_base", data.ModelAPIBase, prior.ModelAPIBase)
	setModelPatchString(litellmParams, "api_version", data.APIVersion, prior.APIVersion)
	if !data.ReasoningEffort.IsNull() && !data.ReasoningEffort.IsUnknown() {
		litellmParams["reasoning_effort"] = data.ReasoningEffort.ValueString()
	} else if data.ReasoningEffort.IsNull() && !prior.ReasoningEffort.IsNull() && !prior.ReasoningEffort.IsUnknown() {
		litellmParams["reasoning_effort"] = "none"
	}
	if !data.MergeReasoningContentInChoices.IsNull() && !data.MergeReasoningContentInChoices.IsUnknown() {
		litellmParams["merge_reasoning_content_in_choices"] = data.MergeReasoningContentInChoices.ValueBool()
	} else if data.MergeReasoningContentInChoices.IsNull() && !prior.MergeReasoningContentInChoices.IsNull() && !prior.MergeReasoningContentInChoices.IsUnknown() {
		litellmParams["merge_reasoning_content_in_choices"] = false
	}

	if topThinkingOwned || priorTopThinkingOwned {
		if topThinkingOwned && data.ThinkingEnabled.ValueBool() {
			litellmParams["thinking"] = map[string]interface{}{
				"type":          "enabled",
				"budget_tokens": data.ThinkingBudgetTokens.ValueInt64(),
			}
		} else {
			litellmParams["thinking"] = map[string]interface{}{"type": "disabled"}
		}
	}

	setModelPatchString(litellmParams, "aws_access_key_id", data.AWSAccessKeyID, prior.AWSAccessKeyID)
	setModelPatchString(litellmParams, "aws_secret_access_key", data.AWSSecretAccessKey, prior.AWSSecretAccessKey)
	setModelPatchString(litellmParams, "aws_region_name", data.AWSRegionName, prior.AWSRegionName)
	setModelPatchString(litellmParams, "aws_session_name", data.AWSSessionName, prior.AWSSessionName)
	setModelPatchString(litellmParams, "aws_role_name", data.AWSRoleName, prior.AWSRoleName)
	setModelPatchString(litellmParams, "vertex_project", data.VertexProject, prior.VertexProject)
	setModelPatchString(litellmParams, "vertex_location", data.VertexLocation, prior.VertexLocation)
	setModelPatchString(litellmParams, "vertex_credentials", data.VertexCredentials, prior.VertexCredentials)
	setModelPatchString(litellmParams, "litellm_credential_name", data.LiteLLMCredentialName, prior.LiteLLMCredentialName)

	setModelPatchCost(litellmParams, "input_cost_per_pixel", data.InputCostPerPixel, prior.InputCostPerPixel, 1.0, false)
	setModelPatchCost(litellmParams, "output_cost_per_pixel", data.OutputCostPerPixel, prior.OutputCostPerPixel, 1.0, false)
	setModelPatchCost(litellmParams, "input_cost_per_second", data.InputCostPerSecond, prior.InputCostPerSecond, 1.0, false)
	setModelPatchCost(litellmParams, "output_cost_per_second", data.OutputCostPerSecond, prior.OutputCostPerSecond, 1.0, false)

	// Additional-map removals remain replacement-only via their plan modifiers.
	// NOTE: LiteLLM PATCH API merges litellm_params (via dict.update), it does not replace them.
	// Parameters removed from config will NOT be removed from the API.
	// To fully remove a parameter, the model must be recreated (e.g. terraform apply -replace=...).
	if !data.AdditionalLiteLLMParams.IsNull() && !data.AdditionalLiteLLMParams.IsUnknown() {
		for key, value := range collections.additionalLiteLLMParams {
			litellmParams[key] = convertStringValue(value)
		}
	}

	// Build model_info for the PATCH request.
	modelInfo := map[string]interface{}{
		"base_model": baseModel,
	}

	if !data.Tier.IsNull() && !data.Tier.IsUnknown() && data.Tier.ValueString() != "" {
		modelInfo["tier"] = data.Tier.ValueString()
	}
	setModelPatchString(modelInfo, "mode", data.Mode, prior.Mode)
	if !data.TeamID.IsNull() && !data.TeamID.IsUnknown() && data.TeamID.ValueString() != "" {
		modelInfo["team_id"] = data.TeamID.ValueString()
		modelInfo["team_public_model_name"] = data.ModelName.ValueString()
	}

	// Empty access_groups is the endpoint-supported authorization clear.
	if !data.AccessGroups.IsNull() && !data.AccessGroups.IsUnknown() {
		modelInfo["access_groups"] = collections.accessGroups
	}

	// Add additional_model_info to the request. Like additional_litellm_params,
	// values are strings in Terraform but converted to native types for the API.
	if !data.AdditionalModelInfo.IsNull() && !data.AdditionalModelInfo.IsUnknown() {
		for key, value := range collections.additionalModelInfo {
			modelInfo[key] = convertStringValue(value)
		}
	}
	if collections.additionalModelInfoConfigured {
		var overlayErr error
		modelInfo, overlayErr = overlayModelAdditionalModelInfoJSON(ctx, modelInfo, semanticModelInfoPatch)
		if overlayErr != nil {
			return nil, fmt.Errorf("semantic model information overlay failed")
		}
	}

	// Build the PATCH request body
	patchReq := map[string]interface{}{
		"model_name":     data.ModelName.ValueString(),
		"litellm_params": litellmParams,
		"model_info":     modelInfo,
	}

	endpoint := endpointWithPathSegment("/model/", modelID, "/update")
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "PATCH", endpoint, patchReq, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// normalizeNumericString normalises a string that represents a number into a
// canonical decimal form.  This ensures that "2.5e-06" and "0.0000025" both
// become "0.0000025", preventing Terraform from seeing a diff between the
// planned value and the value read back from the API.
func normalizeNumericString(s string) string {
	if canonical, ok := canonicalJSONNumberString(s); ok {
		return canonical
	}
	return s
}

// jsonSemanticallyEqual reports whether two strings are both valid JSON that
// decode to the same value, ignoring formatting differences (whitespace, key
// ordering). Used on read-back so a JSON-valued additional_litellm_params
// entry that only differs from the provider's compact re-marshal by formatting
// is not treated as drift.
func jsonSemanticallyEqual(a, b string) bool {
	var av, bv interface{}
	if err := decodeJSONUseNumber([]byte(a), &av); err != nil {
		return false
	}
	if err := decodeJSONUseNumber([]byte(b), &bv); err != nil {
		return false
	}
	return exactJSONValuesEqual(av, bv)
}

func exactJSONValuesEqual(a, b interface{}) bool {
	switch left := a.(type) {
	case json.Number:
		right, ok := b.(json.Number)
		return ok && exactJSONNumbersEqual(left, right)
	case map[string]interface{}:
		right, ok := b.(map[string]interface{})
		if !ok || len(left) != len(right) {
			return false
		}
		for key, leftValue := range left {
			rightValue, exists := right[key]
			if !exists || !exactJSONValuesEqual(leftValue, rightValue) {
				return false
			}
		}
		return true
	case []interface{}:
		right, ok := b.([]interface{})
		if !ok || len(left) != len(right) {
			return false
		}
		for index := range left {
			if !exactJSONValuesEqual(left[index], right[index]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

// jsonSameShape reports whether two JSON strings have the same structure --
// same object keys (recursively) and same array lengths -- ignoring differences
// in scalar (string/number/bool) leaf values. The read path combines this check
// with jsonContainsMaskedValue before preserving prior state, so same-shaped
// unmasked remote drift remains visible.
func jsonSameShape(a, b string) bool {
	var av, bv interface{}
	if err := decodeJSONUseNumber([]byte(a), &av); err != nil {
		return false
	}
	if err := decodeJSONUseNumber([]byte(b), &bv); err != nil {
		return false
	}
	return sameShape(av, bv)
}

func sameShape(a, b interface{}) bool {
	switch at := a.(type) {
	case map[string]interface{}:
		bt, ok := b.(map[string]interface{})
		if !ok || len(at) != len(bt) {
			return false
		}
		for k, av := range at {
			bv, present := bt[k]
			if !present || !sameShape(av, bv) {
				return false
			}
		}
		return true
	case []interface{}:
		bt, ok := b.([]interface{})
		if !ok || len(at) != len(bt) {
			return false
		}
		for i := range at {
			if !sameShape(at[i], bt[i]) {
				return false
			}
		}
		return true
	default:
		// Scalars (string, float64, bool, nil): shape matches if both are
		// scalars of a compatible kind. Differing scalar VALUES are treated as
		// the same shape -- that's the masking case we want to tolerate.
		return !isJSONContainer(b)
	}
}

func isJSONContainer(v interface{}) bool {
	switch v.(type) {
	case map[string]interface{}, []interface{}:
		return true
	default:
		return false
	}
}

func isMaskedAPIString(value string) bool {
	upper := strings.ToUpper(value)
	return strings.Contains(value, "****") || strings.Contains(upper, "REDACTED")
}

func jsonContainsMaskedValue(value string) bool {
	var decoded interface{}
	if err := decodeJSONUseNumber([]byte(value), &decoded); err != nil {
		return false
	}
	return containsMaskedValue(decoded)
}

func containsMaskedValue(value interface{}) bool {
	switch typed := value.(type) {
	case string:
		return isMaskedAPIString(typed)
	case map[string]interface{}:
		for _, child := range typed {
			if containsMaskedValue(child) {
				return true
			}
		}
	case []interface{}:
		for _, child := range typed {
			if containsMaskedValue(child) {
				return true
			}
		}
	}
	return false
}

// normalizeAdditionalParams returns a new MapValue where every numeric string
// has been normalised to decimal notation.
func normalizeAdditionalParams(ctx context.Context, m types.Map) types.Map {
	elements, state, diagnostics := strictTerraformStringMap(ctx, m, path.Root("additional_params"), false)
	if diagnostics.HasError() || state == collectionValueNull || state == collectionValueUnknown {
		return m
	}
	normalised := make(map[string]attr.Value, len(elements))
	for k, v := range elements {
		normalised[k] = types.StringValue(normalizeNumericString(v))
	}
	result, constructorDiagnostics := types.MapValue(types.StringType, normalised)
	if constructorDiagnostics.HasError() {
		return m
	}
	return result
}

// convertStringValue converts a string to its most appropriate Go type.
// This allows additional_litellm_params values (which are stored as strings in
// Terraform state) to be sent as native JSON types in the API request.
func convertStringValue(s string) interface{} {
	// Keep exact integers in their natural request type. Other valid JSON
	// numbers use json.Number so request encoding never rounds through float64.
	if intVal, err := strconv.ParseInt(s, 10, 64); err == nil {
		return intVal
	}
	if canonical, ok := canonicalJSONNumberString(s); ok {
		return json.Number(canonical)
	}
	if boolVal, err := strconv.ParseBool(s); err == nil {
		return boolVal
	}
	// Try JSON (arrays and objects) with UseNumber for exact nested values.
	trimmed := strings.TrimSpace(s)
	if strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "{") {
		var parsed interface{}
		if err := decodeJSONUseNumber([]byte(s), &parsed); err == nil {
			return parsed
		}
	}
	return s
}
