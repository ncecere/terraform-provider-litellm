# litellm_unified_access_groups

Lists LiteLLM Access Groups using the current `/v1/access_group` API.

## Example Usage

```hcl
data "litellm_unified_access_groups" "all" {}
```

## Attribute Reference

* `access_groups` - List of access groups, each with:
  * `id`
  * `access_group_id`
  * `access_group_name`
  * `description`
  * `access_model_names`
  * `access_mcp_server_ids`
  * `access_agent_ids`
  * `assigned_team_ids`
  * `assigned_key_ids` - Hash-shaped values from the access-group row only; the list data source does not confirm the corresponding key rows.
  * `created_at`
  * `created_by`
  * `updated_at`
  * `updated_by`
