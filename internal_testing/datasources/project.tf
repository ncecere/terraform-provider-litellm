data "litellm_project" "lookup" {
  id = litellm_project.minimal.id
}

output "ds_project_budget" {
  value = {
    budget_id             = data.litellm_project.lookup.budget_id
    max_budget            = data.litellm_project.lookup.max_budget
    soft_budget           = data.litellm_project.lookup.soft_budget
    budget_duration       = data.litellm_project.lookup.budget_duration
    tpm_limit             = data.litellm_project.lookup.tpm_limit
    rpm_limit             = data.litellm_project.lookup.rpm_limit
    max_parallel_requests = data.litellm_project.lookup.max_parallel_requests
  }
}
