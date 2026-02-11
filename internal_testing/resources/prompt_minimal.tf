# litellm_prompt - Minimal
# Only required attributes

resource "litellm_prompt" "minimal" {
  prompt_id          = "test-prompt-minimal"
  prompt_integration = "dotprompt"
}

output "prompt_minimal_id" {
  value = litellm_prompt.minimal.id
}
