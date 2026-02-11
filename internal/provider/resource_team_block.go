package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &TeamBlockResource{}
var _ resource.ResourceWithImportState = &TeamBlockResource{}

func NewTeamBlockResource() resource.Resource {
	return &TeamBlockResource{}
}

// TeamBlockResource implements a stateful block for a LiteLLM team.
// Creating this resource blocks the team, destroying it unblocks the team.
type TeamBlockResource struct {
	client *Client
}

type TeamBlockResourceModel struct {
	ID      types.String `tfsdk:"id"`
	TeamID  types.String `tfsdk:"team_id"`
	Blocked types.Bool   `tfsdk:"blocked"`
}

func (r *TeamBlockResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_block"
}

func (r *TeamBlockResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the blocked state of a LiteLLM team. Creating this resource blocks the team; destroying it unblocks the team.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this block (same as team_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"team_id": schema.StringAttribute{
				Description: "The team ID to block/unblock.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the team is currently blocked. Always true when this resource exists.",
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *TeamBlockResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TeamBlockResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TeamBlockResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Block the team
	blockReq := map[string]interface{}{
		"team_id": data.TeamID.ValueString(),
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/block", blockReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to block team: %s", err))
		return
	}

	data.ID = data.TeamID
	data.Blocked = types.BoolValue(true)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamBlockResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TeamBlockResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Check if the team is still blocked
	endpoint := fmt.Sprintf("/team/info?team_id=%s", data.TeamID.ValueString())

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		if IsNotFoundError(err) {
			// Team no longer exists, remove the block resource
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read team: %s", err))
		return
	}

	// The /team/info endpoint may return team data nested inside "team_info"
	teamInfo := result
	if nested, ok := result["team_info"].(map[string]interface{}); ok {
		teamInfo = nested
	}

	// Check blocked status
	if blocked, ok := teamInfo["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
		if !blocked {
			// Team is no longer blocked, remove this resource
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

func (r *TeamBlockResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// TeamID attribute has RequiresReplace, so this should never be called for team_id changes.
	// Just preserve state.
	var data TeamBlockResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state TeamBlockResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = state.ID
	data.Blocked = types.BoolValue(true)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamBlockResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TeamBlockResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Unblock the team
	unblockReq := map[string]interface{}{
		"team_id": data.TeamID.ValueString(),
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/unblock", unblockReq, nil); err != nil {
		// Don't fail if the team doesn't exist
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to unblock team: %s", err))
			return
		}
	}
}

func (r *TeamBlockResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Import by team_id
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("blocked"), true)...)
}
