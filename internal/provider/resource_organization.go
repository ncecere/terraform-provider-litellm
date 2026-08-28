package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

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

var _ resource.Resource = &OrganizationResource{}
var _ resource.ResourceWithImportState = &OrganizationResource{}
var _ resource.ResourceWithModifyPlan = &OrganizationResource{}
var _ resource.ResourceWithUpgradeState = &OrganizationResource{}

func NewOrganizationResource() resource.Resource { return &OrganizationResource{} }

type OrganizationResource struct{ client *Client }

type OrganizationResourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	OrganizationID      types.String  `tfsdk:"organization_id"`
	OrganizationAlias   types.String  `tfsdk:"organization_alias"`
	Models              types.List    `tfsdk:"models"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	ModelRPMLimit       types.Map     `tfsdk:"model_rpm_limit"`
	ModelTPMLimit       types.Map     `tfsdk:"model_tpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	Metadata            types.Map     `tfsdk:"metadata"`
	MetadataJSON        types.String  `tfsdk:"metadata_json"`
	Blocked             types.Bool    `tfsdk:"blocked"`
	Tags                types.List    `tfsdk:"tags"`
	CreatedAt           types.String  `tfsdk:"created_at"`
}

func (r *OrganizationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization"
}

func (r *OrganizationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM organization. Budget controls are read authoritatively from LiteLLM v1.98's nested litellm_budget_table relation.",
		Version:     1,
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Description: "The unique identifier for this organization (same as organization_id).", Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"organization_id":       schema.StringAttribute{Description: "The organization ID. If not specified, one will be generated.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()}},
			"organization_alias":    schema.StringAttribute{Description: "The name/alias of the organization.", Required: true},
			"models":                schema.ListAttribute{Description: "The models the organization has access to.", Optional: true, Computed: true, ElementType: types.StringType},
			"budget_id":             schema.StringAttribute{Description: "The ID for the organization's budget. Reassociating an existing organization is not supported safely by LiteLLM v1.98.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"max_budget":            schema.Float64Attribute{Description: "Maximum hard budget for the organization.", Optional: true},
			"soft_budget":           schema.Float64Attribute{Description: "Soft budget alert threshold for the organization.", Optional: true},
			"tpm_limit":             schema.Int64Attribute{Description: "Maximum tokens per minute for the organization.", Optional: true},
			"rpm_limit":             schema.Int64Attribute{Description: "Maximum requests per minute for the organization.", Optional: true},
			"max_parallel_requests": schema.Int64Attribute{Description: "Maximum parallel requests for the organization budget.", Optional: true},
			"model_rpm_limit":       schema.MapAttribute{Description: "The RPM limit per model. Updated through v1.98's transactional complete-metadata replacement so owned keys can clear safely.", Optional: true, Computed: true, ElementType: types.Int64Type, Validators: []validator.Map{mapvalidator.NoNullValues()}},
			"model_tpm_limit":       schema.MapAttribute{Description: "The TPM limit per model. Updated through v1.98's transactional complete-metadata replacement so owned keys can clear safely.", Optional: true, Computed: true, ElementType: types.Int64Type, Validators: []validator.Map{mapvalidator.NoNullValues()}},
			"budget_duration":       schema.StringAttribute{Description: "Budget reset duration (for example, '30d' or '1h').", Optional: true},
			"metadata":              schema.MapAttribute{Description: "Metadata for the organization.", Optional: true, Computed: true, ElementType: types.StringType},
			"metadata_json":         schema.StringAttribute{Description: "Additional organization metadata as a semantic JSON object.", Optional: true, Computed: true, Sensitive: true, Validators: []validator.String{keySemanticDictionaryValidator{}}},
			"blocked":               schema.BoolAttribute{Description: "Deprecated compatibility field. LiteLLM v1.98 has no persistent organization blocked column; false is accepted as a no-op and true is rejected.", Optional: true, Computed: true, DeprecationMessage: "LiteLLM v1.98 does not persist organization blocked state. Remove this argument; use supported team/project controls instead."},
			"tags":                  schema.ListAttribute{Description: "Deprecated compatibility field. LiteLLM v1.98 has no persistent organization tags column; an empty list is accepted as a no-op and non-empty values are rejected.", Optional: true, Computed: true, ElementType: types.StringType, DeprecationMessage: "LiteLLM v1.98 does not persist organization tags. Remove this argument or store labels in metadata."},
			"created_at":            schema.StringAttribute{Description: "Timestamp when the organization was created.", Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		},
	}
}

func (r *OrganizationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *OrganizationResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if organizationProjectPlanIsDestroy(req) {
		return
	}
	var plan, config OrganizationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if !req.Config.Raw.IsNull() {
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	var state OrganizationResourceModel
	hasState := !req.State.Raw.IsNull()
	importedBudget := false
	if !hasState && !config.BudgetID.IsNull() && organizationBudgetControlsPresentInConfig(&config) {
		resp.Diagnostics.AddAttributeError(path.Root("budget_id"), "Unsafe Shared Organization Budget Controls", "budget_id cannot be combined with organization budget controls during creation because LiteLLM v1.98 ignores or strips those controls for an existing shared budget.")
	}
	if hasState {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if req.Private != nil {
			marker, diagnostics := req.Private.GetKey(ctx, organizationProjectImportedBudgetPrivateKey)
			resp.Diagnostics.Append(diagnostics...)
			importedBudget = string(marker) == "true"
		}
		preserveOrganizationProjectBudgetID(ctx, "Organization", state.BudgetID, config.BudgetID, plan.BudgetID, importedBudget, resp)
		if config.BudgetID.IsNull() {
			plan.BudgetID = state.BudgetID
		}

		raw, diagnostics := req.Private.GetKey(ctx, organizationMetadataJSONProvenancePrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		provenance, err := decodeOrganizationSemanticProvenance(ctx, raw, state.MetadataJSON)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Dictionary Provenance", "Private ownership state is missing, malformed, or inconsistent with public state. No organization plan was produced.")
			return
		}
		if config.MetadataJSON.IsUnknown() {
			plan.MetadataJSON = types.StringUnknown()
		} else {
			prepared, err := prepareOrganizationSemanticDictionary(ctx, config.MetadataJSON, config.Metadata)
			if err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Organization Dictionary", "The JSON object is malformed, overlaps another managed organization metadata surface, or cannot be persisted exactly. No organization plan was produced.")
				return
			}
			changed, err := organizationSemanticNeedsChange(ctx, config.MetadataJSON, state.MetadataJSON, provenance)
			if err != nil {
				resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Organization Dictionary", "The semantic value or private ownership could not be compared safely. No organization plan was produced.")
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
					resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Organization Dictionary", "The legacy metadata projection could not be produced safely. No organization plan was produced.")
					return
				}
				plan.Metadata = filtered
			}
		}
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
	}

	if !config.Blocked.IsNull() && !config.Blocked.IsUnknown() {
		legacyUnchanged := hasState && !state.Blocked.IsNull() && !state.Blocked.IsUnknown() && state.Blocked.Equal(plan.Blocked)
		if config.Blocked.ValueBool() && !legacyUnchanged {
			resp.Diagnostics.AddAttributeError(path.Root("blocked"), "Unsupported Organization Blocked Setting", "LiteLLM v1.98 has no persistent organization blocked field. Setting blocked=true would report false success, so the provider refuses this plan.")
		} else {
			resp.Diagnostics.AddAttributeWarning(path.Root("blocked"), "Deprecated Organization Compatibility Field", "LiteLLM v1.98 does not persist organization blocked state. This value is retained only for compatibility and is never sent to LiteLLM.")
		}
	}
	if !config.Tags.IsNull() && !config.Tags.IsUnknown() {
		legacyUnchanged := hasState && !state.Tags.IsNull() && !state.Tags.IsUnknown() && state.Tags.Equal(plan.Tags)
		if len(config.Tags.Elements()) > 0 && !legacyUnchanged {
			resp.Diagnostics.AddAttributeError(path.Root("tags"), "Unsupported Organization Tags", "LiteLLM v1.98 has no persistent organization tags field. Non-empty tags would report false success, so the provider refuses this plan. Store labels in metadata instead.")
		} else {
			resp.Diagnostics.AddAttributeWarning(path.Root("tags"), "Deprecated Organization Compatibility Field", "LiteLLM v1.98 does not persist organization tags. This value is retained only for compatibility and is never sent to LiteLLM.")
		}
	}
	if hasState && !resp.Diagnostics.HasError() {
		budgetTransition := planImportedOmissionOwnership(ctx, organizationProjectBudgetOwnershipPendingPrivateKey, importedBudget, config.BudgetID, resp)
		forceImportedOwnershipUpdate(ctx, "created_at", budgetTransition && state.BudgetID.Equal(config.BudgetID), resp)
	}
}

