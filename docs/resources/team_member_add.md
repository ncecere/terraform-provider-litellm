# litellm_team_member_add Resource

Manages an explicitly owned batch of members in a LiteLLM team.

> **Deprecated for new configurations:** prefer `for_each` with [`litellm_team_member`](team_member.md). The batch resource remains supported for existing state and HCL. It is useful when every member shares one `max_budget_in_team`, but the single-member resource has a simpler failure and import boundary. In the exact LiteLLM v1.98 membership-only partial condition described below, neither `/team/member_update` nor `/team/member_delete` can repair or delete the orphan; manual upstream remediation is required.

## Minimal Example

```hcl
resource "litellm_team" "engineering" {
  team_alias = "engineering"
}

resource "litellm_team_member_add" "developers" {
  team_id = litellm_team.engineering.id

  member {
    user_email = "developer@example.com"
    role       = "user"
  }
}
```

At least one `member` block is required. Every block must contain a non-empty `user_id`, `user_email`, or both.

## Full Example

```hcl
resource "litellm_team_member_add" "engineering" {
  team_id            = litellm_team.engineering.id
  max_budget_in_team = 100

  member {
    user_id    = "developer-1"
    user_email = "developer-1@example.com"
    role       = "user"
  }

  member {
    user_id    = "lead-1"
    user_email = "lead-1@example.com"
    role       = "admin"
  }
}
```

`max_budget_in_team` is one shared Terraform attribute. When it is configured with a known number, the provider applies it to every owned member through LiteLLM's native member endpoints and refreshes it from the membership budget rows. When it is omitted (including an explicit Terraform `null`), budgets are unmanaged: refresh does not report budget drift, and update does not clear a remote budget. If a managed batch's owned members have different remote budgets, refresh returns a safe error because that state cannot be represented by this resource.

## Recommended `for_each` Migration

Existing resource addresses and state continue to work; no state upgrader or forced replacement is introduced. For new configurations, or when members need different budgets, use one resource instance per member:

```hcl
locals {
  engineering_members = {
    developer = {
      user_id    = "developer-1"
      user_email = "developer-1@example.com"
      role       = "user"
      budget     = 50
    }
    lead = {
      user_id    = "lead-1"
      user_email = "lead-1@example.com"
      role       = "admin"
      budget     = 100
    }
  }
}

resource "litellm_team_member" "engineering" {
  for_each = local.engineering_members

  team_id            = litellm_team.engineering.id
  user_id            = each.value.user_id
  user_email         = each.value.user_email
  role               = each.value.role
  max_budget_in_team = each.value.budget
}
```

Terraform cannot move one batch address to several resource addresses automatically. Back up state, remove only the batch address with `terraform state rm` (do not destroy it), import each remote membership into its new `for_each` address, and then verify a no-op plan. Perform those state operations together so there is no apply while ownership is temporarily absent, and never leave both resource types owning the same remote member.

## Argument Reference

- `team_id` - (Required, Forces replacement) Team ID.
- `max_budget_in_team` - (Optional) Maximum budget applied to every member owned by this resource. Omission is the backward-compatible unmanaged mode; `null` does not detect drift or clear remote budgets.

### `member` Block

One or more `member` blocks are required:

- `user_id` - (Optional, Computed) User ID. At least one identity field must be non-empty. For an email-only block, the provider records LiteLLM's canonical ID in state after a successful add or authoritative roster read; it does not add the ID to configuration.
- `user_email` - (Optional) User email. At least one identity field must be non-empty.
- `role` - (Required) Team role: `admin` or `user`.

Identity values must be unique within the batch. Email identity follows LiteLLM v1.98's case-insensitive matching, so case-only email duplicates are rejected before any mutation and an existing unowned case alias is not adopted. The configured email spelling is preserved when it still resolves safely. If an ID-only block and an email-only block resolve to the same LiteLLM user, reconciliation stops with an error rather than silently merging or adopting the duplicate.

### Email-only lifecycle

An email-only block stays email-only in HCL, but its state contains both the configured email spelling and the canonical `user_id` returned by LiteLLM. This lets the provider correlate later `team_memberships` rows, which contain only `user_id`. Existing historical email-only state is upgraded naturally: the first refresh with an intact roster backfills the canonical ID without changing the resource ID or configuration.

If that historical roster entry is already absent and `/team/info` also contains unmatched membership-only rows, LiteLLM exposes no email with which to prove correlation. Read, update, and destroy then preserve the prior member state and return a permanent administrator-remediation error. They never write an empty owned set, retry `member_add`, send `member_update`, or report destroy success for this ambiguous condition.

## Attribute Reference

- `id` - The historical resource ID, equal to `team_id`. It remains unchanged by this lifecycle implementation.

## Ownership and Drift

The resource owns only the member identities already recorded in its `member` state:

- `/team/info` is authoritative for owned membership, roles, and configured managed budgets. Omitted budgets remain unmanaged.
- Out-of-band role changes and removals of owned members appear in Terraform state and the next plan.
- Unrelated members added to the same team are never absorbed into this resource.
- Create and update refuse to adopt a matching member that is present remotely but is not already owned. Import that member explicitly instead.
- Additions complete before removals. A failed destination addition therefore does not revoke an existing membership.
- Role and configured budget changes use LiteLLM's native `/team/member_update` endpoint.
- LiteLLM v1.98 updates a non-current membership budget row in place. The current shared team-member budget ID is the exact string in `team_info.metadata["team_member_budget_id"]`; it is never inferred from `team_id`. Before a budget write, the provider groups every `team_memberships.budget_id` reference, including membership-only and unrelated rows. It refuses a historical row shared with an unrelated member, updates a compatible all-owned historical group once in deterministic order, and rejects a shared group whose retained desired state is incompatible. Updating the current metadata-identified default is safe because v1.98 clones only the selected member to a private budget row.

