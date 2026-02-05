# litellm_organization Data Source

Retrieves information about a specific LiteLLM organization.

## Example Usage

### Minimal Example

```hcl
data "litellm_organization" "existing" {
  organization_id = "org-xxxxxxxxxxxx"
}
```

### Full Example

```hcl
data "litellm_organization" "company" {
  organization_id = var.org_id
}

output "org_info" {
  value = {
    alias      = data.litellm_organization.company.organization_alias
    max_budget = data.litellm_organization.company.max_budget
    spend      = data.litellm_organization.company.spend
  }
}

# Create team within organization
resource "litellm_team" "new_team" {
  team_alias      = "new-department"
  organization_id = data.litellm_organization.company.organization_id
  max_budget      = data.litellm_organization.company.max_budget * 0.1
}
```

## Argument Reference

The following arguments are supported:

* `organization_id` - (Required) The unique identifier of the organization to retrieve.

## Attribute Reference

The following attributes are exported:

* `id` - The unique identifier of the organization.
* `organization_id` - The organization ID.
* `organization_alias` - The human-readable alias.
* `max_budget` - Maximum budget for the organization.
* `spend` - Current spend for the organization.
* `budget_duration` - Budget reset duration.
* `models` - List of models the organization can access.
* `tpm_limit` - Tokens per minute limit.
* `rpm_limit` - Requests per minute limit.
* `created_at` - Creation timestamp.
* `updated_at` - Last update timestamp.
