package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &FallbackResource{}
var _ resource.ResourceWithImportState = &FallbackResource{}

const (
	fallbackReadMaxAttempts    = 5
	fallbackReadInitialDelay   = time.Second
	fallbackReadMaxDelay       = 10 * time.Second
	fallbackDeleteMaxAttempts  = 5
	fallbackDeleteInitialDelay = 250 * time.Millisecond
	fallbackDeleteMaxDelay     = 2 * time.Second
)

var supportedFallbackTypes = []string{"general", "context_window", "content_policy"}

func NewFallbackResource() resource.Resource {
	return &FallbackResource{}
}

var errFallbackDeleteStillPresent = errors.New("fallback remained present after the bounded deletion confirmation")

const fallbackDeleteStillPresentDiagnostic = "LiteLLM's authoritative fallback GET remained present after the bounded DELETE confirmation. Terraform state was retained."

type FallbackResource struct {
	client             *Client
	readMaxAttempts    int
	readInitialDelay   time.Duration
	readMaxDelay       time.Duration
	deleteMaxAttempts  int
	deleteInitialDelay time.Duration
	deleteMaxDelay     time.Duration
}

type FallbackResourceModel struct {
	ID             types.String `tfsdk:"id"`
	Model          types.String `tfsdk:"model"`
	FallbackModels types.List   `tfsdk:"fallback_models"`
	FallbackType   types.String `tfsdk:"fallback_type"`
}

func (r *FallbackResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fallback"
}

