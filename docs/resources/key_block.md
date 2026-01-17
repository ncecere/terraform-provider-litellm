# litellm_key_block Resource

Manages the blocked status of a LiteLLM API key. This resource allows you to block or unblock an existing API key.

## Example Usage

### Minimal Example

```hcl
resource "litellm_key_block" "blocked_key" {
  key = "sk-xxxxxxxxxxxx"
}
```

### Full Example with Key Reference

```hcl
# Create a key
resource "litellm_key" "api_key" {
  key_alias  = "production-key"
  team_id    = litellm_team.prod_team.team_id
  max_budget = 100.0
}

# Block the key if needed (e.g., for security incident)
resource "litellm_key_block" "block_production" {
  key = litellm_key.api_key.key
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `key` - (Required) The API key to block.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The ID of the key block resource (same as key).

## Import

Key blocks can be imported using the key:

```shell
terraform import litellm_key_block.example sk-xxxxxxxxxxxx
```

## Notes

- Blocking a key prevents it from being used for API calls
- This resource creates a "block" action - removing the resource will unblock the key
- Use this for temporary security incidents or compliance requirements
