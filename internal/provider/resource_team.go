package provider

import (
	"context"
	"fmt"
	"net/url"
	"regexp"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

var _ resource.Resource = &TeamResource{}
var _ resource.ResourceWithImportState = &TeamResource{}

// LiteLLM v1.98 normalizes the four word aliases before computing reset times,
// supports week units in both duration_in_seconds and standardized reset handling,
// and rejects multi-month resets in its monthly reset handler.
var budgetDurationPattern = regexp.MustCompile(`^([1-9][0-9]*(s|m|h|d|w)|1mo|hourly|daily|weekly|monthly)$`)

const budgetDurationValidationMessage = `must be a positive integer with unit s, m, h, d, or w; one of hourly, daily, weekly, or monthly; or exactly 1mo`

func NewTeamResource() resource.Resource {
	return &TeamResource{}
}

type TeamResource struct {
	client *Client
}

type TeamResourceModel struct {
	ID                    types.String  `tfsdk:"id"`
	TeamID                types.String  `tfsdk:"team_id"`
	TeamAlias             types.String  `tfsdk:"team_alias"`
	OrganizationID        types.String  `tfsdk:"organization_id"`
	AccessGroupIDs        types.Set     `tfsdk:"access_group_ids"`
	Metadata              types.Map     `tfsdk:"metadata"`
	TPMLimit              types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit              types.Int64   `tfsdk:"rpm_limit"`
	TPMLimitType          types.String  `tfsdk:"tpm_limit_type"`
	RPMLimitType          types.String  `tfsdk:"rpm_limit_type"`
	MaxBudget             types.Float64 `tfsdk:"max_budget"`
	BudgetDuration        types.String  `tfsdk:"budget_duration"`
	Models                types.List    `tfsdk:"models"`
	ModelAliases          types.Map     `tfsdk:"model_aliases"`
	ModelRPMLimit         types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit         types.Map     `tfsdk:"model_tpm_limit"`
	Tags                  types.List    `tfsdk:"tags"`
	Guardrails            types.List    `tfsdk:"guardrails"`
	Prompts               types.List    `tfsdk:"prompts"`
	Blocked               types.Bool    `tfsdk:"blocked"`
	TeamMemberPermissions types.List    `tfsdk:"team_member_permissions"`
	TeamMemberBudget      types.Float64 `tfsdk:"team_member_budget"`
	MemberBudgetDuration  types.String  `tfsdk:"team_member_budget_duration"`
	TeamMemberRPMLimit    types.Int64   `tfsdk:"team_member_rpm_limit"`
	TeamMemberTPMLimit    types.Int64   `tfsdk:"team_member_tpm_limit"`
	RouterSettings        types.Object  `tfsdk:"router_settings"`
}

type RouterSettingsModel struct {
	Fallbacks              types.List `tfsdk:"fallbacks"`
	ContextWindowFallbacks types.List `tfsdk:"context_window_fallbacks"`
}

type FallbackEntryModel struct {
	Model          types.String `tfsdk:"model"`
	FallbackModels types.List   `tfsdk:"fallback_models"`
}

func teamIDForCreate(configured types.String) string {
	if !configured.IsNull() && !configured.IsUnknown() && configured.ValueString() != "" {
		return configured.ValueString()
	}
	return uuid.New().String()
}

func stringSetFromAPI(ctx context.Context, value interface{}) (types.Set, error) {
	values := []string{}
	switch typed := value.(type) {
	case nil:
	case []string:
		values = typed
	case []interface{}:
		values = make([]string, 0, len(typed))
		for _, item := range typed {
			stringValue, ok := item.(string)
			if !ok {
				return types.SetNull(types.StringType), fmt.Errorf("expected a string, got %T", item)
			}
			values = append(values, stringValue)
		}
	default:
		return types.SetNull(types.StringType), fmt.Errorf("expected a list of strings, got %T", value)
	}

	set, diagnostics := types.SetValueFrom(ctx, types.StringType, values)
	if diagnostics.HasError() {
		return types.SetNull(types.StringType), fmt.Errorf("failed to convert string set: %v", diagnostics.Errors())
	}
	return set, nil
}

var fallbackEntryAttrTypes = map[string]attr.Type{
	"model":           types.StringType,
	"fallback_models": types.ListType{ElemType: types.StringType},
}

var routerSettingsAttrTypes = map[string]attr.Type{
	"fallbacks":                types.ListType{ElemType: types.ObjectType{AttrTypes: fallbackEntryAttrTypes}},
	"context_window_fallbacks": types.ListType{ElemType: types.ObjectType{AttrTypes: fallbackEntryAttrTypes}},
}

func (r *TeamResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team"
}

func (r *TeamResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM team.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this team.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"team_id": schema.StringAttribute{
				Description: "The LiteLLM team ID. If not specified, the provider generates one. Changing it replaces the team.",
				Optional:    true,
				Computed:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"team_alias": schema.StringAttribute{
				Description: "User-defined team alias.",
				Required:    true,
			},
			"organization_id": schema.StringAttribute{
				Description: "Organization ID for the team.",
				Optional:    true,
			},
			"access_group_ids": schema.SetAttribute{
				Description: "Access group IDs associated with this team. Order is ignored. Set an empty collection to detach all access groups.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Validators: []validator.Set{
					setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
			"metadata": schema.MapAttribute{
				Description: "Arbitrary metadata for the team.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"tpm_limit": schema.Int64Attribute{
				Description: "Tokens per minute limit for the team.",
				Optional:    true,
			},
			"rpm_limit": schema.Int64Attribute{
				Description: "Requests per minute limit for the team.",
				Optional:    true,
			},
			"tpm_limit_type": schema.StringAttribute{
				Description: "Create-only TPM limit enforcement type. LiteLLM v1.98 accepts guaranteed_throughput or best_effort_throughput for new teams. Changing this value replaces the team.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("guaranteed_throughput", "best_effort_throughput"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"rpm_limit_type": schema.StringAttribute{
				Description: "Create-only RPM limit enforcement type. LiteLLM v1.98 accepts guaranteed_throughput or best_effort_throughput for new teams. Changing this value replaces the team.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.OneOf("guaranteed_throughput", "best_effort_throughput"),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"max_budget": schema.Float64Attribute{
				Description: "Maximum budget for the team.",
				Optional:    true,
			},
			"budget_duration": schema.StringAttribute{
				Description: "Recurring team budget reset interval. Accepts positive s, m, h, d, or w durations; hourly, daily, weekly, or monthly; or exactly 1mo.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						budgetDurationPattern,
						budgetDurationValidationMessage,
					),
				},
			},
			"models": schema.ListAttribute{
				Description: "List of models the team can access.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"model_aliases": schema.MapAttribute{
				Description: "Model alias mappings for the team.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"model_rpm_limit": schema.MapAttribute{
				Description: "Per-model RPM limits for the team.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
				Validators:  []validator.Map{mapvalidator.NoNullValues()},
			},
			"model_tpm_limit": schema.MapAttribute{
				Description: "Per-model TPM limits for the team.",
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
				Validators:  []validator.Map{mapvalidator.NoNullValues()},
			},
			"tags": schema.ListAttribute{
				Description: "Tags for the team (for spend tracking and routing).",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"guardrails": schema.ListAttribute{
				Description: "Guardrails for the team.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"prompts": schema.ListAttribute{
				Description: "List of prompt IDs the team can access.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"blocked": schema.BoolAttribute{
				Description: "Whether the team is blocked.",
				Optional:    true,
				Computed:    true,
			},
			"team_member_permissions": schema.ListAttribute{
				Description: "List of permissions granted to team members.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"team_member_budget": schema.Float64Attribute{
				Description: "Default budget for team members.",
				Optional:    true,
			},
			"team_member_budget_duration": schema.StringAttribute{
				Description: "Default recurring budget reset interval for team memberships. Accepts positive s, m, h, d, or w durations; hourly, daily, weekly, or monthly; or exactly 1mo. LiteLLM applies it to new/default memberships and may backfill memberships without a budget; private member overrides are preserved.",
				Optional:    true,
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						budgetDurationPattern,
						budgetDurationValidationMessage,
					),
				},
			},
			"team_member_rpm_limit": schema.Int64Attribute{
				Description: "Default RPM limit for team members.",
				Optional:    true,
			},
			"team_member_tpm_limit": schema.Int64Attribute{
				Description: "Default TPM limit for team members.",
				Optional:    true,
			},
			"router_settings": schema.SingleNestedAttribute{
				Description: "Router settings for the team, including fallback configurations. " +
					"These override global fallback settings for requests made with this team's keys. " +
					"Resolution order: Key > Team > Global.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"fallbacks": schema.ListNestedAttribute{
						Description: "Fallback model chains triggered when a model call fails after retries.",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"model": schema.StringAttribute{
									Description: "The primary model name to configure fallbacks for.",
									Required:    true,
								},
								"fallback_models": schema.ListAttribute{
									Description: "Ordered list of fallback model names.",
									Required:    true,
									ElementType: types.StringType,
								},
							},
						},
					},
					"context_window_fallbacks": schema.ListNestedAttribute{
						Description: "Fallback model chains triggered when a context window exceeded error occurs.",
						Optional:    true,
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"model": schema.StringAttribute{
									Description: "The primary model name to configure fallbacks for.",
									Required:    true,
								},
								"fallback_models": schema.ListAttribute{
									Description: "Ordered list of fallback model names.",
									Required:    true,
									ElementType: types.StringType,
								},
							},
						},
					},
				},
			},
		},
	}
}

