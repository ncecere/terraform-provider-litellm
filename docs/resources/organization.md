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

### Lossless structured metadata

Use `metadata_json` when organization metadata contains native booleans, numbers, nulls, arrays, or nested objects. The legacy `metadata` map remains `map(string)` and retains its existing behavior.

```hcl
resource "litellm_organization" "structured" {
  # Semantic metadata requires a caller-selected identity so an accepted create
  # can be recovered after response loss.
  organization_id    = "org-production-platform"
  organization_alias = "production-platform"

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

`metadata_json` is sensitive and owns only its recursively configured JSON leaves. It cannot overlap top-level keys in `metadata` or the dedicated `model_rpm_limit` and `model_tpm_limit` roots. Arrays, scalars, null, and empty objects are atomic leaves. Terraform null leaves the semantic sibling unmanaged; `{}` manages an explicitly empty semantic object.

Before a metadata update, the provider performs one fresh exact-identity read, removes previously owned leaves, overlays the configured legacy, dedicated, and semantic values, and sends the complete metadata column. Unowned API siblings are preserved. If removal readback is interrupted, prior state and value-free recovery metadata are retained until a later refresh confirms either the complete new shape or the complete prior shape; partial transitions fail closed.

LiteLLM v1.98 exposes no ETag, revision, or compare-and-swap field for organization metadata. A concurrent writer can therefore win or be overwritten between hydration and PATCH. Post-write verification detects divergence but cannot eliminate that bounded last-writer-wins window.

Imports and upgraded states leave `metadata_json` null and unmanaged. Explicit configuration performs takeover on a later apply. Organization data sources do not expose a semantic sibling because doing so would persist arbitrary API-owned metadata, including potential credentials, into Terraform state.

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
- `model_rpm_limit` - (Map of Int64) Per-model RPM limits stored in organization metadata.
- `model_tpm_limit` - (Map of Int64) Per-model TPM limits stored in organization metadata.
- `budget_duration` - (String) Reset duration such as `"30d"`, `"1h"`, or `"7d"`.
- `metadata` - (Map of String) Organization metadata. Use `jsonencode()` for complex object or array values; scalar-looking strings retain string identity.
- `metadata_json` - (String, Sensitive) Non-null JSON object for lossless heterogeneous metadata. Top-level keys cannot overlap `metadata`, `model_rpm_limit`, or `model_tpm_limit`. Configuring it during create requires `organization_id`.
- `blocked` - (Bool, Deprecated) Compatibility-only. `false` is a no-op; new `true` configuration is rejected.
- `tags` - (List of String, Deprecated) Compatibility-only. `[]` is a no-op; new non-empty configuration is rejected.

## Attribute Reference

- `id` - Organization ID, equal to `organization_id`.
- `created_at` - Creation timestamp.

## Import

```shell
terraform import litellm_organization.example <organization-id>
```

The first authoritative import read adopts visible nested budget values, including `budget_id` and exact integer limits above `2^53`. The imported `budget_id` may remain omitted from configuration without producing a plan. After it is explicitly configured and successfully applied, even to the same value, its import omission permission is consumed and later omission is rejected as an unsupported configured removal. Normal lifecycle reads do not adopt unconfigured API defaults. Imports never adopt remote values into `metadata_json`; it remains null until explicitly configured.

## Budget and Drift Semantics

- LiteLLM v1.98 returns organization budget controls through `litellm_budget_table`; similarly named top-level fields are not authoritative. Structured `model_max_budget` is deferred because its GenericBudgetConfig values cannot be represented accurately as `map(float64)`.
- Configured/imported budget values detect out-of-band changes and explicit remote nulls. Removing or changing a configured `budget_id` is rejected; an import-provenance marker permits omission only for an imported association.
- An existing `budget_id` cannot be combined with budget limits or duration during create because v1.98 strips or ignores those controls against the shared budget. An absent or null relation clears owned state; malformed relations fail without publishing partial state.
- Scalar and duration removal uses v1.98's transactional `/v2/organization/{id}` merge-patch endpoint with explicit `null` clears. Duration changes also recompute, or clear, the server reset timestamp.
- Per-model RPM/TPM keys use the same endpoint's complete metadata replacement, allowing owned keys to clear without replacing the organization. Unrelated metadata already visible in state is preserved.
- The provider never replaces or deletes an organization merely to clear a budget or metadata key; organization deletion cascades to dependent teams, memberships, and keys.
- `budget_reset_at` is server-managed and is not exposed by the v1.98 organization response model. The provider initializes it when a configured duration is created and updates it when duration changes.
