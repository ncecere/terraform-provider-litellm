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
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &TagResource{}
var _ resource.ResourceWithImportState = &TagResource{}
var _ resource.ResourceWithModifyPlan = &TagResource{}

const (
	tagImportedBudgetOmissionsPrivateKey = "tag_imported_budget_omissions_v1"
	tagBudgetOwnershipPendingPrivateKey  = "tag_budget_ownership_pending_v1"
	tagBudgetOwnershipInitializedKey     = "tag_budget_ownership_initialized_v1"
	tagUncertainCreatePrivateKey         = "tag_uncertain_create_v1"
	tagBudgetResetPendingPrivateKey      = "tag_budget_reset_pending_v1"
)

func NewTagResource() resource.Resource { return &TagResource{} }

type TagResource struct{ client *Client }

type TagResourceModel struct {
	ID                  types.String  `tfsdk:"id"`
	Name                types.String  `tfsdk:"name"`
	Description         types.String  `tfsdk:"description"`
	Models              types.List    `tfsdk:"models"`
	BudgetID            types.String  `tfsdk:"budget_id"`
	MaxBudget           types.Float64 `tfsdk:"max_budget"`
	SoftBudget          types.Float64 `tfsdk:"soft_budget"`
	MaxParallelRequests types.Int64   `tfsdk:"max_parallel_requests"`
	TPMLimit            types.Int64   `tfsdk:"tpm_limit"`
	RPMLimit            types.Int64   `tfsdk:"rpm_limit"`
	BudgetDuration      types.String  `tfsdk:"budget_duration"`
	ModelMaxBudget      types.String  `tfsdk:"model_max_budget"`
}

func (r *TagResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tag"
}

