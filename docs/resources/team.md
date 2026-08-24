# litellm_team Resource

Manages a team in LiteLLM. Teams allow you to group users and apply shared budgets, rate limits, and model access controls.

Team members are managed separately via the `litellm_team_member` resource.

## Example Usage

### Minimal

```hcl
resource "litellm_team" "minimal" {
  team_alias = "test-team-minimal"
}
```

### Custom Team ID

Set `team_id` when an external identity provider or JWT group claim must refer to a stable, predetermined LiteLLM team identity:

```hcl
resource "litellm_team" "identity_group" {
  team_id    = "engineering-platform"
  team_alias = "Engineering Platform"
  models     = ["gpt-4o"]
}
```

If omitted, the provider generates a UUID as before. Changing `team_id` replaces the team.

### With Access Groups

Associate access groups with a team to grant their grouped model access:

```hcl
resource "litellm_access_group" "engineering" {
  access_group = "engineering-models"
  model_names  = ["gpt-4o", "gpt-4o-mini"]
}

resource "litellm_team" "engineering" {
  team_alias = "Engineering"
  access_group_ids = [
    litellm_access_group.engineering.id,
  ]
}
```

`access_group_ids` is unordered. Set it to `[]` to detach all access groups; omitting it allows the provider to read the current API associations.

### Full

```hcl
resource "litellm_team" "full" {
  team_alias      = "ai-research-team"
  max_budget      = 500.0
  tpm_limit       = 100000
  rpm_limit       = 1000
  tpm_limit_type  = "guaranteed_throughput"
  rpm_limit_type  = "guaranteed_throughput"
  budget_duration = "30d"
  blocked         = false

  models     = ["gpt-4o", "gpt-4o-mini"]
  guardrails = []
  prompts    = []

  team_member_permissions = []
  team_member_budget          = 50.0
  team_member_budget_duration = "30d"
  team_member_rpm_limit   = 100
  team_member_tpm_limit   = 10000

  metadata = {
    "environment" = "testing"
  }

  model_aliases = {
    "fast" = "gpt-4o-mini"
  }

  model_rpm_limit = {
    "gpt-4o" = 500
  }

  model_tpm_limit = {
    "gpt-4o" = 50000
  }
}
```

### With Router Settings (Fallbacks)

Configure team-level fallback chains that override global fallback settings. When a request uses a key belonging to this team, these fallbacks take precedence over the global configuration. The resolution order is **Key > Team > Global**.

```hcl
resource "litellm_team" "with_fallbacks" {
  team_alias = "resilient-team"
  models     = ["gpt-4o", "gpt-4o-mini", "gpt-3.5-turbo", "claude-3-haiku"]

  router_settings = {
    fallbacks = [
      {
        model           = "gpt-4o"
        fallback_models = ["gpt-4o-mini", "claude-3-haiku"]
      },
      {
        model           = "gpt-3.5-turbo"
        fallback_models = ["gpt-4o-mini"]
      }
    ]
    context_window_fallbacks = [
      {
        model           = "gpt-3.5-turbo"
        fallback_models = ["gpt-4o"]
      }
    ]
  }
}
```

## Argument Reference

The following arguments are supported:

* `team_alias` - (Required) A human-readable alias for the team.
* `team_id` - (Optional, Computed, Forces replacement) Stable LiteLLM team identifier. Supply a predetermined value for external identity or JWT group mapping, or omit it to let the provider generate a UUID.
* `organization_id` - (Optional) The ID of the organization this team belongs to.
* `access_group_ids` - (Optional, Computed) Unordered set of access group IDs associated with the team. Use `[]` to detach all groups.
* `max_budget` - (Optional) Maximum budget allocated to the team.
* `budget_duration` - (Optional) Duration for the budget cycle (e.g., `"30d"`, `"7d"`, `"1h"`).
* `tpm_limit` - (Optional) Tokens per minute limit for the team.
* `rpm_limit` - (Optional) Requests per minute limit for the team.
* `tpm_limit_type` - (Optional) Type of TPM limit (e.g., `"guaranteed_throughput"`).
* `rpm_limit_type` - (Optional) Type of RPM limit (e.g., `"guaranteed_throughput"`).
* `models` - (Optional) List of model names the team is allowed to use.
* `blocked` - (Optional) Whether the team is blocked from making requests.
* `guardrails` - (Optional) List of guardrail identifiers applied to the team.
* `prompts` - (Optional) List of prompt identifiers associated with the team.
* `team_member_permissions` - (Optional) List of permissions granted to team members.
* `team_member_budget` - (Optional) Default budget for each team member.
* `team_member_budget_duration` - (Optional) Recurring reset interval for the default member budget, such as `30d`, `24h`, `1mo`, or `monthly`. LiteLLM applies it to new/default membership budgets and may backfill memberships without a budget; private member overrides are preserved. Configure `litellm_team_member.budget_duration` for per-member control.
* `team_member_rpm_limit` - (Optional) Default requests per minute limit for each team member.
* `team_member_tpm_limit` - (Optional) Default tokens per minute limit for each team member.
* `metadata` - (Optional) A map of metadata pairs for the team. Values are strings; use `jsonencode()` for complex values (objects, arrays) — they will be sent as native JSON to the API.
* `model_aliases` - (Optional) A map of alias names to model names.
* `model_rpm_limit` - (Optional) A map of model names to per-model RPM limits.
* `model_tpm_limit` - (Optional) A map of model names to per-model TPM limits.
* `tags` - (Optional) List of tags for the team. **Requires LiteLLM Enterprise license.**
* `router_settings` - (Optional) Router settings for the team, including fallback configurations. These override global fallback settings for requests made with this team's keys. Resolution order: Key > Team > Global. Contains the following nested attributes:
  * `fallbacks` - (Optional) List of fallback model chains triggered when a model call fails after retries. Each entry contains:
    * `model` - (Required) The primary model name to configure fallbacks for.
    * `fallback_models` - (Required) Ordered list of fallback model names.
  * `context_window_fallbacks` - (Optional) List of fallback model chains triggered when a context window exceeded error occurs. Each entry has the same structure as `fallbacks`.

## Attribute Reference

In addition to the arguments above, the following attributes are exported:

* `id` - The Terraform resource identifier. It always matches `team_id`.
* `team_id` - The configured or generated LiteLLM team identifier.

The following attributes are both Optional and Computed (they are read back from the API if not explicitly set):

* `metadata`
* `access_group_ids`
* `models`
* `model_aliases`
* `model_rpm_limit`
* `model_tpm_limit`
* `tags`
* `guardrails`
* `prompts`
* `blocked`
* `team_member_permissions`

## Import

Teams can be imported using the team ID:

```shell
terraform import litellm_team.example <team-id>
```

## Notes

- Team members are managed through the separate `litellm_team_member` resource. See the `litellm_team_member` resource documentation for details on managing team membership.
- The `tags` attribute requires a LiteLLM Enterprise license.
