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
* `model_max_budget` - JSON string of per-model budget configuration.
