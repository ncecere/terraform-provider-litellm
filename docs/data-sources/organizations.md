# litellm_organizations Data Source

Retrieves a list of all LiteLLM organizations.

## Example Usage

```hcl
data "litellm_organizations" "all" {}

output "org_count" {
  value = length(data.litellm_organizations.all.organizations)
}

output "org_names" {
  value = [for o in data.litellm_organizations.all.organizations : o.organization_alias]
}
```

### Filter by Alias

```hcl
data "litellm_organizations" "matching" {
  org_alias = "enterprise"
}
```

## Argument Reference

* `org_alias` - (Optional) Filter organizations by alias (partial match, case-insensitive).

## Attribute Reference

* `id` - Stable historical identifier (`organizations`), unchanged by filters.
* `organizations` - List of organization objects, each containing:
  * `organization_id` - The unique identifier.
  * `organization_alias` - The human-readable alias.
  * `max_budget` - Maximum budget.
  * `spend` - Current spend.
  * `tpm_limit` - Tokens per minute limit.
  * `rpm_limit` - Requests per minute limit.
  * `blocked` - Compatibility field reported as `false`; LiteLLM v1.98 does not return organization-level blocked state.

Results are sorted deterministically by organization ID. The v1.98 `/organization/list` endpoint is unpaginated, so the provider makes one escaped, single-snapshot request.
