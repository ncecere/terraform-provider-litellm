# litellm_keys Data Source

Retrieves a list of LiteLLM API keys.

## Example Usage

```hcl
data "litellm_keys" "all" {}

output "key_count" {
  value = length(data.litellm_keys.all.keys)
}
```

### Filter by Team

```hcl
data "litellm_keys" "team_keys" {
  team_id = "team-xxxxxxxxxxxx"
}
```

### Filter by User

```hcl
data "litellm_keys" "user_keys" {
  user_id = "user-xxxxxxxxxxxx"
}
```

## Argument Reference

* `team_id` - (Optional) Filter keys by team ID.
* `user_id` - (Optional) Filter keys by user ID.

## Attribute Reference

* `id` - Placeholder identifier.
* `keys` - List of key objects, each containing:
  * `key_name` - The hashed key name (not the actual key value).
  * `key_alias` - The human-readable alias.
  * `team_id` - Associated team ID.
  * `user_id` - Associated user ID.
  * `max_budget` - Maximum budget.
  * `spend` - Current spend.
  * `tpm_limit` - Tokens per minute limit.
  * `rpm_limit` - Requests per minute limit.
  * `blocked` - Whether the key is blocked.

## Notes

- Full key values are not exposed for security reasons.
- Use filters to narrow down results in large deployments.
