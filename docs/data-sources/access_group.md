# litellm_access_group Data Source

Retrieves information about a specific LiteLLM access group.

## Example Usage

### Minimal Example

```hcl
data "litellm_access_group" "existing" {
  access_group_id = "premium-models"
}
```

### Full Example

```hcl
data "litellm_access_group" "enterprise" {
  access_group_id = "enterprise-tier"
}

output "access_group_info" {
  value = {
    name    = data.litellm_access_group.enterprise.access_group_name
    members = data.litellm_access_group.enterprise.members
  }
}

# Create team with same model access
resource "litellm_team" "enterprise_team" {
  team_alias = "enterprise-users"
  models     = data.litellm_access_group.enterprise.members
}
```

## Argument Reference

The following arguments are supported:

* `access_group_id` - (Required) The unique identifier of the access group to retrieve.

## Attribute Reference

The following attributes are exported:

* `id` - The unique identifier of the access group.
* `access_group_id` - The access group ID.
* `access_group_name` - The human-readable name.
* `description` - Description of the access group.
* `members` - List of model names included in this access group.
* `metadata` - JSON string containing additional metadata.
* `created_at` - Creation timestamp.
* `updated_at` - Last update timestamp.
