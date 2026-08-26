package provider

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	unifiedAccessGroupKeyVerificationMaxAttempts  = 5
	unifiedAccessGroupKeyVerificationInitialDelay = 100 * time.Millisecond
	unifiedAccessGroupKeyVerificationMaxDelay     = 800 * time.Millisecond
)

var (
	errUnifiedAccessGroupKeyInfoContract = errors.New("unexpected LiteLLM key-info response contract")
	errUnifiedAccessGroupKeyNotConverged = errors.New("LiteLLM durable key membership did not converge")
)

var _ resource.Resource = &UnifiedAccessGroupResource{}
var _ resource.ResourceWithImportState = &UnifiedAccessGroupResource{}

func NewUnifiedAccessGroupResource() resource.Resource {
	return &UnifiedAccessGroupResource{}
}

type UnifiedAccessGroupResource struct {
	client *Client

	// Tests can make bounded propagation checks immediate without changing the
	// production retry limit. Zero values select the production defaults.
	keyVerificationMaxAttempts  int
	keyVerificationInitialDelay time.Duration
	keyVerificationMaxDelay     time.Duration
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

type unifiedAccessGroupAssignedKeyIDsValidator struct{}

var _ validator.List = unifiedAccessGroupAssignedKeyIDsValidator{}

func (unifiedAccessGroupAssignedKeyIDsValidator) Description(context.Context) string {
	return "Every value must be a bare or sha256-prefixed 64-hex LiteLLM key management hash."
}

func (v unifiedAccessGroupAssignedKeyIDsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (unifiedAccessGroupAssignedKeyIDsValidator) ValidateList(_ context.Context, req validator.ListRequest, resp *validator.ListResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for _, element := range req.ConfigValue.Elements() {
		stringValue, ok := element.(types.String)
		if !ok || stringValue.IsNull() {
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Invalid Assigned Key Identifier",
				"Each assigned_key_ids value must be a known string containing a bare or sha256-prefixed 64-hex management hash. Raw API keys are not accepted.",
			)
			return
		}
		if stringValue.IsUnknown() {
			continue
		}
		if _, err := unifiedAccessGroupKeyHash(stringValue.ValueString()); err != nil {
			// Deliberately report only the collection path. An element diagnostic
			// could copy a rejected raw token into Terraform output.
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Invalid Assigned Key Identifier",
				"Each assigned_key_ids value must be a bare or sha256-prefixed 64-hex management hash. Raw API keys and malformed values are not accepted.",
			)
			return
		}
	}
}

func (r *UnifiedAccessGroupResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_unified_access_group"
}

func (r *UnifiedAccessGroupResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM unified access group. Unified access groups can grant access to models, MCP servers, and agents and can be assigned to teams or keys.",
		// This resource intentionally remains at version zero. assigned_key_ids is
		// a historical list(string) contract used by indexing, concat, modules,
		// existing state, and imports.
		Version: 0,
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
				Description: "Key SHA256 management identifiers assigned to this access group. Terraform list behavior is preserved, while LiteLLM membership is reconciled as unordered. Values are sent as sorted, deduplicated bare hashes. An explicit empty list detaches every managed key.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Validators: []validator.List{
					unifiedAccessGroupAssignedKeyIDsValidator{},
				},
			},
			"created_at": schema.StringAttribute{Description: "Timestamp when the access group was created.", Computed: true},
			"created_by": schema.StringAttribute{Description: "User who created the access group.", Computed: true},
			"updated_at": schema.StringAttribute{Description: "Timestamp when the access group was last updated.", Computed: true},
			"updated_by": schema.StringAttribute{Description: "User who last updated the access group.", Computed: true},
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

	createRequest, err := buildUnifiedAccessGroupRequest(ctx, &data, false)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Assigned Key Identifiers", "The assigned key identifiers could not be normalized safely.")
		return
	}

	matches, err := r.findUnifiedAccessGroupsByExactNameWithRetry(ctx, data.AccessGroupName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unified Access Group Preflight Error", fmt.Sprintf("Unable to prove that an exact access group does not already exist: %s", err))
		return
	}
	if len(matches) != 0 {
		resp.Diagnostics.AddError("Unified Access Group Already Exists", "An access group with this exact name already exists. Terraform did not adopt or mutate it; import the intended access group by ID.")
		return
	}

	managesKeys := isKnownUnifiedAccessGroupKeyList(data.AssignedKeyIDs)
	keyMutation := managesKeys && len(data.AssignedKeyIDs.Elements()) > 0
	if managesKeys {
		if err := r.preflightUnifiedAccessGroupKeys(ctx, data.AssignedKeyIDs); err != nil {
			resp.Diagnostics.AddError("Assigned Key Verification Failed", preflightUnifiedAccessGroupKeyError(err))
			return
		}
	}

	var result map[string]interface{}
	accepted, mutationErr := r.client.doRequestWithResponse(ctx, http.MethodPost, "/v1/access_group", createRequest, &result)
	responseErr := mutationErr
	if responseErr == nil {
		responseErr = validateUnifiedAccessGroupIdentity(result, "", data.AccessGroupName.ValueString())
	}
	if responseErr != nil {
		if shouldRecoverUnifiedAccessGroupCreate(accepted, mutationErr) {
			r.recoverUnifiedAccessGroupCreate(ctx, &data, keyMutation, accepted, mutationErr, responseErr, resp)
		} else {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create unified access group: %s", responseErr))
		}
		return
	}

	readUnifiedAccessGroupResponse(ctx, result, &data)
	assigned, synchronizationErr := r.synchronizeUnifiedAccessGroupKeys(ctx, data.AccessGroupID.ValueString(), data.AssignedKeyIDs, types.ListNull(types.StringType), result["assigned_key_ids"], true)
	data.AssignedKeyIDs = assigned
	resolveUnifiedAccessGroupUnknowns(&data)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if keyMutation {
		addUnifiedAccessGroupCacheWarning(&resp.Diagnostics)
	}
	if synchronizationErr != nil {
		resp.Diagnostics.AddError("Assigned Key Synchronization Partial", synchronizationErr.Error())
	}
}

func isAmbiguousUnifiedAccessGroupCreateStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout || (statusCode >= http.StatusInternalServerError && statusCode < 600)
}

func shouldRecoverUnifiedAccessGroupCreate(accepted bool, mutationErr error) bool {
	// Any unusable 2xx result is ambiguous: LiteLLM accepted the request even
	// though the provider could not read, decode, or validate its identity.
	if accepted {
		return true
	}
	if mutationErr == nil || errors.Is(mutationErr, context.Canceled) {
		return false
	}

	var apiErr *APIError
	if errors.As(mutationErr, &apiErr) {
		return isAmbiguousUnifiedAccessGroupCreateStatus(apiErr.StatusCode)
	}
	var responseErr *safeResponseError
	if errors.As(mutationErr, &responseErr) {
		return isAmbiguousUnifiedAccessGroupCreateStatus(responseErr.statusCode)
	}
	var transportErr *safeTransportError
	if errors.As(mutationErr, &transportErr) {
		if !transportErr.dispatched {
			return false
		}
		if transportErr.Timeout() || transportErr.Temporary() {
			return true
		}
		// A generic failure after dispatch can be a lost response after commit.
		// Known TLS/protocol failures are terminal and use a distinct safe kind.
		return transportErr.dispatched && transportErr.kind == "LiteLLM HTTP transport request failed"
	}
	return false
}

