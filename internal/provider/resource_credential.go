package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const credentialPrivateMetadataKey = "credential_ownership_v1"

const (
	credentialPostflightAttempts = 4
	credentialReadAttempts       = 8
)

var _ resource.Resource = &CredentialResource{}
var _ resource.ResourceWithImportState = &CredentialResource{}
var _ resource.ResourceWithModifyPlan = &CredentialResource{}

func NewCredentialResource() resource.Resource {
	return &CredentialResource{}
}

type CredentialResource struct {
	client *Client
}

type CredentialResourceModel struct {
	ID                     types.String `tfsdk:"id"`
	CredentialName         types.String `tfsdk:"credential_name"`
	ModelID                types.String `tfsdk:"model_id"`
	CredentialInfo         types.Map    `tfsdk:"credential_info"`
	CredentialValues       types.Map    `tfsdk:"credential_values"`
	CredentialInfoJSON     types.String `tfsdk:"credential_info_json"`
	CredentialValuesJSON   types.String `tfsdk:"credential_values_json"`
	CredentialValuesActive types.Bool   `tfsdk:"credential_values_active"`
	CredentialSource       types.String `tfsdk:"credential_source"`
}

type credentialAPIResponse struct {
	CredentialName   string          `json:"credential_name"`
	CredentialInfo   json.RawMessage `json:"credential_info"`
	CredentialValues json.RawMessage `json:"credential_values"`
}

type credentialRemote struct {
	name   string
	info   map[string]interface{}
	values map[string]interface{}
}

type credentialMutationResponse struct {
	Success *bool  `json:"success"`
	Message string `json:"message"`
}

type credentialPrivateReader interface {
	GetKey(context.Context, string) ([]byte, diag.Diagnostics)
}

type credentialPrivateWriter interface {
	SetKey(context.Context, string, []byte) diag.Diagnostics
}

func (r *CredentialResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential"
}

func (r *CredentialResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	jsonValidators := []validator.String{credentialJSONObjectValidator{}}
	jsonPlanModifiers := []planmodifier.String{canonicalCredentialJSONPlanModifier{}}
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM credential with recursive selective ownership and additive heterogeneous JSON surfaces.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "The unique identifier for this credential (same as credential_name).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"credential_name": schema.StringAttribute{
				Description: "Non-empty credential name. Empty names are rejected because LiteLLM can create them but cannot address them safely for refresh or deletion.",
				Required:    true,
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"model_id": schema.StringAttribute{
				Description: "Create-only model deployment ID. LiteLLM uses it in preference to configured credential values. Any change, including an unknown planned value, requires replacement.",
				Optional:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"credential_info": schema.MapAttribute{
				Description: "Legacy string-only credential metadata. Its public map(string), Optional, and Computed contract is preserved. Use credential_info_json for heterogeneous values.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
			},
			"credential_values": schema.MapAttribute{
				Description: "Legacy string-only sensitive credential values. Its map(string) type and state representation are preserved while the argument is now optional so model-only configuration and metadata-only import are representable. Use credential_values_json for heterogeneous values.",
				Optional:    true,
				Sensitive:   true,
				ElementType: types.StringType,
			},
			"credential_info_json": schema.StringAttribute{
				Description:   "Canonical JSON object for heterogeneous credential metadata. Legacy and JSON keys are merged; overlapping keys must have exactly equal values.",
				Optional:      true,
				Computed:      true,
				Validators:    jsonValidators,
				PlanModifiers: jsonPlanModifiers,
			},
			"credential_values_json": schema.StringAttribute{
				Description:   "Canonical JSON object for heterogeneous sensitive credential values. Legacy and JSON keys are merged; overlapping keys must have exactly equal values.",
				Optional:      true,
				Computed:      true,
				Sensitive:     true,
				Validators:    jsonValidators,
				PlanModifiers: jsonPlanModifiers,
			},
			"credential_values_active": schema.BoolAttribute{
				Description: "Whether configured credential_values and credential_values_json were the active create source. False when model_id won or the resource was imported metadata-only.",
				Computed:    true,
			},
			"credential_source": schema.StringAttribute{
				Description: "Effective ownership source: credential_values, model_id, or imported.",
				Computed:    true,
			},
		},
	}
}

