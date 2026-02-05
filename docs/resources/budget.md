# litellm_budget Resource

Manages a LiteLLM budget configuration. Budgets define spending limits and can be associated with teams or users for cost control.

## Example Usage

### Minimal Example

```hcl
resource "litellm_budget" "basic" {
  budget_id  = "monthly-dev-budget"
  max_budget = 100.0
}
```

### Full Example

```hcl
resource "litellm_budget" "production" {
  budget_id        = "production-budget"
  max_budget       = 5000.0
  budget_duration  = "monthly"
  tpm_limit        = 500000
  rpm_limit        = 5000
  max_parallel_requests = 50
  
  soft_budget = 4000.0  # Alert at 80%
  
  model_max_budget = jsonencode({
    "gpt-4"           = 3000.0
    "gpt-3.5-turbo"   = 1000.0
    "claude-3-sonnet" = 1000.0
  })
}
```

### Budget with Reset Schedule

```hcl
resource "litellm_budget" "weekly_team" {
  budget_id       = "weekly-team-budget"
  max_budget      = 500.0
  budget_duration = "weekly"
  soft_budget     = 400.0
}
```

### Per-Model Budget Limits

```hcl
resource "litellm_budget" "model_specific" {
  budget_id  = "model-controlled-budget"
  max_budget = 2000.0
  
  model_max_budget = jsonencode({
    "gpt-4"         = 1000.0  # Expensive model gets half
    "gpt-3.5-turbo" = 800.0
    "claude-2"      = 200.0
  })
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `budget_id` - (Required) Unique identifier for the budget.
* `max_budget` - (Required) Maximum budget amount in dollars.

### Optional Arguments

* `budget_duration` - (Optional) Duration for budget reset. Valid values: `daily`, `weekly`, `monthly`.
* `soft_budget` - (Optional) Soft limit for budget alerts (triggers warning but doesn't block).
* `tpm_limit` - (Optional) Tokens per minute limit.
* `rpm_limit` - (Optional) Requests per minute limit.
* `max_parallel_requests` - (Optional) Maximum number of parallel requests allowed.
* `model_max_budget` - (Optional) JSON string mapping model names to their individual budget limits.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The unique identifier for this budget (same as budget_id).
* `created_at` - Timestamp when the budget was created.
* `updated_at` - Timestamp when the budget was last updated.

## Import

Budgets can be imported using the budget ID:

```shell
terraform import litellm_budget.example production-budget
```

## Notes

- Budgets can be shared across multiple teams or users
- The soft_budget triggers alerts but doesn't block requests
- max_budget is a hard limit that will block requests when exceeded
- Budget duration determines when the spend counter resets
- Use model_max_budget to control spending on expensive models
