# litellm_access_groups Data Source

Retrieves a list of all LiteLLM access groups.

## Example Usage

### Minimal Example

```hcl
data "litellm_access_groups" "all" {}
```

### Full Example

```hcl
data "litellm_access_groups" "all" {}

output "access_group_count" {
  value = length(data.litellm_access_groups.all.access_groups)
}

output "access_group_names" {
  value = [for g in data.litellm_access_groups.all.access_groups : g.access_group_name]
}

# Find groups with GPT-4 access
locals {
  gpt4_groups = [
    for g in data.litellm_access_groups.all.access_groups : g
    if contains(g.members, "gpt-4")
  ]
}

output "groups_with_gpt4" {
  value = [for g in local.gpt4_groups : g.access_group_name]
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

The following attributes are exported:

* `id` - Placeholder identifier.
* `access_groups` - List of access group objects, each containing:
  * `access_group_id` - The unique identifier.
  * `access_group_name` - The human-readable name.
  * `description` - Description.
  * `members` - List of model names.
  * `created_at` - Creation timestamp.
  * `updated_at` - Last update timestamp.