func (r *TeamResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := configuredClient(req.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *Client, got: %T.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *TeamResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TeamResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	teamID := teamIDForCreate(data.TeamID)
	data.TeamID = types.StringValue(teamID)
	teamReq, err := r.buildTeamRequest(ctx, &data, teamID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Numeric Map", err.Error())
		return
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/new", teamReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create team: %s", err))
		return
	}

	data.ID = types.StringValue(teamID)

	// Read back
	if err := r.readTeam(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Team created but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TeamResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	importedMarker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	if resp.Diagnostics.HasError() {
		return
	}
	imported := string(importedMarker) == "true"

	if err := r.readTeamWithNumericOwnership(ctx, &data, imported); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read team: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && imported {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
}

func (r *TeamResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data TeamResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state TeamResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = state.ID
	data.TeamID = state.TeamID
	if data.TeamID.IsNull() || data.TeamID.IsUnknown() {
		data.TeamID = state.ID
	}
	teamReq, err := r.buildTeamUpdateRequest(ctx, &data, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Numeric Map", err.Error())
		return
	}
	applyTeamNullableClears(teamReq, &state, &data)

	// LiteLLM's team-default budget handler ignores explicit nulls whenever the
	// same request also contains another non-null member-budget field. Split
	// clears into their own merge-patch before applying the remaining changes.
	if clearReq := extractTeamMemberBudgetClears(teamReq, data.ID.ValueString()); clearReq != nil {
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/update", clearReq, nil); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to clear team member budget defaults: %s", err))
			return
		}
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/update", teamReq, nil); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update team: %s", err))
		return
	}

	// Update permissions if changed
	if !data.TeamMemberPermissions.Equal(state.TeamMemberPermissions) {
		var permissions []string
		data.TeamMemberPermissions.ElementsAs(ctx, &permissions, false)
		permReq := map[string]interface{}{
			"team_id":                 data.ID.ValueString(),
			"team_member_permissions": permissions,
		}
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/permissions_update", permReq, nil); err != nil {
			resp.Diagnostics.AddWarning("Permissions Update Error", fmt.Sprintf("Failed to update permissions: %s", err))
		}
	}

	if err := r.readTeam(ctx, &data); err != nil {
		resp.Diagnostics.AddWarning("Read Error", fmt.Sprintf("Team updated but failed to read back: %s", err))
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TeamResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deleteReq := map[string]interface{}{
		"team_ids": []string{data.ID.ValueString()},
	}

	if err := r.client.DoRequestWithResponse(ctx, "POST", "/team/delete", deleteReq, nil); err != nil {
		if !IsNotFoundError(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete team: %s", err))
			return
		}
	}
}

