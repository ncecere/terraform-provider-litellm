# litellm_key Data Source

Retrieves information about a specific LiteLLM API key.

## Example Usage

```hcl
data "litellm_key" "existing" {
  key = var.existing_api_key
}

output "key_info" {
  value = {
    alias      = data.litellm_key.existing.key_alias
    team_id    = data.litellm_key.existing.team_id
    max_budget = data.litellm_key.existing.max_budget
    blocked    = data.litellm_key.existing.blocked
  }
}
```

## Argument Reference

* `key` - (Required, Sensitive) The API key value to look up.

## Attribute Reference

* `id` - The unique identifier of the key.
* `key_alias` - The human-readable alias for the key.
* `team_id` - The team ID associated with this key.
* `project_id` - The project ID associated with this key.
* `user_id` - The user ID associated with this key.
* `models` - List of models that can be used with this key.
* `max_budget` - Maximum budget for this key.
* `spend` - Current spend for this key.
* `max_parallel_requests` - Maximum parallel requests allowed.
* `tpm_limit` - Tokens per minute limit.
* `rpm_limit` - Requests per minute limit.
* `budget_duration` - Budget reset duration.
* `soft_budget` - Soft budget limit for warnings.
* `metadata` - Map of metadata for the key.
* `tags` - List of tags for the key.
* `blocked` - Whether the key is blocked.
* `router_settings_fallbacks` - Per-model fallback chain. Keys are primary model names; values are ordered lists of fallback model names.
* `router_settings_context_window_fallbacks` - Per-model context window fallback chain.

## Notes

- The `key` argument is marked as sensitive and will not appear in plan output.
- Use this data source to check key status and budget information.