func (r *OrganizationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data, config OrganizationResourceModel
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
		resp.Diagnostics.AddError("Unknown Semantic Organization Dictionary", "metadata_json must be known before creating an organization. No request was sent.")
		return
	}
	prepared, err := prepareOrganizationSemanticDictionary(ctx, config.MetadataJSON, config.Metadata)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Organization Dictionary", "The JSON object is malformed, overlaps another managed organization metadata surface, or cannot be persisted exactly. No request was sent.")
		return
	}
	if prepared.provenance.Configured && (!knownString(config.OrganizationID) || config.OrganizationID.ValueString() == "") {
		resp.Diagnostics.AddAttributeError(path.Root("organization_id"), "Explicit Organization Identity Required", "metadata_json requires a known nonempty caller-selected organization_id so an accepted create can be recovered safely. No request was sent.")
		return
	}
	data.MetadataJSON = config.MetadataJSON
	organizationRequest, err := r.buildOrganizationCreateRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Organization Request", "The organization request could not be converted safely. No request was sent.")
		return
	}
	if err := overlayOrganizationCreateSemantic(ctx, organizationRequest, prepared); err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Organization Dictionary", "The complete metadata document could not be composed safely. No request was sent.")
		return
	}
	privateValue, err := encodeOrganizationSemanticProvenance(ctx, prepared.provenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No request was sent.")
		return
	}

	requestedIdentity := config.OrganizationID.ValueString()
	retainAcceptedCreate := func(title, detail string) {
		recoveryCtx := context.WithoutCancel(ctx)
		recovery := partialOrganizationSemanticRecoveryState(data, requestedIdentity)
		unconfigured, encodeErr := encodeOrganizationSemanticProvenance(recoveryCtx, organizationUnconfiguredSemanticProvenance())
		if encodeErr == nil && resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(recoveryCtx, organizationMetadataJSONProvenancePrivateKey, unconfigured)...)
			resp.Diagnostics.Append(resp.Private.SetKey(recoveryCtx, organizationAcceptedCreateRecoveryPrivateKey, []byte("true"))...)
		}
		resp.Diagnostics.Append(resp.State.Set(recoveryCtx, &recovery)...)
		resp.Diagnostics.AddError(title, detail)
	}

	var result map[string]interface{}
	accepted := false
	var createErr error
	if prepared.provenance.Configured {
		accepted, createErr = r.client.doRequestWithResponse(ctx, http.MethodPost, "/organization/new", organizationRequest, &result)
	} else {
		createErr = r.client.DoRequestWithResponse(ctx, http.MethodPost, "/organization/new", organizationRequest, &result)
	}
	if createErr != nil {
		if prepared.provenance.Configured && organizationSemanticCreateRecoveryRequired(accepted, createErr) {
			if accepted {
				retainAcceptedCreate("Organization Creation Not Confirmed", "LiteLLM accepted the organization create, but its response could not be decoded safely. Only the caller-selected identity was retained for authoritative recovery.")
			} else {
				retainAcceptedCreate("Organization Creation Outcome Uncertain", "The organization create was dispatched, but response loss prevented the provider from determining whether it committed. Only the caller-selected identity was retained for authoritative recovery.")
			}
		} else if prepared.provenance.Configured {
			resp.Diagnostics.AddError("Organization Creation Failed", "LiteLLM did not confirm acceptance of the organization create. Response and transport details were omitted.")
		} else {
			resp.Diagnostics.AddError("Organization Creation Failed", "The organization create request failed. Response, identity, URL, and transport details were omitted.")
		}
		return
	}
	if prepared.provenance.Configured {
		if validateOrganizationCreateResponseIdentity(result, requestedIdentity) != nil {
			retainAcceptedCreate("Organization Creation Identity Not Confirmed", "LiteLLM accepted the organization create, but the response did not confirm the caller-selected identity. Only that identity was retained for authoritative recovery.")
			return
		}
		data.OrganizationID = types.StringValue(requestedIdentity)
		data.ID = types.StringValue(requestedIdentity)
	} else {
		object, unwrapErr := unwrapObjectEnvelope(result, "organization_info", "data")
		if unwrapErr != nil {
			resp.Diagnostics.AddError("Invalid API Response", "LiteLLM returned a malformed organization create response. Response and identity details were omitted.")
			return
		}
		organizationID, ok := object["organization_id"].(string)
		if !ok || organizationID == "" {
			resp.Diagnostics.AddError("Invalid API Response", "Organization create response did not contain a nonempty organization_id.")
			return
		}
		data.OrganizationID = types.StringValue(organizationID)
		data.ID = types.StringValue(organizationID)
	}

	organizationID := data.OrganizationID.ValueString()
	if knownString(data.BudgetDuration) {
		var resetResult map[string]interface{}
		endpoint := endpointWithPathSegment("/v2/organization/", organizationID, "")
		if err := r.client.DoRequestWithResponse(ctx, http.MethodPatch, endpoint, map[string]interface{}{"budget_duration": data.BudgetDuration.ValueString()}, &resetResult); err != nil {
			if prepared.provenance.Configured {
				retainAcceptedCreate("Budget Reset Initialization Not Confirmed", "LiteLLM accepted the organization create, but reset initialization could not be confirmed. Only the caller-selected identity was retained for recovery.")
			} else {
				resp.Diagnostics.AddError("Budget Reset Initialization Error", "The organization was created, but budget reset initialization failed. Response, identity, URL, and transport details were omitted.")
				resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			}
			return
		}
		resetObject, unwrapErr := unwrapObjectEnvelope(resetResult, "organization_info", "data")
		if unwrapErr != nil || validateImportedObjectIdentity(true, "organization reset initialization", resetObject, "organization_id", organizationID) != nil {
			if prepared.provenance.Configured {
				retainAcceptedCreate("Budget Reset Initialization Not Confirmed", "LiteLLM accepted the organization create, but reset initialization did not confirm the same identity. Only the caller-selected identity was retained for recovery.")
			} else {
				resp.Diagnostics.AddError("Budget Reset Initialization Error", "Organization was created and LiteLLM accepted reset initialization, but the response did not confirm the matching organization identity.")
				resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			}
			return
		}
	}
	ownership := organizationSemanticOwnership{provenance: prepared.provenance, fresh: prepared.provenance.Configured, confirmCurrentValue: prepared.provenance.Configured}
	if err := r.readOrganizationWithOwnership(ctx, &data, false, ownership); err != nil {
		if prepared.provenance.Configured {
			retainAcceptedCreate("Semantic Organization Dictionary Not Confirmed", "LiteLLM accepted the organization create, but one authoritative identity-bound read did not confirm its metadata. Only the caller-selected identity was retained for recovery.")
		} else {
			resp.Diagnostics.AddError("Organization Create Readback Failed", "The organization was created, but authoritative readback failed. Response, identity, URL, and transport details were omitted.")
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		}
		return
	}
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationMetadataJSONProvenancePrivateKey, privateValue)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationAcceptedCreateRecoveryPrivateKey, nil)...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *OrganizationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OrganizationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	var importMarker, provenanceRaw, acceptedRaw, pendingRaw []byte
	var importDiagnostics, provenanceDiagnostics, acceptedDiagnostics, pendingDiagnostics diag.Diagnostics
	if req.Private != nil {
		importMarker, importDiagnostics = req.Private.GetKey(ctx, numericImportedPrivateKey)
		provenanceRaw, provenanceDiagnostics = req.Private.GetKey(ctx, organizationMetadataJSONProvenancePrivateKey)
		acceptedRaw, acceptedDiagnostics = req.Private.GetKey(ctx, organizationAcceptedCreateRecoveryPrivateKey)
		pendingRaw, pendingDiagnostics = req.Private.GetKey(ctx, organizationPendingUpdatePrivateKey)
	}
	resp.Diagnostics.Append(importDiagnostics...)
	resp.Diagnostics.Append(provenanceDiagnostics...)
	resp.Diagnostics.Append(acceptedDiagnostics...)
	resp.Diagnostics.Append(pendingDiagnostics...)
	if len(acceptedRaw) != 0 && string(acceptedRaw) != "true" {
		resp.Diagnostics.AddError("Invalid Organization Recovery State", "Accepted-create recovery state is malformed. No organization read was performed.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	provenance, err := decodeOrganizationSemanticProvenance(ctx, provenanceRaw, data.MetadataJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No organization read was performed.")
		return
	}
	pending, err := decodeKeySemanticPendingTransition(ctx, pendingRaw)
	if err != nil || pending.Config.Active || pending.Permissions.Active {
		resp.Diagnostics.AddError("Invalid Organization Recovery State", "Pending semantic-update recovery state is malformed. No organization read was performed.")
		return
	}
	reconcile := keySemanticPendingReconcile{}
	ownership := organizationSemanticOwnership{
		provenance: provenance, pending: pending, reconcile: &reconcile,
		acceptedCreate: string(acceptedRaw) == "true", fresh: len(acceptedRaw) != 0 || pending.any(),
	}
	imported := string(importMarker) == "true"
	if err := r.readOrganizationWithOwnership(ctx, &data, imported, ownership); err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Organization Read Failed", "The authoritative organization response could not be validated or projected safely. Response, identity, metadata, and transport details were omitted.")
		return
	}
	if reconcile.Present && reconcile.Committed {
		provenance = reconcile.Effective.metadata
	}
	encoded, err := encodeOrganizationSemanticProvenance(ctx, provenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No organization state was produced.")
		return
	}
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationMetadataJSONProvenancePrivateKey, encoded)...)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() {
		if imported {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
		}
		if string(acceptedRaw) == "true" {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationAcceptedCreateRecoveryPrivateKey, nil)...)
		}
		if reconcile.Present {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationPendingUpdatePrivateKey, nil)...)
		}
	}
}