func (r *TeamResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team_id"), req.ID)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
	}
}

func (r *TeamResource) buildTeamRequest(ctx context.Context, data *TeamResourceModel, teamID string) (map[string]interface{}, error) {
	teamReq := map[string]interface{}{
		"team_id":    teamID,
		"team_alias": data.TeamAlias.ValueString(),
	}

	// String fields - check IsNull, IsUnknown, and empty string
	if !data.OrganizationID.IsNull() && !data.OrganizationID.IsUnknown() && data.OrganizationID.ValueString() != "" {
		teamReq["organization_id"] = data.OrganizationID.ValueString()
	}
	if !data.TPMLimitType.IsNull() && !data.TPMLimitType.IsUnknown() && data.TPMLimitType.ValueString() != "" {
		teamReq["tpm_limit_type"] = data.TPMLimitType.ValueString()
	}
	if !data.RPMLimitType.IsNull() && !data.RPMLimitType.IsUnknown() && data.RPMLimitType.ValueString() != "" {
		teamReq["rpm_limit_type"] = data.RPMLimitType.ValueString()
	}
	if !data.BudgetDuration.IsNull() && !data.BudgetDuration.IsUnknown() && data.BudgetDuration.ValueString() != "" {
		teamReq["budget_duration"] = data.BudgetDuration.ValueString()
	}

	if !data.AccessGroupIDs.IsNull() && !data.AccessGroupIDs.IsUnknown() {
		var accessGroupIDs []string
		data.AccessGroupIDs.ElementsAs(ctx, &accessGroupIDs, false)
		teamReq["access_group_ids"] = accessGroupIDs
	}

	// Numeric fields - check IsNull and IsUnknown
	if !data.TPMLimit.IsNull() && !data.TPMLimit.IsUnknown() {
		teamReq["tpm_limit"] = data.TPMLimit.ValueInt64()
	}
	if !data.RPMLimit.IsNull() && !data.RPMLimit.IsUnknown() {
		teamReq["rpm_limit"] = data.RPMLimit.ValueInt64()
	}
	if !data.MaxBudget.IsNull() && !data.MaxBudget.IsUnknown() {
		teamReq["max_budget"] = data.MaxBudget.ValueFloat64()
	}
	if !data.TeamMemberBudget.IsNull() && !data.TeamMemberBudget.IsUnknown() {
		teamReq["team_member_budget"] = data.TeamMemberBudget.ValueFloat64()
	}
	if !data.MemberBudgetDuration.IsNull() && !data.MemberBudgetDuration.IsUnknown() {
		teamReq["team_member_budget_duration"] = data.MemberBudgetDuration.ValueString()
	}
	if !data.TeamMemberRPMLimit.IsNull() && !data.TeamMemberRPMLimit.IsUnknown() {
		teamReq["team_member_rpm_limit"] = data.TeamMemberRPMLimit.ValueInt64()
	}
	if !data.TeamMemberTPMLimit.IsNull() && !data.TeamMemberTPMLimit.IsUnknown() {
		teamReq["team_member_tpm_limit"] = data.TeamMemberTPMLimit.ValueInt64()
	}

	// Boolean fields - check IsNull and IsUnknown
	if !data.Blocked.IsNull() && !data.Blocked.IsUnknown() {
		teamReq["blocked"] = data.Blocked.ValueBool()
	}

	// List fields - check IsNull, IsUnknown, and len > 0
	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		var models []string
		data.Models.ElementsAs(ctx, &models, false)
		if len(models) > 0 {
			teamReq["models"] = models
		}
	}

	if !data.Tags.IsNull() && !data.Tags.IsUnknown() {
		var tags []string
		data.Tags.ElementsAs(ctx, &tags, false)
		if len(tags) > 0 {
			teamReq["tags"] = tags
		}
	}

	if !data.Guardrails.IsNull() && !data.Guardrails.IsUnknown() {
		var guardrails []string
		data.Guardrails.ElementsAs(ctx, &guardrails, false)
		if len(guardrails) > 0 {
			teamReq["guardrails"] = guardrails
		}
	}

	if !data.Prompts.IsNull() && !data.Prompts.IsUnknown() {
		var prompts []string
		data.Prompts.ElementsAs(ctx, &prompts, false)
		if len(prompts) > 0 {
			teamReq["prompts"] = prompts
		}
	}

	if !data.TeamMemberPermissions.IsNull() && !data.TeamMemberPermissions.IsUnknown() {
		var permissions []string
		data.TeamMemberPermissions.ElementsAs(ctx, &permissions, false)
		if len(permissions) > 0 {
			teamReq["team_member_permissions"] = permissions
		}
	}

	// Map fields - check IsNull, IsUnknown, and len > 0
	if !data.ModelAliases.IsNull() && !data.ModelAliases.IsUnknown() {
		var modelAliases map[string]string
		data.ModelAliases.ElementsAs(ctx, &modelAliases, false)
		if len(modelAliases) > 0 {
			teamReq["model_aliases"] = modelAliases
		}
	}

	if !data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown() {
		modelRPM, err := int64RequestMap(data.ModelRPMLimit, "model_rpm_limit")
		if err != nil {
			return nil, err
		}
		teamReq["model_rpm_limit"] = modelRPM
	}

	if !data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown() {
		modelTPM, err := int64RequestMap(data.ModelTPMLimit, "model_tpm_limit")
		if err != nil {
			return nil, err
		}
		teamReq["model_tpm_limit"] = modelTPM
	}

	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var metadata map[string]string
		data.Metadata.ElementsAs(ctx, &metadata, false)
		if len(metadata) > 0 {
			metadataPayload := convertMetadataToNative(metadata)
			delete(metadataPayload, "model_rpm_limit")
			delete(metadataPayload, "model_tpm_limit")
			if len(metadataPayload) > 0 {
				teamReq["metadata"] = metadataPayload
			}
		}
	}

	if !data.RouterSettings.IsNull() && !data.RouterSettings.IsUnknown() {
		teamReq["router_settings"] = buildRouterSettingsPayload(ctx, data.RouterSettings)
	} else if data.RouterSettings.IsNull() {
		teamReq["router_settings"] = map[string]interface{}{}
	}

	return teamReq, nil
}

