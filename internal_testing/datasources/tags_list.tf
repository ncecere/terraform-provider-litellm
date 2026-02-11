# data.litellm_tags - Lists all tags

data "litellm_tags" "all" {
}

output "ds_tags_list" {
  value = data.litellm_tags.all
}
