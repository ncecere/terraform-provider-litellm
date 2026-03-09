# data.litellm_fallback - Look up fallback config by model and fallback_type.
# Uses hardcoded names that match fallback_minimal.tf (smoke-fallback-minimal-primary, general).
# The datasource expects that fallback to exist (e.g. from running fallback_minimal.tf in the same
# config). If it is not there, Terraform will report the API error, which is fine.

data "litellm_fallback" "lookup" {
  model         = "smoke-fallback-full-fallback"
  fallback_type = "general"
}

output "ds_fallback_model" {
  value = data.litellm_fallback.lookup.model
}

output "ds_fallback_fallback_models" {
  value = data.litellm_fallback.lookup.fallback_models
}

output "ds_fallback_fallback_type" {
  value = data.litellm_fallback.lookup.fallback_type
}
