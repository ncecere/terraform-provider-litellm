# litellm_team Data Source

Retrieves information about a specific LiteLLM team.

## Example Usage

### Minimal Example

```hcl
data "litellm_team" "existing" {
  team_id = "team-xxxxxxxxxxxx"
}
```

### Full Example

```hcl
data "litellm_team" "engineering" {
  team_id = var.engineering_team_id
}

output "team_info" {
  value = {
    alias      = data.litellm_team.engineering.team_alias
    max_budget = data.litellm_team.engineering.max_budget
    spend      = data.litellm_team.engineering.spend
    models     = data.litellm_team.engineering.models
  }
}

# Use team data in key creation
resource "litellm_key" "team_key" {
  team_id    = data.litellm_team.engineering.team_id
  key_alias  = "engineering-api-key"
  max_budget = data.litellm_team.engineering.max_budget * 0.5
}
```

## Argument Reference

The following arguments are supported:

* `team_id` - (Required) The unique identifier of the team to retrieve.

## Attribute Reference

The following attributes are exported:

* `id` - The unique identifier of the team.
* `team_id` - The team ID.
* `team_alias` - The human-readable alias for the team.
* `organization_id` - The organization this team belongs to.
* `models` - List of models the team can access.
* `max_budget` - Maximum budget for the team.
* `spend` - Current spend for the team.
* `tpm_limit` - Tokens per minute limit.
* `rpm_limit` - Requests per minute limit.
* `blocked` - Whether the team is blocked.
