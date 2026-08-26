variable "replacement_phase" {
  type    = string
  default = "before"

  validation {
    condition     = contains(["before", "after"], var.replacement_phase)
    error_message = "replacement_phase must be before or after."
  }
}

resource "litellm_model" "replacement" {
  model_name          = "issue210-model-replacement"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
  tpm                 = var.replacement_phase == "before" ? 1000 : null

  lifecycle {
    create_before_destroy = true
  }
}

resource "litellm_access_group" "replacement_dependency" {
  access_group = "issue210-model-replacement-dependency"
  model_names  = [litellm_model.replacement.model_name]
}
