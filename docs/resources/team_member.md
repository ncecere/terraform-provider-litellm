# litellm_team_member Resource

Manages one member of a LiteLLM team. The Terraform address, schema version, historical state fields, and stored `team_id:user_id` identifier remain compatible with earlier provider releases.

Use this single-member resource for new configurations. The older [`litellm_team_member_add`](team_member_add.md) batch resource remains supported, but has a wider partial-failure boundary.

## Minimal email-only example

```hcl
resource "litellm_team" "engineering" {
  team_alias = "engineering"
}

resource "litellm_team_member" "developer" {
  team_id    = litellm_team.engineering.id
  user_email = "developer@example.com"
  role       = "user"
}
```

LiteLLM resolves or creates the user. After create, Terraform state contains the canonical `user_id` and stable `team_id:user_id` resource ID while HCL can remain email-only.

## Full example with both identities

```hcl
resource "litellm_team_member" "lead" {
  team_id    = litellm_team.engineering.id
  user_id    = "engineering-lead"
  user_email = "lead@example.com"
  role       = "admin"

  max_budget_in_team = 100
  budget_duration    = "30d"
}
```

`user_id` and `user_email` are an at-least-one pair, not an exclusive pair. Existing configurations that set both remain valid. When both are set, the provider verifies the canonical identity before create and correlates the exact ID/email pair in the authoritative team roster; it does not rely on endpoint precedence to choose one silently. LiteLLM v1.98 may return a null account email in `updated_users`, including for a new user selected by ID. That account field is not required for membership correlation.

## Argument reference

- `team_id` - (Required, Forces replacement) Team ID.
- `user_id` - (Optional, Computed, Forces replacement when a configured canonical value changes) User ID. At least one of `user_id` or `user_email` must be set. An email-only create records the canonical ID in state.
- `user_email` - (Optional) Email representation used to resolve and correlate team-roster identity. At least one identity must be set. Matching is case-insensitive. This resource does not manage or update the user's account email; an account response can disprove a conflicting identity during preflight, but it is not copied into this argument.
- `role` - (Required) Team role. LiteLLM v1.98 accepts exactly `admin` or `user`.
- `max_budget_in_team` - (Optional) Maximum member budget. Removing a previously configured value sends explicit JSON `null` to the native update endpoint.
- `budget_duration` - (Optional) Recurring member-budget reset interval. Use a positive integer followed by `s`, `m`, `h`, `d`, or `w`; one of `hourly`, `daily`, `weekly`, or `monthly`; or exactly `1mo`. Removing a configured duration sends explicit JSON `null`.

## Canonical identity and replacement

`team_id` and canonical `user_id` are immutable membership identity. Terraform plans replacement when either known configured value changes. Adding these replacement rules does not replace unchanged historical state, and email-only HCL keeps the canonical computed `user_id` from state during planning.

A case-only email edit does not replace the membership. Another spelling returned by LiteLLM for the same canonical user can also be retained safely. If a changed email resolves to a different user or cannot be proved to resolve to the stored canonical ID, update stops before mutation. Change `user_id` to the new canonical user, or use an explicit `-replace`, rather than allowing an email edit to retarget an existing resource silently.

Refresh, update, delete, and partial recovery always use `team_id` and canonical `user_id` from stored state. A newly planned identity is never used to mutate or delete the old membership.

## Read, drift, and budgets

The resource reads the exact LiteLLM v1.98 `/team/info` roster and membership collections:

- `members_with_roles` is authoritative for membership role and email-to-ID correlation.
- `team_memberships` and its nested budget relation are authoritative when a member budget exists.
- A v1.98 member with no configured maximum or duration and no `team_info.metadata["team_member_budget_id"]` default is intentionally roster-only. In that exact budgetless shape, an empty `updated_team_memberships` add response and no later membership row are healthy complete state. Create, refresh, import, and role changes converge from the roster alone.
- A requested maximum or duration, or a non-null team member-budget default, requires a membership row. If that row is missing, the provider retains the roster identity but reports a partial error.
- A budget can be added later to a healthy roster-only member. The resulting membership and nested budget must then appear during read-back.
- Omitted optional budget arguments remain unmanaged and are not adopted during refresh.
- Configured values, including explicit clears, must be confirmed by authoritative read-back.
- Duplicate IDs, duplicate case-folded email matches, or an ID/email pair that points at different roster entries stop reconciliation instead of selecting one row.

