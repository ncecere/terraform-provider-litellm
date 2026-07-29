# data.litellm_key - Fallbacks
# Reads back the key created by resources/key_fallbacks_full.tf, so the outputs below
# assert the resource -> API -> data source round trip for both fallback maps.

data "litellm_key" "fallbacks" {
  key = litellm_key.with_fallbacks.key
}

output "ds_key_fallbacks_router_settings_fallbacks" {
  value = data.litellm_key.fallbacks.router_settings_fallbacks
}

output "ds_key_fallbacks_router_settings_context_window_fallbacks" {
  value = data.litellm_key.fallbacks.router_settings_context_window_fallbacks
}
