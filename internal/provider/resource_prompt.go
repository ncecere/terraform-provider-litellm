package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &PromptResource{}
var _ resource.ResourceWithImportState = &PromptResource{}

const promptImportedPrivateKey = "prompt_imported_v1"

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
	Environment                       types.String `tfsdk:"environment"`
	Version                           types.Int64  `tfsdk:"version"`
	CreatedAt                         types.String `tfsdk:"created_at"`
	UpdatedAt                         types.String `tfsdk:"updated_at"`
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
			"environment": schema.StringAttribute{
				Description: "Environment-scoped prompt identity. Defaults to development; changing it creates a separately owned logical prompt.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString(defaultPromptEnvironment),
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
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
				Description: "JSON object string of provider-specific query parameters.",
				Optional:    true,
				Validators:  []validator.String{jsonShapeStringValidator{shape: '{'}},
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
				Description: "Type of prompt: config or db.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("config", "db"),
				},
			},
			"version": schema.Int64Attribute{
				Description: "Latest version currently owned in this environment. Updates append a new version.",
				Computed:    true,
			},
			"created_at": schema.StringAttribute{
				Description: "Creation timestamp of the selected latest version.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Last-update timestamp of the selected latest version.",
				Computed:    true,
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

	if !data.PromptType.IsNull() && !data.PromptType.IsUnknown() && data.PromptType.ValueString() == "config" {
		resp.Diagnostics.AddError("Config Prompt Is Read-Only", "LiteLLM v1.98 cannot update or delete config prompts. Import them for read-only visibility, or use prompt_type = \"db\" for managed resources.")
		return
	}
	exists, existenceErr := promptScopedExists(ctx, r.client, data.PromptID.ValueString(), data.Environment.ValueString())
	if existenceErr != nil {
		resp.Diagnostics.AddError("Prompt Existence Not Confirmed", fmt.Sprintf("Unable to verify that the scoped prompt identity is available: %s", existenceErr))
		return
	}
	if exists {
		resp.Diagnostics.AddError("Prompt Already Exists", fmt.Sprintf("A prompt with this ID and environment already exists. Import it with %q instead of creating a second owner.", promptImportID(data.PromptID.ValueString(), promptEnvironment(data.Environment.ValueString()))))
		return
	}
	promptReq, err := r.buildPromptRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Prompt JSON", err.Error())
		return
	}

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/prompts", promptReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create prompt: %s", err))
		return
	}

	data.ID = data.PromptID

	if err := r.readPromptWithRetry(ctx, &data, 8, false); err != nil {
		data.Version = types.Int64Null()
		data.CreatedAt = types.StringNull()
		data.UpdatedAt = types.StringNull()
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("Prompt Create Not Confirmed", fmt.Sprintf("LiteLLM accepted the prompt create, but scoped read-back failed: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PromptResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data PromptResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	importedMarker, privateDiags := req.Private.GetKey(ctx, promptImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(importedMarker) == "true"
	promptID := data.PromptID.ValueString()
	if promptID == "" {
		promptID = data.ID.ValueString()
	}
	environment := promptEnvironment(data.Environment.ValueString())
	if err := r.refreshPrompt(ctx, &data, imported); err != nil {
		if isPromptInfoAbsenceCandidate(err) {
			absent, absenceErr := r.promptScopedVersionsAbsent(ctx, promptID, environment)
			if absenceErr == nil && absent {
				resp.State.RemoveResource(ctx)
				return
			}
		}
		resp.Diagnostics.AddError("Prompt Read Error", "Unable to read and validate the scoped prompt. Response and request details were omitted.")
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if imported && !resp.Diagnostics.HasError() && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, promptImportedPrivateKey, nil)...)
	}
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
	if (!state.PromptType.IsNull() && !state.PromptType.IsUnknown() && state.PromptType.ValueString() == "config") ||
		(!data.PromptType.IsNull() && !data.PromptType.IsUnknown() && data.PromptType.ValueString() == "config") {
		resp.Diagnostics.AddError("Config Prompt Is Read-Only", "LiteLLM v1.98 cannot update config prompts. Keep imported config prompts unchanged, or manage a database prompt instead.")
		return
	}

	promptReq, err := r.buildPromptRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Prompt JSON", err.Error())
		return
	}
	var currentRaw map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", promptEndpoint(data.PromptID.ValueString(), data.Environment.ValueString(), nil), nil, &currentRaw); err != nil {
		resp.Diagnostics.AddError("Prompt Update Not Safe", fmt.Sprintf("Unable to verify the complete current prompt before versioning: %s", err))
		return
	}
	current, err := promptObject(currentRaw, true, data.PromptID.ValueString(), data.Environment.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Prompt Update Not Safe", err.Error())
		return
	}
	if !state.Version.IsNull() && !state.Version.IsUnknown() && (!current.HasVersion || current.Version != state.Version.ValueInt64()) {
		resp.Diagnostics.AddError("Prompt Update Not Safe", "The latest scoped prompt version changed after planning; refresh and retry.")
		return
	}
	if err := validateMutablePromptInfo(current.Info); err != nil {
		resp.Diagnostics.AddError("Config Prompt Is Read-Only", err.Error())
		return
	}
	if err := validatePromptUpdateCoverage(current.Params, &data, &state); err != nil {
		resp.Diagnostics.AddError("Prompt Update Not Safe", err.Error())
		return
	}

	endpoint := promptPath(data.PromptID.ValueString(), nil)
	if err := r.client.DoRequestWithResponse(ctx, "PUT", endpoint, promptReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update prompt: %s", err))
		return
	}

	if err := r.readPromptWithRetry(ctx, &data, 8, false); err != nil {
		resp.Diagnostics.AddError("Prompt Update Not Confirmed", fmt.Sprintf("LiteLLM accepted the prompt update, but scoped read-back failed: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *PromptResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data PromptResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := promptEndpoint(data.PromptID.ValueString(), data.Environment.ValueString(), nil)
	deleteErr := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil)
	// DELETE can return 400 for undeletable config prompts and 404 from a stale
	// process-local registry even while scoped DB rows still exist. Never remove
	// state based on the DELETE status alone; the explicit-environment info read
	// is the authoritative absence check.
	var probe map[string]interface{}
	probeErr := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &probe)
	if deleteErr != nil && IsAPIErrorStatus(deleteErr, http.StatusNotFound) && probeErr == nil {
		// A v1.98 worker can lose its process-local registry key while the scoped
		// DB history still exists. Reinitialize that exact latest DB version with
		// a no-content-change PATCH, retry DELETE once, then prove absence again.
		observed, decodeErr := promptObject(probe, true, data.PromptID.ValueString(), data.Environment.ValueString())
		if decodeErr == nil && validateMutablePromptInfo(observed.Info) == nil {
			patch := map[string]interface{}{"prompt_info": observed.Info}
			if patchErr := r.client.DoRequestWithResponse(ctx, "PATCH", endpoint, patch, nil); patchErr == nil {
				deleteErr = r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil)
				probe = nil
				probeErr = r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &probe)
			}
		}
	}
	if isPromptAbsentError(probeErr) {
		if deleteErr != nil {
			resp.Diagnostics.AddWarning("Prompt Delete Recovered", "LiteLLM returned an error after deletion, but a scoped read confirmed this prompt environment is absent.")
		}
		return
	}
	if deleteErr != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete prompt: %s", deleteErr))
		return
	}
	if probeErr != nil {
		resp.Diagnostics.AddError("Prompt Delete Not Confirmed", fmt.Sprintf("LiteLLM accepted the scoped delete, but absence could not be confirmed: %s", probeErr))
		return
	}
	resp.Diagnostics.AddError("Prompt Delete Not Confirmed", "LiteLLM accepted the scoped delete, but the prompt environment still exists.")
}