func (r *OrganizationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config OrganizationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if req.Config.Raw.Type() == nil {
		config = plan
	} else {
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	}
	var acceptedRaw, pendingRaw []byte
	var acceptedDiagnostics, pendingDiagnostics diag.Diagnostics
	if req.Private != nil {
		acceptedRaw, acceptedDiagnostics = req.Private.GetKey(ctx, organizationAcceptedCreateRecoveryPrivateKey)
		pendingRaw, pendingDiagnostics = req.Private.GetKey(ctx, organizationPendingUpdatePrivateKey)
	}
	resp.Diagnostics.Append(acceptedDiagnostics...)
	resp.Diagnostics.Append(pendingDiagnostics...)
	if len(pendingRaw) != 0 {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Organization Recovery Required", "A prior semantic metadata update has not been reconciled. Refresh must determine whether its shape transition committed before another update can be sent.")
		return
	}
	if string(acceptedRaw) == "true" {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Organization Recovery Required", "A prior organization create was accepted without complete readback. Refresh must reconcile its caller-selected identity before another update can be sent.")
		return
	}
	if len(acceptedRaw) != 0 {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Invalid Organization Recovery State", "Accepted-create recovery state is malformed. No organization update was sent.")
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if config.MetadataJSON.IsUnknown() {
		resp.Diagnostics.AddError("Unknown Semantic Organization Dictionary", "metadata_json must be known before updating an organization. No request was sent.")
		return
	}
	var provenanceRaw []byte
	var provenanceDiagnostics diag.Diagnostics
	if req.Private != nil {
		provenanceRaw, provenanceDiagnostics = req.Private.GetKey(ctx, organizationMetadataJSONProvenancePrivateKey)
	}
	resp.Diagnostics.Append(provenanceDiagnostics...)
	priorProvenance, err := decodeOrganizationSemanticProvenance(ctx, provenanceRaw, state.MetadataJSON)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Dictionary Provenance", "Private ownership is missing, malformed, or inconsistent with public state. No organization update was sent.")
		return
	}
	prepared, err := prepareOrganizationSemanticDictionary(ctx, config.MetadataJSON, config.Metadata)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Organization Dictionary", "The JSON object is malformed, overlaps another managed organization metadata surface, or cannot be persisted exactly. No request was sent.")
		return
	}
	semanticChanged, err := organizationSemanticNeedsChange(ctx, config.MetadataJSON, state.MetadataJSON, priorProvenance)
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("metadata_json"), "Invalid Semantic Organization Dictionary", "The semantic value or private ownership could not be compared safely. No request was sent.")
		return
	}
	confirmationOwnership, err := prepared.updateOwnership(ctx, priorProvenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic shape-transition ownership could not be validated safely. No request was sent.")
		return
	}
	pendingTransition := pendingOrganizationSemanticTransition(confirmationOwnership)
	var pendingPrivate []byte
	if pendingTransition.any() {
		pendingPrivate, err = encodeKeySemanticPendingTransition(ctx, pendingTransition)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Pending semantic shape ownership could not be encoded safely. No request was sent.")
			return
		}
	}
	newProvenanceRaw, err := encodeOrganizationSemanticProvenance(ctx, prepared.provenance)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Semantic ownership could not be encoded safely. No request was sent.")
		return
	}

	plan.ID, plan.OrganizationID = state.ID, state.OrganizationID
	plan.MetadataJSON = config.MetadataJSON
	if state.BudgetID.IsUnknown() || plan.BudgetID.IsUnknown() || !state.BudgetID.Equal(plan.BudgetID) {
		resp.Diagnostics.AddError("Unsafe Organization Budget Reassociation", "The organization budget association changed or remained unknown despite the plan safety check; no API call was made.")
		return
	}
	if !plan.Blocked.IsNull() && !plan.Blocked.IsUnknown() && plan.Blocked.ValueBool() && !plan.Blocked.Equal(state.Blocked) {
		resp.Diagnostics.AddError("Unsupported Organization Blocked Setting", "blocked=true is not persisted by LiteLLM v1.98; no API call was made.")
		return
	}
	if !plan.Tags.IsNull() && !plan.Tags.IsUnknown() && len(plan.Tags.Elements()) > 0 && !plan.Tags.Equal(state.Tags) {
		resp.Diagnostics.AddError("Unsupported Organization Tags", "Non-empty organization tags are not persisted by LiteLLM v1.98; no API call was made.")
		return
	}
	updateRequest, err := buildOrganizationUpdateRequest(ctx, &plan, &state)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Organization Request", "The organization update could not be converted safely. No request was sent.")
		return
	}
	legacyChanged := !plan.Metadata.IsUnknown() && !plan.Metadata.Equal(state.Metadata)
	rpmChanged := !plan.ModelRPMLimit.IsUnknown() && !plan.ModelRPMLimit.Equal(state.ModelRPMLimit)
	tpmChanged := !plan.ModelTPMLimit.IsUnknown() && !plan.ModelTPMLimit.Equal(state.ModelTPMLimit)
	metadataChanged := semanticChanged || legacyChanged || rpmChanged || tpmChanged
	delete(updateRequest, "metadata")

	var hydrated map[string]interface{}
	if metadataChanged {
		hydrated, err = r.getFreshExactOrganizationInfo(ctx, state.OrganizationID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Organization Metadata Hydration Failed", "The complete identity-bound metadata document could not be read safely. No update request was sent.")
			return
		}
		remoteMetadata, err := organizationMetadataObject(ctx, hydrated)
		if err != nil {
			resp.Diagnostics.AddError("Organization Metadata Hydration Failed", "The complete metadata document was malformed or not persistable exactly. No update request was sent.")
			return
		}
		replacement, err := composeOrganizationMetadataReplacement(ctx, remoteMetadata, plan, state, priorProvenance, prepared)
		if err != nil {
			resp.Diagnostics.AddError("Organization Metadata Composition Failed", "The complete metadata replacement could not be composed safely. No update request was sent.")
			return
		}
		updateRequest["metadata"] = replacement
	}
	if organizationUpdateChangesBudget(updateRequest) {
		if hydrated != nil {
			if err := validateOrganizationBudgetFromInfo(hydrated, state.BudgetID); err != nil {
				resp.Diagnostics.AddError("Organization Budget Lookup Error", "The authoritative budget association could not be validated. No update request was sent.")
				return
			}
		} else if _, err := r.lookupOrganizationBudgetID(ctx, state.OrganizationID.ValueString(), state.BudgetID); err != nil {
			resp.Diagnostics.AddError("Organization Budget Lookup Error", "The authoritative budget association could not be validated. No update request was sent; response and identity details were omitted.")
			return
		}
	}

	retainPrior := func(localCtx context.Context) {
		if len(pendingPrivate) != 0 && resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(localCtx, organizationPendingUpdatePrivateKey, pendingPrivate)...)
		}
		resp.Diagnostics.Append(resp.State.Set(localCtx, &state)...)
	}
	if len(updateRequest) > 0 {
		var result map[string]interface{}
		endpoint := endpointWithPathSegment("/v2/organization/", state.OrganizationID.ValueString(), "")
		if err := r.client.DoRequestWithResponse(ctx, http.MethodPatch, endpoint, updateRequest, &result); err != nil {
			if metadataChanged {
				retainPrior(context.WithoutCancel(ctx))
				resp.Diagnostics.AddError("Organization Update Not Confirmed", "The metadata-bearing update may have been dispatched, but its outcome was not confirmed. Prior public and private state were retained.")
			} else {
				resp.Diagnostics.AddError("Organization Update Failed", "The organization update failed. Response, identity, URL, and transport details were omitted.")
			}
			return
		}
		object, unwrapErr := unwrapObjectEnvelope(result, "organization_info", "data")
		if unwrapErr != nil || validateImportedObjectIdentity(true, "organization update", object, "organization_id", state.OrganizationID.ValueString()) != nil {
			if metadataChanged {
				retainPrior(context.WithoutCancel(ctx))
				resp.Diagnostics.AddError("Organization Update Not Confirmed", "LiteLLM accepted the metadata-bearing update, but its response did not confirm the same identity. Prior public and private state were retained.")
			} else {
				resp.Diagnostics.AddError("Invalid API Response", "LiteLLM did not return the matching organization identity.")
			}
			return
		}
	}
	desired := plan
	seedOrganizationClearOwnership(&plan, &state)
	readOwnership := organizationSemanticOwnership{provenance: priorProvenance}
	if metadataChanged {
		readOwnership = confirmationOwnership
	}
	if err := r.readOrganizationWithOwnership(ctx, &plan, false, readOwnership); err != nil {
		if metadataChanged {
			retainPrior(context.WithoutCancel(ctx))
			resp.Diagnostics.AddError("Organization Metadata Update Not Confirmed", "LiteLLM accepted the update, but one authoritative identity-bound read did not confirm the complete metadata transition. Prior public and private state were retained.")
		} else {
			resp.Diagnostics.AddError("Organization Update Readback Failed", "The organization update was accepted, but authoritative readback failed. Prior state was retained; response, identity, URL, and transport details were omitted.")
		}
		return
	}
	if field, ok := organizationChangedFieldMismatch(&desired, &state, &plan); ok {
		if metadataChanged {
			retainPrior(context.WithoutCancel(ctx))
		}
		resp.Diagnostics.AddError("Organization Update Did Not Converge", fmt.Sprintf("LiteLLM accepted the update but authoritative read-back did not match planned %s; prior Terraform state was retained.", field))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	if !resp.Diagnostics.HasError() && resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationMetadataJSONProvenancePrivateKey, newProvenanceRaw)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationPendingUpdatePrivateKey, nil)...)
		pendingBudget, diagnostics := resp.Private.GetKey(ctx, organizationProjectBudgetOwnershipPendingPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		if !resp.Diagnostics.HasError() && string(pendingBudget) == "true" {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationProjectImportedBudgetPrivateKey, nil)...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationProjectBudgetOwnershipPendingPrivateKey, nil)...)
		}
	}
}

