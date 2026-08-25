# litellm_organization Data Source

Retrieves information about a specific LiteLLM organization.

## Example Usage

```hcl
data "litellm_organization" "existing" {
  organization_id = "org-xxxxxxxxxxxx"
}

output "org_info" {
  value = {
    alias      = data.litellm_organization.existing.organization_alias
    max_budget = data.litellm_organization.existing.max_budget
    blocked    = data.litellm_organization.existing.blocked
  }
}

# Create team within organization
resource "litellm_team" "new_team" {
  team_alias      = "new-department"
  organization_id = data.litellm_organization.existing.organization_id
  max_budget      = data.litellm_organization.existing.max_budget * 0.1
}
```

## Argument Reference

* `organization_id` - (Required) The unique identifier of the organization to retrieve.

## Attribute Reference

* `id` - The unique identifier of the organization.
* `organization_id` - The organization ID.
* `organization_alias` - The human-readable alias.
* `models` - List of models the organization can access.
* `budget_id` - Budget ID associated with this organization.
* `max_budget` - Maximum budget for the organization.
* `tpm_limit` - Tokens per minute limit.
* `rpm_limit` - Requests per minute limit.
* `model_rpm_limit` - Per-model requests per minute limits read from LiteLLM organization metadata.
* `model_tpm_limit` - Per-model tokens per minute limits read from LiteLLM organization metadata.
* `budget_duration` - Budget reset duration.
* `metadata` - Map of user metadata for the organization. The reserved per-model limit keys are exposed through their dedicated attributes instead.
* `blocked` - Whether the organization is blocked.
* `tags` - List of tags for the organization.
* `spend` - Current spend for the organization.
* `created_at` - Creation timestamp.
* `updated_at` - Last update timestamp.
