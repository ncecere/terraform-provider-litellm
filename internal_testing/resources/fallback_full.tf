# litellm_fallback - Full (self-contained: models + all three fallback types)

resource "litellm_model" "fallback_full_primary" {
  model_name          = "smoke-fallback-full-primary"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

resource "litellm_model" "fallback_full_fallback" {
  model_name          = "smoke-fallback-full-fallback"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
}

resource "litellm_fallback" "full_general" {
  model          = litellm_model.fallback_full_primary.model_name
  fallback_models = [litellm_model.fallback_full_fallback.model_name]
  fallback_type  = "general"
}

resource "litellm_fallback" "full_context_window" {
  model          = litellm_model.fallback_full_primary.model_name
  fallback_models = [litellm_model.fallback_full_fallback.model_name]
  fallback_type  = "context_window"
}

resource "litellm_fallback" "full_content_policy" {
  model          = litellm_model.fallback_full_primary.model_name
  fallback_models = [litellm_model.fallback_full_fallback.model_name]
  fallback_type  = "content_policy"
}

output "fallback_full_model" {
  value = litellm_fallback.full_general.model
}

output "fallback_full_fallback_models" {
  value = litellm_fallback.full_general.fallback_models
}

output "fallback_full_types" {
  value = {
    general        = litellm_fallback.full_general.fallback_type
    context_window = litellm_fallback.full_context_window.fallback_type
    content_policy = litellm_fallback.full_content_policy.fallback_type
  }
}
