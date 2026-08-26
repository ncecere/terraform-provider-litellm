variable "replacement_phase" {
  type    = string
  default = "before"

  validation {
    condition     = contains(["before", "after"], var.replacement_phase)
    error_message = "Replacement phase must be before or after."
  }
}

resource "litellm_model" "replacement" {
  model_name          = "issue210-model-replacement"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
  tpm                 = var.replacement_phase == "before" ? 1000 : null

  depends_on = [litellm_team.replacement_dependency]

  lifecycle {
    create_before_destroy = true
  }
}

resource "litellm_team" "replacement_dependency" {
  team_alias = "issue210-model-replacement-dependency"
}