func (r *UnifiedAccessGroupResource) recoverUnifiedAccessGroupCreate(
	ctx context.Context,
	data *UnifiedAccessGroupResourceModel,
	keyMutation bool,
	accepted bool,
	mutationErr error,
	responseErr error,
	resp *resource.CreateResponse,
) {
	matches, discoveryErr := r.findUnifiedAccessGroupsForCreateRecovery(ctx, data.AccessGroupName.ValueString())
	if discoveryErr != nil {
		detail := "LiteLLM may have committed the create, but exact-name postflight discovery failed. Terraform did not guess an ID or adopt a group. Check LiteLLM and import the owned group before retrying."
		if accepted {
			detail = "LiteLLM accepted the create, but its response was unusable and exact-name postflight discovery failed. Terraform did not guess an ID or adopt a group. Check LiteLLM and import the owned group before retrying."
		}
		resp.Diagnostics.AddError("Unified Access Group Create Outcome Uncertain", detail)
		return
	}
	if len(matches) == 0 {
		detail := "The create outcome was uncertain and bounded exact-name propagation discovery was exhausted without a recoverable group. Terraform did not invent an identity. Inspect LiteLLM for the requested exact name and import any owned group before retrying, so a delayed commit is not orphaned or duplicated."
		if accepted {
			detail = "LiteLLM accepted the create but returned an unusable response, and bounded exact-name propagation discovery was exhausted without a recoverable group. Terraform did not invent an identity. Inspect LiteLLM for the requested exact name and import any owned group before retrying, so the accepted create is not orphaned or duplicated."
		}
		resp.Diagnostics.AddError("Unified Access Group Create Recovery Exhausted", detail)
		return
	}
	if len(matches) != 1 {
		resp.Diagnostics.AddError("Ambiguous Unified Access Group Creation", "Postflight discovery found multiple exact-name groups. Terraform did not adopt any candidate. Resolve the ambiguity and import the intended group by ID.")
		return
	}

	candidate := matches[0]
	matchesConfiguration, matchErr := unifiedAccessGroupCandidateMatchesConfiguration(ctx, candidate, data)
	if matchErr != nil || !matchesConfiguration {
		resp.Diagnostics.AddError("Ambiguous Unified Access Group Creation", "A group with the requested name appeared after the create attempt, but its full identity did not exactly match the requested known fields. Terraform did not adopt a possibly concurrent group.")
		return
	}

	readUnifiedAccessGroupResponse(ctx, candidate, data)
	assigned, synchronizationErr := r.synchronizeUnifiedAccessGroupKeys(ctx, data.AccessGroupID.ValueString(), data.AssignedKeyIDs, types.ListNull(types.StringType), candidate["assigned_key_ids"], true)
	data.AssignedKeyIDs = assigned
	resolveUnifiedAccessGroupUnknowns(data)
	resp.Diagnostics.Append(resp.State.Set(ctx, data)...)
	if keyMutation {
		addUnifiedAccessGroupCacheWarning(&resp.Diagnostics)
	}

	// Recovery is never silent. A response failure cannot distinguish the
	// provider's commit from a perfectly concurrent identical create, so retain
	// the uniquely recoverable identity for safe read/update/destroy while
	// requiring an operator to review the explicit diagnostic.
	detail := "A unique exact-name, exact-configuration postflight candidate was found and retained in partial state so retry or destroy is safe. LiteLLM did not provide a usable create response, so Terraform cannot prove the candidate's operation identity; review ownership before continuing."
	if synchronizationErr != nil {
		detail += " Two-sided durable assignment discovery was partial: " + synchronizationErr.Error()
	}
	if mutationErr == nil && responseErr != nil {
		detail = "LiteLLM returned HTTP success with a malformed or missing access-group identity. " + detail
	}
	resp.Diagnostics.AddError("Unified Access Group Create Recovered With Uncertain Ownership", detail)
}

