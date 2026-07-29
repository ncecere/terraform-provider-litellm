# litellm_key - Fallbacks
# Tests both router_settings_fallbacks and router_settings_context_window_fallbacks.
# Model names are plain strings — no litellm_model resources required.

resource "litellm_key" "with_fallbacks" {
  key_alias = "test-key-fallbacks"
  models    = ["gpt-4o", "gpt-4o-mini", "gpt-3.5-turbo", "claude-3-5-sonnet-20241022", "claude-3-haiku-20240307"]

  router_settings_fallbacks = {
    "gpt-4o" = ["gpt-4o-mini", "gpt-3.5-turbo"]
  }

  router_settings_context_window_fallbacks = {
    "gpt-4o" = ["gpt-4o-mini", "gpt-3.5-turbo"]
  }
}

output "key_fallbacks_id" {
  value = litellm_key.with_fallbacks.id
}

output "key_fallbacks_router_settings_fallbacks" {
  value = litellm_key.with_fallbacks.router_settings_fallbacks
}

output "key_fallbacks_router_settings_context_window_fallbacks" {
  value = litellm_key.with_fallbacks.router_settings_context_window_fallbacks
}

output "key_fallbacks_key" {
  value     = litellm_key.with_fallbacks.key
  sensitive = true
}
