# litellm_tags Data Source

Retrieves a list of all LiteLLM tags.

## Example Usage

### Minimal Example

```hcl
data "litellm_tags" "all" {}
```

### Full Example

```hcl
data "litellm_tags" "all" {}

output "tag_count" {
  value = length(data.litellm_tags.all.tags)
}

output "tag_names" {
  value = [for t in data.litellm_tags.all.tags : t.name]
}

# Find environment tags
locals {
  env_tags = [
    for t in data.litellm_tags.all.tags : t
    if can(regex("^(dev|staging|prod)", t.name))
  ]
}

output "environment_tags" {
  value = [for t in local.env_tags : t.name]
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

The following attributes are exported:

* `id` - Placeholder identifier.
* `tags` - List of tag objects, each containing:
  * `name` - The tag name.
  * `description` - Tag description.
  * `created_at` - Creation timestamp.
  * `updated_at` - Last update timestamp.
