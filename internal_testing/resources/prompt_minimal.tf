# litellm_prompt - Minimal
# Only required attributes; omitted environment preserves the legacy development default.

resource "litellm_prompt" "minimal" {
  prompt_id          = "test-prompt-minimal"
  prompt_integration = "dotprompt"
}

data "litellm_prompt" "minimal" {
  prompt_id   = litellm_prompt.minimal.prompt_id
  environment = litellm_prompt.minimal.environment
}

data "litellm_prompts" "development" {
  environment = "development"
  depends_on  = [litellm_prompt.minimal]

  lifecycle {
    postcondition {
      condition     = contains([for prompt in self.prompts : prompt.prompt_id], litellm_prompt.minimal.prompt_id)
      error_message = "The environment-scoped prompt inventory omitted the managed prompt."
    }
  }
}

output "prompt_minimal_id" {
  value = litellm_prompt.minimal.id
}

output "prompt_minimal_version" {
  value = litellm_prompt.minimal.version
}
