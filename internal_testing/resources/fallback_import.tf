# litellm_fallback - Real special-identity composite import lifecycle.
# The acceptance harness applies the seed, removes only its Terraform state,
# imports the same remote fallback under the imported address, checks no drift,
# and destroys it.

variable "fallback_import_phase" {
  type    = string
  default = "seed"

  validation {
    condition     = contains(["seed", "imported"], var.fallback_import_phase)
    error_message = "Fallback import phase must be seed or imported."
  }
}

locals {
  fallback_import_model = "smoke-fallback:8b?variant=50%-雪"
}

resource "litellm_model" "fallback_import_primary" {
  model_name          = local.fallback_import_model
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

resource "litellm_model" "fallback_import_secondary" {
  model_name          = "smoke-fallback-import-secondary"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

resource "litellm_fallback" "fallback_import_seed" {
  count = var.fallback_import_phase == "seed" ? 1 : 0

  model           = litellm_model.fallback_import_primary.model_name
  fallback_models = [litellm_model.fallback_import_secondary.model_name]
  fallback_type   = "general"
}

resource "litellm_fallback" "fallback_imported" {
  count = var.fallback_import_phase == "imported" ? 1 : 0

  model           = litellm_model.fallback_import_primary.model_name
  fallback_models = [litellm_model.fallback_import_secondary.model_name]
  fallback_type   = "general"
}
