package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
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
	// Four conclusive probes are sampled over fresh connections. Exact absence
	// is authoritative only when all four are consecutive exact 404 responses.
	// Transient failures reset that absence streak and may use the bounded extra
	// attempts; they never count as evidence that a credential is absent.
	credentialProbeSampleSize  = 4
	credentialProbeMaxAttempts = 8

	// LiteLLM v1.98 PATCH is semantically idempotent for one identical,
	// fully-hydrated body. Use the same bounded fresh-connection budget as the
	// conclusive probe sample to update more than one process-local cache.
	credentialPatchFanoutSize = credentialProbeSampleSize

	// Retained aliases keep focused lifecycle tests source-compatible.
	credentialPostflightAttempts = credentialProbeSampleSize
	credentialReadAttempts       = credentialProbeMaxAttempts
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

type credentialProbeSample struct {
	present            []credentialRemote
	absent             int
	transient          int
	consecutiveAbsence int
}

func (s credentialProbeSample) hasPresence() bool {
	return len(s.present) != 0
}

func (s credentialProbeSample) authoritativeAbsence() bool {
	return len(s.present) == 0 && s.consecutiveAbsence >= credentialProbeSampleSize
}

func (s credentialProbeSample) versionsMatch() bool {
	if len(s.present) < 2 {
		return true
	}
	first := s.present[0]
	for _, remote := range s.present[1:] {
		if remote.name != first.name || !reflect.DeepEqual(remote.info, first.info) || !reflect.DeepEqual(remote.values, first.values) {
			return false
		}
	}
	return true
}

func (s credentialProbeSample) convergenceUncertain() bool {
	return s.hasPresence() && (s.absent != 0 || !s.versionsMatch())
}

