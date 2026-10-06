# litellm_organization_member Resource

Manages one user membership in a LiteLLM organization. Removing this resource removes the membership but does not delete the LiteLLM user.

Organization endpoints require a LiteLLM Enterprise license from LiteLLM 1.102.0, including reads. On an unlicensed proxy every operation fails with `LiteLLM Enterprise License Required` and Terraform state is left unchanged.

LiteLLM's organization member API also creates an internal user when the supplied `user_id` does not identify an existing user and no `user_email` is configured. The provider uses that API behavior; it does not make a separate user-creation request.

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

### Existing user with email

```hcl
resource "litellm_user" "viewer" {
  user_id    = "viewer-user"
  user_email = "viewer@example.com"
}

resource "litellm_organization_member" "viewer" {
  organization_id = litellm_organization.company.id
  user_id         = litellm_user.viewer.user_id
  user_email      = litellm_user.viewer.user_email
  role            = "internal_user_viewer"
}
```

~> **LiteLLM limitation ([LiteLLM #44766](https://github.com/BerriAI/litellm/issues/44766)):** LiteLLM's member-add endpoint (verified on 1.98.0 and 1.104.0) returns HTTP 500 whenever `user_email` is sent and `user_id` does not identify an existing user: it looks the email up with a unique-field query on a column that is not unique. Email-only requests therefore always fail, as does adding a new user by `user_id` together with `user_email`. Configure `user_id` alone to let LiteLLM create a new user, or create the user first (for example with `litellm_user`) and pass its `user_id`; `user_email` is accepted alongside an existing `user_id`. The provider reports the failure without exposing the response body and retains any structurally confirmed membership identity for recovery.

## Argument Reference

- `organization_id` - (Required, ForceNew) Organization ID.
- `user_id` - (Optional, Computed, ForceNew) User ID to resolve or create. At least one of `user_id` and `user_email` must be a non-empty known value. When both are configured, LiteLLM looks up `user_id` first and falls back to `user_email` only if that ID does not exist; see the limitation above for that fallback. If the fallback resolves a different canonical ID, the provider retains that membership in state and reports the mismatch rather than losing the created object.
- `user_email` - (Optional, ForceNew) Email used to resolve the user. Because of the LiteLLM limitation above, configure it only together with the `user_id` of an existing user. Once a `user_id` is resolved, this resource does not manage changes to the user's email.
- `role` - (Required) Organization-scoped role. LiteLLM accepts exactly `org_admin`, `internal_user`, and `internal_user_viewer`. Global roles such as `proxy_admin` and `proxy_admin_viewer` are not valid organization membership roles.
- `max_budget_in_organization` - (Optional) Maximum spend for this user within the organization. LiteLLM declares this field on the add request but does not persist it there, so the provider follows a successful add with `/organization/member_update`. Role and non-null budget changes are updated in place.

~> **Budget removal:** LiteLLM ignores `max_budget_in_organization = null` on member update. When a known configured value is removed, Terraform therefore plans replacement of the membership automatically. Null or unknown prior values, including a newly imported membership whose budget is not visible, do not force replacement. Update also rejects an unsupported clear defensively if it is invoked outside the normal planned lifecycle.

## Attribute Reference

- `id` - Canonical composite membership ID in `organization_id:user_id` form.

Reads use the membership's authoritative `user_role` and use nested `litellm_budget_table.max_budget` whenever that relation is actually returned, including in member-update responses.

LiteLLM organization-admin credentials can use the organization member endpoints, but cannot use `/budget/info`, and `/organization/info` does not reliably load `litellm_budget_table`. The provider therefore does not depend on `/budget/info` for this resource. When the primary organization response proves the member exists but omits or returns null for the nested budget relation, the provider preserves the last configured or observed budget instead of failing refresh or incorrectly removing the membership. Consequently, an organization admin cannot discover an out-of-band budget change until a response with the nested budget relation is available; a newly imported membership may retain a null budget value under those credentials.

A create budget follow-up or an in-place budget change succeeds only when `litellm_budget_table` is actually present in the member-update response or a subsequent organization read-back. Omission cannot confirm the requested budget: the provider reports an error and retains the recoverable membership with its prior budget, or a null budget after create. An update that does not change the budget, such as a role-only change, can still succeed when the changed fields are confirmed; an omitted nested relation then preserves the last-known budget. Whenever the nested relation is present, its value is authoritative and is retained even when it differs from configuration.

`proxy_admin` and `proxy_admin_viewer` belong to LiteLLM's broader user-role enum but are rejected by the LiteLLM organization-member request models. Configurations using either value must select an organization role before planning; import/read state remains refreshable because schema validators apply only to configuration.

## Import

Import using the canonical organization and user IDs:

```shell
terraform import litellm_organization_member.example '<organization_id>:<user_id>'
```

Both components must be non-empty. Email-specific import syntax is not supported; resolve the user's ID first. A `user_id` itself may be an email-shaped string if that is its actual LiteLLM ID.

For a no-drift canonical import, configure `organization_id` and `user_id` and omit `user_email`. The email argument is a create-time identity-resolution input and is not reconstructed from the composite import ID; adding it after import intentionally plans replacement. Under organization-admin credentials, also omit `max_budget_in_organization` unless its value is already known because LiteLLM does not expose the nested member budget on `/organization/info`.
