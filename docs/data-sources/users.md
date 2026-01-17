# litellm_users Data Source

Retrieves a list of LiteLLM users.

## Example Usage

### Minimal Example

```hcl
data "litellm_users" "all" {}
```

### Full Example

```hcl
data "litellm_users" "all" {}

output "user_count" {
  value = length(data.litellm_users.all.users)
}

# Find admin users
locals {
  admin_users = [
    for u in data.litellm_users.all.users : u
    if u.user_role == "admin"
  ]
}

output "admin_emails" {
  value = [for u in local.admin_users : u.user_email]
}

# Calculate total user spend
locals {
  total_user_spend = sum([for u in data.litellm_users.all.users : u.spend])
}

output "total_spend" {
  value = local.total_user_spend
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

The following attributes are exported:

* `id` - Placeholder identifier.
* `users` - List of user objects, each containing:
  * `user_id` - The unique identifier.
  * `user_email` - Email address.
  * `user_alias` - Human-readable alias.
  * `user_role` - User role (admin, user).
  * `max_budget` - Maximum budget.
  * `spend` - Current spend.
  * `created_at` - Creation timestamp.
  * `updated_at` - Last update timestamp.
