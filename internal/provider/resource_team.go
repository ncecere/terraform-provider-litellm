package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"

	"github.com/google/uuid"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var _ resource.Resource = &TeamResource{}
var _ resource.ResourceWithImportState = &TeamResource{}
var _ resource.ResourceWithModifyPlan = &TeamResource{}
var _ resource.ResourceWithUpgradeState = &TeamResource{}

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
	MetadataJSON          types.String  `tfsdk:"metadata_json"`
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

func teamChangedFieldMismatch(desired, prior, actual TeamResourceModel) (string, bool) {
	for _, field := range []struct {
		name                   string
		desired, prior, actual attr.Value
	}{
		{"team_alias", desired.TeamAlias, prior.TeamAlias, actual.TeamAlias},
		{"organization_id", desired.OrganizationID, prior.OrganizationID, actual.OrganizationID},
		{"access_group_ids", desired.AccessGroupIDs, prior.AccessGroupIDs, actual.AccessGroupIDs},
		{"metadata", desired.Metadata, prior.Metadata, actual.Metadata},
		{"metadata_json", desired.MetadataJSON, prior.MetadataJSON, actual.MetadataJSON},
		{"tpm_limit", desired.TPMLimit, prior.TPMLimit, actual.TPMLimit},
		{"rpm_limit", desired.RPMLimit, prior.RPMLimit, actual.RPMLimit},
		{"max_budget", desired.MaxBudget, prior.MaxBudget, actual.MaxBudget},
		{"budget_duration", desired.BudgetDuration, prior.BudgetDuration, actual.BudgetDuration},
		{"models", desired.Models, prior.Models, actual.Models},
		{"model_aliases", desired.ModelAliases, prior.ModelAliases, actual.ModelAliases},
		{"model_rpm_limit", desired.ModelRPMLimit, prior.ModelRPMLimit, actual.ModelRPMLimit},
		{"model_tpm_limit", desired.ModelTPMLimit, prior.ModelTPMLimit, actual.ModelTPMLimit},
		{"tags", desired.Tags, prior.Tags, actual.Tags},
		{"guardrails", desired.Guardrails, prior.Guardrails, actual.Guardrails},
		{"prompts", desired.Prompts, prior.Prompts, actual.Prompts},
		{"blocked", desired.Blocked, prior.Blocked, actual.Blocked},
		{"team_member_permissions", desired.TeamMemberPermissions, prior.TeamMemberPermissions, actual.TeamMemberPermissions},
		{"team_member_budget", desired.TeamMemberBudget, prior.TeamMemberBudget, actual.TeamMemberBudget},
		{"team_member_budget_duration", desired.MemberBudgetDuration, prior.MemberBudgetDuration, actual.MemberBudgetDuration},
		{"team_member_rpm_limit", desired.TeamMemberRPMLimit, prior.TeamMemberRPMLimit, actual.TeamMemberRPMLimit},
		{"team_member_tpm_limit", desired.TeamMemberTPMLimit, prior.TeamMemberTPMLimit, actual.TeamMemberTPMLimit},
		{"router_settings", desired.RouterSettings, prior.RouterSettings, actual.RouterSettings},
	} {
		if !field.desired.IsUnknown() && !field.desired.Equal(field.prior) && !field.desired.Equal(field.actual) {
			return field.name, true
		}
	}
	return "", false
}

func preserveTeamMutationRepresentations(planned TeamResourceModel, observed *TeamResourceModel) {
	for _, pair := range []struct {
		planned  types.List
		observed *types.List
	}{
		{planned.Models, &observed.Models},
		{planned.Tags, &observed.Tags},
		{planned.Guardrails, &observed.Guardrails},
		{planned.Prompts, &observed.Prompts},
		{planned.TeamMemberPermissions, &observed.TeamMemberPermissions},
	} {
		if !pair.planned.IsNull() && !pair.planned.IsUnknown() && len(pair.planned.Elements()) == 0 && pair.observed.IsNull() {
			*pair.observed = pair.planned
		}
	}
	if planned.RouterSettings.IsNull() && !observed.RouterSettings.IsNull() && !observed.RouterSettings.IsUnknown() {
		empty := true
		for _, value := range observed.RouterSettings.Attributes() {
			list, ok := value.(types.List)
			if !ok || (!list.IsNull() && (list.IsUnknown() || len(list.Elements()) != 0)) {
				empty = false
				break
			}
		}
		if empty {
			observed.RouterSettings = planned.RouterSettings
		}
	}
}

