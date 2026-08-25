package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	organizationProjectImportedBudgetPrivateKey         = "organization_project_imported_budget_v1"
	organizationProjectBudgetOwnershipPendingPrivateKey = "organization_project_budget_ownership_pending_v1"
)

// organizationProjectPlanIsDestroy recognizes both the protocol's canonical
// null destroy plan and the fully-null plan/config shape produced by some
// Terraform planning paths. Destroy must bypass every field-level lifecycle
// check so the resource Delete implementation can run.
func organizationProjectPlanIsDestroy(req resource.ModifyPlanRequest) bool {
	if req.Plan.Raw.IsNull() {
		return true
	}
	if req.State.Raw.IsNull() {
		return false
	}
	return req.Config.Raw.IsNull() || (req.Config.Raw.IsFullyNull() && req.Plan.Raw.IsFullyNull())
}

// preserveOrganizationProjectBudgetID enforces v1.98's immutable budget
// association for an existing resource. A per-field import marker identifies
// authoritative Optional+Computed omission; without it, removing a known ID is
// an explicit configured-ownership transition and is rejected.
func preserveOrganizationProjectBudgetID(ctx context.Context, resourceName string, state, config, plan types.String, imported bool, resp *resource.ModifyPlanResponse) {
	if state.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("budget_id"), fmt.Sprintf("Unknown %s Budget Association", resourceName), "The prior budget_id is unknown, so the provider cannot prove that this plan preserves the existing LiteLLM v1.98 budget association. Refresh state and plan again.")
		return
	}

	if config.IsNull() {
		if knownString(state) && !imported {
			resp.Diagnostics.AddAttributeError(path.Root("budget_id"), fmt.Sprintf("Unsafe %s Budget Removal", resourceName), fmt.Sprintf("Removing a configured budget_id from an existing %s cannot converge safely on LiteLLM v1.98. Keep the existing value configured; only an association adopted with the resource import marker can remain omitted as Optional+Computed state.", resourceName))
			return
		}
		// Imported Optional+Computed omission preserves a known association. A
		// normal unconfigured null state also remains null, resolving any
		// framework-generated unknown without adopting an API default.
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("budget_id"), state)...)
		return
	}

	if config.IsUnknown() || plan.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("budget_id"), fmt.Sprintf("Unknown %s Budget Association", resourceName), "budget_id must be known while planning an existing resource because LiteLLM v1.98 cannot safely reassociate its budget after creation.")
		return
	}

	if !state.Equal(config) || !state.Equal(plan) {
		resp.Diagnostics.AddAttributeError(path.Root("budget_id"), fmt.Sprintf("Unsafe %s Budget Reassociation", resourceName), fmt.Sprintf("LiteLLM v1.98 does not provide a safe %s budget reassociation lifecycle. Keep the existing budget_id; an imported Optional+Computed budget_id may remain omitted but cannot be changed.", resourceName))
	}
}

// planImportedOmissionOwnership records an explicit, known HCL ownership
// transition as pending in planned private state while retaining the import
// permission. Apply can persist partial private state on failure, so only a
// successful, authoritative Update may remove the permission. Omission cancels
// a pending transition retained from an earlier failed apply.
func planImportedOmissionOwnership(ctx context.Context, pendingKey string, imported bool, config types.String, resp *resource.ModifyPlanResponse) bool {
	transitioning := imported && knownString(config)
	if resp.Private == nil {
		return transitioning
	}
	if transitioning {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, pendingKey, []byte("true"))...)
		return true
	}
	resp.Diagnostics.Append(resp.Private.SetKey(ctx, pendingKey, nil)...)
	return false
}

// forceImportedOwnershipUpdate makes an otherwise private-only ownership
// transition visible to Terraform. Resource ModifyPlan runs after attribute
// plan modifiers, so replacing a harmless computed timestamp with unknown
// produces an Update without changing the public schema or requiring
// replacement. Update resolves the timestamp through its authoritative read.
func forceImportedOwnershipUpdate(ctx context.Context, timestamp string, force bool, resp *resource.ModifyPlanResponse) {
	if !force || resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root(timestamp), types.StringUnknown())...)
}
