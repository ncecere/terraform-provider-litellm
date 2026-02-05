# litellm_user Data Source

Retrieves information about a specific LiteLLM user.

## Example Usage

### Minimal Example

```hcl
data "litellm_user" "existing" {
  user_id = "user-xxxxxxxxxxxx"
}
```

### Full Example

```hcl
data "litellm_user" "developer" {
  user_id = var.user_id
}

output "user_info" {
  value = {
    email      = data.litellm_user.developer.user_email
    role       = data.litellm_user.developer.user_role
    max_budget = data.litellm_user.developer.max_budget
    spend      = data.litellm_user.developer.spend
  }
}

# Create key for user
resource "litellm_key" "user_key" {
  user_id    = data.litellm_user.developer.user_id
  key_alias  = "user-${data.litellm_user.developer.user_alias}-key"
  max_budget = data.litellm_user.developer.max_budget
}
```

## Argument Reference

The following arguments are supported:

* `user_id` - (Required) The unique identifier of the user to retrieve.

## Attribute Reference

The following attributes are exported:

* `id` - The unique identifier of the user.
* `user_id` - The user ID.
* `user_email` - The user's email address.
* `user_alias` - The human-readable alias.
* `user_role` - The user's role (admin, user).
* `max_budget` - Maximum budget for the user.
* `spend` - Current spend for the user.
* `tpm_limit` - Tokens per minute limit.
* `rpm_limit` - Requests per minute limit.
* `models` - List of models the user can access.
* `created_at` - Creation timestamp.
* `updated_at` - Last update timestamp.