func (r *PromptResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	promptID, environment, err := parsePromptImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Prompt Import ID", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), promptID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("prompt_id"), promptID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("environment"), environment)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, promptImportedPrivateKey, []byte("true"))...)
	}
}

func validateMutablePromptInfo(info map[string]interface{}) error {
	value, exists := info["prompt_type"]
	if !exists || value == nil {
		return fmt.Errorf("prompt response omitted required prompt_info.prompt_type")
	}
	promptType, ok := value.(string)
	if !ok {
		return fmt.Errorf("prompt response field %q must be a string", "prompt_info.prompt_type")
	}
	switch promptType {
	case "db":
		return nil
	case "config":
		return fmt.Errorf("LiteLLM reports this as a config prompt, which v1.98 cannot update or delete through the management API")
	default:
		return fmt.Errorf("prompt response field %q returned unsupported value %q", "prompt_info.prompt_type", promptType)
	}
}

func validatePromptUpdateCoverage(remote map[string]interface{}, plan, prior *PromptResourceModel) error {
	modeled := map[string]struct{}{
		"prompt_integration":                    {},
		"api_base":                              {},
		"api_key":                               {},
		"provider_specific_query_params":        {},
		"ignore_prompt_manager_model":           {},
		"ignore_prompt_manager_optional_params": {},
		"dotprompt_content":                     {},
	}
	for key, value := range remote {
		if value == nil {
			continue
		}
		if _, supported := modeled[key]; !supported {
			return fmt.Errorf("the current prompt contains unmodeled litellm_params field %q; refusing a full-version update that could discard it", key)
		}
	}
	if value, exists := remote["api_key"]; exists && value != nil &&
		(plan.APIKey.IsNull() || plan.APIKey.IsUnknown() || plan.APIKey.ValueString() == "") &&
		(prior.APIKey.IsNull() || prior.APIKey.IsUnknown() || prior.APIKey.ValueString() == "") {
		return fmt.Errorf("the current prompt contains an API credential not owned by Terraform; configure api_key before updating")
	}
	return nil
}