func (r *OrganizationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OrganizationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	var acceptedRaw, pendingRaw []byte
	var acceptedDiagnostics, pendingDiagnostics diag.Diagnostics
	if req.Private != nil {
		acceptedRaw, acceptedDiagnostics = req.Private.GetKey(ctx, organizationAcceptedCreateRecoveryPrivateKey)
		pendingRaw, pendingDiagnostics = req.Private.GetKey(ctx, organizationPendingUpdatePrivateKey)
	}
	resp.Diagnostics.Append(acceptedDiagnostics...)
	resp.Diagnostics.Append(pendingDiagnostics...)
	if len(pendingRaw) != 0 {
		resp.Diagnostics.AddError("Organization Recovery Required", "A prior semantic metadata update has not been reconciled. Refresh must reconcile it before deletion can be sent.")
		return
	}
	if string(acceptedRaw) == "true" {
		resp.Diagnostics.AddError("Organization Recovery Required", "A prior organization create was accepted without complete readback. Refresh must reconcile it before deletion can be sent.")
		return
	}
	if len(acceptedRaw) != 0 {
		resp.Diagnostics.AddError("Invalid Organization Recovery State", "Accepted-create recovery state is malformed. No organization deletion was sent.")
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DoRequestWithResponse(ctx, http.MethodDelete, "/organization/delete", map[string]interface{}{"organization_ids": []string{data.OrganizationID.ValueString()}}, nil); err != nil && !IsNotFoundError(err) {
		resp.Diagnostics.AddError("Organization Delete Failed", "The organization deletion failed. Response, identity, URL, and transport details were omitted.")
	}
}

func (r *OrganizationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	provenance, err := encodeOrganizationSemanticProvenance(ctx, organizationUnconfiguredSemanticProvenance())
	if err != nil {
		resp.Diagnostics.AddError("Invalid Semantic Dictionary Provenance", "Import ownership could not be initialized safely.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("metadata_json"), types.StringNull())...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationMetadataJSONProvenancePrivateKey, provenance)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationAcceptedCreateRecoveryPrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationPendingUpdatePrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, organizationProjectImportedBudgetPrivateKey, []byte("true"))...)
	}
}

// UpgradeState performs the direct v0-to-v1 migration. It adds only a typed
// JSON null and never adopts metadata returned by LiteLLM.
func (r *OrganizationResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: nil,
			StateUpgrader: func(_ context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				if req.RawState == nil {
					resp.Diagnostics.AddError("Unable to Upgrade State", "Prior organization state is unavailable.")
					return
				}
				upgraded, err := marshalOrganizationUpgrade(req.RawState.JSON)
				if err != nil {
					resp.Diagnostics.AddError("Unable to Upgrade State", "Prior organization state could not be decoded safely.")
					return
				}
				resp.DynamicValue = &tfprotov6.DynamicValue{JSON: upgraded}
			},
		},
	}
}

