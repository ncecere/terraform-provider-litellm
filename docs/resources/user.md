# litellm_user Resource

Manages a LiteLLM user. Users represent individuals who can be assigned to teams and organizations, and can have their own API keys and budgets.

## Example Usage

### Minimal Example

```hcl
resource "litellm_user" "basic" {
  user_email = "user@example.com"
}
```

### Full Example

```hcl
resource "litellm_user" "developer" {
  user_email   = "developer@company.com"
  user_alias   = "john-developer"
  user_role    = "user"
  max_budget   = 100.0
  tpm_limit    = 100000
  rpm_limit    = 1000
  models       = ["gpt-4", "gpt-3.5-turbo"]
  
  metadata = jsonencode({
    department = "Engineering"
    team       = "Backend"
  })
}
```

### Admin User

```hcl
resource "litellm_user" "admin" {
  user_email = "admin@company.com"
  user_alias = "system-admin"
  user_role  = "admin"
  max_budget = 0  # Unlimited for admin
}
```

### User with Team Assignment

```hcl
resource "litellm_team" "backend" {
  team_alias = "backend-team"
}

resource "litellm_user" "backend_dev" {
  user_email = "backend-dev@company.com"
  user_alias = "backend-developer"
}

resource "litellm_team_member" "dev_membership" {
  team_id = litellm_team.backend.team_id
  user_id = litellm_user.backend_dev.user_id
  role    = "user"
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `user_email` - (Required) Email address of the user.

### Optional Arguments

* `user_alias` - (Optional) A human-readable alias for the user.
* `user_role` - (Optional) The role of the user. Valid values: `admin`, `user`. Defaults to `user`.
* `max_budget` - (Optional) Maximum budget for the user in dollars.
* `budget_duration` - (Optional) Duration for budget reset. Valid values: `daily`, `weekly`, `monthly`.
* `tpm_limit` - (Optional) Tokens per minute limit for the user.
* `rpm_limit` - (Optional) Requests per minute limit for the user.
* `models` - (Optional) List of model names that this user can access.
* `metadata` - (Optional) JSON string containing additional metadata for the user.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The unique identifier for this user.
* `user_id` - The user ID (same as id).
* `spend` - Current spend for the user.
* `created_at` - Timestamp when the user was created.
* `updated_at` - Timestamp when the user was last updated.

## Import

Users can be imported using the user ID:

```shell
terraform import litellm_user.example user-xxxxxxxxxxxx
```

## Notes

- Users can belong to multiple teams and organizations
- Budget limits are tracked per user
- Admin users have elevated permissions in LiteLLM
- User email must be unique within the LiteLLM instance
