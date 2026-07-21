# data.litellm_unified_access_group - Looks up a unified access group by id
# Note: references the group created by resources/unified_access_group_minimal.tf

data "litellm_unified_access_group" "lookup" {
  access_group_id = litellm_unified_access_group.minimal.id
}

output "ds_unified_access_group_name" {
  value = data.litellm_unified_access_group.lookup.access_group_name
}