// buildOrganizationRequest is retained as the create-payload test seam used by
// earlier releases. Updates use the differential v2 builder below.
func (r *OrganizationResource) buildOrganizationRequest(ctx context.Context, data *OrganizationResourceModel) (map[string]interface{}, error) {
	return r.buildOrganizationCreateRequest(ctx, data)
}

func (r *OrganizationResource) buildOrganizationCreateRequest(ctx context.Context, data *OrganizationResourceModel) (map[string]interface{}, error) {
	if knownString(data.BudgetID) && organizationBudgetControlsConfigured(data) {
		return nil, fmt.Errorf("budget_id cannot be combined with organization budget controls during creation because LiteLLM v1.98 ignores or strips those controls for an existing shared budget")
	}
	if !data.Blocked.IsNull() && !data.Blocked.IsUnknown() && data.Blocked.ValueBool() {
		return nil, fmt.Errorf("blocked=true is unsupported because LiteLLM v1.98 has no persistent organization blocked field")
	}
	if !data.Tags.IsNull() && !data.Tags.IsUnknown() && len(data.Tags.Elements()) > 0 {
		return nil, fmt.Errorf("non-empty tags are unsupported because LiteLLM v1.98 has no persistent organization tags field")
	}
	request := map[string]interface{}{"organization_alias": data.OrganizationAlias.ValueString()}
	if knownString(data.OrganizationID) {
		request["organization_id"] = data.OrganizationID.ValueString()
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
	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		models, err := stringListRequest(ctx, data.Models, "models")
		if err != nil {
			return nil, err
		}
		request["models"] = models
	}
	metadata, err := organizationMetadataPayload(ctx, data)
	if err != nil {
		return nil, err
	}
	if len(metadata) > 0 {
		request["metadata"] = metadata
	}
	return request, nil
}

