# data.litellm_budgets - Lists all budgets

data "litellm_budgets" "all" {
  depends_on = [litellm_budget.minimal]
}

output "ds_budgets_list" {
  value = data.litellm_budgets.all
}