func (r *TeamResource) buildTeamUpdateRequest(ctx context.Context, data *TeamResourceModel, teamID string) (map[string]interface{}, error) {
	teamReq, err := r.buildTeamRequest(ctx, data, teamID)
	if err != nil {
		return nil, err
	}

	// LiteLLM v1.98 only defines limit enforcement types on NewTeamRequest.
	// UpdateTeamRequest ignores them, so changes are handled by replacement.
	delete(teamReq, "tpm_limit_type")
	delete(teamReq, "rpm_limit_type")

	return teamReq, nil
}

func extractTeamMemberBudgetClears(teamReq map[string]interface{}, teamID string) map[string]interface{} {
	clearReq := map[string]interface{}{"team_id": teamID}
	for _, field := range []string{
		"team_member_budget",
		"team_member_budget_duration",
		"team_member_rpm_limit",
		"team_member_tpm_limit",
	} {
		if value, exists := teamReq[field]; exists && value == nil {
			clearReq[field] = nil
			delete(teamReq, field)
		}
	}
	if len(clearReq) == 1 {
		return nil
	}
	return clearReq
}

// applyTeamNullableClears mutates teamReq to send explicit JSON null for nullable
// fields that transition from set (non-null in state) to cleared (null in plan).
// Without this, json.Marshal omits the field entirely; the LiteLLM API uses Pydantic
// exclude_unset=True and ignores omitted fields, so the prior value persists and
// Terraform sees "Provider produced inconsistent result after apply".
func applyTeamNullableClears(teamReq map[string]interface{}, state, plan *TeamResourceModel) {
	if !state.MaxBudget.IsNull() && plan.MaxBudget.IsNull() {
		teamReq["max_budget"] = nil
	}
	if !state.BudgetDuration.IsNull() && plan.BudgetDuration.IsNull() {
		teamReq["budget_duration"] = nil
	}
	if !state.TPMLimit.IsNull() && plan.TPMLimit.IsNull() {
		teamReq["tpm_limit"] = nil
	}
	if !state.RPMLimit.IsNull() && plan.RPMLimit.IsNull() {
		teamReq["rpm_limit"] = nil
	}
	if !state.TeamMemberBudget.IsNull() && plan.TeamMemberBudget.IsNull() {
		teamReq["team_member_budget"] = nil
	}
	if !state.MemberBudgetDuration.IsNull() && plan.MemberBudgetDuration.IsNull() {
		teamReq["team_member_budget_duration"] = nil
	}
	if !state.TeamMemberRPMLimit.IsNull() && plan.TeamMemberRPMLimit.IsNull() {
		teamReq["team_member_rpm_limit"] = nil
	}
	if !state.TeamMemberTPMLimit.IsNull() && plan.TeamMemberTPMLimit.IsNull() {
		teamReq["team_member_tpm_limit"] = nil
	}
}

