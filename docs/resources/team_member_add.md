# litellm_team_member_add Resource

Manages a batch of members within a LiteLLM team. Adding or removing `member` blocks will add or remove members from the team.

## Example Usage

```hcl
resource "litellm_team" "engineering" {
  team_alias = "engineering"
}

resource "litellm_team_member_add" "batch" {
  team_id = litellm_team.engineering.id

  member {
    user_email = "dev1@example.com"
    role       = "user"
  }

  member {
    user_email = "lead@example.com"
    role       = "admin"
  }

  max_budget_in_team = 100.0
}
```

## Argument Reference

- `team_id` - (Required, ForceNew) The ID of the team. Changing this forces creation of a new resource.
- `max_budget_in_team` - (Optional) The maximum budget allocated to members within the team.

### member Block

One or more `member` blocks are supported:

- `user_id` - (Optional) The ID of the user to add.
- `user_email` - (Optional) The email address of the user to add.
- `role` - (Required) The role of the user within the team. Valid values: `admin`, `user`.

## Attribute Reference

- `id` - The ID of this resource.

## Import

Import using the team ID:

```shell
terraform import litellm_team_member_add.example <team-id>
```