func partialTeamState(teamID string) TeamResourceModel {
	return TeamResourceModel{
		ID:                    types.StringValue(teamID),
		TeamID:                types.StringValue(teamID),
		AccessGroupIDs:        types.SetNull(types.StringType),
		Metadata:              types.MapNull(types.StringType),
		MetadataJSON:          types.StringNull(),
		Models:                types.ListNull(types.StringType),
		ModelAliases:          types.MapNull(types.StringType),
		ModelRPMLimit:         types.MapNull(types.Int64Type),
		ModelTPMLimit:         types.MapNull(types.Int64Type),
		Tags:                  types.ListNull(types.StringType),
		Guardrails:            types.ListNull(types.StringType),
		Prompts:               types.ListNull(types.StringType),
		TeamMemberPermissions: types.ListNull(types.StringType),
		RouterSettings:        types.ObjectNull(routerSettingsAttrTypes),
	}
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
		Version:     1,
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
			"metadata_json": schema.StringAttribute{
				Description: "Additional team metadata as a semantic JSON object.",
				Optional:    true,
				Computed:    true,
				Sensitive:   true,
				Validators:  []validator.String{keySemanticDictionaryValidator{}},
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

func (r *TeamResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if organizationProjectPlanIsDestroy(req) {
		return
	}
	var plan, config, state TeamResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if config.MetadataJSON.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Unknown Semantic Team Dictionary", "metadata_json must be known before a team mutation.")
		return
	}
	prepared, err := prepareTeamSemanticDictionary(ctx, config.MetadataJSON, config.Metadata)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Team Dictionary", "The JSON object is malformed, overlaps another managed team metadata surface, or cannot be persisted exactly.")
		return
	}
	if _, err := teamLegacyMetadataObject(ctx, config.Metadata); err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata"), "Invalid Team Metadata", "Legacy metadata overlaps a reserved or server-owned team metadata surface.")
		return
	}
	if req.State.Raw.IsNull() {
		return
	}
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var raw []byte
	if req.Private != nil {
		value, diagnostics := req.Private.GetKey(ctx, teamMetadataJSONProvenancePrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		raw = value
	}
	provenance, err := decodeTeamSemanticProvenance(ctx, raw, state.MetadataJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No team plan was produced.")
		return
	}
	changed, err := keySemanticDictionaryNeedsChange(ctx, config.MetadataJSON, state.MetadataJSON, provenance)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Team Dictionary", "The semantic value or private ownership could not be compared safely.")
		return
	}
	if !provenance.Configured && config.MetadataJSON.IsNull() {
		plan.MetadataJSON = types.StringNull()
	}
	if !changed && provenance.Configured && knownString(config.MetadataJSON) {
		plan.MetadataJSON = state.MetadataJSON
	}
	if changed && config.MetadataJSON.IsNull() {
		plan.MetadataJSON = types.StringUnknown()
	}
	if prepared.object != nil && config.Metadata.IsNull() {
		legacyPlan := plan.Metadata
		if legacyPlan.IsUnknown() {
			legacyPlan = state.Metadata
		}
		filtered, filterErr := excludeKeyLegacyJSONTopLevelKeys(ctx, legacyPlan, prepared.object)
		if filterErr != nil {
			resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Team Dictionary", "The legacy metadata projection could not be produced safely.")
			return
		}
		plan.Metadata = filtered
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *TeamResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data, config TeamResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if req.Config.Raw.Type() == nil {
		config = data
	} else {
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if config.MetadataJSON.IsUnknown() {
		resp.Diagnostics.AddError("Unknown Semantic Team Dictionary", "metadata_json must be known before creating a team. No request was sent.")
		return
	}
	prepared, err := prepareTeamSemanticDictionary(ctx, config.MetadataJSON, config.Metadata)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Team Dictionary", "The JSON object is malformed, overlaps another managed team metadata surface, or cannot be persisted exactly. No request was sent.")
		return
	}
	if _, err := teamLegacyMetadataObject(ctx, config.Metadata); err != nil {
		resp.Diagnostics.AddError("Invalid Team Metadata", "Legacy metadata overlaps a reserved or server-owned team metadata surface. No request was sent.")
		return
	}
	data.MetadataJSON = config.MetadataJSON
	teamID := teamIDForCreate(data.TeamID)
	data.ID = types.StringValue(teamID)
	data.TeamID = types.StringValue(teamID)
	teamReq, err := r.buildTeamRequest(ctx, &data, teamID)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Configuration", "The team request could not be converted safely. No request was sent.")
		return
	}
	if err := overlayTeamCreateSemantic(ctx, teamReq, prepared); err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Team Dictionary", "The complete metadata document could not be composed safely. No request was sent.")
		return
	}
	provenanceRaw, err := encodeSemanticDictionaryProvenance(ctx, prepared.provenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No request was sent.")
		return
	}
	retainRecovery := func(title, detail string) {
		recoveryCtx := context.WithoutCancel(ctx)
		recovery := partialTeamSemanticRecoveryState(teamID)
		unconfigured, encodeErr := encodeSemanticDictionaryProvenance(recoveryCtx, teamUnconfiguredSemanticProvenance())
		if encodeErr == nil && resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(recoveryCtx, teamMetadataJSONProvenancePrivateKey, unconfigured)...)
			resp.Diagnostics.Append(resp.Private.SetKey(recoveryCtx, teamAcceptedCreateRecoveryPrivateKey, []byte("true"))...)
		}
		resp.Diagnostics.Append(resp.State.Set(recoveryCtx, &recovery)...)
		resp.Diagnostics.AddError(title, detail)
	}

	var createResult map[string]interface{}
	accepted, createErr := r.client.doRequestWithResponse(ctx, http.MethodPost, "/team/new", teamReq, &createResult)
	if createErr != nil {
		// Team creation can commit the database row before a later endpoint phase
		// returns a non-2xx response. Once the request was dispatched, status alone
		// cannot prove rejection and the generated/caller identity must be retained.
		if accepted || ClassifyHTTPFailure(createErr).RequestDispatched {
			retainRecovery("Team Creation Outcome Uncertain", "The team create may have been accepted, but its outcome could not be validated safely. Only the generated identity was retained for authoritative recovery.")
		} else {
			resp.Diagnostics.AddError("Team Creation Failed", "LiteLLM definitively rejected the team create before mutation. No state was published.")
		}
		return
	}

	createdID, createIDErr := requiredTeamString(createResult, "team_id")
	if createIDErr != nil || createdID != teamID {
		retainRecovery("Team Creation Identity Not Confirmed", "LiteLLM accepted the team create, but its response did not confirm the generated identity. Only that identity was retained for authoritative recovery.")
		return
	}
	planned := data
	ownership := teamSemanticOwnership{provenance: prepared.provenance, fresh: true, confirmCurrentValue: prepared.provenance.Configured}
	if err := r.readTeamWithOwnership(ctx, &data, false, ownership); err != nil {
		retainRecovery("Team Creation Not Confirmed", "LiteLLM accepted the team create, but one authoritative identity-bound read did not confirm complete state. Only the generated identity was retained for recovery.")
		return
	}
	preserveTeamMutationRepresentations(planned, &data)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMetadataJSONProvenancePrivateKey, provenanceRaw)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamAcceptedCreateRecoveryPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamPendingUpdatePrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamPendingMemberDefaultsPrivateKey, nil)...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TeamResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TeamResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	var importedRaw, provenanceRaw, acceptedRaw, pendingRaw, pendingMemberRaw []byte
	if req.Private != nil {
		for _, entry := range []struct {
			key    string
			target *[]byte
		}{{numericImportedPrivateKey, &importedRaw}, {teamMetadataJSONProvenancePrivateKey, &provenanceRaw}, {teamAcceptedCreateRecoveryPrivateKey, &acceptedRaw}, {teamPendingUpdatePrivateKey, &pendingRaw}, {teamPendingMemberDefaultsPrivateKey, &pendingMemberRaw}} {
			value, diagnostics := req.Private.GetKey(ctx, entry.key)
			resp.Diagnostics.Append(diagnostics...)
			*entry.target = value
		}
	}
	if len(acceptedRaw) != 0 && string(acceptedRaw) != "true" {
		resp.Diagnostics.AddError("Invalid Team Recovery State", "Accepted-create recovery state is malformed. No team read was performed.")
	}
	provenance, err := decodeTeamSemanticProvenance(ctx, provenanceRaw, data.MetadataJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No team read was performed.")
	}
	pending, err := decodeKeySemanticPendingTransition(ctx, pendingRaw)
	if err != nil || pending.Config.Active || pending.Permissions.Active {
		resp.Diagnostics.AddError("Invalid Team Recovery State", "Pending semantic-update recovery state is malformed. No team read was performed.")
	}
	pendingMember, err := decodeTeamPendingMemberDefaults(ctx, pendingMemberRaw)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Recovery State", "Pending member-default recovery state is malformed. No team read was performed.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	reconcile := keySemanticPendingReconcile{}
	ownership := teamSemanticOwnership{provenance: provenance, pending: pending, reconcile: &reconcile, acceptedCreate: string(acceptedRaw) == "true", pendingMemberFields: pendingMember, fresh: len(acceptedRaw) != 0 || pending.any() || len(pendingMember) != 0}
	imported := string(importedRaw) == "true"
	if err := r.readTeamWithOwnership(ctx, &data, imported, ownership); err != nil {
		if IsNotFoundError(err) && !ownership.acceptedCreate {
			resp.State.RemoveResource(ctx)
			return
		}
		if IsNotFoundError(err) && ownership.acceptedCreate {
			resp.State = req.State
			resp.Private = req.Private
			resp.Diagnostics.AddError("Team Creation Not Yet Confirmed", "The accepted team identity is not yet authoritatively readable. Recovery state was retained to prevent duplicate creation.")
			return
		}
		resp.State = req.State
		resp.Private = req.Private
		resp.Diagnostics.AddError("Team Read Failed", "The authoritative team response could not be validated or projected safely. Prior state was retained.")
		return
	}
	if reconcile.Present && reconcile.Committed {
		provenance = reconcile.Effective.metadata
	}
	encoded, err := encodeSemanticDictionaryProvenance(ctx, provenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No team state was produced.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMetadataJSONProvenancePrivateKey, encoded)...)
		if imported {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
		}
		if string(acceptedRaw) == "true" {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamAcceptedCreateRecoveryPrivateKey, nil)...)
		}
		if reconcile.Present {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamPendingUpdatePrivateKey, nil)...)
		}
		if len(pendingMember) != 0 {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamPendingMemberDefaultsPrivateKey, nil)...)
		}
	}
}

