# data.litellm_budgets - Lists all budgets

data "litellm_budgets" "all" {
}

output "ds_budgets_list" {
  value = data.litellm_budgets.all
}
