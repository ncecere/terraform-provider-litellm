# data.litellm_access_group - Looks up an access group by name
# Note: access_group must reference an existing access group

data "litellm_access_group" "lookup" {
  access_group = litellm_access_group.minimal.access_group
}

output "ds_access_group_model_names" {
  value = data.litellm_access_group.lookup.model_names
}
