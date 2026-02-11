# litellm_budgets Data Source

Retrieves a list of all LiteLLM budget configurations.

## Example Usage

```hcl
data "litellm_budgets" "all" {}

output "budget_count" {
  value = length(data.litellm_budgets.all.budgets)
}

# Find high-value budgets
locals {
  high_value_budgets = [
    for b in data.litellm_budgets.all.budgets : b
    if b.max_budget > 1000
  ]
}

output "high_value_budget_ids" {
  value = [for b in local.high_value_budgets : b.budget_id]
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

* `id` - Placeholder identifier.
* `budgets` - List of budget objects, each containing:
  * `budget_id` - The unique identifier.
  * `max_budget` - Maximum budget amount.
  * `soft_budget` - Soft budget limit.
  * `max_parallel_requests` - Maximum parallel requests allowed.
  * `tpm_limit` - Tokens per minute limit.
  * `rpm_limit` - Requests per minute limit.
  * `budget_duration` - Budget reset duration.
  * `model_max_budget` - JSON string of per-model budget limits.