func buildOrganizationUpdateRequest(ctx context.Context, plan, state *OrganizationResourceModel) (map[string]interface{}, error) {
	request := map[string]interface{}{}
	if !plan.OrganizationAlias.IsUnknown() && !plan.OrganizationAlias.Equal(state.OrganizationAlias) {
		request["organization_alias"] = plan.OrganizationAlias.ValueString()
	}
	if !plan.Models.IsUnknown() && !plan.Models.Equal(state.Models) {
		if plan.Models.IsNull() {
			return nil, fmt.Errorf("models cannot be cleared with null on LiteLLM v1.98; configure an empty list instead")
		}
		models, err := stringListRequest(ctx, plan.Models, "models")
		if err != nil {
			return nil, err
		}
		request["models"] = models
	}
	addChangedFloat(request, "max_budget", plan.MaxBudget, state.MaxBudget)
	addChangedFloat(request, "soft_budget", plan.SoftBudget, state.SoftBudget)
	addChangedInt(request, "tpm_limit", plan.TPMLimit, state.TPMLimit)
	addChangedInt(request, "rpm_limit", plan.RPMLimit, state.RPMLimit)
	addChangedInt(request, "max_parallel_requests", plan.MaxParallelRequests, state.MaxParallelRequests)
	if !plan.BudgetDuration.IsUnknown() && !plan.BudgetDuration.Equal(state.BudgetDuration) {
		if plan.BudgetDuration.IsNull() {
			request["budget_duration"] = nil
		} else {
			request["budget_duration"] = plan.BudgetDuration.ValueString()
		}
	}
	metadataChanged := (!plan.Metadata.IsUnknown() && !plan.Metadata.Equal(state.Metadata)) || (!plan.ModelRPMLimit.IsUnknown() && !plan.ModelRPMLimit.Equal(state.ModelRPMLimit)) || (!plan.ModelTPMLimit.IsUnknown() && !plan.ModelTPMLimit.Equal(state.ModelTPMLimit))
	if metadataChanged {
		metadata, err := organizationMetadataPayload(ctx, plan)
		if err != nil {
			return nil, err
		}
		request["metadata"] = metadata
	}
	return request, nil
}

func organizationBudgetControlsConfigured(data *OrganizationResourceModel) bool {
	return knownFloat(data.MaxBudget) || knownFloat(data.SoftBudget) || knownString(data.BudgetDuration) ||
		knownInt(data.TPMLimit) || knownInt(data.RPMLimit) || knownInt(data.MaxParallelRequests)
}

func organizationBudgetControlsPresentInConfig(data *OrganizationResourceModel) bool {
	return !data.MaxBudget.IsNull() || !data.SoftBudget.IsNull() || !data.BudgetDuration.IsNull() ||
		!data.TPMLimit.IsNull() || !data.RPMLimit.IsNull() || !data.MaxParallelRequests.IsNull()
}

func organizationUpdateChangesBudget(request map[string]interface{}) bool {
	for _, field := range []string{"max_budget", "soft_budget", "budget_duration", "tpm_limit", "rpm_limit", "max_parallel_requests"} {
		if _, changed := request[field]; changed {
			return true
		}
	}
	return false
}

