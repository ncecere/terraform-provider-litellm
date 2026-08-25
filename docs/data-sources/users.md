# litellm_users Data Source

Retrieves a list of LiteLLM users.

## Example Usage

```hcl
data "litellm_users" "all" {}

output "user_count" {
  value = length(data.litellm_users.all.users)
}
```

### Filter by Role

```hcl
data "litellm_users" "admins" {
  user_role = "proxy_admin"
}

output "admin_emails" {
  value = [for u in data.litellm_users.admins.users : u.user_email]
}
```

## Argument Reference

* `user_role` - (Optional) Filter users by role (proxy_admin, proxy_admin_viewer, internal_user, internal_user_viewer, team, customer).

## Attribute Reference

* `id` - Stable historical identifier (`users`), unchanged by filters.
* `users` - List of user objects, each containing:
  * `user_id` - The unique identifier.
  * `user_email` - Email address.
  * `user_alias` - Human-readable alias.
  * `user_role` - User role.
  * `max_budget` - Maximum budget.
  * `spend` - Current spend.
  * `tpm_limit` - Tokens per minute limit.
  * `rpm_limit` - Requests per minute limit.

## Notes

- The Terraform argument remains `user_role` for compatibility; the provider sends LiteLLM v1.98's canonical `role` query parameter.
- All `/user/list` pages are retrieved at LiteLLM's maximum page size and sorted deterministically by user ID.
- Concurrent count/page shifts restart the bounded listing at page 1. Persistent inconsistency, repeated pages/items, malformed data, truncation, and over-limit pagination fail rather than returning a partial inventory.
