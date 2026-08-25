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
- `role` - (Required) The role of the user within the organization. LiteLLM v1.98 organization-member add/update accepts exactly `org_admin`, `internal_user`, or `internal_user_viewer`.
- `max_budget_in_organization` - (Optional) The maximum budget allocated to this user within the organization.

~> **Note:** Either `user_id` or `user_email` must be provided.

## Attribute Reference

- `id` - A composite ID in the format `organization_id:user_id`.

`proxy_admin` and `proxy_admin_viewer` belong to LiteLLM's broader user-role enum but are rejected by the v1.98 organization-member request models. Configurations using either value must select an organization role before planning; import/read state remains refreshable because schema validators apply only to configuration.

## Import

Import using the composite ID:

```shell
terraform import litellm_organization_member.example <organization_id>:<user_id>
```
