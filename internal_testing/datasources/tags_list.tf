# data.litellm_tags - Lists all tags

data "litellm_tags" "all" {
  depends_on = [litellm_tag.minimal]
}

output "ds_tags_list" {
  value = data.litellm_tags.all
}