func (r *TagResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a LiteLLM tag. Budget controls are read authoritatively from the nested litellm_budget_table relation.",
		Attributes: map[string]schema.Attribute{
			"id":                    schema.StringAttribute{Description: "The unique identifier for this tag (same as name).", Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"name":                  schema.StringAttribute{Description: "The unique name of the tag.", Required: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"description":           schema.StringAttribute{Description: "Description of the tag.", Optional: true},
			"models":                schema.ListAttribute{Description: "Models associated with this tag.", Optional: true, Computed: true, ElementType: types.StringType},
			"budget_id":             schema.StringAttribute{Description: "Budget ID associated with this tag. LiteLLM v1.98 cannot safely detach or reassign an existing association.", Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"max_budget":            schema.Float64Attribute{Description: "Max budget in USD for this tag.", Optional: true, Computed: true},
			"soft_budget":           schema.Float64Attribute{Description: "Soft budget in USD for this tag.", Optional: true, Computed: true},
			"max_parallel_requests": schema.Int64Attribute{Description: "Max concurrent requests allowed for this tag.", Optional: true, Computed: true},
			"tpm_limit":             schema.Int64Attribute{Description: "Max tokens per minute for this tag.", Optional: true, Computed: true},
			"rpm_limit":             schema.Int64Attribute{Description: "Max requests per minute for this tag.", Optional: true, Computed: true},
			"budget_duration":       schema.StringAttribute{Description: "Duration for budget reset (for example, '1hr', '1d', or '28d').", Optional: true, Computed: true},
			"model_max_budget":      schema.StringAttribute{Description: "JSON object of per-model GenericBudgetConfig values.", Optional: true, Computed: true, Validators: []validator.String{tagModelBudgetValidator{}}},
		},
	}
}

func (r *TagResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TagResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	var resetPending *tagBudgetResetPending
	if req.Private != nil {
		uncertain, diagnostics := req.Private.GetKey(ctx, tagUncertainCreatePrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		if string(uncertain) == "true" {
			resp.Diagnostics.AddError("Uncertain Tag Ownership", "A prior create or attachment mutation could not prove final ownership or budget association. No update, replacement, or destroy is safe. Remove this state entry without destroying the remote object, verify ownership, then import the tag explicitly to resume management.")
			return
		}
		pendingRaw, diagnostics := req.Private.GetKey(ctx, tagBudgetResetPendingPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		var err error
		resetPending, err = decodeTagBudgetResetPending(pendingRaw)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Tag Reset State", err.Error())
			return
		}
	}
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan, config TagResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !req.State.Raw.IsNull() && (plan.ID.IsNull() || plan.Name.IsNull()) {
		return
	}
	if req.State.Raw.IsNull() {
		if knownString(config.BudgetID) && tagBudgetControlsPresent(&config) {
			resp.Diagnostics.AddAttributeError(path.Root("budget_id"), "Unsafe Shared Tag Budget Controls", "budget_id cannot be combined with inline tag budget controls. LiteLLM v1.98 ignores those controls for an existing shared budget.")
		}
		return
	}
	var state TagResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if resetPending != nil {
		if !knownString(state.BudgetID) || state.BudgetID.ValueString() != resetPending.BudgetID {
			resp.Diagnostics.AddError("Tag Reset Retry Not Safe", "The pending reset retry does not match the prior budget identity. No mutation is safe until the original association is restored or state is explicitly reconciled.")
			return
		}
		if !knownString(config.BudgetDuration) || config.BudgetDuration.ValueString() != resetPending.BudgetDuration {
			resp.Diagnostics.AddAttributeError(path.Root("budget_duration"), "Pending Tag Reset Must Complete", "A prior create must first retry its original budget reset duration against the pinned budget ID. Restore the original budget_duration, apply successfully, then make a separate duration change.")
			return
		}
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	}
	if knownString(config.ModelMaxBudget) && (!knownString(state.ModelMaxBudget) || !jsonSemanticallyEqual(config.ModelMaxBudget.ValueString(), state.ModelMaxBudget.ValueString())) {
		legacy, err := configuredModelBudgetIsLegacy(config.ModelMaxBudget)
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("model_max_budget"), "Invalid Tag Model Budget", err.Error())
		} else if legacy {
			resp.Diagnostics.AddAttributeError(path.Root("model_max_budget"), "Unsupported Legacy Scalar Tag Model Budget Update", "Finite scalar model budgets remain readable for compatibility, but LiteLLM v1.98 rejects them on budget update. Keep the existing scalar unchanged or migrate every model value to a GenericBudgetConfig object.")
		}
	}
	if state.BudgetID.IsNull() {
		stateFields, configFields := tagBudgetAttributeValues(&state), tagBudgetAttributeValues(&config)
		for _, name := range tagBudgetControlNames {
			if stateFields[name].IsNull() && !configFields[name].IsNull() && !configFields[name].IsUnknown() {
				resp.Diagnostics.AddAttributeError(path.Root(name), "Unsafe Inline Tag Budget Creation", "LiteLLM v1.98 cannot atomically create and attach a tag budget during update; an association race could mutate another budget. Configure inline controls when creating the tag, or create a dedicated litellm_budget and attach only its budget_id to this existing tag.")
			}
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}

	unmanaged := map[string]bool{}
	initialized := false
	if req.Private != nil {
		raw, diagnostics := req.Private.GetKey(ctx, tagImportedBudgetOmissionsPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		var err error
		unmanaged, err = decodeTagFieldSet(raw)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Tag Ownership State", err.Error())
			return
		}
		initializedRaw, diagnostics := req.Private.GetKey(ctx, tagBudgetOwnershipInitializedKey)
		resp.Diagnostics.Append(diagnostics...)
		initialized = string(initializedRaw) == "true"
	}
	if !initialized {
		// State written by earlier provider versions has no provenance marker.
		// Conservatively treat every known budget field as import/API-owned;
		// unchanged explicit HCL transfers each field through a real apply.
		for name, value := range tagBudgetAttributeValues(&state) {
			if !value.IsNull() && !value.IsUnknown() {
				unmanaged[name] = true
			}
		}
		if resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagImportedBudgetOmissionsPrivateKey, encodeTagFieldSet(unmanaged))...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipInitializedKey, []byte("true"))...)
		}
	}
	pending := map[string]bool{}
	stateFields, configFields := tagBudgetAttributeValues(&state), tagBudgetAttributeValues(&config)
	for _, name := range tagBudgetControlNames {
		configured, prior := configFields[name], stateFields[name]
		if configured.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown Tag Budget Transition", fmt.Sprintf("%s must be known while planning because an unknown value could become an unsafe budget clear.", name))
			continue
		}
		if configured.IsNull() {
			if unmanaged[name] {
				resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(name), prior)...)
			} else if name == "model_max_budget" && knownString(state.ModelMaxBudget) {
				resp.Diagnostics.AddAttributeError(path.Root(name), "Unsupported Tag Model Budget Clear", "LiteLLM v1.98 cannot persist either null or an empty object for model_max_budget through its tag or budget management APIs. Keep the existing value configured; direct database mutation is outside this API-only provider's safety boundary.")
			} else {
				resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(name), tagBudgetNullValue(name))...)
			}
			continue
		}
		if unmanaged[name] {
			pending[name] = true
		}
	}

	if knownString(state.BudgetID) {
		switch {
		case config.BudgetID.IsNull():
			resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("budget_id"), state.BudgetID)...)
		case config.BudgetID.IsUnknown() || !config.BudgetID.Equal(state.BudgetID):
			resp.Diagnostics.AddAttributeError(path.Root("budget_id"), "Unsafe Tag Budget Reassociation", "LiteLLM v1.98 cannot safely detach or reassign an existing tag budget. Keep the existing budget_id or omit it to preserve the association.")
		}
	}
	if knownString(config.BudgetID) && tagBudgetControlsPresent(&config) {
		resp.Diagnostics.AddAttributeError(path.Root("budget_id"), "Unsafe Shared Tag Budget Controls", "A configured budget_id may be shared by other entities. Manage its controls with a dedicated litellm_budget resource instead of through this tag.")
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipPendingPrivateKey, encodeTagFieldSet(pending))...)
	}
	if len(pending) > 0 {
		// Equal-value ownership transitions must produce a real Apply so private
		// provenance is consumed only after authoritative read-back succeeds.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("id"), types.StringUnknown())...)
	}
}

