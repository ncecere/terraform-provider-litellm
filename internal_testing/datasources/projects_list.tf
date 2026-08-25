data "litellm_projects" "all" {}

output "ds_projects_nested_budgets" {
  value = {
    for project in data.litellm_projects.all.projects : project.project_id => {
      budget_id       = project.budget_id
      max_budget      = project.max_budget
      budget_duration = project.budget_duration
      tpm_limit       = project.tpm_limit
      rpm_limit       = project.rpm_limit
    }
  }
}
