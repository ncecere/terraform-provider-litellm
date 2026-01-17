# litellm_organization_member Resource

Manages a member's association with a LiteLLM organization. This resource allows you to add users to organizations with specific roles.

## Example Usage

### Minimal Example

```hcl
resource "litellm_organization_member" "member" {
  organization_id = "org-xxxxxxxxxxxx"
  user_id         = "user-xxxxxxxxxxxx"
}
```

### Full Example

```hcl
resource "litellm_organization" "company" {
  organization_alias = "company-org"
}

resource "litellm_user" "admin" {
  user_email = "admin@company.com"
  user_role  = "admin"
}

resource "litellm_organization_member" "admin_member" {
  organization_id = litellm_organization.company.organization_id
  user_id         = litellm_user.admin.user_id
  user_role       = "org_admin"
}
```

### Multiple Members

```hcl
resource "litellm_organization" "engineering" {
  organization_alias = "engineering"
}

resource "litellm_user" "engineers" {
  for_each   = toset(["alice@company.com", "bob@company.com", "carol@company.com"])
  user_email = each.value
}

resource "litellm_organization_member" "eng_members" {
  for_each        = litellm_user.engineers
  organization_id = litellm_organization.engineering.organization_id
  user_id         = each.value.user_id
  user_role       = "user"
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `organization_id` - (Required) The ID of the organization.
* `user_id` - (Required) The ID of the user to add to the organization.

### Optional Arguments

* `user_role` - (Optional) The role of the user in the organization. Valid values: `org_admin`, `user`. Defaults to `user`.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The unique identifier for this organization membership.

## Import

Organization members can be imported using the format `organization_id:user_id`:

```shell
terraform import litellm_organization_member.example org-xxx:user-xxx
```

## Notes

- A user can belong to multiple organizations
- Organization admins can manage teams and budgets within the organization
- Removing this resource removes the user from the organization but does not delete the user
