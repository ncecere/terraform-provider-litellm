# litellm_organization_member Resource

Manages a member within a LiteLLM organization. Removing this resource removes the user from the organization but does not delete the user.

## Example Usage

```hcl
resource "litellm_organization" "company" {
  organization_alias = "my-company"
}

resource "litellm_organization_member" "admin" {
  organization_id = litellm_organization.company.id
  user_id         = "admin-user"
  role            = "internal_user"
}
```

## Argument Reference

- `organization_id` - (Required, ForceNew) The ID of the organization. Changing this forces creation of a new resource.
- `user_id` - (Optional, ForceNew) The ID of the user to add to the organization. If not provided, it will be computed. Changing this forces creation of a new resource.
- `user_email` - (Optional, ForceNew) The email address of the user. Changing this forces creation of a new resource.
- `role` - (Required) The role of the user within the organization. Valid values: `proxy_admin`, `proxy_admin_viewer`, `internal_user`, `internal_user_viewer`, `org_admin`.
- `max_budget_in_organization` - (Optional) The maximum budget allocated to this user within the organization.

~> **Note:** Either `user_id` or `user_email` must be provided.

## Attribute Reference

- `id` - A composite ID in the format `organization_id:user_id`.

## Import

Import using the composite ID:

```shell
terraform import litellm_organization_member.example <organization_id>:<user_id>
```
