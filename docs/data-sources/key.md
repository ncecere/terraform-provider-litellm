# litellm_key Data Source

Retrieves information about a specific LiteLLM API key.

## Example Usage

### Minimal Example

```hcl
data "litellm_key" "existing" {
  key = "sk-xxxxxxxxxxxx"
}
```

### Full Example

```hcl
data "litellm_key" "production" {
  key = var.existing_api_key
}

output "key_info" {
  value = {
    alias      = data.litellm_key.production.key_alias
    team_id    = data.litellm_key.production.team_id
    max_budget = data.litellm_key.production.max_budget
    spend      = data.litellm_key.production.spend
  }
}
```

## Argument Reference

The following arguments are supported:

* `key` - (Required) The API key to retrieve information about.

## Attribute Reference

The following attributes are exported:

* `id` - The unique identifier of the key.
* `key_alias` - The human-readable alias for the key.
* `team_id` - The team ID associated with this key.
* `user_id` - The user ID associated with this key.
* `models` - List of models that can be used with this key.
* `max_budget` - Maximum budget for this key.
* `spend` - Current spend for this key.
* `tpm_limit` - Tokens per minute limit.
* `rpm_limit` - Requests per minute limit.
* `blocked` - Whether the key is blocked.

## Notes

- This data source does not expose the full key value for security reasons
- Use this to check key status and budget information
