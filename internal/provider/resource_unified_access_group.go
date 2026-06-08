package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &UnifiedAccessGroupResource{}
var _ resource.ResourceWithImportState = &UnifiedAccessGroupResource{}

func NewUnifiedAccessGroupResource() resource.Resource {
	return &UnifiedAccessGroupResource{}
}

type UnifiedAccessGroupResource struct {
	client *Client
}

type UnifiedAccessGroupResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	AccessGroupID      types.String `tfsdk:"access_group_id"`
	AccessGroupName    types.String `tfsdk:"access_group_name"`
	Description        types.String `tfsdk:"description"`
	AccessModelNames   types.List   `tfsdk:"access_model_names"`
	AccessMCPServerIDs types.List   `tfsdk:"access_mcp_server_ids"`
	AccessAgentIDs     types.List   `tfsdk:"access_agent_ids"`
	AssignedTeamIDs    types.List   `tfsdk:"assigned_team_ids"`
	AssignedKeyIDs     types.List   `tfsdk:"assigned_key_ids"`
	CreatedAt          types.String `tfsdk:"created_at"`
	CreatedBy          types.String `tfsdk:"created_by"`
	UpdatedAt          types.String `tfsdk:"updated_at"`
	UpdatedBy          types.String `tfsdk:"updated_by"`
}

func (r *UnifiedAccessGroupResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_unified_access_group"
}

func (r *UnifiedAccessGroupResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM unified access group. Unified access groups can grant access to models, MCP servers, and agents and can be assigned to teams or keys.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this unified access group (same as access_group_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"access_group_id": schema.StringAttribute{
				Description: "The unique identifier for this unified access group.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"access_group_name": schema.StringAttribute{
				Description: "The display/name of the unified access group.",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "Description of the unified access group.",
				Optional:    true,
			},
			"access_model_names": schema.ListAttribute{
				Description: "Model names this access group grants access to.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"access_mcp_server_ids": schema.ListAttribute{
				Description: "MCP server IDs this access group grants access to.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"access_agent_ids": schema.ListAttribute{
				Description: "Agent IDs this access group grants access to.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"assigned_team_ids": schema.ListAttribute{
				Description: "Team IDs assigned to this access group.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"assigned_key_ids": schema.ListAttribute{
				Description: "Key IDs assigned to this access group.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"created_at": schema.StringAttribute{
				Description: "Timestamp when the access group was created.",
				Computed:    true,
			},
			"created_by": schema.StringAttribute{
				Description: "User who created the access group.",
				Computed:    true,
			},
			"updated_at": schema.StringAttribute{
				Description: "Timestamp when the access group was last updated.",
				Computed:    true,
			},
			"updated_by": schema.StringAttribute{
				Description: "User who last updated the access group.",
				Computed:    true,
			},
		},
	}
}

func (r *UnifiedAccessGroupResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData))
		return
	}
	r.client = client
}

func (r *UnifiedAccessGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data UnifiedAccessGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	createReq := buildUnifiedAccessGroupRequest(ctx, &data, false)
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/v1/access_group", createReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create unified access group: %s", err))
		return
	}

	readUnifiedAccessGroupResponse(ctx, result, &data)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *UnifiedAccessGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data UnifiedAccessGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.readUnifiedAccessGroup(ctx, &data); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read unified access group: %s", err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *UnifiedAccessGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data UnifiedAccessGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state UnifiedAccessGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ID = state.ID
	data.AccessGroupID = state.AccessGroupID

	updateReq := buildUnifiedAccessGroupRequest(ctx, &data, true)
	endpoint := fmt.Sprintf("/v1/access_group/%s", data.AccessGroupID.ValueString())
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "PUT", endpoint, updateReq, &result); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update unified access group: %s", err))
		return
	}

	readUnifiedAccessGroupResponse(ctx, result, &data)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *UnifiedAccessGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data UnifiedAccessGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := data.AccessGroupID.ValueString()
	if id == "" {
		id = data.ID.ValueString()
	}
	endpoint := fmt.Sprintf("/v1/access_group/%s", id)
	if err := r.client.DoRequestWithResponse(ctx, "DELETE", endpoint, nil, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete unified access group: %s", err))
		}
	}
}

