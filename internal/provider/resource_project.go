package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/google/uuid"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var _ resource.Resource = &ProjectResource{}
var _ resource.ResourceWithImportState = &ProjectResource{}
var _ resource.ResourceWithModifyPlan = &ProjectResource{}
var _ resource.ResourceWithUpgradeState = &ProjectResource{}

const (
	// projectImportedOptionalStringsPrivateKey is the pre-per-field marker read
	// only to migrate retained private state from provider builds before #188's
	// final ownership fix.
	projectImportedOptionalStringsPrivateKey     = "project_imported_optional_strings_v1"
	projectImportedAliasPrivateKey               = "project_imported_project_alias_v1"
	projectImportedDescriptionPrivateKey         = "project_imported_description_v1"
	projectAliasOwnershipPendingPrivateKey       = "project_alias_ownership_pending_v1"
	projectDescriptionOwnershipPendingPrivateKey = "project_description_ownership_pending_v1"
)

func NewProjectResource() resource.Resource { return &ProjectResource{} }

type ProjectResource struct{ client *Client }

type ProjectResourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	ProjectAlias        types.String  `tfsdk:"project_alias"`
	Description         types.String  `tfsdk:"description"`
	TeamID              types.String  `tfsdk:"team_id"`
	Models              types.List    `tfsdk:"models"`
	Metadata            types.Map     `tfsdk:"metadata"`
	MetadataJSON        types.String  `tfsdk:"metadata_json"`
	Tags                types.List    `tfsdk:"tags"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	ModelMaxBudget      types.Map     `tfsdk:"model_max_budget"`
	ModelRPMLimit       types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit       types.Map     `tfsdk:"model_tpm_limit"`
	Blocked             types.Bool    `tfsdk:"blocked"`
	CreatedAt           types.String  `tfsdk:"created_at"`
	UpdatedAt           types.String  `tfsdk:"updated_at"`
	CreatedBy           types.String  `tfsdk:"created_by"`
	UpdatedBy           types.String  `tfsdk:"updated_by"`
}

func (r *ProjectResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *ProjectResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM Project. Project budget controls are read authoritatively from the nested litellm_budget_table relation.",
		Version:     1,
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Description: "The unique project ID (assigned by LiteLLM).", Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"project_alias":         schema.StringAttribute{Description: "Human-friendly name for the project.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"description":           schema.StringAttribute{Description: "Description of the project's purpose and use case.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"team_id":               schema.StringAttribute{Description: "The team ID that this project belongs to.", Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"models":                schema.ListAttribute{Description: "List of models the project can access.", Optional: true, Computed: true, ElementType: types.StringType},
			"metadata":              schema.MapAttribute{Description: "Metadata for the project. Values are strings; use jsonencode() for complex values.", Optional: true, Computed: true, ElementType: types.StringType},
			"metadata_json":         schema.StringAttribute{Description: "Additional project metadata as a semantic JSON object.", Optional: true, Computed: true, Sensitive: true, Validators: []validator.String{keySemanticDictionaryValidator{}}},
			"tags":                  schema.ListAttribute{Description: "Tags associated with the project. LiteLLM v1.98 stores these in project metadata.", Optional: true, Computed: true, ElementType: types.StringType},
			"max_budget":            schema.Float64Attribute{Description: "Maximum budget for this project.", Optional: true},
			"soft_budget":           schema.Float64Attribute{Description: "Soft budget limit for warnings.", Optional: true},
			"budget_duration":       schema.StringAttribute{Description: "Budget reset duration (for example, '30d' or '1h').", Optional: true},
			"budget_id":             schema.StringAttribute{Description: "Budget ID associated with this project. Reassociation is not safely supported by LiteLLM v1.98.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"tpm_limit":             schema.Int64Attribute{Description: "Tokens per minute limit.", Optional: true},
			"rpm_limit":             schema.Int64Attribute{Description: "Requests per minute limit.", Optional: true},
			"max_parallel_requests": schema.Int64Attribute{Description: "Maximum parallel requests allowed.", Optional: true},
			"model_max_budget":      schema.MapAttribute{Description: "Legacy per-model budget map shape retained for schema compatibility.", Optional: true, Computed: true, ElementType: types.Float64Type, Validators: []validator.Map{mapvalidator.NoNullValues()}},
			"model_rpm_limit":       schema.MapAttribute{Description: "Per-model RPM limits stored in project metadata.", Optional: true, Computed: true, ElementType: types.Int64Type, Validators: []validator.Map{mapvalidator.NoNullValues()}},
			"model_tpm_limit":       schema.MapAttribute{Description: "Per-model TPM limits stored in project metadata.", Optional: true, Computed: true, ElementType: types.Int64Type, Validators: []validator.Map{mapvalidator.NoNullValues()}},
			"blocked":               schema.BoolAttribute{Description: "Whether the project is blocked from making requests.", Optional: true, Computed: true},
			"created_at":            schema.StringAttribute{Description: "Timestamp when the project was created.", Computed: true},
			"updated_at":            schema.StringAttribute{Description: "Timestamp when the project was last updated.", Computed: true},
			"created_by":            schema.StringAttribute{Description: "User who created the project.", Computed: true},
			"updated_by":            schema.StringAttribute{Description: "User who last updated the project.", Computed: true},
		},
	}
}

func (r *ProjectResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *ProjectResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if organizationProjectPlanIsDestroy(req) {
		return
	}
	var state, plan, config ProjectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if req.State.Raw.IsNull() {
		if config.MetadataJSON.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Unknown Semantic Project Dictionary", "metadata_json must be known before project creation.")
		} else if _, err := prepareProjectSemanticDictionary(ctx, config.MetadataJSON, config.Metadata); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Project Dictionary", "The JSON object is malformed, overlaps another managed project metadata surface, or cannot be persisted exactly.")
		}
		if !config.BudgetID.IsNull() && projectBudgetControlsPresentInConfig(&config) {
			resp.Diagnostics.AddAttributeError(path.Root("budget_id"), "Unsafe Shared Project Budget Controls", "budget_id cannot be combined with project budget controls during creation because LiteLLM v1.98 ignores or strips those controls for an existing shared budget.")
		}
		if config.ModelMaxBudget.IsUnknown() || (knownMap(config.ModelMaxBudget) && len(config.ModelMaxBudget.Elements()) > 0) {
			resp.Diagnostics.AddAttributeError(path.Root("model_max_budget"), "Unsupported Structured Project Model Budget", "LiteLLM v1.98 requires GenericBudgetConfig objects for model_max_budget, but this resource's legacy schema is map(float64). Non-empty or unknown configuration is rejected until a migration-safe structured representation is available.")
		}
		return
	}
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	importedBudget, importedAlias, importedDescription, legacyImportedStrings := false, false, false, false
	if req.Private != nil {
		budgetMarker, diagnostics := req.Private.GetKey(ctx, organizationProjectImportedBudgetPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		importedBudget = string(budgetMarker) == "true"
		aliasMarker, diagnostics := req.Private.GetKey(ctx, projectImportedAliasPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		descriptionMarker, diagnostics := req.Private.GetKey(ctx, projectImportedDescriptionPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		legacyMarker, diagnostics := req.Private.GetKey(ctx, projectImportedOptionalStringsPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		legacyImportedStrings = string(legacyMarker) == "true"
		importedAlias = string(aliasMarker) == "true" || legacyImportedStrings
		importedDescription = string(descriptionMarker) == "true" || legacyImportedStrings
	}
	preserveOrganizationProjectBudgetID(ctx, "Project", state.BudgetID, config.BudgetID, plan.BudgetID, importedBudget, resp)

	var provenanceRaw []byte
	if req.Private != nil {
		raw, diagnostics := req.Private.GetKey(ctx, projectMetadataJSONProvenancePrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		provenanceRaw = raw
	}
	provenance, err := decodeProjectSemanticProvenance(ctx, provenanceRaw, state.MetadataJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No project plan was produced.")
		return
	}
	if config.MetadataJSON.IsUnknown() {
		plan.MetadataJSON = types.StringUnknown()
	} else {
		prepared, err := prepareProjectSemanticDictionary(ctx, config.MetadataJSON, config.Metadata)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Project Dictionary", "The JSON object is malformed, overlaps another managed project metadata surface, or cannot be persisted exactly. No project plan was produced.")
			return
		}
		changed, err := projectSemanticNeedsChange(ctx, config.MetadataJSON, state.MetadataJSON, provenance)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Project Dictionary", "The semantic value or private ownership could not be compared safely. No project plan was produced.")
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
			filtered, filterErr := excludeKeyLegacyJSONTopLevelKeys(ctx, plan.Metadata, prepared.object)
			if filterErr != nil {
				resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Project Dictionary", "The legacy metadata projection could not be produced safely. No project plan was produced.")
				return
			}
			plan.Metadata = filtered
		}
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for _, field := range []struct {
		name                string
		state, plan, config types.String
		imported            bool
	}{
		{"project_alias", state.ProjectAlias, plan.ProjectAlias, config.ProjectAlias, importedAlias},
		{"description", state.Description, plan.Description, config.Description, importedDescription},
	} {
		switch {
		case field.config.IsNull() && field.imported:
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(field.name), field.state)...)
		case knownString(field.state) && field.config.IsNull():
			resp.Diagnostics.AddAttributeError(path.Root(field.name), "Unsupported Project String Clear", fmt.Sprintf("LiteLLM v1.98's /project/update excludes null %s values, so removing this configured value cannot converge. Keep it configured or set an explicit non-null replacement.", field.name))
		case knownString(field.state) && (field.config.IsUnknown() || field.plan.IsUnknown()):
			resp.Diagnostics.AddAttributeError(path.Root(field.name), "Unknown Project String Transition", fmt.Sprintf("%s must be known while planning because an unknown value could resolve to an unsupported null clear on LiteLLM v1.98.", field.name))
		}
	}
	if config.ModelMaxBudget.IsNull() && knownMap(state.ModelMaxBudget) {
		// A known legacy scalar map can still be removed safely through an
		// explicit null budget update. Override the framework's computed unknown.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("model_max_budget"), types.MapNull(types.Float64Type))...)
	} else if config.ModelMaxBudget.IsUnknown() || (knownMap(config.ModelMaxBudget) && len(config.ModelMaxBudget.Elements()) > 0 && !config.ModelMaxBudget.Equal(state.ModelMaxBudget)) {
		resp.Diagnostics.AddAttributeError(path.Root("model_max_budget"), "Unsupported Structured Project Model Budget", "LiteLLM v1.98 requires GenericBudgetConfig objects for model_max_budget, but this resource's legacy schema is map(float64). Non-empty additions, changes, or unknown transitions are rejected until a migration-safe structured representation is available.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	// Split the historical shared alias/description marker only in a valid
	// plan. Retain both individual permissions until Update proves any pending
	// explicit configuration succeeded; this is safe even if apply persists
	// partial private state after an error.
	if legacyImportedStrings && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectImportedOptionalStringsPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectImportedAliasPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectImportedDescriptionPrivateKey, []byte("true"))...)
	}
	budgetTransition := planImportedOmissionOwnership(ctx, organizationProjectBudgetOwnershipPendingPrivateKey, importedBudget, config.BudgetID, resp)
	aliasTransition := planImportedOmissionOwnership(ctx, projectAliasOwnershipPendingPrivateKey, importedAlias, config.ProjectAlias, resp)
	descriptionTransition := planImportedOmissionOwnership(ctx, projectDescriptionOwnershipPendingPrivateKey, importedDescription, config.Description, resp)
	forceImportedOwnershipUpdate(ctx, "created_at",
		(budgetTransition && state.BudgetID.Equal(config.BudgetID)) ||
			(aliasTransition && state.ProjectAlias.Equal(config.ProjectAlias)) ||
			(descriptionTransition && state.Description.Equal(config.Description)), resp)
}

func (r *ProjectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data, config ProjectResourceModel
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
		resp.Diagnostics.AddError("Unknown Semantic Project Dictionary", "metadata_json must be known before creating a project. No request was sent.")
		return
	}
	prepared, err := prepareProjectSemanticDictionary(ctx, config.MetadataJSON, config.Metadata)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Project Dictionary", "The JSON object is malformed, overlaps another managed project metadata surface, or cannot be persisted exactly. No request was sent.")
		return
	}
	data.MetadataJSON = config.MetadataJSON
	projectID := uuid.NewString()
	data.ID = types.StringValue(projectID)
	projectRequest, err := r.buildProjectCreateRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Project Request", "The project request could not be converted safely. No request was sent.")
		return
	}
	projectRequest["project_id"] = projectID
	if err := overlayProjectCreateSemantic(ctx, projectRequest, prepared); err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Project Dictionary", "The complete metadata document could not be composed safely. No request was sent.")
		return
	}
	privateValue, err := encodeProjectSemanticProvenance(ctx, prepared.provenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No request was sent.")
		return
	}
	retainRecovery := func(title, detail string) {
		recoveryCtx := context.WithoutCancel(ctx)
		recovery := partialProjectSemanticRecoveryState(data, projectID)
		unconfigured, encodeErr := encodeProjectSemanticProvenance(recoveryCtx, projectUnconfiguredSemanticProvenance())
		if encodeErr == nil && resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(recoveryCtx, projectMetadataJSONProvenancePrivateKey, unconfigured)...)
			resp.Diagnostics.Append(resp.Private.SetKey(recoveryCtx, projectAcceptedCreateRecoveryPrivateKey, []byte("true"))...)
		}
		resp.Diagnostics.Append(resp.State.Set(recoveryCtx, &recovery)...)
		resp.Diagnostics.AddError(title, detail)
	}

	var result map[string]interface{}
	accepted, createErr := r.client.doRequestWithResponse(ctx, http.MethodPost, "/project/new", projectRequest, &result)
	if createErr != nil {
		if semanticCreateRecoveryRequired(accepted, createErr) {
			if accepted {
				retainRecovery("Project Creation Not Confirmed", "LiteLLM accepted the project create, but its response could not be validated safely. Only the generated identity was retained for authoritative recovery.")
			} else {
				retainRecovery("Project Creation Outcome Uncertain", "The project create was dispatched, but its outcome could not be confirmed. Only the generated identity was retained for authoritative recovery.")
			}
		} else {
			resp.Diagnostics.AddError("Project Creation Failed", "LiteLLM did not confirm the project create. Response, identity, URL, and transport details were omitted.")
		}
		return
	}
	if validateProjectCreateResponseIdentity(result, projectID) != nil {
		retainRecovery("Project Creation Identity Not Confirmed", "LiteLLM accepted the project create, but its response did not confirm the generated identity. Only that identity was retained for authoritative recovery.")
		return
	}
	object, unwrapErr := unwrapObjectEnvelope(result, "project_info", "data")
	if unwrapErr != nil {
		retainRecovery("Project Creation Not Confirmed", "LiteLLM accepted the project create, but its response could not be validated safely. Only the generated identity was retained for authoritative recovery.")
		return
	}

	// Project creation stores duration without setting the initial reset time.
	if knownString(data.BudgetDuration) {
		table, parseErr := parseBudgetTable(object)
		budgetID, presence, budgetErr := budgetTableID(object, table)
		if parseErr != nil || budgetErr != nil || presence != apiValuePresent {
			retainRecovery("Budget Reset Initialization Not Confirmed", "LiteLLM accepted the project create, but reset initialization could not be completed safely. Only the generated identity was retained for recovery.")
			return
		}
		var budgetResult map[string]interface{}
		payload := map[string]interface{}{"budget_id": budgetID, "budget_duration": data.BudgetDuration.ValueString()}
		if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/budget/update", payload, &budgetResult); err != nil {
			retainRecovery("Budget Reset Initialization Not Confirmed", "LiteLLM accepted the project create, but reset initialization could not be confirmed. Only the generated identity was retained for recovery.")
			return
		}
		if confirmedID, ok := budgetResult["budget_id"].(string); !ok || confirmedID != budgetID {
			retainRecovery("Budget Reset Initialization Not Confirmed", "LiteLLM accepted the project create, but reset initialization did not confirm the same budget association. Only the generated identity was retained for recovery.")
			return
		}
	}
	ownership := projectSemanticOwnership{provenance: prepared.provenance, fresh: prepared.provenance.Configured, confirmCurrentValue: prepared.provenance.Configured}
	if err := r.readProjectWithOwnership(ctx, &data, false, ownership); err != nil {
		retainRecovery("Project Creation Not Confirmed", "LiteLLM accepted the project create, but one authoritative identity-bound read did not confirm its complete state. Only the generated identity was retained for recovery.")
		return
	}
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectMetadataJSONProvenancePrivateKey, privateValue)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectAcceptedCreateRecoveryPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectPendingUpdatePrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectPendingBudgetPrivateKey, nil)...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *ProjectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ProjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	var importRaw, provenanceRaw, acceptedRaw, pendingRaw, pendingBudgetRaw []byte
	var importDiagnostics, provenanceDiagnostics, acceptedDiagnostics, pendingDiagnostics, pendingBudgetDiagnostics diag.Diagnostics
	if req.Private != nil {
		importRaw, importDiagnostics = req.Private.GetKey(ctx, numericImportedPrivateKey)
		provenanceRaw, provenanceDiagnostics = req.Private.GetKey(ctx, projectMetadataJSONProvenancePrivateKey)
		acceptedRaw, acceptedDiagnostics = req.Private.GetKey(ctx, projectAcceptedCreateRecoveryPrivateKey)
		pendingRaw, pendingDiagnostics = req.Private.GetKey(ctx, projectPendingUpdatePrivateKey)
		pendingBudgetRaw, pendingBudgetDiagnostics = req.Private.GetKey(ctx, projectPendingBudgetPrivateKey)
	}
	resp.Diagnostics.Append(importDiagnostics...)
	resp.Diagnostics.Append(provenanceDiagnostics...)
	resp.Diagnostics.Append(acceptedDiagnostics...)
	resp.Diagnostics.Append(pendingDiagnostics...)
	resp.Diagnostics.Append(pendingBudgetDiagnostics...)
	if len(acceptedRaw) != 0 && string(acceptedRaw) != "true" {
		resp.Diagnostics.AddError("Invalid Project Recovery State", "Accepted-create recovery state is malformed. No project read was performed.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	provenance, err := decodeProjectSemanticProvenance(ctx, provenanceRaw, data.MetadataJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No project read was performed.")
		return
	}
	pending, err := decodeKeySemanticPendingTransition(ctx, pendingRaw)
	if err != nil || pending.Config.Active || pending.Permissions.Active {
		resp.Diagnostics.AddError("Invalid Project Recovery State", "Pending semantic-update recovery state is malformed. No project read was performed.")
		return
	}
	pendingBudget, err := decodeProjectPendingBudget(ctx, pendingBudgetRaw)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Project Recovery State", "Pending budget-update recovery state is malformed. No project read was performed.")
		return
	}
	reconcile := keySemanticPendingReconcile{}
	ownership := projectSemanticOwnership{
		provenance: provenance, pending: pending, reconcile: &reconcile,
		acceptedCreate: string(acceptedRaw) == "true", pendingBudget: pendingBudget,
		fresh: len(acceptedRaw) != 0 || pending.any() || len(pendingBudget) != 0,
	}
	imported := string(importRaw) == "true"
	if err := r.readProjectWithOwnership(ctx, &data, imported, ownership); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Project Read Failed", "The authoritative project response could not be validated or projected safely. Response, identity, metadata, and transport details were omitted.")
		return
	}
	if reconcile.Present && reconcile.Committed {
		provenance = reconcile.Effective.metadata
	}
	encoded, err := encodeProjectSemanticProvenance(ctx, provenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No project state was produced.")
		return
	}
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectMetadataJSONProvenancePrivateKey, encoded)...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		if imported {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
		}
		if string(acceptedRaw) == "true" {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectAcceptedCreateRecoveryPrivateKey, nil)...)
		}
		if reconcile.Present {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectPendingUpdatePrivateKey, nil)...)
		}
		if len(pendingBudget) != 0 {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectPendingBudgetPrivateKey, nil)...)
		}
	}
}

func (r *ProjectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config ProjectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if req.Config.Raw.Type() == nil {
		config = plan
	} else {
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	}
	var acceptedRaw, pendingRaw, pendingBudgetRaw, provenanceRaw []byte
	var acceptedDiagnostics, pendingDiagnostics, pendingBudgetDiagnostics, provenanceDiagnostics diag.Diagnostics
	if req.Private != nil {
		acceptedRaw, acceptedDiagnostics = req.Private.GetKey(ctx, projectAcceptedCreateRecoveryPrivateKey)
		pendingRaw, pendingDiagnostics = req.Private.GetKey(ctx, projectPendingUpdatePrivateKey)
		pendingBudgetRaw, pendingBudgetDiagnostics = req.Private.GetKey(ctx, projectPendingBudgetPrivateKey)
		provenanceRaw, provenanceDiagnostics = req.Private.GetKey(ctx, projectMetadataJSONProvenancePrivateKey)
	}
	resp.Diagnostics.Append(acceptedDiagnostics...)
	resp.Diagnostics.Append(pendingDiagnostics...)
	resp.Diagnostics.Append(pendingBudgetDiagnostics...)
	resp.Diagnostics.Append(provenanceDiagnostics...)
	if len(pendingRaw) != 0 {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Project Recovery Required", "A prior semantic metadata update has not been reconciled. Refresh must determine whether its shape transition committed before another update can be sent.")
		return
	}
	if len(pendingBudgetRaw) != 0 {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Project Recovery Required", "A prior project budget update has not been reconciled. Refresh must retain its prior public values before another update can be sent.")
		return
	}
	if string(acceptedRaw) == "true" {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Project Recovery Required", "A prior project create was accepted without complete readback. Refresh must reconcile its generated identity before another update can be sent.")
		return
	}
	if len(acceptedRaw) != 0 {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Invalid Project Recovery State", "Accepted-create recovery state is malformed. No project update was sent.")
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if config.MetadataJSON.IsUnknown() {
		resp.Diagnostics.AddError("Unknown Semantic Project Dictionary", "metadata_json must be known before updating a project. No request was sent.")
		return
	}
	priorProvenance, err := decodeProjectSemanticProvenance(ctx, provenanceRaw, state.MetadataJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No project update was sent.")
		return
	}
	prepared, err := prepareProjectSemanticDictionary(ctx, config.MetadataJSON, config.Metadata)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Project Dictionary", "The JSON object is malformed, overlaps another managed project metadata surface, or cannot be persisted exactly. No request was sent.")
		return
	}
	semanticChanged, err := projectSemanticNeedsChange(ctx, config.MetadataJSON, state.MetadataJSON, priorProvenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Project Dictionary", "The semantic value or private ownership could not be compared safely. No request was sent.")
		return
	}
	confirmationOwnership, err := prepared.updateOwnership(ctx, priorProvenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic shape-transition ownership could not be validated safely. No request was sent.")
		return
	}
	pendingTransition := pendingProjectSemanticTransition(confirmationOwnership)
	var pendingPrivate []byte
	if pendingTransition.any() {
		pendingPrivate, err = encodeKeySemanticPendingTransition(ctx, pendingTransition)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Pending semantic shape ownership could not be encoded safely. No request was sent.")
			return
		}
	}
	newProvenanceRaw, err := encodeProjectSemanticProvenance(ctx, prepared.provenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No request was sent.")
		return
	}

	plan.ID = state.ID
	plan.MetadataJSON = config.MetadataJSON
	if state.BudgetID.IsUnknown() || plan.BudgetID.IsUnknown() || !state.BudgetID.Equal(plan.BudgetID) {
		resp.Diagnostics.AddError("Unsafe Project Budget Reassociation", "The project budget association changed or remained unknown despite the plan safety check; no API call was made.")
		return
	}
	importedAlias, importedDescription := false, false
	if req.Private != nil {
		aliasMarker, diagnostics := req.Private.GetKey(ctx, projectImportedAliasPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		descriptionMarker, diagnostics := req.Private.GetKey(ctx, projectImportedDescriptionPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		legacyMarker, diagnostics := req.Private.GetKey(ctx, projectImportedOptionalStringsPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		importedAlias = string(aliasMarker) == "true" || string(legacyMarker) == "true"
		importedDescription = string(descriptionMarker) == "true" || string(legacyMarker) == "true"
	}
	if (!importedAlias && knownString(state.ProjectAlias) && config.ProjectAlias.IsNull()) || (!importedDescription && knownString(state.Description) && config.Description.IsNull()) {
		resp.Diagnostics.AddError("Unsupported Project String Clear", "A project string was removed despite the plan safety check; no API call was made.")
		return
	}
	if config.ModelMaxBudget.IsUnknown() || (knownMap(plan.ModelMaxBudget) && len(plan.ModelMaxBudget.Elements()) > 0 && !plan.ModelMaxBudget.Equal(state.ModelMaxBudget)) {
		resp.Diagnostics.AddError("Unsupported Structured Project Model Budget", "The legacy model budget was unknown or changed despite the plan safety check; no API call was made.")
		return
	}
	projectRequest, rowChanged, err := buildProjectRowUpdateRequest(ctx, &plan, &state)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Project Request", "The project row update could not be converted safely. No request was sent.")
		return
	}
	delete(projectRequest, "metadata")
	budgetRequest, budgetChanged, err := buildProjectBudgetUpdateRequest(&plan, &state)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Project Budget Request", "The project budget update could not be converted safely. No request was sent.")
		return
	}
	projectBudgetSets, directBudgetPatch := splitProjectBudgetUpdateRequest(budgetRequest)
	for field, value := range projectBudgetSets {
		projectRequest[field] = value
	}
	pendingBudget := projectPendingBudgetFromPatch(directBudgetPatch)
	pendingBudgetPrivate, err := encodeProjectPendingBudget(ctx, pendingBudget)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Project Recovery State", "Pending budget recovery state could not be encoded safely. No request was sent.")
		return
	}

	legacyChanged := !plan.Metadata.IsUnknown() && !plan.Metadata.Equal(state.Metadata)
	tagsChanged := !plan.Tags.IsUnknown() && !plan.Tags.Equal(state.Tags)
	rpmChanged := !plan.ModelRPMLimit.IsUnknown() && !plan.ModelRPMLimit.Equal(state.ModelRPMLimit)
	tpmChanged := !plan.ModelTPMLimit.IsUnknown() && !plan.ModelTPMLimit.Equal(state.ModelTPMLimit)
	metadataChanged := semanticChanged || legacyChanged || tagsChanged || rpmChanged || tpmChanged
	var hydrated map[string]interface{}
	if metadataChanged {
		hydrated, err = r.getFreshExactProjectInfo(ctx, state.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Project Metadata Hydration Failed", "The complete identity-bound metadata document could not be read safely. No update request was sent.")
			return
		}
		remoteMetadata, err := projectMetadataObject(ctx, hydrated)
		if err != nil {
			resp.Diagnostics.AddError("Project Metadata Hydration Failed", "The complete metadata document was malformed or not persistable exactly. No update request was sent.")
			return
		}
		replacement, err := composeProjectMetadataReplacement(ctx, remoteMetadata, plan, state, priorProvenance, prepared)
		if err != nil {
			resp.Diagnostics.AddError("Project Metadata Composition Failed", "The complete metadata replacement could not be composed safely. No update request was sent.")
			return
		}
		projectRequest["metadata"] = replacement
	}

	var budgetID string
	if budgetChanged {
		if hydrated != nil {
			budgetID, err = validateProjectBudgetFromInfo(hydrated, state.BudgetID)
		} else {
			budgetID, err = r.lookupProjectBudgetID(ctx, state.ID.ValueString(), state.BudgetID)
		}
		if err != nil {
			resp.Diagnostics.AddError("Project Budget Lookup Error", "The authoritative budget association could not be validated. No update request was sent.")
			return
		}
		if len(directBudgetPatch) > 0 {
			directBudgetPatch["budget_id"] = budgetID
		}
	}
	projectChanged := rowChanged || len(projectBudgetSets) > 0 || metadataChanged
	retainPrior := func(localCtx context.Context) {
		if len(pendingPrivate) != 0 && resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(localCtx, projectPendingUpdatePrivateKey, pendingPrivate)...)
		}
		resp.Diagnostics.Append(resp.State.Set(localCtx, &state)...)
	}
	retainPriorWithBudget := func(localCtx context.Context) {
		if len(pendingBudgetPrivate) != 0 && resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(localCtx, projectPendingBudgetPrivateKey, pendingBudgetPrivate)...)
		}
		retainPrior(localCtx)
	}
	if projectChanged {
		projectRequest["project_id"] = state.ID.ValueString()
		var result map[string]interface{}
		if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/project/update", projectRequest, &result); err != nil {
			if metadataChanged || len(pendingBudgetPrivate) != 0 {
				retainPriorWithBudget(context.WithoutCancel(ctx))
				resp.Diagnostics.AddError("Project Update Not Confirmed", "The project update may have been dispatched, but its metadata or budget outcome was not confirmed. Prior public and private state were retained.")
			} else {
				resp.Diagnostics.AddError("Project Update Failed", "The project update failed. Response, identity, URL, and transport details were omitted.")
			}
			return
		}
		object, unwrapErr := unwrapObjectEnvelope(result, "project_info", "data")
		if unwrapErr != nil || validateImportedObjectIdentity(true, "project update", object, "project_id", state.ID.ValueString()) != nil {
			if metadataChanged || len(pendingBudgetPrivate) != 0 {
				retainPriorWithBudget(context.WithoutCancel(ctx))
				resp.Diagnostics.AddError("Project Update Not Confirmed", "LiteLLM accepted the project update, but its response did not confirm the same identity. Prior public and private state were retained.")
			} else {
				resp.Diagnostics.AddError("Invalid API Response", "LiteLLM did not return the matching project identity.")
			}
			return
		}
	}
	if len(directBudgetPatch) > 0 {
		var result map[string]interface{}
		if err := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/budget/update", directBudgetPatch, &result); err != nil {
			retainPriorWithBudget(context.WithoutCancel(ctx))
			resp.Diagnostics.AddError("Project Budget Update Not Confirmed", "A preceding project update may have committed, but the reset or clear phase could not be confirmed. Prior state was retained.")
			return
		}
		confirmedID, ok := result["budget_id"].(string)
		if !ok || confirmedID != budgetID {
			retainPriorWithBudget(context.WithoutCancel(ctx))
			resp.Diagnostics.AddError("Project Budget Update Not Confirmed", "The reset or clear phase returned a malformed response. Prior state was retained.")
			return
		}
	}
	desired := plan
	seedProjectClearOwnership(&plan, &state)
	readOwnership := projectSemanticOwnership{provenance: priorProvenance}
	if metadataChanged {
		readOwnership = confirmationOwnership
	}
	if err := r.readProjectWithOwnership(ctx, &plan, false, readOwnership); err != nil {
		if metadataChanged {
			retainPrior(context.WithoutCancel(ctx))
			resp.Diagnostics.AddError("Project Metadata Update Not Confirmed", "LiteLLM accepted the update, but one authoritative identity-bound read did not confirm the complete metadata transition. Prior public and private state were retained.")
		} else {
			resp.Diagnostics.AddError("Project Update Readback Failed", "The project update was accepted, but authoritative readback failed. Prior state was retained; response, identity, URL, and transport details were omitted.")
		}
		return
	}
	if _, ok := projectChangedFieldMismatch(&desired, &state, &plan); ok {
		if metadataChanged {
			retainPrior(context.WithoutCancel(ctx))
		}
		resp.Diagnostics.AddError("Project Update Did Not Converge", "LiteLLM accepted the update, but authoritative readback did not match the plan. Prior state was retained.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectMetadataJSONProvenancePrivateKey, newProvenanceRaw)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectPendingUpdatePrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectPendingBudgetPrivateKey, nil)...)
		transitions := []struct{ importKey, pendingKey string }{
			{organizationProjectImportedBudgetPrivateKey, organizationProjectBudgetOwnershipPendingPrivateKey},
			{projectImportedAliasPrivateKey, projectAliasOwnershipPendingPrivateKey},
			{projectImportedDescriptionPrivateKey, projectDescriptionOwnershipPendingPrivateKey},
		}
		for _, transition := range transitions {
			marker, diagnostics := resp.Private.GetKey(ctx, transition.pendingKey)
			resp.Diagnostics.Append(diagnostics...)
			if string(marker) == "true" {
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, transition.importKey, nil)...)
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, transition.pendingKey, nil)...)
			}
		}
	}
}

func (r *ProjectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ProjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	var acceptedRaw, pendingRaw, pendingBudgetRaw []byte
	if req.Private != nil {
		var diagnostics diag.Diagnostics
		acceptedRaw, diagnostics = req.Private.GetKey(ctx, projectAcceptedCreateRecoveryPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		pendingRaw, diagnostics = req.Private.GetKey(ctx, projectPendingUpdatePrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		pendingBudgetRaw, diagnostics = req.Private.GetKey(ctx, projectPendingBudgetPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
	}
	if len(pendingRaw) != 0 {
		resp.Diagnostics.AddError("Project Recovery Required", "A prior semantic metadata update has not been reconciled. Refresh must reconcile it before deletion can be sent.")
		return
	}
	if len(pendingBudgetRaw) != 0 {
		resp.Diagnostics.AddError("Project Recovery Required", "A prior project budget update has not been reconciled. Refresh must reconcile it before deletion can be sent.")
		return
	}
	if string(acceptedRaw) == "true" {
		resp.Diagnostics.AddError("Project Recovery Required", "A prior project create was accepted without complete readback. Refresh must reconcile it before deletion can be sent.")
		return
	}
	if len(acceptedRaw) != 0 {
		resp.Diagnostics.AddError("Invalid Project Recovery State", "Accepted-create recovery state is malformed. No project deletion was sent.")
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DoRequestWithResponse(ctx, http.MethodDelete, "/project/delete", map[string]interface{}{"project_ids": []string{data.ID.ValueString()}}, nil); err != nil && !IsNotFoundError(err) {
		resp.Diagnostics.AddError("Project Delete Failed", "The project deletion failed. Response, identity, URL, and transport details were omitted.")
	}
}

func (r *ProjectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	provenance, err := encodeProjectSemanticProvenance(ctx, projectUnconfiguredSemanticProvenance())
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Import ownership could not be initialized safely.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata_json"), types.StringNull())...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectMetadataJSONProvenancePrivateKey, provenance)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectAcceptedCreateRecoveryPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectPendingUpdatePrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectPendingBudgetPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationProjectImportedBudgetPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectImportedAliasPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, projectImportedDescriptionPrivateKey, []byte("true"))...)
	}
}

// UpgradeState performs the direct v0-to-v1 migration without adopting any
// remotely observed metadata.
func (r *ProjectResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: nil,
			StateUpgrader: func(_ context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				if req.RawState == nil {
					resp.Diagnostics.AddError("Unable to Upgrade State", "Prior project state is unavailable.")
					return
				}
				upgraded, err := marshalProjectUpgrade(req.RawState.JSON)
				if err != nil {
					resp.Diagnostics.AddError("Unable to Upgrade State", "Prior project state could not be decoded safely.")
					return
				}
				resp.DynamicValue = &tfprotov6.DynamicValue{JSON: upgraded}
			},
		},
	}
}

// buildProjectRequest is retained as the create-payload test seam used by
// earlier releases. Updates are split into project-row and budget requests.
func (r *ProjectResource) buildProjectRequest(ctx context.Context, data *ProjectResourceModel) (map[string]interface{}, error) {
	return r.buildProjectCreateRequest(ctx, data)
}

func (r *ProjectResource) buildProjectCreateRequest(ctx context.Context, data *ProjectResourceModel) (map[string]interface{}, error) {
	if knownString(data.BudgetID) && projectBudgetControlsConfigured(data) {
		return nil, fmt.Errorf("budget_id cannot be combined with project budget controls during creation because LiteLLM v1.98 ignores or strips those controls for an existing shared budget")
	}
	if knownMap(data.ModelMaxBudget) && len(data.ModelMaxBudget.Elements()) > 0 {
		return nil, fmt.Errorf("non-empty model_max_budget is unsupported because LiteLLM v1.98 requires structured GenericBudgetConfig values while the migration-compatible project schema is map(float64)")
	}
	request := map[string]interface{}{"team_id": data.TeamID.ValueString()}
	if knownString(data.ProjectAlias) {
		request["project_alias"] = data.ProjectAlias.ValueString()
	}
	if knownString(data.Description) {
		request["description"] = data.Description.ValueString()
	}
	if knownString(data.BudgetID) {
		request["budget_id"] = data.BudgetID.ValueString()
	}
	if knownString(data.BudgetDuration) {
		request["budget_duration"] = data.BudgetDuration.ValueString()
	}
	addKnownFloat(request, "max_budget", data.MaxBudget)
	addKnownFloat(request, "soft_budget", data.SoftBudget)
	addKnownInt(request, "tpm_limit", data.TPMLimit)
	addKnownInt(request, "rpm_limit", data.RPMLimit)
	addKnownInt(request, "max_parallel_requests", data.MaxParallelRequests)
	if !data.ModelMaxBudget.IsNull() && !data.ModelMaxBudget.IsUnknown() {
		values, err := float64RequestMap(data.ModelMaxBudget, "model_max_budget")
		if err != nil {
			return nil, err
		}
		request["model_max_budget"] = values
	}
	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		models, err := stringListRequest(ctx, data.Models, "models")
		if err != nil {
			return nil, err
		}
		request["models"] = models
	}
	if !data.Blocked.IsNull() && !data.Blocked.IsUnknown() {
		request["blocked"] = data.Blocked.ValueBool()
	}
	metadata, managed, err := projectMetadataPayload(ctx, data)
	if err != nil {
		return nil, err
	}
	if managed {
		request["metadata"] = metadata
	}
	return request, nil
}

func buildProjectRowUpdateRequest(ctx context.Context, plan, state *ProjectResourceModel) (map[string]interface{}, bool, error) {
	request := map[string]interface{}{}
	if !plan.ProjectAlias.IsUnknown() && !plan.ProjectAlias.Equal(state.ProjectAlias) {
		if plan.ProjectAlias.IsNull() {
			return nil, false, fmt.Errorf("project_alias cannot be cleared because LiteLLM v1.98 excludes null on update")
		}
		request["project_alias"] = plan.ProjectAlias.ValueString()
	}
	if !plan.Description.IsUnknown() && !plan.Description.Equal(state.Description) {
		if plan.Description.IsNull() {
			return nil, false, fmt.Errorf("description cannot be cleared because LiteLLM v1.98 excludes null on update")
		}
		request["description"] = plan.Description.ValueString()
	}
	if !plan.Models.IsUnknown() && !plan.Models.Equal(state.Models) {
		if plan.Models.IsNull() {
			return nil, false, fmt.Errorf("models cannot be cleared with null; configure an empty list")
		}
		models, err := stringListRequest(ctx, plan.Models, "models")
		if err != nil {
			return nil, false, err
		}
		request["models"] = models
	}
	if !plan.Blocked.IsUnknown() && !plan.Blocked.Equal(state.Blocked) {
		if plan.Blocked.IsNull() {
			return nil, false, fmt.Errorf("blocked cannot be cleared with null; configure false")
		}
		request["blocked"] = plan.Blocked.ValueBool()
	}
	metadataChanged := (!plan.Metadata.IsUnknown() && !plan.Metadata.Equal(state.Metadata)) || (!plan.Tags.IsUnknown() && !plan.Tags.Equal(state.Tags)) || (!plan.ModelRPMLimit.IsUnknown() && !plan.ModelRPMLimit.Equal(state.ModelRPMLimit)) || (!plan.ModelTPMLimit.IsUnknown() && !plan.ModelTPMLimit.Equal(state.ModelTPMLimit))
	if metadataChanged {
		metadata, _, err := projectMetadataPayload(ctx, plan)
		if err != nil {
			return nil, false, err
		}
		request["metadata"] = metadata
	}
	return request, len(request) > 0, nil
}

func buildProjectBudgetUpdateRequest(plan, state *ProjectResourceModel) (map[string]interface{}, bool, error) {
	request := map[string]interface{}{}
	addChangedFloat(request, "max_budget", plan.MaxBudget, state.MaxBudget)
	addChangedFloat(request, "soft_budget", plan.SoftBudget, state.SoftBudget)
	addChangedInt(request, "tpm_limit", plan.TPMLimit, state.TPMLimit)
	addChangedInt(request, "rpm_limit", plan.RPMLimit, state.RPMLimit)
	addChangedInt(request, "max_parallel_requests", plan.MaxParallelRequests, state.MaxParallelRequests)
	if !plan.BudgetDuration.IsUnknown() && !plan.BudgetDuration.Equal(state.BudgetDuration) {
		if plan.BudgetDuration.IsNull() {
			request["budget_duration"] = nil
			request["budget_reset_at"] = nil
		} else {
			request["budget_duration"] = plan.BudgetDuration.ValueString()
		}
	}
	if !plan.ModelMaxBudget.IsUnknown() && !plan.ModelMaxBudget.Equal(state.ModelMaxBudget) {
		if plan.ModelMaxBudget.IsNull() {
			request["model_max_budget"] = nil
		} else {
			values, err := float64RequestMap(plan.ModelMaxBudget, "model_max_budget")
			if err != nil {
				return nil, false, err
			}
			request["model_max_budget"] = values
		}
	}
	return request, len(request) > 0, nil
}

// splitProjectBudgetUpdateRequest follows the exact v1.98 endpoint contracts:
// /project/update validates non-null project limits against the parent team but
// drops nulls with exclude_none=True; /budget/update preserves explicitly sent
// nulls with exclude_unset=True and recomputes the reset timestamp for a sent
// duration. A duration therefore goes through the validating route first and is
// replayed only to initialize its reset schedule.
func splitProjectBudgetUpdateRequest(request map[string]interface{}) (map[string]interface{}, map[string]interface{}) {
	projectSets := map[string]interface{}{}
	directPatch := map[string]interface{}{}
	for field, value := range request {
		if value == nil {
			directPatch[field] = nil
			continue
		}
		projectSets[field] = value
		if field == "budget_duration" {
			directPatch[field] = value
		}
	}
	return projectSets, directPatch
}

func projectMetadataPayload(ctx context.Context, data *ProjectResourceModel) (map[string]interface{}, bool, error) {
	metadata := map[string]interface{}{}
	managed := false
	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		managed = true
		var values map[string]string
		if diagnostics := data.Metadata.ElementsAs(ctx, &values, false); diagnostics.HasError() {
			return nil, false, fmt.Errorf("metadata contains a value that cannot be represented as a string")
		}
		metadata = convertMetadataToNative(values)
		delete(metadata, "tags")
		delete(metadata, "model_rpm_limit")
		delete(metadata, "model_tpm_limit")
	}
	if !data.Tags.IsNull() && !data.Tags.IsUnknown() {
		managed = true
		tags, err := stringListRequest(ctx, data.Tags, "tags")
		if err != nil {
			return nil, false, err
		}
		metadata["tags"] = tags
	}
	if !data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown() {
		managed = true
		values, err := int64RequestMap(data.ModelRPMLimit, "model_rpm_limit")
		if err != nil {
			return nil, false, err
		}
		metadata["model_rpm_limit"] = values
	}
	if !data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown() {
		managed = true
		values, err := int64RequestMap(data.ModelTPMLimit, "model_tpm_limit")
		if err != nil {
			return nil, false, err
		}
		metadata["model_tpm_limit"] = values
	}
	return metadata, managed, nil
}

func (r *ProjectResource) getFreshExactProjectInfo(ctx context.Context, projectID string) (map[string]interface{}, error) {
	if projectID == "" {
		return nil, errSemanticDictionaryTraversal
	}
	var result map[string]interface{}
	query := url.Values{"project_id": []string{projectID}}
	endpoint := endpointWithQuery("/project/info", query)
	if err := r.client.doFreshRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
		return nil, err
	}
	object, err := unwrapObjectEnvelope(result, "project_info", "data")
	if err != nil || validateImportedObjectIdentity(true, "project", object, "project_id", projectID) != nil {
		return nil, errSemanticDictionaryTraversal
	}
	return object, nil
}

func validateProjectBudgetFromInfo(object map[string]interface{}, configured types.String) (string, error) {
	table, err := parseBudgetTable(object)
	if err != nil {
		return "", err
	}
	budgetID, presence, err := budgetTableID(object, table)
	if err != nil || presence != apiValuePresent {
		return "", errSemanticDictionaryTraversal
	}
	if knownString(configured) && configured.ValueString() != budgetID {
		return "", errSemanticDictionaryTraversal
	}
	return budgetID, nil
}

func (r *ProjectResource) lookupProjectBudgetID(ctx context.Context, projectID string, configured types.String) (string, error) {
	var result map[string]interface{}
	query := url.Values{"project_id": []string{projectID}}
	endpoint := endpointWithQuery("/project/info", query)
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return "", fmt.Errorf("unable to read authoritative project budget: %w", err)
	}
	object, err := unwrapObjectEnvelope(result, "project_info", "data")
	if err != nil {
		return "", err
	}
	if err := validateImportedObjectIdentity(true, "project budget lookup", object, "project_id", projectID); err != nil {
		return "", err
	}
	table, err := parseBudgetTable(object)
	if err != nil {
		return "", err
	}
	budgetID, presence, err := budgetTableID(object, table)
	if err != nil {
		return "", err
	}
	if presence != apiValuePresent {
		return "", fmt.Errorf("project %q has no authoritative budget identity", projectID)
	}
	if knownString(configured) && configured.ValueString() != budgetID {
		return "", fmt.Errorf("project budget reassociation detected: state budget_id %q, API budget_id %q", configured.ValueString(), budgetID)
	}
	return budgetID, nil
}

func (r *ProjectResource) readProject(ctx context.Context, data *ProjectResourceModel) error {
	return r.readProjectWithOwnership(ctx, data, false, projectSemanticOwnership{provenance: projectUnconfiguredSemanticProvenance()})
}

func (r *ProjectResource) readProjectWithNumericOwnership(ctx context.Context, data *ProjectResourceModel, imported bool) error {
	return r.readProjectWithOwnership(ctx, data, imported, projectSemanticOwnership{provenance: projectUnconfiguredSemanticProvenance()})
}

func (r *ProjectResource) readProjectWithOwnership(ctx context.Context, data *ProjectResourceModel, imported bool, ownership projectSemanticOwnership) error {
	projectID := data.ID.ValueString()
	if projectID == "" {
		return fmt.Errorf("project ID is empty, cannot read project")
	}
	var object map[string]interface{}
	var err error
	if ownership.fresh {
		object, err = r.getFreshExactProjectInfo(ctx, projectID)
	} else {
		var result map[string]interface{}
		query := url.Values{"project_id": []string{projectID}}
		endpoint := endpointWithQuery("/project/info", query)
		if err = r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &result); err == nil {
			object, err = unwrapObjectEnvelope(result, "project_info", "data")
			if err == nil {
				err = validateImportedObjectIdentity(true, "project", object, "project_id", projectID)
			}
		}
	}
	if err != nil {
		return err
	}
	metadataObject, err := projectMetadataObject(ctx, object)
	if err != nil {
		return err
	}
	if ownership.pending.any() {
		var reconcile keySemanticPendingReconcile
		ownership, reconcile, err = resolveProjectSemanticPending(ctx, metadataObject, ownership)
		if err != nil {
			return errSemanticDictionaryTraversal
		}
		if ownership.reconcile != nil {
			*ownership.reconcile = reconcile
		}
	}
	remoteTeam, teamPresence, err := apiValueAt(object, "team_id")
	if err != nil || teamPresence != apiValuePresent {
		return errSemanticDictionaryTraversal
	}
	teamID, ok := remoteTeam.(string)
	if !ok || teamID == "" {
		return errSemanticDictionaryTraversal
	}
	// team_id is required public configuration and cannot be repaired through
	// the project update endpoint. Every authoritative read must confirm the
	// configured association instead of silently adopting a reassignment.
	if knownString(data.TeamID) && teamID != data.TeamID.ValueString() {
		return errSemanticDictionaryTraversal
	}
	original := data
	next := *data
	data = &next
	table, err := parseBudgetTable(object)
	if err != nil {
		return err
	}
	remoteBudgetID, budgetPresence, err := budgetTableID(object, table)
	if err != nil {
		return err
	}
	if table.presence == apiValuePresent {
		_, nestedPresence, nestedErr := table.value("budget_id")
		if nestedErr != nil || nestedPresence == apiValueNull {
			// A present relation originates from a database row whose primary key is
			// non-null. Explicit null is malformed, not authoritative absence.
			return errSemanticDictionaryTraversal
		}
	}
	knownBudgetID := knownString(data.BudgetID)
	budgetOwned := imported || knownBudgetID
	if budgetPresence == apiValuePresent && budgetOwned {
		// Project budget authority is the nested relation. The top-level foreign
		// key is only a consistency copy and cannot establish import ownership,
		// consume recovery, or replace a configured/imported identity by itself.
		nestedBudgetID, nestedPresence, nestedErr := table.value("budget_id")
		if nestedErr != nil || nestedPresence != apiValuePresent {
			return errSemanticDictionaryTraversal
		}
		nestedString, ok := nestedBudgetID.(string)
		if !ok || nestedString == "" || nestedString != remoteBudgetID {
			return errSemanticDictionaryTraversal
		}
		if knownBudgetID && data.BudgetID.ValueString() != remoteBudgetID {
			return errSemanticDictionaryTraversal
		}
		data.BudgetID = types.StringValue(remoteBudgetID)
	} else if knownString(data.BudgetID) && budgetPresence != apiValuePresent {
		// A retained budget identity represents configured/imported authority.
		// Missing readback cannot prove that association and project update cannot
		// repair it, so preserve prior state by failing the transactional read.
		return errSemanticDictionaryTraversal
	} else if imported || data.BudgetID.IsUnknown() {
		// Imports may authoritatively establish that no shared budget exists. An
		// ordinary omitted create must likewise not adopt LiteLLM's generated ID.
		data.BudgetID = types.StringNull()
	}
	data.ID = types.StringValue(projectID)
	if err := updateProjectOptionalString(&data.ProjectAlias, object, "project_alias", imported); err != nil {
		return err
	}
	if err := updateProjectOptionalString(&data.Description, object, "description", imported); err != nil {
		return err
	}
	if err := updateNullableString(&data.TeamID, object, "team_id"); err != nil {
		return err
	}
	for _, field := range []struct {
		name   string
		target *types.String
	}{
		{"created_at", &data.CreatedAt}, {"updated_at", &data.UpdatedAt}, {"created_by", &data.CreatedBy}, {"updated_by", &data.UpdatedBy},
	} {
		if err := updateNullableString(field.target, object, field.name); err != nil {
			return err
		}
	}
	models, modelsPresence, err := stringListFromAPI(object, "models")
	if err != nil {
		return err
	}
	if modelsPresence == apiValuePresent {
		data.Models = models
	} else if data.Models.IsUnknown() {
		data.Models = types.ListNull(types.StringType)
	}
	if blocked, presence, err := apiValueAt(object, "blocked"); err != nil {
		return err
	} else if presence == apiValuePresent {
		value, ok := blocked.(bool)
		if !ok {
			return fmt.Errorf("invalid response field %q: expected a boolean", "blocked")
		}
		data.Blocked = types.BoolValue(value)
	} else {
		data.Blocked = types.BoolNull()
	}

	for _, field := range []struct {
		name   string
		target *types.Float64
	}{
		{"max_budget", &data.MaxBudget}, {"soft_budget", &data.SoftBudget},
	} {
		owned := imported || knownFloat(*field.target)
		if err := updateBudgetFloat64(field.target, table, owned, owned, field.name); err != nil {
			return err
		}
	}
	for _, field := range []struct {
		name   string
		target *types.Int64
	}{
		{"tpm_limit", &data.TPMLimit}, {"rpm_limit", &data.RPMLimit}, {"max_parallel_requests", &data.MaxParallelRequests},
	} {
		owned := imported || knownInt(*field.target)
		if err := updateBudgetInt64(field.target, table, owned, owned, field.name); err != nil {
			return err
		}
	}
	durationOwned := imported || knownString(data.BudgetDuration)
	if err := updateBudgetDuration(&data.BudgetDuration, table, durationOwned, durationOwned); err != nil {
		return err
	}
	if err := updateLegacyProjectModelMaxBudget(&data.ModelMaxBudget, table); err != nil {
		return err
	}
	// A failed second-phase /budget/update must not let refresh adopt values
	// already written by /project/update, or Terraform would lose the diff that
	// retries reset initialization and explicit clears. The marker stores only
	// field names; desired/prior values remain in normal public state.
	if ownership.pendingBudget["max_budget"] {
		data.MaxBudget = original.MaxBudget
	}
	if ownership.pendingBudget["soft_budget"] {
		data.SoftBudget = original.SoftBudget
	}
	if ownership.pendingBudget["budget_duration"] {
		data.BudgetDuration = original.BudgetDuration
	}
	if ownership.pendingBudget["tpm_limit"] {
		data.TPMLimit = original.TPMLimit
	}
	if ownership.pendingBudget["rpm_limit"] {
		data.RPMLimit = original.RPMLimit
	}
	if ownership.pendingBudget["max_parallel_requests"] {
		data.MaxParallelRequests = original.MaxParallelRequests
	}
	if ownership.pendingBudget["model_max_budget"] {
		data.ModelMaxBudget = original.ModelMaxBudget
	}

	nextMetadata, err := projectProjectLegacyMetadata(ctx, data.Metadata, metadataObject, ownership)
	if err != nil {
		return err
	}
	nextJSON, err := projectProjectSemanticMetadata(ctx, data.MetadataJSON, metadataObject, ownership)
	if err != nil {
		return err
	}
	nextTags := data.Tags
	tags, tagsPresence, diagnostics := strictAPIStringList(ctx, metadataObject, "tags", path.Root("tags"))
	if err := collectionProjectionError(ctx, diagnostics); err != nil {
		return err
	}
	if tagsPresence == apiValuePresent {
		nextTags = tags
	} else {
		nextTags = types.ListNull(types.StringType)
	}
	nextRPM, nextTPM := data.ModelRPMLimit, data.ModelTPMLimit
	rpmOwned := imported || knownMap(nextRPM)
	if err := updateInt64MapFromAPI(&nextRPM, object, rpmOwned, rpmOwned, "metadata", "model_rpm_limit"); err != nil {
		return err
	}
	tpmOwned := imported || knownMap(nextTPM)
	if err := updateInt64MapFromAPI(&nextTPM, object, tpmOwned, tpmOwned, "metadata", "model_tpm_limit"); err != nil {
		return err
	}
	data.Metadata, data.MetadataJSON, data.Tags, data.ModelRPMLimit, data.ModelTPMLimit = nextMetadata, nextJSON, nextTags, nextRPM, nextTPM
	*original = *data
	return nil
}

func seedProjectClearOwnership(target, prior *ProjectResourceModel) {
	if target.MaxBudget.IsNull() && knownFloat(prior.MaxBudget) {
		target.MaxBudget = prior.MaxBudget
	}
	if target.SoftBudget.IsNull() && knownFloat(prior.SoftBudget) {
		target.SoftBudget = prior.SoftBudget
	}
	if target.TPMLimit.IsNull() && knownInt(prior.TPMLimit) {
		target.TPMLimit = prior.TPMLimit
	}
	if target.RPMLimit.IsNull() && knownInt(prior.RPMLimit) {
		target.RPMLimit = prior.RPMLimit
	}
	if target.MaxParallelRequests.IsNull() && knownInt(prior.MaxParallelRequests) {
		target.MaxParallelRequests = prior.MaxParallelRequests
	}
	if target.BudgetDuration.IsNull() && knownString(prior.BudgetDuration) {
		target.BudgetDuration = prior.BudgetDuration
	}
	if target.ModelMaxBudget.IsNull() && knownMap(prior.ModelMaxBudget) {
		target.ModelMaxBudget = prior.ModelMaxBudget
	}
	if target.ModelRPMLimit.IsNull() && knownMap(prior.ModelRPMLimit) {
		target.ModelRPMLimit = prior.ModelRPMLimit
	}
	if target.ModelTPMLimit.IsNull() && knownMap(prior.ModelTPMLimit) {
		target.ModelTPMLimit = prior.ModelTPMLimit
	}
}

func projectChangedFieldMismatch(desired, prior, actual *ProjectResourceModel) (string, bool) {
	for _, field := range []struct {
		name                   string
		desired, prior, actual attr.Value
	}{
		{"project_alias", desired.ProjectAlias, prior.ProjectAlias, actual.ProjectAlias}, {"description", desired.Description, prior.Description, actual.Description},
		{"models", desired.Models, prior.Models, actual.Models}, {"blocked", desired.Blocked, prior.Blocked, actual.Blocked},
		{"metadata", desired.Metadata, prior.Metadata, actual.Metadata}, {"metadata_json", desired.MetadataJSON, prior.MetadataJSON, actual.MetadataJSON}, {"tags", desired.Tags, prior.Tags, actual.Tags},
		{"max_budget", desired.MaxBudget, prior.MaxBudget, actual.MaxBudget}, {"soft_budget", desired.SoftBudget, prior.SoftBudget, actual.SoftBudget},
		{"budget_duration", desired.BudgetDuration, prior.BudgetDuration, actual.BudgetDuration},
		{"tpm_limit", desired.TPMLimit, prior.TPMLimit, actual.TPMLimit}, {"rpm_limit", desired.RPMLimit, prior.RPMLimit, actual.RPMLimit},
		{"max_parallel_requests", desired.MaxParallelRequests, prior.MaxParallelRequests, actual.MaxParallelRequests},
		{"model_max_budget", desired.ModelMaxBudget, prior.ModelMaxBudget, actual.ModelMaxBudget}, {"model_rpm_limit", desired.ModelRPMLimit, prior.ModelRPMLimit, actual.ModelRPMLimit}, {"model_tpm_limit", desired.ModelTPMLimit, prior.ModelTPMLimit, actual.ModelTPMLimit},
	} {
		if !field.desired.IsUnknown() && !field.desired.Equal(field.prior) && !field.desired.Equal(field.actual) {
			return field.name, true
		}
	}
	return "", false
}

func updateProjectOptionalString(target *types.String, object map[string]interface{}, field string, imported bool) error {
	owned := imported || knownString(*target)
	value, presence, err := apiValueAt(object, field)
	if err != nil {
		return err
	}
	if presence != apiValuePresent {
		if owned || target.IsUnknown() {
			*target = types.StringNull()
		}
		return nil
	}
	stringValue, ok := value.(string)
	if !ok {
		return fmt.Errorf("invalid response field %q: expected a string or null", field)
	}
	if owned {
		*target = types.StringValue(stringValue)
	} else if target.IsUnknown() {
		*target = types.StringNull()
	}
	return nil
}

func projectBudgetControlsConfigured(data *ProjectResourceModel) bool {
	return knownFloat(data.MaxBudget) || knownFloat(data.SoftBudget) || knownString(data.BudgetDuration) ||
		knownInt(data.TPMLimit) || knownInt(data.RPMLimit) || knownInt(data.MaxParallelRequests) || knownMap(data.ModelMaxBudget)
}

func projectBudgetControlsPresentInConfig(data *ProjectResourceModel) bool {
	return !data.MaxBudget.IsNull() || !data.SoftBudget.IsNull() || !data.BudgetDuration.IsNull() ||
		!data.TPMLimit.IsNull() || !data.RPMLimit.IsNull() || !data.MaxParallelRequests.IsNull() || !data.ModelMaxBudget.IsNull()
}

func updateLegacyProjectModelMaxBudget(target *types.Map, table budgetTableState) error {
	// The public project schema predates LiteLLM v1.98 and can only represent
	// map(float64), while v1.98 returns map(GenericBudgetConfig). Never adopt a
	// remote value into unconfigured/import state, and retain existing legacy
	// scalar state when the authoritative value has the structured v1.98 shape.
	if target.IsNull() || target.IsUnknown() {
		if target.IsUnknown() {
			*target = types.MapNull(types.Float64Type)
		}
		return nil
	}
	value, presence, err := table.value("model_max_budget")
	if err != nil {
		return err
	}
	if presence != apiValuePresent {
		*target = types.MapNull(types.Float64Type)
		return nil
	}
	object, ok := value.(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid response field %q: expected an object", "litellm_budget_table.model_max_budget")
	}
	for _, raw := range object {
		if _, structured := raw.(map[string]interface{}); structured {
			return nil
		}
	}
	return updateFloat64MapFromAPI(target, table.object, true, true, "model_max_budget")
}

func updateNullableString(target *types.String, object map[string]interface{}, field string) error {
	value, exists := object[field]
	if !exists || value == nil {
		*target = types.StringNull()
		return nil
	}
	stringValue, ok := value.(string)
	if !ok {
		return fmt.Errorf("invalid response field %q: expected a string or null", field)
	}
	*target = types.StringValue(stringValue)
	return nil
}
