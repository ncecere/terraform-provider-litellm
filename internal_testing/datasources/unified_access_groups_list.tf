# data.litellm_unified_access_groups - Lists all unified access groups

data "litellm_unified_access_groups" "all" {
  depends_on = [litellm_unified_access_group.minimal]
}

output "ds_unified_access_groups_list" {
  value = data.litellm_unified_access_groups.all
}
