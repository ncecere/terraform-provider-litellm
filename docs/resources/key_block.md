# litellm_key_block Resource

Manages the blocked state of a LiteLLM API key. Creating this resource blocks the key; destroying it unblocks the key.

## Example Usage

```hcl
resource "litellm_key" "example" {
}

resource "litellm_key_block" "block_key" {
  key = litellm_key.example.key
}
```

## Argument Reference

- `key` - (Required, Sensitive, ForceNew) The API key token to block. Changing this forces creation of a new resource.

## Attribute Reference

- `id` - The ID of this resource.
- `blocked` - Whether the key is currently blocked.

## Import

Import using the key token:

```shell
terraform import litellm_key_block.example <key-token>
```
