# Resource: litellm_team_member_add

Add multiple members to a team with a single resource.

## Example Usage

### Basic Usage

```hcl
resource "litellm_team_member_add" "example" {
  team_id = "team-123"
  
  member {
    user_id = "user-456"
    role    = "admin"
  }

  member {
    user_email = "user@example.com"
    role       = "user"
  }

  max_budget_in_team = 100.0
}
```

### Dynamic Members Using Locals

```hcl
locals {
  team_members = [
    {
      user_id  = "user-123"
      role     = "admin"
    },
    {
      user_email = "developer1@company.com"
      role      = "user"
    },
    {
      user_email = "developer2@company.com"
      role      = "user"
    }
  ]
}

resource "litellm_team_member_add" "dynamic_example" {
  team_id = "team-456"
  
  dynamic "member" {
    for_each = local.team_members
    content {
      user_id    = lookup(member.value, "user_id", null)
      user_email = lookup(member.value, "user_email", null)
      role       = member.value.role
    }
  }

  max_budget_in_team = 200.0
}
```

## Argument Reference

* `team_id` - (Required) The ID of the team to add members to.
* `member` - (Required) One or more member blocks defining team members. Each block supports:
  * `user_id` - (Optional) The ID of the user to add to the team. Either `user_id` or `user_email` must be provided.
  * `user_email` - (Optional) The email of the user to add to the team. Either `user_id` or `user_email` must be provided.
  * `role` - (Required) The role of the user in the team. Must be one of: `admin` or `user`.
* `max_budget_in_team` - (Optional) The maximum budget allocated for the team members.

## Attribute Reference

In addition to the arguments above, the following attributes are exported:

* `id` - The unique identifier for the team member add resource. This is set to the team_id.

## Import

Team member add resources can be imported using the team ID:

```shell
terraform import litellm_team_member_add.example <team_id>
```

## Notes

- Either `user_id` or `user_email` must be provided for each member, but not both.
- If a user doesn't exist, a new user row will be added to the User Table.
- Only proxy_admin or admin of the team are allowed to access this endpoint.
- This resource manages multiple team members as a single unit. For individual team member management, use the `litellm_team_member` resource instead.
