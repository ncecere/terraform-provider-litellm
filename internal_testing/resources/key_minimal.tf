# litellm_key - Minimal
# No required attributes beyond computed ones

resource "litellm_key" "minimal" {
}

output "key_minimal_id" {
  value = litellm_key.minimal.id
}

output "key_minimal_key" {
  value     = litellm_key.minimal.key
  sensitive = true
}
