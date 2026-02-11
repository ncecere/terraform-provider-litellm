# litellm_organization (Resource)

Manages organizations in LiteLLM. Organizations provide a way to group teams and users together under a shared budget and set of permissions.

## Example Usage

### Minimal Configuration

```hcl
resource "litellm_organization" "minimal" {
  organization_alias = "my-organization"
}
```

### Full Configuration

```hcl
resource "litellm_organization" "full" {
  organization_alias = "enterprise-org"
  max_budget         = 1000.0
  tpm_limit          = 200000
  rpm_limit          = 2000
  budget_duration    = "30d"
  blocked            = false

  models = ["gpt-4o", "gpt-4o-mini"]
  tags   = ["testing", "full"]

  metadata = {
    "environment" = "testing"
  }

  model_rpm_limit = {
    "gpt-4o" = 1000
  }

  model_tpm_limit = {
    "gpt-4o" = 100000
  }
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

### Required

- `organization_alias` - (String) A human-readable alias for the organization. Must be unique.

### Optional

- `organization_id` - (String, ForceNew) The unique identifier for the organization. If not provided, one will be generated automatically. Changing this forces creation of a new resource.
- `models` - (List of String) List of model names the organization is allowed to use.
- `budget_id` - (String) The ID of an existing budget to associate with this organization.
- `max_budget` - (Float64) Maximum budget allowed for the organization.
- `tpm_limit` - (Int64) Tokens per minute limit for the organization.
- `rpm_limit` - (Int64) Requests per minute limit for the organization.
- `model_rpm_limit` - (Map of Int64) Per-model requests per minute limits.
- `model_tpm_limit` - (Map of Int64) Per-model tokens per minute limits.
- `budget_duration` - (String) Duration of the budget window (e.g., `"30d"`, `"1h"`, `"7d"`).
- `metadata` - (Map of String) Key-value metadata associated with the organization.
- `blocked` - (Bool) Whether the organization is blocked from making requests.
- `tags` - (List of String) Tags associated with the organization.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

- `id` - The unique identifier for the organization (same as `organization_id`).
- `created_at` - Timestamp of when the organization was created.

## Import

Organizations can be imported using their organization ID:

```shell
terraform import litellm_organization.example <organization-id>
```

## Notes

- Organizations are the top-level entity in LiteLLM's hierarchy.
- Teams belong to organizations.
- Budget limits at the organization level apply to all teams within it.
- The `metadata` attribute is a map of strings, not a JSON-encoded string.
