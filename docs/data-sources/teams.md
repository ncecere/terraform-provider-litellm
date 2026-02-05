# litellm_teams Data Source

Retrieves a list of LiteLLM teams.

## Example Usage

### Minimal Example

```hcl
data "litellm_teams" "all" {}
```

### Filter by Organization

```hcl
data "litellm_teams" "org_teams" {
  organization_id = "org-xxxxxxxxxxxx"
}
```

### Full Example

```hcl
data "litellm_teams" "all" {}

output "team_count" {
  value = length(data.litellm_teams.all.teams)
}

# Calculate total spend across all teams
locals {
  total_spend = sum([for t in data.litellm_teams.all.teams : t.spend])
}

output "total_team_spend" {
  value = local.total_spend
}

# Find teams over budget
locals {
  over_budget_teams = [
    for t in data.litellm_teams.all.teams : t
    if t.spend > t.max_budget * 0.9
  ]
}

output "teams_near_budget" {
  value = [for t in local.over_budget_teams : t.team_alias]
}
```

## Argument Reference

The following arguments are supported:

* `organization_id` - (Optional) Filter teams by organization ID.

## Attribute Reference

The following attributes are exported:

* `id` - Placeholder identifier.
* `teams` - List of team objects, each containing:
  * `team_id` - The unique identifier.
  * `team_alias` - The human-readable alias.
  * `organization_id` - Associated organization ID.
  * `max_budget` - Maximum budget.
  * `spend` - Current spend.
  * `tpm_limit` - Tokens per minute limit.
  * `rpm_limit` - Requests per minute limit.
  * `blocked` - Whether the team is blocked.