func (r *OrganizationResource) lookupOrganizationBudgetID(ctx context.Context, organizationID string, configured types.String) (string, error) {
	var result map[string]interface{}
	query := url.Values{"organization_id": []string{organizationID}}
	endpoint := endpointWithQuery("/organization/info", query)
	if err := r.client.DoRequestWithResponse(ctx, "GET", endpoint, nil, &result); err != nil {
		return "", fmt.Errorf("unable to read authoritative organization budget: %w", err)
	}
	object, err := unwrapObjectEnvelope(result, "organization_info", "data")
	if err != nil {
		return "", err
	}
	if err := validateImportedObjectIdentity(true, "organization budget lookup", object, "organization_id", organizationID); err != nil {
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
		return "", fmt.Errorf("organization %q has no authoritative budget identity", organizationID)
	}
	if knownString(configured) && configured.ValueString() != budgetID {
		return "", fmt.Errorf("organization budget reassociation detected: state budget_id %q, API budget_id %q", configured.ValueString(), budgetID)
	}
	return budgetID, nil
}

func (r *OrganizationResource) getFreshExactOrganizationInfo(ctx context.Context, organizationID string) (map[string]interface{}, error) {
	if organizationID == "" {
		return nil, errSemanticDictionaryTraversal
	}
	var result map[string]interface{}
	query := url.Values{"organization_id": []string{organizationID}}
	endpoint := endpointWithQuery("/organization/info", query)
	if err := r.client.doFreshRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
		return nil, err
	}
	object, err := unwrapObjectEnvelope(result, "organization_info", "data")
	if err != nil || validateImportedObjectIdentity(true, "organization", object, "organization_id", organizationID) != nil {
		return nil, errSemanticDictionaryTraversal
	}
	return object, nil
}

func validateOrganizationBudgetFromInfo(object map[string]interface{}, configured types.String) error {
	table, err := parseBudgetTable(object)
	if err != nil {
		return err
	}
	budgetID, presence, err := budgetTableID(object, table)
	if err != nil || presence != apiValuePresent {
		return errSemanticDictionaryTraversal
	}
	if knownString(configured) && configured.ValueString() != budgetID {
		return errSemanticDictionaryTraversal
	}
	return nil
}

func organizationMetadataPayload(ctx context.Context, data *OrganizationResourceModel) (map[string]interface{}, error) {
	metadata := map[string]interface{}{}
	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var values map[string]string
		if diagnostics := data.Metadata.ElementsAs(ctx, &values, false); diagnostics.HasError() {
			return nil, fmt.Errorf("metadata contains a value that cannot be represented as a string")
		}
		metadata = convertMetadataToNative(values)
		delete(metadata, "model_rpm_limit")
		delete(metadata, "model_tpm_limit")
	}
	if !data.ModelRPMLimit.IsNull() && !data.ModelRPMLimit.IsUnknown() {
		values, err := int64RequestMap(data.ModelRPMLimit, "model_rpm_limit")
		if err != nil {
			return nil, err
		}
		metadata["model_rpm_limit"] = values
	}
	if !data.ModelTPMLimit.IsNull() && !data.ModelTPMLimit.IsUnknown() {
		values, err := int64RequestMap(data.ModelTPMLimit, "model_tpm_limit")
		if err != nil {
			return nil, err
		}
		metadata["model_tpm_limit"] = values
	}
	return metadata, nil
}

func (r *OrganizationResource) readOrganization(ctx context.Context, data *OrganizationResourceModel) error {
	return r.readOrganizationWithOwnership(ctx, data, false, organizationSemanticOwnership{provenance: organizationUnconfiguredSemanticProvenance()})
}

func (r *OrganizationResource) readOrganizationWithNumericOwnership(ctx context.Context, data *OrganizationResourceModel, imported bool) error {
	return r.readOrganizationWithOwnership(ctx, data, imported, organizationSemanticOwnership{provenance: organizationUnconfiguredSemanticProvenance()})
}

