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
  * `model_max_budget` - Canonical JSON object mapping model names to LiteLLM `GenericBudgetConfig` objects.

## LiteLLM v1.98 Behavior

Stored tags expose budget fields through the nested `litellm_budget_table` relation. Missing relations and null fields become Terraform null; empty model maps remain `"{}"`; malformed relations or fields fail the complete read. TPM/RPM integers are decoded exactly, models and tags are sorted deterministically, and single/list budget projections use the same decoder. Historical finite numeric scalar model budgets created through earlier provider examples remain readable as a compatibility exception; all other entries must be valid `GenericBudgetConfig` objects.

`/tag/list` can also return dynamic names reconstructed from historical spend. Those items have no stored budget relation and therefore return null budget fields. A deleted, historically used name can reappear this way. LiteLLM does not include tag spend in this response, so no authoritative `spend` attribute is available.