func (r *TagResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TagResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plannedCreate := data
	plannedBudgetID := data.BudgetID
	plannedBudgetDuration := data.BudgetDuration
	tagReq, err := r.buildTagRequest(ctx, &data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Tag Request", err.Error())
		return
	}
	visible, err := r.tagNameVisibleInList(ctx, data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Tag Existence Not Confirmed", fmt.Sprintf("Unable to prove that the tag name is available: %s", err))
		return
	}
	if visible {
		resp.Diagnostics.AddError("Tag Already Exists", "A stored or historical-spend tag with this name is already visible. Import an existing stored tag or choose a unique name; no create request was made.")
		return
	}
	var result map[string]interface{}
	createErr := r.client.DoRequestWithResponse(ctx, "POST", "/tag/new", tagReq, &result)
	data.ID = data.Name
	if createErr != nil {
		recovered := data
		if readErr := r.readTag(ctx, &recovered); readErr != nil {
			resp.Diagnostics.AddError("Tag Create Not Confirmed", fmt.Sprintf("LiteLLM rejected or ambiguously failed the create and the scoped tag identity could not be recovered: %s", createErr))
			return
		}
		field, mismatch := tagCreateFieldMismatch(&data, &recovered)
		detail := "LiteLLM returned an error after this tag identity became readable. The provider cannot distinguish a post-commit failure from a concurrent duplicate, so uncertain partial state was retained and all mutations are blocked. Verify ownership, remove the state entry without destroying the tag, then import it explicitly."
		if mismatch {
			detail = fmt.Sprintf("LiteLLM returned an error after this tag identity became readable, and authoritative %s did not match configuration. Uncertain partial state was retained and all mutations are blocked. Verify ownership, remove the state entry without destroying the tag, then import it explicitly.", field)
		}
		resp.Diagnostics.AddError("Tag Create Ownership Uncertain", detail)
		resp.Diagnostics.Append(resp.State.Set(ctx, &recovered)...)
		if resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipInitializedKey, []byte("true"))...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagUncertainCreatePrivateKey, []byte("true"))...)
		}
		return
	}
	if err := r.readTag(ctx, &data); err != nil {
		resp.Diagnostics.AddError("Tag Create Not Confirmed", fmt.Sprintf("Tag was created but authoritative read-back failed: %s", err))
		if resolveErr := resolveTagCreateUnknowns(ctx, &data); resolveErr != nil {
			resp.Diagnostics.AddError("Tag State Projection Failed", resolveErr.Error())
			return
		}
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		if resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipInitializedKey, []byte("true"))...)
		}
		return
	}
	if knownString(plannedBudgetID) && !data.BudgetID.Equal(plannedBudgetID) {
		resp.Diagnostics.AddError("Tag Budget Association Uncertain", "LiteLLM created the tag but authoritative read-back did not preserve the configured budget_id. Partial state was retained and all mutations are blocked until ownership is verified and the tag is explicitly re-imported.")
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		if resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipInitializedKey, []byte("true"))...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagUncertainCreatePrivateKey, []byte("true"))...)
		}
		return
	}
	if knownString(plannedBudgetDuration) && knownString(data.BudgetID) {
		expectedBudgetID := data.BudgetID.ValueString()
		expectedDuration := plannedBudgetDuration.ValueString()
		var budgetResult map[string]interface{}
		payload := map[string]interface{}{"budget_id": expectedBudgetID, "budget_duration": expectedDuration}
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/budget/update", payload, &budgetResult); err != nil {
			resp.Diagnostics.AddError("Tag Budget Reset Initialization Error", fmt.Sprintf("Tag was created, but LiteLLM could not initialize its budget reset schedule: %s", err))
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			if resp.Private != nil {
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipInitializedKey, []byte("true"))...)
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetResetPendingPrivateKey, encodeTagBudgetResetPending(expectedBudgetID, expectedDuration))...)
			}
			return
		}
		if confirmed, ok := budgetResult["budget_id"].(string); !ok || confirmed != expectedBudgetID {
			resp.Diagnostics.AddError("Tag Budget Reset Initialization Error", "LiteLLM accepted reset initialization but did not confirm the matching budget identity.")
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			if resp.Private != nil {
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipInitializedKey, []byte("true"))...)
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetResetPendingPrivateKey, encodeTagBudgetResetPending(expectedBudgetID, expectedDuration))...)
			}
			return
		}
		verified := data
		// The first read may omit reset fields before /budget/update has initialized
		// them. Preserve configured ownership so the final read adopts and verifies
		// the newly authoritative duration instead of retaining a null seed.
		verified.BudgetDuration = plannedBudgetDuration
		if err := r.readTag(ctx, &verified); err != nil {
			resp.Diagnostics.AddError("Tag Budget Association Uncertain", fmt.Sprintf("Reset initialization succeeded, but final authoritative association verification failed: %s. Partial state was retained and all mutations are blocked until explicit ownership reconciliation.", err))
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			if resp.Private != nil {
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipInitializedKey, []byte("true"))...)
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagUncertainCreatePrivateKey, []byte("true"))...)
			}
			return
		}
		if !knownString(verified.BudgetID) || verified.BudgetID.ValueString() != expectedBudgetID {
			resp.Diagnostics.AddError("Tag Budget Association Uncertain", "The tag budget association changed while reset initialization was in progress. Partial state was retained and all mutations are blocked until ownership is verified and the tag is explicitly re-imported.")
			resp.Diagnostics.Append(resp.State.Set(ctx, &verified)...)
			if resp.Private != nil {
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipInitializedKey, []byte("true"))...)
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagUncertainCreatePrivateKey, []byte("true"))...)
			}
			return
		}
		data = verified
	}
	if field, mismatch := tagCreateFieldMismatch(&plannedCreate, &data); mismatch {
		resp.Diagnostics.AddError("Tag Create Ownership Uncertain", fmt.Sprintf("LiteLLM accepted the create, but authoritative %s did not match configuration. Partial state was retained and all mutations are blocked until ownership is verified and the tag is explicitly re-imported.", field))
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		if resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipInitializedKey, []byte("true"))...)
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagUncertainCreatePrivateKey, []byte("true"))...)
		}
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipInitializedKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetResetPendingPrivateKey, nil)...)
	}
}