func (r *TeamResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data, state, config TeamResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if req.Config.Raw.Type() == nil {
		config = data
	} else {
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	}
	resp.State = req.State
	resp.Private = req.Private
	var acceptedRaw, pendingRaw, pendingMemberRaw, provenanceRaw []byte
	if req.Private != nil {
		for _, entry := range []struct {
			key    string
			target *[]byte
		}{{teamAcceptedCreateRecoveryPrivateKey, &acceptedRaw}, {teamPendingUpdatePrivateKey, &pendingRaw}, {teamPendingMemberDefaultsPrivateKey, &pendingMemberRaw}, {teamMetadataJSONProvenancePrivateKey, &provenanceRaw}} {
			value, diagnostics := req.Private.GetKey(ctx, entry.key)
			resp.Diagnostics.Append(diagnostics...)
			*entry.target = value
		}
	}
	if len(pendingRaw) != 0 || len(pendingMemberRaw) != 0 || string(acceptedRaw) == "true" {
		resp.Diagnostics.AddError("Team Recovery Required", "A prior team mutation has not been reconciled. Refresh must reconcile it before another update can be sent.")
		return
	}
	if len(acceptedRaw) != 0 {
		resp.Diagnostics.AddError("Invalid Team Recovery State", "Accepted-create recovery state is malformed. No team update was sent.")
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if config.MetadataJSON.IsUnknown() {
		resp.Diagnostics.AddError("Unknown Semantic Team Dictionary", "metadata_json must be known before updating a team. No request was sent.")
		return
	}
	priorProvenance, err := decodeTeamSemanticProvenance(ctx, provenanceRaw, state.MetadataJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No team update was sent.")
		return
	}
	prepared, err := prepareTeamSemanticDictionary(ctx, config.MetadataJSON, config.Metadata)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Team Dictionary", "The JSON object is malformed, overlaps another managed team metadata surface, or cannot be persisted exactly. No request was sent.")
		return
	}
	if _, err := teamLegacyMetadataObject(ctx, config.Metadata); err != nil {
		resp.Diagnostics.AddError("Invalid Team Metadata", "Legacy metadata overlaps a reserved or server-owned team metadata surface. No request was sent.")
		return
	}
	semanticChanged, err := keySemanticDictionaryNeedsChange(ctx, config.MetadataJSON, state.MetadataJSON, priorProvenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Team Dictionary", "The semantic value or private ownership could not be compared safely. No request was sent.")
		return
	}
	confirmationOwnership, err := prepared.updateOwnership(ctx, priorProvenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic shape-transition ownership could not be validated safely. No request was sent.")
		return
	}
	pendingTransition := pendingTeamSemanticTransition(confirmationOwnership)
	var pendingPrivate []byte
	if pendingTransition.any() {
		pendingPrivate, err = encodeKeySemanticPendingTransition(ctx, pendingTransition)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Pending semantic shape ownership could not be encoded safely. No request was sent.")
			return
		}
	}
	newProvenanceRaw, err := encodeSemanticDictionaryProvenance(ctx, prepared.provenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No request was sent.")
		return
	}
	data.ID = state.ID
	data.TeamID = state.TeamID
	data.MetadataJSON = config.MetadataJSON
	if data.TeamID.IsNull() || data.TeamID.IsUnknown() {
		data.TeamID = state.ID
	}
	permissionsChanged := !data.TeamMemberPermissions.Equal(state.TeamMemberPermissions)
	var permissions []string
	if permissionsChanged {
		var permissionDiagnostics diag.Diagnostics
		permissions, _, permissionDiagnostics = strictTerraformStringList(ctx, data.TeamMemberPermissions, path.Root("team_member_permissions"))
		resp.Diagnostics.Append(permissionDiagnostics...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	teamReq, err := r.buildTeamUpdateRequest(ctx, &data, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Configuration", "The team update request could not be converted safely. No request was sent.")
		return
	}
	applyTeamNullableClears(teamReq, &state, &data)
	metadataChanged := semanticChanged ||
		(!data.Metadata.IsUnknown() && !data.Metadata.Equal(state.Metadata)) ||
		(!data.Tags.IsUnknown() && !data.Tags.Equal(state.Tags)) ||
		(!data.Guardrails.IsUnknown() && !data.Guardrails.Equal(state.Guardrails)) ||
		(!data.Prompts.IsUnknown() && !data.Prompts.Equal(state.Prompts)) ||
		(!data.ModelRPMLimit.IsUnknown() && !data.ModelRPMLimit.Equal(state.ModelRPMLimit)) ||
		(!data.ModelTPMLimit.IsUnknown() && !data.ModelTPMLimit.Equal(state.ModelTPMLimit))
	delete(teamReq, "metadata")
	memberBudgetReinsert := false
	if metadataChanged {
		fresh, freshErr := r.getFreshExactTeamInfo(ctx, state.ID.ValueString())
		if freshErr != nil {
			resp.Diagnostics.AddError("Team Metadata Hydration Failed", "The complete identity-bound metadata document could not be read safely. No update request was sent.")
			return
		}
		teamInfo, _, unwrapErr := unwrapTeamInfoResponse(fresh)
		if unwrapErr != nil {
			resp.Diagnostics.AddError("Team Metadata Hydration Failed", "The complete metadata document could not be read safely. No update request was sent.")
			return
		}
		remote, _, metadataErr := teamMetadataObject(ctx, teamInfo)
		if metadataErr != nil {
			resp.Diagnostics.AddError("Team Metadata Hydration Failed", "The complete metadata document was malformed or not persistable exactly. No update request was sent.")
			return
		}
		replacement, reinsert, compositionErr := composeTeamMetadataReplacement(ctx, remote, data, state, priorProvenance, prepared, teamReq)
		if compositionErr != nil {
			resp.Diagnostics.AddError("Team Metadata Composition Failed", "The complete metadata replacement could not be composed safely. No update request was sent.")
			return
		}
		teamReq["metadata"] = replacement
		memberBudgetReinsert = reinsert
	}

	pendingMember := changedTeamPendingMemberDefaults(data, state, teamReq)
	clearReq := extractTeamMemberBudgetClears(teamReq, data.ID.ValueString())
	pendingMemberPrivate, err := encodeTeamPendingMemberDefaults(ctx, pendingMember)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Team Recovery State", "Member-default recovery state could not be encoded safely. No request was sent.")
		return
	}
	retainPrior := func(localCtx context.Context, includeSemantic, includeMember bool) {
		if resp.Private != nil {
			if includeSemantic && len(pendingPrivate) != 0 {
				resp.Diagnostics.Append(resp.Private.SetKey(localCtx, teamPendingUpdatePrivateKey, pendingPrivate)...)
			}
			if includeMember && len(pendingMemberPrivate) != 0 {
				resp.Diagnostics.Append(resp.Private.SetKey(localCtx, teamPendingMemberDefaultsPrivateKey, pendingMemberPrivate)...)
			}
		}
		resp.Diagnostics.Append(resp.State.Set(localCtx, &state)...)
	}
	memberDefaultsMayHaveCommitted := false
	if clearReq != nil {
		if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/team/update", clearReq, nil); err != nil {
			if ClassifyHTTPFailure(err).RequestDispatched {
				retainPrior(context.WithoutCancel(ctx), false, true)
			}
			resp.Diagnostics.AddError("Team Member Defaults Update Failed", "The member-default clear was not confirmed. Prior state was retained; dispatched requests require authoritative recovery.")
			return
		}
		memberDefaultsMayHaveCommitted = true
	}
	if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/team/update", teamReq, nil); err != nil {
		dispatched := ClassifyHTTPFailure(err).RequestDispatched
		memberRecovery := len(pendingMember) != 0 && (memberDefaultsMayHaveCommitted || dispatched)
		if metadataChanged || clearReq != nil || memberRecovery {
			retainPrior(context.WithoutCancel(ctx), dispatched, memberRecovery)
			resp.Diagnostics.AddError("Team Update Not Confirmed", "The team update may have partially committed, but its complete outcome was not confirmed. Prior public and private state were retained.")
		} else {
			resp.Diagnostics.AddError("Team Update Failed", "The team update failed. Response, identity, URL, and transport details were omitted.")
		}
		return
	}
	if len(pendingMember) != 0 {
		memberDefaultsMayHaveCommitted = true
	}
	if permissionsChanged {
		permReq := map[string]interface{}{"team_id": data.ID.ValueString(), "team_member_permissions": permissions}
		if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/team/permissions_update", permReq, nil); err != nil {
			resp.Diagnostics.AddWarning("Permissions Update Error", fmt.Sprintf("Failed to update permissions: %s", err))
		}
	}
	planned := data
	readOwnership := teamSemanticOwnership{provenance: priorProvenance, fresh: true}
	if metadataChanged {
		readOwnership = confirmationOwnership
		readOwnership.expectMemberBudgetID = memberBudgetReinsert
	}
	if err := r.readTeamWithOwnership(ctx, &data, false, readOwnership); err != nil {
		if metadataChanged || clearReq != nil || memberDefaultsMayHaveCommitted {
			retainPrior(context.WithoutCancel(ctx), true, memberDefaultsMayHaveCommitted)
		}
		resp.Diagnostics.AddError("Team Update Readback Failed", "LiteLLM accepted the update, but authoritative identity-bound readback did not confirm convergence. Prior state was retained.")
		return
	}
	preserveTeamMutationRepresentations(planned, &data)
	if _, mismatch := teamChangedFieldMismatch(planned, state, data); mismatch {
		if metadataChanged || clearReq != nil || memberDefaultsMayHaveCommitted {
			retainPrior(context.WithoutCancel(ctx), true, memberDefaultsMayHaveCommitted)
		}
		resp.Diagnostics.AddError("Team Update Did Not Converge", "LiteLLM accepted the update, but authoritative readback did not match the plan. Prior state was retained.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMetadataJSONProvenancePrivateKey, newProvenanceRaw)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamPendingUpdatePrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamPendingMemberDefaultsPrivateKey, nil)...)
	}
}