func (r *CredentialResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan rejects clears that LiteLLM's shallow PATCH cannot consume and
// records replacement safety without replacing either public map type.
func (r *CredentialResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan CredentialResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	config := plan
	if !req.Config.Raw.IsNull() {
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	info, infoErr := buildCredentialConfiguredObject(ctx, config.CredentialInfo, config.CredentialInfoJSON)
	values, valuesErr := buildCredentialConfiguredObject(ctx, config.CredentialValues, config.CredentialValuesJSON)
	if infoErr != nil && !errors.Is(infoErr, errCredentialUnknown) {
		resp.Diagnostics.AddError("Credential Attribute Conflict", credentialConflictDiagnostic(infoErr))
		return
	}
	if valuesErr != nil && !errors.Is(valuesErr, errCredentialUnknown) {
		resp.Diagnostics.AddError("Credential Attribute Conflict", credentialConflictDiagnostic(valuesErr))
		return
	}
	if req.State.Raw.IsNull() {
		if errors.Is(valuesErr, errCredentialUnknown) || config.ModelID.IsUnknown() {
			return
		}
		if !credentialKnownModelSource(config.ModelID) && len(values.Object) == 0 {
			resp.Diagnostics.AddError(
				"Missing Credential Create Source",
				"When model_id is omitted, credential_values and credential_values_json must merge to a non-empty object. LiteLLM v1.98 rejects an empty values-only object. Configure at least one value or use model_id.",
			)
		}
		return
	}

	var state CredentialResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	replacementPending := len(credentialReplacementPaths(state, plan)) != 0
	metadata, diagnostics := readCredentialPrivateMetadata(ctx, req.Private, &config)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		// Even invalid private bytes cannot let an identity replacement reach an
		// unguarded Delete. Replace them only with an explicitly unowned marker;
		// the diagnostic still fails the plan closed.
		if replacementPending && resp.Private != nil {
			blocked := unownedCredentialPrivateMetadata()
			blocked.ReplacementPending = true
			resp.Diagnostics.Append(writeCredentialPrivateMetadata(ctx, resp.Private, blocked)...)
		}
		return
	}
	metadata.ReplacementPending = replacementPending
	if replacementPending && resp.Private != nil {
		// Persist this before every unknown-value return below. A caller that
		// bypasses the deferred plan still reaches the guarded Delete path.
		resp.Diagnostics.Append(writeCredentialPrivateMetadata(ctx, resp.Private, metadata)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if metadata.UncertainOwnership {
		resp.Diagnostics.AddError(
			"Uncertain Credential Ownership",
			"A prior create had an ambiguous transport or response outcome. Terraform retained the caller-known identity but will not update or replace the possibly concurrent credential. Inspect LiteLLM, then either import after verifying ownership or remove the retained state only after resolving the remote object.",
		)
		return
	}
	if metadata.Imported && credentialConfigHasSource(config) {
		resp.Diagnostics.AddError(
			"Unsafe Credential Source Adoption",
			"This metadata-only import has remote credential values whose ownership and cleartext reconstructability are unknown. Adding credential_values, credential_values_json, or model_id could overwrite or replace unmanaged secrets, so the plan is rejected. Create a separately named credential or remove and re-import only after arranging explicit ownership outside this lifecycle.",
		)
		return
	}

	if errors.Is(infoErr, errCredentialUnknown) || errors.Is(valuesErr, errCredentialUnknown) {
		return
	}
	if credentialTopLevelKeyRemoved(credentialMetadataOwnership(metadata, false), info.UnionOwnership) ||
		credentialTopLevelKeyRemoved(credentialMetadataOwnership(metadata, true), values.UnionOwnership) {
		resp.Diagnostics.AddError(
			"Unsafe Top-Level Credential Removal",
			"LiteLLM v1.98 PATCH only merges top-level credential dictionaries, and JSON null is stored rather than consumed as a clear. The provider will not delete and recreate the credential because unmanaged keys or secrets could be lost. Keep the key configured, or create a new credential under a different name with the intended complete contents.",
		)
		return
	}

	if replacementPending && !metadata.AllRemoteOwned {
		resp.Diagnostics.AddError(
			"Unsafe Credential Replacement",
			"The create-only model_id or credential_name change requires replacement, but the latest authoritative read did not prove that every remote metadata and secret key is Terraform-owned and reconstructable. Replacement is blocked to avoid deleting unmanaged credential data.",
		)
		return
	}
	if !replacementPending && resp.Private != nil && !metadata.noPrivateFallback {
		resp.Diagnostics.Append(writeCredentialPrivateMetadata(ctx, resp.Private, metadata)...)
	}
}

func (r *CredentialResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan CredentialResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	config := plan
	if !req.Config.Raw.IsNull() {
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	createRequest, info, values, err := buildCredentialCreateRequest(ctx, config)
	if err != nil {
		resp.Diagnostics.AddError("Credential Request Error", credentialConflictDiagnostic(err))
		return
	}
	metadata, err := inferCredentialPrivateMetadata(ctx, config)
	if err != nil {
		resp.Diagnostics.AddError("Credential Request Error", "The provider could not establish credential ownership safely.")
		return
	}
	if metadata.ModelDominant && (values.LegacyConfigured || values.JSONConfigured) {
		resp.Diagnostics.AddWarning(
			"Configured Credential Values Are Inactive",
			"LiteLLM gives model_id precedence during create. The configured legacy/JSON credential values are retained only for HCL compatibility, are marked inactive in state, and are not verified or claimed as applied.",
		)
	}

	name := config.CredentialName.ValueString()
	if existing, preflightErr := r.fetchCredentialByName(ctx, name); preflightErr == nil {
		if existing.name == name {
			resp.Diagnostics.AddError("Credential Already Exists", "A credential with this exact name already exists. Terraform did not adopt or mutate it; import it only after verifying ownership.")
		} else {
			resp.Diagnostics.AddError("Credential Create Preflight Failed", "The exact by-name route returned a different credential identity, so Terraform did not send the create request.")
		}
		return
	} else if !IsAPIErrorStatus(preflightErr, http.StatusNotFound) {
		resp.Diagnostics.AddError("Credential Create Preflight Failed", "Terraform could not prove exact-name absence before create, so it did not send the request.")
		return
	}

	verifyCreate := func(remote credentialRemote) error {
		if err := verifyCredentialPostflight(remote.info, map[string]interface{}{}, info.Object, emptyCredentialOwnership(), info.UnionOwnership, false); err != nil {
			return err
		}
		if metadata.ModelDominant {
			return nil
		}
		return verifyCredentialPostflight(remote.values, map[string]interface{}{}, values.Object, emptyCredentialOwnership(), values.UnionOwnership, true)
	}

	var mutation credentialMutationResponse
	accepted, mutationErr := r.client.doRequestWithResponse(ctx, http.MethodPost, "/credentials", createRequest, &mutation)
	responseErr := mutationErr
	if responseErr == nil {
		responseErr = validateCredentialMutationResponse(mutation)
	}
	if responseErr != nil {
		if !shouldRecoverCredentialCreate(accepted, mutationErr) {
			resp.Diagnostics.AddError("Credential Create Error", "LiteLLM definitively rejected or locally failed the credential create request. No resource state was retained.")
			return
		}
		_, recoveryErr := r.confirmCredentialMutation(ctx, name, verifyCreate)
		setCredentialUncertainCreateState(ctx, resp, &plan, config)
		if recoveryErr == nil {
			resp.Diagnostics.AddError("Credential Create Recovered With Uncertain Ownership", "A unique exact-name, exact-configuration credential appeared during bounded recovery and was retained in partial state. The create response was unusable, so Terraform cannot distinguish its operation from a concurrent identical create; inspect ownership before importing or removing the retained state.")
		} else {
			resp.Diagnostics.AddError("Credential Create Outcome Uncertain", "The create may have committed, but bounded exact-name recovery could not prove the requested owned result. Caller-known identity was retained in partial state with no remote ownership; inspect LiteLLM before importing or removing that state.")
		}
		return
	}

	remote, postflightErr := r.confirmCredentialMutation(ctx, name, verifyCreate)
	if postflightErr != nil {
		setCredentialUncertainCreateState(ctx, resp, &plan, config)
		resp.Diagnostics.AddError("Credential Create Postflight Failed", "LiteLLM reported create success, but bounded exact-name recovery could not prove the complete owned result. Caller-known identity was retained in partial state with no remote ownership.")
		return
	}
	metadata.AllRemoteOwned = credentialRemoteFullyOwned(remote.info, info.Object, info.UnionOwnership, false) &&
		credentialRemoteFullyOwned(remote.values, values.Object, credentialMetadataOwnership(metadata, true), true)
	state := credentialCreateStateFromConfig(config, metadata)
	if err := reconcileCredentialState(ctx, &state, remote, info.Object, values.Object, metadata); err != nil {
		setCredentialUncertainCreateState(ctx, resp, &plan, config)
		resp.Diagnostics.AddError("Credential Create State Reconciliation Failed", "LiteLLM reported create success, but the authoritative result could not be represented safely. Caller-known identity was retained in partial state with no remote ownership.")
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(writeCredentialPrivateMetadata(ctx, resp.Private, metadata)...)
	}
}

func isAmbiguousCredentialCreateStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout || (statusCode >= http.StatusInternalServerError && statusCode < 600)
}

func shouldRecoverCredentialCreate(accepted bool, mutationErr error) bool {
	if accepted {
		return true
	}
	if mutationErr == nil {
		return false
	}
	var apiErr *APIError
	if errors.As(mutationErr, &apiErr) {
		return isAmbiguousCredentialCreateStatus(apiErr.StatusCode)
	}
	var responseErr *safeResponseError
	if errors.As(mutationErr, &responseErr) {
		return isAmbiguousCredentialCreateStatus(responseErr.statusCode)
	}
	var transportErr *safeTransportError
	if errors.As(mutationErr, &transportErr) {
		if !transportErr.dispatched {
			return false
		}
		return transportErr.Timeout() || transportErr.Temporary() ||
			transportErr.kind == "LiteLLM HTTP transport request failed" ||
			errors.Is(transportErr, context.Canceled)
	}
	return false
}

func setCredentialUncertainCreateState(ctx context.Context, resp *resource.CreateResponse, plan *CredentialResourceModel, config CredentialResourceModel) {
	metadata := unownedCredentialPrivateMetadata()
	metadata.ModelDominant = credentialKnownModelSource(config.ModelID)
	metadata.UncertainOwnership = true
	finalizeCredentialRecoveryState(plan, config, metadata)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(writeCredentialPrivateMetadata(ctx, resp.Private, metadata)...)
	}
}

// buildCredentialRequest is retained for source compatibility with focused
// tests. Lifecycle code uses the error-returning create helper.
func (r *CredentialResource) buildCredentialRequest(ctx context.Context, data *CredentialResourceModel) map[string]interface{} {
	request, _, _, _ := buildCredentialCreateRequest(ctx, *data)
	return request
}

func buildCredentialCreateRequest(ctx context.Context, data CredentialResourceModel) (map[string]interface{}, credentialConfiguredObject, credentialConfiguredObject, error) {
	// Apply configuration, rather than the planned state, is the source of the
	// POST payload. Unknown identity or source selection at this boundary must
	// fail closed before even the exact-name preflight, since treating an
	// unknown model_id as omitted could create the wrong credential.
	if data.CredentialName.IsUnknown() || data.ModelID.IsUnknown() {
		return nil, credentialConfiguredObject{}, credentialConfiguredObject{}, errCredentialUnknown
	}
	if data.CredentialName.IsNull() || data.CredentialName.ValueString() == "" {
		return nil, credentialConfiguredObject{}, credentialConfiguredObject{}, errors.New("credential_name must be a known non-empty string at apply time")
	}
	info, err := buildCredentialConfiguredObject(ctx, data.CredentialInfo, data.CredentialInfoJSON)
	if err != nil {
		return nil, credentialConfiguredObject{}, credentialConfiguredObject{}, err
	}
	values, err := buildCredentialConfiguredObject(ctx, data.CredentialValues, data.CredentialValuesJSON)
	if err != nil {
		return nil, credentialConfiguredObject{}, credentialConfiguredObject{}, err
	}
	modelPresent := credentialKnownModelSource(data.ModelID)
	valuesPresent := values.LegacyConfigured || values.JSONConfigured
	if !modelPresent && len(values.Object) == 0 {
		return nil, credentialConfiguredObject{}, credentialConfiguredObject{}, errors.New("when model_id is omitted, credential_values and credential_values_json must merge to a non-empty object")
	}
	request := map[string]interface{}{
		"credential_name": data.CredentialName.ValueString(),
		"credential_info": info.Object,
	}
	if valuesPresent {
		// With model_id, an explicitly configured empty object remains present
		// and is accepted as an inactive source; it is not confused with omission.
		request["credential_values"] = values.Object
	}
	if modelPresent {
		request["model_id"] = data.ModelID.ValueString()
	}
	return request, info, values, nil
}

func (r *CredentialResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data CredentialResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	metadata, diagnostics := readCredentialPrivateMetadata(ctx, req.Private, nil)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if metadata.UncertainOwnership {
		resp.Diagnostics.AddError("Uncertain Credential Ownership", "A prior create outcome remains ambiguous. Refresh retained caller-known state without adopting, removing, or mutating the exact-name credential; inspect LiteLLM and resolve ownership explicitly.")
		return
	}
	priorInfo, err := credentialOwnedObjectFromSurfaces(ctx, data.CredentialInfo, data.CredentialInfoJSON, metadata.LegacyInfo, metadata.JSONInfo)
	if err != nil {
		resp.Diagnostics.AddError("Credential State Error", formatCredentialSafetyError(err))
		return
	}
	priorValues, err := credentialOwnedObjectFromSurfaces(ctx, data.CredentialValues, data.CredentialValuesJSON, metadata.LegacyValues, metadata.JSONValues)
	if err != nil && !metadata.ModelDominant {
		resp.Diagnostics.AddError("Credential State Error", formatCredentialSafetyError(err))
		return
	}

	name := data.CredentialName.ValueString()
	if name == "" {
		name = data.ID.ValueString()
	}
	remote, err := r.fetchCredentialByName(ctx, name)
	if err != nil {
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Credential Read Error", "LiteLLM did not return a valid credential from the exact by-name route.")
		return
	}
	if remote.name != name {
		resp.Diagnostics.AddError("Credential Read Error", "LiteLLM returned a credential identity that did not match the exact requested name.")
		return
	}
	if metadata.noPrivateFallback {
		if err := reconcileSchemaZeroCredentialState(&data, remote); err != nil {
			resp.Diagnostics.AddError("Credential Read Safety Error", formatCredentialSafetyError(err))
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	metadata.AllRemoteOwned = credentialRemoteFullyOwned(remote.info, priorInfo, credentialMetadataOwnership(metadata, false), false) &&
		credentialRemoteFullyOwned(remote.values, priorValues, credentialMetadataOwnership(metadata, true), true)
	if err := reconcileCredentialState(ctx, &data, remote, priorInfo, priorValues, metadata); err != nil {
		resp.Diagnostics.AddError("Credential Read Safety Error", formatCredentialSafetyError(err))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(writeCredentialPrivateMetadata(ctx, resp.Private, metadata)...)
	}
}

func (r *CredentialResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan CredentialResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state CredentialResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	config := plan
	if !req.Config.Raw.IsNull() {
		resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	plan.ID = state.ID

	priorMetadata, diagnostics := readCredentialPrivateMetadata(ctx, req.Private, &config)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if priorMetadata.UncertainOwnership {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Uncertain Credential Ownership", "A prior create outcome remains ambiguous, so Terraform refused to PATCH a possibly concurrent credential.")
		return
	}
	metadata, err := inferCredentialPrivateMetadata(ctx, config)
	if err != nil {
		resp.Diagnostics.AddError("Credential Update Safety Error", formatCredentialSafetyError(err))
		return
	}
	metadata.Imported = priorMetadata.Imported
	if priorMetadata.Imported && credentialConfigHasSource(config) {
		resp.Diagnostics.AddError("Unsafe Credential Source Adoption", "A metadata-only import cannot adopt a values or model source while remote secret ownership and reconstructability remain unknown. No PATCH was sent.")
		return
	}
	priorInfo, err := credentialOwnedObjectFromSurfaces(ctx, state.CredentialInfo, state.CredentialInfoJSON, priorMetadata.LegacyInfo, priorMetadata.JSONInfo)
	if err != nil {
		resp.Diagnostics.AddError("Credential Update Safety Error", formatCredentialSafetyError(err))
		return
	}
	priorValues, err := credentialOwnedObjectFromSurfaces(ctx, state.CredentialValues, state.CredentialValuesJSON, priorMetadata.LegacyValues, priorMetadata.JSONValues)
	if err != nil && !priorMetadata.ModelDominant {
		resp.Diagnostics.AddError("Credential Update Safety Error", formatCredentialSafetyError(err))
		return
	}
	desiredInfo, err := buildCredentialConfiguredObject(ctx, config.CredentialInfo, config.CredentialInfoJSON)
	if err != nil {
		resp.Diagnostics.AddError("Credential Update Safety Error", credentialConflictDiagnostic(err))
		return
	}
	desiredValues, err := buildCredentialConfiguredObject(ctx, config.CredentialValues, config.CredentialValuesJSON)
	if err != nil {
		resp.Diagnostics.AddError("Credential Update Safety Error", credentialConflictDiagnostic(err))
		return
	}
	if metadata.ModelDominant {
		desiredValues.UnionOwnership = emptyCredentialOwnership()
		desiredValues.Object = map[string]interface{}{}
		if desiredValues.LegacyConfigured || desiredValues.JSONConfigured {
			resp.Diagnostics.AddWarning("Configured Credential Values Are Inactive", "model_id remains the effective create source. Changes to configured legacy/JSON credential values are retained as inactive compatibility state and are not sent or claimed as applied.")
		}
	}

	remoteBefore, err := r.fetchCredentialByName(ctx, state.CredentialName.ValueString())
	if err != nil || remoteBefore.name != state.CredentialName.ValueString() {
		resp.Diagnostics.AddError("Credential Update Preflight Failed", "The exact remote credential could not be read and validated before PATCH, so no mutation was sent.")
		return
	}
	priorInfoOwnership := credentialMetadataOwnership(priorMetadata, false)
	infoPatch, err := hydrateCredentialPatch(remoteBefore.info, priorInfo, desiredInfo.Object, priorInfoOwnership, desiredInfo.UnionOwnership, false)
	if err == nil {
		infoPatch, err = hydrateCredentialInfoTopLevel(remoteBefore.info, infoPatch, priorInfoOwnership, desiredInfo.UnionOwnership)
	}
	if err != nil {
		resp.Diagnostics.AddError("Credential Update Safety Error", formatCredentialSafetyError(err))
		return
	}
	valuesPatch, err := hydrateCredentialPatch(remoteBefore.values, priorValues, desiredValues.Object, credentialMetadataOwnership(priorMetadata, true), desiredValues.UnionOwnership, true)
	if err != nil {
		resp.Diagnostics.AddError("Credential Update Safety Error", formatCredentialSafetyError(err))
		return
	}
	patch := map[string]interface{}{
		"credential_name":   plan.CredentialName.ValueString(),
		"credential_info":   infoPatch,
		"credential_values": valuesPatch,
	}

	var mutation credentialMutationResponse
	accepted, mutationErr := r.client.doRequestWithResponse(ctx, http.MethodPatch, credentialMutationPath(plan.CredentialName.ValueString()), patch, &mutation)
	bodyErr := validateCredentialMutationResponse(mutation)
	if mutationErr != nil {
		bodyErr = mutationErr
	}
	remoteAfter, postflightErr := r.confirmCredentialMutation(ctx, plan.CredentialName.ValueString(), func(remote credentialRemote) error {
		if err := verifyCredentialOwnedObject(remote.info, infoPatch, credentialOwnershipForObject(infoPatch), false); err != nil {
			return err
		}
		if err := verifyCredentialNestedRemovals(remote.info, priorInfoOwnership, desiredInfo.UnionOwnership, true); err != nil {
			return err
		}
		return verifyCredentialPostflight(remote.values, priorValues, desiredValues.Object, credentialMetadataOwnership(priorMetadata, true), desiredValues.UnionOwnership, true)
	})
	if postflightErr == nil {
		metadata.AllRemoteOwned = credentialRemoteFullyOwned(remoteAfter.info, desiredInfo.Object, desiredInfo.UnionOwnership, false) &&
			credentialRemoteFullyOwned(remoteAfter.values, desiredValues.Object, desiredValues.UnionOwnership, true)
		if err := reconcileCredentialState(ctx, &plan, remoteAfter, desiredInfo.Object, desiredValues.Object, metadata); err != nil {
			postflightErr = err
		}
	}
	if postflightErr == nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		if resp.Private != nil {
			resp.Diagnostics.Append(writeCredentialPrivateMetadata(ctx, resp.Private, metadata)...)
		}
	} else {
		// UpdateResponse.State is pre-populated from the plan. Restore the
		// prior state explicitly so an unconfirmed PATCH is never claimed.
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	}
	if !accepted {
		resp.Diagnostics.AddError("Credential Update Error", "LiteLLM did not accept the credential PATCH. The authoritative postflight read was still performed.")
	} else if bodyErr != nil {
		resp.Diagnostics.AddError("Malformed Credential Update Response", "LiteLLM accepted the PATCH status, but its body did not contain the required success result. The authoritative postflight read was still performed.")
	}
	if postflightErr != nil {
		resp.Diagnostics.AddError("Credential Update Postflight Failed", "The provider could not confirm every owned value, mask, and nested removal through the authoritative by-name route, so it did not claim the planned update in state.")
	}
}

func (r *CredentialResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data CredentialResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	metadata, diagnostics := readCredentialPrivateMetadata(ctx, req.Private, nil)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() {
		return
	}
	if metadata.UncertainOwnership {
		resp.Diagnostics.AddError("Uncertain Credential Ownership", "A prior create outcome remains ambiguous, so Terraform refused to DELETE a possibly concurrent credential. Resolve ownership in LiteLLM, then import the verified object or remove retained Terraform state deliberately.")
		return
	}
	if metadata.ReplacementPending {
		priorInfo, infoErr := credentialOwnedObjectFromSurfaces(ctx, data.CredentialInfo, data.CredentialInfoJSON, metadata.LegacyInfo, metadata.JSONInfo)
		priorValues, valuesErr := credentialOwnedObjectFromSurfaces(ctx, data.CredentialValues, data.CredentialValuesJSON, metadata.LegacyValues, metadata.JSONValues)
		remote, readErr := r.fetchCredentialByName(ctx, data.CredentialName.ValueString())
		if readErr != nil && !IsAPIErrorStatus(readErr, http.StatusNotFound) {
			resp.Diagnostics.AddError("Unsafe Credential Replacement", "The exact credential could not be re-read before replacement deletion.")
			return
		}
		if readErr == nil && (infoErr != nil || valuesErr != nil ||
			!credentialRemoteFullyOwned(remote.info, priorInfo, credentialMetadataOwnership(metadata, false), false) ||
			!credentialRemoteFullyOwned(remote.values, priorValues, credentialMetadataOwnership(metadata, true), true)) {
			resp.Diagnostics.AddError("Unsafe Credential Replacement", "Replacement deletion was blocked because the current remote credential contains data that is not proven Terraform-owned and reconstructable.")
			return
		}
	}

	var mutation credentialMutationResponse
	accepted, mutationErr := r.client.doRequestWithResponse(ctx, http.MethodDelete, credentialMutationPath(data.CredentialName.ValueString()), nil, &mutation)
	bodyErr := validateCredentialMutationResponse(mutation)
	if mutationErr != nil {
		bodyErr = mutationErr
	}
	absenceErr := r.confirmCredentialAbsence(ctx, data.CredentialName.ValueString())
	if !accepted && IsAPIErrorStatus(mutationErr, http.StatusNotFound) && absenceErr == nil {
		return
	}
	if !accepted {
		resp.Diagnostics.AddError("Credential Delete Error", "LiteLLM did not accept the credential DELETE. Exact absence was still checked through the authoritative by-name route.")
	} else if bodyErr != nil {
		resp.Diagnostics.AddError("Malformed Credential Delete Response", "LiteLLM accepted the DELETE status, but its body did not contain the required success result. Exact absence was still checked through the authoritative by-name route.")
	}
	if absenceErr != nil {
		resp.Diagnostics.AddError("Credential Delete Postflight Failed", "The provider could not confirm exact credential absence through the authoritative by-name route, so Terraform must retain the resource in state.")
	}
}

func (r *CredentialResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError("Invalid Credential Import", "The credential import ID must be a non-empty exact credential name so refresh and delete remain addressable.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("credential_name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("model_id"), types.StringNull())...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("credential_info"), types.MapUnknown(types.StringType))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("credential_values"), types.MapNull(types.StringType))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("credential_info_json"), types.StringUnknown())...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("credential_values_json"), types.StringNull())...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("credential_values_active"), types.BoolValue(false))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("credential_source"), types.StringValue("imported"))...)
	metadata := credentialPrivateMetadata{
		Version:        1,
		Imported:       true,
		LegacyInfo:     emptyCredentialOwnership(),
		JSONInfo:       emptyCredentialOwnership(),
		LegacyValues:   emptyCredentialOwnership(),
		JSONValues:     emptyCredentialOwnership(),
		ModelDominant:  false,
		AllRemoteOwned: false,
	}
	if resp.Private != nil {
		resp.Diagnostics.Append(writeCredentialPrivateMetadata(ctx, resp.Private, metadata)...)
	}
}

