# data.litellm_organizations - Lists all organizations

data "litellm_organizations" "all" {
}

output "ds_organizations_list" {
  value = data.litellm_organizations.all
}
