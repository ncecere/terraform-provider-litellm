# litellm_fallback - Minimal (self-contained: models + one fallback)

resource "litellm_model" "fallback_minimal_primary" {
  model_name          = "smoke-fallback-minimal-primary"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
  mode                = "chat"
  access_groups       = []
  additional_litellm_params = {
    allow_client_keepalive_override = "false"
    use_in_pass_through             = "false"
    use_litellm_proxy               = "false"
    use_xai_oauth                   = "false"
  }
}

resource "litellm_model" "fallback_minimal_fallback" {
  model_name          = "smoke-fallback-minimal-fallback"
  custom_llm_provider = "openai"
  base_model          = "gpt-4o-mini"
  mode                = "chat"
  access_groups       = []
  additional_litellm_params = {
    allow_client_keepalive_override = "false"
    use_in_pass_through             = "false"
    use_litellm_proxy               = "false"
    use_xai_oauth                   = "false"
  }
}

resource "litellm_fallback" "minimal" {
  model           = litellm_model.fallback_minimal_primary.model_name
  fallback_models = [litellm_model.fallback_minimal_fallback.model_name]
  fallback_type   = "general"
}

output "fallback_minimal_model" {
  value = litellm_fallback.minimal.model
}