func (r *OrganizationResource) readOrganizationWithOwnership(ctx context.Context, data *OrganizationResourceModel, imported bool, ownership organizationSemanticOwnership) error {
	organizationID := data.OrganizationID.ValueString()
	if organizationID == "" {
		organizationID = data.ID.ValueString()
	}
	if organizationID == "" {
		return errSemanticDictionaryTraversal
	}
	var object map[string]interface{}
	var err error
	if ownership.fresh {
		object, err = r.getFreshExactOrganizationInfo(ctx, organizationID)
	} else {
		var result map[string]interface{}
		query := url.Values{"organization_id": []string{organizationID}}
		endpoint := endpointWithQuery("/organization/info", query)
		if err = r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &result); err == nil {
			object, err = unwrapObjectEnvelope(result, "organization_info", "data")
			if err == nil {
				err = validateImportedObjectIdentity(true, "organization", object, "organization_id", organizationID)
			}
		}
	}
	if err != nil {
		return err
	}
	metadataObject, err := organizationMetadataObject(ctx, object)
	if err != nil {
		return err
	}
	if ownership.pending.any() {
		var reconcile keySemanticPendingReconcile
		ownership, reconcile, err = resolveOrganizationSemanticPending(ctx, metadataObject, ownership)
		if err != nil {
			return errSemanticDictionaryTraversal
		}
		if ownership.reconcile != nil {
			*ownership.reconcile = reconcile
		}
	}
	if err := requireImportedStringField(imported, "organization", object, "organization_alias"); err != nil {
		return err
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
	budgetOwned := imported || knownString(data.BudgetID)
	if budgetPresence == apiValuePresent && budgetOwned {
		if !imported && knownString(data.BudgetID) && data.BudgetID.ValueString() != remoteBudgetID {
			return fmt.Errorf("organization budget reassociation detected: state budget_id %q, API budget_id %q", data.BudgetID.ValueString(), remoteBudgetID)
		}
		data.BudgetID = types.StringValue(remoteBudgetID)
	} else if budgetOwned && (budgetPresence == apiValueNull || budgetPresence == apiValueAbsent) {
		data.BudgetID = types.StringNull()
	} else if !budgetOwned && data.BudgetID.IsUnknown() {
		// Optional+Computed is required for import adoption, but ordinary creates
		// with omitted budget_id must not adopt LiteLLM's generated default.
		data.BudgetID = types.StringNull()
	}

	data.ID = types.StringValue(organizationID)
	data.OrganizationID = types.StringValue(organizationID)
	if value, ok := object["organization_alias"].(string); ok {
		data.OrganizationAlias = types.StringValue(value)
	}
	if value, ok := object["created_at"].(string); ok {
		data.CreatedAt = types.StringValue(value)
	} else if data.CreatedAt.IsUnknown() {
		data.CreatedAt = types.StringNull()
	}
	models, modelsPresence, diagnostics := strictAPIStringList(ctx, object, "models", path.Root("models"))
	if err := collectionProjectionError(ctx, diagnostics); err != nil {
		return err
	}
	if modelsPresence == apiValuePresent {
		data.Models = models
	} else if data.Models.IsUnknown() {
		data.Models = types.ListNull(types.StringType)
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
	nextMetadata, err := projectOrganizationLegacyMetadata(ctx, data.Metadata, metadataObject, ownership)
	if err != nil {
		return err
	}
	nextJSON, err := projectOrganizationSemanticMetadata(ctx, data.MetadataJSON, metadataObject, ownership)
	if err != nil {
		return err
	}
	nextRPM, nextTPM := data.ModelRPMLimit, data.ModelTPMLimit
	rpmOwned := imported || knownMap(data.ModelRPMLimit)
	if err := updateInt64MapFromAPI(&nextRPM, object, rpmOwned, rpmOwned, "metadata", "model_rpm_limit"); err != nil {
		return err
	}
	tpmOwned := imported || knownMap(data.ModelTPMLimit)
	if err := updateInt64MapFromAPI(&nextTPM, object, tpmOwned, tpmOwned, "metadata", "model_tpm_limit"); err != nil {
		return err
	}
	data.Metadata, data.MetadataJSON, data.ModelRPMLimit, data.ModelTPMLimit = nextMetadata, nextJSON, nextRPM, nextTPM

	// These fields do not exist in the v1.98 organization request/table/response
	// contracts. Ignore equally named API extras rather than adopting phantoms.
	if data.Blocked.IsUnknown() || (imported && data.Blocked.IsNull()) {
		data.Blocked = types.BoolValue(false)
	}
	if data.Tags.IsUnknown() || (imported && data.Tags.IsNull()) {
		empty, diagnostics := checkedStringListValue(ctx, nil, path.Root("tags"))
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		data.Tags = empty
	}
	*original = *data
	return nil
}

func seedOrganizationClearOwnership(target, prior *OrganizationResourceModel) {
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
	if target.ModelRPMLimit.IsNull() && knownMap(prior.ModelRPMLimit) {
		target.ModelRPMLimit = prior.ModelRPMLimit
	}
	if target.ModelTPMLimit.IsNull() && knownMap(prior.ModelTPMLimit) {
		target.ModelTPMLimit = prior.ModelTPMLimit
	}
}

func organizationChangedFieldMismatch(desired, prior, actual *OrganizationResourceModel) (string, bool) {
	for _, field := range []struct {
		name                   string
		desired, prior, actual attr.Value
	}{
		{"organization_alias", desired.OrganizationAlias, prior.OrganizationAlias, actual.OrganizationAlias},
		{"models", desired.Models, prior.Models, actual.Models},
		{"max_budget", desired.MaxBudget, prior.MaxBudget, actual.MaxBudget}, {"soft_budget", desired.SoftBudget, prior.SoftBudget, actual.SoftBudget},
		{"tpm_limit", desired.TPMLimit, prior.TPMLimit, actual.TPMLimit}, {"rpm_limit", desired.RPMLimit, prior.RPMLimit, actual.RPMLimit},
		{"max_parallel_requests", desired.MaxParallelRequests, prior.MaxParallelRequests, actual.MaxParallelRequests},
		{"budget_duration", desired.BudgetDuration, prior.BudgetDuration, actual.BudgetDuration},
		{"metadata", desired.Metadata, prior.Metadata, actual.Metadata}, {"metadata_json", desired.MetadataJSON, prior.MetadataJSON, actual.MetadataJSON}, {"model_rpm_limit", desired.ModelRPMLimit, prior.ModelRPMLimit, actual.ModelRPMLimit}, {"model_tpm_limit", desired.ModelTPMLimit, prior.ModelTPMLimit, actual.ModelTPMLimit},
	} {
		if !field.desired.IsUnknown() && !field.desired.Equal(field.prior) && !field.desired.Equal(field.actual) {
			return field.name, true
		}
	}
	return "", false
}

func knownString(value types.String) bool { return !value.IsNull() && !value.IsUnknown() }
func knownFloat(value types.Float64) bool { return !value.IsNull() && !value.IsUnknown() }
func knownInt(value types.Int64) bool     { return !value.IsNull() && !value.IsUnknown() }
func knownMap(value types.Map) bool       { return !value.IsNull() && !value.IsUnknown() }

func addKnownFloat(request map[string]interface{}, field string, value types.Float64) {
	if knownFloat(value) {
		request[field] = value.ValueFloat64()
	}
}
func addKnownInt(request map[string]interface{}, field string, value types.Int64) {
	if knownInt(value) {
		request[field] = value.ValueInt64()
	}
}
func addChangedFloat(request map[string]interface{}, field string, plan, state types.Float64) {
	if plan.IsUnknown() || plan.Equal(state) {
		return
	}
	if plan.IsNull() {
		request[field] = nil
	} else {
		request[field] = plan.ValueFloat64()
	}
}
func addChangedInt(request map[string]interface{}, field string, plan, state types.Int64) {
	if plan.IsUnknown() || plan.Equal(state) {
		return
	}
	if plan.IsNull() {
		request[field] = nil
	} else {
		request[field] = plan.ValueInt64()
	}
}
func stringListRequest(ctx context.Context, value types.List, field string) ([]string, error) {
	var result []string
	if diagnostics := value.ElementsAs(ctx, &result, false); diagnostics.HasError() {
		return nil, fmt.Errorf("%s contains a value that is not a known string", field)
	}
	return result, nil
}