func (r *UnifiedAccessGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data UnifiedAccessGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.readUnifiedAccessGroup(ctx, &data)
	if err != nil {
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		var synchronizationErr *unifiedAccessGroupSynchronizationError
		if errors.As(err, &synchronizationErr) {
			resolveUnifiedAccessGroupUnknowns(&data)
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			resp.Diagnostics.AddError("Assigned Key Synchronization Partial", synchronizationErr.Error())
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

	updateRequest, err := buildUnifiedAccessGroupRequest(ctx, &data, true)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Assigned Key Identifiers", "The assigned key identifiers could not be normalized safely.")
		return
	}
	managesKeys := isKnownUnifiedAccessGroupKeyList(data.AssignedKeyIDs)
	keyMutation := managesKeys && unifiedAccessGroupKeyMembershipsDiffer(state.AssignedKeyIDs, data.AssignedKeyIDs)
	if managesKeys {
		if err := r.preflightUnifiedAccessGroupKeys(ctx, data.AssignedKeyIDs); err != nil {
			resp.Diagnostics.AddError("Assigned Key Verification Failed", preflightUnifiedAccessGroupKeyError(err))
			return
		}
	}

	accessGroupID := data.AccessGroupID.ValueString()
	endpoint := endpointWithPathSegment("/v1/access_group/", accessGroupID, "")
	desiredByHash, _ := unifiedAccessGroupKeyRepresentations(data.AssignedKeyIDs)
	var snapshot *unifiedAccessGroupMembershipSnapshot
	if managesKeys {
		var current map[string]interface{}
		if err := r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &current); err != nil || validateUnifiedAccessGroupIdentity(current, accessGroupID, "") != nil {
			r.recoverUnifiedAccessGroupUpdate(ctx, state, keyMutation, resp, "Terraform could not establish the current two-sided key membership before mutation. The access group was not changed.")
			return
		}
		snapshot, err = r.inspectUnifiedAccessGroupMembership(ctx, accessGroupID, current["assigned_key_ids"], data.AssignedKeyIDs, state.AssignedKeyIDs)
		if err != nil {
			r.recoverUnifiedAccessGroupUpdate(ctx, state, keyMutation, resp, "Terraform could not establish a complete two-sided membership snapshot before mutation. The access group was not changed.")
			return
		}
		keyMutation = keyMutation || len(snapshot.groupOnly) > 0 || len(snapshot.keyOnly) > 0

		// LiteLLM v1.98 computes key changes from the access-group row delta.
		// A desired key already present only on that row produces no add delta,
		// so remove only those one-sided hashes first, prove the partial state,
		// and let the normal desired update re-add them.
		groupOnlyAttach := make(map[string]bool)
		for _, hash := range snapshot.groupOnly {
			if _, wanted := desiredByHash[hash]; wanted {
				groupOnlyAttach[hash] = true
			}
		}
		if len(groupOnlyAttach) > 0 {
			// Refresh immediately before the bounded reset so every other current
			// group assignment is copied forward, including assignments that
			// appeared while the two-sided snapshot was being inspected.
			var resetCurrent map[string]interface{}
			if err := r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &resetCurrent); err != nil || validateUnifiedAccessGroupIdentity(resetCurrent, accessGroupID, "") != nil {
				r.recoverUnifiedAccessGroupUpdate(ctx, state, false, resp, "Terraform could not refresh the access-group row before the bounded one-sided attach repair. The access group was not changed.")
				return
			}
			freshAssignments, invalid, parseErr := rawUnifiedAccessGroupAssignedKeyRepresentations(resetCurrent["assigned_key_ids"])
			if parseErr != nil || invalid != 0 {
				r.recoverUnifiedAccessGroupUpdate(ctx, state, false, resp, "Terraform could not safely decode the refreshed access-group row before the bounded one-sided attach repair. The access group was not changed.")
				return
			}
			stillGroupOnly := make(map[string]bool)
			for hash := range groupOnlyAttach {
				if _, stillOnGroup := freshAssignments[hash]; !stillOnGroup {
					continue
				}
				groups, verifyErr := r.readUnifiedAccessGroupKeyMembershipWithRetry(ctx, hash)
				if verifyErr != nil {
					r.recoverUnifiedAccessGroupUpdate(ctx, state, false, resp, "Terraform could not refresh the key row before the bounded one-sided attach repair. The access group was not changed.")
					return
				}
				if !containsExactString(groups, accessGroupID) {
					stillGroupOnly[hash] = true
				}
			}
			if len(stillGroupOnly) > 0 {
				temporary := make([]string, 0, len(freshAssignments)-len(stillGroupOnly))
				for hash := range freshAssignments {
					if !stillGroupOnly[hash] {
						temporary = append(temporary, hash)
					}
				}
				sort.Strings(temporary)
				var resetResult map[string]interface{}
				accepted, resetErr := r.client.doRequestWithResponse(ctx, http.MethodPut, endpoint, map[string]interface{}{"assigned_key_ids": temporary}, &resetResult)
				if resetErr == nil {
					resetErr = validateUnifiedAccessGroupIdentity(resetResult, accessGroupID, "")
				}
				resetAssignments, resetInvalid, resetParseErr := rawUnifiedAccessGroupAssignedKeyRepresentations(resetResult["assigned_key_ids"])
				if resetErr != nil || resetParseErr != nil || resetInvalid != 0 || !unifiedAccessGroupHashSetsEqual(unifiedAccessGroupHashSet(temporary), func() map[string]bool {
					observed := make(map[string]bool, len(resetAssignments))
					for hash := range resetAssignments {
						observed[hash] = true
					}
					return observed
				}()) {
					detail := "LiteLLM did not confirm the bounded remove step needed to repair a group-only desired attachment. Terraform stopped before re-adding it and read back the two durable rows."
					if accepted {
						detail = "LiteLLM accepted the bounded remove step but did not confirm its exact partial result. Terraform stopped before re-adding it and read back the two durable rows."
					}
					r.recoverUnifiedAccessGroupUpdate(ctx, state, true, resp, detail)
					return
				}
				for hash := range stillGroupOnly {
					groups, verifyErr := r.readUnifiedAccessGroupKeyMembershipWithRetry(ctx, hash)
					if verifyErr != nil || containsExactString(groups, accessGroupID) {
						r.recoverUnifiedAccessGroupUpdate(ctx, state, true, resp, "The bounded remove step was not confirmed detached on both durable rows. Terraform stopped before re-adding it.")
						return
					}
				}
			}
		}
	}

	var result map[string]interface{}
	accepted, mutationErr := r.client.doRequestWithResponse(ctx, http.MethodPut, endpoint, updateRequest, &result)
	responseErr := mutationErr
	if responseErr == nil {
		responseErr = validateUnifiedAccessGroupIdentity(result, accessGroupID, data.AccessGroupName.ValueString())
	}
	if responseErr != nil {
		detail := "LiteLLM did not return a usable update response. Terraform retained the resource identity and read back both durable membership rows; retry is safe."
		if accepted {
			detail = "LiteLLM accepted the update but did not return a usable response. Terraform retained the resource identity and read back both durable membership rows; retry or destroy is safe."
		}
		r.recoverUnifiedAccessGroupUpdate(ctx, state, keyMutation, resp, detail)
		return
	}

	// A key-only desired detach creates no v1.98 access-group delta. Never add
	// the group side temporarily: that could create authorization. Instead use
	// the public patch-style /key/update contract with the hash identity and a
	// fresh complete access_group_ids list, removing only this group.
	if managesKeys && snapshot != nil {
		for _, hash := range snapshot.keyOnly {
			if _, wanted := desiredByHash[hash]; wanted {
				continue
			}
			groups, readErr := r.readUnifiedAccessGroupKeyMembershipWithRetry(ctx, hash)
			if readErr != nil {
				r.recoverUnifiedAccessGroupUpdate(ctx, state, true, resp, "A key-only desired detach could not safely read the key's complete access-group list. Terraform did not use an authorization-broadening fallback.")
				return
			}
			if !containsExactString(groups, accessGroupID) {
				continue
			}
			remaining := removeExactString(groups, accessGroupID)
			keyUpdateErr := r.client.DoRequestWithResponse(ctx, http.MethodPost, "/key/update", map[string]interface{}{
				"key":              hash,
				"access_group_ids": remaining,
			}, nil)
			observedGroups, verifyErr := r.readUnifiedAccessGroupKeyMembershipWithRetry(ctx, hash)
			if verifyErr != nil || !stringMembershipEqual(observedGroups, remaining) {
				detail := "LiteLLM v1.98 did not confirm an exact hash-identified /key/update detach while preserving every unrelated key access group. Terraform did not temporarily add the access-group side or broaden authorization; repair the one-sided key row and retry."
				if keyUpdateErr == nil {
					detail = "LiteLLM accepted the exact key-side detach but did not confirm that every unrelated key access group was preserved. Terraform did not attempt a broader recovery mutation."
				}
				r.recoverUnifiedAccessGroupUpdate(ctx, state, true, resp, detail)
				return
			}
			if keyUpdateErr != nil {
				resp.Diagnostics.AddWarning("Key-Side Detach Response Recovered", "The /key/update response failed, but a bounded key-info read proved the exact intended access-group list without dropping unrelated groups.")
			}
		}
	}

	readUnifiedAccessGroupResponse(ctx, result, &data)
	assigned, synchronizationErr := r.synchronizeUnifiedAccessGroupKeys(ctx, accessGroupID, data.AssignedKeyIDs, state.AssignedKeyIDs, result["assigned_key_ids"], true)
	data.AssignedKeyIDs = assigned
	resolveUnifiedAccessGroupUnknowns(&data)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if keyMutation {
		addUnifiedAccessGroupCacheWarning(&resp.Diagnostics)
	}
	if synchronizationErr != nil {
		resp.Diagnostics.AddError("Assigned Key Synchronization Partial", synchronizationErr.Error())
	}
}