func readCredentialPrivateMetadata(ctx context.Context, private credentialPrivateReader, config *CredentialResourceModel) (credentialPrivateMetadata, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	var encoded []byte
	if private != nil {
		privateEncoded, privateDiagnostics := private.GetKey(ctx, credentialPrivateMetadataKey)
		diagnostics.Append(privateDiagnostics...)
		if diagnostics.HasError() {
			return credentialPrivateMetadata{}, diagnostics
		}
		encoded = privateEncoded
	}
	if len(encoded) != 0 {
		if metadata, ok := decodeCredentialPrivateMetadata(encoded); ok {
			return metadata, diagnostics
		}
		// Invalid private data must never be confused with an old state that has
		// no private data. Re-inferring here could broaden corrupt ownership.
		diagnostics.AddError("Credential Ownership Metadata Error", "Credential private ownership metadata is invalid; the operation was blocked without inferring ownership from public state.")
		return credentialPrivateMetadata{}, diagnostics
	}
	if config != nil {
		metadata, err := inferCredentialPrivateMetadata(ctx, *config)
		if err == nil {
			return metadata, diagnostics
		}
		if !errors.Is(err, errCredentialUnknown) {
			diagnostics.AddError("Credential Ownership Metadata Error", "Schema-v0 credential ownership could not be inferred from configuration safely.")
			return credentialPrivateMetadata{}, diagnostics
		}
	}

	// Read and Delete do not receive Terraform configuration. An old schema-v0
	// state may retain compatibility values, but Optional+Computed state is not
	// evidence that those values were configured. Keep ownership empty and do
	// not persist this process-local fallback.
	metadata := unownedCredentialPrivateMetadata()
	metadata.noPrivateFallback = true
	return metadata, diagnostics
}

