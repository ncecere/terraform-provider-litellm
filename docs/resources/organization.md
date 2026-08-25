# litellm_organization (Resource)

Manages a LiteLLM organization. Organizations group teams and users under shared model access and budget controls.

## Example Usage

### Minimal Configuration

```hcl
resource "litellm_organization" "minimal" {
  organization_alias = "my-organization"
}
```

### Full Supported Configuration

```hcl
resource "litellm_organization" "full" {
  organization_alias   = "enterprise-org"
  models               = ["gpt-4o", "gpt-4o-mini"]
  max_budget           = 1000.0
  soft_budget          = 800.0
  tpm_limit            = 200000
  rpm_limit            = 2000
  max_parallel_requests = 50
  budget_duration      = "30d"

  metadata = {
    environment = "production"
  }

  model_rpm_limit = {
    "gpt-4o" = 1000
  }

  model_tpm_limit = {
    "gpt-4o" = 100000
  }
}
```

### Deprecated Compatibility Defaults

```hcl
resource "litellm_organization" "legacy_compatible" {
  organization_alias = "legacy-compatible"

  # Accepted as deprecated no-ops for old configurations only.
  blocked = false
  tags    = []
}
```

Do not configure `blocked = true` or non-empty `tags`. LiteLLM v1.98 has no organization columns that persist those values, and the provider rejects new non-default values rather than reporting false success. Use project/team blocking and organization `metadata` as appropriate.

## Argument Reference

### Required

- `organization_alias` - (String) Human-readable organization alias.

### Optional

- `organization_id` - (String, ForceNew) Caller-selected ID. LiteLLM generates one when omitted.
- `models` - (List of String) Models the organization may use. Configure `[]` to clear the list.
- `budget_id` - (String) Existing budget to use during creation. Reassociating an existing organization is blocked because v1.98 has no safe convergent reassociation lifecycle.
- `max_budget` - (Float64) Hard budget limit.
- `soft_budget` - (Float64) Budget alert threshold.
- `tpm_limit` - (Int64) Tokens-per-minute limit.
- `rpm_limit` - (Int64) Requests-per-minute limit.
- `max_parallel_requests` - (Int64) Concurrent request limit.
- `model_max_budget` - (Map of Float64) Legacy schema-compatible per-model budget shape. LiteLLM v1.98 validates non-empty API values as structured GenericBudgetConfig objects; use with care pending structured-budget schema support.
- `model_rpm_limit` - (Map of Int64) Per-model RPM limits stored in organization metadata.
- `model_tpm_limit` - (Map of Int64) Per-model TPM limits stored in organization metadata.
- `budget_duration` - (String) Reset duration such as `"30d"`, `"1h"`, or `"7d"`.
- `metadata` - (Map of String) Organization metadata. Use `jsonencode()` for complex values.
- `blocked` - (Bool, Deprecated) Compatibility-only. `false` is a no-op; new `true` configuration is rejected.
- `tags` - (List of String, Deprecated) Compatibility-only. `[]` is a no-op; new non-empty configuration is rejected.

## Attribute Reference

- `id` - Organization ID, equal to `organization_id`.
- `created_at` - Creation timestamp.

## Import

```shell
terraform import litellm_organization.example <organization-id>
```

The first authoritative import read adopts visible nested budget values, including exact integer limits above `2^53`. Normal lifecycle reads do not adopt unconfigured API defaults.

## Budget and Drift Semantics

- LiteLLM v1.98 returns organization budget controls through `litellm_budget_table`; similarly named top-level fields are not authoritative.
- Configured/imported budget values detect out-of-band changes and explicit remote nulls. An absent or null relation clears owned state; malformed relations fail without publishing partial state.
- Scalar and duration removal uses v1.98's transactional `/v2/organization/{id}` merge-patch endpoint with explicit `null` clears. Duration changes also recompute, or clear, the server reset timestamp.
- Per-model RPM/TPM keys use the same endpoint's complete metadata replacement, allowing owned keys to clear without replacing the organization. Unrelated metadata already visible in state is preserved.
- The provider never replaces or deletes an organization merely to clear a budget or metadata key; organization deletion cascades to dependent teams, memberships, and keys.
- `budget_reset_at` is server-managed and is not exposed by the v1.98 organization response model. The provider initializes it when a configured duration is created and updates it when duration changes.
