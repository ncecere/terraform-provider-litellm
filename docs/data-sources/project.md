# litellm_project Data Source

Retrieves one LiteLLM Project and its authoritative nested budget controls.

## Example Usage

```hcl
data "litellm_project" "example" {
  id = "proj-abc-123"
}

output "project_budget" {
  value = {
    team_id               = data.litellm_project.example.team_id
    budget_id             = data.litellm_project.example.budget_id
    max_budget            = data.litellm_project.example.max_budget
    max_parallel_requests = data.litellm_project.example.max_parallel_requests
  }
}
```

## Argument Reference

- `id` - (Required String) Project ID.

## Attribute Reference

- `project_alias` / `description` / `team_id`
- `models`
- `metadata` - Metadata excluding dedicated tags and per-model rate maps.
- `tags` - Tags read from v1.98 project metadata.
- `blocked` / `spend`
- `budget_id`
- `max_budget` / `soft_budget`
- `budget_duration`
- `tpm_limit` / `rpm_limit`
- `max_parallel_requests`
- `model_rpm_limit` / `model_tpm_limit`
- `created_at` / `updated_at`
- `created_by` / `updated_by`

Representable budget values come only from `litellm_budget_table`. Structured `model_max_budget` remains deferred rather than being flattened into the incompatible legacy resource type. A missing or null relation produces null budget attributes; malformed relations and inconsistent budget IDs fail the read. Exact integer values above `2^53` are preserved.
