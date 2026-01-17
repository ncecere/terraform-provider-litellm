# litellm_keys Data Source

Retrieves a list of LiteLLM API keys.

## Example Usage

### Minimal Example

```hcl
data "litellm_keys" "all" {}
```

### Filter by Team

```hcl
data "litellm_keys" "team_keys" {
  team_id = "team-xxxxxxxxxxxx"
}

output "team_key_count" {
  value = length(data.litellm_keys.team_keys.keys)
}
```

### Full Example

```hcl
data "litellm_keys" "all" {}

# Find keys with high spend
locals {
  high_spend_keys = [
    for k in data.litellm_keys.all.keys : k
    if k.spend > 100
  ]
}

output "high_spend_key_aliases" {
  value = [for k in local.high_spend_keys : k.key_alias]
}
```

## Argument Reference

The following arguments are supported:

* `team_id` - (Optional) Filter keys by team ID.
* `user_id` - (Optional) Filter keys by user ID.

## Attribute Reference

The following attributes are exported:

* `id` - Placeholder identifier.
* `keys` - List of key objects, each containing:
  * `key_alias` - The human-readable alias.
  * `team_id` - Associated team ID.
  * `user_id` - Associated user ID.
  * `max_budget` - Maximum budget.
  * `spend` - Current spend.
  * `blocked` - Whether the key is blocked.

## Notes

- Full key values are not exposed for security
- Use filters to narrow down results in large deployments