LiteLLM v1.98 can update a historical budget row in place. Role-only updates omit every unchanged budget field, including `budget_duration`; they therefore do not touch a shared historical row or its reset schedule. Budget fields are sent only when their owned Terraform value changes, with explicit JSON `null` reserved for a clear. Whenever either budget field is transmitted, the provider runs shared-row safety first and refuses a non-current row shared by another membership. The current shared member-budget row is identified only by `team_info.metadata["team_member_budget_id"]`; it is never inferred from the team ID. v1.98 safely clones the selected member away from that current default.

An exact HTTP 404 from `/team/info` means the team is gone and removes the resource from state. Error text that merely contains `404` is not deletion evidence.

## Partial failures and LiteLLM v1.98 limits

When a budget membership is required, LiteLLM writes `team_memberships` and optional budget data before appending `members_with_roles`. The provider retains a uniquely recoverable canonical ID after an accepted add even if verification fails. It never writes a requested role into state as confirmed when the roster write is absent.

If a malformed or truncated 2xx add response is followed by a propagation-delayed read that still shows the pre-create roster, state retains only the team identity, any proven canonical user ID, and the configured email representation. Role and budget attributes remain unknown rather than copying requested plan values. A provider-private uncertain-ownership marker makes later absent reads retain that recovery state with a hard diagnostic and blocks update or destroy from guessing at an unconfirmed operation. The first authoritative roster or membership observation reconciles the real values and clears the marker. A definitively non-2xx operation does not establish this ownership marker or retain planned state.

A membership-only v1.98 row cannot be repaired safely through the team-member API:

- `/team/member_update` can return 2xx and mutate budget data without restoring `members_with_roles`.
- `/team/member_delete` first requires the missing roster identity and cannot remove the orphan.
- retrying `/team/member_add` can create another budget row.

Read, update, and destroy therefore retain ownership and return an administrator-remediation error without sending an unsafe repair or delete. Remove the inconsistent upstream row using LiteLLM administrator/support guidance, or upgrade to a corrected LiteLLM release, then refresh. A roster-only partial is also retained and blocks update; destroy may use the roster-backed delete path but succeeds only after both authoritative rows are absent.

Failed or partially completed destroys retain Terraform state. A 2xx response, a mutation error, or a 404 from the mutation endpoint alone is not enough: the provider verifies that both stored canonical rows are gone, or that the team itself returned an exact 404.

## Import

### Historical form

The historical grammar remains unchanged:

```shell
terraform import litellm_team_member.example '<team_id>:<user_id>'
```

It splits at the first colon. A colon is therefore supported in `user_id`, but the historical form cannot represent a colon in `team_id`. Existing import IDs and stored composite IDs remain valid.

### Versioned escaped form

Use the unambiguous versioned form only when a team ID contains a colon (or when shell-safe opaque components are preferred):

```text
v1.<team>.<user>
```

`<team>` and `<user>` are canonical unpadded URL-safe base64 (RFC 4648 base64url). For example, generate the exact string with tooling that emits base64url without `=` padding, then import it:

```shell
terraform import litellm_team_member.example \
  'v1.dGVhbTp3aXRoOmNvbG9u.dXNlcjp3aXRoOmNvbG9u'
```

This decodes to team `team:with:colon` and user `user:with:colon`. Terraform still stores the historical natural composite `team_id:user_id` value in `id`; lifecycle operations use the separate stored attributes and never parse that stored value. Historical team IDs beginning with `v1.` remain accepted when the import uses colon grammar.

Import identifies membership only by canonical `user_id`. Refresh hydrates `user_email`, role, and configured/computed state that LiteLLM returns without adopting unrelated members.

## Attribute reference

- `id` - Stable canonical membership identifier in natural `team_id:user_id` form. Lifecycle code uses the separately stored identity attributes, so colons inside a versioned-import team ID do not make read or destroy ambiguous.

## State and diagnostic security

Canonical IDs and `user_email` are stored in ordinary Terraform state for compatibility and identity recovery; they are not secret attributes. Protect state as identity/PII-bearing data and restrict state backend access accordingly. If email must not be stored, configure only `user_id`.

The provider does not include request payloads, request URLs, raw response bodies, email addresses from failed HTTP bodies, API keys, or raw transport causes in team-member diagnostics. Safe remote errors are reduced to exact HTTP status and bounded validated request metadata. Debug logs and external proxies remain outside Terraform state controls and should follow your organization's logging policy.
