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

var _ resource.Resource = &GuardrailResource{}
var _ resource.ResourceWithImportState = &GuardrailResource{}

func NewGuardrailResource() resource.Resource {
	return &GuardrailResource{}
}

type GuardrailResource struct {
	client *Client
}

type GuardrailResourceModel struct {
	ID            types.String `tfsdk:"id"`
	GuardrailID   types.String `tfsdk:"guardrail_id"`
	GuardrailName types.String `tfsdk:"guardrail_name"`
	Guardrail     types.String `tfsdk:"guardrail"`
	Mode          types.String `tfsdk:"mode"`
	DefaultOn     types.Bool   `tfsdk:"default_on"`
	LitellmParams types.String `tfsdk:"litellm_params"`
	GuardrailInfo types.String `tfsdk:"guardrail_info"`
	CreatedAt     types.String `tfsdk:"created_at"`
	UpdatedAt     types.String `tfsdk:"updated_at"`
}

func (r *GuardrailResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_guardrail"
}

func (r *GuardrailResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM guardrail. Guardrails provide content filtering, PII detection, prompt injection protection, and more.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this guardrail (same as guardrail_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"guardrail_id": schema.StringAttribute{
				Description: "The unique guardrail ID. Generated if not specified.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"guardrail_name": schema.StringAttribute{
				Description: "Human-readable name for the guardrail.",
				Required:    true,
			},
			"guardrail": schema.StringAttribute{
				Description: "The guardrail integration type (e.g., 'aporia', 'bedrock', 'lakera', 'presidio', 'openai_moderation', 'hide_secrets').",
				Required:    true,
			},
			"mode": schema.StringAttribute{
				Description: "When to apply the guardrail. Can be a single value or JSON array (e.g., 'pre_call', 'post_call', 'during_call', '[\"pre_call\", \"post_call\"]').",
				Required:    true,
			},
			"default_on": schema.BoolAttribute{
				Description: "Whether the guardrail is enabled by default for all requests.",
				Optional:    true,
			},
			"litellm_params": schema.StringAttribute{
				Description: "JSON string containing additional provider-specific parameters for the guardrail.",
				Optional:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"guardrail_info": schema.StringAttribute{
				Description: "JSON string containing additional metadata for the guardrail.",
				Optional:    true,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the guardrail was created.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the guardrail was last updated.",
				Computed:    true,
			},
		},
	}
}

func (r *GuardrailResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *GuardrailResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data GuardrailResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	guardrailReq := r.buildGuardrailRequest(ctx, &data)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/guardrails", guardrailReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create guardrail: %s", err))
		return
	}

	// Extract guardrail_id from response
	if guardrailID, ok := result["guardrail_id"].(string); ok {
		data.GuardrailID = types.StringValue(guardrailID)
		data.ID = types.StringValue(guardrailID)
	} else if guardrail, ok := result["guardrail"].(map[string]interface{}); ok {
		if guardrailID, ok := guardrail["guardrail_id"].(string); ok {
			data.GuardrailID = types.StringValue(guardrailID)
			data.ID = types.StringValue(guardrailID)
		}
	}

	// Read back for full state
	if err := r.readGuardrail(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Guardrail created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GuardrailResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data GuardrailResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readGuardrail(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read guardrail: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GuardrailResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data GuardrailResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state GuardrailResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Preserve IDs
	data.ID = state.ID
	data.GuardrailID = state.GuardrailID

	guardrailReq := r.buildGuardrailRequest(ctx, &data)

	endpoint := fmt.Sprintf("/guardrails/%s", data.GuardrailID.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "PUT", endpoint, guardrailReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update guardrail: %s", err))
		return
	}

	// Read back for full state
	if err := r.readGuardrail(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Guardrail updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GuardrailResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data GuardrailResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/guardrails/%s", data.GuardrailID.ValueString())
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete guardrail: %s", err))
			return
		}
	}
}

func (r *GuardrailResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("guardrail_id"), req.ID)...)
}

