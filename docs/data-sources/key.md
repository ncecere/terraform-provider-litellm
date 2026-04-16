# litellm_key Data Source

Retrieves information about a specific LiteLLM API key.

## Example Usage

```hcl
data "litellm_key" "existing" {
  key = var.existing_api_key
}

output "key_info" {
  value = {
    alias           = data.litellm_key.existing.key_alias
    team_id         = data.litellm_key.existing.team_id
    max_budget      = data.litellm_key.existing.max_budget
    num_retries     = data.litellm_key.existing.router_settings.num_retries
    timeout         = data.litellm_key.existing.router_settings.timeout
    blocked         = data.litellm_key.existing.blocked
  }
}
```

## Argument Reference

* `key` - (Required, Sensitive) The API key value to look up.

## Attribute Reference

* `id` - The unique identifier of the key.
* `key_alias` - The human-readable alias for the key.
* `team_id` - The team ID associated with this key.
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
* `router_settings` - Router configuration for the key. Contains all 21 fields matching the resource block: `routing_strategy`, `num_retries`, `timeout`, `stream_timeout`, `max_fallbacks`, `allowed_fails`, `cooldown_time`, `retry_after`, `default_max_parallel_requests`, `enable_pre_call_checks`, `set_verbose`, `enable_tag_filtering`, `tag_filtering_match_any`, `disable_cooldowns`, `routing_strategy_args`, `model_group_alias`, `default_litellm_params`, `fallbacks`, `context_window_fallbacks`, `content_policy_fallbacks`, and `retry_policy`.
* `blocked` - Whether the key is blocked.

## Notes

- The `key` argument is marked as sensitive and will not appear in plan output.
- Use this data source to check key status and budget information.
