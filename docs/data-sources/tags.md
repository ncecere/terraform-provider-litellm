# litellm_tags Data Source

Retrieves a list of all LiteLLM tags.

## Example Usage

```hcl
data "litellm_tags" "all" {}

output "tag_count" {
  value = length(data.litellm_tags.all.tags)
}

output "tag_names" {
  value = [for t in data.litellm_tags.all.tags : t.name]
}

# Find tags with budget limits
locals {
  budgeted_tags = [
    for t in data.litellm_tags.all.tags : t
    if t.max_budget != null && t.max_budget > 0
  ]
}

output "budgeted_tag_names" {
  value = [for t in local.budgeted_tags : t.name]
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

* `id` - Placeholder identifier.
* `tags` - List of tag objects, each containing:
  * `name` - The tag name.
  * `description` - Tag description.
  * `models` - List of models associated with this tag.
  * `budget_id` - Budget ID associated with this tag.
  * `max_budget` - Maximum budget in USD.
  * `soft_budget` - Soft budget in USD.
  * `max_parallel_requests` - Maximum concurrent requests allowed.
  * `tpm_limit` - Maximum tokens per minute.
  * `rpm_limit` - Maximum requests per minute.
  * `budget_duration` - Duration for budget reset.
  * `model_max_budget` - JSON string of per-model budget configuration.