func (r *UnifiedAccessGroupResource) recoverUnifiedAccessGroupUpdate(
	ctx context.Context,
	state UnifiedAccessGroupResourceModel,
	keyMutation bool,
	resp *resource.UpdateResponse,
	detail string,
) {
	observed := state
	readErr := r.readUnifiedAccessGroup(ctx, &observed)
	var synchronizationErr *unifiedAccessGroupSynchronizationError
	partialRead := errors.As(readErr, &synchronizationErr)
	if readErr != nil && !partialRead {
		// Membership that cannot be proven from both rows must not survive an
		// ambiguous multi-step outcome merely because it was in prior state.
		observed.AssignedKeyIDs = unifiedAccessGroupKeyList(nil)
	}
	resolveUnifiedAccessGroupUnknowns(&observed)
	resp.Diagnostics.Append(resp.State.Set(ctx, &observed)...)
	if keyMutation {
		addUnifiedAccessGroupCacheWarning(&resp.Diagnostics)
	}
	if partialRead {
		detail += " Two-sided read-back reported: " + synchronizationErr.Error()
	} else if readErr != nil {
		detail += " Two-sided read-back was unavailable; no key membership was retained without proof."
	}
	resp.Diagnostics.AddError("Unified Access Group Update Recovery Required", detail)
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
	endpoint := endpointWithPathSegment("/v1/access_group/", id, "")
	if err := r.client.DoRequestWithResponse(ctx, http.MethodDelete, endpoint, nil, nil); err != nil && !IsNotFoundError(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete unified access group: %s", err))
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
	endpoint := endpointWithPathSegment("/v1/access_group/", id, "")
	var result map[string]interface{}
	if err := r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &result); err != nil {
		return err
	}
	if err := validateUnifiedAccessGroupIdentity(result, id, ""); err != nil {
		return err
	}

	priorAssigned := data.AssignedKeyIDs
	readUnifiedAccessGroupResponse(ctx, result, data)
	assigned, err := r.synchronizeUnifiedAccessGroupKeys(ctx, id, types.ListNull(types.StringType), priorAssigned, result["assigned_key_ids"], false)
	data.AssignedKeyIDs = assigned
	resolveUnifiedAccessGroupUnknowns(data)
	return err
}

func buildUnifiedAccessGroupRequest(ctx context.Context, data *UnifiedAccessGroupResourceModel, includeOptionalName bool) (map[string]interface{}, error) {
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
	if err := addAssignedKeyListToRequest(req, data.AssignedKeyIDs); err != nil {
		return nil, err
	}
	return req, nil
}

func addStringListToRequest(ctx context.Context, req map[string]interface{}, key string, value types.List) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	var values []string
	value.ElementsAs(ctx, &values, false)
	req[key] = values
}

func addAssignedKeyListToRequest(req map[string]interface{}, value types.List) error {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	representations, err := unifiedAccessGroupKeyRepresentations(value)
	if err != nil {
		return err
	}
	hashes := sortedUnifiedAccessGroupKeyHashes(representations)
	req["assigned_key_ids"] = hashes
	return nil
}

func unifiedAccessGroupKeyHash(value string) (string, error) {
	hash := value
	if len(hash) >= len("sha256:") && strings.EqualFold(hash[:len("sha256:")], "sha256:") {
		hash = hash[len("sha256:"):]
	}
	if len(hash) != sha256ManagementHashLength {
		return "", errors.New("invalid SHA256 management hash length")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return "", errors.New("invalid SHA256 management hash")
	}
	return strings.ToLower(hash), nil
}

func unifiedAccessGroupKeyRepresentations(value types.List) (map[string][]string, error) {
	result := make(map[string][]string)
	if value.IsNull() || value.IsUnknown() {
		return result, nil
	}
	for _, element := range value.Elements() {
		stringValue, ok := element.(types.String)
		if !ok || stringValue.IsNull() || stringValue.IsUnknown() {
			return nil, errors.New("assigned key identifier is not a known string")
		}
		representation := stringValue.ValueString()
		hash, err := unifiedAccessGroupKeyHash(representation)
		if err != nil {
			return nil, err
		}
		result[hash] = append(result[hash], representation)
	}
	return result, nil
}

func unifiedAccessGroupValidKeyRepresentations(value types.List) (map[string][]string, int) {
	result := make(map[string][]string)
	invalid := 0
	if value.IsNull() || value.IsUnknown() {
		return result, invalid
	}
	for _, element := range value.Elements() {
		stringValue, ok := element.(types.String)
		if !ok || stringValue.IsNull() || stringValue.IsUnknown() {
			invalid++
			continue
		}
		representation := stringValue.ValueString()
		hash, err := unifiedAccessGroupKeyHash(representation)
		if err != nil {
			invalid++
			continue
		}
		result[hash] = append(result[hash], representation)
	}
	return result, invalid
}

func sortedUnifiedAccessGroupKeyHashes(representations map[string][]string) []string {
	hashes := make([]string, 0, len(representations))
	for hash := range representations {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	return hashes
}

func unifiedAccessGroupKeyList(representations []string) types.List {
	items := make([]attr.Value, 0, len(representations))
	for _, representation := range representations {
		items = append(items, types.StringValue(representation))
	}
	return types.ListValueMust(types.StringType, items)
}

func unifiedAccessGroupKeyMembershipEqual(representations map[string][]string, actual map[string]bool) bool {
	if len(representations) != len(actual) {
		return false
	}
	for hash := range representations {
		if !actual[hash] {
			return false
		}
	}
	return true
}

func reconcileUnifiedAccessGroupKeyMembership(primary, secondary types.List, actual map[string]bool) types.List {
	if primaryRepresentations, invalid := unifiedAccessGroupValidKeyRepresentations(primary); isKnownUnifiedAccessGroupKeyList(primary) && invalid == 0 && unifiedAccessGroupKeyMembershipEqual(primaryRepresentations, actual) {
		return primary
	}
	if secondaryRepresentations, invalid := unifiedAccessGroupValidKeyRepresentations(secondary); isKnownUnifiedAccessGroupKeyList(secondary) && invalid == 0 && unifiedAccessGroupKeyMembershipEqual(secondaryRepresentations, actual) {
		return secondary
	}
	hashes := make([]string, 0, len(actual))
	for hash, attached := range actual {
		if attached {
			hashes = append(hashes, hash)
		}
	}
	sort.Strings(hashes)
	return unifiedAccessGroupKeyList(hashes)
}

func isKnownUnifiedAccessGroupKeyList(value types.List) bool {
	return !value.IsNull() && !value.IsUnknown()
}

func unifiedAccessGroupKeyMembershipsDiffer(left, right types.List) bool {
	leftRepresentations, leftInvalid := unifiedAccessGroupValidKeyRepresentations(left)
	rightRepresentations, rightInvalid := unifiedAccessGroupValidKeyRepresentations(right)
	if leftInvalid != 0 || rightInvalid != 0 || left.IsNull() || left.IsUnknown() || right.IsNull() || right.IsUnknown() {
		return true
	}
	actual := make(map[string]bool, len(rightRepresentations))
	for hash := range rightRepresentations {
		actual[hash] = true
	}
	return !unifiedAccessGroupKeyMembershipEqual(leftRepresentations, actual)
}

func (r *UnifiedAccessGroupResource) preflightUnifiedAccessGroupKeys(ctx context.Context, desired types.List) error {
	representations, err := unifiedAccessGroupKeyRepresentations(desired)
	if err != nil {
		return errUnifiedAccessGroupKeyInfoContract
	}
	verificationErr := &unifiedAccessGroupPreflightError{}
	for _, hash := range sortedUnifiedAccessGroupKeyHashes(representations) {
		if _, err := r.readUnifiedAccessGroupKeyMembershipWithRetry(ctx, hash); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			switch {
			case IsAPIErrorStatus(err, http.StatusNotFound):
				verificationErr.missing++
			default:
				verificationErr.unreadable++
			}
		}
	}
	if verificationErr.missing == 0 && verificationErr.unreadable == 0 {
		return nil
	}
	return verificationErr
}

