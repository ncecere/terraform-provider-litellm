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

* `id` - Stable historical identifier (`keys`), unchanged by filters.
* `keys` - List of key objects, each containing:
  * `key_name` - A SHA256 management hash, never LiteLLM's suffix-bearing `key_name` value or the raw key.
  * `key_alias` - The human-readable alias.
  * `team_id` - Associated team ID.
  * `user_id` - Associated user ID.
  * `max_budget` - Maximum budget.
  * `spend` - Current spend.
  * `tpm_limit` - Tokens per minute limit.
  * `rpm_limit` - Requests per minute limit.
  * `blocked` - Whether the key is blocked.

## Notes

- The provider requests LiteLLM's full-object response. Object entries use only the SHA256 management hash returned in `token`; LiteLLM's `key_name` is ignored because v1.98 includes raw-key suffix characters. String-union entries are already bare SHA256 management hashes in LiteLLM v1.98 and are never hashed again. Valid 64-hex hashes are normalized to lowercase so switching between string and object representations keeps identical state.
- Malformed, redacted, or otherwise unexpected string entries, and objects without a valid token management hash, fail safely without echoing the value or exposing a suffix.
- All `/key/list` pages are retrieved at LiteLLM's maximum page size. Concurrent count/page shifts restart the bounded listing at page 1; persistent inconsistency, repeated pages/items, malformed data, truncation, and over-limit pagination fail rather than returning a partial inventory.
- Results are sorted deterministically by key identity.
- Use filters to narrow down results in large deployments.
