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

For a write-only key, use its non-sensitive management ID instead of reintroducing the token into data-source state:

```hcl
data "litellm_key" "write_only" {
  key_hash = litellm_key.write_only.id
}
```

## Argument Reference

Exactly one lookup argument is required:

* `key` - (Optional, Sensitive) Raw API key value to look up.
* `key_hash` - (Optional) A `sha256:<64-hex>` management identifier, such as the `id` exported by a `litellm_key` using `key_wo`. The provider sends only the bare hash accepted by LiteLLM and does not require the raw token.

## Attribute Reference

* `id` - Non-sensitive SHA256 management identifier for the key; the raw token is never copied into this attribute.
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
* `router_settings` - Complete key-specific LiteLLM v1.98.0 router-settings document. Scalar fields are typed; heterogeneous objects and ordered arrays are returned as canonical JSON strings. See the `litellm_key` resource documentation for the nested fields.

## Notes

- The `key` argument is marked as sensitive and will not appear in plan output. It is still an input stored in data-source state; use `key_hash` for write-only keys.
- Use this data source to check key status and budget information.