func (r *UnifiedAccessGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("access_group_id"), req.ID)...)
}

func (r *UnifiedAccessGroupResource) readUnifiedAccessGroup(ctx context.Context, data *UnifiedAccessGroupResourceModel) error {
	id := data.AccessGroupID.ValueString()
	if id == "" {
		id = data.ID.ValueString()
	}
	endpoint := fmt.Sprintf("/v1/access_group/%s", id)
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}
	readUnifiedAccessGroupResponse(ctx, result, data)
	return nil
}

func buildUnifiedAccessGroupRequest(ctx context.Context, data *UnifiedAccessGroupResourceModel, includeOptionalName bool) map[string]interface{} {
	req := map[string]interface{}{}
	if !data.AccessGroupName.IsNull() && !data.AccessGroupName.IsUnknown() && (includeOptionalName || data.AccessGroupName.ValueString() != "") {
		req["access_group_name"] = data.AccessGroupName.ValueString()
	}
	if !data.Description.IsNull() && !data.Description.IsUnknown() {
		req["description"] = data.Description.ValueString()
	}
	addStringListToRequest(ctx, req, "access_model_names", data.AccessModelNames)
	addStringListToRequest(ctx, req, "access_mcp_server_ids", data.AccessMCPServerIDs)
	addStringListToRequest(ctx, req, "access_agent_ids", data.AccessAgentIDs)
	addStringListToRequest(ctx, req, "assigned_team_ids", data.AssignedTeamIDs)
	addStringListToRequest(ctx, req, "assigned_key_ids", data.AssignedKeyIDs)
	return req
}

func addStringListToRequest(ctx context.Context, req map[string]interface{}, key string, value types.List) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	var values []string
	value.ElementsAs(ctx, &values, false)
	req[key] = values
}

func readUnifiedAccessGroupResponse(ctx context.Context, result map[string]interface{}, data *UnifiedAccessGroupResourceModel) {
	if id, ok := result["access_group_id"].(string); ok {
		data.AccessGroupID = types.StringValue(id)
		data.ID = types.StringValue(id)
	}
	if name, ok := result["access_group_name"].(string); ok {
		data.AccessGroupName = types.StringValue(name)
	}
	if description, ok := result["description"].(string); ok && !data.Description.IsNull() {
		data.Description = types.StringValue(description)
	}
	setListFromResponse(ctx, &data.AccessModelNames, result["access_model_names"])
	setListFromResponse(ctx, &data.AccessMCPServerIDs, result["access_mcp_server_ids"])
	setListFromResponse(ctx, &data.AccessAgentIDs, result["access_agent_ids"])
	setListFromResponse(ctx, &data.AssignedTeamIDs, result["assigned_team_ids"])
	setListFromResponse(ctx, &data.AssignedKeyIDs, result["assigned_key_ids"])
	if createdAt, ok := result["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(createdAt)
	}
	if createdBy, ok := result["created_by"].(string); ok {
		data.CreatedBy = types.StringValue(createdBy)
	}
	if updatedAt, ok := result["updated_at"].(string); ok {
		data.UpdatedAt = types.StringValue(updatedAt)
	}
	if updatedBy, ok := result["updated_by"].(string); ok {
		data.UpdatedBy = types.StringValue(updatedBy)
	}
}

func setListFromResponse(ctx context.Context, target *types.List, raw interface{}) {
	if values, ok := raw.([]interface{}); ok {
		items := make([]attr.Value, 0, len(values))
		for _, value := range values {
			if str, ok := value.(string); ok {
				items = append(items, types.StringValue(str))
			}
		}
		*target, _ = types.ListValue(types.StringType, items)
	} else if target.IsUnknown() {
		*target, _ = types.ListValue(types.StringType, []attr.Value{})
	}
}
