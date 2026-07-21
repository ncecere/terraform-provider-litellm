# data.litellm_unified_access_groups - Lists all unified access groups

data "litellm_unified_access_groups" "all" {
}

output "ds_unified_access_groups_list" {
  value = data.litellm_unified_access_groups.all
}
