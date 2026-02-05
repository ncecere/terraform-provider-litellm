# litellm_team_block Resource

Manages the blocked status of a LiteLLM team. This resource allows you to block or unblock an existing team.

## Example Usage

### Minimal Example

```hcl
resource "litellm_team_block" "blocked_team" {
  team_id = "team-xxxxxxxxxxxx"
}
```

### Full Example with Team Reference

```hcl
# Create a team
resource "litellm_team" "dev_team" {
  team_alias = "development-team"
  max_budget = 500.0
}

# Block the team if needed
resource "litellm_team_block" "block_dev" {
  team_id = litellm_team.dev_team.team_id
}
```

## Argument Reference

The following arguments are supported:

### Required Arguments

* `team_id` - (Required) The ID of the team to block.

## Attribute Reference

In addition to all arguments above, the following attributes are exported:

* `id` - The ID of the team block resource (same as team_id).

## Import

Team blocks can be imported using the team ID:

```shell
terraform import litellm_team_block.example team-xxxxxxxxxxxx
```

## Notes

- Blocking a team prevents all team members from using the API
- This resource creates a "block" action - removing the resource will unblock the team
- All API keys associated with the team will also be effectively blocked