func (r *TagResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TagResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	marker, privateDiags := req.Private.GetKey(ctx, numericImportedPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	uncertain, privateDiags := req.Private.GetKey(ctx, tagUncertainCreatePrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	resetRaw, privateDiags := req.Private.GetKey(ctx, tagBudgetResetPendingPrivateKey)
	resp.Diagnostics.Append(privateDiags...)
	resetPending, resetErr := decodeTagBudgetResetPending(resetRaw)
	if resetErr != nil {
		resp.Diagnostics.AddError("Invalid Tag Reset State", resetErr.Error())
	}
	if resp.Diagnostics.HasError() {
		return
	}
	if string(uncertain) == "true" {
		resp.Diagnostics.AddError("Uncertain Tag Ownership", "Refresh cannot remove or adopt this tag because a prior create or attachment mutation could not prove final ownership or budget association. Remove the state entry without destroying the remote object, verify ownership, then import the tag explicitly to resume management.")
		return
	}
	imported := string(marker) == "true"
	candidate := data
	if err := r.readTagWithNumericOwnership(ctx, &candidate, imported); err != nil {
		if resetPending != nil {
			resp.Diagnostics.AddError("Tag Reset Retry Not Safe", "A reset initialization retry is pending, but the original tag budget association could not be confirmed. Prior state and the expected budget identity were retained.")
			return
		}
		if IsNotFoundError(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tag: %s", err))
		return
	}
	if resetPending != nil && (!knownString(candidate.BudgetID) || candidate.BudgetID.ValueString() != resetPending.BudgetID) {
		resp.Diagnostics.AddError("Tag Reset Retry Not Safe", "The tag budget association changed while reset initialization remained pending. Prior state and the original expected budget identity were retained; no mutation was attempted.")
		return
	}
	data = candidate
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if !resp.Diagnostics.HasError() && imported {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, nil)...)
	}
}

func (r *TagResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state TagResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.ID, plan.Name = state.ID, state.Name
	if knownString(state.BudgetID) && (!knownString(plan.BudgetID) || !plan.BudgetID.Equal(state.BudgetID)) {
		resp.Diagnostics.AddError("Unsafe Tag Budget Reassociation", "The tag budget association changed despite the plan safety check; no API call was made.")
		return
	}
	unmanaged, pending := map[string]bool{}, map[string]bool{}
	var resetPending *tagBudgetResetPending
	if resp.Private != nil {
		unmanagedRaw, diagnostics := resp.Private.GetKey(ctx, tagImportedBudgetOmissionsPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		pendingRaw, diagnostics := resp.Private.GetKey(ctx, tagBudgetOwnershipPendingPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		resetRaw, diagnostics := resp.Private.GetKey(ctx, tagBudgetResetPendingPrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		var err error
		resetPending, err = decodeTagBudgetResetPending(resetRaw)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Tag Reset State", err.Error())
		}
		unmanaged, err = decodeTagFieldSet(unmanagedRaw)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Tag Ownership State", err.Error())
		}
		pending, err = decodeTagFieldSet(pendingRaw)
		if err != nil {
			resp.Diagnostics.AddError("Invalid Tag Ownership State", err.Error())
		}
		if resp.Diagnostics.HasError() {
			return
		}
	}

	current := state
	if err := r.readTag(ctx, &current); err != nil {
		resp.Diagnostics.AddError("Tag Update Not Safe", fmt.Sprintf("Unable to verify the authoritative tag and budget identity before update: %s", err))
		return
	}
	if state.BudgetID.IsUnknown() || current.BudgetID.IsUnknown() || !current.BudgetID.Equal(state.BudgetID) {
		resp.Diagnostics.AddError("Tag Update Not Safe", "The authoritative tag budget association changed after planning; refresh and retry.")
		return
	}
	if resetPending != nil && (!knownString(state.BudgetID) || state.BudgetID.ValueString() != resetPending.BudgetID || !knownString(current.BudgetID) || current.BudgetID.ValueString() != resetPending.BudgetID) {
		resp.Diagnostics.AddError("Tag Reset Retry Not Safe", "The pending reset retry no longer matches both prior and authoritative budget identities; no API call was made.")
		return
	}

	rowRequest, rowChanged, err := buildTagRowUpdateRequest(ctx, &plan, &state)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Tag Request", err.Error())
		return
	}
	budgetRequest, budgetChanged, err := buildTagBudgetUpdateRequest(&plan, &state)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Tag Budget Request", err.Error())
		return
	}
	if resetPending != nil {
		// Complete the original pinned retry before allowing any subsequent
		// duration change. ModifyPlan rejects changed or removed configuration.
		budgetRequest["budget_duration"] = resetPending.BudgetDuration
		budgetChanged = true
	}
	attachBudget := !knownString(state.BudgetID) && knownString(plan.BudgetID)
	budgetExists := knownString(current.BudgetID)
	if budgetChanged && !budgetExists {
		for name, value := range budgetRequest {
			if name != "budget_reset_at" && value != nil {
				resp.Diagnostics.AddError("Unsafe Inline Tag Budget Creation", "LiteLLM v1.98 cannot atomically create and attach a tag budget during update; no API call was made. Configure inline controls at tag creation, or attach a separately managed litellm_budget by budget_id.")
				return
			}
		}
		// Null clears against an absent relation are already converged.
		budgetRequest = nil
	}
	if attachBudget {
		rowRequest["budget_id"] = plan.BudgetID.ValueString()
		rowChanged = true
	}
	if rowChanged {
		if err := addTagRowEcho(ctx, rowRequest, &plan, &state); err != nil {
			resp.Diagnostics.AddError("Invalid Tag Request", err.Error())
			return
		}
		rowRequest["name"] = state.Name.ValueString()
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/tag/update", rowRequest, nil); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update tag: %s", err))
			if attachBudget && resp.Private != nil {
				resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagUncertainCreatePrivateKey, []byte("true"))...)
			}
			return
		}
	}
	if len(budgetRequest) > 0 && budgetExists {
		// Address the exact association verified above. /tag/update reloads the
		// tag and can race onto a different shared budget before handling inline
		// controls; /budget/update cannot redirect to a newly attached ID.
		budgetRequest["budget_id"] = current.BudgetID.ValueString()
		var result map[string]interface{}
		if err := r.client.DoRequestWithResponse(ctx, "POST", "/budget/update", budgetRequest, &result); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("LiteLLM accepted any preceding tag row update, but the authoritative budget update failed; prior state was retained: %s", err))
			return
		}
		if confirmed, ok := result["budget_id"].(string); !ok || confirmed != current.BudgetID.ValueString() {
			resp.Diagnostics.AddError("Invalid API Response", "LiteLLM accepted the tag budget update but did not confirm the matching budget_id; prior state was retained.")
			return
		}
	}

	desired, actual := plan, plan
	seedTagClearOwnership(&actual, &state)
	if err := r.readTag(ctx, &actual); err != nil {
		resp.Diagnostics.AddError("Tag Update Not Confirmed", fmt.Sprintf("LiteLLM accepted the update but authoritative read-back failed; prior state was retained: %s", err))
		if attachBudget && resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagUncertainCreatePrivateKey, []byte("true"))...)
		}
		return
	}
	expectedBudgetID := state.BudgetID
	if attachBudget {
		expectedBudgetID = plan.BudgetID
	}
	if actual.BudgetID.IsUnknown() || !actual.BudgetID.Equal(expectedBudgetID) {
		resp.Diagnostics.AddError("Tag Update Not Confirmed", "The authoritative tag budget association did not match the verified planned association after update. Prior Terraform state was retained; the verified prior budget ID, not any newly associated budget, received a budget mutation.")
		if attachBudget && resp.Private != nil {
			resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagUncertainCreatePrivateKey, []byte("true"))...)
		}
		return
	}
	if field, mismatch := tagChangedFieldMismatch(&desired, &state, &actual); mismatch {
		resp.Diagnostics.AddError("Tag Update Did Not Converge", fmt.Sprintf("LiteLLM accepted the update but authoritative read-back did not match planned %s; prior state was retained.", field))
		return
	}
	if field, mismatch := tagPendingOwnershipMismatch(&desired, &actual, pending); mismatch {
		resp.Diagnostics.AddError("Tag Budget Ownership Not Confirmed", fmt.Sprintf("Authoritative read-back did not match configured %s, so import/upgrade omission ownership was retained.", field))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &actual)...)
	if resp.Diagnostics.HasError() || resp.Private == nil {
		return
	}
	for name := range pending {
		delete(unmanaged, name)
	}
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagImportedBudgetOmissionsPrivateKey, encodeTagFieldSet(unmanaged))...)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipPendingPrivateKey, nil)...)
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetResetPendingPrivateKey, nil)...)
}

