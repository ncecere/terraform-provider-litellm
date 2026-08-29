# Canonical v2.0.1-compatible prompt upgrade fixture.
resource "litellm_prompt" "upgrade" {
  prompt_id          = "issue210-prompt-upgrade"
  prompt_integration = "dotprompt"
}

output "issue210_prompt_upgrade_id" {
  value = litellm_prompt.upgrade.id
}
