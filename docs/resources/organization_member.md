# litellm_organization_member Resource

Manages one user membership in a LiteLLM organization. Removing this resource removes the membership but does not delete the LiteLLM user.

LiteLLM's organization member API also creates an internal user when neither the supplied `user_id` nor `user_email` identifies an existing user. The provider uses that API behavior; it does not make a separate user-creation request.

## Example Usage

### Existing user ID

```hcl
resource "litellm_organization" "company" {
  organization_alias = "my-company"
}

resource "litellm_organization_member" "admin" {
  organization_id            = litellm_organization.company.id
  user_id                    = "admin-user"
  role                       = "org_admin"
  max_budget_in_organization = 500
}
```

### Resolve or create by email

```hcl
resource "litellm_organization_member" "viewer" {
  organization_id = litellm_organization.company.id
  user_email      = "viewer@example.com"
  role            = "internal_user_viewer"
}
```

After an email-only create, LiteLLM's resolved `user_id` is stored in state and used as the canonical membership identity.

## Argument Reference

- `organization_id` - (Required, ForceNew) Organization ID.
- `user_id` - (Optional, Computed, ForceNew) User ID to resolve or create. At least one of `user_id` and `user_email` must be a non-empty known value. When both are configured, LiteLLM looks up `user_id` first, then falls back to an existing `user_email` if that ID does not exist. If the fallback resolves a different canonical ID, the provider retains that membership in state and reports the mismatch rather than losing the created object.
- `user_email` - (Optional, ForceNew) Email used to resolve or create the user. Once a `user_id` is resolved, this resource does not manage changes to the user's email.
- `role` - (Required) Organization-scoped role. LiteLLM v1.98.0 accepts exactly `org_admin`, `internal_user`, and `internal_user_viewer`. Global roles such as `proxy_admin` and `proxy_admin_viewer` are not valid organization membership roles.
- `max_budget_in_organization` - (Optional) Maximum spend for this user within the organization. LiteLLM v1.98.0 declares this field on the add request but does not persist it there, so the provider follows a successful add with `/organization/member_update`. Role and non-null budget changes are updated in place.

~> **Budget removal:** LiteLLM v1.98.0 ignores `max_budget_in_organization = null` on member update. When a known configured value is removed, Terraform therefore plans replacement of the membership automatically. Null or unknown prior values, including a newly imported membership whose budget is not visible, do not force replacement. Update also rejects an unsupported clear defensively if it is invoked outside the normal planned lifecycle.

## Attribute Reference

- `id` - Canonical composite membership ID in `organization_id:user_id` form.

Reads use the membership's authoritative `user_role` and use nested `litellm_budget_table.max_budget` whenever that relation is actually returned, including in member-update responses.

LiteLLM v1.98.0 organization-admin credentials can use the organization member endpoints, but cannot use `/budget/info`, and `/organization/info` does not reliably load `litellm_budget_table`. The provider therefore does not depend on `/budget/info` for this resource. When the primary organization response proves the member exists but omits or returns null for the nested budget relation, the provider preserves the last configured or observed budget instead of failing refresh or incorrectly removing the membership. Consequently, an organization admin cannot discover an out-of-band budget change until a response with the nested budget relation is available; a newly imported membership may retain a null budget value under those credentials.

A create budget follow-up or an in-place budget change succeeds only when `litellm_budget_table` is actually present in the member-update response or a subsequent organization read-back. Omission cannot confirm the requested budget: the provider reports an error and retains the recoverable membership with its prior budget, or a null budget after create. An update that does not change the budget, such as a role-only change, can still succeed when the changed fields are confirmed; an omitted nested relation then preserves the last-known budget. Whenever the nested relation is present, its value is authoritative and is retained even when it differs from configuration.

`proxy_admin` and `proxy_admin_viewer` belong to LiteLLM's broader user-role enum but are rejected by the v1.98 organization-member request models. Configurations using either value must select an organization role before planning; import/read state remains refreshable because schema validators apply only to configuration.

## Import

Import using the canonical organization and user IDs:

```shell
terraform import litellm_organization_member.example '<organization_id>:<user_id>'
```

Both components must be non-empty. Email-specific import syntax is not supported; resolve the user's ID first. A `user_id` itself may be an email-shaped string if that is its actual LiteLLM ID.
