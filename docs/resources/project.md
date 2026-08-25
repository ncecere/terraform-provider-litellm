# litellm_project (Resource)

Manages a LiteLLM Project. Projects sit between teams and keys, providing fine-grained model, budget, rate, and blocking controls.

## Example Usage

### Minimal Project

```hcl
resource "litellm_project" "minimal" {
  team_id = litellm_team.example.id
}
```

### Full Project

```hcl
resource "litellm_project" "full" {
  project_alias = "production-api"
  description   = "Production API project"
  team_id       = litellm_team.platform.id

  models                = ["gpt-4o", "gpt-4o-mini"]
  tags                  = ["production", "platform"]
  max_budget            = 1000.0
  soft_budget           = 800.0
  budget_duration       = "30d"
  tpm_limit             = 100000
  rpm_limit             = 1000
  max_parallel_requests = 25
  blocked               = false

  metadata = {
    environment = "production"
    cost_center = "engineering"
  }

  model_rpm_limit = {
    "gpt-4o"      = 500
    "gpt-4o-mini" = 1000
  }

  model_tpm_limit = {
    "gpt-4o"      = 50000
    "gpt-4o-mini" = 100000
  }
}
```

## Argument Reference

- `team_id` - (Required, ForceNew) Parent team ID.
- `project_alias` - (Optional) Human-friendly project name.
- `description` - (Optional) Project description.
- `models` - (Optional List of String) Accessible models. Configure `[]` to clear.
- `metadata` - (Optional Map of String) Metadata; use `jsonencode()` for complex values.
- `tags` - (Optional List of String) Tags. LiteLLM v1.98 stores them in project metadata, and the provider reads that location authoritatively.
- `max_budget` - (Optional Float64) Hard budget limit.
- `soft_budget` - (Optional Float64) Alert threshold.
- `budget_duration` - (Optional String) Reset duration such as `"30d"` or `"1h"`.
- `budget_id` - (Optional String) Existing budget to associate during creation. Reassociation after creation is blocked because v1.98 cannot converge it safely.
- `tpm_limit` - (Optional Int64) Tokens-per-minute limit.
- `rpm_limit` - (Optional Int64) Requests-per-minute limit.
- `max_parallel_requests` - (Optional Int64) Concurrent request limit.
- `model_max_budget` - (Optional Map of Float64) Legacy schema-compatible shape. Existing scalar-map state remains readable and can be cleared, but new non-empty additions and changes are rejected because LiteLLM v1.98 requires structured GenericBudgetConfig objects.
- `model_rpm_limit` - (Optional Map of Int64) Per-model RPM limits stored in metadata.
- `model_tpm_limit` - (Optional Map of Int64) Per-model TPM limits stored in metadata.
- `blocked` - (Optional Bool) Whether the project is blocked.

## Attribute Reference

- `id` - Project ID.
- `created_at` / `updated_at` - Creation and update timestamps.
- `created_by` / `updated_by` - Creating and last-updating users.

## Import

```shell
terraform import litellm_project.example <project-id>
```

The first authoritative import read adopts visible nested budget values, including `budget_id`, remote aliases/descriptions, and exact integer limits above `2^53`. Imported `budget_id`, `project_alias`, and `description` values may remain omitted without producing a plan. Normal reads do not adopt unconfigured API defaults.

## Budget, Clear, and Partial-Failure Semantics

- LiteLLM v1.98 returns project budget controls through `litellm_budget_table`; similarly named top-level fields are ignored. Unconfigured structured `model_max_budget` values are not exposed through the legacy `map(float64)` attribute.
- Configured/imported values detect out-of-band drift. Removing or changing a configured `budget_id` is rejected; import provenance permits omission only for an imported association.
- An existing `budget_id` cannot be combined with budget limits, duration, or `model_max_budget` during create because v1.98 strips or ignores those controls against the shared budget. A null or absent relation clears owned state; malformed relations and mismatched budget identities fail without publishing partial state.
- Project-row fields and budget-row fields use separate v1.98 endpoints. Budget removals send explicit nulls through `/budget/update`; clearing `budget_duration` also clears the server-managed reset timestamp.
- `budget_reset_at` is not exposed by the v1.98 project response model. The provider initializes it after create and updates or clears it with duration changes.
- Metadata, tags, and per-model RPM/TPM limits are replaced as one authoritative metadata document, so owned keys can be removed safely.
- v1.98 ignores null clears for `project_alias` and `description`; the provider rejects explicit configured removals instead of claiming success. Import provenance allows remotely adopted values to remain omitted. Replace configured values with a non-null value.
- If LiteLLM accepts a project-row update but a subsequent budget update fails or returns the wrong budget identity, Terraform retains prior state and reports the partial failure. A later apply safely retries the idempotent desired values.