func writeCredentialPrivateMetadata(ctx context.Context, private credentialPrivateWriter, metadata credentialPrivateMetadata) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	encoded, err := encodeCredentialPrivateMetadata(metadata)
	if err != nil {
		diagnostics.AddError("Credential Ownership Metadata Error", "Credential ownership metadata could not be encoded safely.")
		return diagnostics
	}
	diagnostics.Append(private.SetKey(ctx, credentialPrivateMetadataKey, encoded)...)
	return diagnostics
}

func credentialCreateStateFromConfig(config CredentialResourceModel, metadata credentialPrivateMetadata) CredentialResourceModel {
	// The plan may legitimately contain unknowns that resolve only in apply
	// configuration. Start create state from that resolved configuration so a
	// known model, write-only value, or JSON document is never replaced by its
	// stale planned unknown. Omitted Optional+Computed surfaces are normalized
	// to known null until an authoritative postflight read populates them.
	data := config
	data.ID = types.StringValue(config.CredentialName.ValueString())
	data.CredentialName = types.StringValue(config.CredentialName.ValueString())
	if data.ModelID.IsUnknown() {
		data.ModelID = types.StringNull()
	}
	if data.CredentialInfo.IsUnknown() {
		data.CredentialInfo = types.MapNull(types.StringType)
	}
	if data.CredentialValues.IsUnknown() {
		data.CredentialValues = types.MapNull(types.StringType)
	}
	if data.CredentialInfoJSON.IsUnknown() {
		data.CredentialInfoJSON = types.StringNull()
	}
	if data.CredentialValuesJSON.IsUnknown() {
		data.CredentialValuesJSON = types.StringNull()
	}
	if metadata.ModelDominant {
		data.CredentialValuesActive = types.BoolValue(false)
		data.CredentialSource = types.StringValue("model_id")
	} else {
		data.CredentialValuesActive = types.BoolValue(true)
		data.CredentialSource = types.StringValue("credential_values")
	}
	if metadata.Imported {
		data.CredentialValuesActive = types.BoolValue(false)
		data.CredentialSource = types.StringValue("imported")
	}
	return data
}

