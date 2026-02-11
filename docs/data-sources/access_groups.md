# litellm_access_groups Data Source

Retrieves a list of all LiteLLM access groups.

## Example Usage

```hcl
data "litellm_access_groups" "all" {}

output "access_group_count" {
  value = length(data.litellm_access_groups.all.access_groups)
}

output "access_group_names" {
  value = [for g in data.litellm_access_groups.all.access_groups : g.access_group]
}

# Find groups containing a specific model
locals {
  gpt4_groups = [
    for g in data.litellm_access_groups.all.access_groups : g
    if contains(g.model_names, "gpt-4-proxy")
  ]
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

* `id` - Placeholder identifier.
* `access_groups` - List of access group objects, each containing:
  * `access_group` - The access group name.
  * `model_names` - List of model names in this access group.
