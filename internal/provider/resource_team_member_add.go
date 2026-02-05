package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &TeamMemberAddResource{}
var _ resource.ResourceWithImportState = &TeamMemberAddResource{}

func NewTeamMemberAddResource() resource.Resource {
	return &TeamMemberAddResource{}
}

type TeamMemberAddResource struct {
	client *Client
}

type TeamMemberAddResourceModel struct {
	ID              types.String  `tfsdk:"id"`
	TeamID          types.String  `tfsdk:"team_id"`
	Members         types.Set     `tfsdk:"member"`
	MaxBudgetInTeam types.Float64 `tfsdk:"max_budget_in_team"`
}

type MemberModel struct {
	UserID    types.String `tfsdk:"user_id"`
	UserEmail types.String `tfsdk:"user_email"`
	Role      types.String `tfsdk:"role"`
}

func (r *TeamMemberAddResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_member_add"
}

func (r *TeamMemberAddResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages multiple LiteLLM team members.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Resource ID (team_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"team_id": schema.StringAttribute{
				Description: "Team ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"max_budget_in_team": schema.Float64Attribute{
				Description: "Maximum budget for members in the team.",
				Optional:    true,
			},
		},
		Blocks: map[string]schema.Block{
			"member": schema.SetNestedBlock{
				Description: "Team members.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"user_id": schema.StringAttribute{
							Description: "User ID.",
							Optional:    true,
						},
						"user_email": schema.StringAttribute{
							Description: "User email.",
							Optional:    true,
						},
						"role": schema.StringAttribute{
							Description: "Role (admin, user).",
							Required:    true,
							Validators: []validator.String{
								stringvalidator.OneOf("admin", "user"),
							},
						},
					},
				},
			},
		},
	}
}

func (r *TeamMemberAddResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TeamMemberAddResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TeamMemberAddResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	members := r.extractMembers(ctx, &data)

	memberReq := map[string]interface{}{
		"member":  members,
		"team_id": data.TeamID.ValueString(),
	}

	if !data.MaxBudgetInTeam.IsNull() && !data.MaxBudgetInTeam.IsUnknown() {
		memberReq["max_budget_in_team"] = data.MaxBudgetInTeam.ValueFloat64()
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_add", memberReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add team members: %s", err))
		return
	}

	data.ID = data.TeamID

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamMemberAddResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TeamMemberAddResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// No specific endpoint to read team members
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamMemberAddResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TeamMemberAddResourceModel
	var state TeamMemberAddResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plan.ID = state.ID

	oldMembers := r.extractMembersMap(ctx, &state)
	newMembers := r.extractMembersMap(ctx, &plan)

	// Delete removed members
	for key, oldMember := range oldMembers {
		if _, exists := newMembers[key]; !exists {
			deleteReq := map[string]interface{}{
				"team_id": plan.TeamID.ValueString(),
			}
			if oldMember["user_id"] != "" {
				deleteReq["user_id"] = oldMember["user_id"]
			}
			if oldMember["user_email"] != "" {
				deleteReq["user_email"] = oldMember["user_email"]
			}
			if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_delete", deleteReq, nil); err != nil {
				resp.Diagnostics.AddWarning("Delete Error", fmt.Sprintf("Failed to remove member %s: %s", key, err))
			}
		}
	}

	// Add new members
	var membersToAdd []map[string]interface{}
	for key, newMember := range newMembers {
		if _, exists := oldMembers[key]; !exists {
			memberData := map[string]interface{}{
				"role": newMember["role"],
			}
			if newMember["user_id"] != "" {
				memberData["user_id"] = newMember["user_id"]
			}
			if newMember["user_email"] != "" {
				memberData["user_email"] = newMember["user_email"]
			}
			membersToAdd = append(membersToAdd, memberData)
		}
	}

	if len(membersToAdd) > 0 {
		memberReq := map[string]interface{}{
			"member":  membersToAdd,
			"team_id": plan.TeamID.ValueString(),
		}
		if !plan.MaxBudgetInTeam.IsNull() && !plan.MaxBudgetInTeam.IsUnknown() {
			memberReq["max_budget_in_team"] = plan.MaxBudgetInTeam.ValueFloat64()
		}
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_add", memberReq, nil); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add team members: %s", err))
			return
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TeamMemberAddResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TeamMemberAddResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	members := r.extractMembersMap(ctx, &data)

	for _, member := range members {
		deleteReq := map[string]interface{}{
			"team_id": data.TeamID.ValueString(),
		}
		if member["user_id"] != "" {
			deleteReq["user_id"] = member["user_id"]
		}
		if member["user_email"] != "" {
			deleteReq["user_email"] = member["user_email"]
		}
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/member_delete", deleteReq, nil); err != nil {
			if !IsNotFoundError(err) {
				resp.Diagnostics.AddWarning("Delete Error", fmt.Sprintf("Failed to remove member: %s", err))
			}
		}
	}
}

func (r *TeamMemberAddResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team_id"), req.ID)...)
}

func (r *TeamMemberAddResource) extractMembers(ctx context.Context, data *TeamMemberAddResourceModel) []map[string]interface{} {
	if data.Members.IsNull() {
		return nil
	}

	var members []map[string]interface{}
	elements := data.Members.Elements()
	for _, elem := range elements {
		obj := elem.(types.Object)
		attrs := obj.Attributes()

		memberData := map[string]interface{}{
			"role": attrs["role"].(types.String).ValueString(),
		}
		if userID := attrs["user_id"].(types.String); !userID.IsNull() && userID.ValueString() != "" {
			memberData["user_id"] = userID.ValueString()
		}
		if userEmail := attrs["user_email"].(types.String); !userEmail.IsNull() && userEmail.ValueString() != "" {
			memberData["user_email"] = userEmail.ValueString()
		}
		members = append(members, memberData)
	}
	return members
}

func (r *TeamMemberAddResource) extractMembersMap(ctx context.Context, data *TeamMemberAddResourceModel) map[string]map[string]string {
	result := make(map[string]map[string]string)
	if data.Members.IsNull() {
		return result
	}

	elements := data.Members.Elements()
	for _, elem := range elements {
		obj := elem.(types.Object)
		attrs := obj.Attributes()

		member := map[string]string{
			"role": attrs["role"].(types.String).ValueString(),
		}

		var key string
		if userID := attrs["user_id"].(types.String); !userID.IsNull() && userID.ValueString() != "" {
			member["user_id"] = userID.ValueString()
			key = "id:" + userID.ValueString()
		}
		if userEmail := attrs["user_email"].(types.String); !userEmail.IsNull() && userEmail.ValueString() != "" {
			member["user_email"] = userEmail.ValueString()
			if key == "" {
				key = "email:" + userEmail.ValueString()
			}
		}
		if key != "" {
			result[key] = member
		}
	}
	return result
}

// MemberObjectType returns the object type for members.
func MemberObjectType() types.ObjectType {
	return types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"user_id":    types.StringType,
			"user_email": types.StringType,
			"role":       types.StringType,
		},
	}
}