func finalizeCredentialRecoveryState(data *CredentialResourceModel, config CredentialResourceModel, metadata credentialPrivateMetadata) {
	*data = credentialCreateStateFromConfig(config, metadata)
}

func reconcileCredentialState(ctx context.Context, data *CredentialResourceModel, remote credentialRemote, priorInfo, priorValues map[string]interface{}, metadata credentialPrivateMetadata) error {
	if remote.name == "" {
		return errors.New("credential identity is empty")
	}
	var next CredentialResourceModel = *data
	next.CredentialName = types.StringValue(remote.name)
	next.ID = types.StringValue(remote.name)

	if metadata.LegacyInfoConfigured {
		projected, err := projectCredentialObject(remote.info, priorInfo, metadata.LegacyInfo, false)
		if err != nil {
			return err
		}
		next.CredentialInfo, err = stringMapValueFromObject(projected)
		if err != nil {
			return err
		}
	} else {
		var err error
		next.CredentialInfo, err = stringMapValueFromObject(remote.info)
		if err != nil {
			return err
		}
	}
	if metadata.JSONInfoConfigured {
		projected, err := projectCredentialObject(remote.info, priorInfo, metadata.JSONInfo, false)
		if err != nil {
			return err
		}
		canonical, err := canonicalCredentialJSON(projected)
		if err != nil {
			return err
		}
		next.CredentialInfoJSON = types.StringValue(canonical)
	} else {
		canonical, err := canonicalCredentialJSON(remote.info)
		if err != nil {
			return err
		}
		next.CredentialInfoJSON = types.StringValue(canonical)
	}

	if metadata.ModelDominant {
		next.CredentialValuesActive = types.BoolValue(false)
		next.CredentialSource = types.StringValue("model_id")
	} else if metadata.Imported {
		next.CredentialValues = types.MapNull(types.StringType)
		next.CredentialValuesJSON = types.StringNull()
		next.CredentialValuesActive = types.BoolValue(false)
		next.CredentialSource = types.StringValue("imported")
	} else {
		if metadata.LegacyValuesConfigured {
			projected, err := projectCredentialObject(remote.values, priorValues, metadata.LegacyValues, true)
			if err != nil {
				return err
			}
			next.CredentialValues, err = stringMapValueFromObject(projected)
			if err != nil {
				return err
			}
		} else {
			next.CredentialValues = types.MapNull(types.StringType)
		}
		if metadata.JSONValuesConfigured {
			projected, err := projectCredentialObject(remote.values, priorValues, metadata.JSONValues, true)
			if err != nil {
				return err
			}
			canonical, err := canonicalCredentialJSON(projected)
			if err != nil {
				return err
			}
			next.CredentialValuesJSON = types.StringValue(canonical)
		} else {
			next.CredentialValuesJSON = types.StringNull()
		}
		next.CredentialValuesActive = types.BoolValue(true)
		next.CredentialSource = types.StringValue("credential_values")
	}
	*data = next
	return nil
}

