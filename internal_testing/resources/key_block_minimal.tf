# litellm_key_block - Minimal
# Blocks an API key

resource "litellm_key_block" "minimal" {
  key = litellm_key.minimal.key
}

output "key_block_minimal_id" {
  value = litellm_key_block.minimal.id
}

output "key_block_minimal_blocked" {
  value = litellm_key_block.minimal.blocked
}
