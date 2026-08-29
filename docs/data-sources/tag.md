# litellm_tag Data Source

Retrieves information about a specific LiteLLM tag.

## Example Usage

```hcl
data "litellm_tag" "existing" {
  name = "production"
}

output "tag_info" {
  value = {
    name        = data.litellm_tag.existing.name
    description = data.litellm_tag.existing.description
    max_budget  = data.litellm_tag.existing.max_budget
    models      = data.litellm_tag.existing.models
  }
}
```

## Argument Reference

* `name` - (Required) The name of the tag to retrieve.

## Attribute Reference

* `id` - The unique identifier of the tag.
* `name` - The tag name.
* `description` - Description of the tag.
* `models` - List of models associated with this tag.
* `budget_id` - Budget ID associated with this tag.
* `max_budget` - Maximum budget in USD.
* `soft_budget` - Soft budget in USD.
* `max_parallel_requests` - Maximum concurrent requests allowed.
* `tpm_limit` - Maximum tokens per minute.
* `rpm_limit` - Maximum requests per minute.
* `budget_duration` - Duration for budget reset (e.g., "daily", "weekly", "monthly").
* `model_max_budget` - Canonical JSON object mapping model names to LiteLLM `GenericBudgetConfig` objects.

## LiteLLM v1.98 Behavior

Budget fields are decoded from the authoritative nested `litellm_budget_table` relation. A missing relation or null field is returned as Terraform null; an empty model map is returned as `"{}"`; malformed relation, numeric, duration, model-list, or JSON shapes fail the read rather than silently retaining an unknown value. Large TPM/RPM integers are decoded exactly, and model names are sorted deterministically. Historical finite numeric scalar model budgets created through earlier provider examples remain readable as a compatibility exception; all other entries must be valid `GenericBudgetConfig` objects.

LiteLLM's tag-info response does not expose tag spend, so this data source cannot publish an authoritative `spend` attribute.
