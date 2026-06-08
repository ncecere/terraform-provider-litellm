# litellm_unified_access_group

Manages a LiteLLM Access Group using the current `/v1/access_group` API. These are the Access Groups shown in the LiteLLM UI and documented at https://docs.litellm.ai/docs/proxy/access_groups.

This resource is separate from `litellm_access_group`, which manages the older model-only access group API.

## Example Usage

```hcl
resource "litellm_unified_access_group" "engineering" {
  access_group_name = "Engineering"
  description       = "Engineering access group"

  access_model_names = [
    "gpt-4o",
    "claude-sonnet-4.5",
  ]

  access_mcp_server_ids = []
  access_agent_ids      = []

  assigned_team_ids = [litellm_team.engineering.team_id]
  assigned_key_ids  = []
}
```

## Argument Reference

* `access_group_name` - (Required) Display/name of the access group.
* `description` - (Optional) Description of the access group.
* `access_model_names` - (Optional) Model names this access group grants access to.
* `access_mcp_server_ids` - (Optional) MCP server IDs this access group grants access to.
* `access_agent_ids` - (Optional) Agent IDs this access group grants access to.
* `assigned_team_ids` - (Optional) Team IDs assigned to this access group.
* `assigned_key_ids` - (Optional) Key IDs assigned to this access group.

The LiteLLM API currently supports assigning Access Groups to teams and keys. Project assignment is not exposed by the `/v1/access_group` schema.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - Access group ID.
* `access_group_id` - Access group ID.
* `created_at` - Timestamp when the access group was created.
* `created_by` - User who created the access group.
* `updated_at` - Timestamp when the access group was last updated.
* `updated_by` - User who last updated the access group.

## Import

Import using the access group ID:

```shell
terraform import litellm_unified_access_group.engineering <access_group_id>
```
