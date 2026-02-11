# litellm_budget - Minimal
# No required attributes

resource "litellm_budget" "minimal" {
}

output "budget_minimal_id" {
  value = litellm_budget.minimal.id
}