// reconcileSchemaZeroCredentialState refreshes public compatibility output
// without deriving recursive ownership from Optional+Computed state. Existing
// cleartext value surfaces are retained verbatim; only a later operation with
// Terraform Config may establish private ownership.
func reconcileSchemaZeroCredentialState(data *CredentialResourceModel, remote credentialRemote) error {
	if remote.name == "" {
		return errors.New("credential identity is empty")
	}
	data.ID = types.StringValue(remote.name)
	data.CredentialName = types.StringValue(remote.name)

	info, err := stringMapValueFromObject(remote.info)
	if err != nil {
		return err
	}
	data.CredentialInfo = info
	canonicalInfo, err := canonicalCredentialJSON(remote.info)
	if err != nil {
		return err
	}
	data.CredentialInfoJSON = types.StringValue(canonicalInfo)

	if data.CredentialValues.IsUnknown() {
		data.CredentialValues = types.MapNull(types.StringType)
	}
	if data.CredentialValuesJSON.IsUnknown() {
		data.CredentialValuesJSON = types.StringNull()
	}
	if credentialKnownModelSource(data.ModelID) {
		data.CredentialValuesActive = types.BoolValue(false)
		data.CredentialSource = types.StringValue("model_id")
	} else if !data.CredentialValues.IsNull() || !data.CredentialValuesJSON.IsNull() {
		data.CredentialValuesActive = types.BoolValue(true)
		data.CredentialSource = types.StringValue("credential_values")
	} else {
		data.CredentialValuesActive = types.BoolValue(false)
		data.CredentialSource = types.StringValue("imported")
	}
	return nil
}