func (r *PromptResource) buildPromptRequest(ctx context.Context, data *PromptResourceModel) (map[string]interface{}, error) {
	litellmParams := map[string]interface{}{
		"prompt_integration": data.PromptIntegration.ValueString(),
	}

	// String fields - check IsNull, IsUnknown, and empty string
	if !data.APIBase.IsNull() && !data.APIBase.IsUnknown() && data.APIBase.ValueString() != "" {
		litellmParams["api_base"] = data.APIBase.ValueString()
	}
	if !data.APIKey.IsNull() && !data.APIKey.IsUnknown() && data.APIKey.ValueString() != "" {
		litellmParams["api_key"] = data.APIKey.ValueString()
	}
	if !data.DotpromptContent.IsNull() && !data.DotpromptContent.IsUnknown() && data.DotpromptContent.ValueString() != "" {
		litellmParams["dotprompt_content"] = data.DotpromptContent.ValueString()
	}
	if !data.ProviderSpecificQueryParams.IsNull() && !data.ProviderSpecificQueryParams.IsUnknown() && data.ProviderSpecificQueryParams.ValueString() != "" {
		params, err := decodeRequestJSONObject(data.ProviderSpecificQueryParams.ValueString(), "provider_specific_query_params")
		if err != nil {
			return nil, err
		}
		litellmParams["provider_specific_query_params"] = params
	}

	// Boolean fields - check IsNull and IsUnknown
	if !data.IgnorePromptManagerModel.IsNull() && !data.IgnorePromptManagerModel.IsUnknown() {
		litellmParams["ignore_prompt_manager_model"] = data.IgnorePromptManagerModel.ValueBool()
	}
	if !data.IgnorePromptManagerOptionalParams.IsNull() && !data.IgnorePromptManagerOptionalParams.IsUnknown() {
		litellmParams["ignore_prompt_manager_optional_params"] = data.IgnorePromptManagerOptionalParams.ValueBool()
	}

	promptReq := map[string]interface{}{
		"prompt_id":      data.PromptID.ValueString(),
		"litellm_params": litellmParams,
	}

	promptInfo := map[string]interface{}{
		"environment": promptEnvironment(data.Environment.ValueString()),
		"prompt_type": "db",
	}
	if !data.PromptType.IsNull() && !data.PromptType.IsUnknown() && data.PromptType.ValueString() != "" {
		promptInfo["prompt_type"] = data.PromptType.ValueString()
	}
	promptReq["prompt_info"] = promptInfo

	return promptReq, nil
}

