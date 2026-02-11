# data.litellm_organization - Looks up an organization by organization_id
# Note: organization_id must reference an existing organization

data "litellm_organization" "lookup" {
  organization_id = litellm_organization.minimal.id
}

output "ds_org_alias" {
  value = data.litellm_organization.lookup.organization_alias
}

output "ds_org_models" {
  value = data.litellm_organization.lookup.models
}

output "ds_org_max_budget" {
  value = data.litellm_organization.lookup.max_budget
}

output "ds_org_spend" {
  value = data.litellm_organization.lookup.spend
}

output "ds_org_blocked" {
  value = data.litellm_organization.lookup.blocked
}

output "ds_org_created_at" {
  value = data.litellm_organization.lookup.created_at
}