// buildRouterSettingsPayload converts the Terraform router_settings object into
// the LiteLLM API wire format where each fallback entry is a single-key dict:
// [{"primary_model": ["fallback1", "fallback2"]}]
func buildRouterSettingsPayload(ctx context.Context, obj types.Object) map[string]interface{} {
	var rs RouterSettingsModel
	obj.As(ctx, &rs, basetypes.ObjectAsOptions{})

	payload := map[string]interface{}{}

	if !rs.Fallbacks.IsNull() && !rs.Fallbacks.IsUnknown() {
		payload["fallbacks"] = fallbackEntriesToAPIFormat(ctx, rs.Fallbacks)
	}
	if !rs.ContextWindowFallbacks.IsNull() && !rs.ContextWindowFallbacks.IsUnknown() {
		payload["context_window_fallbacks"] = fallbackEntriesToAPIFormat(ctx, rs.ContextWindowFallbacks)
	}

	return payload
}

// fallbackEntriesToAPIFormat transforms a Terraform list of FallbackEntryModel
// objects into the LiteLLM wire format: [{"model_name": ["fb1", "fb2"]}, ...]
func fallbackEntriesToAPIFormat(ctx context.Context, list types.List) []map[string][]string {
	var entries []FallbackEntryModel
	list.ElementsAs(ctx, &entries, false)

	result := make([]map[string][]string, 0, len(entries))
	for _, e := range entries {
		var fbModels []string
		e.FallbackModels.ElementsAs(ctx, &fbModels, false)
		result = append(result, map[string][]string{
			e.Model.ValueString(): fbModels,
		})
	}
	return result
}

func (r *TeamResource) readTeam(ctx context.Context, data *TeamResourceModel) error {
	return r.readTeamWithNumericOwnership(ctx, data, false)
}

