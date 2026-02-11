# data.litellm_prompt - Looks up a prompt by prompt_id
# Note: prompt_id must reference an existing prompt

data "litellm_prompt" "lookup" {
  prompt_id = litellm_prompt.minimal.prompt_id
}

output "ds_prompt_integration" {
  value = data.litellm_prompt.lookup.prompt_integration
}

output "ds_prompt_type" {
  value = data.litellm_prompt.lookup.prompt_type
}

output "ds_prompt_dotprompt_content" {
  value = data.litellm_prompt.lookup.dotprompt_content
}