func validateCredentialMutationResponse(response credentialMutationResponse) error {
	if response.Success == nil || !*response.Success || response.Message == "" {
		return errors.New("LiteLLM mutation response did not report success")
	}
	return nil
}

func (r *CredentialResource) fetchCredentialByName(ctx context.Context, name string) (credentialRemote, error) {
	var result credentialAPIResponse
	if err := r.client.DoRequestWithResponse(ctx, http.MethodGet, credentialByNamePath(name), nil, &result); err != nil {
		return credentialRemote{}, err
	}
	return decodeCredentialResponse(result)
}

func decodeCredentialResponse(result credentialAPIResponse) (credentialRemote, error) {
	if result.CredentialName == "" || len(result.CredentialInfo) == 0 || len(result.CredentialValues) == 0 {
		return credentialRemote{}, errors.New("LiteLLM returned a malformed credential response")
	}
	info, err := decodeCredentialJSONObjectBytes(result.CredentialInfo)
	if err != nil {
		return credentialRemote{}, err
	}
	values, err := decodeCredentialJSONObjectBytes(result.CredentialValues)
	if err != nil {
		return credentialRemote{}, err
	}
	return credentialRemote{name: result.CredentialName, info: info, values: values}, nil
}

func (r *CredentialResource) confirmCredentialMutation(ctx context.Context, name string, verify func(credentialRemote) error) (credentialRemote, error) {
	var lastErr error
	for attempt := 0; attempt < credentialPostflightAttempts; attempt++ {
		remote, err := r.fetchCredentialByName(ctx, name)
		if err == nil {
			if remote.name != name {
				lastErr = errors.New("credential identity mismatch")
			} else if err := verify(remote); err == nil {
				return remote, nil
			} else {
				// A matching exact-name row can still be propagating nested values.
				// Keep verification bounded rather than adopting a partial result.
				lastErr = err
			}
		} else {
			lastErr = err
			if !shouldRetryCredentialRecoveryRead(err) {
				return credentialRemote{}, err
			}
		}
		if attempt < credentialPostflightAttempts-1 {
			if err := waitCredentialRetry(ctx, attempt); err != nil {
				return credentialRemote{}, err
			}
		}
	}
	return credentialRemote{}, lastErr
}

