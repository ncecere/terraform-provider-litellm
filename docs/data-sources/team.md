# litellm_team Data Source

Retrieves information about a specific LiteLLM team.

## Example Usage

```hcl
data "litellm_team" "existing" {
  team_id = "team-xxxxxxxxxxxx"
}

output "team_info" {
  value = {
    alias      = data.litellm_team.existing.team_alias
    max_budget = data.litellm_team.existing.max_budget
    models     = data.litellm_team.existing.models
    blocked    = data.litellm_team.existing.blocked
  }
}

# Use team data in key creation
resource "litellm_key" "team_key" {
  team_id    = data.litellm_team.existing.team_id
  key_alias  = "team-api-key"
  max_budget = data.litellm_team.existing.max_budget * 0.5
}
```

## Argument Reference

* `team_id` - (Required) The unique identifier of the team to retrieve.

## Attribute Reference

* `id` - The unique identifier of the team.
* `team_id` - The team ID.
* `team_alias` - The human-readable alias for the team.
* `organization_id` - The organization this team belongs to.
* `models` - List of models the team can access.
* `max_budget` - Maximum budget for the team.
* `spend` - Current spend for the team.
* `tpm_limit` - Tokens per minute limit.
* `rpm_limit` - Requests per minute limit.
* `budget_duration` - Budget reset duration.
* `metadata` - Map of metadata for the team.
* `team_member_permissions` - List of permissions granted to team members.
* `blocked` - Whether the team is blocked.