func (r *TeamResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TeamResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if req.Private != nil {
		for _, key := range []string{teamAcceptedCreateRecoveryPrivateKey, teamPendingUpdatePrivateKey, teamPendingMemberDefaultsPrivateKey} {
			raw, diagnostics := req.Private.GetKey(ctx, key)
			resp.Diagnostics.Append(diagnostics...)
			if len(raw) != 0 {
				resp.Diagnostics.AddError("Team Recovery Required", "A prior team mutation has not been reconciled. Refresh must reconcile it before deletion can be sent.")
				return
			}
		}
	}
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
	provenance, err := encodeSemanticDictionaryProvenance(ctx, teamUnconfiguredSemanticProvenance())
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Import ownership could not be initialized safely.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata_json"), types.StringNull())...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamMetadataJSONProvenancePrivateKey, provenance)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamAcceptedCreateRecoveryPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamPendingUpdatePrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, teamPendingMemberDefaultsPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
	}
}

func (r *TeamResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{0: {PriorSchema: nil, StateUpgrader: func(_ context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
		if req.RawState == nil {
			resp.Diagnostics.AddError("Unable to Upgrade State", "Prior team state is unavailable.")
			return
		}
		upgraded, err := marshalTeamUpgrade(req.RawState.JSON)
		if err != nil {
			resp.Diagnostics.AddError("Unable to Upgrade State", "Prior team state could not be decoded safely.")
			return
		}
		resp.DynamicValue = &tfprotov6.DynamicValue{JSON: upgraded}
	}}}
}