type unifiedAccessGroupPreflightError struct {
	missing    int
	unreadable int
}

func (e *unifiedAccessGroupPreflightError) Error() string { return "assigned key preflight failed" }

func preflightUnifiedAccessGroupKeyError(err error) string {
	var verificationErr *unifiedAccessGroupPreflightError
	if errors.As(err, &verificationErr) {
		switch {
		case verificationErr.missing > 0 && verificationErr.unreadable > 0:
			return fmt.Sprintf("LiteLLM could not authoritatively resolve %d assigned key(s), and %d additional durable key lookup(s) failed. The access group was not changed.", verificationErr.missing, verificationErr.unreadable)
		case verificationErr.missing > 0:
			return fmt.Sprintf("LiteLLM could not authoritatively resolve %d assigned key(s). The access group was not changed.", verificationErr.missing)
		default:
			return fmt.Sprintf("LiteLLM durable key-info verification failed for %d assigned key(s). The access group was not changed.", verificationErr.unreadable)
		}
	}
	return "The assigned key identifiers could not be verified safely. The access group was not changed."
}

type unifiedAccessGroupKeyInfoResponse struct {
	Key  json.RawMessage `json:"key"`
	Info *struct {
		AccessGroupIDs json.RawMessage `json:"access_group_ids"`
	} `json:"info"`
}

func (r *UnifiedAccessGroupResource) readUnifiedAccessGroupKeyMembership(ctx context.Context, bareHash string) ([]string, error) {
	query := url.Values{}
	query.Set("key", bareHash)
	endpoint := endpointWithQuery("/key/info", query)
	var response unifiedAccessGroupKeyInfoResponse
	if err := r.client.DoRequestWithResponse(ctx, http.MethodGet, endpoint, nil, &response); err != nil {
		return nil, err
	}
	if len(response.Key) == 0 || response.Info == nil || len(response.Info.AccessGroupIDs) == 0 {
		return nil, errUnifiedAccessGroupKeyInfoContract
	}
	var echoedKey string
	if err := json.Unmarshal(response.Key, &echoedKey); err != nil {
		return nil, errUnifiedAccessGroupKeyInfoContract
	}
	echoedHash, err := unifiedAccessGroupKeyHash(echoedKey)
	if err != nil || echoedHash != bareHash {
		return nil, errUnifiedAccessGroupKeyInfoContract
	}
	if string(response.Info.AccessGroupIDs) == "null" {
		return []string{}, nil
	}
	var accessGroupIDs []string
	if err := json.Unmarshal(response.Info.AccessGroupIDs, &accessGroupIDs); err != nil || accessGroupIDs == nil {
		return nil, errUnifiedAccessGroupKeyInfoContract
	}
	return accessGroupIDs, nil
}

func containsExactString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func (r *UnifiedAccessGroupResource) verificationSettings() (int, time.Duration, time.Duration) {
	attempts := r.keyVerificationMaxAttempts
	if attempts <= 0 {
		attempts = unifiedAccessGroupKeyVerificationMaxAttempts
	}
	initialDelay := r.keyVerificationInitialDelay
	maxDelay := r.keyVerificationMaxDelay
	if initialDelay == 0 && r.keyVerificationMaxAttempts == 0 {
		initialDelay = unifiedAccessGroupKeyVerificationInitialDelay
	}
	if maxDelay == 0 && r.keyVerificationMaxAttempts == 0 {
		maxDelay = unifiedAccessGroupKeyVerificationMaxDelay
	}
	if maxDelay > 0 && initialDelay > maxDelay {
		initialDelay = maxDelay
	}
	return attempts, initialDelay, maxDelay
}

func waitForUnifiedAccessGroupVerification(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextUnifiedAccessGroupVerificationDelay(delay, maxDelay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	next := delay * 2
	if maxDelay > 0 && next > maxDelay {
		return maxDelay
	}
	return next
}

func shouldRetryUnifiedAccessGroupVerification(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var transportErr *safeTransportError
	if errors.As(err, &transportErr) {
		return transportErr.Timeout() || transportErr.Temporary()
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusRequestTimeout || apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}
	var responseErr *safeResponseError
	if errors.As(err, &responseErr) {
		return responseErr.Temporary()
	}
	return false
}

func (r *UnifiedAccessGroupResource) readUnifiedAccessGroupKeyMembershipWithRetry(ctx context.Context, bareHash string) ([]string, error) {
	attempts, delay, maxDelay := r.verificationSettings()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		groups, err := r.readUnifiedAccessGroupKeyMembership(ctx, bareHash)
		if err == nil {
			return groups, nil
		}
		lastErr = err
		if !shouldRetryUnifiedAccessGroupVerification(err) || attempt == attempts {
			return nil, err
		}
		if err := waitForUnifiedAccessGroupVerification(ctx, delay); err != nil {
			return nil, err
		}
		delay = nextUnifiedAccessGroupVerificationDelay(delay, maxDelay)
	}
	return nil, lastErr
}

func (r *UnifiedAccessGroupResource) verifyUnifiedAccessGroupMembership(ctx context.Context, bareHash, accessGroupID string, wantAttached bool) (bool, bool, error) {
	attempts, delay, maxDelay := r.verificationSettings()
	var lastAttached bool
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return false, false, err
		}
		groups, err := r.readUnifiedAccessGroupKeyMembership(ctx, bareHash)
		if err == nil {
			lastAttached = containsExactString(groups, accessGroupID)
			if lastAttached == wantAttached {
				return lastAttached, true, nil
			}
			if attempt == attempts {
				return lastAttached, true, errUnifiedAccessGroupKeyNotConverged
			}
		} else if !shouldRetryUnifiedAccessGroupVerification(err) || attempt == attempts {
			return false, false, err
		}
		if err := waitForUnifiedAccessGroupVerification(ctx, delay); err != nil {
			return false, false, err
		}
		delay = nextUnifiedAccessGroupVerificationDelay(delay, maxDelay)
	}
	return lastAttached, true, errUnifiedAccessGroupKeyNotConverged
}

type unifiedAccessGroupSynchronizationError struct {
	missing      int
	notConverged int
	unreadable   int
	ambiguous    int
	unsafeEchoes int
	discovery    int
}

func (e *unifiedAccessGroupSynchronizationError) hasError() bool {
	return e.missing+e.notConverged+e.unreadable+e.ambiguous+e.unsafeEchoes+e.discovery > 0
}

