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

### Lossless structured metadata

Use `metadata_json` when team metadata contains native booleans, numbers, nulls, arrays, or nested objects. The legacy `metadata` map remains `map(string)` and retains its existing behavior.

```hcl
resource "litellm_team" "structured" {
  team_alias = "structured-metadata"

  metadata = {
    owner = "platform"
  }

  metadata_json = jsonencode({
    deployment = {
      production = true
      revision   = 9007199254740993
      owner      = null
      regions    = ["us-east", "us-west"]
      options    = {}
    }
  })
}
```

`metadata_json` is sensitive and owns only its recursively configured JSON leaves. It cannot overlap top-level keys in `metadata` or the dedicated `tags`, `guardrails`, `prompts`, `model_rpm_limit`, `model_tpm_limit`, `rpm_limit_type`, and `tpm_limit_type` roots. The server-owned `team_member_budget_id` root is also reserved. Arrays, scalars, null, and empty objects are atomic leaves. Terraform null leaves the semantic sibling unmanaged; `{}` manages an explicitly empty semantic object.

Before a metadata update, the provider performs one fresh exact-identity read, removes previously owned leaves, overlays the configured legacy, dedicated, and semantic values, and sends the complete metadata column. Unowned API siblings are preserved. Interrupted updates retain prior state and value-free recovery metadata until a later refresh confirms the complete new or prior shape; partial transitions fail closed.

LiteLLM stores sensitive callback variables as ciphertext. The provider can preserve exact owned callback leaves from prior plaintext state, but it blocks complete metadata replacement when unowned or structurally ambiguous ciphertext would need to be replayed. Avoid managing the same `logging` or `callback_settings` subtree through both this resource and LiteLLM callback endpoints.

LiteLLM owns `team_member_budget_id`. If that root exists remotely, a metadata update is allowed only when the same team update includes a non-null member-default value that makes LiteLLM reinsert the relation. The provider never sends the ID as caller metadata and blocks metadata updates combined only with member-default clears.

LiteLLM v1.98 exposes no ETag, revision, or compare-and-swap field for team metadata. A concurrent writer can therefore win or be overwritten between hydration and update. Post-write verification detects divergence but cannot eliminate that bounded last-writer-wins window.

Imports and upgraded states leave `metadata_json` null and unmanaged. Explicit configuration performs takeover on a later apply. Team data sources do not expose a semantic sibling because doing so would persist arbitrary API-owned metadata into Terraform state.

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
* `budget_duration` - (Optional) Recurring team budget reset interval. Use a positive integer followed by `s`, `m`, `h`, `d`, or `w` (for example, `30d`, `24h`, or `1w`); one of the exact aliases `hourly`, `daily`, `weekly`, or `monthly`; or exactly `1mo`. Zero values, other month counts such as `2mo` or `12mo`, case variants, and malformed aliases or units are rejected.
* `tpm_limit` - (Optional) Tokens per minute limit for the team.
* `rpm_limit` - (Optional) Requests per minute limit for the team.
* `tpm_limit_type` - (Optional, Forces replacement) Create-only TPM limit type. LiteLLM v1.98 accepts exactly `"guaranteed_throughput"` or `"best_effort_throughput"` when creating a team. Adding, changing, or removing it replaces the team.
* `rpm_limit_type` - (Optional, Forces replacement) Create-only RPM limit type. LiteLLM v1.98 accepts exactly `"guaranteed_throughput"` or `"best_effort_throughput"` when creating a team. Adding, changing, or removing it replaces the team.
* `models` - (Optional) List of model names the team is allowed to use.
* `blocked` - (Optional) Whether the team is blocked from making requests.
* `guardrails` - (Optional) List of guardrail identifiers applied to the team.
* `prompts` - (Optional) List of prompt identifiers associated with the team.
* `team_member_permissions` - (Optional) List of permissions granted to team members.
* `team_member_budget` - (Optional) Default budget for each team member.
* `team_member_budget_duration` - (Optional) Recurring reset interval for the default member budget. Use a positive integer followed by `s`, `m`, `h`, `d`, or `w` (for example, `30d`, `24h`, or `1w`); one of the exact aliases `hourly`, `daily`, `weekly`, or `monthly`; or exactly `1mo`. Zero values, other month counts such as `2mo` or `12mo`, case variants, and malformed aliases or units are rejected. LiteLLM applies it to new/default membership budgets and may backfill memberships without a budget; private member overrides are preserved. Configure `litellm_team_member.budget_duration` for per-member control.
* `team_member_rpm_limit` - (Optional) Default requests per minute limit for each team member.
* `team_member_tpm_limit` - (Optional) Default tokens per minute limit for each team member.
* `metadata` - (Optional) Legacy metadata map. Values are strings; use `jsonencode()` for historically supported objects and arrays.
* `metadata_json` - (Optional, Sensitive) Non-null JSON object for lossless heterogeneous metadata. Its top-level keys cannot overlap legacy, dedicated, or server-owned metadata roots.
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
* `metadata_json`
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

Teams can be imported using the team ID. Imports never adopt remote values into `metadata_json`; it remains null until explicitly configured.

```shell
terraform import litellm_team.example <team-id>
```

## Notes

- LiteLLM v1.98 permits a nullable `team_alias`, but this resource intentionally retains its existing required, non-null Terraform contract. Import and refresh therefore require the remote team to have an alias; a null or omitted alias fails safely instead of retaining stale state.
- Team metadata is selectively owned. The provider preserves API-managed and unconfigured metadata keys while reading configured keys authoritatively. Fields represented by dedicated arguments (including tags, guardrails, prompts, per-model limits, and limit types) are read from their native LiteLLM v1.98 metadata locations rather than copied into the generic `metadata` map.
- Earlier provider documentation suggested `"key"` and `"team"` for the limit-type attributes. LiteLLM v1.98 rejects those values in `NewTeamRequest`, and `UpdateTeamRequest` has no limit-type fields. The attributes are therefore create-only and force replacement when added, changed, or removed. Existing imported/read state is not rewritten by schema validation, but explicit configuration must use one of the two supported throughput literals.
- Team members are managed through the separate `litellm_team_member` resource. See the `litellm_team_member` resource documentation for details on managing team membership.
- The `tags` attribute requires a LiteLLM Enterprise license.
