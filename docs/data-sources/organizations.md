# litellm_organizations Data Source

Retrieves a list of all LiteLLM organizations.

## Example Usage

### Minimal Example

```hcl
data "litellm_organizations" "all" {}
```

### Full Example

```hcl
data "litellm_organizations" "all" {}

output "org_count" {
  value = length(data.litellm_organizations.all.organizations)
}

output "org_names" {
  value = [for o in data.litellm_organizations.all.organizations : o.organization_alias]
}

# Calculate total spend across all organizations
locals {
  total_org_spend = sum([for o in data.litellm_organizations.all.organizations : o.spend])
}

output "total_spend" {
  value = local.total_org_spend
}
```

## Argument Reference

This data source has no required arguments.

## Attribute Reference

The following attributes are exported:

* `id` - Placeholder identifier.
* `organizations` - List of organization objects, each containing:
  * `organization_id` - The unique identifier.
  * `organization_alias` - The human-readable alias.
  * `max_budget` - Maximum budget.
  * `spend` - Current spend.
  * `created_at` - Creation timestamp.
  * `updated_at` - Last update timestamp.