func shouldRetryCredentialRecoveryRead(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if IsAPIErrorStatus(err, http.StatusNotFound) {
		return true
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusRequestTimeout || apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}
	var transportErr *safeTransportError
	if errors.As(err, &transportErr) {
		return transportErr.Retryable()
	}
	var responseErr *safeResponseError
	if errors.As(err, &responseErr) {
		return responseErr.retryable
	}
	return false
}

func (r *CredentialResource) confirmCredentialAbsence(ctx context.Context, name string) error {
	var lastErr error
	for attempt := 0; attempt < credentialPostflightAttempts; attempt++ {
		_, err := r.fetchCredentialByName(ctx, name)
		if IsAPIErrorStatus(err, http.StatusNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		lastErr = errors.New("credential still exists")
		if attempt < credentialPostflightAttempts-1 {
			if err := waitCredentialRetry(ctx, attempt); err != nil {
				return err
			}
		}
	}
	return lastErr
}

func waitCredentialRetry(ctx context.Context, attempt int) error {
	delay := 100 * time.Millisecond * time.Duration(1<<attempt)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

func (r *CredentialResource) readCredential(ctx context.Context, data *CredentialResourceModel) error {
	metadata, err := inferCredentialPrivateMetadata(ctx, *data)
	if err != nil {
		return err
	}
	priorInfo, err := credentialOwnedObjectFromSurfaces(ctx, data.CredentialInfo, data.CredentialInfoJSON, metadata.LegacyInfo, metadata.JSONInfo)
	if err != nil {
		return err
	}
	priorValues, err := credentialOwnedObjectFromSurfaces(ctx, data.CredentialValues, data.CredentialValuesJSON, metadata.LegacyValues, metadata.JSONValues)
	if err != nil && !metadata.ModelDominant {
		return err
	}
	name := data.CredentialName.ValueString()
	if name == "" {
		name = data.ID.ValueString()
	}
	remote, err := r.fetchCredentialByName(ctx, name)
	if err != nil {
		return err
	}
	if remote.name != name {
		return errors.New("LiteLLM returned a credential identity mismatch")
	}
	return reconcileCredentialState(ctx, data, remote, priorInfo, priorValues, metadata)
}

func (r *CredentialResource) readCredentialWithRetry(ctx context.Context, data *CredentialResourceModel, maxRetries int) error {
	var err error
	for attempt := 0; attempt < maxRetries; attempt++ {
		err = r.readCredential(ctx, data)
		if err == nil {
			return nil
		}
		if !IsAPIErrorStatus(err, http.StatusNotFound) {
			return err
		}
		if attempt < maxRetries-1 {
			if waitErr := waitCredentialRetry(ctx, attempt); waitErr != nil {
				return waitErr
			}
		}
	}
	return err
}

func credentialByNamePath(name string) string {
	return "/credentials/by_name/" + escapeCredentialPathValue(name)
}

func credentialByModelPath(modelID string) string {
	return "/credentials/by_model/" + escapeCredentialPathValue(modelID)
}

func credentialMutationPath(name string) string {
	return "/credentials/" + escapeCredentialPathValue(name)
}

func escapeCredentialPathValue(value string) string {
	escaped := url.PathEscape(value)
	// Dot segments are legal text but unsafe as complete segments in proxies
	// that normalize traversal before routing.
	return strings.ReplaceAll(escaped, ".", "%2E")
}
