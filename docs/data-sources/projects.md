# litellm_projects Data Source

Fetches LiteLLM Projects with authoritative nested budget inventories.

## Example Usage

```hcl
data "litellm_projects" "all" {}

output "project_budgets" {
  value = {
    for project in data.litellm_projects.all.projects :
    project.project_id => project.max_budget
  }
}
```

## Attribute Reference

- `id` - Stable data-source identifier.
- `projects` - Deterministically ID-sorted objects containing:
  - `project_id` / `project_alias` / `description` / `team_id`
  - `blocked` / `spend`
  - `budget_id`
  - `max_budget` / `soft_budget`
  - `budget_duration`
  - `tpm_limit` / `rpm_limit`
  - `max_parallel_requests`
  - `model_rpm_limit` / `model_tpm_limit`
  - `created_at` / `updated_at`
  - `created_by` / `updated_by`

Every exposed budget value is decoded from `litellm_budget_table`. Structured `model_max_budget` remains deferred rather than being flattened inaccurately. Null/absent relations produce null inventory values; malformed relations or inconsistent budget IDs fail the whole snapshot. Exact integer limits above `2^53` are preserved.
