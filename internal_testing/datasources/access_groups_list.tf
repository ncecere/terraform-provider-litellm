# data.litellm_access_groups - Lists all access groups

data "litellm_access_groups" "all" {
}

output "ds_access_groups_list" {
  value = data.litellm_access_groups.all
}
