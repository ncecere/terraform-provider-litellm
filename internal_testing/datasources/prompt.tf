# data.litellm_prompt - Looks up a prompt by prompt_id
# Note: prompt_id must reference an existing prompt

data "litellm_prompt" "lookup" {
  prompt_id   = litellm_prompt.minimal.prompt_id
  environment = litellm_prompt.minimal.environment
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

output "ds_prompt_version" {
  value = data.litellm_prompt.lookup.version
}

output "ds_prompt_created_at" {
  value = data.litellm_prompt.lookup.created_at
}