func (r *TeamResource) readTeamWithNumericOwnership(ctx context.Context, data *TeamResourceModel, imported bool) error {
	endpoint := fmt.Sprintf("/team/info?team_id=%s", url.QueryEscape(data.ID.ValueString()))

	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return err
	}

	// The /team/info endpoint may return team data nested inside "team_info".
	teamInfo := result
	if nested, ok := result["team_info"].(map[string]interface{}); ok {
		teamInfo = nested
	}
	if imported {
		validated, err := requireImportedObjectField(true, "team", result, "team_info")
		if err != nil {
			return err
		}
		teamInfo = validated
	}
	if err := validateImportedObjectIdentity(imported, "team", teamInfo, "team_id", data.ID.ValueString()); err != nil {
		return err
	}
	if err := requireImportedStringField(imported, "team", teamInfo, "team_alias"); err != nil {
		return err
	}

	// Keep the configurable team_id and Terraform id tied to the same remote
	// identity. Imports and legacy state learn team_id from the resource ID.
	if observedTeamID, ok := teamInfo["team_id"].(string); ok && observedTeamID != "" && observedTeamID != data.ID.ValueString() {
		return fmt.Errorf("LiteLLM returned team_id %q while reading team %q", observedTeamID, data.ID.ValueString())
	}
	data.TeamID = data.ID

	// Update fields from response
	if teamAlias, ok := teamInfo["team_alias"].(string); ok && teamAlias != "" {
		data.TeamAlias = types.StringValue(teamAlias)
	}
	if orgID, ok := teamInfo["organization_id"].(string); ok && orgID != "" {
		data.OrganizationID = types.StringValue(orgID)
	}
	accessGroupIDs, err := stringSetFromAPI(ctx, teamInfo["access_group_ids"])
	if err != nil {
		return fmt.Errorf("invalid access_group_ids in team response: %w", err)
	}
	data.AccessGroupIDs = accessGroupIDs
	// These numeric attributes are Optional-only. Validate every present value,
	// adopt only configured values, and clear configured state on API null or
	// omission so remote removals are observable.
	tpmOwned := imported || (!data.TPMLimit.IsNull() && !data.TPMLimit.IsUnknown())
	if err := updateInt64FromAPI(&data.TPMLimit, teamInfo, tpmOwned, tpmOwned, "tpm_limit"); err != nil {
		return err
	}
	rpmOwned := imported || (!data.RPMLimit.IsNull() && !data.RPMLimit.IsUnknown())
	if err := updateInt64FromAPI(&data.RPMLimit, teamInfo, rpmOwned, rpmOwned, "rpm_limit"); err != nil {
		return err
	}
	maxBudgetOwned := imported || (!data.MaxBudget.IsNull() && !data.MaxBudget.IsUnknown())
	if err := updateFloat64FromAPI(&data.MaxBudget, teamInfo, maxBudgetOwned, maxBudgetOwned, "max_budget"); err != nil {
		return err
	}
	if v, exists := teamInfo["budget_duration"]; exists {
		if budgetDuration, ok := v.(string); ok && budgetDuration != "" && !data.BudgetDuration.IsNull() {
			data.BudgetDuration = types.StringValue(budgetDuration)
		} else if v == nil {
			data.BudgetDuration = types.StringNull()
		}
	} else if data.BudgetDuration.IsUnknown() {
		data.BudgetDuration = types.StringNull()
	}
	if blocked, ok := teamInfo["blocked"].(bool); ok {
		data.Blocked = types.BoolValue(blocked)
	}
	if tpmLimitType, ok := teamInfo["tpm_limit_type"].(string); ok && tpmLimitType != "" {
		data.TPMLimitType = types.StringValue(tpmLimitType)
	}
	if rpmLimitType, ok := teamInfo["rpm_limit_type"].(string); ok && rpmLimitType != "" {
		data.RPMLimitType = types.StringValue(rpmLimitType)
	}
	teamMemberBudgetInfo := teamInfo
	if nestedBudget, ok := teamInfo["team_member_budget_table"].(map[string]interface{}); ok {
		teamMemberBudgetInfo = nestedBudget
	}
	teamMemberBudgetOwned := imported || (!data.TeamMemberBudget.IsNull() && !data.TeamMemberBudget.IsUnknown())
	if err := updateFloat64FromAPIAliases(&data.TeamMemberBudget, teamMemberBudgetInfo, teamMemberBudgetOwned, teamMemberBudgetOwned, "team_member_budget", "max_budget"); err != nil {
		return err
	}
	if !data.MemberBudgetDuration.IsNull() || data.MemberBudgetDuration.IsUnknown() {
		if v, exists := teamMemberBudgetInfo["team_member_budget_duration"]; exists {
			if duration, ok := v.(string); ok && duration != "" {
				data.MemberBudgetDuration = types.StringValue(duration)
			} else if v == nil || v == "" {
				data.MemberBudgetDuration = types.StringNull()
			}
		} else if v, exists := teamMemberBudgetInfo["budget_duration"]; exists {
			if duration, ok := v.(string); ok && duration != "" {
				data.MemberBudgetDuration = types.StringValue(duration)
			} else if v == nil || v == "" {
				data.MemberBudgetDuration = types.StringNull()
			}
		} else if data.MemberBudgetDuration.IsUnknown() {
			data.MemberBudgetDuration = types.StringNull()
		}
	}
	teamMemberRPMOwned := imported || (!data.TeamMemberRPMLimit.IsNull() && !data.TeamMemberRPMLimit.IsUnknown())
	if err := updateInt64FromAPIAliases(&data.TeamMemberRPMLimit, teamMemberBudgetInfo, teamMemberRPMOwned, teamMemberRPMOwned, "team_member_rpm_limit", "rpm_limit"); err != nil {
		return err
	}
	teamMemberTPMOwned := imported || (!data.TeamMemberTPMLimit.IsNull() && !data.TeamMemberTPMLimit.IsUnknown())
	if err := updateInt64FromAPIAliases(&data.TeamMemberTPMLimit, teamMemberBudgetInfo, teamMemberTPMOwned, teamMemberTPMOwned, "team_member_tpm_limit", "tpm_limit"); err != nil {
		return err
	}

	// Handle models list - preserve null when API returns empty and config didn't specify models
	if models, ok := teamInfo["models"].([]interface{}); ok && len(models) > 0 {
		modelsList := make([]attr.Value, 0, len(models))
		for _, m := range models {
			if str, ok := m.(string); ok {
				modelsList = append(modelsList, types.StringValue(str))
			}
		}
		data.Models, _ = types.ListValue(types.StringType, modelsList)
	} else if !data.Models.IsNull() {
		data.Models, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle tags list - preserve null when API returns empty and config didn't specify tags
	if tags, ok := teamInfo["tags"].([]interface{}); ok && len(tags) > 0 {
		tagsList := make([]attr.Value, 0, len(tags))
		for _, t := range tags {
			if str, ok := t.(string); ok {
				tagsList = append(tagsList, types.StringValue(str))
			}
		}
		data.Tags, _ = types.ListValue(types.StringType, tagsList)
	} else if !data.Tags.IsNull() {
		data.Tags, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle guardrails list - preserve null when API returns empty and config didn't specify guardrails
	if guardrails, ok := teamInfo["guardrails"].([]interface{}); ok && len(guardrails) > 0 {
		guardrailsList := make([]attr.Value, 0, len(guardrails))
		for _, g := range guardrails {
			if str, ok := g.(string); ok {
				guardrailsList = append(guardrailsList, types.StringValue(str))
			}
		}
		data.Guardrails, _ = types.ListValue(types.StringType, guardrailsList)
	} else if !data.Guardrails.IsNull() {
		data.Guardrails, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle prompts list - preserve null when API returns empty and config didn't specify prompts
	if prompts, ok := teamInfo["prompts"].([]interface{}); ok && len(prompts) > 0 {
		promptsList := make([]attr.Value, 0, len(prompts))
		for _, p := range prompts {
			if str, ok := p.(string); ok {
				promptsList = append(promptsList, types.StringValue(str))
			}
		}
		data.Prompts, _ = types.ListValue(types.StringType, promptsList)
	} else if !data.Prompts.IsNull() {
		data.Prompts, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	// Handle metadata map - preserve null when API returns empty and config didn't specify metadata.
	// The API may inject internal keys (e.g. tpm_limit_type, rpm_limit_type) into metadata.
	// Only include keys that were in the user's original config to avoid drift.
	if metadata, ok := teamInfo["metadata"].(map[string]interface{}); ok && len(metadata) > 0 {
		configuredKeys := make(map[string]bool)
		if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
			var currentMeta map[string]string
			data.Metadata.ElementsAs(ctx, &currentMeta, false)
			for k := range currentMeta {
				configuredKeys[k] = true
			}
		}

		metaMap := make(map[string]attr.Value)
		for k, v := range metadata {
			if k == "model_rpm_limit" || k == "model_tpm_limit" {
				continue
			}
			if len(configuredKeys) > 0 && !configuredKeys[k] {
				continue
			}
			metaMap[k] = types.StringValue(metadataValueToString(v))
		}
		if len(metaMap) > 0 {
			data.Metadata, _ = types.MapValue(types.StringType, metaMap)
		} else if data.Metadata.IsUnknown() {
			data.Metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
		}
	} else if data.Metadata.IsUnknown() {
		data.Metadata, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// Handle model_aliases map
	// The API may not echo back model_aliases, so only clear on Unknown.
	if modelAliases, ok := teamInfo["model_aliases"].(map[string]interface{}); ok && len(modelAliases) > 0 {
		aliasMap := make(map[string]attr.Value)
		for k, v := range modelAliases {
			if str, ok := v.(string); ok {
				aliasMap[k] = types.StringValue(str)
			}
		}
		data.ModelAliases, _ = types.MapValue(types.StringType, aliasMap)
	} else if data.ModelAliases.IsUnknown() {
		data.ModelAliases, _ = types.MapValue(types.StringType, map[string]attr.Value{})
	}

	// LiteLLM v1.98 stores team per-model rates in team_info.metadata.
	modelRPMOwned := imported || (!data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown())
	if err := updateInt64MapFromAPI(&data.ModelRPMLimit, teamInfo, modelRPMOwned, modelRPMOwned, "metadata", "model_rpm_limit"); err != nil {
		return err
	}
	modelTPMOwned := imported || (!data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown())
	if err := updateInt64MapFromAPI(&data.ModelTPMLimit, teamInfo, modelTPMOwned, modelTPMOwned, "metadata", "model_tpm_limit"); err != nil {
		return err
	}

	// Handle router_settings - always reflect the API's actual state so Terraform
	// can detect drift and clear stale fallbacks when the block is removed from config.
	if rs, ok := teamInfo["router_settings"].(map[string]interface{}); ok && len(rs) > 0 {
		data.RouterSettings = parseRouterSettingsFromAPI(rs)
	} else {
		data.RouterSettings = types.ObjectNull(routerSettingsAttrTypes)
	}

	// Fetch permissions separately - preserve null when API returns empty and config didn't specify permissions
	permEndpoint := fmt.Sprintf("/team/permissions_list?team_id=%s", url.QueryEscape(data.ID.ValueString()))
	var permResult map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", permEndpoint, nil, &permResult); err == nil {
		permsValue, permsPresent := permResult["team_member_permissions"].([]interface{})
		if imported && !permsPresent {
			return fmt.Errorf("team import permissions response is missing the required permissions array")
		}
		if perms := permsValue; permsPresent && len(perms) > 0 {
			permissions := make([]string, len(perms))
			for i, p := range perms {
				if s, ok := p.(string); ok {
					permissions[i] = s
				}
			}
			data.TeamMemberPermissions, _ = types.ListValueFrom(ctx, types.StringType, permissions)
		} else if !data.TeamMemberPermissions.IsNull() {
			data.TeamMemberPermissions, _ = types.ListValue(types.StringType, []attr.Value{})
		}
	} else if imported {
		return fmt.Errorf("team import permissions response could not be authoritatively validated")
	} else if !data.TeamMemberPermissions.IsNull() {
		data.TeamMemberPermissions, _ = types.ListValue(types.StringType, []attr.Value{})
	}

	return nil
}

// parseRouterSettingsFromAPI converts the LiteLLM API router_settings response
// back into a Terraform types.Object matching the schema.
func parseRouterSettingsFromAPI(rs map[string]interface{}) types.Object {
	rsAttrs := map[string]attr.Value{}

	if fb, ok := rs["fallbacks"].([]interface{}); ok {
		rsAttrs["fallbacks"] = apiFormatToFallbackEntries(fb)
	} else {
		rsAttrs["fallbacks"] = types.ListNull(types.ObjectType{AttrTypes: fallbackEntryAttrTypes})
	}

	if cwf, ok := rs["context_window_fallbacks"].([]interface{}); ok {
		rsAttrs["context_window_fallbacks"] = apiFormatToFallbackEntries(cwf)
	} else {
		rsAttrs["context_window_fallbacks"] = types.ListNull(types.ObjectType{AttrTypes: fallbackEntryAttrTypes})
	}

	obj, _ := types.ObjectValue(routerSettingsAttrTypes, rsAttrs)
	return obj
}

// apiFormatToFallbackEntries transforms the LiteLLM wire format
// [{"model_name": ["fb1", "fb2"]}, ...] into a Terraform list of fallback entry objects.
func apiFormatToFallbackEntries(items []interface{}) basetypes.ListValue {
	entries := make([]attr.Value, 0, len(items))
	for _, item := range items {
		dict, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		for model, fbRaw := range dict {
			fbSlice, ok := fbRaw.([]interface{})
			if !ok {
				continue
			}
			fbModels := make([]attr.Value, 0, len(fbSlice))
			for _, m := range fbSlice {
				if s, ok := m.(string); ok {
					fbModels = append(fbModels, types.StringValue(s))
				}
			}
			fbList, _ := types.ListValue(types.StringType, fbModels)
			entryObj, _ := types.ObjectValue(fallbackEntryAttrTypes, map[string]attr.Value{
				"model":           types.StringValue(model),
				"fallback_models": fbList,
			})
			entries = append(entries, entryObj)
		}
	}
	list, _ := types.ListValue(types.ObjectType{AttrTypes: fallbackEntryAttrTypes}, entries)
	return list
}