func (r *TagResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	if req.Private != nil {
		uncertain, diagnostics := req.Private.GetKey(ctx, tagUncertainCreatePrivateKey)
		resp.Diagnostics.Append(diagnostics...)
		if string(uncertain) == "true" {
			resp.Diagnostics.AddError("Uncertain Tag Ownership", "Delete is blocked because a prior create or attachment mutation could not prove final ownership or budget association. Remove the state entry without destroying the remote object, verify ownership, then import it explicitly before any managed deletion.")
			return
		}
	}
	var data TagResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DoRequestWithResponse(ctx, "POST", "/tag/delete", map[string]interface{}{"name": data.Name.ValueString()}, nil); err != nil && !IsNotFoundError(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete tag: %s", err))
	}
}

func (r *TagResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), req.ID)...)
	if resp.Private != nil {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, numericImportedPrivateKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagImportedBudgetOmissionsPrivateKey, encodeTagFieldSet(allTagBudgetFields()))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetOwnershipInitializedKey, []byte("true"))...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagUncertainCreatePrivateKey, nil)...)
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, tagBudgetResetPendingPrivateKey, nil)...)
	}
}

func (r *TagResource) buildTagRequest(ctx context.Context, data *TagResourceModel) (map[string]interface{}, error) {
	if knownString(data.BudgetID) && tagBudgetControlsConfigured(data) {
		return nil, fmt.Errorf("budget_id cannot be combined with inline tag budget controls because LiteLLM v1.98 ignores those controls for an existing shared budget")
	}
	request := map[string]interface{}{"name": data.Name.ValueString()}
	if knownString(data.Description) {
		request["description"] = data.Description.ValueString()
	}
	if knownString(data.BudgetID) {
		request["budget_id"] = data.BudgetID.ValueString()
	}
	if knownString(data.BudgetDuration) {
		request["budget_duration"] = data.BudgetDuration.ValueString()
	}
	if knownString(data.ModelMaxBudget) {
		value, err := decodeRequestJSONObject(data.ModelMaxBudget.ValueString(), "model_max_budget")
		if err != nil {
			return nil, err
		}
		request["model_max_budget"] = value
	}
	addKnownFloat(request, "max_budget", data.MaxBudget)
	addKnownFloat(request, "soft_budget", data.SoftBudget)
	addKnownInt(request, "max_parallel_requests", data.MaxParallelRequests)
	addKnownInt(request, "tpm_limit", data.TPMLimit)
	addKnownInt(request, "rpm_limit", data.RPMLimit)
	if !data.Models.IsNull() && !data.Models.IsUnknown() {
		models, err := stringListRequest(ctx, data.Models, "models")
		if err != nil {
			return nil, err
		}
		request["models"] = models
	}
	return request, nil
}

