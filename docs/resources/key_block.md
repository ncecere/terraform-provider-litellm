# litellm_key_block Resource

Manages the blocked state of a LiteLLM API key. Creating this resource blocks the key; destroying it unblocks the key.

The resource ID is always a non-sensitive `sha256:<64-hex>` management identifier. LiteLLM v1.98 accepts the corresponding bare hash for block, read, and unblock operations, so this resource hashes raw inputs locally and sends only the bare hash to LiteLLM.

## Recommended: hash-only identity

Use the hashed ID exported by `litellm_key` when possible. The raw key is not copied into this resource's plan or state.

```hcl
resource "litellm_key" "example" {
}

resource "litellm_key_block" "block_key" {
  key_hash = litellm_key.example.id
}
```

## Backward-compatible stateful input

Existing configurations using `key` remain supported. The value remains sensitive, but—as before—is stored in Terraform state. The provider uses only its SHA256 hash as the resource ID and in every LiteLLM request.

```hcl
resource "litellm_key_block" "legacy" {
  key = var.litellm_key
}
```

## Argument Reference

Exactly one identity argument is required:

- `key_hash` - (Optional) Preferred `sha256:<64-hex>` management identifier, such as `litellm_key.example.id`. LiteLLM receives only the bare hash.
- `key` - (Optional, Sensitive) Backward-compatible stateful API key token. The token remains in Terraform state.

Changing the target key forces replacement. Switching between `key` and `key_hash` for the same hash updates state in place without unblocking the remote key.

## Attribute Reference

- `id` - Canonical non-sensitive `sha256:<64-hex>` management identifier.
- `blocked` - Whether the key is currently blocked.

## Import

### Hash-only import

Recommended for existing keys when the verification-token hash is known:

```shell
terraform import litellm_key_block.example 'sha256:<64-character-sha256>'
```

A bare 64-character SHA256 hash is also accepted. Configure the resource with the corresponding `key_hash`.

### Backward-compatible raw import

Raw-token imports remain supported and populate the sensitive `key` attribute:

```shell
terraform import litellm_key_block.example '<key-token>'
```

Hash-only import is the preferred import path because no raw token enters this resource's configuration or state.

## State migration and security

State created by earlier provider versions used the raw token as the non-sensitive resource ID. The provider automatically migrates that ID to `sha256:<64-hex>` without recreating or unblocking the key. Existing `key` configuration remains compatible.

Historical state files and backups may still contain the old raw ID. Existing stateful `key` configurations also retain the token in their sensitive state attribute. Replacing `key` in configuration with the matching `key_hash` updates current state in place, but does not purge historical backend versions; expire those according to the backend's retention controls.

SHA256 management IDs are unsalted identifiers, not password protection. Use cryptographically random, high-entropy API keys because weak values can be guessed offline from their persisted hashes.
