# litellm_user Data Source

Retrieves information about a specific LiteLLM user.

## Example Usage

```hcl
data "litellm_user" "existing" {
  user_id = "user-xxxxxxxxxxxx"
}

output "user_info" {
  value = {
    email      = data.litellm_user.existing.user_email
    role       = data.litellm_user.existing.user_role
    max_budget = data.litellm_user.existing.max_budget
    teams      = data.litellm_user.existing.teams
  }
}

# Create key for user
resource "litellm_key" "user_key" {
  user_id    = data.litellm_user.existing.user_id
  key_alias  = "user-${data.litellm_user.existing.user_alias}-key"
  max_budget = data.litellm_user.existing.max_budget
}
```

## Argument Reference

* `user_id` - (Required) The unique identifier of the user to retrieve.

## Attribute Reference

* `id` - The unique identifier of the user.
* `user_id` - The user ID.
* `user_email` - The user's email address.
* `user_alias` - The human-readable alias.
* `user_role` - The user's role (proxy_admin, proxy_admin_viewer, internal_user, internal_user_viewer, team, customer).
* `teams` - List of team IDs the user belongs to.
* `models` - List of models the user can access.
* `max_budget` - Maximum budget for the user.
* `budget_duration` - Budget reset duration.
* `tpm_limit` - Tokens per minute limit.
* `rpm_limit` - Requests per minute limit.
* `metadata` - Map of metadata for the user.
* `spend` - Current spend for the user.