func (r *PromptResource) readPromptWithRetry(ctx context.Context, data *PromptResourceModel, maxRetries int, adoptImportedDefaults bool) error {
	var err error
	delay := 1 * time.Second
	maxDelay := 10 * time.Second

	for i := 0; i < maxRetries; i++ {
		err = r.readPrompt(ctx, data, adoptImportedDefaults)
		if err == nil {
			return nil
		}

		if !isPromptAbsentError(err) {
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

func promptStringFromAPI(params map[string]interface{}, field string) (types.String, error) {
	value, exists := params[field]
	if !exists || value == nil {
		return types.StringNull(), nil
	}
	text, ok := value.(string)
	if !ok {
		return types.StringNull(), fmt.Errorf("prompt response field %q must be a string or null", "litellm_params."+field)
	}
	return types.StringValue(text), nil
}

func promptBoolFromAPI(params map[string]interface{}, field string) (types.Bool, error) {
	value, exists := params[field]
	if !exists || value == nil {
		return types.BoolNull(), nil
	}
	boolean, ok := value.(bool)
	if !ok {
		return types.BoolNull(), fmt.Errorf("prompt response field %q must be a boolean or null", "litellm_params."+field)
	}
	return types.BoolValue(boolean), nil
}

// readPrompt is reserved for operation-coupled Create/Update confirmation. It
// deliberately performs one request per existing convergence-loop attempt.
func (r *PromptResource) readPrompt(ctx context.Context, data *PromptResourceModel, adoptImportedDefaults bool) error {
	return r.readPromptWithTransport(ctx, data, adoptImportedDefaults, false)
}

// refreshPrompt is the ordinary Terraform refresh path. Only this scoped,
// explicit-environment DB read uses bounded safe-read retries.
func (r *PromptResource) refreshPrompt(ctx context.Context, data *PromptResourceModel, adoptImportedDefaults bool) error {
	return r.readPromptWithTransport(ctx, data, adoptImportedDefaults, true)
}

func (r *PromptResource) readPromptWithTransport(ctx context.Context, data *PromptResourceModel, adoptImportedDefaults, safeRead bool) error {
	promptID := data.PromptID.ValueString()
	if promptID == "" {
		promptID = data.ID.ValueString()
	}
	if promptID == "" {
		return fmt.Errorf("prompt state omitted its identity")
	}
	environment := promptEnvironment(data.Environment.ValueString())
	endpoint := promptEndpoint(promptID, environment, nil)

	var rawResult map[string]interface{}
	var err error
	if safeRead {
		err = r.client.DoReadWithResponse(ctx, http.MethodGet, endpoint, nil, &rawResult)
	} else {
		err = r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &rawResult)
	}
	if err != nil {
		return err
	}
	return projectPromptResourceAPIObject(data, rawResult, promptID, environment, adoptImportedDefaults)
}

func isPromptInfoAbsenceCandidate(err error) bool {
	return IsAPIErrorStatus(err, http.StatusBadRequest) || IsAPIErrorStatus(err, http.StatusNotFound)
}

// promptScopedVersionsAbsent performs one direct authoritative DB-history read.
// At v1.98 only its exact 404 proves that the scoped prompt is absent.
func (r *PromptResource) promptScopedVersionsAbsent(ctx context.Context, promptID, environment string) (bool, error) {
	_, err := fetchEnvelopeListObjects(ctx, r.client, promptVersionsEndpoint(promptID, environment), "prompts", "prompt version item")
	if IsAPIErrorStatus(err, http.StatusNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// projectPromptResourceAPIObject validates every modeled response field before
// assigning a candidate, so malformed successful reads cannot partially mutate
// prior state or mutation-recovery state.
func projectPromptResourceAPIObject(data *PromptResourceModel, rawResult map[string]interface{}, promptID, environment string, adoptImportedDefaults bool) error {
	observed, err := promptObject(rawResult, true, promptID, environment)
	if err != nil {
		return err
	}
	if !observed.HasVersion {
		return fmt.Errorf("prompt response omitted required version")
	}
	integration, ok := observed.Params["prompt_integration"].(string)
	if !ok || integration == "" {
		return fmt.Errorf("prompt response omitted required integration")
	}
	apiBase, err := promptStringFromAPI(observed.Params, "api_base")
	if err != nil {
		return err
	}
	if _, err := promptStringFromAPI(observed.Params, "api_key"); err != nil {
		return err
	}
	dotpromptContent, err := promptStringFromAPI(observed.Params, "dotprompt_content")
	if err != nil {
		return err
	}
	ignoreModel, err := promptBoolFromAPI(observed.Params, "ignore_prompt_manager_model")
	if err != nil {
		return err
	}
	ignoreOptional, err := promptBoolFromAPI(observed.Params, "ignore_prompt_manager_optional_params")
	if err != nil {
		return err
	}
	queryParams := types.StringNull()
	if value, exists := observed.Params["provider_specific_query_params"]; exists && value != nil {
		object, valid := value.(map[string]interface{})
		if !valid {
			return fmt.Errorf("prompt response query parameters were malformed")
		}
		encoded, marshalErr := json.Marshal(object)
		if marshalErr != nil {
			return fmt.Errorf("prompt response query parameters could not be encoded")
		}
		queryParams = types.StringValue(string(encoded))
	}
	if observed.Info == nil {
		return fmt.Errorf("prompt response omitted required prompt information")
	}
	promptTypeValue, exists := observed.Info["prompt_type"]
	if !exists || promptTypeValue == nil {
		return fmt.Errorf("prompt response omitted required prompt type")
	}
	promptType, valid := promptTypeValue.(string)
	if !valid || (promptType != "db" && promptType != "config") {
		return fmt.Errorf("prompt response returned an invalid prompt type")
	}

	candidate := *data
	candidate.ID = types.StringValue(observed.PromptID)
	candidate.PromptID = types.StringValue(observed.PromptID)
	candidate.Environment = types.StringValue(observed.Environment)
	candidate.Version = types.Int64Value(observed.Version)
	candidate.PromptIntegration = types.StringValue(integration)
	candidate.CreatedAt = types.StringNull()
	candidate.UpdatedAt = types.StringNull()
	if observed.CreatedAt != nil {
		candidate.CreatedAt = types.StringValue(*observed.CreatedAt)
	}
	if observed.UpdatedAt != nil {
		candidate.UpdatedAt = types.StringValue(*observed.UpdatedAt)
	}
	candidate.APIBase = apiBase
	candidate.DotpromptContent = dotpromptContent
	if adoptImportedDefaults || !candidate.IgnorePromptManagerModel.IsNull() || candidate.IgnorePromptManagerModel.IsUnknown() {
		candidate.IgnorePromptManagerModel = ignoreModel
	}
	if adoptImportedDefaults || !candidate.IgnorePromptManagerOptionalParams.IsNull() || candidate.IgnorePromptManagerOptionalParams.IsUnknown() {
		candidate.IgnorePromptManagerOptionalParams = ignoreOptional
	}
	if !queryParams.IsNull() && !queryParams.IsUnknown() && !candidate.ProviderSpecificQueryParams.IsNull() && !candidate.ProviderSpecificQueryParams.IsUnknown() && jsonSemanticallyEqual(candidate.ProviderSpecificQueryParams.ValueString(), queryParams.ValueString()) {
		// Preserve configured spelling when the API document is semantically equal.
	} else {
		candidate.ProviderSpecificQueryParams = queryParams
	}
	if adoptImportedDefaults || !candidate.PromptType.IsNull() || candidate.PromptType.IsUnknown() {
		candidate.PromptType = types.StringValue(promptType)
	}
	*data = candidate
	return nil
}

func parsePromptResult(rawResult map[string]interface{}) map[string]interface{} {
	if promptSpec, ok := rawResult["prompt_spec"].(map[string]interface{}); ok {
		return promptSpec
	}
	return rawResult
}
