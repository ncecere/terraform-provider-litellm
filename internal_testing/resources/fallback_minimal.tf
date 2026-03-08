# litellm_fallback - Minimal (self-contained: models + one fallback)

resource "litellm_model" "fallback_minimal_primary" {
  model_name          = "smoke-fallback-minimal-primary"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

resource "litellm_model" "fallback_minimal_fallback" {
  model_name          = "smoke-fallback-minimal-fallback"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

resource "litellm_fallback" "minimal" {
  model           = litellm_model.fallback_minimal_primary.model_name
  fallback_models = [litellm_model.fallback_minimal_fallback.model_name]
  fallback_type   = "general"
}

output "fallback_minimal_model" {
  value = litellm_fallback.minimal.model
}
