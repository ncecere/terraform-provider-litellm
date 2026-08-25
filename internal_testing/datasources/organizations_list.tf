# data.litellm_organizations - Lists all organizations

data "litellm_organizations" "all" {
}

output "ds_organizations_list" {
  value = data.litellm_organizations.all
}

output "ds_organization_nested_budgets" {
  value = {
    for organization in data.litellm_organizations.all.organizations : organization.organization_id => {
      budget_id       = organization.budget_id
      max_budget      = organization.max_budget
      budget_duration = organization.budget_duration
      tpm_limit       = organization.tpm_limit
      rpm_limit       = organization.rpm_limit
    }
  }
}