func (e *unifiedAccessGroupSynchronizationError) Error() string {
	parts := make([]string, 0, 6)
	if e.missing > 0 {
		parts = append(parts, fmt.Sprintf("%d desired key(s) were missing", e.missing))
	}
	if e.notConverged > 0 {
		parts = append(parts, fmt.Sprintf("%d durable membership change(s) did not converge after bounded verification", e.notConverged))
	}
	if e.unreadable > 0 {
		parts = append(parts, fmt.Sprintf("%d key membership(s) could not be verified", e.unreadable))
	}
	if e.ambiguous > 0 {
		parts = append(parts, fmt.Sprintf("%d one-sided assignment(s) disagreed between the access-group row and key row", e.ambiguous))
	}
	if e.unsafeEchoes > 0 {
		parts = append(parts, fmt.Sprintf("%d unsafe or malformed access-group key value(s) were ignored", e.unsafeEchoes))
	}
	if e.discovery > 0 {
		parts = append(parts, "global key-side assignment discovery was incomplete")
	}
	return "Durable LiteLLM assignment synchronization was partial: " + strings.Join(parts, "; ") + ". Terraform retained only the intersection confirmed by both the access-group row and each key row; raw, suffix, and one-sided identifiers were never absorbed."
}

type unifiedAccessGroupKeyCandidate struct {
	desired bool
	prior   bool
	group   bool
	global  bool
}

func rawUnifiedAccessGroupAssignedKeyRepresentations(raw interface{}) (map[string][]string, int, error) {
	result := make(map[string][]string)
	if raw == nil {
		return result, 0, errUnifiedAccessGroupKeyInfoContract
	}
	var values []interface{}
	switch typed := raw.(type) {
	case []interface{}:
		values = typed
	case []string:
		values = make([]interface{}, len(typed))
		for index := range typed {
			values[index] = typed[index]
		}
	default:
		return result, 0, errUnifiedAccessGroupKeyInfoContract
	}
	invalid := 0
	for _, rawValue := range values {
		value, ok := rawValue.(string)
		if !ok {
			invalid++
			continue
		}
		hash, err := unifiedAccessGroupKeyHash(value)
		if err != nil {
			invalid++
			continue
		}
		result[hash] = append(result[hash], value)
	}
	return result, invalid, nil
}

type unifiedAccessGroupDiscoveredKey struct {
	Hash string
}

func (item unifiedAccessGroupDiscoveredKey) listItemIdentity() string { return item.Hash }

func listUnifiedAccessGroupKeys(ctx context.Context, client *Client, accessGroupID string) ([]string, error) {
	items, err := collectNumberedPages(ctx, "/key/list", func(ctx context.Context, page int) (numberedListPage[unifiedAccessGroupDiscoveredKey], error) {
		query := url.Values{}
		query.Set("access_group_id", accessGroupID)
		query.Set("page", fmt.Sprintf("%d", page))
		query.Set("size", "100")
		query.Set("return_full_object", "true")
		query.Set("sort_by", "token")
		query.Set("sort_order", "asc")

		var wire keyListWirePage
		if err := client.DoRequestWithResponse(ctx, http.MethodGet, endpointWithQuery("/key/list", query), nil, &wire); err != nil {
			return numberedListPage[unifiedAccessGroupDiscoveredKey]{}, err
		}
		if wire.TotalCount == nil || wire.CurrentPage == nil || wire.TotalPages == nil {
			return numberedListPage[unifiedAccessGroupDiscoveredKey]{}, fmt.Errorf("/key/list response omitted required pagination metadata")
		}
		rawItems, err := decodeNamedList(wire.Keys, "/key/list", "keys")
		if err != nil {
			return numberedListPage[unifiedAccessGroupDiscoveredKey]{}, err
		}
		pageItems := make([]unifiedAccessGroupDiscoveredKey, 0, len(rawItems))
		for _, rawItem := range rawItems {
			object, err := decodeListObject(rawItem, "/key/list", "full key item")
			if err != nil {
				return numberedListPage[unifiedAccessGroupDiscoveredKey]{}, err
			}
			token, ok := object["token"].(string)
			hash, valid := canonicalSHA256ManagementHash(token)
			if !ok || !valid {
				return numberedListPage[unifiedAccessGroupDiscoveredKey]{}, fmt.Errorf("/key/list returned a full key object without a valid token management hash")
			}
			pageItems = append(pageItems, unifiedAccessGroupDiscoveredKey{Hash: hash})
		}
		return numberedListPage[unifiedAccessGroupDiscoveredKey]{
			Items:      pageItems,
			Number:     *wire.CurrentPage,
			TotalPages: *wire.TotalPages,
			TotalCount: *wire.TotalCount,
		}, nil
	})
	if err != nil {
		return nil, err
	}
	hashes := make([]string, 0, len(items))
	for _, item := range items {
		hashes = append(hashes, item.Hash)
	}
	sort.Strings(hashes)
	return hashes, nil
}

func (r *UnifiedAccessGroupResource) listUnifiedAccessGroupKeysWithRetry(ctx context.Context, accessGroupID string) ([]string, error) {
	attempts, delay, maxDelay := r.verificationSettings()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		hashes, err := listUnifiedAccessGroupKeys(ctx, r.client, accessGroupID)
		if err == nil {
			return hashes, nil
		}
		lastErr = err
		if !shouldRetryUnifiedAccessGroupVerification(err) || attempt == attempts {
			return nil, err
		}
		if err := waitForUnifiedAccessGroupVerification(ctx, delay); err != nil {
			return nil, err
		}
		delay = nextUnifiedAccessGroupVerificationDelay(delay, maxDelay)
	}
	return nil, lastErr
}

type unifiedAccessGroupMembershipSnapshot struct {
	groupOnly []string
	keyOnly   []string
}