func (r *TeamResource) buildTeamRequest(ctx context.Context, data *TeamResourceModel, teamID string) (map[string]interface{}, error) {
	teamReq := map[string]interface{}{
		"team_id":    teamID,
		"team_alias": data.TeamAlias.ValueString(),
	}
	convertList := func(value types.List, name string) ([]string, error) {
		converted, _, diagnostics := strictTerraformStringList(ctx, value, path.Root(name))
		if diagnostics.HasError() {
			return nil, fmt.Errorf("invalid team string-list collection")
		}
		return converted, nil
	}
	convertSet := func(value types.Set, name string) ([]string, error) {
		converted, _, diagnostics := strictTerraformStringSet(ctx, value, path.Root(name))
		if diagnostics.HasError() {
			return nil, fmt.Errorf("invalid team string-set collection")
		}
		return converted, nil
	}
	convertMap := func(value types.Map, name string) (map[string]string, error) {
		converted, _, diagnostics := strictTerraformStringMap(ctx, value, path.Root(name), true)
		if diagnostics.HasError() {
			return nil, fmt.Errorf("invalid team string-map collection")
		}
		return converted, nil
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
		accessGroupIDs, err := convertSet(data.AccessGroupIDs, "access_group_ids")
		if err != nil {
			return nil, err
		}
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

	// List fields - check IsNull, IsUnknown, and len > 0.
	for _, field := range []struct {
		name  string
		value types.List
	}{
		{name: "models", value: data.Models},
		{name: "tags", value: data.Tags},
		{name: "guardrails", value: data.Guardrails},
		{name: "prompts", value: data.Prompts},
	} {
		if field.value.IsNull() || field.value.IsUnknown() {
			continue
		}
		converted, err := convertList(field.value, field.name)
		if err != nil {
			return nil, err
		}
		if len(converted) > 0 {
			teamReq[field.name] = converted
		}
	}
	if !data.TeamMemberPermissions.IsNull() && !data.TeamMemberPermissions.IsUnknown() {
		permissions, err := convertList(data.TeamMemberPermissions, "team_member_permissions")
		if err != nil {
			return nil, err
		}
		teamReq["team_member_permissions"] = permissions
	}

	// Map fields - check IsNull, IsUnknown, and len > 0.
	if !data.ModelAliases.IsNull() && !data.ModelAliases.IsUnknown() {
		modelAliases, err := convertMap(data.ModelAliases, "model_aliases")
		if err != nil {
			return nil, err
		}
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
		metadata, err := convertMap(data.Metadata, "metadata")
		if err != nil {
			return nil, err
		}
		if len(metadata) > 0 {
			metadataPayload := convertMetadataToNative(metadata)
			if _, forbidden := metadataPayload["team_member_budget_id"]; forbidden {
				return nil, fmt.Errorf("legacy team metadata contains a server-owned field")
			}
			delete(metadataPayload, "model_rpm_limit")
			delete(metadataPayload, "model_tpm_limit")
			if len(metadataPayload) > 0 {
				teamReq["metadata"] = metadataPayload
			}
		}
	}

	if !data.RouterSettings.IsNull() && !data.RouterSettings.IsUnknown() {
		routerSettings, err := buildRouterSettingsPayload(ctx, data.RouterSettings)
		if err != nil {
			return nil, err
		}
		teamReq["router_settings"] = routerSettings
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
func buildRouterSettingsPayload(ctx context.Context, obj types.Object) (map[string]interface{}, error) {
	if diagnostics := canceledCollectionDiagnostics(ctx, path.Root("router_settings")); diagnostics.HasError() {
		return nil, fmt.Errorf("invalid team router-settings object")
	}
	var rs RouterSettingsModel
	if diagnostics := obj.As(ctx, &rs, basetypes.ObjectAsOptions{}); diagnostics.HasError() {
		return nil, fmt.Errorf("invalid team router-settings object")
	}

	payload := map[string]interface{}{}
	for _, field := range []struct {
		name  string
		value types.List
	}{
		{name: "fallbacks", value: rs.Fallbacks},
		{name: "context_window_fallbacks", value: rs.ContextWindowFallbacks},
	} {
		if field.value.IsNull() || field.value.IsUnknown() {
			continue
		}
		converted, err := fallbackEntriesToAPIFormat(ctx, field.value, path.Root("router_settings").AtName(field.name))
		if err != nil {
			return nil, err
		}
		payload[field.name] = converted
	}
	if diagnostics := canceledCollectionDiagnostics(ctx, path.Root("router_settings")); diagnostics.HasError() {
		return nil, fmt.Errorf("invalid team router-settings object")
	}
	return payload, nil
}

// fallbackEntriesToAPIFormat transforms a Terraform list of FallbackEntryModel
// objects into the LiteLLM wire format: [{"model_name": ["fb1", "fb2"]}, ...].
// Every entry is validated before any result is returned.
func fallbackEntriesToAPIFormat(ctx context.Context, list types.List, valuePath path.Path) ([]map[string][]string, error) {
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return nil, fmt.Errorf("invalid team fallback collection")
	}
	var entries []FallbackEntryModel
	if diagnostics := list.ElementsAs(ctx, &entries, false); diagnostics.HasError() {
		return nil, fmt.Errorf("invalid team fallback collection")
	}

	result := make([]map[string][]string, len(entries))
	for index, entry := range entries {
		if entry.Model.IsNull() || entry.Model.IsUnknown() {
			return nil, fmt.Errorf("invalid team fallback collection")
		}
		fallbacks, _, diagnostics := strictTerraformStringList(ctx, entry.FallbackModels, valuePath.AtListIndex(index).AtName("fallback_models"))
		if diagnostics.HasError() {
			return nil, fmt.Errorf("invalid team fallback collection")
		}
		result[index] = map[string][]string{entry.Model.ValueString(): fallbacks}
	}
	if diagnostics := canceledCollectionDiagnostics(ctx, valuePath); diagnostics.HasError() {
		return nil, fmt.Errorf("invalid team fallback collection")
	}
	return result, nil
}

func (r *TeamResource) readTeam(ctx context.Context, data *TeamResourceModel) error {
	return r.readTeamWithOwnership(ctx, data, false, teamSemanticOwnership{provenance: teamUnconfiguredSemanticProvenance()})
}

func (r *TeamResource) getTeamInfo(ctx context.Context, teamID string) (map[string]interface{}, error) {
	query := url.Values{"team_id": []string{teamID}}
	endpoint := endpointWithQuery("/team/info", query)
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (r *TeamResource) getFreshExactTeamInfo(ctx context.Context, teamID string) (map[string]interface{}, error) {
	if teamID == "" {
		return nil, errSemanticDictionaryTraversal
	}
	query := url.Values{"team_id": []string{teamID}}
	endpoint := endpointWithQuery("/team/info", query)
	var result map[string]interface{}
	if err := r.client.doFreshRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
		return nil, err
	}
	teamInfo, wrapped, err := unwrapTeamInfoResponse(result)
	if err != nil || !wrapped {
		return nil, errSemanticDictionaryTraversal
	}
	rootID, rootErr := requiredTeamString(result, "team_id")
	nestedID, nestedErr := requiredTeamString(teamInfo, "team_id")
	if rootErr != nil || nestedErr != nil || rootID != teamID || nestedID != teamID || rootID != nestedID {
		return nil, errSemanticDictionaryTraversal
	}
	metadata, presence, metadataErr := optionalObjectAt(teamInfo, "metadata")
	if metadataErr != nil || presence == apiValueAbsent {
		return nil, errSemanticDictionaryTraversal
	}
	if presence == apiValuePresent && (metadata == nil || validateSemanticDictionaryValue(ctx, metadata) != nil || validateModelSemanticDictionaryNumbers(ctx, metadata) != nil) {
		return nil, errSemanticDictionaryTraversal
	}
	prior := partialTeamState(teamID)
	if _, err := projectTeamInfoResponseWithSemantic(ctx, prior, result, false, teamSemanticOwnership{provenance: teamUnconfiguredSemanticProvenance(), fresh: true}); err != nil {
		return nil, errSemanticDictionaryTraversal
	}
	return result, nil
}

func (r *TeamResource) hydrateTeamUpdateMetadata(ctx context.Context, state TeamResourceModel, metadataChanged bool, request map[string]interface{}) error {
	result, err := r.getFreshExactTeamInfo(ctx, state.ID.ValueString())
	if err != nil {
		return err
	}
	if _, err := projectTeamInfoResponse(ctx, state, result, false); err != nil {
		return err
	}
	teamInfo, _, err := unwrapTeamInfoResponse(result)
	if err != nil {
		return err
	}
	remote, presence, err := optionalObjectAt(teamInfo, "metadata")
	if err != nil {
		return err
	}
	if !metadataChanged {
		// Omitting metadata is the only way v1.98 can preserve its server-owned
		// team_member_budget_id during an unrelated update: the endpoint strips
		// that key from every caller-supplied metadata document.
		delete(request, "metadata")
		return nil
	}
	base := map[string]interface{}{}
	if presence == apiValuePresent {
		for key, value := range remote {
			base[key] = value
		}
	}
	if _, serverOwned := base["team_member_budget_id"]; serverOwned {
		willRestore := false
		for _, field := range []string{"team_member_budget", "team_member_budget_duration", "team_member_rpm_limit", "team_member_tpm_limit"} {
			if value, present := request[field]; present && value != nil {
				willRestore = true
				break
			}
		}
		if !willRestore {
			return fmt.Errorf("authoritative team metadata contains a server-owned relation that v1.98 cannot preserve during metadata replacement")
		}
		// The endpoint strips caller-supplied IDs, then its member-budget upsert
		// restores the authoritative relation because a non-null default is sent.
		delete(base, "team_member_budget_id")
	}
	if !state.Metadata.IsNull() && !state.Metadata.IsUnknown() {
		for key := range state.Metadata.Elements() {
			delete(base, key)
		}
	}
	if configured, ok := request["metadata"].(map[string]interface{}); ok {
		for key, value := range configured {
			base[key] = value
		}
	}
	if containsMaskedTeamMetadata(base) {
		return fmt.Errorf("authoritative team metadata contains an unrecoverable masked value")
	}
	if len(base) > 0 || presence == apiValuePresent {
		request["metadata"] = base
	} else {
		delete(request, "metadata")
	}
	return nil
}

func (r *TeamResource) readTeamWithNumericOwnership(ctx context.Context, data *TeamResourceModel, imported bool) error {
	return r.readTeamWithOwnership(ctx, data, imported, teamSemanticOwnership{provenance: teamUnconfiguredSemanticProvenance()})
}

func (r *TeamResource) readTeamWithOwnership(ctx context.Context, data *TeamResourceModel, imported bool, ownership teamSemanticOwnership) error {
	var result map[string]interface{}
	var err error
	if ownership.fresh {
		result, err = r.getFreshExactTeamInfo(ctx, data.ID.ValueString())
	} else {
		result, err = r.getTeamInfo(ctx, data.ID.ValueString())
	}
	if err != nil {
		return err
	}
	projected, err := projectTeamInfoResponseWithSemantic(ctx, *data, result, imported, ownership)
	if err != nil {
		return err
	}

	permissionQuery := url.Values{"team_id": []string{data.ID.ValueString()}}
	permissionEndpoint := endpointWithQuery("/team/permissions_list", permissionQuery)
	var permissionResult map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, "GET", permissionEndpoint, nil, &permissionResult); err != nil {
		return fmt.Errorf("team permissions response could not be authoritatively validated: %w", err)
	}
	permissions, err := projectTeamPermissions(ctx, data.TeamMemberPermissions, permissionResult, data.ID.ValueString())
	if err != nil {
		return err
	}
	projected.TeamMemberPermissions = permissions

	*data = projected
	return nil
}