func (r *GuardrailResource) buildGuardrailRequest(ctx context.Context, data *GuardrailResourceModel) map[string]interface{} {
	litellmParams := map[string]interface{}{
		"guardrail": data.Guardrail.ValueString(),
	}

	// Parse mode - can be string or array
	modeStr := data.Mode.ValueString()
	if len(modeStr) > 0 && modeStr[0] == '[' {
		var modeArray []string
		if err := json.Unmarshal([]byte(modeStr), &modeArray); err == nil {
			litellmParams["mode"] = modeArray
		} else {
			litellmParams["mode"] = modeStr
		}
	} else {
		litellmParams["mode"] = modeStr
	}

	if !data.DefaultOn.IsNull() {
		litellmParams["default_on"] = data.DefaultOn.ValueBool()
	}

	// Merge additional litellm_params if provided
	if !data.LitellmParams.IsNull() && data.LitellmParams.ValueString() != "" {
		var additionalParams map[string]interface{}
		if err := json.Unmarshal([]byte(data.LitellmParams.ValueString()), &additionalParams); err == nil {
			for k, v := range additionalParams {
				litellmParams[k] = v
			}
		}
	}

	guardrail := map[string]interface{}{
		"guardrail_name": data.GuardrailName.ValueString(),
		"litellm_params": litellmParams,
	}

	if !data.GuardrailID.IsNull() && data.GuardrailID.ValueString() != "" {
		guardrail["guardrail_id"] = data.GuardrailID.ValueString()
	}

	if !data.GuardrailInfo.IsNull() && data.GuardrailInfo.ValueString() != "" {
		var guardrailInfo map[string]interface{}
		if err := json.Unmarshal([]byte(data.GuardrailInfo.ValueString()), &guardrailInfo); err == nil {
			guardrail["guardrail_info"] = guardrailInfo
		}
	}

	return map[string]interface{}{
		"guardrail": guardrail,
	}
}

func (r *GuardrailResource) readGuardrail(ctx context.Context, data *GuardrailResourceModel) error {
	guardrailID := data.GuardrailID.ValueString()
	if guardrailID == "" {
		guardrailID = data.ID.ValueString()
	}

	endpoint := fmt.Sprintf("/guardrails/%s/info", guardrailID)

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// Update fields from response
	if id, ok := result["guardrail_id"].(string); ok {
		data.GuardrailID = types.StringValue(id)
		data.ID = types.StringValue(id)
	}
	if name, ok := result["guardrail_name"].(string); ok {
		data.GuardrailName = types.StringValue(name)
	}
	if createdAt, ok := result["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(createdAt)
	}
	if updatedAt, ok := result["updated_at"].(string); ok {
		data.UpdatedAt = types.StringValue(updatedAt)
	}

	// Handle litellm_params
	if litellmParams, ok := result["litellm_params"].(map[string]interface{}); ok {
		if guardrail, ok := litellmParams["guardrail"].(string); ok {
			data.Guardrail = types.StringValue(guardrail)
		}
		if defaultOn, ok := litellmParams["default_on"].(bool); ok {
			data.DefaultOn = types.BoolValue(defaultOn)
		}

		// Handle mode (can be string or array)
		if mode, ok := litellmParams["mode"].(string); ok {
			data.Mode = types.StringValue(mode)
		} else if modeArray, ok := litellmParams["mode"].([]interface{}); ok {
			if jsonBytes, err := json.Marshal(modeArray); err == nil {
				data.Mode = types.StringValue(string(jsonBytes))
			}
		}

		// Get the keys from the user's configuration
		configuredKeys := make(map[string]bool)
		if !data.LitellmParams.IsNull() {
			var configuredParams map[string]interface{}
			if err := json.Unmarshal([]byte(data.LitellmParams.ValueString()), &configuredParams); err == nil {
				for k := range configuredParams {
					configuredKeys[k] = true
				}
			}
		}

		// Store other litellm_params as JSON (excluding guardrail, mode, default_on)
		// and only including keys that are in the user's configuration
		otherParams := make(map[string]interface{})
		for k, v := range litellmParams {
			if k != "guardrail" && k != "mode" && k != "default_on" {
				if configuredKeys[k] {
					otherParams[k] = v
				}
			}
		}
		if len(otherParams) > 0 {
			if jsonBytes, err := json.Marshal(otherParams); err == nil {
				data.LitellmParams = types.StringValue(string(jsonBytes))
			}
		} else {
			data.LitellmParams = types.StringNull()
		}
	}

	// Handle guardrail_info
	if guardrailInfo, ok := result["guardrail_info"].(map[string]interface{}); ok && len(guardrailInfo) > 0 {
		if jsonBytes, err := json.Marshal(guardrailInfo); err == nil {
			data.GuardrailInfo = types.StringValue(string(jsonBytes))
		}
	}

	return nil
}