// inspectUnifiedAccessGroupMembership reads both durable rows. The filtered
// key list is candidate discovery only: /key/info proves the key row, while
// assigned_key_ids proves the access-group row.
func (r *UnifiedAccessGroupResource) inspectUnifiedAccessGroupMembership(
	ctx context.Context,
	accessGroupID string,
	rawGroupAssignments interface{},
	extraLists ...types.List,
) (*unifiedAccessGroupMembershipSnapshot, error) {
	groupByHash, invalid, err := rawUnifiedAccessGroupAssignedKeyRepresentations(rawGroupAssignments)
	if err != nil || invalid != 0 {
		return nil, errors.New("the access-group row did not contain a complete safe assigned-key list")
	}
	globalHashes, err := r.listUnifiedAccessGroupKeysWithRetry(ctx, accessGroupID)
	if err != nil {
		return nil, fmt.Errorf("key-side assignment discovery failed: %w", err)
	}

	candidates := make(map[string]bool)
	group := make(map[string]bool, len(groupByHash))
	for hash := range groupByHash {
		group[hash] = true
		candidates[hash] = true
	}
	for _, hash := range globalHashes {
		candidates[hash] = true
	}
	for _, list := range extraLists {
		representations, listErr := unifiedAccessGroupKeyRepresentations(list)
		if listErr != nil {
			return nil, errors.New("configured or prior assigned-key membership was not safely decodable")
		}
		for hash := range representations {
			candidates[hash] = true
		}
	}

	snapshot := &unifiedAccessGroupMembershipSnapshot{}
	hashes := make([]string, 0, len(candidates))
	for hash := range candidates {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	for _, hash := range hashes {
		groups, keyErr := r.readUnifiedAccessGroupKeyMembershipWithRetry(ctx, hash)
		if keyErr != nil {
			if IsAPIErrorStatus(keyErr, http.StatusNotFound) {
				groups = []string{}
			} else {
				return snapshot, fmt.Errorf("a key row could not be read safely: %w", keyErr)
			}
		}
		keyAttached := containsExactString(groups, accessGroupID)
		switch {
		case group[hash] && keyAttached:
			// Confirmed intersection; no repair required.
		case group[hash]:
			snapshot.groupOnly = append(snapshot.groupOnly, hash)
		case keyAttached:
			snapshot.keyOnly = append(snapshot.keyOnly, hash)
		}
	}
	return snapshot, nil
}

func unifiedAccessGroupHashSet(values []string) map[string]bool {
	result := make(map[string]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func unifiedAccessGroupHashSetsEqual(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if !right[value] {
			return false
		}
	}
	return true
}

func stringMembershipEqual(left, right []string) bool {
	leftSet := make(map[string]bool, len(left))
	rightSet := make(map[string]bool, len(right))
	for _, value := range left {
		leftSet[value] = true
	}
	for _, value := range right {
		rightSet[value] = true
	}
	return unifiedAccessGroupHashSetsEqual(leftSet, rightSet)
}

func removeExactString(values []string, removed string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != removed {
			result = append(result, value)
		}
	}
	return result
}

func (r *UnifiedAccessGroupResource) synchronizeUnifiedAccessGroupKeys(
	ctx context.Context,
	accessGroupID string,
	desired types.List,
	prior types.List,
	rawGroupAssignments interface{},
	mutation bool,
) (types.List, error) {
	desiredByHash, desiredInvalid := unifiedAccessGroupValidKeyRepresentations(desired)
	priorByHash, priorInvalid := unifiedAccessGroupValidKeyRepresentations(prior)
	groupByHash, unsafeEchoes, groupErr := rawUnifiedAccessGroupAssignedKeyRepresentations(rawGroupAssignments)
	groupObserved := groupErr == nil

	synchronizationErr := &unifiedAccessGroupSynchronizationError{unsafeEchoes: unsafeEchoes + desiredInvalid + priorInvalid}
	if !groupObserved {
		synchronizationErr.discovery++
		groupByHash = map[string][]string{}
	}
	globalHashes, globalErr := r.listUnifiedAccessGroupKeysWithRetry(ctx, accessGroupID)
	if globalErr != nil {
		if ctx.Err() != nil {
			return reconcileUnifiedAccessGroupKeyMembership(desired, prior, map[string]bool{}), ctx.Err()
		}
		synchronizationErr.discovery++
	}

	candidates := make(map[string]*unifiedAccessGroupKeyCandidate)
	candidate := func(hash string) *unifiedAccessGroupKeyCandidate {
		if candidates[hash] == nil {
			candidates[hash] = &unifiedAccessGroupKeyCandidate{}
		}
		return candidates[hash]
	}
	for hash := range desiredByHash {
		candidate(hash).desired = true
	}
	for hash := range priorByHash {
		candidate(hash).prior = true
	}
	for hash := range groupByHash {
		candidate(hash).group = true
	}
	for _, hash := range globalHashes {
		candidate(hash).global = true
	}

	actual := make(map[string]bool)
	hashes := make([]string, 0, len(candidates))
	for hash := range candidates {
		hashes = append(hashes, hash)
	}
	sort.Strings(hashes)
	managesDesired := mutation && isKnownUnifiedAccessGroupKeyList(desired)
	for _, hash := range hashes {
		sources := candidates[hash]
		var keyAttached, keyObserved bool
		var err error
		if managesDesired {
			_, wantAttached := desiredByHash[hash]
			keyAttached, keyObserved, err = r.verifyUnifiedAccessGroupMembership(ctx, hash, accessGroupID, wantAttached)
		} else {
			var groups []string
			groups, err = r.readUnifiedAccessGroupKeyMembershipWithRetry(ctx, hash)
			if err == nil {
				keyObserved = true
				keyAttached = containsExactString(groups, accessGroupID)
			}
		}
		if ctx.Err() != nil {
			return reconcileUnifiedAccessGroupKeyMembership(desired, prior, actual), ctx.Err()
		}
		if err != nil {
			switch {
			case IsAPIErrorStatus(err, http.StatusNotFound):
				keyObserved = true
				keyAttached = false
				if sources.desired && managesDesired {
					synchronizationErr.missing++
				}
			case errors.Is(err, errUnifiedAccessGroupKeyNotConverged):
				synchronizationErr.notConverged++
			default:
				synchronizationErr.unreadable++
			}
		}

		// Neither /key/list?access_group_id nor /key/info proves the
		// access-group row. Membership is converged only at the intersection
		// of that row and the key row.
		if groupObserved && keyObserved {
			if sources.group && keyAttached {
				actual[hash] = true
			}
			if sources.group != keyAttached {
				synchronizationErr.ambiguous++
			}
		}
	}

	if managesDesired && !unifiedAccessGroupKeyMembershipEqual(desiredByHash, actual) {
		difference := 0
		for hash := range desiredByHash {
			if !actual[hash] {
				difference++
			}
		}
		for hash := range actual {
			if _, ok := desiredByHash[hash]; !ok {
				difference++
			}
		}
		classified := synchronizationErr.notConverged + synchronizationErr.missing + synchronizationErr.unreadable + synchronizationErr.ambiguous
		if difference > classified {
			synchronizationErr.notConverged += difference - classified
		}
	}

	result := reconcileUnifiedAccessGroupKeyMembership(desired, prior, actual)
	if synchronizationErr.hasError() {
		return result, synchronizationErr
	}
	return result, nil
}

func (r *UnifiedAccessGroupResource) findUnifiedAccessGroupsByExactNameWithRetry(ctx context.Context, name string) ([]map[string]interface{}, error) {
	attempts, delay, maxDelay := r.verificationSettings()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		matches, err := r.findUnifiedAccessGroupsByExactName(ctx, name)
		if err == nil {
			return matches, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !shouldRetryUnifiedAccessGroupVerification(err) || attempt == attempts {
			return nil, err
		}
		if err := waitForUnifiedAccessGroupVerification(ctx, delay); err != nil {
			return nil, err
		}
		delay = nextUnifiedAccessGroupVerificationDelay(delay, maxDelay)
	}
	return nil, lastErr
}

func (r *UnifiedAccessGroupResource) findUnifiedAccessGroupsForCreateRecovery(ctx context.Context, name string) ([]map[string]interface{}, error) {
	attempts, delay, maxDelay := r.verificationSettings()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		matches, err := r.findUnifiedAccessGroupsByExactName(ctx, name)
		if err == nil && len(matches) > 0 {
			return matches, nil
		}
		if err != nil {
			lastErr = err
			if !shouldRetryUnifiedAccessGroupVerification(err) {
				return nil, err
			}
		}
		if attempt == attempts {
			if err != nil {
				return nil, lastErr
			}
			return []map[string]interface{}{}, nil
		}
		// A successful empty list immediately after a possibly accepted create
		// is propagation, not proof of absence. Preflight uses the ordinary
		// helper and therefore never pays this operation-aware retry window.
		if err := waitForUnifiedAccessGroupVerification(ctx, delay); err != nil {
			return nil, err
		}
		delay = nextUnifiedAccessGroupVerificationDelay(delay, maxDelay)
	}
	return nil, lastErr
}

