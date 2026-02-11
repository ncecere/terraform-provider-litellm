# litellm_team_member Resource

Manages an individual member within a LiteLLM team.

## Example Usage

```hcl
resource "litellm_team" "engineering" {
  team_alias = "engineering"
}

resource "litellm_team_member" "developer" {
  team_id    = litellm_team.engineering.id
  user_id    = "developer-1"
  user_email = "developer@example.com"
  role       = "user"
}
```

## Argument Reference

- `team_id` - (Required) The ID of the team.
- `user_id` - (Required) The ID of the user to add to the team.
- `user_email` - (Required) The email address of the user.
- `role` - (Required) The role of the user within the team. Valid values: `org_admin`, `internal_user`, `internal_user_viewer`, `admin`, `user`.
- `max_budget_in_team` - (Optional) The maximum budget allocated to this user within the team.

## Attribute Reference

- `id` - A composite ID in the format `team_id:user_id`.

## Import

Import using the composite ID:

```shell
terraform import litellm_team_member.example <team_id>:<user_id>
```
