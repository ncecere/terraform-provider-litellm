# litellm_organizations Data Source

Retrieves LiteLLM organizations with authoritative nested budget inventories.

## Example Usage

```hcl
data "litellm_organizations" "all" {}

output "organization_budgets" {
  value = {
    for organization in data.litellm_organizations.all.organizations :
    organization.organization_id => organization.max_budget
  }
}
```

### Filter by Alias

```hcl
data "litellm_organizations" "matching" {
  org_alias = "enterprise"
}
```

## Argument Reference

- `org_alias` - (Optional String) Partial, case-insensitive alias filter.

## Attribute Reference

- `id` - Stable historical identifier `organizations`.
- `organizations` - Deterministically ID-sorted objects containing:
  - `organization_id` / `organization_alias`
  - `budget_id`
  - `max_budget` / `soft_budget`
  - `tpm_limit` / `rpm_limit`
  - `max_parallel_requests`
  - `model_max_budget`
  - `model_rpm_limit` / `model_tpm_limit`
  - `budget_duration`
  - `spend`
  - `blocked` - Compatibility value `false`; v1.98 has no organization blocked column.

Every budget value is decoded from each object's `litellm_budget_table`. Null/absent relations produce null inventory values; malformed relations or mismatched identities fail the whole snapshot instead of returning a partial list. Exact integer limits above `2^53` are preserved.