func (r *UnifiedAccessGroupResource) findUnifiedAccessGroupsByExactName(ctx context.Context, name string) ([]map[string]interface{}, error) {
	groups, err := fetchTopLevelListObjects(ctx, r.client, "/v1/access_group", "unified access group item")
	if err != nil {
		return nil, err
	}
	matches := make([]map[string]interface{}, 0)
	for _, group := range groups {
		if err := validateUnifiedAccessGroupIdentity(group, "", ""); err != nil {
			return nil, err
		}
		if group["access_group_name"].(string) == name {
			matches = append(matches, group)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i]["access_group_id"].(string) < matches[j]["access_group_id"].(string)
	})
	return matches, nil
}

func validateUnifiedAccessGroupIdentity(result map[string]interface{}, expectedID, expectedName string) error {
	if result == nil {
		return errors.New("access-group response is not an object")
	}
	id, idOK := result["access_group_id"].(string)
	name, nameOK := result["access_group_name"].(string)
	if !idOK || id == "" || !nameOK || name == "" {
		return errors.New("access-group response omitted a valid identity")
	}
	if expectedID != "" && id != expectedID {
		return errors.New("access-group response identity did not match the requested group")
	}
	if expectedName != "" && name != expectedName {
		return errors.New("access-group response name did not match the requested group")
	}
	return nil
}

func unifiedAccessGroupCandidateMatchesConfiguration(ctx context.Context, candidate map[string]interface{}, data *UnifiedAccessGroupResourceModel) (bool, error) {
	if err := validateUnifiedAccessGroupIdentity(candidate, "", data.AccessGroupName.ValueString()); err != nil {
		return false, err
	}
	if !data.Description.IsNull() && !data.Description.IsUnknown() {
		value, ok := candidate["description"].(string)
		if !ok || value != data.Description.ValueString() {
			return false, nil
		}
	}
	for _, field := range []struct {
		name  string
		value types.List
	}{
		{name: "access_model_names", value: data.AccessModelNames},
		{name: "access_mcp_server_ids", value: data.AccessMCPServerIDs},
		{name: "access_agent_ids", value: data.AccessAgentIDs},
		{name: "assigned_team_ids", value: data.AssignedTeamIDs},
	} {
		if field.value.IsNull() || field.value.IsUnknown() {
			continue
		}
		var configured []string
		if diagnostics := field.value.ElementsAs(ctx, &configured, false); diagnostics.HasError() {
			return false, errors.New("configured access-group list could not be decoded")
		}
		observed, err := rawStringList(candidate[field.name])
		if err != nil {
			return false, nil
		}
		sort.Strings(configured)
		sort.Strings(observed)
		if len(configured) != len(observed) {
			return false, nil
		}
		for index := range configured {
			if configured[index] != observed[index] {
				return false, nil
			}
		}
	}
	if isKnownUnifiedAccessGroupKeyList(data.AssignedKeyIDs) {
		configured, err := unifiedAccessGroupKeyRepresentations(data.AssignedKeyIDs)
		if err != nil {
			return false, errors.New("configured assigned-key membership could not be decoded")
		}
		observed, invalid, err := rawUnifiedAccessGroupAssignedKeyRepresentations(candidate["assigned_key_ids"])
		if err != nil || invalid != 0 {
			return false, nil
		}
		configuredHashes := make(map[string]bool, len(configured))
		observedHashes := make(map[string]bool, len(observed))
		for hash := range configured {
			configuredHashes[hash] = true
		}
		for hash := range observed {
			observedHashes[hash] = true
		}
		if !unifiedAccessGroupHashSetsEqual(configuredHashes, observedHashes) {
			return false, nil
		}
	}
	return true, nil
}

func rawStringList(raw interface{}) ([]string, error) {
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...), nil
	case []interface{}:
		result := make([]string, 0, len(values))
		for _, value := range values {
			stringValue, ok := value.(string)
			if !ok {
				return nil, errors.New("response list contains a non-string value")
			}
			result = append(result, stringValue)
		}
		return result, nil
	default:
		return nil, errors.New("response field is not a string list")
	}
}

func addUnifiedAccessGroupCacheWarning(diagnostics interface {
	AddWarning(summary string, detail string)
}) {
	diagnostics.AddWarning(
		"Peer Worker Authorization Caches May Remain Stale",
		"Terraform verified durable database membership on both the /v1/access_group row and the /key/info key row. LiteLLM v1.98 provides no API that invalidates every worker's in-memory key cache: after an attach or security-sensitive detach, peer workers may retain their prior authorization decision until their configured cache TTL expires. This warning does not promise cross-worker runtime authorization convergence or a fixed wait time.",
	)
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
	// assigned_key_ids is deliberately excluded. The access-group response is
	// candidate discovery only; durable membership comes from /key/info.
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

func resolveUnifiedAccessGroupUnknowns(data *UnifiedAccessGroupResourceModel) {
	for _, value := range []*types.List{
		&data.AccessModelNames,
		&data.AccessMCPServerIDs,
		&data.AccessAgentIDs,
		&data.AssignedTeamIDs,
		&data.AssignedKeyIDs,
	} {
		if value.IsUnknown() {
			*value = types.ListValueMust(types.StringType, []attr.Value{})
		}
	}
	for _, value := range []*types.String{&data.Description, &data.CreatedAt, &data.CreatedBy, &data.UpdatedAt, &data.UpdatedBy} {
		if value.IsUnknown() {
			*value = types.StringNull()
		}
	}
}

func setSafeAssignedKeyListFromResponse(target *types.List, raw interface{}) {
	values, _, err := rawUnifiedAccessGroupAssignedKeyRepresentations(raw)
	if err != nil {
		if target.IsUnknown() {
			*target = unifiedAccessGroupKeyList(nil)
		}
		return
	}
	// Data sources do not perform key-side ownership discovery. Publish only
	// normalized hash-shaped values from the group response, never raw/suffix
	// identifiers. Preserve response representations and duplicates.
	var rawValues []interface{}
	switch typed := raw.(type) {
	case []interface{}:
		rawValues = typed
	case []string:
		rawValues = make([]interface{}, len(typed))
		for index := range typed {
			rawValues[index] = typed[index]
		}
	}
	items := make([]string, 0)
	for _, rawValue := range rawValues {
		value, ok := rawValue.(string)
		if !ok {
			continue
		}
		hash, hashErr := unifiedAccessGroupKeyHash(value)
		if hashErr == nil && len(values[hash]) > 0 {
			items = append(items, value)
		}
	}
	*target = unifiedAccessGroupKeyList(items)
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
	} else if values, ok := raw.([]string); ok {
		items := make([]attr.Value, 0, len(values))
		for _, value := range values {
			items = append(items, types.StringValue(value))
		}
		*target, _ = types.ListValue(types.StringType, items)
	} else if target.IsUnknown() {
		*target, _ = types.ListValue(types.StringType, []attr.Value{})
	}
}
