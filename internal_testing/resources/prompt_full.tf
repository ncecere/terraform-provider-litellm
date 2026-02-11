# litellm_prompt - Full
# All attributes populated

resource "litellm_prompt" "full" {
  prompt_id          = "test-prompt-full"
  prompt_integration = "dotprompt"
  prompt_type        = "db"

  dotprompt_content = <<-EOT
    ---
    model: gpt-4o
    ---
    You are a helpful assistant. Answer the user's question concisely.
    {{question}}
  EOT

  ignore_prompt_manager_model           = false
  ignore_prompt_manager_optional_params = false
}

output "prompt_full_id" {
  value = litellm_prompt.full.id
}