var errCredentialProbeIncomplete = errors.New("bounded credential worker sampling was inconclusive")

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
	client, ok := configuredClient(req.ProviderData)
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
	if credentialValuesAreUnowned(metadata) && credentialConfigHasSource(config) {
		resp.Diagnostics.AddError(
			"Unsafe Credential Source Adoption",
			"This metadata-only credential has remote credential values whose ownership and cleartext reconstructability are unknown. Adding credential_values, credential_values_json, or model_id could overwrite or replace unmanaged secrets, so the plan is rejected. Create a separately named credential or remove and re-import only after arranging explicit ownership outside this lifecycle.",
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
	preflight, preflightErr := probeCredentialEndpoint(ctx, r.client, credentialByNamePath(name), name)
	if preflight.hasPresence() {
		detail := "A credential with this exact name was present on at least one fresh-connection worker probe. Terraform did not adopt or mutate it; import it only after verifying ownership."
		if preflight.convergenceUncertain() || preflightErr != nil {
			detail += " LiteLLM v1.98 keeps credential lookups in process-local worker caches, and the sampled workers were not consistent. Reload or restart workers as appropriate, verify convergence, and retry."
		}
		resp.Diagnostics.AddError("Credential Already Exists", detail)
		return
	}
	if preflightErr != nil || !preflight.authoritativeAbsence() {
		resp.Diagnostics.AddError("Credential Create Preflight Failed", "Terraform could not prove exact-name absence through four consecutive exact 404 responses over fresh connections, so it did not send the create request. Retry transient failures or reconcile LiteLLM v1.98 worker caches before retrying.")
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
		_, _, recoveryErr := r.confirmCredentialCreate(ctx, name, verifyCreate)
		setCredentialUncertainCreateState(ctx, resp, &plan, config)
		if recoveryErr == nil {
			resp.Diagnostics.AddError("Credential Create Recovered With Uncertain Ownership", "A unique exact-name, exact-configuration credential appeared during bounded recovery and was retained in partial state. The create response was unusable, so Terraform cannot distinguish its operation from a concurrent identical create; inspect ownership before importing or removing the retained state.")
		} else {
			resp.Diagnostics.AddError("Credential Create Outcome Uncertain", "The create may have committed, but bounded exact-name recovery could not prove the requested owned result. Caller-known identity was retained in partial state with no remote ownership; inspect LiteLLM before importing or removing that state.")
		}
		return
	}

	remote, postflightSample, postflightErr := r.confirmCredentialCreate(ctx, name, verifyCreate)
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
	if postflightSample.absent != 0 {
		resp.Diagnostics.AddWarning(
			"Credential Worker Convergence Uncertain",
			"Create was confirmed from an exact matching credential on at least one fresh-connection probe, while another LiteLLM v1.98 worker returned exact 404. The credential is durably stored in LiteLLM's database, but this lookup is served from each worker's process-local credential_list. Terraform retained the verified identity without claiming worker-cache or cluster-wide convergence.",
		)
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
	sample, probeErr := probeCredentialEndpoint(ctx, r.client, credentialByNamePath(name), name)
	if !sample.hasPresence() {
		if probeErr == nil && sample.authoritativeAbsence() {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("Credential Read Sampling Inconclusive", "Terraform retained the credential in state because bounded fresh-connection probes did not establish either a usable present credential or four consecutive exact 404 responses. Retry transient failures or reconcile LiteLLM v1.98 process-local worker caches before retrying.")
		return
	}
	if probeErr != nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		resp.Diagnostics.AddError("Credential Read Sampling Inconclusive", "Terraform retained the credential in state because the bounded fresh-connection sample was incomplete. A transient or terminal API result is not evidence of durable deletion; retry after checking LiteLLM worker health.")
		return
	}

	// A mixed cache sample is not remote drift when at least one exact-name
	// worker still serves the previously owned semantic version. Keeping the
	// prior state makes apply -> immediate plan stable without adopting an
	// arbitrary stale worker. Conflicting versions with no prior match remain a
	// hard error, and only a complete consecutive-404 sample removes state.
	if sample.absent != 0 || !sample.versionsMatch() {
		matchingPrior := credentialMatchingRemotes(sample.present, func(remote credentialRemote) bool {
			return credentialRemoteMatchesOwnedState(remote, priorInfo, priorValues, metadata)
		})
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		if len(matchingPrior) != len(sample.present) {
			resp.Diagnostics.AddError("Credential Worker Versions Conflict", "Fresh LiteLLM v1.98 workers returned mixed presence or conflicting cached credential versions, and at least one present version did not semantically match the prior Terraform-owned state. Terraform retained prior state and did not adopt arbitrary data. Verify the durable database record and reconcile each worker's process-local credential_list before retrying.")
			return
		}
		resp.Diagnostics.AddWarning(
			"Credential Worker Convergence Uncertain",
			"At least one fresh LiteLLM v1.98 worker returned the exact identity and prior Terraform-owned semantic version while another returned exact 404 or a different cached version. Terraform retained the previously verified state. LiteLLM stores the durable record in its database but serves this lookup from each worker's process-local credential_list, so this warning does not claim worker-cache or cluster-wide convergence.",
		)
		return
	}
	remote := sample.present[0]
	if metadata.noPrivateFallback {
		if err := reconcileSchemaZeroCredentialState(&data, remote); err != nil {
			resp.Diagnostics.AddError("Credential Read Safety Error", formatCredentialSafetyError(err))
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	metadata.AllRemoteOwned = !credentialValuesAreUnowned(metadata) &&
		credentialRemoteFullyOwned(remote.info, priorInfo, credentialMetadataOwnership(metadata, false), false) &&
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
	if credentialValuesAreUnowned(priorMetadata) && credentialConfigHasSource(config) {
		resp.Diagnostics.AddError("Unsafe Credential Source Adoption", "A metadata-only credential cannot adopt a values or model source while remote secret ownership and reconstructability remain unknown. No PATCH was sent.")
		return
	}
	if priorMetadata.Imported {
		if metadata.LegacyInfoConfigured || metadata.JSONInfoConfigured {
			// A source-free import may establish metadata ownership only after its
			// hydrated PATCH is authoritatively observed. This candidate is valid
			// on its own (Imported and ownership are never encoded together), but
			// it is written only on the successful postflight path below.
			metadata.Imported = false
			metadata.ValuesUnowned = true
		} else {
			metadata.Imported = true
		}
	} else if priorMetadata.ValuesUnowned {
		metadata.ValuesUnowned = true
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

	name := state.CredentialName.ValueString()
	preflight, preflightErr := probeCredentialEndpoint(ctx, r.client, credentialByNamePath(name), name)
	if preflightErr != nil || !preflight.hasPresence() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Credential Update Preflight Failed", "Bounded fresh-connection probes did not return a usable exact-name credential, so no PATCH was sent. Terraform retained prior state; verify the durable LiteLLM database record and worker health before retrying.")
		return
	}
	if !preflight.versionsMatch() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Credential Update Preflight Failed", "Present workers returned conflicting complete versions, so no PATCH was sent. Exact 404 workers may have an empty process-local cache, but conflicting present data could be durable and is never overwritten arbitrarily.")
		return
	}
	remoteBefore := preflight.present[0]
	priorInfoOwnership := credentialMetadataOwnership(priorMetadata, false)
	priorValuesOwnership := credentialMetadataOwnership(priorMetadata, true)
	matchesPrior := credentialRemoteMatchesOwnedState(remoteBefore, priorInfo, priorValues, priorMetadata)
	retryExpectedInfo, retryInfoErr := credentialShallowMergeExpectation(remoteBefore.info, priorInfo, desiredInfo.Object, priorInfoOwnership, desiredInfo.UnionOwnership, false)
	retryExpectedValues, retryValuesErr := credentialShallowMergeExpectation(remoteBefore.values, priorValues, desiredValues.Object, priorValuesOwnership, desiredValues.UnionOwnership, true)
	matchesDesired := retryInfoErr == nil && retryValuesErr == nil && credentialRemoteMatchesExpectedUpdate(
		remoteBefore,
		remoteBefore,
		retryExpectedInfo,
		retryExpectedValues,
		priorInfoOwnership,
		desiredInfo.UnionOwnership,
		priorValuesOwnership,
		desiredValues.UnionOwnership,
	)
	if !matchesPrior && !matchesDesired {
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		resp.Diagnostics.AddError("Credential Update Preflight Failed", "The consistent present worker version matched neither the prior Terraform-owned state nor the planned owned state, so no PATCH was sent. A third version is never overwritten arbitrarily.")
		return
	}

	// If a previous PATCH was accepted but Terraform retained prior state after
	// an unusable response or postflight, hydrate from the already-desired
	// remote version. The resulting request remains byte-for-byte idempotent.
	hydrationPriorInfo := priorInfo
	hydrationPriorValues := priorValues
	hydrationPriorInfoOwnership := priorInfoOwnership
	hydrationPriorValuesOwnership := priorValuesOwnership
	if !matchesPrior && matchesDesired {
		hydrationPriorInfo = desiredInfo.Object
		hydrationPriorValues = desiredValues.Object
		hydrationPriorInfoOwnership = desiredInfo.UnionOwnership
		hydrationPriorValuesOwnership = desiredValues.UnionOwnership
	}
	infoPatch, err := hydrateCredentialPatch(remoteBefore.info, hydrationPriorInfo, desiredInfo.Object, hydrationPriorInfoOwnership, desiredInfo.UnionOwnership, false)
	if err == nil {
		infoPatch, err = hydrateCredentialInfoTopLevel(remoteBefore.info, infoPatch, hydrationPriorInfoOwnership, desiredInfo.UnionOwnership)
	}
	if err != nil {
		resp.Diagnostics.AddError("Credential Update Safety Error", formatCredentialSafetyError(err))
		return
	}
	valuesPatch, err := hydrateCredentialPatch(remoteBefore.values, hydrationPriorValues, desiredValues.Object, hydrationPriorValuesOwnership, desiredValues.UnionOwnership, true)
	if err != nil {
		resp.Diagnostics.AddError("Credential Update Safety Error", formatCredentialSafetyError(err))
		return
	}
	expectedInfo := shallowMergeCredentialObject(remoteBefore.info, infoPatch)
	expectedValues := shallowMergeCredentialObject(remoteBefore.values, valuesPatch)
	patch := map[string]interface{}{
		"credential_name":   plan.CredentialName.ValueString(),
		"credential_info":   infoPatch,
		"credential_values": valuesPatch,
	}

	// LiteLLM v1.98 PATCH merges the same hydrated full dictionaries into the
	// durable row and then updates only the handling worker's credential_list.
	// Repeating this exact body is semantically idempotent, so a bounded fan-out
	// over fresh connections safely reaches more than one process-local cache.
	patchFanout := r.patchCredentialFanout(ctx, plan.CredentialName.ValueString(), patch)

	postflightSample, postflightProbeErr := probeCredentialEndpoint(ctx, r.client, credentialByNamePath(plan.CredentialName.ValueString()), plan.CredentialName.ValueString())
	var remoteAfter credentialRemote
	var postflightErr error
	matchingDesired := make([]credentialRemote, 0, len(postflightSample.present))
	matchingOld := make([]credentialRemote, 0, len(postflightSample.present))
	conflictingVersions := 0
	for _, remote := range postflightSample.present {
		switch {
		case credentialRemoteMatchesExpectedUpdate(remote, remoteBefore, expectedInfo, expectedValues, priorInfoOwnership, desiredInfo.UnionOwnership, priorValuesOwnership, desiredValues.UnionOwnership):
			matchingDesired = append(matchingDesired, remote)
		case credentialRemoteMatchesExpectedUpdate(remote, remoteBefore, remoteBefore.info, remoteBefore.values, emptyCredentialOwnership(), emptyCredentialOwnership(), emptyCredentialOwnership(), emptyCredentialOwnership()):
			matchingOld = append(matchingOld, remote)
		default:
			conflictingVersions++
		}
	}
	if postflightProbeErr != nil {
		postflightErr = postflightProbeErr
	} else if len(matchingDesired) == 0 {
		postflightErr = errors.New("no sampled worker returned the desired credential version")
	} else if conflictingVersions != 0 {
		postflightErr = errors.New("a sampled worker returned a conflicting credential version")
	} else {
		remoteAfter = matchingDesired[0]
		metadata.AllRemoteOwned = !credentialValuesAreUnowned(metadata) &&
			credentialRemoteFullyOwned(remoteAfter.info, desiredInfo.Object, desiredInfo.UnionOwnership, false) &&
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
	if patchFanout.requestFailures != 0 {
		resp.Diagnostics.AddError("Credential Update Fan-out Failed", "At least one bounded fresh-connection PATCH was not accepted by LiteLLM. The exact hydrated request was not retried beyond the fixed worker fan-out budget, and postflight sampling was still performed.")
	}
	if patchFanout.invalidBodies != 0 {
		resp.Diagnostics.AddError("Malformed Credential Update Response", "At least one accepted PATCH returned a body without the required success result. Every 2xx mutation body is validated; postflight sampling was still performed.")
	}
	if postflightErr != nil {
		resp.Diagnostics.AddError("Credential Update Postflight Failed", "The provider did not find a desired matching version without an additional conflicting version in the bounded fresh-connection sample, so it retained prior state and did not claim the planned update.")
	} else if postflightSample.absent != 0 || len(matchingOld) != 0 {
		resp.Diagnostics.AddWarning(
			"Credential Worker Convergence Uncertain",
			"Update was verified from at least one desired matching worker, while another LiteLLM v1.98 worker returned exact 404 or the exact old cached version. Terraform recorded the verified desired state, but the durable database write and each process-local credential_list can become visible at different times; this warning does not claim worker-cache or cluster-wide convergence.",
		)
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
		name := data.CredentialName.ValueString()
		preflight, preflightErr := probeCredentialEndpoint(ctx, r.client, credentialByNamePath(name), name)
		if preflightErr != nil || preflight.convergenceUncertain() || (!preflight.hasPresence() && !preflight.authoritativeAbsence()) {
			resp.Diagnostics.AddError("Unsafe Credential Replacement", "The exact credential was not consistently observable across bounded fresh-connection worker probes before replacement deletion. Terraform retained state; reconcile LiteLLM v1.98 process-local credential caches and retry.")
			return
		}
		if preflight.hasPresence() {
			remote := preflight.present[0]
			if infoErr != nil || valuesErr != nil ||
				!credentialRemoteFullyOwned(remote.info, priorInfo, credentialMetadataOwnership(metadata, false), false) ||
				!credentialRemoteFullyOwned(remote.values, priorValues, credentialMetadataOwnership(metadata, true), true) {
				resp.Diagnostics.AddError("Unsafe Credential Replacement", "Replacement deletion was blocked because the current remote credential contains data that is not proven Terraform-owned and reconstructable.")
				return
			}
		} else if preflight.authoritativeAbsence() {
			// A prior replacement DELETE can remove the durable row while stale
			// workers keep the first attempt from completing. Once every sampled
			// worker is absent, finish without sending an unsafe repeated DELETE.
			return
		}
	}

	// LiteLLM v1.98 deletes the durable row before evicting the handling
	// worker's credential_list. A second DELETE after the row is gone fails
	// before local eviction, so DELETE fan-out cannot safely clear stale workers.
	// Send exactly one mutation and validate its 2xx body; exact 404 means the
	// durable row is already absent. Replacement and terminal destroy then have
	// deliberately different postflight contracts below.
	var mutation credentialMutationResponse
	accepted, mutationErr := r.client.doRequestWithResponse(ctx, http.MethodDelete, credentialMutationPath(data.CredentialName.ValueString()), nil, &mutation)
	validatedDelete := accepted && mutationErr == nil && validateCredentialMutationResponse(mutation) == nil
	alreadyAbsent := !accepted && IsAPIErrorStatus(mutationErr, http.StatusNotFound)
	absenceSample, absenceErr := r.confirmCredentialAbsence(ctx, data.CredentialName.ValueString())

	if metadata.ReplacementPending {
		if !validatedDelete && !alreadyAbsent {
			if accepted {
				resp.Diagnostics.AddError("Malformed Credential Delete Response", "LiteLLM accepted the replacement DELETE status, but its body did not contain the required success result. Terraform retained state and will not recreate or adopt a credential after an unverified deletion.")
			} else {
				resp.Diagnostics.AddError("Credential Delete Error", "LiteLLM did not accept the replacement DELETE. Terraform retained state and will not recreate or adopt a credential after an unverified deletion.")
			}
			return
		}
		if absenceErr != nil {
			detail := "Replacement deletion requires four consecutive exact 404 responses after the exact-name DELETE. Terraform retained state and blocked recreation/adoption because absence was not proven."
			if absenceSample.hasPresence() {
				detail += " At least one LiteLLM v1.98 worker still serves the old credential from its process-local credential_list; reload or restart workers before retrying replacement."
			}
			resp.Diagnostics.AddError("Credential Delete Postflight Failed", detail)
		}
		return
	}

	if !validatedDelete && !alreadyAbsent {
		if accepted {
			resp.Diagnostics.AddError("Malformed Credential Delete Response", "LiteLLM accepted the DELETE status, but its body did not contain the required success result. Terraform retained state because durable database deletion was not validated.")
		} else {
			resp.Diagnostics.AddError("Credential Delete Error", "LiteLLM did not accept the credential DELETE. Terraform retained state because durable database deletion was not validated.")
		}
		return
	}
	detail := "LiteLLM confirmed the durable database credential is deleted or already absent, so terminal destroy removed Terraform state."
	switch {
	case absenceSample.hasPresence():
		detail += " At least one sampled v1.98 worker still serves a process-local cached copy, including masked or usable secret material."
	case absenceErr != nil:
		detail += " Bounded worker-cache probes were inconclusive."
	default:
		detail += " The bounded sample returned four consecutive 404 responses, but the provider cannot enumerate every worker behind the load balancer."
	}
	detail += " Terraform does not claim credential revocation; reload or restart every stale worker or unsampled worker before relying on the credential being unusable."
	resp.Diagnostics.AddWarning("Credential Deleted From Durable Database With Stale Worker Risk", detail)
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
	if credentialValuesAreUnowned(metadata) {
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
	} else if credentialValuesAreUnowned(metadata) {
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

type credentialMutationFanoutResult struct {
	requestFailures int
	invalidBodies   int
}

func (r *CredentialResource) patchCredentialFanout(ctx context.Context, name string, patch map[string]interface{}) credentialMutationFanoutResult {
	var result credentialMutationFanoutResult
	for range credentialPatchFanoutSize {
		var mutation credentialMutationResponse
		accepted, err := r.client.doRequestWithResponseOptions(
			ctx,
			http.MethodPatch,
			credentialMutationPath(name),
			patch,
			&mutation,
			clientRequestOptions{freshConnection: true},
		)
		if err != nil {
			if accepted {
				result.invalidBodies++
			} else {
				result.requestFailures++
			}
			continue
		}
		if !accepted {
			result.requestFailures++
			continue
		}
		if validateCredentialMutationResponse(mutation) != nil {
			result.invalidBodies++
		}
	}
	return result
}

func credentialMatchingRemotes(remotes []credentialRemote, matches func(credentialRemote) bool) []credentialRemote {
	result := make([]credentialRemote, 0, len(remotes))
	for _, remote := range remotes {
		if matches(remote) {
			result = append(result, remote)
		}
	}
	return result
}

// credentialRemoteMatchesOwnedState compares only recursively owned state.
// Worker-local absence or unowned server metadata must not manufacture drift,
// while readable owned values and exact LiteLLM masks remain compare-and-set
// preconditions.
func credentialRemoteMatchesOwnedState(remote credentialRemote, priorInfo, priorValues map[string]interface{}, metadata credentialPrivateMetadata) bool {
	if validateCredentialOwnedAtomicPreconditions(remote.info, priorInfo, credentialMetadataOwnership(metadata, false), false) != nil {
		return false
	}
	if metadata.ModelDominant || credentialValuesAreUnowned(metadata) {
		return true
	}
	return validateCredentialOwnedAtomicPreconditions(remote.values, priorValues, credentialMetadataOwnership(metadata, true), true) == nil
}

// credentialRemoteMatchesVersion requires a complete semantic representation:
// no expected key may be missing, no additional key may appear, and secret
// leaves may differ only by LiteLLM's exact deterministic mask.
func credentialRemoteMatchesVersion(remote credentialRemote, info, values map[string]interface{}) bool {
	infoOwnership := credentialOwnershipForObject(info)
	if verifyCredentialOwnedObject(remote.info, info, infoOwnership, false) != nil ||
		!credentialRemoteFullyOwned(remote.info, info, infoOwnership, false) {
		return false
	}
	valuesOwnership := credentialOwnershipForObject(values)
	return verifyCredentialOwnedObject(remote.values, values, valuesOwnership, true) == nil &&
		credentialRemoteFullyOwned(remote.values, values, valuesOwnership, true)
}

func shallowMergeCredentialObject(remoteBefore, patch map[string]interface{}) map[string]interface{} {
	expected := make(map[string]interface{}, len(remoteBefore)+len(patch))
	for key, value := range remoteBefore {
		expected[key] = value
	}
	for key, value := range patch {
		expected[key] = value
	}
	return expected
}

// credentialShallowMergeExpectation projects the complete result of applying
// desired ownership to one observed remote version. Previously owned nested
// paths omitted from desired are removed, while unmanaged siblings are carried
// forward exactly as LiteLLM's hydrated shallow dictionary merge preserves
// them. Unlike hydrateCredentialPatch, this projection intentionally does not
// require the observed version to satisfy prior compare-and-set preconditions;
// it is used to classify a retry that may already expose an accepted PATCH.
func credentialShallowMergeExpectation(remote, prior, desired map[string]interface{}, priorOwnership, desiredOwnership *credentialOwnership, masked bool) (map[string]interface{}, error) {
	if credentialTopLevelKeyRemoved(priorOwnership, desiredOwnership) {
		return nil, errors.New("LiteLLM PATCH cannot safely remove an owned top-level credential key")
	}
	result := shallowMergeCredentialObject(remote, nil)
	for key, desiredNode := range desiredOwnership.Children {
		desiredValue := desired[key]
		remoteValue, exists := remote[key]
		if !exists {
			result[key] = desiredValue
			continue
		}
		var priorNode *credentialOwnership
		if priorOwnership != nil && priorOwnership.Object {
			priorNode = priorOwnership.Children[key]
		}
		merged, err := hydrateCredentialValue(
			remoteValue,
			prior[key],
			desiredValue,
			priorNode,
			desiredNode,
			credentialChildMasking(masked, key, remoteValue),
		)
		if err != nil {
			return nil, err
		}
		result[key] = merged
	}
	return result, nil
}

// credentialRemoteMatchesExpectedUpdate proves the complete remote version
// produced by LiteLLM's shallow merge. The union of prior and desired
// ownership distinguishes desired leaves from removal tombstones: desired
// leaves may use exact deterministic masks, formerly owned nested paths must
// be absent, and every retained unmanaged leaf must equal the preflight
// version. Postflight and accepted-PATCH retry preflight share this predicate.
func credentialRemoteMatchesExpectedUpdate(remote, remoteBefore credentialRemote, expectedInfo, expectedValues map[string]interface{}, priorInfoOwnership, desiredInfoOwnership, priorValuesOwnership, desiredValuesOwnership *credentialOwnership) bool {
	return credentialObjectMatchesExpectedUpdate(remote.info, remoteBefore.info, expectedInfo, priorInfoOwnership, desiredInfoOwnership, false) &&
		credentialObjectMatchesExpectedUpdate(remote.values, remoteBefore.values, expectedValues, priorValuesOwnership, desiredValuesOwnership, true)
}

func credentialObjectMatchesExpectedUpdate(remote, remoteBefore, expected map[string]interface{}, priorOwnership, desiredOwnership *credentialOwnership, masked bool) bool {
	if len(remote) != len(expected) {
		return false
	}
	unionOwnership := unionCredentialOwnership(priorOwnership, desiredOwnership)
	for key := range unionOwnership.Children {
		var desiredNode *credentialOwnership
		if desiredOwnership != nil && desiredOwnership.Object {
			desiredNode = desiredOwnership.Children[key]
		}
		if desiredNode == nil {
			if _, remoteExists := remote[key]; remoteExists {
				return false
			}
			if _, expectedExists := expected[key]; expectedExists {
				return false
			}
		}
	}
	for key, expectedValue := range expected {
		remoteValue, remoteExists := remote[key]
		if !remoteExists {
			return false
		}
		var priorNode, desiredNode *credentialOwnership
		if priorOwnership != nil && priorOwnership.Object {
			priorNode = priorOwnership.Children[key]
		}
		if desiredOwnership != nil && desiredOwnership.Object {
			desiredNode = desiredOwnership.Children[key]
		}
		beforeValue, beforeExists := remoteBefore[key]
		if desiredNode == nil {
			if priorNode != nil || !beforeExists || !reflect.DeepEqual(expectedValue, beforeValue) || !reflect.DeepEqual(remoteValue, beforeValue) {
				return false
			}
			continue
		}
		if !credentialValueMatchesExpectedUpdate(remoteValue, beforeValue, beforeExists, expectedValue, priorNode, desiredNode, credentialChildMasking(masked, key, remoteValue)) {
			return false
		}
	}
	return true
}

func credentialValueMatchesExpectedUpdate(remote, remoteBefore interface{}, beforeExists bool, expected interface{}, priorOwnership, desiredOwnership *credentialOwnership, maskMode credentialMaskMode) bool {
	if desiredOwnership == nil {
		return false
	}
	if desiredOwnership.Object {
		remoteObject, remoteOK := remote.(map[string]interface{})
		expectedObject, expectedOK := expected.(map[string]interface{})
		if !remoteOK || !expectedOK {
			return false
		}
		beforeObject, _ := remoteBefore.(map[string]interface{})
		if beforeObject == nil {
			beforeObject = map[string]interface{}{}
		}
		return credentialObjectMatchesExpectedUpdate(remoteObject, beforeObject, expectedObject, priorOwnership, desiredOwnership, maskMode == credentialMaskObject)
	}
	if !desiredOwnership.Atomic {
		return false
	}
	if _, remoteIsObject := remote.(map[string]interface{}); remoteIsObject {
		return false
	}
	if maskMode == credentialMaskScalar {
		remoteText, remoteOK := remote.(string)
		expectedText, expectedOK := expected.(string)
		if remoteOK && expectedOK && (remoteText == expectedText || remoteText == maskLiteLLMCredentialString(expectedText)) {
			return true
		}
	}
	return reflect.DeepEqual(remote, expected)
}

func (r *CredentialResource) fetchCredentialByName(ctx context.Context, name string) (credentialRemote, error) {
	var result credentialAPIResponse
	if err := r.client.doFreshRequestWithResponse(ctx, http.MethodGet, credentialByNamePath(name), nil, &result); err != nil {
		return credentialRemote{}, err
	}
	remote, err := decodeCredentialResponse(result)
	if err != nil {
		return credentialRemote{}, err
	}
	if remote.name != name {
		return credentialRemote{}, errors.New("credential identity mismatch")
	}
	return remote, nil
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

// probeCredentialEndpoint samples a bounded number of fresh connections. A
// transient response never counts as absence and resets the consecutive-404
// streak. This cannot enumerate every worker behind an arbitrary load balancer;
// it is an API-only fail-safe against treating one process-local cache miss as
// authoritative deletion.
func probeCredentialEndpoint(ctx context.Context, client *Client, endpoint, expectedName string) (credentialProbeSample, error) {
	var sample credentialProbeSample
	for attempt := 0; attempt < credentialProbeMaxAttempts; attempt++ {
		var result credentialAPIResponse
		err := client.doFreshRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &result)
		if err == nil {
			remote, decodeErr := decodeCredentialResponse(result)
			if decodeErr != nil {
				return sample, decodeErr
			}
			if expectedName != "" && remote.name != expectedName {
				return sample, errors.New("credential identity mismatch")
			}
			sample.present = append(sample.present, remote)
			sample.consecutiveAbsence = 0
		} else if IsAPIErrorStatus(err, http.StatusNotFound) {
			sample.absent++
			sample.consecutiveAbsence++
		} else if shouldRetryCredentialRecoveryRead(err) {
			sample.transient++
			sample.consecutiveAbsence = 0
			if attempt < credentialProbeMaxAttempts-1 {
				if waitErr := waitCredentialProbeRetry(ctx, attempt); waitErr != nil {
					return sample, waitErr
				}
			}
			continue
		} else {
			return sample, err
		}

		conclusive := len(sample.present) + sample.absent
		if sample.authoritativeAbsence() || (sample.hasPresence() && conclusive >= credentialProbeSampleSize) {
			return sample, nil
		}
	}
	return sample, errCredentialProbeIncomplete
}

func (r *CredentialResource) confirmCredentialCreate(ctx context.Context, name string, verify func(credentialRemote) error) (credentialRemote, credentialProbeSample, error) {
	remote, sample, err := r.confirmCredentialMutation(ctx, name, verify)
	if err != nil {
		return credentialRemote{}, sample, err
	}
	// A create may legitimately be visible in only some process-local caches,
	// but every present representation must be the one just verified. Never
	// select a desired-looking worker while another serves conflicting data.
	for _, candidate := range sample.present {
		if verifyErr := verify(candidate); verifyErr != nil {
			return credentialRemote{}, sample, verifyErr
		}
	}
	if !sample.versionsMatch() {
		return credentialRemote{}, sample, errors.New("sampled workers returned conflicting credential versions after create")
	}
	return remote, sample, nil
}

func (r *CredentialResource) confirmCredentialMutation(ctx context.Context, name string, verify func(credentialRemote) error) (credentialRemote, credentialProbeSample, error) {
	sample, err := probeCredentialEndpoint(ctx, r.client, credentialByNamePath(name), name)
	if err != nil {
		return credentialRemote{}, sample, err
	}
	var matching credentialRemote
	var lastErr error
	matched := false
	for _, remote := range sample.present {
		if verifyErr := verify(remote); verifyErr != nil {
			lastErr = verifyErr
			continue
		}
		if !matched {
			matching = remote
			matched = true
		}
	}
	if matched {
		return matching, sample, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no sampled worker returned the requested credential state")
	}
	return credentialRemote{}, sample, lastErr
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

func (r *CredentialResource) confirmCredentialAbsence(ctx context.Context, name string) (credentialProbeSample, error) {
	sample, err := probeCredentialEndpoint(ctx, r.client, credentialByNamePath(name), name)
	if err != nil {
		return sample, err
	}
	if sample.authoritativeAbsence() {
		return sample, nil
	}
	if sample.hasPresence() {
		return sample, errors.New("credential is still present on at least one sampled worker")
	}
	return sample, errCredentialProbeIncomplete
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

func waitCredentialProbeRetry(ctx context.Context, attempt int) error {
	delay := 100 * time.Millisecond * time.Duration(1<<attempt)
	if delay > 200*time.Millisecond {
		delay = 200 * time.Millisecond
	}
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
	sample, err := probeCredentialEndpoint(ctx, r.client, credentialByNamePath(name), name)
	if err != nil {
		return err
	}
	if sample.authoritativeAbsence() {
		return &APIError{StatusCode: http.StatusNotFound}
	}
	if !sample.hasPresence() || sample.convergenceUncertain() {
		return errCredentialProbeIncomplete
	}
	return reconcileCredentialState(ctx, data, sample.present[0], priorInfo, priorValues, metadata)
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
