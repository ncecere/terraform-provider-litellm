# litellm_key_block - Hash-only identity
# Uses a distinct target so the legacy stateful case remains covered.

resource "litellm_key" "key_block_hash_target" {
  key_alias = "acceptance-key-block-hash"
  models    = ["all-proxy-models"]
}

resource "litellm_key_block" "hash" {
  key_hash = litellm_key.key_block_hash_target.id
}

output "key_block_hash_id" {
  value = litellm_key_block.hash.id
}

output "key_block_hash_blocked" {
  value = litellm_key_block.hash.blocked
}
