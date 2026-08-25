# Apply once with the default, then apply with
# -var='clear_project_budget=true' to exercise explicit budget/reset clears.
variable "clear_project_budget" {
  type    = bool
  default = false
}

resource "litellm_project" "budget_clear" {
  project_alias = "test-project-budget-clear"
  team_id       = litellm_team.full.id

  max_budget            = var.clear_project_budget ? null : 100.0
  soft_budget           = var.clear_project_budget ? null : 80.0
  budget_duration       = var.clear_project_budget ? null : "7d"
  tpm_limit             = var.clear_project_budget ? null : 10000
  rpm_limit             = var.clear_project_budget ? null : 100
  max_parallel_requests = var.clear_project_budget ? null : 10
}
