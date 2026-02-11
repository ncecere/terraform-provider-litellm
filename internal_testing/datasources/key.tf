# data.litellm_key - Looks up a key by its value
# Note: key must reference an existing API key

data "litellm_key" "lookup" {
  key = litellm_key.minimal.key
}

output "ds_key_alias" {
  value = data.litellm_key.lookup.key_alias
}

output "ds_key_models" {
  value = data.litellm_key.lookup.models
}

output "ds_key_max_budget" {
  value = data.litellm_key.lookup.max_budget
}

output "ds_key_spend" {
  value = data.litellm_key.lookup.spend
}

output "ds_key_blocked" {
  value = data.litellm_key.lookup.blocked
}

output "ds_key_tags" {
  value = data.litellm_key.lookup.tags
}

output "ds_key_metadata" {
  value = data.litellm_key.lookup.metadata
}
