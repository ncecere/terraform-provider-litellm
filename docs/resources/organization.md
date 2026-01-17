# litellm_organization Resource

Manages a LiteLLM organization. Organizations are top-level containers that can hold multiple teams and provide budget management at the organizational level.

## Example Usage

### Minimal Example

```hcl
resource "litellm_organization" "my_org" {
  organization_alias = "my-organization"
}
```

### Full Example

```hcl
resource "litellm_organization" "enterprise" {
  organization_alias = "enterprise-org"
  max_budget         = 10000.0
  budget_duration    = "monthly"
  models             = ["gpt-4", "claude-3-sonnet"]
  tpm_limit          = 1000000
  rpm_limit          = 10000
  max_parallel_requests = 100
  
  metadata = jsonencode({
    department  = "Engineering"
    cost_center = "CC-12345"
    contact     = "admin@enterprise.com"
  })
}
```

### Organization with Teams

```hcl
resource "litellm_organization" "company" {
  organization_alias = "company-org"
  max_budget         = 5000.0
}

resource "litellm_team" "dev_team" {
  team_alias      = "development"
  organization_id = litellm_organization.company.organization_id
  max_budget      = 1000.0
}

resource "litellm_team" "prod_team" {
  team_alias      = "production"
  organization_id = litellm_organization.company.organization_id
  max_budget      = 3000.0
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `organization_alias` - (Required) A human-readable alias for the organization.

### Optional Arguments

* `max_budget` - (Optional) Maximum budget for the organization in dollars.
* `budget_duration` - (Optional) Duration for budget reset. Valid values: `daily`, `weekly`, `monthly`.
* `models` - (Optional) List of model names that this organization can access.
* `tpm_limit` - (Optional) Tokens per minute limit for the organization.
* `rpm_limit` - (Optional) Requests per minute limit for the organization.
* `max_parallel_requests` - (Optional) Maximum number of parallel requests allowed.
* `metadata` - (Optional) JSON string containing additional metadata for the organization.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The unique identifier for this organization.
* `organization_id` - The organization ID (same as id).
* `spend` - Current spend for the organization.
* `created_at` - Timestamp when the organization was created.
* `updated_at` - Timestamp when the organization was last updated.

## Import

Organizations can be imported using the organization ID:

```shell
terraform import litellm_organization.example org-xxxxxxxxxxxx
```

## Notes

- Organizations are the top-level entity in LiteLLM's hierarchy
- Teams belong to organizations
- Budget limits at the organization level apply to all teams within it
- Use metadata to store custom information like cost centers or department codes