func (r *TagResource) readTag(ctx context.Context, data *TagResourceModel) error {
	return r.readTagWithNumericOwnership(ctx, data, false)
}

func (r *TagResource) readTagWithNumericOwnership(ctx context.Context, data *TagResourceModel, imported bool) error {
	tagName := data.Name.ValueString()
	if tagName == "" {
		tagName = data.ID.ValueString()
	}
	raw, err := r.fetchTagInfo(ctx, tagName)
	if err != nil {
		return err
	}
	result, err := selectTagInfoObject(raw, tagName, imported)
	if err != nil {
		return err
	}
	if err := applyTagObjectToResource(ctx, data, result, tagName, imported); err != nil {
		return err
	}
	return nil
}

func (r *TagResource) tagNameVisibleInList(ctx context.Context, tagName string) (bool, error) {
	items, err := fetchTopLevelListObjects(ctx, r.client, "/tag/list", "tag item")
	if err != nil {
		return false, err
	}
	for _, item := range items {
		name, ok := item["name"].(string)
		if !ok || name == "" {
			return false, fmt.Errorf("/tag/list returned an item without a nonempty name")
		}
		if name == tagName {
			return true, nil
		}
	}
	return false, nil
}

func (r *TagResource) fetchTagInfo(ctx context.Context, tagName string) (interface{}, error) {
	var raw interface{}
	err := r.client.DoRequestWithResponse(ctx, "POST", "/tag/info", map[string]interface{}{"names": []string{tagName}}, &raw)
	return raw, err
}

func selectTagInfoObject(raw interface{}, tagName string, requireKeyed bool) (map[string]interface{}, error) {
	switch value := raw.(type) {
	case map[string]interface{}:
		if nested, exists := value[tagName]; exists {
			object, ok := nested.(map[string]interface{})
			if !ok || object == nil {
				return nil, fmt.Errorf("invalid tag info envelope for %q: expected an object", tagName)
			}
			return object, nil
		}
		if requireKeyed {
			return nil, fmt.Errorf("tag import read response is missing the authoritative keyed envelope")
		}
		if len(value) == 0 {
			return nil, fmt.Errorf("tag not found: %s", tagName)
		}
		return value, nil
	case []interface{}:
		if requireKeyed {
			return nil, fmt.Errorf("tag import read response is missing the authoritative keyed envelope")
		}
		if len(value) != 1 {
			return nil, fmt.Errorf("invalid tag info response: expected exactly one tag object")
		}
		object, ok := value[0].(map[string]interface{})
		if !ok || object == nil {
			return nil, fmt.Errorf("invalid tag info response: expected an object")
		}
		return object, nil
	default:
		return nil, fmt.Errorf("invalid tag info response: expected an object")
	}
}

func applyTagObjectToResource(ctx context.Context, data *TagResourceModel, object map[string]interface{}, tagName string, imported bool) error {
	name, ok := object["name"].(string)
	if !ok || name == "" || name != tagName {
		return fmt.Errorf("invalid tag response identity: expected name %q", tagName)
	}
	next := *data
	next.Name, next.ID = types.StringValue(name), types.StringValue(name)
	if err := updateTagDescription(&next.Description, object); err != nil {
		return err
	}
	models, presence, diagnostics := strictAPIStringList(ctx, object, "models", path.Root("models"))
	if err := collectionProjectionError(ctx, diagnostics); err != nil {
		return err
	}
	if presence == apiValuePresent {
		next.Models = models
	} else if presence == apiValueNull || next.Models.IsUnknown() {
		empty, diagnostics := checkedStringListValue(ctx, nil, path.Root("models"))
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		next.Models = empty
	}
	if err := updateTagBudgetState(tagResourceBudgetTargets(&next), object, imported, false); err != nil {
		return err
	}
	*data = next
	return nil
}

