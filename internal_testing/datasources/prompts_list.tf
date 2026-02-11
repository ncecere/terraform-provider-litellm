# data.litellm_prompts - Lists all prompts

data "litellm_prompts" "all" {
}

output "ds_prompts_list" {
  value = data.litellm_prompts.all
}
