package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// addMCPToolsetIDsToRequest merges configured MCP toolset IDs into the
// request's object_permission map. A null set leaves the remote value
// unmanaged; an empty set clears it explicitly.
func addMCPToolsetIDsToRequest(ctx context.Context, request map[string]interface{}, toolsetIDs types.Set) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	if toolsetIDs.IsNull() {
		return diagnostics
	}
	if toolsetIDs.IsUnknown() {
		diagnostics.AddAttributeError(path.Root("mcp_toolset_ids"), "Invalid MCP Toolset Assignment", "The mcp_toolset_ids value must be known before it can be sent to LiteLLM. No request was sent.")
		return diagnostics
	}

	ids, _, conversionDiagnostics := strictTerraformStringSet(ctx, toolsetIDs, path.Root("mcp_toolset_ids"))
	diagnostics.Append(conversionDiagnostics...)
	if diagnostics.HasError() {
		return diagnostics
	}
	sort.Strings(ids)

	permission, present := request["object_permission"].(map[string]interface{})
	if !present {
		permission = map[string]interface{}{}
		request["object_permission"] = permission
	}
	permission["mcp_toolsets"] = ids
	return diagnostics
}

// readMCPToolsetIDs projects object_permission.mcp_toolsets from an API
// container. When the attribute is unmanaged (null state) and the resource was
// not imported, the prior value is retained so unconfigured remote toolsets do
// not create drift.
func readMCPToolsetIDs(ctx context.Context, container map[string]interface{}, current types.Set, imported bool) (types.Set, error) {
	if !imported && current.IsNull() {
		return current, nil
	}

	rawPermission, present := container["object_permission"]
	if !present || rawPermission == nil {
		return stringSetFromAPI(ctx, nil)
	}
	permission, ok := rawPermission.(map[string]interface{})
	if !ok {
		return types.SetNull(types.StringType), fmt.Errorf("object_permission must be an object, got %T", rawPermission)
	}

	rawIDs, present := permission["mcp_toolsets"]
	if !present || rawIDs == nil {
		return stringSetFromAPI(ctx, nil)
	}
	ids, err := stringSetFromAPI(ctx, rawIDs)
	if err != nil {
		return types.SetNull(types.StringType), fmt.Errorf("invalid object_permission.mcp_toolsets: %w", err)
	}
	return ids, nil
}