func updateTagDescription(target *types.String, object map[string]interface{}) error {
	value, presence, err := apiValueAt(object, "description")
	if err != nil {
		return err
	}
	if presence != apiValuePresent {
		*target = types.StringNull()
		return nil
	}
	description, ok := value.(string)
	if !ok {
		return fmt.Errorf("invalid response field %q: expected a string or null", "description")
	}
	*target = types.StringValue(description)
	return nil
}

func addTagRowEcho(ctx context.Context, request map[string]interface{}, plan, state *TagResourceModel) error {
	description, models := plan.Description, plan.Models
	if description.IsUnknown() {
		description = state.Description
	}
	if models.IsUnknown() {
		models = state.Models
	}
	if description.IsNull() {
		request["description"] = nil
	} else {
		request["description"] = description.ValueString()
	}
	if models.IsNull() {
		request["models"] = []string{}
	} else {
		values, err := stringListRequest(ctx, models, "models")
		if err != nil {
			return err
		}
		request["models"] = values
	}
	return nil
}

func buildTagRowUpdateRequest(ctx context.Context, plan, state *TagResourceModel) (map[string]interface{}, bool, error) {
	description, models := plan.Description, plan.Models
	if description.IsUnknown() {
		description = state.Description
	}
	if models.IsUnknown() {
		models = state.Models
	}
	changed := !description.Equal(state.Description) || !models.Equal(state.Models)
	request := map[string]interface{}{}
	if !changed {
		return request, false, nil
	}
	if description.IsNull() {
		request["description"] = nil
	} else {
		request["description"] = description.ValueString()
	}
	if models.IsNull() {
		request["models"] = []string{}
	} else {
		modelValues, err := stringListRequest(ctx, models, "models")
		if err != nil {
			return nil, false, err
		}
		request["models"] = modelValues
	}
	return request, true, nil
}

func buildTagBudgetUpdateRequest(plan, state *TagResourceModel) (map[string]interface{}, bool, error) {
	request := map[string]interface{}{}
	for _, field := range []struct {
		name        string
		plan, state types.Float64
	}{
		{"max_budget", plan.MaxBudget, state.MaxBudget}, {"soft_budget", plan.SoftBudget, state.SoftBudget},
	} {
		if field.plan.IsUnknown() || field.plan.Equal(field.state) {
			continue
		}
		if field.plan.IsNull() {
			request[field.name] = nil
		} else {
			request[field.name] = field.plan.ValueFloat64()
		}
	}
	for _, field := range []struct {
		name        string
		plan, state types.Int64
	}{
		{"max_parallel_requests", plan.MaxParallelRequests, state.MaxParallelRequests}, {"tpm_limit", plan.TPMLimit, state.TPMLimit}, {"rpm_limit", plan.RPMLimit, state.RPMLimit},
	} {
		if field.plan.IsUnknown() || field.plan.Equal(field.state) {
			continue
		}
		if field.plan.IsNull() {
			request[field.name] = nil
		} else {
			request[field.name] = field.plan.ValueInt64()
		}
	}
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
			return nil, false, fmt.Errorf("model_max_budget cannot be cleared because LiteLLM v1.98 rejects both null and an empty object")
		} else {
			legacy, err := configuredModelBudgetIsLegacy(plan.ModelMaxBudget)
			if err != nil {
				return nil, false, err
			}
			if legacy {
				return nil, false, fmt.Errorf("legacy scalar model_max_budget values cannot be added or changed through LiteLLM v1.98 budget update")
			}
			value, err := decodeRequestJSONObject(plan.ModelMaxBudget.ValueString(), "model_max_budget")
			if err != nil {
				return nil, false, err
			}
			request["model_max_budget"] = value
		}
	}
	return request, len(request) > 0, nil
}

func tagBudgetAttributeValues(data *TagResourceModel) map[string]attr.Value {
	return map[string]attr.Value{
		"max_budget": data.MaxBudget, "soft_budget": data.SoftBudget,
		"max_parallel_requests": data.MaxParallelRequests, "tpm_limit": data.TPMLimit, "rpm_limit": data.RPMLimit,
		"budget_duration": data.BudgetDuration, "model_max_budget": data.ModelMaxBudget,
	}
}

func tagBudgetNullValue(name string) attr.Value {
	switch name {
	case "max_budget", "soft_budget":
		return types.Float64Null()
	case "max_parallel_requests", "tpm_limit", "rpm_limit":
		return types.Int64Null()
	default:
		return types.StringNull()
	}
}

func tagBudgetControlsPresent(data *TagResourceModel) bool {
	for _, value := range tagBudgetAttributeValues(data) {
		if !value.IsNull() {
			return true
		}
	}
	return false
}

func tagBudgetControlsConfigured(data *TagResourceModel) bool {
	for _, value := range tagBudgetAttributeValues(data) {
		if !value.IsNull() && !value.IsUnknown() {
			return true
		}
	}
	return false
}

