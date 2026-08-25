# litellm_budget - Full
# All attributes populated

resource "litellm_budget" "full" {
  budget_id             = "test-budget-full"
  max_budget            = 1000.0
  soft_budget           = 800.0
  max_parallel_requests = 20
  tpm_limit             = 100000
  rpm_limit             = 1000
  budget_duration       = "30d"
  model_max_budget = jsonencode({
    "gpt-4o"      = { max_budget = 500.0, budget_duration = "30d" }
    "gpt-4o-mini" = { budget_limit = 200.0, time_period = "30d" }
  })
}

output "budget_full_id" {
  value = litellm_budget.full.id
}
