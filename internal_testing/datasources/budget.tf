# data.litellm_budget - Looks up a budget by budget_id
# Note: budget_id must reference an existing budget

data "litellm_budget" "lookup" {
  budget_id = litellm_budget.full.budget_id
}

output "ds_budget_max_budget" {
  value = data.litellm_budget.lookup.max_budget
}

output "ds_budget_soft_budget" {
  value = data.litellm_budget.lookup.soft_budget
}

output "ds_budget_tpm_limit" {
  value = data.litellm_budget.lookup.tpm_limit
}

output "ds_budget_rpm_limit" {
  value = data.litellm_budget.lookup.rpm_limit
}

output "ds_budget_duration" {
  value = data.litellm_budget.lookup.budget_duration
}
