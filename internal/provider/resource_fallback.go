package provider

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &FallbackResource{}
var _ resource.ResourceWithImportState = &FallbackResource{}

func NewFallbackResource() resource.Resource {
	return &FallbackResource{}
}

type FallbackResource struct {
	client *Client
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
				Description: "Type of fallback: 'general' (default), 'context_window', or 'content_policy'.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("general"),
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

	fallbackReq := r.buildFallbackRequest(ctx, &data)
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/fallback", fallbackReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create fallback: %s", err))
		return
	}

	data.ID = types.StringValue(data.Model.ValueString() + ":" + data.FallbackType.ValueString())

	if err := r.readFallback(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Fallback created but failed to read back: %s", err))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FallbackResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data FallbackResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readFallback(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read fallback: %s", err))
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

	fallbackReq := r.buildFallbackRequest(ctx, &data)
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/fallback", fallbackReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update fallback: %s", err))
		return
	}

	if err := r.readFallback(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Fallback updated but failed to read back: %s", err))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FallbackResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data FallbackResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := fmt.Sprintf("/fallback/%s?fallback_type=%s",
		url.PathEscape(data.Model.ValueString()),
		url.QueryEscape(data.FallbackType.ValueString()))
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete fallback: %s", err))
			return
		}
	}
}

func (r *FallbackResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// ID format: model:fallback_type
	importID := req.ID
	data := FallbackResourceModel{}
	for i := 0; i < len(importID); i++ {
		if importID[i] == ':' {
			data.Model = types.StringValue(importID[:i])
			if i+1 < len(importID) {
				data.FallbackType = types.StringValue(importID[i+1:])
			} else {
				data.FallbackType = types.StringValue("general")
			}
			break
		}
	}
	if data.Model.ValueString() == "" {
		data.Model = types.StringValue(importID)
		data.FallbackType = types.StringValue("general")
	}
	if err := r.readFallback(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Read Error", fmt.Sprintf("Unable to read fallback after import: %s", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FallbackResource) buildFallbackRequest(ctx context.Context, data *FallbackResourceModel) map[string]interface{} {
	var models []string
	data.FallbackModels.ElementsAs(ctx, &models, false)
	return map[string]interface{}{
		"model":           data.Model.ValueString(),
		"fallback_models": models,
		"fallback_type":   data.FallbackType.ValueString(),
	}
}

func (r *FallbackResource) readFallback(ctx context.Context, data *FallbackResourceModel) error {
	endpoint := fmt.Sprintf("/fallback/%s?fallback_type=%s",
		url.PathEscape(data.Model.ValueString()),
		url.QueryEscape(data.FallbackType.ValueString()))
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	if fallbackModels, ok := result["fallback_models"].([]interface{}); ok {
		list := make([]attr.Value, 0, len(fallbackModels))
		for _, m := range fallbackModels {
			if s, ok := m.(string); ok {
				list = append(list, types.StringValue(s))
			}
		}
		data.FallbackModels, _ = types.ListValue(types.StringType, list)
	}
	if ft, ok := result["fallback_type"].(string); ok {
		data.FallbackType = types.StringValue(ft)
	}
	data.ID = types.StringValue(data.Model.ValueString() + ":" + data.FallbackType.ValueString())
	return nil
}
