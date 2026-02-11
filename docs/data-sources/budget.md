# litellm_budget Data Source

Retrieves information about a specific LiteLLM budget configuration.

## Example Usage

```hcl
data "litellm_budget" "existing" {
  budget_id = "monthly-budget"
}

output "budget_info" {
  value = {
    max_budget      = data.litellm_budget.existing.max_budget
    soft_budget     = data.litellm_budget.existing.soft_budget
    budget_duration = data.litellm_budget.existing.budget_duration
  }
}

# Use budget configuration for a new team
resource "litellm_team" "new_team" {
  team_alias      = "new-department"
  max_budget      = data.litellm_budget.existing.max_budget * 0.5
  budget_duration = data.litellm_budget.existing.budget_duration
}
```

## Argument Reference

* `budget_id` - (Required) The unique identifier of the budget to retrieve.

## Attribute Reference

* `id` - The unique identifier of the budget.
* `budget_id` - The budget ID.
* `max_budget` - Maximum budget amount.
* `soft_budget` - Soft budget limit for alerts.
* `budget_duration` - Budget reset duration (e.g., "daily", "weekly", "monthly").
* `tpm_limit` - Tokens per minute limit.
* `rpm_limit` - Requests per minute limit.
* `max_parallel_requests` - Maximum parallel requests allowed.
* `model_max_budget` - JSON string of per-model budget limits.
* `budget_reset_at` - Datetime when the budget is next reset.