func (r *FallbackResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM fallback configuration for a model. Fallbacks are used when a model call fails after retries.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for this fallback (model:fallback_type).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"model": schema.StringAttribute{
				Description: "The model name to configure fallbacks for (e.g. 'gpt-3.5-turbo').",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"fallback_models": schema.ListAttribute{
				Description: "List of fallback model names in order of priority.",
				Required:    true,
				ElementType: types.StringType,
			},
			"fallback_type": schema.StringAttribute{
				Description: "Type of fallback: general (default), context_window, or content_policy.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("general"),
				Validators: []validator.String{
					stringvalidator.OneOf(supportedFallbackTypes...),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *FallbackResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *FallbackResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data FallbackResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fallbackReq, conversionDiagnostics := r.buildFallbackRequest(ctx, &data)
	resp.Diagnostics.Append(conversionDiagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.writeFallbackWithRetry(ctx, fallbackReq, 5); err != nil {
		resp.Diagnostics.AddError("Fallback Create Error", fallbackOperationDiagnostic("create", err))
		return
	}

	data.ID = types.StringValue(data.Model.ValueString() + ":" + data.FallbackType.ValueString())

	if err := r.readFallbackWithRetry(ctx, &data, fallbackReadMaxAttempts); err != nil {
		resp.Diagnostics.AddWarning("Fallback Read-Back Error", fallbackOperationDiagnostic("read back the newly created", err))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FallbackResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data FallbackResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readFallbackWithRetry(ctx, &data, fallbackReadMaxAttempts); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Fallback Read Error", fallbackOperationDiagnostic("read", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FallbackResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data FallbackResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fallbackReq, conversionDiagnostics := r.buildFallbackRequest(ctx, &data)
	resp.Diagnostics.Append(conversionDiagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.writeFallbackWithRetry(ctx, fallbackReq, 5); err != nil {
		resp.Diagnostics.AddError("Fallback Update Error", fallbackOperationDiagnostic("update", err))
		return
	}

	if err := r.readFallbackWithRetry(ctx, &data, fallbackReadMaxAttempts); err != nil {
		resp.Diagnostics.AddWarning("Fallback Read-Back Error", fallbackOperationDiagnostic("read back the updated", err))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FallbackResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data FallbackResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fallbackEndpoint(data.Model.ValueString(), data.FallbackType.ValueString())
	deleteErr := r.client.DoRequestWithResponse(ctx, http.MethodDelete, endpoint, nil, nil)

	// LiteLLM v1.98 can return DELETE 404 while its authoritative GET still
	// returns the fallback. Never interpret the DELETE status alone as absence:
	// doing so would remove Terraform state while leaving live routing config.
	if confirmationErr := r.confirmFallbackDeleted(ctx, &data, fallbackDeleteMaxAttempts); confirmationErr != nil {
		if errors.Is(confirmationErr, errFallbackDeleteStillPresent) && (deleteErr == nil || IsNotFoundError(deleteErr)) {
			resp.Diagnostics.AddError("Fallback Delete Unconfirmed", fallbackDeleteStillPresentDiagnostic)
			return
		}
		diagnosticErr := confirmationErr
		operation := "confirm deletion of"
		if deleteErr != nil && !IsNotFoundError(deleteErr) {
			diagnosticErr = deleteErr
			operation = "delete"
		}
		resp.Diagnostics.AddError("Fallback Delete Unconfirmed", fallbackOperationDiagnostic(operation, diagnosticErr))
	}
}

func (r *FallbackResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	model, fallbackType, err := parseFallbackImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Fallback Import ID", err.Error())
		return
	}

	data := FallbackResourceModel{
		Model:        types.StringValue(model),
		FallbackType: types.StringValue(fallbackType),
	}
	if err := r.readFallback(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Fallback Import Read Error", fallbackOperationDiagnostic("read during import", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func parseFallbackImportID(importID string) (string, string, error) {
	separator := strings.LastIndexByte(importID, ':')
	if separator < 0 {
		if importID == "" {
			return "", "", fmt.Errorf("the model component must not be empty; use <model> or <model>:<fallback_type>")
		}
		// Preserve the provider's historical model-only import grammar. A model
		// containing a colon must use an explicit supported right-hand suffix so
		// it cannot be confused with a misspelled fallback type.
		return importID, "general", nil
	}
	if separator == len(importID)-1 {
		return "", "", fmt.Errorf("use <model>:<fallback_type>; fallback_type must be one of general, context_window, or content_policy")
	}
	if separator == 0 {
		return "", "", fmt.Errorf("the model component must not be empty; use <model>:<fallback_type>")
	}

	fallbackType := importID[separator+1:]
	if !isSupportedFallbackType(fallbackType) {
		return "", "", fmt.Errorf("the fallback_type suffix is not supported; use exactly general, context_window, or content_policy")
	}
	return importID[:separator], fallbackType, nil
}

func isSupportedFallbackType(value string) bool {
	for _, supported := range supportedFallbackTypes {
		if value == supported {
			return true
		}
	}
	return false
}

func fallbackEndpoint(model, fallbackType string) string {
	query := url.Values{"fallback_type": []string{fallbackType}}
	return endpointWithQuery(endpointWithFallbackPathSegment("/fallback/", model, ""), query)
}

func fallbackOperationDiagnostic(operation string, err error) string {
	detail := "LiteLLM could not " + operation + " the fallback. Verify that the fallback and referenced models exist, that the fallback type is supported, and that the proxy is reachable. The provider omitted identity and response details; consult trusted LiteLLM logs for more information."
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return fmt.Sprintf("LiteLLM returned HTTP status %d while attempting to %s the fallback. Verify that the fallback and referenced models exist and that the fallback type is supported. The provider omitted identity and response details; consult trusted LiteLLM logs for more information.", apiErr.StatusCode, operation)
	}
	return detail
}

func (r *FallbackResource) buildFallbackRequest(ctx context.Context, data *FallbackResourceModel) (map[string]interface{}, diag.Diagnostics) {
	models, _, diagnostics := strictTerraformStringList(ctx, data.FallbackModels, path.Root("fallback_models"))
	if diagnostics.HasError() {
		return nil, diagnostics
	}
	return map[string]interface{}{
		"model":           data.Model.ValueString(),
		"fallback_models": models,
		"fallback_type":   data.FallbackType.ValueString(),
	}, nil
}

func (r *FallbackResource) writeFallbackWithRetry(ctx context.Context, fallbackReq map[string]interface{}, maxRetries int) error {
	var err error
	delay := 1 * time.Second
	maxDelay := 10 * time.Second

	for i := 0; i < maxRetries; i++ {
		err = r.client.DoRequestWithResponse(ctx, "POST", "/fallback", fallbackReq, nil)
		if err == nil {
			return nil
		}

		if !shouldRetryFallbackWriteError(err) {
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

func shouldRetryFallbackWriteError(err error) bool {
	return IsNotFoundError(err) || isFallbackNotReadyError(err)
}

func (r *FallbackResource) confirmFallbackDeleted(ctx context.Context, data *FallbackResourceModel, maxAttempts int) error {
	initialDelay, maxDelay := fallbackDeleteInitialDelay, fallbackDeleteMaxDelay
	if r.deleteMaxAttempts > 0 {
		maxAttempts = r.deleteMaxAttempts
		initialDelay = r.deleteInitialDelay
		maxDelay = r.deleteMaxDelay
	}
	if maxAttempts < 1 {
		return fmt.Errorf("fallback deletion confirmation requires at least one attempt")
	}

	delay := initialDelay
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		observed := *data
		err := r.readFallback(ctx, &observed)
		if IsNotFoundError(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if attempt == maxAttempts-1 {
			return errFallbackDeleteStillPresent
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}
		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}
	return fmt.Errorf("fallback deletion confirmation exhausted unexpectedly")
}

func (r *FallbackResource) readFallbackWithRetry(ctx context.Context, data *FallbackResourceModel, maxAttempts int) error {
	initialDelay, maxDelay := fallbackReadInitialDelay, fallbackReadMaxDelay
	if r.readMaxAttempts > 0 {
		maxAttempts = r.readMaxAttempts
		initialDelay = r.readInitialDelay
		maxDelay = r.readMaxDelay
	}
	return retryFallbackRead(ctx, maxAttempts, initialDelay, maxDelay, func() error {
		return r.readFallback(ctx, data)
	})
}

// retryFallbackRead retries only not-found responses because LiteLLM fallback
// updates can take time to propagate between proxy workers. The delay is
// configurable so tests can exercise retry behavior without sleeping.
func retryFallbackRead(ctx context.Context, maxAttempts int, initialDelay, maxDelay time.Duration, read func() error) error {
	if maxAttempts < 1 {
		return fmt.Errorf("fallback read requires at least one attempt")
	}

	delay := initialDelay
	var err error

	for attempt := 0; attempt < maxAttempts; attempt++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		err = read()
		if err == nil || !IsNotFoundError(err) {
			return err
		}
		if attempt == maxAttempts-1 {
			break
		}

		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			}
		}

		if delay < maxDelay {
			delay *= 2
			if delay > maxDelay {
				delay = maxDelay
			}
		}
	}

	return err
}

func (r *FallbackResource) readFallback(ctx context.Context, data *FallbackResourceModel) error {
	endpoint := fallbackEndpoint(data.Model.ValueString(), data.FallbackType.ValueString())
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
		return err
	}

	if err := validateFallbackReadResponse(result, data.Model.ValueString(), data.FallbackType.ValueString()); err != nil {
		return err
	}
	fallbackModels := result["fallback_models"].([]interface{})
	list := make([]attr.Value, 0, len(fallbackModels))
	for _, model := range fallbackModels {
		list = append(list, types.StringValue(model.(string)))
	}
	data.FallbackModels, _ = types.ListValue(types.StringType, list)
	data.ID = types.StringValue(data.Model.ValueString() + ":" + data.FallbackType.ValueString())
	return nil
}

func validateFallbackReadResponse(result map[string]interface{}, expectedModel, expectedType string) error {
	model, modelOK := result["model"].(string)
	fallbackType, typeOK := result["fallback_type"].(string)
	fallbackModels, modelsOK := result["fallback_models"].([]interface{})
	if !modelOK || model == "" || model != expectedModel || !typeOK || !isSupportedFallbackType(fallbackType) || fallbackType != expectedType || !modelsOK {
		return fmt.Errorf("LiteLLM returned a malformed fallback response; identity and response details were omitted")
	}
	for _, fallbackModel := range fallbackModels {
		if _, ok := fallbackModel.(string); !ok {
			return fmt.Errorf("LiteLLM returned a malformed fallback response; identity and response details were omitted")
		}
	}
	return nil
}