func resolveTagCreateUnknowns(ctx context.Context, data *TagResourceModel) error {
	if data.BudgetID.IsUnknown() {
		data.BudgetID = types.StringNull()
	}
	if data.MaxBudget.IsUnknown() {
		data.MaxBudget = types.Float64Null()
	}
	if data.SoftBudget.IsUnknown() {
		data.SoftBudget = types.Float64Null()
	}
	if data.MaxParallelRequests.IsUnknown() {
		data.MaxParallelRequests = types.Int64Null()
	}
	if data.TPMLimit.IsUnknown() {
		data.TPMLimit = types.Int64Null()
	}
	if data.RPMLimit.IsUnknown() {
		data.RPMLimit = types.Int64Null()
	}
	if data.BudgetDuration.IsUnknown() {
		data.BudgetDuration = types.StringNull()
	}
	if data.ModelMaxBudget.IsUnknown() {
		data.ModelMaxBudget = types.StringNull()
	}
	if data.Models.IsUnknown() {
		empty, diagnostics := checkedStringListValue(ctx, nil, path.Root("models"))
		if err := collectionProjectionError(ctx, diagnostics); err != nil {
			return err
		}
		data.Models = empty
	}
	return nil
}

func seedTagClearOwnership(target, prior *TagResourceModel) {
	if target.MaxBudget.IsNull() && knownFloat(prior.MaxBudget) {
		target.MaxBudget = prior.MaxBudget
	}
	if target.SoftBudget.IsNull() && knownFloat(prior.SoftBudget) {
		target.SoftBudget = prior.SoftBudget
	}
	if target.MaxParallelRequests.IsNull() && knownInt(prior.MaxParallelRequests) {
		target.MaxParallelRequests = prior.MaxParallelRequests
	}
	if target.TPMLimit.IsNull() && knownInt(prior.TPMLimit) {
		target.TPMLimit = prior.TPMLimit
	}
	if target.RPMLimit.IsNull() && knownInt(prior.RPMLimit) {
		target.RPMLimit = prior.RPMLimit
	}
	if target.BudgetDuration.IsNull() && knownString(prior.BudgetDuration) {
		target.BudgetDuration = prior.BudgetDuration
	}
	if target.ModelMaxBudget.IsNull() && knownString(prior.ModelMaxBudget) {
		target.ModelMaxBudget = prior.ModelMaxBudget
	}
}

func tagPendingOwnershipMismatch(desired, actual *TagResourceModel, pending map[string]bool) (string, bool) {
	desiredFields, actualFields := tagBudgetAttributeValues(desired), tagBudgetAttributeValues(actual)
	for name := range pending {
		desiredValue, desiredOK := desiredFields[name]
		actualValue, actualOK := actualFields[name]
		if !desiredOK || !actualOK || desiredValue.IsUnknown() || !desiredValue.Equal(actualValue) {
			return name, true
		}
	}
	return "", false
}

func tagCreateFieldMismatch(desired, actual *TagResourceModel) (string, bool) {
	fields := []struct {
		name            string
		desired, actual attr.Value
	}{
		{"name", desired.Name, actual.Name}, {"description", desired.Description, actual.Description}, {"models", desired.Models, actual.Models},
		{"budget_id", desired.BudgetID, actual.BudgetID}, {"max_budget", desired.MaxBudget, actual.MaxBudget}, {"soft_budget", desired.SoftBudget, actual.SoftBudget},
		{"max_parallel_requests", desired.MaxParallelRequests, actual.MaxParallelRequests}, {"tpm_limit", desired.TPMLimit, actual.TPMLimit},
		{"rpm_limit", desired.RPMLimit, actual.RPMLimit}, {"budget_duration", desired.BudgetDuration, actual.BudgetDuration},
		{"model_max_budget", desired.ModelMaxBudget, actual.ModelMaxBudget},
	}
	for _, field := range fields {
		if field.desired.IsNull() || field.desired.IsUnknown() || field.desired.Equal(field.actual) {
			continue
		}
		if field.name == "model_max_budget" {
			desiredString, desiredOK := field.desired.(types.String)
			actualString, actualOK := field.actual.(types.String)
			if desiredOK && actualOK && knownString(desiredString) && knownString(actualString) && modelBudgetSemanticallyEqual(desiredString.ValueString(), actualString.ValueString()) {
				continue
			}
		}
		return field.name, true
	}
	return "", false
}

func tagChangedFieldMismatch(desired, prior, actual *TagResourceModel) (string, bool) {
	fields := []struct {
		name                   string
		desired, prior, actual attr.Value
	}{
		{"description", desired.Description, prior.Description, actual.Description}, {"models", desired.Models, prior.Models, actual.Models},
		{"budget_id", desired.BudgetID, prior.BudgetID, actual.BudgetID}, {"max_budget", desired.MaxBudget, prior.MaxBudget, actual.MaxBudget},
		{"soft_budget", desired.SoftBudget, prior.SoftBudget, actual.SoftBudget}, {"max_parallel_requests", desired.MaxParallelRequests, prior.MaxParallelRequests, actual.MaxParallelRequests},
		{"tpm_limit", desired.TPMLimit, prior.TPMLimit, actual.TPMLimit}, {"rpm_limit", desired.RPMLimit, prior.RPMLimit, actual.RPMLimit},
		{"budget_duration", desired.BudgetDuration, prior.BudgetDuration, actual.BudgetDuration}, {"model_max_budget", desired.ModelMaxBudget, prior.ModelMaxBudget, actual.ModelMaxBudget},
	}
	for _, field := range fields {
		if !field.desired.IsUnknown() && !field.desired.Equal(field.prior) && !field.desired.Equal(field.actual) {
			return field.name, true
		}
	}
	return "", false
}
