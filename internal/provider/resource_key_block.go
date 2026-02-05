package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &KeyBlockResource{}
var _ resource.ResourceWithImportState = &KeyBlockResource{}

func NewKeyBlockResource() resource.Resource {
	return &KeyBlockResource{}
}

// KeyBlockResource implements a stateful block for a LiteLLM key.
// Creating this resource blocks the key, destroying it unblocks the key.
type KeyBlockResource struct {
	client *Client
}

type KeyBlockResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Key     types.String `tfsdk:"key"`
	Blocked types.Bool   `tfsdk:"blocked"`
}

func (r *KeyBlockResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_key_block"
}

func (r *KeyBlockResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the blocked state of a LiteLLM API key. Creating this resource blocks the key; destroying it unblocks the key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this block (same as key value).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"key": schema.StringAttribute{
				Description: "The API key value to block/unblock.",
				Required:    true,
				Sensitive:   true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the key is currently blocked. Always true when this resource exists.",
				Computed:    true,
			},
		},
	}
}

func (r *KeyBlockResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *KeyBlockResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data KeyBlockResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Block the key
	blockReq := map[string]interface{}{
		"key": data.Key.ValueString(),
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/key/block", blockReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to block key: %s", err))
		return
	}

	data.ID = data.Key
	data.Blocked = types.BoolValue(true)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *KeyBlockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data KeyBlockResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if the key is still blocked
	endpoint := fmt.Sprintf("/key/info?key=%s", data.Key.ValueString())

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		if IsNotFoundError(err) {
			// Key no longer exists, remove the block resource
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read key: %s", err))
		return
	}

	// Check blocked status
	if blocked, ok := result["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
		if !blocked {
			// Key is no longer blocked, remove this resource
			resp.State.RemoveResource(ctx)
			return
		}
	} else {
		// If blocked field doesn't exist or is not a bool, assume not blocked
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *KeyBlockResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Key attribute has RequiresReplace, so this should never be called for key changes.
	// Just preserve state.
	var data KeyBlockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state KeyBlockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = state.ID
	data.Blocked = types.BoolValue(true)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *KeyBlockResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data KeyBlockResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Unblock the key
	unblockReq := map[string]interface{}{
		"key": data.Key.ValueString(),
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/key/unblock", unblockReq, nil); err != nil {
		// Don't fail if the key doesn't exist
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to unblock key: %s", err))
			return
		}
	}
}

func (r *KeyBlockResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import by key value
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("blocked"), true)...)
}
