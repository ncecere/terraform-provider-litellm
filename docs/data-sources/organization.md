# litellm_organization Data Source

Retrieves one LiteLLM organization and its authoritative nested budget controls.

## Example Usage

```hcl
data "litellm_organization" "existing" {
  organization_id = "org-xxxxxxxxxxxx"
}

output "organization_budget" {
  value = {
    budget_id            = data.litellm_organization.existing.budget_id
    max_budget           = data.litellm_organization.existing.max_budget
    soft_budget          = data.litellm_organization.existing.soft_budget
    max_parallel_requests = data.litellm_organization.existing.max_parallel_requests
    duration             = data.litellm_organization.existing.budget_duration
  }
}
```

## Argument Reference

- `organization_id` - (Required String) Organization ID.

## Attribute Reference

- `id` / `organization_id` - Organization ID.
- `organization_alias` - Human-readable alias.
- `models` - Accessible models.
- `budget_id` - Associated budget ID.
- `max_budget` / `soft_budget` - Hard and soft budget limits.
- `tpm_limit` / `rpm_limit` - Global rate limits.
- `max_parallel_requests` - Concurrent request limit.
- `model_max_budget` - Legacy per-model budget map shape.
- `budget_duration` - Reset duration.
- `model_rpm_limit` / `model_tpm_limit` - Exact per-model integer limits stored in metadata.
- `metadata` - Metadata excluding dedicated per-model rate maps.
- `spend` - Organization spend.
- `created_at` / `updated_at` - Timestamps.
- `blocked` - Compatibility value `false`; v1.98 has no organization blocked column.
- `tags` - Compatibility empty list; v1.98 has no organization tags column.

Budget values come only from `litellm_budget_table`. A missing or null relation produces null budget attributes, while malformed relations and inconsistent budget IDs fail the read.