A malformed or partial `/team/info` response is not treated as an empty roster. Every membership row must include `budget_id` and must serialize `litellm_budget_table` as either an object or explicit JSON `null`; omission is a malformed partial response and never clears prior budget state. The provider returns an error and retains prior state. Exact HTTP 404 responses mean the team is absent; other failures, including a 500 response containing the text `404`, retain state and return a safe diagnostic.

After a successful write, authoritative reads are bounded to five attempts. Retryable cases are HTTP 408, 429, and 5xx responses; transient successful-response read or JSON decode failures; transient partial response shapes; and transport failures safely classified as timeout, temporary, or connection reset. A team 404 after preflight, TLS/certificate or configuration transport failures, context cancellation, malformed remote identities, predicate/identity errors, and other permanent 4xx responses stop immediately. Diagnostics and retry categories do not retain response bodies, request URLs, payloads, or raw transport causes.

## Partial Failures

LiteLLM performs member operations individually. LiteLLM v1.98 can create the `team_memberships` row and member budget before writing `members_with_roles`. If an add then fails, a newly matching membership row proves the operation's retained ownership even when the roster entry is absent. The provider stores the recoverable configured identity and budget, leaves the unconfirmed role unknown, and returns an error instead of orphaning or claiming success.

LiteLLM v1.98 has no safe native operation that inserts only the missing roster entry. Retrying `/team/member_add` can create another budget. A direct user-ID `/team/member_update` can return 2xx and may mutate the membership budget, but it does **not** append `members_with_roles`; the provider therefore never uses that apparent success as a repair. `/team/member_delete` requires the identity in `members_with_roles` before it removes `team_memberships`, so only delete returns 400 for the membership-only orphan. While the row remains, refresh, apply, and destroy retain explicit ownership with an unknown role and a permanent actionable error, without sending either repair attempt.

Provider-retained partial ownership is tracked privately by canonical user ID; this does not change the public schema beyond the documented computed `user_id`, the resource ID, or import syntax. It is distinct from a composite import's initially unknown role. An unresolved composite import remains unchanged and errors until its identity appears in the authoritative roster; an unmatched membership row is not enough to adopt it.

The same endpoint limitation applies if a partial delete removes the roster entry but leaves `team_memberships`. Ask a LiteLLM administrator or support to remove the inconsistent membership row (or upgrade to a version whose endpoints are corrected). On the first refresh after that cleanup, the provider recognizes successful remediation, removes only that orphan's private marker and owned member entry, and succeeds. A subsequent apply can recreate a configured member; a pending configuration removal or destroy can finish without state surgery. This unavoidable failure boundary is another reason to prefer `for_each` with `litellm_team_member` for new configurations.

Other partial operations retain the confirmed owned subset so a later apply can retry safely. Removal failures are hard errors. A planned removal is not committed while that member or its membership row remains remote, and destroy failures retain confirmed remaining identities in state.

## Import

### Exact composite import

Use the versioned composite form to reconstruct exactly the batch this resource should own:

```text
v1.<team>.<member>[,<member>...]
```

`<team>` and every identity value use unpadded URL-safe base64 (RFC 4648 base64url). Member tokens are:

- `i~<user-id>` - identify by user ID
- `e~<user-email>` - identify by email
- `b~<user-id>~<user-email>` - require both values to resolve to the same roster entry

For example, this ID owns `user-1` and `dev@example.com` in `team-1`:

```shell
terraform import litellm_team_member_add.example \
  'v1.dGVhbS0x.e~ZGV2QGV4YW1wbGUuY29t,i~dXNlci0x'
```

The token order is not significant. Import refresh reads roles and, when the imported unknown budget is resolved, the common member budget from `/team/info`; it rejects missing or ambiguous identities and does not adopt other team members.

The resource's stored `id` after import is still the decoded team ID, preserving existing state and avoiding replacement.

### Historical and escaped plain team-ID import

The historical plain team-ID form remains accepted for compatibility when it does not collide with a reserved, fully valid import form:

```shell
terraform import litellm_team_member_add.legacy '<team-id>'
```

A team ID alone cannot reconstruct member ownership. It therefore imports an explicitly empty owned roster with a warning and never adopts the team's current roster. Historical IDs beginning with `v1.` (for example, `v1.production`) remain plain team IDs unless the entire composite structure and every base64url component validate. A malformed composite-looking value likewise falls back to the historical empty-roster import.

Use the explicit escaped plain form for **any** historical team ID that could collide with the composite grammar, including a team ID that is itself a fully valid `v1.<team>.<member>` string:

```text
t~<team>
```

`<team>` is the complete literal team ID encoded as canonical unpadded base64url. For example, the literal team ID `v1.dGVhbQ.i~dXNlcg` is imported as:

```shell
terraform import litellm_team_member_add.legacy \
  't~djEuZEdWaGJRLml-ZFhObGNn'
```

`t~` is reserved for this escaped form. To import a literal historical ID that itself begins with `t~`, base64url-encode that entire ID and prepend another `t~`. Empty escaped values, padded or non-canonical base64url, invalid characters, and decoded non-UTF-8 values are rejected rather than reinterpreted as plain IDs. An empty overall import ID is also rejected.

Use the versioned composite form when importing existing members. If configuration then proposes members that already exist remotely, update fails safely instead of adopting them. All import forms store the decoded/literal team ID in both `id` and `team_id`; the resource address and historical stored-ID contract do not change.
