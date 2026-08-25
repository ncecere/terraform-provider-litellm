package provider

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const (
	organizationModelRPMOwnedPrivateKey = "organization_model_rpm_owned_keys_v1"
	organizationModelTPMOwnedPrivateKey = "organization_model_tpm_owned_keys_v1"
)

type organizationNumericMapRemovalModifier struct {
	privateKey string
}

var _ planmodifier.Map = organizationNumericMapRemovalModifier{}

func (organizationNumericMapRemovalModifier) Description(context.Context) string {
	return "Blocks removal of a Terraform-owned per-model numeric key because LiteLLM only merges organization metadata and replacement would cascade destructively."
}

func (m organizationNumericMapRemovalModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m organizationNumericMapRemovalModifier) PlanModifyMap(ctx context.Context, req planmodifier.MapRequest, resp *planmodifier.MapResponse) {
	if req.ConfigValue.IsUnknown() {
		return
	}

	ownedKeys, hasOwnership := organizationOwnedNumericMapKeys(ctx, req, resp, m.privateKey)
	if resp.Diagnostics.HasError() {
		return
	}

	configuredKeys := map[string]struct{}{}
	configuredKeyList := make([]string, 0, len(req.ConfigValue.Elements()))
	if !req.ConfigValue.IsNull() {
		for key := range req.ConfigValue.Elements() {
			configuredKeys[key] = struct{}{}
			configuredKeyList = append(configuredKeyList, key)
		}
	}

	// A normal refreshed state is authoritative for whether a previously owned
	// key still exists. Retire ownership only after that state proves absence;
	// unknown state retains every marker, and -refresh=false keeps stale present
	// keys blocked.
	stateKnown := !req.State.Raw.IsNull() && !req.StateValue.IsUnknown()
	activeOwnedKeys := ownedKeys
	if stateKnown && hasOwnership {
		stateKeys := map[string]struct{}{}
		if !req.StateValue.IsNull() {
			for key := range req.StateValue.Elements() {
				stateKeys[key] = struct{}{}
			}
		}
		activeOwnedKeys = make([]string, 0, len(ownedKeys))
		for _, key := range ownedKeys {
			if _, stillExists := stateKeys[key]; stillExists {
				activeOwnedKeys = append(activeOwnedKeys, key)
			}
		}
		for _, key := range activeOwnedKeys {
			if _, stillConfigured := configuredKeys[key]; stillConfigured {
				continue
			}
			resp.Diagnostics.AddAttributeError(
				req.Path,
				"Unsafe Organization Per-Model Limit Removal",
				"LiteLLM v1.98 only merges organization per-model limit maps, so it cannot remove a Terraform-owned key in place. Automatically replacing or deleting the organization would cascade deletion to its teams, memberships, and keys. The provider blocked this plan without calling LiteLLM or changing state. Restore the removed key in configuration; if removal is required, perform and verify a separately coordinated migration outside this resource, then run a normal refresh so authoritative state confirms the key is absent. A plan with -refresh=false remains blocked while stale state still contains the key.",
			)
			return
		}
	}

	var nextOwnedKeys []string
	switch {
	case stateKnown && !req.ConfigValue.IsNull():
		nextOwnedKeys = configuredKeyList
	case stateKnown:
		nextOwnedKeys = activeOwnedKeys
	case !req.ConfigValue.IsNull():
		// Unknown state cannot prove an old key was removed. Preserve old
		// ownership while recording any newly configured keys.
		seen := make(map[string]struct{}, len(ownedKeys)+len(configuredKeyList))
		for _, key := range append(append([]string{}, ownedKeys...), configuredKeyList...) {
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				nextOwnedKeys = append(nextOwnedKeys, key)
			}
		}
	default:
		return
	}
	if len(nextOwnedKeys) == 0 {
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, m.privateKey, nil)...)
	} else {
		sort.Strings(nextOwnedKeys)
		encoded, err := json.Marshal(nextOwnedKeys)
		if err != nil {
			resp.Diagnostics.AddAttributeError(req.Path, "Unable to Track Organization Numeric Map Ownership", "The provider could not encode private per-model key ownership.")
			return
		}
		resp.Diagnostics.Append(resp.Private.SetKey(ctx, m.privateKey, encoded)...)
	}

	if req.State.Raw.IsNull() || req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		return
	}

	merged := make(map[string]attr.Value, len(req.StateValue.Elements())+len(req.ConfigValue.Elements()))
	for key, value := range req.StateValue.Elements() {
		merged[key] = value
	}
	if !req.ConfigValue.IsNull() {
		for key, value := range req.ConfigValue.Elements() {
			merged[key] = value
		}
	}
	value, diagnostics := types.MapValue(types.Int64Type, merged)
	resp.Diagnostics.Append(diagnostics...)
	if !resp.Diagnostics.HasError() {
		resp.PlanValue = value
	}
}

func organizationOwnedNumericMapKeys(ctx context.Context, req planmodifier.MapRequest, resp *planmodifier.MapResponse, privateKey string) ([]string, bool) {
	encoded, diagnostics := req.Private.GetKey(ctx, privateKey)
	resp.Diagnostics.Append(diagnostics...)
	if resp.Diagnostics.HasError() || len(encoded) == 0 {
		return nil, false
	}
	var keys []string
	if err := decodeJSONUseNumber(encoded, &keys); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Organization Numeric Map Ownership", "The provider could not decode its private per-model key ownership marker.")
		return nil, false
	}
	return keys, true
}
