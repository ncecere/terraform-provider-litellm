variable "recover_create" {
  type    = bool
  default = false
}

resource "litellm_model" "failed_create" {
  model_name          = "issue210-model-failed-create"
  custom_llm_provider = var.recover_create ? "openai" : "issue210-unsupported"
  base_model          = "gpt-4o-mini"
}
