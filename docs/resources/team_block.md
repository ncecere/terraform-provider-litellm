# litellm_team_block Resource

Manages the blocked state of a LiteLLM team. Creating this resource blocks the team and all its associated keys. Destroying it unblocks the team.

## Example Usage

```hcl
resource "litellm_team" "example" {
  team_alias = "my-team"
}

resource "litellm_team_block" "block_team" {
  team_id = litellm_team.example.id
}
```

## Argument Reference

- `team_id` - (Required, ForceNew) The ID of the team to block. Changing this forces creation of a new resource.

## Attribute Reference

- `id` - The ID of this resource.
- `blocked` - Whether the team is currently blocked.

## Import

Import using the team ID:

```shell
terraform import litellm_team_block.example <team-id>
```
