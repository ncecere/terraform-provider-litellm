# data.litellm_tag - Looks up a tag by name
# Note: name must reference an existing tag

data "litellm_tag" "lookup" {
  name = litellm_tag.minimal.name
}

output "ds_tag_description" {
  value = data.litellm_tag.lookup.description
}

output "ds_tag_models" {
  value = data.litellm_tag.lookup.models
}

output "ds_tag_max_budget" {
  value = data.litellm_tag.lookup.max_budget
}
