# litellm_unified_access_group

Retrieves a LiteLLM Access Group by ID using the current `/v1/access_group` API.

## Example Usage

```hcl
data "litellm_unified_access_group" "engineering" {
  access_group_id = "access-group-id"
}
```

## Argument Reference

* `access_group_id` - (Required) Access group ID.

## Attribute Reference

* `id` - Access group ID.
* `access_group_name` - Display/name of the access group.
* `description` - Description of the access group.
* `access_model_names` - Model names this access group grants access to.
* `access_mcp_server_ids` - MCP server IDs this access group grants access to.
* `access_agent_ids` - Agent IDs this access group grants access to.
* `assigned_team_ids` - Team IDs assigned to this access group.
* `assigned_key_ids` - Key IDs assigned to this access group.
* `created_at` - Timestamp when the access group was created.
* `created_by` - User who created the access group.
* `updated_at` - Timestamp when the access group was last updated.
* `updated_by` - User who last updated the access group.
